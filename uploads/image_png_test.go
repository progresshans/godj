package uploads

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"hash/crc32"
	"io"
	"os"
	"sync"
	"testing"
)

type apngReference struct {
	Django, Python, Pillow string
	Sources, Payloads      map[string]string
	Cases                  []struct {
		Name, Filename string
		Valid          bool
		Errors         []string
		LoadError      *string `json:"load_error"`
		Loaded         []struct{ Size []int }
		Cleaned        *struct {
			Format                        string
			ContentType                   string `json:"content_type"`
			Width, Height, Frames, Cursor int
			DefaultImage                  bool `json:"default_image"`
			BytesPreserved                bool `json:"bytes_preserved"`
		}
	}
}

func apngFixture(t testing.TB) apngReference {
	t.Helper()
	raw, err := os.ReadFile("testdata/apng-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var result apngReference
	if err := json.Unmarshal(raw, &result); err != nil {
		t.Fatal(err)
	}
	if result.Django != "6.1" || result.Python != "3.14.3" || result.Pillow != "12.3.0" || len(result.Cases) != 49 || result.Sources["PIL.PngImagePlugin"] != "5911ebb3c8e58edf4fccace85ade20c3a062cc41dc55340bfb7b40f0bf1861c5" {
		t.Fatal("wrong pinned APNG observation")
	}
	return result
}

func apngPayload(t testing.TB, reference apngReference, name string) []byte {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString(reference.Payloads[name])
	if err != nil || len(content) == 0 {
		t.Fatal("missing APNG observation", name, err)
	}
	return content
}

func TestInspectAPNGAgainstPinnedDjango(t *testing.T) {
	reference := apngFixture(t)
	rejected := map[string]string{
		"zero_declared_frames": "invalid_image", "too_many_declared_frames": "invalid_image", "too_few_declared_frames": "invalid_image",
		"first_sequence": "invalid_image", "later_control_sequence": "invalid_image", "later_data_sequence": "invalid_image",
		"outside_canvas": "invalid_image", "zero_frame_width": "invalid_image", "first_frame_size": "invalid_image",
		"invalid_disposal": "invalid_image", "invalid_blend": "invalid_image", "long_frame_control": "invalid_image", "long_animation_control": "invalid_image",
		"late_pixel_error": "invalid_image", "default_pixel_error": "invalid_image", "empty_frame_data": "invalid_image",
		"missing_animation_control": "invalid_image", "duplicate_animation_control": "invalid_image", "late_animation_control": "invalid_image",
		"missing_frame_data": "invalid_image", "missing_frame_control": "invalid_image", "late_idat": "invalid_image", "unknown_critical": "unsupported_image",
		"late_crc_error": "invalid_image", "end_crc_error": "invalid_image", "missing_end": "invalid_image", "truncated_frame_chunk": "invalid_image",
	}
	// These valid PNG 3 streams agree with ImageField metadata. The pinned
	// Pillow frame player separately fails on an interleaved ancillary chunk
	// and an Adam7 later frame; its failure is kept in the observation.
	playerDifferences := map[string]string{"interleaved_chunks": "OSError", "adam7": "TypeError"}
	common, strict := 0, 0
	seen := map[string]bool{}
	for _, observed := range reference.Cases {
		if seen[observed.Name] {
			t.Fatal("duplicate APNG observation", observed.Name)
		}
		seen[observed.Name] = true
		code := rejected[observed.Name]
		delete(rejected, observed.Name)
		if code != "" && observed.Valid {
			strict++
		} else {
			common++
		}
		t.Run(observed.Name, func(t *testing.T) {
			content := apngPayload(t, reference, observed.Name)
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
						t.Fatal("APNG corruption became verified content", code, result.err)
					}
				} else {
					want := observed.Cleaned
					if !observed.Valid || want == nil || result.err != nil || !result.info.Valid() || result.info.FormatName() != want.Format || result.info.ContentType() != want.ContentType || result.info.Width() != want.Width || result.info.Height() != want.Height || result.info.Frames() != want.Frames {
						t.Fatal("APNG native content metadata differs", result.err, result.info.Frames())
					}
				}
			}
			if !observed.Valid && (len(observed.Errors) != 1 || observed.Errors[0] != "invalid_image") {
				t.Fatal("native rejection changed")
			}
			if code == "" {
				if !observed.Cleaned.BytesPreserved || observed.Cleaned.Cursor != 0 {
					t.Fatal("native upload ownership changed")
				}
				if want := playerDifferences[observed.Name]; want != "" {
					if observed.LoadError == nil || *observed.LoadError != want || len(observed.Loaded) != 1 {
						t.Fatal("native APNG player limitation disappeared")
					}
				} else if observed.LoadError != nil || len(observed.Loaded) != observed.Cleaned.Frames {
					t.Fatal("native APNG pixels were not independently loaded")
				}
			}
			if !bytes.Equal(content, before) || file.ContentType() != "application/x-untrusted" {
				t.Fatal("APNG inspection rewrote original bytes or MIME")
			}
		})
	}
	if len(rejected) != 0 || common != 26 || strict != 23 {
		t.Fatal("APNG observation coverage changed", common, strict, rejected)
	}
}

func TestInspectAPNGBudgetsDefaultImageAndAllFrameRectanglesBeforeDecoding(t *testing.T) {
	reference := apngFixture(t)
	for _, test := range []struct {
		name, payload string
		limits        ImageLimits
		code          string
		frames        int
	}{
		{"width", "rgba", ImageLimits{MaxWidth: 2}, "image_pixels", 0},
		{"height", "rgba", ImageLimits{MaxHeight: 1}, "image_pixels", 0},
		{"canvas", "poster", ImageLimits{MaxPixels: 5}, "image_pixels", 0},
		{"frames_before_bad_pixels", "late_pixel_error", ImageLimits{MaxFrames: 1}, "image_frames", 0},
		{"default_counts_as_frame", "poster", ImageLimits{MaxFrames: 2}, "image_frames", 0},
		{"single_with_default", "poster_single", ImageLimits{MaxFrames: 1}, "image_frames", 0},
		{"aggregate_before_default_decode", "default_pixel_error", ImageLimits{MaxTotalPixels: 11}, "image_pixels", 0},
		{"aggregate", "rgba", ImageLimits{MaxTotalPixels: 11}, "image_pixels", 0},
		{"default_pixels", "poster", ImageLimits{MaxTotalPixels: 9}, "image_pixels", 0},
		{"rectangle_pixels", "subrect", ImageLimits{MaxTotalPixels: 7}, "image_pixels", 0},
		{"exact_rectangles", "subrect", ImageLimits{MaxPixels: 6, MaxTotalPixels: 8}, "", 2},
		{"exact_default", "poster", ImageLimits{MaxFrames: 3, MaxPixels: 6, MaxTotalPixels: 10}, "", 3},
		{"single", "single", ImageLimits{MaxFrames: 1, MaxTotalPixels: 6}, "", 1},
		{"adam7_pixels", "adam7", ImageLimits{MaxTotalPixels: 12}, "", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			info, err := InspectImageReader(t.Context(), bytes.NewReader(apngPayload(t, reference, test.payload)), test.limits)
			if test.code != "" {
				if info.Valid() || !errors.Is(err, &Error{Code: test.code}) {
					t.Fatal("APNG default/frame budget was not checked before pixels", err, test.code)
				}
			} else if err != nil || info.Frames() != test.frames || info.Width() != 3 || info.Height() != 2 {
				t.Fatal("exact APNG budget was rejected", err, info.Frames())
			}
		})
	}
	content := apngPayload(t, reference, "poster")
	for _, size := range []int{len(content) - 1, len(content)} {
		info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{MaxBytes: int64(size)})
		if size < len(content) {
			if info.Valid() || !errors.Is(err, &Error{Code: "image_bytes"}) {
				t.Fatal("APNG escaped encoded byte budget", err)
			}
		} else if err != nil || info.Frames() != 3 {
			t.Fatal("exact APNG byte budget was rejected", err)
		}
	}
}

func apngTestChunk(kind string, data []byte) []byte {
	chunk := make([]byte, len(data)+12)
	binary.BigEndian.PutUint32(chunk[:4], uint32(len(data)))
	copy(chunk[4:8], kind)
	copy(chunk[8:], data)
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk
}

func apngAlterChunk(t testing.TB, content []byte, kind string, index int, update func([]byte) []byte) []byte {
	t.Helper()
	for offset := 8; offset < len(content); {
		size := int(binary.BigEndian.Uint32(content[offset:])) + 12
		if string(content[offset+4:offset+8]) == kind {
			if index == 0 {
				out := bytes.Clone(content[:offset])
				out = append(out, apngTestChunk(kind, update(bytes.Clone(content[offset+8:offset+size-4])))...)
				return append(out, content[offset+size:]...)
			}
			index--
		}
		offset += size
	}
	t.Fatal("APNG test chunk missing", kind, index)
	return nil
}

func TestInspectAPNGRejectsMalformedSpansAndInheritedPixelMetadata(t *testing.T) {
	reference := apngFixture(t)
	for _, mode := range []string{"short_animation", "short_control", "short_data", "width_overflow", "offset_overflow", "zero_height", "chunk_overflow", "header_duplicate", "end_data", "late_palette", "late_transparency", "palette_after_transparency", "bad_chunk_type", "bad_ancillary_crc", "huge_declaration"} {
		t.Run(mode, func(t *testing.T) {
			content := apngPayload(t, reference, "rgba")
			want := "invalid_image"
			switch mode {
			case "short_animation", "short_control", "short_data":
				kind := map[string]string{"short_animation": "acTL", "short_control": "fcTL", "short_data": "fdAT"}[mode]
				content = apngAlterChunk(t, content, kind, 0, func([]byte) []byte { return []byte{1, 2, 3} })
			case "width_overflow", "offset_overflow", "zero_height":
				content = apngAlterChunk(t, content, "fcTL", 1, func(data []byte) []byte {
					at, value := 4, ^uint32(0)
					if mode == "offset_overflow" {
						at = 12
					} else if mode == "zero_height" {
						at, value = 8, 0
					}
					binary.BigEndian.PutUint32(data[at:at+4], value)
					return data
				})
			case "chunk_overflow":
				binary.BigEndian.PutUint32(content[33:37], ^uint32(0))
			case "header_duplicate":
				content = append(append(bytes.Clone(content[:33]), content[8:33]...), content[33:]...)
			case "end_data":
				content = apngAlterChunk(t, content, "IEND", 0, func([]byte) []byte { return []byte{0} })
			case "late_palette", "late_transparency":
				kind := map[string]string{"late_palette": "PLTE", "late_transparency": "tRNS"}[mode]
				at := len(content) - 12
				content = append(append(bytes.Clone(content[:at]), apngTestChunk(kind, []byte{1, 2, 3})...), content[at:]...)
			case "palette_after_transparency":
				content = apngPayload(t, reference, "rgb")
				prefix := append(bytes.Clone(content[:33]), apngTestChunk("tRNS", make([]byte, 6))...)
				prefix = append(prefix, apngTestChunk("PLTE", []byte{1, 2, 3})...)
				content = append(prefix, content[33:]...)
			case "bad_chunk_type", "bad_ancillary_crc":
				kind := "teXt"
				if mode == "bad_chunk_type" {
					kind = "a1BC"
				}
				chunk := apngTestChunk(kind, []byte("synthetic"))
				if mode == "bad_ancillary_crc" {
					chunk[len(chunk)-1] ^= 1
				}
				content = append(append(bytes.Clone(content[:33]), chunk...), content[33:]...)
			case "huge_declaration":
				content = apngAlterChunk(t, content, "acTL", 0, func(data []byte) []byte {
					binary.BigEndian.PutUint32(data[:4], ^uint32(0))
					return data
				})
				want = "image_frames"
			}
			info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{})
			if info.Valid() || !errors.Is(err, &Error{Code: want}) {
				t.Fatal("malformed APNG structure escaped inspection", err, want)
			}
		})
	}
}

func TestAPNGFrameViewsPreserveSourceAndObserveCancellation(t *testing.T) {
	content := apngPayload(t, apngFixture(t), "interleaved_chunks")
	before := bytes.Clone(content)
	limits, _ := (ImageLimits{}).Normalize()
	layout, err := pngImageFrames(t.Context(), content, 3, 2, limits)
	if err != nil || len(layout.frames) != 2 {
		t.Fatal("APNG frame metadata unavailable", err)
	}
	frame := layout.frames[1]
	var want []byte
	for offset := frame.start; offset < frame.end; {
		size := int(binary.BigEndian.Uint32(content[offset:])) + 12
		if string(content[offset+4:offset+8]) == "fdAT" {
			want = append(want, apngTestChunk("IDAT", content[offset+12:offset+size-4])...)
		}
		offset += size
	}
	for _, size := range []int{1, 4, 7, 8, 31} {
		reader := &pngFrameReader{ctx: t.Context(), remaining: content[frame.start:frame.end]}
		var got bytes.Buffer
		if n, err := reader.Read(nil); n != 0 || err != nil {
			t.Fatal("empty frame read changed stream", n, err)
		}
		buffer := make([]byte, size)
		for {
			n, err := reader.Read(buffer)
			got.Write(buffer[:n])
			if err == io.EOF {
				break
			}
			if err != nil || n == 0 {
				t.Fatal("frame view stopped making progress", n, err)
			}
		}
		if !bytes.Equal(got.Bytes(), want) {
			t.Fatal("APNG frame view altered data, boundaries or checksum", size)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	reader := &pngFrameReader{ctx: ctx, remaining: content[frame.start:frame.end]}
	if _, err := reader.Read(make([]byte, 1)); err != nil {
		t.Fatal(err)
	}
	cancel()
	if n, err := reader.Read(make([]byte, 1)); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("APNG frame reader ignored cancellation", n, err)
	}
	checksumContext := &imageCancelContext{Context: t.Context(), after: 2}
	if _, err := pngImageCRC(checksumContext, 0, make([]byte, 96<<10)); !errors.Is(err, context.Canceled) {
		t.Fatal("PNG checksum ignored cancellation between bounded chunks", err)
	}
	if !bytes.Equal(content, before) {
		t.Fatal("APNG frame view mutated source")
	}
}

func TestInspectAPNGConcurrentFrameViewsStayIndependent(t *testing.T) {
	content := apngPayload(t, apngFixture(t), "poster")
	file, err := NewFile("private.apng", "application/x-untrusted", content)
	if err != nil {
		t.Fatal(err)
	}
	before := bytes.Clone(content)
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			info, err := InspectImage(t.Context(), file, ImageLimits{})
			if err != nil || info.Frames() != 3 || info.Width() != 3 || info.Height() != 2 {
				t.Error("APNG frame state crossed concurrent inspections", err)
			}
		})
	}
	workers.Wait()
	if !bytes.Equal(content, before) {
		t.Fatal("APNG inspection changed shared content")
	}
}
