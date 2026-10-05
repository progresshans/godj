# Typed query 입력

하나의 선언이 URL query 변환·Go DTO 할당·OpenAPI parameter를 소유한다. `api/parameters`는
reflect/tag 추론이나 임의 decoder/schema 쌍 없이 현재 필요한 int64와 문자열을 지원한다.
인가·CSRF·대상 범위·DB/context·오류 응답은 handler가 소유한다.

```go
type ListQuery struct {
    Limit  int64
    Search *string
}

query, err := parameters.New(2048,
    parameters.Field("limit", parameters.Default(parameters.DigitsInt64(1, 100), 20),
        func(out *ListQuery, value int64) { out.Limit = value }, "Maximum page size."),
    parameters.Field("search", parameters.Optional(parameters.String(64, true)),
        func(out *ListQuery, value *string) { out.Search = value }, "Literal search text."),
)
// 초기화 시 err를 처리한다. Operation.Parameters에는 query.Parameters()를 사용한다.
// 인증을 통과한 handler에서:
input, diagnostics, err := query.Parse(request.HTTP().URL.RawQuery)
// err는 잘못된 Query 사용, diagnostics는 client 입력 오류다.
// 두 실패를 처리한 뒤 input으로 조회한다.
```

| 선언 | 입력과 schema 의미 |
|---|---|
| `CanonicalInt64(min, max)` | inclusive int64 범위. `FormatInt`의 decimal 표기만 허용하며 `+1`, `01`, `-0`, 공백·소수·지수·overflow를 거부한다. |
| `DigitsInt64(min, max)` | nonnegative inclusive int64 범위. 비어 있지 않은 ASCII digits를 받으며 `001`을 1로 읽는다. |
| `String(maxBytes, allowEmpty)` | trim하지 않는 UTF-8. 디코딩한 byte 길이를 제한하고 NUL을 거부한다. |
| `Required(codec)` | 생략은 `required`. 빈 값의 허용 여부는 codec이 정한다. |
| `Default(codec, value)` | 생략한 때만 검증된 기본값을 사용한다. 같은 값이 schema `default`가 된다. |
| `Optional(codec)` | `*T`로 부재를 표현한다. nil·명시적 0·명시적 빈 문자열을 구분한다. JSON null 변환은 없다. |

`New`는 선언 slice를 복사하고 이름·중복·setter·codec·범위·default·OpenAPI 구성을 검증한다.
오류는 `api.FailureInvalidConfig`이며 잘못된 선언은 `parameters[index]`로 위치를 표시한다. 준비 중 setter를 호출하지 않는다. Zero codec/input/parameter/query는
유효하지 않다. 전체 raw query byte 제한은 1부터 1 MiB, parameter 수는 최대 128이다. 빈 선언은 query 값을
받지 않는 endpoint에 쓸 수 있다. 문서의 전체 query 한도 설명에는 `MaxBytes()`를 사용한다.

`Parse`는 `net/url`의 query decoding을 사용한다. `+`는 공백, `%2B`는 literal plus이며 빈 `&` segment는 무시한다.
잘못된 escape·unescaped semicolon·unknown/duplicate 이름은 `__all__/invalid`다. 이름은 URL decoding 후
대소문자를 구분한다. Scalar 오류는 선언 순서의 첫 필드에 `invalid`를 반환한다. 진단에 입력값을 넣지 않는다.
각 endpoint는 이 진단을 자신의 오류 응답에 연결한다. Helpdesk의 두 목록은 기존 전체 query 오류 표현을 유지한다.

모든 값이 검증된 뒤에만 setter를 선언 순서로 실행한다. 입력 실패에는 zero DTO를 반환하며 setter는 실행하지 않는다.
Setter는 순수하고 동시 호출에 안전해야 하며 DTO 포인터를 외부에 보관하면 안 된다. 초기화한 `Query`는 공유할 수 있지만
결과 DTO와 Optional 포인터는 요청마다 따로 소유한다. 디코딩 문자열은 전체 원본 query의 backing string을 보관하지 않는다.
`Parameters()`의 slice도 분리되며 안의 schema는 불변이다. 순수하고 제한된 parsing에는 I/O나 context 수명을 추가하지 않는다.

정수 범위·default는 일반 JSON Schema에, URL 정수 표기는 `x-godj-query-integer`에 나타난다. 문자열의 `maxLength`는
필요한 code-point 상한이고 더 엄격한 UTF-8 byte 제한은 `x-godj-max-bytes`, NUL 금지는 `x-godj-no-nul`에 나타난다.
일반 generator가 확장 정책까지 검증한다고 가정하지 않는다. 서버 parser가 그 정책을 검사하며 실제 문서·고정 생성물·
독립 HTTP client를 대조한다. `allowEmptyValue`는 문자열의 empty 정책과 함께 나온다.

Helpdesk의 요약 HTML/API와 Label·티켓–Label API 목록이 첫 소비자다. JSON body/model binder, header/path,
반복 query/list 값, 추가 scalar, endpoint DSL과 viewset은 이 package의 현재 범위에 포함하지 않는다.
[장기 의미](../../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)와
[환경별 검증](../../docs/status/TEST_EVIDENCE.md)을 구분한다.
