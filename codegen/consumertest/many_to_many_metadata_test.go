package codegen_test

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/schema/ir"
)

func TestGeneratedManyToManyMetadataAndStorageAreOneProjectSnapshot(t *testing.T) {
	spec := projectBundleTestSpec()
	owner := &spec.Apps[0].Schema.Models[0]
	owner.ManyToMany = []ir.ManyToManyField{
		{Name: "fans", GoName: "Fans", Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Reverse: ir.ReverseRelation{Name: "fan_posts"}},
		{Name: "sponsors", GoName: "Sponsors", Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Reverse: ir.ReverseRelation{Name: "sponsored_posts"}, Through: &ir.ThroughModel{Model: ir.ModelIdentity{AppLabel: "blog", ModelName: "sponsorship"}, SourceField: "post", TargetField: "author"}},
		{Name: "related", GoName: "Related", Target: ir.ModelIdentity{AppLabel: "blog", ModelName: "blog_post"}},
		{Name: "follows", GoName: "Follows", Target: ir.ModelIdentity{AppLabel: "blog", ModelName: "blog_post"}, Symmetry: ir.ManyToManyDirected, Reverse: ir.ReverseRelation{Name: "followers"}},
	}
	spec.Apps[0].Schema.Models = append(spec.Apps[0].Schema.Models, ir.Model{Name: "sponsorship", GoName: "Sponsorship", Fields: []ir.Field{
		{Name: "post", GoName: "PostID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "blog", ModelName: "blog_post"}, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}},
		{Name: "author", GoName: "AuthorID", Kind: ir.FieldForeignKey, Relation: &ir.ForeignKeyRelation{Target: ir.ModelIdentity{AppLabel: "authors", ModelName: "author"}, Cardinality: ir.RelationManyToOne, OnDelete: ir.DeleteCascade, Reverse: ir.ReverseRelation{Disabled: true}}},
		{Name: "amount", GoName: "Amount", Kind: ir.FieldInteger},
	}})
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.SnapshotSHA256() != again.SnapshotSHA256() {
		t.Fatal("snapshot not deterministic")
	}
	for index, file := range bundle.Files() {
		if !bytes.Equal(file.Source(), again.Files()[index].Source()) {
			t.Fatal("generated drift", file.Path)
		}
	}
	directory := writeProjectBundleModule(t, bundle)
	writeGeneratedTestFile(t, directory, "consumer/many_test.go", []byte(`package consumer
import (
 "reflect"
 "testing"
 "github.com/progresshans/godj/orm"
 "github.com/progresshans/godj/schema/ir"
 "example.com/godj-project-bundle/blog"
 "example.com/godj-project-bundle/authors"
 "example.com/godj-project-bundle/project"
)
func TestActualGeneratedMetadata(t *testing.T) {
 binding,err:=project.Bind();if err!=nil{t.Fatal(err)}
 if len(binding.ManyToManyRelations())!=4{t.Fatal("collection lost")}
 if len(blog.GoDjRelationSchema().Models)!=2{t.Fatal("derived models leaked into declaration source")}
 owner:=blog.BlogPostDescriptor{}.Metadata()
 if len(owner.Fields)!=4 || len(owner.ManyToMany)!=4{t.Fatal("columnless declaration changed stored columns")}
 owner.ManyToMany[1].Through.SourceField="mutated"
 if (blog.BlogPostDescriptor{}).Metadata().ManyToMany[1].Through.SourceField!="post"{t.Fatal("descriptor metadata aliases through")}
 if _,err:=orm.BindModel(binding,ir.ModelIdentity{AppLabel:"blog",ModelName:"blog_post"},blog.BlogPostDescriptor{});err!=nil{t.Fatal(err)}
 if _,err:=orm.BindModel(binding,ir.ModelIdentity{AppLabel:"blog",ModelName:"blog_post_fans"},blog.BlogPostFansLinkDescriptor{});err!=nil{t.Fatal(err)}
 if _,err:=orm.BindModel(binding,ir.ModelIdentity{AppLabel:"blog",ModelName:"blog_post_related"},blog.BlogPostRelatedLinkDescriptor{});err!=nil{t.Fatal(err)}
 if _,err:=orm.BindModel(binding,ir.ModelIdentity{AppLabel:"blog",ModelName:"sponsorship"},blog.SponsorshipDescriptor{});err!=nil{t.Fatal(err)}
 link:=blog.BlogPostFansLinkDescriptor{}.Metadata();if len(link.Fields)!=3 || !reflect.DeepEqual(link.UniqueConstraints,[]ir.UniqueConstraint{{Name:"relation_pair",Fields:[]string{"source","target"}}}){t.Fatal("automatic physical layout",link)}
 changed:=blog.GoDjRelationSchema();changed.Models[0].ManyToMany[0].Reverse.Name="other"
 stale,err:=orm.BindProject(changed,authors.GoDjRelationSchema());if err!=nil{t.Fatal(err)}
 if _,err:=orm.BindModel(stale,ir.ModelIdentity{AppLabel:"blog",ModelName:"blog_post"},blog.BlogPostDescriptor{});err==nil{t.Fatal("stale collection metadata bound")}
}
`))
	output, err := generatedGoCommand(t.Context(), directory, "test", "-json", "./...").CombinedOutput()
	if err != nil {
		t.Fatalf("generated consumer failed: %v\n%s", err, output)
	}
	started, passed := 0, 0
	packages := map[string]bool{"example.com/godj-project-bundle/authors": false, "example.com/godj-project-bundle/blog": false, "example.com/godj-project-bundle/project": false, "example.com/godj-project-bundle/consumer": false}
	for _, line := range bytes.Split(bytes.TrimSpace(output), []byte{'\n'}) {
		var event struct{ Action, Test, Package string }
		if err := json.Unmarshal(line, &event); err != nil {
			t.Fatalf("invalid child events: %s", line)
		}
		if event.Action == "skip" && event.Test != "" || event.Action == "fail" {
			t.Fatalf("child test did not pass: %s", line)
		}
		if event.Test == "" && (event.Action == "pass" || event.Action == "skip") {
			if _, ok := packages[event.Package]; !ok {
				t.Fatal("unexpected child package", event.Package)
			}
			packages[event.Package] = true
		}
		if event.Test == "TestActualGeneratedMetadata" {
			if event.Action == "run" {
				started++
			}
			if event.Action == "pass" {
				passed++
			}
		}
	}
	if started != 1 || passed != 1 {
		t.Fatal("required generated consumer missing", started, passed)
	}
	for name, complete := range packages {
		if !complete {
			t.Fatal("child package did not complete", name)
		}
	}
	spec.Apps[0].Schema.Models[0].ManyToMany[1].Through.TargetField = "post"
	if failed, err := codegen.GenerateProject(spec); err == nil || len(failed.Files()) != 0 {
		t.Fatal("invalid through published generated prefix")
	}
}
