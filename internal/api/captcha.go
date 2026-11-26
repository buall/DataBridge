package api

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"
)

const (
	captchaFailureThreshold = 3
	captchaFailureWindow    = 15 * time.Minute
	captchaTTL              = 5 * time.Minute
	captchaAlphabet         = "23456789ABCDEFGHJKLMNPQRSTUVWXYZ"
)

type captchaFailureBucket struct {
	count     int
	updatedAt time.Time
}

type captchaChallenge struct {
	code      string
	expiresAt time.Time
}

type captchaManager struct {
	mu         sync.Mutex
	failures   map[string]captchaFailureBucket
	challenges map[string]captchaChallenge
}

func newCaptchaManager() *captchaManager {
	return &captchaManager{
		failures:   make(map[string]captchaFailureBucket),
		challenges: make(map[string]captchaChallenge),
	}
}

func (m *captchaManager) Required(ip string) bool {
	if m == nil || ip == "" {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	bucket, ok := m.failures[ip]
	if !ok || now.Sub(bucket.updatedAt) >= captchaFailureWindow {
		return false
	}
	return bucket.count >= captchaFailureThreshold
}

func (m *captchaManager) RecordFailure(ip string) int {
	if m == nil || ip == "" {
		return 0
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.failures) > 1024 {
		for k, b := range m.failures {
			if now.Sub(b.updatedAt) >= captchaFailureWindow {
				delete(m.failures, k)
			}
		}
	}
	bucket, ok := m.failures[ip]
	if !ok || now.Sub(bucket.updatedAt) >= captchaFailureWindow {
		bucket = captchaFailureBucket{count: 1, updatedAt: now}
	} else {
		bucket.count++
		bucket.updatedAt = now
	}
	m.failures[ip] = bucket
	return bucket.count
}

func (m *captchaManager) ResetFailures(ip string) {
	if m == nil || ip == "" {
		return
	}
	m.mu.Lock()
	delete(m.failures, ip)
	m.mu.Unlock()
}

func (m *captchaManager) Create() (string, string, error) {
	code, err := randomCaptchaCode(4)
	if err != nil {
		return "", "", err
	}
	var idBytes [12]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return "", "", err
	}
	id := hex.EncodeToString(idBytes[:])
	svg := renderCaptchaSVG(code)
	dataURI := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))

	now := time.Now()
	m.mu.Lock()
	if len(m.challenges) >= 2048 {
		for k, ch := range m.challenges {
			if now.After(ch.expiresAt) {
				delete(m.challenges, k)
			}
		}
		if len(m.challenges) >= 2048 {
			clear(m.challenges)
		}
	}
	m.challenges[id] = captchaChallenge{
		code:      code,
		expiresAt: now.Add(captchaTTL),
	}
	m.mu.Unlock()

	return id, dataURI, nil
}

func (m *captchaManager) Verify(id, code string) bool {
	if m == nil {
		return true
	}
	id = strings.TrimSpace(id)
	code = strings.ToUpper(strings.TrimSpace(code))
	if id == "" || code == "" {
		return false
	}
	now := time.Now()
	m.mu.Lock()
	ch, ok := m.challenges[id]
	if ok {
		delete(m.challenges, id)
	}
	m.mu.Unlock()
	if !ok || now.After(ch.expiresAt) {
		return false
	}
	return ch.code == code
}

func randomCaptchaCode(length int) (string, error) {
	var b strings.Builder
	b.Grow(length)
	maxIdx := big.NewInt(int64(len(captchaAlphabet)))
	for i := 0; i < length; i++ {
		n, err := rand.Int(rand.Reader, maxIdx)
		if err != nil {
			return "", err
		}
		b.WriteByte(captchaAlphabet[n.Int64()])
	}
	return b.String(), nil
}

func randomIntRange(minVal, maxVal int) int {
	if maxVal <= minVal {
		return minVal
	}
	n, err := rand.Int(rand.Reader, big.NewInt(int64(maxVal-minVal+1)))
	if err != nil {
		return minVal
	}
	return minVal + int(n.Int64())
}

func renderCaptchaSVG(code string) string {
	const width = 132
	const height = 42
	palette := []string{"#4f46e5", "#0284c7", "#059669", "#d97706", "#dc2626", "#7c3aed"}

	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height)
	b.WriteString(`<rect width="100%" height="100%" rx="8" fill="#f8fafc"/>`)

	for i := 0; i < 5; i++ {
		x1 := randomIntRange(0, width)
		y1 := randomIntRange(0, height)
		x2 := randomIntRange(0, width)
		y2 := randomIntRange(0, height)
		color := palette[randomIntRange(0, len(palette)-1)]
		fmt.Fprintf(&b, `<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="%s" stroke-width="1.2" stroke-opacity="0.35"/>`, x1, y1, x2, y2, color)
	}

	for i := 0; i < 18; i++ {
		cx := randomIntRange(4, width-4)
		cy := randomIntRange(4, height-4)
		color := palette[randomIntRange(0, len(palette)-1)]
		fmt.Fprintf(&b, `<circle cx="%d" cy="%d" r="1.2" fill="%s" fill-opacity="0.35"/>`, cx, cy, color)
	}

	for i, ch := range code {
		x := 18 + i*27 + randomIntRange(-3, 3)
		y := 28 + randomIntRange(-4, 4)
		rot := randomIntRange(-16, 16)
		color := palette[randomIntRange(0, len(palette)-1)]
		fmt.Fprintf(
			&b,
			`<text x="%d" y="%d" fill="%s" font-family="ui-monospace,SFMono-Regular,Menlo,Monaco,Consolas,monospace" font-size="22" font-weight="700" transform="rotate(%d %d %d)">%c</text>`,
			x, y, color, rot, x, y, ch,
		)
	}
	b.WriteString(`</svg>`)
	return b.String()
}
