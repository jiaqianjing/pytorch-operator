// Copyright 2018 The Kubeflow Authors
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

// Package controller provides a Kubernetes controller for a PyTorchJob resource.
package pytorch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	log "github.com/sirupsen/logrus"
	v1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"

	common "github.com/kubeflow/common/job_controller/api/v1"
	pyv1 "github.com/kubeflow/pytorch-operator/pkg/apis/pytorch/v1"
	"github.com/kubeflow/tf-operator/pkg/common/jobcontroller"
	pylogger "github.com/kubeflow/tf-operator/pkg/logger"
	train_util "github.com/kubeflow/tf-operator/pkg/util/train"
)

const (
	gangSchedulingPodGroupAnnotation = "scheduling.k8s.io/group-name"
	// podTemplateRestartPolicyReason is the warning reason when the restart
	// policy is set in pod template.
	podTemplateRestartPolicyReason = "SettedPodTemplateRestartPolicy"
	exitedWithCodeReason           = "ExitedWithCode"
	// podTemplateSchedulerNameReason is the warning reason when other scheduler name is set
	// in pod templates with gang-scheduling enabled
	podTemplateSchedulerNameReason = "SettedPodTemplateSchedulerName"
)

// reconcilePods checks and updates pods for each given PyTorchReplicaSpec.
// It will requeue the job in case of an error while creating/deleting pods.
// PytorchRelica Type 就两类， Master / Worker
func (pc *PyTorchController) reconcilePods(
	job *pyv1.PyTorchJob,
	pods []*v1.Pod,
	rtype pyv1.PyTorchReplicaType,
	spec *common.ReplicaSpec, rstatus map[string]v1.PodPhase) error {

	// Convert PyTorchReplicaType to lower string.
	rt := strings.ToLower(string(rtype))
	logger := pylogger.LoggerForReplica(job, rt)

	// Get all pods for the type rt.
	// 获取所有 master or worker 的 Pods 数组
	pods, err := pc.FilterPodsForReplicaType(pods, rt)
	if err != nil {
		return err
	}
	replicas := int(*spec.Replicas)
	restart := false
	masterRole := false

	// 初始化 PyTorchJob 的 Status.ReplicaStatuses = {"Master":, "Worker": }
	initializePyTorchReplicaStatuses(job, rtype)

	podSlices := getPodSlices(pods, replicas, logger)
	for index, podSlice := range podSlices {
		masterRole = false
		if len(podSlice) > 1 {
			logger.Warningf("We have too many pods for %s %d", rt, index)
			// TODO(gaocegege): Kill some pods.
		} else if len(podSlice) == 0 {
			logger.Infof("Need to create new pod: %s-%d", rt, index)

			//Pytorch Job will have exactly one Master pod available
			if rtype == pyv1.PyTorchReplicaTypeMaster {
				masterRole = true
			}
			// replica 创建对应 index 的 Pod
			err = pc.createNewPod(job, rtype, strconv.Itoa(index), spec, masterRole)
			if err != nil {
				return err
			}
		} else {
			// Check the status of the current pod.
			pod := podSlice[0]
			// Check if the pod is retryable.
			if spec.RestartPolicy == common.RestartPolicyExitCode {
				var exitCode int32
				for _, status := range pod.Status.ContainerStatuses {
					state := status.State
					// Get the exit code of the pytorch container.
					if status.Name == pyv1.DefaultContainerName && state.Terminated != nil {
						exitCode = state.Terminated.ExitCode
						logger.Infof("Pod: %v.%v exited with code %v", pod.Namespace, pod.Name, exitCode)
						pc.Recorder.Eventf(job, v1.EventTypeNormal, exitedWithCodeReason, "Pod: %v.%v exited with code %v", pod.Namespace, pod.Name, exitCode)
					}
				}
				if pod.Status.Phase == v1.PodFailed && train_util.IsRetryableExitCode(exitCode) {
					logger.Infof("Need to restart the pod: %v.%v", pod.Namespace, pod.Name)
					if err := pc.PodControl.DeletePod(pod.Namespace, pod.Name, job); err != nil {
						return err
					}
					restart = true
				}
			}
			updatePyTorchJobReplicaStatuses(job, rtype, pod)
		}
	}

	return pc.updateStatusSingle(job, rtype, replicas, restart)
}

// getPodSlices returns a slice, which element is the slice of pod.
// 假如这个 PyTorchJob  worker 有 3 个:
//     正常情况：[[pod-worker-0] [pod-worker-1] [pod-worker-2]]
//     异常情况：[[pod-worker-0 pod-worker-0-another] [pod-worker-1] [pod-worker-2]]
func getPodSlices(pods []*v1.Pod, replicas int, logger *log.Entry) [][]*v1.Pod {
	podSlices := make([][]*v1.Pod, replicas)
	for _, pod := range pods {
		// 每一个 pod 的 labels 中同一个 PyTorchJob 的同一个 replica pods 都有一个从 0 开始的 index,
		// 例如：pod.Labels['pytorch-replica-index'] = "0"
		if _, ok := pod.Labels[replicaIndexLabel]; !ok {
			logger.Warning("The pod do not have the index label.")
			continue
		}
		index, err := strconv.Atoi(pod.Labels[replicaIndexLabel])
		if err != nil {
			logger.Warningf("Error when strconv.Atoi: %v", err)
			continue
		}
		if index < 0 || index >= replicas {
			logger.Warningf("The label index is not expected: %d", index)
		} else {
			// [[pod-worker-0], [pod-worker-1], [pod-worker-2]]
			podSlices[index] = append(podSlices[index], pod)
		}
	}
	return podSlices
}

// createNewPod creates a new pod for the given index and type.
// 根据索引和 replica 类型创建一个新的 pod.
func (pc *PyTorchController) createNewPod(job *pyv1.PyTorchJob, rtype pyv1.PyTorchReplicaType, index string, spec *common.ReplicaSpec, masterRole bool) error {
	rt := strings.ToLower(string(rtype))
	jobKey, err := KeyFunc(job)
	if err != nil {
		utilruntime.HandleError(fmt.Errorf("couldn't get key for job object %#v: %v", job, err))
		return err
	}
	expectationPodsKey := jobcontroller.GenExpectationPodsKey(jobKey, rt)
	err = pc.Expectations.ExpectCreations(expectationPodsKey, 1)
	if err != nil {
		return err
	}
	logger := pylogger.LoggerForReplica(job, rt)
	// Create OwnerReference.
	controllerRef := pc.GenOwnerReference(job)

	// Set type and index for the worker.
	labels := pc.GenLabels(job.Name)
	labels[replicaTypeLabel] = rt
	labels[replicaIndexLabel] = index

	if masterRole {
		// 如果是 master 的 pod， 在 labels 中添加 "job-role": "master"
		labels[jobcontroller.JobRoleLabel] = "master"
	}
	podTemplate := spec.Template.DeepCopy()

	// 获取 PyTorchJob 总 pods 的副本数
	totalReplicas := getTotalReplicas(job)
	// Set name for the template.
	// pod 的 名字： jobName-relicaType-index, 例如：pytorch-dist-mnist-gloo-master-0
	podTemplate.Name = jobcontroller.GenGeneralName(job.Name, rt, index)

	if podTemplate.Labels == nil {
		podTemplate.Labels = make(map[string]string)
	}

	for key, value := range labels {
		podTemplate.Labels[key] = value
	}
	// 设置集群配置，如环境变量
	if err := setClusterSpec(podTemplate, job, totalReplicas, index, rtype); err != nil {
		return err
	}

	// Submit a warning event if the user specifies restart policy for
	// the pod template. We recommend to set it from the replica level.
	// 如果用户为Pod模板指定重启策略，则提交警告事件. （pod 指定的重启策略将会被 replica 中的策略覆盖）
	// 我们建议对 replica 进行设置重启策略
	if podTemplate.Spec.RestartPolicy != v1.RestartPolicy("") {
		errMsg := "Restart policy in pod template will be overwritten by restart policy in replica spec"
		logger.Warning(errMsg)
		pc.Recorder.Event(job, v1.EventTypeWarning, podTemplateRestartPolicyReason, errMsg)
	}
	setRestartPolicy(podTemplate, spec)

	// worker 节点的 pod 的设置
	if !masterRole {
		// 如果 worker 节点， masterAddr 用 "job.Name-master-0" 替换 "localhost"
		masterAddr := jobcontroller.GenGeneralName(job.Name, strings.ToLower(string(pyv1.PyTorchReplicaTypeMaster)), strconv.Itoa(0))
		err := AddInitContainerForWorkerPod(podTemplate, InitContainerParam{
			MasterAddr:         masterAddr,
			InitContainerImage: pc.initContainerImage,
		})
		if err != nil {
			return err
		}
	}

	// if gang-scheduling is enabled:
	// 1. if user has specified other scheduler, we report a warning without overriding any fields.
	// 2. if no SchedulerName is set for pods, then we set the SchedulerName to "kube-batch".
	// 如果开启了 gang-scheduling:
	// 1. 如果用户指定了其他 scheduler, 直接给出报错信息
	// 2. 如果未为 Pod 设置 SchedulerName，则将 SchedulerName 设置为“ kube-batch”。
	if pc.Config.EnableGangScheduling {
		if pc.isNonGangSchedulerSet(job) {
			errMsg := "Another scheduler is specified when gang-scheduling is enabled and it will not be overwritten"
			logger.Warning(errMsg)
			pc.Recorder.Event(job, v1.EventTypeWarning, podTemplateSchedulerNameReason, errMsg)
		} else {
			podTemplate.Spec.SchedulerName = pc.Config.GangSchedulerName
		}

		if podTemplate.Annotations == nil {
			podTemplate.Annotations = map[string]string{}
		}
		podTemplate.Annotations[gangSchedulingPodGroupAnnotation] = jobcontroller.GenPodGroupName(job.Name)
	}
	// 使用集群配置信息，真正启动 Pod 的创建， 通过调用 k8s 接口创建 pod
	err = pc.PodControl.CreatePodsWithControllerRef(job.Namespace, podTemplate, job, controllerRef)
	if err != nil && k8serrors.IsTimeout(err) {
		// Pod is created but its initialization has timed out.
		// If the initialization is successful eventually, the
		// controller will observe the creation via the informer.
		// If the initialization fails, or if the pod keeps
		// uninitialized for a long time, the informer will not
		// receive any update, and the controller will create a new
		// pod when the expectation expires.
		return nil
	} else if err != nil {
		return err
	}
	return nil
}

// 设置集群配置，如：
// MASTER_PORT、 MASTER_ADDR、WORLD_SIZE（master 节点也参与计算，因此 WORLD_SIZE= master + worker 的总副本数） 等环境变量
func setClusterSpec(podTemplateSpec *v1.PodTemplateSpec, job *pyv1.PyTorchJob, totalReplicas int32, index string, rtype pyv1.PyTorchReplicaType) error {
	rank, err := strconv.Atoi(index)
	if err != nil {
		return err
	}

	masterPort, err := GetPortFromPyTorchJob(job, pyv1.PyTorchReplicaTypeMaster)
	if err != nil {
		return err
	}

	masterAddr := jobcontroller.GenGeneralName(job.Name, strings.ToLower(string(pyv1.PyTorchReplicaTypeMaster)), strconv.Itoa(0))
	if rtype == pyv1.PyTorchReplicaTypeMaster {
		// master
		if rank != 0 {
			// 这里注明了 master 的 index 只能为 0, 也就是 master 的副本数只能为 1
			return errors.New("invalid config: There should be only a single master with index=0")
		}
		masterAddr = "localhost"
	} else {
		// worker 节点对应的非 rank0 节点，因此 rank 号是自身 pod 的 index+1
		rank = rank + 1
	}

	for i := range podTemplateSpec.Spec.Containers {
		if len(podTemplateSpec.Spec.Containers[i].Env) == 0 {
			podTemplateSpec.Spec.Containers[i].Env = make([]v1.EnvVar, 0)
		}
		podTemplateSpec.Spec.Containers[i].Env = append(podTemplateSpec.Spec.Containers[i].Env, v1.EnvVar{
			Name:  "MASTER_PORT",
			Value: strconv.Itoa(int(masterPort)),
		})
		podTemplateSpec.Spec.Containers[i].Env = append(podTemplateSpec.Spec.Containers[i].Env, v1.EnvVar{
			Name:  "MASTER_ADDR",
			Value: masterAddr,
		})
		podTemplateSpec.Spec.Containers[i].Env = append(podTemplateSpec.Spec.Containers[i].Env, v1.EnvVar{
			Name:  "WORLD_SIZE",
			Value: strconv.Itoa(int(totalReplicas)),
		})
		podTemplateSpec.Spec.Containers[i].Env = append(podTemplateSpec.Spec.Containers[i].Env, v1.EnvVar{
			Name:  "RANK",
			Value: strconv.Itoa(rank),
		})
		podTemplateSpec.Spec.Containers[i].Env = append(podTemplateSpec.Spec.Containers[i].Env, v1.EnvVar{
			Name:  "PYTHONUNBUFFERED",
			Value: "0",
		})
	}
	return nil
}

func setRestartPolicy(podTemplateSpec *v1.PodTemplateSpec, spec *common.ReplicaSpec) {
	if spec.RestartPolicy == common.RestartPolicyExitCode {
		podTemplateSpec.Spec.RestartPolicy = v1.RestartPolicyNever
	} else {
		podTemplateSpec.Spec.RestartPolicy = v1.RestartPolicy(spec.RestartPolicy)
	}
}

func (pc *PyTorchController) isNonGangSchedulerSet(job *pyv1.PyTorchJob) bool {
	for _, spec := range job.Spec.PyTorchReplicaSpecs {
		if spec.Template.Spec.SchedulerName != "" && spec.Template.Spec.SchedulerName != pc.Config.GangSchedulerName {
			return true
		}
	}
	return false
}
