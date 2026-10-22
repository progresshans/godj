# Admin의 여러 행 폼

## 부모 없는 모델 집합 생성과 편집

`NewCollectionFormSet[M]`는 canonical `Project`/`Source`, typed manager와 공통 `FormConfig`, bounded
`forms.SetConfig`를 결합한다. 반환된 `ModelCollectionFormSet[M]`은 writer에 유지하고 `View()`를 같은 source의
`ModelConfig.CollectionFormSets`에 등록한다. 목록 링크와 `/<model>/collection-set/<name>/` GET/POST 폼을 제공한다.
[Helpdesk 생성](../examples/helpdesk/ticket_bulk_admin.go)은 빈 cohort를,
[Helpdesk 편집](../examples/helpdesk/ticket_edit_admin.go)은 서버 category의 전체 현재 cohort를 사용한다.
별도의 부모 객체를 만들거나 관계 선택에서 source identity를 추측하지 않는다.
[외부 Directory–Inventory 소비자](../codegen/consumertest/testdata/admincollection/runtime_test.go)는 실제 생성 FileField,
revision과 보호된 관계를 같은 공개 경로에 연결한다. 두 모델 버전의 필드/권한 차이도 같은 writer로 처리한다.

`Load`는 허용된 전체 현재 행과 이미 읽은 M2M 값의 순수 snapshot을 반환한다. 제출 ID만 조회하거나 상한에서
조용히 자르면 안 된다. Site는 active staff·모델 조회 권한과 각 View/Add/Change/Delete, 추가 권한 및 choice 권한을
먼저 확인한다. Change가 없는 기존 행은 read-only이며 Add가 있으면 새 행을 추가할 수 있다. Delete와 ORDER는
각각 명시적 정책과 권한으로 제한한다. PK·management·선택한 `RevisionField`는 read-only와 DELETE 행에도 검사한다.
새 행에는 기존 PK를 허용하지 않는다. 조회 전용 모델이나 쓰기 동작이 전혀 없는 요청은 이 편집 경로에 들어오지 않는다.

POST는 CSRF와 management/PK admission 뒤 행별 선택 key를 현재 범위에서 해석해 바인딩한다.
[검색 페이지](choices.md)는 표시용이며 제출 선택의 허용 목록이 아니다. 검색도 기존 `row_id`의 현재 cohort와
Change를 확인하며 새 행 검색은 Add가 필요하다. 같은 key라도 다른 행의 resolver 결과를 재사용하지 않는다.

`Run`은 해당 선언·호출·actor에 묶인 `CollectionSetSubmission`을 받는다. Writer의 borrowed transaction 안에서
유지한 선언의 `BindCurrent`를 호출해 같은 Form/Set 정책과 원래 입력을 현재 전체 cohort에 다시 적용한다.
Loader는 한 번 호출하며 초기 `Load`나 읽기 전용 `Validate`를 재실행하지 않는다. 순수 Form/모델 검증은 현재 값으로
반복한다. `CollectionSetCurrent.ResolveChoices`는 management·PK/cohort·revision·금지된 제어의 검사 뒤 호출한다.
행 순서가 바뀌어도 preflight의 `Current(index)`/`ChoiceKeys`를 사용한다. 제출 시 Site의 deny overlay는 넓힐 수 없고,
현재 객체 권한·DB 고유성·revision fencing·삭제 관계 정책·audit는 application이 같은 저장 scope에서 확인한다.

`bound.Set().Prepare()`의 `SavePlan` 또는 명시적 bulk writer로 신규/변경/삭제 intent를 저장한다. `ORDER`는
폼의 순서이며 모델의 위치 필드를 추측해 저장하지 않는다. Helpdesk는 ORDER만 바뀌면 모델 값과 audit를 추가하지 않는다.
`bound.Result`에 원래 행 index, create/update/delete, 실제 양수 ID를 넘겨 현재 intent와 정확히 대조한다.
미변경·read-only·삭제된 extra를 쓰기로 보고하거나 기존 행의 ID를 바꾸거나 새 ID를 생존 행과 충돌시키면 오류다.
이 검사는 rollback 가능한 transaction 내부에서 수행하고 `Run`은 확정 commit 뒤 결과를 반환한다.
결과 자체는 commit 증거가 아니며 다른 호출의 결과 재사용과 불완전한 결과는 `ErrReconciliationRequired`다.

모델 파일과 extra file 입력은 multipart의 행별 allowlist와 request 수명을 유지한다. 모델의 pending upload는
기존 `PreparedSet.SaveFiles`로 명시적으로 게시해야 `SavePlan`을 만들 수 있다. 삭제/미변경/read-only 행은 게시하지 않는다.
부분 게시·unknown outcome·뒤쪽 writer/DB 실패에서도 게시 receipt를 보존한다. 파일 게시를 DB rollback이나 자동 삭제로
감추지 않는다. 파일의 실제 모델 이름·clear·보존은 Schema IR과 공통 파일 준비가 소유한다.

확인된 입력 오류는 `RejectCollectionFormSet`의 원래 0-based 행 또는 전체 집합 진단으로 재표시한다.
DELETE로 무시될 행 오류만 반환해 유효한 집합이 되는 결과는 거부한다. Wrapped/joined 오류·실제 취소·DB/rollback/commit
불확실성은 실행 오류를 유지하며 자동 재시도하지 않는다. 확정 commit 뒤 context가 취소돼도 성공을 미확정으로 바꾸지 않는다.
Writer가 거절한 관계 key의 이전 label은 재표시 권한으로 사용하지 않으며 파일은 다시 선택하도록 안내한다.

MinForms는 0 이상이며 min/max 검사가 필수다. AbsoluteMax는 1~100, 선언한 입력 이름과 요청의 전체 값은 최대 1,024다.
URL-encoded 요청은 64 KiB·값당 4 KiB이며 multipart에는 명시한 `SiteConfig.Uploads` 정책도 적용한다.
아래의 동적 추가/제거 UI와 서버 count/field allowlist 검사를 함께 사용한다.
`SiteConfig.RenderLimits`는 template의 전체 loop/output 예산을 명시하며 0은 기존 기본값이다. 큰 선택 목록을
여러 행에 표시하는 소비자는 `web.Config.MaxResponseBytes`도 같은 출력 정책에 맞춰 설정해야 한다.

## 부모와 자식의 inline 편집

`NewInline[P, C]`로 canonical project FK, 양 typed manager, 부모 생성자, 자식 Form과 bounded SetConfig를 선언한다.
반환된 `ModelInline[P, C]`을 저장 쪽에도 유지하고 `View()`를 부모의 `ModelConfig.Inlines`에 넣는다.
부모의 등록 타입은 별도 조회 record여도 된다. 저장 모델의 metadata와
app/model identity는 canonical 부모와 일치해야 한다. 실제 예제는 [Helpdesk](../examples/helpdesk/admin_inlines.go)에 있다.

Site는 부모의 Add/Change/View와 별도로 각 자식의 View/Add/Change/Delete를 확인한다. 권한은 인증된 principal에 대한
Site authorizer의 deny overlay를 포함한다. 저장된 자식은 View 또는 Change가 있을 때만 읽는다. 부모가 새 객체이면
기존 자식을 조회하지 않는다. 자식 choice 권한도 조회 전에 확인한다. Loader는 서버 부모·scope의 현재 집합을 상한까지
조회하고 초과를 명시적으로 거부해야 한다. 조용히 일부 행만 반환하면 전체 cohort 검증을 대신할 수 없다.

기존 행의 Change가 없으면 `ReadOnlyInitial`로 일반 필드를 서버 값에 고정하고 HTML에서 해당 fieldset을 disabled로
표시한다. PK와 명시적으로 허용한 DELETE는 별도 제어다. Add가 있으면 추가 행은 편집할 수 있다. Add가 없는 변경된
extra, Delete가 없는 삭제 제어, Change가 없는 ORDER 제출은 거부한다. 모든 경우에 parent/PK/management 검사를 유지한다.
조회만 가능한 부모 상세 화면에서도 조회가 허용된 자식은 표시한다. PasswordInput에는 initial/제출 값을 출력하지 않는다.

부모 `Create`와 `Update`는 필수 인자로 `InlineSubmission`을 받는다. Inline이 없는 소비자는 그 인자를 사용하지 않아도 된다.
`Lookup(prefix)`는 원래 `forms.Data`와 해당 요청에서 허용한 `InlineAccess`를 반환하며, `FormSet(prefix)`는 읽기용 검증 결과를
반환한다. 이 값은 Site의 registration/parent binding에 묶이며 다른 등록·부모의 값이나 unbound/invalid 결과를 저장할 수 없다.
검증 결과만으로 DB 쓰기 권한이나 commit을 증명하지 않는다.

`ModelInline.BindCurrent`는 등록된 부모의 writer에서 받은 `InlineSubmission`과 현재 principal로 같은 Form·Set·
canonical 관계 정책을 재사용한다. 다른 선언이나 actor의 입력, 등록 writer의 검사를 통과하지 않은 입력은
거부한다. 제출 시 허용한 동작과 현재 모델/choice 권한을 확인한 뒤 전달한 loader를 한 번 호출한다.
Loader는 writer가 소유한 session으로 현재 parent·전체 child cohort를 읽어 `InlineCurrent`를 반환한다.
`ResolveChoices`는 parent·management·PK/cohort admission 뒤 같은 borrowed scope 안에서 동기 호출한다.
순서가 바뀐 행도 preflight의 `Current(index)`로 확인하며 각 행에 허용된 선택을 `RowModelChoices`로 따로 반환한다.
Admission이 실패하면 선택 조회를 실행하지 않는다.
초기 화면의 `Load`나 읽기용 `Validate`는 다시 호출하지 않으므로 저장 검사가 별도 DB 연결로 빠지지 않는다.

```go
bound, present, err := reportInline.BindCurrent(ctx, currentPrincipal, submission,
    func(work context.Context, access admin.InlineAccess) (admin.InlineCurrent[Ticket, Report], error) {
        // 같은 borrowed session에서 허용된 현재 행과 선택을 읽는다.
        return loadCurrentReports(work, session, parentID, access)
    })
```

`bound.Set()`에는 현재 값으로 다시 만든 typed `InlineSet`이 있으며 순수 Form/모델 검증의 오류도 유지한다.
`bound.Access()`는 제출 때의 deny overlay를 포함한 허용 범위다. 새로 얻은 권한으로 이 범위를 넓히지 않는다.
현재 parent key·cohort와 child revision은 원래 입력과 다시 대조한다. 현재 선택은
`InlineCurrent.ResolveChoices`에서 조회한다. 누락한 행/필드는 빈 허용 집합이며 다른 행이나 새 행 prototype의
선택으로 대체하지 않는다. 검색 결과 한 페이지만으로 제출한 key 전체를 검증하지 않는다.
현재 DB의 고유성·객체 정책·revision fencing·저장과 audit는 writer가 계속 소유한다.

저장 callback은 다음 작업을 하나의 transaction에서 수행해야 한다.

1. 인증된 principal과 제출 시 허용한 동작을 유지하고, 현재 parent/category·자식 cohort·관계와 필요한 revision을 다시 확인한다.
2. 현재 snapshot으로 같은 입력을 다시 바인딩해 부모·자식 Form/고유성·DB 제약을 검증한다.
   자식은 유지한 `ModelInline.BindCurrent`를 사용해 Form·Set·관계 구성을 다시 선언하지 않는다.
3. 새 부모를 먼저 저장하고 `InlineSet.PrepareWithParent`로 받은 key를 자식 준비에 연결한다.
4. 해당 모델의 삭제 정책, 모든 scalar/collection 변경, 감사 기록을 같은 borrowed session에서 처리한다.
5. 뒤의 실패는 outer callback 밖으로 반환한다. 부모나 개별 자식마다 commit하거나 실패한 작업을 자동 재시도하지 않는다.

실제로 자식이 달라졌으면 `Update`의 changed 목록에 inline prefix를 포함한다. 이 이름은 Admin의 semantic change/audit
field이며, 부모에 RevisionField가 있으면 child-only 변경도 revision을 올리는 계약에 참여한다. 자식의 optional RevisionField는
제외된 nonnullable integer여야 한다. HTML의 `expected_revision`은 readonly/DELETE 행에서도 현재 snapshot과 비교한다.
최종 writer는 transaction 안에서도 그 조건을 다시 검사해야 한다. UI의 비교가 write-time fencing을 대신하지 않는다.

읽기 전용 `InlineConfig.Validate`와 transaction이 확실히 rollback된 writer는 `RejectInline(prefix, index, errors, cause)`로
입력 진단을 재표시할 수 있다. `index == -1`은 전체 집합이다. 권한·cohort·protected delete처럼 DELETE로 숨길 수 없는 진단은
전체 집합 또는 operation error로 전달한다. Wrapped/joined rejection, context 취소, DB/rollback 실패나 unknown outcome은
성공 또는 입력 진단으로 축소하지 않는다. 다른 inline/없는 행을 지목한 오류도 실행 오류다.

## 동적 행 추가와 제거

Add 권한이 있는 inline은 서버가 만든 빈 행을 비활성 `template`에 담는다. `ExtraForms: 0`이어도 새 행을 추가할 수 있다.
빈 행은 기본값과 서버 부모를 유지하며 기존 자식의 PK·revision·입력값을 복제하지 않는다. 새 부모의 FK는 저장 전까지 빈 값이다.
기존 readonly 행과 독립적으로 새 행을 편집한다. Add가 없으면 빈 행과 추가/제거 버튼을 게시하지 않는다.

Site가 제공하는 버전별 외부 JavaScript는 `min(MaxForms, AbsoluteMax)`까지 행을 추가하고 MinForms보다 많은 미저장 행만
제거한다. 기존 행은 DOM에서 제거하지 않으며 허용한 DELETE 제어로 삭제 의도를 전달한다. 제거 후 미저장 행의 이름과 오류
위치만 연속 번호로 바꾸고 입력값·checkbox 상태·기존 PK·INITIAL_FORMS·다른 inline은 보존한다. 추가 시 첫 입력으로 focus를
옮기며 `formset:added`/`formset:removed` 이벤트의 `detail.formsetName`에 prefix를 전달한다.

Script는 내용 SHA256을 포함한 URL로 제공하며 session을 읽거나 쓰지 않는다. CDN·inline script·eval은 사용하지 않는다.
JavaScript가 없으면 서버가 렌더링한 기존/추가 행으로 제출할 수 있다. 브라우저의 버튼 제한은 서버의 count·권한·부모/PK·
transaction 검사를 대체하지 않는다. 재현 가능한 실제 브라우저 소비자는 [검증 fixture](testdata/browser/README.md)에 있다.

파일 입력은 `Form.Definition.ExtraFields`의 FileField와 multipart로 전달한다. 편집 가능한 inline의 파일 필드는 ExtraForms가
0이어도 부모 form의 multipart 전송을 활성화한다. 기존 readonly 행의 파일은 disabled이며 submitted file은 채택하지 않는다.
`Lookup`은 원래 파일 capability와 clear 입력을 보존하므로 writer는 같은 권한/현재 행으로 다시 bind한 결과를 사용한다.
동적 행 재번호는 선택한 파일을 보존하지만 서버 오류로 페이지를 다시 표시하면 파일을 재선택해야 한다. 파일을 영구 저장하거나
clear 의도를 실제 파일 삭제로 처리하는 동작은 application의 별도 저장 계약이며 자동으로 수행하지 않는다.
[파일 입력 정책](../uploads/README.md#admin)을 따른다.

등록당 inline 8개, 합산 AbsoluteMax 100행이며 부모를 합친 기본 입력 개수도 startup에서 확인한다. URL-encoded 64 KiB body,
multipart 문자열 합계 64 KiB, 전체 입력 1,024개, 값당 4 KiB 제한과 template의 loop/1 MiB 출력 제한은 계속 적용된다.
파일 payload는 SiteConfig.Uploads의 별도 body/file/공유 메모리 예산을 따른다. Multiple choices는 같은 입력/출력 예산을
사용한다. 한도 초과는 명시적 오류이며 행·값·HTML을 잘라 성공으로 반환하지 않는다.
