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
	db            *db.DB
	envService    *EnvService
	webhookSecret string
	snapshotDB    *SnapshotStore
	github        *GitHubService
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

// SetGitHubService wires in the GitHub OAuth service (called after both are constructed).
func (s *RepoService) SetGitHubService(gh *GitHubService) {
	s.github = gh
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
	case "pull_request":
		return s.handlePullRequest(ctx, payload)
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
		`SELECT org_id, auto_fork_enabled FROM repo_connections WHERE repo_full_name = $1 AND (installation_id = $2 OR installation_id = 0)`,
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
		if err := s.autoForkContext(ctx, orgID, parentBranch, newBranch, repoFullName); err != nil {
			log.Printf("[repo] Failed to auto-fork context for org %s: %v", orgID, err)
		}

		// Auto-fork: copy latest snapshot pointer from parent branch
		if err := s.autoForkSnapshot(ctx, orgID, parentBranch, newBranch); err != nil {
			log.Printf("[repo] Failed to auto-fork snapshot for org %s: %v", orgID, err)
		}

		// Auto-fork: create environment for the new branch from parent's snapshot
		if err := s.autoForkEnvironment(ctx, orgID, repoFullName, parentBranch, newBranch); err != nil {
			log.Printf("[repo] Failed to auto-fork environment for org %s: %v", orgID, err)
		}
	}

	return nil
}

// autoForkContext copies context from parent branch to new branch
func (s *RepoService) autoForkContext(ctx context.Context, orgID, parentBranch, newBranch string, repoFullName ...string) error {
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

	repo := ""
	if len(repoFullName) > 0 {
		repo = repoFullName[0]
	}
	newID := uuid.New().String()
	_, err = s.db.Pool.Exec(ctx,
		`INSERT INTO contexts (id, branch, org_id, repo_full_name, commit_sha, installed_packages, previous_failures, attempted_fixes, patterns, global_configs, base_os, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, NOW(), NOW())
		 ON CONFLICT (org_id, repo_full_name, branch) DO NOTHING`,
		newID, newBranch, orgID, repo, parentCtx.CommitSHA,
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

func (s *RepoService) autoForkEnvironment(ctx context.Context, orgID, repoFullName, parentBranch, newBranch string) error {
	parentEnv, err := s.envService.EnvRepo.GetByOrgRepoAndBranch(ctx, orgID, repoFullName, parentBranch)
	if err != nil || parentEnv == nil {
		log.Printf("[repo] No environment found for parent branch %s in repo %s, skipping env fork", parentBranch, repoFullName)
		return nil
	}

	var snapshotRef string
	snap, _ := s.snapshotDB.GetLatestByBranch(ctx, orgID, parentBranch)
	if snap != nil {
		snapshotRef = snap.ImageRef
	}

	newEnv, err := s.envService.CreateEnvironment(ctx, &CreateEnvRequest{
		Name:          fmt.Sprintf("fork-%s-%s", newBranch, uuid.New().String()[:8]),
		OrgID:         orgID,
		Provider:      parentEnv.Provider,
		Region:        parentEnv.Region,
		Size:          parentEnv.Size,
		ContextBranch: newBranch,
		SnapshotRef:   snapshotRef,
		RepoFullName:  repoFullName,
	})
	if err != nil {
		return fmt.Errorf("failed to create forked environment: %w", err)
	}

	log.Printf("[repo] Auto-forked environment: %s → %s (env %s, repo %s)", parentBranch, newBranch, newEnv.ID, repoFullName)
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

type pullRequestEvent struct {
	Action      string `json:"action"`
	PullRequest struct {
		Merged bool   `json:"merged"`
		Head   struct {
			Ref string `json:"ref"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
		} `json:"base"`
	} `json:"pull_request"`
	Repository struct {
		FullName      string `json:"full_name"`
		DefaultBranch string `json:"default_branch"`
	} `json:"repository"`
	Installation struct {
		ID int64 `json:"id"`
	} `json:"installation"`
}

func (s *RepoService) handlePullRequest(ctx context.Context, payload json.RawMessage) error {
	var event pullRequestEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return fmt.Errorf("failed to parse pull_request event: %w", err)
	}

	if event.Action != "closed" || !event.PullRequest.Merged {
		return nil
	}

	headBranch := event.PullRequest.Head.Ref
	baseBranch := event.PullRequest.Base.Ref
	repoFullName := event.Repository.FullName
	installationID := event.Installation.ID

	log.Printf("[repo] PR merged: %s → %s in %s", headBranch, baseBranch, repoFullName)

	rows, err := s.db.Pool.Query(ctx,
		`SELECT org_id FROM repo_connections WHERE repo_full_name = $1 AND (installation_id = $2 OR installation_id = 0)`,
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

		log.Printf("[repo] Processing PR merge cleanup for org %s: %s → %s", orgID, headBranch, baseBranch)

		if err := s.collapseContextOnMerge(ctx, orgID, repoFullName, headBranch, baseBranch); err != nil {
			log.Printf("[repo] Context collapse failed for %s → %s: %v", headBranch, baseBranch, err)
		}

		if err := s.cleanupBranchResources(ctx, orgID, repoFullName, headBranch); err != nil {
			log.Printf("[repo] Branch cleanup failed for %s: %v", headBranch, err)
		}
	}

	return nil
}

func (s *RepoService) collapseContextOnMerge(ctx context.Context, orgID, repoFullName, headBranch, baseBranch string) error {
	var headCtx struct {
		InstalledPackages json.RawMessage
		PreviousFailures  json.RawMessage
		AttemptedFixes    json.RawMessage
		Patterns          json.RawMessage
		GlobalConfigs     json.RawMessage
	}
	err := s.db.Pool.QueryRow(ctx,
		`SELECT installed_packages, previous_failures, attempted_fixes, patterns, global_configs
		 FROM contexts WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`,
		orgID, repoFullName, headBranch,
	).Scan(&headCtx.InstalledPackages, &headCtx.PreviousFailures, &headCtx.AttemptedFixes,
		&headCtx.Patterns, &headCtx.GlobalConfigs)
	if err != nil {
		log.Printf("[collapse] No context found for merged branch %s, skipping collapse", headBranch)
		return nil
	}

	var basePackages, baseFailures, baseFixes []json.RawMessage
	var basePatterns, baseConfigs map[string]interface{}

	var basePkgJSON, baseFailJSON, baseFixJSON, basePatJSON, baseCfgJSON json.RawMessage
	baseErr := s.db.Pool.QueryRow(ctx,
		`SELECT installed_packages, previous_failures, attempted_fixes, patterns, global_configs
		 FROM contexts WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`,
		orgID, repoFullName, baseBranch,
	).Scan(&basePkgJSON, &baseFailJSON, &baseFixJSON, &basePatJSON, &baseCfgJSON)

	if baseErr != nil {
		log.Printf("[collapse] No base context for %s, creating from head", baseBranch)
		_, err = s.db.Pool.Exec(ctx,
			`INSERT INTO contexts (id, branch, org_id, repo_full_name, installed_packages, previous_failures, attempted_fixes, patterns, global_configs, base_os, created_at, updated_at)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 'ubuntu-24.04', NOW(), NOW())
			 ON CONFLICT (org_id, repo_full_name, branch) DO UPDATE SET
			 	installed_packages = EXCLUDED.installed_packages,
			 	previous_failures = EXCLUDED.previous_failures,
			 	attempted_fixes = EXCLUDED.attempted_fixes,
			 	patterns = EXCLUDED.patterns,
			 	global_configs = EXCLUDED.global_configs,
			 	updated_at = NOW()`,
			uuid.New().String(), baseBranch, orgID, repoFullName,
			headCtx.InstalledPackages, headCtx.PreviousFailures, headCtx.AttemptedFixes,
			headCtx.Patterns, headCtx.GlobalConfigs,
		)
		return err
	}

	json.Unmarshal(basePkgJSON, &basePackages)
	json.Unmarshal(baseFailJSON, &baseFailures)
	json.Unmarshal(baseFixJSON, &baseFixes)
	json.Unmarshal(basePatJSON, &basePatterns)
	json.Unmarshal(baseCfgJSON, &baseConfigs)

	var headPackages []json.RawMessage
	var headPatterns, headConfigs map[string]interface{}
	json.Unmarshal(headCtx.InstalledPackages, &headPackages)
	json.Unmarshal(headCtx.Patterns, &headPatterns)
	json.Unmarshal(headCtx.GlobalConfigs, &headConfigs)

	mergedPackages := mergeJSONArrays(basePkgJSON, headCtx.InstalledPackages)
	mergedFailures := mergeJSONArrays(baseFailJSON, headCtx.PreviousFailures)
	mergedFixes := mergeJSONArrays(baseFixJSON, headCtx.AttemptedFixes)
	mergedPatterns := mergeJSONMaps(basePatJSON, headCtx.Patterns)
	mergedConfigs := mergeJSONMaps(baseCfgJSON, headCtx.GlobalConfigs)

	_, err = s.db.Pool.Exec(ctx,
		`UPDATE contexts SET
			installed_packages = $1,
			previous_failures = $2,
			attempted_fixes = $3,
			patterns = $4,
			global_configs = $5,
			updated_at = NOW()
		 WHERE org_id = $6 AND repo_full_name = $7 AND branch = $8`,
		mergedPackages, mergedFailures, mergedFixes, mergedPatterns, mergedConfigs,
		orgID, repoFullName, baseBranch,
	)

	log.Printf("[collapse] Context merged from %s into %s for repo %s", headBranch, baseBranch, repoFullName)
	return err
}

func mergeJSONArrays(base, head json.RawMessage) json.RawMessage {
	var baseArr, headArr []json.RawMessage
	json.Unmarshal(base, &baseArr)
	json.Unmarshal(head, &headArr)

	seen := make(map[string]bool)
	var merged []json.RawMessage
	for _, item := range baseArr {
		key := string(item)
		if !seen[key] {
			seen[key] = true
			merged = append(merged, item)
		}
	}
	for _, item := range headArr {
		key := string(item)
		if !seen[key] {
			seen[key] = true
			merged = append(merged, item)
		}
	}

	result, _ := json.Marshal(merged)
	if result == nil {
		return json.RawMessage("[]")
	}
	return result
}

func mergeJSONMaps(base, head json.RawMessage) json.RawMessage {
	var baseMap, headMap map[string]interface{}
	json.Unmarshal(base, &baseMap)
	json.Unmarshal(head, &headMap)

	if baseMap == nil {
		baseMap = make(map[string]interface{})
	}
	for k, v := range headMap {
		baseMap[k] = v
	}

	result, _ := json.Marshal(baseMap)
	if result == nil {
		return json.RawMessage("{}")
	}
	return result
}

func (s *RepoService) cleanupBranchResources(ctx context.Context, orgID, repoFullName, branch string) error {
	branchEnv, _ := s.envService.EnvRepo.GetByOrgRepoAndBranch(ctx, orgID, repoFullName, branch)
	if branchEnv != nil {
		if err := s.envService.DestroyEnvironment(ctx, branchEnv.ID, orgID); err != nil {
			log.Printf("[cleanup] Failed to destroy environment %s for branch %s: %v", branchEnv.ID, branch, err)
		} else {
			log.Printf("[cleanup] Destroyed environment %s for merged branch %s", branchEnv.ID, branch)
		}
	}

	_, err := s.db.Pool.Exec(ctx,
		`DELETE FROM contexts WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`,
		orgID, repoFullName, branch)
	if err != nil {
		log.Printf("[cleanup] Failed to delete context for branch %s: %v", branch, err)
	}

	_, err = s.db.Pool.Exec(ctx,
		`DELETE FROM context_objects WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`,
		orgID, repoFullName, branch)
	if err != nil {
		log.Printf("[cleanup] Failed to delete context objects for branch %s: %v", branch, err)
	}

	_, err = s.db.Pool.Exec(ctx,
		`DELETE FROM context_events WHERE org_id = $1 AND repo_full_name = $2 AND branch = $3`,
		orgID, repoFullName, branch)
	if err != nil {
		log.Printf("[cleanup] Failed to delete context events for branch %s: %v", branch, err)
	}

	log.Printf("[cleanup] Cleaned up resources for merged branch %s in repo %s", branch, repoFullName)
	return nil
}

// --- Repo connection management ---

// ConnectRepo links a GitHub repo to a Gradient org.
// Uses the stored GitHub OAuth token to create a webhook on the repo.
func (s *RepoService) ConnectRepo(ctx context.Context, orgID, repoFullName string) (*models.RepoConnection, error) {
	if s.github == nil {
		return nil, fmt.Errorf("GitHub OAuth not configured")
	}

	ghConn, err := s.github.GetConnection(ctx, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to check GitHub connection: %w", err)
	}
	if ghConn == nil {
		return nil, fmt.Errorf("GitHub not connected — run `gc repo auth` or connect GitHub in the dashboard first")
	}

	// Create a webhook on the repo so we receive push/create/delete events
	var webhookID int64
	webhookID, err = s.github.CreateWebhook(ctx, orgID, repoFullName, s.webhookSecret)
	if err != nil {
		log.Printf("[repo] Warning: could not create webhook on %s: %v (auto-fork may not work until webhook is set up)", repoFullName, err)
	}

	connID := uuid.New().String()
	conn := &models.RepoConnection{
		ID:                 connID,
		OrgID:              orgID,
		RepoFullName:       repoFullName,
		DefaultBranch:      "main",
		AutoForkEnabled:    true,
		AutoSnapshotOnPush: true,
		WebhookID:          webhookID,
		CreatedAt:          time.Now(),
	}

	query := `
		INSERT INTO repo_connections (id, org_id, installation_id, repo_full_name, default_branch, auto_fork_enabled, auto_snapshot_on_push, webhook_id, created_at)
		VALUES ($1, $2, 0, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (org_id, repo_full_name) DO UPDATE SET
			auto_fork_enabled = EXCLUDED.auto_fork_enabled,
			auto_snapshot_on_push = EXCLUDED.auto_snapshot_on_push,
			webhook_id = EXCLUDED.webhook_id
		RETURNING id
	`
	err = s.db.Pool.QueryRow(ctx, query,
		conn.ID, conn.OrgID, conn.RepoFullName,
		conn.DefaultBranch, conn.AutoForkEnabled, conn.AutoSnapshotOnPush, conn.WebhookID, conn.CreatedAt,
	).Scan(&conn.ID)
	if err != nil {
		return nil, fmt.Errorf("failed to save repo connection: %w", err)
	}

	log.Printf("[repo] Connected repo %s to org %s (webhook %d)", repoFullName, orgID, webhookID)
	return conn, nil
}

// ListRepos returns all repo connections for an org
func (s *RepoService) ListRepos(ctx context.Context, orgID string) ([]*models.RepoConnection, error) {
	query := `
		SELECT id, org_id, installation_id, repo_full_name, default_branch,
			auto_fork_enabled, auto_snapshot_on_push, COALESCE(webhook_id, 0), created_at
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
		if err := rows.Scan(&c.ID, &c.OrgID, &c.InstallationID, &c.RepoFullName, &c.DefaultBranch,
			&c.AutoForkEnabled, &c.AutoSnapshotOnPush, &c.WebhookID, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan repo connection: %w", err)
		}
		conns = append(conns, &c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating repo connections: %w", err)
	}
	return conns, nil
}

// ListAvailableRepos returns repos the user has access to via GitHub OAuth that aren't yet connected.
func (s *RepoService) ListAvailableRepos(ctx context.Context, orgID string) ([]string, error) {
	if s.github == nil {
		return nil, nil
	}

	// Fetch all repos from GitHub API using the user's OAuth token
	allRepos, err := s.github.ListUserRepos(ctx, orgID)
	if err != nil {
		return nil, err
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
	for _, repo := range allRepos {
		if !connectedRepos[repo] {
			available = append(available, repo)
		}
	}

	sort.Strings(available)
	return available, nil
}

// DisconnectRepo removes a repo connection and cleans up the webhook.
func (s *RepoService) DisconnectRepo(ctx context.Context, orgID, connID string) error {
	// Fetch the connection to get webhook ID and repo name for cleanup
	var repoFullName string
	var webhookID *int64
	err := s.db.Pool.QueryRow(ctx,
		`SELECT repo_full_name, webhook_id FROM repo_connections WHERE id = $1 AND org_id = $2`,
		connID, orgID,
	).Scan(&repoFullName, &webhookID)
	if err == pgx.ErrNoRows {
		return fmt.Errorf("repo connection not found")
	}
	if err != nil {
		return fmt.Errorf("failed to look up repo connection: %w", err)
	}

	// Delete the webhook from GitHub if we have a webhook ID
	if s.github != nil && webhookID != nil && *webhookID != 0 {
		if delErr := s.github.DeleteWebhook(ctx, orgID, repoFullName, *webhookID); delErr != nil {
			log.Printf("[repo] Warning: failed to delete webhook %d on %s: %v", *webhookID, repoFullName, delErr)
		}
	}

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

func (ss *SnapshotStore) ListByOrg(ctx context.Context, orgID string) ([]*models.Snapshot, error) {
	query := `
		SELECT id, org_id, branch,
			COALESCE(environment_id, ''), snapshot_type, COALESCE(image_ref, ''), size_bytes,
			COALESCE(parent_snapshot_id, ''), COALESCE(commit_sha, ''), created_at
		FROM snapshots
		WHERE org_id = $1
		ORDER BY created_at DESC
		LIMIT 100
	`
	rows, err := ss.db.Pool.Query(ctx, query, orgID)
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
	return snapshots, rows.Err()
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
