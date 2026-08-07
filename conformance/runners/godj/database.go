package godj

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/progresshans/godj/db/sqlite"
)

var databaseSequence atomic.Uint64

func openArticleDatabase(ctx context.Context, contractID string) (*sqlite.Backend, error) {
	backend, err := openEmptyArticleDatabase(ctx, contractID)
	if err != nil {
		return nil, err
	}
	statement := `INSERT INTO "godj_conformance_article" ("id", "title", "published", "summary") VALUES
  (1, 'Alpine Guide', TRUE, NULL),
  (2, 'django Tips', FALSE, 'ORM'),
  (3, 'Django Deep Dive', TRUE, ''),
  (4, 'Other', TRUE, NULL)`
	if _, err := backend.ExecContext(ctx, statement); err != nil {
		closeErr := backend.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("provision article rows: %w (close after failure: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("provision article rows: %w", err)
	}
	return backend, nil
}

func openEmptyArticleDatabase(ctx context.Context, contractID string) (*sqlite.Backend, error) {
	name := fmt.Sprintf(
		"godj-conformance-%s-%d",
		strings.ToLower(strings.ReplaceAll(contractID, "-", "_")),
		databaseSequence.Add(1),
	)
	backend, err := sqlite.OpenMemory(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("open independent SQLite database: %w", err)
	}
	statement := `CREATE TABLE "godj_conformance_article" (
  "id" INTEGER NOT NULL PRIMARY KEY,
  "title" VARCHAR(200) NOT NULL,
  "published" BOOLEAN NOT NULL,
  "summary" VARCHAR(200) NULL
)`
	if _, err := backend.ExecContext(ctx, statement); err != nil {
		closeErr := backend.Close()
		if closeErr != nil {
			return nil, fmt.Errorf("provision article database: %w (close after failure: %v)", err, closeErr)
		}
		return nil, fmt.Errorf("provision article database: %w", err)
	}
	return backend, nil
}
