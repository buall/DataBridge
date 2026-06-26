package migrate

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"databridge/internal/model"
	"databridge/internal/postgres"
	"github.com/jackc/pgx/v5"
)

func (e *Engine) RunNativePostgres(
	ctx context.Context,
	source model.Connection,
	target model.Connection,
	request model.MigrationRequest,
	report Reporter,
) error {
	sourceMajor, err := postgresMajorVersion(source.Version)
	if err != nil {
		return fmt.Errorf("detect source postgres major version: %w", err)
	}
	targetMajor, err := postgresMajorVersion(target.Version)
	if err != nil {
		return fmt.Errorf("detect target postgres major version: %w", err)
	}
	tools := postgresNativeTools{
		psql:      postgresToolName("psql", targetMajor),
		pgDump:    postgresToolName("pg_dump", sourceMajor),
		pgRestore: postgresToolName("pg_restore", targetMajor),
	}
	for _, name := range []string{tools.psql, tools.pgDump, tools.pgRestore} {
		if _, err := postgresToolPath(name); err != nil {
			return err
		}
	}

	sourceConn, err := postgres.Connect(ctx, source, request.SourceDatabase)
	if err != nil {
		return fmt.Errorf("connect source: %w", err)
	}
	defer sourceConn.Close(ctx)
	targetConn, err := postgres.Connect(ctx, target, request.TargetDatabase)
	if err != nil {
		return fmt.Errorf("connect target: %w", err)
	}
	defer targetConn.Close(ctx)

	tables, err := loadTables(ctx, sourceConn, request.Tables)
	if err != nil {
		return fmt.Errorf("inspect source tables: %w", err)
	}
	if len(tables) == 0 {
		return errors.New("no source tables selected")
	}
	var totalRows int64
	for i := range tables {
		count, countErr := countRows(ctx, sourceConn, tables[i].Schema, tables[i].Name)
		if countErr != nil {
			return fmt.Errorf("count %s.%s: %w", tables[i].Schema, tables[i].Name, countErr)
		}
		tables[i].Rows = count
		totalRows += count
	}
	dumpAll, err := isFullDatabaseSelection(ctx, sourceConn, request.Tables)
	if err != nil {
		return fmt.Errorf("inspect full database selection: %w", err)
	}
	scope := "按所选表"
	if dumpAll {
		scope = "整库"
	}
	report(Progress{Stage: "precheck", RowsTotal: totalRows, EventLevel: "INFO", EventMessage: fmt.Sprintf("原生工具预检查完成，共 %d 张表、%d 行，导出范围：%s", len(tables), totalRows, scope)})

	if err := createSchemas(ctx, targetConn, uniqueSchemas(tables)); err != nil {
		return fmt.Errorf("create target schemas: %w", err)
	}
	if err := prepareNativeTarget(ctx, target, request.TargetDatabase, tables, request.ConflictStrategy, tools.psql, report); err != nil {
		return fmt.Errorf("prepare native target: %w", err)
	}
	if err := syncExtensions(ctx, sourceConn, targetConn, report); err != nil {
		return fmt.Errorf("sync extensions: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "databridge-pg-*")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	dumpPath := filepath.Join(tempDir, "dumpdir")
	listPath := filepath.Join(tempDir, "restore.list")
	jobs := nativeToolJobs(len(tables))

	report(Progress{Stage: "dump", TablesDone: 0, RowsTotal: totalRows, EventLevel: "INFO", EventMessage: fmt.Sprintf("正在使用 %s directory 格式并发导出源端数据，范围=%s，jobs=%d", tools.pgDump, scope, jobs)})
	if err := runLoggedCommand(ctx, tools.pgDump, pgDumpArgs(source, request.SourceDatabase, tables, dumpPath, jobs, dumpAll), source.Password, source.SSLMode, "dump", nil, dumpHeartbeat(dumpPath), report); err != nil {
		return fmt.Errorf("%s: %w", tools.pgDump, err)
	}
	if err := writeFilteredRestoreList(ctx, tools.pgRestore, dumpPath, listPath, target.Password, target.SSLMode); err != nil {
		return fmt.Errorf("prepare pg_restore list: %w", err)
	}

	report(Progress{Stage: "restore", TablesDone: 0, RowsTotal: totalRows, EventLevel: "INFO", EventMessage: fmt.Sprintf("正在使用 %s 并发恢复到目标端，jobs=%d", tools.pgRestore, jobs)})
	if err := runLoggedCommand(ctx, tools.pgRestore, pgRestoreArgs(target, request.TargetDatabase, dumpPath, listPath, jobs), target.Password, target.SSLMode, "restore", nil, commandHeartbeat(tools.pgRestore), report); err != nil {
		return fmt.Errorf("%s: %w", tools.pgRestore, err)
	}

	var copiedTotal int64
	for index, table := range tables {
		count, countErr := countRows(ctx, targetConn, table.Schema, table.Name)
		if countErr != nil {
			return fmt.Errorf("validate %s.%s: %w", table.Schema, table.Name, countErr)
		}
		copiedTotal += count
		result := model.TableResult{
			Table:      table.Schema + "." + table.Name,
			SourceRows: table.Rows,
			TargetRows: count,
			CopiedRows: count,
			Valid:      count == table.Rows,
		}
		report(Progress{
			Stage: "validation", TablesDone: index + 1,
			RowsTotal: totalRows, RowsCopied: copiedTotal, Result: &result,
		})
	}
	report(Progress{Stage: "completed", TablesDone: len(tables), RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: "原生工具迁移完成，行数校验已执行"})
	return nil
}

func (e *Engine) RunNative(
	ctx context.Context,
	source model.Connection,
	target model.Connection,
	request model.MigrationRequest,
	report Reporter,
) error {
	switch source.Type {
	case "PostgreSQL":
		return e.RunNativePostgres(ctx, source, target, request, report)
	case "MySQL":
		return e.RunNativeMySQL(ctx, source, target, request, report)
	case "MongoDB":
		return e.RunNativeMongo(ctx, source, target, request, report)
	case "Neo4j":
		return e.RunNativeNeo4j(ctx, source, target, request, report)
	default:
		return fmt.Errorf("unsupported native migration type %q", source.Type)
	}
}

func pgDumpArgs(connection model.Connection, database string, tables []tableDefinition, dumpPath string, jobs int, dumpAll bool) []string {
	args := []string{
		"--format=directory",
		"--no-owner",
		"--no-privileges",
		"--verbose",
		"--compress=0",
		"--jobs", strconv.Itoa(jobs),
		"--file", dumpPath,
		"--host", connection.Host,
		"--port", fmt.Sprintf("%d", connection.Port),
		"--username", connection.Username,
		"--dbname", database,
	}
	if !dumpAll {
		for _, pattern := range dumpPatterns(tables) {
			args = append(args, "--table", pattern)
		}
	}
	return args
}

func pgRestoreArgs(connection model.Connection, database, dumpPath, listPath string, jobs int) []string {
	return []string{
		"--verbose",
		"--exit-on-error",
		"--no-owner",
		"--no-privileges",
		"--use-list", listPath,
		"--jobs", strconv.Itoa(jobs),
		"--host", connection.Host,
		"--port", fmt.Sprintf("%d", connection.Port),
		"--username", connection.Username,
		"--dbname", database,
		dumpPath,
	}
}

func prepareNativeTarget(ctx context.Context, connection model.Connection, database string, tables []tableDefinition, strategy string, psqlTool string, report Reporter) error {
	if strategy == "" {
		strategy = "backup"
	}
	var statements []string
	for _, table := range tables {
		qualified := postgres.QuoteIdent(table.Schema) + "." + postgres.QuoteIdent(table.Name)
		regclass := quoteLiteral(qualified)
		switch strategy {
		case "backup":
			backup := table.Name + "_databridge_backup_" + time.Now().Format("20060102_150405")
			statements = append(statements, fmt.Sprintf(
				`DO $$ DECLARE index_record record; sequence_record record; rename_suffix text := '_databridge_' || to_char(clock_timestamp(), 'HH24MISSMS'); BEGIN
					IF to_regclass(%s) IS NOT NULL THEN
						EXECUTE %s;
						FOR index_record IN SELECT indexname FROM pg_indexes WHERE schemaname = %s AND tablename = %s LOOP
							EXECUTE format('ALTER INDEX %%I.%%I RENAME TO %%I', %s, index_record.indexname, left(index_record.indexname, 30) || '_' || substr(md5(index_record.indexname), 1, 8) || rename_suffix);
						END LOOP;
						FOR sequence_record IN
							SELECT seq_ns.nspname AS schema_name, seq.relname AS sequence_name
							FROM pg_class seq
							JOIN pg_namespace seq_ns ON seq_ns.oid = seq.relnamespace
							JOIN pg_depend dep ON dep.objid = seq.oid AND dep.deptype = 'a'
							JOIN pg_class tbl ON tbl.oid = dep.refobjid
							JOIN pg_namespace tbl_ns ON tbl_ns.oid = tbl.relnamespace
							WHERE seq.relkind = 'S' AND tbl_ns.nspname = %s AND tbl.relname = %s
						LOOP
							EXECUTE format('ALTER SEQUENCE %%I.%%I RENAME TO %%I', sequence_record.schema_name, sequence_record.sequence_name, left(sequence_record.sequence_name, 30) || '_' || substr(md5(sequence_record.sequence_name), 1, 8) || rename_suffix);
						END LOOP;
					END IF;
				END $$;`,
				regclass,
				quoteLiteral(fmt.Sprintf("ALTER TABLE %s RENAME TO %s", qualified, postgres.QuoteIdent(backup))),
				quoteLiteral(table.Schema),
				quoteLiteral(backup),
				quoteLiteral(table.Schema),
				quoteLiteral(table.Schema),
				quoteLiteral(backup),
			))
		case "overwrite":
			statements = append(statements, "DROP TABLE IF EXISTS "+qualified+" CASCADE;")
		case "skip":
			statements = append(statements, fmt.Sprintf(
				"DO $$ BEGIN IF to_regclass(%s) IS NOT NULL THEN RAISE EXCEPTION 'target table %s exists and strategy is skip'; END IF; END $$;",
				regclass,
				strings.ReplaceAll(table.Schema+"."+table.Name, "'", "''"),
			))
		default:
			return fmt.Errorf("unsupported conflict strategy %q", strategy)
		}
	}
	if len(statements) == 0 {
		return nil
	}
	report(Progress{Stage: "target-prep", EventLevel: "INFO", EventMessage: "正在使用 psql 预处理目标端同名对象"})
	script := strings.Join(statements, "\n") + "\n"
	if err := runLoggedCommand(ctx, psqlTool, psqlArgs(connection, database), connection.Password, connection.SSLMode, "target-prep", strings.NewReader(script), nil, report); err != nil {
		return err
	}
	return nil
}

type postgresNativeTools struct {
	psql      string
	pgDump    string
	pgRestore string
}

func syncExtensions(ctx context.Context, source, target *pgx.Conn, report Reporter) error {
	rows, err := source.Query(ctx, `
		SELECT e.extname, n.nspname
		FROM pg_extension e
		JOIN pg_namespace n ON n.oid = e.extnamespace
		WHERE e.extname <> 'plpgsql'
		ORDER BY e.extname`)
	if err != nil {
		return err
	}
	defer rows.Close()
	type extensionInfo struct {
		Name   string
		Schema string
	}
	var extensions []extensionInfo
	for rows.Next() {
		var item extensionInfo
		if err := rows.Scan(&item.Name, &item.Schema); err != nil {
			return err
		}
		extensions = append(extensions, item)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(extensions) == 0 {
		return nil
	}
	names := make([]string, 0, len(extensions))
	for _, item := range extensions {
		names = append(names, item.Name)
	}
	report(Progress{Stage: "target-prep", EventLevel: "INFO", EventMessage: "正在同步目标端扩展：" + strings.Join(names, ", ")})
	for _, item := range extensions {
		if _, err := target.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+postgres.QuoteIdent(item.Schema)); err != nil {
			return fmt.Errorf("create extension schema %s: %w", item.Schema, err)
		}
		statement := fmt.Sprintf(
			"CREATE EXTENSION IF NOT EXISTS %s WITH SCHEMA %s",
			postgres.QuoteIdent(item.Name),
			postgres.QuoteIdent(item.Schema),
		)
		if _, err := target.Exec(ctx, statement); err != nil {
			return fmt.Errorf("create extension %s: %w", item.Name, err)
		}
	}
	return nil
}

func psqlArgs(connection model.Connection, database string) []string {
	return []string{
		"--no-psqlrc",
		"--quiet",
		"--set", "ON_ERROR_STOP=1",
		"--host", connection.Host,
		"--port", fmt.Sprintf("%d", connection.Port),
		"--username", connection.Username,
		"--dbname", database,
	}
}

func runLoggedCommand(ctx context.Context, name string, args []string, password, sslMode, stage string, stdin io.Reader, heartbeat func(time.Duration) string, report Reporter) error {
	start := time.Now()
	path, err := postgresToolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = postgresCommandEnv(password, sslMode)
	if stdin != nil {
		cmd.Stdin = stdin
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		scanCommandOutput(stdout, name, stage, report)
	}()
	go func() {
		defer wg.Done()
		scanCommandOutput(stderr, name, stage, report)
	}()
	if heartbeat != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if message := heartbeat(time.Since(start)); message != "" {
						report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: message})
					}
				case <-done:
					return
				}
			}
		}()
	}
	err = cmd.Wait()
	close(done)
	wg.Wait()
	if err != nil {
		return err
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: fmt.Sprintf("%s 执行完成，用时 %s", name, time.Since(start).Truncate(time.Second))})
	return nil
}

func runLoggedCommandToWriter(ctx context.Context, name string, args []string, env []string, stage string, stdin io.Reader, stdout io.Writer, heartbeat func(time.Duration) string, report Reporter) error {
	start := time.Now()
	path, err := toolPath(name)
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = env
	if stdin != nil {
		cmd.Stdin = stdin
	}
	if stdout != nil {
		cmd.Stdout = stdout
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		scanCommandOutput(stderr, name, stage, report)
	}()
	if heartbeat != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ticker := time.NewTicker(30 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if message := heartbeat(time.Since(start)); message != "" {
						report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: message})
					}
				case <-done:
					return
				}
			}
		}()
	}
	err = cmd.Wait()
	close(done)
	wg.Wait()
	if err != nil {
		return err
	}
	report(Progress{Stage: stage, EventLevel: "INFO", EventMessage: fmt.Sprintf("%s 执行完成，用时 %s", name, time.Since(start).Truncate(time.Second))})
	return nil
}

func writeFilteredRestoreList(ctx context.Context, pgRestoreTool, dumpPath, listPath, password, sslMode string) error {
	output, err := runCommandOutput(ctx, pgRestoreTool, []string{"--list", dumpPath}, password, sslMode)
	if err != nil {
		return err
	}
	lines := strings.Split(string(output), "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		if skipRestoreTOCEntry(line) {
			continue
		}
		filtered = append(filtered, line)
	}
	return os.WriteFile(listPath, []byte(strings.Join(filtered, "\n")), 0o600)
}

func runCommandOutput(ctx context.Context, name string, args []string, password, sslMode string) ([]byte, error) {
	path, err := postgresToolPath(name)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = postgresCommandEnv(password, sslMode)
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("%s: %w: %s", name, err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}
	return output, nil
}

func postgresToolPath(name string) (string, error) {
	return toolPath(name)
}

func postgresToolName(base string, major int) string {
	if major >= 13 && major <= 18 {
		return fmt.Sprintf("%s-%d", base, major)
	}
	return base
}

func postgresMajorVersion(version string) (int, error) {
	major, ok := ParseMajorVersion(version)
	if !ok {
		return 0, fmt.Errorf("unsupported version %q", version)
	}
	if major < 13 || major > 18 {
		return 0, fmt.Errorf("version %q is outside supported compatibility matrix", version)
	}
	return major, nil
}

func ParseMajorVersion(version string) (int, bool) {
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

func postgresCommandEnv(password, sslMode string) []string {
	if sslMode == "" {
		sslMode = "prefer"
	}
	return append(os.Environ(), "PGPASSWORD="+password, "PGSSLMODE="+sslMode)
}

func mysqlToolPath(name string) (string, error) {
	return toolPath(name)
}

func mysqlCommandEnv(password string) []string {
	return append(os.Environ(), "MYSQL_PWD="+password)
}

func commandHeartbeat(name string) func(time.Duration) string {
	return func(elapsed time.Duration) string {
		return fmt.Sprintf("%s 仍在执行，已用时 %s", name, elapsed.Truncate(time.Second))
	}
}

func dumpHeartbeat(path string) func(time.Duration) string {
	return func(elapsed time.Duration) string {
		size := directorySize(path)
		if size > 0 {
			return fmt.Sprintf("pg_dump 仍在导出，已用时 %s，本地归档大小 %s", elapsed.Truncate(time.Second), formatBytes(size))
		}
		return fmt.Sprintf("pg_dump 仍在导出，已用时 %s，等待源端返回数据", elapsed.Truncate(time.Second))
	}
}

func directorySize(path string) int64 {
	var total int64
	_ = filepath.WalkDir(path, func(_ string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return nil
		}
		total += info.Size()
		return nil
	})
	return total
}

func nativeToolJobs(tableCount int) int {
	if value := strings.TrimSpace(os.Getenv("DATABRIDGE_PG_JOBS")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err == nil && parsed > 0 {
			return minInt(parsed, 16)
		}
	}
	jobs := minInt(runtime.NumCPU(), 4)
	if tableCount > 0 {
		jobs = minInt(jobs, tableCount)
	}
	if jobs < 1 {
		return 1
	}
	return jobs
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func isFullDatabaseSelection(ctx context.Context, conn *pgx.Conn, selected []string) (bool, error) {
	allTables, err := listUserTables(ctx, conn)
	if err != nil {
		return false, err
	}
	if len(allTables) == 0 || len(selected) != len(allTables) {
		return false, nil
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, table := range selected {
		selectedSet[table] = struct{}{}
	}
	for _, table := range allTables {
		if _, ok := selectedSet[table]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func listUserTables(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `
		SELECT n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r','p') AND NOT c.relispartition
		  AND n.nspname NOT IN ('pg_catalog','information_schema')
		  AND n.nspname NOT LIKE 'pg_toast%'
		ORDER BY n.nspname, c.relname`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var schema, table string
		if err := rows.Scan(&schema, &table); err != nil {
			return nil, err
		}
		result = append(result, schema+"."+table)
	}
	return result, rows.Err()
}

func skipRestoreTOCEntry(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, ";") {
		return false
	}
	return strings.Contains(trimmed, " SCHEMA - ") ||
		strings.Contains(trimmed, " COMMENT - SCHEMA ") ||
		strings.Contains(trimmed, " ACL - SCHEMA ") ||
		strings.Contains(trimmed, " EXTENSION - ") ||
		strings.Contains(trimmed, " COMMENT - EXTENSION ") ||
		strings.Contains(trimmed, " ACL - EXTENSION ")
}

func tablePattern(value string) string {
	if strings.Contains(value, `"`) {
		return value
	}
	schema, table, ok := postgres.SplitTable(value)
	if !ok {
		return value
	}
	return postgres.QuoteIdent(schema) + "." + postgres.QuoteIdent(table)
}

func dumpPatterns(tables []tableDefinition) []string {
	seen := make(map[string]struct{})
	result := make([]string, 0, len(tables))
	add := func(value string) {
		if value == "" {
			return
		}
		pattern := tablePattern(value)
		if _, exists := seen[pattern]; exists {
			return
		}
		seen[pattern] = struct{}{}
		result = append(result, pattern)
	}
	for _, table := range tables {
		add(table.Schema + "." + table.Name)
		for _, column := range table.Columns {
			if column.IdentitySequence != nil {
				add(*column.IdentitySequence)
			}
		}
	}
	return result
}

func scanCommandOutput(reader io.Reader, name, stage string, report Reporter) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			report(Progress{Stage: stage, CurrentTable: commandCurrentTable(line), EventLevel: "INFO", EventMessage: commandLogMessage(name, line)})
		}
	}
}

func commandLogMessage(name, line string) string {
	prefix := name + ": "
	if strings.HasPrefix(line, prefix) {
		return line
	}
	return prefix + line
}

func commandCurrentTable(line string) string {
	for _, marker := range []string{`dumping contents of table "`, `TABLE DATA `} {
		index := strings.Index(line, marker)
		if index < 0 {
			continue
		}
		value := line[index+len(marker):]
		if end := strings.IndexByte(value, '"'); end >= 0 {
			value = value[:end]
		} else if end := strings.IndexByte(value, ' '); end >= 0 {
			value = value[:end]
		}
		return strings.Trim(value, `"`)
	}
	return ""
}
