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

// ClusterRestoreSpec defines a cluster-scoped restore request.
type ClusterRestoreSpec struct {
	RestoreSpec `json:",inline"`
}

// ClusterRestoreStatus defines status for cluster restores.
type ClusterRestoreStatus struct {
	RestoreStatus `json:",inline"`
}

// ClusterRestore is the Schema for the clusterrestores API.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster,shortName=cr
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"
type ClusterRestore struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ClusterRestoreSpec   `json:"spec,omitempty"`
	Status ClusterRestoreStatus `json:"status,omitempty"`
}

// ClusterRestoreList contains a list of ClusterRestore.
// +kubebuilder:object:root=true
type ClusterRestoreList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ClusterRestore `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ClusterRestore{}, &ClusterRestoreList{})
}
