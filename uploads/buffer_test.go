package uploads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
)

func TestBufferOwnsContentAndExpiresEveryReader(t *testing.T) {
	input := []byte("private buffer content")
	buffer, err := NewBuffer("private-document.txt", "text/plain", input)
	if err != nil {
		t.Fatal(err)
	}
	file := buffer.File()
	owned := file.state.data
	clear(input)
	reader, err := file.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if err != nil || string(content) != "private buffer content" {
		t.Fatal("buffer borrowed input", err)
	}
	second, err := file.Open(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := buffer.Close(); err != nil {
		t.Fatal("close was not idempotent", err)
	}
	if !bytes.Equal(owned, make([]byte, len(owned))) {
		t.Fatal("closed owner retained its mutable content")
	}
	for _, held := range []*Reader{reader, second} {
		if _, err := held.Read(make([]byte, 1)); !errors.Is(err, &Error{Code: "closed"}) {
			t.Fatal("reader survived owner", err)
		}
		if err := held.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := file.Open(t.Context()); !errors.Is(err, &Error{Code: "closed"}) {
		t.Fatal("capability reopened expired input", err)
	}
	if strings.Contains(fmt.Sprintf("%#v", buffer), "private") {
		t.Fatal("buffer formatted private metadata")
	}
	var absent *Buffer
	if absent.File().Valid() || absent.Close() != nil {
		t.Fatal("zero buffer owns resources")
	}
}

func TestBufferCancellationAndConcurrentCloseShareLifetime(t *testing.T) {
	buffer, err := NewBuffer("empty.txt", "", nil)
	if err != nil || !buffer.File().Valid() || buffer.File().Size() != 0 {
		t.Fatal("empty upload became omission", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := buffer.File().Open(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled reader opened", err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			reader, err := buffer.File().Open(t.Context())
			if err != nil && !errors.Is(err, &Error{Code: "closed"}) {
				t.Error(err)
			}
			if reader != nil {
				_, err = reader.Read(make([]byte, 1))
				if err != io.EOF && !errors.Is(err, &Error{Code: "closed"}) {
					t.Error(err)
				}
				_ = reader.Close()
			}
			_ = buffer.Close()
		})
	}
	workers.Wait()
	if _, err := buffer.File().Open(t.Context()); !errors.Is(err, &Error{Code: "closed"}) {
		t.Fatal("copies did not share lifetime", err)
	}
}
