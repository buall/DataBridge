package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"databridge/internal/model"
	"databridge/internal/remotestorage"
)

type Progress struct {
	Stage        string
	EventLevel   string
	EventMessage string
	Progress     int
	ArtifactName string
	ArtifactSize int64
}

type Reporter func(Progress)

type Engine struct{}

func New() *Engine { return &Engine{} }

func (e *Engine) Run(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	switch source.Type {
	case "PostgreSQL":
		return e.runPostgres(ctx, source, request, report)
	case "MySQL":
		return e.runMySQL(ctx, source, request, report)
	default:
		return fmt.Errorf("unsupported backup source type %q", source.Type)
	}
}

func (e *Engine) runPostgres(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	for _, name := range []string{"pg_dump"} {
		if _, err := toolPath(name); err != nil {
			return err
		}
	}
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp("", "databridge-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	dumpFile := filepath.Join(tempDir, backupFileName(source, request.SourceDatabase, "sql"))
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在导出 PostgreSQL 数据库"})
	args := []string{"--format=plain", "--no-owner", "--no-privileges", "--verbose", "--file", dumpFile, "--host", source.Host, "--port", fmt.Sprintf("%d", source.Port), "--username", source.Username, "--dbname", request.SourceDatabase}
	if err := runCommand(ctx, "pg_dump", args, postgresEnv(source.Password, source.SSLMode), "export", report); err != nil {
		return err
	}
	archive, err := compressBackup(tempDir, dumpFile, source, request.SourceDatabase, report)
	if err != nil {
		return err
	}
	return dest.upload(ctx, archive, backupObjectKey(source, request.SourceDatabase, filepath.Base(archive)), report)
}

func (e *Engine) runMySQL(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	for _, name := range []string{"mysqldump"} {
		if _, err := toolPath(name); err != nil {
			return err
		}
	}
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp("", "databridge-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	dumpFile := filepath.Join(tempDir, backupFileName(source, request.SourceDatabase, "sql"))
	file, err := os.Create(dumpFile)
	if err != nil {
		return err
	}
	defer file.Close()
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在导出 MySQL 数据库"})
	args := []string{"--single-transaction", "--skip-lock-tables", "--default-character-set=utf8mb4", "--routines", "--triggers", "--events", "--host", source.Host, "--port", fmt.Sprintf("%d", source.Port), "--user", source.Username, "--password=" + source.Password, request.SourceDatabase}
	if err := runCommandToWriter(ctx, "mysqldump", args, "export", file, report); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	archive, err := compressBackup(tempDir, dumpFile, source, request.SourceDatabase, report)
	if err != nil {
		return err
	}
	return dest.upload(ctx, archive, backupObjectKey(source, request.SourceDatabase, filepath.Base(archive)), report)
}

type destination interface {
	upload(ctx context.Context, filePath, objectKey string, report Reporter) error
}

func newDestination(request model.BackupRequest) (destination, error) {
	switch strings.ToLower(strings.TrimSpace(request.DestinationType)) {
	case "local":
		return localDestination{directory: request.Destination}, nil
	case "object":
		if request.Storage == nil {
			return nil, fmt.Errorf("object storage destination is not configured")
		}
		return objectDestination{storage: *request.Storage}, nil
	case "s3":
		return s3Destination{endpoint: request.Destination}, nil
	case "gdrive", "google-drive", "google drive":
		return nil, fmt.Errorf("Google Drive destination is planned but not wired yet")
	default:
		return nil, fmt.Errorf("unsupported destination type %q", request.DestinationType)
	}
}

type localDestination struct {
	directory string
}

func (d localDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	dst := filepath.Join(d.directory, filepath.FromSlash(objectKey))
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	input, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer output.Close()
	if _, err := io.Copy(output, input); err != nil {
		return err
	}
	if info, err := output.Stat(); err == nil {
		report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: "备份已保存到本地目录", ArtifactName: objectKey, ArtifactSize: info.Size()})
	} else {
		report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: "备份已保存到本地目录", ArtifactName: objectKey})
	}
	return nil
}

type objectDestination struct {
	storage model.RemoteStorage
}

func (d objectDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	size, err := remotestorage.UploadObject(ctx, d.storage, objectKey, filePath)
	if err != nil {
		return err
	}
	report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: "备份已上传到对象存储", ArtifactName: objectKey, ArtifactSize: size})
	return nil
}

type s3Destination struct {
	endpoint string
}

func (d s3Destination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	body, err := os.ReadFile(filePath)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, d.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	sum, _ := fileSHA256(filePath)
	req.Header.Set("X-Content-SHA256", sum)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("upload failed: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: "备份已上传到对象存储", ArtifactName: objectKey, ArtifactSize: int64(len(body))})
	return nil
}

func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(sum.Sum(nil)), nil
}

func toolPath(name string) (string, error) {
	if path, err := exec.LookPath(name); err == nil {
		return path, nil
	}
	for _, candidate := range []string{
		filepath.Join("/opt/homebrew/opt/libpq/bin", name),
		filepath.Join("/usr/local/opt/libpq/bin", name),
		filepath.Join("/opt/homebrew/opt/mysql-client/bin", name),
		filepath.Join("/usr/local/opt/mysql-client/bin", name),
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	} {
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}

func postgresEnv(password, sslMode string) []string {
	if sslMode == "" {
		sslMode = "prefer"
	}
	return append(os.Environ(), "PGPASSWORD="+password, "PGSSLMODE="+sslMode)
}

func runCommand(ctx context.Context, name string, args []string, env []string, stage string, report Reporter) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	output, err := cmd.Output()
	if len(output) > 0 {
		report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: strings.TrimSpace(string(output))})
	}
	if err != nil {
		return err
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

func runCommandToWriter(ctx context.Context, name string, args []string, stage string, stdout io.Writer, report Reporter) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = stdout
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if stderr.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: strings.TrimSpace(stderr.String())})
	}
	if err != nil {
		return err
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

func compressBackup(tempDir, dumpFile string, source model.Connection, database string, report Reporter) (string, error) {
	archive := filepath.Join(tempDir, backupFileName(source, database, "tar.gz"))
	report(Progress{Stage: "compress", EventLevel: "INFO", EventMessage: "正在压缩备份文件"})
	output, err := os.Create(archive)
	if err != nil {
		return "", err
	}
	gzipWriter := gzip.NewWriter(output)
	tarWriter := tar.NewWriter(gzipWriter)
	input, err := os.Open(dumpFile)
	if err != nil {
		output.Close()
		return "", err
	}
	info, err := input.Stat()
	if err != nil {
		input.Close()
		output.Close()
		return "", err
	}
	header := &tar.Header{
		Name:    filepath.Base(dumpFile),
		Mode:    0o600,
		Size:    info.Size(),
		ModTime: info.ModTime(),
	}
	if err := tarWriter.WriteHeader(header); err != nil {
		input.Close()
		output.Close()
		return "", err
	}
	if _, err := io.Copy(tarWriter, input); err != nil {
		input.Close()
		output.Close()
		return "", err
	}
	input.Close()
	if err := tarWriter.Close(); err != nil {
		output.Close()
		return "", err
	}
	if err := gzipWriter.Close(); err != nil {
		output.Close()
		return "", err
	}
	if err := output.Close(); err != nil {
		return "", err
	}
	return archive, nil
}

func backupObjectKey(source model.Connection, database, fileName string) string {
	return path.Join(pathSafeSegment(source.Name), pathSafeSegment(database), fileName)
}

func backupFileName(source model.Connection, database, ext string) string {
	return fmt.Sprintf("%s_%s_%s.%s", strings.ToLower(source.Type), sanitize(database), time.Now().Format("20060102_150405"), ext)
}

func sanitize(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "_")
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r):
			return r
		case unicode.IsNumber(r):
			return r
		case r == '_' || r == '-':
			return r
		default:
			return '_'
		}
	}, value)
	value = strings.Trim(value, "_")
	if value == "" {
		return "backup"
	}
	return value
}

func pathSafeSegment(value string) string {
	return sanitize(value)
}
