# 여러 Form을 한 번에 처리하기

`SetSpec`은 같은 Form의 여러 행을 prefix로 구분해 바인딩한다. 기존 행의 initial은 서버가 제공한다.
`SetConfig`의 개수는 명시적 값이며, 기본 구성이 필요하면 `DefaultSetConfig()`에서 시작한다.

```go
func ticketRows(data forms.Data) (forms.Set, error) {
    title, err := forms.CharField("title", forms.WithMaxLength(120))
    if err != nil { return forms.Set{}, err }
    row, err := forms.NewSpec([]forms.Field{title})
    if err != nil { return forms.Set{}, err }
    config := forms.DefaultSetConfig()
    config.Prefix = "tickets"
    config.MaxForms = 40
    config.AbsoluteMax = 80
    config.ValidateMax = true
    config.CanDelete = true
    config.CanOrder = true
    spec, err := forms.NewSetSpec(row, config)
    if err != nil { return forms.Set{}, err }
    return spec.Bind(data, nil)
}
```

요청에는 `tickets-TOTAL_FORMS`, `tickets-INITIAL_FORMS`, `tickets-0-title`처럼 prefix가 붙은 이름을 쓴다.
서버 initial이 없는 신규 입력에서는 INITIAL_FORMS가 0이어야 한다. 다른 prefix·범위 밖 index·선행 0이 있는 index는
이 set의 행 입력으로 섞이지 않는다. 원래 전체 제출은 `Set.Submitted()`로 명시적으로 읽을 수 있다.

`spec.Unbound(initial)`은 검증 callback을 실행하지 않고 화면용 행 수와 management 값을 준비한다.
`set.Forms()`는 오류/삭제 여부와 무관하게 모든 행을 돌려주며 각 `SetForm.Form()`은 일반 Form의 initial·cleaned·errors를
제공한다. `FieldName("title")`은 HTML 이름을 만든다. 렌더러에서는 삭제/빈 추가 행을 제출할 수 있도록 HTML의 required
속성을 강제하지 않는다. 서버의 `Field.Required()` 의미는 그대로 유지한다. 자동 HTML renderer는 후속 제품 연결 범위다.

`EmptyForm()`은 `tickets-__prefix__` 이름을 가진 미바인딩 template이다. 클라이언트는 실제 index로 치환하고 TOTAL_FORMS를
증가시킨다. COUNT·Order·Delete는 클라이언트가 서버의 대상·권한·저장 정책을 변경하는 통로가 아니다.

- 기존 initial 수와 INITIAL_FORMS가 다르거나 음수/앞뒤가 맞지 않는 count는 management 오류다.
- AbsoluteMax는 검증 callback보다 먼저 생성할 행 수를 제한한다. 서버 initial도 이 상한을 넘으면 구성 오류다.
- ValidateMax/ValidateMin이 켜져 있으면 삭제·빈 행 의미를 반영해 제출 개수를 검사한다.
- 바뀌지 않은 optional 추가 행은 field/cross validation을 건너뛰고 빈 cleaned data를 유지한다.
- ORDER는 optional 정수다. 같은 순서는 입력 index를 유지하며 빈 ORDER는 뒤로 간다.
- DELETE가 켜진 행의 오류는 다른 행의 유효성을 막지 않는다. CanDeleteExtra=false인 추가 행에는 삭제 field가 없다.

`SetValidator`는 행 snapshot을 받아 non-form 진단을 반환한다. Callback은 pure·동시 실행 안전성을 보장해야 한다.
모든 행을 검사한 다음 한 번 실행하며, 개수 제약이 먼저 실패하면 실행하지 않는다. 관리 필드 오류는 `Management().Errors()`,
각 행의 오류는 `Forms()[index].Form().Errors()`, 전체 진단은 `NonFormErrors()`로 나뉜다.
`WithErrors()`는 callback을 반복하지 않고 전체 진단을 추가한 새 결과를 반환한다.

`BindWith(data, initial, processor)`는 일반 field/cross 검증 뒤 행별 `SetProcessor`를 한 번 적용한다.
바뀌지 않은 optional 추가 행은 processor도 실행하지 않는다. Processor는 받은 행의 Form 또는 그 Form에 `WithErrors`로
진단을 붙인 결과만 반환할 수 있다. 새로 바인딩한 Form이나 다른 행의 Form은 `binding_mismatch` 오류다. 처리된 행의
모델 오류까지 반영한 다음 개수와 여러 행 검증을 수행한다. Processor도 pure하며 실제 I/O는 별도 단계에서 실행한다.

나중에 얻은 DB 데이터 진단은 `WithFormErrors(index, failures)`로 추가할 수 있다. 이 메서드는 기존 callback이나 개수
검증을 다시 실행하지 않고 이미 invalid인 결과를 valid로 바꾸지 않는다. 삭제된 행의 데이터 오류는 무시될 수 있으므로,
identity·권한 등 요청 자체를 거절하는 진단은 반드시 전체 `WithErrors` 또는 명시적 operation error로 전달한다.

`Valid()` 확인 뒤 `ActiveForms()`, `DeletedForms()`, `OrderedForms()`로 처리할 행을 고른다. Invalid/미바인딩 결과의 선택은
오류를 반환한다. 이 선택은 저장 권한도 자동 쓰기도 아니다. 기존 모델 identity·현재 인가·revision/제약과 전체 요청의
transaction/rollback 의미는 모델/애플리케이션 연결이 담당한다.

Database-independent core와 [typed 모델의 여러 행 준비](model/README.md#여러-모델-행의-준비)를 구현했다.
[Helpdesk HTTP 편집](../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)은 typed 준비와 실제 여러 행/관계/감사 기록의
원자 저장을 연결한다. 일반 Formset의 자동 저장·inline/file upload는 후속 범위다. 고정 Django 관찰에서 Go의 count 거부와
Int64/중복 입력·기존 Boolean/Choice parser 경계는 [ADR-0081](../docs/adr/0081-formset-counts-and-row-ownership.md)에 따라 구분한다.
