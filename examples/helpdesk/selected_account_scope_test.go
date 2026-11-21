package helpdesk_test

import (
	"context"
	"errors"
	"strings"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/query"
)

var selectedHelpdeskPrivateFault = errors.New("private selected Helpdesk scope failure")

// Observe the actual borrowed native handles. Root I/O and ordinary atomic
// fallback are errors, so a test cannot pass by authorizing in one scope and
// later querying or mutating through a different root connection.
type selectedHelpdeskProbe struct {
	helpdesk.SelectedBackend
	readMode, writeMode                                                string
	rootIO, plainTransactions, snapshots, scalarScopes, relationScopes int
	businessReads, businessWrites, auditAttempts                       int
	beforeWrite                                                        func(context.Context) error
	beforeQuery                                                        func(context.Context, string) error
	cancel                                                             context.CancelFunc
	lateRead                                                           func(db.Queryer) error
	readHandles                                                        []db.SessionValidator
}

func (p *selectedHelpdeskProbe) Query(context.Context, query.Plan) (db.Rows, error) {
	p.rootIO++
	return nil, selectedHelpdeskPrivateFault
}
func (p *selectedHelpdeskProbe) Insert(context.Context, query.InsertPlan) (int64, error) {
	p.rootIO++
	return 0, selectedHelpdeskPrivateFault
}
func (p *selectedHelpdeskProbe) Update(context.Context, query.UpdatePlan) (int64, error) {
	p.rootIO++
	return 0, selectedHelpdeskPrivateFault
}
func (p *selectedHelpdeskProbe) Delete(context.Context, query.DeletePlan) (int64, error) {
	p.rootIO++
	return 0, selectedHelpdeskPrivateFault
}
func (p *selectedHelpdeskProbe) Atomic(context.Context, func(db.Session) error) error {
	p.plainTransactions++
	return selectedHelpdeskPrivateFault
}
func (p *selectedHelpdeskProbe) AtomicRelation(context.Context, func(db.RelationSession) error) error {
	p.plainTransactions++
	return selectedHelpdeskPrivateFault
}

type selectedHelpdeskReader struct {
	db.Queryer
	validator db.SessionValidator
	owner     *selectedHelpdeskProbe
}

func (r *selectedHelpdeskReader) ValidateSession(ctx context.Context) error {
	return r.validator.ValidateSession(ctx)
}
func (r *selectedHelpdeskReader) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if strings.HasPrefix(plan.Table(), "helpdesk_") {
		r.owner.businessReads++
		if r.owner.beforeQuery != nil {
			if err := r.owner.beforeQuery(ctx, plan.Table()); err != nil {
				return nil, err
			}
		}
		if r.owner.readMode == "swallow" {
			return nil, selectedHelpdeskPrivateFault
		}
	}
	return r.Queryer.Query(ctx, plan)
}

type selectedHelpdeskWritableRead struct {
	*selectedHelpdeskReader
	db.Mutator
}

func (p *selectedHelpdeskProbe) ReadSnapshot(ctx context.Context, run func(db.Queryer) error) error {
	p.snapshots++
	switch p.readMode {
	case "skip":
		return nil
	case "late":
		p.lateRead = run
		return nil
	case "nil":
		return run(nil)
	case "root":
		return run(p)
	case "expired":
		var expired db.Queryer
		if err := p.SelectedBackend.ReadSnapshot(ctx, func(reader db.Queryer) error { expired = reader; return nil }); err != nil {
			return err
		}
		return run(expired)
	}
	err := p.SelectedBackend.ReadSnapshot(ctx, func(reader db.Queryer) error {
		validator, ok := reader.(db.SessionValidator)
		if !ok {
			return errors.New("selected test reader lost native lifetime")
		}
		wrapped := &selectedHelpdeskReader{reader, validator, p}
		p.readHandles = append(p.readHandles, wrapped)
		switch p.readMode {
		case "no_validator":
			return run(struct{ db.Queryer }{wrapped})
		case "writable":
			return run(selectedHelpdeskWritableRead{wrapped, p.SelectedBackend})
		case "repeat":
			first := run(wrapped)
			return errors.Join(first, run(wrapped))
		case "swallow":
			_ = run(wrapped)
			return nil
		default:
			return run(wrapped)
		}
	})
	if p.readMode == "cleanup" {
		return errors.Join(err, selectedHelpdeskPrivateFault)
	}
	if p.cancel != nil {
		p.cancel()
	}
	return err
}

// Keep every native capability needed by the selected business operation. A
// separate relation wrapper preserves the ordinary/relation scope distinction.
type selectedHelpdeskSession struct {
	db.Session
	owner *selectedHelpdeskProbe
}
type selectedHelpdeskRelationSession struct {
	*selectedHelpdeskSession
	relation db.RelationMutator
}

var (
	_ db.SessionValidator       = (*selectedHelpdeskReader)(nil)
	_ db.SessionValidator       = (*selectedHelpdeskSession)(nil)
	_ db.ConflictInserter       = (*selectedHelpdeskSession)(nil)
	_ db.Savepointer            = (*selectedHelpdeskSession)(nil)
	_ db.BulkInserter           = (*selectedHelpdeskSession)(nil)
	_ db.BulkUpdater            = (*selectedHelpdeskSession)(nil)
	_ db.QueryUpdater           = (*selectedHelpdeskSession)(nil)
	_ db.ReadModifyWriteSession = (*selectedHelpdeskSession)(nil)
	_ db.RelationSession        = (*selectedHelpdeskRelationSession)(nil)
)

func (p *selectedHelpdeskProbe) enter(ctx context.Context) error {
	if before := p.beforeWrite; before != nil {
		p.beforeWrite = nil
		return before(ctx)
	}
	return nil
}
func (p *selectedHelpdeskProbe) outcome(err error) error {
	if err == nil && p.writeMode == "commit_unknown" {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeCommitOutcomeUnknown}
	}
	if err != nil && p.writeMode == "rollback_unknown" {
		return errors.Join(err, &query.Error{Category: query.CategoryBackend, Code: query.CodeTransactionOutcomeUnknown})
	}
	return err
}
func (p *selectedHelpdeskProbe) CoordinatedAtomic(ctx context.Context, run func(db.Session) error) error {
	p.scalarScopes++
	if err := p.enter(ctx); err != nil {
		return err
	}
	if p.writeMode == "skip" {
		return nil
	}
	err := p.SelectedBackend.CoordinatedAtomic(ctx, func(scope db.Session) error {
		wrapped := &selectedHelpdeskSession{scope, p}
		first := run(wrapped)
		if p.writeMode == "repeat" {
			return errors.Join(first, run(wrapped))
		}
		return first
	})
	return p.outcome(err)
}
func (p *selectedHelpdeskProbe) CoordinatedAtomicRelation(ctx context.Context, run func(db.RelationSession) error) error {
	p.relationScopes++
	if err := p.enter(ctx); err != nil {
		return err
	}
	if p.writeMode == "skip" {
		return nil
	}
	err := p.SelectedBackend.CoordinatedAtomicRelation(ctx, func(scope db.RelationSession) error {
		wrapped := &selectedHelpdeskRelationSession{&selectedHelpdeskSession{scope, p}, scope}
		first := run(wrapped)
		if p.writeMode == "repeat" {
			return errors.Join(first, run(wrapped))
		}
		return first
	})
	return p.outcome(err)
}
func (s *selectedHelpdeskSession) ValidateSession(ctx context.Context) error {
	validator, ok := s.Session.(db.SessionValidator)
	if !ok {
		return errors.New("selected test writer lost native lifetime")
	}
	return validator.ValidateSession(ctx)
}
func (s *selectedHelpdeskSession) Query(ctx context.Context, plan query.Plan) (db.Rows, error) {
	if strings.HasPrefix(plan.Table(), "helpdesk_") {
		s.owner.businessReads++
	}
	return s.Session.Query(ctx, plan)
}
func (s *selectedHelpdeskSession) write(table string) error {
	if table == "godj_account_audit_event" {
		s.owner.auditAttempts++
		if s.owner.writeMode == "audit_failure" || s.owner.writeMode == "rollback_unknown" {
			return selectedHelpdeskPrivateFault
		}
	}
	if strings.HasPrefix(table, "helpdesk_") {
		s.owner.businessWrites++
	}
	return nil
}
func (s *selectedHelpdeskSession) Insert(ctx context.Context, plan query.InsertPlan) (int64, error) {
	if err := s.write(plan.Table()); err != nil {
		return 0, err
	}
	return s.Session.Insert(ctx, plan)
}
func (s *selectedHelpdeskSession) Update(ctx context.Context, plan query.UpdatePlan) (int64, error) {
	if err := s.write(plan.Table()); err != nil {
		return 0, err
	}
	return s.Session.Update(ctx, plan)
}
func (s *selectedHelpdeskSession) Delete(ctx context.Context, plan query.DeletePlan) (int64, error) {
	if err := s.write(plan.Table()); err != nil {
		return 0, err
	}
	return s.Session.Delete(ctx, plan)
}
func (s *selectedHelpdeskRelationSession) RelationSetNull(ctx context.Context, plan query.RelationSetNullPlan) (int64, error) {
	s.owner.businessWrites++
	return s.relation.RelationSetNull(ctx, plan)
}
func (s *selectedHelpdeskSession) InsertOnConflict(ctx context.Context, plan query.ConflictInsertPlan) (db.ConflictInsertResult, error) {
	native, ok := s.Session.(db.ConflictInserter)
	if !ok {
		return db.ConflictInsertResult{}, errors.New("selected test lacks native conflict insertion")
	}
	if err := s.write(plan.Table()); err != nil {
		return db.ConflictInsertResult{}, err
	}
	return native.InsertOnConflict(ctx, plan)
}
func (s *selectedHelpdeskSession) Savepoint(ctx context.Context, run func(db.Session) error) error {
	return db.WithSavepoint(ctx, s.Session, func(child db.Session) error {
		wrapped := &selectedHelpdeskSession{child, s.owner}
		if relation, ok := child.(db.RelationSession); ok {
			return run(&selectedHelpdeskRelationSession{wrapped, relation})
		}
		return run(wrapped)
	})
}
func (s *selectedHelpdeskSession) BulkInsertLimits(ctx context.Context) (db.BulkInsertLimits, error) {
	native, ok := s.Session.(db.BulkInserter)
	if !ok {
		return db.BulkInsertLimits{}, errors.New("selected test lacks native bulk insert")
	}
	return native.BulkInsertLimits(ctx)
}
func (s *selectedHelpdeskSession) BulkInsert(ctx context.Context, plan query.BulkInsertPlan) (db.BulkInsertResult, error) {
	native, ok := s.Session.(db.BulkInserter)
	if !ok {
		return db.BulkInsertResult{}, errors.New("selected test lacks native bulk insert")
	}
	if err := s.write(plan.Table()); err != nil {
		return db.BulkInsertResult{}, err
	}
	return native.BulkInsert(ctx, plan)
}
func (s *selectedHelpdeskSession) BulkUpdateBatchSize(ctx context.Context, spec query.BulkUpdateSpec) (int, error) {
	native, ok := s.Session.(db.BulkUpdater)
	if !ok {
		return 0, errors.New("selected test lacks native bulk update")
	}
	return native.BulkUpdateBatchSize(ctx, spec)
}
func (s *selectedHelpdeskSession) BulkUpdate(ctx context.Context, plan query.BulkUpdatePlan) (int64, error) {
	native, ok := s.Session.(db.BulkUpdater)
	if !ok {
		return 0, errors.New("selected test lacks native bulk update")
	}
	if err := s.write(plan.Spec().Selection().Table()); err != nil {
		return 0, err
	}
	return native.BulkUpdate(ctx, plan)
}
func (s *selectedHelpdeskSession) CheckQueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) error {
	native, ok := s.Session.(db.QueryUpdater)
	if !ok {
		return errors.New("selected test lacks native query update")
	}
	return native.CheckQueryUpdate(ctx, plan)
}
func (s *selectedHelpdeskSession) QueryUpdate(ctx context.Context, plan query.QueryUpdatePlan) (int64, error) {
	native, ok := s.Session.(db.QueryUpdater)
	if !ok {
		return 0, errors.New("selected test lacks native query update")
	}
	if err := s.write(plan.Selection().Table()); err != nil {
		return 0, err
	}
	return native.QueryUpdate(ctx, plan)
}
func (s *selectedHelpdeskSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	native, ok := s.Session.(db.ReadModifyWriteSession)
	if !ok {
		return "", errors.New("selected test lacks native read-modify-write policy")
	}
	return native.ReadModifyWritePolicy(ctx)
}
