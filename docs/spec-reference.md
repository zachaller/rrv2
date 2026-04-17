# Rollout spec reference

This page describes every field on the `Rollout` CRD. The generated CRD
YAML in `config/crd/bases/rollouts.io_rollouts.yaml` is authoritative for
validation rules; this document explains semantics.

## Top level

| Field | Type | Default | Description |
| ----- | ---- | ------- | ----------- |
| `spec.replicas` | `*int32` | `1` | Desired pod count across all owned ReplicaSets. |
| `spec.selector` | `*LabelSelector` | *required* | Pod selector. Must match `template.metadata.labels`. |
| `spec.template` | `*PodTemplateSpec` | — | Inline pod template. Exactly one of `template` or `workloadRef` must be set. |
| `spec.workloadRef` | `*WorkloadRef` | — | Reference an external Deployment/ReplicaSet/StatefulSet instead of an inline template. |
| `spec.stableServices` | `[]ServiceRef` | — | Services carrying production traffic. Their selectors are flipped at Promote. |
| `spec.canaryServices` | `[]ServiceRef` | — | Services exposing the progressing ReplicaSet. |
| `spec.progression` | `ProgressionSpec` | *required* | Step-based execution plan. |
| `spec.trafficRouting` | `*TrafficRoutingSpec` | — | Router provider and its configuration. |
| `spec.autoPromotion` | enum | `Enabled` | `Enabled` / `Manual` / `Disabled`. Affects Promote behavior. |
| `spec.dynamicStableScale` | enum | `Off` | `Off` / `Proportional` / `Aggressive`. Controls whether stable scales down as canary scales up. |
| `spec.analysis` | `[]AnalysisHook` | — | Analysis hooks — `preStep`, `postStep`, `background`, `prePromotion`, `postPromotion`. |
| `spec.ephemeralMetadata` | `[]EphemeralMetadata` | — | Labels/annotations applied to pods while they hold a role. |
| `spec.restartAt` | `*metav1.Time` | — | Force a rolling pod restart no later than this time. |
| `spec.rollbackWindow` | `*RollbackWindow` | — | Bounds auto-rollback targets. |
| `spec.progressDeadlineSeconds` | `*int32` | `600` | Per-step stall budget. |
| `spec.revisionHistoryLimit` | `*int32` | `10` | Retained prior ReplicaSets. |
| `spec.minReadySeconds` | `int32` | `0` | Mirrors Deployment semantics. |
| `spec.paused` | `bool` | `false` | Stops reconciliation of this Rollout. |
| `spec.abort` | `bool` | `false` | Aborts the in-flight rollout. |

## `ProgressionSpec`

| Field | Type | Description |
| ----- | ---- | ----------- |
| `steps` | `[]Step` | Ordered step plan. Every step has a unique `name`. |
| `maxUnavailable` | `*IntOrString` | Used when no router is configured (replica-weighted canary). Defaults to 25%. |
| `maxSurge` | `*IntOrString` | Mirrors Deployment.Spec.Strategy.RollingUpdate.MaxSurge. Defaults to 25%. |
| `scaleDownDelaySeconds` | `*int32` | Delay between Promote and old-RS scale-down. Default 30s. |

## `Step`

Exactly one of the six action sub-fields must be populated; the CRD's CEL
rule enforces this.

| Action | Purpose |
| ------ | ------- |
| `setWeight` | Shift router traffic from stableServices to canaryServices. Fields: `weight` (0-100), `matches` (header/cookie/query rules). |
| `setCanaryScale` | Adjust canary RS replica count independent of traffic. Fields: `replicas` / `percent` / `matchTrafficWeight`. |
| `pause` | Halt progression. `duration` nil means indefinite. |
| `analysis` | Create owning AnalysisRun and gate on its Phase. Fields: `templateRefs`, `args`, `dryRun`, `failurePolicy`. |
| `experiment` | Run a side-by-side experiment. |
| `promote` | Flip the router to 100% canary and repoint stableServices at the new RS. |

Names must match `^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`.

## `TrafficRoutingSpec`

`provider` is a discriminator: exactly one of the provider-specific config
blocks (`istio`, `nginx`, `alb`, `smi`, `traefik`, `apisix`) must be
populated, and it must match `provider`.

### `IstioConfig`

| Field | Description |
| ----- | ----------- |
| `virtualServices[].name` | VirtualService to patch. |
| `virtualServices[].routes` | Named HTTP routes to rewrite. Empty list = all. |
| `virtualServices[].tlsRoutes`, `tcpRoutes` | Non-HTTP equivalents. |
| `destinationRules` | Optional; reserved for subset switching. |

See [routers/istio.md](routers/istio.md) for the patch semantics.

## `AnalysisHook`

Rollout-scoped analysis, as opposed to step-scoped (`Step.analysis`).

| Field | Description |
| ----- | ----------- |
| `when` | `preStep` / `postStep` / `background` / `prePromotion` / `postPromotion`. |
| `atStep` | Required for `preStep`/`postStep`. Must reference a step by name. |
| `templateRefs[]` | One or more AnalysisTemplates. |
| `args[]` | Argument values. |
| `dryRun[]` | Metric names to evaluate but not fail the run on. |
| `inconclusivePolicy` | `Abort` / `Pause` / `Ignore`. |

## Status

The observed state:

| Field | Meaning |
| ----- | ------- |
| `phase` | `Pending` / `Progressing` / `Paused` / `Promoting` / `Healthy` / `Degraded` / `Aborted`. |
| `currentStep` | Name of the executing step, or empty when done. |
| `stepStartedAt` | When the current step began. |
| `pauseConditions[]` | Typed pause reasons: `PausedStep`, `UserRequested`, `AnalysisInconclusive`, `AnalysisFailed`, `AwaitingPromotion`. |
| `stableRevision` / `currentRevision` | Pod-template-hashes of the previous stable RS and the new one. |
| `analysisRunSummaries[]` | Compact pointer per completed AnalysisRun. |
| `replicas` / `updatedReplicas` / `readyReplicas` / `availableReplicas` | Mirrors Deployment counters exactly. |

## AnalysisTemplate / ClusterAnalysisTemplate

See [analysis.md](analysis.md).

## Patterns

There is no `strategy` field on the Rollout spec. "Canary" and "blue-green"
are step-sequence patterns over the same two service roles:

### Weighted canary

Gradually shift traffic from `stableServices` to `canaryServices`.

```yaml
stableServices: [{name: my-app}]
canaryServices: [{name: my-app-canary}]
progression:
  steps:
    - {name: w25,  setWeight: {weight: 25}}
    - {name: soak, pause: {duration: 5m}}
    - {name: w50,  setWeight: {weight: 50}}
    - {name: gate, analysis: {templateRefs: [{name: perf}]}}
    - {name: w100, setWeight: {weight: 100}}
```

### Full-scale cutover

What other tools call "blue-green": scale the canary to full capacity,
gate on analyses, then flip traffic atomically via `Promote`.

```yaml
stableServices: [{name: my-app}]
canaryServices: [{name: my-app-canary}]
autoPromotion: Manual
progression:
  scaleDownDelaySeconds: 60
  steps:
    - {name: scale-canary, setCanaryScale: {percent: 100}}
    - {name: smoke, analysis: {templateRefs: [{name: smoke}]}}
    - {name: wait,  pause: {}}
    - {name: cutover, promote: {}}
    - {name: verify, analysis: {templateRefs: [{name: health}], failurePolicy: Rollback}}
```

`Promote` is what makes this a cutover: it flips the `stableServices`
selector at the new ReplicaSet's pod-template-hash, so traffic moves in a
single atomic step rather than via a graduated SetWeight ramp.

### Hybrid (pre-warm then ramp)

Combine both — full-scale the canary first, then ramp traffic:

```yaml
progression:
  steps:
    - {name: scale-canary, setCanaryScale: {percent: 100}}
    - {name: w10,  setWeight: {weight: 10}}
    - {name: gate, analysis: {templateRefs: [{name: errors}]}}
    - {name: w100, setWeight: {weight: 100}}
```

See `test/fixtures/full-scale-cutover.yaml` and `test/fixtures/canary-istio.yaml`.
