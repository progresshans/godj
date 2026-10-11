//go:build race

package helpdesk_test

import "time"

// Migration growth, identity, HTTP, relations, formsets and bulk operations
// share one database and a cumulative race budget. Per-scenario cancellation
// and lock-wait bounds remain separate and shorter.
const helpdeskConsumerTimeout = 15 * time.Minute
