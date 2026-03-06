package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// DeviceAuthStore manages ephemeral device auth codes (in-memory, no DB needed).
// These codes are short-lived and only used during the CLI login flow.
type DeviceAuthStore struct {
	mu    sync.RWMutex
	codes map[string]*DeviceAuthEntry
}

type DeviceAuthEntry struct {
	Code      string    `json:"code"`
	UserCode  string    `json:"user_code"` // Short human-readable code shown in browser
	UserID    string    `json:"user_id,omitempty"`
	OrgID     string    `json:"org_id,omitempty"`
	Email     string    `json:"email,omitempty"`
	Name      string    `json:"name,omitempty"`
	Token     string    `json:"token,omitempty"`
	Status    string    `json:"status"` // "pending", "completed", "expired"
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

func NewDeviceAuthStore() *DeviceAuthStore {
	store := &DeviceAuthStore{
		codes: make(map[string]*DeviceAuthEntry),
	}
	// Background cleanup of expired codes
	go store.cleanup()
	return store
}

func (s *DeviceAuthStore) cleanup() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		s.mu.Lock()
		now := time.Now()
		for code, entry := range s.codes {
			if now.After(entry.ExpiresAt) {
				delete(s.codes, code)
			}
		}
		s.mu.Unlock()
	}
}

func (s *DeviceAuthStore) Create() *DeviceAuthEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	code := generateSecureCode(32)
	userCode := generateUserCode()

	entry := &DeviceAuthEntry{
		Code:      code,
		UserCode:  userCode,
		Status:    "pending",
		ExpiresAt: time.Now().Add(10 * time.Minute),
		CreatedAt: time.Now(),
	}

	s.codes[code] = entry
	return entry
}

func (s *DeviceAuthStore) Get(code string) *DeviceAuthEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entry, ok := s.codes[code]
	if !ok {
		return nil
	}
	if time.Now().After(entry.ExpiresAt) {
		return nil
	}
	return entry
}

func (s *DeviceAuthStore) GetByUserCode(userCode string) *DeviceAuthEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	userCode = strings.ToUpper(strings.TrimSpace(userCode))
	for _, entry := range s.codes {
		if entry.UserCode == userCode && time.Now().Before(entry.ExpiresAt) {
			return entry
		}
	}
	return nil
}

func (s *DeviceAuthStore) Complete(code, userID, orgID, email, name, token string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.codes[code]
	if !ok || time.Now().After(entry.ExpiresAt) {
		return false
	}

	entry.UserID = userID
	entry.OrgID = orgID
	entry.Email = email
	entry.Name = name
	entry.Token = token
	entry.Status = "completed"
	return true
}

// generateSecureCode creates a cryptographic random hex string
func generateSecureCode(bytes int) string {
	b := make([]byte, bytes)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// generateUserCode creates a short human-readable code like "ABCD-1234"
func generateUserCode() string {
	chars := "ABCDEFGHJKLMNPQRSTUVWXYZ23456789" // no 0/O/1/I to avoid confusion
	b := make([]byte, 8)
	rand.Read(b)
	code := make([]byte, 8)
	for i := range code {
		code[i] = chars[int(b[i])%len(chars)]
	}
	return fmt.Sprintf("%s-%s", string(code[:4]), string(code[4:]))
}

// --- Device Auth API Handlers ---

// POST /api/v1/auth/device — CLI calls this to start the flow
func (s *Server) handleDeviceAuthStart(w http.ResponseWriter, r *http.Request) {
	entry := s.deviceAuth.Create()

	baseURL := fmt.Sprintf("%s://%s", scheme(r), r.Host)
	verificationURL := fmt.Sprintf("%s/auth/cli?code=%s", baseURL, entry.Code)

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"device_code":      entry.Code,
		"user_code":        entry.UserCode,
		"verification_url": verificationURL,
		"expires_in":       600, // 10 minutes
		"interval":         2,   // poll every 2 seconds
	})
}

// GET /api/v1/auth/device/poll?code={code} — CLI polls this
func (s *Server) handleDeviceAuthPoll(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	entry := s.deviceAuth.Get(code)
	if entry == nil {
		writeError(w, http.StatusNotFound, "code expired or not found")
		return
	}

	if entry.Status == "pending" {
		// Not yet authenticated — tell CLI to keep polling
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "pending",
		})
		return
	}

	// Completed — return the token + user info
	writeJSON(w, http.StatusOK, map[string]string{
		"status":  "completed",
		"token":   entry.Token,
		"org_id":  entry.OrgID,
		"user_id": entry.UserID,
		"email":   entry.Email,
		"name":    entry.Name,
	})
}

// POST /auth/cli/approve — browser submits this to complete the flow
func (s *Server) handleDeviceAuthApprove(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code   string `json:"code"`
		Token  string `json:"token"`   // Clerk session JWT (production) or empty (dev)
		OrgID  string `json:"org_id"`  // From Clerk organization
		UserID string `json:"user_id"` // From Clerk user
		Email  string `json:"email"`   // User email from Clerk
		Name   string `json:"name"`    // User display name from Clerk
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request")
		return
	}

	if req.Code == "" {
		writeError(w, http.StatusBadRequest, "code is required")
		return
	}

	hasClerk := s.config.ClerkPublishableKey != "" && s.config.ClerkSecretKey != ""
	isDevMode := s.config.Env == "development"

	if req.Token != "" {
		// Clerk session token provided — verify it and mint a Gradient JWT
		clerkClaims, err := s.verifyClerkToken(req.Token)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "authentication failed: "+err.Error())
			return
		}

		// Clerk token verified — use the real user/org from claims
		if req.UserID == "" {
			req.UserID = clerkClaims.Subject
		}
		if req.OrgID == "" {
			req.OrgID = clerkClaims.OrgID
		}
		// Personal account (no org selected in Clerk) — use user ID as org scope
		if req.OrgID == "" {
			req.OrgID = "personal_" + req.UserID
		}

		// Mint a long-lived Gradient JWT for the CLI
		gradientToken, err := s.mintGradientToken(req.UserID, req.OrgID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to create CLI token: "+err.Error())
			return
		}
		req.Token = gradientToken
	} else if !hasClerk && isDevMode {
		// Dev mode without Clerk configured: auto-generate dev token
		log.Println("[auth] No Clerk keys configured in dev mode — generating dev token")
		req.Token = "dev-token-" + generateSecureCode(16)
		if req.OrgID == "" {
			req.OrgID = "dev-org"
		}
		if req.UserID == "" {
			req.UserID = "dev-user"
		}
		if req.Email == "" {
			req.Email = "dev@gradient.local"
		}
		if req.Name == "" {
			req.Name = "Dev User"
		}
	} else {
		writeError(w, http.StatusBadRequest, "authentication required — sign in with Clerk")
		return
	}

	if !s.deviceAuth.Complete(req.Code, req.UserID, req.OrgID, req.Email, req.Name, req.Token) {
		writeError(w, http.StatusNotFound, "code expired or not found")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "approved"})
}

// verifyClerkToken verifies a Clerk session JWT using the configured JWKS or PEM key.
func (s *Server) verifyClerkToken(tokenString string) (*GradientClaims, error) {
	if s.authMiddleware == nil {
		return nil, fmt.Errorf("auth middleware not initialized")
	}
	return s.authMiddleware.parseToken(tokenString)
}

// mintGradientToken creates a long-lived HMAC JWT for CLI use.
// These tokens are signed with JWT_SECRET and contain the user_id and org_id.
// They're valid for 30 days — the CLI stores them in ~/.gradient/config.json.
func (s *Server) mintGradientToken(userID, orgID string) (string, error) {
	if s.config.JWTSecret == "" {
		return "", fmt.Errorf("JWT_SECRET not configured — cannot mint CLI tokens")
	}

	claims := GradientClaims{
		OrgID: orgID,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now()),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(30 * 24 * time.Hour)), // 30 days
			Issuer:    "gradient-api",
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString([]byte(s.config.JWTSecret))
}

// GET /auth/cli — serves the browser auth page
func (s *Server) handleAuthPage(w http.ResponseWriter, r *http.Request) {
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "Missing code parameter", http.StatusBadRequest)
		return
	}

	entry := s.deviceAuth.Get(code)
	if entry == nil {
		http.Error(w, "Code expired or invalid. Run `gc auth login` again.", http.StatusNotFound)
		return
	}

	clerkPubKey := s.config.ClerkPublishableKey
	// Only show dev bypass if Clerk is NOT configured
	isDevBypass := clerkPubKey == "" && (s.config.Env == "development" || s.config.ClerkSecretKey == "")

	// Decode Clerk frontend API domain from publishable key for script URL
	// Key format: pk_test_<base64(domain$)> — the $ is INSIDE the base64 encoding
	clerkDomain := ""
	if clerkPubKey != "" {
		keyBody := strings.TrimPrefix(strings.TrimPrefix(clerkPubKey, "pk_test_"), "pk_live_")
		// Try multiple base64 variants (with/without padding)
		var decoded []byte
		var err error
		decoded, err = base64.RawStdEncoding.DecodeString(keyBody)
		if err != nil {
			decoded, err = base64.StdEncoding.DecodeString(keyBody)
		}
		if err == nil {
			clerkDomain = strings.TrimSuffix(string(decoded), "$")
		}
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Args: %s=userCode, %s=deviceCode, %t=isDevBypass, %s=clerkPubKey, %s=clerkDomain
	fmt.Fprintf(w, deviceAuthHTML, entry.UserCode, code, isDevBypass, clerkPubKey, clerkDomain)
}

func scheme(r *http.Request) string {
	if r.TLS != nil {
		return "https"
	}
	if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
		return fwd
	}
	return "http"
}

// deviceAuthHTML is the browser page for CLI authentication.
// In dev mode: shows a simple "Authorize" button that auto-generates a dev token.
// In production: loads the Clerk JS SDK, mounts the sign-in widget, and uses
// the real Clerk session token + user/org IDs to approve the CLI.
var deviceAuthHTML = `<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Gradient — CLI Login</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, sans-serif;
      background: #0a0a0a;
      color: #e5e5e5;
      display: flex;
      justify-content: center;
      align-items: center;
      min-height: 100vh;
    }
    .card {
      background: #171717;
      border: 1px solid #262626;
      border-radius: 16px;
      padding: 48px;
      max-width: 480px;
      width: 100%%;
      text-align: center;
    }
    .logo { font-size: 32px; font-weight: 700; margin-bottom: 8px; color: #fff; }
    .subtitle { color: #737373; font-size: 14px; margin-bottom: 24px; }
    .code-label { color: #a3a3a3; font-size: 12px; text-transform: uppercase; letter-spacing: 2px; margin-bottom: 8px; }
    .code-display {
      font-family: 'SF Mono', 'Fira Code', monospace;
      font-size: 28px;
      font-weight: 700;
      letter-spacing: 4px;
      color: #22c55e;
      background: #0a0a0a;
      border: 1px solid #262626;
      border-radius: 8px;
      padding: 16px;
      margin-bottom: 24px;
      word-break: break-all;
    }
    .divider {
      border: none;
      border-top: 1px solid #262626;
      margin: 24px 0;
    }
    .status {
      font-size: 14px;
      color: #737373;
      margin-bottom: 16px;
    }
    .status.success { color: #22c55e; font-size: 18px; }
    .status.error { color: #ef4444; }
    .btn {
      display: inline-block;
      background: #22c55e;
      color: #000;
      border: none;
      border-radius: 8px;
      padding: 12px 32px;
      font-size: 16px;
      font-weight: 600;
      cursor: pointer;
      transition: background 0.2s;
      margin-top: 8px;
    }
    .btn:hover { background: #16a34a; }
    .btn:disabled { background: #262626; color: #525252; cursor: not-allowed; }
    .spinner {
      display: inline-block;
      width: 16px; height: 16px;
      border: 2px solid #525252;
      border-top-color: #22c55e;
      border-radius: 50%%;
      animation: spin 0.6s linear infinite;
      margin-right: 8px;
      vertical-align: middle;
    }
    @keyframes spin { to { transform: rotate(360deg); } }
    #clerk-signin { margin: 16px 0; min-height: 50px; }
    .dev-badge {
      display: inline-block;
      background: #422006;
      color: #fb923c;
      border: 1px solid #92400e;
      border-radius: 4px;
      padding: 2px 8px;
      font-size: 11px;
      font-weight: 600;
      text-transform: uppercase;
      letter-spacing: 1px;
      margin-bottom: 16px;
    }
    .user-info {
      background: #0a0a0a;
      border: 1px solid #262626;
      border-radius: 8px;
      padding: 12px 16px;
      margin: 16px 0;
      text-align: left;
      font-size: 13px;
    }
    .user-info .label { color: #737373; }
    .user-info .value { color: #e5e5e5; font-weight: 500; }
  </style>
  <!-- CLERK_SCRIPT_TAG is injected below by the Go template.
       The %[4]s = clerkPubKey, %[5]s = clerkDomain (server-decoded).
       Clerk v5 auto-initializes from data-clerk-publishable-key. -->
  <script id="clerk-sdk" async crossorigin="anonymous"
    data-clerk-publishable-key="%[4]s"
    src="https://%[5]s/npm/@clerk/clerk-js@5/dist/clerk.browser.js"
    onerror="document.getElementById('auth-loading')&&(document.getElementById('auth-loading').textContent='Failed to load Clerk SDK')"
  ></script>
</head>
<body>
  <div class="card">
    <div class="logo">Gradient</div>
    <div class="subtitle">Authorize your CLI</div>

    <div class="code-label">Verification Code</div>
    <div class="code-display" id="userCode">%[1]s</div>
    <div class="status">Confirm this code matches your terminal</div>

    <hr class="divider">

    <!-- Auth section: Clerk sign-in or dev mode button -->
    <div id="auth-section">
      <div id="clerk-signin"></div>
      <div id="dev-auth" style="display: none;">
        <div class="dev-badge">Development Mode</div>
        <p class="status">No authentication required in dev mode</p>
        <button class="btn" id="devApproveBtn" onclick="devApprove()">Authorize CLI</button>
      </div>
      <div id="auth-loading" class="status">
        <span class="spinner"></span> Loading authentication...
      </div>
    </div>

    <!-- Logged-in: show user info + authorize button -->
    <div id="authorize-section" style="display: none;">
      <div class="user-info" id="userInfo"></div>
      <button class="btn" id="approveBtn" onclick="approveWithClerk()">Authorize CLI</button>
    </div>

    <!-- Success -->
    <div id="success-section" style="display: none;">
      <div class="status success">✓ CLI authorized!</div>
      <p class="status" style="margin-top: 8px;">You can close this tab and return to your terminal.</p>
    </div>

    <!-- Error -->
    <div id="error-section" style="display: none;">
      <div class="status error" id="errorMsg"></div>
    </div>
  </div>

  <script>
    const deviceCode = '%[2]s';
    const isDevMode = %[3]t;
    const clerkPubKey = '%[4]s';
    const clerkDomain = '%[5]s';

    let clerkInstance = null;

    function showError(msg) {
      document.getElementById('auth-loading').style.display = 'none';
      document.getElementById('auth-section').querySelector('#clerk-signin').innerHTML = '';
      document.getElementById('error-section').style.display = 'block';
      document.getElementById('errorMsg').textContent = msg;
      console.error('[gradient]', msg);
    }

    function showDevBypass() {
      document.getElementById('auth-loading').style.display = 'none';
      document.getElementById('dev-auth').style.display = 'block';
    }

    async function init() {
      // Store device code in localStorage so we can recover after Clerk redirect
      if (deviceCode) {
        localStorage.setItem('gradient_device_code', deviceCode);
      }

      if (isDevMode || !clerkPubKey || !clerkDomain) {
        showDevBypass();
        return;
      }

      // Clerk v5: the script tag with data-clerk-publishable-key is already in <head>.
      // After it loads, window.Clerk is an auto-initialized instance.
      // We wait for 'load' event (all resources done) then call Clerk.load().
      try {
        // Wait for Clerk to appear on window (the script in <head> sets it)
        let attempts = 0;
        while (!window.Clerk && attempts < 100) {
          await new Promise(r => setTimeout(r, 100));
          attempts++;
        }

        if (!window.Clerk) {
          showError('Clerk did not initialize after 10s. Publishable key: ' + clerkPubKey.substring(0, 20) + '...');
          return;
        }

        clerkInstance = window.Clerk;
        console.log('[gradient] Clerk found, type:', typeof clerkInstance, 'loaded:', clerkInstance.loaded);

        // Call .load() to fully initialize (fetches environment, session, etc.)
        if (typeof clerkInstance.load === 'function') {
          await clerkInstance.load();
        }

        console.log('[gradient] Clerk loaded, user:', clerkInstance.user?.id);
        document.getElementById('auth-loading').style.display = 'none';

        if (clerkInstance.user) {
          showAuthorizeButton();
        } else {
          mountSignIn();
        }
      } catch (err) {
        showError('Clerk error: ' + err.message);
      }
    }

    function mountSignIn() {
      const el = document.getElementById('clerk-signin');
      el.style.minHeight = '300px';

      const returnUrl = window.location.href; // /auth/cli?code=XXX
      clerkInstance.mountSignIn(el, {
        afterSignInUrl: returnUrl,
        afterSignUpUrl: returnUrl,
        redirectUrl: returnUrl,
        appearance: {
          variables: {
            colorPrimary: '#22c55e',
            colorBackground: '#171717',
            colorText: '#e5e5e5',
            colorInputBackground: '#0a0a0a',
            colorInputText: '#e5e5e5',
            borderRadius: '8px',
          },
          elements: {
            rootBox: { width: '100%%' },
            card: { background: 'transparent', boxShadow: 'none', border: 'none', padding: '0' },
            socialButtonsBlockButton: { border: '1px solid #262626' },
            formFieldInput: { border: '1px solid #262626' },
          }
        }
      });

      // Listen for sign-in completion
      clerkInstance.addListener(function(payload) {
        if (payload.user && payload.session) {
          try { clerkInstance.unmountSignIn(el); } catch(e) {}
          showAuthorizeButton();
        }
      });
    }

    function showAuthorizeButton() {
      const user = clerkInstance.user;
      const org = clerkInstance.organization;
      const info = document.getElementById('userInfo');

      let email = '';
      if (user.primaryEmailAddress) email = user.primaryEmailAddress.emailAddress;
      else if (user.emailAddresses && user.emailAddresses.length > 0) email = user.emailAddresses[0].emailAddress;
      else email = user.username || user.id;

      let html = '<div><span class="label">Signed in as: </span><span class="value">' + email + '</span></div>';
      if (org) {
        html += '<div><span class="label">Organization: </span><span class="value">' + org.name + '</span></div>';
      }
      info.innerHTML = html;

      document.getElementById('auth-section').style.display = 'none';
      document.getElementById('authorize-section').style.display = 'block';
    }

    async function approveWithClerk() {
      const btn = document.getElementById('approveBtn');
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner"></span> Authorizing...';

      try {
        const session = clerkInstance.session;
        const token = await session.getToken();
        const user = clerkInstance.user;
        const org = clerkInstance.organization;

        const userEmail = user.primaryEmailAddress?.emailAddress || '';
        const userName = [user.firstName, user.lastName].filter(Boolean).join(' ') || user.username || '';

        const resp = await fetch('/auth/cli/approve', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            code: deviceCode,
            token: token,
            user_id: user.id,
            org_id: org ? org.id : '',
            email: userEmail,
            name: userName,
          }),
        });

        if (resp.ok) {
          document.getElementById('authorize-section').style.display = 'none';
          document.getElementById('success-section').style.display = 'block';
        } else {
          const data = await resp.json();
          throw new Error(data.error || 'Authorization failed');
        }
      } catch (err) {
        document.getElementById('authorize-section').style.display = 'none';
        showError('Authorization failed: ' + err.message);
      }
    }

    async function devApprove() {
      const btn = document.getElementById('devApproveBtn');
      btn.disabled = true;
      btn.innerHTML = '<span class="spinner"></span> Authorizing...';

      try {
        const resp = await fetch('/auth/cli/approve', {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            code: deviceCode,
            token: '',
            org_id: '',
            user_id: '',
          }),
        });

        if (resp.ok) {
          document.getElementById('auth-section').style.display = 'none';
          document.getElementById('success-section').style.display = 'block';
        } else {
          const data = await resp.json();
          throw new Error(data.error || 'Authorization failed');
        }
      } catch (err) {
        showError('Dev approve failed: ' + err.message);
      }
    }

    init();
  </script>
</body>
</html>
`
