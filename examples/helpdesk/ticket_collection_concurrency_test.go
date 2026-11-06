package helpdesk_test

import (
	"context"
	"fmt"
	"maps"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/progresshans/godj/auth"
	"github.com/progresshans/godj/examples/helpdesk"
	"github.com/progresshans/godj/systemstate"
)

func verifyCollectionConcurrency(t *testing.T, ctx context.Context, runtime *systemstate.Runtime, open func(context.Context) (helpdeskBackend, error), authenticated *helpdeskClient, categoryID, ticketID int64, keys map[int64]int64, policy systemstate.CredentialPolicy) {
	t.Helper()
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	first, err := open(bounded)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := open(bounded)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	runtimes := make([]*systemstate.Runtime, 2)
	for index, backend := range []helpdeskBackend{first, second} {
		runtimes[index], err = systemstate.OpenIdentity(bounded, backend, systemstate.IdentityRuntimeConfig{PasswordHasher: policy.PasswordHasher})
		if err != nil {
			t.Fatal(err)
		}
	}
	backends := []*collectionFaultBackend{{Backend: runtimes[0]}, {Backend: runtimes[1]}}
	clients := make([]*helpdeskClient, 2)
	for index, b := range backends {
		application, err := helpdesk.New(b, categoryID)
		if err != nil {
			t.Fatal(err)
		}
		clients[index] = helpdeskHTTP(t, application, runtimes[index], auth.PrincipalAuthorizer{})
		clients[index].cookies, clients[index].csrf = maps.Clone(authenticated.cookies), authenticated.csrf
		if response := clients[index].requestContext(bounded, "GET", "/admin/", "", false); response.Code != 200 {
			t.Fatal("concurrent CSRF bootstrap", response.Code)
		}
	}
	admitted := []chan struct{}{make(chan struct{}), make(chan struct{})}
	start := []chan struct{}{make(chan struct{}), make(chan struct{})}
	release := make(chan struct{})
	written := make(chan struct{})
	secondSession := make(chan struct{})
	var releases [3]sync.Once
	unlock := func(index int) {
		releases[index].Do(func() {
			if index == 2 {
				close(release)
			} else {
				close(start[index])
			}
		})
	}
	defer func() {
		for index := range 3 {
			unlock(index)
		}
	}()
	for index, b := range backends {
		b.before = func() error {
			close(admitted[index])
			select {
			case <-start[index]:
				return nil
			case <-bounded.Done():
				return bounded.Err()
			}
		}
	}
	backends[0].afterScalar = func() {
		close(written)
		select {
		case <-release:
		case <-bounded.Done():
		}
	}
	backends[1].onEnter = func() { close(secondSession) }
	results := []chan *httptest.ResponseRecorder{make(chan *httptest.ResponseRecorder, 1), make(chan *httptest.ResponseRecorder, 1)}
	wanted := [][]int64{{keys[7], keys[11]}, {keys[9]}}
	var wait sync.WaitGroup
	for index, client := range clients {
		wait.Go(func() {
			results[index] <- client.requestContext(bounded, "PATCH", fmt.Sprintf("/api/tickets/%d/", ticketID), collectionBody(fmt.Sprintf("Concurrent %d", index), wanted[index]), true)
		})
	}
	// Both authenticated requests have parsed their complete input before either
	// backend takes its transaction fence. Their runtimes and connections differ.
	await := func(channel <-chan struct{}) {
		t.Helper()
		select {
		case <-channel:
		case <-bounded.Done():
			t.Fatal("collection concurrency stage timed out")
		}
	}
	defer func() {
		cancel()
		for index := range 3 {
			unlock(index)
		}
		wait.Wait()
	}()
	await(admitted[0])
	await(admitted[1])
	unlock(0)
	await(written)
	unlock(1)
	select {
	case <-secondSession:
		t.Fatal("second runtime entered while the first scalar/set transaction was held")
	case <-time.After(50 * time.Millisecond):
	case <-bounded.Done():
		t.Fatal(bounded.Err())
	}
	unlock(2)
	for index := range clients {
		select {
		case response := <-results[index]:
			value := decodeCollectionTicket(t, response, 200)
			if value.Subject != fmt.Sprintf("Concurrent %d", index) || !slices.Equal(value.Labels, wanted[index]) {
				t.Fatal("concurrent response mixed two writes", value)
			}
		case <-bounded.Done():
			t.Fatal("collection requests did not finish", bounded.Err())
		}
	}
	wait.Wait()
	stored := readUniqueTicket(t, ctx, runtime, ticketID)
	links := collectionLinks(t, ctx, runtime, ticketID)
	if stored.Subject != "Concurrent 1" || len(links) != 1 || links[keys[9]] == 0 || backends[0].atomics != 1 || backends[1].atomics != 1 {
		t.Fatal("concurrent replacement was merged, lost or retried", stored.Subject, links)
	}
}
