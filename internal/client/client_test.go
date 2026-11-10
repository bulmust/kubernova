package client

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"kubernova/internal/model"
)

func writeKubeconfig(t *testing.T, server string) string {
	t.Helper()

	content := fmt.Sprintf(`apiVersion: v1
kind: Config
current-context: test-context
clusters:
- name: test-cluster
  cluster:
    server: %s
contexts:
- name: test-context
  context:
    cluster: test-cluster
    user: test-user
users:
- name: test-user
  user:
    token: test-token
`, server)

	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}
	return path
}

func TestLoadKubeconfig(t *testing.T) {
	path := writeKubeconfig(t, "https://example.invalid")

	config, err := LoadKubeconfig(path)
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}

	if config.Path != path {
		t.Errorf("Path = %q, want %q", config.Path, path)
	}
	if config.Cluster != "test-cluster" {
		t.Errorf("Cluster = %q, want %q", config.Cluster, "test-cluster")
	}
	if want := []string{"test-cluster"}; !reflect.DeepEqual(config.Clusters, want) {
		t.Errorf("Clusters = %v, want %v", config.Clusters, want)
	}
}

func TestLoadKubeconfigWithContext(t *testing.T) {
	content := `apiVersion: v1
kind: Config
current-context: first
clusters:
- name: cluster-one
  cluster:
    server: https://one.invalid
- name: cluster-two
  cluster:
    server: https://two.invalid
contexts:
- name: first
  context:
    cluster: cluster-one
    user: user-one
- name: second
  context:
    cluster: cluster-two
    user: user-two
users:
- name: user-one
  user:
    token: one
- name: user-two
  user:
    token: two
`
	path := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write kubeconfig: %v", err)
	}

	selected, err := LoadKubeconfigWithContext(path, "second", Options{})
	if err != nil {
		t.Fatalf("LoadKubeconfigWithContext() error = %v", err)
	}
	if selected.Context != "second" || selected.Cluster != "cluster-two" {
		t.Errorf("selected context = %#v, want second/cluster-two", selected)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(selected.Contexts, want) {
		t.Errorf("Contexts = %v, want %v", selected.Contexts, want)
	}

	if _, err := LoadKubeconfigWithContext(path, "missing", Options{}); err == nil {
		t.Fatal("LoadKubeconfigWithContext() error = nil, want unknown context error")
	}
}

func TestLoadKubeconfigMissingFile(t *testing.T) {
	_, err := LoadKubeconfig(filepath.Join(t.TempDir(), "missing"))
	if err == nil {
		t.Fatal("LoadKubeconfig() error = nil, want an error")
	}
}

func TestCheckConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/version" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"major":"1","minor":"30","gitVersion":"v1.30.0"}`)
	}))
	defer server.Close()

	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}
	if err := config.CheckConnection(time.Second); err != nil {
		t.Errorf("CheckConnection() error = %v, want nil", err)
	}
}

func TestCheckConnectionUnreachable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	serverURL := server.URL
	server.Close()

	config, err := LoadKubeconfig(writeKubeconfig(t, serverURL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}
	if err := config.CheckConnection(200 * time.Millisecond); err == nil {
		t.Fatal("CheckConnection() error = nil, want an error")
	}
}

func TestListContainers(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/limitranges" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, `{
				"apiVersion": "v1",
				"kind": "LimitRangeList",
				"items": [
					{
						"metadata": {"name": "defaults", "namespace": "backend"},
						"spec": {"limits": [
							{
								"type": "Container",
								"default": {"cpu": "500m", "memory": "256Mi"},
								"defaultRequest": {"cpu": "100m", "memory": "64Mi"}
							}
						]}
					}
				]
			}`)
			return
		}
		if r.URL.Path != "/api/v1/pods" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"apiVersion": "v1",
			"kind": "PodList",
			"items": [
				{
					"metadata": {
						"name": "api-0",
						"namespace": "backend",
						"annotations": {
							"kubernetes.io/limit-ranger": "LimitRanger plugin set: cpu request for container api; memory limit for container api; cpu request for init container init-db"
						},
						"ownerReferences": [
							{"apiVersion": "apps/v1", "kind": "StatefulSet", "name": "api", "uid": "1"}
						]
					},
					"spec": {"initContainers": [
						{"name": "init-db", "image": "busybox:1.36", "resources": {
							"requests": {"cpu": "10m"}
						}}
					], "containers": [
						{"name": "api", "image": "api:v1", "resources": {
							"requests": {"cpu": "100m", "memory": "64Mi"},
							"limits": {"cpu": "500m", "memory": "256Mi"}
						}},
						{"name": "explicit", "image": "explicit:v1", "resources": {
							"requests": {"cpu": "100m", "memory": "64Mi"},
							"limits": {"cpu": "500m", "memory": "256Mi"}
						}},
						{"name": "sidecar", "image": "envoy:1.30", "resources": {
							"requests": {"cpu": "50m", "memory": "32Mi"},
							"limits": {"cpu": "100m", "memory": "64Mi"}
						}}
					]},
					"status": {"containerStatuses": [
						{"name": "api", "state": {"running": {}}, "lastState": {"terminated": {"reason": "OOMKilled", "exitCode": 137}}},
						{"name": "sidecar", "state": {"running": {}}}
					], "initContainerStatuses": [
						{"name": "init-db", "state": {"terminated": {"reason": "Completed", "exitCode": 0}}}
					]}
				},
				{
					"metadata": {
						"name": "worker-abc",
						"namespace": "jobs",
						"ownerReferences": [
							{"apiVersion": "apps/v1", "kind": "DaemonSet", "name": "worker", "uid": "2"}
						]
					},
					"spec": {"containers": [
						{"name": "worker", "image": "worker:2", "resources": {
							"requests": {"cpu": "1", "memory": "1Gi"}
						}}
					]}
				},
				{
					"metadata": {"name": "mirror-node", "namespace": "default"},
					"spec": {"containers": [
						{"name": "plain"}
					]}
				},
				{
					"metadata": {
						"name": "job-xyz",
						"namespace": "batch",
						"ownerReferences": [
							{"apiVersion": "batch/v1", "kind": "Job", "name": "job", "uid": "3"}
						]
					},
					"spec": {"containers": [{"name": "job"}]}
				},
				{
					"metadata": {
						"name": "cron-xyz",
						"namespace": "batch",
						"ownerReferences": [
							{"apiVersion": "batch/v1", "kind": "CronJob", "name": "cron", "uid": "4"}
						]
					},
					"spec": {"containers": [{"name": "cron"}]}
				}
			]
		}`)
	}))
	defer server.Close()

	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	containers, err := config.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}

	want := []model.Container{
		{
			Cluster:        "test-cluster",
			Namespace:      "backend",
			Pod:            "api-0",
			Owners:         "StatefulSet",
			OwnerName:      "api",
			OwnerUID:       "1",
			Name:           "api",
			Image:          "api:v1",
			Registry:       "docker.io",
			Repository:     "api",
			Tag:            "v1",
			ResourceSource: "LimitRange",
			CPUReq:         "100m",
			CPULim:         "500m",
			MemReq:         "64Mi",
			MemLim:         "256Mi",
			OOM:            "yes",
			Status:         &model.ContainerStatus{State: "Running", Running: true, LastTerminatedReason: "OOMKilled"},
			Replicas:       "-",
			Ownership:      &model.OwnerReference{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "api", UID: "1"},
		},
		{
			Cluster:        "test-cluster",
			Namespace:      "backend",
			Pod:            "api-0",
			Owners:         "StatefulSet",
			OwnerName:      "api",
			OwnerUID:       "1",
			Name:           "explicit",
			Image:          "explicit:v1",
			Registry:       "docker.io",
			Repository:     "explicit",
			Tag:            "v1",
			ResourceSource: "Pod",
			CPUReq:         "100m",
			CPULim:         "500m",
			MemReq:         "64Mi",
			MemLim:         "256Mi",
			OOM:            "no",
			Replicas:       "-",
			Ownership:      &model.OwnerReference{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "api", UID: "1"},
		},
		{
			Cluster:        "test-cluster",
			Namespace:      "backend",
			Pod:            "api-0",
			Owners:         "StatefulSet",
			OwnerName:      "api",
			OwnerUID:       "1",
			Name:           "init-db",
			Image:          "busybox:1.36",
			Registry:       "docker.io",
			Repository:     "busybox",
			Tag:            "1.36",
			ResourceSource: "LimitRange",
			CPUReq:         "10m",
			CPULim:         "-",
			MemReq:         "-",
			MemLim:         "-",
			OOM:            "no",
			Status:         &model.ContainerStatus{State: "Terminated", Reason: "Completed"},
			Replicas:       "-",
			Ownership:      &model.OwnerReference{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "api", UID: "1"},
		},
		{
			Cluster:        "test-cluster",
			Namespace:      "backend",
			Pod:            "api-0",
			Owners:         "StatefulSet",
			OwnerName:      "api",
			OwnerUID:       "1",
			Name:           "sidecar",
			Image:          "envoy:1.30",
			Registry:       "docker.io",
			Repository:     "envoy",
			Tag:            "1.30",
			ResourceSource: "Pod",
			CPUReq:         "50m",
			CPULim:         "100m",
			MemReq:         "32Mi",
			MemLim:         "64Mi",
			OOM:            "no",
			Status:         &model.ContainerStatus{State: "Running", Running: true},
			Replicas:       "-",
			Ownership:      &model.OwnerReference{APIVersion: "apps/v1", Kind: "StatefulSet", Name: "api", UID: "1"},
		},
		{
			Cluster:        "test-cluster",
			Namespace:      "default",
			Pod:            "mirror-node",
			Owners:         "none",
			OwnerName:      "",
			Name:           "plain",
			Image:          "-",
			Registry:       "-",
			Repository:     "-",
			Tag:            "-",
			ResourceSource: "-",
			CPUReq:         "-",
			CPULim:         "-",
			MemReq:         "-",
			MemLim:         "-",
			OOM:            "no",
			Replicas:       "-",
		},
		{
			Cluster:        "test-cluster",
			Namespace:      "jobs",
			Pod:            "worker-abc",
			Owners:         "DaemonSet",
			OwnerName:      "worker",
			OwnerUID:       "2",
			Name:           "worker",
			Image:          "worker:2",
			Registry:       "docker.io",
			Repository:     "worker",
			Tag:            "2",
			ResourceSource: "Pod",
			CPUReq:         "1",
			CPULim:         "-",
			MemReq:         "1Gi",
			MemLim:         "-",
			OOM:            "no",
			Replicas:       "-",
			Ownership:      &model.OwnerReference{APIVersion: "apps/v1", Kind: "DaemonSet", Name: "worker", UID: "2"},
		},
	}
	if !reflect.DeepEqual(containers, want) {
		t.Errorf("ListContainers() = %#v, want %#v", containers, want)
	}
}

func TestListContainersPaginatesWithPageSize(t *testing.T) {
	var podRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pods" {
			http.NotFound(w, r)
			return
		}
		podRequests++
		if got, want := r.URL.Query().Get("limit"), "2"; got != want {
			t.Errorf("limit query = %q, want %q", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("continue") == "" {
			_, _ = fmt.Fprint(w, `{
				"apiVersion": "v1",
				"kind": "PodList",
				"metadata": {"continue": "next-page"},
				"items": [
					{"metadata": {"name": "pod-a", "namespace": "default"}, "spec": {"containers": [{"name": "a"}]}},
					{"metadata": {"name": "pod-b", "namespace": "default"}, "spec": {"containers": [{"name": "b"}]}}
				]
			}`)
			return
		}
		_, _ = fmt.Fprint(w, `{
			"apiVersion": "v1",
			"kind": "PodList",
			"items": [
				{"metadata": {"name": "pod-c", "namespace": "default"}, "spec": {"containers": [{"name": "c"}]}}
			]
		}`)
	}))
	defer server.Close()

	config, err := LoadKubeconfigWithOptions(writeKubeconfig(t, server.URL), Options{PageSize: 2})
	if err != nil {
		t.Fatalf("LoadKubeconfigWithOptions() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	containers, err := config.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if podRequests != 2 {
		t.Errorf("pod list requests = %d, want 2", podRequests)
	}
	if len(containers) != 3 {
		t.Fatalf("containers = %d, want 3", len(containers))
	}
}

func TestLimitRangeContainers(t *testing.T) {
	tests := []struct {
		name       string
		annotation string
		want       map[string]bool
	}{
		{
			name:       "empty annotation",
			annotation: "",
			want:       map[string]bool{},
		},
		{
			name:       "regular containers",
			annotation: "LimitRanger plugin set: cpu, memory request for container api; memory limit for container api",
			want:       map[string]bool{"api": true},
		},
		{
			name:       "init containers",
			annotation: "LimitRanger plugin set: cpu, memory request for init container chmod-pgpass; memory limit for init container chmod-pgpass",
			want:       map[string]bool{"chmod-pgpass": true},
		},
		{
			name:       "mixed containers and resources",
			annotation: "LimitRanger plugin set: cpu request for container api; memory limit for init container init-db; ephemeral-storage request for container logs",
			want:       map[string]bool{"api": true, "init-db": true, "logs": true},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := limitRangeContainers(test.annotation); !reflect.DeepEqual(got, test.want) {
				t.Errorf("limitRangeContainers(%q) = %v, want %v", test.annotation, got, test.want)
			}
		})
	}
}

func TestCheckConnectionTimeout(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
	}))
	defer func() {
		close(block)
		server.Close()
	}()

	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}

	start := time.Now()
	if err := config.CheckConnection(100 * time.Millisecond); err == nil {
		t.Fatal("CheckConnection() error = nil, want a timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("CheckConnection() took %v, want less than 1s", elapsed)
	}
}

func TestExpandPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("cannot determine home directory: %v", err)
	}

	if got, want := ExpandPath("~/kube/config"), filepath.Join(home, "kube/config"); got != want {
		t.Errorf("ExpandPath(~/kube/config) = %q, want %q", got, want)
	}
	if got, want := ExpandPath("~"), home; got != want {
		t.Errorf("ExpandPath(~) = %q, want %q", got, want)
	}
	if got, want := ExpandPath("/tmp/config"), "/tmp/config"; got != want {
		t.Errorf("ExpandPath(/tmp/config) = %q, want %q", got, want)
	}
}

func TestListContainersIncludesCompletedJobPods(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/pods" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"apiVersion": "v1",
			"kind": "PodList",
			"items": [
				{
					"metadata": {"name": "job-done", "namespace": "batch", "ownerReferences": [
						{"apiVersion": "batch/v1", "kind": "Job", "name": "job", "uid": "3"}
					]},
					"spec": {"containers": [{"name": "job"}]},
					"status": {
						"phase": "Succeeded",
						"containerStatuses": [{"name": "job", "state": {"terminated": {"reason": "Completed", "exitCode": 0}}}]
					}
				},
				{
					"metadata": {"name": "job-running", "namespace": "batch", "ownerReferences": [
						{"apiVersion": "batch/v1", "kind": "Job", "name": "job", "uid": "3"}
					]},
					"spec": {"containers": [{"name": "job"}]},
					"status": {"phase": "Running"}
				}
			]
		}`)
	}))
	defer server.Close()

	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	containers, err := config.ListContainers(ctx)
	if err != nil {
		t.Fatalf("ListContainers() error = %v", err)
	}
	if len(containers) != 1 {
		t.Fatalf("containers = %d, want only the completed Job pod", len(containers))
	}
	if containers[0].Pod != "job-done" || !containers[0].Completed() {
		t.Errorf("completed Job container = %#v", containers[0])
	}
}

func TestListNodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/nodes" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{
			"apiVersion": "v1",
			"kind": "NodeList",
			"items": [{
				"metadata": {"name": "worker-1"},
				"status": {
					"capacity": {"cpu": "4", "memory": "8Gi"},
					"allocatable": {"cpu": "3900m", "memory": "7Gi"}
				}
			}]
		}`)
	}))
	defer server.Close()

	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inventory, err := config.ListNodes(ctx)
	if err != nil {
		t.Fatalf("ListNodes() error = %v", err)
	}
	if !inventory.Available || len(inventory.Items) != 1 {
		t.Fatalf("inventory = %#v", inventory)
	}
	if inventory.TotalNodes != 1 || inventory.TotalCPUCapacity != "4" || inventory.TotalAllocatableCPU != "3900m" ||
		inventory.TotalMemoryCapacity != "8Gi" || inventory.TotalAllocatableMemory != "7Gi" {
		t.Errorf("aggregate totals = %#v", inventory)
	}
	node := inventory.Items[0]
	if node.Name != "worker-1" || node.CapacityCPU != "4" || node.CapacityMemory != "8Gi" ||
		node.AllocatableCPU != "3900m" || node.AllocatableMemory != "7Gi" {
		t.Errorf("node = %#v", node)
	}
}

func TestListNodesRBACFailureIsReported(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	inventory, err := config.ListNodes(ctx)
	if err == nil {
		t.Fatal("ListNodes() error = nil, want RBAC error")
	}
	if inventory.Available || inventory.Reason != "n/a" {
		t.Errorf("inventory = %#v, want unavailable n/a", inventory)
	}
}

func TestSilenceKlogKeepsStderrClean(t *testing.T) {
	SilenceKlog()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Add("Warning", `299 - "v1 Foo is deprecated"`)
		_, _ = fmt.Fprint(w, `{"apiVersion":"v1","kind":"PodList","items":[]}`)
	}))
	defer server.Close()
	config, err := LoadKubeconfig(writeKubeconfig(t, server.URL))
	if err != nil {
		t.Fatalf("LoadKubeconfig() error = %v", err)
	}

	original := os.Stderr
	reader, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = writer
	_, listErr := config.ListContainers(context.Background())
	os.Stderr = original
	_ = writer.Close()
	output, _ := io.ReadAll(reader)
	if listErr != nil {
		t.Fatalf("ListContainers() error = %v", listErr)
	}
	if len(output) != 0 {
		t.Errorf("stderr = %q, want no client-go log output", output)
	}
}
