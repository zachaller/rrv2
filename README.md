# Rollouts

A Kubernetes controller for progressive delivery — a fork of the Kubernetes
Deployment controller that adds canary, blue-green, analysis gates, and traffic
shaping over Istio, Nginx, ALB, SMI, Traefik, and APISIX.

## Status

Early development. API group is `rollouts.io/v1alpha1` and is not yet stable.

## Provenance

Portions of `pkg/controller/rollout/_forkedfrom_k8s/` are derived from
`kubernetes/kubernetes` at tag `v1.31.9`, Apache-2.0 licensed. See `NOTICE`.
