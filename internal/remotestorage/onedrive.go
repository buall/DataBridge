package remotestorage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"
	"time"

	"databridge/internal/model"
)

const oneDriveSmallUploadLimit int64 = 4 << 20

type OneDriveCredential struct {
	ClientSecret string `json:"clientSecret"`
	RefreshToken string `json:"refreshToken,omitempty"`
	AccessToken  string `json:"accessToken,omitempty"`
	ExpiresAt    int64  `json:"expiresAt,omitempty"`
}

type oneDriveTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
	Error        string `json:"error"`
	Description  string `json:"error_description"`
}

func ParseOneDriveCredential(secret string) OneDriveCredential {
	var credential OneDriveCredential
	if json.Unmarshal([]byte(secret), &credential) == nil && credential.ClientSecret != "" {
		return credential
	}
	return OneDriveCredential{ClientSecret: secret}
}

func EncodeOneDriveCredential(credential OneDriveCredential) (string, error) {
	if strings.TrimSpace(credential.ClientSecret) == "" {
		return "", fmt.Errorf("onedrive client secret is required")
	}
	data, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func OneDriveAuthorizeURL(storage model.RemoteStorage, redirectURI, state string) (string, error) {
	if strings.TrimSpace(storage.Endpoint) == "" || strings.TrimSpace(storage.AccessKey) == "" {
		return "", fmt.Errorf("onedrive graph endpoint and client id are required")
	}
	credential := ParseOneDriveCredential(storage.SecretKey)
	if credential.ClientSecret == "" {
		return "", fmt.Errorf("onedrive client secret is required")
	}
	authorize, err := oneDriveOAuthEndpoint(storage.Endpoint, true)
	if err != nil {
		return "", err
	}
	query := authorize.Query()
	query.Set("client_id", strings.TrimSpace(storage.AccessKey))
	query.Set("response_type", "code")
	query.Set("redirect_uri", redirectURI)
	query.Set("response_mode", "query")
	query.Set("scope", "offline_access Files.ReadWrite.All")
	query.Set("state", state)
	authorize.RawQuery = query.Encode()
	return authorize.String(), nil
}

func OneDriveExchangeCode(ctx context.Context, storage model.RemoteStorage, code, redirectURI string) (OneDriveCredential, error) {
	credential := ParseOneDriveCredential(storage.SecretKey)
	if credential.ClientSecret == "" {
		return OneDriveCredential{}, fmt.Errorf("onedrive client secret is required")
	}
	tokenURL, err := oneDriveOAuthEndpoint(storage.Endpoint, false)
	if err != nil {
		return OneDriveCredential{}, err
	}
	form := url.Values{
		"client_id":     {strings.TrimSpace(storage.AccessKey)},
		"client_secret": {credential.ClientSecret},
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	response, err := http.PostForm(tokenURL.String(), form)
	if err != nil {
		return OneDriveCredential{}, err
	}
	defer response.Body.Close()
	var payload oneDriveTokenResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&payload); err != nil {
		return OneDriveCredential{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 || payload.AccessToken == "" {
		return OneDriveCredential{}, fmt.Errorf("onedrive OAuth failed: %s", strings.TrimSpace(payload.Error+" "+payload.Description))
	}
	if payload.RefreshToken != "" {
		credential.RefreshToken = payload.RefreshToken
	}
	credential.AccessToken = payload.AccessToken
	credential.ExpiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).Unix()
	return credential, nil
}

func oneDriveOAuthEndpoint(graphEndpoint string, authorize bool) (*url.URL, error) {
	base, err := url.Parse(strings.TrimRight(graphEndpoint, "/"))
	if err != nil || base.Host == "" {
		return nil, fmt.Errorf("invalid onedrive graph endpoint %q", graphEndpoint)
	}
	host := "login.microsoftonline.com"
	if strings.Contains(strings.ToLower(base.Host), "chinacloudapi.cn") {
		host = "login.chinacloudapi.cn"
	}
	base.Scheme = "https"
	base.Host = host
	if authorize {
		base.Path = "/common/oauth2/v2.0/authorize"
	} else {
		base.Path = "/common/oauth2/v2.0/token"
	}
	base.RawQuery = ""
	return base, nil
}

func TestOneDrive(ctx context.Context, storage model.RemoteStorage) error {
	credential, token, err := oneDriveAccessToken(ctx, storage)
	if err != nil {
		return err
	}
	client := oneDriveClient{storage: storage, token: token, credential: credential}
	key := path.Join(cleanOneDrivePath(storage.Path), ".databridge-connectivity-test-"+time.Now().UTC().Format("20060102T150405Z")+".txt")
	if _, err := client.simpleUpload(ctx, key, []byte("databridge onedrive connectivity test\n")); err != nil {
		return fmt.Errorf("upload test file: %w", err)
	}
	if err := client.delete(ctx, key); err != nil {
		return fmt.Errorf("delete test file: %w", err)
	}
	return nil
}

func UploadOneDrive(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	credential, token, err := oneDriveAccessToken(ctx, storage)
	if err != nil {
		return 0, err
	}
	client := oneDriveClient{storage: storage, token: token, credential: credential}
	if info.Size() <= oneDriveSmallUploadLimit {
		file, err := os.Open(filePath)
		if err != nil {
			return 0, err
		}
		defer file.Close()
		body, err := io.ReadAll(file)
		if err != nil {
			return 0, err
		}
		_, err = client.simpleUpload(ctx, objectKey, body)
		return info.Size(), err
	}
	if err := client.chunkedUpload(ctx, objectKey, filePath, info.Size()); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func DownloadOneDrive(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	_, token, err := oneDriveAccessToken(ctx, storage)
	if err != nil {
		return 0, err
	}
	client := oneDriveClient{storage: storage, token: token}
	resp, err := client.request(ctx, http.MethodGet, client.itemPath(objectKey)+"/content", nil, true)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	output, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer output.Close()
	return io.Copy(output, resp.Body)
}

func DeleteOneDrive(ctx context.Context, storage model.RemoteStorage, objectKey string) error {
	_, token, err := oneDriveAccessToken(ctx, storage)
	if err != nil {
		return err
	}
	client := oneDriveClient{storage: storage, token: token}
	_, err = client.request(ctx, http.MethodDelete, client.itemPath(objectKey), nil, false)
	return err
}

func oneDriveAccessToken(ctx context.Context, storage model.RemoteStorage) (OneDriveCredential, string, error) {
	credential := ParseOneDriveCredential(storage.SecretKey)
	if credential.RefreshToken == "" && credential.AccessToken == "" {
		return credential, "", fmt.Errorf("onedrive storage is not authorized")
	}
	if credential.AccessToken != "" && credential.ExpiresAt > time.Now().Add(2*time.Minute).Unix() {
		return credential, credential.AccessToken, nil
	}
	tokenURL, err := oneDriveOAuthEndpoint(storage.Endpoint, false)
	if err != nil {
		return credential, "", err
	}
	form := url.Values{
		"client_id":     {strings.TrimSpace(storage.AccessKey)},
		"client_secret": {credential.ClientSecret},
		"grant_type":    {"refresh_token"},
		"refresh_token": {credential.RefreshToken},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return credential, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return credential, "", err
	}
	defer resp.Body.Close()
	var payload oneDriveTokenResponse
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&payload); err != nil {
		return credential, "", err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || payload.AccessToken == "" {
		return credential, "", fmt.Errorf("onedrive token refresh failed: %s", strings.TrimSpace(payload.Error+" "+payload.Description))
	}
	credential.AccessToken = payload.AccessToken
	credential.ExpiresAt = time.Now().Add(time.Duration(payload.ExpiresIn) * time.Second).Unix()
	if payload.RefreshToken != "" {
		credential.RefreshToken = payload.RefreshToken
	}
	return credential, credential.AccessToken, nil
}

type oneDriveClient struct {
	storage    model.RemoteStorage
	token      string
	credential OneDriveCredential
}

func (c oneDriveClient) graphURL(resource string) (string, error) {
	base, err := url.Parse(strings.TrimRight(c.storage.Endpoint, "/"))
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", fmt.Errorf("invalid onedrive graph endpoint %q", c.storage.Endpoint)
	}
	base.Path = path.Join(base.Path, "me/drive", resource)
	base.RawQuery = ""
	return base.String(), nil
}

func (c oneDriveClient) itemPath(objectKey string) string {
	return "root:/" + strings.Trim(strings.ReplaceAll(objectKey, "\\", "/"), "/") + ":"
}

func (c oneDriveClient) simpleUpload(ctx context.Context, objectKey string, body []byte) (int64, error) {
	return c.doUpload(ctx, c.itemPath(objectKey)+"/content", bytes.NewReader(body), int64(len(body)), true)
}

func (c oneDriveClient) doUpload(ctx context.Context, resource string, body io.Reader, size int64, followRedirect bool) (int64, error) {
	endpoint, err := c.graphURL(resource)
	if err != nil {
		return 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint, body)
	if err != nil {
		return 0, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/octet-stream")
	req.ContentLength = size
	client := &http.Client{Timeout: 2 * time.Hour}
	if !followRedirect {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return 0, oneDriveResponseError(resp)
	}
	return size, nil
}

func (c oneDriveClient) request(ctx context.Context, method, resource string, body io.Reader, followRedirect bool) (*http.Response, error) {
	endpoint, err := c.graphURL(resource)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	client := &http.Client{Timeout: 2 * time.Hour}
	if !followRedirect {
		client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, oneDriveResponseError(resp)
	}
	return resp, nil
}

func (c oneDriveClient) delete(ctx context.Context, objectKey string) error {
	resp, err := c.request(ctx, http.MethodDelete, c.itemPath(objectKey), nil, false)
	if resp != nil {
		resp.Body.Close()
	}
	return err
}

func (c oneDriveClient) chunkedUpload(ctx context.Context, objectKey, filePath string, size int64) error {
	endpoint, err := c.graphURL(c.itemPath(objectKey) + "/createUploadSession")
	if err != nil {
		return err
	}
	payload := strings.NewReader(`{"item":{"@microsoft.graph.conflictBehavior":"replace"}}`)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, payload)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	var session struct {
		UploadURL string `json:"uploadUrl"`
	}
	decodeErr := json.NewDecoder(resp.Body).Decode(&session)
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || decodeErr != nil || session.UploadURL == "" {
		if decodeErr != nil {
			return decodeErr
		}
		return fmt.Errorf("create onedrive upload session: %s", resp.Status)
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	const chunkSize int64 = 10 * 1024 * 1024
	for offset := int64(0); offset < size; {
		length := chunkSize
		if remaining := size - offset; remaining < length {
			length = remaining
		}
		chunk := make([]byte, length)
		if _, err := io.ReadFull(file, chunk); err != nil {
			return err
		}
		part, err := http.NewRequestWithContext(ctx, http.MethodPut, session.UploadURL, bytes.NewReader(chunk))
		if err != nil {
			return err
		}
		part.Header.Set("Content-Length", fmt.Sprintf("%d", length))
		part.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, size))
		response, err := (&http.Client{Timeout: 2 * time.Hour}).Do(part)
		if err != nil {
			return err
		}
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			err = oneDriveResponseError(response)
		}
		response.Body.Close()
		if err != nil {
			return err
		}
		offset += length
	}
	return nil
}

func oneDriveResponseError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	return fmt.Errorf("onedrive API returned %s: %s", resp.Status, strings.TrimSpace(string(body)))
}

func cleanOneDrivePath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	return path.Clean(value)
}
