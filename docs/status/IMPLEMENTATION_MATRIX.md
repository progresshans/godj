# 구현·호환 상태표

- 마지막 갱신: 2026-08-08
- `Design`은 문서 결정, `Code`는 현재 checkout 구현, `Verified`는 기록된 실행 증거를 뜻합니다.
- `—`는 해당 검증이 아직 적용되지 않음을 뜻하며 pass가 아닙니다.

| Capability | Contract | Design | Code | Unit/Compile | Differential | Backend |
|---|---|---|---|---|---|---|
| Compatibility profile | META-001 | Accepted exact profile | Implemented | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | M1/M2 oracle profile bound | Django 6.1 / SQLite 3.50.4 reference |
| Contract/oracle harness | META-002 | Accepted protocol v2 | Django M1/M2와 GoDj M1/M2 adapters implemented | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | 두 set 각각 11 `passing` | Django exact reference + Go SQLite 3.53.3 |
| Write/migration reference oracle | MOD-001..MOD-007, MIG-001..MIG-004 | Accepted compatibility contracts | Django oracle + GoDj runtime adapter + explicit not-implemented fixture | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | 11 `passing`; static fixture는 expected 11 mismatch | Django 6.1 / SQLite 3.50.4 vs Go SQLite 3.53.3 |
| Django model metadata oracle | SCH-001 | Accepted M0 observation | Django reference + generated descriptor projection implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | `passing` | Django SQLite 3.50.4 / Go SQLite 3.53.3 |
| Schema DSL | SCH-M1-001 subset | M1 scope Accepted | Auto/Char/Boolean/nullable Article + typed scalar default | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | SCH-001 `passing` through IR/codegen | — |
| Normalized Schema IR | SCH-M1-001 | [ADR-0001](../adr/0001-schema-ir-as-canonical-source.md) Accepted | Version 2 normalize/validate/canonical hash/default deep clone | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | SCH-001 `passing` | — |
| Deterministic codegen | GEN-M1-001 | ADR-0002/0006/0009 Accepted | Read model + immutable create/patch/write descriptor, hash/golden/check/last-good | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | SCH/MOD generated paths `passing` | external positive/negative compile |
| Codegen bootstrap | GEN-010 / GEN-M1-001 | [ADR-0006](../adr/0006-codegen-input-package-boundary.md) Accepted | M0 spike + production overlay compile-only replacement implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | — | missing/stale target fixtures |
| Generated model/FieldSet | GEN-M1-001 | ADR-0007/0009 Accepted for verified subset | Article/FieldSet/descriptor/Manager/create/patch binding implemented | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | QRY/SCH/MOD subset `passing` | SQLite 3.53.3 |
| Generic Manager/QuerySet | QRY-001..QRY-010 | ADR-0003/0007 Accepted for M1 | Filter/OrderBy/Limit/All implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | 10 QRY contracts `passing` | SQLite 3.53.3 |
| Typed predicate API | QRY-M1-001 | ADR-0003/0007 Accepted for M1 | exact/icontains/isnull + typed order implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | relevant QRY contracts `passing` | external negative compile |
| Dynamic lookup API | QRY-008/QRY-010 + QRY-M1-001 | [ADR-0007](../adr/0007-m1-model-runtime-and-dynamic-query-boundaries.md) Accepted for M1 | Ordered ParseDynamic + policy/error taxonomy implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | QRY-008/010 `passing` | pre-execution validation |
| Shared Query AST | QRY-M1-001 | ADR-0003 Accepted | Immutable Plan/Condition/Ordering/Value subset implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | typed/dynamic equality + QRY `passing` | SQLite compiler |
| QuerySet cache semantics | Q-007 / contract TBD | Open Q-007 | M1 intentionally uncached; final semantics not implemented | Plan/evaluation tests pass; cache contract not run | Not claimed | — |
| SQLite query execution | DB-SQLITE-001 | [ADR-0008](../adr/0008-m1-sqlite-driver-and-execution-boundary.md) Accepted for M1 | Compiler/executor/context/cleanup subset implemented | [Pass EVID-001](TEST_EVIDENCE.md#evid-20260808-001--gdj-0002-model-to-query-walking-skeleton) | QRY-001..010 `passing` | modernc v1.56.0 / SQLite 3.53.3 |
| SQLite mutation/transaction | MOD-001..MOD-007 | [ADR-0009](../adr/0009-m2-explicit-write-change-state.md) Accepted | Parameterized one-row mutation + callback-bound Atomic implemented | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | MOD-001..007 `passing` | modernc v1.56.0 / SQLite 3.53.3 |
| Model write lifecycle subset | MOD-001..MOD-007 | [ADR-0009](../adr/0009-m2-explicit-write-change-state.md) Accepted | Generated create/patch + Manager create/update/delete implemented | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | 7 `passing`; Save/dirty/bulk not claimed | SQLite 3.53.3 |
| Save lifecycle contracts | MOD-008..MOD-017 candidates | Active GDJ-0005 | Product/manifest not started | Probe not run | Not claimed | Django SQLite reference planned |
| Migration state/executor subset | MIG-001..MIG-004 | [ADR-0010](../adr/0010-m2-migration-state-and-executor-boundary.md) Accepted | ProjectState/CreateModel/AddField/Executor implemented | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | MIG-001..004 `passing` | backend-neutral core |
| SQLite migration editor/recorder | MIG-001..MIG-004 | [ADR-0010](../adr/0010-m2-migration-state-and-executor-boundary.md) Accepted | Same-transaction DDL/recorder + explicit capability errors | [Pass EVID-003](TEST_EVIDENCE.md#evid-20260808-003--gdj-0004-write-and-migration-walking-skeleton) | 4 `passing`; file/graph/lock not claimed | modernc v1.56.0 / SQLite 3.53.3 |
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
