package web

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"

	"github.com/progresshans/godj/storage"
)

const maxFileRanges = 16

type fileRange struct{ start, length int64 }

func (r fileRange) contentRange(size int64) string {
	return fmt.Sprintf("bytes %d-%d/%d", r.start, r.start+r.length-1, size)
}

func unsatisfiedFileRange(size int64) string { return fmt.Sprintf("bytes */%d", size) }

// parseFileRanges preserves requested order, bounds range count and prevents
// both integer overflow and amplification beyond the whole representation.
// Unknown units and empty representations ignore Range per RFC 9110. Invalid
// bytes syntax, excessive ranges and a wholly unsatisfiable set are rejected.
func parseFileRanges(value string, size int64) (ranges []fileRange, rejected bool) {
	if value == "" || size == 0 {
		return nil, false
	}
	unit, raw, found := strings.Cut(value, "=")
	unit = strings.Trim(unit, " \t")
	if !found || !validHeaderName(unit) || !strings.EqualFold(unit, "bytes") {
		return nil, false
	}
	var total int64
	amplified := false
	count := 0
	for part := range strings.SplitSeq(raw, ",") {
		count++
		if count > maxFileRanges {
			return nil, true
		}
		part = strings.Trim(part, " \t")
		if part == "" {
			continue
		}
		start, end, found := strings.Cut(part, "-")
		if !found {
			return nil, true
		}
		var selected fileRange
		if start == "" {
			length, valid := fileRangeNumber(end)
			if !valid {
				return nil, true
			}
			if length == 0 {
				continue
			}
			selected.length = min(length, size)
			selected.start = size - selected.length
		} else {
			position, valid := fileRangeNumber(start)
			if !valid {
				return nil, true
			}
			last := size - 1
			if end != "" {
				var valid bool
				last, valid = fileRangeNumber(end)
				if !valid || decimalLess(end, start) {
					return nil, true
				}
				last = min(last, size-1)
			}
			if position >= size {
				continue
			}
			selected = fileRange{start: position, length: last - position + 1}
		}
		if !amplified && selected.length > size-total {
			// Many duplicate/overlapping ranges can cost more than a full GET.
			// Continue syntax validation before choosing a full response.
			amplified = true
		} else if !amplified {
			total += selected.length
		}
		ranges = append(ranges, selected)
	}
	if len(ranges) == 0 {
		return nil, true
	}
	if amplified {
		return nil, false
	}
	return ranges, false
}

// Numerals have arbitrary precision on the wire. Saturate instead of wrapping;
// their relative order is checked separately before clamping to the file size.
func fileRangeNumber(value string) (number int64, valid bool) {
	if value == "" {
		return 0, false
	}
	for i := range value {
		if value[i] < '0' || value[i] > '9' {
			return 0, false
		}
		digit := int64(value[i] - '0')
		if number > (math.MaxInt64-digit)/10 {
			number = math.MaxInt64
		} else {
			number = number*10 + digit
		}
	}
	return number, true
}

func decimalLess(left, right string) bool {
	left, right = strings.TrimLeft(left, "0"), strings.TrimLeft(right, "0")
	return len(left) < len(right) || len(left) == len(right) && left < right
}

func selectFileRanges(response *Response, stream *Stream, reader storage.SeekableReader, ranges []fileRange, limit int64) error {
	size := stream.Size
	var selected io.Reader
	var first *fileRangeReader
	var length int64
	if len(ranges) == 1 {
		part := ranges[0]
		length = part.length
		first = &fileRangeReader{reader: reader, offset: part.start, remaining: part.length}
		selected = first
		response.header.Set("Content-Range", part.contentRange(size))
	} else {
		boundary := "godj-" + rand.Text()
		contentType := response.header.Get("Content-Type")
		parts := make([]io.Reader, 0, len(ranges)*3+1)
		appendSize := func(count int64) bool {
			if count > limit-length {
				return false
			}
			length += count
			return true
		}
		for _, part := range ranges {
			prefix := fmt.Sprintf("--%s\r\nContent-Range: %s\r\nContent-Type: %s\r\n\r\n", boundary, part.contentRange(size), contentType)
			if !appendSize(int64(len(prefix))) || !appendSize(part.length) || !appendSize(2) {
				return &Error{Code: CodeResponseTooLarge, Field: "stream", Detail: "range response exceeds the configured byte limit"}
			}
			section := &fileRangeReader{reader: reader, offset: part.start, remaining: part.length}
			if first == nil {
				first = section
			}
			parts = append(parts, strings.NewReader(prefix), section, strings.NewReader("\r\n"))
		}
		ending := "--" + boundary + "--\r\n"
		if !appendSize(int64(len(ending))) {
			return &Error{Code: CodeResponseTooLarge, Field: "stream", Detail: "range response exceeds the configured byte limit"}
		}
		parts = append(parts, strings.NewReader(ending))
		selected = io.MultiReader(parts...)
		response.header.Set("Content-Type", "multipart/byteranges; boundary="+boundary)
	}
	if length > limit {
		return &Error{Code: CodeResponseTooLarge, Field: "stream", Detail: "range response exceeds the configured byte limit"}
	}
	// Seek errors are backend errors, not unsatisfiable client ranges. Preflight
	// the first selected offset before any multipart prefix can reach the wire.
	if err := seekFileRange(reader, ranges[0].start); err != nil {
		return err
	}
	first.started = true
	response.status, stream.Size = http.StatusPartialContent, length
	stream.Reader = &fileRangeBody{Reader: selected, closer: stream.Reader}
	return nil
}

type fileRangeBody struct {
	io.Reader
	closer io.Closer
}

func (r *fileRangeBody) Close() error { return r.closer.Close() }

type fileRangeReader struct {
	reader    io.ReadSeeker
	offset    int64
	remaining int64
	started   bool
}

func seekFileRange(reader io.Seeker, offset int64) error {
	position, err := reader.Seek(offset, io.SeekStart)
	if err != nil || position != offset {
		return streamFailure("seek", err)
	}
	return nil
}

func (r *fileRangeReader) Read(buffer []byte) (int, error) {
	if len(buffer) == 0 {
		return 0, nil
	}
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if !r.started {
		if err := seekFileRange(r.reader, r.offset); err != nil {
			return 0, err
		}
		r.started = true
	}
	if int64(len(buffer)) > r.remaining {
		buffer = buffer[:int(r.remaining)]
	}
	n, err := r.reader.Read(buffer)
	if n < 0 || n > len(buffer) {
		return 0, streamFailure("read_count", nil)
	}
	r.remaining -= int64(n)
	if err != nil && err != io.EOF {
		return n, err
	}
	if r.remaining == 0 {
		return n, io.EOF // End of the selected slice, not of the whole file.
	}
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, nil
}
