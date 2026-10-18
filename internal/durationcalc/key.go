package durationcalc

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math/big"
)

var ErrKey = errors.New("invalid canonical exact duration key")

// Continued fractions give rationals an exact byte ordering without a chosen
// decimal precision. Coefficients compare ascending at even positions and
// descending at odd positions. Ending after an even coefficient is less than
// every continuation; ending after an odd coefficient is greater. Negative
// values reverse the complete positive encoding. Reduced fractions have one key,
// so SQLite can use native BLOB equality, ORDER BY, GROUP BY and DISTINCT.
//
// The header versions this internal query representation, never stored model
// data. Nonzero coefficients use unsigned ASCII decimal digits preceded by a
// uint64 byte length. This preserves numeric order and lets SQL NUMERIC encode
// the same representation without an intermediate Float or machine integer.
var keyHeader = [...]byte{'G', 'D', 'R', 1}

func (value Value) Key() []byte {
	number := value.rat()
	key := append([]byte(nil), keyHeader[:]...)
	sign := number.Sign()
	key = append(key, byte(sign+1))
	if sign == 0 {
		return key
	}
	start := len(key)
	numerator := new(big.Int).Abs(number.Num())
	denominator := new(big.Int).Set(number.Denom())
	for odd := false; ; odd = !odd {
		var quotient, remainder big.Int
		quotient.QuoRem(numerator, denominator, &remainder)
		var magnitude []byte
		if quotient.Sign() != 0 {
			magnitude = []byte(quotient.String())
		}
		segment := len(key)
		key = append(key, 0x80)
		key = binary.BigEndian.AppendUint64(key, uint64(len(magnitude)))
		key = append(key, magnitude...)
		if odd {
			invert(key[segment:])
		}
		if remainder.Sign() == 0 {
			if odd {
				key = append(key, 0xff)
			} else {
				key = append(key, 0)
			}
			break
		}
		numerator.Set(denominator)
		denominator.Set(&remainder)
	}
	if sign < 0 {
		invert(key[start:])
	}
	return key
}

func invert(value []byte) {
	for index := range value {
		value[index] = ^value[index]
	}
}

// FromKey rejects noncanonical fractions, trailing data, invalid lengths and
// out-of-domain values. It neither modifies nor retains the driver's byte slice.
func FromKey(key []byte) (Value, error) {
	if len(key) < len(keyHeader)+1 || !bytes.Equal(key[:len(keyHeader)], keyHeader[:]) {
		return Value{}, ErrKey
	}
	sign := key[len(keyHeader)]
	if sign > 2 || sign == 1 && len(key) != len(keyHeader)+1 {
		return Value{}, ErrKey
	}
	if sign == 1 {
		return Value{}, nil
	}
	data := bytes.Clone(key[len(keyHeader)+1:])
	if sign == 0 {
		invert(data)
	}
	// Convergent recurrence: p[-2]=0, p[-1]=1; q[-2]=1, q[-1]=0.
	p0, p1 := new(big.Int), big.NewInt(1)
	q0, q1 := big.NewInt(1), new(big.Int)
	for odd, index := false, 0; ; odd, index = !odd, index+1 {
		mask := byte(0)
		if odd {
			mask = 0xff
		}
		if len(data) < 9 || data[0]^mask != 0x80 {
			return Value{}, ErrKey
		}
		var size [8]byte
		for i := range size {
			size[i] = data[i+1] ^ mask
		}
		length := binary.BigEndian.Uint64(size[:])
		if length > uint64(len(data)-9) {
			return Value{}, ErrKey
		}
		data = data[9:]
		part := data[:int(length)]
		if odd {
			invert(part)
		}
		if len(part) > 0 && part[0] == '0' || index > 0 && len(part) == 0 {
			return Value{}, ErrKey
		}
		for _, digit := range part {
			if digit < '0' || digit > '9' {
				return Value{}, ErrKey
			}
		}
		coefficient := new(big.Int)
		if len(part) != 0 {
			coefficient.SetString(string(part), 10) // Checked canonical ASCII digits.
		}
		p := new(big.Int).Mul(coefficient, p1)
		p.Add(p, p0)
		q := new(big.Int).Mul(coefficient, q1)
		q.Add(q, q0)
		p0, p1, q0, q1 = p1, p, q1, q
		data = data[int(length):]
		if len(data) == 0 {
			return Value{}, ErrKey
		}
		if data[0] == mask {
			if len(data) != 1 || p1.Sign() == 0 || index > 0 && coefficient.Cmp(big.NewInt(1)) <= 0 {
				return Value{}, ErrKey
			}
			break
		}
	}
	if sign == 0 {
		p1.Neg(p1)
	}
	value, err := checked(new(big.Rat).SetFrac(p1, q1))
	if err != nil {
		return Value{}, errors.Join(ErrKey, err)
	}
	return value, nil
}
