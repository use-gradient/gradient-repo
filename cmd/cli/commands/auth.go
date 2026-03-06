package commands

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func NewAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Authentication commands",
	}

	cmd.AddCommand(NewAuthLoginCmd())
	cmd.AddCommand(NewAuthLogoutCmd())
	cmd.AddCommand(NewAuthStatusCmd())

	return cmd
}

func NewAuthLoginCmd() *cobra.Command {
	var (
		token  string
		apiURL string
		orgID  string
	)

	cmd := &cobra.Command{
		Use:   "login",
		Short: "Login to Gradient",
		Long:  "Authenticate with the Gradient API. Opens a browser for secure login.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := LoadCLIConfig()

			if apiURL != "" {
				cfg.APIURL = apiURL
			}

			// If --token is provided, use direct token mode (for CI/scripts)
			if token != "" {
				cfg.Token = token
				if orgID != "" {
					cfg.ActiveOrg = orgID
				}
				if err := SaveCLIConfig(cfg); err != nil {
					return fmt.Errorf("failed to save credentials: %w", err)
				}

				// Verify the token works
				client, err := NewAPIClient()
				if err != nil {
					return err
				}
				var health map[string]string
				if err := client.DoJSON("GET", "/api/v1/health", nil, &health); err != nil {
					fmt.Printf("⚠  Warning: could not verify token against API: %v\n", err)
					fmt.Println("   Credentials saved anyway. Make sure the API server is running.")
				} else {
					fmt.Println("✓ Authenticated successfully")
				}
				fmt.Printf("  API URL:    %s\n", cfg.APIURL)
				fmt.Printf("  Active Org: %s\n", cfg.ActiveOrg)
				return nil
			}

			// ── Browser-based device auth flow ──
			fmt.Println()
			fmt.Println("  ┌─────────────────────────────────┐")
			fmt.Println("  │     Gradient — CLI Login         │")
			fmt.Println("  └─────────────────────────────────┘")
			fmt.Println()

			// Step 1: Start device auth flow
			httpClient := &http.Client{Timeout: 10 * time.Second}
			resp, err := httpClient.Post(cfg.APIURL+"/api/v1/auth/device", "application/json", nil)
			if err != nil {
				return fmt.Errorf("failed to connect to API at %s\n  Is the server running? Start it with: make run-api\n  Error: %w", cfg.APIURL, err)
			}
			defer resp.Body.Close()

			var deviceResp struct {
				DeviceCode      string `json:"device_code"`
				UserCode        string `json:"user_code"`
				VerificationURL string `json:"verification_url"`
				ExpiresIn       int    `json:"expires_in"`
				Interval        int    `json:"interval"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&deviceResp); err != nil {
				return fmt.Errorf("invalid response from API: %w", err)
			}

			// Step 2: Show the code and open browser
			fmt.Printf("  Your verification code:\n\n")
			fmt.Printf("%s\n", deviceResp.UserCode)

			fmt.Printf("  Opening browser to complete login...\n")
			fmt.Printf("  URL: %s\n\n", deviceResp.VerificationURL)

			// Open browser
			if err := openBrowser(deviceResp.VerificationURL); err != nil {
				fmt.Printf("  ⚠  Could not open browser automatically.\n")
				fmt.Printf("     Open this URL manually: %s\n\n", deviceResp.VerificationURL)
			}

			// Step 3: Poll for completion
			fmt.Printf("  Waiting for authorization...")

			interval := time.Duration(deviceResp.Interval) * time.Second
			if interval < 2*time.Second {
				interval = 2 * time.Second
			}
			deadline := time.Now().Add(time.Duration(deviceResp.ExpiresIn) * time.Second)

			dots := 0
			for time.Now().Before(deadline) {
				time.Sleep(interval)
				dots++
				if dots%3 == 0 {
					fmt.Print(".")
				}

				pollResp, err := httpClient.Get(cfg.APIURL + "/api/v1/auth/device/poll?code=" + deviceResp.DeviceCode)
				if err != nil {
					continue
				}

			var pollResult struct {
				Status string `json:"status"`
				Token  string `json:"token"`
				OrgID  string `json:"org_id"`
				UserID string `json:"user_id"`
				Email  string `json:"email"`
				Name   string `json:"name"`
			}
				json.NewDecoder(pollResp.Body).Decode(&pollResult)
				pollResp.Body.Close()

				if pollResult.Status == "completed" {
					fmt.Println()
					fmt.Println()

					cfg.Token = pollResult.Token
					cfg.UserID = pollResult.UserID
					cfg.Email = pollResult.Email
					cfg.Name = pollResult.Name
					if pollResult.OrgID != "" {
						cfg.ActiveOrg = pollResult.OrgID
					}
					if orgID != "" {
						cfg.ActiveOrg = orgID // --org flag overrides
					}

					if err := SaveCLIConfig(cfg); err != nil {
						return fmt.Errorf("failed to save credentials: %w", err)
					}

					fmt.Println("  ✓ Authenticated successfully!")
					fmt.Println()
					if cfg.Name != "" {
						fmt.Printf("  Name:       %s\n", cfg.Name)
					}
					if cfg.Email != "" {
						fmt.Printf("  Email:      %s\n", cfg.Email)
					}
					fmt.Printf("  API URL:    %s\n", cfg.APIURL)
					fmt.Printf("  Active Org: %s\n", cfg.ActiveOrg)
					fmt.Println()
					fmt.Println("  Try: gc env list")
					fmt.Println()
					return nil
				}
			}

			fmt.Println()
			return fmt.Errorf("login timed out. Run `gc auth login` to try again")
		},
	}

	cmd.Flags().StringVar(&token, "token", "", "API token (skips browser flow, for CI/scripts)")
	cmd.Flags().StringVar(&apiURL, "api-url", "", "API server URL (default: http://localhost:6767)")
	cmd.Flags().StringVar(&orgID, "org", "", "Organization ID")

	return cmd
}

func NewAuthLogoutCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "logout",
		Short: "Logout from Gradient",
		Long:  "Clear local credentials and optionally revoke the token on the server.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := LoadCLIConfig()

			if cfg.Token == "" {
				fmt.Println("Already logged out.")
				return nil
			}

			// Show what we're clearing
			fmt.Printf("Logging out...\n")
			if cfg.Name != "" || cfg.Email != "" {
				fmt.Printf("  User:       %s <%s>\n", cfg.Name, cfg.Email)
			}
			fmt.Printf("  Active Org: %s\n", cfg.ActiveOrg)

			// Attempt to revoke token on server (best effort)
			if !force {
				client, err := NewAPIClient()
				if err == nil {
					var result map[string]string
					_ = client.DoJSON("POST", "/api/v1/auth/logout", nil, &result)
				}
			}

			// Clear local credentials
			previousOrg := cfg.ActiveOrg
			cfg.Token = ""
			cfg.ActiveOrg = ""
			cfg.UserID = ""
			cfg.Email = ""
			cfg.Name = ""

			if err := SaveCLIConfig(cfg); err != nil {
				return fmt.Errorf("failed to save config: %w", err)
			}

			fmt.Println()
			fmt.Println("✓ Logged out successfully")
			fmt.Printf("  Cleared token and org (%s)\n", previousOrg)
			fmt.Println("  Run `gc auth login` to sign in again.")
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Force logout without server-side revocation")
	return cmd
}

func NewAuthStatusCmd() *cobra.Command {
	var verbose bool

	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show authentication status",
		Long:  "Display detailed authentication status including server health, token validity, and active resources.",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := LoadCLIConfig()

			fmt.Println()
			fmt.Println("  ┌─────────────────────────────────┐")
			fmt.Println("  │     Gradient — Auth Status       │")
			fmt.Println("  └─────────────────────────────────┘")
			fmt.Println()

			if cfg.Token == "" {
				fmt.Println("  Status:       ✗ not logged in")
				fmt.Println()
				fmt.Println("  Run `gc auth login` to authenticate.")
				return nil
			}

			fmt.Println("  Status:       ✓ logged in")
			if cfg.Name != "" {
				fmt.Printf("  Name:         %s\n", cfg.Name)
			}
			if cfg.Email != "" {
				fmt.Printf("  Email:        %s\n", cfg.Email)
			}
			fmt.Printf("  API URL:      %s\n", cfg.APIURL)
			if cfg.ActiveOrg != "" {
				fmt.Printf("  Active Org:   %s\n", cfg.ActiveOrg)
			}
			tokenPreview := cfg.Token
			if len(tokenPreview) > 12 {
				tokenPreview = tokenPreview[:8] + "..." + tokenPreview[len(tokenPreview)-4:]
			}
			fmt.Printf("  Token:        %s\n", tokenPreview)
			fmt.Printf("  Config:       %s\n", configPath())

			fmt.Println()

			// Health check
			client, err := NewAPIClient()
			if err != nil {
				fmt.Printf("  API:          ✗ client error: %v\n", err)
				return nil
			}

			var health map[string]string
			if err := client.DoJSON("GET", "/api/v1/health", nil, &health); err != nil {
				fmt.Printf("  API:          ✗ unreachable (%v)\n", err)
			} else {
				fmt.Printf("  API:          ✓ healthy (v%s)\n", health["version"])
			}

			if verbose && cfg.Token != "" {
				// Get environment count
				var envs []map[string]interface{}
				if err := client.DoJSON("GET", "/api/v1/environments", nil, &envs); err == nil {
					running := 0
					for _, e := range envs {
						if s, ok := e["status"].(string); ok && s == "running" {
							running++
						}
					}
					fmt.Printf("  Environments: %d total (%d running)\n", len(envs), running)
				}

				// Get mesh health
				var meshHealth map[string]interface{}
				if err := client.DoJSON("GET", "/api/v1/mesh/health", nil, &meshHealth); err == nil {
					bus, _ := meshHealth["bus"].(string)
					fmt.Printf("  Mesh Bus:     %s\n", bus)
				}

				// Get billing info
				var usage map[string]interface{}
				if err := client.DoJSON("GET", "/api/v1/billing/usage", nil, &usage); err == nil {
					if totalCost, ok := usage["total_cost"].(float64); ok {
						fmt.Printf("  Month Cost:   $%.2f\n", totalCost)
					}
				}
			}

			fmt.Println()
			return nil
		},
	}

	cmd.Flags().BoolVarP(&verbose, "verbose", "v", false, "Show additional details (environments, billing, mesh)")
	return cmd
}

// openBrowser opens the given URL in the default browser
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("unsupported platform")
	}
	return cmd.Start()
}
