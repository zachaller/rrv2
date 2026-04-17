/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package istio implements the Istio VirtualService/DestinationRule provider.
//
// The implementation uses the dynamic client (not a typed istio.io/client-go
// dependency) so the controller binary stays small when Istio isn't in use.
// Weights are patched onto VirtualService HTTP route `destination.weight`
// entries; the subset/host fields must already be configured by the operator.
package istio

import (
	"context"
	"encoding/json"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
	"github.com/zachaller/rrv2/pkg/trafficrouting"
)

// Type is the discriminator matched against trafficRouting.provider.
const Type = "istio"

// Istio VirtualService GVR. v1beta1 is still the stable default in recent releases.
var virtualServiceGVR = schema.GroupVersionResource{
	Group:    "networking.istio.io",
	Version:  "v1beta1",
	Resource: "virtualservices",
}

// Provider implements trafficrouting.Plugin for Istio.
type Provider struct {
	kube kubernetes.Interface
	dyn  dynamic.Interface
}

// New constructs a Provider.
func New(kube kubernetes.Interface, dyn dynamic.Interface) trafficrouting.Plugin {
	return &Provider{kube: kube, dyn: dyn}
}

func init() { trafficrouting.Register(Type, New) }

// Type returns the provider discriminator.
func (p *Provider) Type() string { return Type }

// SetWeight walks every VirtualService named in the rollout's Istio config and
// rewrites the weight distribution on each configured HTTP route so that
// `desiredWeight` of traffic goes to the canary host and the remainder to the
// stable host.
//
// Host identification: the first canaryService becomes the canary host, the
// first stableService becomes the stable host. Multi-host setups (where a
// VirtualService routes to many Services) are expected to already have each
// destination named with its host — the controller only adjusts weights.
func (p *Provider) SetWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, desiredWeight int32) error {
	cfg := istioConfig(ro)
	if cfg == nil {
		return fmt.Errorf("trafficrouting/istio: rollout %s/%s has no istio config", ro.Namespace, ro.Name)
	}
	canaryHost, stableHost, err := resolveHosts(ro)
	if err != nil {
		return err
	}

	for _, vsRef := range cfg.VirtualServices {
		if err := p.patchVirtualService(ctx, ro.Namespace, vsRef, canaryHost, stableHost, desiredWeight); err != nil {
			return err
		}
	}
	return nil
}

// SetHeaderRoute is unimplemented for now — real Istio implementation inserts
// a match[] block with Headers/Cookies entries into the first HTTP route.
func (p *Provider) SetHeaderRoute(ctx context.Context, ro *rolloutsv1alpha1.Rollout, match []rolloutsv1alpha1.RouteMatch) error {
	if len(match) == 0 {
		return p.RemoveManagedRoutes(ctx, ro)
	}
	return fmt.Errorf("trafficrouting/istio: SetHeaderRoute not implemented")
}

// VerifyWeight reads the VirtualService back and checks that the canary route
// weight matches. In Istio the write is synchronous so this is mostly a
// sanity check for concurrent edits.
func (p *Provider) VerifyWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, desiredWeight int32) (bool, error) {
	cfg := istioConfig(ro)
	if cfg == nil {
		return false, fmt.Errorf("trafficrouting/istio: rollout %s/%s has no istio config", ro.Namespace, ro.Name)
	}
	canaryHost, _, err := resolveHosts(ro)
	if err != nil {
		return false, err
	}

	for _, vsRef := range cfg.VirtualServices {
		vs, err := p.dyn.Resource(virtualServiceGVR).Namespace(ro.Namespace).Get(ctx, vsRef.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		ok, err := verifyHTTPWeight(vs, vsRef.Routes, canaryHost, desiredWeight)
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

// UpdateHosts is a no-op for Istio — hosts are configured once on the VirtualService.
func (p *Provider) UpdateHosts(ctx context.Context, ro *rolloutsv1alpha1.Rollout, canarySvc, stableSvc string) error {
	return nil
}

// RemoveManagedRoutes resets canary weight to zero on rollout completion/abort.
func (p *Provider) RemoveManagedRoutes(ctx context.Context, ro *rolloutsv1alpha1.Rollout) error {
	return p.SetWeight(ctx, ro, 0)
}

// istioConfig returns the Istio-specific block from the rollout, or nil.
func istioConfig(ro *rolloutsv1alpha1.Rollout) *rolloutsv1alpha1.IstioConfig {
	if ro.Spec.TrafficRouting == nil {
		return nil
	}
	return ro.Spec.TrafficRouting.Istio
}

// resolveHosts returns the canary and stable Service names (their Kubernetes
// Service names are also their VirtualService destination hosts, per convention).
func resolveHosts(ro *rolloutsv1alpha1.Rollout) (canary, stable string, err error) {
	if len(ro.Spec.CanaryServices) == 0 {
		return "", "", fmt.Errorf("trafficrouting/istio: rollout %s/%s has no canaryServices", ro.Namespace, ro.Name)
	}
	if len(ro.Spec.StableServices) == 0 {
		return "", "", fmt.Errorf("trafficrouting/istio: rollout %s/%s has no stableServices", ro.Namespace, ro.Name)
	}
	return ro.Spec.CanaryServices[0].Name, ro.Spec.StableServices[0].Name, nil
}

// patchVirtualService rewrites the named routes (or all, when none are named)
// so that canary host gets weight and stable host gets 100-weight. Uses a
// server-side JSON merge patch on the spec subtree.
func (p *Provider) patchVirtualService(ctx context.Context, namespace string, ref rolloutsv1alpha1.IstioVirtualServiceRef, canaryHost, stableHost string, weight int32) error {
	vs, err := p.dyn.Resource(virtualServiceGVR).Namespace(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("get virtualservice %s/%s: %w", namespace, ref.Name, err)
	}

	http, found, err := unstructured.NestedSlice(vs.Object, "spec", "http")
	if err != nil {
		return err
	}
	if !found {
		return fmt.Errorf("trafficrouting/istio: virtualservice %s/%s has no spec.http", namespace, ref.Name)
	}

	routeSelector := routeSelector(ref.Routes)
	rewritten, err := rewriteHTTPRoutes(http, routeSelector, canaryHost, stableHost, weight)
	if err != nil {
		return err
	}
	if err := unstructured.SetNestedSlice(vs.Object, rewritten, "spec", "http"); err != nil {
		return err
	}

	patch, err := json.Marshal(map[string]any{"spec": map[string]any{"http": rewritten}})
	if err != nil {
		return err
	}
	_, err = p.dyn.Resource(virtualServiceGVR).Namespace(namespace).Patch(ctx, ref.Name, types.MergePatchType, patch, metav1.PatchOptions{})
	return err
}

// routeSelector returns a matcher from the configured route names. An empty
// list means "apply to every route".
func routeSelector(names []string) func(string) bool {
	if len(names) == 0 {
		return func(string) bool { return true }
	}
	set := map[string]struct{}{}
	for _, n := range names {
		set[n] = struct{}{}
	}
	return func(name string) bool { _, ok := set[name]; return ok }
}

// rewriteHTTPRoutes adjusts destination weights on every matched HTTP route.
// Routes with a single destination are left alone. Routes with two+ are
// normalized to [{host: canary, weight}, {host: stable, 100-weight}].
func rewriteHTTPRoutes(routes []any, matches func(string) bool, canaryHost, stableHost string, weight int32) ([]any, error) {
	out := make([]any, len(routes))
	for i, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("trafficrouting/istio: http[%d] is not an object", i)
		}
		name, _ := route["name"].(string)
		if !matches(name) {
			out[i] = route
			continue
		}
		dests, _, _ := unstructured.NestedSlice(route, "route")
		if len(dests) < 2 {
			out[i] = route
			continue
		}

		newDests := []any{
			map[string]any{
				"destination": map[string]any{"host": canaryHost},
				"weight":      int64(weight),
			},
			map[string]any{
				"destination": map[string]any{"host": stableHost},
				"weight":      int64(100 - weight),
			},
		}
		if err := unstructured.SetNestedSlice(route, newDests, "route"); err != nil {
			return nil, err
		}
		out[i] = route
	}
	return out, nil
}

// verifyHTTPWeight walks a VirtualService and returns true iff every matched
// route's canary-destination weight equals desiredWeight.
func verifyHTTPWeight(vs *unstructured.Unstructured, routes []string, canaryHost string, desiredWeight int32) (bool, error) {
	http, found, err := unstructured.NestedSlice(vs.Object, "spec", "http")
	if err != nil || !found {
		return false, err
	}
	matches := routeSelector(routes)
	for _, raw := range http {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, _ := route["name"].(string)
		if !matches(name) {
			continue
		}
		dests, _, _ := unstructured.NestedSlice(route, "route")
		for _, d := range dests {
			destMap, ok := d.(map[string]any)
			if !ok {
				continue
			}
			destination, _ := destMap["destination"].(map[string]any)
			host, _ := destination["host"].(string)
			if host != canaryHost {
				continue
			}
			weight, _ := destMap["weight"].(int64)
			if int32(weight) != desiredWeight {
				return false, nil
			}
		}
	}
	return true, nil
}
