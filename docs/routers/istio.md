# Istio integration

The Istio provider shapes traffic by rewriting `weight` fields on HTTP
routes in one or more `VirtualService` resources. Hosts, subsets, and
non-weight route metadata are left untouched.

## Prerequisites

- Istio sidecar injection enabled for the rollout's namespace.
- Two Services — one routed to canary pods, one routed to stable pods.
- A VirtualService whose HTTP route has both destinations declared, with
  any initial weight (the controller will overwrite).

```yaml
apiVersion: networking.istio.io/v1beta1
kind: VirtualService
metadata: {name: my-app, namespace: default}
spec:
  hosts: [my-app]
  http:
    - name: primary
      route:
        - destination: {host: my-app-canary}
          weight: 0
        - destination: {host: my-app}
          weight: 100
```

## How the provider decides which routes to modify

The Rollout's `trafficRouting.istio.virtualServices[]` names VirtualServices
and, optionally, specific route names inside them. An empty `routes` list
means "apply to every HTTP route in this VirtualService."

```yaml
trafficRouting:
  provider: istio
  istio:
    virtualServices:
      - name: my-app
        routes: [primary]     # only this named HTTP route
```

Only routes with **two or more destinations** are rewritten. Single-
destination routes are left alone because the controller has no signal as
to which host is canary vs stable in that shape.

## How weights are assigned

At `setWeight: {weight: N}`, every matched route is normalized to:

```yaml
route:
  - destination: {host: <canaryService>}
    weight: N
  - destination: {host: <stableService>}
    weight: 100 - N
```

Where `canaryService` is the first entry of `spec.canaryServices` and
`stableService` is the first entry of `spec.stableServices`. The host
name is the Service name in the rollout's namespace.

## VerifyWeight

By default the controller reads the VirtualService back after patching and
confirms the canary-destination weight matches. Set
`trafficRouting.verifyWeight: Disabled` to skip; this is useful when
another controller also mutates the VirtualService (in which case
verification can oscillate).

## Header and cookie routing

Header-based routing (`step.setWeight.matches`) is not yet implemented in
the Istio provider — the upstream shape (Istio `http[].match[]` blocks)
differs from the weighted routing above. Setting `matches` on a step
returns an error; the field is carried in the spec so that future
versions can land implementation without a CRD change.

## Promote step

At `promote`, the controller:

1. Sets the router to 100% canary (reusing `SetWeight`).
2. Rewrites the selector of every `stableServices[]` Service to pin it at
   the new ReplicaSet's `pod-template-hash`. Existing selector keys are
   preserved.
3. Waits `progression.scaleDownDelaySeconds` (default 30s) and then scales
   the previous stable ReplicaSet to zero via the forked scaleReplicaSet
   helper.

## Cleanup on abort and completion

On `spec.abort: true` the provider resets the canary-destination weight
to `0` (leaving the rest of the VirtualService untouched). On rollout
completion the controller calls `RemoveManagedRoutes`, which similarly
resets to 0 — the VirtualService is left pointing entirely at stable so
subsequent rollouts start from a known state.

## Limitations

- Only `networking.istio.io/v1beta1` is targeted. `v1` VirtualServices
  should work since the `spec.http[].route[].weight` field is identical,
  but the GVR is hard-coded in `pkg/trafficrouting/istio/istio.go`.
- Subset switching via `DestinationRule` is in the API but not yet
  implemented — the provider currently identifies canary/stable purely
  by Service host.
