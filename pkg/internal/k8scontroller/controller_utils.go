/*
Copyright 2014 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package k8scontroller is a narrow vendor of the subset of
// k8s.io/kubernetes/pkg/controller used by the forked Deployment controller.
//
// Only the symbols referenced by pkg/controller/rollout/_forkedfrom_k8s/ are
// kept; unrelated helpers (node taints, pod control, expectations, etc.) are
// omitted. The upstream hash utility has been inlined to avoid pulling in
// k8s.io/kubernetes/pkg/util/hash.
package k8scontroller

import (
	"context"
	"encoding/binary"
	"fmt"
	"hash/fnv"

	apps "k8s.io/api/apps/v1"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/dump"
	"k8s.io/apimachinery/pkg/util/rand"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/record"
)

// KeyFunc is the canonical controller workqueue key function.
var KeyFunc = cache.DeletionHandlingMetaNamespaceKeyFunc

// Pod event reasons emitted by the deployment controller.
const (
	FailedCreatePodReason     = "FailedCreate"
	SuccessfulCreatePodReason = "SuccessfulCreate"
	FailedDeletePodReason     = "FailedDelete"
	SuccessfulDeletePodReason = "SuccessfulDelete"
)

// RSControlInterface abstracts ReplicaSet patching for testability.
type RSControlInterface interface {
	PatchReplicaSet(ctx context.Context, namespace, name string, data []byte) error
}

// RealRSControl is the production implementation of RSControlInterface.
type RealRSControl struct {
	KubeClient clientset.Interface
	Recorder   record.EventRecorder
}

var _ RSControlInterface = &RealRSControl{}

func (r RealRSControl) PatchReplicaSet(ctx context.Context, namespace, name string, data []byte) error {
	_, err := r.KubeClient.AppsV1().ReplicaSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, data, metav1.PatchOptions{})
	return err
}

// FilterActiveReplicaSets returns replica sets that have (or ought to have) pods.
func FilterActiveReplicaSets(replicaSets []*apps.ReplicaSet) []*apps.ReplicaSet {
	return FilterReplicaSets(replicaSets, func(rs *apps.ReplicaSet) bool {
		return rs != nil && rs.Spec.Replicas != nil && *rs.Spec.Replicas > 0
	})
}

type filterRS func(rs *apps.ReplicaSet) bool

// FilterReplicaSets returns replica sets that match filterFn.
func FilterReplicaSets(rses []*apps.ReplicaSet, filterFn filterRS) []*apps.ReplicaSet {
	var filtered []*apps.ReplicaSet
	for i := range rses {
		if filterFn(rses[i]) {
			filtered = append(filtered, rses[i])
		}
	}
	return filtered
}

// ReplicaSetsByCreationTimestamp sorts ReplicaSets oldest first, name as tiebreaker.
type ReplicaSetsByCreationTimestamp []*apps.ReplicaSet

func (o ReplicaSetsByCreationTimestamp) Len() int      { return len(o) }
func (o ReplicaSetsByCreationTimestamp) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsByCreationTimestamp) Less(i, j int) bool {
	if o[i].CreationTimestamp.Equal(&o[j].CreationTimestamp) {
		return o[i].Name < o[j].Name
	}
	return o[i].CreationTimestamp.Before(&o[j].CreationTimestamp)
}

// ReplicaSetsBySizeOlder sorts by size descending, creation time ascending as tiebreaker.
type ReplicaSetsBySizeOlder []*apps.ReplicaSet

func (o ReplicaSetsBySizeOlder) Len() int      { return len(o) }
func (o ReplicaSetsBySizeOlder) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsBySizeOlder) Less(i, j int) bool {
	if *o[i].Spec.Replicas == *o[j].Spec.Replicas {
		return ReplicaSetsByCreationTimestamp(o).Less(i, j)
	}
	return *o[i].Spec.Replicas > *o[j].Spec.Replicas
}

// ReplicaSetsBySizeNewer sorts by size descending, creation time descending as tiebreaker.
type ReplicaSetsBySizeNewer []*apps.ReplicaSet

func (o ReplicaSetsBySizeNewer) Len() int      { return len(o) }
func (o ReplicaSetsBySizeNewer) Swap(i, j int) { o[i], o[j] = o[j], o[i] }
func (o ReplicaSetsBySizeNewer) Less(i, j int) bool {
	if *o[i].Spec.Replicas == *o[j].Spec.Replicas {
		return ReplicaSetsByCreationTimestamp(o).Less(j, i)
	}
	return *o[i].Spec.Replicas > *o[j].Spec.Replicas
}

// ComputeHash returns a deterministic FNV-1a hash of a pod template, optionally
// salted with a collision counter. Inlined from k8s.io/kubernetes/pkg/util/hash
// to avoid the internal dependency.
func ComputeHash(template *v1.PodTemplateSpec, collisionCount *int32) string {
	hasher := fnv.New32a()
	hasher.Reset()
	fmt.Fprintf(hasher, "%v", dump.ForHash(*template))

	if collisionCount != nil {
		buf := make([]byte, 8)
		binary.LittleEndian.PutUint32(buf, uint32(*collisionCount))
		hasher.Write(buf)
	}

	return rand.SafeEncodeString(fmt.Sprint(hasher.Sum32()))
}
