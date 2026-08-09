package meta

type ModelKey struct {
	App   string
	Model string
}

type Relation struct {
	Field    string
	Column   string
	Target   ModelKey
	Nullable bool
	Delete   string
	Reverse  string
}

type ModelDescriptor struct {
	Key       ModelKey
	Relations []Relation
}

type BoundRelation struct {
	Source      ModelKey
	Field       string
	Column      string
	Target      ModelKey
	Nullable    bool
	Delete      string
	ReverseName string
}
