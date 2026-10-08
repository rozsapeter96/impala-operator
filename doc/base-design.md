# Design

This page records the decisions behind the impala-operator and the reasons
for them. For how the code is organised see [implementation.md](implementation.md);
for the field reference see [api.md](api.md).

## Goal

A production-shaped Kubernetes operator that deploys and manages Apache
Impala 4.x clusters in the layout Impala uses for cloud deployments: one
statestore, one or two catalog daemons, dedicated coordinators, and executor
groups that can be added and removed independently. The operator owns
deployment, configuration, scaling, upgrades, graceful shutdown, security
wiring (TLS, Kerberos, LDAP) and executor group autoscaling.

The Hive Metastore, Iceberg REST catalogs and the object store are external
dependencies. The operator configures Impala to use them but does not run
any of them.

## Decisions

**One custom resource.** `ImpalaCluster` describes the whole cluster. A
separate `ExecutorGroup` kind was considered and rejected for v1: executor
groups only make sense inside a cluster, and one resource keeps rollout
ordering and status in one place.

**Executor group families.** Impala schedules a query on exactly one
executor group, so capacity is added in whole groups rather than by growing
one. The spec therefore describes a *family* (size and configuration) and the
number of identical *instances*; each instance is a StatefulSet registered
with Impala as `<pool>-<family>-<index>`. Impala requires the pool prefix.

**StatefulSets everywhere.** Every daemon needs a stable DNS name for
statestore registration and TLS SANs, and executors may need persistent
scratch or cache volumes. StatefulSets give both; Deployments give neither.

**Validation with CRD markers and CEL, no webhooks.** Admission webhooks need
a certificate, which drags in cert-manager or a self-signed rotation scheme.
Everything the operator has to validate is expressible in CEL, including
cross-field rules and immutability of the PVC templates.

**Server-side apply.** Objects are rendered as pure functions of the spec and
applied with a fixed field manager and forced ownership. This avoids
read-modify-write conflicts with defaulting and other controllers, and makes
the builders unit-testable without a cluster.

**Config-hash rollouts.** Generated configuration and the contents of
referenced Secrets are hashed into a pod-template annotation. A configuration
change or a Secret rotation therefore rolls the affected pods through the
normal StatefulSet update path.

**Tier-ordered rollout.** Changes are applied statestore, catalog,
coordinators, executor groups, and a later tier is not touched until the
previous one has converged. Initial creation is not serialised, so a new
cluster still comes up in parallel.

**Graceful shutdown through a preStop hook.** Impala treats `SIGTERM` as an
immediate kill and has no HTTP shutdown endpoint; `SIGRTMIN` starts the
graceful path. Every impalad pod gets a blocking preStop hook that sends it
and waits, with the termination grace period derived from Impala's shutdown
deadline.

**Autoscaling in the operator, from admission-control state.** Open-source
Impala has no autoscaler. The operator polls the coordinators and adds a
group only when queries queue for a reason more groups can relieve (slot or
per-host memory saturation, or no group at all), and removes the newest
group once it has been healthy and idle. Pool-level caps also queue queries
but more groups cannot help, so those reasons are ignored. Decisions go to
status, never to the spec.

**Iceberg REST catalogs through Impala's own mechanism.** Impala `master`
reads REST catalogs from properties files in `-catalog_config_dir`, next to
or instead of catalogd. The operator renders those files from
`clusterConfig.icebergRestCatalogs` rather than inventing its own
abstraction, and ties the two deployment modes to one knob: catalogd is
deployed exactly when `hiveMetastore` is set, because catalogd needs HMS and
HMS tables are only reachable through catalogd. OAuth2 credentials reach the
file through Impala's `${ENV:...}` substitution from a Secret-backed
environment variable, so the ConfigMap never holds them.

**Secrets are referenced, never copied.** Credentials, certificates and
keytabs are mounted or injected from Secrets in the cluster's namespace.
Nothing secret is written into the generated ConfigMap.

**Network isolation is opt-in.** Without Kerberos, Impala's internal ports
accept any peer. A generated NetworkPolicy can restrict them to the
cluster's own pods, but it only helps on CNIs that enforce policy, so it is
not on by default.

## Upstream facts the design depends on

Verified against the `apache/impala` images and source:

- The official images are `apache/impala:<version>-{statestored,catalogd,impalad_coordinator,impalad_executor}`,
  run as uid 1000, read configuration from `/opt/impala/conf`, and `exec`
  the daemon so it is PID 1 (the preStop hook signals PID 1).
- `/healthz` returns 200 only after statestore registration, so it is a
  correct readiness signal.
- `-executor_groups=<name>:<minSize>` registers a group; the name must start
  with the pool name followed by `-`.
- Without an explicit fair-scheduler placement policy Impala routes each
  user to `root.<user>`, which no executor group serves. The operator renders
  `specified(create=false)` plus a default rule.
- `/admission?json` exposes `resource_pools[].head_queued_reason`, the basis
  of the scale-up decision. `/jsonmetrics` is broken in the published images;
  `/metrics?json` works.
- Every Impala web server, including the metrics-only one, takes TLS from
  the global certificate flag, and the metrics-only server does not serve
  `/admission`. The autoscaler therefore scrapes the main web port over
  HTTPS when TLS is on.
- Catalog HA is active/standby (`-enable_catalogd_ha`, two replicas).
- `-catalog_config_dir` (IMPALA-13586, `master` only) loads every file in
  the directory as a REST catalog and requires `connector.name=iceberg` and
  `iceberg.catalog.type=rest` in each, so the properties files get their own
  mount. `-catalogd_deployed=false` makes impalad skip the catalog topic and
  catalog metrics; with `-use_local_catalog=true` the frontend then serves
  metadata from the REST catalogs alone. `${ENV:NAME}` in a property value
  is resolved from the process environment at startup.

## Out of scope for v1

Statestore HA, the standalone `admissiond`, conversion webhooks, an OLM
bundle, Ranger authorization, HDFS-only deployments, and an operator-managed
Hive Metastore.
