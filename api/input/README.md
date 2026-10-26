# Typed JSON 입력

`input.Body[T]`는 기존 `serializers.Spec`의 검증 결과를 typed DTO로 옮기고 같은 Spec의 full/partial OpenAPI
schema를 제공한다. 모델 입력은 `serializers.FromModel`로 Schema IR의 타입·nullable·default·choices와 허용 필드를
가져온다. DTO는 업무에 필요한 Go 구조이며 struct reflection이나 독립 schema/decoder 선언을 사용하지 않는다.

## 모델과 공통 CRUD 입력

`NewModel(manager, spec, parserConfig)`는 별도 DTO·setter 없이 `Body[orm.ModelInput[M]]`을 만든다.
Spec은 해당 manager와 완전히 같은 IR로 `FromModel`에서 만들어져야 한다. 이름만 같은 generic Spec이나
다른 default·hidden field·table source는 시작 시 거부한다. `NewResource(resource, spec, parserConfig)`는
JSON의 쓰기 노출이 [CRUD Resource](../../crud/README.md)의 명시적 목록을 넓히지 않는지도 확인한다.

`ModelInput.Get`은 생략·SQL NULL·명시적 zero를 구분하고, collection은 별도의 owned 선택으로 보존한다.
`Manager.PrepareInput(input, current)`는 최신 current 객체의 제외 열을 보존하며 nil이면 전체 IR default를 적용한다.
`forms/model.PrepareInput`도 같은 입력을 사용한다. 폼은 초기 객체 key binding까지 전달하므로 다른 대상에
재사용할 수 없다. 이 준비는 인가·full_clean·commit 증거가 아니며, 관계 선택·고유성·출력 준비는 소유 write
scope에서 확인한다. Pending upload는 일반 scalar 전달이 아닌 파일 게시 경로를 사용한다.

아래의 `New`·`Property` 경로는 임의 DTO와 업무 명령에 계속 사용할 수 있다.

## 모델 입력과 명령의 객체 결합

`Object(parserConfig, ObjectField(name, childBody, setter), ...)`는 이미 준비한 `Body`들을 별도 객체 member로
결합한 `Body[T]`를 만든다. 예를 들어 실제 모델에 `password`라는 프로필 필드가 있어도 모델의 `fields.password`와
명령의 `credential.password`는 서로 다른 입력이다. 각 child의 모델 출처·허용 필드·타입·default·choices와
full/partial 의미를 유지하며, 필드 이름으로 모델이나 저장 동작을 추론하지 않는다.

```go
type Credential struct { Password input.Presence[*string] }
type Creation[M any] struct {
    Fields orm.ModelInput[M]
    Credential Credential
}

func creationBody[M any](model input.Body[orm.ModelInput[M]], command input.Body[Credential]) (input.Body[Creation[M]], error) {
    return input.Object(api.ParserConfig{MaxBodyBytes: 64 * 1024},
        input.ObjectField("fields", model, func(out *Creation[M], value orm.ModelInput[M]) { out.Fields = value }),
        input.ObjectField("credential", command, func(out *Creation[M], value Credential) { out.Credential = value }),
    )
}
```

입력은 `{"fields": {...}, "credential": {...}}`다. 바깥 객체 member는 **full과 partial 모두 필수**이고 null·배열·scalar를
받지 않는다. Mode는 각 child 내부 field의 생략/default에 적용한다. Optional·nullable 객체 member와 자동 관계 저장은
이 API의 기능이 아니다. 필요한 command field의 필수·nullable 정책은 그 child Spec에서 별도로 선언한다.

요청 body는 한 번 읽으며 `Object`에 준 parser 설정이 전체 bytes·depth·values·member/array/string/number 제한을
소유한다. Child의 단건 HTTP parser를 호출하거나 각 child에서 예산을 재시작하지 않는다. 각 Spec의 JSONField 경로만
arbitrary JSON member name을 허용하며 다른 위치의 같은 이름이나 배열 안으로 권한이 번지지 않는다.
`serializers.ObjectDecoder`는 이 grammar만 선언하는 불변 객체이며 model 출처나 검증/쓰기 권한을 부여하지 않는다.
`NewObjectDecoder(spec)`와 `ComposeObjectDecoder`를 `api.Parser.ParseObjectWith`/`ParseListWith`에도 사용할 수 있다.
선언은 공유 child의 매 사용을 세어 최대 4,096개 object node·64개 object 깊이를 허용하고, 실제 문서에는 parser의 더 작은
기본값/설정 한도가 별도로 적용된다. Zero child·중복/잘못된 이름·nil setter·과한 graph는 준비 단계에서 거부한다.

전체 child의 Spec 검증·타입 변환과 unknown member 검사가 성공하기 전에는 어느 child/parent setter도 실행하지 않는다.
Default를 이미 제출한 값으로 재검증하지 않아 legacy default와 JSON null/SQL NULL 의미도 유지한다. Setter 순서는
object 선언 순서와 각 child의 기존 Spec 순서다. 취소에는 이후 setter와 부분 DTO를 게시하지 않으며, setter는 순수해야 한다.

결합된 입력의 diagnostic field는 JSON Pointer다. 예를 들어 `/fields/email`, `/credential/password`,
`/outer/child/name`처럼 나타내고 원래 이름의 `~`와 `/`는 각각 `~0`, `~1`로 escape한다.
각 child의 진단 순서를 유지한 뒤 바깥 unknown member를 이름 순으로 추가한다. Diagnostic params와 값 비노출 정책을
유지한다. `Schema(mode)`도 같은 child schema로 만든 닫힌 객체이며 `JSONBody`/`JSONListBody`가 그대로 사용할 수 있다.

아래는 단일 Spec에서 임의 DTO를 구성하는 예시다.

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

## 객체 배열

`NewList(body, ListConfig, optionalValidator)`는 같은 `Body[T]`를 `ListBody[T]`로 연결한다.
`Parse`/`Bind`는 `[]T, validation.Errors, error`를 반환하고 `Schema(mode)`는 같은 item schema의 배열이다.
`MinItems`는 0 이상, `MaxItems`는 1..1024이며 minimum이 maximum보다 클 수 없다. 업무는 그 안에서 더 작은
개수 범위를 선택한다. `Bounds()`는 준비한 inclusive 범위를 반환한다.

```go
list, err := input.NewList(body, input.ListConfig{
    MinItems: 1, MaxItems: 40,
    Parser: api.ParserConfig{MaxBodyBytes: 40 * 4096},
    ItemLimits: serializers.Limits{MaxDocumentBytes: 4096},
})
```

`Parser`는 전체 배열의 bytes·depth·values 등 자원 정책이고, `ItemLimits`는 각 객체를 compact JSON으로 표현한
예산이다. 원래 Body의 단건 parser 예산을 배열 전체에 재사용하지 않는다. 배열 parser는 한 번만 body를 읽으며,
각 객체에 같은 Spec의 full/partial 검증과 typed 변환을 수행한다. 개수 실패는 전체 `__all__/invalid_count`,
항목 예산 실패는 해당 행의 `__all__/limit_exceeded`다. Field 진단은 입력 행 순서와 행 안의 기존 진단 순서를 유지한다.

진단의 `index`는 0부터 시작하는 바깥 행 번호다. Integer-list field가 이미 가진 `index`는 `item_index`로 옮겨
두 위치를 구별한다. 단건 Body의 field `index`는 바뀌지 않는다. 중복·모호한 위치나 표현할 수 없는 진단 문자열은
설정/내부 오류다. 진단은 최대 16,384개를 보관하며 이를 넘으면 전체 개수를 담은 `__all__/too_many_errors` 하나로
거부한다. 일부 오류만 전체 결과처럼 반환하지 않으며 초과 뒤의 진단도 문자열·위치와 context를 확인한다.

선택적인 `ListValidator[T]`는 Spec과 typed binding이 성공한 한 행을 검사한다. 순수하고 동시 요청에 안전해야 하며
DTO·pointer·slice를 변경하거나 보관하지 않고 I/O·commit을 하지 않는다. Field 진단은 다른 행의 검증을 막지 않지만
Go 오류·취소는 즉시 중단한다. Validator의 일반 오류가 parser의 400/413/415로 바뀌지 않는다. 다른 행이 실패했어도
유효한 행의 순수 setter/validator는 실행될 수 있다. 요청 전체가 실패하면 부분 DTO slice는 반환하지 않는다.

실제 연결은 [Helpdesk 선언](../../examples/helpdesk/api_inputs.go), [여러 티켓 생성](../../examples/helpdesk/ticket_bulk_api.go),
[Article 배열 endpoint](../../examples/article/apiapp/bulk_create.go)와
[선택 계정 생성 소비자](../../codegen/consumertest/testdata/accountbinding/creation_test.go)를 참고한다.
객체 배열도 같은 `Object`를 사용하며 행 전체의 결합이 실패하면 그 행의 어떤 setter도 실행하지 않는다.
앞선 유효 행의 setter/validator는 실행될 수 있고, 실패한 요청은 부분 행 slice를 반환하지 않는다.
자동 nested 관계 저장과 viewset은 후속 범위다. Header·경로/endpoint 연결은 [api/endpoint](../endpoint/README.md)가 소유한다.
환경별 검증과 후속 통합은 [실행 증거](../../docs/status/TEST_EVIDENCE.md)에 기록한다.
