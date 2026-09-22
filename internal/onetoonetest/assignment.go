package onetoonetest

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/progresshans/godj/conformance/onetoonefixture/project"
	"github.com/progresshans/godj/conformance/onetoonefixture/reports"
	"github.com/progresshans/godj/conformance/onetoonefixture/tickets"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/migrations"
	"github.com/progresshans/godj/query"
)

type assignmentObserver struct {
	db.Session
	reads, writes int
	failure       error
}

func (b *assignmentObserver) Query(ctx context.Context, p query.Plan) (db.Rows, error) {
	b.reads++
	if b.failure != nil {
		return nil, b.failure
	}
	return b.Session.Query(ctx, p)
}
func (b *assignmentObserver) Insert(ctx context.Context, p query.InsertPlan) (int64, error) {
	b.writes++
	if b.failure != nil {
		return 0, b.failure
	}
	return b.Session.Insert(ctx, p)
}
func (b *assignmentObserver) Update(ctx context.Context, p query.UpdatePlan) (int64, error) {
	b.writes++
	if b.failure != nil {
		return 0, b.failure
	}
	return b.Session.Update(ctx, p)
}
func (b *assignmentObserver) Delete(ctx context.Context, p query.DeletePlan) (int64, error) {
	b.writes++
	if b.failure != nil {
		return 0, b.failure
	}
	return b.Session.Delete(ctx, p)
}

type assignmentStep struct {
	Error   string `json:"error"`
	Selects int    `json:"selects"`
	Writes  int    `json:"writes"`
}
type assignmentReference struct {
	Name   string                    `json:"name"`
	States map[string][]bool         `json:"states"`
	Steps  map[string]assignmentStep `json:"steps"`
}
type assignmentChild[C any] struct {
	kind   string
	new    func(string) (*C, error)
	get    func(*project.TicketsTicket) (*C, bool, error)
	set    func(*project.TicketsTicket, *C) error
	owner  func(*C) (*project.TicketsTicket, error)
	with   func(*C, *project.TicketsTicket) (*C, error)
	clear  func(*C) (*C, error)
	save   func(*C) error
	unwrap func(*C) error
	key    func(*C) int64
	fk     func(*C) int64
	rawSet func(*C, int64)
	withID func(*C, int64) (*C, error)
	stored func(*C) int64
	count  func(*project.TicketsTicket) int64
}

func RunAssignment(t *testing.T, backend ProductBackend, dialect string, _ func(query.Plan) (string, error), queryCount func() uint64) {
	t.Helper()
	ctx := t.Context()
	t.Cleanup(func() {
		if err := backend.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := (migrations.Executor{Backend: backend}).Migrate(ctx, productHistory(t), migrations.LatestLifecycleRequest()); err != nil {
		t.Fatal(err)
	}
	observed := &assignmentObserver{Session: backend}
	models, err := project.Using(observed)
	if err != nil {
		t.Fatal(err)
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join("..", "..", "orm", "testdata", "one-to-one-django61-"+dialect+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var reference struct {
		Assignment []assignmentReference `json:"assignment"`
	}
	if err := json.Unmarshal(data, &reference); err != nil {
		t.Fatal(err)
	}
	expected := map[string]assignmentReference{}
	for _, item := range reference.Assignment {
		if _, found := expected[item.Name]; found {
			t.Fatal("duplicate assignment reference")
		}
		expected[item.Name] = item
	}
	if len(expected) != 14 {
		t.Fatal("assignment reference inventory", len(expected))
	}
	seen := map[string]bool{}
	runAssignmentCases(t, models, observed, queryCount, expected, seen, assignmentChild[project.ReportsReport]{
		kind: "required", new: func(note string) (*project.ReportsReport, error) {
			return models.ReportsReport.New(reports.Report{Note: note})
		},
		get: func(owner *project.TicketsTicket) (*project.ReportsReport, bool, error) { return owner.Report(ctx) }, set: (*project.TicketsTicket).SetReport,
		owner: func(child *project.ReportsReport) (*project.TicketsTicket, error) { return child.Ticket(ctx) }, with: (*project.ReportsReport).WithTicket, clear: (*project.ReportsReport).ClearTicket,
		save: func(child *project.ReportsReport) error { return child.Save(ctx) }, unwrap: func(child *project.ReportsReport) error { _, err := child.Unwrap(); return err }, key: func(child *project.ReportsReport) int64 { return child.ID }, fk: func(child *project.ReportsReport) int64 { return child.TicketID },
		rawSet: func(child *project.ReportsReport, key int64) { child.TicketID = key }, withID: (*project.ReportsReport).WithTicketID,
		stored: func(child *project.ReportsReport) int64 {
			value, found, err := models.ReportsReport.Filter(reports.ReportFields.ID.Exact(child.ID)).OrderBy(reports.ReportFields.ID.Asc()).First(ctx)
			if err != nil || !found {
				t.Fatal("stored required child", err)
			}
			return value.TicketID
		},
		count: func(owner *project.TicketsTicket) int64 {
			count, err := models.ReportsReport.Filter(relations.ReportsReport.Ticket.ID.Exact(owner.ID)).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return count
		},
	})
	runAssignmentCases(t, models, observed, queryCount, expected, seen, assignmentChild[project.ReportsOptionalReport]{
		kind: "optional", new: func(note string) (*project.ReportsOptionalReport, error) {
			return models.ReportsOptionalReport.New(reports.OptionalReport{Note: note})
		},
		get: func(owner *project.TicketsTicket) (*project.ReportsOptionalReport, bool, error) {
			return owner.OptionalReport(ctx)
		}, set: (*project.TicketsTicket).SetOptionalReport,
		owner: func(child *project.ReportsOptionalReport) (*project.TicketsTicket, error) {
			owner, _, err := child.Ticket(ctx)
			return owner, err
		}, with: (*project.ReportsOptionalReport).WithTicket, clear: (*project.ReportsOptionalReport).ClearTicket,
		save: func(child *project.ReportsOptionalReport) error { return child.Save(ctx) }, unwrap: func(child *project.ReportsOptionalReport) error { _, err := child.Unwrap(); return err }, key: func(child *project.ReportsOptionalReport) int64 { return child.ID }, fk: func(child *project.ReportsOptionalReport) int64 {
			if child.TicketID == nil {
				return 0
			}
			return *child.TicketID
		},
		rawSet: func(child *project.ReportsOptionalReport, key int64) { child.TicketID = &key }, withID: (*project.ReportsOptionalReport).WithTicketID,
		stored: func(child *project.ReportsOptionalReport) int64 {
			value, found, err := models.ReportsOptionalReport.Filter(reports.OptionalReportFields.ID.Exact(child.ID)).OrderBy(reports.OptionalReportFields.ID.Asc()).First(ctx)
			if err != nil || !found {
				t.Fatal("stored optional child", err)
			}
			if value.TicketID == nil {
				return 0
			}
			return *value.TicketID
		},
		count: func(owner *project.TicketsTicket) int64 {
			count, err := models.ReportsOptionalReport.Filter(relations.ReportsOptionalReport.Ticket.ID.Exact(owner.ID)).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			return count
		},
	})
	if len(seen) != len(expected) {
		t.Fatal("assignment reference execution incomplete", seen)
	}
	runAssignmentRollback(t, backend, models)
	runAssignmentExplicitKeys(t, models, observed, dialect)
}

func runAssignmentExplicitKeys(t *testing.T, models project.Models, observed *assignmentObserver, dialect string) {
	t.Helper()
	t.Run("explicit_zero_and_missing_target", func(t *testing.T) {
		ctx := t.Context()
		rawOwner := tickets.NewTicketWithID(0)
		rawOwner.Subject = "explicit zero"
		owner, err := models.TicketsTicket.New(rawOwner)
		if err != nil {
			t.Fatal(err)
		}
		checkExplicitSave := func(err error) {
			t.Helper()
			if dialect == "postgres" {
				// PostgreSQL's current write compiler explicitly rejects manual
				// identity INSERTs. Presence/assignment is still valid in memory.
				if !errors.Is(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported}) {
					t.Fatal("manual PostgreSQL identity insert boundary", err)
				}
			} else if err != nil {
				t.Fatal("manual SQLite key save", err)
			}
		}
		checkExplicitSave(owner.Save(ctx))
		if owner.ID != 0 {
			t.Fatal("explicit owner zero changed")
		}
		rawChild := reports.NewReportWithID(0)
		rawChild.Note = "explicit child zero"
		child, err := models.ReportsReport.New(rawChild)
		if err != nil {
			t.Fatal(err)
		}
		if err = owner.SetReport(child); err != nil {
			t.Fatal(err)
		}
		if _, err = child.Unwrap(); err != nil || child.TicketID != 0 {
			t.Fatal("assigned zero treated as absent", err)
		}
		checkExplicitSave(child.Save(ctx))
		if child.ID != 0 {
			t.Fatal("explicit child zero changed")
		}
		if got, found, err := owner.Report(ctx); err != nil || !found || got != child {
			t.Fatal("explicit zero reverse", err)
		}
		if err = owner.SetReport(nil); err != nil {
			t.Fatal(err)
		}
		if _, err = child.Unwrap(); !errors.Is(err, &query.Error{Code: query.CodeRequiredField}) {
			t.Fatal("cleared zero has presence", err)
		}
		rebound, err := child.WithTicketID(0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = rebound.Unwrap(); err != nil {
			t.Fatal("explicit key did not restore presence", err)
		}
		checkExplicitSave(rebound.Save(ctx))
		if dialect == "sqlite" {
			if got, err := rebound.Ticket(ctx); err != nil || got.ID != 0 {
				t.Fatal("explicit key lookup", err)
			}
		} else {
			if count, err := models.TicketsTicket.Filter(tickets.TicketFields.ID.Exact(0)).Count(ctx); err != nil || count != 0 {
				t.Fatal("unsupported insert published a parent", err)
			}
			if count, err := models.ReportsReport.Filter(reports.ReportFields.ID.Exact(0)).Count(ctx); err != nil || count != 0 {
				t.Fatal("unsupported insert published a child", err)
			}
		}
		optional, err := models.ReportsOptionalReport.New(reports.OptionalReport{Note: "zero optional"})
		if err != nil {
			t.Fatal(err)
		}
		if err = owner.SetOptionalReport(optional); err != nil {
			t.Fatal(err)
		}
		if optional.TicketID == nil || *optional.TicketID != 0 {
			t.Fatal("nullable zero lost presence")
		}
		if dialect == "sqlite" {
			if err = optional.Save(ctx); err != nil {
				t.Fatal(err)
			}
		} else {
			if err = optional.Save(ctx); err == nil || errors.Is(err, &query.Error{Code: query.CodeUnsavedRelatedObject}) || errors.Unwrap(err) == nil {
				t.Fatal("missing nullable zero did not reach native FK constraint", err)
			}
		}
		if err = owner.SetOptionalReport(nil); err != nil {
			t.Fatal(err)
		}
		if err = optional.Save(ctx); err != nil || optional.TicketID != nil {
			t.Fatal("nullable zero did not clear", err)
		}

		ghost, err := models.TicketsTicket.New(tickets.NewTicketWithID(9_000_001))
		if err != nil {
			t.Fatal(err)
		}
		missing, err := models.ReportsReport.New(reports.Report{Note: "missing physical parent"})
		if err != nil {
			t.Fatal(err)
		}
		countBefore, err := models.ReportsReport.Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err = ghost.SetReport(missing); err != nil {
			t.Fatal(err)
		}
		before := observed.writes
		err = missing.Save(ctx)
		if err == nil || errors.Is(err, &query.Error{Code: query.CodeUnsavedRelatedObject}) || errors.Unwrap(err) == nil || observed.writes != before+1 || missing.ID != 0 {
			t.Fatal("manual key did not reach the native FK constraint exactly once", err)
		}
		countAfter, err := models.ReportsReport.Count(ctx)
		if err != nil || countAfter != countBefore {
			t.Fatal("native FK failure persisted a child", err)
		}
	})
}

func runAssignmentCases[C any](t *testing.T, models project.Models, observed *assignmentObserver, queryCount func() uint64, expected map[string]assignmentReference, seen map[string]bool, h assignmentChild[C]) {
	t.Helper()
	ctx := t.Context()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	owner := func(saved bool) *project.TicketsTicket {
		value, err := models.TicketsTicket.New(tickets.Ticket{Subject: "assignment"})
		must(err)
		if saved {
			must(value.Save(ctx))
		}
		return value
	}
	child := func(parent *project.TicketsTicket, note string, saved bool) *C {
		value, err := h.new(note)
		must(err)
		if parent != nil {
			value, err = h.with(value, parent)
			must(err)
		}
		if saved {
			must(h.save(value))
		}
		return value
	}
	get := func(parent *project.TicketsTicket) *C { value, _, err := h.get(parent); must(err); return value }
	parent := func(value *C) *project.TicketsTicket { valueOwner, err := h.owner(value); must(err); return valueOwner }
	compare := func(t *testing.T, name string, execute func(*assignmentReference, func(string, func() error))) {
		t.Helper()
		name += "_" + h.kind
		want, exists := expected[name]
		if !exists || seen[name] {
			t.Fatal("unowned reference", name)
		}
		seen[name] = true
		got := assignmentReference{Name: name, States: map[string][]bool{}, Steps: map[string]assignmentStep{}}
		step := func(label string, operation func() error) {
			t.Helper()
			reads, writes, selects := observed.reads, observed.writes, queryCount()
			err := operation()
			result := assignmentStep{Selects: observed.reads - reads, Writes: observed.writes - writes}
			if queryCount()-selects != uint64(result.Selects) {
				t.Fatal("assignment query observer disagrees with actual SELECTs", label)
			}
			switch {
			case err == nil:
			case errors.Is(err, &query.Error{Category: query.CategoryModelState, Code: query.CodeUnsavedRelatedObject}):
				result.Error = "ValueError"
			case errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}):
				if errors.Unwrap(err) == nil {
					t.Fatal("native uniqueness cause was lost", err)
				}
				result.Error = "IntegrityError"
			case h.kind == "required" && label == "save_clear" && errors.Is(err, &query.Error{Category: query.CategoryField, Code: query.CodeRequiredField}):
				// A required Go FK has explicit absence outside its int64 storage value.
				// Reject it before I/O, while Django attempts a NULL write and rejects it.
				if result.Selects != 0 || result.Writes != 0 || want.Steps[label] != (assignmentStep{Error: "IntegrityError", Writes: 1}) {
					t.Fatal("required clear boundary", result, want.Steps[label])
				}
				result = want.Steps[label]
			default:
				t.Fatal("unexpected assignment result", label, err)
			}
			got.Steps[label] = result
		}
		execute(&got, step)
		if name == "unsaved_reverse_"+h.kind {
			// GoDj retains the other wrapper's explicitly assigned cache on a
			// failed Save. Django invalidates that reciprocal cache before its
			// unsaved-target error (DEV-0018); durable outcomes still match.
			if !reflect.DeepEqual(want.States["owner_saved"], []bool{false, true}) || !reflect.DeepEqual(got.States["owner_saved"], []bool{true, true}) {
				t.Fatal("unsaved reverse cache boundary", want.States, got.States)
			}
			copied := make(map[string][]bool, len(want.States))
			for key, state := range want.States {
				copied[key] = append([]bool(nil), state...)
			}
			copied["owner_saved"] = []bool{true, true}
			want.States = copied
		}
		if !reflect.DeepEqual(got, want) {
			a, _ := json.Marshal(got)
			b, _ := json.Marshal(want)
			t.Fatalf("assignment %s\ngot %s\nwant %s", name, a, b)
		}
	}
	t.Run(h.kind, func(t *testing.T) {
		t.Run("reverse_saved", func(t *testing.T) {
			compare(t, "reverse_saved", func(result *assignmentReference, step func(string, func() error)) {
				first, second := owner(true), owner(true)
				value := child(first, "original", true)
				step("assign", func() error { return h.set(second, value) })
				result.States["assigned"] = []bool{h.fk(value) == second.ID, get(second) == value, parent(value) == second}
				result.States["before_save"] = []bool{h.stored(value) == first.ID}
				must(second.Save(ctx))
				result.States["after_owner_save"] = []bool{h.stored(value) == first.ID}
				must(h.save(value))
				result.States["after_child_save"] = []bool{h.stored(value) == second.ID}
				step("clear", func() error { return h.set(second, nil) })
				result.States["cleared"] = []bool{h.fk(value) == 0, get(second) == nil}
				step("save_clear", func() error { return h.save(value) })
				expectedKey := second.ID
				if h.kind == "optional" {
					expectedKey = 0
				}
				result.States["after_clear_save"] = []bool{h.stored(value) == expectedKey}
			})
		})
		t.Run("unsaved_reverse", func(t *testing.T) {
			compare(t, "unsaved_reverse", func(result *assignmentReference, step func(string, func() error)) {
				first, value := owner(false), child(nil, "pending", false)
				step("assign", func() error { return h.set(first, value) })
				result.States["assigned"] = []bool{h.fk(value) == 0, get(first) == value, parent(value) == first}
				step("save_unsaved", func() error { return h.save(value) })
				must(first.Save(ctx))
				result.States["owner_saved"] = []bool{get(first) == value, h.fk(value) == 0}
				must(h.save(value))
				result.States["child_saved"] = []bool{h.fk(value) == first.ID, h.stored(value) == first.ID, get(first) == value, parent(value) == first}
			})
		})
		t.Run("unsaved_reverse_success", func(t *testing.T) {
			compare(t, "unsaved_reverse_success", func(result *assignmentReference, step func(string, func() error)) {
				first, value := owner(false), child(nil, "owner-first", false)
				step("assign", func() error { return h.set(first, value) })
				must(first.Save(ctx))
				result.States["owner_saved"] = []bool{get(first) == value, h.fk(value) == 0}
				must(h.save(value))
				result.States["child_saved"] = []bool{h.fk(value) == first.ID, h.stored(value) == first.ID, get(first) == value, parent(value) == first}
			})
		})
		t.Run("cold_clear", func(t *testing.T) {
			compare(t, "cold_clear", func(result *assignmentReference, step func(string, func() error)) {
				first := owner(true)
				value := child(first, "cold", true)
				fresh, found, err := models.TicketsTicket.Filter(tickets.TicketFields.ID.Exact(first.ID)).OrderBy(tickets.TicketFields.ID.Asc()).First(ctx)
				must(err)
				if !found {
					t.Fatal("missing owner")
				}
				step("clear", func() error { return h.set(fresh, nil) })
				step("read_after_clear", func() error {
					result.States["cleared"] = []bool{get(fresh) == nil, h.fk(value) == first.ID}
					return nil
				})
				must(fresh.Save(ctx))
				result.States["stored"] = []bool{h.stored(value) == first.ID}
			})
		})
		t.Run("replacement", func(t *testing.T) {
			compare(t, "replacement", func(result *assignmentReference, step func(string, func() error)) {
				first, second := owner(true), owner(true)
				value := child(first, "existing", true)
				replacement := child(nil, "replacement", false)
				must(h.set(first, value))
				step("assign", func() error { return h.set(first, replacement) })
				result.States["assigned"] = []bool{get(first) == replacement, parent(replacement) == first, h.fk(value) == first.ID, h.fk(replacement) == first.ID}
				step("save_duplicate", func() error { return h.save(replacement) })
				result.States["failure_preserved"] = []bool{h.key(replacement) == 0, h.fk(replacement) == first.ID, get(first) == replacement, h.count(first) == 1}
				step("reassign", func() error { return h.set(second, replacement) })
				must(h.save(replacement))
				result.States["reassigned"] = []bool{get(first) == replacement, get(second) == replacement, h.fk(value) == first.ID, h.stored(value) == first.ID, h.stored(replacement) == second.ID}
			})
		})
		t.Run("unsaved_forward", func(t *testing.T) {
			compare(t, "unsaved_forward", func(result *assignmentReference, step func(string, func() error)) {
				first, value := owner(false), child(nil, "forward", false)
				step("assign", func() error { var err error; value, err = h.with(value, first); return err })
				result.States["assigned"] = []bool{h.fk(value) == 0, parent(value) == first}
				step("save_unsaved", func() error { return h.save(value) })
				must(first.Save(ctx))
				must(h.save(value))
				result.States["saved"] = []bool{h.fk(value) == first.ID, parent(value) == first, h.stored(value) == first.ID}
			})
		})
		t.Run("forward_clear", func(t *testing.T) {
			compare(t, "forward_clear", func(result *assignmentReference, step func(string, func() error)) {
				first := owner(true)
				value := child(first, "forward-clear", true)
				original := value
				step("clear", func() error { var err error; value, err = h.clear(value); return err })
				if value == original || h.fk(original) != first.ID || parent(original) != first {
					t.Fatal("forward clear mutated the original wrapper")
				}
				result.States["cleared"] = []bool{h.fk(value) == 0}
				step("save_clear", func() error { return h.save(value) })
				expectedKey := first.ID
				if h.kind == "optional" {
					expectedKey = 0
				}
				result.States["stored"] = []bool{h.stored(value) == expectedKey}
			})
		})
		t.Run("forward_cache_and_failed_save", func(t *testing.T) {
			unsaved, saved := owner(false), owner(true)
			original := child(nil, "overridden", false)
			value, err := h.with(original, unsaved)
			must(err)
			if !errors.Is(h.unwrap(value), &query.Error{Code: query.CodeUnsavedRelatedObject}) {
				t.Fatal("pending target escaped through Unwrap")
			}
			h.rawSet(value, saved.ID)
			must(h.save(value))
			if h.stored(value) != saved.ID || parent(value).ID != saved.ID || h.fk(original) != 0 || h.key(original) != 0 {
				t.Fatal("raw FK did not override pending ownership")
			}
			assigned, err := h.with(value, saved)
			must(err)
			same, err := h.withID(assigned, saved.ID)
			must(err)
			beforeReads := observed.reads
			if parent(same) != saved || observed.reads != beforeReads {
				t.Fatal("same explicit key discarded the warm target")
			}
			if _, err := h.with(value, nil); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("nil forward target accepted", err)
			}
			other := owner(true)
			candidate, err := h.with(value, other)
			must(err)
			failure := errors.New("injected write failure")
			beforeWrites := observed.writes
			observed.failure = failure
			err = h.save(candidate)
			observed.failure = nil
			if !errors.Is(err, failure) || observed.writes != beforeWrites+1 || h.fk(candidate) != other.ID || parent(candidate) != other || h.stored(value) != saved.ID {
				t.Fatal("failed Save hid cause, wrote storage or rewound caller memory", err)
			}
			must(h.save(candidate))
			if h.stored(candidate) != other.ID {
				t.Fatal("explicit retry did not persist caller state")
			}
		})
		t.Run("reverse_invalid_target_has_no_effect", func(t *testing.T) {
			first := owner(true)
			value := child(first, "stable", true)
			must(h.set(first, value))
			otherModels, err := project.Using(observed)
			must(err)
			foreign, err := otherModels.TicketsTicket.New(tickets.Ticket{Subject: "foreign"})
			must(err)
			beforeReads, beforeWrites := observed.reads, observed.writes
			if err := h.set(foreign, value); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("origin accepted", err)
			}
			copied := *first
			if err := h.set(&copied, value); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("copied owner accepted", err)
			}
			if err := h.set(nil, value); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("nil owner accepted", err)
			}
			copiedChild := *value
			if err := h.set(first, &copiedChild); !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatal("copied child accepted", err)
			}
			originalID := first.ID
			first.ID++
			if err := h.set(first, value); !errors.Is(err, &query.Error{Code: query.CodePrimaryKeyUpdateField}) {
				t.Fatal("changed owner key accepted", err)
			}
			first.ID = originalID
			if get(first) != value || parent(value) != first || h.fk(value) != first.ID || observed.reads != beforeReads || observed.writes != beforeWrites {
				t.Fatal("invalid assignment had effects")
			}
		})
	})
}

func runAssignmentRollback(t *testing.T, backend ProductBackend, models project.Models) {
	t.Helper()
	ctx := t.Context()
	owner, err := models.TicketsTicket.New(tickets.Ticket{Subject: "before rollback"})
	if err != nil {
		t.Fatal(err)
	}
	if err = owner.Save(ctx); err != nil {
		t.Fatal(err)
	}
	original, err := models.ReportsReport.New(reports.Report{TicketID: owner.ID, Note: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if err = original.Save(ctx); err != nil {
		t.Fatal(err)
	}
	var attempted *project.ReportsReport
	err = backend.Atomic(ctx, func(session db.Session) error {
		bound, e := project.UsingSession(session)
		if e != nil {
			return e
		}
		parent, found, e := bound.TicketsTicket.Filter(tickets.TicketFields.ID.Exact(owner.ID)).OrderBy(tickets.TicketFields.ID.Asc()).First(ctx)
		if e != nil || !found {
			return errors.New("missing transactional owner")
		}
		parent.Subject = "must roll back"
		if e = parent.Save(ctx); e != nil {
			return e
		}
		attempted, e = bound.ReportsReport.New(reports.Report{Note: "duplicate"})
		if e != nil {
			return e
		}
		if e = parent.SetReport(attempted); e != nil {
			return e
		}
		return attempted.Save(ctx)
	})
	if !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) || attempted == nil || attempted.ID != 0 || attempted.TicketID != owner.ID {
		t.Fatal("native transaction duplicate result", err)
	}
	stored, found, err := models.TicketsTicket.Filter(tickets.TicketFields.ID.Exact(owner.ID)).OrderBy(tickets.TicketFields.ID.Asc()).First(ctx)
	if err != nil || !found || stored.Subject != "before rollback" {
		t.Fatal("assignment failure did not roll back earlier mutation", err)
	}
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	count, err := models.ReportsReport.Filter(relations.ReportsReport.Ticket.ID.Exact(owner.ID)).Count(ctx)
	if err != nil || count != 1 {
		t.Fatal("failed transaction published a duplicate", count, err)
	}
}
