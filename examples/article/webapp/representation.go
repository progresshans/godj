package webapp

import (
	"fmt"

	articleproject "github.com/progresshans/godj/examples/article/project"
)

// ArticleView is the app-owned serialization and template boundary. Generated
// wrappers and their lazy/cache state never cross into a renderer.
type ArticleView struct {
	ID        int64   `json:"id"`
	Title     string  `json:"title"`
	Published bool    `json:"published"`
	Summary   *string `json:"summary"`
}

// NewArticleView explicitly unwraps and snapshots one generated facade model.
func NewArticleView(article *articleproject.ModelsArticle) (ArticleView, error) {
	if article == nil {
		return ArticleView{}, fmt.Errorf("article view: wrapper is nil")
	}
	raw, err := article.Unwrap()
	if err != nil {
		return ArticleView{}, fmt.Errorf("article view: unwrap generated model: %w", err)
	}
	return ArticleView{
		ID:        raw.ID,
		Title:     raw.Title,
		Published: raw.Published,
		Summary:   raw.Summary,
	}, nil
}

type articleListPage struct {
	ProjectName string
	SelfURL     string
	Articles    []ArticleView
	Report      articleListReport
	Pagination  articleListPagination
}

type articleListReport struct {
	MatchingCount int64
	LatestID      *int64
}

type articleListPagination struct {
	Offset   int
	Limit    int
	Returned int
}
