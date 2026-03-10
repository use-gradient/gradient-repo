package services

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/pkg/env"
)

// WarmPoolService maintains a small pool of pre-booted servers ready for instant
// environment assignment. Instead of cold-booting a server on every
// `gc env create` (2–6 minutes), this pre-provisions servers so assignment
// takes ~5–15 seconds (start container + agent on already-running server).
//
// v0.1 LIMITS (keep costs manageable before revenue):
//   - Global max: 5 warm servers total across all sizes/regions
//   - Default: 2 small, 1 medium (3 total)
//   - Large/GPU: on-demand only (too expensive to keep warm)
//
// BILLING: Warm servers cost money even when idle. The customer is billed
// from the moment a warm server is assigned to their environment (assigned_at),
// NOT from when the server was first booted. Idle warm pool cost is our cost.
//
// Boot time comparison:
//   - Cold boot (no pool):    2–6 min (create server + cloud-init + Docker + pull image)
//   - Warm pool:              5–15s  (assign existing server, start container)
//   - Warm pool + pre-pulled: 3–8s   (container is already pulled)
type WarmPoolService struct {
	db         *db.DB
	envService *EnvService
	mu         sync.Mutex
	stopCh     chan struct{}
	running    bool

	// Pool configuration per size/region/provider combo
	targets map[string]PoolTarget

	// Global pool constraints (configurable via env vars)
	poolCfg WarmPoolConfig
}

// PoolTarget defines the desired pool size for a provider/size/region combo.
type PoolTarget struct {
	Provider string // "hetzner", "aws", "gcp", etc.
	Size     string // small, medium, large
	Region   string // provider-specific region identifier
	MinReady int    // minimum number of warm servers to keep
	MaxReady int    // maximum warm servers (cost cap)
	PrePull  string // container image to pre-pull on warm servers
}

// WarmServer represents a pre-booted server waiting for assignment.
type WarmServer struct {
	ID         string     `json:"id"`
	ProviderID string     `json:"provider_id"` // provider-specific server/instance ID
	IPAddress  string     `json:"ip_address"`
	Provider   string     `json:"provider"` // which cloud provider ("hetzner", "aws", etc.)
	Size       string     `json:"size"`
	Region     string     `json:"region"`
	Status     string     `json:"status"`                // "warming", "ready", "assigned", "draining"
	AssignedTo string     `json:"assigned_to,omitempty"` // environment ID if assigned
	CreatedAt  time.Time  `json:"created_at"`
	ReadyAt    *time.Time `json:"ready_at,omitempty"`
	AssignedAt *time.Time `json:"assigned_at,omitempty"`
}

// HardMaxWarmPoolSize is the absolute hard cap. Even if someone sets WARM_POOL_MAX_SIZE=100
// this will stop them. 8 servers at Hetzner CX22 = ~$7/day = ~$210/month. Beyond this
// you should be using autoscaling, not a warm pool.
const HardMaxWarmPoolSize = 8

// WarmPoolConfig holds the configurable warm pool settings, loaded from environment variables.
// See internal/config/config.go for env var names.
//
// COST MATH (Hetzner CX22 = ~$0.03–0.04/hr as of March 2026):
//   - 1 server  = ~$0.80/day  = ~$24/month
//   - 3 servers = ~$2.40/day  = ~$72/month  (default)
//   - 5 servers = ~$4.00/day  = ~$120/month
//   - 8 servers = ~$6.40/day  = ~$192/month (hard cap)
//
// REPLENISH POLICY:
//   - Runs every 30 seconds
//   - For each size/region: if ready < MinReady AND total < MaxSize → create servers
//   - Global cap: never exceed WarmPoolConfig.MaxSize total (ready + warming)
//   - Idle cleanup: servers idle longer than IdleTimeout are destroyed
//   - Stale cleanup: warming servers stuck >15 min are deleted
type WarmPoolConfig struct {
	DefaultSize int           // Total warm servers to maintain (default: 3)
	MaxSize     int           // Hard cap, clamped to HardMaxWarmPoolSize (default: 3, max: 8)
	IdleTimeout time.Duration // Destroy warm servers idle longer than this (default: 30m)
}

// DefaultPoolTargets returns conservative warm pool defaults for v0.1.
// The pool targets are sized proportionally to the configured DefaultSize.
// Large/GPU are on-demand only — too expensive to keep warm pre-revenue.
func DefaultPoolTargets(provider string, region string, poolCfg WarmPoolConfig) map[string]PoolTarget {
	if provider == "" {
		provider = "hetzner"
	}
	if region == "" {
		region = "fsn1"
	}

	// Distribute the default pool size: 2/3 small, 1/3 medium (minimum 1 each if size >= 2)
	smallMin := 1
	mediumMin := 0
	if poolCfg.DefaultSize >= 2 {
		mediumMin = 1
		smallMin = poolCfg.DefaultSize - mediumMin
	} else if poolCfg.DefaultSize == 1 {
		smallMin = 1
		mediumMin = 0
	} else {
		smallMin = 0
		mediumMin = 0
	}

	// Cap MaxReady per target so they don't exceed the global max individually
	smallMax := smallMin + 2
	if smallMax > poolCfg.MaxSize {
		smallMax = poolCfg.MaxSize
	}
	mediumMax := mediumMin + 1
	if mediumMax > poolCfg.MaxSize {
		mediumMax = poolCfg.MaxSize
	}

	targets := make(map[string]PoolTarget)
	if smallMin > 0 || smallMax > 0 {
		targets[fmt.Sprintf("%s:small:%s", provider, region)] = PoolTarget{
			Provider: provider, Size: "small", Region: region,
			MinReady: smallMin, MaxReady: smallMax,
		}
	}
	if mediumMin > 0 || mediumMax > 0 {
		targets[fmt.Sprintf("%s:medium:%s", provider, region)] = PoolTarget{
			Provider: provider, Size: "medium", Region: region,
			MinReady: mediumMin, MaxReady: mediumMax,
		}
	}

	// AWS targets (compliance workloads — separate from Hetzner cost pool)
	targets["aws:small:us-east-2"] = PoolTarget{
		Provider: "aws", Size: "small", Region: "us-east-2",
		MinReady: 2, MaxReady: 4,
	}
	targets["aws:medium:us-east-2"] = PoolTarget{
		Provider: "aws", Size: "medium", Region: "us-east-2",
		MinReady: 1, MaxReady: 2,
	}

	// Large/GPU: on-demand only (no warm pool — too expensive pre-revenue)
	return targets
}

// NewWarmPoolService creates a warm pool manager.
// It uses envService to resolve providers by name, so it works with any configured provider.
// poolCfg controls global limits — pass WarmPoolConfig{} for defaults (3 servers, 30m idle timeout).
func NewWarmPoolService(database *db.DB, envService *EnvService, targets map[string]PoolTarget, poolCfg WarmPoolConfig) *WarmPoolService {
	// Apply defaults if not set
	if poolCfg.DefaultSize <= 0 {
		poolCfg.DefaultSize = 3
	}
	if poolCfg.MaxSize <= 0 {
		poolCfg.MaxSize = poolCfg.DefaultSize
	}
	if poolCfg.MaxSize > HardMaxWarmPoolSize {
		poolCfg.MaxSize = HardMaxWarmPoolSize
	}
	if poolCfg.IdleTimeout <= 0 {
		poolCfg.IdleTimeout = 30 * time.Minute
	}

	log.Printf("[warm-pool] config: default_size=%d, max_size=%d, idle_timeout=%s",
		poolCfg.DefaultSize, poolCfg.MaxSize, poolCfg.IdleTimeout)

	return &WarmPoolService{
		db:         database,
		envService: envService,
		targets:    targets,
		poolCfg:    poolCfg,
		stopCh:     make(chan struct{}),
	}
}

// Start begins the background replenishment loop.
func (p *WarmPoolService) Start(interval time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.running {
		return
	}
	p.running = true

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("[warm-pool] replenishment loop started (interval=%s)", interval)

		// Initial replenish on startup
		p.replenish(context.Background())

		for {
			select {
			case <-p.stopCh:
				log.Println("[warm-pool] replenishment loop stopped")
				return
			case <-ticker.C:
				p.replenish(context.Background())
			}
		}
	}()
}

// Stop halts the replenishment loop.
func (p *WarmPoolService) Stop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.running {
		close(p.stopCh)
		p.running = false
	}
}

// AcquireServer grabs a warm server for the given provider/size/region.
// Returns nil if no warm servers are available (caller should fall back to cold boot).
func (p *WarmPoolService) AcquireServer(ctx context.Context, provider, size, region string) (*WarmServer, error) {
	now := time.Now()

	row := p.db.Pool.QueryRow(ctx, `
		UPDATE warm_pool
		SET status = 'assigned', assigned_at = $1
		WHERE id = (
			SELECT id FROM warm_pool
			WHERE provider = $2 AND size = $3 AND region = $4 AND status = 'ready'
			ORDER BY ready_at ASC
			LIMIT 1
			FOR UPDATE SKIP LOCKED
		)
		RETURNING id, provider_id, ip_address, provider, size, region, status, created_at, ready_at, assigned_at`,
		now, provider, size, region)

	ws := &WarmServer{}
	var readyAt, assignedAt sql.NullTime
	err := row.Scan(
		&ws.ID, &ws.ProviderID, &ws.IPAddress,
		&ws.Provider, &ws.Size, &ws.Region, &ws.Status,
		&ws.CreatedAt, &readyAt, &assignedAt,
	)
	if err != nil {
		return nil, nil // no warm server available — not an error, caller should cold-boot
	}
	if readyAt.Valid {
		ws.ReadyAt = &readyAt.Time
	}
	if assignedAt.Valid {
		ws.AssignedAt = &assignedAt.Time
	}

	log.Printf("[warm-pool] assigned server %s (%s/%s/%s) — was warm for %s",
		ws.ProviderID, ws.Provider, ws.Size, ws.Region,
		now.Sub(ws.CreatedAt).Round(time.Second))

	return ws, nil
}

// MarkAssigned links a warm server to an environment ID.
func (p *WarmPoolService) MarkAssigned(ctx context.Context, warmServerID, envID string) error {
	_, err := p.db.Pool.Exec(ctx,
		`UPDATE warm_pool SET assigned_to = $1, status = 'assigned' WHERE id = $2`,
		envID, warmServerID)
	return err
}

// ReturnServer cleans a server and returns it to the pool.
// Used when an environment is destroyed but the server can be recycled.
// Works with any provider that implements RemoteExecutor.
func (p *WarmPoolService) ReturnServer(ctx context.Context, providerName, providerID string) error {
	provider, err := p.envService.GetProviderByName(providerName)
	if err != nil {
		return fmt.Errorf("provider %s not configured: %w", providerName, err)
	}

	// Try to clean the server via remote exec (provider-agnostic)
	if executor, ok := env.AsRemoteExecutor(provider); ok {
		_, execErr := executor.ExecCommand(ctx, providerID,
			"docker stop gradient-env 2>/dev/null; docker rm -f gradient-env 2>/dev/null; "+
				"rm -rf /home/gradient/workspace/* /gradient/context/* /tmp/gradient-*; "+
				"echo 'recycled'",
			2*time.Minute)
		if execErr != nil {
			log.Printf("[warm-pool] failed to clean server %s for recycling: %v", providerID, execErr)
			// Mark for drain instead
			p.db.Pool.Exec(ctx,
				`UPDATE warm_pool SET status = 'draining' WHERE provider_id = $1`, providerID)
			return execErr
		}
	} else {
		// Provider doesn't support remote exec — can't recycle, mark for drain
		log.Printf("[warm-pool] provider %s doesn't support remote exec — draining server %s", providerName, providerID)
		p.db.Pool.Exec(ctx,
			`UPDATE warm_pool SET status = 'draining' WHERE provider_id = $1`, providerID)
		return nil
	}

	now := time.Now()
	_, err = p.db.Pool.Exec(ctx, `
		UPDATE warm_pool
		SET status = 'ready', assigned_to = NULL, assigned_at = NULL, ready_at = $1
		WHERE provider_id = $2`,
		now, providerID)

	log.Printf("[warm-pool] server %s returned to pool", providerID)
	return err
}

// Stats returns current pool statistics.
func (p *WarmPoolService) Stats(ctx context.Context) map[string]interface{} {
	rows, err := p.db.Pool.Query(ctx, `
		SELECT provider, size, region, status, COUNT(*)
		FROM warm_pool
		GROUP BY provider, size, region, status
		ORDER BY provider, size, region, status`)
	if err != nil {
		return map[string]interface{}{"error": err.Error()}
	}
	defer rows.Close()

	type bucket struct {
		Provider, Size, Region, Status string
		Count                          int
	}
	var buckets []bucket
	for rows.Next() {
		var b bucket
		if rows.Scan(&b.Provider, &b.Size, &b.Region, &b.Status, &b.Count) == nil {
			buckets = append(buckets, b)
		}
	}

	return map[string]interface{}{
		"buckets": buckets,
		"targets": p.targets,
	}
}

// replenish ensures each provider/size/region combo has enough warm servers,
// while respecting the global poolCfg.MaxSize cap.
//
// POLICY (documented for cost transparency):
//   - Check global total: if ready+warming >= MaxSize → stop, do nothing
//   - For each target (e.g. hetzner:small:fsn1): if ready < MinReady → create deficit servers
//   - Cap deficit at target.MaxReady and global MaxSize
//   - Clean up idle servers: if ready > MinReady AND server idle > IdleTimeout → delete excess
//   - Clean up stale warming: servers stuck in "warming" > 15 min → delete (cloud API probably failed)
//   - Clean up draining: servers marked for drain → destroy via provider API
func (p *WarmPoolService) replenish(ctx context.Context) {
	maxSize := p.poolCfg.MaxSize

	// Check global pool size first
	var totalPoolSize int
	p.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM warm_pool WHERE status IN ('ready', 'warming')`).Scan(&totalPoolSize)

	for key, target := range p.targets {
		if totalPoolSize >= maxSize {
			log.Printf("[warm-pool] global cap reached (%d/%d) — skipping replenish for %s",
				totalPoolSize, maxSize, key)
			break
		}

		var readyCount int
		err := p.db.Pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM warm_pool
			WHERE provider = $1 AND size = $2 AND region = $3 AND status IN ('ready', 'warming')`,
			target.Provider, target.Size, target.Region).Scan(&readyCount)
		if err != nil {
			log.Printf("[warm-pool] failed to count pool for %s: %v", key, err)
			continue
		}

		deficit := target.MinReady - readyCount
		if deficit <= 0 {
			continue
		}
		if readyCount >= target.MaxReady {
			continue
		}

		// Cap at MaxReady and global limit
		if readyCount+deficit > target.MaxReady {
			deficit = target.MaxReady - readyCount
		}
		if totalPoolSize+deficit > maxSize {
			deficit = maxSize - totalPoolSize
		}
		if deficit <= 0 {
			continue
		}

		log.Printf("[warm-pool] %s: %d ready, need %d more (target: %d-%d, global: %d/%d)",
			key, readyCount, deficit, target.MinReady, target.MaxReady, totalPoolSize, maxSize)

		for i := 0; i < deficit; i++ {
			go p.provisionWarmServer(ctx, target)
			totalPoolSize++
		}
	}

	// Clean up idle servers that exceed MinReady and have been idle > IdleTimeout.
	// This prevents runaway costs when the pool has excess servers nobody is using.
	p.cleanupIdleServers(ctx)

	// Clean up stale warming servers (stuck > 15 minutes — cloud API probably failed)
	p.db.Pool.Exec(ctx, `
		DELETE FROM warm_pool
		WHERE status = 'warming' AND created_at < NOW() - INTERVAL '15 minutes'`)

	// Clean up draining servers
	p.drainStaleServers(ctx)
}

// cleanupIdleServers destroys warm servers that have been idle (ready but unassigned)
// longer than the configured IdleTimeout, but only if the pool is above MinReady for
// that size/region. This prevents burning money on servers nobody is using.
func (p *WarmPoolService) cleanupIdleServers(ctx context.Context) {
	for key, target := range p.targets {
		var readyCount int
		p.db.Pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM warm_pool
			WHERE provider = $1 AND size = $2 AND region = $3 AND status = 'ready'`,
			target.Provider, target.Size, target.Region).Scan(&readyCount)

		if readyCount <= target.MinReady {
			continue // Don't go below MinReady
		}

		// Find servers idle longer than IdleTimeout
		idleCutoff := time.Now().Add(-p.poolCfg.IdleTimeout)
		rows, err := p.db.Pool.Query(ctx, `
			SELECT id, provider_id, provider FROM warm_pool
			WHERE provider = $1 AND size = $2 AND region = $3
			  AND status = 'ready' AND ready_at < $4
			ORDER BY ready_at ASC
			LIMIT $5`,
			target.Provider, target.Size, target.Region, idleCutoff, readyCount-target.MinReady)
		if err != nil {
			continue
		}

		for rows.Next() {
			var id, providerID, providerName string
			if rows.Scan(&id, &providerID, &providerName) != nil {
				continue
			}

			provider, err := p.envService.GetProviderByName(providerName)
			if err != nil {
				log.Printf("[warm-pool] idle cleanup: provider %s not found — skipping %s", providerName, providerID)
				continue
			}

			if err := provider.DestroyEnvironment(ctx, providerID); err != nil {
				log.Printf("[warm-pool] idle cleanup: failed to destroy %s: %v", providerID, err)
				continue
			}

			p.db.Pool.Exec(ctx, `DELETE FROM warm_pool WHERE id = $1`, id)
			log.Printf("[warm-pool] idle cleanup: destroyed %s (%s/%s/%s) — idle > %s",
				providerID, target.Provider, target.Size, target.Region, p.poolCfg.IdleTimeout)
		}
		rows.Close()

		_ = key // used in log context
	}
}

// provisionWarmServer creates a new server via the target's provider.
func (p *WarmPoolService) provisionWarmServer(ctx context.Context, target PoolTarget) {
	provider, err := p.envService.GetProviderByName(target.Provider)
	if err != nil {
		log.Printf("[warm-pool] provider %s not configured: %v", target.Provider, err)
		return
	}

	id := uuid.New().String()
	now := time.Now()

	// Record in DB as "warming"
	_, err = p.db.Pool.Exec(ctx, `
		INSERT INTO warm_pool (id, provider, size, region, status, created_at)
		VALUES ($1, $2, $3, $4, 'warming', $5)`,
		id, target.Provider, target.Size, target.Region, now)
	if err != nil {
		log.Printf("[warm-pool] failed to insert warm server record: %v", err)
		return
	}

	// Create the server (this calls the real cloud API — provider-agnostic)
	providerRef, err := provider.CreateEnvironment(ctx, &env.ProviderConfig{
		Name:   fmt.Sprintf("warm-%s-%s", target.Size, id[:8]),
		Region: target.Region,
		Size:   target.Size,
	})
	if err != nil {
		log.Printf("[warm-pool] failed to create warm server via %s: %v", target.Provider, err)
		p.db.Pool.Exec(ctx, `DELETE FROM warm_pool WHERE id = $1`, id)
		return
	}

	// Get the server's IP (if the provider supports it)
	var ip string
	if netInfo, ok := env.AsNetworkInfo(provider); ok {
		ip, err = netInfo.GetServerIP(ctx, providerRef)
		if err != nil {
			log.Printf("[warm-pool] failed to get IP for warm server %s: %v", providerRef, err)
			p.db.Pool.Exec(ctx, `DELETE FROM warm_pool WHERE id = $1`, id)
			return
		}
	}

	// Wait for the server to be ready (if the provider supports remote exec)
	if executor, ok := env.AsRemoteExecutor(provider); ok {
		if err := executor.WaitForReady(ctx, providerRef, 5*time.Minute); err != nil {
			log.Printf("[warm-pool] warm server %s not ready: %v", providerRef, err)
			p.db.Pool.Exec(ctx, `DELETE FROM warm_pool WHERE id = $1`, id)
			return
		}

		// Pre-pull container image if configured
		if target.PrePull != "" {
			_, err := executor.ExecCommand(ctx, providerRef,
				fmt.Sprintf("docker pull %s", target.PrePull), 5*time.Minute)
			if err != nil {
				log.Printf("[warm-pool] pre-pull failed on %s: %v", providerRef, err)
				// Non-fatal — server is still usable
			}
		}
	}

	// Mark as ready
	readyAt := time.Now()
	p.db.Pool.Exec(ctx, `
		UPDATE warm_pool
		SET provider_id = $1, ip_address = $2, status = 'ready', ready_at = $3
		WHERE id = $4`,
		providerRef, ip, readyAt, id)

	bootTime := readyAt.Sub(now).Round(time.Second)
	log.Printf("[warm-pool] server %s (%s/%s/%s) ready in %s — waiting for assignment",
		providerRef, target.Provider, target.Size, target.Region, bootTime)
}

// drainStaleServers destroys servers marked for draining.
func (p *WarmPoolService) drainStaleServers(ctx context.Context) {
	rows, err := p.db.Pool.Query(ctx, `
		SELECT id, provider_id, provider FROM warm_pool
		WHERE status = 'draining'`)
	if err != nil {
		return
	}
	defer rows.Close()

	for rows.Next() {
		var id, providerID, providerName string
		if rows.Scan(&id, &providerID, &providerName) != nil {
			continue
		}
		provider, err := p.envService.GetProviderByName(providerName)
		if err != nil {
			log.Printf("[warm-pool] provider %s not configured, can't drain server %s", providerName, providerID)
			continue
		}
		if err := provider.DestroyEnvironment(ctx, providerID); err != nil {
			log.Printf("[warm-pool] failed to drain server %s: %v", providerID, err)
			continue
		}
		p.db.Pool.Exec(ctx, `DELETE FROM warm_pool WHERE id = $1`, id)
		log.Printf("[warm-pool] drained server %s (%s)", providerID, providerName)
	}
}
