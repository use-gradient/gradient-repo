package api

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// generateTestKeyPair creates an RSA key pair for testing JWT verification
func generateTestKeyPair(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate RSA key pair: %v", err)
	}

	pubKeyBytes, err := x509.MarshalPKIXPublicKey(&privateKey.PublicKey)
	if err != nil {
		t.Fatalf("Failed to marshal public key: %v", err)
	}

	pemBlock := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: pubKeyBytes,
	})

	return privateKey, string(pemBlock)
}

// signTestJWT creates a signed JWT for testing
func signTestJWT(t *testing.T, privateKey *rsa.PrivateKey, claims *GradientClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tokenString, err := token.SignedString(privateKey)
	if err != nil {
		t.Fatalf("Failed to sign JWT: %v", err)
	}
	return tokenString
}

func TestDevModeAuth(t *testing.T) {
	// Dev mode: no ClerkSecretKey
	middleware := NewAuthMiddleware("development", "", "", "", "")

	handler := middleware.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orgID := GetOrgID(r.Context())
		userID := GetUserID(r.Context())
		w.Header().Set("X-Test-OrgID", orgID)
		w.Header().Set("X-Test-UserID", userID)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("dev mode uses default org and user", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-Test-OrgID"); got != "dev-org" {
			t.Errorf("Expected org_id 'dev-org', got %q", got)
		}
		if got := rec.Header().Get("X-Test-UserID"); got != "dev-user" {
			t.Errorf("Expected user_id 'dev-user', got %q", got)
		}
	})

	t.Run("dev mode uses custom headers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("X-Org-ID", "my-org")
		req.Header.Set("X-User-ID", "my-user")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-Test-OrgID"); got != "my-org" {
			t.Errorf("Expected org_id 'my-org', got %q", got)
		}
		if got := rec.Header().Get("X-Test-UserID"); got != "my-user" {
			t.Errorf("Expected user_id 'my-user', got %q", got)
		}
	})

	t.Run("health check skips auth", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/health", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rec.Code)
		}
	})
}

func TestProductionModeAuth(t *testing.T) {
	privateKey, pemPublicKey := generateTestKeyPair(t)
	middleware := NewAuthMiddleware("production", "sk_live_test_key", pemPublicKey, "", "")

	handler := middleware.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		orgID := GetOrgID(r.Context())
		userID := GetUserID(r.Context())
		w.Header().Set("X-Test-OrgID", orgID)
		w.Header().Set("X-Test-UserID", userID)
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("valid JWT token accepted", func(t *testing.T) {
		claims := &GradientClaims{
			OrgID: "org_123",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "user_456",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}
		tokenString := signTestJWT(t, privateKey, claims)

		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rec.Code)
		}
		if got := rec.Header().Get("X-Test-OrgID"); got != "org_123" {
			t.Errorf("Expected org_id 'org_123', got %q", got)
		}
		if got := rec.Header().Get("X-Test-UserID"); got != "user_456" {
			t.Errorf("Expected user_id 'user_456', got %q", got)
		}
	})

	t.Run("missing Authorization header rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", rec.Code)
		}
	})

	t.Run("invalid token rejected", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("Authorization", "Bearer invalid.token.here")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", rec.Code)
		}
	})

	t.Run("expired token rejected", func(t *testing.T) {
		claims := &GradientClaims{
			OrgID: "org_123",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "user_456",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), // expired
				IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
			},
		}
		tokenString := signTestJWT(t, privateKey, claims)

		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", rec.Code)
		}
	})

	t.Run("token signed with wrong key rejected", func(t *testing.T) {
		wrongKey, _ := rsa.GenerateKey(rand.Reader, 2048)
		claims := &GradientClaims{
			OrgID: "org_123",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "user_456",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}
		tokenString := signTestJWT(t, wrongKey, claims)

		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", rec.Code)
		}
	})

	t.Run("missing org_id in token rejected", func(t *testing.T) {
		claims := &GradientClaims{
			OrgID: "",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "user_456",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}
		tokenString := signTestJWT(t, privateKey, claims)

		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected 401, got %d", rec.Code)
		}
	})

	t.Run("X-Org-ID header does NOT override JWT org in production", func(t *testing.T) {
		claims := &GradientClaims{
			OrgID: "org_from_jwt",
			RegisteredClaims: jwt.RegisteredClaims{
				Subject:   "user_456",
				ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
				IssuedAt:  jwt.NewNumericDate(time.Now()),
			},
		}
		tokenString := signTestJWT(t, privateKey, claims)

		req := httptest.NewRequest("GET", "/api/v1/environments", nil)
		req.Header.Set("Authorization", "Bearer "+tokenString)
		req.Header.Set("X-Org-ID", "org_from_header_SHOULD_BE_IGNORED")
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200, got %d", rec.Code)
		}
		// The JWT org should be used, NOT the header
		if got := rec.Header().Get("X-Test-OrgID"); got != "org_from_jwt" {
			t.Errorf("Expected org_id 'org_from_jwt' (from JWT), got %q — X-Org-ID header override is a security hole!", got)
		}
	})
}

func TestProductionModeRequiresPEMKey(t *testing.T) {
	// Production mode (ClerkSecretKey set) but NO PEM key and NO JWT secret = should fail
	middleware := NewAuthMiddleware("production", "sk_live_test_key", "", "", "")

	handler := middleware.Authenticate(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest("GET", "/api/v1/environments", nil)
	req.Header.Set("Authorization", "Bearer some.jwt.token")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("Expected 401 when PEM key is missing in production, got %d", rec.Code)
	}
}

func TestGetOrgID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	// No org ID in context — should return empty string
	if got := GetOrgID(req.Context()); got != "" {
		t.Errorf("Expected empty string, got %q", got)
	}
}

func TestGetUserID(t *testing.T) {
	req := httptest.NewRequest("GET", "/", nil)
	if got := GetUserID(req.Context()); got != "" {
		t.Errorf("Expected empty string, got %q", got)
	}
}

func TestCORSMiddleware(t *testing.T) {
	handler := CORSMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	t.Run("adds CORS headers", func(t *testing.T) {
		req := httptest.NewRequest("GET", "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
			t.Errorf("Expected CORS origin *, got %q", got)
		}
		if got := rec.Header().Get("Access-Control-Allow-Methods"); got == "" {
			t.Error("Expected CORS methods header to be set")
		}
	})

	t.Run("OPTIONS returns 200", func(t *testing.T) {
		req := httptest.NewRequest("OPTIONS", "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected 200 for OPTIONS, got %d", rec.Code)
		}
	})
}
