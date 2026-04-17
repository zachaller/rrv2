/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Package smi implements the Service Mesh Interface TrafficSplit provider.
package smi

import (
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"

	"github.com/zachaller/rrv2/pkg/trafficrouting"
)

const Type = "smi"

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
