package onetoonetest

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	fixture "github.com/progresshans/godj/conformance/onetoonefixture"
	"github.com/progresshans/godj/conformance/onetoonefixture/project"
	"github.com/progresshans/godj/conformance/onetoonefixture/reports"
	"github.com/progresshans/godj/conformance/onetoonefixture/tickets"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/migrations"
	mb "github.com/progresshans/godj/migrations/backend"
	"github.com/progresshans/godj/migrations/definition"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type ProductBackend interface {
	db.Session
	db.Atomic
	db.RelationAtomic
	mb.RevisionFencedBackend
	Close() error
}

type countedQuery struct {
	db.Queryer
	calls atomic.Int64
	fail  error
}

type productRows struct {
	values   [][]any
	index    int
	closeErr error
	closed   int
}

func (r *productRows) Next() bool   { r.index++; return r.index < len(r.values) }
func (r *productRows) Err() error   { return nil }
func (r *productRows) Close() error { r.closed++; return r.closeErr }
func (r *productRows) Scan(destinations ...any) error {
	if r.index < 0 || r.index >= len(r.values) || len(destinations) != len(r.values[r.index]) {
		return errors.New("invalid injected row shape")
	}
	for index, destination := range destinations {
		pointer, value := reflect.ValueOf(destination), reflect.ValueOf(r.values[r.index][index])
		if pointer.Kind() != reflect.Pointer || pointer.IsNil() || !value.Type().AssignableTo(pointer.Elem().Type()) {
			return fmt.Errorf("invalid injected destination %d", index)
		}
		pointer.Elem().Set(value)
	}
	return nil
}

type suppliedQuery struct{ rows db.Rows }

func (q *suppliedQuery) Query(context.Context, query.Plan) (db.Rows, error) { return q.rows, nil }

func (q *countedQuery) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	q.calls.Add(1)
	if q.fail != nil {
		return nil, q.fail
	}
	return q.Queryer.Query(ctx, plan)
}

func productHistory(t testing.TB) migrations.LoadedDefinitionSet {
	t.Helper()
	spec, err := fixture.ProjectSpec(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	var sources []definition.Source
	for index, app := range spec.Apps {
		migration := migrations.Migration{App: app.Schema.AppLabel, Name: "0001_initial"}
		if index > 0 {
			migration.Dependencies = []migrations.MigrationKey{{App: spec.Apps[0].Schema.AppLabel, Name: "0001_initial"}}
		}
		for _, model := range app.Schema.Models {
			migration.Operations = append(migration.Operations, migrations.CreateModel{AppLabel: app.Schema.AppLabel, Model: model})
		}
		wire, err := definition.Encode(definition.Producer{Name: "one-to-one-product", Version: "1"}, migration)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, definition.Source{SourceID: migration.App, Document: wire})
	}
	loaded, _, err := definition.Load(sources...)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func RunProduct(t *testing.T, backend ProductBackend) {
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
	first, err := tickets.TicketObjects.Create(ctx, backend, tickets.NewTicketCreate("first"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := tickets.TicketObjects.Create(ctx, backend, tickets.NewTicketCreate("second"))
	if err != nil {
		t.Fatal(err)
	}
	third, err := tickets.TicketObjects.Create(ctx, backend, tickets.NewTicketCreate("third"))
	if err != nil {
		t.Fatal(err)
	}
	reverse, err := project.BindReverseObjects()
	if err != nil {
		t.Fatal(err)
	}
	forward, err := project.BindObjects()
	if err != nil {
		t.Fatal(err)
	}
	prefetch, err := project.BindReversePrefetches()
	if err != nil {
		t.Fatal(err)
	}
	reads := &countedQuery{Queryer: backend}
	owner, err := reverse.TicketsTicket.From(reads, first)
	if err != nil {
		t.Fatal(err)
	}
	related, err := owner.Report()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		value, present, err := related.Get(ctx)
		if err != nil || present || value != (reports.Report{}) {
			t.Fatal("missing reverse object is not an ordinary absent result", value, present, err)
		}
	}
	if reads.calls.Load() != 1 {
		t.Fatal("missing reverse cache was not reused")
	}
	report, err := reports.ReportObjects.Create(ctx, backend, reports.NewReportCreate(first.ID, "original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := related.Get(ctx); err != nil || present || reads.calls.Load() != 1 {
		t.Fatal("external write changed an owned cache", present, err)
	}
	related, err = related.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		value, present, err := related.Get(ctx)
		if err != nil || !present || value != report {
			t.Fatal("fresh reverse result", value, present, err)
		}
	}
	if reads.calls.Load() != 2 {
		t.Fatal("fresh reverse object did not own one evaluation")
	}
	concurrent, err := related.Fresh()
	if err != nil {
		t.Fatal(err)
	}
	start := reads.calls.Load()
	var wait sync.WaitGroup
	results := make(chan error, 16)
	for range 16 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			value, present, err := concurrent.Get(ctx)
			if err == nil && (!present || value != report) {
				err = errors.New("concurrent one-to-one result differs")
			}
			results <- err
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if reads.calls.Load() != start+1 {
		t.Fatal("concurrent reverse loads did not share one evaluation")
	}
	// A separately constructed forward handle does not inherit reciprocal
	// descriptor cache from the reverse loader. Its own repeated reads are warm.
	reciprocalStart := reads.calls.Load()
	for range 2 {
		child, present, err := related.Get(ctx)
		if err != nil || !present {
			t.Fatal("reverse result disappeared before a new forward owner", err)
		}
		object, err := forward.ReportsReport.From(reads, child)
		if err != nil {
			t.Fatal(err)
		}
		for range 2 {
			got, err := object.Ticket(ctx)
			if err != nil || got != first {
				t.Fatal("independently owned forward result", got, err)
			}
		}
	}
	if reads.calls.Load() != reciprocalStart+2 {
		t.Fatal("new forward owners shared reciprocal cache or repeated reads were cold")
	}
	copied := *related
	if _, _, err := copied.Get(ctx); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeInvalidPlan}) {
		t.Fatal("copied handle accepted", err)
	}
	if _, err := reverse.TicketsTicket.From(reads, tickets.Ticket{ID: first.ID}); !errors.Is(err, &query.Error{Category: query.CategoryQuery, Code: query.CodeMissingPrimaryKey}) {
		t.Fatal("implicit owner key accepted", err)
	}
	zeroOwner, err := reverse.TicketsTicket.From(reads, tickets.NewTicketWithID(0))
	if err != nil {
		t.Fatal(err)
	}
	zero, err := zeroOwner.Report()
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := zero.Get(ctx); err != nil || present {
		t.Fatal("explicit zero key confused with unsaved state", present, err)
	}

	before := reads.calls.Load()
	batch, err := prefetch.TicketsTicket.Report(ctx, reads, []tickets.Ticket{first, second, first})
	if err != nil {
		t.Fatal(err)
	}
	for index, item := range batch {
		value, err := item.Report()
		if err != nil {
			t.Fatal(err)
		}
		got, present, err := value.Get(ctx)
		if err != nil || present != (index != 1) || present && got != report {
			t.Fatal("prefetch membership", index, got, present, err)
		}
	}
	if reads.calls.Load() != before+1 {
		t.Fatal("reverse prefetch did not use one batch")
	}
	emptyBefore := reads.calls.Load()
	if empty, err := prefetch.TicketsTicket.Report(ctx, reads, nil); err != nil || len(empty) != 0 || reads.calls.Load() != emptyBefore {
		t.Fatal("empty prefetch performed I/O", err)
	}
	a, _ := batch[0].Report()
	b, _ := batch[2].Report()
	if a == b {
		t.Fatal("repeated owner shares a mutable related handle")
	}

	before = reads.calls.Load()
	eager, err := forward.ReportsReport.SelectRelated(reports.ReportObjects.Using(reads)).WithTicket().All(ctx)
	if err != nil || len(eager) != 1 {
		t.Fatal("one-to-one forward eager", err)
	}
	parent, err := eager[0].Ticket(ctx)
	if err != nil || parent != first || reads.calls.Load() != before+1 {
		t.Fatal("one-to-one eager cache", parent, err)
	}
	bindings, err := project.BindReverseRelations()
	if err != nil {
		t.Fatal(err)
	}
	typed := tickets.TicketObjects.Using(backend).Filter(bindings.TicketsTicket.Report.Note.Exact("original"))
	got, err := typed.All(ctx)
	if err != nil || !reflect.DeepEqual(got, []tickets.Ticket{first}) {
		t.Fatal("typed reverse lookup", got, err)
	}
	parsed, err := bindings.TicketsTicket.ParseDynamic(func(ir.Field, query.Lookup) bool { return true }, []orm.LookupInput{{Key: "report__note", Value: "original"}})
	if err != nil {
		t.Fatal(err)
	}
	got, err = tickets.TicketObjects.Using(backend).Filter(parsed...).All(ctx)
	if err != nil || !reflect.DeepEqual(got, []tickets.Ticket{first}) {
		t.Fatal("dynamic reverse lookup", got, err)
	}
	// Fault controls deliberately bypass the native constraint to exercise the
	// runtime's cardinality and all-or-nothing publication checks.
	corrupt := &productRows{index: -1, values: [][]any{{int64(10), first.ID, "one"}, {int64(11), first.ID, "two"}}}
	faults := &suppliedQuery{rows: corrupt}
	faultOwner, err := reverse.TicketsTicket.From(faults, first)
	if err != nil {
		t.Fatal(err)
	}
	faultObject, _ := faultOwner.Report()
	if _, present, err := faultObject.Get(ctx); present || !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectCardinality}) || corrupt.closed == 0 {
		t.Fatal("single-object load hid corrupt cardinality", present, err)
	}
	corrupt = &productRows{index: -1, values: [][]any{{int64(10), first.ID, "one"}, {int64(11), first.ID, "two"}}}
	faults.rows = corrupt
	if result, err := prefetch.TicketsTicket.Report(ctx, faults, []tickets.Ticket{first, second}); result != nil || !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeRelatedObjectCardinality}) {
		t.Fatal("prefetch published partial results for duplicate owner membership", result, err)
	}
	closeFailure := errors.New("injected rows close failure")
	corrupt = &productRows{index: -1, values: [][]any{{report.ID, first.ID, report.Note}}, closeErr: closeFailure}
	faults.rows = corrupt
	faultOwner, err = reverse.TicketsTicket.From(faults, first)
	if err != nil {
		t.Fatal(err)
	}
	faultObject, _ = faultOwner.Report()
	if _, present, err := faultObject.Get(ctx); present || !errors.Is(err, closeFailure) {
		t.Fatal("row close failure published a result", present, err)
	}
	faults.rows = &productRows{index: -1, values: [][]any{{report.ID, first.ID, report.Note}}}
	if value, present, err := faultObject.Get(ctx); err != nil || !present || value != report {
		t.Fatal("row close failure poisoned retry", value, present, err)
	}

	duplicate := backend.Atomic(ctx, func(session db.Session) error {
		if _, err := tickets.TicketObjects.Update(ctx, session, second, (tickets.TicketPatch{}).WithSubject("must rollback")); err != nil {
			return err
		}
		_, err := reports.ReportObjects.Create(ctx, session, reports.NewReportCreate(first.ID, "duplicate"))
		return err
	})
	if !errors.Is(duplicate, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
		t.Fatal("one-to-one native uniqueness missing", duplicate)
	}
	stored, _, err := tickets.TicketObjects.Using(backend).Filter(tickets.TicketFields.ID.Exact(second.ID)).OrderBy(tickets.TicketFields.ID.Asc()).First(ctx)
	if err != nil || stored != second {
		t.Fatal("native duplicate did not roll back earlier update", stored, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := reports.OptionalReportObjects.Create(ctx, backend, reports.NewOptionalReportCreate("orphan")); err != nil {
			t.Fatal("multiple nullable one-to-one rows rejected", err)
		}
	}
	optional, err := reports.OptionalReportObjects.Create(ctx, backend, reports.NewOptionalReportCreate("linked").WithTicketID(third.ID))
	if err != nil {
		t.Fatal(err)
	}
	deleters, err := project.BindRelationDeleters()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := deleters.TicketsTicket.Delete(ctx, backend, &first); err == nil {
		t.Fatal("required one-to-one PROTECT ignored")
	}
	if count, err := deleters.TicketsTicket.Delete(ctx, backend, &third); err != nil || count != 1 {
		t.Fatal("nullable one-to-one SET_NULL failed", count, err)
	}
	optional, present, err := reports.OptionalReportObjects.Using(backend).Filter(reports.OptionalReportFields.ID.Exact(optional.ID)).OrderBy(reports.OptionalReportFields.ID.Asc()).First(ctx)
	if err != nil || !present || optional.TicketID != nil {
		t.Fatal("SET_NULL did not preserve child", optional, err)
	}
	if _, err := reports.LinkObjects.Create(ctx, backend, reports.NewLinkCreate(first.ID, "unique FK")); err != nil {
		t.Fatal(err)
	}
	collectionOwner, err := reverse.TicketsTicket.From(backend, first)
	if err != nil {
		t.Fatal(err)
	}
	collection, err := collectionOwner.Links()
	if err != nil {
		t.Fatal(err)
	}
	links, err := collection.All(ctx)
	if err != nil || len(links) != 1 {
		t.Fatal("unique FK lost its collection API", links, err)
	}

	coldOwner, err := reverse.TicketsTicket.From(reads, first)
	if err != nil {
		t.Fatal(err)
	}
	cold, _ := coldOwner.Report()
	before = reads.calls.Load()
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := cold.Get(canceled); !errors.Is(err, context.Canceled) || reads.calls.Load() != before {
		t.Fatal("cancellation reached I/O", err)
	}
	sentinel := errors.New("query failure")
	reads.fail = sentinel
	if _, _, err := cold.Get(ctx); !errors.Is(err, sentinel) {
		t.Fatal("query failure hidden", err)
	}
	reads.fail = nil
	if got, present, err := cold.Get(ctx); err != nil || !present || got != report {
		t.Fatal("query failure poisoned later evaluation", got, present, err)
	}
}
