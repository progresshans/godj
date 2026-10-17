# ADR-0084 — Slug 필드와 일반 필드 인덱스의 소유권

- 상태: accepted; 제품 구현·환경별 검증은 [GDJ-0105](../../work/0105-slug-fields-and-indexed-article-addresses.md) 참조
- 날짜: 2026-10-01

## 모델 의미와 입력

`schema.SlugField`는 IR kind `slug`와 기본 최대 50자, `DBIndex(true)`, `AllowUnicode(false)`를 선언한다.
길이·nullability·blank·choices·default·unique는 같은 IR을 따른다. `AllowUnicode`는 Slug 전용 옵션이며
일반 `DBIndex`와 함께 history·정규화·digest·엄격한 project wire·생성 descriptor에 보존한다.
Go 값과 Query AST의 저장 타입은 문자열이다. 일반 ORM 저장과 출력은 slug 문법을 재검사하거나 값을 고치지 않는다.
자동 주소 생성, lowercase, Unicode 정규화, percent decoding은 이 필드의 동작이 아니다.

ASCII 입력은 영문·숫자·hyphen·underscore만 받는다. Unicode 입력은 고정 Python Unicode 16의
alphanumeric과 hyphen·underscore만 받으며 combining mark는 받지 않는다. 공통 검증은 순수 함수로 I/O를 하지 않는다.
Form은 고정 공백을 trim하고 일반 SlugField에는 암묵적인 길이 한도가 없다. 모델 투영은 IR 길이를 적용하고,
ModelForm은 choices나 대체 CharField의 후보도 모델 slug 정책으로 다시 검증한다. Nullable blank Form의 빈 값은 NULL이다.
JSON은 기본 trim을 적용하되 명시적 blank와 null을 구분한다. 생략 default와 출력은 입력 정리·문법 검사를 반복하지 않는다.
Form의 진단 순서는 문법→길이→NUL, JSON은 전역 값 경계 뒤 길이→문법이다.

고정 DRF 3.18.0의 ASCII SlugField는 trim을 끈 경우 regex `$` 때문에 한 개의 마지막 LF를 수용한다.
GoDj는 Django validator와 Form의 절대 문자열 끝 의미를 JSON에도 적용해 이를 거부한다. 관찰 원본의 native 성공을
남기고 [DEV-0015](../DEVIATIONS.md#dev-0015--slug-json의-절대-문자열-끝)로 따로 검사한다.
전역 JSON NUL 거부와 Python `None`을 문자열로 바꾸는 validator의 입력 domain도 parity와 구분한다.

OpenAPI 입력의 `x-godj-slug`는 Unicode/ASCII 문자 profile과 정리 뒤 검사 시점을 나타낸다.
Trim 전 원문에 정리 후 길이나 패턴을 적용하지 않는다. 출력은 문자열 길이·nullability만 선언하며
잘못된 기존 slug도 그대로 보존한다. 생성 client는 원문을 전송하고 서버가 입력 정책을 집행한다.

## 일반 필드 인덱스와 migration

`DBIndex(true)`는 저장 column의 일반 단일 btree 인덱스를 선언한다. Primary key 또는 Unique가 같은 column의
검색 인덱스를 이미 제공하면 별도 일반 인덱스를 만들지 않는다. Flag 자체는 metadata에 남는다.
모델·필드 이름으로부터 backend가 domain을 분리한 결정적 이름을 만들며 history가 물리 소유권을 결정한다.
SQLite는 BINARY/ASC 단일 key 인덱스, PostgreSQL은 column collation과 해당 타입의 default btree opclass를 쓴다.
Django PostgreSQL의 별도 pattern-opclass 최적화 인덱스나 SQL 이름·문장 수 동일성은 이 구현의 계약이 아니다.

Create/Add/Remove/Delete·reverse와 SQLite table remake는 일반 인덱스를 함께 관리한다. 인덱스 유무 변경과
같은 저장 구조를 가진 Char/Email/URL/Slug 의미 변경은 같은 revision-fenced operation에서 가능하다.
AllowUnicode 변경만으로 SQL은 실행하지 않지만 선언·정의 digest·capability 확인은 유지한다.
Unique 전환은 일반 index 제거→unique 생성, 역방향은 unique 제거→일반 index 생성으로 동일 transaction 안에서 처리한다.
길이·nullability·default 등 다른 저장 변경을 이 capability에 암묵적으로 섞지 않는다.

물리 검사는 이름/owner·key/순서·column·uniqueness·collation·opclass·method·valid/ready/live를 확인하고
partial/expression/included/options 또는 추가·누락 인덱스를 거부한다. 미래 이름의 다른 객체와 namespace 충돌도
revision을 얻기 전에 거부한다. Retained/related/target model까지 같은 요구를 검사하고 인가된 intent에 seal한다.
중간 DDL 실패는 이전 rows/index/history로 rollback하며, held revision·retry·reverse·새 연결 검증을 유지한다.

## Article 소비자

Article의 slug는 nullable/blank/unique Unicode 필드다. 새 `0003_article_slug` migration은 기존 행을 NULL로 보존하며
이전 migration 파일을 바꾸지 않는다. Admin과 Session/Bearer API는 같은 모델 입력 정책을 쓰고,
생략/명시적 null/빈 문자열을 각 Form·JSON 정책대로 처리한다. 사전 unique 검증과 실제 DB constraint는
쓰기 transaction 안에서 소유한다. Rollback이 확인된 직접 rejection만 입력 오류로 렌더링하고 추가 실패·취소는 오류로 유지한다.

게시된 글은 `/articles/by-slug/<slug>/`에서 정확한 대소문자/Unicode 문자열로 조회한다. 문자열 route가 escaping을
한 번 적용하고 template가 값/속성을 escape한다. Slug가 없거나 기존 값이 문법상 잘못된 게시 글은 ID 주소로 연결한다.
Draft는 slug와 ID 상세 route 모두 404이며 기존 목록의 공개/필터 정책은 별도로 유지한다.
이 경로는 slug 입력만으로 권한을 부여하지 않는다. Admin/API의 permission·CSRF·audit/rollback 소유권은 그대로 적용한다.

출처는 [독립 observer](../../conformance/runners/django/slug_field_reference.py),
[SQLite 관찰](../../internal/slugtest/testdata/slug-django61-sqlite.json),
[PostgreSQL 관찰](../../internal/slugtest/testdata/slug-django61-postgres.json), [SOURCES](../SOURCES.md)다.
Native와 Go의 SQL 구현 차이·허용 입력 차이를 관측값에서 지우지 않는다. 실제 검증은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 둔다.
