package authors

import "example.com/godj-relationbinding-external/meta"

type Author struct {
	ID             int64
	Name           string
	FavoritePostID *int64
	ManagerID      *int64
}

var Descriptor = meta.ModelDescriptor{
	Key: meta.ModelKey{App: "authors", Model: "author"},
	Relations: []meta.Relation{
		{Field: "favorite_post", Column: "favorite_post_id", Target: meta.ModelKey{App: "blog", Model: "post"}, Nullable: true, Delete: "set_null", Reverse: "favored_by"},
		{Field: "manager", Column: "manager_id", Target: meta.ModelKey{App: "authors", Model: "author"}, Nullable: true, Delete: "set_null", Reverse: "reports"},
	},
}
