# ADR-0053: Project-linked Read-only Migration Status

- 상태: Proposed
- 날짜: 2026-08-30
- 관련 work/contract: GDJ-0051, MIG-111..MIG-118, Q-010, Q-012
- 대체하는 ADR: 없음

## 맥락

Completed GDJ-0050 뒤 사용자는 `makemigrations`와 latest-only `migrate`를 실행할 수 있지만, loaded definition catalog와
durable recorder가 보는 적용 상태를 public command로 확인할 수 없습니다. Core에는 immutable Planner, explicit
`CheckHistory`, separate `AppliedMigrationReader`와 SQLite/PostgreSQL read-only snapshots이 이미 있습니다. 반대로 public
target/reverse를 바로 열면 app-zero가 cross-app dependent까지 되돌리고 data를 제거할 수 있는데, 현재 CLI에는 그 상태를
확인할 안전한 관측면이 없습니다.

## 결정 기준

- Django에서 익숙한 migration list 경험
- database mutation이 없는 명확한 read-only 경계
- unknown/inconsistent history를 숨기지 않는 운영 안전성
- existing project selection, definition loader와 backend ownership 재사용
- strict bounded wire와 secret/cause redaction
- 이후 target/reverse와 `--plan`을 추가할 수 있지만 현재 snapshot을 실행 authority로 오해하지 않는 API

## 고려한 선택지

### A. `showmigrations`와 target/reverse를 한 번에 공개

Core 실행 기능은 이미 있지만 관측과 destructive 실행의 failure/UX를 동시에 고정해야 합니다. 특히 app-zero는 cross-app
dependent를 reverse할 수 있고 현재 성공 출력은 실행 plan을 제공하지 않아 첫 public rollback 경계로는 너무 큽니다.

### B. list-only `showmigrations`를 먼저 공개

Existing read-only reader와 Planner를 재사용하며 Schema IR, Definition format, generated ABI와 write transaction을 바꾸지
않습니다. 이후 target/reverse의 선행 관측면이 되며 실패 격리가 쉽습니다.

### C. `sqlmigrate`까지 함께 공개

Current SQLite/PostgreSQL SQL compiler와 relation/remake materialization은 backend/lifecycle private입니다. 실제 transaction을
열어 rollback하며 SQL을 수집하면 DB 접근과 lock side effect가 생기므로 올바른 read-only 구현이 아닙니다. 별도
compile-without-execute port가 필요합니다.

## 제안 결정

GDJ-0051에서는 exact `godj showmigrations [--project <godj.toml>]` list-only command를 구현합니다.

1. Definition catalog를 먼저 load한 뒤 project-owned backend와 revision-fenced read-only session을 각각 한 번 열고 session의
   `AppliedMigrationReader` snapshot을 정확히 한 번 호출합니다.
2. Core가 known history를 검증하고 app별 known migration의 dependency-valid canonical order를 제공합니다. App group은 label
   순이며 전체 출력이 global cross-app dependency order라고 주장하지 않습니다.
3. Known applied/unapplied는 `[X]`/`[ ]`, valid unknown recorded identity는 같은 app의 known rows 뒤에 `[?]`로 표시합니다.
4. Read path는 schema, recorder, revision metadata를 만들거나 수정하지 않습니다.
5. Result는 point-in-time snapshot이며 이후 migration write에 대한 lock/fence/authorization token이 아닙니다.
6. 별도 protocol v1은 structured rows만 전달하고 global owner가 canonical text를 렌더링합니다.
7. Invalid definition은 backend open 전에, orphan/corrupt current control state, inconsistent known history와 resource violation은
   public output 전에 fail-closed합니다.
8. Raw driver cause, DSN, SQL과 definition bytes는 private response와 public output에 포함하지 않습니다.
9. Private wire는 raw valid UTF-8 identity를 유지하지만 public text는 모든 app/name을 injective Go graphic body로 escape합니다.
   특히 app heading의 첫 Unicode whitespace rune는 hex escape해 row marker로 보이는 heading을 만들 수 없게 합니다.
10. Read-only runner도 strict response pipe를 소유하므로 direct child 종료 뒤 descendant-held pipe/process group을 2초 grace로
    bounded cleanup합니다. Backend 획득 전 취소는 open 0이며, backend/session close까지 끝난 snapshot은 직후 취소로 지우지
    않습니다.
11. 논리적 실패는 public stdout write를 시도하지 않고 closed `category/code` stderr를 한 번 시도합니다. Final stdout writer가
    partial/error를 반환하면 기록된 prefix를 회수하거나 결과를 재게시하지 않고 internal nonzero로 끝냅니다.

Phase B local implementation checkpoint `294e7e2...`에서 exact core API와 response byte ceiling은 검증됐습니다. 이 ADR은
Phase A의 pinned reference artifact와 Phase C/D의 SQLite/PostgreSQL no-mutation product evidence까지 고정한 뒤
Accepted로 전환합니다.

## 결과

- 생성·적용·조회의 기본 migration 개발 루프가 이어집니다.
- Target/reverse는 상태 관측 뒤 별도 bounded packet으로 안전하게 공개할 수 있습니다.
- Existing `project.MigrationBackend`의 revision-fenced session port를 재사용하므로 public interface widening이 없습니다.
- Unknown recorder row를 Django처럼 조용히 생략하지 않고 표시하는 GoDj-owned 안전 의미가 추가됩니다.
- Revision-fence failure kind와 final output writer의 partial-write 가능성을 숨기지 않고 각각 closed taxonomy와 terminal
  publication observation으로 분리합니다.
- Q-010/Q-012의 broader upgrade/repair/target/destructive 범위는 계속 열려 있습니다.

## 의도적으로 결정하지 않은 것

- app filter, `--plan`, applied timestamp와 ANSI style
- target/reverse/fake/repair/adoption
- `sqlmigrate`와 backend dry-run compiler
- multi-DB alias/router와 distributed snapshot
- destructive/rename/alter/custom/data migration writer

## 검증

- Pinned Django 6.1 `show_list`의 app/list/applied semantic observation
- Core permutation/property test, immutable clone, history error와 race test
- Strict protocol malformed/truncated/oversize/order/resource tests
- Global/linked load-before-open, one open/read/close, pre-acquisition/closed-snapshot cancellation, bounded descendant cleanup,
  identity escaping, cleanup/redaction tests
- SQLite file과 PostgreSQL 17.10 fresh/prefix/full/restart/no-mutation actual
- Repository-external public project compile/process test
