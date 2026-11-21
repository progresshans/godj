# Password validation

`Binding.DefaultPasswordValidator` builds a policy for the selected account's
typed profile. It applies the four rules configured by Django startproject in
order: attribute similarity, minimum length 8, common passwords, and entirely
numeric passwords. The factory performs no I/O and installs no global policy.
An omitted runtime policy retains Django's empty `AUTH_PASSWORD_VALIDATORS`
strength-validation behavior.

```go
binding, err := project.BindAccount()
if err != nil { return err }
validate, err := binding.DefaultPasswordValidator(
    accountmodel.PasswordAttribute{Field: "contact", Label: "email address"},
    accountmodel.PasswordAttribute{Field: "display_name", Label: "display name"},
)
if err != nil { return err }
// Set the selected runtime's config.Accounts.ValidatePassword to validate.
```

The selected runtime shares this policy across Form/Admin/API, self-service
changes and password reset. Services check current authorization before the
policy, then recheck the current profile inside the final write fence after
hashing. Failed or uncertain rollback is an execution failure; it cannot be
rendered as an ordinary password rejection. Validators must remain pure and
safe for simultaneous calls, and never change the password bytes hashed.

`auth/passwordpolicy` owns the model-independent algorithms and pinned
dictionary. Its `Default()` factory returns the four immutable rules for hosts
that compose a custom typed policy. There is no separate fixed-Profile built-in
validator API.

Fields must belong to the selected model and have a string scalar kind. Labels
are static declaration metadata. Credential/principal identity, revision and
server timestamp roles cannot become similarity attributes. An unrelated model
field named `password` remains selectable when it is not the credential role.
Nullable attributes contribute no text when null. The constructor snapshots
metadata; validation reads detached typed values and preserves raw password
bytes. Cancellation is an execution error, never password diagnostics.

Omitted attributes select the actual login and optional distinct email roles;
an explicit empty slice selects none. Other profile fields are opt-in. A generated
`NewAccountRuntime` shares the configured password policy and byte envelope with
account creation, administrative changes, self-service changes and its optional
`PasswordResetter`. No reset key or policy is installed implicitly.

## Built-in semantics

- `NewMinimumLengthValidator(n)` counts Unicode scalar values, including
  combining characters, and emits `password_too_short` with `min_length`.
- `passwordpolicy.NewSimilarityValidator(maximum)` lowercases the
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

Minimum length must be nonnegative. The model-independent similarity rule
reads the ordered `[]passwordpolicy.Attribute` supplied for each invocation;
nil or empty values select no attributes. The typed binding above owns field
selection, source validation and the declared-attribute copy. Zero threshold
chooses 0.7. Explicit thresholds must be finite and at
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
Django distribution is embedded in [data](../auth/passwordpolicy/data/). Upstream attributes the list
to Royce Williams; the distribution contains **19,640** entries. Exact source,
compressed/decompressed hashes, sizes and count are recorded in
[password-sources.json](../auth/passwordpolicy/data/password-sources.json). Construction verifies the
compressed source hash and decoded size/count before publishing the immutable
shared dictionary. Configuration formatting never exposes dictionary contents.

The [independent observer](../conformance/runners/django/password_validation_reference.py)
executes Django validators and `validate_password` on synthetic inputs. It does
not read Go implementation/output. Tests compare individual and accumulated
errors/parameters for all 156 cases, a 16,245-case similarity matrix, and all
19,640 dictionary entries in 58,920 case/whitespace variants. The unchanged
[input corpus](../auth/passwordpolicy/testdata/password-inputs.json) and
[observation](../auth/passwordpolicy/testdata/password-django61.json) also supply
41 default-rule cases to the selected binding's typed advisory-profile test.
These pure observations do not prove account storage or HTTP acceptance.
Execution scope and results belong to
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md).
