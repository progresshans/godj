package accountstoretest

import (
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/internal/gobuild"
)

// registerCase enumerates one complete first-level subtree. Opening its
// database, migrations, mutations and all child checks belong to the callback.
type registerCase func(string, func(*testing.T))

type storeCase struct {
	name string
	run  func(*testing.T)
}

type caseGroup struct {
	Cases     []string `json:"cases"`
	ElapsedMS int64    `json:"elapsed_ms"`
	work      []storeCase
	weight    int
}

// Scheduling hints from the two Linux race receipts in run 45780889275,
// rounded to five seconds. Every other registered case is scheduled too.
var longCaseSeconds = map[string]int{
	"catalog_affected_batches":         30,
	"catalog_permission_owner_batches": 235,
	"catalog_group_union_batches":      430,
	"catalog_mutation_outcomes":        55,
	"password_admin_commit_failures":   25,
	"self_password_write_failures":     35,
	"password_reset_write_failures":    135,
	"account_reset_acknowledgement":    20,
	"account_reset_failures":           65,
	"account_password_failures":        35,
	"unusable_write_failures":          30,
	"login_write_failures":             65,
	"login_admission":                  25,
	"collection_changes":               25,
	"collection_account_lifecycle":     45,
	"collection_catalog_lifecycle":     40,
}

func caseWeight(name string) int {
	if seconds, ok := longCaseSeconds[name]; ok {
		return seconds
	}
	return 3
}

func planGroups(catalog []storeCase) ([]caseGroup, error) {
	const concurrency = 3
	if len(catalog) < concurrency {
		return nil, fmt.Errorf("only %d SQLite credential cases registered", len(catalog))
	}
	seen := make(map[string]bool, len(catalog))
	ordered := slices.Clone(catalog)
	for _, c := range catalog {
		if c.name == "" || strings.ContainsAny(c.name, "/ \t\n") || seen[c.name] || c.run == nil {
			return nil, fmt.Errorf("invalid or duplicated SQLite credential case %q", c.name)
		}
		seen[c.name] = true
	}
	slices.SortFunc(ordered, func(a, b storeCase) int {
		if delta := caseWeight(b.name) - caseWeight(a.name); delta != 0 {
			return delta
		}
		return strings.Compare(a.name, b.name)
	})
	groups := make([]caseGroup, concurrency)
	for _, c := range ordered {
		lightest := 0
		for i := 1; i < len(groups); i++ {
			if groups[i].weight < groups[lightest].weight {
				lightest = i
			}
		}
		g := &groups[lightest]
		g.work = append(g.work, c)
		g.Cases = append(g.Cases, c.name)
		g.weight += caseWeight(c.name)
	}
	return groups, nil
}

func executeGroups(t *testing.T, groups []caseGroup) {
	t.Helper()
	var workers sync.WaitGroup
	for i := range groups {
		g := &groups[i]
		workers.Go(func() {
			start := time.Now()
			for _, c := range g.work {
				t.Run(c.name, c.run)
			}
			g.ElapsedMS = time.Since(start).Milliseconds()
		})
	}
	workers.Wait()
}

func runCases(t *testing.T, vendor string, register func(registerCase)) {
	t.Helper()
	if vendor != "sqlite" || os.Getenv("GODJ_SQLITE_ACCOUNT_GROUPS") == "" {
		register(func(name string, run func(*testing.T)) { t.Run(name, run) })
		return
	}
	if os.Getenv("GODJ_SQLITE_ACCOUNT_GROUPS") != "3" || runtime.GOOS != "linux" ||
		(runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64") || !gobuild.RaceEnabled {
		t.Fatal("grouped SQLite credential execution requires the Linux race binary")
	}
	var catalog []storeCase
	var names []string
	register(func(name string, run func(*testing.T)) {
		catalog = append(catalog, storeCase{name: name, run: run})
		names = append(names, name)
	})
	groups, err := planGroups(catalog)
	if err != nil {
		t.Fatal(err)
	}
	executeGroups(t, groups)
	receipt, err := json.Marshal(struct {
		GOOS    string      `json:"goos"`
		GOARCH  string      `json:"goarch"`
		Race    bool        `json:"race"`
		Catalog []string    `json:"catalog"`
		Groups  []caseGroup `json:"groups"`
	}{runtime.GOOS, runtime.GOARCH, gobuild.RaceEnabled, names, groups})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("GODJ_SQLITE_ACCOUNT_GROUPS %s", receipt)
}
