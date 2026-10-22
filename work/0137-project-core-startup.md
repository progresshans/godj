# GDJ-0137: Project/Core와 새 앱의 기본 개발 흐름

- 상태: C1 환경·모델 없는 앱 기반 작성 — F1의 같은 source 원격 검증은 별도 작업트리에서 진행 중
- 시작: 2026-10-23
- 현재 기반: `680b6cd297c731a15f0e4aee5da924bbc92f07f9`; 이 source의 F1 전체 실행 완료를 뜻하지 않는다.
- 근거: [전체 실행 계획 C1](../docs/EXECUTION_PLAN.md), [개발 기준](../docs/DEVELOPMENT_CRITERIA.md),
  [CLI와 프로젝트](../docs/ARCHITECTURE.md#cli와-프로젝트), [개발 흐름](../docs/DEVELOPER_EXPERIENCE.md),
  [전역 도구와 프로젝트](../docs/adr/0004-cli-and-project-binary.md),
  [선언 bootstrap](../docs/adr/0006-codegen-input-package-boundary.md),
  [개발 서버](../docs/adr/0042-project-linked-runserver-and-article-development-loop.md)

빈 디렉터리에서 공개 명령으로 프로젝트와 앱을 시작하고, 같은 모델·설정·권한 경계를 유지한 채
생성·migration·초기 사용자·관리 화면·업무 명령·재시작과 수정 후 reload를 수행할 수 있게 한다.
F1의 검증 대기 동안 독립 요구와 설계를 준비하며, F1의 완료 기록을 앞당기거나 미실행 검사를 지우지 않는다.

## 현재 출발점

- `settings.New`는 프로젝트 이름·설치 앱·storage alias의 불변 snapshot을 만든다. 일반 환경 설정은
  아직 예제의 `databaseconfig` 등에 나뉘어 있다. `apps.Registry`는 선언 순서의 identity 목록이며 lifecycle은 없다.
- 전역 `godj`는 project 선택과 build/process를 소유한다. 선언만 import하는 `project.Run`은
  generate·migration·초기 사용자 protocol을 실행하고, generated package를 import하는 Web runtime은 별도다.
- `generate`는 canonical IR과 generator ABI를 snapshot하며 기존 bundle을 보존하는 게시·복구 경계가 있다.
  새 명령은 이 의미를 재사용하고 private file format·publish 절차를 임의 복제하지 않는다.
- 현재 `runserver`는 선언 build와 bundle 검사 후 별도 runtime을 build하고 다시 검사한다.
  loopback 실행·종료·자손 회수 경계는 있지만 watch/reload와 일반 project command 등록은 없다.
- Article는 DB·identity·Form/Admin/API의 실제 소비자다. 현재 이를 복사해 시작하는 수동 조립을
  새 project/app 기본 경로로 연결하되 예제의 도메인 모델을 모든 새 프로젝트에 강제하지 않는다.
- 명령별 wire version과 생성 ABI는 이미 있다. 설치한 전역 도구·linked runtime·생성물의
  호환성을 사용자 관점에서 진단하는 공통 경계는 아직 없다.

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

## 구현과 검증 순서

1. Scaffold가 생성할 실제 외부 앱의 공개 조립을 정하고, 필요한 settings·lifecycle·command 경계를 함께 구현한다.
   API 이름을 먼저 고정해 비어 있는 추상 계층만 추가하지 않는다.
2. 설치/호환성 진단, 기존 generator/migration/operator와 새 기본 runtime을 같은 CLI 흐름에 연결한다.
   처음 생성과 기존 데이터가 있는 앱 확장 모두에서 source·resource 소유권을 확인한다.
3. 실행 후보의 build·readiness·게시·drain이 분리된 reload와 logging/event를 실제 소비자에 연결한다.
4. 제품·생성기·소비자·실패 검사를 묶어 작성하고 gofmt·최소 compile 뒤 push한다. DB·브라우저·race·process·
   외부 설치·전체 platform/cold-build는 원격 CI owner에서 실행하고 같은 source 원본을 대조한다.

고정 Django의 프로젝트/app 생성·management command·app 초기화·signal의 관찰 가능한 의미는
[호환성 기준](../docs/COMPATIBILITY.md)에 따라 독립 관측한다. Python import/weakref/내부 autoreloader 구조는
구현 목표가 아니다. Go의 typed 등록·resource 수명·source 게시·process 교체는 별도 native 실패/경합 검사로 확인한다.
성능이나 개발 시간 단축은 실제 측정한 경우에만 기록한다. 파일 수나 최소 compile을 사용성/기능 완료로 계산하지 않는다.

외부 의미를 확인할 공식 문서는 Django 6.1의 [프로젝트/app 명령](https://docs.djangoproject.com/en/6.1/ref/django-admin/),
[사용자 command](https://docs.djangoproject.com/en/6.1/howto/custom-management-commands/),
[앱 초기화](https://docs.djangoproject.com/en/6.1/ref/applications/),
[signal 전달](https://docs.djangoproject.com/en/6.1/topics/signals/)이다. 실제 관측기는 기존 profile의 고정 source를 사용한다.
Go의 command 충돌·handler 소유권·취소·종료가 다른 부분은 관측 입력과 지원 정책을 구분해 기록한다.

## 완료 조건

- [ ] F1 전체 검증 완료와 C1 주 작업 전환을 확인한다.
- [ ] C1-01~05를 빈 디렉터리와 독립 설치/외부 module에서 실제 사용한다.
- [ ] C1-06~07의 기존 데이터·reload·event/logging·process 실패 경계를 연결하고 검증한다.
- [ ] C1-08의 서로 다른 앱과 동일 변경·오용/실패·독립 기준을 대조한다.
- [ ] 같은 source의 전체 필수 CI 원본과 현행 문서를 대조하고 다음 I1의 요구·경계를 유지한다.

공통 `settings.Environment`를 Article 진입점에 연결했고, IR과 생성기는 모델 없는 앱의 identity·snapshot을
유지하도록 확장했다. 생성된 `InstalledApps`를 Article Web/Admin 설정에 연결해 수동 앱 이름·label 반복을 제거했다.
값 보존·환경 격리·동시 읽기·secret 비노출과 빈/외부/관계 앱·첫 모델 추가·혼합 생성물 거부 검사를 작성했다.
새 소비자의 실제 실행은 원격 CI 대상이다. 다음 구현은 이 기반을 사용하는 scaffold·resource/lifecycle 조립이다.
실행 상세와 미검증 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다. C1 전체 구현·검증 완료는 아니다.
