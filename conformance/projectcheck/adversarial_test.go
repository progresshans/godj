//go:build darwin || linux

package projectcheck

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

func TestInvalidNearestDescriptorDoesNotFallBackToOuterProject(t *testing.T) {
	t.Parallel()
	outer := t.TempDir()
	inner := filepath.Join(outer, "inner")
	cwd := filepath.Join(inner, "child")
	writeDescriptor(t, outer)
	mustMkdir(t, cwd)
	if err := os.WriteFile(filepath.Join(inner, "godj.toml"), []byte("format_version = 2\nunknown = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := oracleMetrics{}
	_, primary := selectProject(cwd, commandArguments{}, &metrics, contractLimits(), selectionHooks{})
	if primary == nil || primary.Code != "invalid_project_descriptor" || metrics.AncestorDirectoriesInspected != 2 || metrics.DescriptorReads != 1 {
		t.Fatalf("nearest invalid selection = %v metrics=%+v", primary, metrics)
	}
}

func TestMarkerRawCaseAndNoFollowSemantics(t *testing.T) {
	t.Parallel()
	t.Run("wrong-case-is-absent", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "GODJ.TOML"), canonicalDescriptor("./cmd/site"), 0o600); err != nil {
			t.Fatal(err)
		}
		metrics := oracleMetrics{}
		_, primary := selectProject(root, commandArguments{}, &metrics, contractLimits(), selectionHooks{virtualFilesystemRoot: root})
		if primary == nil || primary.Code != "project_not_found" || metrics.DescriptorReads != 0 {
			t.Fatalf("wrong-case marker = %v metrics=%+v", primary, metrics)
		}
	})
	t.Run("symlink-is-invalid-not-followed", func(t *testing.T) {
		root := t.TempDir()
		target := filepath.Join(root, "target")
		if err := os.WriteFile(target, canonicalDescriptor("./cmd/site"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(root, "godj.toml")); err != nil {
			t.Fatal(err)
		}
		metrics := oracleMetrics{}
		_, primary := selectProject(root, commandArguments{}, &metrics, contractLimits(), selectionHooks{})
		if primary == nil || primary.Code != "invalid_project_descriptor" || metrics.DescriptorReads != 0 {
			t.Fatalf("symlink marker = %v metrics=%+v", primary, metrics)
		}
	})
}

func TestSelectedDescriptorIdentityRaceBeatsStableInvalidBytes(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	hooks := selectionHooks{afterDescriptorStat: func(parentFD int, name string) {
		if err := unix.Unlinkat(parentFD, name, 0); err != nil {
			t.Errorf("unlink selected descriptor: %v", err)
			return
		}
		if err := unix.Symlinkat("missing-target", parentFD, name); err != nil {
			t.Errorf("replace descriptor with symlink: %v", err)
		}
	}}
	metrics := oracleMetrics{}
	_, primary := selectProject(root, commandArguments{}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "project_selection_failed" || metrics.DescriptorReads != 0 {
		t.Fatalf("descriptor race = %v metrics=%+v", primary, metrics)
	}
}

func TestMarkerDisappearanceAfterRawScanIsSelectionFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	hooks := selectionHooks{afterMarkerScan: func(parentFD int, name string) {
		if err := unix.Unlinkat(parentFD, name, 0); err != nil {
			t.Errorf("unlink scanned marker: %v", err)
		}
	}}
	metrics := oracleMetrics{}
	_, primary := selectProject(root, commandArguments{}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "project_selection_failed" || metrics.DescriptorReads != 0 {
		t.Fatalf("scanned marker disappearance = %v metrics=%+v", primary, metrics)
	}
}

func TestImplicitAncestorRetainedHandleRejectsPathRename(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	cwd := filepath.Join(root, "child")
	mustMkdir(t, cwd)
	mutated := false
	hooks := selectionHooks{afterAncestorScan: func(path string, _ int, _ bool) {
		if mutated {
			return
		}
		mutated = true
		if err := os.Rename(path, path+"-moved"); err != nil {
			t.Errorf("rename retained ancestor: %v", err)
		}
	}}
	metrics := oracleMetrics{}
	_, primary := selectProject(cwd, commandArguments{}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "project_selection_failed" || metrics.AncestorDirectoriesInspected != 1 {
		t.Fatalf("ancestor rename = %v metrics=%+v", primary, metrics)
	}
}

func TestExplicitParentReplacementBetweenResolveAndOpenIsSelectionFailure(t *testing.T) {
	t.Parallel()
	universe := t.TempDir()
	project := filepath.Join(universe, "project")
	writeDescriptor(t, project)
	hooks := selectionHooks{beforeExplicitOpen: func(path string) {
		if err := os.Rename(path, path+"-old"); err != nil {
			t.Errorf("rename explicit parent: %v", err)
			return
		}
		writeDescriptor(t, path)
	}}
	metrics := oracleMetrics{}
	_, primary := selectProject(universe, commandArguments{ExplicitDescriptor: filepath.Join(project, "godj.toml")}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "project_selection_failed" || metrics.AncestorDirectoriesInspected != 0 {
		t.Fatalf("explicit parent replace = %v metrics=%+v", primary, metrics)
	}
}

func TestExplicitDescriptorWithExistingNonDirectoryParentIsInvalidInput(t *testing.T) {
	t.Parallel()
	universe := t.TempDir()
	nonDirectory := filepath.Join(universe, "not-a-directory")
	if err := os.WriteFile(nonDirectory, []byte("ordinary file"), 0o600); err != nil {
		t.Fatal(err)
	}
	metrics := oracleMetrics{}
	_, primary := selectProject(universe, commandArguments{ExplicitDescriptor: filepath.Join(nonDirectory, "godj.toml")}, &metrics, contractLimits(), selectionHooks{})
	if primary == nil || primary.Category != "migration_project_selection_error" || primary.Code != "invalid_project_descriptor" || primary.ExitCode != 2 || metrics.AncestorDirectoriesInspected != 0 || metrics.DescriptorReads != 0 {
		t.Fatalf("explicit non-directory parent = %v metrics=%+v", primary, metrics)
	}
}

func TestParentIdentityRaceOverridesInvalidDescriptorBytes(t *testing.T) {
	t.Parallel()
	universe := t.TempDir()
	project := filepath.Join(universe, "project")
	mustMkdir(t, project)
	if err := os.WriteFile(filepath.Join(project, "godj.toml"), []byte("format_version = 2\nunknown = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	hooks := selectionHooks{afterDescriptorStat: func(_ int, _ string) {
		if err := os.Rename(project, project+"-old"); err != nil {
			t.Errorf("rename descriptor parent: %v", err)
			return
		}
		mustMkdir(t, project)
	}}
	metrics := oracleMetrics{}
	_, primary := selectProject(project, commandArguments{}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "project_selection_failed" || metrics.DescriptorReads != 1 {
		t.Fatalf("parent race plus invalid descriptor = %v metrics=%+v", primary, metrics)
	}
}

func TestSelectedProjectRootReplacementBeforeBuildFailsClosed(t *testing.T) {
	t.Parallel()
	universe := t.TempDir()
	project := filepath.Join(universe, "project")
	writeDescriptor(t, project)
	tempBase := filepath.Join(universe, "temp")
	mustMkdir(t, tempBase)
	create := successfulTestWorkspace(tempBase)
	workspace := func(projectRoot string, observed *observation) (privateWorkspace, *failure) {
		created, primary := create(projectRoot, observed)
		if primary != nil {
			return created, primary
		}
		if err := os.Rename(projectRoot, projectRoot+"-old"); err != nil {
			t.Errorf("rename selected project: %v", err)
			return created, primary
		}
		writeDescriptor(t, projectRoot)
		return created, nil
	}
	observed := runGlobal(globalInvocation{Context: context.Background(), CWD: project, Argv: []string{"migrations", "check"}, Limits: contractLimits(), Deps: globalDependencies{Backend: &inProcessBackend{Limits: contractLimits()}, CreateWorkspace: workspace}})
	if observed.Failure == nil || observed.Failure.Code != "project_selection_failed" || observed.Metrics.BuildCalls != 0 || observed.Feasibility.TempCleanupAttempts != 1 {
		t.Fatalf("project root replacement = %+v", observed)
	}
}

func TestRootPreflightIsSortedNoFollowAndCompletesBeforeEnumeration(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "valid"))
	if err := os.Symlink(filepath.Join(root, "valid"), filepath.Join(root, "unsafe")); err != nil {
		t.Fatal(err)
	}
	enumerations := 0
	metrics := oracleMetrics{}
	_, primary := discoverSources(root, []string{"valid", "unsafe"}, &metrics, contractLimits(), discoveryHooks{enumerateRoot: func(string, *os.File, func([]enumeratedEntry, error) bool) error {
		enumerations++
		return nil
	}})
	if primary == nil || primary.Code != "invalid_source_root" || enumerations != 0 || metrics.RootsOpened != 0 {
		t.Fatalf("root preflight = %v metrics=%+v enumerations=%d", primary, metrics, enumerations)
	}
}

func TestSelectedRootRaceIsDiscoveryFailure(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "migrations"))
	hooks := discoveryHooks{afterRootInitialStat: func(parentFD int, name string) {
		if err := unix.Renameat(parentFD, name, parentFD, name+"-moved"); err != nil {
			t.Errorf("rename root: %v", err)
		}
	}}
	metrics := oracleMetrics{}
	_, primary := discoverSources(root, []string{"migrations"}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "source_discovery_failed" || metrics.RootsOpened != 0 {
		t.Fatalf("root race = %v metrics=%+v", primary, metrics)
	}
}

func TestProjectRootReplacementBetweenResolveStatAndRetainedOpenIsDiscoveryFailure(t *testing.T) {
	t.Parallel()
	universe := t.TempDir()
	root := filepath.Join(universe, "project")
	mustMkdir(t, filepath.Join(root, "migrations"))
	hooks := discoveryHooks{beforeProjectRootOpen: func(path string) {
		if err := os.Rename(path, path+"-old"); err != nil {
			t.Errorf("rename resolved project root: %v", err)
			return
		}
		mustMkdir(t, filepath.Join(path, "migrations"))
	}}
	metrics := oracleMetrics{}
	_, primary := discoverSources(root, []string{"migrations"}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "source_discovery_failed" || metrics.RootsOpened != 0 || metrics.DirectoryEntriesSeen != 0 {
		t.Fatalf("project root replacement = %v metrics=%+v", primary, metrics)
	}
}

func TestFlatDiscoveryIgnoresNestedAndNonmatchingNonregularEntries(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "migrations", "nested"))
	writeDefinition(t, root, "migrations/nested/hidden.godj.json", oneCreateModelDocument())
	if err := os.Symlink("missing", filepath.Join(root, "migrations", "ignored.link")); err != nil {
		t.Fatal(err)
	}
	metrics := oracleMetrics{}
	linked := invokeLinked(linkedInvocation{ProjectRoot: root, Roots: []string{"migrations"}, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Metrics: &metrics})
	result, primary := parseRunnerResponse(linked.Wire, 0, contractLimits())
	if primary != nil || result.SourceCount != 0 || metrics.SourceReads != 0 || metrics.LoadCalls != 1 || metrics.DirectoryEntriesSeen != 2 {
		t.Fatalf("flat discovery = result=%+v primary=%v metrics=%+v", result, primary, metrics)
	}
}

func TestRawCandidateSuffixIncludesEmptyStemAndRejectsNearMisses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		want bool
	}{
		{".godj.json", true},
		{"a.godj.json", true},
		{".GODJ.JSON", false},
		{"godj.json", false},
		{"a.godj.json.tmp", false},
	}
	for _, test := range cases {
		if actual := isDefinitionCandidate([]byte(test.name)); actual != test.want {
			t.Fatalf("candidate %q = %v, want %v", test.name, actual, test.want)
		}
	}
	root := t.TempDir()
	writeDefinition(t, root, "migrations/.godj.json", oneCreateModelDocument())
	metrics := oracleMetrics{}
	linked := invokeLinked(linkedInvocation{ProjectRoot: root, Roots: []string{"migrations"}, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Metrics: &metrics})
	result, primary := parseRunnerResponse(linked.Wire, 0, contractLimits())
	if primary != nil || result.SourceCount != 1 || metrics.SourceReads != 1 || metrics.LoadCalls != 1 {
		t.Fatalf("empty-stem candidate = result=%+v primary=%v metrics=%+v", result, primary, metrics)
	}
}

func TestMatchingDirectoryAndSymlinkAreUnsafeBeforeSourceCount(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			migrationsRoot := filepath.Join(root, "migrations")
			mustMkdir(t, migrationsRoot)
			writeDefinition(t, root, "migrations/z.godj.json", oneCreateModelDocument())
			unsafePath := filepath.Join(migrationsRoot, "a.godj.json")
			if kind == "directory" {
				mustMkdir(t, unsafePath)
			} else if err := os.Symlink("z.godj.json", unsafePath); err != nil {
				t.Fatal(err)
			}
			lim := contractLimits()
			lim.sources = 1
			metrics := oracleMetrics{}
			_, primary := discoverSources(root, []string{"migrations"}, &metrics, lim, discoveryHooks{})
			if primary == nil || primary.Code != "unsafe_source_entry" || metrics.SourceReads != 0 {
				t.Fatalf("%s precedence = %v metrics=%+v", kind, primary, metrics)
			}
		})
	}
}

func TestInvalidByteCandidateUsesHexAndSourceIDCapPrecedesUTF8(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	migrationsRoot := filepath.Join(root, "migrations")
	mustMkdir(t, migrationsRoot)
	name := string([]byte{0xff}) + ".godj.json"
	hooks := discoveryHooks{enumerateRoot: func(_ string, _ *os.File, yield func([]enumeratedEntry, error) bool) error {
		yield([]enumeratedEntry{{name: name}}, io.EOF)
		return nil
	}}
	metrics := oracleMetrics{}
	result, primary := discoverSources(root, []string{"migrations"}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "invalid_source_entry" || result.pathHex == "" || bytes.Contains([]byte(result.pathHex), []byte{0xff}) {
		t.Fatalf("invalid byte candidate = result=%+v primary=%v", result, primary)
	}
	lim := contractLimits()
	lim.sourceIDBytes = 4
	metrics = oracleMetrics{}
	_, primary = discoverSources(root, []string{"migrations"}, &metrics, lim, hooks)
	if primary == nil || primary.Code != "source_catalog_limit_exceeded" {
		t.Fatalf("SourceID cap did not precede UTF-8: %v", primary)
	}
}

func TestDirectoryReadErrorPrecedesEntriesAndEntryCap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "migrations"))
	lim := contractLimits()
	lim.entries = 0
	metrics := oracleMetrics{}
	_, primary := discoverSources(root, []string{"migrations"}, &metrics, lim, discoveryHooks{enumerateRoot: func(_ string, _ *os.File, yield func([]enumeratedEntry, error) bool) error {
		yield([]enumeratedEntry{{name: "noise.txt"}}, syscall.EIO)
		return nil
	}})
	if primary == nil || primary.Code != "source_discovery_failed" || metrics.DirectoryEntriesSeen != 0 {
		t.Fatalf("entries+error precedence = %v metrics=%+v", primary, metrics)
	}
}

func TestPostReadIdentitySafetyPrecedesReadErrorAndSizeCap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDefinition(t, root, "migrations/a.godj.json", bytes.Repeat([]byte("x"), 64))
	lim := contractLimits()
	lim.documentBytes = 4
	hooks := discoveryHooks{
		readCandidate: func(_ string, _ *os.File, maximum uint64) ([]byte, error) {
			return bytes.Repeat([]byte("x"), int(maximum)+1), io.ErrUnexpectedEOF
		},
		afterCandidateRead: func(rootFD int, name string) {
			if err := unix.Unlinkat(rootFD, name, 0); err != nil {
				t.Errorf("unlink candidate: %v", err)
				return
			}
			if err := unix.Symlinkat("missing", rootFD, name); err != nil {
				t.Errorf("replace candidate: %v", err)
			}
		},
	}
	metrics := oracleMetrics{}
	_, primary := discoverSources(root, []string{"migrations"}, &metrics, lim, hooks)
	if primary == nil || primary.Code != "unsafe_source_entry" || metrics.SourceReads != 0 || metrics.LoadCalls != 0 {
		t.Fatalf("post-read safety precedence = %v metrics=%+v", primary, metrics)
	}
}

func TestStableReadErrorPrecedesSizeCap(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDefinition(t, root, "migrations/a.godj.json", oneCreateModelDocument())
	lim := contractLimits()
	lim.documentBytes = 4
	hooks := discoveryHooks{readCandidate: func(_ string, _ *os.File, maximum uint64) ([]byte, error) {
		return bytes.Repeat([]byte("x"), int(maximum)+1), io.ErrUnexpectedEOF
	}}
	metrics := oracleMetrics{}
	_, primary := discoverSources(root, []string{"migrations"}, &metrics, lim, hooks)
	if primary == nil || primary.Code != "source_read_failed" {
		t.Fatalf("stable read error precedence = %v", primary)
	}
}

func TestHardlinksAreRegularAndReachLoaderGraphValidation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDefinition(t, root, "migrations/a.godj.json", oneCreateModelDocument())
	if err := os.Link(filepath.Join(root, "migrations", "a.godj.json"), filepath.Join(root, "migrations", "b.godj.json")); err != nil {
		t.Fatal(err)
	}
	metrics := oracleMetrics{}
	linked := invokeLinked(linkedInvocation{ProjectRoot: root, Roots: []string{"migrations"}, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Metrics: &metrics})
	_, primary := parseRunnerResponse(linked.Wire, 0, contractLimits())
	if primary == nil || primary.Category != "migration_graph_error" || primary.Code != "duplicate_node" || metrics.SourceReads != 2 || metrics.LoadCalls != 1 || metrics.PlannerConstruction != 1 {
		t.Fatalf("hardlink graph outcome = %v metrics=%+v", primary, metrics)
	}
}

func TestCandidateFaultWinnerUsesRawFullSourceIDOrder(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "z"))
	mustMkdir(t, filepath.Join(root, "a"))
	if err := os.Symlink("missing", filepath.Join(root, "z", "unsafe.godj.json")); err != nil {
		t.Fatal(err)
	}
	invalid := string([]byte{0xff}) + ".godj.json"
	hooks := discoveryHooks{enumerateRoot: func(logical string, _ *os.File, yield func([]enumeratedEntry, error) bool) error {
		if logical == "a" {
			yield([]enumeratedEntry{{name: invalid}}, io.EOF)
		} else {
			yield([]enumeratedEntry{{name: "unsafe.godj.json"}}, io.EOF)
		}
		return nil
	}}
	metrics := oracleMetrics{}
	result, primary := discoverSources(root, []string{"z", "a"}, &metrics, contractLimits(), hooks)
	if primary == nil || primary.Code != "invalid_source_entry" || result.pathHex == "" {
		t.Fatalf("raw winner outcome = result=%+v primary=%v", result, primary)
	}
}

func TestCleanupFailureOverridesSuccessButNotNonCancelPrimary(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDescriptor(t, root)
	mustMkdir(t, filepath.Join(root, "migrations"))
	backend := &inProcessBackend{Roots: []string{"migrations"}, Limits: contractLimits()}
	workspace := func(_ string, observed *observation) (privateWorkspace, *failure) {
		observed.Feasibility.TempCreated++
		return privateWorkspace{Root: t.TempDir(), Cleanup: func() error { return errors.New("injected") }}, nil
	}
	success := runGlobal(globalInvocation{Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(), Deps: globalDependencies{Backend: backend, CreateWorkspace: workspace}})
	if success.Failure == nil || success.Failure.Code != "project_cleanup_failed" || success.Result != nil || success.Feasibility.CleanupFailed != 1 || success.Feasibility.ResidualTemp != 1 {
		t.Fatalf("cleanup-over-success = %+v", success)
	}
	backend = &inProcessBackend{BuildFailure: true, Limits: contractLimits()}
	buildFailure := runGlobal(globalInvocation{Context: context.Background(), CWD: root, Argv: []string{"migrations", "check"}, Limits: contractLimits(), Deps: globalDependencies{Backend: backend, CreateWorkspace: workspace}})
	if buildFailure.Failure == nil || buildFailure.Failure.Code != "project_build_failed" || buildFailure.Feasibility.CleanupFailed != 1 || buildFailure.Feasibility.ResidualTemp != 1 {
		t.Fatalf("cleanup-over-primary = %+v", buildFailure)
	}
}

func TestDirectDBPlannerAndLifecycleCountersStayZeroAcrossMutations(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	writeDefinition(t, root, "migrations/a.godj.json", oneCreateModelDocument())
	metrics := oracleMetrics{}
	_ = invokeLinked(linkedInvocation{ProjectRoot: root, Roots: []string{"migrations"}, Request: []byte(`{"protocol_version":1,"command":"migrations.check"}`), Limits: contractLimits(), Metrics: &metrics})
	if !reflect.DeepEqual([]int{metrics.DirectPlannerCalls, metrics.GoDjDBCalls, metrics.RevisionLifecycleCalls}, []int{0, 0, 0}) || metrics.PlannerConstruction != 1 {
		t.Fatalf("DB-free counters = %+v", metrics)
	}
}
