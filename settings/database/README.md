# 프로젝트 DB 설정

`database.Config`는 SQLite 또는 PostgreSQL 선택을 고정한다. 생성자와 환경 해석은 연결을 열지 않으며,
`database.Open(ctx, config)`가 반환한 backend는 호출자가 invocation/runtime 종료 시 닫고 종료 오류를 처리한다.
`Config`의 복사본은 live backend를 공유하거나 소유하지 않는다.

```go
func openProjectDatabase(ctx context.Context, environment settings.Environment) (database.Backend, error) {
    fallback, err := database.SQLite("db.sqlite3")
    if err != nil {
        return nil, err
    }
    selected, err := database.FromEnvironment(environment.Lookup, database.Options{Default: fallback})
    if err != nil {
        return nil, err
    }
    return database.Open(ctx, selected)
}
```

기본 키는 `GODJ_SQLITE_DATABASE` 또는 `GODJ_POSTGRES_URL`+`GODJ_POSTGRES_SCHEMA`다.
`Options.Names`로 프로젝트의 세 키를 모두 지정할 수 있다. 서로 다른 128 bytes 이하 shell identifier를 사용한다.
호출 시점의 여러 환경 조회가 섞이지 않도록 [환경 snapshot](../README.md)의 `Lookup`을 전달한다.
DB 설정 문법은 값의 양끝 공백을 제거하며 원래 `Environment` 값은 변경하지 않는다.

- SQLite와 PostgreSQL 키가 함께 있으면 거부한다. PostgreSQL URL과 schema는 둘 다 필요하다.
- 존재하는 빈 값·잘못된 URL/schema·불완전한 PostgreSQL 설정은 `Default`로 숨기지 않는다.
- `Default`는 DB 환경 키와 `Override`가 모두 없을 때만 사용한다. 기본값이 없는 경우 명시적 선택을 요구한다.
- `Override`는 검증된 명시적 선택이며 DB 환경 키가 하나라도 있으면 충돌이다.
- Custom key 집합이 잘못됐거나 조회 함수가 nil이면 조회 전에 거부한다. 실패는 사용할 수 있는 부분 Config를 반환하지 않는다.

`SQLite`는 비어 있거나 NUL이 있는 파일/URI를 거부한다. `PostgreSQL`은 backend의
`postgres.ValidateConfig`로 명시적인 URL identity/옵션과 schema namespace를 검증한다. Driver 옵션의 값·인증서·접속과 실제 서버 profile은
`Open`의 검증 영역이다. DB/schema 생성 권한은 caller가 준비한다.

PostgreSQL URL에는 사용자·호스트·DB를 명시한다. 생략한 포트는 각 호스트에서 5432이고, 생략한 password는 빈 값이다.
Query의 host/port/user/password/database/dbname으로 URL identity를 덮거나 같은 옵션을 반복하면 거부한다.
`PG*` 환경과 사용자 홈의 기본 password/certificate 파일에서 값을 보충하지 않는다. `PGSERVICE`는 드라이버의
암묵적인 service 파일 로딩을 막기 위해 Open 전에 명시적인 오류로 거부한다. 다른 goroutine이 검사 이후 이를
바꾸더라도 강제로 비운 servicefile 경로 때문에 연결 게시 전에 실패한다. 프로세스 환경을 변경하지 않는다.

TLS 기본값은 `sslmode=verify-full`이며 hostname/인증서 검증과 함께 plaintext fallback을 허용하지 않는다.
TLS를 끄는 로컬 DB에는 `?sslmode=disable`을 명시한다. URL의 `sslcert`/`sslkey`/`sslrootcert`만 Open 시 읽고,
해당 pool의 후속 연결은 이미 읽은 설정을 사용한다. 파일 변경은 새 Open에 반영되며 기존 pool에 자동 reload하지 않는다.
Root certificate를 명시하지 않은 검증 모드는 Go의 시스템 신뢰 저장소를 사용한다. `service`, `servicefile`, `passfile`,
임의 startup parameter는 지원하지 않는다. 명시적인 TLS·인증·timeout·pgx cache 옵션과 고정 세션 profile은
[PostgreSQL 설정 결정](../../docs/adr/0037-postgresql-current-contract-backend.md#명시적-연결-입력과-소유권)을 따른다.

`MigrationSQLRenderer()`는 고정된 DB 선택에서 credential 없는 SQL profile을 만든다. Zero Config에서는 nil을
반환하므로 기존 명령의 정의 로딩·대상 검증이 먼저 이루어질 수 있다. Zero Config로 backend를 열 수는 없다.
`Open`은 nil/canceled context를 I/O 전에 거부하며, 열린 resource를 Config 안에 보관하지 않는다.

## 명시적인 읽기 검사

`Check(ctx, config)`는 선택한 DB에 실제 I/O를 수행하고 연결을 닫는다. 설정 constructor나 기본 `godj check`가
이를 자동 호출하지 않는다. Starter에서는 `godj check --resource database`로 선택한다. 누락된 SQLite 파일이나
PostgreSQL schema를 만들지 않고 migration·repair·계정 생성도 수행하지 않는다.

SQLite는 기존 일반 파일을 `mode=ro`로 열어 연결의 foreign-key 설정과 schema catalog 읽기를 확인한다.
일반 파일명과 `file:` URI, `mode=ro/rw/rwc`, `cache=private/shared`, 0~2147483647의 canonical `_busy_timeout`만
지원한다. `mode=rw/rwc`도 검사 연결은 읽기 전용이다. 경로의 symlink 뒤 `..`를 문자열 정리로 제거하지 않는다.
Memory DB, 임의 `_pragma` 초기화 SQL, custom VFS·immutable/nolock와 다른 DSN 옵션은 열기 전에 명시적으로 거부한다.
Main DB가 읽기 전용이어도 초기화 SQL은 다른 writable DB를 attach할 수 있으므로 검사가 이를 재실행하지 않는다.
동시 SQLite 사용 시의 lock/WAL sidecar 동작은 backend에 따르며, 이 검사가 다른 writer의 변경까지 막지는 않는다.

PostgreSQL은 `Open`과 같은 credential·TLS·세션 profile·schema 존재 검사를 사용하고 즉시 닫는다.
Backend의 세션 설정 외에 DDL·업무 DML을 실행하지 않는다. 두 backend 모두 현재 연결/읽기 검증이며, 전체 파일의
integrity 검사·모델과 실제 table의 일치·적용된 migration 상태나 모든 업무 table 권한을 증명하지 않는다.
프로젝트의 다른 resource 검사는 `command.ResourceCheck`로 명시적으로 등록한다.

SQLite URI의 기준은 공식 [URI filename과 읽기 전용 mode](https://www.sqlite.org/uri.html)다. DSN의 초기화 SQL은
현재 고정 driver가 소유하므로 모호하거나 안전하게 재실행할 수 없는 설정을 조용히 생략하지 않는다.

`fmt` 출력과 JSON에는 접속 값을 넣지 않는다. 오류는 field/key와 고정된 사유를 제공하며 raw driver error를
보관하지 않는다. 값 getter는 backend 연결에 필요한 명시적 접근이며 로그/HTTP 응답에 사용하지 않는다.

Article의 [프로젝트 정책](../../examples/article/databaseconfig/config.go)은 기존 환경변수 이름과
SQLite flag 충돌 규칙을 소유한다. 새 프로젝트용 SQLite fallback은 Article에 암묵적으로 적용하지 않는다.
실행 증거와 아직 끝나지 않은 Project/Core 범위는 [TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)와
[C1 작업](../../work/0137-project-core-startup.md)을 따른다.
