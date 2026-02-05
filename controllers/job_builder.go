package controllers

import (
	"context"
	"fmt"
	"os"
	"strings"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	labelOwnerUID       = "backup.example.com/owner-uid"
	labelOwnerKind      = "backup.example.com/owner-kind"
	labelOwnerName      = "backup.example.com/owner-name"
	labelOwnerNamespace = "backup.example.com/owner-namespace"
)

func operatorNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return "backup-operator-system"
}

func operatorServiceAccount() string {
	if sa := os.Getenv("POD_SERVICE_ACCOUNT"); sa != "" {
		return sa
	}
	return "controller-manager"
}

func operatorImage() string {
	if img := os.Getenv("OPERATOR_IMAGE"); img != "" {
		return img
	}
	return "controller:latest"
}

func clusterID() string {
	if id := os.Getenv("CLUSTER_ID"); id != "" {
		return id
	}
	return "cluster-unknown"
}

func buildOwnerLabels(kind, name, namespace string, uid types.UID) map[string]string {
	labels := map[string]string{
		labelOwnerUID:  string(uid),
		labelOwnerKind: kind,
		labelOwnerName: name,
	}
	if namespace != "" {
		labels[labelOwnerNamespace] = namespace
	}
	return labels
}

func findJob(ctx context.Context, c client.Client, kind, name, namespace string, uid types.UID) (*batchv1.Job, error) {
	jobList := &batchv1.JobList{}
	match := client.MatchingLabels{labelOwnerUID: string(uid)}
	if err := c.List(ctx, jobList, match, client.InNamespace(operatorNamespace())); err != nil {
		return nil, err
	}
	for i := range jobList.Items {
		job := &jobList.Items[i]
		if job.Labels[labelOwnerKind] == kind && job.Labels[labelOwnerName] == name && job.Labels[labelOwnerNamespace] == namespace {
			return job, nil
		}
	}
	return nil, nil
}

func buildBackupJob(ownerKind, ownerName, ownerNamespace string, ownerUID types.UID, storage *backupv1alpha1.BackupStorageLocation) (*batchv1.Job, error) {
	jobName := strings.ToLower(fmt.Sprintf("backup-%s-", ownerName))
	labels := buildOwnerLabels(ownerKind, ownerName, ownerNamespace, ownerUID)

	container := corev1.Container{
		Name:    "backup-worker",
		Image:   operatorImage(),
		Command: []string{"/manager"},
		Args: []string{
			"--mode=backup-worker",
			fmt.Sprintf("--worker-kind=%s", ownerKind),
			fmt.Sprintf("--worker-name=%s", ownerName),
			fmt.Sprintf("--worker-namespace=%s", ownerNamespace),
		},
	}
	container.Env = append(container.Env,
		corev1.EnvVar{
			Name:  "CLUSTER_ID",
			Value: clusterID(),
		},
		corev1.EnvVar{
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
			},
		},
	)

	podSpec := corev1.PodSpec{
		ServiceAccountName: operatorServiceAccount(),
		RestartPolicy:      corev1.RestartPolicyNever,
		Containers:         []corev1.Container{container},
	}

	if storage != nil && storage.Spec.Type == backupv1alpha1.StorageLocationNFS {
		if storage.Spec.NFS == nil || storage.Spec.NFS.Server == "" || storage.Spec.NFS.Path == "" {
			return nil, fmt.Errorf("nfs storage location missing server/path")
		}

		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: "nfs-storage",
			VolumeSource: corev1.VolumeSource{
				NFS: &corev1.NFSVolumeSource{
					Server:   storage.Spec.NFS.Server,
					Path:     storage.Spec.NFS.Path,
					ReadOnly: storage.Spec.NFS.ReadOnly,
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "nfs-storage",
			MountPath: "/data",
		})
		container.Env = append(container.Env, corev1.EnvVar{Name: "NFS_MOUNT_PATH", Value: "/data"})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: jobName,
			Namespace:    operatorNamespace(),
			Labels:       labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(0),
			TTLSecondsAfterFinished: int32Ptr(3600),
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}

	return job, nil
}

func buildRestoreJob(ownerKind, ownerName, ownerNamespace string, ownerUID types.UID, storage *backupv1alpha1.BackupStorageLocation) (*batchv1.Job, error) {
	jobName := strings.ToLower(fmt.Sprintf("restore-%s-", ownerName))
	labels := buildOwnerLabels(ownerKind, ownerName, ownerNamespace, ownerUID)

	container := corev1.Container{
		Name:    "restore-worker",
		Image:   operatorImage(),
		Command: []string{"/manager"},
		Args: []string{
			"--mode=restore-worker",
			fmt.Sprintf("--worker-kind=%s", ownerKind),
			fmt.Sprintf("--worker-name=%s", ownerName),
			fmt.Sprintf("--worker-namespace=%s", ownerNamespace),
		},
	}
	container.Env = append(container.Env,
		corev1.EnvVar{
			Name:  "CLUSTER_ID",
			Value: clusterID(),
		},
		corev1.EnvVar{
			Name: "POD_NAMESPACE",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
			},
		},
	)

	podSpec := corev1.PodSpec{
		ServiceAccountName: operatorServiceAccount(),
		RestartPolicy:      corev1.RestartPolicyNever,
		Containers:         []corev1.Container{container},
	}

	if storage != nil && storage.Spec.Type == backupv1alpha1.StorageLocationNFS {
		if storage.Spec.NFS == nil || storage.Spec.NFS.Server == "" || storage.Spec.NFS.Path == "" {
			return nil, fmt.Errorf("nfs storage location missing server/path")
		}

		podSpec.Volumes = append(podSpec.Volumes, corev1.Volume{
			Name: "nfs-storage",
			VolumeSource: corev1.VolumeSource{
				NFS: &corev1.NFSVolumeSource{
					Server:   storage.Spec.NFS.Server,
					Path:     storage.Spec.NFS.Path,
					ReadOnly: storage.Spec.NFS.ReadOnly,
				},
			},
		})
		container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
			Name:      "nfs-storage",
			MountPath: "/data",
		})
		container.Env = append(container.Env, corev1.EnvVar{Name: "NFS_MOUNT_PATH", Value: "/data"})
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: jobName,
			Namespace:    operatorNamespace(),
			Labels:       labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit:            int32Ptr(0),
			TTLSecondsAfterFinished: int32Ptr(3600),
			Template: corev1.PodTemplateSpec{
				Spec: podSpec,
			},
		},
	}

	return job, nil
}

func int32Ptr(val int32) *int32 {
	return &val
}

func labelSelectorForOwner(kind, name, namespace string, uid types.UID) labels.Selector {
	set := labels.Set(buildOwnerLabels(kind, name, namespace, uid))
	return set.AsSelector()
}
