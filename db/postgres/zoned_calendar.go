package postgres

import (
	"strconv"
	"strings"

	"github.com/progresshans/godj/db/internal/queryplan"
	"github.com/progresshans/godj/internal/zonerules"
	"github.com/progresshans/godj/query"
	"github.com/progresshans/godj/timezone"
)

type postgresZoneTable struct {
	utc, civil, offsets string
	fixed               bool
	fixedOffset         int
}

// Arrays are bound data, not SQL supplied by callers. PostgreSQL performs its
// binary-search width_bucket lookup within the same query and snapshot. The
// server's timezone database and session timezone do not determine our rules.
func postgresZoneParameters(zone timezone.Zone, inverse bool, bind queryplan.ScalarParameterBinder) (*postgresZoneTable, error) {
	rules, err := zonerules.Load(zone.Name())
	if err != nil {
		return nil, err
	}
	var utc, civil, offsets strings.Builder
	utc.WriteByte('{')
	civil.WriteByte('{')
	offsets.WriteByte('{')
	count, fixedOffset := 0, 0
	for transition := range rules.Transitions() {
		if count > 0 {
			utc.WriteByte(',')
			civil.WriteByte(',')
			offsets.WriteByte(',')
		}
		utc.WriteString(strconv.FormatInt(transition.Start, 10))
		civil.WriteString(strconv.FormatInt(transition.LocalStart, 10))
		offsets.WriteString(strconv.Itoa(transition.Offset))
		fixedOffset = transition.Offset
		count++
	}
	if count == 1 {
		return &postgresZoneTable{fixed: true, fixedOffset: fixedOffset}, nil
	}
	if count == 0 || bind == nil {
		return nil, invalidPlan("time zone compiler requires a complete transition table and parameter binder")
	}
	utc.WriteByte('}')
	civil.WriteByte('}')
	offsets.WriteByte('}')
	result := &postgresZoneTable{}
	result.utc, err = bind(query.String(utc.String()))
	if err != nil {
		return nil, err
	}
	result.utc = "(" + result.utc + "::numeric[])"
	result.offsets, err = bind(query.String(offsets.String()))
	if err != nil {
		return nil, err
	}
	result.offsets = "(" + result.offsets + "::integer[])"
	if inverse {
		result.civil, err = bind(query.String(civil.String()))
		if err != nil {
			return nil, err
		}
		result.civil = "(" + result.civil + "::numeric[])"
	}
	return result, nil
}

func postgresZonedCivilSQL(instant string, zone *postgresZoneTable) (string, error) {
	clock, err := queryplan.ScalarSQL("(", instant, " AT TIME ZONE 'UTC')")
	if err != nil {
		return "", err
	}
	civil, err := postgresZoneShiftSQL(clock, zone, false)
	if err != nil {
		return "", err
	}
	// Treat the civil components as a UTC timestamp only for the existing
	// whole-value range guard. Extracting a small part must not hide year 0,
	// year 10000, or a non-finite original instant.
	checked, err := temporalScalarResultSQL(query.FieldDateTime, "("+civil+" AT TIME ZONE 'UTC')")
	if err != nil {
		return "", err
	}
	return queryplan.ScalarSQL("(", checked, " AT TIME ZONE 'UTC')")
}

func postgresZoneShiftSQL(clock string, zone *postgresZoneTable, inverse bool) (string, error) {
	operator := " + "
	if inverse {
		operator = " - "
	}
	if zone.fixed {
		return queryplan.ScalarSQL("(", clock, operator, strconv.Itoa(zone.fixedOffset), " * INTERVAL '1 second')")
	}
	const value = `"godj_zone_epoch"."value"`
	const seconds = `"godj_zone_epoch"."seconds"`
	thresholds := zone.utc
	if inverse {
		thresholds = zone.civil
		if thresholds == "" {
			return "", invalidPlan("time zone inverse requires civil transition boundaries")
		}
	}
	start, period := strconv.FormatInt(zonerules.CycleStart, 10), strconv.FormatInt(zonerules.CycleSeconds, 10)
	phase := "(CASE WHEN " + seconds + " >= " + start + " THEN MOD(" + seconds + " - " + start + ", " + period + ") + " + start + " ELSE " + seconds + " END)"
	offset := zone.offsets + "[WIDTH_BUCKET(" + phase + ", " + thresholds + ")]"
	// EXTRACT(EPOCH FROM timestamp) is exact numeric. Only the small integer
	// offset is multiplied by an interval; the instant never becomes a float.
	return queryplan.ScalarSQL("(SELECT ", value, operator, offset, " * INTERVAL '1 second' FROM (SELECT ",
		`"godj_zone_input"."value", EXTRACT(EPOCH FROM "godj_zone_input"."value") AS "seconds" FROM (SELECT `,
		clock, ` AS "value" OFFSET 0) AS "godj_zone_input" OFFSET 0) AS "godj_zone_epoch")`)
}
