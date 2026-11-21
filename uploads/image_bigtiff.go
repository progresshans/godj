package uploads

import (
	"context"
	"encoding/binary"
	"io"
)

// These are the raster fields consumed by the pinned x/image TIFF decoder.
// All fields, including ignored metadata, have already passed structural
// validation. Keeping only raster fields avoids truncating a 64-bit IFD count
// into the classic decoder's 16-bit count. Original file bytes are preserved.
func bigTIFFRasterTag(tag int) bool {
	switch tag {
	case 256, 257, 258, 259, 262, 266, 273, 278, 279, 292, 293,
		317, 320, 322, 323, 324, 325, 338, 339:
		return true
	}
	return false
}

type bigTIFFRasterField struct {
	tag    uint16
	start  uint64
	values tiffImageValues
}

type bigTIFFSegment struct {
	start  int64
	raw    []byte
	narrow bool // unsigned LONG8 values exposed as classic LONG values
}

func (segment bigTIFFSegment) size() int64 {
	if segment.narrow {
		return int64(len(segment.raw) / 2)
	}
	return int64(len(segment.raw))
}

// A decoder-only view supplies a small classic IFD and header. Pixel offsets
// still address the original immutable input. Ordinary value arrays are views
// into that input, and LONG8 arrays are narrowed only as the decoder reads them.
// Neither file-sized copies nor allocations from untrusted counts are needed.
type bigTIFFDecoderView struct {
	ctx      context.Context
	order    binary.ByteOrder
	header   [8]byte
	segments []bigTIFFSegment
	size     int64
}

func newBigTIFFDecoderView(ctx context.Context, content []byte, order binary.ByteOrder, fields []bigTIFFRasterField) (*bigTIFFDecoderView, error) {
	view := &bigTIFFDecoderView{ctx: ctx, order: order, size: int64(len(content))}
	copy(view.header[:2], content[:2])
	order.PutUint16(view.header[2:4], 42)
	padding := len(content) % 2
	order.PutUint32(view.header[4:8], uint32(len(content)+padding))
	directory := make([]byte, padding+2+len(fields)*12+4)
	order.PutUint16(directory[padding:padding+2], uint16(len(fields)))
	view.segments = append(view.segments, bigTIFFSegment{raw: content}, bigTIFFSegment{start: view.size, raw: directory})
	view.size += int64(len(directory))
	for index, field := range fields {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		entry := directory[padding+2+index*12 : padding+2+(index+1)*12]
		kind := field.values.kind
		if kind == 16 {
			kind = 4
		}
		order.PutUint16(entry[:2], field.tag)
		order.PutUint16(entry[2:4], kind)
		order.PutUint32(entry[4:8], uint32(field.values.count))
		if field.values.kind != 16 {
			if len(field.values.raw) <= 4 {
				copy(entry[8:12], field.values.raw)
			} else {
				order.PutUint32(entry[8:12], uint32(field.start))
			}
			continue
		}
		if field.values.count <= 1 {
			if field.values.count == 1 {
				value := field.values.at(order, 0)
				if value > 1<<32-1 {
					return nil, &Error{Code: "invalid_image"}
				}
				order.PutUint32(entry[8:12], uint32(value))
			}
			continue
		}
		segment := bigTIFFSegment{view.size, field.values.raw, true}
		if segment.size() > (1<<32-1)-view.size {
			return nil, &Error{Code: "invalid_image"}
		}
		order.PutUint32(entry[8:12], uint32(segment.start))
		view.segments = append(view.segments, segment)
		view.size += segment.size()
	}
	return view, nil
}

func (view *bigTIFFDecoderView) ReadAt(target []byte, offset int64) (int, error) {
	if err := view.ctx.Err(); err != nil {
		return 0, err
	}
	if offset < 0 {
		return 0, &Error{Code: "invalid_image"}
	}
	if offset >= view.size {
		return 0, io.EOF
	}
	n := int(min(int64(len(target)), view.size-offset))
	done := 0
	for _, segment := range view.segments {
		at := offset + int64(done)
		if at < segment.start || at >= segment.start+segment.size() {
			continue
		}
		take := int(min(int64(n-done), segment.start+segment.size()-at))
		if !segment.narrow {
			copy(target[done:done+take], segment.raw[at-segment.start:at-segment.start+int64(take)])
			done += take
		} else {
			end := done + take
			for done < end {
				if err := view.ctx.Err(); err != nil {
					return done, err
				}
				position := offset + int64(done) - segment.start
				raw := segment.raw[(position/4)*8 : (position/4)*8+8]
				value := view.order.Uint64(raw)
				if value > 1<<32-1 {
					return done, &Error{Code: "invalid_image"}
				}
				var narrowed [4]byte
				view.order.PutUint32(narrowed[:], uint32(value))
				part := min(end-done, 4-int(position%4))
				copy(target[done:done+part], narrowed[position%4:position%4+int64(part)])
				done += part
			}
		}
		if done == n {
			break
		}
	}
	if offset < 8 {
		end := min(int64(8), offset+int64(n))
		copy(target[:end-offset], view.header[offset:end])
	}
	if n < len(target) {
		return n, io.EOF
	}
	return n, nil
}
