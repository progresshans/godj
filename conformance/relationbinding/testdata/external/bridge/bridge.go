package bridge

import (
	"example.com/godj-relationbinding-external/authors"
	"example.com/godj-relationbinding-external/blog"
	"example.com/godj-relationbinding-external/meta"
)

var AllDescriptors = []meta.ModelDescriptor{authors.Descriptor, blog.Descriptor}

func PostAuthor(post blog.Post, records map[int64]authors.Author) (authors.Author, bool) {
	value, ok := records[post.AuthorID]
	return value, ok
}

func AuthorFavoritePost(author authors.Author, records map[int64]blog.Post) (blog.Post, bool) {
	if author.FavoritePostID == nil {
		return blog.Post{}, false
	}
	value, ok := records[*author.FavoritePostID]
	return value, ok
}

func AuthorManager(author authors.Author, records map[int64]authors.Author) (authors.Author, bool) {
	if author.ManagerID == nil {
		return authors.Author{}, false
	}
	value, ok := records[*author.ManagerID]
	return value, ok
}
