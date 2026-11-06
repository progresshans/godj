// Package passwordpolicy provides model-independent, pinned password-strength rules.
// Callers select profile attributes explicitly and keep password bytes unchanged.
package passwordpolicy

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

// ErrInvalidConfig never includes dictionary entries or profile values.
var ErrInvalidConfig = errors.New("password policy configuration is invalid")

// Attribute contains one ephemeral public profile value and a static display
// label chosen by the host, never an encoded password or principal identity.
// Validation does not retain the slice or its values.
type Attribute struct{ Label, Value string }

func (Attribute) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("passwordpolicy.Attribute{redacted}"))
}
func (Attribute) MarshalJSON() ([]byte, error) {
	return []byte(`"passwordpolicy.Attribute{redacted}"`), nil
}

// Validator is immutable and safe for concurrent validation. Diagnostics never
// include a password, dictionary entry or submitted attribute value. A label
// is declaration metadata; callers must not put user input in it.
type Validator interface {
	ValidatePassword(string, []Attribute) validation.Errors
}

type minimumLengthValidator struct{ minimum int }

func NewMinimumLengthValidator(minimum int) (Validator, error) {
	if minimum < 0 {
		return nil, ErrInvalidConfig
	}
	return minimumLengthValidator{minimum}, nil
}

func (v minimumLengthValidator) ValidatePassword(password string, _ []Attribute) validation.Errors {
	if !utf8.ValidString(password) {
		return passwordViolation("invalid")
	}
	if utf8.RuneCountInString(password) < v.minimum {
		return passwordViolation("password_too_short", validation.NewParam("min_length", strconv.Itoa(v.minimum)))
	}
	return validation.Errors{}
}

// NumericPasswordValidator includes Unicode digit values such as superscripts;
// it does not treat every numeric character (for example fractions) as a digit.
type NumericPasswordValidator struct{}

func (NumericPasswordValidator) ValidatePassword(password string, _ []Attribute) validation.Errors {
	if !utf8.ValidString(password) {
		return passwordViolation("invalid")
	}
	if password == "" {
		return validation.Errors{}
	}
	for _, r := range password {
		if !unicode16.IsDigit(r) {
			return validation.Errors{}
		}
	}
	return passwordViolation("password_entirely_numeric")
}

type attributeSimilarityValidator struct{ maximum float64 }

// NewSimilarityValidator compares the supplied attributes in their order.
// Zero selects 0.7. Non-finite thresholds and values below 0.1 are invalid;
// values above 1 retain Django's behavior of accepting every similarity.
func NewSimilarityValidator(maximum float64) (Validator, error) {
	if maximum == 0 {
		maximum = 0.7
	}
	if maximum < 0.1 || math.IsNaN(maximum) || math.IsInf(maximum, 0) {
		return nil, ErrInvalidConfig
	}
	return attributeSimilarityValidator{maximum}, nil
}

func (v attributeSimilarityValidator) ValidatePassword(password string, attributes []Attribute) validation.Errors {
	if !utf8.ValidString(password) {
		return passwordViolation("invalid")
	}
	lower := unicode16.Lower(password)
	counts := make(map[rune]int)
	length := 0
	for _, r := range lower {
		counts[r]++
		length++
	}
	for _, attribute := range attributes {
		value, label := attribute.Value, attribute.Label
		if !utf8.ValidString(value) {
			return passwordViolation("invalid")
		}
		if value == "" {
			continue
		}
		value = unicode16.Lower(value)
		// Python re.split(\W+) retains an empty part at either end. Keeping
		// these matters when both the password and a boundary part are empty.
		parts := passwordAttributeParts(value)
		parts = append(parts, value)
		for _, part := range parts {
			partLength := utf8.RuneCountInString(part)
			if length/10 >= partLength && float64(partLength) < v.maximum/2*float64(length) {
				continue
			}
			// SequenceMatcher.quick_ratio is multiset overlap, independent of
			// order or autojunk. It is not edit distance or the full ratio().
			matched := 0
			used := make(map[rune]int)
			for _, r := range part {
				if used[r] < counts[r] {
					matched++
				}
				used[r]++
			}
			ratio := 1.0
			if total := length + partLength; total != 0 {
				ratio = 2.0 * float64(matched) / float64(total)
			}
			if ratio >= v.maximum {
				return passwordViolation("password_too_similar", validation.NewParam("verbose_name", label))
			}
		}
	}
	return validation.Errors{}
}

func passwordAttributeParts(value string) []string {
	parts := []string{}
	start, separating := 0, false
	for index, r := range value {
		if r == '_' || unicode16.IsAlphanumeric(r) {
			separating = false
		} else {
			if !separating {
				parts = append(parts, value[start:index])
			}
			start, separating = index+utf8.RuneLen(r), true
		}
	}
	return append(parts, value[start:])
}

//go:embed data/common-passwords.txt.gz
var commonPasswordData []byte

var defaultCommonPasswords = sync.OnceValues(func() (map[string]struct{}, error) { return loadDefaultCommonPasswords(commonPasswordData) })

func loadDefaultCommonPasswords(document []byte) (map[string]struct{}, error) {
	digest := sha256.Sum256(document)
	if hex.EncodeToString(digest[:]) != "3c1baed62596de36860824eb3f436d5932d37ca8b06e59df78f5a44ec175afe4" {
		return nil, ErrInvalidConfig
	}
	reader, err := gzip.NewReader(bytes.NewReader(document))
	if err != nil {
		return nil, ErrInvalidConfig
	}
	data, err := io.ReadAll(io.LimitReader(reader, 162385))
	closed := reader.Close()
	if err != nil || closed != nil || len(data) != 162384 {
		return nil, ErrInvalidConfig
	}
	// The upstream file terminates every entry with a newline. Do not invent
	// an extra empty entry from its final terminator.
	words, err := passwordDictionary(strings.Split(strings.TrimSuffix(string(data), "\n"), "\n"))
	if err != nil || len(words) != 19640 {
		return nil, ErrInvalidConfig
	}
	return words, nil
}

type commonPasswordValidator struct{ words map[string]struct{} }

// NewCommonPasswordValidator snapshots a caller-owned dictionary. Nil selects
// the pinned Django list; an explicit empty slice selects an empty dictionary.
// Entries are stripped, not lowercased, matching Django's custom list contract.
// Loading files belongs to the caller, outside request validation and DB scopes.
func NewCommonPasswordValidator(words []string) (Validator, error) {
	var dictionary map[string]struct{}
	var err error
	if words == nil {
		dictionary, err = defaultCommonPasswords()
	} else {
		dictionary, err = passwordDictionary(words)
	}
	if err != nil {
		return nil, err
	}
	return commonPasswordValidator{dictionary}, nil
}

func passwordDictionary(words []string) (map[string]struct{}, error) {
	// Explicit resource bounds apply only to supplied configuration. The
	// password itself remains subject to the caller/hasher's input envelope.
	if len(words) > 1_000_000 {
		return nil, ErrInvalidConfig
	}
	dictionary := make(map[string]struct{}, len(words))
	bytes := 0
	for _, word := range words {
		if len(word) > 16*1024*1024-bytes || !utf8.ValidString(word) {
			return nil, ErrInvalidConfig
		}
		bytes += len(word)
		dictionary[unicode16.TrimSpace(word)] = struct{}{}
	}
	return dictionary, nil
}

func (v commonPasswordValidator) ValidatePassword(password string, _ []Attribute) validation.Errors {
	if !utf8.ValidString(password) {
		return passwordViolation("invalid")
	}
	if _, found := v.words[unicode16.TrimSpace(unicode16.Lower(password))]; found {
		return passwordViolation("password_too_common")
	}
	return validation.Errors{}
}

// Default returns Django startproject's four policies in
// their declared order. It does not install them globally. Each returned slice
// is independent; shared default dictionary state is immutable.
func Default() ([]Validator, error) {
	similarity, err := NewSimilarityValidator(0)
	if err != nil {
		return nil, err
	}
	minimum, err := NewMinimumLengthValidator(8)
	if err != nil {
		return nil, err
	}
	common, err := NewCommonPasswordValidator(nil)
	if err != nil {
		return nil, err
	}
	return []Validator{similarity, minimum, common, NumericPasswordValidator{}}, nil
}

func passwordViolation(code validation.Code, params ...validation.Param) validation.Errors {
	return validation.NewErrors(validation.New("password", code, params...))
}

// Dictionary contents and options are configuration, never diagnostics.
func (minimumLengthValidator) String() string         { return "passwordpolicy.Validator{redacted}" }
func (attributeSimilarityValidator) String() string   { return "passwordpolicy.Validator{redacted}" }
func (commonPasswordValidator) String() string        { return "passwordpolicy.Validator{redacted}" }
func (minimumLengthValidator) GoString() string       { return "passwordpolicy.Validator{redacted}" }
func (attributeSimilarityValidator) GoString() string { return "passwordpolicy.Validator{redacted}" }
func (commonPasswordValidator) GoString() string      { return "passwordpolicy.Validator{redacted}" }
