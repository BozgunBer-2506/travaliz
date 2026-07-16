package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
)

var (
	ErrUnauthorized = errors.New("unauthorized")
	globalKF        keyfunc.Keyfunc
)

// Init fetches the JWKS from Supabase and caches the keyfunc. Must be called
// once at startup before any request is served; returns an error if the JWKS
// endpoint is unreachable.
func Init(ctx context.Context) error {
	supabaseURL := os.Getenv("SUPABASE_URL")
	if supabaseURL == "" {
		return fmt.Errorf("SUPABASE_URL not configured")
	}
	jwksURL := strings.TrimRight(supabaseURL, "/") + "/auth/v1/.well-known/jwks.json"
	kf, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		return fmt.Errorf("jwks init: %w", err)
	}
	globalKF = kf
	return nil
}

// EmailFromRequest validates the Supabase JWT in the Authorization header
// and returns the authenticated email claim. Returns ErrUnauthorized on failure.
func EmailFromRequest(r *http.Request) (string, error) {
	if globalKF == nil {
		return "", fmt.Errorf("auth not initialized")
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return "", ErrUnauthorized
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	token, err := jwt.Parse(tokenStr, globalKF.Keyfunc,
		jwt.WithAudience("authenticated"),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"ES256", "RS256"}),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || !token.Valid {
		return "", ErrUnauthorized
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", ErrUnauthorized
	}

	email, ok := claims["email"].(string)
	if !ok || email == "" {
		return "", ErrUnauthorized
	}

	return strings.ToLower(email), nil
}
