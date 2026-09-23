# impala-operator

A Kubernetes operator for [Apache Impala](https://impala.apache.org/) 4.x.
It manages the full Impala process layout used in cloud deployments:

* one `statestored`, one or two `catalogd` (active/standby HA),
* dedicated coordinator `impalad`s behind a client Service,
* any number of **executor group** families, each a set of identically sized
  executor groups that can be scaled by hand or by the built-in autoscaler.

The operator expects an external Hive Metastore and S3-compatible object
storage; it does not run either.

Documentation:

* [doc/api.md](doc/api.md): ImpalaCluster field reference
* [doc/autoscaling.md](doc/autoscaling.md): executor groups, autoscaling and graceful shutdown
* [doc/security.md](doc/security.md): TLS, Kerberos, LDAP and credentials
* [doc/implementation.md](doc/implementation.md): how the operator is built (packages, controllers, control flow)
* [doc/base-design.md](doc/base-design.md): design decisions and the upstream facts behind them

## Quick start

Requirements: a Kubernetes 1.30+ cluster, `kubectl`, Docker, and a Hive
Metastore reachable from the cluster. No prebuilt operator image is published
yet, so build one and push it to a registry your cluster can pull from (for
a kind cluster, `kind load docker-image` instead of pushing).

```sh
# Build the operator image, install the CRD and deploy the operator
make docker-build IMG=<registry>/impala-operator:dev
docker push <registry>/impala-operator:dev
make install deploy IMG=<registry>/impala-operator:dev

# Create an S3 credentials Secret and a cluster
kubectl create secret generic impala-s3-credentials \
  --from-literal=AWS_ACCESS_KEY_ID=... --from-literal=AWS_SECRET_ACCESS_KEY=...
kubectl apply -f config/samples/impala_v1alpha1_impalacluster.yaml

kubectl get impalaclusters
kubectl get pods -l app.kubernetes.io/instance=impalacluster-sample
```

Connect with any HiveServer2 client on port 21050 of the
`<name>-coordinator` Service, or with `impala-shell`:

```sh
kubectl run -it --rm shell --image=apache/impala:4.5.2-impala_quickstart_client \
  --restart=Never -- impala-shell -i impalacluster-sample-coordinator:21050
```

## The ImpalaCluster resource

```yaml
apiVersion: impala.operator.dev/v1alpha1
kind: ImpalaCluster
metadata:
  name: demo
spec:
  image: {repository: apache/impala, version: 4.5.2}
  clusterConfig:
    hiveMetastore: {uris: thrift://hms:9083}
    storage:
      s3:
        endpoint: http://minio:9000        # omit for AWS
        pathStyleAccess: true
        credentialsSecretRef: {name: impala-s3-credentials}   # omit for IAM roles
    admissionControl:
      defaultPool: default                # pool for queries that do not set REQUEST_POOL
      pools:
        - {name: default, maxRunningQueries: 10, maxQueuedQueries: 50, maxQueryMemLimit: 4gb}
    security:
      tls: {enabled: false, certSecretRef: {name: impala-tls}}
      kerberos: {enabled: false}
      ldap: {enabled: false}
  catalog:
    replicas: 1                             # 2 enables catalogd HA
  coordinators:
    replicas: 2
    config: {memLimit: 8gb, jvmHeap: 2g, gracefulShutdownDeadlineSeconds: 900}
  executorGroups:
    - name: small
      pool: default
      size: 3                               # executors per group
      groups: 1                             # group instances (ignored when autoscaling)
      autoscaling:
        enabled: true
        minGroups: 1
        maxGroups: 5
        scaleUpDelaySeconds: 30
        scaleDownDelaySeconds: 300
      config:
        memLimit: 16gb
        scratch: {size: 50Gi}
        dataCache: {size: 100Gi}
```

Every component accepts `args`, `env`, `resources` and `podOverrides`
(labels, annotations, nodeSelector, tolerations, affinity, volumes, sidecars,
init containers).

### What the operator does

* Renders `hive-site.xml`, `core-site.xml`, `fair-scheduler.xml` and
  `llama-site.xml` into a ConfigMap. Changing the spec or a referenced Secret
  rolls the affected pods.
* Rolls out changes in dependency order: statestore, catalog, coordinators,
  executor groups. A later tier is not touched until the previous one is ready.
* Drains `impalad`s gracefully on scale-down and rolling restarts: the preStop
  hook sends `SIGRTMIN` and waits for running queries to finish, bounded by
  `gracefulShutdownDeadlineSeconds`.
* Creates PodDisruptionBudgets for coordinators and each executor group.
* Scales executor group families up when queries queue in the group's pool
  and down when the newest group has been idle, writing its decisions to
  `status.executorGroups[].desiredGroups`.

### Status

```
kubectl get impalacluster demo -o yaml
status:
  conditions: [Ready, Progressing, Degraded]
  coordinators: {replicas: 2, readyReplicas: 2}
  executorGroups:
    - name: small
      desiredGroups: 2
      groups:
        - {name: root.default-small-0, replicas: 3, readyReplicas: 3, healthy: true}
  endpoints:
    hiveServer2: demo-coordinator.default.svc:21050
```

## Development

```sh
hack/install-tools.sh        # Go, kubebuilder, kind, kubectl, helm into ~/.local
make test                    # unit + envtest
make lint
make test-e2e                # kind cluster with MinIO, HMS and a real Impala cluster
make api-docs                # regenerate doc/api.md
helm install impala-operator dist/chart   # or install via the generated chart
```

## License

Apache License 2.0.
