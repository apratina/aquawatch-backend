package internal

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sns"
)

// ErrAlreadySubscribed indicates the email is already subscribed to the topic.
var ErrAlreadySubscribed = errors.New("email already subscribed")

// SubscribeAlertsEmail subscribes the provided email to the alerts SNS topic.
// The topic is created if it does not already exist.
// Returns the SubscriptionArn if immediately available; for email subscriptions
// this is typically "pending confirmation" until the recipient confirms.
func SubscribeAlertsEmail(ctx context.Context, email string) (string, error) {
	cfg := getAWSConfig()
	client := sns.NewFromConfig(cfg)

	topicName := os.Getenv("SNS_TOPIC_NAME")
	if topicName == "" {
		topicName = "aquawatch-alerts"
	}

	createOut, err := client.CreateTopic(ctx, &sns.CreateTopicInput{
		Name: aws.String(topicName),
	})
	if err != nil {
		return "", err
	}

	// Check if email is already subscribed (confirmed) to the topic
	p := sns.NewListSubscriptionsByTopicPaginator(client, &sns.ListSubscriptionsByTopicInput{
		TopicArn: createOut.TopicArn,
	})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return "", err
		}
		for _, s := range page.Subscriptions {
			if s.Endpoint != nil && strings.EqualFold(*s.Endpoint, email) && s.Protocol != nil && *s.Protocol == "email" {
				if s.SubscriptionArn != nil && *s.SubscriptionArn != "" && *s.SubscriptionArn != "PendingConfirmation" {
					return "", ErrAlreadySubscribed
				}
			}
		}
	}

	subOut, err := client.Subscribe(ctx, &sns.SubscribeInput{
		Protocol: aws.String("email"),
		Endpoint: aws.String(email),
		TopicArn: createOut.TopicArn,
	})
	if err != nil {
		return "", err
	}
	if subOut.SubscriptionArn == nil {
		return "", nil
	}
	return *subOut.SubscriptionArn, nil
}

// PublishAlert publishes a plain-text alert message to the SNS topic configured by SNS_TOPIC_NAME.
// If the topic doesn't exist, it will be created. Subject is optional.
func PublishAlert(ctx context.Context, subject, message string) error {
	cfg := getAWSConfig()
	client := sns.NewFromConfig(cfg)

	topicName := os.Getenv("SNS_TOPIC_NAME")
	if topicName == "" {
		topicName = "aquawatch-alerts"
	}

	createOut, err := client.CreateTopic(ctx, &sns.CreateTopicInput{Name: aws.String(topicName)})
	if err != nil {
		return err
	}
	pubIn := &sns.PublishInput{TopicArn: createOut.TopicArn, Message: aws.String(message)}
	if strings.TrimSpace(subject) != "" {
		pubIn.Subject = aws.String(subject)
	}
	_, err = client.Publish(ctx, pubIn)
	return err
}
