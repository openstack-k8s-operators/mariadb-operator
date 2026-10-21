package functional_test

import (
	. "github.com/onsi/ginkgo/v2" //revive:disable:dot-imports
	. "github.com/onsi/gomega"    //revive:disable:dot-imports
	"github.com/openstack-k8s-operators/lib-common/modules/common/util"
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var _ = Describe("TLS profile reconciliation", func() {
	var galeraName types.NamespacedName
	var backupName types.NamespacedName
	const customConfig = "[mysqld]\nssl-cipher = ECDHE-RSA-AES128-GCM-SHA256\ntls-version = TLSv1.2\n"

	createWorkloads := func() {
		for name, data := range map[string]map[string][]byte{
			"osp-secret": {"DbRootPassword": []byte("test-root-password")},
			"tls-cert":   {"tls.crt": []byte("test-cert"), "tls.key": []byte("test-key")},
			"tls-ca":     {"tls-ca-bundle.pem": []byte("test-ca")},
		} {
			secret := &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace}, Data: data}
			Expect(k8sClient.Create(ctx, secret)).To(Succeed())
			DeferCleanup(th.DeleteInstance, secret)
		}
		spec := GetDefaultGaleraSpec()
		spec["tls"] = map[string]any{"secretName": "tls-cert", "caBundleSecretName": "tls-ca"}
		spec["customServiceConfig"] = customConfig
		galera := CreateGaleraConfig(namespace, spec)
		galeraName = client.ObjectKeyFromObject(galera)
		DeferCleanup(th.DeleteInstance, galera)
		backup := &mariadbv1.GaleraBackup{
			ObjectMeta: metav1.ObjectMeta{Name: "backup", Namespace: namespace},
			Spec:       mariadbv1.GaleraBackupSpec{DatabaseInstance: galeraName.Name, StorageRequest: "500M", Schedule: "@daily"},
		}
		Expect(k8sClient.Create(ctx, backup)).To(Succeed())
		backupName = client.ObjectKeyFromObject(backup)
		DeferCleanup(th.DeleteInstance, backup)
	}

	checkConfig := func(cipher, versions string) {
		Eventually(func(g Gomega) {
			for _, name := range []string{galeraName.Name + "-config-data", backupName.Name + "-backup-config"} {
				cm := &corev1.ConfigMap{}
				g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, cm)).To(Succeed())
				files := []string{"galera_tls.cnf.in", "galera_external_tls.cnf.in"}
				if name == backupName.Name+"-backup-config" {
					files = []string{"mysql_backup_tls.cnf"}
				} else {
					g.Expect(cm.Data[mariadbv1.CustomServiceConfigFile]).To(Equal(customConfig))
				}
				for _, file := range files {
					if cipher != "" {
						g.Expect(cm.Data[file]).To(ContainSubstring("ssl-cipher = " + cipher + "\n"))
					} else if file == "mysql_backup_tls.cnf" {
						g.Expect(cm.Data[file]).To(ContainSubstring("sockopt = cipher=!SSLv2:"))
					} else {
						g.Expect(cm.Data[file]).To(ContainSubstring("ssl-cipher = !SSLv2:"))
					}
					if versions == "" {
						g.Expect(cm.Data[file]).NotTo(ContainSubstring("tls-version ="))
					} else {
						g.Expect(cm.Data[file]).To(ContainSubstring("tls-version = " + versions + "\n"))
					}
				}
			}
		}, timeout, interval).Should(Succeed())
	}

	getPodHashes := func(g Gomega) (string, string) {
		var galeraHash, backupHash string
		sts := &appsv1.StatefulSet{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: galeraName.Name + "-galera", Namespace: namespace}, sts)).To(Succeed())
		cron := &batchv1.CronJob{}
		g.Expect(k8sClient.Get(ctx, client.ObjectKey{Name: "backup-" + backupName.Name, Namespace: namespace}, cron)).To(Succeed())
		for _, item := range sts.Spec.Template.Spec.Containers[0].Env {
			if item.Name == "CR_CONFIG_HASH" {
				galeraHash = item.Value
			}
		}
		for _, item := range cron.Spec.JobTemplate.Spec.Template.Spec.Containers[0].Env {
			if item.Name == "CR_CONFIG_HASH" {
				backupHash = item.Value
			}
		}
		g.Expect(galeraHash).NotTo(BeEmpty())
		g.Expect(backupHash).NotTo(BeEmpty())
		return galeraHash, backupHash
	}

	It("inherits profile creation and updates, refreshes workload hashes, and falls back on deletion", func() {
		createWorkloads()
		checkConfig("", "")
		var oldGaleraHash, oldBackupHash string
		Eventually(func(g Gomega) {
			oldGaleraHash, oldBackupHash = getPodHashes(g)
		}, timeout, interval).Should(Succeed())
		profile := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: util.TLSProfileConfigMap, Namespace: namespace},
			Data:       map[string]string{"SSLCipherSuite": "TLS_AES_256_GCM_SHA384", "SSLProtocol": "-all +TLSv1.3"},
		}
		Expect(k8sClient.Create(ctx, profile)).To(Succeed())
		checkConfig("TLS_AES_256_GCM_SHA384", "TLSv1.3")
		Eventually(func(g Gomega) {
			galeraHash, backupHash := getPodHashes(g)
			g.Expect(galeraHash).NotTo(Equal(oldGaleraHash))
			g.Expect(backupHash).NotTo(Equal(oldBackupHash))
		}, timeout, interval).Should(Succeed())

		profile.Data = map[string]string{"SSLCipherSuite": "ECDHE-RSA-AES256-GCM-SHA384", "SSLProtocol": "-all +TLSv1.2 +TLSv1.3"}
		Expect(k8sClient.Update(ctx, profile)).To(Succeed())
		checkConfig("ECDHE-RSA-AES256-GCM-SHA384", "TLSv1.2,TLSv1.3")
		Expect(k8sClient.Delete(ctx, profile)).To(Succeed())
		checkConfig("", "")
	})

	It("reports invalid protocols and recovers when the profile is corrected", func() {
		profile := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Name: util.TLSProfileConfigMap, Namespace: namespace},
			Data:       map[string]string{"SSLProtocol": "-all +TLSv1.4"},
		}
		Expect(k8sClient.Create(ctx, profile)).To(Succeed())
		createWorkloads()
		Eventually(func(g Gomega) {
			galera := &mariadbv1.Galera{}
			g.Expect(k8sClient.Get(ctx, galeraName, galera)).To(Succeed())
			configReady := galera.Status.Conditions.Get("ServiceConfigReady")
			g.Expect(configReady).NotTo(BeNil())
			g.Expect(configReady.Message).To(ContainSubstring("unsupported TLS protocol profile"))
		}, timeout, interval).Should(Succeed())
		profile.Data["SSLProtocol"] = "-all +TLSv1.3"
		Expect(k8sClient.Update(ctx, profile)).To(Succeed())
		checkConfig("", "TLSv1.3")
	})
})
