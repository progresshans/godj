# 설치 앱과 시작·종료

`apps.New`는 이름과 label을 복사한 불변 registry를 만든다. Label은 Schema IR과 같은 소문자 ASCII·숫자·밑줄
namespace이며 첫 글자는 숫자가 될 수 없다. 공백을 제거하거나 Unicode/대문자를 다른 이름으로 바꾸지 않는다.
생성된 `project.InstalledApps()`를 settings에 전달하면 모델 없는 앱도 동일한 identity로 등록된다.

`apps.Start`는 이 registry에 명시적 startup hook과 의존성을 연결한다. 새 app identity를 암묵적으로 추가하지 않는다.
프로젝트가 hook을 조립하며 `init()` 등록이나 process 전역 registry는 없다.

```go
lifetime, err := apps.Start(ctx, configured.Apps(), apps.StartupConfig{
    Hooks: hooks,
    RollbackTimeout: 10 * time.Second,
})
if err != nil {
    return err
}
// 이 범위에서 앱과 그 resource를 사용한다.
runErr := runApplication(ctx)
cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
defer cancel()
return errors.Join(runErr, lifetime.Shutdown(cleanup))
```

각 `Startup`은 `Label`, `DependsOn`, `Start(context.Context, apps.Config) (apps.Stop, error)`를 선언한다.
전체 graph의 중복·없는 app/dependency·cycle을 callback 전에 거부한다. 현재 시작 가능한 앱 중 registry의 선언 순서가
앞선 것을 선택한다. Hook이 없는 앱과 nil Start도 순서에 포함된다. `Order()`는 실제 순서의 독립 복사본이다.

Start에 전달되는 context는 해당 lifecycle에 속한다. Callback은 resource를 획득한 뒤 이를 정리하는 Stop을 반환한다.
Stop을 반환하기 전의 resource는 callback이 소유하며, panic/Goexit 같은 비정상 경로도 callback 자신의 defer로 정리해야 한다.
Stop과 error를 함께 반환하면 framework가 그 Stop부터 역순으로 rollback한다. 시작 실패·취소 때 이미 받은 모든 Stop을
호출하며 한 cleanup 오류 때문에 나머지를 생략하지 않는다. Rollback은 startup의 취소와 분리한 context를 사용하고,
`RollbackTimeout` 0은 10초, 음수는 설정 오류다. Cleanup 실패도 startup 오류에 보존한다.

Callback은 순서대로 실행되며 각각의 실행 goroutine을 완료까지 기다린다. 취소·panic·Goexit 때문에 뒤에 작업을 남긴 채
성공하거나 다음 callback으로 넘어가지 않는다. 취소는 협력적이다. Context를 무시하고 반환하지 않는 callback을 강제로
중단하거나 그 resource를 안전하게 회수한다고 보장하지 않는다. 정상 반환 없는 callback은 `CodeCallbackAborted`로 구분한다.

성공 후에는 project owner가 `Shutdown`을 호출한다. 부모 context 취소는 앱 작업에 전달되지만 Stop을 자동 호출하지 않는다.
`Shutdown`은 먼저 앱 context를 취소하고 Stop을 역순으로 한 번씩 호출한다. Web 요청 drain과 Shutdown의 호출 순서는
project/server owner가 결정한다. 종료 중 recursive/concurrent Shutdown은 `CodeShutdownInProgress`로 즉시 반환하고,
다른 caller는 `Wait(ctx)`로 완료를 관찰할 수 있다. Stop이 자신을 기다리는 lifecycle에 Wait하면 안 된다.
완료 뒤 Shutdown/Wait는 callback을 반복하지 않으며 관찰자의 늦은 취소로 이미 완료한 결과를 바꾸지 않는다.
`Lifecycle`은 복사하지 않는다. 설정과 registry를 재사용한 새 Start는 독립 context·종료 상태를 갖는다.

오류는 code·field·app label을 제공한다. Callback cause는 `errors.Is/As`로 검사할 수 있지만 기본 formatting/JSON에는
그 원문을 넣지 않는다. 반환된 framework 오류를 바꿔도 저장된 종료 결과는 바뀌지 않는다.

[외부 생성 소비자](../codegen/consumertest/testdata/startup/consumer_test.go)는 먼저 명시적으로 migration한 뒤 모델 없는
storage 앱이 SQLite를 열고, 의존 앱이 생성된 모델과 worker를 사용한다. 종료·부분 startup 실패 뒤 worker/DB 회수와
기존 데이터 보존을 검사하도록 작성했다. 같은 source의 실제 실행 여부는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
이 API가 migration·계정 생성·durable event 전달을 암묵적으로 수행하지 않는다.

`StartupConfig.Logger`에 caller가 소유한 `*slog.Logger`를 전달하면 앱별 start/stop과 전체 결과를 기록한다.
Project/invocation은 `Logger.With`로 구분하며 nil은 로그를 만들지 않는다. Callback 원문·설정 값은 기록하지 않고
sink 실패·panic·Goexit가 startup/shutdown 결과를 바꾸지 않는다. Handler도 종료까지 기다리므로 caller가 반환 시간과
sink 수명을 소유한다. Process 내부 알림은 앱이 [`events.Event`](../events/README.md)를 만들고 등록/해제한다.
Stop은 receiver가 모두 끝난 것을 확인한 뒤 그 receiver가 사용하는 resource를 닫아야 한다.
