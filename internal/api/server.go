package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/gradient/gradient/internal/config"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/gradient/gradient/internal/services"
	gradctx "github.com/gradient/gradient/pkg/context"
	"github.com/gradient/gradient/pkg/env"
	"github.com/gradient/gradient/pkg/livectx"
	"github.com/gradient/gradient/pkg/secrets"
)

type Server struct {
	config         *config.Config
	db             *db.DB
	envService     *services.EnvService
	contextService *services.ContextService
	billingService *services.BillingService
	repoService    *services.RepoService
	orgService     *services.OrgService
	snapshotStore  *services.SnapshotStore
	authMiddleware *AuthMiddleware
	deviceAuth     *DeviceAuthStore
	secretSyncer   *secrets.SecretSyncer

	// Live Context Mesh
	eventStore    *livectx.EventStore
	eventBus      livectx.Bus
	meshPublisher *livectx.MeshPublisher

	// Autoscaling
	autoscaleService *services.AutoscaleService

	// Agent Tasks (Linear + Claude)
	linearService *services.LinearService
	claudeService *services.ClaudeService
	taskService   *services.TaskService

	// Rate limiting
	rateLimiter *RateLimiter
}

func NewServer(cfg *config.Config, database *db.DB) *Server {
	envRepo := env.NewRepository(database)

	// Hetzner provider (primary) — only initialized if configured
	var hetznerProvider env.Provider
	if cfg.HetznerAPIToken != "" {
		sshKeyIDs := parseIntList(cfg.HetznerSSHKeyIDs)
		firewallID := parseInt64(cfg.HetznerFirewallID)
		networkID := parseInt64(cfg.HetznerNetworkID)
		imageID := parseInt64(cfg.HetznerImageID)

		p, err := env.NewHetznerProvider(
			cfg.HetznerAPIToken,
			cfg.HetznerLocation,
			sshKeyIDs,
			cfg.HetznerSSHPrivKey,
			firewallID,
			networkID,
			cfg.RegistryURL,
			cfg.RegistryUser,
			cfg.RegistryPass,
			imageID,
			cfg.AgentDownloadURL,
		)
		if err == nil {
			hetznerProvider = p
			fmt.Println("[init] Hetzner provider initialized")
		} else {
			fmt.Printf("[init] Hetzner provider not available: %v\n", err)
		}
	}

	// AWS provider (legacy) — only initialized if configured
	var awsProvider env.Provider
	if cfg.AWSAccessKeyID != "" && cfg.AWSAmiID != "" {
		p, err := env.NewAWSProvider(
			cfg.AWSRegion,
			cfg.AWSAmiID,
			cfg.AWSSecurityGroupID,
			cfg.AWSSubnetID,
			cfg.AWSKeyPairName,
			cfg.AWSECRRepoURI,
			cfg.AWSInstanceProfile,
		)
		if err == nil {
			awsProvider = p
			fmt.Println("[init] AWS provider initialized (legacy)")
		} else {
			fmt.Printf("[init] AWS provider not available: %v\n", err)
		}
	}

	envService := services.NewEnvService(envRepo, hetznerProvider, awsProvider, services.EnvServiceConfig{
		APIURL:        cfg.APIURL,
		NATSUrl:       cfg.NATSUrl,
		NATSAuthToken: cfg.NATSAuthToken,
	})
	contextStore := gradctx.NewStore(database)
	contextService := services.NewContextService(contextStore)
	billingService := services.NewBillingService(database, cfg.StripeSecretKey, cfg.StripePriceSmallID, cfg.StripePriceMediumID, cfg.StripePriceLargeID, cfg.StripePriceGPUID)
	if !billingService.StripeConfigured() {
		fmt.Println("[init] ⚠️  WARNING: STRIPE_SECRET_KEY is not set — billing operations will fail!")
		fmt.Println("[init]    Even in development, Stripe must be configured. Use Stripe test keys (sk_test_...).")
		fmt.Println("[init]    Free tier checks will still work, but billing setup, invoices, and usage reporting will error.")
	} else {
		fmt.Println("[init] ✓ Stripe billing configured")
		if cfg.StripePriceSmallID == "" || cfg.StripePriceMediumID == "" || cfg.StripePriceLargeID == "" {
			fmt.Println("[init] ⚠️  WARNING: Some STRIPE_PRICE_*_ID env vars are missing — metered subscriptions may fail")
		}
	}
	repoService := services.NewRepoService(database, envService, cfg.GitHubAppWebhookSecret)
	orgService := services.NewOrgService(cfg.ClerkSecretKey)
	snapshotStore := services.NewSnapshotStore(database)
	authMiddleware := NewAuthMiddleware(cfg.Env, cfg.ClerkSecretKey, cfg.ClerkPEMPublicKey, cfg.ClerkJWKSURL, cfg.JWTSecret)

	// Vault client (optional)
	var secretSyncer *secrets.SecretSyncer
	if cfg.VaultAddr != "" && cfg.VaultToken != "" {
		vaultClient, err := secrets.NewVaultClient(cfg.VaultAddr, cfg.VaultToken)
		if err == nil {
			secretSyncer = secrets.NewSecretSyncer(vaultClient)
			fmt.Println("[init] Vault secret syncer initialized")
		} else {
			fmt.Printf("[init] Vault client not available: %v\n", err)
		}
	}

	// Live Context Mesh — NATS event bus + PostgreSQL event store
	eventStore := livectx.NewEventStore(database)
	var eventBus livectx.Bus
	if cfg.NATSUrl != "" {
		maxAge := 7 * 24 * time.Hour
		if d, err := time.ParseDuration(cfg.NATSMaxAge); err == nil {
			maxAge = d
		}
		bus, err := livectx.NewEventBus(livectx.BusConfig{
			URL:        cfg.NATSUrl,
			MaxAge:     maxAge,
			ClientName: "gradient-api",
			AuthToken:  cfg.NATSAuthToken,
		})
		if err != nil {
			fmt.Printf("[init] NATS event bus not available: %v (using local bus)\n", err)
			eventBus = livectx.NewLocalBus()
		} else {
			eventBus = bus
			fmt.Println("[init] NATS event bus connected (Live Context Mesh enabled)")
		}
	} else {
		eventBus = livectx.NewLocalBus()
		fmt.Println("[init] NATS not configured — using local event bus (Live Context Mesh in local mode)")
	}
	meshPublisher := livectx.NewMeshPublisher(eventBus, eventStore)

	// Autoscale service
	autoscaleService := services.NewAutoscaleService(database, envService)
	// Start the autoscale monitoring loop (evaluates every 30 seconds)
	autoscaleService.StartMonitor(30 * time.Second)

	// Linear integration
	linearService := services.NewLinearService(database, cfg.LinearClientID, cfg.LinearClientSecret, cfg.LinearRedirectURI)
	if linearService.Configured() {
		fmt.Println("[init] ✓ Linear integration configured")
	} else {
		fmt.Println("[init] Linear integration not configured (set LINEAR_CLIENT_ID + LINEAR_CLIENT_SECRET)")
	}

	// Claude service
	claudeService := services.NewClaudeService(database)
	fmt.Println("[init] ✓ Claude Code service initialized")

	// Task service (orchestrator)
	taskService := services.NewTaskService(database, envService, claudeService, linearService, contextService)
	fmt.Println("[init] ✓ Agent task service initialized")

	// Rate limiter
	rateLimiter := NewRateLimiter(DefaultRateLimitConfig())

	return &Server{
		config:           cfg,
		db:               database,
		envService:       envService,
		contextService:   contextService,
		billingService:   billingService,
		repoService:      repoService,
		orgService:       orgService,
		snapshotStore:    snapshotStore,
		authMiddleware:   authMiddleware,
		deviceAuth:       NewDeviceAuthStore(),
		secretSyncer:     secretSyncer,
		eventStore:       eventStore,
		eventBus:         eventBus,
		meshPublisher:    meshPublisher,
		autoscaleService: autoscaleService,
		linearService:    linearService,
		claudeService:    claudeService,
		taskService:      taskService,
		rateLimiter:      rateLimiter,
	}
}

func (s *Server) SetupRoutes(r *mux.Router) {
	// Global middleware
	r.Use(PanicRecovery) // Catch panics — log stack trace, return 500
	r.Use(CORSMiddleware)
	r.Use(s.rateLimiter.Middleware)
	r.Use(RequestLogger)

	// Webhook endpoint — NO AUTH (GitHub calls this directly, verified by HMAC signature)
	r.HandleFunc("/api/v1/webhooks/github", s.handleGitHubWebhook).Methods("POST")

	// Health check — no auth
	r.HandleFunc("/api/v1/health", s.handleHealth).Methods("GET")

	// Prometheus-compatible metrics — no auth (scrape target)
	r.HandleFunc("/metrics", s.handleMetrics).Methods("GET")

	// Serve agent binary (for dev — Hetzner servers download from here)
	r.HandleFunc("/agent/gradient-agent", s.handleServeAgent).Methods("GET")

	// Device auth flow — no auth (this IS the auth flow)
	r.HandleFunc("/api/v1/auth/device", s.handleDeviceAuthStart).Methods("POST")
	r.HandleFunc("/api/v1/auth/device/poll", s.handleDeviceAuthPoll).Methods("GET")
	r.HandleFunc("/auth/cli", s.handleAuthPage).Methods("GET")
	r.HandleFunc("/auth/cli/approve", s.handleDeviceAuthApprove).Methods("POST")

	// Root handler — catches Clerk sign-in redirect (?__clerk_handshake=...)
	// and sends the user back to /auth/cli with their device code via JS localStorage
	r.HandleFunc("/", s.handleClerkRedirect).Methods("GET")

	// Auth logout — best-effort, accepts auth but doesn't require it
	r.HandleFunc("/api/v1/auth/logout", s.handleAuthLogout).Methods("POST")

	// All other routes require authentication
	authenticated := r.PathPrefix("/api/v1").Subrouter()
	authenticated.Use(s.authMiddleware.Authenticate)

	// Environments
	authenticated.HandleFunc("/environments", s.handleCreateEnvironment).Methods("POST")
	authenticated.HandleFunc("/environments", s.handleListEnvironments).Methods("GET")
	authenticated.HandleFunc("/environments/{id}", s.handleGetEnvironment).Methods("GET")
	authenticated.HandleFunc("/environments/{id}", s.handleDestroyEnvironment).Methods("DELETE")
	authenticated.HandleFunc("/environments/{id}/snapshot", s.handleSnapshotEnvironment).Methods("POST")

	// Context
	authenticated.HandleFunc("/contexts", s.handleSaveContext).Methods("POST")
	authenticated.HandleFunc("/contexts", s.handleListContexts).Methods("GET")
	authenticated.HandleFunc("/contexts/{branch:.+}", s.handleGetContext).Methods("GET")
	authenticated.HandleFunc("/contexts/{branch:.+}", s.handleDeleteContext).Methods("DELETE")

	// Snapshots
	authenticated.HandleFunc("/snapshots", s.handleListSnapshots).Methods("GET")

	// Repos (GitHub integration)
	authenticated.HandleFunc("/repos", s.handleConnectRepo).Methods("POST")
	authenticated.HandleFunc("/repos", s.handleListRepos).Methods("GET")
	authenticated.HandleFunc("/repos/available", s.handleListAvailableRepos).Methods("GET")
	authenticated.HandleFunc("/repos/{id}", s.handleDisconnectRepo).Methods("DELETE")

	// Billing
	authenticated.HandleFunc("/billing/usage", s.handleGetUsage).Methods("GET")
	authenticated.HandleFunc("/billing/invoices", s.handleListInvoices).Methods("GET")
	authenticated.HandleFunc("/billing/setup", s.handleBillingSetup).Methods("POST")
	authenticated.HandleFunc("/billing/status", s.handleBillingStatus).Methods("GET")
	authenticated.HandleFunc("/billing/portal", s.handleBillingPortal).Methods("POST")
	authenticated.HandleFunc("/billing/payment-method", s.handleGetPaymentMethod).Methods("GET")

	// Organizations (Clerk SDK)
	authenticated.HandleFunc("/orgs", s.handleListOrgs).Methods("GET")
	authenticated.HandleFunc("/orgs", s.handleCreateOrg).Methods("POST")
	authenticated.HandleFunc("/orgs/members", s.handleListOrgMembers).Methods("GET")
	authenticated.HandleFunc("/orgs/invite", s.handleInviteOrgMember).Methods("POST")
	authenticated.HandleFunc("/orgs/members/{user_id}", s.handleRemoveOrgMember).Methods("DELETE")
	authenticated.HandleFunc("/orgs/members/{user_id}/role", s.handleUpdateOrgMemberRole).Methods("PATCH")
	authenticated.HandleFunc("/orgs/invitations", s.handleListOrgInvitations).Methods("GET")
	authenticated.HandleFunc("/orgs/invitations/{invitation_id}/revoke", s.handleRevokeOrgInvitation).Methods("POST")

	// Org Settings (registry, preferences)
	authenticated.HandleFunc("/orgs/settings/registry", s.handleGetOrgRegistry).Methods("GET")
	authenticated.HandleFunc("/orgs/settings/registry", s.handleSetOrgRegistry).Methods("PUT")
	authenticated.HandleFunc("/orgs/settings/registry", s.handleDeleteOrgRegistry).Methods("DELETE")

	// Secrets
	authenticated.HandleFunc("/secrets/sync", s.handleSecretSync).Methods("POST")

	// SSH Access
	authenticated.HandleFunc("/environments/{id}/ssh-info", s.handleGetSSHInfo).Methods("GET")

	// Agent Health (proxied from API to the agent running on the environment's host)
	authenticated.HandleFunc("/environments/{id}/health", s.handleEnvHealth).Methods("GET")
	authenticated.HandleFunc("/environments/{id}/agent-health", s.handleAgentHealthReport).Methods("POST")

	// Autoscaling
	authenticated.HandleFunc("/environments/{id}/autoscale", s.handleSetAutoscale).Methods("PUT")
	authenticated.HandleFunc("/environments/{id}/autoscale", s.handleGetAutoscale).Methods("GET")
	authenticated.HandleFunc("/environments/{id}/autoscale", s.handleDeleteAutoscale).Methods("DELETE")
	authenticated.HandleFunc("/environments/{id}/autoscale/status", s.handleGetAutoscaleStatus).Methods("GET")
	authenticated.HandleFunc("/environments/{id}/autoscale/history", s.handleGetAutoscaleHistory).Methods("GET")
	authenticated.HandleFunc("/autoscale/policies", s.handleListAutoscalePolicies).Methods("GET")

	// Live Context Mesh
	// NOTE: Literal paths MUST be registered before parameterized paths
	// to prevent {id} from matching "stream", "stats", "ws" etc.
	authenticated.HandleFunc("/events", s.handlePublishEvent).Methods("POST")
	authenticated.HandleFunc("/events", s.handleQueryEvents).Methods("GET")
	authenticated.HandleFunc("/events/stream", s.handleStreamEvents).Methods("GET") // SSE
	authenticated.HandleFunc("/events/ws", s.handleWebSocketEvents).Methods("GET")  // WebSocket
	authenticated.HandleFunc("/events/stats", s.handleEventStats).Methods("GET")
	authenticated.HandleFunc("/events/{id}", s.handleGetEvent).Methods("GET")
	authenticated.HandleFunc("/events/{id}/ack", s.handleAckEvent).Methods("POST")
	authenticated.HandleFunc("/mesh/health", s.handleMeshHealth).Methods("GET")

	// Rate limit stats
	authenticated.HandleFunc("/admin/ratelimit", s.handleRateLimitStats).Methods("GET")

	// ── Agent Tasks ──────────────────────────────────────────────────
	authenticated.HandleFunc("/tasks", s.handleCreateTask).Methods("POST")
	authenticated.HandleFunc("/tasks", s.handleListTasks).Methods("GET")
	authenticated.HandleFunc("/tasks/readiness", s.handleTaskReadiness).Methods("GET")
	authenticated.HandleFunc("/tasks/stats", s.handleTaskStats).Methods("GET")
	authenticated.HandleFunc("/tasks/settings", s.handleGetTaskSettings).Methods("GET")
	authenticated.HandleFunc("/tasks/settings", s.handleSaveTaskSettings).Methods("PUT")
	authenticated.HandleFunc("/tasks/{id}", s.handleGetTask).Methods("GET")
	authenticated.HandleFunc("/tasks/{id}/start", s.handleStartTask).Methods("POST")
	authenticated.HandleFunc("/tasks/{id}/complete", s.handleCompleteTask).Methods("POST")
	authenticated.HandleFunc("/tasks/{id}/fail", s.handleFailTask).Methods("POST")
	authenticated.HandleFunc("/tasks/{id}/cancel", s.handleCancelTask).Methods("POST")
	authenticated.HandleFunc("/tasks/{id}/retry", s.handleRetryTask).Methods("POST")
	authenticated.HandleFunc("/tasks/{id}/logs", s.handleGetTaskLogs).Methods("GET")

	// ── Integrations ─────────────────────────────────────────────────
	authenticated.HandleFunc("/integrations/linear", s.handleGetLinearConnection).Methods("GET")
	authenticated.HandleFunc("/integrations/linear", s.handleDeleteLinearConnection).Methods("DELETE")
	authenticated.HandleFunc("/integrations/linear/auth-url", s.handleLinearAuthURL).Methods("GET")
	authenticated.HandleFunc("/integrations/linear/callback", s.handleLinearCallback).Methods("POST")
	authenticated.HandleFunc("/integrations/claude", s.handleGetClaudeConfig).Methods("GET")
	authenticated.HandleFunc("/integrations/claude", s.handleSaveClaudeConfig).Methods("PUT")
	authenticated.HandleFunc("/integrations/claude", s.handleDeleteClaudeConfig).Methods("DELETE")
	authenticated.HandleFunc("/integrations/status", s.handleIntegrationStatus).Methods("GET")
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[api] failed to encode JSON response: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

// --- Health ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "ok",
		"version": "0.1.0",
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

// handleMetrics returns Prometheus-compatible metrics for monitoring.
// Exposes: boot times, snapshot success, warm pool size, request counts.
func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")

	// Environment counts by status
	var running, creating, failed, destroyed int
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM environments WHERE status = 'running'`).Scan(&running)
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM environments WHERE status = 'creating'`).Scan(&creating)
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM environments WHERE status = 'failed'`).Scan(&failed)
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM environments WHERE status = 'destroyed'`).Scan(&destroyed)

	fmt.Fprintf(w, "# HELP gradient_environments_total Number of environments by status\n")
	fmt.Fprintf(w, "# TYPE gradient_environments_total gauge\n")
	fmt.Fprintf(w, "gradient_environments_total{status=\"running\"} %d\n", running)
	fmt.Fprintf(w, "gradient_environments_total{status=\"creating\"} %d\n", creating)
	fmt.Fprintf(w, "gradient_environments_total{status=\"failed\"} %d\n", failed)
	fmt.Fprintf(w, "gradient_environments_total{status=\"destroyed\"} %d\n", destroyed)

	// Boot time stats (from environments that have boot_time_ms in config)
	var warmCount, coldCount int
	var warmAvg, coldAvg, warmP95, coldP95 float64
	s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(AVG((config->>'boot_time_ms')::float), 0),
		       COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY (config->>'boot_time_ms')::float), 0)
		FROM environments WHERE config->>'boot_type' = 'warm' AND config->>'boot_time_ms' IS NOT NULL
	`).Scan(&warmCount, &warmAvg, &warmP95)
	s.db.Pool.QueryRow(ctx, `
		SELECT COUNT(*), COALESCE(AVG((config->>'boot_time_ms')::float), 0),
		       COALESCE(PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY (config->>'boot_time_ms')::float), 0)
		FROM environments WHERE config->>'boot_type' = 'cold' AND config->>'boot_time_ms' IS NOT NULL
	`).Scan(&coldCount, &coldAvg, &coldP95)

	fmt.Fprintf(w, "# HELP gradient_boot_time_ms Boot time in milliseconds\n")
	fmt.Fprintf(w, "# TYPE gradient_boot_time_ms gauge\n")
	fmt.Fprintf(w, "gradient_boot_time_avg_ms{type=\"warm\"} %.0f\n", warmAvg)
	fmt.Fprintf(w, "gradient_boot_time_avg_ms{type=\"cold\"} %.0f\n", coldAvg)
	fmt.Fprintf(w, "gradient_boot_time_p95_ms{type=\"warm\"} %.0f\n", warmP95)
	fmt.Fprintf(w, "gradient_boot_time_p95_ms{type=\"cold\"} %.0f\n", coldP95)
	fmt.Fprintf(w, "gradient_boot_count{type=\"warm\"} %d\n", warmCount)
	fmt.Fprintf(w, "gradient_boot_count{type=\"cold\"} %d\n", coldCount)

	// Warm pool stats
	var warmPoolReady, warmPoolWarming, warmPoolAssigned int
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM warm_pool WHERE status = 'ready'`).Scan(&warmPoolReady)
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM warm_pool WHERE status = 'warming'`).Scan(&warmPoolWarming)
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM warm_pool WHERE status = 'assigned'`).Scan(&warmPoolAssigned)

	fmt.Fprintf(w, "# HELP gradient_warm_pool_size Warm pool servers by status\n")
	fmt.Fprintf(w, "# TYPE gradient_warm_pool_size gauge\n")
	fmt.Fprintf(w, "gradient_warm_pool_size{status=\"ready\"} %d\n", warmPoolReady)
	fmt.Fprintf(w, "gradient_warm_pool_size{status=\"warming\"} %d\n", warmPoolWarming)
	fmt.Fprintf(w, "gradient_warm_pool_size{status=\"assigned\"} %d\n", warmPoolAssigned)

	// Snapshot counts
	var snapshotCount int
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM snapshots`).Scan(&snapshotCount)

	fmt.Fprintf(w, "# HELP gradient_snapshots_total Total snapshots taken\n")
	fmt.Fprintf(w, "# TYPE gradient_snapshots_total counter\n")
	fmt.Fprintf(w, "gradient_snapshots_total %d\n", snapshotCount)

	// Autoscale policies
	var autoscaleEnabled int
	s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM autoscale_policies WHERE enabled = true`).Scan(&autoscaleEnabled)

	fmt.Fprintf(w, "# HELP gradient_autoscale_policies_enabled Active autoscale policies\n")
	fmt.Fprintf(w, "# TYPE gradient_autoscale_policies_enabled gauge\n")
	fmt.Fprintf(w, "gradient_autoscale_policies_enabled %d\n", autoscaleEnabled)
}

// PanicRecovery is middleware that catches panics in HTTP handlers,
// logs the stack trace, and returns a 500 error instead of crashing.
func PanicRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[PANIC] %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, `{"error":"internal server error"}`, http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// --- Auth Logout ---

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	// Server-side logout is a no-op for JWT-based auth (tokens are stateless).
	// However, we log it for audit purposes and could add token revocation lists in the future.
	authHeader := r.Header.Get("Authorization")
	if authHeader != "" {
		log.Printf("[auth] token logout requested from %s", r.RemoteAddr)
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "logged_out",
		"message": "Local credentials should be cleared by the client.",
	})
}

// --- GitHub Webhook ---

func (s *Server) handleGitHubWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "failed to read request body")
		return
	}

	// Verify webhook signature
	signature := r.Header.Get("X-Hub-Signature-256")
	if !s.repoService.VerifyWebhookSignature(body, signature) {
		writeError(w, http.StatusUnauthorized, "invalid webhook signature")
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")
	if eventType == "" {
		writeError(w, http.StatusBadRequest, "missing X-GitHub-Event header")
		return
	}

	if err := s.repoService.HandleWebhookEvent(r.Context(), eventType, json.RawMessage(body)); err != nil {
		fmt.Printf("[webhook] Error handling %s event: %v\n", eventType, err)
		writeError(w, http.StatusInternalServerError, "webhook processing failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// --- Environments ---

type createEnvRequest struct {
	Name          string `json:"name"`
	Provider      string `json:"provider"`
	Region        string `json:"region"`
	Size          string `json:"size"`
	ContextBranch string `json:"context_branch"`
}

func (s *Server) handleCreateEnvironment(w http.ResponseWriter, r *http.Request) {
	var req createEnvRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	// Provider defaults are handled by EnvService (picks primary configured provider)
	if req.Region == "" {
		writeError(w, http.StatusBadRequest, "region is required")
		return
	}
	if req.Size == "" {
		req.Size = "small"
	}

	orgID := GetOrgID(r.Context())

	// ── Billing Gate ─────────────────────────────────────────────────────
	// Check free tier limits and payment method before allowing env creation.
	// Free tier: 20 hrs/mo, "small" only. After that, must add payment method.
	if billingErr := s.billingService.CheckBillingAllowed(r.Context(), orgID, req.Size); billingErr != nil {
		writeError(w, http.StatusPaymentRequired, billingErr.Error())
		return
	}

	// Check if there's a snapshot to restore from for this branch
	var snapshotRef string
	if req.ContextBranch != "" {
		snap, err := s.snapshotStore.GetLatestByBranch(r.Context(), orgID, req.ContextBranch)
		if err == nil && snap != nil {
			snapshotRef = snap.ImageRef
		}
	}

	newEnv, err := s.envService.CreateEnvironment(r.Context(), &services.CreateEnvRequest{
		Name:          req.Name,
		OrgID:         orgID,
		Provider:      req.Provider,
		Region:        req.Region,
		Size:          req.Size,
		ContextBranch: req.ContextBranch,
		SnapshotRef:   snapshotRef,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Track billing usage
	if trackErr := s.billingService.TrackUsageStart(r.Context(), newEnv.ID, orgID, req.Size); trackErr != nil {
		fmt.Printf("[billing] failed to track usage start: %v\n", trackErr)
	}

	writeJSON(w, http.StatusCreated, newEnv)
}

func (s *Server) handleListEnvironments(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	envs, err := s.envService.ListEnvironments(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if envs == nil {
		envs = []*models.Environment{}
	}

	writeJSON(w, http.StatusOK, envs)
}

func (s *Server) handleGetEnvironment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	envID := vars["id"]

	e, err := s.envService.GetEnvironment(r.Context(), envID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, e)
}

func (s *Server) handleDestroyEnvironment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	envID := vars["id"]
	orgID := GetOrgID(r.Context())

	// Pre-destroy snapshot: capture container state before termination
	e, err := s.envService.GetEnvironment(r.Context(), envID)
	if err == nil && e != nil && e.Status == "running" && e.ClusterName != "" {
		tag := fmt.Sprintf("pre-destroy-%d", time.Now().Unix())
		imageRef, snapErr := s.envService.SnapshotEnvironment(r.Context(), envID, orgID, tag)
		if snapErr != nil {
			fmt.Printf("[destroy] pre-destroy snapshot failed for env %s: %v (continuing with destroy)\n", envID, snapErr)
		} else {
			branch := e.ContextBranch
			snap := &models.Snapshot{
				ID:            uuid.New().String(),
				OrgID:         orgID,
				Branch:        branch,
				EnvironmentID: envID,
				SnapshotType:  "on_stop",
				ImageRef:      imageRef,
				CreatedAt:     time.Now(),
			}
			if saveErr := s.snapshotStore.Save(r.Context(), snap); saveErr != nil {
				fmt.Printf("[destroy] failed to save pre-destroy snapshot record: %v\n", saveErr)
			} else {
				fmt.Printf("[destroy] pre-destroy snapshot saved: %s → %s\n", envID, imageRef)
			}
		}
	}

	// Stop billing usage
	if trackErr := s.billingService.TrackUsageStop(r.Context(), envID); trackErr != nil {
		fmt.Printf("[billing] failed to track usage stop: %v\n", trackErr)
	}

	err = s.envService.DestroyEnvironment(r.Context(), envID, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "destroying",
		"env_id":  envID,
		"message": "environment destruction initiated (pre-destroy snapshot taken)",
	})
}

func (s *Server) handleSnapshotEnvironment(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	envID := vars["id"]
	orgID := GetOrgID(r.Context())

	var req struct {
		Tag string `json:"tag"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.Tag = fmt.Sprintf("manual-%d", time.Now().Unix())
	}
	if req.Tag == "" {
		req.Tag = fmt.Sprintf("manual-%d", time.Now().Unix())
	}

	imageRef, err := s.envService.SnapshotEnvironment(r.Context(), envID, orgID, req.Tag)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Get the environment to record branch info
	e, _ := s.envService.GetEnvironment(r.Context(), envID)
	branch := ""
	if e != nil {
		branch = e.ContextBranch
	}

	// Save snapshot record
	snap := &models.Snapshot{
		ID:            uuid.New().String(),
		OrgID:         orgID,
		Branch:        branch,
		EnvironmentID: envID,
		SnapshotType:  "manual",
		ImageRef:      imageRef,
		CreatedAt:     time.Now(),
	}
	if err := s.snapshotStore.Save(r.Context(), snap); err != nil {
		writeError(w, http.StatusInternalServerError, "snapshot created but failed to save record: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, snap)
}

// --- Context ---

type saveContextRequest struct {
	Branch            string                    `json:"branch"`
	CommitSHA         string                    `json:"commit_sha"`
	InstalledPackages []models.InstalledPackage `json:"installed_packages"`
	PreviousFailures  []models.TestFailure      `json:"previous_failures"`
	AttemptedFixes    []models.Fix              `json:"attempted_fixes"`
	Patterns          map[string]interface{}    `json:"patterns"`
	GlobalConfigs     map[string]string         `json:"global_configs"`
	BaseOS            string                    `json:"base_os"`
}

func (s *Server) handleSaveContext(w http.ResponseWriter, r *http.Request) {
	var req saveContextRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Branch == "" {
		writeError(w, http.StatusBadRequest, "branch is required")
		return
	}

	orgID := GetOrgID(r.Context())

	ctx, err := s.contextService.SaveContext(r.Context(), &services.SaveContextRequest{
		Branch:            req.Branch,
		OrgID:             orgID,
		CommitSHA:         req.CommitSHA,
		InstalledPackages: req.InstalledPackages,
		PreviousFailures:  req.PreviousFailures,
		AttemptedFixes:    req.AttemptedFixes,
		Patterns:          req.Patterns,
		GlobalConfigs:     req.GlobalConfigs,
		BaseOS:            req.BaseOS,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, ctx)
}

func (s *Server) handleGetContext(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	branch := vars["branch"]
	orgID := GetOrgID(r.Context())

	ctx, err := s.contextService.GetContext(r.Context(), orgID, branch)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, ctx)
}

func (s *Server) handleListContexts(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	contexts, err := s.contextService.ListContexts(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if contexts == nil {
		contexts = []*models.Context{}
	}

	writeJSON(w, http.StatusOK, contexts)
}

func (s *Server) handleDeleteContext(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	branch := vars["branch"]
	orgID := GetOrgID(r.Context())

	err := s.contextService.DeleteContext(r.Context(), orgID, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "deleted",
		"branch": branch,
	})
}

// --- Snapshots ---

func (s *Server) handleListSnapshots(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	branch := r.URL.Query().Get("branch")

	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch query parameter is required")
		return
	}

	snapshots, err := s.snapshotStore.ListByOrgAndBranch(r.Context(), orgID, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if snapshots == nil {
		snapshots = []*models.Snapshot{}
	}

	writeJSON(w, http.StatusOK, snapshots)
}

// --- Repos (GitHub integration) ---

type connectRepoRequest struct {
	Repo string `json:"repo"` // "owner/repo"
}

func (s *Server) handleConnectRepo(w http.ResponseWriter, r *http.Request) {
	var req connectRepoRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Repo == "" {
		writeError(w, http.StatusBadRequest, "repo is required (format: owner/repo)")
		return
	}

	orgID := GetOrgID(r.Context())

	conn, err := s.repoService.ConnectRepo(r.Context(), orgID, req.Repo)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, conn)
}

func (s *Server) handleListRepos(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	repos, err := s.repoService.ListRepos(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if repos == nil {
		repos = []*models.RepoConnection{}
	}

	writeJSON(w, http.StatusOK, repos)
}

func (s *Server) handleListAvailableRepos(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	repos, err := s.repoService.ListAvailableRepos(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if repos == nil {
		repos = []string{}
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"repos": repos,
	})
}

func (s *Server) handleDisconnectRepo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	connID := vars["id"]
	orgID := GetOrgID(r.Context())

	err := s.repoService.DisconnectRepo(r.Context(), orgID, connID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status": "disconnected",
		"id":     connID,
	})
}

// --- Billing ---

func (s *Server) handleGetUsage(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	month := r.URL.Query().Get("month")
	if month == "" {
		month = time.Now().Format("2006-01")
	}

	summary, err := s.billingService.GetUsageSummary(r.Context(), orgID, month)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, summary)
}

func (s *Server) handleListInvoices(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	invoices, err := s.billingService.GetStripeInvoices(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type invoiceResponse struct {
		ID         string  `json:"id"`
		Amount     float64 `json:"amount"`
		Currency   string  `json:"currency"`
		Status     string  `json:"status"`
		Created    int64   `json:"created"`
		InvoicePDF string  `json:"invoice_pdf,omitempty"`
	}

	var result []invoiceResponse
	for _, inv := range invoices {
		result = append(result, invoiceResponse{
			ID:         inv.ID,
			Amount:     float64(inv.AmountDue) / 100.0,
			Currency:   string(inv.Currency),
			Status:     string(inv.Status),
			Created:    inv.Created,
			InvoicePDF: inv.InvoicePDF,
		})
	}

	if result == nil {
		result = []invoiceResponse{}
	}

	writeJSON(w, http.StatusOK, result)
}

type billingSetupRequest struct {
	Email string `json:"email"`
	Name  string `json:"name"`
}

func (s *Server) handleBillingSetup(w http.ResponseWriter, r *http.Request) {
	var req billingSetupRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	orgID := GetOrgID(r.Context())

	// Create Stripe customer — MUST succeed (Stripe required even in dev)
	customerID, err := s.billingService.EnsureStripeCustomer(r.Context(), orgID, req.Email, req.Name)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("billing setup failed: %v", err))
		return
	}

	// Create metered subscription — MUST succeed for paid tier activation
	subscriptionID, subErr := s.billingService.CreateMeteredSubscription(r.Context(), orgID)
	if subErr != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("subscription creation failed: %v", subErr))
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":                 "ok",
		"stripe_customer_id":     customerID,
		"stripe_subscription_id": subscriptionID,
	})
}

func (s *Server) handleBillingStatus(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	status, err := s.billingService.GetBillingStatus(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleBillingPortal(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	var req struct {
		ReturnURL string `json:"return_url"`
		Flow      string `json:"flow"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req.ReturnURL = "/dashboard/billing"
	}
	if req.ReturnURL == "" {
		req.ReturnURL = "/dashboard/billing"
	}

	url, err := s.billingService.CreatePortalSession(r.Context(), orgID, req.ReturnURL, req.Flow)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": url})
}

func (s *Server) handleGetPaymentMethod(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	pm, err := s.billingService.GetPaymentMethodInfo(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if pm == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"has_method": false})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"has_method": true,
		"brand":      pm.Brand,
		"last4":      pm.Last4,
		"exp_month":  pm.ExpMonth,
		"exp_year":   pm.ExpYear,
	})
}

// --- Secrets ---

type secretSyncRequest struct {
	EnvironmentID string `json:"environment_id"`
	SecretKey     string `json:"secret_key"`
	Backend       string `json:"backend"`
	BackendPath   string `json:"backend_path"`
}

func (s *Server) handleSecretSync(w http.ResponseWriter, r *http.Request) {
	var req secretSyncRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.EnvironmentID == "" || req.SecretKey == "" || req.Backend == "" {
		writeError(w, http.StatusBadRequest, "environment_id, secret_key, and backend are required")
		return
	}

	if req.Backend != "vault" {
		writeError(w, http.StatusBadRequest, "backend must be 'vault'")
		return
	}

	orgID := GetOrgID(r.Context())

	// Actually read the secret from Vault
	var secretValues map[string]interface{}
	if s.secretSyncer != nil && req.BackendPath != "" {
		var err error
		secretValues, err = s.secretSyncer.SyncToEnvironment(r.Context(), req.BackendPath)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read secret from vault: "+err.Error())
			return
		}
		log.Printf("[secrets] Read %d keys from vault path %s", len(secretValues), req.BackendPath)

		// Inject secrets into the running environment via remote exec (provider-agnostic)
		e, err := s.envService.GetEnvironment(r.Context(), req.EnvironmentID)
		if err == nil && e != nil && e.Status == "running" && e.ClusterName != "" {
			provider, provErr := s.envService.GetProviderByName(e.Provider)
			if provErr == nil {
				if executor, ok := env.AsRemoteExecutor(provider); ok {
					if readyErr := executor.WaitForReady(r.Context(), e.ClusterName, 60*time.Second); readyErr == nil {
						for k, v := range secretValues {
							envCmd := fmt.Sprintf("docker exec gradient-env sh -c 'echo \"export %s=%v\" >> /etc/profile.d/gradient-secrets.sh'", k, v)
							output, execErr := executor.ExecCommand(r.Context(), e.ClusterName, envCmd, 30*time.Second)
							if execErr != nil {
								log.Printf("[secrets] Failed to inject secret %s into env %s: %v (output: %s)", k, req.EnvironmentID, execErr, output)
							} else {
								log.Printf("[secrets] Injected secret %s into env %s", k, req.EnvironmentID)
							}
						}
					} else {
						log.Printf("[secrets] Remote exec not available for env %s: %v — secrets stored in DB only", req.EnvironmentID, readyErr)
					}
				} else {
					log.Printf("[secrets] Provider %s does not support remote execution — secrets stored in DB only", e.Provider)
				}
			}
		}
	} else if s.secretSyncer == nil {
		log.Printf("[secrets] Vault not configured — recording sync metadata only")
	}

	// Save sync metadata to DB
	query := `
		INSERT INTO secret_syncs (id, environment_id, org_id, secret_key, backend, backend_path, synced_at)
		VALUES (gen_random_uuid()::text, $1, $2, $3, $4, $5, NOW())
		ON CONFLICT (environment_id, secret_key) DO UPDATE SET
			backend = EXCLUDED.backend,
			backend_path = EXCLUDED.backend_path,
			synced_at = NOW()
	`
	_, err := s.db.Pool.Exec(r.Context(), query, req.EnvironmentID, orgID, req.SecretKey, req.Backend, req.BackendPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to sync secret: "+err.Error())
		return
	}

	result := map[string]interface{}{
		"status":     "synced",
		"secret_key": req.SecretKey,
		"backend":    req.Backend,
	}
	if secretValues != nil {
		result["keys_synced"] = len(secretValues)
	}

	writeJSON(w, http.StatusOK, result)
}

// --- Organizations (Clerk SDK) ---

func (s *Server) handleListOrgs(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	if !s.orgService.IsEnabled() {
		// Dev mode: return a single stub org
		writeJSON(w, http.StatusOK, []map[string]string{
			{"id": GetOrgID(r.Context()), "name": "Dev Organization", "slug": "dev-org"},
		})
		return
	}

	orgs, err := s.orgService.ListOrganizations(r.Context(), userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if orgs == nil {
		orgs = []services.Organization{}
	}

	writeJSON(w, http.StatusOK, orgs)
}

func (s *Server) handleCreateOrg(w http.ResponseWriter, r *http.Request) {
	userID := GetUserID(r.Context())

	if !s.orgService.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Clerk not configured — set CLERK_SECRET_KEY to manage organizations")
		return
	}

	var req struct {
		Name string `json:"name"`
		Slug string `json:"slug,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}

	org, err := s.orgService.CreateOrganization(r.Context(), req.Name, req.Slug, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, org)
}

func (s *Server) handleListOrgMembers(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	if !s.orgService.IsEnabled() {
		writeJSON(w, http.StatusOK, []map[string]string{
			{"user_id": GetUserID(r.Context()), "role": "org:admin", "email": "dev@gradient.local"},
		})
		return
	}

	members, err := s.orgService.ListMembers(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if members == nil {
		members = []services.OrgMember{}
	}

	writeJSON(w, http.StatusOK, members)
}

type inviteRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

func (s *Server) handleInviteOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	if !s.orgService.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Clerk not configured — set CLERK_SECRET_KEY to manage org members")
		return
	}

	var req inviteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Email == "" {
		writeError(w, http.StatusBadRequest, "email is required")
		return
	}

	invitation, err := s.orgService.InviteMember(r.Context(), orgID, req.Email, req.Role)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, invitation)
}

func (s *Server) handleRemoveOrgMember(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	vars := mux.Vars(r)
	userID := vars["user_id"]

	if !s.orgService.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Clerk not configured — set CLERK_SECRET_KEY to manage org members")
		return
	}

	if err := s.orgService.RemoveMember(r.Context(), orgID, userID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "removed", "user_id": userID})
}

type updateRoleRequest struct {
	Role string `json:"role"`
}

func (s *Server) handleUpdateOrgMemberRole(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	vars := mux.Vars(r)
	userID := vars["user_id"]

	if !s.orgService.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Clerk not configured — set CLERK_SECRET_KEY to manage org members")
		return
	}

	var req updateRoleRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Role == "" {
		writeError(w, http.StatusBadRequest, "role is required")
		return
	}

	if err := s.orgService.UpdateMemberRole(r.Context(), orgID, userID, req.Role); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "updated", "user_id": userID, "role": req.Role})
}

func (s *Server) handleListOrgInvitations(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	if !s.orgService.IsEnabled() {
		writeJSON(w, http.StatusOK, []services.OrgInvitation{})
		return
	}

	invitations, err := s.orgService.ListInvitations(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if invitations == nil {
		invitations = []services.OrgInvitation{}
	}

	writeJSON(w, http.StatusOK, invitations)
}

func (s *Server) handleRevokeOrgInvitation(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	vars := mux.Vars(r)
	invitationID := vars["invitation_id"]

	if !s.orgService.IsEnabled() {
		writeError(w, http.StatusServiceUnavailable, "Clerk not configured — set CLERK_SECRET_KEY to manage org members")
		return
	}

	if err := s.orgService.RevokeInvitation(r.Context(), orgID, invitationID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revoked", "invitation_id": invitationID})
}

// --- Org Registry Settings ---

// handleGetOrgRegistry returns the org's custom container registry config (if set).
func (s *Server) handleGetOrgRegistry(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	var regURL, regUser *string
	err := s.db.Pool.QueryRow(r.Context(), `
		SELECT registry_url, registry_user FROM org_settings WHERE org_id = $1`, orgID).Scan(&regURL, &regUser)
	if err != nil {
		// No settings — org uses platform default
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"registry_url":  nil,
			"registry_user": nil,
			"using_default": true,
			"message":       "Using platform default registry. Set a custom registry with PUT /orgs/settings/registry.",
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"registry_url":  regURL,
		"registry_user": regUser,
		"using_default": regURL == nil || *regURL == "",
	})
}

// handleSetOrgRegistry sets the org's custom container registry.
// Enterprise orgs use this for data sovereignty, compliance, and snapshot isolation.
func (s *Server) handleSetOrgRegistry(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	var req struct {
		RegistryURL  string `json:"registry_url"`
		RegistryUser string `json:"registry_user"`
		RegistryPass string `json:"registry_pass"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RegistryURL == "" {
		writeError(w, http.StatusBadRequest, "registry_url is required")
		return
	}

	_, err := s.db.Pool.Exec(r.Context(), `
		INSERT INTO org_settings (org_id, registry_url, registry_user, registry_pass, updated_at)
		VALUES ($1, $2, $3, $4, NOW())
		ON CONFLICT (org_id) DO UPDATE SET
			registry_url = EXCLUDED.registry_url,
			registry_user = EXCLUDED.registry_user,
			registry_pass = EXCLUDED.registry_pass,
			updated_at = NOW()`,
		orgID, req.RegistryURL, req.RegistryUser, req.RegistryPass)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save registry settings")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":       "saved",
		"registry_url": req.RegistryURL,
		"message":      "All new environments and snapshots for this org will use this registry.",
	})
}

// handleDeleteOrgRegistry removes the org's custom registry (reverts to platform default).
func (s *Server) handleDeleteOrgRegistry(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	_, err := s.db.Pool.Exec(r.Context(), `
		UPDATE org_settings SET registry_url = NULL, registry_user = NULL, registry_pass = NULL, updated_at = NOW()
		WHERE org_id = $1`, orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clear registry settings")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "cleared",
		"message": "Org reverted to platform default registry.",
	})
}

// --- SSH Access ---

func (s *Server) handleGetSSHInfo(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	envID := vars["id"]
	orgID := GetOrgID(r.Context())

	e, err := s.envService.GetEnvironment(r.Context(), envID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if e.OrgID != orgID {
		writeError(w, http.StatusForbidden, "environment does not belong to this org")
		return
	}
	if e.Status != "running" {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("environment is not running (status: %s)", e.Status))
		return
	}
	if e.ClusterName == "" {
		writeError(w, http.StatusBadRequest, "environment has no provider reference yet — still initializing")
		return
	}

	// Get server IP based on provider (provider-agnostic via NetworkInfo interface)
	provider, provErr := s.envService.GetProviderByName(e.Provider)
	if provErr != nil {
		writeError(w, http.StatusInternalServerError, "provider not configured: "+provErr.Error())
		return
	}
	netInfo, ok := env.AsNetworkInfo(provider)
	if !ok {
		writeError(w, http.StatusBadRequest, "provider "+e.Provider+" does not expose network info for SSH access")
		return
	}
	var host string
	host, err = netInfo.GetServerIP(r.Context(), e.ClusterName)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get server IP: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"host":     host,
		"port":     22,
		"user":     "root",
		"env_name": e.Name,
		"command":  fmt.Sprintf("ssh root@%s -t 'docker exec -it gradient-env /bin/bash'", host),
	})
}

// handleEnvHealth proxies health checks to the gradient-agent running on the environment.
func (s *Server) handleEnvHealth(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	envID := vars["id"]
	orgID := GetOrgID(r.Context())

	e, err := s.envService.GetEnvironment(r.Context(), envID)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	if e.OrgID != orgID {
		writeError(w, http.StatusForbidden, "environment does not belong to this org")
		return
	}
	if e.Status != "running" {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status":   e.Status,
			"agent":    "unreachable",
			"env_id":   envID,
			"provider": e.Provider,
			"message":  fmt.Sprintf("environment is %s, agent not available", e.Status),
		})
		return
	}

	// Try to reach the agent's health endpoint on the server (provider-agnostic)
	if e.ClusterName != "" {
		provider, provErr := s.envService.GetProviderByName(e.Provider)
		if provErr == nil {
			if executor, ok := env.AsRemoteExecutor(provider); ok {
				output, execErr := executor.ExecCommand(r.Context(), e.ClusterName,
					"curl -s http://localhost:8090/health 2>/dev/null || echo '{\"status\":\"unreachable\"}'", 15*time.Second)
				if execErr == nil {
					// Parse agent response and return it
					var agentHealth map[string]interface{}
					if json.Unmarshal([]byte(output), &agentHealth) == nil {
						agentHealth["env_id"] = envID
						agentHealth["provider"] = e.Provider
						writeJSON(w, http.StatusOK, agentHealth)
						return
					}
				}
				log.Printf("[health] Could not reach agent on env %s: %v", envID, execErr)
			}
		}
	}

	// Fallback: return basic status from DB
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   e.Status,
		"agent":    "unreachable",
		"env_id":   envID,
		"provider": e.Provider,
		"message":  "agent health endpoint not reachable",
	})
}

// handleAgentHealthReport receives health reports FROM the gradient-agent running in environments.
// The agent POSTs its status periodically so the API can track environment health.
func (s *Server) handleAgentHealthReport(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	envID := vars["id"]

	var report struct {
		Status        string  `json:"status"`
		ContainerUp   bool    `json:"container_up"`
		DiskUsagePct  float64 `json:"disk_usage_pct"`
		MemUsagePct   float64 `json:"mem_usage_pct"`
		CPUUsagePct   float64 `json:"cpu_usage_pct"`
		UptimeSec     int64   `json:"uptime_sec"`
		SnapshotCount int     `json:"snapshot_count"`
		MeshConnected bool    `json:"mesh_connected"`
	}

	if err := json.NewDecoder(r.Body).Decode(&report); err != nil {
		writeError(w, http.StatusBadRequest, "invalid health report")
		return
	}

	// Update the environment's last health check in the DB
	query := `
		UPDATE environments
		SET config = jsonb_set(
			COALESCE(config, '{}'::jsonb),
			'{agent_health}',
			$1::jsonb
		),
		updated_at = NOW()
		WHERE id = $2
	`
	healthJSON, _ := json.Marshal(report)
	_, err := s.db.Pool.Exec(r.Context(), query, string(healthJSON), envID)
	if err != nil {
		log.Printf("[health] Failed to save health report for env %s: %v", envID, err)
		writeError(w, http.StatusInternalServerError, "failed to save health report")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "received"})
}

// --- Helper functions ---

func parseIntList(s string) []int64 {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	var result []int64
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		v, err := strconv.ParseInt(p, 10, 64)
		if err == nil {
			result = append(result, v)
		}
	}
	return result
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	v, _ := strconv.ParseInt(s, 10, 64)
	return v
}

// --- Live Context Mesh ---

// handlePublishEvent publishes a new context event to the mesh.
func (s *Server) handlePublishEvent(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	var event livectx.Event
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		writeError(w, http.StatusBadRequest, "invalid event JSON: "+err.Error())
		return
	}

	// Override org_id from auth context (prevent spoofing)
	event.OrgID = orgID

	// Set source to "api" if not set
	if event.Source == "" {
		event.Source = "api"
	}

	// Set ID if empty
	if event.ID == "" {
		event.ID = uuid.New().String()
	}

	// Set timestamp if empty
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now().UTC()
	}

	// Set schema version
	if event.SchemaVersion == 0 {
		event.SchemaVersion = livectx.SchemaVersion
	}

	if err := event.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "event validation failed: "+err.Error())
		return
	}

	// Publish via mesh (persists to DB + broadcasts via NATS)
	if err := s.meshPublisher.Publish(r.Context(), &event); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to publish event: "+err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, map[string]interface{}{
		"status":   "published",
		"event_id": event.ID,
		"sequence": event.Sequence,
	})
}

// handleQueryEvents queries events with filtering and cursor-based pagination.
func (s *Server) handleQueryEvents(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	filter := livectx.EventFilter{
		OrgID: orgID,
	}

	// Parse query parameters
	if branch := r.URL.Query().Get("branch"); branch != "" {
		filter.Branch = branch
	}
	if envID := r.URL.Query().Get("env_id"); envID != "" {
		filter.EnvID = envID
	}
	if source := r.URL.Query().Get("source"); source != "" {
		filter.Source = source
	}
	if types := r.URL.Query().Get("types"); types != "" {
		for _, t := range strings.Split(types, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				filter.Types = append(filter.Types, livectx.EventType(t))
			}
		}
	}
	if since := r.URL.Query().Get("since"); since != "" {
		if t, err := time.Parse(time.RFC3339, since); err == nil {
			filter.Since = t
		}
	}
	if until := r.URL.Query().Get("until"); until != "" {
		if t, err := time.Parse(time.RFC3339, until); err == nil {
			filter.Until = t
		}
	}
	if minSeq := r.URL.Query().Get("min_seq"); minSeq != "" {
		if v, err := strconv.ParseInt(minSeq, 10, 64); err == nil {
			filter.MinSeq = v
		}
	}
	if limit := r.URL.Query().Get("limit"); limit != "" {
		if v, err := strconv.Atoi(limit); err == nil {
			filter.Limit = v
		}
	}

	batch, err := s.eventStore.Query(r.Context(), filter)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to query events: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, batch)
}

// handleGetEvent retrieves a single event by ID.
func (s *Server) handleGetEvent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventID := vars["id"]

	event, err := s.eventStore.GetByID(r.Context(), eventID)
	if err != nil {
		writeError(w, http.StatusNotFound, "event not found: "+err.Error())
		return
	}

	// Verify org access
	orgID := GetOrgID(r.Context())
	if event.OrgID != orgID {
		writeError(w, http.StatusForbidden, "event does not belong to this organization")
		return
	}

	writeJSON(w, http.StatusOK, event)
}

// handleAckEvent marks an event as acknowledged.
func (s *Server) handleAckEvent(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	eventID := vars["id"]

	if err := s.eventStore.AckEvent(r.Context(), eventID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to acknowledge event: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{
		"status":   "acknowledged",
		"event_id": eventID,
	})
}

// handleStreamEvents provides a Server-Sent Events (SSE) stream for real-time
// event delivery. Clients connect and receive events as they're published.
//
// Query params:
//   - branch (required): branch to stream events for
//   - since_seq: start from this sequence number (replay)
//   - types: comma-separated event type filter
func (s *Server) handleStreamEvents(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch query parameter is required")
		return
	}

	// Check SSE support
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}

	// Set SSE headers
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // Nginx
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Parse optional type filter
	var typeFilter map[livectx.EventType]bool
	if types := r.URL.Query().Get("types"); types != "" {
		typeFilter = make(map[livectx.EventType]bool)
		for _, t := range strings.Split(types, ",") {
			t = strings.TrimSpace(t)
			if t != "" {
				typeFilter[livectx.EventType(t)] = true
			}
		}
	}

	// Replay from sequence if requested
	if sinceSeq := r.URL.Query().Get("since_seq"); sinceSeq != "" {
		if seq, err := strconv.ParseInt(sinceSeq, 10, 64); err == nil {
			filter := livectx.EventFilter{
				OrgID:  orgID,
				Branch: branch,
				MinSeq: seq,
				Limit:  1000,
			}
			batch, err := s.eventStore.Query(r.Context(), filter)
			if err == nil {
				for _, event := range batch.Events {
					if typeFilter != nil && !typeFilter[event.Type] {
						continue
					}
					data, _ := json.Marshal(event)
					fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, string(data))
				}
				flusher.Flush()
			}
		}
	}

	// Send keepalive comment
	fmt.Fprintf(w, ": connected to live context mesh for %s/%s\n\n", orgID, branch)
	flusher.Flush()

	// Set up event channel for new events
	eventCh := make(chan *livectx.Event, 100)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Subscribe to events for this branch
	handler := func(_ context.Context, event *livectx.Event) error {
		if typeFilter != nil && !typeFilter[event.Type] {
			return nil
		}
		select {
		case eventCh <- event:
		default:
			// Channel full — drop event (client too slow)
			log.Printf("[sse] Dropping event %s for slow client on %s/%s", event.ID, orgID, branch)
		}
		return nil
	}

	consumerSuffix := fmt.Sprintf("sse-%s", uuid.New().String()[:8])
	if err := s.eventBus.Subscribe(ctx, orgID, branch, consumerSuffix, handler); err != nil {
		log.Printf("[sse] Failed to subscribe for SSE: %v", err)
		// Fall back to polling mode
	}

	// Stream events until client disconnects
	ticker := time.NewTicker(30 * time.Second) // Keepalive
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case event := <-eventCh:
			data, _ := json.Marshal(event)
			fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.Sequence, event.Type, string(data))
			flusher.Flush()
		case <-ticker.C:
			// SSE keepalive comment
			fmt.Fprintf(w, ": keepalive %s\n\n", time.Now().UTC().Format(time.RFC3339))
			flusher.Flush()
		}
	}
}

// handleEventStats returns aggregate statistics for a branch's event stream.
func (s *Server) handleEventStats(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	branch := r.URL.Query().Get("branch")
	if branch == "" {
		writeError(w, http.StatusBadRequest, "branch query parameter is required")
		return
	}

	stats, err := s.eventStore.GetStats(r.Context(), orgID, branch)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get event stats: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, stats)
}

// handleMeshHealth returns the health status of the Live Context Mesh.
func (s *Server) handleMeshHealth(w http.ResponseWriter, r *http.Request) {
	result := map[string]interface{}{
		"status": "ok",
		"bus":    "local",
	}

	// Check if we have a real NATS bus
	if eb, ok := s.eventBus.(*livectx.EventBus); ok {
		result["bus"] = "nats"
		result["connected"] = eb.IsConnected()

		info, err := eb.StreamInfo(r.Context())
		if err == nil {
			result["stream"] = info
		} else {
			result["stream_error"] = err.Error()
		}
	}

	writeJSON(w, http.StatusOK, result)
}

// GetMeshPublisher returns the mesh publisher for external use (e.g., agent health endpoint).
func (s *Server) GetMeshPublisher() *livectx.MeshPublisher {
	return s.meshPublisher
}

// GetEventBus returns the event bus for external use.
func (s *Server) GetEventBus() livectx.Bus {
	return s.eventBus
}

// --- Autoscale Handlers ---

func (s *Server) handleSetAutoscale(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	envID := mux.Vars(r)["id"]

	var policy models.AutoscalePolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body: "+err.Error())
		return
	}
	policy.EnvironmentID = envID

	result, err := s.autoscaleService.CreateOrUpdatePolicy(r.Context(), orgID, &policy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to set autoscale policy: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, result)
}

func (s *Server) handleGetAutoscale(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	envID := mux.Vars(r)["id"]

	policy, err := s.autoscaleService.GetPolicy(r.Context(), orgID, envID)
	if err != nil {
		writeError(w, http.StatusNotFound, "autoscale policy not found")
		return
	}

	writeJSON(w, http.StatusOK, policy)
}

func (s *Server) handleDeleteAutoscale(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	envID := mux.Vars(r)["id"]

	if err := s.autoscaleService.DeletePolicy(r.Context(), orgID, envID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete autoscale policy: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "autoscale disabled"})
}

func (s *Server) handleGetAutoscaleStatus(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	envID := mux.Vars(r)["id"]

	status, err := s.autoscaleService.GetStatus(r.Context(), orgID, envID)
	if err != nil {
		writeError(w, http.StatusNotFound, "autoscale status not available: "+err.Error())
		return
	}

	writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleGetAutoscaleHistory(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())
	envID := mux.Vars(r)["id"]

	limit := 20
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}

	events, err := s.autoscaleService.ScaleHistory(r.Context(), orgID, envID, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get scale history: "+err.Error())
		return
	}
	if events == nil {
		events = []models.ScaleEvent{}
	}

	writeJSON(w, http.StatusOK, events)
}

func (s *Server) handleListAutoscalePolicies(w http.ResponseWriter, r *http.Request) {
	orgID := GetOrgID(r.Context())

	policies, err := s.autoscaleService.ListPolicies(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list autoscale policies: "+err.Error())
		return
	}
	if policies == nil {
		policies = []*models.AutoscalePolicy{}
	}

	writeJSON(w, http.StatusOK, policies)
}

// --- Rate Limit Stats ---

func (s *Server) handleRateLimitStats(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.rateLimiter.Stats())
}

// handleServeAgent serves the agent binary for Hetzner servers to download.
// In production, host this on a CDN/GitHub Releases. For dev, the API serves it directly.
func (s *Server) handleServeAgent(w http.ResponseWriter, r *http.Request) {
	binaryPath := "bin/gradient-agent-linux"
	if _, err := os.Stat(binaryPath); os.IsNotExist(err) {
		writeError(w, http.StatusNotFound, "agent binary not built — run: make build-agent-linux")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", "attachment; filename=gradient-agent")
	http.ServeFile(w, r, binaryPath)
}

// handleClerkRedirect catches the root URL after Clerk sign-in redirect.
// Clerk redirects to /?__clerk_handshake=... after authentication.
// This page uses localStorage to recover the device code and redirect back to /auth/cli.
func (s *Server) handleClerkRedirect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Gradient — Redirecting...</title>
<style>
body { background: #0a0a0a; color: #e5e5e5; font-family: -apple-system, sans-serif;
       display: flex; justify-content: center; align-items: center; min-height: 100vh; }
.card { background: #171717; border: 1px solid #262626; border-radius: 16px; padding: 48px;
        max-width: 400px; text-align: center; }
.spinner { display: inline-block; width: 24px; height: 24px; border: 3px solid #525252;
           border-top-color: #22c55e; border-radius: 50%; animation: spin 0.6s linear infinite; }
@keyframes spin { to { transform: rotate(360deg); } }
</style></head>
<body><div class="card">
  <div class="spinner" style="margin-bottom: 16px;"></div>
  <p>Signed in! Redirecting back to CLI authorization...</p>
  <p id="fallback" style="display:none; margin-top: 16px; color: #737373; font-size: 13px;">
    If you're not redirected, <a href="#" id="link" style="color: #22c55e;">click here</a>.
  </p>
</div>
<script>
  const code = localStorage.getItem('gradient_device_code');
  if (code) {
    window.location.href = '/auth/cli?code=' + code;
  } else {
    document.querySelector('.spinner').style.display = 'none';
    document.querySelector('p').textContent = 'Sign-in successful, but device code was lost.';
    document.getElementById('fallback').style.display = 'block';
    document.getElementById('fallback').innerHTML =
      '<p style="color:#737373">Run <code style="color:#22c55e">gc auth login</code> again in your terminal.</p>';
  }
</script>
</body></html>`)
}

// ═══════════════════════════════════════════════════════════════════════
// Agent Tasks Handlers
// ═══════════════════════════════════════════════════════════════════════

func (s *Server) handleTaskReadiness(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "missing org ID")
		return
	}
	rs := s.taskService.CheckReadiness(r.Context(), orgID)
	writeJSON(w, http.StatusOK, rs)
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "missing org ID")
		return
	}

	var req services.CreateTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}

	task, err := s.taskService.CreateTask(r.Context(), orgID, req)
	if err != nil {
		// If it's a readiness/precondition error, return 422 not 500
		if strings.Contains(err.Error(), "not configured") {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
		} else {
			writeError(w, http.StatusInternalServerError, err.Error())
		}
		return
	}

	// Auto-start if requested
	autoStart := r.URL.Query().Get("auto_start")
	if autoStart == "true" || autoStart == "1" {
		go s.taskService.StartTaskExecution(context.Background(), orgID, task.ID)
	}

	writeJSON(w, http.StatusCreated, task)
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "missing org ID")
		return
	}

	status := r.URL.Query().Get("status")
	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 200 {
			limit = v
		}
	}

	tasks, err := s.taskService.ListTasks(r.Context(), orgID, status, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*models.AgentTask{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	task, err := s.taskService.GetTask(r.Context(), orgID, taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if task == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleStartTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	if err := s.taskService.StartTaskExecution(r.Context(), orgID, taskID); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "started"})
}

func (s *Server) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	var req services.CompleteTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if err := s.taskService.CompleteTask(r.Context(), orgID, taskID, req); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "completed"})
}

func (s *Server) handleFailTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	var req struct {
		Error string `json:"error"`
	}
	json.NewDecoder(r.Body).Decode(&req)

	if err := s.taskService.FailTask(r.Context(), orgID, taskID, req.Error); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "failed"})
}

func (s *Server) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	if err := s.taskService.CancelTask(r.Context(), orgID, taskID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "cancelled"})
}

func (s *Server) handleRetryTask(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	task, err := s.taskService.RetryTask(r.Context(), orgID, taskID)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Server) handleGetTaskLogs(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	taskID := mux.Vars(r)["id"]

	logs, err := s.taskService.GetTaskLogs(r.Context(), orgID, taskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if logs == nil {
		logs = []*models.TaskLogEntry{}
	}
	writeJSON(w, http.StatusOK, logs)
}

func (s *Server) handleTaskStats(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	stats, err := s.taskService.GetStats(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, stats)
}

func (s *Server) handleGetTaskSettings(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	settings, err := s.taskService.GetSettings(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handleSaveTaskSettings(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	var settings models.TaskSettings
	if err := json.NewDecoder(r.Body).Decode(&settings); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	settings.OrgID = orgID

	if err := s.taskService.SaveSettings(r.Context(), &settings); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// ═══════════════════════════════════════════════════════════════════════
// Integration Handlers (Linear + Claude)
// ═══════════════════════════════════════════════════════════════════════

func (s *Server) handleGetLinearConnection(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	conn, err := s.linearService.GetConnection(r.Context(), orgID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conn == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"connected": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connected":      true,
		"workspace_id":   conn.WorkspaceID,
		"workspace_name": conn.WorkspaceName,
		"status":         conn.Status,
		"trigger_state":  conn.TriggerState,
		"filter_labels":  conn.FilterLabelNames,
		"created_at":     conn.CreatedAt,
	})
}

func (s *Server) handleDeleteLinearConnection(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	if err := s.linearService.DeleteConnection(r.Context(), orgID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleLinearAuthURL(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	if !s.linearService.Configured() {
		writeError(w, http.StatusServiceUnavailable, "Linear integration not configured")
		return
	}

	state := orgID + ":" + uuid.New().String()
	url := s.linearService.GetAuthURL(orgID, state)
	writeJSON(w, http.StatusOK, map[string]string{"url": url, "state": state})
}

func (s *Server) handleLinearCallback(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	var req struct {
		Code  string `json:"code"`
		State string `json:"state"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	conn, err := s.linearService.ExchangeCode(r.Context(), orgID, req.Code)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"connected":      true,
		"workspace_id":   conn.WorkspaceID,
		"workspace_name": conn.WorkspaceName,
	})
}

func (s *Server) handleGetClaudeConfig(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	userID := r.Header.Get("X-User-ID")

	cfg, err := s.claudeService.GetConfig(r.Context(), orgID, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if cfg == nil {
		writeJSON(w, http.StatusOK, map[string]interface{}{"configured": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured":       true,
		"api_key_masked":   cfg.APIKeyMasked,
		"model":            cfg.Model,
		"max_turns":        cfg.MaxTurns,
		"allowed_tools":    cfg.AllowedTools,
		"max_cost_per_task": cfg.MaxCostPerTask,
	})
}

func (s *Server) handleSaveClaudeConfig(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	userID := r.Header.Get("X-User-ID")

	var req struct {
		APIKey       string   `json:"api_key"`
		Model        string   `json:"model"`
		MaxTurns     int      `json:"max_turns"`
		AllowedTools []string `json:"allowed_tools"`
		MaxCost      float64  `json:"max_cost_per_task"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	cfg, err := s.claudeService.SaveConfig(r.Context(), orgID, userID, req.APIKey, req.Model, req.MaxTurns, req.AllowedTools, req.MaxCost)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"configured":     true,
		"api_key_masked": cfg.APIKeyMasked,
		"model":          cfg.Model,
		"max_turns":      cfg.MaxTurns,
	})
}

func (s *Server) handleDeleteClaudeConfig(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")

	if err := s.claudeService.DeleteConfig(r.Context(), orgID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleIntegrationStatus(w http.ResponseWriter, r *http.Request) {
	orgID := r.Header.Get("X-Org-ID")
	if orgID == "" {
		writeError(w, http.StatusBadRequest, "X-Org-ID header is required")
		return
	}
	ctx := r.Context()

	linearConn, _ := s.linearService.GetConnection(ctx, orgID)
	claudeCfg, _ := s.claudeService.GetConfig(ctx, orgID, "")

	// Check billing
	hasBilling := false
	var tier string
	err := s.db.Pool.QueryRow(ctx, `SELECT COALESCE(billing_tier,'free') FROM org_settings WHERE org_id = $1`, orgID).Scan(&tier)
	if err == nil && tier == "paid" {
		hasBilling = true
	}
	// Ignore errors - org might not have billing configured yet

	// Check repos
	var repoCount int
	_ = s.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM repo_connections WHERE org_id = $1`, orgID).Scan(&repoCount)
	// Ignore errors - might be 0 repos

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"linear": map[string]interface{}{
			"connected":      linearConn != nil,
			"workspace_name": func() string { if linearConn != nil { return linearConn.WorkspaceName }; return "" }(),
		},
		"claude": map[string]interface{}{
			"configured":   claudeCfg != nil,
			"model":        func() string { if claudeCfg != nil { return claudeCfg.Model }; return "" }(),
		},
		"billing": map[string]interface{}{
			"active": hasBilling,
			"tier":   tier,
		},
		"repos": map[string]interface{}{
			"connected": repoCount > 0,
			"count":     repoCount,
		},
		"ready": linearConn != nil && claudeCfg != nil,
	})
}
