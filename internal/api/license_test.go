package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"databridge/internal/license"
	"databridge/internal/store"
)

func TestLicensedServerBlocksBusinessAPIUntilOfflineImport(t *testing.T) {
	directory := t.TempDir()
	verificationKey, signingKey, err := license.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := license.New(license.Config{
		Directory:      filepath.Join(directory, "license"),
		ProductVersion: "1.0.0",
		PublicKey:      license.EncodeKey(verificationKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	appStore, err := store.New(filepath.Join(directory, "databridge.db"), filepath.Join(directory, "databridge.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = appStore.Close() })
	handler := NewWithLicense(appStore, manager)

	blocked := httptest.NewRecorder()
	blockedRequest := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	blockedRequest.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(blocked, blockedRequest)
	if blocked.Code != http.StatusForbidden || !bytes.Contains(blocked.Body.Bytes(), []byte("license_required")) {
		t.Fatalf("unlicensed API status = %d, body = %s", blocked.Code, blocked.Body.String())
	}

	requestRecorder := httptest.NewRecorder()
	handler.ServeHTTP(requestRecorder, httptest.NewRequest(http.MethodGet, "/api/license/request", nil))
	requestPayload, err := license.ParseRequest(requestRecorder.Body.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	deploymentPublicKey, err := license.ParsePublicKey(requestPayload.DeploymentPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	licenseData, err := license.Sign(signingKey, license.Claims{
		SchemaVersion:     license.SchemaVersion,
		LicenseID:         "lic_api_test",
		CustomerID:        "customer_api_test",
		DeploymentID:      requestPayload.DeploymentID,
		DeploymentKeyHash: license.PublicKeyHash(deploymentPublicKey),
		Product:           license.ProductName,
		Edition:           "enterprise",
		MaxVersion:        "1.x",
		IssuedAt:          time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}

	var upload bytes.Buffer
	writer := multipart.NewWriter(&upload)
	part, err := writer.CreateFormFile("file", "license.json")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(licenseData)
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	importRecorder := httptest.NewRecorder()
	importRequest := httptest.NewRequest(http.MethodPost, "/api/license/import", &upload)
	importRequest.Header.Set("Content-Type", writer.FormDataContentType())
	handler.ServeHTTP(importRecorder, importRequest)
	if importRecorder.Code != http.StatusOK {
		t.Fatalf("license import status = %d, body = %s", importRecorder.Code, importRecorder.Body.String())
	}
	var status license.Status
	if err := json.Unmarshal(importRecorder.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if !status.Activated {
		t.Fatalf("license import status is not active: %#v", status)
	}

	allowed := httptest.NewRecorder()
	allowedRequest := httptest.NewRequest(http.MethodGet, "/api/connections", nil)
	allowedRequest.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(allowed, allowedRequest)
	if allowed.Code != http.StatusOK {
		t.Fatalf("activated API status = %d, body = %s", allowed.Code, allowed.Body.String())
	}
}
