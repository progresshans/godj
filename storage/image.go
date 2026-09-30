package storage

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/progresshans/godj/uploads"
)

// ImageInspection describes the content consumed from one independently opened
// storage reader. It is not access authority, a database commit, or a promise
// that the name or file will still have the same content later.
type ImageInspection struct {
	file  Info
	image uploads.ImageInfo
}

func (result ImageInspection) Valid() bool              { return result.file.Valid() && result.image.Valid() }
func (result ImageInspection) File() Info               { return result.file }
func (result ImageInspection) Image() uploads.ImageInfo { return result.image }
func (ImageInspection) Format(state fmt.State, _ rune) {
	fmt.Fprint(state, "storage.ImageInspection{redacted}")
}

// InspectImage opens the authorized name once, fully checks the image content,
// and closes only that new reader. It never calls Stat, saves, deletes, or
// rewinds another reader. Optional Reader metadata must name this exact file
// and match the bytes read. Without that capability the byte count is measured
// and content version/modification metadata remains unknown.
//
// Content/limit errors use uploads.Error; backend, lifetime and cancellation
// causes remain discoverable with errors.Is/As. Every failure, including Close
// or cancellation during Close, returns a zero inspection. Backend callbacks
// retain their usual panic behavior; an acquired reader is closed on unwinding.
func InspectImage(ctx context.Context, backend Backend, name string, limits uploads.ImageLimits) (result ImageInspection, err error) {
	if err := contextError(ctx); err != nil {
		return ImageInspection{}, err
	}
	limits, err = limits.Normalize()
	if err != nil {
		return ImageInspection{}, err
	}
	if nilValue(backend) {
		return ImageInspection{}, &Error{Code: "invalid_backend"}
	}
	if err := validateName(name); err != nil {
		return ImageInspection{}, err
	}
	reader, err := backend.Open(ctx, name)
	if !nilValue(reader) {
		defer func() {
			if closeErr := reader.Close(); closeErr != nil {
				err = errors.Join(err, &Error{Code: "image_close_failed", Cause: closeErr})
			}
			if canceled := ctx.Err(); canceled != nil {
				err = errors.Join(err, canceled)
			}
			if err != nil {
				result = ImageInspection{}
			}
		}()
	}
	if err != nil {
		return ImageInspection{}, &Error{Code: "image_open_failed", Cause: err}
	}
	if nilValue(reader) {
		return ImageInspection{}, &Error{Code: "invalid_reader"}
	}
	if err := contextError(ctx); err != nil {
		return ImageInspection{}, err
	}
	measured := &imageContentReader{reader: reader, expected: -1}
	var file Info
	if capable, ok := reader.(Reader); ok {
		file = capable.Info()
		if !file.Valid() || file.Name() != name {
			return ImageInspection{}, &Error{Code: "invalid_result"}
		}
		if file.Size() > limits.MaxBytes {
			return ImageInspection{}, &uploads.Error{Code: "image_bytes"}
		}
		measured.expected = file.Size()
	}
	image, err := uploads.InspectImageReader(ctx, measured, limits)
	if err != nil {
		return ImageInspection{}, err
	}
	if !file.Valid() {
		file, err = NewInfo(name, measured.size)
		if err != nil {
			return ImageInspection{}, err
		}
	}
	return ImageInspection{file: file, image: image}, nil
}

type imageContentReader struct {
	reader         io.Reader
	size, expected int64
}

func (r *imageContentReader) Read(target []byte) (int, error) {
	if r.expected >= 0 {
		target = target[:min(len(target), int(r.expected-r.size+1))]
	}
	n, err := r.reader.Read(target)
	if n < 0 || n > len(target) {
		return 0, &Error{Code: "invalid_read_count", Cause: err}
	}
	r.size += int64(n)
	if r.expected >= 0 && (r.size > r.expected || err == io.EOF && r.size != r.expected) {
		return n, &Error{Code: "invalid_result", Cause: errors.Join(err, io.ErrUnexpectedEOF)}
	}
	return n, err
}
