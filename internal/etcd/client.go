package etcd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"databridge/internal/dbtls"
	"databridge/internal/model"
)

type versionResponse struct {
	EtcdServer string `json:"etcdserver"`
}

type healthResponse struct {
	Health any    `json:"health"`
	Reason string `json:"reason"`
}

type memberListResponse struct {
	Members []struct {
		ClientURLs []string `json:"clientURLs"`
	} `json:"members"`
}

func Test(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	connection := model.Connection{Host: input.Host, Port: input.Port, Username: input.Username, Password: input.Password, SSLMode: input.SSLMode, TLSConfig: input.TLSConfig}
	started := time.Now()
	version, err := probe(ctx, connection)
	return time.Since(started), version, err
}

func ServerVersion(ctx context.Context, connection model.Connection) (string, error) {
	_, version, err := TestConnection(ctx, connection)
	return version, err
}

func TestConnection(ctx context.Context, connection model.Connection) (time.Duration, string, error) {
	started := time.Now()
	endpoints := append([]string(nil), connection.Endpoints...)
	seed := SeedEndpoint(connection)
	seenSeed := false
	for _, endpoint := range endpoints {
		if endpoint == seed {
			seenSeed = true
			break
		}
	}
	if !seenSeed {
		endpoints = append(endpoints, seed)
	}
	var failures []string
	for _, endpoint := range endpoints {
		candidate, err := connectionForEndpoint(connection, endpoint)
		if err != nil {
			failures = append(failures, err.Error())
			continue
		}
		version, err := probe(ctx, candidate)
		if err == nil {
			return time.Since(started), version, nil
		}
		failures = append(failures, endpoint+": "+err.Error())
	}
	return time.Since(started), "", fmt.Errorf("所有 etcd 节点均不可用: %s", strings.Join(failures, "; "))
}

func DiscoverEndpoints(ctx context.Context, connection model.Connection) ([]string, error) {
	tempDir, err := os.MkdirTemp("", "databridge-etcd-discovery-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tempDir)
	args, err := CommandArgs(connection, SeedEndpoint(connection), tempDir)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, "etcdctl", append(args, "member", "list", "--write-out=json")...)
	cmd.Env = append(os.Environ(), "ETCDCTL_API=3")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("发现 etcd 集群成员失败: %w: %s", err, strings.TrimSpace(string(output)))
	}
	response, err := parseMemberListOutput(output)
	if err != nil {
		return nil, fmt.Errorf("解析 etcd 集群成员: %w", err)
	}
	seen := map[string]bool{}
	var endpoints []string
	for _, member := range response.Members {
		for _, endpoint := range member.ClientURLs {
			endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
			if endpoint != "" && !seen[endpoint] {
				seen[endpoint] = true
				endpoints = append(endpoints, endpoint)
			}
		}
	}
	if len(endpoints) == 0 {
		return nil, fmt.Errorf("etcd 成员列表没有可用的 clientURLs")
	}
	sort.Strings(endpoints)
	return endpoints, nil
}

func parseMemberListOutput(output []byte) (memberListResponse, error) {
	var lastErr error
	text := string(output)
	for index, char := range text {
		if char != '{' {
			continue
		}
		var response memberListResponse
		decoder := json.NewDecoder(strings.NewReader(text[index:]))
		if err := decoder.Decode(&response); err != nil {
			lastErr = err
			continue
		}
		if response.Members != nil {
			return response, nil
		}
	}
	if lastErr != nil {
		return memberListResponse{}, lastErr
	}
	return memberListResponse{}, fmt.Errorf("未找到 etcd member list JSON")
}

func HealthyEndpoint(ctx context.Context, connection model.Connection, endpoint string) error {
	candidate, err := connectionForEndpoint(connection, endpoint)
	if err != nil {
		return err
	}
	_, err = probe(ctx, candidate)
	return err
}

func connectionForEndpoint(connection model.Connection, endpoint string) (model.Connection, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Hostname() == "" {
		return model.Connection{}, fmt.Errorf("无效的 etcd endpoint %q", endpoint)
	}
	port := connection.Port
	if parsed.Port() != "" {
		if _, err := fmt.Sscanf(parsed.Port(), "%d", &port); err != nil {
			return model.Connection{}, err
		}
	}
	candidate := connection
	candidate.Host, candidate.Port = parsed.Hostname(), port
	if parsed.Scheme == "http" {
		candidate.SSLMode = "disable"
	} else if parsed.Scheme == "https" && candidate.SSLMode == "disable" {
		candidate.SSLMode = "verify-full"
	}
	return candidate, nil
}

func SeedEndpoint(connection model.Connection) string {
	scheme := "http"
	if strings.ToLower(strings.TrimSpace(connection.SSLMode)) != "disable" {
		scheme = "https"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, connection.Host, connection.Port)
}

func CommandArgs(connection model.Connection, endpoint, tempDir string) ([]string, error) {
	args := []string{"--endpoints=" + endpoint}
	if connection.Username != "" {
		args = append(args, "--user="+connection.Username+":"+connection.Password)
	}
	files := []struct{ flag, name, value string }{{"--cacert=", "ca.pem", connection.TLSConfig.CACert}, {"--cert=", "client.pem", connection.TLSConfig.ClientCert}, {"--key=", "client-key.pem", connection.TLSConfig.ClientKey}}
	for _, item := range files {
		if strings.TrimSpace(item.value) == "" {
			continue
		}
		file := filepath.Join(tempDir, item.name)
		if err := os.WriteFile(file, []byte(item.value), 0o600); err != nil {
			return nil, err
		}
		args = append(args, item.flag+file)
	}
	if connection.SSLMode == "require" {
		args = append(args, "--insecure-skip-tls-verify=true")
	}
	return args, nil
}

func probe(ctx context.Context, connection model.Connection) (string, error) {
	client, baseURL, err := httpClient(connection)
	if err != nil {
		return "", err
	}
	var version versionResponse
	if err := getJSON(ctx, client, baseURL+"/version", connection, &version); err != nil {
		return "", fmt.Errorf("读取 etcd 版本: %w", err)
	}
	var health healthResponse
	if err := getJSON(ctx, client, baseURL+"/health", connection, &health); err != nil {
		return "", fmt.Errorf("检查 etcd 健康状态: %w", err)
	}
	healthy := strings.EqualFold(fmt.Sprint(health.Health), "true")
	if !healthy {
		return "", fmt.Errorf("etcd 集群不健康: %s", strings.TrimSpace(health.Reason))
	}
	return strings.TrimPrefix(strings.TrimSpace(version.EtcdServer), "v"), nil
}

func httpClient(connection model.Connection) (*http.Client, string, error) {
	scheme := "http"
	transport := &http.Transport{}
	if strings.ToLower(strings.TrimSpace(connection.SSLMode)) != "disable" {
		scheme = "https"
		tlsConfig, err := dbtls.Config(connection.Host, connection.TLSConfig, connection.SSLMode == "verify-full")
		if err != nil {
			return nil, "", err
		}
		if tlsConfig == nil {
			tlsConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}
		if connection.SSLMode == "require" {
			tlsConfig.InsecureSkipVerify = true
		} // encryption without hostname verification
		transport.TLSClientConfig = tlsConfig
	}
	return &http.Client{Transport: transport, Timeout: 10 * time.Second}, fmt.Sprintf("%s://%s:%d", scheme, connection.Host, connection.Port), nil
}

func getJSON(ctx context.Context, client *http.Client, url string, connection model.Connection, target any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	if connection.Username != "" {
		req.SetBasicAuth(connection.Username, connection.Password)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(target); err != nil {
		return err
	}
	return nil
}
