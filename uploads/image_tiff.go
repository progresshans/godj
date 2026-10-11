package uploads

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"

	"golang.org/x/image/tiff"
)

type tiffImagePage struct {
	offset, end   uint64
	width, height int
	view          *bigTIFFDecoderView
}

func (page tiffImagePage) reader(ctx context.Context, content []byte, order binary.ByteOrder) io.Reader {
	if page.view != nil {
		return io.NewSectionReader(page.view, 0, page.view.size)
	}
	var patch [4]byte
	order.PutUint32(patch[:], uint32(page.offset))
	return io.NewSectionReader(tiffPageReader{ctx, content, patch}, 0, int64(len(content)))
}

// Values remain views into already bounded encoded input. Even a large array
// never causes another allocation proportional to its untrusted count.
type tiffImageValues struct {
	raw   []byte
	kind  uint16
	count uint64
}

func (values tiffImageValues) at(order binary.ByteOrder, index uint64) uint64 {
	switch values.kind {
	case 1:
		return uint64(values.raw[index])
	case 3:
		return uint64(order.Uint16(values.raw[index*2 : index*2+2]))
	case 16:
		return order.Uint64(values.raw[index*8 : index*8+8])
	default: // Only unsigned integer vectors pass preflight.
		return uint64(order.Uint32(values.raw[index*4 : index*4+4]))
	}
}

// Inspect every main-chain directory before decoding any pixels. The upstream
// decoder reads only the first IFD, so each bounded page receives a private
// header view over the same immutable content, without copying the whole file.
func inspectTIFFImage(ctx context.Context, content []byte, limits ImageLimits) (ImageInfo, error) {
	pages, order, err := tiffImagePages(ctx, content, limits)
	if err != nil {
		return ImageInfo{}, err
	}
	for _, page := range pages {
		decoded, err := tiff.Decode(page.reader(ctx, content, order))
		if err != nil {
			return ImageInfo{}, imageDecodeError(ctx, err)
		}
		if decoded.Bounds().Dx() != page.width || decoded.Bounds().Dy() != page.height {
			return ImageInfo{}, &Error{Code: "invalid_image"}
		}
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, err
	}
	return ImageInfo{"tiff", pages[0].width, pages[0].height, len(pages)}, nil
}

func tiffImagePages(ctx context.Context, content []byte, limits ImageLimits) ([]tiffImagePage, binary.ByteOrder, error) {
	invalid := func() ([]tiffImagePage, binary.ByteOrder, error) {
		return nil, nil, &Error{Code: "invalid_image"}
	}
	if len(content) < 8 {
		return invalid()
	}
	var order binary.ByteOrder
	switch string(content[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return invalid()
	}
	header, countBytes, entryBytes, offsetBytes := uint64(8), uint64(2), uint64(12), uint64(4)
	first := uint64(order.Uint32(content[4:8]))
	big := order.Uint16(content[2:4]) == 43
	if big {
		if len(content) < 16 || order.Uint16(content[4:6]) != 8 || order.Uint16(content[6:8]) != 0 {
			return invalid()
		}
		header, countBytes, entryBytes, offsetBytes = 16, 8, 20, 8
		first = order.Uint64(content[8:16])
	} else if order.Uint16(content[2:4]) != 42 {
		return invalid()
	}
	integer := func(raw []byte) uint64 {
		if big {
			return order.Uint64(raw)
		}
		return uint64(order.Uint32(raw))
	}
	length := uint64(len(content))
	var pages []tiffImagePage
	var totalPixels, totalBlockBytes uint64
	for offset := first; offset != 0; {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		// LibTIFF 4.7.1 writes word-aligned BigTIFF directories even when
		// their offsets are not multiples of eight. Accept those real files.
		if offset < header || offset%2 != 0 || offset > length || length-offset < countBytes+offsetBytes {
			return invalid()
		}
		count := uint64(order.Uint16(content[offset : offset+2]))
		if big {
			count = order.Uint64(content[offset : offset+8])
		}
		if count > (length-offset-countBytes-offsetBytes)/entryBytes {
			return invalid()
		}
		end := offset + countBytes + count*entryBytes + offsetBytes
		// Disjoint tables bound total metadata work by encoded bytes, and also
		// reject repeated, cyclic or partially overlapping directory chains.
		for _, earlier := range pages {
			if offset < earlier.end && earlier.offset < end {
				return invalid()
			}
		}
		if len(pages) == limits.MaxFrames {
			return nil, nil, &Error{Code: "image_frames"}
		}
		var width, height, tileWidth, tileHeight, rowsPerStrip uint64
		var stripOffsets, stripCounts, tileOffsets, tileCounts tiffImageValues
		var raster []bigTIFFRasterField
		previousTag := -1
		for position := offset + countBytes; position < end-offsetBytes; position += entryBytes {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			entry := content[position : position+entryBytes]
			tag := int(order.Uint16(entry[:2]))
			if tag <= previousTag {
				return invalid()
			}
			previousTag = tag
			typ := order.Uint16(entry[2:4])
			// Validate even ignored metadata without allocating its value array.
			widths := [...]uint64{0, 1, 1, 2, 4, 8, 1, 1, 2, 4, 8, 4, 8, 4, 0, 0, 8, 8, 8}
			if int(typ) >= len(widths) || widths[typ] == 0 || !big && typ > 13 {
				return nil, nil, &Error{Code: "unsupported_image"}
			}
			values := integer(entry[4 : 4+offsetBytes])
			if values > length/widths[typ] {
				return invalid()
			}
			size := widths[typ] * values
			start := position + 4 + offsetBytes
			if size > offsetBytes {
				start = integer(entry[4+offsetBytes : entryBytes])
				if start < header || start > length || size > length-start {
					return invalid()
				}
			}
			raw := content[start : start+size]
			if tag == 330 && values != 0 { // SubIFDs contain additional images.
				return nil, nil, &Error{Code: "unsupported_image"}
			}
			if big && bigTIFFRasterTag(tag) {
				if typ != 1 && typ != 3 && typ != 4 && typ != 16 {
					return nil, nil, &Error{Code: "unsupported_image"}
				}
				raster = append(raster, bigTIFFRasterField{uint16(tag), start, tiffImageValues{raw, typ, values}})
			}
			switch tag {
			case 273, 279, 324, 325:
				if values == 0 || typ != 1 && typ != 3 && typ != 4 && typ != 16 {
					return invalid()
				}
				vector := tiffImageValues{raw, typ, values}
				switch tag {
				case 273:
					stripOffsets = vector
				case 279:
					stripCounts = vector
				case 324:
					tileOffsets = vector
				case 325:
					tileCounts = vector
				}
			case 256, 257, 278, 284, 322, 323, 32997, 32998:
				if values != 1 || typ != 1 && typ != 3 && typ != 4 && typ != 16 {
					return invalid()
				}
				value := (tiffImageValues{raw, typ, values}).at(order, 0)
				switch tag {
				case 256:
					width = value
				case 257:
					height = value
				case 278:
					rowsPerStrip = value
				case 322:
					tileWidth = value
				case 323:
					tileHeight = value
				default:
					if value != 1 { // Separate planes and volume tiles need other decoders.
						return nil, nil, &Error{Code: "unsupported_image"}
					}
				}
			}
		}
		if width == 0 || height == 0 || (tileWidth == 0) != (tileHeight == 0) {
			return invalid()
		}
		// Bound 64-bit dimensions before multiplying or converting to int.
		if width > uint64(limits.MaxWidth) || height > uint64(limits.MaxHeight) {
			return nil, nil, &Error{Code: "image_pixels"}
		}
		pixels := width * height
		if pixels > uint64(limits.MaxPixels) {
			return nil, nil, &Error{Code: "image_pixels"}
		}
		blockHeight := height
		if rowsPerStrip != 0 && rowsPerStrip < blockHeight {
			blockHeight = rowsPerStrip
		}
		blocks := (height + blockHeight - 1) / blockHeight
		offsets, counts := stripOffsets, stripCounts
		if tileWidth != 0 {
			// Decoder work includes padding in every tile. Check products by
			// division before multiplication, including on 32-bit platforms.
			ceiling := uint64(limits.MaxPixels)
			if tileWidth > ceiling || tileHeight > ceiling/tileWidth {
				return nil, nil, &Error{Code: "image_pixels"}
			}
			blockPixels := tileWidth * tileHeight
			blocks = ((width + tileWidth - 1) / tileWidth) * ((height + tileHeight - 1) / tileHeight)
			if blocks > ceiling/blockPixels {
				return nil, nil, &Error{Code: "image_pixels"}
			}
			pixels = max(pixels, blocks*blockPixels)
			offsets, counts = tileOffsets, tileCounts
		}
		if pixels > uint64(limits.MaxTotalPixels)-totalPixels {
			return nil, nil, &Error{Code: "image_pixels"}
		}
		totalPixels += pixels
		if offsets.count != blocks || counts.count != blocks {
			return invalid()
		}
		for block := uint64(0); block < blocks; block++ {
			if err := ctx.Err(); err != nil {
				return nil, nil, err
			}
			start, size := offsets.at(order, block), counts.at(order, block)
			// The per-page header view must never become pixel data. Check full
			// encoded spans before the decoder can allocate or follow offsets.
			if start < header || start > length || size == 0 || size > length-start {
				return invalid()
			}
			// Shared/overlapping blocks are legal within the inspection budget;
			// repeated decompression of a small file cannot amplify encoded work
			// beyond MaxBytes across all strips, tiles and pages.
			if size > uint64(limits.MaxBytes)-totalBlockBytes {
				return nil, nil, &Error{Code: "image_bytes"}
			}
			totalBlockBytes += size
		}
		page := tiffImagePage{offset: offset, end: end, width: int(width), height: int(height)}
		if big {
			var err error
			page.view, err = newBigTIFFDecoderView(ctx, content, order, raster)
			if err != nil {
				return nil, nil, err
			}
		}
		config, err := tiff.DecodeConfig(page.reader(ctx, content, order))
		if err != nil {
			return nil, nil, imageDecodeError(ctx, err)
		}
		if config.Width != int(width) || config.Height != int(height) {
			return invalid()
		}
		pages = append(pages, page)
		offset = integer(content[end-offsetBytes : end])
	}
	if len(pages) == 0 {
		return invalid()
	}
	return pages, order, nil
}

type tiffPageReader struct {
	ctx     context.Context
	content []byte
	patch   [4]byte
}

func (reader tiffPageReader) ReadAt(target []byte, offset int64) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	n, err := bytes.NewReader(reader.content).ReadAt(target, offset)
	if n > 0 && offset < 8 && offset+int64(n) > 4 {
		begin, end := max(int64(4), offset), min(int64(8), offset+int64(n))
		copy(target[begin-offset:end-offset], reader.patch[begin-4:end-4])
	}
	return n, err
}
