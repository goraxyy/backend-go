package auth

import (
	"context"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"os"
	"spacesona-go-backend/logging"
	"strings"
)

type contextKey string

// DeviceContextKey is where Middleware stashes the verified device claims so
// handlers can trust the identity in the token instead of the request body.
const DeviceContextKey contextKey = "spaceona.device"

func bearerToken(r *http.Request) (string, bool) {
	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return "", false
	}
	parts := strings.Fields(authHeader)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

// Middleware verifies a device token and attaches its claims to the request.
func Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token, ok := bearerToken(r)
		if !ok {
			logging.RequestsAuthenticatedFailed.Inc()
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		claims, validationErr := ParseToken(token)
		if validationErr != nil {
			slog.Warn("token rejected", "error", validationErr.Error())
			logging.RequestsAuthenticatedFailed.Inc()
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		device, claimsErr := DeviceClaimsFromToken(claims)
		if claimsErr != nil {
			slog.Warn("token carries unusable claims", "error", claimsErr.Error())
			logging.RequestsAuthenticatedFailed.Inc()
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}

		logging.RequestsAuthenticated.Inc()
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), DeviceContextKey, device)))
	}
}

// DeviceFromContext returns the claims Middleware attached to this request.
func DeviceFromContext(ctx context.Context) (DeviceClaims, bool) {
	device, ok := ctx.Value(DeviceContextKey).(DeviceClaims)
	return device, ok
}

// AdminMiddleware guards the operator endpoints.
//
// A plain token check is not enough here: every deployed sensor holds a valid
// token, so Middleware alone would let any board in the field mark every other
// board invalid. This requires a separate operator secret.
//
// This is a stopgap. The intended design is role-based access derived from the
// Google sign-in flow, which cannot be used yet because that flow never
// populates its own cookie (see GO-08 in TICKETS.md).
func AdminMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adminKey := os.Getenv("ADMIN_API_KEY")
		if len(adminKey) < 32 {
			slog.Error("ADMIN_API_KEY is unset or shorter than 32 bytes; refusing admin request")
			http.Error(w, "admin api is not configured", http.StatusServiceUnavailable)
			return
		}
		token, ok := bearerToken(r)
		if !ok {
			logging.RequestsAuthenticatedFailed.Inc()
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		if subtle.ConstantTimeCompare([]byte(token), []byte(adminKey)) != 1 {
			logging.RequestsAuthenticatedFailed.Inc()
			http.Error(w, "not authenticated", http.StatusUnauthorized)
			return
		}
		logging.RequestsAuthenticated.Inc()
		next.ServeHTTP(w, r)
	}
}
