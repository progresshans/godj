package settings_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/settings"
)

func TestEnvironmentOwnsExactValuesWithoutReadingOrChangingTheProcess(t *testing.T) {
	t.Setenv("GODJ_TEST_ENV_SNAPSHOT", "ambient")
	entries := []string{"GODJ_TEST_ENV_SNAPSHOT=first", "EMPTY=", "TOKEN= secret=value\nsecond line ", "custom.name=application"}
	first, err := settings.NewEnvironment(entries)
	if err != nil {
		t.Fatal(err)
	}
	entries[0] = "GODJ_TEST_ENV_SNAPSHOT=mutated"
	second, err := settings.NewEnvironment([]string{"GODJ_TEST_ENV_SNAPSHOT=second"})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		environment settings.Environment
		name, want  string
		present     bool
	}{{first, "GODJ_TEST_ENV_SNAPSHOT", "first", true}, {second, "GODJ_TEST_ENV_SNAPSHOT", "second", true},
		{first, "EMPTY", "", true}, {first, "TOKEN", " secret=value\nsecond line ", true},
		{first, "custom.name", "application", true}, {first, "missing", "", false},
		{settings.Environment{}, "GODJ_TEST_ENV_SNAPSHOT", "", false}} {
		value, present := test.environment.Lookup(test.name)
		if value != test.want || present != test.present {
			t.Fatal("snapshot lookup changed a value or its presence")
		}
	}
	if value := os.Getenv("GODJ_TEST_ENV_SNAPSHOT"); value != "ambient" {
		t.Fatal("snapshot changed the process environment")
	}
	for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
		if rendered := fmt.Sprintf(format, first); rendered != "settings.Environment{redacted}" {
			t.Fatal("environment diagnostics exposed its contents")
		}
	}
	document, err := json.Marshal(first)
	if err != nil || string(document) != "{}" {
		t.Fatal("environment serialized credential-bearing values", err)
	}
}

func TestEnvironmentRejectsMalformedDuplicatesAndResourceOverflowWithoutValues(t *testing.T) {
	secret := "do-not-disclose-environment"
	for _, test := range []struct {
		name    string
		entries []string
	}{{"missing_separator", []string{secret}}, {"empty_name", []string{"=" + secret}},
		{"nul_name", []string{"TOKEN\x00=" + secret}}, {"nul_value", []string{"TOKEN=" + secret + "\x00"}},
		{"duplicate", []string{"TOKEN=" + secret, "TOKEN=another"}},
		{"entry_limit", make([]string, settings.MaximumEnvironmentEntries+1)},
		{"byte_limit", []string{"TOKEN=" + strings.Repeat("x", settings.MaximumEnvironmentBytes)}},
		{"cumulative_byte_limit", []string{"FIRST=" + strings.Repeat("x", settings.MaximumEnvironmentBytes/2), "SECOND=" + strings.Repeat("y", settings.MaximumEnvironmentBytes/2)}}} {
		t.Run(test.name, func(t *testing.T) {
			environment, err := settings.NewEnvironment(test.entries)
			if err == nil {
				t.Fatal("invalid environment was accepted")
			}
			if _, present := environment.Lookup("TOKEN"); present {
				t.Fatal("invalid environment published a partial snapshot")
			}
			for _, format := range []string{"%v", "%+v", "%#v"} {
				if strings.Contains(fmt.Sprintf(format, err), secret) {
					t.Fatal("invalid environment exposed the entry")
				}
			}
		})
	}
	maximum := "VALUE=" + strings.Repeat("x", settings.MaximumEnvironmentBytes-len("VALUE="))
	if _, err := settings.NewEnvironment([]string{maximum}); err != nil {
		t.Fatal("exact environment byte boundary was rejected", err)
	}
}

func TestEnvironmentCopiesShareOnlyImmutableConcurrentLookupState(t *testing.T) {
	entries := []string{"VALUE=original"}
	environment, err := settings.NewEnvironment(entries)
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			copy := environment
			for range 100 {
				if value, present := copy.Lookup("VALUE"); !present || value != "original" {
					t.Error("copied environment changed during a concurrent read")
				}
			}
		})
	}
	for range 100 {
		entries[0] = "VALUE=caller mutation"
	}
	workers.Wait()
}
