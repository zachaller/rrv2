# Rollouts

A Kubernetes controller for progressive delivery — a fork of the
Kubernetes Deployment controller that adds canary, blue-green, analysis
gates, and traffic shaping via Istio (with stubs for Nginx, ALB, SMI,
Traefik, and APISIX).

## Status

Canary + Istio + Prometheus is the complete vertical in this build. Other
routers and analysis providers are stubbed — see
[`docs/operations.md`](docs/operations.md) for the known gaps.

## Quickstart

See [`docs/quickstart.md`](docs/quickstart.md) for a step-by-step walk
through deploying the controller to kind and running a canary rollout
against Istio with a Prometheus analysis gate.

## Documentation

- [Quickstart](docs/quickstart.md)
- [Architecture](docs/architecture.md)
- [Spec reference](docs/spec-reference.md)
- [Analysis and providers](docs/analysis.md)
- [Istio integration](docs/routers/istio.md)
- [Operations](docs/operations.md)

## Build and test

```bash
make build       # bin/rollouts-controller
make test        # unit tests
make manifests   # regenerate CRD YAML from Go annotations
make generate    # regenerate deepcopy methods
```

## Provenance

Portions of `pkg/controller/rollout/_forkedfrom_k8s/` are derived from
`kubernetes/kubernetes` at tag `v1.31.9`, Apache-2.0 licensed. See
`NOTICE`.
