package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
)

type GitHubService struct {
	db           *db.DB
	clientID     string
	clientSecret string
	redirectURI  string
	webhookURL   string // public URL for webhook delivery (e.g. https://xxx.ngrok.io/api/v1/webhooks/github)
}

func NewGitHubService(database *db.DB, clientID, clientSecret, redirectURI, apiURL string) *GitHubService {
	webhookURL := apiURL + "/api/v1/webhooks/github"
	return &GitHubService{
		db:           database,
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		webhookURL:   webhookURL,
	}
}

func (s *GitHubService) Configured() bool {
	return s.clientID != "" && s.clientSecret != ""
}

// GetAuthURL builds the GitHub OAuth authorization URL.
func (s *GitHubService) GetAuthURL(orgID string) (string, string, error) {
	if !s.Configured() {
		return "", "", fmt.Errorf("GitHub OAuth not configured (set GITHUB_OAUTH_CLIENT_ID and GITHUB_OAUTH_CLIENT_SECRET)")
	}
	state := orgID + ":" + uuid.New().String()
	u := fmt.Sprintf(
		"https://github.com/login/oauth/authorize?client_id=%s&redirect_uri=%s&scope=%s&state=%s",
		url.QueryEscape(s.clientID),
		url.QueryEscape(s.redirectURI),
		url.QueryEscape("repo"),
		url.QueryEscape(state),
	)
	return u, state, nil
}

// ExchangeCode exchanges an OAuth code for an access token and stores the connection.
func (s *GitHubService) ExchangeCode(ctx context.Context, orgID, code string) (*models.GitHubConnection, error) {
	form := url.Values{
		"client_id":     {s.clientID},
		"client_secret": {s.clientSecret},
		"code":          {code},
		"redirect_uri":  {s.redirectURI},
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", "https://github.com/login/oauth/access_token", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer resp.Body.Close()

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		Scope       string `json:"scope"`
		TokenType   string `json:"token_type"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}
	if tokenResp.Error != "" {
		return nil, fmt.Errorf("GitHub OAuth error: %s — %s", tokenResp.Error, tokenResp.ErrorDesc)
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in GitHub response")
	}

	ghUser, ghAvatar, err := s.fetchGitHubUser(ctx, tokenResp.AccessToken)
	if err != nil {
		log.Printf("[github] warning: could not fetch user info: %v", err)
	}

	conn := &models.GitHubConnection{
		ID:           uuid.New().String(),
		OrgID:        orgID,
		AccessToken:  tokenResp.AccessToken,
		GitHubUser:   ghUser,
		GitHubAvatar: ghAvatar,
		Scopes:       tokenResp.Scope,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}

	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO github_connections (id, org_id, access_token, github_user, github_avatar, scopes, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		ON CONFLICT (org_id) DO UPDATE SET
			access_token=EXCLUDED.access_token, github_user=EXCLUDED.github_user,
			github_avatar=EXCLUDED.github_avatar, scopes=EXCLUDED.scopes, updated_at=NOW()`,
		conn.ID, conn.OrgID, conn.AccessToken, conn.GitHubUser, conn.GitHubAvatar,
		conn.Scopes, conn.CreatedAt, conn.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to save GitHub connection: %w", err)
	}

	log.Printf("[github] OAuth connected: user=%s, org=%s", ghUser, orgID)
	return conn, nil
}

// GetConnection returns the stored GitHub connection for an org, or nil.
func (s *GitHubService) GetConnection(ctx context.Context, orgID string) (*models.GitHubConnection, error) {
	conn := &models.GitHubConnection{}
	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, access_token, github_user, github_avatar, scopes, created_at, updated_at
		FROM github_connections WHERE org_id = $1`, orgID,
	).Scan(&conn.ID, &conn.OrgID, &conn.AccessToken, &conn.GitHubUser, &conn.GitHubAvatar,
		&conn.Scopes, &conn.CreatedAt, &conn.UpdatedAt)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return conn, nil
}

// DeleteConnection removes the GitHub connection for an org.
func (s *GitHubService) DeleteConnection(ctx context.Context, orgID string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM github_connections WHERE org_id = $1`, orgID)
	return err
}

// ListUserRepos returns repos the authenticated user has access to.
func (s *GitHubService) ListUserRepos(ctx context.Context, orgID string) ([]string, error) {
	conn, err := s.GetConnection(ctx, orgID)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, fmt.Errorf("GitHub not connected — authenticate first")
	}

	var allRepos []string
	page := 1
	for {
		apiURL := fmt.Sprintf("https://api.github.com/user/repos?per_page=100&page=%d&sort=updated", page)
		req, _ := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
		req.Header.Set("Authorization", "token "+conn.AccessToken)
		req.Header.Set("Accept", "application/vnd.github+json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("GitHub API error: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("GitHub API %d: %s", resp.StatusCode, string(body))
		}

		var repos []struct {
			FullName string `json:"full_name"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&repos); err != nil {
			return nil, fmt.Errorf("failed to parse repos: %w", err)
		}
		if len(repos) == 0 {
			break
		}
		for _, r := range repos {
			allRepos = append(allRepos, r.FullName)
		}
		if len(repos) < 100 {
			break
		}
		page++
	}
	return allRepos, nil
}

// CreateWebhook creates a webhook on a GitHub repo for push/create/delete events.
func (s *GitHubService) CreateWebhook(ctx context.Context, orgID, repoFullName, webhookSecret string) (int64, error) {
	conn, err := s.GetConnection(ctx, orgID)
	if err != nil || conn == nil {
		return 0, fmt.Errorf("GitHub not connected")
	}

	body := map[string]interface{}{
		"name":   "web",
		"active": true,
		"events": []string{"create", "push", "delete"},
		"config": map[string]string{
			"url":          s.webhookURL,
			"content_type": "json",
			"secret":       webhookSecret,
		},
	}
	bodyJSON, _ := json.Marshal(body)

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/hooks", repoFullName)
	req, _ := http.NewRequestWithContext(ctx, "POST", apiURL, strings.NewReader(string(bodyJSON)))
	req.Header.Set("Authorization", "token "+conn.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("failed to create webhook: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 {
		respBody, _ := io.ReadAll(resp.Body)
		if resp.StatusCode == 422 && strings.Contains(string(respBody), "already exists") {
			log.Printf("[github] Webhook already exists on %s, continuing", repoFullName)
			return 0, nil
		}
		return 0, fmt.Errorf("GitHub API %d creating webhook: %s", resp.StatusCode, string(respBody))
	}

	var hook struct {
		ID int64 `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&hook); err != nil {
		return 0, fmt.Errorf("failed to parse webhook response: %w", err)
	}

	log.Printf("[github] Created webhook %d on %s", hook.ID, repoFullName)
	return hook.ID, nil
}

// DeleteWebhook removes a webhook from a GitHub repo.
func (s *GitHubService) DeleteWebhook(ctx context.Context, orgID, repoFullName string, hookID int64) error {
	if hookID == 0 {
		return nil
	}
	conn, err := s.GetConnection(ctx, orgID)
	if err != nil || conn == nil {
		return nil
	}

	apiURL := fmt.Sprintf("https://api.github.com/repos/%s/hooks/%d", repoFullName, hookID)
	req, _ := http.NewRequestWithContext(ctx, "DELETE", apiURL, nil)
	req.Header.Set("Authorization", "token "+conn.AccessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to delete webhook: %w", err)
	}
	resp.Body.Close()

	if resp.StatusCode != 204 && resp.StatusCode != 404 {
		return fmt.Errorf("GitHub API %d deleting webhook", resp.StatusCode)
	}
	return nil
}

func (s *GitHubService) fetchGitHubUser(ctx context.Context, token string) (string, string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/user", nil)
	req.Header.Set("Authorization", "token "+token)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var user struct {
		Login     string `json:"login"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return "", "", err
	}
	return user.Login, user.AvatarURL, nil
}
