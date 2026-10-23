//go:build darwin || linux

package projectcheck

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func sourceFixture(t *testing.T) (retainedProject, []makemigrationsGoListPackage, string) {
	t.Helper()
	fixture := newRunserverFixture(t, 0)
	selected, failed := selectProject(fixture.project, commandArguments{}, &Report{})
	if failed != nil {
		t.Fatal(failed)
	}
	t.Cleanup(func() { _ = selected.close() })
	replacement, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	packages := []makemigrationsGoListPackage{
		{Dir: filepath.Join(selected.rootPath, "cmd", "site"), ImportPath: "example.com/site/cmd/site", Name: "main", Module: &makemigrationsGoListModule{Path: "example.com/site", Dir: selected.rootPath, Main: true}},
		{Dir: filepath.Join(selected.rootPath, "cmd", "runtime"), ImportPath: "example.com/site/cmd/runtime", Name: "main", Module: &makemigrationsGoListModule{Path: "example.com/site", Dir: selected.rootPath, Main: true}, EmbedPatterns: []string{"templates/*.txt"}, EmbedFiles: []string{"templates/message.txt"}},
		{Dir: replacement, ImportPath: "example.com/other", Name: "other", Module: &makemigrationsGoListModule{Path: "example.com/other", Version: "v0.0.0", Replace: &makemigrationsGoListModule{Path: replacement, Dir: replacement}}},
	}
	for _, name := range []string{filepath.Join(selected.rootPath, "go.mod"), filepath.Join(replacement, "go.mod"), filepath.Join(replacement, "library.go"), filepath.Join(packages[0].Dir, "main.go"), filepath.Join(packages[1].Dir, "main.go"), filepath.Join(packages[1].Dir, "templates", "message.txt")} {
		if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("initial-source\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return selected, packages, replacement
}

func sourcePlan(t *testing.T, selected retainedProject, packages []makemigrationsGoListPackage) *runserverSource {
	t.Helper()
	var document []byte
	for _, pkg := range packages {
		raw, err := json.Marshal(pkg)
		if err != nil {
			t.Fatal(err)
		}
		document = append(append(document, raw...), '\n')
	}
	plan, err := planRunserverSource(selected, document)
	if err != nil {
		t.Fatal(err)
	}
	plan.digest, err = plan.observe(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRunserverSourceOwnsCompilerInputsEmbeddedMatchesAndMutableDependencies(t *testing.T) {
	for _, mutation := range []string{"same-size-same-mtime", "new-source", "removed-source", "new-embed", "replaced-embed", "replacement-source", "dependency-graph", "module", "manifest", "descriptor"} {
		t.Run(mutation, func(t *testing.T) {
			selected, packages, replacement := sourceFixture(t)
			plan := sourcePlan(t, selected, packages)
			name := filepath.Join(packages[1].Dir, "main.go")
			switch mutation {
			case "new-source":
				name = filepath.Join(packages[1].Dir, "new.go")
			case "removed-source":
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
			case "new-embed":
				name = filepath.Join(packages[1].Dir, "templates", "added.txt")
			case "replaced-embed":
				name = filepath.Join(packages[1].Dir, "templates", "message.txt")
			case "replacement-source":
				name = filepath.Join(replacement, "library.go")
			case "module":
				name = filepath.Join(replacement, "go.mod")
			case "manifest":
				name = filepath.Join(selected.rootPath, ".godj", "generated-manifest.json")
				if err := os.MkdirAll(filepath.Dir(name), 0o700); err != nil {
					t.Fatal(err)
				}
			case "descriptor":
				name = filepath.Join(selected.rootPath, descriptorName)
			case "dependency-graph":
				plan.graph = append(plan.graph, ' ')
			}
			if mutation != "removed-source" && mutation != "dependency-graph" {
				old, _ := os.Stat(name)
				if err := os.WriteFile(name, []byte("changed-source\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				if mutation == "same-size-same-mtime" {
					if err := os.Chtimes(name, old.ModTime(), old.ModTime()); err != nil {
						t.Fatal(err)
					}
				}
			}
			if current, err := plan.observe(t.Context()); err != nil || current == plan.digest {
				t.Fatal("changed build inputs accepted", mutation, err)
			}
		})
	}
}

func TestRunserverSourceExcludesDBWritesAndOwnsMissingPackageRecovery(t *testing.T) {
	selected, packages, _ := sourceFixture(t)
	missing := filepath.Join(selected.rootPath, "missing")
	packages = append(packages, makemigrationsGoListPackage{ImportPath: "example.com/site/missing", Incomplete: true, Error: json.RawMessage(`{"Err":"missing"}`)})
	plan := sourcePlan(t, selected, packages)
	if plan.complete {
		t.Fatal("incomplete source is ready for publication")
	}
	for _, name := range []string{"data.sqlite3", "data.sqlite3-wal", "data.sqlite3-shm", "new_test.go"} {
		if err := os.WriteFile(filepath.Join(packages[1].Dir, name), []byte("committed database or test bytes"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if current, err := plan.observe(t.Context()); err != nil || current != plan.digest {
		t.Fatal("runtime data or test edits triggered reload", err)
	}
	if err := os.Mkdir(missing, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(missing, "new.go"), []byte("package missing\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if current, err := plan.observe(t.Context()); err != nil || current == plan.digest {
		t.Fatal("newly created imported package was not observed", err)
	}
}

func TestRunserverSourceRejectsReboundPathsPendingPublicationAndUnboundedFiles(t *testing.T) {
	for _, mutation := range []string{"source-symlink", "package-symlink", "root-replacement", "pending-publication", "oversized-file", "canceled"} {
		t.Run(mutation, func(t *testing.T) {
			selected, packages, _ := sourceFixture(t)
			plan := sourcePlan(t, selected, packages)
			ctx := t.Context()
			switch mutation {
			case "source-symlink":
				name := filepath.Join(packages[1].Dir, "main.go")
				if err := os.Remove(name); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(packages[0].Dir, "main.go"), name); err != nil {
					t.Fatal(err)
				}
			case "package-symlink":
				if err := os.Rename(packages[1].Dir, packages[1].Dir+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(packages[1].Dir+"-old", packages[1].Dir); err != nil {
					t.Fatal(err)
				}
			case "root-replacement":
				if err := os.Rename(selected.rootPath, selected.rootPath+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(selected.rootPath, 0o700); err != nil {
					t.Fatal(err)
				}
			case "pending-publication":
				if err := os.MkdirAll(filepath.Join(selected.rootPath, ".godj", "transactions", "pending"), 0o700); err != nil {
					t.Fatal(err)
				}
			case "oversized-file":
				file, err := os.OpenFile(filepath.Join(packages[1].Dir, "main.go"), os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				if err := file.Truncate(int64(defaultMakemigrationsBuildInputLimits().fileBytes) + 1); err != nil {
					file.Close()
					t.Fatal(err)
				}
				file.Close()
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if _, err := plan.observe(ctx); err == nil {
				t.Fatal("unsafe source accepted")
			}
		})
	}
}

func TestRunserverSourceInventoryRequiresBothEntrypointsAndBoundModules(t *testing.T) {
	selected, packages, _ := sourceFixture(t)
	for _, mutated := range [][]makemigrationsGoListPackage{packages[:1], append(append([]makemigrationsGoListPackage{}, packages...), packages[0]), append(append([]makemigrationsGoListPackage{}, packages...), makemigrationsGoListPackage{ImportPath: "unbound.example/private", Dir: "/unbound"})} {
		var document strings.Builder
		for _, pkg := range mutated {
			raw, _ := json.Marshal(pkg)
			document.Write(raw)
			document.WriteByte('\n')
		}
		if _, err := planRunserverSource(selected, []byte(document.String())); err == nil {
			t.Fatal("incomplete or ambiguous inventory accepted")
		}
	}
}
