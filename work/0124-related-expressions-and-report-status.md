# GDJ-0124: 관계 값 표현식과 서비스 보고서 상태 조회

- 상태: completed — source `7afe56f73d615788d7aa7baa5e959a4ec27f6993`의 [전체 CI 38346173948](https://github.com/progresshans/godj/actions/runs/38346173948)와 원본 증거 대조 완료
- 시작: 2026-10-13
- 기준: `a5ef8ff6ea9accaea369c021a0e35eccae6f0804`
- 근거: [ADR-0090](../docs/adr/0090-query-update-and-scalar-expressions.md), [ADR-0091](../docs/adr/0091-grouped-results-and-having.md), [Q-013](../docs/OPEN_QUESTIONS.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md)

## 필요한 기반과 소비자

작업 시작 시 관계 필드 projection은 계산식의 operand가 아니며 계산 조건의 RHS는 literal로 제한되어 있었다.
동일한 Query AST에 finite single-valued 관계 값과 같은 kind의 두 scalar 비교를 연결한다.
Helpdesk의 서비스 보고서 완료 상태와 연결된 티켓의 닫힘 상태를 함께 조회하고 비교하는 흐름에서 사용한다.
두 상태는 독립적인 업무 값이다. 차이를 오류로 단정하거나 자동으로 수정하지 않는다.

## 계약

- 관계 operand는 정규화한 Schema IR에서 결합한 forward FK/OneToOne과 reverse OneToOne의 finite 경로다.
  컬렉션 경로는 값의 다중성 계약 전까지 명시적으로 거부한다. Optional ancestor와 없는 reverse OneToOne은 NULL이다.
- Typed/dynamic 입력은 같은 scalar/Boolean AST를 사용한다. Dynamic 관계 이름은 sealed project binding으로 해석한다.
  원 모델 metadata, 관련 모델 leaf metadata와 selected-result scope를 혼합하거나 추측하지 않는다.
- 같은 kind의 scalar끼리 exact/ordered 비교를 지원한다. 기존 scalar literal 비교와 같은 SQL 3값 논리를 적용한다.
  Boolean은 exact만, JSON은 기존 명시적 미지원 경계를 유지한다. 양쪽 operand와 CASE predicate에 동일한 자원·범위 검증을 적용한다.
- 관계 값의 CASE/산술/projection/filter/order/group/aggregate는 한 statement의 canonical join을 공유한다.
  DB별 physical codec·parameter 순서·전체 SQL 예산을 유지한다. 선택되지 않은 branch도 구조·출처를 검증한다.
- 관련 값은 원 행 UPDATE assignment에 넣을 수 없다. CASE 조건에 숨긴 관계도 거부한다.
  원 query의 관계 filter로 갱신 대상을 선택하는 기존 기능과는 구분한다.
- Relation-filtered root MIN/MAX도 공통 grouping compiler를 사용한다. Related/filtered aggregate의 source slice/distinct 제한과
  단일 COUNT(*)의 기존 중복·slice 의미는 유지한다.
- 새로운 서비스 보고서 상태 조회는 ViewServiceReport와 ViewTicket을 모두 요구하고 현재 category와 읽기 snapshot을 유지한다.
  HTML/API/OpenAPI/독립 client에서 같은 데이터와 필터 의미를 제공하며 기존 보고서 API의 권한·응답을 유지한다.

## 완료 조건과 검증 위치

- [x] AST·공통 compiler·SQLite/PostgreSQL·typed/dynamic runtime·생성 facade를 함께 연결한다.
- [x] 원본/결과 범위, 잘못된 모델·타입·metadata, NULL, reverse OneToOne/collection, 자원 예산, assignment 실패를 검증한다.
- [x] 고정 Django 외부 관찰과 양 DB의 실제 SQL 결과를 비교하고 backend 차이를 구분한다.
- [x] Helpdesk HTML/API·권한 부정 대조·snapshot/취소·독립 client·브라우저 흐름을 연결한다.
- [x] 로컬 포맷·최소 compile 확인 후 push하여 자동 전체 CI에서 영향 테스트·generated drift·DB·race·process·platform을 실행한다.
- [x] 같은 source의 필수 실행과 원본 증거를 대조하고 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 실행 상세를 기록한다.

Named annotation/dependency, collection/reverse aggregate의 더 넓은 의미, Decimal/Duration 산술,
function/subquery/window 및 나머지 전체 목표는 기존 카탈로그 범위로 유지한다.
