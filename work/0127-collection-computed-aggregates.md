# GDJ-0127: 컬렉션 계산식 집계와 라벨별 조정 비용

- 상태: 완료 — source `012191bf`의 양 DB·세 mode·전체 통합 검증과 원본 증거 대조 완료
- 시작: 2026-10-15
- 기준: `b66a5aade96a2bb1124c0dbdc0f9edcf122fe70b` (GDJ-0126의 전체 CI·원본 증거 대조 완료)
- 근거: [개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [ADR-0090](../docs/adr/0090-query-update-and-scalar-expressions.md), [ADR-0091](../docs/adr/0091-grouped-results-and-having.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)

## 필요한 기반과 소비자

라벨별 연결 티켓의 예상 비용에 배율·가감액을 적용한 합계를 조회한다. 각 티켓의 값을 계산한 뒤 집계해야
NULL·CASE·나눗셈·반올림과 FILTER가 보존된다. 집계나 페이지를 읽은 뒤 Go에서 보정하는 것으로 대신하지 않는다.

## 계약

- 기존 불변 scalar AST에 finite collection 경로를 보존한다. 계산식의 구성과 사용 문맥의 cardinality 검증을
  나누며, 컬렉션 계산은 완료된 COUNT/MIN/MAX/SUM/AVG 안에서만 실행한다. 일반 projection·그룹 키·행 정렬·
  WHERE의 computed comparison·UPDATE와의 경계는 유지한다. 중첩 집계에는 명시적 결과 stage가 필요하다.
- 기존 kind·Decimal precision·입력 검증·Optional·오류·깊이/노드/SQL/parameter 예산을 유지한다. 숫자 kind 간
  암묵 변환은 없다. Root·관련 leaf·selected result의 source authority는 사용 문맥과 별도로 검사한다.
- 같은 collection의 값과 CASE/FILTER 조건은 같은 joined row를 참조한다. 집계용 JOIN 해석은 원본 Filter가
  materialize한 최신 occurrence를 재사용하며 원본 불변 표현식을 수정하지 않는다. 독립 collection의 행 곱,
  중복 through 행, 값 기준 DISTINCT, 원본 NOT/EXISTS와 집계 FILTER의 행 조건 의미를 유지한다.
- 빈 관계와 NULL을 보존한다. CASE fallback이 LEFT JOIN의 빈 행을 처리하는 의미를 별도로 관찰한다.
  FILTER에서 제외된 행과 실행되지 않은 CASE branch를 먼저 계산하지 않는다. 기여하는 값의 손상·범위 초과는
  후속 연산·HAVING·decoder로 숨기지 않는다.
- Typed 숫자/Decimal capability와 dynamic expression 집계는 같은 AST·compiler·decoder를 사용한다.
  Dynamic 이름의 권한은 명시적인 sealed BoundModel이 소유한다. 행용 BindExpression의 범위를 넓히지 않는다.
- Helpdesk의 라벨별 조정 비용은 현재 Category·ViewLabel/ViewTicket·한 읽기 snapshot을 사용한다.
  집계마다 현재 Category FILTER를 적용하고 물리적인 다른 Category 링크의 값·존재·오류를 노출하지 않는다.
  입력과 결과는 정확한 Decimal 문자열이며 기존 저장값·관계·보고서·audit를 보존한다. HTML/API·OpenAPI·
  독립 client·브라우저에서 입력·페이지·실패 경계를 연결한다.

## 구현과 검증

공통 AST·양 compiler·typed/dynamic 집계와 생성 facade를 연결하고 실제 프로젝트 생성물을 갱신했다.
고정 Django 6.1의 독립 SQLite/PostgreSQL 관측 49개에서 행별 계산 집계·NULL·CASE·DISTINCT·FILTER·
JOIN 재사용을 대조했다. Exact SQLite Decimal의 차이는 기존 deviation과 실제 관측에 연결한다.
두 앱 생성 소비자의 양 DB child 130개와 compile-negative 8개, 취소/cache/session·물리 값 손상·저장 보존을
normal·race·CGO0에서 확인했다. Helpdesk 라벨별 조정 비용의 HTML/API·독립 client·브라우저도 함께 검증했다.

조건부 COUNT 소비자를 명시적인 연결 티켓 ID 집계로 수정했다. Runserver 준비 예산·안전한 단계 진단과
macOS ARM normal/CGO0의 runtime·세 consumer shard를 보완하면서 전체 발견 root·필수 child·no-skip 조건을 유지했다.
실패 재현과 이전 source의 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 소유한다.

GDJ-0128을 포함한 source `012191bfaa35faaad673c8a6d464cbd067b0b5a6`의
[전체 CI 38851526864](https://github.com/progresshans/godj/actions/runs/38851526864)와 원본 증거 감사를 완료했다.
Runner 중단 뒤의 재실행과 최초 성공의 실제 실행 계보를 구분하고, 전체 플랫폼·native DB·필수 실행·capture 소비와
최종 집계를 대조했다. 로컬 전체 제품 테스트를 원격 전체와 중복 실행하지 않았다.

## 완료 조건

- [x] 공통 AST의 집계 cardinality·source authority와 양 compiler의 JOIN/CASE/FILTER를 연결한다.
- [x] Typed/dynamic·생성 facade와 두 앱의 native·부정 대조·실패/보존 검증을 연결한다.
- [x] 독립 Django 관측과 backend 차이를 실제 결과로 기록한다.
- [x] 라벨별 조정 비용의 HTML/API·권한·snapshot·client·브라우저를 연결한다.
- [x] 같은 source의 필수 전체 실행과 원본 증거를 대조하고 현행 문서에 반영한다.

기본 DateTime/Duration 산술은 [GDJ-0128](0128-temporal-expressions-and-deadline-preview.md)에서 확장했다.
Date/Time 이동·Duration scaling/division·일반 named annotation·function/subquery/window와 나머지 카탈로그는 계속 전체 목표로 유지한다.
