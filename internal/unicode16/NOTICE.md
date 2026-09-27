# Pinned Unicode text profile

This package uses Unicode **16.0.0**, matching the pinned CPython 3.14.3/Django
6.1 reference. Its tables do not change with the Go compiler or x/text version.
The existing x/text 15 normalization applies a stream-safe transform that can
insert CGJ after long non-starter sequences; Python's NFKC does not. This package
implements NFKC without that additional transform and default full lowercase
mapping, including Final_Sigma. Language-specific casing is not selected.

The algorithms follow [UAX #15 revision 56](https://www.unicode.org/reports/tr15/tr15-56.html)
and the default casing/property data in the [Unicode 16 UCD](https://www.unicode.org/Public/16.0.0/ucd/).
The independently written Go implementation does not copy x/text or CPython
implementation code. Generated tables and compressed upstream data are covered
by the included [Unicode license](LICENSE.unicode). Exact upstream URLs, byte
sizes and SHA256 values are recorded in [sources.json](sources.json).

`generate.py` validates every input before formatting and atomically replacing
the Go table file. The checked-in gzip inputs reproduce the tables offline:

```sh
python3 internal/unicode16/generate.py --check
python3 -m unittest discover -s internal/unicode16 -p 'test_*.py'
```

To refresh an external cache from the pinned URLs, use `--data-dir <cache> --fetch`;
`--check` prevents replacing generated Go output. The generator reads Unicode
data directly and never derives its tables from Python's normalization output.

Tests execute all 19,965 rows (five columns each) of the official normalization
corpus. A separate pinned [CPython observer](../../conformance/runners/django/unicode16_reference.py)
hashes NFKC, lowercase, character properties and four casing contexts over every
Unicode scalar. Surrogates are outside the accepted UTF-8 input domain. Long
combining sequences, Hangul and malformed UTF-8 have explicit regression cases.
These are verification mechanisms; completed execution is recorded in TEST_EVIDENCE.
