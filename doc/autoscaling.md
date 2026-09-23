# Executor group autoscaling

Impala schedules every query on exactly one *executor group*. A group is a
fixed set of executors registered with the same `-executor_groups` name, and
it only accepts work once at least `minHealthySize` executors are up. Because
groups are fixed-size and independent, the natural way to add capacity is to
add whole groups rather than grow one group.

The operator models this with **executor group families**
(`spec.executorGroups[]`). A family describes the size and configuration of a
group; `groups` says how many identical instances exist. Every instance is a
StatefulSet named `<cluster>-exec-<family>-<index>` and registers with Impala
as `<pool>-<family>-<index>` (for example `root.default-small-0`). Impala
requires the pool prefix: it routes queries of pool `root.default` only to
groups whose name starts with `root.default-`.

## How scaling decisions are made

When `autoscaling.enabled` is true the operator polls every ready coordinator
every 15 seconds. It reads `/admission?json` for the scale-up decision and
`/metrics?json` for the scale-down decision, then evaluates each family:

| Signal | Source | Action |
|---|---|---|
| A query is queued in the family's pool **for a reason more executors can relieve**, held for `scaleUpDelaySeconds` | `/admission?json` `resource_pools[].{agg_num_queued, head_queued_reason}` | add one group (up to `maxGroups`) |
| Newest group idle | `/metrics?json` `admission-controller.executor-group.num-queries-executing.<group>` == 0 across all coordinators for `scaleDownDelaySeconds` | remove the highest-index group (down to `minGroups`) |

A query queues for several reasons, and only some are relieved by more groups.
The operator scales up only when the head-of-queue reason
(`head_queued_reason`) is one of:

* `Not enough admission control slots available on host ...` — the groups are at slot capacity;
* `Not enough memory available on host ...` — per-host memory is exhausted;
* `Waiting for executors to start ...` — no healthy group yet.

It ignores queues caused by the pool's `max-running-queries` cap, the pool's
aggregate memory limit, or a full queue, because adding a group cannot drain
those — it would only add an idle group that is then scaled back down. The
queue-reason strings come from `be/src/scheduling/admission-controller.cc`.

`cooldownSeconds` is the minimum gap between two actions. Decisions are
recorded in `status.executorGroups[].desiredGroups` and `lastScaleTime`, and a
`ScaledUp` / `ScaledDown` event is emitted on the ImpalaCluster. The main
reconciler then creates or deletes the StatefulSet. Removing a group drains
its executors gracefully (see below), so running queries finish first.

`spec.executorGroups[].groups` is only the starting point while autoscaling is
enabled; the operator never edits the spec.

## Pools and placement

`clusterConfig.admissionControl.pools` renders `fair-scheduler.xml` and
`llama-site.xml`. The generated placement policy sends queries that set
`REQUEST_POOL` to that pool (unknown names are rejected) and everything else
to `admissionControl.defaultPool` (the first pool when unset). Without this
policy the fair scheduler would create a `root.<username>` pool per user that
no executor group serves, and queries would queue forever "waiting for
executors to start".

## Sizing guidance

* Set `pools[].maxRunningQueries` so that one group is saturated before
  queries queue. Queueing is the only scale-up signal.
* `scaleUpDelaySeconds` should be shorter than `pools[].queueTimeoutMs` plus
  the time a new group needs to start, otherwise queued queries time out
  before capacity arrives.
* `scaleDownDelaySeconds` should exceed your typical gap between queries so
  the cluster does not oscillate.
* With TLS enabled the operator scrapes the coordinators over HTTPS by pod
  DNS name, verifying against `ca.crt` (or `tls.crt`) in the TLS Secret, so
  the certificate must cover
  `*.<cluster>-coordinator-hl.<namespace>.svc.cluster.local`.
* A newly added group only starts accumulating idle time once it is healthy,
  so `scaleDownDelaySeconds` does not need to include the group's boot time.

## Graceful shutdown

`impalad` treats `SIGTERM` as an immediate kill, so every coordinator and
executor pod gets a `preStop` hook that sends `SIGRTMIN` and waits for the
process to exit. Impala then:

1. marks the backend as quiescing so coordinators stop scheduling on it,
2. waits `gracefulShutdownGracePeriodSeconds` for in-flight scheduling,
3. exits when no fragments are running or `gracefulShutdownDeadlineSeconds`
   has elapsed.

The pod's `terminationGracePeriodSeconds` is derived as
`grace + deadline + 60`. Scale-downs, rolling updates and node drains all go
through this path.
