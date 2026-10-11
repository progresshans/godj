# ADR-0074: CASCADE 삭제 그래프와 FK 검사 시점

- 상태: Accepted design; 제품 구현·환경별 검증은 [GDJ-0098](../../work/0098-cascade-and-ticket-label-links.md)과 [구현 현황](../status/IMPLEMENTATION_MATRIX.md)을 따른다.
- 날짜: 2026-09-22
- 관련 결정: [관계 삭제](0030-project-bound-protect-and-set-null-delete.md), [제약 소유권](0072-column-uniqueness-and-constraint-ownership.md), [일대일 관계](0073-one-to-one-cardinality-and-reverse-objects.md)

## 이유

Ticket과 Label의 연결 행은 endpoint가 삭제될 때 함께 삭제되어야 한다. 삭제는 다른 endpoint와 관계없는 연결을 보존하며,
ServiceReport 등의 보호 규칙도 적용해야 한다. CASCADE 후손이 다시 다른 관계의 대상일 수 있으므로 현재의 한 단계 incoming
PROTECT/SET_NULL만으로 이 동작을 표현할 수 없다. 숨긴 역관계와 OneToOne, 중복 경로·자기 참조·required 순환도 같은 그래프에 속한다.

고정 Django 6.1의 독립 양 DB 관찰은 PROTECT가 CASCADE로 수집된 객체에도 적용되고, 늦은 삭제 실패가 앞선 삭제와
SET_NULL을 함께 rollback함을 보여준다. 자동 ManyToMany intermediary는 두 CASCADE FK와 ordered pair 고유성을 가진다.
선택한 현재 Go API와 물리 DB 표현의 책임을 분리하면서 이 결과를 구현한다.

## 결정

Schema IR의 `DeleteCascade`는 ORM이 source 행도 삭제해야 한다는 관계 의미다. 생성기와 runtime은 같은 immutable project
universe에서 CASCADE로 도달할 모델과 그 모든 incoming 정책을 사용한다. Generated fingerprint는 root의 직접 관계뿐 아니라
도달한 descendant의 정책·table·key·column·cardinality·nullability도 소유한다. Stale/부분 project binding은 I/O 전에 거부한다.

공통 ORM은 반복형 탐색과 `(model identity, primary key)` 방문 집합으로 실제 삭제 행을 수집한다. 중복 경로와 순환은 같은 행을
여러 번 삭제하지 않는다. 수집 중 반환된 key·FK는 요청한 관계와 일치해야 하고, 모든 row/error/close 결과를 확인한다.
조회 오류·취소·잘못된 행이 있으면 수집한 일부 결과로 삭제하거나 부분 보호 진단을 반환하지 않는다.

모든 도달 행의 PROTECT 검사가 끝난 뒤 SET_NULL과 각 행의 정확한 key 삭제를 실행한다. 이미 CASCADE로 도달한 보호 행도
PROTECT 검사 대상이다. Metadata 해석·동적 수집·보호 검사·변경은 동일한 coordinated transaction의 범위와 권한을 유지한다.
삭제 수는 root와 실제 삭제한 후손의 합계다. 기존 exact-key delete의 예상 row 수 검사와 단일 동기 callback 계약을 유지한다.

SQL FK는 `ON DELETE NO ACTION`을 사용한다. CASCADE 선언의 FK는 양 DB에서 `DEFERRABLE INITIALLY DEFERRED`로 생성하여
required 순환의 생성·삭제를 transaction 끝에서 검사할 수 있게 한다. PROTECT/SET_NULL의 현재 즉시 검사 표현은 유지한다.
CASCADE로 바꾸거나 되돌리는 AlterField는 물리 검사 시점도 변경하는 migration이다. SQLite remake와 PostgreSQL constraint 변경은
historical before/after, catalog precondition, revision·recorder, 행·다른 제약 보존과 실패 rollback을 함께 소유한다.
실제 deferrability가 선언과 다르면 drift로 거부한다. SQL projection·적용·역방향은 같은 의미를 사용한다.
DB-free SQL projection은 operation body만 반환한다. SQLite의 sequence는 실행 시 SQL로 복사하고, FK mode·transaction과
catalog/recorder 소유권은 실제 migration lifecycle에 남긴다([SQL projection ADR](0055-project-linked-deterministic-migration-sql-projection.md)).

명확한 commit 성공 뒤에만 호출자가 넘긴 root의 PK/cache를 게시한다. 별도로 보유한 후손 객체나 observer 값을 전역에서 수정하지 않는다.
실패·취소·rollback/commit 결과 불확실성은 기존 원인과 구분을 보존하며 caller의 메모리를 성공 상태로 바꾸거나 자동 재시도하지 않는다.

타입별 생성 코드는 descriptor·관계 metadata·binding을 제공하고, 그래프 수집·검사·실행은 공통 runtime이 담당한다.
DB별 SQL·catalog·transaction 검사 시점은 backend가 소유한다. 동일 모델 의미의 별도 원본이나 과거 generated ABI용 변환 계층을 만들지 않는다.

## 실제 소비자와 검증

TicketLabel은 ticket/label CASCADE FK와 named `(ticket, label)` 고유 제약을 가진다. Category를 중복 저장하지 않고 두 endpoint의
서버 배정 Category를 모두 scoped 조회·저장 transaction에서 확인한다. 두 관계의 view 권한과 링크 mutation 권한은 입력·I/O 전에
검사한다. API Session/Bearer는 하나의 인증/CSRF 경계 안에서 모든 권한을 AND로 평가하고 custom authorizer의 deny overlay를 유지한다.
권한 목록은 생성 시 검증·복사하며 OpenAPI도 같은 primary/additional 요구를 게시한다. Admin 추가/수정은 두 authorized choice를 사용한다.
조회/연결 해제 권한에는 endpoint ID만 포함되고, 이름을 노출하지 않는다. 기존 ServiceReport의 explicit-ID 권한 의미는 바꾸지 않는다.
빈/self PATCH와 생략한 endpoint도 같은 범위를 검사한다. 외부 ORM/SQL의 Category 재할당을 막는 영구 제약/행 잠금은 이 소비자의 계약이 아니다.
Ticket/Label 삭제는 연결만 정리하며 기존 PROTECT를 보존한다. 확정 보호만 정상 거부로 게시하며 취소·cleanup 불확실성과 함께 온 오류는 실패다.
문자열 검색 필드가 없는 연결 모델의 Admin은 SearchFields 생략으로 검색을 끄고 선언하지 않은 q를 I/O 전에 거부한다.
일반 ManyToMany 선언·관리자와 add/remove/set·조회는 [카탈로그](../CAPABILITY_CATALOG.md)에 남아 있는 후속 기능이다.

양 DB의 required/nullable 순환, 깊은 후손·중복 경로·숨긴 역관계·OneToOne, PROTECT 우선·NULL observer 보존, native FK와
정책 migration의 실제 catalog를 확인한다. 취소·조회/변경/row-close 실패·동시 변경·rollback/commit 불확실성·caller/cache 보존은
Go-native 검사로 유지한다. 실제 지원과 source·환경별 결과를 이 설계 채택과 합치지 않는다.

## 출처

Pinned Django 6.1 `deletion.py`, `fields/related.py`와 직접 작성한 public 입력의
[독립 runner](../../conformance/runners/django/cascade_reference.py)를 참조한다(BSD-3-Clause; 입력은 derived=false).
13개 관찰과 source SHA256, 각 환경에서 실행한 범위는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.
Physical policy와 Go의 context·오류·publication 책임은 이 ADR의 GoDj 결정이다.
