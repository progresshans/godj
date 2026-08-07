// Package ir defines GoDj's normalized, versioned schema intermediate
// representation. It is the canonical input to code generation and runtime
// metadata; declaration packages and generated packages do not import each
// other.
package ir

const FormatVersion = 1

type Schema struct {
	FormatVersion int     `json:"format_version"`
	AppLabel      string  `json:"app_label"`
	Models        []Model `json:"models"`
}

type Model struct {
	Name    string  `json:"name"`
	GoName  string  `json:"go_name"`
	DBTable string  `json:"db_table"`
	Fields  []Field `json:"fields"`
}

type FieldKind string

const (
	FieldAuto    FieldKind = "auto"
	FieldChar    FieldKind = "char"
	FieldBoolean FieldKind = "boolean"
)

type Field struct {
	Name       string    `json:"name"`
	GoName     string    `json:"go_name"`
	Column     string    `json:"column"`
	Kind       FieldKind `json:"kind"`
	PrimaryKey bool      `json:"primary_key"`
	Nullable   bool      `json:"nullable"`
	MaxLength  int       `json:"max_length,omitempty"`
}

func (s Schema) Clone() Schema {
	clone := s
	clone.Models = make([]Model, len(s.Models))
	for index := range s.Models {
		clone.Models[index] = s.Models[index].Clone()
	}
	return clone
}

func (m Model) Clone() Model {
	clone := m
	clone.Fields = append([]Field(nil), m.Fields...)
	return clone
}
