# 구현·호환 상태표

- 마지막 갱신: 2026-08-07
- `Design`은 문서 결정, `Code`는 현재 checkout 구현, `Verified`는 기록된 실행 증거를 뜻합니다.
- `—`는 해당 검증이 아직 적용되지 않음을 뜻하며 pass가 아닙니다.

| Capability | Contract | Design | Code | Unit/Compile | Differential | Backend |
|---|---|---|---|---|---|---|
| Compatibility profile | META-001 | Accepted exact profile | Implemented | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260807-002--gdj-0001-compatibility-lab) | `oracle_locked` | Django 6.1 / SQLite 3.50.4 reference |
| Contract/oracle harness | META-002 | Accepted protocol v1 | Implemented | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260807-002--gdj-0001-compatibility-lab) | 11 contracts `oracle_locked` | exact darwin/arm64 reference |
| Django model metadata oracle | SCH-001 | Accepted M0 observation | Implemented reference adapter | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260807-002--gdj-0001-compatibility-lab) | `oracle_locked` | SQLite reference |
| Schema DSL | M1 SCH IDs TBD | Proposed details | Not started | Not run | Not run | — |
| Normalized Schema IR | M1 SCH IDs TBD | Accepted direction | Not started | Not run | Not run | — |
| Deterministic codegen | M1 GEN IDs TBD | Accepted direction | Not started | Not run | Not run | — |
| Codegen bootstrap | GEN-010 | [ADR-0006](../adr/0006-codegen-input-package-boundary.md) Accepted | M0 spike implemented; production not started | [Pass EVID-002](TEST_EVIDENCE.md#evid-20260807-002--gdj-0001-compatibility-lab) | — | darwin/arm64 fixture |
| Generated model/FieldSet | M1 GEN IDs TBD | Proposed details | Not started | Not run | Not run | — |
| Generic Manager/QuerySet | QRY-001..QRY-010 | Accepted direction | Not started | Protocol tests pass; ORM tests not run | Django `oracle_locked`; GoDj explicitly `not_implemented` | SQLite reference only |
| Typed predicate API | M1 mapping TBD | Accepted direction | Not started | Not run | Not run | — |
| Dynamic lookup API | QRY-008/QRY-010 + M1 invariants | Accepted direction | Not started | Not run | Django error oracle locked | — |
| Shared Query AST | M1 internal IDs TBD | Accepted direction | Not started | Not run | Not run | — |
| QuerySet cache semantics | Q-007 / contract TBD | Open Q-007 | Not started | Not run | Not run | — |
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
