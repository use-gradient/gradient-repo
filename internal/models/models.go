package models

import (
	"time"
)

// Environment represents a Gradient environment (Docker container on a cloud server).
// Provider-agnostic: works with Hetzner, AWS, GCP, Azure, bare-metal, etc.
type Environment struct {
	ID            string                 `json:"id"`
	Name          string                 `json:"name"`
	OrgID         string                 `json:"org_id"`
	Provider      string                 `json:"provider"`       // cloud provider name ("hetzner", "aws", "gcp", etc.)
	Region        string                 `json:"region"`         // provider-specific region/location
	Size          string                 `json:"size"`           // abstract size ("small", "medium", "large", "gpu")
	ClusterName   string                 `json:"cluster_name,omitempty"` // provider-specific reference (server ID, instance ID, etc.)
	Status        string                 `json:"status"`
	Resources     ResourceSpec           `json:"resources"`
	Config        map[string]interface{} `json:"config,omitempty"`
	ContextBranch string                 `json:"context_branch,omitempty"`
	CreatedAt     time.Time              `json:"created_at"`
	UpdatedAt     time.Time              `json:"updated_at"`
	DestroyedAt   *time.Time             `json:"destroyed_at,omitempty"`
}

type ResourceSpec struct {
	CPU    string `json:"cpu,omitempty"`
	Memory string `json:"memory,omitempty"`
	Disk   string `json:"disk,omitempty"`
}

// SizeToResources maps size names to resource specs
func SizeToResources(size string) ResourceSpec {
	switch size {
	case "medium":
		return ResourceSpec{CPU: "4", Memory: "8Gi"}
	case "large":
		return ResourceSpec{CPU: "8", Memory: "16Gi"}
	case "gpu":
		return ResourceSpec{CPU: "8", Memory: "16Gi"}
	default: // small
		return ResourceSpec{CPU: "2", Memory: "4Gi"}
	}
}

// SizeToHourlyRate returns the hourly rate for a given size
func SizeToHourlyRate(size string) float64 {
	switch size {
	case "medium":
		return 0.35
	case "large":
		return 0.70
	case "gpu":
		return 3.50
	default: // small
		return 0.15
	}
}

// SizeToEC2InstanceType maps size to EC2 instance type (legacy AWS)
func SizeToEC2InstanceType(size string) string {
	switch size {
	case "medium":
		return "t3.xlarge"
	case "large":
		return "t3.2xlarge"
	case "gpu":
		return "g4dn.xlarge"
	default: // small
		return "t3.medium"
	}
}

// SizeToHetznerServerType maps size to Hetzner Cloud server type
func SizeToHetznerServerType(size string) string {
	switch size {
	case "medium":
		return "cx32" // 4 vCPU, 8 GB RAM
	case "large":
		return "cx42" // 8 vCPU, 16 GB RAM
	case "gpu":
		return "cx52" // 16 vCPU, 32 GB RAM (no native GPU; use largest shared)
	default: // small
		return "cx22" // 2 vCPU, 4 GB RAM
	}
}

// SizeToHetznerHourlyRate returns hourly cost for Hetzner server types
func SizeToHetznerHourlyRate(size string) float64 {
	switch size {
	case "medium":
		return 0.020 // cx32
	case "large":
		return 0.039 // cx42
	case "gpu":
		return 0.078 // cx52
	default: // small
		return 0.010 // cx22
	}
}

// --- Provider-agnostic size mapping registry ---
// Instead of adding a SizeTo*Type function for each provider, use this registry.
// New providers register their size mappings at init time.

// ProviderSizeMap maps (provider, size) → provider-specific machine type.
var ProviderSizeMap = map[string]map[string]string{
	"hetzner": {
		"small":  "cx22",
		"medium": "cx32",
		"large":  "cx42",
		"gpu":    "cx52",
	},
	"aws": {
		"small":  "t3.medium",
		"medium": "t3.xlarge",
		"large":  "t3.2xlarge",
		"gpu":    "g4dn.xlarge",
	},
	// Future providers register here:
	// "gcp": { "small": "e2-medium", ... },
	// "azure": { "small": "Standard_B2s", ... },
}

// ProviderHourlyRate maps (provider, size) → hourly cost.
var ProviderHourlyRate = map[string]map[string]float64{
	"hetzner": {
		"small": 0.010, "medium": 0.020, "large": 0.039, "gpu": 0.078,
	},
	"aws": {
		"small": 0.15, "medium": 0.35, "large": 0.70, "gpu": 3.50,
	},
}

// SizeToMachineType returns the provider-specific machine type for a given size.
// Falls back to the size string itself if the provider isn't registered.
func SizeToMachineType(provider, size string) string {
	if pm, ok := ProviderSizeMap[provider]; ok {
		if mt, ok := pm[size]; ok {
			return mt
		}
	}
	// Fallback: for unregistered providers, return the size as-is
	return size
}

// HourlyRate returns the hourly cost for a provider/size combo.
func HourlyRate(provider, size string) float64 {
	if pr, ok := ProviderHourlyRate[provider]; ok {
		if rate, ok := pr[size]; ok {
			return rate
		}
	}
	return SizeToHourlyRate(size)
}

// RegisterProvider registers a new provider's size mappings and hourly rates.
// Call this from init() in provider implementation packages.
func RegisterProvider(name string, sizeMap map[string]string, rateMap map[string]float64) {
	ProviderSizeMap[name] = sizeMap
	if rateMap != nil {
		ProviderHourlyRate[name] = rateMap
	}
}

// SupportedProviders returns a list of all registered provider names.
func SupportedProviders() []string {
	providers := make([]string, 0, len(ProviderSizeMap))
	for p := range ProviderSizeMap {
		providers = append(providers, p)
	}
	return providers
}

// Context represents branch/PR context with runtime state
type Context struct {
	ID                string                 `json:"id"`
	Branch            string                 `json:"branch"`
	OrgID             string                 `json:"org_id"`
	CommitSHA         string                 `json:"commit_sha,omitempty"`
	InstalledPackages []InstalledPackage     `json:"installed_packages"`
	PreviousFailures  []TestFailure          `json:"previous_failures"`
	AttemptedFixes    []Fix                  `json:"attempted_fixes"`
	Patterns          map[string]interface{} `json:"patterns"`
	GlobalConfigs     map[string]string      `json:"global_configs"`
	BaseOS            string                 `json:"base_os"`
	CreatedAt         time.Time              `json:"created_at"`
	UpdatedAt         time.Time              `json:"updated_at"`
}

type InstalledPackage struct {
	Manager     string    `json:"manager"`
	Name        string    `json:"name"`
	Version     string    `json:"version,omitempty"`
	Source      string    `json:"source,omitempty"`
	InstalledAt time.Time `json:"installed_at"`
}

type TestFailure struct {
	Test      string    `json:"test"`
	Error     string    `json:"error"`
	Timestamp time.Time `json:"timestamp"`
	Commit    string    `json:"commit,omitempty"`
}

type Fix struct {
	Fix       string    `json:"fix"`
	Success   bool      `json:"success"`
	Timestamp time.Time `json:"timestamp"`
	Commit    string    `json:"commit,omitempty"`
}

// UsageEvent represents environment usage for billing
type UsageEvent struct {
	ID            string     `json:"id"`
	EnvironmentID string     `json:"environment_id"`
	OrgID         string     `json:"org_id"`
	Size          string     `json:"size"`
	StartedAt     time.Time  `json:"started_at"`
	StoppedAt     *time.Time `json:"stopped_at,omitempty"`
	BilledSeconds int        `json:"billed_seconds"`
	CreatedAt     time.Time  `json:"created_at"`
}

// OrgSettings holds per-org settings including Stripe info
type OrgSettings struct {
	OrgID                string    `json:"org_id"`
	StripeCustomerID     string    `json:"stripe_customer_id,omitempty"`
	StripeSubscriptionID string    `json:"stripe_subscription_id,omitempty"`
	OwnerEmail           string    `json:"owner_email,omitempty"`
	OwnerUserID          string    `json:"owner_user_id,omitempty"`
	BillingTier          string    `json:"billing_tier"` // "free" or "paid"
	CreatedAt            time.Time `json:"created_at"`
	UpdatedAt            time.Time `json:"updated_at"`
}

// BillingStatus is the computed billing status for an org (returned by billing status API)
type BillingStatus struct {
	OrgID            string   `json:"org_id"`
	Tier             string   `json:"tier"`               // "free" or "paid"
	HasPaymentMethod bool     `json:"has_payment_method"`  // Stripe customer + subscription exists
	StripeConfigured bool     `json:"stripe_configured"`   // whether Stripe is configured on the server
	FreeHoursUsed    float64  `json:"free_hours_used"`     // hours used this month (free tier)
	FreeHoursLimit   float64  `json:"free_hours_limit"`    // 20.0
	FreeHoursLeft    float64  `json:"free_hours_left"`     // remaining free hours
	CanCreateEnv     bool     `json:"can_create_env"`      // whether the org can create a new environment
	AllowedSizes     []string `json:"allowed_sizes"`       // sizes the org can use
	Month            string   `json:"month"`
}

// UsageSummary is a computed billing summary
type UsageSummary struct {
	OrgID       string  `json:"org_id"`
	Month       string  `json:"month"`
	TotalHours  float64 `json:"total_hours"`
	TotalCost   float64 `json:"total_cost"`
	SmallHours  float64 `json:"small_hours"`
	MediumHours float64 `json:"medium_hours"`
	LargeHours  float64 `json:"large_hours"`
	GPUHours    float64 `json:"gpu_hours"`
}

// SecretSync represents secret synchronization metadata
type SecretSync struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	OrgID         string    `json:"org_id"`
	SecretKey     string    `json:"secret_key"`
	Backend       string    `json:"backend"`
	BackendPath   string    `json:"backend_path,omitempty"`
	SyncedAt      time.Time `json:"synced_at"`
}

// Snapshot represents a container commit snapshot (full filesystem delta)
type Snapshot struct {
	ID               string    `json:"id"`
	OrgID            string    `json:"org_id"`
	Branch           string    `json:"branch"`
	EnvironmentID    string    `json:"environment_id,omitempty"`
	SnapshotType     string    `json:"snapshot_type"` // "container_commit", "periodic", "on_stop", "auto_fork"
	ImageRef         string    `json:"image_ref"`     // ECR image URI
	SizeBytes        int64     `json:"size_bytes"`
	ParentSnapshotID string    `json:"parent_snapshot_id,omitempty"` // fork lineage
	CommitSHA        string    `json:"commit_sha,omitempty"`
	CreatedAt        time.Time `json:"created_at"`
}

// AutoscalePolicy defines scaling rules for an environment.
type AutoscalePolicy struct {
	ID                 string     `json:"id"`
	EnvironmentID      string     `json:"environment_id"`
	OrgID              string     `json:"org_id"`
	MinReplicas        int        `json:"min_replicas"`
	MaxReplicas        int        `json:"max_replicas"`
	TargetCPU          float64    `json:"target_cpu"`           // target CPU utilization (0-1)
	TargetMemory       float64    `json:"target_memory"`        // target memory utilization (0-1)
	ScaleUpThreshold   float64    `json:"scale_up_threshold"`   // trigger scale-up above this (0-1)
	ScaleDownThreshold float64    `json:"scale_down_threshold"` // trigger scale-down below this (0-1)
	CooldownSecs       int        `json:"cooldown_secs"`
	CurrentReplicas    int        `json:"current_replicas"`
	Enabled            bool       `json:"enabled"`
	LastScaleAt        *time.Time `json:"last_scale_at,omitempty"`
	LastScaleDirection string     `json:"last_scale_direction,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

// AutoscaleStatus is the live status of an autoscale policy including current metrics.
type AutoscaleStatus struct {
	Policy          AutoscalePolicy `json:"policy"`
	CurrentCPU      float64         `json:"current_cpu"`
	CurrentMemory   float64         `json:"current_memory"`
	ActiveReplicas  int             `json:"active_replicas"`
	DesiredReplicas int             `json:"desired_replicas"`
	ScalingActive   bool            `json:"scaling_active"`
	PendingAction   string          `json:"pending_action,omitempty"` // "scale_up", "scale_down", "cooldown", ""
	CooldownUntil   *time.Time      `json:"cooldown_until,omitempty"`
}

// ScaleEvent records a historical scaling action for auditing.
type ScaleEvent struct {
	ID            string    `json:"id"`
	EnvironmentID string    `json:"environment_id"`
	OrgID         string    `json:"org_id"`
	Direction     string    `json:"direction"` // "up" or "down"
	FromReplicas  int       `json:"from_replicas"`
	ToReplicas    int       `json:"to_replicas"`
	TriggerCPU    float64   `json:"trigger_cpu"`
	TriggerMemory float64   `json:"trigger_memory"`
	CreatedAt     time.Time `json:"created_at"`
}

// GitHubConnection stores a GitHub OAuth token for an org
type GitHubConnection struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	AccessToken  string    `json:"-"`
	GitHubUser   string    `json:"github_user"`
	GitHubAvatar string    `json:"github_avatar"`
	Scopes       string    `json:"scopes"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// RepoConnection links a GitHub repo to a Gradient org for auto-fork
type RepoConnection struct {
	ID                    string    `json:"id"`
	OrgID                 string    `json:"org_id"`
	InstallationID        int64     `json:"installation_id"`
	RepoFullName          string    `json:"repo_full_name"`
	DefaultBranch         string    `json:"default_branch"`
	AutoForkEnabled       bool      `json:"auto_fork_enabled"`
	AutoSnapshotOnPush    bool      `json:"auto_snapshot_on_push"`
	WebhookID             int64     `json:"webhook_id,omitempty"`
	CreatedAt             time.Time `json:"created_at"`
}

// GitHubInstallation stores raw GitHub App installation data from webhooks
type GitHubInstallation struct {
	InstallationID    int64     `json:"installation_id"`
	AccountLogin      string    `json:"account_login"`
	Repos             []string  `json:"repos"` // list of "owner/repo" strings
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ═══════════════════════════════════════════════════════════════
// Agent Tasks: Linear + Claude Code Integration
// ═══════════════════════════════════════════════════════════════

// LinearConnection represents a Linear workspace connection for an org
type LinearConnection struct {
	ID              string    `json:"id"`
	OrgID           string    `json:"org_id"`
	AccessToken     string    `json:"-"` // never serialize
	RefreshToken    string    `json:"-"`
	TokenExpiresAt  *time.Time `json:"token_expires_at,omitempty"`
	WorkspaceID     string    `json:"workspace_id,omitempty"`
	WorkspaceName   string    `json:"workspace_name,omitempty"`
	WebhookID       string    `json:"webhook_id,omitempty"`
	WebhookSecret   string    `json:"-"`
	FilterTeamIDs   []string  `json:"filter_team_ids"`
	FilterProjectIDs []string `json:"filter_project_ids"`
	FilterLabelNames []string `json:"filter_label_names"`
	TriggerState    string    `json:"trigger_state"`
	Status          string    `json:"status"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// ClaudeConfig represents Claude Code configuration for an org (or per-user override)
type ClaudeConfig struct {
	ID               string  `json:"id"`
	OrgID            string  `json:"org_id"`
	UserID           string  `json:"user_id,omitempty"`
	AnthropicAPIKey  string  `json:"-"` // never serialize
	APIKeyMasked     string  `json:"api_key_masked,omitempty"` // computed: "sk-ant-...•••"
	Model            string  `json:"model"`
	MaxTurns         int     `json:"max_turns"`
	AllowedTools     []string `json:"allowed_tools"`
	MaxCostPerTask   float64 `json:"max_cost_per_task,omitempty"`
	MaxTokensPerTask int     `json:"max_tokens_per_task"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// MaskAPIKey returns a masked version of an API key
func MaskAPIKey(key string) string {
	if len(key) < 12 {
		return "•••"
	}
	return key[:8] + "•••" + key[len(key)-4:]
}

// AgentTask represents a task being worked on by Claude Code
type AgentTask struct {
	ID               string     `json:"id"`
	OrgID            string     `json:"org_id"`
	LinearIssueID    string     `json:"linear_issue_id,omitempty"`
	LinearIdentifier string     `json:"linear_identifier,omitempty"`
	LinearURL        string     `json:"linear_url,omitempty"`
	Title            string     `json:"title"`
	Description      string     `json:"description,omitempty"`
	Prompt           string     `json:"prompt,omitempty"`
	EnvironmentID    string     `json:"environment_id,omitempty"`
	Branch           string     `json:"branch,omitempty"`
	RepoFullName     string     `json:"repo_full_name,omitempty"`
	Status           string     `json:"status"` // pending, queued, running, complete, failed, cancelled
	OutputSummary    string     `json:"output_summary,omitempty"`
	OutputJSON       map[string]interface{} `json:"output_json,omitempty"`
	CommitSHA        string     `json:"commit_sha,omitempty"`
	PRURL            string     `json:"pr_url,omitempty"`
	ErrorMessage     string     `json:"error_message,omitempty"`
	StartedAt        *time.Time `json:"started_at,omitempty"`
	CompletedAt      *time.Time `json:"completed_at,omitempty"`
	DurationSeconds  int        `json:"duration_seconds,omitempty"`
	TokensUsed       int        `json:"tokens_used,omitempty"`
	EstimatedCost    float64    `json:"estimated_cost,omitempty"`
	RetryCount       int        `json:"retry_count"`
	MaxRetries       int        `json:"max_retries"`
	ContextSaved     bool       `json:"context_saved"`
	SnapshotTaken    bool       `json:"snapshot_taken"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

// TaskLogEntry is a step in the task execution log
type TaskLogEntry struct {
	ID        string                 `json:"id"`
	TaskID    string                 `json:"task_id"`
	Step      string                 `json:"step"`
	Status    string                 `json:"status"` // started, completed, failed
	Message   string                 `json:"message,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

// TaskSettings holds per-org task execution preferences
type TaskSettings struct {
	OrgID              string `json:"org_id"`
	InstanceStrategy   string `json:"instance_strategy"`   // one_per_task, shared_branch, single_instance, auto
	MaxConcurrentTasks int    `json:"max_concurrent_tasks"`
	DefaultEnvSize     string `json:"default_env_size"`
	DefaultEnvProvider string `json:"default_env_provider"`
	DefaultEnvRegion   string `json:"default_env_region"`
	AutoCreatePR       bool   `json:"auto_create_pr"`
	PRBaseBranch       string `json:"pr_base_branch"`
	AutoDestroyEnv     bool   `json:"auto_destroy_env"`
	EnvTTLMinutes      int    `json:"env_ttl_minutes"`
}
