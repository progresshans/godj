package sqlite

import (
	"database/sql/driver"
	"encoding/binary"
	"errors"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/progresshans/godj/decimal"
	"github.com/progresshans/godj/internal/decimalstorage"
	modernsqlite "modernc.org/sqlite"
)

var errDecimalAggregateInput = errors.New("godj: decimal aggregate operand is not a canonical value in its declared precision")
var errDecimalAggregateState = errors.New("godj: decimal aggregate state is inconsistent or exceeds its row bound")

// One owned BLOB combines field precision with the canonical numeric key.
// SQLite DISTINCT therefore operates on one argument and numeric equality,
// including a single representation of zero. No callback retains driver bytes.
func sqliteDecimalAggregateInput(_ *modernsqlite.FunctionContext, arguments []driver.Value) (driver.Value, error) {
	if len(arguments) != 3 {
		return nil, errDecimalAggregateInput
	}
	digits, digitsOK := arguments[1].(int64)
	places, placesOK := arguments[2].(int64)
	if !digitsOK || !placesOK || digits < 1 || digits > decimal.MaxDigits || places < 0 || places > digits {
		return nil, errDecimalAggregateInput
	}
	if arguments[0] == nil {
		return nil, nil
	}
	key, ok := arguments[0].([]byte)
	if !ok {
		return nil, errDecimalAggregateInput
	}
	value, err := decimalstorage.Decode(key)
	if err != nil || !value.Fits(int(digits), int(places)) {
		return nil, errDecimalAggregateInput
	}
	packed := make([]byte, 4+len(key))
	binary.BigEndian.PutUint16(packed[:2], uint16(digits))
	binary.BigEndian.PutUint16(packed[2:4], uint16(places))
	copy(packed[4:], key)
	return packed, nil
}

type decimalAggregate struct {
	average        bool
	digits, places int
	count          int64
	sum            big.Int
	err            error
}

func (a *decimalAggregate) Step(_ *modernsqlite.FunctionContext, arguments []driver.Value) error {
	if a.err != nil {
		return a.err
	}
	err := a.add(arguments)
	if err != nil {
		a.err = err
	}
	return err
}

func (a *decimalAggregate) add(arguments []driver.Value) error {
	if len(arguments) != 1 {
		return errDecimalAggregateInput
	}
	if arguments[0] == nil {
		return nil
	}
	packed, ok := arguments[0].([]byte)
	if !ok || len(packed) < 5 || len(packed) > decimal.MaxDigits+10 {
		return errDecimalAggregateInput
	}
	digits := int(binary.BigEndian.Uint16(packed[:2]))
	places := int(binary.BigEndian.Uint16(packed[2:4]))
	value, err := decimalstorage.Decode(packed[4:])
	if err != nil || !value.Fits(digits, places) {
		return errDecimalAggregateInput
	}
	if a.count == math.MaxInt64 || a.count != 0 && (a.digits != digits || a.places != places) {
		return errDecimalAggregateState
	}
	var number big.Int
	if value.Coefficient != "" && value.Coefficient != "-0" {
		if _, ok := number.SetString(value.Coefficient, 10); !ok {
			return errDecimalAggregateInput
		}
		shift := int(value.Exponent) + places
		if shift < 0 || shift > digits {
			return errDecimalAggregateInput
		}
		number.Mul(&number, decimalPower(shift))
	}
	// Each unscaled operand has at most digits decimal digits. A positive
	// int64 count bounds this exact accumulator to digits+19 digits, even
	// when the final sum exceeds the public Decimal result domain.
	a.sum.Add(&a.sum, &number)
	a.count++
	a.digits, a.places = digits, places
	return nil
}

func (a *decimalAggregate) WindowValue(_ *modernsqlite.FunctionContext) (driver.Value, error) {
	if a.err != nil {
		return nil, a.err
	}
	if a.count == 0 {
		return nil, nil
	}
	var value decimal.Decimal
	var err error
	if a.average {
		value, err = decimalMean(&a.sum, a.count, a.places)
	} else {
		value, err = decimal.New(a.sum.String(), -int32(a.places))
	}
	if err != nil {
		return nil, err
	}
	return decimalstorage.Encode(value)
}

func (a *decimalAggregate) WindowInverse(_ *modernsqlite.FunctionContext, _ []driver.Value) error {
	a.err = errors.New("godj: decimal window aggregates are not supported")
	return a.err
}
func (a *decimalAggregate) Final(_ *modernsqlite.FunctionContext) {
	*a = decimalAggregate{err: errDecimalAggregateState}
}

func decimalPower(exponent int) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(exponent)), nil)
}

func decimalMean(sum *big.Int, count int64, places int) (decimal.Decimal, error) {
	if count < 1 || places < 0 || places > decimal.MaxDigits {
		return decimal.Decimal{}, errDecimalAggregateState
	}
	if sum.Sign() == 0 {
		return decimal.Decimal{}, nil
	}
	var magnitude big.Int
	magnitude.Abs(sum)
	scale := decimalMeanScale(magnitude.String(), places, count)
	magnitude.Mul(&magnitude, decimalPower(scale-places))
	denominator := big.NewInt(count)
	var result, remainder big.Int
	result.QuoRem(&magnitude, denominator, &remainder)
	if remainder.Lsh(&remainder, 1).Cmp(denominator) >= 0 {
		result.Add(&result, big.NewInt(1))
	}
	if sum.Sign() < 0 {
		result.Neg(&result)
	}
	return decimal.New(result.String(), -int32(scale))
}

// The division-scale rule follows PostgreSQL 17.10 select_div_scale in
// src/backend/utils/adt/numeric.c. Adapted to decimal strings and math/big;
// no PostgreSQL object representation or division implementation is copied.
// Portions Copyright (c) 1998-2024, PostgreSQL Global Development Group.
// PostgreSQL License: see /LICENSE.postgresql and /NOTICE.md.
func decimalMeanScale(unscaled string, places int, count int64) int {
	numeratorWeight, numeratorFirst := decimalLeadingGroup(unscaled, places)
	denominatorWeight, denominatorFirst := decimalLeadingGroup(strconv.FormatInt(count, 10), 0)
	quotientWeight := numeratorWeight - denominatorWeight
	if numeratorFirst <= denominatorFirst {
		quotientWeight--
	}
	return min(decimal.MaxDigits, max(16-4*quotientWeight, places, 0))
}

// Returns the power and leading digit of a positive number in base 10000.
// Scale remains independent of the coefficient's leading or trailing zeros.
func decimalLeadingGroup(coefficient string, places int) (int, int) {
	adjusted := len(coefficient) - places - 1
	weight := adjusted / 4
	if adjusted < 0 && adjusted%4 != 0 {
		weight--
	}
	leadingDigits := adjusted - 4*weight + 1
	leading := coefficient
	if len(leading) > leadingDigits {
		leading = leading[:leadingDigits]
	}
	if len(leading) < leadingDigits {
		leading += strings.Repeat("0", leadingDigits-len(leading))
	}
	value, _ := strconv.Atoi(leading)
	return weight, value
}
