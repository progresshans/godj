# Admin 관계 선택

`NewRelatedChoices`는 모델 Form, inline, collection FormSet과 command에 같은 bounded 선택 선언을 연결한다.
선언은 [`forms/model.ModelChoices`](../forms/model/README.md)의 canonical target과 선택한 source 관계를 유지한다.
일반 모델 필드의 source identity·전체 metadata·필드 이름·단일/다중 선택이 등록 모델과 다르면 시작 시 거부한다.
모델 관계가 없는 command 입력은 `NewModelChoices`로 target을 명시하고 `RelatedChoiceConfig.Field`를 지정한다.
같은 모델의 관계를 사용하는 command는 `NewRelationChoices` 선언을 그대로 사용할 수 있다.

```go
choices, err := formmodel.NewRelationChoices(project, ticketIdentity, "labels", LabelObjects,
    formmodel.ModelChoiceConfig[Label]{
        Label: func(value Label) (string, error) { return value.Name, nil },
        Search: labelSearchPredicates,
        Order: []orm.Ordering[Label]{LabelFields.ID.Asc()},
    })
if err != nil {
    return err
}
source, err := admin.NewRelatedChoices(admin.RelatedChoiceConfig[Label]{
    Choices: choices,
    Permission: viewLabel,
    Scope: func(ctx context.Context, actor auth.Principal, origin admin.ChoiceOrigin,
        visit func(db.Queryer, []orm.Predicate[Label]) error) error {
        return readCurrentScope(ctx, actor, origin, visit)
    },
})
```

`Scope`는 호출마다 현재 권한과 업무 범위를 확인하고, 그 읽기 수명 안에서 `visit`을 한 번 동기 호출한다.
현재 target 범위는 typed predicate로 전달한다. visitor를 보관하거나 queryer를 별도 작업으로 넘기지 않는다.
조회 실패·취소·scope 종료 실패는 부분 결과를 게시하지 않는다. visitor를 호출하기 전에 명시적으로 거부한
operation error는 거부 상태를 유지한다. visitor 누락·중복·반환 뒤 호출은 계약 오류다.

`ChoiceOrigin`은 등록된 source·동작과 검증한 현재 객체/부모/자식 identity를 제공한다. inline의 이름은 선언된 prefix이고,
collection의 이름은 등록된 command/FormSet 이름이다. 행 index는 표시 위치이며 권한 identity가 아니다.
기존 inline 자식의 검색은 서버의 현재 부모·전체 cohort에서 그 PK를 확인한 뒤에만 target을 읽는다.
새 행은 Add 권한이 필요하고 기존 행의 identity를 상속하지 않는다. origin이 제공하는 값이 애플리케이션의 현재
DB 권한이나 transaction 검사를 대신하지 않는다.

GET은 현재 선택 key를 `Resolve`하고 검색 첫 페이지를 따로 읽는다. 렌더링은 선택한 값과 해당 페이지만 합친다.
POST는 원래 입력의 key만 해석하고 `Resolve` 결과로 검증한다. 페이지에 보였다는 사실은 선택 권한이 아니다.
현재 선택이 첫 페이지 밖에 있어도 표시·제출할 수 있으며 범위를 벗어난 선택은 label을 재사용하지 않고 원래 key와
입력 오류를 유지한다. 정수 key는 HTML과 JSON에서 문자열로 전달해 JavaScript의 숫자 정밀도를 거치지 않는다.

검색은 등록 때 고정된 다음 GET 경로를 사용한다. 외부 입력으로 source 모델이나 필드를 바꿀 수 없다.

| 표면 | 경로와 현재 identity |
|---|---|
| 새 객체 | `/<model>/add/choices/<field>/` |
| 기존 객체 | `/<model>/change/choices/<field>/?id=<object>` |
| inline | 부모 add/change 아래 `inline/<prefix>/choices/<field>/`, 기존 부모의 `id`, 기존 자식의 `row_id`, 표시용 `index` |
| command | `/<model>/collection/<name>/choices/<field>/` |
| 여러 건 생성 | `/<model>/collection-set/<name>/choices/<field>/?index=<row>` |

`q`, `offset`, `limit`을 받아 `{items:[{value,label}],offset,limit,more}`를 반환한다. 중복·미지원 query 인자와
범위를 벗어난 페이지는 거부한다. source 실행 권한, target 권한과 Site의 deny overlay를 target I/O 전에 확인한다.
조회 전용 행은 검색하지 않는다. JSON 응답은 `no-store`와 `nosniff`를 사용하며 로그인 next 경로에 등록하지 않는다.
검색 상한은 `ModelChoices`의 페이지 100개·term 256 bytes·offset 1,000,000이다. 제출 key의 상한과 Admin body 예산은
별도 적용되며 초과 결과를 조용히 자르지 않는다.

외부 JavaScript는 native select를 유지하면서 검색·페이지 이동·다중 선택 해제와 상태 안내를 제공한다.
새 검색과 행 재번호는 이전 요청을 취소하고 응답 순서와 현재 행 identity를 다시 확인한다. label은 text로 삽입하며
스크립트 없이도 서버가 렌더링한 선택과 첫 페이지를 제출할 수 있다. readonly controls는 제출에 참여하지 않는다.

Admin은 writer 호출 직전 선택을 다시 확인한다. 최종 writer는 현재 DB 권한·관계·revision·고유성과 commit을 계속 소유한다.
inline의 transaction 내부 재검증에는 [`ModelInline.BindCurrent`](inlines.md)와 `InlineCurrent.ResolveChoices`를 사용한다.
Helpdesk는 [범주별 선언](../examples/helpdesk/admin_choices.go)을, 계정 관리는
[현재 권한과 같은 읽기 snapshot의 선언](../identity/management_choices.go)을 사용한다.
