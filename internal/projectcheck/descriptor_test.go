//go:build darwin || linux

package projectcheck

import (
	"testing"

	"github.com/progresshans/godj/internal/projectcheck/protocol"
)

func TestArgumentsAndDescriptorAreClosed(t *testing.T) {
	t.Parallel()
	for _, valid := range [][]string{
		{"migrations", "check"},
		{"migrations", "check", "--project", "godj.toml"},
	} {
		if _, primary := parseArguments(valid); primary != nil {
			t.Fatalf("parseArguments(%q) = %+v", valid, primary)
		}
	}
	for _, invalid := range [][]string{
		nil,
		{"check", "migrations"},
		{"--project", "godj.toml", "migrations", "check"},
		{"migrations", "check", "--project=godj.toml"},
		{"migrations", "check", "--project"},
		{"migrations", "check", "--project", ""},
		{"migrations", "check", "--project", "godj.toml", "--project", "other.toml"},
		{"migrations", "check", "extra"},
	} {
		_, primary := parseArguments(invalid)
		if primary == nil || primary.Category != protocol.CategoryCommand || primary.Code != protocol.CodeInvalidArguments {
			t.Fatalf("parseArguments(%q) = %+v", invalid, primary)
		}
	}

	valid := []byte("format_version = 1\n\n[project]\npackage = \"./cmd/site\"\n")
	parsed, primary := parseProjectDescriptor(valid)
	if primary != nil || parsed.packagePath != "./cmd/site" || parsed.formatVersion != 1 {
		t.Fatalf("valid descriptor = %+v, %+v", parsed, primary)
	}
	crlf := []byte("# comment\r\nformat_version\t=\t1\r\n[project]\r\npackage = \"./cmd/site\"\r\n")
	if _, primary := parseProjectDescriptor(crlf); primary != nil {
		t.Fatalf("valid CRLF descriptor = %+v", primary)
	}

	invalidDocuments := [][]byte{
		{},
		[]byte("format_version = 1\n[project]\npackage = \"./cmd/site\""),
		[]byte("format_version = 1\r\n[project]\npackage = \"./cmd/site\"\n"),
		[]byte("format_version = 01\n[project]\npackage = \"./cmd/site\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"cmd/site\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"./a]b\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"./a{b\"\n"),
		[]byte("format_version = 1\n[project]\npackage = \"./a}b\"\n"),
		[]byte("format_version = 1 # inline\n[project]\npackage = \"./cmd/site\"\n"),
	}
	for _, document := range invalidDocuments {
		_, primary := parseProjectDescriptor(document)
		if primary == nil || primary.Code != protocol.CodeInvalidProjectDescriptor {
			t.Fatalf("invalid descriptor %q = %+v", document, primary)
		}
	}
	incompatible := []byte("format_version = 2\n[project]\npackage = \"./cmd/site\"\n")
	if _, primary := parseProjectDescriptor(incompatible); primary == nil || primary.Code != protocol.CodeProjectDescriptorIncompatible {
		t.Fatalf("incompatible descriptor = %+v", primary)
	}
	incompatibleAndInvalid := []byte("format_version = 2\n[project]\npackage = \"cmd/site\"\n")
	if _, primary := parseProjectDescriptor(incompatibleAndInvalid); primary == nil || primary.Code != protocol.CodeInvalidProjectDescriptor {
		t.Fatalf("incompatible and invalid descriptor = %+v", primary)
	}
}

func TestGlobalFailureTaxonomyIsProtocolOwned(t *testing.T) {
	t.Parallel()
	pairs := []protocol.Failure{
		{Category: protocol.CategoryCommand, Code: protocol.CodeInvalidArguments},
		{Category: protocol.CategorySelection, Code: protocol.CodeProjectNotFound},
		{Category: protocol.CategoryBuild, Code: protocol.CodeProjectBuildFailed},
		{Category: protocol.CategoryProtocol, Code: protocol.CodeInvalidProjectRunnerResponse},
		{Category: protocol.CategoryProcess, Code: protocol.CodeProjectInterrupted},
		{Category: protocol.CategoryDiscovery, Code: protocol.CodeUnsafeSourceEntry},
		{Category: protocol.CategorySource, Code: "invalid_definition_document"},
		{Category: protocol.CategoryGraph, Code: "dependency_cycle"},
	}
	for _, pair := range pairs {
		actual := failure(pair.Category, pair.Code)
		if actual != pair {
			t.Fatalf("failure(%+v) = %+v", pair, actual)
		}
		if _, ok := protocol.ExitCode(actual); !ok {
			t.Fatalf("protocol rejected %+v", actual)
		}
	}
	if actual := failure("invented", "invented"); actual.Category != protocol.CategoryInternal || actual.Code != protocol.CodeProjectInternalError {
		t.Fatalf("unknown pair = %+v", actual)
	}
}

func TestDescriptorParserIsPanicFreeForArbitraryBytes(t *testing.T) {
	t.Parallel()
	for value := 0; value <= 255; value++ {
		document := []byte{byte(value), 0xff, '\r', '\n', byte(255 - value), '\n'}
		_, _ = parseProjectDescriptor(document)
	}
	oversized := make([]byte, maxDescriptorBytes+1)
	for index := range oversized {
		oversized[index] = byte(index)
	}
	_, _ = parseProjectDescriptor(oversized)
}
