package storage

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"io/fs"
	"math"
	"strings"
	"sync"
	"unicode/utf8"
)

// MemoryConfig bounds one process-local store. MaxBytes counts content held by
// pending saves, published files and unlinked files with open readers. It is
// not a process RSS limit: buffers, names and reader handles also use memory.
// MaxFiles includes pending and reader-retained files. MaxConcurrentSaves
// bounds source callbacks and their fixed-size transfer buffers.
// Zero values select 64 MiB, 4096 files and at most 32 concurrent saves.
type MemoryConfig struct {
	Limits             Limits
	MaxBytes           int64
	MaxFiles           int
	MaxConcurrentSaves int
	// Random is borrowed and serialized by this instance. nil uses crypto/rand.
	Random io.Reader
}

func (MemoryConfig) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.MemoryConfig{redacted}") }

// Memory is an isolated, bounded, non-durable store. Copies of its facade share
// one lifecycle. Open readers retain immutable content and independent cursors
// across Delete, name reuse and Close. There is no backing path or global store.
type Memory struct{ state *memoryState }

type memoryState struct {
	life      sync.RWMutex
	mu        sync.Mutex
	entropyMu sync.Mutex
	closed    bool
	limits    Limits
	maxBytes  int64
	maxFiles  int
	maxSaves  int
	random    io.Reader
	files     map[string]*memoryBlob
	bytes     int64
	live      int
	pending   int
}

type memoryBlob struct {
	data      []byte
	readers   int
	published bool
}

// NewMemory validates configuration without I/O, source reads or entropy use.
func NewMemory(config MemoryConfig) (*Memory, error) {
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	if config.MaxBytes == 0 {
		config.MaxBytes = 64 << 20
	}
	if config.MaxFiles == 0 {
		config.MaxFiles = 4096
	}
	if config.MaxConcurrentSaves == 0 {
		config.MaxConcurrentSaves = min(32, config.MaxFiles)
	}
	if config.MaxBytes < 1 || config.MaxBytes == math.MaxInt64 || config.MaxFiles < 1 || config.MaxFiles > 1<<20 || config.MaxConcurrentSaves < 1 || config.MaxConcurrentSaves > config.MaxFiles {
		return nil, &Error{Code: "invalid_limits", Outcome: NotPublished}
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	} else if nilValue(random) {
		return nil, &Error{Code: "invalid_entropy", Outcome: NotPublished}
	}
	return &Memory{state: &memoryState{limits: limits, maxBytes: config.MaxBytes, maxFiles: config.MaxFiles, maxSaves: config.MaxConcurrentSaves, random: random, files: make(map[string]*memoryBlob)}}, nil
}

func (Memory) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.Memory{redacted}") }

func (m *Memory) lock(ctx context.Context) (*memoryState, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if m == nil || m.state == nil {
		return nil, &Error{Code: "closed", Outcome: NotPublished}
	}
	s := m.state
	s.life.RLock()
	if s.closed {
		s.life.RUnlock()
		return nil, &Error{Code: "closed", Outcome: NotPublished}
	}
	if err := contextError(ctx); err != nil {
		s.life.RUnlock()
		return nil, err
	}
	return s, nil
}

func (s *memoryState) nextName(name string, limit int) (string, error) {
	s.entropyMu.Lock()
	defer s.entropyMu.Unlock()
	return alternativeName(name, limit, s.random)
}

// directory reports a virtual prefix of currently published files. Empty
// directories are not stored; no directory listing or recursive deletion API
// is exposed. This keeps namespace metadata bounded by the file count.
func (s *memoryState) directory(name string) bool {
	prefix := name + "/"
	for key := range s.files {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

func (s *memoryState) parentFile(name string) bool {
	for i := range name {
		if name[i] == '/' && s.files[name[:i]] != nil {
			return true
		}
	}
	return false
}

// Save borrows source, reads it once into private content, then atomically
// publishes a collision-safe name. Rejected input, cancellation and callback
// panic cannot expose partial content. Close waits for active operations; an
// arbitrary source blocked inside Read must cooperate with cancellation.
func (m *Memory) Save(ctx context.Context, name string, source io.Reader, options SaveOptions) (Info, error) {
	if err := validateName(name); err != nil {
		return Info{}, err
	}
	s, err := m.lock(ctx)
	if err != nil {
		return Info{}, err
	}
	defer s.life.RUnlock()
	if nilValue(source) {
		return Info{}, &Error{Code: "invalid_input", Outcome: NotPublished}
	}
	limit, err := nameLimit(s.limits, options)
	if err != nil {
		return Info{}, err
	}
	s.mu.Lock()
	if s.live == s.maxFiles || s.pending == s.maxSaves {
		s.mu.Unlock()
		return Info{}, &Error{Code: "capacity_exceeded", Outcome: NotPublished}
	}
	if s.parentFile(name) {
		s.mu.Unlock()
		return Info{}, &Error{Code: "directory_failed", Outcome: NotPublished}
	}
	s.live++
	s.pending++
	s.mu.Unlock()
	var held int64
	published := false
	defer func() {
		s.mu.Lock()
		s.pending--
		if !published {
			s.live--
			s.bytes -= held
		}
		s.mu.Unlock()
	}()
	var content []byte
	buffer := make([]byte, 32<<10)
	empty := 0
	for {
		if err := contextError(ctx); err != nil {
			return Info{}, err
		}
		remaining := s.limits.MaxFileBytes - int64(len(content))
		s.mu.Lock()
		free := s.maxBytes - s.bytes
		want := min(int64(len(buffer)), remaining+1, free+1)
		claim := min(want, free)
		s.bytes += claim
		held += claim
		s.mu.Unlock()
		n, readErr := source.Read(buffer[:int(want)])
		if n < 0 || n > int(want) {
			return Info{}, &Error{Code: "invalid_read_count", Outcome: NotPublished}
		}
		if readErr != nil && readErr != io.EOF {
			return Info{}, &Error{Code: "input_read_failed", Outcome: NotPublished, Cause: readErr}
		}
		if int64(n) > remaining {
			return Info{}, &Error{Code: "file_too_large", Outcome: NotPublished}
		}
		if int64(n) > claim {
			return Info{}, &Error{Code: "capacity_exceeded", Outcome: NotPublished}
		}
		s.mu.Lock()
		s.bytes -= claim - int64(n)
		held -= claim - int64(n)
		s.mu.Unlock()
		content = append(content, buffer[:n]...)
		if readErr == io.EOF {
			break
		}
		if n == 0 {
			empty++
			if empty == 100 {
				return Info{}, &Error{Code: "input_read_failed", Outcome: NotPublished, Cause: io.ErrNoProgress}
			}
		} else {
			empty = 0
		}
	}
	candidate := name
	for attempt := 0; attempt < s.limits.MaxAttempts; attempt++ {
		if err := contextError(ctx); err != nil {
			return Info{}, err
		}
		if attempt != 0 || utf8.RuneCountInString(candidate) > limit {
			candidate, err = s.nextName(name, limit)
			if err != nil {
				return Info{}, err
			}
		}
		s.mu.Lock()
		if err := contextError(ctx); err != nil {
			s.mu.Unlock()
			return Info{}, err
		}
		if s.parentFile(candidate) {
			s.mu.Unlock()
			return Info{}, &Error{Code: "directory_failed", Outcome: NotPublished}
		}
		if s.files[candidate] != nil || s.directory(candidate) {
			s.mu.Unlock()
			continue
		}
		s.files[candidate] = &memoryBlob{data: content, published: true}
		published = true
		s.mu.Unlock()
		return Info{name: candidate, size: int64(len(content))}, nil
	}
	return Info{}, &Error{Code: "collision_limit", Outcome: NotPublished}
}

func (m *Memory) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	s, err := m.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	blob := s.files[name]
	if blob == nil {
		return nil, s.missing(name)
	}
	blob.readers++
	reader := &memoryContentReader{reader: bytes.NewReader(blob.data), owner: s, blob: blob}
	return &fileReader{state: &fileReaderState{ctx: ctx, reader: reader, info: Info{name: name, size: int64(len(blob.data))}}}, nil
}

func (m *Memory) Stat(ctx context.Context, name string) (Info, error) {
	if err := validateName(name); err != nil {
		return Info{}, err
	}
	s, err := m.lock(ctx)
	if err != nil {
		return Info{}, err
	}
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if blob := s.files[name]; blob != nil {
		return Info{name: name, size: int64(len(blob.data))}, nil
	}
	return Info{}, s.missing(name)
}

func (s *memoryState) missing(name string) error {
	if s.directory(name) {
		return &Error{Code: "not_regular"}
	}
	return &Error{Code: "not_found", Cause: fs.ErrNotExist}
}

func (s *memoryState) release(blob *memoryBlob) {
	if !blob.published && blob.readers == 0 {
		s.bytes -= int64(len(blob.data))
		s.live--
		blob.data = nil
	}
}

func (m *Memory) Delete(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	s, err := m.lock(ctx)
	if err != nil {
		return err
	}
	defer s.life.RUnlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	if blob := s.files[name]; blob != nil {
		delete(s.files, name)
		blob.published = false
		s.release(blob)
		return nil
	}
	if s.directory(name) {
		return &Error{Code: "not_regular"}
	}
	return nil
}

// Close drops the namespace after active operations finish. Already opened
// readers keep their own content until closed, and all new operations fail.
func (m *Memory) Close() error {
	if m == nil || m.state == nil {
		return nil
	}
	s := m.state
	s.life.Lock()
	defer s.life.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, blob := range s.files {
		blob.published = false
		s.release(blob)
	}
	s.files = nil
	return nil
}

type memoryContentReader struct {
	reader *bytes.Reader
	owner  *memoryState
	blob   *memoryBlob
}

func (r *memoryContentReader) Read(destination []byte) (int, error) {
	return r.reader.Read(destination)
}
func (r *memoryContentReader) Close() error {
	// The outer fileReader serializes read/close and invokes this only once.
	r.reader.Reset(nil)
	r.owner.mu.Lock()
	r.blob.readers--
	r.owner.release(r.blob)
	r.owner.mu.Unlock()
	r.owner, r.blob = nil, nil
	return nil
}
