package internal

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// Metadata captures minimal object metadata persisted to DynamoDB for
// observability and traceability of artifacts written to S3.
type Metadata struct {
	ID        string `dynamodbav:"id"`
	S3Key     string `dynamodbav:"s3_key"`
	SizeBytes int    `dynamodbav:"size_bytes"`
	Timestamp string `dynamodbav:"timestamp"`
}

// AlertTrackerItem represents a single alert record stored in DynamoDB.
// Table name defaults to "alert-tracker"; override with ALERT_TRACKER_TABLE.
type AlertTrackerItem struct {
	CreatedOnMs   int64    `dynamodbav:"createdon" json:"createdon_ms"`
	AlertID       string   `dynamodbav:"alert_id" json:"alert_id"`
	AlertName     string   `dynamodbav:"alert_name" json:"alert_name"`
	SignedURL     string   `dynamodbav:"s3_signed_url" json:"s3_signed_url"`
	Severity      string   `dynamodbav:"severity" json:"severity"`
	SitesImpacted []string `dynamodbav:"sites_impacted" json:"sites_impacted"`
	AnomalyDate   string   `dynamodbav:"anomaly_date" json:"anomaly_date"`
}

// SaveMetadata persists a small metadata record for an S3 object to DynamoDB.
func SaveMetadata(ctx context.Context, s3Key string, size int) error {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)
	table := os.Getenv("DDB_TABLE")
	item := Metadata{
		ID:        fmt.Sprintf("data-%d", time.Now().UnixNano()),
		S3Key:     s3Key,
		SizeBytes: size,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &table,
		Item:      av,
	})
	return err
}

// SaveAlertTrackerItem writes an alert record to the alert-tracker table.
func SaveAlertTrackerItem(ctx context.Context, item AlertTrackerItem) error {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)
	table := os.Getenv("ALERT_TRACKER_TABLE")
	if table == "" {
		table = "alert-tracker"
	}
	av, err := attributevalue.MarshalMap(item)
	if err != nil {
		return err
	}
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &table,
		Item:      av,
	})
	return err
}

// SaveAlertTrackerRecord writes a generic alert record represented as a map.
func SaveAlertTrackerRecord(ctx context.Context, record map[string]any) error {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)
	table := os.Getenv("ALERT_TRACKER_TABLE")
	if table == "" {
		table = "alert-tracker"
	}
	av, err := attributevalue.MarshalMap(record)
	if err != nil {
		return err
	}
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &table,
		Item:      av,
	})
	return err
}

// ListRecentAlerts queries the GSI gsi_recent (HASH gsi_pk='recent', RANGE createdon) for items since a timestamp.
func ListRecentAlerts(ctx context.Context, sinceEpochMs int64, limit int) ([]AlertTrackerItem, error) {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)
	table := os.Getenv("ALERT_TRACKER_TABLE")
	if table == "" {
		table = "alert-tracker"
	}
	if limit <= 0 {
		limit = 100
	}
	index := "gsi_recent"
	values, err := attributevalue.MarshalMap(map[string]any{
		":pk":    "recent",
		":since": sinceEpochMs,
	})
	if err != nil {
		return nil, err
	}
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &table,
		IndexName:                 &index,
		KeyConditionExpression:    awsString("gsi_pk = :pk AND createdon >= :since"),
		ExpressionAttributeValues: values,
		ScanIndexForward:          awsBool(false),
		Limit:                     awsInt32(int32(limit)),
	})
	if err != nil {
		return nil, err
	}
	var items []AlertTrackerItem
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &items); err != nil {
		return nil, err
	}
	// Defensive: ensure descending
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedOnMs > items[j].CreatedOnMs })
	return items, nil
}

func awsString(s string) *string { return &s }
func awsInt32(v int32) *int32    { return &v }
func awsBool(b bool) *bool       { return &b }

// -------------------- Train Model Tracker --------------------

// TrainModelTrackerItem represents a training job record.
// Table name defaults to "train-model-tracker"; override with TRAIN_MODEL_TRACKER_TABLE.
type TrainModelTrackerItem struct {
	UUID      string   `dynamodbav:"uuid" json:"uuid"`
	CreatedOn int64    `dynamodbav:"createdon" json:"createdon"`
	Sites     []string `dynamodbav:"sites" json:"sites"`
}

// SaveTrainModelTrackerItem writes a record to the train-model-tracker table.
func SaveTrainModelTrackerItem(ctx context.Context, item TrainModelTrackerItem) error {
	if item.UUID == "" {
		return fmt.Errorf("uuid is required")
	}
	if item.CreatedOn == 0 {
		item.CreatedOn = time.Now().UTC().UnixMilli()
	}
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)
	table := os.Getenv("TRAIN_MODEL_TRACKER_TABLE")
	if table == "" {
		table = "train-model-tracker"
	}
	// Add GSI partition key for recent queries
	record := map[string]any{
		"uuid":      item.UUID,
		"createdon": item.CreatedOn,
		"sites":     item.Sites,
		"gsi_pk":    "recent",
	}
	av, err := attributevalue.MarshalMap(record)
	if err != nil {
		return err
	}
	_, err = client.PutItem(ctx, &dynamodb.PutItemInput{
		TableName: &table,
		Item:      av,
	})
	return err
}

// ListRecentTrainModels queries gsi_recent to get items since a timestamp in descending order of createdon.
func ListRecentTrainModels(ctx context.Context, sinceEpochMs int64, limit int) ([]TrainModelTrackerItem, error) {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)
	table := os.Getenv("TRAIN_MODEL_TRACKER_TABLE")
	if table == "" {
		table = "train-model-tracker"
	}
	if limit <= 0 {
		limit = 100
	}
	index := "gsi_recent"
	values, err := attributevalue.MarshalMap(map[string]any{
		":pk":    "recent",
		":since": sinceEpochMs,
	})
	if err != nil {
		return nil, err
	}
	out, err := client.Query(ctx, &dynamodb.QueryInput{
		TableName:                 &table,
		IndexName:                 &index,
		KeyConditionExpression:    awsString("gsi_pk = :pk AND createdon >= :since"),
		ExpressionAttributeValues: values,
		ScanIndexForward:          awsBool(false),
		Limit:                     awsInt32(int32(limit)),
	})
	if err != nil {
		return nil, err
	}
	var items []TrainModelTrackerItem
	if err := attributevalue.UnmarshalListOfMaps(out.Items, &items); err != nil {
		return nil, err
	}
	sort.Slice(items, func(i, j int) bool { return items[i].CreatedOn > items[j].CreatedOn })
	return items, nil
}
