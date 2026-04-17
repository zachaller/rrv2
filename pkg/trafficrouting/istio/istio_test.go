/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package istio

import (
	"context"
	"encoding/json"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
)

// makeVirtualService builds a minimal VirtualService unstructured with a
// single HTTP route named "primary" routing equally to two destinations.
func makeVirtualService(namespace, name string, canaryHost, stableHost string, canaryWeight int64) *unstructured.Unstructured {
	vs := &unstructured.Unstructured{}
	vs.SetAPIVersion("networking.istio.io/v1beta1")
	vs.SetKind("VirtualService")
	vs.SetNamespace(namespace)
	vs.SetName(name)
	vs.Object["spec"] = map[string]any{
		"hosts": []any{"example.local"},
		"http": []any{
			map[string]any{
				"name": "primary",
				"route": []any{
					map[string]any{
						"destination": map[string]any{"host": canaryHost},
						"weight":      canaryWeight,
					},
					map[string]any{
						"destination": map[string]any{"host": stableHost},
						"weight":      int64(100 - canaryWeight),
					},
				},
			},
		},
	}
	return vs
}

// newFakeProvider wires a Provider backed by a dynamic fake client preloaded
// with the supplied VirtualService objects.
func newFakeProvider(t *testing.T, objects ...runtime.Object) *Provider {
	t.Helper()
	scheme := runtime.NewScheme()
	// Register the VirtualService list kind on the scheme so the dynamic fake
	// client knows how to list it.
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "networking.istio.io", Version: "v1beta1", Kind: "VirtualServiceList",
	}, &unstructured.UnstructuredList{})
	dyn := dynamicfake.NewSimpleDynamicClient(scheme, objects...)
	return &Provider{dyn: dyn}
}

// rolloutWithIstio builds a Rollout whose trafficRouting targets the named
// VirtualService in namespace/default.
func rolloutWithIstio(vsName string, routes ...string) *rolloutsv1alpha1.Rollout {
	return &rolloutsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: rolloutsv1alpha1.RolloutSpec{
			CanaryServices: []rolloutsv1alpha1.ServiceRef{{Name: "app-canary"}},
			StableServices: []rolloutsv1alpha1.ServiceRef{{Name: "app"}},
			TrafficRouting: &rolloutsv1alpha1.TrafficRoutingSpec{
				Provider: "istio",
				Istio: &rolloutsv1alpha1.IstioConfig{
					VirtualServices: []rolloutsv1alpha1.IstioVirtualServiceRef{
						{Name: vsName, Routes: routes},
					},
				},
			},
		},
	}
}

// TestSetWeight_RewritesPrimaryRoute is the happy-path golden check: a 0/100
// VirtualService moves to the desired 25/75 split, preserving the surrounding
// route structure.
func TestSetWeight_RewritesPrimaryRoute(t *testing.T) {
	initial := makeVirtualService("default", "app", "app-canary", "app", 0)
	provider := newFakeProvider(t, initial)
	ro := rolloutWithIstio("app", "primary")

	if err := provider.SetWeight(context.Background(), ro, 25); err != nil {
		t.Fatalf("SetWeight: %v", err)
	}

	got, err := provider.dyn.Resource(virtualServiceGVR).Namespace("default").Get(context.Background(), "app", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get virtualservice back: %v", err)
	}

	http, found, err := unstructured.NestedSlice(got.Object, "spec", "http")
	if err != nil || !found {
		t.Fatalf("missing spec.http: found=%v err=%v", found, err)
	}
	route := http[0].(map[string]any)
	dests, _, _ := unstructured.NestedSlice(route, "route")
	if len(dests) != 2 {
		t.Fatalf("expected 2 destinations, got %d", len(dests))
	}

	// Dump for golden comparison.
	gotJSON, _ := json.MarshalIndent(dests, "", "  ")
	wantJSON := `[
  {
    "destination": {
      "host": "app-canary"
    },
    "weight": 25
  },
  {
    "destination": {
      "host": "app"
    },
    "weight": 75
  }
]`
	if string(gotJSON) != wantJSON {
		t.Errorf("destination weights diverge.\n got: %s\nwant: %s", gotJSON, wantJSON)
	}
}

// TestSetWeight_HonorsRouteSelector verifies that named-route filtering
// prevents the controller from trampling unrelated routes in the same
// VirtualService.
func TestSetWeight_HonorsRouteSelector(t *testing.T) {
	vs := &unstructured.Unstructured{}
	vs.SetAPIVersion("networking.istio.io/v1beta1")
	vs.SetKind("VirtualService")
	vs.SetNamespace("default")
	vs.SetName("app")
	vs.Object["spec"] = map[string]any{
		"http": []any{
			map[string]any{
				"name": "primary",
				"route": []any{
					map[string]any{"destination": map[string]any{"host": "app-canary"}, "weight": int64(0)},
					map[string]any{"destination": map[string]any{"host": "app"}, "weight": int64(100)},
				},
			},
			map[string]any{
				"name": "other",
				"route": []any{
					map[string]any{"destination": map[string]any{"host": "unrelated-canary"}, "weight": int64(10)},
					map[string]any{"destination": map[string]any{"host": "unrelated"}, "weight": int64(90)},
				},
			},
		},
	}
	provider := newFakeProvider(t, vs)
	ro := rolloutWithIstio("app", "primary") // only "primary" named

	if err := provider.SetWeight(context.Background(), ro, 50); err != nil {
		t.Fatalf("SetWeight: %v", err)
	}

	got, _ := provider.dyn.Resource(virtualServiceGVR).Namespace("default").Get(context.Background(), "app", metav1.GetOptions{})
	http, _, _ := unstructured.NestedSlice(got.Object, "spec", "http")

	// Primary route: 50/50.
	primary := http[0].(map[string]any)
	primaryDests, _, _ := unstructured.NestedSlice(primary, "route")
	if w, _ := primaryDests[0].(map[string]any)["weight"].(int64); w != 50 {
		t.Errorf("primary canary weight: got %d, want 50", w)
	}

	// Other route: untouched at 10/90.
	other := http[1].(map[string]any)
	otherDests, _, _ := unstructured.NestedSlice(other, "route")
	if w, _ := otherDests[0].(map[string]any)["weight"].(int64); w != 10 {
		t.Errorf("other canary weight: got %d, want 10 (should be untouched)", w)
	}
}

// TestSetWeight_NoCanaryServices returns an actionable error rather than
// silently misrouting traffic when the rollout is underconfigured.
func TestSetWeight_NoCanaryServices(t *testing.T) {
	provider := newFakeProvider(t)
	ro := &rolloutsv1alpha1.Rollout{
		ObjectMeta: metav1.ObjectMeta{Name: "app", Namespace: "default"},
		Spec: rolloutsv1alpha1.RolloutSpec{
			StableServices: []rolloutsv1alpha1.ServiceRef{{Name: "app"}},
			TrafficRouting: &rolloutsv1alpha1.TrafficRoutingSpec{
				Provider: "istio",
				Istio:    &rolloutsv1alpha1.IstioConfig{VirtualServices: []rolloutsv1alpha1.IstioVirtualServiceRef{{Name: "app"}}},
			},
		},
	}
	err := provider.SetWeight(context.Background(), ro, 25)
	if err == nil {
		t.Fatalf("expected error when canaryServices is empty, got nil")
	}
}

// TestRewriteHTTPRoutes_SingleDestinationUntouched documents the conservative
// behavior for single-destination routes: the controller doesn't know which
// host is canary vs stable in that shape, so it leaves the route alone.
func TestRewriteHTTPRoutes_SingleDestinationUntouched(t *testing.T) {
	routes := []any{
		map[string]any{
			"name": "primary",
			"route": []any{
				map[string]any{"destination": map[string]any{"host": "only"}, "weight": int64(100)},
			},
		},
	}
	out, err := rewriteHTTPRoutes(routes, func(string) bool { return true }, "app-canary", "app", 50)
	if err != nil {
		t.Fatalf("rewriteHTTPRoutes: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("route count changed: got %d, want 1", len(out))
	}
	dests, _, _ := unstructured.NestedSlice(out[0].(map[string]any), "route")
	if len(dests) != 1 {
		t.Errorf("single-destination route was rewritten; got %d destinations, want 1", len(dests))
	}
}

// TestRouteSelector_EmptyMatchesAll documents the "empty list means all routes"
// shorthand so operators don't have to enumerate route names for the common
// single-route VirtualService case.
func TestRouteSelector_EmptyMatchesAll(t *testing.T) {
	m := routeSelector(nil)
	if !m("anything") {
		t.Errorf("empty selector rejected name %q; expected match-all", "anything")
	}
	if !m("") {
		t.Errorf("empty selector rejected empty name; expected match-all")
	}
}

// TestRouteSelector_Named confirms the positive and negative match paths for
// an explicit name list.
func TestRouteSelector_Named(t *testing.T) {
	m := routeSelector([]string{"primary", "secondary"})
	if !m("primary") {
		t.Errorf("named selector rejected %q", "primary")
	}
	if m("other") {
		t.Errorf("named selector matched unlisted %q", "other")
	}
}

// TestRemoveManagedRoutes resets the canary weight to zero — the exit path
// used on rollout completion or abort.
func TestRemoveManagedRoutes(t *testing.T) {
	initial := makeVirtualService("default", "app", "app-canary", "app", 50)
	provider := newFakeProvider(t, initial)
	ro := rolloutWithIstio("app", "primary")

	if err := provider.RemoveManagedRoutes(context.Background(), ro); err != nil {
		t.Fatalf("RemoveManagedRoutes: %v", err)
	}

	got, _ := provider.dyn.Resource(virtualServiceGVR).Namespace("default").Get(context.Background(), "app", metav1.GetOptions{})
	http, _, _ := unstructured.NestedSlice(got.Object, "spec", "http")
	dests, _, _ := unstructured.NestedSlice(http[0].(map[string]any), "route")
	if w, _ := dests[0].(map[string]any)["weight"].(int64); w != 0 {
		t.Errorf("canary weight after RemoveManagedRoutes: got %d, want 0", w)
	}
	if w, _ := dests[1].(map[string]any)["weight"].(int64); w != 100 {
		t.Errorf("stable weight after RemoveManagedRoutes: got %d, want 100", w)
	}
}
