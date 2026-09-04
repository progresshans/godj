package godj

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/conformance/internal/protocol"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/article/adminapp"
	"github.com/progresshans/godj/query"
)

type gdj0046ArticleCreateResult struct {
	article adminapp.Article
	err     error
}

type gdj0046FailingAuditStore struct {
	delegate *systemStateAuditAdapter
	failure  error
	calls    int
}

type gdj0046ArticleSnapshot struct {
	id           int64
	title        string
	published    bool
	summary      string
	summaryValid bool
}

type gdj0046AuditSnapshot struct {
	sequence      int64
	actorID       string
	model         string
	objectID      int64
	action        string
	changedFields string
	displayLabel  string
}

func gdj0046ReadArticleSnapshots(
	ctx context.Context,
	backend db.Queryer,
) ([]gdj0046ArticleSnapshot, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	title := query.NewFieldRef("title", "title", query.FieldString, false)
	published := query.NewFieldRef("published", "published", query.FieldBoolean, false)
	summary := query.NewFieldRef("summary", "summary", query.FieldString, true)
	plan, err := query.NewPlan(systemStateArticleTable, []query.FieldRef{
		id,
		title,
		published,
		summary,
	}).WithLimit(64)
	if err != nil {
		return nil, err
	}
	plan = plan.WithOrderings(query.NewOrdering(id, query.Ascending))
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	var result []gdj0046ArticleSnapshot
	for rows.Next() {
		var snapshot gdj0046ArticleSnapshot
		var nullableSummary sql.NullString
		if err := rows.Scan(&snapshot.id, &snapshot.title, &snapshot.published, &nullableSummary); err != nil {
			_ = rows.Close()
			return nil, err
		}
		snapshot.summary = nullableSummary.String
		snapshot.summaryValid = nullableSummary.Valid
		result = append(result, snapshot)
	}
	return result, errors.Join(rows.Err(), rows.Close())
}

func gdj0046ReadAuditSnapshots(
	ctx context.Context,
	backend db.Queryer,
) ([]gdj0046AuditSnapshot, error) {
	id := query.NewFieldRef("id", "id", query.FieldInteger, false)
	actorID := query.NewFieldRef("actor_id", "actor_id", query.FieldString, false)
	model := query.NewFieldRef("model", "model", query.FieldString, false)
	objectID := query.NewFieldRef("object_id", "object_id", query.FieldString, false)
	action := query.NewFieldRef("action", "action", query.FieldString, false)
	changedFields := query.NewFieldRef("changed_fields", "changed_fields", query.FieldString, false)
	displayLabel := query.NewFieldRef("display_label", "display_label", query.FieldString, false)
	plan, err := query.NewPlan(systemStateAuditTable, []query.FieldRef{
		id,
		actorID,
		model,
		objectID,
		action,
		changedFields,
		displayLabel,
	}).WithLimit(64)
	if err != nil {
		return nil, err
	}
	plan = plan.WithOrderings(query.NewOrdering(id, query.Ascending))
	rows, err := backend.Query(ctx, plan)
	if err != nil {
		return nil, err
	}
	var result []gdj0046AuditSnapshot
	for rows.Next() {
		var snapshot gdj0046AuditSnapshot
		var encodedObjectID string
		if err := rows.Scan(
			&snapshot.sequence,
			&snapshot.actorID,
			&snapshot.model,
			&encodedObjectID,
			&snapshot.action,
			&snapshot.changedFields,
			&snapshot.displayLabel,
		); err != nil {
			_ = rows.Close()
			return nil, err
		}
		snapshot.objectID, err = strconv.ParseInt(encodedObjectID, 10, 64)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("parse durable audit object id %q: %w", encodedObjectID, err)
		}
		result = append(result, snapshot)
	}
	return result, errors.Join(rows.Err(), rows.Close())
}

func gdj0046ValidateArticleAuditSnapshots(
	articles []gdj0046ArticleSnapshot,
	audits []gdj0046AuditSnapshot,
	actor string,
	capacity int,
) error {
	if len(articles) != 4 || len(audits) != capacity || capacity > len(articles) {
		return fmt.Errorf("Article/audit snapshot cardinality = %d/%d, want 4/%d", len(articles), len(audits), capacity)
	}
	articleByID := make(map[int64]gdj0046ArticleSnapshot, len(articles))
	var previousArticleID int64
	for _, article := range articles {
		if article.id <= previousArticleID || article.title == "" {
			return fmt.Errorf("Article identity ordering is invalid: %#v", articles)
		}
		previousArticleID = article.id
		articleByID[article.id] = article
	}
	retainedArticles := articles[len(articles)-capacity:]
	var previousSequence int64
	for index, audit := range audits {
		article, found := articleByID[audit.objectID]
		if audit.sequence <= previousSequence || !found ||
			audit.objectID != retainedArticles[index].id ||
			audit.actorID != actor || audit.model != "godj_conformance.article" ||
			audit.action != string(admin.ActionAdd) || audit.changedFields == "" ||
			audit.displayLabel != article.title {
			return fmt.Errorf("audit %d does not identify its successful Article: audit=%#v article=%#v", index, audit, article)
		}
		previousSequence = audit.sequence
	}
	return nil
}

// systemStateAuditAdapter keeps the fault wrapper's dependency narrow while
// still dispatching to the real durable Runtime implementation.
type systemStateAuditAdapter struct {
	append func(context.Context, db.Session, admin.PreparedEvent) error
	read   func(context.Context, string, int64, int) ([]admin.AuditEntry, error)
}

func (store *gdj0046FailingAuditStore) AppendAudit(
	ctx context.Context,
	session db.Session,
	event admin.PreparedEvent,
) error {
	store.calls++
	if err := store.delegate.append(ctx, session, event); err != nil {
		return err
	}
	return store.failure
}

func (store *gdj0046FailingAuditStore) AuditHistory(
	ctx context.Context,
	model string,
	objectID int64,
	limit int,
) ([]admin.AuditEntry, error) {
	return store.delegate.read(ctx, model, objectID, limit)
}

func systemStateConcurrentArticleAudit(
	ctx context.Context,
	contract protocol.Contract,
) (protocol.Observation, error) {
	const auditCapacity = 3
	pair, err := newGDJ0046RuntimePair(ctx, 8, auditCapacity, true)
	if err != nil {
		return protocol.Observation{}, err
	}
	defer pair.cleanup()
	holderService, err := adminapp.NewDurableService(pair.holder, pair.holder)
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderService, err := adminapp.NewDurableService(pair.contender, pair.contender)
	if err != nil {
		return protocol.Observation{}, err
	}
	actor := pair.config.PrincipalID
	if _, err := holderService.Create(ctx, actor, adminapp.Input{Title: "GDJ-0046 seed one"}); err != nil {
		return protocol.Observation{}, err
	}
	if _, err := holderService.Create(ctx, actor, adminapp.Input{Title: "GDJ-0046 seed two"}); err != nil {
		return protocol.Observation{}, err
	}

	barrier := pair.backends.arm()
	holderResult := make(chan gdj0046ArticleCreateResult, 1)
	contenderResult := make(chan gdj0046ArticleCreateResult, 1)
	go func() {
		article, err := holderService.Create(ctx, actor, adminapp.Input{Title: "GDJ-0046 holder"})
		holderResult <- gdj0046ArticleCreateResult{article: article, err: err}
	}()
	if err := gdj0046WaitSignal(ctx, barrier.holderEntered, "Article holder callback"); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	go func() {
		article, err := contenderService.Create(ctx, actor, adminapp.Input{Title: "GDJ-0046 contender"})
		contenderResult <- gdj0046ArticleCreateResult{article: article, err: err}
	}()
	if err := gdj0046AssertBlocked(ctx, pair.backends, barrier); err != nil {
		barrier.release()
		return protocol.Observation{}, err
	}
	barrier.release()
	holderCreated, err := gdj0046WaitResult(ctx, holderResult, "Article holder result")
	if err != nil {
		return protocol.Observation{}, err
	}
	contenderCreated, err := gdj0046WaitResult(ctx, contenderResult, "Article contender result")
	if err != nil {
		return protocol.Observation{}, err
	}
	pair.backends.disarm()
	if holderCreated.err != nil || contenderCreated.err != nil || holderCreated.article.ID <= 0 ||
		contenderCreated.article.ID <= holderCreated.article.ID ||
		barrier.holderCallbackCalls.Load() != 1 || barrier.contenderCallbackCalls.Load() != 1 {
		return protocol.Observation{}, fmt.Errorf(
			"concurrent Article facts drifted: holder=%+v contender=%+v callbacks=%d/%d",
			holderCreated,
			contenderCreated,
			barrier.holderCallbackCalls.Load(),
			barrier.contenderCallbackCalls.Load(),
		)
	}
	articlesBeforeFault, err := gdj0046ReadArticleSnapshots(ctx, pair.holder)
	if err != nil {
		return protocol.Observation{}, err
	}
	auditsBeforeFault, err := gdj0046ReadAuditSnapshots(ctx, pair.holder)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := gdj0046ValidateArticleAuditSnapshots(
		articlesBeforeFault,
		auditsBeforeFault,
		actor,
		auditCapacity,
	); err != nil {
		return protocol.Observation{}, err
	}
	articleRowsBeforeFault := len(articlesBeforeFault)
	auditRowsBeforeFault := len(auditsBeforeFault)
	globalBoundPreserved := auditRowsBeforeFault == auditCapacity && articleRowsBeforeFault == 4
	if !globalBoundPreserved {
		return protocol.Observation{}, fmt.Errorf(
			"global Article/audit inventory = %d/%d, want 4/%d",
			articleRowsBeforeFault,
			auditRowsBeforeFault,
			auditCapacity,
		)
	}

	failure := errors.New("gdj0046 injected transactional audit failure")
	adapter := &systemStateAuditAdapter{
		append: pair.holder.AppendAudit,
		read:   pair.holder.AuditHistory,
	}
	failingAudit := &gdj0046FailingAuditStore{delegate: adapter, failure: failure}
	failingService, err := adminapp.NewDurableService(pair.holder, failingAudit)
	if err != nil {
		return protocol.Observation{}, err
	}
	_, faultErr := failingService.Create(ctx, actor, adminapp.Input{Title: "GDJ-0046 rollback"})
	if !errors.Is(faultErr, failure) || failingAudit.calls != 1 {
		return protocol.Observation{}, fmt.Errorf("audit fault = calls %d error %v", failingAudit.calls, faultErr)
	}
	articlesAfterFault, err := gdj0046ReadArticleSnapshots(ctx, pair.contender)
	if err != nil {
		return protocol.Observation{}, err
	}
	auditsAfterFault, err := gdj0046ReadAuditSnapshots(ctx, pair.contender)
	if err != nil {
		return protocol.Observation{}, err
	}
	if err := gdj0046ValidateArticleAuditSnapshots(
		articlesAfterFault,
		auditsAfterFault,
		actor,
		auditCapacity,
	); err != nil {
		return protocol.Observation{}, err
	}
	articleRowsAfterFault := len(articlesAfterFault)
	auditRowsAfterFault := len(auditsAfterFault)
	articleDelta := articleRowsAfterFault - articleRowsBeforeFault
	auditDelta := auditRowsAfterFault - auditRowsBeforeFault
	orphanAuditRows := 0
	if auditDelta > articleDelta && auditDelta > 0 {
		orphanAuditRows = auditDelta - max(articleDelta, 0)
	}
	partialCommits := 0
	if articleDelta != 0 {
		partialCommits++
	}
	if auditDelta != 0 {
		partialCommits++
	}
	pruneEscapes := max(auditRowsAfterFault-auditCapacity, 0)
	atomicCalls := pair.backends.holder.atomicCalls.Load() + pair.backends.contender.atomicCalls.Load()
	// Two seeds, two concurrent writes, and one injected fault are the only
	// top-level product mutations in this scenario.
	automaticRetries := max64(atomicCalls-5, 0)
	identityPreserved := slices.Equal(articlesBeforeFault, articlesAfterFault) &&
		slices.Equal(auditsBeforeFault, auditsAfterFault)
	atomic := articleDelta == 0 && auditDelta == 0 && orphanAuditRows == 0 && identityPreserved
	if !atomic || partialCommits != 0 || pruneEscapes != 0 || automaticRetries != 0 {
		return protocol.Observation{}, fmt.Errorf(
			"Article rollback facts drifted: delta=%d/%d identity=%v orphan=%d partial=%d prune=%d retries=%d",
			articleDelta,
			auditDelta,
			identityPreserved,
			orphanAuditRows,
			partialCommits,
			pruneEscapes,
			automaticRetries,
		)
	}
	result := protocol.Object(map[string]protocol.Value{
		"article_and_audit_atomic":       protocol.Boolean(atomic),
		"fault_outcome":                  protocol.String("rolled_back"),
		"global_history_bound_preserved": protocol.Boolean(globalBoundPreserved && pruneEscapes == 0),
	})
	dbState := protocol.Object(map[string]protocol.Value{
		"article_rows_after_fault": systemStateInt(articleDelta),
		"audit_rows_after_fault":   systemStateInt(auditDelta),
		"orphan_audit_rows":        systemStateInt(orphanAuditRows),
	})
	return systemStateObservation(contract, result, dbState, protocol.Object(map[string]protocol.Value{
		"automatic_retries":   systemStateInt64(automaticRetries),
		"partial_commits":     systemStateInt(partialCommits),
		"prune_bound_escapes": systemStateInt(pruneEscapes),
	}))
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
