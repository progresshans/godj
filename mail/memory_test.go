package mail_test

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/progresshans/godj/mail"
)

func TestMailMemoryCapacityOwnershipAndConcurrentSend(t *testing.T) {
	value := message(t, messageConfig(t))
	memory, err := mail.NewMemory(mail.MemoryConfig{MaximumMessages: 16})
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 32)
	var tasks sync.WaitGroup
	for range 32 {
		tasks.Go(func() { results <- memory.Send(t.Context(), value) })
	}
	tasks.Wait()
	close(results)
	accepted, denied := 0, 0
	for err := range results {
		if err == nil {
			accepted++
		} else if errors.Is(err, &mail.Error{Code: mail.CodeCapacity}) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if accepted != 16 || denied != 16 {
		t.Fatal("concurrent sends exceeded capacity or lost accepted messages", accepted, denied)
	}
	snapshot, err := memory.Snapshot()
	if err != nil || len(snapshot) != accepted {
		t.Fatal("missing accepted deliveries", err)
	}
	seen := map[string]bool{}
	for _, delivery := range snapshot {
		id := delivery.MessageID()
		if seen[id] || id == "" || delivery.SentAt().IsZero() || delivery.Message().Text() != value.Text() {
			t.Fatal("missing or reused delivery identity")
		}
		seen[id] = true
	}
	data := snapshot[0].Bytes()
	data[0] = 'X'
	snapshot[0] = mail.Delivery{}
	again, err := memory.Snapshot()
	if err != nil || !bytes.HasPrefix(again[0].Bytes(), []byte("From:")) {
		t.Fatal("snapshot mutation affected backend storage", err)
	}
	drained, err := memory.Drain()
	if err != nil || len(drained) != accepted {
		t.Fatal("drain lost deliveries", err)
	}
	remaining, err := memory.Snapshot()
	if err != nil || len(remaining) != 0 || memory.Send(t.Context(), value) != nil {
		t.Fatal("drain did not release capacity", err)
	}
	isolated, err := mail.NewMemory(mail.MemoryConfig{MaximumBytes: 1})
	if err != nil || !errors.Is(isolated.Send(t.Context(), value), &mail.Error{Code: mail.CodeCapacity}) {
		t.Fatal("wire byte capacity not enforced", err)
	}
	if entries, err := isolated.Snapshot(); err != nil || len(entries) != 0 {
		t.Fatal("capacity failure published a delivery", err)
	}
}

func TestMailMemoryCancellationAndInvalidCalls(t *testing.T) {
	memory, err := mail.NewMemory(mail.MemoryConfig{})
	if err != nil {
		t.Fatal(err)
	}
	value := message(t, messageConfig(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := memory.Send(ctx, value); !errors.Is(err, context.Canceled) || !errors.Is(err, &mail.Error{Code: mail.CodeNotSent}) {
		t.Fatal("cancelled send was not explicit", err)
	}
	if entries, err := memory.Snapshot(); err != nil || len(entries) != 0 {
		t.Fatal("cancelled send accepted data", err)
	}
	for _, err := range []error{memory.Send(nil, value), memory.Send(t.Context(), mail.Message{}), (*mail.Memory)(nil).Send(t.Context(), value)} {
		if err == nil {
			t.Fatal("invalid call accepted")
		}
	}
	if _, err := (*mail.Memory)(nil).Snapshot(); err == nil {
		t.Fatal("uninitialized snapshot accepted")
	}
	if _, err := new(mail.Memory).Drain(); err == nil {
		t.Fatal("uninitialized drain accepted")
	}
	for _, config := range []mail.MemoryConfig{{MaximumMessages: -1}, {MaximumBytes: -1}} {
		if _, err := mail.NewMemory(config); !errors.Is(err, &mail.Error{Code: mail.CodeInvalidConfig}) {
			t.Fatal("invalid capacity accepted", err)
		}
	}
}
