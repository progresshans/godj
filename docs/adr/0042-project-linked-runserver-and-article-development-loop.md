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
2. Global CLI는 `runserver`, optional ordered `--project <godj.toml>`, optional trailing `--addr <address>`만 받습니다.
   기본 주소는 `127.0.0.1:8000`; first slice는 exact IPv4 loopback literal과 canonical port 0..65535만 허용합니다.
3. One retained project selection과 one private external workspace에서 declaration runner를 exactly once build/run하고 current
   GeneratedBundle을 계산합니다. `projectgenerate.CheckRoot`가 committed manifest/source와 interrupted publication을 read-only로
   검증합니다. Stable drift에서는 runtime build/start와 GoDj-owned Article runtime DB I/O가 0입니다.
4. Clean preflight 뒤 exact
   `go build -buildvcs=false -mod=readonly -o <private>/godj-project-server <runserver_package>`를 shell 없이 실행합니다. Build는
   existing external 0700 workspace와 private cache/temp/home, GOWORK-off/GOTOOLCHAIN-local policy를 사용합니다. Private keys는
   교체되고 safe local module proxy가 있으면 `GOPROXY` 앞에 추가되며, 그 밖의 non-private ambient environment는
   declaration/runtime build에도 남으므로 이는 credential sandbox가 아닙니다. Runtime build 뒤 같은 selected
   root와 bundle을 다시 check하고 달라졌으면 child를 시작하지 않습니다.
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
- Safe local module proxy가 `GOPROXY` 앞에 추가될 수 있는 점 외에는 build/runner도 non-private ambient environment와 explicit
  credential/tool helpers를 볼 수 있습니다. Project와 toolchain을 신뢰하지
  않는 환경의 secret confidentiality를 이 단면이 보장하지 않습니다.
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

- [x] Descriptor optional capability, closed argv/address와 invalid-before-selection tests
- [x] Strong preflight의 declaration runner exactly once, stable drift에서 runtime build/start/GoDj-owned Article runtime DB I/O 0와 project-tree no-write
- [x] Build argv/env/no-shell/private workspace and post-build bundle recheck
- [x] Long-lived streaming child의 clean SIGINT/SIGKILL 0, hung child conditional kill, direct reap/output failure/cleanup tests
- [x] Actual global CLI + pre-migrated SQLite Article child HTTP and repeated port reuse
- [x] Digest-pinned local PostgreSQL 17.10 Article child sentinel normal/race/CGO-disabled, pass 1/skip 0
- [ ] Four-coordinate portable와 hosted PostgreSQL 17.10 exact-head required gates
- [x] Final full/Linux-386/repository-external clean-copy and independent final audit

## 현재 구현 상태

Product source `23b1936f46c20e46e4aa689dc6387a78a9847877`은 위 1~8 결정을 구현합니다. PostgreSQL/CI checkpoint
`60da43b64cbc763f0700841ed821401e9a7253e0`은 portable SQLite/stale/forced-cleanup pass/no-skip과 PostgreSQL
required pass/no-skip을 서로 다른 lane에 잠갔습니다. Clean-cache correction
`6101140ef58578ad899c6699fa208b90bc527f81`은 copied fixture에서 `go mod tidy`를 제거하고 current root
dependency/checksum snapshot으로 readonly build만 수행합니다. Clean-checkout fixture correction
`2a61376cdc15cc7a2481210dbf6d3f105517c7a2`는 Git이 빈 디렉터리를 보존하지 않는 checkout에서도 interrupted
publication case가 필요한 transaction directory를 명시적으로 만듭니다.
Backend-close ownership correction `810149fd90ecf0b3a9cb7b4b98344476082ce769`은 Article runtime의 injected
backend/listener close를 각각 정확히 한 번 계수합니다.

SQLite와 digest-pinned PostgreSQL 17.10 local actual 및 current `810149f...` affected normal/race/CGO-disabled/vet가
통과했습니다. Initial documentation checkpoint `47b0eb8...`의 EVID-120 local final 뒤 first submitted
`46a57aa...` run `32657774073`은 26 jobs success와 macOS Intel product job의 exact 20분 timeout으로 끝났으므로
hosted success가 아닙니다. Correction `2b49938...`은 그 matrix만 30분으로 늘리고 central/runserver lock으로
exact 값을 고정했습니다. EVID-121에서 corrected full, all-package Linux/386 compile-only, 803-file
repository-external archive와 두 독립 audit가 다시 통과했습니다.
PostgreSQL actual은 global CLI → generated runtime → advanced HTTP → clean SIGINT → backend
reopen 뒤 migration history 1건/9행 exact durability를 증명합니다. DB service restart, query count, 전체 failure taxonomy와 spawned
child binary 자체의 race 계측은 각각 기존 PostgreSQL restart, Article handler, unit/process evidence 소유이며 이 black-box 하나의
주장이 아닙니다.

이 ADR은 corrected locally frozen 구현 후보가 되었지만 exact-head hosted matrix가 끝날 때까지 Proposed입니다.
WEB-011..020 문서나 local actual만으로 hosted `Verified`, production readiness, Windows/non-loopback 지원을 주장하지 않습니다.
