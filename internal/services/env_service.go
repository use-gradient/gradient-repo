package services

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/models"
	"github.com/gradient/gradient/pkg/env"
)

// EnvServiceConfig holds configuration that gets passed to providers when creating environments.
type EnvServiceConfig struct {
	APIURL          string // Gradient API URL for agent callbacks
	NATSUrl         string // NATS URL for Live Context Mesh
	NATSAuthToken   string // NATS auth token
	DefaultProvider string // Default cloud provider ("aws" or "hetzner"), from DEV_ENV_SRC
}

type EnvService struct {
	EnvRepo   *env.Repository
	providers map[string]env.Provider // provider name → implementation
	config    EnvServiceConfig
	warmPool  *WarmPoolService // optional — if set, CreateEnvironment tries warm pool first
}

// NewEnvService creates an EnvService with named providers. Accepts any number of providers.
// Legacy signature preserved for backward compatibility:
//
//	NewEnvService(repo, hetznerProvider, awsProvider, cfg...)
//
// New callers should use NewEnvServiceWithProviders instead.
func NewEnvService(envRepo *env.Repository, hetznerProvider env.Provider, awsProvider env.Provider, cfg ...EnvServiceConfig) *EnvService {
	var c EnvServiceConfig
	if len(cfg) > 0 {
		c = cfg[0]
	}
	providers := make(map[string]env.Provider)
	if hetznerProvider != nil {
		providers["hetzner"] = hetznerProvider
	}
	if awsProvider != nil {
		providers["aws"] = awsProvider
	}
	return &EnvService{
		EnvRepo:   envRepo,
		providers: providers,
		config:    c,
	}
}

// NewEnvServiceWithProviders creates an EnvService from an explicit provider map.
// Use this for multi-provider setups:
//
//	providers := map[string]env.Provider{"hetzner": hp, "aws": ap, "gcp": gp}
//	svc := NewEnvServiceWithProviders(repo, providers, cfg)
func NewEnvServiceWithProviders(envRepo *env.Repository, providers map[string]env.Provider, cfg EnvServiceConfig) *EnvService {
	return &EnvService{
		EnvRepo:   envRepo,
		providers: providers,
		config:    cfg,
	}
}

// SetWarmPool attaches a warm pool service for fast boot on CreateEnvironment.
func (s *EnvService) SetWarmPool(wp *WarmPoolService) {
	s.warmPool = wp
}

// RegisterProvider adds or replaces a provider at runtime (e.g., for hot-loading new clouds).
func (s *EnvService) RegisterProvider(name string, provider env.Provider) {
	if s.providers == nil {
		s.providers = make(map[string]env.Provider)
	}
	s.providers[name] = provider
}

// GetDefaultProvider returns the configured default provider name.
func (s *EnvService) GetDefaultProvider() string {
	if s.config.DefaultProvider != "" {
		return s.config.DefaultProvider
	}
	return "aws"
}

// AvailableProviders returns the names of all configured providers.
func (s *EnvService) AvailableProviders() []string {
	names := make([]string, 0, len(s.providers))
	for k := range s.providers {
		names = append(names, k)
	}
	return names
}

type CreateEnvRequest struct {
	Name          string
	OrgID         string
	Provider      string
	Region        string
	Size          string
	Config        map[string]interface{}
	ContextBranch string
	RepoFullName  string
	SnapshotRef   string // If restoring from a snapshot image
}

func (s *EnvService) CreateEnvironment(ctx context.Context, req *CreateEnvRequest) (*models.Environment, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if req.Provider == "" {
		if s.config.DefaultProvider != "" {
			if _, ok := s.providers[s.config.DefaultProvider]; ok {
				req.Provider = s.config.DefaultProvider
			}
		}
		if req.Provider == "" {
			for name := range s.providers {
				req.Provider = name
				break
			}
		}
		if req.Provider == "" {
			return nil, fmt.Errorf("no cloud providers configured")
		}
	}
	if _, ok := s.providers[req.Provider]; !ok {
		return nil, fmt.Errorf("provider %q not configured (available: %s)", req.Provider, strings.Join(s.AvailableProviders(), ", "))
	}
	if req.Region == "" {
		return nil, fmt.Errorf("region is required")
	}
	if req.Size == "" {
		req.Size = "small"
	}

	envID := uuid.New().String()
	resources := models.SizeToResources(req.Size)

	newEnv := &models.Environment{
		ID:            envID,
		Name:          req.Name,
		OrgID:         req.OrgID,
		Provider:      req.Provider,
		Region:        req.Region,
		Size:          req.Size,
		Status:        "creating",
		Resources:     resources,
		Config:        req.Config,
		ContextBranch: req.ContextBranch,
		RepoFullName:  req.RepoFullName,
		CreatedAt:     time.Now(),
		UpdatedAt:     time.Now(),
	}

	if err := s.EnvRepo.Create(ctx, newEnv); err != nil {
		return nil, fmt.Errorf("failed to save environment: %w", err)
	}

	provider, err := s.getProvider(req.Provider)
	if err != nil {
		newEnv.Status = "failed"
		if updateErr := s.EnvRepo.Update(ctx, newEnv); updateErr != nil {
			log.Printf("Failed to mark environment %s as failed: %v", envID, updateErr)
		}
		return nil, err
	}

	// Resolve per-org registry (enterprise isolation) — falls back to platform default
	regURL, regUser, regPass := s.resolveRegistry(ctx, req.OrgID)

	providerCfg := env.ProviderConfig{
		Name:          req.Name,
		Region:        req.Region,
		Size:          req.Size,
		Resources:     resources,
		SnapshotRef:   req.SnapshotRef,
		RegistryURL:   regURL,
		RegistryUser:  regUser,
		RegistryPass:  regPass,
		EnvID:         envID,
		OrgID:         req.OrgID,
		RepoFullName:  req.RepoFullName,
		Branch:        req.ContextBranch,
		APIURL:        s.config.APIURL,
		NATSUrl:       s.config.NATSUrl,
		NATSAuthToken: s.config.NATSAuthToken,
	}

	// Try warm pool first for near-instant boot (~5-15s vs 2-6min cold boot).
	// If no warm server is available, fall back to cold boot.
	go func(envID string, providerCfg env.ProviderConfig) {
		bgCtx := context.Background()
		bootStart := time.Now()

		// Warm pool path: acquire pre-booted server, start container on it
		if s.warmPool != nil {
			ws, err := s.warmPool.AcquireServer(bgCtx, req.Provider, req.Size, req.Region)
			if err == nil && ws != nil {
				log.Printf("[warm-pool] Got warm server %s for env %s — configuring...", ws.ProviderID, envID)

				// Mark the warm server as assigned to this env
				s.warmPool.MarkAssigned(bgCtx, ws.ID, envID)

				// Start the container on the warm server (much faster — server is already booted)
				if executor, ok := env.AsRemoteExecutor(provider); ok {
					startCmd := s.buildWarmStartCommand(&providerCfg)
					output, err := executor.ExecCommand(bgCtx, ws.ProviderID, startCmd, 3*time.Minute)
					if err == nil {
						bootDuration := time.Since(bootStart).Round(time.Millisecond)
						s.setEnvStatusFull(bgCtx, envID, "running", ws.ProviderID, ws.IPAddress, bootDuration)
						log.Printf("[warm-pool] Environment %s running on warm server %s (boot: %s)",
							envID, ws.ProviderID, bootDuration)
						return
					}
					log.Printf("[warm-pool] Failed to start container on warm server %s: %v\nOutput: %s",
						ws.ProviderID, err, output)
					// Fall through to cold boot
				}
			}
		}

		// Cold boot path: create a new server from scratch
		providerRef, createErr := provider.CreateEnvironment(bgCtx, &providerCfg)
		if createErr != nil {
			log.Printf("Failed to create environment %s: %v", envID, createErr)
			s.setEnvStatus(bgCtx, envID, "failed", "")
			return
		}

		bootDuration := time.Since(bootStart).Round(time.Millisecond)

		// Get IP address if the provider supports it
		var ip string
		if netInfo, ok := env.AsNetworkInfo(provider); ok {
			ip, _ = netInfo.GetServerIP(bgCtx, providerRef)
		}

		s.setEnvStatusFull(bgCtx, envID, "running", providerRef, ip, bootDuration)
		log.Printf("Environment %s is now running on %s (cold boot: %s)", envID, providerRef, bootDuration)
	}(envID, providerCfg)

	return newEnv, nil
}

// buildWarmStartCommand generates the shell command to start a container on an already-running warm server.
// This is much faster than cold boot — the server is already booted, Docker is ready, base image may be pre-pulled.
func (s *EnvService) buildWarmStartCommand(cfg *env.ProviderConfig) string {
	image := "ubuntu:24.04"
	if cfg.SnapshotRef != "" {
		image = cfg.SnapshotRef
	}

	// Build the startup command — pull image + start container
	return fmt.Sprintf(`set -e
# Pull image (may already be cached from pre-pull)
docker pull %s 2>/dev/null || true

# Remove any stale container
docker rm -f gradient-env 2>/dev/null || true

# Create workspace
mkdir -p /home/gradient/workspace /gradient/context

# Start the container with security hardening
docker run -d \
    --name gradient-env \
    --security-opt seccomp=gradient-seccomp.json \
    --security-opt no-new-privileges \
    --cap-drop ALL \
    --cap-add NET_BIND_SERVICE \
    --cap-add SYS_PTRACE \
    --cap-add DAC_OVERRIDE \
    --network gradient-net \
    --memory 0 \
    --restart unless-stopped \
    -v /home/gradient/workspace:/workspace \
    -e GRADIENT_ENV_NAME=%s \
    -e GRADIENT_ENV_ID=%s \
    %s \
    tail -f /dev/null

echo "ready"
`, image, cfg.Name, cfg.EnvID, image)
}

func (s *EnvService) DestroyEnvironment(ctx context.Context, envID, orgID string) error {
	existingEnv, err := s.EnvRepo.GetByID(ctx, envID)
	if err != nil {
		return fmt.Errorf("environment not found: %w", err)
	}

	if existingEnv.OrgID != orgID {
		return fmt.Errorf("environment does not belong to this org")
	}

	if existingEnv.Status == "destroyed" || existingEnv.Status == "destroying" {
		return fmt.Errorf("environment is already %s", existingEnv.Status)
	}

	existingEnv.Status = "destroying"
	existingEnv.UpdatedAt = time.Now()
	if err := s.EnvRepo.Update(ctx, existingEnv); err != nil {
		log.Printf("Failed to set environment %s to destroying: %v", envID, err)
	}

	provider, err := s.getProvider(existingEnv.Provider)
	if err != nil {
		return err
	}

	// Pre-destroy snapshot: capture state before tearing down the server.
	// Uses hybrid strategy — server-level snapshot (most reliable) + container export (fast).
	if existingEnv.ClusterName != "" {
		s.preDestroySnapshot(ctx, existingEnv, provider)
	}

	if existingEnv.ClusterName != "" {
		// Try returning to warm pool before destroying (saves boot time for next env)
		recycled := false
		if s.warmPool != nil {
			if err := s.warmPool.ReturnServer(ctx, existingEnv.Provider, existingEnv.ClusterName); err == nil {
				recycled = true
				log.Printf("Environment %s server recycled to warm pool", envID)
			}
		}

		if !recycled {
			if err := provider.DestroyEnvironment(ctx, existingEnv.ClusterName); err != nil {
				return fmt.Errorf("failed to destroy environment: %w", err)
			}
		}
	}

	now := time.Now()
	existingEnv.Status = "destroyed"
	existingEnv.DestroyedAt = &now
	existingEnv.UpdatedAt = now

	return s.EnvRepo.Update(ctx, existingEnv)
}

// preDestroySnapshot takes a final snapshot before destroying an environment.
// v0.1: Uses docker export (primary) + docker commit (fallback). No server-level
// snapshots — they're too slow (30-90s on Hetzner) and add complexity.
// Server snapshots planned for v0.2 when we need to capture system-level changes.
//
// Respects per-org registry: if the org has a custom registry in org_settings,
// snapshots are pushed there instead of the platform default.
func (s *EnvService) preDestroySnapshot(ctx context.Context, e *models.Environment, provider env.Provider) {
	tag := fmt.Sprintf("destroy-%s-%d", e.Name, time.Now().Unix())

	// Resolve per-org registry for the snapshot destination
	regURL, regUser, regPass := s.resolveRegistry(ctx, e.OrgID)

	// If org has a custom registry, use remote exec to push there directly
	// (bypasses provider's hardcoded registry)
	if regURL != "" {
		if executor, ok := env.AsRemoteExecutor(provider); ok {
			imageRef := fmt.Sprintf("%s:%s", regURL, tag)
			var cmds []string
			if regUser != "" && regPass != "" {
				domain := strings.Split(regURL, "/")[0]
				cmds = append(cmds, fmt.Sprintf("echo '%s' | docker login --username '%s' --password-stdin %s", regPass, regUser, domain))
			}
			cmds = append(cmds,
				fmt.Sprintf("docker export gradient-env | docker import - %s", imageRef),
				fmt.Sprintf("docker push %s", imageRef),
			)
			if _, err := executor.ExecCommand(ctx, e.ClusterName, strings.Join(cmds, " && "), 10*time.Minute); err == nil {
				log.Printf("[pre-destroy] Container export to org registry: %s for env %s", imageRef, e.ID)
				return
			}
			log.Printf("[pre-destroy] Org registry export failed for env %s, trying provider default...", e.ID)
		}
	}

	// Strategy 1: Container export via HybridSnapshotter (most reliable for filesystem)
	if hs, ok := env.AsHybridSnapshotter(provider); ok {
		if imageRef, err := hs.ExportContainer(ctx, e.ClusterName, tag); err == nil {
			log.Printf("[pre-destroy] Container export completed: %s for env %s", imageRef, e.ID)
			return
		}
		log.Printf("[pre-destroy] Container export failed for env %s, trying docker commit...", e.ID)
	}

	// Strategy 2: Standard docker commit (faster but less reliable with open files)
	if snapshotter, ok := env.AsSnapshotter(provider); ok {
		if imageRef, err := snapshotter.SnapshotEnvironment(ctx, e.ClusterName, tag); err == nil {
			log.Printf("[pre-destroy] Docker commit snapshot: %s for env %s", imageRef, e.ID)
		} else {
			log.Printf("[pre-destroy] Docker commit snapshot failed for env %s: %v (state will be lost)", e.ID, err)
		}
	} else {
		log.Printf("[pre-destroy] Provider %s does not support snapshots — state will be lost for env %s", e.Provider, e.ID)
	}
}

// SnapshotEnvironment takes a container commit snapshot of a running environment.
// Returns the snapshot image ref (ECR URI).
func (s *EnvService) SnapshotEnvironment(ctx context.Context, envID, orgID, tag string) (string, error) {
	existingEnv, err := s.EnvRepo.GetByID(ctx, envID)
	if err != nil {
		return "", fmt.Errorf("environment not found: %w", err)
	}
	if existingEnv.OrgID != orgID {
		return "", fmt.Errorf("environment does not belong to this org")
	}
	if existingEnv.Status != "running" {
		return "", fmt.Errorf("environment is not running (status: %s)", existingEnv.Status)
	}
	if existingEnv.ClusterName == "" {
		return "", fmt.Errorf("environment has no provider reference")
	}

	provider, err := s.getProvider(existingEnv.Provider)
	if err != nil {
		return "", err
	}

	snapshotter, ok := provider.(env.Snapshotter)
	if !ok {
		return "", fmt.Errorf("provider %s does not support snapshots", existingEnv.Provider)
	}

	imageRef, err := snapshotter.SnapshotEnvironment(ctx, existingEnv.ClusterName, tag)
	if err != nil {
		return "", fmt.Errorf("failed to snapshot environment: %w", err)
	}

	return imageRef, nil
}

// SleepEnvironment snapshots the env, stops the VM, and sets status to "sleeping".
func (s *EnvService) SleepEnvironment(ctx context.Context, envID, orgID string) error {
	existingEnv, err := s.EnvRepo.GetByID(ctx, envID)
	if err != nil {
		return fmt.Errorf("environment not found: %w", err)
	}
	if existingEnv.OrgID != orgID {
		return fmt.Errorf("environment does not belong to this org")
	}
	if existingEnv.Status != "running" {
		return fmt.Errorf("can only sleep a running environment (status: %s)", existingEnv.Status)
	}

	tag := fmt.Sprintf("sleep-%s-%d", existingEnv.Name, time.Now().Unix())
	if existingEnv.ClusterName != "" {
		provider, pErr := s.getProvider(existingEnv.Provider)
		if pErr == nil {
			if snapshotter, ok := provider.(env.Snapshotter); ok {
				if imageRef, sErr := snapshotter.SnapshotEnvironment(ctx, existingEnv.ClusterName, tag); sErr == nil {
					log.Printf("[sleep] Snapshot taken for env %s: %s", envID, imageRef)
				}
			}
			if err := provider.DestroyEnvironment(ctx, existingEnv.ClusterName); err != nil {
				log.Printf("[sleep] Failed to stop VM for env %s: %v", envID, err)
			}
		}
	}

	existingEnv.Status = "sleeping"
	existingEnv.UpdatedAt = time.Now()
	return s.EnvRepo.Update(ctx, existingEnv)
}

// WakeEnvironment restores VM from snapshot, waits for ready, and sets status to "running".
func (s *EnvService) WakeEnvironment(ctx context.Context, envID, orgID string) error {
	existingEnv, err := s.EnvRepo.GetByID(ctx, envID)
	if err != nil {
		return fmt.Errorf("environment not found: %w", err)
	}
	if existingEnv.OrgID != orgID {
		return fmt.Errorf("environment does not belong to this org")
	}
	if existingEnv.Status != "sleeping" {
		return fmt.Errorf("can only wake a sleeping environment (status: %s)", existingEnv.Status)
	}

	existingEnv.Status = "creating"
	existingEnv.UpdatedAt = time.Now()
	if err := s.EnvRepo.Update(ctx, existingEnv); err != nil {
		return err
	}

	provider, err := s.getProvider(existingEnv.Provider)
	if err != nil {
		return err
	}

	regURL, regUser, regPass := s.resolveRegistry(ctx, existingEnv.OrgID)

	providerCfg := env.ProviderConfig{
		Name:          existingEnv.Name,
		Region:        existingEnv.Region,
		Size:          existingEnv.Size,
		Resources:     existingEnv.Resources,
		RegistryURL:   regURL,
		RegistryUser:  regUser,
		RegistryPass:  regPass,
		EnvID:         envID,
		OrgID:         existingEnv.OrgID,
		Branch:        existingEnv.ContextBranch,
		APIURL:        s.config.APIURL,
		NATSUrl:       s.config.NATSUrl,
		NATSAuthToken: s.config.NATSAuthToken,
	}

	go func() {
		bgCtx := context.Background()
		providerRef, createErr := provider.CreateEnvironment(bgCtx, &providerCfg)
		if createErr != nil {
			log.Printf("[wake] Failed to wake environment %s: %v", envID, createErr)
			s.setEnvStatus(bgCtx, envID, "sleeping", "")
			return
		}
		var ip string
		if netInfo, ok := env.AsNetworkInfo(provider); ok {
			ip, _ = netInfo.GetServerIP(bgCtx, providerRef)
		}
		s.setEnvStatusFull(bgCtx, envID, "running", providerRef, ip, 0)
		log.Printf("[wake] Environment %s woken up on %s", envID, providerRef)
	}()

	return nil
}

func (s *EnvService) GetByOrgRepoAndBranch(ctx context.Context, orgID, repoFullName, branch string) (*models.Environment, error) {
	return s.EnvRepo.GetByOrgRepoAndBranch(ctx, orgID, repoFullName, branch)
}

func (s *EnvService) ListAllRunning(ctx context.Context) ([]*models.Environment, error) {
	query := `
		SELECT id, name, org_id, COALESCE(repo_full_name, ''), provider, region, size, COALESCE(cluster_name, ''), COALESCE(ip_address, ''),
		       status, resources, config, context_branch, created_at, updated_at, destroyed_at
		FROM environments
		WHERE status = 'running'
		ORDER BY updated_at ASC
	`
	rows, err := s.EnvRepo.DB().Pool.Query(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var envs []*models.Environment
	for rows.Next() {
		var e models.Environment
		var destroyedAt *time.Time
		var resourcesJSON, configJSON []byte
		err := rows.Scan(
			&e.ID, &e.Name, &e.OrgID, &e.RepoFullName, &e.Provider, &e.Region, &e.Size, &e.ClusterName, &e.IPAddress,
			&e.Status, &resourcesJSON, &configJSON, &e.ContextBranch, &e.CreatedAt, &e.UpdatedAt, &destroyedAt,
		)
		if err != nil {
			return nil, err
		}
		e.DestroyedAt = destroyedAt
		json.Unmarshal(resourcesJSON, &e.Resources)
		if configJSON != nil {
			json.Unmarshal(configJSON, &e.Config)
		}
		envs = append(envs, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return envs, nil
}

func (s *EnvService) GetEnvironment(ctx context.Context, envID string) (*models.Environment, error) {
	return s.EnvRepo.GetByID(ctx, envID)
}

func (s *EnvService) ListEnvironments(ctx context.Context, orgID string) ([]*models.Environment, error) {
	return s.EnvRepo.GetByOrgID(ctx, orgID)
}

// GetHetznerProvider returns the Hetzner provider (for SSH access, etc.)
// Deprecated: Use GetProviderForEnv() or GetProviderByName() instead for provider-agnostic code.
func (s *EnvService) GetHetznerProvider() env.Provider {
	return s.providers["hetzner"]
}

// GetProviderForEnv returns the cloud provider for a given environment.
// This is the preferred method — it's provider-agnostic.
func (s *EnvService) GetProviderForEnv(ctx context.Context, envID string) (env.Provider, error) {
	e, err := s.EnvRepo.GetByID(ctx, envID)
	if err != nil {
		return nil, err
	}
	return s.getProvider(e.Provider)
}

// GetProviderByName returns the cloud provider by name.
func (s *EnvService) GetProviderByName(providerName string) (env.Provider, error) {
	return s.getProvider(providerName)
}

// setEnvStatus updates only the status and cluster_name (provider ref) fields.
// Used from goroutines to avoid data races with shared Environment pointers.
func (s *EnvService) setEnvStatus(ctx context.Context, envID, status, clusterName string) {
	query := `
		UPDATE environments
		SET status = $2, cluster_name = CASE WHEN $3 = '' THEN cluster_name ELSE $3 END, updated_at = NOW()
		WHERE id = $1
	`
	if _, err := s.EnvRepo.DB().Pool.Exec(ctx, query, envID, status, clusterName); err != nil {
		log.Printf("Failed to update environment %s status to %s: %v", envID, status, err)
	}
}

// setEnvStatusFull updates status, cluster_name, IP address, and boot time in config.
// Tracks boot metrics so we can report real numbers.
func (s *EnvService) setEnvStatusFull(ctx context.Context, envID, status, clusterName, ipAddress string, bootTime time.Duration) {
	query := `
		UPDATE environments
		SET status = $2,
		    cluster_name = CASE WHEN $3 = '' THEN cluster_name ELSE $3 END,
		    ip_address = CASE WHEN $4 = '' THEN ip_address ELSE $4 END,
		    config = COALESCE(config, '{}')::jsonb || jsonb_build_object(
		        'boot_time_ms', $5::bigint,
		        'boot_type', CASE WHEN $5 < 30000 THEN 'warm' ELSE 'cold' END
		    ),
		    updated_at = NOW()
		WHERE id = $1
	`
	if _, err := s.EnvRepo.DB().Pool.Exec(ctx, query, envID, status, clusterName, ipAddress, bootTime.Milliseconds()); err != nil {
		log.Printf("Failed to update environment %s status to %s: %v", envID, status, err)
	}
}

func (s *EnvService) getProvider(providerName string) (env.Provider, error) {
	if p, ok := s.providers[providerName]; ok {
		return p, nil
	}
	available := s.AvailableProviders()
	if len(available) == 0 {
		return nil, fmt.Errorf("no cloud providers configured — set provider credentials in .env")
	}
	return nil, fmt.Errorf("provider %q not configured (available: %s)", providerName, strings.Join(available, ", "))
}

// GetProvider returns the cloud provider for a given provider name.
// Deprecated: Use GetProviderByName() which returns an error if not found.
func (s *EnvService) GetProvider(providerName string) env.Provider {
	p, _ := s.getProvider(providerName)
	return p
}

// resolveRegistry checks if the org has a custom container registry configured.
// Returns (url, user, pass). If the org has no custom registry, returns empty strings
// and the provider will fall back to its platform-level default from env vars.
//
// Enterprise orgs set their own registry in org_settings for:
//   - Data sovereignty (images stay in their cloud)
//   - Compliance (auditable, their infrastructure)
//   - Isolation (no cross-org image access)
func (s *EnvService) resolveRegistry(ctx context.Context, orgID string) (string, string, string) {
	if orgID == "" {
		return "", "", ""
	}

	var regURL, regUser, regPass *string
	err := s.EnvRepo.DB().Pool.QueryRow(ctx, `
		SELECT registry_url, registry_user, registry_pass
		FROM org_settings
		WHERE org_id = $1`, orgID).Scan(&regURL, &regUser, &regPass)
	if err != nil {
		return "", "", "" // no org settings or error — use platform default
	}

	if regURL != nil && *regURL != "" {
		user, pass := "", ""
		if regUser != nil {
			user = *regUser
		}
		if regPass != nil {
			pass = *regPass
		}
		return *regURL, user, pass
	}

	return "", "", "" // org exists but no custom registry — use platform default
}
