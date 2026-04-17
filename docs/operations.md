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
