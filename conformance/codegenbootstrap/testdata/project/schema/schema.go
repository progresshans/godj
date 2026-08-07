package schema

// Model and Field are deliberately tiny fixture types. They demonstrate that
// declaration data can be compiled without importing generated model types.
type Model struct {
	Name   string
	Fields []Field
}

type Field struct {
	Name   string
	GoType string
}

func Define(name string, fields ...Field) Model {
	return Model{Name: name, Fields: fields}
}

func String(name string) Field {
	return Field{Name: name, GoType: "string"}
}
