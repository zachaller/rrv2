//go:build tools
// +build tools

// Package tools pins build-time tool dependencies so `go mod tidy` keeps them.
package tools

import (
	_ "k8s.io/code-generator"
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
