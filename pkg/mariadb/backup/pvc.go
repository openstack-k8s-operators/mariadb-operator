package mariadb_backup

import (
	mariadbv1 "github.com/openstack-k8s-operators/mariadb-operator/api/v1beta1"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// BackupPVC returns the PVC object used by the backup Job to make a physical copy of the galera cluster
func BackupPVC(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) *corev1.PersistentVolumeClaim {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "mysql-db-backup",
			Namespace: b.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			StorageClassName: &g.Spec.StorageClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": resource.MustParse(g.Spec.StorageRequest),
				},
			},
		},
	}

	return pvc
}

func BackupPVCs(b *mariadbv1.GaleraBackup, g *mariadbv1.Galera) (*corev1.PersistentVolumeClaim, *corev1.PersistentVolumeClaim) {
	physicalName := BackupPhysicalPVCName(b, g)
	physicalSize, err := resource.ParseQuantity(b.Spec.StorageRequest)
	if err != nil {
		physicalSize = resource.MustParse(g.Spec.StorageRequest)
	}
	var physicalClass string
	if b.Spec.StorageClass != "" {
		physicalClass = b.Spec.StorageClass
	} else {
		physicalClass = g.Spec.StorageClass
	}
	physicalPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      physicalName,
			Namespace: b.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			StorageClassName: &physicalClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": physicalSize,
				},
			},
		},
	}

	if b.Spec.LogicalBackup == nil {
		return physicalPVC, nil
	}

	logicalName := BackupLogicalPVCName(b, g)
	logicalSize, err := resource.ParseQuantity(b.Spec.LogicalBackup.StorageRequest)
	if err != nil {
		logicalSize = resource.MustParse(g.Spec.StorageRequest)
	}
	var logicalClass string
	if b.Spec.LogicalBackup.StorageClass != "" {
		logicalClass = b.Spec.LogicalBackup.StorageClass
	} else {
		logicalClass = g.Spec.StorageClass
	}
	logicalPVC := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      logicalName,
			Namespace: b.Namespace,
		},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{
				"ReadWriteOnce",
			},
			StorageClassName: &logicalClass,
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": logicalSize,
				},
			},
		},
	}

	return physicalPVC, logicalPVC
}
