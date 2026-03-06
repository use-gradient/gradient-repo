package env

import (
	"context"
	"fmt"
	"log"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/google/uuid"
	"google.golang.org/api/option"
)

// GCPProvider implements Provider using GCE + Docker containers.
// NOTE: This provider is for v1.1 (month 2+). MVP launches with AWS only.
type GCPProvider struct {
	client    *container.ClusterManagerClient
	projectID string
	region    string
}

func NewGCPProvider(projectID, credentialsPath, region string) (*GCPProvider, error) {
	var opts []option.ClientOption
	if credentialsPath != "" {
		opts = append(opts, option.WithCredentialsFile(credentialsPath))
	}

	client, err := container.NewClusterManagerClient(context.Background(), opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create GKE client: %w", err)
	}

	return &GCPProvider{
		client:    client,
		projectID: projectID,
		region:    region,
	}, nil
}

// CreateEnvironment creates a GKE cluster (v1.1 — GCP support added in month 2)
func (p *GCPProvider) CreateEnvironment(ctx context.Context, config *ProviderConfig) (string, error) {
	clusterName := fmt.Sprintf("gradient-%s-%s", config.Name, uuid.New().String()[:8])
	zone := fmt.Sprintf("%s-a", p.region)
	log.Printf("GCP: Creating GKE cluster %s in project %s, zone %s", clusterName, p.projectID, zone)

	machineType := "e2-medium"
	switch config.Size {
	case "medium":
		machineType = "e2-highmem-4"
	case "large":
		machineType = "e2-highmem-8"
	case "gpu":
		machineType = "n1-standard-8"
	}

	req := &containerpb.CreateClusterRequest{
		ProjectId: p.projectID,
		Zone:      zone,
		Cluster: &containerpb.Cluster{
			Name:             clusterName,
			InitialNodeCount: 1,
			NodeConfig: &containerpb.NodeConfig{
				MachineType: machineType,
			},
		},
	}

	op, err := p.client.CreateCluster(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to create GKE cluster %s: %w", clusterName, err)
	}

	log.Printf("GCP: GKE cluster %s creation initiated. Operation: %s", clusterName, op.Name)
	return clusterName, nil
}

// DestroyEnvironment destroys the GCE instance / GKE cluster
func (p *GCPProvider) DestroyEnvironment(ctx context.Context, providerRef string) error {
	zone := fmt.Sprintf("%s-a", p.region)
	log.Printf("GCP: Destroying GKE cluster %s in project %s, zone %s", providerRef, p.projectID, zone)

	req := &containerpb.DeleteClusterRequest{
		ProjectId: p.projectID,
		Zone:      zone,
		ClusterId: providerRef,
	}

	op, err := p.client.DeleteCluster(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to delete GKE cluster %s: %w", providerRef, err)
	}
	log.Printf("GCP: GKE cluster %s deletion initiated. Operation: %s", providerRef, op.Name)
	return nil
}

// GetEnvironmentStatus returns the status of the GKE cluster
func (p *GCPProvider) GetEnvironmentStatus(ctx context.Context, providerRef string) (string, error) {
	zone := fmt.Sprintf("%s-a", p.region)
	req := &containerpb.GetClusterRequest{
		ProjectId: p.projectID,
		Zone:      zone,
		ClusterId: providerRef,
	}

	cluster, err := p.client.GetCluster(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get GKE cluster %s status: %w", providerRef, err)
	}
	if cluster == nil {
		return "not_found", nil
	}
	return cluster.Status.String(), nil
}
