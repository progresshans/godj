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
JSONField 전체 기능이나 Form/Admin/API·OpenAPI 연결이 완료됐다는 뜻은 아니다.

## Django 관찰과 의도적 차이

[고정 독립 runner](../../conformance/runners/django/json_field_reference.py)와 [raw](../../internal/jsontest/testdata/django61.json)는
Django 6.1/DRF 3.18.0의 public model/Form/API·SQLite 결과다. 별도 native PostgreSQL 관찰은 Django PostgreSQL PASS가 아니다.
GoDj의 객체 key 정규화 때문에 SQLite에서 GoDj로 저장한 reordered object는 동일한 text가 된다. 임의 외부 writer가
다른 공백/key 순서로 저장한 TEXT의 DB equality까지 정규화했다고 가정하지 않는다. Native PostgreSQL은 자체 JSONB 비교를 유지한다.
숫자 token 보존·중복 key/잘못된 surrogate 거부도 [DEV-0017](../DEVIATIONS.md#dev-0017--json-모델의-정확한-token과-엄격한-문서-경계)에 범위를 명시한다.
독립 raw를 수정하거나 모든 JSON 결과를 같은 값으로 뭉쳐 mismatch를 제거하지 않는다.

Form의 빈 값·변경 감지와 API의 입력/응답 nullability는 별도 입력 경계다. Schema IR에서 실제 소비자까지 연결하면서
각 표면의 omission·SQL NULL·JSON null 정책과 OpenAPI/client가 표현하는 범위를 검증한다.
