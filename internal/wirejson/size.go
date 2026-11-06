package wirejson

import (
	"encoding/json"
	"strconv"
)

// Sizer measures canonical JSON before a document-sized encoding allocation.
// A failed addition is sticky, and arithmetic is checked before conversion.
type Sizer struct {
	size    int
	maximum int
	failed  bool
}

func NewSizer(maximum int) *Sizer { return &Sizer{maximum: maximum, failed: maximum < 0} }
func (sizer *Sizer) Size() int    { return sizer.size }

func (sizer *Sizer) Add(count int) bool {
	if sizer.failed || count < 0 || count > sizer.maximum-sizer.size {
		sizer.failed = true
		return false
	}
	sizer.size += count
	return true
}

func (sizer *Sizer) Literal(value string) bool { return sizer.Add(len(value)) }

func (sizer *Sizer) String(value string) bool {
	// Every JSON string needs at least its input bytes and two quotes. Reject
	// that lower bound before allocating an escaped temporary string.
	remaining := sizer.maximum - sizer.size
	if sizer.failed || remaining < 2 || len(value) > remaining-2 {
		return sizer.Add(-1)
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return sizer.Add(-1)
	}
	return sizer.Add(len(encoded))
}

func (sizer *Sizer) Bytes(value []byte) bool {
	if value == nil {
		return sizer.Literal("null")
	}
	encoded := (uint64(len(value))+2)/3*4 + 2
	if sizer.failed || encoded > uint64(sizer.maximum-sizer.size) {
		return sizer.Add(-1)
	}
	return sizer.Add(int(encoded))
}

func (sizer *Sizer) Integer(value int64) bool {
	return sizer.Add(len(strconv.FormatInt(value, 10)))
}

func (sizer *Sizer) Boolean(value bool) bool {
	if value {
		return sizer.Literal("true")
	}
	return sizer.Literal("false")
}
