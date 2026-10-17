package storage_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/progresshans/godj/storage"
)

type aliasBackend struct {
	storage.Backend
	calls atomic.Int64
}

func (b *aliasBackend) Open(context.Context, string) (io.ReadCloser, error) {
	b.calls.Add(1)
	return nil, errors.New("unexpected I/O")
}
func (b *aliasBackend) Stat(context.Context, string) (storage.Info, error) {
	b.calls.Add(1)
	return storage.Info{}, errors.New("unexpected I/O")
}

type nilStorageContext struct{ context.Context }

func TestStorageAliasesOwnConfigurationAndBorrowCapabilities(t *testing.T) {
	first, second := &aliasBackend{}, &aliasBackend{}
	prefix, err := storage.NewURLPrefix("https://cdn.example.test/media")
	if err != nil {
		t.Fatal(err)
	}
	registrations := []storage.Registration{{Alias: storage.DefaultAlias, Backend: first, URL: prefix}, {Alias: "private", Backend: second}}
	registry, err := storage.NewRegistry(registrations...)
	if err != nil {
		t.Fatal(err)
	}
	registrations[0].Alias, registrations[0].Backend = "changed", second
	aliases := registry.Aliases()
	aliases[0] = "changed"
	if !reflect.DeepEqual(registry.Aliases(), []string{"default", "private"}) {
		t.Fatal("alias order leaked mutable input")
	}
	for i := 0; i < 2; i++ {
		value, err := registry.Default()
		if err != nil || value != first {
			t.Fatal("default alias lost backend identity", err)
		}
	}
	if value, err := registry.Lookup("private"); err != nil || value != second {
		t.Fatal("explicit alias identity", err)
	}
	if _, err := registry.Lookup("missing"); !errors.Is(err, &storage.Error{Code: "unknown_alias"}) {
		t.Fatal("unknown alias fell back", err)
	}
	if _, err := registry.URL(t.Context(), "private", "private.txt"); !errors.Is(err, &storage.Error{Code: "url_unavailable"}) {
		t.Fatal("private alias exposed a URL", err)
	}
	value, err := registry.URL(t.Context(), "default", "docs/a+b%.txt")
	if err != nil || value != "https://cdn.example.test/media/docs/a%2Bb%25.txt" || first.calls.Load() != 0 || second.calls.Load() != 0 {
		t.Fatal("URL performed hidden storage I/O or changed name", value, err)
	}
	other, err := storage.NewRegistry(storage.Registration{Alias: "private", Backend: first})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Default(); !errors.Is(err, &storage.Error{Code: "unknown_alias"}) {
		t.Fatal("registry supplied an implicit default", err)
	}
	for _, v := range []any{registry, registrations[1], prefix, storage.URLFunc(func(context.Context, string) (string, error) { return "", nil })} {
		for _, verb := range []string{"%v", "%+v", "%#v"} {
			text := fmt.Sprintf(verb, v)
			if strings.Contains(text, "cdn.example") || strings.Contains(text, "private") {
				t.Fatal("storage configuration formatted private content", text)
			}
		}
	}
}

func TestStorageAliasAndURLConfigurationFailuresBeforeCallbacks(t *testing.T) {
	backend := &aliasBackend{}
	for _, alias := range []string{"", ".", "..", "../private", "has space", "has\nline", "비공개", strings.Repeat("a", 65)} {
		if _, err := storage.NewRegistry(storage.Registration{Alias: alias, Backend: backend}); !errors.Is(err, &storage.Error{Code: "invalid_alias"}) {
			t.Fatal("invalid alias accepted", err)
		}
	}
	if _, err := storage.NewRegistry(storage.Registration{Alias: "same", Backend: backend}, storage.Registration{Alias: "same", Backend: backend}); !errors.Is(err, &storage.Error{Code: "duplicate_alias"}) {
		t.Fatal(err)
	}
	for _, b := range []storage.Backend{nil, (*aliasBackend)(nil)} {
		if _, err := storage.NewRegistry(storage.Registration{Alias: "default", Backend: b}); !errors.Is(err, &storage.Error{Code: "invalid_backend"}) {
			t.Fatal("nil backend accepted", err)
		}
	}
	if _, err := storage.NewRegistry(storage.Registration{Alias: "default", Backend: backend, URL: storage.URLFunc(nil)}); !errors.Is(err, &storage.Error{Code: "invalid_url_resolver"}) {
		t.Fatal("typed nil URL accepted", err)
	}
	calls := 0
	provider := storage.URLFunc(func(ctx context.Context, name string) (string, error) { calls++; return "/media/" + name, nil })
	registry, err := storage.NewRegistry(storage.Registration{Alias: "default", Backend: backend, URL: provider})
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	var typedNil *nilStorageContext
	for _, ctx := range []context.Context{nil, typedNil, canceled} {
		if _, err := registry.URL(ctx, "default", "a.txt"); err == nil {
			t.Fatal("invalid context accepted")
		}
	}
	for _, name := range []string{"", "../escape", "/absolute", "folder\\file", ".godj-staging/private"} {
		if _, err := registry.URL(t.Context(), "default", name); err == nil {
			t.Fatal("invalid name accepted")
		}
	}
	if calls != 0 || backend.calls.Load() != 0 {
		t.Fatal("configuration failure reached capability")
	}
	if _, err := (storage.URLFunc(nil)).URL(t.Context(), "a.txt"); err == nil {
		t.Fatal("nil URL callback accepted")
	}
}

func TestStorageURLPrefixesAgainstPinnedDjango(t *testing.T) {
	raw, err := os.ReadFile("testdata/serving-django61.json")
	if err != nil {
		t.Fatal(err)
	}
	var observed struct {
		Django, Python string
		URLs           []struct{ Base, Name, URL string } `json:"urls"`
		Unsupported    []struct{ Name string }            `json:"unsupported_names"`
		Reads          int                                `json:"url_storage_reads"`
		Aliases        struct {
			Same      bool   `json:"same_alias_identity"`
			Different bool   `json:"different_alias_identity"`
			Missing   string `json:"missing_error"`
		}
	}
	if err := json.Unmarshal(raw, &observed); err != nil {
		t.Fatal(err)
	}
	if observed.Django != "6.1" || observed.Python != "3.14.3" || len(observed.URLs) != 12 || observed.Reads != 0 || !observed.Aliases.Same || !observed.Aliases.Different || observed.Aliases.Missing != "InvalidStorageError" {
		t.Fatal("native serving fixture changed")
	}
	for _, native := range observed.URLs {
		prefix, err := storage.NewURLPrefix(native.Base)
		if err != nil {
			t.Fatal(err)
		}
		value, err := prefix.URL(t.Context(), native.Name)
		if err != nil || value != native.URL {
			t.Fatal("native file URL differs", native.Name, value, native.URL, err)
		}
	}
	prefix, err := storage.NewURLPrefix("/media/")
	if err != nil {
		t.Fatal(err)
	}
	if len(observed.Unsupported) != 4 {
		t.Fatal("missing native URL deviations")
	}
	for _, native := range observed.Unsupported {
		if _, err := prefix.URL(t.Context(), native.Name); !errors.Is(err, &storage.Error{Code: "invalid_name"}) {
			t.Fatal("unsafe native URL name was adopted", native.Name, err)
		}
	}
}

func TestStorageURLsRejectUnsafeTargetsAndRespectCancellation(t *testing.T) {
	for _, base := range []string{"", "relative/", "//outside.test/", "javascript:run()", "data:text/html,content", "https://name:secret@host.test/", "https://host.test/#fragment", "https://host.test/#", "https://host.test/?query=1", "https://host.test/?", "/media/../other/", "/media//nested/", "https://host.test/line\nbreak", "https://host.test\\outside/", "https://host.test/%ZZ"} {
		if _, err := storage.NewURLPrefix(base); err == nil {
			t.Fatal("unsafe base URL accepted", base)
		}
	}
	for _, value := range []string{"javascript:run()", "//outside.test/", "relative", "https://name:secret@host.test/file", "https://host.test/file#fragment", "https://host.test/file\r\nX-Test: injected", "https://host.test/file?sig=%ZZ"} {
		registry, err := storage.NewRegistry(storage.Registration{Alias: "default", Backend: &aliasBackend{}, URL: storage.URLFunc(func(context.Context, string) (string, error) { return value, nil })})
		if err != nil {
			t.Fatal(err)
		}
		if got, err := registry.URL(t.Context(), "default", "valid.txt"); got != "" || !errors.Is(err, &storage.Error{Code: "invalid_url"}) {
			t.Fatal("unsafe URL provider output accepted", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	failure := errors.New("URL provider failed")
	for _, failed := range []bool{true, false} {
		registry, err := storage.NewRegistry(storage.Registration{Alias: "default", Backend: &aliasBackend{}, URL: storage.URLFunc(func(received context.Context, name string) (string, error) {
			if received != ctx || name != "valid.txt" {
				t.Fatal("provider lost context/name")
			}
			if failed {
				return "https://host.test/file?secret=discard", failure
			}
			cancel()
			return "https://host.test/file?secret=discard", nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		got, err := registry.URL(ctx, "default", "valid.txt")
		if got != "" || err == nil || failed && err != failure || !failed && !errors.Is(err, context.Canceled) {
			t.Fatal("failed URL was exposed or outcome changed", err)
		}
	}
}

func TestStorageRegistryConcurrentSnapshotsAndOpenedMetadata(t *testing.T) {
	root, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := root.Close(); err != nil {
			t.Error(err)
		}
	}()
	prefix, err := storage.NewURLPrefix("/media/")
	if err != nil {
		t.Fatal(err)
	}
	registry, err := storage.NewRegistry(storage.Registration{Alias: "default", Backend: root, URL: prefix})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Go(func() {
			for n := 0; n < 20; n++ {
				backend, err := registry.Default()
				if err != nil || backend != root {
					t.Error("lost alias", err)
					return
				}
				uri, err := registry.URL(t.Context(), "default", "docs/private%.txt")
				if err != nil || uri != "/media/docs/private%25.txt" {
					t.Error("shared URL mutation", uri, err)
					return
				}
				registry.Aliases()[0] = "changed"
			}
		})
	}
	group.Wait()
	if _, err := root.Save(t.Context(), "same.txt", strings.NewReader("old"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	old, err := root.Open(t.Context(), "same.txt")
	if err != nil {
		t.Fatal(err)
	}
	reader, ok := old.(storage.Reader)
	if !ok {
		t.Fatal("filesystem omitted opened metadata")
	}
	if err := root.Delete(t.Context(), "same.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Save(t.Context(), "same.txt", strings.NewReader("replacement"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	if info := reader.Info(); info.Name() != "same.txt" || info.Size() != 3 {
		t.Fatal("metadata followed replacement instead of handle")
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(reader)
	closed := reader.Close()
	if err != nil || closed != nil || string(payload) != "old" || reader.Info().Size() != 3 {
		t.Fatal("owned reader/metadata lost after backend close", err, closed)
	}
	if _, err := registry.Default(); err != nil {
		t.Fatal("closing backend erased alias snapshot", err)
	}
	if _, err := root.Open(t.Context(), "same.txt"); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("alias extended backend lifetime", err)
	}
}
