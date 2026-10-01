package uniquetest

import (
	"context"
	"errors"
	"fmt"
	"math"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
	"github.com/progresshans/godj/validation"
)

// CheckCompositeValidation compares the independent scalar equality cases
// inside one bucket, then crosses bucket, NULL, partial-update and rollback
// boundaries. Each backend test owns fresh native schema setup and cleanup.
func CheckCompositeValidation(t *testing.T, backend interface {
	db.Session
	db.Atomic
}, model ir.Model, profile Profile, database string) {
	t.Helper()
	ctx := t.Context()
	manager := orm.NewManager[Record](Descriptor{Model: model})
	count := func() int64 {
		t.Helper()
		n, err := manager.Using(backend).Count(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	check := func(t testing.TB, diagnostics validation.Errors, err error, conflict bool) {
		t.Helper()
		if err != nil || diagnostics.Empty() == conflict {
			t.Fatal("advisory result differs from independent equality/native scope", diagnostics.All(), err)
		}
		if conflict && (diagnostics.Len() != 1 || diagnostics.All()[0].Field() != validation.NonField || diagnostics.All()[0].Code() != validation.CodeUniqueTogether || len(diagnostics.All()[0].Params()) != 0) {
			t.Fatal("compound diagnostic exposed a value or blamed one member", diagnostics.All())
		}
	}
	var canonical Profile
	if database == "sqlite" && profile.Name == "json" {
		for _, candidate := range Profiles(t, database) {
			if candidate.Name == "json_canonical" {
				canonical = candidate
			}
		}
	}
	var saved int64
	deviations := 0
	for _, attempt := range profile.Attempts {
		t.Run(fmt.Sprintf("reference_%02d", attempt.Index), func(t *testing.T) {
			input := CompositeInput{Model: model, Bucket: query.Integer(1), Value: Value(t, profile.Name, attempt.Input)}
			diagnostics, err := manager.ValidateUniqueCreate(ctx, backend, input)
			value, writeErr := manager.Create(ctx, backend, input)
			if number, ok := input.Value.Float(); database == "sqlite" && ok && math.IsNaN(number) {
				// Preserve the established lossless SQLite policy: the Python
				// driver's NaN-to-SQL-NULL conversion is an explicit deviation.
				invalid := &query.Error{Category: query.CategoryField, Code: query.CodeInvalidValue}
				if !attempt.Saved || !errors.Is(err, invalid) || !errors.Is(writeErr, invalid) || !diagnostics.Empty() || value.Present {
					t.Fatal("SQLite NaN rejection policy changed", diagnostics.All(), err, writeErr)
				}
				deviations++
				return
			}
			wantSaved := attempt.Saved
			if database == "sqlite" && profile.Name == "json" {
				if len(canonical.Attempts) != len(profile.Attempts) {
					t.Fatal("canonical reference attempt inventory differs")
				}
				expected := canonical.Attempts[attempt.Index]
				if !Value(t, canonical.Name, expected.Input).Equal(input.Value) {
					t.Fatal("canonical reference has a different logical value")
				}
				wantSaved = expected.Saved
				if wantSaved != attempt.Saved {
					deviations++
				}
			}
			check(t, diagnostics, err, !wantSaved)
			if wantSaved {
				if writeErr != nil || !value.Present || value.ID <= 0 {
					t.Fatal("reference tuple rejected", value, writeErr)
				}
				saved++
			} else if !errors.Is(writeErr, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) || value.Present {
				t.Fatal("reference tuple conflict lost its native boundary", value, writeErr)
			}
		})
	}
	if t.Failed() {
		return
	}
	wantDeviations := 0
	if database == "sqlite" && profile.Name == "float" {
		wantDeviations = 2
	} else if database == "sqlite" && profile.Name == "json" {
		wantDeviations = 1
	}
	if deviations != wantDeviations {
		t.Fatal("composite equality policy deviation inventory changed", deviations, wantDeviations)
	}
	if actual := count(); actual != saved {
		t.Fatal("advisory or rejected writes changed storage", actual, saved)
	}
	sample := CompositeSample(t, profile)
	other := CompositeInput{Model: model, Bucket: query.Integer(2), Value: sample}
	diagnostics, err := manager.ValidateUniqueCreate(ctx, backend, other)
	check(t, diagnostics, err, false)
	current, err := manager.Create(ctx, backend, other)
	if err != nil {
		t.Fatal("different bucket", err)
	}
	self := CompositeInput{Model: model, Value: sample, PatchValue: true}
	diagnostics, err = manager.ValidateUniqueUpdate(ctx, backend, current, self)
	check(t, diagnostics, err, false)
	if _, err := manager.Update(ctx, backend, current, self); err != nil {
		t.Fatal("self update", err)
	}
	conflict := CompositeInput{Model: model, Bucket: query.Integer(1), PatchBucket: true}
	diagnostics, err = manager.ValidateUniqueUpdate(ctx, backend, current, conflict)
	check(t, diagnostics, err, true)
	if _, err := manager.Update(ctx, backend, current, conflict); !errors.Is(err, &query.Error{Category: query.CategoryIntegrity, Code: query.CodeUniqueConstraint}) {
		t.Fatal("partial update ignored retained value", err)
	}
	for _, pair := range [][2]query.Value{{query.Null(), sample}, {query.Integer(1), query.Null()}, {query.Null(), query.Null()}} {
		for range 2 {
			input := CompositeInput{Model: model, Bucket: pair[0], Value: pair[1]}
			diagnostics, err := manager.ValidateUniqueCreate(ctx, backend, input)
			check(t, diagnostics, err, false)
			if _, err := manager.Create(ctx, backend, input); err != nil {
				t.Fatal("SQL NULL tuple", err)
			}
		}
	}
	before := count()
	rejected := errors.New("confirmed duplicate")
	err = backend.Atomic(ctx, func(session db.Session) error {
		if _, err := manager.Create(ctx, session, CompositeInput{Model: model, Bucket: query.Integer(9), Value: sample}); err != nil {
			return err
		}
		diagnostics, err := manager.ValidateUniqueCreate(ctx, session, CompositeInput{Model: model, Bucket: query.Integer(1), Value: sample})
		check(t, diagnostics, err, true)
		return rejected
	})
	if !errors.Is(err, rejected) || count() != before {
		t.Fatal("advisory rejection did not roll back earlier writes", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	diagnostics, err = manager.ValidateUniqueUpdate(canceled, backend, current, conflict)
	if !errors.Is(err, context.Canceled) || !diagnostics.Empty() || count() != before {
		t.Fatal("cancellation became a duplicate diagnostic or changed storage", diagnostics.All(), err)
	}
}
