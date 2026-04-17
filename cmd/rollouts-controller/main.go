/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

// Command rollouts-controller is the Rollouts reconciler entrypoint. It runs
// under controller-runtime's Manager: leader election, shared informers,
// /metrics + /healthz, and graceful shutdown on SIGTERM.
package main

import (
	"flag"
	"fmt"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	rolloutsv1alpha1 "github.com/zachaller/rrv2/pkg/apis/rollouts/v1alpha1"
	rolloutcontroller "github.com/zachaller/rrv2/pkg/controller/rollout"

	// Provider registrations — each init() registers its factory with
	// trafficrouting.Global. Import order doesn't matter; the controller
	// resolves by string discriminator at reconcile time.
	_ "github.com/zachaller/rrv2/pkg/trafficrouting/alb"
	_ "github.com/zachaller/rrv2/pkg/trafficrouting/apisix"
	_ "github.com/zachaller/rrv2/pkg/trafficrouting/istio"
	_ "github.com/zachaller/rrv2/pkg/trafficrouting/nginx"
	_ "github.com/zachaller/rrv2/pkg/trafficrouting/smi"
	_ "github.com/zachaller/rrv2/pkg/trafficrouting/traefik"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(rolloutsv1alpha1.AddToScheme(scheme))
}

func main() {
	var (
		metricsAddr          string
		probeAddr            string
		enableLeaderElection bool
		leaderElectionID     string
	)

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "Prometheus metrics endpoint binding address. Use 0 to disable.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "/healthz + /readyz binding address.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", true, "Elect a leader among controller replicas.")
	flag.StringVar(&leaderElectionID, "leader-election-id", "rollouts.io", "Lease name for leader election.")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	logger := zap.New(zap.UseFlagOptions(&opts))
	ctrl.SetLogger(logger)
	klog.SetLogger(logger)

	cfg, err := ctrl.GetConfig()
	if err != nil {
		fatalf("get kubeconfig: %v", err)
	}

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsserver.Options{BindAddress: metricsAddr},
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       leaderElectionID,
	})
	if err != nil {
		fatalf("create manager: %v", err)
	}

	reconciler := &rolloutcontroller.Reconciler{}
	if err := reconciler.SetupWithManager(mgr); err != nil {
		fatalf("setup rollout controller: %v", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		fatalf("add healthz: %v", err)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		fatalf("add readyz: %v", err)
	}

	klog.InfoS("starting rollouts-controller", "leaderElection", enableLeaderElection)
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		fatalf("manager exited: %v", err)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
