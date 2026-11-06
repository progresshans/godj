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
	detectError(t, request, CodeUnsupportedChange) // Empty cannot erase stored models.
}
