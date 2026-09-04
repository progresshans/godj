package blog

import "example.com/godj-relationbinding-external/meta"

type Post struct {
	ID         int64
	Title      string
	AuthorID   int64
	ReviewerID *int64
}

var Descriptor = meta.ModelDescriptor{
	Key: meta.ModelKey{App: "blog", Model: "post"},
	Relations: []meta.Relation{
		{Field: "author", Column: "author_id", Target: meta.ModelKey{App: "authors", Model: "author"}, Delete: "protect", Reverse: "posts"},
		{Field: "reviewer", Column: "reviewer_id", Target: meta.ModelKey{App: "authors", Model: "author"}, Nullable: true, Delete: "set_null", Reverse: "reviewed_posts"},
	},
}
