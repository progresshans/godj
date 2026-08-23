// Package admin provides the reusable, bounded model administration runtime.
// It preserves semantic workflows rather than Django's private object graph or
// exact DOM/CSS. Model registration and HTTP integration live alongside this
// process-lifetime audit primitive.
package admin

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	DefaultAuditCapacity  = 10_000
	MaximumAuditCapacity  = 1_000_000
	MaximumActorIDBytes   = 128
	MaximumModelBytes     = 128
	MaximumDisplayBytes   = 1024
	MaximumChangedFields  = 128
	MaximumHistoryEntries = 100
)

type Action string

const (
	ActionAdd     Action = "add"
	ActionChange  Action = "change"
	ActionDelete  Action = "delete"
	ActionPublish Action = "publish"
)

// PreparedEvent is validated before a database mutation starts. Its fields
// are private so appending it after a confirmed commit is allocation-only and
// cannot fail due to user-controlled event data.
type PreparedEvent struct {
	actorID      string
	model        string
	objectID     int64
	action       Action
	changed      []string
	displayLabel string
}

// PreparedEventTemplate validates every user-controlled event property before
// a create transaction begins. A confirmed generated primary key can then be
// attached without parsing or allocating user-controlled metadata.
type PreparedEventTemplate struct {
	actorID      string
	model        string
	action       Action
	changed      []string
	displayLabel string
}

// PrepareEvent validates and snapshots semantic audit data. Raw field values,
// credentials, session IDs, and tokens have no representation in this type.
func PrepareEvent(actorID, model string, objectID int64, action Action, changedFields []string, displayLabel string) (PreparedEvent, error) {
	template, err := PrepareEventTemplate(actorID, model, action, changedFields, displayLabel)
	if err != nil {
		return PreparedEvent{}, err
	}
	if objectID <= 0 {
		return PreparedEvent{}, fmt.Errorf("admin audit: object id must be positive")
	}
	event, _ := template.ForObject(objectID)
	return event, nil
}

func PrepareEventTemplate(actorID, model string, action Action, changedFields []string, displayLabel string) (PreparedEventTemplate, error) {
	if !validBoundedText(actorID, MaximumActorIDBytes) {
		return PreparedEventTemplate{}, fmt.Errorf("admin audit: actor id is empty or invalid")
	}
	if !validDottedIdentifier(model, MaximumModelBytes) {
		return PreparedEventTemplate{}, fmt.Errorf("admin audit: model identity is invalid")
	}
	switch action {
	case ActionAdd, ActionChange, ActionDelete, ActionPublish:
	default:
		return PreparedEventTemplate{}, fmt.Errorf("admin audit: action is invalid")
	}
	if len(changedFields) > MaximumChangedFields {
		return PreparedEventTemplate{}, fmt.Errorf("admin audit: changed field count exceeds the supported limit")
	}
	changed := make([]string, len(changedFields))
	seen := make(map[string]struct{}, len(changedFields))
	for index, field := range changedFields {
		if !validIdentifier(field, MaximumModelBytes) {
			return PreparedEventTemplate{}, fmt.Errorf("admin audit: changed field at index %d is invalid", index)
		}
		if _, exists := seen[field]; exists {
			return PreparedEventTemplate{}, fmt.Errorf("admin audit: changed field %q is duplicated", field)
		}
		seen[field] = struct{}{}
		changed[index] = field
	}
	if !utf8.ValidString(displayLabel) || len(displayLabel) > MaximumDisplayBytes || containsUnsafeDisplayControl(displayLabel) {
		return PreparedEventTemplate{}, fmt.Errorf("admin audit: display label is invalid")
	}
	return PreparedEventTemplate{
		actorID:      actorID,
		model:        model,
		action:       action,
		changed:      changed,
		displayLabel: displayLabel,
	}, nil
}

// ForObject attaches a confirmed positive primary key. False is possible only
// for a backend contract violation and does not inspect user-controlled data.
func (template PreparedEventTemplate) ForObject(objectID int64) (PreparedEvent, bool) {
	if template.actorID == "" || template.model == "" || objectID <= 0 {
		return PreparedEvent{}, false
	}
	return PreparedEvent{
		actorID:      template.actorID,
		model:        template.model,
		objectID:     objectID,
		action:       template.action,
		changed:      append([]string(nil), template.changed...),
		displayLabel: template.displayLabel,
	}, true
}

// AuditEntry is an immutable semantic event snapshot. Sequence is process
// local and monotonically increasing; no timestamp or backend-specific admin
// log primary key is claimed.
type AuditEntry struct {
	Sequence      uint64
	ActorID       string
	Model         string
	ObjectID      int64
	Action        Action
	ChangedFields []string
	DisplayLabel  string
}

func (entry AuditEntry) Clone() AuditEntry {
	entry.ChangedFields = append([]string(nil), entry.ChangedFields...)
	return entry
}

// AuditLog is a concurrent, process-lifetime bounded ring. Once constructed,
// Append of a PreparedEvent cannot fail. Oldest entries are evicted when the
// configured capacity is reached; sequence numbers are never reused.
type AuditLog struct {
	state *auditState
}

type auditState struct {
	mu       sync.RWMutex
	entries  []AuditEntry
	start    int
	length   int
	capacity int
	next     uint64
}

func NewAuditLog(capacity int) (*AuditLog, error) {
	if capacity == 0 {
		capacity = DefaultAuditCapacity
	}
	if capacity < 0 || capacity > MaximumAuditCapacity {
		return nil, fmt.Errorf("admin audit: capacity is outside the supported range")
	}
	return &AuditLog{state: &auditState{
		entries:  make([]AuditEntry, capacity),
		capacity: capacity,
		next:     1,
	}}, nil
}

// Valid reports whether log was constructed by NewAuditLog. The capacity and
// backing slice are immutable after construction, so this check is safe during
// concurrent appends and reads.
func (log *AuditLog) Valid() bool {
	return log != nil && log.state != nil && log.state.capacity > 0 && len(log.state.entries) == log.state.capacity
}

// Append publishes one prevalidated event. Callers invoke it only after a
// confirmed database commit. Commit-outcome-unknown must not call Append.
func (log *AuditLog) Append(event PreparedEvent) (AuditEntry, bool) {
	if !log.Valid() || event.actorID == "" || event.model == "" || event.objectID <= 0 {
		return AuditEntry{}, false
	}
	state := log.state
	state.mu.Lock()
	defer state.mu.Unlock()
	entry := AuditEntry{
		Sequence:      state.next,
		ActorID:       event.actorID,
		Model:         event.model,
		ObjectID:      event.objectID,
		Action:        event.action,
		ChangedFields: append([]string(nil), event.changed...),
		DisplayLabel:  event.displayLabel,
	}
	state.next++
	position := (state.start + state.length) % state.capacity
	if state.length == state.capacity {
		position = state.start
		state.start = (state.start + 1) % state.capacity
	} else {
		state.length++
	}
	state.entries[position] = entry
	return entry.Clone(), true
}

func (log *AuditLog) All() []AuditEntry {
	return log.selectEntries("", 0)
}

func (log *AuditLog) ForObject(model string, objectID int64) []AuditEntry {
	if !validDottedIdentifier(model, MaximumModelBytes) || objectID <= 0 {
		return nil
	}
	return log.selectEntries(model, objectID)
}

func (log *AuditLog) Len() int {
	if !log.Valid() {
		return 0
	}
	state := log.state
	state.mu.RLock()
	defer state.mu.RUnlock()
	return state.length
}

func (log *AuditLog) selectEntries(model string, objectID int64) []AuditEntry {
	if !log.Valid() {
		return nil
	}
	state := log.state
	state.mu.RLock()
	defer state.mu.RUnlock()
	var result []AuditEntry
	for offset := 0; offset < state.length; offset++ {
		entry := state.entries[(state.start+offset)%state.capacity]
		if model != "" && (entry.Model != model || entry.ObjectID != objectID) {
			continue
		}
		result = append(result, entry.Clone())
	}
	return result
}

// ForObjectLimited returns at most the newest limit matching events in
// ascending sequence order. It bounds result allocation and checks ctx while
// scanning a large process-lifetime ring.
func (log *AuditLog) ForObjectLimited(ctx context.Context, model string, objectID int64, limit int) ([]AuditEntry, error) {
	if ctx == nil {
		return nil, fmt.Errorf("admin audit: context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !log.Valid() || !validDottedIdentifier(model, MaximumModelBytes) || objectID <= 0 || limit < 1 || limit > MaximumHistoryEntries {
		return nil, fmt.Errorf("admin audit: limited object history request is invalid")
	}
	state := log.state
	state.mu.RLock()
	defer state.mu.RUnlock()
	result := make([]AuditEntry, 0, limit)
	for offset := state.length - 1; offset >= 0 && len(result) < limit; offset-- {
		if offset&255 == 0 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
		}
		entry := state.entries[(state.start+offset)%state.capacity]
		if entry.Model != model || entry.ObjectID != objectID {
			continue
		}
		result = append(result, entry.Clone())
	}
	for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
		result[left], result[right] = result[right], result[left]
	}
	return result, nil
}

func validDottedIdentifier(value string, maximum int) bool {
	if value == "" || len(value) > maximum || strings.HasPrefix(value, ".") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if !validIdentifier(part, maximum) {
			return false
		}
	}
	return true
}

func validIdentifier(value string, maximum int) bool {
	if value == "" || len(value) > maximum {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			(index > 0 && character >= '0' && character <= '9') || (index > 0 && character == '_') {
			continue
		}
		return false
	}
	return true
}

func validBoundedText(value string, maximum int) bool {
	return value != "" && len(value) <= maximum && utf8.ValidString(value) && !containsUnsafeControl(value)
}

func containsUnsafeControl(value string) bool {
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}

func containsUnsafeDisplayControl(value string) bool {
	for _, character := range value {
		if character == '\t' || character == '\n' || character == '\r' {
			continue
		}
		if character < 0x20 || character == 0x7f {
			return true
		}
	}
	return false
}
