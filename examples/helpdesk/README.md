# Helpdesk 소비자

Category–Ticket 관계 모델에 선택형 Form/Admin, 읽기 전용 Category Admin, 인증·CSRF API를 연결한다.
`App`은 caller가 제공한 backend를 사용한다. 테스트는 실제 migration, HTTP CRUD, 재시작과 권한 교체를 검증한다.

```sh
go test ./examples/helpdesk -count=1
go run ./cmd/godj generate --check --project examples/helpdesk/godj.toml
```

선언 runner는 생성 명령을 제공한다. `modeldef` 변경 후 같은 명령에서 `--check`를 빼면 생성물을 갱신한다.
PostgreSQL 검증은 `GODJ_TEST_POSTGRES_URL`과 명시적인 `GODJ_REQUIRE_POSTGRES=1`로 실행하며, CI의 pinned service가 소유한다.
