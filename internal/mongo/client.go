package mongo

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"time"

	"databridge/internal/dbtls"
	"databridge/internal/fakedata"
	"databridge/internal/model"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
)

const initCollectionName = "databridge_init"

func Test(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	start := time.Now()
	client, err := connect(ctx, input.Host, input.Port, input.Username, input.Password, input.SSLMode, input.TLSConfig)
	if err != nil {
		return 0, "", err
	}
	defer disconnect(context.Background(), client)
	version, err := serverVersion(ctx, client)
	if err != nil {
		return 0, "", err
	}
	return time.Since(start), version, nil
}

func ServerVersion(ctx context.Context, connection model.Connection) (string, error) {
	client, err := connect(ctx, connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return "", err
	}
	defer disconnect(context.Background(), client)
	return serverVersion(ctx, client)
}

func Databases(ctx context.Context, connection model.Connection) ([]model.DatabaseInfo, error) {
	client, err := connect(ctx, connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return nil, err
	}
	defer disconnect(context.Background(), client)

	result, err := client.ListDatabases(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	items := make([]model.DatabaseInfo, 0, len(result.Databases))
	for _, item := range result.Databases {
		if isSystemDatabase(item.Name) {
			continue
		}
		items = append(items, model.DatabaseInfo{
			Name: item.Name,
			Size: int64(item.SizeOnDisk),
		})
	}
	return items, nil
}

func connect(ctx context.Context, host string, port int, username, password, sslMode string, tlsConfig model.ConnectionTLSConfig) (*mongo.Client, error) {
	clientOptions := options.Client().ApplyURI(uri(host, port, username, password, sslMode))
	if strings.EqualFold(strings.TrimSpace(sslMode), "verify-full") {
		customTLSConfig, err := dbtls.Config(host, tlsConfig, true)
		if err != nil {
			return nil, err
		}
		if customTLSConfig != nil {
			clientOptions.SetTLSConfig(customTLSConfig)
		}
	}
	client, err := mongo.Connect(clientOptions)
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, readpref.Primary()); err != nil {
		_ = disconnect(context.Background(), client)
		return nil, err
	}
	return client, nil
}

func disconnect(ctx context.Context, client *mongo.Client) error {
	if client == nil {
		return nil
	}
	return client.Disconnect(ctx)
}

func serverVersion(ctx context.Context, client *mongo.Client) (string, error) {
	var buildInfo struct {
		Version string `bson:"version"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&buildInfo); err != nil {
		return "", err
	}
	return buildInfo.Version, nil
}

func uri(host string, port int, username, password, sslMode string) string {
	if port == 0 {
		port = 27017
	}
	u := &url.URL{
		Scheme: "mongodb",
		Host:   net.JoinHostPort(host, strconv.Itoa(port)),
		Path:   "/",
	}
	if strings.TrimSpace(username) != "" {
		u.User = url.UserPassword(username, password)
	}
	query := u.Query()
	query.Set("directConnection", "true")
	query.Set("serverSelectionTimeoutMS", "8000")
	query.Set("connectTimeoutMS", "8000")
	switch strings.ToLower(strings.TrimSpace(sslMode)) {
	case "require", "verify-full":
		query.Set("tls", "true")
	case "disable":
		query.Set("tls", "false")
	default:
		query.Set("tls", "false")
	}
	u.RawQuery = query.Encode()
	return u.String()
}

func isSystemDatabase(name string) bool {
	switch strings.TrimSpace(name) {
	case "admin", "config", "local":
		return true
	default:
		return false
	}
}

func CreateDatabase(ctx context.Context, connection model.Connection, name string) error {
	client, err := connect(ctx, connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return err
	}
	defer disconnect(context.Background(), client)

	database := client.Database(name)
	collections, err := database.ListCollectionNames(ctx, bson.D{{Key: "name", Value: initCollectionName}})
	if err != nil {
		return err
	}
	if len(collections) == 0 {
		if err := database.CreateCollection(ctx, initCollectionName); err != nil && !strings.Contains(strings.ToLower(err.Error()), "namespaceexists") {
			return err
		}
	}
	return nil
}

func SeedData(ctx context.Context, connection model.Connection, database string, rows int, mode, template string) (model.SeedDataResult, error) {
	if rows <= 0 {
		rows = 100
	}
	if template == "" {
		template = "users"
	}
	client, err := connect(ctx, connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	defer disconnect(context.Background(), client)

	collectionName, documents, err := seedDocuments(template, rows)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	collection := client.Database(database).Collection(collectionName)
	if mode == "replace" {
		if _, err := collection.DeleteMany(ctx, bson.D{}); err != nil {
			return model.SeedDataResult{}, err
		}
	}
	if len(documents) > 0 {
		if _, err := collection.InsertMany(ctx, documents); err != nil {
			return model.SeedDataResult{}, err
		}
	}
	return model.SeedDataResult{Database: database, Table: collectionName, Rows: rows, Mode: mode, Template: template}, nil
}

func CollectionNames(ctx context.Context, connection model.Connection, database string) ([]string, error) {
	client, err := connect(ctx, connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return nil, err
	}
	defer disconnect(context.Background(), client)
	items, err := client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, err
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		if isSystemCollection(item) {
			continue
		}
		result = append(result, item)
	}
	return result, nil
}

func DropDatabase(ctx context.Context, connection model.Connection, database string) error {
	client, err := connect(ctx, connection.Host, connection.Port, connection.Username, connection.Password, connection.SSLMode, connection.TLSConfig)
	if err != nil {
		return err
	}
	defer disconnect(context.Background(), client)
	return client.Database(database).Drop(ctx)
}

func seedDocuments(template string, rows int) (string, []any, error) {
	switch template {
	case "users":
		items := fakedata.GenerateUsers(rows)
		documents := make([]any, 0, len(items))
		for _, row := range items {
			documents = append(documents, bson.M{
				"email":      row.Email,
				"full_name":  row.FullName,
				"city":       row.City,
				"company":    row.Company,
				"job_title":  row.JobTitle,
				"status":     row.Status,
				"score":      row.Score,
				"phone":      row.Phone,
				"created_at": row.CreatedAt,
			})
		}
		return "databridge_demo_users", documents, nil
	case "orders":
		items := fakedata.GenerateOrders(rows)
		documents := make([]any, 0, len(items))
		for _, row := range items {
			documents = append(documents, bson.M{
				"order_no":      row.OrderNo,
				"customer_name": row.Customer,
				"city":          row.City,
				"status":        row.Status,
				"channel":       row.Channel,
				"amount":        row.Amount,
				"created_at":    row.CreatedAt,
			})
		}
		return "databridge_demo_orders", documents, nil
	case "products":
		items := fakedata.GenerateProducts(rows)
		documents := make([]any, 0, len(items))
		for _, row := range items {
			documents = append(documents, bson.M{
				"sku":        row.SKU,
				"name":       row.Name,
				"category":   row.Category,
				"brand":      row.Brand,
				"price":      row.Price,
				"stock":      row.Stock,
				"status":     row.Status,
				"created_at": row.CreatedAt,
			})
		}
		return "databridge_demo_products", documents, nil
	default:
		return "", nil, fmt.Errorf("unsupported seed template %q", template)
	}
}

func isSystemCollection(name string) bool {
	value := strings.TrimSpace(name)
	return value == "" || strings.HasPrefix(value, "system.")
}
