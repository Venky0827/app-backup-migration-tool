/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ExecutionMode describes how the controller should track job completion.
// +kubebuilder:validation:Enum=Async;Sync
// +kubebuilder:default=Async
type ExecutionMode string

const (
	ExecutionModeAsync ExecutionMode = "Async"
	ExecutionModeSync  ExecutionMode = "Sync"
)

// ExportFormat describes the serialization format for exported manifests.
// +kubebuilder:validation:Enum=yaml;json
// +kubebuilder:default=yaml
type ExportFormat string

const (
	ExportFormatYAML ExportFormat = "yaml"
	ExportFormatJSON ExportFormat = "json"
)

// BackupPhase indicates the lifecycle state of a backup.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
// +kubebuilder:default=Pending
type BackupPhase string

const (
	BackupPhasePending   BackupPhase = "Pending"
	BackupPhaseRunning   BackupPhase = "Running"
	BackupPhaseCompleted BackupPhase = "Completed"
	BackupPhaseFailed    BackupPhase = "Failed"
)

// RestorePhase indicates the lifecycle state of a restore.
// +kubebuilder:validation:Enum=Pending;Running;Completed;Failed
// +kubebuilder:default=Pending
type RestorePhase string

const (
	RestorePhasePending   RestorePhase = "Pending"
	RestorePhaseRunning   RestorePhase = "Running"
	RestorePhaseCompleted RestorePhase = "Completed"
	RestorePhaseFailed    RestorePhase = "Failed"
)

// StorageLocationType describes where backup artifacts are stored.
// +kubebuilder:validation:Enum=s3;nfs
type StorageLocationType string

const (
	StorageLocationS3  StorageLocationType = "s3"
	StorageLocationNFS StorageLocationType = "nfs"
)

// StorageLocationPhase indicates the readiness of a storage location.
// +kubebuilder:validation:Enum=Pending;Available;Unavailable
// +kubebuilder:default=Pending
type StorageLocationPhase string

const (
	StorageLocationPending     StorageLocationPhase = "Pending"
	StorageLocationAvailable   StorageLocationPhase = "Available"
	StorageLocationUnavailable StorageLocationPhase = "Unavailable"
)

// RestoreOverwritePolicy defines how restore handles existing objects.
// +kubebuilder:validation:Enum=Merge;Replace;Skip
// +kubebuilder:default=Merge
type RestoreOverwritePolicy string

const (
	RestoreOverwriteMerge   RestoreOverwritePolicy = "Merge"
	RestoreOverwriteReplace RestoreOverwritePolicy = "Replace"
	RestoreOverwriteSkip    RestoreOverwritePolicy = "Skip"
)

// RemoteAuthMethod defines how the operator authenticates to a remote cluster.
// +kubebuilder:validation:Enum=ServiceAccountToken;Kubeconfig
// +kubebuilder:default=ServiceAccountToken
type RemoteAuthMethod string

const (
	RemoteAuthServiceAccountToken RemoteAuthMethod = "ServiceAccountToken"
	RemoteAuthKubeconfig          RemoteAuthMethod = "Kubeconfig"
)

// ExportSpec controls manifest export settings.
type ExportSpec struct {
	// Enabled toggles manifest export.
	Enabled *bool `json:"enabled,omitempty"`
	// Format defines the serialization format.
	Format ExportFormat `json:"format,omitempty"`
}

// SnapshotSpec controls volume snapshot settings.
type SnapshotSpec struct {
	// Enabled toggles volume snapshot collection.
	Enabled *bool `json:"enabled,omitempty"`
	// IncludeAllPVCs snapshots all PVCs in scope when true.
	IncludeAllPVCs bool `json:"includeAllPVCs,omitempty"`
	// PVCSelector selects PVCs to snapshot.
	PVCSelector *metav1.LabelSelector `json:"pvcSelector,omitempty"`
	// VolumeSnapshotClassName forces a specific VolumeSnapshotClass.
	VolumeSnapshotClassName *string `json:"volumeSnapshotClassName,omitempty"`
}

// ResourceSelector selects Kubernetes API objects for export.
type ResourceSelector struct {
	// IncludedResources limits backups to these resource names (group/resource or Kind).
	IncludedResources []string `json:"includedResources,omitempty"`
	// ExcludedResources omits these resource names.
	ExcludedResources []string `json:"excludedResources,omitempty"`
	// LabelSelector filters objects by labels.
	LabelSelector *metav1.LabelSelector `json:"labelSelector,omitempty"`
	// AnnotationSelector filters objects by annotations (exact match).
	AnnotationSelector map[string]string `json:"annotationSelector,omitempty"`
}

// NamespaceSelector controls which namespaces are in scope for a cluster backup.
type NamespaceSelector struct {
	Included []string `json:"included,omitempty"`
	Excluded []string `json:"excluded,omitempty"`
}

// BackupSpec defines common backup inputs.
type BackupSpec struct {
	// StorageRef selects a BackupStorageLocation.
	StorageRef *corev1.LocalObjectReference `json:"storageRef,omitempty"`
	// Export controls manifest export behavior.
	Export *ExportSpec `json:"export,omitempty"`
	// Snapshot controls snapshot behavior.
	Snapshot *SnapshotSpec `json:"snapshot,omitempty"`
	// Resources filters which objects are exported.
	Resources *ResourceSelector `json:"resources,omitempty"`
	// ExecutionMode controls when the backup is marked complete.
	ExecutionMode ExecutionMode `json:"executionMode,omitempty"`
	// Timeout limits how long a backup may run.
	Timeout *metav1.Duration `json:"timeout,omitempty"`
	// TTL defines how long to keep backup artifacts.
	TTL *metav1.Duration `json:"ttl,omitempty"`
	// RetainUntil keeps backup artifacts until the given time.
	RetainUntil *metav1.Time `json:"retainUntil,omitempty"`
}

// BackupStatus defines common backup status fields.
type BackupStatus struct {
	Phase              BackupPhase        `json:"phase,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	StartedAt          *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time       `json:"completedAt,omitempty"`
	ArtifactLocation   string             `json:"artifactLocation,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Message            string             `json:"message,omitempty"`
}

// RestoreSourceRef identifies the backup to restore from.
// Kind must be Backup or ClusterBackup.
type RestoreSourceRef struct {
	// +kubebuilder:validation:Enum=Backup;ClusterBackup
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"`
}

// RestoreSpec defines common restore inputs.
type RestoreSpec struct {
	SourceRef        RestoreSourceRef             `json:"sourceRef"`
	TargetClusterRef *corev1.LocalObjectReference `json:"targetClusterRef,omitempty"`
	NamespaceMapping map[string]string            `json:"namespaceMapping,omitempty"`
	OverwritePolicy  RestoreOverwritePolicy       `json:"overwritePolicy,omitempty"`
	ExecutionMode    ExecutionMode                `json:"executionMode,omitempty"`
	Timeout          *metav1.Duration             `json:"timeout,omitempty"`
}

// RestoreStatus defines common restore status fields.
type RestoreStatus struct {
	Phase              RestorePhase       `json:"phase,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	StartedAt          *metav1.Time       `json:"startedAt,omitempty"`
	CompletedAt        *metav1.Time       `json:"completedAt,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Message            string             `json:"message,omitempty"`
}

// S3LocationSpec configures an S3-compatible storage backend.
type S3LocationSpec struct {
	Endpoint        string                 `json:"endpoint,omitempty"`
	Bucket          string                 `json:"bucket,omitempty"`
	Prefix          string                 `json:"prefix,omitempty"`
	Region          string                 `json:"region,omitempty"`
	ForcePathStyle  bool                   `json:"forcePathStyle,omitempty"`
	InsecureSkipTLS bool                   `json:"insecureSkipTLS,omitempty"`
	CABundle        []byte                 `json:"caBundle,omitempty"`
	SecretRef       corev1.SecretReference `json:"secretRef,omitempty"`
}

// NFSLocationSpec configures an NFS storage backend.
type NFSLocationSpec struct {
	Server   string                       `json:"server,omitempty"`
	Path     string                       `json:"path,omitempty"`
	PVCRef   *corev1.LocalObjectReference `json:"pvcRef,omitempty"`
	ReadOnly bool                         `json:"readOnly,omitempty"`
	// MountOptions are passed to the NFS mount when supported by the runtime.
	MountOptions []string `json:"mountOptions,omitempty"`
	// CredentialsSecretRef optionally references credentials (e.g., for Kerberos/CSI-backed NFS).
	CredentialsSecretRef *corev1.SecretReference `json:"credentialsSecretRef,omitempty"`
}

// BackupStorageLocationSpec defines where backup artifacts are stored.
type BackupStorageLocationSpec struct {
	Type    StorageLocationType `json:"type"`
	S3      *S3LocationSpec     `json:"s3,omitempty"`
	NFS     *NFSLocationSpec    `json:"nfs,omitempty"`
	Default bool                `json:"default,omitempty"`
}

// BackupStorageLocationStatus reports storage validation results.
type BackupStorageLocationStatus struct {
	Phase         StorageLocationPhase `json:"phase,omitempty"`
	Conditions    []metav1.Condition   `json:"conditions,omitempty"`
	LastValidated *metav1.Time         `json:"lastValidated,omitempty"`
	Message       string               `json:"message,omitempty"`
}

// RemoteClusterAuth describes how to authenticate to a remote cluster.
type RemoteClusterAuth struct {
	Method    RemoteAuthMethod       `json:"method,omitempty"`
	SecretRef corev1.SecretReference `json:"secretRef,omitempty"`
}

// RemoteClusterSpec defines a peer cluster reference.
type RemoteClusterSpec struct {
	ClusterID       string            `json:"clusterID,omitempty"`
	APIServer       string            `json:"apiServer"`
	Auth            RemoteClusterAuth `json:"auth"`
	CABundle        []byte            `json:"caBundle,omitempty"`
	InsecureSkipTLS bool              `json:"insecureSkipTLS,omitempty"`
}

// RemoteClusterStatus reports connectivity status for the remote cluster.
type RemoteClusterStatus struct {
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	LastValidated      *metav1.Time       `json:"lastValidated,omitempty"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Message            string             `json:"message,omitempty"`
}
