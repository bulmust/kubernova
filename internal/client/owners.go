package client

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"kubernova/internal/model"
)

type ownerInfo struct {
	kind     string
	name     string
	uid      string
	replicas string
	chain    []model.OwnerReference
}

type ownerGetter interface {
	OwnerReferences(ctx context.Context, namespace string, ref metav1.OwnerReference) ([]metav1.OwnerReference, error)
}

type replicasGetter interface {
	Replicas(ctx context.Context, namespace string, ref metav1.OwnerReference) (string, error)
}

// typedOwnerGetter resolves built-in workload owners. Controllers whose objects
// are prefetched are resolved in memory; everything else falls back to a GET.
type typedOwnerGetter struct {
	client     kubernetes.Interface
	pageSize   int
	prefetched map[string][]metav1.OwnerReference
	replicas   map[string]string
}

func newTypedOwnerGetter(client kubernetes.Interface, pageSize int) *typedOwnerGetter {
	return &typedOwnerGetter{
		client:     client,
		pageSize:   pageSize,
		prefetched: make(map[string][]metav1.OwnerReference),
		replicas:   make(map[string]string),
	}
}

func (g *typedOwnerGetter) OwnerReferences(ctx context.Context, namespace string, ref metav1.OwnerReference) ([]metav1.OwnerReference, error) {
	if refs, ok := g.prefetched[lookupKey(namespace, ref.Kind, ref.Name)]; ok {
		return refs, nil
	}

	switch ref.Kind {
	case "ReplicaSet":
		object, err := g.client.AppsV1().ReplicaSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return object.OwnerReferences, nil
	case "Deployment":
		object, err := g.client.AppsV1().Deployments(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return object.OwnerReferences, nil
	case "StatefulSet":
		object, err := g.client.AppsV1().StatefulSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return object.OwnerReferences, nil
	case "DaemonSet":
		object, err := g.client.AppsV1().DaemonSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return object.OwnerReferences, nil
	case "ReplicationController":
		object, err := g.client.CoreV1().ReplicationControllers(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return nil, err
		}
		return object.OwnerReferences, nil
	default:
		return nil, fmt.Errorf("unsupported built-in owner kind %q", ref.Kind)
	}
}

// Replicas returns the desired replica count for a built-in workload owner.
// Prefetched values are used when available.
func (g *typedOwnerGetter) Replicas(ctx context.Context, namespace string, ref metav1.OwnerReference) (string, error) {
	key := lookupKey(namespace, ref.Kind, ref.Name)
	if value, ok := g.replicas[key]; ok {
		return value, nil
	}
	switch ref.Kind {
	case "ReplicaSet":
		object, err := g.client.AppsV1().ReplicaSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return replicaStatus(object.Spec.Replicas, object.Status.ReadyReplicas), nil
	case "Deployment":
		object, err := g.client.AppsV1().Deployments(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return replicaStatus(object.Spec.Replicas, object.Status.ReadyReplicas), nil
	case "StatefulSet":
		object, err := g.client.AppsV1().StatefulSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return replicaStatus(object.Spec.Replicas, object.Status.ReadyReplicas), nil
	case "DaemonSet":
		object, err := g.client.AppsV1().DaemonSets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return replicaStatusInt32(object.Status.DesiredNumberScheduled, object.Status.NumberReady), nil
	case "ReplicationController":
		object, err := g.client.CoreV1().ReplicationControllers(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return replicaStatus(object.Spec.Replicas, object.Status.ReadyReplicas), nil
	default:
		return "", nil
	}
}

type ownerListItem struct {
	namespace string
	name      string
	refs      []metav1.OwnerReference
	replicas  string
}

// Prefetch loads all built-in workload controllers once so owner chains can be
// resolved without one API request per owner. Each list follows the API
// server's pagination and respects the configured page size.
func (g *typedOwnerGetter) Prefetch(ctx context.Context) {
	g.prefetchKind(ctx, "ReplicaSet", func(options metav1.ListOptions) ([]ownerListItem, string, error) {
		list, err := g.client.AppsV1().ReplicaSets(metav1.NamespaceAll).List(ctx, options)
		if err != nil {
			return nil, "", err
		}
		items := make([]ownerListItem, 0, len(list.Items))
		for index := range list.Items {
			item := &list.Items[index]
			items = append(items, ownerListItem{item.Namespace, item.Name, item.OwnerReferences, replicaStatus(item.Spec.Replicas, item.Status.ReadyReplicas)})
		}
		return items, list.Continue, nil
	})
	g.prefetchKind(ctx, "Deployment", func(options metav1.ListOptions) ([]ownerListItem, string, error) {
		list, err := g.client.AppsV1().Deployments(metav1.NamespaceAll).List(ctx, options)
		if err != nil {
			return nil, "", err
		}
		items := make([]ownerListItem, 0, len(list.Items))
		for index := range list.Items {
			item := &list.Items[index]
			items = append(items, ownerListItem{item.Namespace, item.Name, item.OwnerReferences, replicaStatus(item.Spec.Replicas, item.Status.ReadyReplicas)})
		}
		return items, list.Continue, nil
	})
	g.prefetchKind(ctx, "StatefulSet", func(options metav1.ListOptions) ([]ownerListItem, string, error) {
		list, err := g.client.AppsV1().StatefulSets(metav1.NamespaceAll).List(ctx, options)
		if err != nil {
			return nil, "", err
		}
		items := make([]ownerListItem, 0, len(list.Items))
		for index := range list.Items {
			item := &list.Items[index]
			items = append(items, ownerListItem{item.Namespace, item.Name, item.OwnerReferences, replicaStatus(item.Spec.Replicas, item.Status.ReadyReplicas)})
		}
		return items, list.Continue, nil
	})
	g.prefetchKind(ctx, "DaemonSet", func(options metav1.ListOptions) ([]ownerListItem, string, error) {
		list, err := g.client.AppsV1().DaemonSets(metav1.NamespaceAll).List(ctx, options)
		if err != nil {
			return nil, "", err
		}
		items := make([]ownerListItem, 0, len(list.Items))
		for index := range list.Items {
			item := &list.Items[index]
			items = append(items, ownerListItem{item.Namespace, item.Name, item.OwnerReferences, replicaStatusInt32(item.Status.DesiredNumberScheduled, item.Status.NumberReady)})
		}
		return items, list.Continue, nil
	})
	g.prefetchKind(ctx, "ReplicationController", func(options metav1.ListOptions) ([]ownerListItem, string, error) {
		list, err := g.client.CoreV1().ReplicationControllers(metav1.NamespaceAll).List(ctx, options)
		if err != nil {
			return nil, "", err
		}
		items := make([]ownerListItem, 0, len(list.Items))
		for index := range list.Items {
			item := &list.Items[index]
			items = append(items, ownerListItem{item.Namespace, item.Name, item.OwnerReferences, replicaStatus(item.Spec.Replicas, item.Status.ReadyReplicas)})
		}
		return items, list.Continue, nil
	})
}

func (g *typedOwnerGetter) prefetchKind(
	ctx context.Context,
	kind string,
	listPage func(metav1.ListOptions) ([]ownerListItem, string, error),
) {
	options := metav1.ListOptions{Limit: int64(g.pageSize)}
	for {
		items, continueToken, err := listPage(options)
		if err != nil {
			return
		}
		for _, item := range items {
			key := lookupKey(item.namespace, kind, item.name)
			g.prefetched[key] = item.refs
			g.replicas[key] = item.replicas
		}
		if continueToken == "" {
			return
		}
		options.Continue = continueToken
	}
}

func lookupKey(namespace, kind, name string) string {
	return namespace + "/" + kind + "/" + name
}

type ownerResolver struct {
	getter ownerGetter
	cache  map[string]ownerInfo
}

func newOwnerResolver(client kubernetes.Interface, pageSize int) *ownerResolver {
	return &ownerResolver{
		getter: newTypedOwnerGetter(client, pageSize),
		cache:  make(map[string]ownerInfo),
	}
}

// Prefetch loads owner data for built-in controllers in bulk.
func (r *ownerResolver) Prefetch(ctx context.Context) {
	if getter, ok := r.getter.(*typedOwnerGetter); ok {
		getter.Prefetch(ctx)
	}
}

// ResolvePod follows the pod's owner chain as far as possible and returns the
// topmost owner kind and name. Built-in controllers such as ReplicaSet are
// followed to their controlling workload, while custom resources are shown as
// the final owner instead of continuing into infrastructure objects like Node.
func (r *ownerResolver) ResolvePod(ctx context.Context, pod corev1.Pod) (string, string) {
	info := r.ResolvePodInfo(ctx, pod)
	return info.kind, info.name
}

// ResolvePodInfo follows the pod's owner chain and also returns the topmost
// owner UID for the hidden OWNER UID column.
func (r *ownerResolver) ResolvePodInfo(ctx context.Context, pod corev1.Pod) ownerInfo {
	ref := preferredOwnerReference(pod.OwnerReferences)
	if ref == nil {
		return ownerInfo{kind: "none"}
	}
	return r.resolve(ctx, pod.Namespace, *ref)
}

func (r *ownerResolver) resolve(ctx context.Context, namespace string, ref metav1.OwnerReference) ownerInfo {
	key := ownerKey(namespace, ref)
	if info, ok := r.cache[key]; ok {
		return info
	}

	kind, name, uid := ref.Kind, ref.Name, string(ref.UID)
	references := []metav1.OwnerReference{ref}
	current := ref
	visited := make(map[string]bool)
	for r.getter != nil && traceableOwnerKind(current.Kind) {
		currentKey := ownerKey(namespace, current)
		if visited[currentKey] {
			break
		}
		visited[currentKey] = true

		refs, err := r.getter.OwnerReferences(ctx, namespace, current)
		if err != nil {
			break
		}
		next := preferredOwnerReference(refs)
		if next == nil {
			break
		}
		kind, name, uid = next.Kind, next.Name, string(next.UID)
		references = append(references, *next)
		current = *next
	}

	chain := make([]model.OwnerReference, 0, len(references))
	replicas := "-"
	for _, reference := range references {
		node := ownerReference(reference)
		if getter, ok := r.getter.(replicasGetter); ok {
			if value, err := getter.Replicas(ctx, namespace, reference); err == nil && value != "" {
				node.Replicas = value
			}
		}
		chain = append(chain, node)
	}
	for index := len(chain) - 1; index >= 0; index-- {
		if chain[index].Replicas != "" && chain[index].Replicas != "-" {
			replicas = chain[index].Replicas
			break
		}
	}

	info := ownerInfo{kind: kind, name: name, uid: uid, replicas: replicas, chain: chain}
	r.cache[key] = info
	return info
}

func ownerReference(ref metav1.OwnerReference) model.OwnerReference {
	return model.OwnerReference{
		APIVersion: ref.APIVersion,
		Kind:       ref.Kind,
		Name:       ref.Name,
		UID:        string(ref.UID),
	}
}

// ownershipTree turns a direct-owner-first chain into nested owner metadata:
// ReplicaSet -> owner Deployment -> owner ...
func ownershipTree(chain []model.OwnerReference) *model.OwnerReference {
	var root *model.OwnerReference
	for index := len(chain) - 1; index >= 0; index-- {
		node := chain[index]
		node.Owner = root
		root = &node
	}
	return root
}

func replicaStatus(desired *int32, ready int32) string {
	if desired == nil {
		return "-"
	}
	return fmt.Sprintf("%d", *desired)
}

func replicaStatusInt32(desired, ready int32) string {
	if desired == 0 && ready == 0 {
		return "-"
	}
	return fmt.Sprintf("%d", desired)
}

// traceableOwnerKind limits owner-chain traversal to workload controllers.
// This keeps pod -> ReplicaSet -> Deployment resolution without climbing into
// custom-resource or infrastructure owners such as Node.
func traceableOwnerKind(kind string) bool {
	switch kind {
	case "ReplicaSet", "Deployment", "StatefulSet", "DaemonSet", "ReplicationController":
		return true
	default:
		return false
	}
}

// preferredOwnerReference chooses the controlling owner when possible. If
// multiple refs exist without a controller, infrastructure owners like Node are
// deprioritized so the workload owner is shown instead.
func preferredOwnerReference(refs []metav1.OwnerReference) *metav1.OwnerReference {
	var fallback *metav1.OwnerReference
	var nodeFallback *metav1.OwnerReference
	for index := range refs {
		ref := &refs[index]
		if ref.Kind == "" {
			continue
		}
		if nodeFallback == nil && ref.Kind == "Node" {
			nodeFallback = ref
		}
		if ref.Controller != nil && *ref.Controller && ref.Kind != "Node" {
			return ref
		}
		if fallback == nil && ref.Kind != "Node" {
			fallback = ref
		}
	}
	if fallback != nil {
		return fallback
	}
	if nodeFallback != nil {
		return nodeFallback
	}
	if len(refs) > 0 {
		return &refs[0]
	}
	return nil
}

func ownerKey(namespace string, ref metav1.OwnerReference) string {
	return namespace + "/" + ref.APIVersion + "/" + ref.Kind + "/" + ref.Name
}
