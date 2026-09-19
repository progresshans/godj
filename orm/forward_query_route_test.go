package orm

import (
	"errors"
	"testing"

	"github.com/progresshans/godj/query"
)

func TestForwardQueryRouteRejectsTamperedSnapshotMetadata(t *testing.T) {
	post, author, _, _ := bindRelationObjectTestFixture(t)
	bound, err := BindForward(post, "author", author)
	if err != nil {
		t.Fatal(err)
	}
	for _, variant := range []string{"field", "target key", "source model", "project"} {
		t.Run(variant, func(t *testing.T) {
			changed := bound
			changed.route.steps = append([]forwardRelationState(nil), bound.route.steps...)
			step := &changed.route.steps[0]
			switch variant {
			case "field":
				step.metadata.Field = "reviewer"
			case "target key":
				step.targetPrimaryKey.Column = "other_id"
			case "source model":
				step.sourceModel = step.sourceModel.Clone()
				step.sourceModel.DBTable = "other_post"
			case "project":
				step.snapshot = nil
			}
			_, err := changed.String(NewStringField[relationObjectTestAuthor](author.model.Fields[1]))
			if !errors.Is(err, &query.Error{Code: query.CodeInvalidPlan}) {
				t.Fatalf("tampered route: %v", err)
			}
			if _, err := bound.String(NewStringField[relationObjectTestAuthor](author.model.Fields[1])); err != nil {
				t.Fatal("tampering leaked to original", err)
			}
		})
	}
}
