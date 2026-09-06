# Django 호환성

GoDj는 Django의 개념과 외부 동작을 Go API에서 구현한다. Python source, 서드파티 Python app, 내부 객체 구조의 호환은 목표가 아니다.
Django 구현을 그대로 번역하지 않고 결과·부작용·오류·transaction 의미를 비교한다.

## 기준과 권위

- Django reference는 `6.1`, source commit `fe0a859f537d4238cf49fca39073513206f83122`다.
- Exact Python/SQLite/platform/timezone/locale과 dependency fingerprint는
  [Django profile](../conformance/profiles/django-6.1-sqlite-darwin-arm64.json)이 소유한다.
- DRF를 비교하는 계약은 별도 [DRF profile](../conformance/profiles/drf-3.18.0-django-6.1-sqlite-darwin-arm64.json)을 사용한다.
- 실행할 계약, provenance와 비교 dimension은 [contracts](../conformance/contracts/)에 있다.
- Go-specific 정책은 ADR/decision provenance로 표시한다. Django 이름의 oracle 디렉터리에 있다는 이유로 Django 동작이라고 하지 않는다.
- 로컬 Django checkout의 moving HEAD를 reference로 사용하지 않는다. Tag/commit을 고정해서 필요한 코드와 테스트만 읽는다.

위 버전은 이 프로젝트의 비교 기준이며 현재 upstream 최신 버전이라는 주장이 아니다. 출처는 [SOURCES](SOURCES.md),
파생 코드의 분류와 고지는 [LICENSING](LICENSING.md)를 따른다.

## 무엇을 비교하는가

우선순위는 조회 결과·정렬·DB 부작용, 오류의 종류와 시점, transaction/durable outcome, 공개 동작, SQL 의미 순이다.
SQL text, Admin DOM, Python 내부 exception class를 항상 byte-identical하게 만들 필요는 없다.
다만 계약이 명시적으로 고정한 wire·canonical encoding·보안 경계는 그 계약대로 검증한다.

Normalization은 timezone/representation 같은 허용 차이를 제거할 뿐 순서·NULL·오류·data loss를 숨기면 안 된다.
Intentional difference는 [DEVIATIONS](DEVIATIONS.md)의 계약과 selector로 제한한다. 넓은 wildcard waiver로 실제 회귀를 통과시키지 않는다.

`draft → oracle_locked → red → passing`은 계약 실행 상태다. `deviation`은 검토한 차이가 있는 상태이며 전부 같다는 뜻이 아니다.
Oracle만 있거나 static not-implemented fixture가 유효한 것은 제품 구현이 아니다. Reference-only MIG-075..086을 product passing에 포함하지 않는다.
현재 등록 범위는 [구현 현황](status/IMPLEMENTATION_MATRIX.md), 실제 실행한 source/환경은 [Evidence](status/TEST_EVIDENCE.md)에 있다.

## 현재 개발 단계의 내부 변경

아직 외부 지원 릴리스의 내부 ABI를 보존할 의무는 없다. 필요하면 Schema IR, generated ABI, private runner protocol과 public
Go API를 함께 변경한다. 이전 개발 snapshot을 읽는 호환 계층을 새 기능마다 추가하지 않는다.

Version/strict decoding 자체를 없애는 것은 아니다. 지원하는 current 형식을 명시하고 unknown·mixed·stale 입력은 거부한다.
Persisted data, historical state, deterministic publication과 unknown transaction outcome의 안전성은 내부 형식 변경과 별개로 유지한다.
이 선택의 이유는 [ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)에 있다.

첫 externally supported release를 실제로 정할 때 버전 지원 기간·migration/upgrader 정책을 결정한다. Alpha/1.0 범위를 먼저
고정하거나 특정 릴리스 이름을 사용해야 지금 기능 개발을 계속할 수 있는 것은 아니다.

## 설계와 검증을 구분하기

Accepted ADR은 선택한 설계 이유다. 구현된 코드와 특정 플랫폼의 PASS는 Matrix/Evidence에서 별도로 표시한다.
작업을 완료했다고 Django 전체 구현률이 올라간 것으로 계산하지 않는다. 좁은 Article flow의 성공은 다른 model이나 multi-user,
다른 DB, 일반 Admin/Form/API까지 검증했다는 뜻이 아니다.

Django와 같은 동작이 중요할 때 differential scenario를 추가하고, Go의 타입·취소·resource lifecycle 문제는 Go-native test로
검증한다. Go compiler availability 수정이나 CI summary 변경마다 새 Django oracle을 만들지 않는다.
