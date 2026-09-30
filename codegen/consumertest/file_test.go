package codegen_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/conformance/s3fixture"
	"github.com/progresshans/godj/schema"
)

func TestGeneratedFileConsumer(t *testing.T) {
	s, err := schema.Build(schema.Definition{AppLabel: "file_reference", Models: []schema.Model{
		{Name: "document", GoName: "Document", Fields: []schema.Field{
			schema.CharField("title", "Title", 40, schema.Unique()),
			schema.FileField("file", "File", schema.MaxLength(40), schema.Blank(), schema.Default(""), schema.Unique()),
			schema.FileField("optional", "Optional", schema.Nullable(), schema.Blank()),
			schema.CharField("owner", "Owner", 40, schema.Default("alice")),
		}},
		{Name: "photograph", GoName: "Photograph", Fields: []schema.Field{
			schema.CharField("title", "Title", 40, schema.Unique()),
			schema.ImageField("photo", "Photo", schema.Nullable(), schema.Blank(), schema.ImageDimensions("width", "height")),
			schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
		}},
		{Name: "raw_image", GoName: "RawImage", Fields: []schema.Field{schema.ImageField("image", "Image")}},
		{Name: "strict_image", GoName: "StrictImage", Fields: []schema.Field{
			schema.ImageField("photo", "Photo", schema.Blank(), schema.ImageDimensions("width", "height")),
			schema.IntegerField("width", "Width"), schema.IntegerField("height", "Height"),
		}},
		{Name: "asset_reference", GoName: "AssetReference", Fields: []schema.Field{
			schema.CharField("title", "Title", 40),
			schema.FileField("reference", "Reference", schema.Blank(), schema.Default("files/a.txt"), schema.Choices(schema.Choice("files/a.txt", "A"), schema.Choice("files/b.txt", "B"))),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "asset_reference_name", Fields: []string{"title", "reference"}}}},
		{Name: "selected_image", GoName: "SelectedImage", Fields: []schema.Field{
			schema.CharField("title", "Title", 40),
			schema.ImageField("photo", "Photo", schema.Nullable(), schema.Blank(), schema.ImageDimensions("width", "height"), schema.Choices(schema.Choice("images/a.png", "A"), schema.Choice("images/b.png", "B"))),
			schema.IntegerField("width", "Width", schema.Nullable()), schema.IntegerField("height", "Height", schema.Nullable()),
		}, UniqueConstraints: []schema.UniqueConstraint{{Name: "selected_image_name", Fields: []string{"title", "photo"}}}},
		{Name: "photo_link", GoName: "PhotoLink", Fields: []schema.Field{schema.ForeignKey("photograph", "PhotographID", schema.Target("file_reference", "photograph"), schema.RelatedName("links"), schema.Protect)}},
		{Name: "archive", GoName: "Archive", Fields: []schema.Field{schema.FileField("reference", "Reference")}},
		{Name: "link", GoName: "Link", Fields: []schema.Field{schema.ForeignKey("document", "DocumentID", schema.Target("file_reference", "document"), schema.RelatedName("links"), schema.Protect)}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	const module = "example.com/godj-files"
	spec := codegen.ProjectSpec{Project: codegen.PackageSpec{PackageName: "project", ImportPath: module + "/project", Directory: "project"}, Apps: []codegen.AppSpec{{Alias: "models", Package: codegen.PackageSpec{PackageName: "models", ImportPath: module + "/models", Directory: "models"}, Schema: s}}}
	bundle, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	again, err := codegen.GenerateProject(spec)
	if err != nil {
		t.Fatal(err)
	}
	files, repeated := bundle.Files(), again.Files()
	if len(files) == 0 || len(files) != len(repeated) {
		t.Fatal("incomplete generated file bundle")
	}
	root := newGeneratedModule(t, module)
	for i, file := range files {
		if file.Path != repeated[i].Path || !bytes.Equal(file.Source(), repeated[i].Source()) {
			t.Fatal("FileField output is not deterministic")
		}
		writeGeneratedTestFile(t, root, file.Path, file.Source())
	}
	for _, name := range []string{"consumer_test.go", "formset_test.go", "serving_test.go", "image_test.go", "stored_image_test.go", "image_codec_test.go", "file_choice_test.go"} {
		consumer, err := os.ReadFile("testdata/files/" + name)
		if err != nil {
			t.Fatal(err)
		}
		writeGeneratedTestFile(t, root, "consumer/"+name, consumer)
	}
	reference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "forms/model/testdata/model-file-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/reference.json", reference)
	imageReference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "forms/model/testdata/model-image-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/image-reference.json", imageReference)
	storedImageReference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "storage/model/testdata/stored-image-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/stored-image-reference.json", storedImageReference)
	imageCodecs, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "uploads/testdata/image-codecs-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/image-codec-reference.json", imageCodecs)
	apngReference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "uploads/testdata/apng-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/apng-reference.json", apngReference)
	webpReference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "uploads/testdata/webp-animation-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/webp-reference.json", webpReference)
	choiceReference, err := os.ReadFile(filepath.Join(codegenRepositoryRoot(t), "forms/model/testdata/file-choice-django61.json"))
	if err != nil {
		t.Fatal(err)
	}
	writeGeneratedTestFile(t, root, "consumer/file-choice-reference.json", choiceReference)
	required := []string{"TestGeneratedFileProjection", "TestGeneratedFileStorageAndHistory", "TestGeneratedFileStorageAndHistory/sqlite", "TestGeneratedFileStorageAndHistory/sqlite/formset", "TestGeneratedFileStorageAndHistory/sqlite/serving", "TestGeneratedFileStorageAndHistory/sqlite/serving/filesystem", "TestGeneratedFileStorageAndHistory/sqlite/serving/memory"}
	if strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) != "" {
		required = append(required, "TestGeneratedFileStorageAndHistory/postgres", "TestGeneratedFileStorageAndHistory/postgres/formset", "TestGeneratedFileStorageAndHistory/postgres/serving", "TestGeneratedFileStorageAndHistory/postgres/serving/filesystem", "TestGeneratedFileStorageAndHistory/postgres/serving/memory")
	}
	backends := []string{"filesystem", "memory"}
	if s3fixture.Enabled(t) {
		backends = append(backends, "s3")
	}
	for _, database := range []string{"sqlite", "postgres"} {
		if database == "postgres" && strings.TrimSpace(os.Getenv("GODJ_TEST_POSTGRES_URL")) == "" {
			continue
		}
		for _, backend := range backends {
			required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/images/"+backend+"/reference_choices", "TestGeneratedFileStorageAndHistory/"+database+"/images/"+backend+"/reference_choices/formset_unique")
			required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/serving/"+backend)
			required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/images/"+backend, "TestGeneratedFileStorageAndHistory/"+database+"/images/"+backend+"/formset")
			required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/images/"+backend+"/stored_inspection")
			for _, codec := range []string{"bmp_palette", "dib_rgb", "tiff_pages", "tiff_bigendian16", "tiff_tile", "apng_rgba", "apng_poster", "apng_palette4", "apng_adam7", "webp_lossy", "webp_lossless", "webp_compressed_alpha", "webp_mixed"} {
				required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/images/"+backend+"/codecs/"+codec)
			}
			required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/serving/"+backend+"/ranges_and_conditionals")
			if backend == "s3" {
				required = append(required, "TestGeneratedFileStorageAndHistory/"+database+"/serving/s3/signed_download_admission")
			}
		}
	}
	command := generatedGoCommand(t.Context(), root, "test", "-json", "-mod=mod", "./consumer")
	assertGeneratedConsumerTests(t, runStrictGeneratedCommand(t, command), required...)
}
