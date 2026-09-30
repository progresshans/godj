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

`BindWith(data, initial, SetProcessor{Form: ..., Clean: ...})`는 일반 field/cross 검증 뒤 행별 `Form` callback을 한 번 적용한다.
바뀌지 않은 optional 추가 행은 건너뛴다. 처리된 행의 모델 오류까지 반영한 다음 개수를 검사하고, 개수 검사가 통과하면
`Clean` callback과 사용자 `SetValidator`를 순서대로 실행한다. `Clean`은 여러 행 모델 제약의 오류를 행과 전체에 붙일 수 있다.
뒤의 사용자 진단이 모델 진단을 없애지는 않는다. 두 callback은 받은 Form/Set 또는 `WithErrors` 계열로 진단을 붙인 결과만
반환할 수 있다. 새 바인딩이나 다른 행/Set은 `binding_mismatch` 오류다. Callback은 pure하며 I/O는 별도 단계에서 실행한다.

나중에 얻은 DB 데이터 진단은 `WithFormErrors(index, failures)`로 추가할 수 있다. 이 메서드는 기존 callback이나 개수
검증을 다시 실행하지 않고 이미 invalid인 결과를 valid로 바꾸지 않는다. 삭제된 행의 데이터 오류는 무시될 수 있으므로,
identity·권한 등 요청 자체를 거절하는 진단은 반드시 전체 `WithErrors` 또는 명시적 operation error로 전달한다.
`Form.WithErrors(failures, rejectedFields...)`와 `WithFormErrors(index, failures, rejectedFields...)`는 복합/non-field 오류가
무효화한 cleaned field를 명시적으로 제거할 수 있다. 원래 제출·initial·변경 목록은 남고, 진단 없는 제거·알 수 없는 field는 거부한다.

`Valid()` 확인 뒤 `ActiveForms()`, `DeletedForms()`, `OrderedForms()`로 처리할 행을 고른다. Invalid/미바인딩 결과의 선택은
오류를 반환한다. 이 선택은 저장 권한도 자동 쓰기도 아니다. 기존 모델 identity·현재 인가·revision/제약과 전체 요청의
transaction/rollback 의미는 모델/애플리케이션 연결이 담당한다.

Database-independent core와 [typed 모델의 여러 행 준비](model/README.md#여러-모델-행의-준비)를 구현했다.
[Helpdesk HTTP 편집](../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)은 typed 준비와 실제 여러 행/관계/감사 기록의
원자 저장을 연결한다. 부모에 연결한 행은 [InlineSpec](model/README.md#부모에-연결한-여러-행)을 사용한다.
파일/clear 입력은 `NewDataWithFiles`로 전달하며 행 prefix와 빈 추가 행·readonly·삭제 의미를 유지한다.
[파일 입력](../uploads/README.md)의 수명과 순수 검증 경계를 따른다. [Admin inline](../admin/inlines.md)의 동적 행·파일 UI와
[모델 여러 행 저장](model/README.md#여러-행의-저장과-지연-저장)의 명시적 storage·SavePlan을 연결한다. Core Formset 자체는
DB 쓰기·인가·transaction을 소유하지 않는다. 고정 Django 관찰에서 Go의 count 거부와
Int64/중복 입력·기존 Boolean/Choice parser 경계는 [ADR-0081](../docs/adr/0081-formset-counts-and-row-ownership.md)에 따라 구분한다.

`SetSpec.WithFormField(field)`는 행 field를 같은 위치에서 교체하거나 끝에 추가하고, `WithConfig(config)`는 개수/prefix 정책을
교체한다. 둘 다 기존 row/set validator를 보존하고 구성 검사를 수행하며 원래 명세를 바꾸지 않는다.

`InlineParentField(name, identity)`는 서버의 Integer/NULL 부모를 보유한 optional hidden field다. 빈 제출은 서버 값으로 정리하고
다른 숫자·비정규 표기·중복 scalar는 거부하며 항상 Changed에서 제외한다. `HiddenInput`은 현재 Integer field에 제공한다.
렌더러는 `InlineParent()`의 서버 값을 사용한다. 이 저수준 field만으로는 검증을 건너뛰는 빈 행이나 삭제 행의 전체 admission을
보장하지 않는다. 모델 InlineSpec이 그런 행의 원래 제출도 검사하고 parent cohort·pending key·여러 행 제약을 함께 소유한다.

## 조회 전용 기존 행과 편집 가능한 추가 행

`SetConfig.ReadOnlyInitial`은 기존 행의 일반 field를 서버 initial에 고정한다. `Form.ReadOnly()`로 표시 정책을 읽는다.
기존 값이 POST에서 빠지거나 다른 값/중복값이 들어와도 initial을 cleaned 값으로 보존하고 일반 field/cross validator를
호출하거나 Changed에 넣지 않는다. 원래 제출은 명시적 accessor에 남는다. 더 좁은 입력 길이로 기존 값을 다시 검증하지 않는다.
추가 행은 정상 검증한다. ORDER/DELETE는 구성에서 켠 경우에만 별도 검증·변경 추적을 하는 독립 제어 입력이다.
그 오류나 whole-set 검증·management·상한을 조회 전용 정책으로 건너뛰지 않는다.

렌더러는 조회 전용 field에 서버 Initial을 표시한다. PasswordInput의 비공개 출력은 그대로 적용하고 DELETE/ORDER 제어는
해당 SetForm의 선택 결과에서 별도로 표시한다. 이 정책은 조회·추가·수정·삭제 권한을 부여하지 않는다. 현재 권한과 표시할
current 집합은 제품이 정한다. [모델 준비](model/README.md#여러-모델-행의-준비)는 조회 전용 행의 저장 후보 생성을 막으면서
서버 identity와 여러 행 고유성 검사를 유지한다.
