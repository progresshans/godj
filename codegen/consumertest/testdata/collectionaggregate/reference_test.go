package consumer

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestCollectionReference(t *testing.T) {
	withCollectionBackends(t, func(t *testing.T, backend collectionBackend, vendor string, _ collectionPhysicalWriter) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var reference struct {
			Cases []struct {
				Name   string
				Result json.RawMessage
			}
		}
		check(t, json.Unmarshal(data, &reference))
		seedCollection(t, backend)
		before := collectionStorage(t, backend)
		for _, record := range reference.Cases {
			t.Run(record.Name, func(t *testing.T) {
				assertCollectionJSON(t, observeCollection(t, backend, record.Name), string(record.Result))
			})
		}
		if !reflect.DeepEqual(before, collectionStorage(t, backend)) {
			t.Fatal("collection reference observations changed storage")
		}
	})
}
