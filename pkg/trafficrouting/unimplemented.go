/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package trafficrouting

import (
	"context"
	"fmt"

	rolloutsv1alpha1 "github.com/zaller/rollouts/pkg/apis/rollouts/v1alpha1"
)

// Unimplemented is a convenient zero value for stub providers — every method
// returns an error naming the provider. Embed it and override the methods you
// actually support.
type Unimplemented struct {
	ProviderType string
}

func (u Unimplemented) Type() string { return u.ProviderType }

func (u Unimplemented) SetWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, desiredWeight int32) error {
	return fmt.Errorf("trafficrouting/%s: SetWeight not implemented", u.ProviderType)
}

func (u Unimplemented) SetHeaderRoute(ctx context.Context, ro *rolloutsv1alpha1.Rollout, match []rolloutsv1alpha1.RouteMatch) error {
	return fmt.Errorf("trafficrouting/%s: SetHeaderRoute not implemented", u.ProviderType)
}

func (u Unimplemented) VerifyWeight(ctx context.Context, ro *rolloutsv1alpha1.Rollout, desiredWeight int32) (bool, error) {
	return false, fmt.Errorf("trafficrouting/%s: VerifyWeight not implemented", u.ProviderType)
}

func (u Unimplemented) UpdateHosts(ctx context.Context, ro *rolloutsv1alpha1.Rollout, canarySvc, stableSvc string) error {
	return fmt.Errorf("trafficrouting/%s: UpdateHosts not implemented", u.ProviderType)
}

func (u Unimplemented) RemoveManagedRoutes(ctx context.Context, ro *rolloutsv1alpha1.Rollout) error {
	return nil
}
