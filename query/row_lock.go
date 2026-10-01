package query

import (
	"slices"
	"strings"
)

// LockStrength and LockWaitPolicy describe row locking independently of SQL.
// A backend owns support, transaction requirements and the physical lock.
type LockStrength string
type LockWaitPolicy string

const (
	LockForUpdate      LockStrength   = "update"
	LockForNoKeyUpdate LockStrength   = "no_key_update"
	LockWait           LockWaitPolicy = "wait"
	LockNoWait         LockWaitPolicy = "nowait"
	LockSkipLocked     LockWaitPolicy = "skip_locked"
)

// RowLockTarget identifies one occurrence in the query's FROM scope, rather
// than a selected column or a SQL alias. A zero target is invalid.
type RowLockTarget struct {
	self bool
	path RelationPath
}

func LockSelf() RowLockTarget { return RowLockTarget{self: true} }

// LockRelated owns a canonical single-valued route. Selecting different
// columns from that occurrence cannot widen or erase this target. The compiler
// must require this exact route to already exist in the query's FROM scope.
func LockRelated(path RelationPath) (RowLockTarget, error) {
	if err := validateLockPath(path); err != nil {
		return RowLockTarget{}, err
	}
	path.hops = slices.Clone(path.hops)
	path.keys = slices.Clone(path.keys)
	return RowLockTarget{path: path}, nil
}

func validateLockPath(path RelationPath) error {
	if !path.SingleValued() {
		return invalidPlanError("row lock target requires a single-valued route")
	}
	if err := path.Validate(); err != nil {
		return err
	}
	return path.validateSelectionMetadata()
}

func (target RowLockTarget) Self() bool          { return target.self }
func (target RowLockTarget) Hops() []RelationHop { return target.path.Hops() }

// RelationPath retains the supplied metadata for validation; its terminal
// column does not participate in target identity or require column selection.
func (target RowLockTarget) RelationPath() (RelationPath, bool) { return target.path, !target.self }
func (target RowLockTarget) Equal(other RowLockTarget) bool {
	return target.self == other.self && slices.Equal(target.path.hops, other.path.hops) && slices.Equal(target.path.keys, other.path.keys)
}

// RowLock is immutable. No targets means all lockable rows in the SELECT;
// explicit targets always remain explicit, including when projecting values.
type RowLock struct {
	strength LockStrength
	wait     LockWaitPolicy
	targets  []RowLockTarget
}

func NewRowLock(strength LockStrength, wait LockWaitPolicy, targets ...RowLockTarget) (RowLock, error) {
	lock := RowLock{strength: strength, wait: wait, targets: targets}
	if err := lock.validate(); err != nil {
		return RowLock{}, err
	}
	lock.targets = slices.Clone(targets)
	slices.SortFunc(lock.targets, func(left, right RowLockTarget) int {
		return strings.Compare(projectionRouteKey(left.path.hops), projectionRouteKey(right.path.hops))
	})
	for index := 1; index < len(lock.targets); index++ {
		previous, current := lock.targets[index-1], lock.targets[index]
		if projectionRouteKey(previous.path.hops) == projectionRouteKey(current.path.hops) && !previous.Equal(current) {
			return RowLock{}, invalidPlanError("row lock targets contain conflicting route metadata")
		}
	}
	lock.targets = slices.CompactFunc(lock.targets, RowLockTarget.Equal)
	return lock, nil
}

func (lock RowLock) Strength() LockStrength     { return lock.strength }
func (lock RowLock) WaitPolicy() LockWaitPolicy { return lock.wait }
func (lock RowLock) Targets() []RowLockTarget   { return slices.Clone(lock.targets) }
func (lock RowLock) Equal(other RowLock) bool {
	return lock.strength == other.strength && lock.wait == other.wait && slices.EqualFunc(lock.targets, other.targets, RowLockTarget.Equal)
}

func (lock RowLock) validate() error {
	if lock.strength != LockForUpdate && lock.strength != LockForNoKeyUpdate {
		return invalidPlanError("row lock strength is invalid")
	}
	if lock.wait != LockWait && lock.wait != LockNoWait && lock.wait != LockSkipLocked {
		return invalidPlanError("row lock wait policy is invalid")
	}
	if len(lock.targets) > MaximumRelatedLockTargets {
		return invalidPlanError("row lock exceeds its target bound")
	}
	for _, target := range lock.targets {
		if target.self {
			if !target.path.Equal(RelationPath{}) {
				return invalidPlanError("root row lock target contains a related route")
			}
			continue
		}
		if err := validateLockPath(target.path); err != nil {
			return err
		}
	}
	return nil
}

// MaximumRelatedLockTargets bounds validation before canonical coalescing.
const MaximumRelatedLockTargets = 1024

func (p Plan) RowLock() (RowLock, bool) {
	if p.rowLock == nil {
		return RowLock{}, false
	}
	return *p.rowLock, true
}

func (p Plan) WithRowLock(lock RowLock) (Plan, error) {
	p = p.WithoutCollectionFilterReuse()
	p.rowLock = &lock
	if err := p.ValidateRowLock(); err != nil {
		return Plan{}, err
	}
	return p, nil
}

func (p Plan) WithoutRowLock() Plan {
	p.rowLock = nil
	return p
}

// ValidateRowLock binds root-owned declarations before any backend can elide
// an empty result. Join occurrence availability remains a compiler concern.
func (p Plan) ValidateRowLock() error {
	lock, present := p.RowLock()
	if !present {
		return nil
	}
	if err := lock.validate(); err != nil {
		return err
	}
	if p.result.Kind() == ResultAggregate {
		return invalidPlanError("row locks require rows rather than an aggregate result")
	}
	for _, target := range lock.targets {
		if target.self {
			continue
		}
		first := target.path.hops[0]
		root, table := first.From()
		if table != p.table {
			return invalidPlanError("row lock target belongs to a different source table")
		}
		if len(target.path.keys) != 0 && !slices.Contains(p.sourceFields, target.path.keys[0]) {
			return invalidPlanError("row lock route disagrees with the root primary key")
		}
		for _, hop := range target.path.hops {
			if hop.Source() == root && !slices.Contains(p.sourceFields, NewFieldRef(hop.Field(), hop.SourceColumn(), FieldInteger, hop.Nullable())) {
				return invalidPlanError("row lock source key disagrees with model metadata")
			}
			if hop.Target() == root && !containsPlanIntegerColumn(p.sourceFields, hop.TargetPrimaryKeyColumn()) {
				return invalidPlanError("row lock target key disagrees with root metadata")
			}
		}
	}
	return nil
}
