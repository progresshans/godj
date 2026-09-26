package compiletest

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"

	facade "github.com/progresshans/godj/conformance/relationfixture/project"
)

// Runtime facade behavior retains normal, race and CGO-disabled owners. The
// external ABI and source architecture checks are owned by the !race suite.
func TestRelationFacadeDirectJSONDoesNotExposeOrMutateModels(t *testing.T) {
	post := facade.BlogPost{}
	post.Title = "private post"
	author := facade.AuthorsAuthor{}
	author.Name = "private author"
	for _, value := range []any{post, &post, author, &author} {
		if document, err := json.Marshal(value); err == nil || len(document) != 0 {
			t.Fatalf("direct JSON marshal of %T = %q, %v", value, document, err)
		}
	}
	if err := json.Unmarshal([]byte(`{"Title":"changed"}`), &post); err == nil || post.Title != "private post" {
		t.Fatalf("direct JSON unmarshal changed post: title=%q error=%v", post.Title, err)
	}
	if err := json.Unmarshal([]byte(`{"Name":"changed"}`), &author); err == nil || author.Name != "private author" {
		t.Fatalf("direct JSON unmarshal changed author: name=%q error=%v", author.Name, err)
	}
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve compile test source path")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}
