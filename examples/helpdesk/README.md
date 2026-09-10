# Helpdesk 소비자

Category–Ticket 관계 모델에 선택형 Form/Admin, 읽기 전용 Category Admin, 인증·CSRF API를 연결한다.
`App`은 caller가 제공한 backend를 사용한다. 테스트는 실제 migration, HTTP CRUD, 재시작과 권한 교체를 검증한다.

`GET /api/tickets/<id>/`는 `ViewTicket` 권한과 서버가 배정한 Category 범위를 확인한 뒤 티켓과 Category를
하나의 JOIN 조회로 반환한다. 응답의 `ticket`은 id/subject/details/closed/category, `category`는 id/name만 포함한다.
티켓 조회 권한에는 그 티켓의 Category 이름 조회가 포함된다. 별도 Category Admin은 `ViewCategory` 권한을 요구한다.
없는 티켓과 다른 Category의 티켓은 모두 404이며, 인증·권한 거부 시 제품 데이터 조회를 실행하지 않는다.

```sh
go test ./examples/helpdesk -count=1
go run ./cmd/godj generate --check --project examples/helpdesk/godj.toml
```

선언 runner는 생성 명령을 제공한다. `modeldef` 변경 후 같은 명령에서 `--check`를 빼면 생성물을 갱신한다.
PostgreSQL 검증은 `GODJ_TEST_POSTGRES_URL`과 명시적인 `GODJ_REQUIRE_POSTGRES=1`로 실행하며, CI의 pinned service가 소유한다.
