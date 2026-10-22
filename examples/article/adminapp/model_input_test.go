package adminapp_test

import (
	articlemodels "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"testing"
)

// Fixtures explicitly supply every scalar, preserving their full replacement
// policy. Partial fixtures use independent presence maps below.
func fullArticleInput(t testing.TB, value articlemodels.Article) orm.ModelInput[articlemodels.Article] {
	t.Helper()
	values, err := articlemodels.ArticleObjects.ModelValues(value)
	if err != nil {
		t.Fatal(err)
	}
	delete(values, "id")
	return partialArticleInput(t, values)
}

func partialArticleInput(t testing.TB, values map[string]query.Value) orm.ModelInput[articlemodels.Article] {
	t.Helper()
	input, err := articlemodels.ArticleObjects.Input(values)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

func fullArticleInputs(t testing.TB, values []articlemodels.Article) []orm.ModelInput[articlemodels.Article] {
	t.Helper()
	inputs := make([]orm.ModelInput[articlemodels.Article], len(values))
	for index, value := range values {
		inputs[index] = fullArticleInput(t, value)
	}
	return inputs
}
