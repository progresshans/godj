# 기준 출처

정확한 source/version/provenance는 [contract manifests](../conformance/contracts/)와 [profiles](../conformance/profiles/)가 소유한다.
이 문서는 필요한 upstream 위치를 찾는 인덱스다. Pinned version은 GoDj의 비교 기준이며 최신 upstream이라는 주장이 아니다.
기준 파일마다 해시·bytes·CI 실행 과정을 이곳에 다시 복사하지 않는다.

## Django와 DRF

- [Django 6.1 source](https://github.com/django/django/tree/fe0a859f537d4238cf49fca39073513206f83122)
- [QuerySet API](https://docs.djangoproject.com/en/6.1/ref/models/querysets/), [Model fields](https://docs.djangoproject.com/en/6.1/ref/models/fields/)
- [Model save와 relation descriptor](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/base.py),
  [related descriptors](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/fields/related_descriptors.py)
- [Migration loader](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/loader.py),
  [executor](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/executor.py),
  [operations](https://github.com/django/django/tree/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/operations)
- [Transaction와 application memory](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/docs/topics/db/transactions.txt)
- [Django tests](https://github.com/django/django/tree/fe0a859f537d4238cf49fca39073513206f83122/tests)
- [DRF 3.18 reference profile](../conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json)과 각 API/auth manifest의 pinned source
- [RFC 6750 Bearer token usage](https://www.rfc-editor.org/rfc/rfc6750)

Django 테스트는 관찰할 의미와 failure case를 찾는 자료다. 실제 파생물은 [LICENSING](LICENSING.md)에 따라 분류한다.
Python 클래스·private traversal·SQL/DOM 문자열 전체를 그대로 따라야 한다는 뜻은 아니다.
Django `MigrationLoader`의 sibling 순서와 GoDj canonical order 차이는 [DEV-0002](DEVIATIONS.md#dev-0002--app-zero의-incomparable-sibling은-godj-canonical-order를-유지)에 기록한다.

## Go와 DB

- [Go specification](https://go.dev/ref/spec), [context](https://pkg.go.dev/context), [database/sql](https://pkg.go.dev/database/sql)
- [Go testing](https://pkg.go.dev/testing), [Go command](https://pkg.go.dev/cmd/go)
- [SQLite foreign keys](https://sqlite.org/foreignkeys.html), [transactions](https://sqlite.org/lang_transaction.html), [ALTER TABLE](https://sqlite.org/lang_altertable.html)
- [modernc SQLite driver](https://pkg.go.dev/modernc.org/sqlite)
- [pgx](https://github.com/jackc/pgx), [PostgreSQL documentation](https://www.postgresql.org/docs/17/)

빌드와 runtime의 dependency version은 [go.mod](../go.mod)/[go.sum](../go.sum), Python environment는
[pyproject.toml](../pyproject.toml)/[uv.lock](../uv.lock), Hosted service profile은 [workflow](../.github/workflows/)에서 확인한다.
Local moving checkout이나 보고 당시의 tool version을 새 실행의 환경으로 가정하지 않는다.

## GoDj 결정과 과거 조사

Go-specific loader/CLI/wire/resource 정책은 [ADR](adr/README.md)의 이유와 계약의 decision provenance를 사용한다.
Django-named reference corpus에 저장되어 있어도 GoDj 정책을 Django의 결정으로 표현하지 않는다.
이전 조사에서 읽은 exact blob·symbol·hash와 실행 과정은
[정리 전 출처 기록](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/SOURCES.md)에 보존한다.
현재 코드 수정에 필요한 근거만 이 인덱스나 해당 contract에 추가한다.
