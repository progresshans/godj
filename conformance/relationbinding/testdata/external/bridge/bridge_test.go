package bridge

import (
	"reflect"
	"testing"

	"example.com/godj-relationbinding-external/authors"
	"example.com/godj-relationbinding-external/blog"
	"example.com/godj-relationbinding-external/meta"
)

func TestTypedProjectBridge(t *testing.T) {
	favoriteID := int64(10)
	managerID := int64(2)
	post := blog.Post{ID: 10, AuthorID: 1}
	author := authors.Author{ID: 1, FavoritePostID: &favoriteID, ManagerID: &managerID}
	authorRecords := map[int64]authors.Author{1: author, 2: {ID: 2}}
	postRecords := map[int64]blog.Post{10: post}

	if value, ok := PostAuthor(post, authorRecords); !ok || value.ID != 1 {
		t.Fatalf("PostAuthor = %#v/%v", value, ok)
	}
	if value, ok := AuthorFavoritePost(author, postRecords); !ok || value.ID != 10 {
		t.Fatalf("AuthorFavoritePost = %#v/%v", value, ok)
	}
	if value, ok := AuthorManager(author, authorRecords); !ok || value.ID != 2 {
		t.Fatalf("AuthorManager = %#v/%v", value, ok)
	}
	if got, want := len(BoundRelations), 4; got != want {
		t.Fatalf("BoundRelations = %d, want %d", got, want)
	}
	var declared []meta.BoundRelation
	for _, descriptor := range AllDescriptors {
		for _, relation := range descriptor.Relations {
			declared = append(declared, meta.BoundRelation{
				Source: descriptor.Key, Field: relation.Field, Column: relation.Column,
				Target: relation.Target, Nullable: relation.Nullable, Delete: relation.Delete, ReverseName: relation.Reverse,
			})
		}
	}
	if !reflect.DeepEqual(BoundRelations, declared) {
		t.Fatalf("generated binding does not match symbolic app declarations\ngenerated=%#v\ndeclared=%#v", BoundRelations, declared)
	}
}
