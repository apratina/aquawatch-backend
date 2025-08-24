package internal

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sagemakerruntime"
)

// InvokeEndpoint calls a SageMaker endpoint with CSV payload bytes. If targetModel
// is non-empty, it sets the TargetModel header (for multi-model endpoints).
func InvokeEndpoint(ctx context.Context, endpointName string, inputData []byte, targetModel string) ([]byte, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := sagemakerruntime.NewFromConfig(cfg)

	in := &sagemakerruntime.InvokeEndpointInput{
		EndpointName: &endpointName,
		Body:         inputData,
		ContentType:  aws.String("text/csv"),
	}
	if targetModel != "" {
		in.TargetModel = aws.String(targetModel)
	}

	resp, err := client.InvokeEndpoint(ctx, in)
	if err != nil {
		return nil, fmt.Errorf("invoke endpoint failed: %w", err)
	}

	return resp.Body, nil
}
