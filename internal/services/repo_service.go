package services

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

// RepoService handles GitHub App webhooks and auto-fork logic
type RepoService struct {
	db             *db.DB
	envService     *EnvService
	webhookSecret  string
	snapshotDB     *SnapshotStore
}

// SnapshotStore wraps DB operations for snapshots
type SnapshotStore struct {
	db *db.DB
}

func NewSnapshotStore(database *db.DB) *SnapshotStore {
	return &SnapshotStore{db: database}
}

func NewRepoService(database *db.DB, envService *EnvService, webhookSecret string) *RepoService {
	return &RepoService{
		db:            database,
		envService:    envService,
		webhookSecret: webhookSecret,
		snapshotDB:    NewSnapshotStore(database),
	}
}

// VerifyWebhookSignature verifies the GitHub webhook HMAC-SHA256 signature
func (s *RepoService) VerifyWebhookSignature(body []byte, signature string) bool {
	if s.webhookSecret == "" {
		log.Println("[repo] WARNING: GITHUB_APP_WEBHOOK_SECRET not set, skipping signature verification")
		return true
	}

	mac := hmac.New(sha256.New, []byte(s.webhookSecret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

// HandleWebhookEvent dispatches the GitHub webhook event to the appropriate handler
func (s *RepoService) HandleWebhookEvent(ctx context.Context, eventType string, payload json.RawMessage) error {
	switch eventType {
	case "installation":
		return s.handleInstallation(ctx, payload)
	case "create":
		return s.handleBranchCreate(ctx, payload)
	case "push":
		return s.handlePush(ctx, payload)
	case "delete":
		return s.handleBranchDelete(ctx, payload)
	default:
		log.Printf("[repo] Ignoring GitHub event: %s", eventType)
		return nil
	}
}

// --- GitHub webhook event handlers ---

type installationEvent struct {
	Action       string `json:"action"` // created, deleted, etc.
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			Login string `json:"login"`
		} `json:"account"`
	} `json:"installation"`
	Repositories []struct {
		FullName string `json:"full_name"`
	} `json:"repositories"`
}

func (s *RepoService) handleInstallation(ctx context.Context, payload json.RawMessage) error {
	var event installationEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse installation event: %w", err)
	}

	installationID := event.Installation.ID
	accountLogin := event.Installation.Account.Login

	repos := make([]string, 0, len(event.Repositories))
	for _, r := range event.Repositories {
		repos = append(repos, r.FullName)
	}
	reposJSON, _ := json.Marshal(repos)

	switch event.Action {
	case "created":
		log.Printf("[repo] GitHub App installed: installation=%d, account=%s, repos=%v", installationID, accountLogin, repos)

		query := `
			INSERT INTO github_installations (installation_id, account_login, repos, created_at, updated_at)
			VALUES ($1, $2, $3, NOW(), NOW())
			ON CONFLICT (installation_id) DO UPDATE SET
				account_login = EXCLUDED.account_login,
				repos = EXCLUDED.repos,
				updated_at = NOW()
		`
		_, err := s.db.Pool.Exec(ctx, query, installationID, accountLogin, reposJSON)
		if err != nil {
			return fmt.Errorf("failed to save installation: %w", err)
		}

	case "deleted":
		log.Printf("[repo] GitHub App uninstalled: installation=%d", installationID)

		_, err := s.db.Pool.Exec(ctx, `DELETE FROM github_installations WHERE installation_id = $1`, installationID)
		if err != nil {
			return fmt.Errorf("failed to delete installation: %w", err)
		}
		// Also remove all repo connections for this installation
		_, err = s.db.Pool.Exec(ctx, `DELETE FROM repo_connections WHERE installation_id = $1`, installationID)
		if err != nil {
			return fmt.Errorf("failed to delete repo connections: %w", err)
		}
	}

	return nil
}

type createEvent struct {
	Ref        string `json:"ref"`
	RefType    string `json:"ref_type"` // "branch" or "tag"
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (s *RepoService) handleBranchCreate(ctx context.Context, payload json.RawMessage) error {
	var event createEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse create event: %w", err)
	}

	if event.RefType != "branch" {
		return nil // Only handle branch creation
	}

	newBranch := event.Ref
	parentBranch := event.Repository.DefaultBranch
	repoFullName := event.Repository.FullName
	installationID := event.Installation.ID

	log.Printf("[repo] Branch created: %s (parent: %s) in %s", newBranch, parentBranch, repoFullName)

	// Find all repo connections for this repo
	rows, err := s.db.Pool.Query(ctx,
		`SELECT org_id, auto_fork_enabled FROM repo_connections WHERE repo_full_name = $1 AND installation_id = $2`,
		repoFullName, installationID,
	)
	if err != nil {
		return fmt.Errorf("failed to query repo connections: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var orgID string
		var autoForkEnabled bool
		if err := rows.Scan(&orgID, &autoForkEnabled); err != nil {
			log.Printf("[repo] Failed to scan repo connection: %v", err)
			continue
		}

		if !autoForkEnabled {
			continue
		}

		// Auto-fork: copy context from parent branch to new branch
		if err := s.autoForkContext(ctx, orgID, parentBranch, newBranch); err != nil {
			log.Printf("[repo] Failed to auto-fork context for org %s: %v", orgID, err)
		}

		// Auto-fork: copy latest snapshot pointer from parent branch
		if err := s.autoForkSnapshot(ctx, orgID, parentBranch, newBranch); err != nil {
			log.Printf("[repo] Failed to auto-fork snapshot for org %s: %v", orgID, err)
		}
	}

	return nil
}

// autoForkContext copies context from parent branch to new branch
func (s *RepoService) autoForkContext(ctx context.Context, orgID, parentBranch, newBranch string) error {
	// Read parent context
	var parentCtx struct {
		InstalledPackages json.RawMessage
		PreviousFailures  json.RawMessage
		AttemptedFixes    json.RawMessage
		Patterns          json.RawMessage
		GlobalConfigs     json.RawMessage
		BaseOS            string
		CommitSHA         *string
	}

	err := s.db.Pool.QueryRow(ctx,
		`SELECT installed_packages, previous_failures, attempted_fixes, patterns, global_configs, base_os, commit_sha
		 FROM contexts WHERE org_id = $1 AND branch = $2`,
		orgID, parentBranch,
	).Scan(
		&parentCtx.InstalledPackages, &parentCtx.PreviousFailures, &parentCtx.AttemptedFixes,
		&parentCtx.Patterns, &parentCtx.GlobalConfigs, &parentCtx.BaseOS, &parentCtx.CommitSHA,
	)
	if err == pgx.ErrNoRows {
		log.Printf("[repo] No context found for parent branch %s in org %s, skipping fork", parentBranch, orgID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read parent context: %w", err)
	}

	// Insert forked context for new branch
	newID := uuid.New().String()
	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO contexts (id, branch, org_id, commit_sha, installed_packages, previous_failures, attempted_fixes, patterns, global_configs, base_os, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, NOW(), NOW())
		 ON CONFLICT (org_id, branch) DO NOTHING`,
		newID, newBranch, orgID, parentCtx.CommitSHA,
		parentCtx.InstalledPackages, parentCtx.PreviousFailures, parentCtx.AttemptedFixes,
		parentCtx.Patterns, parentCtx.GlobalConfigs, parentCtx.BaseOS,
	)
	if err != nil {
		return fmt.Errorf("failed to insert forked context: %w", err)
	}

	log.Printf("[repo] Auto-forked context: %s → %s (org %s)", parentBranch, newBranch, orgID)
	return nil
}

// autoForkSnapshot creates a new snapshot record pointing to the parent's latest snapshot image
func (s *RepoService) autoForkSnapshot(ctx context.Context, orgID, parentBranch, newBranch string) error {
	// Get latest snapshot for parent branch
	var parentSnapshotID string
	var imageRef *string
	var commitSHA *string
	err := s.db.Pool.QueryRow(ctx,
		`SELECT id, image_ref, commit_sha FROM snapshots
		 WHERE org_id = $1 AND branch = $2
		 ORDER BY created_at DESC LIMIT 1`,
		orgID, parentBranch,
	).Scan(&parentSnapshotID, &imageRef, &commitSHA)
	if err == pgx.ErrNoRows {
		log.Printf("[repo] No snapshot found for parent branch %s in org %s, skipping fork", parentBranch, orgID)
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read parent snapshot: %w", err)
	}
	if imageRef == nil || *imageRef == "" {
		log.Printf("[repo] Parent snapshot %s has no image ref, skipping fork", parentSnapshotID)
		return nil
	}

	// Create a new snapshot record pointing to the same image (cheap fork — no image copy)
	newID := uuid.New().String()
	var commitSHAVal interface{} = nil
	if commitSHA != nil {
		commitSHAVal = *commitSHA
	}
	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO snapshots (id, org_id, branch, snapshot_type, image_ref, parent_snapshot_id, commit_sha, created_at)
		 VALUES ($1, $2, $3, 'auto_fork', $4, $5, $6, NOW())`,
		newID, orgID, newBranch, *imageRef, parentSnapshotID, commitSHAVal,
	)
	if err != nil {
		return fmt.Errorf("failed to insert forked snapshot: %w", err)
	}

	log.Printf("[repo] Auto-forked snapshot: %s → %s (image: %s, org: %s)", parentBranch, newBranch, *imageRef, orgID)
	return nil
}

type pushEvent struct {
	Ref        string `json:"ref"` // "refs/heads/main"
	After      string `json:"after"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (s *RepoService) handlePush(ctx context.Context, payload json.RawMessage) error {
	var event pushEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse push event: %w", err)
	}

	if !strings.HasPrefix(event.Ref, "refs/heads/") {
		return nil // Only handle branch pushes
	}

	branch := strings.TrimPrefix(event.Ref, "refs/heads/")
	commitSHA := event.After
	repoFullName := event.Repository.FullName
	installationID := event.Installation.ID

	shortSHA := commitSHA
	if len(shortSHA) > 8 {
		shortSHA = shortSHA[:8]
	}
	log.Printf("[repo] Push to %s@%s in %s", branch, shortSHA, repoFullName)

	// Find repo connections
	rows, err := s.db.Pool.Query(ctx,
		`SELECT org_id, auto_snapshot_on_push FROM repo_connections WHERE repo_full_name = $1 AND installation_id = $2`,
		repoFullName, installationID,
	)
	if err != nil {
		return fmt.Errorf("failed to query repo connections: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var orgID string
		var autoSnapshotOnPush bool
		if err := rows.Scan(&orgID, &autoSnapshotOnPush); err != nil {
			log.Printf("[repo] Failed to scan repo connection: %v", err)
			continue
		}

		// Update context commit SHA
		_, err := s.db.Pool.Exec(ctx,
			`UPDATE contexts SET commit_sha = $1, updated_at = NOW() WHERE org_id = $2 AND branch = $3`,
			commitSHA, orgID, branch,
		)
		if err != nil {
			log.Printf("[repo] Failed to update context commit SHA: %v", err)
		}

		// If auto-snapshot enabled and there's a running env for this branch, trigger snapshot
		if autoSnapshotOnPush {
			runningEnv, err := s.envService.EnvRepo.GetByOrgAndBranch(ctx, orgID, branch)
			if err != nil {
				log.Printf("[repo] Failed to check running env: %v", err)
				continue
			}
			if runningEnv != nil {
				go func(eID, oID, sha, br string) {
					shortSHA := sha
					if len(shortSHA) > 8 {
						shortSHA = shortSHA[:8]
					}
					tag := fmt.Sprintf("%s-%s-%d", br, shortSHA, time.Now().Unix())
					imageRef, err := s.envService.SnapshotEnvironment(context.Background(), eID, oID, tag)
					if err != nil {
						log.Printf("[repo] Auto-snapshot failed for env %s: %v", eID, err)
						return
					}

					// Save snapshot record
					if saveErr := s.snapshotDB.Save(context.Background(), &models.Snapshot{
						ID:            uuid.New().String(),
						OrgID:         oID,
						Branch:        br,
						EnvironmentID: eID,
						SnapshotType:  "on_push",
						ImageRef:      imageRef,
						CommitSHA:     sha,
						CreatedAt:     time.Now(),
					}); saveErr != nil {
						log.Printf("[repo] Failed to save snapshot record for env %s: %v", eID, saveErr)
					}

					log.Printf("[repo] Auto-snapshot on push: env=%s, image=%s", eID, imageRef)
				}(runningEnv.ID, orgID, commitSHA, branch)
			}
		}
	}

	return nil
}

type deleteEvent struct {
	Ref        string `json:"ref"`
	RefType    string `json:"ref_type"`
	Repository struct {
		FullName string `json:"full_name"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (s *RepoService) handleBranchDelete(ctx context.Context, payload json.RawMessage) error {
	var event deleteEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse delete event: %w", err)
	}

	if event.RefType != "branch" {
		return nil
	}

	branch := event.Ref
	repoFullName := event.Repository.FullName
	installationID := event.Installation.ID

	log.Printf("[repo] Branch deleted: %s in %s", branch, repoFullName)

	// Find repo connections and clean up contexts for deleted branch
	rows, err := s.db.Pool.Query(ctx,
		`SELECT org_id FROM repo_connections WHERE repo_full_name = $1 AND installation_id = $2`,
		repoFullName, installationID,
	)
	if err != nil {
		return fmt.Errorf("failed to query repo connections: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var orgID string
		if err := rows.Scan(&orgID); err != nil {
			continue
		}

		// Soft-delete: just log it. Keeping context/snapshots for history.
		log.Printf("[repo] Branch %s deleted in org %s. Context and snapshots preserved for history.", branch, orgID)
	}

	return nil
}

// --- Repo connection management ---

// ConnectRepo links a GitHub repo to a Gradient org
func (s *RepoService) ConnectRepo(ctx context.Context, orgID, repoFullName string) (*models.RepoConnection, error) {
	// Look up the GitHub installation that has this repo
	var installationID int64
	var reposJSON []byte

	rows, err := s.db.Pool.Query(ctx, `SELECT installation_id, repos FROM github_installations`)
	if err != nil {
		return nil, fmt.Errorf("failed to query installations: %w", err)
	}
	defer rows.Close()

	found := false
	for rows.Next() {
		if err := rows.Scan(&installationID, &reposJSON); err != nil {
			continue
		}
		var repos []string
		if err := json.Unmarshal(reposJSON, &repos); err != nil {
			continue
		}
		for _, r := range repos {
			if r == repoFullName {
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("repo %s not found in any GitHub App installation — install the Gradient GitHub App first: https://github.com/apps/gradient", repoFullName)
	}

	connID := uuid.New().String()
	conn := &models.RepoConnection{
		ID:                 connID,
		OrgID:              orgID,
		InstallationID:     installationID,
		RepoFullName:       repoFullName,
		DefaultBranch:      "main",
		AutoForkEnabled:    true,
		AutoSnapshotOnPush: true,
		CreatedAt:          time.Now(),
	}

	query := `
		INSERT INTO repo_connections (id, org_id, installation_id, repo_full_name, default_branch, auto_fork_enabled, auto_snapshot_on_push, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (org_id, repo_full_name) DO UPDATE SET
			installation_id = EXCLUDED.installation_id,
			auto_fork_enabled = EXCLUDED.auto_fork_enabled,
			auto_snapshot_on_push = EXCLUDED.auto_snapshot_on_push
		RETURNING id
	`
	err = s.db.Pool.QueryRow(ctx, query,
		conn.ID, conn.OrgID, conn.InstallationID, conn.RepoFullName,
		conn.DefaultBranch, conn.AutoForkEnabled, conn.AutoSnapshotOnPush, conn.CreatedAt,
	).Scan(&conn.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to save repo connection: %w", err)
	}

	log.Printf("[repo] Connected repo %s to org %s (installation %d)", repoFullName, orgID, installationID)
	return conn, nil
}

// ListRepos returns all repo connections for an org
func (s *RepoService) ListRepos(ctx context.Context, orgID string) ([]*models.RepoConnection, error) {
	query := `
		SELECT id, org_id, installation_id, repo_full_name, default_branch, auto_fork_enabled, auto_snapshot_on_push, created_at
		FROM repo_connections
		WHERE org_id = $1
		ORDER BY created_at DESC
	`
	rows, err := s.db.Pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to query repo connections: %w", err)
	}
	defer rows.Close()

	var conns []*models.RepoConnection
	for rows.Next() {
		var c models.RepoConnection
		if err := rows.Scan(&c.ID, &c.OrgID, &c.InstallationID, &c.RepoFullName, &c.DefaultBranch, &c.AutoForkEnabled, &c.AutoSnapshotOnPush, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan repo connection: %w", err)
		}
		conns = append(conns, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating repo connections: %w", err)
	}
	return conns, nil
}

// ListAvailableRepos returns all repos from GitHub App installations that aren't yet connected to this org
func (s *RepoService) ListAvailableRepos(ctx context.Context, orgID string) ([]string, error) {
	// Get all repos from GitHub installations
	rows, err := s.db.Pool.Query(ctx, `SELECT repos FROM github_installations`)
	if err != nil {
		return nil, fmt.Errorf("failed to query installations: %w", err)
	}
	defer rows.Close()

	allRepos := make(map[string]bool)
	for rows.Next() {
		var reposJSON []byte
		if err := rows.Scan(&reposJSON); err != nil {
			continue
		}
		var repos []string
		if err := json.Unmarshal(reposJSON, &repos); err != nil {
			continue
		}
		for _, r := range repos {
			allRepos[r] = true
		}
	}

	// Get repos already connected to this org
	connectedRows, err := s.db.Pool.Query(ctx,
		`SELECT repo_full_name FROM repo_connections WHERE org_id = $1`,
		orgID,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to query connected repos: %w", err)
	}
	defer connectedRows.Close()

	connectedRepos := make(map[string]bool)
	for connectedRows.Next() {
		var repoFullName string
		if err := connectedRows.Scan(&repoFullName); err != nil {
			continue
		}
		connectedRepos[repoFullName] = true
	}

	// Return repos that are available but not yet connected
	available := make([]string, 0)
	for repo := range allRepos {
		if !connectedRepos[repo] {
			available = append(available, repo)
		}
	}

	// Sort for consistent ordering
	sort.Strings(available)
	return available, nil
}

// DisconnectRepo removes a repo connection
func (s *RepoService) DisconnectRepo(ctx context.Context, orgID, connID string) error {
	cmdTag, err := s.db.Pool.Exec(ctx,
		`DELETE FROM repo_connections WHERE id = $1 AND org_id = $2`,
		connID, orgID,
	)
	if err != nil {
		return fmt.Errorf("failed to delete repo connection: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return fmt.Errorf("repo connection not found")
	}
	return nil
}

// --- Snapshot store ---

func (ss *SnapshotStore) Save(ctx context.Context, snapshot *models.Snapshot) error {
	query := `
		INSERT INTO snapshots (id, org_id, branch, environment_id, snapshot_type, image_ref, size_bytes, parent_snapshot_id, commit_sha, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
	`
	_, err := ss.db.Pool.Exec(ctx, query,
		snapshot.ID, snapshot.OrgID, snapshot.Branch, snapshot.EnvironmentID,
		snapshot.SnapshotType, snapshot.ImageRef, snapshot.SizeBytes,
		snapshot.ParentSnapshotID, snapshot.CommitSHA, snapshot.CreatedAt,
	)
	return err
}

func (ss *SnapshotStore) ListByOrgAndBranch(ctx context.Context, orgID, branch string) ([]*models.Snapshot, error) {
	query := `
		SELECT id, org_id, branch,
			COALESCE(environment_id, ''), snapshot_type, COALESCE(image_ref, ''), size_bytes,
			COALESCE(parent_snapshot_id, ''), COALESCE(commit_sha, ''), created_at
		FROM snapshots
		WHERE org_id = $1 AND branch = $2
		ORDER BY created_at DESC
	`
	rows, err := ss.db.Pool.Query(ctx, query, orgID, branch)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var snapshots []*models.Snapshot
	for rows.Next() {
		var snap models.Snapshot
		if err := rows.Scan(
			&snap.ID, &snap.OrgID, &snap.Branch, &snap.EnvironmentID,
			&snap.SnapshotType, &snap.ImageRef, &snap.SizeBytes,
			&snap.ParentSnapshotID, &snap.CommitSHA, &snap.CreatedAt,
		); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, &snap)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating snapshots: %w", err)
	}
	return snapshots, nil
}

func (ss *SnapshotStore) GetLatestByBranch(ctx context.Context, orgID, branch string) (*models.Snapshot, error) {
	var snap models.Snapshot
	err := ss.db.Pool.QueryRow(ctx,
		`SELECT id, org_id, branch,
			COALESCE(environment_id, ''), snapshot_type, COALESCE(image_ref, ''), size_bytes,
			COALESCE(parent_snapshot_id, ''), COALESCE(commit_sha, ''), created_at
		 FROM snapshots WHERE org_id = $1 AND branch = $2 ORDER BY created_at DESC LIMIT 1`,
		orgID, branch,
	).Scan(
		&snap.ID, &snap.OrgID, &snap.Branch, &snap.EnvironmentID,
		&snap.SnapshotType, &snap.ImageRef, &snap.SizeBytes,
		&snap.ParentSnapshotID, &snap.CommitSHA, &snap.CreatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &snap, nil
}
