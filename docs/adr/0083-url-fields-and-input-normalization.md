# ADR-0083 — URL 필드와 입력 정규화

- 상태: accepted; 제품/소비자 구현과 환경 검증은 [GDJ-0104](../../work/0104-url-fields-and-helpdesk-links.md) 참조
- 날짜: 2026-10-01

## 모델과 저장

URLField는 Schema IR에서 `url`을 보존한다. 기본 최대 길이는 200자이며 nullable·blank·choices·default·unique는
같은 IR이 소유한다. Go 값과 공통 Query AST는 문자열이며 SQL 저장 구조는 같은 길이의 CharField와 같다.
생성 descriptor·typed/dynamic·관계 조건·scan/write·migration history는 별도 kind를 잃지 않는다.
다른 속성이 모두 같은 Char/Email/URL의 의미 변경은 기존 capability·revision·catalog fence 아래의 metadata 변경이다.
고정 Django SQLite가 table remake를 수행하는 것과 SQL 구현은 다르지만 데이터 결과를 보존한다.

일반 ORM 저장은 URL 문법 검증이나 스킴 추가·소문자·IDNA·percent decoding을 실행하지 않는다.
출력도 기존 문자열에 입력 문법을 재강제하지 않는다. 기존 문법상 잘못된 값과 대소문자/공백을 숨기거나 수정하지 않는다.
URL 검증은 호스트 조회나 네트워크 I/O를 수행하지 않으며 fetch·redirect·HTML 링크에 대한 인가를 제공하지 않는다.

## Form과 JSON

Form URLField는 고정 Python Unicode 16 공백을 정리한 뒤 스킴이 없는 입력에 기본 `https`를 붙인다.
`WithAssumeScheme`으로 지원하는 네 스킴을 선택할 수 있고 명시적 스킴은 보존한다. `//example.com`은
`https://example.com`이 된다. 고정 Django 6.1처럼 colon 앞에 ASCII letter로 시작하고 slash가 없는 문자열은
이미 스킴을 가진 것으로 처리한다. 따라서 `example.com:8000`을 임의로 host:port로 복구하지 않는다.
일반 Form은 암묵적 max_length가 없고 모델 투영은 IR 길이를 사용한다. URL 문법 자체의 최대 길이는 2048자다.
Changed는 변환된 제출과 원래 initial을 비교한다. ModelForm은 choices/대체 문자열 field를 거친 후보에도 URL 검증을
적용하며 실패한 input·후보·원래 모델을 구분한다. Nullable blank 모델의 빈 Form 값은 NULL이다.

공통 문법은 `http`, `https`, `ftp`, `ftps`를 허용한다. 고정 Django의 localhost·IPv4/IPv6·IDN·사용자 정보·
1–5자리 port·domain 길이·NFKC authority 검사를 적용한다. Port 99999와 path의 `%zz`도 native가 허용하는
문자열이다. Go `net/url`의 다른 파싱/재작성 정책으로 이 문법을 대체하지 않는다.
Form의 오류 순서는 required/URL/길이/NUL, JSON은 required/null/blank 뒤 길이/URL이다.

JSON URLField는 같은 공백 정책을 적용하되 스킴을 보완하지 않는다. Blank와 NULL은 별도이며 부분 수정의 생략은
현재 값을 보존한다. Serializer의 생략 default는 입력 검증/정규화를 다시 수행하지 않는다.
JSON value 경계는 NUL을 field 이전에 거부하므로 native DRF의 NUL field 진단 순서와 동등하다고 계산하지 않는다.
OpenAPI의 `x-godj-url`과 `x-godj-normalization`이 스킴·시점·최대 길이와 공백 정책을 설명한다.
원문에 `format: uri`나 정리 후 길이를 강제하지 않고 출력에는 URL 문법 제약을 붙이지 않는다.
서버 omission default는 `x-godj-omission-default`로 기록한다. 생성 client는 입력을 고치거나 서버 검증을 대체하지 않는다.

## 소비자와 출처

Helpdesk `external_url`은 nullable/blank 모델 필드다. 새 migration은 기존 행을 NULL로 보존하며,
Admin은 escaped `type=url` 입력을 사용한다. 고정 Django Admin처럼 모델 입력 Form에는 `novalidate`를 두어
브라우저가 스킴 없는 주소 등 서버가 정리할 값을 제출 전에 막지 않게 한다. 필수값·문법·범위는 서버가 검증한다. JSON의 빈 문자열은 빈 문자열, 명시적 null은 NULL로 저장한다.
명시적 인가·Category 범위·transaction·CSRF와 오류/rollback을 같은 기존 저장 경로가 소유한다.
URL을 입력했다는 이유로 기존 문자열을 활성 href로 렌더링하지 않는다.

[독립 observer](../../conformance/runners/django/url_field_reference.py)는 synthetic 입력만 읽고 고정 Django 6.1/
DRF 3.18.0/Python 3.14.3과 실제 SQLite/PostgreSQL을 실행한다. Form·serializer·validator·전체 ModelForm·choices,
Char→URL→Char의 stored result와 query를 각각 기록한다. Go 소스나 기대 결과에서 관측을 만들지 않는다.
[Django license](../../LICENSE.django)와 [DRF 출처](../SOURCES.md)를 적용한다.
공개 API 출처는 [Django Form URLField](https://docs.djangoproject.com/en/6.1/ref/forms/fields/#urlfield),
[Model URLField](https://docs.djangoproject.com/en/6.1/ref/models/fields/#urlfield),
[DRF URLField](https://www.django-rest-framework.org/api-guide/fields/#urlfield)다.

실제 source·환경별 성공/실패와 미실행 범위는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)를 따른다.
