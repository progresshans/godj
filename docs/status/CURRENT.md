# 현재 상태

- 마지막 갱신: 2026-08-07
- 저장소: `/Users/hanhyeonjin/Documents/godj`
- 브랜치: `main`
- 기준 구현 commit: `927788d28f964a9597ff0962138bc56e78de7b14`
  (`test: establish Django compatibility lab`)
- remote: `https://github.com/progresshans/godj.git`, remote refs 없음
- 현재 단계: M0 Compatibility Lab 완료, M1 시작 전
- 활성 작업: 없음
- 다음 준비 작업: [GDJ-0002 Model-to-Query Walking Skeleton](../../work/0002-model-to-query-walking-skeleton.md)

## 현재 checkout에서 확인된 사실

- Go module은 `github.com/progresshans/godj`, language/toolchain은 Go 1.26/1.26.5로
  시작했습니다.
- Framework/ORM/Schema DSL은 아직 구현하지 않았습니다. 현재 Go/Python 코드는 M0
  conformance protocol, Django reference runner, codegen architecture spike뿐입니다.
- Exact profile `django-6.1-sqlite-darwin-arm64`에서 QRY-001~010과 SCH-001의 Django
  oracle 11개가 `oracle_locked`입니다.
- GoDj 구현 결과는 모두 명시적 `not_implemented`이며 `passing` contract는 아직
  없습니다.
- Oracle SHA-256은
  `0fc307d8be596c993bd1424c365de8c17ae9ace626d603e2e62272011845b7b0`입니다.
- Local Go/race/vet, Python exact/portable suite, artifact validation, oracle byte check가
  [EVID-20260807-002](TEST_EVIDENCE.md#evid-20260807-002--gdj-0001-compatibility-lab)에서
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

## 현재 차단 요인과 미결정 사항

M1 시작을 막는 외부 blocker는 없습니다. 다만 production API를 작성하기 전에
GDJ-0002의 첫 compile/runtime spike에서 다음을 좁혀야 합니다.

1. Q-005 descriptor 형태와 freeze 시점
2. Q-006 M1 nullable 표현
3. Q-008 dynamic lookup 오류/chaining API
4. Q-009 package dependency 자동 검증
5. SQLite driver와 CGO/pure-Go, cancellation, license 선택

QuerySet cache(Q-007), CLI/library version(Q-010), goroutine safety(Q-011)는 M1 범위와
맞닿는 지점에서 계약을 확정합니다.

## 다음 정확한 작업

1. GDJ-0002를 `active`로 바꾸고 시작 시점의 `main` commit과 dirty 상태를
   baseline으로 기록합니다.
2. descriptor/nullable/dynamic lookup/package boundary/SQLite driver 후보를 작은
   compile/runtime fixture로 비교합니다.
3. 결과를 ADR 또는 명시적 M1 한정 결정으로 기록합니다.
4. ADR-0006 경계를 지키는 최소 Schema DSL → versioned IR → one-file codegen 단면을
   만듭니다.
5. Generic QuerySet/AST/SQLite 실행을 연결한 뒤 M0 oracle과 실제 differential
   comparison을 시작합니다.

## 작업 재개 체크포인트

- 미커밋 변경: 없음 — 상태 기록 commit 후 `git status`로 다시 확인
- 공개 framework API: 아직 freeze된 항목 없음
- 건드리면 안 되는 범위: `/Users/hanhyeonjin/Documents/django` reference checkout
- M0 exact regeneration: `make check`
- Portable CI equivalent: `make ci`
- 가장 위험한 추측: spike의 fixture DSL/generator를 production API로 승격하거나,
  nullable/dynamic lookup/SQLite driver를 검증 없이 선택하는 것

작업 상태는 [IMPLEMENTATION_MATRIX.md](IMPLEMENTATION_MATRIX.md), 실제 명령은
[TEST_EVIDENCE.md](TEST_EVIDENCE.md)에 기록되어 있습니다.
