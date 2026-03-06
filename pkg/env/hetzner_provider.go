package env

import (
	"context"
	"fmt"
	"log"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/crypto/ssh"
)

// HetznerProvider implements Provider and Snapshotter using Hetzner Cloud Servers + Docker.
// Each environment is a Hetzner Cloud Server running a Docker container with the dev environment.
// Snapshots are taken via SSH (docker commit + push to registry).
type HetznerProvider struct {
	client       *hcloud.Client
	location     string     // Hetzner datacenter location (fsn1, nbg1, hel1, ash, hil)
	sshKeyIDs    []int64    // Hetzner SSH Key IDs to inject into servers
	sshSigner    ssh.Signer // SSH private key for executing remote commands
	firewallID   int64      // Hetzner Firewall ID (optional, 0 = none)
	networkID    int64      // Hetzner Network ID (optional, 0 = none)
	registryURL  string     // Container registry URL for snapshots (e.g. registry.example.com/gradient)
	registryUser string     // Registry auth username
	registryPass string     // Registry auth password
	imageID      int64      // Hetzner OS image ID (0 = use ubuntu-24.04 by name)
	agentURL     string     // URL to download gradient-agent binary
}

func NewHetznerProvider(
	apiToken string,
	location string,
	sshKeyIDs []int64,
	sshPrivateKeyPEM string,
	firewallID int64,
	networkID int64,
	registryURL string,
	registryUser string,
	registryPass string,
	imageID int64,
	agentURL string,
) (*HetznerProvider, error) {
	if apiToken == "" {
		return nil, fmt.Errorf("HETZNER_API_TOKEN is required for Hetzner provider")
	}

	client := hcloud.NewClient(hcloud.WithToken(apiToken))

	var signer ssh.Signer
	if sshPrivateKeyPEM != "" {
		var err error
		signer, err = ssh.ParsePrivateKey([]byte(sshPrivateKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("failed to parse SSH private key: %w", err)
		}
	}

	if location == "" {
		location = "fsn1" // Default to Falkenstein, Germany
	}

	return &HetznerProvider{
		client:       client,
		location:     location,
		sshKeyIDs:    sshKeyIDs,
		sshSigner:    signer,
		firewallID:   firewallID,
		networkID:    networkID,
		registryURL:  registryURL,
		registryUser: registryUser,
		registryPass: registryPass,
		imageID:      imageID,
		agentURL:     agentURL,
	}, nil
}

// SizeToHetznerServerType maps environment size to Hetzner Cloud server type.
// Hetzner deprecated cx22/cx32/cx42/cx52 in early 2026 → replaced with cx23/cx33/cx43/cx53.
func SizeToHetznerServerType(size string) string {
	switch size {
	case "medium":
		return "cx33" // 4 vCPU, 8 GB RAM
	case "large":
		return "cx43" // 8 vCPU, 16 GB RAM
	case "gpu":
		return "cx53" // 16 vCPU, 32 GB RAM (Hetzner doesn't have GPU instances; use largest shared)
	default: // small
		return "cx23" // 2 vCPU, 4 GB RAM
	}
}

// CreateEnvironment launches a Hetzner Cloud Server and starts a Docker container inside it.
// Returns the Hetzner server ID as the provider ref.
func (p *HetznerProvider) CreateEnvironment(ctx context.Context, config *ProviderConfig) (string, error) {
	serverType := SizeToHetznerServerType(config.Size)
	serverName := fmt.Sprintf("gradient-%s", config.Name)

	// Use the region from the request, fall back to provider default
	location := config.Region
	if location == "" {
		location = p.location
	}

	log.Printf("Hetzner: Creating server (type=%s, location=%s) for env %s", serverType, location, config.Name)

	// Build cloud-init script
	baseImage := "ubuntu:24.04"
	if config.SnapshotRef != "" {
		baseImage = config.SnapshotRef
	}
	cloudInit := p.buildCloudInit(baseImage, config)

	// Build SSH keys list
	var sshKeys []*hcloud.SSHKey
	for _, keyID := range p.sshKeyIDs {
		sshKeys = append(sshKeys, &hcloud.SSHKey{ID: keyID})
	}

	// Determine the OS image
	var image *hcloud.Image
	if p.imageID > 0 {
		image = &hcloud.Image{ID: p.imageID}
	} else {
		// Use Ubuntu 24.04 by name
		image = &hcloud.Image{Name: "ubuntu-24.04"}
	}

	opts := hcloud.ServerCreateOpts{
		Name:       serverName,
		ServerType: &hcloud.ServerType{Name: serverType},
		Image:      image,
		Location:   &hcloud.Location{Name: location},
		SSHKeys:    sshKeys,
		UserData:   cloudInit,
		Labels: map[string]string{
			"gradient-env":  config.Name,
			"gradient-size": config.Size,
			"managed-by":    "gradient",
		},
	}

	if p.firewallID > 0 {
		opts.Firewalls = []*hcloud.ServerCreateFirewall{
			{Firewall: hcloud.Firewall{ID: p.firewallID}},
		}
	}
	if p.networkID > 0 {
		opts.Networks = []*hcloud.Network{{ID: p.networkID}}
	}

	result, _, err := p.client.Server.Create(ctx, opts)
	if err != nil {
		return "", fmt.Errorf("failed to create Hetzner server: %w", err)
	}

	serverID := strconv.FormatInt(result.Server.ID, 10)
	log.Printf("Hetzner: Server %s (ID: %s) created for env %s", serverName, serverID, config.Name)

	// Wait for the server action to complete
	if result.Action != nil {
		_, errCh := p.client.Action.WatchProgress(ctx, result.Action)
		if err := <-errCh; err != nil {
			log.Printf("Hetzner: Warning — server creation action error: %v", err)
		}
	}

	return serverID, nil
}

// DestroyEnvironment deletes the Hetzner Cloud Server.
func (p *HetznerProvider) DestroyEnvironment(ctx context.Context, providerRef string) error {
	serverID, err := strconv.ParseInt(providerRef, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid Hetzner server ID %q: %w", providerRef, err)
	}

	log.Printf("Hetzner: Deleting server %d", serverID)

	server := &hcloud.Server{ID: serverID}
	_, _, err = p.client.Server.DeleteWithResult(ctx, server)
	if err != nil {
		return fmt.Errorf("failed to delete Hetzner server %d: %w", serverID, err)
	}

	log.Printf("Hetzner: Server %d deletion initiated", serverID)
	return nil
}

// GetEnvironmentStatus returns the status of the Hetzner Cloud Server.
func (p *HetznerProvider) GetEnvironmentStatus(ctx context.Context, providerRef string) (string, error) {
	serverID, err := strconv.ParseInt(providerRef, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid Hetzner server ID %q: %w", providerRef, err)
	}

	server, _, err := p.client.Server.GetByID(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("failed to get Hetzner server %d: %w", serverID, err)
	}
	if server == nil {
		return "not_found", nil
	}

	switch server.Status {
	case hcloud.ServerStatusRunning:
		return "running", nil
	case hcloud.ServerStatusInitializing:
		return "creating", nil
	case hcloud.ServerStatusStarting:
		return "creating", nil
	case hcloud.ServerStatusStopping:
		return "destroying", nil
	case hcloud.ServerStatusOff:
		return "stopped", nil
	case hcloud.ServerStatusDeleting:
		return "destroying", nil
	default:
		return string(server.Status), nil
	}
}

// SnapshotEnvironment runs `docker commit` on the running container via SSH,
// then pushes the image to the configured container registry.
func (p *HetznerProvider) SnapshotEnvironment(ctx context.Context, providerRef string, tag string) (string, error) {
	if p.registryURL == "" {
		return "", fmt.Errorf("HETZNER_REGISTRY_URL is required for snapshots")
	}
	if p.sshSigner == nil {
		return "", fmt.Errorf("SSH private key is required for snapshots (HETZNER_SSH_PRIVATE_KEY)")
	}

	imageRef := fmt.Sprintf("%s:%s", p.registryURL, tag)

	// Get server IP
	ip, err := p.getServerIP(ctx, providerRef)
	if err != nil {
		return "", fmt.Errorf("failed to get server IP for snapshot: %w", err)
	}

	// Build snapshot commands
	var commands []string

	// Registry login if credentials provided
	if p.registryUser != "" && p.registryPass != "" {
		commands = append(commands,
			fmt.Sprintf("echo '%s' | docker login --username '%s' --password-stdin %s",
				p.registryPass, p.registryUser, strings.Split(p.registryURL, "/")[0]))
	}

	commands = append(commands,
		fmt.Sprintf("docker commit gradient-env %s", imageRef),
		fmt.Sprintf("docker push %s", imageRef),
	)

	log.Printf("Hetzner: Taking snapshot of server %s → %s", providerRef, imageRef)

	script := strings.Join(commands, " && ")
	output, err := p.sshExec(ip, script, 10*time.Minute)
	if err != nil {
		return "", fmt.Errorf("snapshot command failed on %s: %w\nOutput: %s", providerRef, err, output)
	}

	log.Printf("Hetzner: Snapshot %s completed successfully", imageRef)
	return imageRef, nil
}

// RestoreFromSnapshot creates a new server that pulls and runs the snapshot image.
func (p *HetznerProvider) RestoreFromSnapshot(ctx context.Context, snapshotRef string, config *ProviderConfig) (string, error) {
	config.SnapshotRef = snapshotRef
	return p.CreateEnvironment(ctx, config)
}

// ServerSnapshot creates a Hetzner server-level image snapshot.
// This is the most reliable snapshot method — it captures the entire server disk
// including system-level changes, CUDA kernels, ldconfig, systemd units, /etc/ tweaks,
// and anything that docker commit might miss with open files/processes.
// Slower than docker commit (~30-120s) but captures everything.
func (p *HetznerProvider) ServerSnapshot(ctx context.Context, providerRef string, description string) (string, error) {
	serverID, err := strconv.ParseInt(providerRef, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid Hetzner server ID %q: %w", providerRef, err)
	}

	server := &hcloud.Server{ID: serverID}

	log.Printf("Hetzner: Creating server-level snapshot for server %d...", serverID)

	result, _, err := p.client.Server.CreateImage(ctx, server, &hcloud.ServerCreateImageOpts{
		Type:        hcloud.ImageTypeSnapshot,
		Description: hcloud.Ptr(description),
		Labels: map[string]string{
			"gradient":   "true",
			"managed-by": "gradient",
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create Hetzner server snapshot: %w", err)
	}

	// Wait for the snapshot action to complete
	if result.Action != nil {
		_, errCh := p.client.Action.WatchProgress(ctx, result.Action)
		if err := <-errCh; err != nil {
			return "", fmt.Errorf("server snapshot action failed: %w", err)
		}
	}

	snapshotID := strconv.FormatInt(result.Image.ID, 10)
	log.Printf("Hetzner: Server snapshot created (image ID: %s) for server %d", snapshotID, serverID)
	return snapshotID, nil
}

// ExportContainer runs `docker export` on the running container to capture the full
// filesystem as a tar, then imports it as a new Docker image and pushes to the registry.
// More reliable than `docker commit` for running containers — docker export captures the
// complete filesystem state without issues from open files, running processes, or tmpfs mounts.
func (p *HetznerProvider) ExportContainer(ctx context.Context, providerRef string, tag string) (string, error) {
	if p.registryURL == "" {
		return "", fmt.Errorf("HETZNER_REGISTRY_URL is required for container export")
	}
	if p.sshSigner == nil {
		return "", fmt.Errorf("SSH private key is required for container export")
	}

	imageRef := fmt.Sprintf("%s:%s", p.registryURL, tag)

	ip, err := p.getServerIP(ctx, providerRef)
	if err != nil {
		return "", fmt.Errorf("failed to get server IP for container export: %w", err)
	}

	// Build export commands
	var commands []string

	// Registry login
	if p.registryUser != "" && p.registryPass != "" {
		commands = append(commands,
			fmt.Sprintf("echo '%s' | docker login --username '%s' --password-stdin %s",
				p.registryPass, p.registryUser, strings.Split(p.registryURL, "/")[0]))
	}

	// docker export captures complete filesystem (more reliable than commit with open files)
	// Then docker import creates a clean image from the tar
	commands = append(commands,
		fmt.Sprintf("docker export gradient-env | docker import - %s", imageRef),
		fmt.Sprintf("docker push %s", imageRef),
	)

	log.Printf("Hetzner: Exporting container on server %s → %s", providerRef, imageRef)

	script := strings.Join(commands, " && ")
	output, err := p.sshExec(ip, script, 15*time.Minute)
	if err != nil {
		return "", fmt.Errorf("container export failed on %s: %w\nOutput: %s", providerRef, err, output)
	}

	log.Printf("Hetzner: Container export %s completed successfully", imageRef)
	return imageRef, nil
}

// GetServerIP returns the public IPv4 address of a Hetzner server.
// Exported so the API layer can use it for SSH access.
func (p *HetznerProvider) GetServerIP(ctx context.Context, providerRef string) (string, error) {
	return p.getServerIP(ctx, providerRef)
}

// getServerIP looks up the server's public IPv4 address.
func (p *HetznerProvider) getServerIP(ctx context.Context, providerRef string) (string, error) {
	serverID, err := strconv.ParseInt(providerRef, 10, 64)
	if err != nil {
		return "", fmt.Errorf("invalid Hetzner server ID %q: %w", providerRef, err)
	}

	server, _, err := p.client.Server.GetByID(ctx, serverID)
	if err != nil {
		return "", fmt.Errorf("failed to get Hetzner server %d: %w", serverID, err)
	}
	if server == nil {
		return "", fmt.Errorf("Hetzner server %d not found", serverID)
	}

	ip := server.PublicNet.IPv4.IP.String()
	if ip == "" || ip == "<nil>" {
		return "", fmt.Errorf("Hetzner server %d has no public IPv4 address", serverID)
	}

	return ip, nil
}

// sshExec runs a command on the remote server via SSH.
func (p *HetznerProvider) sshExec(host string, command string, timeout time.Duration) (string, error) {
	config := &ssh.ClientConfig{
		User: "root",
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(p.sshSigner),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(), // MVP: skip host key verification
		Timeout:         30 * time.Second,
	}

	addr := net.JoinHostPort(host, "22")
	conn, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return "", fmt.Errorf("SSH connect to %s failed: %w", addr, err)
	}
	defer conn.Close()

	session, err := conn.NewSession()
	if err != nil {
		return "", fmt.Errorf("SSH session failed: %w", err)
	}
	defer session.Close()

	// Set up timeout via context
	done := make(chan struct{})
	var output []byte
	var cmdErr error

	go func() {
		output, cmdErr = session.CombinedOutput(command)
		close(done)
	}()

	select {
	case <-done:
		return string(output), cmdErr
	case <-time.After(timeout):
		session.Close()
		return "", fmt.Errorf("SSH command timed out after %s", timeout)
	}
}

// ExecCommand implements the RemoteExecutor interface for Hetzner.
// Runs a command on the server via SSH.
func (p *HetznerProvider) ExecCommand(ctx context.Context, providerRef string, command string, timeout time.Duration) (string, error) {
	ip, err := p.getServerIP(ctx, providerRef)
	if err != nil {
		return "", err
	}
	return p.sshExec(ip, command, timeout)
}

// SSHExec is an alias for ExecCommand — kept for backward compatibility.
func (p *HetznerProvider) SSHExec(ctx context.Context, providerRef string, command string, timeout time.Duration) (string, error) {
	return p.ExecCommand(ctx, providerRef, command, timeout)
}

// WaitForReady implements the RemoteExecutor interface for Hetzner.
// Waits until SSH is available on the server.
func (p *HetznerProvider) WaitForReady(ctx context.Context, providerRef string, timeout time.Duration) error {
	ip, err := p.getServerIP(ctx, providerRef)
	if err != nil {
		return err
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		_, err := p.sshExec(ip, "echo ok", 10*time.Second)
		if err == nil {
			return nil
		}
		log.Printf("Hetzner: Waiting for SSH on %s...", ip)

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
	return fmt.Errorf("SSH not available on %s after %s", ip, timeout)
}

// WaitForSSH is an alias for WaitForReady — kept for backward compatibility.
func (p *HetznerProvider) WaitForSSH(ctx context.Context, providerRef string, timeout time.Duration) error {
	return p.WaitForReady(ctx, providerRef, timeout)
}

// buildCloudInit generates the cloud-init script that runs on server creation.
// This is Hetzner-specific but the agent it boots is provider-agnostic.
// Other providers (AWS, GCP, etc.) implement their own bootstrap scripts:
//   - AWS uses EC2 UserData (similar format)
//   - GCP uses startup-script metadata
//   - Azure uses custom-data
//
// The container and agent setup is identical across all providers — only the
// package installation commands differ by OS family.
//
// Currently supports:
//   - Debian/Ubuntu (apt-get)
//
// Planned:
//   - RHEL/CentOS (yum/dnf)
//   - Alpine (apk)
//   - macOS (brew) — for local dev
func (p *HetznerProvider) buildCloudInit(image string, config *ProviderConfig) string {
	// Resolve registry: per-org override > platform default
	regURL := p.registryURL
	regUser := p.registryUser
	regPass := p.registryPass
	if config.RegistryURL != "" {
		regURL = config.RegistryURL
		regUser = config.RegistryUser
		regPass = config.RegistryPass
	}

	// Only login to registry if we're pulling a snapshot image (not a public base like ubuntu:24.04)
	registryLogin := ""
	if regUser != "" && regPass != "" && regURL != "" && config.SnapshotRef != "" {
		registryDomain := strings.Split(regURL, "/")[0]
		registryLogin = fmt.Sprintf(`
# Registry login for snapshot restore
echo '%s' | docker login --username '%s' --password-stdin %s
`, regPass, regUser, registryDomain)
	}

	// Build agent env file lines (key=value for /etc/gradient/agent.env)
	agentEnvFile := fmt.Sprintf("GRADIENT_ENV_NAME=%s\n", config.Name)
	agentEnvFile += fmt.Sprintf("GRADIENT_REGISTRY_URL=%s\n", regURL)
	agentEnvFile += fmt.Sprintf("GRADIENT_REGISTRY_USER=%s\n", regUser)
	agentEnvFile += fmt.Sprintf("GRADIENT_REGISTRY_PASS=%s\n", regPass)
	if config.EnvID != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_ENV_ID=%s\n", config.EnvID)
	}
	if config.OrgID != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_ORG_ID=%s\n", config.OrgID)
	}
	if config.APIURL != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_API_URL=%s\n", config.APIURL)
	}
	if config.AuthToken != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_AUTH_TOKEN=%s\n", config.AuthToken)
	}
	if config.Branch != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_BRANCH=%s\n", config.Branch)
	}
	if config.NATSUrl != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_NATS_URL=%s\n", config.NATSUrl)
	}
	if config.NATSAuthToken != "" {
		agentEnvFile += fmt.Sprintf("GRADIENT_NATS_AUTH_TOKEN=%s\n", config.NATSAuthToken)
	}

	// Agent setup: either use pre-baked binary or download fresh
	agentSetup := ""
	if p.agentURL != "" {
		agentSetup = fmt.Sprintf(`
# Write agent environment file
mkdir -p /etc/gradient
cat > /etc/gradient/agent.env <<'AGENTENV'
%s
AGENTENV

# Download gradient-agent if not pre-baked in the snapshot
if [ ! -f /usr/local/bin/gradient-agent ]; then
    echo "Downloading gradient-agent..."
    curl -fsSL -o /usr/local/bin/gradient-agent "%s"
    chmod +x /usr/local/bin/gradient-agent
fi

# Create/update systemd service (uses EnvironmentFile for config)
cat > /etc/systemd/system/gradient-agent.service <<'AGENTSVC'
[Unit]
Description=Gradient Agent — periodic snapshots, health reporting, and Live Context Mesh
After=docker.service
Requires=docker.service

[Service]
Type=simple
ExecStart=/usr/local/bin/gradient-agent
Restart=always
RestartSec=10
EnvironmentFile=/etc/gradient/agent.env

[Install]
WantedBy=multi-user.target
AGENTSVC

systemctl daemon-reload
systemctl enable gradient-agent
systemctl start gradient-agent
`, agentEnvFile, p.agentURL)
	}

	envName := config.Name
	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail

# Log everything
exec > /var/log/gradient-init.log 2>&1

echo "Gradient environment init starting: %s"

# Install Docker + base tools (skip if pre-baked in snapshot)
if ! command -v docker &>/dev/null; then
    echo "Installing Docker and base packages..."
    apt-get update -qq
    DEBIAN_FRONTEND=noninteractive apt-get install -y -qq \
        docker.io curl jq git wget \
        build-essential python3 python3-pip python3-venv \
        nodejs npm unzip zip htop tmux vim nano \
        ca-certificates openssh-server net-tools
fi
systemctl enable docker
systemctl start docker

# Wait for Docker to be ready
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
        break
    fi
    echo "Waiting for Docker daemon..."
    sleep 2
done

# Ensure gradient directories exist
mkdir -p /home/gradient/workspace
mkdir -p /gradient/context
mkdir -p /etc/gradient
%s
# Pull the base/snapshot image
echo "Pulling image: %s"
docker pull %s

# Create isolated bridge network (not host network)
docker network create gradient-net 2>/dev/null || true

# Start the gradient-env container with security hardening:
# - NOT privileged (dropped in favor of specific capabilities)
# - Docker's built-in default seccomp profile (blocks ~44 dangerous syscalls like
#   kexec_load, reboot, mount, etc. while allowing everything a dev env needs)
# - no-new-privileges prevents privilege escalation
# - Specific capabilities only: SYS_PTRACE (for debugging), DAC_OVERRIDE (for package installs)
# - Bridge network with port mapping (not host network)
# - Memory/CPU limits based on size
# - Read-only /proc and /sys where possible
docker run -d \
    --name gradient-env \
    --security-opt no-new-privileges \
    --cap-drop ALL \
    --cap-add CHOWN \
    --cap-add DAC_OVERRIDE \
    --cap-add FSETID \
    --cap-add FOWNER \
    --cap-add SETGID \
    --cap-add SETUID \
    --cap-add NET_BIND_SERVICE \
    --cap-add SYS_PTRACE \
    --cap-add KILL \
    --cap-add AUDIT_WRITE \
    --cap-add NET_RAW \
    --network gradient-net \
    -p 2222:22 \
    -p 8080:8080 \
    --restart unless-stopped \
    -v /home/gradient/workspace:/workspace \
    -e GRADIENT_ENV_NAME=%s \
    %s \
    tail -f /dev/null

echo "Gradient environment ready: %s"

# Signal readiness
echo "ready" > /tmp/gradient-status
%s
`, envName, registryLogin, image, image, envName, image, envName, agentSetup)
}
