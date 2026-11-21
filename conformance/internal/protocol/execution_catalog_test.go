package protocol

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type executionSuite struct {
	Name      string   `json:"name"`
	Profile   string   `json:"profile"`
	Manifest  string   `json:"manifest"`
	Oracle    string   `json:"oracle"`
	Baseline  string   `json:"baseline"`
	Product   bool     `json:"product"`
	Deviation string   `json:"deviation"`
	Captures  []string `json:"captures"`
}

type executionCatalog struct {
	Profiles map[string]struct {
		Path          string `json:"path"`
		PythonProject string `json:"python_project"`
	} `json:"profiles"`
	Suites []executionSuite `json:"suites"`
}

func readExecutionCatalog(t *testing.T) executionCatalog {
	t.Helper()
	var catalog executionCatalog
	if err := json.Unmarshal([]byte(ciRead(t, "conformance/suites.json")), &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog.Suites) == 0 {
		t.Fatal("execution catalog is empty")
	}
	return catalog
}

// The emitted argument arrays, manifest status and profile locks are the
// execution contract. Variable spelling, source layout and target order are not.
func TestExecutionCatalogPlansEveryIndependentReferenceAndEligibleProduct(t *testing.T) {
	t.Parallel()
	root := conformanceRepositoryRoot(t)
	command := exec.CommandContext(t.Context(), "python3", "scripts/conformance.py", "plan")
	command.Dir = root
	captureRoot := t.TempDir()
	systemCapture := filepath.Join(captureRoot, "systemstate.json")
	operatorCapture := filepath.Join(captureRoot, "operator.json")
	command.Env = append(os.Environ(), "PYTHONDONTWRITEBYTECODE=1", "SYSTEM_STATE_POSTGRES_ATTESTATION="+systemCapture, "PROJECT_OPERATOR_POSTGRES_ATTESTATION="+operatorCapture)
	document, err := command.Output()
	if err != nil {
		t.Fatalf("read execution plans: %v", err)
	}
	type invocation struct {
		Suite     string   `json:"suite"`
		Tool      string   `json:"tool"`
		Arguments []string `json:"arguments"`
	}
	var plans map[string][]invocation
	if err := json.Unmarshal(document, &plans); err != nil {
		t.Fatal(err)
	}
	catalog := readExecutionCatalog(t)
	byName := make(map[string]executionSuite)
	declared := make(map[string]bool)
	for _, suite := range catalog.Suites {
		if _, exists := byName[suite.Name]; exists || declared[suite.Manifest] {
			t.Fatal("catalog repeats a suite or manifest")
		}
		byName[suite.Name], declared[suite.Manifest] = suite, true
		manifest := requireArtifact(t, filepath.Join(root, suite.Manifest), LoadManifest)
		profile := requireArtifact(t, filepath.Join(root, catalog.Profiles[suite.Profile].Path), LoadProfile)
		if manifest.ProfileID != profile.ID {
			t.Fatalf("%s profile does not match its manifest", suite.Name)
		}
		if filepath.Clean(catalog.Profiles[suite.Profile].PythonProject) != filepath.Dir(profile.Lock.File) {
			t.Fatalf("%s Python environment is not the profile's locked project", suite.Name)
		}
		product, deviation := true, false
		for _, contract := range manifest.Contracts {
			product = product && (contract.Status == ContractPassing || contract.Status == ContractDeviation)
			deviation = deviation || contract.Status == ContractDeviation
		}
		if suite.Product != product || (suite.Deviation != "") != deviation {
			t.Fatalf("%s execution eligibility/deviation differs from contract status", suite.Name)
		}
		if suite.Name == "system-state" {
			if !reflect.DeepEqual(suite.Captures, []string{"systemstate", "operator"}) {
				t.Fatal("system-state lost its actual capture requirements")
			}
		} else if len(suite.Captures) != 0 {
			t.Fatalf("%s unexpectedly consumes a system-state capture", suite.Name)
		}
	}
	files, err := filepath.Glob(filepath.Join(root, "conformance/contracts/*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		relative, err := filepath.Rel(root, file)
		if err != nil || !declared[filepath.ToSlash(relative)] {
			t.Fatalf("manifest has no execution owner: %s", file)
		}
	}
	if len(files) != len(declared) {
		t.Fatal("catalog includes an undeclared manifest")
	}
	modes := []string{"reference", "product", "oracle-check", "oracle-regenerate"}
	if len(plans) != len(modes) {
		t.Fatal("unexpected execution modes")
	}
	for _, mode := range modes {
		seen := make(map[string][]string)
		for _, step := range plans[mode] {
			suite, ok := byName[step.Suite]
			if !ok {
				t.Fatalf("%s executes unknown suite %s", mode, step.Suite)
			}
			profile := catalog.Profiles[suite.Profile]
			prefix := "-"
			if strings.HasPrefix(mode, "oracle-") {
				prefix = "--"
			}
			if argumentValue(step.Arguments, prefix+"manifest") != filepath.Join(root, suite.Manifest) || argumentValue(step.Arguments, prefix+"profile") != filepath.Join(root, profile.Path) {
				t.Fatalf("%s/%s uses the wrong manifest or profile", mode, step.Suite)
			}
			switch mode {
			case "reference":
				if step.Tool != "contractcheck" {
					t.Fatal("reference shape check uses a product runner")
				}
				seen[step.Suite] = append(seen[step.Suite], argumentValue(step.Arguments, "-suite"))
			case "product":
				if step.Tool != "godjcheck" || !suite.Product || argumentValue(step.Arguments, "-expected") != filepath.Join(root, suite.Oracle) || containsString(step.Arguments, filepath.Join(root, suite.Baseline)) {
					t.Fatalf("%s product uses the wrong authority or an ineligible suite", step.Suite)
				}
				wantDeviation := ""
				if suite.Deviation != "" {
					wantDeviation = filepath.Join(root, suite.Deviation)
				}
				if argumentValue(step.Arguments, "-deviation-expected") != wantDeviation {
					t.Fatalf("%s deviation is not passed exactly", step.Suite)
				}
				if suite.Name == "system-state" && (argumentValue(step.Arguments, "-system-state-postgres-attestation") != systemCapture || argumentValue(step.Arguments, "-project-operator-postgres-attestation") != operatorCapture) {
					t.Fatal("product lost current capture overrides")
				}
				seen[step.Suite] = append(seen[step.Suite], "product")
			default:
				if step.Tool != "uv" || argumentValue(step.Arguments, "--project") != filepath.Join(root, profile.PythonProject) || !containsString(step.Arguments, "--frozen") || argumentValue(step.Arguments, "-m") != "conformance.runners.django" || argumentValue(step.Arguments, "--output") != filepath.Join(root, suite.Oracle) || containsString(step.Arguments, "--check") != (mode == "oracle-check") {
					t.Fatalf("%s/%s lost its independent locked reference invocation", mode, step.Suite)
				}
				seen[step.Suite] = append(seen[step.Suite], "oracle")
			}
		}
		for _, suite := range catalog.Suites {
			want := []string{"oracle"}
			if mode == "reference" {
				want = []string{filepath.Join(root, suite.Oracle), filepath.Join(root, suite.Baseline)}
			}
			if mode == "product" {
				want = nil
				if suite.Product {
					want = []string{"product"}
				}
			}
			sort.Strings(want)
			sort.Strings(seen[suite.Name])
			if !reflect.DeepEqual(seen[suite.Name], want) {
				t.Fatalf("%s/%s executes %v, want %v", mode, suite.Name, seen[suite.Name], want)
			}
		}
	}
}

func argumentValue(arguments []string, name string) string {
	for i, value := range arguments {
		if value == name && i+1 < len(arguments) {
			return arguments[i+1]
		}
	}
	return ""
}

func TestMakeConformanceTargetsDispatchTheIntendedMode(t *testing.T) {
	t.Parallel()
	root, temporary := conformanceRepositoryRoot(t), t.TempDir()
	output := filepath.Join(temporary, "arguments")
	python := filepath.Join(temporary, "python3")
	if err := os.WriteFile(python, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$GODJ_CAPTURE_ARGUMENTS\"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(temporary, "evidence.json")
	if err := os.WriteFile(evidence, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for target, mode := range map[string]string{"conformance-check": "reference", "godj-conformance": "product", "oracle-check": "oracle-check", "oracle-regenerate": "oracle-regenerate"} {
		command := exec.CommandContext(t.Context(), "make", target, "SYSTEM_STATE_POSTGRES_ATTESTATION="+evidence, "PROJECT_OPERATOR_POSTGRES_ATTESTATION="+evidence)
		command.Dir = root
		command.Env = append(os.Environ(), "PATH="+temporary+string(os.PathListSeparator)+os.Getenv("PATH"), "GODJ_CAPTURE_ARGUMENTS="+output)
		if document, err := command.CombinedOutput(); err != nil {
			t.Fatalf("%s: %v\n%s", target, err, document)
		}
		document, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if string(document) != "scripts/conformance.py\n"+mode+"\n" {
			t.Fatalf("%s dispatch = %q", target, document)
		}
	}
}
