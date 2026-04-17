/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package trafficrouting is the provider interface the Rollout controller uses
// to shape Service/Ingress/Mesh traffic during a progressive rollout.
//
// Each provider (istio, nginx, alb, smi, traefik, apisix) lives in its own
// subpackage and registers itself via init() on import.
package trafficrouting

import (
	"context"
	"fmt"
	"sync"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	rolloutsv1alpha1 "github.com/zaller/rollouts/pkg/apis/rollouts/v1alpha1"
)

// Plugin is the surface every router implementation provides.
type Plugin interface {
	// Type returns the provider discriminator (e.g. "istio") — must match
	// rolloutsv1alpha1.TrafficRoutingSpec.Provider.
	Type() string

	// SetWeight shifts `desiredWeight` percent of traffic to the canary role
	// (or preview, when the rollout is blue-green). Providers that don't accept
	// inline weights (Traefik, APISIX) mutate their external CRs to match.
	SetWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, desiredWeight int32) error

	// SetHeaderRoute installs (or removes, when match is empty) a header/cookie/
	// query-based routing rule that sends matching traffic to the canary
	// regardless of weight. Providers that can't express this return an error.
	SetHeaderRoute(ctx context.Context, ro *rolloutsv1alpha1.Rollout, match []rolloutsv1alpha1.RouteMatch) error

	// VerifyWeight returns true once the router has observed the desired weight.
	// Return (false, nil) to signal "not yet, check again later".
	VerifyWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, desiredWeight int32) (bool, error)

	// UpdateHosts rewrites Service selectors / Ingress backends to point at
	// the named canary and stable Services. Called once per rollout start and
	// at every Promote step.
	UpdateHosts(ctx context.Context, ro *rolloutsv1alpha1.Rollout, canarySvc, stableSvc string) error

	// RemoveManagedRoutes tears down managed routes when the rollout completes
	// or is aborted.
	RemoveManagedRoutes(ctx context.Context, ro *rolloutsv1alpha1.Rollout) error
}

// Factory constructs a Plugin given the cluster clients it needs.
type Factory func(kube kubernetes.Interface, dyn dynamic.Interface) Plugin

// Registry is the process-wide registry of provider factories.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// Global is the default registry — each provider subpackage calls Global.Register in init().
var Global = &Registry{factories: map[string]Factory{}}

// Register adds a provider factory under its type name. Repeated registration
// under the same name panics (indicates a build-time mistake).
func (r *Registry) Register(typ string, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.factories[typ]; exists {
		panic(fmt.Sprintf("trafficrouting: duplicate provider registration for %q", typ))
	}
	r.factories[typ] = f
}

// Lookup returns the factory for typ. Returns nil when no provider is registered.
func (r *Registry) Lookup(typ string) Factory {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.factories[typ]
}

// Register is a shortcut for Global.Register — the typical call site in init() blocks.
func Register(typ string, f Factory) { Global.Register(typ, f) }

// Resolve returns the Plugin configured by the Rollout's spec, or nil if
// trafficRouting is unset. Returns an error if the named provider isn't registered.
func Resolve(ro *rolloutsv1alpha1.Rollout, kube kubernetes.Interface, dyn dynamic.Interface) (Plugin, error) {
	if ro.Spec.TrafficRouting == nil {
		return nil, nil
	}
	f := Global.Lookup(ro.Spec.TrafficRouting.Provider)
	if f == nil {
		return nil, fmt.Errorf("trafficrouting: no provider registered for %q", ro.Spec.TrafficRouting.Provider)
	}
	return f(kube, dyn), nil
}
