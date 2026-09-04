//go:build darwin || linux

package projectcheck

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

var privateEnvironmentKeys = map[string]struct{}{
	"TMPDIR": {}, "GOWORK": {}, "GOTOOLCHAIN": {}, "GOFLAGS": {}, "GOENV": {},
	"GOCACHE": {}, "GOCACHEPROG": {}, "GOMODCACHE": {}, "GOTMPDIR": {}, "HOME": {},
	"XDG_CONFIG_HOME": {}, "XDG_CACHE_HOME": {}, "TEST_TELEMETRY_DIR": {},
}

type privateWorkspace struct {
	root        string
	environment []string
	base        *os.File
	name        string
	cleaned     bool
}

type workspaceHooks struct {
	beforeContainment func()
	afterRootCreated  func(string, *os.File)
}

func createPrivateWorkspaceWithHooks(project retainedProject, ambient []string, report *Report, hooks workspaceHooks) (privateWorkspace, *Failure) {
	environment := environmentValues(ambient)
	if hooks.beforeContainment != nil {
		hooks.beforeContainment()
	}
	protected := make([]unix.Stat_t, 0, 3)
	for _, key := range []string{"HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME"} {
		value := environment[key]
		if key == "HOME" && value == "" {
			primary := failure("migration_project_build_error", "project_temporary_storage_failed")
			return privateWorkspace{}, &primary
		}
		if value == "" {
			continue
		}
		physical, err := resolvedPhysicalDirectory(value)
		if err != nil {
			primary := failure("migration_project_build_error", "project_temporary_storage_failed")
			return privateWorkspace{}, &primary
		}
		var identity unix.Stat_t
		if err := unix.Lstat(physical, &identity); err != nil || identity.Mode&unix.S_IFMT != unix.S_IFDIR {
			primary := failure("migration_project_build_error", "project_temporary_storage_failed")
			return privateWorkspace{}, &primary
		}
		protected = append(protected, identity)
	}
	if project.root == nil || project.rootIdentity.Mode&unix.S_IFMT != unix.S_IFDIR {
		primary := failure("migration_project_build_error", "project_temporary_storage_failed")
		return privateWorkspace{}, &primary
	}
	moduleProxy := ambientWorkspaceModuleProxy(project, environment)
	baseInput := environment["TMPDIR"]
	var physicalBase string
	var err error
	if baseInput == "" {
		physicalBase, err = resolvedPhysicalDirectory("/tmp")
	} else {
		physicalBase, err = configuredPhysicalDirectory(baseInput)
	}
	insideProject, containmentErr := physicalSameOrDescendantIdentity(physicalBase, project.rootIdentity)
	if err != nil || containmentErr != nil || insideProject {
		primary := failure("migration_project_build_error", "project_temporary_storage_failed")
		return privateWorkspace{}, &primary
	}
	for _, root := range protected {
		insideProtected, checkErr := physicalSameOrDescendantIdentity(physicalBase, root)
		if checkErr != nil || insideProtected {
			primary := failure("migration_project_build_error", "project_temporary_storage_failed")
			return privateWorkspace{}, &primary
		}
	}
	base, _, err := openRetainedDirectory(physicalBase)
	if err != nil {
		primary := failure("migration_project_build_error", "project_temporary_storage_failed")
		return privateWorkspace{}, &primary
	}
	root, err := os.MkdirTemp(physicalBase, "godj-projectcheck-")
	if err != nil {
		_ = base.Close()
		primary := failure("migration_project_build_error", "project_temporary_storage_failed")
		return privateWorkspace{}, &primary
	}
	report.TempCreated++
	if hooks.afterRootCreated != nil {
		hooks.afterRootCreated(root, base)
	}
	failed := func() (privateWorkspace, *Failure) {
		partial := privateWorkspace{root: root, base: base, name: filepath.Base(root)}
		report.TempCleanupAttempts++
		if err := partial.cleanup(); err != nil {
			report.CleanupFailed = 1
			report.ResidualTemp = 1
		}
		primary := failure("migration_project_build_error", "project_temporary_storage_failed")
		return privateWorkspace{}, &primary
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return failed()
	}
	physicalRoot, err := configuredPhysicalDirectory(root)
	insideProject, containmentErr = physicalSameOrDescendantIdentity(physicalRoot, project.rootIdentity)
	if err != nil || physicalRoot != filepath.Clean(root) || containmentErr != nil || insideProject {
		return failed()
	}
	for _, protectedRoot := range protected {
		insideProtected, checkErr := physicalSameOrDescendantIdentity(physicalRoot, protectedRoot)
		if checkErr != nil || insideProtected {
			return failed()
		}
	}

	privatePaths := map[string]string{
		"TMPDIR":             "tmp",
		"GOTMPDIR":           "gotmp",
		"GOCACHE":            "gocache",
		"GOMODCACHE":         "gomodcache",
		"HOME":               "home",
		"XDG_CONFIG_HOME":    "xdg-config",
		"XDG_CACHE_HOME":     "xdg-cache",
		"TEST_TELEMETRY_DIR": "telemetry",
	}
	for _, relative := range privatePaths {
		directory := filepath.Join(physicalRoot, relative)
		if err := os.Mkdir(directory, 0o700); err != nil || os.Chmod(directory, 0o700) != nil {
			return failed()
		}
	}
	child := make(map[string]string, len(environment)+len(privatePaths)+5)
	for key, value := range environment {
		if _, isolated := privateEnvironmentKeys[key]; !isolated {
			child[key] = value
		}
	}
	if moduleProxy != "" {
		upstream := environment["GOPROXY"]
		if upstream == "" {
			upstream = "https://proxy.golang.org,direct"
		}
		child["GOPROXY"] = moduleProxy + "," + upstream
	}
	child["GOWORK"] = "off"
	child["GOTOOLCHAIN"] = "local"
	child["GOENV"] = "off"
	child["GOFLAGS"] = "-modcacherw"
	child["GOCACHEPROG"] = ""
	for key, relative := range privatePaths {
		child[key] = filepath.Join(physicalRoot, relative)
	}
	return privateWorkspace{
		root:        physicalRoot,
		environment: sortedEnvironment(child),
		base:        base,
		name:        filepath.Base(physicalRoot),
	}, nil
}

func ambientWorkspaceModuleProxy(project retainedProject, environment map[string]string) string {
	candidates := []string{environment["GOMODCACHE"]}
	if goPath := environment["GOPATH"]; goPath != "" {
		candidates = append(candidates, filepath.Join(strings.Split(goPath, string(os.PathListSeparator))[0], "pkg", "mod"))
	}
	if home := environment["HOME"]; home != "" {
		candidates = append(candidates, filepath.Join(home, "go", "pkg", "mod"))
	}
	for _, candidate := range candidates {
		physicalCache, safe := externalWorkspaceDirectory(candidate, project.rootPath)
		if !safe {
			continue
		}
		physicalDownload, safe := externalWorkspaceDirectory(filepath.Join(physicalCache, "cache", "download"), project.rootPath)
		if !safe {
			continue
		}
		proxy := (&url.URL{Scheme: "file", Path: filepath.ToSlash(physicalDownload)}).String()
		proxy = strings.ReplaceAll(proxy, ",", "%2C")
		proxy = strings.ReplaceAll(proxy, "|", "%7C")
		return proxy
	}
	return ""
}

func externalWorkspaceDirectory(candidate, projectRoot string) (string, bool) {
	if candidate == "" || !filepath.IsAbs(candidate) || projectRoot == "" {
		return "", false
	}
	physicalProject, err := filepath.EvalSymlinks(filepath.Clean(projectRoot))
	if err != nil {
		return "", false
	}
	physicalProject, err = filepath.Abs(physicalProject)
	if err != nil {
		return "", false
	}
	physical, err := filepath.EvalSymlinks(filepath.Clean(candidate))
	if err != nil {
		return "", false
	}
	physical, err = filepath.Abs(physical)
	if err != nil {
		return "", false
	}
	info, err := os.Lstat(physical)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return "", false
	}
	physical = filepath.Clean(physical)
	physicalProject = filepath.Clean(physicalProject)
	if sameOrDescendant(physical, physicalProject) || sameOrDescendant(physicalProject, physical) {
		return "", false
	}
	return physical, true
}

func (workspace *privateWorkspace) cleanup() error {
	if workspace == nil || workspace.cleaned {
		return fmt.Errorf("workspace cleanup invoked more than once")
	}
	workspace.cleaned = true
	removeErr := os.RemoveAll(workspace.root)
	var retained unix.Stat_t
	postErr := unix.Fstatat(int(workspace.base.Fd()), workspace.name, &retained, unix.AT_SYMLINK_NOFOLLOW)
	closeErr := workspace.base.Close()
	workspace.base = nil
	if removeErr != nil {
		return removeErr
	}
	if postErr == nil {
		return fmt.Errorf("private workspace remains")
	}
	if !errors.Is(postErr, syscall.ENOENT) {
		return postErr
	}
	return closeErr
}

func environmentValues(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	for _, entry := range entries {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	return values
}

func sortedEnvironment(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}

func configuredPhysicalDirectory(candidate string) (string, error) {
	return resolveWorkspaceDirectory(candidate, true)
}

func resolvedPhysicalDirectory(candidate string) (string, error) {
	return resolveWorkspaceDirectory(candidate, false)
}

func resolveWorkspaceDirectory(candidate string, rejectFinalSymlink bool) (string, error) {
	absolute, err := filepath.Abs(candidate)
	if err != nil {
		return "", err
	}
	initial, err := os.Lstat(absolute)
	if err != nil || (rejectFinalSymlink && initial.Mode()&os.ModeSymlink != 0) || (initial.Mode()&os.ModeSymlink == 0 && !initial.IsDir()) {
		return "", fmt.Errorf("configured path is not a real directory")
	}
	physical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	final, err := os.Lstat(physical)
	if err != nil || final.Mode()&os.ModeSymlink != 0 || !final.IsDir() {
		return "", fmt.Errorf("configured path is not a physical directory")
	}
	return filepath.Clean(physical), nil
}

func sameOrDescendant(candidate, parent string) bool {
	if candidate == parent {
		return true
	}
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && relative != ".." && !filepath.IsAbs(relative) && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func physicalSameOrDescendantIdentity(candidate string, parentStat unix.Stat_t) (bool, error) {
	if candidate == "" || parentStat.Mode&unix.S_IFMT != unix.S_IFDIR {
		return false, fmt.Errorf("invalid physical containment boundary")
	}
	current := filepath.Clean(candidate)
	for {
		var currentStat unix.Stat_t
		if err := unix.Lstat(current, &currentStat); err != nil || currentStat.Mode&unix.S_IFMT != unix.S_IFDIR {
			return false, fmt.Errorf("cannot stat physical candidate ancestor")
		}
		if sameIdentity(currentStat, parentStat) {
			return true, nil
		}
		next := filepath.Dir(current)
		if next == current {
			return false, nil
		}
		current = next
	}
}
