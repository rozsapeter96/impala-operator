# Metadata catalogs

Impala reads table metadata from one or both of two sources, and the
operator configures either from `spec.clusterConfig`:

* a **Hive Metastore** (`hiveMetastore`), served to the coordinators through
  `catalogd`;
* one or more **Iceberg REST catalogs** (`icebergRestCatalogs`), such as
  Apache Polaris, Lakekeeper or Gravitino, read by the coordinators directly.

REST catalog support needs an Impala build that includes IMPALA-13586 and
its follow-ups (Impala `master`; not in the published 4.5.x images). Build
the daemon images from `master` and point `spec.image` at them. The e2e
suite exercises this against Apache Polaris with such images
(`test/e2e/fixtures/polaris.yaml` and `impalacluster-rest.yaml`).

## Deployment modes

| `hiveMetastore` | `icebergRestCatalogs` | Result |
|---|---|---|
| set | empty | Classic: catalogd + HMS (the default) |
| set | one or more | Hybrid: catalogd + HMS, plus every REST catalog |
| unset | one or more | Standalone: no catalogd, REST catalogs only |
| unset | empty | Rejected by CRD validation |

In every mode the coordinators run with `-use_local_catalog=true`. With REST
catalogs they also get `-catalog_config_dir=/opt/impala/catalogs`, which
holds one properties file per catalog. In standalone mode the operator does
not render the catalog StatefulSet or Services, passes
`-catalogd_deployed=false` to every impalad, and leaves `hive-site.xml`
without a metastore. Adding or removing `hiveMetastore` on a running cluster
switches modes: the catalog StatefulSet is created or deleted and the
coordinators roll.

Tables are addressed as `database.table` regardless of which catalog holds
them. A REST catalog namespace maps to a database. A name that exists in more
than one catalog is rejected as ambiguous.

## Apache Polaris

```yaml
apiVersion: impala.operator.dev/v1alpha1
kind: ImpalaCluster
metadata:
  name: lake
spec:
  image: {repository: registry.example.com/impala, version: master}
  clusterConfig:
    # no hiveMetastore: standalone mode
    icebergRestCatalogs:
      - name: polaris
        uri: http://polaris.polaris.svc:8181/api/catalog
        warehouse: lake                      # the Polaris catalog name
        oauth2:
          credentialSecretRef: {name: polaris-impala}   # key "credential"
          scope: PRINCIPAL_ROLE:ALL
        properties:
          io-impl: org.apache.iceberg.hadoop.HadoopFileIO
    storage:
      s3:
        endpoint: http://minio:9000
        pathStyleAccess: true
        credentialsSecretRef: {name: impala-s3-credentials}
  coordinators: {replicas: 1}
  executorGroups:
    - {name: small, size: 2}
```

The credential Secret holds the Polaris principal's client id and secret in
Iceberg's `<client-id>:<client-secret>` form:

```sh
kubectl create secret generic polaris-impala \
  --from-literal=credential=<client-id>:<client-secret>
```

Polaris issues tokens from its own `/v1/oauth/tokens` endpoint, which is
what the operator renders when `oauth2.serverURI` is empty. Point it at an
external identity provider's token endpoint when Polaris is configured to
delegate authentication.

The principal needs a principal role that is granted a catalog role with at
least `TABLE_READ_DATA` on the catalog. For `INSERT INTO`, add
`TABLE_WRITE_DATA`.

### Reading table data

Impala reads data files with the cluster's own storage configuration from
`core-site.xml` (`storage.s3`), so the operator sets nothing catalog-specific
for data access by default. Two things can get in the way:

* **FileIO.** Polaris tells clients to use `S3FileIO`, which bypasses
  `core-site.xml` and expects AWS-style configuration. Setting
  `properties: {io-impl: org.apache.iceberg.hadoop.HadoopFileIO}` keeps
  metadata reads on the Hadoop S3A client that `storage.s3` configures.
  Alternatively supply `S3FileIO` settings (`s3.endpoint`,
  `s3.path-style-access`, `s3.access-key-id`, `s3.secret-access-key`)
  through `properties`, using `${ENV:...}` references for the secrets.
* **Vended credentials.** With `vendedCredentials: true` Impala asks the
  catalog for per-table storage credentials on every table load instead of
  using the cluster's credentials. Leave it off unless the catalog is set up
  for credential vending and the executors can use the vended credentials.

## Other REST catalogs

Any server implementing the Iceberg REST specification works. Differences
are in authentication and the meaning of `warehouse`:

* **Lakekeeper**: `warehouse` is the Lakekeeper warehouse name; OAuth2 via
  an external IdP, so set `oauth2.serverURI` to the IdP's token endpoint.
* **Gravitino, Unity Catalog, Tabular**: consult the server's documentation
  for `uri`, `warehouse` and the token endpoint; pass anything else through
  `properties`.
* **Unauthenticated servers** (development only): omit `oauth2`.

## How the configuration is rendered

Each catalog becomes a key `rest-catalog-<name>.properties` in the cluster's
ConfigMap, projected alone into `/opt/impala/catalogs/<name>.properties` on
the coordinators. The file uses the Trino-compatible key names Impala
documents:

```properties
connector.name=iceberg
iceberg.catalog.type=rest
iceberg.rest-catalog.name=polaris
iceberg.rest-catalog.oauth2.credential=${ENV:IMPALA_REST_CATALOG_POLARIS_CREDENTIAL}
iceberg.rest-catalog.oauth2.scope=PRINCIPAL_ROLE:ALL
iceberg.rest-catalog.oauth2.server-uri=http://polaris.polaris.svc:8181/api/catalog/v1/oauth/tokens
iceberg.rest-catalog.security=OAUTH2
iceberg.rest-catalog.uri=http://polaris.polaris.svc:8181/api/catalog
iceberg.rest-catalog.warehouse=lake
io-impl=org.apache.iceberg.hadoop.HadoopFileIO
```

The OAuth2 credential never enters the ConfigMap. It is injected into the
coordinator container as the environment variable named in the file, from
the referenced Secret, and Impala substitutes it at startup. Rotating the
Secret rolls the coordinators, and a missing Secret marks the cluster
`Degraded`. Executors receive neither the properties files nor the
credential; they do not plan queries.

`properties` entries are written last and override the generated ones, so
any Iceberg `RESTCatalog` or Impala-recognised key can be set or replaced.

## Limitations (upstream)

REST catalog support in Impala `master` is read-mostly:

* `SELECT` and `INSERT INTO` work; `INSERT OVERWRITE`, `UPDATE`, `DELETE`,
  `MERGE`, `OPTIMIZE` and `TRUNCATE` do not.
* DDL (`CREATE`, `ALTER`, `DROP`) is not supported through a REST catalog.
* Iceberg views are not supported.
* Only flat namespaces (no nested namespaces) and `NONE` sessions.

`INSERT INTO` is routed by `iceberg.rest-catalog.name`, which the operator
sets from `name`, so every catalog in a cluster keeps a distinct name.
