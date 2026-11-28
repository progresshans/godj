# Process-local events

`events.New[T](Config{Name, Logger})` creates one explicitly owned event.
`Subscribe(name, func(context.Context, T) error)` retains a receiver strongly and
returns its subscription. Names are public diagnostic identifiers, with at most
128 ASCII letters/digits/underscore/dot/hyphen; the first character must be a
letter or underscore. Duplicate names, nil handlers and more than 1,024 active
receivers are rejected. There is no global registry or import-time registration.

`Send(ctx, value)` runs receivers in registration order and stops at the first
error. It returns attempted receivers and their errors. `SendRobust` continues
after receiver errors, which remain in `[]Result`; its separate error reports
invalid invocation, close or cancellation. Both join every admitted callback,
including its defers on panic or Goexit. Panic values are not exposed.

Each send snapshots registrations. A newly registered receiver participates in
the next send, including a nested send. Removing a receiver prevents a call that
has not yet been admitted, even if it appeared in an earlier snapshot. Callbacks
run without the event lock and can subscribe, unsubscribe or send again.
Concurrent sends may enter the same receiver concurrently. The caller owns
synchronization of callback captures and payloads. Go assignment copies the
payload; maps, slices, pointers and interfaces are not deep-copied.

```go
type NoteSaved struct { ID int64 }
saved, err := events.New[NoteSaved](events.Config{Name: "notes.saved", Logger: logger})
if err != nil { return err }
subscription, err := saved.Subscribe("notify", notifySavedNote)
if err != nil { return err }
// After the application has confirmed its transaction:
results, deliveryErr := saved.SendRobust(ctx, NoteSaved{ID: stored.ID})
observeNotifications(results, deliveryErr) // Preserve the confirmed save result.
subscription.Unsubscribe()
```

The example's notification and observation functions belong to the application.
Events do not infer commit/rollback, retry callbacks or provide an outbox,
durable queue or cross-process transport. A failed notification after a confirmed
write must not be reported as if that write were rolled back. Required business
invariants belong in explicit calls and the owning transaction.

`Unsubscribe` and `Close` stop future admissions without blocking; they are safe
from a receiver. `Subscription.Wait(ctx)` observes completion after removal;
`Event.Wait(ctx)` observes completion after Close. A canceled Wait does **not**
mean callbacks have finished or their resources can be released. Owners cancel
their delivery contexts and wait for successful drain before closing resources.
An app Stop that must join owned work may use `context.WithoutCancel(cleanup)`
after canceling that work. A handler that ignores cancellation can delay return;
the framework never abandons a running callback or forcibly terminates Go code.

Waiting on the active receiver or any enclosing send using its supplied/derived
context returns `would_deadlock`. Nested sends retain this detection. Replacing
the context with an unrelated one discards cancellation and reentrancy markers.
Completed frames no longer cause a false cycle. Event and Subscription values
must not be copied; their zero values are invalid.

An optional caller-owned `*slog.Logger` records closed component/action/outcome,
registered names and a separate cancellation flag. Add project/invocation
attributes with `Logger.With`; nil emits nothing. Payloads, arbitrary error text,
credentials and private protocol bytes are never automatically logged. Logger
errors, panic and Goexit do not change delivery results. Handler calls are joined
and recursive logging with the supplied context is suppressed. The caller owns
the sink, its concurrency safety, additional attributes and timely return.

The fixed Django [independent observer](../conformance/runners/django/event_reference/README.md)
records registration order and ordinary/robust dispatch. Go deliberately rejects
duplicate names, stops removed-but-unadmitted callbacks, and uses typed event
instances instead of Python sender identity/weak references. Source-bound actual
execution and remaining C1 requirements are in [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md).
