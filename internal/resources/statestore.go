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

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

// BuildStatestore renders the statestore StatefulSet and Services.
func BuildStatestore(c *impalav1alpha1.ImpalaCluster, configHash string) (*appsv1.StatefulSet, []*corev1.Service) {
	name := StatestoreName(c)
	headless := HeadlessName(name)
	args := []string{
		fmt.Sprintf("-state_store_port=%d", PortStatestore),
	}
	if catalogReplicas(c) > 1 {
		args = append(args, "-enable_catalogd_ha=true")
	}
	sts := buildStatefulSet(c, daemon{
		component:   ComponentStatestore,
		name:        name,
		headless:    headless,
		imageSuffix: "statestored",
		replicas:    1,
		labels:      Labels(c, ComponentStatestore),
		selector:    SelectorLabels(c, ComponentStatestore),
		args:        args,
		ports: []corev1.ContainerPort{
			containerPort("statestore", PortStatestore),
			containerPort("web", PortStatestoreWeb),
		},
		webPort:    PortStatestoreWeb,
		spec:       c.Spec.Statestore.ComponentSpec,
		jvmHeap:    c.Spec.Statestore.Config.JVMHeap,
		configHash: configHash,
	})
	labels := Labels(c, ComponentStatestore)
	selector := SelectorLabels(c, ComponentStatestore)
	return sts, []*corev1.Service{
		buildService(c, headless, labels, selector, []corev1.ServicePort{servicePort("statestore", PortStatestore), servicePort("web", PortStatestoreWeb)}, true),
		buildService(c, name, labels, selector, []corev1.ServicePort{servicePort("statestore", PortStatestore)}, false),
	}
}
