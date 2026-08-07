# ADR-0008: M1 SQLite backend는 modernc database/sql driver를 사용한다

- 상태: Accepted
- 날짜: 2026-08-08
- 관련 work/contract: GDJ-0002, DB-SQLITE-001, QRY-001..QRY-010
- 대체하는 ADR: 없음

## 맥락

첫 Model-to-Query 단면은 SQLite에서 실제 AST compilation, context cancellation,
resource cleanup을 검증해야 합니다. GoDj는 장기적으로 단일 binary와 교차 build를
중요하게 보지만, M1에서 자체 SQLite binding까지 구현하는 것은 범위 밖입니다.

후보는 `modernc.org/sqlite`, `mattn/go-sqlite3`, `ncruces/go-sqlite3`,
`zombiezen/go/sqlite`였습니다. `database/sql` 호환, CGO 요구, cancellation 오류,
지원 Go version, license와 binary 크기를 현재 버전에서 비교했습니다.

## 결정 기준

- public ORM이 concrete driver나 DSN에 결합하지 않을 것
- Go 1.26과 `database/sql` context API를 사용할 것
- `CGO_ENABLED=0`에서 build/test할 수 있을 것
- 실행 중 cancellation이 표준 context error로 전달되고 연결을 재사용할 수 있을 것
- row close와 backend 오류를 명시적으로 처리할 수 있을 것
- 의존 version과 license를 재현 가능하게 고정할 것

## 고려한 선택지

### `modernc.org/sqlite v1.56.0`

Go 1.25 이상, CGo-free, `database/sql`, BSD 3-Clause입니다. SQLite 3.53.3을 포함하고
context 취소가 `context.DeadlineExceeded`로 전달되는 것을 실측했습니다. Binary가
CGO 후보보다 크고 `modernc.org/libc` 의존이 있다는 비용이 있습니다.

### `mattn/go-sqlite3 v1.14.49`

성숙하고 비교 binary가 작지만 실제 실행에 CGO와 C compiler가 필요합니다. M1 기본
경로에서는 단일 binary/교차 build 제약과 맞지 않아 선택하지 않았습니다. 성능 단계의
benchmark 대조군으로 남깁니다.

### `ncruces/go-sqlite3 v0.35.3`

CGo-free `database/sql` driver지만 현재 v0이고 취소 실측 error가 표준 context error로
직접 분류되지 않아 normalization이 더 필요했습니다.

### `zombiezen/go/sqlite v1.4.2`

CGo-free지만 의도적으로 `database/sql`을 제공하지 않는 저수준 API라 M1의 공통 실행
경계를 별도로 작성해야 합니다.

## 결정

- M1 SQLite backend는 `modernc.org/sqlite v1.56.0`을 직접 의존성으로 고정하고 그
  dependency graph의 `modernc.org/libc v1.74.4`도 lock합니다.
- Driver import, driver name과 DSN 형식은 `db/sqlite` 안에 봉인합니다. `orm`은
  중립적인 `db.Queryer`/`db.Rows`만 import합니다.
- `db/sqlite.Compile`은 DB 독립 Query Plan을 parameterized SQL로 바꾸며 identifier를
  quote하고 `LIKE` wildcard를 escape합니다. 지원하지 않는 AST는 구조화된 error를
  반환합니다.
- M1 named shared-memory helper는 lifetime을 결정적으로 유지하기 위해 connection 수를
  하나로 제한합니다. 일반 production pool/pragma 정책으로 확대 해석하지 않습니다.
- Django oracle profile의 SQLite 3.50.4와 Go backend의 SQLite 3.53.3을 별도로
  기록합니다. Reference fingerprint를 Go runtime 정보로 바꾸지 않습니다.
- CI/local gate는 `CGO_ENABLED=0`, 사전 취소, 실행 중 interrupt, 취소 후 재사용,
  `Rows.Close`, Query/Close race를 검증합니다.

## 결과

M1 backend는 C compiler 없이 build되고 generic ORM의 context를 `database/sql`까지
전달합니다. Driver 차이는 `db/sqlite` 아래에 머물고 향후 다른 backend가 ORM에
역의존하지 않습니다.

현재 선택은 binary 크기 증가와 modernc dependency graph를 가져옵니다. 배포 전에는
전체 transitive dependency license notice와 platform matrix를 다시 검사해야 합니다.

## 의도적으로 결정하지 않은 것

- production connection pool, busy timeout, journal/synchronous pragma 기본값
- transaction, savepoint, schema editor와 migration DDL 정책
- SQLite version 차이가 영향을 주는 향후 Unicode/JSON/date contract
- backend별 성능 우선순위와 CGO opt-in variant
- Windows atomic generated-file replacement 의미

## 검증

- exact/ASCII icontains/isnull/AND/order/limit compiler 및 integration test
- parameter binding, identifier quote, `%`/`_`/backslash escape test
- pre-canceled `All`과 실행 중 recursive statement timeout
- cancellation 후 같은 database 재사용
- success/scan/iteration/close/backend error cleanup
- concurrent Query/Close `go test -race`
- `CGO_ENABLED=0` SQLite/GoDj conformance test
- 11-contract Django oracle differential comparison
