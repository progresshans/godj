package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3SignedURL struct {
	backend *S3
	expires time.Duration
}

// SignedURL creates an explicitly selected download capability. Calling URL
// signs a GET for the named key; it performs no HEAD, existence or admission
// check. A recipient can read that name until signature/credential expiry,
// including content later placed under the same name. It is not a snapshot.
// It does not permit upload, inline execution or response-header overrides.
func (b *S3) SignedURL(expires time.Duration) (URLResolver, error) {
	if b == nil || b.state == nil {
		return nil, &Error{Code: "invalid_backend"}
	}
	if expires < time.Second || expires > 7*24*time.Hour || expires%time.Second != 0 {
		return nil, &Error{Code: "invalid_url_expiry"}
	}
	return s3SignedURL{backend: b, expires: expires}, nil
}

func (s3SignedURL) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.S3SignedURL{redacted}") }

func (resolver s3SignedURL) URL(ctx context.Context, name string) (string, error) {
	s, err := resolver.backend.lock(ctx)
	if err != nil {
		return "", err
	}
	defer s.life.RUnlock()
	key, err := s.key(name)
	if err != nil {
		return "", err
	}
	result, err := awss3.NewPresignClient(s.client).PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(s.bucket), Key: aws.String(key), ExpectedBucketOwner: s.owner,
		ResponseContentType: aws.String("application/octet-stream"), ResponseContentDisposition: aws.String("attachment"), ResponseCacheControl: aws.String("private, no-store"),
	}, func(options *awss3.PresignOptions) { options.Expires = resolver.expires })
	if err != nil {
		return "", &Error{Code: "url_failed", Cause: err}
	}
	for name := range result.SignedHeader {
		if !strings.EqualFold(name, "host") {
			return "", &Error{Code: "unsupported_signed_headers"}
		}
	}
	if err := contextError(ctx); err != nil {
		return "", err
	}
	return result.URL, nil
}
