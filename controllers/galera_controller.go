/*
Copyright 2022.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package controllers

import (
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	configmap "github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	env "github.com/openstack-k8s-operators/lib-common/modules/common/env"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	commonstatefulset "github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/util/podutils"

	"bytes"
	"context"
	"fmt"
	"golang.org/x/exp/maps"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg"
)

// GaleraReconciler reconciles a Galera object
type GaleraReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	config  *rest.Config
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

// findBestCandidate returns the node with the lowest seqno
func findBestCandidate(status *mariadbv1.GaleraStatus) string {
	sortednodes := maps.Keys(status.Attributes)
	sort.Strings(sortednodes)
	bestnode := ""
	bestseqno := -1
	for _, node := range sortednodes {
		seqno := status.Attributes[node].Seqno
		intseqno, _ := strconv.Atoi(seqno)
		if intseqno >= bestseqno {
			bestnode = node
			bestseqno = intseqno
		}
	}
	return bestnode //"galera-0"
}

// buildGcommURI builds a gcomm URI for a galera instance
// e.g. "gcomm://galera-0.galera,galera-1.galera,galera-2.galera"
func buildGcommURI(instance *mariadbv1.Galera) string {
	replicas := int(instance.Spec.Replicas)
	basename := instance.Name + "-galera"
	res := []string{}

	for i := 0; i < replicas; i++ {
		res = append(res, basename+"-"+strconv.Itoa(i)+"."+basename)
	}
	uri := "gcomm://" + strings.Join(res, ",")
	return uri
}

// injectGcommURI configures a pod to start galera with a given URI
func injectGcommURI(h *helper.Helper, config *rest.Config, instance *mariadbv1.Galera, pod string, uri string) error {
	err := mariadb.ExecInPod(h, config, instance.Namespace, pod, "galera",
		[]string{"/bin/bash", "-c", "echo '" + uri + "' > /var/lib/mysql/gcomm_uri"},
		func(stdout *bytes.Buffer, stderr *bytes.Buffer) error {
			attr := instance.Status.Attributes[pod]
			attr.Gcomm = uri
			instance.Status.Attributes[pod] = attr
			return nil
		})
	return err
}

// retrieveSequenceNumber probes a pod's galera instance for sequence number
func retrieveSequenceNumber(helper *helper.Helper, config *rest.Config, instance *mariadbv1.Galera, pod string) error {
	err := mariadb.ExecInPod(helper, config, instance.Namespace, pod, "galera",
		[]string{"/bin/bash", "/var/lib/operator-scripts/detect_last_commit.sh"},
		func(stdout *bytes.Buffer, stderr *bytes.Buffer) error {
			seqno := strings.TrimSuffix(stdout.String(), "\n")
			attr := mariadbv1.GaleraAttributes{
				Seqno: seqno,
			}
			instance.Status.Attributes[pod] = attr
			return nil
		})
	return err
}

// RBAC for galera resources
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/finalizers,verbs=update

// RBAC for statefulsets
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get;list;watch

// RBAC for pods
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

// RBAC for services and endpoints
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=endpoints,verbs=get;list;watch;create;update;patch;delete;

// RBAC for configmaps
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;

// Reconcile - Galera
func (r *GaleraReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	_ = log.FromContext(ctx)

	// Fetch the Galera instance
	instance := &mariadbv1.Galera{}
	err := r.Get(ctx, req.NamespacedName, instance)
	if err != nil {
		if k8s_errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return ctrl.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return ctrl.Result{}, err
	}

	helper, err := helper.NewHelper(
		instance,
		r.Client,
		r.Kclient,
		r.Scheme,
		r.Log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Always patch the instance status when exiting this function so we can persist any changes.
	defer func() {
		// Update the overall status condition if service is ready
		if instance.IsReady() {
			instance.Status.Conditions.MarkTrue(condition.ReadyCondition, condition.ReadyMessage)
		}

		err := helper.PatchInstance(ctx, instance)
		if err != nil {
			_err = err
			return
		}
	}()

	//
	// initialize status
	//
	if instance.Status.Attributes == nil {
		instance.Status.Attributes = make(map[string]mariadbv1.GaleraAttributes)
	}
	if instance.Status.Conditions == nil {
		instance.Status.Conditions = condition.Conditions{}
		// initialize conditions used later as Status=Unknown
		cl := condition.CreateList(
			// endpoint for adoption redirect
			condition.UnknownCondition(condition.ExposeServiceReadyCondition, condition.InitReason, condition.ExposeServiceReadyInitMessage),
			// configmap generation
			condition.UnknownCondition(condition.ServiceConfigReadyCondition, condition.InitReason, condition.ServiceConfigReadyInitMessage),
			// cluster bootstrap
			condition.UnknownCondition(condition.DeploymentReadyCondition, condition.InitReason, condition.DeploymentReadyInitMessage),
		)

		instance.Status.Conditions.Init(&cl)

		// Register overall status immediately to have an early feedback e.g. in the cli
		return ctrl.Result{}, nil
	}

	//
	// Create/Update all the resources associated to this galera instance
	//

	adoption := &instance.Spec.AdoptionRedirect

	// Endpoints
	endpoints := mariadb.EndpointsForAdoption(instance, adoption)
	if endpoints != nil {
		op, err := controllerutil.CreateOrPatch(ctx, r.Client, endpoints, func() error {
			err := controllerutil.SetControllerReference(instance, endpoints, r.Scheme)
			if err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.ExposeServiceReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.ExposeServiceReadyErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		if op != controllerutil.OperationResultNone {
			r.Log.Info(fmt.Sprintf("%s %s database endpoints %s - operation: %s", instance.Kind, instance.Name, endpoints.Name, string(op)))
		}
	}

	instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)

	// the headless service provides DNS entries for pods
	// the name of the resource must match the name of the app selector
	pkghl := mariadb.HeadlessService(instance)
	headless := &corev1.Service{ObjectMeta: pkghl.ObjectMeta}
	op, err := controllerutil.CreateOrPatch(ctx, r.Client, headless, func() error {
		headless.Spec = pkghl.Spec
		err := controllerutil.SetOwnerReference(instance, headless, r.Client.Scheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("%s %s database headless service %s - operation: %s", instance.Kind, instance.Name, headless.Name, string(op)))
	}

	pkgsvc := mariadb.ServiceForAdoption(instance, "galera", adoption)
	service := &corev1.Service{ObjectMeta: pkgsvc.ObjectMeta}
	op, err = controllerutil.CreateOrPatch(ctx, r.Client, service, func() error {
		service.Spec = pkgsvc.Spec
		err := controllerutil.SetOwnerReference(instance, service, r.Client.Scheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		r.Log.Info(fmt.Sprintf("%s %s database service %s - operation: %s", instance.Kind, instance.Name, service.Name, string(op)))
	}

	// Generate the config maps for the various services
	configMapVars := make(map[string]env.Setter)
	err = r.generateConfigMaps(ctx, helper, instance, &configMapVars)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %v", err)
	}
	// From hereon, configMapVars holds a hash of the config generated for this instance
	// This is used in an envvar in the statefulset to restart it on config change
	envHash := &corev1.EnvVar{}
	configMapVars[configMapNameForConfig(instance)](envHash)
	instance.Status.ConfigHash = envHash.Value
	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	commonstatefulset := commonstatefulset.NewStatefulSet(mariadb.StatefulSet(instance), 5)
	sfres, sferr := commonstatefulset.CreateOrPatch(ctx, helper)
	if sferr != nil {
		return sfres, sferr
	}
	statefulset := commonstatefulset.GetStatefulSet()

	// Retrieve pods managed by the associated statefulset
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
		client.MatchingLabels(mariadb.StatefulSetLabels(instance)),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		util.LogErrorForObject(helper, err, "Failed to list pods", instance)
		return ctrl.Result{}, err
	}

	//
	// Reconstruct the state of the galera resource based on the replicaset and its pods
	//

	// Ensure status is cleaned up in case of scale down
	if *statefulset.Spec.Replicas < statefulset.Status.Replicas {
		removeOldAttributesOnScaleDown(helper, instance)
	}

	// Note:
	//   . A pod is available in the statefulset if the pod's readiness
	//     probe returns true (i.e. galera is running in the pod and clustered)
	//   . Cluster is bootstrapped if as soon as one pod is available
	instance.Status.Bootstrapped = statefulset.Status.AvailableReplicas > 0

	if instance.Status.Bootstrapped {
		// Sync Ready condition
		instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)

		// All pods that are 'Ready' have their local galera running and connected.
		// We can clear their attributes from our status as these are now outdated.
		for _, pod := range getReadyPods(podList.Items) {
			name := pod.Name
			if _, found := instance.Status.Attributes[name]; found {
				util.LogForObject(helper, "Galera started on pod "+pod.Name, instance)
				delete(instance.Status.Attributes, name)
			}
		}

		// The other 'Running' pods can join the existing cluster.
		for _, pod := range getRunningPodsMissingGcomm(podList.Items, instance) {
			name := pod.Name
			joinerURI := buildGcommURI(instance)
			util.LogForObject(helper, "Pushing gcomm URI to joiner", instance, "pod", name)
			// Setting the gcomm attribute marks this pod as 'currently joining the cluster'
			err := injectGcommURI(helper, r.config, instance, name, joinerURI)
			if err != nil {
				util.LogErrorForObject(helper, err, "Failed to push gcomm URI", instance, "pod", name)
				return ctrl.Result{}, err
			}
		}
	}

	// If the cluster is not running, probes the available pods for seqno
	// to determine the bootstrap node.
	// Note:
	//   . a pod whose phase is Running but who is not Ready hasn't started
	//     galera, it is waiting for the operator's instructions.
	//     We can record its galera's seqno in our status.
	//   . any other status means the the pod is starting/restarting. We can't
	//     exec into the pod yet, so we will probe it in another reconcile loop.
	if !instance.Status.Bootstrapped && !isBootstrapInProgress(instance) {
		for _, pod := range getRunningPodsMissingAttributes(podList.Items, instance) {
			name := pod.Name
			util.LogForObject(helper, fmt.Sprintf("Pod %s running, retrieve seqno", name), instance)
			err := retrieveSequenceNumber(helper, r.config, instance, name)
			if err != nil {
				util.LogErrorForObject(helper, err, "Failed to retrieve seqno for "+name, instance)
				return ctrl.Result{}, err
			}
			util.LogForObject(helper, fmt.Sprintf("Pod %s seqno: %s", name, instance.Status.Attributes[name].Seqno), instance)
		}

		// Check if we have enough info to bootstrap the cluster now
		if len(podList.Items) == len(instance.Status.Attributes) {
			node := findBestCandidate(&instance.Status)
			util.LogForObject(helper, "Pushing gcomm URI to bootstrap", instance, "pod", node)
			// Setting the gcomm attribute marks this pod as 'currently bootstrapping the cluster'
			err := injectGcommURI(helper, r.config, instance, node, "gcomm://")
			if err != nil {
				util.LogErrorForObject(helper, err, "Failed to push gcomm URI", instance, "pod", node)
				return ctrl.Result{}, err
			}
		}
	}

	// The statefulset usually instantiates the pods instantly, and the galera
	// operator doesn't receive individual events for pod's phase transition or
	// readiness, as it is not controlling the pods (the statefulset is).
	// So until all pods become available, we have to requeue this event to get
	// a chance to react to all pod's transitions.
	if statefulset.Status.AvailableReplicas != statefulset.Status.Replicas {
		util.LogForObject(helper, "Requeuing until all replicas are available", instance)
		return ctrl.Result{RequeueAfter: time.Duration(3) * time.Second}, nil
	}

	return ctrl.Result{}, err
}

// configMapNameForScripts - name of the configmap that holds the
// used by the statefulset associated to a galera CR
func configMapNameForScripts(instance *mariadbv1.Galera) string {
	return fmt.Sprintf("%s-scripts", instance.Name)
}

// configMapNameForConfig - name of the configmap that holds configuration
// files injected into pods associated to a galera CR
func configMapNameForConfig(instance *mariadbv1.Galera) string {
	return fmt.Sprintf("%s-config-data", instance.Name)
}

// generateConfigMaps returns the config map resource for a galera instance
func (r *GaleraReconciler) generateConfigMaps(
	ctx context.Context,
	h *helper.Helper,
	instance *mariadbv1.Galera,
	envVars *map[string]env.Setter,
) error {
	templateParameters := make(map[string]interface{})
	customData := make(map[string]string)
	customData[mariadbv1.CustomServiceConfigFile] = instance.Spec.CustomServiceConfig

	cms := []util.Template{
		// ScriptsConfigMap
		{
			Name:         configMapNameForScripts(instance),
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeScripts,
			InstanceType: instance.Kind,
			Labels:       map[string]string{},
		},
		// ConfigMap
		{
			Name:          configMapNameForConfig(instance),
			Namespace:     instance.Namespace,
			Type:          util.TemplateTypeConfig,
			InstanceType:  instance.Kind,
			CustomData:    customData,
			ConfigOptions: templateParameters,
			Labels:        map[string]string{},
		},
	}

	err := configmap.EnsureConfigMaps(ctx, h, instance, cms, envVars)
	if err != nil {
		util.LogErrorForObject(h, err, "Unable to retrieve or create config maps", instance)
		return err
	}

	return nil
}

// getReadyPods returns all the pods in Running phase, filtered by Ready state
func getReadyPods(pods []corev1.Pod) (ret []corev1.Pod) {
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning && podutils.IsPodReady(&pod) {
			ret = append(ret, pod)
		}
	}
	return
}

// getRunningPodsMissingAttributes returns all the pods without seqno information
func getRunningPodsMissingAttributes(pods []corev1.Pod, instance *mariadbv1.Galera) (ret []corev1.Pod) {
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning {
			if _, found := instance.Status.Attributes[pod.Name]; !found {
				ret = append(ret, pod)
			}
		}
	}
	return
}

// getRunningPodsMissingGcomm returns all the pods without seqno information
func getRunningPodsMissingGcomm(pods []corev1.Pod, instance *mariadbv1.Galera) (ret []corev1.Pod) {
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning && !podutils.IsPodReady(&pod) {
			if _, found := instance.Status.Attributes[pod.Name]; found {
				if instance.Status.Attributes[pod.Name].Gcomm == "" {
					ret = append(ret, pod)
				}
			} else {
				ret = append(ret, pod)
			}
		}
	}
	return
}

// isBootstrapInProgress checks whether a node is currently starting a galera cluster
func isBootstrapInProgress(instance *mariadbv1.Galera) bool {
	for _, attr := range instance.Status.Attributes {
		if attr.Gcomm == "gcomm://" {
			return true
		}
	}
	return false
}

// isBootstrapInProgress checks whether a node is currently starting a galera cluster
func removeOldAttributesOnScaleDown(helper *helper.Helper, instance *mariadbv1.Galera) {
	replicas := int(instance.Spec.Replicas)

	// a pod's name is built as 'statefulsetname-n'
	for node := range instance.Status.Attributes {
		parts := strings.Split(node, "-")
		index, _ := strconv.Atoi(parts[len(parts)-1])
		if index >= replicas {
			delete(instance.Status.Attributes, node)
			util.LogForObject(helper, "Remove old node from status after scale-down", instance, "node", node)
		}
	}
}

// SetupWithManager sets up the controller with the Manager.
func (r *GaleraReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.config = mgr.GetConfig()
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.Galera{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Endpoints{}).
		Owns(&corev1.ConfigMap{}).
		Complete(r)
}
