//go:build linux && godj_starter_browser

package runserverproduct_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func checkStarterDocumentRefusal(t *testing.T, settings map[string]string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	db := starterObserver(t, settings)
	var members, domains, audits int
	if err := db.QueryRowContext(ctx, `SELECT (SELECT count(*) FROM people_member),
		(SELECT count(*) FROM godj_account_domain_transition), (SELECT count(*) FROM godj_account_audit_event)`).Scan(&members, &domains, &audits); err != nil || members != 0 || domains != 0 || audits != 0 {
		t.Fatal("rejected first account changed native state", err)
	}
	for _, directory := range []string{settings["STARTER_MEDIA_DIR"], settings["STARTER_REPORTS_DIR"]} {
		if entries, err := os.ReadDir(directory); err != nil || len(entries) != 0 {
			t.Fatal("rejected input reached storage or its reporter", err)
		}
	}
}

// The application's file report, the actual published bytes and the native
// account are independent observations, including after the first server exits.
func checkStarterDocuments(t *testing.T, ctx context.Context, database *sql.DB, phase string, settings map[string]string) {
	t.Helper()
	type report struct {
		Operation, Field, Name    string
		ID, Revision, Size        int64
		Outcome                   string
		AccountFailed, FileFailed bool
	}
	read := func(action, content string) report {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(settings["STARTER_REPORTS_DIR"], action+".json"))
		var r report
		if err != nil || json.Unmarshal(raw, &r) != nil || r.Field != "document" || !fs.ValidPath(r.Name) ||
			!strings.HasPrefix(r.Name, "documents/") || r.Size != int64(len(content)) || r.Outcome != "published" || r.FileFailed || r.AccountFailed {
			t.Fatalf("invalid %s publication report", action)
		}
		data, err := os.ReadFile(filepath.Join(settings["STARTER_MEDIA_DIR"], filepath.FromSlash(r.Name)))
		if err != nil || string(data) != content {
			t.Fatalf("%s reported content differs from storage", action)
		}
		return r
	}
	first := read("first", "starter operator document\n")
	create := read("create", "starter member document\n")
	if first.Name != "documents/operator.txt" || create.Name != "documents/reader.txt" ||
		first.Revision != 1 || create.Revision != 1 || first.ID == create.ID || create.Operation != "" {
		t.Fatal("unexpected first or Admin creation receipt")
	}
	var operation string
	if err := database.QueryRowContext(ctx, "SELECT operation_id FROM godj_account_domain_transition WHERE sequence=1").Scan(&operation); err != nil ||
		len(operation) != 64 || first.Operation != operation {
		t.Fatal("first-account report differs from durable operation")
	}

	wantReports, wantFiles := 2, 2
	member, region := create, "Initial"
	if phase == "restart" {
		member = read("edit", "starter revised document\n")
		if member.ID != create.ID || member.Revision <= create.Revision || member.Name == create.Name || member.Operation != "" {
			t.Fatal("edit did not preserve the earlier publication and advance the account")
		}
		data, err := os.ReadFile(filepath.Join(settings["STARTER_MEDIA_DIR"], "documents", "reconciled.txt"))
		if err != nil || string(data) != "starter unreported document\n" {
			t.Fatal("reporting failure lost the new publication")
		}
		member.Name = "documents/reconciled.txt"
		member.Revision++
		wantReports, wantFiles, region = 3, 4, "Reviewed"
	}
	for directory, want := range map[string]int{settings["STARTER_REPORTS_DIR"]: wantReports, filepath.Join(settings["STARTER_MEDIA_DIR"], "documents"): wantFiles} {
		if entries, err := os.ReadDir(directory); err != nil || len(entries) != want {
			t.Fatal("unexpected number of publications or reports", err)
		}
	}
	rows, err := database.QueryContext(ctx, "SELECT contact, id, version, region, document FROM people_member ORDER BY contact")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for _, want := range []struct {
		login, region string
		stored        report
	}{{starterOperator, "NZ", first}, {starterReader, region, member}} {
		var login, profile, stored string
		var id, revision int64
		if !rows.Next() || rows.Scan(&login, &id, &revision, &profile, &stored) != nil ||
			login != want.login || profile != want.region || id != want.stored.ID || revision != want.stored.Revision || stored != want.stored.Name {
			t.Fatal("native account differs from its file report")
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected native document account inventory", rows.Err())
	}
}
