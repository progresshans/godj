# 개발 흐름

실행 가능한 시작점은 [README의 Article 예제](../README.md#article-실행)다.
아래는 현재 명령과 코드 경계를 설명한다. 제품 지원 폭은 [구현 현황](status/IMPLEMENTATION_MATRIX.md)을 따른다.
새 기능의 작성·변경·진단 편의를 평가할 때는 [개발 판단 기준](DEVELOPMENT_CRITERIA.md)을 사용한다.

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

일반 정수는 `schema.IntegerField("priority", "Priority", schema.Nullable())`처럼 선언한다. 저장·계산 타입은 int64이며 nullable
model 값은 `*int64`다. generated Create/Patch의 `WithPriority(0)`과 `WithPriorityNull()`은 다른 값을 표현한다.
`schema.Default(int64(...))`는 생략한 Create 값에 적용한다. `orm.AutoField` ID는 수정 mask에 넣을 수 없고 일반 정수는 넣을 수 있다.
기존 행이 있는 Helpdesk의 확장 예는 [새 migration과 consumer](../examples/helpdesk/README.md)에 있다.

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
Snapshot은 list/form에서 실제 사용하는 필드만 필수다. `forms/model.InitialValues`와
`admin.NewModelProjector`·`serializers.NewModelEncoder`는 generated descriptor의 typed reader와 명시적 필드 선택을 사용한다.
Admin/API projector는 시작 시 선택 metadata를 검증·복사하고 목록의 각 객체에서는 값만 변환한다.

`serializers.FromModel`은 명시적인 `ModelField` 목록에서 kind/null/default/length를 IR로부터 가져온다.
`ReadOnly`, `Optional`, `AllowEmpty` 같은 API 표현 선택은 여전히 명시한다. Auto PK는 read-only다.
Field 의미의 반복 정의를 없애면서도 model의 모든 field가 자동으로 API 입력이 되는 일을 막는다.

Operator 권한을 바꿀 때는 `systemstate.UpdateOperatorPermissions(ctx, backend, expectedPolicy, permissions)`를 명시적으로 호출한다.
이 API는 같은 cooperative transaction에서 기존 정책을 비교하고 권한을 갱신하며 session을 폐기한다.
Username/password hash/ID/active는 바꾸지 않는다. 기존 runtime은 정책 불일치로 인증을 거부하므로 새 정책으로 다시 연다.
Startup이 새 권한을 자동 승인하거나 이미 admitted된 작업을 소급 취소하지 않는다.

[Helpdesk 소비자](../examples/helpdesk/app_test.go)는 Category–Ticket 관계, 선택형 Admin, API와 기존 DB의 operator 권한 변경을 같은 공개 API로 검증한다.

두 번째 모델이나 cross-app flow를 추가할 때는 새 모델이 실제로 같은 공개 경계를 사용하도록 구성하고 core 수정 없이 안 되는 지점을 확인한다.

## OpenAPI와 client

Article 개발 서버에서 Admin에 로그인한 뒤 `/api/openapi.json`을 조회한다. 문서는 Article view 권한으로 보호되며,
credential을 provision하지 않은 public-only 구성에는 이 경로가 없다. JSON 문서만 제공하고 HTML 문서 UI는 아직 없다.

[Article operation 선언](../examples/article/apiapp/openapi.go)은 실행 route와 같은 원본이며, serializer Spec에서 full/partial
요청과 응답 schema를 가져온다. 독립 구성에서는 `articleAPI.OpenAPI()`의 오류를 처리하고 반환한 `Document.Response()`를
application이 선택한 인증·권한 handler로 감싸 게시한다. Article middleware는 같은 `articleAPI.Middleware()`에서 가져온다. `Document.Bytes()`는 파일이나 client 도구에 전달할 수 있는 복사본이다.
`Document.Routes()`에는 설명한 API route만 있고 문서 조회 route를 자동 추가하지 않는다.

Model serializer를 이미 갖고 있다면 다음 공개 투영 API를 사용한다. `spec`은 실제 binding·encoding에 쓰는 같은 Spec이다.

| 용도 | 호출 |
|---|---|
| Full 입력 | `openapi.RequestSchema(spec, serializers.ModeFull)` |
| Partial 입력 | `openapi.RequestSchema(spec, serializers.ModePartial)` |
| ModelEncoder 응답 | `openapi.ModelResponseSchema(spec)` |

각 호출의 schema와 오류를 처리한다. `openapi.Operation`에 route·permission·query·body·response를 연결하고
`openapi.New`로 문서를 검증한다. Handler를 같은 authentication·permission으로 보호하고 API middleware를 구성하는 책임은
application에 있다. 문서를 만들었다고 handler에 인증이나 응답 검증이 자동 추가되지 않는다.

Full 입력의 `title`은 필수이고 생략한 `published`는 false다. PATCH는 생략한 값을 바꾸지 않으며 `summary:null`과
`summary:""`를 구분한다. Read-only `id`는 입력 schema에서 빠진다. Trim 후 길이·빈 문자열 규칙은 `x-godj-normalization`에,
body·문자열 byte 제한과 query의 세부 오류 규칙은 설명에 기록한다. 일반 JSON Schema 검사만으로 모든 runtime 제한을 재현할 수 없다.
Session unsafe 요청은 session·CSRF cookie와 masked header를 함께 요구한다. Bearer로 구성한 API의 문서는 HTTP bearer만 표시한다.

타입을 여러 operation에서 공유할 때는 `openapi.NamedSchema{Name: "Ticket", Schema: ticketSchema}`를 `Config.Schemas`에
등록하고 `openapi.Ref("Ticket")`의 결과를 쓴다. 이름의 중복·미해결 참조·순환은 문서를 게시하기 전에 실패한다.
공통 오류 `GoDjAPIError`는 문서가 소유하므로 재선언하지 않는다. 구조가 같은 타입을 자동으로 합치지 않는다.

JSON negotiation을 설치하는 application은 `api.NewJSONPolicy("/api/")`로 만든 같은 policy의 `Middleware()`와
`openapi.Config.JSONPolicy`를 사용한다. Zero policy에는 406이 없다. 한 dynamic route 일부에만 적용되는 prefix는 거부한다.

Helpdesk는 `application.API(authentication)`을 한 번 호출해 `Routes()`와 `OpenAPI()`를 얻는다.
선택 category의 bare list·nested detail과 create만 설명하며, 입력에서 category/id를 받지 않는다. 기존 Helpdesk에는
JSON negotiation middleware가 없으므로 Article의 406 정책을 그대로 붙이지 않는다.

[실제 HTTP client 회귀](../examples/article/apiapp/openapi_test.go)는 문서와 생성·PATCH·HEAD의 응답을 대조한다.
[외부 생성 client 회귀](../api/openapi/consumertest/README.md)는 별도 module의 고정 ogen v1.24.0 Go client로
Article Bearer·Session과 Helpdesk Session을 검증한다. 생성물과 입력 문서·설정·lock의 일치도 필수다.

```sh
make api-client-dependencies
go test -count=1 ./api/openapi/consumertest
```

Session은 실제 cookie·CSRF를 사용하며 parent fixture가 준비한 session에서 시작한다. SDK의 로그인 기능이나 임의 generator,
다른 언어의 정수/nullable 동작까지 검증한 범위는 아니다. 문서/생성물 변경은 위 회귀의 README 갱신 절차를 따른다.

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
