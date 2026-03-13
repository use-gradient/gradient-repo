package services

import (
	"math"
	"testing"

	"github.com/gradient/gradient/internal/models"
)

func TestCreditsForDuration(t *testing.T) {
	tests := []struct {
		name          string
		size          string
		billedSeconds int
		wantCredits   int64
	}{
		{name: "small one minute minimum", size: "small", billedSeconds: 1, wantCredits: 1},
		{name: "small ninety seconds", size: "small", billedSeconds: 90, wantCredits: 2},
		{name: "medium one hour", size: "medium", billedSeconds: 3600, wantCredits: 180},
		{name: "large one hour", size: "large", billedSeconds: 3600, wantCredits: 300},
		{name: "gpu one hour", size: "gpu", billedSeconds: 3600, wantCredits: 1440},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := creditsForDuration(tt.size, tt.billedSeconds)
			if got != tt.wantCredits {
				t.Fatalf("creditsForDuration(%s, %d) = %d, want %d", tt.size, tt.billedSeconds, got, tt.wantCredits)
			}
		})
	}
}

func TestCreditPricingDefaults(t *testing.T) {
	svc := NewBillingService(nil, "", "", "", "", "", 10)
	pricing, err := svc.getCreditPricing(nil)
	if err != nil {
		t.Fatalf("getCreditPricing returned error: %v", err)
	}
	if pricing.PackageCredits != DefaultCreditPackageCredits {
		t.Fatalf("PackageCredits = %d, want %d", pricing.PackageCredits, DefaultCreditPackageCredits)
	}
	if math.Abs(pricing.USDPerCredit-0.003) > 0.000001 {
		t.Fatalf("USDPerCredit = %f, want 0.003", pricing.USDPerCredit)
	}
}

func TestFreeTrialCredits(t *testing.T) {
	pricing := creditPricing{
		PackageAmountCents: 300,
		PackageCredits:     1000,
		USDPerCredit:       0.003,
	}
	got := pricing.FreeTrialCredits(10)
	if got != 3333 {
		t.Fatalf("FreeTrialCredits(10) = %d, want 3333", got)
	}
}

func TestBillingServiceCreation(t *testing.T) {
	t.Run("disabled without stripe key", func(t *testing.T) {
		svc := NewBillingService(nil, "", "price_credit", "gradient_credits", "stripe_customer_id", "credits", 10)
		if svc.enabled {
			t.Error("Expected billing to be disabled without Stripe key")
		}
		if svc.StripeConfigured() {
			t.Error("Expected StripeConfigured() to return false without Stripe key")
		}
	})

	t.Run("enabled with stripe key", func(t *testing.T) {
		svc := NewBillingService(nil, "sk_test_abc123", "price_credit", "gradient_credits", "stripe_customer_id", "credits", 10)
		if !svc.enabled {
			t.Error("Expected billing to be enabled with Stripe key")
		}
		if !svc.StripeConfigured() {
			t.Error("Expected StripeConfigured() to return true with Stripe key")
		}
	})
}

func TestStripeRequiredForBillingOps(t *testing.T) {
	svc := NewBillingService(nil, "", "price_credit", "gradient_credits", "stripe_customer_id", "credits", 10)

	t.Run("CheckBillingAllowed errors without Stripe", func(t *testing.T) {
		err := svc.CheckBillingAllowed(nil, "org_test", "small")
		if err == nil {
			t.Error("Expected CheckBillingAllowed to fail when Stripe is not configured")
		}
	})

	t.Run("ReportUsageToStripe errors without Stripe", func(t *testing.T) {
		err := svc.ReportUsageToStripe(nil, "org_test", "env_test", "small", 3600)
		if err == nil {
			t.Error("Expected ReportUsageToStripe to fail when Stripe is not configured")
		}
	})
}

func TestBillingConstants(t *testing.T) {
	if DefaultFreeTrialCreditUSD != 10.0 {
		t.Errorf("DefaultFreeTrialCreditUSD should be 10.0, got %f", DefaultFreeTrialCreditUSD)
	}
	if MinBilledSeconds != 60 {
		t.Errorf("MinBilledSeconds should be 60, got %d", MinBilledSeconds)
	}
}

func TestBillingStatusModel(t *testing.T) {
	status := models.BillingStatus{
		OrgID:             "org_123",
		Tier:              "free",
		HasPaymentMethod:  false,
		FreeCreditsUsed:   100,
		FreeCreditsLimit:  3333,
		FreeCreditsLeft:   3233,
		FreeTrialValueUSD: 10,
		CanCreateEnv:      true,
		AllowedSizes:      []string{"small", "medium", "large", "gpu"},
		Month:             "2026-03",
	}

	if !status.CanCreateEnv {
		t.Error("Expected CanCreateEnv to be true for trial credits remaining")
	}
	if status.FreeCreditsLeft != 3233 {
		t.Errorf("Expected FreeCreditsLeft 3233, got %d", status.FreeCreditsLeft)
	}
	if len(status.AllowedSizes) != 4 {
		t.Errorf("Expected all sizes to be allowed, got %v", status.AllowedSizes)
	}
}

func TestUsageSummaryModel(t *testing.T) {
	summary := models.UsageSummary{
		OrgID:            "org_123",
		Month:            "2026-03",
		TotalHours:       10.5,
		TotalCost:        4.5,
		TotalCredits:     1500,
		IncludedCredits:  3333,
		BillableCredits:  0,
		IncludedValueUSD: 10,
	}

	if summary.TotalCredits != 1500 {
		t.Errorf("Expected TotalCredits 1500, got %d", summary.TotalCredits)
	}
	if summary.OrgID != "org_123" {
		t.Errorf("Expected OrgID 'org_123', got %q", summary.OrgID)
	}
}
