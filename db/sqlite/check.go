package sqlite

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/progresshans/godj/query"
)

// Check opens an existing filesystem database read-only, verifies the backend
// connection and reads its schema catalog, then closes it. It does not create a
// database, run migrations or validate application tables against model state.
// Memory databases and initialization SQL cannot be inspected by this separate
// connection. Unsupported DSN options fail before opening any database.
func Check(ctx context.Context, dataSourceName string) (resultErr error) {
	if ctx == nil {
		return &query.Error{Category: query.CategoryArgument, Code: query.CodeInvalidValue, Field: "context", Detail: "context is nil"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	dsn, filename, err := readCheckDataSource(dataSourceName)
	if err != nil {
		return err
	}
	info, err := os.Stat(filename)
	if err != nil || !info.Mode().IsRegular() {
		return &query.Error{Category: query.CategoryBackend, Code: query.CodeInvalidPlan, Field: "database", Detail: "SQLite read check requires an existing regular file"}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backend, err := Open(ctx, dsn)
	if err != nil {
		// The driver may finish its busy wait with SQLITE_BUSY after the
		// context was canceled. Open has already closed its failed connection;
		// retain the check's cancellation semantics at this earlier I/O stage.
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, backend.Close()) }()
	// Ping alone need not read the database header. Reading sqlite_schema also
	// rejects a regular file whose bytes are not a usable SQLite database.
	var objects int64
	if err := backend.database.QueryRowContext(ctx, "SELECT count(*) FROM sqlite_schema").Scan(&objects); err != nil {
		if canceled := ctx.Err(); canceled != nil {
			return canceled
		}
		return err
	}
	return ctx.Err()
}

func readCheckDataSource(input string) (dsn, filename string, err error) {
	unsupported := func() (string, string, error) {
		return "", "", &query.Error{Category: query.CategoryBackend, Code: query.CodeUnsupported, Field: "database", Detail: "SQLite read checks require a filesystem database with only mode, cache or _busy_timeout options"}
	}
	if input == "" || strings.ContainsRune(input, 0) {
		return unsupported()
	}
	// The driver splits query parameters even for ordinary filenames. Preserve
	// that target interpretation before constructing a file URI with mode=ro.
	base, rawQuery := input, ""
	if queryStart := strings.IndexByte(input, '?'); queryStart >= 1 {
		base, rawQuery = input[:queryStart], input[queryStart+1:]
	}
	filename = base
	if strings.HasPrefix(base, "file:") {
		parsed, parseErr := url.Parse(input)
		if parseErr != nil || parsed.Scheme != "file" || parsed.User != nil || parsed.Host != "" && parsed.Host != "localhost" || parsed.Fragment != "" {
			return unsupported()
		}
		filename, rawQuery = parsed.Path, parsed.RawQuery
		if parsed.Opaque != "" {
			filename, err = url.PathUnescape(parsed.Opaque)
			if err != nil {
				return unsupported()
			}
		}
	}
	if filename == "" || filename == ":memory:" || strings.ContainsRune(filename, 0) {
		return unsupported()
	}
	parameters, err := url.ParseQuery(rawQuery)
	if err != nil {
		return unsupported()
	}
	for key, values := range parameters {
		if len(values) != 1 {
			return unsupported()
		}
		value := values[0]
		switch key {
		case "mode":
			if value != "ro" && value != "rw" && value != "rwc" {
				return unsupported()
			}
		case "cache":
			if value != "private" && value != "shared" {
				return unsupported()
			}
		case "_busy_timeout":
			milliseconds, parseErr := strconv.ParseInt(value, 10, 32)
			if parseErr != nil || milliseconds < 0 || strconv.FormatInt(milliseconds, 10) != value {
				return unsupported()
			}
		default:
			// In particular, _pragma may contain arbitrary SQL and attach another
			// writable database, even when the main connection is read-only.
			return unsupported()
		}
	}
	if !filepath.IsAbs(filename) {
		workingDirectory, err := os.Getwd()
		if err != nil {
			return unsupported()
		}
		// Preserve dot segments after symlinks; lexical cleaning could select
		// a different file from the application's configured driver target.
		filename = workingDirectory + string(filepath.Separator) + filename
	}
	parameters.Set("mode", "ro")
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(filename), RawQuery: parameters.Encode()}).String(), filename, nil
}
