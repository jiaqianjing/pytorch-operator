// Copyright 2020 The Kubeflow Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package v1

import (
	common "github.com/kubeflow/common/job_controller/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +resource:path=pytorchjob

// Represents a PyTorchJob resource.
type PyTorchJob struct {
	// Standard Kubernetes type metadata.
	// 匿名继承自定义 struct 类型的成员
	// `json` 为 struct 的 tag, 一般用作解释、 struct 转 json 映射变量；也可以通过 reflect 获取其值用作其他逻辑，如验证数据
	metav1.TypeMeta `json:",inline"`

	// Standard Kubernetes object's metadata.
	// 标准的 k8s 元数据类型
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Specification of the desired state of the PyTorchJob.
	// 定义 PytorchJob 资源所需要的状态
	Spec PyTorchJobSpec `json:"spec,omitempty"`

	// Most recently observed status of the PyTorchJob.
	// Read-only (modified by the system).
	// 获取最新的 PytorchJob 的状态，继承 kubeflow 通用的获取状态数据类型
	Status common.JobStatus `json:"status,omitempty"`
}

// PyTorchJobSpec is a desired state description of the PyTorchJob.
// 创建 PyTorchJob 的状态描述
type PyTorchJobSpec struct {
	// Specifies the duration (in seconds) since startTime during which the job can remain active
	// before it is terminated. Must be a positive integer.
	// This setting applies only to pods where restartPolicy is OnFailure or Always.
	// +optional
	// 可选，表明设置 pod 从 startTime 开始保活的时间，必须是正整数，适用于 “OnFailure 和 Always” 两种重启策略
	ActiveDeadlineSeconds *int64 `json:"activeDeadlineSeconds,omitempty"`

	// Number of retries before marking this job as failed.
	// +optional
	// 可选，设置 pod 标记为 failed 之前的重试次数
	BackoffLimit *int32 `json:"backoffLimit,omitempty"`

	// Defines the policy for cleaning up pods after the PyTorchJob completes.
	// Defaults to None.
	// 定义 PyTorchJob 任务结束后 pods 的清理策略，默认值： None
	CleanPodPolicy *common.CleanPodPolicy `json:"cleanPodPolicy,omitempty"`

	// Defines the TTL for cleaning up finished PyTorchJobs (temporary
	// before Kubernetes adds the cleanup controller).
	// It may take extra ReconcilePeriod seconds for the cleanup, since
	// reconcile gets called periodically.
	// Defaults to infinite.
	// 默认值：无限时间； 定义了清理 PyTorchJobs 的生存时间（在 k8s 添加清理控制器之前是临时的）
	// 清理可能需要额外消耗执行 ReconcilePeriod 的时间（轮询同步状态）
	TTLSecondsAfterFinished *int32 `json:"ttlSecondsAfterFinished,omitempty"`

	// A map of PyTorchReplicaType (type) to ReplicaSpec (value). Specifies the PyTorch cluster configuration.
	// For example,
	//   {
	//     "Master": PyTorchReplicaSpec,
	//     "Worker": PyTorchReplicaSpec,
	//   }
	// 指定 PyTorchReplicaType (type) 和 RelicaSpec (value)的映射关系, 指定 Pytorch 作业 cluster 的配置，比如：
	//   {
	//     "Master": PyTorchReplicaSpec,
	//     "Worker": PyTorchReplicaSpec,
	//   }
	PyTorchReplicaSpecs map[PyTorchReplicaType]*common.ReplicaSpec `json:"pytorchReplicaSpecs"`
}

// PyTorchReplicaType is the type for PyTorchReplica. Can be one of "Master" or "Worker".
// PyTorchReplicaType 和 RelicaType 底层类型都是 string, 但是不同的数据类型，用在不同的 crd 上，刻意区分是为了避免混淆；
// PyTorchRelica 的 type 只有  Master 和 Work 两种类型的 ReplicaType
type PyTorchReplicaType common.ReplicaType

const (
	// PyTorchReplicaTypeMaster is the type of Master of distributed PyTorch
	PyTorchReplicaTypeMaster PyTorchReplicaType = "Master"

	// PyTorchReplicaTypeWorker is the type for workers of distributed PyTorch.
	PyTorchReplicaTypeWorker PyTorchReplicaType = "Worker"
)

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +resource:path=pytorchjobs

// PyTorchJobList is a list of PyTorchJobs.
type PyTorchJobList struct {
	// Standard type metadata.
	metav1.TypeMeta `json:",inline"`

	// Standard list metadata.
	metav1.ListMeta `json:"metadata,omitempty"`

	// List of PyTorchJobs.
	Items []PyTorchJob `json:"items"`
}
