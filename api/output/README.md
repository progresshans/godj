# Typed API 출력

`api/output`은 공개할 필드와 Go getter를 한 번 선언해 JSON 출력 검증과 OpenAPI schema에 함께 사용한다.
객체를 reflection으로 열지 않으며 getter의 DTO·반환 타입이 맞지 않으면 compile 단계에서 실패한다.
아래 선언은 초기화 때 준비하고 요청 사이에 공유한다.

```go
type Summary struct {
    Priority *int64
    Open     int64
}

shape := output.Named("Summary", output.Object(
    output.Field("priority", output.Nullable(output.Int64()),
        func(value Summary) *int64 { return value.Priority }),
    output.Field("open", output.Int64Range(0, math.MaxInt64),
        func(value Summary) int64 { return value.Open }),
))
response, err := output.New(output.Array(shape, 0, 20), serializers.Limits{})
if err != nil {
    return err
}
components, err := output.Components(response.Declaration())
if err != nil {
    return err
}
```

`response.Schema()`를 해당 `openapi.Response.Schema`에, `components`를 `openapi.Config.Schemas`에 넣는다.
인가·조회가 끝난 handler는 `response.JSON(request.Context(), http.StatusOK, rows)`를 반환한다.
완전한 조합은 [Helpdesk 출력 선언](../../examples/helpdesk/api_output.go)과
[operation 구성](../../examples/helpdesk/api.go)에 있다. `Encode(ctx, value)`는 응답 대신 완성된 JSON bytes를 반환한다.

모든 객체 필드는 필수다. `Nullable`의 nil은 `null`, non-nil은 원래 타입의 값이며 필드를 생략하지 않는다.
`Array`의 nil slice는 `[]`다. 정수는 exact int64이고 범위는 양 끝을 포함한다. 배열 길이 범위도 같은 방식으로
실제 출력과 schema에 적용한다. String·Boolean은 입력 trimming·default·choices를 적용하지 않는다.
추가 primitive가 필요한 모델은 `output.Model(encoder)`로 기존 `serializers.ModelEncoder`를 연결한다.
이 연결은 encoder가 소유한 동일한 Spec에서 schema를 얻으므로 별도 Spec이나 임의 schema를 붙일 수 없다.
모델 필드 허용 목록·NULL·길이·Decimal/JSON 등 기존 codec·명시적 computed field의 의미를 보존한다.

`Named`가 반환한 Shape를 재사용하면 같은 component identity를 공유한다. 별도로 만든 두 Named 선언이 같은 이름을
사용하면 구조가 같아도 실패한다. 여러 응답의 component는 `output.Components(a.Declaration(), b.Declaration())`로
모아 중복된 공유 선언을 한 번만 넣는다. 기존 수동 schema와의 충돌은 최종 `openapi.New`가 확인한다.
Zero shape·property·output, nil getter, 중복/잘못된 이름, 잘못된 한도와 schema graph는 초기화 오류다.

JSON의 전체 bytes·깊이·값 개수와 개별 객체/배열/문자열/숫자 한도는 `serializers.Limits`를 따른다.
Zero 필드는 기본값을 쓰고 hard cap을 넘는 설정은 실패한다. Projection은 컨테이너의 길이와 남은 예산을 확인한 뒤
필드를 순서대로 읽으며, 모델과 중첩 JSON까지 같은 예산을 사용한다. Context가 없거나 취소됐거나 값/출력이 잘못됐으면
부분 bytes와 부분 HTTP 응답을 반환하지 않는다. 오류의 cause에는 선언 위치와 원인을 보존하지만 응답에는 그대로 노출하지 않는다.

선언 slice와 출력 bytes의 소유권은 분리한다. Getter는 순수하고 동시 호출에 안전해야 하며, 요청의 DTO·포인터·slice는
출력이 끝날 때까지 변경하지 않는다. Getter에서 I/O나 lazy relation 조회를 하지 않는다. 조회와 transaction은 handler가
소유하며 쓰기 작업의 출력 검증을 commit 뒤로 미루면 안 된다. `serializers.Projection`은 이 실행을 위한 요청별 lazy view이며
응답 cache나 immutable model snapshot으로 사용하지 않는다.

Typed endpoint에서는 `Prepare(ctx, status, value)`로 같은 검증·encode를 마친 `Prepared[T]`를 반환한다.
이는 DTO 대신 완성된 응답을 보관하므로 transaction 안에서 준비하고 commit 뒤에 반환할 수 있다.
`Response(prepared)`는 그 Output 또는 복사본의 결과만 받아 getter를 다시 실행하지 않는다. 독립적으로 준비한
같은 schema/타입/한도라도 다른 Output의 응답은 거부한다. Zero 또는 실패한 준비 결과도 사용할 수 없다.
`Prepared.WithHeaders`는 body를 유지하며 완전한 header 집합을 교체·복사한다.
[Endpoint](../endpoint/README.md)가 성공 상태와 JSON 표현을 실제 문서 선언에 연결한다.

입력 binder, 선택적 필드 생략, arbitrary struct/schema/encoder 연결과 순환 schema는 이 출력 API의 지원 범위가 아니다.
기존의 명시적 `api.JSON`, `serializers.Value`와 수동 OpenAPI 선언도 사용할 수 있다.
