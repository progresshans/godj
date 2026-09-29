package storage

import (
	"errors"
	"io"
	"strings"
	"testing"
)

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
