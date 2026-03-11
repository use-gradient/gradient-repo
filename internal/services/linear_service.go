package services

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
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

// LinearService handles Linear workspace connections, webhooks, and GraphQL queries.
type LinearService struct {
	db           *db.DB
	clientID     string
	clientSecret string
	redirectURI  string
	apiURL       string
}

func NewLinearService(database *db.DB, clientID, clientSecret, redirectURI string) *LinearService {
	return &LinearService{
		db:           database,
		clientID:     clientID,
		clientSecret: clientSecret,
		redirectURI:  redirectURI,
		apiURL:       "https://api.linear.app",
	}
}

func (s *LinearService) Configured() bool {
	return s.clientID != "" && s.clientSecret != "" && s.redirectURI != ""
}

// ─── OAuth Flow ─────────────────────────────────────────────────────────

func (s *LinearService) GetAuthURL(orgID, state string) (string, error) {
	if s.redirectURI == "" {
		return "", fmt.Errorf("LINEAR_REDIRECT_URI is not set. Run 'make run-api' to start ngrok and set it automatically")
	}
	return fmt.Sprintf(
		"https://linear.app/oauth/authorize?client_id=%s&redirect_uri=%s&response_type=code&scope=read,write,issues:create&state=%s",
		s.clientID, s.redirectURI, state,
	), nil
}

func (s *LinearService) ExchangeCode(ctx context.Context, orgID, code string) (*models.LinearConnection, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("client_id", s.clientID)
	form.Set("client_secret", s.clientSecret)
	form.Set("redirect_uri", s.redirectURI)
	form.Set("code", code)

	resp, err := http.PostForm("https://api.linear.app/oauth/token", form)
	if err != nil {
		return nil, fmt.Errorf("token exchange failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read token response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("linear token exchange returned %d: %s", resp.StatusCode, string(respBody))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w (body: %s)", err, string(respBody))
	}
	if tokenResp.AccessToken == "" {
		return nil, fmt.Errorf("no access token in response (body: %s)", string(respBody))
	}

	wsID, wsName, err := s.fetchWorkspaceInfo(tokenResp.AccessToken)
	if err != nil {
		log.Printf("[linear] warning: failed to fetch workspace info: %v", err)
	}

	webhookSecret := generateWebhookSecret()

	conn := &models.LinearConnection{
		ID:               uuid.New().String(),
		OrgID:            orgID,
		AccessToken:      tokenResp.AccessToken,
		RefreshToken:     tokenResp.RefreshToken,
		WorkspaceID:      wsID,
		WorkspaceName:    wsName,
		WebhookSecret:    webhookSecret,
		FilterLabelNames: []string{"gradient-agent"},
		TriggerState:     "Todo",
		Status:           "active",
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	if tokenResp.ExpiresIn > 0 {
		t := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		conn.TokenExpiresAt = &t
	}

	labelsJSON, _ := json.Marshal(conn.FilterLabelNames)
	teamsJSON, _ := json.Marshal(conn.FilterTeamIDs)
	projectsJSON, _ := json.Marshal(conn.FilterProjectIDs)

	_, err = s.db.Pool.Exec(ctx, `
		INSERT INTO linear_connections (id, org_id, access_token, refresh_token, token_expires_at,
			workspace_id, workspace_name, webhook_id, webhook_secret,
			filter_team_ids, filter_project_ids, filter_label_names, trigger_state, status, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)
		ON CONFLICT (org_id) DO UPDATE SET
			access_token=EXCLUDED.access_token, refresh_token=EXCLUDED.refresh_token,
			token_expires_at=EXCLUDED.token_expires_at, workspace_id=EXCLUDED.workspace_id,
			workspace_name=EXCLUDED.workspace_name, webhook_id=EXCLUDED.webhook_id,
			webhook_secret=EXCLUDED.webhook_secret, status='active', updated_at=NOW()`,
		conn.ID, conn.OrgID, conn.AccessToken, conn.RefreshToken, conn.TokenExpiresAt,
		conn.WorkspaceID, conn.WorkspaceName, conn.WebhookID, conn.WebhookSecret,
		string(teamsJSON), string(projectsJSON), string(labelsJSON), conn.TriggerState, conn.Status,
		conn.CreatedAt, conn.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to save connection: %w", err)
	}

	return conn, nil
}

func (s *LinearService) GetConnection(ctx context.Context, orgID string) (*models.LinearConnection, error) {
	conn := &models.LinearConnection{}
	var labelsJSON, teamsJSON, projectsJSON string

	err := s.db.Pool.QueryRow(ctx, `
		SELECT id, org_id, access_token, refresh_token, token_expires_at,
			workspace_id, workspace_name, webhook_id, webhook_secret,
			filter_team_ids, filter_project_ids, filter_label_names, trigger_state, status,
			created_at, updated_at
		FROM linear_connections WHERE org_id = $1`, orgID,
	).Scan(
		&conn.ID, &conn.OrgID, &conn.AccessToken, &conn.RefreshToken, &conn.TokenExpiresAt,
		&conn.WorkspaceID, &conn.WorkspaceName, &conn.WebhookID, &conn.WebhookSecret,
		&teamsJSON, &projectsJSON, &labelsJSON, &conn.TriggerState, &conn.Status,
		&conn.CreatedAt, &conn.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	json.Unmarshal([]byte(labelsJSON), &conn.FilterLabelNames)
	json.Unmarshal([]byte(teamsJSON), &conn.FilterTeamIDs)
	json.Unmarshal([]byte(projectsJSON), &conn.FilterProjectIDs)

	return conn, nil
}

func (s *LinearService) DeleteConnection(ctx context.Context, orgID string) error {
	_, err := s.db.Pool.Exec(ctx, `DELETE FROM linear_connections WHERE org_id = $1`, orgID)
	return err
}

// GetAllConnections returns all active Linear connections (for webhook org lookup).
func (s *LinearService) GetAllConnections(ctx context.Context) ([]*models.LinearConnection, error) {
	rows, err := s.db.Pool.Query(ctx, `
		SELECT id, org_id, workspace_id, workspace_name, status
		FROM linear_connections WHERE status = 'active'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var conns []*models.LinearConnection
	for rows.Next() {
		c := &models.LinearConnection{}
		if err := rows.Scan(&c.ID, &c.OrgID, &c.WorkspaceID, &c.WorkspaceName, &c.Status); err != nil {
			return nil, err
		}
		conns = append(conns, c)
	}
	return conns, nil
}

// ─── Webhook Processing ─────────────────────────────────────────────────

func (s *LinearService) VerifyWebhook(body []byte, signature, orgID string) bool {
	conn, err := s.GetConnection(context.Background(), orgID)
	if err != nil || conn == nil || conn.WebhookSecret == "" {
		return false
	}
	mac := hmac.New(sha256.New, []byte(conn.WebhookSecret))
	mac.Write(body)
	expected := hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(signature))
}

func (s *LinearService) ParseWebhookEvent(body []byte) (action string, issueData map[string]interface{}, err error) {
	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return "", nil, err
	}

	action, _ = payload["action"].(string)
	data, _ := payload["data"].(map[string]interface{})
	eventType, _ := payload["type"].(string)

	if eventType != "Issue" {
		return action, nil, nil
	}
	return action, data, nil
}

func (s *LinearService) ShouldProcessIssue(conn *models.LinearConnection, issueData map[string]interface{}) bool {
	state, _ := issueData["state"].(map[string]interface{})
	stateName, _ := state["name"].(string)
	if conn.TriggerState != "" && !strings.EqualFold(stateName, conn.TriggerState) {
		return false
	}

	if len(conn.FilterLabelNames) > 0 {
		labels, _ := issueData["labels"].([]interface{})
		found := false
		for _, l := range labels {
			lm, ok := l.(map[string]interface{})
			if !ok {
				continue
			}
			name, _ := lm["name"].(string)
			for _, fl := range conn.FilterLabelNames {
				if strings.EqualFold(name, fl) {
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if !found {
			return false
		}
	}

	return true
}

func (s *LinearService) UpdateIssueState(ctx context.Context, orgID, issueID, stateName string) error {
	conn, err := s.GetConnection(ctx, orgID)
	if err != nil || conn == nil {
		return fmt.Errorf("no linear connection for org")
	}

	stateID, err := s.resolveStateID(conn.AccessToken, issueID, stateName)
	if err != nil {
		return err
	}

	query := fmt.Sprintf(`mutation { issueUpdate(id: "%s", input: { stateId: "%s" }) { success } }`, issueID, stateID)
	_, err = s.graphql(conn.AccessToken, query)
	return err
}

func (s *LinearService) AddComment(ctx context.Context, orgID, issueID, body string) error {
	conn, err := s.GetConnection(ctx, orgID)
	if err != nil || conn == nil {
		return fmt.Errorf("no linear connection for org")
	}

	escaped := strings.ReplaceAll(body, `"`, `\"`)
	escaped = strings.ReplaceAll(escaped, "\n", `\n`)

	query := fmt.Sprintf(`mutation { commentCreate(input: { issueId: "%s", body: "%s" }) { success } }`, issueID, escaped)
	_, err = s.graphql(conn.AccessToken, query)
	return err
}

// ─── Internal helpers ───────────────────────────────────────────────────

func (s *LinearService) fetchWorkspaceInfo(token string) (string, string, error) {
	resp, err := s.graphql(token, `{ viewer { organization { id name } } }`)
	if err != nil {
		return "", "", err
	}
	viewer, _ := resp["viewer"].(map[string]interface{})
	org, _ := viewer["organization"].(map[string]interface{})
	wsID, _ := org["id"].(string)
	wsName, _ := org["name"].(string)
	return wsID, wsName, nil
}

func (s *LinearService) resolveStateID(token, issueID, stateName string) (string, error) {
	query := fmt.Sprintf(`{
		issue(id: "%s") {
			team { states { nodes { id name } } }
		}
	}`, issueID)

	resp, err := s.graphql(token, query)
	if err != nil {
		return "", err
	}

	issue, _ := resp["issue"].(map[string]interface{})
	team, _ := issue["team"].(map[string]interface{})
	states, _ := team["states"].(map[string]interface{})
	nodes, _ := states["nodes"].([]interface{})

	for _, n := range nodes {
		node, _ := n.(map[string]interface{})
		name, _ := node["name"].(string)
		if strings.EqualFold(name, stateName) {
			id, _ := node["id"].(string)
			return id, nil
		}
	}
	return "", fmt.Errorf("state %q not found", stateName)
}

func (s *LinearService) graphql(token, query string) (map[string]interface{}, error) {
	body, _ := json.Marshal(map[string]string{"query": query})
	req, _ := http.NewRequest("POST", s.apiURL+"/graphql", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Data   map[string]interface{} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("linear API error: %s", result.Errors[0].Message)
	}
	return result.Data, nil
}

func generateWebhookSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
