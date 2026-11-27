package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/binary"
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
	"syscall"
	"time"
	"unicode"

	dbetcd "databridge/internal/etcd"
	"databridge/internal/model"
	dbmongo "databridge/internal/mongo"
	"databridge/internal/remotestorage"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Progress struct {
	Stage           string
	EventLevel      string
	EventMessage    string
	Progress        int
	ArtifactName    string
	ArtifactSize    int64
	ArtifactPath    string
	ArtifactCopy    *model.BackupArtifactCopy
	BackupMode      string
	MongoOplogStart *model.MongoOplogTimestamp
	MongoOplogEnd   *model.MongoOplogTimestamp
}

type Reporter func(Progress)

type Engine struct{}

func New() *Engine { return &Engine{} }

const backupWorkDirEnv = "DATABRIDGE_BACKUP_WORK_DIR"

// CleanupStaleWorkDirs removes work directories left behind when the previous
// process was terminated before its deferred cleanup could run. The configured
// work directory is dedicated to DataBridge and the Kubernetes deployment uses
// a single replica with the Recreate strategy, so no backup can still be using
// these directories when a new process starts.
func CleanupStaleWorkDirs() (int, error) {
	baseDir := strings.TrimSpace(os.Getenv(backupWorkDirEnv))
	if baseDir == "" {
		return 0, nil
	}
	entries, err := os.ReadDir(baseDir)
	if errors.Is(err, os.ErrNotExist) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	removed := 0
	var cleanupErr error
	for _, entry := range entries {
		if !isDataBridgeWorkDir(entry.Name()) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(baseDir, entry.Name())); err != nil {
			cleanupErr = errors.Join(cleanupErr, fmt.Errorf("remove stale work directory %q: %w", entry.Name(), err))
			continue
		}
		removed++
	}
	return removed, cleanupErr
}

func isDataBridgeWorkDir(name string) bool {
	for _, prefix := range []string{
		"databridge-backup-",
		"databridge-restore-",
		"databridge-restore-artifact-",
		"databridge-artifact-download-",
	} {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func MkdirTemp(pattern string) (string, error) {
	baseDir := strings.TrimSpace(os.Getenv(backupWorkDirEnv))
	if baseDir != "" {
		if err := os.MkdirAll(baseDir, 0o700); err != nil {
			return "", err
		}
	}
	return os.MkdirTemp(baseDir, pattern)
}

func AvailableWorkBytes() (string, int64, error) {
	baseDir := strings.TrimSpace(os.Getenv(backupWorkDirEnv))
	if baseDir == "" {
		baseDir = os.TempDir()
	}
	if err := os.MkdirAll(baseDir, 0o700); err != nil {
		return baseDir, 0, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(baseDir, &stat); err != nil {
		return baseDir, 0, err
	}
	return baseDir, int64(stat.Bavail) * int64(stat.Bsize), nil
}

func (e *Engine) Run(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch source.Type {
	case "PostgreSQL":
		return e.runPostgres(ctx, source, request, report)
	case "MySQL":
		return e.runMySQL(ctx, source, request, report)
	case "MongoDB":
		return e.runMongo(ctx, source, request, report)
	case "Neo4j":
		return e.runNeo4j(ctx, source, request, report)
	case "etcd":
		return e.runEtcd(ctx, source, request, report)
	default:
		return fmt.Errorf("unsupported backup source type %q", source.Type)
	}
}

func (e *Engine) runEtcd(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	if _, err := toolPath("etcdctl"); err != nil {
		return err
	}
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	tempDir, err := MkdirTemp("databridge-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	snapshot := filepath.Join(tempDir, backupFileName(source, "cluster", "db"))
	endpoints := append([]string(nil), source.Endpoints...)
	seed := dbetcd.SeedEndpoint(source)
	foundSeed := false
	for _, endpoint := range endpoints {
		if endpoint == seed {
			foundSeed = true
			break
		}
	}
	if !foundSeed {
		endpoints = append(endpoints, seed)
	}
	var failures []string
	snapshotCreated := false
	for _, endpoint := range endpoints {
		probeCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
		healthErr := dbetcd.HealthyEndpoint(probeCtx, source, endpoint)
		cancel()
		if healthErr != nil {
			failures = append(failures, endpoint+": "+healthErr.Error())
			report(Progress{Stage: "export", EventLevel: "WARN", EventMessage: "etcd 节点不可用，尝试下一节点：" + endpoint})
			continue
		}
		args, argsErr := dbetcd.CommandArgs(source, endpoint, tempDir)
		if argsErr != nil {
			return argsErr
		}
		report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在从健康节点创建 etcd 集群快照：" + endpoint})
		_ = os.Remove(snapshot)
		if runErr := runCommand(ctx, "etcdctl", append(args, "snapshot", "save", snapshot), append(os.Environ(), "ETCDCTL_API=3"), "export", report); runErr != nil {
			failures = append(failures, endpoint+": "+runErr.Error())
			report(Progress{Stage: "export", EventLevel: "WARN", EventMessage: "etcd 快照失败，切换下一节点：" + endpoint})
			continue
		}
		snapshotCreated = true
		break
	}
	if !snapshotCreated {
		return fmt.Errorf("所有 etcd 节点均无法完成快照: %s", strings.Join(failures, "; "))
	}
	report(Progress{Stage: "verify", EventLevel: "INFO", EventMessage: "正在校验 etcd 快照"})
	if err := runCommand(ctx, "etcdctl", []string{"snapshot", "status", snapshot, "--write-out=json"}, append(os.Environ(), "ETCDCTL_API=3"), "verify", report); err != nil {
		return fmt.Errorf("etcd snapshot status: %w", err)
	}
	archive, err := compressBackup(ctx, tempDir, snapshot, source, "cluster", report)
	if err != nil {
		return err
	}
	return dest.upload(ctx, archive, backupObjectKey(source, "cluster", filepath.Base(archive)), report)
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
	tempDir, err := MkdirTemp("databridge-backup-*")
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
	archive, err := compressBackup(ctx, tempDir, dumpFile, source, request.SourceDatabase, report)
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
	tempDir, err := MkdirTemp("databridge-backup-*")
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
	archive, err := compressBackup(ctx, tempDir, dumpFile, source, request.SourceDatabase, report)
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
	if request.BackupMode == "mongo_oplog" {
		if request.MongoOplogStart != nil {
			return e.runMongoOplogIncremental(ctx, source, request, report)
		}
		return e.runMongoOplogFull(ctx, source, request, report)
	}
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	tempDir, err := MkdirTemp("databridge-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	dumpDir := filepath.Join(tempDir, sanitize(request.SourceDatabase))
	if err := os.MkdirAll(dumpDir, 0o700); err != nil {
		return err
	}
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在导出 MongoDB 数据库"})
	args := mongoDumpArgs(source, "--db", request.SourceDatabase, "--out", tempDir)
	if err := runCommand(ctx, "mongodump", args, os.Environ(), "export", report); err != nil {
		return err
	}
	archive, err := compressBackup(ctx, tempDir, dumpDir, source, request.SourceDatabase, report)
	if err != nil {
		return err
	}
	return dest.upload(ctx, archive, backupObjectKey(source, request.SourceDatabase, filepath.Base(archive)), report)
}

func (e *Engine) runMongoOplogFull(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	baseline, err := dbmongo.IncrementalBackupInfo(ctx, source)
	if err != nil {
		return err
	}
	tempDir, err := MkdirTemp("databridge-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	dumpDir := filepath.Join(tempDir, "replica-set")
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在导出 MongoDB 副本集全量基线"})
	args := mongoDumpArgs(source, "--oplog", "--out", dumpDir)
	if err := runCommand(ctx, "mongodump", args, os.Environ(), "export", report); err != nil {
		return err
	}
	checkpoint := baseline.Newest
	if last, ok, parseErr := mongoOplogLastTimestamp(filepath.Join(dumpDir, "oplog.bson")); parseErr != nil {
		return parseErr
	} else if ok {
		checkpoint = last
	}
	archive, err := compressMongoBackup(ctx, tempDir, dumpDir, source, "replica-set", "full", report)
	if err != nil {
		return err
	}
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "MongoDB 全量基线已生成，记录 oplog 位点", BackupMode: "full", MongoOplogEnd: &checkpoint})
	return dest.upload(ctx, archive, backupObjectKey(source, "replica-set", filepath.Base(archive)), report)
}

func (e *Engine) runMongoOplogIncremental(ctx context.Context, source model.Connection, request model.BackupRequest, report Reporter) error {
	start := *request.MongoOplogStart
	window, err := dbmongo.IncrementalBackupInfo(ctx, source)
	if err != nil {
		return err
	}
	if dbmongo.OplogTimestampCompare(start, window.Oldest) < 0 {
		return fmt.Errorf("oplog 已滚动，无法从位点 %d:%d 继续增量备份", start.Seconds, start.Increment)
	}
	end := window.Newest
	dest, err := newDestination(request)
	if err != nil {
		return err
	}
	tempDir, err := MkdirTemp("databridge-backup-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	dumpDir := filepath.Join(tempDir, "oplog")
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "正在导出 MongoDB oplog 增量"})
	query := fmt.Sprintf(`{"ts":{"$gt":{"$timestamp":{"t":%d,"i":%d}},"$lte":{"$timestamp":{"t":%d,"i":%d}}}}`, start.Seconds, start.Increment, end.Seconds, end.Increment)
	args := mongoDumpArgs(source, "--db", "local", "--collection", "oplog.rs", "--query", query, "--out", dumpDir)
	if err := runCommand(ctx, "mongodump", args, os.Environ(), "export", report); err != nil {
		return err
	}
	// mongorestore recognizes a root-level oplog.bson as replay-only input.
	if err := os.Rename(filepath.Join(dumpDir, "local", "oplog.rs.bson"), filepath.Join(dumpDir, "oplog.bson")); err != nil {
		return fmt.Errorf("prepare oplog increment: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(dumpDir, "local")); err != nil {
		return fmt.Errorf("prepare oplog increment: %w", err)
	}
	archive, err := compressMongoBackup(ctx, tempDir, dumpDir, source, "replica-set", "oplog", report)
	if err != nil {
		return err
	}
	report(Progress{Stage: "export", EventLevel: "INFO", EventMessage: "MongoDB oplog 增量已生成", BackupMode: "incremental", MongoOplogStart: &start, MongoOplogEnd: &end})
	return dest.upload(ctx, archive, backupObjectKey(source, "replica-set", filepath.Base(archive)), report)
}

func mongoDumpArgs(source model.Connection, extra ...string) []string {
	args := dbmongo.MongoToolArgs(source)
	return append(args, extra...)
}

func mongoOplogLastTimestamp(path string) (model.MongoOplogTimestamp, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		return model.MongoOplogTimestamp{}, false, err
	}
	defer file.Close()
	var last model.MongoOplogTimestamp
	found := false
	for {
		var sizeBytes [4]byte
		if _, err := io.ReadFull(file, sizeBytes[:]); errors.Is(err, io.EOF) {
			break
		} else if err != nil {
			return model.MongoOplogTimestamp{}, false, fmt.Errorf("read oplog.bson: %w", err)
		}
		size := int(binary.LittleEndian.Uint32(sizeBytes[:]))
		if size < 5 {
			return model.MongoOplogTimestamp{}, false, fmt.Errorf("read oplog.bson: invalid BSON document length %d", size)
		}
		data := make([]byte, size)
		copy(data, sizeBytes[:])
		if _, err := io.ReadFull(file, data[4:]); err != nil {
			return model.MongoOplogTimestamp{}, false, fmt.Errorf("read oplog.bson: %w", err)
		}
		var entry struct {
			Timestamp bson.Timestamp `bson:"ts"`
		}
		if err := bson.Unmarshal(data, &entry); err != nil {
			return model.MongoOplogTimestamp{}, false, fmt.Errorf("decode oplog.bson: %w", err)
		}
		last = model.MongoOplogTimestamp{Seconds: entry.Timestamp.T, Increment: entry.Timestamp.I}
		found = true
	}
	return last, found, nil
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
	tempDir, err := MkdirTemp("databridge-backup-*")
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
	archive, err := compressBackup(ctx, tempDir, dumpFile, source, database, report)
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
	if len(request.TargetStorages) > 0 {
		items := make([]destination, 0, len(request.TargetStorages))
		for index, storage := range request.TargetStorages {
			target := backupTargetForStorage(request, storage, index)
			dest, err := destinationForStorage(storage, target)
			if err != nil {
				return nil, err
			}
			items = append(items, dest)
		}
		return multiDestination{items: items}, nil
	}
	switch strings.ToLower(strings.TrimSpace(request.DestinationType)) {
	case "local":
		return localDestination{directory: request.Destination}, nil
	case "object":
		if request.Storage == nil {
			return nil, fmt.Errorf("object storage destination is not configured")
		}
		return objectDestination{storage: *request.Storage}, nil
	case "webdav":
		if request.Storage == nil {
			return nil, fmt.Errorf("webdav destination is not configured")
		}
		return webDAVDestination{storage: *request.Storage}, nil
	case "s3":
		return s3Destination{endpoint: request.Destination}, nil
	case "google_drive", "gdrive", "google-drive", "google drive":
		if request.Storage == nil {
			return nil, fmt.Errorf("google drive destination is not configured")
		}
		return googleDriveDestination{storage: *request.Storage}, nil
	default:
		return nil, fmt.Errorf("unsupported destination type %q", request.DestinationType)
	}
}

func backupTargetForStorage(request model.BackupRequest, storage model.RemoteStorage, fallbackOrder int) model.BackupTarget {
	for _, target := range request.BackupTargets {
		if strings.TrimSpace(target.StorageID) == storage.ID {
			return target
		}
	}
	role := "secondary"
	if fallbackOrder == 0 || strings.TrimSpace(request.PrimaryDestinationID) == storage.ID || strings.TrimSpace(request.Destination) == storage.ID {
		role = "primary"
	}
	return model.BackupTarget{StorageID: storage.ID, Role: role, Order: fallbackOrder}
}

func destinationForStorage(storage model.RemoteStorage, target model.BackupTarget) (destination, error) {
	switch storage.Type {
	case "object":
		return objectDestination{storage: storage, target: target}, nil
	case "webdav":
		return webDAVDestination{storage: storage, target: target}, nil
	case "one_drive":
		return oneDriveDestination{storage: storage, target: target}, nil
	case "google_drive":
		return googleDriveDestination{storage: storage, target: target}, nil
	case "local":
		return localDestination{directory: storage.Path, storage: storage, target: target}, nil
	default:
		return nil, fmt.Errorf("storage %q does not support backup writes", storage.Name)
	}
}

type multiDestination struct {
	items []destination
}

func (d multiDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	var failed []string
	success := 0
	for _, item := range d.items {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := item.upload(ctx, filePath, objectKey, report); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			failed = append(failed, err.Error())
			report(Progress{Stage: "upload", EventLevel: "WARN", EventMessage: "备份目标同步失败：" + err.Error()})
			continue
		}
		success++
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if success == 0 && len(failed) > 0 {
		return fmt.Errorf("all backup destinations failed: %s", strings.Join(failed, "; "))
	}
	if len(failed) > 0 {
		report(Progress{Stage: "upload", EventLevel: "WARN", EventMessage: fmt.Sprintf("%d 个备份目标同步失败，已保留成功副本", len(failed))})
	}
	return nil
}

type localDestination struct {
	directory string
	storage   model.RemoteStorage
	target    model.BackupTarget
}

func (d localDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	if err := ctx.Err(); err != nil {
		return err
	}
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
	complete := false
	defer func() {
		_ = output.Close()
		if !complete {
			_ = os.Remove(dst)
		}
	}()
	if _, err := io.Copy(output, contextReader{ctx: ctx, reader: input}); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := output.Close(); err != nil {
		return err
	}
	complete = true
	if info, err := os.Stat(dst); err == nil {
		report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: uploadEventMessage("备份已保存到本地目录", d.storage), ArtifactName: objectKey, ArtifactSize: info.Size(), ArtifactPath: dst, ArtifactCopy: artifactCopyForDestination(d.storage, d.target, objectKey, info.Size(), dst, "")})
	} else {
		report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: uploadEventMessage("备份已保存到本地目录", d.storage), ArtifactName: objectKey, ArtifactPath: dst, ArtifactCopy: artifactCopyForDestination(d.storage, d.target, objectKey, 0, dst, "")})
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type objectDestination struct {
	storage model.RemoteStorage
	target  model.BackupTarget
}

func (d objectDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	size, err := remotestorage.UploadObject(ctx, d.storage, objectKey, filePath)
	if err != nil {
		return err
	}
	report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: uploadEventMessage("备份已上传到对象存储", d.storage), ArtifactName: objectKey, ArtifactSize: size, ArtifactCopy: artifactCopyForDestination(d.storage, d.target, objectKey, size, "", "")})
	return nil
}

type webDAVDestination struct {
	storage model.RemoteStorage
	target  model.BackupTarget
}

type oneDriveDestination struct {
	storage model.RemoteStorage
	target  model.BackupTarget
}

func (d oneDriveDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	remoteKey := path.Join(d.storage.Path, objectKey)
	size, err := remotestorage.UploadOneDrive(ctx, d.storage, remoteKey, filePath)
	if err != nil {
		return err
	}
	report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: uploadEventMessage("备份已上传到 OneDrive", d.storage), ArtifactName: remoteKey, ArtifactSize: size, ArtifactCopy: artifactCopyForDestination(d.storage, d.target, remoteKey, size, "", "")})
	return nil
}

type googleDriveDestination struct {
	storage model.RemoteStorage
	target  model.BackupTarget
}

func (d googleDriveDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	remoteKey := path.Join(d.storage.Path, objectKey)
	size, err := remotestorage.UploadGoogleDrive(ctx, d.storage, remoteKey, filePath)
	if err != nil {
		return err
	}
	report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: uploadEventMessage("备份已上传到 Google Drive", d.storage), ArtifactName: remoteKey, ArtifactSize: size, ArtifactCopy: artifactCopyForDestination(d.storage, d.target, remoteKey, size, "", "")})
	return nil
}

func (d webDAVDestination) upload(ctx context.Context, filePath, objectKey string, report Reporter) error {
	remoteKey := path.Join(d.storage.Path, objectKey)
	size, err := remotestorage.UploadWebDAV(ctx, d.storage, remoteKey, filePath)
	if err != nil {
		return err
	}
	report(Progress{Stage: "upload", EventLevel: "INFO", EventMessage: uploadEventMessage("备份已上传到 WebDAV", d.storage), ArtifactName: remoteKey, ArtifactSize: size, ArtifactCopy: artifactCopyForDestination(d.storage, d.target, remoteKey, size, "", "")})
	return nil
}

func artifactCopyForDestination(storage model.RemoteStorage, target model.BackupTarget, name string, size int64, artifactPath, errText string) *model.BackupArtifactCopy {
	if strings.TrimSpace(storage.ID) == "" {
		return nil
	}
	status := "completed"
	if errText != "" {
		status = "failed"
	}
	role := target.Role
	if role == "" {
		role = "secondary"
	}
	return &model.BackupArtifactCopy{
		StorageID:   storage.ID,
		StorageName: storage.Name,
		Role:        role,
		Order:       target.Order,
		Type:        storage.Type,
		Name:        name,
		Size:        size,
		Path:        artifactPath,
		Status:      status,
		Error:       errText,
	}
}

func uploadEventMessage(prefix string, storage model.RemoteStorage) string {
	if strings.TrimSpace(storage.Name) == "" {
		return prefix
	}
	return prefix + "：" + storage.Name
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
	Strategy        string
	SourceDatabase  string
	MongoReplicaSet bool
	MongoOplogOnly  bool
}

func (e *Engine) Restore(ctx context.Context, target model.Connection, database, archivePath string, options RestoreOptions, report Reporter) error {
	if strings.TrimSpace(archivePath) == "" {
		return fmt.Errorf("backup artifact path is empty")
	}
	tempDir, err := MkdirTemp("databridge-restore-*")
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
	if options.MongoReplicaSet {
		return e.restoreMongoReplicaSet(ctx, target, dumpPath, options, report)
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
	args := append(dbmongo.MongoToolArgs(target),
		"--nsFrom", sourceDatabase+".*",
		"--nsTo", database+".*",
		restoreSourcePath,
	)
	if normalizeRestoreStrategy(options.Strategy) == "recreate" {
		args = append([]string{"--drop"}, args...)
	}
	if err := runCommand(ctx, "mongorestore", args, os.Environ(), "restore", report); err != nil {
		return fmt.Errorf("mongorestore restore: %w", err)
	}
	return nil
}

func (e *Engine) restoreMongoReplicaSet(ctx context.Context, target model.Connection, dumpPath string, options RestoreOptions, report Reporter) error {
	report(Progress{Stage: "restore", EventLevel: "INFO", EventMessage: mongoRestoreProgressMessage(options)})
	args := append(dbmongo.MongoToolArgs(target), "--oplogReplay")
	if !options.MongoOplogOnly {
		args = append(args, "--drop")
	}
	args = append(args, dumpPath)
	if err := runCommand(ctx, "mongorestore", args, os.Environ(), "restore", report); err != nil {
		return fmt.Errorf("mongorestore oplog replay: %w", err)
	}
	return nil
}

func mongoRestoreProgressMessage(options RestoreOptions) string {
	if options.MongoOplogOnly {
		return "正在重放 MongoDB oplog 增量"
	}
	return "正在恢复 MongoDB 副本集全量基线"
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
	postgresMajor, postgresTool, isVersionedPostgresTool := splitPostgresVersionedTool(name)
	for _, candidate := range toolPathCandidates(name) {
		if stat, err := os.Stat(candidate); err == nil && !stat.IsDir() {
			return candidate, nil
		}
	}
	if isVersionedPostgresTool {
		if path, ok := matchingUnversionedPostgresTool(postgresTool, postgresMajor); ok {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s not found in PATH", name)
}

func toolPathCandidates(name string) []string {
	candidates := []string{}
	if postgresMajor, postgresTool, ok := splitPostgresVersionedTool(name); ok {
		major := strconv.Itoa(postgresMajor)
		candidates = append(candidates,
			filepath.Join("/usr/lib/postgresql", major, "bin", postgresTool),
			filepath.Join("/opt/homebrew/opt/postgresql@"+major, "bin", postgresTool),
			filepath.Join("/usr/local/opt/postgresql@"+major, "bin", postgresTool),
			filepath.Join("/Applications/Postgres.app/Contents/Versions", major, "bin", postgresTool),
			filepath.Join("/Library/PostgreSQL", major, "bin", postgresTool),
		)
	}
	candidates = append(candidates,
		filepath.Join("/opt/homebrew/opt/libpq/bin", name),
		filepath.Join("/usr/local/opt/libpq/bin", name),
		filepath.Join("/opt/homebrew/opt/mysql-client/bin", name),
		filepath.Join("/usr/local/opt/mysql-client/bin", name),
		filepath.Join("/opt/homebrew/bin", name),
		filepath.Join("/usr/local/bin", name),
	)
	return candidates
}

func matchingUnversionedPostgresTool(base string, major int) (string, bool) {
	candidates := []string{}
	if path, err := exec.LookPath(base); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range []string{
		filepath.Join("/opt/homebrew/opt/libpq/bin", base),
		filepath.Join("/usr/local/opt/libpq/bin", base),
		filepath.Join("/opt/homebrew/bin", base),
		filepath.Join("/usr/local/bin", base),
	} {
		candidates = append(candidates, candidate)
	}
	seen := map[string]bool{}
	for _, candidate := range candidates {
		if seen[candidate] {
			continue
		}
		seen[candidate] = true
		if stat, err := os.Stat(candidate); err != nil || stat.IsDir() {
			continue
		}
		if postgresToolMajor(candidate) == major {
			return candidate, true
		}
	}
	return "", false
}

func postgresToolMajor(path string) int {
	output, err := exec.Command(path, "--version").Output()
	if err != nil {
		return 0
	}
	return parsePostgresToolMajor(string(output))
}

func parsePostgresToolMajor(output string) int {
	match := regexp.MustCompile(`(?i)postgresql\)\s+(\d+)`).FindStringSubmatch(output)
	if len(match) < 2 {
		return 0
	}
	major, _ := strconv.Atoi(match[1])
	return major
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

func backupCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	// tar may launch gzip. Stop the whole command group so child processes do
	// not keep working or hold output pipes open after the parent is killed.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	return cmd
}

func runCommand(ctx context.Context, name string, args []string, env []string, stage string, report Reporter) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := backupCommand(ctx, path, args...)
	cmd.Env = env
	stdout, stderr := newCommandOutputBuffer(), newCommandOutputBuffer()
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stdout.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: stdout.String()})
	}
	if stderr.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: commandOutputLevel(err), EventMessage: stderr.String()})
	}
	if err != nil {
		output := stderr.String()
		if strings.TrimSpace(output) == "" {
			output = stdout.String()
		}
		return commandExecutionError(name, err, output)
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

func runCommandToWriterEnv(ctx context.Context, name string, args []string, env []string, stage string, stdout io.Writer, report Reporter, stdin ...io.Reader) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := backupCommand(ctx, path, args...)
	cmd.Env = env
	cmd.Stdout = stdout
	if len(stdin) > 0 && stdin[0] != nil {
		cmd.Stdin = stdin[0]
	}
	stderr := newCommandOutputBuffer()
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stderr.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: commandOutputLevel(err), EventMessage: stderr.String()})
	}
	if err != nil {
		return commandExecutionError(name, err, stderr.String())
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

func runCommandToWriter(ctx context.Context, name string, args []string, stage string, stdout io.Writer, report Reporter, stdin ...io.Reader) error {
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := backupCommand(ctx, path, args...)
	cmd.Stdout = stdout
	if len(stdin) > 0 && stdin[0] != nil {
		cmd.Stdin = stdin[0]
	}
	stderr := newCommandOutputBuffer()
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if stderr.Len() > 0 {
		report(Progress{Stage: stage, EventLevel: commandOutputLevel(err), EventMessage: stderr.String()})
	}
	if err != nil {
		return commandExecutionError(name, err, stderr.String())
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: name + " 执行完成"})
	return nil
}

const (
	maxCommandOutputBytes      = 64 * 1024
	maxCommandErrorDetailRunes = 3072
)

func commandOutputLevel(err error) string {
	if err != nil {
		return "ERROR"
	}
	return "INFO"
}

func commandExecutionError(name string, cause error, output string) error {
	detail := commandErrorDetail(output)
	if detail == "" {
		return fmt.Errorf("%s: %w", name, cause)
	}
	return fmt.Errorf("%s: %w\n%s", name, cause, detail)
}

func commandErrorDetail(output string) string {
	output = strings.TrimSpace(output)
	runes := []rune(output)
	if len(runes) <= maxCommandErrorDetailRunes {
		return output
	}
	const marker = "...错误输出前部已截断，仅保留末尾...\n"
	limit := maxCommandErrorDetailRunes - len([]rune(marker))
	return marker + string(runes[len(runes)-limit:])
}

type commandOutputBuffer struct {
	data      []byte
	truncated bool
}

func newCommandOutputBuffer() commandOutputBuffer {
	return commandOutputBuffer{data: make([]byte, 0, maxCommandOutputBytes)}
}

func (b *commandOutputBuffer) Write(p []byte) (int, error) {
	written := len(p)
	if len(p) >= maxCommandOutputBytes {
		b.data = append(b.data[:0], p[len(p)-maxCommandOutputBytes:]...)
		b.truncated = true
		return written, nil
	}
	if overflow := len(b.data) + len(p) - maxCommandOutputBytes; overflow > 0 {
		copy(b.data, b.data[overflow:])
		b.data = b.data[:len(b.data)-overflow]
		b.truncated = true
	}
	b.data = append(b.data, p...)
	return written, nil
}

func (b *commandOutputBuffer) Len() int { return len(b.data) }

func (b *commandOutputBuffer) String() string {
	output := strings.TrimSpace(string(b.data))
	if b.truncated {
		return "...输出过长，仅保留最后 64 KiB...\n" + output
	}
	return output
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

func compressBackup(ctx context.Context, tempDir, dumpFile string, source model.Connection, database string, report Reporter) (string, error) {
	archive := filepath.Join(tempDir, backupFileName(source, database, "tar.gz"))
	report(Progress{Stage: "compress", EventLevel: "INFO", EventMessage: "正在压缩备份文件"})
	if _, err := os.Stat(dumpFile); err != nil {
		return "", err
	}
	args := []string{"-C", filepath.Dir(dumpFile), "-czf", archive, "--", filepath.Base(dumpFile)}
	if err := runCommand(ctx, "tar", args, os.Environ(), "compress", report); err != nil {
		_ = os.Remove(archive)
		return "", fmt.Errorf("tar backup: %w", err)
	}
	info, err := os.Stat(archive)
	if err != nil {
		return "", fmt.Errorf("stat backup archive: %w", err)
	}
	if info.Size() == 0 {
		return "", fmt.Errorf("backup archive is empty")
	}
	return archive, nil
}

func compressMongoBackup(ctx context.Context, tempDir, dumpDir string, source model.Connection, database, kind string, report Reporter) (string, error) {
	archive, err := compressBackup(ctx, tempDir, dumpDir, source, database, report)
	if err != nil {
		return "", err
	}
	name := strings.TrimSuffix(filepath.Base(archive), ".tar.gz") + "_" + kind + ".tar.gz"
	renamed := filepath.Join(filepath.Dir(archive), name)
	if err := os.Rename(archive, renamed); err != nil {
		return "", err
	}
	return renamed, nil
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
