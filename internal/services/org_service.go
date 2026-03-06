package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OrgService wraps Clerk Backend API for organization management.
// Uses direct HTTP calls to avoid the heavy Clerk Go SDK dependency.
type OrgService struct {
	clerkSecretKey string
	baseURL        string
	client         *http.Client
}

func NewOrgService(clerkSecretKey string) *OrgService {
	return &OrgService{
		clerkSecretKey: clerkSecretKey,
		baseURL:        "https://api.clerk.com/v1",
		client:         &http.Client{Timeout: 15 * time.Second},
	}
}

// IsEnabled returns true if the Clerk secret key is configured.
func (s *OrgService) IsEnabled() bool {
	return s.clerkSecretKey != ""
}

// --- Types ---

// OrgMember represents a member of a Clerk organization.
type OrgMember struct {
	ID        string `json:"id"`
	UserID    string `json:"user_id"`
	OrgID     string `json:"org_id"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
	// Populated from user lookup
	Email     string `json:"email,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	LastName  string `json:"last_name,omitempty"`
}

// OrgInvitation represents a pending invitation to a Clerk organization.
type OrgInvitation struct {
	ID             string `json:"id"`
	EmailAddress   string `json:"email_address"`
	OrgID          string `json:"organization_id"`
	Role           string `json:"role"`
	Status         string `json:"status"`
	CreatedAt      int64  `json:"created_at"`
}

// Organization represents a Clerk organization.
type Organization struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Slug      string `json:"slug"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
}

// --- API Methods ---

// CreateOrganization creates a new Clerk organization and adds the creator as admin.
// If the slug already exists, it retries with a numeric suffix (e.g. "acme-2").
func (s *OrgService) CreateOrganization(ctx context.Context, name, slug, createdByUserID string) (*Organization, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	// Try up to 3 times with slug variations
	maxAttempts := 3
	currentSlug := slug
	for attempt := 0; attempt < maxAttempts; attempt++ {
		body := map[string]string{
			"name":       name,
			"created_by": createdByUserID,
		}
		if currentSlug != "" {
			body["slug"] = currentSlug
		}
		bodyJSON, _ := json.Marshal(body)

		resp, err := s.clerkRequest(ctx, "POST", "/organizations", strings.NewReader(string(bodyJSON)))
		if err != nil {
			// Check if it's a slug conflict
			errMsg := err.Error()
			if strings.Contains(errMsg, "already been taken") || strings.Contains(errMsg, "form_identifier_exists") || strings.Contains(errMsg, "unique") {
				if currentSlug == "" {
					// Clerk auto-generated a slug that conflicted — add a suffix to the name
					currentSlug = fmt.Sprintf("%s-%d", strings.ToLower(strings.ReplaceAll(name, " ", "-")), attempt+2)
				} else {
					currentSlug = fmt.Sprintf("%s-%d", slug, attempt+2)
				}
				continue
			}
			return nil, fmt.Errorf("failed to create organization: %w", err)
		}

		var org Organization
		if err := json.Unmarshal(resp, &org); err != nil {
			return nil, fmt.Errorf("failed to parse organization response: %w", err)
		}

		return &org, nil
	}

	return nil, fmt.Errorf("slug '%s' is already taken — try a different name or use --slug", slug)
}

// ListOrganizations lists organizations the current user belongs to.
// Note: Clerk Backend API lists ALL orgs (admin endpoint). For user-specific orgs,
// we filter by membership.
func (s *OrgService) ListOrganizations(ctx context.Context, userID string) ([]Organization, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	// Get all org memberships for this user
	resp, err := s.clerkRequest(ctx, "GET",
		fmt.Sprintf("/users/%s/organization_memberships?limit=50", userID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list user's org memberships: %w", err)
	}

	var membershipResult struct {
		Data []struct {
			Organization Organization `json:"organization"`
			Role         string       `json:"role"`
		} `json:"data"`
		TotalCount int `json:"total_count"`
	}
	if err := json.Unmarshal(resp, &membershipResult); err != nil {
		return nil, fmt.Errorf("failed to parse memberships response: %w", err)
	}

	var orgs []Organization
	for _, m := range membershipResult.Data {
		orgs = append(orgs, m.Organization)
	}

	return orgs, nil
}

// ListMembers returns all members of an organization.
func (s *OrgService) ListMembers(ctx context.Context, orgID string) ([]OrgMember, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	resp, err := s.clerkRequest(ctx, "GET",
		fmt.Sprintf("/organizations/%s/memberships?limit=50", orgID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list org members: %w", err)
	}

	var result struct {
		Data []struct {
			ID        string `json:"id"`
			Role      string `json:"role"`
			CreatedAt int64  `json:"created_at"`
			UpdatedAt int64  `json:"updated_at"`
			PublicUserData struct {
				UserID    string `json:"user_id"`
				FirstName string `json:"first_name"`
				LastName  string `json:"last_name"`
				Identifier string `json:"identifier"` // email
			} `json:"public_user_data"`
		} `json:"data"`
		TotalCount int `json:"total_count"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse members response: %w", err)
	}

	var members []OrgMember
	for _, m := range result.Data {
		members = append(members, OrgMember{
			ID:        m.ID,
			UserID:    m.PublicUserData.UserID,
			OrgID:     orgID,
			Role:      m.Role,
			Email:     m.PublicUserData.Identifier,
			FirstName: m.PublicUserData.FirstName,
			LastName:  m.PublicUserData.LastName,
			CreatedAt: fmt.Sprintf("%d", m.CreatedAt),
			UpdatedAt: fmt.Sprintf("%d", m.UpdatedAt),
		})
	}

	return members, nil
}

// InviteMember invites a user to the organization by email.
func (s *OrgService) InviteMember(ctx context.Context, orgID, email, role string) (*OrgInvitation, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	if role == "" {
		role = "org:member"
	}

	body := map[string]string{
		"email_address": email,
		"role":          role,
	}
	bodyJSON, _ := json.Marshal(body)

	resp, err := s.clerkRequest(ctx, "POST",
		fmt.Sprintf("/organizations/%s/invitations", orgID),
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		return nil, fmt.Errorf("failed to invite member: %w", err)
	}

	var invitation OrgInvitation
	if err := json.Unmarshal(resp, &invitation); err != nil {
		return nil, fmt.Errorf("failed to parse invitation response: %w", err)
	}

	return &invitation, nil
}

// RemoveMember removes a user from the organization.
func (s *OrgService) RemoveMember(ctx context.Context, orgID, userID string) error {
	if !s.IsEnabled() {
		return fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	_, err := s.clerkRequest(ctx, "DELETE",
		fmt.Sprintf("/organizations/%s/memberships/%s", orgID, userID), nil)
	if err != nil {
		return fmt.Errorf("failed to remove member: %w", err)
	}

	return nil
}

// UpdateMemberRole changes a member's role in the organization.
func (s *OrgService) UpdateMemberRole(ctx context.Context, orgID, userID, newRole string) error {
	if !s.IsEnabled() {
		return fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	body := map[string]string{
		"role": newRole,
	}
	bodyJSON, _ := json.Marshal(body)

	_, err := s.clerkRequest(ctx, "PATCH",
		fmt.Sprintf("/organizations/%s/memberships/%s", orgID, userID),
		strings.NewReader(string(bodyJSON)))
	if err != nil {
		return fmt.Errorf("failed to update member role: %w", err)
	}

	return nil
}

// ListInvitations lists pending invitations for an organization.
func (s *OrgService) ListInvitations(ctx context.Context, orgID string) ([]OrgInvitation, error) {
	if !s.IsEnabled() {
		return nil, fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	resp, err := s.clerkRequest(ctx, "GET",
		fmt.Sprintf("/organizations/%s/invitations?status=pending&limit=50", orgID), nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list invitations: %w", err)
	}

	var result struct {
		Data       []OrgInvitation `json:"data"`
		TotalCount int             `json:"total_count"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return nil, fmt.Errorf("failed to parse invitations response: %w", err)
	}

	return result.Data, nil
}

// RevokeInvitation revokes a pending invitation.
func (s *OrgService) RevokeInvitation(ctx context.Context, orgID, invitationID string) error {
	if !s.IsEnabled() {
		return fmt.Errorf("Clerk not configured — set CLERK_SECRET_KEY")
	}

	_, err := s.clerkRequest(ctx, "POST",
		fmt.Sprintf("/organizations/%s/invitations/%s/revoke", orgID, invitationID), nil)
	if err != nil {
		return fmt.Errorf("failed to revoke invitation: %w", err)
	}

	return nil
}

// --- HTTP helper ---

func (s *OrgService) clerkRequest(ctx context.Context, method, path string, body io.Reader) ([]byte, error) {
	url := s.baseURL + path

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+s.clerkSecretKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("clerk API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read clerk response: %w", err)
	}

	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("clerk API returned %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}
