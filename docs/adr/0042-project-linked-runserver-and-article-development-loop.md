# ADR-0042: Project-linked Runserver and Generated-aware Development Loop

- 상태: Proposed
- 날짜: 2026-08-24
- 관련 work/contract: [GDJ-0042](../../work/0042-project-linked-runserver-and-article-development-loop.md), WEB-011..020, Q-010, Q-017, M5
- 선행 결정: [ADR-0004](0004-cli-and-project-binary.md), [ADR-0022](0022-project-runtime-and-global-migration-check.md),
  [ADR-0036](0036-project-schema-generated-bundle-and-recoverable-publication.md),
  [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md)
- 채택 시 부분 대체: [ADR-0021](0021-project-linked-migration-check.md)의 descriptor exact-three-semantic-line 조항

## 맥락

Global `godj`는 project-linked migration check와 recoverable `generate --check`를 제공하고, Article example에는 generated
model/project package를 import하는 별도 site binary와 graceful Web server가 있습니다. 그러나 사용자는 아직 site package를
직접 build하고 DB argv를 조립해야 합니다. ADR-0004가 의도한 project-aware command 경험과 실제 generated Web 흐름이
연결되지 않았습니다.

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
검증합니다.

## 제안 결정

1. Current descriptor format 1은 exact `package` 뒤 optional
   `runserver_package = "./cmd/site"` 한 줄을 허용합니다. 같은 strict path grammar와 canonical ordering을 사용하고 두 package는
   달라야 합니다. Absent field는 migration/generate에 유효하지만 runserver는 `runserver_not_configured`로 닫습니다. Legacy reader,
   alternate profile 또는 fixed-path fallback은 만들지 않습니다. 이는 ADR-0021의 exact three-semantic-line descriptor 조항만
   additive current capability로 후속 확장합니다.
2. Global CLI는 `runserver`, optional ordered `--project <godj.toml>`, optional trailing `--addr <address>`만 받습니다.
   기본 주소는 `127.0.0.1:8000`; first slice는 exact IPv4 loopback literal과 canonical port 0..65535만 허용합니다.
3. One retained project selection과 one private external workspace에서 declaration runner를 exactly once build/run하고 current
   GeneratedBundle을 계산합니다. `projectgenerate.CheckRoot`가 committed manifest/source와 interrupted publication을 read-only로
   검증합니다. Stable drift에서는 runtime build/start와 GoDj-owned DB I/O가 0입니다.
4. Clean preflight 뒤 exact
   `go build -buildvcs=false -mod=readonly -o <private>/godj-project-server <runserver_package>`를 shell 없이 실행합니다. Build는
   existing scrubbed external 0700 workspace/GOWORK-off/GOTOOLCHAIN-local policy를 사용합니다. Runtime build 뒤 같은 selected root와
   bundle을 다시 check하고 달라졌으면 child를 시작하지 않습니다.
5. Runtime argv는 exact `<binary> serve --listen <address>`입니다. Project root cwd와 snapshotted ambient application environment,
   user stdout/stderr를 전달합니다. Global은 DB URL/schema를 해석·출력하지 않습니다.
6. Runtime child는 별도 long-lived owner가 새 process group에서 시작합니다. Operator SIGINT를 group에 한 번 전달하고 child
   Web shutdown보다 긴 bounded grace 동안 direct exit를 관찰합니다. Clean exit 0이면 global exit 0/SIGKILL 0, timeout이면 group
   SIGKILL·direct reap 뒤 `project_interrupted`/130입니다. Context cancellation, unexpected exit, stream/cleanup failure는 closed
   nonzero taxonomy로 반환합니다.
7. Global-owned private workspace는 모든 normal/error/handled interrupt 경로에서 exactly-once cleanup하고 성공한 post-check에서
   residue 0을 보장합니다. Arbitrary application side effect, same-user continuous mutation/ABA, parent crash/SIGKILL scavenging과
   independently daemonized descendant까지 막는다고 주장하지 않습니다.
8. `runserver`는 generated source를 publish/repair하거나 migration/retry/reload/background task를 실행하지 않습니다. Article
   SQLite/PostgreSQL configuration은 example-owned environment이며 framework DB settings API가 아닙니다.

## 결과

- Global CLI와 separate generated-aware Web runtime이 처음으로 실제 사용자 흐름에서 연결됩니다.
- Descriptor에 optional current capability가 추가되지만 project public Go API, ProjectSpec, generated ABI와 persisted schema/migration
  format은 바뀌지 않습니다.
- Strong stale preflight 때문에 declaration runner build/run 비용이 있고 runtime build도 invocation마다 수행합니다. Persistent cache와
  reload는 후속입니다.
- Runtime application output/environment는 project-owned이므로 global private protocol과 다른 trust/diagnostic 경계를 가집니다.
- Q-010의 direct runserver 하위 경계와 Q-017의 generated runtime usability는 진전하지만 semver/upgrade/general raw-model UX가 남아
  둘 다 닫지 않습니다.

## 의도적으로 결정하지 않은 것

- Installed/persistent runner cache, semver negotiation, general command/custom command registry
- Auto-generate/migrate/repair, watch/reload, hot settings와 source rebuild
- Non-loopback/TLS/production proxy tuning과 Windows
- Framework-wide database settings/env schema
- Dynamic route/request transaction/background/streaming/DTL/Form/Auth/Admin/API
- Parent crash/fatal-signal stale-temp scavenging, hostile same-user path mutation와 daemonized descendant ownership

## 검증 계획

- Descriptor optional capability, closed argv/address와 invalid-before-selection tests
- Strong preflight의 declaration runner exactly once, stable drift에서 runtime build/start/DB 0와 project-tree no-write
- Build argv/env/no-shell/private workspace and post-build bundle recheck
- Long-lived streaming child의 clean SIGINT/SIGKILL 0, hung child conditional kill, direct reap/output failure/cleanup tests
- Actual global CLI + pre-migrated SQLite Article child HTTP and repeated port reuse
- Hosted PostgreSQL 17 required Article child sentinel with skip 0
- Affected normal/race/CGO0/vet, Linux/386, final full/repository-external clean-copy and independent audit

이 ADR은 Phase A/B product proof 전까지 Proposed입니다. WEB-011..020 문서만으로 구현/지원/Verified를 주장하지 않습니다.
