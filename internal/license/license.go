package license

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ProductName        = "databridge"
	SchemaVersion      = 1
	maxLicenseBytes    = 1024 * 1024
	identityFileName   = "deployment.key"
	licenseFileName    = "license.json"
	defaultHTTPTimeout = 15 * time.Second
)

var (
	ErrNotActivated = errors.New("license is not activated")
	ErrInvalid      = errors.New("license is invalid")
)

type Config struct {
	Directory      string
	ProductVersion string
	PublicKey      string
	ActivationURL  string
	HTTPClient     *http.Client
}

type Manager struct {
	mu             sync.RWMutex
	directory      string
	productVersion string
	verifyKey      ed25519.PublicKey
	activationURL  string
	httpClient     *http.Client
	privateKey     ed25519.PrivateKey
	publicKey      ed25519.PublicKey
	deploymentID   string
	claims         *Claims
	state          string
	message        string
}

type RequestPayload struct {
	SchemaVersion       int       `json:"schemaVersion"`
	Product             string    `json:"product"`
	DeploymentID        string    `json:"deploymentId"`
	DeploymentPublicKey string    `json:"deploymentPublicKey"`
	CreatedAt           time.Time `json:"createdAt"`
}

type SignedRequest struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type Claims struct {
	SchemaVersion     int        `json:"schemaVersion"`
	LicenseID         string     `json:"licenseId"`
	CustomerID        string     `json:"customerId"`
	CustomerName      string     `json:"customerName,omitempty"`
	DeploymentID      string     `json:"deploymentId"`
	DeploymentKeyHash string     `json:"deploymentKeyHash"`
	Product           string     `json:"product"`
	Edition           string     `json:"edition"`
	Features          []string   `json:"features,omitempty"`
	MaxVersion        string     `json:"maxVersion,omitempty"`
	IssuedAt          time.Time  `json:"issuedAt"`
	ExpiresAt         *time.Time `json:"expiresAt,omitempty"`
}

type Envelope struct {
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type Status struct {
	Activated        bool       `json:"activated"`
	State            string     `json:"state"`
	Message          string     `json:"message"`
	DeploymentID     string     `json:"deploymentId"`
	ProductVersion   string     `json:"productVersion"`
	OnlineActivation bool       `json:"onlineActivation"`
	LicenseID        string     `json:"licenseId,omitempty"`
	CustomerID       string     `json:"customerId,omitempty"`
	CustomerName     string     `json:"customerName,omitempty"`
	Edition          string     `json:"edition,omitempty"`
	Features         []string   `json:"features,omitempty"`
	MaxVersion       string     `json:"maxVersion,omitempty"`
	IssuedAt         *time.Time `json:"issuedAt,omitempty"`
	ExpiresAt        *time.Time `json:"expiresAt,omitempty"`
}

func New(config Config) (*Manager, error) {
	if strings.TrimSpace(config.Directory) == "" {
		return nil, errors.New("license directory is required")
	}
	verifyKey, err := ParsePublicKey(config.PublicKey)
	if err != nil {
		return nil, fmt.Errorf("parse license verification key: %w", err)
	}
	if err := os.MkdirAll(config.Directory, 0o700); err != nil {
		return nil, fmt.Errorf("create license directory: %w", err)
	}
	privateKey, err := loadOrCreateIdentity(filepath.Join(config.Directory, identityFileName))
	if err != nil {
		return nil, err
	}
	publicKey := privateKey.Public().(ed25519.PublicKey)
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultHTTPTimeout}
	}
	manager := &Manager{
		directory:      config.Directory,
		productVersion: strings.TrimSpace(config.ProductVersion),
		verifyKey:      verifyKey,
		activationURL:  strings.TrimSpace(config.ActivationURL),
		httpClient:     client,
		privateKey:     privateKey,
		publicKey:      publicKey,
		deploymentID:   DeploymentID(publicKey),
		state:          "unlicensed",
		message:        "尚未导入部署许可证",
	}
	manager.reload()
	return manager, nil
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, message := m.currentStateLocked(time.Now())
	status := Status{
		Activated:        state == "active",
		State:            state,
		Message:          message,
		DeploymentID:     m.deploymentID,
		ProductVersion:   m.productVersion,
		OnlineActivation: m.activationURL != "",
	}
	if m.claims != nil {
		issuedAt := m.claims.IssuedAt
		status.LicenseID = m.claims.LicenseID
		status.CustomerID = m.claims.CustomerID
		status.CustomerName = m.claims.CustomerName
		status.Edition = m.claims.Edition
		status.Features = append([]string(nil), m.claims.Features...)
		status.MaxVersion = m.claims.MaxVersion
		status.IssuedAt = &issuedAt
		status.ExpiresAt = m.claims.ExpiresAt
	}
	return status
}

func (m *Manager) Active() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	state, _ := m.currentStateLocked(time.Now())
	return state == "active"
}

func (m *Manager) Request() ([]byte, error) {
	payload := RequestPayload{
		SchemaVersion:       SchemaVersion,
		Product:             ProductName,
		DeploymentID:        m.deploymentID,
		DeploymentPublicKey: base64.RawStdEncoding.EncodeToString(m.publicKey),
		CreatedAt:           time.Now().UTC(),
	}
	encodedPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode activation request: %w", err)
	}
	request := SignedRequest{
		Payload:   base64.RawURLEncoding.EncodeToString(encodedPayload),
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(m.privateKey, encodedPayload)),
	}
	return json.MarshalIndent(request, "", "  ")
}

func (m *Manager) Import(data []byte) error {
	claims, err := Verify(data, m.verifyKey, m.deploymentID, m.publicKey, m.productVersion, time.Now())
	if err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(m.directory, licenseFileName), bytes.TrimSpace(data), 0o600); err != nil {
		return fmt.Errorf("save license: %w", err)
	}
	m.mu.Lock()
	m.claims = &claims
	m.state = "active"
	m.message = "许可证有效"
	m.mu.Unlock()
	return nil
}

func (m *Manager) Activate(ctx context.Context, activationCode string) error {
	if m.activationURL == "" {
		return errors.New("未配置在线激活服务，请使用离线许可证导入")
	}
	activationCode = strings.TrimSpace(activationCode)
	if activationCode == "" {
		return errors.New("激活码不能为空")
	}
	requestData, err := m.Request()
	if err != nil {
		return err
	}
	var signedRequest SignedRequest
	if err := json.Unmarshal(requestData, &signedRequest); err != nil {
		return fmt.Errorf("decode activation request: %w", err)
	}
	body, err := json.Marshal(struct {
		ActivationCode string        `json:"activationCode"`
		Request        SignedRequest `json:"request"`
	}{ActivationCode: activationCode, Request: signedRequest})
	if err != nil {
		return fmt.Errorf("encode activation request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.activationURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create activation request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("连接在线激活服务失败: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxLicenseBytes+1))
	if err != nil {
		return fmt.Errorf("读取激活响应失败: %w", err)
	}
	if len(responseBody) > maxLicenseBytes {
		return errors.New("激活响应过大")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var apiError struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal(responseBody, &apiError)
		if apiError.Message == "" {
			apiError.Message = fmt.Sprintf("在线激活失败（HTTP %d）", response.StatusCode)
		}
		return errors.New(apiError.Message)
	}
	var result struct {
		License json.RawMessage `json:"license"`
	}
	if err := json.Unmarshal(responseBody, &result); err != nil || len(result.License) == 0 {
		return errors.New("在线激活服务未返回有效许可证")
	}
	return m.Import(result.License)
}

func (m *Manager) reload() {
	data, err := os.ReadFile(filepath.Join(m.directory, licenseFileName))
	if errors.Is(err, os.ErrNotExist) {
		return
	}
	if err != nil {
		m.state = "invalid"
		m.message = "读取许可证失败"
		return
	}
	claims, err := Verify(data, m.verifyKey, m.deploymentID, m.publicKey, m.productVersion, time.Now())
	if err != nil {
		m.state = "invalid"
		m.message = err.Error()
		return
	}
	m.claims = &claims
	m.state = "active"
	m.message = "许可证有效"
}

func (m *Manager) currentStateLocked(now time.Time) (string, string) {
	if m.claims != nil && m.claims.ExpiresAt != nil && now.After(*m.claims.ExpiresAt) {
		return "expired", "许可证已过期"
	}
	return m.state, m.message
}

func ParseRequest(data []byte) (RequestPayload, error) {
	var request SignedRequest
	if err := json.Unmarshal(data, &request); err != nil {
		return RequestPayload{}, fmt.Errorf("decode activation request: %w", err)
	}
	payloadBytes, err := base64.RawURLEncoding.DecodeString(request.Payload)
	if err != nil {
		return RequestPayload{}, errors.New("activation request payload is invalid")
	}
	var payload RequestPayload
	if err := json.Unmarshal(payloadBytes, &payload); err != nil {
		return RequestPayload{}, errors.New("activation request payload is invalid")
	}
	publicKey, err := ParsePublicKey(payload.DeploymentPublicKey)
	if err != nil {
		return RequestPayload{}, errors.New("activation request public key is invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(request.Signature)
	if err != nil || !ed25519.Verify(publicKey, payloadBytes, signature) {
		return RequestPayload{}, errors.New("activation request signature is invalid")
	}
	if payload.SchemaVersion != SchemaVersion || payload.Product != ProductName || payload.DeploymentID != DeploymentID(publicKey) {
		return RequestPayload{}, errors.New("activation request identity is invalid")
	}
	return payload, nil
}

func Sign(privateKey ed25519.PrivateKey, claims Claims) ([]byte, error) {
	if err := validateClaims(claims); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return nil, fmt.Errorf("encode license claims: %w", err)
	}
	envelope := Envelope{
		Payload:   base64.RawURLEncoding.EncodeToString(payload),
		Signature: base64.RawURLEncoding.EncodeToString(ed25519.Sign(privateKey, payload)),
	}
	return json.MarshalIndent(envelope, "", "  ")
}

func Verify(data []byte, publicKey ed25519.PublicKey, deploymentID string, deploymentPublicKey ed25519.PublicKey, productVersion string, now time.Time) (Claims, error) {
	var envelope Envelope
	if err := json.Unmarshal(data, &envelope); err != nil {
		return Claims{}, fmt.Errorf("%w: 无法解析许可证文件", ErrInvalid)
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		return Claims{}, fmt.Errorf("%w: 许可证内容编码错误", ErrInvalid)
	}
	signature, err := base64.RawURLEncoding.DecodeString(envelope.Signature)
	if err != nil || !ed25519.Verify(publicKey, payload, signature) {
		return Claims{}, fmt.Errorf("%w: 许可证签名无效", ErrInvalid)
	}
	var claims Claims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return Claims{}, fmt.Errorf("%w: 许可证内容无效", ErrInvalid)
	}
	if err := validateClaims(claims); err != nil {
		return Claims{}, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if claims.Product != ProductName {
		return Claims{}, fmt.Errorf("%w: 许可证产品不匹配", ErrInvalid)
	}
	if claims.DeploymentID != deploymentID || claims.DeploymentKeyHash != PublicKeyHash(deploymentPublicKey) {
		return Claims{}, fmt.Errorf("%w: 许可证不属于当前部署", ErrInvalid)
	}
	if claims.ExpiresAt != nil && now.After(*claims.ExpiresAt) {
		return Claims{}, fmt.Errorf("%w: 许可证已过期", ErrInvalid)
	}
	if !versionAllowed(productVersion, claims.MaxVersion) {
		return Claims{}, fmt.Errorf("%w: 当前版本超出许可证允许的升级范围", ErrInvalid)
	}
	return claims, nil
}

func validateClaims(claims Claims) error {
	if claims.SchemaVersion != SchemaVersion {
		return errors.New("unsupported license schema version")
	}
	if strings.TrimSpace(claims.LicenseID) == "" || strings.TrimSpace(claims.CustomerID) == "" {
		return errors.New("license and customer identifiers are required")
	}
	if strings.TrimSpace(claims.DeploymentID) == "" || strings.TrimSpace(claims.DeploymentKeyHash) == "" {
		return errors.New("deployment identity is required")
	}
	if strings.TrimSpace(claims.Product) == "" || strings.TrimSpace(claims.Edition) == "" || claims.IssuedAt.IsZero() {
		return errors.New("product, edition and issue time are required")
	}
	return nil
}

func GenerateSigningKey() (ed25519.PublicKey, ed25519.PrivateKey, error) {
	return ed25519.GenerateKey(rand.Reader)
}

func ParsePublicKey(value string) (ed25519.PublicKey, error) {
	decoded, err := decodeKey(value)
	if err != nil || len(decoded) != ed25519.PublicKeySize {
		return nil, errors.New("Ed25519 public key must be a base64-encoded 32-byte value")
	}
	return ed25519.PublicKey(decoded), nil
}

func ParsePrivateKey(value string) (ed25519.PrivateKey, error) {
	decoded, err := decodeKey(value)
	if err != nil || len(decoded) != ed25519.PrivateKeySize {
		return nil, errors.New("Ed25519 private key must be a base64-encoded 64-byte value")
	}
	return ed25519.PrivateKey(decoded), nil
}

func EncodeKey(value []byte) string {
	return base64.RawStdEncoding.EncodeToString(value)
}

func DeploymentID(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return "dep_" + hex.EncodeToString(sum[:16])
}

func PublicKeyHash(publicKey ed25519.PublicKey) string {
	sum := sha256.Sum256(publicKey)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func loadOrCreateIdentity(path string) (ed25519.PrivateKey, error) {
	data, err := os.ReadFile(path)
	if err == nil {
		privateKey, parseErr := ParsePrivateKey(string(data))
		if parseErr != nil {
			return nil, fmt.Errorf("load deployment identity: %w", parseErr)
		}
		return privateKey, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("read deployment identity: %w", err)
	}
	_, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate deployment identity: %w", err)
	}
	if err := atomicWrite(path, []byte(EncodeKey(privateKey)+"\n"), 0o600); err != nil {
		if existing, readErr := os.ReadFile(path); readErr == nil {
			return ParsePrivateKey(string(existing))
		}
		return nil, fmt.Errorf("save deployment identity: %w", err)
	}
	return privateKey, nil
}

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".databridge-license-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func decodeKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, errors.New("key is empty")
	}
	if decoded, err := base64.RawStdEncoding.DecodeString(value); err == nil {
		return decoded, nil
	}
	return base64.StdEncoding.DecodeString(value)
}

func versionAllowed(current, maximum string) bool {
	current = strings.TrimSpace(strings.TrimPrefix(current, "v"))
	maximum = strings.TrimSpace(strings.TrimPrefix(maximum, "v"))
	if current == "" || current == "dev" || maximum == "" {
		return true
	}
	currentMajor, ok := versionMajor(current)
	if !ok {
		return false
	}
	if strings.HasSuffix(maximum, ".x") {
		maximumMajor, err := strconv.Atoi(strings.TrimSuffix(maximum, ".x"))
		return err == nil && currentMajor <= maximumMajor
	}
	currentParts, currentOK := versionParts(current)
	maximumParts, maximumOK := versionParts(maximum)
	if !currentOK || !maximumOK {
		return false
	}
	for index := range currentParts {
		if currentParts[index] != maximumParts[index] {
			return currentParts[index] < maximumParts[index]
		}
	}
	return true
}

func versionMajor(value string) (int, bool) {
	segment := strings.SplitN(value, ".", 2)[0]
	major, err := strconv.Atoi(segment)
	return major, err == nil && major >= 0
}

func versionParts(value string) ([3]int, bool) {
	var result [3]int
	segments := strings.SplitN(value, ".", 4)
	if len(segments) > 3 {
		return result, false
	}
	for index, segment := range segments {
		if separator := strings.IndexAny(segment, "-+"); separator >= 0 {
			segment = segment[:separator]
		}
		parsed, err := strconv.Atoi(segment)
		if err != nil || parsed < 0 {
			return result, false
		}
		result[index] = parsed
	}
	return result, true
}
