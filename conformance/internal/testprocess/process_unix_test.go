//go:build darwin || linux

package testprocess

import (
	"errors"
	"reflect"
	"syscall"
	"testing"
	"time"
)

func TestOwnedGroupsKeepsOnlySafeDescendants(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name, rows string
		want       []int
		invalid    bool
	}{
		{"out of order and separate child group", "30 20 30\n99 1 99\n20 10 10\n10 1 10\n31 30 30\n", []int{10, 30}, false},
		{"exited root", "99 1 99\n", []int{10}, false},
		{"empty snapshot", "\n", []int{10}, false},
		{"malformed row", "20 10\n", []int{10}, true},
		{"invalid identifier", "20 no 20\n", []int{10}, true},
		{"unsafe child group", "20 10 1\n", []int{10}, true},
		{"test runner group", "20 10 50\n", []int{10}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseOwnedGroups([]byte(test.rows), 10, 50)
			if !reflect.DeepEqual(got, test.want) || (err != nil) != test.invalid {
				t.Fatalf("owned groups = %v, %v; want %v, invalid=%t", got, err, test.want, test.invalid)
			}
		})
	}
}

func TestWaitPreservesExitErrorAndBoundsBlockedCleanup(t *testing.T) {
	t.Parallel()
	finished := make(chan error, 1)
	want := errors.New("child exit")
	finished <- want
	if got := Wait(finished, time.Second); !errors.Is(got, want) {
		t.Fatalf("wait error = %v, want original exit error", got)
	}
	if err := Wait(make(chan error), time.Millisecond); err == nil {
		t.Fatal("blocked cleanup returned success")
	}
}

func TestSignalRefusesUnsafeProcessGroups(t *testing.T) {
	t.Parallel()
	for _, group := range []int{-1, 0, 1, syscall.Getpgrp()} {
		// Signal zero checks the guard without delivering a destructive signal.
		if err := SignalGroup(group, 0); err == nil {
			t.Fatalf("unsafe group %d was accepted", group)
		}
	}
}
