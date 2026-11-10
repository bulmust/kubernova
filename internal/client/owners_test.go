package client

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	"kubernova/internal/model"
)

type fakeOwnerGetter map[string][]metav1.OwnerReference

func (f fakeOwnerGetter) OwnerReferences(_ context.Context, _ string, ref metav1.OwnerReference) ([]metav1.OwnerReference, error) {
	return f[ref.Kind+"/"+ref.Name], nil
}

func TestOwnerResolverFollowsToDeployment(t *testing.T) {
	resolver := &ownerResolver{
		getter: fakeOwnerGetter{
			"ReplicaSet/api-abc": {
				{Kind: "Deployment", Name: "api"},
			},
		},
		cache: make(map[string]ownerInfo),
	}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "api-abc", APIVersion: "apps/v1"},
			},
		},
	}

	kind, name := resolver.ResolvePod(context.Background(), pod)
	if kind != "Deployment" || name != "api" {
		t.Errorf("ResolvePod() = %q/%q, want Deployment/api", kind, name)
	}
}

func TestOwnerResolverIncludesCustomResource(t *testing.T) {
	resolver := &ownerResolver{
		getter: fakeOwnerGetter{
			"StatefulSet/postgres": {
				{Kind: "Cluster", Name: "postgres-cluster", APIVersion: "postgresql.cnpg.io/v1"},
			},
		},
		cache: make(map[string]ownerInfo),
	}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "db",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "postgres", APIVersion: "apps/v1"},
			},
		},
	}

	kind, name := resolver.ResolvePod(context.Background(), pod)
	if kind != "Cluster" || name != "postgres-cluster" {
		t.Errorf("ResolvePod() = %q/%q, want Cluster/postgres-cluster", kind, name)
	}
}

func TestOwnerResolverFollowsReplicaSetWithoutDiscovery(t *testing.T) {
	replicaSet := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "assetclient-9494cbb8b",
			Namespace: "quattro-devops-apps",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "Deployment",
					Name:       "assetclient",
					Controller: boolPtr(true),
				},
			},
		},
	}
	clientset := fake.NewSimpleClientset(replicaSet)
	resolver := &ownerResolver{
		getter: newTypedOwnerGetter(clientset, 0),
		cache:  make(map[string]ownerInfo),
	}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "quattro-devops-apps",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "apps/v1",
					Kind:       "ReplicaSet",
					Name:       "assetclient-9494cbb8b",
					Controller: boolPtr(true),
				},
			},
		},
	}

	kind, name := resolver.ResolvePod(context.Background(), pod)
	if kind != "Deployment" || name != "assetclient" {
		t.Errorf("ResolvePod() = %q/%q, want Deployment/assetclient", kind, name)
	}
}

func boolPtr(value bool) *bool {
	return &value
}

func TestOwnerResolverStopsAtCustomResource(t *testing.T) {
	resolver := &ownerResolver{
		getter: fakeOwnerGetter{
			"InstanceManager/app": {
				{Kind: "Node", Name: "worker-1", APIVersion: "v1"},
			},
		},
		cache: make(map[string]ownerInfo),
	}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "InstanceManager", Name: "app", APIVersion: "multigres.com/v1alpha1"},
			},
		},
	}

	kind, name := resolver.ResolvePod(context.Background(), pod)
	if kind != "InstanceManager" || name != "app" {
		t.Errorf("ResolvePod() = %q/%q, want InstanceManager/app", kind, name)
	}
}

func TestOwnerResolverPrefersNonNodeOwner(t *testing.T) {
	resolver := &ownerResolver{cache: make(map[string]ownerInfo)}

	pod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Node", Name: "worker-1", APIVersion: "v1"},
				{Kind: "InstanceManager", Name: "app", APIVersion: "multigres.com/v1alpha1"},
			},
		},
	}

	kind, name := resolver.ResolvePod(context.Background(), pod)
	if kind != "InstanceManager" || name != "app" {
		t.Errorf("ResolvePod() = %q/%q, want InstanceManager/app", kind, name)
	}
}

func TestOwnerResolverWithoutOwner(t *testing.T) {
	resolver := &ownerResolver{cache: make(map[string]ownerInfo)}

	kind, name := resolver.ResolvePod(context.Background(), corev1.Pod{})
	if kind != "none" || name != "" {
		t.Errorf("ResolvePod() = %q/%q, want none/empty", kind, name)
	}
}

func TestOwnerResolverReturnsTopmostOwnerUID(t *testing.T) {
	resolver := &ownerResolver{
		getter: fakeOwnerGetter{
			"ReplicaSet/api-abc": {
				{Kind: "Deployment", Name: "api", UID: "deployment-uid", APIVersion: "apps/v1"},
			},
		},
		cache: make(map[string]ownerInfo),
	}
	pod := corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace: "default",
		OwnerReferences: []metav1.OwnerReference{
			{Kind: "ReplicaSet", Name: "api-abc", UID: "replicaset-uid", APIVersion: "apps/v1"},
		},
	}}
	info := resolver.ResolvePodInfo(context.Background(), pod)
	if info.kind != "Deployment" || info.name != "api" || info.uid != "deployment-uid" {
		t.Errorf("ResolvePodInfo() = %#v, want Deployment/api/deployment-uid", info)
	}
	if len(info.chain) != 2 || info.chain[0].Kind != "ReplicaSet" || info.chain[1].Kind != "Deployment" {
		t.Errorf("ownership chain = %#v, want ReplicaSet -> Deployment", info.chain)
	}
}

func TestOwnershipTreeIsHierarchical(t *testing.T) {
	chain := []model.OwnerReference{
		{Kind: "ReplicaSet", Name: "api-abc", UID: "rs"},
		{Kind: "Deployment", Name: "api", UID: "deploy"},
	}
	tree := ownershipTree(chain)
	if tree == nil || tree.Kind != "ReplicaSet" || tree.Owner == nil || tree.Owner.Kind != "Deployment" {
		t.Fatalf("ownershipTree() = %#v, want ReplicaSet -> Deployment", tree)
	}
}

func TestReplicaStatus(t *testing.T) {
	desired := int32(3)
	if got := replicaStatus(&desired, 2); got != "3" {
		t.Errorf("replicaStatus() = %q, want 3", got)
	}
	if got := replicaStatus(nil, 0); got != "-" {
		t.Errorf("replicaStatus(nil, 0) = %q, want -", got)
	}
	if got := replicaStatusInt32(4, 3); got != "4" {
		t.Errorf("replicaStatusInt32() = %q, want 4", got)
	}
}
