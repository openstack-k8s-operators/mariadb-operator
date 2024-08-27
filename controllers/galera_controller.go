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
	"bytes"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/openstack-k8s-operators/lib-common/modules/common"
	condition "github.com/openstack-k8s-operators/lib-common/modules/common/condition"
	configmap "github.com/openstack-k8s-operators/lib-common/modules/common/configmap"
	env "github.com/openstack-k8s-operators/lib-common/modules/common/env"
	helper "github.com/openstack-k8s-operators/lib-common/modules/common/helper"
	common_rbac "github.com/openstack-k8s-operators/lib-common/modules/common/rbac"
	secret "github.com/openstack-k8s-operators/lib-common/modules/common/secret"
	"github.com/openstack-k8s-operators/lib-common/modules/common/service"
	commonstatefulset "github.com/openstack-k8s-operators/lib-common/modules/common/statefulset"
	"github.com/openstack-k8s-operators/lib-common/modules/common/tls"
	util "github.com/openstack-k8s-operators/lib-common/modules/common/util"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	k8s_errors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/kubectl/pkg/util/podutils"

	"golang.org/x/exp/maps"

	"github.com/go-logr/logr"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/builder"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	databasev1beta1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	mariadb "github.com/openstack-k8s-operators/mariadb-operator/pkg/mariadb"
)

// fields to index to reconcile on CR change
const (
	serviceSecretNameField = ".spec.tls.genericService.SecretName"
	caSecretNameField      = ".spec.tls.ca.caBundleSecretName"
)

var allWatchFields = []string{
	serviceSecretNameField,
	caSecretNameField,
}

// GaleraReconciler reconciles a Galera object
type GaleraReconciler struct {
	client.Client
	Kclient kubernetes.Interface
	config  *rest.Config
	Scheme  *runtime.Scheme
}

// GetLog returns a logger object with a prefix of "controller.name" and additional controller context fields
func GetLog(ctx context.Context, controller string) logr.Logger {
	return log.FromContext(ctx).WithName("Controllers").WithName(controller)
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
		// Generate Gcomm with subdomains for TLS validation
		res = append(res, basename+"-"+strconv.Itoa(i)+"."+basename+"."+instance.Namespace+".svc")
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
		func(stdout *bytes.Buffer, _ *bytes.Buffer) error {
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
		func(_ *bytes.Buffer, _ *bytes.Buffer) error {
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
		func(stdout *bytes.Buffer, _ *bytes.Buffer) error {
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
func clearOldPodsAttributesOnScaleDown(ctx context.Context, instance *mariadbv1.Galera) {
	log := GetLog(ctx, "galera")
	replicas := int(*instance.Spec.Replicas)

	// a pod's name is built as 'statefulsetname-n'
	for node := range instance.Status.Attributes {
		parts := strings.Split(node, "-")
		index, _ := strconv.Atoi(parts[len(parts)-1])
		if index >= replicas {
			clearPodAttributes(instance, node)
			log.Info("Remove old pod from status after scale-down", "instance", instance, "pod", node)
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
		// A node can have various attributes depending on its known state.
		// A ContainerID attribute is only present if the node is being started.
		attrCID := instance.Status.Attributes[pod.Name].ContainerID
		podCID := pod.Status.ContainerStatuses[0].ContainerID
		if attrCID != "" && attrCID != podCID {
			// This gcomm URI was pushed in a pod which was restarted
			// before the attribute got cleared, which means the pod
			// failed to start galera. Clear the attribute here, and
			// reprobe the pod's state in the next reconcile loop
			clearPodAttributes(instance, pod.Name)
			util.LogForObject(helper, "Pod restarted while galera was starting", instance, "pod", pod.Name, "current pod ID", podCID, "recorded ID", attrCID)
		}
	}
}

// RBAC for galera resources
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=mariadb.openstack.org,resources=galeras/finalizers,verbs=update;patch

// RBAC for statefulsets
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets/status,verbs=get;list;watch

// RBAC for pods
// +kubebuilder:rbac:groups=core,resources=pods,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=pods/exec,verbs=create

// RBAC for secrets
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete;

// RBAC for services and endpoints
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete;
// +kubebuilder:rbac:groups=core,resources=endpoints,verbs=get;list;watch;create;update;patch;delete;

// RBAC for configmaps
// +kubebuilder:rbac:groups=core,resources=configmaps,verbs=get;list;watch;create;update;patch;delete;

// RBAC permissions to create service accounts, roles, role bindings
// +kubebuilder:rbac:groups="",resources=serviceaccounts,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=roles,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="rbac.authorization.k8s.io",resources=rolebindings,verbs=get;list;watch;create;update;patch

// RBAC required to grant the service account role these capabilities
// +kubebuilder:rbac:groups="security.openshift.io",resourceNames=anyuid,resources=securitycontextconstraints,verbs=use
// +kubebuilder:rbac:groups="",resources=pods,verbs=create;delete;get;list;patch;update;watch

// +kubebuilder:rbac:groups=core,resources=persistentvolumeclaims,verbs=get;list;watch;create;update;delete;patch

// Reconcile - Galera
func (r *GaleraReconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, _err error) {
	log := GetLog(ctx, "galera")

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
		log,
	)
	if err != nil {
		return ctrl.Result{}, err
	}

	// initialize status if Conditions is nil, but do not reset if it already
	// exists
	isNewInstance := instance.Status.Conditions == nil
	if isNewInstance {
		instance.Status.Conditions = condition.Conditions{}
	}

	// Save a copy of the condtions so that we can restore the LastTransitionTime
	// when a condition's state doesn't change.
	savedConditions := instance.Status.Conditions.DeepCopy()

	// Always patch the instance status when exiting this function so we can
	// persist any changes.
	defer func() {
		condition.RestoreLastTransitionTimes(
			&instance.Status.Conditions, savedConditions)
		if instance.Status.Conditions.IsUnknown(condition.ReadyCondition) {
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
	// initialize conditions used later as Status=Unknown
	cl := condition.CreateList(
		condition.UnknownCondition(condition.ReadyCondition, condition.InitReason, condition.ReadyInitMessage),
		// DB Root password
		condition.UnknownCondition(condition.InputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
		// TLS cert secrets
		condition.UnknownCondition(condition.TLSInputReadyCondition, condition.InitReason, condition.InputReadyInitMessage),
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
	instance.Status.ObservedGeneration = instance.Generation

	// If we're not deleting this and the service object doesn't have our finalizer, add it.
	if instance.DeletionTimestamp.IsZero() && controllerutil.AddFinalizer(instance, helper.GetFinalizer()) || isNewInstance {
		return ctrl.Result{}, err
	}

	// Handle service delete
	if !instance.DeletionTimestamp.IsZero() {
		return r.reconcileDelete(ctx, instance, helper)
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
		{
			APIGroups: []string{""},
			Resources: []string{"services"},
			Verbs:     []string{"get", "list", "update", "patch"},
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
			log.Info("", "Kind", instance.Kind, "Name", instance.Name, "database endpoints", endpoints.Name, "operation:", string(op))
		}
	}

	instance.Status.Conditions.MarkTrue(condition.ExposeServiceReadyCondition, condition.ExposeServiceReadyMessage)

	// the headless service provides DNS entries for pods
	// the name of the resource must match the name of the app selector
	pkghl := mariadb.HeadlessService(instance)
	headless := &corev1.Service{ObjectMeta: pkghl.ObjectMeta}
	op, err := controllerutil.CreateOrPatch(ctx, r.Client, headless, func() error {
		headless.Spec = pkghl.Spec
		err := controllerutil.SetControllerReference(instance, headless, r.Client.Scheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("", "Kind", instance.Kind, "Name", instance.Name, "database headless service", headless.Name, "operation", string(op))
	}

	pkgsvc := mariadb.ServiceForAdoption(instance, "galera", adoption)
	service := &corev1.Service{ObjectMeta: pkgsvc.ObjectMeta}
	op, err = controllerutil.CreateOrPatch(ctx, r.Client, service, func() error {
		// Add finalizer to the svc to prevent deletion. If the svc gets deleted
		// and re-created it will receive a new ClusterIP and connection from
		// service get stuck.
		controllerutil.AddFinalizer(service, helper.GetFinalizer())

		if !instance.Spec.EnableMultiMaster {
			// NOTE(dciabrin) We deploy Galera as an A/P service (i.e. no multi-master writes)
			// by setting labels in the service's label selectors.
			// This label is dynamically set based on the status of the Galera cluster,
			// so in this CreateOrPatch block we must reuse whatever is present in
			// the existing service CR in case we're patching it.
			activePod, present := service.Spec.Selector[mariadb.ActivePodSelectorKey]
			service.Spec = pkgsvc.Spec

			if present {
				service.Spec.Selector[mariadb.ActivePodSelectorKey] = activePod
			}
		} else {
			service.Spec = pkgsvc.Spec
			delete(service.Spec.Selector, mariadb.ActivePodSelectorKey)
		}

		err := controllerutil.SetControllerReference(instance, service, r.Client.Scheme())
		if err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return ctrl.Result{}, err
	}
	if op != controllerutil.OperationResultNone {
		log.Info("", "Kind", instance.Kind, "Name", instance.Name, "database service", service.Name, "operation", string(op))
	}

	// Map of all resources that may cause a rolling service restart
	inputHashEnv := make(map[string]env.Setter)

	// Map of all cluster properties that require a full service restart
	clusterPropertiesEnv := make(map[string]env.Setter)

	// Check and hash inputs
	secretName := instance.Spec.Secret
	// NOTE do not hash the db root password, as its change requires
	// more orchestration than a simple rolling restart
	_, _, err = secret.GetSecret(ctx, helper, secretName, instance.Namespace)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.InputReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.InputReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error calculating input hash: %w", err)
	}
	instance.Status.Conditions.MarkTrue(condition.InputReadyCondition, condition.InputReadyMessage)

	//
	// TLS input validation
	//
	// Validate the CA cert secret if provided
	if instance.Spec.TLS.CaBundleSecretName != "" {
		hash, err := tls.ValidateCACertSecret(
			ctx,
			helper.GetClient(),
			types.NamespacedName{
				Name:      instance.Spec.TLS.CaBundleSecretName,
				Namespace: instance.Namespace,
			},
		)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					fmt.Sprintf(condition.TLSInputReadyWaitingMessage, instance.Spec.TLS.CaBundleSecretName)))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}

		if hash != "" {
			inputHashEnv["CA"] = env.SetValue(hash)
		}
	}

	// Validate service cert secret
	if instance.Spec.TLS.Enabled() {
		hash, err := instance.Spec.TLS.ValidateCertSecret(ctx, helper, instance.Namespace)
		if err != nil {
			if k8s_errors.IsNotFound(err) {
				instance.Status.Conditions.Set(condition.FalseCondition(
					condition.TLSInputReadyCondition,
					condition.RequestedReason,
					condition.SeverityInfo,
					fmt.Sprintf(condition.TLSInputReadyWaitingMessage, err.Error())))
				return ctrl.Result{}, nil
			}
			instance.Status.Conditions.Set(condition.FalseCondition(
				condition.TLSInputReadyCondition,
				condition.ErrorReason,
				condition.SeverityWarning,
				condition.TLSInputErrorMessage,
				err.Error()))
			return ctrl.Result{}, err
		}
		inputHashEnv["Cert"] = env.SetValue(hash)
	}
	// all cert input checks out so report InputReady
	instance.Status.Conditions.MarkTrue(condition.TLSInputReadyCondition, condition.InputReadyMessage)

	// Generate and hash config maps
	err = r.generateConfigMaps(ctx, helper, instance, &inputHashEnv)
	if err != nil {
		instance.Status.Conditions.Set(condition.FalseCondition(
			condition.ServiceConfigReadyCondition,
			condition.ErrorReason,
			condition.SeverityWarning,
			condition.ServiceConfigReadyErrorMessage,
			err.Error()))
		return ctrl.Result{}, fmt.Errorf("error calculating configmap hash: %w", err)
	}
	instance.Status.Conditions.MarkTrue(condition.ServiceConfigReadyCondition, condition.ServiceConfigReadyMessage)

	// build state of the restart hash. this is used to decide whether the
	// statefulset must stop all its pods before applying a config update
	clusterPropertiesEnv["GCommTLS"] = env.SetValue(strconv.FormatBool(instance.Spec.TLS.Enabled() && instance.Spec.TLS.Ca.CaBundleSecretName != ""))
	clusterPropertiesHash, err := util.HashOfInputHashes(clusterPropertiesEnv)
	if err != nil {
		return ctrl.Result{}, err
	}
	inputHashEnv["ClusterProperties"] = env.SetValue(clusterPropertiesHash)

	//
	// create hash over all the different input resources to identify if has changed
	// and a restart/recreate is required.
	//
	hashOfHashes, err := util.HashOfInputHashes(inputHashEnv)
	if err != nil {
		return ctrl.Result{}, err
	}

	// Update ClusterProperties here, as from this point we are sure we can update
	// both `ClusterProperties` and `StopRequired` in this reconcile loop.
	instance.Status.ClusterProperties = make(map[string]string)
	for k, s := range clusterPropertiesEnv {
		var envVar corev1.EnvVar
		s(&envVar)
		instance.Status.ClusterProperties[k] = envVar.Value
	}

	// check whether we need to stop the cluster after a cluster-wide change
	if oldPropertiesHash, exists := instance.Status.Hash["ClusterProperties"]; exists {
		if oldPropertiesHash != clusterPropertiesHash {
			util.LogForObject(helper, fmt.Sprintf("ClusterProperties changed (%#v -> %#v), cluster restart required", oldPropertiesHash, clusterPropertiesHash), instance)
			instance.Status.StopRequired = true
			// Do not return here, let the return happen after the other
			// config hashes get updated due to this hash change
		}
	}

	if hashMap, changed := util.SetHash(instance.Status.Hash, common.InputHashName, hashOfHashes); changed {
		// Hash changed and instance status should be updated (which will be done by main defer func),
		// so update all the input hashes and return to reconcile again
		instance.Status.Hash = hashMap
		for k, s := range inputHashEnv {
			var envVar corev1.EnvVar
			s(&envVar)
			instance.Status.Hash[k] = envVar.Value
		}
		util.LogForObject(helper, fmt.Sprintf("Input hash changed %s", hashOfHashes), instance)
		return ctrl.Result{}, nil
	}

	commonstatefulset := commonstatefulset.NewStatefulSet(mariadb.StatefulSet(instance, hashOfHashes), 5)
	sfres, sferr := commonstatefulset.CreateOrPatch(ctx, helper)
	if sferr != nil {
		if k8s_errors.IsNotFound(sferr) {
			return ctrl.Result{RequeueAfter: time.Duration(3) * time.Second}, nil
		}
		return sfres, sferr
	}

	// util.LogForObject(helper, fmt.Sprintf("DAM BEFORE %v - AFTER %v", helper.GetBefore(), helper.GetAfter()), instance)

	statefulset := commonstatefulset.GetStatefulSet()

	// If a full cluster restart was requested,
	// check whether it is still in progress
	if instance.Status.StopRequired && statefulset.Status.Replicas == 0 {
		util.LogForObject(helper, "Full cluster restart finished, config update can now proceed", instance)
		instance.Status.StopRequired = false
		// return now to force the next reconcile to reconfigure
		// the statefulset to recreate the pods
		return ctrl.Result{}, nil
	}

	// Retrieve pods managed by the associated statefulset
	podList := &corev1.PodList{}
	listOpts := []client.ListOption{
		client.InNamespace(instance.Namespace),
		client.MatchingLabels(mariadb.StatefulSetLabels(instance)),
	}
	if err = r.List(ctx, podList, listOpts...); err != nil {
		log.Error(err, "Failed to list pods")
		return ctrl.Result{}, err
	}

	//
	// Reconstruct the state of the galera resource based on the replicaset and its pods
	//

	// Ensure status is cleaned up in case of scale down
	if *statefulset.Spec.Replicas < statefulset.Status.Replicas {
		clearOldPodsAttributesOnScaleDown(ctx, instance)
	}

	// Ensure that all the ongoing galera start actions are still running
	assertPodsAttributesValidity(helper, instance, podList.Items)

	// Note:
	//   . A pod is available in the statefulset if the pod's readiness
	//     probe returns true (i.e. galera is running in the pod and clustered)
	//   . Cluster is bootstrapped as soon as one pod is available
	instance.Status.Bootstrapped = statefulset.Status.AvailableReplicas > 0

	if instance.Status.Bootstrapped {
		// Sync Ready condition
		instance.Status.Conditions.MarkTrue(condition.DeploymentReadyCondition, condition.DeploymentReadyMessage)

		// All pods that are 'Ready' have their local galera running and connected.
		// We can clear their attributes from our status as these are now outdated.
		for _, pod := range getReadyPods(podList.Items) {
			name := pod.Name
			if _, found := instance.Status.Attributes[name]; found {
				log.Info("Galera started on", "pod", pod.Name)
				clearPodAttributes(instance, name)
			}
		}

		runningPods := getRunningPodsMissingGcomm(ctx, podList.Items, instance, helper, r.config)
		// Special case for 1-node deployment: if the statefulset reports 1 node is available
		// but the pod shows up in runningPods (i.e. NotReady), do not consider it a joiner.
		// Wait for the two statuses to re-sync after another k8s probe is run.
		if *instance.Spec.Replicas == 1 && len(runningPods) == 1 {
			log.Info("Galera node no longer running. Requeuing")
			return ctrl.Result{RequeueAfter: time.Duration(3) * time.Second}, nil
		}

		// The other 'Running' pods can join the existing cluster.
		for _, pod := range runningPods {
			name := pod.Name
			joinerURI := buildGcommURI(instance)
			log.Info("Pushing gcomm URI to joiner", "pod", name)
			// Setting the gcomm attribute marks this pod as 'currently joining the cluster'
			err := injectGcommURI(ctx, helper, r.config, instance, &pod, joinerURI)
			if err != nil {
				log.Error(err, "Failed to push gcomm URI", "pod", name)
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
				log.Error(err, "Failed to retrieve seqno for ", "name", name)
				return ctrl.Result{}, err
			}
			log.Info("", "Pod", name, "seqno:", instance.Status.Attributes[name].Seqno)
		}

		// Check if we have enough info to bootstrap the cluster now
		if (len(instance.Status.Attributes) > 0) &&
			(len(instance.Status.Attributes) == len(podList.Items)) {
			node := findBestCandidate(&instance.Status)
			pod := getPodFromName(podList.Items, node)
			log.Info("Pushing gcomm URI to bootstrap", "pod", node)
			// Setting the gcomm attribute marks this pod as 'currently bootstrapping the cluster'
			err := injectGcommURI(ctx, helper, r.config, instance, pod, "gcomm://")
			if err != nil {
				log.Error(err, "Failed to push gcomm URI", "pod", node)
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
		log.Info("Requeuing until all replicas are available")
		return ctrl.Result{RequeueAfter: time.Duration(3) * time.Second}, nil
	}

	// We reached the end of the Reconcile, update the Ready condition based on
	// the sub conditions
	if instance.Status.Conditions.AllSubConditionIsTrue() {
		instance.Status.Conditions.MarkTrue(
			condition.ReadyCondition, condition.ReadyMessage)
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
	log := GetLog(ctx, "galera")
	templateParameters := map[string]interface{}{
		"logToDisk": instance.Spec.LogToDisk,
	}
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
		log.Error(err, "Unable to retrieve or create config maps")
		return err
	}

	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GaleraReconciler) SetupWithManager(mgr ctrl.Manager) error {
	r.config = mgr.GetConfig()

	// Various CR fields need to be indexed to filter watch events
	// for the secret changes we want to be notified of
	// index caBundleSecretName
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &mariadbv1.Galera{}, caSecretNameField, func(rawObj client.Object) []string {
		// Extract the secret name from the spec, if one is provided
		cr := rawObj.(*mariadbv1.Galera)
		tls := &cr.Spec.TLS
		if tls.Ca.CaBundleSecretName != "" {
			return []string{tls.Ca.CaBundleSecretName}
		}
		return nil
	}); err != nil {
		return err
	}
	// index secretName
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &mariadbv1.Galera{}, serviceSecretNameField, func(rawObj client.Object) []string {
		// Extract the secret name from the spec, if one is provided
		cr := rawObj.(*mariadbv1.Galera)
		tls := &cr.Spec.TLS
		if tls.Enabled() {
			return []string{*tls.GenericService.SecretName}
		}
		return nil
	}); err != nil {
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&mariadbv1.Galera{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Endpoints{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(
			&corev1.Secret{},
			handler.EnqueueRequestsFromMapFunc(r.findObjectsForSrc),
			builder.WithPredicates(predicate.ResourceVersionChangedPredicate{}),
		).
		Complete(r)
}

// GetDatabaseObject - returns either a Galera or MariaDB object (and an associated client.Object interface).
// used by both MariaDBDatabaseReconciler and MariaDBAccountReconciler
// this will later return only Galera objects, so as a lookup it's part of the galera controller
func GetDatabaseObject(ctx context.Context, clientObj client.Client, name string, namespace string) (*databasev1beta1.Galera, error) {
	dbGalera := &databasev1beta1.Galera{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	objectKey := client.ObjectKeyFromObject(dbGalera)

	err := clientObj.Get(ctx, objectKey, dbGalera)
	if err != nil {
		return nil, err
	}

	return dbGalera, err
}

// findObjectsForSrc - returns a reconcile request if the object is referenced by a Galera CR
func (r *GaleraReconciler) findObjectsForSrc(ctx context.Context, src client.Object) []reconcile.Request {
	requests := []reconcile.Request{}

	for _, field := range allWatchFields {
		crList := &mariadbv1.GaleraList{}
		listOps := &client.ListOptions{
			FieldSelector: fields.OneTermEqualSelector(field, src.GetName()),
			Namespace:     src.GetNamespace(),
		}
		err := r.List(ctx, crList, listOps)
		if err != nil {
			return []reconcile.Request{}
		}

		for _, item := range crList.Items {
			requests = append(requests,
				reconcile.Request{
					NamespacedName: types.NamespacedName{
						Name:      item.GetName(),
						Namespace: item.GetNamespace(),
					},
				},
			)
		}
	}

	return requests
}

func (r *GaleraReconciler) reconcileDelete(ctx context.Context, instance *databasev1beta1.Galera, helper *helper.Helper) (ctrl.Result, error) {
	helper.GetLogger().Info("Reconciling Service delete")

	// Remove our finalizer from the db svc
	svc, err := service.GetServiceWithName(ctx, helper, instance.Name, instance.Namespace)
	if err != nil && !k8s_errors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if !k8s_errors.IsNotFound(err) && svc != nil {
		if controllerutil.RemoveFinalizer(svc, helper.GetFinalizer()) {
			err := r.Update(ctx, svc)
			if err != nil {
				return ctrl.Result{}, err
			}
		}
	}

	// Service is deleted so remove the finalizer.
	controllerutil.RemoveFinalizer(instance, helper.GetFinalizer())
	helper.GetLogger().Info("Reconciled Service delete successfully")

	return ctrl.Result{}, nil
}
