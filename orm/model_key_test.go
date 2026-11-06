package orm_test

import (
	"errors"
	"math"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/orm"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

type readKeyDescriptor struct {
	metadata ir.Model
	key      func(models.Article) (query.Value, bool)
}

func (d readKeyDescriptor) Metadata() ir.Model {
	if d.metadata.Name != "" {
		return d.metadata
	}
	return (models.ArticleDescriptor{}).Metadata()
}
func (readKeyDescriptor) Scan(row db.Row) (models.Article, error) {
	return (models.ArticleDescriptor{}).Scan(row)
}
func (readKeyDescriptor) CloneModel(value models.Article) models.Article {
	return (models.ArticleDescriptor{}).CloneModel(value)
}
func (d readKeyDescriptor) PrimaryKey(value models.Article) (query.Value, bool) {
	if d.key != nil {
		return d.key(value)
	}
	return (models.ArticleDescriptor{}).PrimaryKey(value)
}

type missingReadKeyDescriptor struct{}

func (missingReadKeyDescriptor) Metadata() ir.Model {
	return (models.ArticleDescriptor{}).Metadata()
}
func (missingReadKeyDescriptor) Scan(row db.Row) (models.Article, error) {
	return (models.ArticleDescriptor{}).Scan(row)
}
func (missingReadKeyDescriptor) CloneModel(value models.Article) models.Article {
	return (models.ArticleDescriptor{}).CloneModel(value)
}

func TestManagerPrimaryKeyReadsWithoutWriteCapabilityAndPreservesPresence(t *testing.T) {
	var descriptor orm.ModelDescriptor[models.Article] = readKeyDescriptor{}
	if _, writer := descriptor.(orm.WriteDescriptor[models.Article]); writer {
		t.Fatal("fixture accidentally provides write capability")
	}
	manager := orm.NewManager(descriptor)
	for _, key := range []int64{math.MinInt64, -4, 0, 1 << 60, math.MaxInt64} {
		value := models.NewArticleWithID(key)
		got, present, err := manager.PrimaryKey(value)
		if err != nil || !present || !got.Equal(query.Integer(key)) {
			t.Fatalf("key %d: present=%v err=%v", key, present, err)
		}
	}
	got, present, err := manager.PrimaryKey(models.Article{ID: 42})
	if err != nil || present || !got.IsNull() {
		t.Fatal("numeric field manufactured key presence", present, err)
	}
	if _, err := manager.ModelValues(models.NewArticleWithID(7)); err == nil {
		t.Fatal("read capability unexpectedly granted model writing")
	}
}

func TestManagerPrimaryKeyUsesOwnedValueAndConstructionMetadata(t *testing.T) {
	metadata := (models.ArticleDescriptor{}).Metadata()
	text := "caller-owned"
	value := models.NewArticleWithID(0)
	value.Summary = &text
	calls := 0
	descriptor := readKeyDescriptor{metadata: metadata, key: func(snapshot models.Article) (query.Value, bool) {
		calls++
		*snapshot.Summary = "descriptor-only"
		return query.Integer(snapshot.ID), true
	}}
	manager := orm.NewManager[models.Article](descriptor)
	metadata.Fields[0].Kind = ir.FieldChar
	metadata.Fields[0].PrimaryKey = false
	primary, err := manager.PrimaryKeyField()
	if err != nil || primary.Name != "id" || calls != 0 {
		t.Fatal("startup key validation invoked model callback or reread metadata", err)
	}
	primary.Name = "caller-mutated"
	if fresh, err := manager.PrimaryKeyField(); err != nil || fresh.Name != "id" {
		t.Fatal("key metadata accessor aliases manager", err)
	}
	key, present, err := manager.PrimaryKey(value)
	if err != nil || !present || !key.Equal(query.Integer(0)) || text != "caller-owned" || calls != 1 {
		t.Fatal("key reader borrowed metadata or model storage", err)
	}
}

func TestManagerPrimaryKeyRejectsInvalidMetadataAndDescriptorValues(t *testing.T) {
	base := (models.ArticleDescriptor{}).Metadata()
	for _, mode := range []string{"zero", "no_reader", "no_key", "two_keys", "integer_key", "nullable_key", "null_key", "text_key"} {
		t.Run(mode, func(t *testing.T) {
			metadata := base.Clone()
			descriptor := readKeyDescriptor{metadata: metadata}
			manager := orm.NewManager[models.Article](descriptor)
			switch mode {
			case "zero":
				manager = orm.Manager[models.Article]{}
			case "no_reader":
				manager = orm.NewManager[models.Article](missingReadKeyDescriptor{})
			case "no_key":
				descriptor.metadata.Fields[0].PrimaryKey = false
				manager = orm.NewManager[models.Article](descriptor)
			case "two_keys":
				descriptor.metadata.Fields[1].PrimaryKey = true
				manager = orm.NewManager[models.Article](descriptor)
			case "integer_key":
				descriptor.metadata.Fields[0].Kind = ir.FieldInteger
				manager = orm.NewManager[models.Article](descriptor)
			case "nullable_key":
				descriptor.metadata.Fields[0].Nullable = true
				manager = orm.NewManager[models.Article](descriptor)
			case "null_key":
				descriptor.key = func(models.Article) (query.Value, bool) { return query.Null(), true }
				manager = orm.NewManager[models.Article](descriptor)
			case "text_key":
				descriptor.key = func(models.Article) (query.Value, bool) { return query.String("private"), true }
				manager = orm.NewManager[models.Article](descriptor)
			}
			_, present, err := manager.PrimaryKey(models.NewArticleWithID(7))
			var diagnostic *query.Error
			if err == nil || present || !errors.As(err, &diagnostic) {
				t.Fatal("invalid key contract accepted", present, err)
			}
			_, metadataErr := manager.PrimaryKeyField()
			if (metadataErr == nil) != (mode == "null_key" || mode == "text_key") {
				t.Fatal("startup/read capability boundary differs", metadataErr)
			}
		})
	}
}
