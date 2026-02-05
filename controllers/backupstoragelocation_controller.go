package controllers

import (
	"context"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// BackupStorageLocationReconciler reconciles BackupStorageLocation resources.
type BackupStorageLocationReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *BackupStorageLocationReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var location backupv1alpha1.BackupStorageLocation
	if err := r.Get(ctx, req.NamespacedName, &location); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("reconciling BackupStorageLocation", "name", location.Name)
	status := location.Status
	now := metav1.Now()
	status.LastValidated = &now
	status.ObservedGeneration = location.Generation

	switch location.Spec.Type {
	case backupv1alpha1.StorageLocationS3:
		if location.Spec.S3 == nil || location.Spec.S3.Bucket == "" {
			status.Phase = backupv1alpha1.StorageLocationUnavailable
			status.Message = "s3 storage location requires bucket"
		} else {
			status.Phase = backupv1alpha1.StorageLocationAvailable
			status.Message = "s3 storage location configured"
		}
	case backupv1alpha1.StorageLocationNFS:
		if location.Spec.NFS == nil || location.Spec.NFS.Server == "" || location.Spec.NFS.Path == "" {
			status.Phase = backupv1alpha1.StorageLocationUnavailable
			status.Message = "nfs storage location requires server and path"
		} else {
			status.Phase = backupv1alpha1.StorageLocationAvailable
			status.Message = "nfs storage location configured"
		}
	default:
		status.Phase = backupv1alpha1.StorageLocationUnavailable
		status.Message = "unsupported storage location type"
	}

	location.Status = status
	if err := r.Status().Update(ctx, &location); err != nil {
		logger.Error(err, "unable to update BackupStorageLocation status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *BackupStorageLocationReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.BackupStorageLocation{}).
		Complete(r)
}
