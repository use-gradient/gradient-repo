package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gradient/gradient/pkg/livectx"
)

// gradient-agent runs on each cloud server (the host, not inside the container).
// v0.1: Linux only. The agent runs on Linux cloud servers (Hetzner, AWS, etc.)
// with Docker. macOS/Windows support is planned for v0.2+.
//
// It periodically snapshots the Docker container, reports health to the Gradient API,
// and participates in the Live Context Mesh for real-time context sharing.
//
// Environment variables:
//   GRADIENT_API_URL           — Gradient API URL (e.g. https://api.usegradient.dev)
//   GRADIENT_ENV_ID            — Environment ID
//   GRADIENT_ORG_ID            — Organization ID
//   GRADIENT_AUTH_TOKEN        — Auth token for API calls
//   GRADIENT_ENV_NAME          — Environment name (for tagging)
//   GRADIENT_BRANCH            — Git branch this env is on (for context mesh scoping)
//   GRADIENT_REGISTRY_URL      — Container registry URL for pushing snapshots
//   GRADIENT_REGISTRY_USER     — Registry username
//   GRADIENT_REGISTRY_PASS     — Registry password
//   GRADIENT_SNAPSHOT_INTERVAL  — Snapshot interval (default: 15m)
//   GRADIENT_HEALTH_INTERVAL   — Health report interval (default: 1m)
//   GRADIENT_NATS_URL          — NATS server URL for Live Context Mesh
//   GRADIENT_NATS_AUTH_TOKEN   — Optional NATS auth token
//   GRADIENT_CONTEXT_DIR       — Directory to write received context (default: /gradient/context)
//   GRADIENT_WATCH_INTERVAL    — How often to scan for changes (default: 10s)
//   GRADIENT_PLATFORM          — Override platform detection (default: "linux", only "linux" supported in v0.1)

type AgentConfig struct {
	APIURL           string
	EnvID            string
	OrgID            string
	RepoFullName     string
	AuthToken        string
	EnvName          string
	Branch           string
	RegistryURL      string
	RegistryUser     string
	RegistryPass     string
	SnapshotInterval time.Duration
	HealthInterval   time.Duration
	NATSUrl          string
	NATSAuthToken    string
	ContextDir       string
	WatchInterval    time.Duration
}

func loadConfig() *AgentConfig {
	snapshotInterval := 15 * time.Minute
	if v := os.Getenv("GRADIENT_SNAPSHOT_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			snapshotInterval = d
		}
	}

	healthInterval := 1 * time.Minute
	if v := os.Getenv("GRADIENT_HEALTH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			healthInterval = d
		}
	}

	watchInterval := 10 * time.Second
	if v := os.Getenv("GRADIENT_WATCH_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			watchInterval = d
		}
	}

	return &AgentConfig{
		APIURL:           getEnv("GRADIENT_API_URL", ""),
		EnvID:            getEnv("GRADIENT_ENV_ID", ""),
		OrgID:            getEnv("GRADIENT_ORG_ID", ""),
		RepoFullName:     getEnv("GRADIENT_REPO_FULL_NAME", ""),
		AuthToken:        getEnv("GRADIENT_AUTH_TOKEN", ""),
		EnvName:          getEnv("GRADIENT_ENV_NAME", "unknown"),
		Branch:           getEnv("GRADIENT_BRANCH", ""),
		RegistryURL:      getEnv("GRADIENT_REGISTRY_URL", ""),
		RegistryUser:     getEnv("GRADIENT_REGISTRY_USER", ""),
		RegistryPass:     getEnv("GRADIENT_REGISTRY_PASS", ""),
		SnapshotInterval: snapshotInterval,
		HealthInterval:   healthInterval,
		NATSUrl:          getEnv("GRADIENT_NATS_URL", ""),
		NATSAuthToken:    getEnv("GRADIENT_NATS_AUTH_TOKEN", ""),
		ContextDir:       getEnv("GRADIENT_CONTEXT_DIR", "/gradient/context"),
		WatchInterval:    watchInterval,
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	cfg := loadConfig()

	log.Printf("[agent] gradient-agent starting (v0.1.0 — Linux only)")
	log.Printf("[agent] env=%s org=%s branch=%s snapshot_interval=%s",
		cfg.EnvName, cfg.OrgID, cfg.Branch, cfg.SnapshotInterval)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle signals for graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// Ensure context directory exists
	if cfg.ContextDir != "" {
		if err := os.MkdirAll(cfg.ContextDir, 0755); err != nil {
			log.Printf("[agent] Warning: failed to create context dir %s: %v", cfg.ContextDir, err)
		}
	}

	// Replay saved context from API on cold boot (install packages, apply configs)
	if cfg.APIURL != "" && cfg.Branch != "" {
		go replayContext(cfg)
	}

	// Start health reporting
	go healthLoop(ctx, cfg)

	// Start periodic snapshots
	go snapshotLoop(ctx, cfg)

	// Start Live Context Mesh
	var bus livectx.Bus
	var meshCloser func()
	if cfg.NATSUrl != "" && cfg.OrgID != "" && cfg.Branch != "" {
		var err error
		bus, meshCloser, err = startMesh(ctx, cfg)
		if err != nil {
			log.Printf("[agent] Live Context Mesh failed to start: %v (continuing without mesh)", err)
		} else {
			log.Printf("[agent] Live Context Mesh active on branch %s", cfg.Branch)
		}
	} else {
		if cfg.NATSUrl == "" {
			log.Printf("[agent] GRADIENT_NATS_URL not set — Live Context Mesh disabled")
		}
		if cfg.Branch == "" {
			log.Printf("[agent] GRADIENT_BRANCH not set — Live Context Mesh disabled")
		}
	}

	// Start change watcher (detects package installs, config changes, etc.)
	if bus != nil && cfg.OrgID != "" && cfg.Branch != "" {
		go watchLoop(ctx, cfg, bus)
	}

	// Start outbox watcher (picks up events from MCP server and publishes to NATS)
	if bus != nil && cfg.OrgID != "" && cfg.Branch != "" {
		go outboxLoop(ctx, cfg, bus)
	}

	// Start local health endpoint
	go func() {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			status := getContainerStatus()
			meshStatus := "disabled"
			if bus != nil {
				meshStatus = "active"
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]interface{}{
				"status":         status,
				"agent":          "ok",
				"version":        "0.1.0",
				"container_up":   status == "running",
				"env_name":       cfg.EnvName,
				"branch":         cfg.Branch,
				"mesh":           meshStatus,
				"mesh_connected": meshConnected,
				"disk_usage_pct": getDiskUsage(),
				"mem_usage_pct":  getMemUsage(),
				"cpu_usage_pct":  getCPUUsage(),
				"uptime_sec":     getUptimeSeconds(),
				"snapshot_count": snapshotCount,
				"time":           time.Now().UTC().Format(time.RFC3339),
			})
		})

		// Context endpoint — serves the live context file for agents to read
		mux.HandleFunc("/context", func(w http.ResponseWriter, r *http.Request) {
			contextFile := filepath.Join(cfg.ContextDir, "live.json")
			data, err := os.ReadFile(contextFile)
			if err != nil {
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(map[string]interface{}{
					"events": []interface{}{},
				})
				return
			}
			w.Header().Set("Content-Type", "application/json")
			w.Write(data)
		})

		log.Printf("[agent] Health endpoint listening on :8090")
		if err := http.ListenAndServe(":8090", mux); err != nil {
			log.Printf("[agent] Health endpoint error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sig := <-sigCh
	log.Printf("[agent] Received signal %s, shutting down...", sig)
	cancel()

	// Take a final snapshot before shutting down
	log.Printf("[agent] Taking final snapshot before shutdown...")
	if err := takeSnapshot(cfg, "shutdown"); err != nil {
		log.Printf("[agent] Final snapshot failed: %v", err)
	} else {
		log.Printf("[agent] Final snapshot completed")
	}

	// Close mesh
	if meshCloser != nil {
		meshCloser()
	}

	log.Printf("[agent] gradient-agent stopped")
}

// --- Live Context Mesh ---

// startMesh connects to NATS, subscribes to the branch's event stream,
// and sets up event handling.
func startMesh(ctx context.Context, cfg *AgentConfig) (livectx.Bus, func(), error) {
	bus, err := livectx.NewEventBus(livectx.BusConfig{
		URL:        cfg.NATSUrl,
		ClientName: fmt.Sprintf("gradient-agent-%s", cfg.EnvID),
		AuthToken:  cfg.NATSAuthToken,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to connect to NATS: %w", err)
	}

	// Create an event handler that processes incoming events
	handler := newEventHandler(cfg)

	// Subscribe to branch-scoped events
	consumerSuffix := fmt.Sprintf("agent-%s", cfg.EnvID)
	if err := bus.Subscribe(ctx, cfg.OrgID, cfg.Branch, consumerSuffix, handler.Handle); err != nil {
		bus.Close()
		return nil, nil, fmt.Errorf("failed to subscribe to branch events: %w", err)
	}

	meshConnected = true

	closer := func() {
		log.Printf("[agent] Closing mesh connection...")
		meshConnected = false
		bus.Close()
	}

	return bus, closer, nil
}

// --- Event Handler ---

type eventHandler struct {
	cfg         *AgentConfig
	mu          sync.Mutex
	liveContext *liveContextFile
}

type liveContextFile struct {
	Packages      map[string]string   `json:"packages"`       // name → version
	Configs       map[string]string   `json:"configs"`        // key → value
	Contracts     map[string]string   `json:"contracts"`      // contract_id → summary
	UrgentIssues  []operationalUpdate `json:"urgent_issues"`  // last urgent peer issues
	RecentUpdates []operationalUpdate `json:"recent_updates"` // compact unread/read delta source
	LastUpdate    time.Time           `json:"last_update"`
	LastSequence  int64               `json:"last_sequence"`
}

type operationalUpdate struct {
	Seq       int64           `json:"seq"`
	ID        string          `json:"id"`
	Type      string          `json:"type"`
	EnvID     string          `json:"env_id"`
	Summary   string          `json:"summary"`
	Data      json.RawMessage `json:"data"`
	Timestamp time.Time       `json:"timestamp"`
}

func newEventHandler(cfg *AgentConfig) *eventHandler {
	h := &eventHandler{
		cfg: cfg,
		liveContext: &liveContextFile{
			Packages:      make(map[string]string),
			Configs:       make(map[string]string),
			Contracts:     make(map[string]string),
			UrgentIssues:  []operationalUpdate{},
			RecentUpdates: []operationalUpdate{},
		},
	}

	// Load existing context file if present
	contextFile := filepath.Join(cfg.ContextDir, "live.json")
	if data, err := os.ReadFile(contextFile); err == nil {
		var existing liveContextFile
		if json.Unmarshal(data, &existing) == nil {
			h.liveContext = &existing
		}
	}

	return h
}

// Handle processes an incoming event from the mesh.
func (h *eventHandler) Handle(ctx context.Context, event *livectx.Event) error {
	// Skip events from our own environment (we already know about them)
	if event.EnvID == h.cfg.EnvID {
		return nil
	}

	log.Printf("[mesh] Received %s event from env %s: %s", event.Type, event.EnvID, string(event.Data))

	h.mu.Lock()
	defer h.mu.Unlock()

	seq := event.Sequence
	if seq <= 0 {
		seq = h.liveContext.LastSequence + 1
	}
	update := operationalUpdate{
		Seq:       seq,
		ID:        event.ID,
		Type:      string(event.Type),
		EnvID:     event.EnvID,
		Summary:   summarizeOperationalEvent(event),
		Data:      event.Data,
		Timestamp: event.Timestamp,
	}

	h.liveContext.RecentUpdates = append(h.liveContext.RecentUpdates, update)
	if len(h.liveContext.RecentUpdates) > 100 {
		h.liveContext.RecentUpdates = h.liveContext.RecentUpdates[len(h.liveContext.RecentUpdates)-100:]
	}

	// Type-specific handling
	switch event.Type {
	case livectx.EventPackageInstalled:
		var data livectx.PackageData
		if err := json.Unmarshal(event.Data, &data); err == nil {
			h.liveContext.Packages[data.Name] = data.Version
			// Auto-install the package in the background
			go h.autoInstallPackage(data)
		}

	case livectx.EventPackageRemoved:
		var data livectx.PackageData
		if err := json.Unmarshal(event.Data, &data); err == nil {
			delete(h.liveContext.Packages, data.Name)
		}

	case livectx.EventConfigChanged:
		var data livectx.ConfigData
		if err := json.Unmarshal(event.Data, &data); err == nil {
			h.liveContext.Configs[data.Key] = data.Value
			// Apply config change if scope allows
			if data.Scope == "env" || data.Scope == "" {
				go h.applyConfigChange(data)
			}
		}

	case livectx.EventContractUpdated:
		var data livectx.ContractUpdatedData
		if err := json.Unmarshal(event.Data, &data); err == nil && data.ContractID != "" {
			h.liveContext.Contracts[data.ContractID] = fmt.Sprintf("%s %s", data.Type, data.Action)
		}
	}

	if isUrgentOperationalEvent(event) {
		h.liveContext.UrgentIssues = append(h.liveContext.UrgentIssues, update)
		if len(h.liveContext.UrgentIssues) > 20 {
			h.liveContext.UrgentIssues = h.liveContext.UrgentIssues[len(h.liveContext.UrgentIssues)-20:]
		}
	}

	h.liveContext.LastUpdate = time.Now().UTC()
	h.liveContext.LastSequence = seq

	// Persist to disk
	return h.saveToDisk()
}

// saveToDisk writes the live context to the context directory.
func (h *eventHandler) saveToDisk() error {
	contextFile := filepath.Join(h.cfg.ContextDir, "live.json")
	data, err := json.MarshalIndent(h.liveContext, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal live context: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(contextFile), 0755); err != nil {
		return fmt.Errorf("failed to create context dir: %w", err)
	}

	return os.WriteFile(contextFile, data, 0644)
}

func summarizeOperationalEvent(event *livectx.Event) string {
	switch event.Type {
	case livectx.EventPackageInstalled:
		var data livectx.PackageData
		if json.Unmarshal(event.Data, &data) == nil {
			return fmt.Sprintf("Package %s installed via %s", data.Name, data.Manager)
		}
	case livectx.EventPackageRemoved:
		var data livectx.PackageData
		if json.Unmarshal(event.Data, &data) == nil {
			return fmt.Sprintf("Package %s removed", data.Name)
		}
	case livectx.EventConfigChanged:
		var data livectx.ConfigData
		if json.Unmarshal(event.Data, &data) == nil {
			return fmt.Sprintf("Config %s changed", data.Key)
		}
	case livectx.EventContractUpdated:
		var data livectx.ContractUpdatedData
		if json.Unmarshal(event.Data, &data) == nil {
			return fmt.Sprintf("Contract %s %s", data.ContractID, data.Action)
		}
	case livectx.EventDecisionMade:
		var data livectx.DecisionData
		if json.Unmarshal(event.Data, &data) == nil && data.Message != "" {
			return data.Message
		}
	case livectx.EventSubtaskMarked:
		var data livectx.SubtaskData
		if json.Unmarshal(event.Data, &data) == nil {
			return fmt.Sprintf("Subtask %s marked %s", data.Name, data.Outcome)
		}
	case livectx.EventErrorEncountered:
		var data livectx.ErrorData
		if json.Unmarshal(event.Data, &data) == nil && data.Error != "" {
			return data.Error
		}
	case livectx.EventTestFailed:
		var data livectx.TestData
		if json.Unmarshal(event.Data, &data) == nil {
			return fmt.Sprintf("Test failed: %s", data.Test)
		}
	}

	return fmt.Sprintf("%s event received", event.Type)
}

func isUrgentOperationalEvent(event *livectx.Event) bool {
	switch event.Type {
	case livectx.EventErrorEncountered,
		livectx.EventTestFailed,
		livectx.EventConflictDetected,
		livectx.EventBugDiscovered:
		return true
	case livectx.EventSubtaskMarked:
		var data livectx.SubtaskData
		if json.Unmarshal(event.Data, &data) == nil {
			return strings.EqualFold(data.Outcome, "failed") || strings.EqualFold(data.Outcome, "blocked")
		}
	case livectx.EventDecisionMade:
		var data livectx.DecisionData
		if json.Unmarshal(event.Data, &data) == nil {
			return strings.EqualFold(data.Outcome, "failed") || strings.EqualFold(data.Outcome, "blocked")
		}
	}
	return false
}

// autoInstallPackage attempts to install a package that a peer env discovered.
func (h *eventHandler) autoInstallPackage(pkg livectx.PackageData) {
	if pkg.Command == "" {
		// Construct install command from manager info (v0.1: pip/npm/apt)
		switch pkg.Manager {
		case "pip", "pip3":
			if pkg.Version != "" {
				pkg.Command = fmt.Sprintf("pip install %s==%s", pkg.Name, pkg.Version)
			} else {
				pkg.Command = fmt.Sprintf("pip install %s", pkg.Name)
			}
		case "npm":
			if pkg.Version != "" {
				pkg.Command = fmt.Sprintf("npm install %s@%s", pkg.Name, pkg.Version)
			} else {
				pkg.Command = fmt.Sprintf("npm install %s", pkg.Name)
			}
		case "apt", "apt-get":
			pkg.Command = fmt.Sprintf("apt-get install -y %s", pkg.Name)
		default:
			// v0.2: add cargo, go, conda, nix, yum, dnf, apk, brew
			log.Printf("[mesh] Unknown package manager %s — skipping auto-install of %s (v0.2)", pkg.Manager, pkg.Name)
			return
		}
	}

	log.Printf("[mesh] Auto-installing package %s (%s) from peer environment...", pkg.Name, pkg.Manager)

	// Run inside the container
	cmd := exec.Command("docker", "exec", "gradient-env", "sh", "-c", pkg.Command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		log.Printf("[mesh] Auto-install of %s failed: %v\n%s", pkg.Name, err, string(output))
	} else {
		log.Printf("[mesh] Auto-installed %s successfully", pkg.Name)
	}
}

// applyConfigChange applies a config change received from a peer.
// v0.1: Linux containers only.
func (h *eventHandler) applyConfigChange(cfg livectx.ConfigData) {
	log.Printf("[mesh] Applying config change: %s=%s", cfg.Key, cfg.Value)

	profileScript := fmt.Sprintf(
		"mkdir -p /etc/profile.d && echo 'export %s=%s' >> /etc/profile.d/gradient-mesh.sh",
		cfg.Key, cfg.Value)

	cmd := exec.Command("docker", "exec", "gradient-env", "sh", "-c", profileScript)
	if output, err := cmd.CombinedOutput(); err != nil {
		log.Printf("[mesh] Config change failed: %v\n%s", err, string(output))
	}
}

// --- Change Watcher ---

// watchLoop periodically scans the container for changes and publishes events.
func watchLoop(ctx context.Context, cfg *AgentConfig, bus livectx.Bus) {
	log.Printf("[agent] Starting change watcher (interval: %s)", cfg.WatchInterval)

	// State tracking for diff detection
	tracker := newChangeTracker()

	ticker := time.NewTicker(cfg.WatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			detectAndPublish(ctx, cfg, bus, tracker)
		}
	}
}

type changeTracker struct {
	mu          sync.Mutex
	pipPackages map[string]string // name → version
	npmPackages map[string]string
	aptPackages map[string]string
	envVars     map[string]string
	initialized bool
}

func newChangeTracker() *changeTracker {
	return &changeTracker{
		pipPackages: make(map[string]string),
		npmPackages: make(map[string]string),
		aptPackages: make(map[string]string),
		envVars:     make(map[string]string),
	}
}

func detectAndPublish(ctx context.Context, cfg *AgentConfig, bus livectx.Bus, tracker *changeTracker) {
	tracker.mu.Lock()
	defer tracker.mu.Unlock()

	// Check if container is running
	status := getContainerStatus()
	if status != "running" {
		return
	}

	// Detect pip packages
	detectPipChanges(ctx, cfg, bus, tracker)

	// Detect npm packages
	detectNpmChanges(ctx, cfg, bus, tracker)

	// Detect apt packages (less frequent — only first run + major changes)
	if !tracker.initialized {
		detectAptPackages(tracker)
		tracker.initialized = true
	}

	// Detect env var changes
	detectEnvChanges(ctx, cfg, bus, tracker)
}

func detectPipChanges(ctx context.Context, cfg *AgentConfig, bus livectx.Bus, tracker *changeTracker) {
	cmd := exec.Command("docker", "exec", "gradient-env", "pip", "list", "--format=json")
	output, err := cmd.Output()
	if err != nil {
		return // pip not available
	}

	var packages []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	if json.Unmarshal(output, &packages) != nil {
		return
	}

	current := make(map[string]string)
	for _, pkg := range packages {
		current[pkg.Name] = pkg.Version
	}

	// Detect new packages
	for name, version := range current {
		if _, existed := tracker.pipPackages[name]; !existed && tracker.initialized {
			event, err := livectx.NewEvent(livectx.EventPackageInstalled, cfg.OrgID, cfg.Branch, cfg.EnvID,
				livectx.PackageData{
					Manager: "pip",
					Name:    name,
					Version: version,
					Command: fmt.Sprintf("pip install %s==%s", name, version),
				})
			if err == nil {
				event.WithSource("agent-watcher")
				if pubErr := bus.Publish(ctx, event); pubErr != nil {
					log.Printf("[watcher] Failed to publish pip install event: %v", pubErr)
				} else {
					log.Printf("[watcher] Published: pip install %s==%s", name, version)
				}
			}
		}
	}

	// Detect removed packages
	for name := range tracker.pipPackages {
		if _, exists := current[name]; !exists && tracker.initialized {
			event, err := livectx.NewEvent(livectx.EventPackageRemoved, cfg.OrgID, cfg.Branch, cfg.EnvID,
				livectx.PackageData{
					Manager: "pip",
					Name:    name,
				})
			if err == nil {
				event.WithSource("agent-watcher")
				bus.Publish(ctx, event)
			}
		}
	}

	tracker.pipPackages = current
}

func detectNpmChanges(ctx context.Context, cfg *AgentConfig, bus livectx.Bus, tracker *changeTracker) {
	cmd := exec.Command("docker", "exec", "gradient-env", "npm", "list", "--json", "--depth=0")
	output, err := cmd.Output()
	if err != nil {
		return // npm not available or no package.json
	}

	var npmList struct {
		Dependencies map[string]struct {
			Version string `json:"version"`
		} `json:"dependencies"`
	}
	if json.Unmarshal(output, &npmList) != nil {
		return
	}

	current := make(map[string]string)
	for name, info := range npmList.Dependencies {
		current[name] = info.Version
	}

	// Detect new packages
	for name, version := range current {
		if _, existed := tracker.npmPackages[name]; !existed && tracker.initialized {
			event, err := livectx.NewEvent(livectx.EventPackageInstalled, cfg.OrgID, cfg.Branch, cfg.EnvID,
				livectx.PackageData{
					Manager: "npm",
					Name:    name,
					Version: version,
					Command: fmt.Sprintf("npm install %s@%s", name, version),
				})
			if err == nil {
				event.WithSource("agent-watcher")
				bus.Publish(ctx, event)
				log.Printf("[watcher] Published: npm install %s@%s", name, version)
			}
		}
	}

	tracker.npmPackages = current
}

func detectAptPackages(tracker *changeTracker) {
	// dpkg-query is Linux/Debian-specific. On other platforms (or in non-Debian containers)
	// this will simply return nothing, which is fine — pip/npm detection is cross-platform.
	cmd := exec.Command("docker", "exec", "gradient-env", "dpkg-query", "-W", "-f", "${Package}=${Version}\n")
	output, err := cmd.Output()
	if err != nil {
		// Not a Debian/Ubuntu container or dpkg not available — try rpm for RHEL/Fedora
		cmd = exec.Command("docker", "exec", "gradient-env", "rpm", "-qa", "--queryformat", "%{NAME}=%{VERSION}\n")
		output, err = cmd.Output()
		if err != nil {
			return // no system package manager detected — that's OK
		}
	}

	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			tracker.aptPackages[parts[0]] = parts[1]
		}
	}
}

func detectEnvChanges(ctx context.Context, cfg *AgentConfig, bus livectx.Bus, tracker *changeTracker) {
	cmd := exec.Command("docker", "exec", "gradient-env", "env")
	output, err := cmd.Output()
	if err != nil {
		return
	}

	current := make(map[string]string)
	for _, line := range strings.Split(string(output), "\n") {
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			current[parts[0]] = parts[1]
		}
	}

	// Only publish changes for important env vars (skip noisy ones)
	importantPrefixes := []string{"CUDA", "LD_LIBRARY", "PYTHONPATH", "NODE_PATH", "GOPATH", "PATH", "HOME"}
	for key, value := range current {
		isImportant := false
		for _, prefix := range importantPrefixes {
			if strings.HasPrefix(key, prefix) {
				isImportant = true
				break
			}
		}
		if !isImportant {
			continue
		}

		if oldValue, existed := tracker.envVars[key]; existed && oldValue != value && tracker.initialized {
			event, err := livectx.NewEvent(livectx.EventConfigChanged, cfg.OrgID, cfg.Branch, cfg.EnvID,
				livectx.ConfigData{
					Key:      key,
					Value:    value,
					OldValue: oldValue,
					Scope:    "env",
				})
			if err == nil {
				event.WithSource("agent-watcher")
				bus.Publish(ctx, event)
				log.Printf("[watcher] Published: config change %s", key)
			}
		}
	}

	tracker.envVars = current
}

// --- Outbox Watcher ---

// outboxLoop watches /gradient/context/outbox.jsonl inside the container for events
// published by the MCP context server. It reads new lines and publishes them to NATS.
func outboxLoop(ctx context.Context, cfg *AgentConfig, bus livectx.Bus) {
	outboxPath := filepath.Join(cfg.ContextDir, "outbox.jsonl")
	// The outbox file lives on the host at cfg.ContextDir (default /gradient/context).
	// The MCP server inside the container writes to the same path via the shared volume.
	// We also check inside the container in case the volume mount differs.
	containerOutbox := "/gradient/context/outbox.jsonl"

	log.Printf("[agent] Starting outbox watcher (host=%s, container=%s)", outboxPath, containerOutbox)

	var lastOffset int64
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Try host path first, then copy from container if not found
			data, err := os.ReadFile(outboxPath)
			if err != nil {
				// Try to copy from container
				cmd := exec.Command("docker", "cp", "gradient-env:"+containerOutbox, outboxPath)
				if cpErr := cmd.Run(); cpErr != nil {
					continue
				}
				data, err = os.ReadFile(outboxPath)
				if err != nil {
					continue
				}
			}

			if int64(len(data)) <= lastOffset {
				continue
			}

			newData := data[lastOffset:]
			lastOffset = int64(len(data))

			lines := strings.Split(string(newData), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}

				var entry struct {
					Type      string          `json:"type"`
					Message   string          `json:"message"`
					Timestamp string          `json:"timestamp"`
					Data      json.RawMessage `json:"data,omitempty"`
				}
				if json.Unmarshal([]byte(line), &entry) != nil {
					continue
				}

				eventType := mapOutboxType(entry.Type)
				eventData := map[string]interface{}{
					"message": entry.Message,
				}
				if entry.Data != nil {
					var extra map[string]interface{}
					if json.Unmarshal(entry.Data, &extra) == nil {
						for k, v := range extra {
							eventData[k] = v
						}
					}
				}
				if eventType == livectx.EventSubtaskMarked {
					if name, ok := eventData["subtask"]; ok {
						eventData["name"] = name
					} else {
						eventData["name"] = entry.Message
					}
					if _, ok := eventData["summary"]; !ok {
						eventData["summary"] = entry.Message
					}
				}

				evt, evtErr := livectx.NewEvent(eventType, cfg.OrgID, cfg.Branch, cfg.EnvID, eventData)
				if evtErr != nil {
					log.Printf("[agent] Failed to create outbox event: %v", evtErr)
					continue
				}
				evt.WithSource("mcp-context")
				if pubErr := bus.Publish(ctx, evt); pubErr != nil {
					log.Printf("[agent] Failed to publish outbox event: %v", pubErr)
				} else {
					log.Printf("[agent] Published outbox event: [%s] %s", entry.Type, entry.Message)
				}
			}
		}
	}
}

func mapOutboxType(t string) livectx.EventType {
	switch t {
	case "error_encountered":
		return livectx.EventErrorEncountered
	case "pattern_learned":
		return livectx.EventPatternLearned
	case "package_installed":
		return livectx.EventPackageInstalled
	case "config_changed":
		return livectx.EventConfigChanged
	case "decision_made":
		return livectx.EventDecisionMade
	case "subtask_marked":
		return livectx.EventSubtaskMarked
	case "contract_updated":
		return livectx.EventContractUpdated
	default:
		return livectx.EventCustom
	}
}

// --- Snapshots ---

// snapshotLoop periodically takes container snapshots.
// --- Context Replay (Cold Boot) ---

// replayContext fetches saved context from the API and replays package installs & config changes
// into the running container. This runs once at agent startup to restore the environment state.
func replayContext(cfg *AgentConfig) {
	log.Printf("[agent] Replaying saved context for branch %s...", cfg.Branch)

	// Wait for container to be ready
	for i := 0; i < 30; i++ {
		if getContainerStatus() == "running" {
			break
		}
		time.Sleep(2 * time.Second)
	}
	if getContainerStatus() != "running" {
		log.Printf("[agent] Container not running — skipping context replay")
		return
	}

	// Fetch saved context from API
	contextURL := fmt.Sprintf("%s/api/v1/contexts/%s", cfg.APIURL, cfg.Branch)
	if cfg.RepoFullName != "" {
		contextURL += "?repo_full_name=" + url.QueryEscape(cfg.RepoFullName)
	}
	req, err := http.NewRequest("GET", contextURL, nil)
	if err != nil {
		log.Printf("[agent] Failed to create context request: %v", err)
		return
	}
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	if cfg.OrgID != "" {
		req.Header.Set("X-Org-ID", cfg.OrgID)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[agent] Failed to fetch context: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Printf("[agent] Context API returned %d — no saved context", resp.StatusCode)
		return
	}

	var ctxData struct {
		InstalledPackages []struct {
			Manager string `json:"manager"`
			Name    string `json:"name"`
			Version string `json:"version"`
		} `json:"installed_packages"`
		GlobalConfigs map[string]string `json:"global_configs"`
		SummaryText   string            `json:"summary_text"`
		ChangeLogText string            `json:"change_log_text"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&ctxData); err != nil {
		log.Printf("[agent] Failed to decode context: %v", err)
		return
	}

	// Replay package installs (v0.1: pip/npm/apt)
	for _, pkg := range ctxData.InstalledPackages {
		installCmd := ""
		switch pkg.Manager {
		case "pip", "pip3":
			if pkg.Version != "" {
				installCmd = fmt.Sprintf("pip3 install %s==%s 2>/dev/null || pip3 install %s", pkg.Name, pkg.Version, pkg.Name)
			} else {
				installCmd = fmt.Sprintf("pip3 install %s", pkg.Name)
			}
		case "npm":
			if pkg.Version != "" {
				installCmd = fmt.Sprintf("npm install -g %s@%s 2>/dev/null || npm install -g %s", pkg.Name, pkg.Version, pkg.Name)
			} else {
				installCmd = fmt.Sprintf("npm install -g %s", pkg.Name)
			}
		case "apt", "apt-get":
			installCmd = fmt.Sprintf("apt-get install -y -qq %s", pkg.Name)
		default:
			// v0.2: add cargo, go, conda, nix, yum, dnf, apk, brew
			log.Printf("[agent] Unsupported package manager %q in v0.1 — skipping %s", pkg.Manager, pkg.Name)
			continue
		}

		cmd := exec.Command("docker", "exec", "gradient-env", "sh", "-c", installCmd)
		if output, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[agent] Context replay: failed to install %s %s (%s): %v\n%s", pkg.Manager, pkg.Name, installCmd, err, string(output))
		} else {
			log.Printf("[agent] Context replay: installed %s %s", pkg.Manager, pkg.Name)
		}
	}

	// Replay environment vars
	for k, v := range ctxData.GlobalConfigs {
		envCmd := fmt.Sprintf("echo 'export %s=%s' >> /etc/profile.d/gradient-context.sh", k, v)
		cmd := exec.Command("docker", "exec", "gradient-env", "sh", "-c", envCmd)
		if _, err := cmd.CombinedOutput(); err != nil {
			log.Printf("[agent] Context replay: failed to set env var %s: %v", k, err)
		}
	}

	contextDocument := strings.TrimSpace(strings.Join([]string{ctxData.SummaryText, ctxData.ChangeLogText}, "\n\n"))
	if contextDocument != "" {
		contextPath := filepath.Join(cfg.ContextDir, "context.md")
		if err := os.WriteFile(contextPath, []byte(contextDocument), 0644); err != nil {
			log.Printf("[agent] Context replay: failed to write materialized context: %v", err)
		} else {
			log.Printf("[agent] Context replay: wrote materialized context to %s", contextPath)
		}
	}

	log.Printf("[agent] Context replay complete: %d packages, %d env vars",
		len(ctxData.InstalledPackages), len(ctxData.GlobalConfigs))
}

// --- Snapshots ---

func snapshotLoop(ctx context.Context, cfg *AgentConfig) {
	if cfg.RegistryURL == "" {
		log.Printf("[agent] GRADIENT_REGISTRY_URL not set — periodic snapshots disabled")
		return
	}

	ticker := time.NewTicker(cfg.SnapshotInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			log.Printf("[agent] Periodic snapshot starting...")
			if err := takeSnapshot(cfg, "periodic"); err != nil {
				log.Printf("[agent] Periodic snapshot failed: %v", err)
			} else {
				log.Printf("[agent] Periodic snapshot completed")
			}
		}
	}
}

// scanExtraPaths checks for non-package-managed artifacts that docker commit/export
// captures but the watcher doesn't detect. This lets agents know about CUDA installs,
// Nix environments, conda envs, and custom binaries that would otherwise be invisible
// in the context store. Results are stored in the snapshot metadata and context.
//
// This is a perception win: even if we can't auto-replay these installs (they're not
// package-managed), agents can SEE them in context and avoid re-doing manual steps.
func scanExtraPaths(cfg *AgentConfig) map[string]interface{} {
	extraPaths := map[string]interface{}{}

	// Check for CUDA
	cmd := exec.Command("docker", "exec", "gradient-env", "sh", "-c",
		"ls -d /usr/local/cuda* 2>/dev/null | head -5")
	if output, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		extraPaths["cuda_paths"] = strings.Split(strings.TrimSpace(string(output)), "\n")
	}

	// Check for Nix
	cmd = exec.Command("docker", "exec", "gradient-env", "sh", "-c",
		"ls -d /nix /root/.nix-profile 2>/dev/null | head -5")
	if output, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		extraPaths["nix_paths"] = strings.Split(strings.TrimSpace(string(output)), "\n")
	}

	// Check for conda
	cmd = exec.Command("docker", "exec", "gradient-env", "sh", "-c",
		"ls -d /opt/conda /root/.conda /root/miniconda* /root/anaconda* 2>/dev/null | head -5")
	if output, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		extraPaths["conda_paths"] = strings.Split(strings.TrimSpace(string(output)), "\n")
	}

	// Check for custom binaries in /usr/local/bin (non-system)
	cmd = exec.Command("docker", "exec", "gradient-env", "sh", "-c",
		"find /usr/local/bin -maxdepth 1 -type f -newer /etc/hostname 2>/dev/null | head -20")
	if output, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		extraPaths["custom_binaries"] = strings.Split(strings.TrimSpace(string(output)), "\n")
	}

	// Check for Rust/cargo installs
	cmd = exec.Command("docker", "exec", "gradient-env", "sh", "-c",
		"ls -d /root/.cargo/bin 2>/dev/null && ls /root/.cargo/bin/ 2>/dev/null | head -10")
	if output, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		extraPaths["cargo_binaries"] = strings.Split(strings.TrimSpace(string(output)), "\n")
	}

	// Check for Go binaries
	cmd = exec.Command("docker", "exec", "gradient-env", "sh", "-c",
		"ls /root/go/bin/ 2>/dev/null | head -10")
	if output, err := cmd.Output(); err == nil && len(strings.TrimSpace(string(output))) > 0 {
		extraPaths["go_binaries"] = strings.Split(strings.TrimSpace(string(output)), "\n")
	}

	if len(extraPaths) > 0 {
		log.Printf("[agent] Extra paths detected: %v", extraPaths)
	}

	return extraPaths
}

// takeSnapshot uses a hybrid strategy for maximum reliability:
//  1. docker export (primary) — full filesystem capture, no issues with open files
//  2. docker commit (fallback) — faster but can be flaky with running processes
//
// docker export captures the COMPLETE container filesystem as a tar and re-imports it.
// This is more reliable than docker commit which can miss files held open by running processes,
// tmpfs mounts, and procfs state. The tradeoff is ~30% slower, but correctness > speed.
//
// Additionally, each snapshot scans for non-package-managed artifacts (CUDA, Nix, conda,
// custom binaries) and includes them in the snapshot metadata for context visibility.
func takeSnapshot(cfg *AgentConfig, snapshotType string) error {
	if cfg.RegistryURL == "" {
		return fmt.Errorf("registry URL not configured")
	}

	// Scan for extra paths before snapshot (CUDA, Nix, conda, custom binaries)
	extraPaths := scanExtraPaths(cfg)

	tag := fmt.Sprintf("%s-%s-%d", cfg.EnvName, snapshotType, time.Now().Unix())
	imageRef := fmt.Sprintf("%s:%s", cfg.RegistryURL, tag)

	// Registry login if credentials provided
	if cfg.RegistryUser != "" && cfg.RegistryPass != "" {
		registryDomain := strings.Split(cfg.RegistryURL, "/")[0]
		loginCmd := exec.Command("docker", "login",
			"--username", cfg.RegistryUser,
			"--password-stdin", registryDomain)
		loginCmd.Stdin = strings.NewReader(cfg.RegistryPass)
		if output, err := loginCmd.CombinedOutput(); err != nil {
			return fmt.Errorf("docker login failed: %w\n%s", err, string(output))
		}
	}

	// Strategy 1: docker export + import (more reliable for running containers)
	// Captures full filesystem as tar — no issues with open files, running processes
	exportErr := takeExportSnapshot(imageRef)

	if exportErr != nil {
		log.Printf("[agent] docker export failed (%v), falling back to docker commit...", exportErr)
		// Strategy 2: docker commit (faster but can miss open files)
		commitErr := takeCommitSnapshot(imageRef)
		if commitErr != nil {
			return fmt.Errorf("all snapshot strategies failed: export=%v, commit=%v", exportErr, commitErr)
		}
	}

	// Docker push
	pushCmd := exec.Command("docker", "push", imageRef)
	if output, err := pushCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker push failed: %w\n%s", err, string(output))
	}

	log.Printf("[agent] Snapshot pushed: %s", imageRef)
	snapshotCount++

	// Report snapshot to API (includes method used + extra paths)
	if cfg.APIURL != "" && cfg.EnvID != "" {
		reportSnapshot(cfg, imageRef, snapshotType, extraPaths)
	}

	return nil
}

// takeExportSnapshot captures the container filesystem via docker export + import.
// More reliable than docker commit — captures everything including files held open by processes.
func takeExportSnapshot(imageRef string) error {
	// docker export produces a tar of the complete filesystem
	// docker import reads that tar and creates a new image
	cmd := exec.Command("sh", "-c",
		fmt.Sprintf("docker export gradient-env | docker import - %s", imageRef))
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker export|import failed: %w\n%s", err, string(output))
	}
	log.Printf("[agent] Snapshot via docker export: %s", imageRef)
	return nil
}

// takeCommitSnapshot captures the container via docker commit (faster but less reliable).
func takeCommitSnapshot(imageRef string) error {
	cmd := exec.Command("docker", "commit", "gradient-env", imageRef)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker commit failed: %w\n%s", err, string(output))
	}
	log.Printf("[agent] Snapshot via docker commit: %s", imageRef)
	return nil
}

// reportSnapshot notifies the Gradient API about a new snapshot.
// Includes extra_paths metadata so the context store knows about CUDA, Nix, etc.
func reportSnapshot(cfg *AgentConfig, imageRef, snapshotType string, extraPaths map[string]interface{}) {
	payload := map[string]interface{}{
		"tag": fmt.Sprintf("agent-%s-%d", snapshotType, time.Now().Unix()),
	}
	if len(extraPaths) > 0 {
		payload["extra_paths"] = extraPaths
	}
	body, _ := json.Marshal(payload)

	url := fmt.Sprintf("%s/api/v1/environments/%s/snapshot", cfg.APIURL, cfg.EnvID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		log.Printf("[agent] Failed to create snapshot report request: %v", err)
		return
	}

	req.Header.Set("Content-Type", "application/json")
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}
	if cfg.OrgID != "" {
		req.Header.Set("X-Org-ID", cfg.OrgID)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[agent] Failed to report snapshot to API: %v", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		log.Printf("[agent] API returned %d when reporting snapshot", resp.StatusCode)
	} else {
		log.Printf("[agent] Snapshot reported to API successfully")
	}
}

// --- Health ---

// healthLoop periodically reports health to the Gradient API.
func healthLoop(ctx context.Context, cfg *AgentConfig) {
	if cfg.APIURL == "" {
		log.Printf("[agent] GRADIENT_API_URL not set — health reporting disabled")
		return
	}

	ticker := time.NewTicker(cfg.HealthInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			reportHealth(cfg)
		}
	}
}

// reportHealth sends health data to the API.
func reportHealth(cfg *AgentConfig) {
	status := getContainerStatus()

	// Gather system metrics
	diskPct := getDiskUsage()
	memPct := getMemUsage()
	cpuPct := getCPUUsage()
	uptimeSec := getUptimeSeconds()

	payload := map[string]interface{}{
		"status":         status,
		"container_up":   status == "running",
		"disk_usage_pct": diskPct,
		"mem_usage_pct":  memPct,
		"cpu_usage_pct":  cpuPct,
		"uptime_sec":     uptimeSec,
		"snapshot_count": snapshotCount,
		"mesh_connected": cfg.NATSUrl != "" && meshConnected,
		"agent_version":  "0.1.0",
		"branch":         cfg.Branch,
		"time":           time.Now().UTC().Format(time.RFC3339),
	}
	body, _ := json.Marshal(payload)

	if cfg.APIURL == "" || cfg.EnvID == "" {
		return
	}
	url := fmt.Sprintf("%s/api/v1/environments/%s/agent-health", cfg.APIURL, cfg.EnvID)
	req, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.AuthToken != "" {
		req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	}

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[agent] Health report failed: %v", err)
		return
	}
	resp.Body.Close()
}

// System metrics collection — Linux only (v0.1).
// macOS/Windows support planned for v0.2+.

var snapshotCount int
var meshConnected bool

func getDiskUsage() float64 {
	cmd := exec.Command("sh", "-c", "df --output=pcent / | tail -1 | tr -d ' %'")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	var v float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &v)
	return v
}

func getMemUsage() float64 {
	cmd := exec.Command("sh", "-c", "free | awk 'NR==2{printf \"%.1f\", $3/$2*100}'")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	var v float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &v)
	return v
}

func getCPUUsage() float64 {
	cmd := exec.Command("sh", "-c", "top -bn1 | head -3 | grep Cpu | awk '{print $2}'")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	var v float64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%f", &v)
	return v
}

func getUptimeSeconds() int64 {
	cmd := exec.Command("sh", "-c", "cat /proc/uptime | awk '{print int($1)}'")
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	var v int64
	fmt.Sscanf(strings.TrimSpace(string(output)), "%d", &v)
	return v
}

// getContainerStatus checks if the gradient-env container is running.
func getContainerStatus() string {
	cmd := exec.Command("docker", "inspect", "--format", "{{.State.Status}}", "gradient-env")
	output, err := cmd.Output()
	if err != nil {
		return "not_found"
	}
	return strings.TrimSpace(string(output))
}
