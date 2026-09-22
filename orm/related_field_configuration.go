package orm

// Generated groups bind each concrete field through the typed route and keep
// any failure on that field. Scalar comparison and membership share this cause.
func (field RelatedIntegerField[M]) WithConfigurationError(err error) RelatedIntegerField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
func (field RelatedStringField[M]) WithConfigurationError(err error) RelatedStringField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
func (field RelatedBooleanField[M]) WithConfigurationError(err error) RelatedBooleanField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
func (field RelatedDateTimeField[M]) WithConfigurationError(err error) RelatedDateTimeField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

func (field RelatedDurationField[M]) WithConfigurationError(err error) RelatedDurationField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

func (field RelatedTimeField[M]) WithConfigurationError(err error) RelatedTimeField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

func (field RelatedDateField[M]) WithConfigurationError(err error) RelatedDateField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

func (field RelatedFloatField[M]) WithConfigurationError(err error) RelatedFloatField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

func (field RelatedDecimalField[M]) WithConfigurationError(err error) RelatedDecimalField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}

func (field RelatedUUIDField[M]) WithConfigurationError(err error) RelatedUUIDField[M] {
	if field.configurationErr == nil {
		field.configurationErr = err
	}
	return field
}
