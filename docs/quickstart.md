# Quickstart: canary rollout on Istio with a Prometheus gate

This walks through deploying the Rollouts controller to a kind cluster,
installing Istio, and rolling out a sample application through a
three-step canary that pauses on a Prometheus analysis gate.

## Prerequisites

- kind 0.27+, kubectl 1.30+, istioctl 1.22+
- A Prometheus reachable from the cluster (the quickstart uses
  `prometheus-community/prometheus` on the `monitoring` namespace).

## 1. Build and load the controller

```bash
make docker
kind create cluster --name rollouts
kind load docker-image rollouts-controller:dev --name rollouts
```

## 2. Install the CRDs, RBAC, and controller

```bash
kubectl apply -f config/crd/bases/
kubectl apply -k config/manager/
kubectl -n rollouts-system wait deploy/rollouts-controller --for=condition=available --timeout=60s
```

## 3. Install Istio

```bash
istioctl install --set profile=minimal -y
kubectl label namespace default istio-injection=enabled --overwrite
```

## 4. Deploy the sample app (stable and canary Services, VirtualService)

```yaml
# app.yaml
apiVersion: v1
kind: Service
metadata: {name: my-app, namespace: default}
spec:
  selector: {app: my-app}
  ports: [{port: 80, targetPort: 8080}]
---
apiVersion: v1
kind: Service
metadata: {name: my-app-canary, namespace: default}
spec:
  selector: {app: my-app}
  ports: [{port: 80, targetPort: 8080}]
---
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

```bash
kubectl apply -f app.yaml
```

## 5. Define an AnalysisTemplate

The template queries Prometheus for the success rate over the last minute
and fails the canary if it drops below 99%.

```yaml
apiVersion: rollouts.io/v1alpha1
kind: AnalysisTemplate
metadata: {name: perf, namespace: default}
spec:
  metrics:
    - name: success-rate
      interval: 30s
      count: 3
      successCondition: result >= 0.99
      failureCondition: result < 0.95
      failureLimit: 0
      provider:
        type: prometheus
        prometheus:
          address: http://prometheus.monitoring.svc.cluster.local:9090
          query: |
            sum(rate(http_requests_total{app="my-app",status=~"2.."}[1m]))
              /
            sum(rate(http_requests_total{app="my-app"}[1m]))
```

## 6. Apply the Rollout

```yaml
apiVersion: rollouts.io/v1alpha1
kind: Rollout
metadata: {name: my-app, namespace: default}
spec:
  replicas: 5
  selector: {matchLabels: {app: my-app}}
  template:
    metadata: {labels: {app: my-app}}
    spec:
      containers:
        - name: app
          image: ghcr.io/example/my-app:v2
          ports: [{containerPort: 8080}]

  canaryServices: [{name: my-app-canary}]
  stableServices: [{name: my-app}]

  trafficRouting:
    provider: istio
    istio:
      virtualServices:
        - {name: my-app, routes: [primary]}

  progression:
    steps:
      - name: canary-25
        setWeight: {weight: 25}
      - name: soak
        pause: {duration: 2m}
      - name: canary-50
        setWeight: {weight: 50}
      - name: gate
        analysis:
          templateRefs: [{name: perf}]
          failurePolicy: Rollback
      - name: canary-100
        setWeight: {weight: 100}
```

## 7. Watch the rollout

```bash
kubectl get rollout my-app -w
```

Expected phase progression:

```
Pending → Progressing → Paused (during soak) → Progressing → Healthy
```

At the `gate` step the controller creates an AnalysisRun named
`my-app-gate-<pod-hash>`. Inspect it with:

```bash
kubectl get analysisrun -o wide
kubectl describe analysisrun my-app-gate-<hash>
```

## 8. Abort or roll back

If the canary is behaving badly, set `spec.abort: true`:

```bash
kubectl patch rollout my-app --type=merge -p '{"spec":{"abort":true}}'
```

The controller resets router weight to 0, scales the canary RS to 0, and
moves to `phase: Aborted`. Clear `abort` to resume from the current step.
