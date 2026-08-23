package adminapp

import (
	"context"
	"fmt"

	"github.com/progresshans/godj/admin"
)

const articleModelIdentity = "godj_conformance.article"

var articleWritableFields = []string{"title", "published", "summary"}

// Service couples confirmed Article transactions to process-lifetime semantic
// audit events. It never appends an event for rollback or commit-outcome-
// unknown, and it never retries a repository write.
type Service struct {
	repository Repository
	audit      *admin.AuditLog
}

func NewService(backend Backend, audit *admin.AuditLog) (Service, error) {
	repository, err := NewRepository(backend)
	if err != nil {
		return Service{}, err
	}
	if audit == nil {
		return Service{}, invalid("audit", "audit log is nil")
	}
	return Service{repository: repository, audit: audit}, nil
}

func (s Service) List(ctx context.Context, options ListOptions) (Page, error) {
	if s.audit == nil {
		return Page{}, invalid("service", "service is zero or invalid")
	}
	return s.repository.List(ctx, options)
}

func (s Service) Get(ctx context.Context, id int64) (Article, bool, error) {
	if s.audit == nil {
		return Article{}, false, invalid("service", "service is zero or invalid")
	}
	return s.repository.Get(ctx, id)
}

func (s Service) Create(ctx context.Context, actorID string, input Input) (Article, error) {
	template, err := admin.PrepareEventTemplate(
		actorID,
		articleModelIdentity,
		admin.ActionAdd,
		articleWritableFields,
		input.Title,
	)
	if err != nil {
		return Article{}, fmt.Errorf("article admin prepare add audit: %w", err)
	}
	created, err := s.repository.Create(ctx, input)
	if err != nil {
		return Article{}, err
	}
	if err := s.appendConfirmed(template, created.ID); err != nil {
		return Article{}, err
	}
	return created, nil
}

func (s Service) Update(ctx context.Context, actorID string, id int64, input Input) (Article, []string, error) {
	templates, err := prepareArticleChangeTemplates(actorID, input.Title)
	if err != nil {
		return Article{}, nil, err
	}
	updated, changed, err := s.repository.Update(ctx, id, input)
	if err != nil {
		return Article{}, nil, err
	}
	if len(changed) == 0 {
		return updated, nil, nil
	}
	mask := articleChangedMask(changed)
	template, ok := templates[mask]
	if !ok {
		return Article{}, nil, fmt.Errorf("article admin: confirmed update returned an unknown changed-field set")
	}
	if err := s.appendConfirmed(template, updated.ID); err != nil {
		return Article{}, nil, err
	}
	return updated, append([]string(nil), changed...), nil
}

func (s Service) Delete(ctx context.Context, actorID string, id int64) (Article, error) {
	current, found, err := s.repository.Get(ctx, id)
	if err != nil {
		return Article{}, err
	}
	if !found {
		return Article{}, notFound(id)
	}
	template, err := admin.PrepareEventTemplate(
		actorID,
		articleModelIdentity,
		admin.ActionDelete,
		nil,
		current.Title,
	)
	if err != nil {
		return Article{}, fmt.Errorf("article admin prepare delete audit: %w", err)
	}
	deleted, err := s.repository.Delete(ctx, id)
	if err != nil {
		return Article{}, err
	}
	if err := s.appendConfirmed(template, deleted.ID); err != nil {
		return Article{}, err
	}
	return deleted, nil
}

func (s Service) Publish(ctx context.Context, actorID string, ids []int64) (PublishResult, error) {
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
	result, err := s.repository.Publish(ctx, ids)
	if err != nil {
		return PublishResult{}, err
	}
	for _, id := range result.MatchedIDs {
		if err := s.appendConfirmed(template, id); err != nil {
			return PublishResult{}, err
		}
	}
	return result, nil
}

func (s Service) History(id int64) []admin.AuditEntry {
	if s.audit == nil {
		return nil
	}
	return s.audit.ForObject(articleModelIdentity, id)
}

func (s Service) appendConfirmed(template admin.PreparedEventTemplate, objectID int64) error {
	if s.audit == nil {
		return fmt.Errorf("article admin: audit log is unavailable after confirmed commit")
	}
	event, ok := template.ForObject(objectID)
	if !ok {
		return fmt.Errorf("article admin: confirmed write returned an invalid object id")
	}
	if _, ok := s.audit.Append(event); !ok {
		return fmt.Errorf("article admin: audit append invariant failed after confirmed commit")
	}
	return nil
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

func articleChangedMask(fields []string) int {
	mask := 0
	for _, changed := range fields {
		for index, field := range articleWritableFields {
			if changed == field {
				mask |= 1 << index
				break
			}
		}
	}
	return mask
}
