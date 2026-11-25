package migrationautodetect

import (
	"reflect"
	"testing"

	"github.com/progresshans/godj/migrations"
)

func TestDetectEmptyInstalledAppsHaveNoTableHistoryUntilFirstModel(t *testing.T) {
	empty := mustProjectState(t, testSchema("site"), testSchema("notes"))
	request := Request{Definitions: mustLoadDefinitions(t), Desired: empty, ManagedApps: []string{"site", "notes"}}
	plan := mustDetect(t, request)
	if !plan.Empty() || !plan.BaseState().Equal(migrations.EmptyProjectState()) || !reflect.DeepEqual(request.Desired.Apps(), []string{"notes", "site"}) {
		t.Fatal("empty app fabricated history or lost declaration identity")
	}
	request.Desired = mustProjectState(t, testSchema("site"), testSchema("notes", testModel("note", testChar("title", false, nil))))
	plan = mustDetect(t, request)
	generated := plan.Migrations()
	if len(generated) != 1 || generated[0].App != "notes" || generated[0].Name != "0001_initial" {
		t.Fatal("first model did not create only its own app migration", generated)
	}
	request.Definitions = mustLoadDefinitions(t, generated...)
	if repeated := mustDetect(t, request); !repeated.Empty() {
		t.Fatal("empty peer app created repeated drift")
	}
	request.Desired = empty
	retired := mustDetect(t, request).Migrations()
	if len(retired) != 1 || retired[0].App != "notes" || len(retired[0].Operations) != 1 {
		t.Fatal("expected only the declared note model to be retired", retired)
	}
	if drop, ok := retired[0].Operations[0].(migrations.DeleteModel); !ok || drop.Model.Name != "note" || drop.BeforeModel != "" {
		t.Fatal("wrong final model deletion", retired)
	}
	request.Definitions = mustLoadDefinitions(t, generated[0], retired[0])
	if repeated := mustDetect(t, request); !repeated.Empty() {
		t.Fatal("empty installed app fabricated another migration after its last model was removed")
	}
	request.Definitions = mustLoadDefinitions(t, generated...)
	request.Desired = mustProjectState(t, testSchema("site"))
	detectError(t, request, CodeUnsupportedChange)
}
