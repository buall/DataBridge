package main

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"

	"databridge/internal/license"
)

func TestSignOnlyRequiresRequestAndValidityDays(t *testing.T) {
	directory := t.TempDir()
	verificationKey, signingKey, err := license.GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(directory)
	privatePath := filepath.Join(".local-license", "signing-private.key")
	if err := writeKey(privatePath, signingKey, 0o600); err != nil {
		t.Fatal(err)
	}
	manager, err := license.New(license.Config{
		Directory:      filepath.Join(directory, "deployment"),
		ProductVersion: "1.0.0",
		PublicKey:      license.EncodeKey(verificationKey),
	})
	if err != nil {
		t.Fatal(err)
	}
	requestData, err := manager.Request()
	if err != nil {
		t.Fatal(err)
	}
	requestPath := filepath.Join(directory, "request.json")
	if err := os.WriteFile(requestPath, requestData, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := sign([]string{"--request", requestPath, "--days", "30"}); err != nil {
		t.Fatal(err)
	}
	outputPath := filepath.Join(directory, "databridge-license-"+manager.Status().DeploymentID+".json")
	licenseData, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	claims, err := license.Verify(licenseData, verificationKey, manager.Status().DeploymentID, managerPublicKey(t, requestData), "1.0.0", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if claims.ExpiresAt == nil || time.Until(*claims.ExpiresAt) < 29*24*time.Hour || claims.LicenseID == "" {
		t.Fatalf("unexpected claims: %#v", claims)
	}
}

func managerPublicKey(t *testing.T, requestData []byte) ed25519.PublicKey {
	t.Helper()
	request, err := license.ParseRequest(requestData)
	if err != nil {
		t.Fatal(err)
	}
	publicKey, err := license.ParsePublicKey(request.DeploymentPublicKey)
	if err != nil {
		t.Fatal(err)
	}
	return publicKey
}
