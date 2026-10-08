# Security configuration

All security settings live under `spec.clusterConfig.security` and apply to
every daemon in the cluster.

## TLS

```yaml
security:
  tls:
    enabled: true
    certSecretRef: {name: impala-tls}   # kubernetes.io/tls: tls.crt, tls.key, ca.crt
    internal: true                      # also encrypt daemon-to-daemon traffic
    minimumVersion: tlsv1.2
```

* Client connections (HiveServer2, HS2-over-HTTP) and every daemon's web UI
  are served with the certificate.
* With `internal: true` the certificate and CA are also used for Thrift and
  KRPC traffic between daemons (`-ssl_client_ca_certificate`).
* Daemons register with the statestore under their pod DNS name
  `<pod>.<cluster>-<component>-hl.<namespace>.svc.cluster.local`, so the
  certificate must cover those names, typically with wildcard SANs such as
  `*.demo-coordinator-hl.default.svc.cluster.local` and
  `*.demo-exec-small-hl.default.svc.cluster.local`. cert-manager can issue
  such certificates from an internal CA.
* Rotating the Secret rolls the pods automatically.
* Readiness and liveness probes switch to HTTPS.

## Kerberos

```yaml
security:
  kerberos:
    enabled: true
    principal: impala/_HOST@EXAMPLE.COM
    keytabSecretRef: {name: impala-keytab}     # key: impala.keytab
    krb5ConfigMapRef: {name: krb5-conf}        # key: krb5.conf
```

`_HOST` is replaced by the pod DNS name, so the keytab must contain a
principal for every pod (`impala/demo-coordinator-0.demo-coordinator-hl...`).
`hadoop.security.authentication=kerberos` is added to `core-site.xml`, so the
Hive Metastore and object store must accept Kerberos as well, or override
those properties through `configOverrides`.

## LDAP

```yaml
security:
  ldap:
    enabled: true
    uri: ldaps://ldap.example.com
    bindPattern: "uid=#UID,ou=people,dc=example,dc=com"
    caCertSecretRef: {name: ldap-ca}           # key: ca.crt
```

LDAP password authentication applies to coordinators only. Impala refuses
LDAP over plaintext unless `allowPasswordsInClear: true`, so enable TLS first.

## Object store credentials

`storage.s3.credentialsSecretRef` points at a Secret with
`AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`; they are injected as
environment variables and never written to a ConfigMap. Omit the reference to
use the instance profile or IRSA of the pod's service account
(`podOverrides.serviceAccountName`).

## Network isolation

Without Kerberos, Impala's internal ports (statestore 24000, catalog 26000,
KRPC 27000, statestore subscriber 23000) accept any peer: any pod that can
reach them can register as an executor and receive query data. `tls.internal`
verifies servers, not clients. Enable the generated NetworkPolicy to close
this:

```yaml
security:
  networkPolicy:
    enabled: true
    clientFrom:                 # who may reach HiveServer2 (empty = anyone)
      - namespaceSelector: {matchLabels: {team: analytics}}
    webUIFrom:                  # extra peers for the daemon web UI ports
      - namespaceSelector: {matchLabels: {kubernetes.io/metadata.name: monitoring}}
```

The policy selects every pod of the cluster and admits: all ports from the
cluster's own pods; HiveServer2 (21050, 28000) from `clientFrom`; and the web
UI ports (25000, 25010, 25020) from the operator's namespace (the autoscaler
scrapes coordinators) plus `webUIFrom`. Egress is not restricted. It only
takes effect on CNIs that enforce NetworkPolicy. The operator learns its own
namespace from `POD_NAMESPACE` (set by the manifests and chart) or the
mounted ServiceAccount token; if neither is available, clusters with
`networkPolicy.enabled` are marked `Degraded` instead of silently losing
autoscaling.

## Debug web UI

Impala's web UI (port 25000 on impalads, 25010 on the statestore, 25020 on
catalogd) has no authentication: it shows query text and profiles, sessions,
logs and flags, and can cancel queries and change log levels. The operator
therefore keeps it off the client Services, which may be of type
`LoadBalancer` or `NodePort`, and exposes it only through the headless
Services (`status.endpoints.webUI`). Reach it with
`kubectl port-forward pod/<coordinator-pod> 25000` or restrict it further with
`networkPolicy.webUIFrom`.

## Pod hardening

Every Impala pod runs as uid/gid 1000 with `runAsNonRoot`, a `RuntimeDefault`
seccomp profile, all capabilities dropped and privilege escalation disabled,
which satisfies the `restricted` Pod Security Standard as long as
`podOverrides` does not relax it.

Impala executes user SQL and native UDFs, so the daemons are a code-execution
surface by design. The ServiceAccount token is therefore not mounted into
them (`automountServiceAccountToken: false`). Setting
`podOverrides.serviceAccountName` mounts it again, on the assumption that a
dedicated ServiceAccount exists to be used (cloud IAM bindings, Vault agents);
`podOverrides.automountServiceAccountToken` overrides either default.

## Iceberg REST catalog credentials

`clusterConfig.icebergRestCatalogs[].oauth2.credentialSecretRef` names a
Secret key holding the OAuth2 client credential (`<client-id>:<client-secret>`).
The operator injects it into the coordinator container as an environment
variable and references that variable from the catalog's properties file
with Impala's `${ENV:...}` substitution, so the credential is never written
to the ConfigMap. Executors do not receive it. Rotating the Secret rolls the
coordinators. See [catalogs.md](catalogs.md).
