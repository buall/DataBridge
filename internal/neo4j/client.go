package neo4j

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"databridge/internal/dbtls"
	"databridge/internal/fakedata"
	"databridge/internal/model"
	driver "github.com/neo4j/neo4j-go-driver/v6/neo4j"
	"github.com/neo4j/neo4j-go-driver/v6/neo4j/config"
)

func Test(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	start := time.Now()
	drv, err := connect(input.Host, input.Port, input.Username, input.Password, input.SSLMode, input.TLSConfig)
	if err != nil {
		return 0, "", err
	}
	defer drv.Close(ctx)

	if err := drv.VerifyConnectivity(ctx); err != nil {
		return 0, "", err
	}
	version, err := serverVersion(ctx, drv)
	if err != nil {
		return 0, "", err
	}
	return time.Since(start), version, nil
}

func ServerVersion(ctx context.Context, connection model.Connection) (string, error) {
	drv, err := connect(connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return "", err
	}
	defer drv.Close(ctx)
	return serverVersion(ctx, drv)
}

func Databases(ctx context.Context, connection model.Connection) ([]model.DatabaseInfo, error) {
	drv, err := connect(connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return nil, err
	}
	defer drv.Close(ctx)

	items, err := driver.ExecuteQuery[*driver.EagerResult](
		ctx,
		drv,
		`SHOW DATABASES YIELD name, currentStatus
		 WHERE currentStatus = 'online'
		 RETURN name
		 ORDER BY name`,
		nil,
		driver.EagerResultTransformer,
		driver.ExecuteQueryWithReadersRouting(),
		driver.ExecuteQueryWithDatabase("system"),
	)
	if err != nil {
		return nil, err
	}

	result := make([]model.DatabaseInfo, 0, len(items.Records))
	for _, record := range items.Records {
		name, isNil, err := driver.GetRecordValue[string](record, "name")
		if err != nil {
			return nil, fmt.Errorf("decode database name: %w", err)
		}
		if isNil {
			return nil, fmt.Errorf("decode database name: empty value")
		}
		info := model.DatabaseInfo{Name: name, Size: 0}
		if name != "system" {
			nodes, relationships, err := databaseCounts(ctx, drv, name)
			if err != nil {
				return nil, fmt.Errorf("count neo4j database %s: %w", name, err)
			}
			info.Nodes = nodes
			info.Relationships = relationships
		}
		result = append(result, info)
	}
	return result, nil
}

func CreateDatabase(ctx context.Context, connection model.Connection, name string) error {
	drv, err := connect(connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return err
	}
	defer drv.Close(ctx)
	_, err = driver.ExecuteQuery[*driver.EagerResult](
		ctx,
		drv,
		"CREATE DATABASE "+quoteName(name)+" IF NOT EXISTS",
		nil,
		driver.EagerResultTransformer,
		driver.ExecuteQueryWithDatabase("system"),
	)
	return err
}

func LabelsAndRelationshipTypes(ctx context.Context, connection model.Connection, database string) ([]model.TableInfo, error) {
	drv, err := connect(connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return nil, err
	}
	defer drv.Close(ctx)

	labels, err := driver.ExecuteQuery[*driver.EagerResult](
		ctx,
		drv,
		`CALL db.labels() YIELD label
		 RETURN label
		 ORDER BY label`,
		nil,
		driver.EagerResultTransformer,
		driver.ExecuteQueryWithReadersRouting(),
		driver.ExecuteQueryWithDatabase(database),
	)
	if err != nil {
		return nil, err
	}

	relationshipTypes, err := driver.ExecuteQuery[*driver.EagerResult](
		ctx,
		drv,
		`CALL db.relationshipTypes() YIELD relationshipType
		 RETURN relationshipType
		 ORDER BY relationshipType`,
		nil,
		driver.EagerResultTransformer,
		driver.ExecuteQueryWithReadersRouting(),
		driver.ExecuteQueryWithDatabase(database),
	)
	if err != nil {
		return nil, err
	}

	result := make([]model.TableInfo, 0, len(labels.Records)+len(relationshipTypes.Records))
	for _, record := range labels.Records {
		label, isNil, err := driver.GetRecordValue[string](record, "label")
		if err != nil {
			return nil, fmt.Errorf("decode label: %w", err)
		}
		if isNil {
			return nil, fmt.Errorf("decode label: empty value")
		}
		result = append(result, model.TableInfo{
			ID:        "label:" + label,
			Schema:    database,
			Name:      label,
			SizeBytes: 0,
			Rows:      0,
		})
	}
	for _, record := range relationshipTypes.Records {
		relationshipType, isNil, err := driver.GetRecordValue[string](record, "relationshipType")
		if err != nil {
			return nil, fmt.Errorf("decode relationship type: %w", err)
		}
		if isNil {
			return nil, fmt.Errorf("decode relationship type: empty value")
		}
		result = append(result, model.TableInfo{
			ID:        "relationship:" + relationshipType,
			Schema:    database,
			Name:      relationshipType,
			SizeBytes: 0,
			Rows:      0,
		})
	}
	return result, nil
}

func Counts(ctx context.Context, connection model.Connection, database string) (int64, int64, error) {
	drv, err := connect(connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return 0, 0, err
	}
	defer drv.Close(ctx)
	return databaseCounts(ctx, drv, database)
}

func SeedData(ctx context.Context, connection model.Connection, database string, rows int, mode, template string) (model.SeedDataResult, error) {
	if rows <= 0 {
		rows = 100
	}
	if template == "" {
		template = "users"
	}
	label, err := seedLabel(template)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	drv, err := connect(connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	defer drv.Close(ctx)

	if mode == "replace" {
		if _, err := driver.ExecuteQuery[*driver.EagerResult](
			ctx,
			drv,
			"MATCH (n:"+label+") DETACH DELETE n",
			nil,
			driver.EagerResultTransformer,
			driver.ExecuteQueryWithDatabase(database),
		); err != nil {
			return model.SeedDataResult{}, err
		}
	}
	if err := insertSeedRows(ctx, drv, database, template, label, rows); err != nil {
		return model.SeedDataResult{}, err
	}
	return model.SeedDataResult{Database: database, Table: label, Rows: rows, Mode: mode, Template: template}, nil
}

func connect(host string, port int, username, password, sslMode string, tlsConfig model.ConnectionTLSConfig) (driver.Driver, error) {
	var configurers []func(*config.Config)
	if strings.EqualFold(strings.TrimSpace(sslMode), "verify-full") {
		customTLSConfig, err := dbtls.Config(host, tlsConfig, true)
		if err != nil {
			return nil, err
		}
		if customTLSConfig != nil {
			configurers = append(configurers, func(config *config.Config) {
				config.TlsConfig = customTLSConfig
			})
		}
	}
	drv, err := driver.NewDriver(uri(host, port, sslMode), driver.BasicAuth(username, password, ""), configurers...)
	if err != nil {
		return nil, err
	}
	return drv, nil
}

func seedLabel(template string) (string, error) {
	switch template {
	case "users":
		return "DatabridgeDemoUser", nil
	case "orders":
		return "DatabridgeDemoOrder", nil
	case "products":
		return "DatabridgeDemoProduct", nil
	default:
		return "", fmt.Errorf("unsupported seed template %q", template)
	}
}

func databaseCounts(ctx context.Context, drv driver.Driver, database string) (int64, int64, error) {
	result, err := driver.ExecuteQuery[*driver.EagerResult](
		ctx,
		drv,
		`MATCH (n)
		 WITH count(n) AS nodes
		 OPTIONAL MATCH ()-[r]->()
		 RETURN nodes, count(r) AS relationships`,
		nil,
		driver.EagerResultTransformer,
		driver.ExecuteQueryWithReadersRouting(),
		driver.ExecuteQueryWithDatabase(database),
	)
	if err != nil {
		return 0, 0, err
	}
	if len(result.Records) == 0 {
		return 0, 0, nil
	}
	nodes, _, err := driver.GetRecordValue[int64](result.Records[0], "nodes")
	if err != nil {
		return 0, 0, err
	}
	relationships, _, err := driver.GetRecordValue[int64](result.Records[0], "relationships")
	if err != nil {
		return 0, 0, err
	}
	return nodes, relationships, nil
}

func insertSeedRows(ctx context.Context, drv driver.Driver, database, template, label string, rows int) error {
	switch template {
	case "users":
		items := fakedata.GenerateUsers(rows)
		payload := make([]map[string]any, 0, len(items))
		for _, row := range items {
			payload = append(payload, map[string]any{
				"email": row.Email, "full_name": row.FullName, "city": row.City,
				"company": row.Company, "job_title": row.JobTitle, "status": row.Status,
				"score": row.Score, "phone": row.Phone, "created_at": row.CreatedAt,
			})
		}
		return insertNodes(ctx, drv, database, label, payload)
	case "orders":
		items := fakedata.GenerateOrders(rows)
		payload := make([]map[string]any, 0, len(items))
		for _, row := range items {
			payload = append(payload, map[string]any{
				"order_no": row.OrderNo, "customer_name": row.Customer, "city": row.City,
				"status": row.Status, "channel": row.Channel, "amount": row.Amount,
				"created_at": row.CreatedAt,
			})
		}
		return insertNodes(ctx, drv, database, label, payload)
	case "products":
		items := fakedata.GenerateProducts(rows)
		payload := make([]map[string]any, 0, len(items))
		for _, row := range items {
			payload = append(payload, map[string]any{
				"sku": row.SKU, "name": row.Name, "category": row.Category,
				"brand": row.Brand, "price": row.Price, "stock": row.Stock,
				"status": row.Status, "created_at": row.CreatedAt,
			})
		}
		return insertNodes(ctx, drv, database, label, payload)
	default:
		return fmt.Errorf("unsupported seed template %q", template)
	}
}

func insertNodes(ctx context.Context, drv driver.Driver, database, label string, rows []map[string]any) error {
	const batchSize = 500
	for start := 0; start < len(rows); start += batchSize {
		end := start + batchSize
		if end > len(rows) {
			end = len(rows)
		}
		if _, err := driver.ExecuteQuery[*driver.EagerResult](
			ctx,
			drv,
			"UNWIND $rows AS row CREATE (n:"+label+") SET n = row",
			map[string]any{"rows": rows[start:end]},
			driver.EagerResultTransformer,
			driver.ExecuteQueryWithDatabase(database),
		); err != nil {
			return err
		}
	}
	return nil
}

func serverVersion(ctx context.Context, drv driver.Driver) (string, error) {
	result, err := driver.ExecuteQuery[*driver.EagerResult](
		ctx,
		drv,
		`CALL dbms.components() YIELD versions
		 RETURN versions[0] AS version
		 LIMIT 1`,
		nil,
		driver.EagerResultTransformer,
		driver.ExecuteQueryWithReadersRouting(),
	)
	if err != nil {
		return "", err
	}
	if len(result.Records) == 0 {
		return "", fmt.Errorf("neo4j server version not found")
	}
	version, isNil, err := driver.GetRecordValue[string](result.Records[0], "version")
	if err != nil {
		return "", fmt.Errorf("decode server version: %w", err)
	}
	if isNil {
		return "", fmt.Errorf("decode server version: empty value")
	}
	return version, nil
}

func uri(host string, port int, sslMode string) string {
	if port == 0 {
		port = 7687
	}
	scheme := "bolt"
	switch strings.ToLower(strings.TrimSpace(sslMode)) {
	case "prefer":
		scheme = "bolt"
	case "require":
		scheme = "bolt+ssc"
	case "verify-full":
		scheme = "bolt+s"
	case "disable":
		scheme = "bolt"
	default:
		scheme = "bolt"
	}
	return (&url.URL{
		Scheme: scheme,
		Host:   host + ":" + strconv.Itoa(port),
	}).String()
}

func quoteName(value string) string {
	return "`" + strings.ReplaceAll(value, "`", "``") + "`"
}
