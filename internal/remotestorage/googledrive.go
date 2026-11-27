package remotestorage

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"databridge/internal/model"
)

const (
	defaultGoogleDriveEndpoint        = "https://www.googleapis.com"
	defaultGoogleOAuthAuthURL         = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultGoogleOAuthTokenURL        = "https://oauth2.googleapis.com/token"
	googleDriveScope                  = "https://www.googleapis.com/auth/drive"
	googleDriveFolderMimeType         = "application/vnd.google-apps.folder"
	googleDriveSmallUploadLimit int64 = 5 << 20
	googleDriveChunkSize        int64 = 10 * 1024 * 1024 // multiple of 256 KiB
)

// GoogleDriveCredential stores OAuth client secret and tokens for a Google Drive storage.
type GoogleDriveCredential struct {
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
}

// GoogleServiceAccountKey represents the fields used from a Google Cloud service account JSON key.
type GoogleServiceAccountKey struct {
	Type        string `json:"type"`
	ClientEmail string `json:"client_email"`
	PrivateKey  string `json:"private_key"`
	TokenURI    string `json:"token_uri"`
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

// ParseGoogleDriveCredential decodes stored credential JSON or falls back to raw client secret.
func ParseGoogleDriveCredential(secret string) GoogleDriveCredential {
	var credential GoogleDriveCredential
	if json.Unmarshal([]byte(secret), &credential) == nil && credential.ClientSecret != "" {
		return credential
	}
	return GoogleDriveCredential{ClientSecret: secret}
}

// EncodeGoogleDriveCredential serializes a GoogleDriveCredential to JSON.
func EncodeGoogleDriveCredential(credential GoogleDriveCredential) (string, error) {
	if strings.TrimSpace(credential.ClientSecret) == "" {
		return "", fmt.Errorf("google drive client secret is required")
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ParseGoogleServiceAccountKey parses and validates a Google service account JSON key.
func ParseGoogleServiceAccountKey(raw string) (GoogleServiceAccountKey, error) {
	var key GoogleServiceAccountKey
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &key); err != nil {
		return GoogleServiceAccountKey{}, fmt.Errorf("invalid service account JSON: %w", err)
	}
	if strings.TrimSpace(key.ClientEmail) == "" || strings.TrimSpace(key.PrivateKey) == "" {
		return GoogleServiceAccountKey{}, fmt.Errorf("service account JSON must contain client_email and private_key")
	}
	if _, err := parseRSAPrivateKey(key.PrivateKey); err != nil {
		return GoogleServiceAccountKey{}, fmt.Errorf("invalid service account private_key: %w", err)
	}
	if strings.TrimSpace(key.TokenURI) == "" {
		key.TokenURI = defaultGoogleOAuthTokenURL
	}
	return key, nil
}

// GoogleDriveAuthorizeURL builds the Google OAuth 2.0 consent URL.
func GoogleDriveAuthorizeURL(storage model.RemoteStorage, redirectURI, state string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(storage.AuthMode), "service_account") {
		return "", fmt.Errorf("service account mode does not require OAuth authorization")
	}
	if strings.TrimSpace(storage.AccessKey) == "" {
		return "", fmt.Errorf("google drive client id is required")
	}
	credential := ParseGoogleDriveCredential(storage.SecretKey)
	if credential.ClientSecret == "" {
		return "", fmt.Errorf("google drive client secret is required")
	}
	authorize, err := url.Parse(defaultGoogleOAuthAuthURL)
	if err != nil {
		return "", err
	}
	query := authorize.Query()
	query.Set("client_id", strings.TrimSpace(storage.AccessKey))
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", googleDriveScope)
	query.Set("access_type", "offline")
	query.Set("prompt", "consent")
	query.Set("state", state)
	authorize.RawQuery = query.Encode()
	return authorize.String(), nil
}

// GoogleDriveExchangeCode exchanges an OAuth authorization code for access and refresh tokens.
func GoogleDriveExchangeCode(ctx context.Context, storage model.RemoteStorage, code, redirectURI string) (GoogleDriveCredential, error) {
	return googleDriveExchangeCodeWithClient(ctx, storage, code, redirectURI, defaultGoogleOAuthTokenURL, &http.Client{Timeout: 30 * time.Second})
}

func googleDriveExchangeCodeWithClient(ctx context.Context, storage model.RemoteStorage, code, redirectURI, tokenEndpoint string, httpClient *http.Client) (GoogleDriveCredential, error) {
	credential := ParseGoogleDriveCredential(storage.SecretKey)
	if credential.ClientSecret == "" {
		return GoogleDriveCredential{}, fmt.Errorf("google drive client secret is required")
	}
	form := url.Values{
		"client_id":     {strings.TrimSpace(storage.AccessKey)},
		"client_secret": {credential.ClientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return GoogleDriveCredential{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return GoogleDriveCredential{}, err
	}
	defer resp.Body.Close()
	var payload googleTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return GoogleDriveCredential{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || payload.AccessToken == "" {
		return GoogleDriveCredential{}, fmt.Errorf("google drive OAuth failed: %s", strings.TrimSpace(payload.Error+" "+payload.Description))
	}
	if payload.RefreshToken != "" {
		credential.RefreshToken = payload.RefreshToken
	}
	credential.AccessToken = payload.AccessToken
	credential.ExpiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).Unix()
	return credential, nil
}

// TestGoogleDrive verifies Google Drive credentials by uploading and deleting a small test file.
func TestGoogleDrive(ctx context.Context, storage model.RemoteStorage) error {
	token, err := googleDriveAccessToken(ctx, storage, defaultGoogleOAuthTokenURL, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	client := newGoogleDriveClient(storage, token)
	key := path.Join(cleanGoogleDrivePath(storage.Path), ".databridge-connectivity-test-"+time.Now().UTC().Format("20060102T150405Z")+".txt")
	if _, err := client.uploadBytes(ctx, key, []byte("databridge google drive connectivity test\n")); err != nil {
		return fmt.Errorf("upload test file: %w", err)
	}
	if err := client.deleteByKey(ctx, key); err != nil {
		return fmt.Errorf("delete test file: %w", err)
	}
	return nil
}

// UploadGoogleDrive uploads a local file to Google Drive at the given objectKey path.
func UploadGoogleDrive(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	token, err := googleDriveAccessToken(ctx, storage, defaultGoogleOAuthTokenURL, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return 0, err
	}
	client := newGoogleDriveClient(storage, token)
	if info.Size() <= googleDriveSmallUploadLimit {
		body, err := os.ReadFile(filePath)
		if err != nil {
			return 0, err
		}
		if _, err := client.uploadBytes(ctx, objectKey, body); err != nil {
			return 0, err
		}
		return info.Size(), nil
	}
	if err := client.resumableUpload(ctx, objectKey, filePath, info.Size()); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

// DownloadGoogleDrive downloads a file from Google Drive at objectKey to filePath.
func DownloadGoogleDrive(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	token, err := googleDriveAccessToken(ctx, storage, defaultGoogleOAuthTokenURL, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return 0, err
	}
	client := newGoogleDriveClient(storage, token)
	return client.downloadByKey(ctx, objectKey, filePath)
}

// DeleteGoogleDrive deletes a file from Google Drive at objectKey (idempotent if missing).
func DeleteGoogleDrive(ctx context.Context, storage model.RemoteStorage, objectKey string) error {
	token, err := googleDriveAccessToken(ctx, storage, defaultGoogleOAuthTokenURL, &http.Client{Timeout: 30 * time.Second})
	if err != nil {
		return err
	}
	client := newGoogleDriveClient(storage, token)
	return client.deleteByKey(ctx, objectKey)
}

func googleDriveAccessToken(ctx context.Context, storage model.RemoteStorage, tokenEndpoint string, httpClient *http.Client) (string, error) {
	if strings.EqualFold(strings.TrimSpace(storage.AuthMode), "service_account") {
		return googleServiceAccountToken(ctx, storage.SecretKey, tokenEndpoint, httpClient)
	}
	credential := ParseGoogleDriveCredential(storage.SecretKey)
	if credential.RefreshToken == "" && credential.AccessToken == "" {
		return "", fmt.Errorf("google drive storage is not authorized")
	}
	if credential.AccessToken != "" && credential.ExpiresAt > time.Now().Add(2*time.Minute).Unix() {
		return credential.AccessToken, nil
	}
	if credential.RefreshToken == "" {
		return "", fmt.Errorf("google drive storage is not authorized")
	}
	form := url.Values{
		"client_id":     {strings.TrimSpace(storage.AccessKey)},
		"client_secret": {credential.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload googleTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || payload.AccessToken == "" {
		return "", fmt.Errorf("google drive token refresh failed: %s", strings.TrimSpace(payload.Error+" "+payload.Description))
	}
	return payload.AccessToken, nil
}

func googleServiceAccountToken(ctx context.Context, rawKey, defaultTokenURL string, httpClient *http.Client) (string, error) {
	saKey, err := ParseGoogleServiceAccountKey(rawKey)
	if err != nil {
		return "", err
	}
	privateKey, err := parseRSAPrivateKey(saKey.PrivateKey)
	if err != nil {
		return "", err
	}
	tokenURL := saKey.TokenURI
	if strings.TrimSpace(tokenURL) == "" {
		tokenURL = defaultTokenURL
	}
	now := time.Now().Unix()
	headerJSON, _ := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT"})
	claimsJSON, _ := json.Marshal(map[string]any{
		"iss":   saKey.ClientEmail,
		"scope": googleDriveScope,
		"aud":   tokenURL,
		"iat":   now,
		"exp":   now + 3600,
	})
	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." + base64.RawURLEncoding.EncodeToString(claimsJSON)
	digest := sha256.Sum256([]byte(signingInput))
	signature, err := rsa.SignPKCS1v15(rand.Reader, privateKey, crypto.SHA256, digest[:])
	if err != nil {
		return "", fmt.Errorf("sign service account JWT: %w", err)
	}
	assertion := signingInput + "." + base64.RawURLEncoding.EncodeToString(signature)
	form := url.Values{
		"grant_type": {"urn:ietf:params:oauth:grant-type:jwt-bearer"},
		"assertion":  {assertion},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var payload googleTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || payload.AccessToken == "" {
		return "", fmt.Errorf("google drive service account auth failed: %s", strings.TrimSpace(payload.Error+" "+payload.Description))
	}
	return payload.AccessToken, nil
}

func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("invalid PEM private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		rsaKey, ok := key.(*rsa.PrivateKey)
		if !ok {
			return nil, fmt.Errorf("private key is not RSA")
		}
		return rsaKey, nil
	}
	return x509.ParsePKCS1PrivateKey(block.Bytes)
}

type googleDriveClient struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

func newGoogleDriveClient(storage model.RemoteStorage, token string) googleDriveClient {
	endpoint := strings.TrimSpace(storage.Endpoint)
	if endpoint == "" {
		endpoint = defaultGoogleDriveEndpoint
	}
	return googleDriveClient{
		baseURL:    strings.TrimRight(endpoint, "/"),
		token:      token,
		httpClient: &http.Client{Timeout: 2 * time.Hour},
	}
}

func (c googleDriveClient) apiURL(p string) string {
	return c.baseURL + p
}

func (c googleDriveClient) ensureFolderPath(ctx context.Context, dirPath string) (string, error) {
	parentID := "root"
	cleaned := cleanGoogleDrivePath(dirPath)
	if cleaned == "" || cleaned == "." {
		return parentID, nil
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." {
			continue
		}
		existingID, found, err := c.findChild(ctx, parentID, segment, true)
		if err != nil {
			return "", err
		}
		if found {
			parentID = existingID
			continue
		}
		createdID, err := c.createFolder(ctx, parentID, segment)
		if err != nil {
			return "", err
		}
		parentID = createdID
	}
	return parentID, nil
}

func (c googleDriveClient) resolveFolderPath(ctx context.Context, dirPath string) (string, bool, error) {
	parentID := "root"
	cleaned := cleanGoogleDrivePath(dirPath)
	if cleaned == "" || cleaned == "." {
		return parentID, true, nil
	}
	for _, segment := range strings.Split(cleaned, "/") {
		if segment == "" || segment == "." {
			continue
		}
		existingID, found, err := c.findChild(ctx, parentID, segment, true)
		if err != nil {
			return "", false, err
		}
		if !found {
			return "", false, nil
		}
		parentID = existingID
	}
	return parentID, true, nil
}

func (c googleDriveClient) findChild(ctx context.Context, parentID, name string, folderOnly bool) (string, bool, error) {
	escapedName := strings.ReplaceAll(strings.ReplaceAll(name, `\`, `\\`), `'`, `\'`)
	mimeClause := fmt.Sprintf("mimeType != '%s'", googleDriveFolderMimeType)
	if folderOnly {
		mimeClause = fmt.Sprintf("mimeType = '%s'", googleDriveFolderMimeType)
	}
	q := fmt.Sprintf("'%s' in parents and name = '%s' and %s and trashed = false", parentID, escapedName, mimeClause)
	endpoint := c.apiURL("/drive/v3/files") + "?q=" + url.QueryEscape(q) + "&fields=files(id,name)&pageSize=1&supportsAllDrives=true&includeItemsFromAllDrives=true"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", false, googleDriveResponseError(resp)
	}
	var result struct {
		Files []struct {
			ID string `json:"id"`
		} `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result); err != nil {
		return "", false, err
	}
	if len(result.Files) == 0 {
		return "", false, nil
	}
	return result.Files[0].ID, true, nil
}

func (c googleDriveClient) createFolder(ctx context.Context, parentID, name string) (string, error) {
	metadata, err := json.Marshal(map[string]any{
		"name":     name,
		"mimeType": googleDriveFolderMimeType,
		"parents":  []string{parentID},
	})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL("/drive/v3/files?supportsAllDrives=true"), bytes.NewReader(metadata))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", googleDriveResponseError(resp)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&created); err != nil {
		return "", err
	}
	if created.ID == "" {
		return "", fmt.Errorf("google drive create folder returned empty id")
	}
	return created.ID, nil
}

func (c googleDriveClient) resolveFile(ctx context.Context, objectKey string) (string, bool, error) {
	cleaned := cleanGoogleDrivePath(objectKey)
	if cleaned == "" || cleaned == "." {
		return "", false, fmt.Errorf("google drive object key is empty")
	}
	dirPath := path.Dir(cleaned)
	fileName := path.Base(cleaned)
	parentID, found, err := c.resolveFolderPath(ctx, dirPath)
	if err != nil || !found {
		return "", false, err
	}
	return c.findChild(ctx, parentID, fileName, false)
}

func (c googleDriveClient) uploadBytes(ctx context.Context, objectKey string, body []byte) (string, error) {
	cleaned := cleanGoogleDrivePath(objectKey)
	if cleaned == "" || cleaned == "." {
		return "", fmt.Errorf("google drive object key is empty")
	}
	parentID, err := c.ensureFolderPath(ctx, path.Dir(cleaned))
	if err != nil {
		return "", err
	}
	fileName := path.Base(cleaned)
	existingID, exists, err := c.findChild(ctx, parentID, fileName, false)
	if err != nil {
		return "", err
	}

	metaMap := map[string]any{"name": fileName}
	method := http.MethodPost
	endpoint := c.apiURL("/upload/drive/v3/files?uploadType=multipart&supportsAllDrives=true")
	if exists {
		method = http.MethodPatch
		endpoint = c.apiURL("/upload/drive/v3/files/" + url.PathEscape(existingID) + "?uploadType=multipart&supportsAllDrives=true")
	} else {
		metaMap["parents"] = []string{parentID}
	}
	metaBytes, err := json.Marshal(metaMap)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	metaHeader := make(textproto.MIMEHeader)
	metaHeader.Set("Content-Type", "application/json; charset=UTF-8")
	metaPart, err := writer.CreatePart(metaHeader)
	if err != nil {
		return "", err
	}
	if _, err := metaPart.Write(metaBytes); err != nil {
		return "", err
	}
	dataHeader := make(textproto.MIMEHeader)
	dataHeader.Set("Content-Type", "application/octet-stream")
	dataPart, err := writer.CreatePart(dataHeader)
	if err != nil {
		return "", err
	}
	if _, err := dataPart.Write(body); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, method, endpoint, &buf)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "multipart/related; boundary="+writer.Boundary())
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", googleDriveResponseError(resp)
	}
	var uploaded struct {
		ID string `json:"id"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&uploaded)
	return uploaded.ID, nil
}

func (c googleDriveClient) resumableUpload(ctx context.Context, objectKey, filePath string, size int64) error {
	cleaned := cleanGoogleDrivePath(objectKey)
	if cleaned == "" || cleaned == "." {
		return fmt.Errorf("google drive object key is empty")
	}
	parentID, err := c.ensureFolderPath(ctx, path.Dir(cleaned))
	if err != nil {
		return err
	}
	fileName := path.Base(cleaned)
	existingID, exists, err := c.findChild(ctx, parentID, fileName, false)
	if err != nil {
		return err
	}

	metaMap := map[string]any{"name": fileName}
	method := http.MethodPost
	endpoint := c.apiURL("/upload/drive/v3/files?uploadType=resumable&supportsAllDrives=true")
	if exists {
		method = http.MethodPatch
		endpoint = c.apiURL("/upload/drive/v3/files/" + url.PathEscape(existingID) + "?uploadType=resumable&supportsAllDrives=true")
	} else {
		metaMap["parents"] = []string{parentID}
	}
	metaBytes, err := json.Marshal(metaMap)
	if err != nil {
		return err
	}

	initReq, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(metaBytes))
	if err != nil {
		return err
	}
	initReq.Header.Set("Authorization", "Bearer "+c.token)
	initReq.Header.Set("Content-Type", "application/json; charset=UTF-8")
	initReq.Header.Set("X-Upload-Content-Type", "application/octet-stream")
	initReq.Header.Set("X-Upload-Content-Length", fmt.Sprintf("%d", size))
	initResp, err := c.httpClient.Do(initReq)
	if err != nil {
		return err
	}
	uploadURL := initResp.Header.Get("Location")
	if initResp.StatusCode < 200 || initResp.StatusCode >= 300 || uploadURL == "" {
		defer initResp.Body.Close()
		if initResp.StatusCode >= 200 && initResp.StatusCode < 300 {
			return fmt.Errorf("create google drive upload session: missing Location header")
		}
		return googleDriveResponseError(initResp)
	}
	initResp.Body.Close()

	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	for offset := int64(0); offset < size; {
		length := googleDriveChunkSize
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		chunk := make([]byte, length)
		if _, err := io.ReadFull(file, chunk); err != nil {
			return err
		}
		partReq, err := http.NewRequestWithContext(ctx, http.MethodPut, uploadURL, bytes.NewReader(chunk))
		if err != nil {
			return err
		}
		partReq.Header.Set("Content-Length", fmt.Sprintf("%d", length))
		partReq.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, size))
		partResp, err := c.httpClient.Do(partReq)
		if err != nil {
			return err
		}
		okStatus := (partResp.StatusCode >= 200 && partResp.StatusCode < 300) || partResp.StatusCode == http.StatusPermanentRedirect
		if !okStatus {
			err = googleDriveResponseError(partResp)
		}
		partResp.Body.Close()
		if err != nil {
			return err
		}
		offset += length
	}
	return nil
}

func (c googleDriveClient) downloadByKey(ctx context.Context, objectKey, filePath string) (int64, error) {
	fileID, found, err := c.resolveFile(ctx, objectKey)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("google drive file %q not found", objectKey)
	}
	endpoint := c.apiURL("/drive/v3/files/" + url.PathEscape(fileID) + "?alt=media&supportsAllDrives=true")
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, googleDriveResponseError(resp)
	}
	output, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer output.Close()
	return io.Copy(output, resp.Body)
}

func (c googleDriveClient) deleteByKey(ctx context.Context, objectKey string) error {
	fileID, found, err := c.resolveFile(ctx, objectKey)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	endpoint := c.apiURL("/drive/v3/files/" + url.PathEscape(fileID) + "?supportsAllDrives=true")
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return googleDriveResponseError(resp)
	}
	return nil
}

func googleDriveResponseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("google drive API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func cleanGoogleDrivePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	return path.Clean(value)
}
