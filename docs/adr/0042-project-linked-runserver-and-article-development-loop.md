# ADR-0042: Project-linked Runserver and Generated-aware Development Loop

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0042-project-linked-runserver-and-article-development-loop.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-24
- 현재 확장: 2026-10-23, [C1](../../work/0137-project-core-startup.md)의 기본 watch/reload와 후보 게시 수명.
- 관련 work/contract: [GDJ-0042](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/work/0042-project-linked-runserver-and-article-development-loop.md), WEB-011..020, Q-010, Q-017, M5
- 선행 결정: [ADR-0004](0004-cli-and-project-binary.md), [ADR-0022](0022-project-runtime-and-global-migration-check.md),
  [ADR-0036](0036-project-schema-generated-bundle-and-recoverable-publication.md),
  [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md)
- 부분 대체: [ADR-0021](0021-project-linked-migration-check.md)의 descriptor exact-three-semantic-line 조항

## 맥락

Global `godj`는 project-linked migration check와 recoverable `generate --check`를 제공하고, Article example에는 generated
model/project package를 import하는 별도 site binary와 graceful Web server가 있습니다. 이 ADR을 제안할 당시에는 사용자가 site
package를 직접 build하고 DB argv를 조립해야 했고, ADR-0004가 의도한 project-aware command 경험과 실제 generated Web 흐름이
연결되지 않았습니다. 아래 결정의 current implementation이 그 간극을 닫았습니다.

Declaration runner는 stale/missing generated source에서도 ProjectSpec을 load해야 하므로 generated package를 import할 수
없습니다. 따라서 같은 descriptor package를 runserver로 재사용하면 ADR-0036/0038 bootstrap graph를 깨뜨립니다. 반대로
global CLI가 DB/settings를 해석하면 project ownership을 침범합니다. Current committed bundle을 검사하지 않고 runtime을
build하면 stale generated behavior를 조용히 실행할 수 있습니다.

Existing short-lived process owner는 private response를 종료까지 cap/capture하고 cancellation에서 fixed grace 뒤 group kill을
시도합니다. Long-lived development server는 output streaming, Web shutdown보다 긴 grace와 child exit를 관찰한 conditional
force-kill이 필요하므로 같은 lifecycle을 재사용할 수 없습니다.

## 결정 기준

- Familiar global `godj runserver`와 explicit project selection
- Declaration-only runner와 generated-aware runtime import graph 분리
- Current bundle read-only preflight와 project-tree no-write
- Shell 없는 readonly isolated build와 invocation-local cleanup
- Loopback-only development exposure와 closed argument/error boundary
- Application-owned DB/settings environment와 stdout/stderr
- Clean SIGINT drain, conditional force-kill, direct reap와 repeatability
- SQLite/PostgreSQL actual Article flow without Query/ORM/Schema/Migration public changes

## 고려한 선택지

### Declaration runner에 serve를 추가

Generated imports 때문에 missing/broken output에서 declaration bootstrap이 compile되지 않습니다. 채택하지 않습니다.

### Global CLI가 fixed `./cmd/site` 또는 DB flags를 추측

빠르지만 runtime package와 database ownership을 convention/CLI detail로 숨기고 multi-project 확장성을 막습니다. 채택하지
않습니다.

### Public `project.Config` direct command dispatcher

General command registry와 public compatibility surface를 먼저 고정하며 declaration runner가 generated runtime을 직접
소유하게 만들기 쉽습니다. 이번 단면에는 채택하지 않습니다.

### Optional descriptor capability와 별도 runtime binary

Existing selector/build isolation을 재사용하고 Web이 없는 project는 capability를 생략할 수 있습니다. Generated-aware binary는
DB/settings를 project-owned environment에서 열고 global CLI는 build/process lifecycle만 소유합니다. 이 선택지를 Phase A/B에서
검증했습니다.

## 결정

1. Current descriptor format 1은 exact `package` 뒤 optional
   `runserver_package = "./cmd/site"` 한 줄을 허용합니다. 같은 strict path grammar와 canonical ordering을 사용하고 두 package는
   달라야 합니다. Absent field는 migration/generate에 유효하지만 runserver는 `runserver_not_configured`로 닫습니다. Legacy reader,
   alternate profile 또는 fixed-path fallback은 만들지 않습니다. 이는 ADR-0021의 exact three-semantic-line descriptor 조항만
   additive current capability로 후속 확장합니다.
2. Global CLI는 `runserver`, optional ordered `--project <godj.toml>`, `--addr <address>`, 마지막 `--no-reload`를 받습니다.
   기본 주소는 `127.0.0.1:8000`; exact IPv4 loopback literal과 canonical port 0..65535만 허용합니다.
   기본은 watch/reload이며 `--no-reload`는 한 번 준비한 runtime만 실행합니다.
3. 각 후보는 retained project selection과 private external workspace에서 declaration runner를 한 번 build/run하고 current
   GeneratedBundle을 계산합니다. `projectgenerate.CheckRoot`가 committed manifest/source와 interrupted publication을 read-only로
   검증합니다. Stable drift에서는 runtime build/start와 GoDj-owned Article runtime DB I/O가 0입니다.
4. Clean preflight 뒤 exact
   `go build -buildvcs=false -mod=readonly -o <private>/godj-project-server <runserver_package>`를 shell 없이 실행합니다. Build는
   existing external 0700 workspace와 private cache/temp/home, GOWORK-off/GOTOOLCHAIN-local policy를 사용합니다. Private keys는
   교체되고 safe local module proxy가 있으면 `GOPROXY` 앞에 추가되며, 그 밖의 non-private ambient environment는
   declaration/runtime build에도 남으므로 이는 credential sandbox가 아닙니다. Runtime build 뒤 같은 selected
   root와 bundle을 다시 check하고 달라졌으면 child를 시작하지 않습니다.
5. 단일 실행의 runtime argv는 exact `<binary> serve --listen <address>`입니다. Watch 모드는 여기에 private control flag와
   상속 listener/control descriptor를 전달합니다. 공개 `project/server.Serve`가 resource와 주소를 시작 전에 검증합니다.
   Project root cwd와 snapshotted application environment, user stdout/stderr를 전달하며 global은 DB URL/schema를 해석하지 않습니다.
6. Runtime child는 별도 long-lived owner가 새 process group에서 시작합니다. 단일 실행은 operator SIGINT를 group에 한 번 전달하고 child
   Web shutdown보다 긴 bounded grace 동안 direct exit를 관찰합니다. Clean exit 0이면 global exit 0/SIGKILL 0, timeout이면 group
   SIGKILL·direct reap 뒤 `project_interrupted`/130입니다. Context cancellation, unexpected exit, stream/cleanup failure는 closed
   nonzero taxonomy로 반환합니다.
7. Global-owned private workspace는 모든 normal/error/handled interrupt 경로에서 exactly-once cleanup하고 성공한 post-check에서
   residue 0을 보장합니다. Arbitrary application side effect, same-user continuous mutation/ABA, parent crash/SIGKILL scavenging과
   independently daemonized descendant까지 막는다고 주장하지 않습니다.
8. `runserver`의 watch는 source/build/startup 후보를 교체하며 generated source publish/repair·migration·계정·권한 변경이나
   업무 command를 실행하지 않습니다. 프로젝트는 명시적인 settings/database 구성과 resource opener를 소유합니다.

## 현재 watch/reload의 게시와 소유권

후보 준비 전후의 `go list -deps -e -json -mod=readonly` 결과에서 선언/runtime의 실제 dependency graph를 얻습니다.
사용 중인 로컬 package·replacement의 compiler 입력과 embed 자산, module 선언·manifest·descriptor를 bounded byte hash로
관측합니다. 파일 mtime만 비교하지 않으며 새 compiler 파일·embed match·없던 로컬 package의 생성도 감지합니다.
고정 module과 toolchain의 내용은 Go의 소유권을 유지합니다. Symlink/교체된 root·열린 generation journal은 거부하며
정규화한 최초 root identity를 다른 project로 자동 교체하지 않습니다. 테스트 파일과 일반 DB 저장은 감시 입력이 아닙니다.
최초 source snapshot은 열린 journal을 hash에 기록하고 기존 generated-bundle preflight가 그 원인을 진단하게 합니다.
Journal이 사라져도 snapshot이 달라지므로 이전 후보를 승인할 수 없으며, 후속 source 검사와 실제 게시는 journal을 거부합니다.

부모가 한 TCP listener를 유지합니다. 새 후보는 복제 descriptor를 받아 Start·Ready와 HTTP accept 준비를 끝낸 뒤
상속된 private socket에 준비 완료를 알리고 대기합니다. 부모는 시작 후에도 source를 검사하며 stdout은 제어 입력으로
사용하지 않습니다. 정상 후보가 준비되면 기존 listener의 닫힘을 확인한 뒤 새 후보를 활성화합니다. 같은 주소를 유지하고
TCP proxy를 추가하지 않으므로 request의 client identity/authority를 다시 작성하지 않습니다.

이미 전송한 Retire·Activate는 각 제어 교환의 기존 30초 기한 안에서 확인 응답을 받습니다. Invocation 취소가 그 연결을
먼저 닫아 자식의 실제 admission 상태를 잃지 않게 합니다. Retire 확인 뒤 취소가 있으면 새 앱을 활성화하지 않고,
Activate 확인 중 취소가 들어오면 확인된 게시를 기록한 뒤 종료합니다. 후보 정리 실패는 정상 취소로 덮지 않습니다.

기존 앱은 이미 받은 HTTP 요청을 처리하고 앱 context·DB를 마지막에 정리합니다. 동시에 활성 runtime 하나와 준비 후보
하나만 소유하며 이전 후보/퇴역 앱의 종료·process group 회수·workspace 정리를 join한 다음 후속 build를 시작합니다.
Retire를 확인한 앱은 정상 exit를 grace 동안 먼저 기다립니다. 종료 직전 signal handler가 복원된 앱에 불필요한 SIGINT를
다시 보내지 않으며, grace를 넘기면 기존 취소·group 회수 경계를 적용합니다.
준비·제어 교환의 context와 장기 runtime의 종료 수명은 분리합니다. 전역 interrupt나 부모 context 취소는 준비를 중단하고,
감독자가 각 runtime을 직접 join합니다. 이미 Retire한 앱의 정상 종료 대기를 우회해 같은 취소를 다시 전달하지 않습니다.
소스가 다시 바뀌면 준비 중 후보를 취소하고 최신 변경을 모읍니다. Build/startup/source 검사 실패는 기존 정상 앱을 유지하고
닫힌 rejection code를 출력합니다. 최초 시작 실패와 게시 이후의 process/stream/cleanup 실패는 명시적 전체 실패입니다.

Process grace는 기본 20초로 HTTP 기본 drain 5초와 app cleanup 10초를 포함합니다. 부모의 정상 SIGINT/SIGTERM과 제어
socket 상실은 자식의 drain/Stop을 유도하며, 협력하지 않는 자손은 bounded group kill과 reap의 대상입니다. 게시된 앱이
강제 종료되거나 cleanup에 실패하면 정상 종료로 보고하지 않습니다. Fatal parent crash의 임시 폴더 자동 회수나 독립
daemon 자손의 완전한 소유권은 보장하지 않습니다.

환경은 최초 invocation의 snapshot입니다. Migration은 명시적 명령이고 DB 쓰기는 rebuild를 유발하지 않으므로, migration을
마친 뒤 실패 후보를 다시 준비하려면 source를 저장하거나 서버를 재시작합니다. Startup의 schema/resource 검사는 프로젝트가
소유합니다. 이전 앱과 호환되지 않는 migration의 운영 안전성은 자동 reload가 제공하는 보장이 아닙니다.
SQLite backend는 모든 물리 연결에서 double-quoted string fallback을 끕니다. 아직 적용하지 않은 필드의 quoted column이
문자열 상수로 반환되어 잘못된 startup 검사를 통과하지 않게 하며, schema 변경 명령은 여전히 명시적입니다.
기본 Article 앱의 로그인 session은 DB에 유지되지만 CSRF 서명 key는 process에 속합니다. Reload 후 이전 token은 거부하고
새 안전한 요청으로 token을 받은 다음 쓰기를 수행합니다. 여러 process에서 key를 공유하는 정책은 별도의 명시적 구성을 사용합니다.

## 결과

- Global CLI와 separate generated-aware Web runtime이 처음으로 실제 사용자 흐름에서 연결됩니다.
- Descriptor에 optional current capability가 추가되지만 project public Go API, ProjectSpec, generated ABI와 persisted schema/migration
  format은 바뀌지 않습니다.
- Strong stale preflight 때문에 후보마다 declaration/runtime build 비용이 있습니다. Watch의 실제 변경 비용은 native 외부 앱
  흐름에서 검증하며, 최소 compile이나 설계 채택만으로 성능·환경별 완료를 주장하지 않습니다. Persistent runner cache는 후속입니다.
- Runtime application output/environment는 project-owned이므로 global private protocol과 다른 trust/diagnostic 경계를 가집니다.
- Safe local module proxy가 `GOPROXY` 앞에 추가될 수 있는 점 외에는 build/runner도 non-private ambient environment와 explicit
  credential/tool helpers를 볼 수 있습니다. Project와 toolchain을 신뢰하지
  않는 환경의 secret confidentiality를 이 단면이 보장하지 않습니다.
- Q-010의 direct runserver 하위 경계와 Q-017의 generated runtime usability는 진전하지만 semver/upgrade/general raw-model UX가 남아
  둘 다 닫지 않습니다.

## 의도적으로 결정하지 않은 것

- Installed/persistent runner cache와 지원 릴리스의 semver/upgrade 정책
- Auto-generate/migrate/repair와 실행 중 환경 변경
- Non-loopback/TLS/production proxy tuning과 Windows
- 일반 DB 설정·사용자 command의 별도 결정은 해당 공개 package와 현행 아키텍처를 따릅니다.
- Dynamic route/request transaction/background/streaming/DTL/Form/Auth/Admin/API
- Parent crash/fatal-signal stale-temp scavenging, hostile same-user path mutation와 daemonized descendant ownership
