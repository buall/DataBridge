package migrate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"databridge/internal/model"
	"databridge/internal/postgres"
	"github.com/jackc/pgx/v5"
)

type Progress struct {
	Stage        string
	CurrentTable string
	TablesDone   int
	RowsTotal    int64
	RowsCopied   int64
	EventLevel   string
	EventMessage string
	Result       *model.TableResult
}

type Reporter func(Progress)

type Engine struct{}

func New() *Engine { return &Engine{} }

func (e *Engine) Run(
	ctx context.Context,
	source model.Connection,
	target model.Connection,
	request model.MigrationRequest,
	report Reporter,
) error {
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
	if _, err := targetConn.Exec(ctx, "SET synchronous_commit = off"); err != nil {
		return fmt.Errorf("configure target session: %w", err)
	}

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
	report(Progress{Stage: "precheck", RowsTotal: totalRows, EventLevel: "INFO", EventMessage: fmt.Sprintf("预检查完成，共 %d 张表、%d 行", len(tables), totalRows)})

	schemas := uniqueSchemas(tables)
	if err := createSchemas(ctx, targetConn, schemas); err != nil {
		return fmt.Errorf("create schemas: %w", err)
	}
	if err := createEnumTypes(ctx, sourceConn, targetConn, schemas); err != nil {
		return fmt.Errorf("create enum types: %w", err)
	}
	if err := createSequences(ctx, sourceConn, targetConn, schemas); err != nil {
		return fmt.Errorf("create sequences: %w", err)
	}

	for _, table := range tables {
		if err := prepareTable(ctx, sourceConn, targetConn, table, request.ConflictStrategy); err != nil {
			return fmt.Errorf("prepare %s.%s: %w", table.Schema, table.Name, err)
		}
	}
	report(Progress{Stage: "schema", RowsTotal: totalRows, EventLevel: "INFO", EventMessage: "Schema、类型、序列和表结构创建完成"})

	var copiedTotal int64
	results := make([]model.TableResult, 0, len(tables))
	for index, table := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		fullName := table.Schema + "." + table.Name
		report(Progress{Stage: "data", CurrentTable: fullName, TablesDone: index, RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: "开始迁移 " + fullName})
		start := time.Now()
		copied, copyErr := copyTable(ctx, sourceConn, targetConn, table, func(bytes int64, elapsed time.Duration) {
			report(Progress{
				Stage: "data", CurrentTable: fullName, TablesDone: index,
				RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO",
				EventMessage: fmt.Sprintf("%s 正在迁移，已传输 %s，用时 %s", fullName, formatBytes(bytes), elapsed.Truncate(time.Second)),
			})
		})
		result := model.TableResult{
			Table: fullName, SourceRows: table.Rows, CopiedRows: copied,
			Duration: time.Since(start),
		}
		if copyErr != nil {
			result.Error = copyErr.Error()
			report(Progress{Stage: "data", CurrentTable: fullName, TablesDone: index, RowsTotal: totalRows, RowsCopied: copiedTotal, Result: &result, EventLevel: "ERROR", EventMessage: "迁移失败 " + fullName + ": " + copyErr.Error()})
			return fmt.Errorf("copy %s: %w", fullName, copyErr)
		}
		copiedTotal += copied
		results = append(results, result)
		report(Progress{Stage: "data", CurrentTable: fullName, TablesDone: index + 1, RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: fmt.Sprintf("%s 已迁移 %d 行", fullName, copied)})
	}

	report(Progress{Stage: "constraints", TablesDone: len(tables), RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: "正在创建主键、唯一约束、检查约束和索引"})
	for _, table := range tables {
		if err := createNonForeignKeyConstraints(ctx, sourceConn, targetConn, table); err != nil {
			return fmt.Errorf("create non-foreign-key constraints for %s.%s: %w", table.Schema, table.Name, err)
		}
		if err := createIndexes(ctx, sourceConn, targetConn, table); err != nil {
			return fmt.Errorf("create indexes for %s.%s: %w", table.Schema, table.Name, err)
		}
	}
	report(Progress{Stage: "constraints", TablesDone: len(tables), RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: "正在创建外键约束"})
	for _, table := range tables {
		if err := createForeignKeys(ctx, sourceConn, targetConn, table); err != nil {
			return fmt.Errorf("create foreign keys for %s.%s: %w", table.Schema, table.Name, err)
		}
	}
	if err := syncSequences(ctx, sourceConn, targetConn, schemas); err != nil {
		return fmt.Errorf("sync sequences: %w", err)
	}

	for index := range results {
		schema, table, _ := postgres.SplitTable(results[index].Table)
		count, countErr := countRows(ctx, targetConn, schema, table)
		if countErr != nil {
			return fmt.Errorf("validate %s: %w", results[index].Table, countErr)
		}
		results[index].TargetRows = count
		results[index].Valid = count == results[index].SourceRows
		result := results[index]
		report(Progress{Stage: "validation", TablesDone: len(tables), RowsTotal: totalRows, RowsCopied: copiedTotal, Result: &result})
	}
	report(Progress{Stage: "completed", TablesDone: len(tables), RowsTotal: totalRows, RowsCopied: copiedTotal, EventLevel: "INFO", EventMessage: "迁移完成，行数校验已执行"})
	return nil
}

type tableDefinition struct {
	Schema  string
	Name    string
	Rows    int64
	Columns []columnDefinition
}

type columnDefinition struct {
	Name             string
	Type             string
	NotNull          bool
	Default          *string
	Identity         string
	IdentitySequence *string
	Generated        string
}

func loadTables(ctx context.Context, conn *pgx.Conn, selected []string) ([]tableDefinition, error) {
	result := make([]tableDefinition, 0, len(selected))
	seen := make(map[string]struct{}, len(selected))
	for _, value := range selected {
		schema, table, ok := postgres.SplitTable(value)
		if !ok {
			return nil, fmt.Errorf("invalid table name %q; expected schema.table", value)
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		rows, err := conn.Query(ctx, `
			SELECT a.attname, pg_catalog.format_type(a.atttypid, a.atttypmod),
			       a.attnotnull, pg_get_expr(d.adbin, d.adrelid),
			       a.attidentity::text,
			       pg_get_serial_sequence(quote_ident(n.nspname)||'.'||quote_ident(c.relname), a.attname),
			       a.attgenerated::text
			FROM pg_attribute a
			JOIN pg_class c ON c.oid = a.attrelid
			JOIN pg_namespace n ON n.oid = c.relnamespace
			LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
			WHERE n.nspname=$1 AND c.relname=$2 AND a.attnum > 0 AND NOT a.attisdropped
			ORDER BY a.attnum`, schema, table)
		if err != nil {
			return nil, err
		}
		var columns []columnDefinition
		for rows.Next() {
			var column columnDefinition
			if err := rows.Scan(&column.Name, &column.Type, &column.NotNull, &column.Default, &column.Identity, &column.IdentitySequence, &column.Generated); err != nil {
				rows.Close()
				return nil, err
			}
			columns = append(columns, column)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		if len(columns) == 0 {
			return nil, fmt.Errorf("table %s not found or has no columns", value)
		}
		result = append(result, tableDefinition{Schema: schema, Name: table, Columns: columns})
	}
	return result, nil
}

func createSchemas(ctx context.Context, target *pgx.Conn, schemas []string) error {
	for _, schema := range schemas {
		if _, err := target.Exec(ctx, "CREATE SCHEMA IF NOT EXISTS "+postgres.QuoteIdent(schema)); err != nil {
			return err
		}
	}
	return nil
}

func createEnumTypes(ctx context.Context, source, target *pgx.Conn, schemas []string) error {
	for _, schema := range schemas {
		rows, err := source.Query(ctx, `
			SELECT t.typname, array_agg(e.enumlabel ORDER BY e.enumsortorder)
			FROM pg_type t JOIN pg_enum e ON e.enumtypid=t.oid
			JOIN pg_namespace n ON n.oid=t.typnamespace
			WHERE n.nspname=$1 GROUP BY t.typname`, schema)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			var labels []string
			if err := rows.Scan(&name, &labels); err != nil {
				rows.Close()
				return err
			}
			var quoted []string
			for _, label := range labels {
				quoted = append(quoted, quoteLiteral(label))
			}
			sql := fmt.Sprintf(`DO $$ BEGIN CREATE TYPE %s.%s AS ENUM (%s); EXCEPTION WHEN duplicate_object THEN NULL; END $$`,
				postgres.QuoteIdent(schema), postgres.QuoteIdent(name), strings.Join(quoted, ","))
			if _, err := target.Exec(ctx, sql); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func createSequences(ctx context.Context, source, target *pgx.Conn, schemas []string) error {
	for _, schema := range schemas {
		rows, err := source.Query(ctx, `SELECT sequencename FROM pg_sequences WHERE schemaname=$1 ORDER BY sequencename`, schema)
		if err != nil {
			return err
		}
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			if _, err := target.Exec(ctx, fmt.Sprintf("CREATE SEQUENCE IF NOT EXISTS %s.%s", postgres.QuoteIdent(schema), postgres.QuoteIdent(name))); err != nil {
				rows.Close()
				return err
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	return nil
}

func prepareTable(ctx context.Context, source, target *pgx.Conn, table tableDefinition, strategy string) error {
	qualified := postgres.QuoteIdent(table.Schema) + "." + postgres.QuoteIdent(table.Name)
	var exists bool
	if err := target.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, table.Schema+"."+table.Name).Scan(&exists); err != nil {
		return err
	}
	if exists {
		switch strategy {
		case "skip":
			return fmt.Errorf("target table %s exists and strategy is skip", table.Schema+"."+table.Name)
		case "backup":
			backup := table.Name + "_databridge_backup_" + time.Now().Format("20060102_150405")
			if _, err := target.Exec(ctx, fmt.Sprintf("ALTER TABLE %s RENAME TO %s", qualified, postgres.QuoteIdent(backup))); err != nil {
				return err
			}
			if err := renameBackupIndexes(ctx, target, table.Schema, backup); err != nil {
				return err
			}
		case "overwrite":
			if _, err := target.Exec(ctx, "DROP TABLE "+qualified+" CASCADE"); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported conflict strategy %q", strategy)
		}
	}
	definitions := make([]string, 0, len(table.Columns))
	for _, column := range table.Columns {
		part := postgres.QuoteIdent(column.Name) + " " + column.Type
		if column.Generated != "" && column.Default != nil {
			part += " GENERATED ALWAYS AS (" + *column.Default + ") STORED"
		} else if column.Identity != "" && column.IdentitySequence != nil {
			part += " DEFAULT nextval(" + quoteLiteral(*column.IdentitySequence) + "::regclass)"
		} else if column.Default != nil {
			part += " DEFAULT " + *column.Default
		}
		if column.NotNull {
			part += " NOT NULL"
		}
		definitions = append(definitions, part)
	}
	_, err := target.Exec(ctx, fmt.Sprintf("CREATE TABLE %s (%s)", qualified, strings.Join(definitions, ",")))
	return err
}

func renameBackupIndexes(ctx context.Context, target *pgx.Conn, schema, table string) error {
	rows, err := target.Query(ctx, `SELECT indexname FROM pg_indexes WHERE schemaname=$1 AND tablename=$2`, schema, table)
	if err != nil {
		return err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return err
		}
		names = append(names, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	suffix := "_databridge_" + time.Now().Format("150405")
	for _, name := range names {
		newName := name + suffix
		if _, err := target.Exec(ctx, fmt.Sprintf("ALTER INDEX %s.%s RENAME TO %s", postgres.QuoteIdent(schema), postgres.QuoteIdent(name), postgres.QuoteIdent(newName))); err != nil {
			return err
		}
	}
	return nil
}

func copyTable(ctx context.Context, source, target *pgx.Conn, table tableDefinition, heartbeat func(bytes int64, elapsed time.Duration)) (int64, error) {
	var columns []string
	for _, column := range table.Columns {
		if column.Generated == "" {
			columns = append(columns, postgres.QuoteIdent(column.Name))
		}
	}
	qualified := postgres.QuoteIdent(table.Schema) + "." + postgres.QuoteIdent(table.Name)
	columnList := strings.Join(columns, ",")
	reader, writer := io.Pipe()
	var bytesCopied int64
	done := make(chan struct{})
	if heartbeat != nil {
		start := time.Now()
		go func() {
			ticker := time.NewTicker(10 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					heartbeat(atomic.LoadInt64(&bytesCopied), time.Since(start))
				case <-done:
					return
				}
			}
		}()
	}
	defer close(done)
	var sourceErr error
	var sourceWG sync.WaitGroup
	sourceWG.Add(1)
	go func() {
		defer sourceWG.Done()
		_, sourceErr = source.PgConn().CopyTo(ctx, countingWriter{writer: writer, bytes: &bytesCopied}, fmt.Sprintf("COPY %s (%s) TO STDOUT BINARY", qualified, columnList))
		_ = writer.CloseWithError(sourceErr)
	}()
	tag, targetErr := target.PgConn().CopyFrom(ctx, reader, fmt.Sprintf("COPY %s (%s) FROM STDIN BINARY", qualified, columnList))
	_ = reader.CloseWithError(targetErr)
	sourceWG.Wait()
	if targetErr != nil {
		return 0, targetErr
	}
	if sourceErr != nil {
		return 0, sourceErr
	}
	return tag.RowsAffected(), nil
}

type countingWriter struct {
	writer io.Writer
	bytes  *int64
}

func (w countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	atomic.AddInt64(w.bytes, int64(n))
	return n, err
}

func createNonForeignKeyConstraints(ctx context.Context, source, target *pgx.Conn, table tableDefinition) error {
	rows, err := source.Query(ctx, `
		SELECT conname, pg_get_constraintdef(oid, true)
		FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype IN ('p','u','c')
		ORDER BY CASE contype WHEN 'p' THEN 1 WHEN 'u' THEN 2 ELSE 3 END`,
		table.Schema+"."+table.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	qualified := postgres.QuoteIdent(table.Schema) + "." + postgres.QuoteIdent(table.Name)
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return err
		}
		if _, err := target.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", qualified, postgres.QuoteIdent(name), definition)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func createForeignKeys(ctx context.Context, source, target *pgx.Conn, table tableDefinition) error {
	rows, err := source.Query(ctx, `
		SELECT conname, pg_get_constraintdef(oid, true)
		FROM pg_constraint
		WHERE conrelid = $1::regclass AND contype = 'f'
		ORDER BY conname`,
		table.Schema+"."+table.Name)
	if err != nil {
		return err
	}
	defer rows.Close()
	qualified := postgres.QuoteIdent(table.Schema) + "." + postgres.QuoteIdent(table.Name)
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			return err
		}
		if _, err := target.Exec(ctx, fmt.Sprintf("ALTER TABLE %s ADD CONSTRAINT %s %s", qualified, postgres.QuoteIdent(name), definition)); err != nil {
			return err
		}
	}
	return rows.Err()
}

func createIndexes(ctx context.Context, source, target *pgx.Conn, table tableDefinition) error {
	indexRows, err := source.Query(ctx, `
		SELECT indexdef FROM pg_indexes
		WHERE schemaname=$1 AND tablename=$2
		  AND indexname NOT IN (
		    SELECT conname FROM pg_constraint WHERE conrelid=$3::regclass
		  )`, table.Schema, table.Name, table.Schema+"."+table.Name)
	if err != nil {
		return err
	}
	for indexRows.Next() {
		var definition string
		if err := indexRows.Scan(&definition); err != nil {
			indexRows.Close()
			return err
		}
		if _, err := target.Exec(ctx, definition); err != nil {
			indexRows.Close()
			return err
		}
	}
	indexRows.Close()
	return indexRows.Err()
}

func syncSequences(ctx context.Context, source, target *pgx.Conn, schemas []string) error {
	for _, schema := range schemas {
		rows, err := source.Query(ctx, `SELECT sequencename FROM pg_sequences WHERE schemaname=$1`, schema)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				rows.Close()
				return err
			}
			names = append(names, name)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}

		// pgx connections cannot execute another query while Rows is open.
		// Read the sequence list first, then query each sequence value.
		for _, name := range names {
			qualified := postgres.QuoteIdent(schema) + "." + postgres.QuoteIdent(name)
			var lastValue int64
			var isCalled bool
			if err := source.QueryRow(ctx, "SELECT last_value, is_called FROM "+qualified).Scan(&lastValue, &isCalled); err != nil {
				return err
			}
			if _, err := target.Exec(ctx, "SELECT setval($1::regclass, $2, $3)", schema+"."+name, lastValue, isCalled); err != nil {
				return err
			}
		}
	}
	return nil
}

func countRows(ctx context.Context, conn *pgx.Conn, schema, table string) (int64, error) {
	var count int64
	err := conn.QueryRow(ctx, fmt.Sprintf("SELECT count(*) FROM %s.%s", postgres.QuoteIdent(schema), postgres.QuoteIdent(table))).Scan(&count)
	return count, err
}

func uniqueSchemas(tables []tableDefinition) []string {
	seen := make(map[string]struct{})
	var result []string
	for _, table := range tables {
		if _, ok := seen[table.Schema]; !ok {
			seen[table.Schema] = struct{}{}
			result = append(result, table.Schema)
		}
	}
	return result
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func formatBytes(value int64) string {
	const unit = 1024
	if value < unit {
		return fmt.Sprintf("%d B", value)
	}
	div, exp := int64(unit), 0
	for n := value / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(value)/float64(div), "KMGTPE"[exp])
}
