package passwordpolicy

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	passwordreference "github.com/progresshans/godj/auth/passwordpolicy/testdata"
	"github.com/progresshans/godj/validation"
)

func readPasswordReference(t *testing.T) (passwordreference.Corpus, passwordreference.Reference) {
	t.Helper()
	inputs, reference, err := passwordreference.Load()
	if err != nil {
		t.Fatal(err)
	}
	dictionaryHash := sha256.Sum256(commonPasswordData)
	if reference.DictionarySHA256 != hex.EncodeToString(dictionaryHash[:]) {
		t.Fatal("password reference dictionary source mismatch")
	}
	return inputs, reference
}

func referenceAttributes(input passwordreference.Case) []Attribute {
	original := input.ProfileAttributes()
	attributes := make([]Attribute, len(original))
	for i, value := range original {
		attributes[i] = Attribute{Label: value.Label, Value: value.Value}
	}
	return attributes
}

func passwordDiagnostics(errors validation.Errors) []passwordreference.Diagnostic {
	result := []passwordreference.Diagnostic{}
	for _, item := range errors.All() {
		parameters := map[string]string{}
		for _, parameter := range item.Params() {
			parameters[parameter.Key()] = parameter.Value()
		}
		result = append(result, passwordreference.Diagnostic{Code: string(item.Code()), Params: parameters})
	}
	return result
}

func TestPasswordValidatorsMatchPinnedDjango(t *testing.T) {
	inputs, reference := readPasswordReference(t)
	for index, input := range inputs.Cases {
		t.Run(input.Name, func(t *testing.T) {
			similarity, err := NewSimilarityValidator(input.MaximumSimilarity)
			if err != nil {
				t.Fatal(err)
			}
			minimum, err := NewMinimumLengthValidator(input.Minimum)
			if err != nil {
				t.Fatal(err)
			}
			common, err := NewCommonPasswordValidator(input.Dictionary)
			if err != nil {
				t.Fatal(err)
			}
			validators := []Validator{similarity, minimum, common, NumericPasswordValidator{}}
			names := []string{"similarity", "minimum", "common", "numeric"}
			want := reference.Cases[index]
			if want.Name != input.Name {
				t.Fatal("reference case ordering mismatch")
			}
			attributes := referenceAttributes(input)
			var combined []validation.Errors
			for i, validator := range validators {
				diagnostics := validator.ValidatePassword(input.Password, attributes)
				combined = append(combined, diagnostics)
				got := passwordDiagnostics(diagnostics)
				if !reflect.DeepEqual(got, want.Errors[names[i]]) {
					t.Fatalf("%s: got %+v, reference %+v", names[i], got, want.Errors[names[i]])
				}
			}
			if got := passwordDiagnostics(validation.Join(combined...)); !reflect.DeepEqual(got, want.Errors["combined"]) {
				t.Fatalf("combined policy: got %+v, reference %+v", got, want.Errors["combined"])
			}
		})
	}
}

func TestPasswordSimilarityMatrixAndCompleteCommonDictionary(t *testing.T) {
	inputs, reference := readPasswordReference(t)
	matrix := sha256.New()
	count := 0
	var frame [13]byte
	for pi, password := range inputs.MatrixValues {
		for vi, value := range inputs.MatrixValues {
			for ti, threshold := range inputs.MatrixThresholds {
				validator, err := NewSimilarityValidator(threshold)
				if err != nil {
					t.Fatal(err)
				}
				binary.BigEndian.PutUint32(frame[:4], uint32(pi))
				binary.BigEndian.PutUint32(frame[4:8], uint32(vi))
				binary.BigEndian.PutUint32(frame[8:12], uint32(ti))
				frame[12] = 0
				if !validator.ValidatePassword(password, []Attribute{{Label: "username", Value: value}}).Empty() {
					frame[12] = 1
				}
				matrix.Write(frame[:])
				count++
			}
		}
	}
	if count != 16245 || count != reference.MatrixCases || hex.EncodeToString(matrix.Sum(nil)) != reference.MatrixSHA256 {
		t.Fatal("similarity decision matrix differs from Django", count, hex.EncodeToString(matrix.Sum(nil)))
	}
	reader, err := gzip.NewReader(bytes.NewReader(commonPasswordData))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(reader)
	closed := reader.Close()
	if err != nil || closed != nil {
		t.Fatal(err, closed)
	}
	words := strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
	slices.Sort(words)
	if len(words) != reference.DictionaryWords || len(words) != 19640 {
		t.Fatal("dictionary coverage incomplete")
	}
	validator, err := NewCommonPasswordValidator(nil)
	if err != nil {
		t.Fatal(err)
	}
	membership := sha256.New()
	count = 0
	for _, word := range words {
		upper := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' {
				return r - 32
			}
			return r
		}, word)
		for _, password := range []string{word, upper, "\x1c " + upper + "\u2003"} {
			binary.BigEndian.PutUint32(frame[:4], uint32(len(password)))
			membership.Write(frame[:4])
			membership.Write([]byte(password))
			frame[0] = 0
			if !validator.ValidatePassword(password, nil).Empty() {
				frame[0] = 1
			}
			membership.Write(frame[:1])
			count++
		}
	}
	if count != reference.DictionaryChecks || hex.EncodeToString(membership.Sum(nil)) != reference.DictionaryObserved {
		t.Fatal("complete dictionary behavior differs from Django", count)
	}
	t.Logf("%d similarity cases and %d common-password variants match pinned Django", reference.MatrixCases, count)
}

func TestPasswordValidatorConfigurationOwnershipAndConcurrency(t *testing.T) {
	for _, threshold := range []float64{-1, .099, math.NaN(), math.Inf(1)} {
		if validator, err := NewSimilarityValidator(threshold); err != ErrInvalidConfig || validator != nil {
			t.Fatal("invalid similarity configuration published", err)
		}
	}
	if validator, err := NewMinimumLengthValidator(-1); err != ErrInvalidConfig || validator != nil {
		t.Fatal("negative minimum published")
	}
	if validator, err := NewCommonPasswordValidator([]string{"\xff"}); err != ErrInvalidConfig || validator != nil {
		t.Fatal("invalid dictionary published")
	}
	words := []string{"private-dictionary-marker"}
	common, err := NewCommonPasswordValidator(words)
	if err != nil {
		t.Fatal(err)
	}
	words[0] = "changed"
	similarity, err := NewSimilarityValidator(0)
	if err != nil {
		t.Fatal(err)
	}
	attributes := []Attribute{{Label: "first name", Value: "abcdefgh"}}
	if common.ValidatePassword("PRIVATE-DICTIONARY-MARKER", nil).Empty() || similarity.ValidatePassword("abcdefgh", attributes).Empty() {
		t.Fatal("common rule lost its owned dictionary or declared attribute")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, common), "private-dictionary-marker") {
			t.Fatal("dictionary appeared in diagnostics")
		}
	}
	defaults, err := Default()
	if err != nil || len(defaults) != 4 {
		t.Fatal("default construction", err)
	}
	copyDefaults, err := Default()
	if err != nil {
		t.Fatal(err)
	}
	copyDefaults[0] = nil
	if defaults[0] == nil {
		t.Fatal("default policy slice aliased")
	}
	for _, validator := range defaults {
		if validator.ValidatePassword("\xff", nil).Empty() {
			t.Fatal("malformed UTF-8 accepted")
		}
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 50 {
				if common.ValidatePassword("PRIVATE-DICTIONARY-MARKER", nil).Empty() || similarity.ValidatePassword("abcdefgh", attributes).Empty() {
					t.Error("concurrent immutable policy changed")
				}
				for _, validator := range defaults {
					validator.ValidatePassword("strong synthetic passphrase!", []Attribute{{Label: "username", Value: "member"}})
				}
			}
		})
	}
	group.Wait()
}
