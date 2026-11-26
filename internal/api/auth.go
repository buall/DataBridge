package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const sessionCookieName = "databridge_session"

type authConfig struct {
	username        string
	passwordHash    []byte
	sessionSecret   []byte
	cookieSecure    bool
	localOnlyBypass bool
}

func loadAuthConfig() authConfig {
	username := strings.TrimSpace(envString("DATABRIDGE_AUTH_USER", "admin"))
	password := os.Getenv("DATABRIDGE_AUTH_PASSWORD")
	hash := strings.TrimSpace(os.Getenv("DATABRIDGE_AUTH_PASSWORD_HASH"))
	secret := strings.TrimSpace(os.Getenv("DATABRIDGE_SESSION_SECRET"))
	cfg := authConfig{username: username, cookieSecure: envBool("DATABRIDGE_COOKIE_SECURE", false)}
	if cfg.username == "" {
		cfg.username = "admin"
	}
	switch {
	case hash != "":
		cfg.passwordHash = []byte(hash)
	case password != "":
		value, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
		if err != nil {
			log.Printf("auth: bcrypt password initialization failed: %v", err)
			break
		}
		cfg.passwordHash = value
	default:
		cfg.localOnlyBypass = true
		log.Printf("auth: DATABRIDGE_AUTH_PASSWORD is not set; browser/API access is limited to localhost")
	}
	if secret != "" {
		cfg.sessionSecret = []byte(secret)
	} else if cfg.passwordHash != nil {
		log.Printf("auth: DATABRIDGE_SESSION_SECRET is not set; deriving session signing key from password hash (set DATABRIDGE_SESSION_SECRET in production)")
		sum := sha256.Sum256(append([]byte("databridge-session:"), cfg.passwordHash...))
		cfg.sessionSecret = sum[:]
	}
	return cfg
}

func (cfg authConfig) enabled() bool {
	return len(cfg.passwordHash) > 0 && len(cfg.sessionSecret) > 0
}

func (cfg authConfig) checkPassword(username, password string) bool {
	if !cfg.enabled() || subtle.ConstantTimeCompare([]byte(username), []byte(cfg.username)) != 1 {
		return false
	}
	return bcrypt.CompareHashAndPassword(cfg.passwordHash, []byte(password)) == nil
}

func (cfg authConfig) setSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    cfg.signSession(time.Now().Add(12 * time.Hour)),
		Path:     "/",
		MaxAge:   int((12 * time.Hour).Seconds()),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cfg.cookieSecure,
	})
}

func (cfg authConfig) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   cfg.cookieSecure,
	})
}

func (cfg authConfig) validSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return false
	}
	var session struct {
		Username  string `json:"u"`
		ExpiresAt int64  `json:"e"`
		Nonce     string `json:"n"`
	}
	if err := json.Unmarshal(payload, &session); err != nil {
		return false
	}
	if session.Username != cfg.username || session.ExpiresAt < time.Now().Unix() {
		return false
	}
	mac := hmac.New(sha256.New, cfg.sessionSecret)
	mac.Write([]byte(parts[0]))
	expected := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(parts[1]), []byte(expected))
}

func (cfg authConfig) signSession(expiresAt time.Time) string {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		panic(err)
	}
	payload, err := json.Marshal(struct {
		Username  string `json:"u"`
		ExpiresAt int64  `json:"e"`
		Nonce     string `json:"n"`
	}{Username: cfg.username, ExpiresAt: expiresAt.Unix(), Nonce: hex.EncodeToString(nonce[:])})
	if err != nil {
		panic(err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, cfg.sessionSecret)
	mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (cfg authConfig) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !cfg.enabled() {
			if cfg.localOnlyBypass && isLocalRequest(r) {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, http.StatusForbidden, "auth_not_configured", "authentication is not configured; set DATABRIDGE_AUTH_PASSWORD")
			return
		}
		if cfg.validSession(r) {
			next.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusUnauthorized, "unauthorized", "please sign in")
	})
}

func isLocalRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}

func envString(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
