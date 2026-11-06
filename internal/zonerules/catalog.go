// Package zonerules owns the immutable IANA database used by GoDj. Lookup is
// memory-only; process environment, host files and time.Local are not inputs.
package zonerules

import (
	"archive/zip"
	"crypto/sha256"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"strings"
	"sync"
	"time"
)

const (
	Version          = "2025c"
	SHA256           = "8f55634d05f8bca1f7bc7c69c5933428c69357e0bdf565e5ba224e3f88ff12e8"
	MaximumNameBytes = 255
	maximumZoneBytes = 64 << 10
	maximumDataBytes = 8 << 20
	maximumZones     = 4096
)

var (
	ErrInvalid     = errors.New("time zone must name an entry in the pinned IANA database")
	ErrUnavailable = errors.New("pinned IANA time zone data is unavailable")
)

//go:embed zoneinfo.zip
var embeddedData string

type zoneEntry struct {
	file      *zip.File
	once      sync.Once
	location  *time.Location
	err       error
	rulesOnce sync.Once
	rules     *ruleData
	rulesErr  error
}

type catalog struct {
	entries map[string]*zoneEntry
	names   []string
}

// The catalog is published once. Its map and names never change; each entry
// resolves at most once and retains either one read-only location or an error.
var database = sync.OnceValues(func() (*catalog, error) {
	return readCatalog(embeddedData)
})

func readCatalog(data string) (*catalog, error) {
	if len(data) == 0 || len(data) > maximumDataBytes {
		return nil, fmt.Errorf("%w: archive exceeds its size budget", ErrUnavailable)
	}
	if fmt.Sprintf("%x", sha256.Sum256([]byte(data))) != SHA256 {
		return nil, fmt.Errorf("%w: archive checksum differs from its pinned source", ErrUnavailable)
	}
	archive, err := zip.NewReader(strings.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if len(archive.File) == 0 || len(archive.File) > maximumZones {
		return nil, fmt.Errorf("%w: invalid archive entry count", ErrUnavailable)
	}
	result := &catalog{entries: make(map[string]*zoneEntry, len(archive.File))}
	for _, file := range archive.File {
		name := file.Name
		if name == "" || len(name) > MaximumNameBytes || name == "." || name == ".." ||
			path.IsAbs(name) || path.Clean(name) != name || strings.HasPrefix(name, "../") ||
			strings.ContainsAny(name, "\\\x00") || file.Mode()&fs.ModeType != 0 ||
			file.UncompressedSize64 == 0 || file.UncompressedSize64 > maximumZoneBytes {
			return nil, fmt.Errorf("%w: invalid archive entry", ErrUnavailable)
		}
		if _, duplicate := result.entries[name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate archive entry", ErrUnavailable)
		}
		result.entries[name] = &zoneEntry{file: file}
		result.names = append(result.names, name)
	}
	if _, present := result.entries["UTC"]; !present {
		return nil, fmt.Errorf("%w: UTC entry is missing", ErrUnavailable)
	}
	slices.Sort(result.names)
	return result, nil
}

// Borrow returns a package-owned read-only location. Internal callers must not
// modify it or expose its pointer. Public APIs return a location copy instead.
func Borrow(name string) (*time.Location, error) {
	if name == "" || len(name) > MaximumNameBytes {
		return nil, ErrInvalid
	}
	data, err := database()
	if err != nil {
		return nil, err
	}
	entry, present := data.entries[name]
	if !present {
		return nil, ErrInvalid
	}
	entry.once.Do(func() {
		raw, err := entry.read()
		if err != nil {
			entry.err = err
			return
		}
		entry.location, err = time.LoadLocationFromTZData(name, raw)
		if err != nil {
			entry.err = errors.Join(ErrUnavailable, err)
		}
	})
	return entry.location, entry.err
}

func (entry *zoneEntry) read() ([]byte, error) {
	stream, err := entry.file.Open()
	if err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(stream, maximumZoneBytes+1))
	closeErr := stream.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, errors.Join(ErrUnavailable, err)
	}
	if len(raw) != int(entry.file.UncompressedSize64) || len(raw) > maximumZoneBytes {
		return nil, fmt.Errorf("%w: incomplete zone data", ErrUnavailable)
	}
	return raw, nil
}

// Names returns an owned, sorted list, including the aliases in the pinned data.
func Names() ([]string, error) {
	data, err := database()
	if err != nil {
		return nil, err
	}
	return slices.Clone(data.names), nil
}
