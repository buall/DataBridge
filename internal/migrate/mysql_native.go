package migrate

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"databridge/internal/model"
	"databridge/internal/mysql"
)

type mysqlTableDefinition struct {
	Schema string
	Name   string
	Rows   int64
}

func (e *Engine) RunNativeMySQL(
	ctx context.Context,
	source model.Connection,
	target model.Connection,
	request model.MigrationRequest,
	report Reporter,
) error {
	for _, name := range []string{"mysql", "mysqldump"} {
		if _, err := mysqlToolPath(name); err != nil {
			return err
		}
	}

	sourceDB, err := mysql.Connect(ctx, source, request.SourceDatabase)
	if err != nil {
		return fmt.Errorf("connect source: %w", err)
	}
	defer sourceDB.Close()
	targetDB, err := mysql.Connect(ctx, target, request.TargetDatabase)
	if err != nil {
		return fmt.Errorf("connect target: %w", err)
	}
	defer targetDB.Close()

	tables, err := loadMySQLTables(ctx, sourceDB, request.SourceDatabase, request.Tables)
	if err != nil {
		return fmt.Errorf("inspect source tables: %w", err)
	}
	if len(tables) == 0 {
		return fmt.Errorf("no source tables selected")
	}

	var totalRows int64
	for i := range tables {
		count, countErr := countMySQLRows(ctx, sourceDB, tables[i].Schema, tables[i].Name)
		if countErr != nil {
			return fmt.Errorf("count %s.%s: %w", tables[i].Schema, tables[i].Name, countErr)
		}
		tables[i].Rows = count
		totalRows += count
	}
	dumpAll, err := isFullMySQLDatabaseSelection(ctx, sourceDB, request.SourceDatabase, request.Tables)
	if err != nil {
		return fmt.Errorf("inspect full database selection: %w", err)
	}
	scope := "按所选表"
	if dumpAll {
		scope = "整库"
	}
	report(Progress{Stage: "precheck", RowsTotal: totalRows, EventLevel: "INFO", EventMessage: fmt.Sprintf("MySQL 原生工具预检查完成，共 %d 张表、%d 行，导出范围：%s", len(tables), totalRows, scope)})

	if err := prepareNativeMySQLTarget(ctx, targetDB, tables, request.ConflictStrategy, report); err != nil {
		return fmt.Errorf("prepare native target: %w", err)
	}

	tempDir, err := os.MkdirTemp("", "databridge-mysql-*")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	dumpPath := filepath.Join(tempDir, "dump.sql")

	report(Progress{Stage: "dump", RowsTotal: totalRows, EventLevel: "INFO", EventMessage: fmt.Sprintf("正在使用 mysqldump 导出源端数据，范围=%s", scope)})
	if err := dumpMySQL(ctx, source, request.SourceDatabase, tables, dumpPath, dumpAll, report); err != nil {
		return err
	}

	report(Progress{Stage: "restore", RowsTotal: totalRows, EventLevel: "INFO", EventMessage: "正在使用 mysql 恢复到目标端"})
	if err := restoreMySQL(ctx, target, request.TargetDatabase, dumpPath, report); err != nil {
		return err
	}

	var copiedTotal int64
	for index, table := range tables {
		count, countErr := countMySQLRows(ctx, targetDB, table.Schema, table.Name)
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
		report(Progress{Stage: "validation", TablesDone: index + 1, RowsTotal: totalRows, RowsCopied: copiedTotal, Result: &result})
	}
	report(Progress{Stage: "completed", TablesDone: len(tables), RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: "MySQL 原生工具迁移完成，行数校验已执行"})
	return nil
}

func loadMySQLTables(ctx context.Context, db *sql.DB, database string, selected []string) ([]mysqlTableDefinition, error) {
	result := make([]mysqlTableDefinition, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		schema, table := splitMySQLTable(database, value)
		key := schema + "." + table
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		var found string
		err := db.QueryRowContext(ctx, `
			SELECT table_name
			FROM information_schema.tables
			WHERE table_schema = ? AND table_name = ? AND table_type = 'BASE TABLE'`,
			schema, table,
		).Scan(&found)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil, fmt.Errorf("table %s not found", key)
			}
			return nil, err
		}
		result = append(result, mysqlTableDefinition{Schema: schema, Name: table})
	}
	return result, nil
}

func isFullMySQLDatabaseSelection(ctx context.Context, db *sql.DB, database string, selected []string) (bool, error) {
	allTables, err := listMySQLUserTables(ctx, db, database)
	if err != nil {
		return false, err
	}
	if len(allTables) == 0 || len(selected) != len(allTables) {
		return false, nil
	}
	selectedSet := make(map[string]struct{}, len(selected))
	for _, table := range selected {
		_, name := splitMySQLTable(database, table)
		selectedSet[name] = struct{}{}
	}
	for _, table := range allTables {
		if _, ok := selectedSet[table]; !ok {
			return false, nil
		}
	}
	return true, nil
}

func listMySQLUserTables(ctx context.Context, db *sql.DB, database string) ([]string, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = ? AND table_type = 'BASE TABLE'
		ORDER BY table_name`, database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			return nil, err
		}
		result = append(result, table)
	}
	return result, rows.Err()
}

func prepareNativeMySQLTarget(ctx context.Context, db *sql.DB, tables []mysqlTableDefinition, strategy string, report Reporter) error {
	if strategy == "" {
		strategy = "backup"
	}
	report(Progress{Stage: "target-prep", EventLevel: "INFO", EventMessage: "正在预处理目标端同名对象"})
	for _, table := range tables {
		qualified := mysql.QuoteIdent(table.Name)
		switch strategy {
		case "backup":
			var found string
			err := db.QueryRowContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, table.Name).Scan(&found)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil {
				backup := table.Name + "_databridge_backup_" + time.Now().Format("20060102_150405")
				if _, err := db.ExecContext(ctx, "RENAME TABLE "+qualified+" TO "+mysql.QuoteIdent(backup)); err != nil {
					return err
				}
			}
		case "overwrite":
			if _, err := db.ExecContext(ctx, "DROP TABLE IF EXISTS "+qualified); err != nil {
				return err
			}
		case "skip":
			var found string
			err := db.QueryRowContext(ctx, `SELECT table_name FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = ?`, table.Name).Scan(&found)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if err == nil {
				return fmt.Errorf("target table %s exists and strategy is skip", table.Schema+"."+table.Name)
			}
		default:
			return fmt.Errorf("unsupported conflict strategy %q", strategy)
		}
	}
	return nil
}

func dumpMySQL(ctx context.Context, source model.Connection, database string, tables []mysqlTableDefinition, dumpPath string, dumpAll bool, report Reporter) error {
	args := []string{
		"--host", source.Host,
		"--port", strconv.Itoa(source.Port),
		"--user", source.Username,
		"--single-transaction",
		"--skip-lock-tables",
		"--default-character-set=utf8mb4",
		"--routines",
		"--triggers",
		"--events",
		database,
	}
	if !dumpAll {
		for _, table := range tables {
			args = append(args, table.Name)
		}
	}
	file, err := os.Create(dumpPath)
	if err != nil {
		return fmt.Errorf("create dump file: %w", err)
	}
	defer file.Close()
	if err := runLoggedCommandToWriter(ctx, "mysqldump", args, mysqlCommandEnv(source.Password), "dump", nil, file, commandHeartbeat("mysqldump"), report); err != nil {
		return fmt.Errorf("mysqldump: %w", err)
	}
	return nil
}

func restoreMySQL(ctx context.Context, target model.Connection, database, dumpPath string, report Reporter) error {
	file, err := os.Open(dumpPath)
	if err != nil {
		return fmt.Errorf("open dump file: %w", err)
	}
	defer file.Close()
	args := []string{
		"--host", target.Host,
		"--port", strconv.Itoa(target.Port),
		"--user", target.Username,
		"--default-character-set=utf8mb4",
		database,
	}
	if err := runLoggedCommandToWriter(ctx, "mysql", args, mysqlCommandEnv(target.Password), "restore", file, io.Discard, commandHeartbeat("mysql"), report); err != nil {
		return fmt.Errorf("mysql restore: %w", err)
	}
	return nil
}

func countMySQLRows(ctx context.Context, db *sql.DB, _, table string) (int64, error) {
	var count int64
	err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+mysql.QuoteIdent(table)).Scan(&count)
	return count, err
}

func splitMySQLTable(database, value string) (string, string) {
	parts := strings.SplitN(value, ".", 2)
	if len(parts) == 2 && parts[0] != "" && parts[1] != "" {
		return parts[0], parts[1]
	}
	return database, value
}

