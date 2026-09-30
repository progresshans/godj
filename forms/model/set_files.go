package model

import (
	"context"
	"fmt"
	"slices"
)

// SetFilePublication retains the submitted row index for every attempted file
// publication. It has the same non-transactional semantics as FilePublication.
type SetFilePublication struct {
	index int
	file  FilePublication
}

func (publication SetFilePublication) Index() int            { return publication.index }
func (publication SetFilePublication) File() FilePublication { return publication.file }
func (SetFilePublication) Format(s fmt.State, _ rune) {
	fmt.Fprint(s, "model.SetFilePublication{redacted}")
}

// SaveFiles explicitly publishes uploads of rows selected for saving. Select
// is a pure binding callback called once per row with pending uploads; it must
// return the complete authorized file savers for that row. Every row's bindings
// and names are checked before any publication. Deleted, read-only, unchanged
// and empty-extra rows never publish files. No database writes occur.
//
// On success the new immutable preparation can make a SavePlan. On failure it
// is invalid, but all attempted file outcomes retain their row indexes. The
// original preparation is unchanged; never blindly repeat an uncertain or
// partial publication or delete files just from their names to compensate.
func (set PreparedSet[M]) SaveFiles(ctx context.Context, selectSavers func(PreparedSetRow[M]) ([]FileSaver[M], error)) (PreparedSet[M], []SetFilePublication, error) {
	if nilSaveValue(ctx) {
		return PreparedSet[M]{}, nil, &Error{Path: "context", Code: "nil"}
	}
	if err := ctx.Err(); err != nil {
		return PreparedSet[M]{}, nil, err
	}
	if _, err := set.manager.Metadata(); err != nil {
		return PreparedSet[M]{}, nil, err
	}
	operations := make([][]fileSave[M], len(set.rows))
	for i, row := range set.rows {
		if len(row.changed) == 0 || len(row.prepared.files) == 0 {
			continue
		}
		if selectSavers == nil {
			return PreparedSet[M]{}, nil, &Error{Path: "set.files", Code: "missing_saver"}
		}
		if err := ctx.Err(); err != nil {
			return PreparedSet[M]{}, nil, err
		}
		savers, err := selectSavers(row)
		if err != nil {
			return PreparedSet[M]{}, nil, err
		}
		operations[i], err = row.prepared.fileSaves(ctx, savers)
		if err != nil {
			return PreparedSet[M]{}, nil, err
		}
	}
	result := set
	result.rows = slices.Clone(set.rows)
	var publications []SetFilePublication
	for i, row := range set.rows {
		if len(operations[i]) == 0 {
			continue
		}
		prepared, files, err := row.prepared.saveFiles(ctx, operations[i])
		for _, file := range files {
			publications = append(publications, SetFilePublication{index: row.index, file: file})
		}
		if err != nil {
			return PreparedSet[M]{}, publications, err
		}
		result.rows[i].prepared = prepared
	}
	return result, publications, nil
}
