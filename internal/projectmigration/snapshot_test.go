package projectmigration

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/schema/ir"
)

func TestBuildSnapshotFreshCleanAndCrossAppTopology(t *testing.T) {
	t.Run("empty project", func(t *testing.T) {
		spec := codegen.ProjectSpec{Project: codegen.PackageSpec{
			PackageName: "project",
			ImportPath:  "example.com/project/project",
			Directory:   "project",
		}}
		snapshot := mustBuildSnapshot(t, Request{ProjectSpec: spec, WriterRoot: "migrations"})
		if len(snapshot.ProjectSpec().Apps) != 0 || snapshot.ProjectSpec().Apps == nil || len(snapshot.Candidates()) != 0 {
			t.Fatalf("empty snapshot = apps %#v candidates %#v", snapshot.ProjectSpec().Apps, snapshot.Candidates())
		}
		if snapshot.ExistingSemanticDigest() != definition.EmptySetDigest || snapshot.FinalSemanticDigest() != definition.EmptySetDigest {
			t.Fatal("empty project did not preserve initialized empty definition set")
		}
	})

	t.Run("fresh initial", func(t *testing.T) {
		spec := testProjectSpec(testSchema("content", testModel("article", testChar("title", false))))
		snapshot := mustBuildSnapshot(t, Request{ProjectSpec: spec, WriterRoot: "migrations"})
		if !snapshot.Initialized() {
			t.Fatal("snapshot is not initialized")
		}
		if !strings.HasPrefix(snapshot.ProjectSpecDigest(), "sha256:") || len(snapshot.GeneratedBundleSnapshotSHA256()) != 64 ||
			strings.TrimPrefix(snapshot.ProjectSpecDigest(), "sha256:") == snapshot.GeneratedBundleSnapshotSHA256() {
			t.Fatal("project semantic and generated-bundle identities are not distinct valid fields")
		}
		candidates := snapshot.Candidates()
		if len(candidates) != 1 || candidates[0].App() != "content" || candidates[0].Name() != "0001_initial" {
			t.Fatalf("fresh candidates = %#v", candidateIdentities(candidates))
		}
		loaded := mustLoadSources(t, definition.Source{
			SourceID: "migrations/content_0001_initial.godj.json",
			Document: candidates[0].Document(),
		})
		info := loaded.Sources()
		if len(info) != 1 || info[0].Producer != (definition.Producer{Name: candidateProducerName, Version: candidateProducerVersion}) {
			t.Fatalf("candidate producer = %#v", info)
		}
		if snapshot.ExistingSemanticDigest() == snapshot.FinalSemanticDigest() {
			t.Fatal("fresh candidate did not change final semantic digest")
		}
	})

	t.Run("clean", func(t *testing.T) {
		schema := testSchema("content", testModel("article", testChar("title", false)))
		source := initialSource(t, "migrations/content_0001_initial.godj.json", schema, definition.Producer{Name: "fixture", Version: "1"})
		snapshot := mustBuildSnapshot(t, Request{
			ProjectSpec:       testProjectSpec(schema),
			FilesystemSources: []definition.Source{source},
			WriterRoot:        "migrations",
		})
		if len(snapshot.Candidates()) != 0 {
			t.Fatalf("clean candidates = %#v", candidateIdentities(snapshot.Candidates()))
		}
		if snapshot.ExistingSemanticDigest() != snapshot.FinalSemanticDigest() {
			t.Fatal("clean snapshot changed semantic digest")
		}
	})

	t.Run("cross app", func(t *testing.T) {
		authors := testSchema("zeta", testModel("author", testChar("name", false)))
		content := testSchema("alpha", testModel("entry",
			testChar("title", false),
			testForeignKey("author", true, "zeta", "author"),
		))
		snapshot := mustBuildSnapshot(t, Request{
			ProjectSpec: testProjectSpec(content, authors),
			WriterRoot:  "migrations",
		})
		candidates := snapshot.Candidates()
		if got := candidateIdentities(candidates); !reflect.DeepEqual(got, []string{"zeta.0001_initial", "alpha.0001_initial"}) {
			t.Fatalf("cross-app candidates = %#v", got)
		}
		prefix := make([]definition.Source, 0, len(candidates))
		for _, candidate := range candidates {
			prefix = append(prefix, definition.Source{
				SourceID: "migrations/" + candidate.App() + "_" + candidate.Name() + ".godj.json",
				Document: candidate.Document(),
			})
			loaded := mustLoadSources(t, prefix...)
			if _, err := reconstructLatest(loaded); err != nil {
				t.Fatalf("reconstruct strict prefix %d: %v", len(prefix), err)
			}
		}
		final := mustLoadSources(t, prefix...)
		state, err := reconstructLatest(final)
		if err != nil {
			t.Fatal(err)
		}
		if !state.Equal(snapshot.DesiredState()) || final.Digest() != snapshot.FinalSemanticDigest() {
			t.Fatal("strict final catalog differs from snapshot authority")
		}
	})
}

func TestBuildSnapshotManagedOwnershipPreservesProgrammaticOnlyHistory(t *testing.T) {
	external := testSchema("external", testModel("token", testChar("value", false)))
	programmatic := initialSource(t, "embedded/external", external, definition.Producer{Name: "embedded", Version: "1"})
	content := testSchema("content", testModel("article", testChar("title", false)))
	snapshot := mustBuildSnapshot(t, Request{
		ProjectSpec:         testProjectSpec(content),
		ProgrammaticSources: []definition.Source{programmatic},
		WriterRoot:          "migrations",
	})
	if got := snapshot.ManagedApps(); !reflect.DeepEqual(got, []string{"content"}) {
		t.Fatalf("managed apps = %#v", got)
	}
	if got := candidateIdentities(snapshot.Candidates()); !reflect.DeepEqual(got, []string{"content.0001_initial"}) {
		t.Fatalf("candidates = %#v", got)
	}
	combined := append(snapshot.ProgrammaticSources(), definition.Source{
		SourceID: "migrations/content_0001_initial.godj.json",
		Document: snapshot.Candidates()[0].Document(),
	})
	final := mustLoadSources(t, combined...)
	state, err := reconstructLatest(final)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := state.Schema("external"); !exists {
		t.Fatal("programmatic-only historical app was not preserved")
	}
}

func TestBuildSnapshotFilesystemHistoryIsManaged(t *testing.T) {
	historical := testSchema("legacy", testModel("entry", testChar("value", false)))
	_, err := BuildSnapshot(Request{
		ProjectSpec:       testProjectSpec(),
		FilesystemSources: []definition.Source{initialSource(t, "migrations/legacy_0001_initial.godj.json", historical, definition.Producer{Name: "fixture", Version: "1"})},
		WriterRoot:        "migrations",
	})
	assertSnapshotError(t, err, CategoryPlanning, CodeUnsupportedChange)
}

func TestBuildSnapshotSeparatesPhysicalProgrammaticAndSemanticDigests(t *testing.T) {
	schema := testSchema("content", testModel("article", testChar("title", false)))
	first := initialSource(t, "migrations/content_0001_initial.godj.json", schema, definition.Producer{Name: "producer-a", Version: "1"})
	second := initialSource(t, "migrations/content_0001_initial.godj.json", schema, definition.Producer{Name: "producer-b", Version: "1"})
	left := mustBuildSnapshot(t, Request{
		ProjectSpec:       testProjectSpec(schema),
		FilesystemSources: []definition.Source{first},
		WriterRoot:        "migrations",
	})
	right := mustBuildSnapshot(t, Request{
		ProjectSpec:       testProjectSpec(schema),
		FilesystemSources: []definition.Source{second},
		WriterRoot:        "migrations",
	})
	if left.ExistingSemanticDigest() != right.ExistingSemanticDigest() {
		t.Fatal("producer-only change altered semantic digest")
	}
	if left.FilesystemCatalogDigest() == right.FilesystemCatalogDigest() {
		t.Fatal("producer/raw-document change did not alter physical digest")
	}
	if digestSources(filesystemCatalogDigestDomain, []definition.Source{first}) == digestSources(programmaticCatalogDigestDomain, []definition.Source{first}) {
		t.Fatal("filesystem and programmatic domains are not separated")
	}
}

func TestCatalogDigestsLockDomainsCountAndBigEndianLengthFrames(t *testing.T) {
	sources := []definition.Source{
		{SourceID: "b", Document: []byte("x")},
		{SourceID: "a", Document: []byte{0x00, 0x01}},
	}
	if got, want := digestSources(filesystemCatalogDigestDomain, sources), "sha256:b40868408593f048a27214362fb78d19ead076d3d6406a4467a3cba0333c9418"; got != want {
		t.Fatalf("filesystem catalog digest = %q, want %q", got, want)
	}
	if got, want := digestSources(programmaticCatalogDigestDomain, sources), "sha256:ef5e6c44e45b7fc1cafce14544db6a276e48f3cbdc88e33b3e969f7ca92c5c97"; got != want {
		t.Fatalf("programmatic catalog digest = %q, want %q", got, want)
	}
}

func TestBuildSnapshotCanonicalizesAppAndSourceOrdering(t *testing.T) {
	alpha := testSchema("alpha", testModel("entry", testChar("value", false)))
	zeta := testSchema("zeta", testModel("entry", testChar("value", false)))
	// UTF-8 bytes for z sort before the leading bytes for é. This locks raw
	// SourceID byte order rather than locale or Unicode collation.
	alphaSource := initialSource(t, "migrations/z.godj.json", alpha, definition.Producer{Name: "fixture", Version: "1"})
	zetaSource := initialSource(t, "migrations/é.godj.json", zeta, definition.Producer{Name: "fixture", Version: "1"})
	first := mustBuildSnapshot(t, Request{
		ProjectSpec:       testProjectSpec(zeta, alpha),
		FilesystemSources: []definition.Source{zetaSource, alphaSource},
		WriterRoot:        "migrations",
	})
	second := mustBuildSnapshot(t, Request{
		ProjectSpec:       testProjectSpec(alpha, zeta),
		FilesystemSources: []definition.Source{alphaSource, zetaSource},
		WriterRoot:        "migrations",
	})
	if first.ProjectSpecDigest() != second.ProjectSpecDigest() ||
		first.GeneratedBundleSnapshotSHA256() != second.GeneratedBundleSnapshotSHA256() ||
		first.FilesystemCatalogDigest() != second.FilesystemCatalogDigest() ||
		first.ExistingSemanticDigest() != second.ExistingSemanticDigest() {
		t.Fatal("input permutation changed canonical snapshot identities")
	}
	apps := first.ProjectSpec().Apps
	if len(apps) != 2 || apps[0].Schema.AppLabel != "alpha" || apps[1].Schema.AppLabel != "zeta" {
		t.Fatalf("canonical project apps = %#v", apps)
	}
	if sources := first.FilesystemSources(); len(sources) != 2 || sources[0].SourceID != alphaSource.SourceID {
		t.Fatalf("canonical sources = %#v", sources)
	}
}

func TestBuildSnapshotDeepCopiesInputsAndAccessors(t *testing.T) {
	external := testSchema("external", testModel("entry", testChar("value", false)))
	source := initialSource(t, "embedded/external", external, definition.Producer{Name: "embedded", Version: "1"})
	request := Request{
		ProjectSpec:         testProjectSpec(testSchema("content", testModel("article", testChar("title", false)))),
		ProgrammaticSources: []definition.Source{source},
		WriterRoot:          "migrations",
	}
	snapshot := mustBuildSnapshot(t, request)
	originalSpecDigest := snapshot.ProjectSpecDigest()
	originalSource := append([]byte(nil), snapshot.ProgrammaticSources()[0].Document...)
	originalCandidate := append([]byte(nil), snapshot.Candidates()[0].Document()...)

	request.ProjectSpec.Apps[0].Alias = "mutated"
	request.ProjectSpec.Apps[0].Schema.Models[0].Fields[0].Name = "mutated"
	request.ProgrammaticSources[0].SourceID = "mutated"
	request.ProgrammaticSources[0].Document[0] ^= 0xff

	returnedSpec := snapshot.ProjectSpec()
	returnedSpec.Apps[0].Schema.Models[0].Fields[0].Name = "returned_mutation"
	returnedSources := snapshot.ProgrammaticSources()
	returnedSources[0].Document[0] ^= 0xff
	returnedCandidates := snapshot.Candidates()
	returnedCandidateDocument := returnedCandidates[0].Document()
	returnedCandidateDocument[0] ^= 0xff

	if snapshot.ProjectSpecDigest() != originalSpecDigest ||
		!bytes.Equal(snapshot.ProgrammaticSources()[0].Document, originalSource) ||
		!bytes.Equal(snapshot.Candidates()[0].Document(), originalCandidate) {
		t.Fatal("caller or accessor mutation changed snapshot authority")
	}
}

func TestBuildSnapshotRejectsInvalidInputsAndRedactsCause(t *testing.T) {
	validSpec := testProjectSpec(testSchema("content", testModel("article", testChar("title", false))))
	tests := []struct {
		name     string
		request  func() Request
		category ErrorCategory
		code     ErrorCode
	}{
		{
			name:     "writer root",
			request:  func() Request { return Request{ProjectSpec: validSpec, WriterRoot: "/absolute"} },
			category: CategoryRequest,
			code:     CodeInvalidWriterRoot,
		},
		{
			name: "project spec",
			request: func() Request {
				request := Request{ProjectSpec: cloneProjectSpec(validSpec), WriterRoot: "migrations"}
				request.ProjectSpec.Project.Directory = "project/../other"
				return request
			},
			category: CategoryProject,
			code:     CodeInvalidProjectSpec,
		},
		{
			name: "source",
			request: func() Request {
				return Request{
					ProjectSpec:       validSpec,
					FilesystemSources: []definition.Source{{SourceID: "private-source-id", Document: []byte("private-document-bytes")}},
					WriterRoot:        "migrations",
				}
			},
			category: CategoryCatalog,
			code:     CodeInvalidCatalog,
		},
		{
			name: "source count",
			request: func() Request {
				return Request{
					ProjectSpec:       validSpec,
					FilesystemSources: make([]definition.Source, definition.MaxSources+1),
					WriterRoot:        "migrations",
				}
			},
			category: CategoryCatalog,
			code:     CodeCatalogResourceLimit,
		},
		{
			name: "writer root resource limit",
			request: func() Request {
				return Request{ProjectSpec: codegen.ProjectSpec{Project: validSpec.Project}, WriterRoot: strings.Repeat("a", definition.MaxSourceIDBytes+1)}
			},
			category: CategoryRequest,
			code:     CodeInvalidWriterRoot,
		},
		{
			name: "candidate source id",
			request: func() Request {
				return Request{ProjectSpec: validSpec, WriterRoot: strings.Repeat("a", definition.MaxSourceIDBytes)}
			},
			category: CategoryCandidate,
			code:     CodeInvalidCandidateCatalog,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := BuildSnapshot(test.request())
			failure := assertSnapshotError(t, err, test.category, test.code)
			if errors.Unwrap(failure) == nil {
				t.Fatal("snapshot failure does not preserve an inspectable cause")
			}
			if strings.Contains(failure.Error(), "private-source-id") || strings.Contains(failure.Error(), "private-document-bytes") {
				t.Fatalf("Error() exposed source authority: %q", failure.Error())
			}
		})
	}
}

func TestBuildSnapshotSourceIDLimitBoundary(t *testing.T) {
	schema := testSchema("content", testModel("article", testChar("title", false)))
	document := initialSource(t, "temporary", schema, definition.Producer{Name: "fixture", Version: "1"}).Document
	atLimit := definition.Source{SourceID: strings.Repeat("a", definition.MaxSourceIDBytes), Document: document}
	if _, err := BuildSnapshot(Request{
		ProjectSpec:       testProjectSpec(schema),
		FilesystemSources: []definition.Source{atLimit},
		WriterRoot:        "migrations",
	}); err != nil {
		t.Fatalf("source ID at limit rejected: %v", err)
	}
	above := atLimit
	above.SourceID += "a"
	_, err := BuildSnapshot(Request{
		ProjectSpec:       testProjectSpec(schema),
		FilesystemSources: []definition.Source{above},
		WriterRoot:        "migrations",
	})
	assertSnapshotError(t, err, CategoryCatalog, CodeCatalogResourceLimit)
}

func TestBuildSnapshotWriterRootLimitBoundaryForNoop(t *testing.T) {
	project := codegen.ProjectSpec{Project: codegen.PackageSpec{
		PackageName: "project",
		ImportPath:  "example.com/project/project",
		Directory:   "project",
	}}
	if _, err := BuildSnapshot(Request{
		ProjectSpec: project,
		WriterRoot:  strings.Repeat("a", definition.MaxSourceIDBytes),
	}); err != nil {
		t.Fatalf("writer root at limit rejected: %v", err)
	}
	_, err := BuildSnapshot(Request{
		ProjectSpec: project,
		WriterRoot:  strings.Repeat("a", definition.MaxSourceIDBytes+1),
	})
	assertSnapshotError(t, err, CategoryRequest, CodeInvalidWriterRoot)
}

func testProjectSpec(schemas ...ir.Schema) codegen.ProjectSpec {
	spec := codegen.ProjectSpec{
		Project: codegen.PackageSpec{
			PackageName: "project",
			ImportPath:  "example.com/project/project",
			Directory:   "project",
		},
		Apps: make([]codegen.AppSpec, len(schemas)),
	}
	for index := range schemas {
		app := schemas[index].AppLabel
		spec.Apps[index] = codegen.AppSpec{
			Alias: app,
			Package: codegen.PackageSpec{
				PackageName: app,
				ImportPath:  "example.com/project/" + app,
				Directory:   app,
			},
			Schema: schemas[index],
		}
	}
	return spec
}

func testSchema(app string, models ...ir.Model) ir.Schema {
	return ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: app, Models: models}
}

func testModel(name string, fields ...ir.Field) ir.Model {
	return ir.Model{Name: name, GoName: strings.ToUpper(name[:1]) + name[1:], Fields: fields}
}

func testChar(name string, nullable bool) ir.Field {
	return ir.Field{Name: name, GoName: strings.ToUpper(name[:1]) + name[1:], Kind: ir.FieldChar, Nullable: nullable, MaxLength: 255}
}

func testForeignKey(name string, nullable bool, targetApp, targetModel string) ir.Field {
	return ir.Field{
		Name: name, GoName: strings.ToUpper(name[:1]) + name[1:] + "ID", Kind: ir.FieldForeignKey, Nullable: nullable,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: targetApp, ModelName: targetModel},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "entries"},
			OnDelete:    ir.DeleteProtect,
		},
	}
}

func initialSource(t *testing.T, sourceID string, schema ir.Schema, producer definition.Producer) definition.Source {
	t.Helper()
	normalized, err := ir.Normalize(schema)
	if err != nil {
		t.Fatalf("normalize initial schema: %v", err)
	}
	operations := make([]migrations.Operation, len(normalized.Models))
	for index := range normalized.Models {
		operations[index] = migrations.CreateModel{AppLabel: normalized.AppLabel, Model: normalized.Models[index].Clone()}
	}
	document, err := definition.Encode(producer, migrations.Migration{
		App: normalized.AppLabel, Name: "0001_initial", Operations: operations,
	})
	if err != nil {
		t.Fatalf("encode initial migration: %v", err)
	}
	return definition.Source{SourceID: sourceID, Document: document}
}

func mustBuildSnapshot(t *testing.T, request Request) Snapshot {
	t.Helper()
	snapshot, err := BuildSnapshot(request)
	if err != nil {
		t.Fatalf("BuildSnapshot(): %v", err)
	}
	return snapshot
}

func mustLoadSources(t *testing.T, sources ...definition.Source) migrations.LoadedDefinitionSet {
	t.Helper()
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatalf("definition.Load(): %v", err)
	}
	return loaded
}

func assertSnapshotError(t *testing.T, err error, category ErrorCategory, code ErrorCode) *Error {
	t.Helper()
	if err == nil {
		t.Fatalf("BuildSnapshot() succeeded, want %s/%s", category, code)
	}
	var failure *Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %T %v, want *Error", err, err)
	}
	if failure.Category != category || failure.Code != code {
		t.Fatalf("failure = %s/%s, want %s/%s", failure.Category, failure.Code, category, code)
	}
	return failure
}

func candidateIdentities(candidates []Candidate) []string {
	result := make([]string, len(candidates))
	for index := range candidates {
		result[index] = candidates[index].App() + "." + candidates[index].Name()
	}
	return result
}
