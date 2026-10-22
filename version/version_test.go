package version

import (
	"encoding/json"
	"fmt"
	"reflect"
	"runtime/debug"
	"strings"
	"testing"
)

func TestFrameworkIdentityUsesTheEffectiveModuleAndOwnVCS(t *testing.T) {
	settings := []debug.BuildSetting{
		{Key: "vcs.revision", Value: "f00d1234"}, {Key: "vcs.modified", Value: "true"},
		{Key: "-ldflags", Value: "-X main.password=secret-marker"},
		{Key: "vcs.time", Value: "private-build-time"},
	}
	for _, test := range []struct {
		name      string
		main      bool
		framework debug.Module
		want      Info
	}{
		{"main checkout", true, debug.Module{Path: Module, Version: "(devel)"},
			Info{Origin: Development, Revision: "f00d1234", Modified: true}},
		{"installed command", true, debug.Module{Path: Module, Version: "v1.2.3"},
			Info{Origin: ModuleVersion, Version: "v1.2.3", Revision: "f00d1234", Modified: true}},
		{"dependency", false, debug.Module{Path: Module, Version: "v0.0.0-20261022123456-abcdef123456"},
			Info{Origin: ModuleVersion, Version: "v0.0.0-20261022123456-abcdef123456"}},
		{"workspace dependency", false, debug.Module{Path: Module, Version: "(devel)"},
			Info{Origin: Development}},
		{"local replacement", false, debug.Module{Path: Module, Version: "v1.2.3", Replace: &debug.Module{Path: "/private/secret-marker/godj", Version: "(devel)"}},
			Info{Origin: LocalReplacement, RequestedVersion: "v1.2.3"}},
		{"relative replacement", false, debug.Module{Path: Module, Version: "v1.2.3", Replace: &debug.Module{Path: "../secret-marker/godj"}},
			Info{Origin: LocalReplacement, RequestedVersion: "v1.2.3"}},
		{"version replacement", false, debug.Module{Path: Module, Version: "v1.2.3", Replace: &debug.Module{Path: Module, Version: "v1.2.4"}},
			Info{Origin: ModuleReplacement, Version: "v1.2.4", RequestedVersion: "v1.2.3", ReplacementModule: Module}},
		{"fork replacement", false, debug.Module{Path: Module, Version: "v1.2.3", Replace: &debug.Module{Path: "example.com/fork/godj", Version: "v1.8.0"}},
			Info{Origin: ModuleReplacement, Version: "v1.8.0", RequestedVersion: "v1.2.3", ReplacementModule: "example.com/fork/godj"}},
		{"unknown version", false, debug.Module{Path: Module, Version: "v1.2.3\nsecret-marker"}, Info{Origin: Unknown}},
	} {
		t.Run(test.name, func(t *testing.T) {
			build := &debug.BuildInfo{GoVersion: "go1.26.5", Settings: settings}
			if test.main {
				build.Main = test.framework
			} else {
				build.Main = debug.Module{Path: "example.com/private-application", Version: "v99.0.0"}
				build.Deps = []*debug.Module{nil, {Path: "example.com/unrelated", Version: "v8.0.0"}, &test.framework}
			}
			test.want.Module, test.want.GoVersion = Module, "go1.26.5"
			got := fromBuildInfo(build)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("identity = %+v, want %+v", got, test.want)
			}
			wire, err := json.Marshal(got)
			if err != nil || strings.Contains(string(wire)+fmt.Sprintf("%+v", got), "secret-marker") {
				t.Fatal("unrelated/private build metadata escaped", err)
			}
			build.Main.Version = "v9.9.9"
			if got != test.want {
				t.Fatal("returned value retained build metadata ownership")
			}
		})
	}
}

func TestMissingAmbiguousAndUnsafeBuildMetadataStaysUnknown(t *testing.T) {
	for _, build := range []*debug.BuildInfo{
		nil,
		{},
		{Main: debug.Module{Path: "example.com/other", Version: "v1.0.0"}},
		{Deps: []*debug.Module{{Path: Module, Version: "v1.0.0"}, {Path: Module, Version: "v2.0.0"}}},
		{Main: debug.Module{Path: Module, Version: "/private/version"}},
		{Main: debug.Module{Path: Module, Version: "v" + strings.Repeat("1", 256)}},
	} {
		if got := fromBuildInfo(build); got != (Info{Module: Module, Origin: Unknown}) {
			t.Fatalf("invalid metadata acquired identity: %+v", got)
		}
	}
	for _, path := range []string{"/private/absolute", "../relative", "C:\\private", "example.com//fork", "example.com/../fork", "example.com/fork\nsecret", strings.Repeat("x", 1025)} {
		got := fromBuildInfo(&debug.BuildInfo{Deps: []*debug.Module{{Path: Module, Version: "v1.0.0", Replace: &debug.Module{Path: path, Version: "v2.0.0"}}}})
		if got.Origin != Unknown || got.Version != "" || got.ReplacementModule != "" {
			t.Fatalf("unsafe replacement acquired identity: %+v", got)
		}
	}
	got := fromBuildInfo(&debug.BuildInfo{Main: debug.Module{Path: Module, Version: "(devel)"},
		GoVersion: "go1.26.5\nsecret", Settings: []debug.BuildSetting{{Key: "vcs.revision", Value: "\x1b[31msecret"}}})
	if got.GoVersion != "" || got.Revision != "" || got.Origin != Development {
		t.Fatalf("unsafe display metadata retained: %+v", got)
	}
}
