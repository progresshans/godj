package uploads

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"reflect"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"
)

// ImageLimits bounds encoded input and decoded dimensions before allocation.
// Zero entries use the defaults below. Limits are per inspection; applications
// also own admission and the number of concurrent requests. Decoders cooperate
// with context at reads; they are not forcibly interrupted during pixel work.
type ImageLimits struct {
	MaxBytes       int64
	MaxWidth       int
	MaxHeight      int
	MaxPixels      int64
	MaxFrames      int
	MaxTotalPixels int64
}

func (limits ImageLimits) Normalize() (ImageLimits, error) {
	defaults := ImageLimits{16 << 20, 16384, 16384, 8 << 20, 128, 32 << 20}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxWidth == 0 {
		limits.MaxWidth = defaults.MaxWidth
	}
	if limits.MaxHeight == 0 {
		limits.MaxHeight = defaults.MaxHeight
	}
	if limits.MaxPixels == 0 {
		limits.MaxPixels = defaults.MaxPixels
	}
	if limits.MaxFrames == 0 {
		limits.MaxFrames = defaults.MaxFrames
	}
	if limits.MaxTotalPixels == 0 {
		limits.MaxTotalPixels = defaults.MaxTotalPixels
	}
	// The ceiling also prevents integer overflow on supported 32-bit targets.
	if limits.MaxBytes < 1 || limits.MaxBytes > 64<<20 || limits.MaxWidth < 1 || limits.MaxWidth > 65535 || limits.MaxHeight < 1 || limits.MaxHeight > 65535 || limits.MaxPixels < 1 || limits.MaxPixels > 32<<20 || limits.MaxFrames < 1 || limits.MaxFrames > 1024 || limits.MaxTotalPixels < 1 || limits.MaxTotalPixels > 64<<20 {
		return ImageLimits{}, &Error{Code: "invalid_image_limits"}
	}
	return limits, nil
}

// ImageInfo is verified content metadata, independent of the client MIME type
// and filename. Verification does not sanitize or re-encode the original bytes.
type ImageInfo struct {
	format                string
	width, height, frames int
}

func (info ImageInfo) FormatName() string { return info.format }
func (info ImageInfo) Width() int         { return info.width }
func (info ImageInfo) Height() int        { return info.height }
func (info ImageInfo) Frames() int        { return info.frames }
func (info ImageInfo) Valid() bool        { return info.format != "" }
func (info ImageInfo) ContentType() string {
	if !info.Valid() {
		return ""
	}
	if info.format == "dib" {
		return "image/bmp"
	}
	return "image/" + info.format
}
func (ImageInfo) Format(state fmt.State, _ rune) { fmt.Fprint(state, "uploads.ImageInfo{redacted}") }

// InspectImage opens and closes its own upload reader, leaving every other
// reader's cursor unchanged. It decodes static PNG/JPEG/BMP/DIB/WebP, all GIF
// frames, or every page in a classic TIFF's main directory chain. APNG,
// animated WebP, BigTIFF, TIFF SubIFDs and unsupported codec features are errors.
// Content errors use invalid_image, unsupported_image, image_bytes,
// image_pixels, or image_frames; I/O, lifetime and cancellation errors remain
// operational errors. No decoded pixels or encoded copies escape this call.
func InspectImage(ctx context.Context, file File, limits ImageLimits) (info ImageInfo, err error) {
	if nilImageValue(ctx) {
		return ImageInfo{}, &Error{Code: "invalid_context"}
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, err
	}
	limits, err = limits.Normalize()
	if err != nil {
		return ImageInfo{}, err
	}
	if !file.Valid() {
		return ImageInfo{}, &Error{Code: "invalid_file"}
	}
	if file.Size() > limits.MaxBytes {
		return ImageInfo{}, &Error{Code: "image_bytes"}
	}
	reader, err := file.Open(ctx)
	if err != nil {
		return ImageInfo{}, err
	}
	defer func() {
		if closeErr := reader.Close(); closeErr != nil {
			info, err = ImageInfo{}, errors.Join(err, closeErr)
		}
	}()
	content, err := readImageContent(ctx, reader, limits.MaxBytes, file.Size())
	if err != nil {
		return ImageInfo{}, err
	}
	return inspectImageBytes(ctx, content, limits)
}

// InspectImageReader consumes a borrowed reader from its current position to
// EOF. It does not seek, close, or retain the reader. The caller owns that
// lifetime and any metadata/length checks; InspectImage owns an upload reader.
// Reading uses at most MaxBytes plus one overflow probe byte, and every read
// and decoding stage observes ctx. A reader must honor cancellation while its
// own Read is blocked; inspection does not create a detached goroutine.
func InspectImageReader(ctx context.Context, reader io.Reader, limits ImageLimits) (ImageInfo, error) {
	if nilImageValue(ctx) {
		return ImageInfo{}, &Error{Code: "invalid_context"}
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, err
	}
	limits, err := limits.Normalize()
	if err != nil {
		return ImageInfo{}, err
	}
	if nilImageValue(reader) {
		return ImageInfo{}, &Error{Code: "invalid_reader"}
	}
	content, err := readImageContent(ctx, reader, limits.MaxBytes, -1)
	if err != nil {
		return ImageInfo{}, err
	}
	return inspectImageBytes(ctx, content, limits)
}

func readImageContent(ctx context.Context, reader io.Reader, maxBytes, exactSize int64) ([]byte, error) {
	limit := maxBytes
	if exactSize >= 0 {
		limit = min(limit, exactSize)
	}
	content := make([]byte, 0, min(int(limit), 32*1024))
	var buffer [32 * 1024]byte
	emptyReads := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := limit - int64(len(content))
		want := min(int64(len(buffer)), remaining+1)
		n, readErr := reader.Read(buffer[:int(want)])
		if n < 0 || n > int(want) {
			return nil, &Error{Code: "image_read_failed", Cause: &Error{Code: "invalid_read_count", Cause: readErr}}
		}
		if readErr != nil && readErr != io.EOF {
			return nil, &Error{Code: "image_read_failed", Cause: readErr}
		}
		if int64(n) > remaining {
			if exactSize >= 0 {
				return nil, &Error{Code: "image_read_failed"}
			}
			return nil, &Error{Code: "image_bytes"}
		}
		if n > cap(content)-len(content) {
			capacity := min(int(limit), max(len(content)+n, 2*cap(content)))
			grown := make([]byte, len(content), capacity)
			copy(grown, content)
			content = grown
		}
		content = append(content, buffer[:n]...)
		if readErr == io.EOF {
			if exactSize >= 0 && int64(len(content)) != exactSize {
				return nil, &Error{Code: "image_read_failed", Cause: io.ErrUnexpectedEOF}
			}
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return content, nil
		}
		if n == 0 {
			emptyReads++
			if emptyReads == 100 {
				return nil, &Error{Code: "image_read_failed", Cause: io.ErrNoProgress}
			}
		} else {
			emptyReads = 0
		}
	}
}

func nilImageValue(input any) bool {
	if input == nil {
		return true
	}
	switch value := reflect.ValueOf(input); value.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Chan, reflect.Func, reflect.Map, reflect.Slice:
		return value.IsNil()
	}
	return false
}

type imageReader struct {
	ctx context.Context
	*bytes.Reader
}

func (r imageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}
func (r imageReader) ReadByte() (byte, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.ReadByte()
}

func inspectImageBytes(ctx context.Context, content []byte, limits ImageLimits) (ImageInfo, error) {
	var config func(io.Reader) (image.Config, error)
	var decode func(io.Reader) (image.Image, error)
	format := ""
	switch {
	case bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n")):
		format, config, decode = "png", png.DecodeConfig, png.Decode
	case bytes.HasPrefix(content, []byte("\xff\xd8")):
		format, config, decode = "jpeg", jpeg.DecodeConfig, jpeg.Decode
	case bytes.HasPrefix(content, []byte("GIF87a")) || bytes.HasPrefix(content, []byte("GIF89a")):
		format, config = "gif", gif.DecodeConfig
	case len(content) >= 12 && string(content[:4]) == "RIFF" && string(content[8:12]) == "WEBP":
		format, config, decode = "webp", webp.DecodeConfig, webp.Decode
		if err := webpContainer(ctx, content); err != nil {
			return ImageInfo{}, err
		}
	case bytes.HasPrefix(content, []byte("BM")):
		format, config, decode = "bmp", bmp.DecodeConfig, bmp.Decode
		if err := bitmapImageBudget(content, 14, limits); err != nil {
			return ImageInfo{}, err
		}
	case isDIB(content):
		format, config, decode = "dib", bmp.DecodeConfig, bmp.Decode
		if err := bitmapImageBudget(content, 0, limits); err != nil {
			return ImageInfo{}, err
		}
	case bytes.HasPrefix(content, []byte("II\x2a\x00")) || bytes.HasPrefix(content, []byte("MM\x00\x2a")):
		return inspectTIFFImage(ctx, content, limits)
	default:
		return ImageInfo{}, &Error{Code: "unsupported_image"}
	}
	reader := func() io.Reader { return imageReader{ctx, bytes.NewReader(content)} }
	if format == "dib" {
		header, err := dibFileHeader(content)
		if err != nil {
			return ImageInfo{}, err
		}
		reader = func() io.Reader {
			return io.MultiReader(imageReader{ctx, bytes.NewReader(header[:])}, imageReader{ctx, bytes.NewReader(content)})
		}
	}
	metadata, err := config(reader())
	if err != nil {
		return ImageInfo{}, imageDecodeError(ctx, err)
	}
	if !limits.dimensions(metadata.Width, metadata.Height) || int64(metadata.Width)*int64(metadata.Height) > limits.MaxTotalPixels {
		return ImageInfo{}, &Error{Code: "image_pixels"}
	}
	if format == "png" {
		if err := pngContainer(ctx, content); err != nil {
			return ImageInfo{}, err
		}
	}
	info := ImageInfo{format, metadata.Width, metadata.Height, 1}
	if format == "gif" {
		// DecodeAll allocates every frame. Count and bound each descriptor
		// first, including frames after the first otherwise-valid image.
		info.frames, err = gifFrameBudget(ctx, content, limits)
		if err != nil {
			return ImageInfo{}, err
		}
		decoded, err := gif.DecodeAll(reader())
		if err != nil {
			return ImageInfo{}, imageDecodeError(ctx, err)
		}
		if len(decoded.Image) != info.frames || decoded.Config.Width != info.width || decoded.Config.Height != info.height {
			return ImageInfo{}, &Error{Code: "invalid_image"}
		}
	} else {
		decoded, err := decode(reader())
		if err != nil {
			return ImageInfo{}, imageDecodeError(ctx, err)
		}
		if decoded.Bounds().Dx() != info.width || decoded.Bounds().Dy() != info.height {
			return ImageInfo{}, &Error{Code: "invalid_image"}
		}
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, err
	}
	return info, nil
}

func pngContainer(ctx context.Context, content []byte) error {
	for offset := 8; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(content)-offset < 12 {
			return &Error{Code: "invalid_image"}
		}
		length := uint64(binary.BigEndian.Uint32(content[offset:]))
		if length > uint64(len(content)-offset-12) {
			return &Error{Code: "invalid_image"}
		}
		switch string(content[offset+4 : offset+8]) {
		case "acTL", "fcTL", "fdAT":
			return &Error{Code: "unsupported_image"}
		case "IEND":
			return nil
		}
		offset += int(length) + 12
	}
	return &Error{Code: "invalid_image"}
}

// The decoder returns after the first image chunk. Validate the outer framing
// too, so an advertised but absent tail, another image, or animation cannot be
// mistaken for a verified static file.
func webpContainer(ctx context.Context, content []byte) error {
	invalid := &Error{Code: "invalid_image"}
	if uint64(binary.LittleEndian.Uint32(content[4:8]))+8 != uint64(len(content)) {
		return invalid
	}
	images := 0
	for offset := 12; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(content)-offset < 8 {
			return invalid
		}
		size := uint64(binary.LittleEndian.Uint32(content[offset+4:]))
		if size+(size&1) > uint64(len(content)-offset-8) {
			return invalid
		}
		switch string(content[offset : offset+4]) {
		case "VP8X":
			if offset != 12 || size != 10 {
				return invalid
			}
			if content[offset+8]&2 != 0 {
				return &Error{Code: "unsupported_image"}
			}
		case "ANIM", "ANMF":
			return &Error{Code: "unsupported_image"}
		case "VP8 ", "VP8L":
			images++
		}
		offset += 8 + int(size+(size&1))
	}
	if images != 1 {
		return invalid
	}
	return nil
}
func imageDecodeError(ctx context.Context, err error) error {
	if canceled := ctx.Err(); canceled != nil {
		return canceled
	}
	var unsupported tiff.UnsupportedError
	if errors.Is(err, bmp.ErrUnsupported) || errors.As(err, &unsupported) {
		return &Error{Code: "unsupported_image", Cause: err}
	}
	return &Error{Code: "invalid_image", Cause: err}
}
func (limits ImageLimits) dimensions(width, height int) bool {
	return width > 0 && height > 0 && width <= limits.MaxWidth && height <= limits.MaxHeight && int64(width)*int64(height) <= limits.MaxPixels
}

func gifFrameBudget(ctx context.Context, content []byte, limits ImageLimits) (int, error) {
	invalid := func() (int, error) { return 0, &Error{Code: "invalid_image"} }
	if len(content) < 13 {
		return invalid()
	}
	offset := 13
	if content[10]&128 != 0 {
		offset += 3 << (uint(content[10]&7) + 1)
	}
	frames, pixels := 0, int64(0)
	for offset < len(content) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		block := content[offset]
		offset++
		switch block {
		case 0x3b:
			if frames == 0 {
				return invalid()
			}
			return frames, nil
		case 0x21:
			if offset >= len(content) {
				return invalid()
			}
			offset++ // extension label
		case 0x2c:
			if len(content)-offset < 9 {
				return invalid()
			}
			width := int(binary.LittleEndian.Uint16(content[offset+4:]))
			height := int(binary.LittleEndian.Uint16(content[offset+6:]))
			if !limits.dimensions(width, height) {
				return 0, &Error{Code: "image_pixels"}
			}
			frames++
			pixels += int64(width) * int64(height)
			if frames > limits.MaxFrames {
				return 0, &Error{Code: "image_frames"}
			}
			if pixels > limits.MaxTotalPixels {
				return 0, &Error{Code: "image_pixels"}
			}
			packed := content[offset+8]
			offset += 9
			if packed&128 != 0 {
				offset += 3 << (uint(packed&7) + 1)
			}
			if offset >= len(content) {
				return invalid()
			}
			offset++ // LZW code size
		default:
			return invalid()
		}
		for {
			if err := ctx.Err(); err != nil {
				return 0, err
			}
			if offset >= len(content) {
				return invalid()
			}
			length := int(content[offset])
			offset++
			if length == 0 {
				break
			}
			offset += length
		}
	}
	return invalid()
}
