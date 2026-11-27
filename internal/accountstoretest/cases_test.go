package accountstoretest

import (
	"slices"
	"sync/atomic"
	"testing"
	"time"
)

func TestSQLiteCaseCatalogPreservesCompleteRegisteredSubtrees(t *testing.T) {
	var catalog []storeCase
	registerAccountCases(func(name string, run func(*testing.T)) {
		catalog = append(catalog, storeCase{name, run})
	}, "sqlite", nil, nil, nil)
	catalog = append(catalog, storeCase{"future_case", func(*testing.T) {}})
	groups, err := planGroups(catalog)
	if err != nil {
		t.Fatal(err)
	}
	var actual, expected []string
	for _, group := range groups {
		if len(group.Cases) == 0 {
			t.Fatal("empty group")
		}
		actual = append(actual, group.Cases...)
	}
	for _, c := range catalog {
		expected = append(expected, c.name)
	}
	slices.Sort(actual)
	slices.Sort(expected)
	if !slices.Equal(actual, expected) {
		t.Fatal("group membership differs from the registered SQLite cases")
	}
	for _, extra := range []storeCase{catalog[0], {"not/a/whole_case", func(*testing.T) {}}, {"missing_callback", nil}} {
		if _, err := planGroups(append(slices.Clone(catalog), extra)); err == nil {
			t.Fatalf("invalid registration was accepted: %s", extra.name)
		}
	}
}

func TestSQLiteCaseGroupsUseBoundedConcurrencyAndWaitForChildren(t *testing.T) {
	const groupCount = 3
	var active, finished atomic.Int32
	var arrived atomic.Int32
	ready := make(chan struct{})
	var catalog []storeCase
	for _, name := range []string{"a", "b", "c", "d", "e", "f"} {
		catalog = append(catalog, storeCase{name, func(t *testing.T) {
			if n := active.Add(1); n > groupCount {
				t.Errorf("too many active cases: %d", n)
			}
			defer active.Add(-1)
			if arrived.Add(1) == groupCount {
				close(ready)
			}
			select {
			case <-ready:
			case <-time.After(10 * time.Second):
				t.Fatal("independent case groups did not overlap")
			}
			t.Run("stateful_child", func(t *testing.T) { finished.Add(1) })
		}})
	}
	groups, err := planGroups(catalog)
	if err != nil {
		t.Fatal(err)
	}
	executeGroups(t, groups)
	if active.Load() != 0 || int(finished.Load()) != len(catalog) {
		t.Fatal("group execution returned before every child finished")
	}
}
