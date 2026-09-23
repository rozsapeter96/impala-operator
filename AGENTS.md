# Working on impala-operator

A kubebuilder (go/v4) project. Read [doc/implementation.md](doc/implementation.md)
before changing controller or builder code.

## Layout

```
cmd/main.go                        Manager entry point
api/v1alpha1/impalacluster_types.go CRD types, validation markers, CEL rules
api/v1alpha1/zz_generated.*        Generated (do not edit)
internal/resources/                Pure builders: spec -> Kubernetes objects
internal/controller/               Reconciler and autoscaler
internal/impala/                   HTTP client for the Impala debug web server
config/                            Kustomize manifests; crd/ and rbac/role.yaml are generated
dist/chart/                        Helm chart; its CRD and RBAC mirror config/
doc/                               Documentation; api.md is generated
test/e2e/                          Kind-based end-to-end suite
```

## Rules

- Never hand-edit generated files: `config/crd/bases/*`, `config/rbac/role.yaml`,
  `**/zz_generated.*`, `doc/api.md`, `PROJECT`. After changing types or
  markers run `make manifests generate api-docs`, then copy the CRD into
  `dist/chart/templates/crd/` (with the Helm wrapper) and the RBAC rules into
  `dist/chart/templates/rbac/manager-role.yaml`, and run `make build-installer`.
- Keep `// +kubebuilder:scaffold:*` markers.
- CEL rules must guard optional fields with `has()`; reading an unset field
  is an evaluation error, not `false`.
- Both controllers patch `status` with optimistic locking. Keep it that way:
  a merge patch replaces `status.executorGroups` wholesale.
- Builders in `internal/resources` must stay pure (no cluster access) so they
  are unit-testable.

## Verify

```
make lint          # golangci-lint
make test          # unit + envtest
make test-e2e      # kind cluster with MinIO, HMS and a real Impala cluster
```

The e2e suite needs a dedicated kind cluster; it builds and loads the manager
image itself. Set `IMPALA_E2E_KEEP=true` to keep the namespace for inspection.
