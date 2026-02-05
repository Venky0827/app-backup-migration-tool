package controllers

import (
	"context"
	"fmt"
	"time"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"example.com/backup-operator/internal/resolve"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ClusterRestoreReconciler reconciles ClusterRestore resources.
type ClusterRestoreReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ClusterRestoreReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var restore backupv1alpha1.ClusterRestore
	if err := r.Get(ctx, req.NamespacedName, &restore); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("reconciling ClusterRestore", "name", restore.Name)

	if !restore.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	job, err := findJob(ctx, r.Client, "ClusterRestore", restore.Name, "", restore.UID)
	if err != nil {
		return ctrl.Result{}, err
	}

	sourceBackup, err := r.getBackupForRestore(ctx, &restore)
	if err != nil {
		return r.failClusterRestore(ctx, &restore, err.Error())
	}

	if sourceBackup.Status.ArtifactLocation == "" {
		logger.V(1).Info("backup artifact location not ready yet", "backup", sourceBackup.Name)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if job == nil {
		storageName := ""
		if sourceBackup.Spec.StorageRef != nil {
			storageName = sourceBackup.Spec.StorageRef.Name
		}
		storage, err := resolve.StorageLocation(ctx, r.Client, storageName)
		if err != nil {
			return r.failClusterRestore(ctx, &restore, fmt.Sprintf("storage location error: %v", err))
		}

		job, err = buildRestoreJob("ClusterRestore", restore.Name, "", restore.UID, storage)
		if err != nil {
			return r.failClusterRestore(ctx, &restore, err.Error())
		}
		if restore.Spec.Timeout != nil {
			seconds := int64(restore.Spec.Timeout.Duration.Seconds())
			if seconds > 0 {
				job.Spec.ActiveDeadlineSeconds = &seconds
			}
		}

		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}

		now := metav1.Now()
		restore.Status.Phase = backupv1alpha1.RestorePhaseRunning
		restore.Status.StartedAt = &now
		restore.Status.ObservedGeneration = restore.Generation
		if err := r.Status().Update(ctx, &restore); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	if job.Status.Succeeded > 0 && restore.Status.Phase != backupv1alpha1.RestorePhaseCompleted {
		restore.Status.Phase = backupv1alpha1.RestorePhaseCompleted
		restore.Status.ObservedGeneration = restore.Generation
		if err := r.Status().Update(ctx, &restore); err != nil {
			return ctrl.Result{}, err
		}
	}

	if job.Status.Failed > 0 && restore.Status.Phase != backupv1alpha1.RestorePhaseFailed {
		return r.failClusterRestore(ctx, &restore, "restore job failed")
	}

	return ctrl.Result{}, nil
}

func (r *ClusterRestoreReconciler) getBackupForRestore(ctx context.Context, restore *backupv1alpha1.ClusterRestore) (*backupv1alpha1.Backup, error) {
	switch restore.Spec.SourceRef.Kind {
	case "Backup":
		if restore.Spec.SourceRef.Namespace == "" {
			return nil, fmt.Errorf("sourceRef.namespace is required for Backup")
		}
		var backup backupv1alpha1.Backup
		if err := r.Get(ctx, client.ObjectKey{Namespace: restore.Spec.SourceRef.Namespace, Name: restore.Spec.SourceRef.Name}, &backup); err != nil {
			return nil, err
		}
		return &backup, nil
	case "ClusterBackup":
		var clusterBackup backupv1alpha1.ClusterBackup
		if err := r.Get(ctx, client.ObjectKey{Name: restore.Spec.SourceRef.Name}, &clusterBackup); err != nil {
			return nil, err
		}
		converted := backupv1alpha1.Backup{
			ObjectMeta: metav1.ObjectMeta{Name: clusterBackup.Name},
			Spec:       clusterBackup.Spec.BackupSpec,
			Status:     clusterBackup.Status.BackupStatus,
		}
		return &converted, nil
	default:
		return nil, fmt.Errorf("unsupported sourceRef.kind %q", restore.Spec.SourceRef.Kind)
	}
}

func (r *ClusterRestoreReconciler) failClusterRestore(ctx context.Context, restore *backupv1alpha1.ClusterRestore, message string) (ctrl.Result, error) {
	now := metav1.Now()
	restore.Status.Phase = backupv1alpha1.RestorePhaseFailed
	restore.Status.Message = message
	restore.Status.CompletedAt = &now
	restore.Status.ObservedGeneration = restore.Generation
	if err := r.Status().Update(ctx, restore); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ClusterRestoreReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.ClusterRestore{}).
		Complete(r)
}
