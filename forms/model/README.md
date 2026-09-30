# 모델 Form의 후보와 후처리

Schema IR에서 선택한 필드를 `Definition`으로 투영한다. `Bind`는 원래 제출과 Form의 cleaned data를 보존하고,
현재/default 모델 후보를 별도로 만든다. 모델 field cleaning 뒤 `PostClean.Clean`, 읽기 전용 `PostClean.Validators`를
실행한다. 필드가 실패해도 후처리는 실행하며, 값 변환이 기존 오류를 없애거나 field cleaning을 다시 실행하지 않는다.

제공한 initial은 기존 값의 표시와 변경 비교에 사용한다. Form 입력의 길이·Decimal 정밀도를 더 좁혀도 기존 값을
자르거나 거부하지 않아 사용자가 유효한 값으로 수정할 수 있다. 같은 기존 값을 그대로 제출하면 현재 입력 제약으로
검증하므로 오류가 나면서도 Changed는 비어 있을 수 있다. 타입·유효한 표현·UTF-8/NUL 검사와 선언한 default의 제약,
Admin의 모델 snapshot 및 최종 ORM 저장 검사는 유지한다. `InitialValues`는 저장 가능성이나 권한 검사의 대체가 아니다.

```go
definition := formmodel.Definition{
    Fields: []string{"code", "email", "counter"},
    PostClean: formmodel.PostClean{
        Fields: []string{"code", "hidden"},
        Clean: func(candidate forms.Values) (forms.Values, validation.Errors) {
            code, _ := candidate.String("code")
            return forms.NewValues(map[string]forms.Value{
                "code": forms.String(strings.ToUpper(code)),
                "hidden": forms.String("server-derived"),
            }), validation.Errors{}
        },
    },
}
bound, err := definition.Bind(metadata, submitted, authorizedInitial)
```

`PostClean.Fields`는 callback이 바꿀 수 있는 scalar의 명시적 목록이다. 선택 입력도 여기에 선언해야 변경할 수 있다.
제외한 scalar를 선언하면 숨긴 입력을 클라이언트에게 허용하지 않고 서버가 파생한 값을 저장 adapter에 전달할 수 있다.
PK·모델 파일·컬렉션·추가 command input·알 수 없는 field는 변경할 수 없다. 선언하지 않은 변경이나 잘못된 값 표현은
부분 후보를 반환하지 않고 configuration error로 처리한다. 이 오류에는 입력값을 넣지 않는다.
Callback은 pure하고 동시 호출에 안전해야 한다. 반환한 `forms.Values`는 **변경 집합**이며 빈 집합은 후보를 그대로 둔다.
Python `Model.clean()`의 반환값을 무시하는 규약과 다르다. 후보를 암묵적으로 수정하는 Python 객체 구조는 구현 목표가 아니다.

`Form.Cleaned()`는 사용자 입력 정리 결과다. `Candidate()`는 clean 변경과 오류를 반영한 모델 상태다.
`Input()`은 유효한 bound form에서만 선택 입력·command input·실제로 반환한 명시적 clean 변경을 제공한다.
선언만 하고 바꾸지 않은 제외 필드는 저장 입력에 추가하지 않는다. Clean이 nonnullable field에 만든 NULL은 후보에
보존하지만 `Input()`에서 `nonnullable` 준비 오류로 거부한다. Form의 유효성과 기존 field 오류는 바꾸지 않는다.
`Input()`과 typed instance 준비는 I/O를 수행하지 않으며,
전체 `Candidate()`를 저장 권한으로 간주해서는 안 된다.

DB unique/constraint 검사는 변환된 후보를 사용하지만 제외 정책은 여전히 Form의 선택·오류에서 얻는다.
제외 필드가 clean에서 바뀌어도 해당 field의 advisory unique 검사는 생략될 수 있다. 최종 저장의 DB 제약을 유지해야 한다.
`CheckDatabase`는 현재 권한을 확인한 하나의 읽기 scope에서 실행하고, scope 정리까지 성공한 진단만 `WithErrors`로 적용한다.
이후 `Input()`을 typed create/patch 또는 명시적으로 준비한 모델 instance에 적용하고 현재 권한·revision·DB 제약을
보호하는 저장 adapter를 호출한다. Typed 변환은 값의 존재·타입·NULL 여부를 확인해야 하며 변환 실패를 Go의 zero value로
대체해서는 안 된다. 일반 ORM Save는 full_clean을 암묵적으로 수행하지 않는다.

Admin은 `ModelConfig.PostClean`과 생성 전용 `CreateForm.Definition.PostClean`을 지원한다. 수정의 initial은 현재 인가된
row의 모든 stored scalar를 제공해야 하며 PK와 구성된 revision은 registry가 Snapshot에서 소유한다. Revision을 clean의
출력으로 선언할 수 없다. Clean 출력은 Snapshot의 구조 검사와 변경 field/audit 검사에 포함되며 숨긴 Form 입력이 되지 않는다.
저장 직전 원래 제출과 최신 row에서 다시 바인딩하므로 clean은 바인딩마다 한 번 실행된다. 최종 transaction fence도 유지한다.

실제 생성 모델의 준비·SQLite/PostgreSQL 저장·rollback과 고정 Django 15개 사례의 비교는
[독립 생성 소비자](../../codegen/consumertest/testdata/modelclean/consumer_test.go)가 검증한다.
일반 `ModelForm.save()` 전체 자동 생성·전체 constraint 종류·여러 행 자동 저장은 이 API의 존재만으로 완료되지 않는다.
장기 의미는 [ADR-0080](../../docs/adr/0080-model-blank-policy-and-form-post-clean.md), 실행 범위는
[TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)를 따른다.

## 여러 모델 행의 준비

`UnboundSet(manager, setSpec, current)`는 화면용 모델/identity snapshot을 만들고,
`BindSet(manager, setSpec, data, current, postClean)`은 [Formset](../formset.md)의 여러 행에 같은 모델 후보 검증을 연결한다.
현재 지원하는 PK는 Auto/Integer다. `current`는 caller가 조회하고 허용한 기존 모델 집합이며 자동으로 query를 실행하지 않는다.
저장되지 않았거나 PK가 중복된 current는 구성 오류이고, 명시적으로 존재하는 PK 0은 다른 PK와 같은 값으로 처리한다.

각 기존 행은 `IdentityName(index)`의 이름(예: `items-0-id`)과 `Identity(index)` 값으로 별도 hidden input을 제출한다.
서버 집합 안에서 PK 순서를 바꿀 수 있지만 누락·중복·잘못된 값·외부 PK는 전체 요청을 거부한다. 추가 행의 PK는 없거나
단일 빈 값이어야 한다. 빈 추가 행이나 DELETE로 표시한 행도 이 identity 규칙을 우회하지 못한다. PK는 editable field나
`BoundForm.Input()`에 들어가지 않는다. 이 집합은 검증 시점의 snapshot이며 최종 저장 시 현재 인가를 다시 확인해야 한다.

`Instance(index)`는 실제 field/model 검증을 수행한 행의 `InstanceForm`을 제공한다. Unbound와 그대로 둔 optional 추가 행에는
모델 후보를 만들지 않는다. 일반 Form을 한 번 바인딩한 결과에 model field/clean/validator를 적용하며 cleaned 값을 다시
직렬화하거나 재바인딩하지 않는다. 원래 제출, 제외한 기존 값, IR default, 명시적 clean 변경을 단일 Form과 동일하게 보존한다.
기존 scalar와 nullable pointer는 복사하며 `Current(index)`도 분리된 모델을 반환한다.

`SetConfig.ReadOnlyInitial`인 기존 행에는 검증 대신 서버의 읽기 전용 후보를 제공한다. 일반 field/model clean을 실행하지 않고
서버 값을 유지한다. 이 후보도 형제 unique tuple 비교에 참여하지만 `Input()`과 독립 `Instance().Prepare()`는 `read_only`로
거부한다. 전체 `Prepare().Rows()`에서도 제외한다. 명시적으로 허용한 DELETE는 별도 `Deleted()`의 서버 snapshot으로 남는다.
조회 전용 행에도 management·PK/cohort·Inline 부모 admission을 적용하며 새 행은 정상 검증한다. 상태 표시에는
`instance.BoundForm().Form().ReadOnly()`를 사용한다. 이 기능만으로 Admin의 권한 분기·HTML·합성 저장을 구현했다고 주장하지 않는다.

선택한 ManyToMany field가 있으면 이미 읽은 관계 데이터를 돌려주는 하나의 pure reader를 마지막 인자로 제공한다.
Reader는 `func(M, ir.ManyToManyField) ([]int64, bool)`이며 별도 모델/metadata snapshot을 받는다. 기존 행의 관계 값을
읽을 수 없으면 명시적으로 실패한다. 관계 query를 binding 안에 숨기지 않는다. 새 행에 필요한 서버 소유 값은
`PostClean.Fields/Clean`으로 명시한다.

```go
boundSet, err := formmodel.BindSet(
    models.ArticleObjects, setSpec, submitted, currentArticles, postClean,
)
if err != nil { return err }
// 현재 권한과 DB 진단을 명시적으로 확인한 뒤 전체 요청이 유효할 때 준비한다.
preparedSet, err := boundSet.Prepare()
if err != nil { return err }
for _, row := range preparedSet.Rows() {
    candidate, err := row.Model()
    if err != nil { return err }
    // row.Index()/Existing()/Changed()와 row.Prepared().Input()/Collections()를
    // 실제 transaction의 저장 정책에 연결한다.
    _ = candidate
}
```

`Prepare()`는 삭제되지 않은 기존 행과 변경된 추가 행을 typed 후보로 준비하고, 기존 행의 삭제 의도를 `Deleted()`로
분리한다. Django `save(commit=False)`와 달리 **편집 가능한 기존 행은 변경하지 않아도 Rows에 남긴다**. Caller는 모델 후처리와 실제 쓰기
정책에 맞춰 저장할 대상을 정한다. `Changed()`는 Form의 입력 변경이며 model clean의 변경 field 목록이 아니다.
삭제한 추가 행과 변경하지 않은 optional 추가 행은 저장 후보에 포함하지 않는다. 삭제 대상의 Model은 원래 서버의 모델
snapshot이다. 준비는 저장/삭제·transaction·인가·여러 행 DB 제약을 자동 실행하지 않는다.

행 데이터의 DB 진단은 `WithRowErrors`로 붙여 typed/core 결과를 함께 갱신한다. Callback과 앞선 개수 검증을 반복하지 않는다.
DELETE는 행 데이터 오류를 무시할 수 있으므로 요청 전체의 identity·인가 실패에는 `WithErrors` 또는 operation error를 쓴다.
고정 Django 20개 관찰에서 정상/모델 오류/삭제/정렬 11개 결과를 대조하고 identity 9개는 전체 거부한다. 그중 native가 허용한
6개는 의도적인 강화 차이다. Native의 queryset 조회 수와 GoDj의 미리 읽은 snapshot 처리는 같은 query 계약으로 세지 않는다.
실제 [Helpdesk 편집기](../../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)는 요청별 현재 cohort/인가·관계 선택지를 확인하고,
같은 relation transaction 안에서 여러 행·삭제 정책·감사 기록을 저장한다. 일반 `InstanceSet`이 자동으로 이 저장 정책을 실행하지는 않는다.
일반 저장은 아래 `SavePlan`으로 연결하며 모델 파일은 명시적인 `SaveFiles`를 사용한다. ExtraFields의 [파일 명령 입력](../../uploads/README.md)은
typed 준비 결과의 Input에 보존하며 model scalar에 넣지 않는다. [Admin inline](../../admin/inlines.md)은 권한별 표시와 합성 저장을 연결하며, 부모에 연결한 여러 행 준비는 아래 Inline API를 사용한다.

`BindSet`은 행 검증과 개수 제한 뒤, 사용자 SetValidator 전에 선택한 unique field와 IR의 복합 UniqueConstraint를 행 사이에서
검사한다. Valid인 행의 **cleaned 입력값**을 비교하며, model clean이 바꾼 candidate나 DB 저장값의 검사를 대체하지 않는다.
NULL이 포함된 튜플·제외된 field·invalid 행·바뀌지 않은 빈 추가 행은 비교하지 않는다. 삭제 행을 제외하는 시점도 기존 Formset의
유효성/개수 규칙을 따른다. 다른 invalid 행 때문에 삭제 선택이 아직 유효하지 않으면 유효한 삭제 표시 행도 중복 검사에 참여한다.

뒤에 나온 중복 행에는 non-field `unique` 진단을 붙이고 해당 튜플의 cleaned field를 제거하며, 전체에도 진단을 남긴다.
Typed/core 오류와 exclusion은 함께 바뀌고 원래 제출·candidate는 보존한다. 모든 제약은 같은 검사 시작 snapshot의 완전한 튜플로
비교한다. 겹친 제약에서 앞선 오류가 field를 제거했다고 다른 제약을 부분 튜플로 비교하지 않는다. 제약 순서는 IR 순서로 결정적이다.
이 pure 검사에는 backend collation·저장된 다른 행·동시 쓰기와 candidate 변환이 포함되지 않으므로 최종 transaction의 DB 검사는 필수다.

## 여러 행의 저장과 지연 저장

`preparedSet.SavePlan()`은 변경된 기존 행·변경된 새 행과 기존 행의 삭제 의도를 모은다. `Prepare().Rows()`에 남아 있는
변경 없는 기존 행은 제외한다. MinForms 때문에 검증한 빈 추가 행도 변경이 없으면 저장하지 않는다. 조회 전용 행·DELETE인
추가 행도 저장하지 않는다. Form의 `Changed()`를 기준으로 하므로 model clean만 바꾼 후보를 자동 저장하지 않는다.

기존 행의 변경과 삭제는 **제출된 행 순서**로 실행하고 그다음 새 행을 저장한다. `ORDER`는 이 실행 순서를 바꾸지 않는다.
ORDER만 바뀐 editable 행은 Django처럼 변경 행에 속하지만 정렬 번호를 모델 열에 자동 저장하지 않는다.

```go
plan, err := preparedSet.SavePlan()
if err != nil { return err }
err = backend.AtomicRelation(ctx, func(session db.RelationSession) error {
    // 현재 권한·revision·DB 제약은 애플리케이션이 이 scope에서 확인한다.
    _, err := plan.Save(ctx, session, formmodel.SetSaveOptions[models.Article]{
        Collections: collectionSavers,
        Delete: deleteAdmittedArticle,
    })
    return err
})
```

기본 쓰기는 각 행의 `PreparedInstance.Save`로 scalar와 IR 순서의 selected collection을 저장한다. `Delete`는
`func(context.Context, db.Session, DeletedSetRow[M]) error`이며 기존 삭제가 있으면 반드시 제공한다. 완전한 project relation
정책을 가진 모델에는 해당 deleter의 `DeleteInSession`을 연결한다. 일반 manager 삭제로 PROTECT/CASCADE를 생략하지 않는다.
외부 transaction을 열지 않은 사용에서는 앞선 쓰기가 뒤쪽 실패 뒤에도 남을 수 있다.

`SetSaveOptions.Save`는 `func(context.Context, db.Session, *SetSaveRow[M]) error`로 **행의 전체 쓰기**를 대체한다.
이 callback이 선택된 collection까지 소유하므로 `Collections`와 동시에 지정하면 오류다. 최종 인가·revision 확인·update-only
저장·감사 기록이 필요한 제품은 이 callback에서 기존 writer를 연결한다. [Helpdesk Admin](../../examples/helpdesk/admin_inlines.go)은
이 방식으로 report의 권한·고유값·선택 열 저장·audit를 유지한다. Callback은 제공된 scope를 사용하고 오류를 그대로 반환한다.

기본 collection 설정과 필수 삭제 callback은 첫 쓰기 전에 전부 검사한다. `Save`는 첫 실패에서 중단하고 실패 연산까지의
`[]SetWrite`와 원래 error를 반환한다. 각 결과의 `Index()`는 원래 행 위치, `Kind()`는 선택된 create/update/delete 의도이며 실제
SQL 종류나 commit receipt가 아니다. Callback이 반환한 validation/운영 오류를 wrapping해서 의미를 바꾸지 않는다.
Callback 성공과 Go 값에 남은 PK도 최종 commit 증거가 아니므로 outer transaction의 terminal 결과를 따로 보존한다.

지연 저장에서는 `plan.Rows()`의 `row.Model()`이 반환한 **plan 소유의 mutable pointer**를 수정하고 caller가 scalar 저장을 수행한다.
그 뒤 `plan.SaveCollections(ctx, session, savers...)`가 selected 관계만 저장한다. 모든 selected 모델의 PK와 collection 설정을
첫 관계 쓰기 전에 검사한다. 지연 단계는 scalar나 삭제를 실행하지 않으므로 `plan.Deleted()`는 별도로 처리한다.
같은 plan을 복사하면 pointer를 공유하며 동시 수정하면 안 된다. 독립 작업에는 원래 불변 `PreparedSet`에서 새 `SavePlan`을 만든다.
발급된 key는 뒤쪽 오류나 rollback 후에도 pointer에 남는다. 반복 저장은 명시적인 새 시도이고 자동 재시도·멱등 보장은 없다.

모델 업로드가 남아 있으면 `SavePlan` 생성도 거부한다. 먼저 `preparedSet.SaveFiles(ctx, selectSavers)`를 호출한다.
Pure selector `func(PreparedSetRow[M]) ([]FileSaver[M], error)`는 저장할 업로드가 있는 행에만 한 번 호출되며 행마다 필요한
storage/name binding을 반환한다. 모든 행의 binding·제안 이름을 확인한 후 행 순서/IR field 순서로 게시한다. 성공 결과의 새
PreparedSet에서 `SavePlan`을 만들면 충돌 처리 후의 실제 파일 이름을 DB에 저장한다. 원래 입력과 준비 결과는 바뀌지 않는다.
`[]SetFilePublication`은 실패한 게시를 포함해 행 위치와 file outcome을 보존한다. DB rollback·clear·행 삭제는 파일을 자동
제거하지 않으며, 요청이 끝난 upload capability로 다시 게시할 수 없다. [파일/DB 경계](#모델-파일의-준비와-저장)를 따른다.

## 부모에 연결한 여러 행

`NewInlineSpec(projectBinding, parentManager, childManager, foreignKey, setSpec)`은 프로젝트의 canonical IR에서 자식 FK와
대상 부모를 확인한다. 부모/자식 manager의 전체 metadata가 일치해야 하며 다른 앱의 부모도 같은 project binding으로 연결한다.
현재 Auto/Integer 부모 PK와 required/nullable FK·OneToOne을 지원한다. FK 이름은 명시하고 모호한 관계는 거부한다.
OneToOne은 표시 MaxForms를 1로 제한하고 여러 행의 부모 고유성도 검사한다. IR이 지원하지 않는 FK default는 추가하지 않는다.

```go
inline, err := formmodel.NewInlineSpec(
    binding, models.CategoryObjects, models.LabelObjects, "category", setSpec,
)
if err != nil { return err }
children, err := inline.Bind(submitted, parent, currentLabels, formmodel.PostClean{})
if err != nil { return err }
```

`Unbound(parent, current)`와 `Bind`는 이미 인가하고 읽은 snapshot을 받으며 I/O를 하지 않는다. 기존 current 자식은 모두 같은
부모에 속해야 하고, PK가 아직 없는 새 부모에는 기존 자식을 넘길 수 없다. 선택한 collection은 일반 InstanceSet과 같은 pure
reader로 전달한다. 부모와 자식은 복사하며 입력 필드/validator·개수 정책은 기존 SetSpec에서 보존한다.

FK는 서버 identity를 가진 optional `HiddenInput`으로 추가하거나 교체한다. 빈 값/생략은 서버 값을 쓰고 nonempty 제출은
정확한 canonical 숫자여야 한다. 명시한 PK 0도 유효하다. 부모 field는 Changed에 들어가지 않으며 빈 추가 행을 활성화하지 않는다.
부모 표시에는 `ParentIdentity()` 또는 field의 `InlineParent()`를 사용한다. 공격자가 제출한 다른 부모를 재표시하지 않는다.
삭제 행·그대로 둔 빈 추가 행도 잘못된 부모/중복값 제출이면 전체 `invalid_parent`로 거부한다. `PostClean`은 이 FK를 변경할 수 없다.

새 부모의 key가 없어도 자식 Form을 검증할 수 있다. 이때 후보의 FK는 NULL이고, 부모를 포함한 unique tuple은 같은 부모라는
상수를 사용해 형제끼리 비교한다. 개별 `Instance(index).Prepare()`와 전체 `Prepare()`는 부모 key가 없으면 `unsaved_parent`다.
nullable FK여도 연결이 풀린 자식으로 준비하지 않는다. `PrepareWithParent(parent)`는 caller가 부모 저장으로 얻은 key를 별도
자식 후보에 적용한다. 기존 부모의 identity 변경은 거부하고, field/model clean을 재실행하거나 원래 pending snapshot을 바꾸지 않는다.

새 부모를 만드는 흐름에서는 일반 `BindInstance`로 얻은 `preparedParent`와 위의 `children`을 하나의 transaction에 연결한다.

```go
err = backend.AtomicRelation(ctx, func(session db.RelationSession) error {
    // 현재 권한과 최종 모델 제약은 이 scope에서 확인한다.
    if err := preparedParent.Save(ctx, session, &parent); err != nil { return err }
    prepared, err := children.PrepareWithParent(parent)
    if err != nil { return err }
    plan, err := prepared.SavePlan()
    if err != nil { return err }
    _, err = plan.Save(ctx, session, formmodel.SetSaveOptions[models.Label]{})
    return err
})
```

이 예시는 collection 없는 신규 Label이다. Collection saver·기존 행의 변경/삭제·감사 기록은 제품의 저장 정책에서 명시한다.
`PrepareWithParent`는 순수한 key 연결이며 부모의 DB 존재/인가나 commit을 증명하지 않는다. 지연 저장도 같은 준비 결과를 사용하되
부모와 모든 자식의 원자성은 caller가 유지한다. 뒤쪽 자식 실패는 전체 scope를 롤백해야 하며 이미 발급된 parent PK는 Go 값에
남을 수 있다. 자동 재시도하지 않는다. 실제 양 DB 저장 소비자는 [inline 저장 검사](../../examples/helpdesk/inline_save_test.go)에 있다.

[Helpdesk 편집기](../../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)는 저장된 Category의 InlineSet을 실제 HTTP로 사용한다.
[Helpdesk Admin](../../examples/helpdesk/README.md#admin에서-티켓과-보고서를-함께-편집하기)은 새 부모/자식 HTML과 권한별 inline 편집·원자 저장을 연결한다.
`InlineSpec.EmptyForm(parent)`는 validator나 I/O 없이 서버 부모·기본값과 `__prefix__`를 가진 빈 행을 만든다. 자식 PK와 기존
입력값은 포함하지 않는다. [Admin inline](../../admin/inlines.md)은 이를 비활성 prototype으로 렌더링하고 권한·개수 범위에서
브라우저의 미저장 행 추가/제거에 사용한다. 파일의 multipart 수신·Form/Formset 바인딩과 Admin 파일 widget/전송을 연결했다.
일반 여러 행 저장과 모델 파일 게시도 아래와 같은 `PreparedSet.SaveFiles`·`SavePlan`으로 연결한다. Storage alias·URL/인가된 serving·추가 backend는 별도 미완료 범위다.

## 생성 모델의 typed 준비

`orm.Manager.Metadata`는 생성 시점 IR의 복사본, `ModelValues`는 PK 존재와 nullable 값을 보존한 scalar snapshot을 반환한다.
`ApplyValues`는 명시한 scalar만 별도 모델에 적용한다. Field 소유권·타입·nullable 검사 뒤 생성 descriptor의 직접 대입을
사용하고 결과 값·변경하지 않은 field·PK 값과 존재를 다시 확인한다. Metadata와 nullable pointer를 공유하지 않으며
reflection으로 모델 field를 찾거나 DB I/O를 수행하지 않는다. 이 메서드는 default나 full_clean을 암묵적으로 적용하지 않는다.

`BindInstance(manager, spec, data, current, postClean)`은 직접 initial map을 복사하지 않고 typed 현재 모델을 바인딩한다.
`current == nil`이면 IR default와 unsaved 후보를 사용한다. Non-nil은 저장 여부와 별개로 명시한 instance snapshot을 사용한다.
PK가 0이어도 생성 descriptor의 presence를 유지하며 원래 instance나 이후 caller 변경을 공유하지 않는다.

```go
instanceForm, err := formmodel.BindInstance(
    models.ContactObjects, scopedSpec, submitted, current, definition.PostClean,
)
if err != nil { return err }
// 현재 인가된 읽기 scope에서 instanceForm.BoundForm()의 DB 검사를 수행한다.
instanceForm, err = instanceForm.WithErrors(failures)
if err != nil { return err }
prepared, err := instanceForm.Prepare()
if err != nil { return err }
candidate, err := prepared.Model()
if err != nil { return err }
```

`Prepare`는 scalar를 typed 모델로 바꾸고 command 입력은 `Input()`, 선택한 ManyToMany 목록은 `Collections()`에 보존한다.
선택하지 않은 컬렉션과 선택했지만 비운 컬렉션은 다르다. 후자는 명시적 clear intent를 가진 빈 목록이다.
`Model()`은 호출마다 별도 값을 반환하므로 nullable field를 바꿔 다른 결과나 원래 row를 수정하지 않는다.
Prepared 값은 저장 성공도 저장 권한도 아니다. 모든 scalar·컬렉션 쓰기와 현재 권한/revision 재검사는 실제 저장 scope가
계속 소유한다. 준비 자체는 저장하지 않는다. 명시적 scalar/관계 저장 조정은 아래 API를 사용하며 자동 commit·재시도는 하지 않는다.

Django의 15개 native 관찰 중 기존 13개의 값·오류·준비/저장 결과를 대조한다. 나머지 두 사례는 clean이 nonnullable
counter 또는 제외한 hidden field를 NULL로 만드는 경우다. Django는 commit=False 후보를 허용하고 저장에서 IntegrityError를
내지만 Go는 primitive int/string의 zero value로 바꾸지 않도록 준비 단계에서 거부한다. 이 두 준비 시점의 차이와 Python
instance identity·clean 반환 규약의 차이를 native 완전 동등성으로 합치지 않는다.

## 이미 바인딩한 Form과 제품 저장

`PrepareInstance(manager, bound, current)`는 기존 BoundForm을 typed 준비에 연결한다. 다시 입력을 해석하거나 model clean을
호출하지 않으며, 전체 모델 metadata와 PK 값/존재가 일치해야 한다. 선택한 값·명시적 clean 변경은 bound가 소유하고,
선택하지 않은 값은 전달한 typed current가 소유한다. 현재 row에 의존하는 후처리의 최신성은 호출자의 revision 검사 또는
명시적 재바인딩이 계속 보장해야 한다. 이 메서드는 현재 권한이나 row 버전을 검증했다는 표시가 아니다.

Admin의 `ModelConfig.Create/Update`는 최종 재바인딩한 `BoundForm`을 받는다. Registry는 `Input()`의 저장 표현 경계를
통과시킨 뒤 호출하며 원래 제출·cleaned 값·후처리 후보를 함께 보존한다. Typed 모델이 필요한 callback은 `PrepareInstance`를,
별도 credential/session 처리가 필요한 callback은 `bound.Input()`을 사용한다. Command callback의 별도 Values 계약은 유지한다.

Helpdesk Ticket Form은 같은 transaction에서 인가된 principal·현재 row/category·전체 저장 후보의 unique 제약과 label 소속을
확인하고 typed 준비/관계 저장을 사용한다. 신규 모델은 `Save`, 기존 모델은 실제 변경 필드만 ORM의 `UpdateFieldNames`로
저장한 뒤 `SaveCollections`를 호출한다. 제외된 서버 category를 UPDATE에 넣지 않고, 관계만 바뀌면 scalar 쓰기를 생략한다.
Category를 바꾸는 후처리는 저장 전에 거부한다. JSON의 Form 동등성·기존 원문/SQL NULL 보존, 변경 field/audit와 저장된 결과의
응답 변환 확인도 같은 scope에 남긴다. JSON API는 별도 입력/부분 변경 계약을 사용한다.

## Scalar와 선택한 컬렉션 저장

`PreparedInstance.Save(ctx, backend, &candidate, savers...)`는 caller가 소유한 typed 모델을 일반 ORM Save로 저장한 뒤,
선택한 컬렉션을 **Schema IR 선언 순서**로 반영한다. `candidate`는 위의 `prepared.Model()`로 얻고 필요한 서버 변경을
적용할 수 있다. Save는 준비 당시 값을 다시 덮어쓰지 않는다. 같은 pointer를 재사용하면 저장된 PK로 update하며,
다시 `Model()`을 호출하면 원래 준비 snapshot의 새 복사본을 얻으므로 새 객체의 반복 저장에 사용해서는 안 된다.

`SaveManyToMany(relations.ModelsArticleLabels)`는 typed forward binding에서 field 이름과 기본 저장 callback을
만든다. 같은 모델의 전체 IR 정책과 field 이름을 저장 전에 확인하므로 다른 모델/관계로 잘못 연결하면 scalar 쓰기부터
거부한다. Reverse accessor는 모델이 선언한 Form field가 아니므로 구성 단계에서 거부한다.
`CollectionSaver[M]`는 이 기본 adapter나 애플리케이션이 명시한 callback을 담는다. 선택한 컬렉션마다 정확히 하나가 필요하며
누락·중복·nil callback·제외된 field의 등록은 scalar 쓰기 전에 거부한다. 순서는 saver 인자나 Form 표시 순서에 따르지 않는다.
빈 목록은 clear 요청이고 제외된 컬렉션은 호출하지 않는다. Callback마다 분리된 모델/nullable pointee와 key 목록을 전달한다.
Command 입력은 callback에 자동 전달하지 않으며 필요하면 `prepared.Input()`에서 애플리케이션이 별도로 소비한다.

```go
candidate, err := prepared.Model()
if err != nil { return err }
labels, err := formmodel.SaveManyToMany(relations.ModelsArticleLabels)
if err != nil { return err }
err = backend.CoordinatedAtomicRelation(ctx, func(session db.RelationSession) error {
    // 현재 actor·row/revision·선택 대상의 소속/권한을 확인한다.
    // 필요하면 최신 row와 원래 제출로 재바인딩한다.
    return prepared.Save(ctx, session, &candidate, labels)
})
```

기본 adapter는 root에서는 `From`, 빌린 scope에서는 `InSession`을 사용하여 `SetKeys`에 연결한다. 선택적으로
`orm.ManyToManySetOptions[Through]{Clear: ..., ThroughDefaults: ...}`를 한 개 전달할 수 있다. Options 값은 복사하고
기존 link의 ID/payload 보존, 명시적 Clear, through endpoint 보호와 실패 원자성은 원래 ORM 관계 의미를 따른다.
Custom ThroughDefaults는 불투명 callback capability이므로 caller가 immutable·동시 실행 안전성을 보장한다.
생성 시 실행하지 않으며 typed nil은 거부한다. 기본 adapter는 인가·선택의 현재 유효성·외부 transaction을 대신하지 않는다.

별도 감사/인가 처리가 필요하면 기본 saver의 `Save`를 감싸거나 명시적 `CollectionSaver`를 구성할 수 있다.
이는 신뢰하는 애플리케이션 코드이며 callback 자체가 검증되거나 봉인되었다는 의미는 아니다. 기본 saver를 감싼 경우에도
선언된 모델/field 검사는 유지한다. Callback은 전달된 session을 사용하고 오류를 반환해야 한다. 이미 빌린 session에서 root backend의 transaction을 다시
시작하지 않는다. Root backend를 직접 사용하는 경우에는 생성 relation의 `From(backend, owner)`를 사용할 수 있다.
원자적 scope가 없으면 scalar와 먼저 성공한 collection은 뒤의 오류에도 남을 수 있다. Django의 기본 저장도 이 순서다.
여러 쓰기의 원자성과 최종 권한/revision은 호출자의 `AtomicRelation/CoordinatedAtomicRelation`이 소유한다.
Callback의 nil error는 outer commit 성공이 아니며, COMMIT의 `commit_outcome_unknown`은 그대로 전달하고 자동 재시도하지 않는다.

Deferred 흐름은 `candidate`를 원하는 ORM 저장 경로로 저장한 뒤 `prepared.SaveCollections(ctx, backend, candidate, savers...)`를
호출한다. 이 메서드는 scalar를 다시 저장하지 않는다. 선택한 collection이 있으면 PK의 명시적 presence가 필요하고 0도 유효하다.
일반 저장과 마찬가지로 PK의 존재는 저장된 row나 권한의 증명이 아니다. Context 취소와 session lifetime은 빈 작업에서도 확인한다.
실패나 outer rollback 뒤에도 caller 모델의 이미 발급된 PK는 남는다. 다음 시도는 현재 DB 상태를 확인하고 새 작업으로 판단한다.

독립 [생성 소비자](../../codegen/consumertest/testdata/modelsave/consumer_test.go)는 고정 Django의 15개 저장 사례와
추가 인가·현재 row·선택 변경·실패/취소/session 사례를 실제 SQLite/PostgreSQL에서 검증한다. 저장 결과·PK/collection 경계를
대조하지만, 지연 FK 제약으로 COMMIT이 실패하는 두 사례는 Django IntegrityError와 GoDj의 기존 outcome-unknown 오류를
의도적으로 구분한다. DB를 새로 조회한 테스트 결과를 일반적인 commit 보장으로 바꾸지 않는다.

[교차 앱 생성 소비자](../../codegen/consumertest/testdata/manytomany/form_save_test.go)는 기본 adapter의 자동/명시적 through,
nullable endpoint, 자기 참조의 대칭/방향, options 소유권과 borrowed transaction·rollback·만료를 검사한다.


## 모델 파일의 준비와 저장

`schema.FileField("file", "File", schema.Blank(), schema.Default(""))`는 이름을 저장하는 모델 필드다. 기본 max length는
100 Unicode 문자이며 `schema.MaxLength`로 지정한다. 생성된 Go 값은 `string`, nullable이면 `*string`이다. 저장소 선택과
업로드 이름 생성은 runtime callback이 소유하고 모델에는 request upload나 열린 reader를 넣지 않는다.

같은 IR에서 multipart FileField와 clearable widget을 투영한다. `InitialValues`는 저장 이름을 `forms.ExistingFile`로
변환한다. 빈 이름도 ExistingFile로 표현해 SQL NULL과 구분하며 required 검사를 충족시키지는 않는다. 새 업로드가 없으면
기존/default 참조를 유지하고, 명시적 clear는 nullable 모델도 `""`를 기록한다. 일반 POST/JSON 문자열은 업로드가 아니다.
후보와 DB unique 검사는 제안된 이름을 보지만 `Input`은 업로드 capability를 유지한다. Storage가 충돌 이름을 바꿀 수 있으므로
최종 DB unique 제약을 항상 유지한다. Scalar `PostClean.Fields`로 파일 이름을 변경하지 않고 아래 `Name`에서 결정한다.

`InstanceForm.Prepare()`와 `PreparedSetRow.Prepared()`는 파일을 읽거나 저장하지 않는다. `PendingFiles()`가 비어 있지
않으면 `Model()`, `Save()`, `SaveCollections()`는 `upload_pending` 오류를 반환한다. 요청 수명 안에서 먼저 처리한다.

```go
resolved, publications, err := prepared.SaveFiles(ctx, formmodel.FileSaver[models.Document]{
    Field:   "file",
    Backend: files,
    Name: func(instance models.Document, upload uploads.File) (string, error) {
        return "documents/" + upload.Name(), nil
    },
})
// 실패여도 publications에는 이미 시도한 파일의 Info/error/outcome이 남는다.
// caller가 이 결과를 보존하고 조정한다. 자동 재시도/보상 삭제를 하지 않는다.
if err != nil { return err }
instance, err := resolved.Model()
if err != nil { return err }
err = backend.AtomicRelation(ctx, func(tx db.RelationSession) error {
    return resolved.Save(ctx, tx, &instance)
})
// 이 terminal error/outcome을 확인해야 DB 결과를 판단할 수 있다.
// publications의 Published는 DB commit을 의미하지 않는다.
```

위 이름 callback은 pure해야 하며 다른 준비 값과 기존/default 파일 참조가 담긴 분리된 모델을 받는다. 모든 saver와
이름을 먼저 확인한 뒤 IR 순서로 저장한다. Backend가 반환한 이름과 크기, 모델 이름 길이 제약을 검사하며 그 실제 이름으로
모델을 준비한다. `SaveFiles`는 원래 준비나 제출을 바꾸지 않는다. 같은 준비를 다시 저장하면 별도 파일이 생길 수 있으므로
에러 뒤 무조건 재호출하지 않는다. 파일이 만료된 요청 이후에는 그 capability로 저장할 수 없다.

`FilePublication.Field()/Info()/Err()/Outcome()`으로 부분 실패를 확인한다. 새 파일 게시 후 DB rollback이 되면 DB의 이전
참조와 새 파일이 각각 남을 수 있다. Clear·모델 삭제·교체 역시 이전 파일을 자동 삭제하지 않는다. 이름은 공유될 수 있고
불확실한 DB 결과나 파일 소유권을 참조 문자열만으로 확정할 수 없기 때문이다. 권한·revision과 transaction admission은
애플리케이션이 계속 소유한다. [storage](../../storage/README.md), [결정](../../docs/adr/0082-file-storage-publication-and-reference.md)을 따른다.

Read-only JSON 투영은 `serializers.ModelField{Name: "file", ReadOnly: true}`로 저장 이름을 제공한다. Writable JSON 문자열,
자동 URL/다운로드 인가, storage alias·다른 backend·ImageField·파일 choices는 아직 지원하지 않는다.
[독립 생성 소비자](../../codegen/consumertest/testdata/files/consumer_test.go)는 실제 multipart→typed 준비→파일/DB 저장과
DB rollback·clear·모델 삭제·재개방을 실행한다. 실행 source와 환경은 [TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)에 둔다.
