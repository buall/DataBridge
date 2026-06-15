package postgres

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

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
