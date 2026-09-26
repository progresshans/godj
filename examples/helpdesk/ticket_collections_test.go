package helpdesk_test

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http/httptest"
	"os"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/systemstate"
)

type collectionTicketJSON struct {
	ID      int64
	Subject string
	Labels  []int64
}

type collectionReferenceSnapshot struct {
	PositiveID bool `json:"positive_id"`
	Subject    string
	Labels     []int64
	Retained   map[string][]bool
}

type collectionObservation struct {
	Mode             string
	Input            map[string]json.RawMessage
	Valid            bool
	Errors           map[string]json.RawMessage
	Result           collectionReferenceSnapshot
	Original         collectionReferenceSnapshot
	TicketCountDelta int64 `json:"ticket_count_delta"`
}

func collectionLinks(t *testing.T, ctx context.Context, backend *systemstate.Runtime, ticketID int64) map[int64]int64 {
	t.Helper()
	relations, err := project.BindRelations()
	if err != nil {
		t.Fatal(err)
	}
	links, err := models.TicketLabelObjects.Using(backend).Filter(relations.ModelsTicketLabel.Ticket.ID.Exact(ticketID)).OrderBy(models.TicketLabelFields.ID.Asc()).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result := make(map[int64]int64, len(links))
	for _, link := range links {
		if _, duplicate := result[link.LabelID]; duplicate {
			t.Fatal("duplicate stored membership")
		}
		result[link.LabelID] = link.ID
	}
	return result
}

func decodeCollectionTicket(t *testing.T, response *httptest.ResponseRecorder, status int) collectionTicketJSON {
	t.Helper()
	var value collectionTicketJSON
	if response.Code != status || json.Unmarshal(response.Body.Bytes(), &value) != nil || value.ID <= 0 || value.Labels == nil {
		t.Fatalf("collection response %d: %s", response.Code, response.Body)
	}
	return value
}

func verifyHelpdeskTicketCollections(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), client *helpdeskClient, categoryID, otherCategoryID int64, policy systemstate.CredentialPolicy, seedLargeLabel func(context.Context, db.Mutator, int64) (int64, error)) {
	t.Helper()
	var reference struct {
		Observations []collectionObservation
		Rollback     collectionReferenceSnapshot
	}
	data, err := os.ReadFile("testdata/ticket-collection-django61-drf318-sqlite.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &reference); err != nil || len(reference.Observations) != 48 {
		t.Fatal("incomplete independent collection reference", err)
	}
	var postgres struct {
		Observations []collectionObservation
		Rollback     collectionReferenceSnapshot
	}
	data, err = os.ReadFile("testdata/ticket-collection-django61-drf318-postgres.json")
	if err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal(data, &postgres); err != nil || !reflect.DeepEqual(reference, postgres) {
		t.Fatal("cross-backend reference behavior changed", err)
	}
	keys := map[int64]int64{999: 9223372036854775806}
	for _, key := range []int64{7, 9, 11, 12} {
		category := categoryID
		if key == 12 {
			category = otherCategoryID
		}
		name := fmt.Sprintf("Collection reference %d", key)
		if key == 7 {
			name = "<script>Collection seven</script>"
		}
		label, err := models.LabelObjects.Create(ctx, runtime, models.NewLabelCreate(name, category))
		if err != nil {
			t.Fatal(err)
		}
		keys[key] = label.ID
	}
	const large = int64(1 << 60)
	largeID, err := seedLargeLabel(ctx, runtime, categoryID)
	if err != nil || largeID != large {
		t.Fatal("large native relation key", largeID, err)
	}
	keys[large] = large
	mapKeys := func(input []int64) []int64 {
		output := make([]int64, len(input))
		for index, key := range input {
			if mapped, ok := keys[key]; ok {
				output[index] = mapped
			} else {
				output[index] = key
			}
		}
		return output
	}
	for index, observation := range reference.Observations {
		t.Run(fmt.Sprintf("%s/%02d", observation.Mode, index), func(t *testing.T) {
			original, err := models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate("Before", categoryID))
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range []int64{keys[7], keys[9]} {
				if _, err := models.TicketLabelObjects.Create(ctx, runtime, models.NewTicketLabelCreate(original.ID, key)); err != nil {
					t.Fatal(err)
				}
			}
			old := collectionLinks(t, ctx, runtime, original.ID)
			input := maps.Clone(observation.Input)
			if raw, present := input["labels"]; present && string(raw) != "null" {
				var values []json.RawMessage
				if json.Unmarshal(raw, &values) == nil {
					for i, item := range values {
						key, err := strconv.ParseInt(string(item), 10, 64)
						if err == nil {
							if mapped, ok := keys[key]; ok {
								values[i] = json.RawMessage(strconv.FormatInt(mapped, 10))
							}
						}
					}
					input["labels"], err = json.Marshal(values)
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			body, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			beforeCount, err := models.TicketObjects.Using(runtime).Count(ctx)
			if err != nil {
				t.Fatal(err)
			}
			method, path := "PATCH", fmt.Sprintf("/api/tickets/%d/", original.ID)
			if observation.Mode == "put" {
				method = "PUT"
			} else if observation.Mode == "create" {
				method, path = "POST", "/api/tickets/"
			}
			response := client.request(method, path, string(body), true)
			var createdID int64
			if observation.Valid {
				status := 200
				if observation.Mode == "create" {
					status = 201
				}
				result := decodeCollectionTicket(t, response, status)
				if result.Subject != observation.Result.Subject || !slices.Equal(result.Labels, mapKeys(observation.Result.Labels)) {
					t.Fatal("reference result mismatch", result, observation.Result)
				}
				stored := readUniqueTicket(t, ctx, runtime, result.ID)
				if stored.Subject != result.Subject {
					t.Fatal("response precedes durable row")
				}
				links := collectionLinks(t, ctx, runtime, result.ID)
				if len(links) != len(result.Labels) {
					t.Fatal("response membership differs from storage")
				}
				for _, key := range result.Labels {
					if links[key] == 0 {
						t.Fatal("reported link missing")
					}
				}
				if observation.Mode == "create" {
					createdID = result.ID
				} else {
					for raw, retained := range observation.Result.Retained {
						key, _ := strconv.ParseInt(raw, 10, 64)
						if len(retained) != 2 || !retained[0] || !retained[1] || links[keys[key]] != old[keys[key]] {
							t.Fatal("retained through identity lost")
						}
					}
				}
			} else {
				var failure struct {
					Code   string
					Errors []struct{ Field string }
				}
				if response.Code != 400 || json.Unmarshal(response.Body.Bytes(), &failure) != nil || failure.Code != "validation_error" {
					t.Fatal("reference rejection mismatch", response.Code, response.Body)
				}
				fields := map[string]bool{}
				for _, item := range failure.Errors {
					fields[item.Field] = true
				}
				if len(fields) != len(observation.Errors) {
					t.Fatal("rejection field coverage", fields, observation.Errors)
				}
				for field := range observation.Errors {
					if !fields[field] {
						t.Fatal("reference rejected field missing", field)
					}
				}
			}
			current := readUniqueTicket(t, ctx, runtime, original.ID)
			links := collectionLinks(t, ctx, runtime, original.ID)
			actualKeys := make([]int64, 0, len(links))
			for key := range links {
				actualKeys = append(actualKeys, key)
			}
			slices.Sort(actualKeys)
			if current.Subject != observation.Original.Subject || !slices.Equal(actualKeys, mapKeys(observation.Original.Labels)) {
				t.Fatal("rejection or update changed unrelated original state", current.Subject, actualKeys)
			}
			afterCount, err := models.TicketObjects.Using(runtime).Count(ctx)
			if err != nil || afterCount-beforeCount != observation.TicketCountDelta {
				t.Fatal("create atomicity", afterCount-beforeCount, err)
			}
			for _, id := range []int64{createdID, original.ID} {
				if id != 0 {
					if r := client.request("DELETE", fmt.Sprintf("/api/tickets/%d/", id), "", true); r.Code != 204 {
						t.Fatal("collection fixture cleanup", r.Code, r.Body)
					}
				}
			}
		})
	}
	verifyTicketCollectionBoundaries(t, ctx, runtime, open, client, categoryID, otherCategoryID, keys, policy)
}

// SQLite owns explicit integer key assignment and sequence reconciliation.
// PostgreSQL provisions its owned sequence separately, then uses normal Create.
func insertLargeCollectionLabel(ctx context.Context, backend db.Mutator, categoryID int64) (int64, error) {
	const large = int64(1 << 60)
	idField := query.NewFieldRef("id", "id", query.FieldInteger, false)
	return backend.Insert(ctx, query.NewInsertPlanReturningKey("helpdesk_label", []query.Assignment{
		query.NewAssignment(idField, query.Integer(large)), query.NewAssignment(query.NewFieldRef("name", "name", query.FieldString, false), query.String("Collection large key")),
		query.NewAssignment(query.NewFieldRef("category", "category_id", query.FieldInteger, false), query.Integer(categoryID)),
	}, idField))
}
