package services

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/models"
	gradctx "github.com/gradient/gradient/pkg/context"
)

type ContextService struct {
	store *gradctx.Store
}

func NewContextService(store *gradctx.Store) *ContextService {
	return &ContextService{store: store}
}

type SaveContextRequest struct {
	Branch            string
	OrgID             string
	RepoFullName      string
	CommitSHA         string
	InstalledPackages []models.InstalledPackage
	PreviousFailures  []models.TestFailure
	AttemptedFixes    []models.Fix
	Patterns          map[string]interface{}
	GlobalConfigs     map[string]string
	BaseOS            string
}

func (s *ContextService) SaveContext(ctx context.Context, req *SaveContextRequest) (*models.Context, error) {
	if req.Branch == "" {
		return nil, fmt.Errorf("branch is required")
	}
	if req.OrgID == "" {
		return nil, fmt.Errorf("org_id is required")
	}

	baseOS := req.BaseOS
	if baseOS == "" {
		baseOS = "ubuntu-24.04"
	}

	if req.InstalledPackages == nil {
		req.InstalledPackages = []models.InstalledPackage{}
	}
	if req.PreviousFailures == nil {
		req.PreviousFailures = []models.TestFailure{}
	}
	if req.AttemptedFixes == nil {
		req.AttemptedFixes = []models.Fix{}
	}
	if req.Patterns == nil {
		req.Patterns = map[string]interface{}{}
	}
	if req.GlobalConfigs == nil {
		req.GlobalConfigs = map[string]string{}
	}

	ctxModel := &models.Context{
		ID:                uuid.New().String(),
		Branch:            req.Branch,
		OrgID:             req.OrgID,
		RepoFullName:      req.RepoFullName,
		CommitSHA:         req.CommitSHA,
		InstalledPackages: req.InstalledPackages,
		PreviousFailures:  req.PreviousFailures,
		AttemptedFixes:    req.AttemptedFixes,
		Patterns:          req.Patterns,
		GlobalConfigs:     req.GlobalConfigs,
		BaseOS:            baseOS,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := s.store.Save(ctx, ctxModel); err != nil {
		return nil, fmt.Errorf("failed to save context: %w", err)
	}

	return ctxModel, nil
}

func (s *ContextService) GetContext(ctx context.Context, orgID, branch string) (*models.Context, error) {
	return s.store.GetByBranch(ctx, orgID, branch)
}

func (s *ContextService) GetRepoContext(ctx context.Context, orgID, repoFullName, branch string) (*models.Context, error) {
	if repoFullName == "" {
		return s.store.GetByBranch(ctx, orgID, branch)
	}
	return s.store.GetByRepoBranch(ctx, orgID, repoFullName, branch)
}

func (s *ContextService) ListContexts(ctx context.Context, orgID string) ([]*models.Context, error) {
	return s.store.ListByOrg(ctx, orgID)
}

func (s *ContextService) DeleteContext(ctx context.Context, orgID, branch string) error {
	return s.store.Delete(ctx, orgID, branch)
}
