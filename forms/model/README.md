# 모델 Form의 후보와 후처리

Schema IR에서 선택한 필드를 `Definition`으로 투영한다. `Bind`는 원래 제출과 Form의 cleaned data를 보존하고,
현재/default 모델 후보를 별도로 만든다. 모델 field cleaning 뒤 `PostClean.Clean`, 읽기 전용 `PostClean.Validators`를
실행한다. 필드가 실패해도 후처리는 실행하며, 값 변환이 기존 오류를 없애거나 field cleaning을 다시 실행하지 않는다.

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
PK·컬렉션·추가 command input·알 수 없는 field는 변경할 수 없다. 선언하지 않은 변경이나 잘못된 값 표현은
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
일반 `ModelForm.save()` 자동 생성, 관계 컬렉션 저장·전체 constraint 종류·formset/files는 이 API의 존재만으로 완료되지 않는다.
장기 의미는 [ADR-0080](../../docs/adr/0080-model-blank-policy-and-form-post-clean.md), 실행 범위는
[TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)를 따른다.

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
계속 소유한다. 이 API에 자동 commit·관계 저장·재시도는 포함하지 않는다.

Django의 15개 native 관찰 중 기존 13개의 값·오류·준비/저장 결과를 대조한다. 나머지 두 사례는 clean이 nonnullable
counter 또는 제외한 hidden field를 NULL로 만드는 경우다. Django는 commit=False 후보를 허용하고 저장에서 IntegrityError를
내지만 Go는 primitive int/string의 zero value로 바꾸지 않도록 준비 단계에서 거부한다. 이 두 준비 시점의 차이와 Python
instance identity·clean 반환 규약의 차이를 native 완전 동등성으로 합치지 않는다.
