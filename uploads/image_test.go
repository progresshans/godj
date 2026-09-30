package uploads

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"os"
	"strings"
	"testing"
)

func imagePNG(t *testing.T) []byte {
	t.Helper()
	var out bytes.Buffer
	if err := png.Encode(&out, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func imageMultipart(t *testing.T, content []byte, memory int64) (*Form, File, string) {
	t.Helper()
	var out bytes.Buffer
	writer := multipart.NewWriter(&out)
	part, err := writer.CreateFormFile("image", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write(content); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	config := DefaultConfig()
	config.MemoryBytes = memory
	config.TempDir = t.TempDir()
	form, err := Parse(t.Context(), bytes.NewReader(out.Bytes()), writer.FormDataContentType(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := form.Close(); err != nil {
			t.Error(err)
		}
	})
	return form, form.Files()["image"][0], config.TempDir
}
func TestInspectImageOwnsReaderAndPreservesUploadLifetime(t *testing.T) {
	for _, memory := range []int64{0, 1024} {
		t.Run(fmt.Sprint(memory), func(t *testing.T) {
			content := imagePNG(t)
			form, file, root := imageMultipart(t, content, memory)
			reader, err := file.Open(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			prefix := make([]byte, 2)
			if _, err = io.ReadFull(reader, prefix); err != nil {
				t.Fatal(err)
			}
			info, err := InspectImage(t.Context(), file, ImageLimits{})
			if err != nil || !info.Valid() || info.Width() != 3 || info.Height() != 2 || info.Frames() != 1 || info.ContentType() != "image/png" {
				t.Fatal("inspection failed", err, info.Width(), info.Height())
			}
			file.state.owner.mu.Lock()
			readers := len(file.state.owner.readers)
			file.state.owner.mu.Unlock()
			if readers != 1 {
				t.Fatal("inspection retained its reader", readers)
			}
			rest, err := io.ReadAll(reader)
			if err != nil || !bytes.Equal(append(prefix, rest...), content) {
				t.Fatal("inspection consumed another cursor", err)
			}
			if err = form.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = InspectImage(t.Context(), file, ImageLimits{}); !errors.Is(err, &Error{Code: "closed"}) {
				t.Fatal("closed upload became image diagnostics", err)
			}
			if entries, err := os.ReadDir(root); err != nil || len(entries) != 0 {
				t.Fatal("temporary image retained", err, len(entries))
			}
		})
	}
}
func TestInspectImageOperationalFailuresAndCancellation(t *testing.T) {
	content := imagePNG(t)
	_, file, _ := imageMultipart(t, content, 0)
	if err := os.Remove(file.state.path); err != nil {
		t.Fatal(err)
	}
	if info, err := InspectImage(t.Context(), file, ImageLimits{}); info.Valid() || !errors.Is(err, &Error{Code: "open_failed"}) {
		t.Fatal("missing staging file became validation", err)
	}
	source, _ := NewFile("photo.png", "image/png", content)
	for _, delta := range []int64{-1, 1} {
		tampered := *source.state
		tampered.size += delta
		if _, err := InspectImage(t.Context(), File{&tampered}, ImageLimits{}); !errors.Is(err, &Error{Code: "image_read_failed"}) {
			t.Fatal("source size mismatch", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := InspectImage(ctx, source, ImageLimits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("initial cancellation lost", err)
	}
	var typedNil *imageCancelContext
	for _, ctx := range []context.Context{nil, typedNil} {
		if _, err := InspectImage(ctx, source, ImageLimits{}); !errors.Is(err, &Error{Code: "invalid_context"}) {
			t.Fatal("nil context accepted", err)
		}
	}
	delayed := &imageCancelContext{Context: t.Context(), after: 5}
	if info, err := InspectImage(delayed, source, ImageLimits{}); info.Valid() || !errors.Is(err, context.Canceled) || delayed.calls < 5 {
		t.Fatal("decoder cancellation lost", err, delayed.calls)
	}
	if _, err := InspectImage(t.Context(), File{}, ImageLimits{}); !errors.Is(err, &Error{Code: "invalid_file"}) {
		t.Fatal("zero file accepted", err)
	}
	if _, err := InspectImage(t.Context(), source, ImageLimits{MaxFrames: -1}); !errors.Is(err, &Error{Code: "invalid_image_limits"}) {
		t.Fatal("invalid limits accepted", err)
	}
	// Ordinary formatting does not disclose filenames, decoder text, or dimensions.
	message := fmt.Sprintf("%+v %#v %v", &Error{Code: "invalid_image", Cause: errors.New("private raw bytes")}, ImageInfo{"png", 3, 2, 1}, source)
	if strings.Contains(message, "private") || strings.Contains(message, "photo.png") || strings.Contains(message, "width") {
		t.Fatal("metadata leaked", message)
	}
}

type imageCancelContext struct {
	context.Context
	calls, after int
}

func (c *imageCancelContext) Err() error {
	c.calls++
	if c.calls >= c.after {
		return context.Canceled
	}
	return nil
}

func TestInspectImageRejectsHeaderOnlyPixelBombsAndMalformedContainers(t *testing.T) {
	content := imagePNG(t)
	binary.BigEndian.PutUint32(content[16:20], 65535)
	binary.BigEndian.PutUint32(content[20:24], 65535)
	binary.BigEndian.PutUint32(content[29:33], crc32.ChecksumIEEE(content[12:29]))
	file, _ := NewFile("bomb.png", "image/png", content)
	if _, err := InspectImage(t.Context(), file, ImageLimits{}); !errors.Is(err, &Error{Code: "image_pixels"}) {
		t.Fatal("pixel budget checked after decoding", err)
	}
	raw, err := os.ReadFile("../forms/testdata/image-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Payloads map[string]string }
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	webp, err := base64.StdEncoding.DecodeString(fixture.Payloads["webp"])
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"riff_size", "trailing_header", "duplicate_image", "late_animation", "chunk_overflow", "odd_missing_pad"} {
		t.Run(kind, func(t *testing.T) {
			malformed := bytes.Clone(webp)
			switch kind {
			case "riff_size":
				binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)))
			case "trailing_header":
				malformed = append(malformed, 'X')
				binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)-8))
			case "duplicate_image":
				malformed = append(malformed, webp[12:]...)
				binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)-8))
			case "late_animation":
				malformed = append(malformed, []byte{'A', 'N', 'I', 'M', 0, 0, 0, 0}...)
				binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)-8))
			case "chunk_overflow":
				binary.LittleEndian.PutUint32(malformed[16:20], ^uint32(0))
			case "odd_missing_pad":
				malformed = append(malformed, []byte{'J', 'U', 'N', 'K', 1, 0, 0, 0, 0}...)
				binary.LittleEndian.PutUint32(malformed[4:8], uint32(len(malformed)-8))
			}
			file, _ := NewFile("malformed.webp", "image/webp", malformed)
			info, err := InspectImage(t.Context(), file, ImageLimits{})
			want := "invalid_image"
			if kind == "late_animation" {
				want = "unsupported_image"
			}
			if info.Valid() || !errors.Is(err, &Error{Code: want}) {
				t.Fatal("outer framing ignored", err)
			}
		})
	}
	for _, format := range []string{"png", "animated_png", "jpeg", "gif", "animated_gif", "webp"} {
		data, err := base64.StdEncoding.DecodeString(fixture.Payloads[format])
		if err != nil {
			t.Fatal(err)
		}
		file, _ := NewFile("any.bin", "untrusted", data)
		info, err := InspectImage(t.Context(), file, ImageLimits{})
		frames := 1
		if format == "animated_gif" || format == "animated_png" {
			frames = 2
		}
		if err != nil || info.Width() != 3 || info.Height() != 2 || info.Frames() != frames {
			t.Fatal("pixel decoding/frames lost", format, err, info.Frames())
		}
	}
}

func FuzzInspectImage(f *testing.F) {
	raw, err := os.ReadFile("../forms/testdata/image-django61.json")
	if err != nil {
		f.Fatal(err)
	}
	var fixture struct{ Payloads map[string]string }
	if err = json.Unmarshal(raw, &fixture); err != nil {
		f.Fatal(err)
	}
	for _, name := range []string{"png", "jpeg", "gif", "animated_gif", "webp", "webp_alpha", "webp_lossless", "corrupt_later_gif"} {
		data, err := base64.StdEncoding.DecodeString(fixture.Payloads[name])
		if err != nil {
			f.Fatal(err)
		}
		f.Add(data)
	}

	f.Add([]byte("GIF89a"))
	f.Add([]byte("\x89PNG\r\n\x1a\n"))
	f.Add([]byte("RIFF\x04\x00\x00\x00WEBP"))
	f.Add([]byte("\xff\xd8\xff\xd9"))
	codecs := codecReference(f)
	for _, observed := range codecs.Cases {
		f.Add(codecPayload(f, codecs, observed.Name))
	}
	shared, _ := sharedTIFFBlocks(f, 2)
	f.Add(shared)
	animations := apngFixture(f)
	for _, observed := range animations.Cases {
		f.Add(apngPayload(f, animations, observed.Name))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 1<<20 {
			return
		}
		file, err := NewFile("image.bin", "application/octet-stream", data)
		if err != nil {
			t.Fatal(err)
		}
		info, err := InspectImage(t.Context(), file, ImageLimits{MaxBytes: 1 << 20, MaxWidth: 128, MaxHeight: 128, MaxPixels: 16384, MaxFrames: 4, MaxTotalPixels: 32768})
		if err == nil && (!info.Valid() || info.Width() > 128 || info.Height() > 128 || info.Frames() > 4) {
			t.Fatal("successful inspection escaped limits")
		}
	})
}

type imageCloseFailure struct {
	io.Closer
	cause error
}

func (closer imageCloseFailure) Close() error {
	return errors.Join(closer.Closer.Close(), closer.cause)
}

type imageCloseContext struct {
	context.Context
	owned *owner
	armed bool
	cause error
}

func (ctx *imageCloseContext) Err() error {
	// Open invokes Err before registering a reader. Read invokes it under the
	// reader lock, after registration. This owned test hook injects a real close
	// error without exposing arbitrary providers in the public upload capability.
	if !ctx.armed && len(ctx.owned.readers) == 1 {
		for reader := range ctx.owned.readers {
			reader.closer = imageCloseFailure{reader.closer, ctx.cause}
			ctx.armed = true
		}
	}
	return nil
}
func TestInspectImageCloseFailureInvalidatesMetadataAndKeepsBothCauses(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprint(invalid), func(t *testing.T) {
			content := imagePNG(t)
			if invalid {
				content = []byte("not an image")
			}
			_, file, _ := imageMultipart(t, content, 0)
			cause := errors.New("fixture close failure")
			ctx := &imageCloseContext{Context: t.Context(), owned: file.state.owner, cause: cause}
			info, err := InspectImage(ctx, file, ImageLimits{})
			if !ctx.armed || info.Valid() || !errors.Is(err, cause) || !errors.Is(err, &Error{Code: "close_failed"}) {
				t.Fatal("close failure lost", err)
			}
			if invalid && !errors.Is(err, &Error{Code: "unsupported_image"}) {
				t.Fatal("decode failure lost after close", err)
			}
			if _, contentOnly := err.(*Error); contentOnly {
				t.Fatal("joined close failure can masquerade as a content-only error")
			}
			if len(file.state.owner.readers) != 0 {
				t.Fatal("failed close retained inspection reader")
			}
		})
	}
}
