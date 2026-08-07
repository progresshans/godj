# Third-party notices

## Django

GoDj uses Django 6.1 as a development-time behavioral reference and test
dependency. Django is installed from PyPI by the conformance workflow and is not
part of a GoDj binary or generated source.

The initial M0 scenarios are independently written behavioral scenarios. Their
manifest entries point to Django documentation and tests for provenance but do
not copy or translate Django source, fixtures, comments, or assertion structure.
This notice does not classify those scenarios as derivative works.

Django is licensed under the BSD 3-Clause license. A copy is included in
[`LICENSE.django`](LICENSE.django). If a future file copies, translates, or
adapts upstream expression, that file must be marked as derived in the contract
manifest and carry source commit, path, symbol, modification, and license
metadata close to the file.

Django is a trademark of the Django Software Foundation. GoDj is an independent
project and is not endorsed by the Django Software Foundation.
