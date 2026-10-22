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

외부 생성 소비자는 model-free storage app과 실제 모델 app의 의존성, 명시적 사전 migration, worker/DB 수명과 실패 후
기존 데이터 보존을 사용한다. App lifecycle 작성은 C1의 scaffold·command·event/logging·reload 완료를 대신하지 않는다.
설계·구현과 환경별 실행 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 구분한다.
