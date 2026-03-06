package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/gradient/gradient/pkg/env"
)

// AutoscaleService manages horizontal autoscaling policies for environments.
// It monitors environment health metrics from the agent and scales replicas
// (additional Docker containers on the same server) up/down based on CPU/memory thresholds.
//
// v0.1: Container-only scaling. Server-level scaling (spinning up new servers)
// is disabled to keep costs manageable and avoid complexity. Scale up adds
// containers to existing servers; scale down removes them. If a server is at
// container capacity, scaling stops (it does NOT create new servers).
type AutoscaleService struct {
	db             *db.DB
	envService     *EnvService
	mu             sync.RWMutex
	monitorCancel  context.CancelFunc
	monitorRunning bool
}

// NewAutoscaleService creates a new autoscale service.
func NewAutoscaleService(database *db.DB, envService *EnvService) *AutoscaleService {
	return &AutoscaleService{
		db:         database,
		envService: envService,
	}
}

// CreateOrUpdatePolicy creates or updates an autoscale policy for an environment.
func (s *AutoscaleService) CreateOrUpdatePolicy(ctx context.Context, orgID string, policy *models.AutoscalePolicy) (*models.AutoscalePolicy, error) {
	if policy.EnvironmentID == "" {
		return nil, fmt.Errorf("environment_id is required")
	}
	if policy.MinReplicas < 1 {
		policy.MinReplicas = 1
	}
	if policy.MaxReplicas < policy.MinReplicas {
		policy.MaxReplicas = policy.MinReplicas
	}
	if policy.MaxReplicas > 10 {
		return nil, fmt.Errorf("max_replicas cannot exceed 10 in v0.1 (container-only scaling)")
	}
	if policy.TargetCPU <= 0 || policy.TargetCPU > 1.0 {
		policy.TargetCPU = 0.70
	}
	if policy.TargetMemory <= 0 || policy.TargetMemory > 1.0 {
		policy.TargetMemory = 0.80
	}
	if policy.CooldownSecs < 60 {
		policy.CooldownSecs = 300
	}
	if policy.ScaleUpThreshold <= 0 {
		policy.ScaleUpThreshold = 0.85
	}
	if policy.ScaleDownThreshold <= 0 {
		policy.ScaleDownThreshold = 0.30
	}

	now := time.Now()
	policy.OrgID = orgID
	policy.UpdatedAt = now

	// Check if policy already exists
	existing, _ := s.GetPolicy(ctx, orgID, policy.EnvironmentID)
	if existing != nil {
		// Update existing policy
		_, err := s.db.Pool.Exec(ctx, `
			UPDATE autoscale_policies
			SET min_replicas = $1, max_replicas = $2, target_cpu = $3, target_memory = $4,
			    cooldown_secs = $5, enabled = $6, scale_up_threshold = $7, scale_down_threshold = $8,
			    updated_at = $9
			WHERE environment_id = $10 AND org_id = $11`,
			policy.MinReplicas, policy.MaxReplicas, policy.TargetCPU, policy.TargetMemory,
			policy.CooldownSecs, policy.Enabled, policy.ScaleUpThreshold, policy.ScaleDownThreshold,
			now, policy.EnvironmentID, orgID)
		if err != nil {
			return nil, fmt.Errorf("failed to update autoscale policy: %w", err)
		}
		policy.ID = existing.ID
		policy.CurrentReplicas = existing.CurrentReplicas
		policy.LastScaleAt = existing.LastScaleAt
		policy.CreatedAt = existing.CreatedAt
		return policy, nil
	}

	// Create new policy
	policy.ID = uuid.New().String()
	policy.CreatedAt = now
	policy.CurrentReplicas = 1

	_, err := s.db.Pool.Exec(ctx, `
		INSERT INTO autoscale_policies
		(id, environment_id, org_id, min_replicas, max_replicas, target_cpu, target_memory,
		 cooldown_secs, current_replicas, enabled, scale_up_threshold, scale_down_threshold,
		 created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		policy.ID, policy.EnvironmentID, orgID,
		policy.MinReplicas, policy.MaxReplicas, policy.TargetCPU, policy.TargetMemory,
		policy.CooldownSecs, policy.CurrentReplicas, policy.Enabled,
		policy.ScaleUpThreshold, policy.ScaleDownThreshold,
		now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to create autoscale policy: %w", err)
	}

	return policy, nil
}

// GetPolicy returns the autoscale policy for an environment.
func (s *AutoscaleService) GetPolicy(ctx context.Context, orgID, envID string) (*models.AutoscalePolicy, error) {
	row := s.db.Pool.QueryRow(ctx, `
		SELECT id, environment_id, org_id, min_replicas, max_replicas, target_cpu, target_memory,
		       cooldown_secs, current_replicas, enabled, scale_up_threshold, scale_down_threshold,
		       last_scale_at, last_scale_direction, created_at, updated_at
		FROM autoscale_policies WHERE environment_id = $1 AND org_id = $2`, envID, orgID)

	p := &models.AutoscalePolicy{}
	var lastScaleAt sql.NullTime
	var lastScaleDirection sql.NullString
	err := row.Scan(
		&p.ID, &p.EnvironmentID, &p.OrgID,
		&p.MinReplicas, &p.MaxReplicas, &p.TargetCPU, &p.TargetMemory,
		&p.CooldownSecs, &p.CurrentReplicas, &p.Enabled,
		&p.ScaleUpThreshold, &p.ScaleDownThreshold,
		&lastScaleAt, &lastScaleDirection, &p.CreatedAt, &p.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("autoscale policy not found: %w", err)
	}
	if lastScaleAt.Valid {
		p.LastScaleAt = &lastScaleAt.Time
	}
	if lastScaleDirection.Valid {
		p.LastScaleDirection = lastScaleDirection.String
	}
	return p, nil
}

// DeletePolicy removes an autoscale policy.
func (s *AutoscaleService) DeletePolicy(ctx context.Context, orgID, envID string) error {
	_, err := s.db.Pool.Exec(ctx,
		`DELETE FROM autoscale_policies WHERE environment_id = $1 AND org_id = $2`,
		envID, orgID)
	return err
}

// ListPolicies returns all autoscale policies for an org.
func (s *AutoscaleService) ListPolicies(ctx context.Context, orgID string) ([]*models.AutoscalePolicy, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, environment_id, org_id, min_replicas, max_replicas, target_cpu, target_memory,
		       cooldown_secs, current_replicas, enabled, scale_up_threshold, scale_down_threshold,
		       last_scale_at, last_scale_direction, created_at, updated_at
		FROM autoscale_policies WHERE org_id = $1 ORDER BY created_at DESC`, orgID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var policies []*models.AutoscalePolicy
	for rows.Next() {
		p := &models.AutoscalePolicy{}
		var lastScaleAt sql.NullTime
		var lastScaleDirection sql.NullString
		if err := rows.Scan(
			&p.ID, &p.EnvironmentID, &p.OrgID,
			&p.MinReplicas, &p.MaxReplicas, &p.TargetCPU, &p.TargetMemory,
			&p.CooldownSecs, &p.CurrentReplicas, &p.Enabled,
			&p.ScaleUpThreshold, &p.ScaleDownThreshold,
			&lastScaleAt, &lastScaleDirection, &p.CreatedAt, &p.UpdatedAt,
		); err != nil {
			continue
		}
		if lastScaleAt.Valid {
			p.LastScaleAt = &lastScaleAt.Time
		}
		if lastScaleDirection.Valid {
			p.LastScaleDirection = lastScaleDirection.String
		}
		policies = append(policies, p)
	}
	return policies, nil
}

// GetStatus returns the current autoscale status including live metrics and replica info.
func (s *AutoscaleService) GetStatus(ctx context.Context, orgID, envID string) (*models.AutoscaleStatus, error) {
	policy, err := s.GetPolicy(ctx, orgID, envID)
	if err != nil {
		return nil, err
	}

	// Get the parent environment to read health metrics from config
	row := s.db.Pool.QueryRow(ctx,
		`SELECT config FROM environments WHERE id = $1 AND org_id = $2`,
		envID, orgID)

	var configJSON []byte
	if err := row.Scan(&configJSON); err != nil {
		return nil, fmt.Errorf("environment not found: %w", err)
	}

	var envConfig map[string]interface{}
	json.Unmarshal(configJSON, &envConfig)

	// Extract health metrics from env config (set by agent health reports)
	cpuUsage := extractFloat(envConfig, "cpu_percent")
	memUsage := extractFloat(envConfig, "mem_percent")

	// Count sibling environments (replicas)
	var replicaCount int
	s.db.Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM environments
		 WHERE org_id = $1 AND status = 'running'
		 AND config->>'autoscale_parent' = $2`, orgID, envID).Scan(&replicaCount)

	status := &models.AutoscaleStatus{
		Policy:          *policy,
		CurrentCPU:      cpuUsage,
		CurrentMemory:   memUsage,
		ActiveReplicas:  replicaCount + 1, // +1 for the parent
		DesiredReplicas: policy.CurrentReplicas,
		ScalingActive:   policy.Enabled,
	}

	// Determine if scaling action is pending
	if policy.Enabled {
		if cpuUsage > policy.ScaleUpThreshold || memUsage > policy.ScaleUpThreshold {
			status.PendingAction = "scale_up"
		} else if cpuUsage < policy.ScaleDownThreshold && memUsage < policy.ScaleDownThreshold {
			if policy.CurrentReplicas > policy.MinReplicas {
				status.PendingAction = "scale_down"
			}
		}

		// Check cooldown
		if policy.LastScaleAt != nil {
			cooldownEnd := policy.LastScaleAt.Add(time.Duration(policy.CooldownSecs) * time.Second)
			if time.Now().Before(cooldownEnd) {
				status.CooldownUntil = &cooldownEnd
				status.PendingAction = "cooldown"
			}
		}
	}

	return status, nil
}

// StartMonitor launches a background goroutine that periodically checks all enabled
// autoscale policies and performs scaling actions as needed.
func (s *AutoscaleService) StartMonitor(interval time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.monitorRunning {
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.monitorCancel = cancel
	s.monitorRunning = true

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		log.Printf("[autoscale] monitor started (interval=%s)", interval)

		for {
			select {
			case <-ctx.Done():
				log.Println("[autoscale] monitor stopped")
				return
			case <-ticker.C:
				s.evaluateAll(ctx)
			}
		}
	}()
}

// StopMonitor stops the autoscale monitoring loop.
func (s *AutoscaleService) StopMonitor() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.monitorCancel != nil {
		s.monitorCancel()
		s.monitorRunning = false
	}
}

// evaluateAll checks all enabled policies and performs scaling decisions.
func (s *AutoscaleService) evaluateAll(ctx context.Context) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, environment_id, org_id, min_replicas, max_replicas, target_cpu, target_memory,
		       cooldown_secs, current_replicas, enabled, scale_up_threshold, scale_down_threshold,
		       last_scale_at, last_scale_direction
		FROM autoscale_policies WHERE enabled = true`)
	if err != nil {
		log.Printf("[autoscale] failed to query policies: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var p models.AutoscalePolicy
		var lastScaleAt sql.NullTime
		var lastScaleDirection sql.NullString
		if err := rows.Scan(
			&p.ID, &p.EnvironmentID, &p.OrgID,
			&p.MinReplicas, &p.MaxReplicas, &p.TargetCPU, &p.TargetMemory,
			&p.CooldownSecs, &p.CurrentReplicas, &p.Enabled,
			&p.ScaleUpThreshold, &p.ScaleDownThreshold,
			&lastScaleAt, &lastScaleDirection,
		); err != nil {
			continue
		}
		if lastScaleAt.Valid {
			p.LastScaleAt = &lastScaleAt.Time
		}

		s.evaluate(ctx, &p)
	}
}

// evaluate checks a single policy against current metrics and scales if needed.
func (s *AutoscaleService) evaluate(ctx context.Context, policy *models.AutoscalePolicy) {
	// Check cooldown
	if policy.LastScaleAt != nil {
		cooldownEnd := policy.LastScaleAt.Add(time.Duration(policy.CooldownSecs) * time.Second)
		if time.Now().Before(cooldownEnd) {
			return // still in cooldown
		}
	}

	// Get environment health metrics
	var configJSON []byte
	err := s.db.Pool.QueryRow(ctx,
		`SELECT config FROM environments WHERE id = $1 AND org_id = $2 AND status = 'running'`,
		policy.EnvironmentID, policy.OrgID).Scan(&configJSON)
	if err != nil {
		return // env not running or not found
	}

	var envConfig map[string]interface{}
	json.Unmarshal(configJSON, &envConfig)

	cpuPct := extractFloat(envConfig, "cpu_percent")
	memPct := extractFloat(envConfig, "mem_percent")

	// No metrics available yet
	if cpuPct == 0 && memPct == 0 {
		return
	}

	desired := policy.CurrentReplicas

	// Scale up: if either CPU or memory exceeds the scale-up threshold
	if cpuPct > policy.ScaleUpThreshold || memPct > policy.ScaleUpThreshold {
		// Calculate desired based on how far above target we are
		cpuRatio := cpuPct / policy.TargetCPU
		memRatio := memPct / policy.TargetMemory
		ratio := math.Max(cpuRatio, memRatio)
		desired = int(math.Ceil(float64(policy.CurrentReplicas) * ratio))
	}

	// Scale down: if both CPU and memory are below the scale-down threshold
	if cpuPct < policy.ScaleDownThreshold && memPct < policy.ScaleDownThreshold {
		cpuRatio := cpuPct / policy.TargetCPU
		memRatio := memPct / policy.TargetMemory
		ratio := math.Max(cpuRatio, memRatio)
		if ratio < 0.5 {
			desired = int(math.Max(float64(policy.MinReplicas), float64(policy.CurrentReplicas-1)))
		}
	}

	// Clamp to [min, max]
	if desired < policy.MinReplicas {
		desired = policy.MinReplicas
	}
	if desired > policy.MaxReplicas {
		desired = policy.MaxReplicas
	}

	// No change needed
	if desired == policy.CurrentReplicas {
		return
	}

	direction := "up"
	if desired < policy.CurrentReplicas {
		direction = "down"
	}

	log.Printf("[autoscale] env=%s scaling %s: %d → %d (cpu=%.1f%%, mem=%.1f%%)",
		policy.EnvironmentID, direction, policy.CurrentReplicas, desired, cpuPct*100, memPct*100)

	// Perform scaling
	if desired > policy.CurrentReplicas {
		s.scaleUp(ctx, policy, desired-policy.CurrentReplicas)
	} else {
		s.scaleDown(ctx, policy, policy.CurrentReplicas-desired)
	}

	// Update policy state
	now := time.Now()
	s.db.Pool.Exec(ctx, `
		UPDATE autoscale_policies
		SET current_replicas = $1, last_scale_at = $2, last_scale_direction = $3, updated_at = $2
		WHERE id = $4`,
		desired, now, direction, policy.ID)
}

// scaleUp adds container replicas on the same server.
// v0.1: Container-only. No new servers are created by autoscaling.
// If the server is at capacity, scaling stops and logs a warning.
// Server-level scaling (warm pool → cold boot) planned for v0.2.
func (s *AutoscaleService) scaleUp(ctx context.Context, policy *models.AutoscalePolicy, count int) {
	// Get the parent environment
	row := s.db.Pool.QueryRow(ctx,
		`SELECT name, provider, region, size, cluster_name, context_branch, config FROM environments WHERE id = $1`,
		policy.EnvironmentID)

	var name, provider, region, size string
	var clusterName sql.NullString
	var branch sql.NullString
	var configJSON []byte
	if err := row.Scan(&name, &provider, &region, &size, &clusterName, &branch, &configJSON); err != nil {
		log.Printf("[autoscale] failed to get parent env: %v", err)
		return
	}

	// Container-level scaling on the same server (instant, no extra cost)
	if clusterName.Valid && clusterName.String != "" {
		created := s.scaleUpContainers(ctx, policy, provider, clusterName.String, name, count)
		if created < count {
			log.Printf("[autoscale] server %s at container capacity — scaled %d/%d (server scaling disabled in v0.1)",
				clusterName.String, created, count)
		}
	} else {
		log.Printf("[autoscale] env %s has no provider ref — cannot scale", policy.EnvironmentID)
	}

	// v0.1: No server-level scaling. Uncomment in v0.2:
	// remaining := count - created
	// for i := 0; i < remaining; i++ { ... s.envService.CreateEnvironment() ... }
	_ = region
	_ = size
	_ = branch
}

// scaleUpContainers adds additional Docker containers on an existing server.
// This is the fastest and cheapest way to scale — no new server needed.
// Returns the number of containers successfully added.
func (s *AutoscaleService) scaleUpContainers(ctx context.Context, policy *models.AutoscalePolicy, providerName, providerRef, baseName string, count int) int {
	p := s.envService.GetProvider(providerName)
	if p == nil {
		return 0
	}

	executor, ok := env.AsRemoteExecutor(p)
	if !ok {
		return 0
	}

	// Check server capacity (rough: count existing containers)
	output, err := executor.ExecCommand(ctx, providerRef,
		"docker ps --format '{{.Names}}' | grep -c '^gradient-' || echo 0", 10*time.Second)
	if err != nil {
		return 0
	}

	var existingContainers int
	fmt.Sscanf(strings.TrimSpace(output), "%d", &existingContainers)

	// Max containers per server depends on size (leave headroom for host)
	maxPerServer := 4 // default for small
	row := s.db.Pool.QueryRow(ctx,
		`SELECT size FROM environments WHERE id = $1`, policy.EnvironmentID)
	var envSize string
	if row.Scan(&envSize) == nil {
		switch envSize {
		case "medium":
			maxPerServer = 6
		case "large":
			maxPerServer = 10
		case "gpu":
			maxPerServer = 2
		}
	}

	available := maxPerServer - existingContainers
	if available <= 0 {
		log.Printf("[autoscale] server %s is at container capacity (%d/%d) — need new server",
			providerRef, existingContainers, maxPerServer)
		return 0
	}

	toCreate := count
	if toCreate > available {
		toCreate = available
	}

	created := 0
	for i := 0; i < toCreate; i++ {
		containerName := fmt.Sprintf("gradient-replica-%s-%d", policy.EnvironmentID[:8], policy.CurrentReplicas+i)

		// Start a new container on the same server
		startCmd := fmt.Sprintf(`docker run -d \
			--name %s \
			--security-opt seccomp=/etc/gradient/seccomp.json \
			--security-opt no-new-privileges \
			--cap-drop ALL \
			--cap-add CHOWN --cap-add DAC_OVERRIDE --cap-add FSETID --cap-add FOWNER \
			--cap-add SETGID --cap-add SETUID --cap-add NET_BIND_SERVICE --cap-add SYS_PTRACE \
			--cap-add KILL --cap-add AUDIT_WRITE --cap-add NET_RAW \
			--network gradient-net \
			--restart unless-stopped \
			-e GRADIENT_ENV_NAME=%s \
			-e GRADIENT_REPLICA=true \
			ubuntu:24.04 \
			tail -f /dev/null`,
			containerName, baseName+"-replica")

		_, err := executor.ExecCommand(ctx, providerRef, startCmd, 1*time.Minute)
		if err != nil {
			log.Printf("[autoscale] failed to create container replica %s: %v", containerName, err)
			continue
		}

		created++
		log.Printf("[autoscale] created container replica %s on server %s (fast scale: ~5s)",
			containerName, providerRef)

		// Record the container replica in the DB
		replicaID := fmt.Sprintf("%s-c%d", policy.EnvironmentID, policy.CurrentReplicas+i)
		s.db.Pool.Exec(ctx, `
			INSERT INTO environments (id, name, org_id, provider, region, size, cluster_name, status, config, created_at, updated_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7, 'running', $8, NOW(), NOW())
			ON CONFLICT (id) DO NOTHING`,
			replicaID, containerName, policy.OrgID, providerName, "same-server", "container",
			providerRef,
			fmt.Sprintf(`{"autoscale_parent":"%s","scale_type":"container","container_name":"%s"}`,
				policy.EnvironmentID, containerName))
	}

	return created
}

// scaleDown removes container replicas from the server.
// v0.1: Container-only. No server destruction from autoscaling.
func (s *AutoscaleService) scaleDown(ctx context.Context, policy *models.AutoscalePolicy, count int) {
	remaining := count

	// Remove container replicas (they're cheap to re-add)
	containerRows, err := s.db.Pool.Query(ctx, `
		SELECT id, cluster_name, config->>'container_name' FROM environments
		WHERE org_id = $1 AND status = 'running'
		AND config->>'autoscale_parent' = $2
		AND config->>'scale_type' = 'container'
		ORDER BY created_at DESC LIMIT $3`,
		policy.OrgID, policy.EnvironmentID, remaining)
	if err == nil {
		defer containerRows.Close()
		for containerRows.Next() && remaining > 0 {
			var replicaID, providerRef, containerName string
			if containerRows.Scan(&replicaID, &providerRef, &containerName) != nil {
				continue
			}

			// Get the parent env's provider for remote exec
			row := s.db.Pool.QueryRow(ctx,
				`SELECT provider FROM environments WHERE id = $1`, policy.EnvironmentID)
			var providerName string
			if row.Scan(&providerName) == nil {
				p := s.envService.GetProvider(providerName)
				if p != nil {
					if executor, ok := env.AsRemoteExecutor(p); ok {
						executor.ExecCommand(ctx, providerRef,
							fmt.Sprintf("docker rm -f %s 2>/dev/null || true", containerName),
							30*time.Second)
					}
				}
			}

			// Mark as destroyed in DB
			s.db.Pool.Exec(ctx,
				`UPDATE environments SET status = 'destroyed', destroyed_at = NOW() WHERE id = $1`, replicaID)
			remaining--
			log.Printf("[autoscale] removed container replica %s", containerName)
		}
	}

	// v0.1: No server-level scale-down from autoscaling.
	// Server replicas (if any exist from manual creation) are not touched.
	if remaining > 0 {
		log.Printf("[autoscale] %d replicas still need removal but only container scale-down is enabled in v0.1", remaining)
	}
}

// ScaleHistory returns recent scaling events for an environment.
func (s *AutoscaleService) ScaleHistory(ctx context.Context, orgID, envID string, limit int) ([]models.ScaleEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, environment_id, org_id, direction, from_replicas, to_replicas,
		       trigger_cpu, trigger_memory, created_at
		FROM autoscale_events
		WHERE environment_id = $1 AND org_id = $2
		ORDER BY created_at DESC LIMIT $3`, envID, orgID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []models.ScaleEvent
	for rows.Next() {
		var e models.ScaleEvent
		if err := rows.Scan(
			&e.ID, &e.EnvironmentID, &e.OrgID,
			&e.Direction, &e.FromReplicas, &e.ToReplicas,
			&e.TriggerCPU, &e.TriggerMemory, &e.CreatedAt,
		); err != nil {
			continue
		}
		events = append(events, e)
	}
	return events, nil
}

// recordScaleEvent writes a scaling event for audit/history.
func (s *AutoscaleService) recordScaleEvent(ctx context.Context, policy *models.AutoscalePolicy, direction string, from, to int, cpu, mem float64) {
	s.db.Pool.Exec(ctx, `
		INSERT INTO autoscale_events (id, environment_id, org_id, direction, from_replicas, to_replicas, trigger_cpu, trigger_memory, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		uuid.New().String(), policy.EnvironmentID, policy.OrgID,
		direction, from, to, cpu, mem, time.Now())
}

// extractFloat safely extracts a float64 from a map.
func extractFloat(m map[string]interface{}, key string) float64 {
	v, ok := m[key]
	if !ok {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}

// ProviderForEnv returns the cloud provider interface for an environment's provider type.
func (s *AutoscaleService) ProviderForEnv(provider string) env.Provider {
	return s.envService.GetProvider(provider)
}
