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

// CoordinatorReplicas returns the desired coordinator count.
func CoordinatorReplicas(c *impalav1alpha1.ImpalaCluster) int32 {
	if c.Spec.Coordinators.Replicas != nil {
		return *c.Spec.Coordinators.Replicas
	}
	return 1
}

// BuildCoordinators renders the coordinator StatefulSet and Services.
func BuildCoordinators(c *impalav1alpha1.ImpalaCluster, configHash string) (*appsv1.StatefulSet, []*corev1.Service) {
	name := CoordinatorName(c)
	headless := HeadlessName(name)
	cfg := &c.Spec.Coordinators.Config

	args := append(impaladCommonArgs(c, &cfg.ImpaladConfig),
		"-is_coordinator=true",
		"-is_executor=false",
		"-use_local_catalog=true",
		fmt.Sprintf("-hs2_port=%d", PortHS2),
		fmt.Sprintf("-hs2_http_port=%d", PortHS2HTTP),
		fmt.Sprintf("-num_expected_executors=%d", maxExecutorGroupSize(c)),
	)
	if CatalogdDeployed(c) {
		args = append(args,
			"-catalog_service_host="+CatalogName(c),
			fmt.Sprintf("-catalog_service_port=%d", PortCatalog),
		)
	}
	// The local catalog loads every REST catalog from the properties files
	// in this directory, next to (or instead of) the catalogd provider.
	if len(c.Spec.ClusterConfig.IcebergRESTCatalogs) > 0 {
		args = append(args, "-catalog_config_dir="+CatalogConfigDir)
	}
	if len(cfg.DefaultQueryOptions) > 0 {
		args = append(args, "-default_query_options="+joinOptions(cfg.DefaultQueryOptions))
	}
	if HasPools(c) {
		args = append(args,
			"-fair_scheduler_allocation_path="+ConfDir+"/"+FileFairScheduler,
			"-llama_site_path="+ConfDir+"/"+FileLlamaSite,
		)
	}

	sts := buildStatefulSet(c, daemon{
		component:   ComponentCoordinator,
		name:        name,
		headless:    headless,
		imageSuffix: "impalad_coordinator",
		replicas:    CoordinatorReplicas(c),
		labels:      Labels(c, ComponentCoordinator),
		selector:    SelectorLabels(c, ComponentCoordinator),
		args:        args,
		ports: []corev1.ContainerPort{
			containerPort("hs2", PortHS2),
			containerPort("hs2-http", PortHS2HTTP),
			containerPort("krpc", PortKRPC),
			containerPort("web", PortImpaladWeb),
		},
		webPort:       PortImpaladWeb,
		spec:          c.Spec.Coordinators.ComponentSpec,
		jvmHeap:       cfg.JVMHeap,
		shutdown:      &cfg.ImpaladConfig,
		extraEnv:      restCatalogEnv(c),
		extraVolumes:  restCatalogVolumes(c),
		extraMounts:   restCatalogMounts(c),
		configHash:    configHash,
		startupBudget: 90,
	})

	labels := Labels(c, ComponentCoordinator)
	selector := SelectorLabels(c, ComponentCoordinator)
	allPorts := []corev1.ServicePort{
		servicePort("hs2", PortHS2), servicePort("hs2-http", PortHS2HTTP),
		servicePort("krpc", PortKRPC), servicePort("web", PortImpaladWeb),
	}
	// The client Service carries only the HiveServer2 ports. The debug web
	// UI is unauthenticated (query text, profiles, logs, cancel/close
	// actions), so it stays on the headless Service, which never gets a
	// LoadBalancer or NodePort.
	client := buildService(c, name, labels, selector, []corev1.ServicePort{
		servicePort("hs2", PortHS2), servicePort("hs2-http", PortHS2HTTP),
	}, false)
	client.Spec.Type = c.Spec.Coordinators.Service.Type
	client.Annotations = c.Spec.Coordinators.Service.Annotations
	return sts, []*corev1.Service{
		buildService(c, headless, labels, selector, allPorts, true),
		client,
	}
}

// impaladCommonArgs are shared by coordinators and executors.
func impaladCommonArgs(c *impalav1alpha1.ImpalaCluster, cfg *impalav1alpha1.ImpaladConfig) []string {
	args := []string{
		"-state_store_host=" + StatestoreName(c),
		fmt.Sprintf("-state_store_port=%d", PortStatestore),
		fmt.Sprintf("-krpc_port=%d", PortKRPC),
		fmt.Sprintf("-state_store_subscriber_port=%d", PortSubscriber),
		"-mem_limit_includes_jvm=true",
	}
	if !CatalogdDeployed(c) {
		// Every impalad, executors included, must stop waiting for a catalog
		// topic and polling catalog metrics when no catalogd exists.
		args = append(args, "-catalogd_deployed=false")
	}
	if cfg.MemLimit != "" {
		args = append(args, "-mem_limit="+cfg.MemLimit)
	}
	if k := c.Spec.ClusterConfig.Kudu; k != nil {
		args = append(args, "-kudu_master_hosts="+k.MasterHosts)
	}
	return args
}

func maxExecutorGroupSize(c *impalav1alpha1.ImpalaCluster) int32 {
	var m int32 = 1
	for i := range c.Spec.ExecutorGroups {
		if s := c.Spec.ExecutorGroups[i].Size; s > m {
			m = s
		}
	}
	return m
}
