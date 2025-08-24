package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sfn"
)

// StartStateMachine starts an AWS Step Functions execution with the provided input.
// The input can be any Go value that can be marshaled to JSON, or a raw []byte JSON payload.
func StartStateMachine(ctx context.Context, stateMachineArn string, input any) (string, error) {
	cfg := getAWSConfig()
	client := sfn.NewFromConfig(cfg)

	var inputJSON []byte
	switch v := input.(type) {
	case []byte:
		inputJSON = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return "", fmt.Errorf("marshal state machine input: %w", err)
		}
		inputJSON = b
	}

	execName := fmt.Sprintf("exec-%d", time.Now().UnixNano())
	out, err := client.StartExecution(ctx, &sfn.StartExecutionInput{
		StateMachineArn: aws.String(stateMachineArn),
		Name:            aws.String(execName),
		Input:           aws.String(string(inputJSON)),
	})
	if err != nil {
		return "", err
	}
	if out.ExecutionArn == nil {
		return "", fmt.Errorf("missing execution arn in response")
	}
	return *out.ExecutionArn, nil
}
