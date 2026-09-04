package relationbinding

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
)

func relationFixture() []modelDescriptor {
	authors := modelKey{App: "authors", Model: "author"}
	posts := modelKey{App: "blog", Model: "post"}
	return []modelDescriptor{
		{
			Key:    authors,
			Fields: []string{"id", "name"},
			Relations: []relationDeclaration{
				{Field: "favorite_post", Column: "favorite_post_id", Target: posts, Nullable: true, Delete: deleteSetNull, Reverse: "favored_by"},
				{Field: "manager", Column: "manager_id", Target: authors, Nullable: true, Delete: deleteSetNull, Reverse: "reports"},
			},
		},
		{
			Key:    posts,
			Fields: []string{"id", "title"},
			Relations: []relationDeclaration{
				{Field: "author", Column: "author_id", Target: authors, Nullable: false, Delete: deleteProtect, Reverse: "posts"},
				{Field: "reviewer", Column: "reviewer_id", Target: authors, Nullable: true, Delete: deleteSetNull, Reverse: "reviewed_posts"},
			},
		},
	}
}

func TestCanonicalSymbolicIdentityAndFieldOwnership(t *testing.T) {
	t.Parallel()

	key, err := canonicalModelKey(" Authors ", "Author")
	if err != nil {
		t.Fatalf("canonicalModelKey: %v", err)
	}
	if want := (modelKey{App: "authors", Model: "author"}); key != want {
		t.Fatalf("canonical key = %#v, want %#v", key, want)
	}
	for _, input := range [][2]string{{"", "author"}, {"authors", ""}, {"authors/blog", "post"}, {"1authors", "author"}} {
		if _, err := canonicalModelKey(input[0], input[1]); err == nil {
			t.Errorf("canonicalModelKey(%q, %q) unexpectedly succeeded", input[0], input[1])
		}
	}

	fixture := relationFixture()
	fixture[1].Relations[0].Generated = true
	_, err = bindProject(fixture)
	assertBindingCode(t, err, "relation_not_source_owned")
}

func TestAtomicProjectBinderMutualSelfAndDeterminism(t *testing.T) {
	t.Parallel()

	fixture := relationFixture()
	first, err := bindProject(fixture)
	if err != nil {
		t.Fatalf("bind fixture: %v", err)
	}
	if got, want := len(first.Forward()), 4; got != want {
		t.Fatalf("forward bindings = %d, want %d", got, want)
	}
	if got, want := len(first.Reverse()), 4; got != want {
		t.Fatalf("reverse bindings = %d, want %d", got, want)
	}
	if first.Digest() == "" || len(first.CanonicalBytes()) == 0 {
		t.Fatal("binding did not publish canonical bytes and digest")
	}

	permuted := relationFixture()
	slices.Reverse(permuted)
	slices.Reverse(permuted[0].Relations)
	slices.Reverse(permuted[1].Relations)
	second, err := bindProject(permuted)
	if err != nil {
		t.Fatalf("bind permuted fixture: %v", err)
	}
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) || first.Digest() != second.Digest() {
		t.Fatalf("input order changed canonical binding\nfirst=%s\nsecond=%s", first.CanonicalBytes(), second.CanonicalBytes())
	}

	// Caller mutation after binding must not alias the published snapshot.
	fixture[0].Key.App = "mutated"
	fixture[0].Fields[0] = "mutated"
	fixture[0].Relations[0].Target = modelKey{App: "missing", Model: "target"}
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatal("caller mutation changed an already-bound snapshot")
	}
	forwardCopy := first.Forward()
	forwardCopy[0].Column = "mutated"
	canonicalCopy := first.CanonicalBytes()
	canonicalCopy[0] ^= 0xff
	if !bytes.Equal(first.CanonicalBytes(), second.CanonicalBytes()) {
		t.Fatal("accessor mutation changed immutable binding storage")
	}
}

func TestBinderFailuresAreStructuredCanonicalAndPublishNothing(t *testing.T) {
	t.Parallel()

	missing := modelKey{App: "missing", Model: "target"}
	tests := []struct {
		name string
		edit func([]modelDescriptor) []modelDescriptor
		code string
	}{
		{
			name: "unresolved target",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[0].Target = missing
				return models
			},
			code: "unresolved_target",
		},
		{
			name: "duplicate model identity",
			edit: func(models []modelDescriptor) []modelDescriptor {
				return append(models, models[0])
			},
			code: "duplicate_model_identity",
		},
		{
			name: "invalid source relation",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[0].Field = "Bad-Field"
				return models
			},
			code: "invalid_source_relation",
		},
		{
			name: "duplicate source field",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[0].Field = "title"
				return models
			},
			code: "duplicate_source_field",
		},
		{
			name: "reverse collides with target field",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[0].Reverse = "name"
				return models
			},
			code: "reverse_name_collision",
		},
		{
			name: "reverse collides with target relation field",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[0].Reverse = "manager"
				return models
			},
			code: "reverse_name_collision",
		},
		{
			name: "reverse collides with another relation",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[1].Reverse = "posts"
				return models
			},
			code: "reverse_name_collision",
		},
		{
			name: "set null requires nullable",
			edit: func(models []modelDescriptor) []modelDescriptor {
				models[1].Relations[1].Nullable = false
				return models
			},
			code: "set_null_requires_nullable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			set, err := bindProject(tt.edit(relationFixture()))
			assertBindingCode(t, err, tt.code)
			if len(set.Models()) != 0 || len(set.Forward()) != 0 || len(set.Reverse()) != 0 || len(set.CanonicalBytes()) != 0 || set.Digest() != "" {
				t.Fatalf("failed binding partially published: %#v", set)
			}
		})
	}

	// Two simultaneous unresolved targets must choose the canonical relation
	// identity, not descriptor or map iteration order.
	canonicalFailure := relationFixture()
	canonicalFailure[0].Relations[0].Target = missing // authors.author.favorite_post
	canonicalFailure[1].Relations[1].Target = missing // blog.post.reviewer
	for i := 0; i < 20; i++ {
		if i%2 == 1 {
			slices.Reverse(canonicalFailure)
		}
		_, err := bindProject(canonicalFailure)
		var bindingErr *bindingError
		if !errors.As(err, &bindingErr) {
			t.Fatalf("failure %d is not structured: %v", i, err)
		}
		if got, want := (relationIdentity{Source: bindingErr.Model, Field: bindingErr.Field}).String(), "authors.author.favorite_post"; got != want {
			t.Fatalf("failure %d identity = %q, want %q", i, got, want)
		}
	}
}

func TestAtomicPublisherPreservesLastGoodAcrossFailureAndConcurrentReads(t *testing.T) {
	t.Parallel()

	var publisher bindingPublisher
	if err := publisher.Publish(relationFixture()); err != nil {
		t.Fatalf("publish initial binding: %v", err)
	}
	before, ok := publisher.Snapshot()
	if !ok {
		t.Fatal("publisher has no initial snapshot")
	}

	failed := relationFixture()
	failed[1].Relations[0].Target = modelKey{App: "missing", Model: "target"}
	if err := publisher.Publish(failed); err == nil {
		t.Fatal("invalid project unexpectedly published")
	}
	after, ok := publisher.Snapshot()
	if !ok || !bytes.Equal(before.CanonicalBytes(), after.CanonicalBytes()) || before.Digest() != after.Digest() {
		t.Fatal("failed publication replaced last-good binding")
	}

	const readers = 32
	var wait sync.WaitGroup
	wait.Add(readers)
	for i := 0; i < readers; i++ {
		go func() {
			defer wait.Done()
			for iteration := 0; iteration < 100; iteration++ {
				snapshot, exists := publisher.Snapshot()
				if !exists || snapshot.Digest() == "" || len(snapshot.Forward()) != 4 || len(snapshot.Reverse()) != 4 {
					t.Errorf("reader observed partial snapshot: exists=%v digest=%q forward=%d reverse=%d", exists, snapshot.Digest(), len(snapshot.Forward()), len(snapshot.Reverse()))
					return
				}
			}
		}()
	}
	for i := 0; i < 32; i++ {
		if err := publisher.Publish(failed); err == nil {
			t.Errorf("failed publication %d unexpectedly succeeded", i)
		}
	}
	wait.Wait()
}

func TestBindingMeaningMutationsCannotRemainGreen(t *testing.T) {
	t.Parallel()

	baseline, err := bindProject(relationFixture())
	if err != nil {
		t.Fatalf("bind baseline: %v", err)
	}
	mutations := []struct {
		name string
		edit func([]modelDescriptor) []modelDescriptor
	}{
		{"target", func(models []modelDescriptor) []modelDescriptor {
			models[1].Relations[0].Target = modelKey{App: "authors", Model: "author_archive"}
			return append(models, modelDescriptor{Key: modelKey{App: "authors", Model: "author_archive"}, Fields: []string{"id"}})
		}},
		{"reverse", func(models []modelDescriptor) []modelDescriptor {
			models[1].Relations[0].Reverse = "authored_posts"
			return models
		}},
		{"nullability", func(models []modelDescriptor) []modelDescriptor {
			models[1].Relations[0].Nullable = true
			return models
		}},
		{"delete", func(models []modelDescriptor) []modelDescriptor {
			models[1].Relations[0].Delete = deleteSetNull
			models[1].Relations[0].Nullable = true
			return models
		}},
		{"column", func(models []modelDescriptor) []modelDescriptor {
			models[1].Relations[0].Column = "writer_id"
			return models
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			fixture := mutation.edit(relationFixture())
			changed, err := bindProject(fixture)
			if err != nil {
				t.Fatalf("bind mutation: %v", err)
			}
			if bytes.Equal(baseline.CanonicalBytes(), changed.CanonicalBytes()) || baseline.Digest() == changed.Digest() {
				t.Fatalf("%s mutation did not change canonical binding", mutation.name)
			}
		})
	}
}

func assertBindingCode(t *testing.T, err error, want string) *bindingError {
	t.Helper()
	if err == nil {
		t.Fatalf("binding unexpectedly succeeded; want %s", want)
	}
	var bindingErr *bindingError
	if !errors.As(err, &bindingErr) {
		t.Fatalf("binding error = %T %v, want *bindingError", err, err)
	}
	if bindingErr.Code != want {
		t.Fatalf("binding code = %q, want %q (%v)", bindingErr.Code, want, bindingErr)
	}
	return bindingErr
}

func Example_bindingCandidate() {
	set, err := bindProject(relationFixture())
	fmt.Println(err == nil, len(set.Forward()), len(set.Reverse()))
	// Output: true 4 4
}
