# GoDj

GoDj는 Django의 모델 중심 개발 경험을 Go의 정적 타입·코드 생성·명시적 I/O에 맞게 구현하는 풀스택 웹 프레임워크다.
Schema에서 ORM·Migration·Form·Admin·API를 연결한다. 현재는 개발 중이며 내부 API와 생성 형식은 변경될 수 있다.
구현 범위와 제한은 [구현 현황](docs/status/IMPLEMENTATION_MATRIX.md)에 있다.

## Article 실행

Go 버전은 [go.mod](go.mod)를 따른다. 현재 CLI는 Linux/macOS용이다. 저장소 root에서 다음을 실행한다.
임시 디렉터리에 실행 파일과 새 SQLite DB를 만들기 때문에 기존 프로젝트 DB를 수정하지 않는다.

```sh
godj_demo_dir="$(mktemp -d)"
go build -o "$godj_demo_dir/godj" ./cmd/godj
export GODJ_ARTICLE_SQLITE_DATABASE="$godj_demo_dir/article.sqlite3"
"$godj_demo_dir/godj" migrate --project examples/article/godj.toml
"$godj_demo_dir/godj" createsuperuser --project examples/article/godj.toml
"$godj_demo_dir/godj" runserver --project examples/article/godj.toml
```

`createsuperuser`는 실제 터미널에서 username/password를 입력받는다. 비밀번호를 명령 인자에 넣지 않는다.
[Article](http://127.0.0.1:8000/)과 [Admin](http://127.0.0.1:8000/admin/)을 열고, 종료는 Ctrl-C를 사용한다.
같은 DB로 다시 실행할 때는 `runserver`만 실행한다. Provisioning은 한 번만 수행하며 startup은 저장된 credential을 연다.

SQLite 선택과 PostgreSQL 환경변수를 동시에 설정하지 않는다. PostgreSQL 사용법과 명령별 흐름은
[개발 흐름](docs/DEVELOPER_EXPERIENCE.md)에 있다. 예제는 loopback 개발 서버이며 배포 설정을 대신하지 않는다.

## 코드 탐색

- [Schema 선언](examples/article/modeldef/schema.go)과 [프로젝트 설정](examples/article/cmd/projectrunner/main.go)
- [생성 model](examples/article/models/), [관계 binding](examples/article/project/)
- [Helpdesk 소비자](examples/helpdesk/app_test.go): 구조가 다른 관계 모델과 선택형 Admin·API·권한 변경
- [Article Web](examples/article/webapp/), [Admin](examples/article/adminapp/), [API](examples/article/apiapp/)
- [아키텍처](docs/ARCHITECTURE.md), [동시성과 실패](docs/CONCURRENCY.md), [테스트 실행](docs/TESTING.md)

Django는 고정된 버전의 외부 동작을 비교하는 기준이다. Python source나 서드파티 Python app의 실행 호환은 목표가 아니다.
[호환성](docs/COMPATIBILITY.md)과 [라이선스·출처](docs/LICENSING.md)를 참고한다.

## 개발 재개

[AGENTS.md](AGENTS.md)와 [CURRENT](docs/status/CURRENT.md)가 활성 작업과 다음 행동을 안내한다.
과거 작업 전체를 먼저 읽을 필요는 없다. 현재 작업의 검증 결과는 [TEST_EVIDENCE](docs/status/TEST_EVIDENCE.md)에만 기록한다.
