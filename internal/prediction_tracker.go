package internal

import (
	"context"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/feature/dynamodb/attributevalue"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// PredictionTrackerItem represents an item in the prediction-tracker DynamoDB table.
// Primary key: site (HASH), status (RANGE).
type PredictionTrackerItem struct {
	Site      string `dynamodbav:"site"`
	Status    string `dynamodbav:"status"`
	CreatedOn int64  `dynamodbav:"createdon"`
	UpdatedOn int64  `dynamodbav:"updatedon"`
}

// AddPredictionTrackerStarted inserts a new entry into the prediction-tracker table
// with status set to "started" and both createdon/updatedon set to the current
// epoch time in milliseconds.
//
// The table name can be overridden with PREDICTION_TRACKER_TABLE env var;
// defaults to "prediction-tracker".
func AddPredictionTrackerStarted(ctx context.Context, site string) error {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)

	table := os.Getenv("PREDICTION_TRACKER_TABLE")
	if table == "" {
		table = "prediction-tracker"
	}

	nowEpochMs := time.Now().UTC().UnixMilli()
	item := PredictionTrackerItem{
		Site:      site,
		Status:    "started",
		CreatedOn: nowEpochMs,
		UpdatedOn: nowEpochMs,
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

// GetPredictionTrackerItem fetches a prediction-tracker record by site and status.
// Returns (nil, nil) if no such item exists.
func GetPredictionTrackerItem(ctx context.Context, site, status string) (*PredictionTrackerItem, error) {
	cfg := getAWSConfig()
	client := dynamodb.NewFromConfig(cfg)

	table := os.Getenv("PREDICTION_TRACKER_TABLE")
	if table == "" {
		table = "prediction-tracker"
	}

	// Build the key for GetItem
	key, err := attributevalue.MarshalMap(struct {
		Site   string `dynamodbav:"site"`
		Status string `dynamodbav:"status"`
	}{
		Site:   site,
		Status: status,
	})
	if err != nil {
		return nil, err
	}

	consistent := true
	out, err := client.GetItem(ctx, &dynamodb.GetItemInput{
		TableName:      &table,
		Key:            key,
		ConsistentRead: &consistent,
	})
	if err != nil {
		return nil, err
	}
	if out.Item == nil || len(out.Item) == 0 {
		return nil, nil
	}

	var item PredictionTrackerItem
	if err := attributevalue.UnmarshalMap(out.Item, &item); err != nil {
		return nil, err
	}
	return &item, nil
}
