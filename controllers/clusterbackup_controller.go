package controllers

import (
	"context"
	"fmt"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"example.com/backup-operator/internal/resolve"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// ClusterBackupReconciler reconciles ClusterBackup resources.
type ClusterBackupReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *ClusterBackupReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var backup backupv1alpha1.ClusterBackup
	if err := r.Get(ctx, req.NamespacedName, &backup); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("reconciling ClusterBackup", "name", backup.Name)

	if !backup.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	job, err := findJob(ctx, r.Client, "ClusterBackup", backup.Name, "", backup.UID)
	if err != nil {
		return ctrl.Result{}, err
	}

	if job == nil {
		storageName := ""
		if backup.Spec.StorageRef != nil {
			storageName = backup.Spec.StorageRef.Name
		}
		storage, err := resolve.StorageLocation(ctx, r.Client, storageName)
		if err != nil {
			return r.failClusterBackup(ctx, &backup, fmt.Sprintf("storage location error: %v", err))
		}

		job, err = buildBackupJob("ClusterBackup", backup.Name, "", backup.UID, storage)
		if err != nil {
			return r.failClusterBackup(ctx, &backup, err.Error())
		}
		if backup.Spec.Timeout != nil {
			seconds := int64(backup.Spec.Timeout.Duration.Seconds())
			if seconds > 0 {
				job.Spec.ActiveDeadlineSeconds = &seconds
			}
		}

		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}

		now := metav1.Now()
		backup.Status.Phase = backupv1alpha1.BackupPhaseRunning
		backup.Status.StartedAt = &now
		backup.Status.ObservedGeneration = backup.Generation
		if err := r.Status().Update(ctx, &backup); err != nil {
			return ctrl.Result{}, err
		}

		return ctrl.Result{}, nil
	}

	if job.Status.Succeeded > 0 && backup.Status.Phase != backupv1alpha1.BackupPhaseCompleted {
		backup.Status.Phase = backupv1alpha1.BackupPhaseCompleted
		backup.Status.ObservedGeneration = backup.Generation
		if err := r.Status().Update(ctx, &backup); err != nil {
			return ctrl.Result{}, err
		}
	}

	if job.Status.Failed > 0 && backup.Status.Phase != backupv1alpha1.BackupPhaseFailed {
		return r.failClusterBackup(ctx, &backup, "backup job failed")
	}

	return ctrl.Result{}, nil
}

func (r *ClusterBackupReconciler) failClusterBackup(ctx context.Context, backup *backupv1alpha1.ClusterBackup, message string) (ctrl.Result, error) {
	now := metav1.Now()
	backup.Status.Phase = backupv1alpha1.BackupPhaseFailed
	backup.Status.Message = message
	backup.Status.CompletedAt = &now
	backup.Status.ObservedGeneration = backup.Generation
	if err := r.Status().Update(ctx, backup); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ClusterBackupReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.ClusterBackup{}).
		Complete(r)
}
