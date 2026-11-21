package consumer

import (
	"context"
	"errors"
	"reflect"
	"strconv"
	"sync/atomic"
	"testing"

	"example.com/godj-project-bundle/labels"
	"example.com/godj-project-bundle/owners"
	"example.com/godj-project-bundle/project"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
)

// This module is generated from two independent apps. In particular, its
// explicit through model has nullable endpoints and a required unique payload.
func TestCollectionFormSavers(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, b collectionBackend, _ func() (collectionBackend, error), _ func(string) error, _ bool) {
		t.Cleanup(func() { check(t, b.Close()) })
		ctx := t.Context()
		migrateCollections(t, b)
		relations, err := project.BindCollections()
		check(t, err)
		var targets []labels.Label
		for _, name := range []string{"a", "b", "c"} {
			value, err := labels.LabelObjects.Create(ctx, b, labels.NewLabelCreate(name))
			check(t, err)
			targets = append(targets, value)
		}
		newOwner := func(t *testing.T) owners.Owner {
			t.Helper()
			value, err := owners.OwnerObjects.Create(ctx, b, owners.NewOwnerCreate(t.Name()))
			check(t, err)
			return value
		}
		prepare := func(t *testing.T, owner owners.Owner, field, name string, keys ...int64) (formmodel.PreparedInstance[owners.Owner], owners.Owner) {
			t.Helper()
			metadata, err := owners.OwnerObjects.Metadata()
			check(t, err)
			spec, err := formmodel.NewSpecForFields(metadata, []string{"name", field})
			check(t, err)
			var choices []forms.Choice
			var input []string
			for _, key := range keys {
				text := strconv.FormatInt(key, 10)
				choices = append(choices, forms.Choice{Value: forms.Integer(key), Label: text})
				input = append(input, text)
			}
			spec, err = spec.WithModelChoices(field, choices...)
			check(t, err)
			bound, err := formmodel.BindInstance(t.Context(), owners.OwnerObjects, spec, forms.NewData(map[string][]string{"name": {name}, field: input}), &owner, formmodel.PostClean{})
			check(t, err)
			prepared, err := bound.Prepare()
			check(t, err)
			candidate, err := prepared.Model()
			check(t, err)
			return prepared, candidate
		}
		t.Run("automatic_retained_and_option_copy", func(t *testing.T) {
			owner := newOwner(t)
			prepared, candidate := prepare(t, owner, "labels", "automatic", targets[0].ID)
			saver, err := formmodel.SaveManyToMany(relations.OwnersOwnerLabels)
			check(t, err)
			check(t, prepared.Save(ctx, b, &candidate, saver))
			before, err := owners.OwnerLabelsLinkObjects.Using(b).All(ctx)
			check(t, err)
			if len(before) != 1 || before[0].SourceID != owner.ID || before[0].TargetID != targets[0].ID {
				t.Fatal("automatic link not saved", before)
			}
			check(t, prepared.Save(ctx, b, &candidate, saver))
			retained, err := owners.OwnerLabelsLinkObjects.Using(b).All(ctx)
			check(t, err)
			if !reflect.DeepEqual(before, retained) {
				t.Fatal("default saver replaced retained link", retained)
			}
			options := []orm.ManyToManySetOptions[owners.OwnerLabelsLink]{{Clear: true}}
			replace, err := formmodel.SaveManyToMany(relations.OwnersOwnerLabels, options...)
			check(t, err)
			options[0].Clear = false
			check(t, prepared.SaveCollections(ctx, b, candidate, replace))
			after, err := owners.OwnerLabelsLinkObjects.Using(b).All(ctx)
			check(t, err)
			if len(after) != 1 || after[0].ID == before[0].ID || after[0].TargetID != targets[0].ID {
				t.Fatal("clear option lost its construction snapshot", after)
			}
		})
		t.Run("through_defaults_and_failure", func(t *testing.T) {
			owner := newOwner(t)
			prepared, candidate := prepare(t, owner, "ranked", "payload", targets[0].ID)
			options := []orm.ManyToManySetOptions[owners.RankedLink]{{ThroughDefaults: owners.RankedLinkCreate{}.WithAmount(117)}}
			saver, err := formmodel.SaveManyToMany(relations.OwnersOwnerRanked, options...)
			check(t, err)
			options[0].ThroughDefaults = owners.RankedLinkCreate{}.WithAmount(118)
			check(t, prepared.Save(ctx, b, &candidate, saver))
			before, err := owners.RankedLinkObjects.Using(b).All(ctx)
			check(t, err)
			if len(before) != 1 || before[0].Amount != 117 || before[0].OwnerID == nil || *before[0].OwnerID != owner.ID || before[0].LabelID == nil || *before[0].LabelID != targets[0].ID {
				t.Fatal("through input or nullable endpoints changed", before)
			}
			withoutDefaults, err := formmodel.SaveManyToMany(relations.OwnersOwnerRanked)
			check(t, err)
			check(t, prepared.SaveCollections(ctx, b, candidate, withoutDefaults))
			failing, changed := prepare(t, candidate, "ranked", "scalar-survives", targets[1].ID, targets[2].ID)
			if err := failing.Save(ctx, b, &changed, saver); err == nil {
				t.Fatal("duplicate required through payload was accepted")
			}
			after, err := owners.RankedLinkObjects.Using(b).All(ctx)
			check(t, err)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("failed root relation write lost retained payload", after)
			}
			storedRows, err := owners.OwnerObjects.Using(b).Filter(owners.OwnerFields.ID.Exact(owner.ID)).All(ctx)
			check(t, err)
			if len(storedRows) != 1 {
				t.Fatal("stored owner missing", storedRows)
			}
			stored := storedRows[0]
			if stored.Name != "scalar-survives" {
				t.Fatal("saver invented an outer transaction", stored.Name)
			}
			redirect, err := formmodel.SaveManyToMany(relations.OwnersOwnerRanked, orm.ManyToManySetOptions[owners.RankedLink]{ThroughDefaults: owners.RankedLinkCreate{}.WithOwnerID(owner.ID + 1000).WithAmount(119)})
			check(t, err)
			if err := failing.SaveCollections(ctx, b, changed, redirect); err == nil {
				t.Fatal("through payload redirected the owner")
			}
			after, err = owners.RankedLinkObjects.Using(b).All(ctx)
			check(t, err)
			if !reflect.DeepEqual(before, after) {
				t.Fatal("rejected endpoint rewrite mutated storage", after)
			}
		})
		t.Run("self_direction_and_reverse_refusal", func(t *testing.T) {
			owner, other := newOwner(t), newOwner(t)
			prepared, candidate := prepare(t, owner, "friends", "symmetric", other.ID)
			friends, err := formmodel.SaveManyToMany(relations.OwnersOwnerFriends)
			check(t, err)
			check(t, prepared.Save(ctx, b, &candidate, friends))
			reverse, err := relations.OwnersOwnerFriends.From(b, other)
			check(t, err)
			values, err := reverse.All(ctx)
			check(t, err)
			if len(values) != 1 || values[0].ID != owner.ID {
				t.Fatal("symmetric reverse link missing", values)
			}
			prepared, candidate = prepare(t, owner, "follows", "directed", other.ID)
			follows, err := formmodel.SaveManyToMany(relations.OwnersOwnerFollows)
			check(t, err)
			check(t, prepared.Save(ctx, b, &candidate, follows))
			outgoing, err := relations.OwnersOwnerFollows.From(b, other)
			check(t, err)
			values, err = outgoing.All(ctx)
			check(t, err)
			if len(values) != 0 {
				t.Fatal("directed relation was mirrored", values)
			}
			if _, err := formmodel.SaveManyToMany(relations.OwnersOwnerFollowers); err == nil {
				t.Fatal("same typed owner made a reverse accessor a form field")
			}
		})
		t.Run("borrowed_lifetime_and_rollback", func(t *testing.T) {
			owner := newOwner(t)
			prepared, candidate := prepare(t, owner, "labels", "rolled-back", targets[1].ID)
			saver, err := formmodel.SaveManyToMany(relations.OwnersOwnerLabels)
			check(t, err)
			rollback := errors.New("abort outer write")
			var borrowed db.RelationSession
			var nested, queries atomic.Int64
			err = b.AtomicRelation(ctx, func(session db.RelationSession) error {
				borrowed = tracedSession{RelationSession: session, nested: &nested, queries: &queries}
				if err := prepared.Save(ctx, borrowed, &candidate, saver); err != nil {
					return err
				}
				inside, err := relations.OwnersOwnerLabels.InSession(borrowed, candidate)
				check(t, err)
				values, err := inside.All(ctx)
				check(t, err)
				if len(values) != 1 || values[0].ID != targets[1].ID {
					t.Fatal("saver missed borrowed writes", values)
				}
				return rollback
			})
			if !errors.Is(err, rollback) || nested.Load() != 0 {
				t.Fatal("saver lost scope or terminal result", err, nested.Load())
			}
			storedRows, err := owners.OwnerObjects.Using(b).Filter(owners.OwnerFields.ID.Exact(owner.ID)).All(ctx)
			check(t, err)
			if len(storedRows) != 1 {
				t.Fatal("stored owner missing", storedRows)
			}
			stored := storedRows[0]
			links, err := relations.OwnersOwnerLabels.From(b, stored)
			check(t, err)
			values, err := links.All(ctx)
			check(t, err)
			if stored.Name != owner.Name || len(values) != 0 {
				t.Fatal("borrowed save escaped rollback", stored, values)
			}
			before := queries.Load()
			if err := saver.Save(ctx, borrowed, candidate, []int64{targets[0].ID}); err == nil || queries.Load() != before {
				t.Fatal("expired saver reached transaction I/O", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if err := saver.Save(canceled, b, candidate, []int64{targets[0].ID}); !errors.Is(err, context.Canceled) {
				t.Fatal("cancellation lost", err)
			}
			check(t, prepared.Save(ctx, b, &candidate, saver))
			fresh, err := relations.OwnersOwnerLabels.From(b, candidate)
			check(t, err)
			values, err = fresh.All(ctx)
			check(t, err)
			if len(values) != 1 || values[0].ID != targets[1].ID {
				t.Fatal("failed use poisoned reusable saver", values)
			}
		})
	})
}
