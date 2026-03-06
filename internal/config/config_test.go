package config

import (
	"os"
	"testing"
)

func TestLoadDefaults(t *testing.T) {
	// Clear relevant env vars to test defaults
	envVars := []string{
		"PORT", "ENV", "DATABASE_URL", "AWS_REGION",
		"MCP_SERVER_PORT", "LOG_LEVEL", "GCP_REGION",
	}
	for _, v := range envVars {
		os.Unsetenv(v)
	}

	cfg := Load()

	tests := []struct {
		field    string
		got      string
		expected string
	}{
		{"Port", cfg.Port, "6767"},
		{"Env", cfg.Env, "development"},
		{"DatabaseURL", cfg.DatabaseURL, ""},
		{"AWSRegion", cfg.AWSRegion, "us-east-1"},
		{"GCPRegion", cfg.GCPRegion, "us-west1"},
		{"MCPServerPort", cfg.MCPServerPort, "8081"},
		{"LogLevel", cfg.LogLevel, "info"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if tt.got != tt.expected {
				t.Errorf("%s: got %q, want %q", tt.field, tt.got, tt.expected)
			}
		})
	}
}

func TestLoadFromEnv(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("ENV", "production")
	t.Setenv("DATABASE_URL", "postgres://localhost:5432/test")
	t.Setenv("AWS_REGION", "eu-west-1")
	t.Setenv("LOG_LEVEL", "debug")

	cfg := Load()

	if cfg.Port != "9090" {
		t.Errorf("Expected Port '9090', got %q", cfg.Port)
	}
	if cfg.Env != "production" {
		t.Errorf("Expected Env 'production', got %q", cfg.Env)
	}
	if cfg.DatabaseURL != "postgres://localhost:5432/test" {
		t.Errorf("Expected DatabaseURL 'postgres://localhost:5432/test', got %q", cfg.DatabaseURL)
	}
	if cfg.AWSRegion != "eu-west-1" {
		t.Errorf("Expected AWSRegion 'eu-west-1', got %q", cfg.AWSRegion)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("Expected LogLevel 'debug', got %q", cfg.LogLevel)
	}
}

func TestGetEnv(t *testing.T) {
	t.Run("returns env var when set", func(t *testing.T) {
		t.Setenv("TEST_VAR_123", "hello")
		if got := getEnv("TEST_VAR_123", "default"); got != "hello" {
			t.Errorf("Expected 'hello', got %q", got)
		}
	})

	t.Run("returns default when not set", func(t *testing.T) {
		os.Unsetenv("TEST_VAR_UNSET_456")
		if got := getEnv("TEST_VAR_UNSET_456", "fallback"); got != "fallback" {
			t.Errorf("Expected 'fallback', got %q", got)
		}
	})

	t.Run("returns default when empty", func(t *testing.T) {
		t.Setenv("TEST_VAR_EMPTY_789", "")
		if got := getEnv("TEST_VAR_EMPTY_789", "default"); got != "default" {
			t.Errorf("Expected 'default' for empty env var, got %q", got)
		}
	})
}

func TestConfigFields(t *testing.T) {
	// Verify all Stripe config fields can be loaded
	t.Setenv("STRIPE_SECRET_KEY", "sk_test_123")
	t.Setenv("STRIPE_WEBHOOK_SECRET", "whsec_123")
	t.Setenv("STRIPE_PRICE_SMALL_ID", "price_small")
	t.Setenv("STRIPE_PRICE_MEDIUM_ID", "price_medium")
	t.Setenv("STRIPE_PRICE_LARGE_ID", "price_large")

	cfg := Load()

	if cfg.StripeSecretKey != "sk_test_123" {
		t.Errorf("StripeSecretKey: got %q", cfg.StripeSecretKey)
	}
	if cfg.StripeWebhookSecret != "whsec_123" {
		t.Errorf("StripeWebhookSecret: got %q", cfg.StripeWebhookSecret)
	}
	if cfg.StripePriceSmallID != "price_small" {
		t.Errorf("StripePriceSmallID: got %q", cfg.StripePriceSmallID)
	}
}

func TestAWSConfigFields(t *testing.T) {
	t.Setenv("AWS_AMI_ID", "ami-12345")
	t.Setenv("AWS_SECURITY_GROUP_ID", "sg-12345")
	t.Setenv("AWS_SUBNET_ID", "subnet-12345")
	t.Setenv("AWS_KEY_PAIR_NAME", "my-key")
	t.Setenv("AWS_ECR_REPO_URI", "123456789.dkr.ecr.us-east-1.amazonaws.com/gradient")
	t.Setenv("AWS_INSTANCE_PROFILE", "gradient-ec2-role")

	cfg := Load()

	if cfg.AWSAmiID != "ami-12345" {
		t.Errorf("AWSAmiID: got %q", cfg.AWSAmiID)
	}
	if cfg.AWSSecurityGroupID != "sg-12345" {
		t.Errorf("AWSSecurityGroupID: got %q", cfg.AWSSecurityGroupID)
	}
	if cfg.AWSECRRepoURI != "123456789.dkr.ecr.us-east-1.amazonaws.com/gradient" {
		t.Errorf("AWSECRRepoURI: got %q", cfg.AWSECRRepoURI)
	}
}

func TestGitHubConfigFields(t *testing.T) {
	t.Setenv("GITHUB_APP_ID", "12345")
	t.Setenv("GITHUB_APP_WEBHOOK_SECRET", "ghsecret123")

	cfg := Load()

	if cfg.GitHubAppID != "12345" {
		t.Errorf("GitHubAppID: got %q", cfg.GitHubAppID)
	}
	if cfg.GitHubAppWebhookSecret != "ghsecret123" {
		t.Errorf("GitHubAppWebhookSecret: got %q", cfg.GitHubAppWebhookSecret)
	}
}
