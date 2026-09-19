---
id: GDJ-0085
status: active
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
- 모델/필드 순서는 보존해야 한다. 관계 필드를 뒤로 옮기기만 하면 중간 FK 뒤에 scalar가 있는 선언의 순서가 달라진다.
  필요한 Create prefix와 나머지 field suffix의 Add 순서를 함께 설계한다. Migration dependency graph는 DAG를 유지한다.
- Same-app later target은 한 migration 안에서 Create prefix들을 먼저 만들고 미룬 suffix를 Add하면 처리할 수 있는지 확인한다.
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

GDJ-0084의 로컬·Hosted ORM 검증을 완료했다. 별도 `feature/relation-autodetection` 작업 사본에서 self Create와
existing nullable self Add의 거부 규칙을 수정하고 후보/재구성/no-op 검증을 작성했다. 현재 compile-only를 확인했으며,
이 작업의 runtime PASS나 기존 Draft PR로의 제품 통합은 아직 없다. Same-app later target·cross-app cycle 분할과
code-owned 거부 정책, 실제 CLI/DB 소비자를 이어서 구현한다.
