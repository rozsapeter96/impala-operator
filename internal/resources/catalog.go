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

func catalogReplicas(c *impalav1alpha1.ImpalaCluster) int32 {
	if c.Spec.Catalog.Replicas != nil {
		return *c.Spec.Catalog.Replicas
	}
	return 1
}

// BuildCatalog renders the catalog StatefulSet and Services.
func BuildCatalog(c *impalav1alpha1.ImpalaCluster, configHash string) (*appsv1.StatefulSet, []*corev1.Service) {
	name := CatalogName(c)
	headless := HeadlessName(name)
	cfg := &c.Spec.Catalog.Config
	topicMode := cfg.CatalogTopicMode
	if topicMode == "" {
		topicMode = "minimal"
	}
	polling := int32(1)
	if v := c.Spec.ClusterConfig.HiveMetastore.EventPollingIntervalSeconds; v != nil {
		polling = *v
	}
	args := []string{
		"-state_store_host=" + StatestoreName(c),
		fmt.Sprintf("-state_store_port=%d", PortStatestore),
		fmt.Sprintf("-catalog_service_port=%d", PortCatalog),
		"-catalog_topic_mode=" + topicMode,
		fmt.Sprintf("-hms_event_polling_interval_s=%d", polling),
	}
	if catalogReplicas(c) > 1 {
		args = append(args, "-enable_catalogd_ha=true")
	}
	sts := buildStatefulSet(c, daemon{
		component:   ComponentCatalog,
		name:        name,
		headless:    headless,
		imageSuffix: "catalogd",
		replicas:    catalogReplicas(c),
		labels:      Labels(c, ComponentCatalog),
		selector:    SelectorLabels(c, ComponentCatalog),
		args:        args,
		ports: []corev1.ContainerPort{
			containerPort("catalog", PortCatalog),
			containerPort("web", PortCatalogWeb),
		},
		webPort:       PortCatalogWeb,
		spec:          c.Spec.Catalog.ComponentSpec,
		jvmHeap:       cfg.JVMHeap,
		configHash:    configHash,
		startupBudget: 60,
	})
	labels := Labels(c, ComponentCatalog)
	selector := SelectorLabels(c, ComponentCatalog)
	return sts, []*corev1.Service{
		buildService(c, headless, labels, selector, []corev1.ServicePort{servicePort("catalog", PortCatalog), servicePort("web", PortCatalogWeb)}, true),
		buildService(c, name, labels, selector, []corev1.ServicePort{servicePort("catalog", PortCatalog)}, false),
	}
}
