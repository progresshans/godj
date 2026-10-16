package orm

import "github.com/progresshans/godj/query"

func (expression DynamicExpression) calendar(function query.FunctionKind) DynamicExpression {
	if err := expression.validate(); err != nil {
		return DynamicExpression{err: err}
	}
	if expression.node.depth >= query.MaximumScalarDepth || expression.node.nodes >= query.MaximumScalarNodes {
		return DynamicExpression{err: invalidWritePlan("dynamic function exceeds its resource budget")}
	}
	return DynamicExpression{node: &dynamicScalarNode{
		function: function, left: expression, depth: expression.node.depth + 1, nodes: expression.node.nodes + 1,
	}}
}

// Date and Time require a DateTime operand at binding and use UTC. Calendar
// parts require Date or DateTime; clock parts require Time or DateTime. The
// binding uses the same closed signatures and source checks as typed queries.
func (expression DynamicExpression) Date() DynamicExpression {
	return expression.calendar(query.FunctionDate)
}
func (expression DynamicExpression) Time() DynamicExpression {
	return expression.calendar(query.FunctionTime)
}
func (expression DynamicExpression) Year() DynamicExpression {
	return expression.calendar(query.FunctionYear)
}
func (expression DynamicExpression) ISOYear() DynamicExpression {
	return expression.calendar(query.FunctionISOYear)
}
func (expression DynamicExpression) Quarter() DynamicExpression {
	return expression.calendar(query.FunctionQuarter)
}
func (expression DynamicExpression) Month() DynamicExpression {
	return expression.calendar(query.FunctionMonth)
}
func (expression DynamicExpression) Week() DynamicExpression {
	return expression.calendar(query.FunctionWeek)
}
func (expression DynamicExpression) WeekDay() DynamicExpression {
	return expression.calendar(query.FunctionWeekDay)
}
func (expression DynamicExpression) ISOWeekDay() DynamicExpression {
	return expression.calendar(query.FunctionISOWeekDay)
}
func (expression DynamicExpression) Day() DynamicExpression {
	return expression.calendar(query.FunctionDay)
}
func (expression DynamicExpression) Hour() DynamicExpression {
	return expression.calendar(query.FunctionHour)
}
func (expression DynamicExpression) Minute() DynamicExpression {
	return expression.calendar(query.FunctionMinute)
}
func (expression DynamicExpression) Second() DynamicExpression {
	return expression.calendar(query.FunctionSecond)
}
