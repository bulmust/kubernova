// Package client loads kubeconfig data and talks to the Kubernetes API.
package client

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/klog/v2"

	"kubernova/internal/model"
)

// ConnectTimeout is the maximum time allowed to reach the Kubernetes API.
const ConnectTimeout = 7 * time.Second

// ListTimeout is the maximum time allowed to list pods.
const ListTimeout = 30 * time.Second

// SilenceKlog stops client-go from writing log lines, such as API deprecation
// warnings, to stderr where they would corrupt the terminal UI.
func SilenceKlog() {
	klog.LogToStderr(false)
	klog.SetOutput(io.Discard)
}

// Options configures Kubernetes API requests.
type Options struct {
	// RequestTimeout limits each individual API request. Zero leaves the
	// timeout to the client-go defaults.
	RequestTimeout time.Duration

	// PageSize limits how many resources are retrieved per API request. Zero
	// lets the API server choose.
	PageSize int
}

// Kubeconfig describes the kubeconfig currently used by the app.
type Kubeconfig struct {
	Path          string
	Context       string
	Cluster       string
	User          string
	ServerVersion string
	Contexts      []string
	Clusters      []string

	restConfig *rest.Config
	options    Options
}

// LoadKubeconfig loads the kubeconfig used by the current shell. If path is
// empty, the KUBECONFIG environment variable and the default ~/.kube/config
// location are used.
func LoadKubeconfig(path string) (*Kubeconfig, error) {
	return LoadKubeconfigWithOptions(path, Options{})
}

// LoadKubeconfigWithOptions loads a kubeconfig and applies the request
// options used by subsequent Kubernetes API calls.
func LoadKubeconfigWithOptions(path string, options Options) (*Kubeconfig, error) {
	return loadKubeconfig(path, "", options)
}

// LoadKubeconfigWithContext loads a kubeconfig and selects a specific context
// instead of the kubeconfig's current context. An empty context keeps the
// existing current-context behavior.
func LoadKubeconfigWithContext(path, contextName string, options Options) (*Kubeconfig, error) {
	return loadKubeconfig(path, contextName, options)
}

func loadKubeconfig(path, contextName string, options Options) (*Kubeconfig, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	if path != "" {
		path = ExpandPath(path)
		if _, err := os.Stat(path); err != nil {
			return nil, fmt.Errorf("read kubeconfig %q: %w", path, err)
		}
		rules.ExplicitPath = path
	}

	raw, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{}).RawConfig()
	if err != nil {
		return nil, fmt.Errorf("read kubeconfig %q: %w", rules.GetDefaultFilename(), err)
	}
	if len(raw.Contexts) == 0 {
		return nil, fmt.Errorf("kubeconfig %q contains no contexts", rules.GetDefaultFilename())
	}

	contexts := make([]string, 0, len(raw.Contexts))
	for name := range raw.Contexts {
		contexts = append(contexts, name)
	}
	sort.Strings(contexts)

	clusters := make([]string, 0, len(raw.Clusters))
	for name := range raw.Clusters {
		clusters = append(clusters, name)
	}
	sort.Strings(clusters)

	current := strings.TrimSpace(contextName)
	if current == "" {
		current = raw.CurrentContext
	}
	if current == "" {
		current = contexts[0]
	}
	namedContext, ok := raw.Contexts[current]
	if !ok {
		return nil, fmt.Errorf("context %q not found in kubeconfig", current)
	}

	restConfig, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules,
		&clientcmd.ConfigOverrides{CurrentContext: current},
	).ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("build client config for context %q: %w", current, err)
	}
	if options.RequestTimeout > 0 {
		restConfig.Timeout = options.RequestTimeout
	}

	return &Kubeconfig{
		Path:       rules.GetDefaultFilename(),
		Context:    current,
		Cluster:    namedContext.Cluster,
		User:       namedContext.AuthInfo,
		Contexts:   contexts,
		Clusters:   clusters,
		restConfig: restConfig,
		options:    options,
	}, nil
}

// CheckConnection verifies that the Kubernetes API for the current context is
// reachable within timeout.
func (k *Kubeconfig) CheckConnection(timeout time.Duration) error {
	if timeout <= 0 {
		timeout = ConnectTimeout
	}

	restConfig := rest.CopyConfig(k.restConfig)
	restConfig.Timeout = timeout

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("create Kubernetes client: %w", err)
	}
	version, err := clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("connect to cluster %q: %w", k.Cluster, err)
	}
	k.ServerVersion = version.GitVersion
	return nil
}

// ListContainers returns the containers of every pod in the cluster, excluding
// only active batch workloads, together with their images, owner kinds,
// resources, status, and OOM status.
func (k *Kubeconfig) ListContainers(ctx context.Context) ([]model.Container, error) {
	clientset, err := kubernetes.NewForConfig(k.restConfig)
	if err != nil {
		return nil, fmt.Errorf("create Kubernetes client: %w", err)
	}

	pods, err := k.listPods(ctx, clientset)
	if err != nil {
		return nil, fmt.Errorf("list pods in cluster %q: %w", k.Cluster, err)
	}

	containers := make([]model.Container, 0, len(pods))
	resolver := newOwnerResolver(clientset, k.options.PageSize)
	resolver.Prefetch(ctx)
	for _, pod := range pods {
		if isBatchPod(pod) {
			continue
		}

		owner := resolver.ResolvePodInfo(ctx, pod)
		owners, ownerName, ownerUID := owner.kind, owner.name, owner.uid
		replicas := owner.replicas
		if replicas == "" {
			replicas = "-"
		}
		ownership := ownershipTree(owner.chain)
		limitRangeSet := limitRangeContainers(pod.Annotations[limitRangerAnnotation])
		regularStatuses := containerStatuses(pod)
		initStatuses := initContainerStatuses(pod)

		appendContainer := func(container corev1.Container, statuses map[string]corev1.ContainerStatus) {
			cpuReq, cpuLim, memReq, memLim := containerResources(container)
			oom := "no"
			if status, ok := statuses[container.Name]; ok && oomKilled(status) {
				oom = "yes"
			}

			image := container.Image
			if image == "" {
				image = "-"
			}
			parsedImage := model.ParseImage(image)

			source := "Pod"
			if !containerHasResources(container) {
				source = "-"
			} else if limitRangeSet[container.Name] {
				source = "LimitRange"
			}

			var status *model.ContainerStatus
			if containerStatus, ok := statuses[container.Name]; ok {
				status = statusFromContainerStatus(containerStatus)
			}

			containers = append(containers, model.Container{
				Cluster:        k.Cluster,
				Namespace:      pod.Namespace,
				Pod:            pod.Name,
				Owners:         owners,
				OwnerName:      ownerName,
				ContainerUID:   string(pod.UID),
				OwnerUID:       ownerUID,
				Name:           container.Name,
				Image:          image,
				Registry:       parsedImage.Registry,
				Repository:     parsedImage.Repository,
				Tag:            parsedImage.Tag,
				ResourceSource: source,
				CPUReq:         cpuReq,
				CPULim:         cpuLim,
				MemReq:         memReq,
				MemLim:         memLim,
				OOM:            oom,
				Status:         status,
				Replicas:       replicas,
				Ownership:      ownership,
			})
		}

		for _, container := range pod.Spec.InitContainers {
			appendContainer(container, initStatuses)
		}
		for _, container := range pod.Spec.Containers {
			appendContainer(container, regularStatuses)
		}
	}

	sort.Slice(containers, func(i, j int) bool {
		if containers[i].Cluster != containers[j].Cluster {
			return containers[i].Cluster < containers[j].Cluster
		}
		if containers[i].Namespace != containers[j].Namespace {
			return containers[i].Namespace < containers[j].Namespace
		}
		if containers[i].Pod != containers[j].Pod {
			return containers[i].Pod < containers[j].Pod
		}
		return containers[i].Name < containers[j].Name
	})

	return containers, nil
}

// ListNodes returns non-secret node capacity metadata. RBAC failures are
// returned to the caller so the cache can record an unavailable inventory
// instead of failing the container refresh.
func (k *Kubeconfig) ListNodes(ctx context.Context) (NodeInventory, error) {
	clientset, err := kubernetes.NewForConfig(k.restConfig)
	if err != nil {
		return NodeInventory{Available: false, Reason: "n/a"}, fmt.Errorf("create Kubernetes client: %w", err)
	}

	inventory := NodeInventory{Available: true}
	options := metav1.ListOptions{Limit: int64(k.options.PageSize)}
	for {
		list, err := clientset.CoreV1().Nodes().List(ctx, options)
		if err != nil {
			return NodeInventory{Available: false, Reason: "n/a"}, err
		}
		for _, node := range list.Items {
			inventory.Items = append(inventory.Items, CachedNode{
				Name:              node.Name,
				CapacityCPU:       resourceString(node.Status.Capacity, corev1.ResourceCPU),
				CapacityMemory:    resourceString(node.Status.Capacity, corev1.ResourceMemory),
				AllocatableCPU:    resourceString(node.Status.Allocatable, corev1.ResourceCPU),
				AllocatableMemory: resourceString(node.Status.Allocatable, corev1.ResourceMemory),
			})
		}
		if list.Continue == "" {
			inventory.finalize()
			return inventory, nil
		}
		options.Continue = list.Continue
	}
}

func (inventory *NodeInventory) finalize() {
	inventory.TotalNodes = len(inventory.Items)
	inventory.TotalCPUCapacity = sumNodeQuantity(inventory.Items, func(node CachedNode) string { return node.CapacityCPU })
	inventory.TotalAllocatableCPU = sumNodeQuantity(inventory.Items, func(node CachedNode) string { return node.AllocatableCPU })
	inventory.TotalMemoryCapacity = sumNodeQuantity(inventory.Items, func(node CachedNode) string { return node.CapacityMemory })
	inventory.TotalAllocatableMemory = sumNodeQuantity(inventory.Items, func(node CachedNode) string { return node.AllocatableMemory })
}

func sumNodeQuantity(nodes []CachedNode, field func(CachedNode) string) string {
	total := resource.Quantity{}
	found := false
	for _, node := range nodes {
		value := strings.TrimSpace(field(node))
		if value == "" {
			continue
		}
		quantity, err := resource.ParseQuantity(value)
		if err != nil {
			continue
		}
		total.Add(quantity)
		found = true
	}
	if !found {
		return ""
	}
	return total.String()
}

// listPods retrieves every pod, following the API server's pagination tokens
// and limiting each request to the configured page size.
func (k *Kubeconfig) listPods(ctx context.Context, clientset kubernetes.Interface) ([]corev1.Pod, error) {
	var pods []corev1.Pod
	options := metav1.ListOptions{Limit: int64(k.options.PageSize)}
	for {
		list, err := clientset.CoreV1().Pods(metav1.NamespaceAll).List(ctx, options)
		if err != nil {
			return nil, err
		}
		pods = append(pods, list.Items...)
		if list.Continue == "" {
			return pods, nil
		}
		options.Continue = list.Continue
	}
}

// isBatchPod reports whether the pod belongs to an active Job or CronJob
// workload. Completed and failed batch pods are kept so their final status is
// visible in the table.
func isBatchPod(pod corev1.Pod) bool {
	for _, ref := range pod.OwnerReferences {
		switch ref.Kind {
		case "Job", "CronJob":
			return pod.Status.Phase != corev1.PodSucceeded && pod.Status.Phase != corev1.PodFailed
		}
	}
	return false
}

// statusFromContainerStatus extracts the non-secret status metadata rendered
// in the table.
func statusFromContainerStatus(status corev1.ContainerStatus) *model.ContainerStatus {
	result := &model.ContainerStatus{RestartCount: status.RestartCount}
	if terminated := status.LastTerminationState.Terminated; terminated != nil {
		result.LastTerminatedReason = terminated.Reason
	}
	switch {
	case status.State.Running != nil:
		result.State = "Running"
		result.Running = true
	case status.State.Waiting != nil:
		result.State = "Waiting"
		result.Reason = status.State.Waiting.Reason
	case status.State.Terminated != nil:
		result.State = "Terminated"
		result.Reason = status.State.Terminated.Reason
		result.ExitCode = status.State.Terminated.ExitCode
	default:
		result.State = "Unknown"
	}
	return result
}

func containerStatuses(pod corev1.Pod) map[string]corev1.ContainerStatus {
	statuses := make(map[string]corev1.ContainerStatus, len(pod.Status.ContainerStatuses))
	for _, status := range pod.Status.ContainerStatuses {
		statuses[status.Name] = status
	}
	return statuses
}

func initContainerStatuses(pod corev1.Pod) map[string]corev1.ContainerStatus {
	statuses := make(map[string]corev1.ContainerStatus, len(pod.Status.InitContainerStatuses))
	for _, status := range pod.Status.InitContainerStatuses {
		statuses[status.Name] = status
	}
	return statuses
}

const limitRangerAnnotation = "kubernetes.io/limit-ranger"

// limitRangeContainers extracts the container names listed in the LimitRanger
// admission annotation. The annotation only lists containers whose resources
// were actually defaulted by a LimitRange.
func limitRangeContainers(annotation string) map[string]bool {
	names := make(map[string]bool)
	for _, part := range strings.Split(annotation, ";") {
		part = strings.TrimSpace(part)
		if index := strings.Index(part, "for init container "); index >= 0 {
			name := strings.TrimSpace(part[index+len("for init container "):])
			if name != "" {
				names[name] = true
			}
			continue
		}
		if index := strings.Index(part, "for container "); index >= 0 {
			name := strings.TrimSpace(part[index+len("for container "):])
			if name != "" {
				names[name] = true
			}
		}
	}
	return names
}

func containerHasResources(container corev1.Container) bool {
	return len(container.Resources.Requests) > 0 || len(container.Resources.Limits) > 0
}

func oomKilled(status corev1.ContainerStatus) bool {
	return terminatedOOMKilled(status.State.Terminated) ||
		terminatedOOMKilled(status.LastTerminationState.Terminated)
}

func terminatedOOMKilled(terminated *corev1.ContainerStateTerminated) bool {
	return terminated != nil && terminated.Reason == "OOMKilled"
}

func containerResources(container corev1.Container) (cpuReq, cpuLim, memReq, memLim string) {
	var totals resourceTotals
	totals.addContainer(container)
	return totals.values()
}

type resourceTotals struct {
	cpuReq, cpuLim resource.Quantity
	memReq, memLim resource.Quantity

	hasCPUReq, hasCPULim bool
	hasMemReq, hasMemLim bool
}

func (r *resourceTotals) addContainer(container corev1.Container) {
	if quantity, ok := container.Resources.Requests[corev1.ResourceCPU]; ok {
		r.cpuReq.Add(quantity)
		r.hasCPUReq = true
	}
	if quantity, ok := container.Resources.Requests[corev1.ResourceMemory]; ok {
		r.memReq.Add(quantity)
		r.hasMemReq = true
	}
	if quantity, ok := container.Resources.Limits[corev1.ResourceCPU]; ok {
		r.cpuLim.Add(quantity)
		r.hasCPULim = true
	}
	if quantity, ok := container.Resources.Limits[corev1.ResourceMemory]; ok {
		r.memLim.Add(quantity)
		r.hasMemLim = true
	}
}

func (r resourceTotals) values() (cpuReq, cpuLim, memReq, memLim string) {
	return formatQuantity(r.cpuReq, r.hasCPUReq),
		formatQuantity(r.cpuLim, r.hasCPULim),
		formatQuantity(r.memReq, r.hasMemReq),
		formatQuantity(r.memLim, r.hasMemLim)
}

func resourceString(values corev1.ResourceList, name corev1.ResourceName) string {
	value, ok := values[name]
	if !ok {
		return ""
	}
	return value.String()
}

func formatQuantity(quantity resource.Quantity, present bool) string {
	if !present {
		return "-"
	}
	return quantity.String()
}

// ExpandPath expands a leading ~ in path to the current user's home directory.
func ExpandPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
	}
	if strings.HasPrefix(path, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, path[2:])
		}
	}
	return path
}
