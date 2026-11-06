# 사용자 생성 Form

## 선택 모델의 생성 Form

`identity/model.NewCreationForm(runtime.Accounts(), config)`는 선택한 계정 모델과 관리 owner의 CreateFields·정규화·관계·
파일·비밀번호 정책을 사용하는 재사용 Form이다. 기본 설정은 비밀번호와 확인 값을 요구한다.
`CreationFormConfig{AllowUnusablePassword: true}`는 명시적인 사용 불가 선택을 추가한다. 실제 입력 이름은
`PasswordFields()`로 읽는다. 모델에 `password1`, `password2`, `unusable_password`라는 필드가 있어도 명령 입력과 섞지 않는다.

```go
creation, err := accountmodel.NewCreationForm(runtime.Accounts(), accountmodel.CreationFormConfig{})
if err != nil {
    return err
}
bound, err := creation.Bind(ctx, actor, forms.NewData(submitted))
if err != nil {
    return err // 현재 인가·DB·취소·파일 검사 실패
}
if !bound.Form().Valid() {
    return renderForm(bound.Form())
}
result, publications, err := creation.Create(ctx, actor, bound)
return reportCreation(result, publications, err)
```

`renderForm`과 `reportCreation`은 호출 애플리케이션의 표시·결과 보관 함수다. 파일이 있는 입력은 `forms.NewDataWithFiles`를
사용한다. `reportCreation`은 오류에도 모든 파일 게시 시도를 보관해야 한다. 생성 결과와 파일 저장은 별도 결과이며,
unknown을 성공으로 바꾸거나 파일을 자동 삭제·재게시하지 않는다.

Bind는 파일 검사 전 현재 add/change 및 추가 권한을 확인한다. Site의 staff 조건은 요구하지 않으며 익명 가입이나 로그인은
수행하지 않는다. Relation 입력은 IR default와 key 문법을 보존하고, 같은 owner의 현재 존재·필터·한도·unique 검사를 거친다.
직접 Bind의 관계 label은 숫자 key다. 검색 결과 label이 필요한 Admin은 같은 `Definition`을 현재 choice로 bind한 뒤 `Check`를
사용한다. 모델 field 오류와 비밀번호 정책 진단은 합쳐 표시하되 서로의 입력 이름을 덮지 않는다. Bind/Check는 hash·게시·DB
쓰기를 하지 않으며 이후 저장을 허가하는 증명도 아니다.

지연 저장은 `creation.Prepare(ctx, actor, bound)`로 준비 객체와 게시 기록을 받은 뒤
`creation.Commit(ctx, actor, prepared)`로 수행한다. Prepare는 private command의 이름·타입·확인·byte 한도를 다시 검증하고
현재 owner의 준비 경로에서 usable 비밀번호를 한 번 hash한다. 업로드는 Prepare가 끝날 때까지 유효해야 하며 그 뒤 닫아도 된다.
준비 실패·준비 포기·Commit 실패에서도 파일 기록은 호출자가 보존한다. PreparedCreation은 같은 creator·actor·domain과 한 번의
native 시도에 묶이며, 최종 인가·관계/필터·unique·정책을 재확인한다. 확정 실패·unknown 뒤 사본으로 재시도할 수 없다.

같은 준비 객체의 복사본은 하나의 시도를 공유한다. 동시에 Commit해도 하나만 저장되며, 성공한 계정을 나중에 삭제해도
소비한 준비 객체로 다시 만들 수 없다. 생성 권한이 있는 다른 actor도 원래 actor의 준비 객체를 넘겨받을 수 없다.
다른 owner/actor 또는 시도 전 취소로 거부된 호출은 정당한 소유자의 시도를 소모하지 않는다. 로그인·last_login 기록과
자동 session 생성은 계정 생성에 포함되지 않는다.

## Login 입력 규칙

기본 Form은 선택 모델의 IR 길이와 쓰기 정규화를 따른다. Username 입력 관례가 필요한 경우 실제 login 역할에
`UnicodeUsernameInput(150)`을 명시한다. 필드 이름이 `username`일 필요는 없으며 동명의 다른 profile 필드에는 적용하지 않는다.

```go
creation, err := accountmodel.NewCreationForm(runtime.Accounts(), accountmodel.CreationFormConfig{
    AllowUnusablePassword: true,
    LoginInput:            accountmodel.UnicodeUsernameInput(150),
})
```

한도는 양수여야 하고 유한한 저장 한도를 넓힐 수 없다. Python 공백을 제거한 원문의 rune 수가 이미 한도를 넘으면
비싼 정규화를 생략한다. 정규화 자체는 선택 역할의 쓰기 정책을 따르며, 정규화 후 길이도 확인한다. 문자 문법은 Unicode 16의
letter/number와 `_.@+-`다. 현재 unique 진단이 있으면 그것을 유지하고, 문법 오류만 있는 candidate는 비밀번호 강도 검사가
관찰할 수 있다. 이 정책의 150자 생성 입력 한도와 기본 User의 256자 저장/편집 한도는 별도다.

대소문자를 무시한 생성 중복 검사는 관리 owner의 `AccountManagementConfig.CaseInsensitiveCreationLogin`에서 선택한다.
입력 정책이 그 설정이나 인증 정규화를 변경하지 않는다. 준비 단계는 valid Form의 candidate도 한도·문법으로 다시 확인하며,
느슨한 Form이나 model-clean 변경으로 재정규화한 값과 실제 저장 후보가 달라지면 거부한다.

선택 Admin도 같은 정책을 명시할 수 있다.

```go
err := identityadmin.RegisterAccount(builder, runtime.Accounts(), identityadmin.AccountRegistration{
    Slug:               "accounts",
    CreationLoginInput: accountmodel.UnicodeUsernameInput(150),
})
```

Form을 거치지 않는 typed 생성은 `runtime.Accounts().Creator()`의 Prepare/PrepareUnusable/Commit을 사용한다.
Principal ID 공급자는 관리 owner의 생성 설정에 두며 제출 필드로 노출하지 않는다. 기본/custom 소비자의 독립 Django 기준과
환경별 수용은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.

## 기존 기본 User 전용 Form

아래 API는 아직 기존 `identityadmin.Register` 경로에서도 사용한다. 선택 모델의 새 조립은 위 공통 Form/Admin owner를
사용하며, 남은 고정 소비자의 이관 범위는 [현재 작업](../work/0138-custom-account-lifecycle.md)에 기록한다.

`identity.NewUserCreationForm(manager)`는 기본 User 모델의 username·password1·password2 Form을 만든다.
`NewAdminUserCreationForm(manager)`는 비밀번호 로그인을 끄는 명시적 선택을 추가하며, Admin 밖에서도 사용할 수 있다.
두 Form은 같은 Schema IR의 저장 의미와 현재 Manager 권한을 사용한다. 일반 Form에는 Site의 staff 조건이 없고,
Manager의 현재 add_user/change_user 조건은 유지한다. 익명 회원가입 endpoint를 자동으로 만드는 API는 아니다.

Startup에서 Form을 한 번 구성한 뒤 요청 사이에 재사용할 수 있다. Unbound 화면은 `form.Spec().Unbound(nil)`로 만들고,
제출은 context·인증된 actor와 함께 bind한다. 아래 흐름의 `nextPrincipalID`는 애플리케이션이 소유한 ID 공급자다.

```go
form, err := identity.NewUserCreationForm(manager)
if err != nil {
    return err
}

bound, err := form.Bind(ctx, actor, forms.NewData(submitted))
if err != nil {
    return err // DB/인가/취소 오류; 입력 오류로 낮추지 않는다.
}
if !bound.Valid() {
    return renderForm(form.Spec(), bound) // password 원문은 재표시하지 않는다.
}
principalID, err := nextPrincipalID(ctx)
if err != nil {
    return err
}
user, err := form.Create(ctx, actor, principalID, bound.Cleaned())
```

Bind는 field 정리 뒤 이름 중복과 남은 비밀번호 정책을 검사한다. 필수 입력이 빠져도 검사 가능한 다른 오류를 함께 모으며,
확인 비밀번호 불일치·password2 field 오류·명시적 사용 불가 선택은 해당 강도 검사를 생략한다.
Bind는 hash나 사용자·session·audit 저장을 하지 않는다. `Spec`과 `Definition`만으로 만든 순수 Form에는 DB 검증이 없으므로,
직접 소비자는 `Bind`, 공통 Admin은 `ValidateCreate`에 연결한 `Check`를 사용한다.

저장을 미룰 때는 `Create` 대신 준비와 확정을 나눈다.

```go
prepared, err := form.Prepare(ctx, actor, principalID, bound.Cleaned())
if err != nil {
    return err
}
// 아직 사용자는 저장되지 않았다. 준비 객체를 버리면 DB 부작용은 없다.
user, err := form.Commit(ctx, actor, prepared)
```

Prepare는 선택한 입력 이름·타입·기본 검사를 다시 확인하고 Manager의 현재 인가·관계·중복·정책 검사를 수행한다.
읽기 scope를 종료한 뒤 usable password를 한 번 hash한다. 사용 불가 password는 정책/hash를 호출하지 않는다.
`PreparedUserCreation`은 비밀번호·해시를 공개하지 않는 불변 객체다. 요청 작업이 끝나면 참조를 버린다.
준비 객체는 원래 Manager와 actor ID에 결합하며 다른 Manager나 actor에게 넘겨 저장할 수 없다.

Commit은 hash를 반복하지 않는다. 현재 권한·중복·관계 선택·비밀번호 정책을 native write fence에서 재검사하고,
user·관계·audit를 한 transaction에 저장한다. 준비 후 DB 상태가 달라지면 실패할 수 있다. 오류에는 성공한 UserDetails를 게시하지
않으며 unknown 결과를 자동 재시도하지 않는다. 준비 객체의 복사본들은 한 번의 저장 시도를 공유한다.
실패·unknown을 포함해 한 번 시도한 후보는 다시 저장하지 못한다. 새 시도에는 새 Prepare가 필요하다.
성공한 사용자를 나중에 삭제해도 과거 준비 객체로 같은 credential을 다시 만들 수 없다.
다른 Manager·actor 또는 저장 시도 전 취소로 거부된 호출은 정당한 소유자의 후보를 소비하지 않는다.
로그인·last_login 변경·기존 session 변경은 사용자 생성 자체에 포함되지 않는다.

Form을 사용하지 않는 관리 작업도 `Manager.PrepareUserCreation`과 `CommitUserCreation`을 사용할 수 있다.
추가 profile·role·관계는 `NewUserCreate(...).WithFirstName(...).WithGroups(...)` 등의 typed 입력으로 준비 전에 명시한다.
준비한 객체를 Python model처럼 수정하거나 password encoding을 직접 저장하는 기능은 제공하지 않는다.
고정 Django의 `save(commit=False)`에서 저장 전 hash·저장 없음·나중 저장의 외부 수명을 채택하며, Python 객체 모양을 복제하지 않는다.

`forms/model.Definition`은 IR field 선택·override·비저장 입력·pure cross validation을 공유한다.
Admin의 `FormConfig.Definition`도 같은 표현을 사용하며 transport용 예약 이름과 relation 선택 권한은 Admin이 별도로 검사한다.
Default User의 이 Form과 준비/저장 경로는 custom user model 또는 모든 ModelForm 저장 동작의 지원 선언이 아니다.
실행 source와 환경별 검증은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
