// Package version identifies the GoDj code linked into the current executable.
// It reads compiler build metadata only; it does not inspect the working
// directory, query Git, load project settings, or contact a module registry.
package version

import (
	"runtime/debug"
	"strings"
)

const Module = "github.com/progresshans/godj"

// Origin describes how the executable obtained its framework code. A local
// replacement has no known module version, even when go.mod requires one.
type Origin string

const (
	Unknown           Origin = "unknown"
	ModuleVersion     Origin = "module"
	Development       Origin = "development"
	LocalReplacement  Origin = "local_replacement"
	ModuleReplacement Origin = "module_replacement"
)

// Info is a caller-owned value. Version identifies the effective module, and
// RequestedVersion is populated only for replacements. ReplacementModule is
// populated only for a versioned module replacement; local paths are omitted.
// Revision and Modified describe GoDj only when it is the main module. Build
// settings from a consuming project's main module never identify a dependency.
// GoVersion describes the executable's compiler, not a framework requirement.
// Missing metadata stays unknown. This information is not a compatibility or
// source-integrity attestation.
type Info struct {
	Module            string `json:"module"`
	Origin            Origin `json:"origin"`
	Version           string `json:"version,omitempty"`
	RequestedVersion  string `json:"requested_version,omitempty"`
	ReplacementModule string `json:"replacement_module,omitempty"`
	GoVersion         string `json:"go_version,omitempty"`
	Revision          string `json:"revision,omitempty"`
	Modified          bool   `json:"modified,omitempty"`
}

// Current reports the framework linked into this executable, which may differ
// from a separately installed godj tool. No global overrides or caches exist.
func Current() Info {
	build, ok := debug.ReadBuildInfo()
	if !ok {
		return Info{Module: Module, Origin: Unknown}
	}
	return fromBuildInfo(build)
}

func fromBuildInfo(build *debug.BuildInfo) Info {
	result := Info{Module: Module, Origin: Unknown}
	if build == nil {
		return result
	}
	result.GoVersion = token(build.GoVersion)
	var framework *debug.Module
	main := build.Main.Path == Module
	if main {
		framework = &build.Main
	} else {
		for _, dependency := range build.Deps {
			if dependency != nil && dependency.Path == Module {
				if framework != nil {
					return result // Ambiguous metadata cannot select an identity.
				}
				framework = dependency
			}
		}
	}
	if framework == nil {
		return result
	}
	if replacement := framework.Replace; replacement != nil {
		result.RequestedVersion = moduleVersion(framework.Version)
		if replacement.Version == "" || replacement.Version == "(devel)" {
			result.Origin = LocalReplacement
			return result
		}
		version := moduleVersion(replacement.Version)
		path := modulePath(replacement.Path)
		if version != "" && path != "" {
			result.Origin = ModuleReplacement
			result.Version = version
			result.ReplacementModule = path
		}
		return result
	}
	result.Version = moduleVersion(framework.Version)
	if result.Version != "" {
		result.Origin = ModuleVersion
	} else if framework.Version == "" || framework.Version == "(devel)" {
		result.Origin = Development
	}
	if main && result.Origin != Unknown {
		// A dependency, including a workspace module, has no independently
		// stamped VCS revision in debug.BuildInfo. Do not borrow the app's.
		for _, setting := range build.Settings {
			switch setting.Key {
			case "vcs.revision":
				result.Revision = token(setting.Value)
			case "vcs.modified":
				result.Modified = setting.Value == "true"
			}
		}
	}
	return result
}

// Keep only bounded display tokens. Build flags, checksums, source paths, and
// the consuming executable's module identity are never copied into Info.
func token(input string) string {
	if len(input) == 0 || len(input) > 256 {
		return ""
	}
	for _, character := range input {
		if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune(".-+", character)) {
			return ""
		}
	}
	return input
}

func moduleVersion(input string) string {
	if len(input) < 2 || input[0] != 'v' || input[1] < '0' || input[1] > '9' {
		return ""
	}
	return token(input)
}

func modulePath(input string) string {
	if len(input) == 0 || len(input) > 1024 || strings.HasPrefix(input, "/") ||
		strings.HasPrefix(input, ".") || strings.HasSuffix(input, "/") {
		return ""
	}
	for _, component := range strings.Split(input, "/") {
		if component == "" || component == "." || component == ".." {
			return ""
		}
		for _, character := range component {
			if !(character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
				character >= '0' && character <= '9' || strings.ContainsRune(".-_~", character)) {
				return ""
			}
		}
	}
	return input
}
