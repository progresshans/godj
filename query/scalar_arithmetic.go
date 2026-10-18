package query

func arithmeticNumericKind(kind FieldKind) bool {
	return kind == FieldInteger || kind == FieldFloat || kind == FieldDecimal
}

// Signature resolution belongs to the common AST. Backends choose physical
// operations and value checks, not implicit casts between model domains.
func arithmeticResultKind(operator ArithmeticOperator, left, right FieldKind) (FieldKind, error) {
	switch operator {
	case ArithmeticAdd, ArithmeticSubtract, ArithmeticMultiply, ArithmeticDivide, ArithmeticModulo:
	default:
		return "", invalidPlanError("unknown arithmetic operator")
	}
	// A model period and its dimensionless factor retain separate operand
	// kinds. Dividing two periods produces a dimensionless binary64 ratio.
	if operator == ArithmeticMultiply && (left == FieldDuration && arithmeticNumericKind(right) || arithmeticNumericKind(left) && right == FieldDuration) {
		return FieldDuration, nil
	}
	if operator == ArithmeticDivide && left == FieldDuration {
		if arithmeticNumericKind(right) {
			return FieldDuration, nil
		}
		if right == FieldDuration {
			return FieldFloat, nil
		}
	}
	if arithmeticNumericKind(left) || arithmeticNumericKind(right) {
		if left != right {
			return "", invalidPlanError("numeric arithmetic operands require the same explicit kind")
		}
		if operator == ArithmeticModulo && left != FieldInteger && left != FieldDecimal {
			return "", invalidPlanError("remainder requires signed integer or Decimal operands")
		}
		return left, nil
	}
	switch operator {
	case ArithmeticAdd:
		switch {
		case left == FieldDuration && right == FieldDuration:
			return FieldDuration, nil
		case left == FieldDateTime && right == FieldDuration, left == FieldDuration && right == FieldDateTime:
			return FieldDateTime, nil
		}
	case ArithmeticSubtract:
		switch {
		case left == FieldDuration && right == FieldDuration, left == FieldDateTime && right == FieldDateTime:
			return FieldDuration, nil
		case left == FieldDateTime && right == FieldDuration:
			return FieldDateTime, nil
		}
	}
	return "", &Error{Category: CategoryQuery, Code: CodeUnsupported, Detail: "arithmetic operands do not match a supported signature"}
}
