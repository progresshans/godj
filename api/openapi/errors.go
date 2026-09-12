package openapi

// JSONErrorSchema describes the stable API error envelope, including ordered
// field diagnostics and presentation-independent string parameters. Internal
// Web failures have a separate text response and never use this schema.
func JSONErrorSchema() (Schema, error) {
	parameter, err := Object(
		Property{Name: "key", Schema: String(), Required: true},
		Property{Name: "value", Schema: String(), Required: true},
	)
	if err != nil {
		return Schema{}, err
	}
	parameters, err := Array(parameter)
	if err != nil {
		return Schema{}, err
	}
	diagnostic, err := Object(
		Property{Name: "field", Schema: String(), Required: true},
		Property{Name: "code", Schema: String(), Required: true},
		Property{Name: "params", Schema: parameters, Required: true},
	)
	if err != nil {
		return Schema{}, err
	}
	diagnostics, err := Array(diagnostic)
	if err != nil {
		return Schema{}, err
	}
	return Object(
		Property{Name: "code", Schema: String(), Required: true},
		Property{Name: "errors", Schema: diagnostics, Required: true},
	)
}
