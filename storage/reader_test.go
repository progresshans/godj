package storage_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/storage"
)

func TestStorageReaderSeeksWithinItsOwnedContextAndContent(t *testing.T) {
	for _, mode := range []string{"filesystem", "memory"} {
		t.Run(mode, func(t *testing.T) {
			var backend storage.Backend
			directory := t.TempDir()
			if mode == "memory" {
				backend = newMemory(t, storage.MemoryConfig{})
			} else {
				root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: directory})
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = root.Close() })
				backend = root
			}
			memorySave(t, backend, "file.bin", "0123456789")
			modified := time.Date(2025, 2, 3, 4, 5, 6, 0, time.UTC)
			if mode == "filesystem" {
				if err := os.Chtimes(filepath.Join(directory, "file.bin"), modified, modified); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			opened, err := backend.Open(ctx, "file.bin")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = opened.Close() })
			reader, ok := opened.(storage.SeekableReader)
			if !ok {
				t.Fatal("built-in reader does not support ranges")
			}
			info := reader.Info()
			metadata := info.ContentMetadata()
			if metadata.Modified.IsZero() || metadata.ModifiedStrong || mode == "filesystem" && (!metadata.Modified.Equal(modified) || metadata.Version != "") || mode == "memory" && metadata.Version == "" {
				t.Fatal("content validators do not describe the opened backend")
			}
			if err := backend.Delete(ctx, "file.bin"); err != nil {
				t.Fatal(err)
			}
			memorySave(t, backend, "file.bin", "replacement")
			for _, step := range []struct {
				offset   int64
				whence   int
				position int64
				want     string
			}{{2, io.SeekStart, 2, "23"}, {1, io.SeekCurrent, 5, "56"}, {-2, io.SeekEnd, 8, "89"}, {12, io.SeekStart, 12, ""}, {0, io.SeekStart, 0, "01"}} {
				position, err := reader.Seek(step.offset, step.whence)
				if err != nil || position != step.position {
					t.Fatal("seek position", position, err)
				}
				var got [2]byte
				n, err := reader.Read(got[:])
				if err != nil && err != io.EOF || string(got[:n]) != step.want {
					t.Fatal("seek switched content after name reuse", string(got[:n]), err)
				}
			}
			if reader.Info() != info {
				t.Fatal("opened metadata followed a reused name")
			}
			if _, err := reader.Seek(-1, io.SeekStart); err == nil {
				t.Fatal("negative seek accepted")
			}
			if _, err := reader.Seek(0, 17); err == nil {
				t.Fatal("invalid seek origin accepted")
			}
			cancel()
			if _, err := reader.Seek(0, io.SeekStart); !errors.Is(err, context.Canceled) {
				t.Fatal("seek ignored its Open context", err)
			}
			if err := opened.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := reader.Seek(0, io.SeekStart); !errors.Is(err, &storage.Error{Code: "closed"}) {
				t.Fatal("closed reader still seeks", err)
			}
			if reader.Info() != info {
				t.Fatal("close erased the metadata snapshot")
			}
		})
	}
}

func TestStorageContentMetadataIsExplicitAndMemoryVersionsFollowBytes(t *testing.T) {
	info, err := storage.NewInfo("file.bin", 3)
	if err != nil || info.ContentMetadata() != (storage.ContentMetadata{}) {
		t.Fatal("unspecified metadata inferred from a name", err)
	}
	for _, value := range []storage.ContentMetadata{{Version: strings.Repeat("x", 1025)}, {ModifiedStrong: true}, {Modified: time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)}, {Modified: time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)}} {
		if _, err := info.WithContentMetadata(value); err == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
	if _, err := (storage.Info{}).WithContentMetadata(storage.ContentMetadata{}); err == nil {
		t.Fatal("metadata made an invalid Info valid")
	}
	value := storage.ContentMetadata{Version: "opaque\x00version", Modified: time.Date(2025, 1, 2, 3, 4, 5, 0, time.FixedZone("local", 3600)), ModifiedStrong: true}
	derived, err := info.WithContentMetadata(value)
	if err != nil || derived.Name() != info.Name() || derived.Size() != info.Size() || !derived.ContentMetadata().Modified.Equal(value.Modified) || !derived.ContentMetadata().ModifiedStrong || info.ContentMetadata() != (storage.ContentMetadata{}) {
		t.Fatal("metadata mutated the source snapshot", err)
	}
	backend := newMemory(t, storage.MemoryConfig{})
	first := memorySave(t, backend, "same.bin", "old")
	same := memorySave(t, backend, "other.bin", "old")
	if first.ContentMetadata().Version == "" || same.ContentMetadata().Version != first.ContentMetadata().Version {
		t.Fatal("version is not a content identity")
	}
	opened, err := backend.Open(t.Context(), first.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer opened.Close()
	if err := backend.Delete(t.Context(), first.Name()); err != nil {
		t.Fatal(err)
	}
	replacement := memorySave(t, backend, first.Name(), "new")
	if replacement.ContentMetadata().Version == first.ContentMetadata().Version || opened.(storage.Reader).Info() != first {
		t.Fatal("version followed a reusable name or only its byte count")
	}
}

func TestStorageReaderConcurrentCursorOperationsAndClose(t *testing.T) {
	backend := newMemory(t, storage.MemoryConfig{})
	memorySave(t, backend, "file.bin", strings.Repeat("bytes", 1024))
	opened, err := backend.Open(t.Context(), "file.bin")
	if err != nil {
		t.Fatal(err)
	}
	reader := opened.(storage.SeekableReader)
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				_, _ = reader.Seek(0, io.SeekStart)
				var buffer [3]byte
				_, _ = reader.Read(buffer[:])
				if reader.Info().Size() != 5120 {
					t.Error("cursor operation mutated metadata")
				}
			}
		})
	}
	workers.Go(func() { _ = reader.Close() })
	workers.Wait()
	if _, err := reader.Seek(0, io.SeekStart); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("close did not end concurrent cursor operations", err)
	}
}
