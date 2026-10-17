package storage_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/storage"
)

func openStorage(t *testing.T, root string, config storage.FilesystemConfig) *storage.Filesystem {
	t.Helper()
	config.Directory = root
	backend, err := storage.OpenFilesystem(t.Context(), config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	return backend
}
func stageEmpty(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, ".godj-staging"))
	if err != nil || len(entries) != 0 {
		t.Fatal("staging leaked", len(entries), err)
	}
}
func readStorage(t *testing.T, backend storage.Backend, name string) string {
	t.Helper()
	reader, err := backend.Open(t.Context(), name)
	if err != nil {
		t.Fatal(err)
	}
	content, readErr := io.ReadAll(reader)
	if err = errors.Join(readErr, reader.Close()); err != nil {
		t.Fatal(err)
	}
	return string(content)
}
func TestFilesystemNamesCollisionPersistenceAndOwnership(t *testing.T) {
	root := t.TempDir()
	backend := openStorage(t, root, storage.FilesystemConfig{})
	first, err := backend.Save(t.Context(), "reports/archive.tar.gz", strings.NewReader("original"), storage.SaveOptions{})
	if err != nil || first.Name() != "reports/archive.tar.gz" || first.Size() != 8 {
		t.Fatal(first, err)
	}
	second, err := backend.Save(t.Context(), first.Name(), strings.NewReader("second"), storage.SaveOptions{MaxLength: 28})
	if err != nil || !regexp.MustCompile(`^reports/archi_[A-Z2-7]{7}\.tar\.gz$`).MatchString(second.Name()) {
		t.Fatal("compound suffix or length", second.Name(), err)
	}
	if readStorage(t, backend, first.Name()) != "original" || readStorage(t, backend, second.Name()) != "second" {
		t.Fatal("collision overwrote content")
	}
	empty, err := backend.Save(t.Context(), "empty", strings.NewReader(""), storage.SaveOptions{})
	if err != nil || empty.Size() != 0 {
		t.Fatal("zero byte storage", err)
	}
	reader, err := backend.Open(t.Context(), first.Name())
	if err != nil {
		t.Fatal(err)
	}
	if err = backend.Close(); err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(reader)
	if err != nil || string(content) != "original" {
		t.Fatal("backend closed caller-owned reader", err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = reader.Read(make([]byte, 1)); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("reader close not enforced", err)
	}
	if _, err = backend.Stat(t.Context(), first.Name()); !errors.Is(err, &storage.Error{Code: "closed"}) {
		t.Fatal("closed backend accepted operation", err)
	}
	reopened := openStorage(t, root, storage.FilesystemConfig{})
	if readStorage(t, reopened, first.Name()) != "original" {
		t.Fatal("file did not persist")
	}
	for range 2 {
		if err = reopened.Delete(t.Context(), second.Name()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = reopened.Stat(t.Context(), second.Name()); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("missing file classification", err)
	}
	if readStorage(t, reopened, first.Name()) != "original" {
		t.Fatal("delete removed another file")
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(filepath.Join(root, first.Name()))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("file permissions", err)
		}
	}
	for _, value := range []any{first, &first, *reopened, reopened, &storage.Error{Code: "private", Cause: errors.New(root)}} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%q"} {
			output := fmt.Sprintf(verb, value)
			if strings.Contains(output, "reports/") || strings.Contains(output, root) {
				t.Fatal("format exposed storage metadata")
			}
		}
	}
	stageEmpty(t, root)
}

type failReader struct {
	err   error
	panic bool
}

func (r failReader) Read([]byte) (int, error) {
	if r.panic {
		panic("reader panic")
	}
	return 0, r.err
}

type stopReader struct {
	read   func()
	reader io.Reader
}

func (r *stopReader) Read(p []byte) (int, error) { r.read(); return r.reader.Read(p) }
func TestFilesystemFailureCancellationPanicAndAtomicVisibility(t *testing.T) {
	for _, kind := range []string{"read", "limit", "cancel", "panic"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			backend := openStorage(t, root, storage.FilesystemConfig{Limits: storage.Limits{MaxFileBytes: 8}})
			sentinel := errors.New("read failure")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var source io.Reader = io.MultiReader(strings.NewReader("part"), failReader{err: sentinel, panic: kind == "panic"})
			if kind == "limit" {
				source = strings.NewReader("123456789")
			}
			if kind == "cancel" {
				source = &stopReader{read: cancel, reader: strings.NewReader("cancel")}
			}
			if kind == "panic" {
				func() {
					defer func() {
						if recover() == nil {
							t.Error("expected input panic")
						}
					}()
					_, _ = backend.Save(ctx, "target", source, storage.SaveOptions{})
				}()
			} else {
				result, err := backend.Save(ctx, "target", source, storage.SaveOptions{})
				var failure *storage.Error
				if result.Valid() || !errors.As(err, &failure) || failure.Outcome != storage.NotPublished {
					t.Fatal("failed input published", result, err)
				}
				if kind == "read" && !errors.Is(err, sentinel) || kind == "cancel" && !errors.Is(err, context.Canceled) {
					t.Fatal("source cause lost", err)
				}
			}
			if _, err := backend.Stat(t.Context(), "target"); !errors.Is(err, fs.ErrNotExist) {
				t.Fatal("partial file visible", err)
			}
			stageEmpty(t, root)
		})
	}
	root := t.TempDir()
	backend := openStorage(t, root, storage.FilesystemConfig{})
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	source := &stopReader{reader: strings.NewReader("complete"), read: func() { once.Do(func() { close(started); <-release }) }}
	done := make(chan error, 1)
	go func() { _, err := backend.Save(t.Context(), "target", source, storage.SaveOptions{}); done <- err }()
	<-started
	if _, err := backend.Stat(t.Context(), "target"); !errors.Is(err, fs.ErrNotExist) {
		t.Error("file visible before source finished", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if readStorage(t, backend, "target") != "complete" {
		t.Fatal("published partial content")
	}
	stageEmpty(t, root)
}
func TestFilesystemRejectsUnsafeNamesAndEscapingLinks(t *testing.T) {
	root := t.TempDir()
	backend := openStorage(t, root, storage.FilesystemConfig{})
	for _, name := range []string{"", ".", "..", "/absolute", "../outside", "nested/../outside", `dir\file`, "C:ads", "a//b", "a/", "a\x00b", "a\nb", "NUL.txt", "dir/COM1.txt", "LPT¹.txt", "trailing.", "trailing ", ".godj-staging/x", ".GODJ-STAGING/x", strings.Repeat("a", 256), "bad\xff"} {
		t.Run(fmt.Sprintf("name_%x", name), func(t *testing.T) {
			if _, err := backend.Save(t.Context(), name, failReader{panic: true}, storage.SaveOptions{}); !errors.Is(err, &storage.Error{Code: "invalid_name"}) {
				t.Fatal("unsafe name accepted", err)
			}
			if _, err := backend.Open(t.Context(), name); !errors.Is(err, &storage.Error{Code: "invalid_name"}) {
				t.Fatal("unsafe read name", err)
			}
			if err := backend.Delete(t.Context(), name); !errors.Is(err, &storage.Error{Code: "invalid_name"}) {
				t.Fatal("unsafe delete name", err)
			}
		})
	}
	outside := t.TempDir()
	sentinel := filepath.Join(outside, "sentinel")
	if err := os.WriteFile(sentinel, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Fatal("symlink fixture unavailable", err)
	}
	for _, name := range []string{"link/sentinel", "link/new"} {
		if _, err := backend.Save(t.Context(), name, strings.NewReader("replace"), storage.SaveOptions{}); err == nil {
			t.Fatal("escaped save")
		}
		if _, err := backend.Open(t.Context(), name); err == nil {
			t.Fatal("escaped open")
		}
		if err := backend.Delete(t.Context(), name); err == nil {
			t.Fatal("escaped delete")
		}
	}
	content, err := os.ReadFile(sentinel)
	if err != nil || string(content) != "keep" {
		t.Fatal("outside file modified", err)
	}
	if _, err = os.Stat(filepath.Join(outside, "new")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("outside file created", err)
	}
	if err = os.Mkdir(filepath.Join(root, "directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err = backend.Delete(t.Context(), "directory"); !errors.Is(err, &storage.Error{Code: "not_regular"}) {
		t.Fatal("storage deleted directory", err)
	}
	stageEmpty(t, root)
}
func TestFilesystemConcurrentInstancesNeverOverwrite(t *testing.T) {
	root := t.TempDir()
	var group sync.WaitGroup
	results := make(chan storage.Info, 8)
	failures := make(chan error, 8)
	for index := range 8 {
		backend := openStorage(t, root, storage.FilesystemConfig{})
		group.Add(1)
		go func() {
			defer group.Done()
			info, err := backend.Save(t.Context(), "shared", strings.NewReader(fmt.Sprint(index)), storage.SaveOptions{})
			results <- info
			failures <- err
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	names, contents := map[string]bool{}, map[string]bool{}
	backend := openStorage(t, root, storage.FilesystemConfig{})
	for info := range results {
		if names[info.Name()] {
			t.Fatal("concurrent names collided")
		}
		names[info.Name()] = true
		contents[readStorage(t, backend, info.Name())] = true
	}
	if len(names) != 8 || len(contents) != 8 {
		t.Fatal("concurrent content lost")
	}
	stageEmpty(t, root)
}
func TestFilesystemProcessWriter(t *testing.T) {
	root := os.Getenv("GODJ_STORAGE_PROCESS_ROOT")
	if root == "" {
		return
	}
	backend := openStorage(t, root, storage.FilesystemConfig{})
	_, err := backend.Save(t.Context(), "shared", strings.NewReader(os.Getenv("GODJ_STORAGE_PROCESS_VALUE")), storage.SaveOptions{})
	if err != nil {
		t.Fatal(err)
	}
}
func TestFilesystemSeparateProcessesNeverOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "shared"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	var commands []*exec.Cmd
	for _, value := range []string{"one", "two"} {
		cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestFilesystemProcessWriter$", "-test.count=1")
		cmd.Env = append(os.Environ(), "GODJ_STORAGE_PROCESS_ROOT="+root, "GODJ_STORAGE_PROCESS_VALUE="+value)
		cmd.Stdout = &bytes.Buffer{}
		cmd.Stderr = cmd.Stdout
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		commands = append(commands, cmd)
	}
	for _, cmd := range commands {
		if err := cmd.Wait(); err != nil {
			t.Fatal("child failed", err, cmd.Stdout)
		}
	}
	backend := openStorage(t, root, storage.FilesystemConfig{})
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	contents := map[string]bool{}
	for _, entry := range entries {
		if !entry.IsDir() {
			contents[readStorage(t, backend, entry.Name())] = true
		}
	}
	if len(contents) != 3 || !contents["original"] || !contents["one"] || !contents["two"] {
		t.Fatal("process publication lost content")
	}
	stageEmpty(t, root)
}

type cancelEntropy struct{ cancel context.CancelFunc }

func (r cancelEntropy) Read(p []byte) (int, error) {
	clear(p)
	if len(p) == 5 {
		r.cancel()
	}
	return len(p), nil
}
func TestFilesystemPolicyEntropyAndLateCancellation(t *testing.T) {
	root := t.TempDir()
	for _, limits := range []storage.Limits{{MaxFileBytes: -1}, {MaxNameLength: 1025}, {MaxAttempts: -1}} {
		if _, err := storage.OpenFilesystem(t.Context(), storage.FilesystemConfig{Directory: root, Limits: limits}); !errors.Is(err, &storage.Error{Code: "invalid_limits"}) {
			t.Fatal("invalid budget accepted", err)
		}
	}
	sentinel := errors.New("entropy unavailable")
	backend := openStorage(t, root, storage.FilesystemConfig{Random: failReader{err: sentinel}})
	if _, err := backend.Save(t.Context(), "file", failReader{panic: true}, storage.SaveOptions{}); !errors.Is(err, sentinel) {
		t.Fatal("entropy failure lost", err)
	}
	bounded := openStorage(t, root, storage.FilesystemConfig{Random: bytes.NewReader(make([]byte, 4096)), Limits: storage.Limits{MaxAttempts: 2}})
	for range 2 {
		if _, err := bounded.Save(t.Context(), "same", strings.NewReader("keep"), storage.SaveOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	result, err := bounded.Save(t.Context(), "same", strings.NewReader("exhausted"), storage.SaveOptions{})
	if result.Valid() || !errors.Is(err, &storage.Error{Code: "collision_limit", Outcome: storage.NotPublished}) {
		t.Fatal("collision retries not bounded", err)
	}
	if _, err := bounded.Save(t.Context(), "cancel-target", strings.NewReader("keep"), storage.SaveOptions{}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	late := openStorage(t, root, storage.FilesystemConfig{Random: cancelEntropy{cancel: cancel}})
	result, err = late.Save(ctx, "cancel-target", strings.NewReader("canceled"), storage.SaveOptions{})
	if result.Valid() || !errors.Is(err, context.Canceled) {
		t.Fatal("canceled entropy step published", err)
	}
	if readStorage(t, late, "cancel-target") != "keep" {
		t.Fatal("cancellation overwrote content")
	}
	stageEmpty(t, root)
}
