package parameters_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/progresshans/godj/api/parameters"
	"github.com/progresshans/godj/serializers"
	"github.com/progresshans/godj/timezone"
)

func TestTimeZoneParametersPreservePinnedNamesPresenceAndAtomicValidation(t *testing.T) {
	type input struct {
		Zone     timezone.Zone
		Optional *timezone.Zone
	}
	setters := 0
	query, err := parameters.New(1024,
		parameters.Field("zone", parameters.Default(parameters.TimeZone(), timezone.UTC()), func(v *input, zone timezone.Zone) { setters++; v.Zone = zone }, "Calendar zone."),
		parameters.Field("optional", parameters.Optional(parameters.TimeZone()), func(v *input, zone *timezone.Zone) { setters++; v.Optional = zone }, "Optional zone."),
	)
	if err != nil {
		t.Fatal(err)
	}
	names, err := timezone.Names()
	if err != nil {
		t.Fatal(err)
	}
	longest := 0
	for _, name := range names {
		longest = max(longest, len(name))
		value, diagnostics, err := query.Parse("zone=" + url.QueryEscape(name) + "&optional=" + url.QueryEscape(name))
		if err != nil || !diagnostics.Empty() || value.Zone.Name() != name || value.Optional == nil || *value.Optional != value.Zone {
			t.Fatal("pinned name or alias was changed or rejected", name, value, diagnostics, err)
		}
	}
	omitted, diagnostics, err := query.Parse("")
	if err != nil || !diagnostics.Empty() || omitted.Zone != timezone.UTC() || omitted.Optional != nil {
		t.Fatal("timezone omission differs from explicit presence", omitted, diagnostics, err)
	}
	for _, raw := range []string{"", "Local", "local", "utc", "UTC ", " Asia/Seoul", "Asia/seoul", "Mars/Olympus", "+09:00", "UTC+9", "../UTC", "/usr/share/zoneinfo/UTC", "UTC\x00", "UTC\n", "\xff", strings.Repeat("X", timezone.MaximumNameBytes+1)} {
		before := setters
		value, diagnostics, err := query.Parse("optional=UTC&zone=" + url.QueryEscape(raw))
		if err != nil || diagnostics.Empty() || value.Zone.Valid() || value.Optional != nil || setters != before {
			t.Fatal("invalid zone reached any setter", raw, value, diagnostics, err)
		}
	}
	for _, raw := range []string{"zone=UTC&zone=UTC", "zone=UTC&optional=", "zone=%", "zone=%FF", "other=UTC"} {
		before := setters
		value, diagnostics, err := query.Parse(raw)
		if err != nil || diagnostics.Empty() || value.Zone.Valid() || value.Optional != nil || setters != before {
			t.Fatal("invalid timezone transport reached a setter", raw, diagnostics, err)
		}
	}
	first, _, _ := query.Parse("optional=US/Eastern")
	second, _, _ := query.Parse("optional=US/Eastern")
	if first.Optional == nil || second.Optional == nil || first.Optional == second.Optional {
		t.Fatal("timezone result ownership was shared")
	}
	*first.Optional = timezone.Zone{}
	if second.Optional.Name() != "US/Eastern" || !second.Optional.Valid() {
		t.Fatal("caller changed another parsed timezone")
	}
	if _, err := parameters.New(1024, parameters.Field("zone", parameters.Default(parameters.TimeZone(), timezone.Zone{}), func(*input, timezone.Zone) {}, "Invalid default.")); err == nil {
		t.Fatal("zero timezone default accepted")
	}
	required, err := parameters.NewHeader("X-Time-Zone", parameters.Required(parameters.TimeZone()), longest, "Exact zone.")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		value, diagnostics, err := required.Parse(http.Header{"X-Time-Zone": {name}})
		if err != nil || !diagnostics.Empty() || value.Name() != name {
			t.Fatal("timezone header rejected a declared name", name, diagnostics, err)
		}
	}
	for _, header := range []http.Header{nil, {"X-Time-Zone": {""}}, {"X-Time-Zone": {"UTC", "UTC"}}, {"X-Time-Zone": {"Asia%2FSeoul"}}, {"X-Time-Zone": {"UTC\r\n"}}} {
		if value, diagnostics, err := required.Parse(header); err != nil || diagnostics.Empty() || value.Valid() {
			t.Fatal("timezone header presence or transport changed", diagnostics, err)
		}
	}
	for _, budget := range []int{0, longest - 1, 1<<20 + 1} {
		if _, err := parameters.NewHeader("X-Time-Zone", parameters.Required(parameters.TimeZone()), budget, "Invalid domain budget."); err == nil {
			t.Fatal("timezone header advertised values outside its byte budget", budget)
		}
	}
	encoded, err := serializers.Encode(query.Parameters()[0].Schema.Value(), serializers.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Type, Default string
		Enum          []string
		MaxLength     int
		Data          struct{ Database, Version, SHA256 string } `json:"x-godj-timezone"`
	}
	if err := json.Unmarshal(encoded, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Type != "string" || schema.Default != "UTC" || !slices.Equal(schema.Enum, names) || schema.MaxLength != longest || schema.Data.Database != "IANA" || schema.Data.Version != timezone.DataVersion || schema.Data.SHA256 != timezone.DataSHA256 {
		t.Fatal("timezone schema lost data identity, names or presence", string(encoded))
	}
}
