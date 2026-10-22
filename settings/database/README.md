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
`postgres.ValidateConfig`로 URL 형태와 schema namespace를 검증한다. Driver 옵션·인증서·접속과 실제 서버 profile은
`Open`의 검증 영역이다. DB/schema 생성 권한은 caller가 준비한다.

`MigrationSQLRenderer()`는 고정된 DB 선택에서 credential 없는 SQL profile을 만든다. Zero Config에서는 nil을
반환하므로 기존 명령의 정의 로딩·대상 검증이 먼저 이루어질 수 있다. Zero Config로 backend를 열 수는 없다.
`Open`은 nil/canceled context를 I/O 전에 거부하며, 열린 resource를 Config 안에 보관하지 않는다.

`fmt` 출력과 JSON에는 접속 값을 넣지 않는다. 오류는 field/key와 고정된 사유를 제공하며 raw driver error를
보관하지 않는다. 값 getter는 backend 연결에 필요한 명시적 접근이며 로그/HTTP 응답에 사용하지 않는다.

Article의 [프로젝트 정책](../../examples/article/databaseconfig/config.go)은 기존 환경변수 이름과
SQLite flag 충돌 규칙을 소유한다. 새 프로젝트용 SQLite fallback은 Article에 암묵적으로 적용하지 않는다.
실행 증거와 아직 끝나지 않은 Project/Core 범위는 [TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)와
[C1 작업](../../work/0137-project-core-startup.md)을 따른다.
