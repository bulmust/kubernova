package app

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"

	"kubernova/internal/client"
	"kubernova/internal/config"
	"kubernova/internal/export"
	"kubernova/internal/model"
	"kubernova/internal/registry"
	"kubernova/internal/report"
	"kubernova/internal/state"
	"kubernova/internal/ui"
)

type barMode int

const (
	barIdle barMode = iota
	barFilter
	barCommand
	barPrompt
)

const (
	idlePrompt            = "press / to filter or : for command"
	filterPrompt          = "filter all fields (esc to clear)"
	commandPrompt         = "command (e.g. ns argocd, option)"
	urlPrompt             = "Prometheus URL (esc to cancel)"
	refreshConfirmation   = "all connections and requests will be redone, are you sure?"
	mainPage              = "main"
	kubeconfigPage        = "kubeconfig"
	helpPage              = "help"
	statusPage            = "status"
	prometheusConfigPage  = "prometheus-config"
	discardPrometheusPage = "discard-prometheus"
	columnsPage           = "columns"
	contextsPage          = "contexts"
	discardColumnsPage    = "discard-columns"
	configListPage        = "config-list"
	warningPage           = "warning"
	refreshConfirmPage    = "refresh-confirm"
)

const (
	prometheusFormURL = iota
	prometheusFormSSLVerify
	prometheusFormTenant
	prometheusFormBearer
	prometheusFormBasicUser
	prometheusFormBasicPassword
	prometheusFormLabels
)

type prometheusFormValues struct {
	URL          string
	SSLVerify    bool
	TenantHeader string
	BearerToken  string
	BasicUser    string
	BasicPass    string
	LabelFilters string
}

// Run starts the application and runs the event loop, blocking it.
func Run() error {
	client.SilenceKlog()

	loaded, cfgErr := config.Load()
	cfg := loaded.Config
	if cfgErr != nil {
		cfg = config.Default()
	}
	activeEffective := loaded.EffectiveYAML
	if len(activeEffective) == 0 {
		if snapshot, err := config.Merge(nil); err == nil {
			activeEffective = snapshot.EffectiveYAML
		}
	}
	reg := registry.Default().WithBindings(cfg.Keybindings)
	baseConfig := cfg
	legacyPrometheus := loaded.LegacyPrometheus
	legacyPrometheusMigrated := false

	skin := skinFromConfig(cfg)
	ui.BackgroundColor = skin.BodyBG
	skinManager := ui.NewSkinManager(skin)
	tview.Styles.PrimitiveBackgroundColor = skin.BodyBG
	tview.Styles.ContrastBackgroundColor = skin.BodyBG
	tview.Styles.MoreContrastBackgroundColor = skin.BodyBG
	tview.Styles.BorderColor = skin.Section
	tview.Styles.TitleColor = skin.Section
	tview.Styles.GraphicsColor = skin.MenuKey
	tview.Styles.PrimaryTextColor = skin.BodyFG
	tview.Styles.SecondaryTextColor = skin.MenuFG
	tview.Styles.TertiaryTextColor = skin.MenuFG
	tview.Styles.InverseTextColor = skin.BodyBG
	tview.Styles.ContrastSecondaryTextColor = skin.MenuFG

	a := tview.NewApplication()
	guard := &panicGuard{app: a}

	// Read the shell's kubeconfig before drawing the app. This is a local file
	// read; cluster connectivity is checked asynchronously with a timeout.
	clusterName := "unknown"
	prometheusURL := cfg.Prometheus.URL
	prometheusState := ui.ConnectionNotConfigured
	prometheusStatus := ""
	var prometheusMeta *client.CachePrometheus
	prometheusBusy := false
	var prometheusCancel context.CancelFunc
	prometheusPromptConnect := false
	prometheusPromptSuppressed := false
	qColumnsCustomized := false
	currentContainers := []model.Container{}
	var currentNodeInventory *client.NodeInventory
	var kubeconfig *client.Kubeconfig
	var kubeconfigErr error
	activeKubeconfigPath := ""
	var kubernetesCancel context.CancelFunc
	requestGeneration := 0
	activeKubeconfigPath, _ = kubeconfigSource(cfg)
	kubeconfig, kubeconfigErr = loadKubeconfig(cfg)
	if kubeconfigErr == nil {
		clusterName = kubeconfig.Cluster
	}

	table := ui.NewContainerTable(nil)
	header := ui.NewHeader(skinManager)
	statusIndicator := ui.NewStatusIndicator(skinManager)

	namespaceSlots := make([]string, 0, 9)
	activeNamespace := "all"
	stateCluster := ""
	var persistViewState func()
	var applyPrometheusState func(state.State)
	var applyClusterState func(string, string)
	var mergeNamespaces func([]string)
	var selectNamespace func(string, bool)
	var refreshMenu func()

	contextName := ""
	if kubeconfigErr == nil && kubeconfig != nil {
		contextName = kubeconfig.Context
	}

	connectionState := ui.ConnectionDisconnected
	if kubeconfigErr == nil {
		connectionState = ui.ConnectionConnecting
	}
	clusterState := connectionState
	var updateStatusIndicator func()
	headerStatus := ""
	statusHistory := make([]string, 0, 64)

	updateClusterInfo := func() {
		prometheus := prometheusState.Marker() + " not configured"
		if prometheusURL != "" {
			prometheus = prometheusState.Marker() + " " + prometheusURL
			switch prometheusState {
			case ui.ConnectionConnected:
				prometheus = prometheusState.Marker() + " connected"
			case ui.ConnectionConnecting:
				prometheus += " connecting"
			}
			if prometheusStatus != "" {
				prometheus += " " + prometheusStatus
			}
		}
		header.SetClusterInfo(ui.ClusterInfoData{
			ClusterName: clusterName,
			Context:     contextName,
			Cluster:     clusterState.Marker() + " " + clusterName,
			Prometheus:  prometheus,
			Status:      headerStatus,
		})
	}
	setStatus := func(state ui.ConnectionState) {
		clusterState = state
		updateClusterInfo()
		if updateStatusIndicator != nil {
			updateStatusIndicator()
		}
	}
	setStatus(connectionState)

	loadContainers := func(config *client.Kubeconfig) ([]model.Container, *client.NodeInventory, error) {
		ctx, cancel := context.WithTimeout(context.Background(), client.ListTimeout)
		kubernetesCancel = cancel
		defer cancel()
		containers, err := config.ListContainers(ctx)
		if err != nil {
			return nil, nil, err
		}
		nodes, err := config.ListNodes(ctx)
		if err != nil {
			// Node RBAC failures must not hide the container inventory.
			nodes = client.NodeInventory{Available: false, Reason: "n/a"}
		}
		return containers, &nodes, nil
	}

	// General-purpose command/prompt line. It is only inserted while a
	// filter, command, or URL prompt is active.
	barBG := skin.BodyBG
	idlePlaceholderStyle := tcell.StyleDefault.
		Background(barBG).
		Foreground(skin.MenuFG)
	bar := ui.NewPromptInput()
	bar.SetLabel(" > ").
		SetLabelStyle(tcell.StyleDefault.
			Background(barBG).
			Foreground(skin.Section)).
		SetFieldStyle(tcell.StyleDefault.
			Background(barBG).
			Foreground(skin.BodyFG)).
		SetPlaceholder(idlePrompt).
		SetPlaceholderStyle(idlePlaceholderStyle)
	bar.SetBackgroundColor(barBG)

	promptPanel := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(bar, 0, 1, true)
	promptPanel.SetBorder(true).
		SetTitle(" command ").
		SetTitleColor(skin.Section).
		SetBorderColor(skin.Section).
		SetBackgroundColor(barBG)

	flash := tview.NewTextView().
		SetWrap(false)
	flash.SetBackgroundColor(skin.BodyBG)
	flash.SetTextStyle(tcell.StyleDefault.
		Background(skin.BodyBG).
		Foreground(skin.MenuFG))

	mode := barIdle
	status := ""
	statusColor := skin.MenuFG
	headerVisible := true
	fullscreen := cfg.View.Fullscreen

	layout := tview.NewFlex().SetDirection(tview.FlexRow)
	rebuildLayout := func() {
		layout.Clear()
		table.SetBorder(!fullscreen)
		if fullscreen {
			layout.AddItem(table, 0, 1, true)
			return
		}
		if headerVisible {
			layout.AddItem(header, ui.HeaderHeight, 0, false)
		} else {
			layout.AddItem(statusIndicator, 1, 0, false)
		}
		if mode != barIdle {
			layout.AddItem(promptPanel, 3, 0, false)
		}
		layout.AddItem(table, 0, 1, true)
	}
	rebuildLayout()

	pages := tview.NewPages().AddPage(mainPage, layout, true, true)

	// Help page: commands on the left, shortcuts on the right.
	help := ui.NewHelp(reg)
	pages.AddPage(helpPage, help, true, false)

	statusView := tview.NewTextView().
		SetDynamicColors(true).
		SetWrap(false)
	statusView.SetBackgroundColor(ui.BackgroundColor)
	statusView.SetTextStyle(tcell.StyleDefault.
		Background(ui.BackgroundColor).
		Foreground(tcell.ColorWhite))
	statusView.SetBorder(true)
	statusView.SetBorderColor(tcell.ColorFuchsia)
	statusView.SetTitle(" STATUS MESSAGES ")
	statusView.SetTitleColor(tcell.ColorFuchsia)
	pages.AddPage(statusPage, statusView, true, false)

	prometheusForm := tview.NewForm().
		AddInputField("URL", "", 0, nil, nil).
		AddCheckbox("SSL verify", false, nil).
		AddInputField("Tenant header", "", 0, nil, nil).
		AddInputField("Bearer token env", "", 0, nil, nil).
		AddInputField("Basic username env", "", 0, nil, nil).
		AddInputField("Basic password env", "", 0, nil, nil).
		AddInputField("Label filters", "", 0, nil, nil)
	prometheusForm.SetBackgroundColor(ui.BackgroundColor)
	prometheusForm.SetBorder(true)
	prometheusForm.SetBorderColor(tcell.ColorFuchsia)
	prometheusForm.SetTitle(" PROMETHEUS CONTEXT SETTINGS ")
	prometheusForm.SetTitleColor(tcell.ColorFuchsia)
	prometheusForm.SetFieldBackgroundColor(ui.BackgroundColor)
	prometheusForm.SetFieldTextColor(tcell.ColorWhite)
	prometheusConfigHelp := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetText("[yellow]Enter[-] save    [yellow]Tab[-] next field    [yellow]Esc[-] cancel    environment-variable names only; secrets are never stored")
	prometheusConfigHelp.SetBackgroundColor(ui.BackgroundColor)
	prometheusConfigHelp.SetTextStyle(tcell.StyleDefault.
		Background(ui.BackgroundColor).
		Foreground(tcell.ColorGray))
	prometheusConfigPageView := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(prometheusForm, 0, 1, true).
		AddItem(prometheusConfigHelp, 1, 0, false)
	pages.AddPage(prometheusConfigPage, prometheusConfigPageView, true, false)

	prometheusDiscardModal := tview.NewModal()
	prometheusDiscardModal.SetBackgroundColor(ui.BackgroundColor)
	pages.AddPage(discardPrometheusPage, prometheusDiscardModal, true, false)

	// Configuration file chooser for :option. Files are edited externally.
	configTable := tview.NewTable().
		SetSelectable(true, false).
		SetFixed(0, 0)
	configTable.SetBackgroundColor(ui.BackgroundColor)
	configTable.SetBorder(true)
	configTable.SetBorderColor(tcell.ColorFuchsia)
	configTable.SetTitle(" CONFIGURATION FILES ")
	configTable.SetTitleColor(tcell.ColorFuchsia)
	configTable.SetSelectedStyle(tcell.StyleDefault.
		Background(tcell.ColorAqua).Foreground(tcell.ColorBlack))
	configHelp := tview.NewTextView().
		SetDynamicColors(true).
		SetTextStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorGray)).
		SetText("[yellow]Enter[-] edit with $VISUAL/$EDITOR    [yellow]Esc[-] cancel")
	configHelp.SetBackgroundColor(ui.BackgroundColor)
	configListPageView := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(configTable, 0, 1, true).
		AddItem(configHelp, 2, 0, false)
	pages.AddPage(configListPage, configListPageView, true, false)

	warningModal := tview.NewModal()
	warningModal.SetBackgroundColor(ui.BackgroundColor)
	pages.AddPage(warningPage, warningModal, true, false)

	refreshConfirmModal := tview.NewModal()
	refreshConfirmModal.SetBackgroundColor(ui.BackgroundColor)
	pages.AddPage(refreshConfirmPage, refreshConfirmModal, true, false)

	// Column picker page.
	columnList := tview.NewList().
		ShowSecondaryText(false).
		SetHighlightFullLine(true)
	columnList.SetUseStyleTags(false, false)
	columnList.SetBackgroundColor(ui.BackgroundColor)
	columnList.SetBorder(true)
	columnList.SetBorderColor(tcell.ColorFuchsia)
	columnList.SetTitle(" CHOOSE COLUMNS ")
	columnList.SetTitleColor(tcell.ColorFuchsia)
	columnHelp := tview.NewTextView().
		SetDynamicColors(true).
		SetTextStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorGray)).
		SetText("[yellow]Space[-] toggle    [yellow]Enter[-] save and close    [yellow]Esc[-] discard")
	columnHelp.SetBackgroundColor(ui.BackgroundColor)
	columnsPageView := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(columnList, 0, 1, true).
		AddItem(columnHelp, 2, 0, false)
	pages.AddPage(columnsPage, columnsPageView, true, false)

	// Kubernetes context picker shown by :context without an argument.
	contextList := tview.NewList().
		ShowSecondaryText(false).
		SetHighlightFullLine(true)
	contextList.SetUseStyleTags(false, false)
	contextList.SetBackgroundColor(ui.BackgroundColor)
	contextList.SetBorder(true)
	contextList.SetBorderColor(tcell.ColorFuchsia)
	contextList.SetTitle(" KUBERNETES CONTEXTS ")
	contextList.SetTitleColor(tcell.ColorFuchsia)
	contextHelp := tview.NewTextView().
		SetDynamicColors(true).
		SetTextStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorGray)).
		SetText("[yellow]Enter[-] switch context    [yellow]Esc[-] close")
	contextHelp.SetBackgroundColor(ui.BackgroundColor)
	contextsPageView := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(contextList, 0, 1, true).
		AddItem(contextHelp, 2, 0, false)
	pages.AddPage(contextsPage, contextsPageView, true, false)

	discardColumnsModal := tview.NewModal()
	discardColumnsModal.SetBackgroundColor(ui.BackgroundColor)
	pages.AddPage(discardColumnsPage, discardColumnsModal, true, false)

	// Kubeconfig prompt shown when the current kubeconfig cannot be reached.
	promptMessage := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetTextStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorWhite))
	promptMessage.SetBackgroundColor(ui.BackgroundColor)

	pathInput := tview.NewInputField().
		SetLabel(" Path: ").
		SetLabelStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorFuchsia)).
		SetFieldStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorWhite)).
		SetPlaceholder("~/.kube/config").
		SetPlaceholderStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorGray))
	pathInput.SetBackgroundColor(ui.BackgroundColor)

	promptHelp := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetTextStyle(tcell.StyleDefault.
			Background(ui.BackgroundColor).
			Foreground(tcell.ColorGray)).
		SetText("[gray]Enter connect    Esc quit")
	promptHelp.SetBackgroundColor(ui.BackgroundColor)

	promptBox := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(promptMessage, 4, 0, false).
		AddItem(pathInput, 1, 0, true).
		AddItem(promptHelp, 1, 0, false)
	promptBox.SetBorder(true).
		SetBorderColor(tcell.ColorFuchsia).
		SetTitle(" kubeconfigpath ").
		SetTitleColor(tcell.ColorFuchsia).
		SetBackgroundColor(ui.BackgroundColor)

	promptWidth := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(promptBox, 0, 3, true).
		AddItem(nil, 0, 1, false)

	promptPage := tview.NewFlex().
		SetDirection(tview.FlexRow).
		AddItem(nil, 0, 1, false).
		AddItem(promptWidth, 8, 0, true).
		AddItem(nil, 0, 1, false)

	pages.AddPage(kubeconfigPage, promptPage, true, false)

	setMode := func(m barMode) {
		wasIdle := mode == barIdle
		mode = m
		switch m {
		case barFilter:
			bar.SetLabel(" / ")
			bar.SetPlaceholder(filterPrompt)
			promptPanel.SetTitle(" filter ")
		case barCommand:
			bar.SetLabel(" : ")
			bar.SetPlaceholder(commandPrompt)
			promptPanel.SetTitle(" command ")
		case barPrompt:
			bar.SetLabel(" : ")
			bar.SetPlaceholder(urlPrompt)
			promptPanel.SetTitle(" input ")
		default:
			bar.SetLabel(" > ")
			bar.SetPlaceholder(idlePrompt)
		}
		if wasIdle != (mode == barIdle) {
			rebuildLayout()
		}
		updateStatusIndicator()
	}

	setFlash := func(message string, color tcell.Color) {
		status = message
		statusColor = color
		headerStatus = message
		if message != "" {
			statusHistory = append(statusHistory, time.Now().Format("15:04:05")+" "+message)
		}
		updateClusterInfo()
		flash.SetText(message)
		flash.SetTextStyle(tcell.StyleDefault.
			Background(skin.BodyBG).
			Foreground(color))
		updateStatusIndicator()
	}
	setStatusMessage := func(message string) {
		setFlash(message, skin.MenuFG)
	}
	setErrorMessage := func(message string) {
		setFlash(message, skin.Error)
	}

	legacyWarningShown := false
	showConfigWarnings := func(warnings []string) {
		if legacyWarningShown || len(warnings) == 0 {
			return
		}
		legacyWarningShown = true
		setStatusMessage(strings.Join(warnings, "; "))
	}

	toggleFullscreen := func() {
		fullscreen = !fullscreen
		rebuildLayout()
		persistViewState()
		if fullscreen {
			setStatusMessage("fullscreen: on")
		} else {
			setStatusMessage("fullscreen: off")
		}
	}

	updateStatusIndicator = func() {
		statusIndicator.SetConnectionState(clusterState)
		statusIndicator.SetContent("Kubernova")
	}
	updateStatusIndicator()

	applySkin := func(next ui.Skin) {
		skin = next
		ui.BackgroundColor = next.BodyBG
		tview.Styles.PrimitiveBackgroundColor = next.BodyBG
		tview.Styles.ContrastBackgroundColor = next.BodyBG
		tview.Styles.MoreContrastBackgroundColor = next.BodyBG
		tview.Styles.BorderColor = next.Section
		tview.Styles.TitleColor = next.Section
		tview.Styles.GraphicsColor = next.MenuKey
		tview.Styles.PrimaryTextColor = next.BodyFG
		tview.Styles.SecondaryTextColor = next.MenuFG
		tview.Styles.TertiaryTextColor = next.MenuFG
		tview.Styles.InverseTextColor = next.BodyBG
		tview.Styles.ContrastSecondaryTextColor = next.MenuFG

		bar.SetBackgroundColor(next.BodyBG)
		bar.SetLabelStyle(tcell.StyleDefault.
			Background(next.BodyBG).
			Foreground(next.Section))
		bar.SetFieldStyle(tcell.StyleDefault.
			Background(next.BodyBG).
			Foreground(next.BodyFG))
		bar.SetPlaceholderStyle(tcell.StyleDefault.
			Background(next.BodyBG).
			Foreground(next.MenuFG))
		promptPanel.SetBorderColor(next.Section)
		promptPanel.SetTitleColor(next.Section)
		promptPanel.SetBackgroundColor(next.BodyBG)
		flash.SetBackgroundColor(next.BodyBG)
		table.SetBackgroundColor(next.BodyBG)
		skinManager.SetSkin(next)
		setFlash(status, statusColor)
	}

	applyStateColumns := func(columns []string) {
		visibility := ui.ColumnVisibilityFromIDs(columns)
		if visibility == nil {
			return
		}
		table.SetColumnVisibility(visibility)
	}
	table.SetSort(cfg.View.SortColumn, cfg.View.SortDescending)

	persistViewState = func() {
		if clusterName == "" || clusterName == "unknown" {
			return
		}
		columns := ui.ColumnVisibilityIDs(table.ColumnVisibility())
		collapsed := table.ReplicaCollapsed()
		sslVerify := cfg.Prometheus.SSLVerify
		value := state.State{
			Namespaces:                 append([]string(nil), namespaceSlots...),
			ActiveNamespace:            activeNamespace,
			Columns:                    columns,
			ReplicaCollapsed:           &collapsed,
			HiddenRows:                 table.HiddenKeys(),
			Fullscreen:                 fullscreen,
			SortColumn:                 table.SortColumnID(),
			SortDescending:             table.SortDescending(),
			PrometheusURL:              sanitizePrometheusURL(prometheusURL),
			PrometheusSSLVerify:        &sslVerify,
			PrometheusTenantHeader:     cfg.Prometheus.TenantHeader,
			PrometheusBearerTokenEnv:   cfg.Prometheus.BearerTokenEnv,
			PrometheusBasicUsernameEnv: cfg.Prometheus.BasicUsernameEnv,
			PrometheusBasicPasswordEnv: cfg.Prometheus.BasicPasswordEnv,
			PrometheusLabelFilters:     copyLabelFilters(cfg.Prometheus.LabelFilters),
			QColumnsCustomized:         qColumnsCustomized,
		}
		if err := state.SaveFor(clusterName, contextName, value); err != nil {
			setErrorMessage("cluster state: " + err.Error())
		}
	}

	applyPrometheusState = func(value state.State) {
		// Built-in defaults and config.yaml provide global settings; the
		// per-context state overrides only the moved connection fields.
		cfg = baseConfig
		cfg.ApplyPrometheusOverride(config.PrometheusOverride{
			URL:              sanitizePrometheusURL(value.PrometheusURL),
			SSLVerify:        value.PrometheusSSLVerify,
			TenantHeader:     value.PrometheusTenantHeader,
			BearerTokenEnv:   value.PrometheusBearerTokenEnv,
			BasicUsernameEnv: value.PrometheusBasicUsernameEnv,
			BasicPasswordEnv: value.PrometheusBasicPasswordEnv,
			LabelFilters:     value.PrometheusLabelFilters,
		})
		prometheusURL = cfg.Prometheus.URL
		if prometheusURL == "" {
			prometheusStatus = ""
		}
		updateClusterInfo()
	}

	applyClusterState = func(cluster, context string) {
		key := cluster + "\x00" + context
		if cluster == "" || cluster == "unknown" || key == stateCluster {
			return
		}
		stateCluster = key

		value, err := state.LoadFor(cluster, context)
		if err != nil {
			setErrorMessage("cluster state: " + err.Error())
		}
		hasState := state.ExistsFor(cluster, context)
		if !legacyPrometheusMigrated && legacyPrometheus != nil {
			if migrated := migrateLegacyPrometheus(&value, legacyPrometheus); migrated {
				if saveErr := state.SaveFor(cluster, context, value); saveErr == nil {
					legacyPrometheusMigrated = true
					setStatusMessage("prometheus settings migrated to context state")
				} else {
					setErrorMessage("cluster state: " + saveErr.Error())
				}
			} else {
				legacyPrometheusMigrated = true
			}
		}
		namespaceSlots = normalizeNamespaceSlots(value.Namespaces)
		activeNamespace = normalizeNamespaceName(value.ActiveNamespace)
		if activeNamespace != "all" && !containsNamespace(namespaceSlots, activeNamespace) {
			namespaceSlots = prependNamespace(namespaceSlots, activeNamespace)
		}

		applyStateColumns(value.Columns)
		if hasState {
			if value.SortColumn != "" {
				table.SetSort(value.SortColumn, value.SortDescending)
			} else {
				table.SetSort(cfg.View.SortColumn, cfg.View.SortDescending)
			}
			fullscreen = value.Fullscreen
			qColumnsCustomized = value.QColumnsCustomized
		}
		applyPrometheusState(value)
		if value.ReplicaCollapsed != nil {
			table.SetReplicaCollapsed(*value.ReplicaCollapsed)
		}
		table.SetHiddenKeys(value.HiddenRows)
		table.SetNamespace(activeNamespace)
		rebuildLayout()
		updateStatusIndicator()
		refreshMenu()
	}

	mergeNamespaces = func(discovered []string) {
		changed := false
		for _, namespace := range discovered {
			if namespace == "" || namespace == "all" || containsNamespace(namespaceSlots, namespace) {
				continue
			}
			if len(namespaceSlots) >= 9 {
				break
			}
			namespaceSlots = append(namespaceSlots, namespace)
			changed = true
		}
		if activeNamespace != "all" && !containsNamespace(namespaceSlots, activeNamespace) {
			namespaceSlots = prependNamespace(namespaceSlots, activeNamespace)
			changed = true
		}
		refreshMenu()
		if changed {
			persistViewState()
		}
	}

	selectNamespace = func(namespace string, promote bool) {
		namespace = normalizeNamespaceName(namespace)
		if namespace == "all" {
			activeNamespace = "all"
		} else {
			activeNamespace = namespace
			if promote || !containsNamespace(namespaceSlots, namespace) {
				namespaceSlots = prependNamespace(namespaceSlots, namespace)
			}
		}

		table.SetNamespace(activeNamespace)
		persistViewState()
		updateStatusIndicator()
		refreshMenu()
		setStatusMessage("namespace: " + activeNamespace)
	}

	activeView := "main"
	buildNamespaceHints := func() []ui.MenuHint {
		hints := make([]ui.MenuHint, 0, 10)
		hints = append(hints, ui.MenuHint{Key: "0", Description: "all", Digit: true})
		for index, namespace := range namespaceSlots {
			if index >= 9 {
				break
			}
			hints = append(hints, ui.MenuHint{
				Key:         fmt.Sprintf("%d", index+1),
				Description: namespace,
				Digit:       true,
			})
		}
		return hints
	}
	buildShortcutHints := func() []ui.MenuHint {
		switch activeView {
		case "help":
			return []ui.MenuHint{
				{Key: reg.Key(registry.ShortcutHelp), Description: "close help"},
				{Key: "esc", Description: "close help"},
			}
		case "status":
			return []ui.MenuHint{
				{Key: "esc", Description: "close status"},
				{Key: "up/down", Description: "scroll status"},
			}
		case "prometheus-config":
			return []ui.MenuHint{
				{Key: "enter", Description: "save settings"},
				{Key: "tab", Description: "next field"},
				{Key: "esc", Description: "cancel"},
			}
		case "columns":
			return []ui.MenuHint{
				{Key: "space", Description: "toggle column"},
				{Key: "enter", Description: "save and close"},
				{Key: "esc", Description: "discard"},
			}
		case "contexts":
			return []ui.MenuHint{
				{Key: "enter", Description: "switch context"},
				{Key: "esc", Description: "close"},
			}
		case "config":
			return []ui.MenuHint{
				{Key: "enter", Description: "edit file"},
				{Key: "esc", Description: "cancel"},
			}
		}

		hints := make([]ui.MenuHint, 0, len(reg.Shortcuts))
		for _, shortcut := range reg.Shortcuts {
			switch shortcut.ID {
			case registry.ShortcutNamespaceAll, registry.ShortcutMoveDown, registry.ShortcutMoveUp:
				continue
			}
			hints = append(hints, ui.MenuHint{
				Key:         shortcut.Key,
				Description: shortcut.Description,
				Danger:      shortcut.ID == registry.ShortcutRefresh,
			})
		}
		return hints
	}
	refreshMenu = func() {
		hints := buildNamespaceHints()
		hints = append(hints, buildShortcutHints()...)
		header.SetHints(hints)
	}
	refreshMenu()

	setContainers := func(containers []model.Container) {
		currentContainers = containers
		table.SetContainers(containers)
		table.SetNamespace(activeNamespace)
		mergeNamespaces(containerNamespaces(containers))
		updateStatusIndicator()
		refreshMenu()
	}

	loadCached := loadCacheSnapshot
	saveCached := func(cluster, context string, containers []model.Container, nodes *client.NodeInventory) (string, time.Time, error) {
		fetchedAt := time.Now()
		cache := client.BuildCache(cluster, context, containers, fetchedAt)
		cache.Prometheus = prometheusMeta
		cache.Nodes = nodes
		path, err := client.SaveCache(cache)
		return path, cache.FetchedAt, err
	}
	applyPColumns := func(active bool) {
		if qColumnsCustomized {
			return
		}
		available := availablePrometheusColumns(cfg)
		visibility := table.ColumnVisibility()
		for _, column := range model.Columns {
			if !strings.HasPrefix(column.ID, "P") {
				continue
			}
			index := ui.ColumnIndex(column.ID)
			if index < 0 {
				continue
			}
			visibility[index] = active && containsString(available, column.ID)
		}
		table.SetColumnVisibility(visibility)
	}

	if clusterName != "unknown" {
		applyClusterState(clusterName, contextName)
	}

	setPrometheus := func(state ui.ConnectionState) {
		prometheusState = state
		updateClusterInfo()
		switch state {
		case ui.ConnectionConnected:
			header.Logo().Info("Prometheus connected")
		case ui.ConnectionError, ui.ConnectionDisconnected:
			header.Logo().Error("Prometheus unavailable")
		case ui.ConnectionConnecting:
			header.Logo().Info("Checking Prometheus")
		default:
			header.Logo().Reset()
		}
	}
	applyCachedPrometheus := func(snapshot client.Cache, containers []model.Container) {
		if !client.HasPrometheus(containers) {
			applyPColumns(false)
			return
		}
		if snapshot.Prometheus != nil {
			prometheusMeta = snapshot.Prometheus
			prometheusStatus = "cached " + humanAge(snapshot.Prometheus.FetchedAt)
		}
		stale := snapshot.Prometheus == nil ||
			snapshot.Prometheus.Fingerprint != client.PrometheusFingerprint(cfg.Prometheus, cfg.Resources)
		client.MarkPrometheusStale(containers, stale)
		applyPColumns(true)
		if stale {
			prometheusStatus += " stale"
		}
		prometheusState = ui.ConnectionDisconnected
		updateClusterInfo()
	}
	setPrometheusURL := func(endpoint string) {
		url := strings.TrimSpace(endpoint)
		if err := config.ValidatePrometheusURL(url); err != nil {
			setErrorMessage("prometheus: " + err.Error())
			return
		}
		prometheusURL = sanitizePrometheusURL(url)
		cfg.Prometheus.URL = prometheusURL
		prometheusStatus = ""
		if prometheusURL == "" {
			setPrometheus(ui.ConnectionNotConfigured)
		} else {
			setPrometheus(ui.ConnectionDisconnected)
			setStatusMessage("prometheus url saved: " + prometheusURL)
		}
		persistViewState()
	}

	promptPrometheusURL := func(connect bool) {
		prometheusPromptConnect = connect
		setMode(barPrompt)
		bar.SetText(prometheusURL)
		a.SetFocus(bar)
	}

	connectPrometheus := func(endpoint string) {
		url := strings.TrimSpace(endpoint)
		if err := config.ValidatePrometheusURL(url); err != nil {
			setErrorMessage("prometheus: " + err.Error())
			return
		}
		if clusterState != ui.ConnectionConnected {
			setErrorMessage("kubernetes is offline; connect to kubernetes before prometheus")
			prometheusStatus = "kubernetes offline"
			updateClusterInfo()
			return
		}
		prometheusClient, err := client.NewPrometheusClient(prometheusOptions(cfg, url))
		if err != nil {
			setPrometheus(ui.ConnectionError)
			setErrorMessage("prometheus: " + err.Error())
			applyPColumns(client.HasPrometheus(currentContainers))
			return
		}
		prometheusURL = url
		cfg.Prometheus.URL = url
		prometheusStatus = "connecting"
		setPrometheus(ui.ConnectionConnecting)
		setStatusMessage("● connecting Prometheus...")
		if prometheusCancel != nil {
			prometheusCancel()
		}
		ctx, cancel := context.WithCancel(context.Background())
		prometheusCancel = cancel
		prometheusBusy = true
		cfgSnapshot := cfg
		containers := append([]model.Container(nil), currentContainers...)
		nodesSnapshot := currentNodeInventory
		cluster, contextNameSnapshot := clusterName, contextName
		namespaces := client.PrometheusNamespaces(containers)
		batches := client.PrometheusNamespaceBatches(namespaces, client.PrometheusNamespaceBatchSize)
		guard.Go(func() {
			if err := prometheusClient.Probe(ctx); err != nil {
				a.QueueUpdateDraw(func() {
					prometheusBusy = false
					prometheusStatus = ""
					setPrometheus(ui.ConnectionError)
					setErrorMessage("prometheus: " + err.Error())
					applyPColumns(client.HasPrometheus(currentContainers))
				})
				return
			}
			a.QueueUpdateDraw(func() {
				if ctx.Err() != nil {
					return
				}
				prometheusStatus = ""
				setPrometheus(ui.ConnectionConnected)
				setStatusMessage("Prometheus connected: " + url)
				if len(batches) == 0 {
					setStatusMessage("Prometheus connected: " + url + " (no namespaces to query)")
					return
				}
				setStatusMessage(fmt.Sprintf(
					"Prometheus queries started: %d namespaces in %d batches of up to %d",
					len(namespaces), len(batches), client.PrometheusNamespaceBatchSize,
				))
			})
			if ctx.Err() != nil {
				a.QueueUpdateDraw(func() {
					prometheusBusy = false
					setStatusMessage("Prometheus queries cancelled")
				})
				return
			}

			now := time.Now()
			updated := containers
			var queryPlan client.ResourceQueryPlan
			for index, batch := range batches {
				if ctx.Err() != nil {
					a.QueueUpdateDraw(func() {
						prometheusBusy = false
						setStatusMessage("Prometheus queries cancelled")
					})
					return
				}
				plan := client.BuildQueryPlanForNamespaces(cfgSnapshot.Prometheus, cfgSnapshot.Resources, batch)
				if queryPlan.CPU == nil && queryPlan.Memory == nil && queryPlan.OOM == nil {
					queryPlan = plan
				}
				results, err := client.ExecuteQueryPlan(ctx, prometheusClient, plan, cfgSnapshot.Prometheus.MaxConcurrent)
				if err != nil {
					if ctx.Err() != nil {
						a.QueueUpdateDraw(func() {
							prometheusBusy = false
							setStatusMessage("Prometheus queries cancelled")
						})
						return
					}
					a.QueueUpdateDraw(func() {
						prometheusBusy = false
						prometheusStatus = ""
						setPrometheus(ui.ConnectionError)
						setErrorMessage("prometheus queries failed: " + err.Error())
						applyPColumns(client.HasPrometheus(currentContainers))
					})
					return
				}
				updated = client.ApplyPrometheusResults(updated, plan, results, now)
				batchNumber := index + 1
				batchNames := strings.Join(batch, ", ")
				a.QueueUpdateDraw(func() {
					setStatusMessage(fmt.Sprintf(
						"Prometheus queries finished for: %s (batch %d/%d)",
						batchNames, batchNumber, len(batches),
					))
				})
			}

			rangeValue, step := planRangeStep(queryPlan, cfgSnapshot.Prometheus.QueryRange)
			meta := client.CachePrometheusFor(url, cfgSnapshot.Prometheus, cfgSnapshot.Resources, now, rangeValue, step)
			cache := client.BuildCache(cluster, contextNameSnapshot, updated, now)
			cache.Prometheus = meta
			cache.Nodes = nodesSnapshot
			cachePath, cacheErr := client.SaveCache(cache)
			a.QueueUpdateDraw(func() {
				prometheusBusy = false
				prometheusMeta = meta
				prometheusURL = url
				prometheusStatus = ""
				cfg.Prometheus.URL = url
				setContainers(updated)
				table.SetCachePath(cachePath)
				setPrometheus(ui.ConnectionConnected)
				applyPColumns(client.HasPrometheus(updated))
				if cacheErr != nil {
					setErrorMessage("cache: " + cacheErr.Error())
					return
				}
				if len(batches) == 0 {
					setStatusMessage("Prometheus connected: " + url + " (no namespaces to query)")
				} else {
					setStatusMessage(fmt.Sprintf(
						"Prometheus queries completed: all %d namespaces in %d batches",
						len(namespaces), len(batches),
					))
				}
				persistViewState()
			})
		})
	}

	autoConnectPrometheus := func() {
		switch prometheusAutoDecision(cfg.Prometheus.Enabled, false, prometheusPromptSuppressed, prometheusURL) {
		case prometheusSkip:
			if !cfg.Prometheus.Enabled {
				return
			}
			setPrometheus(ui.ConnectionNotConfigured)
			return
		case prometheusPrompt:
			if clusterState != ui.ConnectionConnected {
				prometheusStatus = "kubernetes offline"
				updateClusterInfo()
				return
			}
			promptPrometheusURL(true)
			return
		}
		if clusterState != ui.ConnectionConnected {
			prometheusStatus = "kubernetes offline"
			updateClusterInfo()
			return
		}
		connectPrometheus(prometheusURL)
	}

	switchContext := func(name string) {
		name = strings.TrimSpace(name)
		if name == "" {
			if contextName != "" {
				setStatusMessage("context: " + contextName)
			} else {
				setStatusMessage("usage: :context <context>")
			}
			return
		}
		if kubeconfig != nil && len(kubeconfig.Contexts) > 0 && !containsString(kubeconfig.Contexts, name) {
			setErrorMessage("unknown context: " + name)
			return
		}
		if name == contextName {
			setStatusMessage("context: " + name)
			return
		}

		// Cancel all in-flight work from the previous session and invalidate
		// queued updates from it.
		requestGeneration++
		generation := requestGeneration
		if kubernetesCancel != nil {
			kubernetesCancel()
			kubernetesCancel = nil
		}
		if prometheusCancel != nil {
			prometheusCancel()
			prometheusCancel = nil
		}
		prometheusBusy = false
		prometheusPromptConnect = false
		setStatus(ui.ConnectionConnecting)
		setStatusMessage("● switching context to " + name + "...")

		snapshot := cfg
		path := activeKubeconfigPath
		guard.Go(func() {
			next, err := client.LoadKubeconfigWithContext(path, name, clientOptions(snapshot))
			if err == nil {
				err = next.CheckConnection(connectTimeout(snapshot))
			}
			targetCluster, targetContext := clusterName, name
			if next != nil {
				targetCluster, targetContext = next.Cluster, next.Context
			}
			cacheSnapshot, cachePath, cachedOK := loadCached(targetCluster, targetContext)
			cached := cacheSnapshot.Containers()
			cachedAt := cacheSnapshot.FetchedAt

			if err != nil {
				a.QueueUpdateDraw(func() {
					if generation != requestGeneration {
						return
					}
					if next != nil {
						kubeconfig = next
						activeKubeconfigPath = path
						clusterName = next.Cluster
						contextName = next.Context
					}
					setStatus(ui.ConnectionError)
					if cachedOK {
						applyClusterState(targetCluster, targetContext)
						table.SetCachePath(cachePath)
						currentNodeInventory = cacheSnapshot.Nodes
						applyCachedPrometheus(cacheSnapshot, cached)
						setPrometheus(ui.ConnectionDisconnected)
						setContainers(cached)
						setErrorMessage(cacheStatus("offline cache", cachedAt))
						return
					}
					setErrorMessage("context switch failed: " + err.Error())
				})
				return
			}

			live, nodes, listErr := loadContainers(next)
			if listErr == nil && cachedOK && cacheSnapshot.Prometheus != nil {
				// Keep cached Prometheus values for this context only.
				live = client.CarryPrometheus(cached, live)
			}
			var cacheErr error
			if listErr == nil {
				cachePath, cachedAt, cacheErr = saveCached(next.Cluster, next.Context, live, nodes)
			}
			a.QueueUpdateDraw(func() {
				if generation != requestGeneration {
					return
				}
				kubeconfig = next
				activeKubeconfigPath = path
				clusterName = next.Cluster
				contextName = next.Context
				applyClusterState(clusterName, contextName)
				setStatus(ui.ConnectionConnected)
				if listErr != nil {
					if cachedOK {
						table.SetCachePath(cachePath)
						currentNodeInventory = cacheSnapshot.Nodes
						applyCachedPrometheus(cacheSnapshot, cached)
						setPrometheus(ui.ConnectionDisconnected)
						setContainers(cached)
						setErrorMessage(cacheStatus("offline cache", cachedAt))
						return
					}
					setErrorMessage("context switch failed: " + listErr.Error())
					return
				}
				table.SetCachePath(cachePath)
				currentNodeInventory = nodes
				setContainers(live)
				prometheusStatus = ""
				if cachedOK && cacheSnapshot.Prometheus != nil {
					applyCachedPrometheus(cacheSnapshot, live)
					setPrometheus(ui.ConnectionDisconnected)
				} else {
					applyPColumns(client.HasPrometheus(live))
					setPrometheus(ui.ConnectionDisconnected)
					autoConnectPrometheus()
				}
				updateClusterInfo()
				if cacheErr != nil {
					setErrorMessage("cache: " + cacheErr.Error())
					return
				}
				setStatusMessage(cacheStatus("context: "+contextName+" cached", cachedAt))
			})
		})
	}

	contextsVisible := false
	showContexts := func() {
		if kubeconfig == nil || len(kubeconfig.Contexts) == 0 {
			setErrorMessage("no Kubernetes contexts available")
			return
		}
		contextList.Clear()
		currentIndex := 0
		for index, name := range kubeconfig.Contexts {
			label := name
			if name == contextName {
				label += "  (current)"
				currentIndex = index
			}
			contextList.AddItem(label, name, 0, nil)
		}
		contextList.SetCurrentItem(currentIndex)
		contextsVisible = true
		activeView = "contexts"
		refreshMenu()
		pages.ShowPage(contextsPage)
		a.SetFocus(contextList)
	}
	hideContexts := func() {
		contextsVisible = false
		activeView = "main"
		refreshMenu()
		pages.HidePage(contextsPage)
		a.SetFocus(table)
	}
	contextList.SetSelectedFunc(func(_ int, _ string, secondary string, _ rune) {
		name := strings.TrimSpace(secondary)
		hideContexts()
		if name != "" {
			switchContext(name)
		}
	})

	refreshAll := func() {
		snapshot := cfg
		path := activeKubeconfigPath
		if snapshot.Kubernetes.AccessMode == "file" && strings.TrimSpace(path) == "" {
			setStatus(ui.ConnectionError)
			setErrorMessage("kubernetes.kubeconfig_path is required when access_mode is \"file\"")
			return
		}

		setStatus(ui.ConnectionConnecting)
		setStatusMessage("● refreshing...")

		// Refresh covers every API source and keeps the selected context.
		requestGeneration++
		generation := requestGeneration
		snapshotContext := contextName
		guard.Go(func() {
			cfg, err := client.LoadKubeconfigWithContext(path, snapshotContext, clientOptions(snapshot))
			if err == nil {
				err = cfg.CheckConnection(connectTimeout(snapshot))
			}
			if err != nil {
				fallbackCluster, fallbackContext := clusterName, contextName
				if cfg != nil {
					fallbackCluster, fallbackContext = cfg.Cluster, cfg.Context
				}
				snapshot, cachePath, cachedOK := loadCached(fallbackCluster, fallbackContext)
				cached := snapshot.Containers()
				cachedAt := snapshot.FetchedAt
				a.QueueUpdateDraw(func() {
					if generation != requestGeneration {
						return
					}
					setStatus(ui.ConnectionError)
					if cachedOK {
						contextName = fallbackContext
						clusterName = fallbackCluster
						table.SetCachePath(cachePath)
						currentNodeInventory = snapshot.Nodes
						applyCachedPrometheus(snapshot, cached)
						setContainers(cached)
						setErrorMessage(cacheStatus("offline cache", cachedAt))
						return
					}
					setErrorMessage("refresh failed: " + err.Error())
				})
				return
			}

			containers, nodes, listErr := loadContainers(cfg)
			if listErr == nil {
				containers = client.CarryPrometheus(currentContainers, containers)
			}
			var cachePath string
			var cacheAt time.Time
			var cacheErr error
			if listErr == nil {
				cachePath, cacheAt, cacheErr = saveCached(cfg.Cluster, cfg.Context, containers, nodes)
			}
			a.QueueUpdateDraw(func() {
				if generation != requestGeneration {
					return
				}
				contextName = cfg.Context
				clusterName = cfg.Cluster
				applyClusterState(clusterName, contextName)
				setStatus(ui.ConnectionConnected)
				if listErr != nil {
					setErrorMessage("refresh failed: " + listErr.Error())
					return
				}
				kubeconfig = cfg
				table.SetCachePath(cachePath)
				currentNodeInventory = nodes
				setContainers(containers)
				if cacheErr != nil {
					setErrorMessage("cache: " + cacheErr.Error())
					return
				}
				setStatusMessage(cacheStatus("cached", cacheAt))
				autoConnectPrometheus()
			})
		})
	}

	refreshConfirmVisible := false
	confirmRefresh := func() {
		refreshConfirmVisible = true
		refreshConfirmModal.SetText(refreshConfirmation)
		refreshConfirmModal.ClearButtons()
		refreshConfirmModal.AddButtons([]string{"Yes", "No"})
		pages.ShowPage(refreshConfirmPage)
		a.SetFocus(refreshConfirmModal)
	}
	refreshConfirmModal.SetDoneFunc(func(index int, _ string) {
		refreshConfirmVisible = false
		pages.HidePage(refreshConfirmPage)
		a.SetFocus(table)
		if index == 0 {
			refreshAll()
		}
	})

	applyRuntimeConfig := func(next config.Config) error {
		// Build and validate the complete next runtime first. Nothing is
		// committed until the registry, bindings, and skin are all usable.
		nextRegistry := registry.Default().WithBindings(next.Keybindings)
		if err := nextRegistry.ValidateBindings(); err != nil {
			return err
		}
		nextSkin := skinFromConfig(next)

		kubernetesChanged := cfg.Kubernetes != next.Kubernetes
		baseConfig = next
		cfg = next
		if stateCluster != "" {
			if value, err := state.LoadFor(clusterName, contextName); err == nil {
				applyPrometheusState(value)
			}
		} else {
			prometheusURL = cfg.Prometheus.URL
			updateClusterInfo()
		}
		reg = nextRegistry
		applySkin(nextSkin)
		help.Update(reg)
		refreshMenu()
		if kubernetesChanged {
			activeKubeconfigPath, _ = kubeconfigSource(cfg)
			refreshAll()
			return nil
		}
		setStatusMessage("configuration applied")
		return nil
	}

	closeBar := func(clearFilter bool) {
		if clearFilter && mode == barFilter {
			table.SetFilter("")
		}
		setMode(barIdle)
		bar.SetText("")
		a.SetFocus(table)
	}

	openFilter := func() {
		setStatusMessage("")
		setMode(barFilter)
		bar.SetText(table.Filter())
		a.SetFocus(bar)
	}

	openCommand := func() {
		setStatusMessage("")
		setMode(barCommand)
		bar.SetText("")
		a.SetFocus(bar)
	}

	helpVisible := false
	showHelp := func() {
		helpVisible = true
		activeView = "help"
		refreshMenu()
		pages.ShowPage(helpPage)
		a.SetFocus(help)
	}
	hideHelp := func() {
		helpVisible = false
		activeView = "main"
		refreshMenu()
		pages.HidePage(helpPage)
		a.SetFocus(table)
	}

	statusVisible := false
	showStatus := func() {
		statusVisible = true
		activeView = "status"
		refreshMenu()
		if len(statusHistory) == 0 {
			statusView.SetText(" no status messages yet")
		} else {
			statusView.SetText(strings.Join(statusHistory, "\n"))
		}
		statusView.ScrollToBeginning()
		pages.ShowPage(statusPage)
		a.SetFocus(statusView)
	}
	hideStatus := func() {
		statusVisible = false
		activeView = "main"
		refreshMenu()
		pages.HidePage(statusPage)
		a.SetFocus(table)
	}

	prometheusConfigVisible := false
	prometheusDiscardVisible := false
	prometheusFormOriginal := prometheusFormValues{}
	prometheusInput := func(index int) *tview.InputField {
		item, _ := prometheusForm.GetFormItem(index).(*tview.InputField)
		return item
	}
	prometheusCheckbox := func(index int) *tview.Checkbox {
		item, _ := prometheusForm.GetFormItem(index).(*tview.Checkbox)
		return item
	}
	readPrometheusForm := func() prometheusFormValues {
		return prometheusFormValues{
			URL:          prometheusInput(prometheusFormURL).GetText(),
			SSLVerify:    prometheusCheckbox(prometheusFormSSLVerify).IsChecked(),
			TenantHeader: prometheusInput(prometheusFormTenant).GetText(),
			BearerToken:  prometheusInput(prometheusFormBearer).GetText(),
			BasicUser:    prometheusInput(prometheusFormBasicUser).GetText(),
			BasicPass:    prometheusInput(prometheusFormBasicPassword).GetText(),
			LabelFilters: prometheusInput(prometheusFormLabels).GetText(),
		}
	}
	prometheusFormDirty := func() bool {
		return readPrometheusForm() != prometheusFormOriginal
	}
	showPrometheusConfig := func() {
		values := prometheusFormValues{
			URL:          sanitizePrometheusURL(cfg.Prometheus.URL),
			SSLVerify:    cfg.Prometheus.SSLVerify,
			TenantHeader: cfg.Prometheus.TenantHeader,
			BearerToken:  cfg.Prometheus.BearerTokenEnv,
			BasicUser:    cfg.Prometheus.BasicUsernameEnv,
			BasicPass:    cfg.Prometheus.BasicPasswordEnv,
			LabelFilters: config.FormatLabelFilters(cfg.Prometheus.LabelFilters),
		}
		prometheusInput(prometheusFormURL).SetText(values.URL)
		prometheusCheckbox(prometheusFormSSLVerify).SetChecked(values.SSLVerify)
		prometheusInput(prometheusFormTenant).SetText(values.TenantHeader)
		prometheusInput(prometheusFormBearer).SetText(values.BearerToken)
		prometheusInput(prometheusFormBasicUser).SetText(values.BasicUser)
		prometheusInput(prometheusFormBasicPassword).SetText(values.BasicPass)
		prometheusInput(prometheusFormLabels).SetText(values.LabelFilters)
		prometheusFormOriginal = values
		prometheusConfigVisible = true
		activeView = "prometheus-config"
		refreshMenu()
		pages.ShowPage(prometheusConfigPage)
		prometheusForm.SetFocus(prometheusFormURL)
		a.SetFocus(prometheusForm)
	}
	hidePrometheusConfig := func() {
		prometheusConfigVisible = false
		prometheusDiscardVisible = false
		activeView = "main"
		refreshMenu()
		pages.HidePage(prometheusConfigPage)
		pages.HidePage(discardPrometheusPage)
		a.SetFocus(table)
	}
	savePrometheusConfig := func() {
		values := readPrometheusForm()
		url := strings.TrimSpace(values.URL)
		if err := config.ValidatePrometheusURL(url); err != nil {
			setErrorMessage("prometheus: " + err.Error())
			return
		}
		for _, item := range []struct {
			label string
			value string
		}{
			{"bearer_token_env", values.BearerToken},
			{"basic_username_env", values.BasicUser},
			{"basic_password_env", values.BasicPass},
		} {
			if err := config.ValidateEnvironmentName(item.value); err != nil {
				setErrorMessage("prometheus " + item.label + ": " + err.Error())
				return
			}
		}
		filters, err := config.ParseLabelFilters(values.LabelFilters)
		if err != nil {
			setErrorMessage("prometheus label_filters: " + err.Error())
			return
		}

		oldFingerprint := client.PrometheusFingerprint(cfg.Prometheus, cfg.Resources)
		cfg.Prometheus.URL = sanitizePrometheusURL(url)
		cfg.Prometheus.SSLVerify = values.SSLVerify
		cfg.Prometheus.TenantHeader = strings.TrimSpace(values.TenantHeader)
		cfg.Prometheus.BearerTokenEnv = strings.TrimSpace(values.BearerToken)
		cfg.Prometheus.BasicUsernameEnv = strings.TrimSpace(values.BasicUser)
		cfg.Prometheus.BasicPasswordEnv = strings.TrimSpace(values.BasicPass)
		cfg.Prometheus.LabelFilters = filters
		prometheusURL = cfg.Prometheus.URL
		if client.PrometheusFingerprint(cfg.Prometheus, cfg.Resources) != oldFingerprint {
			client.MarkPrometheusStale(currentContainers, true)
		}
		if prometheusURL == "" {
			setPrometheus(ui.ConnectionNotConfigured)
		} else {
			setPrometheus(ui.ConnectionDisconnected)
		}
		prometheusStatus = ""
		persistViewState()
		hidePrometheusConfig()
		setStatusMessage("prometheus context settings saved")
	}
	discardPrometheusChanges := func() {
		prometheusDiscardVisible = true
		prometheusDiscardModal.SetText("discard pending Prometheus context changes?")
		prometheusDiscardModal.ClearButtons()
		prometheusDiscardModal.AddButtons([]string{"Discard", "Keep editing"})
		pages.ShowPage(discardPrometheusPage)
		a.SetFocus(prometheusDiscardModal)
	}
	prometheusDiscardModal.SetDoneFunc(func(index int, _ string) {
		prometheusDiscardVisible = false
		pages.HidePage(discardPrometheusPage)
		if index == 0 {
			hidePrometheusConfig()
			return
		}
		pages.ShowPage(prometheusConfigPage)
		a.SetFocus(prometheusForm)
	})
	cancelPrometheusConfig := func() {
		if prometheusFormDirty() {
			discardPrometheusChanges()
			return
		}
		hidePrometheusConfig()
	}

	columnsVisible := false
	columnDraft := table.ColumnVisibility()
	columnPickerIndexes := make([]int, 0, len(model.Columns))
	refreshColumnList := func() {
		selected := columnList.GetCurrentItem()
		columnList.Clear()
		columnPickerIndexes = columnPickerIndexes[:0]
		availableQ := availablePrometheusColumns(cfg)
		for index, column := range model.Columns {
			id := ui.ColumnID(index)
			if strings.HasPrefix(id, "P") && !containsString(availableQ, id) {
				continue
			}
			mark := "[ ]"
			if columnDraft[index] {
				mark = "[x]"
			}
			columnPickerIndexes = append(columnPickerIndexes, index)
			columnList.AddItem(fmt.Sprintf("%s %s %-14s [%s] %s", mark, id, column.Header, columnSource(id), column.Description), "", 0, nil)
		}
		if selected < 0 || selected >= len(columnPickerIndexes) {
			selected = 0
		}
		columnList.SetCurrentItem(selected)
	}
	toggleColumnDraft := func(listIndex int) {
		if listIndex < 0 || listIndex >= len(columnPickerIndexes) {
			return
		}
		index := columnPickerIndexes[listIndex]
		if index < 0 || index >= len(columnDraft) {
			return
		}
		columnDraft[index] = !columnDraft[index]
		refreshColumnList()
		columnList.SetCurrentItem(listIndex)
	}
	columnDraftDirty := func() bool {
		return visibilityDiffers(table.ColumnVisibility(), columnDraft)
	}
	showColumns := func() {
		columnDraft = table.ColumnVisibility()
		selected := columnList.GetCurrentItem()
		refreshColumnList()
		if selected < 0 || selected >= len(columnPickerIndexes) {
			selected = 0
		}
		columnList.SetCurrentItem(selected)
		columnsVisible = true
		activeView = "columns"
		refreshMenu()
		pages.ShowPage(columnsPage)
		a.SetFocus(columnList)
	}
	hideColumns := func() {
		columnsVisible = false
		activeView = "main"
		refreshMenu()
		pages.HidePage(columnsPage)
		pages.HidePage(discardColumnsPage)
		a.SetFocus(table)
	}
	applyColumns := func() {
		current := table.ColumnVisibility()
		for _, column := range model.Columns {
			if !strings.HasPrefix(column.ID, "P") {
				continue
			}
			index := ui.ColumnIndex(column.ID)
			if index >= 0 && index < len(current) && index < len(columnDraft) && current[index] != columnDraft[index] {
				qColumnsCustomized = true
			}
		}
		table.SetColumnVisibility(columnDraft)
		hideColumns()
		persistViewState()
		setStatusMessage("columns saved")
	}
	discardColumnsVisible := false
	discardColumnChanges := func() {
		discardColumnsVisible = true
		discardColumnsModal.SetText("discard pending column changes?")
		discardColumnsModal.ClearButtons()
		discardColumnsModal.AddButtons([]string{"Discard", "Keep editing"})
		pages.ShowPage(discardColumnsPage)
		a.SetFocus(discardColumnsModal)
	}
	discardColumnsModal.SetDoneFunc(func(index int, _ string) {
		discardColumnsVisible = false
		pages.HidePage(discardColumnsPage)
		if index == 0 {
			hideColumns()
			return
		}
		pages.ShowPage(columnsPage)
		a.SetFocus(columnList)
	})
	columnList.SetSelectedFunc(func(_ int, _ string, _ string, _ rune) {
		applyColumns()
	})

	configListVisible := false
	hideConfigList := func() {
		configListVisible = false
		activeView = "main"
		refreshMenu()
		pages.HidePage(configListPage)
		a.SetFocus(table)
	}

	warningVisible := false
	warningAllowsQuit := false
	warningModal.SetDoneFunc(func(index int, _ string) {
		warningVisible = false
		pages.HidePage(warningPage)
		a.SetFocus(table)
		if warningAllowsQuit && index == 0 {
			a.Stop()
		}
	})
	showConfigWarning := func(parseErr error, backupPath, configPath string, changed bool) {
		var b strings.Builder
		if parseErr != nil {
			b.WriteString("The configuration could not be parsed or validated.\n\n")
			b.WriteString(tview.Escape(parseErr.Error()))
			b.WriteString("\n\n")
		} else {
			b.WriteString("The configuration has changed.\n\n")
			b.WriteString("Kubernova must restart to apply the new effective configuration.\n\n")
		}
		b.WriteString("Configuration: ")
		b.WriteString(tview.Escape(configPath))
		b.WriteString("\n")
		if backupPath != "" {
			b.WriteString("Previous configuration backup: ")
			b.WriteString(tview.Escape(backupPath))
		} else {
			b.WriteString("Previous configuration: ")
			b.WriteString(tview.Escape(configPath))
		}

		warningAllowsQuit = changed && parseErr == nil
		warningModal.ClearButtons()
		if warningAllowsQuit {
			warningModal.SetText(b.String() + "\n\nQuit now and restart to use the updated configuration?")
			warningModal.AddButtons([]string{"Quit now", "Later"})
		} else {
			warningModal.SetText(b.String() + "\n\nFix the file with :option before restarting.")
			warningModal.AddButtons([]string{"Back"})
		}
		warningVisible = true
		pages.ShowPage(warningPage)
		a.SetFocus(warningModal)
	}

	editConfigFile := func(path string) {
		backupPath, err := config.Backup(path)
		if err != nil {
			hideConfigList()
			showConfigWarning(fmt.Errorf("create configuration backup: %w", err), "", path, false)
			return
		}

		command := editorCommand()
		var runErr error
		suspended := a.Suspend(func() {
			cmd := exec.Command(command[0], append(command[1:], path)...)
			cmd.Stdin = os.Stdin
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			runErr = cmd.Run()
		})
		if !suspended {
			hideConfigList()
			showConfigWarning(fmt.Errorf("could not suspend the terminal to start the editor"), backupPath, path, false)
			return
		}
		if runErr != nil {
			hideConfigList()
			showConfigWarning(fmt.Errorf("editor failed: %w", runErr), backupPath, path, false)
			return
		}

		loadedConfig, loadErr := config.Load()
		hideConfigList()
		if loadErr != nil {
			showConfigWarning(loadErr, backupPath, path, false)
			return
		}
		if bytes.Equal(bytes.TrimSpace(loadedConfig.EffectiveYAML), bytes.TrimSpace(activeEffective)) {
			setStatusMessage("configuration unchanged")
			a.SetFocus(table)
			return
		}

		unknownEqual, compareErr := config.UnknownEqual(activeEffective, loadedConfig.EffectiveYAML)
		if compareErr != nil {
			showConfigWarning(compareErr, backupPath, path, false)
			return
		}
		if !unknownEqual {
			showConfigWarning(nil, backupPath, path, true)
			return
		}

		if err := applyRuntimeConfig(loadedConfig.Config); err != nil {
			showConfigWarning(err, backupPath, path, false)
			return
		}
		activeEffective = loadedConfig.EffectiveYAML
		a.SetFocus(table)
	}

	openOptions := func() {
		if _, err := config.EnsureDefault(); err != nil {
			setErrorMessage("configuration: " + err.Error())
			return
		}
		_, effectivePath, err := config.Paths()
		if err != nil {
			setErrorMessage("configuration: " + err.Error())
			return
		}
		editConfigFile(effectivePath)
	}

	reloadConfiguration := func() {
		loadedConfig, err := config.Load()
		if err != nil {
			setErrorMessage("configuration reload failed: " + err.Error())
			return
		}
		if err := applyRuntimeConfig(loadedConfig.Config); err != nil {
			setErrorMessage("configuration reload failed: " + err.Error())
			return
		}
		activeEffective = loadedConfig.EffectiveYAML
		showConfigWarnings(loadedConfig.Warnings)
		setStatusMessage("configuration reloaded")
	}

	runExport := func(formatArg, pathArg string) {
		format, err := export.ParsedFormat(formatArg)
		if err != nil {
			setErrorMessage("export: " + err.Error())
			return
		}
		now := time.Now()
		cache := client.BuildCache(clusterName, contextName, currentContainers, now)
		cache.Prometheus = prometheusMeta
		cache.Nodes = currentNodeInventory
		cached := clusterState != ui.ConnectionConnected
		value := report.Build(cache, cfg, report.Metadata{
			PrometheusURL:  prometheusURL,
			PrometheusLive: prometheusState == ui.ConnectionConnected,
			ExportDate:     now,
		})
		sorted := ui.SortContainers(cache.Containers(), table.SortColumnID(), table.SortDescending())

		dir := strings.TrimSpace(pathArg)
		if dir == "" {
			dir, err = export.Dir(clusterName, contextName)
			if err != nil {
				setErrorMessage("export: " + err.Error())
				return
			}
		} else {
			dir = client.ExpandPath(dir)
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			setErrorMessage("export: " + err.Error())
			return
		}

		written := make([]string, 0, 2)
		writeJSON := format == export.FormatJSON || format == export.FormatAll
		writeMarkdown := format == export.FormatMarkdown || format == export.FormatAll
		if writeJSON {
			raw, err := export.RenderJSON(value, cache, cached, now)
			if err != nil {
				setErrorMessage("export json: " + err.Error())
				return
			}
			path := filepath.Join(dir, export.FileName(clusterName, contextName, now, "json"))
			if err := export.WriteAtomic(path, raw); err != nil {
				setErrorMessage("export json: " + err.Error())
				return
			}
			written = append(written, path)
		}
		if writeMarkdown {
			raw := export.RenderMarkdown(value, sorted, cached, now)
			path := filepath.Join(dir, export.FileName(clusterName, contextName, now, "md"))
			if err := export.WriteAtomic(path, raw); err != nil {
				setErrorMessage("export markdown: " + err.Error())
				return
			}
			written = append(written, path)
		}
		setStatusMessage("exported: " + strings.Join(written, ", "))
	}

	promptVisible := false
	connecting := false

	showKubeconfigPrompt := func(reason string) {
		promptVisible = true
		promptMessage.SetText(fmt.Sprintf(
			"[red]Cannot connect using the current kubeconfig.\n"+
				"[gray]%s",
			tview.Escape(reason),
		))
		pages.ShowPage(kubeconfigPage)
		a.SetFocus(pathInput)
	}

	hideKubeconfigPrompt := func() {
		promptVisible = false
		pages.HidePage(kubeconfigPage)
		a.SetFocus(table)
	}

	executeCommand := func(command string) {
		fields := strings.Fields(command)
		if len(fields) == 0 {
			closeBar(false)
			return
		}
		name := strings.ToLower(fields[0])
		args := fields[1:]

		registered, ok := reg.Command(name)
		if !ok {
			setErrorMessage("unknown command: " + name)
			closeBar(false)
			return
		}

		switch registered.Name {
		case registry.CommandQuit:
			a.Stop()
		case registry.CommandHelp:
			closeBar(false)
			showHelp()
		case registry.CommandColumns:
			closeBar(false)
			showColumns()
		case registry.CommandFullscreen:
			closeBar(false)
			toggleFullscreen()
		case registry.CommandOptions:
			closeBar(false)
			openOptions()
		case registry.CommandNamespace:
			closeBar(false)
			if len(args) == 0 {
				setStatusMessage("usage: :ns <namespace|all>")
				return
			}
			selectNamespace(args[0], true)
		case registry.CommandPrometheusURL:
			if len(args) == 0 {
				prometheusPromptSuppressed = false
				promptPrometheusURL(false)
				return
			}
			setPrometheusURL(args[0])
			closeBar(false)
		case registry.CommandPrometheusConfig:
			closeBar(false)
			showPrometheusConfig()
		case registry.CommandPrometheusConnect:
			if name != "prometheus" {
				if len(args) == 0 || !strings.EqualFold(args[0], "prometheus") {
					setErrorMessage("usage: :connect prometheus [url]")
					closeBar(false)
					return
				}
				args = args[1:]
			}
			url := ""
			if len(args) > 0 {
				url = args[0]
			}
			closeBar(false)
			if url != "" {
				connectPrometheus(url)
				return
			}
			prometheusPromptSuppressed = false
			promptPrometheusURL(true)
		case registry.CommandKubeconfigPath:
			closeBar(false)
			if len(args) > 0 {
				pathInput.SetText(args[0])
			}
			showKubeconfigPrompt("Enter a kubeconfig path.")
		case registry.CommandContext:
			closeBar(false)
			if len(args) == 0 {
				showContexts()
				return
			}
			switchContext(args[0])
		case registry.CommandRefresh:
			closeBar(false)
			confirmRefresh()
		case registry.CommandReload:
			closeBar(false)
			reloadConfiguration()
		case registry.CommandExport:
			closeBar(false)
			format := ""
			path := ""
			if len(args) > 0 {
				format = args[0]
			}
			if len(args) > 1 {
				path = args[1]
			}
			runExport(format, path)
		case registry.CommandStatus:
			closeBar(false)
			showStatus()
		default:
			setErrorMessage("unknown command: " + name)
			closeBar(false)
		}
	}

	bar.SetFocusFunc(func() {
		if mode == barIdle {
			setStatusMessage("")
			setMode(barCommand)
			bar.SetText("")
		}
	})

	bar.SetChangedFunc(func(text string) {
		if mode == barFilter {
			table.SetFilter(text)
		}
		if mode == barCommand {
			namespaces := append(containerNamespaces(currentContainers), namespaceSlots...)
			var contexts []string
			if kubeconfig != nil {
				contexts = kubeconfig.Contexts
			}
			bar.SetSuggestion(completionSuffix(text, commandCompletions(reg, text, namespaces, contexts)))
		} else {
			bar.SetSuggestion("")
		}
	})

	bar.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			if mode == barPrompt {
				prometheusPromptSuppressed = true
				if prometheusURL == "" {
					setPrometheus(ui.ConnectionNotConfigured)
				}
				setStatusMessage("prometheus url prompt dismissed")
			}
			closeBar(mode == barFilter)
			return
		}

		switch mode {
		case barFilter:
			closeBar(false)
		case barCommand:
			executeCommand(strings.TrimSpace(bar.GetText()))
		case barPrompt:
			url := strings.TrimSpace(bar.GetText())
			if err := config.ValidatePrometheusURL(url); err != nil {
				setErrorMessage("prometheus: " + err.Error())
				return
			}
			if url == "" {
				setErrorMessage("Prometheus URL cannot be empty")
				return
			}
			prometheusPromptSuppressed = false
			closeBar(false)
			if prometheusPromptConnect {
				connectPrometheus(url)
			} else {
				setPrometheusURL(url)
			}
		default:
			closeBar(false)
		}
	})

	pathInput.SetDoneFunc(func(key tcell.Key) {
		if key == tcell.KeyEscape {
			a.Stop()
			return
		}
		if key != tcell.KeyEnter || connecting {
			return
		}

		path := strings.TrimSpace(pathInput.GetText())
		if path == "" {
			promptMessage.SetText("[red]Kubeconfig path cannot be empty.")
			return
		}

		connecting = true
		pathInput.SetDisabled(true)
		setStatus(ui.ConnectionConnecting)
		promptMessage.SetText("[yellow]Connecting with [white]" + tview.Escape(path) + "[yellow] ...")

		snapshot := cfg
		guard.Go(func() {
			cfg, err := client.LoadKubeconfigWithOptions(path, clientOptions(snapshot))
			if err == nil {
				err = cfg.CheckConnection(connectTimeout(snapshot))
			}
			if err != nil {
				fallbackCluster, fallbackContext := clusterName, contextName
				if cfg != nil {
					fallbackCluster, fallbackContext = cfg.Cluster, cfg.Context
				}
				snapshot, cachePath, cachedOK := loadCached(fallbackCluster, fallbackContext)
				fallback := snapshot.Containers()
				cacheAt := snapshot.FetchedAt
				a.QueueUpdateDraw(func() {
					connecting = false
					pathInput.SetDisabled(false)
					if cfg != nil {
						contextName = cfg.Context
						clusterName = cfg.Cluster
					}
					setStatus(ui.ConnectionError)
					if cachedOK {
						hideKubeconfigPrompt()
						table.SetCachePath(cachePath)
						currentNodeInventory = snapshot.Nodes
						applyCachedPrometheus(snapshot, fallback)
						setContainers(fallback)
						setErrorMessage(cacheStatus("offline cache", cacheAt))
						return
					}
					promptMessage.SetText(
						"[red]Connection failed.\n[gray]" + tview.Escape(err.Error()),
					)
				})
				return
			}

			a.QueueUpdateDraw(func() {
				connecting = false
				pathInput.SetDisabled(false)
				kubeconfig = cfg
				activeKubeconfigPath = path
				contextName = cfg.Context
				clusterName = cfg.Cluster
				applyClusterState(clusterName, contextName)
				setContainers(nil)
				setStatus(ui.ConnectionConnected)
				setStatusMessage("● loading containers...")
				hideKubeconfigPrompt()
			})

			containers, nodes, listErr := loadContainers(cfg)
			if listErr == nil {
				containers = client.CarryPrometheus(currentContainers, containers)
			}
			var cachePath string
			var cacheAt time.Time
			var cacheErr error
			if listErr == nil {
				cachePath, cacheAt, cacheErr = saveCached(cfg.Cluster, cfg.Context, containers, nodes)
			}
			a.QueueUpdateDraw(func() {
				if listErr != nil {
					setErrorMessage("failed to load containers: " + listErr.Error())
					return
				}
				table.SetCachePath(cachePath)
				currentNodeInventory = nodes
				setContainers(containers)
				if cacheErr != nil {
					setErrorMessage("cache: " + cacheErr.Error())
					return
				}
				setStatusMessage(cacheStatus("cached", cacheAt))
				autoConnectPrometheus()
			})
		})
	})

	// shortcutActions maps registry shortcut identifiers to their behavior.
	// Returning an event passes it on to the focused primitive.
	consumed := func(fn func()) func() *tcell.EventKey {
		return func() *tcell.EventKey {
			fn()
			return nil
		}
	}
	shortcutActions := map[string]func() *tcell.EventKey{
		registry.ShortcutFullscreen:  consumed(toggleFullscreen),
		registry.ShortcutSelectLeft:  consumed(func() { table.MoveSortColumn(-1) }),
		registry.ShortcutSelectRight: consumed(func() { table.MoveSortColumn(1) }),
		registry.ShortcutSortAscending: consumed(func() {
			table.SetSortDescending(false)
			persistViewState()
		}),
		registry.ShortcutSortDescending: consumed(func() {
			table.SetSortDescending(true)
			persistViewState()
		}),
		registry.ShortcutFilter:  consumed(openFilter),
		registry.ShortcutCommand: consumed(openCommand),
		registry.ShortcutHelp:    consumed(showHelp),
		registry.ShortcutColumns: consumed(showColumns),
		registry.ShortcutHideRow: consumed(func() {
			if table.ToggleSelectedHidden() {
				persistViewState()
			}
		}),
		registry.ShortcutMoveDown: func() *tcell.EventKey {
			return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
		},
		registry.ShortcutMoveUp: func() *tcell.EventKey {
			return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
		},
		registry.ShortcutNamespaceAll: consumed(func() { selectNamespace("all", false) }),
		registry.ShortcutToggleReplicas: consumed(func() {
			table.ToggleReplicaCollapse()
			persistViewState()
		}),
		registry.ShortcutExport:           consumed(func() { runExport("all", "") }),
		registry.ShortcutRefresh:          consumed(confirmRefresh),
		registry.ShortcutOptions:          consumed(openOptions),
		registry.ShortcutPrometheusConfig: consumed(showPrometheusConfig),
	}
	for _, shortcut := range reg.Shortcuts {
		if _, ok := shortcutActions[shortcut.ID]; shortcut.Configurable && !ok {
			return fmt.Errorf("internal error: shortcut %q has no handler", shortcut.ID)
		}
	}

	// Arrows are default.
	a.SetInputCapture(func(ev *tcell.EventKey) *tcell.EventKey {
		// Modal buttons and list navigation are handled by the primitives.
		if warningVisible || discardColumnsVisible || refreshConfirmVisible || prometheusDiscardVisible {
			return ev
		}
		if prometheusBusy && ev.Key() == tcell.KeyEscape {
			if prometheusCancel != nil {
				prometheusCancel()
			}
			prometheusBusy = false
			setStatusMessage("prometheus operation cancelled")
			return nil
		}
		if configListVisible {
			if ev.Key() == tcell.KeyEscape {
				hideConfigList()
				return nil
			}
			return ev
		}
		if contextsVisible {
			if ev.Key() == tcell.KeyEscape {
				hideContexts()
				return nil
			}
			return ev
		}
		if columnsVisible {
			if ev.Key() == tcell.KeyEscape {
				if columnDraftDirty() {
					discardColumnChanges()
				} else {
					hideColumns()
				}
				return nil
			}
			if ev.Rune() == ' ' {
				toggleColumnDraft(columnList.GetCurrentItem())
				return nil
			}
			return ev
		}
		if helpVisible {
			if ev.Key() == tcell.KeyEscape || matchShortcut(ev, reg.Key(registry.ShortcutHelp)) {
				hideHelp()
				return nil
			}
			return ev
		}
		if statusVisible {
			if ev.Key() == tcell.KeyEscape || matchShortcut(ev, reg.Key(registry.ShortcutHelp)) {
				hideStatus()
				return nil
			}
			return ev
		}
		if prometheusConfigVisible {
			switch ev.Key() {
			case tcell.KeyEscape:
				cancelPrometheusConfig()
				return nil
			case tcell.KeyEnter:
				savePrometheusConfig()
				return nil
			}
			return ev
		}

		// Tab accepts the inline gray completion suffix.
		if bar.HasFocus() && ev.Key() == tcell.KeyTab {
			if suffix := bar.Suggestion(); suffix != "" {
				bar.SetText(bar.GetText() + suffix)
				bar.SetSuggestion("")
			}
			return nil
		}

		// While a popup or the bar is focused, let it handle every key.
		if promptVisible || bar.HasFocus() {
			return ev
		}

		// Every configurable shortcut is dispatched from the registry, so the
		// help page and header cannot list a key that does nothing.
		for _, shortcut := range reg.Shortcuts {
			if !shortcut.Configurable || !matchShortcut(ev, shortcut.Key) {
				continue
			}
			if action, ok := shortcutActions[shortcut.ID]; ok {
				return action()
			}
		}

		// 1-9 select header namespace slots.
		if ev.Rune() >= '1' && ev.Rune() <= '9' && ev.Modifiers() == tcell.ModNone {
			if index := int(ev.Rune() - '0'); index-1 < len(namespaceSlots) {
				selectNamespace(namespaceSlots[index-1], false)
			}
			return nil
		}

		// Esc clears an active filter.
		if ev.Key() == tcell.KeyEscape && table.Filter() != "" {
			table.SetFilter("")
			return nil
		}

		return ev
	})

	if cfgErr != nil {
		setErrorMessage("configuration: " + cfgErr.Error())
		_, effectivePath, _ := config.Paths()
		showConfigWarning(cfgErr, "", effectivePath, false)
	}

	if kubeconfigErr != nil {
		showKubeconfigPrompt(kubeconfigErr.Error())
	} else if snapshot, cachePath, ok := loadCached(kubeconfig.Cluster, kubeconfig.Context); ok {
		// Cache-first startup: an existing valid cache makes no API calls.
		contextName = kubeconfig.Context
		clusterName = kubeconfig.Cluster
		applyClusterState(clusterName, contextName)
		table.SetCachePath(cachePath)
		cached := snapshot.Containers()
		currentNodeInventory = snapshot.Nodes
		applyCachedPrometheus(snapshot, cached)
		setContainers(cached)
		setStatus(ui.ConnectionDisconnected)
		setStatusMessage(cacheStatus("cached", snapshot.FetchedAt))
		if snapshot.Prometheus == nil && cfg.Prometheus.Enabled {
			initial := cfg
			guard.Go(func() {
				err := kubeconfig.CheckConnection(connectTimeout(initial))
				a.QueueUpdateDraw(func() {
					if err != nil {
						setStatus(ui.ConnectionError)
						setErrorMessage("kubernetes offline; cached rows shown")
						return
					}
					setStatus(ui.ConnectionConnected)
					autoConnectPrometheus()
				})
			})
		}
	} else {
		initialConfig := cfg
		guard.Go(func() {
			if err := kubeconfig.CheckConnection(connectTimeout(initialConfig)); err != nil {
				snapshot, cachePath, cachedOK := loadCached(kubeconfig.Cluster, kubeconfig.Context)
				fallback := snapshot.Containers()
				cacheAt := snapshot.FetchedAt
				a.QueueUpdateDraw(func() {
					setStatus(ui.ConnectionError)
					if cachedOK {
						table.SetCachePath(cachePath)
						currentNodeInventory = snapshot.Nodes
						applyCachedPrometheus(snapshot, fallback)
						setContainers(fallback)
						setErrorMessage(cacheStatus("offline cache", cacheAt))
						return
					}
					showKubeconfigPrompt(err.Error())
				})
				return
			}

			a.QueueUpdateDraw(func() {
				contextName = kubeconfig.Context
				updateClusterInfo()
				setStatus(ui.ConnectionConnected)
				setStatusMessage("● loading containers...")
			})

			containers, nodes, err := loadContainers(kubeconfig)
			var cachePath string
			var cacheAt time.Time
			var cacheErr error
			if err == nil {
				cachePath, cacheAt, cacheErr = saveCached(kubeconfig.Cluster, kubeconfig.Context, containers, nodes)
			}
			a.QueueUpdateDraw(func() {
				if err != nil {
					setErrorMessage("failed to load containers: " + err.Error())
					return
				}
				table.SetCachePath(cachePath)
				currentNodeInventory = nodes
				setContainers(containers)
				if cacheErr != nil {
					setErrorMessage("cache: " + cacheErr.Error())
					return
				}
				setStatusMessage(cacheStatus("cached", cacheAt))
				autoConnectPrometheus()
			})
		})
	}

	a.SetBeforeDrawFunc(func(screen tcell.Screen) bool {
		width, _ := screen.Size()
		header.SetNarrow(width)
		return false
	})

	showConfigWarnings(loaded.Warnings)

	runErr := a.SetRoot(pages, true).Run()
	if panicErr := guard.Err(); panicErr != nil {
		return panicErr
	}
	return runErr
}

func skinFromConfig(cfg config.Config) ui.Skin {
	defaults := ui.DefaultSkin()
	return ui.Skin{
		BodyFG:   ui.ParseColor(cfg.Skin.Body.FGColor, defaults.BodyFG),
		BodyBG:   ui.ParseColor(cfg.Skin.Body.BGColor, defaults.BodyBG),
		Logo:     ui.ParseColor(cfg.Skin.Body.LogoColor, defaults.Logo),
		InfoFG:   ui.ParseColor(cfg.Skin.Info.FGColor, defaults.InfoFG),
		Section:  ui.ParseColor(cfg.Skin.Info.SectionColor, defaults.Section),
		MenuFG:   ui.ParseColor(cfg.Skin.Frame.Menu.FGColor, defaults.MenuFG),
		MenuKey:  ui.ParseColor(cfg.Skin.Frame.Menu.KeyColor, defaults.MenuKey),
		MenuNum:  ui.ParseColor(cfg.Skin.Frame.Menu.NumKeyColor, defaults.MenuNum),
		StatusOK: defaults.StatusOK,
		Error:    ui.ParseColor(cfg.Skin.Status.ErrorColor, defaults.Error),
		Warn:     ui.ParseColor(cfg.Skin.Status.WarnColor, defaults.Warn),
		Info:     ui.ParseColor(cfg.Skin.Status.InfoColor, defaults.Info),
	}
}

func clientOptions(cfg config.Config) client.Options {
	return client.Options{
		RequestTimeout: time.Duration(cfg.Kubernetes.RequestTimeout),
		PageSize:       cfg.Kubernetes.PageSize,
	}
}

func connectTimeout(cfg config.Config) time.Duration {
	if cfg.Kubernetes.RequestTimeout > 0 {
		return time.Duration(cfg.Kubernetes.RequestTimeout)
	}
	return client.ConnectTimeout
}

func kubeconfigSource(cfg config.Config) (string, error) {
	if cfg.Kubernetes.AccessMode == "file" {
		if strings.TrimSpace(cfg.Kubernetes.KubeconfigPath) == "" {
			return "", fmt.Errorf("kubernetes.kubeconfig_path is required when access_mode is \"file\"")
		}
		return cfg.Kubernetes.KubeconfigPath, nil
	}
	return "", nil
}

func loadKubeconfig(cfg config.Config) (*client.Kubeconfig, error) {
	path, err := kubeconfigSource(cfg)
	if err != nil {
		return nil, err
	}
	return client.LoadKubeconfigWithOptions(path, clientOptions(cfg))
}

func editorCommand() []string {
	for _, variable := range []string{"VISUAL", "EDITOR"} {
		if value := strings.TrimSpace(os.Getenv(variable)); value != "" {
			if fields := strings.Fields(value); len(fields) > 0 {
				return fields
			}
		}
	}
	return []string{"vi"}
}

func visibilityDiffers(current, draft []bool) bool {
	if len(current) != len(draft) {
		return true
	}
	for index := range current {
		if current[index] != draft[index] {
			return true
		}
	}
	return false
}

func loadCacheSnapshot(cluster, context string) (client.Cache, string, bool) {
	cache, path, err := client.LoadCache(cluster, context)
	if err != nil {
		return client.Cache{}, path, false
	}
	return cache, path, true
}

func cacheStatus(prefix string, at time.Time) string {
	if at.IsZero() {
		return prefix
	}
	return prefix + ": " + at.Local().Format("2006-01-02 15:04:05")
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func humanAge(at time.Time) string {
	if at.IsZero() {
		return "-"
	}
	age := time.Since(at)
	if age < time.Minute {
		return "<1m"
	}
	if age < time.Hour {
		return fmt.Sprintf("%dm", int(age.Minutes()))
	}
	if age < 24*time.Hour {
		return fmt.Sprintf("%dh", int(age.Hours()))
	}
	return fmt.Sprintf("%dd", int(age.Hours()/24))
}
