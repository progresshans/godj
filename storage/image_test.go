package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/storage"
	"github.com/progresshans/godj/uploads"
)

func storedPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewNRGBA(image.Rect(0, 0, width, height))); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

type imageOpenProbe struct {
	storage.Backend
	open  func(context.Context, string) (io.ReadCloser, error)
	opens int
}

func (backend *imageOpenProbe) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	backend.opens++
	return backend.open(ctx, name)
}

type inspectedReaderProbe struct {
	io.Reader
	metadata             storage.Info
	info                 func() storage.Info
	read                 func([]byte) (int, error)
	close                func() error
	reads, closes, infos int
}

func (reader *inspectedReaderProbe) Read(p []byte) (int, error) {
	reader.reads++
	if reader.read != nil {
		return reader.read(p)
	}
	return reader.Reader.Read(p)
}
func (reader *inspectedReaderProbe) Close() error {
	reader.closes++
	if reader.close != nil {
		return reader.close()
	}
	return nil
}
func (reader *inspectedReaderProbe) Info() storage.Info {
	reader.infos++
	if reader.info != nil {
		return reader.info()
	}
	return reader.metadata
}

// Exposes only the minimum Backend.Open contract, deliberately hiding Info.
type imageReaderWithoutInfo struct{ io.ReadCloser }

func TestStoredImageInspectionUsesOneOwnedHandleAndDoesNotCacheReusableNames(t *testing.T) {
	for _, kind := range []string{"filesystem", "memory"} {
		t.Run(kind, func(t *testing.T) {
			var backend storage.Backend = newMemory(t, storage.MemoryConfig{})
			if kind == "filesystem" {
				backend = openStorage(t, t.TempDir(), storage.FilesystemConfig{})
			}
			name := "images/private.png"
			original := storedPNG(t, 3, 2)
			memorySave(t, backend, name, string(original))
			borrowed, err := backend.Open(t.Context(), name)
			if err != nil {
				t.Fatal(err)
			}
			defer borrowed.Close()
			var prefix [2]byte
			if _, err := io.ReadFull(borrowed, prefix[:]); err != nil {
				t.Fatal(err)
			}
			var probe *inspectedReaderProbe
			open := &imageOpenProbe{open: func(ctx context.Context, got string) (io.ReadCloser, error) {
				if got != name || ctx != t.Context() {
					t.Fatal("inspection lost explicit name/context")
				}
				r, err := backend.Open(ctx, got)
				if err != nil {
					return nil, err
				}
				probe = &inspectedReaderProbe{Reader: r, metadata: r.(storage.Reader).Info(), close: r.Close}
				if err := backend.Delete(ctx, name); err != nil {
					t.Fatal(err)
				}
				memorySave(t, backend, name, string(storedPNG(t, 7, 5)))
				return probe, nil
			}}
			result, err := storage.InspectImage(t.Context(), open, name, uploads.ImageLimits{})
			if err != nil || !result.Valid() || result.File().Name() != name || result.File().Size() != int64(len(original)) || result.Image().Width() != 3 || result.Image().Height() != 2 {
				t.Fatal("inspection followed reused name instead of opened content", err)
			}
			if open.opens != 1 || probe.closes != 1 || probe.infos != 1 || result.File() != probe.metadata {
				t.Fatal("inspection opened/stat-ed/closed outside its owned reader", open.opens, probe.closes, probe.infos)
			}
			rest, err := io.ReadAll(borrowed)
			if err != nil || !bytes.Equal(append(prefix[:], rest...), original) {
				t.Fatal("inspection consumed or closed another reader", err)
			}
			again, err := storage.InspectImage(t.Context(), backend, name, uploads.ImageLimits{})
			if err != nil || again.Image().Width() != 7 || again.Image().Height() != 5 {
				t.Fatal("reinspection reused a stale dimensions cache", err)
			}
			if strings.Contains(fmt.Sprintf("%+v %#v", result, result), name) || (storage.ImageInspection{}).Valid() {
				t.Fatal("inspection formatting leaked a path or zero became valid")
			}
		})
	}
}

func TestStoredImageInspectionMeasuresReadersWithoutMetadata(t *testing.T) {
	content := storedPNG(t, 4, 3)
	reader := &inspectedReaderProbe{Reader: bytes.NewReader(content)}
	backend := &imageOpenProbe{open: func(context.Context, string) (io.ReadCloser, error) { return imageReaderWithoutInfo{reader}, nil }}
	result, err := storage.InspectImage(t.Context(), backend, "image.dat", uploads.ImageLimits{MaxBytes: int64(len(content))})
	if err != nil || !result.Valid() || result.File().Size() != int64(len(content)) || result.File().Name() != "image.dat" || result.Image().Width() != 4 || result.Image().Height() != 3 || result.File().ContentMetadata() != (storage.ContentMetadata{}) {
		t.Fatal("unknown metadata was invented or inspection lost measured bytes", err)
	}
	if backend.opens != 1 || reader.infos != 0 || reader.closes != 1 {
		t.Fatal("optional metadata became a backend requirement")
	}
}

func TestStoredImageInspectionRejectsFailuresAndAlwaysClosesItsAcquiredReader(t *testing.T) {
	content := storedPNG(t, 3, 2)
	openFailure, readFailure, closeFailure := errors.New("private open detail"), errors.New("private read detail"), errors.New("private close detail")
	for _, mode := range []string{"open_error", "open_error_with_reader", "nil_reader", "typed_nil_reader", "invalid_info", "different_name", "declared_limit", "short", "long", "empty_metadata", "read_error", "joined_eof", "invalid_read_count", "corrupt", "header_only", "close_error", "combined_error", "canceled_after_open", "canceled_on_close"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			metadata, err := storage.NewInfo("image.png", int64(len(content)))
			if err != nil {
				t.Fatal(err)
			}
			reader := &inspectedReaderProbe{Reader: bytes.NewReader(content), metadata: metadata}
			var returned io.ReadCloser = reader
			var openErr error
			want := error(&storage.Error{Code: "invalid_result"})
			wantCloses, wantReads := 1, -1
			limits := uploads.ImageLimits{}
			switch mode {
			case "open_error":
				returned, openErr, want, wantCloses, wantReads = nil, openFailure, openFailure, 0, 0
			case "open_error_with_reader":
				openErr, want, wantReads = openFailure, openFailure, 0
			case "nil_reader":
				returned, wantCloses, wantReads, want = nil, 0, 0, &storage.Error{Code: "invalid_reader"}
			case "typed_nil_reader":
				var missing *inspectedReaderProbe
				returned, wantCloses, wantReads, want = missing, 0, 0, &storage.Error{Code: "invalid_reader"}
			case "invalid_info":
				reader.metadata, wantReads = storage.Info{}, 0
			case "different_name":
				reader.metadata, _ = storage.NewInfo("other.png", int64(len(content)))
				wantReads = 0
			case "declared_limit":
				limits.MaxBytes = int64(len(content) - 1)
				want, wantReads = &uploads.Error{Code: "image_bytes"}, 0
			case "short":
				reader.metadata, _ = storage.NewInfo("image.png", int64(len(content)+1))
			case "long":
				reader.metadata, _ = storage.NewInfo("image.png", int64(len(content)-1))
			case "empty_metadata":
				reader.metadata, _ = storage.NewInfo("image.png", 0)
			case "read_error", "joined_eof", "combined_error":
				reader.read = func(p []byte) (int, error) {
					n, _ := reader.Reader.Read(p)
					if mode == "joined_eof" {
						return n, errors.Join(io.EOF, readFailure)
					}
					return n, readFailure
				}
				want = readFailure
				if mode == "combined_error" {
					reader.close = func() error { return closeFailure }
				}
			case "invalid_read_count":
				reader.metadata, _ = storage.NewInfo("image.png", 0)
				reader.read = func(p []byte) (int, error) { return len(p) + 1, nil }
				want = &storage.Error{Code: "invalid_read_count"}
			case "corrupt":
				reader.Reader = bytes.NewReader(bytes.Repeat([]byte("x"), len(content)))
				want = &uploads.Error{Code: "unsupported_image"}
			case "header_only":
				reader.Reader = bytes.NewReader(content[:41])
				reader.metadata, _ = storage.NewInfo("image.png", 41)
				want = &uploads.Error{Code: "invalid_image"}
			case "close_error":
				reader.close = func() error { return closeFailure }
				want = closeFailure
			case "canceled_after_open":
				want, wantReads = context.Canceled, 0
			case "canceled_on_close":
				reader.close = func() error { cancel(); return nil }
				want = context.Canceled
			}
			backend := &imageOpenProbe{open: func(context.Context, string) (io.ReadCloser, error) {
				if mode == "canceled_after_open" {
					cancel()
				}
				return returned, openErr
			}}
			result, err := storage.InspectImage(ctx, backend, "image.png", limits)
			if result.Valid() || result != (storage.ImageInspection{}) || !errors.Is(err, want) || reader.closes != wantCloses || wantReads >= 0 && reader.reads != wantReads {
				t.Fatal("inspection failure lost result/lifetime/cause", err, want, reader.closes, reader.reads)
			}
			if mode == "combined_error" && !errors.Is(err, closeFailure) {
				t.Fatal("Close replaced or hid the read failure", err)
			}
			if strings.Contains(fmt.Sprintf("%+v %#v", err, err), "private") {
				t.Fatal("operational formatting exposed backend details")
			}
		})
	}
}

func TestStoredImageInspectionPreflightDoesNotOpenStorage(t *testing.T) {
	for _, mode := range []string{"nil_context", "typed_nil_context", "canceled", "limits", "name", "nil_backend", "typed_nil_backend"} {
		t.Run(mode, func(t *testing.T) {
			probe := &imageOpenProbe{open: func(context.Context, string) (io.ReadCloser, error) {
				t.Fatal("invalid request opened storage")
				return nil, nil
			}}
			var backend storage.Backend = probe
			ctx, name, limits := t.Context(), "image.png", uploads.ImageLimits{}
			var want error
			switch mode {
			case "nil_context":
				ctx, want = nil, &storage.Error{Code: "invalid_context"}
			case "typed_nil_context":
				var missing *storageNilContext
				ctx, want = missing, &storage.Error{Code: "invalid_context"}
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
				want = context.Canceled
			case "limits":
				limits.MaxPixels, want = -1, &uploads.Error{Code: "invalid_image_limits"}
			case "name":
				name, want = "../image.png", &storage.Error{Code: "invalid_name"}
			case "nil_backend":
				backend, want = nil, &storage.Error{Code: "invalid_backend"}
			case "typed_nil_backend":
				var missing *imageOpenProbe
				backend, want = missing, &storage.Error{Code: "invalid_backend"}
			}
			if result, err := storage.InspectImage(ctx, backend, name, limits); result.Valid() || !errors.Is(err, want) || probe.opens != 0 {
				t.Fatal("preflight failure", err, want, probe.opens)
			}
		})
	}
}

type storageNilContext struct{ context.Context }

func TestStoredImageInspectionClosesOnCallbackPanic(t *testing.T) {
	for _, mode := range []string{"read", "info"} {
		t.Run(mode, func(t *testing.T) {
			info, _ := storage.NewInfo("image.png", 3)
			reader := &inspectedReaderProbe{Reader: strings.NewReader("png"), metadata: info}
			marker := &struct{}{}
			if mode == "read" {
				reader.read = func([]byte) (int, error) { panic(marker) }
			} else {
				reader.info = func() storage.Info { panic(marker) }
			}
			backend := &imageOpenProbe{open: func(context.Context, string) (io.ReadCloser, error) { return reader, nil }}
			func() {
				defer func() {
					if got := recover(); got != marker {
						t.Fatal("callback panic changed", got)
					}
				}()
				_, _ = storage.InspectImage(t.Context(), backend, "image.png", uploads.ImageLimits{})
			}()
			if reader.closes != 1 {
				t.Fatal("panic retained the opened reader", reader.closes)
			}
		})
	}
}

func TestStoredImageInspectionConcurrentCallsHaveIndependentReaders(t *testing.T) {
	backend := newMemory(t, storage.MemoryConfig{})
	content := storedPNG(t, 8, 5)
	memorySave(t, backend, "concurrent.png", string(content))
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			result, err := storage.InspectImage(t.Context(), backend, "concurrent.png", uploads.ImageLimits{})
			if err != nil || !result.Valid() || result.Image().Width() != 8 || result.Image().Height() != 5 || result.File().Size() != int64(len(content)) {
				t.Error("concurrent inspection shared mutable reader state", err)
			}
		})
	}
	workers.Wait()
}
