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

통합 개발 경험과 API 사용성의 공식 문서·태그 고정 소스 비교는
[프레임워크 조사](research/2026-09-12-framework-developer-experience.md#출처)에 있다.
이 자료의 조사 버전을 현재 Django/DRF conformance profile이나 제품 dependency로 자동 채택하지 않는다.

일반 int64 필드는 고정된 Django 6.1 `BigIntegerField`와 그 integer form을 참조한다. BSD-3-Clause 소스 위치와 비교 범위는
[ADR-0059](adr/0059-signed-integer-field-and-model-growth.md#출처), 실제 입력·오류 관찰은
[integer reference runner](../conformance/runners/django/integer_field_reference.py)에 있다.

모델·operation 문서의 표현 기준은 [OpenAPI 3.1.1](https://spec.openapis.org/oas/v3.1.1.html)이다.
독립 규격 검사는 [openapi-spec-validator](https://openapi-spec-validator.readthedocs.io/en/latest/)를 격리 실행하며,
실제 사용 버전과 범위는 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)에 기록한다.

외부 Go client 검증은 [ogen v1.24.0](https://github.com/ogen-go/ogen/tree/v1.24.0)의 client generator를 고정해서 사용한다.
[설정과 module lock](../api/openapi/consumertest/testdata/client/go.mod),
[생성·검증 절차](../api/openapi/consumertest/README.md)에 실제 소비 경계를 둔다. 다른 generator의 지원을 추론하지 않는다.

TextField와 Form widget·빈 문자열 정책은 같은 고정 Django의 CharField/TextField·Char Form·textarea template을 참조한다.
[ADR-0060](adr/0060-text-field-and-form-widget-semantics.md#출처와-제한), [실제 관찰 runner](../conformance/runners/django/text_field_reference.py)에 비교 범위와 출처를 둔다.

DateTimeField는 고정 Django 6.1의 model/form field·dateparse와 SQLite/PostgreSQL adapter를 참조한다(BSD-3-Clause).
[ADR-0061](adr/0061-datetime-field-and-canonical-instant-values.md#출처와-남은-범위), [UTC 관찰 runner](../conformance/runners/django/datetime_field_reference.py)에
Python 3.14.3 환경·지원 입력·정규화 정책·NUL 결과 차이를 명시한다.

Calendar Date는 같은 고정 Django의 model/form DateField·dateparse·en-us date input formats와 DRF 3.18.0 DateField를
참조한다(BSD-3-Clause). [ADR-0065](adr/0065-calendar-date-field-and-input-boundaries.md#출처와-검증-경계)와
[독립 reference](../conformance/runners/django/calendar_date_reference.py)에 입력 경계·실제 DB 관찰을 둔다. Go의 comparable Date,
invalid literal·strict canonical storage·scanner의 시각 거부는 별도로 명시한 Go API 정책이다.

Clock Time은 [Django 6.1 model fields](https://github.com/django/django/blob/6.1/django/db/models/fields/__init__.py),
[Form fields](https://github.com/django/django/blob/6.1/django/forms/fields.py)·[Form initial 처리](https://github.com/django/django/blob/6.1/django/forms/forms.py),
[dateparse](https://github.com/django/django/blob/6.1/django/utils/dateparse.py)와
[DRF 3.18.0 fields](https://github.com/encode/django-rest-framework/blob/3.18.0/rest_framework/fields.py)를 참조한다(BSD-3-Clause).
Python 버전별 clock grammar는 [CPython 3.14.3](https://github.com/python/cpython/blob/v3.14.3/Modules/_datetimemodule.c)의 public 결과를 참조한다(PSF license).
SQL driver 경계는 [pgx v5.10.0 TimeCodec](https://github.com/jackc/pgx/blob/v5.10.0/pgtype/time.go)(MIT), wire 의미는
[OpenAPI time registry](https://spec.openapis.org/registry/format/time)의 RFC3339 full-time 정의를 확인했다.
[ADR-0066](adr/0066-clock-time-field-and-precision-boundaries.md)와 [독립 runner](../conformance/runners/django/clock_time_reference.py)는
기본 Django 위젯의 초기 소수초 제거와 Go의 precision 보존, JSON NUL 거부와 Python typed aware time의 별도 관찰을 명시한다.

Duration의 값·입력·DB 범위와 exact JSON number는 [ADR-0067](adr/0067-duration-model-range-and-number-input.md)에 정리한다.
고정 Django/DRF의 실제 public API 관찰과 JSON numeric ingress는 [Duration runner](../conformance/runners/django/duration_reference.py)가 소유한다.

FloatField의 구현 준비는 같은 pinned Django/DRF의 model/form FloatField·JSONRenderer를 독립 관찰한다(BSD-3-Clause).
[ADR-0068](adr/0068-binary64-field-and-finite-json-boundaries.md), [runner](../conformance/runners/django/float_reference.py),
[실제 관찰과 범위](status/TEST_EVIDENCE.md#gdj-0090--float의-독립-기준-준비)를 따른다.
