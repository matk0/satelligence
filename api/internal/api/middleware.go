package api

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/httprate"
	"github.com/google/uuid"
	"github.com/trandor/trandor/config"
	"github.com/trandor/trandor/internal/l402"
	"github.com/trandor/trandor/internal/session"
)

// L402Middleware handles L402 authentication
func L402Middleware(l402Service *l402.Service, sessionStore *session.Store) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Check for L402 header
			authHeader := r.Header.Get("Authorization")

			// No auth header - issue challenge for new session
			if authHeader == "" {
				issueChallenge(w, l402Service, r)
				return
			}

			// Parse L402 header: "L402 <macaroon>:<preimage>"
			if !strings.HasPrefix(authHeader, "L402 ") {
				l402.WriteError(w, http.StatusUnauthorized, "invalid_auth", "expected L402 authorization")
				return
			}

			credentials := strings.TrimPrefix(authHeader, "L402 ")
			parts := strings.SplitN(credentials, ":", 2)
			if len(parts) != 2 {
				l402.WriteError(w, http.StatusUnauthorized, "invalid_auth", "invalid L402 format")
				return
			}

			macaroon := parts[0]
			// preimage := parts[1] // We could verify this against the invoice

			// Verify macaroon
			data, err := l402Service.VerifyMacaroon(macaroon)
			if err != nil {
				if err == l402.ErrExpiredMacaroon {
					l402.WriteError(w, http.StatusUnauthorized, "expired_session", "session has expired")
				} else {
					l402.WriteError(w, http.StatusUnauthorized, "invalid_macaroon", "invalid or expired macaroon")
				}
				return
			}

			// Get session from database
			sessionID, err := uuid.Parse(data.SessionID)
			if err != nil {
				l402.WriteError(w, http.StatusUnauthorized, "invalid_session", "invalid session ID")
				return
			}

			sess, err := sessionStore.GetByID(r.Context(), sessionID)
			if err != nil {
				if err == session.ErrSessionNotFound {
					l402.WriteError(w, http.StatusUnauthorized, "session_not_found", "session not found")
				} else {
					slog.Error("failed to get session", "error", err)
					l402.WriteError(w, http.StatusInternalServerError, "internal_error", "failed to retrieve session")
				}
				return
			}

			// Check if banned
			if sess.Banned {
				l402.WriteError(w, http.StatusForbidden, "session_banned", "session has been banned")
				return
			}

			// Add session to context
			r = setSessionInContext(r, sess)
			next.ServeHTTP(w, r)
		})
	}
}

func issueChallenge(w http.ResponseWriter, l402Service *l402.Service, r *http.Request) {
	result, err := l402Service.CreateNewSession(r.Context())
	if err != nil {
		slog.Error("failed to create session", "error", err.Error())
		l402.WriteError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}

	l402.WriteChallenge(w, result.Macaroon, result.Invoice, result.AmountSats)
}

// RateLimitMiddleware creates rate limiting middleware
func RateLimitMiddleware(cfg *config.Config) func(http.Handler) http.Handler {
	return httprate.Limit(
		cfg.RateLimitRPM,
		time.Minute,
		httprate.WithKeyFuncs(func(r *http.Request) (string, error) {
			// Rate limit by session from context, or by IP if no session
			sess := getSessionFromContext(r.Context())
			if sess != nil {
				return "session:" + sess.ID.String(), nil
			}
			return "ip:" + r.RemoteAddr, nil
		}),
		httprate.WithLimitHandler(func(w http.ResponseWriter, r *http.Request) {
			l402.WriteError(w, http.StatusTooManyRequests, "rate_limited", "too many requests")
		}),
	)
}

// LoggingMiddleware logs requests
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Wrap response writer to capture status code
		wrapped := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		next.ServeHTTP(wrapped, r)

		slog.Info("request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", wrapped.statusCode,
			"duration", time.Since(start),
			"remote_addr", r.RemoteAddr,
		)
	})
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// Flush implements http.Flusher for streaming support
func (w *responseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

// RecoveryMiddleware recovers from panics
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if err := recover(); err != nil {
				slog.Error("panic recovered", "error", err)
				l402.WriteError(w, http.StatusInternalServerError, "internal_error", "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
