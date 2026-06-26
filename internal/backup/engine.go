package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"databridge/internal/model"
	dbmongo "databridge/internal/mongo"
	"databridge/internal/remotestorage"
)

type Progress struct {
	Stage        string
	EventLevel   string
	EventMessage string
	Progress     int
	ArtifactName string
	ArtifactSize int64
	ArtifactPath string
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
	case "MongoDB":
		return e.runMongo(ctx, source, request, report)
	case "Neo4j":
		return e.runNeo4j(ctx, source, request, report)
	default:
		return fmt.Errorf("unsupported backup source type %q", source.Type)
	}
}

func (e *Engine) runPostgres(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	pgDumpTool, err := postgresToolForConnection("pg_dump", source)
	if err != nil {
		return err
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
	if err := runCommand(ctx, pgDumpTool, args, postgresEnv(source.Password, source.SSLMode), "export", report); err != nil {
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

func (e *Engine) runMongo(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	for _, name := range []string{"mongodump"} {
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

	dumpDir := filepath.Join(tempDir, sanitize(request.SourceDatabase))
	if err := os.MkdirAll(dumpDir, 0o700); err != nil {
		return err
	}
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在导出 MongoDB 数据库"})
	args := []string{
		"--host", source.Host,
		"--port", fmt.Sprintf("%d", source.Port),
		"--username", source.Username,
		"--password", source.Password,
		"--authenticationDatabase", "admin",
		"--db", request.SourceDatabase,
		"--out", tempDir,
	}
	if err := runCommand(ctx, "mongodump", args, os.Environ(), "export", report); err != nil {
		return err
	}
	archive, err := compressBackup(tempDir, dumpDir, source, request.SourceDatabase, report)
	if err != nil {
		return err
	}
	return dest.upload(ctx, archive, backupObjectKey(source, request.SourceDatabase, filepath.Base(archive)), report)
}

func (e *Engine) runNeo4j(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	archive, cleanup, err := e.ExportLocal(ctx, source, request.SourceDatabase, report)
	if err != nil {
		return err
	}
	defer cleanup()
	return dest.upload(ctx, archive, backupObjectKey(source, request.SourceDatabase, filepath.Base(archive)), report)
}

func (e *Engine) ExportLocal(ctx context.Context, source model.Connection, database string, report Reporter) (string, func(), error) {
	switch source.Type {
	case "Neo4j":
		return e.exportNeo4jLocal(ctx, source, database, report)
	default:
		return "", nil, fmt.Errorf("unsupported local export source type %q", source.Type)
	}
}

func (e *Engine) exportNeo4jLocal(ctx context.Context, source model.Connection, database string, report Reporter) (string, func(), error) {
	for _, name := range []string{"cypher-shell"} {
		if _, err := toolPath(name); err != nil {
			return "", nil, err
		}
	}
	tempDir, err := os.MkdirTemp("", "databridge-backup-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { os.RemoveAll(tempDir) }

	dumpFile := filepath.Join(tempDir, backupFileName(source, database, "cypher"))
	rawFile := dumpFile + ".raw"
	rawOutput, err := os.Create(rawFile)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在使用 APOC 导出 Neo4j Cypher 备份"})
	statement := `CALL apoc.export.cypher.all(null, {format: "plain", stream: true, useOptimizations: {type: "UNWIND_BATCH", unwindBatchSize: 1000}, cypherFormat: "updateAll", ifNotExists: true})
		YIELD cypherStatements
		RETURN cypherStatements`
	args := []string{
		"-a", neo4jBoltURI(source),
		"-u", source.Username,
		"-p", source.Password,
		"-d", database,
		"--format", "plain",
		statement,
	}
	if err := runCommandToWriterEnv(ctx, "cypher-shell", args, neo4jEnv(), "export", rawOutput, report); err != nil {
		rawOutput.Close()
		cleanup()
		return "", nil, fmt.Errorf("cypher-shell export: %w", err)
	}
	if err := rawOutput.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	if err := decodeCypherShellPlainOutput(rawFile, dumpFile); err != nil {
		cleanup()
		return "", nil, err
	}
	if stat, err := os.Stat(dumpFile); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("neo4j cypher export file was not created: %w", err)
	} else if stat.Size() == 0 {
		cleanup()
		return "", nil, fmt.Errorf("neo4j cypher export file is empty")
	}
	archive, err := compressBackup(tempDir, dumpFile, source, database, report)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	return archive, cleanup, nil
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
		report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: "备份已保存到本地目录", ArtifactName: objectKey, ArtifactSize: info.Size(), ArtifactPath: dst})
	} else {
		report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: "备份已保存到本地目录", ArtifactName: objectKey, ArtifactPath: dst})
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

type RestoreOptions struct {
	Strategy       string
	SourceDatabase string
}

func (e *Engine) Restore(ctx context.Context, target model.Connection, database, archivePath string, options RestoreOptions, report Reporter) error {
	if strings.TrimSpace(archivePath) == "" {
		return fmt.Errorf("backup artifact path is empty")
	}
	tempDir, err := os.MkdirTemp("", "databridge-restore-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	report(Progress{Stage: "extract", EventLevel: "INFO", EventMessage: "正在解压备份文件"})
	dumpFile, err := extractBackup(archivePath, tempDir)
	if err != nil {
		return err
	}
	switch target.Type {
	case "PostgreSQL":
		return e.restorePostgres(ctx, target, database, dumpFile, options, report)
	case "MySQL":
		return e.restoreMySQL(ctx, target, database, dumpFile, options, report)
	case "MongoDB":
		return e.restoreMongo(ctx, target, database, dumpFile, options, report)
	case "Neo4j":
		return e.restoreNeo4j(ctx, target, database, dumpFile, options, report)
	default:
		return fmt.Errorf("unsupported restore target type %q", target.Type)
	}
}

func (e *Engine) restorePostgres(ctx context.Context, target model.Connection, database, dumpFile string, options RestoreOptions, report Reporter) error {
	psqlTool, err := postgresToolForConnection("psql", target)
	if err != nil {
		return err
	}
	if normalizeRestoreStrategy(options.Strategy) == "recreate" {
		if err := recreatePostgresDatabase(ctx, target, database, psqlTool, report); err != nil {
			return err
		}
	}
	report(Progress{Stage: "restore", EventLevel: "INFO", EventMessage: "正在恢复 PostgreSQL 数据库"})
	args := []string{"--set", "ON_ERROR_STOP=on", "--host", target.Host, "--port", fmt.Sprintf("%d", target.Port), "--username", target.Username, "--dbname", database, "--file", dumpFile}
	if err := runCommand(ctx, psqlTool, args, postgresEnv(target.Password, target.SSLMode), "restore", report); err != nil {
		return fmt.Errorf("psql restore: %w", err)
	}
	return nil
}

func (e *Engine) restoreMySQL(ctx context.Context, target model.Connection, database, dumpFile string, options RestoreOptions, report Reporter) error {
	for _, name := range []string{"mysql"} {
		if _, err := toolPath(name); err != nil {
			return err
		}
	}
	if normalizeRestoreStrategy(options.Strategy) == "recreate" {
		if err := recreateMySQLDatabase(ctx, target, database, report); err != nil {
			return err
		}
	}
	file, err := os.Open(dumpFile)
	if err != nil {
		return err
	}
	defer file.Close()
	report(Progress{Stage: "restore", EventLevel: "INFO", EventMessage: "正在恢复 MySQL 数据库"})
	args := []string{"--default-character-set=utf8mb4", "--host", target.Host, "--port", fmt.Sprintf("%d", target.Port), "--user", target.Username, "--password=" + target.Password, database}
	if err := runCommandToWriter(ctx, "mysql", args, "restore", io.Discard, report, file); err != nil {
		return fmt.Errorf("mysql restore: %w", err)
	}
	return nil
}

func (e *Engine) restoreMongo(ctx context.Context, target model.Connection, database, dumpPath string, options RestoreOptions, report Reporter) error {
	for _, name := range []string{"mongorestore"} {
		if _, err := toolPath(name); err != nil {
			return err
		}
	}
	if normalizeRestoreStrategy(options.Strategy) == "recreate" {
		if err := recreateMongoDatabase(ctx, target, database, report); err != nil {
			return err
		}
	}
	report(Progress{Stage: "restore", EventLevel: "INFO", EventMessage: "正在恢复 MongoDB 数据库"})
	restoreSourcePath := dumpPath
	if info, err := os.Stat(dumpPath); err == nil && info.IsDir() {
		restoreSourcePath = filepath.Dir(dumpPath)
	}
	sourceDatabase := strings.TrimSpace(options.SourceDatabase)
	if sourceDatabase == "" {
		sourceDatabase = database
	}
	args := []string{
		"--host", target.Host,
		"--port", fmt.Sprintf("%d", target.Port),
		"--username", target.Username,
		"--password", target.Password,
		"--authenticationDatabase", "admin",
		"--nsFrom", sourceDatabase + ".*",
		"--nsTo", database + ".*",
		restoreSourcePath,
	}
	if normalizeRestoreStrategy(options.Strategy) == "recreate" {
		args = append([]string{"--drop"}, args...)
	}
	if err := runCommand(ctx, "mongorestore", args, os.Environ(), "restore", report); err != nil {
		return fmt.Errorf("mongorestore restore: %w", err)
	}
	return nil
}

func (e *Engine) restoreNeo4j(ctx context.Context, target model.Connection, database, dumpFile string, options RestoreOptions, report Reporter) error {
	for _, name := range []string{"cypher-shell"} {
		if _, err := toolPath(name); err != nil {
			return err
		}
	}
	if err := normalizeNeo4jDumpForTarget(dumpFile, target.Version); err != nil {
		return fmt.Errorf("normalize neo4j dump: %w", err)
	}
	if normalizeRestoreStrategy(options.Strategy) == "recreate" {
		if err := clearNeo4jDatabase(ctx, target, database, report); err != nil {
			return err
		}
	}
	report(Progress{Stage: "restore", EventLevel: "INFO", EventMessage: "正在恢复 Neo4j 数据库"})
	args := []string{
		"-a", neo4jBoltURI(target),
		"-u", target.Username,
		"-p", target.Password,
		"-d", database,
		"--format", "plain",
		"-f", dumpFile,
	}
	if err := runCommand(ctx, "cypher-shell", args, neo4jEnv(), "restore", report); err != nil {
		return fmt.Errorf("cypher-shell restore: %w", err)
	}
	return nil
}

func normalizeRestoreStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "recreate", "drop-create", "drop_create":
		return "recreate"
	default:
		return "append"
	}
}

func recreatePostgresDatabase(ctx context.Context, target model.Connection, database, psqlTool string, report Reporter) error {
	maintenanceDatabase := "postgres"
	if strings.EqualFold(strings.TrimSpace(database), maintenanceDatabase) {
		maintenanceDatabase = "template1"
	}
	report(Progress{Stage: "restore", EventLevel: "WARN", EventMessage: "正在重建 PostgreSQL 目标数据库 " + database})
	for _, statement := range []string{
		fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid();", sqlStringLiteral(database)),
		fmt.Sprintf("DROP DATABASE IF EXISTS %s;", postgresIdentifier(database)),
		fmt.Sprintf("CREATE DATABASE %s;", postgresIdentifier(database)),
	} {
		args := []string{"--set", "ON_ERROR_STOP=on", "--host", target.Host, "--port", fmt.Sprintf("%d", target.Port), "--username", target.Username, "--dbname", maintenanceDatabase, "--command", statement}
		if err := runCommand(ctx, psqlTool, args, postgresEnv(target.Password, target.SSLMode), "restore", report); err != nil {
			return fmt.Errorf("recreate postgres database: %w", err)
		}
	}
	return nil
}

func recreateMySQLDatabase(ctx context.Context, target model.Connection, database string, report Reporter) error {
	report(Progress{Stage: "restore", EventLevel: "WARN", EventMessage: "正在重建 MySQL 目标数据库 " + database})
	statement := fmt.Sprintf("DROP DATABASE IF EXISTS %s; CREATE DATABASE %s CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;", mysqlIdentifier(database), mysqlIdentifier(database))
	args := []string{"--default-character-set=utf8mb4", "--host", target.Host, "--port", fmt.Sprintf("%d", target.Port), "--user", target.Username, "--password=" + target.Password, "--execute", statement}
	if err := runCommandToWriter(ctx, "mysql", args, "restore", io.Discard, report); err != nil {
		return fmt.Errorf("recreate mysql database: %w", err)
	}
	return nil
}

func recreateMongoDatabase(ctx context.Context, target model.Connection, database string, report Reporter) error {
	report(Progress{Stage: "restore", EventLevel: "WARN", EventMessage: "正在重建 MongoDB 目标数据库 " + database})
	if err := dbmongo.DropDatabase(ctx, target, database); err != nil {
		return fmt.Errorf("recreate mongo database: %w", err)
	}
	return nil
}

func clearNeo4jDatabase(ctx context.Context, target model.Connection, database string, report Reporter) error {
	report(Progress{Stage: "restore", EventLevel: "WARN", EventMessage: "正在清空 Neo4j 目标数据库 " + database})
	args := []string{
		"-a", neo4jBoltURI(target),
		"-u", target.Username,
		"-p", target.Password,
		"-d", database,
		"--format", "plain",
		"MATCH (n) DETACH DELETE n",
	}
	if err := runCommand(ctx, "cypher-shell", args, neo4jEnv(), "restore", report); err != nil {
		return fmt.Errorf("clear neo4j database: %w", err)
	}
	return nil
}

func postgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func mysqlIdentifier(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func sqlStringLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
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
	if postgresMajor, postgresTool, ok := splitPostgresVersionedTool(name); ok {
		candidate := filepath.Join("/usr/lib/postgresql", strconv.Itoa(postgresMajor), "bin", postgresTool)
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
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

func ToolPathForPrecheck(name string) (string, error) {
	return toolPath(name)
}

func postgresToolForConnection(base string, connection model.Connection) (string, error) {
	major, ok := parseMajorVersion(connection.Version)
	if !ok {
		return "", fmt.Errorf("unsupported postgres version %q", connection.Version)
	}
	name := base
	if major >= 13 && major <= 18 {
		name = fmt.Sprintf("%s-%d", base, major)
	}
	if _, err := toolPath(name); err != nil {
		return "", err
	}
	return name, nil
}

func splitPostgresVersionedTool(name string) (int, string, bool) {
	for _, base := range []string{"psql", "pg_dump", "pg_restore"} {
		prefix := base + "-"
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		major, err := strconv.Atoi(strings.TrimPrefix(name, prefix))
		if err != nil {
			return 0, "", false
		}
		return major, base, true
	}
	return 0, "", false
}

func parseMajorVersion(version string) (int, bool) {
	version = strings.TrimSpace(version)
	if version == "" {
		return 0, false
	}
	segment := version
	if index := strings.Index(segment, "."); index >= 0 {
		segment = segment[:index]
	}
	value, err := strconv.Atoi(segment)
	if err != nil {
		return 0, false
	}
	return value, true
}

func postgresEnv(password, sslMode string) []string {
	if sslMode == "" {
		sslMode = "prefer"
	}
	return append(os.Environ(), "PGPASSWORD="+password, "PGSSLMODE="+sslMode)
}

func neo4jEnv() []string {
	return append(os.Environ(), "LANG=C.UTF-8", "LC_ALL=C.UTF-8")
}

func neo4jBoltURI(source model.Connection) string {
	port := source.Port
	if port == 0 {
		port = 7687
	}
	scheme := "bolt"
	switch strings.ToLower(strings.TrimSpace(source.SSLMode)) {
	case "require":
		scheme = "bolt+ssc"
	case "verify-full":
		scheme = "bolt+s"
	}
	return fmt.Sprintf("%s://%s:%d", scheme, source.Host, port)
}

func runCommand(ctx context.Context, name string, args []string, env []string, stage string, report Reporter) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if stdout.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: strings.TrimSpace(stdout.String())})
	}
	if stderr.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: strings.TrimSpace(stderr.String())})
	}
	if err != nil {
		return err
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

func runCommandToWriterEnv(ctx context.Context, name string, args []string, env []string, stage string, stdout io.Writer, report Reporter, stdin ...io.Reader) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	cmd.Stdout = stdout
	if len(stdin) > 0 && stdin[0] != nil {
		cmd.Stdin = stdin[0]
	}
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

func runCommandToWriter(ctx context.Context, name string, args []string, stage string, stdout io.Writer, report Reporter, stdin ...io.Reader) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Stdout = stdout
	if len(stdin) > 0 && stdin[0] != nil {
		cmd.Stdin = stdin[0]
	}
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

func decodeCypherShellPlainOutput(rawPath, outputPath string) error {
	data, err := os.ReadFile(rawPath)
	if err != nil {
		return err
	}
	value := string(data)
	firstLineEnd := strings.IndexByte(value, '\n')
	if firstLineEnd < 0 {
		return fmt.Errorf("cypher-shell output did not include a result body")
	}
	body := strings.TrimRight(value[firstLineEnd+1:], "\r\n")
	if strings.HasPrefix(body, "\"") && strings.HasSuffix(body, "\"") && len(body) >= 2 {
		body = body[1 : len(body)-1]
	}
	body = strings.ReplaceAll(body, `\"`, `"`)
	body = strings.ReplaceAll(body, `\\`, `\`)
	return os.WriteFile(outputPath, []byte(body), 0o600)
}

var neo4jLegacyUniqueConstraintPattern = regexp.MustCompile(`^CREATE CONSTRAINT( IF NOT EXISTS)? ON \(([^)]+)\) ASSERT \(([^)]+)\) IS UNIQUE;?$`)
var neo4jLegacyDropUniqueConstraintPattern = regexp.MustCompile(`^DROP CONSTRAINT ON \(([^)]+)\) ASSERT \(([^)]+)\) IS UNIQUE;?$`)

func normalizeNeo4jDumpForTarget(dumpFile, targetVersion string) error {
	if !neo4jNeedsConstraintSyntaxUpgrade(targetVersion) {
		return nil
	}
	data, err := os.ReadFile(dumpFile)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	changed := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		matches := neo4jLegacyUniqueConstraintPattern.FindStringSubmatch(trimmed)
		if matches != nil {
			replacement := "CREATE CONSTRAINT"
			if matches[1] != "" {
				replacement += matches[1]
			}
			replacement += " FOR (" + matches[2] + ") REQUIRE (" + matches[3] + ") IS UNIQUE;"
			lines[i] = strings.Replace(line, trimmed, replacement, 1)
			changed = true
			continue
		}
		matches = neo4jLegacyDropUniqueConstraintPattern.FindStringSubmatch(trimmed)
		if matches != nil {
			replacement := "DROP CONSTRAINT " + neo4jLegacyConstraintName(matches[1], matches[2]) + " IF EXISTS;"
			lines[i] = strings.Replace(line, trimmed, replacement, 1)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return os.WriteFile(dumpFile, []byte(strings.Join(lines, "\n")), 0o600)
}

func neo4jNeedsConstraintSyntaxUpgrade(version string) bool {
	major, ok := parseMajorVersion(version)
	return ok && major >= 5
}

func neo4jLegacyConstraintName(variable, propertyExpr string) string {
	label := variable
	if parts := strings.SplitN(variable, ":", 2); len(parts) == 2 {
		label = parts[1]
	}
	label = strings.Trim(label, "` ")
	property := propertyExpr
	if parts := strings.SplitN(propertyExpr, ".", 2); len(parts) == 2 {
		property = parts[1]
	}
	property = strings.Trim(property, "`() ")
	return fmt.Sprintf("constraint_%s_%s", sanitize(label), sanitize(property))
}

func extractBackup(archivePath, tempDir string) (string, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return "", err
	}
	defer file.Close()
	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return "", err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	var dumpFile string
	var dumpDir string
	for {
		header, err := tarReader.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return "", err
		}
		if header.Typeflag != tar.TypeReg {
			continue
		}
		name := filepath.Base(header.Name)
		if name == "." || name == string(filepath.Separator) {
			continue
		}
		target := filepath.Join(tempDir, header.Name)
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return "", err
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(output, tarReader); err != nil {
			output.Close()
			return "", err
		}
		if err := output.Close(); err != nil {
			return "", err
		}
		lowerName := strings.ToLower(name)
		if strings.HasSuffix(lowerName, ".sql") || strings.HasSuffix(lowerName, ".cypher") {
			dumpFile = target
		}
		if dumpDir == "" {
			parts := strings.Split(filepath.ToSlash(header.Name), "/")
			if len(parts) > 1 && parts[0] != "" {
				dumpDir = filepath.Join(tempDir, parts[0])
			}
		}
	}
	if dumpFile == "" {
		if dumpDir != "" {
			return dumpDir, nil
		}
		return "", fmt.Errorf("backup archive does not contain a recognizable dump")
	}
	return dumpFile, nil
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
	info, err := os.Stat(dumpFile)
	if err != nil {
		output.Close()
		return "", err
	}
	if info.IsDir() {
		if err := addDirectoryToTar(tarWriter, dumpFile, filepath.Dir(dumpFile)); err != nil {
			output.Close()
			return "", err
		}
	} else {
		input, err := os.Open(dumpFile)
		if err != nil {
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
	}
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

func addDirectoryToTar(tarWriter *tar.Writer, root, base string) error {
	return filepath.Walk(root, func(current string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(base, current)
		if err != nil {
			return err
		}
		header := &tar.Header{
			Name:    filepath.ToSlash(relative),
			Mode:    0o600,
			Size:    info.Size(),
			ModTime: info.ModTime(),
		}
		if err := tarWriter.WriteHeader(header); err != nil {
			return err
		}
		input, err := os.Open(current)
		if err != nil {
			return err
		}
		defer input.Close()
		if _, err := io.Copy(tarWriter, input); err != nil {
			return err
		}
		return nil
	})
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
