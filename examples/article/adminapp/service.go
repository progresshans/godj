package adminapp

import (
	"context"
	"fmt"
	"reflect"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/articleapp"
)

const articleModelIdentity = "godj_conformance.article"

var articleWritableFields = []string{"title", "published", "summary"}

// TransactionalAuditWriter is the exact same-transaction write capability
// required by Article Admin. Implementations may append and prune through the
// borrowed session but must not retain it. Returning an error rolls back both
// Article DML and every audit write.
type TransactionalAuditWriter interface {
	AppendAudit(context.Context, db.Session, admin.PreparedEvent) error
}

// AuditHistoryReader is separated from the writer so adapters do not need to
// expose transaction ownership through their read surface.
type AuditHistoryReader interface {
	AuditHistory(context.Context, string, int64, int) ([]admin.AuditEntry, error)
}

// DurableAuditStore is the narrow durable audit capability consumed by the
// Admin service. Implementations own persistence and bounded history reads.
type DurableAuditStore interface {
	TransactionalAuditWriter
	AuditHistoryReader
}

// Service couples Article transactions to semantic audit events. The legacy
// constructor retains the bounded process-lifetime AuditLog for unit and
// walking-skeleton use. NewDurableService instead writes every event through
// the borrowed Article transaction and never synthesizes a post-commit append.
type Service struct {
	repository   Repository
	memoryAudit  *admin.AuditLog
	durableAudit DurableAuditStore
}

func NewService(backend Backend, audit *admin.AuditLog) (Service, error) {
	repository, err := NewRepository(backend)
	if err != nil {
		return Service{}, err
	}
	if !audit.Valid() {
		return Service{}, invalid("audit", "audit log is zero or invalid")
	}
	return Service{repository: repository, memoryAudit: audit}, nil
}

func NewDurableService(backend Backend, audit DurableAuditStore) (Service, error) {
	repository, err := NewRepository(backend)
	if err != nil {
		return Service{}, err
	}
	if interfaceNil(audit) {
		return Service{}, invalid("audit", "durable audit store is nil")
	}
	return Service{repository: repository, durableAudit: audit}, nil
}

func (s Service) List(ctx context.Context, options ListOptions) (Page, error) {
	if !s.validState() {
		return Page{}, invalid("service", "service is zero or invalid")
	}
	return s.repository.List(ctx, options)
}

func (s Service) Get(ctx context.Context, id int64) (Article, bool, error) {
	if !s.validState() {
		return Article{}, false, invalid("service", "service is zero or invalid")
	}
	return s.repository.Get(ctx, id)
}

func (s Service) Create(ctx context.Context, actorID string, input Input) (Article, error) {
	if !s.validState() {
		return Article{}, invalid("service", "service is zero or invalid")
	}
	template, err := admin.PrepareEventTemplate(
		actorID,
		articleModelIdentity,
		admin.ActionAdd,
		nil,
		input.Title,
	)
	if err != nil {
		return Article{}, fmt.Errorf("article admin prepare add audit: %w", err)
	}
	hook, publish := s.auditMutation(func(result articleapp.MutationResult) ([]admin.PreparedEvent, error) {
		item, err := singleMutationItem(result, articleapp.MutationCreate)
		if err != nil {
			return nil, err
		}
		event, ok := template.ForObject(item.Article.ID)
		if !ok {
			return nil, reconciliation("confirmed create returned an invalid object id")
		}
		return []admin.PreparedEvent{event}, nil
	})
	created, err := s.repository.withMutationHook(hook).Create(ctx, input)
	if err != nil {
		return Article{}, err
	}
	if err := publish(); err != nil {
		return Article{}, err
	}
	return created, nil
}

func (s Service) Update(ctx context.Context, actorID string, id int64, input Input) (Article, []string, error) {
	if !s.validState() {
		return Article{}, nil, invalid("service", "service is zero or invalid")
	}
	templates, err := prepareArticleChangeTemplates(actorID, input.Title)
	if err != nil {
		return Article{}, nil, err
	}
	hook, publish := s.auditMutation(func(result articleapp.MutationResult) ([]admin.PreparedEvent, error) {
		item, err := singleMutationItem(result, articleapp.MutationUpdate)
		if err != nil {
			return nil, err
		}
		mask, ok := articleChangedMask(item.ChangedFields)
		if !ok || mask == 0 {
			return nil, reconciliation("confirmed update returned an unknown changed-field set")
		}
		template, ok := templates[mask]
		if !ok {
			return nil, reconciliation("confirmed update returned an unsupported changed-field set")
		}
		event, ok := template.ForObject(item.Article.ID)
		if !ok {
			return nil, reconciliation("confirmed update returned an invalid object id")
		}
		return []admin.PreparedEvent{event}, nil
	})
	updated, changed, err := s.repository.withMutationHook(hook).Update(ctx, id, input)
	if err != nil {
		return Article{}, nil, err
	}
	if len(changed) == 0 {
		return updated, nil, nil
	}
	if err := publish(); err != nil {
		return Article{}, nil, err
	}
	return updated, append([]string(nil), changed...), nil
}

func (s Service) Delete(ctx context.Context, actorID string, id int64) (Article, error) {
	if !s.validState() {
		return Article{}, invalid("service", "service is zero or invalid")
	}
	// Validate actor/model/action before backend work. The row-derived display
	// label is validated by the hook after DML, so failure still rolls back.
	if _, err := admin.PrepareEventTemplate(actorID, articleModelIdentity, admin.ActionDelete, nil, ""); err != nil {
		return Article{}, fmt.Errorf("article admin prepare delete audit: %w", err)
	}
	hook, publish := s.auditMutation(func(result articleapp.MutationResult) ([]admin.PreparedEvent, error) {
		item, err := singleMutationItem(result, articleapp.MutationDelete)
		if err != nil {
			return nil, err
		}
		event, err := admin.PrepareEvent(
			actorID,
			articleModelIdentity,
			item.Article.ID,
			admin.ActionDelete,
			nil,
			item.Article.Title,
		)
		if err != nil {
			return nil, fmt.Errorf("article admin prepare delete audit: %w", err)
		}
		return []admin.PreparedEvent{event}, nil
	})
	deleted, err := s.repository.withMutationHook(hook).Delete(ctx, id)
	if err != nil {
		return Article{}, err
	}
	if err := publish(); err != nil {
		return Article{}, err
	}
	return deleted, nil
}

func (s Service) Publish(ctx context.Context, actorID string, ids []int64) (PublishResult, error) {
	if !s.validState() {
		return PublishResult{}, invalid("service", "service is zero or invalid")
	}
	template, err := admin.PrepareEventTemplate(
		actorID,
		articleModelIdentity,
		admin.ActionPublish,
		[]string{"published"},
		"Article publish action",
	)
	if err != nil {
		return PublishResult{}, fmt.Errorf("article admin prepare publish audit: %w", err)
	}
	hook, publish := s.auditMutation(func(result articleapp.MutationResult) ([]admin.PreparedEvent, error) {
		if result.Operation != articleapp.MutationPublish || len(result.Items) == 0 {
			return nil, reconciliation("confirmed publish returned an invalid mutation result")
		}
		events := make([]admin.PreparedEvent, 0, len(result.Items))
		var previousID int64
		for _, item := range result.Items {
			if item.Article.ID <= previousID || len(item.ChangedFields) != 1 || item.ChangedFields[0] != "published" {
				return nil, reconciliation("confirmed publish returned unordered or invalid mutation items")
			}
			event, ok := template.ForObject(item.Article.ID)
			if !ok {
				return nil, reconciliation("confirmed publish returned an invalid object id")
			}
			events = append(events, event)
			previousID = item.Article.ID
		}
		return events, nil
	})
	result, err := s.repository.withMutationHook(hook).Publish(ctx, ids)
	if err != nil {
		return PublishResult{}, err
	}
	if result.Matched() == 0 {
		return result, nil
	}
	if err := publish(); err != nil {
		return PublishResult{}, err
	}
	return result, nil
}

// History preserves the process-lifetime convenience API. Durable history
// requires context and is therefore exposed only through HistoryLimited.
func (s Service) History(id int64) []admin.AuditEntry {
	if !s.validState() || s.memoryAudit == nil {
		return nil
	}
	return s.memoryAudit.ForObject(articleModelIdentity, id)
}

func (s Service) HistoryLimited(ctx context.Context, id int64, limit int) ([]admin.AuditEntry, error) {
	if !s.validState() {
		return nil, invalid("service", "service is zero or invalid")
	}
	if s.durableAudit != nil {
		entries, err := s.durableAudit.AuditHistory(ctx, articleModelIdentity, id, limit)
		if err != nil {
			return nil, err
		}
		result := make([]admin.AuditEntry, len(entries))
		for index := range entries {
			result[index] = entries[index].Clone()
		}
		return result, nil
	}
	return s.memoryAudit.ForObjectLimited(ctx, articleModelIdentity, id, limit)
}

func (s Service) validState() bool {
	if !s.repository.validState() {
		return false
	}
	memory := s.memoryAudit != nil && s.memoryAudit.Valid()
	durable := !interfaceNil(s.durableAudit)
	return memory != durable
}

type auditEventFactory func(articleapp.MutationResult) ([]admin.PreparedEvent, error)

func (s Service) auditMutation(factory auditEventFactory) (articleapp.MutationHook, func() error) {
	var pending []admin.PreparedEvent
	hook := func(ctx context.Context, session db.Session, result articleapp.MutationResult) error {
		events, err := factory(result.Clone())
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return reconciliation("confirmed mutation produced no audit events")
		}
		if s.durableAudit != nil {
			for _, event := range events {
				if err := s.durableAudit.AppendAudit(ctx, session, event); err != nil {
					return fmt.Errorf("article admin transactional audit append: %w", err)
				}
			}
			return nil
		}
		pending = append([]admin.PreparedEvent(nil), events...)
		return nil
	}
	publish := func() error {
		if s.durableAudit != nil || len(pending) == 0 {
			return nil
		}
		if !s.memoryAudit.Valid() {
			return reconciliation("audit log is unavailable after confirmed commit")
		}
		for _, event := range pending {
			if _, ok := s.memoryAudit.Append(event); !ok {
				return reconciliation("audit append invariant failed after confirmed commit")
			}
		}
		return nil
	}
	return hook, publish
}

func singleMutationItem(result articleapp.MutationResult, operation articleapp.MutationOperation) (articleapp.MutationItem, error) {
	if result.Operation != operation || len(result.Items) != 1 {
		return articleapp.MutationItem{}, reconciliation("confirmed write returned an invalid mutation result")
	}
	return result.Items[0].Clone(), nil
}

func reconciliation(detail string) error {
	return fmt.Errorf("article admin: %s: %w", detail, admin.ErrReconciliationRequired)
}

func prepareArticleChangeTemplates(actorID, displayLabel string) (map[int]admin.PreparedEventTemplate, error) {
	result := make(map[int]admin.PreparedEventTemplate, 7)
	for mask := 1; mask < 1<<len(articleWritableFields); mask++ {
		fields := make([]string, 0, len(articleWritableFields))
		for index, field := range articleWritableFields {
			if mask&(1<<index) != 0 {
				fields = append(fields, field)
			}
		}
		template, err := admin.PrepareEventTemplate(
			actorID,
			articleModelIdentity,
			admin.ActionChange,
			fields,
			displayLabel,
		)
		if err != nil {
			return nil, fmt.Errorf("article admin prepare change audit: %w", err)
		}
		result[mask] = template
	}
	return result, nil
}

func articleChangedMask(fields []string) (int, bool) {
	mask := 0
	for _, changed := range fields {
		matched := false
		for index, field := range articleWritableFields {
			if changed != field {
				continue
			}
			bit := 1 << index
			if mask&bit != 0 {
				return 0, false
			}
			mask |= bit
			matched = true
			break
		}
		if !matched {
			return 0, false
		}
	}
	return mask, true
}

func interfaceNil(value any) bool {
	if value == nil {
		return true
	}
	reflected := reflect.ValueOf(value)
	switch reflected.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return reflected.IsNil()
	default:
		return false
	}
}
