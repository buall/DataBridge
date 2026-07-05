package remotestorage

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path"
	"sort"
	"strings"
	"time"

	"databridge/internal/model"
)

const testObjectBody = "databridge object storage connectivity test\n"

const maxMCOutputBytes = 64 << 10

var findMCExecutable = func() (string, error) { return exec.LookPath("mc") }

func TestObjectStorage(ctx context.Context, storage model.RemoteStorage) error {
	if strings.TrimSpace(storage.Endpoint) == "" || strings.TrimSpace(storage.Bucket) == "" || strings.TrimSpace(storage.AccessKey) == "" || storage.SecretKey == "" {
		return fmt.Errorf("object storage endpoint, bucket, access key and secret key are required")
	}
	objectKey := cleanObjectKey(storage.Path, ".databridge/connectivity-test-"+time.Now().UTC().Format("20060102T150405Z")+".txt")
	body := []byte(testObjectBody)
	if err := signedObjectRequest(ctx, http.MethodPut, storage, objectKey, body); err != nil {
		return fmt.Errorf("upload test object: %w", err)
	}
	if err := signedObjectRequest(ctx, http.MethodDelete, storage, objectKey, nil); err != nil {
		return fmt.Errorf("delete test object: %w", err)
	}
	return nil
}

func ListObjectBuckets(ctx context.Context, input model.StorageInput) ([]string, error) {
	storage := model.RemoteStorage{
		Endpoint:  strings.TrimSpace(input.Endpoint),
		Region:    strings.TrimSpace(input.Region),
		AccessKey: strings.TrimSpace(input.AccessKey),
		SecretKey: input.SecretKey,
	}
	if storage.Endpoint == "" || storage.AccessKey == "" || storage.SecretKey == "" {
		return nil, fmt.Errorf("object storage endpoint, access key and secret key are required")
	}
	body, err := signedServiceRequest(ctx, http.MethodGet, storage)
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Buckets []struct {
			Name string `xml:"Name"`
		} `xml:"Buckets>Bucket"`
	}
	if err := xml.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("parse bucket list: %w", err)
	}
	buckets := make([]string, 0, len(parsed.Buckets))
	for _, bucket := range parsed.Buckets {
		if strings.TrimSpace(bucket.Name) != "" {
			buckets = append(buckets, bucket.Name)
		}
	}
	sort.Strings(buckets)
	return buckets, nil
}

func CreateObjectBucket(ctx context.Context, storage model.RemoteStorage, bucketName string) error {
	bucketName = cleanBucketName(bucketName)
	if bucketName == "" {
		return fmt.Errorf("bucket name is required")
	}
	endpoint, err := url.Parse(strings.TrimRight(storage.Endpoint, "/"))
	if err != nil {
		return fmt.Errorf("invalid endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return fmt.Errorf("endpoint must include scheme and host")
	}
	endpoint.Path = path.Join(endpoint.EscapedPath(), bucketName)
	endpoint.RawQuery = ""
	payloadHash := sha256Hex(nil)
	now := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, endpoint.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Host", endpoint.Host)
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signS3Request(req, storage, now, payloadHash)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("create bucket failed: %s %s", resp.Status, strings.TrimSpace(string(snippet)))
	}
	return nil
}

func UploadObject(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, err
	}
	mcPath, err := findMCExecutable()
	if err != nil {
		return 0, fmt.Errorf("mc not found in PATH: %w", err)
	}
	endpoint, err := url.Parse(strings.TrimRight(storage.Endpoint, "/"))
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return 0, fmt.Errorf("invalid object storage endpoint %q", storage.Endpoint)
	}
	endpoint.User = url.UserPassword(storage.AccessKey, storage.SecretKey)
	configDir, err := os.MkdirTemp("", "databridge-mc-*")
	if err != nil {
		return 0, err
	}
	defer os.RemoveAll(configDir)

	const alias = "databridge"
	target := alias + "/" + path.Join(storage.Bucket, objectKey)
	cmd := exec.CommandContext(ctx, mcPath, "--config-dir", configDir, "--quiet", "cp", filePath, target)
	cmd.Env = append(os.Environ(), "MC_HOST_"+alias+"="+endpoint.String(), "MC_NO_COLOR=1")
	output := newMCOutputBuffer()
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Run(); err != nil {
		message := output.String()
		if message != "" {
			return 0, fmt.Errorf("mc upload failed: %w: %s", err, message)
		}
		return 0, fmt.Errorf("mc upload failed: %w", err)
	}
	return info.Size(), nil
}

type mcOutputBuffer struct {
	data []byte
}

func newMCOutputBuffer() mcOutputBuffer {
	return mcOutputBuffer{data: make([]byte, 0, maxMCOutputBytes)}
}

func (b *mcOutputBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) >= maxMCOutputBytes {
		b.data = append(b.data[:0], p[len(p)-maxMCOutputBytes:]...)
		return written, nil
	}
	if overflow := len(b.data) + len(p) - maxMCOutputBytes; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *mcOutputBuffer) String() string { return strings.TrimSpace(string(b.data)) }

func DownloadObject(ctx context.Context, storage model.RemoteStorage, objectKey, filePath string) (int64, error) {
	resp, err := signedObjectResponse(ctx, http.MethodGet, storage, objectKey, nil)
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

func DeleteObject(ctx context.Context, storage model.RemoteStorage, objectKey string) error {
	return signedObjectRequest(ctx, http.MethodDelete, storage, objectKey, nil)
}

func signedServiceRequest(ctx context.Context, method string, storage model.RemoteStorage) ([]byte, error) {
	endpoint, err := url.Parse(strings.TrimRight(storage.Endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("endpoint must include scheme and host")
	}
	endpoint.Path = canonicalPath(endpoint.EscapedPath())
	endpoint.RawQuery = ""
	payloadHash := sha256Hex(nil)
	now := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Host", endpoint.Host)
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	signS3Request(req, storage, now, payloadHash)
	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("list buckets failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return body, nil
}

func signedObjectRequest(ctx context.Context, method string, storage model.RemoteStorage, objectKey string, body []byte) error {
	resp, err := signedObjectResponse(ctx, method, storage, objectKey, body)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func signedObjectResponse(ctx context.Context, method string, storage model.RemoteStorage, objectKey string, body []byte) (*http.Response, error) {
	endpoint, err := url.Parse(strings.TrimRight(storage.Endpoint, "/"))
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint: %w", err)
	}
	if endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, fmt.Errorf("endpoint must include scheme and host")
	}
	endpoint.Path = path.Join(endpoint.EscapedPath(), storage.Bucket, objectKey)
	endpoint.RawQuery = ""

	payloadHash := sha256Hex(body)
	now := time.Now().UTC()
	req, err := http.NewRequestWithContext(ctx, method, endpoint.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Host", endpoint.Host)
	req.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	req.Header.Set("X-Amz-Content-Sha256", payloadHash)
	if method == http.MethodPut {
		req.Header.Set("Content-Type", objectContentType(objectKey))
	}
	signS3Request(req, storage, now, payloadHash)

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		snippet := string(responseBody)
		return nil, fmt.Errorf("%s %s failed: %s %s", method, objectKey, resp.Status, strings.TrimSpace(snippet))
	}
	return resp, nil
}

func signS3Request(req *http.Request, storage model.RemoteStorage, now time.Time, payloadHash string) {
	region := strings.TrimSpace(storage.Region)
	if region == "" {
		region = "us-east-1"
	}
	date := now.Format("20060102")
	scope := date + "/" + region + "/s3/aws4_request"
	signedHeaders, canonicalHeaders := canonicalRequestHeaders(req.Header, req.URL.Host)
	canonicalURI := canonicalPath(req.URL.EscapedPath())
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		req.URL.RawQuery,
		canonicalHeaders,
		signedHeaders,
		payloadHash,
	}, "\n")
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		now.Format("20060102T150405Z"),
		scope,
		sha256Hex([]byte(canonicalRequest)),
	}, "\n")
	signingKey := s3SigningKey(storage.SecretKey, date, region)
	signature := hex.EncodeToString(hmacSHA256(signingKey, stringToSign))
	req.Header.Set("Authorization", fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s", storage.AccessKey, scope, signedHeaders, signature))
}

func canonicalRequestHeaders(headers http.Header, host string) (string, string) {
	values := map[string]string{"host": host}
	for key, list := range headers {
		lower := strings.ToLower(strings.TrimSpace(key))
		if lower == "authorization" {
			continue
		}
		joined := strings.Join(list, ",")
		values[lower] = strings.Join(strings.Fields(joined), " ")
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var canonical strings.Builder
	for _, key := range keys {
		canonical.WriteString(key)
		canonical.WriteByte(':')
		canonical.WriteString(values[key])
		canonical.WriteByte('\n')
	}
	return strings.Join(keys, ";"), canonical.String()
}

func s3SigningKey(secret, date, region string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), date)
	kRegion := hmacSHA256(kDate, region)
	kService := hmacSHA256(kRegion, "s3")
	return hmacSHA256(kService, "aws4_request")
}

func hmacSHA256(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func canonicalPath(value string) string {
	if value == "" {
		return "/"
	}
	if !strings.HasPrefix(value, "/") {
		value = "/" + value
	}
	return value
}

func cleanObjectKey(prefix, name string) string {
	prefix = strings.Trim(strings.TrimSpace(prefix), "/")
	name = strings.TrimLeft(name, "/")
	if prefix == "" {
		return name
	}
	return prefix + "/" + name
}

func cleanBucketName(value string) string {
	return strings.Trim(strings.TrimSpace(value), "/")
}

func objectContentType(objectKey string) string {
	if strings.HasSuffix(strings.ToLower(objectKey), ".tar.gz") || strings.HasSuffix(strings.ToLower(objectKey), ".tgz") {
		return "application/gzip"
	}
	return "application/octet-stream"
}
