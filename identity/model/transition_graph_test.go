package model

import (
	"context"
	"errors"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

type transitionGraphTestScope struct {
	*allocationTestScope
	limits  db.InsertGraphLimits
	reserve func(context.Context, query.AutoKeyPlan) ([]int64, error)
	insert  func(context.Context, query.InsertGraphPlan) ([]int64, error)
}

func (scope *transitionGraphTestScope) InsertGraphLimits(ctx context.Context) (db.InsertGraphLimits, error) {
	return scope.limits, scope.ValidateSession(ctx)
}

func (scope *transitionGraphTestScope) ReserveAutoKeys(ctx context.Context, plan query.AutoKeyPlan) ([]int64, error) {
	return scope.reserve(ctx, plan)
}

func (scope *transitionGraphTestScope) InsertGraph(ctx context.Context, plan query.InsertGraphPlan) ([]int64, error) {
	return scope.insert(ctx, plan)
}

func TestTransitionGraphPublishesOnlyCompleteNativeCorrespondence(t *testing.T) {
	for _, mode := range []string{"complete", "missing_capability", "missing_factory", "row_limit", "parameter_limit", "source_revision", "reservation_error", "short_reservation", "occupied_reservation", "prepare_error", "wrong_table", "wrong_key", "underreported_values", "missing_accept", "graph_budget", "insert_error", "short_result", "changed_result", "accept_error", "changed_payload", "expired_after_reservation", "expired_after_insert", "canceled_after_insert", "expired_after_accept"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			failure := errors.New("required graph boundary failure")
			scope := &transitionGraphTestScope{allocationTestScope: &allocationTestScope{}, limits: db.InsertGraphLimits{Rows: query.MaximumInsertGraphRows, Parameters: 2000}}
			kinds := []TransitionReferenceKind{TransitionReferenceGroup, TransitionReferencePermission, TransitionReferenceAccount}
			sources, native := []int64{-7, 0, 5}, []int64{0, -9, 9007199254740993}
			tables := []string{"graph_groups", "graph_permissions", "graph_accounts"}
			references := make(transitionTargetMappings)
			for index, kind := range kinds {
				references[kind] = transitionTargetNamespace{revisions: map[int64]int64{sources[index]: 2}}
			}
			if mode == "source_revision" {
				references[kinds[0]].revisions[sources[0]] = 3
			} else if mode == "occupied_reservation" {
				namespace := references[kinds[0]]
				namespace.revisions[-100] = 1
				namespace.targets = map[int64]int64{-100: native[0]}
				references[kinds[0]] = namespace
			}
			before := cloneTransitionTargetMappings(references)
			reserved, prepared, inserted, accepted, individual := 0, 0, 0, 0, 0
			var events []string
			values := make([]map[string]query.Value, len(kinds))
			var payload query.Value
			if mode == "graph_budget" {
				payload = query.String(strings.Repeat("x", 1<<20))
			}
			key := query.NewFieldRef("id", "id", query.FieldInteger, false)
			peer := query.NewFieldRef("peer", "peer_id", query.FieldInteger, false)
			scope.reserve = func(_ context.Context, plan query.AutoKeyPlan) ([]int64, error) {
				index := slices.Index(tables, plan.Table())
				if index < 0 || plan.Key() != key || plan.Count() != 1 || inserted != 0 || prepared != 0 {
					t.Fatal("reservation lost its declared allocator or graph phase", plan)
				}
				reserved++
				events = append(events, "reserve")
				if reserved == 2 && mode == "reservation_error" {
					return []int64{native[index]}, failure
				}
				if reserved == 2 && mode == "short_reservation" {
					return nil, nil
				}
				if reserved == len(kinds) && mode == "expired_after_reservation" {
					scope.failure = failure
				}
				return []int64{native[index]}, nil
			}
			scope.insert = func(_ context.Context, plan query.InsertGraphPlan) ([]int64, error) {
				inserted++
				events = append(events, "insert")
				if reserved != len(kinds) || prepared != len(kinds) || plan.RowCount() != len(kinds) || accepted != 0 {
					t.Fatal("graph inserted before complete native reservation and preparation")
				}
				result := slices.Clone(native)
				switch mode {
				case "insert_error":
					return result, failure
				case "short_result":
					result = result[:2]
				case "changed_result":
					result[0], result[1] = result[1], result[0]
				case "expired_after_insert":
					scope.failure = failure
				case "canceled_after_insert":
					cancel()
				}
				return result, nil
			}
			nodes := make([]transitionAllocation, len(kinds))
			for index, kind := range kinds {
				reservation, err := query.NewAutoKeyPlan(tables[index], key, 1)
				if err != nil {
					t.Fatal(err)
				}
				next := (index + 1) % len(kinds)
				nodes[index] = transitionAllocation{
					reference: transitionReferenceNode{kind: kind, source: SourceAccount{ID: sources[index], Revision: 2}, references: map[string]TransitionTargetReference{
						"peer": {Kind: kinds[next], Source: SourceAccount{ID: sources[next], Revision: 2}},
					}},
					insert: func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) (int64, error) {
						individual++
						return 0, failure
					},
					values: func() map[string]query.Value { return values[index] },
					graph: &transitionGraphAllocation{reservation: reservation, fields: 2,
						prepare: func(_ context.Context, _ db.RelationSession, _ auth.Principal, provisional transitionTargetMappings, actual int64, deferred map[string]bool) (transitionGraphInsertion, error) {
							prepared++
							events = append(events, "prepare")
							if reserved != len(kinds) || actual != native[index] || len(deferred) != 0 || inserted != 0 {
								t.Fatal("typed preparation received a guessed or incomplete graph key")
							}
							for position, kind := range kinds {
								value, present := provisional[kind].targets[sources[position]]
								if !present || value != native[position] || provisional[kind].revisions[sources[position]] != 2 {
									t.Fatal("provisional graph lost source revision or signed key")
								}
							}
							if mode == "prepare_error" && index == 1 {
								return transitionGraphInsertion{}, failure
							}
							rowValues := map[string]query.Value{"id": query.Integer(actual), "peer": query.Integer(native[next])}
							assignments := []query.Assignment{query.NewAssignment(key, rowValues["id"]), query.NewAssignment(peer, rowValues["peer"])}
							if mode == "graph_budget" {
								for field := range 32 {
									name := "payload_" + strconv.Itoa(field)
									rowValues[name] = payload
									assignments = append(assignments, query.NewAssignment(query.NewFieldRef(name, name, query.FieldString, false), payload))
								}
							}
							table := tables[index]
							if index == 1 {
								switch mode {
								case "wrong_table":
									table = "unowned_table"
								case "wrong_key":
									rowValues["id"] = query.Integer(actual + 1)
									assignments[0] = query.NewAssignment(key, rowValues["id"])
								case "underreported_values":
									rowValues["peer"] = query.Integer(-12345)
								}
							}
							row := transitionGraphInsertion{plan: query.NewInsertPlanReturningKey(table, assignments, key), values: rowValues,
								accept: func(context.Context, db.RelationSession, int64) error {
									accepted++
									events = append(events, "accept")
									if inserted != 1 {
										t.Fatal("typed row accepted without actual graph insertion")
									}
									if index == 1 && mode == "accept_error" {
										return failure
									}
									values[index] = maps.Clone(rowValues)
									if index == 1 && mode == "changed_payload" {
										values[index]["peer"] = query.Integer(-12345)
									}
									if index == 1 && mode == "expired_after_accept" {
										scope.failure = failure
									}
									return nil
								}}
							if mode == "missing_accept" && index == 1 {
								row.accept = nil
							}
							return row, nil
						}},
				}
				if mode == "graph_budget" {
					nodes[index].graph.fields = 34
				}
			}
			var borrowed db.RelationSession = scope
			switch mode {
			case "missing_capability":
				borrowed = scope.allocationTestScope
			case "missing_factory":
				nodes[1].graph = nil
			case "row_limit":
				scope.limits.Rows = 2
			case "parameter_limit":
				scope.limits.Parameters = 5
			}
			actual, err := applyTransitionAllocations(ctx, borrowed, auth.Principal{}, references, nodes)
			if !reflect.DeepEqual(references, before) || individual != 0 || inserted > 1 || reserved > len(kinds) {
				t.Fatal("required graph mutated reviewed mappings, split rows, or retried", events, err)
			}
			if mode == "complete" {
				want := []string{"reserve", "reserve", "reserve", "prepare", "prepare", "prepare", "insert", "accept", "accept", "accept"}
				if err != nil || !slices.Equal(events, want) {
					t.Fatal("required graph phases were incomplete", events, err)
				}
				for index, kind := range kinds {
					key, present := actual[kind].targets[sources[index]]
					if !present || key != native[index] {
						t.Fatal("final correspondence lost actual native keys")
					}
				}
				actual[kinds[0]].revisions[sources[0]] = 99
				if !reflect.DeepEqual(references, before) {
					t.Fatal("published graph aliases reviewed source state")
				}
				return
			}
			if err == nil || actual != nil {
				t.Fatal("incomplete graph published correspondence", mode, events, err)
			}
			if slices.Contains([]string{"missing_capability", "missing_factory", "row_limit", "parameter_limit", "source_revision"}, mode) && len(events) != 0 {
				t.Fatal("unsupported graph consumed keys or wrote rows", mode, events)
			}
			if slices.Contains([]string{"reservation_error", "prepare_error", "insert_error", "accept_error", "expired_after_reservation", "expired_after_insert", "expired_after_accept"}, mode) && !errors.Is(err, failure) {
				t.Fatal("graph boundary lost its failure cause", mode, err)
			}
			if mode == "canceled_after_insert" && !errors.Is(err, context.Canceled) {
				t.Fatal("graph cancellation lost its cause", err)
			}
			if mode == "graph_budget" && (inserted != 0 || accepted != 0) {
				t.Fatal("oversized complete rows reached native insertion", events)
			}
		})
	}
}

func TestTransitionGraphBatchesEachPhysicalAllocator(t *testing.T) {
	for _, mode := range []string{"complete", "duplicate_keys", "short_keys"} {
		t.Run(mode, func(t *testing.T) {
			key := query.NewFieldRef("id", "id", query.FieldInteger, false)
			peer := query.NewFieldRef("peer", "peer", query.FieldInteger, false)
			reservation, err := query.NewAutoKeyPlan("same_table", key, 1)
			if err != nil {
				t.Fatal(err)
			}
			keys := []int64{0, -3, -6}
			refs := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{0: 1, 1: 1, 2: 1}}}
			values := make([]map[string]query.Value, 3)
			calls, insertions := 0, 0
			scope := &transitionGraphTestScope{allocationTestScope: &allocationTestScope{}, limits: db.InsertGraphLimits{Rows: 3, Parameters: 6}}
			scope.reserve = func(_ context.Context, plan query.AutoKeyPlan) ([]int64, error) {
				calls++
				if plan.Count() != 3 || plan.Table() != "same_table" || plan.Key() != key {
					t.Fatal("one physical allocator was not batched", plan)
				}
				result := slices.Clone(keys)
				if mode == "duplicate_keys" {
					result[1] = result[0]
				} else if mode == "short_keys" {
					result = result[:2]
				}
				return result, nil
			}
			scope.insert = func(context.Context, query.InsertGraphPlan) ([]int64, error) {
				insertions++
				return slices.Clone(keys), nil
			}
			nodes := make([]transitionAllocation, 3)
			for index := range nodes {
				next := (index + 1) % len(nodes)
				nodes[index] = transitionAllocation{
					reference: transitionReferenceNode{source: SourceAccount{ID: int64(index), Revision: 1}, references: map[string]TransitionTargetReference{"peer": {Source: SourceAccount{ID: int64(next), Revision: 1}}}},
					insert: func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) (int64, error) {
						t.Fatal("required cycle split into ordinary INSERTs")
						return 0, nil
					},
					values: func() map[string]query.Value { return values[index] },
					graph: &transitionGraphAllocation{reservation: reservation, fields: 2, prepare: func(_ context.Context, _ db.RelationSession, _ auth.Principal, mappings transitionTargetMappings, actual int64, _ map[string]bool) (transitionGraphInsertion, error) {
						peerKey, present := mappings[TransitionReferenceAccount].targets[int64(next)]
						if !present || peerKey != keys[next] || actual != keys[index] {
							t.Fatal("batched native reservation lost row association")
						}
						expected := map[string]query.Value{"id": query.Integer(actual), "peer": query.Integer(peerKey)}
						return transitionGraphInsertion{plan: query.NewInsertPlanReturningKey("same_table", []query.Assignment{query.NewAssignment(key, expected["id"]), query.NewAssignment(peer, expected["peer"])}, key), values: expected,
							accept: func(context.Context, db.RelationSession, int64) error {
								values[index] = maps.Clone(expected)
								return nil
							}}, nil
					}},
				}
			}
			result, err := applyTransitionAllocations(t.Context(), scope, auth.Principal{}, refs, nodes)
			if calls != 1 || refs[TransitionReferenceAccount].targets != nil {
				t.Fatal("allocator retried or changed original mappings", calls)
			}
			if mode == "complete" {
				if err != nil || insertions != 1 || !maps.Equal(result[TransitionReferenceAccount].targets, map[int64]int64{0: 0, 1: -3, 2: -6}) {
					t.Fatal("batched native graph was incomplete", result, err)
				}
			} else if err == nil || result != nil || insertions != 0 {
				t.Fatal("invalid allocation reached the graph INSERT", result, insertions, err)
			}
		})
	}
}

func TestTransitionGraphCompletesNullableEdgesAcrossComponents(t *testing.T) {
	for _, mode := range []string{"complete", "finish_error", "missing_finish"} {
		t.Run(mode, func(t *testing.T) {
			failure := errors.New("nullable graph completion failed")
			key := query.NewFieldRef("id", "id", query.FieldInteger, false)
			peer := query.NewFieldRef("peer", "peer", query.FieldInteger, false)
			outside := query.NewFieldRef("outside", "outside", query.FieldInteger, true)
			reservation, err := query.NewAutoKeyPlan("mixed", key, 1)
			if err != nil {
				t.Fatal(err)
			}
			refs := transitionTargetMappings{TransitionReferenceAccount: {revisions: map[int64]int64{0: 1, 1: 1, 2: 1}}}
			values := make([]map[string]query.Value, 3)
			var events []string
			scope := &transitionGraphTestScope{allocationTestScope: &allocationTestScope{}, limits: db.InsertGraphLimits{Rows: 2, Parameters: 6}}
			scope.reserve = func(_ context.Context, plan query.AutoKeyPlan) ([]int64, error) {
				events = append(events, "reserve")
				if plan.Count() != 2 || plan.Table() != "mixed" {
					t.Fatal("nullable dependent was absorbed into native cycle")
				}
				return []int64{10, 20}, nil
			}
			scope.insert = func(_ context.Context, plan query.InsertGraphPlan) ([]int64, error) {
				events = append(events, "graph")
				if plan.RowCount() != 2 {
					t.Fatal("native graph omitted or absorbed a row")
				}
				return []int64{10, 20}, nil
			}
			nodes := make([]transitionAllocation, 3)
			for index := range 2 {
				next := 1 - index
				nodes[index] = transitionAllocation{reference: transitionReferenceNode{source: SourceAccount{ID: int64(index), Revision: 1}, references: map[string]TransitionTargetReference{
					"peer": {Source: SourceAccount{ID: int64(next), Revision: 1}},
				}}, values: func() map[string]query.Value { return values[index] },
					insert: func(context.Context, db.RelationSession, auth.Principal, transitionTargetMappings, map[string]bool) (int64, error) {
						t.Fatal("required component was inserted one row at a time")
						return 0, failure
					}, graph: &transitionGraphAllocation{reservation: reservation, fields: 3,
						prepare: func(_ context.Context, _ db.RelationSession, _ auth.Principal, actual transitionTargetMappings, id int64, deferred map[string]bool) (transitionGraphInsertion, error) {
							events = append(events, "prepare")
							if id != int64(10+index*10) || len(deferred) != index || index == 1 && !deferred["outside"] {
								t.Fatal("native row lost its exact nullable staging fields", id, deferred)
							}
							if _, found := actual[TransitionReferenceAccount].targets[2]; found {
								t.Fatal("native component guessed a later dependent's key")
							}
							expected := map[string]query.Value{"id": query.Integer(id), "peer": query.Integer(int64(10 + next*10)), "outside": query.Null()}
							return transitionGraphInsertion{plan: query.NewInsertPlanReturningKey("mixed", []query.Assignment{query.NewAssignment(key, expected["id"]), query.NewAssignment(peer, expected["peer"]), query.NewAssignment(outside, query.Null())}, key), values: expected,
								accept: func(context.Context, db.RelationSession, int64) error {
									events = append(events, "accept")
									values[index] = maps.Clone(expected)
									return nil
								}}, nil
						}},
				}
			}
			nodes[1].reference.references["outside"] = TransitionTargetReference{Source: SourceAccount{ID: 2, Revision: 1}}
			nodes[1].reference.nullable = map[string]bool{"outside": true}
			if mode != "missing_finish" {
				nodes[1].finish = func(_ context.Context, _ db.RelationSession, _ auth.Principal, actual transitionTargetMappings, fields map[string]bool) error {
					events = append(events, "finish")
					if !maps.Equal(fields, map[string]bool{"outside": true}) || !maps.Equal(actual[TransitionReferenceAccount].targets, map[int64]int64{0: 10, 1: 20, 2: 30}) || !values[1]["outside"].IsNull() {
						t.Fatal("nullable completion preceded all native identities or changed extra fields")
					}
					if mode == "finish_error" {
						return failure
					}
					values[1]["outside"] = query.Integer(30)
					return nil
				}
			}
			nodes[2] = transitionAllocation{reference: transitionReferenceNode{source: SourceAccount{ID: 2, Revision: 1}, references: map[string]TransitionTargetReference{
				"peer": {Source: SourceAccount{ID: 1, Revision: 1}},
			}}, values: func() map[string]query.Value { return values[2] },
				insert: func(_ context.Context, _ db.RelationSession, _ auth.Principal, actual transitionTargetMappings, fields map[string]bool) (int64, error) {
					events = append(events, "ordinary")
					if actual[TransitionReferenceAccount].targets[1] != 20 || len(fields) != 0 {
						t.Fatal("ordinary dependent preceded its actual native graph")
					}
					values[2] = map[string]query.Value{"id": query.Integer(30), "peer": query.Integer(20), "outside": query.Null()}
					return 30, nil
				}}
			result, err := applyTransitionAllocations(t.Context(), scope, auth.Principal{}, refs, nodes)
			if refs[TransitionReferenceAccount].targets != nil {
				t.Fatal("mixed graph changed original source correspondence")
			}
			if mode == "missing_finish" {
				if err == nil || result != nil || len(events) != 0 {
					t.Fatal("missing nullable finalizer reserved keys or wrote rows", events, err)
				}
				return
			}
			if !slices.Equal(events, []string{"reserve", "prepare", "prepare", "graph", "accept", "accept", "ordinary", "finish"}) {
				t.Fatal("mixed graph phases are incomplete", events, err)
			}
			if mode == "finish_error" {
				if !errors.Is(err, failure) || result != nil {
					t.Fatal("failed finalization published partial correspondence", result, err)
				}
			} else if err != nil || !maps.Equal(result[TransitionReferenceAccount].targets, map[int64]int64{0: 10, 1: 20, 2: 30}) || !values[1]["outside"].Equal(query.Integer(30)) {
				t.Fatal("nullable edge was not completed with the actual later key", result, err)
			}
		})
	}
}
