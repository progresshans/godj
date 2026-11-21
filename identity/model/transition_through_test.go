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
	pair := ThroughEndpoints{source: ir.Field{Name: "owner"}, target: ir.Field{Name: "target"}, sourceReference: TransitionTargetReference{Kind: TransitionReferenceAccount, Source: SourceAccount{ID: -5, Revision: 2}}, targetReference: TransitionTargetReference{Kind: TransitionReferenceGroup, Source: SourceAccount{ID: 0, Revision: 3}}, sourceReferenced: true, targetReferenced: true}
	refs := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{-5: 2}, targets: map[int64]int64{-5: 0}}, TransitionReferenceGroup: {revisions: map[int64]int64{0: 3}, targets: map[int64]int64{0: -9}}}
	for _, mode := range []string{"complete", "missing_account", "missing_group", "stale_group", "missing_revision", "invalid_revision", "unknown_kind", "wrong_present_key", "wrong_source_key", "missing_unreferenced", "hidden_reference"} {
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
			case "missing_revision":
				delete(owned[TransitionReferenceAccount].revisions, -5)
			case "invalid_revision":
				current.sourceReference.Source.Revision = 0
			case "unknown_kind":
				current.sourceReference.Kind = "unknown"
			case "wrong_present_key":
				current.targetPresent, current.targetKey = true, 0
			case "wrong_source_key":
				current.sourcePresent, current.sourceKey = true, -1
			case "missing_unreferenced":
				current.sourceReference = TransitionTargetReference{}
				current.sourceReferenced = false
			case "hidden_reference":
				current.sourcePresent, current.sourceKey, current.sourceReferenced = true, 0, false
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
			for _, mutate := range []func(*ThroughEndpoints){func(p *ThroughEndpoints) { p.targetPresent = false }, func(p *ThroughEndpoints) { p.source.Column = "changed" }, func(p *ThroughEndpoints) { p.targetReference.Source.Revision++ }, func(p *ThroughEndpoints) { p.sourceReferenced = false }, func(p *ThroughEndpoints) { p.targetReferenced = false }} {
				changed := actual
				mutate(&changed)
				if pair.accepts(changed) {
					t.Fatal("endpoint binding ignored a changed field or reference")
				}
			}
		})
	}
	if reference, present := pair.SourceReference(); !present || reference.Kind != TransitionReferenceAccount || reference.Source != (SourceAccount{ID: -5, Revision: 2}) {
		t.Fatal("account reference was confused with absence")
	}
	if reference, present := pair.TargetReference(); !present || reference.Kind != TransitionReferenceGroup || reference.Source != (SourceAccount{ID: 0, Revision: 3}) {
		t.Fatal("zero source key was confused with absence")
	}
	// The account namespace may occupy either endpoint. Reverse orientation
	// retains its explicit presence and resolves the native zero target key.
	reversed := pair
	reversed.source, reversed.target = pair.target, pair.source
	reversed.sourceReference, reversed.targetReference = pair.targetReference, pair.sourceReference
	actual, err := resolveTransitionThroughPair(reversed, refs)
	if err != nil || actual.sourceKey != -9 || actual.targetKey != 0 {
		t.Fatal("account target reference did not resolve", err)
	}
	if reference, present := actual.TargetReference(); !present || reference.Kind != TransitionReferenceAccount {
		t.Fatal("account target reference lost presence")
	}
	zeroSource := pair
	zeroSource.sourceReference.Source.ID = 0
	zeroMappings := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{0: 2}, targets: map[int64]int64{0: 0}}, TransitionReferenceGroup: refs[TransitionReferenceGroup]}
	if actual, err := resolveTransitionThroughPair(zeroSource, zeroMappings); err != nil || !actual.sourcePresent || actual.sourceKey != 0 {
		t.Fatal("zero account source and native key lost their independent presence", err)
	}
	if reference, present := zeroSource.SourceReference(); !present || reference.Source.ID != 0 || reference.Source.Revision != 2 {
		t.Fatal("zero account correspondence was not exposed")
	}
	ordinary := ThroughEndpoints{source: pair.source, target: pair.target, sourcePresent: true, targetPresent: true, sourceKey: 0, targetKey: -2}
	if _, present := ordinary.SourceReference(); present {
		t.Fatal("ordinary source acquired a correspondence")
	}
	if _, present := ordinary.TargetReference(); present {
		t.Fatal("ordinary target acquired a correspondence")
	}
	if actual, err := resolveTransitionThroughPair(ordinary, nil); err != nil || !ordinary.accepts(actual) {
		t.Fatal("ordinary signed endpoints required a transition reference", err)
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
	pending := &transitionThroughPreparation{ordered: []transitionThroughIntent{{policy: &throughCreationState{model: ir.ModelIdentity{AppLabel: "a", ModelName: "account"}, declaration: strings.Repeat("a", 64)}, kind: "account", pair: ThroughEndpoints{source: ir.Field{Name: "owner"}, target: ir.Field{Name: "target"}}, draft: &throughDraft{values: func() map[string]query.Value { return maps.Clone(values) }, encode: func(context.Context) error { calls++; values["password"] = query.String("encoded-secret"); return nil }}}}}
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
	pending.ordered[0].pair.sourceReferenced = true
	third, err := pending.seal(strings.Repeat("b", 64), strings.Repeat("c", 64))
	if err != nil || second == third {
		t.Fatal("endpoint reference presence was not bound")
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

func TestTransitionCollectionsUseExplicitDurableKinds(t *testing.T) {
	role := ir.AccountRole{Model: ir.ModelIdentity{AppLabel: "a", ModelName: "member"}, Catalog: ir.AccountCatalog{Group: ir.ModelIdentity{AppLabel: "a", ModelName: "team"}, Permission: ir.ModelIdentity{AppLabel: "a", ModelName: "grant"}}}
	for _, item := range []struct {
		model ir.ModelIdentity
		kind  string
	}{{role.Model, "account"}, {role.Catalog.Group, "group"}, {role.Catalog.Permission, "permission"}} {
		t.Run(item.kind, func(t *testing.T) {
			kind, err := transitionCollectionKind(role, item.model)
			if err != nil || kind != item.kind {
				t.Fatal("collection model lost its explicit durable kind", kind, err)
			}
			pending := &transitionThroughPreparation{ordered: []transitionThroughIntent{{kind: kind, policy: &throughCreationState{declaration: strings.Repeat("a", 64)}}}}
			manifest := TransitionManifest{Version: 8, OperationID: strings.Repeat("b", 64), PriorOperationID: strings.Repeat("c", 64), PriorSequence: 1,
				ProjectSHA256: strings.Repeat("d", 64), SourceRoleSHA256: strings.Repeat("e", 64), TargetRoleSHA256: strings.Repeat("f", 64), StateSHA256: strings.Repeat("1", 64),
				CreationSources: []SourceAccount{{ID: 0, Revision: 1}}, Collections: pending.manifest()}
			if manifest.Collections[0].Kind != item.kind {
				t.Fatal("manifest changed the durable kind")
			}
			if _, err := manifest.evidence(); err != nil {
				t.Fatal("valid collection manifest rejected", err)
			}
			for _, invalid := range []string{"", "unknown"} {
				manifest.Collections[0].Kind = invalid
				if _, err := manifest.evidence(); err == nil {
					t.Fatal("invalid durable kind accepted", invalid)
				}
			}
		})
	}
	if _, err := transitionCollectionKind(role, ir.ModelIdentity{AppLabel: "a", ModelName: "other"}); err == nil {
		t.Fatal("unowned model acquired a collection kind")
	}
}
