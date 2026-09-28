package consumer_test

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"example.com/godj-model-clean/models"
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

type probeBackend interface {
	db.Session
	db.Atomic
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

func TestGeneratedModelClean(t *testing.T) {
	t.Run("sqlite", func(t *testing.T) {
		backend, err := sqlite.Open(t.Context(), filepath.Join(t.TempDir(), "form.sqlite3"))
		if err != nil {
			t.Fatal(err)
		}
		runModelClean(t, backend)
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
		runModelClean(t, backend)
	})
}

type observation struct {
	Name       string              `json:"name"`
	Input      map[string]string   `json:"input"`
	Valid      bool                `json:"valid"`
	Errors     map[string][]string `json:"errors"`
	Cleaned    map[string]any      `json:"cleaned"`
	Candidate  map[string]any      `json:"candidate"`
	Trace      []stage             `json:"trace"`
	QueryCount int                 `json:"validation_query_count"`
	Prepare    struct {
		OK         bool           `json:"ok"`
		Candidate  map[string]any `json:"candidate"`
		QueryCount int            `json:"query_count"`
	} `json:"prepare"`
	Save struct {
		OK       bool           `json:"ok"`
		Error    string         `json:"error"`
		Stored   map[string]any `json:"stored"`
		RowCount int64          `json:"row_count"`
	} `json:"save_inside_rolled_back_transaction"`
}
type stage struct {
	Stage     string         `json:"stage"`
	Exclude   []string       `json:"exclude,omitempty"`
	Candidate map[string]any `json:"candidate"`
}

func runModelClean(t *testing.T, backend probeBackend) {
	t.Helper()
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	var fixture struct {
		Cases []observation `json:"cases"`
	}
	if err := json.Unmarshal(reference, &fixture); err != nil || len(fixture.Cases) != 15 {
		t.Fatal("native clean fixture", err)
	}
	metadata := models.ContactDescriptor{}.Metadata()
	document, err := definition.Encode(definition.Producer{Name: "model-clean-reference", Version: "1"}, migrations.Migration{App: "model_clean_probe", Name: "0001_initial", Operations: []migrations.Operation{migrations.CreateModel{AppLabel: "model_clean_probe", Model: metadata}}})
	if err != nil {
		t.Fatal(err)
	}
	loaded, _, err := definition.Load(definition.Source{SourceID: "clean/0001_initial", Document: document})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = (migrations.Executor{Backend: backend}).Migrate(t.Context(), loaded, migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	existing, err := models.ContactObjects.Create(t.Context(), backend, models.NewContactCreate("used", "old@example.com").WithHidden("server-old"))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range fixture.Cases {
		t.Run(test.Name, func(t *testing.T) {
			raw := make(map[string][]string, len(test.Input))
			for name, value := range test.Input {
				raw[name] = []string{value}
			}
			var current *models.Contact
			if strings.HasPrefix(test.Name, "existing_") {
				current = &existing
			}
			var trace []stage
			calls := 0
			projection := formmodel.Definition{Fields: []string{"code", "email", "counter"}, PostClean: formmodel.PostClean{
				Fields: []string{"code", "email", "counter", "hidden"},
				Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
					calls++
					trace = append(trace, stage{Stage: "model_clean_before", Candidate: formCandidate(t, candidate)})
					changes := map[string]forms.Value{}
					var failures validation.Errors
					switch test.Name {
					case "normalize_selected", "existing_selected":
						value, _ := candidate.String("code")
						changes["code"] = forms.String(strings.ToUpper(value))
					case "overwrite_duplicate":
						changes["code"] = forms.String("fresh")
					case "introduce_duplicate":
						changes["code"] = forms.String("used")
					case "rewrite_excluded_hidden", "existing_hidden_change":
						changes["hidden"] = forms.String("changed-hidden")
					case "excluded_hidden_duplicate":
						changes["hidden"] = forms.String("server-old")
					case "change_email_after_fields":
						changes["email"] = forms.String("invalid-after-clean")
					case "repair_invalid_field", "missing_default":
						changes["counter"] = forms.Integer(7)
					case "mutate_and_field_error":
						changes["code"] = forms.String("changed")
						changes["counter"] = forms.Integer(7)
						failures = validation.NewErrors(validation.New("counter", "semantic"))
					case "mutate_nonfield_error":
						changes["code"] = forms.String("changed")
						changes["hidden"] = forms.String("changed-hidden")
						failures = validation.NewErrors(validation.New(validation.NonField, "model_policy"))
					case "clear_counter_after_fields":
						changes["counter"] = forms.Null()
					case "clear_excluded_hidden":
						changes["hidden"] = forms.Null()
					case "returns_mapping":
						// Python ignores Model.clean's returned mapping. Go represents the
						// same unchanged candidate with an empty explicit change set; its
						// CleanFunc return protocol intentionally differs from Python's.
					default:
						t.Fatal("unknown native case")
					}
					return forms.NewValues(changes), failures
				},
			}}
			spec, err := projection.Spec(metadata)
			if err != nil {
				t.Fatal(err)
			}
			instanceForm, err := formmodel.BindInstance(models.ContactObjects, spec, forms.NewData(raw), current, projection.PostClean)
			bound := instanceForm.BoundForm()
			if err != nil || calls != 1 {
				t.Fatal("clean", err, calls)
			}
			trace = append(trace, stage{Stage: "model_clean_after", Candidate: formCandidate(t, bound.Candidate())})
			queries := 0
			var failures validation.Errors
			err = backend.ReadSnapshot(t.Context(), func(reader db.Queryer) error {
				counted := &countedReader{reader: reader}
				record := func(name string, values map[string]query.Value) {
					excluded := []string{}
					for _, field := range metadata.Fields {
						if _, found := values[field.Name]; !found {
							excluded = append(excluded, field.Name)
						}
					}
					sort.Strings(excluded)
					trace = append(trace, stage{Stage: name, Exclude: excluded, Candidate: formCandidate(t, bound.Candidate())})
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
				t.Fatal("read-only checks", err)
			}
			instanceForm, err = instanceForm.WithErrors(failures)
			bound = instanceForm.BoundForm()
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
			wantedTrace := []stage{}
			for _, step := range test.Trace {
				if step.Stage != "model_fields" {
					wantedTrace = append(wantedTrace, step)
				}
			}
			equalJSON(t, "candidate", formCandidate(t, bound.Candidate()), test.Candidate)
			equalJSON(t, "cleaned", formValues(t, bound.Form().Cleaned()), test.Cleaned)
			equalJSON(t, "trace", trace, wantedTrace)
			if bound.Form().Valid() != test.Valid || !reflect.DeepEqual(codes, test.Errors) || queries != test.QueryCount {
				t.Fatalf("native validation mismatch: valid %t, codes %v, queries %d; want %t %v %d", bound.Form().Valid(), codes, queries, test.Valid, test.Errors, test.QueryCount)
			}
			// Preparation receives no backend. It applies Input to a detached typed
			// current/default instance, preserving excluded fields unless clean owns
			// an explicit change. It cannot perform persistence or mutate the caller.
			prepare := func() (models.Contact, error) {
				prepared, err := instanceForm.Prepare()
				if err != nil {
					return models.Contact{}, err
				}
				if len(prepared.Collections().All()) != 0 {
					t.Fatal("scalar form acquired pending collections")
				}
				return prepared.Model()
			}
			prepared, prepareErr := prepare()
			typedNull := test.Name == "clear_counter_after_fields" || test.Name == "clear_excluded_hidden"
			if typedNull {
				var invalid *formmodel.Error
				if !test.Prepare.OK || !test.Valid || test.Prepare.QueryCount != 0 || !errors.As(prepareErr, &invalid) || invalid.Code != "nonnullable" {
					t.Fatal("typed NULL preparation boundary differs", prepareErr)
				}
			} else if (prepareErr == nil) != test.Prepare.OK || test.Prepare.QueryCount != 0 {
				t.Fatal("invalid form entered preparation", prepareErr)
			}
			if prepareErr == nil {
				equalJSON(t, "prepared", storedCandidate(prepared), test.Prepare.Candidate)
			}
			restore := errors.New("restore native observation case")
			writes := 0
			saveErr := backend.Atomic(t.Context(), func(session db.Session) error {
				value, err := prepare()
				if err != nil {
					return err
				}
				writes++
				if err = models.ContactObjects.Save(t.Context(), session, &value); err != nil {
					return err
				}
				stored, present, err := models.ContactObjects.Using(session).Filter(models.ContactFields.ID.Exact(value.ID)).OrderBy(models.ContactFields.ID.Asc()).First(t.Context())
				if err != nil {
					return err
				}
				if !present {
					return errors.New("saved row is missing")
				}
				equalJSON(t, "stored", storedCandidate(stored), test.Save.Stored)
				count, err := models.ContactObjects.Using(session).Count(t.Context())
				if err != nil {
					return err
				}
				if count != test.Save.RowCount {
					t.Fatal("saved row count differs", count, test.Save.RowCount)
				}
				return restore
			})
			switch {
			case typedNull:
				var invalid *formmodel.Error
				if test.Save.Error != "IntegrityError" || !errors.As(saveErr, &invalid) || invalid.Code != "nonnullable" || writes != 0 {
					t.Fatal("typed NULL was coerced before storage", saveErr, writes)
				}
			case test.Save.OK:
				if !errors.Is(saveErr, restore) || writes != 1 {
					t.Fatal("expected actual write followed by rollback", saveErr, writes)
				}
			case test.Save.Error == "ValueError":
				var invalid *formmodel.Error
				if !errors.As(saveErr, &invalid) || invalid.Code != "not_bound_valid" || writes != 0 {
					t.Fatal("invalid form reached ORM save", saveErr, writes)
				}
			case test.Save.Error == "IntegrityError":
				if !errors.Is(saveErr, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) || writes != 1 {
					t.Fatal("excluded uniqueness must remain a final DB constraint", saveErr, writes)
				}
			default:
				t.Fatal("unknown native save outcome")
			}
			rows, err := models.ContactObjects.Using(backend).OrderBy(models.ContactFields.ID.Asc()).All(t.Context())
			if err != nil || !reflect.DeepEqual(rows, []models.Contact{existing}) {
				t.Fatal("case did not restore existing row", err)
			}
		})
	}
}

func storedCandidate(value models.Contact) map[string]any {
	_, present := (models.ContactDescriptor{}).PrimaryKey(value)
	return map[string]any{"code": value.Code, "email": value.Email, "counter": value.Counter, "hidden": value.Hidden, "has_id": present}
}
func formCandidate(t *testing.T, values forms.Values) map[string]any {
	result := formValues(t, values)
	id, _ := values.Get("id")
	delete(result, "id")
	result["has_id"] = !id.IsNull()
	return result
}
func formValues(t *testing.T, values forms.Values) map[string]any {
	result := map[string]any{}
	for _, entry := range values.All() {
		if entry.Value().IsNull() {
			result[entry.Name()] = nil
			continue
		}
		if value, ok := entry.Value().AsString(); ok {
			result[entry.Name()] = value
			continue
		}
		if value, ok := entry.Value().AsInteger(); ok {
			result[entry.Name()] = value
			continue
		}
		t.Fatal("unexpected native observation value")
	}
	return result
}
func equalJSON(t *testing.T, label string, actual, wanted any) {
	t.Helper()
	got, err := json.Marshal(actual)
	if err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(wanted)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s mismatch: %s != %s", label, got, want)
	}
}
