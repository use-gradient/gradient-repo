package commands

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// CLIConfig stores CLI configuration on disk
type CLIConfig struct {
	APIURL    string `json:"api_url"`
	Token     string `json:"token"`
	ActiveOrg string `json:"active_org"`
	UserID    string `json:"user_id,omitempty"`
	Email     string `json:"email,omitempty"`
	Name      string `json:"name,omitempty"`
}

func configDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".gradient")
}

func configPath() string {
	return filepath.Join(configDir(), "config.json")
}

// LoadCLIConfig loads CLI config from ~/.gradient/config.json
func LoadCLIConfig() *CLIConfig {
	data, err := os.ReadFile(configPath())
	if err != nil {
		return &CLIConfig{
			APIURL: "http://localhost:6767",
		}
	}

	var cfg CLIConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return &CLIConfig{
			APIURL: "http://localhost:6767",
		}
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "http://localhost:6767"
	}
	return &cfg
}

// SaveCLIConfig saves CLI config to ~/.gradient/config.json
func SaveCLIConfig(cfg *CLIConfig) error {
	if err := os.MkdirAll(configDir(), 0700); err != nil {
		return fmt.Errorf("failed to create config dir: %w", err)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	return os.WriteFile(configPath(), data, 0600)
}

// APIClient handles HTTP communication with the Gradient API
type APIClient struct {
	BaseURL string
	Token   string
	OrgID   string
	client  *http.Client
}

// NewAPIClient creates a client from the saved CLI config
func NewAPIClient() (*APIClient, error) {
	cfg := LoadCLIConfig()

	if cfg.APIURL == "" {
		return nil, fmt.Errorf("API URL not configured. Run: gc auth login")
	}

	return &APIClient{
		BaseURL: cfg.APIURL,
		Token:   cfg.Token,
		OrgID:   cfg.ActiveOrg,
		client:  &http.Client{},
	}, nil
}

// DoJSON makes an API request and decodes the JSON response
func (c *APIClient) DoJSON(method, path string, body interface{}, result interface{}) error {
	resp, err := c.Do(method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode >= 400 {
		var errResp map[string]string
		if json.Unmarshal(respBody, &errResp) == nil {
			if msg, ok := errResp["error"]; ok {
				return fmt.Errorf("API error (%d): %s", resp.StatusCode, msg)
			}
		}
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("failed to decode response: %w", err)
		}
	}

	return nil
}

// Do makes a raw HTTP request to the API
func (c *APIClient) Do(method, path string, body interface{}) (*http.Response, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.BaseURL+path, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if c.OrgID != "" {
		req.Header.Set("X-Org-ID", c.OrgID)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed (is the API server running?): %w", err)
	}

	return resp, nil
}
