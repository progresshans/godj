package definition

import (
	"bytes"
	"encoding/json"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/schema/ir"
)

func TestEncodeCurrentDefinitionGolden(t *testing.T) {
	t.Parallel()

	create := migrations.CreateModel{AppLabel: "blog", Model: encodeArticleModel()}
	add := &migrations.AddField{
		AppLabel:  "blog",
		ModelName: "article",
		Field: ir.Field{
			Name:      "summary",
			GoName:    "Summary",
			Column:    "summary",
			Kind:      ir.FieldChar,
			Nullable:  true,
			MaxLength: 255,
		},
	}
	document, err := Encode(
		Producer{Name: "godj-makemigrations", Version: "1"},
		migrations.Migration{
			App:  "blog",
			Name: "0003_article",
			Dependencies: []migrations.MigrationKey{
				{App: "blog", Name: "0002_previous"},
				{App: "authors", Name: "0001_author"},
			},
			Operations: []migrations.Operation{&create, add},
		},
	)
	if err != nil {
		t.Fatalf("Encode(current): %v", err)
	}

	want := `{"format_version":1,"producer":{"name":"godj-makemigrations","version":"1"},"migration":{"app":"blog","name":"0003_article","dependencies":[{"app":"authors","name":"0001_author"},{"app":"blog","name":"0002_previous"}],"operations":[{"kind":"create_model","app_label":"blog","model":{"name":"article","go_name":"Article","db_table":"blog_article","fields":[{"name":"id","go_name":"ID","column":"id","kind":"auto","primary_key":true,"nullable":false,"max_length":0,"default":null},{"name":"title","go_name":"Title","column":"title","kind":"char","primary_key":false,"nullable":false,"max_length":80,"default":{"kind":"string","string":"untitled"}},{"name":"published","go_name":"Published","column":"published","kind":"boolean","primary_key":false,"nullable":false,"max_length":0,"default":{"kind":"boolean","boolean":false}},{"name":"author","go_name":"Author","column":"author_id","kind":"foreign_key","primary_key":false,"nullable":false,"max_length":0,"default":null,"relation":{"target":{"app_label":"authors","model_name":"author"},"cardinality":"many_to_one","reverse":{"name":"articles","disabled":false},"on_delete":"protect"}}]}},{"kind":"add_field","app_label":"blog","model_name":"article","field":{"name":"summary","go_name":"Summary","column":"summary","kind":"char","primary_key":false,"nullable":true,"max_length":255,"default":null}}]}}
`
	if string(document) != want {
		t.Fatalf("Encode(current) bytes:\n got: %s\nwant: %s", document, want)
	}
	if !json.Valid(bytes.TrimSuffix(document, []byte{'\n'})) || !bytes.HasSuffix(document, []byte{'\n'}) || bytes.HasSuffix(document, []byte("\n\n")) {
		t.Fatalf("Encode(current) framing = %q", document)
	}
}

func TestEncodeIsDeterministicAcrossDependencyOrderAndOperationPointerShape(t *testing.T) {
	t.Parallel()

	model := encodeArticleModel()
	field := ir.Field{
		Name:      "summary",
		GoName:    "Summary",
		Column:    "summary",
		Kind:      ir.FieldChar,
		Nullable:  true,
		MaxLength: 255,
	}
	valueMigration := migrations.Migration{
		App:  "blog",
		Name: "0003_article",
		Dependencies: []migrations.MigrationKey{
			{App: "blog", Name: "0002_previous"},
			{App: "authors", Name: "0001_author"},
		},
		Operations: []migrations.Operation{
			migrations.CreateModel{AppLabel: "blog", Model: model},
			migrations.AddField{AppLabel: "blog", ModelName: "article", Field: field},
		},
	}
	pointerCreate := &migrations.CreateModel{AppLabel: "blog", Model: model.Clone()}
	pointerAdd := &migrations.AddField{AppLabel: "blog", ModelName: "article", Field: field.Clone()}
	pointerMigration := migrations.Migration{
		App:  valueMigration.App,
		Name: valueMigration.Name,
		Dependencies: []migrations.MigrationKey{
			{App: "authors", Name: "0001_author"},
			{App: "blog", Name: "0002_previous"},
		},
		Operations: []migrations.Operation{pointerCreate, pointerAdd},
	}

	producer := Producer{Name: "deterministic", Version: "1"}
	want, err := Encode(producer, valueMigration)
	if err != nil {
		t.Fatalf("Encode(value): %v", err)
	}
	for iteration := 0; iteration < 32; iteration++ {
		got, encodeErr := Encode(producer, pointerMigration)
		if encodeErr != nil {
			t.Fatalf("Encode(pointer) iteration %d: %v", iteration, encodeErr)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("deterministic bytes iteration %d differ:\n got: %s\nwant: %s", iteration, got, want)
		}
	}
}

func TestEncodePreservesOperationOrder(t *testing.T) {
	t.Parallel()

	first := migrations.AddField{
		AppLabel:  "blog",
		ModelName: "article",
		Field: ir.Field{
			Name:      "summary",
			GoName:    "Summary",
			Column:    "summary",
			Kind:      ir.FieldChar,
			Nullable:  true,
			MaxLength: 255,
		},
	}
	second := migrations.AddField{
		AppLabel:  "blog",
		ModelName: "article",
		Field: ir.Field{
			Name:    "featured",
			GoName:  "Featured",
			Column:  "featured",
			Kind:    ir.FieldBoolean,
			Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean, Boolean: true},
		},
	}
	migration := migrations.Migration{App: "blog", Name: "0002_fields", Operations: []migrations.Operation{first, second}}
	forward, err := Encode(Producer{Name: "test", Version: "1"}, migration)
	if err != nil {
		t.Fatalf("Encode(forward): %v", err)
	}
	migration.Operations[0], migration.Operations[1] = migration.Operations[1], migration.Operations[0]
	reverse, err := Encode(Producer{Name: "test", Version: "1"}, migration)
	if err != nil {
		t.Fatalf("Encode(reverse): %v", err)
	}
	if bytes.Equal(forward, reverse) {
		t.Fatal("operation permutation did not change encoded bytes")
	}
	if bytes.Index(forward, []byte(`"name":"summary"`)) > bytes.Index(forward, []byte(`"name":"featured"`)) {
		t.Fatalf("forward operation order was not preserved: %s", forward)
	}
	if bytes.Index(reverse, []byte(`"name":"featured"`)) > bytes.Index(reverse, []byte(`"name":"summary"`)) {
		t.Fatalf("reverse operation order was not preserved: %s", reverse)
	}
}

func TestEncodeStrictLoadRoundTripWithDependencySources(t *testing.T) {
	t.Parallel()

	producer := Producer{Name: "roundtrip", Version: "1"}
	author := migrations.Migration{
		App:  "authors",
		Name: "0001_author",
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: "authors",
			Model: ir.Model{
				Name:    "author",
				GoName:  "Author",
				DBTable: "authors_author",
				Fields:  []ir.Field{encodeAutoField()},
			},
		}},
	}
	article := migrations.Migration{
		App:          "blog",
		Name:         "0001_article",
		Dependencies: []migrations.MigrationKey{author.Key()},
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: "blog",
			Model:    encodeArticleModel(),
		}},
	}
	tail := migrations.Migration{
		App:          "blog",
		Name:         "0002_summary",
		Dependencies: []migrations.MigrationKey{article.Key()},
		Operations: []migrations.Operation{migrations.AddField{
			AppLabel:  "blog",
			ModelName: "article",
			Field: ir.Field{
				Name:      "summary",
				GoName:    "Summary",
				Column:    "summary",
				Kind:      ir.FieldChar,
				Nullable:  true,
				MaxLength: 255,
			},
		}},
	}

	inputs := []migrations.Migration{tail, author, article}
	sources := make([]Source, len(inputs))
	for index, migration := range inputs {
		document, err := Encode(producer, migration)
		if err != nil {
			t.Fatalf("Encode(%s.%s): %v", migration.App, migration.Name, err)
		}
		sources[index] = Source{SourceID: migration.App + "-" + migration.Name, Document: document}
	}

	loaded, report, err := Load(sources...)
	if err != nil {
		t.Fatalf("Load(encoded sources): %v", err)
	}
	if report.DocumentsReceived != 3 || report.HeadersValidated != 3 || report.OperationsDecoded != 3 || report.DefinitionsPublished != 3 || report.DefinitionSetsPublished != 1 {
		t.Fatalf("Load(encoded sources) report = %+v", report)
	}
	reconstructor, err := migrations.NewStateReconstructor(loaded.Definitions()...)
	if err != nil {
		t.Fatalf("NewStateReconstructor(encoded): %v", err)
	}
	state, err := reconstructor.Reconstruct(migrations.LatestStateRequest())
	if err != nil {
		t.Fatalf("Reconstruct(encoded): %v", err)
	}
	articleState, exists := state.Model("blog", "article")
	if !exists || len(articleState.Fields) != len(encodeArticleModel().Fields)+1 || articleState.Fields[len(articleState.Fields)-1].Name != "summary" {
		t.Fatalf("round-trip article state = %#v, exists=%t", articleState, exists)
	}
	if _, exists := state.Model("authors", "author"); !exists {
		t.Fatal("round-trip author state is absent")
	}
}

func TestEncodeSnapshotsAndNeverMutatesInput(t *testing.T) {
	t.Parallel()

	defaultValue := &ir.ScalarDefault{Kind: ir.ScalarString, String: "untitled"}
	relation := &ir.ForeignKeyRelation{
		Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
		Cardinality: ir.RelationManyToOne,
		Reverse:     ir.ReverseRelation{Name: "articles"},
		OnDelete:    ir.DeleteProtect,
	}
	model := encodeArticleModel()
	model.Fields[1].Default = defaultValue
	model.Fields[3].Relation = relation
	operation := &migrations.CreateModel{AppLabel: "blog", Model: model}
	dependencies := []migrations.MigrationKey{
		{App: "blog", Name: "0002_previous"},
		{App: "authors", Name: "0001_author"},
	}
	migration := migrations.Migration{
		App:          "blog",
		Name:         "0003_article",
		Dependencies: dependencies,
		Operations:   []migrations.Operation{operation},
	}
	wantInput := cloneMigration(migration)

	document, err := Encode(Producer{Name: "snapshot", Version: "1"}, migration)
	if err != nil {
		t.Fatalf("Encode(snapshot): %v", err)
	}
	if !reflect.DeepEqual(migration, wantInput) {
		t.Fatalf("Encode mutated input:\n got: %#v\nwant: %#v", migration, wantInput)
	}
	if !reflect.DeepEqual(dependencies, []migrations.MigrationKey{{App: "blog", Name: "0002_previous"}, {App: "authors", Name: "0001_author"}}) {
		t.Fatalf("Encode sorted caller dependency slice: %#v", dependencies)
	}

	wantBytes := append([]byte(nil), document...)
	dependencies[0].Name = "mutated"
	operation.Model.Fields[1].Default.String = "mutated"
	operation.Model.Fields[3].Relation.Target.ModelName = "mutated"
	operation.Model.Fields[0].Name = "mutated"
	if !bytes.Equal(document, wantBytes) {
		t.Fatal("caller mutation changed previously returned document bytes")
	}
}

func TestEncodeRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	validProducer := Producer{Name: "test", Version: "1"}
	validMigration := migrations.Migration{
		App:  "blog",
		Name: "0001_initial",
		Operations: []migrations.Operation{migrations.CreateModel{
			AppLabel: "blog",
			Model:    encodeArticleModel(),
		}},
	}
	invalidUTF8 := string([]byte{0xff})
	var nilCreate *migrations.CreateModel
	var nilAdd *migrations.AddField

	tests := []struct {
		name      string
		producer  Producer
		migration migrations.Migration
		want      string
	}{
		{name: "empty producer name", producer: Producer{Version: "1"}, migration: validMigration, want: "producer.name"},
		{name: "invalid producer name UTF-8", producer: Producer{Name: invalidUTF8, Version: "1"}, migration: validMigration, want: "producer.name"},
		{name: "empty producer version", producer: Producer{Name: "test"}, migration: validMigration, want: "producer.version"},
		{name: "invalid producer version UTF-8", producer: Producer{Name: "test", Version: invalidUTF8}, migration: validMigration, want: "producer.version"},
		{name: "empty app", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.App = "" }), want: "migration.app"},
		{name: "invalid app", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.App = "Blog" }), want: "migration.app"},
		{name: "empty name", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Name = "" }), want: "migration.name"},
		{name: "invalid name UTF-8", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Name = invalidUTF8 }), want: "migration.name"},
		{name: "invalid dependency app", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) {
			value.Dependencies = []migrations.MigrationKey{{App: "Authors", Name: "0001_author"}}
		}), want: "dependencies[0].app"},
		{name: "empty dependency name", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Dependencies = []migrations.MigrationKey{{App: "authors"}} }), want: "dependencies[0].name"},
		{name: "invalid dependency name UTF-8", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) {
			value.Dependencies = []migrations.MigrationKey{{App: "authors", Name: invalidUTF8}}
		}), want: "dependencies[0].name"},
		{name: "self dependency", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Dependencies = []migrations.MigrationKey{value.Key()} }), want: "self dependency"},
		{name: "duplicate dependency", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) {
			value.Dependencies = []migrations.MigrationKey{{App: "authors", Name: "0001_author"}, {App: "authors", Name: "0001_author"}}
		}), want: "duplicate dependency"},
		{name: "nil operation", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Operations = []migrations.Operation{nil} }), want: "nil operation"},
		{name: "nil create pointer", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Operations = []migrations.Operation{nilCreate} }), want: "nil *migrations.CreateModel"},
		{name: "nil add pointer", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) { value.Operations = []migrations.Operation{nilAdd} }), want: "nil *migrations.AddField"},
		{name: "mismatched operation app", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) {
			create := value.Operations[0].(migrations.CreateModel)
			create.AppLabel = "other"
			value.Operations[0] = create
		}), want: "app_label"},
		{name: "unnormalized create model", producer: validProducer, migration: mutationOf(validMigration, func(value *migrations.Migration) {
			create := value.Operations[0].(migrations.CreateModel)
			create.Model.DBTable = ""
			value.Operations[0] = create
		}), want: "exact current normalized IR"},
		{name: "invalid add model", producer: validProducer, migration: migrations.Migration{App: "blog", Name: "0002_field", Operations: []migrations.Operation{migrations.AddField{AppLabel: "blog", ModelName: "Article", Field: encodeBooleanField()}}}, want: "model_name"},
		{name: "unnormalized add field", producer: validProducer, migration: migrations.Migration{App: "blog", Name: "0002_field", Operations: []migrations.Operation{migrations.AddField{AppLabel: "blog", ModelName: "article", Field: mutationOfField(encodeBooleanField(), func(field *ir.Field) { field.Column = "" })}}}, want: "exact current normalized IR"},
		{name: "unsupported add auto field", producer: validProducer, migration: migrations.Migration{App: "blog", Name: "0002_field", Operations: []migrations.Operation{migrations.AddField{AppLabel: "blog", ModelName: "article", Field: encodeAutoField()}}}, want: "exact current normalized IR"},
		{name: "invalid foreign key", producer: validProducer, migration: migrations.Migration{App: "blog", Name: "0002_field", Operations: []migrations.Operation{migrations.AddField{AppLabel: "blog", ModelName: "article", Field: invalidSetNullField()}}}, want: "exact current normalized IR"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := Encode(test.producer, test.migration)
			if err == nil || document != nil {
				t.Fatalf("Encode(invalid) = (%q, %v), want nil error result", document, err)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Encode(invalid) error = %q, want substring %q", err, test.want)
			}
		})
	}
}

func TestEncodeRejectsResourceAndDocumentOverflow(t *testing.T) {
	t.Parallel()

	producer := Producer{Name: "test", Version: "1"}
	base := migrations.Migration{App: "blog", Name: "0001_initial"}
	tests := []struct {
		name      string
		producer  Producer
		migration migrations.Migration
		want      string
	}{
		{
			name: "dependency count",
			migration: mutationOf(base, func(value *migrations.Migration) {
				value.Dependencies = make([]migrations.MigrationKey, MaxDependenciesPerMigration+1)
			}),
			want: "dependencies_per_migration",
		},
		{
			name: "operation count",
			migration: mutationOf(base, func(value *migrations.Migration) {
				value.Operations = make([]migrations.Operation, MaxOperationsPerMigration+1)
			}),
			want: "operations_per_migration",
		},
		{
			name: "create model field count",
			migration: mutationOf(base, func(value *migrations.Migration) {
				value.Operations = []migrations.Operation{migrations.CreateModel{
					AppLabel: "blog",
					Model:    ir.Model{Fields: make([]ir.Field, MaxFieldsPerCreateModel+1)},
				}}
			}),
			want: "fields_per_create_model",
		},
		{
			name:      "document bytes",
			producer:  Producer{Name: strings.Repeat("x", MaxDocumentBytes), Version: "1"},
			migration: base,
			want:      "document_bytes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			currentProducer := test.producer
			if currentProducer == (Producer{}) {
				currentProducer = producer
			}
			document, err := Encode(currentProducer, test.migration)
			if err == nil || document != nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Encode(resource limit) = (%d bytes, %v), want %q", len(document), err, test.want)
			}
		})
	}

	if runtime.GOARCH == "386" {
		return
	}
	overflow := encodeBooleanField()
	overflow.Kind = ir.FieldChar
	maximum := int64(maximumWireLength)
	overflow.MaxLength = int(maximum)
	overflow.MaxLength++
	overflow.Default = &ir.ScalarDefault{Kind: ir.ScalarString}
	_, err := Encode(producer, migrations.Migration{
		App:  "blog",
		Name: "0002_field",
		Operations: []migrations.Operation{migrations.AddField{
			AppLabel: "blog", ModelName: "article", Field: overflow,
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "max_length") {
		t.Fatalf("Encode(max_length overflow) error = %v", err)
	}
}

func TestEncodeDocumentByteLimitIncludesTrailingNewline(t *testing.T) {
	t.Parallel()

	migration := migrations.Migration{App: "blog", Name: "0001_initial"}
	baseProducer := Producer{Name: "x", Version: "1"}
	base, err := Encode(baseProducer, migration)
	if err != nil {
		t.Fatalf("Encode(base boundary document): %v", err)
	}
	padding := MaxDocumentBytes - len(base)
	if padding <= 0 {
		t.Fatalf("base boundary document length = %d", len(base))
	}

	exactProducer := baseProducer
	exactProducer.Name += strings.Repeat("x", padding)
	exact, err := Encode(exactProducer, migration)
	if err != nil {
		t.Fatalf("Encode(exact document byte limit): %v", err)
	}
	if len(exact) != MaxDocumentBytes || !bytes.HasSuffix(exact, []byte{'\n'}) {
		t.Fatalf("exact document framing = %d bytes, suffix newline=%t", len(exact), bytes.HasSuffix(exact, []byte{'\n'}))
	}

	exactProducer.Name += "x"
	overflow, err := Encode(exactProducer, migration)
	if err == nil || overflow != nil || !strings.Contains(err.Error(), "document_bytes") {
		t.Fatalf("Encode(exact+1 document) = (%d bytes, %v)", len(overflow), err)
	}
}

func TestEncodeNonEmptyCurrentShapesSucceedAtExactDocumentByteLimit(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		migration migrations.Migration
	}{
		{
			name: "dependency operation and create model fields",
			migration: migrations.Migration{
				App:          "blog",
				Name:         "0002_article",
				Dependencies: []migrations.MigrationKey{{App: "authors", Name: "0001_author"}},
				Operations: []migrations.Operation{migrations.CreateModel{
					AppLabel: "blog",
					Model:    encodeArticleModel(),
				}},
			},
		},
		{
			name: "dependency operation and add field",
			migration: migrations.Migration{
				App:          "blog",
				Name:         "0002_summary",
				Dependencies: []migrations.MigrationKey{{App: "blog", Name: "0001_article"}},
				Operations: []migrations.Operation{migrations.AddField{
					AppLabel:  "blog",
					ModelName: "article",
					Field: ir.Field{
						Name:      "summary",
						GoName:    "Summary",
						Column:    "summary",
						Kind:      ir.FieldChar,
						Nullable:  true,
						MaxLength: 255,
					},
				}},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			producer := Producer{Name: "x", Version: "1"}
			base, err := Encode(producer, test.migration)
			if err != nil {
				t.Fatalf("Encode(non-empty boundary base): %v", err)
			}
			padding := MaxDocumentBytes - len(base)
			if padding <= 0 {
				t.Fatalf("non-empty boundary base length = %d", len(base))
			}
			producer.Name += strings.Repeat("x", padding)
			exact, err := Encode(producer, test.migration)
			if err != nil {
				t.Fatalf("Encode(non-empty exact document limit): %v", err)
			}
			if len(exact) != MaxDocumentBytes || !bytes.HasSuffix(exact, []byte{'\n'}) {
				t.Fatalf("non-empty exact document framing = %d bytes, suffix newline=%t", len(exact), bytes.HasSuffix(exact, []byte{'\n'}))
			}
		})
	}
}

func TestEncodePreflightRejectsOversizedRawStringsBeforeSnapshot(t *testing.T) {
	t.Parallel()

	baseMigration := migrations.Migration{App: "blog", Name: "0001_initial"}
	tests := []struct {
		name      string
		producer  Producer
		migration migrations.Migration
		wantPath  string
	}{
		{
			name:      "producer",
			producer:  Producer{Name: strings.Repeat("p", MaxDocumentBytes+1), Version: "1"},
			migration: baseMigration,
			wantPath:  "producer.name",
		},
		{
			name:     "nested scalar default",
			producer: Producer{Name: "test", Version: "1"},
			migration: migrations.Migration{
				App:  "blog",
				Name: "0002_summary",
				Operations: []migrations.Operation{migrations.AddField{
					AppLabel:  "blog",
					ModelName: "article",
					Field: ir.Field{
						Name:      "summary",
						GoName:    "Summary",
						Column:    "summary",
						Kind:      ir.FieldChar,
						MaxLength: MaxDocumentBytes + 1,
						Default: &ir.ScalarDefault{
							Kind:   ir.ScalarString,
							String: strings.Repeat("d", MaxDocumentBytes+1),
						},
					},
				}},
			},
			wantPath: "migration.operations[0].field.default.string",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document, err := Encode(test.producer, test.migration)
			if err == nil || document != nil {
				t.Fatalf("Encode(oversized raw string) = (%d bytes, %v)", len(document), err)
			}
			if !strings.Contains(err.Error(), test.wantPath) || !strings.Contains(err.Error(), "document_bytes") || !strings.Contains(err.Error(), "lower bound") {
				t.Fatalf("Encode(oversized raw string) error = %q, want path %q resource lower bound", err, test.wantPath)
			}
		})
	}
}

func TestEncodePreflightBoundsAggregateCreateModelFieldAmplification(t *testing.T) {
	t.Parallel()

	// Every operation deliberately shares the same maximum-sized field slice.
	// The logical input therefore contains more than four million field visits
	// without making the fixture itself allocate four million Field values.
	sharedFields := make([]ir.Field, MaxFieldsPerCreateModel)
	operations := make([]migrations.Operation, MaxOperationsPerMigration)
	for index := range operations {
		operations[index] = migrations.CreateModel{
			AppLabel: "blog",
			Model: ir.Model{
				Name:    "article",
				GoName:  "Article",
				DBTable: "blog_article",
				Fields:  sharedFields,
			},
		}
	}
	if uint64(len(sharedFields))*uint64(len(operations)) <= 4_000_000 {
		t.Fatalf("aggregate fixture visits = %d", uint64(len(sharedFields))*uint64(len(operations)))
	}

	document, err := Encode(
		Producer{Name: "test", Version: "1"},
		migrations.Migration{App: "blog", Name: "0001_initial", Operations: operations},
	)
	if err == nil || document != nil || !strings.Contains(err.Error(), "document_bytes") || !strings.Contains(err.Error(), "lower bound") {
		t.Fatalf("Encode(aggregate field amplification) = (%d bytes, %v)", len(document), err)
	}
	if strings.Contains(err.Error(), "fields_per_create_model") {
		t.Fatalf("aggregate amplification incorrectly failed an individual field cap: %v", err)
	}
}

func TestEncodeEscapingCanPassLowerBoundAndFailExactDocumentCap(t *testing.T) {
	t.Parallel()

	migration := migrations.Migration{App: "blog", Name: "0001_initial"}
	baseProducer := Producer{Name: "x", Version: "1"}
	base, err := Encode(baseProducer, migration)
	if err != nil {
		t.Fatalf("Encode(escaping base): %v", err)
	}
	rawCount := (MaxDocumentBytes-len(base))/2 + 1_024
	producer := Producer{Name: strings.Repeat(`\`, rawCount), Version: "1"}
	if len(producer.Name) >= MaxDocumentBytes {
		t.Fatalf("escaping fixture raw bytes = %d", len(producer.Name))
	}
	if err := preflightEncodingResources(producer, migration); err != nil {
		t.Fatalf("escaping lower-bound preflight = %v, want pass", err)
	}

	document, err := Encode(producer, migration)
	if err == nil || document != nil || !strings.Contains(err.Error(), "document_bytes") {
		t.Fatalf("Encode(escaping exact overflow) = (%d bytes, %v)", len(document), err)
	}
	if strings.Contains(err.Error(), "lower bound") {
		t.Fatalf("escaping overflow was rejected by lower bound instead of exact cap: %v", err)
	}
}

func encodeArticleModel() ir.Model {
	return ir.Model{
		Name:    "article",
		GoName:  "Article",
		DBTable: "blog_article",
		Fields: []ir.Field{
			encodeAutoField(),
			{
				Name:      "title",
				GoName:    "Title",
				Column:    "title",
				Kind:      ir.FieldChar,
				MaxLength: 80,
				Default:   &ir.ScalarDefault{Kind: ir.ScalarString, String: "untitled"},
			},
			encodeBooleanField(),
			{
				Name:   "author",
				GoName: "Author",
				Column: "author_id",
				Kind:   ir.FieldForeignKey,
				Relation: &ir.ForeignKeyRelation{
					Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
					Cardinality: ir.RelationManyToOne,
					Reverse:     ir.ReverseRelation{Name: "articles"},
					OnDelete:    ir.DeleteProtect,
				},
			},
		},
	}
}

func encodeAutoField() ir.Field {
	return ir.Field{
		Name:       "id",
		GoName:     "ID",
		Column:     "id",
		Kind:       ir.FieldAuto,
		PrimaryKey: true,
	}
}

func encodeBooleanField() ir.Field {
	return ir.Field{
		Name:    "published",
		GoName:  "Published",
		Column:  "published",
		Kind:    ir.FieldBoolean,
		Default: &ir.ScalarDefault{Kind: ir.ScalarBoolean},
	}
}

func invalidSetNullField() ir.Field {
	return ir.Field{
		Name:   "author",
		GoName: "Author",
		Column: "author_id",
		Kind:   ir.FieldForeignKey,
		Relation: &ir.ForeignKeyRelation{
			Target:      ir.ModelIdentity{AppLabel: "authors", ModelName: "author"},
			Cardinality: ir.RelationManyToOne,
			Reverse:     ir.ReverseRelation{Name: "articles"},
			OnDelete:    ir.DeleteSetNull,
		},
	}
}

func mutationOf(input migrations.Migration, mutate func(*migrations.Migration)) migrations.Migration {
	result := cloneMigration(input)
	mutate(&result)
	return result
}

func mutationOfField(input ir.Field, mutate func(*ir.Field)) ir.Field {
	result := input.Clone()
	mutate(&result)
	return result
}
