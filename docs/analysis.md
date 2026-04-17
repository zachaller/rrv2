# Analysis and metric providers

The Rollouts controller gates progression on `AnalysisRun` outcomes.
AnalysisRuns are built from `AnalysisTemplate` or `ClusterAnalysisTemplate`
resources and live beside the Rollout in the same namespace (or at cluster
scope for `ClusterAnalysisTemplate`).

## Writing an AnalysisTemplate

```yaml
apiVersion: rollouts.io/v1alpha1
kind: AnalysisTemplate
metadata: {name: perf, namespace: default}
spec:
  args:
    - name: threshold
      value: "0.99"
  metrics:
    - name: success-rate
      interval: 30s            # how often to re-sample
      initialDelay: 15s        # wait before first sample
      count: 3                 # total samples; run finalizes after this many
      failureLimit: 0          # 0 = fail immediately on first failed sample
      consecutiveErrorLimit: 4 # tolerate N consecutive provider errors
      successCondition: result >= 0.99
      failureCondition: result < 0.95
      provider:
        type: prometheus
        prometheus:
          address: http://prometheus.monitoring.svc:9090
          query: |
            sum(rate(http_requests_total{job="my-app",status=~"2.."}[1m]))
              /
            sum(rate(http_requests_total{job="my-app"}[1m]))
          timeout: 10s
```

## Expression language

`successCondition` and `failureCondition` are evaluated by
[expr-lang/expr](https://expr-lang.org/) — a safe, compile-ahead expression
language with familiar syntax. The sampled metric value is bound to the
`result` variable; scalars and slices both work.

Common patterns:

```
result >= 0.99                     # scalar success rate above 99%
result < 0.01                      # failure rate below 1%
result[0] >= 0.99 && result[1] < 0.5
result >= 0.95 || result == 1.0
!(result < 0.5)                    # negation
result >= 0.5 ? result > 0.9 : result > 0   # ternary
result in [200, 204, 206]          # set membership
```

Collection helpers over multi-sample vectors:

```
all(result, {# >= 0.99})           # every sample above 99%
any(result, {# < 0.1})             # at least one sample below 10%
none(result, {# < 0})              # no negative samples
count(result, {# > 0.5}) >= 2      # at least two samples above 50%
len(result) > 2                    # slice length check
```

Arithmetic is available too — e.g. `result[0] / result[1] > 2` for ratio
comparisons between named queries.

Strings are parsed into numbers when they look numeric, so Prometheus
sample values (always returned as strings by the wire format) compare
directly against numeric literals. A bare `result` is a truthiness test
— non-zero numbers are true.

When both `successCondition` and `failureCondition` fail to evaluate
cleanly (compile error, undefined variable, index out of range), the
sample is recorded as `Inconclusive` and the metric is subject to the
configured `InconclusivePolicy` — `Abort`, `Pause`, or `Ignore`. See the
[expr documentation](https://expr-lang.org/docs/language-definition) for
the full grammar.

## Metric phases

Each metric in an AnalysisRun accumulates a `MetricResult` with these
counters: `Successful`, `Failed`, `Inconclusive`, `Error`. The per-metric
phase is derived each reconcile from these counters:

- `Error` — more than `consecutiveErrorLimit` consecutive Error samples.
- `Failed` — more than `failureLimit` Failed samples total.
- `Successful` — `count` conclusive samples taken with zero Failed.
- `Running` — otherwise.

The run's overall phase aggregates per-metric phases. A metric listed in
`spec.dryRun` can report `Failed` or `Error` without failing the run.

## Providers implemented in this build

- **prometheus** — implemented. Instant queries, optional headers for auth,
  per-query timeout, scalar/vector result types. Multi-sample vectors are
  exposed to the evaluator as `[]float64` so `result[N]` indexing works.

The other provider types in the API (`datadog`, `newRelic`, `wavefront`,
`cloudWatch`, `kayenta`, `web`, `job`) are defined in the spec but return
"not implemented" at runtime. Contributions are straightforward: implement
`providers.<Name>` with a `Query(ctx, spec) (any, error)` method and add
a case to the `dispatch` switch in `pkg/controller/analysis/controller.go`.

## Rollout-scoped vs step-scoped analysis

There are two places to declare analysis:

- **Step-scoped** (`progression.steps[].analysis`): the rollout pauses on
  this step and waits for the AnalysisRun's terminal phase before moving
  on. Use for gates — "only advance if success rate is fine."
- **Rollout-scoped** (`spec.analysis[]`): fires at a lifecycle event
  (`preStep`, `postStep`, `background`, `prePromotion`, `postPromotion`).
  `background` runs for the lifetime of the rollout and can abort it on
  failure even while other steps are executing.

Both shapes share the same AnalysisHook shape and the same AnalysisRun
output, so the same templates are reusable across both surfaces.
