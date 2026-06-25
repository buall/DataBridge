package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"databridge/internal/dbtls"
	"databridge/internal/fakedata"
	"databridge/internal/model"
	mysqlDriver "github.com/go-sql-driver/mysql"
)

func Test(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	start := time.Now()
	dsn, err := DSNWithTLSConfig(input.Host, input.Port, input.Username, input.Password, "", input.SSLMode, input.TLSConfig)
	if err != nil {
		return 0, "", err
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return 0, "", err
	}
	defer conn.Close()
	conn.SetConnMaxLifetime(time.Minute)
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	if err := conn.PingContext(ctx); err != nil {
		return 0, "", err
	}
	var version string
	if err := conn.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&version); err != nil {
		return 0, "", err
	}
	return time.Since(start), version, nil
}

func Connect(ctx context.Context, connection model.Connection, database string) (*sql.DB, error) {
	dsn, err := DSNWithTLSConfig(connection.Host, connection.Port, connection.Username, connection.Password, database, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return nil, err
	}
	conn, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetConnMaxLifetime(time.Minute)
	conn.SetMaxOpenConns(4)
	conn.SetMaxIdleConns(4)
	if err := conn.PingContext(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

func ServerVersion(ctx context.Context, connection model.Connection) (string, error) {
	conn, err := Connect(ctx, connection, "")
	if err != nil {
		return "", err
	}
	defer conn.Close()
	var version string
	if err := conn.QueryRowContext(ctx, `SELECT VERSION()`).Scan(&version); err != nil {
		return "", err
	}
	return version, nil
}

func DSN(host string, port int, username, password, database, sslMode string) string {
	return dsn(host, port, username, password, database, tlsMode(sslMode))
}

func DSNWithTLSConfig(host string, port int, username, password, database, sslMode string, tlsConfig model.ConnectionTLSConfig) (string, error) {
	if !strings.EqualFold(strings.TrimSpace(sslMode), "verify-full") {
		return DSN(host, port, username, password, database, sslMode), nil
	}
	config, err := dbtls.Config(host, tlsConfig, true)
	if err != nil {
		return "", err
	}
	key := "databridge-verify-full-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := mysqlDriver.RegisterTLSConfig(key, config); err != nil {
		return "", err
	}
	return dsn(host, port, username, password, database, key), nil
}

func dsn(host string, port int, username, password, database, tlsMode string) string {
	if port == 0 {
		port = 3306
	}
	address := net.JoinHostPort(host, strconv.Itoa(port))
	params := url.Values{}
	params.Set("charset", "utf8mb4")
	params.Set("parseTime", "true")
	params.Set("timeout", "8s")
	params.Set("readTimeout", "30s")
	params.Set("writeTimeout", "30s")
	params.Set("multiStatements", "true")
	params.Set("tls", tlsMode)
	return fmt.Sprintf("%s:%s@tcp(%s)%s?%s",
		username,
		password,
		address,
		"/"+database,
		params.Encode(),
	)
}

func tlsMode(sslMode string) string {
	switch strings.ToLower(strings.TrimSpace(sslMode)) {
	case "", "prefer":
		return "preferred"
	case "require", "verify-full":
		return "true"
	case "disable":
		return "false"
	default:
		return "preferred"
	}
}

func Databases(ctx context.Context, connection model.Connection) ([]model.DatabaseInfo, error) {
	conn, err := Connect(ctx, connection, "")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT schema_name,
		       COALESCE(SUM(data_length + index_length), 0) AS size_bytes
		FROM information_schema.schemata s
		LEFT JOIN information_schema.tables t ON t.table_schema = s.schema_name
		WHERE s.schema_name NOT IN ('information_schema', 'mysql', 'performance_schema', 'sys')
		GROUP BY schema_name
		ORDER BY schema_name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.DatabaseInfo
	for rows.Next() {
		var item model.DatabaseInfo
		if err := rows.Scan(&item.Name, &item.Size); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	if result == nil {
		result = []model.DatabaseInfo{}
	}
	return result, rows.Err()
}

func CreateDatabase(ctx context.Context, connection model.Connection, name string) error {
	conn, err := Connect(ctx, connection, "")
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.ExecContext(ctx, "CREATE DATABASE "+QuoteIdent(name)+" CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci")
	return err
}

func Tables(ctx context.Context, connection model.Connection, database string) ([]model.TableInfo, error) {
	conn, err := Connect(ctx, connection, database)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, `
		SELECT table_schema, table_name,
		       COALESCE(data_length + index_length, 0) AS size_bytes,
		       COALESCE(table_rows, 0) AS estimated_rows
		FROM information_schema.tables
		WHERE table_schema = ?
		  AND table_type = 'BASE TABLE'
		ORDER BY table_schema, table_name`, database)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []model.TableInfo
	for rows.Next() {
		var item model.TableInfo
		if err := rows.Scan(&item.Schema, &item.Name, &item.SizeBytes, &item.Rows); err != nil {
			return nil, err
		}
		item.ID = fmt.Sprintf("%s.%s", item.Schema, item.Name)
		result = append(result, item)
	}
	if result == nil {
		result = []model.TableInfo{}
	}
	return result, rows.Err()
}

func QuoteIdent(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}

func SeedData(ctx context.Context, connection model.Connection, database string, rows int, mode, template string) (model.SeedDataResult, error) {
	if rows <= 0 {
		rows = 100
	}
	if template == "" {
		template = "users"
	}
	conn, err := Connect(ctx, connection, database)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	defer conn.Close()

	tableName, createSQL, err := seedTableDefinitionMySQL(template)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	if _, err := conn.ExecContext(ctx, createSQL); err != nil {
		return model.SeedDataResult{}, err
	}
	if mode == "replace" {
		if _, err := conn.ExecContext(ctx, "DELETE FROM "+QuoteIdent(tableName)); err != nil {
			return model.SeedDataResult{}, err
		}
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	defer tx.Rollback()
	if err := insertSeedRowsMySQL(ctx, tx, tableName, template, rows); err != nil {
		return model.SeedDataResult{}, err
	}

	if err := tx.Commit(); err != nil {
		return model.SeedDataResult{}, err
	}
	return model.SeedDataResult{Database: database, Table: tableName, Rows: rows, Mode: mode, Template: template}, nil
}

func seedTableDefinitionMySQL(template string) (string, string, error) {
	switch template {
	case "users":
		tableName := "databridge_demo_users"
		return tableName, `
			CREATE TABLE IF NOT EXISTS ` + QuoteIdent(tableName) + ` (
				id BIGINT PRIMARY KEY AUTO_INCREMENT,
				email VARCHAR(120) NOT NULL,
				full_name VARCHAR(80) NOT NULL,
				city VARCHAR(40) NOT NULL,
				company VARCHAR(80) NOT NULL,
				job_title VARCHAR(80) NOT NULL,
				status VARCHAR(20) NOT NULL,
				score INT NOT NULL,
				phone VARCHAR(32) NOT NULL,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`, nil
	case "orders":
		tableName := "databridge_demo_orders"
		return tableName, `
			CREATE TABLE IF NOT EXISTS ` + QuoteIdent(tableName) + ` (
				id BIGINT PRIMARY KEY AUTO_INCREMENT,
				order_no VARCHAR(32) NOT NULL,
				customer_name VARCHAR(80) NOT NULL,
				city VARCHAR(40) NOT NULL,
				status VARCHAR(20) NOT NULL,
				channel VARCHAR(20) NOT NULL,
				amount DECIMAL(10,2) NOT NULL,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`, nil
	case "products":
		tableName := "databridge_demo_products"
		return tableName, `
			CREATE TABLE IF NOT EXISTS ` + QuoteIdent(tableName) + ` (
				id BIGINT PRIMARY KEY AUTO_INCREMENT,
				sku VARCHAR(32) NOT NULL,
				name VARCHAR(120) NOT NULL,
				category VARCHAR(40) NOT NULL,
				brand VARCHAR(40) NOT NULL,
				price DECIMAL(10,2) NOT NULL,
				stock INT NOT NULL,
				status VARCHAR(20) NOT NULL,
				created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
			)`, nil
	default:
		return "", "", fmt.Errorf("unsupported seed template %q", template)
	}
}

func insertSeedRowsMySQL(ctx context.Context, tx *sql.Tx, tableName, template string, rows int) error {
	switch template {
	case "users":
		return bulkInsertMySQL(ctx, tx, tableName, 9, fakedata.GenerateUsers(rows), func(row fakedata.UserRow) []any {
			return []any{row.Email, row.FullName, row.City, row.Company, row.JobTitle, row.Status, row.Score, row.Phone, row.CreatedAt}
		}, []string{"email", "full_name", "city", "company", "job_title", "status", "score", "phone", "created_at"})
	case "orders":
		return bulkInsertMySQL(ctx, tx, tableName, 7, fakedata.GenerateOrders(rows), func(row fakedata.OrderRow) []any {
			return []any{row.OrderNo, row.Customer, row.City, row.Status, row.Channel, row.Amount, row.CreatedAt}
		}, []string{"order_no", "customer_name", "city", "status", "channel", "amount", "created_at"})
	case "products":
		return bulkInsertMySQL(ctx, tx, tableName, 8, fakedata.GenerateProducts(rows), func(row fakedata.ProductRow) []any {
			return []any{row.SKU, row.Name, row.Category, row.Brand, row.Price, row.Stock, row.Status, row.CreatedAt}
		}, []string{"sku", "name", "category", "brand", "price", "stock", "status", "created_at"})
	default:
		return fmt.Errorf("unsupported seed template %q", template)
	}
}

func bulkInsertMySQL[T any](ctx context.Context, tx *sql.Tx, tableName string, fieldCount int, rows []T, mapper func(T) []any, columns []string) error {
	const batchSize = 500
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		placeholders := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*fieldCount)
		for _, row := range rows[start:end] {
			placeholders = append(placeholders, "("+strings.TrimRight(strings.Repeat("?,", fieldCount), ",")+")")
			args = append(args, mapper(row)...)
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", QuoteIdent(tableName), strings.Join(columns, ", "), strings.Join(placeholders, ","))
		if _, err := tx.ExecContext(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
