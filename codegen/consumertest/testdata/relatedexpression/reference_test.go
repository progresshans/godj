package consumer

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
)

func TestRelatedReference(t *testing.T) {
	withRelatedBackends(t, func(t *testing.T, backend relatedBackend, vendor string, _ relatedPhysicalWriter) {
		data, err := os.ReadFile("django61-" + vendor + ".json")
		check(t, err)
		var reference struct {
			Cases []struct {
				Name   string
				Result json.RawMessage
			}
		}
		check(t, json.Unmarshal(data, &reference))
		seedRelated(t, backend)
		before := relatedStorage(t, backend)
		for _, record := range reference.Cases {
			t.Run(record.Name, func(t *testing.T) {
				assertRelatedJSON(t, observeRelated(t, backend, record.Name), string(record.Result))
			})
		}
		if !reflect.DeepEqual(before, relatedStorage(t, backend)) {
			t.Fatal("related reference observations changed storage")
		}
	})
}
