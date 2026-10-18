package codegen_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/progresshans/godj/timezone"
)

type zonedReferenceInput struct {
	Kind             string
	ProviderInstants []string `json:"provider_instants"`
	Operations       []string
	Fixtures         []struct {
		Name, Zone string
		Instants   []*string
	}
	Failures []struct {
		Name, Zone, At, Function string
		Corrupt                  bool
	}
}
type zonedReferenceData struct {
	Kind      string
	IANA      string `json:"iana_version"`
	Names     []string
	NamesHash string `json:"names_sha256"`
	Build     map[string]string
	Archives  map[string]struct {
		SHA256, URL string
		Bytes       int
	}
	Packed struct {
		SHA256 string
		Bytes  int
	} `json:"tzdata_zi"`
}
type zonedBuildReceipt struct {
	Kind     string
	IANA     string `json:"iana_version"`
	Build    map[string]string
	Data     string `json:"data_source_sha256"`
	Prepare  string `json:"prepare_sha256"`
	Archives map[string]string
	Names    string `json:"names_sha256"`
	Files    int
	Packed   string `json:"tzdata_zi_sha256"`
	Binary   string `json:"zic_binary_sha256"`
	Compiler string
	Awk      string
	Complete bool
}
type zonedReferenceDocument struct {
	Kind, Backend string
	Commit        string            `json:"django_commit"`
	Observer      string            `json:"observer_sha256"`
	Shared        string            `json:"shared_observer_sha256"`
	Input         string            `json:"input_sha256"`
	Data          string            `json:"data_source_sha256"`
	Prepare       string            `json:"prepare_sha256"`
	ReceiptHash   string            `json:"build_receipt_sha256"`
	Receipt       zonedBuildReceipt `json:"build_receipt"`
	Sources       string            `json:"source_verification_sha256"`
	Runtime       struct {
		Python, Django, SQLite, PostgreSQL, Psycopg, Timezone, IANA string
		DataProfile                                                 string `json:"data_profile"`
		ZoneInfoHash                                                string `json:"zoneinfo_module_sha256"`
	}
	Unchanged bool `json:"storage_unchanged"`
	Cleaned   bool `json:"storage_cleaned"`
	Provider  []struct {
		Name   string
		Values []struct {
			Local, Error string
			Offset       *int `json:"offset_seconds"`
		}
	}
	Probes []struct {
		Fixture, Zone, Instant string
		Python                 int  `json:"zoneinfo_offset_seconds"`
		Postgres               *int `json:"postgres_offset_seconds"`
	} `json:"native_zone_probes"`
	Cases []struct {
		Name, Zone string
		Unchanged  bool `json:"storage_unchanged"`
		Error      *struct{ Type, Message, Stage, SQLState string }
	}
}

func zonedReferenceSources(t *testing.T) (zonedReferenceInput, zonedReferenceData, map[string]string) {
	t.Helper()
	root := codegenRepositoryRoot(t)
	profile := filepath.Join(root, "conformance/runners/django/zoned_calendar_reference")
	hashes := map[string]string{}
	var input zonedReferenceInput
	var data zonedReferenceData
	for _, name := range []string{"observe.py", "prepare_data.py", "input.json", "data-source.json", "../calendar_extraction_reference/observe.py", "../calendar_extraction_reference/source-verification.json"} {
		value, err := os.ReadFile(filepath.Join(profile, name))
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(value)
		hashes[name] = hex.EncodeToString(hash[:])
		if name == "input.json" {
			if err := json.Unmarshal(value, &input); err != nil {
				t.Fatal(err)
			}
		}
		if name == "data-source.json" {
			if err := json.Unmarshal(value, &data); err != nil {
				t.Fatal(err)
			}
		}
	}
	return input, data, hashes
}

func validateZonedReference(document zonedReferenceDocument, input zonedReferenceInput, data zonedReferenceData, hashes map[string]string, receiptBytes []byte, vendor string) error {
	if document.Kind != "django-zoned-calendar-reference-v1" || document.Backend != vendor || document.Commit != "fe0a859f537d4238cf49fca39073513206f83122" ||
		document.Observer != hashes["observe.py"] || document.Shared != hashes["../calendar_extraction_reference/observe.py"] || document.Input != hashes["input.json"] || document.Data != hashes["data-source.json"] || document.Prepare != hashes["prepare_data.py"] ||
		document.Sources != hashes["../calendar_extraction_reference/source-verification.json"] || !document.Unchanged || !document.Cleaned {
		return errors.New("zoned reference source, backend or cleanup differs")
	}
	if document.Runtime.Python != "3.14.3" || document.Runtime.Django != "6.1" || document.Runtime.SQLite == "" || document.Runtime.Timezone != "UTC" || document.Runtime.DataProfile != "backzone/zone.tab" || document.Runtime.IANA != timezone.DataVersion ||
		(vendor == "postgres" && (document.Runtime.PostgreSQL != "17.10" && !strings.HasPrefix(document.Runtime.PostgreSQL, "17.10 ") || document.Runtime.Psycopg != "3.3.6")) {
		return errors.New("zoned reference runtime or data version differs")
	}
	if raw, err := hex.DecodeString(document.Runtime.ZoneInfoHash); err != nil || len(raw) != sha256.Size {
		return errors.New("zoned reference lacks its ZoneInfo module identity")
	}
	var receipt zonedBuildReceipt
	decoder := json.NewDecoder(bytes.NewReader(receiptBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return errors.New("timezone build receipt contains trailing input")
	}
	receiptHash := sha256.Sum256(receiptBytes)
	build := map[string]string{"target": "posix_only", "CFLAGS": "-DSTD_INSPIRED", "AWK": "gawk", "PACKRATDATA": "backzone", "PACKRATLIST": "zone.tab"}
	archives := map[string]string{}
	for name, identity := range data.Archives {
		archives[name] = identity.SHA256
	}
	if document.ReceiptHash != hex.EncodeToString(receiptHash[:]) || !reflect.DeepEqual(document.Receipt, receipt) || receipt.Kind != "independent-iana-build-v1" || !receipt.Complete || receipt.IANA != "2025c" ||
		receipt.Data != hashes["data-source.json"] || receipt.Prepare != hashes["prepare_data.py"] || !maps.Equal(receipt.Build, build) || !maps.Equal(data.Build, build) || !maps.Equal(receipt.Archives, archives) ||
		receipt.Names != data.NamesHash || receipt.Files != 598 || receipt.Packed != data.Packed.SHA256 || receipt.Compiler == "" || !strings.HasPrefix(receipt.Awk, "GNU Awk ") {
		return errors.New("independent IANA build receipt differs from its exact source and policy")
	}
	if binary, err := hex.DecodeString(receipt.Binary); err != nil || len(binary) != sha256.Size {
		return errors.New("IANA compiler binary identity missing")
	}
	names, err := timezone.Names()
	if err != nil {
		return err
	}
	if input.Kind != "independent-zoned-calendar-inputs-v1" || len(input.Fixtures) != 11 || len(input.Operations) != 28 || len(input.Failures) != 8 || len(input.ProviderInstants) != 15 ||
		data.Kind != "independent-iana-build-source-v1" || data.IANA != timezone.DataVersion || len(data.Names) != 598 || !slices.Equal(data.Names, names) || len(data.Archives) != 2 ||
		data.Archives["tzcode2025c.tar.gz"].SHA256 != "697ebe6625444aef5080f58e49d03424bbb52e08bf483d3ddb5acf10cbd15740" || data.Archives["tzdata2025c.tar.gz"].SHA256 != "4aa79e4effee53fc4029ffe5f6ebe97937282ebcdf386d5d2da91ce84142f957" {
		return errors.New("zoned reference inputs or independent data identity differs")
	}
	if hash := sha256.Sum256([]byte(strings.Join(names, "\n") + "\n")); data.NamesHash != hex.EncodeToString(hash[:]) {
		return errors.New("zoned reference names checksum differs")
	}
	if len(document.Provider) != len(names) {
		return errors.New("zoned reference provider inventory differs")
	}
	for index, zone := range document.Provider {
		if zone.Name != names[index] || len(zone.Values) != len(input.ProviderInstants) {
			return errors.New("zoned reference provider order or probe count differs")
		}
		for _, value := range zone.Values {
			if value.Error != "" {
				if value.Error != "OverflowError" || value.Local != "" || value.Offset != nil {
					return errors.New("zoned provider error lost its exact domain")
				}
			} else {
				if value.Offset == nil || *value.Offset < -86400 || *value.Offset > 86400 {
					return errors.New("zoned provider offset is invalid")
				}
				local, err := time.Parse("2006-01-02T15:04:05.000000", value.Local)
				if err != nil || local.Year() < 1 || local.Year() > 9999 || local.Format("2006-01-02T15:04:05.000000") != value.Local {
					return errors.New("zoned provider local value lost canonical precision")
				}
			}
		}
	}
	caseIndex, probeIndex := 0, 0
	checkCase := func(name, zone string, failure bool) error {
		if caseIndex >= len(document.Cases) {
			return errors.New("zoned reference case is missing")
		}
		value := document.Cases[caseIndex]
		caseIndex++
		if value.Name != name || value.Zone != zone || !value.Unchanged || !failure && value.Error != nil {
			return fmt.Errorf("zoned reference case identity, success or preservation differs: %s", name)
		}
		if value.Error != nil && (value.Error.Type == "" || value.Error.Stage == "" || value.Error.Message == "") {
			return errors.New("zoned reference error lost its native identity")
		}
		return nil
	}
	for _, fixture := range input.Fixtures {
		for _, text := range fixture.Instants {
			if text == nil {
				continue
			}
			if probeIndex >= len(document.Probes) {
				return errors.New("native zone probe is missing")
			}
			probe := document.Probes[probeIndex]
			probeIndex++
			instant, err := time.Parse(time.RFC3339Nano, *text)
			if err != nil || probe.Fixture != fixture.Name || probe.Zone != fixture.Zone || probe.Instant != instant.UTC().Format("2006-01-02T15:04:05.000000Z") || probe.Python < -86400 || probe.Python > 86400 {
				return errors.New("native timezone probe identity differs")
			}
			if vendor == "postgres" && (probe.Postgres == nil || *probe.Postgres != probe.Python) || vendor == "sqlite" && probe.Postgres != nil {
				return errors.New("native PostgreSQL data differs from pinned Python rules")
			}
		}
		for _, operation := range input.Operations {
			if err := checkCase(fixture.Name+"/"+operation, fixture.Zone, false); err != nil {
				return err
			}
		}
	}
	for _, failure := range input.Failures {
		if err := checkCase("failure/"+failure.Name, failure.Zone, true); err != nil {
			return err
		}
	}
	if caseIndex != len(document.Cases) || probeIndex != len(document.Probes) {
		return errors.New("zoned reference contains unexpected cases or probes")
	}
	return nil
}

func prepareZonedCalendarReference(t *testing.T, root, references string) []string {
	t.Helper()
	input, source, hashes := zonedReferenceSources(t)
	profile := filepath.Join(codegenRepositoryRoot(t), "conformance/runners/django/zoned_calendar_reference")
	for _, file := range []struct{ source, target string }{{"input.json", "zoned-input.json"}, {"data-source.json", "zoned-data-source.json"}} {
		data, err := os.ReadFile(filepath.Join(profile, file.source))
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+file.target, data)
	}
	receiptBytes, err := os.ReadFile(filepath.Join(references, "zoned-build.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, vendor := range []string{"sqlite", "postgres"} {
		data, err := os.ReadFile(filepath.Join(references, "zoned-django61-"+vendor+".json"))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 32<<20 {
			t.Fatal("zoned reference exceeds its document budget")
		}
		var document zonedReferenceDocument
		if err := json.Unmarshal(data, &document); err != nil {
			t.Fatal(err)
		}
		if err := validateZonedReference(document, input, source, hashes, receiptBytes, vendor); err != nil {
			t.Fatal(vendor, err)
		}
		writeGeneratedTestFile(t, root, "consumer/zoned-django61-"+vendor+".json", data)
		t.Logf("zoned reference %s: sha256=%x observer=%s input=%s data=%s cases=%d provider_zones=%d", vendor, sha256.Sum256(data), document.Observer, document.Input, document.Data, len(document.Cases), len(document.Provider))
	}
	data, err := os.ReadFile(filepath.Join("testdata", "calendarextraction", "zoned_reference_test.go"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/zoned_reference_test.go", data)
	required := []string{"TestZonedCalendarReference", "TestZonedCalendarReference/pinned_provider"}
	for _, fixture := range input.Fixtures {
		required = append(required, "TestZonedCalendarReference/"+fixture.Name)
		for _, vendor := range []string{"sqlite", "postgres"} {
			prefix := "TestZonedCalendarReference/" + fixture.Name + "/" + vendor
			required = append(required, prefix)
			for _, operation := range input.Operations {
				required = append(required, prefix+"/"+operation)
			}
		}
	}
	for _, fixture := range input.Failures {
		prefix := "TestZonedCalendarReference/failure_" + fixture.Name
		required = append(required, prefix, prefix+"/sqlite", prefix+"/postgres")
	}
	return required
}

func TestGeneratedZonedCalendarReferenceBindingRejectsForeignEvidence(t *testing.T) {
	input, data, hashes := zonedReferenceSources(t)
	// This synthetic metadata fixture exercises admission only. Its placeholder
	// observations never supply expected calendar results to a consumer.
	receipt := zonedBuildReceipt{Kind: "independent-iana-build-v1", IANA: "2025c", Build: maps.Clone(data.Build),
		Data: hashes["data-source.json"], Prepare: hashes["prepare_data.py"], Archives: map[string]string{},
		Names: data.NamesHash, Files: 598, Packed: data.Packed.SHA256, Binary: strings.Repeat("a", 64), Compiler: "synthetic compiler identity", Awk: "GNU Awk synthetic identity", Complete: true}
	for name, identity := range data.Archives {
		receipt.Archives[name] = identity.SHA256
	}
	receiptBytes, err := json.Marshal(receipt)
	if err != nil {
		t.Fatal(err)
	}
	receiptHash := sha256.Sum256(receiptBytes)
	provider := make([]map[string]any, 0, len(data.Names))
	for _, name := range data.Names {
		values := make([]map[string]any, len(input.ProviderInstants))
		for index := range values {
			values[index] = map[string]any{"local": "2000-01-01T00:00:00.000000", "offset_seconds": 0}
		}
		provider = append(provider, map[string]any{"name": name, "values": values})
	}
	var cases, probes []map[string]any
	for _, fixture := range input.Fixtures {
		for _, operation := range input.Operations {
			cases = append(cases, map[string]any{"name": fixture.Name + "/" + operation, "zone": fixture.Zone, "storage_unchanged": true})
		}
		for _, text := range fixture.Instants {
			if text == nil {
				continue
			}
			instant, err := time.Parse(time.RFC3339Nano, *text)
			if err != nil {
				t.Fatal(err)
			}
			probes = append(probes, map[string]any{"fixture": fixture.Name, "zone": fixture.Zone,
				"instant": instant.UTC().Format("2006-01-02T15:04:05.000000Z"), "zoneinfo_offset_seconds": 0, "postgres_offset_seconds": 0})
		}
	}
	for _, failure := range input.Failures {
		cases = append(cases, map[string]any{"name": "failure/" + failure.Name, "zone": failure.Zone, "storage_unchanged": true})
	}
	raw, err := json.Marshal(map[string]any{
		"kind": "django-zoned-calendar-reference-v1", "backend": "postgres", "django_commit": "fe0a859f537d4238cf49fca39073513206f83122",
		"observer_sha256": hashes["observe.py"], "shared_observer_sha256": hashes["../calendar_extraction_reference/observe.py"],
		"input_sha256": hashes["input.json"], "data_source_sha256": hashes["data-source.json"], "prepare_sha256": hashes["prepare_data.py"],
		"source_verification_sha256": hashes["../calendar_extraction_reference/source-verification.json"],
		"build_receipt_sha256":       hex.EncodeToString(receiptHash[:]), "build_receipt": receipt,
		"runtime": map[string]any{"python": "3.14.3", "django": "6.1", "sqlite": "3.50.4", "postgresql": "17.10", "psycopg": "3.3.6",
			"timezone": "UTC", "data_profile": "backzone/zone.tab", "iana": "2025c", "zoneinfo_module_sha256": strings.Repeat("b", 64)},
		"storage_unchanged": true, "storage_cleaned": true, "provider": provider, "cases": cases, "native_zone_probes": probes,
	})
	if err != nil {
		t.Fatal(err)
	}
	fresh := func() zonedReferenceDocument {
		var value zonedReferenceDocument
		if err := json.Unmarshal(raw, &value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	valid := fresh()
	if err := validateZonedReference(valid, input, data, hashes, receiptBytes, "postgres"); err != nil {
		t.Fatal("positive admission", err)
	}
	for name, mutate := range map[string]func(*zonedReferenceDocument){
		"kind":               func(v *zonedReferenceDocument) { v.Kind = "foreign" },
		"backend":            func(v *zonedReferenceDocument) { v.Backend = "sqlite" },
		"commit":             func(v *zonedReferenceDocument) { v.Commit = "other" },
		"observer":           func(v *zonedReferenceDocument) { v.Observer = "other" },
		"shared observer":    func(v *zonedReferenceDocument) { v.Shared = "other" },
		"inputs":             func(v *zonedReferenceDocument) { v.Input = "other" },
		"data":               func(v *zonedReferenceDocument) { v.Data = "other" },
		"prepare":            func(v *zonedReferenceDocument) { v.Prepare = "other" },
		"sources":            func(v *zonedReferenceDocument) { v.Sources = "other" },
		"receipt":            func(v *zonedReferenceDocument) { v.ReceiptHash = "other" },
		"embedded receipt":   func(v *zonedReferenceDocument) { v.Receipt.Files-- },
		"cleanup":            func(v *zonedReferenceDocument) { v.Cleaned = false },
		"storage":            func(v *zonedReferenceDocument) { v.Unchanged = false },
		"Python":             func(v *zonedReferenceDocument) { v.Runtime.Python = "3.14.7" },
		"Django":             func(v *zonedReferenceDocument) { v.Runtime.Django = "6.2" },
		"PostgreSQL":         func(v *zonedReferenceDocument) { v.Runtime.PostgreSQL = "18.0" },
		"driver":             func(v *zonedReferenceDocument) { v.Runtime.Psycopg = "other" },
		"profile":            func(v *zonedReferenceDocument) { v.Runtime.DataProfile = "wheel" },
		"IANA":               func(v *zonedReferenceDocument) { v.Runtime.IANA = "2026a" },
		"ZoneInfo":           func(v *zonedReferenceDocument) { v.Runtime.ZoneInfoHash = "" },
		"provider inventory": func(v *zonedReferenceDocument) { v.Provider = v.Provider[:597] },
		"provider name":      func(v *zonedReferenceDocument) { v.Provider[0].Name = "unknown" },
		"provider samples":   func(v *zonedReferenceDocument) { v.Provider[0].Values = nil },
		"provider precision": func(v *zonedReferenceDocument) { v.Provider[0].Values[0].Local = "2000-01-01T00:00:00" },
		"provider offset":    func(v *zonedReferenceDocument) { v.Provider[0].Values[0].Offset = nil },
		"provider error":     func(v *zonedReferenceDocument) { v.Provider[0].Values[0].Error = "ValueError" },
		"probe inventory":    func(v *zonedReferenceDocument) { v.Probes = v.Probes[:len(v.Probes)-1] },
		"probe identity":     func(v *zonedReferenceDocument) { v.Probes[0].Instant = "2000-01-01T00:00:00.000000Z" },
		"server data":        func(v *zonedReferenceDocument) { v.Probes[0].Postgres = new(1) },
		"case inventory":     func(v *zonedReferenceDocument) { v.Cases = v.Cases[:315] },
		"case order":         func(v *zonedReferenceDocument) { v.Cases[0], v.Cases[1] = v.Cases[1], v.Cases[0] },
		"case zone":          func(v *zonedReferenceDocument) { v.Cases[0].Zone = "other" },
		"case storage":       func(v *zonedReferenceDocument) { v.Cases[0].Unchanged = false },
	} {
		changed := fresh()
		mutate(&changed)
		if validateZonedReference(changed, input, data, hashes, receiptBytes, "postgres") == nil {
			t.Fatal("foreign zoned reference accepted", name)
		}
	}
	for name, mutate := range map[string]func(*zonedBuildReceipt){
		"policy":        func(v *zonedBuildReceipt) { v.Build["PACKRATDATA"] = "" },
		"release":       func(v *zonedBuildReceipt) { v.Archives["tzdata2025c.tar.gz"] = "other" },
		"incomplete":    func(v *zonedBuildReceipt) { v.Complete = false },
		"compiled data": func(v *zonedBuildReceipt) { v.Packed = "other" },
		"compiler":      func(v *zonedBuildReceipt) { v.Binary = "" },
		"awk":           func(v *zonedBuildReceipt) { v.Awk = "" },
	} {
		changed := fresh()
		mutate(&changed.Receipt)
		changedBytes, err := json.Marshal(changed.Receipt)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(changedBytes)
		changed.ReceiptHash = hex.EncodeToString(hash[:])
		if validateZonedReference(changed, input, data, hashes, changedBytes, "postgres") == nil {
			t.Fatal("self-consistent foreign build accepted", name)
		}
	}
	if validateZonedReference(valid, input, data, hashes, append(slices.Clone(receiptBytes), []byte(" {}")...), "postgres") == nil {
		t.Fatal("trailing receipt accepted")
	}
}
