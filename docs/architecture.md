# Architecture

The Rollouts controller is a fork of the Kubernetes Deployment controller
with a progressive-delivery executor on top. This document walks the code
path a reconcile takes, and explains why each boundary exists.

## Top-level shape

```
 ┌─────────────────────────────────────────────────────────────────────┐
 │ cmd/rollouts-controller  (controller-runtime Manager, leader elect) │
 └──────┬──────────────────┬──────────────────┬─────────────────────────┘
        │                  │                  │
        ▼                  ▼                  ▼
 ┌──────────────┐   ┌──────────────┐   ┌──────────────────┐
 │ Rollout      │   │ AnalysisRun  │   │ Experiment       │
 │ reconciler   │   │ reconciler   │   │ reconciler       │
 └──────┬───────┘   └──────┬───────┘   └──────────────────┘
        │                  │
        │                  └──► providers.Prometheus (HTTP client)
        │
        ├──► forkeddeployment.GetAllReplicaSetsAndSyncRevision
        │     (replica-set creation, revision sync, scaling math)
        │
        ├──► Executor.Execute  (progression.go)
        │     ├─ SetWeight       → trafficrouting.Plugin.SetWeight
        │     ├─ SetCanaryScale  → forkeddeployment.ScaleReplicaSet
        │     ├─ Pause           → status.PauseConditions
        │     ├─ Analysis        → create owning AnalysisRun, gate on Phase
        │     ├─ Promote         → Service selector flip + scaleDownDelay
        │     └─ Experiment      → (stub)
        │
        └──► trafficrouting.Plugin  (istio, nginx, alb, smi, traefik, apisix)
```

## The synthetic Deployment view

The forked code takes `*appsv1.Deployment` arguments because it was lifted
verbatim from `k8s.io/kubernetes/pkg/controller/deployment`. The Rollout CR
carries a very similar shape under the hood (replicas, selector, pod
template, strategy knobs), so rather than re-implement revision-sync,
pod-template-hashing, and RS creation, the reconciler builds a synthetic
`Deployment` view of the Rollout (`adapter.go::asDeploymentView`) and hands
it to the forked helpers.

Two invariants make this safe:

1. **Reads are authoritative; writes are discarded.** The forked helpers
   read from the view (what replicas to target, what template to hash).
   When they would update the Deployment's status, we don't forward the
   update — the Rollout has its own status shape.
2. **Ownership is rewritten.** Newly created ReplicaSets are owned by the
   synthetic Deployment; we rewrite the ownerReference to point at the real
   Rollout in `ensureOwnedByRollout`. This is idempotent.

## The progression Executor

The flat `progression.steps` model (see the design plan in
`~/.claude/plans/joyful-jumping-karp.md`) is interpreted by a small
switch in `progression.go::Executor.Execute`. Each step type has its own
handler in `steps.go`. Handlers share a contract:

- Returning `(0, nil)` advances to the next step.
- Returning `(duration, nil)` requeues after the given duration.
- Returning `(_, err)` surfaces the error to the reconciler, which logs and
  rate-limits.

Status transitions are owned by the Executor. The outer reconciler only
updates replica counts and handles abort.

## Named steps

Rollout steps are referenced by name, not by index, both in status
(`status.currentStep`) and in analysis hooks (`analysis[].atStep`). This
means:

- Inserting a step before the current one doesn't make the controller jump
  forward — it re-resolves `status.currentStep` on the new step list.
- Renaming a step that the status field points at moves the controller
  past that step rather than repeating work.
- Analysis hooks referencing a step name that no longer exists are
  rejected by CRD admission validation (CEL rule).

See `progression.go::resolveCurrentStep` for the resolver.

## AnalysisRun lifecycle

`runAnalysis` creates an AnalysisRun named deterministically from the
(rollout, step, pod-hash) tuple. If the AR already exists (common — most
reconciles are mid-poll), it reuses it. The Rollout blocks on
`ar.status.phase`; the AnalysisRun reconciler drives that phase.

The AR reconciler itself is a tick loop: each metric has an Interval, and
each sweep samples only metrics whose last measurement is older than their
Interval. Samples are evaluated against SuccessCondition / FailureCondition
via the expression evaluator in `evaluator.go`.

Terminal per-metric phases (Successful, Failed, Error, Inconclusive) are
aggregated into a single run phase by `aggregatePhase`, honoring the
DryRun list — a failing DryRun metric doesn't fail the run.

## The traffic-routing plugin interface

Routers implement `trafficrouting.Plugin` (`pkg/trafficrouting/plugin.go`).
The Rollout executor only calls four methods: `SetWeight`, `SetHeaderRoute`,
`VerifyWeight`, and `RemoveManagedRoutes`. Each provider package registers
itself via `init()` so adding a new router is a new subpackage plus one
line in the main binary's import block.

The Istio provider is the fully-implemented reference. See
[routers/istio.md](routers/istio.md) for how it patches VirtualServices.

## Where the forked fork lives

`pkg/controller/rollout/_forkedfrom_k8s/` is byte-identical to
`k8s.io/kubernetes/pkg/controller/deployment@v1.31.9` modulo a single
mechanical package rename (`deployment` → `forkeddeployment`). The
directory prefix `_` keeps it out of `go build ./...` wildcards so tests
don't accidentally pick up the upstream test files we deleted during
import.

A sibling file `headless.go` (not part of the upstream import) exposes a
constructor (`NewHeadless`) that builds a `DeploymentController` without
informers/workqueue/event-broadcaster — just a clientset, event recorder,
and a lister. The Rollout reconciler uses that constructor to drive the
forked primitives directly.

To pick up a newer upstream deployment controller:

```
./hack/import-k8s-deployment.sh v1.32.0
```

This runs `git read-tree --prefix=...`, rewriting the subtree with a
single commit that preserves upstream authorship for `git blame`.
