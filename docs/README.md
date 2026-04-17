# Rollouts documentation

- [Quickstart](quickstart.md) — deploy the controller and run a canary rollout end-to-end against Istio with a Prometheus gate.
- [Architecture](architecture.md) — how the controller is put together and why.
- [Rollout spec reference](spec-reference.md) — every field on every CRD, with defaults and semantics.
- [Istio integration](routers/istio.md) — how the VirtualService is patched and what it requires.
- [Analysis and metric providers](analysis.md) — writing AnalysisTemplates, the expression language, and the Prometheus provider.
- [Operations](operations.md) — deploy topology, RBAC, observability, abort and rollback.
