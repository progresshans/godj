package storage

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"runtime"
	"sync"
	"unicode/utf8"
)

type FilesystemConfig struct {
	// Directory must already exist and be controlled by the application. Root
	// confinement is not a sandbox against privileged writers or bind mounts.
	Directory string
	Limits    Limits
	// Random is borrowed and serialized by this instance. nil uses crypto/rand.
	Random io.Reader
}

type Filesystem struct{ state *filesystemState }

// A narrow OS port keeps outcome fault tests independent of process privilege.
// Production always uses the traversal-resistant os.Root implementation.
type rootOperations interface {
	Close() error
	OpenFile(string, int, fs.FileMode) (*os.File, error)
	Open(string) (*os.File, error)
	Lstat(string) (fs.FileInfo, error)
	MkdirAll(string, fs.FileMode) error
	Link(string, string) error
	Remove(string) error
}

type filesystemState struct {
	mu        sync.RWMutex
	root      rootOperations
	limits    Limits
	random    io.Reader
	entropyMu sync.Mutex
	closed    bool
	closeErr  error
}

// OpenFilesystem acquires a root directory handle and a private staging
// namespace. Close waits for current operations. Readers already returned by
// Open remain owned by their callers and must be closed separately.
func OpenFilesystem(ctx context.Context, config FilesystemConfig) (_ *Filesystem, err error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if runtime.GOOS == "js" || runtime.GOOS == "plan9" {
		return nil, &Error{Code: "unsupported_platform", Outcome: NotPublished}
	}
	limits, err := normalizeLimits(config.Limits)
	if err != nil {
		return nil, err
	}
	if config.Directory == "" {
		return nil, &Error{Code: "invalid_directory", Outcome: NotPublished}
	}
	root, err := os.OpenRoot(config.Directory)
	if err != nil {
		return nil, &Error{Code: "open_failed", Outcome: NotPublished, Cause: err}
	}
	failed := true
	defer func() {
		if failed {
			if closeErr := root.Close(); closeErr != nil {
				err = &Error{Code: "cleanup_failed", Outcome: NotPublished, Cause: errors.Join(err, closeErr)}
			}
		}
	}()
	if err = root.Mkdir(stagingDirectory, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, &Error{Code: "stage_failed", Outcome: NotPublished, Cause: err}
	}
	info, err := root.Lstat(stagingDirectory)
	if err != nil || !info.IsDir() {
		return nil, &Error{Code: "invalid_staging_directory", Outcome: NotPublished, Cause: err}
	}
	if err = contextError(ctx); err != nil {
		return nil, err
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	failed = false
	return &Filesystem{state: &filesystemState{root: root, limits: limits, random: random}}, nil
}
func (Filesystem) Format(s fmt.State, _ rune) { fmt.Fprint(s, "storage.Filesystem{redacted}") }

func (f *Filesystem) lock(ctx context.Context) (*filesystemState, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if f == nil || f.state == nil {
		return nil, &Error{Code: "closed", Outcome: NotPublished}
	}
	s := f.state
	s.mu.RLock()
	if s.closed {
		s.mu.RUnlock()
		return nil, &Error{Code: "closed", Outcome: NotPublished}
	}
	if err := contextError(ctx); err != nil {
		s.mu.RUnlock()
		return nil, err
	}
	return s, nil
}
func (f *Filesystem) Close() error {
	if f == nil || f.state == nil {
		return nil
	}
	s := f.state
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		if err := s.root.Close(); err != nil {
			s.closeErr = &Error{Code: "close_failed", Cause: err}
		}
	}
	return s.closeErr
}
func (s *filesystemState) stage() (*os.File, string, error) {
	for i := 0; i < s.limits.MaxAttempts; i++ {
		var entropy [16]byte
		s.entropyMu.Lock()
		_, err := io.ReadFull(s.random, entropy[:])
		s.entropyMu.Unlock()
		if err != nil {
			return nil, "", &Error{Code: "entropy_failed", Outcome: NotPublished, Cause: err}
		}
		name := stagingDirectory + "/" + hex.EncodeToString(entropy[:])
		file, err := s.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", &Error{Code: "stage_failed", Outcome: NotPublished, Cause: err}
		}
		return file, name, nil
	}
	return nil, "", &Error{Code: "collision_limit", Outcome: NotPublished}
}

// Save writes and syncs private content before atomically linking a complete
// file into the public namespace. Link never replaces an existing destination,
// including across separate Filesystem instances/processes. No DB transaction
// is implied. File Sync does not promise directory-entry power-loss durability.
func (f *Filesystem) Save(ctx context.Context, name string, source io.Reader, options SaveOptions) (result Info, err error) {
	if err = validateName(name); err != nil {
		return Info{}, err
	}
	s, err := f.lock(ctx)
	if err != nil {
		return Info{}, err
	}
	defer s.mu.RUnlock()
	if source == nil {
		return Info{}, &Error{Code: "invalid_input", Outcome: NotPublished}
	}
	limit, err := nameLimit(s.limits, options)
	if err != nil {
		return Info{}, err
	}
	staged, stagedName, err := s.stage()
	if err != nil {
		return Info{}, err
	}
	closed := false
	outcome := NotPublished
	defer func() {
		var closeErr error
		if !closed {
			closeErr = staged.Close()
		}
		removeErr := s.root.Remove(stagedName)
		if removeErr != nil || closeErr != nil {
			err = &Error{Code: "cleanup_failed", Outcome: outcome, Cause: errors.Join(err, closeErr, removeErr)}
		}
	}()
	size, err := io.Copy(staged, io.LimitReader(contextReader{ctx: ctx, reader: source}, s.limits.MaxFileBytes+1))
	if err != nil {
		return Info{}, &Error{Code: "input_read_failed", Outcome: NotPublished, Cause: err}
	}
	if size > s.limits.MaxFileBytes {
		return Info{}, &Error{Code: "file_too_large", Outcome: NotPublished}
	}
	if err = contextError(ctx); err != nil {
		return Info{}, err
	}
	if err = staged.Sync(); err != nil {
		return Info{}, &Error{Code: "sync_failed", Outcome: NotPublished, Cause: err}
	}
	err = staged.Close()
	closed = true
	if err != nil {
		return Info{}, &Error{Code: "close_failed", Outcome: NotPublished, Cause: err}
	}
	if err = s.root.MkdirAll(path.Dir(name), 0700); err != nil {
		return Info{}, &Error{Code: "directory_failed", Outcome: NotPublished, Cause: err}
	}
	candidate := name
	for attempt := 0; attempt < s.limits.MaxAttempts; attempt++ {
		if err = contextError(ctx); err != nil {
			return Info{}, err
		}
		if attempt != 0 || utf8.RuneCountInString(candidate) > limit {
			s.entropyMu.Lock()
			candidate, err = alternativeName(name, limit, s.random)
			s.entropyMu.Unlock()
			if err != nil {
				return Info{}, err
			}
		}
		if err = contextError(ctx); err != nil {
			return Info{}, err
		}
		err = s.root.Link(stagedName, candidate)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			outcome = Uncertain
			return Info{name: candidate, size: size}, &Error{Code: "publish_failed", Outcome: Uncertain, Cause: err}
		}
		// Cancellation after this point cannot turn a published file into an
		// uncommitted result. Cleanup errors preserve both metadata and outcome.
		outcome = Published
		return Info{name: candidate, size: size}, nil
	}
	return Info{}, &Error{Code: "collision_limit", Outcome: NotPublished}
}

func regularInfo(s *filesystemState, name string) (Info, error) {
	info, err := s.root.Lstat(name)
	if err != nil {
		return Info{}, &Error{Code: "stat_failed", Cause: err}
	}
	if !info.Mode().IsRegular() {
		return Info{}, &Error{Code: "not_regular"}
	}
	return Info{name: name, size: info.Size()}, nil
}
func (f *Filesystem) Stat(ctx context.Context, name string) (Info, error) {
	if err := validateName(name); err != nil {
		return Info{}, err
	}
	s, err := f.lock(ctx)
	if err != nil {
		return Info{}, err
	}
	defer s.mu.RUnlock()
	return regularInfo(s, name)
}
func (f *Filesystem) Open(ctx context.Context, name string) (io.ReadCloser, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	s, err := f.lock(ctx)
	if err != nil {
		return nil, err
	}
	defer s.mu.RUnlock()
	if _, err = regularInfo(s, name); err != nil {
		return nil, err
	}
	file, err := s.root.Open(name)
	if err != nil {
		return nil, &Error{Code: "open_failed", Cause: err}
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		closeErr := file.Close()
		return nil, &Error{Code: "not_regular", Cause: errors.Join(err, closeErr)}
	}
	return &fileReader{state: &fileReaderState{ctx: ctx, reader: file}}, nil
}
func (f *Filesystem) Delete(ctx context.Context, name string) error {
	if err := validateName(name); err != nil {
		return err
	}
	s, err := f.lock(ctx)
	if err != nil {
		return err
	}
	defer s.mu.RUnlock()
	if _, err = regularInfo(s, name); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err = contextError(ctx); err != nil {
		return err
	}
	if err = s.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return &Error{Code: "delete_failed", Cause: err}
	}
	return nil
}
