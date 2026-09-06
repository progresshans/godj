# 개발 흐름

실행 가능한 시작점은 [README의 Article 예제](../README.md#article-실행)다.
아래는 현재 명령과 코드 경계를 설명한다. 제품 지원 폭은 [구현 현황](status/IMPLEMENTATION_MATRIX.md)을 따른다.

## 모델에서 query까지

[modeldef/schema.go](../examples/article/modeldef/schema.go)는 선언만 소유하고 generated model을 import하지 않는다.

```go
var Definition = schema.Definition{
    AppLabel: "godj_conformance",
    Models: []schema.Model{{
        Name: "article",
        GoName: "Article",
        Fields: []schema.Field{
            schema.CharField("title", "Title", 200),
            schema.BooleanField("published", "Published", schema.Default(false)),
            schema.CharField("summary", "Summary", 200, schema.Nullable()),
        },
    }},
}
```

`schema.Build`가 normalized IR을 만들고 declaration-owned `ProjectSpec`이 app/project package 위치를 연결한다.
생성된 FieldSet과 Manager를 사용한 query는 다음 형태다. `sqliteBackend`는 application이 연 backend다.

```go
articles, err := models.ArticleObjects.Using(sqliteBackend).
    Filter(models.ArticleFields.Title.IContains("django")).
    OrderBy(models.ArticleFields.ID.Asc()).
    All(ctx)
```

`err`를 처리하고 backend를 명시적으로 닫는다. Field/model을 잘못 조합하는 typed query는 compile 단계에서 거부하고,
동적 이름으로 만든 query도 실행 전에 metadata로 검증한다. QuerySet 복사와 결과 cache의 의미는 [CONCURRENCY](CONCURRENCY.md)를 따른다.

## 프로젝트 명령

README에서 만든 `$godj_demo_dir/godj`와 DB 환경을 같은 shell에서 사용한다.

```sh
"$godj_demo_dir/godj" generate --check --project examples/article/godj.toml
"$godj_demo_dir/godj" showmigrations --project examples/article/godj.toml
"$godj_demo_dir/godj" migrate --plan --project examples/article/godj.toml
"$godj_demo_dir/godj" sqlmigrate godj_conformance 0001_initial --project examples/article/godj.toml
```

- `generate --check`는 선언과 생성물의 drift를 확인한다. 선언 변경 뒤에는 `generate`로 현재 후보 전체를 검증·게시한다.
- `makemigrations`는 지원하는 schema difference에서 migration을 작성한다. Destructive/general custom operation을 임의로 추론하지 않는다.
- `showmigrations`는 현재 DB history의 한 snapshot이다.
- `migrate --plan`은 preview이며 그 출력으로 나중 실행을 승인하지 않는다. 실제 `migrate`는 fresh history에서 다시 계획한다.
- `sqlmigrate`는 명명된 migration의 forward SQL을 DB 없이 render한다. 데이터 실행 결과나 runtime history를 보여 주는 명령이 아니다.
- `createsuperuser`는 migrated clean system state에 operator를 한 번 만든다. 재시작은 raw password 없이 OpenExisting 경로를 사용한다.

`godj.toml`은 [예제 파일](../examples/article/godj.toml)처럼 project runner와 runserver package를 지정한다.
명령은 그 프로젝트의 schema/catalog/backend/policy를 사용한다. 임의 Python settings나 generated model package를 schema 입력으로 읽지 않는다.

## Form, Admin과 API

[Admin 등록](../examples/article/adminapp/registration.go)은 model metadata, 허용 field, 권한과 persistence callback을 연결한다.
[API 구성](../examples/article/apiapp/)은 serialization·validation·authentication과 HTTP CRUD를 연결한다.
HTML과 JSON의 표현 차이는 유지하고 model 의미를 반복 선언하는 곳은 공통 metadata 경계로 모은다.

모든 field를 자동 공개하는 것이 기본 정책은 아니다. Read-only PK, 허용한 입력 field와 relationship input을 명시한다.
권한이 부족한 요청은 DB mutation 전에 거부하고, 인증/CSRF/error response에 credential이나 internal cause를 넣지 않는다.
`forms/model.NewSpecForFields`는 editable field allowlist에서 Form을 만든다. 선언 순서를 유지하고 unknown/duplicate/PK/지원하지 않는
FK 입력은 거부한다. Relation field를 입력에서 제외하고 scalar만 편집할 수 있다.
`admin.ModelConfig.FormFields`는 같은 선택을 사용한다. `ReadOnly` 등록은 List/Snapshot을 요구하며 mutation callback·action을
받지 않고 mutation route를 게시하지 않는다. Get/History는 필요한 read flow에 연결한다.
Snapshot은 list/form에서 실제 사용하는 필드만 필수다. `forms/model.InitialValues`, `admin.ModelObject`,
`serializers.ModelValue`는 generated descriptor의 typed reader와 명시적 필드 선택으로 변환을 공유한다.

`serializers.FromModel`은 명시적인 `ModelField` 목록에서 kind/null/default/length를 IR로부터 가져온다.
`ReadOnly`, `Optional`, `AllowEmpty` 같은 API 표현 선택은 여전히 명시한다. Auto PK는 read-only다.
Field 의미의 반복 정의를 없애면서도 model의 모든 field가 자동으로 API 입력이 되는 일을 막는다.

Operator 권한을 바꿀 때는 `systemstate.UpdateOperatorPermissions(ctx, backend, expectedPolicy, permissions)`를 명시적으로 호출한다.
이 API는 같은 cooperative transaction에서 기존 정책을 비교하고 권한을 갱신하며 session을 폐기한다.
Username/password hash/ID/active는 바꾸지 않는다. 기존 runtime은 정책 불일치로 인증을 거부하므로 새 정책으로 다시 연다.
Startup이 새 권한을 자동 승인하거나 이미 admitted된 작업을 소급 취소하지 않는다.

[Helpdesk 소비자](../examples/helpdesk/app_test.go)는 Category–Ticket 관계, 선택형 Admin, API와 기존 DB의 operator 권한 변경을 같은 공개 API로 검증한다.

두 번째 모델이나 cross-app flow를 추가할 때는 새 모델이 실제로 같은 공개 경계를 사용하도록 구성하고 core 수정 없이 안 되는 지점을 확인한다.

## PostgreSQL

예제의 backend 설정은 [databaseconfig](../examples/article/databaseconfig/config.go)가 소유한다.
SQLite 환경변수를 해제하고 caller-owned URL/schema를 사용한다. URL에 있는 credential은 출력하거나 문서에 복사하지 않는다.

```sh
unset GODJ_ARTICLE_SQLITE_DATABASE
export GODJ_ARTICLE_POSTGRES_URL="$MY_POSTGRES_URL"
export GODJ_ARTICLE_POSTGRES_SCHEMA="godj_article_demo"
"$godj_demo_dir/godj" migrate --project examples/article/godj.toml
"$godj_demo_dir/godj" createsuperuser --project examples/article/godj.toml
"$godj_demo_dir/godj" runserver --project examples/article/godj.toml
```

DB/schema의 생성 권한과 isolation은 caller가 준비한다. Tests의 pinned PostgreSQL 환경과 임의 production 배포는 같은 검증이 아니다.
현재 backend 제한은 [BACKEND_MATRIX](BACKEND_MATRIX.md)에 있다.
