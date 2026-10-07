# Typed HTTP endpoint

`api/endpoint`는 typed 입력·준비 응답·명시한 인가와 상태에서 실제 HTTP handler와 OpenAPI operation을 함께 만든다.
입력은 기존 `parameters.Query[T]` 또는 `input.Body[T]`, 출력은 `output.Output[T]`를 사용한다.
모델의 검증 원본·조회·transaction 경계와 응답 encode 시점은 각 API가 소유한다.

```go
type Query struct { Page int64 }
type Result struct { Page int64 }

query, err := parameters.New(128, parameters.Field(
    "page", parameters.Default(parameters.CanonicalInt64(1, 20), int64(1)),
    func(value *Query, page int64) { value.Page = page }, "Page.",
))
if err != nil { return err }
response, err := output.New(output.Named("Result", output.Object(
    output.Field("page", output.Int64(), func(value Result) int64 { return value.Page }),
)), serializers.Limits{})
if err != nil { return err }
prepared, err := endpoint.New(authentication, endpoint.Config[Query, Result]{
    Route: web.Route{Name: "items:list", Method: "GET", Path: "/api/items/"},
    Admission: endpoint.Authenticated(),
    Input: endpoint.Query(query), Output: response,
    Success: []endpoint.Status{{Code: 200, Description: "Page."}},
    Handle: func(request *web.Request, actor auth.Principal, value Query) (output.Prepared[Result], error) {
        return response.Prepare(request.Context(), 200, Result{Page: value.Page})
    },
})
if err != nil { return err }
operations, schemas, err := endpoint.Collect([]endpoint.Endpoint{prepared})
if err != nil { return err }
```

`operations`와 `schemas`를 같은 authentication을 사용하는 `openapi.Config`에 넣는다. 최종 `openapi.New`가
수동 operation을 포함한 전체 경로·이름·schema 충돌을 검사하고 `Document.Routes()`를 HTTP application에 등록한다.
Authentication은 실제 transport를 설명하는 `api.AuthenticationDescriber`도 제공해야 한다. 알 수 없는 profile은 초기화 때 거부한다.
JSON negotiation은 기존 `api.JSONPolicy`와 실제 middleware가 소유한다. Endpoint는 `Route.Handler`가 이미 설정된
선언을 거부한다. 초기화에서 getter·setter·handler·DB I/O를 실행하지 않으며 각 callback은 동시 요청에 안전해야 한다.

`All(first, more...)`는 모든 권한, `Any(first, alternatives...)`는 명시한 권한 중 하나를 요구한다.
`Authenticated()`는 모델 권한과 독립적인 인증, `CSRFOnly(requireSessionProof)`는 Session adapter의 익명 CSRF admission이다.
마지막 경우 handler에는 zero Principal을 전달하며 `requireSessionProof`는 application이 직접 검증하는 session proof의
문서 선언이다. 이 bool이 proof 검증을 수행하지는 않는다. Zero admission·빈/중복 권한·없는 adapter capability는 준비 오류다.
인증 transport와 CSRF는 기존 adapter가 검증하고 typed 입력을 읽기 전에 끝낸다. 같은 permission snapshot을 실행과 문서에 쓴다.

`Query(query)`는 같은 bounded URL parser/parameter를 연결한다. `JSONBody(name, body, mode, description)`는
같은 full/partial model 입력의 JSON parser와 named schema를 연결한다. 같은 Input 값을 재사용하면 component도 공유하며,
독립 선언의 같은 이름은 오류다. `NoInput()`은 입력을 읽지 않으며 query를 검사하지도 않는다.
`NoQuery(input)`은 비어 있지 않은 raw query를 wrapped parser 이전에 거부한다. Query parameter가 선언된 Input과는 조합할 수 없다.
각 Input은 자체 client 오류 상태를 자동으로 선언한다. Query는 400, JSON body는 400/413/415이며
`Errors`에서 같은 상태의 업무 설명을 지정하거나 다른 4xx를 추가할 수 있다.

Handler는 성공 응답을 `Output.Prepare(ctx, status, dto)`로 완전히 encode하고 반환한다.
정확히 그 Output 또는 그 복사본이 만든 응답만 허용한다. 같은 Go 타입·schema라도 별도 Output이나 다른 예산에서
준비한 응답은 거부한다. `Prepared[T]`는 원 DTO/가변 slice·getter를 보관하지 않고 완성된 bytes만 보유한다.
서로 다른 DTO의 Prepared 타입 사이의 대입·명시적 변환은 compile 오류다.
성공 상태는 선언한 JSON 응답을 갖는 2xx이고 204/205는 허용하지 않는다. 다른 상태, zero 응답, JSON 이외/중복 Content-Type은
실행 오류다. `Prepared.WithHeaders`는 header를 교체·복사하며 Content-Type을 포함한 완전한 header 집합을 넘겨야 한다.
추가 header와 인증 profile의 응답 header는 해당 application/adapter가 소유한다.

쓰기 handler는 transaction 안에서 출력 준비를 끝내고 commit 성공이 확인된 뒤 준비 응답을 반환한다.
Endpoint가 commit 뒤 getter를 다시 실행하지 않는다. Handler는 취소·불확실한 결과를 Go 오류로 반환하고,
endpoint는 오류와 함께 온 준비 응답을 버린다. 반면 handler가 nil 오류로 확정한 준비 결과는 그 뒤의 context 취소만으로
실패로 바꾸지 않는다. 이 경계가 이미 확인된 commit을 재시도할 쓰기로 오인하는 일을 막는다.
예상한 client 거부만 `endpoint.Reject(status, code, diagnostics)`로 반환하며 그 상태는 `Errors`에 선언해야 한다.
이 오류를 직접 반환할 때만 client 응답으로 처리한다. Wrapped/joined 오류를 내부에서 찾아 4xx로 바꾸지 않으므로
rollback 실패·불확실한 transaction 결과가 일반 입력 오류로 바뀌지 않는다. 원 Go 오류는 내부에서 보존하고 HTTP 오류 처리는
Web layer에 맡긴다. 직접 반환하는 진단은 기존 API error envelope의 공개 값 계약을 따른다.

실제 사용 예시는 [요약 조회](../../examples/helpdesk/ticket_summary_api.go)와
[Label ensure의 원자 저장](../../examples/helpdesk/label_ensure.go)에 있다.
Header/path typed binder, query/body를 합친 typed 입력, 배열 body, streaming/no-content 성공, 여러 종류의 성공 DTO,
자동 CRUD/viewset은 후속 범위다. 현재 수동 handler/OpenAPI API와 혼합할 수 있으며 `Collect`의 추가 Output declaration으로
그 수동 operation의 named schema도 함께 모을 수 있다.
