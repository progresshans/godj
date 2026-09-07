---
id: GDJ-0063
status: active
updated: 2026-09-08
baseline_commit: "df19040fc0e9c5a0e966ceada4b2d4484bdc5a57"
integration_owner: "Codex"
---

# 제품·검증 코드의 책임 정리와 결함 수정

## 결과와 범위

구조 검토에서 재현한 세션 만료 경쟁과 showmigrations Unicode identity 손실을 수정한다.
제품과 검증 코드의 반복 정책·상태 준비를 단일 소유자로 모으고 실제 순감소와 실행 범위를 확인한다.
관련 의미는 [현행 아키텍처](../docs/ARCHITECTURE.md), [검증 원칙](../docs/TESTING.md), 각 관련 ADR에 반영한다.
기존 브랜치와 Draft PR #1을 사용한다. 불필요한 내부 ABI 호환 계층과 목표 줄 수를 만들지 않는다.

## 구현과 검증

- [x] F1: Store의 원자적 만료·touch·삭제와 같은 세션의 순서 역전·rotation·영속 저장소 회귀
- [x] F2/S1: 공용 JSON lexical 검사와 ProjectSpec wire 처리, 명령별 budget·envelope·오류 우선순위 보존
- [x] M1/S3: 값싼 definition metadata 조회와 immutable 정의·graph 소유권, fresh history/revision fence 보존
- [x] S2: CLI workspace/build/process/cleanup 공통 흐름과 명령별 terminal policy 분리
- [x] S4: DB 독립 Query AST 검사 공유, SQLite/PostgreSQL compiler·DDL·transaction 차이 보존
- [x] S5: migration operation 경계 정규화와 반복 pointer/value 처리 축소
- [x] S6: source-shape audit 축소와 import/I/O 경계·실행 관측·부정 대조 보존
- [x] S7: contract별 필요한 observation 실행, fresh fixture와 actual/oracle 독립성 보존
- [x] S8: 위험별 unit/process/DB/platform 검증 소유권과 CI 선택 정리, required/no-skip/completion 유지
- [x] M2: 실제 generated header 기준 집계 도구와 기존 분류 오류 정정
- [x] 관련 normal/race/CGO-disabled·generated drift·Python/oracle·DB/process 통합 checkpoint
- [ ] 고정 구현 소스의 전체 Hosted 검증, 기존 Draft PR 정리와 완료 기록

작은 편집 중에는 필요한 compile 확인만 한다. 각 설계 묶음의 제품·지원 코드·회귀 테스트를 함께 완성한 뒤
관련 검증을 실행한다. 로컬은 변경 위험의 집중 검증을, 마지막 Hosted 실행은 전체 플랫폼·PostgreSQL·cold-build를 소유한다.
같은 전체 gate를 로컬과 Hosted에서 관성적으로 반복하지 않는다. 어떤 검사를 통합했는지와 남은 검증 소유자는
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 기록한다.

### CLI 실행과 terminal policy

공용 실행 소유자는 retained project 선택·재검사, private workspace, build/child 실행 관측과 자원 해제를 담당한다.
argv·private envelope·실패 분류·공개 결과는 각 명령이 소유한다. cleanup과 공개 출력은 항상 이 순서로 한 번 실행한다.

| 명령 | 준비와 실행 경계 | 완료 뒤 취소와 실패 선택 |
| --- | --- | --- |
| check | argv, 선택, workspace, build, 제한된 child 응답 | 엄격한 parse 뒤 outer cancellation barrier 유지 |
| migrate / showmigrations / sqlmigrate | argv, 선택, workspace, build, 전체 response drain과 parse | 완성된 child 결과가 terminal. 늦은 취소로 durable 결과·닫힌 read 결과를 덮어쓰지 않음 |
| generate | 공용 준비 뒤 ProjectSpec, bundle 검사·publication | publication의 durable commit과 recovery-required 의미 유지 |
| makemigrations | build 전후 입력 fingerprint, writer lock 뒤 재계획·append | fsync된 prefix가 생기면 늦은 취소로 결과를 덮어쓰지 않음 |
| createsuperuser | 잘못된 argv는 환경·cwd·TTY보다 먼저 거부. build 뒤 TTY와 별도 sensitive child | terminal 복구 실패·known-created·outcome-unknown 및 전용 cleanup 오류 보존 |
| runserver | 선택 시 runtime package 검사, 선언 build, bundle 검사, 별도 runtime build | foreground signal·listener·process drain의 전용 결과와 종료 코드 유지 |

workspace 부분 생성 실패는 생성 owner가 회수하고 retained project를 닫는다. full workspace는 공용 close가 회수한다.
정리 실패는 성공·취소·interrupt를 대체하며 이미 확인한 다른 주 오류는 보존한다. operator의 known-created 정리는 전용 정책이다.

## 현재 상태와 다음 행동

F1/F2, M1/M2와 S1..S8 구현·관련 의미 문서화를 마쳤다. 집중 normal, 관련 race/CGO-disabled와
소비자 통합·실행 대조·외부 명령·고정 reference 검증을 확인했다. 구현 소스를 고정하고
Hosted full scope의 실제 PostgreSQL·platform·cold-build 결과까지 확인한다.
DB별 physical safety, generated bundle의 복구와 migration 문서의 append-only publication, 인증 방식별 경계는 유지한다.
검토 후보 전부를 추적하며 공통화가 부적절한 부분은 남겨야 하는 의미와 검증 근거를 기록한다.
