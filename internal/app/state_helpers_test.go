package app

import (
	"reflect"
	"testing"

	"github.com/gdamore/tcell/v2"

	"kubernova/internal/config"
	"kubernova/internal/model"
	"kubernova/internal/registry"
	"kubernova/internal/state"
)

func TestNormalizeNamespaceName(t *testing.T) {
	tests := map[string]string{
		"":       "all",
		"-A":     "all",
		"all":    "all",
		"ALL":    "all",
		"argocd": "argocd",
	}
	for input, want := range tests {
		if got := normalizeNamespaceName(input); got != want {
			t.Errorf("normalizeNamespaceName(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeNamespaceSlots(t *testing.T) {
	got := normalizeNamespaceSlots([]string{"argocd", "all", "default", "argocd", "-A", "kube-system"})
	want := []string{"argocd", "default", "kube-system"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeNamespaceSlots() = %v, want %v", got, want)
	}
}

func TestPrependNamespaceKeepsNineSlots(t *testing.T) {
	slots := []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"}
	got := prependNamespace(slots, "new")
	want := []string{"new", "a", "b", "c", "d", "e", "f", "g", "h"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("prependNamespace() = %v, want %v", got, want)
	}
}

func TestContainerNamespaces(t *testing.T) {
	containers := []model.Container{
		{Namespace: "zeta"},
		{Namespace: "alpha"},
		{Namespace: "zeta"},
		{Namespace: ""},
	}
	got := containerNamespaces(containers)
	want := []string{"alpha", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("containerNamespaces() = %v, want %v", got, want)
	}
}

func TestVisibilityDiffers(t *testing.T) {
	base := []bool{true, false, true}
	if visibilityDiffers(base, append([]bool(nil), base...)) {
		t.Error("identical visibility should not be dirty")
	}
	if !visibilityDiffers(base, []bool{true, true, true}) {
		t.Error("changed visibility should be dirty")
	}
	if !visibilityDiffers(base, []bool{true}) {
		t.Error("different lengths should be dirty")
	}
}

func TestMatchShortcut(t *testing.T) {
	if !matchShortcut(tcell.NewEventKey(tcell.KeyCtrlP, 0, tcell.ModNone), "ctrl+p") {
		t.Error("ctrl+p should match")
	}
	if matchShortcut(tcell.NewEventKey(tcell.KeyCtrlP, 0, tcell.ModNone), "ctrl+r") {
		t.Error("ctrl+p should not match ctrl+r")
	}
	if !matchShortcut(tcell.NewEventKey(tcell.KeyRune, 'f', tcell.ModNone), "f") {
		t.Error("f should match")
	}
	if !matchShortcut(tcell.NewEventKey(tcell.KeyCtrlJ, 0, tcell.ModNone), "ctrl+j") {
		t.Error("ctrl+j should match")
	}
	if !matchShortcut(tcell.NewEventKey(tcell.KeyCtrlL, 0, tcell.ModNone), "ctrl+l") {
		t.Error("ctrl+l should match")
	}
	for _, test := range []struct {
		r       rune
		binding string
		want    bool
	}{
		{'H', "H", true}, {'h', "H", false}, {'h', "h", true}, {'H', "h", false},
		{'<', "<", true}, {'>', "<", false}, {'L', "L", true}, {'l', "L", false},
	} {
		got := matchShortcut(tcell.NewEventKey(tcell.KeyRune, test.r, tcell.ModNone), test.binding)
		if got != test.want {
			t.Errorf("matchShortcut(%q, %q) = %v, want %v", test.r, test.binding, got, test.want)
		}
	}
}

func TestCommandCompletions(t *testing.T) {
	reg := registry.Default()
	if got := commandCompletions(reg, "prom", nil); len(got) != 5 || got[0] != "promconf" || got[1] != "promconfig" || got[2] != "prometheus" || got[3] != "prometheusurl" || got[4] != "promurl" {
		t.Errorf("command completions = %v, want all prometheus-related commands", got)
	}
	if got := commandCompletions(reg, "namespace ar", []string{"argocd", "default"}); len(got) != 1 || got[0] != "namespace argocd" {
		t.Errorf("namespace completions = %v, want namespace argocd", got)
	}
	if got := commandCompletions(reg, "connect ", nil); len(got) != 1 || got[0] != "connect prometheus" {
		t.Errorf("connect completions = %v, want connect prometheus", got)
	}
	if got := commandCompletions(reg, "ns ar", []string{"argocd", "default"}); len(got) != 1 || got[0] != "ns argocd" {
		t.Errorf("ns completions = %v, want ns argocd", got)
	}
	if got := commandCompletions(reg, "ns ", []string{"argocd", "default"}); len(got) != 3 {
		t.Errorf("empty ns completions = %v, want all/default/argocd", got)
	}
}

func TestNamespaceCompletionUsesAllLoadedNamespaces(t *testing.T) {
	reg := registry.Default()
	containers := []model.Container{
		{Namespace: "argocd"},
		{Namespace: "payments"},
	}
	// Only argocd is a header favorite; payments still has to complete.
	favorites := []string{"argocd"}
	candidates := append(containerNamespaces(containers), favorites...)
	got := commandCompletions(reg, "ns pay", candidates)
	if len(got) != 1 || got[0] != "ns payments" {
		t.Errorf("non-favorite namespace completions = %v, want ns payments", got)
	}
	if got := commandCompletions(reg, "namespace pay", candidates); len(got) != 1 || got[0] != "namespace payments" {
		t.Errorf("namespace alias completions = %v, want namespace payments", got)
	}
}

func TestContextCompletionAndExactAlias(t *testing.T) {
	reg := registry.Default()
	if got := commandCompletions(reg, "ctx pro", nil, []string{"prod", "staging"}); len(got) != 1 || got[0] != "ctx prod" {
		t.Errorf("context completions = %v, want ctx prod", got)
	}
	if got := commandCompletions(reg, "context sta", nil, []string{"prod", "staging"}); len(got) != 1 || got[0] != "context staging" {
		t.Errorf("context completions = %v, want context staging", got)
	}
	if got := commandCompletions(reg, "c", nil); len(got) != 1 || got[0] != "c" {
		t.Errorf("exact alias completions = %v, want c without a prefix expansion", got)
	}
	command, ok := reg.Command("c")
	if !ok || command.Name != registry.CommandColumns {
		t.Errorf("Command(c) = %#v, %v; want columns", command, ok)
	}
}

func TestRefreshConfirmationWording(t *testing.T) {
	want := "all connections and requests will be redone, are you sure?"
	if refreshConfirmation != want {
		t.Errorf("refresh confirmation = %q, want %q", refreshConfirmation, want)
	}
}

func TestCompletionSuffix(t *testing.T) {
	commands := commandCompletions(registry.Default(), "promet", nil)
	if got := completionSuffix("promet", commands); got != "heus" {
		t.Errorf("completionSuffix(promet) = %q, want heus", got)
	}
	namespaces := commandCompletions(registry.Default(), "ns ar", []string{"argocd", "default"})
	if got := completionSuffix("ns ar", namespaces); got != "gocd" {
		t.Errorf("completionSuffix(ns ar) = %q, want gocd", got)
	}
	if got := completionSuffix("prometheusurl", commands); got != "" {
		t.Errorf("completionSuffix(exact) = %q, want empty", got)
	}
}

func TestAvailableQColumns(t *testing.T) {
	cfg := config.Default()
	if got := availablePrometheusColumns(cfg); len(got) != 7 {
		t.Errorf("default available P columns = %v, want 7", got)
	}
	cfg.Resources.CPU.Percentiles.Request = 0
	cfg.Resources.Memory.OOMKilledCheck = false
	got := availablePrometheusColumns(cfg)
	if containsString(got, "P01") || containsString(got, "P09") {
		t.Errorf("disabled P columns still available: %v", got)
	}
	cfg.Prometheus.Enabled = false
	if got := availablePrometheusColumns(cfg); len(got) != 0 {
		t.Errorf("prometheus disabled still offers P columns: %v", got)
	}
}

func TestPrometheusAutoDecision(t *testing.T) {
	tests := []struct {
		name       string
		enabled    bool
		cached     bool
		suppressed bool
		url        string
		want       prometheusDecision
	}{
		{"disabled", false, false, false, "https://prom.example", prometheusSkip},
		{"filled cache", true, true, false, "https://prom.example", prometheusSkip},
		{"auto connect", true, false, false, "https://prom.example", prometheusConnect},
		{"missing url prompts", true, false, false, "", prometheusPrompt},
		{"suppressed prompt", true, false, true, "", prometheusSkip},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := prometheusAutoDecision(test.enabled, test.cached, test.suppressed, test.url); got != test.want {
				t.Errorf("prometheusAutoDecision() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestMigrateLegacyPrometheusDoesNotOverwriteState(t *testing.T) {
	verify := true
	legacy := &config.PrometheusContext{
		URL:            "https://prom.example",
		SSLVerify:      &verify,
		TenantHeader:   "tenant-a",
		BearerTokenEnv: "PROM_TOKEN",
		LabelFilters:   map[string]string{"cluster": "prod"},
	}
	value := state.State{}
	if !migrateLegacyPrometheus(&value, legacy) {
		t.Fatal("migrateLegacyPrometheus() = false, want migration")
	}
	if value.PrometheusURL != "https://prom.example" || value.PrometheusSSLVerify == nil || !*value.PrometheusSSLVerify ||
		value.PrometheusTenantHeader != "tenant-a" || value.PrometheusBearerTokenEnv != "PROM_TOKEN" ||
		value.PrometheusLabelFilters["cluster"] != "prod" {
		t.Errorf("migrated state = %#v", value)
	}

	existing := state.State{PrometheusURL: "https://existing.example"}
	migrateLegacyPrometheus(&existing, legacy)
	if existing.PrometheusURL != "https://existing.example" {
		t.Errorf("existing URL = %q, want it preserved", existing.PrometheusURL)
	}
	if existing.PrometheusTenantHeader != "tenant-a" {
		t.Errorf("missing settings were not migrated: %#v", existing)
	}
}

func TestExportCompletionIncludesFormats(t *testing.T) {
	got := commandCompletions(registry.Default(), "export j", nil)
	if len(got) != 1 || got[0] != "export json" {
		t.Errorf("export completions = %v, want export json", got)
	}
	got = commandCompletions(registry.Default(), "export ", nil)
	if len(got) != 3 || got[0] != "export all" || got[1] != "export json" || got[2] != "export md" {
		t.Errorf("export completions = %v, want all/json/md", got)
	}
}

func TestColumnSourceLabels(t *testing.T) {
	if got := columnSource("K01"); got != "kubeapi" {
		t.Errorf("columnSource(K01) = %q, want kubeapi", got)
	}
	if got := columnSource("P05"); got != "prometheus" {
		t.Errorf("columnSource(P05) = %q, want prometheus", got)
	}
}
