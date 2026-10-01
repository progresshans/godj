package postgres

import (
	"context"

	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/query"
)

var _ db.ReadModifyWriteSession = (*transactionSession)(nil)

func (session *transactionSession) ReadModifyWritePolicy(ctx context.Context) (db.ReadModifyWritePolicy, error) {
	if err := session.validate(ctx); err != nil {
		return "", err
	}
	if session.readOnly {
		return "", &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Detail: "read-modify-write requires a writable transaction"}
	}
	return db.ReadModifyWriteRowLock, nil
}
