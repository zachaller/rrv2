/*
Copyright 2026 The Rollouts Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0
*/

package rollout

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	clientset "k8s.io/client-go/kubernetes"
	appsv1listers "k8s.io/client-go/listers/apps/v1"
)

// clientsetRSLister is a thin ReplicaSetLister implementation backed by a
// clientset instead of an informer cache. The forked deployment controller
// only reads through the lister on one code path (post-create fallback when
// a pod-template-hash collision is detected), so an uncached impl is fine
// for the Rollouts reconciler — controller-runtime's own cache fronts the
// common read paths.
type clientsetRSLister struct{ kube clientset.Interface }

func newClientsetRSLister(kube clientset.Interface) appsv1listers.ReplicaSetLister {
	return &clientsetRSLister{kube: kube}
}

func (l *clientsetRSLister) List(selector labels.Selector) ([]*appsv1.ReplicaSet, error) {
	// Cluster-wide list is not used by the forked code but the interface
	// requires it; return an error rather than a silent empty list.
	return l.ReplicaSets("").List(selector)
}

func (l *clientsetRSLister) ReplicaSets(namespace string) appsv1listers.ReplicaSetNamespaceLister {
	return &clientsetRSNamespaceLister{kube: l.kube, namespace: namespace}
}

// GetPodReplicaSets is unused by any caller in the forked code path; we
// implement it to satisfy the interface but it returns nil.
func (l *clientsetRSLister) GetPodReplicaSets(pod *corev1.Pod) ([]*appsv1.ReplicaSet, error) {
	return nil, nil
}

type clientsetRSNamespaceLister struct {
	kube      clientset.Interface
	namespace string
}

func (l *clientsetRSNamespaceLister) List(selector labels.Selector) ([]*appsv1.ReplicaSet, error) {
	list, err := l.kube.AppsV1().ReplicaSets(l.namespace).List(context.Background(), metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return nil, err
	}
	out := make([]*appsv1.ReplicaSet, 0, len(list.Items))
	for i := range list.Items {
		out = append(out, &list.Items[i])
	}
	return out, nil
}

func (l *clientsetRSNamespaceLister) Get(name string) (*appsv1.ReplicaSet, error) {
	return l.kube.AppsV1().ReplicaSets(l.namespace).Get(context.Background(), name, metav1.GetOptions{})
}
