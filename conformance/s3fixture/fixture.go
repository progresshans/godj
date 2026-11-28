// Package s3fixture owns fresh buckets on an explicitly configured loopback S3
// service. Setup and cleanup use the official client independently of GoDj's
// storage operations. It never discovers cloud credentials or existing data.
package s3fixture

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go/logging"
	"github.com/progresshans/godj/storage"
)

// Enabled returns false only for an absent optional profile. Required or
// partially configured profiles fail, so CI cannot silently omit the server.
func Enabled(t testing.TB) bool {
	t.Helper()
	endpoint, access, secret := os.Getenv("GODJ_TEST_S3_ENDPOINT"), os.Getenv("GODJ_TEST_S3_ACCESS_KEY"), os.Getenv("GODJ_TEST_S3_SECRET_KEY")
	if endpoint == "" && access == "" && secret == "" && os.Getenv("GODJ_REQUIRE_S3") != "1" {
		return false
	}
	u, err := url.Parse(endpoint)
	if err != nil || u == nil {
		t.Fatal("invalid S3 test endpoint")
		return false
	}
	ip, err := netip.ParseAddr(u.Hostname())
	if err != nil || !ip.IsLoopback() || u.Scheme != "http" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || access == "" || secret == "" {
		t.Fatal("S3 test profile requires a literal loopback HTTP endpoint and explicit test credentials")
	}
	return true
}

type credentials struct{ access, secret string }

func (c credentials) Retrieve(context.Context) (aws.Credentials, error) {
	return aws.Credentials{AccessKeyID: c.access, SecretAccessKey: c.secret, Source: "godj-owned-test-service"}, nil
}

// Config creates a unique bucket and registers bounded cleanup of that exact
// bucket, including old versions and delete markers. Consumers own their backend
// and readers and must close them before the registered cleanup runs.
func Config(t testing.TB, versioned bool) storage.S3Config {
	t.Helper()
	if !Enabled(t) {
		t.Fatal("S3 fixture requested without its service profile")
	}
	var identity [16]byte
	if _, err := rand.Read(identity[:]); err != nil {
		t.Fatal(err)
	}
	config := storage.S3Config{Bucket: "godj-" + hex.EncodeToString(identity[:]), Region: "us-east-1", Endpoint: os.Getenv("GODJ_TEST_S3_ENDPOINT"), Credentials: credentials{os.Getenv("GODJ_TEST_S3_ACCESS_KEY"), os.Getenv("GODJ_TEST_S3_SECRET_KEY")}, UsePathStyle: true, AllowLoopbackHTTP: true, RequestTimeout: 15 * time.Second}
	client := Client(config)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(config.Bucket)}); err != nil {
		t.Fatal("create owned S3 bucket:", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := removeBucket(ctx, client, config.Bucket); err != nil {
			t.Error("remove owned S3 bucket:", err)
		}
	})
	if versioned {
		if _, err := client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: aws.String(config.Bucket), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); err != nil {
			t.Fatal("enable owned bucket versions:", err)
		}
	}
	return config
}

// Client is the independent protocol peer for setup and native assertions.
func Client(config storage.S3Config) *s3.Client {
	return s3.New(s3.Options{Region: config.Region, BaseEndpoint: aws.String(config.Endpoint), UsePathStyle: true, Credentials: config.Credentials, Retryer: aws.NopRetryer{}, Logger: logging.Nop{}, HTTPClient: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
}

func removeBucket(ctx context.Context, client *s3.Client, bucket string) error {
	// Delete the first page repeatedly; unlike a mutable continuation token this
	// cannot skip an object after the preceding page's versions are removed.
	for page := 0; page < 64; page++ {
		result, err := client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: aws.String(bucket), MaxKeys: aws.Int32(1000)})
		if err != nil {
			return err
		}
		objects := make([]types.ObjectIdentifier, 0, len(result.Versions)+len(result.DeleteMarkers))
		for _, object := range result.Versions {
			objects = append(objects, types.ObjectIdentifier{Key: object.Key, VersionId: object.VersionId})
		}
		for _, object := range result.DeleteMarkers {
			objects = append(objects, types.ObjectIdentifier{Key: object.Key, VersionId: object.VersionId})
		}
		if len(objects) == 0 {
			_, err := client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
			return err
		}
		deleted, err := client.DeleteObjects(ctx, &s3.DeleteObjectsInput{Bucket: aws.String(bucket), Delete: &types.Delete{Objects: objects, Quiet: aws.Bool(true)}})
		if err != nil {
			return err
		}
		if len(deleted.Errors) != 0 {
			return errors.New("S3 bucket cleanup returned object failures")
		}
	}
	return errors.New("S3 bucket cleanup exceeded bounded inventory")
}
