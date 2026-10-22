//go:build darwin || linux

package projectscaffold

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/progresshans/godj/internal/callback"
)

// Result distinguishes publication from later durability/cleanup failures.
// A published project must not be retried as if nothing had been created.
type Result struct {
	Directory string
	Published bool
	Durable   bool
}

// Error exposes only the failed stage. Filesystem paths and dependency
// diagnostics remain available through explicit cause inspection.
type Error struct {
	Code  string
	cause error
}

func (err *Error) Error() string {
	if err == nil {
		return "project scaffold: invalid error"
	}
	return "project scaffold: " + err.Code
}
func (err *Error) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.cause
}
func (err *Error) Is(target error) bool {
	want, ok := target.(*Error)
	return ok && err != nil && want != nil && want.Code == err.Code
}
func (err Error) Format(state fmt.State, _ rune) { fmt.Fprint(state, (&err).Error()) }

type publicationHooks struct {
	beforeCommit func()
	afterCommit  func()
}

// PublishProject prepares a complete new directory next to its destination
// and publishes it with an exclusive rename. An existing destination is never
// adopted or replaced, including an empty directory or a symbolic link.
// prepare must resolve dependencies, generate the actual model bundle and
// compile the runtime in candidate. It owns and joins any processes it starts.
// No database or project runtime is started by this publication owner.
func PublishProject(ctx context.Context, destination string, project Project, prepare func(context.Context, string) error) (Result, error) {
	return publishProject(ctx, destination, project, prepare, publicationHooks{})
}

func publishProject(ctx context.Context, destination string, project Project, prepare func(context.Context, string) error, hooks publicationHooks) (result Result, resultErr error) {
	if ctx == nil || destination == "" || strings.ContainsAny(destination, "\x00\r\n") || prepare == nil {
		return result, &Error{Code: "invalid_configuration"}
	}
	files, err := RenderProject(project)
	if err != nil {
		return result, &Error{Code: "invalid_configuration", cause: err}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	absolute, err := filepath.Abs(destination)
	if err != nil || absolute == filepath.Dir(absolute) {
		return result, &Error{Code: "invalid_destination", cause: err}
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return result, &Error{Code: "destination_parent_unavailable", cause: err}
	}
	name := filepath.Base(absolute)
	result.Directory = filepath.Join(parent, name)
	parentRoot, err := os.OpenRoot(parent)
	if err != nil {
		return result, &Error{Code: "destination_parent_unavailable", cause: err}
	}
	defer func() { resultErr = joinStage(resultErr, "cleanup_failed", parentRoot.Close()) }()
	parentFile, err := parentRoot.Open(".")
	if err != nil {
		return result, &Error{Code: "destination_parent_unavailable", cause: err}
	}
	defer func() { resultErr = joinStage(resultErr, "cleanup_failed", parentFile.Close()) }()
	parentInfo, err := parentFile.Stat()
	if err != nil || !physicalBinding(parent, parentInfo) {
		return result, &Error{Code: "destination_parent_changed", cause: err}
	}
	if _, err := parentRoot.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
		return result, &Error{Code: "destination_exists", cause: err}
	}
	stageName := ".godj-startproject-" + rand.Text()
	if err := parentRoot.Mkdir(stageName, 0o700); err != nil {
		return result, &Error{Code: "staging_failed", cause: err}
	}
	stage, err := parentRoot.OpenRoot(stageName)
	if err != nil {
		// Without a retained stage identity it is unsafe to remove its name.
		return result, &Error{Code: "staging_recovery_required", cause: err}
	}
	stageInfo, err := stage.Stat(".")
	if err != nil {
		_ = stage.Close()
		return result, &Error{Code: "staging_recovery_required", cause: err}
	}
	defer func() {
		if !result.Published {
			resultErr = joinStage(resultErr, "staging_recovery_required", removeOwnedStage(stage, parentRoot, stageName, stageInfo))
		}
		resultErr = joinStage(resultErr, "cleanup_failed", stage.Close())
	}()
	stagePath := filepath.Join(parent, stageName)
	if !physicalBinding(parent, parentInfo) || !childBinding(parentRoot, stageName, stageInfo) {
		return result, &Error{Code: "destination_parent_changed"}
	}
	for _, file := range files {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if err := stage.MkdirAll(path.Dir(file.Path), 0o755); err != nil {
			return result, &Error{Code: "staging_failed", cause: err}
		}
		output, err := stage.OpenFile(file.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, file.Mode)
		if err != nil {
			return result, &Error{Code: "staging_failed", cause: err}
		}
		_, writeErr := output.Write(file.Data)
		if err := errors.Join(writeErr, output.Close()); err != nil {
			return result, &Error{Code: "staging_failed", cause: err}
		}
	}
	_, err = callback.Invoke(func() (struct{}, error) { return struct{}{}, prepare(ctx, stagePath) }, &Error{Code: "preparation_aborted"})
	if err != nil {
		return result, &Error{Code: "preparation_failed", cause: err}
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if err := syncPreparedTree(ctx, stage); err != nil {
		return result, &Error{Code: "candidate_invalid", cause: err}
	}
	if hooks.beforeCommit != nil {
		hooks.beforeCommit()
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if !physicalBinding(parent, parentInfo) || !childBinding(parentRoot, stageName, stageInfo) {
		return result, &Error{Code: "destination_parent_changed"}
	}
	if err := renameProjectNoReplace(int(parentFile.Fd()), stageName, name); err != nil {
		return result, &Error{Code: "publication_conflict", cause: err}
	}
	// Publication has happened. Never remove this tree in a failure defer or
	// reinterpret a late cancellation as an uncommitted operation.
	result.Published = true
	if err := parentFile.Sync(); err != nil {
		return result, &Error{Code: "publication_durability_unknown", cause: err}
	}
	result.Durable = true
	if hooks.afterCommit != nil {
		hooks.afterCommit()
	}
	if !physicalBinding(parent, parentInfo) || !childBinding(parentRoot, name, stageInfo) {
		return result, &Error{Code: "publication_location_changed"}
	}
	return result, nil
}

func joinStage(primary error, code string, cause error) error {
	if cause == nil {
		return primary
	}
	return errors.Join(primary, &Error{Code: code, cause: cause})
}

func physicalBinding(name string, expected fs.FileInfo) bool {
	if expected == nil || !expected.IsDir() {
		return false
	}
	physical, err := filepath.EvalSymlinks(name)
	if err != nil || physical != name {
		return false
	}
	current, err := os.Lstat(name)
	return err == nil && current.IsDir() && os.SameFile(expected, current)
}

func childBinding(parent *os.Root, name string, expected fs.FileInfo) bool {
	current, err := parent.Lstat(name)
	return err == nil && current.IsDir() && os.SameFile(expected, current)
}

func removeOwnedStage(stage, parent *os.Root, name string, expected fs.FileInfo) error {
	// The retained Root keeps cleanup inside our original private directory,
	// even if the public parent path or staging name has been replaced.
	directory, err := stage.Open(".")
	if err != nil {
		return err
	}
	entries, readErr := directory.ReadDir(-1)
	if err := errors.Join(readErr, directory.Close()); err != nil {
		return err
	}
	for _, entry := range entries {
		if err := stage.RemoveAll(entry.Name()); err != nil {
			return err
		}
	}
	if !childBinding(parent, name, expected) {
		return errors.New("owned stage name changed")
	}
	return parent.Remove(name)
}

func syncPreparedTree(ctx context.Context, root *os.Root) error {
	var directories []string
	var files int
	var bytes int64
	err := fs.WalkDir(root.FS(), ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			directories = append(directories, name)
			if len(directories) > 4096 {
				return errors.New("candidate has too many directories")
			}
			return nil
		}
		before, err := root.Lstat(name)
		if err != nil || !before.Mode().IsRegular() {
			return errors.New("candidate contains a nonregular file")
		}
		files++
		bytes += before.Size()
		if files > 8192 || bytes > 128<<20 {
			return errors.New("candidate exceeds source limits")
		}
		file, err := root.OpenFile(name, os.O_RDWR, 0)
		if err != nil {
			return err
		}
		after, statErr := file.Stat()
		if statErr != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
			_ = file.Close()
			return errors.New("candidate file changed")
		}
		return errors.Join(file.Sync(), file.Close())
	})
	if err != nil {
		return err
	}
	sort.Slice(directories, func(i, j int) bool { return len(directories[i]) > len(directories[j]) })
	for _, name := range directories {
		directory, err := root.Open(name)
		if err != nil {
			return err
		}
		if err := errors.Join(directory.Sync(), directory.Close()); err != nil {
			return err
		}
	}
	return nil
}
