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

[Update-or-create observer](../conformance/runners/django/update_or_create_reference.py)와
[행 잠금 observer](../conformance/runners/django/row_lock_reference.py)는 고정 Django 6.1의
[QuerySet](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/query.py),
[Atomic](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/transaction.py),
[SQLCompiler](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/sql/compiler.py)를
실행한다(BSD-3-Clause). 직접 작성한 입력·관찰 코드와 두 DB 출력은
[생성 소비자 fixture](../codegen/consumertest/testdata/updateorcreate/)로 보존한다. 명시적 Go 차이는
[ADR-0087](adr/0087-row-locking-and-update-or-create.md), source·환경·실행은 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)가 소유한다.

[Bulk-create observer](../conformance/runners/django/bulk_create_reference.py)는 같은 고정 Django 6.1의
QuerySet·Atomic·SQLInsertCompiler를 실행한다(BSD-3-Clause). 직접 작성한 입력과 각 사례의 새 table/sequence로
[양 DB의 원 출력](../codegen/consumertest/testdata/bulkcreate/)을 보존한다. 입력 객체·빈 입력·ignore·부모 savepoint와
literal commit 오류의 Go 차이는 [ADR-0088](adr/0088-bulk-creation-and-native-batch-ownership.md)이 설명한다.

[Query-update observer](../conformance/runners/django/query_update_reference.py)와
[동시 갱신 observer](../conformance/runners/django/query_update_concurrency_reference.py)는 같은 고정 Django 6.1의
QuerySet·Atomic·SQLUpdateCompiler·UpdateQuery·CombinedExpression을 실행한다(BSD-3-Clause).
직접 작성한 입력으로 각 사례의 새 table과 PostgreSQL 서버가 확인한 실제 lock wait를 관찰한다.
Go source/output·expected fixture를 읽지 않으며 [양 DB 원 출력](../codegen/consumertest/testdata/queryupdate/)을 보존한다.
수치/NULL·cache·transaction 의미와 명시적 미지원은 [ADR-0090](adr/0090-query-update-and-scalar-expressions.md),
환경·source hash·실행 결과는 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)가 소유한다.

## Go와 DB

- [Go specification](https://go.dev/ref/spec), [context](https://pkg.go.dev/context), [database/sql](https://pkg.go.dev/database/sql)
- [Go testing](https://pkg.go.dev/testing), [Go command](https://pkg.go.dev/cmd/go)
- [RFC 9110 HTTP Semantics](https://www.rfc-editor.org/rfc/rfc9110.html#section-13): conditional 우선순위·validator·Range/HEAD/206/304/412/416 기준.
  [고정 Django conditional observer](../conformance/runners/django/file_conditional_reference.py)는 같은 6.1 `django/utils/cache.py`의
  공통 결과와 세 명시적 차이를 관찰한다. Range의 공통 결과는 실행 Go 버전의 `net/http.ServeContent`와 별도로 대조한다.
- [RFC 9110 204](https://www.rfc-editor.org/rfc/rfc9110.html#section-15.3.5)·[Content-Length](https://www.rfc-editor.org/rfc/rfc9110.html#section-8.6),
  [RFC 9112 Transfer-Encoding](https://www.rfc-editor.org/rfc/rfc9112.html#section-6.1): typed NoContent의 본문/trailer·framing 제한과 resource metadata 기준. 구현 코드는 직접 작성했다.
- [SQLite foreign keys](https://sqlite.org/foreignkeys.html), [transactions](https://sqlite.org/lang_transaction.html), [ALTER TABLE](https://sqlite.org/lang_altertable.html)
- [modernc SQLite driver](https://pkg.go.dev/modernc.org/sqlite)
- [pgx](https://github.com/jackc/pgx), [PostgreSQL documentation](https://www.postgresql.org/docs/17/)
- [PostgreSQL 17 SELECT locking clause](https://www.postgresql.org/docs/17/sql-select.html#SQL-FOR-UPDATE-SHARE),
  [DECLARE와 cursor 수명](https://www.postgresql.org/docs/17/sql-declare.html): [행 잠금 결정](adr/0087-row-locking-and-update-or-create.md)의 SQL capability와 실제 transaction 경계.

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

FloatField는 같은 pinned Django/DRF의 model/form FloatField·JSONRenderer를 독립 관찰한다(BSD-3-Clause).
[ADR-0068](adr/0068-binary64-field-and-finite-json-boundaries.md), [runner](../conformance/runners/django/float_reference.py),
[실제 관찰과 범위](status/TEST_EVIDENCE.md#gdj-0090--float-모델과-finite-소비자-연결)를 따른다.

DecimalField는 같은 pinned Django의 model/form DecimalField·DecimalValidator·SQLite adapter와 DRF DecimalField를 참조한다(BSD-3-Clause).
[독립 runner](../conformance/runners/django/decimal_reference.py)는 public 입력·precision·JSON 표현과 실제 SQLite 저장 결과를 생성하고,
[ADR-0069](adr/0069-exact-decimal-values-and-storage.md)는 exact 값·SQLite BLOB·PostgreSQL NUMERIC과 JSON lexical Decimal profile을 구분한다.
[GDJ-0091](../work/0091-decimal-cost-models.md)과 실행 증거에서 설계 채택·제품 연결·환경별 검증을 별도로 유지한다.

OneToOne은 같은 pinned Django의 [관계 필드](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/fields/related.py),
[관계 descriptor](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/fields/related_descriptors.py),
[공개 OneToOneField 동작](https://docs.djangoproject.com/en/6.1/ref/models/fields/#onetoonefield)을 참조한다(BSD-3-Clause).
[독립 runner](../conformance/runners/django/one_to_one_reference.py)는 `derived=false`로 작성한 public ORM·ModelForm·migration 입력이다.
기대 fixture와 GoDj를 읽지 않으며 [활성 작업](../work/0096-one-to-one-service-reports.md)에서 기준 관찰과 제품 구현을 구분한다.

관계 선택 Form은 같은 pinned Django의 `django/forms/models.py` ModelChoiceField와 AutoField key lookup을 참조한다(BSD-3-Clause).
[독립 ModelChoice runner](../conformance/runners/django/model_choice_reference.py)는 실제 양 DB QuerySet의 scope·입력·initial/changed를
관찰하고 설치된 source 파일의 SHA256을 기록한다. GoDj나 기대 fixture를 읽지 않는다. Snapshot과 int64 key를 반환하는
Go API의 소유권 차이는 [ADR-0073](adr/0073-one-to-one-cardinality-and-reverse-objects.md#작업-보고서와-명시적-관계-선택)에 구분한다.

모델 복합 고유성은 같은 pinned Django의 [UniqueConstraint](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/constraints.py),
Model/ModelForm의 constraint 검증과 [migration autodetector](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/autodetector.py)를 참조한다(BSD-3-Clause).
[독립 runner](../conformance/runners/django/composite_unique_reference.py)는 `derived=false`인 선언 입력으로 양 DB의 오류·NULL·transaction·migration 결과와
이름·필드 변경 및 순환 관계의 제약 배치를 관찰한다. GoDj나 기대 fixture를 읽지 않으며 설치된 upstream source SHA256을 남긴다.
기준 관찰과 GoDj 제품 지원은 [GDJ-0097](../work/0097-composite-uniqueness-and-labels.md)에서 구분한다.

CASCADE는 같은 pinned Django의 [deletion collector](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/deletion.py)와
[관계 선언·자동 intermediary](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/fields/related.py)를 참조한다(BSD-3-Clause).
[독립 runner](../conformance/runners/django/cascade_reference.py)는 직접 작성한 public model·ORM 입력이며 GoDj나 기대 fixture를 읽지 않는다.
Recursive 보호·SET_NULL·중복 경로·숨긴 관계·required/nullable 순환·실패 rollback과 실제 intermediary 정리를 관찰하고 upstream source SHA256을 남긴다.
GoDj의 native 검사 시점·graph ownership·지원 상태는 [GDJ-0098](../work/0098-cascade-and-ticket-label-links.md)과 실행 근거에서 구분한다.

ManyToMany는 같은 pinned Django의 [관계 관리자](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/fields/related_descriptors.py),
위 관계 선언과 [migration operations](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/operations/fields.py)를 참조한다(BSD-3-Clause).
[독립 runner](../conformance/runners/django/many_to_many_reference.py)는 직접 작성한 public model·ORM·migration 입력이며 GoDj나 기대 fixture를 읽지 않는다.
자동/명시적 through·동시 중복·set rollback·cache·self 관계·조회 multiplicity·migration·signal을 관찰하고 upstream module SHA256을 저장한다.
이 기준 확보와 제품 지원은 [GDJ-0099](../work/0099-many-to-many-and-ticket-label-collections.md)에서 구분한다.

ModelMultipleChoice는 같은 pinned Django의 `django/forms/models.py` ModelMultipleChoiceField와 BigAutoField의
실제 QuerySet을 참조한다(BSD-3-Clause). [독립 runner](../conformance/runners/django/model_multiple_choice_reference.py)는
공개 Form/모델 입력만 사용하며 GoDj나 기대 fixture를 읽지 않는다. HTML 제출 목록의 membership·required·순서·중복·
initial/changed를 양 DB에서 관찰하고, transport 밖 Python 입력과 int64 밖 native 오류를 별도로 기록한다.
구현을 번역한 파일이 아니며 source module SHA256·determinism·의미 변경 control은 TEST_EVIDENCE에 남긴다.

Ticket 컬렉션 transport는 pinned Django 6.1·DRF 3.18.0의 public ModelSerializer와
PrimaryKeyRelatedField(many=True, pk_field=IntegerField), 명시적 through 모델을 참조한다(BSD-3-Clause).
[독립 runner](../conformance/runners/django/ticket_collection_reference.py)는 GoDj/기대값을 읽지 않고
생성·PUT·PATCH 48개 canonical 입력, 별도 coercion, explicit outer atomic rollback과 validation 이후 scope 변화를 관찰한다.
Raw fixtures에는 DRF fields/relations/serializers source SHA256을 보관한다. 실행한 버전·DB와 source별 검증은 TEST_EVIDENCE를 따른다.


Credential/session 관찰은 고정 Django 6.1(BSD-3-Clause)의 `django.contrib.auth`, `base_user`, `backends`,
`hashers`와 실제 SessionMiddleware/AuthenticationMiddleware를 실행한다.
[독립 runner](../conformance/runners/django/credential_session_reference.py)는 공개 API로 새로 작성했으며 GoDj나 예상 fixture를 읽지 않는다.
양 DB fixture의 `source_sha256`에 실행한 네 auth module의 바이트 해시를 보존한다.
[ADR-0076](adr/0076-credential-snapshots-and-session-binding.md)과 [DEV-0013](DEVIATIONS.md#dev-0013--credential-session의-go-표현과-invalid-identity-정리)이 비교 범위를 소유한다.


모델 FileField의 기본 이름 길이·폼 생략/clear·파일 저장과 DB rollback 의미는 고정 Django 6.1의
`django/db/models/fields/files.py`(BSD-3-Clause)를 독립 실행한다.
[관찰 runner](../conformance/runners/django/model_file_reference.py),
[고정 관찰](../forms/model/testdata/model-file-django61.json), [GoDj의 경계](adr/0082-file-storage-publication-and-reference.md)를 따른다.

Storage alias/URL과 파일 응답은 같은 고정 Django 6.1의 `django/core/files/storage/handler.py`, `filesystem.py`,
`django/http/response.py`(BSD-3-Clause)를 독립 실행한다. [관찰 runner](../conformance/runners/django/storage_serving_reference.py)와
[고정 관찰](../storage/testdata/serving-django61.json)에 source SHA256을 보존한다. 같은 alias identity와 URL escaping, 명시한
MIME/attachment·length·stream/file close를 비교하며 GoDj의 eager backend 등록·lazy response descriptor·안전한 기본 MIME와
portable 이름 제한은 [파일 경계](adr/0082-file-storage-publication-and-reference.md)에서 차이로 구분한다.

메모리 storage의 정상 저장·격리와 명시적 수명/실패 차이는 같은 고정 source의
`django/core/files/storage/memory.py`(BSD-3-Clause)를 [독립 관찰](../conformance/runners/django/memory_storage_reference.py)한다.
[고정 관찰](../storage/testdata/memory-django61.json)의 source SHA256과 [storage 계약](../storage/README.md)을 따른다.


이미지 입력은 고정 Django 6.1의 `django/forms/fields.py`(BSD-3-Clause)와 Pillow 12.3.0(MIT-CMU)의 공개 Image API를
[독립 관찰](../conformance/runners/django/image_field_reference.py)한다. Python/Django/Pillow 버전은 별도
[이미지 reference 프로젝트](../conformance/reference/images/pyproject.toml)와 [lock](../conformance/reference/images/uv.lock)으로 고정하고
[관찰 fixture](../forms/testdata/image-django61.json)에 실행 source SHA256과 직접 생성한 합성 image bytes를 둔다.
GoDj wrapper와 GIF/container 예산 검사는 독립 작성했으며 실제 디코딩은 Go 1.26.5의 `image/png`, `image/jpeg`, `image/gif`
및 [golang.org/x/image v0.46.0](https://pkg.go.dev/golang.org/x/image@v0.46.0)의 WebP·BMP·TIFF decoder(BSD-3-Clause)를 사용한다.
지원 형식·전체 GIF 검증·한도/취소의 차이는 [이미지 계약](../uploads/README.md#이미지-내용-검증)과 ADR-0082를 따른다.


모델 이미지의 기본 이름 길이·폭/높이 필드 반영·기존 파일 재개방·clear/DB rollback/삭제 의미는 같은 고정 Django 6.1의
`django/db/models/fields/files.py`(BSD-3-Clause, SHA-256
`fed8e0f0f32feb483bcc96fb16ce417f77493079981c252182a4fee17b4298c8`)를 참조한다.
[독립 observer](../conformance/runners/django/model_image_reference.py)는 Pillow 12.3.0으로 합성 PNG만 만들며
[관찰값](../forms/model/testdata/model-image-django61.json)에 15개 폼 결과와 실제 저장/rollback을 남긴다. 기존 파일을 다시 여는
Django의 동작과 GoDj의 명시적인 I/O 경계는 [모델 이미지](../forms/model/README.md#모델-이미지와-크기-필드)에 구분한다.

저장된 이미지의 명시적 크기 갱신은 같은 `django/db/models/fields/files.py`와
`django/core/files/images.py`(BSD-3-Clause, SHA-256
`32985b35b2436e046568049eed52caff8232271c45b3b5cc5533d0238915fc33`)를 독립 실행한다.
[Observer](../conformance/runners/django/stored_image_reference.py)와 [고정 관찰](../storage/model/testdata/stored-image-django61.json)에
9개 검사·cache 재사용/새 instance·명시적 DB 저장/rollback을 기록한다. GoDj의 새 reader와 전체 내용 검증, 원본 모델 보존은
[명시적 검사 계약](../storage/model/README.md)을 따르며 native의 손상 내용/부분 header/cache 결과와 구분한다.

BMP/DIB·TIFF는 같은 고정 Django/Pillow 환경의 [codec observer](../conformance/runners/django/image_codec_reference.py)에서
36개 입력을 독립 실행한다. [관찰값](../uploads/testdata/image-codecs-django61.json)의 합성 palette/V4/V5/alpha·endian·압축·
여러 페이지·tile과 손상 입력에 원본 bytes와 native 진단을 둔다. `PIL.BmpImagePlugin`의 SHA-256은
`e0067eb268d1257de5a1d746652938a39107a06eb5e3fe415d31ee07b7a83b0f`, `PIL.TiffImagePlugin`은
`440b2a3a80b280d18cda3fc70d9fd206e2fa44e9897c9550f70b51395047b273`다(MIT-CMU). GoDj의 DIB prefix·TIFF 페이지/예산 검사는
독립 작성했고 실제 pixel 디코딩에는 위 고정 Go 라이브러리를 사용한다. 미지원 특성·전체 내용 검증의 차이는 이미지 계약을 따른다.

APNG의 구조·sequence·frame 범위·data 상속은
[PNG Third Edition, W3C Recommendation 2025-06-24](https://www.w3.org/TR/2025/REC-png-3-20250624/)을 기준으로
독립 작성했다. PNG pixel 디코딩은 Go 1.26.5 표준 라이브러리의 BSD-3-Clause 구현을 사용한다. 같은 고정 Django/Pillow의
[observer](../conformance/runners/django/apng_reference.py)와 [49개 관찰](../uploads/testdata/apng-django61.json)에 폼 결과와
별도 frame player 결과를 구분한다. `PIL.PngImagePlugin` SHA-256은
`5911ebb3c8e58edf4fccace85ade20c3a062cc41dc55340bfb7b40f0bf1861c5`다(MIT-CMU). 합성 grayscale/RGB/RGBA·palette·16-bit·Adam7·
기본 이미지/부분 frame·분할/빈 data·잘못된 control/CRC/pixels를 포함한다. 고정 player의 Adam7/ancillary 실패도 관찰값에 남긴다.


WebP의 RIFF·VP8X·ANIM/ANMF·ALPH 구조, 크기/좌표·padding·reserved/future field 처리는
[Google WebP Container Specification](https://developers.google.com/speed/webp/docs/riff_container)을 기준으로 독립 작성했다.
Pixel 디코딩은 위 고정 `golang.org/x/image`의 BSD-3-Clause 구현을 사용한다. 같은 고정 Django/Pillow와 libwebp 1.6.0의
[독립 observer](../conformance/runners/django/webp_animation_reference.py), [61개 관찰](../uploads/testdata/webp-animation-django61.json)은
lossy/lossless·raw/compressed alpha·부분 frame·metadata·reserved field·손상 입력의 폼/별도 frame 결과를 구분한다.
`PIL.WebPImagePlugin` SHA-256은 `634360a326abfcd29ec5e05f6b84c3be6e5a783e7c4dd6ad46bd0eba182dae23`다(MIT-CMU).
Native의 관대한 control/alpha 처리와 일부 reserved header/player 거부를 GoDj의 검증 결과와 동일시하지 않는다.

S3 요청·SigV4·checksum 직렬화는 [AWS SDK for Go v2 S3 v1.113.4](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/s3@v1.113.4),
core v1.47.1과 Smithy Go v1.28.1(Apache-2.0)을 사용한다. 직접 구현한 wrapper의 게시 결과/reader 수명은
[PutObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_PutObject.html),
[GetObject](https://docs.aws.amazon.com/AmazonS3/latest/API/API_GetObject.html),
[checksum](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/s3-checksums.html)과
[서명 URL](https://docs.aws.amazon.com/AmazonS3/latest/userguide/using-presigned-url.html)의 공개 계약을 참고한다.
Dependency version과 module checksum은 go.mod/go.sum이 소유한다. 서비스 오류를 Python storage 내부 구조로 번역하지 않는다.

독립 실제 서비스는 [MinIO commit 9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a](https://github.com/minio/minio/tree/9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a)
(AGPL-3.0-or-later, RELEASE.2025-10-15T17-29-55Z)를 별도 프로세스로 빌드/실행한다. GoDj 제품에 링크하거나 server 소스를 복사하지 않는다.
정확한 module/해시/빌드 profile은 [S3 service owner](../scripts/ci/s3_service.py), 독립 bucket setup/cleanup은
[s3fixture](../conformance/s3fixture/fixture.go)가 소유한다. 고정 service의 checksum 거부 코드와 version/range/서명 동작은
실제 요청으로 확인하며 AWS 자체의 실행 증거와 구분한다.


SlugField의 기본 길이/인덱스·ASCII/Unicode 문법·Form/serializer는 고정 Django 6.1의
`django/db/models/fields/__init__.py`, `django/forms/fields.py`, `django/core/validators.py`(BSD-3-Clause)와
DRF 3.18.0의 `rest_framework/fields.py`(BSD-3-Clause)를 독립 실행한다.
[Observer](../conformance/runners/django/slug_field_reference.py)는 합성 73개 입력만 사용하며
[SQLite](../internal/slugtest/testdata/slug-django61-sqlite.json)·[PostgreSQL](../internal/slugtest/testdata/slug-django61-postgres.json)
관찰에 실행 source SHA256을 보존한다. 모델 선택·후보·commit=False/저장·rollback과 여섯 index 변경 단계를 포함한다.
Unicode 문자표는 기존 고정 Unicode 16 구현을 재사용한다. 공개 문서는
[Django SlugField](https://docs.djangoproject.com/en/6.1/ref/models/fields/#slugfield),
[Django Form SlugField](https://docs.djangoproject.com/en/6.1/ref/forms/fields/#slugfield),
[DRF SlugField](https://www.django-rest-framework.org/api-guide/fields/#slugfield)를 참고하며
마지막 LF와 PostgreSQL pattern-opclass 차이는 [ADR-0084](adr/0084-slug-fields-and-column-index-ownership.md)에 구분한다.


BinaryField와 공통 editable 입력 정책은 고정 Django 6.1의 `django/db/models/fields/__init__.py`,
`django/forms/models.py`, `django/forms/fields.py`와 DRF 3.18.0의 `rest_framework/fields.py`,
`rest_framework/serializers.py`(각 BSD-3-Clause)를 독립 실행하여 관찰한다.
[Observer](../conformance/runners/django/binary_field_reference.py)와 합성 [입력](../internal/binarytest/testdata/inputs.json),
[SQLite](../internal/binarytest/testdata/django61-sqlite.json)·[PostgreSQL](../internal/binarytest/testdata/django61-postgres.json)
관찰에 실행 환경과 모듈 source SHA256을 보존한다. Python 원문을 Go로 번역한 구현이 아니며
39개 입력·7개 profile과 실제 저장/조회·schema 변경/역방향·deferred save·rollback의 외부 결과를 비교한다.
임의 Python 객체 입력, read-only 제출과 PostgreSQL Min/Max 차이는
[Binary 결정](adr/0085-binary-fields-and-model-input-policy.md)과 [DEV-0020](DEVIATIONS.md#dev-0020--binary의-닫힌-입력과-명시적-집계)에 구분한다.

단건 `Get`·`GetOrCreate`와 nested transaction은 고정 Django 6.1의
[`django/db/models/query.py`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/query.py)와
[`django/db/transaction.py`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/transaction.py)
(BSD-3-Clause)의 공개 동작을 독립 실행하여 관찰한다.
[Observer](../conformance/runners/django/single_object_reference.py)는 Go source·Go output·예상 fixture를 읽지 않는다.
[SQLite](../codegen/consumertest/testdata/singleobject/django61-sqlite.json)·
[PostgreSQL](../codegen/consumertest/testdata/singleobject/django61-postgres.json) 관찰에 Python/Django version,
실행한 두 Django 모듈과 observer 자신의 SHA-256을 보존한다. 조회 11개, 생성/중첩 scope 9개와 두 연결의
실제 unique 경쟁을 포함한다. 각 DB에서 독립 두 프로세스의 byte 일치를 확인했다.

별도 생성 Go module은 객체 값·cache 독립성·입력 지연 평가·안정된 오류 범주와 실제 저장/rollback·경쟁 결과를 비교한다.
Python 예외명은 명시한 Go 오류 code에 대응하며 원문 문구는 ABI가 아니다. `Get`의 SQL 개수/정렬 여부는 native 진단으로
보존하고 Go SQL 문자열/개수와 동일하다고 주장하지 않는다. Go의 정렬/빈 SQL 경계는 별도 ORM/backend 검사에서 검증한다.
생성 비교의 transaction 기록은 실제 native owner의 진입·성공/rollback 후 반환에 대응하는 DB port 경계를 기록한다.
Native SQL의 savepoint 이름이나 물리 제어문 문자열을 비교하는 recorder가 아니며, 제어문/실패 정리는 양 backend 검사가 소유한다.
Python keyword/default 입력을 소스 호환으로 구현하지 않는다. Go는 명시적인 typed CreateInput과 원 Manager snapshot을 사용하고
잘못된 생성 필드는 mutation 검증에서 거부한다. 관련 계약은 [ADR-0086](adr/0086-single-object-creation-and-savepoint-ownership.md)을 따른다.

GDJ-0110의 selected-field bulk update 기준은 고정 Django 6.1의 `django/db/models/query.py`,
`django/db/models/sql/compiler.py`, `django/db/transaction.py`와
[공식 QuerySet 문서](https://docs.djangoproject.com/en/6.1/ref/models/querysets/#bulk-update)다.
[독립 observer](../conformance/runners/django/bulk_update_reference.py)는 직접 작성한 40개 입력으로 필터·반복/없는 key·
실제 count·batch/실패/부모 scope를 관찰하며 Go 코드·출력·expected를 읽지 않는다. 고정 Python/Django/DB와
실행한 전체 upstream module 및 observer 자신의 hash를 [원본 fixture](../codegen/consumertest/testdata/bulkupdate/)에 남긴다.
Django는 BSD-3-Clause이며 Python 내부 객체 구조의 복제나 소스 호환을 목표로 하지 않는다. 이 native 자료만으로
Go 구현·대조·환경별 검증의 완료를 주장하지 않는다.

[잠금 대기 observer](../conformance/runners/django/bulk_update_concurrency_reference.py)는 두 PostgreSQL 연결과
`pg_blocking_pids`의 실제 barrier로 root/join/trimmed relation/NOT EXISTS의 일곱 동시 수정 결과를 독립 관찰한다.
[원본 결과](../codegen/consumertest/testdata/bulkupdate/bulk_update-concurrency-django61-postgres.json)는 변경 없이 보존한다.
실행한 upstream module hash는 결과에, observer hash와 정리 증거는 별도 native receipt에 기록한다.
Typed 입력·빈 IN 실행·borrowed savepoint·지연 FK commit의 명시적 Go 차이와 native 조건 재평가는
[ADR-0089](adr/0089-bulk-update-and-selected-field-ownership.md)가 설명한다.
