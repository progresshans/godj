package model

import (
	"context"
	"errors"
	"maps"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func TestTransitionThroughEndpointsRequireRealKeysInBothNamespaces(t *testing.T) {
	pair := ThroughEndpoints{source: ir.Field{Name: "owner"}, target: ir.Field{Name: "target"}, sourceReference: TransitionTargetReference{Kind: TransitionReferenceAccount, Source: SourceAccount{ID: -5, Revision: 2}}, targetReference: TransitionTargetReference{Kind: TransitionReferenceGroup, Source: SourceAccount{ID: 0, Revision: 3}}}
	refs := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{-5: 2}, targets: map[int64]int64{-5: 0}}, TransitionReferenceGroup: {revisions: map[int64]int64{0: 3}, targets: map[int64]int64{0: -9}}}
	for _, mode := range []string{"complete", "missing_account", "missing_group", "stale_group", "wrong_present_key", "missing_unreferenced"} {
		t.Run(mode, func(t *testing.T) {
			current := pair
			owned := make(transitionTargetMappings, len(refs))
			for kind, namespace := range refs {
				owned[kind] = transitionTargetNamespace{revisions: maps.Clone(namespace.revisions), targets: maps.Clone(namespace.targets)}
			}
			switch mode {
			case "missing_account":
				delete(owned[TransitionReferenceAccount].targets, -5)
			case "missing_group":
				delete(owned[TransitionReferenceGroup].targets, 0)
			case "stale_group":
				owned[TransitionReferenceGroup].revisions[0] = 4
			case "wrong_present_key":
				current.targetPresent, current.targetKey = true, 0
			case "missing_unreferenced":
				current.sourceReference = TransitionTargetReference{}
			}
			actual, err := resolveTransitionThroughPair(current, owned)
			if mode != "complete" {
				if err == nil || actual.sourcePresent || actual.targetPresent {
					t.Fatal("incomplete correspondence published endpoint keys", err)
				}
				return
			}
			if err != nil || !pair.accepts(actual) {
				t.Fatal("real native endpoints did not bind", err)
			}
			left, leftPresent := actual.SourceKey()
			right, rightPresent := actual.TargetKey()
			if !leftPresent || !rightPresent || left != 0 || right != -9 {
				t.Fatal("signed keys lost presence")
			}
			for _, mutate := range []func(*ThroughEndpoints){func(p *ThroughEndpoints) { p.targetPresent = false }, func(p *ThroughEndpoints) { p.source.Column = "changed" }, func(p *ThroughEndpoints) { p.targetReference.Source.Revision++ }} {
				changed := actual
				mutate(&changed)
				if pair.accepts(changed) {
					t.Fatal("endpoint binding ignored a changed field or reference")
				}
			}
		})
	}
	if _, present := pair.SourceKey(); present {
		t.Fatal("draft exposed a guessed source")
	}
	if _, present := pair.TargetKey(); present {
		t.Fatal("draft exposed a guessed target")
	}
	values, pending := pair.assignments()
	if len(values) != 0 || !pending["owner"] || !pending["target"] {
		t.Fatal("pending fields became sentinel values")
	}
}

func TestTransitionThroughPreparationKeepsAuthoritySeparateFromTargetDomain(t *testing.T) {
	authority, err := auth.NewCredentialDomain(strings.Repeat("1", 64))
	if err != nil {
		t.Fatal(err)
	}
	target, err := auth.NewCredentialDomain(strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}
	principal, err := auth.NewPrincipal(auth.PrincipalConfig{ID: "source", Active: true})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.NewCredentialForDomain(authority, "source", "!", principal)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := auth.NewCredentialForDomain(target, "source", "!", principal)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	scope := &allocationTestScope{}
	pending := &transitionThroughPreparation{authority: authority, domain: target, actorID: "source", ordered: []transitionThroughIntent{{draft: &throughDraft{check: func(context.Context, db.Queryer, auth.Principal, bool) error { calls++; return nil }}}}}
	if err := pending.check(t.Context(), scope, credential.Principal()); err != nil || calls != 1 {
		t.Fatal("source authority was confused with target credentials", err)
	}
	if err := pending.check(t.Context(), scope, foreign.Principal()); err == nil || calls != 1 {
		t.Fatal("new target identity gained source authority")
	}
	bound := &throughPreparation{authority: authority, domain: target, actorID: "source"}
	if err := bound.check(t.Context(), scope, credential.Principal(), orm.CollectionChangeEffects{}, true); err != nil {
		t.Fatal(err)
	}
	if err := bound.check(t.Context(), scope, foreign.Principal(), orm.CollectionChangeEffects{}, true); err == nil {
		t.Fatal("bound candidate authority switched domains")
	}
	scope.failure = errors.New("expired transition scope")
	if err := pending.check(t.Context(), scope, credential.Principal()); !errors.Is(err, scope.failure) {
		t.Fatal("expired preparation scope passed", err)
	}
}

func TestTransitionThroughSealBindsEncodedValuesAndIntentWithoutRerunningFactory(t *testing.T) {
	values := map[string]query.Value{"id": query.Null(), "principal": query.String("owned"), "password": query.String("!")}
	calls := 0
	pending := &transitionThroughPreparation{ordered: []transitionThroughIntent{{policy: &throughCreationState{model: ir.ModelIdentity{AppLabel: "a", ModelName: "account"}, declaration: strings.Repeat("a", 64)}, kind: TransitionReferenceAccount, pair: ThroughEndpoints{source: ir.Field{Name: "owner"}, target: ir.Field{Name: "target"}}, draft: &throughDraft{values: func() map[string]query.Value { return maps.Clone(values) }, encode: func(context.Context) error { calls++; values["password"] = query.String("encoded-secret"); return nil }}}}}
	if _, err := pending.seal(strings.Repeat("b", 64), strings.Repeat("c", 64)); err == nil {
		t.Fatal("unencoded draft acquired durable evidence")
	}
	if err := pending.encode(t.Context()); err != nil {
		t.Fatal(err)
	}
	first, err := pending.seal(strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err != nil || calls != 1 {
		t.Fatal(err)
	}
	values["principal"] = query.String("changed")
	second, err := pending.seal(strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err != nil || first == second {
		t.Fatal("draft identity not bound")
	}
	if err := pending.encode(t.Context()); err == nil || calls != 1 {
		t.Fatal("password work repeated")
	}
	pending.ordered[0].draft.encode = func(context.Context) error { return errors.New("hash failure") }
	pending.encoded = false
	if err := pending.encode(t.Context()); err == nil || pending.encoded {
		t.Fatal("failed encoding became complete")
	}
}
