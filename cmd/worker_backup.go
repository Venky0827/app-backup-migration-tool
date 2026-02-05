package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	backupv1alpha1 "example.com/backup-operator/api/v1alpha1"
	"example.com/backup-operator/internal/resolve"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	sigsyaml "sigs.k8s.io/yaml"
)

const (
	artifactFileName = "backup.tar.gz"
)

type backupObject struct {
	kind                    string
	name                    string
	namespace               string
	spec                    backupv1alpha1.BackupSpec
	namespaces              *backupv1alpha1.NamespaceSelector
	includeClusterResources bool
	status                  backupv1alpha1.BackupStatus
	updateStatus            func(status backupv1alpha1.BackupStatus) error
}

func runBackupWorker(ctx context.Context, c client.Client, restCfg *rest.Config, cfg workerConfig) error {
	backup, err := loadBackupObject(ctx, c, cfg)
	if err != nil {
		return err
	}

	storageName := ""
	if backup.spec.StorageRef != nil {
		storageName = backup.spec.StorageRef.Name
	}
	storage, err := resolve.StorageLocation(ctx, c, storageName)
	if err != nil {
		return backup.updateStatus(failedBackupStatus(backup.status, backup, err.Error()))
	}

	now := time.Now().UTC()
	timestamp := now.Format("20060102T150405Z")

	workDir, err := os.MkdirTemp("", "backup-worker-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(workDir)

	resourcesBytes, err := exportResources(ctx, restCfg, backup)
	if err != nil {
		return backup.updateStatus(failedBackupStatus(backup.status, backup, err.Error()))
	}

	snapshotsBytes, err := exportSnapshots(ctx, restCfg, backup)
	if err != nil {
		return backup.updateStatus(failedBackupStatus(backup.status, backup, err.Error()))
	}

	metadataBytes, err := buildBackupMetadata(backup, storage, timestamp)
	if err != nil {
		return backup.updateStatus(failedBackupStatus(backup.status, backup, err.Error()))
	}

	artifactPath := filepath.Join(workDir, artifactFileName)
	files := map[string][]byte{
		"metadata.json":  metadataBytes,
		"resources.yaml": resourcesBytes,
		"snapshots.yaml": snapshotsBytes,
	}
	if err := writeTarGz(artifactPath, files); err != nil {
		return backup.updateStatus(failedBackupStatus(backup.status, backup, err.Error()))
	}

	location, err := storeArtifact(ctx, c, storage, artifactPath, backup, timestamp)
	if err != nil {
		return backup.updateStatus(failedBackupStatus(backup.status, backup, err.Error()))
	}

	completed := backup.status
	completed.Phase = backupv1alpha1.BackupPhaseCompleted
	completed.CompletedAt = &metav1.Time{Time: now}
	completed.ArtifactLocation = location
	completed.Message = "backup completed"
	completed.ObservedGeneration = backup.status.ObservedGeneration
	return backup.updateStatus(completed)
}

func loadBackupObject(ctx context.Context, c client.Client, cfg workerConfig) (*backupObject, error) {
	switch cfg.kind {
	case "Backup":
		var backup backupv1alpha1.Backup
		if err := c.Get(ctx, client.ObjectKey{Namespace: cfg.namespace, Name: cfg.name}, &backup); err != nil {
			return nil, err
		}
		return &backupObject{
			kind:      "Backup",
			name:      backup.Name,
			namespace: backup.Namespace,
			spec:      backup.Spec,
			status:    backup.Status,
			updateStatus: func(status backupv1alpha1.BackupStatus) error {
				backup.Status = status
				return c.Status().Update(ctx, &backup)
			},
		}, nil
	case "ClusterBackup":
		var backup backupv1alpha1.ClusterBackup
		if err := c.Get(ctx, client.ObjectKey{Name: cfg.name}, &backup); err != nil {
			return nil, err
		}
		includeCluster := true
		if backup.Spec.IncludeClusterResources != nil {
			includeCluster = *backup.Spec.IncludeClusterResources
		}
		return &backupObject{
			kind:                    "ClusterBackup",
			name:                    backup.Name,
			namespace:               "",
			spec:                    backup.Spec.BackupSpec,
			namespaces:              backup.Spec.Namespaces,
			includeClusterResources: includeCluster,
			status:                  backup.Status.BackupStatus,
			updateStatus: func(status backupv1alpha1.BackupStatus) error {
				backup.Status.BackupStatus = status
				return c.Status().Update(ctx, &backup)
			},
		}, nil
	default:
		return nil, fmt.Errorf("unsupported backup kind %q", cfg.kind)
	}
}

func failedBackupStatus(current backupv1alpha1.BackupStatus, backup *backupObject, message string) backupv1alpha1.BackupStatus {
	now := metav1.Now()
	current.Phase = backupv1alpha1.BackupPhaseFailed
	current.Message = message
	current.CompletedAt = &now
	current.ObservedGeneration = current.ObservedGeneration
	return current
}

func buildBackupMetadata(backup *backupObject, storage *backupv1alpha1.BackupStorageLocation, timestamp string) ([]byte, error) {
	metadata := map[string]any{
		"kind":        backup.kind,
		"name":        backup.name,
		"namespace":   backup.namespace,
		"clusterID":   clusterID(),
		"timestamp":   timestamp,
		"storageType": string(storage.Spec.Type),
	}
	return json.MarshalIndent(metadata, "", "  ")
}

func exportResources(ctx context.Context, restCfg *rest.Config, backup *backupObject) ([]byte, error) {
	if backup.spec.Export != nil && backup.spec.Export.Enabled != nil && !*backup.spec.Export.Enabled {
		return []byte(""), nil
	}

	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	disco, err := discovery.NewDiscoveryClientForConfig(restCfg)
	if err != nil {
		return nil, err
	}
	labelSelector := ""
	annotationSelector := map[string]string{}
	includedResources := []string{}
	excludedResources := defaultExcludedResources()
	exportFormat := backupv1alpha1.ExportFormatYAML
	if backup.spec.Resources != nil {
		if backup.spec.Resources.LabelSelector != nil {
			selector, err := metav1.LabelSelectorAsSelector(backup.spec.Resources.LabelSelector)
			if err != nil {
				return nil, err
			}
			labelSelector = selector.String()
		}
		if backup.spec.Resources.AnnotationSelector != nil {
			annotationSelector = backup.spec.Resources.AnnotationSelector
		}
		if len(backup.spec.Resources.IncludedResources) > 0 {
			includedResources = backup.spec.Resources.IncludedResources
		}
		if len(backup.spec.Resources.ExcludedResources) > 0 {
			excludedResources = append(excludedResources, backup.spec.Resources.ExcludedResources...)
		}
	}
	if backup.spec.Export != nil && backup.spec.Export.Format != "" {
		exportFormat = backup.spec.Export.Format
	}

	includeCluster := backup.kind == "ClusterBackup" && backup.includeClusterResources

	namespaces, err := resolveNamespaces(ctx, restCfg, backup)
	if err != nil {
		return nil, err
	}

	resources, err := disco.ServerPreferredResources()
	if err != nil && !discovery.IsGroupDiscoveryFailedError(err) {
		return nil, err
	}

	selected := []*unstructured.Unstructured{}
	includeSet := sets.NewString(normalizeResourceNames(includedResources)...)
	excludeSet := sets.NewString(normalizeResourceNames(excludedResources)...)

	for _, list := range resources {
		gv, err := schema.ParseGroupVersion(list.GroupVersion)
		if err != nil {
			continue
		}
		for _, res := range list.APIResources {
			if strings.Contains(res.Name, "/") {
				continue
			}
			if !resourceSupportsList(res.Verbs) {
				continue
			}

			ids := resourceIdentifiers(gv.Group, res)
			if includeSet.Len() > 0 && !matchesAny(ids, includeSet) {
				continue
			}
			if matchesAny(ids, excludeSet) {
				continue
			}
			if shouldSkipKind(res.Kind) {
				continue
			}

			gvr := schema.GroupVersionResource{Group: gv.Group, Version: gv.Version, Resource: res.Name}
			if res.Namespaced {
				for _, ns := range namespaces {
					listOpts := metav1.ListOptions{LabelSelector: labelSelector}
					items, err := dyn.Resource(gvr).Namespace(ns).List(ctx, listOpts)
					if err != nil {
						continue
					}
					for i := range items.Items {
						item := items.Items[i]
						if !matchAnnotations(item.GetAnnotations(), annotationSelector) {
							continue
						}
						sanitizeObject(&item)
						selected = append(selected, &item)
					}
				}
			} else if includeCluster {
				listOpts := metav1.ListOptions{LabelSelector: labelSelector}
				items, err := dyn.Resource(gvr).List(ctx, listOpts)
				if err != nil {
					continue
				}
				for i := range items.Items {
					item := items.Items[i]
					if !matchAnnotations(item.GetAnnotations(), annotationSelector) {
						continue
					}
					sanitizeObject(&item)
					selected = append(selected, &item)
				}
			}
		}
	}

	sort.SliceStable(selected, func(i, j int) bool {
		return resourcePriority(selected[i]) < resourcePriority(selected[j])
	})

	var buffer bytes.Buffer
	for _, obj := range selected {
		var out []byte
		if exportFormat == backupv1alpha1.ExportFormatJSON {
			out, err = json.Marshal(obj.Object)
		} else {
			out, err = sigsyaml.Marshal(obj.Object)
		}
		if err != nil {
			return nil, err
		}
		if len(out) == 0 {
			continue
		}
		buffer.WriteString("---\n")
		buffer.Write(out)
	}

	return buffer.Bytes(), nil
}

func exportSnapshots(ctx context.Context, restCfg *rest.Config, backup *backupObject) ([]byte, error) {
	if backup.spec.Snapshot == nil {
		return []byte(""), nil
	}

	enabled := true
	if backup.spec.Snapshot.Enabled != nil {
		enabled = *backup.spec.Snapshot.Enabled
	} else if !backup.spec.Snapshot.IncludeAllPVCs && backup.spec.Snapshot.PVCSelector == nil {
		enabled = false
	}

	if !enabled {
		return []byte(""), nil
	}

	dyn, err := dynamic.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}

	pvcSelector := backup.spec.Snapshot.PVCSelector
	labelSelector := labels.Everything()
	if pvcSelector != nil {
		labelSelector, err = metav1.LabelSelectorAsSelector(pvcSelector)
		if err != nil {
			return nil, err
		}
	}

	namespaces, err := resolveNamespaces(ctx, restCfg, backup)
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{Group: "snapshot.storage.k8s.io", Version: "v1", Resource: "volumesnapshots"}
	pvcGvr := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}

	var snapshots []*unstructured.Unstructured
	timestamp := time.Now().UTC().Format("20060102T150405Z")
	for _, ns := range namespaces {
		pvcs, err := dyn.Resource(pvcGvr).Namespace(ns).List(ctx, metav1.ListOptions{LabelSelector: labelSelector.String()})
		if err != nil {
			continue
		}
		for i := range pvcs.Items {
			pvc := pvcs.Items[i]
			name := sanitizeName(fmt.Sprintf("%s-%s-%s", backup.name, pvc.GetName(), timestamp))
			snapshot := &unstructured.Unstructured{Object: map[string]any{
				"apiVersion": "snapshot.storage.k8s.io/v1",
				"kind":       "VolumeSnapshot",
				"metadata": map[string]any{
					"name":      name,
					"namespace": ns,
					"labels": map[string]any{
						"backup.example.com/backup-name": backup.name,
					},
				},
				"spec": map[string]any{
					"source": map[string]any{
						"persistentVolumeClaimName": pvc.GetName(),
					},
				},
			}}
			if backup.spec.Snapshot.VolumeSnapshotClassName != nil {
				setNestedField(snapshot.Object, *backup.spec.Snapshot.VolumeSnapshotClassName, "spec", "volumeSnapshotClassName")
			}

			created, err := dyn.Resource(gvr).Namespace(ns).Create(ctx, snapshot, metav1.CreateOptions{})
			if err != nil {
				if errors.IsAlreadyExists(err) {
					created, err = dyn.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
					if err == nil {
						snapshots = append(snapshots, created)
					}
					continue
				}
				continue
			}
			snapshots = append(snapshots, created)
		}
	}

	var buffer bytes.Buffer
	for _, snap := range snapshots {
		sanitizeObject(snap)
		out, err := sigsyaml.Marshal(snap.Object)
		if err != nil {
			return nil, err
		}
		if len(out) == 0 {
			continue
		}
		buffer.WriteString("---\n")
		buffer.Write(out)
	}
	return buffer.Bytes(), nil
}

func resolveNamespaces(ctx context.Context, restCfg *rest.Config, backup *backupObject) ([]string, error) {
	if backup.kind == "Backup" {
		return []string{backup.namespace}, nil
	}

	clientset, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, err
	}

	var namespaces []string
	if backup.namespaces != nil && len(backup.namespaces.Included) > 0 {
		namespaces = append(namespaces, backup.namespaces.Included...)
	} else {
		list, err := clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}
		for i := range list.Items {
			namespaces = append(namespaces, list.Items[i].Name)
		}
	}

	if backup.namespaces != nil && len(backup.namespaces.Excluded) > 0 {
		excluded := sets.NewString(backup.namespaces.Excluded...)
		filtered := []string{}
		for _, ns := range namespaces {
			if !excluded.Has(ns) {
				filtered = append(filtered, ns)
			}
		}
		namespaces = filtered
	}

	sort.Strings(namespaces)
	return namespaces, nil
}

func writeTarGz(path string, files map[string][]byte) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()

	gzipWriter := gzip.NewWriter(file)
	defer gzipWriter.Close()

	tarWriter := tar.NewWriter(gzipWriter)
	defer tarWriter.Close()

	for name, data := range files {
		head := &tar.Header{
			Name: name,
			Mode: 0600,
			Size: int64(len(data)),
		}
		if err := tarWriter.WriteHeader(head); err != nil {
			return err
		}
		if _, err := tarWriter.Write(data); err != nil {
			return err
		}
	}
	return nil
}

func sanitizeObject(obj *unstructured.Unstructured) {
	unstructured.RemoveNestedField(obj.Object, "status")
	metadata, found, _ := unstructured.NestedMap(obj.Object, "metadata")
	if found {
		delete(metadata, "uid")
		delete(metadata, "resourceVersion")
		delete(metadata, "generation")
		delete(metadata, "managedFields")
		delete(metadata, "selfLink")
		delete(metadata, "creationTimestamp")
		if ann, ok := metadata["annotations"].(map[string]any); ok {
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			metadata["annotations"] = ann
		}
		_ = unstructured.SetNestedMap(obj.Object, metadata, "metadata")
	}

	if obj.GetKind() == "Service" {
		unstructured.RemoveNestedField(obj.Object, "spec", "clusterIP")
		unstructured.RemoveNestedField(obj.Object, "spec", "clusterIPs")
		unstructured.RemoveNestedField(obj.Object, "spec", "healthCheckNodePort")
	}
}

func resourceSupportsList(verbs []string) bool {
	for _, verb := range verbs {
		if verb == "list" {
			return true
		}
	}
	return false
}

func resourceIdentifiers(group string, res metav1.APIResource) []string {
	group = strings.ToLower(group)
	name := strings.ToLower(res.Name)
	kind := strings.ToLower(res.Kind)
	identifiers := []string{name, kind}
	if group != "" {
		identifiers = append(identifiers, fmt.Sprintf("%s.%s", name, group))
		identifiers = append(identifiers, fmt.Sprintf("%s.%s", kind, group))
	}
	return identifiers
}

func normalizeResourceNames(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		result = append(result, strings.ToLower(strings.TrimSpace(value)))
	}
	return result
}

func matchesAny(ids []string, set sets.String) bool {
	for _, id := range ids {
		if set.Has(id) {
			return true
		}
	}
	return false
}

func defaultExcludedResources() []string {
	return []string{
		"events",
		"events.events.k8s.io",
		"leases",
		"endpoints",
		"endpointslices",
		"bindings",
		"replicasets",
		"controllerrevisions",
		"pods",
	}
}

func shouldSkipKind(kind string) bool {
	switch kind {
	case "Event", "Lease", "ControllerRevision", "ReplicaSet", "Pod", "EndpointSlice", "Endpoints", "Binding":
		return true
	default:
		return false
	}
}

func matchAnnotations(annotations map[string]string, selector map[string]string) bool {
	if len(selector) == 0 {
		return true
	}
	for key, val := range selector {
		if annotations == nil {
			return false
		}
		if annotations[key] != val {
			return false
		}
	}
	return true
}

func resourcePriority(obj *unstructured.Unstructured) int {
	kind := obj.GetKind()
	switch kind {
	case "Namespace":
		return 0
	case "CustomResourceDefinition":
		return 1
	case "ClusterRole", "ClusterRoleBinding", "Role", "RoleBinding":
		return 2
	case "ServiceAccount":
		return 3
	case "PersistentVolume", "StorageClass", "VolumeSnapshotClass":
		return 4
	case "PersistentVolumeClaim":
		return 5
	case "ConfigMap", "Secret":
		return 6
	case "Deployment", "StatefulSet", "DaemonSet", "Job", "CronJob":
		return 7
	case "Service", "Ingress", "Route":
		return 8
	default:
		return 100
	}
}

func sanitizeName(name string) string {
	name = strings.ToLower(name)
	name = strings.ReplaceAll(name, "_", "-")
	if len(name) > 63 {
		return name[:63]
	}
	return name
}

func setNestedField(obj map[string]any, value any, fields ...string) {
	current := obj
	for i := 0; i < len(fields)-1; i++ {
		field := fields[i]
		next, ok := current[field].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[field] = next
		}
		current = next
	}
	current[fields[len(fields)-1]] = value
}
