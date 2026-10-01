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

## 조건부 조회와 부분 다운로드

FileResponse는 전체 middleware 승인·Open 뒤 GET/HEAD의 조건을 평가한다. 같은 열린 `Info.ContentMetadata()`의 Version은
안전한 opaque ETag로, Modified는 Last-Modified로 표현한다. Memory는 immutable bytes의 해시를 제공하고 filesystem은
수정 시각만 제공한다. 현재보다 미래인 수정 시각은 현재 초로 제한한다. 값이 없으면 해당 validator를 발명하지 않는다.
Conditional 요청 자체가 caching이나 접근 권한을 만들지 않으며 기본 `no-store`와 매 요청의 모델/권한 검사는 그대로다.

If-Match/If-Unmodified-Since, If-None-Match/If-Modified-Since, If-Range 순서로 판단한다. 요청의 ETag 조건이 있으면 같은 단계의 날짜 조건보다
우선하며 If-Match는 strong, If-None-Match는 weak 비교다. 이미 연 파일의 존재로 wildcard를 판단한다. 알려지지 않은/잘못된
날짜는 무시하고 잘못된 ETag 목록은 400으로 거부한다. 일치하는 조회는 304, 실패한 전제는 412이며 본문을 read/seek하지 않고
reader는 닫는다. 304에는 Content-Length/Content-Type을 넣지 않고 cookie·cache 정책과 validator를 보존한다. 이 API는
handler가 이미 수행한 POST 등의 쓰기를 보호하는 precondition API가 아니다. GET/HEAD 외 메서드에는 조회 조건을 적용하지 않는다.

GET의 Range는 같은 handle이 `storage.SeekableReader`이고 크기를 알 때 처리한다. Single·suffix·open-ended·여러 byte 범위를
지원하며 요청 순서를 보존한다. 단일 범위는 206/Content-Range, 여러 범위는 길이를 계산한 multipart/byteranges다. 전체 파일을
복사하거나 전송 goroutine을 만들지 않고 같은 reader를 seek해 32 KiB 이하 조각으로 전송한다. 알려지지 않은 range unit과
빈 파일은 전체 응답으로 처리한다. 비어 있는/잘못된 byte 범위나 모두 파일 밖인 범위는 416과 `Content-Range: bytes */size`다.
너무 큰 정수도 overflow 없이 해석한다. HEAD는 Range를 무시하고 전체 GET의 길이를 유지하며 본문을 read/seek하지 않는다.

Range와 conditional 관련 request field는 합계 8 KiB/64개 값, ETag 목록은 64개 구간, Range 목록은 빈 항목을 포함해 16개로 제한한다. Header 한도 초과는
431, 범위 개수 초과는 416이다. 겹친 범위의 합이 전체 파일보다 크면 전체 응답을 선택한다. `MaxStreamBytes`는 선택한 응답의
길이에 적용하며 multipart header/경계도 포함한다. 따라서 큰 파일의 작은 일부는 전송할 수 있고 과도한 framing도 거부한다.
If-Range의 strong ETag나 `ModifiedStrong`인 정확한 시각이 일치할 때만 재개한다. 기본 filesystem의 약한 시각만으로는 안전한
재개를 주장하지 않고 전체 내용을 반환한다. Seek 불가/길이 미상 reader는 `Accept-Ranges: none`으로 전체 전송한다.

FileResponse의 ETag/Last-Modified/Accept-Ranges/Content-Range/Content-Encoding은 opened content의 표현 경계가 소유하므로
`WithHeaders`로 위조하지 않는다. 별도 on-the-fly 압축은 제공하지 않는다. `Response.Status()`/`Header()`는 아직 I/O를 하지 않은
기본 설명이며 실제 206/304/412/416 선택은 전송 단계에서 일어난다. Reader·seek/read/metadata 실패의 정리와 header 후 abort는
일반 stream과 같다. 외부 writer가 이미 연 filesystem 파일을 직접 수정하는 상황을 immutable snapshot으로 바꾸지는 않는다.

조건의 공통 결과는 [고정 Django 관찰](../conformance/runners/django/file_conditional_reference.py)과 비교한다. Django helper의
Range 미지원·잘못된 ETag 목록 일부 수용·ETag 없는 wildcard 처리와 차이를 명시한다. 부분 전송은 HTTP 표준을 기준으로 독립
구현하고 Go 표준 라이브러리의 공통 범위 결과도 대조한다. Source와 실제 검증 범위는 TEST_EVIDENCE를 따른다.

sendfile·비동기/SSE/WebSocket·공개 media directory·서명 URL 발급 provider는 별도 미완료 범위다.
URL을 만드는 [storage capability](../storage/README.md)는 서버 route나 다운로드 권한을 만들지 않는다.
[생성 소비자](../codegen/consumertest/testdata/files/serving_test.go)가 실제 로그인/CSRF·multipart·모델/소유권·HTTP 전송을 연결한다.
실행한 source·DB·race·platform 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
