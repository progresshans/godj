package sqlite

import (
	"context"

	"github.com/progresshans/godj/db"
)

var _ db.ReadModifyWriteSession = (*transactionSession)(nil)
var _ db.ReadModifyWriteSession = (*relationSession)(nil)
var _ db.ReadModifyWriteSession = (*writeSession)(nil)

func (session *transactionSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	if err := session.validate(ctx); err != nil {
		return "", err
	}
	return db.ReadModifyWriteConflict, nil
}

func (session *relationSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	if err := session.validate(ctx); err != nil {
		return "", err
	}
	return db.ReadModifyWriteConflict, nil
}

func (session *writeSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	if err := session.ValidateSession(ctx); err != nil {
		return "", err
	}
	return session.session.ReadModifyWritePolicy(ctx)
}
