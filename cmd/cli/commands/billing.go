package commands

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func NewBillingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "billing",
		Short: "Billing and usage commands",
	}

	cmd.AddCommand(NewBillingUsageCmd())
	cmd.AddCommand(NewBillingInvoicesCmd())
	cmd.AddCommand(NewBillingSetupCmd())
	cmd.AddCommand(NewBillingStatusCmd())

	return cmd
}

func NewBillingUsageCmd() *cobra.Command {
	var month string

	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show current usage for active organization",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			path := "/api/v1/billing/usage"
			if month != "" {
				path += "?month=" + month
			}

			var result map[string]interface{}
			if err := client.DoJSON("GET", path, nil, &result); err != nil {
				return err
			}

			fmt.Printf("Usage Summary (%s)\n", result["month"])
			fmt.Printf("─────────────────────────────────\n")
			fmt.Printf("  Small hours:   %.2f  ($%.2f)\n",
				toFloat(result["small_hours"]), toFloat(result["small_hours"])*0.15)
			fmt.Printf("  Medium hours:  %.2f  ($%.2f)\n",
				toFloat(result["medium_hours"]), toFloat(result["medium_hours"])*0.35)
			fmt.Printf("  Large hours:   %.2f  ($%.2f)\n",
				toFloat(result["large_hours"]), toFloat(result["large_hours"])*0.70)
			fmt.Printf("  GPU hours:     %.2f  ($%.2f)\n",
				toFloat(result["gpu_hours"]), toFloat(result["gpu_hours"])*3.50)
			fmt.Printf("─────────────────────────────────\n")
			fmt.Printf("  Total:         %.2f hrs  $%.2f\n",
				toFloat(result["total_hours"]), toFloat(result["total_cost"]))

			return nil
		},
	}

	cmd.Flags().StringVar(&month, "month", time.Now().Format("2006-01"), "Month in YYYY-MM format")

	return cmd
}

func NewBillingInvoicesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invoices",
		Short: "List invoices for active organization",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var invoices []map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/billing/invoices", nil, &invoices); err != nil {
				return err
			}

			if len(invoices) == 0 {
				fmt.Println("No invoices found.")
				fmt.Println("Set up billing with: gc billing setup")
				return nil
			}

			for _, inv := range invoices {
				data, _ := json.MarshalIndent(inv, "", "  ")
				fmt.Println(string(data))
			}

			return nil
		},
	}
	return cmd
}

func NewBillingSetupCmd() *cobra.Command {
	var (
		email string
		name  string
	)

	cmd := &cobra.Command{
		Use:   "setup",
		Short: "Set up billing (org owner only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			body := map[string]string{
				"email": email,
				"name":  name,
			}

			var result map[string]interface{}
			if err := client.DoJSON("POST", "/api/v1/billing/setup", body, &result); err != nil {
				return err
			}

			fmt.Println("✓ Billing set up successfully via Stripe")
			if cid, ok := result["stripe_customer_id"]; ok && cid != "" {
				fmt.Printf("  Stripe Customer ID:     %s\n", cid)
			}
			if sid, ok := result["stripe_subscription_id"]; ok && sid != "" {
				fmt.Printf("  Stripe Subscription ID: %s\n", sid)
			}
			fmt.Println("  Your org has been upgraded to paid tier.")

			return nil
		},
	}

	cmd.Flags().StringVar(&email, "email", "", "Billing email (required)")
	cmd.Flags().StringVar(&name, "name", "", "Organization name (required)")
	cmd.MarkFlagRequired("email")
	cmd.MarkFlagRequired("name")

	return cmd
}

func NewBillingStatusCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show billing tier, limits, and payment status",
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := NewAPIClient()
			if err != nil {
				return err
			}

			var status map[string]interface{}
			if err := client.DoJSON("GET", "/api/v1/billing/status", nil, &status); err != nil {
				return err
			}

			tier := fmt.Sprint(status["tier"])
			hasPayment := status["has_payment_method"] == true
			canCreate := status["can_create_env"] == true

			stripeConfigured := status["stripe_configured"] == true

			fmt.Printf("Billing Status (%s)\n", status["month"])
			fmt.Printf("─────────────────────────────────────\n")

			// Stripe configuration
			if !stripeConfigured {
				fmt.Printf("  ⚠️  Stripe:      NOT CONFIGURED\n")
				fmt.Printf("     Set STRIPE_SECRET_KEY on the server\n")
				fmt.Printf("     (use Stripe test keys for development)\n")
				fmt.Printf("─────────────────────────────────────\n")
			}

			// Tier display
			tierIcon := "🆓"
			if tier == "paid" {
				tierIcon = "💳"
			}
			fmt.Printf("  Tier:           %s %s\n", tierIcon, tier)

			// Payment method
			paymentIcon := "✗"
			if hasPayment {
				paymentIcon = "✓"
			}
			fmt.Printf("  Payment Method: %s\n", paymentIcon)

			// Free tier limits
			if tier == "free" || !hasPayment {
				freeUsed := toFloat(status["free_hours_used"])
				freeLimit := toFloat(status["free_hours_limit"])
				freeLeft := toFloat(status["free_hours_left"])

				fmt.Printf("  Free Hours:     %.1f / %.0f used (%.1f remaining)\n", freeUsed, freeLimit, freeLeft)

				// Progress bar
				pct := freeUsed / freeLimit
				if pct > 1 {
					pct = 1
				}
				barLen := 20
				filled := int(pct * float64(barLen))
				bar := ""
				for i := 0; i < barLen; i++ {
					if i < filled {
						bar += "█"
					} else {
						bar += "░"
					}
				}
				fmt.Printf("  Usage:          [%s] %.0f%%\n", bar, pct*100)
			}

			// Allowed sizes
			if sizes, ok := status["allowed_sizes"].([]interface{}); ok {
				sizeList := ""
				for i, s := range sizes {
					if i > 0 {
						sizeList += ", "
					}
					sizeList += fmt.Sprint(s)
				}
				fmt.Printf("  Allowed Sizes:  %s\n", sizeList)
			}

			// Can create?
			fmt.Printf("─────────────────────────────────────\n")
			if canCreate {
				fmt.Printf("  ✓ You can create environments\n")
			} else {
				fmt.Printf("  ✗ Free tier exhausted — add a payment method:\n")
				fmt.Printf("    gc billing setup --email you@example.com --name \"Your Org\"\n")
			}

			return nil
		},
	}
	return cmd
}

func toFloat(v interface{}) float64 {
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case json.Number:
		f, _ := val.Float64()
		return f
	default:
		return 0
	}
}
