package license

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestManagerCreatesStableDeploymentIdentityAndImportsLicense(t *testing.T) {
	verificationKey, signingKey, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	manager := newTestManager(t, directory, verificationKey, "")
	if manager.Status().Activated {
		t.Fatal("new deployment should not be activated")
	}
	requestData, err := manager.Request()
	if err != nil {
		t.Fatal(err)
	}
	request, err := ParseRequest(requestData)
	if err != nil {
		t.Fatal(err)
	}
	licenseData := signedTestLicense(t, signingKey, request, nil)
	if err := manager.Import(licenseData); err != nil {
		t.Fatal(err)
	}
	status := manager.Status()
	if !status.Activated || status.LicenseID != "lic_test" || status.CustomerID != "customer_test" {
		t.Fatalf("unexpected activated status: %#v", status)
	}

	restarted := newTestManager(t, directory, verificationKey, "")
	if restarted.Status().DeploymentID != status.DeploymentID || !restarted.Active() {
		t.Fatalf("deployment identity or license was not preserved: %#v", restarted.Status())
	}
	mode, err := os.Stat(filepath.Join(directory, identityFileName))
	if err != nil {
		t.Fatal(err)
	}
	if mode.Mode().Perm() != 0o600 {
		t.Fatalf("deployment key mode = %o", mode.Mode().Perm())
	}
}

func TestManagerRejectsLicenseFromAnotherDeployment(t *testing.T) {
	verificationKey, signingKey, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	first := newTestManager(t, t.TempDir(), verificationKey, "")
	second := newTestManager(t, t.TempDir(), verificationKey, "")
	requestData, err := first.Request()
	if err != nil {
		t.Fatal(err)
	}
	request, err := ParseRequest(requestData)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Import(signedTestLicense(t, signingKey, request, nil)); err == nil {
		t.Fatal("license from another deployment should be rejected")
	}
}

func TestManagerRejectsTamperedAndExpiredLicenses(t *testing.T) {
	verificationKey, signingKey, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	manager := newTestManager(t, t.TempDir(), verificationKey, "")
	requestData, _ := manager.Request()
	request, _ := ParseRequest(requestData)
	expired := time.Now().Add(-time.Hour)
	if err := manager.Import(signedTestLicense(t, signingKey, request, &expired)); err == nil {
		t.Fatal("expired license should be rejected")
	}
	valid := signedTestLicense(t, signingKey, request, nil)
	var envelope Envelope
	if err := json.Unmarshal(valid, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Signature = "invalid"
	tampered, _ := json.Marshal(envelope)
	if err := manager.Import(tampered); err == nil {
		t.Fatal("tampered license should be rejected")
	}
}

func TestOnlineActivationImportsReturnedLicense(t *testing.T) {
	verificationKey, signingKey, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	var manager *Manager
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ActivationCode string        `json:"activationCode"`
			Request        SignedRequest `json:"request"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Fatal(err)
		}
		if input.ActivationCode != "DB-TEST-CODE" {
			t.Fatalf("activation code = %q", input.ActivationCode)
		}
		requestBytes, _ := json.Marshal(input.Request)
		payload, err := ParseRequest(requestBytes)
		if err != nil {
			t.Fatal(err)
		}
		licenseData := signedTestLicense(t, signingKey, payload, nil)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"license":` + string(licenseData) + `}`))
	}))
	defer server.Close()
	manager = newTestManager(t, directory, verificationKey, server.URL)
	if err := manager.Activate(context.Background(), "DB-TEST-CODE"); err != nil {
		t.Fatal(err)
	}
	if !manager.Active() {
		t.Fatal("online activation should activate the deployment")
	}
}

func TestVersionAllowed(t *testing.T) {
	tests := []struct {
		current string
		maximum string
		allowed bool
	}{
		{current: "1.8.2", maximum: "1.x", allowed: true},
		{current: "2.0.0", maximum: "1.x", allowed: false},
		{current: "1.2.3", maximum: "1.2.3", allowed: true},
		{current: "1.2.4", maximum: "1.2.3", allowed: false},
		{current: "1.1.9", maximum: "1.2.0", allowed: true},
		{current: "dev", maximum: "1.x", allowed: true},
	}
	for _, test := range tests {
		if actual := versionAllowed(test.current, test.maximum); actual != test.allowed {
			t.Errorf("versionAllowed(%q, %q) = %t, want %t", test.current, test.maximum, actual, test.allowed)
		}
	}
}

func newTestManager(t *testing.T, directory string, publicKey ed25519.PublicKey, activationURL string) *Manager {
	t.Helper()
	manager, err := New(Config{
		Directory:      directory,
		ProductVersion: "1.0.0",
		PublicKey:      EncodeKey(publicKey),
		ActivationURL:  activationURL,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func signedTestLicense(t *testing.T, signingKey ed25519.PrivateKey, request RequestPayload, expiresAt *time.Time) []byte {
	t.Helper()
	publicKey, err := ParsePublicKey(request.DeploymentPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	data, err := Sign(signingKey, Claims{
		SchemaVersion:     SchemaVersion,
		LicenseID:         "lic_test",
		CustomerID:        "customer_test",
		CustomerName:      "测试客户",
		DeploymentID:      request.DeploymentID,
		DeploymentKeyHash: PublicKeyHash(publicKey),
		Product:           ProductName,
		Edition:           "enterprise",
		Features:          []string{"migration", "backup", "restore"},
		MaxVersion:        "1.x",
		IssuedAt:          time.Now().UTC(),
		ExpiresAt:         expiresAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
