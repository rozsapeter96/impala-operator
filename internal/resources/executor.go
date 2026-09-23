/*
Copyright 2026 Peter Rozsa.

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

package resources

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

// MinHealthySize returns the executor count a group needs before it serves queries.
func MinHealthySize(eg *impalav1alpha1.ExecutorGroupSpec) int32 {
	if eg.MinHealthySize != nil {
		return *eg.MinHealthySize
	}
	return eg.Size
}

// SpecGroups returns the group count requested in the spec.
func SpecGroups(eg *impalav1alpha1.ExecutorGroupSpec) int32 {
	if eg.Groups != nil {
		return *eg.Groups
	}
	return 1
}

// BuildExecutorGroupHeadlessService renders the headless Service shared by all
// instances of one executor group family.
func BuildExecutorGroupHeadlessService(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec) *corev1.Service {
	labels := Labels(c, ComponentExecutor)
	labels[LabelExecutorGroup] = eg.Name
	selector := SelectorLabels(c, ComponentExecutor)
	selector[LabelExecutorGroup] = eg.Name
	ports := []corev1.ServicePort{servicePort("krpc", PortKRPC), servicePort("web", PortImpaladWeb)}
	return buildService(c, ExecutorGroupHeadlessName(c, eg), labels, selector, ports, true)
}

// BuildExecutorGroup renders the StatefulSet for one executor group instance.
func BuildExecutorGroup(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec, idx int32, configHash string) *appsv1.StatefulSet {
	cfg := &eg.Config
	groupName := ImpalaExecutorGroupName(c, eg, idx)

	args := append(impaladCommonArgs(c, &cfg.ImpaladConfig),
		"-is_coordinator=false",
		"-is_executor=true",
		fmt.Sprintf("-executor_groups=%s:%d", groupName, MinHealthySize(eg)),
		"-scratch_dirs="+ScratchDir,
	)

	var claims []corev1.PersistentVolumeClaim
	var volumes []corev1.Volume
	mounts := []corev1.VolumeMount{{Name: volumeScratch, MountPath: ScratchDir}}
	if cfg.Scratch != nil {
		claims = append(claims, pvcTemplate(volumeScratch, cfg.Scratch))
	} else {
		volumes = append(volumes, corev1.Volume{Name: volumeScratch, VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}})
	}
	if cfg.DataCache != nil {
		claims = append(claims, pvcTemplate(volumeCache, cfg.DataCache))
		mounts = append(mounts, corev1.VolumeMount{Name: volumeCache, MountPath: CacheDir})
		// Leave headroom below the PVC size for filesystem overhead.
		quota := cfg.DataCache.Size.DeepCopy()
		quota.Set(quota.Value() / 10 * 9)
		args = append(args, fmt.Sprintf("-data_cache=%s:%s", CacheDir, impalaMemSpec(quota)))
	}

	labels := ExecutorGroupLabels(c, eg, idx)
	return buildStatefulSet(c, daemon{
		component:   ComponentExecutor,
		name:        ExecutorGroupName(c, eg, idx),
		headless:    ExecutorGroupHeadlessName(c, eg),
		imageSuffix: "impalad_executor",
		replicas:    eg.Size,
		labels:      labels,
		selector:    ExecutorGroupSelectorLabels(c, eg, idx),
		args:        args,
		ports: []corev1.ContainerPort{
			containerPort("krpc", PortKRPC),
			containerPort("web", PortImpaladWeb),
		},
		webPort:      PortImpaladWeb,
		spec:         eg.ComponentSpec,
		jvmHeap:      cfg.JVMHeap,
		shutdown:     &cfg.ImpaladConfig,
		claims:       claims,
		extraVolumes: volumes,
		extraMounts:  mounts,
		configHash:   configHash,
	})
}

func pvcTemplate(name string, v *impalav1alpha1.VolumeSpec) corev1.PersistentVolumeClaim {
	return corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			StorageClassName: v.StorageClassName,
			Resources:        corev1.VolumeResourceRequirements{Requests: corev1.ResourceList{corev1.ResourceStorage: v.Size}},
		},
	}
}
