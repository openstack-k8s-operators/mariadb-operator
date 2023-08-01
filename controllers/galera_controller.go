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
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	commonstatefulset "github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/util/podutils"

	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/exp/maps"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg/mariadb"
)

// GaleraReconciler reconciles a Galera object
type GaleraReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	config  *rest.Config
	Log     logr.Logger
	Scheme  *runtime.Scheme
}

///
// General Galera helper functions
//

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
	replicas := int(*instance.Spec.Replicas)
	basename := instance.Name + "-galera"
	res := []string{}

	for i := 0; i < replicas; i++ {
		res = append(res, basename+"-"+strconv.Itoa(i)+"."+basename)
	}
	uri := "gcomm://" + strings.Join(res, ",")
	return uri
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

///
// Reconcile logics helper functions
//

// getPodFromName returns the pod object from a pod name
func getPodFromName(pods []corev1.Pod, name string) *corev1.Pod {
	for _, pod := range pods {
		if pod.Name == name {
			return &pod
		}
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

// getRunningPodsMissingAttributes returns all the pods for which the operator
// has no seqno information, and which are ready for being inspected.
// Note: a pod is considered 'ready for inspection' when its main container is
// started and its inner process is currently waiting for a gcomm URI
// (i.e. it is not running mysqld)
func getRunningPodsMissingAttributes(ctx context.Context, pods []corev1.Pod, instance *mariadbv1.Galera, h *helper.Helper, config *rest.Config) (ret []corev1.Pod) {
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning && !podutils.IsPodReady(&pod) {
			_, attrFound := instance.Status.Attributes[pod.Name]
			if !attrFound && isGaleraContainerStartedAndWaiting(ctx, &pod, instance, h, config) {
				ret = append(ret, pod)
			}
		}
	}
	return
}

// getRunningPodsMissingGcomm returns all the pods which are not running galera
// yet but are ready to join the cluster.
// Note: a pod is considered 'ready to join' when its main container is
// started and its inner process is currently waiting for a gcomm URI
// (i.e. it is not running mysqld)
func getRunningPodsMissingGcomm(ctx context.Context, pods []corev1.Pod, instance *mariadbv1.Galera, h *helper.Helper, config *rest.Config) (ret []corev1.Pod) {
	for _, pod := range pods {
		if pod.Status.Phase == corev1.PodRunning && !podutils.IsPodReady(&pod) &&
			isGaleraContainerStartedAndWaiting(ctx, &pod, instance, h, config) {
			if _, attrFound := instance.Status.Attributes[pod.Name]; attrFound {
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

// isGaleraContainerStartedAndWaiting checks whether the galera container is waiting for a gcomm_uri file
func isGaleraContainerStartedAndWaiting(ctx context.Context, pod *corev1.Pod, instance *mariadbv1.Galera, h *helper.Helper, config *rest.Config) bool {
	waiting := false
	err := mariadb.ExecInPod(ctx, h, config, instance.Namespace, pod.Name, "galera",
		[]string{"/bin/bash", "-c", "test ! -f /var/lib/mysql/gcomm_uri && pgrep -aP1 | grep -o detect_gcomm_and_start.sh"},
		func(stdout *bytes.Buffer, stderr *bytes.Buffer) error {
			predicate := strings.TrimSuffix(stdout.String(), "\n")
			waiting = (predicate == "detect_gcomm_and_start.sh")
			return nil
		})
	return err == nil && waiting
}

///
// Status management helper functions
// These functions have side effect and modify the galera CR's status
//

// injectGcommURI configures a pod to start galera with a given URI
func injectGcommURI(ctx context.Context, h *helper.Helper, config *rest.Config, instance *mariadbv1.Galera, pod *corev1.Pod, uri string) error {
	err := mariadb.ExecInPod(ctx, h, config, instance.Namespace, pod.Name, "galera",
		[]string{"/bin/bash", "-c", "echo '" + uri + "' > /var/lib/mysql/gcomm_uri"},
		func(stdout *bytes.Buffer, stderr *bytes.Buffer) error {
			attr := instance.Status.Attributes[pod.Name]
			attr.Gcomm = uri
			attr.ContainerID = pod.Status.ContainerStatuses[0].ContainerID
			instance.Status.Attributes[pod.Name] = attr
			return nil
		})
	return err
}

// retrieveSequenceNumber probes a pod's galera instance for sequence number
func retrieveSequenceNumber(ctx context.Context, helper *helper.Helper, config *rest.Config, instance *mariadbv1.Galera, pod *corev1.Pod) error {
	err := mariadb.ExecInPod(ctx, helper, config, instance.Namespace, pod.Name, "galera",
		[]string{"/bin/bash", "/var/lib/operator-scripts/detect_last_commit.sh"},
		func(stdout *bytes.Buffer, stderr *bytes.Buffer) error {
			seqno := strings.TrimSuffix(stdout.String(), "\n")
			attr := mariadbv1.GaleraAttributes{
				Seqno: seqno,
			}
			instance.Status.Attributes[pod.Name] = attr
			return nil
		})
	return err
}

// clearPodAttributes clears information known by the operator about a pod
func clearPodAttributes(instance *mariadbv1.Galera, podName string) {
	delete(instance.Status.Attributes, podName)
	// If the pod was deemed safeToBootstrap, this state has to be reassessed
	if instance.Status.SafeToBootstrap == podName {
		instance.Status.SafeToBootstrap = ""
	}
}

// clearOldPodsAttributesOnScaleDown removes known information from old pods
// that no longer exist after a scale down of the galera CR
func clearOldPodsAttributesOnScaleDown(helper *helper.Helper, instance *mariadbv1.Galera) {
	replicas := int(*instance.Spec.Replicas)

	// a pod's name is built as 'statefulsetname-n'
	for node := range instance.Status.Attributes {
		parts := strings.Split(node, "-")
		index, _ := strconv.Atoi(parts[len(parts)-1])
		if index >= replicas {
			clearPodAttributes(instance, node)
			util.LogForObject(helper, "Remove old pod from status after scale-down", instance, "pod", node)
		}
	}
}

// assertPodsAttributesValidity compares the current state of the pods that are starting galera
// against their known state in the CR's attributes. If a pod's attributes don't match its actual
// state (i.e. it failed to start galera), the attributes are cleared from the CR's status
func assertPodsAttributesValidity(helper *helper.Helper, instance *mariadbv1.Galera, pods []corev1.Pod) {
	for _, pod := range pods {
		_, found := instance.Status.Attributes[pod.Name]
		if !found {
			continue
		}
		ci := instance.Status.Attributes[pod.Name].ContainerID
		pci := pod.Status.ContainerStatuses[0].ContainerID
		if ci != pci {
			// This gcomm URI was pushed in a pod which was restarted
			// before the attribute got cleared, which means the pod
			// failed to start galera. Clear the attribute here, and
			// reprobe the pod's state in the next reconcile loop
			clearPodAttributes(instance, pod.Name)
			util.LogForObject(helper, "Pod restarted while galera was starting", instance, "pod", pod.Name)
		}
	}
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

// RBAC permissions to create service accounts, roles, role bindings
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update

// RBAC required to grant the service account role these capabilities
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch

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
		// update the Ready condition based on the sub conditions
		if instance.Status.Conditions.AllSubConditionIsTrue() {
			instance.Status.Conditions.MarkTrue(
				condition.ReadyCondition, condition.ReadyMessage)
		} else {
			// something is not ready so reset the Ready condition
			instance.Status.Conditions.MarkUnknown(
				condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage)
			// and recalculate it based on the state of the rest of the conditions
			instance.Status.Conditions.Set(
				instance.Status.Conditions.Mirror(condition.ReadyCondition))
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
			// service account, role, rolebinding
			condition.UnknownCondition(condition.ServiceAccountReadyCondition, condition.InitReason, condition.ServiceAccountReadyInitMessage),
			condition.UnknownCondition(condition.RoleReadyCondition, condition.InitReason, condition.RoleReadyInitMessage),
			condition.UnknownCondition(condition.RoleBindingReadyCondition, condition.InitReason, condition.RoleBindingReadyInitMessage),
		)

		instance.Status.Conditions.Init(&cl)

		// Register overall status immediately to have an early feedback e.g. in the cli
		return ctrl.Result{}, nil
	}

	//
	// Service account, role, binding
	//
	rbacRules := []rbacv1.PolicyRule{
		{
			APIGroups:     []string{"security.openshift.io"},
			ResourceNames: []string{"anyuid"},
			Resources:     []string{"securitycontextconstraints"},
			Verbs:         []string{"use"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"pods"},
			Verbs:     []string{"create", "get", "list", "watch", "update", "patch", "delete"},
		},
	}
	rbacResult, err := common_rbac.ReconcileRbac(ctx, helper, instance, rbacRules)
	if err != nil {
		return rbacResult, err
	} else if (rbacResult != ctrl.Result{}) {
		return rbacResult, nil
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
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %w", err)
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
		clearOldPodsAttributesOnScaleDown(helper, instance)
	}

	// Ensure that all the ongoing galera start actions are still running
	assertPodsAttributesValidity(helper, instance, podList.Items)

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
				clearPodAttributes(instance, name)
			}
		}

		// The other 'Running' pods can join the existing cluster.
		for _, pod := range getRunningPodsMissingGcomm(ctx, podList.Items, instance, helper, r.config) {
			name := pod.Name
			joinerURI := buildGcommURI(instance)
			util.LogForObject(helper, "Pushing gcomm URI to joiner", instance, "pod", name)
			// Setting the gcomm attribute marks this pod as 'currently joining the cluster'
			err := injectGcommURI(ctx, helper, r.config, instance, &pod, joinerURI)
			if err != nil {
				util.LogErrorForObject(helper, err, "Failed to push gcomm URI", instance, "pod", name)
				// A failed injection likely means the pod's status has changed.
				// drop it from status and reprobe it in another reconcile loop
				clearPodAttributes(instance, name)
				return ctrl.Result{}, err
			}
		}
	}

	// If the cluster is not running, probe the available pods for seqno
	// to determine the bootstrap node.
	// Note:
	//   . a pod whose phase is Running but who is not Ready hasn't started
	//     galera, it is waiting for the operator's instructions.
	//     We can record its galera's seqno in our status.
	//   . any other status means the the pod is starting/restarting. We can't
	//     exec into the pod yet, so we will probe it in another reconcile loop.
	if !instance.Status.Bootstrapped && !isBootstrapInProgress(instance) {
		for _, pod := range getRunningPodsMissingAttributes(ctx, podList.Items, instance, helper, r.config) {
			name := pod.Name
			util.LogForObject(helper, fmt.Sprintf("Pod %s running, retrieve seqno", name), instance)
			err := retrieveSequenceNumber(ctx, helper, r.config, instance, &pod)
			if err != nil {
				util.LogErrorForObject(helper, err, "Failed to retrieve seqno for "+name, instance)
				return ctrl.Result{}, err
			}
			util.LogForObject(helper, fmt.Sprintf("Pod %s seqno: %s", name, instance.Status.Attributes[name].Seqno), instance)
		}

		// Check if we have enough info to bootstrap the cluster now
		if (len(instance.Status.Attributes) > 0) &&
			(len(instance.Status.Attributes) == len(podList.Items)) {
			node := findBestCandidate(&instance.Status)
			pod := getPodFromName(podList.Items, node)
			util.LogForObject(helper, "Pushing gcomm URI to bootstrap", instance, "pod", node)
			// Setting the gcomm attribute marks this pod as 'currently bootstrapping the cluster'
			err := injectGcommURI(ctx, helper, r.config, instance, pod, "gcomm://")
			if err != nil {
				util.LogErrorForObject(helper, err, "Failed to push gcomm URI", instance, "pod", node)
				// A failed injection likely means the pod's status has changed.
				// drop it from status and reprobe it in another reconcile loop
				clearPodAttributes(instance, node)
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
			Name:         fmt.Sprintf("%s-scripts", instance.Name),
			Namespace:    instance.Namespace,
			Type:         util.TemplateTypeScripts,
			InstanceType: instance.Kind,
			Labels:       map[string]string{},
		},
		// ConfigMap
		{
			Name:          fmt.Sprintf("%s-config-data", instance.Name),
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

// SetupWithManager sets up the controller with the Manager.
func (r *GaleraReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.config = mgr.GetConfig()
	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.Galera{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Endpoints{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Complete(r)
}
