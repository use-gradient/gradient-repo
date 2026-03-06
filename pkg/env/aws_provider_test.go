package env

import (
	"testing"

	"github.com/gradient/gradient/internal/models"
)

func TestNewAWSProviderValidation(t *testing.T) {
	tests := []struct {
		name      string
		amiID     string
		sgID      string
		subnetID  string
		wantError string
	}{
		{
			name:      "missing AMI ID",
			amiID:     "",
			sgID:      "sg-123",
			subnetID:  "subnet-123",
			wantError: "AWS_AMI_ID is required for EC2 provider",
		},
		{
			name:      "missing security group",
			amiID:     "ami-123",
			sgID:      "",
			subnetID:  "subnet-123",
			wantError: "AWS_SECURITY_GROUP_ID is required for EC2 provider",
		},
		{
			name:      "missing subnet",
			amiID:     "ami-123",
			sgID:      "sg-123",
			subnetID:  "",
			wantError: "AWS_SUBNET_ID is required for EC2 provider",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAWSProvider("us-east-1", tt.amiID, tt.sgID, tt.subnetID, "", "", "")
			if err == nil {
				t.Fatal("Expected error, got nil")
			}
			if err.Error() != tt.wantError {
				t.Errorf("Expected error %q, got %q", tt.wantError, err.Error())
			}
		})
	}
}

func TestEC2InstanceTypeMapping(t *testing.T) {
	// Verify instance type mapping is consistent
	tests := []struct {
		size         string
		wantInstance string
	}{
		{"small", "t3.medium"},
		{"medium", "t3.xlarge"},
		{"large", "t3.2xlarge"},
		{"gpu", "g4dn.xlarge"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := models.SizeToEC2InstanceType(tt.size)
			if got != tt.wantInstance {
				t.Errorf("SizeToEC2InstanceType(%q) = %q, want %q", tt.size, got, tt.wantInstance)
			}
		})
	}
}

func TestProviderInterface(t *testing.T) {
	// Verify AWSProvider implements Provider and Snapshotter interfaces
	var _ Provider = (*AWSProvider)(nil)
	var _ Snapshotter = (*AWSProvider)(nil)
}
