package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsConfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

func main() {
	endpoint := os.Getenv("MINIO_ENDPOINT")
	accessKey := os.Getenv("MINIO_ROOT_USER")
	secretKey := os.Getenv("MINIO_ROOT_PASSWORD")
	region := os.Getenv("MINIO_REGION")
	
	if endpoint == "" {
		endpoint = "http://minio:9000"
	}
	if accessKey == "" {
		accessKey = "cascataadmin"
	}
	if secretKey == "" {
		secretKey = "cascatasecret"
	}
	if region == "" {
		region = "us-east-1"
	}
	
	fmt.Printf("=== MinIO Connection Test ===\n")
	fmt.Printf("Endpoint: %s\n", endpoint)
	fmt.Printf("AccessKey: %s...\n", accessKey[:min(8, len(accessKey))])
	fmt.Printf("Region: %s\n\n", region)
	
	creds := credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")
	
	optFns := []func(*awsConfig.LoadOptions) error{
		awsConfig.WithCredentialsProvider(creds),
		awsConfig.WithRegion(region),
		awsConfig.WithEndpointResolverWithOptions(
			aws.EndpointResolverWithOptionsFunc(func(service, region string, options ...interface{}) (aws.Endpoint, error) {
				return aws.Endpoint{
					URL:               endpoint,
					HostnameImmutable: true,
					Source:            aws.EndpointSourceCustom,
				}, nil
			}),
		),
	}
	
	cfg, err := awsConfig.LoadDefaultConfig(context.Background(), optFns...)
	if err != nil {
		fmt.Printf("❌ ERROR: Failed to load AWS config: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("✓ AWS config loaded\n")
	
	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(endpoint)
	})
	fmt.Printf("✓ S3 client created\n")
	
	// Test 1: List buckets
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	fmt.Printf("\n--- Test 1: List Buckets ---\n")
	result, err := client.ListBuckets(ctx, &s3.ListBucketsInput{})
	if err != nil {
		fmt.Printf("❌ ERROR listing buckets: %v\n", err)
	} else {
		fmt.Printf("✓ Successfully listed %d buckets:\n", len(result.Buckets))
		for _, b := range result.Buckets {
			fmt.Printf("  - %s (created: %v)\n", *b.Name, b.CreationDate)
		}
	}
	
	// Test 2: Try to create a test bucket
	fmt.Printf("\n--- Test 2: Create Test Bucket ---\n")
	testBucket := "test-bucket-" + fmt.Sprintf("%d", time.Now().Unix())
	_, err = client.CreateBucket(ctx, &s3.CreateBucketInput{
		Bucket: aws.String(testBucket),
	})
	if err != nil {
		fmt.Printf("❌ ERROR creating bucket: %v\n", err)
	} else {
		fmt.Printf("✓ Created bucket: %s\n", testBucket)
		
		// Delete it
		_, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{
			Bucket: aws.String(testBucket),
		})
		if err != nil {
			fmt.Printf("❌ ERROR deleting bucket: %v\n", err)
		} else {
			fmt.Printf("✓ Deleted test bucket\n")
		}
	}
	
	fmt.Printf("\n=== Test Complete ===\n")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
