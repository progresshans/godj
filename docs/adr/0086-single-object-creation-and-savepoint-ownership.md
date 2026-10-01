# ADR-0086: 단건 조회·조회 후 생성과 savepoint 소유권

- 상태: Proposed
- 날짜: 2026-10-01
- 구현: [GDJ-0107](../../work/0107-single-object-creation-and-savepoints.md), 진행 중

## 검토하는 의미

`QuerySet.Get`은 원 query의 full-result cache를 읽거나 변경하지 않고 새 평가를 수행한다.
정확히 한 객체는 독립 복사로 반환하고, 0개와 여러 개는 서로 다른 안정된 오류 code로 구분한다.
슬라이스 없는 조회는 정렬을 제거하고, 명시한 limit/offset은 보존한다. 결과 판정은 최대 21행으로
제한한다. 빈 조건도 context·query·session 검증을 먼저 수행한다. typed/dynamic 조건은 같은 AST를 쓴다.
선택한 관계와 prefetch의 소유권은 기존 materialization 경계와 함께 유지한다.

Savepoint는 이미 열린 transaction의 borrowed session에서 수행한다. 생성·rollback·해제와
callback 동안의 자원은 그 scope가 소유하며, child session은 callback 종료 뒤 사용할 수 없다.
중첩 child가 활성인 동안 parent handle의 I/O를 거부하여 child의 rollback 범위를 유지한다.
열린 cursor와 동시 진입·부모/자식 수명도 명시적으로 검사한다. savepoint의 rollback/해제를
확인할 수 없으면 부모 transaction을 정상 commit하거나 생성 경쟁을 자동 재시도하지 않는다.
부모 transaction의 commit·rollback과 SQLite quarantine의 기존 소유권은 유지한다.

`GetOrCreate`는 먼저 fresh 단건 조회를 하고, 부재일 때만 명시적 생성 입력을 준비한다.
생성은 owned transaction 또는 borrowed session의 savepoint 안에서 한 번만 시도한다.
무결성 오류 뒤 rollback을 확인한 경우에만 원 조건을 한 번 다시 조회한다. 여전히 부재라면 원래
생성 오류를 보존한다. 다중 일치·취소·다른 DB 오류·unknown outcome을 정상 반환으로 바꾸지 않는다.
실제 unique 제약 없는 임의 조건의 중복 생성을 막는 보장은 하지 않는다.
Query 조건을 생성 입력에 암묵적으로 복사하거나 인가 정책으로 취급하지 않는다. 생성 값과 조회 조건의
일치는 호출 앱이 명시하고, 기본값과 필드 의미는 원 Manager의 정규화 metadata snapshot을 따른다.

Helpdesk는 category/name의 실제 복합 unique와 현재 category 접근·생성/조회 권한을 함께 검사한다.
이미 존재하는 Label은 기존 값을 반환하고, 새 행과 감사 기록은 같은 부모 transaction에 속한다.
일반 create endpoint의 중복 거부와 별개의 명시적 업무 동작으로 제공한다.

## 기준과 남은 결정

고정 Django 6.1의 `QuerySet.get`, `get_or_create`, `transaction.Atomic` source와 양 DB 실제 관찰을
기준으로 한다. Python 내부 객체/예외 구현을 복제하지 않고 Go의 context·error·명시적 session 수명으로
외부 결과를 표현한다. 출처는 [SOURCES](../SOURCES.md)의 고정 Django와 BSD-3-Clause 표기를 따른다.
독립 탐색과 최종 fixture·Go 제품 실행은 구분한다.

공개 savepoint helper의 최종 형태, backend/streaming wrapper의 capability 전달과 생성 모델 facade는
실제 소비자를 연결하면서 확정한다. row locking·update-or-create·bulk의 넓은 요구는 같은 완료로 주장하지 않는다.
