---
id: GDJ-0001
status: completed
updated: 2026-08-07
baseline_branch: "main"
baseline_commit: "f2afd7f"
result_commit: "927788d28f964a9597ff0962138bc56e78de7b14"
depends_on: ["GDJ-0000", "ADR-0005"]
contracts: ["META-001", "META-002", "QRY-001..QRY-010", "SCH-001", "GEN-010"]
allowed_paths: ["go.mod", "go.sum", "pyproject.toml", "uv.lock", "requirements*.txt", ".python-version", ".gitignore", "LICENSE*", "NOTICE*", "Makefile", "conformance/**", "internal/testutil/**", ".github/workflows/**", "README.md", "AGENTS.md", "docs/**", "work/**", "prompts/**"]
integration_owner: "one primary agent"
---

# Django 6.1 Compatibility Lab

## 사용자에게 보이는 결과

개발자는 고정된 Django 6.1 환경에서 작은 동작 계약을 반복 재생하고, 이후 GoDj 결과와 비교할 수 있습니다. 아직 ORM 사용자는 만들지 않습니다.

## 목표

1. exact compatibility profile을 재현 가능하게 잠급니다.
2. 8~12개 초기 contract의 provenance와 expected observation을 기록합니다.
3. Django runner가 deterministic oracle을 생성합니다.
4. normalizer/comparator가 실제 차이를 탐지하고 false green을 만들지 않음을 증명합니다.
5. codegen bootstrap의 대표 실패를 재현하고 후보를 비교할 evidence를 만듭니다.

## 비목표

- production Schema DSL/IR/Codegen 완성
- GoDj ORM, SQL compiler, migration 구현
- 범용 YAML/JSON Django operation 언어
- PostgreSQL/MySQL/Oracle setup
- public Go API freeze
- Django source checkout 수정

## 필수 읽기

- [`AGENTS.md`](../AGENTS.md)
- [Architecture](../docs/ARCHITECTURE.md)
- [Compatibility](../docs/COMPATIBILITY.md)
- [Testing](../docs/TESTING.md)
- [Open Questions Q-001~Q-004](../docs/OPEN_QUESTIONS.md)
- [ADR-0001](../docs/adr/0001-schema-ir-as-canonical-source.md)
- [ADR-0002](../docs/adr/0002-codegen-generics-runtime-metadata.md)
- [ADR-0004](../docs/adr/0004-cli-and-project-binary.md)
- [ADR-0005](../docs/adr/0005-contract-first-vertical-slices.md)
- [Sources](../docs/SOURCES.md)

## 설계 원칙

- 초기 scenario adapter는 Django와 GoDj 쪽에 명시적으로 작성합니다.
- 공통 protocol은 fixture/parameters/normalized observation의 최소 교집합만 가집니다.
- oracle은 generated artifact이며 provenance와 regeneration command가 있어야 합니다.
- 순서, NULL, error category를 정규화로 숨기지 않습니다.
- GoDj 미구현 결과는 skip/pass가 아니라 명시적 `not_implemented` 상태입니다.
- upstream 테스트를 복사·번역하면 원본 경로/이름/commit/license를 파일 가까이에 둡니다.

## 초기 contract 후보

최종 8~12개는 profile lock과 함께 확정합니다.

- `QRY-001` exact lookup
- `QRY-002` ASCII `icontains`
- `QRY-003` chained filter의 AND 의미
- `QRY-004` chain 후 원본 QuerySet 의미 불변
- `QRY-005` `order_by`와 limit
- `QRY-006` empty result
- `QRY-007` nullable/`isnull`
- `QRY-008` unknown field/lookup error category
- `QRY-009` query construction 시 DB I/O 없음
- `SCH-001` model/field metadata normalization 후보

result cache는 [Open Questions Q-007](../docs/OPEN_QUESTIONS.md)이 결정되기 전 oracle observation만 수집하고 GoDj contract 고정은 보류할 수 있습니다.

## 구현 단계

### 1. 기준 상태와 module bootstrap

- Git branch/commit/dirty files 기록
- remote를 재확인하고 module path 결정
- Go language/toolchain 정책과 최소 CI 골격 결정
- Python environment manager와 exact lock 방식 결정

### 2. Profile과 provenance

- Django tag/commit, Python, SQLite, timezone, locale pin
- 재현 명령과 environment fingerprint 출력
- upstream source/license metadata schema 정의

### 3. Contract/observation v0

- contract manifest 최소 schema
- canonical observation encoding
- error category, datetime/decimal/UUID/bytes/NULL/PK normalization
- manifest와 normalizer validation test

### 4. Django runner와 oracle

- explicit scenario adapter 구현
- disposable SQLite DB로 실행
- 같은 환경에서 oracle 두 번 생성 후 byte comparison

### 5. Comparator와 false-green tests

- result value, order, error category, DB state 중 하나를 의도적으로 변경
- 각 mismatch가 실패하는 test 작성
- `not_implemented` GoDj result가 pass되지 않음을 검증

### 6. Codegen bootstrap spike

production package를 만들기보다 최소 fixture로 다음 실패를 재현합니다.

- schema rename/delete 후 stale generated type 때문에 package compile 실패
- 사용자 method가 old generated field/type에 의존
- generator가 새 output을 만들려면 broken package를 import해야 하는 상황

선언/생성 package 분리, 제한적 AST 추출, bootstrap package 후보의 compile 가능성·사용성·제약을 기록하고 Q-001 ADR 초안을 만듭니다.

## 완료 조건

- [x] exact profile과 lockfile/hash가 기록됨
- [x] 11개 contract가 manifest validation을 통과함
- [x] 각 contract에 upstream path/version/provenance가 있음
- [x] Django oracle이 동일 환경 두 번 실행에서 byte-identical함
- [x] normalizer unit/property tests가 통과함
- [x] comparator mutation tests가 result/order/error/DB-state mismatch를 탐지함
- [x] GoDj `not_implemented`가 false green을 만들지 않음
- [x] codegen bootstrap 실패가 실행 가능한 fixture로 재현됨
- [x] Q-001 선택지 비교와 ADR 결정이 기록됨
- [x] CI가 최소 manifest/reference suite를 실행하도록 구성되고 동일 명령이 로컬에서 통과함
- [x] 실제 명령이 TEST_EVIDENCE에 기록됨
- [x] CURRENT와 IMPLEMENTATION_MATRIX가 갱신됨

## 위험

- harness가 framework보다 커지는 것
- Python/Django/SQLite drift로 oracle이 바뀌는 것
- normalizer가 의미 차이를 숨기는 것
- upstream 코드를 provenance 없이 복사하는 것
- compile spike를 production API처럼 굳히는 것

## 인수인계 체크포인트

중단 시 exact environment command, 마지막 성공 contract, 실패한 comparator case, generated oracle diff, Q-001 spike 결과, 미커밋 파일, 다음 명령을 기록합니다.

## 구현 결과

- Profile `django-6.1-sqlite-darwin-arm64`에 Django/Python/SQLite/source ID,
  timezone, locale, platform, uv version과 lock hash를 고정했습니다.
- QRY-001~010과 SCH-001의 독립 scenario, provenance manifest, deterministic
  Django oracle을 만들었습니다.
- Strict Go protocol은 unknown JSON field, 잘못된 tagged value, contract 순서와
  comparison dimension drift를 거부합니다.
- Comparator는 result value/order, phase, error category/code, contractual message,
  DB state, metrics mutation을 탐지합니다.
- `godj-not-implemented.json`은 유효한 미구현 observation이지만 oracle과 비교하면
  11개 mismatch로 실패합니다.
- Codegen spike 결과 선언 package와 generated target package를 분리하는
  [ADR-0006](../docs/adr/0006-codegen-input-package-boundary.md)을 채택했습니다.
- 독립 시나리오와 Django 파생물의 구분, BSD 고지, 아직 선택되지 않은 GoDj 자체
  라이선스 상태를 [LICENSING.md](../docs/LICENSING.md)에 기록했습니다.

## 검증과 제한

[EVID-20260807-002](../docs/status/TEST_EVIDENCE.md#evid-20260807-002--gdj-0001-compatibility-lab)에
exact 환경, 명령, 결과가 있습니다. 구현 commit은
`927788d28f964a9597ff0962138bc56e78de7b14`입니다.

- GitHub-hosted CI는 아직 push되지 않아 원격 실행 증거가 없습니다. Workflow YAML과
  동일한 `make ci`는 로컬에서 통과했습니다.
- 일반 Linux CI는 portable suite만 검증하고 darwin/arm64 oracle 재현을 주장하지
  않습니다.
- CPython 3.14.3은 현재 3.14 최신 micro가 아니며 exact GoDj reference일 뿐입니다.
- Codegen fixture는 production DSL/generator가 아니고 최초 생성, 다중 파일,
  Windows, build tag, cross-app relation을 검증하지 않았습니다.
- GoDj ORM이 없으므로 contract 상태는 `oracle_locked`이며 `passing`이 아닙니다.

## 다음 단계

M0 gate를 닫고 [GDJ-0002](0002-model-to-query-walking-skeleton.md)를 `ready`로
승격했습니다. 다음 작업은 M1의 API/SQLite dependency 결정을 작은 compile spike로
검증하는 것입니다.
