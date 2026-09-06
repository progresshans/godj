// Package gobuild contains the small environment and diagnostic boundaries
// shared by project compilation and conformance helper builds. It never caches
// a command's success or skips a compiler invocation.
package gobuild

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"unicode"
)

const (
	// ColdBuildEnvironment requests private compilation caches for an isolation
	// or cold-build check. It is a host control, not a child runtime setting.
	ColdBuildEnvironment = "GODJ_COLD_BUILD"
	MaxDiagnosticBytes   = 8 << 10
	maxDiagnosticLines   = 24
)

// Environment reuses an existing, owner-writable Go build cache outside the
// project. All other settings, including private HOME/TMP/GOMODCACHE, remain
// owned by the caller. -trimpath permits dependency compilation reuse across
// private module-cache roots without retaining those roots in output binaries.
func Environment(private, ambient []string, projectRoot string) []string {
	values := environmentValues(private)
	source := environmentValues(ambient)
	delete(values, ColdBuildEnvironment)
	if source[ColdBuildEnvironment] != "1" && source["GOCACHE"] != "off" {
		for _, candidate := range cacheCandidates(source) {
			if cache, ok := externalCache(candidate, projectRoot); ok {
				values["GOCACHE"] = cache
				flags := strings.Fields(values["GOFLAGS"])
				found := false
				for _, flag := range flags {
					found = found || flag == "-trimpath"
				}
				if !found {
					flags = append(flags, "-trimpath")
				}
				values["GOFLAGS"] = strings.Join(flags, " ")
				break
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(values))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}

// RuntimeEnvironment retains ambient settings except host-only build controls.
// A long-lived project server uses this instead of the private build environment.
func RuntimeEnvironment(ambient []string) []string {
	result := make([]string, 0, len(ambient))
	for _, entry := range ambient {
		if !strings.HasPrefix(entry, ColdBuildEnvironment+"=") {
			result = append(result, entry)
		}
	}
	return result
}

func environmentValues(environment []string) map[string]string {
	values := make(map[string]string, len(environment))
	for _, entry := range environment {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[key] = value
		}
	}
	return values
}

func cacheCandidates(environment map[string]string) []string {
	candidates := []string{environment["GOCACHE"]}
	if runtime.GOOS == "darwin" {
		if home := environment["HOME"]; home != "" {
			candidates = append(candidates, filepath.Join(home, "Library", "Caches", "go-build"))
		}
	} else if xdg := environment["XDG_CACHE_HOME"]; xdg != "" {
		candidates = append(candidates, filepath.Join(xdg, "go-build"))
	} else if home := environment["HOME"]; home != "" {
		candidates = append(candidates, filepath.Join(home, ".cache", "go-build"))
	}
	return candidates
}

func externalCache(candidate, projectRoot string) (string, bool) {
	if !filepath.IsAbs(candidate) || !filepath.IsAbs(projectRoot) {
		return "", false
	}
	cache, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false
	}
	project, err := filepath.EvalSymlinks(projectRoot)
	if err != nil || containsPath(cache, project) || containsPath(project, cache) {
		return "", false
	}
	info, err := os.Stat(cache)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0o222 != 0o200 {
		return "", false
	}
	return cache, true
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

var (
	compilerLine  = regexp.MustCompile(`(?:^|[/\\])[^\s/\\]+\.go:[0-9]+(?::[0-9]+)?:`)
	secretKey     = regexp.MustCompile(`(?i)password|passwd|secret|token|credential|authorization|cookie|api_?key|private_?key`)
	credentialURL = regexp.MustCompile(`(?i)\b[a-z][a-z0-9+.-]*://[^\s]+`)
	quotedValue   = regexp.MustCompile("\"(?:[^\"\\\\]|\\\\.)*\"|'[^']*'|`[^`]*`")
	absolutePath  = regexp.MustCompile(`(^|[\s(=])(?:/[^\s:"'` + "`" + `]+)+`)
	secretPair    = regexp.MustCompile(`(?i)(password|passwd|secret|token|credential|authorization|cookie|api_?key)\s*[:=]\s*[^\s,;]+`)
	ansiEscape    = regexp.MustCompile("\x1b\\[[0-?]*[ -/]*[@-~]")
)

// Summary preserves compiler/module/network causes while excluding arbitrary
// child output, quoted literals, credentials and absolute local paths. Input
// captures are already bounded; output has independent line and byte limits.
func Summary(stdout, stderr []byte, environment []string) string {
	var secrets []string
	for key, value := range environmentValues(environment) {
		if value != "" && secretKey.MatchString(key) {
			secrets = append(secrets, value)
		}
	}
	sort.Slice(secrets, func(i, j int) bool { return len(secrets[i]) > len(secrets[j]) })
	var result strings.Builder
	seen := make(map[string]bool)
	lines := 0
	for _, input := range [][]byte{stderr, stdout} {
		scanner := bufio.NewScanner(bytes.NewReader(input))
		scanner.Buffer(make([]byte, 4096), 128<<10)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "go: ") && !compilerLine.MatchString(line) &&
				!strings.Contains(line, "fatal error:") && !strings.Contains(line, "executable file not found") {
				continue
			}
			for _, secret := range secrets {
				line = strings.ReplaceAll(line, secret, "<redacted>")
			}
			line = ansiEscape.ReplaceAllString(line, "")
			line = credentialURL.ReplaceAllString(line, "<url>")
			line = quotedValue.ReplaceAllString(line, "<quoted>")
			line = absolutePath.ReplaceAllString(line, "${1}<path>")
			line = secretPair.ReplaceAllString(line, "${1}=<redacted>")
			line = strings.Map(func(character rune) rune {
				if unicode.IsControl(character) {
					return ' '
				}
				return character
			}, line)
			if seen[line] || line == "" {
				continue
			}
			seen[line] = true
			if lines == maxDiagnosticLines || result.Len()+len(line)+1 > MaxDiagnosticBytes {
				if result.Len() == 0 {
					return "Go build failed; oversized diagnostics were redacted"
				}
				return strings.TrimSpace(result.String())
			}
			result.WriteString(line)
			result.WriteByte('\n')
			lines++
		}
	}
	if result.Len() == 0 && len(stdout)+len(stderr) != 0 {
		return "Go build failed; unrecognized diagnostics were redacted"
	}
	return strings.TrimSpace(result.String())
}

// Error carries a sanitized compiler cause through a higher-level typed error.
// Its underlying error is available for cancellation/exit classification only.
type Error struct {
	Cause      error
	Diagnostic string
}

func (failure *Error) Error() string {
	if failure == nil {
		return "Go build failed"
	}
	return "Go build failed: " + failure.Diagnostic
}
func (failure *Error) Unwrap() error {
	if failure == nil {
		return nil
	}
	return failure.Cause
}

func Diagnostic(err error) string {
	var failure *Error
	if errors.As(err, &failure) && failure != nil {
		return failure.Diagnostic
	}
	return ""
}

// Capture drains compiler output while retaining a bounded prefix for Summary.
// Use separate captures for stdout and stderr; each is owned by one pipe.
type Capture struct {
	prefix []byte
	count  int
}

func (capture *Capture) Write(payload []byte) (int, error) {
	capture.count += len(payload)
	const maximum = 128 << 10
	if remaining := maximum - len(capture.prefix); remaining > 0 {
		capture.prefix = append(capture.prefix, payload[:min(remaining, len(payload))]...)
	}
	return len(payload), nil
}

func (capture *Capture) Bytes() []byte { return capture.prefix }
func (capture *Capture) Len() int      { return capture.count }
