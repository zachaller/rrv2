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
| `spec.canaryServices` | `[]ServiceRef` | — | Services routed to the canary ReplicaSet. |
| `spec.stableServices` | `[]ServiceRef` | — | Services routed to the stable ReplicaSet. |
| `spec.activeServices` | `[]ServiceRef` | — | Blue-green active Services (flipped at Promote). |
| `spec.previewServices` | `[]ServiceRef` | — | Blue-green preview Services. |
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
| `setWeight` | Shift router traffic. Fields: `weight` (0-100), `forRole` (canary/preview), `matches` (header/cookie/query rules). |
| `setCanaryScale` | Adjust canary RS replica count independent of traffic. Fields: `replicas` / `percent` / `matchTrafficWeight`. |
| `pause` | Halt progression. `duration` nil means indefinite. |
| `analysis` | Create owning AnalysisRun and gate on its Phase. Fields: `templateRefs`, `args`, `dryRun`, `failurePolicy`. |
| `experiment` | Run a side-by-side experiment. |
| `promote` | Flip the router and repoint stable/active Services at the new RS. |

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
| `pauseConditions[]` | Typed pause reasons (PausedStep, UserRequested, AnalysisFailed, etc.). |
| `stableRevision` / `currentRevision` | Pod-template-hashes of the previous stable RS and the new one. |
| `analysisRunSummaries[]` | Compact pointer per completed AnalysisRun. |
| `replicas` / `updatedReplicas` / `readyReplicas` / `availableReplicas` | Mirrors Deployment counters exactly. |

## AnalysisTemplate / ClusterAnalysisTemplate

See [analysis.md](analysis.md).
