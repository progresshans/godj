package projectwire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/progresshans/godj/codegen"
	"github.com/progresshans/godj/internal/wirejson"
	"github.com/progresshans/godj/schema/ir"
)

func TestExternalAppWireRetainsOwnershipAndExactBound(t *testing.T) {
	for _, external := range []bool{false, true} {
		input := codegen.ProjectSpec{Apps: []codegen.AppSpec{{
			Alias: "<library>\u2028한글", External: external,
			Schema: ir.Schema{FormatVersion: ir.CurrentFormatVersion, AppLabel: "library", Models: []ir.Model{}},
		}}}
		for _, spec := range []Spec{Snapshot(input), View(input)} {
			wire, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(wire, []byte(`"external":`)) != external {
				t.Fatal("wire ownership does not match declaration")
			}
			for _, limit := range []int{len(wire) - 1, len(wire), len(wire) + 1} {
				sizer := wirejson.NewSizer(limit)
				ok := Measure(sizer, spec)
				if ok != (limit >= len(wire)) || ok && sizer.Size() != len(wire) {
					t.Fatal("ownership changed exact wire budget", limit, sizer.Size(), len(wire))
				}
			}
			canonical, err := json.Marshal(Snapshot(Declaration(spec)))
			if err != nil {
				t.Fatal(err)
			}
			decoder := json.NewDecoder(bytes.NewReader(canonical))
			decoder.UseNumber()
			if err := Scan(decoder); err != nil {
				t.Fatal(err)
			}
			var decoded Spec
			if err := json.Unmarshal(canonical, &decoded); err != nil {
				t.Fatal(err)
			}
			if Declaration(decoded).Apps[0].External != external || Clone(input).Apps[0].External != external {
				t.Fatal("declaration lost external file ownership")
			}
		}
	}
}

func TestExternalAppWireRejectsMalformedOwnershipBeforeDecode(t *testing.T) {
	valid := fmt.Sprintf(`{"alias":"library","package":{"package_name":"models","import_path":"example.com/library/models","directory":""},"schema":{"format_version":%d,"app_label":"library","models":[]},"external":true}`, ir.CurrentFormatVersion)
	control := json.NewDecoder(strings.NewReader(valid))
	control.UseNumber()
	if err := parseApp(control, &specBudget{}); err != nil {
		t.Fatal("valid control failed", err)
	}
	for _, replacement := range []string{`null`, `"true"`, `1`, `[]`, `{}`, `true,"external":false`} {
		wire := strings.Replace(valid, `true}`, replacement+`}`, 1)
		decoder := json.NewDecoder(strings.NewReader(wire))
		decoder.UseNumber()
		if err := parseApp(decoder, &specBudget{}); err == nil {
			t.Fatal("malformed ownership accepted", replacement)
		}
	}
}
