package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"example.com/backup-operator/internal/resolve"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

type restoreObject struct {
	kind      string
	name      string
	namespace string
	spec      backupv1alpha1.RestoreSpec
	status    backupv1alpha1.RestoreStatus
	update    func(status backupv1alpha1.RestoreStatus) error
}

type backupRef struct {
	name      string
	namespace string
	spec      backupv1alpha1.BackupSpec
	status    backupv1alpha1.BackupStatus
}

func runRestoreWorker(ctx context.Context, c client.Client, restCfg *rest.Config, cfg workerConfig) error {
	restore, err := loadRestoreObject(ctx, c, cfg)
	if err != nil {
		return err
	}

	source, err := loadBackupReference(ctx, c, restore.spec.SourceRef)
	if err != nil {
		return restore.update(failedRestoreStatus(restore.status, err.Error()))
	}

	if source.status.ArtifactLocation == "" {
		return restore.update(failedRestoreStatus(restore.status, "artifact location missing on source backup"))
	}

	storageName := ""
	if source.spec.StorageRef != nil {
		storageName = source.spec.StorageRef.Name
	}
	storage, err := resolve.StorageLocation(ctx, c, storageName)
	if err != nil {
		return restore.update(failedRestoreStatus(restore.status, err.Error()))
	}

	workDir, err := os.MkdirTemp("", "restore-worker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	artifactPath := filepath.Join(workDir, artifactFileName)
	if err := loadArtifact(ctx, c, storage, source.status.ArtifactLocation, artifactPath); err != nil {
		return restore.update(failedRestoreStatus(restore.status, err.Error()))
	}

	files, err := extractTarGz(artifactPath)
	if err != nil {
		return restore.update(failedRestoreStatus(restore.status, err.Error()))
	}

	resourceObjects, err := decodeYAMLDocuments(files["resources.yaml"])
	if err != nil {
		return restore.update(failedRestoreStatus(restore.status, err.Error()))
	}

	if len(files["snapshots.yaml"]) > 0 {
		snapshotObjects, err := decodeYAMLDocuments(files["snapshots.yaml"])
		if err != nil {
			return restore.update(failedRestoreStatus(restore.status, err.Error()))
		}
		resourceObjects = append(resourceObjects, snapshotObjects...)
	}

	targetCfg := restCfg
	if restore.spec.TargetClusterRef != nil {
		remoteCfg, err := buildRemoteConfigForRestore(ctx, c, restore.spec.TargetClusterRef.Name)
		if err != nil {
			return restore.update(failedRestoreStatus(restore.status, err.Error()))
		}
		targetCfg = remoteCfg
	}

	defaultNamespace := ""
	if restore.kind == "Restore" {
		defaultNamespace = restore.namespace
	}

	if err := applyResources(ctx, targetCfg, resourceObjects, restore.spec.NamespaceMapping, defaultNamespace); err != nil {
		return restore.update(failedRestoreStatus(restore.status, err.Error()))
	}

	completed := restore.status
	now := metav1.Now()
	completed.Phase = backupv1alpha1.RestorePhaseCompleted
	completed.CompletedAt = &now
	completed.Message = "restore completed"
	completed.ObservedGeneration = restore.status.ObservedGeneration
	return restore.update(completed)
}

func loadRestoreObject(ctx context.Context, c client.Client, cfg workerConfig) (*restoreObject, error) {
	switch cfg.kind {
	case "Restore":
		var restore backupv1alpha1.Restore
		if err := c.Get(ctx, client.ObjectKey{Namespace: cfg.namespace, Name: cfg.name}, &restore); err != nil {
			return nil, err
		}
		return &restoreObject{
			kind:      "Restore",
			name:      restore.Name,
			namespace: restore.Namespace,
			spec:      restore.Spec,
			status:    restore.Status,
			update: func(status backupv1alpha1.RestoreStatus) error {
				restore.Status = status
				return c.Status().Update(ctx, &restore)
			},
		}, nil
	case "ClusterRestore":
		var restore backupv1alpha1.ClusterRestore
		if err := c.Get(ctx, client.ObjectKey{Name: cfg.name}, &restore); err != nil {
			return nil, err
		}
		return &restoreObject{
			kind:      "ClusterRestore",
			name:      restore.Name,
			namespace: "",
			spec:      restore.Spec.RestoreSpec,
			status:    restore.Status.RestoreStatus,
			update: func(status backupv1alpha1.RestoreStatus) error {
				restore.Status.RestoreStatus = status
				return c.Status().Update(ctx, &restore)
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported restore kind %q", cfg.kind)
	}
}

func loadBackupReference(ctx context.Context, c client.Client, ref backupv1alpha1.RestoreSourceRef) (*backupRef, error) {
	switch ref.Kind {
	case "Backup":
		var backup backupv1alpha1.Backup
		if err := c.Get(ctx, client.ObjectKey{Namespace: ref.Namespace, Name: ref.Name}, &backup); err != nil {
			return nil, err
		}
		return &backupRef{name: backup.Name, namespace: backup.Namespace, spec: backup.Spec, status: backup.Status}, nil
	case "ClusterBackup":
		var backup backupv1alpha1.ClusterBackup
		if err := c.Get(ctx, client.ObjectKey{Name: ref.Name}, &backup); err != nil {
			return nil, err
		}
		return &backupRef{name: backup.Name, namespace: "", spec: backup.Spec.BackupSpec, status: backup.Status.BackupStatus}, nil
	default:
		return nil, fmt.Errorf("unsupported sourceRef.kind %q", ref.Kind)
	}
}

func failedRestoreStatus(current backupv1alpha1.RestoreStatus, message string) backupv1alpha1.RestoreStatus {
	now := metav1.Now()
	current.Phase = backupv1alpha1.RestorePhaseFailed
	current.Message = message
	current.CompletedAt = &now
	current.ObservedGeneration = current.ObservedGeneration
	return current
}

func extractTarGz(path string) (map[string][]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	tarReader := tar.NewReader(gzipReader)
	files := map[string][]byte{}
	for {
		head, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if head.Size == 0 {
			continue
		}
		buf := make([]byte, head.Size)
		if _, err := io.ReadFull(tarReader, buf); err != nil {
			return nil, err
		}
		files[head.Name] = buf
	}
	return files, nil
}

func decodeYAMLDocuments(data []byte) ([]*unstructured.Unstructured, error) {
	decoder := yaml.NewYAMLOrJSONDecoder(bytes.NewReader(data), 4096)
	var objs []*unstructured.Unstructured
	for {
		var raw map[string]any
		if err := decoder.Decode(&raw); err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
		if len(raw) == 0 {
			continue
		}
		objs = append(objs, &unstructured.Unstructured{Object: raw})
	}
	return objs, nil
}

func applyResources(ctx context.Context, restCfg *rest.Config, resources []*unstructured.Unstructured, mapping map[string]string, defaultNamespace string) error {
	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return err
	}
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return err
	}
	mapper := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(disco))

	ensureNamespace := func(namespace string) error {
		if namespace == "" {
			return nil
		}
		gvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "namespaces"}
		_, err := dyn.Resource(gvr).Get(ctx, namespace, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			obj := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "v1",
				"kind":       "Namespace",
				"metadata": map[string]any{
					"name": namespace,
				},
			}}
			_, err = dyn.Resource(gvr).Create(ctx, obj, metav1.CreateOptions{})
		}
		return err
	}

	sort.SliceStable(resources, func(i, j int) bool {
		return resourcePriority(resources[i]) < resourcePriority(resources[j])
	})

	for _, obj := range resources {
		if obj == nil || obj.Object == nil {
			continue
		}
		if shouldSkipKind(obj.GetKind()) {
			continue
		}
		sanitizeObject(obj)

		if obj.GetNamespace() != "" {
			targetNamespace := obj.GetNamespace()
			if defaultNamespace != "" {
				targetNamespace = defaultNamespace
			}
			if mapping != nil {
				if mapped, ok := mapping[obj.GetNamespace()]; ok {
					targetNamespace = mapped
				}
			}
			obj.SetNamespace(targetNamespace)
			if err := ensureNamespace(targetNamespace); err != nil {
				return err
			}
		}

		gvk := obj.GroupVersionKind()
		mappingInfo, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			continue
		}

		resourceClient := dyn.Resource(mappingInfo.Resource)
		if mappingInfo.Scope.Name() == meta.RESTScopeNameNamespace {
			resourceClient = resourceClient.Namespace(obj.GetNamespace())
		}

		obj.SetResourceVersion("")
		_, err = resourceClient.Create(ctx, obj, metav1.CreateOptions{})
		if err != nil {
			if errors.IsAlreadyExists(err) {
				existing, getErr := resourceClient.Get(ctx, obj.GetName(), metav1.GetOptions{})
				if getErr != nil {
					return getErr
				}
				obj.SetResourceVersion(existing.GetResourceVersion())
				_, err = resourceClient.Update(ctx, obj, metav1.UpdateOptions{})
			}
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func buildRemoteConfigForRestore(ctx context.Context, c client.Client, remoteName string) (*rest.Config, error) {
	var remote backupv1alpha1.RemoteCluster
	if err := c.Get(ctx, client.ObjectKey{Name: remoteName}, &remote); err != nil {
		return nil, err
	}

	secret := &corev1.Secret{}
	key := client.ObjectKey{Name: remote.Spec.Auth.SecretRef.Name, Namespace: remote.Spec.Auth.SecretRef.Namespace}
	if key.Namespace == "" {
		key.Namespace = operatorNamespace()
	}
	if err := c.Get(ctx, key, secret); err != nil {
		return nil, err
	}

	switch remote.Spec.Auth.Method {
	case backupv1alpha1.RemoteAuthKubeconfig:
		raw, ok := secret.Data["kubeconfig"]
		if !ok {
			return nil, fmt.Errorf("kubeconfig key not found in remote secret")
		}
		return clientcmd.RESTConfigFromKubeConfig(raw)
	case backupv1alpha1.RemoteAuthServiceAccountToken, "":
		token, ok := secret.Data["token"]
		if !ok {
			return nil, fmt.Errorf("token key not found in remote secret")
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
