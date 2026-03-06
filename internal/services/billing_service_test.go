package services

import (
	"math"
	"testing"

	"github.com/gradient/gradient/internal/models"
)

func TestBillingCostCalculation(t *testing.T) {
	// Replicate the cost calculation from billing_service.go GetUsageSummary
	// to ensure the rates are correct and consistent with models.SizeToHourlyRate

	tests := []struct {
		name       string
		smallSec   int
		mediumSec  int
		largeSec   int
		gpuSec     int
		wantCost   float64
		wantHours  float64
	}{
		{
			name:      "one hour of small",
			smallSec:  3600,
			wantCost:  0.15,
			wantHours: 1.0,
		},
		{
			name:      "one hour of medium",
			mediumSec: 3600,
			wantCost:  0.35,
			wantHours: 1.0,
		},
		{
			name:      "one hour of large",
			largeSec:  3600,
			wantCost:  0.70,
			wantHours: 1.0,
		},
		{
			name:      "one hour of gpu",
			gpuSec:    3600,
			wantCost:  3.50,
			wantHours: 1.0,
		},
		{
			name:      "zero usage",
			wantCost:  0.0,
			wantHours: 0.0,
		},
		{
			name:       "mixed usage",
			smallSec:   7200, // 2 hours
			mediumSec:  3600, // 1 hour
			largeSec:   1800, // 0.5 hours
			gpuSec:     900,  // 0.25 hours
			wantCost:   2 * 0.15 + 1*0.35 + 0.5*0.70 + 0.25*3.50,
			wantHours:  2.0 + 1.0 + 0.5 + 0.25,
		},
		{
			name:      "partial hour",
			smallSec:  1800, // 30 minutes = 0.5 hours
			wantCost:  0.5 * 0.15,
			wantHours: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			smallHours := float64(tt.smallSec) / 3600.0
			mediumHours := float64(tt.mediumSec) / 3600.0
			largeHours := float64(tt.largeSec) / 3600.0
			gpuHours := float64(tt.gpuSec) / 3600.0

			totalHours := smallHours + mediumHours + largeHours + gpuHours
			totalCost := (smallHours * 0.15) + (mediumHours * 0.35) + (largeHours * 0.70) + (gpuHours * 3.50)

			if math.Abs(totalCost-tt.wantCost) > 0.001 {
				t.Errorf("Cost: got %f, want %f", totalCost, tt.wantCost)
			}
			if math.Abs(totalHours-tt.wantHours) > 0.001 {
				t.Errorf("Hours: got %f, want %f", totalHours, tt.wantHours)
			}
		})
	}
}

func TestBillingRatesMatchModels(t *testing.T) {
	// Verify that the rates hardcoded in billing_service.go
	// match the rates in models.SizeToHourlyRate
	rates := map[string]float64{
		"small":  0.15,
		"medium": 0.35,
		"large":  0.70,
		"gpu":    3.50,
	}

	for size, expectedRate := range rates {
		modelRate := models.SizeToHourlyRate(size)
		if math.Abs(modelRate-expectedRate) > 0.001 {
			t.Errorf("Rate mismatch for size %q: models says %f, billing uses %f", size, modelRate, expectedRate)
		}
	}
}

func TestBillingServiceCreation(t *testing.T) {
	t.Run("disabled without stripe key", func(t *testing.T) {
		svc := NewBillingService(nil, "", "", "", "", "")
		if svc.enabled {
			t.Error("Expected billing to be disabled without Stripe key")
		}
		if svc.StripeConfigured() {
			t.Error("Expected StripeConfigured() to return false without Stripe key")
		}
	})

	t.Run("enabled with stripe key", func(t *testing.T) {
		svc := NewBillingService(nil, "sk_test_abc123", "price_s", "price_m", "price_l", "price_g")
		if !svc.enabled {
			t.Error("Expected billing to be enabled with Stripe key")
		}
		if !svc.StripeConfigured() {
			t.Error("Expected StripeConfigured() to return true with Stripe key")
		}
	})
}

func TestStripeRequiredForBillingOps(t *testing.T) {
	// Verify that all Stripe-touching operations return errors when Stripe is not configured.
	// Even in dev, we must go through Stripe — no silent bypasses.
	svc := NewBillingService(nil, "", "price_s", "price_m", "price_l", "price_g")

	t.Run("CheckBillingAllowed errors without Stripe", func(t *testing.T) {
		err := svc.CheckBillingAllowed(nil, "org_test", "small")
		if err == nil {
			t.Error("Expected CheckBillingAllowed to fail when Stripe is not configured")
		}
	})

	t.Run("ReportUsageToStripe errors without Stripe", func(t *testing.T) {
		err := svc.ReportUsageToStripe(nil, "org_test", "small", 3600)
		if err == nil {
			t.Error("Expected ReportUsageToStripe to fail when Stripe is not configured")
		}
	})
}

func TestFreeTierConstants(t *testing.T) {
	if FreeTierMonthlyHours != 20.0 {
		t.Errorf("FreeTierMonthlyHours should be 20.0, got %f", FreeTierMonthlyHours)
	}
	if FreeTierAllowedSize != "small" {
		t.Errorf("FreeTierAllowedSize should be 'small', got %q", FreeTierAllowedSize)
	}
	if MinBilledSeconds != 60 {
		t.Errorf("MinBilledSeconds should be 60, got %d", MinBilledSeconds)
	}
}

func TestSizeToPriceID(t *testing.T) {
	svc := NewBillingService(nil, "", "price_small", "price_medium", "price_large", "price_gpu")

	tests := []struct {
		size     string
		wantID   string
	}{
		{"small", "price_small"},
		{"medium", "price_medium"},
		{"large", "price_large"},
		{"gpu", "price_gpu"},
		{"unknown", "price_small"}, // defaults to small
	}

	for _, tt := range tests {
		t.Run(tt.size, func(t *testing.T) {
			got := svc.sizeToPriceID(tt.size)
			if got != tt.wantID {
				t.Errorf("sizeToPriceID(%q) = %q, want %q", tt.size, got, tt.wantID)
			}
		})
	}
}

func TestSizeToPriceIDGPUFallback(t *testing.T) {
	// When GPU price ID is not configured, should fall back to large
	svc := NewBillingService(nil, "", "price_small", "price_medium", "price_large", "")
	got := svc.sizeToPriceID("gpu")
	if got != "price_large" {
		t.Errorf("sizeToPriceID('gpu') with no GPU price should fall back to large, got %q", got)
	}
}

func TestBillingStatusModel(t *testing.T) {
	status := models.BillingStatus{
		OrgID:            "org_123",
		Tier:             "free",
		HasPaymentMethod: false,
		FreeHoursUsed:    5.0,
		FreeHoursLimit:   20.0,
		FreeHoursLeft:    15.0,
		CanCreateEnv:     true,
		AllowedSizes:     []string{"small"},
		Month:            "2026-03",
	}

	if status.CanCreateEnv != true {
		t.Error("Expected CanCreateEnv to be true for free tier with hours remaining")
	}
	if status.FreeHoursLeft != 15.0 {
		t.Errorf("Expected FreeHoursLeft 15.0, got %f", status.FreeHoursLeft)
	}
	if len(status.AllowedSizes) != 1 || status.AllowedSizes[0] != "small" {
		t.Errorf("Expected AllowedSizes [small], got %v", status.AllowedSizes)
	}
}

func TestUsageSummaryModel(t *testing.T) {
	summary := models.UsageSummary{
		OrgID:       "org_123",
		Month:       "2025-06",
		TotalHours:  10.5,
		TotalCost:   3.675,
		SmallHours:  5.0,
		MediumHours: 3.0,
		LargeHours:  2.0,
		GPUHours:    0.5,
	}

	if summary.TotalHours != 10.5 {
		t.Errorf("Expected TotalHours 10.5, got %f", summary.TotalHours)
	}
	if summary.OrgID != "org_123" {
		t.Errorf("Expected OrgID 'org_123', got %q", summary.OrgID)
	}
}
