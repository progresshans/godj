package storage_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/progresshans/godj/storage"
)

func TestFilesystemPinnedDjangoNameAndContentObservations(t *testing.T) {
	fixture, err := os.ReadFile("testdata/filesystem-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Django string `json:"django"`
		Python string `json:"python"`
		Cases  []struct {
			Proposed  string `json:"proposed"`
			Content   string `json:"content"`
			MaxLength int    `json:"max_length"`
			Saved     string `json:"saved"`
			Size      int64  `json:"size"`
			Exists    bool   `json:"exists"`
			Error     string `json:"error"`
		} `json:"cases"`
	}
	if err = json.Unmarshal(fixture, &reference); err != nil || reference.Django != "6.1" || reference.Python != "3.14.3" || len(reference.Cases) != 7 {
		t.Fatal("invalid native reference", err)
	}
	root := t.TempDir()
	backend := openStorage(t, root, storage.FilesystemConfig{Random: bytes.NewReader(make([]byte, 4096))})
	for _, observation := range reference.Cases {
		t.Run(observation.Proposed, func(t *testing.T) {
			saved, err := backend.Save(t.Context(), observation.Proposed, strings.NewReader(observation.Content), storage.SaveOptions{MaxLength: observation.MaxLength})
			if observation.Error != "" {
				if err == nil || saved.Valid() {
					t.Fatal("expected native rejection", observation.Error)
				}
				return
			}
			if err != nil || saved.Name() != observation.Saved || saved.Size() != observation.Size || !observation.Exists {
				t.Fatal("storage differs from pinned Django", saved.Name(), observation.Saved, err)
			}
			if readStorage(t, backend, saved.Name()) != observation.Content {
				t.Fatal("reference content differs")
			}
		})
	}
	if readStorage(t, backend, "reports/archive.tar.gz") != "original" {
		t.Fatal("reference collision replaced original")
	}
	for range 2 {
		if err = backend.Delete(t.Context(), "empty"); err != nil {
			t.Fatal(err)
		}
	}
	stageEmpty(t, root)
}
