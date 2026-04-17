/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package nginx implements the Nginx Ingress traffic-routing provider.
// The stub registers itself and returns unimplemented errors — real
// implementation copies the primary Ingress and layers canary annotations.
package nginx

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/zachaller/rrv2/pkg/trafficrouting"
)

// Type is the discriminator string matched against trafficRouting.provider.
const Type = "nginx"

// Provider is the Nginx implementation.
type Provider struct {
	trafficrouting.Unimplemented
	kube kubernetes.Interface
	dyn  dynamic.Interface
}

// New constructs a Provider.
func New(kube kubernetes.Interface, dyn dynamic.Interface) trafficrouting.Plugin {
	return &Provider{
		Unimplemented: trafficrouting.Unimplemented{ProviderType: Type},
		kube:          kube,
		dyn:           dyn,
	}
}

func init() { trafficrouting.Register(Type, New) }
