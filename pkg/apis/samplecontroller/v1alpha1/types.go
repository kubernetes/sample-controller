/*
Copyright 2017 The Kubernetes Authors.

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

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SessionJob is a specification for a SessionJob resource
type SessionJob struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SessionJobSpec   `json:"spec"`
	Status SessionJobStatus `json:"status"`
}

// SessionJobSpec is the spec for a SessionJob resource
type SessionJobSpec struct {
	DeploymentName string `json:"deploymentName"`
	Replicas       *int32 `json:"replicas"`
	TaskCount      *int32 `json:"taskCount"`
	TaskRuntime    *int32 `json:"taskRuntime"`
}

// SessionJobStatus is the status for a SessionJob resource
type SessionJobStatus struct {
	AvailableReplicas int32 `json:"availableReplicas"`
	RunningTasks      int32 `json:"runningTasks"`
	PendingTasks      int32 `json:"pendingTasks"`
	DoneTasks         int32 `json:"doneTasks"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object

// SessionJobList is a list of SessionJob resources
type SessionJobList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata"`

	Items []SessionJob `json:"items"`
}
