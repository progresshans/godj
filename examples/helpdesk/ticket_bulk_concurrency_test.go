package helpdesk_test

import (
	"context"
	"encoding/json"
	"errors"
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
	"github.com/progresshans/godj/examples/helpdesk/project"
	"github.com/progresshans/godj/systemstate"
)

func verifyHelpdeskBulkConcurrentRequests(t *testing.T, root context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), hasher auth.PasswordHasher, authenticated *helpdeskClient) {
	for _, rollback := range []bool{false, true} {
		name := "leader_commit"
		reference := "8c0c7607-ddc0-4870-8287-6df430000001"
		if rollback {
			name = "leader_rollback"
			reference = "8c0c7607-ddc0-4870-8287-6df430000002"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(root, 15*time.Second)
			defer cancel()
			category, err := models.CategoryObjects.Create(ctx, runtime, models.NewCategoryCreate("Bulk concurrent "+name))
			if err != nil {
				t.Fatal(err)
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
			audited, release := make(chan int64, 1), make(chan struct{})
			var once sync.Once
			unlock := func() { once.Do(func() { close(release) }) }
			var workers sync.WaitGroup
			defer func() { unlock(); workers.Wait() }()
			auditCount := 0
			leader := helpdeskHTTP(t, leaderApp, runtime, auth.PrincipalAuthorizer{}, func(ctx context.Context, session db.Session, event admin.PreparedEvent) error {
				if err := runtime.AppendAudit(ctx, session, event); err != nil {
					return err
				}
				auditCount++
				if auditCount == 1 {
					audited <- event.ObjectID()
					select {
					case <-release:
					case <-ctx.Done():
						return ctx.Err()
					}
					if rollback {
						return errors.New("leader audit rollback")
					}
				}
				return nil
			})
			follower := helpdeskHTTP(t, followerApp, peerRuntime, auth.PrincipalAuthorizer{})
			for _, client := range []*helpdeskClient{leader, follower} {
				client.cookies, client.csrf = maps.Clone(authenticated.cookies), authenticated.csrf
				if response := client.request("GET", "/admin/", "", false); response.Code != 200 {
					t.Fatal("concurrent admission", response.Code)
				}
			}
			peer.entered = make(chan struct{})
			results := []chan *httptest.ResponseRecorder{make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)}
			body := `[{"subject":"Shared bulk first","external_reference":"` + reference + `"},{"subject":"Shared bulk second"}]`
			start := func(index int, client *helpdeskClient) {
				workers.Go(func() { results[index] <- client.requestContext(ctx, "POST", "/api/tickets/bulk/", body, true) })
			}
			start(0, leader)
			var provisional int64
			select {
			case provisional = <-audited:
			case <-ctx.Done():
				t.Fatal("leader never reached provisional audit")
			}
			start(1, follower)
			select {
			case <-peer.entered:
			case <-ctx.Done():
				t.Fatal("follower never entered coordination")
			}
			relations, err := project.BindRelations()
			if err != nil {
				t.Fatal(err)
			}
			count, err := models.TicketObjects.Using(observer).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).Count(ctx)
			if err != nil || count != 0 {
				t.Fatal("provisional batch visible from another connection", count, err)
			}
			var history []admin.AuditEntry
			err = observer.ReadSnapshot(ctx, func(reader db.Queryer) error {
				var err error
				history, err = observerRuntime.AuditHistoryInSnapshot(ctx, reader, "helpdesk.ticket", provisional, 10)
				return err
			})
			if err != nil || len(history) != 0 {
				t.Fatal("provisional audit visible from another connection", err)
			}
			unlock()
			var committed []collectionTicketJSON
			for index := range results {
				select {
				case response := <-results[index]:
					want := 201
					if index == 1 {
						want = 400
					}
					if rollback {
						want = 500
						if index == 1 {
							want = 201
						}
					}
					if response.Code != want {
						t.Fatal("concurrent bulk status", index, response.Code, want)
					}
					if want == 201 && (json.Unmarshal(response.Body.Bytes(), &committed) != nil || len(committed) != 2) {
						t.Fatal("concurrent bulk result")
					}
				case <-ctx.Done():
					t.Fatal("concurrent bulk request did not finish")
				}
			}
			workers.Wait()
			rows, err := models.TicketObjects.Using(observer).Filter(relations.ModelsTicket.Category.ID.Exact(category.ID)).OrderBy(models.TicketFields.ID.Asc()).All(ctx)
			if err != nil || len(rows) != 2 || len(committed) != 2 {
				t.Fatal("concurrent all-or-nothing batch", err)
			}
			for index, row := range rows {
				if row.ID != committed[index].ID {
					t.Fatal("concurrent result key")
				}
				history, err := observerRuntime.AuditHistory(ctx, "helpdesk.ticket", row.ID, 10)
				if err != nil || len(history) != 1 || history[0].Action != admin.ActionAdd {
					t.Fatal("concurrent exactly-once audit", err)
				}
			}
		})
	}
}
