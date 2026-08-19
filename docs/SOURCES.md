# 기준 출처와 검증 기록

- 마지막 확인: 2026-08-12 (Asia/Seoul)
- 외부 사실은 가능하면 공식 1차 출처를 사용합니다.

## Django

- [Django download page](https://www.djangoproject.com/download/) — 2026-08-07 확인 시 최신 공식 버전은 6.1입니다.
- [Django 6.1 release notes](https://docs.djangoproject.com/en/6.1/releases/6.1/) — release date 2026-08-05, Python 3.12/3.13/3.14 지원.
- [Django 6.1 documentation](https://docs.djangoproject.com/en/6.1/)
- [QuerySet API](https://docs.djangoproject.com/en/6.1/ref/models/querysets/)
- [Django test suite guide](https://docs.djangoproject.com/en/6.1/internals/contributing/writing-code/unit-tests/)
- [Django 6.1 source tag](https://github.com/django/django/tree/6.1)
- [Django BSD license](https://github.com/django/django/blob/6.1/LICENSE)
- [`Migration` identity and ordered operations](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/migration.py) — MIG-057의 identity/dependency/operation-order 관찰 근거.
- [`MigrationLoader.build_graph`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/loader.py) — MIG-057/MIG-064의 public graph 구성 관찰 근거.
- [`MigrationExecutor`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/migrations/executor.py)와 [`ExecutorTests.test_run`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/tests/migrations/test_executor.py) — MIG-064의 public plan/migrate handoff 관찰 근거.
- [`ForeignKey` reference](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/docs/ref/models/fields.txt),
  [`many_to_one` tests](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/tests/many_to_one/tests.py) — REL-001..006의 lazy absolute target, forward cache와 forward/reverse lookup 관찰 근거.
- [`on_delete` tests](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/tests/delete/tests.py) — REL-007/008의 `PROTECT`와 `SET_NULL` 결과·mutation 관찰 근거.
- [`select_related` tests](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/tests/select_related/tests.py)와
  [`prefetch_related` tests](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/tests/prefetch_related/tests.py) — REL-009..012의 eager join, invalid reverse path와 two-query reverse batch 관찰 근거.
- [`ForeignKeyDeferredAttribute`와 `ForwardManyToOneDescriptor`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/fields/related_descriptors.py) — REL-002 raw FK cache invalidation, relation assignment의 FK 복사/cache warm와 nullable clear 관찰 근거.
- [`Model._prepare_related_fields_for_save`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/django/db/models/base.py) — REL-002 no-PK assigned target preflight, manual-PK pass-through, target-save-after-assignment key reconciliation과 stale cache invalidation 근거.
- [`ManyToOneTests.test_fk_assignment_and_related_object_cache`](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/tests/many_to_one/tests.py) — FK assignment와 exact related object cache regression 근거.
- [Transaction rollback application-state guidance](https://github.com/django/django/blob/fe0a859f537d4238cf49fca39073513206f83122/docs/topics/db/transactions.txt) — rollback이 model field/application memory를 자동 복원하지 않아 caller가 original values를 수동 복원해야 하는 observable guidance.

로컬 `/Users/hanhyeonjin/Documents/django`에서 tag `6.1`은 commit `fe0a859f537d4238cf49fca39073513206f83122`이며 `VERSION = (6, 1, 0, "final", 0)`임을 확인했습니다. 2026-08-07 당시 checkout `main`은 commit `4243ab11dc957fd14a1875e6b715ff5e6114a415`, Django `6.2.0-alpha`였으므로 6.1 oracle로 직접 사용하지 않습니다.

2026-08-12 GDJ-0033 activation audit에서는 pinned Django 6.1 commit/tag
`fe0a859f537d4238cf49fca39073513206f83122`의 위 exact source/test/docs를 다시 기준으로 삼았습니다. Moving local
main `4243ab11...`은 authoritative evidence가 아닙니다. Checkout을 바꾸지 않고 항상
`git show fe0a859f...:<path>`로 exact object를 읽었습니다. Exact tree는
`7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`입니다.

| Exact object | Blob | Bytes | Audited range/meaning |
|---|---|---:|---|
| `django/db/models/fields/related_descriptors.py` | `eafcb63ceb7c41e6bbf40d4f1f0165c3119b6374` | 69,743 | 87–93 same/different scalar cache; 293–367 assignment/FK/cache/clear |
| `django/db/models/base.py` | `7b7a8833cc6f5a4b3dd4329f0a3cad4374ee8808` | 99,654 | 710–723 PK zero presence; 1270–1307 no-PK/manual-PK/pending reconcile |
| `tests/basic/tests.py` | `a6609f0f30d8d5a2f43271952fe5c5084998e77b` | 42,583 | 563–577 PK zero is set |
| `tests/many_to_one/tests.py` | `e0def73db01621103f5ae7161e962bb373a04e40` | 39,954 | 600–703 assignment identity/cache/no-PK/later-key |
| `docs/topics/db/transactions.txt` | `4733a95bf823e22fc9b9027bfdaffec8498c782b` | 28,481 | 190–194 rollback does not restore memory |

Assignment/FK/cache와 rollback memory non-rewind는 Django observation입니다. Fresh Go source wrapper, exact local target
pointer, corrected canonical three-phase validation, per-edge COW cache와 project-private descriptor는 Accepted ADR-0033의
Go-specific decision입니다. 이 translation은 exact implementation head `be6f3d4e...`의 EVID-076/run `31586910749`에서
Implemented/Verified됐습니다. GoDj는 Django source를 포팅하지 않고 result, side effect, error timing과 transaction
meaning만 independent Go tests로 번역합니다.

Phase A에서 다시 고정한 GoDj relation artifacts는 manifest 10,776 bytes/SHA-256
`3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, pinned Django oracle 33,792
bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static not-implemented fixture
1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`입니다. Phase A/B/C
decision은 이 bytes를 수정하지 않았습니다.

PyPI Django 6.1 wheel SHA-256은
`6c132cd980c9392b06807d4ca52d72530d631dc65a85d9dacede00a780cefbbe`이며
`uv.lock`과 exact profile에 기록했습니다.

## GoDj decision provenance

Accepted [ADR-0019](adr/0019-versioned-migration-definition-source.md)는 Django source가
정의하지 않는 GoDj migration definition JSON v1, compatibility tuple `(1,1,1,2)`, closed
`CreateModel`/`AddField` codec, canonical digest, atomic publication과 failure precedence의
정본입니다. MIG-057..064 manifest의 여덟 contract는 모두 이 ADR을 `kind=decision`,
`derived=false`로 참조합니다. Pinned Django source/test provenance는 실제 공통 동작을 관찰한
MIG-057과 MIG-064에만 별도로 기록하므로 GoDj wire 결정을 Django format claim과 섞지 않습니다.

GDJ-0019 locked artifact는 manifest 5,195 bytes/
`8a5f914a05eaa6382d1f43589743e4e8ba466b747e6fa80eb1cabef61bb924e6`, oracle 29,851
bytes/`efd8cb148bd37445e797da6bc9c1a5184c05214335db64367bafac485956082f`, static fixture
1,574 bytes/`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`입니다. Oracle
directory의 `SHA256SUMS`는 959 bytes/
`c87e6aaaadae94cd7e8bf2f746df81870ba1f88d542ed2d3d2b820d4863b6f1a`입니다.

Accepted [ADR-0021](adr/0021-project-linked-migration-check.md)은 Django가 정의하지 않는
GoDj `godj.toml` project selection, descriptor-v1 subset, private project-runner JSON
protocol, flat no-follow source discovery와 public exit/cancellation 의미의 정본입니다.
MIG-065..074 manifest의 열 contract는 모두 이 ADR만 `kind=decision`,
`derived=false`로 참조하며 Django source/test provenance는 없습니다. 기존 Django-named
exact profile/runner/oracle directory를 재사용하는 것은 protocol-v2 reference corpus와
checksum을 한 gate에서 유지하기 위한 것이고 Django behavior parity 주장이 아닙니다.

GDJ-0021 reference artifact는 manifest 4,580 bytes/
`0cd8d77b03820af75c8bda8434620f40acd1a3cb6319cf4fb732db4b38d44218`, oracle 19,971
bytes/`49f50b97bfa1973cef6fe464296a7c973b87e4ad1f9aaefecee24ab64f04d4d2`, static fixture
1,729 bytes/`86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`입니다. 기존
`SHA256SUMS` 10줄은 byte-for-byte prefix로 보존하고 새 oracle을 11번째 줄에 append해
1,061 bytes/`74b5b253b2026b98ff4cf5a6abce4c0aa4881488df6c874c9012050495b0b59f`로 만들었습니다.

GDJ-0021 implementation head `84ddf109c04acd72992b816aa72140c6e748e5f0`의 hosted 검증은 Draft PR #1
[run 31320798963](https://github.com/progresshans/godj/actions/runs/31320798963)입니다. GitHub의
[hosted runner reference](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)와
[`actions/runner-images`](https://github.com/actions/runner-images)를 label/architecture 근거로 사용했고,
기존 full/exact 2개와 Linux/macOS x64/arm64 project-check 4개, 같은 좌표의 actual SQLite 4개가
모두 성공했습니다. PostgreSQL/MySQL service-only job은 지원 출처가 아니며, 해당 backend의 첫
required job은 digest-pinned service image, health check, UTC timezone과 C locale 또는 명시적으로
승인된 collation, actual query/write/transaction/schema/migration/recorder/revision-lifecycle 및
durable restart/persistence contract를 모두 실행해야 합니다. Expected contract 수와 executed 수가
같고 `skipped=0`, `continue-on-error` 없음, final clean worktree도 함께 요구합니다.

GDJ-0023 relation reference는 exact Django 6.1 commit
`fe0a859f537d4238cf49fca39073513206f83122`와 기존
`django-6.1-sqlite-darwin-arm64` profile을 사용합니다. Scenario와 fixture는 upstream
표현을 복사하지 않은 독립 작성이고 manifest 12개 provenance는 모두
`derived=false`, `license=BSD-3-Clause`입니다. Locked artifact는 manifest 10,842 bytes/
`08124b420e6313e4c2c1a5be32a3bdd29d831f02f1479bc3591af6f8f7da1522`, oracle 33,792
bytes/`6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture
1,859 bytes/`2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`입니다. 기존
11-line/1,061-byte `SHA256SUMS` prefix는 byte-for-byte 보존하고 `relation-oracle.json`을
12번째 줄로 append해 1,148 bytes/
`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`로 만들었습니다.
Relation manifest는 `oracle_locked`, static fixture는 ordered 12 `not_implemented`이며
GoDj product relation adapter나 PostgreSQL/MySQL 지원을 주장하지 않습니다.

## Python과 환경 도구

- [Python 3.14.6 release](https://www.python.org/downloads/release/python-3146/) — 2026-08-07 당시 source/profile 검토에 사용한 역사 기준.
- [Python source releases](https://www.python.org/downloads/source/)
- [uv documentation](https://docs.astral.sh/uv/)

GoDj의 local/exact reference는 계속 CPython 3.14.3으로 고정합니다. Hosted compatibility
matrix는 exact 3.12.13, 3.13.15, 3.14.3, 3.14.7 좌표를 각각 실행해 3.14.3과 더 새 micro의
차이도 관찰하지만, 어느 좌표도 부동 `latest` 별칭이나 Python 전체 지원 범위로 주장하지
않습니다. Exact reference profile은 runtime fingerprint 불일치를 실패시킵니다.

## Go

- [Go release history](https://go.dev/doc/devel/release) — Go 1.26.5 release 기록.
- [Go 1.26 announcement](https://go.dev/blog/go1.26)
- [Go language specification](https://go.dev/ref/spec) — generic type/method receiver와 type parameter 규칙.
- [Generics tutorial](https://go.dev/doc/tutorial/generics)
- [`go generate` design](https://go.dev/blog/generate) — build에 자동 포함되지 않는 별도 명령.

로컬 개발 환경은 2026-08-07 확인 시 `go1.26.5 darwin/arm64`입니다. 프로젝트의
언어 기준은 Go 1.26이고 CI toolchain은 Go 1.26.5로 고정했습니다.

CI는 Go 1.26.5를 사용하며 [actions/checkout v7.0.1](https://github.com/actions/checkout/releases/tag/v7.0.1),
[actions/setup-go v7.0.0](https://github.com/actions/setup-go/releases/tag/v7.0.0),
[astral-sh/setup-uv v9.0.0](https://github.com/astral-sh/setup-uv/releases/tag/v9.0.0)을
전체 commit SHA로 고정합니다. 실제 pin은 `.github/workflows/ci.yml`이 정본입니다.

## Go SQLite backend

- [`modernc.org/sqlite` package documentation](https://pkg.go.dev/modernc.org/sqlite) —
  CGo-free `database/sql` driver, 지원 platform과 license.
- [`modernc.org/sqlite v1.56.0` changelog](https://gitlab.com/cznic/sqlite/-/blob/v1.56.0/CHANGELOG.md) —
  내장 SQLite 3.53.3과 release 변경 기록.
- [`mattn/go-sqlite3 v1.14.49`](https://github.com/mattn/go-sqlite3/tree/v1.14.49) —
  CGO 후보 비교 근거.
- [`ncruces/go-sqlite3 v0.35.3`](https://github.com/ncruces/go-sqlite3/tree/v0.35.3) —
  CGo-free Wasm 후보 비교 근거.
- [`zombiezen/go/sqlite`](https://github.com/zombiezen/go-sqlite) —
  `database/sql`을 의도적으로 제공하지 않는 저수준 후보.
- [Go database cancellation](https://go.dev/doc/database/cancel-operations) —
  `QueryContext`/`ExecContext` cancellation 전달 기준.

2026-08-08 Go 1.26.5 darwin/arm64 비교에서 M1 기본 driver로
`modernc.org/sqlite v1.56.0`과 `modernc.org/libc v1.74.4`를 고정했습니다.
`CGO_ENABLED=0` 실행, 20ms 실행 중 cancellation의
`context.DeadlineExceeded` 분류, cancellation 뒤 연결 재사용을 확인했습니다. Go
backend SQLite 3.53.3은 Django reference SQLite 3.50.4와 별도 fingerprint입니다.
선택과 제한은 [ADR-0008](adr/0008-m1-sqlite-driver-and-execution-boundary.md)에
기록합니다.

## Codex 작업 지침

- [OpenAI 공식 AGENTS.md 문서](https://learn.chatgpt.com/docs/agent-configuration/agents-md) — Codex가 작업 전 지침 chain을 구성하고 project root에서 현재 directory까지 계층적으로 파일을 읽는 방식.

루트 `AGENTS.md`는 반복 규칙과 읽기 순서만 유지하고, 전체 설계·상태·작업 기록은 별도 문서로 분리했습니다. 하위 `AGENTS.md`는 실제 하위 시스템이 생기고 별도 규칙이 필요할 때 추가합니다.

## 2026-08-07 로컬 환경 관찰 snapshot

| 항목 | 2026-08-07 관찰값 | 호환 약속 여부 |
|---|---|---|
| Go | 1.26.5, darwin/arm64 | 언어 기준만 Accepted |
| Reference Python | uv-managed CPython 3.14.3 | exact M0 profile |
| Reference SQLite | 3.50.4 + exact source ID | exact M0 profile |
| 기본 shell Python | pyenv CPython 3.13.1 / SQLite 3.51.0 | reference가 아닌 개발 환경 관찰 |
| SQLite CLI | 3.51.0 | 개발 환경 관찰만 |
| GoDj Git branch | `main`, M1 baseline `8eac1dc` | 2026-08-07 snapshot |
| GoDj module/remote | `github.com/progresshans/godj` / `https://github.com/progresshans/godj.git` | `go.mod`와 remote 관찰 |
| GoDj SQLite | modernc driver v1.56.0 / SQLite 3.53.3 | M1 backend exact pin |

## 갱신 규칙

- reference profile 변경 시 날짜, exact version/commit, 이유, 영향을 받는 contract를 함께 기록합니다.
- 웹의 `latest` 문구보다 tag/commit과 lockfile을 우선합니다.
- drift 가능성이 있는 version·tool behavior는 새 milestone 시작 시 재확인합니다.
- upstream source에서 코드를 복사·번역하면 file-level provenance와 license notice를 실제 파생 파일 가까이에 둡니다.

## GDJ-0035 Phase A source and provenance lock

GDJ-0035 Phase A는 pinned Django 6.1 commit/profile의 migration operation/executor/recorder/schema-editor 외부
동작과 SQLite exact profile을 관찰했습니다. Exact 16-document activation head는 EVID-084/run
`31618469072`에서 hosted-verified됐고, 실제 Phase A artifact/local proof는 EVID-085에 별도 기록했습니다.
Phase A exact head는 EVID-086/run `31625898551`, Phase B no-product head는 EVID-088/run `31653237691`,
Phase C exact 8-test-only decision-proof head `7d36502...`는 EVID-090/run `32174259324`에서 각각 별도
hosted-verified됐습니다.
아래 exact objects는 조사 모음이며 manifest provenance는 각 contract가 실제 사용한 symbol만 더 좁게 가리킵니다.

조사 객체는 Django exact commit `fe0a859f537d4238cf49fca39073513206f83122`, tree
`7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`에서 `git show`로 읽습니다.

| Exact object | Blob | Bytes | Audited range/meaning |
|---|---|---:|---|
| `django/db/migrations/state.py` | `9e9cc58fae13a00178cd8ea13749a391a6e73f59` | 42,119 | 105–139, 514–536 project relation index와 resolution |
| `django/db/migrations/operations/models.py` | `1b241230df922b9bc2350858da3604c9d1b01eef` | 45,901 | 85–100 CreateModel state/database transition |
| `django/db/migrations/operations/fields.py` | `72b54382ef4902d599d7b62900cd677aac208f0c` | 12,787 | 102–121 AddField state/database transition |
| `django/db/migrations/executor.py` | `074d7b2d285fbd05a357c6789a4094ff684b8945` | 19,029 | 39–75 target plan/apply lifecycle |
| `django/db/migrations/recorder.py` | `77ad80ba62751adee2fdadd27a1ef37cb886f6a9` | 3,826 | applied-state read와 `record_applied` boundary |
| `django/db/backends/base/schema.py` | `9857eea57107c37a8c45d4aa0276ca775e70d162` | 85,400 | 516–570, 760–879 create/add/remove와 FK DDL boundary |
| `django/db/backends/sqlite3/schema.py` | `47edec8f1ccc5b8c9309a41aabac9414a4e9e079` | 20,358 | 28–44, 80–280, 302–358 constraint-check와 remake/add/remove |
| `tests/migrations/test_state.py` | `c31f8b80dd361aea32c013cdeb758323c0a65c9d` | 81,574 | 1265–1465 relation population/add/remove state |
| `tests/migrations/test_executor.py` | `10cf505d039d8c453ab6c6688555e444dafa2d19` | 39,663 | 39–75 apply/unapply lifecycle |
| `tests/schema/tests.py` | `024e68f2b36da1d3ff814ae9bdb5011324cdbc10` | 247,095 | 365–465 physical FK create/add enforcement |

- Django `AddField`/`RemoveField`/`CreateModel` operation state/DB transitions
- Django SQLite schema editor remake, deferred SQL과 constraint check 경계
- Django migration executor/recorder restart/failure 경계
- SQLite `PRAGMA foreign_keys`, `foreign_key_check`, `sqlite_schema`, `sqlite_sequence`
- Existing GoDj ADR-0010/0014/0017/0019/0020/0024의 transaction, tuple, state와 IR 불변 조건

Pinned Django 관찰과 GoDj-owned decision을 구분합니다. Relation tuple `(1,2,2,3)`, mixed digest v2,
Relation State v2, physical `NO ACTION`과 bounded remake는 Django file ABI parity가 아니라
[Accepted ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)의 GoDj-owned
decision입니다. Phase A에서 Accepted 전 생성된 candidate payload는 historical `kind=proposal`, decision ID
`GDJ-0035`, `derived=false`로 기록합니다.
Django BSD source/test reference는 위 exact object 중 실제 관찰한 부분에만 붙이고 GoDj scenario/payload는 독립적으로
작성하며 source, fixture, comment 또는 assertion 구조를 복사·번역하지 않습니다. Phase A artifact를 만들 때 exact
upstream path/test name, commit, observed symbol과 license/provenance를 artifact 가까이에 기록했습니다. Final
manifest/oracle/ordered NI/checksum은 7,792/125,248/1,846/1,245 bytes이고 SHA-256은 각각
`dfe021c22931de3383b44068cf5f6e0ecbc86aa5f8ed96cb017c60171dcb569b`,
`c742f91abee12708ef635c540578c6757470e34270e6594ad8a618f9b1afde27`,
`f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24`,
`5022a23094702463861f32270f373ba1287b609e5b3f8cb5723b74db8d69cf4f`입니다.

MIG-085에서 Django SQLite schema-editor DDL은 recorder fault 전에 commit되어 schema는 남고 migration record는
없는 경계가 관찰됐습니다. Pre-DDL fault만 완전 rollback됐습니다. 이 upstream-observed behavior와
GoDj same-transaction proposal은 서로 다른 provenance payload로 유지하며 ADR-0034 Accepted 결정을 앞당겨
주장하지 않습니다.

Phase C Proposed decision freeze와 later EVID-091-backed acceptance는 relation public constants/profile,
digest/state, wire ownership, three-stage preflight, additive existing-fence backend와 SQLite order를 동결했지만
source provenance를 바꾸는 사건이 아닙니다.
Checked-in Phase A manifest/oracle/NI/checksum의 GoDj-owned payload는 계속 historical `kind=proposal`, decision ID
`GDJ-0035`, `derived=false`이고 Django-observed payload와 합치거나 `kind=decision`으로 소급 재분류하지 않습니다.
Test-only candidate helpers, golden/hash와 private catalogs는 source/public API 정본이 아닙니다. ADR-0034 bounded
design은 Accepted입니다. EVID-091은 Proposed docs head `5bdf013...`만 증명하고 EVID-092/run
`32187094845`는 별도 acceptance head `7cdc6d6...`만 증명합니다. Later D1 definition/handoff,
D2 private state/readiness, D3a direct optional SQLite Create/Delete port는
[EVID-093](status/TEST_EVIDENCE.md#evid-20260819-093--gdj-0035-phase-d1-d2-d3a-bounded-product-slices-local-and-hosted-verification)의
각 product/correction head에서 구현·검증됐습니다. 이 product 작업은 위 upstream source/provenance
분류를 재작성하지 않으며 MIG-075..086은 계속 `oracle_locked`입니다. D3b normal loaded core integration
`74c2b72...`/`167ef03...`도
[EVID-094](status/TEST_EVIDENCE.md#evid-20260819-094--gdj-0035-phase-d3b-loaded-relation-core-integration-local-and-hosted-verification)에서
구현·검증됐지만 upstream source/provenance와 MIG status를 바꾸지 않습니다.
D4 test-only verification `424ec4d...`도
[EVID-095](status/TEST_EVIDENCE.md#evid-20260819-095--gdj-0035-phase-d4-loaded-relation-file-backed-restart-local-and-hosted-verification)에서
기존 product path의 bounded captured-snapshot restart만 검증했으므로 upstream source/provenance와 MIG status는
계속 불변입니다. EVID-096 docs head `62df9b2...`/run `32260744096`과 D4d final head `dd83362...`의
[EVID-097](status/TEST_EVIDENCE.md#evid-20260820-097--gdj-0035-d4d-bounded-nullable-foreignkey-add-local-and-hosted-verification) /
run `32271361724`도 Phase A source payload를 rewrite하거나 재분류하지 않습니다. Nullable Add는 GoDj-owned
independent implementation이며 exact capability는 `{true,true,false,false}`입니다. Required Add/Remove-remake,
general restart와 actual adapter는 미지원입니다.
