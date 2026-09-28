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
선언만 하고 바꾸지 않은 제외 필드는 저장 입력에 추가하지 않는다. `Input()`과 typed instance 준비는 I/O를 수행하지 않으며,
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

실제 생성 모델의 준비·SQLite/PostgreSQL 저장·rollback과 고정 Django 13개 사례의 비교는
[독립 생성 소비자](../../codegen/consumertest/testdata/modelclean/consumer_test.go)가 검증한다.
일반 `ModelForm.save()` 자동 생성, 관계 컬렉션 저장·전체 constraint 종류·formset/files는 이 API의 존재만으로 완료되지 않는다.
장기 의미는 [ADR-0080](../../docs/adr/0080-model-blank-policy-and-form-post-clean.md), 실행 범위는
[TEST_EVIDENCE](../../docs/status/TEST_EVIDENCE.md)를 따른다.
