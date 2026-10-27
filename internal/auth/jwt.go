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
	"github.com/google/uuid"
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
func requestClaims(r *http.Request) (jwt.MapClaims, error) {
	if globalKF == nil {
		return nil, fmt.Errorf("auth not initialized")
	}
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, ErrUnauthorized
	}
	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	token, err := jwt.Parse(tokenStr, globalKF.Keyfunc,
		jwt.WithAudience("authenticated"),
		jwt.WithExpirationRequired(),
		jwt.WithValidMethods([]string{"ES256", "RS256"}),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil || !token.Valid {
		return nil, ErrUnauthorized
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, ErrUnauthorized
	}
	return claims, nil
}

func EmailFromRequest(r *http.Request) (string, error) {
	claims, err := requestClaims(r)
	if err != nil {
		return "", err
	}
	email, ok := claims["email"].(string)
	if !ok || email == "" {
		return "", ErrUnauthorized
	}

	return strings.ToLower(email), nil
}

// Checkout ownership uses the stable Supabase subject, never an email supplied
// by the browser. Existing email-based flight routes keep their behavior.
func SubjectFromRequest(r *http.Request) (string, error) {
	claims, err := requestClaims(r)
	if err != nil {
		return "", err
	}
	sub, _ := claims["sub"].(string)
	id, err := uuid.Parse(sub)
	if err != nil || id == uuid.Nil {
		return "", ErrUnauthorized
	}
	return id.String(), nil
}
