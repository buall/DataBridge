package mongo

import (
	"context"
	cryptorand "crypto/rand"
	"fmt"
	"io"
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

const (
	payloadDocumentBytes = 2048
	payloadInsertBatch   = 500
)

type OplogBackupInfo struct {
	ReplicaSetName string
	Oldest         model.MongoOplogTimestamp
	Newest         model.MongoOplogTimestamp
}

// IncrementalBackupInfo verifies that the connection is a replica-set member
// and returns the inclusive oplog window available for a backup chain.
func IncrementalBackupInfo(ctx context.Context, connection model.Connection) (OplogBackupInfo, error) {
	client, err := connect(ctx, connection)
	if err != nil {
		return OplogBackupInfo{}, err
	}
	defer disconnect(context.Background(), client)

	var hello struct {
		SetName string `bson:"setName"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello); err != nil {
		return OplogBackupInfo{}, err
	}
	if strings.TrimSpace(hello.SetName) == "" {
		return OplogBackupInfo{}, fmt.Errorf("源端不是 MongoDB 副本集，无法使用 oplog 增量备份")
	}
	type oplogEntry struct {
		Timestamp bson.Timestamp `bson:"ts"`
	}
	oplog := client.Database("local").Collection("oplog.rs")
	var oldest, newest oplogEntry
	if err := oplog.FindOne(ctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "$natural", Value: 1}})).Decode(&oldest); err != nil {
		return OplogBackupInfo{}, fmt.Errorf("读取 oplog 起始位点失败，请确认账号可读取 local.oplog.rs：%w", err)
	}
	if err := oplog.FindOne(ctx, bson.D{}, options.FindOne().SetSort(bson.D{{Key: "$natural", Value: -1}})).Decode(&newest); err != nil {
		return OplogBackupInfo{}, fmt.Errorf("读取 oplog 最新位点失败，请确认账号可读取 local.oplog.rs：%w", err)
	}
	return OplogBackupInfo{
		ReplicaSetName: hello.SetName,
		Oldest:         model.MongoOplogTimestamp{Seconds: oldest.Timestamp.T, Increment: oldest.Timestamp.I},
		Newest:         model.MongoOplogTimestamp{Seconds: newest.Timestamp.T, Increment: newest.Timestamp.I},
	}, nil
}

func OplogTimestampCompare(left, right model.MongoOplogTimestamp) int {
	if left.Seconds != right.Seconds {
		if left.Seconds < right.Seconds {
			return -1
		}
		return 1
	}
	if left.Increment < right.Increment {
		return -1
	}
	if left.Increment > right.Increment {
		return 1
	}
	return 0
}

func Test(ctx context.Context, input model.ConnectionInput) (time.Duration, string, error) {
	start := time.Now()
	client, err := connect(ctx, connectionFromInput(input))
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
	client, err := connect(ctx, connection)
	if err != nil {
		return "", err
	}
	defer disconnect(context.Background(), client)
	return serverVersion(ctx, client)
}

func Databases(ctx context.Context, connection model.Connection) ([]model.DatabaseInfo, error) {
	client, err := connect(ctx, connection)
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

func connect(ctx context.Context, connection model.Connection) (*mongo.Client, error) {
	clientOptions := options.Client().ApplyURI(URIForConnection(connection))
	if strings.EqualFold(strings.TrimSpace(connection.SSLMode), "verify-full") {
		customTLSConfig, err := dbtls.Config(connection.Host, connection.TLSConfig, true)
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

func URIForConnection(connection model.Connection) string {
	u := &url.URL{
		Scheme: "mongodb",
		Host:   strings.Join(seedAddresses(connection.Host, connection.Port, connection.Endpoints), ","),
		Path:   "/",
	}
	if strings.TrimSpace(connection.Username) != "" {
		u.User = url.UserPassword(connection.Username, connection.Password)
	}
	query := u.Query()
	if len(connection.Endpoints) == 0 {
		query.Set("directConnection", "true")
	}
	query.Set("serverSelectionTimeoutMS", "8000")
	query.Set("connectTimeoutMS", "8000")
	switch strings.ToLower(strings.TrimSpace(connection.SSLMode)) {
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

// URIForMongoTools strips credentials from the driver-facing URI. Database
// tools receive credentials via dedicated flags so failures cannot echo a
// password into a job event.
func URIForMongoTools(connection model.Connection) string {
	value := URIForConnection(connection)
	u, err := url.Parse(value)
	if err != nil {
		return value
	}
	u.User = nil
	return u.String()
}

func MongoToolArgs(connection model.Connection) []string {
	args := []string{"--uri", URIForMongoTools(connection)}
	if strings.TrimSpace(connection.Username) != "" {
		args = append(args,
			"--username", connection.Username,
			"--password", connection.Password,
			"--authenticationDatabase", "admin",
			"--authenticationMechanism", "SCRAM-SHA-256",
		)
	}
	return args
}

// NormalizeReplicaSeeds validates seed nodes provided in replica-set mode.
// A non-empty seed list switches the driver from direct-node to topology mode.
func NormalizeReplicaSeeds(endpoints []string) ([]string, error) {
	seen := map[string]bool{}
	seeds := make([]string, 0, len(endpoints))
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" || seen[endpoint] {
			continue
		}
		if _, _, err := net.SplitHostPort(endpoint); err != nil {
			return nil, fmt.Errorf("MongoDB 副本集种子节点 %q 必须使用 主机:端口 格式", endpoint)
		}
		seen[endpoint] = true
		seeds = append(seeds, endpoint)
	}
	return seeds, nil
}

func seedAddresses(host string, port int, endpoints []string) []string {
	if port == 0 {
		port = 27017
	}
	seeds := []string{net.JoinHostPort(host, strconv.Itoa(port))}
	seen := map[string]bool{seeds[0]: true}
	for _, endpoint := range endpoints {
		endpoint = strings.TrimSpace(endpoint)
		if endpoint == "" || seen[endpoint] {
			continue
		}
		seen[endpoint] = true
		seeds = append(seeds, endpoint)
	}
	return seeds
}

func connectionFromInput(input model.ConnectionInput) model.Connection {
	return model.Connection{Host: input.Host, Port: input.Port, Username: input.Username, Password: input.Password, SSLMode: input.SSLMode, TLSConfig: input.TLSConfig, Endpoints: input.Endpoints}
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
	client, err := connect(ctx, connection)
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
	client, err := connect(ctx, connection)
	if err != nil {
		return model.SeedDataResult{}, err
	}
	defer disconnect(context.Background(), client)

	collectionName := "databridge_demo_payload"
	if template != "payload" {
		var documents []any
		collectionName, documents, err = seedDocuments(template, rows)
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
	collection := client.Database(database).Collection(collectionName)
	if mode == "replace" {
		if _, err := collection.DeleteMany(ctx, bson.D{}); err != nil {
			return model.SeedDataResult{}, err
		}
	}
	if err := insertHighEntropyPayloads(ctx, collection, rows); err != nil {
		return model.SeedDataResult{}, err
	}
	return model.SeedDataResult{Database: database, Table: collectionName, Rows: rows, Mode: mode, Template: template}, nil
}

func insertHighEntropyPayloads(ctx context.Context, collection *mongo.Collection, rows int) error {
	for offset := 0; offset < rows; offset += payloadInsertBatch {
		count := min(payloadInsertBatch, rows-offset)
		documents := make([]any, 0, count)
		for index := 0; index < count; index++ {
			payload := make([]byte, payloadDocumentBytes)
			if _, err := io.ReadFull(cryptorand.Reader, payload); err != nil {
				return fmt.Errorf("generate high-entropy payload: %w", err)
			}
			documents = append(documents, bson.M{
				"sequence":   offset + index,
				"payload":    payload,
				"created_at": time.Now().UTC(),
			})
		}
		if _, err := collection.InsertMany(ctx, documents); err != nil {
			return err
		}
	}
	return nil
}

func CollectionNames(ctx context.Context, connection model.Connection, database string) ([]string, error) {
	client, err := connect(ctx, connection)
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
	client, err := connect(ctx, connection)
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
