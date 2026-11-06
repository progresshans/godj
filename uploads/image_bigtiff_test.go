package uploads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"image"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"golang.org/x/image/tiff"
)

type bigTIFFObservation struct {
	Django, Python, Pillow, LibTIFF string
	Sources, Tools, Payloads        map[string]string
	Cases                           []struct {
		Name, Filename string
		ClassicPayload string `json:"classic_payload"`
		Valid          bool
		Cleaned        struct {
			Format                string
			ContentType           string `json:"content_type"`
			Width, Height, Frames int
		}
		NativeFrames []struct {
			Width, Height int
			PixelHash     string `json:"pixels_rgba16_sha256"`
		} `json:"native_frames"`
		Django struct {
			Valid  bool
			Errors []string
		}
		LibTIFF struct {
			Info int `json:"tiffinfo_exit"`
			Copy int `json:"tiffcp_exit"`
		}
	}
}

func bigTIFFReference(t testing.TB) bigTIFFObservation {
	t.Helper()
	raw, err := os.ReadFile("testdata/bigtiff-libtiff471.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference bigTIFFObservation
	if err := json.Unmarshal(raw, &reference); err != nil {
		t.Fatal(err)
	}
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Pillow != "12.3.0" || reference.LibTIFF != "4.7.1" || len(reference.Sources) != 2 || len(reference.Tools) != 2 || len(reference.Cases) != 18 || len(reference.Payloads) != 18 {
		t.Fatal("incomplete independent BigTIFF reference")
	}
	return reference
}

func bigTIFFPayload(t testing.TB, reference bigTIFFObservation, name string) []byte {
	t.Helper()
	content, err := base64.StdEncoding.DecodeString(reference.Payloads[name])
	if err != nil || len(content) == 0 {
		t.Fatal("missing independent BigTIFF payload", name, err)
	}
	return content
}

func TestInspectBigTIFFMatchesIndependentContent(t *testing.T) {
	reference := bigTIFFReference(t)
	limits, err := (ImageLimits{}).Normalize()
	if err != nil {
		t.Fatal(err)
	}
	for _, observed := range reference.Cases {
		t.Run(observed.Name, func(t *testing.T) {
			content := bigTIFFPayload(t, reference, observed.Name)
			before := bytes.Clone(content)
			file, err := NewFile(observed.Filename, "application/x-untrusted", content)
			if err != nil {
				t.Fatal(err)
			}
			owned, ownedErr := InspectImage(t.Context(), file, ImageLimits{})
			borrowed, borrowedErr := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{})
			wantCode := ""
			if !observed.Valid {
				wantCode = "invalid_image"
				if observed.LibTIFF.Info == 0 && observed.LibTIFF.Copy == 0 || !observed.Django.Valid {
					t.Fatal("later corruption must escape native form verification but fail full decode")
				}
			} else if observed.Name == "le_cmyk" || observed.Name == "le_jpeg" {
				wantCode = "unsupported_image"
			}
			for _, result := range []struct {
				info ImageInfo
				err  error
			}{{owned, ownedErr}, {borrowed, borrowedErr}} {
				if wantCode != "" {
					if result.info.Valid() || !errors.Is(result.err, &Error{Code: wantCode}) {
						t.Fatal("BigTIFF policy lost", result.err, wantCode)
					}
				} else if result.err != nil || !result.info.Valid() || result.info.FormatName() != "tiff" || result.info.ContentType() != "image/tiff" || result.info.Width() != observed.Cleaned.Width || result.info.Height() != observed.Cleaned.Height || result.info.Frames() != observed.Cleaned.Frames {
					t.Fatal("BigTIFF metadata differs", result.err, result.info.Width(), result.info.Height(), result.info.Frames())
				}
			}
			if !bytes.Equal(content, before) || file.ContentType() != "application/x-untrusted" {
				t.Fatal("inspection rewrote source bytes or caller metadata")
			}
			if wantCode != "" {
				return
			}
			if observed.LibTIFF.Info != 0 || observed.LibTIFF.Copy != 0 || strings.HasPrefix(observed.Name, "be_") == observed.Django.Valid {
				t.Fatal("LibTIFF full decode and Pillow byte-order difference were conflated")
			}
			pages, order, err := tiffImagePages(t.Context(), content, limits)
			if err != nil || len(pages) != len(observed.NativeFrames) {
				t.Fatal("page inventory differs", err)
			}
			for index, page := range pages {
				decoded, err := tiff.Decode(page.reader(t.Context(), content, order))
				if err != nil {
					t.Fatal(err)
				}
				want := observed.NativeFrames[index]
				// The pinned decoder reports CCITT run colors for BlackIsZero,
				// opposite to LibTIFF's displayed colors, in classic TIFF too.
				// Inspection exports no pixels. Keep this known difference explicit
				// and prove the BigTIFF view did not introduce it or change pixels.
				fax := observed.Name == "le_group3" || observed.Name == "be_group4"
				if fax {
					classic, err := base64.StdEncoding.DecodeString(observed.ClassicPayload)
					if err != nil || len(classic) == 0 {
						t.Fatal("missing independent classic CCITT", err)
					}
					comparison, err := tiff.Decode(bytes.NewReader(classic))
					if err != nil || bigTIFFPixelHash(comparison, false) != bigTIFFPixelHash(decoded, false) || bigTIFFPixelHash(decoded, false) == want.PixelHash {
						t.Fatal("pinned CCITT difference changed", err)
					}
				}
				if page.width != want.Width || page.height != want.Height || bigTIFFPixelHash(decoded, fax) != want.PixelHash {
					t.Fatal("decoded pixels differ from independent LibTIFF", index, bigTIFFPixelHash(decoded, fax), want.PixelHash)
				}
			}
		})
	}
}

func bigTIFFPixelHash(decoded image.Image, invert bool) string {
	hash := sha256.New()
	for y := decoded.Bounds().Min.Y; y < decoded.Bounds().Max.Y; y++ {
		for x := decoded.Bounds().Min.X; x < decoded.Bounds().Max.X; x++ {
			r, g, b, a := decoded.At(x, y).RGBA()
			if invert {
				r, g, b = 65535-r, 65535-g, 65535-b
			}
			var pixel [8]byte
			for i, value := range []uint32{r, g, b, a} {
				binary.BigEndian.PutUint16(pixel[i*2:i*2+2], uint16(value))
			}
			hash.Write(pixel[:])
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func bigTIFFTestDirectory(t testing.TB, content []byte, page int) (binary.ByteOrder, uint64, uint64) {
	t.Helper()
	var order binary.ByteOrder = binary.LittleEndian
	if string(content[:2]) == "MM" {
		order = binary.BigEndian
	}
	offset := order.Uint64(content[8:16])
	for index := 0; ; index++ {
		if offset < 16 || offset > uint64(len(content))-8 {
			t.Fatal("fixture IFD missing")
		}
		end := offset + 8 + order.Uint64(content[offset:offset+8])*20 + 8
		if end > uint64(len(content)) {
			t.Fatal("fixture IFD truncated")
		}
		if index == page {
			return order, offset, end
		}
		offset = order.Uint64(content[end-8 : end])
	}
}

func bigTIFFTestEntry(t testing.TB, content []byte, page int, tag uint16) (binary.ByteOrder, []byte) {
	t.Helper()
	order, offset, end := bigTIFFTestDirectory(t, content, page)
	for position := offset + 8; position < end-8; position += 20 {
		entry := content[position : position+20]
		if order.Uint16(entry[:2]) == tag {
			return order, entry
		}
	}
	t.Fatal("fixture tag missing", tag)
	return nil, nil
}

func TestInspectBigTIFFRejectsOverflowAndDirectoryCorruption(t *testing.T) {
	reference := bigTIFFReference(t)
	for _, source := range []string{"le_pages", "be_pages"} {
		for _, mode := range []string{"offset_size", "reserved", "short_header", "no_page", "offset_overflow", "offset_high32", "cycle", "overlap", "odd", "header", "count_overflow", "count_high32", "missing_next", "duplicate_tag", "value_count_overflow", "value_offset_overflow", "unknown_type", "width_overflow", "height_overflow", "tile_overflow", "block_offset_overflow", "block_size_overflow", "pixel_header", "block_mismatch", "planar", "subifd", "late_compression"} {
			t.Run(source+"/"+mode, func(t *testing.T) {
				content := bigTIFFPayload(t, reference, source)
				order, offset, end := bigTIFFTestDirectory(t, content, 0)
				want := "invalid_image"
				long8 := func(page int, tag uint16, value uint64) {
					_, entry := bigTIFFTestEntry(t, content, page, tag)
					order.PutUint16(entry[2:4], 16)
					order.PutUint64(entry[4:12], 1)
					order.PutUint64(entry[12:20], value)
				}
				switch mode {
				case "offset_size":
					order.PutUint16(content[4:6], 16)
				case "reserved":
					order.PutUint16(content[6:8], 1)
				case "short_header":
					content = content[:15]
				case "no_page":
					order.PutUint64(content[8:16], 0)
				case "offset_overflow":
					order.PutUint64(content[8:16], ^uint64(0))
				case "offset_high32":
					order.PutUint64(content[8:16], offset+1<<32)
				case "cycle":
					order.PutUint64(content[end-8:end], offset)
				case "overlap":
					order.PutUint64(content[end-8:end], offset+8)
				case "odd":
					order.PutUint64(content[8:16], offset+1)
				case "header":
					order.PutUint64(content[8:16], 8)
				case "count_overflow":
					order.PutUint64(content[offset:offset+8], ^uint64(0))
				case "count_high32":
					order.PutUint64(content[offset:offset+8], order.Uint64(content[offset:offset+8])+1<<32)
				case "missing_next":
					_, _, last := bigTIFFTestDirectory(t, content, 1)
					content = content[:last-1]
				case "duplicate_tag":
					copy(content[offset+28:offset+30], content[offset+8:offset+10])
				case "value_count_overflow", "value_offset_overflow":
					_, entry := bigTIFFTestEntry(t, content, 0, 258)
					order.PutUint64(entry[4:12], 5)
					if mode == "value_count_overflow" {
						order.PutUint64(entry[4:12], ^uint64(0))
					} else {
						order.PutUint64(entry[12:20], ^uint64(0))
					}
				case "unknown_type":
					order.PutUint16(content[offset+10:offset+12], 14)
					want = "unsupported_image"
				case "width_overflow":
					long8(0, 256, 1<<63)
					want = "image_pixels"
				case "height_overflow":
					long8(1, 257, 1<<63)
					want = "image_pixels"
				case "tile_overflow":
					content = bigTIFFPayload(t, reference, "be_tile")
					order, _ = bigTIFFTestEntry(t, content, 0, 322)
					long8(0, 322, ^uint64(0))
					want = "image_pixels"
				case "block_offset_overflow":
					long8(1, 273, ^uint64(0)-5)
				case "block_size_overflow":
					long8(1, 279, ^uint64(0))
				case "pixel_header":
					long8(0, 273, 8)
				case "block_mismatch":
					_, entry := bigTIFFTestEntry(t, content, 0, 273)
					order.PutUint64(entry[4:12], 0)
				case "planar":
					long8(0, 284, 2)
					want = "unsupported_image"
				case "subifd":
					entry := content[end-28 : end-8]
					order.PutUint16(entry[:2], 330)
					order.PutUint16(entry[2:4], 18)
					order.PutUint64(entry[4:12], 1)
					order.PutUint64(entry[12:20], offset)
					want = "unsupported_image"
				case "late_compression":
					long8(1, 259, 65000)
					want = "unsupported_image"
				}
				before := bytes.Clone(content)
				info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{})
				if info.Valid() || !errors.Is(err, &Error{Code: want}) || !bytes.Equal(content, before) {
					t.Fatal("untrusted BigTIFF escaped validation", err, want)
				}
			})
		}
	}
}

func TestInspectBigTIFFAppliesWholeFileBudgets(t *testing.T) {
	reference := bigTIFFReference(t)
	for _, test := range []struct {
		name   string
		limits ImageLimits
		code   string
	}{
		{"le_pages", ImageLimits{MaxFrames: 1}, "image_frames"},
		{"le_pages", ImageLimits{MaxWidth: 6}, "image_pixels"},
		{"le_pages", ImageLimits{MaxHeight: 4}, "image_pixels"},
		{"le_pages", ImageLimits{MaxPixels: 34}, "image_pixels"},
		{"le_pages", ImageLimits{MaxTotalPixels: 40}, "image_pixels"},
		{"le_pages", ImageLimits{MaxTotalPixels: 41}, ""},
		{"be_tile", ImageLimits{MaxPixels: 255}, "image_pixels"},
		{"be_tile", ImageLimits{MaxTotalPixels: 255}, "image_pixels"},
		{"be_tile", ImageLimits{MaxTotalPixels: 256}, ""},
		{"be_tiles", ImageLimits{MaxTotalPixels: 1023}, "image_pixels"},
		{"be_tiles", ImageLimits{MaxTotalPixels: 1024}, ""},
	} {
		content := bigTIFFPayload(t, reference, test.name)
		info, err := InspectImageReader(t.Context(), bytes.NewReader(content), test.limits)
		if test.code == "" {
			if err != nil || !info.Valid() {
				t.Fatal("valid bounded BigTIFF rejected", test.name, err)
			}
		} else if info.Valid() || !errors.Is(err, &Error{Code: test.code}) {
			t.Fatal("BigTIFF budget bypass", test.name, err, test.code)
		}
	}
}

func TestInspectBigTIFFBoundsRepeatedCompressedWork(t *testing.T) {
	content := bigTIFFPayload(t, bigTIFFReference(t), "le_strips")
	order, offsets := bigTIFFTestEntry(t, content, 0, 273)
	_, counts := bigTIFFTestEntry(t, content, 0, 279)
	blocks := int(order.Uint64(offsets[4:12]))
	offsetArray := int(order.Uint64(offsets[12:20]))
	countArray := int(order.Uint64(counts[12:20]))
	if blocks < 2 || order.Uint16(offsets[2:4]) != 16 {
		t.Fatal("native LONG8 strips unavailable")
	}
	start := int(order.Uint64(content[offsetArray : offsetArray+8]))
	var size int
	switch order.Uint16(counts[2:4]) {
	case 3:
		size = int(order.Uint16(content[countArray : countArray+2]))
	case 4:
		size = int(order.Uint32(content[countArray : countArray+4]))
	case 16:
		size = int(order.Uint64(content[countArray : countArray+8]))
	default:
		t.Fatal("native byte-count type unavailable")
	}
	// LZW's end-of-information terminates pixels, but the declared compressed
	// span remains real read work. Reference that same padded strip repeatedly.
	compressed := append(bytes.Clone(content[start:start+size]), make([]byte, 1024)...)
	data := len(content)
	newCounts := data + len(compressed)
	order.PutUint16(counts[2:4], 16)
	order.PutUint64(counts[12:20], uint64(newCounts))
	for block := range blocks {
		order.PutUint64(content[offsetArray+block*8:offsetArray+block*8+8], uint64(data))
	}
	content = append(content, compressed...)
	for range blocks {
		var count [8]byte
		order.PutUint64(count[:], uint64(len(compressed)))
		content = append(content, count[:]...)
	}
	work := blocks * len(compressed)
	if len(content) >= work {
		t.Fatal("fixture did not amplify encoded work")
	}
	for _, bound := range []int{len(content), work - 1, work} {
		info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{MaxBytes: int64(bound)})
		if bound < work {
			if info.Valid() || !errors.Is(err, &Error{Code: "image_bytes"}) {
				t.Fatal("BigTIFF amplified compressed read work", bound, err)
			}
		} else if err != nil || info.Width() != 19 || info.Height() != 17 {
			t.Fatal("bounded shared strips rejected", err)
		}
	}
}

func TestBigTIFFViewsPreserveArraysAndCancellation(t *testing.T) {
	content := bigTIFFPayload(t, bigTIFFReference(t), "le_strips")
	before := bytes.Clone(content)
	limits, _ := (ImageLimits{}).Normalize()
	ctx, cancel := context.WithCancel(t.Context())
	pages, _, err := tiffImagePages(ctx, content, limits)
	if err != nil {
		t.Fatal(err)
	}
	view := pages[0].view
	if view == nil {
		t.Fatal("BigTIFF did not receive a decoder view")
	}
	var expected []byte
	narrowed := 0
	for _, segment := range view.segments {
		if !segment.narrow {
			expected = append(expected, segment.raw...)
			continue
		}
		narrowed++
		for index := 0; index < len(segment.raw); index += 8 {
			var value [4]byte
			binary.LittleEndian.PutUint32(value[:], uint32(binary.LittleEndian.Uint64(segment.raw[index:index+8])))
			expected = append(expected, value[:]...)
		}
	}
	if narrowed == 0 {
		t.Fatal("native strips did not exercise LONG8 arrays")
	}
	copy(expected[:8], view.header[:])
	for _, length := range []int{1, 3, 4, 7, 13, len(expected) + 1} {
		for offset := 0; offset < len(expected); offset++ {
			got := make([]byte, length)
			n, err := view.ReadAt(got, int64(offset))
			want := expected[offset:min(len(expected), offset+length)]
			if n != len(want) || !bytes.Equal(got[:n], want) || n < length && err != io.EOF || n == length && err != nil {
				t.Fatal("partial LONG8 view read changed bytes", offset, length, n, err)
			}
		}
	}
	if n, err := view.ReadAt(make([]byte, 1), -1); n != 0 || err == nil {
		t.Fatal("negative offset accepted")
	}
	if n, err := view.ReadAt(make([]byte, 1), view.size); n != 0 || err != io.EOF {
		t.Fatal("virtual EOF ignored")
	}
	var group sync.WaitGroup
	for range 8 {
		group.Go(func() {
			info, err := InspectImageReader(t.Context(), bytes.NewReader(content), ImageLimits{})
			if err != nil || info.Width() != 19 || info.Height() != 17 {
				t.Error("shared immutable BigTIFF changed", err)
			}
		})
	}
	group.Wait()
	cancel()
	if n, err := view.ReadAt(make([]byte, 1), 0); n != 0 || !errors.Is(err, context.Canceled) {
		t.Fatal("view ignored cancellation", err)
	}
	if !bytes.Equal(content, before) {
		t.Fatal("decoder view rewrote input")
	}
}
