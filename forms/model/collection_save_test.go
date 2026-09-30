package model_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/forms"
	formmodel "github.com/progresshans/godj/forms/model"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/schema/ir"
)

type nilTicketDefaults struct{}

func (*nilTicketDefaults) BuildManyToManyCreate(ir.Field, ir.Field, int64, int64) orm.Mutation[models.TicketLabel] {
	panic("typed nil through input invoked")
}

type saverTicketDescriptor struct {
	models.TicketDescriptor
	metadata ir.Model
}

func (d saverTicketDescriptor) Metadata() ir.Model { return d.metadata.Clone() }

func TestBuiltInCollectionSaverRejectsConstructionBeforeCallbacks(t *testing.T) {
	relations, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := formmodel.SaveManyToMany(orm.ManyToMany[models.Ticket, models.Label, models.TicketLabel]{}); err == nil {
		t.Fatal("zero relation accepted")
	}
	if _, err := formmodel.SaveManyToMany(relations.ModelsLabelTickets); err == nil {
		t.Fatal("reverse accessor accepted as model field")
	}
	if _, err := formmodel.SaveManyToMany(relations.ModelsTicketLabels, orm.ManyToManySetOptions[models.TicketLabel]{}, orm.ManyToManySetOptions[models.TicketLabel]{}); err == nil {
		t.Fatal("duplicate options accepted")
	}
	var defaults *nilTicketDefaults
	if _, err := formmodel.SaveManyToMany(relations.ModelsTicketLabels, orm.ManyToManySetOptions[models.TicketLabel]{ThroughDefaults: defaults}); err == nil {
		t.Fatal("typed nil through input accepted")
	}
	saver, err := formmodel.SaveManyToMany(relations.ModelsTicketLabels)
	if err != nil || saver.Field != "labels" || saver.Save == nil {
		t.Fatal("declaration not used", err)
	}
	for _, format := range []string{"%v", "%+v", "%#v"} {
		if text := fmt.Sprintf(format, saver); text != "model.CollectionSaver{redacted}" {
			t.Fatal("collection saver disclosed metadata", text)
		}
	}
}

func TestBuiltInCollectionSaverOwnsModelAndFieldBindingBeforeScalarWrite(t *testing.T) {
	relations, err := project.BindCollections()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "renamed_field", "other_model", "changed_policy", "changed_target", "changed_through"} {
		t.Run(mode, func(t *testing.T) {
			saver, err := formmodel.SaveManyToMany(relations.ModelsTicketLabels)
			if err != nil {
				t.Fatal(err)
			}
			metadata, field, err := relations.ModelsTicketLabels.Declaration()
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "renamed_field":
				saver.Field = "reviewers"
			case "other_model":
				metadata.DBTable = "other_ticket"
			case "changed_policy":
				metadata.Fields[1].Blank = !metadata.Fields[1].Blank
			case "changed_target":
				metadata.ManyToMany[0].Target.ModelName = "other_label"
			case "changed_through":
				metadata.ManyToMany[0].Through.SourceField = "other_source"
			}
			manager := orm.NewManager[models.Ticket](saverTicketDescriptor{metadata: metadata})
			spec, err := formmodel.NewSpecForFields(metadata, []string{"subject", "labels"})
			if err != nil {
				t.Fatal(err)
			}
			spec, err = spec.WithModelChoices("labels", forms.Choice{Value: forms.Integer(7), Label: "Allowed"})
			if err != nil {
				t.Fatal(err)
			}
			current := models.Ticket{CategoryID: 3}
			form, err := formmodel.BindInstance(t.Context(), manager, spec, forms.NewData(map[string][]string{"subject": {"candidate"}, "labels": {"7"}}), &current, formmodel.PostClean{})
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := form.Prepare()
			if err != nil {
				t.Fatal(err)
			}
			value, err := prepared.Model()
			if err != nil {
				t.Fatal(err)
			}
			called := 0
			saver.Save = func(context.Context, db.Session, models.Ticket, []int64) error { called++; return nil }
			b := &formSaveBackend{}
			err = prepared.Save(t.Context(), b, &value, saver)
			if mode == "valid" {
				if err != nil || called != 1 || !reflect.DeepEqual(b.events, []string{"insert"}) {
					t.Fatal("valid callback rejected", err, b.events)
				}
			} else {
				var invalid *formmodel.Error
				if !errors.As(err, &invalid) || invalid.Code != "binding_mismatch" || called != 0 || len(b.events) != 0 {
					t.Fatal("mismatched relation reached scalar writer", err, called, b.events)
				}
			}
			metadata.Fields[1].Name = "caller-edit"
			field.Name = "caller-edit"
			original, originalField, err := relations.ModelsTicketLabels.Declaration()
			if err != nil || originalField.Name != "labels" || strings.Contains(fmt.Sprint(original.Fields), "caller-edit") {
				t.Fatal("declaration leaked mutable metadata", err)
			}
		})
	}
}
