# 구현·호환 상태표

- 마지막 갱신: 2026-08-08
- `Design`은 문서 결정, `Code`는 현재 checkout 구현, `Verified`는 기록된 실행 증거를 뜻합니다.
- `—`는 해당 검증이 아직 적용되지 않음을 뜻하며 pass가 아닙니다.

| Capability | Contract | Design | Code | Unit/Compile | Differential | Backend |
|---|---|---|---|---|---|---|
| Compatibility profile | META-001 | Accepted exact profile | Implemented | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260808-002--gdj-0003-write-migration-compatibility-contracts) | M1/M2 oracle profile bound | Django 6.1 / SQLite 3.50.4 reference |
| Contract/oracle harness | META-002 | Accepted protocol v2 | Django M1/M2 and GoDj M1 adapters implemented | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260808-002--gdj-0003-write-migration-compatibility-contracts) | M1 11 `passing`; M2 11 `oracle_locked` | Django exact reference + Go SQLite M1 |
| Write/migration reference oracle | MOD-001..MOD-007, MIG-001..MIG-004 | Accepted compatibility contracts | Django reference runner + explicit GoDj `not_implemented` fixture | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260808-002--gdj-0003-write-migration-compatibility-contracts) | 11 `oracle_locked`; expected 11 mismatches | Django 6.1 / SQLite 3.50.4 reference |
| Django model metadata oracle | SCH-001 | Accepted M0 observation | Django reference + generated descriptor projection implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | `passing` | Django SQLite 3.50.4 / Go SQLite 3.53.3 |
| Schema DSL | SCH-M1-001 subset | M1 scope Accepted | Auto/Char/Boolean/nullable Article implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | SCH-001 `passing` through IR/codegen | — |
| Normalized Schema IR | SCH-M1-001 | [ADR-0001](../adr/0001-schema-ir-as-canonical-source.md) Accepted | Version 1 normalize/validate/canonical hash implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | SCH-001 `passing` | — |
| Deterministic codegen | GEN-M1-001 | ADR-0002/0006 Accepted | One-file generation, hash, golden/check/last-good implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | SCH-001 generated metadata `passing` | external consumer compile |
| Codegen bootstrap | GEN-010 / GEN-M1-001 | [ADR-0006](../adr/0006-codegen-input-package-boundary.md) Accepted | M0 spike + production overlay compile-only replacement implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | — | missing/stale target fixtures |
| Generated model/FieldSet | GEN-M1-001 | [ADR-0007](../adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) Accepted for M1 | Article/FieldSet/descriptor/Manager binding implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | QRY/SCH 11 `passing` | SQLite M1 |
| Generic Manager/QuerySet | QRY-001..QRY-010 | ADR-0003/0007 Accepted for M1 | Filter/OrderBy/Limit/All implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | 10 QRY contracts `passing` | SQLite 3.53.3 |
| Typed predicate API | QRY-M1-001 | ADR-0003/0007 Accepted for M1 | exact/icontains/isnull + typed order implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | relevant QRY contracts `passing` | external negative compile |
| Dynamic lookup API | QRY-008/QRY-010 + QRY-M1-001 | [ADR-0007](../adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) Accepted for M1 | Ordered ParseDynamic + policy/error taxonomy implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | QRY-008/010 `passing` | pre-execution validation |
| Shared Query AST | QRY-M1-001 | ADR-0003 Accepted | Immutable Plan/Condition/Ordering/Value subset implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | typed/dynamic equality + QRY `passing` | SQLite compiler |
| QuerySet cache semantics | Q-007 / contract TBD | Open Q-007 | M1 intentionally uncached; final semantics not implemented | Plan/evaluation tests pass; cache contract not run | Not claimed | — |
| SQLite query execution | DB-SQLITE-001 | [ADR-0008](../adr/0008-m1-sqlite-driver-and-execution-boundary.md) Accepted for M1 | Compiler/executor/context/cleanup subset implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | QRY-001..010 `passing` | modernc v1.56.0 / SQLite 3.53.3 |
| Model write lifecycle | MOD-001..MOD-007 | [ADR-0009](../adr/0009-m2-explicit-write-change-state.md) Accepted | Product code not started | External compile spike passed; durable gate pending | Django oracle locked; GoDj adapter `not_implemented` | SQLite product backend not started |
| Migration engine | MIG-001..MIG-004 | [ADR-0010](../adr/0010-m2-migration-state-and-executor-boundary.md) Accepted | Product code not started | Isolated runtime/race/CGO=0 spike passed; durable gate pending | Django oracle locked; GoDj adapter `not_implemented` | SQLite product backend not started |
| Relations | REL-001+ | Open Q-013 / M3 | Not started | Not run | Not run | — |
| PostgreSQL backend | DB-PG-001+ | Planned M3 | Not started | Not run | Not run | Not started |
| Web core | WEB-001+ | Long-term accepted | Not started | Not run | Not run | — |
| Forms/Auth/Admin | FRM/AUT/ADM | Long-term accepted | Not started | Not run | Not run | — |
| API | API-001+ | Profile open Q-016 | Not started | Not run | Not run | — |
| Realtime | RTM-001+ | Profile open Q-016 | Not started | Not run | Not run | — |
| MySQL/MariaDB/Oracle | DB-* | Planned M9 | Not started | Not run | Not run | Not started |
| GIS/i18n/contrib | GIS/I18N/CTR | Long-term accepted | Not started | Not run | Not run | — |

## 상태 갱신 규칙

- `Code = Implemented`는 관련 source가 현재 checkout에 있을 때만 사용합니다.
- `Unit/Compile = Pass`와 `Differential = passing`은 [TEST_EVIDENCE.md](TEST_EVIDENCE.md)의 evidence ID를 셀 또는 주석에 연결합니다.
- backend 이름만 적지 않고 exact profile을 evidence에 기록합니다.
- 기능 일부만 통과하면 행을 더 잘게 나눕니다. 부분 구현을 전체 capability의 pass로 올리지 않습니다.
- intentional deviation은 [COMPATIBILITY.md](../COMPATIBILITY.md)의 정책과 ADR 링크를 함께 남깁니다.
