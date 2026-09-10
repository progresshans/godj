package validation_test

import (
	"sync"
	"testing"

	"github.com/progresshans/godj/validation"
)

func TestErrorsAreOrderedAndDetached(t *testing.T) {
	params := []validation.Param{validation.NewParam("limit", "8")}
	items := []validation.Violation{
		validation.New("title", "required"),
		validation.New("title", "max_length", params...),
		validation.New(validation.NonField, "conflict"),
	}
	errors := validation.NewErrors(items...)

	params[0] = validation.NewParam("changed", "true")
	items[0] = validation.New("other", "changed")
	all := errors.All()
	all[1] = validation.New("other", "changed")
	returnedParams := all[0].Params()
	returnedParams = append(returnedParams, validation.NewParam("mutated", "true"))

	if errors.Len() != 3 || errors.Empty() {
		t.Fatalf("errors length = %d, empty = %v", errors.Len(), errors.Empty())
	}
	first, ok := errors.At(0)
	if !ok || first.Field() != "title" || first.Code() != "required" {
		t.Fatalf("first = (%q, %q, %v)", first.Field(), first.Code(), ok)
	}
	second, _ := errors.At(1)
	if got := second.Params(); len(got) != 1 || got[0].Key() != "limit" || got[0].Value() != "8" {
		t.Fatalf("second params = %#v", got)
	}
	if _, ok := errors.At(-1); ok {
		t.Fatal("At(-1) succeeded")
	}
	if _, ok := errors.At(3); ok {
		t.Fatal("At(len) succeeded")
	}
}

func TestJoinPreservesSharedGroupsAndParameterSnapshots(t *testing.T) {
	first := validation.NewErrors(validation.New("title", "max_length", validation.NewParam("limit", "8")))
	last := validation.NewErrors(validation.New(validation.NonField, "conflict"))
	groups := []validation.Errors{{}, first, {}, last}
	joined := validation.Join(groups...)
	groups[1] = last
	if !validation.Join().Empty() || validation.Join(validation.Errors{}, first).Len() != 1 {
		t.Fatal("empty groups changed the result")
	}
	var workers sync.WaitGroup
	for range 16 {
		workers.Go(func() {
			for _, collection := range []validation.Errors{first, validation.Join(first), joined.ByField("title")} {
				items := collection.All()
				params := items[0].Params()
				params[0] = validation.NewParam("mutated", "true")
				items[0] = validation.New("other", "changed")
			}
			items := joined.All()
			if len(items) != 2 || items[0].Field() != "title" || items[1].Field() != validation.NonField ||
				items[0].Params()[0].Value() != "8" {
				t.Errorf("joined errors changed: %#v", items)
			}
		})
	}
	workers.Wait()
}

func TestByFieldAndAppendPreserveSourceCollections(t *testing.T) {
	left := validation.NewErrors(
		validation.New("title", "required"),
		validation.New("published", "invalid"),
	)
	right := validation.NewErrors(validation.New("title", "max_length"))
	combined := left.Append(right)

	if left.Len() != 2 || right.Len() != 1 || combined.Len() != 3 {
		t.Fatalf("lengths = %d, %d, %d", left.Len(), right.Len(), combined.Len())
	}
	title := combined.ByField("title").All()
	if len(title) != 2 || title[0].Code() != "required" || title[1].Code() != "max_length" {
		t.Fatalf("title errors = %#v", title)
	}
	if !validation.NewErrors().Empty() {
		t.Fatal("zero errors is not empty")
	}
}
