package relationbinding

import (
	"bytes"
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustRelationBindings(t *testing.T) bindingSet {
	t.Helper()
	set, err := bindProject(relationFixture())
	if err != nil {
		t.Fatalf("bind relation fixture: %v", err)
	}
	return set
}

func TestTypedAndDynamicRelationPathsConvergeToCanonicalAST(t *testing.T) {
	t.Parallel()

	set := mustRelationBindings(t)
	authors := modelKey{App: "authors", Model: "author"}
	posts := modelKey{App: "blog", Model: "post"}
	tests := []struct {
		name        string
		typed       typedSelector
		dynamicRoot modelKey
		dynamic     string
	}{
		{
			name:        "forward terminal",
			typed:       typedSelector{Root: posts, Relation: "author", Direction: directionForward, TerminalField: "name", Lookup: lookupExact},
			dynamicRoot: posts,
			dynamic:     "author__name",
		},
		{
			name:        "reverse terminal",
			typed:       typedSelector{Root: authors, Relation: "posts", Direction: directionReverse, TerminalField: "title", Lookup: lookupExact},
			dynamicRoot: authors,
			dynamic:     "posts__title",
		},
		{
			name:        "nullable isnull",
			typed:       typedSelector{Root: posts, Relation: "reviewer", Direction: directionForward, Lookup: lookupIsNull},
			dynamicRoot: posts,
			dynamic:     "reviewer__isnull",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			typed, err := buildTypedPath(set, tt.typed)
			if err != nil {
				t.Fatalf("typed path: %v", err)
			}
			dynamic, err := buildDynamicPath(set, tt.dynamicRoot, tt.dynamic)
			if err != nil {
				t.Fatalf("dynamic path: %v", err)
			}
			if !reflect.DeepEqual(typed, dynamic) || !bytes.Equal(typed.CanonicalBytes(), dynamic.CanonicalBytes()) {
				t.Fatalf("typed and dynamic AST diverged\ntyped=%s\ndynamic=%s", typed.CanonicalBytes(), dynamic.CanonicalBytes())
			}
		})
	}
}

func TestOperationContextChoosesJoinMeaningAndReusesEdges(t *testing.T) {
	t.Parallel()

	set := mustRelationBindings(t)
	posts := modelKey{App: "blog", Model: "post"}

	authorName, err := buildDynamicPath(set, posts, "author__name")
	if err != nil {
		t.Fatal(err)
	}
	authorID, err := buildDynamicPath(set, posts, "author__id")
	if err != nil {
		t.Fatal(err)
	}
	rel004, err := planRelations(operationPredicate, []relationPath{authorName, authorID}, nil)
	if err != nil {
		t.Fatalf("REL-004 plan: %v", err)
	}
	assertSingleJoin(t, rel004, joinInner)

	reviewerNull, err := buildDynamicPath(set, posts, "reviewer__isnull")
	if err != nil {
		t.Fatal(err)
	}
	rel006, err := planRelations(operationPredicate, []relationPath{reviewerNull}, nil)
	if err != nil {
		t.Fatalf("REL-006 plan: %v", err)
	}
	if len(rel006.Joins) != 0 {
		t.Fatalf("REL-006 isnull joins = %#v, want none", rel006.Joins)
	}

	authorEager, err := buildTypedPath(set, typedSelector{Root: posts, Relation: "author", Direction: directionForward, Lookup: lookupRelated})
	if err != nil {
		t.Fatal(err)
	}
	rel009, err := planRelations(operationSelectRelated, []relationPath{authorEager}, nil)
	if err != nil {
		t.Fatalf("REL-009 plan: %v", err)
	}
	assertSingleJoin(t, rel009, joinInner)

	reviewerEager, err := buildTypedPath(set, typedSelector{Root: posts, Relation: "reviewer", Direction: directionForward, Lookup: lookupRelated})
	if err != nil {
		t.Fatal(err)
	}
	rel010, err := planRelations(operationSelectRelated, []relationPath{reviewerEager}, nil)
	if err != nil {
		t.Fatalf("REL-010 plan: %v", err)
	}
	assertSingleJoin(t, rel010, joinLeftOuter)

	if bytes.Equal(rel004.CanonicalBytes(), rel006.CanonicalBytes()) || bytes.Equal(rel009.CanonicalBytes(), rel010.CanonicalBytes()) {
		t.Fatal("distinct operation/nullability contexts produced identical canonical plans")
	}
}

func TestReversePrefetchPlanIsTwoStageJoinFreeAndOrdersKeys(t *testing.T) {
	t.Parallel()

	set := mustRelationBindings(t)
	authors := modelKey{App: "authors", Model: "author"}
	posts, err := buildTypedPath(set, typedSelector{Root: authors, Relation: "posts", Direction: directionReverse, Lookup: lookupRelated})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planRelations(operationPrefetch, []relationPath{posts}, []int64{3, 1, 2, 3, 1})
	if err != nil {
		t.Fatalf("prefetch plan: %v", err)
	}
	if len(plan.Joins) != 0 || len(plan.Stages) != 2 {
		t.Fatalf("prefetch plan joins/stages = %d/%d, want 0/2: %#v", len(plan.Joins), len(plan.Stages), plan)
	}
	if got, want := plan.Stages[0].Kind, "primary"; got != want {
		t.Fatalf("first stage = %q, want %q", got, want)
	}
	batch := plan.Stages[1]
	if got, want := batch.Kind, "foreign_key_batch"; got != want {
		t.Fatalf("batch kind = %q, want %q", got, want)
	}
	if got, want := batch.Keys, []int64{1, 2, 3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("batch keys = %v, want %v", got, want)
	}
	if got, want := batch.ForeignKey, "author_id"; got != want {
		t.Fatalf("batch FK = %q, want %q", got, want)
	}
}

func TestInvalidRelationPathsFailBeforeCompilerAndDatabaseIO(t *testing.T) {
	t.Parallel()

	set := mustRelationBindings(t)
	authors := modelKey{App: "authors", Model: "author"}
	posts := modelKey{App: "blog", Model: "post"}
	probe := &ioProbe{}

	for _, expression := range []string{"missing__name", "author__missing", "author__name__contains", "author"} {
		if _, err := compileDynamicCandidate(set, posts, expression, operationPredicate, probe); err == nil {
			t.Errorf("invalid dynamic path %q unexpectedly succeeded", expression)
		}
	}

	reverse, err := buildTypedPath(set, typedSelector{Root: authors, Relation: "posts", Direction: directionReverse, Lookup: lookupRelated})
	if err != nil {
		t.Fatal(err)
	}
	_, err = compileAndEvaluateCandidate(operationSelectRelated, []relationPath{reverse}, nil, probe)
	var pathErr *relationPathError
	if !errors.As(err, &pathErr) || pathErr.Code != "multi_valued_select_related" {
		t.Fatalf("reverse select_related error = %T %v, want multi_valued_select_related", err, err)
	}
	if probe.CompilerCalls != 0 || probe.DatabaseCalls != 0 {
		t.Fatalf("invalid path reached compiler/database: %#v", probe)
	}
}

func TestRelationASTMeaningMutationCannotRemainGreen(t *testing.T) {
	t.Parallel()

	set := mustRelationBindings(t)
	posts := modelKey{App: "blog", Model: "post"}
	baseline, err := buildDynamicPath(set, posts, "author__name")
	if err != nil {
		t.Fatal(err)
	}
	mutated, err := buildDynamicPath(set, posts, "reviewer__name")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(baseline.CanonicalBytes(), mutated.CanonicalBytes()) {
		t.Fatal("relation-edge mutation left canonical AST unchanged")
	}

	copyBytes := baseline.CanonicalBytes()
	copyBytes[0] ^= 0xff
	if bytes.Equal(copyBytes, baseline.CanonicalBytes()) {
		t.Fatal("canonical AST bytes alias caller mutation")
	}

	input := []relationPath{baseline}
	plan, err := planRelations(operationPredicate, input, nil)
	if err != nil {
		t.Fatal(err)
	}
	wantPlan := plan.CanonicalBytes()
	input[0].Hops[0].Column = "mutated_after_planning_id"
	input[0].Hops = append(input[0].Hops, input[0].Hops[0])
	if !bytes.Equal(plan.CanonicalBytes(), wantPlan) {
		t.Fatal("caller path mutation changed an already-built immutable plan")
	}

	const readers = 32
	errors := make(chan []byte, readers)
	var wait sync.WaitGroup
	wait.Add(readers)
	for range readers {
		go func() {
			defer wait.Done()
			for range 100 {
				if got := plan.CanonicalBytes(); !bytes.Equal(got, wantPlan) {
					errors <- got
					return
				}
			}
		}()
	}
	wait.Wait()
	close(errors)
	for got := range errors {
		t.Fatalf("concurrent immutable-plan read = %s, want %s", got, wantPlan)
	}
}

func assertSingleJoin(t *testing.T, plan relationPlan, kind joinKind) {
	t.Helper()
	if len(plan.Joins) != 1 || plan.Joins[0].Kind != kind {
		t.Fatalf("joins = %#v, want one %s join", plan.Joins, kind)
	}
}
