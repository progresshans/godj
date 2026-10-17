package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
)

// S3Config describes an existing general-purpose S3 bucket. Construction does
// not discover credentials, contact the service, create a bucket or change its
// policy. Credentials and Transport are borrowed concurrent capabilities.
// The endpoint must implement conditional PUT and full-object SHA256 checksums.
type S3Config struct {
	Bucket, Region      string
	Endpoint            string // Empty selects the SDK's regional AWS endpoint.
	Prefix              string // Optional portable directory name, without a slash suffix.
	ExpectedBucketOwner string // Optional AWS account ID, sent on every operation.
	Credentials         aws.CredentialsProvider
	Transport           http.RoundTripper
	UsePathStyle        bool
	// HTTP is restricted to an explicitly enabled loopback endpoint for local
	// services. Redirects and SDK retries are disabled on every endpoint.
	AllowLoopbackHTTP  bool
	RequestTimeout     time.Duration // Default 30 seconds, including response reads.
	Limits             Limits
	MaxBufferedBytes   int64 // Default 64 MiB of pending upload content/reservations.
	MaxConcurrentSaves int   // Default 32. Does not queue or start a source read when full.
	Random             io.Reader
}

func (S3Config) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.S3Config{redacted}") }

// S3 owns operation admission and private upload buffers, not a bucket's
// lifetime. Copies share one lifecycle. Open readers remain caller-owned after
// Close; Delete removes a name, never enumerates a prefix or deletes versions.
type S3 struct{ state *s3State }

type s3State struct {
	life                    sync.RWMutex
	entropy                 sync.Mutex
	mu                      sync.Mutex
	closed                  atomic.Bool
	pending                 int
	buffered                int64
	maxBuffered             int64
	maxSaves                int
	limits                  Limits
	bucket, prefix, service string
	owner                   *string
	random                  io.Reader
	client                  *awss3.Client
	closeIdle               func()
}

// NewS3 validates a fixed configuration without network, credential or entropy
// I/O. Requests use SigV4 from the pinned official SDK and an explicit provider.
func NewS3(config S3Config) (*S3, error) {
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	if !validS3Bucket(config.Bucket) || !validS3Region(config.Region) || nilValue(config.Credentials) {
		return nil, &Error{Code: "invalid_s3_config", Outcome: NotPublished}
	}
	switch config.Credentials.(type) {
	case aws.AnonymousCredentials, *aws.AnonymousCredentials:
		return nil, &Error{Code: "invalid_s3_config", Outcome: NotPublished}
	}
	prefix := config.Prefix
	if prefix != "" {
		if err := validateName(prefix); err != nil || len(prefix) >= 1023 {
			return nil, &Error{Code: "invalid_s3_prefix", Outcome: NotPublished, Cause: err}
		}
		prefix += "/"
	}
	if config.ExpectedBucketOwner != "" {
		if len(config.ExpectedBucketOwner) != 12 || strings.ContainsFunc(config.ExpectedBucketOwner, func(r rune) bool { return r < '0' || r > '9' }) {
			return nil, &Error{Code: "invalid_s3_owner", Outcome: NotPublished}
		}
	}
	var endpoint *string
	if config.Endpoint != "" {
		u, err := parseURL(config.Endpoint)
		if err != nil || u.Scheme == "" || u.RawQuery != "" || u.ForceQuery || u.Path != "" && u.Path != "/" {
			return nil, &Error{Code: "invalid_s3_endpoint", Outcome: NotPublished, Cause: err}
		}
		if u.Scheme != "https" {
			address, err := netip.ParseAddr(u.Hostname())
			if !config.AllowLoopbackHTTP || err != nil || !address.IsLoopback() {
				return nil, &Error{Code: "invalid_s3_endpoint", Outcome: NotPublished}
			}
		}
		endpoint = aws.String(strings.TrimSuffix(u.String(), "/"))
	}
	if config.RequestTimeout == 0 {
		config.RequestTimeout = 30 * time.Second
	}
	if config.MaxBufferedBytes == 0 {
		config.MaxBufferedBytes = 64 << 20
	}
	if config.MaxConcurrentSaves == 0 {
		config.MaxConcurrentSaves = 32
	}
	if config.RequestTimeout < 0 || config.MaxBufferedBytes < 1 || uint64(config.MaxBufferedBytes) >= uint64(^uint(0)>>1) || limits.MaxFileBytes > 5<<30 || config.MaxConcurrentSaves < 1 || config.MaxConcurrentSaves > 1024 {
		return nil, &Error{Code: "invalid_limits", Outcome: NotPublished}
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	} else if nilValue(random) {
		return nil, &Error{Code: "invalid_entropy", Outcome: NotPublished}
	}
	transport := config.Transport
	var closeIdle func()
	if transport == nil {
		transport = http.DefaultTransport
		if defaults, ok := transport.(*http.Transport); ok && defaults != nil {
			owned := defaults.Clone()
			owned.MaxResponseHeaderBytes = 64 << 10
			transport, closeIdle = owned, owned.CloseIdleConnections
		}
	}
	if nilValue(transport) {
		return nil, &Error{Code: "invalid_transport", Outcome: NotPublished}
	}
	httpClient := &http.Client{Transport: s3Transport{transport}, Timeout: config.RequestTimeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	client := awss3.New(awss3.Options{
		Region: config.Region, BaseEndpoint: endpoint, UsePathStyle: config.UsePathStyle,
		Credentials: s3Credentials{config.Credentials}, HTTPClient: s3HTTPClient{httpClient}, Retryer: aws.NopRetryer{},
		Logger: logging.Nop{}, DisableLogOutputChecksumValidationSkipped: true,
		RequestChecksumCalculation:     aws.RequestChecksumCalculationWhenRequired,
		ResponseChecksumValidation:     aws.ResponseChecksumValidationWhenRequired,
		DisableMultiRegionAccessPoints: true, DisableS3ExpressSessionAuth: aws.Bool(true),
	})
	var owner *string
	if config.ExpectedBucketOwner != "" {
		owner = aws.String(config.ExpectedBucketOwner)
	}
	return &S3{state: &s3State{limits: limits, maxBuffered: config.MaxBufferedBytes, maxSaves: config.MaxConcurrentSaves, bucket: config.Bucket, prefix: prefix, service: config.Region + "\x00" + aws.ToString(endpoint), owner: owner, random: random, client: client, closeIdle: closeIdle}}, nil
}

func validS3Region(value string) bool {
	return len(value) >= 1 && len(value) <= 63 && value[0] != '-' && value[len(value)-1] != '-' && !strings.ContainsFunc(value, func(r rune) bool { return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') })
}

type s3Credentials struct{ provider aws.CredentialsProvider }

func (c s3Credentials) Retrieve(ctx context.Context) (aws.Credentials, error) {
	credentials, err := c.provider.Retrieve(ctx)
	if err != nil {
		return aws.Credentials{}, err
	}
	if !credentials.HasKeys() {
		return aws.Credentials{}, &Error{Code: "invalid_credentials"}
	}
	return credentials, nil
}

func validS3Bucket(value string) bool {
	if len(value) < 3 || len(value) > 63 || strings.Contains(value, "..") || strings.HasPrefix(value, "xn--") || strings.HasPrefix(value, "sthree-") || strings.HasPrefix(value, "amzn-s3-demo-") || strings.HasSuffix(value, "--x-s3") || strings.HasSuffix(value, "--ol-s3") || strings.HasSuffix(value, "-s3alias") || strings.HasSuffix(value, ".mrap") || strings.HasSuffix(value, "--table-s3") {
		return false
	}
	alphanumeric := func(r rune) bool { return r >= 'a' && r <= 'z' || r >= '0' && r <= '9' }
	if !alphanumeric(rune(value[0])) || !alphanumeric(rune(value[len(value)-1])) || strings.ContainsFunc(value, func(r rune) bool { return !alphanumeric(r) && r != '-' && r != '.' }) {
		return false
	}
	_, err := netip.ParseAddr(value)
	return err != nil
}

func (S3) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.S3{redacted}") }

func (b *S3) lock(ctx context.Context) (*s3State, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if b == nil || b.state == nil {
		return nil, &Error{Code: "closed", Outcome: NotPublished}
	}
	s := b.state
	s.life.RLock()
	if s.closed.Load() {
		s.life.RUnlock()
		return nil, &Error{Code: "closed", Outcome: NotPublished}
	}
	if err := contextError(ctx); err != nil {
		s.life.RUnlock()
		return nil, err
	}
	return s, nil
}

func (b *S3) Close() error {
	if b == nil || b.state == nil {
		return nil
	}
	s := b.state
	s.life.Lock()
	defer s.life.Unlock()
	if !s.closed.Swap(true) {
		if s.closeIdle != nil {
			s.closeIdle()
		}
	}
	return nil
}

// Save reads borrowed input once to EOF into a bounded private buffer, then
// issues a single conditional PUT per confirmed collision. A transport failure
// after dispatch preserves the candidate and Uncertain outcome. It never
// probes, retries, overwrites or deletes a possibly published object.
func (b *S3) Save(ctx context.Context, name string, source io.Reader, options SaveOptions) (Info, error) {
	if err := validateName(name); err != nil {
		return Info{}, err
	}
	s, err := b.lock(ctx)
	if err != nil {
		return Info{}, err
	}
	defer s.life.RUnlock()
	if nilValue(source) {
		return Info{}, &Error{Code: "invalid_input", Outcome: NotPublished}
	}
	limit, err := nameLimit(s.limits, options)
	if err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	if s.pending == s.maxSaves {
		s.mu.Unlock()
		return Info{}, &Error{Code: "capacity_exceeded", Outcome: NotPublished}
	}
	s.pending++
	s.mu.Unlock()
	var held int64
	defer func() { s.mu.Lock(); s.pending--; s.buffered -= held; s.mu.Unlock() }()
	content, checksum, err := s.readUpload(ctx, source, &held)
	if err != nil {
		return Info{}, err
	}
	candidate := name
	for attempt := 0; attempt < s.limits.MaxAttempts; attempt++ {
		if err := contextError(ctx); err != nil {
			return Info{}, err
		}
		if attempt != 0 || utf8.RuneCountInString(candidate) > limit || len(s.prefix+candidate) > 1024 {
			s.entropy.Lock()
			func() {
				defer s.entropy.Unlock()
				candidate, err = alternativeName(name, limit, 1024-len(s.prefix), s.random)
			}()
			if err != nil {
				return Info{}, err
			}
		}
		dispatched := &atomic.Bool{}
		response, err := func() (*awss3.PutObjectOutput, error) {
			callContext, cancel := context.WithCancel(context.WithValue(ctx, s3DispatchKey{}, dispatched))
			// A rejected request may still be reading its body after Do returns.
			// Every attempt owns a cursor and ends it before another candidate;
			// only the immutable content buffer is shared between attempts.
			body := &s3UploadBody{ctx: callContext, reader: bytes.NewReader(content)}
			defer body.Close()
			defer cancel()
			return s.client.PutObject(callContext, &awss3.PutObjectInput{
				Bucket: aws.String(s.bucket), Key: aws.String(s.prefix + candidate), ExpectedBucketOwner: s.owner,
				Body: body, ContentLength: aws.Int64(int64(len(content))), IfNoneMatch: aws.String("*"),
				ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumSHA256: aws.String(checksum),
				ContentType: aws.String("application/octet-stream"), ContentDisposition: aws.String("attachment"), CacheControl: aws.String("private, no-store"),
			})
		}()
		if s3StatusCode(err) == http.StatusPreconditionFailed && s3ErrorCode(err) == "PreconditionFailed" {
			continue
		}
		candidateInfo := Info{name: candidate, size: int64(len(content))}
		if err != nil {
			outcome := Uncertain
			if !dispatched.Load() || s3WriteRejected(err) {
				outcome, candidateInfo = NotPublished, Info{}
			}
			return candidateInfo, &Error{Code: "publish_failed", Outcome: outcome, Cause: err}
		}
		if response == nil || aws.ToString(response.ChecksumSHA256) != checksum || response.ChecksumType != types.ChecksumTypeFullObject {
			return candidateInfo, &Error{Code: "publication_integrity_failed", Outcome: Published}
		}
		digest, _ := base64.StdEncoding.DecodeString(checksum)
		candidateInfo.metadata.Version = "sha256-" + hex.EncodeToString(digest)
		return candidateInfo, nil
	}
	return Info{}, &Error{Code: "collision_limit", Outcome: NotPublished}
}

func (s *s3State) readUpload(ctx context.Context, source io.Reader, held *int64) ([]byte, string, error) {
	var content []byte
	digest := sha256.New()
	var buffer [32 << 10]byte
	empty := 0
	for {
		if err := contextError(ctx); err != nil {
			return nil, "", err
		}
		remaining := s.limits.MaxFileBytes - int64(len(content))
		s.mu.Lock()
		free := s.maxBuffered - s.buffered
		want := min(int64(len(buffer)), remaining+1, free+1)
		claim := min(want, free)
		s.buffered += claim
		*held += claim
		s.mu.Unlock()
		n, err := source.Read(buffer[:int(want)])
		if n < 0 || n > int(want) {
			return nil, "", &Error{Code: "invalid_read_count", Outcome: NotPublished, Cause: err}
		}
		if err != nil && err != io.EOF {
			return nil, "", &Error{Code: "input_read_failed", Outcome: NotPublished, Cause: err}
		}
		if int64(n) > remaining {
			return nil, "", &Error{Code: "file_too_large", Outcome: NotPublished}
		}
		if int64(n) > claim {
			return nil, "", &Error{Code: "capacity_exceeded", Outcome: NotPublished}
		}
		s.mu.Lock()
		s.buffered -= claim - int64(n)
		*held -= claim - int64(n)
		s.mu.Unlock()
		content = append(content, buffer[:n]...)
		_, _ = digest.Write(buffer[:n])
		if err == io.EOF {
			return content, base64.StdEncoding.EncodeToString(digest.Sum(nil)), nil
		}
		if n == 0 {
			empty++
			if empty == 100 {
				return nil, "", &Error{Code: "input_read_failed", Outcome: NotPublished, Cause: io.ErrNoProgress}
			}
		} else {
			empty = 0
		}
	}
}

// The SDK borrows its request stream. Close serializes with any final transport
// read and drops the underlying bytes even if a request object remains retained.
type s3UploadBody struct {
	mu     sync.Mutex
	ctx    context.Context
	reader *bytes.Reader
}

func (b *s3UploadBody) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reader == nil {
		return 0, fs.ErrClosed
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.reader.Read(p)
}
func (b *s3UploadBody) Seek(offset int64, whence int) (int64, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reader == nil {
		return 0, fs.ErrClosed
	}
	if err := b.ctx.Err(); err != nil {
		return 0, err
	}
	return b.reader.Seek(offset, whence)
}
func (b *s3UploadBody) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.reader = nil
	return nil
}

type s3DispatchKey struct{}
type s3HTTPClient struct{ client *http.Client }
type s3Transport struct{ base http.RoundTripper }

func (t s3Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	if dispatched, ok := request.Context().Value(s3DispatchKey{}).(*atomic.Bool); ok {
		dispatched.Store(true)
	}
	return t.base.RoundTrip(request)
}

func (c s3HTTPClient) Do(request *http.Request) (*http.Response, error) {
	response, err := c.client.Do(request)
	if response != nil && response.Body != nil && (response.StatusCode < 200 || response.StatusCode >= 300) {
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, 64<<10), response.Body}
	}
	return response, err
}

func s3StatusCode(err error) int {
	var response interface{ HTTPStatusCode() int }
	if errors.As(err, &response) {
		return response.HTTPStatusCode()
	}
	return 0
}
func s3ErrorCode(err error) string {
	var service smithy.APIError
	if errors.As(err, &service) {
		return service.ErrorCode()
	}
	return ""
}
func s3WriteRejected(err error) bool {
	status := s3StatusCode(err)
	if status < 400 || status >= 500 || status == http.StatusRequestTimeout {
		return false
	}
	switch s3ErrorCode(err) {
	case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch", "ExpiredToken", "InvalidToken", "InvalidRequest", "BadDigest", "XAmzContentChecksumMismatch", "InvalidDigest", "NoSuchBucket", "EntityTooLarge", "ConditionalRequestConflict", "PreconditionFailed":
		return true
	}
	return false
}

func s3OperationError(code string, err error) error {
	if s3ErrorCode(err) == "NoSuchKey" || s3ErrorCode(err) == "NoSuchVersion" || s3ErrorCode(err) == "NotFound" && s3StatusCode(err) == http.StatusNotFound {
		err = errors.Join(err, fs.ErrNotExist)
	}
	return &Error{Code: code, Cause: err}
}

func (s *s3State) key(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	key := s.prefix + name
	if len(key) > 1024 {
		return "", &Error{Code: "name_too_long", Outcome: NotPublished}
	}
	return key, nil
}

func (b *S3) Stat(ctx context.Context, name string) (Info, error) {
	s, err := b.lock(ctx)
	if err != nil {
		return Info{}, err
	}
	defer s.life.RUnlock()
	key, err := s.key(name)
	if err != nil {
		return Info{}, err
	}
	result, err := s.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ExpectedBucketOwner: s.owner, ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		return Info{}, s3OperationError("stat_failed", err)
	}
	return s3ObjectInfo(name, s.service, s.bucket, key, result.ContentLength, result.LastModified, result.VersionId, result.ChecksumSHA256, result.ChecksumType)
}

func (b *S3) Delete(ctx context.Context, name string) error {
	s, err := b.lock(ctx)
	if err != nil {
		return err
	}
	defer s.life.RUnlock()
	key, err := s.key(name)
	if err != nil {
		return err
	}
	_, err = s.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ExpectedBucketOwner: s.owner})
	if s3ErrorCode(err) == "NoSuchKey" {
		return nil
	}
	if err != nil {
		return s3OperationError("delete_failed", err)
	}
	return nil
}
