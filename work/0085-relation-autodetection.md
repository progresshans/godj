---
id: GDJ-0085
status: completed
updated: 2026-09-20
baseline_commit: "d6db513aba479ebec6a5f256bebac9348a0a32ce"
integration_owner: "root"
---

# Self/cyclic 모델 선언의 자동 migration 계획

## 목표

GDJ-0084의 historical graph를 모델 선언 → makemigrations preview/write → load → 실제 migrate/역방향으로 연결한다.
새 자기참조·순환 모델과 기존 모델의 허용된 nullable 관계 추가를 수작업 definition 없이 표현한다. 전체 완성 목표의
migration 의존성을 이어 가는 작업이며 넓은 field 변경·임의 backfill까지 완료했다는 뜻은 아니다.

## 확인된 출발점

- `internal/migrationautodetect.validateAddedRelation`은 self를 거부한다. 같은 CreateModel에서 visible한 self와
  기존 모델의 nullable self Add에는 GDJ-0084의 runtime 기반을 사용할 수 있다.
- 서로 다른 새 model의 later target과 cross-app candidate cycle은 아직 거부한다. 현재 `Detect`는 app당 candidate
  하나를 만들며 `NewStateReconstructor`로 최종 state가 desired와 정확히 같은지 확인한다.
- 모델/필드 순서는 보존해야 한다. 명시적 AutoField는 어느 위치에도 올 수 있으므로 Create prefix만 남기면 PK를 잃을 수 있다.
  필요한 FK만 Create에서 미루고 `AddField.BeforeField`로 원래 위치에 삽입한다. Migration dependency graph는 DAG를 유지한다.
- Same-app later target은 한 migration 안에서 model들을 먼저 만들고 미룬 FK를 Add하는 순서로 연결한다.
  Cross-app은 app 후보 의존성의 순환 성분과 기존 target의 historical authority를 구분하고, 필요한 경우 생성 후보와 후속
  관계 후보로 나눈다. 단순히 모든 relation에 상대 app의 최신 후보 의존성을 추가하면 불필요한 cycle을 만들 수 있다.
- SQLite/PostgreSQL은 새 빈 table에 대한 required/default Add의 검증 기반이 있다. 기존 populated table의 required 관계는
  임의 값으로 채우지 않으며 현재 unsupported/backfill 경계를 유지한다.
- 기존 Go-owned MIG-107의 `self_or_cyclic_relation` 거부 설명도 실제 지원 범위와 함께 갱신해야 한다. Raw Django 관찰이나
  일반 comparator를 바꿔서 미완료 동작을 통과시키지 않는다.

## 구현과 검증 순서

1. Self Create와 existing nullable self Add의 자동 후보, 결정적 naming/dependency, strict load와 desired-state 일치를 연결한다.
2. Same-app later target·상호참조와 cross-app 새 cycle을 위한 operation/candidate 분할을 설계한다. App당 한 candidate라는
   현재 구현 가정과 protocol/publication의 실제 제한을 확인하고 필요한 부분만 바꾼다. 한도 초과는 복제·게시 전에 거부한다.
3. Generated candidate 순서는 각 파일의 durable publication prefix에서도 dependency-valid해야 한다. 기존 source 보존,
   CAS·동시 게시·중단 복구와 replay/no-op을 유지한다. 선언 순서·field order·NULL/default·정확한 target identity를 함께 검사한다.
4. 독립 Django autodetector 관찰과 Go-owned 결정의 범위를 구분한다. 별도 module의 공개 CLI/생성 모델 소비자에서
   preview/write/reload/migrate, 실제 자기·상호 참조 값, restart와 역방향을 실행한다. 실패 시 partial source/state를 성공으로 세지 않는다.
5. 완성한 변경 묶음의 affected 검증과 필요한 DB/process checkpoint를 실행하고 CURRENT/TEST_EVIDENCE에 source·환경을 기록한다.

## 현재

Self Create·nullable self Add, same-app later/mutual 관계와 cross-app cycle의 자동 계획을 구현했다. 매 후보를 정확한
직전 durable prefix에서 다시 계산하여 어떤 후보 파일 뒤에서 중단해도 나머지 bytes가 같다. BeforeField는 선언 순서를
보존하고 물리 column 순서와 분리된다. Required PROTECT FK는 실제 빈 table에서만 적용한다.

실제 SQLite/PG의 apply/reopen/reverse/zero/reapply와 populated required Add 거부, 공개 CLI의 preview/write/clean,
별도 module의 generated ORM 저장·조회, 세 후보 각각의 부분 쓰기/fsync SIGKILL 재개와 동시 게시를 연결했다.
재접속 소비자에서 발견한 SQLite 기본 FK 검사 누락은 모든 physical connection의 초기화·readback으로 수정했다.
Go-owned MIG-107만 현재 독립 decision 실행으로 갱신하고 나머지 관찰·Django profile은 보존했다.

로컬 normal/race/CGO0의 affected runtime, 현재 writer conformance, 네 Python 버전의 독립 Django 관찰,
전체 compile-only·affected vet·generated drift 검사를 완료했다. 상세 source·명령·범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 있다.
기존 Draft PR에 제품을 통합했다. 첫 Hosted에서 발견한 공통 artifact checksum 갱신 누락을 수정하고 전체 protocol 검사를 통과했다.
수정 source의 Hosted ORM은 같은 source의 네트워크 실패 job 재실행 후 완료했다. Python fingerprint 보조 연결 종료 수정도
별도 재생으로 검증했다. Source·실행 범위와 의도적 차이는 TEST_EVIDENCE에서 구분한다. GDJ-0085의 완료는 전체 프레임워크 완성을 뜻하지 않는다.
