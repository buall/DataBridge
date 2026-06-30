package remotestorage

import (
	"context"
	"encoding/xml"
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

const testWebDAVBody = "databridge webdav connectivity test\n"

type WebDAVEntry struct {
	Name        string `json:"name"`
	Path        string `json:"path"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size,omitempty"`
	Modified    string `json:"modified,omitempty"`
}

func TestWebDAV(ctx context.Context, storage model.RemoteStorage) error {
	if err := validateWebDAVStorage(storage); err != nil {
		return err
	}
	objectKey := path.Join(cleanWebDAVPath(storage.Path), ".databridge-connectivity-test-"+time.Now().UTC().Format("20060102T150405Z")+".txt")
	if err := webDAVRequest(ctx, http.MethodPut, storage, objectKey, strings.NewReader(testWebDAVBody), nil); err != nil {
		return fmt.Errorf("upload test file: %w", err)
	}
	if err := DeleteWebDAV(ctx, storage, objectKey); err != nil {
		return fmt.Errorf("delete test file: %w", err)
	}
	return nil
}

func UploadWebDAV(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	if err := validateWebDAVStorage(storage); err != nil {
		return 0, err
	}
	if err := EnsureWebDAVCollection(ctx, storage, path.Dir(objectKey)); err != nil {
		return 0, err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return 0, err
	}
	headers := http.Header{}
	headers.Set("Content-Type", objectContentType(objectKey))
	headers.Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	if err := webDAVRequest(ctx, http.MethodPut, storage, objectKey, file, headers); err != nil {
		return 0, err
	}
	return info.Size(), nil
}

func DownloadWebDAV(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	resp, err := webDAVResponse(ctx, http.MethodGet, storage, objectKey, nil, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	output, err := os.OpenFile(filePath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return 0, err
	}
	defer output.Close()
	size, err := io.Copy(output, resp.Body)
	if err != nil {
		return 0, err
	}
	return size, nil
}

func DeleteWebDAV(ctx context.Context, storage model.RemoteStorage, objectKey string) error {
	return webDAVRequest(ctx, http.MethodDelete, storage, objectKey, nil, nil)
}

func ListWebDAVDirectory(ctx context.Context, storage model.RemoteStorage, dir string) ([]WebDAVEntry, error) {
	if err := validateWebDAVStorage(storage); err != nil {
		return nil, err
	}
	headers := http.Header{}
	headers.Set("Depth", "1")
	resp, err := webDAVResponse(ctx, "PROPFIND", storage, dir, nil, headers)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	var parsed webDAVMultiStatus
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&parsed); err != nil {
		return nil, fmt.Errorf("parse webdav directory: %w", err)
	}
	basePath := cleanWebDAVPath(dir)
	entries := make([]WebDAVEntry, 0, len(parsed.Responses))
	for _, item := range parsed.Responses {
		entry, ok := webDAVEntryFromResponse(item, basePath)
		if ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

func CreateWebDAVDirectory(ctx context.Context, storage model.RemoteStorage, dir string) error {
	if err := validateWebDAVStorage(storage); err != nil {
		return err
	}
	dir = cleanWebDAVPath(dir)
	if dir == "" {
		return fmt.Errorf("directory name is required")
	}
	return EnsureWebDAVCollection(ctx, storage, dir)
}

func EnsureWebDAVCollection(ctx context.Context, storage model.RemoteStorage, dir string) error {
	dir = cleanWebDAVPath(dir)
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	parts := strings.Split(strings.Trim(dir, "/"), "/")
	current := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		current = path.Join(current, part)
		err := webDAVRequest(ctx, "MKCOL", storage, current, nil, nil)
		if err == nil || isWebDAVCollectionExists(err) {
			continue
		}
		return err
	}
	return nil
}

func validateWebDAVStorage(storage model.RemoteStorage) error {
	if strings.TrimSpace(storage.Endpoint) == "" || strings.TrimSpace(storage.AccessKey) == "" || storage.SecretKey == "" {
		return fmt.Errorf("webdav endpoint, username and password are required")
	}
	return nil
}

func webDAVRequest(ctx context.Context, method string, storage model.RemoteStorage, objectKey string, body io.Reader, headers http.Header) error {
	resp, err := webDAVResponse(ctx, method, storage, objectKey, body, headers)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func webDAVResponse(ctx context.Context, method string, storage model.RemoteStorage, objectKey string, body io.Reader, headers http.Header) (*http.Response, error) {
	endpoint, err := webDAVURL(storage, objectKey)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(strings.TrimSpace(storage.AccessKey), storage.SecretKey)
	for key, values := range headers {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, webDAVStatusError{StatusCode: resp.StatusCode, Message: fmt.Sprintf("%s %s failed: %s %s", method, objectKey, resp.Status, strings.TrimSpace(string(responseBody)))}
	}
	return resp, nil
}

func webDAVURL(storage model.RemoteStorage, objectKey string) (string, error) {
	endpoint, err := url.Parse(strings.TrimRight(storage.Endpoint, "/"))
	if err != nil {
		return "", fmt.Errorf("invalid endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return "", fmt.Errorf("endpoint must include scheme and host")
	}
	endpoint.Path = path.Join(endpoint.EscapedPath(), cleanWebDAVPath(objectKey))
	endpoint.RawQuery = ""
	return endpoint.String(), nil
}

func cleanWebDAVPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.Trim(value, "/")
	if value == "" {
		return ""
	}
	return path.Clean(value)
}

type webDAVStatusError struct {
	StatusCode int
	Message    string
}

func (e webDAVStatusError) Error() string {
	return e.Message
}

func isWebDAVCollectionExists(err error) bool {
	statusErr, ok := err.(webDAVStatusError)
	return ok && (statusErr.StatusCode == http.StatusMethodNotAllowed || statusErr.StatusCode == http.StatusConflict)
}

type webDAVMultiStatus struct {
	Responses []webDAVResponseItem `xml:"response"`
}

type webDAVResponseItem struct {
	Href     string             `xml:"href"`
	Propstat []webDAVPropStatus `xml:"propstat"`
}

type webDAVPropStatus struct {
	Status string     `xml:"status"`
	Prop   webDAVProp `xml:"prop"`
}

type webDAVProp struct {
	DisplayName   string             `xml:"displayname"`
	GetContentLen int64              `xml:"getcontentlength"`
	GetLastMod    string             `xml:"getlastmodified"`
	ResourceType  webDAVResourceType `xml:"resourcetype"`
}

type webDAVResourceType struct {
	Collection *struct{} `xml:"collection"`
}

func webDAVEntryFromResponse(item webDAVResponseItem, basePath string) (WebDAVEntry, bool) {
	prop := webDAVProp{}
	for _, propstat := range item.Propstat {
		if strings.Contains(propstat.Status, " 200 ") || strings.HasSuffix(propstat.Status, " 200 OK") {
			prop = propstat.Prop
			break
		}
	}
	href, err := url.PathUnescape(item.Href)
	if err != nil {
		href = item.Href
	}
	cleanHref := cleanWebDAVPath(href)
	if cleanHref == basePath {
		return WebDAVEntry{}, false
	}
	name := strings.TrimSpace(prop.DisplayName)
	if name == "" {
		name = path.Base(cleanHref)
	}
	if name == "." || name == "/" || name == "" {
		return WebDAVEntry{}, false
	}
	return WebDAVEntry{
		Name:        name,
		Path:        cleanHref,
		IsDirectory: prop.ResourceType.Collection != nil,
		Size:        prop.GetContentLen,
		Modified:    prop.GetLastMod,
	}, true
}
