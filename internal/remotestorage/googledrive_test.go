package remotestorage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"databridge/internal/model"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonResponse(status int, body any) *http.Response {
	data, _ := json.Marshal(body)
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(data)),
	}
}

func TestGoogleDriveAuthorizeURL(t *testing.T) {
	storage := model.RemoteStorage{
		Type:      "google_drive",
		AccessKey: "client-123",
		SecretKey: "secret-456",
		AuthMode:  "oauth",
	}
	rawURL, err := GoogleDriveAuthorizeURL(storage, "https://db.example.com/api/storages/s1/google-drive/callback", "state-xyz")
	if err != nil {
		t.Fatalf("GoogleDriveAuthorizeURL failed: %v", err)
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse authorize URL: %v", err)
	}
	q := parsed.Query()
	if q.Get("client_id") != "client-123" || q.Get("state") != "state-xyz" || q.Get("access_type") != "offline" {
		t.Fatalf("unexpected authorize query: %v", q)
	}
	if q.Get("scope") != googleDriveScope {
		t.Fatalf("expected scope %q, got %q", googleDriveScope, q.Get("scope"))
	}

	storage.AuthMode = "service_account"
	if _, err := GoogleDriveAuthorizeURL(storage, "https://db.example.com/cb", "s"); err == nil {
		t.Fatal("expected error for service_account mode")
	}
}

func TestGoogleDriveExchangeCodeAndRefresh(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://oauth2.test/token" {
				t.Fatalf("unexpected token URL: %s", r.URL.String())
			}
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			switch vals.Get("grant_type") {
			case "authorization_code":
				if vals.Get("code") != "auth-code-1" {
					t.Fatalf("unexpected code: %s", vals.Get("code"))
				}
				return jsonResponse(http.StatusOK, map[string]any{
					"access_token":  "access-1",
					"refresh_token": "refresh-1",
					"expires_in":    3600,
				}), nil
			case "refresh_token":
				if vals.Get("refresh_token") != "refresh-1" {
					t.Fatalf("unexpected refresh token: %s", vals.Get("refresh_token"))
				}
				return jsonResponse(http.StatusOK, map[string]any{
					"access_token": "access-2",
					"expires_in":   3600,
				}), nil
			default:
				t.Fatalf("unexpected grant_type: %s", vals.Get("grant_type"))
				return nil, nil
			}
		}),
	}

	storage := model.RemoteStorage{
		Type:      "google_drive",
		AccessKey: "client-id",
		SecretKey: "client-secret",
		AuthMode:  "oauth",
	}
	cred, err := googleDriveExchangeCodeWithClient(context.Background(), storage, "auth-code-1", "http://localhost/cb", "https://oauth2.test/token", client)
	if err != nil {
		t.Fatalf("exchange code failed: %v", err)
	}
	if cred.AccessToken != "access-1" || cred.RefreshToken != "refresh-1" {
		t.Fatalf("unexpected credential: %+v", cred)
	}
	encoded, err := EncodeGoogleDriveCredential(cred)
	if err != nil {
		t.Fatalf("encode credential: %v", err)
	}
	cred.AccessToken = ""
	cred.ExpiresAt = time.Now().Add(-time.Hour).Unix()
	expiredEncoded, _ := EncodeGoogleDriveCredential(cred)
	storage.SecretKey = expiredEncoded
	refreshed, err := googleDriveAccessToken(context.Background(), storage, "https://oauth2.test/token", client)
	if err != nil {
		t.Fatalf("refresh token failed: %v", err)
	}
	if refreshed != "access-2" {
		t.Fatalf("expected refreshed token access-2, got %q", refreshed)
	}
	if ParseGoogleDriveCredential(encoded).RefreshToken != "refresh-1" {
		t.Fatal("failed to parse encoded credential")
	}
}

func TestGoogleServiceAccountToken(t *testing.T) {
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privKey),
	})
	saJSON, _ := json.Marshal(map[string]string{
		"type":         "service_account",
		"client_email": "backup@test-project.iam.gserviceaccount.com",
		"private_key":  string(pemBytes),
		"token_uri":    "https://oauth2.test/sa-token",
	})

	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.String() != "https://oauth2.test/sa-token" {
				t.Fatalf("unexpected token URL: %s", r.URL.String())
			}
			body, _ := io.ReadAll(r.Body)
			vals, _ := url.ParseQuery(string(body))
			if vals.Get("grant_type") != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
				t.Fatalf("unexpected grant_type: %s", vals.Get("grant_type"))
			}
			if parts := strings.Split(vals.Get("assertion"), "."); len(parts) != 3 {
				t.Fatalf("expected 3-part JWT, got %d parts", len(parts))
			}
			return jsonResponse(http.StatusOK, map[string]any{
				"access_token": "sa-access-token",
				"expires_in":   3600,
			}), nil
		}),
	}

	token, err := googleServiceAccountToken(context.Background(), string(saJSON), defaultGoogleOAuthTokenURL, client)
	if err != nil {
		t.Fatalf("googleServiceAccountToken failed: %v", err)
	}
	if token != "sa-access-token" {
		t.Fatalf("expected sa-access-token, got %q", token)
	}
}

func TestGoogleDriveClientUploadDownloadDelete(t *testing.T) {
	var mu sync.Mutex
	folders := map[string]string{}
	files := map[string][]byte{}
	fileParents := map[string]string{}
	nextID := 1

	client := googleDriveClient{
		baseURL: "https://drive.test",
		token:   "test-token",
		httpClient: &http.Client{
			Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/resumable-session" && r.Header.Get("Authorization") != "Bearer test-token" {
					t.Fatalf("missing bearer token on %s %s", r.Method, r.URL.Path)
				}
				mu.Lock()
				defer mu.Unlock()

				switch {
				case r.Method == http.MethodGet && r.URL.Path == "/drive/v3/files":
					q := r.URL.Query().Get("q")
					var matched []map[string]string
					if strings.Contains(q, googleDriveFolderMimeType) && !strings.Contains(q, "!=") {
						for key, id := range folders {
							parts := strings.SplitN(key, "/", 2)
							if strings.Contains(q, "'"+parts[0]+"' in parents") && strings.Contains(q, "name = '"+parts[1]+"'") {
								matched = append(matched, map[string]string{"id": id, "name": parts[1]})
							}
						}
					} else {
						for key, id := range fileParents {
							parts := strings.SplitN(key, "/", 2)
							if strings.Contains(q, "'"+parts[0]+"' in parents") && strings.Contains(q, "name = '"+parts[1]+"'") {
								matched = append(matched, map[string]string{"id": id, "name": parts[1]})
							}
						}
					}
					return jsonResponse(http.StatusOK, map[string]any{"files": matched}), nil

				case r.Method == http.MethodPost && r.URL.Path == "/drive/v3/files":
					var meta struct {
						Name    string   `json:"name"`
						Parents []string `json:"parents"`
					}
					_ = json.NewDecoder(r.Body).Decode(&meta)
					id := "folder-" + string(rune('0'+nextID))
					nextID++
					folders[meta.Parents[0]+"/"+meta.Name] = id
					return jsonResponse(http.StatusOK, map[string]string{"id": id}), nil

				case r.Method == http.MethodPost && r.URL.Path == "/upload/drive/v3/files" && r.URL.Query().Get("uploadType") == "multipart":
					raw, _ := io.ReadAll(r.Body)
					id := "file-" + string(rune('0'+nextID))
					nextID++
					// Extract name from JSON metadata part and payload from octet-stream part.
					content := string(raw)
					idx := strings.Index(content, `{"name":"`)
					end := strings.Index(content[idx:], "}") + idx + 1
					var meta struct {
						Name    string   `json:"name"`
						Parents []string `json:"parents"`
					}
					_ = json.Unmarshal([]byte(content[idx:end]), &meta)
					payloadStart := strings.Index(content, "application/octet-stream\r\n\r\n") + len("application/octet-stream\r\n\r\n")
					payloadEnd := strings.LastIndex(content, "\r\n--")
					files[id] = []byte(content[payloadStart:payloadEnd])
					fileParents[meta.Parents[0]+"/"+meta.Name] = id
					return jsonResponse(http.StatusOK, map[string]string{"id": id}), nil

				case r.Method == http.MethodPost && r.URL.Path == "/upload/drive/v3/files" && r.URL.Query().Get("uploadType") == "resumable":
					resp := jsonResponse(http.StatusOK, map[string]any{})
					resp.Header.Set("Location", "https://drive.test/resumable-session")
					return resp, nil

				case r.Method == http.MethodPut && r.URL.Path == "/resumable-session":
					chunk, _ := io.ReadAll(r.Body)
					files["resumable-1"] = append(files["resumable-1"], chunk...)
					fileParents["root/large.bin"] = "resumable-1"
					return jsonResponse(http.StatusOK, map[string]string{"id": "resumable-1"}), nil

				case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/drive/v3/files/"):
					id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
					data, ok := files[id]
					if !ok {
						return jsonResponse(http.StatusNotFound, map[string]string{"error": "not found"}), nil
					}
					return &http.Response{
						StatusCode: http.StatusOK,
						Status:     "200 OK",
						Body:       io.NopCloser(bytes.NewReader(data)),
					}, nil

				case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/drive/v3/files/"):
					id := strings.TrimPrefix(r.URL.Path, "/drive/v3/files/")
					delete(files, id)
					for k, v := range fileParents {
						if v == id {
							delete(fileParents, k)
						}
					}
					return &http.Response{StatusCode: http.StatusNoContent, Status: "204 No Content", Body: io.NopCloser(strings.NewReader(""))}, nil

				default:
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.String())
					return nil, nil
				}
			}),
		},
	}

	ctx := context.Background()
	if _, err := client.uploadBytes(ctx, "DataBridge Backups/db/sample.tar.gz", []byte("backup-payload")); err != nil {
		t.Fatalf("uploadBytes failed: %v", err)
	}

	tmpDir := t.TempDir()
	downloadPath := filepath.Join(tmpDir, "downloaded.tar.gz")
	n, err := client.downloadByKey(ctx, "DataBridge Backups/db/sample.tar.gz", downloadPath)
	if err != nil {
		t.Fatalf("downloadByKey failed: %v", err)
	}
	if n != int64(len("backup-payload")) {
		t.Fatalf("expected %d bytes, got %d", len("backup-payload"), n)
	}
	got, _ := os.ReadFile(downloadPath)
	if string(got) != "backup-payload" {
		t.Fatalf("expected backup-payload, got %q", string(got))
	}

	if err := client.deleteByKey(ctx, "DataBridge Backups/db/sample.tar.gz"); err != nil {
		t.Fatalf("deleteByKey failed: %v", err)
	}
	// Idempotent delete
	if err := client.deleteByKey(ctx, "DataBridge Backups/db/sample.tar.gz"); err != nil {
		t.Fatalf("idempotent deleteByKey failed: %v", err)
	}

	// Test resumable upload
	largePath := filepath.Join(tmpDir, "large.bin")
	if err := os.WriteFile(largePath, []byte("large-backup-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.resumableUpload(ctx, "large.bin", largePath, int64(len("large-backup-content"))); err != nil {
		t.Fatalf("resumableUpload failed: %v", err)
	}
	if string(files["resumable-1"]) != "large-backup-content" {
		t.Fatalf("unexpected resumable content: %q", string(files["resumable-1"]))
	}
}
