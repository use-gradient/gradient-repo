package env

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/gradient/gradient/internal/models"
)

// AWSProvider implements Provider and Snapshotter using EC2 + Docker containers.
// Each environment is an EC2 instance running a Docker container with the dev environment.
type AWSProvider struct {
	ec2Client       *ec2.Client
	ssmClient       *ssm.Client
	region          string
	amiID           string // Pre-baked AMI with Docker, SSM agent, gradient-agent
	securityGroupID string
	subnetID        string
	keyPairName     string
	ecrRepoURI      string // ECR repo for container snapshots (e.g. 123456789.dkr.ecr.us-east-1.amazonaws.com/gradient-envs)
	instanceProfile string // IAM instance profile with SSM + ECR permissions
}

func NewAWSProvider(region, amiID, sgID, subnetID, keyPair, ecrRepoURI, instanceProfile string) (*AWSProvider, error) {
	if amiID == "" {
		return nil, fmt.Errorf("AWS_AMI_ID is required for EC2 provider")
	}
	if sgID == "" {
		return nil, fmt.Errorf("AWS_SECURITY_GROUP_ID is required for EC2 provider")
	}
	if subnetID == "" {
		return nil, fmt.Errorf("AWS_SUBNET_ID is required for EC2 provider")
	}

	cfg, err := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion(region),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	return &AWSProvider{
		ec2Client:       ec2.NewFromConfig(cfg),
		ssmClient:       ssm.NewFromConfig(cfg),
		region:          region,
		amiID:           amiID,
		securityGroupID: sgID,
		subnetID:        subnetID,
		keyPairName:     keyPair,
		ecrRepoURI:      ecrRepoURI,
		instanceProfile: instanceProfile,
	}, nil
}

// CreateEnvironment launches an EC2 instance and starts a Docker container inside it.
// The pre-baked AMI has Docker pre-installed. UserData pulls the base image and starts the container.
// Returns the EC2 instance ID as the provider ref.
func (p *AWSProvider) CreateEnvironment(ctx context.Context, config *ProviderConfig) (string, error) {
	instanceType := models.SizeToEC2InstanceType(config.Size)
	log.Printf("AWS: Creating EC2 instance (type=%s, AMI=%s) for env %s", instanceType, p.amiID, config.Name)

	// Build user data script that starts the Docker container
	baseImage := "ubuntu:24.04"
	if config.SnapshotRef != "" {
		baseImage = config.SnapshotRef // Restore from snapshot
	}
	userData := p.buildUserData(baseImage, config.Name)
	encodedUserData := base64.StdEncoding.EncodeToString([]byte(userData))

	input := &ec2.RunInstancesInput{
		ImageId:      aws.String(p.amiID),
		InstanceType: ec2types.InstanceType(instanceType),
		MinCount:     aws.Int32(1),
		MaxCount:     aws.Int32(1),
		UserData:     aws.String(encodedUserData),
		TagSpecifications: []ec2types.TagSpecification{
			{
				ResourceType: ec2types.ResourceTypeInstance,
				Tags: []ec2types.Tag{
					{Key: aws.String("Name"), Value: aws.String(fmt.Sprintf("gradient-%s", config.Name))},
					{Key: aws.String("gradient-env"), Value: aws.String(config.Name)},
					{Key: aws.String("gradient-size"), Value: aws.String(config.Size)},
				},
			},
		},
		SecurityGroupIds: []string{p.securityGroupID},
		SubnetId:         aws.String(p.subnetID),
	}

	if p.keyPairName != "" {
		input.KeyName = aws.String(p.keyPairName)
	}
	if p.instanceProfile != "" {
		input.IamInstanceProfile = &ec2types.IamInstanceProfileSpecification{
			Name: aws.String(p.instanceProfile),
		}
	}

	result, err := p.ec2Client.RunInstances(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to launch EC2 instance: %w", err)
	}

	if len(result.Instances) == 0 {
		return "", fmt.Errorf("EC2 RunInstances returned no instances")
	}

	instanceID := *result.Instances[0].InstanceId
	log.Printf("AWS: EC2 instance %s launched for env %s", instanceID, config.Name)
	return instanceID, nil
}

// DestroyEnvironment terminates the EC2 instance.
func (p *AWSProvider) DestroyEnvironment(ctx context.Context, providerRef string) error {
	log.Printf("AWS: Terminating EC2 instance %s", providerRef)
	_, err := p.ec2Client.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
		InstanceIds: []string{providerRef},
	})
	if err != nil {
		return fmt.Errorf("failed to terminate EC2 instance %s: %w", providerRef, err)
	}
	log.Printf("AWS: EC2 instance %s termination initiated", providerRef)
	return nil
}

// GetEnvironmentStatus returns the status of the EC2 instance.
func (p *AWSProvider) GetEnvironmentStatus(ctx context.Context, providerRef string) (string, error) {
	result, err := p.ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{providerRef},
	})
	if err != nil {
		return "", fmt.Errorf("failed to describe EC2 instance %s: %w", providerRef, err)
	}

	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return "not_found", nil
	}

	instance := result.Reservations[0].Instances[0]
	switch instance.State.Name {
	case ec2types.InstanceStateNameRunning:
		return "running", nil
	case ec2types.InstanceStateNamePending:
		return "creating", nil
	case ec2types.InstanceStateNameTerminated:
		return "destroyed", nil
	case ec2types.InstanceStateNameStopped:
		return "stopped", nil
	case ec2types.InstanceStateNameShuttingDown:
		return "destroying", nil
	default:
		return string(instance.State.Name), nil
	}
}

// SnapshotEnvironment runs `docker commit` on the running container via SSM,
// then pushes the image to ECR. Returns the full ECR image ref.
func (p *AWSProvider) SnapshotEnvironment(ctx context.Context, providerRef string, tag string) (string, error) {
	if p.ecrRepoURI == "" {
		return "", fmt.Errorf("AWS_ECR_REPO_URI is required for snapshots")
	}

	imageRef := fmt.Sprintf("%s:%s", p.ecrRepoURI, tag)
	ecrDomain := strings.Split(p.ecrRepoURI, "/")[0]

	// SSM command: docker commit the running container, login to ECR, push the image
	commands := []string{
		fmt.Sprintf("aws ecr get-login-password --region %s | docker login --username AWS --password-stdin %s", p.region, ecrDomain),
		fmt.Sprintf("docker commit gradient-env %s", imageRef),
		fmt.Sprintf("docker push %s", imageRef),
	}

	log.Printf("AWS: Taking snapshot of instance %s → %s", providerRef, imageRef)

	sendOutput, err := p.ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{providerRef},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands":         commands,
			"executionTimeout": {"600"},
		},
		Comment: aws.String(fmt.Sprintf("Gradient snapshot: %s", tag)),
	})
	if err != nil {
		return "", fmt.Errorf("failed to send snapshot command to %s: %w", providerRef, err)
	}

	commandID := *sendOutput.Command.CommandId

	// Poll for completion (up to 10 minutes for large containers)
	deadline := time.Now().Add(10 * time.Minute)
	for time.Now().Before(deadline) {
		time.Sleep(5 * time.Second)

		invocation, err := p.ssmClient.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(providerRef),
		})
		if err != nil {
			// InvocationDoesNotExist means SSM hasn't registered it yet
			continue
		}

		switch invocation.Status {
		case ssmtypes.CommandInvocationStatusSuccess:
			log.Printf("AWS: Snapshot %s completed successfully", imageRef)
			return imageRef, nil
		case ssmtypes.CommandInvocationStatusFailed:
			errContent := ""
			if invocation.StandardErrorContent != nil {
				errContent = *invocation.StandardErrorContent
			}
			return "", fmt.Errorf("snapshot command failed on %s: %s", providerRef, errContent)
		case ssmtypes.CommandInvocationStatusCancelled:
			return "", fmt.Errorf("snapshot command was cancelled on %s", providerRef)
		case ssmtypes.CommandInvocationStatusTimedOut:
			return "", fmt.Errorf("snapshot command timed out on %s", providerRef)
			// InProgress, Pending — keep waiting
		}
	}

	return "", fmt.Errorf("snapshot command timed out after 10 minutes on %s", providerRef)
}

// RestoreFromSnapshot launches a new EC2 instance that pulls and runs the snapshot image.
func (p *AWSProvider) RestoreFromSnapshot(ctx context.Context, snapshotRef string, config *ProviderConfig) (string, error) {
	config.SnapshotRef = snapshotRef
	return p.CreateEnvironment(ctx, config)
}

// GetServerIP returns the public IP of the EC2 instance.
func (p *AWSProvider) GetServerIP(ctx context.Context, providerRef string) (string, error) {
	result, err := p.ec2Client.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{providerRef},
	})
	if err != nil {
		return "", err
	}
	if len(result.Reservations) == 0 || len(result.Reservations[0].Instances) == 0 {
		return "", fmt.Errorf("instance %s not found", providerRef)
	}
	inst := result.Reservations[0].Instances[0]
	if inst.PublicIpAddress != nil {
		return *inst.PublicIpAddress, nil
	}
	if inst.PrivateIpAddress != nil {
		return *inst.PrivateIpAddress, nil
	}
	return "", nil
}

// ExecCommand runs a command on the EC2 instance via SSM SendCommand and waits for the result.
func (p *AWSProvider) ExecCommand(ctx context.Context, providerRef string, command string, timeout time.Duration) (string, error) {
	timeoutSec := int(timeout.Seconds())
	if timeoutSec < 10 {
		timeoutSec = 10
	}

	sendOutput, err := p.ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
		InstanceIds:  []string{providerRef},
		DocumentName: aws.String("AWS-RunShellScript"),
		Parameters: map[string][]string{
			"commands":         {command},
			"executionTimeout": {fmt.Sprintf("%d", timeoutSec)},
		},
		Comment: aws.String("gradient-exec"),
	})
	if err != nil {
		return "", fmt.Errorf("SSM SendCommand failed on %s: %w", providerRef, err)
	}

	commandID := *sendOutput.Command.CommandId
	deadline := time.Now().Add(timeout + 30*time.Second)

	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)

		invocation, err := p.ssmClient.GetCommandInvocation(ctx, &ssm.GetCommandInvocationInput{
			CommandId:  aws.String(commandID),
			InstanceId: aws.String(providerRef),
		})
		if err != nil {
			continue
		}

		switch invocation.Status {
		case ssmtypes.CommandInvocationStatusSuccess:
			output := ""
			if invocation.StandardOutputContent != nil {
				output = *invocation.StandardOutputContent
			}
			return output, nil
		case ssmtypes.CommandInvocationStatusFailed:
			stderr := ""
			if invocation.StandardErrorContent != nil {
				stderr = *invocation.StandardErrorContent
			}
			stdout := ""
			if invocation.StandardOutputContent != nil {
				stdout = *invocation.StandardOutputContent
			}
			return stdout, fmt.Errorf("command failed on %s: %s", providerRef, stderr)
		case ssmtypes.CommandInvocationStatusCancelled:
			return "", fmt.Errorf("command cancelled on %s", providerRef)
		case ssmtypes.CommandInvocationStatusTimedOut:
			return "", fmt.Errorf("command timed out on %s", providerRef)
		}
	}

	return "", fmt.Errorf("command polling timed out on %s (commandID=%s)", providerRef, commandID)
}

// WaitForReady blocks until the EC2 instance is running and the SSM agent is responding.
func (p *AWSProvider) WaitForReady(ctx context.Context, providerRef string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)

	// Phase 1: wait for instance to be running
	for time.Now().Before(deadline) {
		status, err := p.GetEnvironmentStatus(ctx, providerRef)
		if err == nil && status == "running" {
			break
		}
		time.Sleep(3 * time.Second)
	}

	// Phase 2: wait for SSM agent to respond (cloud-init may still be running)
	for time.Now().Before(deadline) {
		_, err := p.ssmClient.SendCommand(ctx, &ssm.SendCommandInput{
			InstanceIds:  []string{providerRef},
			DocumentName: aws.String("AWS-RunShellScript"),
			Parameters: map[string][]string{
				"commands":         {"echo ready"},
				"executionTimeout": {"10"},
			},
			Comment: aws.String("gradient-readiness-check"),
		})
		if err == nil {
			// SSM accepted the command — agent is running
			log.Printf("AWS: Instance %s SSM agent is ready", providerRef)
			return nil
		}
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("instance %s did not become SSM-ready within %v", providerRef, timeout)
}

// buildUserData generates the cloud-init script that runs on EC2 instance launch.
// It pulls the specified Docker image and starts the gradient-env container.
func (p *AWSProvider) buildUserData(image string, envName string) string {
	ecrLogin := ""
	if p.ecrRepoURI != "" {
		ecrDomain := strings.Split(p.ecrRepoURI, "/")[0]
		ecrLogin = fmt.Sprintf("aws ecr get-login-password --region %s | docker login --username AWS --password-stdin %s\n", p.region, ecrDomain)
	}

	return fmt.Sprintf(`#!/bin/bash
set -euo pipefail

exec > /var/log/gradient-init.log 2>&1
echo "Gradient environment init starting: %s"

# ECS-optimized AMI: Docker + AWS CLI + SSM agent are pre-installed.
# Just wait for Docker daemon to be ready.
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then break; fi
    echo "Waiting for Docker daemon..."
    sleep 2
done

# Install git if not present (needed for repo cloning inside container)
if ! command -v git &>/dev/null; then
    yum install -y -q git 2>/dev/null || dnf install -y -q git 2>/dev/null || true
fi

# ECR login if needed (for snapshot restore)
%s

echo "Pulling image: %s"
docker pull %s || echo "Pull failed, continuing with local image"

docker run -d \
    --name gradient-env \
    --privileged \
    --network host \
    --restart unless-stopped \
    -v /home/gradient/workspace:/workspace \
    -e GRADIENT_ENV_NAME=%s \
    %s \
    tail -f /dev/null

# Install essential tools inside the container
docker exec gradient-env bash -c '
    apt-get update -qq && apt-get install -y -qq git curl nodejs npm 2>/dev/null || \
    (yum install -y -q git curl nodejs npm 2>/dev/null) || true
    npm install -g @anthropic-ai/claude-code 2>/dev/null || echo "Claude CLI install deferred"
' || echo "Container tool install deferred"

echo "Gradient environment ready: %s"
echo "ready" > /tmp/gradient-status
`, envName, ecrLogin, image, image, envName, image, envName)
}
