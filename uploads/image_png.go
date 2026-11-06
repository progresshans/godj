package uploads

import (
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"image/png"
	"io"
	"strings"
)

type pngImageFrame struct {
	width, height int
	start, end    int
}

type pngImageLayout struct {
	frames                []pngImageFrame
	palette, transparency []byte
}

func inspectPNGImage(ctx context.Context, content []byte, limits ImageLimits) (ImageInfo, error) {
	config, err := png.DecodeConfig(imageReader{ctx, bytes.NewReader(content)})
	if err != nil {
		return ImageInfo{}, imageDecodeError(ctx, err)
	}
	if !limits.dimensions(config.Width, config.Height) || int64(config.Width)*int64(config.Height) > limits.MaxTotalPixels {
		return ImageInfo{}, &Error{Code: "image_pixels"}
	}
	layout, err := pngImageFrames(ctx, content, config.Width, config.Height, limits)
	if err != nil {
		return ImageInfo{}, err
	}
	for _, frame := range layout.frames {
		// A frame inherits the PNG pixel format, palette and transparency.
		// Only its dimensions and fdAT framing change in this private view.
		// Encoded content and the full canvas are never copied per frame.
		var header [33]byte
		copy(header[:], content[:len(header)])
		binary.BigEndian.PutUint32(header[16:20], uint32(frame.width))
		binary.BigEndian.PutUint32(header[20:24], uint32(frame.height))
		binary.BigEndian.PutUint32(header[29:33], crc32.ChecksumIEEE(header[12:29]))
		const end = "\x00\x00\x00\x00IEND\xae\x42\x60\x82"
		reader := io.MultiReader(
			imageReader{ctx, bytes.NewReader(header[:])},
			imageReader{ctx, bytes.NewReader(layout.palette)},
			imageReader{ctx, bytes.NewReader(layout.transparency)},
			&pngFrameReader{ctx: ctx, remaining: content[frame.start:frame.end]},
			strings.NewReader(end),
		)
		decoded, err := png.Decode(reader)
		if err != nil {
			return ImageInfo{}, imageDecodeError(ctx, err)
		}
		if decoded.Bounds().Dx() != frame.width || decoded.Bounds().Dy() != frame.height {
			return ImageInfo{}, &Error{Code: "invalid_image"}
		}
	}
	if err := ctx.Err(); err != nil {
		return ImageInfo{}, err
	}
	// As in the pinned ImageField/Pillow observation, a separate default image
	// is included in Frames. Display timing/compositing are not inspection I/O.
	return ImageInfo{"png", config.Width, config.Height, len(layout.frames)}, nil
}

// Scan the entire datastream before any pixels are allocated. Metadata storage
// grows only with bounded frames; data chunks remain views in the input.
func pngImageFrames(ctx context.Context, content []byte, width, height int, limits ImageLimits) (pngImageLayout, error) {
	invalid := func() (pngImageLayout, error) { return pngImageLayout{}, &Error{Code: "invalid_image"} }
	layout := pngImageLayout{frames: []pngImageFrame{{width: width, height: height}}}
	pixels := int64(width) * int64(height)
	var declared uint32
	var sequence uint64
	controls := 0
	seenIDAT, closedIDAT, hasData := false, false, false
	for offset := 8; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return pngImageLayout{}, err
		}
		if len(content)-offset < 12 {
			return invalid()
		}
		length := uint64(binary.BigEndian.Uint32(content[offset : offset+4]))
		if length > uint64(len(content)-offset-12) {
			return invalid()
		}
		next := offset + int(length) + 12
		chunk := content[offset:next]
		kind, data := string(chunk[4:8]), chunk[8:len(chunk)-4]
		checksum, err := pngImageCRC(ctx, 0, chunk[4:len(chunk)-4])
		if err != nil {
			return pngImageLayout{}, err
		}
		if checksum != binary.BigEndian.Uint32(chunk[len(chunk)-4:]) {
			return invalid()
		}
		if seenIDAT && kind != "IDAT" {
			closedIDAT = true
		}
		switch kind {
		case "IHDR":
			if offset != 8 || length != 13 {
				return invalid()
			}
		case "PLTE":
			if seenIDAT || layout.palette != nil || layout.transparency != nil || length == 0 || length > 768 || length%3 != 0 {
				return invalid()
			}
			layout.palette = chunk
		case "tRNS":
			if seenIDAT || layout.transparency != nil || length > 256 {
				return invalid()
			}
			layout.transparency = chunk
		case "acTL":
			if seenIDAT || declared != 0 || length != 8 {
				return invalid()
			}
			declared = binary.BigEndian.Uint32(data[:4])
			if declared == 0 || uint64(controls) > uint64(declared) {
				return invalid()
			}
			if uint64(declared) > uint64(limits.MaxFrames) {
				return pngImageLayout{}, &Error{Code: "image_frames"}
			}
		case "fcTL":
			if length != 26 || uint64(binary.BigEndian.Uint32(data[:4])) != sequence {
				return invalid()
			}
			sequence++
			w, h := uint64(binary.BigEndian.Uint32(data[4:8])), uint64(binary.BigEndian.Uint32(data[8:12]))
			x, y := uint64(binary.BigEndian.Uint32(data[12:16])), uint64(binary.BigEndian.Uint32(data[16:20]))
			if w == 0 || h == 0 || x+w > uint64(width) || y+h > uint64(height) || data[24] > 2 || data[25] > 1 {
				return invalid()
			}
			if !seenIDAT {
				if controls != 0 || x != 0 || y != 0 || w != uint64(width) || h != uint64(height) {
					return invalid()
				}
			} else {
				if declared == 0 || !hasData {
					return invalid()
				}
				if len(layout.frames) == limits.MaxFrames {
					return pngImageLayout{}, &Error{Code: "image_frames"}
				}
				pixels += int64(w * h) // The rectangle already fits the bounded canvas.
				if pixels > limits.MaxTotalPixels {
					return pngImageLayout{}, &Error{Code: "image_pixels"}
				}
				layout.frames[len(layout.frames)-1].end = offset
				layout.frames = append(layout.frames, pngImageFrame{int(w), int(h), next, 0})
				hasData = false
			}
			controls++
			if declared != 0 && uint64(controls) > uint64(declared) {
				return invalid()
			}
		case "IDAT":
			if closedIDAT || controls != 0 && declared == 0 {
				return invalid()
			}
			if !seenIDAT {
				// A poster which is not in the animation is still decoded and
				// consumes one frame/pixel budget in addition to declared frames.
				if declared != 0 && controls == 0 && uint64(declared)+1 > uint64(limits.MaxFrames) {
					return pngImageLayout{}, &Error{Code: "image_frames"}
				}
				layout.frames[0].start = offset
			}
			seenIDAT, hasData = true, true
		case "fdAT":
			if length < 4 || !seenIDAT || declared == 0 || len(layout.frames) < 2 || uint64(binary.BigEndian.Uint32(data[:4])) != sequence {
				return invalid()
			}
			sequence++
			hasData = true
		case "IEND":
			if length != 0 || !seenIDAT || !hasData || uint64(controls) != uint64(declared) {
				return invalid()
			}
			layout.frames[len(layout.frames)-1].end = offset
			return layout, nil
		default:
			// Unknown ancillary metadata does not change pixel decoding.
			// Unknown critical chunks could, and cannot be silently discarded.
			for _, letter := range chunk[4:8] {
				if !(letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z') {
					return invalid()
				}
			}
			if chunk[4]&0x20 == 0 {
				return pngImageLayout{}, &Error{Code: "unsupported_image"}
			}
		}
		offset = next
	}
	return invalid()
}

func pngImageCRC(ctx context.Context, checksum uint32, data []byte) (uint32, error) {
	for len(data) != 0 {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		count := min(len(data), 32<<10)
		checksum = crc32.Update(checksum, crc32.IEEETable, data[:count])
		data = data[count:]
	}
	return checksum, nil
}

// Reframe validated fdAT chunks as IDAT while borrowing their compressed data.
// No slice of chunk descriptors or complete encoded frame is allocated.
type pngFrameReader struct {
	ctx                         context.Context
	remaining, part, data, tail []byte
	header                      [8]byte
	checksum                    [4]byte
}

func (reader *pngFrameReader) Read(target []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if len(target) == 0 {
		return 0, nil
	}
	for len(reader.part) == 0 {
		if err := reader.ctx.Err(); err != nil {
			return 0, err
		}
		if len(reader.data) != 0 {
			reader.part, reader.data = reader.data, nil
			continue
		}
		if len(reader.tail) != 0 {
			reader.part, reader.tail = reader.tail, nil
			continue
		}
		if len(reader.remaining) == 0 {
			return 0, io.EOF
		}
		length := int(binary.BigEndian.Uint32(reader.remaining[:4]))
		chunk := reader.remaining[:length+12]
		reader.remaining = reader.remaining[length+12:]
		switch string(chunk[4:8]) {
		case "IDAT":
			reader.part = chunk
		case "fdAT":
			data := chunk[12 : len(chunk)-4]
			binary.BigEndian.PutUint32(reader.header[:4], uint32(len(data)))
			copy(reader.header[4:], "IDAT")
			checksum, err := pngImageCRC(reader.ctx, crc32.ChecksumIEEE(reader.header[4:]), data)
			if err != nil {
				return 0, err
			}
			binary.BigEndian.PutUint32(reader.checksum[:], checksum)
			reader.part, reader.data, reader.tail = reader.header[:], data, reader.checksum[:]
		}
	}
	n := copy(target, reader.part)
	reader.part = reader.part[n:]
	return n, nil
}
