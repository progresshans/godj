package article_test

import "testing"

func TestArticleAPIBearerPostgresUserFlow(t *testing.T) {
	runArticleAPIBearerUserFlow(t, newArticleAPIPostgresBackend(t, "godj_article_api_bearer"))
}
