//go:build !race

package helpdesk_test

import "time"

const helpdeskConsumerTimeout = 10 * time.Minute
