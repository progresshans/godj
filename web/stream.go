package web

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// Stream transfers ownership of Reader to Application, even when an opener
// returns it with an error. Size is the exact byte count, or -1 when unknown.
// A finite stream must end with EOF. Its I/O must honor the supplied context;
// the runtime checks cancellation between reads/writes but cannot interrupt
// an arbitrary reader blocked inside Read. Application closes it exactly once.
// An opener owns resources it acquires but cannot return (for example, if it
// panics before transferring them); it must clean those up itself.
type Stream struct {
	Reader io.ReadCloser
	Size   int64
}

func (Stream) Format(s fmt.State, _ rune) { fmt.Fprint(s, "web.Stream{redacted}") }

type streamSpec struct {
	open     func(context.Context) (Stream, error)
	fileName string
}

// NewStreamResponse describes finite streaming content without opening it.
// Every successful request opens its own reader after all middleware has
// accepted the response. Dropped/error responses open nothing. The opener must
// not retain a borrowed Request, upload, or transaction session; capture only
// authorized immutable values and application-lifetime capabilities.
//
// Before headers are sent, stream failures become the ordinary fixed 500.
// Afterwards the transport is aborted, never followed by a replacement error
// body or a normal end-of-stream. Response size limits apply to bytes, while
// transfer buffering stays constant. This is not an SSE/WebSocket API.
func NewStreamResponse(status int, header http.Header, open func(context.Context) (Stream, error)) (Response, error) {
	if open == nil || status == http.StatusNoContent || status == http.StatusNotModified {
		return Response{}, &Error{Code: CodeInvalidResponse, Field: "stream", Detail: "stream requires an opener and a body-bearing status"}
	}
	if err := validateStreamHeaders(header); err != nil {
		return Response{}, err
	}
	response, err := NewResponse(status, header, nil)
	if err != nil {
		return Response{}, err
	}
	response.stream = &streamSpec{open: open}
	return response, nil
}

func validateStreamHeaders(header http.Header) error {
	for name := range header {
		switch strings.ToLower(name) {
		case "content-length", "transfer-encoding", "trailer", "connection", "keep-alive", "proxy-connection", "upgrade":
			return &Error{Code: CodeInvalidResponse, Field: "header", Detail: "stream framing headers belong to the transport"}
		}
	}
	return nil
}

func nilStreamValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	}
	return false
}

func streamFailure(field string, cause error) error {
	return &Error{Code: CodeStreamFailure, Field: field, Detail: "stream operation failed", Cause: cause}
}

// writeStream holds the final known-length chunk until EOF is confirmed so a
// short/oversized/failed source cannot look like a complete Content-Length
// response. Unknown lengths complete only by returning normally to net/http.
// A close failure is reported separately: it cannot rewrite delivered content.
func writeStream(ctx context.Context, writer http.ResponseWriter, response Response, request *http.Request, limit int64) (committed bool, err, closeErr error) {
	var stream Stream
	defer func() {
		if recover() != nil {
			err = streamFailure("panic", nil)
		}
		if !nilStreamValue(stream.Reader) {
			closeErr = closeStream(stream.Reader)
		}
	}()
	if err = ctx.Err(); err != nil {
		return false, err, nil
	}
	stream, err = response.stream.open(ctx)
	if err != nil {
		return false, streamFailure("open", err), nil
	}
	if nilStreamValue(stream.Reader) || stream.Size < -1 {
		return false, streamFailure("source", nil), nil
	}
	bodyless := false
	if response.stream.fileName != "" {
		bodyless, err = prepareFileStream(ctx, request, &response, &stream, limit)
		if err != nil {
			return false, err, nil
		}
	}
	if stream.Size > limit {
		return false, &Error{Code: CodeResponseTooLarge, Field: "stream", Detail: "stream exceeds the configured byte limit"}, nil
	}
	if err = ctx.Err(); err != nil {
		return false, err, nil
	}
	start := func() {
		if committed {
			return
		}
		// Mark first: a writer that panics during headers may already have
		// committed bytes, so a second response would be unsafe.
		committed = true
		copyResponseHeaders(writer.Header(), response.header)
		if response.status == http.StatusNotModified {
			writer.Header().Del("Content-Length")
		} else if stream.Size >= 0 {
			writer.Header().Set("Content-Length", strconv.FormatInt(stream.Size, 10))
		} else {
			writer.Header().Del("Content-Length")
		}
		writer.WriteHeader(response.status)
	}
	if request.Method == http.MethodHead || bodyless {
		start()
		return committed, nil, nil
	}
	maximum := limit
	if stream.Size >= 0 {
		maximum = stream.Size
	}
	buffer := make([]byte, 32<<10)
	var total int64
	for {
		remaining := maximum - total
		want := int64(len(buffer))
		if remaining < want {
			want = remaining + 1 // detect a byte beyond the exact size/limit
		}
		n, readErr := readStream(ctx, stream.Reader, buffer[:int(want)])
		if readErr != nil && readErr != io.EOF {
			return committed, streamFailure("read", readErr), nil
		}
		if int64(n) > remaining {
			if stream.Size < 0 {
				return committed, &Error{Code: CodeResponseTooLarge, Field: "stream", Detail: "stream exceeds the configured byte limit"}, nil
			}
			return committed, streamFailure("size", nil), nil
		}
		total += int64(n)
		finished := readErr == io.EOF
		if !finished && total == maximum {
			var extra [1]byte
			count, tailErr := readStream(ctx, stream.Reader, extra[:])
			if count != 0 || tailErr != io.EOF {
				if tailErr != nil && tailErr != io.EOF {
					return committed, streamFailure("read", tailErr), nil
				}
				if stream.Size < 0 {
					return committed, &Error{Code: CodeResponseTooLarge, Field: "stream", Detail: "stream exceeds the configured byte limit"}, nil
				}
				return committed, streamFailure("size", nil), nil
			}
			finished = true
		}
		if finished && stream.Size >= 0 && total != stream.Size {
			return committed, streamFailure("size", io.ErrUnexpectedEOF), nil
		}
		if err = ctx.Err(); err != nil {
			return committed, err, nil
		}
		start()
		if n != 0 {
			written, writeErr := writer.Write(buffer[:n])
			if writeErr != nil {
				return committed, streamFailure("write", writeErr), nil
			}
			if written != n {
				return committed, streamFailure("write", io.ErrShortWrite), nil
			}
		}
		if finished {
			return committed, nil, nil
		}
	}
}

func readStream(ctx context.Context, reader io.Reader, buffer []byte) (int, error) {
	for empty := 0; empty < 100; empty++ {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := reader.Read(buffer)
		if n < 0 || n > len(buffer) {
			return 0, streamFailure("read_count", nil)
		}
		if n != 0 || err != nil {
			return n, err
		}
	}
	return 0, io.ErrNoProgress
}

func closeStream(reader io.Closer) (err error) {
	defer func() {
		if recover() != nil {
			err = streamFailure("close_panic", nil)
		}
	}()
	if err := reader.Close(); err != nil {
		return streamFailure("close", err)
	}
	return nil
}
