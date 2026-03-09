package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	// Server
	Port string
	Env  string

	// Database
	DatabaseURL string

	// Clerk Auth
	ClerkSecretKey      string
	ClerkPEMPublicKey   string // Legacy: static PEM key (optional if JWKS URL is set)
	ClerkJWKSURL        string // Preferred: auto-fetches public keys from Clerk
	ClerkPublishableKey string // Frontend key for Clerk JS SDK (pk_test_... or pk_live_...)

	// Stripe
	StripeSecretKey     string
	StripeWebhookSecret string
	StripePriceSmallID  string
	StripePriceMediumID string
	StripePriceLargeID  string
	StripePriceGPUID    string

	// Hetzner Cloud (primary provider)
	HetznerAPIToken   string
	HetznerLocation   string // Datacenter location: fsn1, nbg1, hel1, ash, hil
	HetznerSSHKeyIDs  string // Comma-separated Hetzner SSH Key IDs
	HetznerSSHPrivKey string // PEM-encoded SSH private key for remote commands
	HetznerFirewallID string // Hetzner Firewall ID (optional)
	HetznerNetworkID  string // Hetzner Network ID (optional)
	HetznerImageID    string // Hetzner OS image ID (optional, defaults to ubuntu-24.04)

	// Container Registry (for snapshots — works with any Docker-compatible registry)
	RegistryURL  string // e.g. "registry.example.com/gradient-envs" or "docker.io/myorg/gradient"
	RegistryUser string
	RegistryPass string

	// AWS (legacy, kept for migration)
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	AWSRegion          string
	AWSAmiID           string
	AWSSecurityGroupID string
	AWSSubnetID        string
	AWSKeyPairName     string
	AWSECRRepoURI      string
	AWSInstanceProfile string

	// GCP (v1.1 — not used in MVP)
	GCPProjectID       string
	GCPCredentialsPath string
	GCPRegion          string

	// GitHub App (auto-fork)
	GitHubAppID            string
	GitHubAppWebhookSecret string

	// GitHub OAuth (repo connect)
	GitHubOAuthClientID     string
	GitHubOAuthClientSecret string
	GitHubOAuthRedirectURI  string

	// Linear Integration (agent tasks)
	LinearClientID     string
	LinearClientSecret string
	LinearRedirectURI  string

	// Secrets Backends
	VaultAddr  string
	VaultToken string

	// JWT Signing (for Gradient-issued CLI tokens)
	JWTSecret string // HMAC secret for signing long-lived CLI tokens

	// API
	APIURL string // External API URL (e.g. "https://api.usegradient.dev") — passed to agents

	// Agent
	AgentDownloadURL string // URL for gradient-agent binary download

	// Live Context Mesh (NATS)
	NATSUrl       string // NATS server URL (e.g. "nats://localhost:4222")
	NATSAuthToken string // Optional NATS auth token
	NATSMaxAge    string // Event retention period (default "168h" = 7 days)

	// Warm Pool
	WarmPoolDefaultSize int    // Default number of warm servers to keep (default: 3)
	WarmPoolMaxSize     int    // Hard cap on total warm servers (default: 3, max: 8)
	WarmPoolIdleTimeout string // Destroy warm servers idle longer than this (default: "30m")

	// MCP
	MCPServerPort string

	// Logging
	LogLevel string
}

func Load() *Config {
	return &Config{
		Port:                   getEnv("PORT", "6767"),
		Env:                    getEnv("ENV", "development"),
		DatabaseURL:            getEnv("DATABASE_URL", ""),
		ClerkSecretKey:         getEnv("CLERK_SECRET_KEY", ""),
		ClerkPEMPublicKey:      getEnv("CLERK_PEM_PUBLIC_KEY", ""),
		ClerkJWKSURL:           getEnv("CLERK_JWKS_URL", ""),
		ClerkPublishableKey:    getEnv("CLERK_PUBLISHABLE_KEY", ""),
		StripeSecretKey:        getEnv("STRIPE_SECRET_KEY", ""),
		StripeWebhookSecret:    getEnv("STRIPE_WEBHOOK_SECRET", ""),
		StripePriceSmallID:     getEnv("STRIPE_PRICE_SMALL_ID", ""),
		StripePriceMediumID:    getEnv("STRIPE_PRICE_MEDIUM_ID", ""),
		StripePriceLargeID:     getEnv("STRIPE_PRICE_LARGE_ID", ""),
		StripePriceGPUID:       getEnv("STRIPE_PRICE_GPU_ID", ""),
		HetznerAPIToken:        getEnv("HETZNER_API_TOKEN", ""),
		HetznerLocation:        getEnv("HETZNER_LOCATION", "fsn1"),
		HetznerSSHKeyIDs:       getEnv("HETZNER_SSH_KEY_IDS", ""),
		HetznerSSHPrivKey:      unescapeNewlines(getEnv("HETZNER_SSH_PRIVATE_KEY", "")),
		HetznerFirewallID:      getEnv("HETZNER_FIREWALL_ID", ""),
		HetznerNetworkID:       getEnv("HETZNER_NETWORK_ID", ""),
		HetznerImageID:         getEnv("HETZNER_IMAGE_ID", ""),
		RegistryURL:            getEnv("REGISTRY_URL", ""),
		RegistryUser:           getEnv("REGISTRY_USER", ""),
		RegistryPass:           getEnv("REGISTRY_PASS", ""),
		AWSAccessKeyID:         getEnv("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey:     getEnv("AWS_SECRET_ACCESS_KEY", ""),
		AWSRegion:              getEnv("AWS_REGION", "us-east-1"),
		AWSAmiID:               getEnv("AWS_AMI_ID", ""),
		AWSSecurityGroupID:     getEnv("AWS_SECURITY_GROUP_ID", ""),
		AWSSubnetID:            getEnv("AWS_SUBNET_ID", ""),
		AWSKeyPairName:         getEnv("AWS_KEY_PAIR_NAME", ""),
		AWSECRRepoURI:          getEnv("AWS_ECR_REPO_URI", ""),
		AWSInstanceProfile:     getEnv("AWS_INSTANCE_PROFILE", ""),
		GCPProjectID:           getEnv("GCP_PROJECT_ID", ""),
		GCPCredentialsPath:     getEnv("GCP_CREDENTIALS_PATH", ""),
		GCPRegion:              getEnv("GCP_REGION", "us-west1"),
		GitHubAppID:             getEnv("GITHUB_APP_ID", ""),
		GitHubAppWebhookSecret:  getEnv("GITHUB_APP_WEBHOOK_SECRET", ""),
		GitHubOAuthClientID:     getEnv("GITHUB_OAUTH_CLIENT_ID", ""),
		GitHubOAuthClientSecret: getEnv("GITHUB_OAUTH_CLIENT_SECRET", ""),
		GitHubOAuthRedirectURI:  getEnv("GITHUB_OAUTH_REDIRECT_URI", ""),
		LinearClientID:         getEnv("LINEAR_CLIENT_ID", ""),
		LinearClientSecret:     getEnv("LINEAR_CLIENT_SECRET", ""),
		LinearRedirectURI:      getEnv("LINEAR_REDIRECT_URI", ""),
		VaultAddr:              getEnv("VAULT_ADDR", ""),
		VaultToken:             getEnv("VAULT_TOKEN", ""),
		JWTSecret:              getEnv("JWT_SECRET", ""),
		APIURL:                 getEnv("API_URL", ""),
		AgentDownloadURL:       getEnv("AGENT_DOWNLOAD_URL", ""),
		NATSUrl:                getEnv("NATS_URL", ""),
		NATSAuthToken:          getEnv("NATS_AUTH_TOKEN", ""),
		NATSMaxAge:             getEnv("NATS_MAX_AGE", "168h"),
		WarmPoolDefaultSize:    getEnvInt("WARM_POOL_DEFAULT_SIZE", 3),
		WarmPoolMaxSize:        clampInt(getEnvInt("WARM_POOL_MAX_SIZE", 3), 0, 8),
		WarmPoolIdleTimeout:    getEnv("WARM_POOL_IDLE_TIMEOUT", "30m"),
		MCPServerPort:          getEnv("MCP_SERVER_PORT", "8081"),
		LogLevel:               getEnv("LOG_LEVEL", "info"),
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if v, err := strconv.Atoi(value); err == nil {
			return v
		}
	}
	return defaultValue
}

// unescapeNewlines converts literal \n sequences back to real newlines.
// Needed for .env files where multiline values (SSH keys, PEM certs) are
// stored as single lines with escaped newlines.
func unescapeNewlines(s string) string {
	return strings.ReplaceAll(s, `\n`, "\n")
}

func clampInt(v, min, max int) int {
	if v < min {
		return min
	}
	if v > max {
		return max
	}
	return v
}
