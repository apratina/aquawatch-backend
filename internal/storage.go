package internal

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

// getAWSConfig returns the default resolved AWS configuration used to create
// service clients in this package.
func getAWSConfig() aws.Config {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic("failed to load AWS config: " + err.Error())
	}
	return cfg
}

// getS3Client constructs a new S3 client using default config.
func getS3Client() *s3.Client {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		panic(err)
	}
	return s3.NewFromConfig(cfg)
}

// LoadFromS3 retrieves the full contents of an object at bucket/key.
func LoadFromS3(ctx context.Context, bucket, key string) ([]byte, error) {
	client := getS3Client()
	out, err := client.GetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})
	if err != nil {
		return nil, err
	}
	defer out.Body.Close()
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(out.Body)
	if err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// SaveToS3 writes data to a time-based key under the bucket configured via the
// S3_BUCKET environment variable. It returns the generated key on success.
func SaveToS3(ctx context.Context, data []byte) (string, error) {
	cfg := getAWSConfig()
	client := s3.NewFromConfig(cfg)
	bucket := os.Getenv("S3_BUCKET")
	key := fmt.Sprintf("raw/%d.json", time.Now().Unix())
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: &bucket,
		Key:    &key,
		Body:   bytes.NewReader(data),
	})
	if err != nil {
		return "", err
	}
	return key, nil
}

// SaveToS3WithKey stores data to the specified bucket/key.
func SaveToS3WithKey(ctx context.Context, data []byte, bucket, key string) error {
	client := getS3Client()
	_, err := client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader(data),
	})
	return err
}

// GeneratePresignedGetURL returns a presigned GET url that expires after expiry.
func GeneratePresignedGetURL(ctx context.Context, bucket, key string, expiry time.Duration) (string, error) {
	cfg := getAWSConfig()
	s3Client := s3.NewFromConfig(cfg)
	presigner := s3.NewPresignClient(s3Client)
	out, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	}, s3.WithPresignExpires(expiry))
	if err != nil {
		return "", err
	}
	return out.URL, nil
}
