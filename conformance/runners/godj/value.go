package godj

import (
	"strconv"

	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/examples/article/models"
	"github.com/progresshans/godj/schema/ir"
)

func valuePointer(value protocol.Value) *protocol.Value {
	return &value
}

func boolPointer(value bool) *bool {
	return &value
}

func articleList(articles []models.Article) protocol.Value {
	values := make([]protocol.Value, len(articles))
	for index := range articles {
		values[index] = articleValue(articles[index])
	}
	return protocol.List(values...)
}

func articleValue(article models.Article) protocol.Value {
	summary := protocol.Null()
	if article.Summary != nil {
		summary = protocol.String(*article.Summary)
	}
	return protocol.Object(map[string]protocol.Value{
		"id":        protocol.PrimaryKey(protocol.Integer(strconv.FormatInt(article.ID, 10))),
		"published": protocol.Boolean(article.Published),
		"summary":   summary,
		"title":     protocol.String(article.Title),
	})
}

func databaseState(articles []models.Article) protocol.Value {
	return protocol.Object(map[string]protocol.Value{
		"articles": articleList(articles),
	})
}

func metadataValue(metadata ir.Model) (protocol.Value, error) {
	fields := make([]protocol.Value, len(metadata.Fields))
	for index, field := range metadata.Fields {
		internalType, err := djangoInternalType(field.Kind)
		if err != nil {
			return protocol.Value{}, err
		}
		maxLength := protocol.Null()
		if field.MaxLength > 0 {
			maxLength = protocol.Integer(strconv.Itoa(field.MaxLength))
		}
		fields[index] = protocol.Object(map[string]protocol.Value{
			"internal_type": protocol.String(internalType),
			"max_length":    maxLength,
			"name":          protocol.String(field.Name),
			"null":          protocol.Boolean(field.Nullable),
			"primary_key":   protocol.Boolean(field.PrimaryKey),
		})
	}
	return protocol.Object(map[string]protocol.Value{
		"db_table":   protocol.String(metadata.DBTable),
		"fields":     protocol.List(fields...),
		"model_name": protocol.String(metadata.Name),
	}), nil
}

func djangoInternalType(kind ir.FieldKind) (string, error) {
	switch kind {
	case ir.FieldAuto:
		return "AutoField", nil
	case ir.FieldChar:
		return "CharField", nil
	case ir.FieldBoolean:
		return "BooleanField", nil
	default:
		return "", &unsupportedMetadataFieldError{kind: kind}
	}
}

type unsupportedMetadataFieldError struct {
	kind ir.FieldKind
}

func (e *unsupportedMetadataFieldError) Error() string {
	return "unsupported schema metadata field kind " + strconv.Quote(string(e.kind))
}
