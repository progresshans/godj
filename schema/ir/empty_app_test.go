package ir_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/progresshans/godj/schema"
	"github.com/progresshans/godj/schema/ir"
)

func TestEmptyAppHasCanonicalIdentityWithoutInventingAModel(t *testing.T) {
	t.Parallel()
	app, err := schema.Build(schema.Definition{AppLabel: "notifications"})
	if err != nil {
		t.Fatal(err)
	}
	if app.AppLabel != "notifications" || app.FormatVersion != ir.CurrentFormatVersion || len(app.Models) != 0 {
		t.Fatalf("empty declaration acquired a model or lost identity: %#v", app)
	}
	canonical, err := ir.CanonicalJSON(app)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(canonical, []byte(`"models":[]`)) {
		t.Fatalf("empty model list is not canonical: %s", canonical)
	}
	app.Models = nil
	nilCanonical, err := ir.CanonicalJSON(app)
	if err != nil || !bytes.Equal(canonical, nilCanonical) || app.Models != nil {
		t.Fatalf("nil and empty differ or caller was changed: %s, %v", nilCanonical, err)
	}
	emptyHash, err := ir.Hash(app)
	if err != nil {
		t.Fatal(err)
	}
	other := app.Clone()
	other.AppLabel = "tasks"
	otherHash, err := ir.Hash(other)
	if err != nil || otherHash == emptyHash {
		t.Fatalf("empty app label did not affect identity: %s, %v", otherHash, err)
	}
	app.Models = []ir.Model{{Name: "notification", GoName: "Notification"}}
	grown, grownHash, err := ir.NormalizeAndHash(app)
	if err != nil || grownHash == emptyHash || len(grown.Models) != 1 || len(grown.Models[0].Fields) != 1 {
		t.Fatalf("first model did not produce its normal implicit key: %#v, %v", grown, err)
	}
}

func TestEmptyAppStillRejectsInvalidIdentityAndFormat(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, path string
		input      ir.Schema
	}{
		{"missing_label", "app_label", ir.Schema{FormatVersion: ir.CurrentFormatVersion}},
		{"invalid_label", "app_label", ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "bad-label"}},
		{"old_format", "format_version", ir.Schema{AppLabel: "notifications"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, hash, err := ir.NormalizeAndHash(test.input)
			var validation *ir.ValidationError
			if !errors.As(err, &validation) || validation.Path != test.path || got.AppLabel != "" || hash != "" {
				t.Fatalf("invalid empty app returned partial success: %#v, %q, %v", got, hash, err)
			}
		})
	}
}
