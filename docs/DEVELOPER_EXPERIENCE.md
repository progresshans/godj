# 개발 흐름

실행 가능한 시작점은 [README의 Article 예제](../README.md#article-실행)다.
아래는 현재 명령과 코드 경계를 설명한다. 제품 지원 폭은 [구현 현황](status/IMPLEMENTATION_MATRIX.md)을 따른다.
새 기능의 작성·변경·진단 편의를 평가할 때는 [개발 판단 기준](DEVELOPMENT_CRITERIA.md)을 사용한다.

설치한 도구는 `godj version` 또는 `godj version --json`으로 확인한다. 프로젝트 runtime은
공개 `version.Current()`로 자신의 GoDj dependency를 조회한다. 개발 source와 replacement의 표시 규칙은
[버전 조회](../version/README.md)를 따른다. 두 결과의 출력만으로 호환성 검증이 끝난 것은 아니다.

## 새 프로젝트

`godj startproject MODULE DIRECTORY`는 identity와 모델이 없는 `site` 앱, 선언 runner, generated runtime과
설정을 만든다. 대상의 부모 디렉터리는 있어야 하고 대상 이름은 없어야 한다. 기존 빈 디렉터리도 덮어쓰지 않는다.
고정 module에서 설치한 도구는 그 version을 사용하며 `--framework-version VERSION`으로 명시할 수도 있다.
개발 checkout은 `--framework-source PATH`로 선택한다. 설치 도구의 위치나 현재 폴더에서 dependency를 추측하지 않는다.
Compiler가 checkout에 기록한 tag/pseudo-version도 개발 source의 진단 정보이며 자동 dependency 선택에 사용하지 않는다.

README에서 만든 도구를 사용해 저장소 루트에서 실행하는 개발 예시는 다음과 같다.

```sh
"$godj_demo_dir/godj" startproject example.com/my-site ../my-site --framework-source "$PWD"
cd ../my-site
"$godj_demo_dir/godj" generate --check
"$godj_demo_dir/godj" migrations check
"$godj_demo_dir/godj" migrate
"$godj_demo_dir/godj" createsuperuser
"$godj_demo_dir/godj" runserver
```

생성 명령은 실제 generator와 runtime compile을 마친 후보를 게시한다. 기본 `site.sqlite3`의 migration과 초기 계정은
위 명시적 명령으로 만든다. `/admin/`과 `/api/identity/`는 같은 저장된 identity·권한·Session/CSRF를 사용한다.
기본 runtime은 literal loopback만 허용하며 local HTTP cookie 정책을 쓴다. 운영 network/TLS 설정은 별도 entrypoint의 책임이다.

생성된 `README.md`가 모델 선언·Admin/API 노출·DB 설정과 변경 후 명령을 설명한다. 업무 모델을 추가해도 모든 field를
자동으로 HTTP에 공개하지 않는다. `published_...` 오류는 이미 생성된 디렉터리를 의미하므로 이를 미게시로 보고 반복하지 않는다.
Source 게시와 실패 시 소유권은 [ADR-0004](adr/0004-cli-and-project-binary.md#현재-startproject-게시)를 따른다.
일반 command의 현재 사용은 아래를 따른다. 모든 프로젝트 명령은 실행 전 호환성 검사를 사용한다.
독립 고정 module 조합의 실행과 reload는 [C1](../work/0137-project-core-startup.md)에 남아 있다.
이 명령의 실제 환경별 검증은 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)를 따른다.

`godj startapp LABEL [DIRECTORY]`는 `modeldef/schema.go`와 등록 안내를 새 디렉터리에 만든다.
생략한 대상은 현재 디렉터리 아래 LABEL이다. 기존 대상은 거부하며 프로젝트 탐색·실행·DB I/O는 하지 않는다.
Label은 63자 이내의 canonical schema 이름과 portable path 규칙을 사용한다. 설치 여부는 결과의 `installed: false`로 구분한다.

Starter project에서는 `godj startapp line_items apps/line_items` 뒤 새 `modeldef`를 import하고
`modeldef/apps.go`의 `Applications()`에 그 `Definition()`을 추가한다. 기존 앱 정의를 보존한다.
이후 `generate`와 명시적 migration을 사용한다. 새 앱은 빈 상태로 등록할 수 있고 나중에 첫 모델을 추가한다.
Generated model package와 선언 package는 분리하며 Admin/API 노출과 권한은 프로젝트가 명시한다.

## 사용자 관리 명령과 check

`godj commands`는 프로젝트 명령 목록을, `godj help NAME`은 인자/option 안내를 표시한다.
`godj command NAME [ARGUMENTS]`로 실행한다. 다른 프로젝트는 NAME 앞의 `--project PATH`로 선택한다.
전역 built-in 도움말은 `godj help`이며 프로젝트나 Go executable 없이 표시할 수 있다.

Starter의 `internal/commands/commands.go`는 `count-groups`를 등록한다. 같은 설정으로 DB를 열고 generated Group
manager로 저장된 그룹 수를 읽는다. 새 명령은 [`project/command`](../project/command/README.md)의 명시적
등록과 typed 인자/option, invocation별 resource opener/cleanup을 사용한다. 목록·help는 callback이나 DB를 실행하지 않는다.
`godj check`는 선언·생성물·등록과 프로젝트의 순수 configuration check를 수행한다.
`godj check --list`로 선택 가능한 I/O 검사를 보고, `godj check --resource database`로 기존 DB의 연결/읽기를 확인한다.
추가 검사는 `command.ResourceCheck`로 등록하며 `--resource NAME`을 반복해 선택한다. 모든 선택을 먼저 검증하고,
순수 설정이나 앞선 검사가 실패하면 이후 I/O를 실행하지 않는다. DB·schema 생성과 migration은 검사에서 수행하지 않는다.
`CheckError`는 설정 field 이름을 명시적으로 표시하고 raw cause와 값은 진단에 포함하지 않는다.

`godj.toml`의 `commands_package`는 선언 runner와 다른 디렉터리를 선택한다. 명령 runtime은 generated 모델을 import할 수 있다.
전역 CLI는 선언 snapshot과 bundle을 검사하고 readonly runtime build 후 같은 bundle을 재확인한다.
두 linked executable의 [호환성 정보](../project/compatibility/README.md)를 비교한 뒤에만 명령을 실행한다.
고정 module은 실제 path/version과 contract가 같아야 하며 명시적인 개발 source는 동일 contract를 요구한다.
이 확인은 source byte 일치나 DB schema 검증을 대신하지 않는다.

Custom command는 실행 사용자의 OS/DB 권한을 사용한다. HTTP의 현재 사용자·인가·CSRF를 암묵적으로 부여하거나 생략하지 않는다.
명령의 비정상 종료·출력 실패·cleanup 실패는 이미 저장된 데이터의 rollback을 뜻하지 않으며 자동 재시도하지 않는다.
기존 generate·migration 명령은 선언 runner를, runserver는 선언/HTTP runtime을 같은 기준으로 검사한다.
초기 사용자 생성은 terminal 확인과 비밀번호 입력 전에 검사를 마친다. Entry point는 설정을 읽기 전에
`compatibility.Serve`를 호출하며 resource를 package `init`에서 열지 않는다. 고정 module 설치와 독립 Django 대조는 C1에서 계속한다.

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
`schema.Definition{AppLabel: "notifications"}`처럼 모델이 없는 앱도 선언할 수 있다. 생성된
`project.InstalledApps()`를 `settings.Definition.InstalledApps`에 전달하면 동일한 앱 이름·label을 사용한다.
목록은 caller 소유 복사이며 canonical package alias 순서다. 목록 생성 자체는 startup callback을 실행하지 않는다.
`apps.Start`에 이 registry와 명시적 hook/dependency를 전달하면 앱 초기화와 resource 종료를 연결한다.
설정과 같은 canonical label을 사용하며 [실패 정리와 종료 소유권](../apps/README.md)을 따른다.
실제 서버 시작은 [`project/server.Run`](../project/server/README.md)에서 Start/Listen/Ready와 요청 drain·앱 cleanup을 연결한다.
프로젝트는 Start에서 만든 application과 Stop을 반환한다. App lifecycle을 반환하면 요청이 끝난 뒤 같은 의존 순서로 resource를 정리한다.
나중에 첫 모델을 추가하면 같은 앱 identity에 새 schema/project snapshot을 생성한다.
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
긴 본문은 `schema.TextField("resolution", "Resolution", schema.Nullable())`로 선언한다. 저장형은 TEXT이고 generated 값은
`*string`이다. `WithResolution("")`와 `WithResolutionNull()`을 구분한다. 저장 길이 제약과 HTTP/parser 자원 한도는 별개다.
시각은 `schema.DateTimeField("due_at", "DueAt", schema.Nullable())`로 선언한다. Generated 값은 `*time.Time`이며 `WithDueAt(time.Time{})`은
연도 1의 실제 값이고 `WithDueAtNull()`과 다르다. 저장/조회는 UTC·microsecond로 정규화한다. JSON은 explicit offset이 있는 RFC3339,
Form은 offset 생략 시 UTC로 해석한다. [정밀도·범위·남은 시간 기능](adr/0061-datetime-field-and-canonical-instant-values.md)을 따른다.
기존 행이 있는 Helpdesk의 확장 예는 [새 migration과 consumer](../examples/helpdesk/README.md)에 있다.

## 관계 값을 계산하고 비교하기

Project binding의 단일 값 관계는 원 모델의 scalar와 같은 계산 AST를 사용한다. 다음은 Helpdesk 보고서의 완료와
티켓의 닫힘이 같은 행을 고르는 예다. `backend`는 실제 query backend이며 인가·현재 Category 범위는 application이 소유한다.

```go
relations, err := project.BindRelations()
if err != nil { return err }
fields := models.ServiceReportFields
same := orm.ExactExpressions(orm.F(fields.Completed), relations.ModelsServiceReport.Ticket.Closed)
source := models.ServiceReportObjects.Using(backend).Filter(same)
rows, err := orm.SelectInto(ctx, source, orm.Project1(fields.ID, func(id int64) int64 { return id }))
```

Forward FK/OneToOne과 reverse OneToOne의 finite 혼합 경로를 허용한다. 없는 optional 대상은 NULL이며
계산 비교의 NOT도 SQL 3값 의미를 따른다. `Scalar(related)`는 `Optional[T]` projection·비교·정렬을,
`Numeric(related)`는 int64/float64 집계를 연결한다. 관계를 UPDATE assignment에 넣는 것은 거부한다.

생성 query의 `Expression`, `ExpressionPredicate`, `ProjectExpressions`는 자기 project binding으로
`DynamicF("ticket__closed")` 같은 경로를 해석한다. 예를 들어
`ExpressionPredicate(orm.DynamicF("completed").Exact(orm.DynamicF("ticket__closed")))`는 위 typed 비교와 같은 AST다.
Generic 함수에서는 `BindExpressionIn`/`BindPredicateIn`/`ProjectDynamicIn`에 source와 일치하는 `BoundModel`을 전달한다.
`Decimal(operand)`는 정확한 Decimal 산술을, `DateTime(operand)`와 `Duration(operand)`는 시각 이동·시각 차이와
기간 산술을 연결한다. Collection 경로의 값·계산식은 완료된 집계 안에서 사용하며 일반 행 projection·WHERE·UPDATE의
operand로 사용하지 않는다. Dynamic 집계는 `BindAggregateExpressionIn` 또는 생성 query의 `AggregateExpression`을
사용한다. `DateTime(F(DueAt)).Date()/Time()`은 UTC 날짜/시계를 추출하고 `Date(F(ServiceOn)).ISOYear()`나
`Time(F(ServiceAt)).Second()`는 해당 값의 달력/시계 part를 계산한다.
`DateTime(F(DueAt)).TruncMonth()`는 UTC 월 시작 DateTime, `Date(F(ServiceOn)).TruncWeek()`는 월요일 Date,
`Time(F(ServiceAt)).TruncMinute()`는 초 이하를 버린 Time을 만든다. DateTime은 연·분기·월·주·일·시·분·초,
Date는 연·분기·월·주·일, Time은 시·분·초를 지원하며 입력 타입과 NULL을 유지한다. Dynamic의 같은 이름 메서드도
동일 AST를 사용한다.

`Date(...).Add(duration)`는 UTC 자정에서 이동한 DateTime을, `Time(...).Add(duration)`는 하루 안으로 정규화한 Time을
만든다. Date/DateTime 사이의 차이와 Time 사이의 차이는 Duration이며, `ScaleDuration`·`DivideDuration`은
Integer/Float/Decimal 배율과 나눗셈을 지원한다. 중간 분수를 유지하고 최종 Duration에서 half-even으로 반올림한다.
`Duration(...).Ratio(...)`는 두 기간의 비율을 finite Float으로 반환한다.

`timezone.Parse("Asia/Seoul")`로 검증한 zone을 `DateTime(F(DueAt)).InZone(zone).Date()`에 전달하면 해당 지역의 날짜를
얻는다. `InZone(zone).TruncMonth()`는 지역 월 시작에 해당하는 UTC instant를 반환한다. 생략한 시간대는 UTC이며
고정 IANA 데이터의 전체 이름/alias를 지원한다. 잘못된 이름이나 변환 범위 오류를 UTC·NULL로 대체하지 않는다.
이 명시적 조회 기능과 locale 표시·naive 입력·일반 wall-time 산술·사용자 설정 저장은 별개다.

JSON scalar 계산 비교·일반 named annotation·나머지 function/subquery/window는 아직 지원하지 않는다.
[Helpdesk 계산 조회](../examples/helpdesk/README.md)에서 실제 조립을 확인한다.
설계와 실제 환경별 검증은 [ADR-0090](adr/0090-query-update-and-scalar-expressions.md)·[TEST_EVIDENCE](status/TEST_EVIDENCE.md)를 따른다.

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
- `createsuperuser`는 명시적으로 migration한 빈 identity 저장소에 첫 active/staff/superuser를 만든다. 재시작은 raw password 없이 `OpenIdentity`를 사용한다.
  Project config의 `InitialSuperuser`와 `PasswordHasher`는 최초 생성 입력이며, 실행 중인 사용자의 role/권한을 덧씌우지 않는다.

`godj.toml`은 [예제 파일](../examples/article/godj.toml)처럼 project runner와 runserver package를 지정한다.
명령은 그 프로젝트의 schema/catalog/backend/policy를 사용한다. 임의 Python settings나 generated model package를 schema 입력으로 읽지 않는다.

Process 진입점은 [환경 snapshot](../settings/README.md)을 한 번 만든 뒤 같은 `Lookup`을 설정 파서에 전달할 수 있다.
원래 값과 존재 여부를 유지하며 DB 선택·필수 값·타입의 의미는 프로젝트 설정에서 검증한다.
값이 secret일 수 있으므로 환경 snapshot이나 DB URL의 내용을 진단으로 출력하지 않는다.

## 기존 operator 이전

Article/Helpdesk의 선언은 재사용 `identity` 앱을 포함한다. `systemstate.IdentityMigrationSources()`는 기존 system `0001`,
identity `0001`, system `0002_identity_transition`의 역사적 정의를 반환한다. Migration과 데이터 이전은 별도 명시적 작업이다.
기존 `godj_system_credential`의 operator를 사용하는 Article DB에서는 같은 DB 환경을 선택한 뒤 다음을 실행한다.

```sh
"$godj_demo_dir/godj" migrate --project examples/article/godj.toml
go run ./examples/article/cmd/adoptoperator --staff=true --superuser=false
"$godj_demo_dir/godj" runserver --project examples/article/godj.toml
```

위 예시는 기존 model 권한을 가진 staff로 이전한다. 두 role을 모두 명시해야 하며 superuser가 필요한 경우 그 값도 명시한다.
`AdoptOperator`는 기존 principal ID·username·비밀번호 해시·active·권한을 보존한다. 기존 세션 binding과 감사 행도 유지한다.
Staff/superuser는 과거 저장 형식에 없으므로 기존 permission 이름에서 추론하지 않는다. 기존 정책이 예상과 다르면 이전을 거부한다.
새 User·직접 권한·전환 기록과 옛 credential의 비활성 표시는 한 transaction으로 저장하며 옛 provisioning을 다시 활성화하지 않는다.

명령의 응답을 잃었거나 `uncertain`을 받았을 때는 같은 작업을 반복하기 전에 저장된 전환 기록을 조회한다.

```sh
go run ./examples/article/cmd/adoptoperator --inspect
```

일반 호스트는 `systemstate.AdoptOperator`와 `InspectIdentityTransition`을 사용한다. 새 DB는 `ProvisionIdentity`를 명시적으로 호출한다.
전환 후 `OpenIdentity`는 현재 User/Group/Permission을 읽는 인증기와 기존 durable session/audit 저장소를 연결한다.

`identity.NewManager(runtime, hasher, authorizer)`는 이 runtime의 native snapshot·동일 transaction·session/audit 소유권을 사용한다.
`SetPassword(ctx, actor, userID, expectedRevision, password)`로 관리자 비밀번호 교체를 수행한다. Actor는 호스트가 인증한
Principal이어야 하며, 서비스가 저장된 현재 `godj_identity.change_user`와 deny overlay를 다시 확인한다. 대상의 revision을 명시하고,
성공 때 반환하는 Profile에만 새 revision이 있다. Hash 생성은 DB 범위 밖에서 한 번, User 변경·대상 세션 폐기·감사는 같은 transaction이다.
오류에는 성공 Profile이 없으며 unknown outcome을 자동 재시도하지 않는다.

`CreateUser`에는 호스트가 선택한 불변 principal ID로 만든 `NewUserCreate`와 password를 별도로 전달한다.
`User`/`Users`는 현재 조회 또는 변경 권한을 확인하고 공개 profile을 반환한다. 단일 조회에는 그룹·직접 권한 ID도 포함된다.
`UpdateUser`는 expected revision과 명시적 `UserPatch`를 받는다. 관계 입력을 생략하면 유지하고 빈 collection을 주면 해제한다.
Profile·role·관계·감사는 하나의 transaction이며 비활성화는 대상의 현재 session도 폐기한다. Password/hash·principal ID는 patch에 없다.
`DeleteUser`에는 호스트가 생성한 전체 User relation deleter를 전달한다. 외부 모델의 CASCADE/PROTECT와 session·감사를 함께 처리하며
성공 결과는 ID·revision·삭제 건수다. 실패/unknown에는 성공 값을 반환하지 않는다.
생성은 현재 `add_user`와 `change_user`를 모두 요구하고 모든 작업은 현재 grant와 deny overlay를 다시 확인한다.
Group/Permission 관리 service와 관리 JSON API·OpenAPI·독립 Session/Bearer client도 연결했다.
[Identity Admin](../identity/admin/config.go)은 같은 Manager·현재 권한·revision·삭제 정책으로 User/Group/Permission의
Form·관리 화면·비밀번호 명령을 구성한다. 호스트의 전체 관계 graph에서 얻은 삭제 정책과 비밀번호 정책을 명시한다.

[일반 계정](../identity/account/README.md)은 login/logout Form과 자기 비밀번호 변경의 Form·Session JSON API를 제공한다.
변경은 현재 사용자에 한정하며 비밀번호·revision·현재 session 회전·다른 session 폐기·audit를 원자적으로 처리한다.
메일 reset은 key·sender·신뢰한 origin·비공개 오류 reporter를 명시해 활성화한다. Token을 서버 proof로 바꾼 뒤 현재 상태를
재검사하고 비밀번호·session 폐기·proof 정리·audit를 함께 저장하며 자동 로그인하지 않는다.
[Article 구성](../examples/article/internal/siteapp/siteapp.go)은 관리 화면/API와 일반 계정이 같은 저장 runtime과
비밀번호 정책을 사용하게 연결한다. Custom user model·JWT/OAuth/OIDC 등의 추가 인증 provider는 남은 범위다.

## Form, Admin과 API

[Admin 등록](../examples/article/adminapp/registration.go)은 model metadata, 허용 field, 권한과 persistence callback을 연결한다.
[API 구성](../examples/article/apiapp/)은 serialization·validation·authentication과 HTTP CRUD를 연결한다.
Admin 진입에는 active staff가 필요하다. Model permission, 선택적인 추가 site permission 및 Authorizer의 거부도 적용한다.
HTML과 JSON의 표현 차이는 유지하고 model 의미를 반복 선언하는 곳은 공통 metadata 경계로 모은다.

모든 field를 자동 공개하는 것이 기본 정책은 아니다. Read-only PK, 허용한 입력 field와 relationship input을 명시한다.
권한이 부족한 요청은 DB mutation 전에 거부하고, 인증/CSRF/error response에 credential이나 internal cause를 넣지 않는다.
`forms/model.NewSpecForFields`는 editable field allowlist에서 Form을 만든다. 선언 순서를 유지하고 unknown/duplicate/PK/지원하지 않는
FK 입력은 거부한다. Relation field를 입력에서 제외하고 scalar만 편집할 수 있다.
Text는 Textarea, Char는 TextInput을 기본 widget으로 사용한다. `forms/model.WithWidget`은 표시만 바꾸며 호환되지 않는 조합은
시작 시 거부한다. Optional nullable Char의 빈 입력은 Null, Text의 빈 입력은 빈 문자열이다. [상세 의미](adr/0060-text-field-and-form-widget-semantics.md)를 따른다.
`admin.ModelConfig.FormFields`는 같은 선택을 사용한다. `ReadOnly` 등록은 List/Snapshot을 요구하며 mutation callback·action을
받지 않고 mutation route를 게시하지 않는다. Get/History는 필요한 read flow에 연결한다.
List/Get/History callback은 context와 현재 인증 actor를 받으며, DB 권한 재확인은 해당 service가 소유한다.
조회에는 view 또는 change 권한을 허용하고, 인가 실행 오류를 대체 권한으로 우회하지 않는다.
CreateForm은 생성에 필요한 별도 model field 선택과 password confirmation 같은 비저장 입력을 선언한다.
Commands는 별도 Form·권한·명시적 callback으로 password 교체 같은 객체 동작을 구성한다.
RevisionField를 지정한 모델의 수정·삭제·command는 표시한 revision을 Mutation으로 전달한다.
실제 write callback은 같은 transaction에서 조건을 다시 확인한다. 충돌은 409이며 자동 재시도하지 않는다.
PasswordInput에는 저장 기본값을 둘 수 없고, 검증 실패에도 값을 다시 표시하지 않는다.
`forms.WithTrimWhitespace(false)`는 비밀번호의 공백을 보존한다. Form 값의 일반 진단은 비공개이며,
값 접근은 명시적 accessor, 값 비교는 `Value.Equal`을 사용한다.
List snapshot에는 목록 필드와 선언한 revision만 필수이며, 편집 snapshot에는 편집 필드도 필요하다. `forms/model.InitialValues`와
`admin.NewModelProjector`·`serializers.NewModelEncoder`는 generated descriptor의 typed reader와 명시적 필드 선택을 사용한다.
Admin/API projector는 시작 시 선택 metadata를 검증·복사하고 목록의 각 객체에서는 값만 변환한다.

`serializers.FromModel`은 명시적인 `ModelField` 목록에서 kind/null/default/length를 IR로부터 가져온다.
`ReadOnly`, `Optional`, `AllowEmpty` 같은 API 표현 선택은 여전히 명시한다. Auto PK는 read-only다.
Field 의미의 반복 정의를 없애면서도 model의 모든 field가 자동으로 API 입력이 되는 일을 막는다.

Identity로 이전하기 전의 legacy operator 권한을 바꿀 때는 `systemstate.UpdateOperatorPermissions(ctx, backend, expectedPolicy, permissions)`를 명시적으로 호출한다.
이 API는 같은 cooperative transaction에서 기존 정책을 비교하고 권한을 갱신하며 session을 폐기한다.
Username/password hash/ID/active는 바꾸지 않는다. 기존 runtime은 정책 불일치로 인증을 거부하므로 새 정책으로 다시 연다.
Startup이 새 권한을 자동 승인하거나 이미 admitted된 작업을 소급 취소하지 않는다.

[Helpdesk 소비자](../examples/helpdesk/app_test.go)는 Category–Ticket 관계, 선택형 Admin, API와 기존 operator의 권한 변경·명시적 identity 이전·재시작을 같은 공개 API로 연결한다.

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

각 호출의 schema와 오류를 처리한다. [Typed endpoint](../api/endpoint/README.md)의 `endpoint.New`는 같은 admission과
typed 입력·출력에서 handler와 OpenAPI operation을 함께 만든다. `endpoint.Collect`로 operation/schema를 모으고
`openapi.New`로 전체 문서를 검증한다. 수동 handler를 사용하는 경로는 `openapi.Operation`에 route·permission·query·
body·response를 명시하고 같은 authentication·permission으로 handler를 보호한다. 실제 middleware 설치와
transaction·응답 게시의 책임은 application이 유지한다.

Full 입력의 `title`은 필수이고 생략한 `published`는 false다. PATCH는 생략한 값을 바꾸지 않으며 `summary:null`과
`summary:""`를 구분한다. Read-only `id`는 입력 schema에서 빠진다. Trim 후 길이·빈 문자열 규칙은 `x-godj-normalization`에,
body·문자열 byte 제한과 query의 세부 오류 규칙은 설명에 기록한다. 일반 JSON Schema 검사만으로 모든 runtime 제한을 재현할 수 없다.
Session unsafe 요청은 session·CSRF cookie와 masked header를 함께 요구한다. Bearer로 구성한 API의 문서는 HTTP bearer만 표시한다.

타입을 여러 operation에서 공유할 때는 `openapi.NamedSchema{Name: "Ticket", Schema: ticketSchema}`를 `Config.Schemas`에
등록하고 `openapi.Ref("Ticket")`의 결과를 쓴다. 이름의 중복·미해결 참조·순환은 문서를 게시하기 전에 실패한다.
공통 오류 `GoDjAPIError`는 문서가 소유하므로 재선언하지 않는다. 구조가 같은 타입을 자동으로 합치지 않는다.

JSON negotiation을 설치하는 application은 `api.NewJSONPolicy("/api/")`로 만든 같은 policy의 `Middleware()`와
`openapi.Config.JSONPolicy`를 사용한다. Zero policy에는 406이 없다. 한 dynamic route 일부에만 적용되는 prefix는 거부한다.

Helpdesk는 `application.API(helpdesk.APIConfig{Authentication: authentication, AppendAudit: appendAudit})`로
`Routes()`와 `OpenAPI()`를 얻는다. `appendAudit`는 전달받은 transaction 안에서 기록하고 별도 commit을 하지 않는다.
티켓 CRUD·bulk·우선순위 명령, Label·연결·보고서와 집계·비용/기한 미리보기를 같은 선언에 연결한다.
현재 Category는 application이 소유하고 생성 입력에서 category/id를 받지 않는다. Helpdesk에는 기본 Accept negotiation
정책이 없으며, [각 경로의 입력·권한·실패 계약](../examples/helpdesk/README.md)을 따른다.

[실제 HTTP client 회귀](../examples/article/apiapp/openapi_test.go)는 문서와 생성·PATCH·HEAD의 응답을 대조한다.
[외부 생성 client 회귀](../api/openapi/consumertest/README.md)는 별도 module의 고정 ogen v1.24.0 Go client로
Article Bearer·Session, Helpdesk Session, Identity Bearer·Session과 Account Session의 여섯 profile을 검증한다.
생성물과 입력 문서·설정·lock의 일치도 필수다.

```sh
make api-client-dependencies
go test -count=1 ./api/openapi/consumertest
```

Session은 실제 cookie·CSRF를 사용한다. Article/Helpdesk와 Identity 관리 client는 parent가 준비한 session에서 시작하고,
Account client는 제품 login Form을 제출해 session을 만든 뒤 생성 password/reset API와 제품 logout을 사용한다.
Reset 메일 조회는 fixture만 제공하며 제품 endpoint가 아니다. 임의 generator나 다른 언어의 정수/nullable 동작은
별도 검증이 필요하다. 위 명령의 실행 위치·환경은 [검증 전략](TESTING.md)을, 문서/생성물 변경은 회귀 README의 갱신 절차를 따른다.

## PostgreSQL

공통 선택·검증·backend opener는 [settings/database](../settings/database/README.md)가 제공한다.
예제의 환경변수 이름과 명시적 선택 정책은 [databaseconfig](../examples/article/databaseconfig/config.go)가 소유한다.
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
