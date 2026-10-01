// Package testenv owns deterministic, detached environment preparation for
// test processes. Callers retain database, credential and executable policies.
package testenv

import (
	"maps"
	"runtime"
	"slices"
	"strings"
)

// Map returns mutable caller-owned values. Malformed entries are ignored and
// duplicate keys use the last value, matching os/exec's effective environment.
func Map(entries []string) map[string]string {
	values := make(map[string]string, len(entries))
	var names map[string]string
	if runtime.GOOS == "windows" {
		names = make(map[string]string, len(entries))
	}
	for _, entry := range entries {
		if key, value, ok := strings.Cut(entry, "="); ok && key != "" {
			if names != nil {
				folded := strings.ToLower(key)
				delete(values, names[folded])
				names[folded] = key
			}
			values[key] = value
		}
	}
	return values
}

func Sorted(values map[string]string) []string {
	keys := slices.Sorted(maps.Keys(values))
	result := make([]string, len(keys))
	for index, key := range keys {
		result[index] = key + "=" + values[key]
	}
	return result
}

func With(base []string, overrides map[string]string) []string {
	entries := make([]string, 0, len(base)+len(overrides))
	entries = append(entries, base...)
	entries = append(entries, Sorted(overrides)...)
	return Sorted(Map(entries))
}

func Without(base []string, keys ...string) []string {
	values := Map(base)
	for _, key := range keys {
		if runtime.GOOS == "windows" {
			folded := strings.ToLower(key)
			for name := range values {
				if strings.ToLower(name) == folded {
					delete(values, name)
				}
			}
		} else {
			delete(values, key)
		}
	}
	return Sorted(values)
}

// OfflineGo uses the already-downloaded module graph and installed toolchain.
// The execution owner supplies flags (including -race) and retains GOOS, GOARCH,
// CGO_ENABLED and cache directories. Host goenv, overlays, external cache
// programs and private-module network bypasses cannot change this profile.
func OfflineGo(base []string, flags string) []string {
	return With(base, map[string]string{
		"GOENV": "off", "GOFLAGS": flags, "GOCACHEPROG": "",
		"GOWORK": "off", "GOTOOLCHAIN": "local",
		"GOPROXY": "off", "GOSUMDB": "off", "GONOPROXY": "none",
	})
}
