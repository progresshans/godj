package storage

import (
	"encoding/base32"
	"io"
	"io/fs"
	"math"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"
)

const stagingDirectory = ".godj-staging"
const MaximumNameLength = 1024

type Limits struct {
	MaxFileBytes               int64
	MaxNameLength, MaxAttempts int
}

func DefaultLimits() Limits {
	return Limits{MaxFileBytes: 32 << 20, MaxNameLength: MaximumNameLength, MaxAttempts: 64}
}
func normalizeLimits(l Limits) (Limits, error) {
	defaults := DefaultLimits()
	if l.MaxFileBytes == 0 {
		l.MaxFileBytes = defaults.MaxFileBytes
	}
	if l.MaxNameLength == 0 {
		l.MaxNameLength = defaults.MaxNameLength
	}
	if l.MaxAttempts == 0 {
		l.MaxAttempts = defaults.MaxAttempts
	}
	if l.MaxFileBytes < 1 || l.MaxFileBytes == math.MaxInt64 || l.MaxNameLength < 1 || l.MaxNameLength > MaximumNameLength || l.MaxAttempts < 1 || l.MaxAttempts > 1024 {
		return Limits{}, &Error{Code: "invalid_limits", Outcome: NotPublished}
	}
	return l, nil
}
func validateName(name string) error {
	invalid := !utf8.ValidString(name) || !fs.ValidPath(name) || name == "." || utf8.RuneCountInString(name) > MaximumNameLength || strings.ContainsAny(name, `\:<>"|?*`) || strings.ContainsFunc(name, unicode.IsControl)
	for _, component := range strings.Split(name, "/") {
		stem := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		stemRunes := []rune(stem)
		invalid = invalid || strings.EqualFold(component, stagingDirectory) || strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") || len(component) > 255 || stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || stem == "CONIN$" || stem == "CONOUT$" || len(stemRunes) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && strings.ContainsRune("123456789¹²³", stemRunes[3])
	}
	if invalid {
		return &Error{Code: "invalid_name", Outcome: NotPublished}
	}
	return nil
}
func nameLimit(l Limits, options SaveOptions) (int, error) {
	if options.MaxLength < 0 {
		return 0, &Error{Code: "invalid_name_limit", Outcome: NotPublished}
	}
	if options.MaxLength == 0 {
		return l.MaxNameLength, nil
	}
	return min(l.MaxNameLength, options.MaxLength), nil
}

// Preserve compound suffixes like Django's PurePath.suffixes. The random
// spelling is not a contract; it remains seven alphanumeric characters.
func alternativeName(name string, limit int, random io.Reader) (string, error) {
	directory, base := path.Split(name)
	root, ext := base, ""
	offset := len(base) - len(strings.TrimLeft(base, "."))
	if dot := strings.IndexByte(base[offset:], '.'); dot >= 0 {
		root, ext = base[:offset+dot], base[offset+dot:]
	}
	available := limit - utf8.RuneCountInString(directory+ext) - 8
	runes := []rune(root)
	if available < 1 {
		return "", &Error{Code: "name_too_long", Outcome: NotPublished}
	}
	if len(runes) > available {
		root = string(runes[:available])
	}
	var entropy [5]byte
	if _, err := io.ReadFull(random, entropy[:]); err != nil {
		return "", &Error{Code: "entropy_failed", Outcome: NotPublished, Cause: err}
	}
	suffix := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(entropy[:])[:7]
	result := directory + root + "_" + suffix + ext
	if err := validateName(result); err != nil {
		return "", err
	}
	return result, nil
}
