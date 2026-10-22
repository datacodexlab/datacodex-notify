// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const MaxBodySize = 32 * 1024 // 32 KiB

type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
	}
}

func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	var valid []time.Time
	for _, t := range rl.requests[key] {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.limit {
		rl.requests[key] = valid
		return false
	}

	rl.requests[key] = append(valid, now)
	return true
}

func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]interface{}{
		"error":   message,
		"status":  status,
		"success": false,
	})
}

// HostGuard rejects requests whose Host header is not the loopback address on
// the server's own port, which blocks DNS-rebinding attacks from web pages.
func (s *Server) HostGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.ToLower(r.Host)
		if host != fmt.Sprintf("127.0.0.1:%d", s.port) && host != fmt.Sprintf("localhost:%d", s.port) {
			WriteError(w, http.StatusForbidden, "Invalid Host header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) LimitBodySize(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodySize)
		next.ServeHTTP(w, r)
	})
}

func (s *Server) RequireAgentAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Reject any request with an Origin header (blocks browser-originated CSRF/XHR)
		if r.Header.Get("Origin") != "" {
			WriteError(w, http.StatusForbidden, "Origin header forbidden on agent API")
			return
		}

		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			WriteError(w, http.StatusUnauthorized, "Missing or malformed Authorization header")
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if subtle.ConstantTimeCompare([]byte(token), []byte(s.currentToken())) != 1 {
			WriteError(w, http.StatusUnauthorized, "Invalid bearer token")
			return
		}

		// Rate limiting: 60 req/min
		if !s.rateLimiter.Allow(token) {
			WriteError(w, http.StatusTooManyRequests, "Rate limit exceeded (60 req/min)")
			return
		}

		next(w, r)
	}
}

func (s *Server) RequireUIAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("dcn_session")
		if err != nil || cookie.Value == "" {
			WriteError(w, http.StatusUnauthorized, "Missing UI session cookie")
			return
		}

		s.sessionsMu.RLock()
		csrfToken, exists := s.uiSessions[cookie.Value]
		s.sessionsMu.RUnlock()

		if !exists {
			WriteError(w, http.StatusUnauthorized, "Invalid UI session")
			return
		}

		// For state-modifying requests, verify CSRF header
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
			headerCSRF := r.Header.Get("X-CSRF-Token")
			if subtle.ConstantTimeCompare([]byte(headerCSRF), []byte(csrfToken)) != 1 {
				WriteError(w, http.StatusForbidden, "Invalid CSRF token")
				return
			}
		}

		next(w, r)
	}
}
