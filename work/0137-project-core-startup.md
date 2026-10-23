# GDJ-0137: Project/Core와 새 앱의 기본 개발 흐름

- 상태: 완료 — C1-01~08의 같은 source 필수 원격 실행과 원본 대조 완료
- 시작: 2026-10-23
- 완료: 2026-10-24
- 제품 source: `b19aeeddcfdf6ac2aef5bbf0102d5f13c8a0e1e0`; 환경별 결과와 수용 대조는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
- 근거: [전체 실행 계획 C1](../docs/EXECUTION_PLAN.md), [개발 기준](../docs/DEVELOPMENT_CRITERIA.md),
  [CLI와 프로젝트](../docs/ARCHITECTURE.md#cli와-프로젝트), [개발 흐름](../docs/DEVELOPER_EXPERIENCE.md),
  [전역 도구와 프로젝트](../docs/adr/0004-cli-and-project-binary.md),
  [선언 bootstrap](../docs/adr/0006-codegen-input-package-boundary.md),
  [개발 서버](../docs/adr/0042-project-linked-runserver-and-article-development-loop.md)

빈 디렉터리에서 공개 명령으로 프로젝트와 앱을 시작하고, 같은 모델·설정·권한 경계를 유지한 채
생성·migration·초기 사용자·관리 화면·업무 명령·재시작과 수정 후 reload를 수행할 수 있게 한다.
선행 F1과 C1의 제품 검증을 완료했으며 주 작업은 [I1 사용자 정의 계정](0138-custom-account-lifecycle.md)으로 전환했다.
C1의 완료는 I1과 전체 카탈로그의 남은 기능을 지원으로 바꾸지 않는다.

## 구현

- `settings.Environment`와 `settings/database`가 불변 설정·DB 선택·opener를 제공한다. 앱은 설정 key/default 정책을
  소유하며 `apps.Start`가 명시적 dependency·시작과 역순 정리를 담당한다.
- `startproject`/`startapp`은 앱 선언·module·기본 runtime과 등록 안내를 원자적으로 게시한다. 기존 대상과 source
  충돌·취소에서 부분 프로젝트를 성공으로 반환하지 않는다. 초기 선언 runner는 generated import 없이 시작한다.
- 프로젝트는 management command·typed 인자·resource check와 logger를 명시한다. 전역 CLI가 build·호환성 검사·
  protocol·process 수명을 소유하며, 순수 check와 명시적으로 선택한 DB I/O를 구분한다.
- 도구·linked runtime·생성 ABI의 실제 metadata를 실행 전에 대조한다. 개발 checkout/replacement와 고정 module
  설치를 구분하며 잘못된 조합으로 비밀 입력이나 업무 I/O를 시작하지 않는다.
- `project/server.Run`/`Serve`와 `runserver`는 source/bundle·build·startup이 확인된 후보를 같은 listener에 게시한다.
  실패 후보는 기존 앱을 유지하고 현재 요청의 저장과 drain 뒤 앱·DB·자손을 정리한다. 준비 취소와 runtime 수명을 분리한다.
- 명시적 event는 등록 순서·중첩/동시 전달·해제 admission·callback drain을 소유한다. 기본 slog 진단은 비밀값을
  포함하지 않으며 저장 후 receiver/sink 실패를 이미 확정된 DB 작업의 재시도 승인으로 바꾸지 않는다.
- Scaffold·고정 module 설치·Notes·Article의 실제 공개 소비자가 앱 추가, 기존 데이터와 필드/권한 변경,
  인증된 Admin/API·custom command·실패 복구와 restart를 사용한다. DB와 플랫폼별 검증은 TEST_EVIDENCE에 둔다.

## 하위 요구와 수용 시나리오

| ID | 구현할 결과 | 완료에 필요한 실제 동작 |
|---|---|---|
| C1-01 | `startproject`와 `startapp`으로 실행 가능한 프로젝트·앱 선언을 만든다. | 비어 있는 대상에서 module·설정·선언 runner·runtime·앱 등록과 안내를 연결한다. 모델 생성물이 없어도 선언 runner가 build되고, app 추가 뒤 생성·migration과 기존 데이터가 유지된다. 충돌·잘못된 이름·경로·중단은 기존 파일을 덮거나 부분 성공으로 표시하지 않는다. |
| C1-02 | 환경과 명시적 설정에서 프로젝트별 불변 구성과 필요한 backend resource를 만든다. | 기본 SQLite와 설정한 PostgreSQL에서 같은 공개 흐름을 사용한다. 누락·빈 값·중복/상충 설정·잘못된 타입은 시작 전에 field 단위로 진단한다. secret을 오류·format/log에 포함하지 않고 별도 프로젝트·동시 invocation 사이에 설정이 섞이지 않는다. |
| C1-03 | 명시적인 app startup/shutdown과 process 내부 event를 연결한다. | 선언된 순서/의존성, 중복·cycle·등록 실패를 검사하고 부분 startup 실패 시 이미 연 resource를 역순으로 정리한다. context·callback error·한 번 종료·등록 해제·동시 전달·callback 수명과 재진입을 확인한다. global init registry와 background goroutine의 암묵적 소유권을 만들지 않는다. |
| C1-04 | built-in과 사용자 management command를 project 코드에서 선언하고 전역 CLI로 실행한다. | 목록/help·이름/인자/출력 검증과 취소·오류·stdout/stderr·exit 의미를 연결한다. reserved 이름 충돌과 잘못된 입력은 업무 callback 전에 거부한다. 생성 모델과 현재 DB를 사용하는 외부 custom command가 같은 resource·현재 권한/무결성 경계를 사용한다. |
| C1-05 | 설치 도구·linked runtime·생성 ABI의 호환성을 확인한다. | 실제 버전 정보와 지원 contract를 조회하고 잘못된 조합을 data I/O·장기 runtime 시작 전에 진단한다. 오래된 생성물을 재사용하거나 자동 업그레이드/수정하지 않는다. 개발 source와 고정 module 설치의 식별을 구분하고 실제 외부 module의 정상/불일치 조합을 실행한다. |
| C1-06 | generate→migration→초기 사용자→runserver와 안전한 reload를 기본 구성으로 연결한다. | 로그인·Admin/API 저장과 기존 데이터의 모델 변경·재시작을 수행한다. reload는 source/bundle 검사와 build·startup이 성공한 후보만 게시하며 실패 시 이전 정상 runtime을 유지한다. 연속 변경·취소·부모 종료·늦은 후보·자손 회수와 현재 요청 drain을 실제 process/HTTP에서 확인한다. |
| C1-07 | 기본 structured logging과 startup/command/event 진단을 연결한다. | caller가 logger와 sink를 명시하고 프로젝트·실행 경계를 구분한다. 오류·취소·resource 종료를 관측할 수 있으며 credential·업로드 bytes·private protocol 원문을 기본 로그로 내보내지 않는다. logging 실패가 이미 확정된 DB 결과를 재시도 가능 실패로 바꾸지 않는다. |
| C1-08 | 서로 다른 외부 프로젝트에서 같은 기본 경로와 변경 비용을 검증한다. | 단일 모델과 관계 모델 앱의 처음 생성, 앱 추가, 저장된 데이터가 있는 필드/권한 변경, custom command, reload·진단·복구를 실제 실행한다. 개발 checkout에 있는 private import나 임시 수동 패치 없이 공개 package/CLI만 사용하고 수동 조립 대비 책임 변화를 기록한다. |

C1의 명령 목록은 기존 generate·makemigrations·migrate·showmigrations·sqlmigrate·createsuperuser·runserver에
version·help·startproject·startapp·일반 check·앱 command를 연결하는 범위다. 일반 check는 설정·앱/command 등록·
모델/생성물의 진단과 명시적으로 선택한 I/O 검사를 구분한다. 실패한 검사 뒤 업무 callback이나 자동 수정을 실행하지 않는다.
데이터 fixture load/dump는 K3, DB introspection/이행은 X1, 일반 계정 수명 명령은 I1/I2에서 이어 간다.

## 구현 경계

- 프로젝트가 settings·앱 선언·DB opener·업무 command를 소유한다. 전역 도구는 선택·호환성 확인·build·process·
  source 게시를 조정하며 프로젝트의 DB 설정을 추측하거나 업무 코드를 직접 import하지 않는다.
- 선언 runner와 generated runtime의 import graph를 유지한다. 여러 entrypoint가 같은 선언을 사용하도록
  기본 구성을 제공하고, bootstrap을 깨는 숨은 generated import나 mutable init 등록을 추가하지 않는다.
- 모델 의미는 Schema IR에 남긴다. Scaffold의 app 등록/배치는 IR의 모델·관계·권한 선언을 복제하는 새 원본이 아니다.
- 명령은 context와 I/O·resource 수명을 명시한다. 순수 정의 검증과 실제 backend opening을 구분하며,
  지원하지 않는 동작·호환성·불확실한 durable 결과는 명시적인 오류/결과로 유지한다.
- `generate`, `makemigrations`, `migrate`, 초기 사용자 생성은 명시적인 변경 명령이다. 개발 서버의 source 감시가
  DB migration·계정 생성·repair나 업무 command를 암묵적으로 수행하지 않는다.
- 요청의 data I/O와 process 종료·로그 전송을 같은 transaction으로 취급하지 않는다. 새로운 정상 후보 게시와
  이전 process의 정리 결과를 구분하고 실패 후보를 사용한 응답을 정상 reload 성공으로 쓰지 않는다.
- 현재 지원 platform은 Linux/macOS amd64/arm64다. 플랫폼별 API가 필요하면 경계를 명시하고 compile/skip만으로
  실제 process 동작을 대신하지 않는다. Windows나 새로운 운영 topology를 C1 성공으로 주장하지 않는다.

Process 내부 event는 durable queue나 다중 process 전달을 제공하지 않는다. R2의 전달/재접속 요구는 남는다.
O1은 운영 배포·키 분배·백업/복구·관측/성능과 향후 지원 릴리스의 upgrade 정책을 맡는다.
C2는 일반 Web 경로·static/media 등의 잔여 기능을 맡는다. 이 구분은 C1의 실제 기본 앱 흐름과 진단 책임을 줄이지 않는다.

## 독립 기준과 개발 흐름

고정 Django의 프로젝트/app 생성·management command·app 초기화·signal의 관찰 가능한 의미는
[호환성 기준](../docs/COMPATIBILITY.md)에 따라 독립 관측한다. Python import/weakref/내부 autoreloader 구조는
구현 목표가 아니다. Go의 typed 등록·resource 수명·source 게시·process 교체는 별도 native 실패/경합 검사로 확인한다.

독립 [개발 관측기](../conformance/runners/django/development_reference/)는 실제 공개 생성 명령·두 앱 초기화·SQLite·HTTP로
reload/명시적 migration/현재 권한/명령 durable 결과를 기록한다. Notes·Article·scaffold command와 event의 실제
Go 결과를 같은 CI source·원본 checksum에 묶어 대조했다. 고정 module의 두 version은 전체 source로 만든 test proxy
fixture이며 배포 릴리스나 일반 upgrade 정책의 증거가 아니다.

수동 Article 조립과 starter·공개 package의 책임 변화는 [개발 흐름](../docs/DEVELOPER_EXPERIENCE.md#새-프로젝트의-조립과-변경-책임)에
기록한다. 공개 소비자의 동일 변경과 조립 책임을 비교했으며 개발 시간 단축이나 성능 수치는 측정하지 않았다.

## 완료 조건

- [x] F1 전체 검증 완료와 C1 주 작업 전환을 확인한다.
- [x] C1-01~05를 빈 디렉터리와 독립 설치/외부 module에서 실제 사용한다.
- [x] C1-06~07의 기존 데이터·reload·event/logging·process 실패 경계를 검증한다.
- [x] C1-08의 서로 다른 앱과 동일 변경·오용/실패·독립 기준을 대조한다.
- [x] 같은 source의 전체 필수 CI 원본과 현행 문서를 대조하고 I1의 요구·경계를 유지한다.

실행 source·재실행의 원래 실패·필수 목록·별도 owner의 검사와 원본 hash는
[TEST_EVIDENCE의 C1 수용 기록](../docs/status/TEST_EVIDENCE.md#gdj-0137--projectcore-전체-원격-검증과-i1-전환)에 있다.
운영 배포·추가 Web 기능·durable event와 사용자 정의 계정은 각 후속 묶음에 유지한다.
