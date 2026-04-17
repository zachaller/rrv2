# Operations

## Topology

One controller Deployment per cluster, scaled to two replicas for
availability, with leader election via a Lease. Metrics on `:8080`,
probes on `:8081`. Run under a non-root user with a read-only root
filesystem; the delivered Deployment does this already.

```
kubectl -n rollouts-system get deploy,pod,lease
```

## RBAC

The bundled `ClusterRole` is broad — it touches every router's CRDs plus
core Services, Deployments, ReplicaSets, Pods, Events, Leases. Narrow it
to just the providers you actually use:

- Istio-only: drop `split.smi-spec.io`, `traefik.*`, `apisix.apache.org`
  rules.
- Nginx-only: drop `networking.istio.io` and the mesh rules.

## Observability

Built-in metrics (exposed by controller-runtime, no extra instrumentation
yet in the Rollouts controller itself):

- `controller_runtime_reconcile_total{controller,result}`
- `controller_runtime_reconcile_time_seconds{controller}`
- `controller_runtime_max_concurrent_reconciles{controller}`
- `workqueue_depth{name}`, `workqueue_retries_total{name}`

Useful PromQL for alerting:

```promql
# Any Rollout stuck in a non-terminal phase for over an hour:
rate(controller_runtime_reconcile_total{controller="rollout"}[5m])
  unless on (namespace, name)
(kube_customresource_status_phase{customresource_kind="Rollout", phase=~"Healthy|Aborted|Degraded"})
```

(Kube-state-metrics configured to expose CRD fields is required for the
`phase` series — see the kube-state-metrics `--custom-resource-state`
flag.)

## Abort

```
kubectl patch rollout <name> --type=merge -p '{"spec":{"abort":true}}'
```

The controller:

1. Calls `SetWeight(0)` on the router (if configured) — canary stops
   receiving traffic immediately.
2. Scales the canary ReplicaSet to 0.
3. Sets `status.phase: Aborted`.

Clearing `spec.abort` resumes at `status.currentStep`. If the rollout was
mid-canary and you want to discard the canary entirely, delete the canary
RS manually — the controller will re-create it with the current
pod-template-hash on next reconcile.

## Pause

`spec.paused: true` halts the reconcile loop without changing any
downstream state. Use this when you want to freeze the rollout for
inspection. `spec.paused` is different from `progression.steps[].pause`,
which is a normal step-level pause that the Executor honors.

## Autoscaling (HPA cooperation)

A HorizontalPodAutoscaler can target a Rollout directly:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata: {name: my-app, namespace: default}
spec:
  scaleTargetRef:
    apiVersion: rollouts.io/v1alpha1
    kind: Rollout
    name: my-app
  minReplicas: 4
  maxReplicas: 20
  metrics:
    - type: Resource
      resource: {name: cpu, target: {type: Utilization, averageUtilization: 70}}
```

The `Rollout` CRD exposes a `scale` subresource pointing at
`spec.replicas`, so HPA updates land in the normal place.

### How the split is maintained

On every reconcile the controller:

1. Reads `spec.replicas` as the total desired pod count.
2. Reads `status.currentWeight` (updated by the most recent `SetWeight` or
   `Promote` step) as the current traffic split.
3. Computes `canary = ceil(desired × weight / 100)`, `stable = desired - canary`.
4. Calls the forked `scaleReplicaSet` on each side to converge.

If HPA increases `spec.replicas` from 10 to 14 while the rollout is mid-
ramp at 25% canary, the next reconcile takes the canary from 3 pods to 4
and the stable from 7 to 10, preserving the weight split without any
human action.

`status.currentWeight` and `status.desiredReplicas` are persisted between
reconciles and are what let the controller distinguish its own step-
driven scale changes from external scaler activity.

### `spec.hpaStrategy`

| Mode | Behavior |
| ---- | -------- |
| `Preserve` (default) | HPA controls total pods; weight-driven split is preserved. |
| `StableOnly` | HPA scales the stable RS only; canary count is whatever `SetCanaryScale` asked for. Useful when you want an exact-replica canary regardless of autoscaling. |
| `Disabled` | Controller ignores `spec.replicas` changes during the rollout. It still reads the initial value; subsequent HPA updates are observed but not acted on until the rollout completes. |

### Known interactions

**`scaleDownDelaySeconds` doubles capacity briefly.** At `Promote`, the
controller moves traffic to 100% canary and repoints `stableServices` at
the new RS's pod-template-hash, but the old stable RS is retained for up
to `scaleDownDelaySeconds` (default 30s) so in-flight requests can drain.
During that window both RSes run at their promote-time capacities. For a
50% canary being promoted to 100%, this means `1.5 × desired` pods for
the delay duration. Size `maxSurge` and `minReplicas` accordingly.

**Promote guards against HPA races.** If HPA bumps `spec.replicas` at the
same moment Promote is scaling down the old RS, the scale-down is
deferred until `status.observedGeneration == metadata.generation` and
`status.desiredReplicas == spec.replicas`. Without this guard the old RS
could be scaled to zero while HPA was mid-way through scaling it up.

**First-sync capacity.** A fresh Rollout's first reconcile creates the
new ReplicaSet at zero replicas (not at `spec.replicas`). The second
reconcile grows it to the weight-derived canary count. This avoids a
momentary capacity doubling between RS creation and the split
reconciliation.

## Rollback

The `rollbackWindow` field bounds automatic rollback to the most recent N
revisions. The forked cleanup helper prunes history to
`revisionHistoryLimit` during `finalize`, so old revisions disappear
once they fall out of that window.

To manually roll back to a previous revision, update `spec.template` to
match the desired revision's pod spec. The controller will pick up the
existing ReplicaSet at that pod-template-hash rather than creating a new
one.

## Upgrading the forked deployment controller

Re-run the import with a new upstream tag:

```
./hack/import-k8s-deployment.sh v1.32.0
```

The script uses `git read-tree --prefix`, so upstream authorship and
blame are preserved. Re-apply the mechanical patches (package rename,
internal import rewrites) by hand or via `sed` as documented in
`hack/import-k8s-deployment.sh` comments.

## Known gaps

- Experiments: CRD defined, reconciler is a skeleton.
- Header/cookie routing on Istio: spec defined, provider unimplemented.
- Datadog / NewRelic / Wavefront / CloudWatch / Kayenta / Web / Job
  analysis providers: spec defined, all return "not implemented."
- Nginx / ALB / SMI / Traefik / APISIX routers: registered but stubbed.
