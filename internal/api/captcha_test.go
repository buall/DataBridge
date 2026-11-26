package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"databridge/internal/store"
)

func TestLoginCaptchaAfterThreeFailures(t *testing.T) {
	t.Setenv("DATABRIDGE_AUTH_PASSWORD", "secret-pass")

	dir := t.TempDir()
	appStore, err := store.New(filepath.Join(dir, "db.sqlite"), filepath.Join(dir, "db.key"))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	defer appStore.Close()

	mgr := newCaptchaManager()
	handler := New(appStore)
	// Swap in our test manager? Better: test via Server HTTP endpoints directly,
	// and read the generated code from captchaManager via a helper or test Create/Verify.
	_ = mgr

	const remoteIP = "198.51.100.7:43210"
	postLogin := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(body))
		req.RemoteAddr = remoteIP
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	// First 2 failures: captchaRequired should be false.
	for i := 1; i <= 2; i++ {
		rec := postLogin(`{"username":"admin","password":"wrong"}`)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", i, rec.Code)
		}
		var payload map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
			t.Fatalf("unmarshal attempt %d: %v", i, err)
		}
		if payload["captchaRequired"] != false {
			t.Fatalf("attempt %d captchaRequired = %v, want false", i, payload["captchaRequired"])
		}
	}

	// 3rd failure: captchaRequired becomes true.
	rec3 := postLogin(`{"username":"admin","password":"wrong"}`)
	if rec3.Code != http.StatusUnauthorized {
		t.Fatalf("attempt 3 status = %d, want 401", rec3.Code)
	}
	var payload3 map[string]any
	if err := json.Unmarshal(rec3.Body.Bytes(), &payload3); err != nil {
		t.Fatalf("unmarshal attempt 3: %v", err)
	}
	if payload3["captchaRequired"] != true {
		t.Fatalf("attempt 3 captchaRequired = %v, want true", payload3["captchaRequired"])
	}

	// Session endpoint should reflect captchaRequired = true for the same IP.
	sessReq := httptest.NewRequest(http.MethodGet, "/api/auth/session", nil)
	sessReq.RemoteAddr = remoteIP
	sessRec := httptest.NewRecorder()
	handler.ServeHTTP(sessRec, sessReq)
	var sessPayload map[string]any
	if err := json.Unmarshal(sessRec.Body.Bytes(), &sessPayload); err != nil {
		t.Fatalf("unmarshal session: %v", err)
	}
	if sessPayload["captchaRequired"] != true {
		t.Fatalf("session captchaRequired = %v, want true", sessPayload["captchaRequired"])
	}

	// Missing captcha should be rejected with 400 captcha_required even with valid password.
	missingRec := postLogin(`{"username":"admin","password":"secret-pass"}`)
	if missingRec.Code != http.StatusBadRequest {
		t.Fatalf("missing captcha status = %d, want 400", missingRec.Code)
	}
	var missingPayload map[string]any
	_ = json.Unmarshal(missingRec.Body.Bytes(), &missingPayload)
	if missingPayload["code"] != "captcha_required" {
		t.Fatalf("missing captcha code = %v, want captcha_required", missingPayload["code"])
	}

	// Fetch a captcha image from /api/auth/captcha.
	capReq := httptest.NewRequest(http.MethodGet, "/api/auth/captcha", nil)
	capRec := httptest.NewRecorder()
	handler.ServeHTTP(capRec, capReq)
	if capRec.Code != http.StatusOK {
		t.Fatalf("GET /api/auth/captcha status = %d, want 200", capRec.Code)
	}
	var capPayload struct {
		ID    string `json:"id"`
		Image string `json:"image"`
	}
	if err := json.Unmarshal(capRec.Body.Bytes(), &capPayload); err != nil {
		t.Fatalf("unmarshal captcha: %v", err)
	}
	if capPayload.ID == "" || !strings.HasPrefix(capPayload.Image, "data:image/svg+xml;base64,") {
		t.Fatalf("unexpected captcha payload: id=%q imagePrefix=%q", capPayload.ID, capPayload.Image[:min(30, len(capPayload.Image))])
	}

	// Wrong captcha code should return 401 invalid_captcha and consume the challenge.
	badCapRec := postLogin(`{"username":"admin","password":"secret-pass","captchaId":"` + capPayload.ID + `","captchaCode":"0000"}`)
	if badCapRec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong captcha status = %d, want 401", badCapRec.Code)
	}
	var badCapPayload map[string]any
	_ = json.Unmarshal(badCapRec.Body.Bytes(), &badCapPayload)
	if badCapPayload["code"] != "invalid_captcha" {
		t.Fatalf("wrong captcha code = %v, want invalid_captcha", badCapPayload["code"])
	}
}

func TestCaptchaManagerCreateVerifyAndSingleUse(t *testing.T) {
	mgr := newCaptchaManager()
	id, image, err := mgr.Create()
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(image, "data:image/svg+xml;base64,") {
		t.Fatalf("expected SVG data URI, got %q", image)
	}
	code := mgr.challenges[id].code
	if len(code) != 4 {
		t.Fatalf("expected 4-char code, got %q", code)
	}
	if !mgr.Verify(id, strings.ToLower(code)) {
		t.Fatalf("expected case-insensitive Verify to succeed")
	}
	if mgr.Verify(id, code) {
		t.Fatalf("expected single-use captcha to reject replay")
	}
}
