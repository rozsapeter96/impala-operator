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

	impalav1alpha1 "github.com/rozsapeter96/impala-operator/api/v1alpha1"
)

// Component identifies a daemon type. It is used in labels and object names.
type Component string

const (
	ComponentStatestore  Component = "statestore"
	ComponentCatalog     Component = "catalog"
	ComponentCoordinator Component = "coordinator"
	ComponentExecutor    Component = "executor"
)

// Well-known Impala ports.
const (
	PortStatestore       int32 = 24000
	PortStatestoreWeb    int32 = 25010
	PortCatalog          int32 = 26000
	PortCatalogWeb       int32 = 25020
	PortHS2              int32 = 21050
	PortHS2HTTP          int32 = 28000
	PortKRPC             int32 = 27000
	PortImpaladWeb       int32 = 25000
	PortSubscriber       int32 = 23000
	PortCatalogSubscribe int32 = 23020
)

// Paths inside the official images.
const (
	ConfDir     = "/opt/impala/conf"
	LogDir      = "/opt/impala/logs"
	SecretsDir  = "/opt/impala/secrets"
	ScratchDir  = "/opt/impala/scratch"
	CacheDir    = "/opt/impala/cache"
	KeytabFile  = SecretsDir + "/kerberos/impala.keytab"
	Krb5File    = SecretsDir + "/kerberos/krb5.conf"
	TLSCertFile = SecretsDir + "/tls/tls.crt"
	TLSKeyFile  = SecretsDir + "/tls/tls.key"
	TLSCAFile   = SecretsDir + "/tls/ca.crt"
	LDAPCAFile  = SecretsDir + "/ldap/ca.crt"

	// DefaultPoolName is Impala's built-in admission pool when no
	// fair-scheduler configuration is present.
	DefaultPoolName = "default-pool"

	// TLSSecretCAKey is the CA bundle key in a kubernetes.io/tls Secret.
	TLSSecretCAKey = "ca.crt"
)

// ConfigMapName returns the name of the shared configuration ConfigMap.
func ConfigMapName(c *impalav1alpha1.ImpalaCluster) string { return c.Name + "-conf" }

// StatestoreName is the StatefulSet and client Service name of the statestore.
func StatestoreName(c *impalav1alpha1.ImpalaCluster) string { return c.Name + "-statestore" }

// CatalogName is the StatefulSet and client Service name of the catalog.
func CatalogName(c *impalav1alpha1.ImpalaCluster) string { return c.Name + "-catalog" }

// CoordinatorName is the StatefulSet and client Service name of the coordinators.
func CoordinatorName(c *impalav1alpha1.ImpalaCluster) string { return c.Name + "-coordinator" }

// HeadlessName returns the headless Service name that gives pods stable DNS.
func HeadlessName(base string) string { return base + "-hl" }

// ExecutorGroupName is the StatefulSet name of one executor group instance.
func ExecutorGroupName(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec, idx int32) string {
	return fmt.Sprintf("%s-exec-%s-%d", c.Name, eg.Name, idx)
}

// ExecutorGroupHeadlessName returns the headless Service shared by all
// instances of an executor group family.
func ExecutorGroupHeadlessName(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec) string {
	return fmt.Sprintf("%s-exec-%s-hl", c.Name, eg.Name)
}

// PoolName returns the fully qualified admission-control pool an executor
// group serves: "root.<pool>" when pools are configured, otherwise Impala's
// built-in default pool.
func PoolName(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec) string {
	if len(c.Spec.ClusterConfig.AdmissionControl.Pools) == 0 {
		return DefaultPoolName
	}
	return "root." + eg.Pool
}

// ImpalaExecutorGroupName is the name registered with Impala via
// -executor_groups. Impala requires it to be prefixed with "<pool>-".
func ImpalaExecutorGroupName(c *impalav1alpha1.ImpalaCluster, eg *impalav1alpha1.ExecutorGroupSpec, idx int32) string {
	return fmt.Sprintf("%s-%s-%d", PoolName(c, eg), eg.Name, idx)
}

// PodFQDN returns the DNS name a StatefulSet pod gets through its headless Service.
func PodFQDN(podName, headlessService, namespace string) string {
	return fmt.Sprintf("%s.%s.%s.svc.cluster.local", podName, headlessService, namespace)
}

// Image returns the image reference for a component.
func Image(c *impalav1alpha1.ImpalaCluster, component string) string {
	repo := c.Spec.Image.Repository
	if repo == "" {
		repo = "apache/impala"
	}
	version := c.Spec.Image.Version
	if version == "" {
		version = "4.5.2"
	}
	return fmt.Sprintf("%s:%s-%s", repo, version, component)
}
