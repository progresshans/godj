# 구현·호환 상태표

- 마지막 갱신: 2026-08-07
- `Design`은 문서 결정, `Code`는 현재 checkout 구현, `Verified`는 기록된 실행 증거를 뜻합니다.
- `—`는 해당 검증이 아직 적용되지 않음을 뜻하며 pass가 아닙니다.

| Capability | Contract | Design | Code | Unit/Compile | Differential | Backend |
|---|---|---|---|---|---|---|
| Compatibility profile | META-001 | Accepted target; exact lock open Q-002 | Not started | — | draft | — |
| Contract/oracle harness | META-002 | Accepted direction; protocol open Q-003 | Not started | Not run | draft | SQLite reference planned |
| Schema DSL | SCH-001+ | Proposed details | Not started | Not run | Not run | — |
| Normalized Schema IR | SCH-010+ | Accepted direction | Not started | Not run | Not run | — |
| Deterministic codegen | GEN-001+ | Accepted direction | Not started | Not run | Not run | — |
| Codegen bootstrap | GEN-010 | Open Q-001 | Not started | Not run | — | — |
| Generated model/FieldSet | GEN-020+ | Proposed details | Not started | Not run | Not run | — |
| Generic Manager/QuerySet | QRY-001+ | Accepted direction | Not started | Not run | Not run | — |
| Typed predicate API | QRY-010+ | Accepted direction | Not started | Not run | Not run | — |
| Dynamic lookup API | QRY-020+ | Accepted direction | Not started | Not run | Not run | — |
| Shared Query AST | QRY-030+ | Accepted direction | Not started | Not run | Not run | — |
| QuerySet cache semantics | QRY-040+ | Open Q-007 | Not started | Not run | Not run | — |
| SQLite query execution | DB-SQLITE-001+ | Planned M1 | Not started | Not run | Not run | Not started |
| Model write lifecycle | MOD-001+ | Planned M2 | Not started | Not run | Not run | — |
| Migration engine | MIG-001+ | Planned M2 | Not started | Not run | Not run | — |
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
