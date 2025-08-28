package main

import (
	"aquawatch/internal"
	"context"
	"encoding/csv"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
)

// Prediction represents a potential downstream structure for parsed predictions.
// The current implementation logs raw endpoint bytes instead of parsing.
type Prediction struct {
	StationID string  `json:"station_id"`
	Timestamp string  `json:"timestamp"`
	PredValue float64 `json:"pred_value"`
	Unit      string  `json:"unit"`
}

// inferInput matches the Step Functions payload. If training ran in the same
// execution, s3_model_artifacts carries the model artifact S3 URI. For MME,
// the handler derives the model file name (or uses env).
type inferInput struct {
	Bucket           string   `json:"bucket"`
	ProcessedKey     string   `json:"processed_key"`
	S3ModelArtifacts string   `json:"s3_model_artifacts,omitempty"`
	Sites            []string `json:"sites"`
}

func handler(ctx context.Context, input inferInput) error {
	log.Println("AquaWatch Infer Lambda triggered")

	if input.Bucket == "" || input.ProcessedKey == "" {
		return fmt.Errorf("missing required fields: bucket, processedKey")
	}

	endpoint := os.Getenv("SAGEMAKER_ENDPOINT")
	if endpoint == "" {
		return fmt.Errorf("SAGEMAKER_ENDPOINT not configured")
	}

	if input.S3ModelArtifacts == "" {
		defaultModel := os.Getenv("DEFAULT_MODEL")
		if defaultModel == "" {
			return fmt.Errorf("DEFAULT_MODEL not configured")
		}
		input.S3ModelArtifacts = defaultModel
	}

	log.Println("using target model:", input.S3ModelArtifacts)

	prefix := fmt.Sprintf("s3://%s/model", input.Bucket)
	targetModel := strings.TrimPrefix(input.S3ModelArtifacts, prefix)

	csvData, err := internal.LoadFromS3(ctx, input.Bucket, input.ProcessedKey)
	if err != nil {
		return fmt.Errorf("failed to load processed data: %w", err)
	}

	// Convert training CSV (label + numeric features) into features-only rows for inference.
	reader := csv.NewReader(strings.NewReader(string(csvData)))
	records, err := reader.ReadAll()
	if err != nil {
		return fmt.Errorf("failed to parse csv: %w", err)
	}
	var builder strings.Builder
	for _, r := range records {
		if len(r) == 0 {
			continue
		}
		// drop label (first column), keep numeric features
		features := r
		if len(r) > 1 {
			features = r[1:]
		}
		builder.WriteString(strings.Join(features, ","))
		builder.WriteByte('\n')
	}

	predBytes, err := internal.InvokeEndpoint(ctx, endpoint, []byte(builder.String()), targetModel)
	if err != nil {
		return fmt.Errorf("failed to invoke endpoint: %w", err)
	}

	log.Println("raw prediction bytes:", string(predBytes))

	// Best-effort: record training job details if available
	uuid := ""
	if input.S3ModelArtifacts != "" {
		// Expected: s3://<bucket>/model/<training-job-name>/output/model.tar.gz
		parts := strings.Split(input.S3ModelArtifacts, "/model/")
		if len(parts) == 2 {
			tail := parts[1]
			if idx := strings.Index(tail, "/"); idx > 0 {
				uuid = tail[:idx]
			} else {
				uuid = tail
			}
		}
	}
	if uuid == "" {
		uuid = fmt.Sprintf("train-%d", time.Now().UTC().UnixNano())
	}
	err = internal.SaveTrainModelTrackerItem(ctx, internal.TrainModelTrackerItem{
		UUID:      uuid,
		CreatedOn: time.Now().UTC().UnixMilli(),
		Sites:     input.Sites,
	})
	if err != nil {
		log.Printf("failed to save train model tracker item: %v", err)
	}

	return nil
}

func main() {
	lambda.Start(handler)
}
