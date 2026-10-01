package migrations

import (
	"container/heap"
	"sort"
)

type plannerGraph struct {
	nodes    []MigrationKey
	parents  map[MigrationKey][]MigrationKey
	children map[MigrationKey][]MigrationKey
}

type plannerDefinition struct {
	key          MigrationKey
	dependencies []MigrationKey
}

type dependencyEdge struct {
	child  MigrationKey
	parent MigrationKey
}

func newPlannerGraph(migrations []Migration) (*plannerGraph, error) {
	definitions := make([]plannerDefinition, len(migrations))
	invalidNodes := make([]MigrationKey, 0)
	for index, migration := range migrations {
		key := migration.Key()
		definitions[index] = plannerDefinition{
			key:          key,
			dependencies: append([]MigrationKey(nil), migration.Dependencies...),
		}
		if !validMigrationKey(key) {
			invalidNodes = append(invalidNodes, key)
		}
	}
	if len(invalidNodes) != 0 {
		sortMigrationKeys(invalidNodes)
		return nil, newPlanningError(CategoryGraph, CodeInvalidNode, invalidNodes[0], MigrationKey{}, nil)
	}

	sort.Slice(definitions, func(left, right int) bool {
		return migrationKeyLess(definitions[left].key, definitions[right].key)
	})
	for index := 1; index < len(definitions); index++ {
		if definitions[index].key == definitions[index-1].key {
			return nil, newPlanningError(CategoryGraph, CodeDuplicateNode, definitions[index].key, MigrationKey{}, nil)
		}
	}

	invalidEdges := make([]dependencyEdge, 0)
	duplicateEdges := make([]dependencyEdge, 0)
	for index := range definitions {
		dependencies := definitions[index].dependencies
		sortMigrationKeys(dependencies)
		for _, parent := range dependencies {
			if !validMigrationKey(parent) {
				invalidEdges = append(invalidEdges, dependencyEdge{child: definitions[index].key, parent: parent})
			}
		}
		for dependencyIndex := 1; dependencyIndex < len(dependencies); dependencyIndex++ {
			if dependencies[dependencyIndex] == dependencies[dependencyIndex-1] {
				duplicateEdges = append(duplicateEdges, dependencyEdge{
					child:  definitions[index].key,
					parent: dependencies[dependencyIndex],
				})
			}
		}
	}
	if len(invalidEdges) != 0 {
		sortDependencyEdges(invalidEdges)
		edge := invalidEdges[0]
		return nil, newPlanningError(CategoryGraph, CodeInvalidDependency, edge.child, edge.parent, nil)
	}
	if len(duplicateEdges) != 0 {
		sortDependencyEdges(duplicateEdges)
		edge := duplicateEdges[0]
		return nil, newPlanningError(CategoryGraph, CodeDuplicateDependency, edge.child, edge.parent, nil)
	}

	nodeSet := make(map[MigrationKey]struct{}, len(definitions))
	for _, definition := range definitions {
		nodeSet[definition.key] = struct{}{}
	}
	missingEdges := make([]dependencyEdge, 0)
	for _, definition := range definitions {
		for _, parent := range definition.dependencies {
			if _, exists := nodeSet[parent]; !exists {
				missingEdges = append(missingEdges, dependencyEdge{child: definition.key, parent: parent})
			}
		}
	}
	if len(missingEdges) != 0 {
		sortDependencyEdges(missingEdges)
		edge := missingEdges[0]
		return nil, newPlanningError(CategoryGraph, CodeDependencyNotFound, edge.child, edge.parent, nil)
	}

	graph := &plannerGraph{
		nodes:    make([]MigrationKey, 0, len(definitions)),
		parents:  make(map[MigrationKey][]MigrationKey, len(definitions)),
		children: make(map[MigrationKey][]MigrationKey, len(definitions)),
	}
	for _, definition := range definitions {
		graph.nodes = append(graph.nodes, definition.key)
		graph.parents[definition.key] = append([]MigrationKey(nil), definition.dependencies...)
		for _, parent := range definition.dependencies {
			graph.children[parent] = append(graph.children[parent], definition.key)
		}
	}
	for _, key := range graph.nodes {
		sortMigrationKeys(graph.parents[key])
		sortMigrationKeys(graph.children[key])
	}

	if members := graph.firstCycleComponent(); len(members) != 0 {
		return nil, newPlanningError(CategoryGraph, CodeDependencyCycle, MigrationKey{}, MigrationKey{}, members)
	}
	return graph, nil
}

func emptyPlannerGraph() *plannerGraph {
	return &plannerGraph{}
}

func (g *plannerGraph) contains(key MigrationKey) bool {
	_, exists := g.parents[key]
	return exists
}

func (g *plannerGraph) containsApp(app string) bool {
	for _, key := range g.nodes {
		if key.App == app {
			return true
		}
	}
	return false
}

func (g *plannerGraph) validateAppliedHistory(applied map[MigrationKey]struct{}) error {
	for _, child := range g.nodes {
		if _, exists := applied[child]; !exists {
			continue
		}
		for _, parent := range g.parents[child] {
			if _, exists := applied[parent]; !exists {
				return newPlanningError(CategoryHistory, CodeInconsistentAppliedHistory, child, parent, nil)
			}
		}
	}
	return nil
}

func (g *plannerGraph) planForward(target MigrationKey, applied map[MigrationKey]struct{}) ([]PlanStep, error) {
	candidates := make(map[MigrationKey]struct{})
	stack := []MigrationKey{target}
	for len(stack) != 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, exists := applied[key]; exists {
			continue
		}
		if _, exists := candidates[key]; exists {
			continue
		}
		candidates[key] = struct{}{}
		stack = append(stack, g.parents[key]...)
	}

	unresolvedParents := make(map[MigrationKey]int, len(candidates))
	ready := make(migrationKeyHeap, 0, len(candidates))
	for key := range candidates {
		for _, parent := range g.parents[key] {
			if _, exists := applied[parent]; !exists {
				unresolvedParents[key]++
			}
		}
		if unresolvedParents[key] == 0 {
			ready = append(ready, key)
		}
	}
	heap.Init(&ready)

	steps := make([]PlanStep, 0, len(candidates))
	for len(candidates) != 0 {
		if ready.Len() == 0 {
			return nil, internalCyclePlanningError(candidates)
		}
		next := heap.Pop(&ready).(MigrationKey)
		steps = append(steps, PlanStep{Key: next, Direction: DirectionForward})
		applied[next] = struct{}{}
		delete(candidates, next)
		for _, child := range g.children[next] {
			if _, exists := candidates[child]; !exists {
				continue
			}
			unresolvedParents[child]--
			if unresolvedParents[child] == 0 {
				heap.Push(&ready, child)
			}
		}
	}
	return steps, nil
}

func (g *plannerGraph) planBackward(seed MigrationKey, applied map[MigrationKey]struct{}) ([]PlanStep, error) {
	candidates := make(map[MigrationKey]struct{})
	stack := []MigrationKey{seed}
	visited := make(map[MigrationKey]struct{})
	for len(stack) != 0 {
		key := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, exists := visited[key]; exists {
			continue
		}
		visited[key] = struct{}{}
		if _, exists := applied[key]; exists {
			candidates[key] = struct{}{}
		}
		stack = append(stack, g.children[key]...)
	}

	unresolvedChildren := make(map[MigrationKey]int, len(candidates))
	ready := make(migrationKeyHeap, 0, len(candidates))
	for key := range candidates {
		for _, child := range g.children[key] {
			if _, exists := candidates[child]; exists {
				unresolvedChildren[key]++
			}
		}
		if unresolvedChildren[key] == 0 {
			ready = append(ready, key)
		}
	}
	heap.Init(&ready)

	steps := make([]PlanStep, 0, len(candidates))
	for len(candidates) != 0 {
		if ready.Len() == 0 {
			return nil, internalCyclePlanningError(candidates)
		}
		next := heap.Pop(&ready).(MigrationKey)
		steps = append(steps, PlanStep{Key: next, Direction: DirectionBackward})
		delete(applied, next)
		delete(candidates, next)
		for _, parent := range g.parents[next] {
			if _, exists := candidates[parent]; !exists {
				continue
			}
			unresolvedChildren[parent]--
			if unresolvedChildren[parent] == 0 {
				heap.Push(&ready, parent)
			}
		}
	}
	return steps, nil
}

type migrationKeyHeap []MigrationKey

func (h migrationKeyHeap) Len() int { return len(h) }

func (h migrationKeyHeap) Less(left, right int) bool {
	return migrationKeyLess(h[left], h[right])
}

func (h migrationKeyHeap) Swap(left, right int) {
	h[left], h[right] = h[right], h[left]
}

func (h *migrationKeyHeap) Push(value any) {
	*h = append(*h, value.(MigrationKey))
}

func (h *migrationKeyHeap) Pop() any {
	old := *h
	last := len(old) - 1
	value := old[last]
	old[last] = MigrationKey{}
	*h = old[:last]
	return value
}

func (g *plannerGraph) appRoots(app string) []MigrationKey {
	roots := make([]MigrationKey, 0)
	for _, key := range g.nodes {
		if key.App != app {
			continue
		}
		hasSameAppParent := false
		for _, parent := range g.parents[key] {
			if parent.App == app {
				hasSameAppParent = true
				break
			}
		}
		if !hasSameAppParent {
			roots = append(roots, key)
		}
	}
	return roots
}

// appLeaves returns every node that has no child in the same app. A node with
// only cross-app dependents is still a leaf for its own app, matching the
// migration graph semantics used when reconstructing the latest state.
func (g *plannerGraph) appLeaves() []MigrationKey {
	leaves := make([]MigrationKey, 0)
	for _, key := range g.nodes {
		hasSameAppChild := false
		for _, child := range g.children[key] {
			if child.App == key.App {
				hasSameAppChild = true
				break
			}
		}
		if !hasSameAppChild {
			leaves = append(leaves, key)
		}
	}
	return leaves
}

func (g *plannerGraph) firstCycleComponent() []MigrationKey {
	index := 0
	indices := make(map[MigrationKey]int, len(g.nodes))
	lowLink := make(map[MigrationKey]int, len(g.nodes))
	onStack := make(map[MigrationKey]bool, len(g.nodes))
	stack := make([]MigrationKey, 0, len(g.nodes))
	components := make([][]MigrationKey, 0)

	var visit func(MigrationKey)
	visit = func(node MigrationKey) {
		index++
		indices[node] = index
		lowLink[node] = index
		stack = append(stack, node)
		onStack[node] = true

		for _, parent := range g.parents[node] {
			if indices[parent] == 0 {
				visit(parent)
				if lowLink[parent] < lowLink[node] {
					lowLink[node] = lowLink[parent]
				}
			} else if onStack[parent] && indices[parent] < lowLink[node] {
				lowLink[node] = indices[parent]
			}
		}

		if lowLink[node] != indices[node] {
			return
		}
		component := make([]MigrationKey, 0)
		for {
			member := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			onStack[member] = false
			component = append(component, member)
			if member == node {
				break
			}
		}
		sortMigrationKeys(component)
		if len(component) > 1 || g.hasSelfLoop(component[0]) {
			components = append(components, component)
		}
	}

	for _, node := range g.nodes {
		if indices[node] == 0 {
			visit(node)
		}
	}
	if len(components) == 0 {
		return nil
	}
	sort.Slice(components, func(left, right int) bool {
		return migrationKeyLess(components[left][0], components[right][0])
	})
	return append([]MigrationKey(nil), components[0]...)
}

func (g *plannerGraph) hasSelfLoop(node MigrationKey) bool {
	for _, parent := range g.parents[node] {
		if parent == node {
			return true
		}
	}
	return false
}

func internalCyclePlanningError(candidates map[MigrationKey]struct{}) error {
	members := make([]MigrationKey, 0, len(candidates))
	for key := range candidates {
		members = append(members, key)
	}
	sortMigrationKeys(members)
	return newPlanningError(CategoryGraph, CodeDependencyCycle, MigrationKey{}, MigrationKey{}, members)
}

func validMigrationKey(key MigrationKey) bool {
	return key.App != "" && key.Name != ""
}

func migrationKeyLess(left, right MigrationKey) bool {
	if left.App != right.App {
		return left.App < right.App
	}
	return left.Name < right.Name
}

func sortMigrationKeys(keys []MigrationKey) {
	sort.Slice(keys, func(left, right int) bool {
		return migrationKeyLess(keys[left], keys[right])
	})
}

func sortDependencyEdges(edges []dependencyEdge) {
	sort.Slice(edges, func(left, right int) bool {
		if edges[left].child != edges[right].child {
			return migrationKeyLess(edges[left].child, edges[right].child)
		}
		return migrationKeyLess(edges[left].parent, edges[right].parent)
	})
}
