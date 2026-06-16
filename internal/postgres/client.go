package postgres

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"databridge/internal/fakedata"
	"databridge/internal/model"
	"github.com/jackc/pgx/v5"
)

func Test(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	start := time.Now()
	conn, err := pgx.Connect(ctx, DSN(input.Host, input.Port, input.Username, input.Password, "postgres", input.SSLMode))
	if err != nil {
		return 0, "", err
	}
	defer conn.Close(ctx)
	if err := conn.Ping(ctx); err != nil {
		return 0, "", err
	}
	var version string
	if err := conn.QueryRow(ctx, `SHOW server_version`).Scan(&version); err != nil {
		return 0, "", err
	}
	return time.Since(start), version, nil
}

func Connect(ctx context.Context, connection model.Connection, database string) (*pgx.Conn, error) {
	return pgx.Connect(ctx, DSN(connection.Host, connection.Port, connection.Username, connection.Password, database, connection.SSLMode))
}

func ServerVersion(ctx context.Context, connection model.Connection) (string, error) {
	conn, err := Connect(ctx, connection, "postgres")
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	var version string
	if err := conn.QueryRow(ctx, `SHOW server_version`).Scan(&version); err != nil {
		return "", err
	}
	return version, nil
}

func DSN(host string, port int, username, password, database, sslMode string) string {
	if port == 0 {
		port = 5432
	}
	if sslMode == "" {
		sslMode = "prefer"
	}
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(username, password),
		Host:   host + ":" + strconv.Itoa(port),
		Path:   database,
	}
	query := u.Query()
	query.Set("sslmode", sslMode)
	query.Set("connect_timeout", "8")
	u.RawQuery = query.Encode()
	return u.String()
}

func Databases(ctx context.Context, connection model.Connection) ([]model.DatabaseInfo, error) {
	conn, err := Connect(ctx, connection, "postgres")
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `
		SELECT datname, pg_database_size(datname)
		FROM pg_database
		WHERE datallowconn AND NOT datistemplate
		ORDER BY datname`)
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
	return result, rows.Err()
}

func CreateDatabase(ctx context.Context, connection model.Connection, name string) error {
	conn, err := Connect(ctx, connection, "postgres")
	if err != nil {
		return err
	}
	defer conn.Close(ctx)
	_, err = conn.Exec(ctx, "CREATE DATABASE "+QuoteIdent(name))
	return err
}

func Tables(ctx context.Context, connection model.Connection, database string) ([]model.TableInfo, error) {
	conn, err := Connect(ctx, connection, database)
	if err != nil {
		return nil, err
	}
	defer conn.Close(ctx)
	rows, err := conn.Query(ctx, `
		SELECT n.nspname, c.relname, pg_total_relation_size(c.oid),
		       GREATEST(c.reltuples::bigint, 0)
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
	var result []model.TableInfo
	for rows.Next() {
		var item model.TableInfo
		if err := rows.Scan(&item.Schema, &item.Name, &item.SizeBytes, &item.Rows); err != nil {
			return nil, err
		}
		item.ID = fmt.Sprintf("%s.%s", item.Schema, item.Name)
		result = append(result, item)
	}
	return result, rows.Err()
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
	defer conn.Close(ctx)

	tableName, createSQL, err := seedTableDefinitionPostgres(template)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	if _, err := conn.Exec(ctx, createSQL); err != nil {
		return model.SeedDataResult{}, err
	}
	if mode == "replace" {
		if _, err := conn.Exec(ctx, "TRUNCATE TABLE "+tableName+" RESTART IDENTITY"); err != nil {
			return model.SeedDataResult{}, err
		}
	}

	if err := insertSeedRowsPostgres(ctx, conn, tableName, template, rows); err != nil {
		return model.SeedDataResult{}, err
	}

	return model.SeedDataResult{Database: database, Table: tableName, Rows: rows, Mode: mode, Template: template}, nil
}

func seedTableDefinitionPostgres(template string) (string, string, error) {
	switch template {
	case "users":
		return "public.databridge_demo_users", `
			CREATE TABLE IF NOT EXISTS public.databridge_demo_users (
				id BIGSERIAL PRIMARY KEY,
				email TEXT NOT NULL,
				full_name TEXT NOT NULL,
				city TEXT NOT NULL,
				company TEXT NOT NULL,
				job_title TEXT NOT NULL,
				status TEXT NOT NULL,
				score INTEGER NOT NULL,
				phone TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`, nil
	case "orders":
		return "public.databridge_demo_orders", `
			CREATE TABLE IF NOT EXISTS public.databridge_demo_orders (
				id BIGSERIAL PRIMARY KEY,
				order_no TEXT NOT NULL,
				customer_name TEXT NOT NULL,
				city TEXT NOT NULL,
				status TEXT NOT NULL,
				channel TEXT NOT NULL,
				amount NUMERIC(10,2) NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`, nil
	case "products":
		return "public.databridge_demo_products", `
			CREATE TABLE IF NOT EXISTS public.databridge_demo_products (
				id BIGSERIAL PRIMARY KEY,
				sku TEXT NOT NULL,
				name TEXT NOT NULL,
				category TEXT NOT NULL,
				brand TEXT NOT NULL,
				price NUMERIC(10,2) NOT NULL,
				stock INTEGER NOT NULL,
				status TEXT NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
			)`, nil
	default:
		return "", "", fmt.Errorf("unsupported seed template %q", template)
	}
}

func insertSeedRowsPostgres(ctx context.Context, conn *pgx.Conn, tableName, template string, rows int) error {
	switch template {
	case "users":
		return bulkInsertPostgres(ctx, conn, tableName, 9, fakedata.GenerateUsers(rows), func(row fakedata.UserRow) []any {
			return []any{row.Email, row.FullName, row.City, row.Company, row.JobTitle, row.Status, row.Score, row.Phone, row.CreatedAt}
		}, []string{"email", "full_name", "city", "company", "job_title", "status", "score", "phone", "created_at"})
	case "orders":
		return bulkInsertPostgres(ctx, conn, tableName, 7, fakedata.GenerateOrders(rows), func(row fakedata.OrderRow) []any {
			return []any{row.OrderNo, row.Customer, row.City, row.Status, row.Channel, row.Amount, row.CreatedAt}
		}, []string{"order_no", "customer_name", "city", "status", "channel", "amount", "created_at"})
	case "products":
		return bulkInsertPostgres(ctx, conn, tableName, 8, fakedata.GenerateProducts(rows), func(row fakedata.ProductRow) []any {
			return []any{row.SKU, row.Name, row.Category, row.Brand, row.Price, row.Stock, row.Status, row.CreatedAt}
		}, []string{"sku", "name", "category", "brand", "price", "stock", "status", "created_at"})
	default:
		return fmt.Errorf("unsupported seed template %q", template)
	}
}

func bulkInsertPostgres[T any](ctx context.Context, conn *pgx.Conn, tableName string, fieldCount int, rows []T, mapper func(T) []any, columns []string) error {
	const batchSize = 500
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		placeholders := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*fieldCount)
		argIndex := 1
		for _, row := range rows[start:end] {
			parts := make([]string, 0, fieldCount)
			for i := 0; i < fieldCount; i++ {
				parts = append(parts, fmt.Sprintf("$%d", argIndex))
				argIndex++
			}
			placeholders = append(placeholders, "("+strings.Join(parts, ",")+")")
			args = append(args, mapper(row)...)
		}
		query := fmt.Sprintf("INSERT INTO %s (%s) VALUES %s", tableName, strings.Join(columns, ", "), strings.Join(placeholders, ","))
		if _, err := conn.Exec(ctx, query, args...); err != nil {
			return err
		}
	}
	return nil
}
