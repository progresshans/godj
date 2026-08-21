//go:build darwin || linux

package projectgenerate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/progresshans/godj/codegen"
)

const (
	maxCandidateListBytes       = 64 << 20
	maxCandidateDiagnosticBytes = 1 << 20
)

type candidateFile struct {
	path   string
	sha256 string
	mode   fs.FileMode
	source []byte
}

type goCandidateVerifier struct {
	projectRoot  string
	rootSeal     ProjectRoot
	manifest     committedManifest
	manifestData []byte
	files        []candidateFile
}

// NewGoCandidateVerifier snapshots one immutable bundle and returns a
// compile-only verifier for the physical project root. The constructor and
// Verify perform no project mutation.
func NewGoCandidateVerifier(projectRoot string, bundle codegen.GeneratedBundle) (CandidateVerifier, error) {
	return NewGoCandidateVerifierRoot(ProjectRoot{absolute: projectRoot}, bundle)
}

// NewGoCandidateVerifierRoot constructs a verifier bound to one sealed
// physical project identity.
func NewGoCandidateVerifierRoot(projectRoot ProjectRoot, bundle codegen.GeneratedBundle) (CandidateVerifier, error) {
	manifest, err := validateGeneratedBundle(bundle)
	if err != nil {
		return nil, err
	}
	root, err := resolveProjectRoot(projectRoot)
	if err != nil {
		return nil, fmt.Errorf("create candidate verifier: %w: %v", ErrGeneratedConflict, err)
	}
	generated := bundle.Files()
	files := make([]candidateFile, len(generated))
	for index, file := range generated {
		files[index] = candidateFile{
			path:   file.Path,
			sha256: file.SHA256,
			mode:   file.Mode,
			source: file.Source(),
		}
	}
	return &goCandidateVerifier{
		projectRoot:  root.absolute,
		rootSeal:     root,
		manifest:     manifest,
		manifestData: bundle.Manifest(),
		files:        files,
	}, nil
}

func (verifier *goCandidateVerifier) Verify(ctx context.Context, candidateRoot string) error {
	if verifier == nil {
		return ErrCandidateVerification
	}
	if ctx == nil {
		return fmt.Errorf("%w: context is nil", ErrCandidateVerification)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("%w: %w", ErrCandidateVerification, err)
	}
	if err := verifyProjectRoot(verifier.rootSeal); err != nil {
		return fmt.Errorf("%w: %w: selected project root changed", ErrCandidateVerification, ErrGeneratedConflict)
	}
	stageRoot, err := canonicalProjectRoot(candidateRoot)
	if err != nil {
		return fmt.Errorf("%w: invalid candidate root: %v", ErrCandidateVerification, err)
	}
	if stageRoot == verifier.projectRoot {
		return fmt.Errorf("%w: candidate root aliases project root", ErrCandidateVerification)
	}
	if err := verifier.validateStage(ctx, stageRoot); err != nil {
		return err
	}

	overlay := make(map[string]string, len(verifier.files))
	current := make(map[string]struct{}, len(verifier.files))
	for _, file := range verifier.files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrCandidateVerification, err)
		}
		target, err := validateOverlayTarget(verifier.projectRoot, file.path)
		if err != nil {
			return fmt.Errorf("%w: %w: %v", ErrCandidateVerification, ErrGeneratedConflict, err)
		}
		backing, err := confinedProjectPath(stageRoot, file.path)
		if err != nil {
			return fmt.Errorf("%w: invalid staged path: %v", ErrCandidateVerification, err)
		}
		overlay[target] = backing
		current[file.path] = struct{}{}
	}

	prior, exists, err := readPriorManifest(verifier.projectRoot)
	if err != nil {
		return fmt.Errorf("%w: %w: %v", ErrCandidateVerification, ErrGeneratedConflict, err)
	}
	if exists {
		for _, file := range prior.Files {
			if _, retained := current[file.Path]; retained {
				continue
			}
			target, err := validateOverlayTarget(verifier.projectRoot, file.Path)
			if err != nil {
				return fmt.Errorf("%w: %w: stale target: %v", ErrCandidateVerification, ErrGeneratedConflict, err)
			}
			overlay[target] = ""
		}
	}
	compileErr := verifier.compileOverlay(ctx, overlay)
	if err := verifyProjectRoot(verifier.rootSeal); err != nil {
		return fmt.Errorf("%w: %w: selected project root changed", ErrCandidateVerification, ErrGeneratedConflict)
	}
	return compileErr
}

func (verifier *goCandidateVerifier) validateStage(ctx context.Context, stageRoot string) error {
	manifest, mode, err := readRegularProjectFileBounded(stageRoot, generatedManifestRelativePath, maxCommittedManifestBytes)
	if err != nil || mode != 0o644 || !bytes.Equal(manifest, verifier.manifestData) {
		return fmt.Errorf("%w: staged manifest does not match bundle", ErrCandidateVerification)
	}
	if _, err := decodeCommittedManifest(manifest); err != nil {
		return fmt.Errorf("%w: staged manifest is invalid: %v", ErrCandidateVerification, err)
	}
	for _, file := range verifier.files {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrCandidateVerification, err)
		}
		contents, fileMode, err := readRegularProjectFile(stageRoot, file.path)
		if err != nil || fileMode != file.mode || sha256Hex(contents) != file.sha256 || !bytes.Equal(contents, file.source) {
			return fmt.Errorf("%w: staged file %q does not match bundle", ErrCandidateVerification, file.path)
		}
	}
	return nil
}

func (verifier *goCandidateVerifier) compileOverlay(ctx context.Context, replacements map[string]string) error {
	workspace, err := createCandidateWorkspace(verifier.projectRoot)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCandidateVerification, err)
	}
	defer os.RemoveAll(workspace.root)

	overlayData, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: replacements})
	if err != nil {
		return fmt.Errorf("%w: encode overlay: %v", ErrCandidateVerification, err)
	}
	overlayPath := filepath.Join(workspace.root, "overlay.json")
	if err := os.WriteFile(overlayPath, overlayData, 0o600); err != nil {
		return fmt.Errorf("%w: write overlay: %v", ErrCandidateVerification, err)
	}

	packages, err := listCandidatePackages(ctx, verifier.projectRoot, overlayPath, workspace.environment)
	if err != nil {
		return err
	}
	module, err := inspectCandidateModule(ctx, verifier.projectRoot, workspace.environment)
	if err != nil {
		return err
	}
	packages, err = mergeCandidatePackages(packages, verifier.manifest, module)
	if err != nil {
		return err
	}
	if len(packages) == 0 {
		return fmt.Errorf("%w: overlay project contains no Go packages", ErrCandidateVerification)
	}
	for index, importPath := range packages {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%w: %w", ErrCandidateVerification, err)
		}
		outputPath := filepath.Join(workspace.output, fmt.Sprintf("%06d.test", index))
		arguments := []string{
			"test", "-c", "-vet=off", "-buildvcs=false", "-mod=readonly", "-overlay=" + overlayPath,
			"-o", outputPath, importPath,
		}
		output, runErr := runCandidateCommand(ctx, verifier.projectRoot, workspace.environment, maxCandidateDiagnosticBytes, "go", arguments...)
		if runErr != nil {
			return fmt.Errorf("%w: compile package %q: %v\n%s", ErrCandidateVerification, importPath, runErr, output)
		}
	}
	return nil
}

func readPriorManifest(projectRoot string) (committedManifest, bool, error) {
	document, _, err := readRegularProjectFileBounded(projectRoot, generatedManifestRelativePath, maxCommittedManifestBytes)
	if errors.Is(err, errProjectPathMissing) {
		return committedManifest{}, false, nil
	}
	if err != nil {
		return committedManifest{}, false, err
	}
	manifest, err := decodeCommittedManifest(document)
	if err != nil {
		return committedManifest{}, false, err
	}
	return manifest, true, nil
}

func validateOverlayTarget(projectRoot, relative string) (string, error) {
	absolute, err := confinedProjectPath(projectRoot, relative)
	if err != nil {
		return "", err
	}
	file, openErr := openProjectRelative(projectRoot, relative, false)
	if errors.Is(openErr, errProjectPathMissing) {
		return absolute, nil
	}
	if openErr != nil {
		return "", openErr
	}
	if closeErr := file.Close(); closeErr != nil {
		return "", closeErr
	}
	return absolute, nil
}

type candidateWorkspace struct {
	root        string
	output      string
	environment []string
}

func createCandidateWorkspace(projectRoot string) (candidateWorkspace, error) {
	temporaryBase := filepath.Clean(os.TempDir())
	physicalBase, err := filepath.EvalSymlinks(temporaryBase)
	if err != nil {
		return candidateWorkspace{}, fmt.Errorf("resolve temporary directory: %w", err)
	}
	if sameOrDescendantPath(physicalBase, projectRoot) {
		return candidateWorkspace{}, fmt.Errorf("temporary directory is inside project root")
	}
	root, err := os.MkdirTemp(physicalBase, "godj-candidate-")
	if err != nil {
		return candidateWorkspace{}, fmt.Errorf("create candidate workspace: %w", err)
	}
	fail := func(cause error) (candidateWorkspace, error) {
		_ = os.RemoveAll(root)
		return candidateWorkspace{}, cause
	}
	directories := []string{"output", "tmp", "gotmp", "gocache", "home", "xdg-config", "xdg-cache", "gopath", "gomodcache"}
	for _, name := range directories {
		if err := os.Mkdir(filepath.Join(root, name), 0o700); err != nil {
			return fail(fmt.Errorf("create candidate workspace directory: %w", err))
		}
	}
	environment := candidateCommandEnvironment(projectRoot, root, os.Environ())
	return candidateWorkspace{root: root, output: filepath.Join(root, "output"), environment: environment}, nil
}

func candidateCommandEnvironment(projectRoot, workspace string, ambient []string) []string {
	values := make(map[string]string, len(ambient)+16)
	for _, entry := range ambient {
		key, value, ok := strings.Cut(entry, "=")
		if ok && key != "" {
			values[key] = value
		}
	}
	moduleProxy := ambientModuleDownloadProxy(projectRoot, values)
	for _, key := range []string{
		"GOFLAGS", "GOWORK", "GOTOOLCHAIN", "GOENV", "GOCACHE", "GOCACHEPROG", "GOTMPDIR",
		"TMPDIR", "HOME", "XDG_CONFIG_HOME", "XDG_CACHE_HOME", "GOPATH", "GOMODCACHE",
	} {
		delete(values, key)
	}
	values["GOFLAGS"] = ""
	values["GOWORK"] = "off"
	values["GOTOOLCHAIN"] = "local"
	values["GOENV"] = "off"
	values["GOCACHE"] = filepath.Join(workspace, "gocache")
	values["GOCACHEPROG"] = ""
	values["GOTMPDIR"] = filepath.Join(workspace, "gotmp")
	values["TMPDIR"] = filepath.Join(workspace, "tmp")
	values["HOME"] = filepath.Join(workspace, "home")
	values["XDG_CONFIG_HOME"] = filepath.Join(workspace, "xdg-config")
	values["XDG_CACHE_HOME"] = filepath.Join(workspace, "xdg-cache")
	values["GOPATH"] = filepath.Join(workspace, "gopath")
	// All Go writes, including module download and extraction state, stay in
	// the private candidate workspace. A safe ambient cache may only be exposed
	// through its immutable download tree as a file proxy below.
	values["GOMODCACHE"] = filepath.Join(workspace, "gomodcache")
	if moduleProxy != "" {
		upstream := values["GOPROXY"]
		if upstream == "" {
			upstream = "https://proxy.golang.org,direct"
		}
		values["GOPROXY"] = moduleProxy + "," + upstream
	}
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

func ambientModuleDownloadProxy(projectRoot string, environment map[string]string) string {
	candidates := []string{environment["GOMODCACHE"]}
	if goPath := environment["GOPATH"]; goPath != "" {
		first := strings.Split(goPath, string(os.PathListSeparator))[0]
		candidates = append(candidates, filepath.Join(first, "pkg", "mod"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(home, "go", "pkg", "mod"))
	}
	for _, candidate := range candidates {
		if candidate == "" || !filepath.IsAbs(candidate) {
			continue
		}
		physicalCache, safe := externalPhysicalDirectory(candidate, projectRoot)
		if !safe {
			continue
		}
		physicalDownload, safe := externalPhysicalDirectory(filepath.Join(physicalCache, "cache", "download"), projectRoot)
		if !safe {
			continue
		}
		proxy := (&url.URL{Scheme: "file", Path: filepath.ToSlash(physicalDownload)}).String()
		// GOPROXY uses comma and pipe as separators even when they occur in a
		// URL path. Escape them explicitly so a valid cache path cannot alter
		// the configured fallback chain.
		proxy = strings.ReplaceAll(proxy, ",", "%2C")
		proxy = strings.ReplaceAll(proxy, "|", "%7C")
		return proxy
	}
	return ""
}

func externalPhysicalDirectory(candidate, projectRoot string) (string, bool) {
	physicalProjectRoot, err := filepath.EvalSymlinks(filepath.Clean(projectRoot))
	if err != nil {
		return "", false
	}
	physicalProjectRoot, err = filepath.Abs(physicalProjectRoot)
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
	if sameOrDescendantPath(physical, physicalProjectRoot) || sameOrDescendantPath(physicalProjectRoot, physical) {
		return "", false
	}
	return physical, true
}

func listCandidatePackages(ctx context.Context, projectRoot, overlayPath string, environment []string) ([]string, error) {
	output, diagnostics, err := runCandidateStructuredCommand(ctx, projectRoot, environment, maxCandidateListBytes,
		"go", "list", "-json", "-buildvcs=false", "-mod=readonly", "-overlay="+overlayPath, "./...")
	if err != nil {
		return nil, fmt.Errorf("%w: list overlay packages: %v\n%s", ErrCandidateVerification, err, diagnostics)
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	packages := make([]string, 0)
	seen := make(map[string]struct{})
	for {
		var listed struct {
			ImportPath string
		}
		if err := decoder.Decode(&listed); err == io.EOF {
			break
		} else if err != nil {
			return nil, fmt.Errorf("%w: decode overlay package list: %v", ErrCandidateVerification, err)
		}
		if listed.ImportPath == "" {
			return nil, fmt.Errorf("%w: package list contains an empty import path", ErrCandidateVerification)
		}
		if _, duplicate := seen[listed.ImportPath]; duplicate {
			return nil, fmt.Errorf("%w: package list repeats %q", ErrCandidateVerification, listed.ImportPath)
		}
		seen[listed.ImportPath] = struct{}{}
		packages = append(packages, listed.ImportPath)
	}
	sort.Strings(packages)
	return packages, nil
}

type candidateModule struct {
	Path            string
	Dir             string
	ProjectRelative string
}

func inspectCandidateModule(ctx context.Context, projectRoot string, environment []string) (candidateModule, error) {
	output, diagnostics, err := runCandidateStructuredCommand(ctx, projectRoot, environment, maxCandidateDiagnosticBytes,
		"go", "list", "-m", "-json", "-buildvcs=false", "-mod=readonly")
	if err != nil {
		return candidateModule{}, fmt.Errorf("%w: inspect project module: %v\n%s", ErrCandidateVerification, err, diagnostics)
	}
	var module candidateModule
	// The go command's module object has many fields. Decode the bounded subset
	// through a permissive local shape while still rejecting trailing values.
	var described struct {
		Path string
		Dir  string
	}
	decoder := json.NewDecoder(strings.NewReader(output))
	if err := decoder.Decode(&described); err != nil {
		return candidateModule{}, fmt.Errorf("%w: decode project module: %v", ErrCandidateVerification, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return candidateModule{}, fmt.Errorf("%w: project module output has trailing data", ErrCandidateVerification)
	}
	if described.Path == "" || described.Dir == "" {
		return candidateModule{}, fmt.Errorf("%w: project root is not a local Go module", ErrCandidateVerification)
	}
	directory, err := filepath.Abs(described.Dir)
	if err != nil {
		return candidateModule{}, fmt.Errorf("%w: resolve project module: %v", ErrCandidateVerification, err)
	}
	physical, err := filepath.EvalSymlinks(filepath.Clean(directory))
	if err != nil || !sameOrDescendantPath(projectRoot, physical) {
		return candidateModule{}, fmt.Errorf("%w: project root is outside the selected module", ErrCandidateVerification)
	}
	projectRelative, err := filepath.Rel(physical, projectRoot)
	if err != nil || filepath.IsAbs(projectRelative) || projectRelative == ".." || strings.HasPrefix(projectRelative, ".."+string(filepath.Separator)) {
		return candidateModule{}, fmt.Errorf("%w: resolve project root inside module", ErrCandidateVerification)
	}
	module.Path = described.Path
	module.Dir = physical
	module.ProjectRelative = filepath.ToSlash(projectRelative)
	return module, nil
}

func mergeCandidatePackages(existing []string, manifest committedManifest, module candidateModule) ([]string, error) {
	seen := make(map[string]struct{}, len(existing)+len(manifest.Apps)+1)
	packages := append([]string(nil), existing...)
	for _, importPath := range existing {
		seen[importPath] = struct{}{}
	}
	declared := make([]manifestPackage, 0, len(manifest.Apps)+1)
	declared = append(declared, manifest.Project)
	for _, app := range manifest.Apps {
		declared = append(declared, app.Package)
	}
	for _, pkg := range declared {
		moduleDirectory := module.ProjectRelative
		if pkg.Directory != "." {
			moduleDirectory = path.Join(moduleDirectory, pkg.Directory)
		}
		expected := module.Path
		if moduleDirectory != "." {
			expected += "/" + moduleDirectory
		}
		if pkg.ImportPath != expected {
			return nil, fmt.Errorf(
				"%w: package %q at %q is outside module mapping %q",
				ErrCandidateVerification,
				pkg.ImportPath,
				pkg.Directory,
				expected,
			)
		}
		if _, duplicate := seen[pkg.ImportPath]; !duplicate {
			seen[pkg.ImportPath] = struct{}{}
			packages = append(packages, pkg.ImportPath)
		}
	}
	sort.Strings(packages)
	return packages, nil
}

func runCandidateCommand(
	ctx context.Context,
	directory string,
	environment []string,
	maximum int,
	program string,
	arguments ...string,
) (string, error) {
	capture := &boundedCommandCapture{maximum: maximum}
	command := exec.CommandContext(ctx, program, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout = capture
	command.Stderr = capture
	err := command.Run()
	output, truncated := capture.result()
	if truncated {
		output += "\n[diagnostics truncated]"
	}
	return output, err
}

func runCandidateStructuredCommand(
	ctx context.Context,
	directory string,
	environment []string,
	maximum int,
	program string,
	arguments ...string,
) (string, string, error) {
	stdout := &boundedCommandCapture{maximum: maximum}
	stderr := &boundedCommandCapture{maximum: maxCandidateDiagnosticBytes}
	command := exec.CommandContext(ctx, program, arguments...)
	command.Dir = directory
	command.Env = append([]string(nil), environment...)
	command.Stdout = stdout
	command.Stderr = stderr
	err := command.Run()
	output, outputTruncated := stdout.result()
	diagnostics, diagnosticsTruncated := stderr.result()
	if outputTruncated {
		if err == nil {
			err = fmt.Errorf("structured command output exceeds resource limit")
		}
		diagnostics += "\n[structured output truncated]"
	}
	if diagnosticsTruncated {
		diagnostics += "\n[diagnostics truncated]"
	}
	return output, diagnostics, err
}

type boundedCommandCapture struct {
	mu        sync.Mutex
	maximum   int
	contents  bytes.Buffer
	truncated bool
}

func (capture *boundedCommandCapture) Write(payload []byte) (int, error) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	remaining := capture.maximum - capture.contents.Len()
	if remaining > 0 {
		if remaining > len(payload) {
			remaining = len(payload)
		}
		_, _ = capture.contents.Write(payload[:remaining])
	}
	if remaining < len(payload) {
		capture.truncated = true
	}
	return len(payload), nil
}

func (capture *boundedCommandCapture) result() (string, bool) {
	capture.mu.Lock()
	defer capture.mu.Unlock()
	return capture.contents.String(), capture.truncated
}

func sameOrDescendantPath(candidate, parent string) bool {
	relative, err := filepath.Rel(parent, candidate)
	return err == nil && !filepath.IsAbs(relative) && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
