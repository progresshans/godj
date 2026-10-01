package orm

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/schema/ir"
)

func BenchmarkWideModelWrite(b *testing.B) {
	for _, count := range []int{4, 32, 256} {
		b.Run(fmt.Sprintf("fields_%d", count), func(b *testing.B) {
			descriptor := wideWriteDescriptor{
				metadata: ir.Model{Name: "wide", GoName: "Wide", DBTable: "bench_wide", Fields: []ir.Field{{Name: "id", Column: "id", GoName: "ID", Kind: ir.FieldAuto, PrimaryKey: true}}},
				index:    make(map[string]int, count),
			}
			value := wideWriteModel{key: 1, values: make([]string, count)}
			for index := range count {
				name := fmt.Sprintf("field_%d", index)
				descriptor.metadata.Fields = append(descriptor.metadata.Fields, ir.Field{Name: name, Column: name, GoName: fmt.Sprintf("Field%d", index), Kind: ir.FieldChar, MaxLength: 128})
				descriptor.index[name] = index
				value.values[index] = "value"
			}
			manager := NewManager[wideWriteModel](descriptor)
			input := wideWriteInput{metadata: descriptor.metadata, value: value}
			for _, operation := range []string{"Create", "Update", "Save", "SaveMask", "NewManager"} {
				b.Run(operation, func(b *testing.B) {
					names := make([]string, count)
					for index, field := range descriptor.metadata.Fields[1:] {
						names[index] = field.Name
					}
					mask := UpdateFieldNames[wideWriteModel](names...)
					b.ReportAllocs()
					for b.Loop() {
						var err error
						switch operation {
						case "Create":
							_, err = manager.Create(context.Background(), wideWriteBackend{}, input)
						case "Update":
							_, err = manager.Update(context.Background(), wideWriteBackend{}, value, input)
						case "Save":
							current := value
							err = manager.Save(context.Background(), wideWriteBackend{}, &current)
						case "SaveMask":
							current := value
							err = manager.Save(context.Background(), wideWriteBackend{}, &current, mask)
						case "NewManager":
							if NewManager[wideWriteModel](descriptor).prepared == nil {
								b.Fatal("manager was not prepared")
							}
						}
						if err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

type wideWriteModel struct {
	key    int64
	values []string
}

type wideWriteDescriptor struct {
	metadata ir.Model
	index    map[string]int
}

func (d wideWriteDescriptor) Metadata() ir.Model                { return d.metadata }
func (wideWriteDescriptor) Scan(db.Row) (wideWriteModel, error) { return wideWriteModel{}, nil }
func (d wideWriteDescriptor) CloneModel(value wideWriteModel) wideWriteModel {
	return d.CloneWriteModel(value)
}
func (wideWriteDescriptor) CloneWriteModel(value wideWriteModel) wideWriteModel {
	value.values = slices.Clone(value.values)
	return value
}
func (wideWriteDescriptor) PrimaryKey(value wideWriteModel) (query.Value, bool) {
	return query.Integer(value.key), value.key != 0
}
func (wideWriteDescriptor) SetPrimaryKey(value *wideWriteModel, key int64) { value.key = key }
func (wideWriteDescriptor) ClearPrimaryKey(value *wideWriteModel)          { value.key = 0 }
func (d wideWriteDescriptor) WriteFieldValue(value wideWriteModel, field ir.Field) (query.Value, bool) {
	index, found := d.index[field.Name]
	if !found {
		return query.Value{}, false
	}
	return query.String(value.values[index]), true
}

type wideWriteInput struct {
	metadata ir.Model
	value    wideWriteModel
}

func (input wideWriteInput) assignments() []query.Assignment {
	assignments := make([]query.Assignment, len(input.value.values))
	for index, value := range input.value.values {
		assignments[index] = NewAssignment(input.metadata.Fields[index+1], query.String(value))
	}
	return assignments
}
func (input wideWriteInput) BuildCreate() Mutation[wideWriteModel] {
	return NewCreateMutation(input.value, input.metadata.DBTable, input.assignments())
}
func (input wideWriteInput) BuildPatch(current wideWriteModel) Mutation[wideWriteModel] {
	copy(current.values, input.value.values)
	return NewPatchMutation(current, input.metadata.DBTable, input.assignments())
}

type wideWriteBackend struct{}

func (wideWriteBackend) Insert(context.Context, query.InsertPlan) (int64, error) { return 1, nil }
func (wideWriteBackend) Update(context.Context, query.UpdatePlan) (int64, error) { return 1, nil }
func (wideWriteBackend) Delete(context.Context, query.DeletePlan) (int64, error) { return 1, nil }
