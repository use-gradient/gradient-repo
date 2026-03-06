package api

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const (
	ContextKeyOrgID  contextKey = "org_id"
	ContextKeyUserID contextKey = "user_id"
)

// jwksKey represents a single key from a JWKS response
type jwksKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
	Alg string `json:"alg"`
}

// jwksResponse is the JSON structure returned by /.well-known/jwks.json
type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

// jwksCache caches JWKS public keys with automatic refresh
type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey // kid → public key
	fetchedAt time.Time
	jwksURL   string
	authToken string // Bearer token for authenticated JWKS endpoints (e.g. Clerk Backend API)
	ttl       time.Duration
}

func newJWKSCache(jwksURL, authToken string) *jwksCache {
	return &jwksCache{
		jwksURL:   jwksURL,
		authToken: authToken,
		keys:      make(map[string]*rsa.PublicKey),
		ttl:       1 * time.Hour, // Refresh keys every hour
	}
}

func (c *jwksCache) getKey(kid string) (*rsa.PublicKey, error) {
	c.mu.RLock()
	key, ok := c.keys[kid]
	expired := time.Since(c.fetchedAt) > c.ttl
	c.mu.RUnlock()

	if ok && !expired {
		return key, nil
	}

	// Fetch fresh keys
	if err := c.refresh(); err != nil {
		// If we have a cached key and fetch failed, use it anyway
		if ok {
			return key, nil
		}
		return nil, fmt.Errorf("JWKS fetch failed: %w", err)
	}

	c.mu.RLock()
	key, ok = c.keys[kid]
	c.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("key ID %q not found in JWKS", kid)
	}
	return key, nil
}

// getAnyKey returns the first signing key (for JWTs without a kid header)
func (c *jwksCache) getAnyKey() (*rsa.PublicKey, error) {
	c.mu.RLock()
	expired := time.Since(c.fetchedAt) > c.ttl
	c.mu.RUnlock()

	if expired || len(c.keys) == 0 {
		if err := c.refresh(); err != nil {
			c.mu.RLock()
			defer c.mu.RUnlock()
			// Return any cached key if available
			for _, k := range c.keys {
				return k, nil
			}
			return nil, fmt.Errorf("JWKS fetch failed and no cached keys: %w", err)
		}
	}

	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, k := range c.keys {
		return k, nil
	}
	return nil, fmt.Errorf("no keys found in JWKS")
}

func (c *jwksCache) refresh() error {
	client := &http.Client{Timeout: 10 * time.Second}

	req, err := http.NewRequest("GET", c.jwksURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create JWKS request: %w", err)
	}
	if c.authToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.authToken)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch JWKS from %s: %w", c.jwksURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("JWKS endpoint returned %d", resp.StatusCode)
	}

	var jwks jwksResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Kty != "RSA" {
			continue
		}
		pub, err := parseRSAPublicKeyFromJWK(k)
		if err != nil {
			log.Printf("[auth] Skipping JWKS key %s: %v", k.Kid, err)
			continue
		}
		kid := k.Kid
		if kid == "" {
			kid = "_default"
		}
		keys[kid] = pub
	}

	if len(keys) == 0 {
		return fmt.Errorf("no valid RSA keys in JWKS response")
	}

	c.mu.Lock()
	c.keys = keys
	c.fetchedAt = time.Now()
	c.mu.Unlock()

	log.Printf("[auth] JWKS refreshed: %d keys loaded from %s", len(keys), c.jwksURL)
	return nil
}

func parseRSAPublicKeyFromJWK(k jwksKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}

	return &rsa.PublicKey{N: n, E: e}, nil
}

// ──────────────────────────────────────────────────────────────────────

type AuthMiddleware struct {
	devMode      bool
	pemPublicKey string     // Static PEM key (legacy Clerk)
	jwksCache    *jwksCache // Auto-fetching JWKS (preferred for Clerk)
	jwtSecret    []byte     // HMAC secret for Gradient-issued CLI tokens
}

func NewAuthMiddleware(env, clerkSecretKey, pemPublicKey, jwksURL, jwtSecret string) *AuthMiddleware {
	devMode := env == "development"
	if devMode {
		log.Println("[auth] Running in DEV MODE — accepting dev tokens + X-Org-ID/X-User-ID headers")
	} else if jwksURL != "" {
		log.Printf("[auth] Running in PRODUCTION MODE — JWKS from %s + Gradient HMAC tokens", jwksURL)
	} else if pemPublicKey != "" {
		log.Println("[auth] Running in PRODUCTION MODE — static PEM + Gradient HMAC tokens")
	} else {
		log.Println("[auth] WARNING: Production mode with no JWKS URL or PEM key — only HMAC tokens will work")
	}

	var cache *jwksCache
	if jwksURL != "" {
		authToken := ""
		if strings.Contains(jwksURL, "api.clerk.com") {
			authToken = clerkSecretKey
		}
		cache = newJWKSCache(jwksURL, authToken)
		if err := cache.refresh(); err != nil {
			log.Printf("[auth] WARNING: Initial JWKS fetch failed: %v (will retry on first request)", err)
		}
	}

	return &AuthMiddleware{
		devMode:      devMode,
		pemPublicKey: pemPublicKey,
		jwksCache:    cache,
		jwtSecret:    []byte(jwtSecret),
	}
}

func (m *AuthMiddleware) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip auth for health check
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		var orgID, userID string

		authHeader := r.Header.Get("Authorization")
		hasToken := strings.HasPrefix(authHeader, "Bearer ")

		if hasToken {
			tokenString := strings.TrimPrefix(authHeader, "Bearer ")

			// Try parsing as a real JWT (Gradient HMAC or Clerk RSA)
			claims, err := m.parseToken(tokenString)
			if err == nil {
				userID = claims.Subject
				orgID = claims.OrgID

				// Dev mode: allow X-Org-ID header to override JWT org_id.
				// This makes `gc org switch` work during local development.
				// In production, the JWT org is ALWAYS authoritative (security).
				if m.devMode {
					if headerOrg := r.Header.Get("X-Org-ID"); headerOrg != "" {
						orgID = headerOrg
					}
				}
			} else if m.devMode && strings.HasPrefix(tokenString, "dev-token-") {
				// Dev mode: accept dev tokens (non-JWT strings from device auth)
				orgID = r.Header.Get("X-Org-ID")
				userID = r.Header.Get("X-User-ID")
				if orgID == "" {
					orgID = "dev-org"
				}
				if userID == "" {
					userID = "dev-user"
				}
			} else {
				writeError(w, http.StatusUnauthorized, "invalid token: "+err.Error())
				return
			}
		} else if m.devMode {
			// Dev mode without any token: trust headers
			orgID = r.Header.Get("X-Org-ID")
			userID = r.Header.Get("X-User-ID")
			if orgID == "" {
				orgID = "dev-org"
			}
			if userID == "" {
				userID = "dev-user"
			}
		} else {
			writeError(w, http.StatusUnauthorized, "missing or invalid authorization header")
			return
		}

		if orgID == "" || userID == "" {
			writeError(w, http.StatusUnauthorized, "token missing org_id or user_id")
			return
		}

		ctx := context.WithValue(r.Context(), ContextKeyOrgID, orgID)
		ctx = context.WithValue(ctx, ContextKeyUserID, userID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

type GradientClaims struct {
	OrgID string `json:"org_id"`
	jwt.RegisteredClaims
}

func (m *AuthMiddleware) parseToken(tokenString string) (*GradientClaims, error) {
	// Strategy 1: Gradient-issued HMAC token (for CLI — long-lived, signed with JWT_SECRET)
	if len(m.jwtSecret) > 0 {
		claims, err := m.parseTokenWithHMAC(tokenString)
		if err == nil {
			return claims, nil
		}
		// Not an HMAC token — fall through to Clerk verification
	}

	// Strategy 2: JWKS URL (Clerk RSA tokens — from web UI or direct Clerk sessions)
	if m.jwksCache != nil {
		claims, err := m.parseTokenWithJWKS(tokenString)
		if err == nil {
			return claims, nil
		}
	}

	// Strategy 3: Static PEM key (legacy Clerk)
	if m.pemPublicKey != "" {
		return m.parseTokenWithPEM(tokenString)
	}

	return nil, fmt.Errorf("token verification failed — not a valid Gradient or Clerk JWT")
}

func (m *AuthMiddleware) parseTokenWithHMAC(tokenString string) (*GradientClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &GradientClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return m.jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*GradientClaims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

func (m *AuthMiddleware) parseTokenWithJWKS(tokenString string) (*GradientClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &GradientClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, _ := token.Header["kid"].(string)
		if kid != "" {
			return m.jwksCache.getKey(kid)
		}
		return m.jwksCache.getAnyKey()
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*GradientClaims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

func (m *AuthMiddleware) parseTokenWithPEM(tokenString string) (*GradientClaims, error) {
	pubKey, err := jwt.ParseRSAPublicKeyFromPEM([]byte(m.pemPublicKey))
	if err != nil {
		return nil, fmt.Errorf("failed to parse PEM public key: %w", err)
	}

	token, err := jwt.ParseWithClaims(tokenString, &GradientClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return pubKey, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*GradientClaims)
	if !ok || !token.Valid {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

// Helper functions for extracting auth info from context
func GetOrgID(ctx context.Context) string {
	orgID, _ := ctx.Value(ContextKeyOrgID).(string)
	return orgID
}

func GetUserID(ctx context.Context) string {
	userID, _ := ctx.Value(ContextKeyUserID).(string)
	return userID
}

// RequestLogger logs all incoming requests
func RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("[%s] %s %s (%s)", r.Method, r.URL.Path, r.RemoteAddr, time.Since(start))
	})
}

// CORS middleware
func CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Org-ID, X-User-ID")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
