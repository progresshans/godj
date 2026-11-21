package testprocess

import (
	"bytes"
	"sync"
)

// NewBuffer keeps at most maximum bytes while accepting every write in full.
func NewBuffer(maximum int) *Buffer { return &Buffer{maximum: maximum} }

type Buffer struct {
	mu        sync.Mutex
	buffer    bytes.Buffer
	maximum   int
	truncated bool
}

func (buffer *Buffer) Write(document []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.writeLocked(document)
}

func (buffer *Buffer) writeLocked(document []byte) (int, error) {
	original := len(document)
	if original == 0 {
		return 0, nil
	}
	remaining := buffer.maximum - buffer.buffer.Len()
	if remaining <= 0 {
		buffer.truncated = true
		return original, nil
	}
	if len(document) > remaining {
		buffer.truncated = true
		document = document[:remaining]
	}
	_, _ = buffer.buffer.Write(document)
	return original, nil
}

func (buffer *Buffer) String() string {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String()
}

// Truncated reports discarded output, including writes after the buffer filled.
func (buffer *Buffer) Truncated() bool {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.truncated
}

// Bytes returns a copy so callers cannot mutate retained output.
func (buffer *Buffer) Bytes() []byte {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return bytes.Clone(buffer.buffer.Bytes())
}

func (buffer *Buffer) Snapshot() (string, bool) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	return buffer.buffer.String(), buffer.truncated
}

// ReadinessBuffer reports the first complete line with prefix. It retains the
// same bounded diagnostic output even if a writer fragments or joins lines.
type ReadinessBuffer struct {
	*Buffer
	prefix    string
	ready     chan string
	scanned   int
	published bool
}

func NewReadinessBuffer(maximum int, prefix string) *ReadinessBuffer {
	return &ReadinessBuffer{Buffer: NewBuffer(maximum), prefix: prefix, ready: make(chan string, 1)}
}

func (buffer *ReadinessBuffer) Ready() <-chan string { return buffer.ready }

func (buffer *ReadinessBuffer) Write(document []byte) (int, error) {
	buffer.mu.Lock()
	defer buffer.mu.Unlock()
	n, err := buffer.writeLocked(document)
	contents := buffer.buffer.Bytes()
	for !buffer.published && buffer.scanned < len(contents) {
		relativeEnd := bytes.IndexByte(contents[buffer.scanned:], '\n')
		if relativeEnd < 0 {
			break
		}
		end := buffer.scanned + relativeEnd
		line := contents[buffer.scanned:end]
		buffer.scanned = end + 1
		if bytes.HasPrefix(line, []byte(buffer.prefix)) {
			buffer.ready <- string(line[len(buffer.prefix):])
			buffer.published = true
		}
	}
	return n, err
}
