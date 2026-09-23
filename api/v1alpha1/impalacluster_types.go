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

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// Condition types reported in ImpalaClusterStatus.Conditions.
const (
	// ConditionReady is True when every component has all replicas ready.
	ConditionReady = "Ready"
	// ConditionProgressing is True while the operator is rolling out changes.
	ConditionProgressing = "Progressing"
	// ConditionDegraded is True when the spec is invalid or a component cannot be reconciled.
	ConditionDegraded = "Degraded"
)

// ImageSpec selects the Impala container image family.
type ImageSpec struct {
	// repository is the image repository. Component names are appended as
	// "<version>-<component>" tags, matching the official apache/impala layout.
	// +kubebuilder:default="apache/impala"
	// +optional
	Repository string `json:"repository,omitempty"`

	// version is the Impala version, e.g. "4.5.2".
	// +kubebuilder:default="4.5.2"
	// +kubebuilder:validation:MinLength=1
	// +optional
	Version string `json:"version,omitempty"`

	// pullPolicy for all Impala containers.
	// +kubebuilder:default=IfNotPresent
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`

	// pullSecrets for private registries.
	// +optional
	PullSecrets []corev1.LocalObjectReference `json:"pullSecrets,omitempty"`
}

// HiveMetastoreSpec points Impala at an external Hive Metastore.
type HiveMetastoreSpec struct {
	// uris is the value of hive.metastore.uris, e.g. "thrift://hms.default.svc:9083".
	// +kubebuilder:validation:MinLength=1
	// +required
	URIs string `json:"uris"`

	// eventPollingIntervalSeconds sets -hms_event_polling_interval_s on catalogd.
	// 0 disables HMS event processing.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	EventPollingIntervalSeconds *int32 `json:"eventPollingIntervalSeconds,omitempty"`
}

// S3Spec configures S3A access for table data.
type S3Spec struct {
	// endpoint overrides fs.s3a.endpoint (leave empty for AWS).
	// +optional
	Endpoint string `json:"endpoint,omitempty"`

	// region sets fs.s3a.endpoint.region.
	// +optional
	Region string `json:"region,omitempty"`

	// pathStyleAccess sets fs.s3a.path.style.access, required by MinIO and most on-prem stores.
	// +optional
	PathStyleAccess bool `json:"pathStyleAccess,omitempty"`

	// credentialsSecretRef names a Secret with keys AWS_ACCESS_KEY_ID and
	// AWS_SECRET_ACCESS_KEY (optionally AWS_SESSION_TOKEN). They are injected as
	// environment variables. Omit to rely on IAM roles / IRSA.
	// +optional
	CredentialsSecretRef *corev1.LocalObjectReference `json:"credentialsSecretRef,omitempty"`
}

// StorageSpec configures the object stores Impala reads and writes.
type StorageSpec struct {
	// s3 enables S3A configuration in core-site.xml.
	// +optional
	S3 *S3Spec `json:"s3,omitempty"`
}

// KuduSpec configures an optional Kudu integration.
type KuduSpec struct {
	// masterHosts is the value of -kudu_master_hosts, e.g. "kudu-master-0:7051,kudu-master-1:7051".
	// +kubebuilder:validation:MinLength=1
	// +required
	MasterHosts string `json:"masterHosts"`
}

// PoolSpec is an admission-control resource pool. Impala names it "root.<name>".
type PoolSpec struct {
	// name of the pool without the "root." prefix.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=63
	// +required
	Name string `json:"name"`

	// maxRunningQueries limits concurrently running queries (-1 = unlimited).
	// +optional
	MaxRunningQueries *int32 `json:"maxRunningQueries,omitempty"`

	// maxQueuedQueries limits queued queries before rejection.
	// +optional
	MaxQueuedQueries *int32 `json:"maxQueuedQueries,omitempty"`

	// queueTimeoutMs is how long a query may wait in the queue.
	// +optional
	QueueTimeoutMs *int64 `json:"queueTimeoutMs,omitempty"`

	// maxMemResources is the pool's aggregate memory cap in fair-scheduler
	// syntax, e.g. "50000 mb".
	// +optional
	MaxMemResources string `json:"maxMemResources,omitempty"`

	// maxQueryMemLimit caps the per-node memory of a query, e.g. "8gb".
	// +optional
	MaxQueryMemLimit string `json:"maxQueryMemLimit,omitempty"`

	// minQueryMemLimit floors the per-node memory of a query, e.g. "1gb".
	// +optional
	MinQueryMemLimit string `json:"minQueryMemLimit,omitempty"`

	// defaultQueryOptions applied to every query in the pool.
	// +optional
	DefaultQueryOptions map[string]string `json:"defaultQueryOptions,omitempty"`
}

// AdmissionControlSpec generates fair-scheduler.xml and llama-site.xml.
// +kubebuilder:validation:XValidation:rule="!has(self.defaultPool) || !has(self.pools) || self.pools.exists(p, p.name == self.defaultPool)",message="defaultPool must name one of the pools"
type AdmissionControlSpec struct {
	// defaultPool receives queries that do not set REQUEST_POOL. Defaults to
	// the first pool. Queries naming an unknown pool are rejected.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	DefaultPool string `json:"defaultPool,omitempty"`

	// pools defines admission-control resource pools. When empty Impala uses
	// its built-in "default-pool".
	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=64
	// +optional
	Pools []PoolSpec `json:"pools,omitempty"`
}

// TLSSpec enables TLS for client, internal and web endpoints.
// +kubebuilder:validation:XValidation:rule="!has(self.enabled) || !self.enabled || has(self.certSecretRef)",message="tls.certSecretRef is required when tls.enabled is true"
type TLSSpec struct {
	// enabled turns on TLS for HiveServer2/Beeswax client connections and the
	// web UI of all daemons.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// certSecretRef names a kubernetes.io/tls Secret (tls.crt, tls.key, ca.crt).
	// The certificate must cover the coordinator and executor pod DNS names,
	// typically with a wildcard "*.<cluster>-<component>-hl.<ns>.svc.cluster.local".
	// +optional
	CertSecretRef *corev1.LocalObjectReference `json:"certSecretRef,omitempty"`

	// internal also encrypts daemon-to-daemon traffic (Thrift and KRPC).
	// +kubebuilder:default=true
	// +optional
	Internal *bool `json:"internal,omitempty"`

	// minimumVersion is passed to -ssl_minimum_version.
	// +kubebuilder:validation:Enum=tlsv1.2;tlsv1.3
	// +optional
	MinimumVersion string `json:"minimumVersion,omitempty"`
}

// KerberosSpec enables Kerberos authentication.
// +kubebuilder:validation:XValidation:rule="!has(self.enabled) || !self.enabled || (has(self.principal) && has(self.keytabSecretRef))",message="kerberos.principal and kerberos.keytabSecretRef are required when kerberos.enabled is true"
type KerberosSpec struct {
	// enabled turns on Kerberos for clients and internal RPC.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// principal in "service/_HOST@REALM" form, passed to -principal.
	// +optional
	Principal string `json:"principal,omitempty"`

	// keytabSecretRef names a Secret containing the keytab under key "impala.keytab".
	// +optional
	KeytabSecretRef *corev1.LocalObjectReference `json:"keytabSecretRef,omitempty"`

	// krb5ConfigMapRef names a ConfigMap containing krb5.conf under key "krb5.conf".
	// +optional
	Krb5ConfigMapRef *corev1.LocalObjectReference `json:"krb5ConfigMapRef,omitempty"`
}

// LDAPSpec enables LDAP password authentication for clients.
type LDAPSpec struct {
	// enabled turns on -enable_ldap_auth.
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// uri of the LDAP server, e.g. "ldaps://ldap.example.com".
	// +optional
	URI string `json:"uri,omitempty"`

	// bindPattern is passed to -ldap_bind_pattern, e.g. "uid=#UID,ou=people,dc=example,dc=com".
	// +optional
	BindPattern string `json:"bindPattern,omitempty"`

	// domain is passed to -ldap_domain (Active Directory style).
	// +optional
	Domain string `json:"domain,omitempty"`

	// baseDN is passed to -ldap_baseDN.
	// +optional
	BaseDN string `json:"baseDN,omitempty"`

	// caCertSecretRef names a Secret with key "ca.crt" for -ldap_ca_certificate.
	// +optional
	CACertSecretRef *corev1.LocalObjectReference `json:"caCertSecretRef,omitempty"`

	// allowPasswordsInClear sets -ldap_passwords_in_clear_ok (only for testing).
	// +optional
	AllowPasswordsInClear bool `json:"allowPasswordsInClear,omitempty"`
}

// NetworkPolicySpec isolates the cluster's daemons on the pod network.
// Without Kerberos, Impala's internal ports (statestore, catalog, KRPC,
// statestore subscriber) accept any peer, so any pod that can reach them can
// register as an executor and receive query data. Enabling this renders one
// NetworkPolicy per cluster that admits:
//
//   - every port from the cluster's own pods;
//   - the HiveServer2 client ports (21050, 28000) from clientFrom, or from
//     anywhere when clientFrom is empty;
//   - the daemon web UI ports (25000, 25010, 25020) from the operator's own
//     namespace (the autoscaler scrapes them) and from webUIFrom.
//
// Egress is not restricted. Kubelet probes are exempt from NetworkPolicy.
type NetworkPolicySpec struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// clientFrom lists the peers allowed to reach the coordinator client
	// ports. Leave empty to allow any source.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	ClientFrom []networkingv1.NetworkPolicyPeer `json:"clientFrom,omitempty"`

	// webUIFrom lists additional peers allowed to reach the daemon web UI
	// ports, e.g. a Prometheus namespace. Cluster pods and the operator's
	// namespace are always allowed.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	WebUIFrom []networkingv1.NetworkPolicyPeer `json:"webUIFrom,omitempty"`
}

// SecuritySpec groups authentication, encryption and network isolation settings.
type SecuritySpec struct {
	// +optional
	TLS TLSSpec `json:"tls,omitzero"`
	// +optional
	Kerberos KerberosSpec `json:"kerberos,omitzero"`
	// +optional
	LDAP LDAPSpec `json:"ldap,omitzero"`
	// +optional
	NetworkPolicy NetworkPolicySpec `json:"networkPolicy,omitzero"`
}

// LoggingSpec controls glog output.
type LoggingSpec struct {
	// verbosity is the glog -v level.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=3
	// +optional
	Verbosity *int32 `json:"verbosity,omitempty"`
}

// ClusterConfig holds settings shared by every daemon.
type ClusterConfig struct {
	// +required
	HiveMetastore HiveMetastoreSpec `json:"hiveMetastore"`

	// +optional
	Storage StorageSpec `json:"storage,omitzero"`

	// +optional
	Kudu *KuduSpec `json:"kudu,omitempty"`

	// +optional
	AdmissionControl AdmissionControlSpec `json:"admissionControl,omitzero"`

	// +optional
	Security SecuritySpec `json:"security,omitzero"`

	// configOverrides adds or replaces properties in the generated Hadoop-style
	// XML files, keyed by file name ("hive-site.xml", "core-site.xml",
	// "hdfs-site.xml").
	// +optional
	ConfigOverrides map[string]map[string]string `json:"configOverrides,omitempty"`

	// +optional
	Logging LoggingSpec `json:"logging,omitzero"`
}

// PodOverrides customises the generated pod template of a component.
type PodOverrides struct {
	// +optional
	Labels map[string]string `json:"labels,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
	// +optional
	NodeSelector map[string]string `json:"nodeSelector,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Tolerations []corev1.Toleration `json:"tolerations,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Affinity *corev1.Affinity `json:"affinity,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`
	// serviceAccountName runs the pods under a specific ServiceAccount, for
	// example one bound to a cloud IAM role.
	// +optional
	ServiceAccountName string `json:"serviceAccountName,omitempty"`
	// automountServiceAccountToken controls whether the ServiceAccount token
	// is mounted into the daemon pods. Impala executes user SQL and native
	// UDFs, so the token is withheld by default; it is mounted when
	// serviceAccountName is set unless this is explicitly false.
	// +optional
	AutomountServiceAccountToken *bool `json:"automountServiceAccountToken,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	SecurityContext *corev1.PodSecurityContext `json:"securityContext,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Volumes []corev1.Volume `json:"volumes,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	VolumeMounts []corev1.VolumeMount `json:"volumeMounts,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	InitContainers []corev1.Container `json:"initContainers,omitempty"`
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Sidecars []corev1.Container `json:"sidecars,omitempty"`
}

// ComponentSpec holds knobs common to every daemon type.
type ComponentSpec struct {
	// args are extra daemon flags appended after the operator-generated ones,
	// so they take precedence.
	// +optional
	Args []string `json:"args,omitempty"`

	// env adds environment variables to the daemon container.
	// +optional
	// +kubebuilder:validation:Schemaless
	// +kubebuilder:pruning:PreserveUnknownFields
	Env []corev1.EnvVar `json:"env,omitempty"`

	// resources for the daemon container.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitzero"`

	// +optional
	PodOverrides PodOverrides `json:"podOverrides,omitzero"`
}

// DaemonConfig is shared by all daemons.
type DaemonConfig struct {
	// jvmHeap is the embedded JVM -Xmx, e.g. "2g".
	// +optional
	JVMHeap string `json:"jvmHeap,omitempty"`
}

// StatestoreConfig configures statestored.
type StatestoreConfig struct {
	DaemonConfig `json:",inline"`
}

// StatestoreSpec configures the statestore StatefulSet.
type StatestoreSpec struct {
	ComponentSpec `json:",inline"`

	// +optional
	Config StatestoreConfig `json:"config,omitzero"`
}

// CatalogConfig configures catalogd.
type CatalogConfig struct {
	DaemonConfig `json:",inline"`

	// catalogTopicMode is passed to -catalog_topic_mode.
	// +kubebuilder:validation:Enum=minimal;full;mixed
	// +kubebuilder:default=minimal
	// +optional
	CatalogTopicMode string `json:"catalogTopicMode,omitempty"`
}

// CatalogSpec configures the catalog StatefulSet.
type CatalogSpec struct {
	ComponentSpec `json:",inline"`

	// replicas is 1, or 2 to enable catalogd active/standby HA.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=2
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	Config CatalogConfig `json:"config,omitzero"`
}

// ImpaladConfig is shared by coordinators and executors.
type ImpaladConfig struct {
	DaemonConfig `json:",inline"`

	// memLimit is passed to -mem_limit, e.g. "8gb" or "80%".
	// +optional
	MemLimit string `json:"memLimit,omitempty"`

	// gracefulShutdownDeadlineSeconds is -shutdown_deadline_s. The pod's
	// terminationGracePeriodSeconds is derived from it.
	// +kubebuilder:default=900
	// +kubebuilder:validation:Minimum=1
	// +optional
	GracefulShutdownDeadlineSeconds *int32 `json:"gracefulShutdownDeadlineSeconds,omitempty"`

	// gracefulShutdownGracePeriodSeconds is -shutdown_grace_period_s: the
	// minimum time a quiescing daemon waits for coordinators to notice.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=0
	// +optional
	GracefulShutdownGracePeriodSeconds *int32 `json:"gracefulShutdownGracePeriodSeconds,omitempty"`
}

// CoordinatorConfig configures coordinator impalads.
type CoordinatorConfig struct {
	ImpaladConfig `json:",inline"`

	// defaultQueryOptions is passed to -default_query_options.
	// +optional
	DefaultQueryOptions map[string]string `json:"defaultQueryOptions,omitempty"`
}

// ServiceSpec customises the client-facing Service.
type ServiceSpec struct {
	// +kubebuilder:default=ClusterIP
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// CoordinatorSpec configures the coordinator StatefulSet.
type CoordinatorSpec struct {
	ComponentSpec `json:",inline"`

	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// +optional
	Config CoordinatorConfig `json:"config,omitzero"`

	// +optional
	Service ServiceSpec `json:"service,omitzero"`
}

// VolumeSpec requests a persistent volume for scratch or cache data.
type VolumeSpec struct {
	// +required
	Size resource.Quantity `json:"size"`
	// +optional
	StorageClassName *string `json:"storageClassName,omitempty"`
}

// ExecutorConfig configures executor impalads.
type ExecutorConfig struct {
	ImpaladConfig `json:",inline"`

	// scratch provisions a PVC for spill-to-disk (-scratch_dirs). Omit to use
	// an emptyDir. Immutable once the executor group exists, because it
	// becomes a StatefulSet volumeClaimTemplate.
	// +optional
	Scratch *VolumeSpec `json:"scratch,omitempty"`

	// dataCache provisions a PVC for the remote read cache (-data_cache).
	// Omit to disable the cache. Immutable once the executor group exists.
	// +optional
	DataCache *VolumeSpec `json:"dataCache,omitempty"`
}

// AutoscalingSpec lets the operator add and remove executor group instances
// based on admission-control queue metrics.
type AutoscalingSpec struct {
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	MinGroups *int32 `json:"minGroups,omitempty"`

	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	// +optional
	MaxGroups *int32 `json:"maxGroups,omitempty"`

	// scaleUpDelaySeconds is how long queries must be queued before a group is added.
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=0
	// +optional
	ScaleUpDelaySeconds *int32 `json:"scaleUpDelaySeconds,omitempty"`

	// scaleDownDelaySeconds is how long a group must be idle before it is removed.
	// +kubebuilder:default=300
	// +kubebuilder:validation:Minimum=0
	// +optional
	ScaleDownDelaySeconds *int32 `json:"scaleDownDelaySeconds,omitempty"`

	// cooldownSeconds is the minimum time between two scaling actions.
	// +kubebuilder:default=120
	// +kubebuilder:validation:Minimum=0
	// +optional
	CooldownSeconds *int32 `json:"cooldownSeconds,omitempty"`
}

// ExecutorGroupSpec describes a family of identically sized executor groups.
// Each group instance is one StatefulSet registered with Impala as
// "<pool>-<name>-<index>".
// +kubebuilder:validation:XValidation:rule="!has(self.minHealthySize) || self.minHealthySize <= self.size",message="minHealthySize must not exceed size"
// +kubebuilder:validation:XValidation:rule="!has(self.autoscaling) || !has(self.autoscaling.enabled) || !self.autoscaling.enabled || (!has(self.autoscaling.minGroups) || !has(self.autoscaling.maxGroups) || self.autoscaling.minGroups <= self.autoscaling.maxGroups)",message="autoscaling.minGroups must not exceed maxGroups"
// +kubebuilder:validation:XValidation:rule="(has(self.config) && has(self.config.scratch)) == (has(oldSelf.config) && has(oldSelf.config.scratch)) && (!has(self.config) || !has(self.config.scratch) || self.config.scratch == oldSelf.config.scratch)",message="config.scratch is immutable (StatefulSet volumeClaimTemplates cannot change); remove and re-add the executor group instead"
// +kubebuilder:validation:XValidation:rule="(has(self.config) && has(self.config.dataCache)) == (has(oldSelf.config) && has(oldSelf.config.dataCache)) && (!has(self.config) || !has(self.config.dataCache) || self.config.dataCache == oldSelf.config.dataCache)",message="config.dataCache is immutable (StatefulSet volumeClaimTemplates cannot change); remove and re-add the executor group instead"
type ExecutorGroupSpec struct {
	// name identifies this executor group family; it becomes part of Kubernetes
	// object names and Impala executor group names.
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`
	// +kubebuilder:validation:MaxLength=20
	// +required
	Name string `json:"name"`

	// pool is the admission-control pool served by these executors. Required
	// when admissionControl.pools is set; otherwise the built-in default pool
	// is used.
	// +kubebuilder:validation:MaxLength=63
	// +optional
	Pool string `json:"pool,omitempty"`

	// size is the number of executors in each group instance.
	// +kubebuilder:validation:Minimum=1
	// +required
	Size int32 `json:"size"`

	// minHealthySize is the number of registered executors needed before the
	// group accepts queries (defaults to size).
	// +kubebuilder:validation:Minimum=1
	// +optional
	MinHealthySize *int32 `json:"minHealthySize,omitempty"`

	// groups is the number of group instances. Ignored while autoscaling is enabled.
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=0
	// +optional
	Groups *int32 `json:"groups,omitempty"`

	// +optional
	Autoscaling *AutoscalingSpec `json:"autoscaling,omitempty"`

	// +optional
	Config ExecutorConfig `json:"config,omitzero"`

	ComponentSpec `json:",inline"`
}

// ImpalaClusterSpec defines the desired state of ImpalaCluster.
// +kubebuilder:validation:XValidation:rule="!has(self.clusterConfig.admissionControl) || !has(self.clusterConfig.admissionControl.pools) || size(self.clusterConfig.admissionControl.pools) == 0 || !has(self.executorGroups) || self.executorGroups.all(g, has(g.pool) && self.clusterConfig.admissionControl.pools.exists(p, p.name == g.pool))",message="every executorGroup.pool must reference a pool in clusterConfig.admissionControl.pools"
type ImpalaClusterSpec struct {
	// +optional
	Image ImageSpec `json:"image,omitzero"`

	// +required
	ClusterConfig ClusterConfig `json:"clusterConfig"`

	// +optional
	Statestore StatestoreSpec `json:"statestore,omitzero"`

	// +optional
	Catalog CatalogSpec `json:"catalog,omitzero"`

	// +optional
	Coordinators CoordinatorSpec `json:"coordinators,omitzero"`

	// +listType=map
	// +listMapKey=name
	// +kubebuilder:validation:MaxItems=32
	// +optional
	ExecutorGroups []ExecutorGroupSpec `json:"executorGroups,omitempty"`
}

// ComponentStatus reports replica counts for one component.
type ComponentStatus struct {
	// +optional
	Replicas int32 `json:"replicas"`
	// +optional
	ReadyReplicas int32 `json:"readyReplicas"`
}

// ExecutorGroupInstanceStatus reports on one executor group instance.
type ExecutorGroupInstanceStatus struct {
	// name is the Impala executor group name, e.g. "root.default-small-0".
	Name string `json:"name"`
	// +optional
	Replicas int32 `json:"replicas"`
	// +optional
	ReadyReplicas int32 `json:"readyReplicas"`
	// healthy is true once at least minHealthySize executors are ready.
	// +optional
	Healthy bool `json:"healthy"`
}

// ExecutorGroupStatus reports on one executor group family.
type ExecutorGroupStatus struct {
	Name string `json:"name"`

	// desiredGroups is the group count chosen by the autoscaler (or spec.groups).
	// +optional
	DesiredGroups int32 `json:"desiredGroups"`

	// +optional
	LastScaleTime *metav1.Time `json:"lastScaleTime,omitempty"`

	// +listType=map
	// +listMapKey=name
	// +optional
	Groups []ExecutorGroupInstanceStatus `json:"groups,omitempty"`
}

// EndpointsStatus lists the client-facing addresses.
type EndpointsStatus struct {
	// +optional
	HiveServer2 string `json:"hiveServer2,omitempty"`
	// +optional
	HiveServer2HTTP string `json:"hiveServer2Http,omitempty"`
	// webUI is the coordinators' debug web UI, reachable through the headless
	// Service only (it is unauthenticated and is not exposed on the client
	// Service).
	// +optional
	WebUI string `json:"webUI,omitempty"`
}

// ImpalaClusterStatus defines the observed state of ImpalaCluster.
type ImpalaClusterStatus struct {
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	// +optional
	Statestore ComponentStatus `json:"statestore,omitzero"`
	// +optional
	Catalog ComponentStatus `json:"catalog,omitzero"`
	// +optional
	Coordinators ComponentStatus `json:"coordinators,omitzero"`

	// +listType=map
	// +listMapKey=name
	// +optional
	ExecutorGroups []ExecutorGroupStatus `json:"executorGroups,omitempty"`

	// +optional
	Endpoints EndpointsStatus `json:"endpoints,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.image.version`
// +kubebuilder:printcolumn:name="Coordinators",type=string,JSONPath=`.status.coordinators.readyReplicas`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="HS2",type=string,JSONPath=`.status.endpoints.hiveServer2`,priority=1
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ImpalaCluster is the Schema for the impalaclusters API
type ImpalaCluster struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of ImpalaCluster
	// +required
	Spec ImpalaClusterSpec `json:"spec"`

	// status defines the observed state of ImpalaCluster
	// +optional
	Status ImpalaClusterStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ImpalaClusterList contains a list of ImpalaCluster
type ImpalaClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []ImpalaCluster `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &ImpalaCluster{}, &ImpalaClusterList{})
		return nil
	})
}
