package httpx

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/zenstar13/survey-prototype/internal/kernel/errs"
)

type Middleware func(http.Handler) http.Handler

// Chain applies middlewares so that the first one listed is the outermost.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

type ctxKey int

const requestIDKey ctxKey = iota

func RequestIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(requestIDKey).(string)
	return v
}

// RequestID assigns every request an id, echoed in the X-Request-Id header.
func RequestID() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			var b [8]byte
			_, _ = rand.Read(b[:])
			rid := hex.EncodeToString(b[:])
			w.Header().Set("X-Request-Id", rid)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), requestIDKey, rid)))
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

// AccessLog logs method, path, status and latency (never bodies).
func AccessLog(log *slog.Logger, server string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			log.InfoContext(r.Context(), "http request",
				"server", server, "method", r.Method, "path", r.URL.Path, "status", rec.status,
				"duration", time.Since(start).String(), "request_id", RequestIDFrom(r.Context()))
		})
	}
}

// Recoverer turns panics into 500s instead of killing the connection.
func Recoverer(log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if p := recover(); p != nil {
					log.ErrorContext(r.Context(), "panic", "panic", p, "path", r.URL.Path)
					WriteJSON(w, http.StatusInternalServerError, errorBody{errorDetail{Code: "internal", Message: "internal server error"}})
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// SecurityHeaders sets conservative defaults for API and UI responses.
func SecurityHeaders() Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("X-Frame-Options", "DENY")
			h.Set("Referrer-Policy", "no-referrer")
			h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; frame-ancestors 'none'")
			next.ServeHTTP(w, r)
		})
	}
}

// RateLimit rejects requests whose client exceeds the limiter's budget.
func RateLimit(l *RateLimiter, log *slog.Logger) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !l.Allow(ClientIP(r), time.Now()) {
				w.Header().Set("Retry-After", "60")
				WriteError(w, r, log, errs.TooMany("rate_limited", "too many requests, slow down"))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ClientIP uses the TCP peer address. X-Forwarded-For is intentionally ignored:
// trusting it is only safe behind a known proxy (a deployment-time setting).
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// RateLimiter is an in-memory token bucket per key. Good enough for a single
// instance; a multi-instance deployment would move this to a shared store or
// the edge proxy.
type RateLimiter struct {
	mu      sync.Mutex
	perSec  float64
	burst   float64
	buckets map[string]*bucket
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewRateLimiter allows perMinute requests per key on average, with bursts up to burst.
func NewRateLimiter(perMinute, burst int) *RateLimiter {
	return &RateLimiter{perSec: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}}
}

func (l *RateLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.buckets) > 10_000 { // crude memory bound
		for k, b := range l.buckets {
			if now.Sub(b.last) > 10*time.Minute {
				delete(l.buckets, k)
			}
		}
	}
	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens += now.Sub(b.last).Seconds() * l.perSec
	if b.tokens > l.burst {
		b.tokens = l.burst
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
