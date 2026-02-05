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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ClusterBackupSpec defines a cluster-wide backup request.
type ClusterBackupSpec struct {
	BackupSpec `json:",inline"`
	// Namespaces controls which namespaces are included.
	Namespaces *NamespaceSelector `json:"namespaces,omitempty"`
	// IncludeClusterResources includes cluster-scoped objects when true.
	IncludeClusterResources *bool `json:"includeClusterResources,omitempty"`
}

// ClusterBackupStatus defines status for cluster backups.
type ClusterBackupStatus struct {
	BackupStatus `json:",inline"`
}

// ClusterBackup is the Schema for the clusterbackups API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cb
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Location",type=string,JSONPath=".status.artifactLocation"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ClusterBackup struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterBackupSpec   `json:"spec,omitempty"`
	Status ClusterBackupStatus `json:"status,omitempty"`
}

// ClusterBackupList contains a list of ClusterBackup.
// +kubebuilder:object:root=true
type ClusterBackupList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterBackup `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterBackup{}, &ClusterBackupList{})
}
