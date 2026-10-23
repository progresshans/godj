# Independent synchronous event observations

`observe.py` uses public `django.dispatch.Signal` with the synthetic cases in
`input.json`. It never imports GoDj or reads Go tests or expected output.
`source-verification.json` binds the installed dispatcher and supporting modules
to upstream Django commit `fe0a859f537d4238cf49fca39073513206f83122` (BSD-3-Clause).
This module-level check is not a whole-wheel identity. The official behavioral
reference is [Django signals](https://docs.djangoproject.com/en/6.1/topics/signals/).

Nine cases observe actual calls, return/exception shapes, disconnection results
and robust-dispatch log metadata. Six have shared call-order/removal boundaries;
three record explicit differences in duplicate IDs, removal during a selected
snapshot and Python sender identity. Go's error values, attempted-result list,
panic/Goexit, cancellation, callback joining and typed payload ownership require
native tests. No DB transaction or durable-delivery claim is made by this observer.

The Python suite runs two hash seeds, checks the complete case/source inventory,
and changes the public send method to verify that observations follow actual
dispatch. Its raw CI log includes `DJANGO_EVENT_OBSERVATION`. Go's
`TestEventSynchronousReferenceBoundaries` uses the same input hash and emits the
six shared call/removal projections as `GODJ_EVENT_OBSERVATION`; it never reads
the Python output. Compare those receipts only from their actual CI executions.

Run through the repository's locked Python environment:

```sh
uv run --frozen python -m conformance.runners.django.event_reference.observe
```

Current execution status is maintained in [TEST_EVIDENCE](../../../../docs/status/TEST_EVIDENCE.md).
