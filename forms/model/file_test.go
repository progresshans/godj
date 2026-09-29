package model_test

import (
	"testing"

	article "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/uploads"
)

func TestFileCommandSurvivesTypedFormsetWithoutBecomingStoredScalar(t *testing.T) {
	field, err := forms.FileField("document")
	if err != nil {
		t.Fatal(err)
	}
	row, err := (formmodel.Definition{Fields: []string{"title"}, ExtraFields: []forms.Field{field}}).Spec((article.ArticleDescriptor{}).Metadata())
	if err != nil {
		t.Fatal(err)
	}
	config := forms.DefaultSetConfig()
	config.Prefix = "articles"
	spec, err := forms.NewSetSpec(row, config)
	if err != nil {
		t.Fatal(err)
	}
	file, err := uploads.NewFile("report.txt", "text/plain", []byte("attachment"))
	if err != nil {
		t.Fatal(err)
	}
	data := forms.NewDataWithFiles(map[string][]string{"articles-TOTAL_FORMS": {"1"}, "articles-INITIAL_FORMS": {"0"}, "articles-0-title": {"File command"}}, map[string][]uploads.File{"articles-0-document": {file}})
	set, err := formmodel.BindSet(article.ArticleObjects, spec, data, nil, formmodel.PostClean{})
	if err != nil || !set.Valid() {
		t.Fatal("file command binding", err)
	}
	instance, ok := set.Instance(0)
	if !ok {
		t.Fatal("missing row")
	}
	if _, present := instance.BoundForm().Candidate().Get("document"); present {
		t.Fatal("upload became a model scalar")
	}
	prepared, err := set.Prepare()
	if err != nil {
		t.Fatal(err)
	}
	rows := prepared.Rows()
	if len(rows) != 1 {
		t.Fatal("file command lost its row")
	}
	command, ok := rows[0].Prepared().Input().File("document")
	if !ok {
		t.Fatal("file command absent after typed preparation")
	}
	upload, ok := command.Upload()
	if !ok || !upload.Equal(file) {
		t.Fatal("typed preparation replaced upload")
	}
	model, err := rows[0].Model()
	if err != nil || model.Title != "File command" {
		t.Fatal("scalar preparation changed", err)
	}
}
