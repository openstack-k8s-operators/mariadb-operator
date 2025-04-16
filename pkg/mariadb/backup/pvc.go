package mariadbbackup

import (
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupPVCs returns the PVC objects used by the backup Job to transfer data and create a SQL backup
func BackupPVCs(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolumeClaim) {
	backupName := BackupPVCName(b, g)
	backupSize, err := resource.ParseQuantity(b.Spec.StorageRequest)
	if err != nil {
		backupSize = resource.MustParse(g.Spec.StorageRequest)
	}
	var backupClass string
	if b.Spec.StorageClass != "" {
		backupClass = b.Spec.StorageClass
	} else {
		backupClass = g.Spec.StorageClass
	}
	backupPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      backupName,
			Namespace: b.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			StorageClassName: &backupClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": backupSize,
				},
			},
		},
	}

	if b.Spec.TransferStorage == nil {
		return backupPVC, nil
	}

	transferName := BackupTransferPVCName(b, g)
	transferSize, err := resource.ParseQuantity(b.Spec.TransferStorage.StorageRequest)
	if err != nil {
		transferSize = resource.MustParse(g.Spec.StorageRequest)
	}
	var transferClass string
	if b.Spec.TransferStorage.StorageClass != "" {
		transferClass = b.Spec.TransferStorage.StorageClass
	} else {
		transferClass = g.Spec.StorageClass
	}
	transferPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      transferName,
			Namespace: b.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			StorageClassName: &transferClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": transferSize,
				},
			},
		},
	}

	return backupPVC, transferPVC
}
