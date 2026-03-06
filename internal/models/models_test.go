package models

import (
	"testing"
)

func TestSizeToResources(t *testing.T) {
	tests := []struct {
		name     string
		size     string
		wantCPU  string
		wantMem  string
	}{
		{"small (default)", "small", "2", "4Gi"},
		{"medium", "medium", "4", "8Gi"},
		{"large", "large", "8", "16Gi"},
		{"gpu", "gpu", "8", "16Gi"},
		{"unknown defaults to small", "unknown", "2", "4Gi"},
		{"empty defaults to small", "", "2", "4Gi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SizeToResources(tt.size)
			if got.CPU != tt.wantCPU {
				t.Errorf("SizeToResources(%q).CPU = %q, want %q", tt.size, got.CPU, tt.wantCPU)
			}
			if got.Memory != tt.wantMem {
				t.Errorf("SizeToResources(%q).Memory = %q, want %q", tt.size, got.Memory, tt.wantMem)
			}
		})
	}
}

func TestSizeToHourlyRate(t *testing.T) {
	tests := []struct {
		size     string
		wantRate float64
	}{
		{"small", 0.15},
		{"medium", 0.35},
		{"large", 0.70},
		{"gpu", 3.50},
		{"unknown", 0.15},
		{"", 0.15},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := SizeToHourlyRate(tt.size)
			if got != tt.wantRate {
				t.Errorf("SizeToHourlyRate(%q) = %f, want %f", tt.size, got, tt.wantRate)
			}
		})
	}
}

func TestSizeToEC2InstanceType(t *testing.T) {
	tests := []struct {
		size         string
		wantInstance string
	}{
		{"small", "t3.medium"},
		{"medium", "t3.xlarge"},
		{"large", "t3.2xlarge"},
		{"gpu", "g4dn.xlarge"},
		{"unknown", "t3.medium"},
		{"", "t3.medium"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := SizeToEC2InstanceType(tt.size)
			if got != tt.wantInstance {
				t.Errorf("SizeToEC2InstanceType(%q) = %q, want %q", tt.size, got, tt.wantInstance)
			}
		})
	}
}

func TestSizeToHourlyRateConsistency(t *testing.T) {
	// Verify that the billing rates used in billing_service.go match models
	sizes := []string{"small", "medium", "large", "gpu"}
	expectedRates := map[string]float64{
		"small":  0.15,
		"medium": 0.35,
		"large":  0.70,
		"gpu":    3.50,
	}

	for _, size := range sizes {
		got := SizeToHourlyRate(size)
		want := expectedRates[size]
		if got != want {
			t.Errorf("Rate mismatch for size %q: got %f, want %f", size, got, want)
		}
	}
}

func TestEnvironmentDefaults(t *testing.T) {
	env := Environment{}

	// Verify zero values are safe defaults
	if env.Status != "" {
		t.Errorf("Expected empty default status, got %q", env.Status)
	}
	if env.DestroyedAt != nil {
		t.Errorf("Expected nil DestroyedAt, got %v", env.DestroyedAt)
	}
}

func TestSizeToHetznerServerType(t *testing.T) {
	tests := []struct {
		size       string
		wantServer string
	}{
		{"small", "cx22"},
		{"medium", "cx32"},
		{"large", "cx42"},
		{"gpu", "cx52"},
		{"unknown", "cx22"},
		{"", "cx22"},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := SizeToHetznerServerType(tt.size)
			if got != tt.wantServer {
				t.Errorf("SizeToHetznerServerType(%q) = %q, want %q", tt.size, got, tt.wantServer)
			}
		})
	}
}

func TestSizeToHetznerHourlyRate(t *testing.T) {
	tests := []struct {
		size     string
		wantRate float64
	}{
		{"small", 0.010},
		{"medium", 0.020},
		{"large", 0.039},
		{"gpu", 0.078},
		{"unknown", 0.010},
		{"", 0.010},
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := SizeToHetznerHourlyRate(tt.size)
			if got != tt.wantRate {
				t.Errorf("SizeToHetznerHourlyRate(%q) = %f, want %f", tt.size, got, tt.wantRate)
			}
		})
	}
}

func TestSnapshotDefaults(t *testing.T) {
	snap := Snapshot{}

	if snap.SizeBytes != 0 {
		t.Errorf("Expected zero SizeBytes, got %d", snap.SizeBytes)
	}
}

// Tests for the provider-agnostic size mapping registry

func TestSizeToMachineType(t *testing.T) {
	tests := []struct {
		provider string
		size     string
		want     string
	}{
		{"hetzner", "small", "cx22"},
		{"hetzner", "large", "cx42"},
		{"aws", "small", "t3.medium"},
		{"aws", "gpu", "g4dn.xlarge"},
		{"unknown-provider", "small", "small"}, // fallback
	}

	for _, tt := range tests {
		t.Run(tt.provider+"/"+tt.size, func(t *testing.T) {
			got := SizeToMachineType(tt.provider, tt.size)
			if got != tt.want {
				t.Errorf("SizeToMachineType(%q, %q) = %q, want %q", tt.provider, tt.size, got, tt.want)
			}
		})
	}
}

func TestHourlyRate(t *testing.T) {
	// Hetzner rates
	if got := HourlyRate("hetzner", "small"); got != 0.010 {
		t.Errorf("HourlyRate(hetzner, small) = %f, want 0.010", got)
	}
	// AWS rates
	if got := HourlyRate("aws", "small"); got != 0.15 {
		t.Errorf("HourlyRate(aws, small) = %f, want 0.15", got)
	}
	// Unknown provider falls back to generic SizeToHourlyRate
	if got := HourlyRate("gcp", "small"); got != 0.15 {
		t.Errorf("HourlyRate(gcp, small) = %f, want 0.15 (fallback)", got)
	}
}

func TestRegisterProvider(t *testing.T) {
	// Register a new provider
	RegisterProvider("test-cloud", map[string]string{
		"small": "tc-small",
		"large": "tc-large",
	}, map[string]float64{
		"small": 0.05,
		"large": 0.20,
	})

	if got := SizeToMachineType("test-cloud", "small"); got != "tc-small" {
		t.Errorf("Expected tc-small, got %q", got)
	}
	if got := HourlyRate("test-cloud", "large"); got != 0.20 {
		t.Errorf("Expected 0.20, got %f", got)
	}

	// Cleanup
	delete(ProviderSizeMap, "test-cloud")
	delete(ProviderHourlyRate, "test-cloud")
}

func TestSupportedProviders(t *testing.T) {
	providers := SupportedProviders()
	if len(providers) < 2 {
		t.Errorf("Expected at least 2 built-in providers (hetzner, aws), got %d: %v", len(providers), providers)
	}
}
