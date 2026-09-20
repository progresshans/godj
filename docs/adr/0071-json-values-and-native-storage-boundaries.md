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
Range·ordering·MIN/MAX는 명시적으로 거부하며 key/path/contains는 별도의 의미와 backend capability 구현을 이어간다.
이 범위는 JSONField의 모든 lookup을 지원한다는 뜻이 아니다.

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
