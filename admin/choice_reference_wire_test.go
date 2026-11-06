package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"
)

const adminChoiceDjangoCommit = "fe0a859f537d4238cf49fca39073513206f83122"
const adminChoiceInputPath = "../conformance/runners/django/admin_choice_fixture/input.json"

type adminChoiceActor struct {
	Source []string `json:"source_permissions"`
	Target []string `json:"target_permissions"`
	Tenant string   `json:"tenant"`
}
type adminChoiceCase struct {
	Name     string           `json:"name"`
	Kind     string           `json:"kind"`
	Relation string           `json:"relation"`
	Actor    adminChoiceActor `json:"actor"`
	Input    json.RawMessage  `json:"input"`
	Setup    struct {
		Revoke []string `json:"revoke_keys"`
	} `json:"setup"`
	Comparison string `json:"comparison"`
	Reason     string `json:"comparison_reason"`
}
type adminChoiceTarget struct {
	Key     string `json:"key"`
	Tenant  string `json:"tenant"`
	Label   string `json:"label"`
	Enabled bool   `json:"enabled"`
}
type adminChoiceSource struct {
	Key     string   `json:"key"`
	Tenant  string   `json:"tenant"`
	Foreign string   `json:"foreign_key"`
	Single  string   `json:"one_to_one"`
	Many    []string `json:"many"`
}
type adminChoiceInputs struct {
	Kind      string `json:"kind"`
	Version   int    `json:"schema_version"`
	Relations []struct {
		Name, Kind string
		Required   bool
	} `json:"relations"`
	Targets []adminChoiceTarget `json:"targets"`
	Source  adminChoiceSource   `json:"source"`
	Cases   []adminChoiceCase   `json:"cases"`
}
type adminChoiceItem struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}
type adminChoiceOption struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Selected bool   `json:"selected"`
}
type adminChoiceSearchInput struct {
	Term string `json:"term"`
	Page string `json:"page"`
}
type adminChoiceFormInput struct {
	Bound bool                `json:"bound"`
	Mode  string              `json:"mode"`
	Data  map[string][]string `json:"data"`
}
type adminChoiceSearchResult struct {
	Status int               `json:"status"`
	Items  []adminChoiceItem `json:"items"`
	More   *bool             `json:"more"`
	Native json.RawMessage   `json:"native"`
}
type adminChoiceFormResult struct {
	Fields     []string                   `json:"fields"`
	Widget     *string                    `json:"widget"`
	Options    []adminChoiceOption        `json:"options"`
	Selected   []adminChoiceItem          `json:"selected"`
	HTML       string                     `json:"rendered_html"`
	Valid      *bool                      `json:"valid"`
	Codes      map[string][]string        `json:"codes"`
	Cleaned    map[string]json.RawMessage `json:"cleaned"`
	Saved      bool                       `json:"saved"`
	Before     adminChoiceSource          `json:"stored_before"`
	After      adminChoiceSource          `json:"stored_after"`
	RolledBack bool                       `json:"transaction_rolled_back"`
}
type adminChoiceObservation struct {
	adminChoiceCase
	Result json.RawMessage `json:"result"`
}
type adminChoiceReference struct {
	Kind    string `json:"kind"`
	Version int    `json:"schema_version"`
	Backend string `json:"backend"`
	Commit  string `json:"django_commit"`
	Profile struct {
		Python   string  `json:"python"`
		Django   string  `json:"django"`
		Vendor   string  `json:"database_vendor"`
		Database string  `json:"database_version"`
		Psycopg  *string `json:"psycopg"`
	} `json:"profile"`
	ObserverSHA     string                   `json:"observer_sha256"`
	InputSHA        string                   `json:"input_sha256"`
	VerificationSHA string                   `json:"source_verification_sha256"`
	Sources         map[string]string        `json:"sources_sha256"`
	Cases           []adminChoiceObservation `json:"cases"`
	Database        struct {
		TargetSeedSHA    string `json:"target_seed_sha256"`
		TargetFinalSHA   string `json:"target_final_sha256"`
		SourceInitialSHA string `json:"source_initial_sha256"`
		SourceFinalSHA   string `json:"source_final_sha256"`
		TargetsPreserved bool   `json:"target_seed_preserved"`
		SourceRestored   bool   `json:"source_state_restored"`
		SaveCalls        int    `json:"form_save_calls"`
		Rollbacks        int    `json:"form_rollbacks"`
		Empty            bool   `json:"cleanup_empty"`
		SchemaRemoved    *bool  `json:"schema_removed"`
		FileRemoved      *bool  `json:"sqlite_file_removed"`
	} `json:"database"`
}

func adminChoiceSHA(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func adminChoiceBytes(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Reject duplicate object members before typed decoding; otherwise JSON's
// last-value-wins rule can conceal a foreign classification or numeric ID.
func adminChoiceDecode(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value func() error
	value = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		mark, container := token.(json.Delim)
		if !container {
			return nil
		}
		switch mark {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return errors.New("duplicate JSON member")
				}
				seen[name] = true
				if err := value(); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := value(); err != nil {
					return err
				}
			}
		default:
			return errors.New("unexpected JSON delimiter")
		}
		_, err = decoder.Token()
		return err
	}
	if err := value(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("trailing JSON value")
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}
func adminChoiceKey(key string) bool {
	n, err := strconv.ParseInt(key, 10, 64)
	return err == nil && strconv.FormatInt(n, 10) == key
}
func adminChoiceMembers(raw []byte, names ...string) bool {
	var members map[string]json.RawMessage
	if json.Unmarshal(raw, &members) != nil || len(members) != len(names) {
		return false
	}
	for _, name := range names {
		if _, present := members[name]; !present {
			return false
		}
	}
	return true
}
func adminChoiceSourceValid(row adminChoiceSource) bool {
	if !adminChoiceKey(row.Key) || !adminChoiceKey(row.Tenant) || !adminChoiceKey(row.Foreign) || !adminChoiceKey(row.Single) || row.Many == nil {
		return false
	}
	for _, key := range row.Many {
		if !adminChoiceKey(key) {
			return false
		}
	}
	return true
}
func adminChoiceJSONEqual(left, right any) bool {
	l, err := json.Marshal(left)
	if err != nil {
		return false
	}
	r, err := json.Marshal(right)
	if err != nil {
		return false
	}
	var lv, rv any
	ld, rd := json.NewDecoder(bytes.NewReader(l)), json.NewDecoder(bytes.NewReader(r))
	ld.UseNumber()
	rd.UseNumber()
	return ld.Decode(&lv) == nil && rd.Decode(&rv) == nil && reflect.DeepEqual(lv, rv)
}

func adminChoiceReadReference(raw, inputRaw, observerRaw, verificationRaw []byte, backend string) (adminChoiceReference, adminChoiceInputs, error) {
	var reference adminChoiceReference
	var inputs adminChoiceInputs
	bad := func(message string) (adminChoiceReference, adminChoiceInputs, error) {
		return reference, inputs, errors.New(message)
	}
	if err := adminChoiceDecode(inputRaw, &inputs); err != nil {
		return bad("invalid input: " + err.Error())
	}
	if err := adminChoiceDecode(raw, &reference); err != nil {
		return bad("invalid reference: " + err.Error())
	}
	if inputs.Kind != "django-admin-choice-inputs-v1" || inputs.Version != 1 || len(inputs.Cases) == 0 || len(inputs.Targets) == 0 {
		return bad("input contract missing")
	}
	if reference.Kind != "django-admin-choice-reference-v1" || reference.Version != 1 || reference.Backend != backend || reference.Commit != adminChoiceDjangoCommit ||
		reference.InputSHA != adminChoiceSHA(inputRaw) || reference.ObserverSHA != adminChoiceSHA(observerRaw) || reference.VerificationSHA != adminChoiceSHA(verificationRaw) {
		return bad("foreign source or input")
	}
	var verification struct {
		Kind        string `json:"kind"`
		Commit      string `json:"django_commit"`
		License     string `json:"license"`
		Description string `json:"source_verification"`
		Sources     map[string]struct {
			Bytes int    `json:"bytes"`
			SHA   string `json:"sha256"`
			URL   string `json:"pinned_url"`
		} `json:"sources"`
	}
	if err := adminChoiceDecode(verificationRaw, &verification); err != nil || verification.Commit != reference.Commit || len(verification.Sources) != 4 || len(reference.Sources) != 4 {
		return bad("missing pinned source inventory")
	}
	for name, source := range verification.Sources {
		if reference.Sources[name] != source.SHA {
			return bad("foreign upstream source")
		}
	}
	profile := reference.Profile
	if profile.Python != "3.14.3" || profile.Django != "6.1" || profile.Database == "" {
		return bad("wrong runtime profile")
	}
	if backend == "sqlite" {
		if profile.Vendor != "sqlite" || profile.Psycopg != nil {
			return bad("wrong SQLite profile")
		}
	} else if backend != "postgres" || profile.Vendor != "postgresql" || profile.Psycopg == nil || *profile.Psycopg != "3.3.6" || len(strings.Fields(profile.Database)) == 0 || strings.Fields(profile.Database)[0] != "17.10" {
		return bad("wrong PostgreSQL profile")
	}
	if len(reference.Cases) != len(inputs.Cases) {
		return bad("incomplete case inventory")
	}
	seen, saves, rollbacks := map[string]bool{}, 0, 0
	for i, actual := range reference.Cases {
		declared := inputs.Cases[i]
		if seen[actual.Name] || !adminChoiceJSONEqual(actual.adminChoiceCase, declared) || actual.Reason == "" ||
			!slices.Contains([]string{"shared", "django_native_only"}, actual.Comparison) || !slices.Contains([]string{"foreign_key", "one_to_one", "many"}, actual.Relation) {
			return bad("foreign case or comparison boundary")
		}
		seen[actual.Name] = true
		if actual.Kind == "autocomplete" {
			var result adminChoiceSearchResult
			if err := adminChoiceDecode(actual.Result, &result); err != nil || result.Items == nil || !adminChoiceMembers(actual.Result, "status", "items", "more", "native") {
				return bad("invalid search observation")
			}
			if result.Status == 200 {
				var native struct {
					Results []struct {
						ID   string `json:"id"`
						Text string `json:"text"`
					} `json:"results"`
					Pagination struct {
						More *bool `json:"more"`
					} `json:"pagination"`
				}
				if err := adminChoiceDecode(result.Native, &native); err != nil || native.Results == nil || native.Pagination.More == nil || result.More == nil || *native.Pagination.More != *result.More {
					return bad("invalid native search document")
				}
				items := []adminChoiceItem{}
				for _, item := range native.Results {
					if !adminChoiceKey(item.ID) {
						return bad("numeric or noncanonical native key")
					}
					items = append(items, adminChoiceItem{item.ID, item.Text})
				}
				if !reflect.DeepEqual(items, result.Items) {
					return bad("search normalization changed native data")
				}
			} else if !slices.Contains([]int{403, 404}, result.Status) || result.More != nil || len(result.Items) != 0 || string(result.Native) != "null" {
				return bad("invalid rejected search")
			}
			continue
		}
		if actual.Kind != "form" {
			return bad("unknown observation kind")
		}
		var input adminChoiceFormInput
		var result adminChoiceFormResult
		if !adminChoiceMembers(actual.Result, "fields", "widget", "options", "selected", "rendered_html", "valid", "codes", "cleaned", "saved", "stored_before", "stored_after", "transaction_rolled_back") ||
			adminChoiceDecode(actual.Input, &input) != nil || adminChoiceDecode(actual.Result, &result) != nil || (result.Valid != nil) != input.Bound || !result.RolledBack ||
			result.Fields == nil || result.Options == nil || result.Selected == nil || result.Codes == nil || result.Cleaned == nil || !adminChoiceSourceValid(result.Before) || !adminChoiceSourceValid(result.After) {
			return bad("incomplete form observation")
		}
		if !reflect.DeepEqual(result.Fields, []string{}) && !reflect.DeepEqual(result.Fields, []string{actual.Relation}) {
			return bad("foreign form field")
		}
		if (result.Widget == nil) != (len(result.Fields) == 0) || result.Widget != nil && !slices.Contains([]string{"AutocompleteSelect", "AutocompleteSelectMultiple"}, *result.Widget) {
			return bad("invalid widget projection")
		}
		selected := []adminChoiceItem{}
		for _, option := range result.Options {
			if option.Key != "" && !adminChoiceKey(option.Key) {
				return bad("invalid option key")
			}
			if option.Selected && option.Key != "" {
				selected = append(selected, adminChoiceItem{option.Key, option.Label})
			}
		}
		if !reflect.DeepEqual(selected, result.Selected) {
			return bad("selection did not come from rendered options")
		}
		rendered, _, err := adminChoiceOptions(result.HTML, actual.Relation)
		if err != nil || !reflect.DeepEqual(rendered, result.Options) {
			return bad("native HTML differs from rendered options")
		}
		for field, value := range result.Cleaned {
			if !slices.Contains(result.Fields, field) {
				return bad("unprojected cleaned field")
			}
			if field == "many" {
				var keys []string
				if adminChoiceDecode(value, &keys) != nil || keys == nil {
					return bad("nontext collection keys")
				}
				for _, key := range keys {
					if !adminChoiceKey(key) {
						return bad("invalid collection key")
					}
				}
			} else if string(value) != "null" {
				var key string
				if adminChoiceDecode(value, &key) != nil || !adminChoiceKey(key) {
					return bad("nontext relation key")
				}
			}
		}
		if result.Saved {
			if result.Valid == nil || !*result.Valid {
				return bad("save without validation")
			}
			saves++
		}
		rollbacks++
	}
	d := reference.Database
	if !d.Empty || !d.TargetsPreserved || !d.SourceRestored || d.TargetSeedSHA != d.TargetFinalSHA || d.SourceInitialSHA != d.SourceFinalSHA || d.SaveCalls != saves || d.Rollbacks != rollbacks {
		return bad("incomplete database receipt")
	}
	for _, digest := range []string{d.TargetSeedSHA, d.TargetFinalSHA, d.SourceInitialSHA, d.SourceFinalSHA} {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 {
			return bad("invalid database digest")
		}
	}
	if backend == "sqlite" {
		if d.FileRemoved == nil || !*d.FileRemoved || d.SchemaRemoved != nil {
			return bad("SQLite resource remains")
		}
	} else if d.SchemaRemoved == nil || !*d.SchemaRemoved || d.FileRemoved != nil {
		return bad("PostgreSQL resource remains")
	}
	return reference, inputs, nil
}

func adminChoiceReferenceNegatives(t *testing.T, raw, inputs, observer, verification []byte, backend string) {
	t.Helper()
	changes := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing_case", func(v map[string]any) { v["cases"] = v["cases"].([]any)[1:] }},
		{"duplicate_case", func(v map[string]any) { c := v["cases"].([]any); c[1] = c[0] }},
		{"foreign_input", func(v map[string]any) { v["input_sha256"] = fmt.Sprintf("%064d", 0) }},
		{"foreign_observer", func(v map[string]any) { v["observer_sha256"] = fmt.Sprintf("%064d", 0) }},
		{"foreign_upstream", func(v map[string]any) {
			v["sources_sha256"].(map[string]any)["forms/models.py"] = fmt.Sprintf("%064d", 0)
		}},
		{"unknown_classification", func(v map[string]any) { v["cases"].([]any)[0].(map[string]any)["comparison"] = "ignored" }},
		{"false_cleanup", func(v map[string]any) { v["database"].(map[string]any)["cleanup_empty"] = false }},
		{"changed_label", func(v map[string]any) {
			c := v["cases"].([]any)[0].(map[string]any)
			c["result"].(map[string]any)["items"].([]any)[0].(map[string]any)["label"] = "forged"
		}},
		{"numeric_native_key", func(v map[string]any) {
			c := v["cases"].([]any)[0].(map[string]any)
			c["result"].(map[string]any)["native"].(map[string]any)["results"].([]any)[0].(map[string]any)["id"] = float64(1)
		}},
		{"false_rollback", func(v map[string]any) {
			for _, x := range v["cases"].([]any) {
				c := x.(map[string]any)
				if c["kind"] == "form" {
					c["result"].(map[string]any)["transaction_rolled_back"] = false
					return
				}
			}
		}},
	}
	for _, test := range changes {
		t.Run(test.name, func(t *testing.T) {
			var value map[string]any
			if err := json.Unmarshal(raw, &value); err != nil {
				t.Fatal(err)
			}
			test.change(value)
			changed, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := adminChoiceReadReference(changed, inputs, observer, verification, backend); err == nil {
				t.Fatal("corrupted evidence accepted")
			}
		})
	}
	t.Run("duplicate_member", func(t *testing.T) {
		changed := append([]byte(`{"input_sha256":"foreign",`), bytes.TrimSpace(raw)[1:]...)
		if _, _, err := adminChoiceReadReference(changed, inputs, observer, verification, backend); err == nil {
			t.Fatal("duplicate JSON member accepted")
		}
	})
}

func adminChoiceWriteCapture(t *testing.T, backend string, document any) {
	t.Helper()
	directory := os.Getenv("GODJ_ADMIN_CHOICE_CAPTURE_DIR")
	if directory == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required Admin choice capture destination absent")
		}
		return
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(filepath.Join(directory, "godj-"+backend+".json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.Write(append(raw, '\n'))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
}
