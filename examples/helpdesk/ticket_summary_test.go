package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"math"
	"net/http"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/duration"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/jsonvalue"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
	"github.com/progresshans/godj/web/sessionauth"
)

type summaryEnvelope struct {
	Category struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	} `json:"category"`
	Page        int64         `json:"page"`
	PageSize    int64         `json:"page_size"`
	MinimumOpen int64         `json:"min_open"`
	Total       int64         `json:"total_groups"`
	Results     []summaryItem `json:"results"`
}
type summaryItem struct {
	Priority    *int64 `json:"priority"`
	RaiseTo     int64  `json:"raise_to_priority"`
	RaiseLabel  string `json:"raise_to_label"`
	WouldChange bool   `json:"would_change"`
	Label       string `json:"priority_label"`
	Total, Open int64
	Cost        *string  `json:"expected_cost_total"`
	Effort      *float64 `json:"effort_average"`
	Elapsed     *string  `json:"elapsed_total"`
}

func TestTicketSummaryRequiresUsableAuthentication(t *testing.T) {
	application, err := helpdesk.New(helpdeskConstructionBackend{t: t}, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, authentication := range []*sessionauth.Runtime{nil, {}} {
		if _, err := application.TicketSummary(authentication); err == nil {
			t.Fatal("summary accepted unusable authentication")
		}
	}
}

type summaryBackend struct {
	helpdesk.Backend
	mode                       string
	snapshots, queries, groups int
	closed                     int
	rowsets                    int
	cancel                     context.CancelFunc
}

func (backend *summaryBackend) ReadSnapshot(ctx context.Context, callback func(db.Queryer) error) error {
	backend.snapshots++
	if backend.mode == "missing_callback" {
		return nil
	}
	if backend.mode == "nil_reader" {
		return callback(nil)
	}
	if backend.mode == "typed_nil_reader" {
		var reader *summaryReader
		return callback(reader)
	}
	err := backend.Backend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		wrapped := &summaryReader{Queryer: reader, owner: backend}
		if backend.mode == "concurrent_callback" {
			var wait sync.WaitGroup
			wait.Add(2)
			failures := make(chan error, 2)
			for range 2 {
				go func() { defer wait.Done(); failures <- callback(wrapped) }()
			}
			wait.Wait()
			close(failures)
			var result error
			for failure := range failures {
				result = errors.Join(result, failure)
			}
			return result
		}
		failure := callback(wrapped)
		if backend.mode == "double_callback" {
			failure = errors.Join(failure, callback(wrapped))
		}
		if backend.mode == "swallowed_error" {
			return nil
		}
		return failure
	})
	if backend.mode == "cleanup_failure" {
		return errors.Join(err, errors.New("private summary cleanup failure"))
	}
	if backend.mode == "post_cleanup_cancel" {
		backend.cancel()
	}
	return err
}

type summaryReader struct {
	db.Queryer
	owner *summaryBackend
}

func (reader *summaryReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	reader.owner.queries++
	if plan.ResultShape().GroupMode() == query.GroupPage {
		reader.owner.groups++
		if reader.owner.mode == "query_failure" || reader.owner.mode == "swallowed_error" {
			return nil, errors.New("private summary query failure")
		}
		if reader.owner.mode == "canceled_read" {
			reader.owner.cancel()
		}
		rows, err := reader.Queryer.Query(ctx, plan)
		if err != nil {
			return nil, err
		}
		reader.owner.rowsets++
		return &summaryRows{Rows: rows, owner: reader.owner}, nil
	}
	if plan.Table() != "helpdesk_category" {
		return nil, fmt.Errorf("summary unexpectedly loaded %s", plan.Table())
	}
	return reader.Queryer.Query(ctx, plan)
}

type summaryRows struct {
	db.Rows
	owner *summaryBackend
}

func (rows *summaryRows) Scan(destinations ...any) error {
	if rows.owner.mode == "scan_failure" {
		return errors.New("private summary scan failure")
	}
	return rows.Rows.Scan(destinations...)
}
func (rows *summaryRows) Err() error {
	if rows.owner.mode == "iteration_failure" {
		return errors.Join(rows.Rows.Err(), errors.New("private summary iteration failure"))
	}
	return rows.Rows.Err()
}
func (rows *summaryRows) Close() error {
	rows.owner.closed++
	err := rows.Rows.Close()
	if rows.owner.mode == "close_failure" {
		return errors.Join(err, errors.New("private summary close failure"))
	}
	return err
}

func verifyHelpdeskTicketSummary(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, authenticated *helpdeskClient) {
	category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Summary <script>& category"))
	if err != nil {
		t.Fatal(err)
	}
	outside, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Summary private outside"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("summary private outside", outside.ID).WithPriority(1)); err != nil {
		t.Fatal(err)
	}
	var keys []int64
	create := func(priority *int64, closed bool, payload bool) {
		input := models.NewTicketCreate("Summary ticket", category.ID).WithClosed(closed)
		if priority != nil {
			input = input.WithPriority(*priority)
		}
		if payload {
			document, err := jsonvalue.Parse([]byte(`{"retained":9007199254740993}`))
			if err != nil {
				t.Fatal(err)
			}
			input = input.WithExternalPayload(document)
		}
		if priority == nil {
			cost, err := decimal.Parse("999999999999.99")
			if err != nil {
				t.Fatal(err)
			}
			effort, microseconds := float64(2), int64(2)
			if payload {
				effort, microseconds = 1, 1
			}
			if closed {
				cost, err = decimal.Parse("0.02")
				if err != nil {
					t.Fatal(err)
				}
				effort, microseconds = 3, -1
			}
			input = input.WithExpectedCost(cost).WithEffort(effort).WithElapsed(duration.FromMicroseconds(microseconds))
		} else if *priority == -1 && !closed {
			input = input.WithExpectedCost(decimal.Decimal{}).WithEffort(0).WithElapsed(duration.Duration{})
		}
		row, err := models.TicketObjects.Create(ctx, runtime, input)
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, row.ID)
	}
	for _, value := range []int64{-1, 0, 1, math.MinInt64, math.MaxInt64} {
		create(&value, value == 0, false)
	}
	low := int64(-1)
	create(&low, true, false)
	create(nil, false, true)
	create(nil, false, false)
	create(nil, true, false)
	for value := int64(10); value <= 30; value++ {
		create(&value, false, false)
	}
	originals, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.In(keys...)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	newClient := func(categoryID int64, mode string, authorizer auth.Authorizer) (*helpdeskClient, *summaryBackend, *int) {
		backend := &summaryBackend{Backend: runtime, mode: mode}
		application, err := helpdesk.New(backend, categoryID)
		if err != nil {
			t.Fatal(err)
		}
		audits := new(int)
		client := helpdeskHTTP(t, application, runtime, authorizer, func(context.Context, db.Session, admin.PreparedEvent) error {
			*audits++
			return errors.New("summary attempted an audit")
		})
		client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		return client, backend, audits
	}
	decode := func(client *helpdeskClient, suffix string) summaryEnvelope {
		response := client.request(http.MethodGet, "/api/tickets/summary/"+suffix, "", false)
		var result summaryEnvelope
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &result) != nil {
			t.Fatalf("summary response %d %s", response.Code, response.Body.String())
		}
		if response.Header().Get("Cache-Control") != "no-store" || result.Category.ID != category.ID || result.Category.Name != category.Name || result.Results == nil {
			t.Fatal("summary lost category, cache or array contract")
		}
		var wire struct{ Results []map[string]json.RawMessage }
		if err := json.Unmarshal(response.Body.Bytes(), &wire); err != nil {
			t.Fatal(err)
		}
		for index, row := range wire.Results {
			actual := result.Results[index]
			expected, change := int64(0), true
			if actual.Priority != nil {
				expected = *actual.Priority
				if expected == -1 || expected == 0 {
					expected++
				} else {
					change = false
				}
			}
			if actual.RaiseTo != expected || actual.WouldChange != change || actual.RaiseLabel == "" {
				t.Fatal("priority preview changed command policy", actual)
			}
			for _, name := range []string{"raise_to_priority", "raise_to_label", "would_change", "expected_cost_total", "effort_average", "elapsed_total"} {
				if _, present := row[name]; !present {
					t.Fatal("nullable metric omitted", name)
				}
			}
		}
		return result
	}
	t.Run("groups_filters_and_pages", func(t *testing.T) {
		client, backend, audits := newClient(category.ID, "", helpdeskDeniedPermissions{helpdesk.ChangeTicket, helpdesk.ViewCategory, helpdesk.ViewLabel})
		first := decode(client, "")
		if first.Total != 27 || first.Page != 1 || first.PageSize != 20 || len(first.Results) != 20 || first.Results[0].Priority != nil || first.Results[0].Label != "Not set" || first.Results[0].Total != 3 || first.Results[0].Open != 2 || first.Results[1].Priority == nil || *first.Results[1].Priority != math.MaxInt64 || first.Results[1].Label != "Other (9223372036854775807)" {
			t.Fatalf("first summary page %+v", first)
		}
		metrics := first.Results[0]
		if metrics.Cost == nil || *metrics.Cost != "2000000000000" || metrics.Effort == nil || *metrics.Effort != 2 || metrics.Elapsed == nil || *metrics.Elapsed != "00:00:00.000002" {
			t.Fatal("summary lost exact widened cost, average or duration", metrics)
		}
		if missing := first.Results[1]; missing.Cost != nil || missing.Effort != nil || missing.Elapsed != nil {
			t.Fatal("missing metrics became zero", missing)
		}
		for index := 2; index < 20; index++ {
			if first.Results[index].Priority == nil || *first.Results[index].Priority != int64(32-index) {
				t.Fatal("deterministic first page order")
			}
		}
		second := decode(client, "?p=2")
		if second.Total != 27 || len(second.Results) != 7 {
			t.Fatal("second group page count")
		}
		for index, key := range []int64{12, 11, 10, 1, -1, math.MinInt64, 0} {
			if second.Results[index].Priority == nil || *second.Results[index].Priority != key {
				t.Fatal("second group order")
			}
		}
		if second.Results[3].Label != "Urgent" || second.Results[4].Label != "Low" || second.Results[4].Total != 2 || second.Results[4].Open != 1 || second.Results[6].Label != "Normal" || second.Results[6].Open != 0 {
			t.Fatal("choice labels or conditional count")
		}
		zero := second.Results[4]
		if zero.Cost == nil || *zero.Cost != "0" || zero.Effort == nil || *zero.Effort != 0 || zero.Elapsed == nil || *zero.Elapsed != "00:00:00" {
			t.Fatal("present zero or excluded NULL metric", zero)
		}
		for _, input := range []struct {
			suffix string
			count  int64
			rows   int
		}{{"?min_open=2", 1, 1}, {"?min_open=1", 26, 20}, {"?min_open=9223372036854775807", 0, 0}, {"?p=50001", 27, 0}, {"?p=2&min_open=2", 1, 0}} {
			result := decode(client, input.suffix)
			if result.Total != input.count || len(result.Results) != input.rows {
				t.Fatal("filtered/past-end cardinality", input.suffix)
			}
		}
		page := client.request(http.MethodGet, helpdesk.TicketSummaryPath+"?p=2", "", false)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Summary &lt;script&gt;&amp; category") || strings.Contains(page.Body.String(), "<script>") || strings.Contains(page.Body.String(), "private outside") || !strings.Contains(page.Body.String(), "Other (-9223372036854775808)") || !strings.Contains(page.Body.String(), "?p=1&amp;min_open=0") {
			t.Fatalf("summary HTML %d %s", page.Code, page.Body.String())
		}
		if !strings.Contains(page.Body.String(), "<td>0</td><td>0</td><td>00:00:00</td>") || !strings.Contains(page.Body.String(), "Total expected cost") || !strings.Contains(page.Body.String(), "Average effort") || !strings.Contains(page.Body.String(), "Total elapsed") {
			t.Fatal("summary HTML numeric columns", page.Body.String())
		}
		wide := client.request(http.MethodGet, helpdesk.TicketSummaryPath, "", false)
		if wide.Code != http.StatusOK || !strings.Contains(wide.Body.String(), "<td>2000000000000</td><td>2</td><td>00:00:00.000002</td>") {
			t.Fatal("summary HTML lost exact metrics", wide.Code, wide.Body.String())
		}
		if backend.snapshots != 9 || backend.queries != 18 || backend.groups != 9 || backend.closed != 9 || *audits != 0 {
			t.Fatal("summary must use one snapshot/two reads, close rows, and avoid audit", backend, *audits)
		}
	})
	t.Run("nonfinite_metrics_publish_no_result", func(t *testing.T) {
		category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Non-finite metrics"))
		if err != nil {
			t.Fatal(err)
		}
		for _, effort := range []float64{math.Inf(1), math.Inf(-1)} {
			if _, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Metric special value", category.ID).WithEffort(effort)); err != nil {
				t.Fatal(err)
			}
		}
		client, backend, audits := newClient(category.ID, "", auth.PrincipalAuthorizer{})
		for _, path := range []string{helpdesk.TicketSummaryPath, "/api/tickets/summary/"} {
			response := client.request(http.MethodGet, path, "", false)
			if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "expected_cost_total") || strings.Contains(response.Body.String(), "Metric special") || strings.Contains(response.Body.String(), "NaN") || *audits != 0 {
				t.Fatal("failed metric published a partial result", path, response.Code, response.Body.String())
			}
		}
		if backend.snapshots != 2 || backend.groups != 2 || backend.closed != backend.rowsets {
			t.Fatal("failed metric leaked its read scope", backend)
		}
	})
	t.Run("admission_and_query", func(t *testing.T) {
		for _, path := range []string{helpdesk.TicketSummaryPath, "/api/tickets/summary/"} {
			client, backend, _ := newClient(category.ID, "", auth.PrincipalAuthorizer{})
			for _, suffix := range []string{"?p=0", "?p=50002", "?p=01", "?p=+1", "?p=1.0", "?p=1e1", "?p=", "?p=1&p=2", "?min_open=-1", "?min_open=01", "?min_open=", "?min_open=9223372036854775808", "?min_open=0&min_open=1", "?category=1", "?p=%FF", "?p=%", "?p=1;min_open=0", "?" + strings.Repeat("x", 129)} {
				response := client.request(http.MethodGet, path+suffix, "", false)
				if response.Code != http.StatusBadRequest || backend.snapshots != 0 {
					t.Fatal("invalid query reached database", path, suffix, response.Code)
				}
			}
			if path == "/api/tickets/summary/" {
				response := client.request(http.MethodGet, path+"?min_open=bad&p=bad", "", false)
				var envelope struct {
					Code   string
					Errors []struct{ Field, Code string }
				}
				if response.Code != http.StatusBadRequest || json.Unmarshal(response.Body.Bytes(), &envelope) != nil || envelope.Code != "validation_error" || len(envelope.Errors) != 1 || envelope.Errors[0].Field != "p" || envelope.Errors[0].Code != "invalid" || backend.snapshots != 0 {
					t.Fatal("summary diagnostic order differs from the declaration", response.Code, response.Body)
				}
			}
			denied, deniedBackend, _ := newClient(category.ID, "", helpdeskDeniedPermissions{helpdesk.ViewTicket})
			if response := denied.request(http.MethodGet, path+"?p=bad", "", false); response.Code != http.StatusForbidden || deniedBackend.snapshots != 0 {
				t.Fatal("permission must precede parsing", response.Code)
			}
			denied.cookies = map[string]*http.Cookie{}
			response := denied.request(http.MethodGet, path+"?p=bad", "", false)
			want := http.StatusFound
			if strings.HasPrefix(path, "/api/") {
				want = http.StatusForbidden
			}
			if response.Code != want || deniedBackend.snapshots != 0 {
				t.Fatal("anonymous summary admission", path, response.Code)
			}
		}
	})
	t.Run("empty_and_missing_category", func(t *testing.T) {
		empty, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Summary empty"))
		if err != nil {
			t.Fatal(err)
		}
		client, backend, _ := newClient(empty.ID, "", auth.PrincipalAuthorizer{})
		response := client.request(http.MethodGet, "/api/tickets/summary/", "", false)
		var value summaryEnvelope
		if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.Total != 0 || value.Results == nil || len(value.Results) != 0 || backend.groups != 1 {
			t.Fatal("empty category lost its empty summary")
		}
		for _, path := range []string{helpdesk.TicketSummaryPath, "/api/tickets/summary/"} {
			missing, reads, _ := newClient(math.MaxInt64, "", auth.PrincipalAuthorizer{})
			if response := missing.request(http.MethodGet, path, "", false); response.Code != http.StatusNotFound || reads.groups != 0 {
				t.Fatal("missing category must stop before grouping", response.Code)
			}
		}
	})
	for _, mode := range []string{"missing_callback", "nil_reader", "typed_nil_reader", "double_callback", "concurrent_callback", "swallowed_error", "cleanup_failure", "post_cleanup_cancel", "query_failure", "canceled_read", "scan_failure", "iteration_failure", "close_failure"} {
		t.Run(mode, func(t *testing.T) {
			for _, path := range []string{helpdesk.TicketSummaryPath, "/api/tickets/summary/"} {
				client, backend, audits := newClient(category.ID, mode, auth.PrincipalAuthorizer{})
				work, cancel := context.WithCancel(ctx)
				defer cancel()
				backend.cancel = cancel
				response := client.requestContext(work, http.MethodGet, path, "", false)
				if response.Code != http.StatusInternalServerError || strings.Contains(response.Body.String(), "private summary") || strings.Contains(response.Body.String(), "priority_label") || *audits != 0 {
					t.Fatal("failed summary published output", mode, path, response.Code, response.Body.String())
				}
				if slices.Contains([]string{"scan_failure", "iteration_failure", "close_failure"}, mode) && backend.closed != 1 {
					t.Fatal("failed summary leaked rows", mode, backend.closed)
				}
			}
		})
	}
	after, err := models.TicketObjects.Using(runtime).Filter(models.TicketFields.ID.In(keys...)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
	if err != nil || !reflect.DeepEqual(originals, after) {
		t.Fatal("summary changed tickets or repaired a digest", err)
	}
	for _, key := range keys {
		history, err := runtime.AuditHistory(ctx, "helpdesk.ticket", key, 10)
		if err != nil || len(history) != 0 {
			t.Fatal("summary wrote durable audit", strconv.FormatInt(key, 10), err)
		}
	}
}
