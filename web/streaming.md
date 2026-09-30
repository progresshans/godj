# 유한 스트림과 인가된 파일 응답

`NewStreamResponse(status, headers, open)`은 요청마다 독립 reader를 여는 불변 응답 설명을 만든다. 생성과 `WithHeaders`는
파일을 열거나 본문을 읽지 않는다. 전체 handler/middleware chain이 성공하고 borrowed Request·multipart 자원을 해제한 뒤
Application이 opener를 실행한다. Middleware가 응답을 교체하거나 오류를 반환하면 원래 opener는 실행하지 않는다.

Opener는 transport의 `context.Context`를 받으며 `Stream{Reader: reader, Size: size}`를 반환한다. Size는 정확한 byte 수,
모르면 -1이다. Reader는 빈 파일도 EOF로 끝나야 한다. 기본 `Config.MaxStreamBytes`는 32 MiB이며 0은 기본값, 음수는 오류다.
`MaxResponseBytes`의 기본 1 MiB는 buffered 응답에 적용된다. 전송 버퍼는 32 KiB로 고정하고 무진행 reader도 유한하게 거부한다.
`Response.Body()`는 `([]byte, error)`이며 buffered body의 복사본만 반환한다. Stream은 `CodeBodyNotBuffered`, zero response는
`CodeInvalidResponse`다. `Streaming()`과 `WithHeaders`를 사용하면 session cookie나 API header adapter가 본문을 소비하지 않는다.

Application은 opener가 error와 함께 돌려준 reader도 정확히 한 번 닫는다. Opener가 획득한 뒤 panic 등으로 반환하지 못한
자원은 opener가 정리해야 한다. Request·upload·borrowed DB transaction을 closure에 보관하지 않는다. 이미 확인한 이름 등
불변 값과 application 수명의 backend를 캡처한다. 각 reader의 I/O는 context를 존중해야 하며 runtime은 read/write 사이에
취소를 검사한다. 임의의 context 비협조 reader가 막힌 Read를 framework가 강제로 중단하지는 않는다.

Header 전 open/read/크기 오류는 상세 없는 500이다. Header를 보낸 뒤의 오류는 `http.ErrAbortHandler`로 HTTP 연결/stream을
중단하며 오류 본문이나 정상 EOF를 덧붙이지 않는다. 알려진 길이의 마지막 조각은 EOF를 확인한 뒤 쓰므로 초과·누락·늦은
reader 오류가 완전한 Content-Length 성공처럼 보이지 않는다. Close 실패는 별도 진단이며 전달된 본문을 다시 실패 응답으로
바꾸지 않는다. Framing/hop-by-hop header는 runtime이 소유하며 사용자 header로 지정할 수 없다. 일반 handler 오류를 panic으로
전달하는 API가 아니라 이미 시작된 HTTP 전송을 끝낼 수 없음을 net/http에 알리는 transport 규약이다.

HEAD도 명시적으로 route를 등록한다. Stream을 열어 metadata를 확인하고 닫지만 Read하지 않는다. Buffered HEAD 역시 GET과
같은 Content-Length를 유지하며 본문을 쓰지 않는다. Server는 middleware 종료 뒤 실제 파일 전송까지 in-flight 요청으로 drain한다.

## 모델 파일 다운로드

```go
// 이 handler에 도달하기 전에 현재 principal과 view permission을 확인한다.
id, ok := request.Int64Parameter("id")
if !ok { return web.Response{}, errors.New("missing document key") }
rows, err := models.DocumentObjects.Using(database).
    Filter(models.DocumentFields.ID.Exact(id),
        models.DocumentFields.Owner.Exact(principal.ID())).All(request.Context())
if err != nil { return web.Response{}, err }
if len(rows) != 1 || rows[0].File == "" {
    return web.NewResponse(http.StatusNotFound, nil, nil)
}
files, err := request.Settings().Storages().Lookup("documents")
if err != nil { return web.Response{}, err }
return web.FileResponse(files, rows[0].File, web.FileOptions{})
```

`FileResponse`는 명시적으로 인가한 저장 이름 하나를 열며 디렉터리 route를 공개하지 않는다. Request의 파일 이름/alias를
그대로 사용하지 않고 요청마다 새 model query와 현재 권한으로 이름을 결정한다. 예제의 권한 확인은 그 요청의 admission
snapshot이며 전송 도중 바뀌는 권한을 소급 취소하는 lease API는 아니다. 별도의 transaction 정책이 필요하면 애플리케이션이 소유한다.

기본 응답은 `attachment`, `application/octet-stream`, `Cache-Control: no-store`, `X-Content-Type-Options: nosniff`,
`Referrer-Policy: no-referrer`다. `FileOptions`로 filename·MIME·inline을 명시할 수 있다. 파일 확장자/본문을 자동 MIME 추측에
사용하지 않는다. `storage.Reader.Info()`를 구현한 reader는 **같은 열린 handle**의 이름/크기를 제공한다. 그 외 backend는
알 수 없는 길이로 전송하며 `Stat` 후 `Open`하는 경쟁을 만들지 않는다. 인가 후 missing file은 404, 다른 storage 실패는 500이다.

Range·conditional GET·sendfile·비동기/SSE/WebSocket·공개 media directory·서명 URL 발급 provider는 별도 미완료 범위다.
URL을 만드는 [storage capability](../storage/README.md)는 서버 route나 다운로드 권한을 만들지 않는다.
[생성 소비자](../codegen/consumertest/testdata/files/serving_test.go)가 실제 로그인/CSRF·multipart·모델/소유권·HTTP 전송을 연결한다.
실행한 source·DB·race·platform 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
