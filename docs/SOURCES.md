# 기준 출처와 검증 기록

- 외부 source 마지막 확인: 2026-08-26 (Asia/Seoul)
- current artifact/provenance 마지막 검토: 2026-08-31 (Asia/Seoul)
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
pointer, corrected canonical three-phase validation, per-edge COW cache와 project-private descriptor는 ADR-0033 당시의
Go-specific decision입니다. 이 translation은 exact implementation head `be6f3d4e...`의 EVID-076/run `31586910749`에서
Implemented/Verified됐습니다. Accepted ADR-0035 current reset은 observable assignment/cache 의미를 유지하면서
facade-private write descriptor 중복을 제거하고 app main generator의 current descriptor/write metadata를 직접 사용합니다.
그 current ABI는 EVID-100의 local evidence 뒤 corrected exact head의
[EVID-103](status/TEST_EVIDENCE.md#evid-20260820-103--gdj-0036-corrected-exact-head-hosted-completion)에서
hosted-verified됐습니다. GoDj는
Django source를 포팅하지 않고 result, side effect, error timing과 transaction meaning만 independent Go tests로 번역합니다.

GDJ-0033 Phase A에서 다시 고정했던 historical GoDj relation artifacts는 manifest 10,776 bytes/SHA-256
`3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`, pinned Django oracle 33,792
bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static not-implemented fixture
1,859 bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`입니다. Phase A/B/C
decision은 이 bytes를 수정하지 않았습니다.

현재 relation reference artifact는 manifest 10,770 bytes/SHA-256
`791408c2c31864217f63b15218740214e4a850997d1e2b65dbb32b41586ff25b`, 같은 pinned Django oracle 33,792
bytes/SHA-256 `6b7d138d5b0ec60da13e142117e5c9154be2864491c6e9ec63734f9b7dd08290`, static fixture 1,859
bytes/SHA-256 `2450dcb948d7418f06458359c73fa78492df59336f0ff666e11a3ca860bd9209`입니다. 이 relation triple을
처음 추가한 12-line `SHA256SUMS` prefix는 1,148 bytes/SHA-256
`067b7d8963233f215cabb86ac8e57cd5e674ad7ecac9d3373e42281136411056`이고, 현재 shared 13-line catalog는
1,245 bytes/SHA-256 `76578c225edfa6af4bf2d119f93fdcdf633cfee8ebb5a9092aa5157e5f218be1`입니다.
REL-001..012는 current product에서 모두 `passing`이고 current reset은 EVID-103에서 hosted-verified됐습니다.

PyPI Django 6.1 wheel SHA-256은
`6c132cd980c9392b06807d4ca52d72530d631dc65a85d9dacede00a780cefbbe`이며
`uv.lock`과 exact profile에 기록했습니다.

### GDJ-0050 migration-writer authority와 artifact lock

GDJ-0050 Phase A는 같은 pinned Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`에서 다음 exact
objects를 감사했습니다. Django authority는 semantic autodetection/name/dependency와 `--dry-run`/`--check` 결과이고 Python module
source byte, timestamp header/name, internal class/questioner는 GoDj parity 대상이 아닙니다.

| Exact object | Blob | Bytes | Audited meaning |
|---|---|---:|---|
| `django/db/migrations/autodetector.py` | `0c2e215fcd5a349ce561e08b7cb9ddfe20829bc7` | 90,088 | no-change, CreateModel/AddField, dependency/topology |
| `django/db/migrations/migration.py` | `2041a28780bc8f0d4e3556688fa414051dee7244` | 9,765 | leaf successor와 suggested semantic name |
| `django/db/migrations/writer.py` | `c1101b5bb012cd9a474eec54eff0793b65337d0c` | 11,933 | writer path/document boundary 관찰 |
| `django/core/management/commands/makemigrations.py` | `7f711ed7aec4ef86361fac1bb2eed7b1bceab99c` | 22,559 | dry-run/check exit와 no-write behavior |
| `tests/migrations/test_autodetector.py` | `7a66e500cb8951f0abcfed4a26c6b7e19e6af1da` | 211,969 | translated detector scenario source |
| `tests/migrations/test_commands.py` | `61336f55332844bfd372b97aa6a7b1fad6cca027` | 153,217 | translated command scenario source |

MIG-099..110 reference artifacts는 manifest 8,864 bytes/SHA-256
`75b3f485d392dceb5800b68efe267e6cb7010d873f845ecc0fa7c459b00f5d1e`, not-implemented fixture 1,876 bytes/
`b27563a864fe417df53a20092c44f169829e9798cb2e40348c8dbbdcf4715502`, oracle 25,980 bytes/
`9068d0e603d631ac8a4da5c564b1aa1037c0854a0935342e3518812bf452fd41`입니다. 이 추가 뒤 shared 21-line
`SHA256SUMS`는 1,982 bytes/SHA-256 `de1e924b10c828db95ee945ff1fa414e9ad2802fce667cffd8bbf82b16d906e5`입니다.
이는 Phase A historical lock입니다. 당시 reference는 `oracle_locked`였고 public CLI/private protocol/publication product actual은
포함되지 않았습니다. Later status 전환으로 이 세 artifact와 shared checksum의 historical bytes를 소급 rewrite하지 않습니다.

Phase D current product manifest는 9,227 bytes/SHA-256
`90bce609ffb4f771007379495629a31efbf00594dca16f9efe875005e97f1c72`, reviewed DEV-0010 sparse expectation은
7,242 bytes/SHA-256 `74617f20f72ecd5b26284ae8cffb7a1c408cdef03e0933d457beeb82f9f4718e`입니다. Exact 열아홉
result replacement는 MIG-103의 `PROTECT`, MIG-104의 digest-derived name, MIG-105/106의 flat JSON roster/output와
MIG-107의 stable GoDj error taxonomy에만 한정됩니다. MIG-107의 Phase-A source는 Django가 아니라 GoDj-owned decision
oracle이므로 DEV-0010은 그 historical taxonomy를 production code로 위장하지 않고 명시적으로 supersede합니다. Current product
분류는 MIG-099/100/101/102/108/109/110 `passing`과 MIG-103..107 Verified DEV-0010 `deviation`입니다. PostgreSQL
17.10 normal/race/CGO-disabled actual과 repository-external public module에 이어 Phase E local full `make ci`, Linux/386
compile-only, relation/archive와 current source-bound attestation recapture도 통과했습니다. Exact submitted tree
`48994a0...`는 EVID-153/CI #171 run `33280434425`의 same-head failed-job rerun 뒤 effective 41/41 jobs·464/464 steps로
corrected exact-head Hosted terminal gate를 통과했습니다.

### GDJ-0051 migration-status authority

GDJ-0051 Phase A는 같은 pinned Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, tree
`7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`에서 다음 exact objects를 감사했습니다. Django authority는 app label grouping,
app 내부 root-to-leaf list traversal과 recorded known row의 `[X]`/`[ ]` 표시에 한정합니다. Global empty output, unknown
row `[?]`, inconsistent known history rejection, revision-fenced one-read snapshot과 project cleanup/redaction은 GoDj-owned
decision입니다.

| Exact object | Blob | Bytes | Audited meaning |
|---|---|---:|---|
| `django/core/management/commands/showmigrations.py` | `e88f83f273aae8fb07253b37df650d01976a86fa` | 6,847 | app grouping, list traversal와 known applied marker |
| `django/db/migrations/loader.py` | `af2d521d893f1a657d6a8edda72ae590831a60ee` | 18,744 | graph/applied loading과 unknown row 비노출 |
| `django/db/migrations/graph.py` | `cc06dde11f61d5a0d905c2fc4f0c79220d429bcb` | 13,149 | sorted leaves와 forward plan traversal |
| `django/db/migrations/recorder.py` | `77ad80ba62751adee2fdadd27a1ef37cb886f6a9` | 3,826 | recorder 부재 시 mutation 없는 empty read |
| `tests/migrations/test_commands.py` | `61336f55332844bfd372b97aa6a7b1fad6cca027` | 153,217 | `test_showmigrations_list`와 Django-only display states |
| `tests/migrations/test_loader.py` | `ba10e7ce935a89f6a214f1c18f33a47fcc21a8c8` | 29,443 | consistency validation이 `show_list`와 별도임을 확인 |

MIG-112..115만 pinned Django source observation을 사용합니다. Upstream `test_showmigrations_list`의 exact test provenance는
그 테스트가 직접 다루는 MIG-112/113에만 붙고, repeated observation과 cross-app fixture인 MIG-114/115에는 과장해 붙이지
않습니다. MIG-111/116/117/118은 independent GoDj decision이며 Django
deviation으로 모델링하지 않습니다. Replacement/squash의 transitional `[-]`, zero-migration app heading과 incomparable
sibling의 exact Django 순서는 current contract가 아닙니다. MIG-112..115의 reference comparison은 portable result에
한정하며 in-memory loader/recorder counter는 Python source/test evidence에만 남기고 oracle payload로 게시하지 않습니다.
Durable no-mutation과 fresh-process는
SQLite/PostgreSQL product black-box proof가 별도로 소유합니다.

### GDJ-0052 targeted migration plan authority

GDJ-0052 Phase A는 같은 pinned Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, tree
`7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`의 real `MigrationExecutor.migration_plan()`만 MIG-120..122의
ordered plan authority로 사용합니다. Exact public argv, structured JSON, known-app zero, preview freshness, revision fence,
commit outcome, cleanup, private protocol과 resource/redaction은 Django CLI parity가 아니라 GoDj-owned decision입니다.

| Exact object | Blob | Bytes | Audited meaning |
|---|---|---:|---|
| `django/db/migrations/executor.py` | `074d7b2d285fbd05a357c6789a4094ff684b8945` | 19,029 | named forward/reverse와 app-zero `migration_plan()` traversal |
| `tests/migrations/test_executor.py` | `10cf505d039d8c453ab6c6688555e444dafa2d19` | 39,663 | `test_run`, `test_minimize_rollbacks_branchy`, unrelated applied migration 경계 |

MIG-122 reference graph의 `B1`과 `A3`은 비교 불가능한 reverse sibling입니다. Django exact order
`B1, A3, A2, A1`을 정렬하지 않고 보존하며, existing DEV-0002 GoDj canonical order `A3, A2, B1, A1`은 같은
membership/dependency-safe partial order를 가진 별도 bounded deviation으로 product publication에서 처리합니다. App-only,
prefix resolution, human-readable plan, SQL rendering과 upstream command presentation은 reference 범위가 아닙니다.

Phase A exact manifest/not-implemented/oracle은 각각 6,781/1,707/43,516 bytes이고 SHA-256은
`d76a42f2a0fb4daa190d03f18d18707192c8b42881b94a1462b701a9d481947b`,
`dfefb6fd6ca27e5e70dffea002fd07d801792ba7c6a83142dab18b969617bd44`,
`dc688e27a727270594b32291e8cff83e1bd929af0a0fcd6fcf9b1f706dba9a7f`입니다. 이 추가 뒤 shared 23-line
`SHA256SUMS`는 2,177 bytes/SHA-256 `00bd4d0d865ace8620bc577d84fd4198b5724360727117fd4998f0772460f331`입니다.
MIG-119..128은 이 checkpoint에서 모두 `oracle_locked`이며 product actual이나 지원 주장을 포함하지 않습니다.

### GDJ-0054 migration SQL projection authority

GDJ-0054 Phase A는 같은 pinned Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, tree
`7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`에서 실제
`Command.handle → MigrationLoader.collect_sql → Migration.apply → operation.database_forwards → collect-SQL schema editor`
경로를 감사했습니다. `MigrationExecutor`는 이 경로에 포함되지 않습니다. Django result authority는 MIG-131의
target-before state와 exactly-one forward operation order, MIG-132의 normalized SQLite CreateModel/AddField 의미에만
한정합니다. Exact argv, identity-bearing request, PostgreSQL projection, canonical raw bytes, DB-free lifecycle, error/resource/
cleanup/publication과 external project configuration은 GoDj-owned decision입니다.

| Exact object | Blob | Bytes | Audited range and meaning |
|---|---|---:|---|
| `django/core/management/base.py` | `8f2447905064bf3838a16ecee25f8e31a5feb472` | 25,059 | `BaseCommand.execute` L441–476; excluded terminal transaction wrapper owner |
| `django/core/management/commands/sqlmigrate.py` | `3c2e25eeeaff217e7bf001b5d6d45a882908d3eb` | 3,310 | `Command.execute` L34–38, `Command.handle` L40–83 |
| `django/db/migrations/loader.py` | `af2d521d893f1a657d6a8edda72ae590831a60ee` | 18,744 | `project_state` L402–411, `collect_sql` L413–433 |
| `django/db/migrations/migration.py` | `2041a28780bc8f0d4e3556688fa414051dee7244` | 9,765 | `Migration.apply` L94–137 |
| `django/db/migrations/operations/models.py` | `1b241230df922b9bc2350858da3604c9d1b01eef` | 45,901 | `CreateModel.database_forwards` L97–111 |
| `django/db/migrations/operations/fields.py` | `72b54382ef4902d599d7b62900cd677aac208f0c` | 12,787 | `AddField.database_forwards` L111–123 |
| `django/db/backends/base/schema.py` | `9857eea57107c37a8c45d4aa0276ca775e70d162` | 85,400 | `create_model` L516–549, `add_field` L760–847 |
| `django/db/backends/sqlite3/schema.py` | `47edec8f1ccc5b8c9309a41aabac9414a4e9e079` | 20,358 | SQLite `add_field` L302–331 and base delegation |
| `tests/migrations/test_commands.py` | `61336f55332844bfd372b97aa6a7b1fad6cca027` | 153,217 | `MigrateTests.test_sqlmigrate_forwards` L908–964 |

Installed Django package 파일 8개는 exact bytes/SHA-256/Git blob과 runtime method range를 함께 검증합니다. Wheel에 없는
`test_commands.py`는 checkout blob, SHA-256
`15b8ca276a4aca3237cbadb062947c1b052fa095c6425d02b4dd7dffb455bcca`와 method range로 별도 잠급니다. Raw SQL,
comments와 BEGIN/COMMIT wrapper는 reference comparison에서 제외하며 clean database와 recorder zero를 독립 확인합니다.

Phase A historical manifest/not-implemented/oracle은 각각 8,010/1,727/46,941 bytes이고 SHA-256은
`7074d37ffc5889d86374a14c528a6eeca0007c9a7789b1fc7ffbacbb2a776703`,
`217e906548e57dab1020d6fcefcfb02700e6184001bc6aed204c557236f30144`,
`fa015cb0414709d0fc66d20d34776821fc2612ddac7702f8854141deb89abc99`입니다. 이 추가 뒤 shared 24-line
`SHA256SUMS`는 2,279 bytes/SHA-256 `9d2180400e5ffd339593d11feb95fdaf9b1532eaa14fdee8cfdc2d9c88f6e71d`였습니다.
Phase D current manifest/oracle은 7,950/47,337 bytes와 SHA-256
`fb737465cabf955fced0e04f52d5d2a89b6c00a2646b3a4e339eae37d6f084b9`/
`0d51318daf8c26aa58d8f10b49234f032fcc90c147743a41ca6e0d053c2921df`, shared checksum은
2,279 bytes/SHA-256 `7aadb1328fdfccb6bcd1e817f054e60f442c79cb55ad37aec20d24d001ce1138`입니다. MIG-129..138은
deviation fixture 없이 exact ten product `passing`이며 EVID-176이 current publication 증거를 소유합니다.

## Django REST framework

- [DRF 3.18.0 release notes](https://www.django-rest-framework.org/community/release-notes/#3180) — 2026-08-07
  release와 Django 6.1 support의 공식 근거.
- [DRF 3.18.0 source tag](https://github.com/encode/django-rest-framework/tree/3.18.0) — exact commit
  `11875a38f483cea69d8ef2fd9ede6b96fb602ec4`.
- [DRF 3.18.0 packaging metadata](https://github.com/encode/django-rest-framework/blob/3.18.0/pyproject.toml) —
  Python >=3.10, Django >=5.2와 Django 6.1 classifier/test group 근거.
- [PyPI djangorestframework 3.18.0](https://pypi.org/project/djangorestframework/3.18.0/) — exact wheel
  `djangorestframework-3.18.0-py3-none-any.whl`, SHA-256
  `381fc44d3249c9565c5f723850855b734e99030eb30957a49f506d3fe11d7dcb`.
- [Authentication](https://www.django-rest-framework.org/api-guide/authentication/)과
  [Permissions](https://www.django-rest-framework.org/api-guide/permissions/) — SessionAuthentication anonymous denial
  403, authenticated unsafe-method CSRF와 explicit permission boundary.
- [Serializers](https://www.django-rest-framework.org/api-guide/serializers/) — required full update와 supplied-field-only
  partial update 의미.
- [Routers](https://www.django-rest-framework.org/api-guide/routers/) — SimpleRouter list/create/retrieve/update/
  partial_update/destroy와 default trailing slash.
- [Pagination](https://www.django-rest-framework.org/api-guide/pagination/) — explicit PageNumberPagination/page size와
  paginated response 의미.
- [Settings](https://www.django-rest-framework.org/api-guide/settings/),
  [Parsers](https://www.django-rest-framework.org/api-guide/parsers/)와
  [Renderers](https://www.django-rest-framework.org/api-guide/renderers/) — JSON-only exact reference configuration.

GDJ-0044는 기존 Django-only `uv.lock`과 18개 oracle을 재작성하지 않고
`conformance/reference/drf/uv.lock`에 DRF dependency를 격리합니다. New exact profile은 기존 Django/Python/SQLite
fingerprint를 보존하면서 별도 ID와 nested lock hash를 사용합니다. OpenAPI/browsable API/Channels는 이 source lock의
지원 주장에 포함하지 않습니다.

## HTTP Bearer authentication

- [RFC 6750](https://datatracker.ietf.org/doc/html/rfc6750) — `Authorization: Bearer`의 `1*SP`/`b64token` grammar,
  challenge와 `invalid_request`/`invalid_token`/`insufficient_scope` status 의미의 normative authority.
- [RFC 9110](https://www.rfc-editor.org/rfc/rfc9110.html) — HTTP authentication framework와 401 response의
  `WWW-Authenticate` requirement authority.
- [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html) — OAuth 2.0 deployment security의 future checklist. 이를 인용하는 것은
  GDJ-0047이 OAuth authorization server, JWT, refresh token이나 key distribution을 지원한다는 뜻이 아닙니다.

GDJ-0047 Phase A는 exact DRF 3.18.0 `TokenAuthentication`을 `Bearer` keyword로 관찰하되, raw duplicate header handling,
fixed byte cap, secret redaction, Go interface ownership과 no-fallback profile composition을 당시 Proposed ADR-0049의 GoDj
proposal provenance로 분리했습니다. DRF 관찰과 normative RFC 결과가 다르면 같은 것으로 합성하지 않고
selector/deviation 후보로 게시합니다. EVID-138의 later ADR-0049 acceptance는 Phase A manifest의 historical
`kind=proposal`, `reference=GDJ-0047`, `derived=false` provenance를 소급 변경하지 않습니다.

## GDJ-0048 Go embedding and representation authority

GDJ-0048의 facade 선택은 Django 내부 model 객체를 복사한 호환 계약이 아니라 Go public API 결정입니다. Activation 시점의
local toolchain은 `go1.26.5 darwin/arm64`, GOROOT `/opt/homebrew/Cellar/go/1.26.5/libexec`입니다.

- Local `doc/go_spec.html`의 embedded field, promoted field/method와 selector/method-set 규칙을 private alias embedding 및
  method shadowing compile prototype의 language authority로 사용합니다.
- Local standard-library `encoding/json` package documentation의 anonymous-field flattening과 `Marshaler` method precedence를
  wrapper direct JSON fail-closed 결정의 language/runtime 근거로 사용합니다.
- [Go language specification](https://go.dev/ref/spec)과
  [`encoding/json` package documentation](https://pkg.go.dev/encoding/json)은 위 local exact documentation의 공식 public
  locator입니다. Product acceptance는 moving web page가 아니라 checked Go source/compile/runtime tests에 묶습니다.

External compile comparison은 private alias embedding, explicit unwrap-only와 pointer-map sidecar를 GoDj-owned fixture로 비교했고,
raw `Save()`가 generated outer `Save(context.Context)`에 compile error 없이 가려질 수 있음을 확인했습니다. 이는 upstream source를
번역한 artifact가 아니라 ADR-0050의 planning evidence이며 product implementation/pass 주장이 아닙니다.

## GDJ-0039 query-breadth source and provenance lock

QRY-022..033은 Django exact commit `fe0a859f537d4238cf49fca39073513206f83122`, tree
`7f258820eaf4450018b5d59c3b51f5a98cbeb4ee`의 공개 QuerySet·aggregate 결과와 cursor 수명 경계를
관찰합니다. Manifest는 contract마다 실제 사용한 문서 section/test symbol만 가리키며, 아래 표는 그
reference가 속한 exact object inventory입니다.

| Exact object | Blob | Bytes | 관찰 의미 |
|---|---|---:|---|
| `docs/ref/models/querysets.txt` | `eec3ac51d359f5568d787b29706f278e59a7d1d1` | 160,722 | values/values_list, distinct, slicing, count, iterator |
| `tests/lookup/tests.py` | `9314fa05b082ecf604d1a5e07373bda8dcaef571` | 78,011 | projection field order와 filtered values_list |
| `docs/topics/db/queries.txt` | `507017f9675b12088154661cf68d6739cbdb1137` | 78,439 | QuerySet filter/order/slicing 외부 의미 |
| `django/db/models/query.py` | `ff5105748aa18fd7db9e5bcd5acf4d288b212638` | 118,105 | public QuerySet count/aggregate/iterator boundary |
| `tests/queries/tests.py` | `169ca4924af46bf4d0506c213ee50959d891c66b` | 184,540 | distinct/order/slice/count 결과 회귀 |
| `django/db/models/sql/query.py` | `22dd479d67d9ea8e20f6e5e007dab492667ff0b3` | 123,905 | aggregate source query construction 의미 |
| `docs/topics/db/aggregation.txt` | `8d8fff021127e1dcfe3d1d864e12cf15f03ae82f` | 28,701 | empty/filtered Count와 Max 결과 |
| `tests/aggregation/tests.py` | `eda328621714ca348a3cdcaa946cb10290cb7a07` | 106,211 | aggregate over sliced/filtered QuerySet |
| `django/db/models/sql/compiler.py` | `575143a7d6b22a8e83b3f190456b368267dbedfa` | 96,047 | iterator cursor close와 row delivery boundary |

ADR-0039의 Go source/result AST, top-level generic result builder, cache ownership과 backend error policy는
Django internal object ABI를 복제한 것이 아닌 GoDj-owned `kind=decision`, `derived=false` provenance입니다.
Reference scenario, fixture, expected payload와 Go product adapter도 독립 작성했으며 Django source/test의 코드,
fixture, comment 또는 assertion 구조를 복사·번역하지 않았습니다. BSD-3-Clause reference는 동작 근거와 exact
version 추적에만 사용하고 파생물 분류·고지는 `LICENSING.md`와 `NOTICE.md`를 따릅니다.

## GoDj decision provenance

Accepted [ADR-0035](adr/0035-pre-release-current-only-format-and-generated-publication.md)는 Django source가
정의하지 않는 현재 GoDj migration definition 단일 format, closed codec, canonical digest와 opaque loaded-set
publication의 정본입니다. 현재 MIG-057..064 manifest의 여덟 contract는 모두 이 ADR을 `kind=decision`,
`derived=false`로 참조합니다. Pinned Django source/test provenance는 실제 공통 동작을 관찰한 MIG-057과
MIG-064에만 별도로 기록하므로 GoDj wire 결정을 Django format claim과 섞지 않습니다. Superseded
[ADR-0019](adr/0019-versioned-migration-definition-source.md)의 compatibility tuple과 당시 artifact는 Git/EVID에
보존된 역사이지 현재 지원 형식이 아닙니다.

현재 migration-definition reference artifact는 manifest 5,151 bytes/
`b5bc2612f3cfc642397ebff779294aa1cdc1a25b675632d2c7a2e615d47ee7fa`, oracle 29,654 bytes/
`61401746ce6b01caac002e7043e0818c1eaec417e31a54a8a16450d860104410`, static fixture 1,574 bytes/
`41ec09d0aba93924fc85fc5b84168ab9124fe2422ab0d86c06228102ad4bf299`입니다. MIG-057..064는 기존
`passing`/registered product adapter 상태를 유지하며, reset은 artifact bytes와 decision provenance만 current-only로
재기준화했습니다.

Accepted [ADR-0021](adr/0021-project-linked-migration-check.md)은 Django가 정의하지 않는
GoDj `godj.toml` project selection, descriptor-v1 subset, private project-runner JSON
protocol, flat no-follow source discovery와 public exit/cancellation 의미의 정본입니다.
현재 MIG-065..074 manifest의 열 contract는 모두 ADR-0021을 `kind=decision`, `derived=false`로 참조하고,
definition format/digest를 직접 다루는 MIG-065..068과 MIG-073은 ADR-0035도 함께 참조합니다. Django
source/test provenance는 없습니다. 기존 Django-named exact profile/runner/oracle directory를 재사용하는 것은
protocol-v2 reference corpus와 checksum을 한 gate에서 유지하기 위한 것이고 Django behavior parity 주장이
아닙니다.

현재 migration-project-check reference artifact는 manifest 5,085 bytes/
`e689b37098a4b26e4faddbd7c7e8a09d9145526f2b7bd1de7fb6cd5cb139c16b`, oracle 19,971 bytes/
`8bbf10c02950181a8753a11a40a6a81e816be33d1825a8a2469655d9f65bc0aa`, static fixture 1,729 bytes/
`86e0190cc30cd4cf3cb30d882ace3b1c3e2577fd03cca6fe4684a366e7260680`입니다. MIG-065..074도 기존
`passing`/registered product adapter 상태를 유지하며, current-only rebaseline은 status/registry를 새로 전환한 사건이
아닙니다.

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

GDJ-0023 당시 relation reference는 exact Django 6.1 commit
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
당시 Relation manifest는 `oracle_locked`, static fixture는 ordered 12 `not_implemented`였고
GoDj product relation adapter나 PostgreSQL/MySQL 지원을 주장하지 않았습니다. 이는 위에 별도로 기록한 current
REL-001..012 `passing` 상태를 소급해 부정하지 않는 historical baseline입니다.

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

## Go PostgreSQL backend

- [`pgx v5.10.0` source tag](https://github.com/jackc/pgx/tree/v5.10.0) — GDJ-0038의
  `database/sql` driver bridge exact pin.
- [`pgx/stdlib v5.10.0` package documentation](https://pkg.go.dev/github.com/jackc/pgx/v5/stdlib@v5.10.0) —
  standard-library SQL adapter 경계.
- [`pgx v5.10.0` MIT license](https://github.com/jackc/pgx/blob/v5.10.0/LICENSE) —
  root `LICENSE.pgx`의 upstream source.
- [PostgreSQL 17 documentation](https://www.postgresql.org/docs/17/)와
  [17.10 release notes](https://www.postgresql.org/docs/release/17.10/) — supported major와 hosted
  reference minor.
- [PostgreSQL versioning policy](https://www.postgresql.org/support/versioning/) — supported major 안에서
  current minor를 유지하는 근거.
- [PostgreSQL identifier limits](https://www.postgresql.org/docs/17/limits.html) — default 63-byte identifier
  경계를 I/O 전에 검증하는 근거.
- [PostgreSQL 17 sequence functions](https://www.postgresql.org/docs/17/functions-sequence.html) — sequence는
  gapless counter가 아니며 failed/conflicting allocation 뒤 hole이 남을 수 있다는 restart assertion 근거.
- [Docker Engine API published-port behavior](https://github.com/moby/moby/blob/master/api/docs/v1.24.md#L634-L639) —
  ephemeral published port는 stop 때 해제되고 start/restart에서 다시 할당될 수 있으므로 service restart 뒤
  current host port를 재조회해야 한다는 CI 교정 근거.

2026-08-21 기준 GDJ-0038은 `github.com/jackc/pgx/v5 v5.10.0`을 direct runtime dependency로
고정했습니다. 지원 후보는 PostgreSQL major 17, hosted reference profile은 17.10/UTF8/C locale/UTC입니다.
로컬의 더 이른 17.x smoke는 affected implementation evidence일 뿐 reference minor를 대체하지 않습니다.
Exact 17.10 transaction/schema/migration/revision/restart hosted gate는 GDJ-0038/EVID-108에서 bounded
`Verified`됐습니다. Broader support/production readiness 제한은
[ADR-0037](adr/0037-postgresql-current-contract-backend.md)이 소유합니다.

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
[ADR-0034](adr/0034-relation-capable-migration-format-state-and-sqlite-foreign-key-ddl.md)가 역사적으로 Accepted했던
GoDj-owned decision이며 현재는 ADR-0035에 의해 Superseded됐습니다. Phase A에서 Accepted 전 생성된 candidate payload는 historical `kind=proposal`, decision ID
`GDJ-0035`, `derived=false`로 기록합니다.
Django BSD source/test reference는 위 exact object 중 실제 관찰한 부분에만 붙이고 GoDj scenario/payload는 독립적으로
작성하며 source, fixture, comment 또는 assertion 구조를 복사·번역하지 않습니다. Phase A artifact를 만들 때 exact
upstream path/test name, commit, observed symbol과 license/provenance를 artifact 가까이에 기록했습니다. Phase-A final
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
Phase-A checkout/Git history의 manifest/oracle/NI/checksum payload는 계속 historical `kind=proposal`, decision ID
`GDJ-0035`, `derived=false`이고 Django-observed payload와 합치거나 소급 재분류하지 않습니다. 현재 checked-in
same-ID diagnostic corpus는 별도 사건인 ADR-0035 current-only reset으로 재생성됐으며 manifest 7,858 bytes/
`ec90feaf988e5c014a9cc08d00f6744993af146f2e5d5c4cd86d1ed6e18f25a9`, oracle 120,502 bytes/
`5beadac7a80d0903d552e0bf9d5fae85b139ce0754d9163184d907fcf0da5968`, static fixture 1,846 bytes/
`f9bd9c47b5ab3f91e3bb2b0ca5bf4fc88c1d612caf8d6051236af6738eef9e24`입니다. 현재 manifest는 ADR-0035를
`kind=decision`, `derived=false`로 기록하지만 12 contract는 모두 `oracle_locked`이고 normal registry에
미등록이므로 이 재생성만으로 product passing/지원이 되지 않습니다. 현재 13-line shared `SHA256SUMS`는 1,245 bytes/
`76578c225edfa6af4bf2d119f93fdcdf633cfee8ebb5a9092aa5157e5f218be1`이며, 같은 크기의 Phase-A checksum
`5022a230...`과 다른 current catalog입니다.
Test-only candidate helpers, golden/hash와 private catalogs는 source/public API 정본이 아닙니다. ADR-0034 bounded
design은 역사적으로 Accepted됐고 현재는 ADR-0035에 의해 Superseded됐습니다. EVID-091은 Proposed docs head `5bdf013...`만 증명하고 EVID-092/run
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
independent implementation이며 당시 exact capability는 `{true,true,false,false}`입니다. EVID-097 docs head
`c59669c...`/run `32278555810`과 D4e final head `1d86f6e...`의
[EVID-098](status/TEST_EVIDENCE.md#evid-20260820-098--gdj-0035-d4e-bounded-required-foreignkey-add-local-and-hosted-verification) /
run `32282269755`도 reference source를 rewrite하지 않습니다. Required-empty Add 역시 GoDj-owned independent
implementation이고 당시 exact capability는 `{true,true,true,false}`였습니다. EVID-098 docs head `85f9270...` /
CI #94 run `32288383027`과 D4f final head `9d5b894...`의
[EVID-099](status/TEST_EVIDENCE.md#evid-20260820-099--gdj-0035-d4f-bounded-foreignkey-remove-by-table-remake-local-and-hosted-verification) /
CI #95 run `32294983953`도 reference source를 rewrite하지 않습니다. Bounded Remove/remake 역시 GoDj-owned
independent implementation이고 exact capability는 `{true,true,true,true}`입니다. General/arbitrary remake,
general restart와 actual adapter는 미지원입니다. D4g observer-only characterization은 reset 전에
`b80f06a...`에서 실행됐지만 publication/status sequence는 GDJ-0036에서 retire됐습니다. 현재 same-ID diagnostic
corpus와 그 Go capture는 oracle semantic comparison이나 adapter 등록 증거가 아니므로 MIG-075..086은 계속
`oracle_locked`/unregistered입니다.

## GDJ-0040 Phase A query-expression source and provenance lock

QRY-034..043은 pinned Django 6.1 commit
`fe0a859f537d4238cf49fca39073513206f83122`의 public `Q`/`QuerySet` observable behavior를 독립 scenario로
관찰합니다. GoDj는 Django의 Python `Q` object layout이나 compiler 내부 구조를 복사·호환하지 않으며,
[ADR-0040](adr/0040-composable-typed-boolean-predicates-and-article-search.md)의 Go-owned immutable tree를 별도로
구현합니다. Manifest provenance는 contract별로 실제 사용한 아래 documentation/source/test locator와 BSD-3-Clause
license를 기록합니다.

| Upstream locator | QRY | 관찰 범위 |
|---|---|---|
| `docs/topics/db/queries.txt#complex-lookups-with-q` | 034, 036, 037, 039 | `Q` OR/AND/negation과 grouped lookup 외부 동작 |
| `docs/topics/db/queries.txt#filtered-querysets-are-unique` | 040 | chained source의 독립성과 재사용 |
| `docs/ref/models/querysets.txt#icontains` | 035 | ASCII wildcard escaping과 case-insensitive containment |
| `docs/ref/models/querysets.txt#distinct` | 041 | composite predicate 뒤 distinct 결과 |
| `docs/topics/db/queries.txt#limiting-querysets` | 041 | stable order 뒤 offset/limit 결과 |
| `docs/ref/models/querysets.txt#values` | 042 | projection 밖 field를 predicate에 사용하는 결과 |
| `docs/topics/db/aggregation.txt#generating-aggregates-over-a-queryset` | 043 | filtered source의 Count/Max |
| `django/db/models/sql/query.py::Query.build_filter` | 038 | nullable negation의 `IS NOT NULL` guard 관찰 |
| `django/db/models/query_utils.py::Q._combine` | 040 | connector 결합 뒤 source/child 재사용 관찰 |
| `tests/or_lookups/tests.py::OrLookupsTests.test_filter_or` | 034 | scalar exact OR result/order |
| `tests/or_lookups/tests.py::OrLookupsTests.test_q_negated` | 036, 037 | grouped OR와 negation result |
| `tests/or_lookups/tests.py::OrLookupsTests.test_q_and` | 039 | variadic/repeated implicit AND |
| `tests/lookup/tests.py::LookupTests.test_escaping` | 035 | lookup wildcard escaping |
| `tests/lookup/tests.py::LookupTests.test_isnull_textfield` | 038 | nullable exact/`icontains`/`isnull` negation truth table |
| `tests/lookup/tests.py::LookupTests.test_values_list_filter_and_no_fields` | 042 | filtered field와 projected field 분리 |
| `tests/aggregation/tests.py::AggregateTestCase.test_multiple_aggregates` | 043 | 한 filtered source의 Count/Max 결과 |

Phase A의 GoDj-owned scenario, manifest와 payload는 upstream source, fixture, comment 또는 assertion 구조를
복사·번역하지 않았습니다. Exact manifest/oracle/ordered NI fixture는 8,135/41,264/1,715 bytes이고 SHA-256은
각각 `8ed9ef62b568a2bf4843e3136574c3d73d5571ddd4fe7f1efad0493c7300e895`,
`8b087a394b52620b84d510d6981e77171179ac3690fda738261bf64bea00583e`,
`0df907357fcab944272eb45158189e68520e3567678c57995e05c5a0feccbffb`입니다. 모든 QRY-034..043 status는
`oracle_locked`이며 이 source/provenance lock만으로 GoDj product support나 `passing`을 주장하지 않습니다.

## GDJ-0041 typed comparison and field-reference provenance

QRY-044..053도 pinned Django 6.1 commit
`fe0a859f537d4238cf49fca39073513206f83122`의 public observable만 독립 관찰합니다. GoDj의 sealed `orm.F`,
private RHS union과 backend compiler는 upstream Python object layout을 복사하지 않는 Go-owned 구현입니다.

| Upstream locator | QRY | 관찰 범위 |
|---|---|---|
| `docs/ref/models/querysets.txt#gt/#gte/#lt/#lte`와 `django/db/models/lookups.py` ordering lookup classes | 044..047 | Integer literal ordering boundary와 parameter 의미 |
| `docs/topics/db/queries.txt#complex-lookups-with-q`, `#filtered-querysets-are-unique` | 048 | range AND/negation과 predicate/source 재사용 |
| `docs/topics/db/queries.txt#filters-can-reference-fields-on-the-model` | 049, 050, 052, 053 | same-row field RHS, projection과 aggregate source |
| `django/db/models/sql/query.py::Query.build_filter` | 050, 051 | nullable LHS/RHS negation complement guard |
| `tests/expressions/tests.py::BasicExpressionsTests.test_filter_inter_attribute` | 049 | same-field exact/ordering reference |
| `tests/queries/tests.py::ExcludeTests.test_exclude_nullable_fields` | 050 | nullable field-reference exclusion result |
| `docs/ref/models/querysets.txt#values` | 052 | predicate 밖 typed projection |
| `docs/topics/db/aggregation.txt#generating-aggregates-over-a-queryset` | 053 | field-filtered Count/Max |

Phase A manifest 16,652 bytes/`90adeee098285a3b6581a3d0029c22ee115351f21483f4d704101813bbe940e3`와
신규 10 `oracle_locked` 분류는 역사 경계로 보존합니다. Current manifest는 20 `passing`, 16,592 bytes/
`a32365e72bff2f96d576dc2a6322c703c6f0cf7c277776f6b326eda47cf9de17`입니다. Oracle 87,852 bytes/
`4efa5c26f5f17c77e7ef65a0bbdb00cff72835c9a98642726bd61f5524e1ec6f`와 ordered NI fixture 2,465 bytes/
`7ab556ff1f6b77f5e1d4614d6d752cabd6f3428572558d39007e9cd15972f6c2`는 바뀌지 않았습니다. Product actual은
expected artifact를 읽지 않고 QRY-034..053 20/20·신규 10/10 zero-diff를 냅니다. Frozen source
`7f2bb223...`의 local-final과 submitted `e97a4e3...`의
[EVID-118](status/TEST_EVIDENCE.md#evid-20260824-118--gdj-0041-exact-head-hosted-completion)이 통과해 이 provenance에
묶인 bounded product는 hosted-verified됐습니다.
