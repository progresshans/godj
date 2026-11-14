package identitytest

import "time"

// Shared fixed instant of the retained password/reset reference consumers.
var loginInstant = time.Date(2026, 9, 27, 1, 2, 3, 123456000, time.UTC)
