# ADR-0093: 명시적 앱 시작 순서와 resource 종료

- 상태: Accepted design; 구현 작성, 원격 검증 대기
- 날짜: 2026-10-23
- 관련 작업: [C1 / GDJ-0137](../../work/0137-project-core-startup.md)
- 관련 경계: [Schema IR](0001-schema-ir-as-canonical-source.md), [프로젝트 실행 owner](0004-cli-and-project-binary.md)

설치 앱 목록은 모델·Admin·Web이 사용하는 identity snapshot이다. 여기에 callback을 전역 등록하거나 import만으로
resource를 열면 선언 runner의 bootstrap과 별도 프로젝트의 설정·종료 수명이 섞인다. 모델 없는 앱도 같은 명시적
설치 목록에서 infrastructure resource를 소유할 수 있어야 한다.

Registry는 Schema IR의 canonical label 규칙을 공유하고 identity만 보관한다. Startup hook과 의존성은 project가
별도로 조립한다. 중복·없는 app/dependency·cycle은 callback 실행 전에 모두 검사한다. 현재 준비된 앱 중 선언 순서가
앞선 앱을 선택하므로 결과는 결정적이며 dependency를 hook 입력 순서로 대체하지 않는다.

Start는 callback과 반환된 Stop의 소유권을 명확히 한다. Callback은 Stop을 반환하기 전까지 자신의 부분 획득 resource를
소유한다. Stop+error도 유효한 실패 전달이며 framework가 그 Stop과 앞서 받은 Stop을 역순으로 호출한다.
초기화 중 취소는 정상 handle을 게시하지 않는다. Rollback에는 취소된 시작 context를 재사용하지 않고 caller가 정한
협력적 정리 시간을 적용한다. Cleanup 오류를 원래 startup 오류와 함께 남긴다.

Callback마다 종료를 관찰하는 goroutine을 만들고 반드시 완료를 기다린다. 이는 panic/Goexit에도 원래 제어 흐름과
나머지 cleanup을 유지하기 위한 실행 경계다. 취소 시 callback을 버려두고 뒤늦은 Stop 반환이나 resource 획득이 발생하게
하지 않는다. 반환하지 않는 Go callback을 강제 종료하는 보장은 없다. 일반 callback 오류는 원인을 명시적으로 검사할 수
있게 보존하고 panic 원문은 공개 진단에 싣지 않는다.

성공한 lifecycle의 Shutdown은 context 취소와 역순 Stop을 한 번 수행한다. Caller가 명시적으로 호출하며 숨은 취소 감시
goroutine은 만들지 않는다. 종료 중 Shutdown에 재진입하면 즉시 진행 중 오류를 반환해 callback이 자기 종료를 기다리는
deadlock을 피한다. 동시 caller의 완료 관찰은 Wait로 분리한다. 완료 결과는 독립된 framework 오류 사본으로 반환한다.
Web drain·process 교체·DB/파일 게시와 app lifecycle을 하나의 transaction처럼 취급하지 않는다.

`project/server.Run`은 프로젝트의 Start·Listen·Ready·HTTP Serve·Stop을 연결한다. Start를 Listen보다 먼저 실행해
인증/공개 정책의 거부가 listener 생성보다 앞서게 한다. Start/Listen이 cleanup 가능한 resource와 error를 함께 반환하면
부분 획득도 해당 owner가 정리한다. 공통 callback 실행 경계는 apps와 server가 공유하며 의미별 오류와 순서는 각 owner가 정한다.

Caller 취소를 앱 context에 영구 연결하면 HTTP drain 중에도 worker가 멈추거나 DB를 닫을 수 있다. 따라서 startup 동안만
caller 취소를 전달하고, Ready 이후에는 caller 취소로 HTTP drain을 시작한다. 전체 handler/stream cleanup이 끝나면 앱 context를
취소하고 별도 cleanup deadline으로 Stop을 호출한다. Context 값은 유지하며 준비 이후의 취소는 정상 종료지만 serving/cleanup
오류를 숨기지 않는다. 이 분리는 새 runtime의 source 게시나 reload의 성공을 의미하지 않는다.

외부 생성 소비자는 model-free storage app과 실제 모델 app의 의존성, 명시적 사전 migration, worker/DB 수명과 실패 후
기존 데이터 보존을 사용한다. App lifecycle 작성은 C1 전체 수용이나 reload 완료를 대신하지 않는다.
설계·구현과 환경별 실행 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 구분한다.

Process 내부 이벤트는 앱이 소유하는 typed `events.Event[T]`로 조립한다. 전역 signal registry나 숨은 background queue를
두지 않는다. 등록 순서 snapshot에서 동기 전달하고 일반 오류는 첫 실패 중단/결과 수집 후 계속 전달을 구분한다.
등록 해제는 아직 시작하지 않은 callback의 admission을 막고, 완료 대기는 별도 Wait로 둔다. 이를 통해 callback 안의
자기 해제는 막히지 않으면서 resource owner가 종료를 확인할 수 있다. 취소된 Wait는 정리 완료가 아니며 live callback의
resource를 닫을 권한을 주지 않는다. Callback은 자체 작업을 join하고 caller는 성공한 drain 뒤에만 resource를 해제한다.

같은 event의 동시 Send는 receiver에 동시에 진입할 수 있으며 payload는 Go 대입으로 전달한다. Mutable 참조의 동기화는
caller가 소유한다. Callback context의 활성 frame을 따라 자기/상위 대기를 명시적 오류로 막고, 완료 frame은 그 판단에서
제외한다. Django의 weak reference·sender object identity·중복 UID 묵시적 무시는 복제하지 않는다. 현재 snapshot에서의
disconnect 의미 차이는 [독립 기준](../../conformance/runners/django/event_reference/README.md)에 공개한다.

Logging은 caller가 명시적으로 전달한 표준 `*slog.Logger`와 그 sink를 사용한다. Framework는 닫힌 동작/결과·등록 이름과
취소 여부만 기록하며 error 원문·인자·credential·payload를 자동 기록하지 않는다. Logger 추가 속성과 sink의 안전성/수명은
caller 책임이다. Handler도 callback 경계에서 join하되 반환 오류·panic·Goexit를 업무 결과에 합치지 않는다. 이미 확정된
저장 결과를 로그 전송 실패로 재시도 가능 실패로 만들지 않기 때문이다. 반환하지 않는 handler의 지연을 강제로 해결하거나
운영 audit의 내구성을 보장하지 않는다. Durable event·배포 관측 정책은 R2/O1에 남는다.
