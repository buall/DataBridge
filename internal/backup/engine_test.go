package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"databridge/internal/model"
)

func TestNormalizeNeo4jDumpForTargetUpgradesLegacyUniqueConstraint(t *testing.T) {
	dumpFile := filepath.Join(t.TempDir(), "dump.cypher")
	input := strings.Join([]string{
		"CREATE CONSTRAINT IF NOT EXISTS ON (n:`CompatUser`) ASSERT (n.`email`) IS UNIQUE;",
		"DROP CONSTRAINT ON (n:`CompatUser`) ASSERT (n.`email`) IS UNIQUE;",
	}, "\n")
	if err := os.WriteFile(dumpFile, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := normalizeNeo4jDumpForTarget(dumpFile, "5.26.0"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatal(err)
	}
	output := string(data)
	for _, want := range []string{
		"CREATE CONSTRAINT IF NOT EXISTS FOR (n:`CompatUser`) REQUIRE (n.`email`) IS UNIQUE;",
		"DROP CONSTRAINT constraint_compatuser_email IF EXISTS;",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("normalized dump missing %q\n%s", want, output)
		}
	}
}

func TestNormalizeNeo4jDumpForTargetLeavesNeo4j4Unchanged(t *testing.T) {
	dumpFile := filepath.Join(t.TempDir(), "dump.cypher")
	input := "CREATE CONSTRAINT IF NOT EXISTS ON (n:CompatUser) ASSERT (n.email) IS UNIQUE;\n"
	if err := os.WriteFile(dumpFile, []byte(input), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := normalizeNeo4jDumpForTarget(dumpFile, "4.4.42"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != input {
		t.Fatalf("neo4j 4 dump changed unexpectedly\n%s", string(data))
	}
}

func TestToolPathCandidatesIncludeVersionedPostgresInstallDirectories(t *testing.T) {
	candidates := strings.Join(toolPathCandidates("pg_dump-18"), "\n")
	for _, want := range []string{
		"/usr/lib/postgresql/18/bin/pg_dump",
		"/opt/homebrew/opt/postgresql@18/bin/pg_dump",
		"/usr/local/opt/postgresql@18/bin/pg_dump",
		"/Applications/Postgres.app/Contents/Versions/18/bin/pg_dump",
		"/Library/PostgreSQL/18/bin/pg_dump",
	} {
		if !strings.Contains(candidates, want) {
			t.Fatalf("missing candidate %q\n%s", want, candidates)
		}
	}
}

func TestParsePostgresToolMajor(t *testing.T) {
	tests := map[string]int{
		"pg_dump (PostgreSQL) 18.4":          18,
		"psql (PostgreSQL) 14.13 (Homebrew)": 14,
		"pg_restore (PostgreSQL) 17beta1":    17,
		"not postgres":                       0,
	}
	for input, want := range tests {
		if got := parsePostgresToolMajor(input); got != want {
			t.Fatalf("parsePostgresToolMajor(%q) = %d, want %d", input, got, want)
		}
	}
}

func TestMkdirTempUsesConfiguredBackupWorkDir(t *testing.T) {
	workDir := filepath.Join(t.TempDir(), "backup-work")
	t.Setenv(backupWorkDirEnv, workDir)

	tempDir, err := MkdirTemp("databridge-backup-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	relative, err := filepath.Rel(workDir, tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) {
		t.Fatalf("temp dir %q was not created under configured work dir %q", tempDir, workDir)
	}
}

func TestCleanupStaleWorkDirsRemovesOnlyDataBridgeWorkDirectories(t *testing.T) {
	workDir := t.TempDir()
	t.Setenv(backupWorkDirEnv, workDir)

	for _, name := range []string{
		"databridge-backup-123",
		"databridge-restore-456",
		"databridge-restore-artifact-789",
		"databridge-artifact-download-012",
		"keep-me",
	} {
		if err := os.Mkdir(filepath.Join(workDir, name), 0o700); err != nil {
			t.Fatal(err)
		}
	}

	removed, err := CleanupStaleWorkDirs()
	if err != nil {
		t.Fatal(err)
	}
	if removed != 4 {
		t.Fatalf("removed = %d, want 4", removed)
	}
	if _, err := os.Stat(filepath.Join(workDir, "keep-me")); err != nil {
		t.Fatalf("unrelated directory was removed: %v", err)
	}
	for _, name := range []string{"databridge-backup-123", "databridge-restore-456", "databridge-restore-artifact-789", "databridge-artifact-download-012"} {
		if _, err := os.Stat(filepath.Join(workDir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("stale work directory %q still exists: %v", name, err)
		}
	}
}

func TestCleanupStaleWorkDirsDoesNothingWithoutConfiguredDirectory(t *testing.T) {
	t.Setenv(backupWorkDirEnv, "")
	removed, err := CleanupStaleWorkDirs()
	if err != nil || removed != 0 {
		t.Fatalf("CleanupStaleWorkDirs() = (%d, %v), want (0, nil)", removed, err)
	}
}

func TestCommandOutputBufferKeepsBoundedTail(t *testing.T) {
	buffer := newCommandOutputBuffer()
	prefix := strings.Repeat("a", maxCommandOutputBytes)
	if _, err := buffer.Write([]byte(prefix)); err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.Write([]byte("important error at the end")); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() != maxCommandOutputBytes {
		t.Fatalf("buffer length = %d, want %d", buffer.Len(), maxCommandOutputBytes)
	}
	output := buffer.String()
	if !strings.Contains(output, "仅保留最后 64 KiB") || !strings.HasSuffix(output, "important error at the end") {
		t.Fatalf("unexpected bounded output: %q", output)
	}
}

func TestCommandOutputBufferHandlesWritesLargerThanLimit(t *testing.T) {
	buffer := newCommandOutputBuffer()
	large := strings.Repeat("x", maxCommandOutputBytes) + "tail"
	if _, err := buffer.Write([]byte(large)); err != nil {
		t.Fatal(err)
	}
	if buffer.Len() != maxCommandOutputBytes || !strings.HasSuffix(buffer.String(), "tail") {
		t.Fatalf("large write was not reduced to bounded tail")
	}
}

func TestRunCommandReturnsStderrAndReportsFailure(t *testing.T) {
	binDir := t.TempDir()
	tool := filepath.Join(binDir, "failing-backup-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\necho 'specific database failure' >&2\nexit 1\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	var events []Progress
	err := runCommand(context.Background(), "failing-backup-tool", nil, os.Environ(), "export", func(progress Progress) {
		events = append(events, progress)
	})
	if err == nil || !strings.Contains(err.Error(), "specific database failure") {
		t.Fatalf("command error should include stderr, got %v", err)
	}
	if len(events) != 1 || events[0].EventLevel != "ERROR" || !strings.Contains(events[0].EventMessage, "specific database failure") {
		t.Fatalf("failure output should be reported as ERROR: %#v", events)
	}
}

func TestCommandExecutionErrorKeepsFailureTail(t *testing.T) {
	output := "command started\n" + strings.Repeat("progress\n", maxCommandErrorDetailRunes) + "root cause at the end"
	err := commandExecutionError("mongodump", errors.New("exit status 1"), output)
	if !strings.Contains(err.Error(), "错误输出前部已截断") || !strings.HasSuffix(err.Error(), "root cause at the end") {
		t.Fatalf("command error did not preserve the failure tail: %q", err)
	}
}

func TestCompressBackupWithSystemTarIncludesNestedFiles(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "dump")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		filepath.Join(root, "collection.bson"):         "root data",
		filepath.Join(root, "nested", "metadata.json"): "nested data",
	}
	for name, content := range files {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	archive, err := compressBackup(context.Background(), base, root, model.Connection{Type: "MongoDB", Name: "source"}, "database", func(Progress) {})
	if err != nil {
		t.Fatal(err)
	}
	archiveFile, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer archiveFile.Close()
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		t.Fatal(err)
	}
	defer gzipReader.Close()

	got := map[string]string{}
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatal(err)
		}
		got[header.Name] = string(data)
	}
	if got["dump/collection.bson"] != "root data" || got["dump/nested/metadata.json"] != "nested data" {
		t.Fatalf("unexpected archive contents: %#v", got)
	}
}
