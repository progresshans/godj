package relationstate

import "testing"

func TestSeedOwnsRowsAndNullableKeys(t *testing.T) {
	t.Parallel()
	left, right := Seed(), Seed()
	left.Authors[0].Name = "changed"
	left.Posts[0].Title = "changed"
	*left.Posts[0].ReviewerID = 99
	if right.Authors[0].Name != "Ada" || right.Posts[0].Title != "Alpha" || *right.Posts[0].ReviewerID != 2 {
		t.Fatalf("mutating one fixture changed another: %#v", right)
	}
	copy := CloneIntegerPointer(right.Posts[0].ReviewerID)
	*copy = 100
	if *right.Posts[0].ReviewerID != 2 || CloneIntegerPointer(nil) != nil {
		t.Fatal("snapshot key copy does not preserve independent nullable storage")
	}
}
