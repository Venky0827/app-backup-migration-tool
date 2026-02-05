package controllers

import (
	"context"
	"fmt"
	"time"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// RemoteClusterReconciler reconciles RemoteCluster resources.
type RemoteClusterReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

func (r *RemoteClusterReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var remote backupv1alpha1.RemoteCluster
	if err := r.Get(ctx, req.NamespacedName, &remote); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.V(1).Info("reconciling RemoteCluster", "name", remote.Name)
	status := remote.Status
	now := metav1.Now()
	status.LastValidated = &now
	status.ObservedGeneration = remote.Generation

	secret := &corev1.Secret{}
	secretKey := client.ObjectKey{
		Namespace: remote.Spec.Auth.SecretRef.Namespace,
		Name:      remote.Spec.Auth.SecretRef.Name,
	}
	if secretKey.Namespace == "" {
		secretKey.Namespace = operatorNamespace()
	}

	if err := r.Get(ctx, secretKey, secret); err != nil {
		status.Message = fmt.Sprintf("unable to read auth secret: %v", err)
		remote.Status = status
		_ = r.Status().Update(ctx, &remote)
		return ctrl.Result{}, nil
	}

	cfg, err := buildRemoteConfig(&remote, secret)
	if err != nil {
		status.Message = err.Error()
		remote.Status = status
		_ = r.Status().Update(ctx, &remote)
		return ctrl.Result{}, nil
	}

	clientset, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		status.Message = fmt.Sprintf("failed to build remote client: %v", err)
		remote.Status = status
		_ = r.Status().Update(ctx, &remote)
		return ctrl.Result{}, nil
	}

	ctxTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if _, err := clientset.CoreV1().Namespaces().List(ctxTimeout, metav1.ListOptions{Limit: 1}); err != nil {
		status.Message = fmt.Sprintf("remote cluster not reachable: %v", err)
		remote.Status = status
		_ = r.Status().Update(ctx, &remote)
		return ctrl.Result{}, nil
	}

	status.Message = "remote cluster reachable"
	remote.Status = status
	if err := r.Status().Update(ctx, &remote); err != nil {
		logger.Error(err, "unable to update RemoteCluster status")
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func buildRemoteConfig(remote *backupv1alpha1.RemoteCluster, secret *corev1.Secret) (*rest.Config, error) {
	switch remote.Spec.Auth.Method {
	case backupv1alpha1.RemoteAuthKubeconfig:
		raw, ok := secret.Data["kubeconfig"]
		if !ok {
			return nil, fmt.Errorf("kubeconfig key not found in secret")
		}
		return clientcmd.RESTConfigFromKubeConfig(raw)
	case backupv1alpha1.RemoteAuthServiceAccountToken, "":
		token, ok := secret.Data["token"]
		if !ok {
			return nil, fmt.Errorf("token key not found in secret")
		}
		cfg := &rest.Config{
			Host:        remote.Spec.APIServer,
			BearerToken: string(token),
			TLSClientConfig: rest.TLSClientConfig{
				Insecure: remote.Spec.InsecureSkipTLS,
				CAData:   secret.Data["ca.crt"],
			},
		}
		if len(remote.Spec.CABundle) > 0 {
			cfg.TLSClientConfig.CAData = remote.Spec.CABundle
		}
		return cfg, nil
	default:
		return nil, fmt.Errorf("unsupported auth method %q", remote.Spec.Auth.Method)
	}
}

func (r *RemoteClusterReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&backupv1alpha1.RemoteCluster{}).
		Complete(r)
}
