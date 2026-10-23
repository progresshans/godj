package sqlite

import (
	"database/sql/driver"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math/big"

	"github.com/progresshans/godj/jsonvalue"
	modernsqlite "modernc.org/sqlite"
)

var jsonSortRegistrationError = modernsqlite.RegisterDeterministicScalarFunction("godj_json_sort_key", 1, sqliteJSONSortKey)

// A checked BLOB key preserves SQLite's numeric-before-text path ordering
// without converting exact numbers to float64. Missing paths remain SQL NULL.
// Returning errors here also avoids an error-less collation callback.
func sqliteJSONSortKey(_ *modernsqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	invalid := errors.New("invalid SQLite JSON ordering arguments")
	if len(args) != 1 {
		return nil, invalid
	}
	if args[0] == nil {
		return nil, nil
	}
	raw, ok := args[0].(string)
	if !ok {
		return nil, invalid
	}
	value, err := (jsonvalue.Value{Text: raw}).Decode()
	if err != nil {
		return nil, err
	}
	if number, ok := value.(json.Number); ok {
		return jsonNumberSortKey(number.String())
	}
	text, ok := value.(string)
	if !ok {
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, err
		}
		text = string(encoded)
	}
	return append([]byte{1}, []byte(text)...), nil
}

// Scientific magnitude and coefficient are bounded by the input token size;
// a huge exponent never allocates its expanded decimal representation.
func jsonNumberSortKey(raw string) ([]byte, error) {
	digits, magnitude, sign, err := jsonNumberParts(raw)
	if err != nil {
		return nil, err
	}
	key := []byte{0, byte(sign + 1)}
	if sign == 0 {
		return key, nil
	}
	key = appendSortMagnitude(key, magnitude)
	key = append(key, digits...)
	key = append(key, 0) // Must reverse the coefficient terminator as well.
	if sign < 0 {
		complementSortBytes(key[2:])
	}
	return key, nil
}

func appendSortMagnitude(key []byte, magnitude *big.Int) []byte {
	key = append(key, byte(magnitude.Sign()+1))
	if magnitude.Sign() == 0 {
		return key
	}
	abs := new(big.Int).Abs(magnitude).String()
	start := len(key)
	key = binary.BigEndian.AppendUint32(key, uint32(len(abs)))
	key = append(key, abs...)
	if magnitude.Sign() < 0 {
		complementSortBytes(key[start:])
	}
	return key
}

func complementSortBytes(data []byte) {
	for i := range data {
		data[i] = ^data[i]
	}
}
