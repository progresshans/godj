# Database Backend Matrix

- 상태: 장기 목표 Accepted, 구현 상태는 모두 Not started
- 마지막 검토: 2026-08-07

이 표는 지원 주장표가 아니라 **계획과 검증 범위**입니다. `Planned`는 동작한다는 뜻이 아닙니다.

## 제품별 계획

| Backend | 도입 단계 | 현재 상태 | 초기 역할 |
|---|---|---|---|
| SQLite | M0 reference / M1 GoDj | Not started | 빠른 conformance와 첫 ORM 수직 단면 |
| PostgreSQL | M3 | Not started | relation, locking, production-oriented semantics |
| MySQL | M9 | Not started | backend conformance |
| MariaDB | M9 | Not started | MySQL과 차이를 별도 capability로 검증 |
| Oracle | M9 | Not started | 별도 driver/CI/licensing 운영 검토 필요 |
| PostGIS | M10 | Not started | GIS 첫 production spatial target 후보 |
| SpatiaLite | M10 | Not started | SQLite spatial conformance 후보 |
| MySQL Spatial | M10 | Not started | 지원 범위 capability 기반 |
| Oracle Spatial | M10 | Not started | optional official module 가능성 검토 |

## Capability 축

각 backend는 단순 boolean 하나가 아니라 최소 다음 capability를 선언하고 conformance test와 연결합니다.

```text
RETURNING
partial / expression indexes
deferrable constraints
SELECT FOR UPDATE / NOWAIT / SKIP LOCKED
JSON / array / generated columns
window functions
DDL transaction
savepoint
rename column/index/constraint
upsert and conflict target
timezone and precision behavior
collation/case-insensitive lookup
spatial fields/lookups/aggregates
```

지원하지 않는 기능은 compile/runtime에서 구조화된 `NotSupported` 계열 오류로 드러나야 합니다. backend가 기능을 조용히 무시하거나 의미가 다른 SQL로 바꾸면 안 됩니다.

## 버전 정책

각 milestone 시작 시 DB server/client/library 버전을 정확히 pin하고 [SOURCES.md](SOURCES.md)와 CI matrix에 기록합니다. 로컬 macOS의 SQLite 버전은 개발 환경 관찰일 뿐 compatibility 약속이 아닙니다.

Backend별 verified 상태는 기능 contract와 [status/IMPLEMENTATION_MATRIX.md](status/IMPLEMENTATION_MATRIX.md)에서 관리합니다. 이 문서에는 실제 통과하지 않은 체크 표시를 추가하지 않습니다.
