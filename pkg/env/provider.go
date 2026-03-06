package env

import (
	"context"
	"time"

	"github.com/gradient/gradient/internal/models"
)

// Provider is the interface for all cloud providers (container-first).
// Every provider (Hetzner, AWS, GCP, Azure, bare-metal, etc.) must implement this.
// Provider-specific details are hidden behind this interface so the rest of the
// codebase never references a concrete provider type.
type Provider interface {
	// CreateEnvironment launches a cloud server with a Docker container and returns a provider ref.
	CreateEnvironment(ctx context.Context, config *ProviderConfig) (string, error)
	// DestroyEnvironment tears down the container + instance.
	DestroyEnvironment(ctx context.Context, providerRef string) error
	// GetEnvironmentStatus returns the current status of the environment.
	GetEnvironmentStatus(ctx context.Context, providerRef string) (string, error)
}

// Snapshotter is an optional interface for providers that support container snapshots.
type Snapshotter interface {
	// SnapshotEnvironment takes a container commit snapshot, returns image ref.
	SnapshotEnvironment(ctx context.Context, providerRef string, tag string) (string, error)
	// RestoreFromSnapshot creates a new env from a snapshot image, returns provider ref.
	RestoreFromSnapshot(ctx context.Context, snapshotRef string, config *ProviderConfig) (string, error)
}

// HybridSnapshotter extends Snapshotter with server-level and container-export snapshots
// for maximum reliability. docker commit is fast but flaky with open files/processes.
// Server-level snapshots (Hetzner Image API, AWS AMI, etc.) are slower but capture everything.
// docker export captures the full filesystem as a tar (more reliable than commit for running containers).
type HybridSnapshotter interface {
	Snapshotter
	// ServerSnapshot creates a cloud-provider-level image snapshot of the entire server.
	// More reliable than docker commit — captures everything including system-level changes.
	// Returns the provider-specific image/snapshot ID.
	ServerSnapshot(ctx context.Context, providerRef string, description string) (string, error)
	// ExportContainer runs `docker export` to capture the full filesystem as a tar,
	// then imports + pushes to registry. More reliable than `docker commit` for running containers.
	ExportContainer(ctx context.Context, providerRef string, tag string) (string, error)
}

// AsHybridSnapshotter attempts to extract the HybridSnapshotter interface from a provider.
func AsHybridSnapshotter(p Provider) (HybridSnapshotter, bool) {
	hs, ok := p.(HybridSnapshotter)
	return hs, ok
}

// RemoteExecutor is an optional interface for providers that support executing
// commands on running instances (SSH, SSM, exec, etc.). Any provider that exposes
// remote command execution implements this — the caller never needs to know
// whether it's SSH (Hetzner/bare-metal), SSM (AWS), or gcloud ssh (GCP).
type RemoteExecutor interface {
	// ExecCommand runs a command on the instance identified by providerRef.
	// Returns stdout+stderr and any error. Timeout controls max execution time.
	ExecCommand(ctx context.Context, providerRef string, command string, timeout time.Duration) (string, error)
	// WaitForReady blocks until the instance is accepting commands (SSH up, SSM agent running, etc.)
	WaitForReady(ctx context.Context, providerRef string, timeout time.Duration) error
}

// NetworkInfo is an optional interface for providers that can return network
// details about a running instance (IP address, hostname, etc.).
type NetworkInfo interface {
	// GetServerIP returns the public IP address (or hostname) for the instance.
	GetServerIP(ctx context.Context, providerRef string) (string, error)
}

// ProviderConfig contains all config needed to create an environment.
// This struct is provider-neutral — each provider picks the fields it needs.
type ProviderConfig struct {
	Name        string
	Region      string
	Size        string
	Resources   models.ResourceSpec
	SnapshotRef string // If restoring from a snapshot, the image URI

	// Per-org registry override (enterprise isolation).
	// If set, the agent pushes snapshots here instead of the platform default.
	// If empty, the provider falls back to its own registryURL from construction.
	RegistryURL  string
	RegistryUser string
	RegistryPass string

	// Agent configuration (passed to gradient-agent via cloud-init / userdata / startup script)
	EnvID         string // Environment ID for API calls & mesh scoping
	OrgID         string // Organization ID
	Branch        string // Git branch (for Live Context Mesh scoping)
	APIURL        string // Gradient API URL (e.g. https://api.gradient.dev)
	AuthToken     string // Auth token for API calls
	NATSUrl       string // NATS server URL for Live Context Mesh
	NATSAuthToken string // NATS auth token
}

// ProviderName returns a human-readable name for a provider.
// Used in logs and error messages without hardcoding provider names.
func ProviderName(p Provider) string {
	switch p.(type) {
	default:
		return "unknown"
	}
}

// AsRemoteExecutor attempts to extract the RemoteExecutor interface from a provider.
// Returns (executor, true) if the provider supports it, (nil, false) otherwise.
func AsRemoteExecutor(p Provider) (RemoteExecutor, bool) {
	re, ok := p.(RemoteExecutor)
	return re, ok
}

// AsNetworkInfo attempts to extract the NetworkInfo interface from a provider.
func AsNetworkInfo(p Provider) (NetworkInfo, bool) {
	ni, ok := p.(NetworkInfo)
	return ni, ok
}

// AsSnapshotter attempts to extract the Snapshotter interface from a provider.
func AsSnapshotter(p Provider) (Snapshotter, bool) {
	s, ok := p.(Snapshotter)
	return s, ok
}
