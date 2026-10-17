package uploads

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"os"
	"sync"
	"testing"

	"golang.org/x/image/webp"
)

type webpReference struct {
	Django, Python, Pillow, Libwebp string
	Sources, Payloads               map[string]string
	Cases                           []struct {
		Name, Filename string
		Valid          bool
		Errors         []string
		LoadError      *string `json:"load_error"`
		Loaded         []struct{ Size []int }
		Cleaned        *struct {
			Format                        string
			ContentType                   string `json:"content_type"`
			Width, Height, Frames, Cursor int
			BytesPreserved                bool `json:"bytes_preserved"`
		}
	}
}

func webpFixture(t testing.TB) webpReference {
	t.Helper()
	raw, err := os.ReadFile("testdata/webp-animation-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference webpReference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Pillow != "12.3.0" || reference.Libwebp != "1.6.0" || len(reference.Cases) != 61 || len(reference.Payloads) != 61 || reference.Sources["PIL.WebPImagePlugin"] != "634360a326abfcd29ec5e05f6b84c3be6e5a783e7c4dd6ad46bd0eba182dae23" || reference.Sources["django.forms.fields"] != "2b756230a12f2329e2796314dd99fc57a8d0b016658863be2e899a2988d33f66" {
		t.Fatal("wrong pinned WebP observation")
	}
	return reference
}

func webpPayload(t testing.TB, reference webpReference, name string) []byte {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString(reference.Payloads[name])
	if err != nil || len(content) == 0 {
		t.Fatal("missing WebP observation", name, err)
	}
	return content
}

func TestInspectWebPAgainstPinnedDjango(t *testing.T) {
	reference := webpFixture(t)
	rejected := map[string]string{
		"unknown_frame_chunks": "invalid_image", "static_alpha_flag_only": "invalid_image", "static_alpha_missing_flag": "invalid_image",
		"missing_anim": "invalid_image", "duplicate_anim": "invalid_image", "missing_frames": "invalid_image", "frame_before_anim": "invalid_image",
		"duplicate_header": "invalid_image", "mixed_top_level": "invalid_image", "short_header": "invalid_image", "short_anim": "invalid_image", "long_anim": "invalid_image",
		"short_frame": "invalid_image", "unflagged_frames": "invalid_image", "outside_canvas": "invalid_image", "offset_overflow": "invalid_image",
		"dimension_mismatch": "invalid_image", "duplicate_bitstream": "invalid_image", "missing_bitstream": "invalid_image",
		"late_lossy_pixels": "invalid_image", "late_lossless_pixels": "invalid_image", "alpha_after_pixels": "invalid_image", "duplicate_alpha": "invalid_image", "alpha_with_lossless": "invalid_image",
		"truncated_raw_alpha": "invalid_image", "extra_raw_alpha": "invalid_image", "truncated_compressed_alpha": "invalid_image", "unsupported_alpha_compression": "unsupported_image",
		"trailing_bytes": "invalid_image", "truncated_chunk": "invalid_image", "bad_riff_size": "invalid_image", "nonzero_padding": "invalid_image",
	}
	// The published container requires readers to ignore reserved fields and
	// future VP8X fields. Pinned libwebp rejects these three headers; metadata
	// is compared with the unchanged lossy control, not claimed as parity.
	specDifferences := map[string]bool{"reserved_header": true, "reserved_header_flags": true, "extended_header": true}
	control := reference.Cases[0]
	if control.Name != "lossy" || !control.Valid || control.Cleaned == nil {
		t.Fatal("WebP independent control missing")
	}
	common, strict, spec := 0, 0, 0
	seen := map[string]bool{}
	for _, observed := range reference.Cases {
		if seen[observed.Name] {
			t.Fatal("duplicate WebP observation", observed.Name)
		}
		seen[observed.Name] = true
		code, differs := rejected[observed.Name], specDifferences[observed.Name]
		delete(rejected, observed.Name)
		delete(specDifferences, observed.Name)
		if differs {
			spec++
		} else if code != "" && observed.Valid {
			strict++
		} else {
			common++
		}
		t.Run(observed.Name, func(t *testing.T) {
			content := webpPayload(t, reference, observed.Name)
			before := bytes.Clone(content)
			file, err := NewFile(observed.Filename, "application/x-untrusted", content)
			if err != nil {
				t.Fatal(err)
			}
			owned, ownedErr := InspectImage(t.Context(), file, ImageLimits{})
			borrowed, borrowedErr := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{})
			for _, result := range []struct {
				info ImageInfo
				err  error
			}{{owned, ownedErr}, {borrowed, borrowedErr}} {
				if code != "" {
					if result.info.Valid() || !errors.Is(result.err, &Error{Code: code}) {
						t.Fatal("WebP corruption became verified content", code, result.err)
					}
				} else {
					want := observed.Cleaned
					if differs {
						want = control.Cleaned
					}
					if want == nil || result.err != nil || !result.info.Valid() || result.info.FormatName() != want.Format || result.info.ContentType() != want.ContentType || result.info.Width() != want.Width || result.info.Height() != want.Height || result.info.Frames() != want.Frames {
						t.Fatal("WebP native content metadata differs", result.err, result.info.Frames())
					}
				}
			}
			if !observed.Valid && (len(observed.Errors) != 1 || observed.Errors[0] != "invalid_image") {
				t.Fatal("native WebP rejection changed")
			}
			if observed.Valid && (observed.Cleaned == nil || !observed.Cleaned.BytesPreserved || observed.Cleaned.Cursor != 0) {
				t.Fatal("native WebP upload ownership changed")
			}
			if differs {
				if observed.Valid || observed.LoadError == nil || *observed.LoadError != "OSError" || len(observed.Loaded) != 0 {
					t.Fatal("native WebP reserved header difference disappeared")
				}
			} else if code == "" {
				if observed.Name == "reserved_alpha" {
					if observed.LoadError == nil || *observed.LoadError != "OSError" || len(observed.Loaded) != 1 {
						t.Fatal("native WebP reserved alpha player difference disappeared")
					}
				} else if observed.LoadError != nil || len(observed.Loaded) != observed.Cleaned.Frames {
					t.Fatal("native WebP pixels were not independently loaded")
				}
			}
			if !bytes.Equal(content, before) || file.ContentType() != "application/x-untrusted" {
				t.Fatal("WebP inspection rewrote original bytes or MIME")
			}
		})
	}
	if len(rejected) != 0 || len(specDifferences) != 0 || common != 44 || strict != 14 || spec != 3 {
		t.Fatal("WebP observation coverage changed", common, strict, spec, rejected, specDifferences)
	}
}

func TestInspectWebPBoundsCanvasAndAllFramesBeforePixels(t *testing.T) {
	reference := webpFixture(t)
	for _, test := range []struct {
		name, payload, code   string
		limits                ImageLimits
		width, height, frames int
	}{
		{"width", "lossy", "image_pixels", ImageLimits{MaxWidth: 2}, 0, 0, 0},
		{"height", "lossy", "image_pixels", ImageLimits{MaxHeight: 1}, 0, 0, 0},
		{"canvas", "offsets", "image_pixels", ImageLimits{MaxPixels: 19}, 0, 0, 0},
		{"canvas_total", "offsets", "image_pixels", ImageLimits{MaxTotalPixels: 19}, 0, 0, 0},
		{"frames_before_bad_pixels", "late_lossless_pixels", "image_frames", ImageLimits{MaxFrames: 1}, 0, 0, 0},
		{"aggregate_before_bad_pixels", "late_lossless_pixels", "image_pixels", ImageLimits{MaxTotalPixels: 11}, 0, 0, 0},
		{"aggregate", "lossy", "image_pixels", ImageLimits{MaxTotalPixels: 11}, 0, 0, 0},
		{"rectangle_pixels", "subrect", "image_pixels", ImageLimits{MaxTotalPixels: 7}, 0, 0, 0},
		{"exact_rectangles", "subrect", "", ImageLimits{MaxPixels: 6, MaxTotalPixels: 8}, 3, 2, 2},
		{"exact_canvas", "offsets", "", ImageLimits{MaxWidth: 5, MaxHeight: 4, MaxPixels: 20, MaxTotalPixels: 20}, 5, 4, 2},
		{"single", "single", "", ImageLimits{MaxFrames: 1, MaxTotalPixels: 6}, 3, 2, 1},
		{"static", "static_alpha", "", ImageLimits{MaxFrames: 1, MaxTotalPixels: 6}, 3, 2, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			info, err := InspectImageReader(t.Context(), bytes.NewReader(webpPayload(t, reference, test.payload)), test.limits)
			if test.code != "" {
				if info.Valid() || !errors.Is(err, &Error{Code: test.code}) {
					t.Fatal("WebP frame budget was not checked before pixels", err, test.code)
				}
			} else if err != nil || info.Width() != test.width || info.Height() != test.height || info.Frames() != test.frames {
				t.Fatal("exact WebP frame budget was rejected", err, info.Frames())
			}
		})
	}
	content := webpPayload(t, reference, "lossy")
	for _, size := range []int{len(content) - 1, len(content)} {
		info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{MaxBytes: int64(size)})
		if size < len(content) {
			if info.Valid() || !errors.Is(err, &Error{Code: "image_bytes"}) {
				t.Fatal("WebP escaped encoded byte budget", err)
			}
		} else if err != nil || info.Frames() != 2 {
			t.Fatal("exact WebP byte budget was rejected", err)
		}
	}
}

func webpTestChunk(kind string, data []byte) []byte {
	chunk := make([]byte, 8+len(data)+(len(data)&1))
	copy(chunk, kind)
	binary.LittleEndian.PutUint32(chunk[4:8], uint32(len(data)))
	copy(chunk[8:], data)
	return chunk
}

func webpAlterChunk(t testing.TB, content []byte, kind string, index int, update func([]byte) []byte) []byte {
	t.Helper()
	for offset := 12; offset < len(content); {
		size := int(binary.LittleEndian.Uint32(content[offset+4 : offset+8]))
		end := offset + 8 + size + (size & 1)
		if string(content[offset:offset+4]) == kind {
			if index == 0 {
				out := append(bytes.Clone(content[:offset]), webpTestChunk(kind, update(bytes.Clone(content[offset+8:offset+8+size])))...)
				out = append(out, content[end:]...)
				binary.LittleEndian.PutUint32(out[4:8], uint32(len(out)-8))
				return out
			}
			index--
		}
		offset = end
	}
	t.Fatal("WebP test chunk missing", kind, index)
	return nil
}

func TestInspectWebPRejectsActualPixelBombAndMalformedSubchunks(t *testing.T) {
	reference := webpFixture(t)
	for _, mode := range []string{"actual_pixel_bomb", "subchunk_overflow", "subchunk_header", "subchunk_padding", "nested_control", "empty_alpha", "empty_compressed_alpha", "first_pixels_then_budget"} {
		t.Run(mode, func(t *testing.T) {
			content := webpPayload(t, reference, "raw_alpha_filter0")
			limits, want, index := ImageLimits{}, "invalid_image", 1
			if mode == "first_pixels_then_budget" {
				content, limits, want, index = webpPayload(t, reference, "lossless"), ImageLimits{MaxTotalPixels: 11}, "image_pixels", 0
			}
			content = webpAlterChunk(t, content, "ANMF", index, func(data []byte) []byte {
				switch mode {
				case "actual_pixel_bomb":
					// A 3x2 ANMF hides a 16384x16384 VP8L header without pixels.
					want = "image_pixels"
					return append(data[:16], webpTestChunk("VP8L", []byte{0x2f, 0xff, 0xff, 0xff, 0x0f})...)
				case "subchunk_overflow":
					binary.LittleEndian.PutUint32(data[20:24], ^uint32(0))
				case "subchunk_header":
					data = append(data, 'X')
				case "subchunk_padding":
					data[31] = 1 // Odd seven-byte ALPH requires a zero pad.
				case "nested_control":
					data = append(data, webpTestChunk("ANIM", make([]byte, 6))...)
				case "empty_alpha", "empty_compressed_alpha":
					var alpha []byte
					if mode == "empty_compressed_alpha" {
						alpha = []byte{1}
					}
					data = append(append(bytes.Clone(data[:16]), webpTestChunk("ALPH", alpha)...), data[32:]...)
				case "first_pixels_then_budget":
					data = append(bytes.Clone(data[:16]), webpTestChunk("VP8L", data[24:29])...)
				}
				return data
			})
			info, err := InspectImageReader(t.Context(), bytes.NewReader(content), limits)
			if info.Valid() || !errors.Is(err, &Error{Code: want}) {
				t.Fatal("WebP actual pixels or subchunk framing escaped preflight", err, want)
			}
		})
	}
}

func TestWebPFrameViewsPreserveAlphaSourceAndCancellation(t *testing.T) {
	content := webpPayload(t, webpFixture(t), "raw_alpha_filter0")
	before := bytes.Clone(content)
	limits, _ := (ImageLimits{}).Normalize()
	layout, err := webpImageFrames(t.Context(), content, limits)
	if err != nil || len(layout.frames) != 2 {
		t.Fatal("WebP frame metadata unavailable", err)
	}
	frame := layout.frames[1]
	view, err := io.ReadAll(frame.reader(t.Context()))
	if err != nil || len(view) != 30+len(frame.alpha)+len(frame.pixels) || string(view[8:16]) != "WEBPVP8X" || view[20] != 16 || binary.LittleEndian.Uint32(view[4:8]) != uint32(len(view)-8) || !bytes.Equal(view[30:], append(bytes.Clone(frame.alpha), frame.pixels...)) {
		t.Fatal("WebP frame view changed original chunks or alpha", err)
	}
	decoded, err := webp.Decode(bytes.NewReader(view))
	if err != nil {
		t.Fatal(err)
	}
	for index, want := range []uint32{0, 64, 128, 255, 96, 192} {
		_, _, _, alpha := decoded.At(index%3, index/3).RGBA()
		if alpha != want*257 {
			t.Fatal("WebP frame view lost alpha pixels", index, alpha, want)
		}
	}
	for _, offset := range []int{1, 30, 30 + len(frame.alpha)} {
		ctx, cancel := context.WithCancel(t.Context())
		reader := frame.reader(ctx)
		if _, err := io.ReadFull(reader, make([]byte, offset)); err != nil {
			t.Fatal(err)
		}
		cancel()
		if n, err := reader.Read(make([]byte, 1)); n != 0 || !errors.Is(err, context.Canceled) {
			t.Fatal("WebP frame reader ignored cancellation", n, err)
		}
	}
	if !bytes.Equal(content, before) {
		t.Fatal("WebP frame view mutated source")
	}
}

func TestInspectWebPConcurrentFrameViewsStayIndependent(t *testing.T) {
	reference := webpFixture(t)
	content := webpPayload(t, reference, "encoded_alpha")
	// Keep a global alpha flag while the first frame is opaque. It must not
	// force a nonexistent ALPH chunk into that frame's private decoder view.
	opaque := webpPayload(t, reference, "lossy")
	var first []byte
	webpAlterChunk(t, opaque, "ANMF", 0, func(data []byte) []byte { first = data; return data })
	content = webpAlterChunk(t, content, "ANMF", 0, func([]byte) []byte { return first })
	file, err := NewFile("private.webp", "application/x-untrusted", content)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(content)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			info, err := InspectImage(t.Context(), file, ImageLimits{})
			if err != nil || info.Width() != 3 || info.Height() != 2 || info.Frames() != 2 {
				t.Error("WebP frame state crossed concurrent inspections", err)
			}
		})
	}
	workers.Wait()
	if !bytes.Equal(content, before) {
		t.Fatal("WebP inspection changed shared content")
	}
}
