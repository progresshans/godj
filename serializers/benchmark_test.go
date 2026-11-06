package serializers_test

import (
	"testing"

	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/serializers"
)

// Compare reusable projection to explicitly preparing the same metadata for
// every row. This measures the ownership choice, not an old implementation.
func BenchmarkModelEncoding(b *testing.B) {
	descriptor := models.TicketDescriptor{}
	metadata := descriptor.Metadata()
	spec, err := serializers.FromModel(metadata, serializers.ModelField{Name: "id"}, serializers.ModelField{Name: "subject"}, serializers.ModelField{Name: "details"}, serializers.ModelField{Name: "closed"})
	if err != nil {
		b.Fatal(err)
	}
	encoder, err := serializers.NewModelEncoder(spec, metadata, descriptor.WriteFieldValue)
	if err != nil {
		b.Fatal(err)
	}
	value := models.Ticket{ID: 1, Subject: "Example", CategoryID: 1}
	b.Run("prepared", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := encoder.Encode(value); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("prepare_each_row", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			encoder, err := serializers.NewModelEncoder(spec, descriptor.Metadata(), descriptor.WriteFieldValue)
			if err != nil {
				b.Fatal(err)
			}
			if _, err := encoder.Encode(value); err != nil {
				b.Fatal(err)
			}
		}
	})
}
