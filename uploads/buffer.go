package uploads

import "fmt"

// Buffer owns a detached in-memory upload for a non-HTTP invocation. Its File
// and every reader expire at Close. Copies share the same lifetime.
type Buffer struct{ file File }

func NewBuffer(name, contentType string, content []byte) (*Buffer, error) {
	file, err := NewFile(name, contentType, content)
	if err != nil {
		return nil, err
	}
	file.state.owner = &owner{readers: make(map[*readerState]struct{}), files: []*fileState{file.state}}
	return &Buffer{file: file}, nil
}

func (buffer *Buffer) File() File {
	if buffer == nil {
		return File{}
	}
	return buffer.file
}

func (buffer *Buffer) Close() error {
	if buffer == nil || buffer.file.state == nil {
		return nil
	}
	return buffer.file.state.owner.close()
}

func (Buffer) Format(state fmt.State, _ rune) { fmt.Fprint(state, "uploads.Buffer{redacted}") }
