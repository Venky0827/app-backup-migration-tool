package main

import (
	"context"
	"fmt"
	"os"
	"time"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type workerConfig struct {
	mode      string
	kind      string
	name      string
	namespace string
}

func runWorker(ctx context.Context, cfg workerConfig) error {
	if cfg.kind == "" || cfg.name == "" {
		return fmt.Errorf("worker-kind and worker-name are required")
	}

	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return err
	}
	if err := backupv1alpha1.AddToScheme(scheme); err != nil {
		return err
	}

	restCfg := ctrl.GetConfigOrDie()
	c, err := client.New(restCfg, client.Options{Scheme: scheme})
	if err != nil {
		return err
	}

	switch cfg.mode {
	case "backup-worker":
		return runBackupWorker(ctx, c, restCfg, cfg)
	case "restore-worker":
		return runRestoreWorker(ctx, c, restCfg, cfg)
	default:
		return fmt.Errorf("unknown worker mode %q", cfg.mode)
	}
}

func workerContext() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 2*time.Hour)
}

func getEnvOrDefault(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func clusterID() string {
	return getEnvOrDefault("CLUSTER_ID", "cluster-unknown")
}

func operatorNamespace() string {
	return getEnvOrDefault("POD_NAMESPACE", "backup-operator-system")
}

func operatorServiceAccount() string {
	return getEnvOrDefault("POD_SERVICE_ACCOUNT", "controller-manager")
}

func operatorImage() string {
	return getEnvOrDefault("OPERATOR_IMAGE", "controller:latest")
}

func restConfigForWorker(cfg *rest.Config) *rest.Config {
	return cfg
}
