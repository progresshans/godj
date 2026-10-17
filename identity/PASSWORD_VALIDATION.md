# Password validation

`DefaultPasswordValidators()` returns the four validators configured by Django
startproject, in order: user-attribute similarity, minimum length 8, common
passwords, and entirely numeric passwords. Calling the factory does not enable
policies globally. Omitting a Manager/Admin/API policy retains Django's empty
`AUTH_PASSWORD_VALIDATORS` behavior.

```go
validators, err := identity.DefaultPasswordValidators()
if err != nil {
    return err
}
manager, err := identity.NewManager(backend, hasher, authorizer,
    identity.WithPasswordValidators(validators...))
```

Admin uses `Config.WithPasswordValidators`; JSON API uses
`Config.PasswordValidators`. Article's `siteapp.Config.WithPasswordValidators`
selects the same policy for both surfaces. Constructors snapshot their slices;
validators must remain pure and safe for simultaneous calls. Validation does
not perform filesystem or database I/O and never changes the password hashed.
The manager applies the policy after current authorization and again inside
the final write fence. Failed or uncertain rollback is an execution failure,
not a renderable password rejection.

## Built-in semantics

- `NewMinimumLengthValidator(n)` counts Unicode scalar values, including
  combining characters, and emits `password_too_short` with `min_length`.
- `NewUserAttributeSimilarityValidator(SimilarityConfig{...})` lowercases the
  password and selected profile values using pinned Unicode 16, splits values
  on Python `\W+`, and compares each part and the full value using Django's
  `SequenceMatcher.quick_ratio` behavior (character multiset overlap). The
  threshold comparison is inclusive. The first matching attribute emits
  `password_too_similar` and its static `verbose_name`, never its value.
- `NewCommonPasswordValidator(nil)` selects the pinned Django dictionary.
  Candidate lowercase/strip affects the comparison only; the password remains
  unchanged. A match emits `password_too_common`.
- `NumericPasswordValidator{}` uses Unicode `isdigit`, including superscript
  and newly assigned digits. Empty strings and non-digit numeric characters
  such as fractions are not entirely numeric. Rejections use
  `password_entirely_numeric`.

Configured validators run in order and retain all diagnostics in that order.
The service/API field is `password`; Admin maps those diagnostics to
`password2` without redisplaying either secret. Parameters are immutable string
values in the framework's existing validation contract. UTF-8 is required;
malformed strings produce `invalid` rather than replacement characters.

## Go configuration boundaries

Minimum length must be nonnegative. Similarity supports `username`,
`first_name`, `last_name` and `email`; unknown attributes are explicit
configuration errors. Nil attributes choose all four; an empty slice chooses
none. Zero threshold chooses 0.7. Explicit thresholds must be finite and at
least 0.1; values above 1 select no similarity rejection, as in Django. These
are typed Go configuration decisions, not Python object/introspection parity.

A caller-supplied dictionary is an owned `[]string`: entries are stripped but
are not implicitly lowercased, matching Django's requirement for lowercase
custom lists. Nil selects the built-in list; an empty slice selects an empty
list. Custom lists are bounded to 1,000,000 entries and 16 MiB of input UTF-8.
Any file reading/decompression belongs to the caller before policy selection.

## Sources and license

The validator flow and error codes/parameters are adapted from Django 6.1
commit `fe0a859f537d4238cf49fca39073513206f83122`,
`django/contrib/auth/password_validation.py`, under the
[BSD-3-Clause license](../LICENSE.django). Copyright (c) Django Software
Foundation and individual contributors. Go's multiset implementation is
independent and does not reproduce Python's SequenceMatcher object machinery.

The unmodified `django/contrib/auth/common-passwords.txt.gz` from that pinned
Django distribution is embedded in [data](data/). Upstream attributes the list
to Royce Williams; the distribution contains **19,640** entries. Exact source,
compressed/decompressed hashes, sizes and count are recorded in
[password-sources.json](data/password-sources.json). Construction verifies the
compressed source hash and decoded size/count before publishing the immutable
shared dictionary. Configuration formatting never exposes dictionary contents.

The [independent observer](../conformance/runners/django/password_validation_reference.py)
executes Django validators and `validate_password` on synthetic inputs. It does
not read Go implementation/output. Tests compare individual and accumulated
errors/parameters, a similarity matrix, and all dictionary entries with case
and whitespace variants. Execution scope and results belong to
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md).
