package identity

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	_ "embed"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/internal/unicode16"
	"github.com/progresshans/godj/validation"
)

//go:embed testdata/password-inputs.json
var passwordReferenceInputs []byte

//go:embed testdata/password-django61.json
var passwordReferenceData []byte

type passwordDiagnostic struct {
	Code   string            `json:"code"`
	Params map[string]string `json:"params"`
}

type passwordCorpus struct {
	Cases []struct {
		Name, Password    string
		Profile           Profile
		Minimum           int
		MaximumSimilarity float64 `json:"maximum_similarity"`
		Attributes        []string
		Dictionary        []string
	}
	MatrixValues     []string  `json:"matrix_values"`
	MatrixThresholds []float64 `json:"matrix_thresholds"`
}

type passwordReference struct {
	Django, Python, Unicode string
	InputSHA256             string `json:"input_sha256"`
	SourceSHA256            string `json:"source_sha256"`
	DictionarySHA256        string `json:"dictionary_sha256"`
	DictionaryWords         int    `json:"dictionary_words"`
	DictionaryChecks        int    `json:"dictionary_checks"`
	DictionaryObserved      string `json:"dictionary_sha256_observed"`
	MatrixCases             int    `json:"matrix_cases"`
	MatrixSHA256            string `json:"matrix_sha256"`
	Cases                   []struct {
		Name   string
		Errors map[string][]passwordDiagnostic
	}
}

func readPasswordReference(t *testing.T) (passwordCorpus, passwordReference) {
	t.Helper()
	var inputs passwordCorpus
	var reference passwordReference
	if err := json.Unmarshal(passwordReferenceInputs, &inputs); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(passwordReferenceData, &reference); err != nil {
		t.Fatal(err)
	}
	inputHash, dictionaryHash := sha256.Sum256(passwordReferenceInputs), sha256.Sum256(commonPasswordData)
	if reference.Django != "6.1" || reference.Python != "3.14.3" || reference.Unicode != unicode16.Version || reference.InputSHA256 != hex.EncodeToString(inputHash[:]) || reference.DictionarySHA256 != hex.EncodeToString(dictionaryHash[:]) || reference.SourceSHA256 != "eccaf8bc084009e006abc45eade4c08fe753420dc0015a8138b1b95b37282a21" || len(inputs.Cases) != len(reference.Cases) {
		t.Fatal("password reference authority/input/source mismatch")
	}
	return inputs, reference
}

func passwordDiagnostics(errors validation.Errors) []passwordDiagnostic {
	result := []passwordDiagnostic{}
	for _, item := range errors.All() {
		parameters := map[string]string{}
		for _, parameter := range item.Params() {
			parameters[parameter.Key()] = parameter.Value()
		}
		result = append(result, passwordDiagnostic{string(item.Code()), parameters})
	}
	return result
}

func TestPasswordValidatorsMatchPinnedDjango(t *testing.T) {
	inputs, reference := readPasswordReference(t)
	for index, input := range inputs.Cases {
		t.Run(input.Name, func(t *testing.T) {
			similarity, err := NewUserAttributeSimilarityValidator(SimilarityConfig{Attributes: input.Attributes, MaxSimilarity: input.MaximumSimilarity})
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
			validators := []PasswordValidator{similarity, minimum, common, NumericPasswordValidator{}}
			names := []string{"similarity", "minimum", "common", "numeric"}
			want := reference.Cases[index]
			if want.Name != input.Name {
				t.Fatal("reference case ordering mismatch")
			}
			for i, validator := range validators {
				got := passwordDiagnostics(validator.ValidatePassword(input.Password, input.Profile))
				if !reflect.DeepEqual(got, want.Errors[names[i]]) {
					t.Fatalf("%s: got %+v, reference %+v", names[i], got, want.Errors[names[i]])
				}
			}
			manager := &Manager{state: &managerState{passwordValidators: validators}}
			errors, _ := validation.Rejected(manager.validatePassword(t.Context(), input.Password, input.Profile))
			if got := passwordDiagnostics(errors); !reflect.DeepEqual(got, want.Errors["combined"]) {
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
				validator, err := NewUserAttributeSimilarityValidator(SimilarityConfig{MaxSimilarity: threshold})
				if err != nil {
					t.Fatal(err)
				}
				binary.BigEndian.PutUint32(frame[:4], uint32(pi))
				binary.BigEndian.PutUint32(frame[4:8], uint32(vi))
				binary.BigEndian.PutUint32(frame[8:12], uint32(ti))
				frame[12] = 0
				if !validator.ValidatePassword(password, Profile{Username: value}).Empty() {
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
			if !validator.ValidatePassword(password, Profile{}).Empty() {
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
	for _, config := range []SimilarityConfig{{MaxSimilarity: -1}, {MaxSimilarity: .099}, {MaxSimilarity: math.NaN()}, {MaxSimilarity: math.Inf(1)}, {Attributes: []string{"private-dictionary-marker"}}} {
		if validator, err := NewUserAttributeSimilarityValidator(config); err == nil || validator != nil || strings.Contains(fmt.Sprint(err), "private-dictionary-marker") {
			t.Fatal("invalid similarity configuration published or leaked")
		}
	}
	if validator, err := NewMinimumLengthValidator(-1); err == nil || validator != nil {
		t.Fatal("negative minimum published")
	}
	if validator, err := NewCommonPasswordValidator([]string{"\xff"}); err == nil || validator != nil {
		t.Fatal("invalid dictionary published")
	}
	for _, document := range [][]byte{nil, []byte("truncated"), append(append([]byte{}, commonPasswordData...), 0)} {
		if words, err := loadDefaultCommonPasswords(document); err == nil || words != nil {
			t.Fatal("unverified default dictionary published")
		}
	}
	words := []string{"private-dictionary-marker"}
	common, err := NewCommonPasswordValidator(words)
	if err != nil {
		t.Fatal(err)
	}
	words[0] = "changed"
	attributes := []string{"first_name"}
	similarity, err := NewUserAttributeSimilarityValidator(SimilarityConfig{Attributes: attributes})
	if err != nil {
		t.Fatal(err)
	}
	attributes[0] = "username"
	if common.ValidatePassword("PRIVATE-DICTIONARY-MARKER", Profile{}).Empty() || similarity.ValidatePassword("abcdefgh", Profile{FirstName: "abcdefgh"}).Empty() {
		t.Fatal("constructor retained caller slice")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if strings.Contains(fmt.Sprintf(format, common), "private-dictionary-marker") {
			t.Fatal("dictionary appeared in diagnostics")
		}
	}
	defaults, err := DefaultPasswordValidators()
	if err != nil || len(defaults) != 4 {
		t.Fatal("default construction", err)
	}
	copyDefaults, err := DefaultPasswordValidators()
	if err != nil {
		t.Fatal(err)
	}
	copyDefaults[0] = nil
	if defaults[0] == nil {
		t.Fatal("default policy slice aliased")
	}
	for _, validator := range defaults {
		if validator.ValidatePassword("\xff", Profile{}).Empty() {
			t.Fatal("malformed UTF-8 accepted")
		}
	}
	var group sync.WaitGroup
	for range 16 {
		group.Go(func() {
			for range 50 {
				if common.ValidatePassword("PRIVATE-DICTIONARY-MARKER", Profile{}).Empty() || similarity.ValidatePassword("abcdefgh", Profile{FirstName: "abcdefgh"}).Empty() {
					t.Error("concurrent immutable policy changed")
				}
				for _, validator := range defaults {
					validator.ValidatePassword("strong synthetic passphrase!", Profile{Username: "member"})
				}
			}
		})
	}
	group.Wait()
}
