package protocol

import (
	"errors"

	"github.com/progresshans/godj/internal/projectwire"
	"github.com/progresshans/godj/internal/wirejson"
)

var errResponseTooLarge = errors.New("project generation protocol: response exceeds maximum size")

func measureSuccessDocument(spec projectwire.Spec) (int, error) {
	sizer := wirejson.NewSizer(MaxResponseBytes)
	if !sizer.Literal(`{"protocol_version":1,"status":"ok","project_spec":`) ||
		!projectwire.Measure(sizer, spec) || !sizer.Literal(`}`) {
		return 0, errResponseTooLarge
	}
	return sizer.Size(), nil
}
