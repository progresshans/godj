package mail

import (
	"bytes"
	"context"
	"fmt"
	"sync"
	"time"
)

// Delivery is a memory backend's accepted message and wire representation.
// Copies are immutable; Bytes returns a caller-owned copy.
type Delivery struct{ state *delivery }
type delivery struct {
	message Message
	at      time.Time
	id      string
	data    []byte
}

func (Delivery) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.Delivery{redacted}"))
}
func (Delivery) MarshalJSON() ([]byte, error) { return []byte(`"mail.Delivery{redacted}"`), nil }
func (d Delivery) Message() Message {
	if d.state == nil {
		return Message{}
	}
	return d.state.message
}
func (d Delivery) Bytes() []byte {
	if d.state == nil {
		return nil
	}
	return bytes.Clone(d.state.data)
}
func (d Delivery) SentAt() time.Time {
	if d.state == nil {
		return time.Time{}
	}
	return d.state.at
}
func (d Delivery) MessageID() string {
	if d.state == nil {
		return ""
	}
	return d.state.id
}

type MemoryConfig struct {
	// Zero selects 100 messages and 64 MiB of MIME bytes. Limits are inclusive;
	// a full backend rejects new messages without evicting accepted deliveries.
	MaximumMessages int
	MaximumBytes    int
}

// Memory is a bounded, process-local test/development backend, not a durable
// queue. Copies of Memory share the same synchronized storage. Construct one
// explicitly for each application or test; there is no global outbox.
type Memory struct{ state *memoryState }
type memoryState struct {
	mu         sync.Mutex
	config     MemoryConfig
	bytes      int
	deliveries []Delivery
}

func (*Memory) Format(state fmt.State, _ rune) {
	_, _ = state.Write([]byte("mail.Memory{redacted}"))
}
func (*Memory) MarshalJSON() ([]byte, error) { return []byte(`"mail.Memory{redacted}"`), nil }

func NewMemory(config MemoryConfig) (*Memory, error) {
	if config.MaximumMessages == 0 {
		config.MaximumMessages = 100
	}
	if config.MaximumBytes == 0 {
		config.MaximumBytes = 64 << 20
	}
	if config.MaximumMessages < 1 || config.MaximumBytes < 1 {
		return nil, failure(CodeInvalidConfig, "memory_capacity", nil)
	}
	return &Memory{&memoryState{config: config}}, nil
}

func (memory *Memory) Send(ctx context.Context, message Message) error {
	if memory == nil || memory.state == nil {
		return failure(CodeInvalidConfig, "memory", nil)
	}
	delivery, err := prepare(ctx, message)
	if err != nil {
		return err
	}
	state := memory.state
	state.mu.Lock()
	defer state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return failure(CodeNotSent, "context", err)
	}
	if len(state.deliveries) >= state.config.MaximumMessages || len(delivery.state.data) > state.config.MaximumBytes-state.bytes {
		return failure(CodeCapacity, "memory", nil)
	}
	state.deliveries = append(state.deliveries, delivery)
	state.bytes += len(delivery.state.data)
	// Cancellation after this linearization point cannot undo acceptance.
	return nil
}

func (memory *Memory) Snapshot() ([]Delivery, error) {
	if memory == nil || memory.state == nil {
		return nil, failure(CodeInvalidConfig, "memory", nil)
	}
	state := memory.state
	state.mu.Lock()
	defer state.mu.Unlock()
	return append([]Delivery(nil), state.deliveries...), nil
}

// Drain atomically transfers the accepted deliveries and frees the capacity.
func (memory *Memory) Drain() ([]Delivery, error) {
	if memory == nil || memory.state == nil {
		return nil, failure(CodeInvalidConfig, "memory", nil)
	}
	state := memory.state
	state.mu.Lock()
	defer state.mu.Unlock()
	result := state.deliveries
	state.deliveries, state.bytes = nil, 0
	return result, nil
}
