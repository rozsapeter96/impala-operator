# API Reference

## Packages
- [impala.operator.dev/v1alpha1](#impalaoperatordevv1alpha1)


## impala.operator.dev/v1alpha1

Package v1alpha1 contains API Schema definitions for the impala v1alpha1 API group.

### Resource Types
- [ImpalaCluster](#impalacluster)



#### AdmissionControlSpec



AdmissionControlSpec generates fair-scheduler.xml and llama-site.xml.



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `defaultPool` _string_ | defaultPool receives queries that do not set REQUEST_POOL. Defaults to<br />the first pool. Queries naming an unknown pool are rejected. |  | MaxLength: 63 <br /> |
| `pools` _[PoolSpec](#poolspec) array_ | pools defines admission-control resource pools. When empty Impala uses<br />its built-in "default-pool". |  | MaxItems: 64 <br /> |


#### AutoscalingSpec



AutoscalingSpec lets the operator add and remove executor group instances
based on admission-control queue metrics.



_Appears in:_
- [ExecutorGroupSpec](#executorgroupspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  |  |
| `minGroups` _integer_ |  | 1 | Minimum: 0 <br /> |
| `maxGroups` _integer_ |  | 5 | Minimum: 1 <br /> |
| `scaleUpDelaySeconds` _integer_ | scaleUpDelaySeconds is how long queries must be queued before a group is added. | 30 | Minimum: 0 <br /> |
| `scaleDownDelaySeconds` _integer_ | scaleDownDelaySeconds is how long a group must be idle before it is removed. | 300 | Minimum: 0 <br /> |
| `cooldownSeconds` _integer_ | cooldownSeconds is the minimum time between two scaling actions. | 120 | Minimum: 0 <br /> |


#### CatalogConfig



CatalogConfig configures catalogd.



_Appears in:_
- [CatalogSpec](#catalogspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `jvmHeap` _string_ | jvmHeap is the embedded JVM -Xmx, e.g. "2g". |  |  |
| `catalogTopicMode` _string_ | catalogTopicMode is passed to -catalog_topic_mode. | minimal | Enum: [minimal full mixed] <br /> |


#### CatalogSpec



CatalogSpec configures the catalog StatefulSet. It is ignored when
clusterConfig.hiveMetastore is unset, because catalogd is not deployed then.



_Appears in:_
- [ImpalaClusterSpec](#impalaclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `args` _string array_ | args are extra daemon flags appended after the operator-generated ones,<br />so they take precedence. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#envvar-v1-core) array_ | env adds environment variables to the daemon container. |  | Schemaless: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#resourcerequirements-v1-core)_ | resources for the daemon container. |  |  |
| `podOverrides` _[PodOverrides](#podoverrides)_ |  |  |  |
| `replicas` _integer_ | replicas is 1, or 2 to enable catalogd active/standby HA. | 1 | Maximum: 2 <br />Minimum: 1 <br /> |
| `config` _[CatalogConfig](#catalogconfig)_ |  |  |  |


#### ClusterConfig



ClusterConfig holds settings shared by every daemon.



_Appears in:_
- [ImpalaClusterSpec](#impalaclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hiveMetastore` _[HiveMetastoreSpec](#hivemetastorespec)_ | hiveMetastore is the metastore catalogd serves. Omit it to run without<br />catalogd and HMS, in which case icebergRestCatalogs must list at least<br />one catalog and the coordinators read metadata from those only. |  |  |
| `icebergRestCatalogs` _[IcebergRESTCatalogSpec](#icebergrestcatalogspec) array_ | icebergRestCatalogs lists Iceberg REST catalogs the coordinators query,<br />alongside the Hive Metastore when one is configured. Tables are<br />addressed by database and table name; a name present in several<br />catalogs is rejected as ambiguous. |  | MaxItems: 16 <br /> |
| `storage` _[StorageSpec](#storagespec)_ |  |  |  |
| `kudu` _[KuduSpec](#kuduspec)_ |  |  |  |
| `admissionControl` _[AdmissionControlSpec](#admissioncontrolspec)_ |  |  |  |
| `security` _[SecuritySpec](#securityspec)_ |  |  |  |
| `configOverrides` _object (keys:string, values:object)_ | configOverrides adds or replaces properties in the generated Hadoop-style<br />XML files, keyed by file name ("hive-site.xml", "core-site.xml",<br />"hdfs-site.xml"). |  |  |
| `logging` _[LoggingSpec](#loggingspec)_ |  |  |  |


#### ComponentSpec



ComponentSpec holds knobs common to every daemon type.



_Appears in:_
- [CatalogSpec](#catalogspec)
- [CoordinatorSpec](#coordinatorspec)
- [ExecutorGroupSpec](#executorgroupspec)
- [StatestoreSpec](#statestorespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `args` _string array_ | args are extra daemon flags appended after the operator-generated ones,<br />so they take precedence. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#envvar-v1-core) array_ | env adds environment variables to the daemon container. |  | Schemaless: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#resourcerequirements-v1-core)_ | resources for the daemon container. |  |  |
| `podOverrides` _[PodOverrides](#podoverrides)_ |  |  |  |


#### ComponentStatus



ComponentStatus reports replica counts for one component.



_Appears in:_
- [ImpalaClusterStatus](#impalaclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `replicas` _integer_ |  |  |  |
| `readyReplicas` _integer_ |  |  |  |


#### CoordinatorConfig



CoordinatorConfig configures coordinator impalads.



_Appears in:_
- [CoordinatorSpec](#coordinatorspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `jvmHeap` _string_ | jvmHeap is the embedded JVM -Xmx, e.g. "2g". |  |  |
| `memLimit` _string_ | memLimit is passed to -mem_limit, e.g. "8gb" or "80%". |  |  |
| `gracefulShutdownDeadlineSeconds` _integer_ | gracefulShutdownDeadlineSeconds is -shutdown_deadline_s. The pod's<br />terminationGracePeriodSeconds is derived from it. | 900 | Minimum: 1 <br /> |
| `gracefulShutdownGracePeriodSeconds` _integer_ | gracefulShutdownGracePeriodSeconds is -shutdown_grace_period_s: the<br />minimum time a quiescing daemon waits for coordinators to notice. | 30 | Minimum: 0 <br /> |
| `defaultQueryOptions` _object (keys:string, values:string)_ | defaultQueryOptions is passed to -default_query_options. |  |  |


#### CoordinatorSpec



CoordinatorSpec configures the coordinator StatefulSet.



_Appears in:_
- [ImpalaClusterSpec](#impalaclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `args` _string array_ | args are extra daemon flags appended after the operator-generated ones,<br />so they take precedence. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#envvar-v1-core) array_ | env adds environment variables to the daemon container. |  | Schemaless: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#resourcerequirements-v1-core)_ | resources for the daemon container. |  |  |
| `podOverrides` _[PodOverrides](#podoverrides)_ |  |  |  |
| `replicas` _integer_ |  | 1 | Minimum: 0 <br /> |
| `config` _[CoordinatorConfig](#coordinatorconfig)_ |  |  |  |
| `service` _[ServiceSpec](#servicespec)_ |  |  |  |


#### DaemonConfig



DaemonConfig is shared by all daemons.



_Appears in:_
- [CatalogConfig](#catalogconfig)
- [CoordinatorConfig](#coordinatorconfig)
- [ExecutorConfig](#executorconfig)
- [ImpaladConfig](#impaladconfig)
- [StatestoreConfig](#statestoreconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `jvmHeap` _string_ | jvmHeap is the embedded JVM -Xmx, e.g. "2g". |  |  |


#### EndpointsStatus



EndpointsStatus lists the client-facing addresses.



_Appears in:_
- [ImpalaClusterStatus](#impalaclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `hiveServer2` _string_ |  |  |  |
| `hiveServer2Http` _string_ |  |  |  |
| `webUI` _string_ | webUI is the coordinators' debug web UI, reachable through the headless<br />Service only (it is unauthenticated and is not exposed on the client<br />Service). |  |  |


#### ExecutorConfig



ExecutorConfig configures executor impalads.



_Appears in:_
- [ExecutorGroupSpec](#executorgroupspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `jvmHeap` _string_ | jvmHeap is the embedded JVM -Xmx, e.g. "2g". |  |  |
| `memLimit` _string_ | memLimit is passed to -mem_limit, e.g. "8gb" or "80%". |  |  |
| `gracefulShutdownDeadlineSeconds` _integer_ | gracefulShutdownDeadlineSeconds is -shutdown_deadline_s. The pod's<br />terminationGracePeriodSeconds is derived from it. | 900 | Minimum: 1 <br /> |
| `gracefulShutdownGracePeriodSeconds` _integer_ | gracefulShutdownGracePeriodSeconds is -shutdown_grace_period_s: the<br />minimum time a quiescing daemon waits for coordinators to notice. | 30 | Minimum: 0 <br /> |
| `scratch` _[VolumeSpec](#volumespec)_ | scratch provisions a PVC for spill-to-disk (-scratch_dirs). Omit to use<br />an emptyDir. Immutable once the executor group exists, because it<br />becomes a StatefulSet volumeClaimTemplate. |  |  |
| `dataCache` _[VolumeSpec](#volumespec)_ | dataCache provisions a PVC for the remote read cache (-data_cache).<br />Omit to disable the cache. Immutable once the executor group exists. |  |  |


#### ExecutorGroupInstanceStatus



ExecutorGroupInstanceStatus reports on one executor group instance.



_Appears in:_
- [ExecutorGroupStatus](#executorgroupstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name is the Impala executor group name, e.g. "root.default-small-0". |  |  |
| `replicas` _integer_ |  |  |  |
| `readyReplicas` _integer_ |  |  |  |
| `healthy` _boolean_ | healthy is true once at least minHealthySize executors are ready. |  |  |


#### ExecutorGroupSpec



ExecutorGroupSpec describes a family of identically sized executor groups.
Each group instance is one StatefulSet registered with Impala as
"<pool>-<name>-<index>".



_Appears in:_
- [ImpalaClusterSpec](#impalaclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name identifies this executor group family; it becomes part of Kubernetes<br />object names and Impala executor group names. |  | MaxLength: 20 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` <br /> |
| `pool` _string_ | pool is the admission-control pool served by these executors. Required<br />when admissionControl.pools is set; otherwise the built-in default pool<br />is used. |  | MaxLength: 63 <br /> |
| `size` _integer_ | size is the number of executors in each group instance. |  | Minimum: 1 <br /> |
| `minHealthySize` _integer_ | minHealthySize is the number of registered executors needed before the<br />group accepts queries (defaults to size). |  | Minimum: 1 <br /> |
| `groups` _integer_ | groups is the number of group instances. Ignored while autoscaling is enabled. | 1 | Minimum: 0 <br /> |
| `autoscaling` _[AutoscalingSpec](#autoscalingspec)_ |  |  |  |
| `config` _[ExecutorConfig](#executorconfig)_ |  |  |  |
| `args` _string array_ | args are extra daemon flags appended after the operator-generated ones,<br />so they take precedence. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#envvar-v1-core) array_ | env adds environment variables to the daemon container. |  | Schemaless: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#resourcerequirements-v1-core)_ | resources for the daemon container. |  |  |
| `podOverrides` _[PodOverrides](#podoverrides)_ |  |  |  |


#### ExecutorGroupStatus



ExecutorGroupStatus reports on one executor group family.



_Appears in:_
- [ImpalaClusterStatus](#impalaclusterstatus)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  |  |
| `desiredGroups` _integer_ | desiredGroups is the group count chosen by the autoscaler (or spec.groups). |  |  |
| `lastScaleTime` _[Time](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#time-v1-meta)_ |  |  |  |
| `groups` _[ExecutorGroupInstanceStatus](#executorgroupinstancestatus) array_ |  |  |  |


#### HiveMetastoreSpec



HiveMetastoreSpec points Impala at an external Hive Metastore. The
operator deploys catalogd only when a metastore is configured; without one
the coordinators serve metadata straight from the Iceberg REST catalogs in
clusterConfig.icebergRestCatalogs.



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `uris` _string_ | uris is the value of hive.metastore.uris, e.g. "thrift://hms.default.svc:9083". |  | MinLength: 1 <br /> |
| `eventPollingIntervalSeconds` _integer_ | eventPollingIntervalSeconds sets -hms_event_polling_interval_s on catalogd.<br />0 disables HMS event processing. | 1 | Minimum: 0 <br /> |


#### IcebergRESTCatalogSpec



IcebergRESTCatalogSpec connects the coordinators to one Iceberg REST
catalog (Apache Polaris, Lakekeeper, Gravitino, Unity, ...). Each entry is
rendered as a Java properties file under -catalog_config_dir. Requires an
Impala build that includes IMPALA-13586 (master after 4.5).



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name identifies the catalog. It is the properties file name and the<br />value of iceberg.rest-catalog.name, which Impala needs to route INSERT<br />INTO statements, so it must be unique within the cluster. |  | MaxLength: 63 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` <br /> |
| `uri` _string_ | uri of the REST catalog endpoint, e.g. "http://polaris:8181/api/catalog". |  | Pattern: `^https?://.+` <br /> |
| `warehouse` _string_ | warehouse is passed as iceberg.rest-catalog.warehouse. Polaris uses it<br />to select the catalog by name; other servers take a storage location. |  |  |
| `prefix` _string_ | prefix is passed as iceberg.rest-catalog.prefix. |  |  |
| `oauth2` _[RESTCatalogOAuth2Spec](#restcatalogoauth2spec)_ |  |  |  |
| `vendedCredentials` _boolean_ | vendedCredentials asks the catalog for per-table storage credentials on<br />loadTable (iceberg.rest-catalog.vended-credentials-enabled). Leave it<br />off to read table data with the cluster's own storage credentials. |  |  |
| `properties` _object (keys:string, values:string)_ | properties adds or overrides raw entries in the properties file, for<br />example "io-impl" or Trino-style "iceberg.rest-catalog.*" keys. Impala<br />resolves "$\{ENV:NAME\}" references against the coordinator environment,<br />so secrets can be supplied through coordinators.env. |  |  |


#### ImageSpec



ImageSpec selects the Impala container image family.



_Appears in:_
- [ImpalaClusterSpec](#impalaclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `repository` _string_ | repository is the image repository. Component names are appended as<br />"<version>-<component>" tags, matching the official apache/impala layout. | apache/impala |  |
| `version` _string_ | version is the Impala version, e.g. "4.5.2". | 4.5.2 | MinLength: 1 <br /> |
| `pullPolicy` _[PullPolicy](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#pullpolicy-v1-core)_ | pullPolicy for all Impala containers. | IfNotPresent |  |
| `pullSecrets` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#localobjectreference-v1-core) array_ | pullSecrets for private registries. |  |  |


#### ImpalaCluster



ImpalaCluster is the Schema for the impalaclusters API





| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `apiVersion` _string_ | `impala.operator.dev/v1alpha1` | | |
| `kind` _string_ | `ImpalaCluster` | | |
| `metadata` _[ObjectMeta](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#objectmeta-v1-meta)_ | Refer to Kubernetes API documentation for fields of `metadata`. |  |  |
| `spec` _[ImpalaClusterSpec](#impalaclusterspec)_ | spec defines the desired state of ImpalaCluster |  |  |
| `status` _[ImpalaClusterStatus](#impalaclusterstatus)_ | status defines the observed state of ImpalaCluster |  |  |


#### ImpalaClusterSpec



ImpalaClusterSpec defines the desired state of ImpalaCluster.



_Appears in:_
- [ImpalaCluster](#impalacluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `image` _[ImageSpec](#imagespec)_ |  |  |  |
| `clusterConfig` _[ClusterConfig](#clusterconfig)_ |  |  |  |
| `statestore` _[StatestoreSpec](#statestorespec)_ |  |  |  |
| `catalog` _[CatalogSpec](#catalogspec)_ |  |  |  |
| `coordinators` _[CoordinatorSpec](#coordinatorspec)_ |  |  |  |
| `executorGroups` _[ExecutorGroupSpec](#executorgroupspec) array_ |  |  | MaxItems: 32 <br /> |


#### ImpalaClusterStatus



ImpalaClusterStatus defines the observed state of ImpalaCluster.



_Appears in:_
- [ImpalaCluster](#impalacluster)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `observedGeneration` _integer_ |  |  |  |
| `conditions` _[Condition](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#condition-v1-meta) array_ |  |  |  |
| `statestore` _[ComponentStatus](#componentstatus)_ |  |  |  |
| `catalog` _[ComponentStatus](#componentstatus)_ |  |  |  |
| `coordinators` _[ComponentStatus](#componentstatus)_ |  |  |  |
| `executorGroups` _[ExecutorGroupStatus](#executorgroupstatus) array_ |  |  |  |
| `endpoints` _[EndpointsStatus](#endpointsstatus)_ |  |  |  |


#### ImpaladConfig



ImpaladConfig is shared by coordinators and executors.



_Appears in:_
- [CoordinatorConfig](#coordinatorconfig)
- [ExecutorConfig](#executorconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `jvmHeap` _string_ | jvmHeap is the embedded JVM -Xmx, e.g. "2g". |  |  |
| `memLimit` _string_ | memLimit is passed to -mem_limit, e.g. "8gb" or "80%". |  |  |
| `gracefulShutdownDeadlineSeconds` _integer_ | gracefulShutdownDeadlineSeconds is -shutdown_deadline_s. The pod's<br />terminationGracePeriodSeconds is derived from it. | 900 | Minimum: 1 <br /> |
| `gracefulShutdownGracePeriodSeconds` _integer_ | gracefulShutdownGracePeriodSeconds is -shutdown_grace_period_s: the<br />minimum time a quiescing daemon waits for coordinators to notice. | 30 | Minimum: 0 <br /> |


#### KerberosSpec



KerberosSpec enables Kerberos authentication.



_Appears in:_
- [SecuritySpec](#securityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | enabled turns on Kerberos for clients and internal RPC. |  |  |
| `principal` _string_ | principal in "service/_HOST@REALM" form, passed to -principal. |  |  |
| `keytabSecretRef` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#localobjectreference-v1-core)_ | keytabSecretRef names a Secret containing the keytab under key "impala.keytab". |  |  |
| `krb5ConfigMapRef` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#localobjectreference-v1-core)_ | krb5ConfigMapRef names a ConfigMap containing krb5.conf under key "krb5.conf". |  |  |


#### KuduSpec



KuduSpec configures an optional Kudu integration.



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `masterHosts` _string_ | masterHosts is the value of -kudu_master_hosts, e.g. "kudu-master-0:7051,kudu-master-1:7051". |  | MinLength: 1 <br /> |


#### LDAPSpec



LDAPSpec enables LDAP password authentication for clients.



_Appears in:_
- [SecuritySpec](#securityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | enabled turns on -enable_ldap_auth. |  |  |
| `uri` _string_ | uri of the LDAP server, e.g. "ldaps://ldap.example.com". |  |  |
| `bindPattern` _string_ | bindPattern is passed to -ldap_bind_pattern, e.g. "uid=#UID,ou=people,dc=example,dc=com". |  |  |
| `domain` _string_ | domain is passed to -ldap_domain (Active Directory style). |  |  |
| `baseDN` _string_ | baseDN is passed to -ldap_baseDN. |  |  |
| `caCertSecretRef` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#localobjectreference-v1-core)_ | caCertSecretRef names a Secret with key "ca.crt" for -ldap_ca_certificate. |  |  |
| `allowPasswordsInClear` _boolean_ | allowPasswordsInClear sets -ldap_passwords_in_clear_ok (only for testing). |  |  |


#### LoggingSpec



LoggingSpec controls glog output.



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `verbosity` _integer_ | verbosity is the glog -v level. | 1 | Maximum: 3 <br />Minimum: 0 <br /> |


#### NetworkPolicySpec



NetworkPolicySpec isolates the cluster's daemons on the pod network.
Without Kerberos, Impala's internal ports (statestore, catalog, KRPC,
statestore subscriber) accept any peer, so any pod that can reach them can
register as an executor and receive query data. Enabling this renders one
NetworkPolicy per cluster that admits:

  - every port from the cluster's own pods;
  - the HiveServer2 client ports (21050, 28000) from clientFrom, or from
    anywhere when clientFrom is empty;
  - the daemon web UI ports (25000, 25010, 25020) from the operator's own
    namespace (the autoscaler scrapes them) and from webUIFrom.

Egress is not restricted. Kubelet probes are exempt from NetworkPolicy.



_Appears in:_
- [SecuritySpec](#securityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ |  |  |  |
| `clientFrom` _[NetworkPolicyPeer](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#networkpolicypeer-v1-networking) array_ | clientFrom lists the peers allowed to reach the coordinator client<br />ports. Leave empty to allow any source. |  | Schemaless: \{\} <br /> |
| `webUIFrom` _[NetworkPolicyPeer](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#networkpolicypeer-v1-networking) array_ | webUIFrom lists additional peers allowed to reach the daemon web UI<br />ports, e.g. a Prometheus namespace. Cluster pods and the operator's<br />namespace are always allowed. |  | Schemaless: \{\} <br /> |


#### PodOverrides



PodOverrides customises the generated pod template of a component.



_Appears in:_
- [CatalogSpec](#catalogspec)
- [ComponentSpec](#componentspec)
- [CoordinatorSpec](#coordinatorspec)
- [ExecutorGroupSpec](#executorgroupspec)
- [StatestoreSpec](#statestorespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `labels` _object (keys:string, values:string)_ |  |  |  |
| `annotations` _object (keys:string, values:string)_ |  |  |  |
| `nodeSelector` _object (keys:string, values:string)_ |  |  |  |
| `tolerations` _[Toleration](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#toleration-v1-core) array_ |  |  | Schemaless: \{\} <br /> |
| `affinity` _[Affinity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#affinity-v1-core)_ |  |  | Schemaless: \{\} <br /> |
| `topologySpreadConstraints` _[TopologySpreadConstraint](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#topologyspreadconstraint-v1-core) array_ |  |  | Schemaless: \{\} <br /> |
| `priorityClassName` _string_ |  |  |  |
| `serviceAccountName` _string_ | serviceAccountName runs the pods under a specific ServiceAccount, for<br />example one bound to a cloud IAM role. |  |  |
| `automountServiceAccountToken` _boolean_ | automountServiceAccountToken controls whether the ServiceAccount token<br />is mounted into the daemon pods. Impala executes user SQL and native<br />UDFs, so the token is withheld by default; it is mounted when<br />serviceAccountName is set unless this is explicitly false. |  |  |
| `securityContext` _[PodSecurityContext](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#podsecuritycontext-v1-core)_ |  |  | Schemaless: \{\} <br /> |
| `volumes` _[Volume](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#volume-v1-core) array_ |  |  | Schemaless: \{\} <br /> |
| `volumeMounts` _[VolumeMount](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#volumemount-v1-core) array_ |  |  | Schemaless: \{\} <br /> |
| `initContainers` _[Container](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#container-v1-core) array_ |  |  | Schemaless: \{\} <br /> |
| `sidecars` _[Container](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#container-v1-core) array_ |  |  | Schemaless: \{\} <br /> |


#### PoolSpec



PoolSpec is an admission-control resource pool. Impala names it "root.<name>".



_Appears in:_
- [AdmissionControlSpec](#admissioncontrolspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ | name of the pool without the "root." prefix. |  | MaxLength: 63 <br />Pattern: `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$` <br /> |
| `maxRunningQueries` _integer_ | maxRunningQueries limits concurrently running queries (-1 = unlimited). |  |  |
| `maxQueuedQueries` _integer_ | maxQueuedQueries limits queued queries before rejection. |  |  |
| `queueTimeoutMs` _integer_ | queueTimeoutMs is how long a query may wait in the queue. |  |  |
| `maxMemResources` _string_ | maxMemResources is the pool's aggregate memory cap in fair-scheduler<br />syntax, e.g. "50000 mb". |  |  |
| `maxQueryMemLimit` _string_ | maxQueryMemLimit caps the per-node memory of a query, e.g. "8gb". |  |  |
| `minQueryMemLimit` _string_ | minQueryMemLimit floors the per-node memory of a query, e.g. "1gb". |  |  |
| `defaultQueryOptions` _object (keys:string, values:string)_ | defaultQueryOptions applied to every query in the pool. |  |  |


#### RESTCatalogOAuth2Spec



RESTCatalogOAuth2Spec authenticates to an Iceberg REST catalog with the
OAuth2 client-credentials flow (iceberg.rest-catalog.security=OAUTH2).



_Appears in:_
- [IcebergRESTCatalogSpec](#icebergrestcatalogspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `credentialSecretRef` _[SecretKeyReference](#secretkeyreference)_ | credentialSecretRef names a Secret key holding the OAuth2 client<br />credential in Iceberg's "<client-id>:<client-secret>" form. The value is<br />injected into the coordinator pods as an environment variable and<br />referenced from the properties file with Impala's $\{ENV:...\}<br />substitution, so it never lands in the ConfigMap. Rotating the Secret<br />rolls the coordinators. The key defaults to "credential". |  |  |
| `serverURI` _string_ | serverURI is the token endpoint (iceberg.rest-catalog.oauth2.server-uri).<br />Defaults to the catalog's own "<uri>/v1/oauth/tokens" endpoint, which<br />is what Apache Polaris serves. |  |  |
| `scope` _string_ | scope requested with the token (iceberg.rest-catalog.oauth2.scope).<br />Apache Polaris expects "PRINCIPAL_ROLE:ALL" or a specific principal role. |  |  |


#### S3Spec



S3Spec configures S3A access for table data.



_Appears in:_
- [StorageSpec](#storagespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `endpoint` _string_ | endpoint overrides fs.s3a.endpoint (leave empty for AWS). |  |  |
| `region` _string_ | region sets fs.s3a.endpoint.region. |  |  |
| `pathStyleAccess` _boolean_ | pathStyleAccess sets fs.s3a.path.style.access, required by MinIO and most on-prem stores. |  |  |
| `credentialsSecretRef` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#localobjectreference-v1-core)_ | credentialsSecretRef names a Secret with keys AWS_ACCESS_KEY_ID and<br />AWS_SECRET_ACCESS_KEY (optionally AWS_SESSION_TOKEN). They are injected as<br />environment variables. Omit to rely on IAM roles / IRSA. |  |  |


#### SecretKeyReference



SecretKeyReference selects one key of a Secret in the cluster's namespace.



_Appears in:_
- [RESTCatalogOAuth2Spec](#restcatalogoauth2spec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `name` _string_ |  |  | MinLength: 1 <br /> |
| `key` _string_ |  | credential |  |


#### SecuritySpec



SecuritySpec groups authentication, encryption and network isolation settings.



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `tls` _[TLSSpec](#tlsspec)_ |  |  |  |
| `kerberos` _[KerberosSpec](#kerberosspec)_ |  |  |  |
| `ldap` _[LDAPSpec](#ldapspec)_ |  |  |  |
| `networkPolicy` _[NetworkPolicySpec](#networkpolicyspec)_ |  |  |  |


#### ServiceSpec



ServiceSpec customises the client-facing Service.



_Appears in:_
- [CoordinatorSpec](#coordinatorspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `type` _[ServiceType](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#servicetype-v1-core)_ |  | ClusterIP |  |
| `annotations` _object (keys:string, values:string)_ |  |  |  |


#### StatestoreConfig



StatestoreConfig configures statestored.



_Appears in:_
- [StatestoreSpec](#statestorespec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `jvmHeap` _string_ | jvmHeap is the embedded JVM -Xmx, e.g. "2g". |  |  |


#### StatestoreSpec



StatestoreSpec configures the statestore StatefulSet.



_Appears in:_
- [ImpalaClusterSpec](#impalaclusterspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `args` _string array_ | args are extra daemon flags appended after the operator-generated ones,<br />so they take precedence. |  |  |
| `env` _[EnvVar](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#envvar-v1-core) array_ | env adds environment variables to the daemon container. |  | Schemaless: \{\} <br /> |
| `resources` _[ResourceRequirements](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#resourcerequirements-v1-core)_ | resources for the daemon container. |  |  |
| `podOverrides` _[PodOverrides](#podoverrides)_ |  |  |  |
| `config` _[StatestoreConfig](#statestoreconfig)_ |  |  |  |


#### StorageSpec



StorageSpec configures the object stores Impala reads and writes.



_Appears in:_
- [ClusterConfig](#clusterconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `s3` _[S3Spec](#s3spec)_ | s3 enables S3A configuration in core-site.xml. |  |  |


#### TLSSpec



TLSSpec enables TLS for client, internal and web endpoints.



_Appears in:_
- [SecuritySpec](#securityspec)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `enabled` _boolean_ | enabled turns on TLS for HiveServer2/Beeswax client connections and the<br />web UI of all daemons. |  |  |
| `certSecretRef` _[LocalObjectReference](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#localobjectreference-v1-core)_ | certSecretRef names a kubernetes.io/tls Secret (tls.crt, tls.key, ca.crt).<br />The certificate must cover the coordinator and executor pod DNS names,<br />typically with a wildcard "*.<cluster>-<component>-hl.<ns>.svc.cluster.local". |  |  |
| `internal` _boolean_ | internal also encrypts daemon-to-daemon traffic (Thrift and KRPC). | true |  |
| `minimumVersion` _string_ | minimumVersion is passed to -ssl_minimum_version. |  | Enum: [tlsv1.2 tlsv1.3] <br /> |


#### VolumeSpec



VolumeSpec requests a persistent volume for scratch or cache data.



_Appears in:_
- [ExecutorConfig](#executorconfig)

| Field | Description | Default | Validation |
| --- | --- | --- | --- |
| `size` _[Quantity](https://kubernetes.io/docs/reference/generated/kubernetes-api/v1.37/#quantity-resource-api)_ |  |  |  |
| `storageClassName` _string_ |  |  |  |


