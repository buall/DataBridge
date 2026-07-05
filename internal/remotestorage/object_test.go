package remotestorage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"databridge/internal/model"
)

func TestUploadObjectUsesMCWithoutCredentialsInArguments(t *testing.T) {
	directory := t.TempDir()
	capturePath := filepath.Join(directory, "capture.txt")
	scriptPath := filepath.Join(directory, "mc")
	script := "#!/bin/sh\nprintf '%s\\n' \"$MC_HOST_databridge\" > \"$MC_CAPTURE\"\nprintf '%s\\n' \"$@\" >> \"$MC_CAPTURE\"\n"
	if err := os.WriteFile(scriptPath, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	originalFindMC := findMCExecutable
	findMCExecutable = func() (string, error) { return scriptPath, nil }
	t.Cleanup(func() { findMCExecutable = originalFindMC })
	t.Setenv("MC_CAPTURE", capturePath)

	filePath := filepath.Join(directory, "backup.tar.gz")
	if err := os.WriteFile(filePath, []byte("backup-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	storage := model.RemoteStorage{
		Endpoint: "https://minio.example.com", Bucket: "backups",
		AccessKey: "access", SecretKey: "s@cret/value",
	}
	size, err := UploadObject(context.Background(), storage, "mongo/iot.tar.gz", filePath)
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len("backup-data")) {
		t.Fatalf("uploaded size = %d", size)
	}
	captured, err := os.ReadFile(capturePath)
	if err != nil {
		t.Fatal(err)
	}
	output := string(captured)
	if !strings.Contains(output, "MC_HOST") && !strings.Contains(output, "access:s%40cret%2Fvalue@minio.example.com") {
		t.Fatalf("mc endpoint credentials were not passed through the environment: %s", output)
	}
	if strings.Contains(output[strings.Index(output, "\n")+1:], storage.AccessKey) || strings.Contains(output[strings.Index(output, "\n")+1:], storage.SecretKey) {
		t.Fatalf("credentials leaked into mc arguments: %s", output)
	}
	for _, want := range []string{"--config-dir", "--quiet", "cp", filePath, "databridge/backups/mongo/iot.tar.gz"} {
		if !strings.Contains(output, want) {
			t.Fatalf("mc invocation missing %q: %s", want, output)
		}
	}
}
