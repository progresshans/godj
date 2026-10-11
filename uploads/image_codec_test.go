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
)

type imageCodecReference struct {
	Django, Python, Pillow string
	Sources, Payloads      map[string]string
	Cases                  []struct {
		Name, Filename string
		Valid          bool
		Errors         []string
		Cleaned        struct {
			Format                        string
			ContentType                   string `json:"content_type"`
			Width, Height, Frames, Cursor int
			FrameError                    *string `json:"frame_error"`
			BytesPreserved                bool    `json:"bytes_preserved"`
		}
	}
}

func codecReference(t testing.TB) imageCodecReference {
	t.Helper()
	raw, err := os.ReadFile("testdata/image-codecs-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference imageCodecReference
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Pillow != "12.3.0" || len(reference.Cases) != 36 || reference.Sources["PIL.BmpImagePlugin"] != "e0067eb268d1257de5a1d746652938a39107a06eb5e3fe415d31ee07b7a83b0f" || reference.Sources["PIL.TiffImagePlugin"] != "440b2a3a80b280d18cda3fc70d9fd206e2fa44e9897c9550f70b51395047b273" {
		t.Fatal("wrong pinned bitmap/TIFF observation")
	}
	return reference
}

func codecPayload(t testing.TB, reference imageCodecReference, name string) []byte {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString(reference.Payloads[name])
	if err != nil || len(content) == 0 {
		t.Fatal("missing codec payload", name, err)
	}
	return content
}

func TestInspectBitmapAndTIFFContentsAgainstPinnedDjango(t *testing.T) {
	reference := codecReference(t)
	differences := map[string]string{"bmp16": "unsupported_image", "tiff_cmyk": "unsupported_image", "tiff_jpeg": "unsupported_image", "bmp_truncated": "invalid_image", "dib_truncated": "invalid_image", "tiff_truncated": "invalid_image", "tiff_late_truncated": "invalid_image"}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			if !observed.Valid || len(observed.Errors) != 0 || !observed.Cleaned.BytesPreserved || observed.Cleaned.Cursor != 0 {
				t.Fatal("native case changed its independent outcome")
			}
			content := codecPayload(t, reference, observed.Name)
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
				if code := differences[observed.Name]; code != "" {
					if result.info.Valid() || !errors.Is(result.err, &Error{Code: code}) {
						t.Fatal("codec policy difference was not explicit", result.err, code)
					}
					continue
				}
				want := observed.Cleaned
				if result.err != nil || !result.info.Valid() || result.info.FormatName() != want.Format || result.info.ContentType() != want.ContentType || result.info.Width() != want.Width || result.info.Height() != want.Height || result.info.Frames() != want.Frames || want.FrameError != nil {
					t.Fatal("native codec metadata differs", result.err, result.info.FormatName(), result.info.Frames())
				}
			}
			if !bytes.Equal(content, before) || file.ContentType() != "application/x-untrusted" {
				t.Fatal("inspection rewrote native bytes or caller metadata")
			}
			if observed.Name == "tiff_late_truncated" && (observed.Cleaned.FrameError == nil || *observed.Cleaned.FrameError != "SyntaxError") {
				t.Fatal("native first-page success hid its later-directory failure")
			}
		})
	}
}

func tiffTestDirectory(t *testing.T, content []byte, page int) (binary.ByteOrder, uint64, uint64) {
	t.Helper()
	var order binary.ByteOrder = binary.LittleEndian
	if string(content[:2]) == "MM" {
		order = binary.BigEndian
	}
	offset := uint64(order.Uint32(content[4:8]))
	for index := 0; ; index++ {
		if offset < 8 || offset+2 > uint64(len(content)) {
			t.Fatal("test directory unavailable")
		}
		end := offset + 2 + uint64(order.Uint16(content[offset:offset+2]))*12 + 4
		if end > uint64(len(content)) {
			t.Fatal("test directory incomplete")
		}
		if index == page {
			return order, offset, end
		}
		offset = uint64(order.Uint32(content[end-4 : end]))
	}
}

func tiffTestEntry(t *testing.T, content []byte, page int, tag uint16) (binary.ByteOrder, []byte) {
	t.Helper()
	order, offset, end := tiffTestDirectory(t, content, page)
	for at := offset + 2; at < end-4; at += 12 {
		entry := content[at : at+12]
		if order.Uint16(entry[:2]) == tag {
			return order, entry
		}
	}
	t.Fatal("test TIFF tag absent", tag)
	return nil, nil
}

func TestInspectTIFFBudgetsAllPagesBeforePixelDecoding(t *testing.T) {
	reference := codecReference(t)
	for _, test := range []struct {
		name   string
		limits ImageLimits
		code   string
	}{
		{"width", ImageLimits{MaxWidth: 6}, "image_pixels"},
		{"height", ImageLimits{MaxHeight: 4}, "image_pixels"},
		{"page_pixels", ImageLimits{MaxPixels: 34}, "image_pixels"},
		{"total_pixels", ImageLimits{MaxTotalPixels: 40}, "image_pixels"},
		{"frames", ImageLimits{MaxFrames: 1}, "image_frames"},
		{"encoded", ImageLimits{MaxBytes: 100}, "image_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			content := codecPayload(t, reference, "tiff_pages")
			info, err := InspectImageReader(t.Context(), bytes.NewReader(content), test.limits)
			if info.Valid() || !errors.Is(err, &Error{Code: test.code}) {
				t.Fatal("later TIFF page escaped its budget", err, test.code)
			}
		})
	}
	content := codecPayload(t, reference, "tiff_pages")
	order, entry := tiffTestEntry(t, content, 1, 256)
	order.PutUint16(entry[2:4], 4)
	order.PutUint32(entry[8:12], 0xffffffff)
	if info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{}); info.Valid() || !errors.Is(err, &Error{Code: "image_pixels"}) {
		t.Fatal("huge later width reached a raster allocation", err)
	}
}

func TestInspectTIFFCountsPaddedTilesInPixelBudgets(t *testing.T) {
	content := codecPayload(t, codecReference(t), "tiff_tile")
	for _, limits := range []ImageLimits{{MaxPixels: 255}, {MaxTotalPixels: 255}} {
		if info, err := InspectImageReader(t.Context(), bytes.NewReader(content), limits); info.Valid() || !errors.Is(err, &Error{Code: "image_pixels"}) {
			t.Fatal("small visible dimensions hid padded tile work", err)
		}
	}
	info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{MaxWidth: 3, MaxHeight: 2, MaxPixels: 256, MaxTotalPixels: 256})
	if err != nil || info.Width() != 3 || info.Height() != 2 || info.Frames() != 1 {
		t.Fatal("exact padded tile budget rejected", err)
	}
	order, width := tiffTestEntry(t, content, 0, 322)
	order.PutUint32(width[8:12], 0xffffffff)
	_, height := tiffTestEntry(t, content, 0, 323)
	order.PutUint32(height[8:12], 0xffffffff)
	if info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{}); info.Valid() || !errors.Is(err, &Error{Code: "image_pixels"}) {
		t.Fatal("tile product overflow reached decoder", err)
	}
}

func TestInspectBitmapHeadersRespectBudgetsAndDIBPaletteBounds(t *testing.T) {
	reference := codecReference(t)
	for _, kind := range []string{"bmp", "dib"} {
		for _, mode := range []string{"width", "negative_height", "pixel_budget", "palette_overflow", "short_header", "unsupported_header"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				content := codecPayload(t, reference, kind+"_palette")
				base := 0
				if kind == "bmp" {
					base = 14
				}
				limits, code := ImageLimits{}, "image_pixels"
				switch mode {
				case "width":
					binary.LittleEndian.PutUint32(content[base+4:base+8], 0x7fffffff)
				case "negative_height":
					binary.LittleEndian.PutUint32(content[base+8:base+12], 0x80000000)
				case "pixel_budget":
					limits.MaxPixels = 5
				case "palette_overflow":
					binary.LittleEndian.PutUint32(content[base+32:base+36], 0xffffffff)
					code = "invalid_image"
					if kind == "bmp" {
						code = "unsupported_image"
					}
				case "short_header":
					content, code = content[:base+20], "invalid_image"
				case "unsupported_header":
					binary.LittleEndian.PutUint32(content[base:base+4], 12)
					code = "unsupported_image"
				}
				if info, err := InspectImageReader(t.Context(), bytes.NewReader(content), limits); info.Valid() || !errors.Is(err, &Error{Code: code}) {
					t.Fatal("bitmap preflight escaped its budget", err, code)
				}
			})
		}
	}
}

func TestInspectTIFFRejectsMalformedDirectoryGraphsAndDataSpans(t *testing.T) {
	reference := codecReference(t)
	for _, mode := range []string{"cycle", "overlap", "past_eof", "odd_offset", "header_offset", "no_page", "missing_next", "count_overflow", "duplicate_tag", "value_overflow", "value_past_eof", "planar", "subifd", "bigtiff", "late_invalid_compression", "late_pixel_truncation", "pixels_in_header", "block_past_eof", "block_count_mismatch"} {
		t.Run(mode, func(t *testing.T) {
			name := "tiff_pages"
			if mode == "pixels_in_header" {
				name = "tiff_raw"
			}
			content := codecPayload(t, reference, name)
			order, offset, end := tiffTestDirectory(t, content, 0)
			want := "invalid_image"
			switch mode {
			case "cycle":
				_, _, last := tiffTestDirectory(t, content, 1)
				order.PutUint32(content[last-4:last], uint32(offset))
			case "overlap":
				order.PutUint32(content[end-4:end], uint32(offset+2))
			case "past_eof":
				order.PutUint32(content[end-4:end], 0xfffffffe)
			case "odd_offset":
				order.PutUint32(content[end-4:end], 9)
			case "header_offset":
				order.PutUint32(content[4:8], 4)
			case "no_page":
				order.PutUint32(content[4:8], 0)
			case "missing_next":
				content = content[:end-1]
			case "count_overflow":
				order.PutUint16(content[offset:offset+2], 65535)
			case "duplicate_tag":
				copy(content[offset+14:offset+16], content[offset+2:offset+4])
			case "value_overflow", "value_past_eof":
				_, field := tiffTestEntry(t, content, 0, 258)
				if mode == "value_overflow" {
					order.PutUint32(field[4:8], 0xffffffff)
				} else {
					order.PutUint32(field[8:12], uint32(len(content)))
				}
			case "planar":
				_, field := tiffTestEntry(t, content, 0, 284)
				order.PutUint16(field[8:10], 2)
				want = "unsupported_image"
			case "subifd":
				// Replace the final sorted tag with an actual SubIFD reference.
				field := content[end-16 : end-4]
				order.PutUint16(field[:2], 330)
				order.PutUint16(field[2:4], 4)
				order.PutUint32(field[4:8], 1)
				order.PutUint32(field[8:12], uint32(offset))
				want = "unsupported_image"
			case "bigtiff":
				// Merely changing classic magic does not produce a valid BigTIFF
				// header/directory. Real BigTIFF files have separate coverage.
				order.PutUint16(content[2:4], 43)
			case "late_invalid_compression":
				_, field := tiffTestEntry(t, content, 1, 259)
				order.PutUint16(field[8:10], 65000)
				want = "unsupported_image"
			case "late_pixel_truncation":
				_, field := tiffTestEntry(t, content, 1, 273)
				order.PutUint32(field[8:12], uint32(len(content)-1))
			case "pixels_in_header":
				_, field := tiffTestEntry(t, content, 0, 273)
				order.PutUint32(field[8:12], 4)
			case "block_past_eof":
				_, field := tiffTestEntry(t, content, 0, 279)
				order.PutUint32(field[8:12], uint32(len(content)))
			case "block_count_mismatch":
				_, field := tiffTestEntry(t, content, 0, 273)
				order.PutUint32(field[4:8], 2)
			}
			before := bytes.Clone(content)
			info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{})
			if info.Valid() || !errors.Is(err, &Error{Code: want}) || !bytes.Equal(content, before) {
				t.Fatal("malformed TIFF became complete content", err, want)
			}
		})
	}
}

// The same valid PackBits block is referenced by two strips on every page.
// No-op packets make the encoded work much larger than the decoded pixels.
func sharedTIFFBlocks(t testing.TB, pages int) ([]byte, int) {
	t.Helper()
	const entries, directoryBytes = 10, 6 + 10*12
	compressed := append(bytes.Repeat([]byte{0x80}, 256), 2, 17, 51, 79)
	arrays := 8 + pages*directoryBytes
	data := arrays + pages*16
	content := make([]byte, data+len(compressed))
	copy(content, "II\x2a\x00")
	order := binary.LittleEndian
	order.PutUint32(content[4:8], 8)
	for page := range pages {
		start := 8 + page*directoryBytes
		order.PutUint16(content[start:start+2], entries)
		fields := []struct {
			tag, kind    uint16
			count, value uint32
		}{
			{256, 4, 1, 3}, {257, 4, 1, 2}, {258, 3, 1, 8},
			{259, 3, 1, 32773}, {262, 3, 1, 1},
			{273, 4, 2, uint32(arrays + page*16)}, {277, 3, 1, 1},
			{278, 4, 1, 1}, {279, 4, 2, uint32(arrays + page*16 + 8)}, {284, 3, 1, 1},
		}
		for index, field := range fields {
			entry := content[start+2+index*12 : start+2+(index+1)*12]
			order.PutUint16(entry[:2], field.tag)
			order.PutUint16(entry[2:4], field.kind)
			order.PutUint32(entry[4:8], field.count)
			order.PutUint32(entry[8:12], field.value)
		}
		if page+1 < pages {
			order.PutUint32(content[start+directoryBytes-4:start+directoryBytes], uint32(start+directoryBytes))
		}
		for strip := range 2 {
			at := arrays + page*16 + strip*4
			order.PutUint32(content[at:at+4], uint32(data))
			order.PutUint32(content[at+8:at+12], uint32(len(compressed)))
		}
	}
	copy(content[data:], compressed)
	return content, pages * 2 * len(compressed)
}

func TestInspectTIFFBoundsReferencedCompressedBytesAcrossStripsAndPages(t *testing.T) {
	for _, pages := range []int{1, 2} {
		content, work := sharedTIFFBlocks(t, pages)
		if len(content) >= work {
			t.Fatal("test does not amplify encoded work")
		}
		for _, bound := range []int64{int64(len(content)), int64(work - 1), int64(work)} {
			info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{MaxBytes: bound})
			if bound < int64(work) {
				if info.Valid() || !errors.Is(err, &Error{Code: "image_bytes"}) {
					t.Fatal("small TIFF amplified compressed work beyond its byte budget", pages, err)
				}
			} else if err != nil || info.Frames() != pages || info.Width() != 3 || info.Height() != 2 {
				t.Fatal("bounded shared TIFF blocks were rejected", pages, err)
			}
		}
	}
}

func TestTIFFPageViewsPreserveSourceAndObserveReadAtCancellation(t *testing.T) {
	content := codecPayload(t, codecReference(t), "tiff_pages")
	before := bytes.Clone(content)
	order, offset, _ := tiffTestDirectory(t, content, 1)
	var patch [4]byte
	order.PutUint32(patch[:], uint32(offset))
	ctx, cancel := context.WithCancel(t.Context())
	reader := tiffPageReader{ctx, content, patch}
	want := bytes.Clone(content)
	copy(want[4:8], patch[:])
	for _, at := range []int64{0, 3, 4, 6, 8, int64(len(content) - 2)} {
		var got [10]byte
		n, err := reader.ReadAt(got[:], at)
		expected := want[at:min(int64(len(want)), at+int64(len(got)))]
		if n != len(expected) || !bytes.Equal(got[:n], expected) || n < len(got) && err != io.EOF || n == len(got) && err != nil {
			t.Fatal("page view patched unrelated content", at, n, err)
		}
	}
	if n, err := reader.ReadAt(make([]byte, 1), -1); n != 0 || err == nil {
		t.Fatal("negative offset accepted")
	}
	cancel()
	if n, err := reader.ReadAt(make([]byte, 1), 0); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("random access ignored context", err)
	}
	if !bytes.Equal(content, before) {
		t.Fatal("page views mutated original file")
	}
	for _, after := range []int{2, 20} {
		ctx := &imageCancelContext{Context: t.Context(), after: after}
		if info, err := InspectImageReader(ctx, bytes.NewReader(content), ImageLimits{}); info.Valid() || !errors.Is(err, context.Canceled) {
			t.Fatal("TIFF stage/ReadAt ignored cancellation", after, err)
		}
	}
}

func TestInspectTIFFConcurrentPageViewsStayIndependent(t *testing.T) {
	content := codecPayload(t, codecReference(t), "tiff_pages")
	before := bytes.Clone(content)
	file, err := NewFile("private.tiff", "application/x-untrusted", content)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			info, err := InspectImage(t.Context(), file, ImageLimits{})
			if err != nil || info.Frames() != 2 || info.Width() != 3 || info.Height() != 2 {
				t.Error("page view state crossed concurrent callers", err)
			}
		})
	}
	workers.Wait()
	if !bytes.Equal(content, before) {
		t.Fatal("concurrent inspection rewrote source bytes")
	}
}
