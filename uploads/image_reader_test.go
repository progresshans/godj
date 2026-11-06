package uploads

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

type imageReaderProbe struct {
	*bytes.Reader
	read                   func([]byte) (int, error)
	reads, closes, seeks   int
	bytesRead, largestRead int
}

func (r *imageReaderProbe) Read(p []byte) (int, error) {
	r.reads++
	r.largestRead = max(r.largestRead, len(p))
	var n int
	var err error
	if r.read != nil {
		n, err = r.read(p)
	} else {
		n, err = r.Reader.Read(p)
	}
	if n >= 0 && n <= len(p) {
		r.bytesRead += n
	}
	return n, err
}
func (r *imageReaderProbe) Close() error { r.closes++; return nil }
func (r *imageReaderProbe) Seek(offset int64, whence int) (int64, error) {
	r.seeks++
	return r.Reader.Seek(offset, whence)
}

func TestInspectBorrowedImageReaderUsesCurrentCursorAndLeavesOwnershipWithCaller(t *testing.T) {
	content := imagePNG(t)
	prefix := []byte("unrelated prefix")
	r := &imageReaderProbe{Reader: bytes.NewReader(append(prefix, content...))}
	if _, err := r.Reader.Seek(int64(len(prefix)), io.SeekStart); err != nil {
		t.Fatal(err)
	}
	info, err := InspectImageReader(t.Context(), r, ImageLimits{MaxBytes: int64(len(content))})
	if err != nil || !info.Valid() || info.Width() != 3 || info.Height() != 2 || info.ContentType() != "image/png" {
		t.Fatal("borrowed inspection", err, info)
	}
	if r.closes != 0 || r.seeks != 0 || r.Reader.Len() != 0 || r.bytesRead != len(content) || r.largestRead > 32*1024 {
		t.Fatalf("borrowed lifetime or cursor changed: %+v", r)
	}
	file, err := NewFile("untrusted.bin", "untrusted/type", content)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := InspectImage(t.Context(), file, ImageLimits{})
	if err != nil || owned != info {
		t.Fatal("upload and borrowed content inspection diverged", err)
	}
	if err := r.Close(); err != nil || r.closes != 1 {
		t.Fatal("caller could not close borrowed reader", err)
	}
}

func TestInspectBorrowedImageReaderBoundsInputAndRejectsOperationalFailures(t *testing.T) {
	content := imagePNG(t)
	readFailure := errors.New("synthetic image source failed")
	for _, mode := range []string{"limit", "negative_count", "excess_count", "error_after_bytes", "joined_eof", "no_progress", "canceled_during_read", "exact_limit_with_eof", "temporary_empty_reads"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			r := &imageReaderProbe{Reader: bytes.NewReader(content)}
			limits := ImageLimits{MaxBytes: int64(len(content))}
			var want error
			switch mode {
			case "limit":
				limits.MaxBytes--
				want = &Error{Code: "image_bytes"}
			case "negative_count":
				r.read = func([]byte) (int, error) { return -1, nil }
				want = &Error{Code: "invalid_read_count"}
			case "excess_count":
				r.read = func(p []byte) (int, error) { return len(p) + 1, readFailure }
				want = &Error{Code: "invalid_read_count"}
			case "error_after_bytes", "joined_eof":
				r.read = func(p []byte) (int, error) {
					n, _ := r.Reader.Read(p)
					if mode == "joined_eof" {
						return n, errors.Join(io.EOF, readFailure)
					}
					return n, readFailure
				}
				want = readFailure
			case "no_progress":
				r.read = func([]byte) (int, error) { return 0, nil }
				want = io.ErrNoProgress
			case "canceled_during_read":
				r.read = func(p []byte) (int, error) { n, _ := r.Reader.Read(p); cancel(); return n, io.EOF }
				want = context.Canceled
			case "exact_limit_with_eof":
				r.read = func(p []byte) (int, error) { n, _ := r.Reader.Read(p); return n, io.EOF }
			case "temporary_empty_reads":
				r.read = func(p []byte) (int, error) {
					if r.reads < 4 {
						return 0, nil
					}
					return r.Reader.Read(p)
				}
			}
			info, err := InspectImageReader(ctx, r, limits)
			if want == nil {
				if err != nil || !info.Valid() {
					t.Fatal("valid reader rejected", err)
				}
			} else if info.Valid() || !errors.Is(err, want) {
				t.Fatal("read failure became image success", err, want)
			}
			if mode == "excess_count" && !errors.Is(err, readFailure) {
				t.Fatal("invalid byte count hid the source failure", err)
			}
			if r.closes != 0 || r.seeks != 0 || r.bytesRead > int(limits.MaxBytes)+1 || mode == "no_progress" && r.reads != 100 {
				t.Fatal("reader ownership or bounded progress violated", r.reads, r.bytesRead)
			}
		})
	}
	var typedNil *imageReaderProbe
	for _, reader := range []io.Reader{nil, typedNil} {
		if info, err := InspectImageReader(t.Context(), reader, ImageLimits{}); info.Valid() || !errors.Is(err, &Error{Code: "invalid_reader"}) {
			t.Fatal("nil reader accepted", err)
		}
	}
	var nilContext *imageCancelContext
	for _, ctx := range []context.Context{nil, nilContext} {
		r := &imageReaderProbe{Reader: bytes.NewReader(content)}
		if info, err := InspectImageReader(ctx, r, ImageLimits{}); info.Valid() || !errors.Is(err, &Error{Code: "invalid_context"}) || r.reads != 0 {
			t.Fatal("nil context reached reader", err)
		}
	}
	r := &imageReaderProbe{Reader: bytes.NewReader(content)}
	if info, err := InspectImageReader(t.Context(), r, ImageLimits{MaxBytes: -1}); info.Valid() || !errors.Is(err, &Error{Code: "invalid_image_limits"}) || r.reads != 0 {
		t.Fatal("invalid limits reached reader", err)
	}
}

func TestInspectBorrowedImageReaderUsesBoundedChunksAndChecksAllEncodedBytes(t *testing.T) {
	content := append(imagePNG(t), bytes.Repeat([]byte{0}, 100*1024)...)
	for _, bound := range []int64{int64(len(content)), int64(len(content) - 1)} {
		r := &imageReaderProbe{Reader: bytes.NewReader(content)}
		info, err := InspectImageReader(t.Context(), r, ImageLimits{MaxBytes: bound})
		if bound == int64(len(content)) {
			if err != nil || !info.Valid() || r.bytesRead != len(content) {
				t.Fatal("inspection stopped at image header or decoder end", err, r.bytesRead)
			}
		} else if info.Valid() || !errors.Is(err, &Error{Code: "image_bytes"}) || r.bytesRead != int(bound)+1 {
			t.Fatal("encoded tail escaped input limit", err, r.bytesRead)
		}
		if r.largestRead > 32*1024 || r.closes != 0 || r.seeks != 0 {
			t.Fatal("unbounded read or borrowed lifetime mutation")
		}
	}
}
