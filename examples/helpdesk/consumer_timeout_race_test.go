//go:build race

package helpdesk_test

import "time"

// Migration growth, identity, HTTP, relations, formsets, zoned calendars and
// bulk operations share one database and a cumulative race budget. This fits
// within the integration package's 25-minute limit. Per-scenario cancellation
// and lock-wait bounds remain separate and shorter.
const helpdeskConsumerTimeout = 20 * time.Minute
