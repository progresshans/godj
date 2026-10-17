package storage_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	"github.com/progresshans/godj/conformance/s3fixture"
	"github.com/progresshans/godj/storage"
)

func liveS3(t *testing.T, config storage.S3Config) *storage.S3 {
	t.Helper()
	backend, err := storage.NewS3(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	return backend
}

func liveS3Read(t *testing.T, reader io.ReadCloser) []byte {
	t.Helper()
	content, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal("read stored object:", readErr, closeErr)
	}
	return content
}

func TestS3LiveServer(t *testing.T) {
	if !s3fixture.Enabled(t) {
		return
	}
	t.Run("conditional_publication", func(t *testing.T) {
		config := s3fixture.Config(t, true)
		config.Prefix = "private/tenant"
		first, second := liveS3(t, config), liveS3(t, config)
		const count = 16
		infos, failures := make([]storage.Info, count), make([]error, count)
		var wait sync.WaitGroup
		for i := range count {
			wait.Go(func() {
				infos[i], failures[i] = []*storage.S3{first, second}[i%2].Save(t.Context(), "동시 문서.txt", strings.NewReader(fmt.Sprintf("content-%d", i)), storage.SaveOptions{})
			})
		}
		wait.Wait()
		// With retries disabled the independent SDK also observes occasional EOF
		// when MinIO closes an idle connection after a rejected conditional PUT.
		// Every successful write must be present; an unknown result must retain
		// its candidate without another attempt. Reconcile only in this observer.
		confirmed, uncertain := map[string]int{}, 0
		for i, info := range infos {
			if failures[i] == nil {
				if _, duplicate := confirmed[info.Name()]; !info.Valid() || duplicate {
					t.Fatal("conditional publication lost uniqueness")
				}
				confirmed[info.Name()] = i
				continue
			}
			var failure *storage.Error
			var transport *url.Error
			if !errors.As(failures[i], &failure) || failure.Code != "publish_failed" || failure.Outcome != storage.Uncertain || !errors.As(failures[i], &transport) || !info.Valid() || info.Size() != int64(len(fmt.Sprintf("content-%d", i))) || info.ContentMetadata().Version != "" {
				t.Fatal("transport failure lost its unverified publication candidate", failures[i])
			}
			t.Logf("observed transport interruption: %v", transport.Err)
			uncertain++
		}
		if len(confirmed) == 0 || len(confirmed)+uncertain != count {
			t.Fatal("concurrent publication did not exercise successful writes")
		}
		peer := s3fixture.Client(config)
		objects, err := peer.ListObjectsV2(t.Context(), &s3.ListObjectsV2Input{Bucket: aws.String(config.Bucket)})
		if err != nil || aws.ToBool(objects.IsTruncated) || len(objects.Contents) < len(confirmed) || len(objects.Contents) > count {
			t.Fatal("server inventory lost or duplicated a write", err)
		}
		seen := map[int]bool{}
		for _, object := range objects.Contents {
			response, err := peer.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: object.Key, ChecksumMode: types.ChecksumModeEnabled})
			if err != nil {
				t.Fatal(err)
			}
			content := string(liveS3Read(t, response.Body))
			var index int
			if _, err := fmt.Sscanf(content, "content-%d", &index); err != nil || index < 0 || index >= count || content != fmt.Sprintf("content-%d", index) || seen[index] {
				t.Fatal("native content was corrupted or published more than once")
			}
			seen[index] = true
			info := infos[index]
			digest := sha256.Sum256([]byte(content))
			if aws.ToString(object.Key) != config.Prefix+"/"+info.Name() || aws.ToString(response.ChecksumSHA256) != base64.StdEncoding.EncodeToString(digest[:]) || response.ChecksumType != types.ChecksumTypeFullObject || aws.ToString(response.VersionId) == "" {
				t.Fatal("native GET differs from its recorded publication candidate")
			}
			if failures[index] == nil && (info.Size() != int64(len(content)) || info.ContentMetadata().Version != fmt.Sprintf("sha256-%x", digest[:])) {
				t.Fatal("verified publication receipt differs from native content")
			}
		}
		for _, index := range confirmed {
			if !seen[index] {
				t.Fatal("confirmed write was overwritten or removed")
			}
		}
		versions, err := peer.ListObjectVersions(t.Context(), &s3.ListObjectVersionsInput{Bucket: aws.String(config.Bucket)})
		if err != nil || aws.ToBool(versions.IsTruncated) || len(versions.DeleteMarkers) != 0 || len(versions.Versions) != len(objects.Contents) {
			t.Fatal("concurrent writes replaced a version or compensated a candidate", err)
		}
		t.Logf("confirmed=%d uncertain=%d native_objects=%d", len(confirmed), uncertain, len(objects.Contents))
		rejectionTransport := http.DefaultTransport.(*http.Transport).Clone()
		rejectionTransport.DisableKeepAlives = true
		defer rejectionTransport.CloseIdleConnections()
		rejectionOptions := peer.Options()
		rejectionOptions.HTTPClient = &http.Client{Transport: rejectionTransport, Timeout: 10 * time.Second}
		rejectionPeer := s3.New(rejectionOptions)
		// The independent client confirms that the service enforces both the
		// conditional request and SHA256, rather than trusting GoDj's result.
		_, err = rejectionPeer.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(config.Bucket), Key: objects.Contents[0].Key, Body: strings.NewReader("replace"), IfNoneMatch: aws.String("*")})
		var api smithy.APIError
		if !errors.As(err, &api) || api.ErrorCode() != "PreconditionFailed" {
			t.Fatal("server did not enforce If-None-Match", err)
		}
		_, err = rejectionPeer.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String("invalid-checksum"), Body: strings.NewReader("bad digest"), ChecksumAlgorithm: types.ChecksumAlgorithmSha256, ChecksumSHA256: aws.String(base64.StdEncoding.EncodeToString(make([]byte, 32)))})
		if !errors.As(err, &api) || api.ErrorCode() != "XAmzContentChecksumMismatch" {
			t.Fatal("server accepted an incorrect SHA256", err)
		}
		if _, err := peer.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String("invalid-checksum")}); !errors.As(err, &api) || api.ErrorCode() != "NoSuchKey" {
			t.Fatal("checksum rejection left published content", err)
		}
		if _, err := first.Stat(t.Context(), "absent"); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("missing HEAD lost not-exist identity", err)
		}
		if err := first.Delete(t.Context(), "absent"); err != nil {
			t.Fatal("missing Delete was not idempotent", err)
		}
	})
	t.Run("versioned_reader", func(t *testing.T) {
		config := s3fixture.Config(t, true)
		backend, peer := liveS3(t, config), s3fixture.Client(config)
		original := "original content before replacement"
		info, err := backend.Save(t.Context(), "same.txt", strings.NewReader(original), storage.SaveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		opened, err := backend.Open(t.Context(), info.Name())
		if err != nil {
			t.Fatal(err)
		}
		defer opened.Close()
		reader, ok := opened.(storage.SeekableReader)
		if !ok || reader.Info().ContentMetadata().Version != info.ContentMetadata().Version || reader.Info().ContentMetadata().Modified.IsZero() {
			t.Fatal("versioned handle lost metadata or seeking")
		}
		independent, err := backend.Open(t.Context(), info.Name())
		if err != nil {
			t.Fatal(err)
		}
		one := make([]byte, 1)
		if n, err := reader.Read(one); err != nil || n != 1 || one[0] != original[0] {
			t.Fatal("initial read", err)
		}
		if _, err := peer.PutObject(t.Context(), &s3.PutObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(info.Name()), Body: strings.NewReader("replacement with different length")}); err != nil {
			t.Fatal(err)
		}
		if err := backend.Delete(t.Context(), info.Name()); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Open(t.Context(), info.Name()); !errors.Is(err, fs.ErrNotExist) {
			t.Fatal("delete marker still exposes current name", err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		if string(liveS3Read(t, independent)) != original {
			t.Fatal("independent GET followed replacement or backend Close")
		}
		for _, offset := range []int64{7, 2, 0, int64(len(original)) + 9} {
			if got, err := reader.Seek(offset, io.SeekStart); err != nil || got != offset {
				t.Fatal("seek immutable version", err)
			}
			content, err := io.ReadAll(reader)
			want := original[min(offset, int64(len(original))):]
			if err != nil || string(content) != want {
				t.Fatal("range followed mutable name or deleted version", err)
			}
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("unversioned_reader", func(t *testing.T) {
		backend := liveS3(t, s3fixture.Config(t, false))
		empty, err := backend.Save(t.Context(), "empty", strings.NewReader(""), storage.SaveOptions{})
		if err != nil || !empty.Valid() || empty.Size() != 0 {
			t.Fatal("empty object publication failed", err)
		}
		emptyReader, err := backend.Open(t.Context(), empty.Name())
		if err != nil {
			t.Fatal(err)
		}
		if len(liveS3Read(t, emptyReader)) != 0 {
			t.Fatal("empty object acquired content")
		}
		info, err := backend.Save(t.Context(), "without-version", strings.NewReader("snapshot"), storage.SaveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		reader, err := backend.Open(t.Context(), info.Name())
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := reader.(storage.SeekableReader); ok {
			t.Fatal("mutable name advertised immutable seeking")
		}
		if err := backend.Delete(t.Context(), info.Name()); err != nil {
			t.Fatal(err)
		}
		if err := backend.Close(); err != nil {
			t.Fatal(err)
		}
		if string(liveS3Read(t, reader)) != "snapshot" {
			t.Fatal("open body followed deletion")
		}
	})
	t.Run("signed_download", func(t *testing.T) {
		config := s3fixture.Config(t, false)
		config.Prefix = "private"
		backend := liveS3(t, config)
		info, err := backend.Save(t.Context(), "한글 문서%#.html", strings.NewReader("<html>private</html>"), storage.SaveOptions{})
		if err != nil {
			t.Fatal(err)
		}
		// SigV4 timestamps have second precision. Leave one full second for the
		// initial request even when signing happens just before a clock tick.
		resolver, err := backend.SignedURL(2 * time.Second)
		if err != nil {
			t.Fatal(err)
		}
		issued, err := resolver.URL(t.Context(), info.Name())
		if err != nil {
			t.Fatal(err)
		}
		fetch := func(method, location string, want int) []byte {
			t.Helper()
			req, err := http.NewRequestWithContext(t.Context(), method, location, nil)
			if err != nil {
				t.Fatal(err)
			}
			response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
			if err != nil {
				t.Fatal(err)
			}
			content := liveS3Read(t, response.Body)
			if response.StatusCode != want {
				t.Fatal("signed request status", response.StatusCode, want)
			}
			if want == 200 && (response.Header.Get("Content-Type") != "application/octet-stream" || response.Header.Get("Content-Disposition") != "attachment" || response.Header.Get("Cache-Control") != "private, no-store") {
				t.Fatal("signed URL lost download policy")
			}
			return content
		}
		if string(fetch("GET", issued, 200)) != "<html>private</html>" {
			t.Fatal("signed URL escaped a different object")
		}
		tampered, _ := url.Parse(issued)
		query := tampered.Query()
		query.Set("response-content-type", "text/html")
		tampered.RawQuery = query.Encode()
		fetch("GET", tampered.String(), 403)
		fetch("PUT", issued, 403)
		// A signature's lifetime is exercised against the independent service.
		timer := time.NewTimer(3100 * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
		fetch("GET", issued, 403)
	})
	t.Run("rejected_credentials", func(t *testing.T) {
		config := s3fixture.Config(t, false)
		config.Credentials = aws.CredentialsProviderFunc(func(context.Context) (aws.Credentials, error) {
			return aws.Credentials{AccessKeyID: "invalid-test-identity", SecretAccessKey: "invalid-test-secret"}, nil
		})
		backend := liveS3(t, config)
		info, err := backend.Save(t.Context(), "no-object", strings.NewReader("private"), storage.SaveOptions{})
		var failure *storage.Error
		if !errors.As(err, &failure) || failure.Outcome != storage.NotPublished || info.Name() != "" {
			t.Fatal("authentication rejection was not definite", err)
		}
	})
	t.Run("lost_publication_response", func(t *testing.T) {
		config := s3fixture.Config(t, false)
		peer := s3fixture.Client(config)
		var calls atomic.Int64
		lost := errors.New("synthetic response loss after server commit")
		transport := http.DefaultTransport.(*http.Transport).Clone()
		defer transport.CloseIdleConnections()
		config.Transport = liveS3Transport(func(request *http.Request) (*http.Response, error) {
			calls.Add(1)
			response, err := transport.RoundTrip(request)
			if err != nil {
				return response, err
			}
			if response.StatusCode != 200 {
				return response, nil
			}
			_, readErr := io.Copy(io.Discard, response.Body)
			return nil, errors.Join(lost, readErr, response.Body.Close())
		})
		backend := liveS3(t, config)
		info, err := backend.Save(t.Context(), "uncertain", strings.NewReader("published once"), storage.SaveOptions{})
		var failure *storage.Error
		if !errors.As(err, &failure) || failure.Outcome != storage.Uncertain || !errors.Is(err, lost) || info.Name() != "uncertain" || info.Size() != 14 || info.ContentMetadata().Version != "" || calls.Load() != 1 {
			t.Fatal("lost response was retried, hidden or reclassified", err, calls.Load())
		}
		response, err := peer.GetObject(t.Context(), &s3.GetObjectInput{Bucket: aws.String(config.Bucket), Key: aws.String(info.Name())})
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(liveS3Read(t, response.Body), []byte("published once")) {
			t.Fatal("uncertain publication was compensated or changed")
		}
	})
}

type liveS3Transport func(*http.Request) (*http.Response, error)

func (f liveS3Transport) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }
