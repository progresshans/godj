package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/admin"
	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/db"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/examples/helpdesk/models"
	"github.com/progresshans/godj/systemstate"
)

func verifyHelpdeskBulkUpdateConcurrency(t *testing.T, root context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), hasher auth.PasswordHasher, authenticated *helpdeskClient) {
	for _, rollback := range []bool{false, true} {
		name := "leader_commit"
		if rollback {
			name = "leader_rollback"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(root, 20*time.Second)
			defer cancel()
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Update concurrent "+name))
			if err != nil {
				t.Fatal(err)
			}
			rows := make([]models.Ticket, 2)
			for index := range rows {
				rows[index], err = models.TicketObjects.Create(ctx, runtime, models.NewTicketCreate(fmt.Sprintf("Original %d", index), category.ID))
				if err != nil {
					t.Fatal(err)
				}
			}
			peerBackend, err := open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer peerBackend.Close()
			observer, err := open(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer observer.Close()
			peer := &coordinatedPeerBackend{helpdeskBackend: peerBackend}
			peerRuntime, err := systemstate.OpenIdentity(ctx, peer, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
			if err != nil {
				t.Fatal(err)
			}
			observerRuntime, err := systemstate.OpenIdentity(ctx, observer, systemstate.IdentityRuntimeConfig{PasswordHasher: hasher})
			if err != nil {
				t.Fatal(err)
			}
			leaderApp, err := helpdesk.New(runtime, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			followerApp, err := helpdesk.New(peerRuntime, category.ID)
			if err != nil {
				t.Fatal(err)
			}
			audited, release := make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			unlock := func() { releaseOnce.Do(func() { close(release) }) }
			var workers sync.WaitGroup
			defer func() { unlock(); workers.Wait() }()
			audits := 0
			leader := helpdeskHTTP(t, leaderApp, runtime, auth.PrincipalAuthorizer{}, func(work context.Context, session db.Session, event admin.PreparedEvent) error {
				if err := runtime.AppendAudit(work, session, event); err != nil {
					return err
				}
				audits++
				if audits == 1 {
					close(audited)
					select {
					case <-release:
					case <-work.Done():
						return work.Err()
					}
					if rollback {
						return errors.New("leader bulk update audit rollback")
					}
				}
				return nil
			})
			follower := helpdeskHTTP(t, followerApp, peerRuntime, auth.PrincipalAuthorizer{})
			for _, client := range []*helpdeskClient{leader, follower} {
				client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
				if response := client.request("GET", "/admin/", "", false); response.Code != 200 {
					t.Fatal("concurrent bulk update admission", response.Code)
				}
			}
			peer.entered = make(chan struct{})
			results := []chan *httptest.ResponseRecorder{make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)}
			start := func(index int, client *helpdeskClient, field string) {
				inputs := make([]map[string]any, 2)
				for i, row := range rows {
					inputs[i] = map[string]any{"id": row.ID, field: fmt.Sprintf("writer-%d-%d", index, i)}
				}
				body, err := json.Marshal(inputs)
				if err != nil {
					t.Fatal(err)
				}
				workers.Go(func() {
					results[index] <- client.requestContext(ctx, "PATCH", "/api/tickets/bulk/", string(body), true)
				})
			}
			start(0, leader, "subject")
			select {
			case <-audited:
			case <-ctx.Done():
				t.Fatal("leader did not reach transactional audit")
			}
			start(1, follower, "details")
			select {
			case <-peer.entered:
			case <-ctx.Done():
				t.Fatal("follower did not enter coordination")
			}
			for _, original := range rows {
				current, err := models.TicketObjects.Using(observer).Filter(models.TicketFields.ID.Exact(original.ID)).Get(ctx)
				if err != nil || current.Subject != original.Subject || current.Details != nil {
					t.Fatal("uncommitted batch visible from other connection", err)
				}
				var history []admin.AuditEntry
				err = observer.ReadSnapshot(ctx, func(reader db.Queryer) error {
					var err error
					history, err = observerRuntime.AuditHistoryInSnapshot(ctx, reader, "helpdesk.ticket", original.ID, 10)
					return err
				})
				if err != nil || len(history) != 0 {
					t.Fatal("uncommitted batch audit visible", err)
				}
			}
			unlock()
			for index, result := range results {
				select {
				case response := <-result:
					want := 200
					if index == 0 && rollback {
						want = 500
					}
					if response.Code != want {
						t.Fatal("concurrent bulk update result", index, response.Code, want)
					}
				case <-ctx.Done():
					t.Fatal("concurrent bulk update did not finish")
				}
			}
			workers.Wait()
			for index, original := range rows {
				current, err := models.TicketObjects.Using(observer).Filter(models.TicketFields.ID.Exact(original.ID)).Get(ctx)
				if err != nil {
					t.Fatal(err)
				}
				wantSubject := fmt.Sprintf("writer-0-%d", index)
				wantAudit := 2
				if rollback {
					wantSubject = original.Subject
					wantAudit = 1
				}
				if current.Subject != wantSubject || current.Details == nil || *current.Details != fmt.Sprintf("writer-1-%d", index) {
					t.Fatal("later partial batch overwrote omitted current fields", current)
				}
				history, err := observerRuntime.AuditHistory(ctx, "helpdesk.ticket", current.ID, 10)
				if err != nil || len(history) != wantAudit {
					t.Fatal("concurrent bulk update audit count", len(history), err)
				}
				for _, entry := range history {
					if entry.Action != admin.ActionChange {
						t.Fatal("wrong concurrent action", entry)
					}
				}
			}
		})
	}
}
