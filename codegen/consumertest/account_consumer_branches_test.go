package codegen_test

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type accountConsumerTestPlan struct {
	name, pattern string
	required      []string
}

// Split complete, independent fixture branches across child invocations.
// Go's per-process test deadline, race mode and each leaf's assertions remain
// unchanged. Ancestors execute in each selected branch; every leaf has one
// owner. A missing or overlapping branch is rejected before launching Go.
func planAccountConsumerBranches(required, branches []string) ([]accountConsumerTestPlan, error) {
	if len(required) == 0 || len(branches) == 0 {
		return nil, fmt.Errorf("account consumer branch inventory is empty")
	}
	seen := make(map[string]bool, len(required))
	for _, name := range required {
		if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") || strings.Contains(name, "//") || seen[name] {
			return nil, fmt.Errorf("account consumer test name is empty, repeated or malformed: %q", name)
		}
		seen[name] = true
	}
	within := func(name, prefix string) bool { return name == prefix || strings.HasPrefix(name, prefix+"/") }
	plans := make([]accountConsumerTestPlan, len(branches))
	for index, branch := range branches {
		if !seen[branch] {
			return nil, fmt.Errorf("account consumer branch is absent from the required inventory: %q", branch)
		}
		for _, previous := range branches[:index] {
			if within(branch, previous) || within(previous, branch) {
				return nil, fmt.Errorf("account consumer branches overlap: %q and %q", branch, previous)
			}
		}
		segments := strings.Split(branch, "/")
		for part := range segments {
			segments[part] = "^" + regexp.QuoteMeta(segments[part]) + "$"
		}
		plans[index] = accountConsumerTestPlan{name: branch, pattern: strings.Join(segments, "/")}
	}
	for _, name := range required {
		owners := 0
		for index, branch := range branches {
			if within(name, branch) || within(branch, name) {
				plans[index].required = append(plans[index].required, name)
				owners++
			}
		}
		if owners == 0 {
			return nil, fmt.Errorf("required account consumer test has no branch owner: %q", name)
		}
	}
	return plans, nil
}

func TestAccountConsumerBranchesPreserveEveryRequiredLeaf(t *testing.T) {
	required := []string{"TestFixture", "TestFixture/plain", "TestFixture/plain/staged", "TestFixture/plain/staged/a", "TestFixture/plain/staged/b", "TestFixture/plain/created", "TestFixture/plain/created/a", "TestFixture/unique", "TestFixture/unique/staged", "TestFixture/unique/staged/a", "TestFixture/unique/created", "TestFixture/unique/created/a", "TestDefault"}
	branches := []string{"TestFixture/plain/staged", "TestFixture/plain/created", "TestFixture/unique/staged", "TestFixture/unique/created", "TestDefault"}
	plans, err := planAccountConsumerBranches(required, branches)
	if err != nil || len(plans) != len(branches) {
		t.Fatal(plans, err)
	}
	counts := make(map[string]int)
	for _, plan := range plans {
		parts := strings.Split(plan.pattern, "/")
		matches := func(name string) bool {
			segments := strings.Split(name, "/")
			for index := 0; index < min(len(parts), len(segments)); index++ {
				matched, err := regexp.MatchString(parts[index], segments[index])
				if err != nil || !matched {
					return false
				}
			}
			return true
		}
		for _, name := range required {
			if matches(name) != slices.Contains(plan.required, name) {
				t.Fatal("Go's segmented selector differs from required inventory", plan, name)
			}
		}
		for _, name := range plan.required {
			counts[name]++
		}
		if matches(plan.name + "Extra") {
			t.Fatal("partial branch name selected unintended tests", plan)
		}
	}
	for _, name := range required {
		isParent := slices.ContainsFunc(required, func(other string) bool { return strings.HasPrefix(other, name+"/") })
		if counts[name] == 0 || (!isParent && counts[name] != 1) {
			t.Fatal("missing or duplicate leaf execution", name, counts[name])
		}
	}
	for _, test := range []struct{ required, branches []string }{
		{nil, branches}, {required, nil}, {required, branches[:4]},
		{required, append(slices.Clone(branches), branches[0])},
		{required, append(slices.Clone(branches), "TestFixture")},
		{required, append(slices.Clone(branches), "TestMissing")},
		{append(slices.Clone(required), "TestMissing/child"), branches},
		{append(slices.Clone(required), required[0]), branches},
		{append(slices.Clone(required), "TestFixture//bad"), branches},
	} {
		if plans, err := planAccountConsumerBranches(test.required, test.branches); err == nil || plans != nil {
			t.Fatal("incomplete or ambiguous branch plan was accepted", test)
		}
	}
	before := slices.Clone(plans[0].required)
	required[0], branches[0] = "mutated", "mutated"
	if !slices.Equal(plans[0].required, before) {
		t.Fatal("caller mutated a prepared branch inventory")
	}
}
