package services

import (
	"context"
	"fmt"
	"log"
	"math"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/stripe/stripe-go/v76"
	"github.com/stripe/stripe-go/v76/billing/meterevent"
	portalsession "github.com/stripe/stripe-go/v76/billingportal/session"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/invoice"
	"github.com/stripe/stripe-go/v76/paymentmethod"
	priceclient "github.com/stripe/stripe-go/v76/price"
	"github.com/stripe/stripe-go/v76/subscription"
)

// Billing constants — free trial and credits
const (
	DefaultCreditPackageCredits = int64(1000)
	DefaultCreditPackageCents   = int64(300) // $3.00 per 1,000 credits
	DefaultFreeTrialCreditUSD   = 10.0
	MinBilledSeconds            = 60
)

// AllSizes lists all valid environment sizes
var AllSizes = []string{"small", "medium", "large", "gpu"}

var creditMultipliersBySize = map[string]int64{
	"small":  1,
	"medium": 3,
	"large":  5,
	"gpu":    24,
}

type BillingService struct {
	db                     *db.DB
	enabled                bool
	priceCreditsID         string
	creditMeterEventName   string
	creditMeterCustomerKey string
	creditMeterValueKey    string
	freeTrialCreditUSD     float64
}

func NewBillingService(database *db.DB, stripeKey, priceCreditsID, creditMeterEventName, creditMeterCustomerKey, creditMeterValueKey string, freeTrialCreditUSD float64) *BillingService {
	if stripeKey != "" {
		stripe.Key = stripeKey
	}
	if creditMeterEventName == "" {
		creditMeterEventName = "gradient_credits"
	}
	if creditMeterCustomerKey == "" {
		creditMeterCustomerKey = "stripe_customer_id"
	}
	if creditMeterValueKey == "" {
		creditMeterValueKey = "credits"
	}
	if freeTrialCreditUSD <= 0 {
		freeTrialCreditUSD = DefaultFreeTrialCreditUSD
	}
	return &BillingService{
		db:                     database,
		enabled:                stripeKey != "",
		priceCreditsID:         priceCreditsID,
		creditMeterEventName:   creditMeterEventName,
		creditMeterCustomerKey: creditMeterCustomerKey,
		creditMeterValueKey:    creditMeterValueKey,
		freeTrialCreditUSD:     freeTrialCreditUSD,
	}
}

// StripeConfigured returns whether Stripe is properly configured.
// Even in dev, Stripe must be configured (use test keys).
func (s *BillingService) StripeConfigured() bool {
	return s.enabled
}

// ─── Billing Gate ───────────────────────────────────────────────────────────
// CheckBillingAllowed is the main billing gate — call this BEFORE creating
// any environment. Returns nil if allowed, error if blocked.
//
// Rules:
//   - Stripe MUST be configured (even in dev — use test keys)
//   - Free users get a monthly included credit allowance worth freeTrialCreditUSD
//   - Once free credits are exhausted, a payment method is required to continue
//   - Paid tier (has payment method): any size, no free-tier gating
func (s *BillingService) CheckBillingAllowed(ctx context.Context, orgID, requestedSize string) error {
	if !s.enabled {
		return fmt.Errorf("billing system unavailable — Stripe is not configured (set STRIPE_SECRET_KEY)")
	}

	status, err := s.GetBillingStatus(ctx, orgID)
	if err != nil {
		// Fail-closed: if we can't determine billing status, block creation.
		// This prevents unbilled usage if DB is down or misconfigured.
		log.Printf("[billing] ERROR: could not determine billing status for org %s: %v — blocking creation", orgID, err)
		return fmt.Errorf("unable to verify billing status — please try again or contact support")
	}

	// Paid tier: allow everything
	if status.HasPaymentMethod {
		return nil
	}

	if status.FreeCreditsLeft <= 0 {
		return fmt.Errorf(
			"free trial credits exhausted: %d/%d credits used this month (about $%.2f included) — add a payment method to continue (gc billing setup)",
			status.FreeCreditsUsed, status.FreeCreditsLimit, status.FreeTrialValueUSD,
		)
	}

	return nil
}

// GetBillingStatus computes the full billing status for an org.
func (s *BillingService) GetBillingStatus(ctx context.Context, orgID string) (*models.BillingStatus, error) {
	month := time.Now().Format("2006-01")

	summary, err := s.GetUsageSummary(ctx, orgID, month)
	if err != nil {
		return nil, fmt.Errorf("failed to compute usage: %w", err)
	}

	// Check if org has a payment method (Stripe customer + subscription)
	hasPayment := s.HasPaymentMethod(ctx, orgID)

	// Determine tier
	tier := "free"
	settings, settingsErr := s.getOrgSettings(ctx, orgID)
	if settingsErr == nil && settings.BillingTier == "paid" {
		tier = "paid"
	}
	// If they have a payment method, they're effectively paid tier regardless of DB value
	if hasPayment {
		tier = "paid"
	}

	freeCreditsLimit := summary.IncludedCredits
	freeCreditsUsed := minInt64(summary.TotalCredits, summary.IncludedCredits)
	freeCreditsLeft := maxInt64(0, freeCreditsLimit-freeCreditsUsed)

	allowedSizes := AllSizes
	canCreate := hasPayment || freeCreditsLeft > 0

	return &models.BillingStatus{
		OrgID:             orgID,
		Tier:              tier,
		HasPaymentMethod:  hasPayment,
		StripeConfigured:  s.enabled,
		FreeHoursUsed:     float64(freeCreditsUsed) / 60.0,
		FreeHoursLimit:    float64(freeCreditsLimit) / 60.0,
		FreeHoursLeft:     float64(freeCreditsLeft) / 60.0,
		FreeCreditsUsed:   freeCreditsUsed,
		FreeCreditsLimit:  freeCreditsLimit,
		FreeCreditsLeft:   freeCreditsLeft,
		FreeTrialValueUSD: s.freeTrialCreditUSD,
		EstimatedCostUSD:  summary.TotalCost,
		CanCreateEnv:      canCreate,
		AllowedSizes:      allowedSizes,
		Month:             month,
	}, nil
}

// ─── Usage Tracking ─────────────────────────────────────────────────────────

// TrackUsageStart records the start of environment usage
func (s *BillingService) TrackUsageStart(ctx context.Context, envID, orgID, size string) error {
	query := `
		INSERT INTO usage_events (id, environment_id, org_id, size, started_at, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	id := uuid.New().String()
	now := time.Now()
	_, err := s.db.Pool.Exec(ctx, query, id, envID, orgID, size, now, now)
	return err
}

// TrackUsageStop records the end of environment usage and reports to Stripe.
// Enforces minimum billing of 1 minute (60 seconds).
func (s *BillingService) TrackUsageStop(ctx context.Context, envID string) error {
	// First, get the usage event info before updating
	var orgID, size string
	var startedAt time.Time
	err := s.db.Pool.QueryRow(ctx,
		`SELECT org_id, size, started_at FROM usage_events WHERE environment_id = $1 AND stopped_at IS NULL LIMIT 1`,
		envID,
	).Scan(&orgID, &size, &startedAt)

	// Update the usage event with minimum 1 minute billing
	query := `
		UPDATE usage_events
		SET stopped_at = NOW(),
		    billed_seconds = GREATEST(EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER, $2)
		WHERE environment_id = $1 AND stopped_at IS NULL
	`
	_, updateErr := s.db.Pool.Exec(ctx, query, envID, MinBilledSeconds)

	// Report usage to Stripe (if we found the event and they have a subscription)
	if err == nil {
		billedSeconds := int(time.Since(startedAt).Seconds())
		if billedSeconds < MinBilledSeconds {
			billedSeconds = MinBilledSeconds
		}
		if reportErr := s.ReportUsageToStripe(ctx, orgID, envID, size, billedSeconds); reportErr != nil {
			// Log as ERROR — Stripe must be configured. If this fires, billing is broken.
			log.Printf("[billing] ERROR: Failed to report usage to Stripe for org %s: %v", orgID, reportErr)
		}
	}

	return updateErr
}

// GetUsageSummary calculates usage summary for an org in a given month.
// Includes both completed and currently-running sessions.
func (s *BillingService) GetUsageSummary(ctx context.Context, orgID, month string) (*models.UsageSummary, error) {
	smallSec, mediumSec, largeSec, gpuSec, err := s.getUsageSecondsBySize(ctx, orgID, month)
	if err != nil {
		return nil, err
	}

	smallHours := float64(smallSec) / 3600.0
	mediumHours := float64(mediumSec) / 3600.0
	largeHours := float64(largeSec) / 3600.0
	gpuHours := float64(gpuSec) / 3600.0
	totalHours := smallHours + mediumHours + largeHours + gpuHours

	totalCredits := creditsForDuration("small", smallSec) +
		creditsForDuration("medium", mediumSec) +
		creditsForDuration("large", largeSec) +
		creditsForDuration("gpu", gpuSec)
	pricing, _ := s.getCreditPricing(ctx)
	includedCredits := pricing.FreeTrialCredits(s.freeTrialCreditUSD)
	if s.HasPaymentMethod(ctx, orgID) {
		includedCredits = 0
	}
	if includedCredits < 0 {
		includedCredits = 0
	}
	billableCredits := maxInt64(0, totalCredits-includedCredits)
	totalCost := pricing.EstimatedCostUSD(billableCredits)

	return &models.UsageSummary{
		OrgID:            orgID,
		Month:            month,
		TotalHours:       totalHours,
		TotalCost:        totalCost,
		SmallHours:       smallHours,
		MediumHours:      mediumHours,
		LargeHours:       largeHours,
		GPUHours:         gpuHours,
		TotalCredits:     totalCredits,
		IncludedCredits:  includedCredits,
		BillableCredits:  billableCredits,
		IncludedValueUSD: s.freeTrialCreditUSD,
	}, nil
}

// HasPaymentMethod checks if an org has a Stripe customer, subscription, AND an actual
// payment method (card) attached. A subscription without a payment method is not "active".
func (s *BillingService) HasPaymentMethod(ctx context.Context, orgID string) bool {
	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil {
		return false
	}
	if settings.StripeCustomerID == "" || settings.StripeSubscriptionID == "" {
		return false
	}
	if !s.enabled {
		return false
	}

	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(settings.StripeCustomerID),
		Type:     stripe.String("card"),
	}
	i := paymentmethod.List(params)
	return i.Next()
}

func (s *BillingService) getUsageSecondsBySize(ctx context.Context, orgID, month string) (int, int, int, int, error) {
	query := `
		SELECT
			COALESCE(SUM(CASE WHEN size = 'small' THEN billed_seconds ELSE 0 END), 0) as small_seconds,
			COALESCE(SUM(CASE WHEN size = 'medium' THEN billed_seconds ELSE 0 END), 0) as medium_seconds,
			COALESCE(SUM(CASE WHEN size = 'large' THEN billed_seconds ELSE 0 END), 0) as large_seconds,
			COALESCE(SUM(CASE WHEN size = 'gpu' THEN billed_seconds ELSE 0 END), 0) as gpu_seconds
		FROM usage_events
		WHERE org_id = $1
		  AND TO_CHAR(started_at, 'YYYY-MM') = $2
		  AND billed_seconds > 0
	`
	var smallSec, mediumSec, largeSec, gpuSec int
	if err := s.db.Pool.QueryRow(ctx, query, orgID, month).Scan(&smallSec, &mediumSec, &largeSec, &gpuSec); err != nil {
		return 0, 0, 0, 0, fmt.Errorf("failed to get usage summary: %w", err)
	}

	activeQuery := `
		SELECT
			COALESCE(SUM(CASE WHEN size = 'small' THEN EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN size = 'medium' THEN EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN size = 'large' THEN EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN size = 'gpu' THEN EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER ELSE 0 END), 0)
		FROM usage_events
		WHERE org_id = $1
		  AND TO_CHAR(started_at, 'YYYY-MM') = $2
		  AND stopped_at IS NULL
	`
	var activeSmall, activeMedium, activeLarge, activeGPU int
	if err := s.db.Pool.QueryRow(ctx, activeQuery, orgID, month).Scan(&activeSmall, &activeMedium, &activeLarge, &activeGPU); err == nil {
		smallSec += activeSmall
		mediumSec += activeMedium
		largeSec += activeLarge
		gpuSec += activeGPU
	}
	return smallSec, mediumSec, largeSec, gpuSec, nil
}

type creditPricing struct {
	PackageAmountCents int64
	PackageCredits     int64
	USDPerCredit       float64
}

func (p creditPricing) FreeTrialCredits(usd float64) int64 {
	if usd <= 0 || p.USDPerCredit <= 0 {
		return 0
	}
	return int64(math.Floor(usd / p.USDPerCredit))
}

func (p creditPricing) EstimatedCostUSD(credits int64) float64 {
	if credits <= 0 || p.USDPerCredit <= 0 {
		return 0
	}
	return float64(credits) * p.USDPerCredit
}

func (s *BillingService) getCreditPricing(ctx context.Context) (creditPricing, error) {
	pricing := creditPricing{
		PackageAmountCents: DefaultCreditPackageCents,
		PackageCredits:     DefaultCreditPackageCredits,
		USDPerCredit:       float64(DefaultCreditPackageCents) / 100.0 / float64(DefaultCreditPackageCredits),
	}
	if !s.enabled || s.priceCreditsID == "" {
		return pricing, nil
	}

	stripePrice, err := priceclient.Get(s.priceCreditsID, nil)
	if err != nil {
		return pricing, nil
	}

	packageCredits := int64(1)
	if stripePrice.TransformQuantity != nil && stripePrice.TransformQuantity.DivideBy > 0 {
		packageCredits = stripePrice.TransformQuantity.DivideBy
	}
	packageAmountCents := stripePrice.UnitAmount
	if packageAmountCents <= 0 && stripePrice.UnitAmountDecimal > 0 {
		packageAmountCents = int64(math.Round(stripePrice.UnitAmountDecimal))
	}
	if packageAmountCents <= 0 {
		packageAmountCents = DefaultCreditPackageCents
	}

	return creditPricing{
		PackageAmountCents: packageAmountCents,
		PackageCredits:     packageCredits,
		USDPerCredit:       float64(packageAmountCents) / 100.0 / float64(packageCredits),
	}, nil
}

func creditsForDuration(size string, billedSeconds int) int64 {
	if billedSeconds <= 0 {
		return 0
	}
	if billedSeconds < MinBilledSeconds {
		billedSeconds = MinBilledSeconds
	}
	minutes := int64((billedSeconds + 59) / 60)
	multiplier := creditMultiplier(size)
	return minutes * multiplier
}

func creditMultiplier(size string) int64 {
	if multiplier, ok := creditMultipliersBySize[size]; ok {
		return multiplier
	}
	return creditMultipliersBySize["small"]
}

// GetActiveUsageEvents returns all currently running usage events for an org
func (s *BillingService) GetActiveUsageEvents(ctx context.Context, orgID string) ([]*models.UsageEvent, error) {
	query := `
		SELECT id, environment_id, org_id, size, started_at, stopped_at, billed_seconds, created_at
		FROM usage_events
		WHERE org_id = $1 AND stopped_at IS NULL
		ORDER BY started_at DESC
	`

	rows, err := s.db.Pool.Query(ctx, query, orgID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active usage: %w", err)
	}
	defer rows.Close()

	var events []*models.UsageEvent
	for rows.Next() {
		var e models.UsageEvent
		err := rows.Scan(&e.ID, &e.EnvironmentID, &e.OrgID, &e.Size, &e.StartedAt, &e.StoppedAt, &e.BilledSeconds, &e.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan usage event: %w", err)
		}
		events = append(events, &e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating usage events: %w", err)
	}

	return events, nil
}

// --- Stripe Integration ---

// EnsureStripeCustomer creates a Stripe customer for an org if one doesn't exist.
// Stripe MUST be configured — even in dev, all billing goes through Stripe (use test keys).
func (s *BillingService) EnsureStripeCustomer(ctx context.Context, orgID, email, name string) (string, error) {
	// Check if we already have a Stripe customer ID
	existing, err := s.getOrgSettings(ctx, orgID)
	if err == nil && existing.StripeCustomerID != "" {
		return existing.StripeCustomerID, nil
	}

	if !s.enabled {
		return "", fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY (use Stripe test keys for development)")
	}

	// Create Stripe customer
	params := &stripe.CustomerParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	}
	params.AddMetadata("org_id", orgID)

	c, err := customer.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create Stripe customer: %w", err)
	}

	// Save to DB
	err = s.upsertOrgSettings(ctx, orgID, c.ID, "", email, "")
	if err != nil {
		return "", fmt.Errorf("failed to save Stripe customer ID: %w", err)
	}

	return c.ID, nil
}

// CreateMeteredSubscription creates a Stripe subscription with credit-based metered billing for an org.
// This sets up the usage-based credits price so billing meter events can be reported.
// Once a subscription is active, the org is automatically upgraded to "paid" tier.
// Stripe MUST be configured — even in dev, all billing goes through Stripe (use test keys).
func (s *BillingService) CreateMeteredSubscription(ctx context.Context, orgID string) (string, error) {
	if !s.enabled {
		return "", fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY (use Stripe test keys for development)")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeCustomerID == "" {
		return "", fmt.Errorf("no Stripe customer found for org %s — run billing setup first", orgID)
	}

	// If subscription already exists, return it
	if settings.StripeSubscriptionID != "" {
		return settings.StripeSubscriptionID, nil
	}

	if s.priceCreditsID == "" {
		return "", fmt.Errorf("no Stripe credits price configured — set STRIPE_PRICE_CREDITS_ID")
	}

	params := &stripe.SubscriptionParams{
		Customer: stripe.String(settings.StripeCustomerID),
		Items: []*stripe.SubscriptionItemsParams{{
			Price: stripe.String(s.priceCreditsID),
		}},
	}
	params.AddMetadata("org_id", orgID)

	sub, err := subscription.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create Stripe subscription: %w", err)
	}

	// Save subscription ID
	if err := s.upsertOrgSettings(ctx, orgID, "", sub.ID, "", ""); err != nil {
		log.Printf("[billing] Failed to save subscription ID: %v", err)
	}

	// Upgrade org to paid tier now that they have a subscription
	s.upgradeToPaid(ctx, orgID)

	log.Printf("[billing] Created Stripe subscription %s for org %s — upgraded to paid tier", sub.ID, orgID)
	return sub.ID, nil
}

// ReportUsageToStripe sends credit usage to Stripe as billing meter events.
// Called when an environment is stopped (TrackUsageStop).
func (s *BillingService) ReportUsageToStripe(ctx context.Context, orgID, envID, size string, billedSeconds int) error {
	if !s.enabled {
		return fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY (use Stripe test keys for development)")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeSubscriptionID == "" {
		// No subscription = trial-only org. Usage is tracked in the DB but not reported to Stripe.
		log.Printf("[billing] No subscription for org %s (trial-only), usage tracked in DB only", orgID)
		return nil
	}

	if settings.StripeCustomerID == "" {
		log.Printf("[billing] No Stripe customer found for org %s, skipping usage report", orgID)
		return nil
	}

	credits := creditsForDuration(size, billedSeconds)
	if credits <= 0 {
		return nil
	}

	params := &stripe.BillingMeterEventParams{
		EventName:  stripe.String(s.creditMeterEventName),
		Identifier: stripe.String(fmt.Sprintf("%s-%s-%s-%d", orgID, envID, size, time.Now().UnixNano())),
		Timestamp:  stripe.Int64(time.Now().Unix()),
		Payload: map[string]string{
			s.creditMeterCustomerKey: settings.StripeCustomerID,
			s.creditMeterValueKey:    strconv.FormatInt(credits, 10),
		},
	}

	_, err = meterevent.New(params)
	if err != nil {
		return fmt.Errorf("failed to report usage to Stripe: %w", err)
	}

	log.Printf("[billing] Reported %d credits (%d seconds, %s) to Stripe for org %s", credits, billedSeconds, size, orgID)
	return nil
}

// GetStripeInvoices lists invoices for an org from Stripe.
// Stripe MUST be configured — even in dev, all billing goes through Stripe (use test keys).
func (s *BillingService) GetStripeInvoices(ctx context.Context, orgID string) ([]*stripe.Invoice, error) {
	if !s.enabled {
		return nil, fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY (use Stripe test keys for development)")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeCustomerID == "" {
		// No Stripe customer = no invoices. Not an error for orgs still on included credits.
		return []*stripe.Invoice{}, nil
	}

	params := &stripe.InvoiceListParams{
		Customer: stripe.String(settings.StripeCustomerID),
	}
	params.Filters.AddFilter("limit", "", "20")

	var invoices []*stripe.Invoice
	i := invoice.List(params)
	for i.Next() {
		invoices = append(invoices, i.Invoice())
	}
	if err := i.Err(); err != nil {
		return nil, fmt.Errorf("failed to list invoices: %w", err)
	}

	return invoices, nil
}

// CreatePortalSession creates a Stripe Billing Portal session so the user can manage
// their payment method, view invoices, or cancel their subscription.
// If flow is "payment_method_update", it opens directly to the add/update payment method page.
func (s *BillingService) CreatePortalSession(ctx context.Context, orgID, returnURL, flow string) (string, error) {
	if !s.enabled {
		return "", fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeCustomerID == "" {
		return "", fmt.Errorf("no Stripe customer found for org %s — run billing setup first", orgID)
	}

	params := &stripe.BillingPortalSessionParams{
		Customer:  stripe.String(settings.StripeCustomerID),
		ReturnURL: stripe.String(returnURL),
	}

	if flow == "payment_method_update" {
		params.FlowData = &stripe.BillingPortalSessionFlowDataParams{
			Type: stripe.String(string(stripe.BillingPortalSessionFlowTypePaymentMethodUpdate)),
		}
	}

	session, err := portalsession.New(params)
	if err != nil {
		return "", fmt.Errorf("failed to create billing portal session: %w", err)
	}

	return session.URL, nil
}

// PaymentMethodInfo holds the display details of a payment method.
type PaymentMethodInfo struct {
	Brand    string `json:"brand"`     // "visa", "mastercard", etc.
	Last4    string `json:"last4"`     // last 4 digits
	ExpMonth int64  `json:"exp_month"` // expiration month
	ExpYear  int64  `json:"exp_year"`  // expiration year
}

// GetPaymentMethodInfo retrieves the default payment method details from Stripe.
func (s *BillingService) GetPaymentMethodInfo(ctx context.Context, orgID string) (*PaymentMethodInfo, error) {
	if !s.enabled {
		return nil, fmt.Errorf("Stripe is not configured")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeCustomerID == "" {
		return nil, nil // no customer = no payment method
	}

	// Get the Stripe customer to find their default payment method
	c, err := customer.Get(settings.StripeCustomerID, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get Stripe customer: %w", err)
	}

	// Check default payment method on customer
	if c.InvoiceSettings != nil && c.InvoiceSettings.DefaultPaymentMethod != nil {
		pm, err := paymentmethod.Get(c.InvoiceSettings.DefaultPaymentMethod.ID, nil)
		if err == nil && pm.Card != nil {
			return &PaymentMethodInfo{
				Brand:    string(pm.Card.Brand),
				Last4:    pm.Card.Last4,
				ExpMonth: int64(pm.Card.ExpMonth),
				ExpYear:  int64(pm.Card.ExpYear),
			}, nil
		}
	}

	// Fallback: list payment methods attached to the customer
	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(settings.StripeCustomerID),
		Type:     stripe.String("card"),
	}
	i := paymentmethod.List(params)
	if i.Next() {
		pm := i.PaymentMethod()
		if pm.Card != nil {
			return &PaymentMethodInfo{
				Brand:    string(pm.Card.Brand),
				Last4:    pm.Card.Last4,
				ExpMonth: int64(pm.Card.ExpMonth),
				ExpYear:  int64(pm.Card.ExpYear),
			}, nil
		}
	}

	return nil, nil // no payment method found
}

// --- DB helpers for org_settings ---

func (s *BillingService) getOrgSettings(ctx context.Context, orgID string) (*models.OrgSettings, error) {
	query := `
		SELECT org_id, stripe_customer_id, stripe_subscription_id, owner_email, owner_user_id,
		       COALESCE(billing_tier, 'free'), created_at, updated_at
		FROM org_settings
		WHERE org_id = $1
	`

	var settings models.OrgSettings
	var stripeCustomerID, stripeSubscriptionID, ownerEmail, ownerUserID *string

	err := s.db.Pool.QueryRow(ctx, query, orgID).Scan(
		&settings.OrgID, &stripeCustomerID, &stripeSubscriptionID,
		&ownerEmail, &ownerUserID, &settings.BillingTier, &settings.CreatedAt, &settings.UpdatedAt,
	)
	if err == pgx.ErrNoRows {
		return nil, fmt.Errorf("org settings not found")
	}
	if err != nil {
		return nil, err
	}

	if stripeCustomerID != nil {
		settings.StripeCustomerID = *stripeCustomerID
	}
	if stripeSubscriptionID != nil {
		settings.StripeSubscriptionID = *stripeSubscriptionID
	}
	if ownerEmail != nil {
		settings.OwnerEmail = *ownerEmail
	}
	if ownerUserID != nil {
		settings.OwnerUserID = *ownerUserID
	}

	return &settings, nil
}

func (s *BillingService) upsertOrgSettings(ctx context.Context, orgID, stripeCustomerID, stripeSubID, email, userID string) error {
	query := `
		INSERT INTO org_settings (org_id, stripe_customer_id, stripe_subscription_id, owner_email, owner_user_id, billing_tier, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, 'free', NOW(), NOW())
		ON CONFLICT (org_id) DO UPDATE SET
			stripe_customer_id = COALESCE(NULLIF($2, ''), org_settings.stripe_customer_id),
			stripe_subscription_id = COALESCE(NULLIF($3, ''), org_settings.stripe_subscription_id),
			owner_email = COALESCE(NULLIF($4, ''), org_settings.owner_email),
			owner_user_id = COALESCE(NULLIF($5, ''), org_settings.owner_user_id),
			updated_at = NOW()
	`
	_, err := s.db.Pool.Exec(ctx, query, orgID, stripeCustomerID, stripeSubID, email, userID)
	return err
}

// upgradeToPaid sets the org's billing tier to "paid" (called when Stripe subscription is created).
func (s *BillingService) upgradeToPaid(ctx context.Context, orgID string) {
	query := `UPDATE org_settings SET billing_tier = 'paid', updated_at = NOW() WHERE org_id = $1`
	if _, err := s.db.Pool.Exec(ctx, query, orgID); err != nil {
		log.Printf("[billing] Failed to upgrade org %s to paid tier: %v", orgID, err)
	}
}

// GetOrgSettings returns org settings (public accessor)
func (s *BillingService) GetOrgSettings(ctx context.Context, orgID string) (*models.OrgSettings, error) {
	return s.getOrgSettings(ctx, orgID)
}

func minInt64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
