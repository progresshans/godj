//go:build darwin || linux

package projectcheck

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

const descriptorName = "godj.toml"

type retainedProject struct {
	root           *os.File
	rootPath       string
	rootIdentity   unix.Stat_t
	descriptorName string
	descriptorStat unix.Stat_t
	descriptor     projectDescriptor
}

type selectionHooks struct {
	afterDirectoryScan  func(string)
	afterDescriptorStat func(int, string)
	afterDescriptorRead func(int, string)
}

func (project *retainedProject) close() error {
	if project == nil || project.root == nil {
		return nil
	}
	err := project.root.Close()
	project.root = nil
	return err
}

func selectProject(cwd string, arguments commandArguments, report *Report) (retainedProject, *Failure) {
	return selectProjectWithHooks(cwd, arguments, report, selectionHooks{})
}

func selectProjectWithHooks(cwd string, arguments commandArguments, report *Report, hooks selectionHooks) (retainedProject, *Failure) {
	physicalCWD, err := physicalDirectory(cwd)
	if err != nil {
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	if arguments.explicitDescriptor != "" {
		return selectExplicitProject(physicalCWD, arguments.explicitDescriptor, report, hooks)
	}
	return selectImplicitProject(physicalCWD, report, hooks)
}

func selectImplicitProject(cwd string, report *Report, hooks selectionHooks) (retainedProject, *Failure) {
	current, currentStat, err := openRetainedDirectory(cwd)
	if err != nil {
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	currentPath := cwd
	for inspected := 1; inspected <= maxAncestors; inspected++ {
		report.AncestorDirectoriesInspected++
		found, listErr := directoryContainsExact(current, descriptorName)
		if listErr != nil {
			_ = current.Close()
			primary := failure("migration_project_selection_error", "project_selection_failed")
			return retainedProject{}, &primary
		}
		if hooks.afterDirectoryScan != nil {
			hooks.afterDirectoryScan(currentPath)
		}
		if !verifyRetainedDirectory(currentPath, current, currentStat) {
			_ = current.Close()
			primary := failure("migration_project_selection_error", "project_selection_failed")
			return retainedProject{}, &primary
		}
		if found {
			return readSelectedDescriptor(current, currentPath, currentStat, false, report, hooks)
		}

		parent, parentStat, openErr := openDirectoryAt(current, "..")
		if openErr != nil {
			_ = current.Close()
			primary := failure("migration_project_selection_error", "project_selection_failed")
			return retainedProject{}, &primary
		}
		if sameIdentity(currentStat, parentStat) {
			_ = parent.Close()
			_ = current.Close()
			primary := failure("migration_project_selection_error", "project_not_found")
			return retainedProject{}, &primary
		}
		if inspected == maxAncestors {
			_ = parent.Close()
			_ = current.Close()
			primary := failure("migration_project_selection_error", "project_search_limit_exceeded")
			return retainedProject{}, &primary
		}
		_ = current.Close()
		current = parent
		currentStat = parentStat
		currentPath = filepath.Dir(currentPath)
	}
	_ = current.Close()
	primary := failure("migration_project_internal_error", "project_internal_error")
	return retainedProject{}, &primary
}

func selectExplicitProject(cwd, supplied string, report *Report, hooks selectionHooks) (retainedProject, *Failure) {
	if filepath.Base(supplied) != descriptorName {
		primary := failure("migration_project_selection_error", "invalid_project_descriptor")
		return retainedProject{}, &primary
	}
	resolved := supplied
	if !filepath.IsAbs(resolved) {
		resolved = filepath.Join(cwd, resolved)
	}
	resolved = filepath.Clean(resolved)
	parentInput := filepath.Dir(resolved)
	parentPath, classification, err := physicalExplicitParent(parentInput)
	if err != nil {
		primary := failure("migration_project_selection_error", classification)
		return retainedProject{}, &primary
	}
	parent, parentStat, err := openRetainedDirectory(parentPath)
	if err != nil {
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	if hooks.afterDirectoryScan != nil {
		hooks.afterDirectoryScan(parentPath)
	}
	found, err := directoryContainsExact(parent, descriptorName)
	if err != nil {
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	if !verifyRetainedDirectory(parentPath, parent, parentStat) {
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	if !found {
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "invalid_project_descriptor")
		return retainedProject{}, &primary
	}
	return readSelectedDescriptor(parent, parentPath, parentStat, true, report, hooks)
}

func readSelectedDescriptor(parent *os.File, rootPath string, rootStat unix.Stat_t, explicit bool, report *Report, hooks selectionHooks) (retainedProject, *Failure) {
	var initial unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), descriptorName, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		_ = parent.Close()
		code := "project_selection_failed"
		if explicit && errors.Is(err, syscall.ENOENT) {
			code = "invalid_project_descriptor"
		}
		primary := failure("migration_project_selection_error", code)
		return retainedProject{}, &primary
	}
	if initial.Mode&unix.S_IFMT != unix.S_IFREG {
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "invalid_project_descriptor")
		return retainedProject{}, &primary
	}
	if hooks.afterDescriptorStat != nil {
		hooks.afterDescriptorStat(int(parent.Fd()), descriptorName)
	}

	fd, err := unix.Openat(int(parent.Fd()), descriptorName, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	file := os.NewFile(uintptr(fd), descriptorName)
	if file == nil {
		_ = unix.Close(fd)
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	var opened unix.Stat_t
	if err := unix.Fstat(fd, &opened); err != nil || opened.Mode&unix.S_IFMT != unix.S_IFREG || !sameIdentity(initial, opened) {
		_ = file.Close()
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	document, readErr := io.ReadAll(io.LimitReader(file, maxDescriptorBytes+1))
	closeErr := file.Close()
	if hooks.afterDescriptorRead != nil {
		hooks.afterDescriptorRead(int(parent.Fd()), descriptorName)
	}
	var after unix.Stat_t
	postErr := unix.Fstatat(int(parent.Fd()), descriptorName, &after, unix.AT_SYMLINK_NOFOLLOW)
	if readErr != nil || closeErr != nil || postErr != nil || after.Mode&unix.S_IFMT != unix.S_IFREG || !sameIdentity(opened, after) {
		_ = parent.Close()
		primary := failure("migration_project_selection_error", "project_selection_failed")
		return retainedProject{}, &primary
	}
	report.DescriptorReads++
	descriptor, primary := parseProjectDescriptor(document)
	clear(document)
	if primary != nil {
		_ = parent.Close()
		return retainedProject{}, primary
	}
	return retainedProject{
		root:           parent,
		rootPath:       rootPath,
		rootIdentity:   rootStat,
		descriptorName: descriptorName,
		descriptorStat: after,
		descriptor:     descriptor,
	}, nil
}

func verifyRetainedProject(project retainedProject) bool {
	if project.root == nil {
		return false
	}
	var retained unix.Stat_t
	if err := unix.Fstat(int(project.root.Fd()), &retained); err != nil || !sameIdentity(retained, project.rootIdentity) || retained.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false
	}
	var pathStat unix.Stat_t
	if err := unix.Lstat(project.rootPath, &pathStat); err != nil || pathStat.Mode&unix.S_IFMT != unix.S_IFDIR || !sameIdentity(pathStat, retained) {
		return false
	}
	var descriptorStat unix.Stat_t
	return unix.Fstatat(int(project.root.Fd()), project.descriptorName, &descriptorStat, unix.AT_SYMLINK_NOFOLLOW) == nil && descriptorStat.Mode&unix.S_IFMT == unix.S_IFREG && sameIdentity(descriptorStat, project.descriptorStat)
}

func physicalDirectory(candidate string) (string, error) {
	if candidate == "" {
		var err error
		candidate, err = os.Getwd()
		if err != nil {
			return "", err
		}
	}
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(physical, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", fmt.Errorf("project cwd is not a physical directory")
	}
	return filepath.Clean(physical), nil
}

func physicalExplicitParent(candidate string) (string, string, error) {
	if _, err := os.Lstat(candidate); err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return "", "invalid_project_descriptor", err
		}
		return "", "project_selection_failed", err
	}
	physical, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
			return "", "invalid_project_descriptor", err
		}
		return "", "project_selection_failed", err
	}
	var stat unix.Stat_t
	if err := unix.Lstat(physical, &stat); err != nil {
		return "", "project_selection_failed", err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return "", "invalid_project_descriptor", fmt.Errorf("descriptor parent is not a directory")
	}
	return filepath.Clean(physical), "", nil
}

func openRetainedDirectory(path string) (*os.File, unix.Stat_t, error) {
	return openRetainedDirectoryWithHook(path, nil)
}

func openRetainedDirectoryWithHook(path string, afterInitialStat func()) (*os.File, unix.Stat_t, error) {
	var initial unix.Stat_t
	if err := unix.Lstat(path, &initial); err != nil {
		return nil, unix.Stat_t{}, err
	}
	if initial.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, unix.Stat_t{}, fmt.Errorf("path is not a real directory")
	}
	if afterInitialStat != nil {
		afterInitialStat()
	}
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, unix.Stat_t{}, fmt.Errorf("cannot retain directory")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || !sameIdentity(initial, stat) {
		_ = file.Close()
		if err == nil {
			err = fmt.Errorf("retained entry is not a directory")
		}
		return nil, unix.Stat_t{}, err
	}
	return file, stat, nil
}

func openDirectoryAt(parent *os.File, name string) (*os.File, unix.Stat_t, error) {
	var initial unix.Stat_t
	if err := unix.Fstatat(int(parent.Fd()), name, &initial, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		return nil, unix.Stat_t{}, err
	}
	if initial.Mode&unix.S_IFMT != unix.S_IFDIR {
		return nil, unix.Stat_t{}, fmt.Errorf("entry is not a directory")
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, unix.Stat_t{}, err
	}
	file := os.NewFile(uintptr(fd), name)
	if file == nil {
		_ = unix.Close(fd)
		return nil, unix.Stat_t{}, fmt.Errorf("cannot retain parent directory")
	}
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil || stat.Mode&unix.S_IFMT != unix.S_IFDIR || !sameIdentity(initial, stat) {
		_ = file.Close()
		if err == nil {
			err = fmt.Errorf("parent is not a directory")
		}
		return nil, unix.Stat_t{}, err
	}
	return file, stat, nil
}

func directoryContainsExact(directory *os.File, exact string) (bool, error) {
	return scanDirectoryForExact(directory.ReadDir, exact)
}

func scanDirectoryForExact(read func(int) ([]os.DirEntry, error), exact string) (bool, error) {
	for {
		entries, err := read(128)
		if err != nil && err != io.EOF {
			return false, err
		}
		for _, entry := range entries {
			if entry.Name() == exact {
				return true, nil
			}
		}
		if err == io.EOF {
			return false, nil
		}
	}
}

func verifyRetainedDirectory(path string, handle *os.File, expected unix.Stat_t) bool {
	if handle == nil {
		return false
	}
	var retained unix.Stat_t
	if err := unix.Fstat(int(handle.Fd()), &retained); err != nil || retained.Mode&unix.S_IFMT != unix.S_IFDIR || !sameIdentity(retained, expected) {
		return false
	}
	var current unix.Stat_t
	return unix.Lstat(path, &current) == nil && current.Mode&unix.S_IFMT == unix.S_IFDIR && sameIdentity(current, retained)
}

func sameIdentity(left, right unix.Stat_t) bool {
	return left.Dev == right.Dev && left.Ino == right.Ino
}
