package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type publicationEntropy func([]byte) (int, error)

func (read publicationEntropy) Read(destination []byte) (int, error) { return read(destination) }

func TestFilesystemEntropyPanicDoesNotPoisonLaterPublication(t *testing.T) {
	for _, collision := range []bool{false, true} {
		name := "stage"
		if collision {
			name = "collision"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			calls := 0
			panicAt := 1
			if collision {
				panicAt = 3
			} // first file, second staging, collision name
			sentinel := errors.New("private entropy panic")
			backend, err := OpenFilesystem(t.Context(), FilesystemConfig{Directory: root, Random: publicationEntropy(func(destination []byte) (int, error) {
				calls++
				if calls == panicAt {
					panic(sentinel)
				}
				for i := range destination {
					destination[i] = byte(calls)
				}
				return len(destination), nil
			})})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			if collision {
				if _, err := backend.Save(t.Context(), "report.bin", strings.NewReader("previous"), SaveOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			var recovered any
			func() {
				defer func() { recovered = recover() }()
				_, _ = backend.Save(t.Context(), "report.bin", strings.NewReader("failed content"), SaveOptions{})
			}()
			if recovered != sentinel {
				t.Fatal("entropy panic changed or was swallowed", recovered)
			}
			// Fail immediately instead of hanging the test/cleanup on a poisoned
			// mutex; successful follow-up I/O below verifies the public behavior.
			if !backend.state.entropyMu.TryLock() {
				t.Fatal("entropy panic retained serialization lock")
			}
			backend.state.entropyMu.Unlock()
			entries, err := os.ReadDir(filepath.Join(root, stagingDirectory))
			if err != nil || len(entries) != 0 {
				t.Fatal("entropy panic left staged content", err)
			}
			info, err := backend.Save(t.Context(), "report.bin", strings.NewReader("recovered"), SaveOptions{})
			if err != nil {
				t.Fatal("later publication failed after recovered panic", err)
			}
			reader, err := backend.Open(t.Context(), info.Name())
			if err != nil {
				t.Fatal(err)
			}
			body, readErr := io.ReadAll(reader)
			closeErr := reader.Close()
			if readErr != nil || closeErr != nil || string(body) != "recovered" {
				t.Fatal("later publication lost content", readErr, closeErr)
			}
			if collision {
				old, err := os.ReadFile(filepath.Join(root, "report.bin"))
				if err != nil || string(old) != "previous" || info.Name() == "report.bin" {
					t.Fatal("panic/retry replaced earlier publication", err)
				}
			}
		})
	}
}

type failingRoot struct {
	rootOperations
	failure   error
	afterLink bool
}

func (r failingRoot) Link(oldName, newName string) error {
	if err := r.rootOperations.Link(oldName, newName); err != nil {
		return err
	}
	if r.afterLink {
		return r.failure
	}
	return nil
}
func (r failingRoot) Remove(name string) error {
	if !r.afterLink {
		return r.failure
	}
	return r.rootOperations.Remove(name)
}
func TestFilesystemPublicationFailureKeepsKnownAndUncertainOutcomes(t *testing.T) {
	for _, lostAck := range []bool{false, true} {
		name := "cleanup failure"
		if lostAck {
			name = "lost publication acknowledgement"
		}
		t.Run(name, func(t *testing.T) {
			backend, err := OpenFilesystem(t.Context(), FilesystemConfig{Directory: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := backend.Close(); err != nil {
					t.Error(err)
				}
			})
			if _, err = backend.Save(t.Context(), "report", strings.NewReader("old"), SaveOptions{}); err != nil {
				t.Fatal(err)
			}
			original := backend.state.root
			sentinel := errors.New("injected filesystem failure")
			backend.state.root = failingRoot{rootOperations: original, failure: sentinel, afterLink: lostAck}
			result, err := backend.Save(t.Context(), "report", strings.NewReader("new"), SaveOptions{})
			backend.state.root = original
			expected := Published
			code := "cleanup_failed"
			if lostAck {
				expected = Uncertain
				code = "publish_failed"
			}
			var failure *Error
			if !result.Valid() || !errors.As(err, &failure) || failure.Outcome != expected || failure.Code != code || !errors.Is(err, sentinel) {
				t.Fatal("publication state lost", result, err)
			}
			// An uncertain result can already exist. Treating it as not written
			// and retrying or deleting automatically would lose this state.
			for stored, expected := range map[string]string{"report": "old", result.Name(): "new"} {
				reader, err := backend.Open(t.Context(), stored)
				if err != nil {
					t.Fatal(err)
				}
				content, err := io.ReadAll(reader)
				closeErr := reader.Close()
				if err != nil || closeErr != nil || string(content) != expected {
					t.Fatal("publication content changed", err, closeErr)
				}
			}
		})
	}
}
