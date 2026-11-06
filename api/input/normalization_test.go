package input_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/api"
	"github.com/progresshans/godj/api/input"
	article "github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/serializers"
)

func TestNormalizedModelBodyPreservesCompleteIRAndTypedPreparation(t *testing.T) {
	metadata := (article.ArticleDescriptor{}).Metadata()
	fields := []serializers.ModelField{{Name: "title", Normalization: serializers.StringUnicode16NFKC, MaxInputBytes: 600}, {Name: "published"}}
	spec := require[serializers.Spec](t)(serializers.FromModel(metadata, fields...))
	fields[0].Name = "id"
	source, found := spec.Model()
	if !found || !source.Equal(metadata) {
		t.Fatal("normalization changed storage source")
	}
	source.Fields[0].Name = "changed"
	body := require[input.Body[orm.ModelInput[article.Article]]](t)(input.NewModel(article.ArticleObjects, spec, api.ParserConfig{}))
	for _, test := range []struct{ name, title, code string }{
		{"contracting", strings.Repeat("e\u0301", 200), ""},
		{"expanding", strings.Repeat("ﬃ", 67), "max_length"},
		{"raw_bytes", " " + strings.Repeat("e\u0301", 200), "max_bytes"},
	} {
		t.Run(test.name, func(t *testing.T) {
			object := require[serializers.Object](t)(serializers.NewObject(serializers.MemberOf("title", serializers.String(test.title))))
			value, failures, err := body.Bind(t.Context(), object, serializers.ModeFull)
			if err != nil {
				t.Fatal(err)
			}
			if test.code != "" {
				items := failures.All()
				if !reflect.DeepEqual(value, orm.ModelInput[article.Article]{}) || len(items) != 1 || string(items[0].Code()) != test.code {
					t.Fatal("invalid typed input published", failures)
				}
				return
			}
			if !failures.Empty() {
				t.Fatal(failures)
			}
			prepared := require[orm.PreparedModel[article.Article]](t)(article.ArticleObjects.PrepareInput(value, nil))
			model := require[article.Article](t)(prepared.Model())
			if model.Title != strings.Repeat("é", 200) || model.Published {
				t.Fatal("typed preparation lost normalized value/default")
			}
			if published, present := value.Get("published"); !present || !published.Equal(query.Boolean(false)) {
				t.Fatal("default source lost")
			}
		})
	}
	empty := require[serializers.Object](t)(serializers.NewObject())
	partial, failures, err := body.Bind(t.Context(), empty, serializers.ModePartial)
	if err != nil || !failures.Empty() || len(partial.Values()) != 0 {
		t.Fatal("partial normalization invented input", err)
	}
	generic := require[serializers.Spec](t)(serializers.NewSpec(spec.Fields()))
	if _, err := input.NewModel(article.ArticleObjects, generic, api.ParserConfig{}); err == nil {
		t.Fatal("normalized fields forged model source")
	}
	foreign := metadata.Clone()
	foreign.DBTable += "_other"
	foreignSpec := require[serializers.Spec](t)(serializers.FromModel(foreign, serializers.ModelField{Name: "title", Normalization: serializers.StringUnicode16NFKC}))
	if _, err := input.NewModel(article.ArticleObjects, foreignSpec, api.ParserConfig{}); err == nil {
		t.Fatal("foreign normalized source accepted")
	}
}

func TestStringInputPolicyNeverRewritesTypedModelOutput(t *testing.T) {
	metadata := (article.ArticleDescriptor{}).Metadata()
	spec := require[serializers.Spec](t)(serializers.FromModel(metadata, serializers.ModelField{Name: "title", ReadOnly: true, Normalization: serializers.StringUnicode16NFKC, MaxInputBytes: 1}))
	encoder := require[serializers.ModelEncoder[article.Article]](t)(serializers.NewModelEncoder(spec, metadata, (article.ArticleDescriptor{}).WriteFieldValue))
	value := require[serializers.Value](t)(encoder.Encode(article.Article{Title: "Ｆｒｅｄ"}))
	object, _ := value.AsObject()
	title, _ := object.Get("title")
	text, _ := title.AsString()
	if text != "Ｆｒｅｄ" {
		t.Fatal("output used input normalization/byte limit")
	}
	for _, selection := range []serializers.ModelField{
		{Name: "published", Normalization: serializers.StringUnicode16NFKC},
		{Name: "published", MaxInputBytes: 1},
		{Name: "title", Normalization: "private-invalid"},
		{Name: "title", MaxInputBytes: -1},
	} {
		if _, err := serializers.FromModel(metadata, selection); err == nil {
			t.Fatal("invalid model input policy accepted")
		}
	}
}
