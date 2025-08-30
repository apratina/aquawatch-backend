package main

import (
	"aquawatch/internal"
	"context"
	"fmt"
	"log"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
)

// input expected from Step Functions or direct invocation
// uuid: unique training job identifier (e.g., training job name)
// createdon: optional epoch millis override; defaults to now
// sites: optional list of sites used for training
type trackerInput struct {
	CreatedOn int64    `json:"createdon,omitempty"`
	Sites     []string `json:"sites,omitempty"`
}

func handler(ctx context.Context, in trackerInput) error {
	log.Println("AquaWatch Train Model Tracker Lambda triggered")
	if in.CreatedOn == 0 {
		in.CreatedOn = time.Now().UTC().UnixMilli()
	}
	item := internal.TrainModelTrackerItem{
		UUID:      fmt.Sprintf("train-%d", time.Now().UTC().UnixMilli()),
		CreatedOn: in.CreatedOn,
		Sites:     in.Sites,
	}
	if err := internal.SaveTrainModelTrackerItem(ctx, item); err != nil {
		return fmt.Errorf("failed to save train model tracker item: %w", err)
	}
	return nil
}

func main() {
	lambda.Start(handler)
}
