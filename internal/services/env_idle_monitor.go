package services

import (
	"context"
	"log"
	"time"
)

type EnvIdleMonitor struct {
	envService *EnvService
	interval   time.Duration
	ttl        time.Duration
	stopCh     chan struct{}
}

func NewEnvIdleMonitor(envService *EnvService, interval, ttl time.Duration) *EnvIdleMonitor {
	return &EnvIdleMonitor{
		envService: envService,
		interval:   interval,
		ttl:        ttl,
		stopCh:     make(chan struct{}),
	}
}

func (m *EnvIdleMonitor) Start() {
	go m.run()
}

func (m *EnvIdleMonitor) Stop() {
	close(m.stopCh)
}

func (m *EnvIdleMonitor) run() {
	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkIdleEnvironments()
		}
	}
}

func (m *EnvIdleMonitor) checkIdleEnvironments() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cutoff := time.Now().Add(-m.ttl)

	envs, err := m.envService.ListAllRunning(ctx)
	if err != nil {
		log.Printf("[idle-monitor] Failed to list running environments: %v", err)
		return
	}

	for _, env := range envs {
		if env.RepoFullName == "" {
			continue
		}
		if env.UpdatedAt.After(cutoff) {
			continue
		}
		if m.hasActiveTask(ctx, env.ID) {
			continue
		}

		log.Printf("[idle-monitor] Sleeping idle environment %s (repo=%s, branch=%s, idle since %s)",
			env.ID, env.RepoFullName, env.ContextBranch, env.UpdatedAt.Format(time.RFC3339))

		if err := m.envService.SleepEnvironment(ctx, env.ID, env.OrgID); err != nil {
			log.Printf("[idle-monitor] Failed to sleep env %s: %v", env.ID, err)
		}
	}
}

func (m *EnvIdleMonitor) hasActiveTask(ctx context.Context, envID string) bool {
	var count int
	err := m.envService.EnvRepo.DB().Pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_tasks WHERE environment_id = $1 AND status IN ('running', 'queued')`, envID).Scan(&count)
	if err != nil {
		return true
	}
	return count > 0
}
