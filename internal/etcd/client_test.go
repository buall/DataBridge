package etcd

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"databridge/internal/model"
)

func TestServerVersionChecksVersionHealthAndBasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, password, ok := r.BasicAuth()
		if !ok || username != "root" || password != "secret" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"etcdserver":"3.5.21"}`))
			return
		}
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"health":"true"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()

	endpoint, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(endpoint.Port())
	version, err := ServerVersion(context.Background(), model.Connection{Host: endpoint.Hostname(), Port: port, Username: "root", Password: "secret", SSLMode: "disable"})
	if err != nil {
		t.Fatal(err)
	}
	if version != "3.5.21" {
		t.Fatalf("version = %q", version)
	}
}

func TestDiscoverEndpointsFromSeedMemberList(t *testing.T) {
	dir := t.TempDir()
	tool := filepath.Join(dir, "etcdctl")
	script := "#!/bin/sh\nprintf '%s' '{\"members\":[{\"clientURLs\":[\"https://etcd-2:2379\",\"https://etcd-1:2379\"]},{\"clientURLs\":[\"https://etcd-2:2379\"]}]}'\n"
	if err := os.WriteFile(tool, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	endpoints, err := DiscoverEndpoints(context.Background(), model.Connection{Host: "seed", Port: 2379, SSLMode: "require"})
	if err != nil {
		t.Fatal(err)
	}
	if len(endpoints) != 2 || endpoints[0] != "https://etcd-1:2379" || endpoints[1] != "https://etcd-2:2379" {
		t.Fatalf("endpoints = %#v", endpoints)
	}
}

func TestParseMemberListOutputSkipsExtraJSON(t *testing.T) {
	output := []byte(`{"cluster_id":"ignored"}{"members":[{"clientURLs":["https://etcd-1:2379"]}]}`)
	response, err := parseMemberListOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Members) != 1 || len(response.Members[0].ClientURLs) != 1 || response.Members[0].ClientURLs[0] != "https://etcd-1:2379" {
		t.Fatalf("response = %#v", response)
	}
}

func TestParseMemberListOutputSkipsLogPrefix(t *testing.T) {
	output := []byte("warning: retrying member list\n{\"members\":[{\"clientURLs\":[\"http://127.0.0.1:2379\"]}]}\n")
	response, err := parseMemberListOutput(output)
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Members) != 1 || response.Members[0].ClientURLs[0] != "http://127.0.0.1:2379" {
		t.Fatalf("response = %#v", response)
	}
}

func TestServerVersionRejectsUnhealthyCluster(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			_, _ = w.Write([]byte(`{"etcdserver":"3.5.21"}`))
			return
		}
		_, _ = w.Write([]byte(`{"health":false,"reason":"RAFT NO LEADER"}`))
	}))
	defer server.Close()
	endpoint, _ := url.Parse(server.URL)
	port, _ := strconv.Atoi(endpoint.Port())
	if _, err := ServerVersion(context.Background(), model.Connection{Host: endpoint.Hostname(), Port: port, SSLMode: "disable"}); err == nil {
		t.Fatal("expected unhealthy cluster error")
	}
}
