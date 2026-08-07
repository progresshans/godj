# 현재 상태

- 마지막 갱신: 2026-08-08
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 구현 commit: `bb9225df91f12f2faaa3d50da5b9555819fe0256`
  (`feat: implement M1 model-to-query walking skeleton`)
- remote: `https://github.com/progresshans/godj.git`, remote refs 없음
- 현재 단계: M1 Model-to-Query Walking Skeleton 완료, M2 contract 확장 준비
- 활성 작업: 없음
- 다음 준비 작업: [GDJ-0003 Write/Migration Compatibility Contracts](../../work/0003-write-migration-compatibility-contracts.md)

## 현재 checkout에서 확인된 사실

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5로
  시작했습니다.
- M1 범위의 Schema DSL, versioned IR, deterministic codegen, generated Article/FieldSet/
  descriptor, generic Manager/QuerySet, immutable Query AST와 SQLite compiler/executor가
  구현됐습니다. 한 app/한 model/제한 field와 read query만 해당합니다.
- Exact profile `django-6.1-sqlite-darwin-arm64`에서 QRY-001~010과 SCH-001의 Django
  oracle 11개를 GoDj adapter가 모두 통과해 manifest 상태가 `passing`입니다.
- M1 Go backend는 `modernc.org/sqlite v1.56.0`, SQLite 3.53.3이며 Django reference
  SQLite 3.50.4와 별도 fingerprint로 관리합니다.
- Oracle SHA-256은
  `0fc307d8be596c993bd1424c365de8c17ae9ace626d603e2e62272011845b7b0`입니다.
- Generation drift/target compile, 전체 Go/race/vet, `CGO_ENABLED=0`, external compile,
  Python exact/portable suite, GoDj differential과 oracle byte check가
  [EVID-20260808-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton)에서
  통과했습니다.
- GitHub Actions workflow는 전체 action SHA와 tool version을 고정했지만 아직 push하지
  않아 원격 실행 증거는 없습니다.
- 로컬 Django checkout은 수정하지 않았습니다. Oracle은 잠긴 PyPI Django 6.1
  environment에서 생성했습니다.

## M0에서 닫은 결정

- Exact Python/Django/SQLite/platform/locale profile과 uv lock/hash
- 11개 contract manifest와 typed observation protocol v1
- Strict normalizer/comparator와 false-green mutation gate
- 독립 시나리오와 Django-derived material의 provenance/license 정책
- Codegen 입력과 generated target을 import graph에서 분리하는
  [ADR-0006](../adr/0006-codegen-input-package-boundary.md)

## M1에서 닫은 결정

- Generated zero-state descriptor + `orm` 소유 generic interface와 generation-time freeze
- Nullable CharField read representation `*string`; write omission은 계속 Q-006
- Ordered `ParseDynamic` construction validation과 typed predicate 합류
- External nested module compile와 `go list` dependency boundary gate
- `modernc.org/sqlite v1.56.0` pure-Go backend, context/cleanup/race gate
- Production candidate overlay compile-only와 last-good generated output 보존

## 현재 차단 요인과 미결정 사항

다음 작업을 막는 외부 blocker는 없습니다. 다음 결정은 아직 열려 있습니다.

1. Q-006 create/update에서 omitted, explicit NULL, zero value 표현
2. Q-007 QuerySet result/error cache와 terminal operation 공유
3. Q-010 public CLI와 project library/generator version protocol
4. Q-011 QuerySet/transaction/hook의 전체 goroutine safety
5. Q-012 migration format, historical state, recorder, lock와 DDL transaction

## 다음 정확한 작업

1. GDJ-0003을 `active`로 바꾸고 M1 완료 commit과 clean status를 baseline으로
   기록합니다.
2. 새 manifest를 쓰기 전에 Django runtime probe로 write/schema/transaction 후보
   8~12개의 관찰 안정성을 확인합니다.
3. 기존 11개 M1 manifest와 분리된 두 번째 ordered contract set/oracle을 만듭니다.
4. Q-006/Q-012의 제품 구현 전 최소 결정을 좁히고 다음 product work item을 ready로
   작성합니다.

## 작업 재개 체크포인트

- 미커밋 변경: 없음 — M1 handoff commit 후 `git status`로 다시 확인
- 공개 framework API: M1 subset은 검증됐지만 pre-1.0이며 전체 API freeze 아님
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- M1 전체 local gate와 exact regeneration: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: M1 read API만 보고 write state/migration ABI를 설계하거나 새
  계약 없이 Django 전체 save/migration 의미를 구현했다고 주장하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
