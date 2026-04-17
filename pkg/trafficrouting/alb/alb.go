/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package alb implements the AWS Load Balancer Controller provider.
// Stub — the real implementation writes alb.ingress.kubernetes.io/actions.*
// annotations to shape weighted target-group traffic.
package alb

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/zaller/rollouts/pkg/trafficrouting"
)

const Type = "alb"

type Provider struct {
	trafficrouting.Unimplemented
	kube kubernetes.Interface
	dyn  dynamic.Interface
}

func New(kube kubernetes.Interface, dyn dynamic.Interface) trafficrouting.Plugin {
	return &Provider{
		Unimplemented: trafficrouting.Unimplemented{ProviderType: Type},
		kube:          kube,
		dyn:           dyn,
	}
}

func init() { trafficrouting.Register(Type, New) }
