package uploads

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"

	"golang.org/x/image/webp"
)

type webpImageFrame struct {
	width, height int
	alpha, pixels []byte // Complete original chunks, including their RIFF padding.
	alphaFlag     bool
}

type webpImageLayout struct {
	width, height int
	frames        []webpImageFrame
}

func inspectWebPImage(ctx context.Context, content []byte, limits ImageLimits) (ImageInfo, error) {
	layout, err := webpImageFrames(ctx, content, limits)
	if err != nil {
		return ImageInfo{}, err
	}
	for _, frame := range layout.frames {
		decoded, err := webp.Decode(frame.reader(ctx))
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
	return ImageInfo{"webp", layout.width, layout.height, len(layout.frames)}, nil
}

// All outer chunks and every frame's actual bitstream dimensions are checked
// before pixel decoding. An ANMF rectangle cannot hide a larger VP8/VP8L image.
func webpImageFrames(ctx context.Context, content []byte, limits ImageLimits) (webpImageLayout, error) {
	invalid := func() (webpImageLayout, error) { return webpImageLayout{}, &Error{Code: "invalid_image"} }
	if len(content) < 12 || string(content[:4]) != "RIFF" || string(content[8:12]) != "WEBP" || uint64(binary.LittleEndian.Uint32(content[4:8]))+8 != uint64(len(content)) {
		return invalid()
	}
	var layout webpImageLayout
	var still webpImageFrame
	var totalPixels int64
	extended, animated, control := false, false, false
	for offset := 12; offset < len(content); {
		if err := ctx.Err(); err != nil {
			return webpImageLayout{}, err
		}
		kind, data, raw, next, err := webpImageChunk(content, offset)
		if err != nil {
			return webpImageLayout{}, err
		}
		if offset == 12 && kind != "VP8X" && kind != "VP8 " && kind != "VP8L" {
			return invalid()
		}
		switch kind {
		case "VP8X":
			if offset != 12 || len(data) < 10 {
				return invalid()
			}
			extended, animated = true, data[0]&2 != 0
			layout.width, layout.height = int(webpImageUint24(data[4:7]))+1, int(webpImageUint24(data[7:10]))+1
			if !limits.dimensions(layout.width, layout.height) || int64(layout.width)*int64(layout.height) > limits.MaxTotalPixels {
				return webpImageLayout{}, &Error{Code: "image_pixels"}
			}
			still.width, still.height, still.alphaFlag = layout.width, layout.height, data[0]&16 != 0
			// The format requires readers to ignore reserved bits/bytes and
			// unknown fields; only the defined prefix is used for this view.
		case "ANIM":
			if !animated {
				break // Explicitly ignored when the animation flag is absent.
			}
			if control || len(layout.frames) != 0 || len(data) != 6 {
				return invalid()
			}
			control = true
		case "ANMF":
			if !animated || !control || len(data) < 16 {
				return invalid()
			}
			if len(layout.frames) == limits.MaxFrames {
				return webpImageLayout{}, &Error{Code: "image_frames"}
			}
			x, y := uint64(webpImageUint24(data[:3]))*2, uint64(webpImageUint24(data[3:6]))*2
			w, h := uint64(webpImageUint24(data[6:9]))+1, uint64(webpImageUint24(data[9:12]))+1
			if x+w > uint64(layout.width) || y+h > uint64(layout.height) {
				return invalid()
			}
			totalPixels += int64(w * h) // The rectangle fits the bounded canvas.
			if totalPixels > limits.MaxTotalPixels {
				return webpImageLayout{}, &Error{Code: "image_pixels"}
			}
			frame, err := webpImageSubframe(ctx, data[16:], int(w), int(h), limits)
			if err != nil {
				return webpImageLayout{}, err
			}
			layout.frames = append(layout.frames, frame)
		case "ALPH":
			if animated || !extended || still.alpha != nil || still.pixels != nil {
				return invalid()
			}
			still.alpha = raw
		case "VP8 ", "VP8L":
			if animated || still.pixels != nil || kind == "VP8L" && still.alpha != nil {
				return invalid()
			}
			still.pixels = raw
		case "ICCP":
			if !extended || control || len(layout.frames) != 0 || still.pixels != nil || still.alpha != nil {
				return invalid()
			}
		}
		offset = next
	}
	if animated {
		if !control || len(layout.frames) == 0 {
			return invalid()
		}
	} else {
		var err error
		still, err = webpImageFrameConfig(ctx, still, limits)
		if err != nil {
			return webpImageLayout{}, err
		}
		layout.width, layout.height = still.width, still.height
		layout.frames = []webpImageFrame{still}
	}
	return layout, nil
}

func webpImageSubframe(ctx context.Context, data []byte, width, height int, limits ImageLimits) (webpImageFrame, error) {
	invalid := func() (webpImageFrame, error) { return webpImageFrame{}, &Error{Code: "invalid_image"} }
	frame := webpImageFrame{width: width, height: height}
	for offset := 0; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return webpImageFrame{}, err
		}
		kind, _, raw, next, err := webpImageChunk(data, offset)
		if err != nil {
			return webpImageFrame{}, err
		}
		switch kind {
		case "ALPH":
			if frame.alpha != nil || frame.pixels != nil {
				return invalid()
			}
			frame.alpha = raw
		case "VP8 ", "VP8L":
			if frame.pixels != nil || kind == "VP8L" && frame.alpha != nil {
				return invalid()
			}
			frame.pixels = raw
		case "VP8X", "ANIM", "ANMF", "ICCP", "EXIF", "XMP ":
			return invalid()
		default:
			// Frame extensions follow its complete alpha/pixel subchunks.
			if frame.pixels == nil {
				return invalid()
			}
		}
		offset = next
	}
	frame.alphaFlag = frame.alpha != nil
	return webpImageFrameConfig(ctx, frame, limits)
}

func webpImageFrameConfig(ctx context.Context, frame webpImageFrame, limits ImageLimits) (webpImageFrame, error) {
	invalid := func() (webpImageFrame, error) { return webpImageFrame{}, &Error{Code: "invalid_image"} }
	if frame.pixels == nil {
		return invalid()
	}
	// DecodeConfig on VP8X returns the declared canvas without reading pixels.
	// Present only the original bitstream chunk to obtain its actual dimensions.
	var header [12]byte
	copy(header[:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(4+len(frame.pixels)))
	copy(header[8:], "WEBP")
	config, err := webp.DecodeConfig(io.MultiReader(imageReader{ctx, bytes.NewReader(header[:])}, imageReader{ctx, bytes.NewReader(frame.pixels)}))
	if err != nil {
		return webpImageFrame{}, imageDecodeError(ctx, err)
	}
	if !limits.dimensions(config.Width, config.Height) || int64(config.Width)*int64(config.Height) > limits.MaxTotalPixels {
		return webpImageFrame{}, &Error{Code: "image_pixels"}
	}
	if frame.width == 0 {
		frame.width, frame.height = config.Width, config.Height
	} else if config.Width != frame.width || config.Height != frame.height {
		return invalid()
	}
	if frame.alpha != nil {
		size := int(binary.LittleEndian.Uint32(frame.alpha[4:8]))
		if size == 0 {
			return invalid()
		}
		switch frame.alpha[8] & 3 {
		case 0:
			if int64(size-1) != int64(frame.width)*int64(frame.height) {
				return invalid()
			}
		case 1:
			if size == 1 {
				return invalid()
			}
		default:
			return webpImageFrame{}, &Error{Code: "unsupported_image"}
		}
	}
	return frame, nil
}

// Complete RIFF spans include their zero padding. Arithmetic is checked before
// conversion to int, also for an untrusted uint32 maximum on 32-bit targets.
func webpImageChunk(content []byte, offset int) (kind string, data, raw []byte, next int, err error) {
	invalid := func() (string, []byte, []byte, int, error) { return "", nil, nil, 0, &Error{Code: "invalid_image"} }
	if len(content)-offset < 8 {
		return invalid()
	}
	size := uint64(binary.LittleEndian.Uint32(content[offset+4 : offset+8]))
	padded := size + (size & 1)
	if padded > uint64(len(content)-offset-8) {
		return invalid()
	}
	next = offset + 8 + int(padded)
	if size&1 != 0 && content[next-1] != 0 {
		return invalid()
	}
	return string(content[offset : offset+4]), content[offset+8 : offset+8+int(size)], content[offset:next], next, nil
}

func webpImageUint24(value []byte) uint32 {
	return uint32(value[0]) | uint32(value[1])<<8 | uint32(value[2])<<16
}

func (frame webpImageFrame) reader(ctx context.Context) io.Reader {
	// The per-frame header changes no original bytes, and its canvas is bound
	// to the already checked bitstream dimensions. Alpha is decoded only when
	// present in this frame, independently of other animation frames.
	var header [30]byte
	copy(header[:4], "RIFF")
	binary.LittleEndian.PutUint32(header[4:8], uint32(22+len(frame.alpha)+len(frame.pixels)))
	copy(header[8:16], "WEBPVP8X")
	binary.LittleEndian.PutUint32(header[16:20], 10)
	if frame.alphaFlag {
		header[20] = 16
	}
	w, h := uint32(frame.width-1), uint32(frame.height-1)
	for index := range 3 {
		header[24+index], header[27+index] = byte(w>>(index*8)), byte(h>>(index*8))
	}
	return io.MultiReader(imageReader{ctx, bytes.NewReader(header[:])}, imageReader{ctx, bytes.NewReader(frame.alpha)}, imageReader{ctx, bytes.NewReader(frame.pixels)})
}
