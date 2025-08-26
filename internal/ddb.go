package internal

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb/types"
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

// ListRecentAlerts returns at most 'limit' alerts created on or after sinceEpochMs.
// Uses a Scan with FilterExpression because the table's HASH key is the timestamp itself.
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
	exprValues, err := attributevalue.MarshalMap(map[string]int64{":since": sinceEpochMs})
	if err != nil {
		return nil, err
	}
	var items []AlertTrackerItem
	var lastKey map[string]types.AttributeValue
	for {
		out, err := client.Scan(ctx, &dynamodb.ScanInput{
			TableName:                 &table,
			FilterExpression:          awsString("createdon >= :since"),
			ExpressionAttributeValues: exprValues,
			ExclusiveStartKey:         lastKey,
			Limit:                     awsInt32(int32(limit - len(items))),
		})
		if err != nil {
			return nil, err
		}
		var batch []AlertTrackerItem
		if err := attributevalue.UnmarshalListOfMaps(out.Items, &batch); err != nil {
			return nil, err
		}
		items = append(items, batch...)
		if len(items) >= limit || out.LastEvaluatedKey == nil || len(out.LastEvaluatedKey) == 0 {
			break
		}
		lastKey = out.LastEvaluatedKey
	}
	return items, nil
}

func awsString(s string) *string { return &s }
func awsInt32(v int32) *int32    { return &v }
