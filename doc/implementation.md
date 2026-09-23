# Implementation

This page documents how the impala-operator is built: the packages, the two
controllers, how desired state is rendered and applied, and the runtime
behaviours (rollout ordering, config-driven restarts, graceful shutdown and
autoscaling). For the CRD field reference see [api.md](api.md); for the
rationale and history see [base-design.md](base-design.md).

## Overview

The operator is a standard controller-runtime manager built with kubebuilder
(go/v4 layout). It watches a single custom resource, `ImpalaCluster`, and
reconciles a full Impala 4.x deployment: one statestore, one or two catalogs,
a set of coordinators, and one or more executor group families. Two
controllers run in the same manager process:

- **ImpalaClusterReconciler** owns the desired state. It renders every
  Kubernetes object from the spec and applies them.
- **ExecutorGroupAutoscaler** owns the executor group count when autoscaling
  is enabled. It reads coordinator metrics and records a target group count in
  status, which the first controller then materialises.

```
                    ┌────────────────────────────────────────────┐
                    │              manager (cmd/main.go)          │
                    │                                             │
   ImpalaCluster ──▶│  ImpalaClusterReconciler ──▶ ConfigMap,     │
   (spec)           │      │  ▲                     StatefulSets,  │
                    │      │  │ Owns()              Services, PDBs │
                    │      ▼  │                                    │
                    │   status.executorGroups[].desiredGroups     │
                    │      ▲                                       │
                    │      │ writes                                │
                    │  ExecutorGroupAutoscaler ◀─ /admission?json ─┼──▶ coordinators
                    └────────────────────────────────────────────┘
```

## Package layout

| Path | Responsibility |
|---|---|
| `api/v1alpha1/impalacluster_types.go` | The `ImpalaCluster` spec and status types, validation markers and CEL rules. |
| `internal/resources/` | Pure builders: functions of the spec that return Kubernetes objects. No cluster access, so they are unit-tested in isolation. |
| `internal/controller/impalacluster_controller.go` | The main reconcile loop: render, apply, gate, prune, report status. |
| `internal/controller/apply.go` | Server-side apply helper (typed object → pruned unstructured → `client.Apply`). |
| `internal/controller/autoscaler_controller.go` | The executor group autoscaler. |
| `internal/impala/metrics.go` | HTTP client that reads and flattens a daemon's `/metrics?json` page. |
| `cmd/main.go` | Manager setup; registers both controllers. |

### internal/resources

Every managed object comes from a builder here. The important ones:

- `BuildConfigMap` renders the Hadoop-style XML files (`hive-site.xml`,
  `core-site.xml`, `fair-scheduler.xml`, `llama-site.xml`) and returns a
  ConfigMap. `ConfigHash` produces a stable SHA-256 of its contents.
- `BuildStatestore`, `BuildCatalog`, `BuildCoordinators` each return a
  StatefulSet plus its headless and client Services.
- `BuildExecutorGroup` returns the StatefulSet for one group instance;
  `BuildExecutorGroupHeadlessService` returns the Service shared by a family.
- `BuildPDB` returns a `maxUnavailable: 1` PodDisruptionBudget.
- `Build` (in `build.go`) assembles all of the above into a `Desired` struct,
  ordered into tiers.

The shared pod template lives in `pod.go` (`buildStatefulSet`). It sets the
container image, args, env, probes, volumes, security context, graceful
shutdown hook and the config-hash annotation. `security.go` adds the TLS,
Kerberos and LDAP flags and the Secret/ConfigMap volumes. `names.go` centralises
object names, ports and the executor group naming scheme; `labels.go`
centralises labels and selectors.

## The ImpalaCluster resource

Spec is grouped by component: `image`, `clusterConfig` (HMS, storage,
admission control, security, overrides), `statestore`, `catalog`,
`coordinators`, and a list of `executorGroups`. Status reports
`observedGeneration`, conditions, per-component replica counts, per-family
executor group health, and the client endpoints.

Three conditions are reported, following Kubernetes conventions and set with
`meta.SetStatusCondition`:

| Condition | Meaning |
|---|---|
| `Ready` | Every component has all replicas ready and every executor group is healthy. |
| `Progressing` | A rollout is in flight (a StatefulSet has not converged to its latest revision). |
| `Degraded` | The spec is invalid or an object could not be applied (for example a referenced Secret is missing). |

Validation is done entirely with CRD markers and CEL `x-kubernetes-validations`
rules, so there are no admission webhooks and no cert-manager dependency. CEL
rules enforce that every `executorGroup.pool` names a configured pool, that
`minHealthySize <= size`, and that `minGroups <= maxGroups`. Rules are guarded
with `has()` for optional fields, and lists carry `maxItems` so the API
server's CEL cost budget is satisfied.

## Naming and topology

For a cluster named `demo` in namespace `ns`:

| Object | Name |
|---|---|
| Config | `demo-conf` |
| Statestore | `demo-statestore` (+ `-hl` headless Service) |
| Catalog | `demo-catalog` (+ `-hl`) |
| Coordinators | `demo-coordinator` (client Service, HS2 ports only) + `demo-coordinator-hl` (headless, adds KRPC and web UI) |
| Executor group instance | `demo-exec-<family>-<index>` |
| Executor group headless Service | `demo-exec-<family>-hl` |
| NetworkPolicy (opt-in) | `demo-impala` |

Every daemon is a StatefulSet so it gets a stable pod identity and DNS name.
Pods are addressed by `<pod>.<headless>.<ns>.svc.cluster.local`, passed to the
daemon as `-hostname` with `-use_resolved_hostname=false` so registration and
TLS SANs match the DNS name rather than the pod IP.

Executor groups use a two-level model. An **executor group family**
(`spec.executorGroups[i]`) describes the size and configuration of a group;
`groups` (or the autoscaler) sets how many identical instances exist. Each
instance registers with Impala under `-executor_groups=<pool>-<family>-<index>:<minHealthySize>`.
Impala requires the pool prefix: it schedules queries of pool `root.<pool>`
only onto groups named `root.<pool>-...`.

## Reconcile loop

`ImpalaClusterReconciler.Reconcile` is level-based and idempotent:

1. **Fetch** the CR. If it is being deleted, return (owner references garbage
   collect the children; there is no finalizer).
2. **Hash referenced data.** Read every referenced Secret and user ConfigMap
   (S3 credentials, TLS cert, keytab, krb5.conf) and fold their contents into a
   digest. A missing reference sets `Degraded` and stops.
3. **Render** the full desired state with `resources.Build`, passing the
   autoscaler's target group counts and the referenced-data digest. The digest
   is mixed into the config hash, so rotating a Secret changes the hash.
4. **Apply the ConfigMap** and the NetworkPolicy (or delete a stale one),
   then apply each tier in order.
5. **Gate on readiness** (see below).
6. **Prune** executor group StatefulSets, PDBs and Services that are no longer
   desired (label-selected and owner-checked).
7. **Update status**: replica counts, executor group health, endpoints.
8. **Set conditions** and, while progressing, requeue after 15 seconds.

### Server-side apply

`applyOwned` (in `apply.go`) sets the controller owner reference, converts the
typed object to unstructured, prunes fields that must not appear in an apply
configuration (`status`, null `creationTimestamp`, PVC-template status), and
calls `client.Apply` with a fixed field manager (`impala-operator`) and
`ForceOwnership`. Server-side apply avoids read-modify-write conflicts and
defaulting drift. The applied object is decoded back so status can read live
values.

### Rollout ordering

Objects are applied in dependency order: statestore, catalog, coordinators,
then executor groups. After each of the first three tiers the reconciler
checks whether the StatefulSet has converged (`statefulSetReady`: observed
generation current, update revision equals current revision, updated and ready
replicas equal the desired count). If a tier is not ready, later tiers are not
touched and the CR is marked `Progressing`. A freshly created StatefulSet with
no revision yet is treated as ready, so initial creation is not serialised and
all tiers come up in parallel; the gate only slows down *changes* to an
already-running cluster, so an image or config change rolls out bottom-up.

### Config-driven restarts

`BuildConfigMap` output is hashed and the digest, combined with the
referenced-data digest, is written to every pod template as the annotation
`impala.operator.dev/config-hash`. When configuration or a referenced Secret
changes, the annotation changes, the StatefulSet's pod template changes, and
the StatefulSet controller performs a rolling update. The operator watches the
referenced Secrets and ConfigMaps through field indexes
(`.spec.clusterConfig.secretRefs`, `.spec.clusterConfig.configMapRefs`) so a
Secret rotation enqueues the owning cluster.

### Watches

`SetupWithManager` registers:

```
For(&ImpalaCluster{}).
  Owns(&StatefulSet{}).Owns(&Service{}).Owns(&ConfigMap{}).Owns(&PodDisruptionBudget{}).Owns(&NetworkPolicy{}).
  Watches(&Secret{},    mapToClustersReferencing(secretIndex)).
  Watches(&ConfigMap{}, mapToClustersReferencing(configMapIndex))
```

`Owns` requeues the parent when a child changes; the `Watches` map referenced
Secrets and ConfigMaps back to the clusters that use them.

## Pods

The shared pod template applies to every daemon:

- Runs as uid/gid 1000 (`RunAsNonRoot`), all capabilities dropped, no
  privilege escalation, `RuntimeDefault` seccomp profile — satisfying the
  restricted Pod Security Standard.
- `/opt/impala/conf` mounted read-only from the ConfigMap; `/opt/impala/logs`
  an emptyDir; TLS, keytab and LDAP CA projected under `/opt/impala/secrets`.
- `JAVA_TOOL_OPTIONS=-Xmx<jvmHeap>` sets the embedded JVM heap.
- Readiness, liveness and startup probes hit `/healthz` on the daemon's web
  port; the scheme switches to HTTPS when TLS is enabled. Impala's `/healthz`
  returns 200 only after statestore registration, so it is a correct readiness
  signal.
- Prometheus scrape annotations point at `/metrics_prometheus`.
- The ServiceAccount token is not mounted unless `podOverrides.serviceAccountName`
  or `podOverrides.automountServiceAccountToken` asks for it.
- Executor pods get a `scratch` volume (PVC or emptyDir) for spill and an
  optional `dataCache` PVC; the cache flag reserves 90% of the PVC for
  filesystem overhead.
- User-supplied `args` are appended last, so they override anything the
  operator generated.

### Daemon flags

Flags are assembled per component. Common impalad flags set the statestore
host, KRPC and subscriber ports, and `-mem_limit`. Coordinators add
`-is_executor=false -use_local_catalog=true`, the catalog host, HS2 ports, the
admission-control config paths, and `-num_expected_executors`. Executors add
`-is_coordinator=false`, their `-executor_groups` registration and
`-scratch_dirs`/`-data_cache`. Catalog with two replicas and its statestore
both get `-enable_catalogd_ha=true`.

## Graceful shutdown

Impala treats `SIGTERM` as an immediate kill, so relying on the default pod
termination signal would abort running queries. Instead every coordinator and
executor pod has a preStop hook:

```
kill -s RTMIN 1; while kill -0 1 2>/dev/null; do sleep 2; done
```

`SIGRTMIN` starts Impala's graceful shutdown (quiesce, stop accepting new
fragments, wait for running queries), and the loop blocks until the process
exits. `terminationGracePeriodSeconds` is derived as
`gracePeriod + deadline + 60`, so Kubernetes waits long enough for
`-shutdown_deadline_s` to elapse. This path covers scale-down, rolling
updates and node drains: removing an executor group or lowering `size` drains
the highest-ordinal pods first, and their queries finish before the pod exits.

## Admission control and query placement

When `clusterConfig.admissionControl.pools` is set, the operator renders
`fair-scheduler.xml` (per-pool memory caps) and `llama-site.xml` (running and
queued limits, queue timeout, query memory bounds) and points the coordinators
at them. It also renders a `queuePlacementPolicy`:

```xml
<rule name="specified" create="false"/>
<rule name="default" queue="root.<defaultPool>"/>
```

Without this policy Impala's fair scheduler creates a `root.<username>` pool
per user, which no executor group serves, and queries queue forever. The policy
routes queries with `REQUEST_POOL` set to that pool (unknown names rejected)
and everything else to `admissionControl.defaultPool` (the first pool if
unset).

## Autoscaler

`ExecutorGroupAutoscaler` is a second controller reconciling the same kind. It
reacts to spec changes (generation-changed predicate) and otherwise requeues
itself every `PollInterval` (default 15s) for clusters that have any family
with `autoscaling.enabled`.

Each round it scrapes every ready coordinator pod at `/admission?json` (for
the scale-up decision) and `/metrics?json` (for the scale-down decision) via
`internal/impala`, and for each autoscaled family:

- **Scale up** by one instance when a query has been queued in the family's
  pool for a reason more executors can relieve — `head_queued_reason` is one of
  "Not enough admission control slots", "Not enough memory available on host",
  or "Waiting for executors to start" — held for `scaleUpDelaySeconds`, and the
  current count is below `maxGroups`. Queues caused by the pool's
  `max-running-queries` cap or aggregate memory limit are ignored, because a
  new group cannot drain them.
- **Scale down** the highest-index instance when it has been idle
  (`admission-controller.executor-group.num-queries-executing.<group>` == 0
  across all coordinators) for `scaleDownDelaySeconds`, and the count is above
  `minGroups`.
- `cooldownSeconds` is the minimum gap between two actions.

Per-family timers (`queuedSince`, `idleSince`) are held in the controller's
memory. Decisions are written to `status.executorGroups[].desiredGroups` and
`lastScaleTime`, and a `ScaledUp` / `ScaledDown` event is emitted. The
autoscaler never edits the spec. The main reconciler reads `desiredGroups`
(when a `lastScaleTime` is present) and creates or deletes the corresponding
StatefulSets; removing an instance drains it gracefully.

The endpoints are `/admission?json` and `/metrics?json`, not `/jsonmetrics`, because the latter
page is broken in the published images. With TLS enabled every Impala web
server serves HTTPS (the certificate flag is global, and the metrics-only
`-metrics_webserver_port` would not serve `/admission` anyway), so the
autoscaler scrapes the main web port over HTTPS, addressing each coordinator
by its pod DNS name and verifying it against `ca.crt` (or `tls.crt`) from the
referenced TLS Secret. The operator does not enable SPNEGO or LDAP on the web
server, so Kerberos and LDAP clusters are scraped like plain ones.

Both controllers patch the same `status`, and a JSON merge patch replaces the
`status.executorGroups` list wholesale, so both use optimistic locking
(`MergeFromWithOptimisticLock`). Whoever loses the race drops its patch and
requeues after a second; the autoscaler defers its timer resets and events
until its patch has landed, so a lost race costs one poll interval, not a full
`scaleUpDelaySeconds`.

A freshly added group reports no executing queries until it registers, which
would look idle. The idle timer therefore only runs once the main reconciler
has marked the highest-index instance `healthy` in status.

## Ports

| Daemon | RPC | Web |
|---|---|---|
| statestore | 24000 | 25010 |
| catalog | 26000 | 25020 |
| impalad (coordinator/executor) | KRPC 27000, subscriber 23000, HS2 21050, HS2-HTTP 28000 | 25000 |

## Testing

- **Unit** (`internal/resources`, `internal/impala`): builders are pure
  functions, tested by asserting on the objects and rendered XML they return;
  the metrics client is tested against an `httptest` server. ~90% coverage.
- **envtest** (`internal/controller`): runs the reconciler against a real API
  server (no kubelet), asserting that the expected objects are created, that a
  config change re-hashes the pod template, that scaled-away groups are pruned,
  and that a missing Secret sets `Degraded`.
- **e2e** (`test/e2e`, build tag `e2e`): a kind cluster with MinIO and a
  quickstart Hive Metastore. It deploys the operator, applies a sample
  cluster, and verifies readiness, `select`/`create`/`insert` on S3, graceful
  drain of a running query when its executor pod is deleted, a full autoscale
  cycle on a family that starts at zero groups (cold start on "Waiting for
  executors to start", a second group on "Not enough admission control
  slots", then removal of both once idle), and that a queue caused by the
  pool running-query cap does *not* add a group. Because Ginkgo shuffles
  top-level suites, the Impala suite deploys the operator itself (with a
  rollout restart, since the image tag is fixed) and undeploys afterward.

## Build and packaging

Standard kubebuilder Makefile targets: `make manifests generate` regenerates
the CRD and deepcopy code, `make test` runs unit and envtest, `make lint` runs
golangci-lint, `make test-e2e` runs the kind suite, and `make api-docs`
regenerates [api.md](api.md). A Helm chart is generated into `dist/chart`
(kubebuilder `helm/v2-alpha` plugin) and GitHub Actions runs lint, unit and
e2e on every push.

## Known constraints

- **Default image version is 4.5.2.** The `apache/impala:4.5.2-*` images ship
  a conflicting `hadoop-client-api` jar that crashes catalogd and impalad at
  startup; see the implementation notes in [base-design.md](base-design.md).
- **Catalog and statestore HA is active/standby only** (one or two replicas);
  there is no quorum. Statestore HA and the standalone `admissiond` are out of
  scope for v1.
- **TLS, Kerberos and LDAP are unit-tested only**; there is no CA, KDC or
  LDAP e2e fixture.
- **`executorGroups[].config.scratch` and `.dataCache` are immutable** once
  the family exists, enforced by CEL, because they become StatefulSet
  `volumeClaimTemplates`, which Kubernetes does not allow to change. To
  switch between emptyDir and PVC, add a new family and remove the old one.
