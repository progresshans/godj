package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"math"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

// A remote version is a stable content handle. A null/unversioned object's
// live name is not: those readers deliberately do not advertise Seek.
type s3Reader struct{ state *s3ReadState }
type s3VersionReader struct{ *s3Reader }

type s3ReadState struct {
	mu                   sync.Mutex
	ctx                  context.Context
	cancel               context.CancelFunc
	client               *awss3.Client
	bucket, key, version string
	owner                *string
	info                 Info
	body                 io.ReadCloser
	position             int64
	empty                int
	eof, closed          bool
	failure, closeErr    error
	expected             []byte
	digest               hash.Hash
	release              func()
}

func (b *S3) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	s, err := b.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer s.life.RUnlock()
	key, err := s.key(name)
	if err != nil {
		return nil, err
	}
	readContext, cancel := context.WithCancel(ctx)
	result, err := s.client.GetObject(readContext, &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(key), ExpectedBucketOwner: s.owner, ChecksumMode: types.ChecksumModeEnabled})
	if err != nil {
		cancel()
		return nil, s3OperationError("open_failed", err)
	}
	info, infoErr := s3ObjectInfo(name, s.service, s.bucket, key, result.ContentLength, result.LastModified, result.VersionId, result.ChecksumSHA256, result.ChecksumType)
	expected, checksumErr := s3FullChecksum(result.ChecksumSHA256, result.ChecksumType)
	if infoErr != nil || checksumErr != nil || result.Body == nil || aws.ToString(result.ContentRange) != "" {
		cancel()
		var closeErr error
		if result.Body != nil {
			closeErr = result.Body.Close()
		}
		return nil, &Error{Code: "invalid_s3_response", Cause: errors.Join(infoErr, checksumErr, closeErr)}
	}
	state := &s3ReadState{ctx: readContext, cancel: cancel, client: s.client, bucket: s.bucket, key: key, version: aws.ToString(result.VersionId), owner: s.owner, info: info, body: result.Body, expected: expected}
	state.release = func() {
		// A reader can start version-bound GETs after backend Close. Reap idle
		// connections returned by those requests when the caller closes it.
		if s.closed.Load() && s.closeIdle != nil {
			s.closeIdle()
		}
	}
	if expected != nil {
		state.digest = sha256.New()
	}
	reader := &s3Reader{state: state}
	if state.version != "" && state.version != "null" {
		return &s3VersionReader{reader}, nil
	}
	return reader, nil
}

func s3FullChecksum(encoded *string, kind types.ChecksumType) ([]byte, error) {
	// A multipart/composite checksum is not a digest of the response bytes.
	if encoded == nil || kind != types.ChecksumTypeFullObject {
		return nil, nil
	}
	digest, err := base64.StdEncoding.Strict().DecodeString(*encoded)
	if err != nil || len(digest) != sha256.Size {
		return nil, &Error{Code: "invalid_s3_checksum"}
	}
	return digest, nil
}

func s3ObjectInfo(name, service, bucket, key string, length *int64, modified *time.Time, version, checksum *string, kind types.ChecksumType) (Info, error) {
	if length == nil || *length < 0 || len(aws.ToString(version)) > 1024 {
		return Info{}, &Error{Code: "invalid_s3_response"}
	}
	info, err := NewInfo(name, *length)
	if err != nil {
		return Info{}, err
	}
	metadata := ContentMetadata{}
	if modified != nil {
		metadata.Modified = *modified
	}
	digest, err := s3FullChecksum(checksum, kind)
	if err != nil {
		return Info{}, err
	}
	if digest != nil {
		metadata.Version = "sha256-" + hex.EncodeToString(digest)
	} else if value := aws.ToString(version); value != "" && value != "null" {
		// Version identifiers can be scoped to a key. Bind the whole remote
		// identity before exposing a public validator; do not promote an ETag,
		// object length or modification time to a full-content checksum.
		identity := sha256.Sum256([]byte(service + "\x00" + bucket + "\x00" + key + "\x00" + value))
		metadata.Version = "s3-version-" + hex.EncodeToString(identity[:])
	}
	return info.WithContentMetadata(metadata)
}

func (r *s3Reader) Info() Info {
	if r == nil || r.state == nil {
		return Info{}
	}
	return r.state.info
}
func (s3Reader) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.S3Reader{redacted}") }

func (r *s3Reader) Read(p []byte) (int, error) {
	if r == nil || r.state == nil {
		return 0, &Error{Code: "closed"}
	}
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, &Error{Code: "closed"}
	}
	if err := s.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	if s.failure != nil {
		return 0, s.failure
	}
	if len(p) == 0 {
		return 0, nil
	}
	if s.eof {
		return 0, io.EOF
	}
	if s.body == nil {
		if s.position >= s.info.Size() {
			return 0, io.EOF
		}
		if err := s.reopen(); err != nil {
			s.failure = err
			return 0, err
		}
	}
	remaining := s.info.Size() - s.position
	want := int64(len(p))
	if remaining < want {
		want = remaining + 1
	}
	n, err := s.body.Read(p[:int(want)])
	if n < 0 || int64(n) > want {
		s.failure = &Error{Code: "invalid_read_count", Cause: err}
		return 0, s.failure
	}
	if int64(n) > remaining {
		s.failure = &Error{Code: "invalid_size", Cause: err}
		return 0, s.failure
	}
	s.position += int64(n)
	if s.digest != nil {
		_, _ = s.digest.Write(p[:n])
	}
	if err != nil && err != io.EOF {
		s.failure = &Error{Code: "read_failed", Cause: err}
		return n, s.failure
	}
	if n == 0 && err == nil {
		s.empty++
		if s.empty == 100 {
			s.failure = &Error{Code: "read_failed", Cause: io.ErrNoProgress}
			return 0, s.failure
		}
	} else {
		s.empty = 0
	}
	if err == io.EOF {
		if s.position != s.info.Size() {
			s.failure = &Error{Code: "invalid_size", Cause: io.ErrUnexpectedEOF}
		} else if s.digest != nil && !bytes.Equal(s.digest.Sum(nil), s.expected) {
			s.failure = &Error{Code: "checksum_mismatch"}
		}
		if s.failure != nil {
			return n, s.failure
		}
		s.eof = true
	}
	if canceled := s.ctx.Err(); canceled != nil {
		s.failure = &Error{Code: "canceled", Cause: canceled}
		return n, s.failure
	}
	return n, err
}

// Reopen a range by the original immutable version, never by the current name
// or a separate HEAD result. Responses must still describe that exact version
// and span. The SDK's full-object checksum is requested only for full reads.
func (s *s3ReadState) reopen() error {
	request := &awss3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(s.key), VersionId: aws.String(s.version), ExpectedBucketOwner: s.owner}
	if s.position != 0 {
		request.Range = aws.String(fmt.Sprintf("bytes=%d-", s.position))
	} else {
		request.ChecksumMode = types.ChecksumModeEnabled
	}
	result, err := s.client.GetObject(s.ctx, request)
	if err != nil {
		return s3OperationError("read_failed", err)
	}
	valid := result.Body != nil && result.ContentLength != nil && *result.ContentLength == s.info.Size()-s.position && aws.ToString(result.VersionId) == s.version
	if s.position != 0 {
		valid = valid && aws.ToString(result.ContentRange) == fmt.Sprintf("bytes %d-%d/%d", s.position, s.info.Size()-1, s.info.Size())
	} else {
		valid = valid && aws.ToString(result.ContentRange) == ""
	}
	if !valid {
		var closeErr error
		if result.Body != nil {
			closeErr = result.Body.Close()
		}
		return &Error{Code: "invalid_s3_response", Cause: closeErr}
	}
	s.body = result.Body
	s.digest = nil
	if s.position == 0 && s.expected != nil {
		s.digest = sha256.New()
	}
	return nil
}

func (r *s3VersionReader) Seek(offset int64, whence int) (int64, error) {
	if r == nil || r.s3Reader == nil || r.state == nil {
		return 0, &Error{Code: "closed"}
	}
	s := r.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, &Error{Code: "closed"}
	}
	if err := s.ctx.Err(); err != nil {
		return 0, &Error{Code: "canceled", Cause: err}
	}
	if s.failure != nil {
		return 0, s.failure
	}
	var base int64
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		base = s.position
	case io.SeekEnd:
		base = s.info.Size()
	default:
		return 0, &Error{Code: "invalid_seek"}
	}
	if offset > 0 && base > math.MaxInt64-offset || offset < 0 && offset < -base {
		return 0, &Error{Code: "invalid_seek"}
	}
	next := base + offset
	if next < 0 {
		return 0, &Error{Code: "invalid_seek"}
	}
	if next == s.position {
		return next, nil
	}
	if s.body != nil {
		body := s.body
		s.body = nil
		if err := body.Close(); err != nil {
			s.failure = &Error{Code: "close_failed", Cause: err}
			return 0, s.failure
		}
	}
	s.body, s.position, s.eof, s.digest, s.empty = nil, next, false, nil, 0
	return next, nil
}

func (r *s3Reader) Close() error {
	if r == nil || r.state == nil {
		return nil
	}
	s := r.state
	s.cancel() // Interrupt an in-flight network read before waiting for its lock.
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if s.body != nil {
			if err := s.body.Close(); err != nil {
				s.closeErr = &Error{Code: "close_failed", Cause: err}
			}
			s.body = nil
		}
		if s.release != nil {
			s.release()
			s.release = nil
		}
	}
	return s.closeErr
}
