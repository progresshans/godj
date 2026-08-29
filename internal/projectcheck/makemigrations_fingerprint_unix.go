//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"golang.org/x/sys/unix"
)

const makemigrationsBuildInputFingerprintDomain = "godj/makemigrations-build-input/v1\x00"

const (
	makemigrationsBuildInputAbsent    byte = 0
	makemigrationsBuildInputRegular   byte = 1
	makemigrationsBuildInputSynthetic byte = 2
)

const (
	makemigrationsDependencyGraphMember = "@go-list/dependency-graph-v1"
	makemigrationsFrameworkModulePath   = "github.com/progresshans/godj"
)

var errMakemigrationsBuildInputMissing = errors.New("makemigrations build input is missing")

type makemigrationsBuildInputFingerprint struct {
	digest        [sha256.Size]byte
	memberCount   uint64
	documentBytes uint64
}

type makemigrationsBuildInputLimits struct {
	goListBytes      uint64
	packageCount     int
	memberCount      int
	pathBytes        int
	pathDepth        int
	fileBytes        uint64
	documentBytes    uint64
	totalRosterBytes uint64
}

func defaultMakemigrationsBuildInputLimits() makemigrationsBuildInputLimits {
	return makemigrationsBuildInputLimits{
		goListBytes:      32 << 20,
		packageCount:     65_536,
		memberCount:      65_536,
		pathBytes:        4 << 10,
		pathDepth:        128,
		fileBytes:        64 << 20,
		documentBytes:    256 << 20,
		totalRosterBytes: 16 << 20,
	}
}

type makemigrationsGoListModule struct {
	Path    string                      `json:"Path"`
	Version string                      `json:"Version"`
	Dir     string                      `json:"Dir"`
	Main    bool                        `json:"Main"`
	Replace *makemigrationsGoListModule `json:"Replace"`
}

type makemigrationsGoListPackage struct {
	Dir          string                      `json:"Dir"`
	ImportPath   string                      `json:"ImportPath"`
	Name         string                      `json:"Name"`
	Goroot       bool                        `json:"Goroot"`
	Standard     bool                        `json:"Standard"`
	Incomplete   bool                        `json:"Incomplete"`
	Error        json.RawMessage             `json:"Error"`
	Module       *makemigrationsGoListModule `json:"Module"`
	GoFiles      []string                    `json:"GoFiles"`
	CgoFiles     []string                    `json:"CgoFiles"`
	CFiles       []string                    `json:"CFiles"`
	CXXFiles     []string                    `json:"CXXFiles"`
	MFiles       []string                    `json:"MFiles"`
	HFiles       []string                    `json:"HFiles"`
	FFiles       []string                    `json:"FFiles"`
	SFiles       []string                    `json:"SFiles"`
	SwigFiles    []string                    `json:"SwigFiles"`
	SwigCXXFiles []string                    `json:"SwigCXXFiles"`
	SysoFiles    []string                    `json:"SysoFiles"`
	EmbedFiles   []string                    `json:"EmbedFiles"`
}

type makemigrationsDependencyGraphModule struct {
	Path    string                                    `json:"path"`
	Version string                                    `json:"version"`
	Replace *makemigrationsDependencyGraphReplacement `json:"replace"`
}

type makemigrationsDependencyGraphReplacement struct {
	Path    string `json:"path"`
	Version string `json:"version"`
}

type makemigrationsDependencyGraphPackage struct {
	ImportPath string                               `json:"import_path"`
	Module     *makemigrationsDependencyGraphModule `json:"module"`
}

type makemigrationsBuildInputMember struct {
	path       string
	marker     byte
	permission uint32
	document   []byte
	info       os.FileInfo
}

type makemigrationsRetainedDirectory struct {
	path string
	info os.FileInfo
}

// computeMakemigrationsBuildInputFingerprint hashes the exact retained
// project inputs named by a bounded `go list -deps -json -mod=readonly`
// response. It does not invoke go or any other child process.
func computeMakemigrationsBuildInputFingerprint(
	project retainedProject,
	goListDocument []byte,
) (makemigrationsBuildInputFingerprint, error) {
	return computeMakemigrationsBuildInputFingerprintWithLimits(
		project,
		goListDocument,
		defaultMakemigrationsBuildInputLimits(),
	)
}

func computeMakemigrationsBuildInputFingerprintWithLimits(
	project retainedProject,
	goListDocument []byte,
	limits makemigrationsBuildInputLimits,
) (makemigrationsBuildInputFingerprint, error) {
	if err := validateMakemigrationsBuildInputLimits(limits); err != nil {
		return makemigrationsBuildInputFingerprint{}, err
	}
	if !verifyRetainedProject(project) || !validMakemigrationsPhysicalRoot(project.rootPath) ||
		!validProjectPackage(project.descriptor.packagePath) {
		return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: retained project is invalid")
	}
	if len(goListDocument) == 0 || uint64(len(goListDocument)) > limits.goListBytes || !utf8.Valid(goListDocument) {
		return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: invalid go-list document")
	}

	rootBefore, err := project.root.Stat()
	if err != nil || !rootBefore.IsDir() {
		return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: inspect retained root")
	}

	packages, err := parseMakemigrationsGoList(goListDocument, limits.packageCount)
	if err != nil {
		return makemigrationsBuildInputFingerprint{}, err
	}
	graphDocument, err := canonicalMakemigrationsDependencyGraph(packages)
	if err != nil {
		return makemigrationsBuildInputFingerprint{}, err
	}
	members := make([]makemigrationsBuildInputMember, 0, len(packages)+7)
	seenMembers := make(map[string]struct{}, len(packages)+7)
	seenPackageDirectories := make(map[string]struct{}, len(packages))
	seenRetainedDirectories := make(map[string]struct{}, len(packages))
	retainedDirectories := make([]makemigrationsRetainedDirectory, 0, len(packages))
	selectedDirectory := strings.TrimPrefix(project.descriptor.packagePath, "./")
	selectedSeen := false
	var rosterBytes uint64

	for _, listed := range packages {
		inside, relativeDirectory, classifyErr := classifyMakemigrationsPackageDirectory(project.rootPath, listed)
		if classifyErr != nil {
			return makemigrationsBuildInputFingerprint{}, classifyErr
		}
		if !inside {
			continue
		}
		if listed.Name == "" || !utf8.ValidString(listed.Name) || !utf8.ValidString(listed.ImportPath) {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: invalid project-owned package")
		}
		if _, duplicate := seenPackageDirectories[relativeDirectory]; duplicate {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: duplicate package directory %q", relativeDirectory)
		}
		seenPackageDirectories[relativeDirectory] = struct{}{}
		if relativeDirectory == selectedDirectory {
			if listed.Name != "main" || selectedSeen {
				return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: selected package is not one exact main package")
			}
			selectedSeen = true
		}

		directory, openErr := openMakemigrationsRetainedRelative(project.root, relativeDirectory, true, limits)
		if openErr != nil {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: open package directory %q: %w", relativeDirectory, openErr)
		}
		directoryInfo, statErr := directory.Stat()
		closeErr := directory.Close()
		if statErr != nil || closeErr != nil || !directoryInfo.IsDir() {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: retain package directory %q", relativeDirectory)
		}
		retainedDirectories = append(retainedDirectories, makemigrationsRetainedDirectory{path: relativeDirectory, info: directoryInfo})
		seenRetainedDirectories[relativeDirectory] = struct{}{}

		for _, filename := range makemigrationsPackageFiles(listed) {
			memberPath, pathErr := makemigrationsPackageMemberPath(relativeDirectory, filename, limits)
			if pathErr != nil {
				return makemigrationsBuildInputFingerprint{}, pathErr
			}
			if err := reserveMakemigrationsMember(memberPath, seenMembers, &rosterBytes, limits); err != nil {
				return makemigrationsBuildInputFingerprint{}, err
			}
			members = append(members, makemigrationsBuildInputMember{path: memberPath, marker: makemigrationsBuildInputRegular})
		}
	}
	if !selectedSeen {
		return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: selected main package is absent")
	}
	if err := reserveMakemigrationsMember(makemigrationsDependencyGraphMember, seenMembers, &rosterBytes, limits); err != nil {
		return makemigrationsBuildInputFingerprint{}, err
	}
	members = append(members, makemigrationsBuildInputMember{
		path:     makemigrationsDependencyGraphMember,
		marker:   makemigrationsBuildInputSynthetic,
		document: graphDocument,
	})

	staticMembers := []struct{ path string }{
		{path: descriptorName},
		{path: "go.mod"},
		{path: "go.sum"},
		{path: "go.work"},
		{path: "go.work.sum"},
		{path: ".godj/generated-manifest.json"},
	}
	for _, static := range staticMembers {
		if err := reserveMakemigrationsMember(static.path, seenMembers, &rosterBytes, limits); err != nil {
			return makemigrationsBuildInputFingerprint{}, err
		}
		members = append(members, makemigrationsBuildInputMember{
			path:   static.path,
			marker: makemigrationsBuildInputRegular,
		})
	}
	if len(members) > limits.memberCount {
		return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: member limit exceeded")
	}
	for _, member := range members {
		if member.marker == makemigrationsBuildInputSynthetic {
			continue
		}
		requiredDirectory := member.path != ".godj/generated-manifest.json"
		for _, directoryPath := range makemigrationsMemberParentDirectories(member.path) {
			if _, retained := seenRetainedDirectories[directoryPath]; retained {
				continue
			}
			directory, openErr := openMakemigrationsRetainedRelative(project.root, directoryPath, true, limits)
			if errors.Is(openErr, errMakemigrationsBuildInputMissing) && !requiredDirectory {
				continue
			}
			if openErr != nil {
				return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: open member parent %q: %w", directoryPath, openErr)
			}
			directoryInfo, statErr := directory.Stat()
			closeErr := directory.Close()
			if statErr != nil || closeErr != nil || !directoryInfo.IsDir() {
				return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: retain member parent %q", directoryPath)
			}
			retainedDirectories = append(retainedDirectories, makemigrationsRetainedDirectory{path: directoryPath, info: directoryInfo})
			seenRetainedDirectories[directoryPath] = struct{}{}
		}
	}

	sort.Slice(members, func(left, right int) bool {
		return members[left].path < members[right].path
	})
	var documentBytes uint64
	for index := range members {
		if members[index].marker == makemigrationsBuildInputSynthetic {
			if uint64(len(members[index].document)) > limits.documentBytes-documentBytes {
				return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: dependency graph limit exceeded")
			}
			documentBytes += uint64(len(members[index].document))
			continue
		}
		required := members[index].path == descriptorName || !isMakemigrationsOptionalStaticMember(members[index].path)
		document, permission, info, present, readErr := readMakemigrationsBuildInputMember(
			project.root,
			members[index].path,
			required,
			limits,
			documentBytes,
		)
		if readErr != nil {
			return makemigrationsBuildInputFingerprint{}, readErr
		}
		if !present {
			members[index].marker = makemigrationsBuildInputAbsent
			continue
		}
		members[index].permission = permission
		members[index].document = document
		members[index].info = info
		if members[index].path == descriptorName {
			parsed, failure := parseProjectDescriptor(document)
			if failure != nil || parsed != project.descriptor {
				return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: descriptor semantics changed")
			}
		}
		documentBytes += uint64(len(document))
	}

	for _, member := range members {
		if member.marker != makemigrationsBuildInputRegular {
			continue
		}
		current, openErr := openMakemigrationsRetainedRelative(project.root, member.path, false, limits)
		if openErr != nil {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: member changed after capture %q", member.path)
		}
		currentInfo, statErr := current.Stat()
		closeErr := current.Close()
		if statErr != nil || closeErr != nil || !sameMakemigrationsFileVersion(member.info, currentInfo) {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: member changed after capture %q", member.path)
		}
	}
	for _, retained := range retainedDirectories {
		current, openErr := openMakemigrationsRetainedRelative(project.root, retained.path, true, limits)
		if openErr != nil {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: package path rebound %q", retained.path)
		}
		currentInfo, statErr := current.Stat()
		closeErr := current.Close()
		if statErr != nil || closeErr != nil || !sameMakemigrationsFileVersion(retained.info, currentInfo) {
			return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: package path rebound %q", retained.path)
		}
	}
	rootAfter, err := project.root.Stat()
	if err != nil || !sameMakemigrationsFileVersion(rootBefore, rootAfter) || !verifyRetainedProject(project) {
		return makemigrationsBuildInputFingerprint{}, fmt.Errorf("makemigrations build input: project root rebound")
	}

	hash := sha256.New()
	_, _ = hash.Write([]byte(makemigrationsBuildInputFingerprintDomain))
	var scalar [8]byte
	var mode [4]byte
	for _, member := range members {
		binary.BigEndian.PutUint64(scalar[:], uint64(len(member.path)))
		_, _ = hash.Write(scalar[:])
		_, _ = hash.Write([]byte(member.path))
		_, _ = hash.Write([]byte{member.marker})
		binary.BigEndian.PutUint32(mode[:], member.permission)
		_, _ = hash.Write(mode[:])
		binary.BigEndian.PutUint64(scalar[:], uint64(len(member.document)))
		_, _ = hash.Write(scalar[:])
		_, _ = hash.Write(member.document)
	}
	var digest [sha256.Size]byte
	copy(digest[:], hash.Sum(nil))
	return makemigrationsBuildInputFingerprint{
		digest:        digest,
		memberCount:   uint64(len(members)),
		documentBytes: documentBytes,
	}, nil
}

func validateMakemigrationsBuildInputLimits(limits makemigrationsBuildInputLimits) error {
	if limits.goListBytes == 0 || limits.packageCount <= 0 || limits.memberCount < 6 || limits.pathBytes <= 0 ||
		limits.pathDepth <= 0 || limits.fileBytes == 0 || limits.documentBytes == 0 || limits.totalRosterBytes == 0 ||
		limits.fileBytes >= uint64(^uint64(0)>>1) || limits.documentBytes >= uint64(^uint64(0)>>1) {
		return fmt.Errorf("makemigrations build input: invalid limits")
	}
	return nil
}

func validMakemigrationsPhysicalRoot(root string) bool {
	return root != "" && filepath.IsAbs(root) && filepath.Clean(root) == root && utf8.ValidString(root) &&
		!strings.ContainsRune(root, 0)
}

func parseMakemigrationsGoList(document []byte, maximum int) ([]makemigrationsGoListPackage, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	packages := make([]makemigrationsGoListPackage, 0, 32)
	for {
		var listed makemigrationsGoListPackage
		err := decoder.Decode(&listed)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("makemigrations build input: decode go-list document: %w", err)
		}
		if len(packages) == maximum {
			return nil, fmt.Errorf("makemigrations build input: package limit exceeded")
		}
		if listed.Incomplete || (len(listed.Error) != 0 && !bytes.Equal(bytes.TrimSpace(listed.Error), []byte("null"))) {
			return nil, fmt.Errorf("makemigrations build input: incomplete go-list package")
		}
		packages = append(packages, listed)
	}
	if len(packages) == 0 {
		return nil, fmt.Errorf("makemigrations build input: empty go-list document")
	}
	return packages, nil
}

func canonicalMakemigrationsDependencyGraph(packages []makemigrationsGoListPackage) ([]byte, error) {
	graph := make([]makemigrationsDependencyGraphPackage, 0, len(packages))
	seen := make(map[string]struct{}, len(packages))
	for _, listed := range packages {
		if !validMakemigrationsDependencyScalar(listed.ImportPath) {
			return nil, fmt.Errorf("makemigrations build input: invalid dependency import path")
		}
		if _, duplicate := seen[listed.ImportPath]; duplicate {
			return nil, fmt.Errorf("makemigrations build input: duplicate dependency import path %q", listed.ImportPath)
		}
		seen[listed.ImportPath] = struct{}{}
		entry := makemigrationsDependencyGraphPackage{ImportPath: listed.ImportPath}
		if listed.Module != nil {
			if !validMakemigrationsDependencyScalar(listed.Module.Path) ||
				!validMakemigrationsDependencyScalarAllowEmpty(listed.Module.Version) {
				return nil, fmt.Errorf("makemigrations build input: invalid dependency module")
			}
			entry.Module = &makemigrationsDependencyGraphModule{
				Path:    listed.Module.Path,
				Version: listed.Module.Version,
			}
			if listed.Module.Replace != nil {
				if !validMakemigrationsDependencyScalar(listed.Module.Replace.Path) ||
					!validMakemigrationsDependencyScalarAllowEmpty(listed.Module.Replace.Version) {
					return nil, fmt.Errorf("makemigrations build input: invalid dependency replacement")
				}
				entry.Module.Replace = &makemigrationsDependencyGraphReplacement{
					Path:    listed.Module.Replace.Path,
					Version: listed.Module.Replace.Version,
				}
			}
		}
		graph = append(graph, entry)
	}
	sort.Slice(graph, func(left, right int) bool {
		return graph[left].ImportPath < graph[right].ImportPath
	})
	document, err := json.Marshal(graph)
	if err != nil {
		return nil, fmt.Errorf("makemigrations build input: encode dependency graph: %w", err)
	}
	return document, nil
}

func validMakemigrationsDependencyScalar(value string) bool {
	return value != "" && validMakemigrationsDependencyScalarAllowEmpty(value)
}

func validMakemigrationsDependencyScalarAllowEmpty(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func classifyMakemigrationsPackageDirectory(
	root string,
	listed makemigrationsGoListPackage,
) (bool, string, error) {
	if listed.Dir == "" || !utf8.ValidString(listed.Dir) || strings.ContainsRune(listed.Dir, 0) ||
		!filepath.IsAbs(listed.Dir) || filepath.Clean(listed.Dir) != listed.Dir {
		return false, "", fmt.Errorf("makemigrations build input: invalid package directory")
	}
	relative, err := filepath.Rel(root, listed.Dir)
	if err != nil {
		return false, "", fmt.Errorf("makemigrations build input: classify package directory: %w", err)
	}
	relative = filepath.ToSlash(relative)
	inside := relative == "." || (relative != ".." && !strings.HasPrefix(relative, "../"))
	if inside {
		return true, relative, nil
	}
	if listed.Standard || listed.Goroot || makemigrationsExternalModuleIsContentAddressed(listed.Module) ||
		makemigrationsExternalModuleIsFramework(listed.Module) {
		return false, "", nil
	}
	return false, "", fmt.Errorf("makemigrations build input: unbound outside-root package %q", listed.ImportPath)
}

func makemigrationsExternalModuleIsContentAddressed(module *makemigrationsGoListModule) bool {
	if module == nil || module.Main {
		return false
	}
	effective := module
	if module.Replace != nil {
		effective = module.Replace
	}
	return module.Path != "" && effective.Version != "" && effective.Dir != "" && utf8.ValidString(effective.Dir) &&
		filepath.IsAbs(effective.Dir) && filepath.Clean(effective.Dir) == effective.Dir
}

func makemigrationsExternalModuleIsFramework(module *makemigrationsGoListModule) bool {
	return module != nil && module.Path == makemigrationsFrameworkModulePath
}

func makemigrationsPackageFiles(listed makemigrationsGoListPackage) []string {
	count := len(listed.GoFiles) + len(listed.CgoFiles) + len(listed.CFiles) + len(listed.CXXFiles) +
		len(listed.MFiles) + len(listed.HFiles) + len(listed.FFiles) + len(listed.SFiles) +
		len(listed.SwigFiles) + len(listed.SwigCXXFiles) + len(listed.SysoFiles) + len(listed.EmbedFiles)
	files := make([]string, 0, count)
	files = append(files, listed.GoFiles...)
	files = append(files, listed.CgoFiles...)
	files = append(files, listed.CFiles...)
	files = append(files, listed.CXXFiles...)
	files = append(files, listed.MFiles...)
	files = append(files, listed.HFiles...)
	files = append(files, listed.FFiles...)
	files = append(files, listed.SFiles...)
	files = append(files, listed.SwigFiles...)
	files = append(files, listed.SwigCXXFiles...)
	files = append(files, listed.SysoFiles...)
	files = append(files, listed.EmbedFiles...)
	return files
}

func makemigrationsPackageMemberPath(
	directory string,
	filename string,
	limits makemigrationsBuildInputLimits,
) (string, error) {
	if !validMakemigrationsRelativePath(filename, false, limits) {
		return "", fmt.Errorf("makemigrations build input: invalid package member %q", filename)
	}
	memberPath := filename
	if directory != "." {
		memberPath = directory + "/" + filename
	}
	if !validMakemigrationsRelativePath(memberPath, false, limits) {
		return "", fmt.Errorf("makemigrations build input: invalid project member %q", memberPath)
	}
	return memberPath, nil
}

func makemigrationsMemberParentDirectories(memberPath string) []string {
	components := strings.Split(memberPath, "/")
	if len(components) < 2 {
		return nil
	}
	directories := make([]string, 0, len(components)-1)
	for end := 1; end < len(components); end++ {
		directories = append(directories, strings.Join(components[:end], "/"))
	}
	return directories
}

func reserveMakemigrationsMember(
	memberPath string,
	seen map[string]struct{},
	rosterBytes *uint64,
	limits makemigrationsBuildInputLimits,
) error {
	if !validMakemigrationsRelativePath(memberPath, false, limits) {
		return fmt.Errorf("makemigrations build input: invalid member path %q", memberPath)
	}
	if _, duplicate := seen[memberPath]; duplicate {
		return fmt.Errorf("makemigrations build input: duplicate member path %q", memberPath)
	}
	if len(seen) == limits.memberCount {
		return fmt.Errorf("makemigrations build input: member limit exceeded")
	}
	if uint64(len(memberPath)) > limits.totalRosterBytes-*rosterBytes {
		return fmt.Errorf("makemigrations build input: roster path limit exceeded")
	}
	*rosterBytes += uint64(len(memberPath))
	seen[memberPath] = struct{}{}
	return nil
}

func validMakemigrationsRelativePath(value string, allowDot bool, limits makemigrationsBuildInputLimits) bool {
	if value == "." {
		return allowDot
	}
	if value == "" || len(value) > limits.pathBytes || !utf8.ValidString(value) || strings.ContainsRune(value, 0) ||
		strings.Contains(value, "\\") || strings.HasPrefix(value, "/") || path.Clean(value) != value {
		return false
	}
	components := strings.Split(value, "/")
	if len(components) > limits.pathDepth {
		return false
	}
	for _, component := range components {
		if component == "" || component == "." || component == ".." {
			return false
		}
	}
	return true
}

func isMakemigrationsOptionalStaticMember(memberPath string) bool {
	switch memberPath {
	case "go.mod", "go.sum", "go.work", "go.work.sum", ".godj/generated-manifest.json":
		return true
	default:
		return false
	}
}

func readMakemigrationsBuildInputMember(
	root *os.File,
	memberPath string,
	required bool,
	limits makemigrationsBuildInputLimits,
	documentBytes uint64,
) ([]byte, uint32, os.FileInfo, bool, error) {
	file, err := openMakemigrationsRetainedRelative(root, memberPath, false, limits)
	if errors.Is(err, errMakemigrationsBuildInputMissing) && !required {
		return nil, 0, nil, false, nil
	}
	if err != nil {
		return nil, 0, nil, false, fmt.Errorf("makemigrations build input: open %q: %w", memberPath, err)
	}
	before, statErr := file.Stat()
	if statErr != nil || !before.Mode().IsRegular() {
		_ = file.Close()
		return nil, 0, nil, false, fmt.Errorf("makemigrations build input: inspect %q", memberPath)
	}
	maximum := limits.fileBytes
	if documentBytes >= limits.documentBytes {
		maximum = 0
	} else if remaining := limits.documentBytes - documentBytes; maximum > remaining {
		maximum = remaining
	}
	if before.Size() < 0 || uint64(before.Size()) > maximum {
		_ = file.Close()
		return nil, 0, nil, false, fmt.Errorf("makemigrations build input: document limit exceeded at %q", memberPath)
	}
	document, readErr := io.ReadAll(io.LimitReader(file, int64(maximum)+1))
	after, afterErr := file.Stat()
	closeErr := file.Close()
	if readErr != nil || afterErr != nil || closeErr != nil || uint64(len(document)) > maximum ||
		!sameMakemigrationsFileVersion(before, after) || int64(len(document)) != after.Size() {
		clear(document)
		return nil, 0, nil, false, fmt.Errorf("makemigrations build input: unstable or oversized member %q", memberPath)
	}
	current, reopenErr := openMakemigrationsRetainedRelative(root, memberPath, false, limits)
	if reopenErr != nil {
		clear(document)
		return nil, 0, nil, false, fmt.Errorf("makemigrations build input: member path rebound %q", memberPath)
	}
	currentInfo, currentStatErr := current.Stat()
	currentCloseErr := current.Close()
	if currentStatErr != nil || currentCloseErr != nil || !sameMakemigrationsFileVersion(before, currentInfo) {
		clear(document)
		return nil, 0, nil, false, fmt.Errorf("makemigrations build input: member path rebound %q", memberPath)
	}
	return document, uint32(before.Mode().Perm()), before, true, nil
}

func openMakemigrationsRetainedRelative(
	root *os.File,
	relative string,
	directory bool,
	limits makemigrationsBuildInputLimits,
) (*os.File, error) {
	if root == nil || !validMakemigrationsRelativePath(relative, directory, limits) {
		return nil, fmt.Errorf("invalid retained relative path")
	}
	rootFD := int(root.Fd())
	fd, err := unix.Openat(rootFD, ".", unix.O_RDONLY|unix.O_CLOEXEC|unix.O_DIRECTORY|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("retain root: %w", err)
	}
	current := os.NewFile(uintptr(fd), ".")
	if current == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("retain root")
	}
	if relative == "." {
		return current, nil
	}

	components := strings.Split(relative, "/")
	for index, component := range components {
		last := index == len(components)-1
		wantDirectory := !last || directory
		var expected unix.Stat_t
		if err := unix.Fstatat(int(current.Fd()), component, &expected, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			_ = current.Close()
			if errors.Is(err, unix.ENOENT) {
				return nil, errMakemigrationsBuildInputMissing
			}
			return nil, fmt.Errorf("inspect retained component: %w", err)
		}
		kind := expected.Mode & unix.S_IFMT
		if (wantDirectory && kind != unix.S_IFDIR) || (!wantDirectory && kind != unix.S_IFREG) {
			_ = current.Close()
			return nil, fmt.Errorf("retained component has wrong kind")
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if wantDirectory {
			flags |= unix.O_DIRECTORY
		} else {
			flags |= unix.O_NONBLOCK
		}
		nextFD, openErr := unix.Openat(int(current.Fd()), component, flags, 0)
		if openErr != nil {
			_ = current.Close()
			return nil, fmt.Errorf("open retained component: %w", openErr)
		}
		var opened unix.Stat_t
		var rebound unix.Stat_t
		openedErr := unix.Fstat(nextFD, &opened)
		reboundErr := unix.Fstatat(int(current.Fd()), component, &rebound, unix.AT_SYMLINK_NOFOLLOW)
		if openedErr != nil || reboundErr != nil || !sameIdentity(expected, opened) || !sameIdentity(opened, rebound) ||
			opened.Mode&unix.S_IFMT != kind || rebound.Mode&unix.S_IFMT != kind {
			_ = unix.Close(nextFD)
			_ = current.Close()
			return nil, fmt.Errorf("retained component rebound")
		}
		next := os.NewFile(uintptr(nextFD), relative)
		if next == nil {
			_ = unix.Close(nextFD)
			_ = current.Close()
			return nil, fmt.Errorf("retain component")
		}
		if err := current.Close(); err != nil {
			_ = next.Close()
			return nil, fmt.Errorf("close retained parent: %w", err)
		}
		current = next
	}
	return current, nil
}

func sameMakemigrationsFileVersion(left, right os.FileInfo) bool {
	return left != nil && right != nil && os.SameFile(left, right) && left.Mode() == right.Mode() &&
		left.Size() == right.Size() && left.ModTime().Equal(right.ModTime())
}
