package services

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/gradient/gradient/internal/db"
	"github.com/gradient/gradient/internal/models"
	"github.com/jackc/pgx/v5"
	"github.com/stripe/stripe-go/v76"
	portalsession "github.com/stripe/stripe-go/v76/billingportal/session"
	"github.com/stripe/stripe-go/v76/customer"
	"github.com/stripe/stripe-go/v76/invoice"
	"github.com/stripe/stripe-go/v76/paymentmethod"
	"github.com/stripe/stripe-go/v76/subscription"
	"github.com/stripe/stripe-go/v76/usagerecord"
)

// Billing constants — free tier limits
const (
	FreeTierMonthlyHours = 20.0   // 20 free hours per month
	FreeTierAllowedSize  = "small" // Free tier only allows "small" environments
	MinBilledSeconds     = 60     // Minimum 1 minute billing
)

// AllSizes lists all valid environment sizes
var AllSizes = []string{"small", "medium", "large", "gpu"}

type BillingService struct {
	db            *db.DB
	enabled       bool   // Stripe enabled
	priceSmallID  string // Stripe Price ID for small tier
	priceMediumID string // Stripe Price ID for medium tier
	priceLargeID  string // Stripe Price ID for large tier
	priceGPUID    string // Stripe Price ID for GPU tier
}

func NewBillingService(database *db.DB, stripeKey, priceSmallID, priceMediumID, priceLargeID, priceGPUID string) *BillingService {
	if stripeKey != "" {
		stripe.Key = stripeKey
	}
	return &BillingService{
		db:            database,
		enabled:       stripeKey != "",
		priceSmallID:  priceSmallID,
		priceMediumID: priceMediumID,
		priceLargeID:  priceLargeID,
		priceGPUID:    priceGPUID,
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
//   - Free tier (no payment method): 20 hours/month, "small" only
//   - After 20 hours without payment method: HARD BLOCK — must add payment
//   - Paid tier (has payment method): any size, no hour limit
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

	// Free tier checks
	// 1. Size restriction: free tier only allows "small"
	if requestedSize != FreeTierAllowedSize {
		return fmt.Errorf(
			"free tier only allows '%s' environments — upgrade to pay-as-you-go by adding a payment method (gc billing setup) to use '%s' instances",
			FreeTierAllowedSize, requestedSize,
		)
	}

	// 2. Hours limit: free tier capped at 20 hours/month
	if status.FreeHoursLeft <= 0 {
		return fmt.Errorf(
			"free tier limit reached: %.1f/%.0f hours used this month — add a payment method to continue (gc billing setup)",
			status.FreeHoursUsed, status.FreeHoursLimit,
		)
	}

	return nil
}

// GetBillingStatus computes the full billing status for an org.
func (s *BillingService) GetBillingStatus(ctx context.Context, orgID string) (*models.BillingStatus, error) {
	month := time.Now().Format("2006-01")

	// Get current month usage (including currently running envs)
	usedHours, err := s.getMonthlyUsedHours(ctx, orgID, month)
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

	freeLeft := FreeTierMonthlyHours - usedHours
	if freeLeft < 0 {
		freeLeft = 0
	}

	// Determine allowed sizes
	allowedSizes := AllSizes
	canCreate := true
	if !hasPayment {
		allowedSizes = []string{FreeTierAllowedSize}
		if freeLeft <= 0 {
			canCreate = false
		}
	}

	return &models.BillingStatus{
		OrgID:            orgID,
		Tier:             tier,
		HasPaymentMethod: hasPayment,
		StripeConfigured: s.enabled,
		FreeHoursUsed:    usedHours,
		FreeHoursLimit:   FreeTierMonthlyHours,
		FreeHoursLeft:    freeLeft,
		CanCreateEnv:     canCreate,
		AllowedSizes:     allowedSizes,
		Month:            month,
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

	// Verify an actual payment method exists on the Stripe customer
	if !s.enabled {
		return false
	}
	params := &stripe.PaymentMethodListParams{
		Customer: stripe.String(settings.StripeCustomerID),
		Type:     stripe.String("card"),
	}
	i := paymentmethod.List(params)
	return i.Next() // true only if at least one card is attached
}

// getMonthlyUsedHours returns total hours used this month, INCLUDING currently running envs.
func (s *BillingService) getMonthlyUsedHours(ctx context.Context, orgID, month string) (float64, error) {
	// Completed sessions: sum of billed_seconds
	query := `
		SELECT COALESCE(SUM(billed_seconds), 0)
		FROM usage_events
		WHERE org_id = $1
		  AND TO_CHAR(started_at, 'YYYY-MM') = $2
		  AND billed_seconds > 0
	`
	var completedSeconds int
	err := s.db.Pool.QueryRow(ctx, query, orgID, month).Scan(&completedSeconds)
	if err != nil {
		return 0, err
	}

	// Currently running sessions: compute live duration
	activeQuery := `
		SELECT COALESCE(SUM(EXTRACT(EPOCH FROM (NOW() - started_at))::INTEGER), 0)
		FROM usage_events
		WHERE org_id = $1
		  AND TO_CHAR(started_at, 'YYYY-MM') = $2
		  AND stopped_at IS NULL
	`
	var activeSeconds int
	err = s.db.Pool.QueryRow(ctx, activeQuery, orgID, month).Scan(&activeSeconds)
	if err != nil {
		return 0, err
	}

	totalSeconds := completedSeconds + activeSeconds
	return float64(totalSeconds) / 3600.0, nil
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
		if reportErr := s.ReportUsageToStripe(ctx, orgID, size, billedSeconds); reportErr != nil {
			// Log as ERROR — Stripe must be configured. If this fires, billing is broken.
			log.Printf("[billing] ERROR: Failed to report usage to Stripe for org %s: %v", orgID, reportErr)
		}
	}

	return updateErr
}

// GetUsageSummary calculates usage summary for an org in a given month.
// Includes both completed and currently-running sessions.
func (s *BillingService) GetUsageSummary(ctx context.Context, orgID, month string) (*models.UsageSummary, error) {
	// Completed sessions
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
	err := s.db.Pool.QueryRow(ctx, query, orgID, month).Scan(&smallSec, &mediumSec, &largeSec, &gpuSec)
	if err != nil {
		return nil, fmt.Errorf("failed to get usage summary: %w", err)
	}

	// Add currently running sessions
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

	smallHours := float64(smallSec) / 3600.0
	mediumHours := float64(mediumSec) / 3600.0
	largeHours := float64(largeSec) / 3600.0
	gpuHours := float64(gpuSec) / 3600.0

	totalHours := smallHours + mediumHours + largeHours + gpuHours
	totalCost := (smallHours * 0.15) + (mediumHours * 0.35) + (largeHours * 0.70) + (gpuHours * 3.50)

	return &models.UsageSummary{
		OrgID:       orgID,
		Month:       month,
		TotalHours:  totalHours,
		TotalCost:   totalCost,
		SmallHours:  smallHours,
		MediumHours: mediumHours,
		LargeHours:  largeHours,
		GPUHours:    gpuHours,
	}, nil
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

// CreateMeteredSubscription creates a Stripe subscription with metered billing for an org.
// This sets up the pricing tiers so usage records can be reported.
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

	// Build subscription items from configured price IDs
	var items []*stripe.SubscriptionItemsParams
	if s.priceSmallID != "" {
		items = append(items, &stripe.SubscriptionItemsParams{
			Price: stripe.String(s.priceSmallID),
		})
	}
	if s.priceMediumID != "" {
		items = append(items, &stripe.SubscriptionItemsParams{
			Price: stripe.String(s.priceMediumID),
		})
	}
	if s.priceLargeID != "" {
		items = append(items, &stripe.SubscriptionItemsParams{
			Price: stripe.String(s.priceLargeID),
		})
	}
	if s.priceGPUID != "" {
		items = append(items, &stripe.SubscriptionItemsParams{
			Price: stripe.String(s.priceGPUID),
		})
	}

	if len(items) == 0 {
		return "", fmt.Errorf("no Stripe price IDs configured — set STRIPE_PRICE_SMALL_ID, etc.")
	}

	params := &stripe.SubscriptionParams{
		Customer: stripe.String(settings.StripeCustomerID),
		Items:    items,
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

// ReportUsageToStripe sends a usage record to Stripe for metered billing.
// Called when an environment is stopped (TrackUsageStop).
// Reports usage in minutes (Stripe prices should be set as per-minute rates).
// Minimum billing: 1 minute (60 seconds).
// Stripe MUST be configured — even in dev, all billing goes through Stripe (use test keys).
func (s *BillingService) ReportUsageToStripe(ctx context.Context, orgID, size string, billedSeconds int) error {
	if !s.enabled {
		return fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY (use Stripe test keys for development)")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeSubscriptionID == "" {
		// No subscription = free tier user. Usage is tracked in the DB but not reported to Stripe.
		// This is expected — free tier users don't have Stripe subscriptions.
		log.Printf("[billing] No subscription for org %s (free tier), usage tracked in DB only", orgID)
		return nil
	}

	// Get subscription items to find the right one for this size
	sub, err := subscription.Get(settings.StripeSubscriptionID, nil)
	if err != nil {
		return fmt.Errorf("failed to get subscription: %w", err)
	}

	// Find the subscription item matching this size's price
	priceID := s.sizeToPriceID(size)
	if priceID == "" {
		log.Printf("[billing] No price ID configured for size %s, skipping usage report", size)
		return nil
	}

	var subItemID string
	for _, item := range sub.Items.Data {
		if item.Price.ID == priceID {
			subItemID = item.ID
			break
		}
	}

	if subItemID == "" {
		log.Printf("[billing] No subscription item found for price %s, skipping usage report", priceID)
		return nil
	}

	// Enforce minimum billing of 1 minute
	if billedSeconds < MinBilledSeconds {
		billedSeconds = MinBilledSeconds
	}

	// Report usage in minutes (rounded up) for proper per-second billing granularity
	// A 12-minute session = 12 minutes (not 1 hour)
	minutes := int64(billedSeconds+59) / 60
	if minutes < 1 {
		minutes = 1
	}

	params := &stripe.UsageRecordParams{
		SubscriptionItem: stripe.String(subItemID),
		Quantity:         stripe.Int64(minutes),
		Timestamp:        stripe.Int64(time.Now().Unix()),
		Action:           stripe.String("increment"),
	}

	_, err = usagerecord.New(params)
	if err != nil {
		return fmt.Errorf("failed to report usage to Stripe: %w", err)
	}

	log.Printf("[billing] Reported %d minutes (%d seconds) of %s usage to Stripe for org %s", minutes, billedSeconds, size, orgID)
	return nil
}

// sizeToPriceID maps environment size to the configured Stripe price ID.
func (s *BillingService) sizeToPriceID(size string) string {
	switch size {
	case "small":
		return s.priceSmallID
	case "medium":
		return s.priceMediumID
	case "large":
		return s.priceLargeID
	case "gpu":
		// GPU has its own price ($3.50/hr vs $0.70/hr for large)
		if s.priceGPUID != "" {
			return s.priceGPUID
		}
		// Fallback to large if GPU price not configured (log warning)
		log.Printf("[billing] WARNING: GPU price ID not configured (STRIPE_PRICE_GPU_ID), falling back to large price")
		return s.priceLargeID
	default:
		return s.priceSmallID
	}
}

// GetStripeInvoices lists invoices for an org from Stripe.
// Stripe MUST be configured — even in dev, all billing goes through Stripe (use test keys).
func (s *BillingService) GetStripeInvoices(ctx context.Context, orgID string) ([]*stripe.Invoice, error) {
	if !s.enabled {
		return nil, fmt.Errorf("Stripe is not configured — set STRIPE_SECRET_KEY (use Stripe test keys for development)")
	}

	settings, err := s.getOrgSettings(ctx, orgID)
	if err != nil || settings.StripeCustomerID == "" {
		// No Stripe customer = no invoices. Not an error — free tier users won't have a customer.
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
