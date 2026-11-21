package storage_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/progresshans/godj/storage"
)

type memoryReadFunc func([]byte) (int, error)

func (read memoryReadFunc) Read(p []byte) (int, error) { return read(p) }

type memoryInput struct {
	io.Reader
	closes int
}

func (source *memoryInput) Close() error { source.closes++; return nil }

func newMemory(t *testing.T, config storage.MemoryConfig) *storage.Memory {
	t.Helper()
	backend, err := storage.NewMemory(config)
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
func memorySave(t *testing.T, backend storage.Backend, name, contents string) storage.Info {
	t.Helper()
	info, err := backend.Save(t.Context(), name, strings.NewReader(contents), storage.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return info
}
func memoryRead(t *testing.T, backend storage.Backend, name string) string {
	t.Helper()
	reader, err := backend.Open(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	return string(data)
}

func TestMemoryStorageOwnsContentCursorsAndRetainedReaders(t *testing.T) {
	backend := newMemory(t, storage.MemoryConfig{})
	data := []byte("abcdef")
	info, err := backend.Save(t.Context(), "docs/한글.tar.gz", bytes.NewReader(data), storage.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	left, err := backend.Open(t.Context(), info.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer left.Close()
	var first [2]byte
	if _, err := io.ReadFull(left, first[:]); err != nil || string(first[:]) != "ab" {
		t.Fatal("storage borrowed source bytes", err)
	}
	if value := memoryRead(t, backend, info.Name()); value != "abcdef" {
		t.Fatal("second open shared a cursor", value)
	}
	if _, err := io.ReadFull(left, first[:]); err != nil || string(first[:]) != "cd" {
		t.Fatal("opening another reader reset the first", err)
	}
	collision := memorySave(t, backend, info.Name(), "second")
	if collision.Name() == info.Name() || !strings.HasSuffix(collision.Name(), ".tar.gz") || memoryRead(t, backend, info.Name()) != "abcdef" {
		t.Fatal("collision overwrote previous content")
	}
	if err := backend.Delete(t.Context(), info.Name()); err != nil {
		t.Fatal(err)
	}
	if _, err := backend.Stat(t.Context(), info.Name()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("deleted reference still published", err)
	}
	replacement := memorySave(t, backend, info.Name(), "new")
	if replacement.Name() != info.Name() {
		t.Fatal("deleted name remained occupied")
	}
	if err := backend.Close(); err != nil {
		t.Fatal(err)
	}
	remainder, err := io.ReadAll(left)
	if err != nil || string(remainder) != "ef" {
		t.Fatal("backend close/delete/name reuse changed an owned reader", err)
	}
	metadata, ok := left.(storage.Reader)
	if !ok || metadata.Info().Name() != info.Name() || metadata.Info().Size() != 6 {
		t.Fatal("opened-handle metadata changed")
	}
	if err := left.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := left.Read(first[:]); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("closed reader remained usable", err)
	}
	if metadata.Info().Size() != 6 {
		t.Fatal("closing reader discarded immutable metadata")
	}
	if _, err := backend.Open(t.Context(), replacement.Name()); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("closed store reopened content", err)
	}
	other := newMemory(t, storage.MemoryConfig{})
	if _, err := other.Stat(t.Context(), replacement.Name()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("new store shared global content", err)
	}
}

func TestMemoryStorageCapacityIncludesUnlinkedReadersAndEmptyFiles(t *testing.T) {
	for _, kind := range []string{"bytes", "files"} {
		t.Run(kind, func(t *testing.T) {
			config := storage.MemoryConfig{MaxBytes: 4, MaxFiles: 2}
			if kind == "files" {
				config.MaxBytes = 8
				config.MaxFiles = 1
			}
			backend := newMemory(t, config)
			memorySave(t, backend, "first", "1234")
			reader, err := backend.Open(t.Context(), "first")
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			if err := backend.Delete(t.Context(), "first"); err != nil {
				t.Fatal(err)
			}
			if info, err := backend.Save(t.Context(), "next", strings.NewReader("x"), storage.SaveOptions{}); info.Valid() || !errors.Is(err, &storage.Error{Code: "capacity_exceeded", Outcome: storage.NotPublished}) {
				t.Fatal("retained content escaped the capacity budget", err)
			}
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			memorySave(t, backend, "next", "1234")
			if err := backend.Delete(t.Context(), "next"); err != nil {
				t.Fatal(err)
			}
			memorySave(t, backend, "empty", "")
			if kind == "bytes" {
				memorySave(t, backend, "also-empty", "")
			}
			if _, err := backend.Save(t.Context(), "too-many", strings.NewReader(""), storage.SaveOptions{}); !errors.Is(err, &storage.Error{Code: "capacity_exceeded", Outcome: storage.NotPublished}) {
				t.Fatal("empty files bypassed file count", err)
			}
			if err := backend.Delete(t.Context(), "empty"); err != nil {
				t.Fatal(err)
			}
			memorySave(t, backend, "released", "")
		})
	}
}

func TestMemoryStorageRejectsInvalidConfigurationAndAdmissionBeforeInput(t *testing.T) {
	for _, config := range []storage.MemoryConfig{{MaxBytes: -1}, {MaxBytes: math.MaxInt64}, {MaxFiles: -1}, {MaxFiles: 1 << 21}, {MaxConcurrentSaves: -1}, {MaxFiles: 1, MaxConcurrentSaves: 2}, {Limits: storage.Limits{MaxFileBytes: -1}}, {Random: memoryReadFunc(nil)}} {
		if _, err := storage.NewMemory(config); err == nil {
			t.Fatal("invalid memory configuration accepted")
		}
	}
	backend := newMemory(t, storage.MemoryConfig{})
	unread := memoryReadFunc(func([]byte) (int, error) { t.Fatal("invalid admission consumed input"); return 0, io.EOF })
	for _, name := range []string{"", "../outside", "/absolute", "a\\b", ".godj-staging/a"} {
		if _, err := backend.Save(t.Context(), name, unread, storage.SaveOptions{}); !errors.Is(err, &storage.Error{Code: "invalid_name"}) {
			t.Fatal("invalid name admitted", err)
		}
	}
	if _, err := backend.Save(t.Context(), "a", unread, storage.SaveOptions{MaxLength: -1}); err == nil {
		t.Fatal("invalid name limit admitted")
	}
	var nilSource memoryReadFunc
	if _, err := backend.Save(t.Context(), "a", nilSource, storage.SaveOptions{}); !errors.Is(err, &storage.Error{Code: "invalid_input"}) {
		t.Fatal("typed nil input admitted", err)
	}
	if _, err := backend.Open(nil, "a"); !errors.Is(err, &storage.Error{Code: "invalid_context"}) {
		t.Fatal("nil context admitted", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := backend.Save(ctx, "a", unread, storage.SaveOptions{}); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled save consumed source", err)
	}
	memorySave(t, backend, "parent", "file")
	if _, err := backend.Save(t.Context(), "parent/child", unread, storage.SaveOptions{}); !errors.Is(err, &storage.Error{Code: "directory_failed"}) {
		t.Fatal("file was used as directory", err)
	}
	for _, value := range []any{backend, storage.MemoryConfig{Random: unread}} {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			if got := fmt.Sprintf(verb, value); strings.Contains(got, "parent") || strings.Contains(got, "file") {
				t.Fatal("memory formatting exposed content", got)
			}
		}
	}
}

func TestMemoryStorageFailedReadsReleaseCapacityWithoutPublication(t *testing.T) {
	sentinel := errors.New("private reader failure")
	for _, kind := range []string{"error", "panic", "cancel", "joined_eof", "negative_count", "large_count", "no_progress", "file_limit", "quota"} {
		t.Run(kind, func(t *testing.T) {
			config := storage.MemoryConfig{MaxBytes: 8, MaxFiles: 1, MaxConcurrentSaves: 1, Limits: storage.Limits{MaxFileBytes: 4}}
			if kind == "quota" {
				config.MaxBytes = 2
			}
			backend := newMemory(t, config)
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			reads := 0
			source := memoryReadFunc(func(p []byte) (int, error) {
				reads++
				if kind == "file_limit" || kind == "quota" {
					return copy(p, "oversized"), nil
				}
				if kind == "no_progress" {
					return 0, nil
				}
				if reads == 1 {
					return copy(p, "a"), nil
				}
				switch kind {
				case "error":
					return 0, sentinel
				case "panic":
					panic(sentinel)
				case "cancel":
					cancel()
					return 0, io.EOF
				case "joined_eof":
					return 0, errors.Join(io.EOF, sentinel)
				case "negative_count":
					return -1, nil
				case "large_count":
					return len(p) + 1, nil
				}
				return 0, io.EOF
			})
			var info storage.Info
			var err error
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				info, err = backend.Save(ctx, "failed", source, storage.SaveOptions{})
			}()
			if kind == "panic" {
				if panicked != sentinel {
					t.Fatal("source panic changed", panicked)
				}
			} else if err == nil || panicked != nil {
				t.Fatal("invalid source accepted", err, panicked)
			}
			if (kind == "error" || kind == "joined_eof") && !errors.Is(err, sentinel) {
				t.Fatal("read error identity lost", err)
			}
			if kind == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation identity lost", err)
			}
			if kind == "no_progress" && (!errors.Is(err, io.ErrNoProgress) || reads > 1024) {
				t.Fatal("no-progress read was unbounded", reads, err)
			}
			if info.Valid() {
				t.Fatal("failed read returned published metadata")
			}
			if _, err := backend.Stat(t.Context(), "failed"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("failed source exposed partial content", err)
			}
			recoveredContent := "full"
			if kind == "quota" {
				recoveredContent = "ok"
			}
			memorySave(t, backend, "recovered", recoveredContent)
			if memoryRead(t, backend, "recovered") != recoveredContent {
				t.Fatal("failed save retained capacity or input")
			}
		})
	}
}

func TestMemoryStorageNeverClosesBorrowedInput(t *testing.T) {
	for _, kind := range []string{"success", "error", "panic"} {
		t.Run(kind, func(t *testing.T) {
			backend := newMemory(t, storage.MemoryConfig{})
			sentinel := errors.New("private source failure")
			source := &memoryInput{Reader: memoryReadFunc(func(p []byte) (int, error) {
				if kind == "panic" {
					panic(sentinel)
				}
				if kind == "error" {
					return 0, sentinel
				}
				return copy(p, "content"), io.EOF
			})}
			var err error
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				_, err = backend.Save(t.Context(), "input", source, storage.SaveOptions{})
			}()
			if source.closes != 0 {
				t.Fatal("storage closed caller-owned input")
			}
			if kind == "error" && !errors.Is(err, sentinel) || kind == "panic" && panicked != sentinel || kind == "success" && (err != nil || panicked != nil) {
				t.Fatal("source result changed", err, panicked)
			}
			if err := source.Close(); err != nil || source.closes != 1 {
				t.Fatal("caller could not close its input", err)
			}
		})
	}
}

func TestMemoryStorageEntropyFailuresReleaseResources(t *testing.T) {
	for _, kind := range []string{"error", "panic", "collision_limit"} {
		t.Run(kind, func(t *testing.T) {
			sentinel := errors.New("private entropy failure")
			failed := false
			random := memoryReadFunc(func(p []byte) (int, error) {
				if !failed && kind != "collision_limit" {
					failed = true
					if kind == "panic" {
						panic(sentinel)
					}
					return 0, sentinel
				}
				clear(p)
				return len(p), nil
			})
			backend := newMemory(t, storage.MemoryConfig{Random: random, MaxBytes: 16, MaxFiles: 3, Limits: storage.Limits{MaxAttempts: 2}})
			memorySave(t, backend, "same.bin", "old")
			if kind == "collision_limit" {
				memorySave(t, backend, "same.bin", "other")
			}
			var err error
			var info storage.Info
			var panicked any
			func() {
				defer func() { panicked = recover() }()
				info, err = backend.Save(t.Context(), "same.bin", strings.NewReader("bad"), storage.SaveOptions{})
			}()
			if kind == "panic" {
				if panicked != sentinel {
					t.Fatal("entropy panic was swallowed")
				}
			} else if err == nil {
				t.Fatal("entropy failure accepted")
			}
			if kind == "error" && !errors.Is(err, sentinel) {
				t.Fatal("entropy error identity lost")
			}
			if kind == "collision_limit" && !errors.Is(err, &storage.Error{Code: "collision_limit", Outcome: storage.NotPublished}) {
				t.Fatal("collision retry not bounded", err)
			}
			if info.Valid() || memoryRead(t, backend, "same.bin") != "old" {
				t.Fatal("failed entropy replaced original")
			}
			// Use another colliding name after panic to exercise the same entropy lock.
			if kind == "panic" || kind == "error" {
				memorySave(t, backend, "same.bin", "new")
			} else {
				memorySave(t, backend, "free.bin", "ok")
			}
		})
	}
}

func TestMemoryStorageStagesPrivatelyAndBoundsConcurrentSaves(t *testing.T) {
	backend := newMemory(t, storage.MemoryConfig{MaxBytes: 64 << 10, MaxFiles: 4, MaxConcurrentSaves: 1, Limits: storage.Limits{MaxFileBytes: 8}})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	source := memoryReadFunc(func(p []byte) (int, error) {
		close(entered)
		select {
		case <-release:
			return copy(p, "ready"), io.EOF
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	})
	result := make(chan error, 1)
	go func() { _, err := backend.Save(ctx, "pending.bin", source, storage.SaveOptions{}); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("source not entered")
	}
	if _, err := backend.Open(t.Context(), "pending.bin"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("unfinished content became visible", err)
	}
	var touched bool
	unread := memoryReadFunc(func([]byte) (int, error) { touched = true; return 0, io.EOF })
	if _, err := backend.Save(t.Context(), "other.bin", unread, storage.SaveOptions{}); !errors.Is(err, &storage.Error{Code: "capacity_exceeded", Outcome: storage.NotPublished}) || touched {
		t.Fatal("concurrent source limit consumed input", err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("publication did not finish")
	}
	if memoryRead(t, backend, "pending.bin") != "ready" {
		t.Fatal("staged bytes lost")
	}
	memorySave(t, backend, "other.bin", "after")
}

func TestMemoryStorageConcurrentPublicationAndSerializedEntropy(t *testing.T) {
	var entropyCalls atomic.Int64
	entropy := memoryReadFunc(func(p []byte) (int, error) {
		if entropyCalls.Add(1) != 1 {
			entropyCalls.Add(-1)
			return 0, errors.New("concurrent entropy callback")
		}
		defer entropyCalls.Add(-1)
		runtime.Gosched()
		return rand.Read(p)
	})
	backend := newMemory(t, storage.MemoryConfig{Random: entropy, MaxBytes: 1 << 20, MaxFiles: 32})
	type saved struct {
		info    storage.Info
		content string
		err     error
	}
	results := make(chan saved, 16)
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Go(func() {
			text := fmt.Sprintf("payload-%d", i)
			info, err := backend.Save(t.Context(), "same.tar.gz", strings.NewReader(text), storage.SaveOptions{})
			results <- saved{info, text, err}
		})
	}
	workers.Wait()
	close(results)
	names := make(map[string]bool)
	for result := range results {
		if result.err != nil || names[result.info.Name()] || !strings.HasSuffix(result.info.Name(), ".tar.gz") {
			t.Fatal("concurrent publication collided", result.err)
		}
		names[result.info.Name()] = true
		if memoryRead(t, backend, result.info.Name()) != result.content {
			t.Fatal("concurrent content crossed ownership")
		}
	}
	if len(names) != 16 {
		t.Fatal("publication lost a caller")
	}
}

func TestMemoryStorageCloseWaitsForPendingSaveAndCopiesShareLifecycle(t *testing.T) {
	backend := newMemory(t, storage.MemoryConfig{})
	copied := *backend
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	saved := make(chan error, 1)
	go func() {
		_, err := backend.Save(ctx, "pending", memoryReadFunc(func(p []byte) (int, error) {
			close(entered)
			select {
			case <-release:
				return copy(p, "finished"), io.EOF
			case <-ctx.Done():
				return 0, ctx.Err()
			}
		}), storage.SaveOptions{})
		saved <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("pending save did not enter")
	}
	closing, closed := make(chan struct{}), make(chan error, 1)
	go func() { close(closing); closed <- copied.Close() }()
	<-closing
	select {
	case err := <-closed:
		t.Fatal("close returned while source was borrowed", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-saved:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("save did not finish")
	}
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("close did not finish")
	}
	if _, err := backend.Stat(t.Context(), "pending"); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("facade copy had a different lifecycle", err)
	}
}

func TestMemoryStorageNativeCommonBehaviorAndExplicitDeviations(t *testing.T) {
	raw, err := os.ReadFile("testdata/memory-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var native struct {
		Django, Python string
		Cases          []struct {
			Name, Stored, Hex string
			Size              int64
		}
		Isolated        bool `json:"instance_isolation"`
		SharedReaders   bool `json:"shared_readers"`
		Partial         bool `json:"partial_after_failure"`
		RecursiveDelete bool `json:"recursive_directory_delete"`
	}
	if err := json.Unmarshal(raw, &native); err != nil || native.Django != "6.1" || native.Python != "3.14.3" || len(native.Cases) != 3 || !native.Isolated || !native.SharedReaders || !native.Partial || !native.RecursiveDelete {
		t.Fatal("incomplete pinned memory reference", err)
	}
	backend := newMemory(t, storage.MemoryConfig{})
	for _, item := range native.Cases {
		// The observer encodes content as hex to preserve NUL and Unicode bytes.
		content, err := hex.DecodeString(item.Hex)
		if err != nil {
			t.Fatal(err)
		}
		info, err := backend.Save(t.Context(), item.Name, bytes.NewReader(content), storage.SaveOptions{})
		if err != nil || info.Name() != item.Stored || info.Size() != item.Size || fmt.Sprintf("%x", []byte(memoryRead(t, backend, info.Name()))) != item.Hex {
			t.Fatal("native common memory behavior differs", err)
		}
	}
	memorySave(t, backend, "folder/child", "child")
	if _, err := backend.Open(t.Context(), "folder"); !errors.Is(err, &storage.Error{Code: "not_regular"}) {
		t.Fatal("directory prefix became a file", err)
	}
	if err := backend.Delete(t.Context(), "folder"); !errors.Is(err, &storage.Error{Code: "not_regular"}) || memoryRead(t, backend, "folder/child") != "child" {
		t.Fatal("directory delete removed another file", err)
	}
	if name := memorySave(t, backend, "folder", "separate").Name(); name == "folder" {
		t.Fatal("file shadowed a live directory prefix")
	}
}
