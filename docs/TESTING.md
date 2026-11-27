# 검증 실행

테스트는 중요한 위험을 찾아내는 도구다. 테스트 이름·파일 수·옛 byte roster 자체를 보존하기 위해 제품 개발을 멈추지 않는다.
제품 동작, Go-native 안전성, Django 비교와 실제 프로세스·DB 검증은 목적이 다르므로 필요한 위치에서 실행한다.

## 작업 중

한 가지 설계 변경에 필요한 제품 코드·생성기·테스트를 먼저 함께 정리한다. 로컬에서는 포맷·필요한 최소 compile 확인과
실패 재현을 수행한다. 변경 묶음에 gofmt를 적용하고 push하면 원격 CI가 영향 테스트를 포함한 전체 검증을 실행한다.
아래 명령은 필요한 로컬 재현의 예시이며 package/test를 실패 원인과 변경 영향에 맞게 좁힌다.

```sh
go test ./schema/... ./query/...
go test ./orm -count=1
```

실제로 존재하는 selector를 선택하고 테스트가 하나도 실행되지 않은 성공을 근거로 사용하지 않는다.
생성기·IR·모델 선언 변경에는 `make generate-check`와 관련 whole-candidate/external compile을 포함한다.
순수 parsing/validation의 각 입력마다 외부 앱 전체를 다시 build하지 않는다. Public API·generated ABI 변경은 소비자 compile까지 확인한다.

## 검증 범위

| 범위 | 소유하는 위험 | 실행 시점 |
|---|---|---|
| 로컬 feedback | formatting, 필요한 compile와 실패 재현 | 편집·문제 조사 |
| 관련 integration | SQLite/PostgreSQL 실제 동작, relation/cache, security, rollback와 필요한 race | 원격 CI |
| CLI/process | 실제 linked build/child, TTY/signal/cancel/reap, private workspace와 output | 원격 CI |
| Reference | pinned Django/DRF actual, normalizer/comparator와 source authority | 원격 CI |
| 전체 platform | OS/arch/mode, cold build, external archive와 전체 기능 간 조합 | 원격 CI |
| 장기 검증 | 반복 stress/fuzz, 큰 입력, RSS·성능 | 관련 위험 또는 주기적 실행 |

`make quick`, `make generate-check`, 관련 `go-test-*`는 PostgreSQL capture 없이 실행할 수 있다.
`make ci`는 현재 소스에 맞는 실제 PostgreSQL capture를 추가로 요구하는 전체 로컬 gate다.
Capture가 없으면 시작 단계에서 실패하며, 준비 방법은 아래를 따른다. 빠른 검사의 성공은 전체 CI·다른 DB/platform의 성공이 아니다.

## 원격 CI 실행 범위

일반 PR과 Draft PR은 생성·갱신·재개할 때 Linux amd64·arm64의 기존 제품 검사를 모두 실행한다. SQLite·PostgreSQL, 생성물 drift, race·CGO=0, 격리된 cold build, 브라우저와 독립 Django 대조가 여기에 포함된다.
일반 PR은 Apple Silicon 일반 모드의 프로젝트·reload·명령, Apple Silicon race·CGO=0의 네이티브 필수 검사, Intel 일반 호환성, 고정 Darwin 참조를 실행한다. 공용 `*_unix.go` 변경은 Apple Silicon 제품을 세 모드로 확대하고 Intel CGO=0 호환성을 추가한다. 이때 필수 네이티브 검사는 Apple Silicon reload 두 작업에서 실행한다.

`platform-obligations-v3`의 full은 다음 실행을 요구한다. [실행 계획](../scripts/ci/workflow.py) · [네이티브 선택 목록](../scripts/ci/native_suites.py)

| 기존 검사와 책임 | full에서 실제 실행하는 곳 |
|---|---|
| ORM·계정·schema·negative compile, 생성 소비자의 모든 바이너리 발견 root와 필수 child, DB 전체 | Linux amd64·arm64의 일반·race·CGO=0 및 기존 PostgreSQL 분할 |
| 프로젝트 두 버전 설치·기동·reload·외부 명령 | Linux 여섯 좌표와 Apple Silicon 세 좌표. 제품 기준·reload 원본 18개와 독립 Python 원본 한 개를 대조 |
| 관계 runtime 패키지·SQLite·compile·godjcheck·vet | Linux 전체와 Apple Silicon 일반 모드 전체 |
| 외부 relation·mixed collection·앱 시작/정리, 실제 독립 SDK의 HTTP·DB, 첫 계정과 파일 수명 | Apple Silicon 일반·race·CGO=0. Custom 13개, Default 생성/재접속·잘못된 프로필, 실제 filesystem·SQLite·multipart·이미지·range/조건부 응답 |
| PTY·FD·파일 공개·잠금·취소·다중 프로세스 SQLite | Apple Silicon reload의 기존 13개 root. Intel 일반 모드는 같은 집합과 파일 handle·pipe·잠금 경계를 실행 |
| Intel 설치와 호환성 | 일반은 한 버전 공개 설치·첫 계정·HTTP 쓰기·재시작·reload 거부·정리, CGO=0은 CLI와 실제 생성 runner·server·command, SQLite 저장·재접속 |
| Darwin 빌드 태그·타입·링크 | Linux 작업이 Darwin amd64·arm64 CGO=0 환경에서 각각 패키지를 발견하고 체크인된 코드와 패키지별 테스트 실행 파일을 컴파일 |
| 고정 Python·SQLite·Django 관측 | 기존 macOS 15 Apple Silicon exact 환경 |

PostgreSQL normal의 core·operator-target 두 작업이 각각 시스템 상태와 운영자 증거를 생성한다. conformance는 이 두 작업이 끝나면 원본을 대조한다. 나머지 PostgreSQL 작업은 최종 CI 집계에 모두 필수다. 각 비교는 업로드 바로 다음 단계의 artifact ID·SHA-256, 실제 성공한 job ID·회차, 제출 소스와 checkout에 묶인다.

생성 계정 fixture의 독립적인 DB 검사는 [backend별 대응 목록](../scripts/ci/account-database-owners.txt)에서 관리한다. 각 행의 SQLite 경로는 Linux 두 아키텍처 관계 검사의 같은 모드가, PostgreSQL 경로는 해당 PostgreSQL 분할이 맡는다. 계획은 두 실제 필수 목록과 대조하며 PostgreSQL 실행 원본에서 필수 자식의 정확히 한 번 실행·통과를 확인한다. 양 DB의 결과를 직접 대조하는 Django reference와 시스템·운영자 attestation은 해당 원래 작업에서 두 DB를 계속 실행한다.

FirstAccount의 관찰용 관리 명령은 최종 생성 소스에서 fixture별로 한 번 빌드한다. 각 초기화·조회는 소스 지문을 다시 확인한 뒤 독립 프로세스에서 실행한다. 공개 첫 계정 명령의 선언·런타임 빌드, 호환성 검사, PTY 복구와 자식 회수는 각 시나리오에서 실행한다. 네이티브 원본에는 관찰 명령의 실제 빌드 좌표·지문, PID, 빌드·조회·공개 명령별 실행 시간이 남는다.

Linux의 Darwin 교차 컴파일은 두 대상의 패키지를 발견하고 실제 Mach-O 링크와 빌드 정보를 검증한다. 러너·Go 도구 버전별 객체 캐시를 복원하고, 같은 계열의 저장본이 없을 때만 새로 저장한다. Go는 입력이 바뀐 객체를 다시 빌드한다. 링크된 테스트 파일은 검증 후 폐기하고 별도 cold-build 제품 테스트는 자체 임시 캐시를 사용한다.

전용 빌드 캐시 실험은 Linux ARM64 race의 계정 catalog transition 작업과 Apple Silicon 일반 runtime에 한정한다. Go 모듈과 빌드 객체는 별도로 저장한다. 잠금 파일·OS·아키텍처·도구 버전이 같은 실행이 재사용하며, 빌드 객체는 작업 종류별로 저장 담당을 하나만 둔다. 소스가 달라진 객체는 Go가 다시 빌드한다. 소스 커밋별 archive 저장을 피하고, 복원·저장 시간과 실제 검사 시간을 함께 측정한다.

Mac에서 생략되는 조합은 Intel race, 두 Mac에서 공통 생성 기능 전수의 교차 조합, Apple Silicon race·CGO=0의 runtime 전수다. Go 런타임·링커·DB 드라이버의 해당 조합에서만 나타나는 결함은 남을 수 있다. 해당 플랫폼 재현이나 아키텍처별 코드·의존성 변경의 구체적 위험이 있을 때 필요한 조합을 별도로 진단한다. 동적으로 생성하는 외부 모듈은 대표 네이티브 실행이 검증한다.

PR이 루트 `go.mod`·`go.sum`, `*_darwin.go`·`*_darwin_*.go`를 변경하거나 삭제하면 전체 플랫폼을 자동 실행한다. 계획기는 이벤트의 base·head와 실제 병합 커밋의 양쪽 부모를 확인하고, 이름 변경 전후 경로를 포함해 비교한다. main push와 수동 실행도 전체 플랫폼을 실행한다. 릴리스 후보는 해당 소스에서 전체 결과를 확인한다. [선택 기준](../scripts/ci/platform_policy.py)

일반·공용 Unix·full의 제품 좌표는 각각 7·9·9개다. 계획이 선택한 작업만 실행하고 모든 소유 작업의 성공을 요구한다. 같은 PR의 새 push는 진행 중인 이전 실행을 취소한다. 최종 집계는 같은 소스에서 범위를 다시 계산하고 모든 필수 작업의 성공을 요구한다. 일반 PR은 `profile=pr`, 공용 Unix 변경은 `profile=native`, 전체 플랫폼 성공은 `profile=full`과 `full_platform_verified=true`로 기록한다.

Makefile과 workflow가 실제 명령·platform matrix를 소유한다. 계약별 profile·manifest·oracle·baseline과 제품 실행 여부는
[suite catalog](../conformance/suites.json)가 한 번 선언한다. `scripts/conformance.py plan`으로 실제 실행 인자를 확인한다.
Reference check와 product check는 각각 checker를 한 번 build하고, 계약마다 새 process를 실행한다. Python oracle은 각 profile의
locked project에서 새 process로 관측하며 Go actual·expected를 공통 구현으로 만들지 않는다. 이 문서에 명령별 테스트 수·해시·Job/Step 수를 복사하지 않는다.
[Makefile](../Makefile), [workflows](../.github/workflows/)의 현재 설정을 사용한다.

## 반복 실행과 cache

외부 프로젝트의 오프라인 테스트에 필요한 모듈은 `make project-command-dependencies`로 준비한다. 같은 Go 캐시 키를 복원해도 추가 모듈 다운로드가 필요할 수 있다. 확인된 일시적 전송 오류에는 같은 `go mod download all`을 최대 두 번 더 실행한다. 체크섬·권한 오류와 중단 신호는 즉시 실패로 처리한다.

Linux 32비트 컴파일과 관계 테스트는 일반 Portable core에서 한 번 실행한다. PostgreSQL 원본을 기다리는 비교 작업과 병렬로 진행하며 두 작업의 성공을 모두 요구한다.

Darwin 필수 검사는 패키지 하나씩 실행하고 매번 시작·종료 시각, Go 이벤트와 stderr를 남긴다. SQLite는 마지막에 실행한다. `-p=1`로 동시 빌드 도구 수를 제한하고 외부 프로젝트와 같은 `-trimpath`를 사용한다. 각 패키지 테스트의 15분 제한, race·CGO=0, 하위 검사 필수 통과 조건을 유지한다. Apple Silicon의 필수 네이티브 단계 제한은 80분이다. Intel 호환성 작업은 패키지별 바이너리와 실제 실행·종료 기록을 별도로 검증한다.

공개 저장소의 표준 GitHub 호스팅 러너를 사용한다. Go 설정은 OS·CPU·Go 버전별로 루트 모듈과 OpenAPI 클라이언트의 `go.mod`·`go.sum` 조합을 공유하므로 같은 PR에서 Portable과 다른 Linux 작업이 같은 캐시를 복원할 수 있다. Python은 uv의 기존 캐시와 고정 lockfile을 사용한다. GitHub의 PR 캐시 접근 범위와 저장소의 10 GB 상한을 따른다. [GitHub 캐시 정책](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching)

외부 operator·targeted migration fixture는 준비 단계에서 저장소의 검증된 Go 체크섬을 복사한다. 오프라인에서 실제 실행 파일의 의존성을 해석하고 사용된 합계를 원본과 대조한다. 실제 DB/process 검증은 `-count=1`로 test-result cache를 끄더라도
다운로드와 compile cache를 재사용한다. Cold build나 private workspace 격리가 검증 대상일 때만 그 경계를 따로 실행한다.
Credential·사용자 입력·secret을 포함한 임시 workspace는 공유 cache로 저장하지 않는다.
동일 소스를 다른 디렉터리에서 재현하는 일반 compile에는 `-trimpath`를 사용한다. 소스 경로 자체나 cold/private
build가 검증 대상이면 그 검사의 원래 조건을 유지한다. 소스·의존성·모델·module version·race/CGO 조건이 다른 결과는
같은 cache로 간주하지 않는다. OpenAPI SDK는 실제 임시 module 빌드 뒤 원래 경로에서도 여섯 library가 재사용되는지 검사한다.

계정 카탈로그 생성·전환과 공유 through 전환은 완성된 각 fixture의 테스트 바이너리를 한 번 준비한다. 공식 `go test -c`의 vet와 실제 바이너리의 Go 버전·OS·아키텍처·race·CGO를 확인한다. 매 분기에서 module 전체 파일의 지문을 다시 대조하고 consumer 디렉터리에서 새 프로세스를 실행한다. 10분 테스트 제한, backend별 임시 DB, 공식 test2json 원본, PID·종료 상태·필수 leaf를 확인한다. 소스가 바뀌면 기존 바이너리의 사용을 거부한다.

외부 build가 많은 conformance runner/godjcheck와 명령별 제품 흐름은 순수 core loop에서 분리한다.
Operator fixture의 module 준비·global build·generated project 검증은 compiler 비용을 포함하는 준비 예산을 사용한다.
준비 단계별 시작/완료 시간과 timeout·일반 종료 실패·출력 한도를 구분한다. 실제 제품 명령/PTY와 취소·잠금/cleanup
시나리오의 별도 짧은 제한은 유지하며, 준비 timeout도 소유 process tree를 회수한다.
Runserver의 readiness 전 준비에는 declaration/runtime 두 readonly 빌드와 생성물 검사가 포함되므로 별도 5분 예산을 둔다.
인증된 Article의 최초 계정도 첫 입력 prompt 전에 선언·commands runtime을 준비하므로 같은 5분 예산과 Go audit를 사용한다.
그 뒤의 입력·저장·출력 drain·자손 회수 제한은 별도로 유지한다.
Go audit는 시작 시각을 기록하고 준비 진단은 고정 phase 이름·명령 시작 수·경과 시간만 표시한다. 시작 기록을 완료로
간주하지 않으며 private 경로·환경 값·child 출력을 진단에 복사하지 않는다. HTTP·shutdown·자손 회수 제한은 별개다.
새 프로젝트·고정 module·Notes의 최초 `startproject`도 같은 Go audit를 사용한다. 실패 전에 HTTP runtime이 없더라도
bootstrap 의존성·candidate inventory/compile·module tidy·declaration/runtime build 중 마지막 시작 단계를 남긴다.
이 진단은 기존 5분 명령·출력·process 회수 제한에서 동작하며 compiler 완료나 시간 초과 원인의 확정을 뜻하지 않는다.
Article reload에서 로그인과 계정 생성은 실제 비밀번호 비용을 포함하므로 인증 정보가 담긴 POST를 30초 제한으로 한 번 보낸다. 일반 HTTP 요청의 제한은 5초다. 고정 버전 설치 검사는 전용 모듈 캐시와 파일 프록시를 사용하며 직접 빌드에도 `-trimpath`를 적용한다. 두 버전의 설치·기동·호환성 거부는 Linux와 Apple Silicon 제품 검사가 담당한다. [검증 근거](status/TEST_EVIDENCE.md#통합-ci에서-확인한-mac-실행-제한)

Runserver의 기존 흐름과 reload 흐름은 별도 native CI owner가 같은 전체 binary discovery를 사용한다. 필수 manifest의
root/child와 이후 추가된 root 모두 정확히 한 owner에서 실행해야 한다. PostgreSQL 전용 root는 별도 DB owner가 맡으며
그 밖의 skip·누락·중복·잘린 결과는 실패다. 두 owner는 성공/실패 원 JSON과 source·환경·checksum을 artifact로 보존한다.
전역 CLI와 자식 Go 빌드는 부모의 race build tag를 전달한다. Private 빌드는 도구 자체의 계측을 따르며 host GOFLAGS는 계속 차단한다.
실제 전역 CLI·독립 설치 바이너리와 최초 ready 시점의 선언 runner·서버 파일에서 Go build metadata를 읽어
OS·architecture·명시한 CGO와 race를 대조한다. 부모 테스트의 `-race` 성공만으로 자식 runtime의 계측을 대신하지 않는다.
단일 모델 reload는 처리 중인 저장·필드 추가·실패 후보·연속 수정·부모 종료와 `--no-reload`의 명시적 재시작을,
관계 앱은 같은 세션·관계·현재 권한과 CSRF/revision을 실제 public CLI/HTTP로 검증한다.
실행 범위는 [공유 manifest](../scripts/ci/runserver-required.txt)에 둔다.
필수 `project-reference-comparison`은 같은 실행의 고정 Python 관측과 일반 PR 7개 또는 공용 Unix·전체 플랫폼 9개 좌표의 원본을 다시 읽어 대조한다.
비교기는 같은 실행에서 고정 Python과 각 baseline·reload를 실제로 수행한 최신 작업을 찾는다. 해당 범위의 생산자(일반 PR 15개, 공용 Unix·전체 플랫폼 19개)가 모두 성공하고 각 생산 작업에서 업로드 직후 성공한 단계 이름에 고정 업로드 액션이 반환한 artifact ID·SHA-256을 기록한다. 비교기는 작업 API의 해당 시도·단계 순서·영수증을 GitHub artifact 메타데이터와 대조한 뒤 다운로드한다. 일부 작업만 재실행하면 재실행한 생산자는 새 시도의 원본을, 나머지는 같은 실행에서 성공한 원본을 사용한다. 최신 작업이 실패·취소됐거나 원본이 사라지면 비교가 실패한다.
실패한 작업만 재실행하면 GitHub가 이미 성공한 작업에도 새 ID를 부여할 수 있다. 작업이 끝난 뒤 만들어진 기록은 같은 runner, 시작·종료 시각, 결과와 업로드 영수증을 포함한 전체 단계가 이전 실행과 일치해야 한다. 비교 결과에는 원본을 실제로 올린 작업 ID와 시도를 기록한다.
각 원본의 source·tree·run·해당 생산자의 attempt, member checksum·전체 discovery/필수 phase·관측 owner와 입력 hash를 확인한다. 비교 결과에 실행 범위와 모든 생산자의 ID·시도·artifact ID·해시를 남긴다.
개발 결과 8개와 event 호출/해제 6개는 정확히 일치해야 하며, source 실패의 이전 앱 유지 차이만 닫힌 값으로 허용한다.
제품/관측 suite를 재실행하지 않고 성공한 원본을 읽으며 누락·skip·중복·truncation·다른 source는 실패다.
SYS-003 등의 단일 worker 준비는 cold 의존성 컴파일을 포함해 최대 5분을 허용하고, 실제 worker 동작은 별도의
90초 제한을 유지한다. 빌드 실패는 context 취소·deadline을 보존하고 원문 process 오류 대신 안전한 분류와 정제된
compiler 진단을 표시한다. 이 준비 예산은 실제 시나리오의 결과·비밀값·재시작 검사를 생략하지 않는다.
생성기의 byte/schema 단위 검사는 `codegen`, 생성된 별도 Go module의 compile·runtime·잘못된 조합 거부는
`codegen/consumertest`가 소유한다. 후자는 integration과 relation platform 범위에서 실행하며 `make quick`에는 포함하지 않는다.
게시 전 candidate 검증은 발견한 package와 manifest의 새 package를 모두 정렬해 제한된 인자 크기로 묶어 compile한다.
`go test -c -o /dev/null`로 각 test binary의 compile/link를 유지하고 init·TestMain·test 본문은 실행하지 않는다.
서로 다른 디렉터리의 동명 package와 마지막 test package의 타입 오류도 검사하며 실패하면 정상 생성물을 보존한다.
생성 module의 자식 Go 명령은 `-trimpath`를 사용한다. 각 fixture의 writable 임시 디렉터리와 실행 위치는 계속 분리하면서,
컴파일 결과에 포함되는 임시 절대경로가 동일 소스의 cache key를 매번 바꾸지 않게 한다. 기본 GOCACHE를 공유하며 별도 cache나
고정 workspace를 매 실행마다 만들지 않는다. 새 compiler 설정의 첫 빌드는 별도 cache를 만들 수 있지만 이후 동일 입력은 재사용한다.
Scaffold의 중첩 generation에도 해당 준비가 이미 선택한 compiler cache를 전달한다. 하위 명령의 HOME/module/temp는
별도로 만들고 정리하며, cold-build는 invocation의 private cache를 유지한다. 공유 가능한 캐시가 있어도 모든 Go 호출과
후보/생성물 검사는 실행하며, compiler의 결과 재사용을 command 성공이나 현재 source 검사의 생략으로 바꾸지 않는다.
다른 디렉터리의 동일 source가 같은 BuildID/Export 파일을 사용하는지, source 변경이 새 결과를 만들고 원래 module을 건드리지
않는지 검사한다. 모듈 밖 실행 marker로 동일 명령을 반복해도 test body가 실제 실행됨을 별도로 확인한다.
이 설정은 `codegen/consumertest`의 자식에 한정하며 실제 경로를 검증하는 CLI/process profile에 암묵적으로 확대하지 않는다.

자식 테스트는 부모의 race build tag에 따라 실제 `-race`로 실행하고 `-count=1`로 결과 cache를 끈다. Host GOFLAGS·workspace로 검사를
바꾸지 않으며 CGO/OS/arch 설정은 유지한다. 별도 `internal/compiletest`의 ABI·오용·정적 source 검사는 normal과
CGO-disabled가 소유한다(`!race`). Runtime facade JSON·candidate verifier 검사는 같은 패키지의 세 mode에서 계속 실행한다.
따라서 부모 race 실행에서 동일한 비계측 ABI child compile을 반복하지 않는다. 외부 fixture는 실행 owner가 locked
dependency graph를 한 번 준비한 뒤 GOPROXY=off·GONOPROXY=none·GOSUMDB=off로 컴파일한다. ABI와 생성 소비자 fixture는 저장소 checksum도 복사한다.
의존성 변경 뒤에는 `go mod tidy`뿐 아니라 기존 `go mod download all` 준비도 실행하고 재실행 시 go.mod/go.sum이 바뀌지 않는지
확인한다. Tidy의 package graph와 전체 module graph 준비가 요구하는 checksum 범위가 다를 수 있다.
독립 ABI fixture는 한 임시 module의 별도 package로 묶는다. 각 package의 시작·종료와 자기 `build-fail`·진단을
대조하며 aggregate 실패만으로 negative fixture를 통과시키지 않는다. `consumer.go`만 있는 정상 fixture의
`[no test files]` 종료는 compile 성공을 뜻하며, runtime test의 skip은 허용하지 않는다.
환경 준비는 `internal/testenv`가 마지막 값·플랫폼별 키 비교·정렬·제거를 소유한다. 공통 offline Go profile은
host goenv·GOFLAGS·외부 cache program·private module network 우회를 닫으며, suite별 DB·비밀값·TTY/poison 정책은 호출자가 유지한다.

`TestGeneratedModelResources`는 전후 생성 모델의 실제 OpenAPI에서 별도 SDK를 만들고 HTTP로 실행한다.
기존 OpenAPI client의 고정 `go.mod`·`go.sum`·`ogen.yml`을 재사용하며, 이 root를 소유한 relation/PostgreSQL 작업은
`make api-client-dependencies`로 해당 잠금을 먼저 준비한다. 필요한 로컬 실패 재현에도 같은 준비가 선행한다.
SDK에는 GoDj 의존성과 DB 환경을 넘기지 않고, 부모의 race·CGO/OS/arch를 유지한다. 부모는 정확한 완료 항목·HTTP
전송 횟수·실제 DB와 기존 행 보존을 확인한다. Native artifact에는 두 모델 버전과 양 DB별 문서·전체 생성 source·잠금·
완료 receipt와 hash를 보존하며, 모델 소비자의 원본 JSON 이벤트에 이 출처를 함께 기록한다.

순수 schema/codegen 검사는 Portable Go의 각 mode에서 실행한다. 관계 matrix는 생성 소비자와 실제 DB 동작의 플랫폼 차이를 검증한다.
각 위험에 주 실행 경로를 두고 같은 test/platform/mode의 반복은 새 위험이나 실패를 조사할 때만 추가한다.
Helpdesk 요약의 실제 브라우저는 Portable normal integration이 한 번 실행한다. 별도 임시 SQLite·실제 로그인·
고정 CLI/Chromium으로 페이지/HAVING·지표·우선순위 미리보기를 검사하고 native readback으로 원본 행/링크/audit
보존을 확인한다. CLI의 오류 텍스트·불완전한 결과, 서버 종료/DB 정리 실패를 통과시키지 않으며 source/version·로그·
screenshot·receipt를 같은 run의 artifact로 보존한다. 이 단일 브라우저 좌표와 양 DB/세 mode 제품 검사는 서로 구분한다.
같은 normal integration owner는 Admin inline·업로드 UI도 두 개의 별도 임시 SQLite·브라우저 세션에서 실행한다.
동적 행 추가/제거, 기존/새 부모 저장, 권한별 control, 파일 내용 검증과 rollback·재선택을 검사한다.
Go fixture의 종료 시 native readback에서 제출한 행·부모 연결과 기존 행의 전체 scalar 보존을 확인하고,
`admin-inline-browser` artifact에 모든 명령·검사·source·저장 결과와 정리 receipt를 남긴다.
Helpdesk 조회 화면의 read-only 증거와 Admin의 실제 쓰기 증거는 별도 receipt로 구분한다.
DB schema/port/temp 디렉터리는 lane별로 분리하고 기본 build cache와 병렬 실행을 유지한다. 로컬 디스크 여유가 부족해
cache 정리가 필요하면 활성 Go 작업이 종료된 뒤 정리한다. 이후 무거운 재빌드와 검증은 원격 CI에서 수행한다.
중복 build·검증 범위·cache 누적과 실행 중 임시 공간을 구분하며, 같은 검증을 로컬과 원격에서 관성적으로 반복하지 않는다.

`internal/projectcheck`의 다섯 protocol과 projectgenerate/projectmigration protocol은 Portable core가 문법·resource 검사를
소유한다. 공용 `wirejson`과 `projectwire`도 core에 속한다. CLI platform owner는 이들을 사용하는 실제 outer command와
linked runner를 실행하며 protocol 패키지 전체를 다시 선택하지 않는다. 32-bit compile은 정수 범위와 architecture 경계를 위해 유지한다.

| 변경 위험 | 주 검증 위치 | 추가 실행 경계와 보존할 관측 |
|---|---|---|
| Strict JSON·Schema IR wire | 공용 primitive와 명령별 protocol unit | duplicate/trailing/Unicode·정확한 budget·transport precedence, 실제 linked wire의 정상/실패 연결 |
| SQLMigrate argv 전체 조합 | `TestParseSQLMigrateArgumentsRejectsInvalidForms` | outer Run과 global dispatch의 대표 arity/identity/option 실패, absent cwd·poison descriptor·build/init 0 |
| CLI 공용 수명 | `internal/projectcheck` fault/process 회귀 | retained root·workspace 정리·완료 뒤 취소, 외부 migrate/writer/operator/server의 durable/TTY/signal 경계 |
| SQL projection의 실제 위임 | `TestSQLProductRunnerPipelineExecutionControls` | compiled bypass·private rename·IR 변경 대조. 별도 Phase D는 Hosted PostgreSQL 환경 격리·DB 접속 0·중단/reap |
| 세션 동시 접근 | real MemoryStore·durable SQLite의 `AtomicAccess` | 같은 ID의 갱신/만료/rotation/취소 interleaving, multi-runtime PostgreSQL·restart는 DB owner |
| Loaded definition·operation | migration/definition unit·lifecycle | 입력/결과 mutation·동시 replay·typed nil, fresh history·revision fence·durable prefix는 실제 DB owner |
| 공통 Query AST 의미 | backend compiler unit | SQLite와 PostgreSQL 실제 SQL·identifier/NULL·rollback은 각각 DB owner |
| 관계 actual 생성 | contract별 fresh observer | 관계별 query/cache/rollback 회귀, 고정 oracle 대조와 sibling case 미실행 대조 |

Portable과 관계/CLI matrix의 Linux amd64는 같은 Ubuntu 이미지와 Go 버전을 사용한다.
`scripts/ci/packages.py`는 CI에서 관계 package 전체를 관계 matrix에 배정하고 Portable에서 제외한다.
Runserver는 project-check matrix가 맡는다. CI owner를 전달하지 않는 로컬 재현은 원래 package 범위를 유지한다.
OS·architecture·CGO·race가 다른 좌표의 검증은 각각 실행한다.

Operator와 targeted migrate의 같은 OS/arch/mode는 `command-product-matrix`에서 checkout·toolchain·dependency 준비를 공유한다.
각 제품은 독립 process·timeout·JSON log·required sentinel·no-skip 검사를 유지한다. 하나가 실패해도 실행이 취소되지 않았다면
다른 제품을 실행하며, 마지막 outcome 검사는 양쪽 step의 실패·skip·누락을 거부한다.
PostgreSQL capture producer, exact reference, cold build와 32-bit 경계는 각각의 기존 owner가 맡는다.

Linux의 각 아키텍처·모드는 자체 바이너리의 `go test -list .` 결과로 Test·Example·Fuzz를 배정한다. Runtime 전용 러너가 생성 소비자를 제외한 관계 패키지를 담당한다. 소비자 러너는 checkout, Go 캐시, 모듈 다운로드와 발견을 한 번 수행하고 최대 세 묶음을 독립 Go 프로세스와 제한으로 실행한다. [관계 실행기](../scripts/ci/relation-product.sh) · [배정 함수](../scripts/ci/relation_shards.py)

Linux에서는 오래 걸리는 계정 테스트 16개를 각각 단독 Go 프로세스에서 실행한다. ARM64 race의 나머지 상위 테스트는 예상 시간으로 균형을 맞춘 다섯 묶음에 배정한다. 두 Linux race runtime과 ARM64 race의 소비자 묶음을 먼저 제출한다. [Linux 시간 측정](status/TEST_EVIDENCE.md#linux-arm64-race-소비자-작업-시간)

Apple Silicon 일반 runtime은 소비자를 제외한 전체 패키지 목록과 필수 테스트를 대조하고 큰 패키지를 `-p=1`로 빌드한다. 대표 생성 검사는 각 root·child의 정확한 실행과 종료, 실제 자식의 좌표와 SDK의 잠금 파일·HTTP·DB 결과를 검증한다. 최종 집계는 같은 실행의 해당 작업이 올린 artifact ID·digest와 최신 유효 시도를 대조하고 원본을 다시 읽는다. [네이티브 증거](../scripts/ci/native_evidence.py) · [최종 검증](../scripts/ci/native_receipts.py)

Linux 일반 소비자 한 묶음의 step 제한은 45분, Go 패키지 제한은 35분이다. Linux 양 아키텍처의 race runtime은 70분 안에 `db/sqlite` 전체를 별도 Go 프로세스에서 45분 제한으로 한 번 실행하고 나머지 패키지를 35분 제한으로 실행한다. [SQLite 시간 제한의 근거](status/TEST_EVIDENCE.md#026a9627-linux-arm64-race-sqlite-패키지-시간-제한)

묶음마다 Go 이벤트·stderr·진단·필수 목록을 별도 경로에 보관한다. 실행기는 발견한 상위 테스트와 배정한 패키지의 정확한 실행·통과 및 필수 하위 테스트 결과를 대조한다. 첫 묶음이 실패해도 취소되지 않은 러너는 다음 묶음을 실행한다. 마지막 검사는 계획에 있는 모든 step의 성공과 사용하지 않은 step의 생략을 확인한다. 실패했을 때도 원본을 게시한다. [묶음 결과 검사](../scripts/ci/worker_parts.py)

관계 product의 Author/Post 생성 모델과 프로젝트는 `conformance/relationfixture`를 공유한다.
이 패키지가 whole-project drift, 생성물 없이 declaration runner를 만드는 bootstrap, 앱 간 의존성과 observer의 oracle-blind 경계를 검증한다.
기능별 product는 실제 query/object/reverse/prefetch/select/delete 결과와 cache·취소·rollback 검증을 소유한다.
옛 fixture별 파일 수·이전 내부 ABI의 복제본을 유지하지 않는다. 현재 생성 조합의 일관성·잘못 섞인 snapshot 거부는
`codegen/consumertest`, 소비자 타입 오류와 publication 실패 시 기존 결과 보존은 각각 compile·projectgenerate 검증이 맡는다.

SQLite/migrations 전체 normal·race·CGO-disabled와 vet는 Linux의 각 OS/CPU 좌표에서 실행한다. 전체 플랫폼의 Apple Silicon 일반 runtime에서도 SQLite와 migrations 전체를 실행한다.
Conformance Go runner 전체는 project-check matrix가 실행하고 관계 matrix는 같은 runner subset을 반복하지 않는다.
Linux amd64 checker 전체는 Portable이 맡고 관계 matrix의 같은 subset을 생략한다.
Portable core/conformance는 `scripts/ci/portable_tests.py`가 normal/race/CGO0의 JSON 시작·종료와 package 완료를 대조한다.
Core의 필수 root/child는 `portable-core-required.txt`에 두고 모두 실제 pass를 요구한다. 별도 PostgreSQL/native owner에 속한
기존 선택적 검사의 skip은 core 결과에 구분해 기록한다. Conformance는 옮겨진 checker의 필수 sentinel·package no-skip을 유지한다.
원격 job은 JSON 전체·stderr·실제 package/필수 목록·결과와 파일 hash를 portable test evidence artifact로 보존한다.
다른 owner가 전체 vet를 실행하는 같은 좌표에서는 vet도 반복하지 않는다. 필수 sentinel과 no-skip 검사는 실제 실행 owner에 적용하고,
aggregate는 모든 필수 owner의 실패·취소·skip·누락을 거부한다. Darwin CGO=0은 프로필별 Apple Silicon 필수·제품 검사와 공용 Unix·full의 Intel 호환성 작업에서 검증한다.

## 남겨야 하는 검증

- Migration의 per-step atomicity, durable prefix, restart, revision fence와 outcome unknown
- Codegen의 stale/mixed candidate 거부, 실패 시 이전 결과 보존과 중단 후 recovery
- Typed/dynamic 의미 일치, nullable 값, relation/cache 복사·소유권
- 권한 거부, CSRF, token/password 비노출, session rotation/logout/revocation
- Context cancellation, signal, pipe ownership와 child reap
- Oracle-blind actual, comparator negative control, 필수 실행 누락·skip·잘린 로그 거부

테스트를 삭제하거나 통합하면 위 위험을 현재 어느 test가 검증하는지 확인한다.
전체 이름·개수·payload 길이의 영구 잠금 대신 실행한 scope, required capability/sentinel과 실제 실패/skip를 검사한다.
Test 파일의 문장이나 work 일지에 같은 prose가 남아 있는지는 runtime 안전성의 대체 검증이 아니다.
Python normal·exact·compatibility는 `scripts/ci/python_tests.py --profile`의 공통 launcher로 현재 발견한 testcase의
시작·종료와 허용 skip을 대조한다. Make의 normal/exact는 고정 DRF 환경에서 전체 suite를 실행하며 DRF skip을 허용하지 않는다.
Normal·compatibility의 고정 reference 전용 네 검사는 exact가 맡고, exact는 skip을 허용하지 않는다. Launcher가 discovery 전에
exact flag를 설정하므로 host flag가 선택한 profile을 바꾸지 않는다. 전역 테스트 수를 고정하지 않고 실패·expected failure·
임의 skip·중단된 실행을 거부하며 고정 reference 의미의 별도 digest 검증을 유지한다.

CI 도구의 Python 검사는 같은 launcher의 `tools` profile로 `scripts/ci/test_*.py` 전체를 실행한다.
Portable normal core-vet의 필수 `Verify CI tooling` step과 `make ci-tools-test`/`make ci`가 같은 경로를 사용한다.
Skip을 허용하지 않으며 발견한 각 testcase의 시작·종료를 대조한다. 결과가 완전할 때만 `CI_TOOLS_VERIFIED`를
게시한다. 브라우저의 거짓 성공·불완전 결과·native 보존 증거 누락 거부도 이 실행의 필수 검사다.

고정 reference 파일의 size/hash는 protocol의 artifact 검증에서 대조한다. 실행 catalog는 그 expected payload의 원본이 아니다.
전체 manifest/oracle 교차 거부는 현재 계약 집합에서 한 번 검증하며, 각 계약의 phase·payload·provenance와 부정 대조는 해당 계약 테스트가 맡는다.
실제 catalog 실행 계획과 Make target dispatch로 빠진 계약·잘못된 profile·expected/capture wiring을 검증한다.
구현 파일 자체의 과거 SHA를 보존하기 위해 현재 테스트의 구조를 고정하지 않는다.
공통 fixture는 호출마다 새 mutable 입력을 만들며 actual 관찰과 expected 로딩은 별도로 유지한다.
공통화한 환경 준비에서도 각 실행의 timeout·출력 제한·cleanup·필수 DB 조건을 유지한다.
SQLite command snapshot과 PostgreSQL migrate/target의 공통 physical catalog는 `conformance/internal/dbstate`가 관측한다.
제품별 expected와 application/history/revision 검증은 각 호출자가 소유한다. 더 상세한 SQL 정의를 검증하는 showmigrations 관측도 유지한다.
PostgreSQL catalog의 컬럼·인덱스·sequence·constraint trigger·policy·view 변경 감지는 실제 DB negative control이 검증한다.
Process 출력·readiness·종료 관리는 `conformance/internal/testprocess`가 맡는다.
호출자는 명령별 시간·출력 한도를 지정하며 snapshot의 raw bytes·schema·행 수·migration history·revision 관측은 그대로 유지한다.
Django 관측 envelope는 `observations`, SQL capture는 `sql_observation`이 공유한다. 결과 누락과 명시적 null을 구분하고
계약별 SQL 분류·실패 판단은 호출자가 유지한다. Django migration 관측은 Django 내부에서만 schema/field 정규화를 공유한다. 계약별 primary-key 필드·datetime 정규화와 recorder 접근 순서는 보존한다.
Select/object/delete 관계 handler는 자신이 맡은 case만 관측한다. 같은 DB에서 sibling case 전체를 실행한 결과의 전역 cache는
사용하지 않는다. 검증 편의를 위한 test-only 결합은 각 case의 fresh DB 결과와 DB state 일치도 따로 확인한다.

## 실제 source의 증거

PostgreSQL actual은 검증할 source·observer·환경에서 생성하고, source/profile/scenario identity와 digest를 확인한 consumer가 사용한다.
Oracle·expected로 actual을 만들지 않고, 다른 source의 actual이나 stale attestation을 current proof로 인정하지 않는다.
같은 신뢰된 CI 실행의 artifact를 생성 job에서 소비 job으로 전달한다. 장기 기록은 source·환경·명령·결과와 불변 artifact 위치를 남긴다.
Consumer만 재시도할 때는 같은 run의 성공한 producer attempt를 재사용할 수 있다. `capture_artifact.py resolve`가
artifact ID와 실제 normal producer job의 run·source·성공 상태를 확인하고, consumer는 그 producing attempt의 envelope를
검증한다. 새 attempt의 실패를 과거 성공 artifact로 숨기거나 checkout/payload 검사를 생략하지 않는다.
Source API 관찰은 GoDj 자체를 현재 source로 type-check하고 외부 package는 현재 module의 native compiler export에서 읽는다.
GOPATH나 과거 dependency 집합을 가정하지 않으며 native export 조회에도 context·timeout·출력 한도와 readonly/offline 조건을 적용한다.
외부 CLI fixture의 module 준비는 제품 호출 이전에 수행한다. 실제 제품 명령의 private cache·readonly·무변경 검사는 그대로 유지한다.
Python compatibility observer는 실제 실행 interpreter를 보고하고 고정 fixture의 runtime metadata와 구분한다.
각 지원 Python에서 311개 시나리오를 실행한다. 비교기는 Django·DRF 원본의 체크섬과 프로필, manifest의 계약 ID·순서를 확인한다. 호출자가 지정한 ID를 포함한 각 결과를 저장된 관측과 바이트 단위로 대조하고, 전체 크기와 SHA-256을 출력한다. [비교기](../scripts/ci/python_semantics.py)
고정 Django source hash·관찰 동작과 부정 대조는 각 지원 interpreter에서 유지하며 고정 reference fixture를 다시 쓰지 않는다.
관찰자나 attestation I/O를 공통 helper로 옮기면 그 helper도 사용하는 attestation의 source binding에 포함한다.
JSON/file 읽기 구현을 공유해도 각 attestation의 source inventory·크기 제한·schema는 독립적으로 검증한다.

로컬에서 전체 gate를 재현하려면 검증할 소스의 성공한 CI run과 attempt를 선택하고 두 artifact를 내려받는다.
아래 `RUN_ID`, `ATTEMPT`, 절대 경로를 실제 값으로 바꾼다. Artifact는 90일간 보관하므로 만료됐다면 같은 소스에서 새 CI 실행이 필요하다.

```sh
ci_run=RUN_ID
ci_attempt=ATTEMPT
capture_root=/absolute/path/to/godj-evidence
gh run download "$ci_run" --name "systemstate-postgres-$ci_attempt" --dir "$capture_root/systemstate"
gh run download "$ci_run" --name "operator-postgres-$ci_attempt" --dir "$capture_root/operator"
```

두 `provenance.json`의 `checkout`과 같은 commit을 별도 작업 사본에서 사용한다. PR 실행의 checkout은 PR head와 다른 merge commit일 수 있다.
그 작업 사본에서 아래를 실행한다. 다른 저장소·run·attempt·payload·checkout은 envelope 검증이 거부하고,
Go consumer는 checksum과 실제 source/profile/scenario binding도 확인한다. `testdata`의 고정 codec fixture는 이 입력으로 사용할 수 없다.

```sh
export GITHUB_REPOSITORY=progresshans/godj GITHUB_RUN_ID="$ci_run" GITHUB_RUN_ATTEMPT="$ci_attempt"
export GITHUB_REPOSITORY_ID="$(gh api repos/progresshans/godj --jq .id)"
python3 scripts/ci/capture_artifact.py verify "$capture_root/systemstate" postgresql-17.10-two-process-v1.json
python3 scripts/ci/capture_artifact.py verify "$capture_root/operator" postgresql-17.10-sqlite-external-operator-v1.json
ATTESTATION_DIR="$capture_root" make ci
```

이 로컬 gate는 현재 OS에서의 재현이다. 다른 OS/arch와 PostgreSQL service 실행 결과까지 새로 만든 것으로 기록하지 않는다.

실행 로그는 build-output/build-fail과 dependency ImportPath/FailedBuild, test failure, timeout, malformed/truncated JSON과
stderr-only failure를 구분해야 한다. 원래 test exit code와 유용한 원인을 보존하고 bounded/redacted diagnostics를 출력한다.
검증을 리팩터링할 때는 대표적인 실제 결함·필수 실행 누락·위조 actual이 여전히 실패하는지 확인한다.

## S3 실제 서비스 profile

S3 wire 단위 검사는 모든 관련 portable 실행에 포함한다. 실제 서비스는 [s3_service.py](../scripts/ci/s3_service.py)가 고정
MinIO commit과 module checksum·Go 1.26.5·dependency 파일 hash로 독립 binary를 빌드하고 `go mod verify`를 확인한다.
실행마다 새 loopback process·data directory·합성 credential을 만들며 child의 exit code와 server 종료/회수를 기록한다.
제품에 server 코드를 링크하지 않는다. [s3fixture](../conformance/s3fixture/fixture.go)는 공식 SDK로 새 bucket을 만들고
테스트 종료 시 그 bucket의 version/delete marker까지 정리한다. 기존 cloud bucket이나 환경의 AWS credential을 찾지 않는다.

```sh
s3_build="$(mktemp -d)"
python3 scripts/ci/s3_service.py build --directory "$s3_build"
python3 scripts/ci/s3_service.py run --directory "$s3_build" -- \
  go test -json -count=1 ./storage ./admin ./codegen/consumertest \
  -run '^(TestS3LiveServer|TestAdminImageUploadVerifiedContentAndStorage|TestGeneratedFileConsumer)$'
```

이 명령은 실제 S3와 SQLite 소비자를 실행한다. PostgreSQL은 기존의 명시적 `GODJ_TEST_POSTGRES_URL`과
`GODJ_REQUIRE_POSTGRES=1`을 전달해 포함한다. Wrapper는 `GODJ_REQUIRE_S3=1`을 설정하고 endpoint/credential을 child와
생성 소비자에 전달한다. 선택적 profile이 없으면 일반 portable 검사에서는 live root가 반환하지만, required/부분 설정은 실패한다.
세 모드에서 `core`와 일반 생성 소비자 작업(`core-consumers`, `core-consumers-2`)이 S3 서비스를 기동한다.
각 작업은 배정받은 양 DB·S3 하위 사례와 Admin 저장의 실제 통과를 필수 목록과 생성 소비자의 실행 기록으로 확인한다.

PostgreSQL core의 race mode는 [postgres_shards.py](../scripts/ci/postgres_shards.py)가 framework, native PostgreSQL,
generated consumer, process product를 나눈다. Framework는 core, process는 core-processes가 맡는다.
`db/postgres`는 core-postgres-1/2가 필수 목록의 전체 root를 SHA-256 순서로 번갈아 배정한다. Child만 필수 목록에 있는
root도 포함하며 모든 child를 부모와 함께 실행한다. 생성 소비자 중 bulk create/update·query update·
단일 객체 생성·update-or-create·행 잠금의 전체 root는 core-consumer-writes가 맡는다. Helpdesk의 PostgreSQL 소비자 root는
core-helpdesk-postgres에서 실행한다. 같은 Go package에 있는 SQLite 소비자와 helper는 core가 맡아
두 DB 소비자가 하나의 package timeout을 공유하지 않게 한다. Normal과 CGO-disabled는 framework·native PostgreSQL·process를
core에 유지한다. 필수 목록의 각 검사는 모드마다 한 작업에 속하고 하위 검사는 상위 테스트와 함께 실행한다.
각 실행의 `plan.json`에 담당 패키지·시간 제한이, `required.txt`에 필수 검사 목록이 남는다.

세 모드 모두 `core-account-consumers`가 모델 바인딩과 계정 생성 입력 정책을, `core-account-management`가 첫 계정 명령·Admin·JSON 관리를,
`core-account-reset`이 비밀번호 재설정을 검증한다. 프로필 관계·컬렉션·파일은 `core-account-profiles`가 담당한다.
전환·중간 테이블별 담당 작업은 [배정 함수](../scripts/ci/postgres_shards.py)를 따른다.
이 함수가 나머지 일반 생성 소비자를 `core-consumers`와 `core-consumers-2`에 상위 테스트 단위로 나눈다.
생성 소비자, normal/CGO-disabled의 core, native PostgreSQL credential 검사를 맡은 작업의 Go 패키지 제한은 30분이고
나머지는 18분이다. 모든 작업이 필수 테스트의 실행·통과와 skip 유무를 확인한다. 일반 생성 소비자 패키지의 vet는 normal/core-consumers가 맡는다.

계정 소비자 여러 묶음이 한 러너의 PostgreSQL 서비스를 공유할 때는 각 묶음 전용 DB를 `template0`으로 생성한다. Go는 묶음마다 필수 목록을 읽어 독립 프로세스로 실행하며, 생성 앱 진단과 테스트 로그도 따로 보관한다. Django 대조, S3 참조 서비스, DB 재기동과 실행 증거 생산 작업은 각각 전용 서비스에서 실행한다. [PostgreSQL 실행기](../scripts/ci/postgres-product.sh)

normal/core-account-management에서 [Django 계정 관측기 실행 도구](../scripts/ci/identity_reference.py)가 모델 권한·관리·카탈로그,
계정 생성·로그인, 비밀번호 변경·재설정·HTTP·사용 불가 비밀번호를 SQLite와 PostgreSQL에서 각각 실행한다.
Go 검사가 읽는 같은 DB의 기준 파일과 관측값·Django 소스 해시를 대조한다. 기준과 실행의 DB 패치 버전은 각각 기록한다.
18개 원본과 SHA-256 대조표는 해당 작업의 `identity-reference/`에 보관한다.

M1의 독립 Django 기준은 PostgreSQL 일반 모드의 `core` 작업에서 SQLite 파일과 일회용 PostgreSQL DB를 각각 사용한다. 과거 모델로 기존 행을 변환하고, 중간 오류와 비가역 작업 뒤 재접속해 행·제약·적용 기록을 읽는다. 양 DB의 관측 결과와 입력 해시를 대조한 뒤 원본 둘과 해시 목록을 `migration-data-reference/`에 게시한다. GoDj의 데이터 변환 API와 revision 검사는 별도 제품 검증 대상이다. [비교 실행기](../scripts/ci/migration_data_reference.py) · [Django 관측기](../conformance/runners/django/migration_data_reference.py)

Helpdesk의 같은 DB를 이어 쓰는 소비자는 normal/CGO-disabled 10분, race 15분의 전체 context를 사용한다.
개별 동시성·취소 검사의 짧은 제한과 PostgreSQL 정리 20초는 별도로 유지한다.
Normal core의 system-state capture 생산·service restart, operator-target의 별도 capture 생산도 그대로 실행한다.
원 Go JSON/stderr·필수 목록·partition 계획은 성공 여부와 관계없이 `postgres-product-<mode>-<shard>-<attempt>`에 보존한다.
취소 실행의 불완전한 로그는 진단 자료이며 package 완료나 필수 실행 PASS를 대신하지 않는다.

GDJ-0124의 관계 표현식은 normal·CGO-disabled의 core와 세 모드의 일반 생성 소비자 작업에서 먼저 고정
[Django observer](../conformance/runners/django/related_expression_reference/README.md)를 양 DB로 실행한다.
그 실행의 원 reference와 생성 consumer의 native child JSON events를 같은 PostgreSQL product artifact에 남긴다.
Parent는 현재 observer/input/module manifest hash와 정확한 17개 reference 사례·52개 native child 실행·5개 compile-negative를 요구한다.
Portable의 SQLite invariant 실행은 이 reference/native 양 DB 검증을 대신하지 않는다.

GDJ-0125·0127의 컬렉션 필드/계산식 집계도 같은 작업에서 별도의
[Django observer](../conformance/runners/django/collection_aggregate_reference/README.md)를 실행한다.
역방향 FK·M2M·혼합 경로, NULL·중복·독립 JOIN의 행 곱·연속 필터의 JOIN 재사용과 Distinct,
행별 숫자/Decimal·CASE·computed FILTER와 후속 결과 단계를 49개 사례로 대조한다.
현재 observer/input/module manifest와 runtime pin을 확인하고, 양 DB의 reference/runtime 130개 child run/pass,
부모의 모델·kind·decoder compile-negative 8개를 요구한다. 원 reference와 `godj-collection-native.jsonl`을
`collection-aggregate-reference/`에 함께 보존한다. Missing reference·잘린 event·미실행/skip은 성공이 아니다.
Helpdesk의 라벨별 조정 비용은 기존 양 DB의 권한·Category·snapshot·저장/audit 보존 검증에 추가한다.
독립 client는 티켓별 유리수 계산과 required nullable wire, 브라우저는 28개 필수 검사를 소유한다.
필수 실행 구성을 실제 PASS와 구분하며 source별 완료 상태는 TEST_EVIDENCE를 따른다.

GDJ-0126의 Decimal 계산식도 같은 작업에서 [독립 observer](../conformance/runners/django/decimal_expression_reference/README.md)의
양 DB 각 30개 사례와 현재 source hash/runtime/오류 목록·전체 컬럼 rollback/정리를 확인한다.
생성 소비자는 양 DB reference/runtime 84개 child run/pass·skip 0과 compile-negative 7개를 요구하며
`decimal-expression-reference/`의 원 JSON과 `godj-decimal-native.jsonl`을 같은 artifact에 보존한다.
Native child 실행 실패에도 원 이벤트를 보존하며 stdout/stderr가 잘렸거나 실행이 빠진 경우 성공으로 처리하지 않는다.
Helpdesk의 45개 티켓·외부 Category, exact 비용·NULL·계산 필터/순서/Page, 권한·15개 snapshot/cursor 실패와
전체 저장/audit 보존은 기존 양 DB owner에 추가한다. 별도 output 실패는 prepared 응답의 부재를 검사한다.
독립 OpenAPI client의 `helpdesk_cost_preview`, `generated_cost_preview_wire` receipt와 브라우저 22개 check,
Ticket/Report/Label/연결/비용/audit의 최종 DB 대조도 필수다. 전체 원격 검증 완료는 TEST_EVIDENCE를 따른다.

CI는 source/run/attempt별 native build와 수명 receipt를 `s3-service-<mode>-<shard>-<attempt>` artifact에 남긴다. 요청 응답 유실 후
결과 보존·conditional no-overwrite·checksum 거부·version 고정 seek·서명 변조/만료와 actual HTTP admission을 검증한다.
실행별 source와 결과는 TEST_EVIDENCE에 기록하며 MinIO 성공을 AWS 운영 account나 다른 S3 구현의 실행 증거로 대체하지 않는다.

## 문서와 과거 증거

문서-only 변경의 로컬 검증은 link·상태 일관성과 `git diff --check`로 제한한다. PR과 main push에는 위의 원격 CI 실행 범위를 적용한다.
현재 source에서 실행한 결과는 [TEST_EVIDENCE](status/TEST_EVIDENCE.md)에 한 번 기록한다.
명령, source, 환경, 결과, 실패/skip, 미실행 범위를 남기면 된다. 작은 수정마다 여러 activation/checkpoint/terminal EVID를 만들지 않는다.

코드 규모는 `go run ./scripts/sourceinventory`로 집계한다. 현재 작업 사본의 Git 추적 파일과 무시되지 않은 새 Go/Python 파일을
포함하고 삭제된 파일은 제외한다. `-revision COMMIT`은 고정 commit의 바이트를 읽는다. Go는 `ast.IsGenerated`로 판정하고
test → generated → conformance 지원 → examples → framework/CLI/generator/support 순서로 중복 없이 분류한다.
생성 머리말을 문자열로 출력하는 수작업 생성기는 generated가 아니다. 빈 줄·주석도 줄 수에 포함하며 source digest와 파일별
SHA-256을 함께 출력한다. 이 집계는 품질이나 성능을 대신하는 목표값이 아니다.
