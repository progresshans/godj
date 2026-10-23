# 프로젝트 서버의 수명

`server.Run(ctx, Config)`는 한 번의 앱 준비·listener 생성·준비 상태 출력·HTTP 실행·정리를 소유한다.
프로젝트가 환경 설정과 앱 구성을 만들고, `Start`가 `Runtime{Application, Stop}`을 반환한다.
`Listen`은 context를 받아 `net.Listener`를 연다. 설정·migration·초기 사용자·background worker를 암묵적으로 만들지 않는다.
실제 조립은 [Article 진입점](../../examples/article/cmd/site/main.go)과
[독립 generated 앱 소비자](../../codegen/consumertest/testdata/startup/server_test.go)를 따른다.

```go
err := server.Run(ctx, server.Config{
    Start: openApplication,
    Listen: func(ctx context.Context) (net.Listener, error) {
        var listen net.ListenConfig
        return listen.Listen(ctx, "tcp", "127.0.0.1:8000")
    },
    Ready: reportListener,
    HTTP: web.ServerOptions{ShutdownTimeout: 5 * time.Second},
    CleanupTimeout: 10 * time.Second,
})
```

`openApplication`은 프로젝트의 DB/앱 resource를 열고 application과 cleanup을 반환하는 함수다.
`apps.Start`를 사용했다면 `Runtime.Stop`에 해당 lifecycle의 `Shutdown`을 연결할 수 있다.
DB가 dependency 앱 소유라면 handler와 다른 앱의 정리가 끝난 뒤 그 앱이 DB를 닫는다.
`reportListener`는 `ListenerInfo{Network, Address}`를 받아 출력 등 명시적 I/O를 수행한다.
이 callback 동안에는 HTTP serving이 아직 시작하지 않았으므로 같은 서버의 HTTP 응답을 동기적으로 기다리지 않는다.

## 시작과 실패

- Config와 HTTP 옵션을 먼저 검사한다. `Start`가 끝난 뒤에만 `Listen`을 호출하므로 인증·공개 범위 등의 정책을 network 공개 전에 거부할 수 있다.
- `Start`는 반환 전까지 resource를 소유한다. Panic/Goexit 때도 그 함수의 defer가 새 resource를 정리해야 한다.
  `Runtime.Stop`과 error를 함께 반환하면 Run이 그 부분 획득의 정리도 맡는다.
- `Listen`이 listener와 error를 함께 반환하면 그 listener도 닫는다. nil/typed-nil listener는 시작 실패다.
  전달한 listener는 `net.Listener`의 Close/Accept 계약을 따르며, 소유권 이전 뒤 다른 owner가 닫지 않는다.
- `Start`, `Listen`, `Ready`, `Stop`은 callback 종료까지 기다린다. Panic 값은 버리고 Goexit도 명시적인 callback 오류로 보고한다.
- `Ready`의 성공은 bound address의 게시를 뜻한다. 외부 접근성이나 deployment 검증이 아니다. 이후 취소는 정상 종료이며 serving/cleanup 오류가 있으면 실패로 반환한다.

## 취소와 종료

Startup의 application context는 caller의 값과 취소 cause를 전달한다. Context deadline 자체를 영구 앱 수명에 복사하지 않고
startup 동안 caller 취소를 연결한다. Ready 뒤에는 이 연결을 해제하고 caller 취소로 HTTP drain을 시작한다.
진행 중인 요청을 처리하는 동안 앱 worker나 DB resource를 먼저 멈추지 않는다.

HTTP drain 기한이 지나면 연결을 강제로 닫지만, `web.Server`가 handler와 stream Close의 반환을 기다린다.
그다음 application context를 취소하고 `Stop`을 한 번 호출한다. Stop에는 caller의 취소에서 분리한 cleanup context를 주며
값은 유지한다. `CleanupTimeout`의 기본값은 10초이고 HTTP의 graceful drain 기한과 별개다.
취소에 협력하지 않는 callback을 강제로 종료할 수는 없으며, 동작 중인 callback을 버리고 resource를 닫지 않는다.

Listener close는 net/http와 최종 owner 사이에서 한 번만 실행한다. Listener 정리가 실패해도 Stop을 시도한다.
시작/serving 오류와 cleanup 오류를 함께 보존하고, 이미 확정된 application 결과를 자동으로 재시도하지 않는다.
기본 오류·fmt·JSON은 callback 원문과 resource 내부를 출력하지 않는다. 상세 cause는 명시적인 `errors.Is/As`로 검사한다.

`Config.Logger`는 caller가 선택한 `*slog.Logger`다. Start·ready·HTTP drain·Stop·전체 Run의 닫힌 결과와 context의
취소 여부를 기록하며 주소·요청·credential·callback 원문은 넣지 않는다. `Logger.With`로 프로젝트/실행 ID를 붙인다.
로그는 별도 goroutine에 맡겨두지 않고 handler 반환을 기다린다. Sink 오류·panic·Goexit는 원래 결과를 바꾸지 않으며
nil은 아무 로그도 만들지 않는다. Handler의 추가 정보·concurrency·출력 resource의 수명은 caller가 소유한다.
