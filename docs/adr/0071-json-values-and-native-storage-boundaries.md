# ADR-0071: JSON 값의 소유권·정밀도와 native 저장 경계

- 상태: Accepted
- 날짜: 2026-09-20
- 관련 작업: [GDJ-0094](../../work/0094-json-models.md)

설계 채택과 제품 구현·환경별 검증을 구분한다. 현재 실행 결과는 TEST_EVIDENCE가 소유한다.

## 모델과 이력의 값

`jsonvalue.Value`는 변경 가능한 container를 공유하지 않는 JSON 문서 값이다. 공개 `Text` literal도 입력 경계에서 검증한다.
Zero value는 invalid이며 `jsonvalue.Null()`은 존재하는 JSON null이다. Nullable 모델의 nil `*Value`만 SQL NULL이다.
`Decode`는 호출자 소유의 map/array와 `json.Number`를 새로 만들고 `Bytes`는 별도 복사본을 반환한다.
JSON을 담은 문자열을 두 번 decode하거나, Go map/string/float를 typed JSON 값으로 암묵 변환하지 않는다.

공통 bounded parser가 한 문서·정상 Unicode·중복 key 거부와 깊이/크기/개수 한도를 검사한다.
객체 key와 공백·문자열 escaping은 결정적으로 정리하되 숫자는 원래 자릿수와 fraction/exponent token을 보존한다.
float64로 바꿔 overflow·underflow·반올림을 숨기지 않는다. 기본 한도는 문서 1 MiB, 깊이 64, 값 65536개,
object/array 각각 16384개, 숫자 token 4096 byte다. Escaping 이후 canonical 크기도 할당 전에 검사한다.

Schema IR의 JSON kind와 문자열 default arm이 정규화 원본이다. 다른 scalar payload·비정규 default를 거부하고
clone/hash·historical digest·resource budget·project wire에 같은 값을 포함한다. Literal default는 생성 write가 적용한다.
JSON null default와 default 없음·SQL NULL을 합치지 않는다. 임의 callable default나 자동 DB backfill을 뜻하지 않는다.

## 저장과 조회

SQLite는 TEXT와 `JSON_VALID(column) OR column IS NULL` CHECK를 사용한다. 기존 catalog의 CHECK 변경/제거도
revision-fenced migration preflight에서 drift다. Model scanner는 TEXT나 native adapter의 typed JSON 값만 받고
BLOB·중복 key·잘못된 Unicode는 읽기 오류로 처리하며 이전 값을 남기지 않는다. DB CHECK만으로 모든 codec 한도가
보장된다고 가정하지 않는다. Foreign writer의 COUNT처럼 값을 decode하지 않는 동작까지 검사하는 계약은 아니다.

PostgreSQL은 native JSONB와 `json.RawMessage` parameter를 사용한다. Go `Value` struct를 JSON 객체로 encode하지 않는다.
Native text/bytes는 backend가 typed JSON으로 변환한 뒤 root·projection·joined ORM scanner에 전달한다.
SQL NULL과 JSON null은 이 단계에서도 다르다. JSONB는 native numeric equality와 표현을 유지한다.
따라서 `1`, `1.0`, `1e0`은 같은 값으로 비교될 수 있고, exponent는 펼쳐지며 negative zero의 부호는 사라질 수 있다.

Native JSONB의 U+0000 제한은 PostgreSQL 경계에서 거부한다. 숫자의 exponent를 펼친 길이와 문서 전체의 native
separator 공간을 계산해 성공한 write가 shared scanner의 크기 한도를 넘어가지 않도록 한다. 계산 중 부동소수점 변환이나
exponent 크기의 무제한 할당을 하지 않는다. Go escaping을 포함한 상한을 사용하므로 PostgreSQL 자체가 받는 모든 값이
GoDj의 round-trip profile에 포함되지는 않는다. SQLite에 이 native 한도를 적용하지 않는다.

Common AST는 JSON exact/IN, 같은 모델의 JSON F exact, SQL isnull과 projection을 소유한다. Forward JSON 조회와
non-null reverse exact는 기존 relation binding·cache/clone 경계를 따른다. JSON 값을 ordered scalar로 일괄 취급하지 않는다.
Range·ordering·MIN/MAX는 명시적으로 거부하며 추가 연산은 아래 의미와 backend capability에 따라 제공한다.
이 범위는 JSONField의 모든 lookup을 지원한다는 뜻이 아니다.

## JSON 경로 predicate

`query.JSONKey`와 `query.JSONIndex`는 객체 key와 배열 index를 구분한다. `JSONKey("0")`을 index로 바꾸지 않으며
빈 key·점·따옴표·역슬래시·Unicode·lookup처럼 보이는 이름도 문자열 그대로다. 경로는 1..64 segment,
key의 UTF-8 합계 4096 byte와 index 0..2147483647로 제한한다. 음수 index·wildcard·query-language 식은 현재 경로 문법이 아니다.
`query.JSONPath`와 condition은 소유한 immutable snapshot이며 반환된 segment slice는 복사본이다.

Typed `Payload.At(query.JSONKey("items"), query.JSONIndex(0))`와 dynamic `LookupInput.JSONPath`는 같은 AST를 사용한다.
Dynamic `Key`는 기존 model/relation field·lookup만 선택하며 arbitrary JSON key를 `__` 문법에 끼워 넣지 않는다.
오타 lookup을 JSON key로 암묵 수용하지 않고 기존 allowlist policy를 적용한다. Nil JSONPath는 whole field,
non-nil empty JSONPath는 오류다. 경로에는 exact/IN/isnull·key presence와 backend가 지원하는 contains/contained_by를 제공하고
Root path의 typed projection은 아래 결과 경계를 따른다. F·ordering·write field로 노출하지 않는다.

없는 key, 범위 밖 index, 맞지 않는 container는 SQL NULL이다. `IsNull(true)`가 이를 조회하고,
`Exact(jsonvalue.Null())`는 실제 존재하는 JSON null만 조회한다. 부정 조건의 기존 root SQL NULL 보정과 missing path는
구분한다. 예를 들어 nullable field의 NOT exact는 root SQL NULL을 포함하지만, 존재하는 문서에서 missing인 path를
자동으로 포함하지 않는다. Path IN의 SQL NULL member는 거부하며 명시적인 JSON null과 empty IN의 기존 Boolean 의미는 유지한다.
Forward의 optional JOIN·Boolean·eager·Count와 direct reverse exact는 기존 relation provenance와 지원 경계를 따른다.

PostgreSQL은 매개변수로 전달한 literal path를 `jsonb_path_query_first`의 strict mode로 읽는다. Missing/type mismatch는
NULL이며 native JSONB equality를 유지한다. Native `-> integer`가 scalar를 1-element array처럼 읽는 경우를 피하고,
NUL key는 query 실행 전 거부한다. Empty IN으로 I/O를 생략할 때도 이 backend 검사를 먼저 한다.
SQL/JSON 경로와 strict mode의 문법은 [PostgreSQL 17의 공식 문서](https://www.postgresql.org/docs/17/functions-json.html)를 참조한다.

SQLite는 native `json_extract`의 숫자 변환과 native `->`의 NUL-key prefix 충돌을 사용하지 않는다.
실제 여러 행에서 빈 key lookup이 NUL key 행까지 선택했고 같은 object의 prefix/NUL key도 구분해야 했다.
Backend의 deterministic `godj_json_at` 함수가 shared bounded JSON codec으로 key/type을 검사하고 exact number token을 보존한다.
SQL 안에서 predicate를 평가하며 ORM에서 모든 model을 가져와 filtering하지 않는다. SQLite 경로의 반환 subtree는 canonical JSON이다.
따라서 foreign writer의 subtree 공백·key 순서도 정리되며 duplicate key·잘못된 Unicode·한도 초과 문서는 명시적인 오류다.
함수는 package 초기화 때 driver에 한 번 등록되고 모든 새 physical connection에서 사용할 수 있다. 실패는 Open의 error다.
전역 문서/query cache는 없으며 호출마다 소유한 bounded tree를 사용한다. 각 경로 조건의 parsing 비용은 SQLite backend의 비용이다.

[독립 lookup runner](../../conformance/runners/django/json_lookup_reference.py)는 고정 Django 6.1의 public ORM을 실행한다.
[SQLite raw](../../internal/jsontest/testdata/django61-lookups-sqlite.json)와
[PostgreSQL raw](../../internal/jsontest/testdata/django61-lookups-postgres.json)는 각각 88개 filter/exclude와 3개 projection 관찰이다.
SQLite reference는 `PYTHONHASHSEED=0`으로 Django 내부 set의 SQL 출력 순서만 고정하며 DB 결과는 정규화하지 않는다.
GoDj의 타입·숫자 보존과 명시적 segment 문법 차이는 DEV-0017에 기록한다. Reference의 관찰과 제품 구현·검증은 별개다.

## JSON containment의 backend 경계

`Contains`/`ContainedBy`는 부분 문자열 검색과 구분되는 JSON 문서 포함 관계다. 같은 `LookupContains`/`LookupContainedBy`
AST를 whole field와 명시적인 path, root와 forward 관계, typed와 dynamic 경계에서 사용한다. RHS는 검증된 `jsonvalue.Value`이며
SQL NULL·Go map/string·F 참조를 암묵 변환하지 않는다. Dynamic lookup policy와 query snapshot 소유권도 그대로 적용한다.

PostgreSQL은 native JSONB `@>`/`<@`를 사용한다. 중첩 객체·배열과 scalar의 포함 관계, 배열 순서/중복과 숫자 1/1.0의
의미는 [PostgreSQL 17 containment](https://www.postgresql.org/docs/17/datatype-json.html#JSON-CONTAINMENT)를 따른다.
JSON null literal과 SQL NULL은 다르다. 부정 조건은 기존 containing column의 NULL을 보정하며 missing path를 자동 포함하지 않는다.
Optional forward target의 non-null field도 JOIN 뒤에는 NULL일 수 있다. Positive containment의 INNER JOIN 승격과
OR/NOT의 LEFT JOIN·NULL 보정은 같은 expression tree로 판단한다. Direct reverse는 기존 exact-only 지원 범위를 유지한다.

SQLite는 고정 Django와 같이 contains/contained_by를 지원하지 않으며 compiler가 `backend_error/unsupported_feature`를 반환한다.
Client-side filtering이나 string LIKE로 바꾸지 않는다. Count·LIMIT 0·empty IN으로 실행을 생략할 때도 전체 계획의 capability를
검사한다. PostgreSQL도 native NUL/number expansion parameter 한도를 I/O 생략 전에 검사한다.

독립 runner의 `containment`는 별도 table에서 33개 문서에 root/key 경로·양 연산·filter/exclude **168개 조건**을 관찰한다.
기존 88개 lookup·3개 projection은 그대로 보존한다. Generated 소비자는 PostgreSQL raw의 실제 결과를 직접 비교하고
SQLite의 같은 표현은 오류 category/code와 I/O 0을 검사한다. 이 테스트는 JSONField의 모든 transform을 뜻하지 않는다.

## JSON key presence

`HasKey(string)`, `HasKeys(...string)`(모두), `HasAnyKeys(...string)`(하나 이상)은 root·명시적 JSON path·forward에서
같은 AST를 사용한다. RHS는 JSON 문서나 IN 값 목록과 구분되는 닫힌 `JSONKeyList`다. 키 수 1024개·UTF-8 합계
4096 byte를 제한하고 원본/반환 slice를 복사한다. 순서·중복은 AST에 보존하며 SQL의 membership 의미와 구분한다.
Zero list는 invalid, constructor의 빈 목록은 valid다. Dynamic은 has_key에 string, 나머지에 []string/모든 원소가 string인
[]any만 받는다. Nil interface·숫자·JSON 문서를 문자열로 변환하지 않으며 기존 lookup policy를 적용한다.
Typed nil slice는 빈 목록이며 nil interface와 구분한다.

PostgreSQL은 매개변수로 전달한 string/text[]와 native `?`/`?&`/`?|`를 사용한다. 객체 key뿐 아니라 scalar string과
문자열 배열의 원소도 검사한다. SQL NULL/missing path는 unknown이며 부정 조건은 root SQL NULL·optional JOIN만 보정한다.
NUL이 포함된 key는 Count·LIMIT 0·empty IN을 포함한 전체 계획 preflight에서 거부한다.

SQLite의 nonempty key 목록은 객체의 literal key만 검사한다. JSON null 값인 key도 존재하며 missing path는 false라서
NOT에서는 포함된다. Native path의 empty/NUL prefix 충돌을 피하기 위해 deterministic `godj_json_has_keys`가 bounded codec으로
평가한다. 등록·오류·연결·cache 소유권과 외부 malformed/duplicate/Unicode 문서의 거부는 `godj_json_at`과 같다.
Path와 presence를 함께 쓰면 path 추출과 subtree 검사를 각각 수행하며 전역 문서 cache를 만들지 않는다.

빈 목록은 PostgreSQL의 의미를 채택한다. 존재하는 JSON 값은 종류와 무관하게 HasKeys()=true, HasAnyKeys()=false이고,
SQL NULL/missing path는 unknown이다. 이는 SQLite nonempty lookup의 Boolean missing과 구분하며 빈 IN처럼 상수로 접지 않는다.
고정 Django SQLite의 빈 목록은 OperationalError이므로 이 확장은 DEV-0017에 명시한다. Root SQL NULL과 optional JOIN의
부정 보정은 계속 적용한다. Reverse는 기존 exact-only 경계를 유지한다.

[독립 helper](../../conformance/runners/django/json_key_presence_reference.py)는 SQLite 19개/PostgreSQL 17개 문서에서 각각
96개 root/path·single/all/any·filter/exclude를 관찰한다. 이전 path·containment raw는 보존한다. Generated 소비자는 DB별 raw를
직접 비교하고 SQLite empty-list 8조건, root empty/NUL key 12조건만 명시한 차이로 검사한다. PostgreSQL NUL 12조건은
reference의 DataError와 GoDj의 preflight invalid-value를 구분한다. 환경별 실행 완료는 TEST_EVIDENCE가 소유한다.

## JSON 경로 projection

`orm.Project1..4`와 `SelectInto`는 root JSON 경로를 선택해 DTO로 반환한다. 예를 들어
`orm.Project2(RecordFields.Label, RecordFields.Payload.At(query.JSONKey("a")), build)`에서 build의 두 번째 인자는
`*jsonvalue.Value`다. 원본 필드가 required여도 missing/type mismatch/root SQL NULL은 nil, 존재하는 JSON null은
non-nil `jsonvalue.Null()`로 구분한다. Whole field와 여러 서로 다른 path를 한 행에 함께 선택할 수 있다.
각 행/셀의 nullable pointer는 독립적이며 source model cache를 읽거나 교체하지 않고 실제 SELECT를 실행한다.

공통 `ResultExpression`에 JSON path를 담되 원본 `FieldRef`의 nullability·identity를 바꾸지 않는다.
`NewProjectionResult`는 FieldResult 또는 JSONPathResult의 표현 목록을 받는다. 기존 field-only 저수준 호출도 이 표현으로
옮기며 별도 호환 계층을 추가하지 않는다. 선택 목록은 1..2048개로 제한하고 같은 source/path의 중복을 거부한다.
Path 비교는 포인터 identity가 아니라 literal key/index 순서로 판단한다. 동일한 source의 다른 path와 whole-field 선택은 다르다.
Typed projection의 model·값 타입은 sealed capability를 유지하며 path에 ordered/write/F capability를 추가하지 않는다.

SQLite는 기존 bounded `godj_json_at`, PostgreSQL은 strict JSON path를 SELECT에서 평가한다. 선택 path 매개변수는
WHERE·LIMIT/OFFSET보다 앞에 배치한다. Native NUL path는 LIMIT 0·empty source에서도 preflight 오류다.
Projection DISTINCT는 선택한 값의 DB 의미를 따른다. SQLite canonical JSON text의 `1`/`1.0`은 다르며 native JSONB는
동등하게 취급한다. ORDER BY field는 DISTINCT에서 해당 whole field도 선택되어 있어야 한다. JSON ordering 자체는 계속 미지원이다.
Partial scan/rows/context 실패 시 결과 일부를 반환하지 않고 cursor를 닫는다. 새 실행으로 재시도할 수 있으며 잘못된 값의
오류를 숨기거나 모델 cache에서 대체하지 않는다. 관계 filter가 있는 source에서도 root scalar와 JSON 경로를 DTO로
선택할 수 있다. JOIN의 중복·nullable Boolean 의미를 유지하고 DISTINCT는 실제 선택한 값에 적용한다.
Forward 대상의 JSON 문서 전체와 경로도 같은 nullable DTO 값으로 선택한다. 일반 forward scalar-column도 지원하며 related-object hydration의 DTO 결합은 별도 범위다. [ADR-0039](0039-typed-projection-scalar-aggregate-and-stable-pagination.md)를 따른다.

[독립 projection runner](../../conformance/runners/django/json_projection_reference.py)는 SQLite 32개/PostgreSQL 30개 문서에서
각각 8개 경로를 관찰한다. Public ORM의 값·missing·root SQL NULL을 별도 기록하여 Python None만으로 JSON null을 판정하지 않는다.
SQLite의 문자열 재해석·큰 정수 반올림·empty/NUL 오조회와 numeric key/index 및 native scalar-index의 차이는 DEV-0017에
정확한 selector로 기록한다. 이 raw 및 타입 비교는 일반 relation projection이나 JSON transform 전체 지원을 뜻하지 않는다.

## Form/Admin과 JSON API

Form은 JSON 문서를 Textarea로 편집하며 실패한 입력 원문을 유지한다. Optional의 빈 제출/null은 SQL NULL이며,
빈 object/array/string은 그대로 저장한다. Required는 null·빈 object/array/string을 거부한다. Bool false와 숫자 0은 빈 값이 아니다.
초기값과 변경 감지는 object 순서와 floating token의 동등한 표기(1.0/1e0/1.00)를 무시하되 integer/float,
bool/number와 floating signed zero를 구분한다. 정확한 coefficient/exponent 비교로 Python float의 반올림·underflow를 재현하지 않는다.
Form은 SQL NULL과 stored JSON null을 같은 빈 의미로 비교하지만 모델 tag를 바꾸지는 않는다. 저장 adapter는 현재 행을 같은
transaction에서 비교해 JSON null과 원래 숫자 표기를 보존한다. Form의 Changed만으로 실제 UPDATE 생략이 보장된다고 가정하지 않는다.

Generic Form의 최상위 JSON 문자열 NUL은 `null_characters_not_allowed`다. Model 문서와 generic Form의 nested NUL 수용은
API 수용과 별개다. Serializer/parser는 기존의 NUL 거부·정상 Unicode·중복 key 거부·전체 request 예산을 유지한다.
`Spec.DecodeObject`와 `Parser.ParseObjectFor`만 선언된 JSONField 내부의 빈 문자열 key를 허용한다.
일반 `DecodeObject`/`NewObject`와 API 최상위 envelope의 이름 규칙은 그대로다. Opaque JSON 값으로 공개한 뒤에도 응답 전체의
깊이·값 수·byte 예산을 다시 적용한다. Typed String에 든 JSON 문법을 두 번 decode하지 않는다.

명시적인 JSON null 입력은 nullable field를 비우며 non-null field에서 거부한다. 이는 Go caller가 opaque JSON null을
직접 넣어도 같다. Full omission은 IR default를 적용하고 PATCH omission은 아무 값도 넣지 않는다.
IR의 JSON null default는 SQL NULL default가 아니므로 non-null 모델에도 존재할 수 있다. 응답에는 양 null 모두 JSON null이다.
OpenAPI 입력은 any JSON과 non-null인 경우 `not: {type: null}`, 완전한 응답은 nullable과 무관하게 required any JSON이다.
Non-null 입력이 수용하지 않는 JSON null omission default는 `x-godj-default`로 구분한다. `x-godj-json`은 런타임 정책이며
표준 schema가 duplicate/정밀도/NUL/예산을 대신 검사하지 않는다. 고정 ogen은 raw JSON bytes를 사용하며 runtime 검증도 유지한다.

Helpdesk `external_payload`는 이 공통 경계를 실제 사용하는 nullable JSON 모델이다. Form/API는 이 앱의 4096-byte·깊이 14
payload 예산과 API 문자 정책을 공유한다. Detail/list wrapper를 포함해 응답할 수 있도록 한 입력 제한이다.
Create/update는 실제 저장 결과를 transaction 안에서 다시 읽고 응답 encode를 확인한다. Native JSONB의 지수 전개가 API의
숫자 token/응답 예산을 넘으면 생성·수정과 함께 rollback한다. 모델 scanner 한도와 API 응답 한도는 같다고 가정하지 않는다.
개별 응답과 여러 행의 목록도 예산을 구분한다. `api.JSONWithLimits`는 한 응답 전체에 caller가 지정한 bounded limits를 적용한다.
Helpdesk의 최대 20행 목록은 65536개 값의 예산을 사용하며 기존 1 MiB·깊이 16·container 한도는 유지한다.
각각 허용한 JSON 배열 네 개를 목록으로 묶을 때 기본 4096개 값 한도를 넘던 경우를 별도 회귀로 검증한다.

## Django 관찰과 의도적 차이

[고정 독립 runner](../../conformance/runners/django/json_field_reference.py)와 [raw](../../internal/jsontest/testdata/django61.json)는
Django 6.1/DRF 3.18.0의 public model/Form/API·SQLite 결과다. 별도 native PostgreSQL 관찰은 Django PostgreSQL PASS가 아니다.
GoDj의 객체 key 정규화 때문에 SQLite에서 GoDj로 저장한 reordered object는 동일한 text가 된다. 임의 외부 writer가
다른 공백/key 순서로 저장한 TEXT의 DB equality까지 정규화했다고 가정하지 않는다. Native PostgreSQL은 자체 JSONB 비교를 유지한다.
숫자 token 보존·중복 key/잘못된 surrogate 거부도 [DEV-0017](../DEVIATIONS.md#dev-0017--json-모델의-정확한-token과-엄격한-문서-경계)에 범위를 명시한다.
독립 raw를 수정하거나 모든 JSON 결과를 같은 값으로 뭉쳐 mismatch를 제거하지 않는다.

각 표면의 omission·SQL NULL·JSON null 정책과 OpenAPI/client 범위는 서로 구분해 검증한다.

### Forward 대상의 JSON 경로 선택

`RelatedJSONField.At(...)`도 `Project1..4`의 JSON 값 선택에 사용할 수 있다. Generated direct selector와
`ChainForward`의 유한한 forward 경로를 함께 지원한다. Root·target field의 실제 nullability는 변경하지 않으며
결과는 항상 `*jsonvalue.Value`다. 대상 부재·원본 SQL NULL·없는 JSON 경로는 nil, 존재하는 JSON null은 non-nil 값이다.

선택 표현이 exact terminal field·JSON path와 별도의 immutable RelationPath를 보존한다. 서로 다른 관계를 거쳐
같은 target column/key를 선택해도 다른 표현이며 root 선택과 충돌하지 않는다. Plan은 root FK metadata를,
공통 JOIN 준비는 filter·selected route의 논리 root·FK/table 선언 충돌과 alias를 검사한다. Nullable ancestry와
전체 Boolean predicate에 따른 JOIN 선택을 재사용하고 별도의 related-object hydration은 만들지 않는다.
조회가 비어도 source/binding·중복 표현·native NUL·backend JOIN 한도를 먼저 검사한다.

[독립 public runner](../../conformance/runners/django/forward_json_projection_reference.py)는 필수/선택 parent와
child의 네 경로에서 filter 없음·AND/OR/NOT·선택값 DISTINCT·slice를 관찰한다. 대상 부재·SQL NULL·missing을
별도 public 조회로 기록하며 Go의 JSON null 표현과 구분한다. Runtime 검증 여부는 TEST_EVIDENCE가 소유한다.
Reverse selected path·JSON F/order·일반 관계 aggregate는 이 확장에 포함하지 않는다.

Whole `RelatedJSONField`도 같은 `Project1..4`에 전달할 수 있다. Forward scalar와 공유하는 결과 경계는
[ADR-0039](0039-typed-projection-scalar-aggregate-and-stable-pagination.md#forward-scalar-결과)를 따른다.
대상 부재와 SQL NULL은 nil, 저장된 JSON null은 non-nil이며 native adapter는 root에 JSON field가 없어도
selected target field의 kind를 확인한다. Whole document와 그 안의 path를 함께 선택해도 서로 다른 표현이다.
