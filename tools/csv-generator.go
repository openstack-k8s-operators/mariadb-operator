package main

import (
	"flag"
	"os"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"

	"github.com/blang/semver"
	util "github.com/openstack-k8s-operators/lib-common/pkg/util"
	"github.com/openstack-k8s-operators/mariadb-operator/tools/helper"
	csvv1 "github.com/operator-framework/operator-lifecycle-manager/pkg/api/apis/operators/v1alpha1"
	"github.com/operator-framework/operator-lifecycle-manager/pkg/lib/version"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	csvVersion         = flag.String("csv-version", "", "")
	replacesCsvVersion = flag.String("replaces-csv-version", "", "")
	namespace          = flag.String("namespace", "", "")
	pullPolicy         = flag.String("pull-policy", "Always", "")

	logoBase64 = flag.String("logo-base64", "", "")
	verbosity  = flag.String("verbosity", "1", "")

	operatorImage = flag.String("operator-image-name", "quay.io/openstack-k8s-operators/mariadb-operator:devel", "optional")
)

func main() {
	flag.Parse()

	data := NewClusterServiceVersionData{
		CsvVersion:         *csvVersion,
		ReplacesCsvVersion: *replacesCsvVersion,
		Namespace:          *namespace,
		ImagePullPolicy:    *pullPolicy,
		IconBase64:         *logoBase64,
		Verbosity:          *verbosity,
		OperatorImage:      *operatorImage,
	}

	csv, err := createClusterServiceVersion(&data)
	if err != nil {
		panic(err)
	}
	util.MarshallObject(csv, os.Stdout)

}

//NewClusterServiceVersionData - Data arguments used to create mariadb operators's CSV manifest
type NewClusterServiceVersionData struct {
	CsvVersion         string
	ReplacesCsvVersion string
	Namespace          string
	ImagePullPolicy    string
	IconBase64         string
	Verbosity          string

	DockerPrefix string
	DockerTag    string

	OperatorImage string
}

func createOperatorDeployment(repo, namespace, deployClusterResources, operatorImage, tag, verbosity, pullPolicy string) *appsv1.Deployment {
	deployment := helper.CreateOperatorDeployment("mariadb-operator", namespace, "name", "mariadb-operator", "mariadb-operator", int32(1))
	container := helper.CreateOperatorContainer("mariadb-operator", operatorImage, verbosity, corev1.PullPolicy(pullPolicy))
	container.Env = *helper.CreateOperatorEnvVar(repo, deployClusterResources, operatorImage, pullPolicy)
	deployment.Spec.Template.Spec.Containers = []corev1.Container{container}
	return deployment
}

func createClusterServiceVersion(data *NewClusterServiceVersionData) (*csvv1.ClusterServiceVersion, error) {

	description := `
Install and configure MariaDB.
`
	deployment := createOperatorDeployment(
		data.DockerPrefix,
		data.Namespace,
		"true",
		data.OperatorImage,
		data.DockerTag,
		data.Verbosity,
		data.ImagePullPolicy)

	rules := getOperatorRules()
	serviceRules := getServiceRules()

	strategySpec := csvv1.StrategyDetailsDeployment{
		Permissions: []csvv1.StrategyDeploymentPermissions{
			{
				ServiceAccountName: "mariadb-operator",
				Rules:              *rules,
			},
			{
				ServiceAccountName: "mariadb",
				Rules:              *serviceRules,
			},
		},
		DeploymentSpecs: []csvv1.StrategyDeploymentSpec{
			{
				Name: "mariadb-operator",
				Spec: deployment.Spec,
			},
		},
	}

	csvVersion, err := semver.New(data.CsvVersion)
	if err != nil {
		return nil, err
	}

	return &csvv1.ClusterServiceVersion{
		TypeMeta: metav1.TypeMeta{
			Kind:       "ClusterServiceVersion",
			APIVersion: "operators.coreos.com/v1alpha1",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mariadb-operator." + data.CsvVersion,
			Namespace: data.Namespace,
			Annotations: map[string]string{

				"capabilities": "Basic Install",
				"categories":   "Identity",
				"description":  "Creates and maintains MariaDB deployments.",
			},
		},

		Spec: csvv1.ClusterServiceVersionSpec{
			DisplayName: "MariaDB Operator",
			Description: description,
			Keywords:    []string{"MariaDB Operator", "OpenStack", "Database"},
			Version:     version.OperatorVersion{Version: *csvVersion},
			Maturity:    "alpha",
			Replaces:    data.ReplacesCsvVersion,
			Maintainers: []csvv1.Maintainer{{
				Name:  "OpenStack k8s Operators",
				Email: "openstack-k8s-operators@googlegroups.com",
			}},
			Provider: csvv1.AppLink{
				Name: "OpenStack K8s Operators MariaDB Operator project",
			},
			Links: []csvv1.AppLink{
				{
					Name: "MariaDB Operator",
					URL:  "https://github.com/openstack-k8s-operators/mariadb-operator/blob/master/README.md",
				},
				{
					Name: "Source Code",
					URL:  "https://github.com/openstack-k8s-operators/mariadb-operator",
				},
			},
			Icon: []csvv1.Icon{{
				Data:      data.IconBase64,
				MediaType: "image/png",
			}},
			Labels: map[string]string{
				"alm-owner-mariadb-operator": "mariadb-operator",
				"operated-by":                "mariadb-operator",
			},
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"alm-owner-mariadb-operator": "mariadb-operator",
					"operated-by":                "mariadb-operator",
				},
			},
			InstallModes: []csvv1.InstallMode{
				{
					Type:      csvv1.InstallModeTypeOwnNamespace,
					Supported: true,
				},
				{
					Type:      csvv1.InstallModeTypeSingleNamespace,
					Supported: true,
				},
				{
					Type:      csvv1.InstallModeTypeMultiNamespace,
					Supported: false,
				},
				{
					Type:      csvv1.InstallModeTypeAllNamespaces,
					Supported: false,
				},
			},
			InstallStrategy: csvv1.NamedInstallStrategy{
				StrategyName: "deployment",
				StrategySpec: strategySpec,
			},
			CustomResourceDefinitions: csvv1.CustomResourceDefinitions{

				Owned: []csvv1.CRDDescription{
					{
						Name:        "database.openstack.org",
						Version:     "v1beta1",
						Kind:        "MariaDB",
						DisplayName: "MariaDB",
						Description: "MariaDB Instance",
					},
					{
						Name:        "database.openstack.org",
						Version:     "v1beta1",
						Kind:        "MariaDBSchema",
						DisplayName: "MariaDBSchema",
						Description: "MariaDB Schema",
					},
				},
			},
		},
	}, nil
}

func getOperatorRules() *[]rbacv1.PolicyRule {
	return &[]rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"pods",
				"services",
				"services/finalizers",
				"persistentvolumeclaims",
				"endpoints",
				"events",
				"configmaps",
				"secrets",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"batch",
			},
			Resources: []string{
				"jobs",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"monitoring.coreos.com",
			},
			Resources: []string{
				"servicemonitors",
			},
			Verbs: []string{
				"get",
				"create",
			},
		},
		{
			APIGroups: []string{
				"apps",
			},
			Resources: []string{
				"deployments/finalizers",
			},
			ResourceNames: []string{
				"mariadb-operator",
			},
			Verbs: []string{
				"update",
			},
		},
		{
			APIGroups: []string{
				"mariadb.openstack.org",
			},
			Resources: []string{
				"*",
				"mariadbs",
				"mariadbschemas",
			},
			Verbs: []string{
				"*",
			},
		},
	}
}

func getServiceRules() *[]rbacv1.PolicyRule {
	return &[]rbacv1.PolicyRule{
		{
			APIGroups: []string{
				"",
			},
			Resources: []string{
				"pods",
			},
			Verbs: []string{
				"*",
			},
		},
		{
			APIGroups: []string{
				"security.openshift.io",
			},
			Resources: []string{
				"securitycontextconstraints",
			},
			ResourceNames: []string{
				"anyuid",
			},
			Verbs: []string{
				"use",
			},
		},
	}
}
