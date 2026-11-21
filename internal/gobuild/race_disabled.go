//go:build !race

package gobuild

// RaceEnabled records the instrumentation of this executable, independently of
// mutable host Go flags or environment settings.
const RaceEnabled = false
