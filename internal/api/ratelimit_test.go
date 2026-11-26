package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"databridge/internal/store"
)

func TestLoginRateLimiterAndSecurityHeaders(t *testing.T) {
	t.Setenv("DATABRIDGE_AUTH_PASSWORD", "secret-pass")
	t.Setenv("DATABRIDGE_CORS_ORIGIN", "http://localhost:5173")

	dir := t.TempDir()
	appStore, err := store.New(filepath.Join(dir, "db.sqlite"), filepath.Join(dir, "db.key"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer appStore.Close()

	handler := New(appStore)

	// Verify security headers & CORS DELETE method on preflight.
	req := httptest.NewRequest(http.MethodOptions, "/api/connections/1", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("OPTIONS status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); !strings.Contains(got, "DELETE") {
		t.Fatalf("Access-Control-Allow-Methods = %q, want DELETE included", got)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
	if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
		t.Fatalf("X-Frame-Options = %q, want DENY", got)
	}

	// Verify login rate limiting triggers 429 after 10 failed attempts
	// (attempts 1-3 return 401 invalid_credentials; attempts 4-10 return 400 captcha_required).
	for i := 0; i < 10; i++ {
		loginReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		loginReq.RemoteAddr = "192.0.2.10:12345"
		loginRec := httptest.NewRecorder()
		handler.ServeHTTP(loginRec, loginReq)
		wantStatus := http.StatusUnauthorized
		if i >= 3 {
			wantStatus = http.StatusBadRequest
		}
		if loginRec.Code != wantStatus {
			t.Fatalf("attempt %d status = %d, want %d", i+1, loginRec.Code, wantStatus)
		}
	}
	blockedReq := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"secret-pass"}`))
	blockedReq.RemoteAddr = "192.0.2.10:12345"
	blockedRec := httptest.NewRecorder()
	handler.ServeHTTP(blockedRec, blockedReq)
	if blockedRec.Code != http.StatusTooManyRequests {
		t.Fatalf("blocked attempt status = %d, want 429", blockedRec.Code)
	}
}

func TestLoginRateLimiterWindowReset(t *testing.T) {
	limiter := newLoginRateLimiter(2, 20*time.Millisecond)
	if !limiter.Allow("10.0.0.1") || !limiter.Allow("10.0.0.1") {
		t.Fatalf("expected first two attempts to be allowed")
	}
	if limiter.Allow("10.0.0.1") {
		t.Fatalf("expected third attempt in window to be blocked")
	}
	limiter.Reset("10.0.0.1")
	if !limiter.Allow("10.0.0.1") {
		t.Fatalf("expected attempt after Reset to be allowed")
	}
}
