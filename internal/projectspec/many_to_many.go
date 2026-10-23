package projectspec

import (
	"fmt"

	"github.com/progresshans/godj/schema/ir"
)

func validateManyToManyResources(budget *resourceBudget, app, path string, model ir.Model, storageModels *int) error {
	if len(model.ManyToMany) > MaxFieldsPerModel || len(model.Fields) > MaxFieldsPerModel-len(model.ManyToMany) {
		return resourceError(path+".many_to_many", "fields_per_model", MaxFieldsPerModel, len(model.Fields)+len(model.ManyToMany))
	}
	if err := consumeFields(budget, path+".many_to_many", uint64(len(model.ManyToMany))); err != nil {
		return err
	}
	for index, field := range model.ManyToMany {
		p := fmt.Sprintf("%s.many_to_many[%d]", path, index)
		if err := consumeNodes(budget, p, 3); err != nil {
			return err
		}
		values := []string{field.Name, field.GoName, field.Target.AppLabel, field.Target.ModelName, field.Reverse.Name, string(field.Symmetry)}
		for _, value := range values {
			if err := validateString(p, value); err != nil {
				return err
			}
		}
		if through := field.Through; through != nil {
			if err := consumeNodes(budget, p+".through", 2); err != nil {
				return err
			}
			values = append(values, through.Model.AppLabel, through.Model.ModelName, through.SourceField, through.TargetField)
		} else {
			// Charge the derived model, three columns, FK metadata and pair
			// constraint before StorageSchema can allocate them.
			*storageModels++
			if *storageModels > MaxModelsPerApp {
				return resourceError(p, "storage_models_per_app", MaxModelsPerApp, *storageModels)
			}
			if err := consumeFields(budget, p+".storage", 3); err != nil {
				return err
			}
			if err := consumeNodes(budget, p+".storage", 13); err != nil {
				return err
			}
			table := model.DBTable
			if table == "" {
				table = app + "_" + model.Name
			}
			values = append(values, model.Name+"_"+field.Name, model.GoName+field.GoName+"Link", table+"_"+field.Name)
		}
		for _, value := range values {
			if err := validateString(p, value); err != nil {
				return err
			}
		}
	}
	return nil
}
