package app

import (
	"kubernova/internal/model"
	"sort"
	"strings"
)

func containerNamespaces(containers []model.Container) []string {
	seen := make(map[string]bool)
	namespaces := make([]string, 0)
	for _, container := range containers {
		if container.Namespace == "" || seen[container.Namespace] {
			continue
		}
		seen[container.Namespace] = true
		namespaces = append(namespaces, container.Namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

func normalizeNamespaceName(namespace string) string {
	namespace = strings.TrimSpace(namespace)
	if namespace == "" || namespace == "-A" || strings.EqualFold(namespace, "all") {
		return "all"
	}
	return namespace
}

func normalizeNamespaceSlots(namespaces []string) []string {
	slots := make([]string, 0, 9)
	for _, namespace := range namespaces {
		namespace = normalizeNamespaceName(namespace)
		if namespace == "all" || containsNamespace(slots, namespace) {
			continue
		}
		slots = append(slots, namespace)
		if len(slots) >= 9 {
			break
		}
	}
	return slots
}

func containsNamespace(slots []string, namespace string) bool {
	for _, candidate := range slots {
		if candidate == namespace {
			return true
		}
	}
	return false
}

func prependNamespace(slots []string, namespace string) []string {
	next := make([]string, 0, 9)
	next = append(next, namespace)
	for _, candidate := range slots {
		if candidate == namespace {
			continue
		}
		if len(next) >= 9 {
			break
		}
		next = append(next, candidate)
	}
	return next
}
