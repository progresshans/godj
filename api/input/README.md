# Typed JSON 입력

`input.Body[T]`는 기존 `serializers.Spec`의 검증 결과를 typed DTO로 옮기고 같은 Spec의 full/partial OpenAPI
schema를 제공한다. 모델 입력은 `serializers.FromModel`로 Schema IR의 타입·nullable·default·choices와 허용 필드를
가져온다. DTO는 업무에 필요한 Go 구조이며 struct reflection이나 독립 schema/decoder 선언을 사용하지 않는다.

```go
type PatchDTO struct {
    Name     input.Presence[string]
    Priority input.Presence[*int64]
}

func prepare(spec serializers.Spec) (input.Body[PatchDTO], error) {
    return input.New(spec, api.ParserConfig{MaxBodyBytes: 4096},
        input.Field("name", input.String(),
            func(out *PatchDTO, value input.Presence[string]) { out.Name = value }),
        input.Field("priority", input.Nullable(input.Integer()),
            func(out *PatchDTO, value input.Presence[*int64]) { out.Priority = value }),
    )
}
```

이 예시의 Spec은 writable string `name`과 nullable integer `priority`를 선언해야 한다. `New`는 모든 writable
field가 정확히 한 번 연결됐는지, Kind/nullability와 Go codec이 일치하는지, setter·Spec·parser 설정이 유효한지
검사한다. Read-only field는 입력 거부를 위해 Spec에 남고 DTO setter를 갖지 않는다. 누락·중복·알 수 없는 이름과
잘못된 선언은 준비 단계의 오류다. Default는 기존 Spec이 준비한 값을 Go type으로 옮길 수 있는지만 확인한다.

Handler는 필요한 인증·CSRF·권한 확인 뒤 `body.Parse(request, serializers.ModePartial)`을 호출한다. 반환은
`(DTO, validation.Errors, error)`다. Field 검증 실패는 diagnostics이고 body 문법·media·한도·read/취소·잘못된 설정은
Go error다. 기존 `api.RequestErrorResponse`는 client parser 오류만 표현하며 reader failure와 취소는 내부 오류로
남긴다. 이미 decoded Object가 있으면 `body.Bind(ctx, object, mode)`로 같은 순수 검증/변환을 수행한다.

`Presence.Get()`의 bool은 검증 후 effective 값의 존재다.

| 입력 상태 | Full | Partial |
|---|---|---|
| 생략, default 있음 | default, present=true | zero, present=false |
| 생략, required이고 default 없음 | `required` 진단 | zero, present=false |
| 생략, optional이고 default 없음 | zero, present=false | zero, present=false |
| 명시적 false·0·빈 배열 | 검증한 값, present=true | 검증한 값, present=true |
| 허용된 null | Nullable 값 nil, present=true | Nullable 값 nil, present=true |

String의 empty·trim·code-point 한도, Email/URL/Slug의 정규화, choices와 모든 타입의 입력 grammar는 Spec이
소유한다. 오류는 Spec field 순서와 unknown field의 이름 정렬을 유지한다. Full omission default를 제출 값처럼
다시 검증하지 않으므로 legacy choice나 기존 Email default의 의미가 바뀌지 않는다. JSON literal-null default는
`jsonvalue.Value{Text: "null"}`의 non-nil 포인터이고 모델 null은 nil이다. 제출한 JSON null의 정책도 Spec을 따른다.

| Codec | Go type |
|---|---|
| `String` | string; String/Email/URL/Slug field |
| `Boolean`, `Integer`, `Float` | bool, int64, float64 |
| `DateTime`, `Date`, `Time`, `Duration` | time.Time, calendar.Date, clock.Time, duration.Duration |
| `Decimal`, `UUID` | decimal.Decimal, uuid.UUID |
| `Binary`, `JSON` | binaryvalue.Value, jsonvalue.Value |
| `Integers` | []int64 |
| `Nullable(codec)` | *T; nullable field에만 연결 |

전체 검증과 타입 변환이 성공하기 전에는 setter를 호출하지 않는다. 성공한 setter는 Spec 순서로 실행되며 absent
field도 absent Presence를 받는다. Setter는 순수하고 concurrent 요청에 안전해야 하며 DTO pointer를 보관하지 않는다.
Default를 포함한 integer-list는 매번 복사하고 nullable pointer는 요청마다 만든다. String 결과는 정리 전 문자열의
backing storage에서 분리하며 다른 scalar 값 객체의 string은 불변 값으로 공유할 수 있다. 같은 DTO에서 꺼낸
pointer/slice의 변경은 그 DTO의 소유자가 관리한다. 실패나 늦은 취소에는 zero DTO만 반환하며 이미 실행된 잘못된
setter의 외부 부작용까지 되돌리는 transaction을 제공하지는 않는다.

`api.ParserConfig`의 전체 body byte·JSON depth/value/member/array/string/number 예산을 그대로 적용한다. Parser는
읽기 전·Read 사이·decode 이후 context를 확인한다. Body/transport가 진행 중인 Read를 해제하고 borrowed body를
닫는 수명을 소유하며 binder는 body를 닫거나 별도 reader goroutine을 시작하지 않는다. Bind는 검증 전후·변환과
setter 사이·반환 전에 취소를 확인한다.

`body.Schema(mode)`는 같은 Spec의 `openapi.RequestSchema`다. OpenAPI 표준 schema만으로 duplicate member·NUL·
aggregate budget·legacy default와 모든 runtime grammar를 검증했다고 해석하지 않는다. Named component와 route,
관계/고유성 검사·DB transaction·audit·출력 준비는 기존 operation/업무 owner가 계속 소유한다.

실제 연결은 [Helpdesk 선언](../../examples/helpdesk/api_inputs.go), [Label](../../examples/helpdesk/labels_api.go),
[ServiceReport](../../examples/helpdesk/service_reports_api.go)를 참고한다. Ticket bulk 배열·header/path 입력·자동 endpoint와
viewset은 이 API의 구현 범위에 포함하지 않는다. 환경별 검증과 후속 통합은 [실행 증거](../../docs/status/TEST_EVIDENCE.md)에 기록한다.
