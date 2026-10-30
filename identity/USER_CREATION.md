# 사용자 생성 Form

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
