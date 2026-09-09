# Backend 범위

공통 AST·historical state를 유지하고 backend 차이는 capability, compiler와 schema editor가 소유한다.
아래는 구현 범위이며 현재 변경의 모든 플랫폼 PASS를 의미하지 않는다. 실행 결과는 [Evidence](status/TEST_EVIDENCE.md)에 있다.

| 기능 | SQLite | PostgreSQL |
|---|---|---|
| Driver | modernc.org/sqlite, database/sql | pgx database/sql adapter |
| Query/CRUD | current scalar/FK AST와 typed write | current scalar/FK AST와 typed write |
| Relation query | current forward/reverse, eager/prefetch | current-profile relation 경로 |
| Relation delete | supported physical FK의 PROTECT/SET_NULL | declared current backend capability 기준 |
| Migration | revision session, current Create/Delete/Add/Remove의 검증된 형태 | current schema-bound lifecycle와 해당 capability |
| SQL projection | immutable DB-free renderer | schema-bound immutable DB-free renderer |
| System state | file-backed cooperative runtime와 explicit operator | schema-bound cooperative runtime와 explicit operator |
| CGO | pure Go 경로 | pure Go 경로 |

## 현재 schema와 query 폭

Schema는 Auto primary key, Char, Boolean과 AutoField-target ForeignKey를 중심으로 구현되어 있다.
모든 Django Field, OneToOne/ManyToMany, arbitrary `to_field`, self/cyclic relation이나 범용 constraint/index migration을 지원하지 않는다.
Scalar comparison·Boolean composition·same-model field reference와 projection/aggregate는 구현한 AST 범위 안에서만 허용한다.
Scalar COUNT/MIN/MAX와 현재 관계 filter 위의 단일 COUNT(*)를 지원한다. 관계 COUNT는 원래 JOIN·Distinct·정렬·
슬라이스의 결과 행 수를 세며 eager projection이나 일반 관계 집계를 허용하지 않는다.
지원하지 않는 표현은 silent fallback이나 client-side full scan으로 바꾸지 않는다.

## SQLite 경계

FK enforcement는 connection별 설정이다. GoDj가 사용하는 raw relation/migration path는 필요한 FK 상태와 physical schema를
확인한다. FK-off 또는 out-of-band writer를 포함한 모든 process가 자동 보호된다고 주장하지 않는다.

Migration remake는 허용된 schema에서 row/null/default/PK/sequence 의미를 보존해야 한다. Preflight가 확인하지 않은
index/trigger/inbound/cyclic shape는 mutation 전에 거부한다. Migration revision metadata는 cooperating writer 사이의
freshness를 검증하며 임의 schema drift나 cutover 이전 non-cooperating ABA를 복원하지 않는다.

Raw transaction의 rollback/discard를 확인하지 못하면 Backend는 connection 하나를 보관하고 새 I/O를 terminal quarantine으로
닫는다. 이미 admitted된 작업, Close 순서와 outcome unknown은 [CONCURRENCY](CONCURRENCY.md)를 따른다.
In-memory DB의 Close 이후 복구 가능성을 durable file과 동일하게 주장하지 않는다.

## PostgreSQL 경계

현재 conformance의 서비스 이미지는 workflow에 고정한 PostgreSQL 17.10 profile을 사용한다. DB URL과 schema는 caller가
명시적으로 제공한다. Tests는 scope별 schema를 사용하고 credential-bearing 설정을 로그나 oracle에 넣지 않는다.

PostgreSQL source-bound actual은 해당 source·observer·환경에서 실제 실행한 증거다. SQLite local PASS나 옛 checked-in actual을
현재 PostgreSQL 실행으로 대체하지 않는다. 테스트 실행과 증거 생성·소비 경계는 [TESTING](TESTING.md)를 따른다.

## 다른 환경

MySQL/MariaDB/Oracle와 GIS는 장기 범위이며 현재 backend 지원이 아니다. Windows용 CLI·파일 publication·signal/PTY 동작도
Linux/macOS 결과만으로 검증됐다고 표현하지 않는다. OS/architecture/mode별 지원 주장은 실제 Hosted 결과에만 연결한다.
새 backend나 환경을 추가할 때는 전부를 한 번에 구현하지 않고 같은 external flow와 실패 의미를 수직으로 검증한다.
