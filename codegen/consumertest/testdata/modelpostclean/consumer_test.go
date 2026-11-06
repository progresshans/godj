package consumer_test

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/godj-model-postclean/models"
	"github.com/jackc/pgx/v5"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/db/postgres"
	"github.com/progresshans/godj/db/sqlite"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/migrations"
	migrationbackend "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/validation"
)

//go:embed reference.json
var reference []byte

type observation struct {
	Name       string                     `json:"name"`
	Input      map[string]string          `json:"input"`
	Form       string                     `json:"form"`
	Valid      bool                       `json:"valid"`
	Errors     map[string][]string        `json:"errors"`
	Cleaned    map[string]json.RawMessage `json:"cleaned"`
	Trace      []stage                    `json:"trace"`
	QueryCount int                        `json:"query_count"`
}
type stage struct {
	Stage   string   `json:"stage"`
	Exclude []string `json:"exclude,omitempty"`
}
type probeBackend interface {
	db.Session
	db.SnapshotReader
	migrationbackend.RevisionFencedBackend
	Close() error
}
type countedReader struct {
	reader db.Queryer
	count  int
}

func (reader *countedReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	reader.count++
	return reader.reader.Query(ctx, plan)
}

func TestGeneratedModelFormDatabase(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "form.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		runModelFormDatabase(t, backend)
	})
	dsn := strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL"))
	if dsn == "" {
		if os.Getenv("GODJ_REQUIRE_POSTGRES") == "1" {
			t.Fatal("required PostgreSQL is absent")
		}
		return
	}
	t.Run("postgres", func(t *testing.T) {
		connection, err := pgx.Connect(t.Context(), dsn)
		if err != nil {
			t.Fatal("connect model form reference database")
		}
		name := fmt.Sprintf("godj_model_form_%d_%d", os.Getpid(), time.Now().UnixNano())
		quoted := pgx.Identifier{name}.Sanitize()
		if _, err := connection.Exec(t.Context(), "CREATE SCHEMA "+quoted); err != nil {
			_ = connection.Close(context.Background())
			t.Fatal(err)
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			if _, err := connection.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE"); err != nil {
				t.Error(err)
			}
			if err := connection.Close(ctx); err != nil {
				t.Error(err)
			}
		})
		backend, err := postgres.Open(t.Context(), postgres.Config{URL: dsn, Schema: name})
		if err != nil {
			t.Fatal("open model form reference backend")
		}
		runModelFormDatabase(t, backend)
	})
}

func runModelFormDatabase(t *testing.T, backend probeBackend) {
	t.Helper()
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	var fixture struct {
		Cases []observation `json:"cases"`
	}
	if err := json.Unmarshal(reference, &fixture); err != nil || len(fixture.Cases) != 16 {
		t.Fatal("native observation fixture", err)
	}
	metadata := models.ContactDescriptor{}.Metadata()
	document, err := definition.Encode(definition.Producer{Name: "model-form-reference", Version: "1"}, migrations.Migration{App: "postclean_probe", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "postclean_probe", Model: metadata}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "reference/0001_initial", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	existing, err := models.ContactObjects.Create(t.Context(), backend, models.NewContactCreate("used", "a@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			selected := []string{"key", "address", "counter"}
			if test.Form == "subset" || test.Form == "extra" {
				selected = []string{"key", "counter"}
			}
			projection := formmodel.Definition{Fields: selected}
			if test.Form == "extra" {
				// Go's explicit projection refuses a command input shadowing a
				// stored field. Native ModelForm allows this when the stored
				// field is excluded. Assert the declared difference before I/O.
				extra, err := forms.CharField("address", forms.WithRequired(false))
				if err != nil {
					t.Fatal(err)
				}
				projection.ExtraFields = []forms.Field{extra}
				_, err = projection.Spec(metadata)
				failure, ok := err.(*formmodel.Error)
				if !ok || failure.Code != "shadows_model" || failure.Path != "address" {
					t.Fatal("excluded shadow input was not rejected at construction", err)
				}
				return
			}
			spec, err := projection.Spec(metadata)
			if err != nil {
				t.Fatal(err)
			}
			if test.Form == "override" || test.Form == "optional" {
				name := "address"
				if test.Form == "optional" {
					name = "key"
				}
				fields := spec.Fields()
				for i, field := range fields {
					if field.Name() == name {
						fields[i], err = forms.CharField(name, forms.WithRequired(false))
						if err != nil {
							t.Fatal(err)
						}
					}
				}
				spec, err = forms.NewSpec(fields)
				if err != nil {
					t.Fatal(err)
				}
			}
			input := make(map[string][]string, len(test.Input))
			for name, value := range test.Input {
				input[name] = []string{value}
			}
			var initial map[string]forms.Value
			var current *models.Contact
			if test.Name == "same_row" {
				current = &existing
				initial = map[string]forms.Value{"id": forms.Integer(existing.ID), "key": forms.String(existing.Key), "address": forms.String(existing.Address), "counter": forms.Integer(existing.Counter)}
			}
			modelCalls := 0
			bound, err := formmodel.Bind(t.Context(), metadata, spec, forms.NewData(input), initial, formmodel.PostClean{Validators: []formmodel.Validator{formmodel.ValidatorFunc(func(candidate forms.Values) validation.Errors {
				modelCalls++
				if key, _ := candidate.String("key"); key == "reject" {
					return validation.NewErrors(validation.New("counter", "semantic"))
				}
				return validation.Errors{}
			})}})
			if err != nil || modelCalls != 1 {
				t.Fatal("model clean was skipped or failed", err)
			}
			var trace []stage
			var failures validation.Errors
			queries := 0
			err = backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
				counted := &countedReader{reader: reader}
				record := func(name string, values map[string]query.Value) {
					excluded := []string{}
					for _, field := range metadata.Fields {
						if _, present := values[field.Name]; !present {
							excluded = append(excluded, field.Name)
						}
					}
					sort.Strings(excluded)
					trace = append(trace, stage{Stage: name, Exclude: excluded})
				}
				var err error
				failures, err = bound.CheckDatabase(t.Context(), formmodel.DatabaseChecks{
					UniqueFields: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
						record("unique", values)
						return models.ContactObjects.ValidateUniqueFields(ctx, counted, values, current)
					},
					Constraints: func(ctx context.Context, values map[string]query.Value) (validation.Errors, error) {
						record("constraints", values)
						return models.ContactObjects.ValidateUniqueConstraints(ctx, counted, values, current)
					},
				})
				queries = counted.count
				return err
			})
			if err != nil {
				t.Fatal("database model validation execution", err)
			}
			bound, err = bound.WithErrors(failures)
			if err != nil {
				t.Fatal(err)
			}
			codes := map[string][]string{}
			for _, failure := range bound.Form().Errors().All() {
				name := string(failure.Field())
				if failure.Field() == validation.NonField {
					name = "__all__"
				}
				codes[name] = append(codes[name], string(failure.Code()))
			}
			cleaned := map[string]json.RawMessage{}
			for _, entry := range bound.Form().Cleaned().All() {
				var encoded []byte
				if value, ok := entry.Value().AsString(); ok {
					encoded, err = json.Marshal(value)
				} else if value, ok := entry.Value().AsInteger(); ok {
					encoded, err = json.Marshal(value)
				} else {
					t.Fatal("unexpected cleaned value kind")
				}
				if err != nil {
					t.Fatal(err)
				}
				cleaned[entry.Name()] = encoded
			}
			actualJSON, err := json.Marshal(cleaned)
			if err != nil {
				t.Fatal(err)
			}
			wantJSON, err := json.Marshal(test.Cleaned)
			if err != nil {
				t.Fatal(err)
			}
			wantTrace := []stage{}
			for _, step := range test.Trace {
				if step.Stage == "unique" || step.Stage == "constraints" {
					wantTrace = append(wantTrace, step)
				}
			}
			if bound.Form().Valid() != test.Valid || !reflect.DeepEqual(codes, test.Errors) || !bytes.Equal(actualJSON, wantJSON) || !reflect.DeepEqual(trace, wantTrace) || queries != test.QueryCount {
				t.Fatalf("native mismatch: valid=%t codes=%v cleaned=%s trace=%v queries=%d; want valid=%t codes=%v cleaned=%s trace=%v queries=%d", bound.Form().Valid(), codes, actualJSON, trace, queries, test.Valid, test.Errors, wantJSON, wantTrace, test.QueryCount)
			}
			rows, err := models.ContactObjects.Using(backend).OrderBy(models.ContactFields.ID.Asc()).All(t.Context())
			if err != nil || !reflect.DeepEqual(rows, []models.Contact{existing}) {
				t.Fatal("read checks modified the stored candidate", err)
			}
		})
	}
}
