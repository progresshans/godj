package uploads

import "encoding/binary"

func bitmapImageBudget(content []byte, base int, limits ImageLimits) error {
	if len(content)-base < 4 {
		return &Error{Code: "invalid_image"}
	}
	header := content[base:]
	length := binary.LittleEndian.Uint32(header[:4])
	if length != 40 && length != 108 && length != 124 {
		return &Error{Code: "unsupported_image"}
	}
	if uint64(length) > uint64(len(header)) {
		return &Error{Code: "invalid_image"}
	}
	width := int64(int32(binary.LittleEndian.Uint32(header[4:8])))
	height := int64(int32(binary.LittleEndian.Uint32(header[8:12])))
	if height < 0 {
		height = -height // Top-down BMP rows, including a bounded int32 minimum.
	}
	if width <= 0 || height == 0 {
		return &Error{Code: "invalid_image"}
	}
	if width > int64(limits.MaxWidth) || height > int64(limits.MaxHeight) || width*height > limits.MaxPixels || width*height > limits.MaxTotalPixels {
		return &Error{Code: "image_pixels"}
	}
	return nil
}

func isDIB(content []byte) bool {
	if len(content) < 4 {
		return false
	}
	switch binary.LittleEndian.Uint32(content[:4]) {
	case 12, 40, 52, 56, 64, 108, 124:
		return true
	}
	return false
}

// A DIB has no BMP file header. Supply only that fixed-size header to the
// pinned BMP decoder; original bytes and caller metadata stay unchanged.
func dibFileHeader(content []byte) ([14]byte, error) {
	var result [14]byte
	if len(content) < 4 {
		return result, &Error{Code: "invalid_image"}
	}
	length := binary.LittleEndian.Uint32(content[:4])
	if length != 40 && length != 108 && length != 124 {
		return result, &Error{Code: "unsupported_image"}
	}
	if uint64(length) > uint64(len(content)) {
		return result, &Error{Code: "invalid_image"}
	}
	bits := binary.LittleEndian.Uint16(content[14:16])
	colors := binary.LittleEndian.Uint32(content[32:36])
	if bits >= 1 && bits <= 8 {
		if colors == 0 {
			colors = 1 << bits
		} else if colors > 1<<bits {
			return result, &Error{Code: "invalid_image"}
		}
	} else if colors != 0 {
		return result, &Error{Code: "unsupported_image"}
	}
	offset := uint64(length) + uint64(colors)*4
	if offset > uint64(len(content)) {
		return result, &Error{Code: "invalid_image"}
	}
	copy(result[:2], "BM")
	binary.LittleEndian.PutUint32(result[2:6], uint32(len(content)+len(result)))
	binary.LittleEndian.PutUint32(result[10:14], uint32(offset)+uint32(len(result)))
	return result, nil
}
