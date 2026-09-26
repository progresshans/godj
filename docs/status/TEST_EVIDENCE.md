# 테스트 증거

현재 변경의 실행 결과는 이 파일에 한 번만 기록한다. 설계 채택, 코드 존재, 특정 환경에서의 검증은 서로 다른 상태다.
미실행·비대상·환경 실패를 PASS로 표현하지 않으며 다른 source의 성공을 현재 실행 결과로 옮기지 않는다.

## GDJ-0100 — 관리자 비밀번호 교체와 대상별 세션 폐기

2026-09-27, `9cb5085c` 뒤의 변경이다. Non-Markdown **2,279 파일** source map SHA-256
`a271708620a21c3f4667151ab414b6981c489fcbb4dadc9167a951f382e607c5`를 고정했다.
Darwin arm64 / Go 1.26.5, 실제 SQLite와 private PostgreSQL **17.10**에서 **12 packages / 283 required entries**
(227 roots와 명시한 password-management subcase 56개)의 completion·no-skip을 검사했다.
Auth·identity·systemstate·Admin·Web/API session·Bearer 전체, native snapshot/identity/adoption roots,
Article·siteapp·Helpdesk 소비자 전체를 선택했다. Generated schema/ABI 변경은 없으며 이 묶음의 별도 process/Hosted full은 아직 실행하지 않았다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 848 PASS / skip 0 | 28.584초 |
| race | 848 PASS / skip 0 | 162.081초 |
| CGO=0 | 848 PASS / skip 0 | 35.037초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`에 mode별 `-race` 또는 `CGO_ENABLED=0`.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`이며 PostgreSQL image는
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
양 native 연결은 각각 max-open=1로 고정했다. Password Hash 안에서 같은 backend의 write transaction을 완료하여
읽기 scope를 종료했음을 확인했다. 별도 연결의 동시 expected-revision 요청은 하나만 commit했다.

260개 실제 session으로 batch 경계와 target만의 폐기, 다른 사용자/anonymous row의 byte 보존을 확인했다.
재접속한 runtime의 기존 cookie 거부·다른 사용자 cookie 유지·옛 비밀번호 거부·새 비밀번호 로그인을 실제 HTTP에서 확인했다.
현재 group grant·deny overlay, hash 중 actor 비활성/그룹 권한 회수·대상 revision/hash 변경, stale/missing 입력의 password work 생략,
비정상/동일 encoded hash 거부, update/두 번째 session delete/audit 실패, 뒤 batch의 payload 손상 시 전체 rollback을 검사했다.
각 주입 fault는 지정한 저장 단계에 실제 도달했음을 확인한다. Callback/읽기 종료 실패·누락/중복/nil callback·삼킨 오류도 성공 Profile을 게시하지 않는다.
Unknown commit과 unknown rollback의 분류·성공 값 미게시·자동 재시도 부재, 확정 commit 뒤 늦은 취소를 검증했다.
Unknown 시 durable 상태는 직접 검사했지만 특정 command receipt/idempotency API를 구현한 것으로 주장하지 않는다.

Go overlay **5개 변형 × 양 DB = 10/10 실제 assertion 탐지**: 현재 인가 우회, revision 검사 제거,
대상 session 격리 제거, audit 오류 무시, unknown commit의 성공 게시. Build 실패는 탐지로 세지 않았다.
Checkpoint와 control 모두 source 전후 동일, PostgreSQL 잔여 table·owned schema·다른 connection `0|0|0`, private container 제거 완료.
영향 `go vet`, gofmt/diff와 문서 링크, CI Python **41/41**을 통과했다.

초기 source `d316d7c9…`에서는 새 fixture와 service의 First 조회에 정렬이 없어 native 검사가 실패했다.
명시적 ID 정렬을 추가하고 수정 source에서 위 세 모드를 새로 실행했다. 초기 CI 도구 호출의 디렉터리 오기는 `make ci-tools-test`로 수정했다.
Source별 실패/성공 stream을 보존하며 초기 실행을 현재 PASS에 합치지 않는다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.
- `identity-password-checkpoint-1790456249178561000/receipt.json`: 세 모드의 roster·원본 JSON stream·전후 source map.
- `identity-password-checkpoint-1790456143158068000/receipt.json`: 초기 unordered-query 실패.
- `identity-password-controls-1790456345270990000/receipt.json`: 다섯 source overlay와 양 DB assertion 결과.

이 묶음은 관리자 password service·durable session/auth 소비자 연결까지다. 전용 관리 Form/Admin/API·독립 generated client,
나머지 사용자/그룹/권한 관리와 self-service/reset, GDJ-0100의 전체 통합 milestone은 아직 미완료다.

## GDJ-0100 — 저장 인증·identity 전환·durable 소비자 통합

2026-09-27, 게시 기준 `f7371ac664114d06c366a3b5a6b49db1a8c6bbea`의 후속 변경이다.
Non-Markdown **2,272 파일** source map SHA-256
`df5724eb5acb6e090cce6442a4bb3c8c32ac091b5518ede2b5af3ea06074ec81`을 고정했다.
아래 각 checkpoint/negative control의 실행 전후 source가 같고, 새 source의 전체 platform/Hosted full은 실행하지 않았다.

구현 commit `4ff0007900e113542b8ad8aed51df16eed9689df`를 Draft PR #1에 게시했다.
[첫 Hosted Fast](https://github.com/progresshans/godj/actions/runs/36269843295)는 Go 실행 전 CI 도구 검사에서 실패했다.
`relation-required.txt`·`postgres-core-required.txt`에 추가한 중복 빈 줄이 원인이었다. 실제 필수 test 항목은 빠지거나 중복되지 않았다.
빈 줄만 제거한 source map은 `85889e77b8981b6bf1128d9881468970c4bf60a833af6458f53b060f8bc0f3ab`이다.
전후 map에서 이 두 roster 외 모든 non-Markdown bytes가 같음을 확인했다. 로컬 CI Python **41/41**을 통과했다.
위 source의 Go 구현·생성물·test는 아래 checkpoint와 같다. 수정 commit `610ebe18e94ed8566fc6a57a8a26ace1cfac18ba`의
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36270067564)는 실제 **Fast Go feedback** step까지 성공했다.
Documentation-only skip이나 Hosted full 성공으로 해석하지 않는다.
Source binding이 달라진 Linux producer 2 roots를 새로 실행해 **2 PASS / skip 0**, 43.688초를 기록했다.
새 capture/checksum을 읽은 실제 godjcheck의 **30 contracts**도 통과했다. 전후 source 동일, PG `0|0|0`, container/network 제거 완료.


Darwin arm64 / Go 1.26.5에서 **28 packages, 460 required roots**를 선언하고 전체 test/package completion과 no-skip을 검사했다.
Auth·identity·systemstate·Admin·Session/Bearer adapter·project/linked/protocol 전체, createsuperuser 관련 CLI roots,
양 DB의 snapshot/identity roots, Article·Helpdesk 전체 소비자, system-state worker/product/restart와 두 attestation owner,
외부 module operator product, GDJ-0055와 AUTH/Admin 관찰을 포함한다. 원본 roster와 실행 명령이 scope의 기준이다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,422 PASS / skip 0 | 117.489초 |
| race | 1,422 PASS / skip 0 | 339.089초 |
| CGO=0 | 1,422 PASS / skip 0 | 125.332초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`와 mode별 `-race`/`CGO_ENABLED=0`.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`; 실제 SQLite와 private PostgreSQL **17.10**을 사용했다.
PG image는 `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
Normal에서 이미 같은 source/roster로 성공한 core·CLI·native·consumer·conformance 결과는 다시 실행하지 않고 원본 기록을 참조했다.
실패했던 process 그룹만 환경을 바로잡아 다시 실행했다. Receipt에 `reused_from`과 원본 경로가 있으며 다른 source의 PASS를 전이하지 않았다.
각 모드의 race는 해당 test binary의 검사다. 고정 옵션으로 따로 빌드하는 child까지 모두 race instrumented라고 주장하지 않는다.
종료 뒤 public table·owned schema·다른 connection `0|0|0`, private container 제거를 확인했다.

Identity 이전은 실제 옛 `0001` operator·session·audit에서 시작한다. 새 User의 물리 PK가 달라도 opaque principal ID·encoded password·
stamp·직접 grant와 기존 Permission 표시 이름을 보존한다. Session/audit 원본 행은 byte 단위로 같다.
새 User/Permission/link/receipt 생성과 source 비활성 표시 중 실패, target ID/username 충돌, 두 native 연결의 동시 이전/첫 생성,
알려진 rollback·취소 중 불확실한 rollback·commit 응답 손실·확정 commit 뒤 늦은 취소를 검증했다.
확정 rollback에서는 새 User/권한/receipt 행이 남지 않는다. Unknown 결과에는 성공 receipt를 게시하지 않고, 저장된 전환은 재시도 없이 durable receipt로 확인한다. 옛 인증/provisioning은 다시 활성화되지 않는다.
Read scope 종료 실패·누락/중복 callback·nil reader·callback 오류를 삼킨 backend도 Runtime/receipt의 부분 결과를 게시하지 않는다.

Helpdesk는 기존 operator 권한 CAS 뒤 명시적으로 이전하고 재접속한 Admin/API와 collection 동시 수정 runtime을 실행했다.
Article의 실제 `adoptoperator --staff=true --superuser=false` 명령 경계는 기존 비밀번호·세션으로 재개한 Admin 진입과 `--inspect`,
재실행 거부를 확인한다. 새 `createsuperuser`는 첫 active/staff/superuser를 저장하고 siteapp/runserver는 OpenIdentity를 사용한다.
External module의 실제 TTY/global CLI·별도 server 프로세스·재시작·잘린/없는 응답·backend close/output 실패를 검사했다.
Secret scan은 legacy row 외에 User와 전환 fingerprint 저장소도 포함한다. Artifact source binding도 새 identity 코드/생성물/migration을 포함한다.

고정 Linux amd64 / Go 1.26.5 producer **2 roots / 2 PASS / skip 0**를 별도로 실행했다. 총 246.645초(준비·빌드 포함),
SDK image `golang@sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`, 동일 PG 17.10 image,
명시적인 Docker `--platform linux/amd64`, read-only source/module mount와 private network를 사용했다.
실제 PostgreSQL two-process coordination 및 PostgreSQL+SQLite external operator producer가 현재 source에 묶인 canonical captures를 만들었다.
Capture bytes는 변경하지 않고 owner별 canonical filename·SHA256SUMS를 준비하여 실제 godjcheck가 읽도록 했다.
**System-state 30 contracts**가 검토된 DEV-0008 기대와 일치했다. Go consumer가 profile·checksum·현재 source를 다시 검증했다.
이는 로컬 Linux 실행이며 GitHub run/producer provenance를 만들거나 Hosted 성공으로 표시하지 않았다. PG `0|0|0`, container와 network 제거 완료.

Credential을 `%d`/`%f`/잘못된 `%p`/`%w`로 출력할 때 encoded hash가 노출되는 결함을 marker로 재현했다.
Credential·Account·Memory/StoredAuthenticator·Directory·receipt를 opaque pointer state와 Formatter로 고친 뒤 회귀를 통과했다.
Go overlay **8/8 실제 assertion 탐지**: credential reflection 노출, source framing 제거, 채택한 hash 변경, grant 누락,
legacy source active 유지, unknown adoption의 성공 게시, 기존 operator의 public-only downgrade, read 실패의 Runtime 게시.
첫 grant-omission overlay의 변수 재선언 compile 오류는 탐지로 세지 않았고 수정한 overlay의 실제 실패를 따로 기록했다.

고정 uv **0.10.12**와 frozen Django profile로 system-state oracle을 재생성했다. 환경/profile은 그대로이며 **SYS-028 한 관찰만** 바뀌었다.
이는 independent GoDj decision의 startup 계약 변경이며 새로운 Django 외부 동작 측정으로 주장하지 않는다.
Default uv 0.12.3의 profile mismatch, 초기 Article memory role/단일-app 기대값, SQLite 테스트 연결의 busy timeout 누락,
PG 요청 취소를 항상 확정 rollback으로 보던 테스트 기대값을 보정했다. 성공한 결과만 위 inventory에 포함했다.
Darwin에서 Linux 전용 producer를 선택한 초기 실행은 exec/profile 단계에서 실패했다. 실행 scope를 나누어 실제 Linux에서 모두 실행했다.
Consumer capture 준비 중 빠진 deviation 인자·canonical name/path·checksum은 수정 전 성공으로 세지 않았다.
영향 `go vet`, Article/Helpdesk generated drift, gofmt/diff, 현행 문서 링크 검사를 통과했다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.
- `identity-integration-1790453485253847000/receipt.json`: 최종 세 모드와 전후 source map, required roster, JSON stream.
- `identity-integration-1790453155978286000/`: 같은 source의 재사용한 normal 결과 및 첫 process 환경 실패.
- `identity-linux-producers-1790453651919127000/receipt.json`: 실제 Linux producers, 원본 capture와 owner별 checksum,
  `consumer-final-receipt.json`·`system-state-actual.json` 및 consumer stream.
- `identity-linux-producers-1790454895131988000/receipt.json`: roster 빈 줄 수정 뒤 source `85889e77…`의 새 producers와 capture,
  owner별 checksum·`consumer-final-receipt.json`·`system-state-actual.json` 및 consumer stream.
- `identity-transition-controls-1790453913764348000/receipt.json`: 8개 control; 같은 source의 선행 3개 결과 경로 포함.

아래 저장 인증 중간 checkpoint는 그 당시의 상태다. 그때 남아 있던 Article/Helpdesk·CLI·재시작 연결은 위 통합에서 검증했다.
사용자/그룹 관리 UI/API·revision/audit 기반 credential maintenance·password reset 등 GDJ-0100의 나머지 범위는 아직 완료하지 않았다.

## GDJ-0100 — 저장 인증·staff admission 중간 checkpoint (소비자 전환 진행 중)

2026-09-27, 게시 기준 `f7371ac664114d06c366a3b5a6b49db1a8c6bbea` 뒤 작업 트리의 변경이다.
이 묶음은 **미게시**이며 아래 핵심 검증을 기존 operator 소비자 전체의 성공으로 해석하지 않는다.
Source map 2,259개 non-Markdown 파일 SHA-256
`a51982effa9f3306dd1e0267c1493fa9f68ec1afebad579088335e5e2f4bc336`에서 전후 동일성을 확인했다.
Go 1.26.5 / Darwin arm64; `auth`, `identity`, `systemstate`, `admin`, `web/sessionauth`,
`api/sessionauth`, `api/bearerauth` 전체 suite와 두 backend의 read_snapshot_test.go roots를 선택했다.
Discovery 뒤 **190 required roots**를 고정하고 package completion·전체 test completion·no-skip을 검사했다.

| 모드 | 완료 inventory | 시간 |
|---|---|---|
| normal | 9 packages / 567 PASS / skip 0 | 3.176초 |
| race | 9 packages / 567 PASS / skip 0 | 23.760초 |
| cgo0 | 9 packages / 567 PASS / skip 0 | 6.062초 |

`go test -json -count=1 -p=3 -timeout=25m -run <고정 roots>`, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`.
SQLite와 private PostgreSQL 17.10을 실제 사용했다. Image는 선행 checkpoint와 같은 digest
`sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
각 private DB의 public table·owned schema·다른 connection은 `0|0|0`, non-force drop과 container 제거를 확인했다.

단일 DB 연결로 password Verify 중 일반 ORM 변경을 실행해 snapshot/admission이 이미 종료되었음을 확인했다.
비밀번호·동일 비밀번호 재해싱·username·inactive·삭제는 과거 credential의 로그인을 거부하고, role 변경은 최신 관찰로 반환했다.
양 DB에서 실제 PBKDF2와 저장 User/Group/Permission을 사용했다. Role 8조합의 인증·등록/미등록 canonical permission을
선행 고정 Django의 두 backend capture와 비교했다. 비정규 문자열은 DEV-0014의 명시한 차이를 유지한다.

실제 HTTP/Web session/JSON API는 두 사용자 격리, 그룹 grant 제거와 직접 grant 추가, superuser 승격/해제,
Authorizer deny overlay, username 변경의 세션 유지, 비밀번호 변경·inactive의 기존 세션 폐기를 확인했다.
이 HTTP fixture의 credential은 DB에 저장하지만 session store는 메모리다. 새 다중 사용자 durable session runtime·재시작 증거가 아니다.
Admin Site의 8개 role 조합은 별도의 실제 route 호출로 active staff만 세션을 만들고 진입하도록 확인했다.
기존 site permission과 superuser는 staff를 대체하지 않는다. Legacy operator policy가 저장하지 않는 role을 거부하는 검사도 포함한다.

Go overlay **5/5 실제 assertion 탐지**: 이전 비밀번호 허용, 이전 권한 반환, inactive grant 허용,
staff 검사 없이 session 게시, superuser의 비정규 permission 허용. Compile 실패는 탐지로 세지 않았다.
Hasher의 취소/비밀 cause 결합은 errors.Is를 보존하면서 진단/JSON에서 숨기는 실제 실패 검사를 추가했다. 영향 vet도 통과했다.
초기 개발 HTTP helper는 nil Header로 cookiejar의 AddCookie를 호출해 panic했고, 기본 Header를 보존하도록 고친 뒤 위 checkpoint를 실행했다.

Checkpoint 뒤 제거된 Admin 상수의 import 잔여를 두 소비자 파일에서 정리했다:
`conformance/projectoperatorproduct/runner_source_unix_test.go`의 embedded Go source와
`conformance/systemstate/restart/restart_unix_test.go`의 실제 import. 다른 non-Markdown 파일은 checkpoint와 같았다.
그 중간 source map은 `257c117f0c1f53ed4483049259113d260b7631f915842589e538c61dda16d1b7`다.
처음 변경 package compile은 restart의 unused import로 실패했으며 수정 후 두 package compile을 통과했다.
Embedded 외부 module의 실제 실행은 operator 전환 통합이 소유한다.

이 중간 source에서 Article·Helpdesk 소비자를 실행했고 둘 다 기존 staff 없는 principal의 Admin login이 거부되어 실패했다.
이를 완료/skip/정상 회귀 결과로 세지 않는다. 이어서 Article의 메모리 fixture와 두 conformance fixture에 staff를 명시했다.
그 source map은 `1bb71a709647aacfdac31adb24856862cf50b443dad77f8c4d9be94a748717cc`이며 앞의 import 보정 두 파일과
이 세 fixture 외의 non-Markdown 파일은 핵심 checkpoint와 같다. Article Admin 실제 SQLite 소비자와 AUTH/Admin conformance의
10 required roots를 고정해 두 package를 실행했다. normal/race/CGO=0 각각 **28 PASS / skip 0**,
1.570/11.857/1.931초이며 실행 전후 source가 같다. 기존 rendered 관찰·고정 reference·session/CSRF 경계도 포함한다.
이것은 memory credential fixture의 역할 선언 수정 검증이다. 실제 durable operator를 사용하는 Helpdesk·Article siteapp·CLI/재시작에는
명시적 데이터 전환이 남아 있고 위 Helpdesk 로그인 실패도 아직 해결하지 않았다.
Startup role을 기존 operator에 덧씌우거나 staff gate를 약화해 이 실패를 우회하지 않는다.
이 작업 트리에는 아직 새 Hosted 검증을 요청하지 않았고, 전체 milestone도 미실행이다.

Evidence root: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/stored-auth-integration-1790448397986320000`.
`checkpoint-1790448398086001000/receipt.json`, 필수 roster·JSON stream·전후 source map, `negative-controls/receipt.json`,
`changed-package-compile.log`, `compile-followup.log`, `legacy-consumers.log`, `memory-consumers/receipt.json`에 성공과 남은 실패를 함께 보존했다.

## GDJ-0100 — 일관된 계정·권한 조회와 읽기 snapshot

2026-09-27, 기준 `c2d446b22dc96ce878bfc7696ae06d4b69039c14`의 후속 변경이다.
2,253개 non-Markdown 파일의 source map SHA-256은
`54635b8b07f4a18e4dcdf899b1c0767c73537bef15d4974c70b0911da278e20b`이며 통합 실행 전후 같다.
Go 1.26.5 / Darwin arm64에서 `auth`, `identity`, `db/internal/readscope`, `db/internal/streamconn`,
`conformance/identityfixture`, `web/sessionauth`의 전체 suite와 아래 DB 파일의 roots를 선택했다.
SQLite: read_snapshot, batch_root, relation_transaction, quarantine_surface, transaction_internal.
PostgreSQL: read_snapshot, batch_root, transaction, relation_mutation, identity.
파일의 실제 test discovery에서 **93 required roots**를 고정하고 전체 package completion·no-skip을 검사했다.

| 모드 | 완료 inventory | 시간 |
|---|---|---|
| normal | 8 packages / 377 PASS / skip 0 | 2.347초 |
| race | 8 packages / 377 PASS / skip 0 | 37.709초 |
| cgo0 | 8 packages / 377 PASS / skip 0 | 4.796초 |

`go test -json -count=1 -p=3 -timeout=25m -run <고정 root 목록>`에 mode별 `-race` 또는 `CGO_ENABLED=0`을 적용했다.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`; 실제 SQLite와 별도 PostgreSQL 17.10 bookworm container를 사용했다.
Image digest는 `sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
각 mode의 private DB는 public table·owned schema·다른 connection `0|0|0`, non-force drop과 container 제거까지 확인했다.

실제 사용자 SELECT 종료 직후 다른 연결이 일반 ORM transaction으로 사용자 flag/username/revision과 permission을 변경했다.
Directory는 첫 사용자 상태와 같은 시점의 권한을 반환하고 새 조회는 새 상태를 읽었다. SQLite WAL과 PostgreSQL에서 실행했다.
직접만/그룹만/중복 grant·다른 사용자 격리·그룹 삭제·256개 허용/257개 거부, 잘못된 저장 revision/credential/username/ID/code,
missing user, 종료 실패/취소 뒤 부분 Account 게시 거부를 실제 저장값으로 확인했다. Profile pointer와 권한 slice는 복사본이다.
Account 진단과 JSON에는 저장 password가 나오지 않으며, driver 오류의 기본 표현은 비밀을 숨기고 errors.Is 원인은 보존한다.

두 backend의 connection fault probe는 열린 rows를 일부러 남긴 callback의 정상/오류/취소/panic/Goexit와 BEGIN/ROLLBACK 실패를 실행했다.
Borrowed Queryer의 mutation capability 부재·만료, rows 정리와 한 번의 종료, 실패 연결 discard와 새 연결 복구를 검사했다.
읽기 종료 실패를 write-unknown으로 표시하지 않는다. 기존 SQLite quarantine·retention과 PostgreSQL write-root unknown-outcome 회귀도 포함했다.
Go overlay **5/5 실제 assertion 탐지**: PostgreSQL READ COMMITTED로 약화, 그룹 권한 누락, 권한 초과 절단,
종료 실패의 부분 게시, Profile timestamp alias 공유. Build 실패는 탐지로 세지 않았고 별도 private DB도 `0|0|0` 후 제거했다.

독립 Django 6.1 / Python 3.14.3 runner는 SQLite 3.50.4와 별도 PostgreSQL **17.5** DB에서 각각 hash seed 0/813으로 실행했다.
양 seed의 전체 capture가 같고 양 backend의 observation·Django source hashes가 같다. PostgreSQL reference DB는 잔여 table 0 확인 후 삭제했다.
이 reference 환경을 GoDj checkpoint의 PostgreSQL 17.10과 혼동하지 않는다. Runner SHA-256:
`1379f55515f43b1a3846ca0b461a9cc77b81e29078a23da6c433844f81a7e9ad`.
User/Group/Permission·ModelBackend·AdminSite의 grant union·held/fresh·group deletion과 role 8조합을 관찰했다.
GoDj는 이 중 grant union·held/fresh·group deletion을 양 reference와 비교한다. Role admission은 reference-only다.
Reference unittest **3/3**, group loader·active·staff 조건을 각각 제거한 **3/3 의미 변화 탐지**, CI Python **41/41**, 영향 vet·format·diff·문서 검사를 통과했다.

생성기는 변경하지 않았다. 기존 외부 identity fixture의 generated drift 검사는 영향 suite에 포함했다.
새 source의 Hosted full은 미실행이며 GDJ-0100 admission·operator adoption·관리 소비자 통합 milestone이 소유한다.
이번 Account는 데이터 조회 경계다. 비밀번호 인증·staff/superuser 허용, 기존 operator adoption, credential 관리 API/UI의 구현 완료가 아니다.
초기 개발 검사의 invalid-username 사례는 기존 계약에서 허용하는 내부 newline을 잘못 사용해 실패했다.
NUL 입력으로 바로잡은 뒤 현 source의 위 checkpoint를 실행했으며 실패 로그도 보존했다.

원본 evidence: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-read-1790445794134289000`의
`checkpoint-1790446462454743000/receipt.json`, 필수 roster·JSON stream·전후 source map, `negative-controls/receipt.json`.
Reference는 같은 상위 directory의 `identity-reference-1790446263392613000/reference-receipt.json`과 backend/seed captures다.

## GDJ-0100 — 재사용 identity 앱과 외부 app 생성 소유권

2026-09-27, 기준 `490cb978afd3e96a107c73583895432663ac38b0`에서 구현한 변경 묶음이다.
User·Group·Permission 선언·초기 historical migration과 같은 Go 모델 타입을 쓰는 호스트 fixture를 연결했다.
호스트는 external app의 파일을 소유하지 않고 전체 관계·삭제 graph를 생성한다. 라이브러리의 네 companion에
schema/ABI marker를 두고 host-owned app의 전체 project seal은 유지한다. AppSpec·wire·manifest·bounded scan/size와
candidate compile, handwritten 예약 메서드·변경 전후 source fence를 함께 연결했다.

실제 migration 후 User의 안전한 초기 flag·revision·빈 profile·nullable login을 확인했다.
사용자·그룹·직접/그룹 권한의 중복 없는 연결과 nested prefetch, 실제 library User type의 FK 조회를 실행했다.
호스트 PROTECT 실패는 User·CASCADE 후손을 보존하며 보호 행 제거 후 삭제는 호스트 후손과 사용자 link만 제거한다.
재사용 Group·Permission은 남는다. 이는 현재 권한을 평가하는 인증 backend나 관리 UI/API의 검증은 아니다.

첫 source는 2,235개 non-Markdown map SHA-256
`031cdceec453293e4b1086319a20edf34c72f7680e175b567ec8a8d235bf9071`다. Go 1.26.5 / Darwin arm64에서
`codegen`, `codegen/consumertest`, `internal/projectwire`, `internal/projectgenerate`·protocol·linked,
identity/relation/cascade/onetoone/relationproduct fixture의 전체 package suite와
`internal/projectcheck`의 TestRunGenerate 10 roots, 새 PostgreSQL identity root를 선택했다.
**296 required roots**를 discovery 후 고정했다. Crash helper는 parent crash tests가 별도 process로 실행하며
standalone helper invocation을 선택하지 않았다. 실제 crash/recovery 부모 검사·필수 실행은 유지했다.

| 모드 | 완료 inventory | 시간 |
|---|---|---|
| normal | 13 packages / 877 PASS / skip 0 | 212.387초 |
| race | 13 packages / 877 PASS / skip 0 | 659.702초 |
| cgo0 | 13 packages / 877 PASS / skip 0 | 221.204초 |

`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`, count=1을 적용했다.
SQLite와 고정 PostgreSQL 17.10 bookworm image의 별도 localhost container를 사용했다.
각 mode의 public table·owned schema·다른 connection은 `0|0|0`, non-force DB drop과 container 제거까지 확인했다.
초기 DB 준비 시 createdb가 실패해 테스트를 시작하지 못한 실행도 남겼다. 당시 stderr를 보관하지 않아 원인을
단정하지 않는다. 후속 실행은 실제 mapped TCP connection 준비를 확인한 뒤 시작했다.

추가 경계 probe에서 `.GODJ` 표기와 case-folded prior file ownership의 누락을 재현했다.
물리 project root를 사용하는 두 probe의 실제 assertion 실패를 보존했다. 초기 probe의 비정규화 임시 root 오류는
별도 진단으로 보관하고 수정했다. Manifest와 동일한 경로 대소문자 규칙, root case alias와 control directory 경계를
적용하고 검사 전용 roster를 쓰기·삭제 journal과 분리했다. 이 수정은 아래 세 파일에만 영향을 준다:
`internal/projectgenerate/external_app.go`, `external_app_test.go`, `source_namespace.go`.
다른 non-Markdown 파일은 첫 checkpoint와 같음을 map diff로 확인했다.

최종 source map은 `c87f06ed9359e87096cec6ce1e5c48ab311a5f0df484b0de481eedddf0dfe652`다.
수정 후 `internal/projectgenerate`·linked, `conformance/identityfixture`, TestRunGenerate의
**99 required roots**를 새 source에서 다시 실행했다. CLI/candidate·rollback/crash·drift와 SQLite 호스트 소비자를 포함한다.

| 모드 | 완료 inventory | 시간 |
|---|---|---|
| normal | 4 packages / 202 PASS / skip 0 | 66.199초 |
| race | 4 packages / 202 PASS / skip 0 | 94.124초 |
| cgo0 | 4 packages / 202 PASS / skip 0 | 58.787초 |

두 checkpoint는 각각 실행 전후 source map이 같다. 서로 다른 source의 결과를 하나의 현재 전체 실행으로 합치지 않는다.
최종 source의 Go overlay **5/5 실제 assertion 탐지**: ABI marker 무시, dependency 변경 fence 제거,
read-only 파일 소유권 검사 제거, control directory 및 prior ownership의 대소문자 규칙 제거.
Build 실패를 control 탐지로 세지 않았다. 별도 dependency init canary도 실행되지 않았다.

7개 project와 standalone relation fixture의 generated drift, 영향 vet·format·diff·문서 검사,
CI 도구 **41/41**을 통과했다. 새 fixture·identity package 및 PostgreSQL root의 CI 실행 owner를 연결했다.
이 source의 Hosted full은 미실행이다. GDJ-0100의 다중 사용자 admission·operator adoption·관리 소비자 통합 뒤
명시한 전체 milestone에서 검증하며, 이전 `01b67211`의 전체 PASS를 새 구현에 전이하지 않는다.

원본 evidence root: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/external-app-identity-1790442058966120000`.
`checkpoint-1790442129401822000/receipt.json`, `case-checkpoint-1790443342760441000/receipt.json`과 JSON streams·필수 roster·source maps,
`negative-controls/receipt.json`, `case-boundary-probe`, `development-diagnostics`, `supporting-checks.json`과
`generate-check-after-case.log`에 전체 결과와 실패 진단을 보관했다.

## GDJ-0099·0100 — ManyToMany와 credential/session의 Hosted 전체 통합

2026-09-27, source `01b67211a083c507d5e69c6be26702439aada559`의
[Hosted full 36253381368](https://github.com/progresshans/godj/actions/runs/36253381368), attempt 1을 확인했다.
**62 jobs 모두 completed/success**이며 최종 job `108444008264`의 실제 로그에서
`scope=full`, `full_platform_verified=true`와 현행 `scopes.selected("full")`의 **8 owner 전체 일치**를 확인했다.
Command products, conformance, exact Darwin, portable Go, PostgreSQL product, project-check product,
Python compatibility, relation products를 포함한다. 고정 필수 목록·no-skip·normal/race/CGO0 조건을 유지했다.
동일 source의 [Fast feedback](https://github.com/progresshans/godj/actions/runs/36253326420)도 실제 실행 step 성공을 확인했다.

같은 실행의 normal PostgreSQL producer job `108435516403`에서 나온 `systemstate-postgres-1`
artifact `10909903745`, job `108435516405`의 `operator-postgres-1` artifact `10909938175`를 내려받았다.
GitHub archive digest·ZIP roster·payload SHA-256과 repository/run/attempt/checkout provenance를 대조했다.
Consumer job `108437345524`의 provenance 및 conformance 실행, 32-bit migration/project-check와 runserver compile,
32-bit relation runtime, 양 oracle checksum 및 reference artifact 무변경 step까지 실제 성공을 확인했다.
검증 보조 스크립트의 오래된 step 이름 `Require a clean worktree`는 이 workflow에 없었으므로 최초 audit은 실패했다.
현행 YAML과 job metadata를 대조해 실제 `Ensure reference artifacts were not rewritten` 및 위 필수 step을 명시한 뒤
재실행했다. 이 step의 보장은 reference 경로 무변경이며 저장소 전체 clean 검사의 대체 증거로 세지 않는다.

Gate 로그 SHA-256: `c9f79d0c3de6f7ffc5924fedb35dc5f5a441680a11fed735142e6be37e223e03`.
Consumer 로그 SHA-256: `9ff8869a0b745d1b072d8b8188d9b58673fe0219943247687e242c9e2fe6b9e7`.
원본은 아래 credential evidence root의 `hosted-full-36253381368/receipt.json`, `gate.log`,
`capture-consumer.log`와 artifact/provenance 파일에 보관했다. 검증에 사용한 checkout의 HEAD도 위 source와 같았다.
GDJ-0099 통합 조건을 완료하지만 GDJ-0100의 다중 사용자 저장·관리 UI/API·operator adoption은 미완료다.
이후 외부 앱 및 identity 모델의 미게시 변경에는 이 전체 PASS를 전이하지 않는다.

## GDJ-0100 — Credential snapshot과 서버 세션 결합 checkpoint

2026-09-27, 기준 `1036bcd079e96260dc5230dab1172e0228f34ce5`에서 구현한 변경 묶음이다.
2,193개 non-Markdown 파일의 map SHA-256은
`a18bb412c536f7fcf18c508e0b7f426d4bd523e96f29eecb9497a5bd940dc0fa`이며 아래 실행 전후 같음을 확인했다.
이는 새 source의 로컬 영향 검증이며 Hosted 전체·다중 사용자 저장의 완료를 뜻하지 않는다.

Authenticate/Resolve가 한 불변 Credential과 Principal을 반환한다. 로그인과 회전은 ID·credential stamp를 함께 저장하고,
다음 요청은 현재 credential의 ID·active·stamp를 검증한다. 비밀번호·재해싱은 이전 세션을 거부하고 권한·username 변경은
다음 요청에 반영한다. 원래 허용한 Principal은 바뀌지 않는다. 다른 ID·비어 있는 credential·누락/위조 stamp,
세션 폐기 실패·인증 I/O 오류와 익명 세션의 일반 앱 데이터 보존을 실제 HTTP에서 확인했다.
로그인 도중 credential 변경과 다른 사용자로 재로그인할 때 두 시점/사용자의 인증 정보를 섞지 않는다.
Operator는 coordinated read의 현재 credential을 반환하고, startup에서 검증한 과거 비밀번호를 새 credential로 승격하지 않는다.
기존 policy CAS·revocation은 그대로 검증하며 startup login verifier 교체는 명시적 reopen을 요구한다.

고정 Django 6.1 / Python 3.14.3의 [독립 runner](../../conformance/runners/django/credential_session_reference.py)는
SQLite 3.50.4 / PostgreSQL 17.5 각각 hash seed 0·813에서 **7개 관찰**을 생성했다. 같은 backend의 전체 bytes와
양 DB의 관찰·source fingerprint가 일치한다. Runner SHA-256은
`17c7deed8a405342244aad694c544713e3fcdbf6cbe5500c94906463a24796bb`다.
두 fixture를 실제 HTTP 검사가 읽으며, Django의 비활성 사용자 session 행 유지와 GoDj의 폐기 차이는
[DEV-0013](../DEVIATIONS.md#dev-0013--credential-session의-go-표현과-invalid-identity-정리)에 별도로 남긴다.
Reference unittest **2/2 PASS**에는 session hash 결합 제거와 direct permission 조회 제거의 두 control이 포함된다.
이 fixture는 광범위한 User/Group lifecycle의 구현 증거가 아니다.

Go 1.26.5·Darwin/arm64의 영향 범위는 `auth`, `web/sessionauth`, `api/sessionauth`, `systemstate`, `admin`,
`api/openapi/consumertest`, `examples/article`, `examples/article/apiapp`, `examples/article/cmd/projectrunner`,
`examples/helpdesk`, `conformance/systemstate/product`, `conformance/systemstate/worker`, `conformance/projectoperatorproduct`다.
211개 필수 root를 discovery 후 고정하고 `go test -json -count=1 -p=3 -timeout=20m`을 실행했다.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다.

| 모드 | 완료 inventory | 실행 시간 |
|---|---|---|
| normal | 13 packages / 591 PASS / skip 0 | 57.577초 |
| race | 13 packages / 591 PASS / skip 0 | 123.036초 |
| CGO=0 | 13 packages / 591 PASS / skip 0 | 61.366초 |

실제 SQLite와 PostgreSQL 17.10을 포함한다. 고정 CI image
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`의
별도 localhost container에 UTF8/C/libc DB를 만들었다. 각 mode 종료 시 public table·owned schema·남은 다른 connection이
`0|0|0`이고 non-force drop을 완료했다. 종료 후 이 container도 정리했다. 기존 로컬 DB·다른 container는 변경하지 않았다.
초기 로컬 PostgreSQL 17.5 실행은 외부 operator의 고정 17.10 fingerprint 조건 때문에 두 test에서 실패했다.
그 실패는 `checkpoint-1790437162143201000`에 남겼으며 version 조건을 약화하거나 skip하지 않고 맞는 환경에서 재실행했다.

기존 인증·세션·API·systemstate conformance adapter의 관련 **46 required roots / 124 PASS / skip 0**도 normal로 확인했다.
실행 선택은 `^(TestGDJ0043|TestGDJ0044|TestGDJ0046|TestGDJ0047|TestGDJ0055|TestSystemState)`이며 78.639초다.
새 입력 형식은 실제 generated OpenAPI client와 HTTP 소비자에도 적용했다. 기존 generated-tree drift/누락/변경 검사와
독립 client contract를 위 13 package checkpoint에서 실행했다. Model/Facade ABI·schema generator는 이번에 변경하지 않았다.

Go overlay control **3/3 탐지**: password를 stamp에서 제외, 과거 password 검증 결과 승격 허용,
로그인 검증 직후 Resolve로 다른 credential 관찰을 섞는 변경이 각각 실제 실패 assertion으로 잡혔다.
빌드 실패를 control 탐지로 세지 않았다. CI Python **41/41**, workflow **5 roots / skip 0**,
고정 actionlint v1.7.12(ShellCheck/Pyflakes 비활성), 영향 auth/session/systemstate vet, gofmt·문서·diff 검사도 통과했다.

원본 evidence root:
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/credential-session-1790436757240879000`.
`reference-receipt.json`, `checkpoint-1790437357313061000/receipt.json`과 각 JSON stream/required roster,
`auth-runner-receipt.json`, `negative-controls/receipt.json`, `supporting-checks.json`, `final-source-audit.json`에
명령·필수 실행·결과·source·cleanup을 보관했다. 새로운 Hosted 전체 milestone은 별도로 확인한다.

## GDJ-0099 — 후속 Hosted 좌표와 일반 모드의 시간 제한

Source `1036bcd079e96260dc5230dab1172e0228f34ce5`의
[Hosted full](https://github.com/progresshans/godj/actions/runs/36250036987)에서 Linux amd64 relation race
job `108426465116`은 **35 package / 5,859 PASS / skip 0**으로 완료했다.
로그 SHA-256은 `83d53695518bd08b059260a1d85b2e008f41ac31eb5c6460de2389d44421b452`다.
같은 실행의 PostgreSQL normal producer가 만든 `systemstate-postgres-1` artifact **10908843171**과
`operator-postgres-1` artifact **10908842799**의 archive digest·payload·repository/run/attempt/checkout을 확인했다.

그러나 Intel macOS normal job `108426465058`은 **20분 job 제한 초과** annotation과 함께 cancelled로 끝났다.
시작 `2026-09-26T15:00:22Z`, 종료 `15:20:37Z`이며 cleanup에 consumer·Go compiler가 남았다.
Go package의 완료 보고서가 없어 해당 좌표와 전체 실행을 PASS로 세지 않는다.
같은 source의 capture 성공을 전체 gate 성공으로 합치지 않는다.
관계 matrix의 모든 normal/race/CGO0 좌표를 package 35분·job 45분으로 통일해 내부 timeout 진단의 여유를 둔다.
테스트/필수 목록·race 전파·no-skip은 변경하지 않았다. 이 workflow 변경의 로컬 검사는 위 checkpoint에 포함하며
누적 인증 변경과 함께 다음 Hosted full을 실행한다.
원본은 Ticket evidence root의 `current-full-intel-normal-job.json`, `current-full-intel-normal.log`,
`full-36250036987-linux-race-108426465116.log`, `hosted-full-36250036987/capture-receipt.json`에 보관한다.

## GDJ-0099 — 관계 제품 race의 Hosted 실행 시간 경계

2026-09-26, source `ea9b9599e7c21a114c30e4bde5deaa6b7e8b0eb8`의
[두 번째 Hosted full](https://github.com/progresshans/godj/actions/runs/36248358512)에서 앞선 세 검증 소비자 회귀가 수정됐다.
PostgreSQL 제품 6개 모드와 Linux normal/CGO=0 관계 제품은 통과했지만 Linux amd64 race job
`108421930845`는 GitHub의 **20분 job 제한 초과** annotation과 함께 cancelled로 끝났다.
시작 14:25:44 UTC·종료 14:45:56 UTC이며 마지막 cleanup에는 `consumertest.test`·하위 `go`·`compile` process가 남아 있었다.
Go package의 최종 판정과 필수 실행 보고서가 없으므로 해당 race나 전체 실행을 PASS로 세지 않는다.
이 자료는 외부 시간 제한에서 끊겼음을 증명하며, 모든 내부 검사가 정상 종료될 것이라는 사전 증명은 아니다.

관계 matrix의 race에 대해 기존 Intel macOS의 합산 budget(Go package 35분·job 45분)을 모든 좌표로 적용했다.
Generated module별 실제 compile/runtime·기존 test 목록·race 전파·필수 실행·no-skips를 유지한다.
Normal/CGO=0 설정은 바꾸지 않았다. Runtime이나 테스트의 의미를 바꾸는 변경은 없다.
Non-Markdown 변경은 `.github/workflows/ci.yml` 하나이며 2,186개 source map은
`acf360140a4a0dea6cef2052f78f058fad9a3aebec90314ce782b285c7a8a7cc`다.

고정 actionlint v1.7.12의 YAML/Actions 표현식 검사를 통과했다(ShellCheck/Pyflakes는 해당 명령에서 비활성).
CI 도구 unittest **41/41 PASS**, `TestWorkflow*`의 필수 5 root **5 PASS / skip 0**, diff·문서 검사도 통과했다.
원본은 아래 Ticket evidence root의 `latest-race-budget-checks-path`, `race-budget-audit.json`,
`relation-race-timeout-check.json`, `full-36248358512-job-108421930845.json`·`.log`에 보관한다.

두 번째 실행의 `systemstate-postgres-1` artifact **10907833422**와 `operator-postgres-1` artifact **10908840441**은
archive digest·payload SHA256·repository/run/attempt/checkout·성공한 normal producer job을 직접 대조했다.
상세 원본은 `hosted-full-36248358512/capture-receipt.json`에 있다. 이는 ea9b9599의 두 capture 검증이며 전체 gate 성공이 아니다.
실행 시간 경계를 바꾼 새 source의 full gate와 새 capture는 다시 확인해야 한다. 이전의 일부 성공을 옮기지 않는다.

## GDJ-0099 — Hosted 통합에서 드러난 검증 소비자 갱신

2026-09-26, Ticket 소비자 source `d05468800ef9db483e61100c6f8f330065256adb`를 게시하고
[PR feedback](https://github.com/progresshans/godj/actions/runs/36247428139)의 실제 Fast Go feedback success를 확인했다.
같은 source의 [첫 Hosted full](https://github.com/progresshans/godj/actions/runs/36247440549)은
Linux amd64/arm64 관계 제품 normal·CGO=0에서 아래 세 검사가 실패했다. 이 실행은 전체 PASS가 아니다.

- `TestWorkflowRequiredProductSentinelsRemainInventoried`: core required roster가 파일에서 채워지는데 인라인 빈 initializer만 읽었다.
  같은 외부 roster에서 필수 sentinel을 확인하도록 고쳤다. 기존 Python execution-owner 검사는 실제 workflow shell loader를
  실행해 파일 전체 bytes가 inventory에 도달하는지 확인하며, Go 이벤트 검증의 필수 실행·no-skips 검사는 유지한다.
- `TestProjectFacadeUsesCallbackLocalSession`: borrowed session에 root `Using`을 사용했다. 명시적 `UsingSession`을 사용하고,
  root constructor의 borrowed 입력 거부와 callback 종료 뒤 query/캐시 model 접근이 I/O 없이 실패하는지 추가로 확인했다.
  Callback-local rollback 쓰기 검사도 별도 recorder에 native SessionValidator를 전달한다. Root recorder에 session capability를 합성하지 않는다.
- `TestExternalConsumerCompiles`: 이미 지원하는 `Posts()`가 없어야 한다는 예전 negative 입력이었다.
  외부 consumer에서 실제 reverse collection 조회를 컴파일하고, 결과를 다른 model slice로 받는 경우의 compile rejection으로 갱신했다.

변경한 non-Markdown 파일은 `conformance/internal/protocol/ci_workflow_test.go`,
`conformance/relationdeleteproduct/product_test.go`, `internal/compiletest/compile_test.go`,
`internal/compiletest/testdata/relation_facade/external_consumer.go.txt` 네 개다. 제품 코드·generator·workflow·lock은 앞선 source와 같다.
2,186개 non-Markdown source map은 `5ff176ad18f14d5241878086dc8e0a3b3de8e5e9e4700daa91f602ba650edcf4`다.
`./conformance/internal/protocol ./conformance/relationdeleteproduct ./internal/compiletest` 전체를 Go 1.26.5·macOS arm64에서 실행했다.

| 모드 | 필수 root | run=PASS | skip | 시간 | 실행 디렉터리 |
| --- | ---: | ---: | ---: | ---: | --- |
| normal | 151 | 995 | 0 | 6.5 s | `integration-repair-normal-1790432368008274000` |
| race | 144 | 930 | 0 | 16.8 s | `integration-repair-race-1790432424061077000` |
| cgo0 | 151 | 995 | 0 | 5.4 s | `integration-repair-cgo0-1790432424058302000` |

Standalone compile fixture 검사는 기존 `!race` 실행 소유권에 따라 normal/CGO=0에 포함된다. Runtime/race 실행 누락을 skip으로 숨기지 않았다.
실행 전후 source map과 원본 event/stderr SHA256을 확인했다. `scripts/ci/test_packages.py` **12/12 PASS**에는
실제 shell inventory 전달과 CI package/실행 소유권 회귀가 포함된다. 최초 dotted-module 호출의 import 경로 오류는
`integration-repair-check-import-failure.log`에 보관하고 정식 discovery로 다시 실행했다. Format·diff·문서 검사도 적용했다.
앞선 Ticket 소비자의 6개 package 결과를 이 source의 재실행으로 표현하지 않으며, 변경되지 않은 제품의 영향 근거로 유지한다.
수정 source의 Hosted 전체 재실행과 최종 gate·같은 source capture 확인은 남아 있다.
원본은 아래 Ticket 소비자 evidence root의 `latest-integration-repair-normal-path`, `latest-integration-repair-race-path`,
`latest-integration-repair-cgo0-path`, `integration-repair-checks.json`, `integration-repair-audit.json`에 보관한다.

## GDJ-0099 — Ticket 라벨 집합의 실제 Form/Admin·API·독립 client

2026-09-26, `3cfe3a0026a03470d48423d4d78843f5c37f6522` 위에서 Ticket.labels를 실제 Form/Admin과
API/OpenAPI·독립 ogen client에 공개했다. Pure collection encoder와 exact int64 list serializer를 연결하고,
현재 owner·전체 후보의 Category·admitted 권한 재검증, scalar·Set·실제 응답 재조회를 같은 RelationAtomic에서 실행한다.
POST 생략은 빈 집합, PUT/PATCH 생략은 보존, 명시한 빈 배열은 전체 해제다. 유지한 through ID는 보존한다.
Facade ABI v19·relation object v6은 변경하지 않았다.

고정 Django 6.1·DRF 3.18.0·Python 3.14.3의 [독립 runner](../../conformance/runners/django/ticket_collection_reference.py)를
SQLite 3.50.4·PostgreSQL 17.5(170005), psycopg 3.3.6에서 실행했다. 양 DB×PYTHONHASHSEED 0/813의
48개 canonical 관찰·12개 별도 coercion·outer atomic rollback·validation 후 scope 변화가 결정적이다.
Runner SHA256은 `ca77f9c6cfaff8f9a9f3b6e70c982c023efa264c3b8a2958e0b1f0041a96faee`이며
DRF fields/relations/serializers module SHA256은 raw fixture에 보관한다. 별도 reference unittest **2/2 PASS**는
두 seed·fixture 일치와 replacement 제거/생략의 잘못된 clear라는 두 의미 변경을 탐지했다.
GoDj의 실제 양 DB HTTP는 canonical 48행의 수용/거부·오류 field·최종 row/집합·retained ID를 각각 비교했다.
DRF의 더 넓은 coercion·오류 code taxonomy는 [DEV-0012](../DEVIATIONS.md#dev-0012--choice-json-입력도-scalar-type을-유지)에
차이로 기록하며 parity PASS에 포함하지 않는다. 기본 DRF가 validation 이후의 Category 변경을 재검증한다고 주장하지 않는다.

Serializer는 exact >2^53 키·독립 snapshot·생략/empty/null·원소 index 진단·default/readonly·명시적 loaded reader를 검증한다.
Admin은 25개 이상의 전체 후보, 두 번째 key의 scope 변경, 중복 입력과 누락에 의한 전체 해제, escaped 원문을 검사한다.
JSON API는 인증·권한·CSRF 뒤에 본문을 해석한다. Admin은 bounded form에서 CSRF를 추출한 뒤 인증·권한과 필드 검증으로
진행한다. 거부된 CSRF의 세션 저장소 접근 0은 기존 Admin 회귀로 유지하고, 실제 Ticket의 add/change 거부에서
owner/후보 조회·transaction·쓰기 0을 확인한다. JSON Content-Type을 Admin에 보낸 요청은 transport 400이며
이를 잘못된 403 기대값에 맞추려고 기존 CSRF 경계를 바꾸지 않았다.

실제 scalar UPDATE 뒤 native link 실패, 첫 삭제 뒤 두 번째 삭제 실패, 최종 response 재조회 실패,
실제 link insert 후 context 취소가 scalar·전체 set과 retained ID를 보존한다. Joined rollback unknown은 확정된
400/404가 되지 않고, 실제 commit 후 주입한 outcome unknown도 성공/자동 재시도로 바뀌지 않는다.
두 runtime/독립 DB 연결의 승인된 요청을 transaction 직전에 함께 멈춘 뒤 첫 scalar 쓰기 동안 두 번째 session 진입을
차단하고, 각 응답과 최종 전체 집합이 순서대로 일치하는지 확인했다. 새 backend로 다시 읽어 durability도 확인했다.
이 결과는 cooperative Runtime의 DB fence 범위이며 비협력 raw writer를 포함하지 않는다.

같은 **2,186개 non-Markdown source map**
`ba7153126d3f462846cfa5a052d0b7568f593fc3733c287ca95f52beda3b0c4a`에서
`./serializers ./api/openapi ./api/openapi/consumertest ./examples/helpdesk ./examples/article/apiapp ./admin`
6개 package의 필수 root **145개**와 하위 실행 이벤트를 검사했다. Go 1.26.5·macOS arm64의 영향 검증이다.

| 모드 | run=PASS | skip | 시간 | 실행 디렉터리 |
| --- | ---: | ---: | ---: | --- |
| normal | 2666 | 0 | 7.6 s | `consumer-normal-1790431018237716000` |
| race | 2666 | 0 | 61.9 s | `consumer-race-1790431069262153000` |
| cgo0 | 2666 | 0 | 9.6 s | `consumer-cgo0-1790431069262152000` |

각 모드는 별도 owned PostgreSQL DB에서 수행했고 session/table/custom schema **0|0|0** 확인 뒤 non-force drop했다.
Reference DB도 session/table **0|0** 확인 뒤 non-force drop했다. 모든 실행 전후 source map이 같으며
필수 skip·build failure·잘린 실행을 PASS로 합치지 않았다. 초기 checkpoint의 상세 조회 query-count 기대값,
테스트 wrapper의 RelationSession capability·CSRF bootstrap·PostgreSQL generated-key fixture와 Admin transport 기대값
실패는 `consumer-normal-1790430215847229000`, `consumer-normal-1790430410434292000`,
`consumer-normal-1790430645205146000`에 보존했다. 위 표는 해당 문제를 정리한 최종 source 실행이다.

원본을 바꾸지 않는 Go overlay **6개 의미 변경 control**이 실제 assertion에서 실패했다:
정수 원소의 float64 왕복, 생략을 empty로 해석, 후보 Category 필터 제거, Set 오류 무시,
unknown rollback을 404로 합침, scalar 변경이 없을 때 labels-only PATCH 생략.
마지막 변경은 실제 generated HTTP client의 실패 단계로 탐지했다. Compile 실패를 탐지 성공으로 세지 않았다.

독립 client는 고정 ogen으로 세 profile을 임시 위치에서 재생성하고 checked-in 전체 파일 집합과 비교한 뒤
framework import/replace 없는 별도 module을 offline build해 실제 HTTP로 실행했다. 부모 race 모드도 child에 전달한다.
`helpdesk_ticket_collections`와 `generated_collection_wire`를 필수 receipt에 추가하고 실제 실행 뒤에만 게시한다.
라벨 생성/교체/생략/해제·retained link·전체 foreign set 거부·권한/CSRF와 큰 정수 wire·required response를 확인하고,
부모는 최종 scalar와 정확한 세 through 행(기존 두 행·추가된 owner 링크)을 직접 조회한다. 이 child fixture는 SQLite이며
Helpdesk 양 DB HTTP와 구분한다. Helpdesk OpenAPI SHA256은
`de41e9afa73abcfacd8be616c8e8685bf391f5537d006efe86bb177059cd3858`이다.
생성물 12개 중 4개를 갱신했고 Article 두 spec과 client module/tool lock은 그대로다.

`make format-check generate-check`, 영향 `go vet`, diff check를 통과했다. API generated drift는 위 실제 consumer가 소유한다.
문서 링크·정합성과 전체 변경 파일 소유권·source/log SHA256 재검사는 최종 audit receipt에 남긴다.
원본 root는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/ticket-label-collections`이며 `latest-consumer-reference-path`, `latest-consumer-reference-tests-path`,
`latest-openapi-candidate-path`, `latest-consumer-normal-path`, `latest-consumer-race-path`, `latest-consumer-cgo0-path`,
`latest-consumer-negative-path`, `latest-consumer-checks-path`, `latest-consumer-audit-path`가 상세 기록을 가리킨다.

현재 source의 Hosted full은 다음 통합 milestone이다. 이전 `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[Hosted full](https://github.com/progresshans/godj/actions/runs/35689549739)을 이 source의 성공으로 옮기지 않는다.

## GDJ-0099 — 다중 관계 선택 Form/Admin과 Ticket.labels 선언

2026-09-26, `a8116dd20ca279df806c68b7a03978e1d4f15ab1` 위에서 ModelMultipleChoice의 immutable int64 목록과
Schema IR allowlist projection, Admin SelectMultiple·전체 집합 재검증을 연결했다. 기존 TicketLabel을 채택하는
Ticket.labels/Label.tickets 선언·generated accessor와 `0020_ticket_labels` historical migration을 추가했다.
Ticket의 실제 Form/Admin 필드 노출·transaction 저장·API/OpenAPI/client는 아직 구현하지 않았으므로 이 기반 결과를
소비자 전체 완료로 세지 않는다. Facade ABI v19·relation object v6은 유지한다.

[독립 Django runner](../../conformance/runners/django/model_multiple_choice_reference.py)는 pinned Django 6.1·Python 3.14.3,
SQLite 3.50.4·PostgreSQL 17.5(170005), psycopg 3.3.6으로 실행했다. 양 DB×PYTHONHASHSEED 0/813의
HTML list-of-text **476개 observation**·후보 snapshot·direct Python 관찰이 같고, source module SHA256은
`858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910`이다.
Go Form은 두 fixture의 476행 각각에 cleaned membership·required/error code·raw-input Changed를 비교한다.
필수/optional·빈 목록·순서·중복·단일 선택과 다른 키 별칭·NUL·private/missing key·2^60 key를 포함한다.
Int64 밖 두 입력은 SQLite OverflowError와 PostgreSQL invalid_choice로 달라 별도 보존했다. Go는 invalid_pk_value로
선행 거부하며 해당 행과 HTML로 표현하지 않는 direct Python 입력을 동등 PASS로 합치지 않는다.
별도 reference unittest **2/2 PASS**는 두 hash seed 결정성·fixture 일치와 Changed 무력화/별칭 허용의 두 의미 변경을 검출했다.
Runner SHA256은 `430cb31c877df5356367a4303f54e28b5a9eb1b2839e7dbbc760125efb9ff069`이다.

공통 Form 검증에는 입력/반환/validator/선택 snapshot의 독립 소유권, 부분 cleaned 집합 미공개, nullable/widget/type 거부,
동시 서로 다른 후보 snapshot을 포함한다. Model projection은 생략된 collection 미노출·명시적 pure initial reader를 검사한다.
Admin은 전체 canonical key 목록·optional 빈 집합·source 실패·두 번째 후보의 저장 전 소실·target 권한 거부,
initial/snapshot의 타입·순서·개수 불일치, 실제 HTTP의 다중 선택·기존/거부된 원문 escape·CSRF 선행 거부를 검사했다.
기존 FK와 새 collection 선언을 함께 포함한 단일 관계 테스트가 nullable empty 입력에 labels까지 요구한 최초 실패를
`choices-normal-1790426586126802000`에 남겼다. 해당 검증의 필드 선택을 명시하고 기존 single-choice 위험 검증을 유지했다.
Staged diff 검사에서 발견한 신규 migration의 마지막 빈 줄을 정리한 뒤 아래 최종 source로 모든 영향 모드를 다시 실행했다.

Helpdesk 실제 SQLite/PostgreSQL 소비자 안에서 0019↔0020을 두 번 왕복했다. 기존 Ticket/Label/TicketLabel 행,
retained link ID와 삭제한 intermediary ID 이후의 sequence 상한, 재시작 후 generated forward/reverse collection 읽기를 확인했다.
기존 scalar/관계 소비자와 Article의 영향을 함께 실행했다. 아래는 모두 같은 **2,171개 non-Markdown source map**
`3b4f8f862b8616f5621c88ead7f4961e45f6ebba62d34a245b6340320abe1590`에 대한 `./forms ./forms/model ./admin ./examples/helpdesk ./examples/article` 5개 package,
필수 root 114개와 하위 test의 상세 이벤트 검증이다. Go 1.26.5·macOS arm64의 영향 검증이며 전체 플랫폼 검증은 아니다.

| 모드 | run=PASS | skip | 시간 | 실행 디렉터리 |
| --- | ---: | ---: | ---: | --- |
| normal | 1373 | 0 | 6.9 s | `choices-normal-1790427182866923000` |
| race | 1373 | 0 | 37.6 s | `choices-race-1790427182866899000` |
| cgo0 | 1373 | 0 | 8.2 s | `choices-cgo0-1790427182870107000` |

서로 다른 owned PostgreSQL DB에서 실행했고 모두 active session/user table/custom schema **0|0|0** 확인 뒤 non-force drop했다.
Reference 전용 DB는 session/table **0|0** 확인 뒤 non-force drop했다. 모든 checkpoint의 source map이 실행 전후 같다.
원본을 바꾸지 않는 Go overlay로 **6개 의미 변경 control**을 각각 실제 assertion 실패로 탐지했다:
키 별칭 허용, Changed에서 중복 개수 무시, canonical 재검증에서 첫 키만 남김, 초기 선택 해제,
target permission 검사 생략, 0020 migration source 누락. Build failure·skip을 탐지 성공으로 세지 않았다.

`make format-check generate-check`는 4개 managed tree와 기존 checked-in relation generated test를 통과했다.
Helpdesk generated snapshot은 `188bc012f908008e77342603f094c24960057d7328316d2574cc0ae0118648bd`(12개 파일)다.
Affected go vet와 diff check도 통과했다. Generator 자체 변경·full generator/전체 platform 재실행으로 세지 않는다.
최종 문서 링크·정합성 검사와 source audit는 아래 receipts에 보존한다.

로컬 원본 evidence root는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/ticket-label-collections`이다.
`latest-reference-path`, `latest-choice-reference-tests-path`, `latest-choices-normal-path`, `latest-choices-race-path`,
`latest-choices-cgo0-path`, `latest-choices-negative-path`, `latest-choices-checks-path`, `latest-choices-audit-path`가
상세 source/request/receipt와 이벤트·stderr·control log를 가리킨다. Receipt는 원본 log SHA256을 보존한다.

현재 source의 Hosted full은 실행하지 않았다. 기존 [Hosted full](https://github.com/progresshans/godj/actions/runs/35689549739)은
`93e77bd9c19d6e7b137de3a068c40a403970e73d`의 기록이다. Ticket 저장 시 권한·양쪽 Category·전체 desired set을 같은
relation transaction에서 검증하는 소비자·API/OpenAPI/client와 동시성/실패/durability를 완성한 뒤 GDJ-0099 Hosted milestone을 실행한다.

## GDJ-0099 — 배치 model graph와 generated streaming

2026-09-26, `8820ffd6c9b1fb14c6f5ed46a231d754e0dd3d91` 위에서 공통 `MaterializeBatches`·eager/prefetch
`IterateBatches`와 generated plain/eager/prefetch `Iterate(ctx, size, callback)`를 연결했다. 한 배치의 source decode·
eager 검증·하위 prefetch·graph clone·generated wrapper를 완료한 뒤 모델을 전달한다. Source query의 순서·중복·slice를
보존하고 full evaluation cache를 우회한다. Callback의 불변 context가 같은 backend의 ORM query·CRUD·Save·relation atomic을
pinned executor로 전달하며 기존 model의 origin·pointer identity·assignment/cache와 root/borrowed 수명은 바꾸지 않는다.
실행 capability가 없으면 원래 root로 우회하지 않는다. 현재 executor가 소유하는 capability를 사용하므로 collection에 별도로
저장하던 session capability 사본도 제거했다. Facade ABI v19·golden·5개 managed generated tree를 함께 갱신했다.

기존 고정 Django 6.1 runner/51개 관찰은 변경하지 않았다. `prefetch_stream`의 배치 1/2/3/8·0/음수 거부·조기 중단·
callback 오류·nested filter·owner universe 1/2·transaction·warm cache의 **13개 실행 사례**를 양 DB의 실제 generated
소비자에서 결과와 논리 query 수까지 비교했다. Reference의 size 생략은 Go API에서 필수 인자이며 별도 runtime 사례로 세지 않았다.
Reverse OneToOne eager는 서로 다른 batch의 같은 owner에서 상충하는 child를 거부한다. Decoded graph는 현재 배치만
보관하지만 이 검사에 필요한 route/owner/child key ledger는 고유 owner 수에 비례한다. 상수 메모리라는 주장을 하지 않는다.

새 소비자의 SQLite/PostgreSQL **63개 run=PASS / skip 0** 상세 이벤트를 별도 generated module에 보존했다.
기존 model pointer를 관계에 할당하고 callback context로 lazy/collection 조회·Save·Create/Update/Delete·relation delete·Set을
실행했다. Foreign Using origin의 할당 거부, retained root 사용, source slice, typed/path·nested/eager·중복 model/cache 분리,
각 session의 capability·만료·outer rollback, source/child의 늦은 실패·취소·panic·재시도 시 full cache 상태, 중첩 stream과
동시 context를 확인했다. SQLite는 기본 단일 연결을 사용한다. PostgreSQL native transaction은 기존 RelationSession capability를
유지하고, 명시적으로 가린 ordinary wrapper/SQLite coordinated ordinary session은 빈 관계 변경도 거부한다.

통합 checkpoint의 non-Markdown source **2,161파일**, map hash
`a98ff593cd5fb2c3a5f358637efb44ff0db781e92eb9d551d6fba057433f808a`에서 `./orm ./codegen ./codegen/consumertest`
전체·`./conformance/onetoonefixture`와 PostgreSQL OneToOne eager 영향 회귀를 실행했다. 필수 root **378개**,
일반·race·CGO=0 각각 **5 package / 1,258 run=PASS / skip 0**, **212.7초·705.8초·191.9초**다.

마지막 검토에서 확장 backend의 nil/typed-nil row도 panic 대신 명시 오류로 거부하도록 보완했다. 이 변경은
`orm/materialize_stream.go`와 해당 테스트 두 파일뿐이다. 최종 non-Markdown source는 같은 **2,161파일**, map hash
`cf69da3c265000f34efc19c68fbfdb90f76bb5f0ef230b0ea9d95421e9970741`이다. 최종 source의 ORM 전체와 새 generated
streaming 소비자를 일반·race·CGO=0으로 실행해 각각 **2 package / 629 run=PASS / skip 0 / 필수 root 218개**,
**14.3초·41.4초·14.0초**를 확인했다. 앞선 5 package 실행을 이 최종 source 전체의 재실행으로 표현하지 않는다.
최종 감사에서 두 파일 이외의 모든 non-Markdown 파일이 통합 checkpoint와 같음을 대조했다.

모두 Go **1.26.5**, macOS arm64, PostgreSQL **17.5**에서 실행했고 각 실행 전후 source가 같았다.
소유 DB의 연결·table·schema 정리는 모두 **0|0|0**이며 force 없이 제거했다. 전체 ORM 지원 범위나 full-platform PASS로 확대하지 않는다.

초기 normal source `2f9f52e46643ad58bb510e943ee5c98b7a0d15d335e8cd96794b2d79f90e267e`의 실패도 보존했다.
취소 회귀 테스트는 세 번째 Err 호출에 의존해 대기 전에 취소되었고, 새 소비자는 heterogeneous reference 관찰 전체를 같은
map shape로 읽으려 했다. 취소 검사는 owner flight 종료 사건으로 연결하고 reference에서는 해당 관찰만 decode했다.
추가 session 검사 source `3c7a9c848a8fdafd88d1a4b05e4c856818a27939613357c60d2e5d11a7bac561`는 PostgreSQL의
기존 relation capability까지 금지하던 테스트 기대 때문에 실패했다. 원래 session이 광고한 capability의 보존을 검사하도록
수정했으며 backend의 권한을 바꾸거나 실패 테스트를 제거하지 않았다. 실패 실행의 source·원본 로그·정리 receipt를 유지한다.

최종 source의 의미 변경 overlay **6개**(배치 크기 축소, full cache 사용, 배치 간 cardinality 삭제, 실행 연결 scope 무시,
부족한 writer capability를 root로 우회, 다른 facade origin 허용)가 모두 지정한 assertion 실패로 탐지됐다.
Build 오류·skip·timeout은 negative control 성공으로 세지 않았다. gofmt·affected vet·문서 링크·diff·5개 tree generated drift를
확인했다. 이번 source의 Hosted 전체는 미실행이며 GDJ-0099 전체 milestone은 Ticket 소비자 통합 뒤에 수행한다.

원본 source map·초기 실패·세 환경 checkpoint·실제 generated child 이벤트·negative·정리·검사·게시 receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-materialized-stream/`의
`latest-normal-path`, `latest-race-path`, `latest-cgo0-path`, `latest-consumer-normal-path`, `latest-negative-path`,
`latest-checks-path`, 최종 보완의 `latest-delta-normal-path`·`latest-delta-race-path`·`latest-delta-cgo0-path`와
최종 audit/publication을 따른다. 다른 source의 Hosted 전체 결과를 전이하지 않는다.

## GDJ-0099 — root 배치 조회의 연결과 transaction 종료 소유권

2026-09-26, `8a1f722a7514bd821c6bf23676f92b8c3f39dbd0` 위에서 root `BatchQueryer`와 callback의 pinned executor를
양 DB에 연결했다. 제공된 executor로 중첩 query/stream·CRUD/conflict insert·ordinary/relation/coordinated atomic을 실행한다.
Root executor는 SessionValidator를 광고하지 않고 stream 종료 뒤 원래 backend로 복귀한다. Borrowed session의 만료와 구분한다.
이것은 native 실행 기반이다. Model graph·facade origin과 실행 backend 분리·generated iterator는 아직 미연결이며
configured raw Iterate의 명시 오류를 유지한다. 기존 51개 Django 관찰은 그대로이고 이 묶음에서는 reference runner나 생성물을 바꾸지 않았다.

SQLite는 raw admission을 얻은 뒤 연결을 고정한다. 같은 backend의 다른 raw writer가 기다리는 중에도 callback의
scoped 작업이 같은 admission/connection으로 완료되고, stream 정리 뒤 기다리던 writer가 한 번 실행되는 것을 확인했다.
Shared lease는 callback의 직접 rows를 다음 batch 전에 닫고 iterator의 source rows는 보존한다. Raw discard 전에 열린 rows를
정리하여 database/sql의 pinned Conn close 대기를 해제한다. Unconfirmed rollback·discard 주입에서는 stream이 끝나도
retained 연결은 pool에 돌아가지 않고 quarantine에 남는다. Backend.Close의 pool 봉인·close 1회 뒤 file DB를 재개방해
미완료 write가 남지 않음을 확인했다. 이 경로를 confirmed rollback으로 오분류하지 않는다.

PostgreSQL root source는 WITH HOLD cursor다. Source/FETCH/child rows의 소유권을 나누고 원본 query를 반복 실행하지 않는다.
처음에는 pinned Conn의 sql.Tx를 재사용했지만 parent 취소 뒤의 조회가 중간에 `conn closed`로 실패했다.
초기 normal source `8e1c56b16da75f3bc506fc3d851fac46535157a712865c28de47621a6dea78a8`와 실패 로그를 보존했다.
현재 Go의 sql.Tx 취소 rollback 경로를 대조해, pinned 범위에서는 BEGIN/COMMIT/ROLLBACK을 동기 소유하도록 정리했다.
기존 transactionSession의 lifetime·query/write·오류 분류는 공유하고 root pool의 기존 sql.Tx 동작은 유지한다.
분리된 read context에서도 parent 취소를 관찰하며 rollback 뒤 root source와 연결을 다시 사용할 수 있다.
실제 deferred FK 때문에 literal COMMIT이 실패하면 commit outcome unknown을 유지한다. Callback이 오류를 잡아도
폐기한 source를 성공 처리하거나 새 연결에서 재시도하지 않는다. 새 backend PID와 저장 상태를 직접 확인했다.

최종 non-Markdown source **2,155파일**, map hash
`3df1eeee02f80d27e6dedf51a8ddf7297e7f259056d3b1a649f8480afc7b9262`에서
`./db/internal/streamconn ./db/internal/batchread`와 SQLite/PostgreSQL의 batch·transaction·conflict insert·coordinated relation
영향 회귀 **63개 필수 root**를 실행했다. 일반·race·CGO=0 각각 **4 package / 530 run=PASS / skip 0**,
**2.4초·22.2초·2.6초**다. Go **1.26.5**, macOS arm64, PostgreSQL **17.5**에서 전후 source가 같고
각 실행의 연결·table·schema 정리는 **0|0|0**, 소유 DB는 force 없이 제거했다. 전체 DB package/full-platform PASS를 뜻하지 않는다.

양 backend의 root fixture는 pool을 연결 하나로 제한했다. 네 종류의 atomic에서 기존 batch 경계·nested query/stream·slice·
빈/aggregate 결과·callback 오류/panic/Goexit·취소·만료·commit/rollback을 반복 검증했다. Root 자체의 scan/yield 오류·panic·Goexit·
취소와 callback이 남긴 rows 정리, retained executor의 read/write/새 stream, iterator 실패 뒤 이미 완료한 root write 보존도 확인했다.
Lease 동시 종료와 물리 discard·보존의 경계를 검증하고 실제 PostgreSQL root/child **87개**를 Hosted 필수 inventory에 추가했다.

같은 최종 source의 의미 변경 overlay **6개**(root를 만료 session으로 취급, WITH HOLD 제거, unknown commit 정리 제거,
retained lease 조기 반환, callback rows 경계 제거, root에 borrowed session capability 부여)는 지정한 assertion 실패로 모두 탐지했다.
Build 실패·skip·timeout을 성공적인 negative control로 세지 않았다. Negative DB의 정리도 **0|0|0**이고 원본 source는 같다.
gofmt·affected vet·문서 링크·diff·CI package/inventory 검사를 수행했다. Generator·generated ABI와 reference 내용은 변경하지 않아
이 단계에서 drift/전체 consumer/reference suite를 반복 실행하지 않았다.

원본 실행·source map·초기 실패·mutation·cleanup receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-root-stream/`의
`latest-normal-path`, `latest-race-path`, `latest-cgo0-path`, `latest-negative-path`, `latest-checks-path`를 따른다.
최종 source 대조·게시·fast CI receipt도 같은 디렉터리에 둔다. 이 source의 Hosted 전체는 아직 실행하지 않았다.
GDJ-0099 전체 milestone은 model materialization과 Ticket 소비자 통합 뒤에 수행하며 기존 `93e77bd9…`의 전체 결과를 전이하지 않는다.

## GDJ-0099 — transaction session의 배치 실행 기반

2026-09-26, `8a658682fb632df4a5e581bb4b61119c231a3298` 위에서 `db.BatchQueryer`를 ordinary/relation/coordinated
transaction session에 연결했다. 한 source query를 유지하고 row decode와 완성 batch의 yield를 구분한다.
SQLite는 같은 연결을, PostgreSQL은 `NO SCROLL CURSOR WITHOUT HOLD`와 bounded FETCH를 사용한다.
FETCH의 scan·row error·close가 끝난 뒤 같은 session에서 하위 조회와 광고한 write capability를 사용할 수 있다.
Root backend의 연결 소유권·model graph·generated iterator는 아직 미연결이며 configured raw Iterate의 명시 오류를 유지한다.

사전 실제 연결 probe에서 root SQLite의 열린 rowset 중 두 번째 조회는 약 302ms 뒤 context deadline으로 실패했다.
SQLite transaction 안에서는 같은 조회가 성공했다. PostgreSQL root pool은 다른 연결로 성공했지만 borrowed transaction은
`conn busy`, 후속 연결 오류와 commit outcome unknown을 반환했다. 이 probe는 transaction 안에서 쓰기를 하지 않았고
별도 DB의 연결 0을 확인해 제거했다. 이 관찰을 root streaming 구현 완료나 프레임워크 전체 결함으로 확대하지 않는다.

고정 Django **6.1**, Python **3.14.3**, asgiref **3.12.1**, sqlparse **0.5.5**, psycopg **3.3.6**의
SQLite **3.50.4**·PostgreSQL **170005**, seed **0/813**의 네 실행이 같다. 기존 **50개 관찰·Django source hash**를
보존하고 chunk 1/2/3/8·invalid size·조기 중단·callback 오류·nested/filtered·owner universe·transaction·warm cache의
**14개 streaming 사례**를 추가해 총 **51개 관찰**이다. Runner hash는
`5d1f7c11b4b939774f24d70c11f6f8236958d847a7d6245e7ad059ca043e2277`이다.
독립 Python 검사 **17 PASS / skip 0**이며 chunk 크기 변경·iterator를 전체 cache 평가로 대체하는 의미 변경도 탐지했다.
배치 경계에 따른 owner scope와 full cache 보존은 reference 증거이며 아직 Go materialized graph의 PASS로 세지 않는다.

최종 non-Markdown source **2,148파일**, map hash
`0e42527573a4cbe43610b01aa4c14dfdfcc294ff41a10a49c2f7d2e2e9168614`에서
`./db/internal/batchread`와 SQLite/PostgreSQL의 새 배치·기존 ordinary/relation/coordinated transaction 회귀 **47개 필수 root**를 실행했다.
일반·race·CGO=0 각각 **3 package / 253 run=PASS / skip 0**, **3.1초·8.0초·2.3초**다.
Go **1.26.5**, macOS arm64, PostgreSQL **17.5**에서 전후 source가 같고 각 실행의 연결·table·schema 정리는 **0|0|0**이며
소유 DB를 force 없이 제거했다. 전체 DB package나 전체 platform 검증으로 확대하지 않는다.

양 DB의 네 session 종류에서 실제 nested query/stream·순서·중복·slice·빈 결과와 empty aggregate·callback 중단/오류/panic·
session 만료·분리한 read context에서도 parent 취소·outer commit/rollback·Goexit rollback을 확인했다.
PostgreSQL은 source 변경 뒤에도 원래 cursor membership을 유지하고 NUMERIC·INTERVAL·UUID·JSONB·SQL NULL과 query parameter를 보존한다.
Native DECLARE/FETCH/row/row-close/cursor-close 오류·callback+cleanup 오류·commit unknown의 보존과 재시도 부재도 확인했다.
현재 실제 PostgreSQL root/child **75개**를 Hosted 필수 실행 inventory에 추가했으며 새 source의 Hosted 전체 실행은 아직 하지 않았다.

초기 normal 실패(source `818a40e8d01990c52112c1b6d5caa7f6cc4f3b5683a003dbe1c4ad7c4cba301b`)에서
coordinated ordinary wrapper의 capability 연결 누락을 보완했다. 새 테스트의 explicit generated-key 삽입·Duration의 `any` 반환 기대도
기존 지원 계약에 맞췄다. 이때 callback Goexit가 raw SQLite transaction과 pinned connection을 남기는 기존 정리 누락을 재현했다.
Panic만 recover하던 defer를 모든 비정상 종료에 적용하고 원래 panic/Goexit를 그대로 전파한다. 실제 rollback·session 만료·재사용과
rollback/discard 모두 미확정인 경우의 retention/quarantine·최종 close 1회를 검증했다. 기존 uncertain-outcome 규칙은 유지한다.

최종 source의 의미 변경 overlay **7개**(다음 batch 선행 읽기, yield 오류 유실, scalar adapter 제거, cursor 정리 제거,
FETCH close 오류 유실, coordinated affinity 변경, Goexit 정리 제거)는 모두 지정한 제품 assertion 실패로 탐지했다.
Build 실패·skip·timeout을 성공적인 negative control로 세지 않았고 원본 source는 바뀌지 않았다.
gofmt·affected go vet·diff·문서 링크 **144개 문서**·CI package/inventory Python 검사 **12개**를 통과했다.
Generator·generated ABI를 바꾸지 않아 이 묶음에서는 generated drift와 전체 consumer suite를 다시 실행하지 않았다.

원본 실행·source map·실패·mutation·cleanup receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-stream/`의
`latest-probe-path`, `latest-connection-probe-path`, `latest-oracle-tests-path`, `latest-normal-path`,
`latest-race-path`, `latest-cgo0-path`, `latest-negative-path`, `latest-checks-path`를 따른다.
최종 source 대조와 PR/fast CI 게시 영수증은 같은 디렉터리에 둔다.
GDJ-0099의 Hosted 전체 milestone은 Ticket 소비자 통합 뒤 실행한다. 기존 `93e77bd9…`의 전체 PASS를 이번 source로 전이하지 않는다.

## GDJ-0099 — custom single target query와 조회 부재

2026-09-26, `887c8ee27623b03634a3838a52e7c858d934a0f3` 위에서 required/nullable FK·역방향 OneToOne의
target Filter·OrderBy·Distinct·SelectRelated를 native/generated SinglePrefetch에 연결했다. Custom query의 child와
별도로 추가한 typed/path child를 구분한다. 이미 eager로 읽은 대상은 custom query와 그 안의 child를 건너뛰며 명시적
하위 경로는 유지한다. 같은 target의 join 중복은 첫 행으로 처리하고 서로 다른 target·batch 밖 row·clone의 key 변경은 거부한다.
Parent 조회·target eager·child graph가 모두 성공해야 결과를 공개한다. Facade v18·relation object v6·golden과 5개 프로젝트 생성물을 갱신했다.

필터로 제외된 required target 때문에 부모 목록 전체가 실패하던 generated materialization 경로를 고쳤다.
부모 목록은 반환하고 required 접근은 `related_object_missing`, nullable/reverse 접근은 기존 bool 부재를 반환한다.
읽기 결과인 `RelationLoadedAbsent`를 해제 할당과 구분한다. 같은 FK의 파생 모델·Save에도 cache와 저장 FK를 보존하고,
실제 FK 변경은 cache를 초기화한다. Low-level object의 Fresh는 기본 관계를 다시 읽으며 기존 부재 snapshot을 바꾸지 않는다.

고정 Django **6.1**, Python **3.14.3**, asgiref **3.12.1**, sqlparse **0.5.5**, psycopg **3.3.6**에서 새 관찰을 실행했다.
SQLite **3.50.4**와 PostgreSQL **170005**, hash seed **0/813**의 결과가 모두 같고 기존 **49개 관찰·Django source hash**는 같다.
Required/nullable/reverse filter·eager target 재사용·명시적 child·join 중복·target eager·일반/named single slice 오류의
**9개 사례**를 추가한 **50개 관찰**이다. Runner hash는
`ad9c524432d7a15a0c9e88126388f3b07bddb8274e4a9174677917d5418c9ed4`이며 양 DB capture와 현재 fixture의 JSON 내용이 같다.
독립 Python 검사 **15 PASS / skip 0**이고 filter 제거·eager 제거의 의미 변경 control도 실제 실행했다. 이를 Go 제품 PASS로 세지 않는다.

최종 제품 source는 non-Markdown **2,139파일**, map hash
`5dfb77b9fcdd771bd2c9fe95a2c903540349f152c6328bf042f8919f637139cb`다.
범위는 `./orm ./codegen ./codegen/consumertest ./conformance/onetoonefixture` 전체와
PostgreSQL의 `TestPostgresOneToOneFacadeReverseEager`다. 일반·race·CGO=0 각각
**5 package / 1,247 run=PASS / skip 0**, **178.4초·622.8초·168.8초**다.
각 mode의 compile된 **373개 필수 root**·package terminal·실행 전후 동일 source map을 확인했다.
PostgreSQL package 전체나 full-platform 결과로 표현하지 않는다.

새 generated 소비자는 각 DB에서 **15개 필수 사례**의 실행 완료를 요구한다. 독립 결과·query 수와 함께 invalid predicate·
foreign origin·eager node budget·lookup 재정의·Count/First/파생 query·중복 owner의 독립 cache·실패/취소 재시도·
foreign row와 서로 다른 단일 target·동시 warm 조회·session 종료·실제 저장 FK 보존·low-level missing/Fresh를 확인했다.
첫 일반 checkpoint(source `d10ebf46…`)에서 required 부재를 목록 전체 오류로 처리하는 결함이 실제 소비자에서 발견되었다.
부모 harness의 진단은 일반적인 build 실패로 축약되어, 같은 generated module을 별도로 실행해 실제 runtime 오류를 확인했다.
첫 실패·진단 로그를 보존했고 제품의 부재 처리와 cache 의미를 수정한 뒤 위 최종 source로 전체 영향 범위를 다시 실행했다.

최종 source의 원본 SQLite 소비자 **17 run=PASS / skip 0** 뒤 target filter 제거·eager 부모에 custom child 실행·
서로 다른 단일 target 허용·target eager 제거·child 실패 무시·읽기 부재를 해제 할당으로 교체하는 **6개 overlay**를 실행했다.
모두 정상 compile 후 지정한 의미 검증에서 실패했으며 원본 source는 바뀌지 않았다.
영향 vet·format·문서 링크/diff·5개 프로젝트 generated drift도 통과했다.

환경은 Go **1.26.5**, Darwin arm64, modernc SQLite, PostgreSQL **17.5 Homebrew**다.
모든 완료 checkpoint는 `GODJ_REQUIRE_POSTGRES=1`·mode별 전용 database를 사용했고 cleanup **0|0|0** 뒤 force 없이 제거했다.
독립 reference 전용 DB도 connection/table **0|0** 뒤 제거했다.
로그·source map·inventory·receipt·negative overlay는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-custom-single/`의
`latest-probe-path`, `latest-oracle-tests-path`, `latest-normal-path`, `latest-race-path`, `latest-cgo0-path`,
`latest-diagnostic-path`, `latest-negative-path`, `latest-checks-path`가 가리키는 artifact를 따른다.

Prefetch materialized streaming과 Ticket 컬렉션 Form/Admin/API/OpenAPI/client의 권한·CSRF·Category·동시성·durability,
GDJ-0099 Hosted 전체 milestone은 남아 있다. 기록된 최근 전체 검증은
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`다.

## GDJ-0099 — named collection snapshot과 전체 owner 집합

2026-09-26, `6e31229a2a6289dae285f8f985d9aca18754d332` 위에서 ManyToMany·reverse FK collection의
`Snapshot`·`Limit`·`Offset`·`Read`를 native runtime과 generated facade에 연결했다. 별도 이름에 보관한 결과는
일반 manager를 채우거나 제한하지 않는다. Read는 I/O 없이 새 model·graph·cache handle을 반환하고 미선택과 빈 목록을
구분한다. 관계·binding·origin·owner PK·session 수명을 검증하고 이름 충돌·lookup 재정의·부분 publication을 거부한다.
동일 owner의 With/Clear 파생 model과 Save에도 immutable snapshot을 보존한다. Facade ABI v17·golden과
5개 프로젝트 생성물을 갱신했다.

Custom ManyToMany는 전체 owner 집합을 한 query에서 유지한다. 첫 join의 grouping owner와 마지막 join의 membership
owner가 다른 경우 기존 999개 분할로 행·순번이 달라지는 문제를 실제 generated 소비자의 **1,003 owner**로 검증했다.
큰 integer membership은 SQLite `json_each`·PostgreSQL `bigint[]`의 단일 parameter로 전달한다. 양 DB에서
**40,004 owner key**·int64 양 끝값·0·중복·NULL·부정 조건과 작은 scalar 목록의 동등 결과를 확인했다.

검증 source는 모두 non-Markdown **2,137파일**이다. 첫 source map
`817bfff21a66677ecd06ecd990d97f1535faea4af5f27aa189131cf1b7c0d2a0`에서
query·queryplan·ORM·codegen·generated 소비자 전체와 SQLite/PostgreSQL SELECT·관계·eager·scalar 영향 root를 실행했다.
일반 **7 package / 5,528 run=PASS / skip 0**, **166.3초**, 실제 compile된 필수 root **604개**다.
첫 inventory script가 Go raw string 안의 예제 test 선언을 root로 잘못 수집해 판정을 실패로 남겼다.
실행 결과와 원본 receipt를 보존하고 같은 source에서 `go test -list`로 실제 root를 확인한 `inventory-audit.json`에
수정 판정을 기록했다. 실제 실패·skip·package 종료 누락은 없다.

고정 Django runner의 외부 변형으로 일반 M2M·reverse manager의 `[0:]`가 오류 없이 **1 query**임을 추가 확인했다.
Go의 Offset(0)도 unsliced로 처리한다. 체크인 runner·49개 관찰 fixture는 이번에 변경하지 않았다.
이 변경 뒤 source map은 `40d29574b18a82d12c15b01e333422348f418c114763ca09bc90d86ab40fb45f`다.
차이는 snapshot 소비자·runtime 검증·query window·query test의 4파일이며 생성기와 생성물은 같다.
해당 영향 일반 검증은 **6 package / 5,059 run=PASS / skip 0**, **22.5초**, 필수 root **456개**다.
넓은 race·CGO=0 checkpoint는 각각 **7 package / 5,529 run=PASS / skip 0**, **663.7초·184.8초**,
필수 root **605개**다. 실제 generated ManyToMany module은 각 완료 mode에서 양 DB **325 child run=PASS / skip 0**다.
독립 slice 관찰과 결과·query 수를 비교하고 nested/eager·중복 owner·다중 alias·manager 변경·동시 warm Read·실패/취소 재시도·
session 종료·native Read와 generated API를 확인했다. DB package 전체나 full-platform 실행으로 세지 않는다.

마지막 model 파생 경로에서 private snapshot graph를 전달하도록 보완했다. 최종 source map은
`d9ba26f81faa8ce739bee48eaa0a3583cec07a20da5c2a63cc49ea586f86056d`다.
직전 source와 차이는 생성기 1·composition test 2·golden 1·manifest/facade 8의 **12파일**이며 runtime/query/backend는 같다.
최종 source에서 codegen 전체와 facade·prefetch·ManyToMany·reverse·SelectRelated의 실제 generated 소비자를 실행했다.
일반·race·CGO=0 각각 **2 package / 384 run=PASS / skip 0**, **49.8초·147.1초·47.0초**, 필수 root **105개**다.
Clear/With 파생·Save의 snapshot 보존과 반환 target의 독립성을 양 DB에서 확인했다. 모든 완료 checkpoint는 실행 전후
source map이 같고 필수 root·package terminal을 검사했다. 앞 source의 넓은 검증을 최종 source의 전체 재실행으로 표시하지 않는다.

원본 snapshot 소비자 **16 PASS**·integer 정밀도 **2 PASS** 뒤 manager cache로 대체·owner 집합 분할·named child graph 제거·
child 실패 후 publication·integer float 변환의 **5개 의미 변경 overlay**를 정상 compile 후 지정한 실패로 모두 탐지했다.
최종 파생 소비자 **3 PASS** 뒤 derived graph 전달만 제거한 overlay도 지정한 회귀에서 실패했다.
앞 5개는 `40d29574` source 범위, 파생 control은 최종 source 범위이며 원본 제품은 변경하지 않았다.
초기 generated overlay가 `/var` symlink와 Go의 `/private/var` 경로 차이로 적용되지 않은 것을 탐지했고 realpath로 수정했다.
최초 파생 fixture는 지원하지 않는 nil-wrapper setter를 사용해 실패했다. 공개 Clear/WithID API로 고쳤으며 제품 검사를 완화하지 않았다.
두 초기 실패와 수정 후 로그를 보존했다.

최종 파생 checkpoint의 첫 재실행은 빌드 중 disk full로 실패했다. 진행 중인 Go 작업이 없음을 확인하고 재생성 가능한
Go build cache **92GiB**를 `go clean -cache`로 정리한 뒤 같은 source에서 세 mode를 순서대로 실행했다.
부분 삭제로 invalid 상태였던 전용 DB는 활성 connection **0**을 확인하고 force 없이 제거했다. 이 DB를 정상 cleanup
검사 통과로 세지 않으며 별도 `disk-recovery.json`에 기록했다. 성공한 모든 DB checkpoint는 `GODJ_REQUIRE_POSTGRES=1`과
전용 database를 사용했고 cleanup **0|0|0** 뒤 force 없이 제거했다. 마지막 catalog 확인에서 남은 전용 DB는 없다.

환경은 Go **1.26.5**, Darwin arm64, modernc SQLite, PostgreSQL **17.5 Homebrew**다.
Runtime/query/backend 영향 vet와 최종 generator/소비자 vet, format·5개 프로젝트 generated drift를 통과했다.
로그·source map·inventory·receipt·negative overlay·4/12파일 delta는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-snapshots/`의
`latest-normal-path`, `latest-delta-normal-path`, `latest-race-path`, `latest-cgo0-path`, `latest-derivation-*-path`,
`latest-negative-path`, `latest-derivation-negative-path`, `latest-zero-offset-path`, `latest-checks-path`,
`latest-final-checks-path`를 따른다.

Custom single target query·materialized streaming과 Ticket 컬렉션 Form/Admin/API/OpenAPI/client의 권한·CSRF·Category·
동시성·durability, GDJ-0099 Hosted 전체 milestone은 남아 있다. 기록된 최근 전체 검증은
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`다.

## GDJ-0099 — owner별 slice의 독립 기준과 조회 계획

2026-09-26, `33ebc19ea32dde54d36339814e4c00e490e34939` 위에서 owner별 slice의 Query AST와
SQLite/PostgreSQL window compiler를 연결했다. `ForPrefetchOwners`는 마지막 일치 join의 membership owner별로 순번을
계산하며 첫 join의 grouping owner가 요청 범위 밖인지 필요한 경우 바깥 query에서 검사한다. `ForPrefetchForeignKey`는
같은 계획을 역방향 collection target의 integer FK에 적용한다. 기존 model·eager·owner cell의 scan 순서를 유지한다.
이 checkpoint는 조회 계획·compiler 범위이며 named snapshot의 runtime/generated API 완료를 뜻하지 않는다.

고정 Django **6.1**, Python **3.14.3**, asgiref **3.12.1**, sqlparse **0.5.5**, psycopg **3.3.6**에서 독립 runner를 실행했다.
SQLite **3.50.4**와 PostgreSQL **170005** 각각 hash seed **0/813** 결과가 같고 양 DB의 observation도 일치한다.
이전 **48개 관찰과 Django source hash**는 모두 같으며 `prefetch_slices` 14개 세부 사례를 포함한 **49개 관찰**을 확보했다.
Head·middle·tail·empty·범위 밖·내림차순, 중복 owner·empty batch, 하위 prefetch·reverse eager, 비고유 through와 DISTINCT,
일반 manager의 sliced query 오류, grouping/membership join이 다른 경우를 포함한다. 별도 snapshot을 읽어도 일반 manager는
기본 집합을 조회한다. Independent Python 검사 **13 PASS / skip 0**이며 offset·window partition을 바꾸는 실제 의미 변경
negative control 2개를 기존 control에 추가했다. Reference는 Go 제품 실행 증거로 세지 않는다.

제품 source는 non-Markdown **2,133파일**, map hash
`795c65bd39ce729019dfaf6c3ef360623a07eb441e82b0993ee4e2247ce418e2`다.
`./query ./db/internal/queryplan` 전체, SQLite/PostgreSQL의 SELECT·ordering·scalar/Boolean/JSON·관계·eager 관련 root,
`TestGeneratedManyToManyCollections`와 `TestGeneratedFilteredPrefetchComposition`을 묶어 검사했다.
일반·race·CGO=0 각각 **5 package / 4,443 run=PASS / skip 0**, **19.6초·89.9초·24.2초**다.
각 mode의 **243개 필수 root**, package/terminal 이벤트, 실행 전후 동일 source map을 확인했다.
양 DB query·기존 generated 소비자의 영향 검증이며 DB package 전체나 full-platform 실행으로 세지 않는다.

실제 DB 결과를 독립 slice 관찰과 비교하고 reverse eager의 scan 폭·owner 0·정렬 없는 window·큰 limit/offset·JSON sort key와
parameter 순서·DISTINCT를 확인했다. Empty slice/membership는 검증 뒤 I/O를 생략하며 잘못된 empty plan과 취소는 connection에
도달하지 않는다. 원본 SQLite **15 run=PASS** 후 전역 순번으로 변경·offset 제거·owner 필터를 window 앞으로 이동·바깥 DISTINCT
추가의 **4개 compiler/AST overlay**가 모두 정상 compile 뒤 지정한 의미 검증에서 실패했다. 저장소 원본은 변경하지 않았다.

Go **1.26.5**, Darwin arm64, modernc SQLite, PostgreSQL **17.5 Homebrew** 환경이다.
각 checkpoint는 `GODJ_REQUIRE_POSTGRES=1`과 별도 전용 DB를 사용했고 cleanup은 **0|0|0**, force 없이 제거했다.
독립 Django 전용 DB도 남은 connection/table **0|0**을 확인한 뒤 제거했다. 영향 vet·format·문서 링크·diff와
프로젝트 5개의 generated drift를 확인했다. 생성기·Facade ABI·체크인 generated Go 파일은 이번에 변경하지 않았다.
로그·source·필수 inventory·receipt·negative overlay는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-slices/`의
`latest-reference-path`, `latest-oracle-tests-path`, `latest-normal-path`, `latest-race-path`, `latest-cgo0-path`,
`latest-negative-path`, `latest-checks-path`를 따른다.

Named snapshot의 runtime/generated 접근자와 여러 owner batch의 grouping/membership 통합, custom single target query,
materialized streaming, Ticket 컬렉션 Form/Admin/API/OpenAPI/client와 권한·CSRF·양쪽 Category·동시성·durability는 남아 있다.
GDJ-0099 Hosted 전체 milestone은 소비자 통합 뒤 실행한다. 현재 source의 full Hosted PASS로 이전 결과를 옮기지 않는다.

## GDJ-0099 — reverse collection과 target eager 구성

2026-09-26, `4ec3595d74c9128f091d40b3130ed20936d4ee0b` 위에서 reverse FK collection을 공통 prefetch graph와
generated model 접근자에 연결했다. ReverseCollectionPrefetch와 ManyPrefetch의 SelectRelated는 같은 target SQL row에서
eager target을 읽는다. 모든 target rowset을 닫은 뒤 하위 prefetch를 전체 batch에 적용하고, 전체 성공 뒤에만 graph를 공개한다.
명시한 eager/child 설정은 held query의 파생 조회·추가 eager/prefetch·First/At에 유지된다. 역방향 manager Fresh/Invalidate는
기본 owner scope·정렬로 돌아가며 held query와 다른 materialization은 보존한다. Facade ABI v16·golden·프로젝트 5개의 생성물을 갱신했다.

제품 checkpoint는 non-Markdown **2,128파일**, map hash
`eea1ddf815cdf01b429b9edff1a48713c4998dbc73d7891e27d59932182b5b65`다.
`./orm ./codegen ./codegen/consumertest ./conformance/onetoonefixture` 전체와 `./db/postgres`의
`TestPostgresOneToOneFacadeReverseEager`에서 일반·race·CGO=0 각각 **5 package / 1,244 run=PASS / skip 0**다.
각각 206.1초·640.3초·205.3초이며 26개 필수 root·완료 이벤트와 실행 전후 동일 source map을 확인했다.
Actual generated ManyToMany module의 양 DB **292 child run=PASS / skip 0**를 mode별 parent가 검사했다.
PostgreSQL 패키지 전체나 전체 platform 실행으로 세지 않는다.

마지막 정적 검사에서 기존 RelatedSet 복사 거부 테스트가 새 inline mutex의 복사까지 감지했다. 각 handle이 별도 mutex 포인터를
소유하도록 바꿨으며 기존 nil/zero/copy 거부를 유지한다. Query snapshot과 Invalidate의 교체를 같은 mutex로 보호한다.
추가 검토에서 native At의 target eager·owner scope·큰 index row-drain·오류 재시도·취소 시 no-I/O를 별도 SQLite probe로 확인하고
정식 generated 소비자에 넣어 양 DB에서 재검증했다.
최종 source는 **2,128파일**, map hash
`0f61a71e89e1353c9f2879fcc0daaf8674c588d028429423e8a5fa46293559c6`다.
위 checkpoint와 차이는 `orm/reverse_object.go`와 관련 generated 소비자 테스트 2파일뿐이며 생성기·생성물은 같다.
최종 source의 ORM 전체·ManyToMany/filtered composition 소비자·기존 reverse/current/no-edge 및 prefetch companion 소비자를 검사했다.
일반·race·CGO=0 각각 **2 package / 620 run=PASS / skip 0**, 32.6초·82.5초·35.7초다.
15개 필수 root와 source 불변을 확인했으며 actual ManyToMany 소비자는 각 mode에서 **294 child run=PASS / skip 0**다.
파일별 delta는 `latest-delta-*-path`의 `delta.json`에 보관했다. 넓은 checkpoint를 최종 source에서 재실행했다고 표시하지 않는다.

고정 Django `prefetch_eager_child`의 결과와 **source+target eager 2 query / warm 0**을 typed selector와 implicit child path 병합으로 비교한다.
같은 관계를 일반 single child prefetch로 읽으면 **3 query**다. ManyToMany target eager와 하위 collection의 **3 query**, 파생 target/child의
**2 query**, 추가 eager와 추가 prefetch에도 원래 eager 설정이 남는지 확인한다. 정상 부재·query filter/order/Distinct 구성,
lookup 재정의·origin/type·공유 node 한도, target 실패/foreign owner/취소 후 재시도, manager 초기화·held snapshot,
동시 warm 소비·session 종료와 reverse owner **1,000개/중복 포함 1,002개**의 두 batch·부분 publication 거부를 포함한다.

최종 원본 SQLite 생성 소비자 **18 run=PASS** 뒤 target eager 설정 제거·reverse foreign owner 허용·manager filter 잔존·lookup 재정의 허용·
ManyToMany target 설정 제거의 **5개 runtime overlay**를 정상 compile 후 모두 탐지했다.
초기 넓은 실행에서는 실제 project bundle 소비자는 통과했으나 예전 standalone facade helper가 reverse companion을 누락해 6개 root가 compile 실패했다.
실제 facade 의존성을 helper와 prerequisite 보존 검사에 추가했다. 고정 11/12개 파일 수 대신 필요한 companion의 존재·중복 거부와 실제 compile·
public error cause·stale binding·alias·COW 검증을 유지하도록 테스트를 정리했다. 초기 compile 실패와 vet 실패 로그도 보관한다.

Go **1.26.5**, Darwin arm64, modernc SQLite, PostgreSQL **17.5 Homebrew** 환경이다.
양 DB checkpoint는 `GODJ_REQUIRE_POSTGRES=1`·실행별 전용 database를 사용했고 cleanup은 **0|0|0**, force 없이 제거했다.
최종 영향 vet·format·문서 링크·diff와 프로젝트 5개의 generated drift가 모두 통과했다.
고정 Django runner·48개 ManyToMany 관찰 fixture는 변경/재실행하지 않았다.
로그·source·필수 inventory·receipt·delta·negative overlay는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-reverse/`의
`latest-final-*-path`, `latest-delta-*-path`, `latest-negative-path`, `latest-indexed-probe-path`, `latest-checks-path`를 따른다.

Owner별 slice·custom single target query·materialized streaming과 Ticket 컬렉션 Form/Admin/API/OpenAPI/client,
권한·CSRF·양쪽 Category·동시성·durability는 남아 있다. Reverse FK 변경 API나 전체 framework 완료로 세지 않는다.
GDJ-0099 Hosted 전체 milestone도 아직이며 최근 전체 검증은
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`다.

## GDJ-0099 — 단일 관계 prefetch와 eager 부모 재사용

2026-09-26, `6eba1f1ced9f908a08c43fb52cb65b65952d2588` 위에서 required/nullable FK와 역방향 OneToOne의
`SinglePrefetch`를 공통 graph에 연결했다. Generated typed/path selector와 root eager를 양쪽 호출 순서로 조합한다.
이미 읽은 부모는 projection·binding·membership을 검증해 재사용하고, 나머지는 고유 key를 999개씩 나누어 조회한다.
Eager descendants와 collection descendants는 전체 성공 뒤 함께 publish한다. Collection만 가진 target도 같은 materialization을
사용하며 반환 부모·collection handle은 서로 독립이다. NULL 관계도 session 수명을 보존한다. Facade ABI v15·golden·프로젝트 5개의 생성물을 갱신했다.

제품 checkpoint는 non-Markdown **2,123파일**, source map hash
`24bdccff029748914879356ef342e0da2e12254f81d454a14bfee785a584ca0d`다.
범위는 `./orm ./codegen ./codegen/consumertest ./conformance/onetoonefixture` 전체와
`./db/postgres`의 `TestPostgresOneToOneFacadeReverseEager`다. PostgreSQL 패키지 전체나 전체 platform 실행으로 세지 않는다.
일반·race·CGO=0 각각 **5 package / 1,237 run=PASS / skip 0**이며 실행 시간은 각각
176.6초·577.9초·171.1초다. 24개 필수 root와 시작/종료 이벤트를 검사했고 모든 완료 실행 전후 source map이 같다.

Actual generated ManyToMany module은 mode마다 SQLite/PostgreSQL **273 child run=PASS / skip 0**다.
고정 Django `prefetch_eager_owner`의 결과와 **eager+child 2 query / warm 0**을 typed/path와 양쪽 호출 순서에서 비교했다.
같은 graph의 일반 single prefetch는 **3 query**다. Cold First의 제한·full cache 독립, Count의 child I/O 생략,
파생 Filter/Offset/Limit/Fresh, origin·공유 node budget, 단계별 실패와 취소 후 재시도, 중복 부모·held query의 독립 cache와
동시 warm 소비·session 종료를 검사한다. 별도 composition 소비자는 collection→single 혼합 경로와 명시한 single child 설정의
파생 조회 보존을 양 DB에서 검사한다. 기존 OneToOne 12개 기준 graph에도 일반 single prefetch와 모든 eager 부모 재사용을
적용해 같은 결과·정상 부재·하위 cache를 양 DB에서 확인했다. Runner와 고정 48개 ManyToMany 관찰 fixture는 변경/재실행하지 않았다.

결함 주입 점검에서 native foreign-row 테스트가 batch membership 거부 뒤의 중복 행 오류로도 통과할 수 있음을 발견했다.
정확한 `RelatedObjectProjection`/`RelatedObjectCardinality` 오류와 첫 batch에서의 중단을 각각 요구하도록 테스트 한 파일만 강화했다.
최종 source **2,123파일**, map hash
`a7becfc25861637e8f579ed48c81ba008f9973e98801efd10d3481b82d0e0c72`와 위 제품 checkpoint의 차이는
`orm/prefetch_single_test.go`뿐이다. 제품·생성기·생성물·소비자는 동일하다. 최종 source에서 해당 두 root의
**9 run=PASS / skip 0**를 일반·race·CGO=0 각각 확인했다. 전체 checkpoint를 최종 test-only source에서 다시 실행했다고 표시하지 않는다.

원본 generated 소비자와 native 회귀를 확인한 뒤 부모 재조회·하위 collection cache 제거·foreign row 허용·NULL handle의 session 제거
네 runtime overlay를 정상 compile 후 모두 탐지했다. 외부 overlay로 강화한 테스트를 먼저 확인하고 같은 내용을 소스에 반영했다.
초기 generated 소비자는 test backend 인터페이스에 없는 Atomic 직접 호출로 compile 실패했다. 실제 `db.Atomic` capability로 수정했으며
제품 코드를 완화하지 않았다. 초기 compile 실패와 느슨한 negative 판정, 수정 후 실행 로그를 모두 보관한다.

환경은 Go **1.26.5**, Darwin arm64, modernc SQLite, PostgreSQL **17.5 Homebrew**다.
양 DB checkpoint는 `GODJ_REQUIRE_POSTGRES=1`·mode별 전용 database를 사용했다. 완료 실행의 cleanup은 **0|0|0**, force 없이 제거했다.
영향 vet·format·문서 링크·diff와 5개 프로젝트 generated drift도 통과했다.
원본 로그·source map·필수 inventory·receipt·test-only delta·negative overlay는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-single/`의
`latest-final-*-path`, `latest-final-test-path`, `latest-negative-path`, `latest-checks-path`가 가리키는 artifact를 따른다.

Reverse collection의 target eager 구성(`prefetch_eager_child`)·custom single target query·owner별 slice·materialized streaming,
Ticket 컬렉션 Form/Admin/API/OpenAPI/client와 권한·CSRF·Category·동시성·durability는 남아 있다.
GDJ-0099 Hosted 전체 milestone도 남아 있으며, 기록된 최근 전체 검증은
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739), source `93e77bd9c19d6e7b137de3a068c40a403970e73d`다.

## GDJ-0099 — filtered child query와 manager cache 소유권

2026-09-26, `3d2c751ab5cbf6083b308613e0ae43eb41deff33` 위에서 ManyToMany target의
Filter·OrderBy·Distinct와 명시한 하위 prefetch 설정을 native/generated API에 연결했다.
`PrefetchPath`는 같은 origin의 path selector를 만들며 custom typed selector와 조합할 수 있다.
이미 선택한 경로의 query 재정의는 I/O 전에 거부하고, custom query 뒤에 하위 path를 더하는 것은 허용한다.

Custom target은 `ForPrefetchOwners`의 같은 Query AST를 사용한다. Target model projection과 owner key를
같은 행에서 해석하고 batch 밖·NULL owner, 잘못된 presence/PK와 clone의 PK 변경을 거부한다.
명시한 query의 하위 설정은 파생 Filter/OrderBy/Distinct/Fresh·cold First/At에 남으며 model과 graph를 함께 publish한다.
각 manager는 기본 membership plan을 따로 보관한다. Mutation·Invalidate·manager Fresh는 기본 조회로 돌아가고
held query의 조건·설정·snapshot과 다른 materialization은 유지한다. Facade ABI v14·golden과 프로젝트 5개의 생성물을 갱신했다.

검증 source는 non-Markdown **2,118파일**, map hash
`6557f0ad23c640d22fe32d4bbf616990f8948dc17b348d6f8d40c885137141bd`다.
`./orm ./codegen ./codegen/consumertest` 전체에서 일반·CGO=0 각각 **3 package / 1,068 run=PASS / skip 0**,
133.7초·149.0초다. Race도 **3 package / 1,068 run=PASS / skip 0**, 528.7초다.
필수 root는 20개이며 모든 완료 실행 전후 source map이 같다. Actual generated ManyToMany module은
각 완료 mode에서 SQLite/PostgreSQL **248 child run=PASS / skip 0**다.

최종 조합 점검에서 설정된 target query의 eager·추가 prefetch가 기존 하위 설정을 버리는 경로를 보완했다.
Eager rowset을 닫고 검증한 뒤 기존 child batch를 읽고 한 평가로 공개한다. 추가 prefetch는 기존 선택 뒤에 병합하며
두 경로 모두 원래 project binding과 node budget을 유지한다.
최종 source는 **2,120파일**, map hash
`925a03d5ecab54bf80b995930e33b730aa44d788709e620c6af54abecfcf3230`다.
앞의 넓은 checkpoint와 차이는 ORM 3파일·소비자 테스트 4파일이며 생성기·생성물은 같다. 최종 source에서
`./orm` 전체와 ManyToMany 소비자·compile-negative·새 composed eager 소비자를 다시 검증했다.
일반 **2 package / 606 run=PASS / skip 0**, 20.7초다. Race·CGO=0도 각각 **606 run=PASS / skip 0**, 60.6초·22.8초이며 세 mode 모두 같은 최종 source다.
기존 ManyToMany **248 child**와 새 조합 소비자 **9 child**의 필수 양 DB 실행을 각 부모 harness가 확인한다.
앞 source의 넓은 범위를 최종 source의 전체 재실행으로 표시하지 않는다. File map 차이는 `composition-delta.json`에 보관한다.

새 조합 소비자는 nullable FK를 갖는 target에서 기존 child 설정과 eager cache가 함께 남는지 검사한다.
Cold First·full All은 각각 source+child **2 query**, warm All/First는 **0**, cold Count는 **1**이다.
추가 prefetch는 기존·추가 관계를 **3 query**로 읽고 foreign binding·원래 설정을 포함한 node 한도를 I/O 전에 거부한다.
실패 재시도와 session 종료도 확인했다. Fixture 준비 중 cross-app 순환 의존과 단일 target 모델만 만드는 기존 helper의
한계가 각각 migration validation에 걸렸다. 같은 앱의 별도 FK target과 모든 declared target 모델의 순서 있는 생성으로
fixture를 수정했으며 제품 migration 검사를 완화하지 않았다. 두 실패 실행도 보존했다.
추가 SQLite 조합 원본 **5 run=PASS** 이후 eager에서 기존 child 제거·추가 prefetch에서 기존 선택 제거의 두 overlay를
정상 compile 후 모두 탐지했다. 이전 네 overlay도 최종 source에서 재실행했다.


고정 Django의 기존 `prefetch_filtered`, `prefetch_filtered_cache`, `prefetch_order_conflicts`를 실제 생성 API와 비교한다.
초기 target/child **2 query**, warm **0**, refined target/child **2**, no-op add 뒤 기본 manager **1 query**와
내림차순·held/duplicate snapshot·lookup 순서별 허용/거부를 확인했다. 기존 owner-filter 일곱 사례도
compiler plan 직접 실행뿐 아니라 generated selector의 Filter/Distinct와 model collection 접근자로 비교한다.
Nullable/nonunique intermediary의 중복과 명시 DISTINCT, 기본 child와 명시 child 설정의 차이,
cold/warm First·First 뒤 full All, Count, 실패·취소 후 source부터 재시도, session 종료와 typed predicate 거부를 포함한다.
Raw Iterate가 설정을 조용히 버리지 않고 명시 오류를 반환하는 것도 확인했다. Materialized batch streaming은 남은 범위다.

SQLite 생성 모듈 원본 **6 run=PASS** 뒤 아래 네 runtime overlay를 정상 compile 후 모두 탐지했다.
명시한 하위 설정 제거, mutation 후 custom filter 잔존, lookup query 재정의 허용, batch 밖 owner 허용이다.
하위 설정 제거는 한 target만 남는 reference에서 query 수가 우연히 같으므로, 여러 target의 파생 조회와
Fresh/First 뒤 All을 검사하는 회귀가 실패한다. 첫 negative harness는 필수 실패 이름을 단일 target reference로 지정해
중단했으며, 실제로 실패한 multi-target 회귀를 필수 이름으로 정정하고 네 변이를 모두 재실행했다.
그 사이 제품과 테스트 source는 바뀌지 않았고 최초 중단의 로그도 보존했다.

고정 Django runner·48개 관찰 fixture는 변경/재실행하지 않았다. Pinned Django 6.1의 `_apply_rel_filters`,
`_chain`, `get_prefetch_querysets`, `prefetch_one_level`에서 custom 조건 뒤에 core membership을 붙이는 소유권도 확인했다.
Eager parent/child 조합은 아직 reference-only이며 이번 filtered 기능의 PASS로 합치지 않는다.
환경은 Go **1.26.5**, Darwin arm64, modernc SQLite, PostgreSQL **17.5 Homebrew**다.
`GODJ_REQUIRE_POSTGRES=1`과 mode별 전용 database를 사용했다. 완료 실행의 cleanup은 **0|0|0**이며 force 없이 제거했다.
영향 vet·gofmt·5개 generated drift·문서 링크·diff와 checked-in relation product의 deterministic candidate 검사를 통과했다.
Source·command·event·inventory·DB cleanup·overlay·supplemental receipt는 아래에 보관한다.

`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-filtered`

`latest-final-{normal,race,cgo0}-path`는 넓은 checkpoint, `latest-composition-{normal,race,cgo0}-path`는 최종 조합 검증이다.
`latest-negative-path`, `latest-composition-negative-path`, `supplemental-checks.json`에 추가 검증을 보관한다.
Eager/prefetch 구성·owner별 slice·설정된 query의 streaming, Ticket 컬렉션 Form/Admin/API/OpenAPI·client와
권한/CSRF/양쪽 Category/전체 후보/동시성/durability 및 해당 Hosted 전체 milestone은 아직 남아 있다.

## GDJ-0099 — 중첩 ManyToMany와 공통 model materialization

2026-09-26, `265afaf18d55bc0b118ea4d38448e4a1e1be545b` 위에서 중첩 collection graph를
생성된 모델 접근자까지 연결했다. `ManyPrefetch.WithChildren`과 generated typed/path selector가 같은 tree를 사용한다.
같은 관계의 하위 요청을 합치며 각 단계의 전체 parent batch를 함께 읽는다. 깊이 64·중복 포함 1024 node 한도를
준비 단계에서 확인한다. 모든 하위 조회의 성공·context·session 검사를 마친 뒤에만 결과를 공개한다.

`RelatedSelected`에 source·single-valued·collection cache를 함께 담고 일반 generated 조회도
`Materialize`/`MaterializeFirst`를 통해 query 평가에 붙은 graph를 복제한다. Objects와 Collections를 같은
project binding으로 생성한다. 반환 model/manager·중복 owner·held query는 각각의 cache 소유권을 유지한다.
Facade ABI v13·relation-reverse ABI v6와 golden·checked-in 프로젝트 5개의 생성물을 함께 갱신했다.

최종 non-Markdown source는 **2,116파일**, map hash는
`7eea025369b01b08dcb414741401096952119bcf07b1aedc3265ba05376f1f26`다.
`./orm ./codegen ./codegen/consumertest` 전체를 실행했다. 실행 전후 source map이 같고 필수 root는 20개다.
일반·CGO=0은 각각 **3 package / 1,068 run=PASS / skip 0**이며 139.6초·137.1초다.
Race도 **3 package / 1,068 run=PASS / skip 0**, 514.3초이며 세 mode의 source map이 같다.
Actual generated ManyToMany module은 세 mode 모두 양 DB **231 child run=PASS / skip 0**다.
다른 generated 소비자의 일반 query·forward/reverse·single/multiple/nested eager·projection·session 회귀도 포함한다.

새 소비자는 고정 Django의 `prefetch_nested`와 같은 3단계 `labels__owners__labels`를 비교한다.
Owner source query를 제외한 추가 query **3회**, warm 접근 **0회**, Filter refinement 뒤 **4회**와
전체 graph·빈 owner·독립 duplicate를 확인한다. Typed/path/중복 선택의 결과가 같고 아래 위험도 유지한다.

- 깊이·중복 포함 node 한도, typed cycle·잘못된 child target·foreign origin·하위 unknown path를 I/O 전에 거부한다.
- 늦은 하위 실패·취소는 partial graph를 공개하지 않으며 같은 query의 재시도는 source부터 다시 읽는다.
- Cold First는 full cache를 채우지 않고 warm First와 concurrent warm All은 graph를 보존한다.
- Nested manager 변경은 해당 manager만 무효화하며 다른 materialization·held query의 snapshot과 deep clone은 유지한다.
- Owner multiplicity·empty no-child-read·borrowed session 종료 뒤 warm target query와 접근자 거부를 확인한다.

SQLite 실제 생성 모듈 원본 **6 run=PASS** 이후 runtime overlay 세 개를 실행했다.
하위 graph 전달 제거, mutable collection handle 공유, 중복 선택의 하위 요청 누락은 모두 정상 compile 뒤
대응 assertion에서 실패했다. 제품 파일은 변경하지 않았으며 overlay·결과·실패 이름·hash를 별도 보관했다.
고정 Django runner·fixture는 변경하지 않았고 이번에는 reference를 재실행하지 않았다. 기존 48개 관찰 중
중첩 collection graph와 이전 owner-filter SQL을 제품에서 비교하며 filtered/eager 조합은 아직 reference-only다.

초기 정상 검증의 생성기 namespace 충돌 test는 먼저 보고되는 식별자를 과거 `ABCQuery`로 고정해 실패했다.
여전히 잘못된 입력의 nil bytes·명시적 충돌 오류를 요구하면서 식별자를 `ABC`로 수정한 뒤 최종 source를 검증했다.
이 실패 실행도 보관했으며 현재 PASS 수에 합치지 않는다.

환경은 Go **1.26.5**, Darwin arm64, modernc SQLite와 PostgreSQL **17.5 Homebrew**다.
`GODJ_REQUIRE_POSTGRES=1`과 mode별 전용 database를 사용했다. 세 실행 모두 cleanup 관찰은 **0|0|0**이고
force 없이 database를 제거했다.
영향 vet·gofmt·문서 링크·diff, 예제/fixture 5개 generated drift와 checked-in relation product의 deterministic candidate 검사를 통과했다.
실행 source·command·event·stderr·inventory·DB cleanup·overlay는 아래 디렉터리에 보관했다.

`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-materialization`

`latest-final-{normal,race,cgo0}-path`, `latest-negative-path`가 각 상세 receipt를 가리킨다.
이번 범위는 중첩 ManyToMany와 결과 materialization이다. Filtered child·eager/prefetch 구성 API·owner별 slice,
Ticket Form/Admin/API/OpenAPI·client와 그 뒤의 Hosted 전체 통합은 남아 있다. Hosted 전체를 이번 source의 PASS로 세지 않는다.

## GDJ-0099 — custom prefetch의 owner projection과 연결 행 scope

2026-09-23, `8cff2ea317af2008072248571206fd461c6ea942` 위에서 custom target query를 위한
`Plan.ForPrefetchOwners`·`ResultPrefetch`를 같은 Query AST와 SQLite/PostgreSQL compiler에 연결했다.
기존 target WHERE·정렬·DISTINCT를 보존하고 첫 grouping join과 가장 최근의 일치하는 membership join을 구분한다.
NOT EXISTS 내부의 경로를 외부 join으로 재사용하지 않으며 owner projection에 요청 batch의 필수 IN anchor를 요구한다.
이것은 조회 기반이다. Generated nested/filtered prefetch tree·하위 model cache·eager 통합은 아직 구현 중이다.
Owner별 slice는 window 계획이 필요하므로 일반 LIMIT/OFFSET으로 실행하지 않고 명시 오류로 거부한다.

제품 checkpoint의 non-Markdown source **2,113파일** map hash는
`1267c19f0d2456989c0edbaedd584a17ab9f959dae7a66684ab1e95f0c232440`다.
`./query ./db/internal/queryplan ./db/sqlite ./db/postgres ./orm` 전체와 `./codegen/consumertest`의
`TestGeneratedManyToManyCollections`, `TestGeneratedCollectionFacadeRejectsNamespaceAndCrossModelInputs`를 실행했다.
각 **6 package / 6,561 run=PASS / skip 0**, 필수 root 13개다. Normal 34.7초, race 89.4초, CGO=0 31.6초이며
세 mode 모두 실행 전후 source가 같다. Actual generated module의 양 DB **210 child run=PASS**도 각 parent가 검사했다.

최종 source hash는 `d66e5960d18025459e6c9b35e411eeb98aba8635dc50b5d525fddd0aeb69a855`다.
차이는 부모 harness 한 파일에 새 owner-plan 일곱 subcase를 양 DB 모두 필수 inventory로 추가한 것뿐이다. 제품·fixture·다른 테스트는 같다.
최종 source에서 해당 actual consumer/compile-negative를 normal/race/CGO=0으로 다시 실행했다. 각 **5 parent / 210 child PASS / skip 0**,
source 불변이며 이전 넓은 범위를 최종 source의 재실행으로 표시하지 않는다. Source 차이는 `final-delta.json`에 보관했다.

새 native 사례는 custom owner 조건·두 owner 조건·연속 Filter·각각 한 owner만 요청·DISTINCT·제외 조건의 일곱 결과를
독립 Django 관찰과 비교한다. Grouping owner가 요청 밖으로 빠져나오면 실패하며 query 수는 batch당 1이다.
기존 mutation·session·direct prefetch·cache 및 mixed query 회귀도 같은 actual generated module에서 유지했다.
그 외 새 nested/filtered/eager reference group은 아직 reference-only이며 이 일곱 SQL 사례의 성공으로 제품 완료를 주장하지 않는다.

독립 Django **6.1**은 두 DB·hash seed 0/813의 **48개 관찰**이다. 기존 41개 관찰과 고정 Django source hash를 보존했다.
Nested warm/refined cache, filtered ordering·no-op mutation 후 manager/held/duplicate snapshot, lookup 순서 충돌,
eager parent와 eager child 조합, 관계 filter의 grouping scope를 추가했다. CPython **3.14.3**, SQLite **3.50.4**,
PostgreSQL **170005**, asgiref **3.12.1**, sqlparse **0.5.5**, psycopg **3.3.6**을 고정했다.
최종 runner SHA256은 `b9fac0cefffd230d04dad5584b0f0c1c18fab910e7a7ac2136e7adab147d78c6`이며 unittest **11 PASS**다.
Nested load 제거·정렬 반전·owner 조건 변경·eager 제거라는 실제 의미 변경 다섯 가지도 차이를 감지했다.

Go runtime/AST의 SQLite generated module 원본 **9 run=PASS** 뒤 grouping을 마지막 alias로 바꾸기, membership에 새 alias를
강제하기, grouping owner의 필수 anchor를 제거하기의 세 overlay는 대응 사례에서 모두 실패했다. 모두 정상 compile 뒤 실패했고
workspace 제품 파일은 변하지 않았다. 초기 test 작성 중 `query.FieldText`라는 잘못된 상수 이름의 compile 실패는 `FieldString`으로 수정했다.

환경은 Go **1.26.5**, Darwin arm64, modernc SQLite와 격리 PostgreSQL **17.5 Homebrew**다.
DB 회귀는 `GODJ_REQUIRE_POSTGRES=1`과 mode별 전용 database를 사용했다. 넓은 세 실행의 추가 연결·사용자 table·추가 schema는 **0|0|0**이었다.
마지막 CGO=0 consumer의 첫 cleanup 관찰은 **1|0|0**이었으며 그 뒤 force 없는 database 제거가 성공했다.
최종 별도 catalog 조회에서도 해당 임시 database가 남지 않았음을 확인했다. 이 첫 관찰을 0으로 바꾸어 기록하지 않는다.
영향 vet와 CI Python **41 PASS**를 확인했다. Generator는 바뀌지 않아 generated drift를 다시 실행하지 않았다.
최종 gofmt·문서 링크·diff는 `final-document-check.json`을 따른다.

전체 command·source·event·stderr·inventory·reference·negative control은
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch-tree/`의
`latest-*-path`, `product-checkpoint-source.json`, `final-source.json`, `final-delta.json`, `terminal-database-cleanup.json`에 보관했다.
이 범위는 GDJ-0099 Hosted 전체 milestone이나 전체 프레임워크 완료가 아니다.

## GDJ-0099 — 역방향 prefetch의 session publication 보완

2026-09-23, 아래 direct prefetch 묶음 `61aadfc6`을 검증한 뒤 기존 reverse prefetch의 두 경계를 보완했다.
종료된 session에 빈 owner 입력을 주거나, 조회 후 storage callback이 grouping 중 session을 종료하면 기존 코드는 성공 결과를 반환했다.
원본 source에 두 regression만 overlay하여 **2 FAIL**로 재현한 후, 공통 load 진입과 empty/cache/handle 반환에서 session을 다시 검사했다.
원인 오류를 유지하고 partial result는 반환하지 않는다. Reverse FK와 OneToOne prefetch는 같은 공통 load를 사용한다.

최종 non-Markdown source 2,111파일의 map hash는
`ce4a71d6f1bb33e1eb1a291c2b6ab9d4ec59320156f21e9b509b7812474992fa`다.
아래 넓은 prefetch checkpoint와의 제품/검증 source 차이는 `orm/reverse_prefetch.go`, `orm/session_test.go` **두 파일뿐**이다.
`./orm` 전체와 실제 generated prefetch/OneToOne 소비자 두 root의 **2 package / 600 run=PASS / skip 0**를
normal(8.4초), race(22.2초), CGO=0(6.5초)에서 각각 확인했다. 모두 필수 root 5개와 실행 전후 source 불변을 확인했다.
이 후속 검사에서 native DB 전체나 전체 generated consumer suite를 다시 실행한 것으로 세지 않는다.

실행·원본 실패·source 차이는 아래 `prefetch/` artifact의 `latest-reverse-session-*-path`,
`reverse-session-delta.json`, `reverse-session-final-source.json`을 따른다.
영향 vet·gofmt·문서/diff는 `reverse-session-document-check.json`에 보관한다. Hosted 전체 milestone은 계속 미완료다.

## GDJ-0099 — 직접 ManyToMany prefetch와 model cache

2026-09-23, `2b9685ea53dc98eb3cc56e2e4e835bd763ac4f0a` 위에서 direct ManyToMany batch prefetch와 generated
`PrefetchRelated`·typed selector·문자열 경로를 연결했다. 기존 through QuerySet과 target eager projection을 재사용한다.
Owner multiplicity·nullable/nonunique through·양방향/self와 여러 direct selection을 처리하고 전체 batch 성공 뒤 cache를 반환한다.
중첩/filtered child prefetch·eager/prefetch tree 통합, Ticket 소비자와 GDJ-0099 Hosted 전체 milestone은 남아 있다.

최종 non-Markdown source **2,111파일**의 정렬 path→SHA256 map hash는
`b9f434778fc3c5894ce73295a616a4c309391b2241ac59be95392ca828faf0c7`다.
검증 대상은 ORM·생성기·실제 생성 소비자와 기존 facade/OneToOne 회귀이며 DB compiler·migration 전체나 전체 platform 실행으로 확대하지 않는다.

- Normal: `./orm ./codegen ./codegen/consumertest ./internal/compiletest ./conformance/relationreverseproduct ./conformance/onetoonefixture`의
  **6 package / 1,255 run=PASS / skip 0**, 필수 root 9개, 실행 전후 source 불변(136.2초).
- 같은 6 package의 Race: **1,190 run=PASS / skip 0**, 필수 root 10개, source 불변(511.5초).
  `internal/compiletest/compile_test.go`의 기존 `!race` build tag에 따라 세 facade/project-bundle root를 요구했다.
- 같은 6 package의 CGO=0: **1,255 run=PASS / skip 0**, 필수 root 9개, source 불변(130.0초).
  외부 compile/type-misuse 전체는 normal·CGO=0에서 실행했다.

Actual generated module은 양 DB **193 child run=PASS / skip 0**를 각 mode의 parent가 검사했다. 독립 reference의 다섯 prefetch group,
1001 owner의 두 batch·늦은 실패·다른 owner 행/반복 through PK 거부·scan 도중 취소와 rows close, 다중 selection의 늦은 실패 후
source부터 재평가, cold Count/First·중복/Distinct·slice·Fresh, origin/type/namespace 거부와 동시 All cache 소유권을 확인했다.
빌린 ordinary/coordinated session의 실제 capability를 제한한 adapter로 읽기 성공·변경/no-op 거부·중첩 transaction 미실행과
callback 종료 뒤 warm/empty cache·model/query 거부를 확인했다. 기존 collection 변경·rollback·session 회귀를 유지했다.

독립 Django **6.1** 관찰은 두 DB·hash seed 0/813의 **41개**다. 앞의 36개 관찰과 고정 Django source hash가 그대로이며
동일 backend의 두 seed와 양 DB 관찰이 일치한다. 새 관찰은 batch/warm/refined/mutation query 수, held/duplicate-owner cache,
nullable duplicate와 reverse, 대칭/비대칭 self다. CPython **3.14.3**, SQLite **3.50.4**, PostgreSQL **170005**,
asgiref **3.12.1**, sqlparse **0.5.5**, psycopg **3.3.6**을 고정했다. Runner SHA256은
`578616a34697750c725e260d41a836ddf74c196158fa7985afbc528e2bc7ab31`이고 reference unittest **9 PASS**다.
Reference helper에서 held QuerySet에 `.all()`을 추가 호출하던 초안은 새 평가를 만들어 다른 동작을 관찰했다.
그 helper를 held 값의 직접 iteration으로 고친 뒤 양 DB·두 seed를 다시 capture했다. 이전 receipt를 삭제하지 않았다.
Django prefetch 호출을 제거한 실제 negative control은 warm 조회가 발생해 실패한다.

별도 SQLite generated module에서 원본 **11 run=PASS** 뒤 ready cache 제거, target 행 소실, batch owner 검증 제거,
relation capability 검사 제거, canonical handle 공유의 다섯 overlay가 각각 대응 사례에서 실패했다.
추가로 target PK가 같은 행만 coalesce하는 overlay도 nullable multiplicity 사례에서 실패했다.
각 overlay는 정상 compile 뒤 실패했으며 workspace 제품 파일은 변경하지 않았다. 독립 reference와 Go-native 소유권 증거를 구분한다.

환경은 Go **1.26.5**, Darwin arm64, modernc SQLite와 PostgreSQL **17.5 Homebrew**다.
`GODJ_REQUIRE_POSTGRES=1`과 mode별 전용 database를 사용하며 추가 연결·사용자 table·추가 schema **0|0|0**을 확인하고 제거한다.
중간 consumer 실행은 PostgreSQL ordinary session이 RelationSession도 구현한다는 사실을 무시한 test assertion 때문에 실패했다.
실제 capability를 제한한 adapter와 중첩 transaction trap으로 수정했으며 최종 normal에서 양 DB를 통과했다.
다음 실행의 disk-full build 실패도 보존했다. 당시 여유 공간 217MiB에서 실행 중인 Go 작업이 없음을 확인하고 `go clean -cache`만
수행해 약 95GiB를 확보했다. Source·module cache·증거는 유지했고 같은 source의 consumer를 재실행해 통과했다.

다섯 project의 실제 CLI generated drift, 영향 vet, CI Python **41 PASS**를 확인했다. Facade ABI v12의 generated Go·manifest·golden을
실제 generator로 갱신했다. 최종 gofmt·문서 링크·diff 결과는 `final-document-check.json`에 보관한다.
전체 command·source map·event·stderr·inventory·receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/prefetch/`의 `latest-*-path`, `final-source.json`을 따른다.
최신 Hosted 전체는 source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739)이며 이번 prefetch source의 전체 검증으로 전이하지 않는다.

## GDJ-0099 — 컬렉션 관계 조건과 공통 typed/dynamic 경로

2026-09-23, `031bfbabbb548278fedd5c213ead472986c5954a` 위에서 forward/reverse/ManyToMany의 mixed scalar 조건을
`QueryRelation`·`ChainRelations`·generated `BindRelations`와 같은 Query AST에 연결했다. Filter별 join scope·중복/Distinct/Count,
부정 EXISTS의 상관 identity와 조건 순서, OR·nullable through·manager core filter를 처리한다.
ManyToMany prefetch·Ticket 컬렉션 편집과 GDJ-0099 Hosted 통합은 남아 있다.

최종 non-Markdown source **2,107파일**의 정렬 path→SHA256 map hash는
`cc148bbfe75f4fa0b903e07b792f6a832da67be161e209c64cd2fb1c9094b285`다. 모든 아래 최종 실행은 시작/종료 source가 동일하고 test skip이 없다.

- 최종 normal: `./orm ./query ./codegen ./codegen/consumertest ./db/internal/queryplan ./db/sqlite ./db/postgres`
  및 `./internal/compiletest ./conformance/relationreverseproduct ./conformance/onetoonefixture`에서
  **10 package / 7,209 run=PASS / skip 0**, 필수 root 19개를 확인했다(181.7초).
- 최종 변경 범위의 race: 위 마지막 세 package 전체와 PostgreSQL `TestPostgresOneToOneReverseLookupsMatchDjango`에서
  **4 package / 168 run=PASS / skip 0**, 필수 root 5개(7.9초).
  같은 범위 CGO=0은 **233 run=PASS / skip 0**, 필수 root 4개(11.2초).
  `internal/compiletest/compile_test.go`는 기존 `!race` build tag를 가지므로 외부 compile/type-misuse 전체는 normal·CGO=0 증거다.
  Race에서는 실제 빌드되는 facade/project-bundle의 세 root를 요구한다. 최초 임시 harness의 잘못된 race inventory 실패도 보존했다.
- 위 최종 source 이전 `94bead39a90c8a83977c50b48eb82101396237e52ae057ddab7ea1a4af78a1e3`에서 앞의 7개 package를 normal/race/CGO=0으로 실행했다.
  각 **7,018 run 중 7,017 PASS·1 FAIL·skip 0**이며 모두 source 불변이다. 실패는 공통 OneToOne test helper가 정상 빈 조회의
  backend 진입을 오류 경로의 호출 횟수에 합친 assertion 한 건이다. 이 넓은 실행 전체를 PASS로 기록하지 않는다.
  정상 LIMIT 0의 실제 SQL count와 invalid/canceled 입력의 backend 호출을 따로 검증하도록 수정했다.
  추가로 외부 compile fixture 두 곳의 이전 BindReverseRelations 호출을 새 API로 옮겼다. 이후 차이는 이 **검증 파일 세 개뿐**이며
  제품·생성기·다른 테스트 입력은 같다(`final-delta.json`). 이 수정은 위 최종 normal과 해당 race/CGO=0 범위에서 확인했다.

별도 actual generated module의 양 DB **172 child run=PASS**를 마지막 넓은 세 mode와 최종 normal에서 부모가 검사했다.
새 reference의 다섯 query group을 빠짐없이 typed/dynamic AST·조회 결과·cold Count로 비교하고, reverse root·nullable duplicate·
shared through alias·고정된 manager scope를 함께 검사한다. 기존 mutation·session·cache 소유권 사례도 유지한다.
JSON collection의 lookup·부정·중복과 mixed 단일/다중 관계를 실제 생성 API로 검증했다. SQLite JSON containment 미지원은
All/Count/LIMIT 0에서 SQL 없이 명시 오류로 남는다. Row identity 충돌과 EXISTS 내부의 SQLite 64-table 한도도 빈 결과보다 먼저 검증한다.

독립 Django **6.1** 관찰을 양 DB·hash seed 0/813에서 **36개**로 확장했다. 기존 31개와 고정 Django source hash를 보존했고
동일 backend의 두 seed 및 양 DB의 관찰이 같다. Reference 환경은 CPython **3.14.3**, SQLite **3.50.4**, PostgreSQL **170005**,
asgiref **3.12.1**, sqlparse **0.5.5**, psycopg **3.3.6**이다. Runner SHA256은
`56ad9e628bb70a1ba46f3bd53c66c2b363be0efb2b82ce3f886303a4b7b69a0e`이며 전용 reference unittest **8 PASS**다.
하나의 Filter로 합치기·Q 순서 바꾸기 등 실제 Django 의미 변경은 reference negative control에서 실패한다.

Go runtime은 별도 SQLite generated module에서 원본 **59 run=PASS** 뒤, filter scope 합치기·root로 상관 대상 강제·manager의
첫 scope 재사용 제거·INNER JOIN 강제라는 네 overlay를 각각 적용했다. 각 대응 query 사례가 실제 실패하고 workspace 제품 파일은
변하지 않았다. 원본/변경 파일·전체 event·실패 inventory와 hash를 보존했다. Reference 결과와 Go-native 보안/소유권 증거를 구분한다.

공통 로컬 환경은 Go **1.26.5**, Darwin arm64, modernc SQLite와 격리 PostgreSQL **17.5 Homebrew**다.
DB 테스트는 `GODJ_REQUIRE_POSTGRES=1`이며 mode별 전용 database의 추가 연결·사용자 table·추가 schema가 종료 시 **0|0|0**인 것을
확인하고 database를 제거했다. 독립 PostgreSQL helper entrypoint는 직접 root 실행에서 제외하고 실제 subprocess 회귀를 유지했다.
제품 전체 플랫폼/cold-build를 실행한 것으로 세지 않는다.

다섯 기존 project의 실제 CLI generated drift와 영향 vet, 마지막 수정 helper/외부 소비자의 vet, CI Python **41 PASS**를 확인했다.
Query ABI v3·reverse companion ABI v5와 generated Go/manifest/golden을 실제 generator로 갱신했다.
초기 golden/기존 미지원 기대값·nullable 객체 AST 불일치와 후속 수정의 실패 로그를 지우지 않았다.
최종 gofmt·문서 링크·diff 검사 결과는 같은 artifact의 `final-document-check.json`에 보관한다.

전체 command·source map·event·stderr·inventory·receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/collection-query/`의 `latest-*-path`, `final-source.json`, `final-delta.json`을 따른다.
최신 Hosted 전체는 여전히 source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739)이다. 이후 변경의 Hosted 전체 결과로 전이하지 않는다.

## GDJ-0099 — model collection facade와 빌린 session composition

2026-09-22, `5e949e470ecfa8f60aa949fcb1c844a30d843640` 위에서 generated model의 forward/reverse collection 접근자와
`UsingSession`/`InSession`을 연결했다. Root와 빌린 session constructor를 구분하며 후자는 기존 transaction/fence 안에서
작업한다. 일반 관계 조건/prefetch·Ticket 컬렉션 소비자·GDJ-0099 Hosted 통합은 남아 있다.

최종 non-Markdown source **2,101파일**의 정렬 path→SHA256 map hash는
`06f9fe134f9b6cd783bafa4287226a5aec2c99a2476137bcb8a0a07a015816f9`다. 실행 source와 범위를 다음처럼 구분한다.

- 넓은 영향 source map `d1403883bceb056bcb041778498a49c9e9c9c964ec4e933692a7213dff77958c`에서
  `./orm ./query ./codegen ./codegen/consumertest ./db/internal/queryplan ./db/sqlite ./db/postgres`를 실행했다.
  Normal·race·CGO=0 각각 **7,012 run=PASS, skip 0**, 7개 package 완료와 12개 필수 root를 확인했다.
  세 mode 모두 실행 전후 source가 동일하다(159.6초/500.9초/160.0초).
- 그 뒤 iterator가 Scan/Clone 중 끝난 session의 행을 callback에 전달하는 경로를 발견했다.
  별도 artifact의 test overlay로 실제 callback 1회 호출을 확인한 실패 로그를 보존하고, 복제 후 callback 직전에 수명을 다시 검사했다.
  최종 source에서 ORM 전체와 `./codegen/consumertest -run '^TestGenerated(ManyToMany|CollectionFacade)'`를 실행했다.
  Normal·race·CGO=0 각각 **601 run=PASS, skip 0**, 7개 필수 root와 실행 전후 위 최종 hash의 일치를 확인했다(16.3초/38.3초/16.9초).
  넓은 실행 이후 차이는 `orm/manager.go`, `orm/session_test.go`, CI 필수 목록 한 파일뿐이며 `final-delta.json`에 보관한다.

공통 환경은 Go **1.26.5**, Darwin arm64, modernc SQLite, 격리 PostgreSQL **17.5 Homebrew**다.
DB 실행은 `GODJ_REQUIRE_POSTGRES=1`이며 각 mode의 전용 database를 사용한다. 모든 실행의 종료 시 connection·사용자 table·
추가 schema가 0개임을 확인하고 해당 database를 제거했다. 독립 `TestPostgresRevisionFenceHelperProcess` entrypoint는
root 목록에서 제외하고 이를 실제 subprocess로 호출하는 PostgreSQL 회귀는 포함했다. 제외 패턴의 다른 helper인
`TestPublicationCrashHelper`는 이번 package 범위 밖이므로 publication crash 검증으로 세지 않는다. 필수 실행 누락과 test skip을 허용하지 않는다.

별도 generated module의 양 DB **55개 child run=PASS**를 넓은/최종 각 mode에서 부모가 전체 JSON event로 검사한다.
Native ordinary/coordinated relation session 각각의 commit/rollback에서 scalar 저장과 여러 collection 변경을 조합했다.
같은 transaction의 provisional 읽기, outer rollback의 전체 복원, outer commit의 durable 저장과 root cache 독립성을 확인한다.
Nested Atomic을 호출하면 실패하는 session wrapper를 사용하며, 종료된 session의 model/view/Fresh/no-op과
warm/empty/eager QuerySet의 All/Count/Exists/At/First/Iterate가 새 SQL 없이 거부되는지 검사한다.
Projection/aggregate decoder·clone·streaming callback 경계의 수명 종료와 원인 오류 보존은 ORM 회귀가 소유한다.

같은 owner 접근자의 cache 공유·동시 getter·다른 materialization/Fresh의 독립 cache, typed target의 origin·PK fence·복사 거부,
미저장 owner의 저장 후 binding·reverse 변경·실패 입력의 cache 무효화를 실제 generated API로 확인했다.
Collection 이름과 기존 method/promoted field의 충돌은 생성물 없이 실패하고, 다른 모델의 typed target은 실제 Go compiler가 거부한다.

Artifact 안의 별도 SQLite generated module에서 원본을 통과시킨 뒤 runtime overlay로 session 수명 검사·빌린 직접 실행·cache 공유를
각각 제거했다. 대응 회귀가 모두 실제 실패하며, workspace 제품 파일은 변경하지 않았다. 이는 Go의 session/cache 소유권에 대한
negative control이다. 이전 source의 고정 Django 양 DB 31개 관찰은 별도 reference 증거이며 이번 Go-native 검증으로 대체하지 않는다.

처음 넓은 normal 실행은 기존 OneToOne helper가 빌린 session에 `Using`을 호출해 실패했다. 그 실행은 PASS로 세지 않고 보존했다.
해당 한 곳을 `UsingSession`으로 바꿨으며 기존 제약 실패/rollback assertions를 유지한 뒤 위 넓은 세 mode를 통과했다.
다섯 project의 실제 CLI generated drift, 영향 vet, 최종 ORM/OneToOne helper vet, CI Python **41 PASS**, gofmt·문서/diff를 확인했다.
Facade ABI는 v11이며 생성된 Go와 manifest·golden을 실제 generator로 갱신했다.

실행 command·source·전체 event·필수 inventory·stderr·receipt와 원본 실패/negative control은
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/composition/`의
`latest-*-path`, `final-source.json`, `final-delta.json`, `final-supplementary.json`을 따른다.
로컬 영향 검증이며 Hosted 전체 결과가 아니다. 최근 Hosted 전체는 여전히 source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739)이다.

## GDJ-0099 — root collection manager와 transaction/cache 소유권

2026-09-22, `1c20e5d21a103f9ab6809b50ab9432e22f6503fd` 위에서 공통 runtime·generated `BindCollections()`의
forward/reverse add/remove/clear/set와 같은 AST의 기본 컬렉션 조회/Distinct를 연결했다. 자동/명시적 through·nullable/nonunique
연결·payload·자기 관계를 실제 별도 generated module에서 실행했다. 통합 model facade·외부 transaction composition·일반 관계
조건/prefetch·Ticket 컬렉션 소비자와 GDJ-0099 Hosted 전체 검증은 아직 남아 있다.

최종 non-Markdown source **2,097파일**의 정렬 path→SHA256 map hash는
`2af49c6d42359688e11474644faa5049c6723712c6bd27187752681f260f1a8e`다.
검증은 아래 두 source/범위로 구분한다. 뒤의 작은 보완을 앞선 전체 실행 결과로 덮지 않는다.

- 넓은 영향 source map `315cda6d0504e1424213c56f9a257dc1044ff8608e8e40e1e145a3ea0ce09e78`:
  `./orm ./query ./codegen ./codegen/consumertest ./db/internal/queryplan ./db/sqlite ./db/postgres`의
  normal·CGO=0은 각각 **6,997 run=PASS, skip 0**, 실행 전후 source 불변이다(118.2초/125.3초).
  Race도 Go exit 0, **6,997 run=PASS, skip 0**다(461.9초). 다만 실행 중 추가한 임시
  `.scratch/m2m-collections-negative/main.go` 때문에 원래 whole-workspace source audit는 실패했다.
  원본 실패 receipt를 그대로 보존했다. 유일한 변경이 이 ad-hoc 도구이고 346개 test/dependency package의 입력에 속하지 않으며
  모든 제품·테스트 입력이 동일함을 `scope-reconciliation.json`에 별도로 확인했다. 임시 도구는 artifact로 이동했다.
- 이후 실제 오류 주입으로 `Query`가 context를 취소한 뒤 nil rows/nil error를 반환하면 취소 원인이 누락됨을 발견했다.
  실패하는 regression을 먼저 실행했고, nil rows 규약 오류에 `context.Canceled`도 보존하도록 한 줄을 보완했다.
  최종 source에서 `./orm` 전체와 `./codegen/consumertest -run '^TestGeneratedManyToMany'`를 normal·race·CGO=0으로 재실행했다.
  각각 **584 run=PASS, skip 0**이며 세 mode 모두 시작/종료 source map이 위 최종 hash와 일치한다(9.6초/25.8초/7.5초).
  넓은 source 이후 제품/테스트 차이는 이 ORM 보완·새 regression뿐이다. 추가 CI 필수 목록 두 파일과 임시 helper 제거는
  `final-delta.json`에 구분했으며 CI Python **41 PASS**로 목록의 실제 실행 owner·shell 전달·workflow 한도를 확인했다.

공통 환경은 Go **1.26.5**, Darwin arm64, modernc SQLite **3.53.3**, 격리 PostgreSQL **17.5 Homebrew**다.
모든 DB 실행은 `GODJ_REQUIRE_POSTGRES=1`이며 mode별 전용 database를 만들었다. 종료 시 다른 connection과 사용자 table/schema가
없음을 확인하고 해당 database를 제거했다. Generated module의 양 DB **22개 child run=PASS**와 metadata child를 각각
전체 JSON event로 검사하며, 모든 필수 하위 사례·완료 package·skip 부재를 부모 테스트가 확인한다. Parent race/CGO mode를 상속한다.

실제 사례는 PK presence/0·동시 중복 add·retained ID/payload·Set(clear=true)·늦은 unique/FK 오류 rollback·nullable NULL 링크·
비고유 연결의 multiplicity/Distinct·모든 duplicate 제거·대칭 self/mirror와 directed reverse·삭제 root 집합의 PROTECT/CASCADE/SET_NULL·
재접속을 포함한다. Held QuerySet·독립 materialization·Fresh·실패 시 cache 무효화·동시 조회/변경도 검사한다.
Trigger가 insert를 생략한 실제 0행, insert 뒤 실제 context 취소, outer commit/unknown 결과 publication·늦은 취소,
누락/반복/비동기 callback과 nil/malformed/out-of-scope row·scan/iteration/close 오류를 확인했다.
Unknown 결과 주입은 manager의 publication 경계 검증이며 실제 driver의 commit/rollback uncertainty는 기존 양 DB port 회귀가 소유한다.

별도 generated SQLite module에서 원본을 통과시킨 뒤 runtime overlay로 mirror 생성을 제거하거나 Set이 retained link를 교체하도록
변경했을 때 대응 회귀가 실제 실패함을 확인했다. 원래 workspace 제품 파일은 변경하지 않았다.
독립 Django **6.1** reference는 양 DB·hash seed 0/813에서 **31개 관찰**이 동일하고 이전 29개 및 고정 Django source hash가
보존됐다. Nullable duplicate/NULL의 set/remove/clear/reverse clear와 incoming link 정책을 추가했다.
Python **3.14.3**, SQLite **3.50.4**, PostgreSQL **17.5**, psycopg **3.3.6**이며 reference unittest **7 PASS**와 실제
set/symmetry 변경 negative control을 유지한다. Reference 성공을 GoDj 전체 동등성으로 세지 않는다.

다섯 기존 project(Helpdesk·Article·relationfixture·onetoonefixture·cascadefixture)의 실제 CLI generated drift,
영향 vet·gofmt·문서 링크/diff를 확인했다. 최초 넓은 실행에서 실패한 생성 ABI 기준 파일은 실제 generator로 갱신했으며
그 실패 로그도 보존했다. 관련 실행의 source·전체 event·필수 inventory·stderr·receipt·negative control은
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/collections/`의
`latest-*-path`, `final-source.json`, `final-delta.json`, `python-checks.json`, `helpers/`를 따른다.
이 결과는 로컬 영향 검증이다. 최신 Hosted 전체 결과는 여전히 source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[CASCADE·TicketLabel full](https://github.com/progresshans/godj/actions/runs/35689549739)이며 이번 source로 전이하지 않는다.

## GDJ-0099 — CI 필수 목록의 워크플로 입력 한도

자동 storage 구현 source `e7b465a99154a18990fe07ee3973d05bfe290759`를 push한 뒤 GitHub의
[워크플로 admission 실패](https://github.com/progresshans/godj/actions/runs/35729576853)를 확인했다.
CI job은 하나도 시작되지 않았으며, PostgreSQL step의 inline `run` 21,574자가 플랫폼의 expression 21,000자 한도를 넘었다.
제품 테스트 실패나 Hosted 전체 실행으로 세지 않는다.

PostgreSQL core의 필수 **150개 항목**을 [별도 목록](../../scripts/ci/postgres-core-required.txt)으로 이동하고 shell array로 읽는다.
기존 전체 항목·순서를 보존하며 실제 shell에 전달된 bytes가 목록과 같은지 검사한다. Inline script 길이 검증도 추가했다.
새 회귀가 기존 source의 과대 block을 실제로 거부하는 negative control을 실행했고 CI Python **41 PASS**를 확인했다.
제품 Go source는 아래 e7b465a9 검증 이후 그대로이며 CI-only 변경으로 전체 DB/플랫폼 검증을 중복하지 않는다.
`automatic-history/ci-followup-python-receipt.json`, `postgres-inventory-extraction.json`, `workflow-length-negative-control.log`에
GitHub 진단·원본 전체 inventory·negative control·로컬 실행을 보관한다. 이후 Hosted 전체 통합은 여전히 GDJ-0099의 통합 milestone이다.

## GDJ-0099 — 자동 intermediary의 historical storage

2026-09-22, 기준 `c88c67746139875d025e005181489b86714e7b17` 위 제품·테스트·CI **47 non-Markdown 경로(Go 44)**를 변경했다.
검증한 전체 non-Markdown source 2,088파일의 정렬 path→SHA256 map hash는
`b2dd746f6dd26ebead7eb100fd26c5fb7eb9df1bd04aa74166272a5f99f3c121`이며 세 mode의 시작/종료에 불변을 확인했다.
최종 source map도 같은 hash이며 제품·테스트·CI의 사후 차이가 없다.

자동 intermediary의 Create/Add/Remove/Rename·reverse·자동 계획·forward SQL projection과 raw CreateModel의 columnless 선언을 연결했다.
`AutomaticManyToMany` capability는 변경 및 retained binding의 전체 graph/catalog 검증을 소유한다. 하나의 logical operation에서
정확한 IR storage 변경을 유도하고, DDL 전체와 최종 검증·이력/revision을 같은 transaction에서 완료한다. 파생 모델의 별도 공개 이력은 없다.
모델 authority·기존/새 이름·fan-out budget과 초기/최종 부재 검사는 한 step 중간에만 존재하는 table에도 적용된다.
자동 storage를 다른 관계가 참조하면 의존성 순서로 생성·역순 제거하며, owner의 저장 FK가 자신의 intermediary로 돌아오는 생성 순환은
모델 생성 뒤 AddField로 작성한다. 자동 계획은 준비되지 않은 FK·alias를 prefix 이후로 지연하고 모든 재개 prefix의 문서 bytes를 보존한다.

양 DB 각각 general/self × same/cross-app × pristine/empty-highwater/populated의 **12개 profile**에서 Add→Rename→재접속→역방향을 실행했다.
PK 0을 포함한 연결 행과 sequence 상태를 보존하며, SQLite는 table rootpage·sqlite_sequence의 부재/값,
PostgreSQL은 table/identity sequence OID·last_value/is_called도 보존한다. Managed pair index 또는 PK/FK/unique/sequence 이름을
새 table에 맞춰 변경하며, rename 뒤 native pair uniqueness와 FK 거부를 실제 쓰기로 확인했다.
Remove·reverse Add는 소유 link table만 제거하고 endpoint 행을 보존한다. Reverse Remove와 재적용은 빈 intermediary를 생성한다.

별도로 inline 자동/명시적 혼합 선언, 서로 참조하는 automatic storage의 역순 선언, Add→Rename→Remove→Add의 transient table,
Go 이름만 바꾸는 무-DDL rename, owner FK 추가/제거·SQLite remake의 retained link 보존을 확인했다.
실제 실행한 forward SQL body도 연결 PK를 유지하며 최종 소유 table inventory를 만든다.
Late native unique·관리 pair 누락/추가 index·대상 table/sequence 충돌·관리 밖의 inbound FK/view·취소의 **8개 실패 profile**을 양 DB에서 실행했다.
실패 뒤 원본 행·sequence·catalog와 이력/revision을 대조했다. Alias/FK가 옛 storage를 계속 참조하거나 ancestry·정확한 파생 모델이 없으면
history/graph 단계에서 거부한다. Nullable·nonunique·payload의 명시적 through 기존 회귀도 함께 유지했다.

첫 normal은 owner-remake 테스트 입력의 정규화된 FK column 누락에서 양 DB 각 1건 실패했다. 테스트 입력을 수정하고 기능 미지원
backend의 changed/retained capability 검증을 추가해 통과했다. 이어서 automatic storage 사이의 의존성 순서를 보강한 최종 Go source를 검증했다.
최초 실패 원본도 보관한다. 실패 실행의 순간 cleanup 관찰에 남은 연결 1건은 PASS 근거에 포함하지 않는다.
마지막 소유권 검토에서는 graph plan의 CreateModel storage inventory가 호출자의 metadata를 공유하는 결함을 실제 원본 변경으로 재현했다.
계획 확정 시 Before/After를 deep clone하도록 고치고, 이 회귀를 포함한 최종 source를 세 mode에서 다시 검증했다.
수정 전 실패는 `ownership-before-fix.log`에 남기며 앞선 통과를 최종 source의 검증으로 옮기지 않는다.

Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`에서
`./schema ./schema/ir ./internal/migrationgraph ./migrations ./migrations/backend ./migrations/definition ./internal/projectgenerate ./internal/migrationautodetect ./db/sqlite ./db/postgres`를 실행했다.
Normal·race·CGO=0 **각 10 package / 6,795 run=PASS / skip 0**이며 필수 30 root·기존/신규 CI 세부 profile을 합한
**142개 항목**을 세 mode 모두 확인했다. 실행 시간은 109.9/148.9/108.3초다. 직접 process helper만 제외하며 parent의 실제 process 회귀는 유지했다.
최종 세 전용 PostgreSQL DB는 연결·table·추가 schema 0을 확인한 뒤 삭제했다.
다섯 기존 project의 실제 CLI `generate --check`, 영향 vet와 gofmt/diff를 통과했다. CI 필수 목록은 relation/PostgreSQL 실행 owner에 배치하고,
core 소유 unit suite는 기존 portable owner를 유지한다. 필수 목록에 다른 owner의 test를 넣지 못하는 검증을 추가해 CI Python **39 PASS**를 확인했다.

Artifact `godj-many-to-many-reference-4sl0bvdp/automatic-history/`의 `latest-{normal,race,cgo0,supplementary}-path`,
`required-automatic-inventory.json`, `final-source.json`, `ci-python-receipt.json`에 source map·전체 JSON event·필수 inventory·stderr·hash·receipt를 보관한다.
이번 결과는 로컬 영향 검증이다. 일반 collection manager/query·Ticket 컬렉션 소비자와 GDJ-0099의 Hosted 전체 통합은 남아 있다.
최근 Hosted 전체 source는 GDJ-0098의 `93e77bd9`이며 이후 변경에 해당 성공을 전이하지 않는다.

## GDJ-0099 — 명시적 through의 columnless migration

2026-09-22, 기준 `a9b8e3a2acd83d07d1f2534f40359d0eab5050d9` 위 제품·테스트·CI **44 non-Markdown 경로(Go 42)**를 변경했다.
전체 non-Markdown source 2,077파일의 정렬 path→SHA256 map hash는
`3e0ecf27a52634cf7111f697b2daf38499979e65e8b6f6820cd8e82b9426b614`다. 세 mode의 시작/종료 source 불변을 확인했다.

`AddManyToMany`, `RemoveManyToMany`, `RenameManyToMany`는 columnless 선언·완전한 through binding·삽입 anchor를 역사에 보관한다.
이름 변경은 이름/Go 이름만 바꿀 수 있으며 target·reverse·symmetry·through FK 선택을 바꾸지 않는다. Definition의 closed shape·
중복 key·사전 byte/node 한도·semantic digest·deep copy와 최적화된 historical 재구성을 연결했다. 기존 scalar/FK/constraint operation도
retained ManyToMany 의미를 변경할 수 없다. 잘못된 endpoint 선택·없는 target/through·reverse namespace·dependency ancestry·stale removal을 거부한다.

명시적 through의 선언 변경은 양 DB에서 DDL 없이 실행한다. Owner에 직접 FK가 없어도 연결 모델·두 endpoint·transitive FK를 모두
sealed graph에 포함해 실제 catalog와 이력/revision을 검증한다. 변경과 retained binding 검증에는 `ExplicitManyToMany` capability가 필요하다.
SQLite의 seal도 중첩 through 의미를 포함한다. SQL projection은 이 metadata operation의 빈 statement group을 검증하며 임의 DDL을 허용하지 않는다.

자동 계획은 모델과 선택한 FK를 먼저 생성한 뒤 선언을 추가한다. 같은 앱/서로 다른 앱의 순환 의존성, cold 생성·기존 모델 추가·
이름 변경·제거·중간 anchor를 검사하고 모든 durable prefix에서 남은 문서 bytes가 같음을 확인했다. 모호한 rename·retained 순서 변경·
binding retarget을 임의의 삭제/추가로 바꾸지 않는다. Raw CreateModel의 columnless 선언과 자동 intermediary DDL은 아직 명시적으로 거부한다.

양 DB에서 nullable/non-null × pair unique 유/무 × 일반/self × same/cross-app의 **16개 실제 저장 profile** 각각에 대해
Add→Rename→Remove→역방향→재적용과 connection close/reopen 후 역방향을 실행했다. 연결/endpoint 행·중복/null·payload·PK·
sequence·physical catalog를 보존한다. SQLite schema version/rootpage/FK 설정과 PostgreSQL catalog/sequence 상태도 대조한다.
늦은 native unique 실패, through/endpoint catalog drift, 취소는 변경 state나 부분 history를 게시하지 않는다.
첫 normal은 새 capability를 반영하지 않은 기존 구조 inventory(8→9) 하나에서 실패했다. 실제 신규 DB 동작은 통과했으며,
해당 기대를 갱신하고 cross-app 저장 profile·retained binding capability 검증을 포함해 최종 source를 다시 검증했다. 최초 실패 원본도 보존한다.

Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`에서
`./schema ./schema/ir ./internal/migrationgraph ./migrations ./migrations/backend ./migrations/definition ./internal/projectgenerate ./internal/migrationautodetect ./db/sqlite ./db/postgres`를 실행했다.
normal·race·CGO=0 **각 10 package / 6,712 run=PASS / skip 0**이며 필수 15 root와 CI 필수 세부 profile을 합한 **55개 항목**을
각 mode에서 모두 확인했다. 실행 시간은 각각 48.7/90.6/51.3초다. Process helper의 직접 실행만 제외하고 parent의 실제 process 회귀는 유지했다.
다섯 기존 project의 실제 CLI `generate --check`와 영향 vet를 통과했다. 이 검사와 동시 실행한 고정 Go source 이후 차이는 CI 필수
목록 두 경로뿐이며 비교 map을 보관했다. CI Python 전체 **38 PASS**와 Markdown 링크/diff 검사도 통과했다.

Artifact `godj-many-to-many-reference-4sl0bvdp/explicit-history/`의 `latest-{normal,race,cgo0,supplementary}-path`,
`required-history-inventory.json`, `ci-python-receipt.json`에 source map·전체 event·필수 inventory·stderr·hash·receipt를 보관한다.
세 전용 PostgreSQL DB는 연결·table·추가 schema 0을 확인한 뒤 삭제했다. 향후 Hosted relation/PostgreSQL owner의 필수 항목에도
명시적 through의 실제 root/세부 profile을 등록했다. 이 로컬 결과를 Hosted 실행으로 세지 않는다.

자동 through의 Create/Add/Remove/Rename·reverse·retained identity DDL, collection manager/query·Ticket 컬렉션 소비자와
GDJ-0099의 Hosted 전체 통합은 남아 있다. 전체 플랫폼의 최신 검증 source는 아래 GDJ-0098의 `93e77bd9`이며 이번 변경에 전이하지 않는다.

## GDJ-0099 — Columnless 선언·storage projection·생성 metadata

2026-09-22, 기준 `b4094c25b78b2ef1f2033b2c308a1464d62b48bf` 위 Go **40경로**를 변경했다.
전체 non-Markdown source 2,062파일의 정렬 path→SHA256 map hash는
`e30f4e8fe0d1cb7d46e1f006cbe43df221335e9f3c65483f2f395227a1ac00a3`이며 모든 mode의 시작/종료에 불변을 확인했다.

`Model.ManyToMany`는 저장 `Fields`와 분리된 선언이다. Normalization·clone·equality·hash가 target·reverse·symmetry·명시적 through의
모델과 두 FK 선택을 보존한다. `StorageSchema`는 자동 source/target CASCADE FK와 named pair unique 모델을 결정적으로 유도하며
모델명·Go명·table 충돌을 거부한다. 명시적 through의 nullable FK·추가 필드·pair unique 부재를 다른 storage로 바꾸지 않는다.
기본 self symmetry와 directed reverse를 구분하고, 잘못된 target/through FK와 reverse namespace 충돌은 partial binding 없이 거부한다.

생성기의 logical schema companion과 파생 storage descriptor를 나누고 프로젝트 binding은 같은 IR projection을 사용한다.
별도 실제 Go module에서 cross-app 자동/명시적 through·symmetrical/directed self의 네 선언, owner의 저장 컬럼 불변,
자동 link descriptor·기존 payload through binding·metadata accessor 분리와 stale descriptor 거부를 실행했다.
두 번 생성한 전체 bundle의 bytes와 snapshot은 같았으며 실패한 through 선택에는 생성 prefix를 반환하지 않는다.
부모는 child JSON event에서 필수 실제 test의 run/pass 각각 1회와 네 package의 완료를 검사한다. 테스트 파일이 없는 세 generated
package의 package-level skip은 컴파일 완료로 확인하고, test-level skip은 거부한다. Child도 부모의 race/CGO mode를 계승한다.

Project wire는 closed shape·중복 key·필수 field·escaped byte 한도와 deep snapshot을 보존한다. Resource admission은 clone/생성 전에
columnless field와 자동 storage의 model·3개 column·FK/constraint node·파생 이름까지 센다. Migration intent의 문자열/field/node 예산도
ManyToMany metadata를 포함한다. 기존 scalar-only 입력은 기존 생성물과 hash를 그대로 유지한다.

Historical storage 변경은 아직 미구현이다. Definition encoding, 일반/최적화된 historical 재구성, 자동 계획과 native Create가 이를
관계 정보 없이 처리하지 않고 명시적으로 거부한다. 첫 normal에서 최적화된 재구성이 일반 Operation.stateForward를 거치지 않는
경계를 발견해 공통 normalizedSingleModel에 검증을 연결했다. 첫 generated fixture의 잘못된 `post` identity도 실제 `blog_post`로
수정했다. Child inventory는 테스트 없는 package와 실제 test skip을 구분하도록 정리했다. 최초 실패 원본을 보존하며 위 수정 뒤 검증했다.

Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`에서
`./schema ./schema/ir ./codegen ./codegen/consumertest ./orm ./internal/irresource ./internal/projectspec ./internal/projectwire ./internal/migrationgraph ./migrations ./migrations/definition ./internal/projectgenerate ./internal/migrationautodetect ./db/sqlite ./db/postgres`를 실행했다.
normal·race·CGO=0 **각 15 package / 7,695 run=PASS / skip 0**이며 필수 15개 root와 전체 시작/종료 inventory를 확인했다.
Process helper의 직접 실행만 제외하고 필요한 parent의 실제 process 회귀는 유지했다.
Helpdesk·Article·relationfixture·onetoonefixture·cascadefixture의 실제 CLI `generate --check`, 영향 vet와 CI Python **13 PASS**도 확인했다.
Artifact는 `godj-many-to-many-reference-4sl0bvdp/declaration/latest-{normal,race,cgo0,supplementary}-path`에 source·event·stderr·hash·receipt를 보관한다.
각 전용 PostgreSQL DB는 연결·table·추가 schema 0을 확인하고 삭제했다.

이는 선언·metadata의 로컬 checkpoint이며 실제 ManyToMany migration·조회·manager·Ticket 컬렉션 소비자와 Hosted 통합은 남아 있다.
선행 native insert source `b4094c25`의 [PR feedback](https://github.com/progresshans/godj/actions/runs/35695590914)은 실제 checkout과
Fast Go step의 success를 확인했으나 위 새 source나 full-platform 검증으로 옮기지 않는다.

## GDJ-0099 — 독립 ManyToMany 기준과 native conflict insert

2026-09-22, 기준 `3eb403e718f511ac06dc457b41a5e50a41ec8890` 위에서 ManyToMany 준비와 native 삽입을 연결했다.
[독립 runner](../../conformance/runners/django/many_to_many_reference.py)는 GoDj·fixture를 읽지 않는 public model/ORM/migration 입력이다.
Django 6.1·Python 3.14.3·SQLite 3.50.4와 psycopg 3.3.6·격리 PostgreSQL 17.5에서 hash seed 0/813의 **29개 관찰**이 동일했다.
Runner SHA256은 `ea90292bcf269bd0298ddcdc504beb3d701d1301041fd9155788eec381db5aa3`이며 각 Django module source hash를 fixture에 저장했다.
자동/명시적 through·두 연결의 동시 중복 add·set retained identity/payload·늦은 INSERT/iterable/FK 오류 rollback,
대칭/비대칭 self·multiplicity·독립 cache/held query·실제 historical Add/Rename/reverse/reapply·signal을 포함한다.
저장한 두 DB 기준과 실제 SQLite를 비교하는 Python **6 tests PASS / skip 0**이며 set(clear=True)와 자기 관계의 symmetry를 실제로
변경해 관찰이 달라지는 두 negative control을 실행했다. 이는 GoDj ManyToMany 동등성 PASS가 아닌 독립 기준 확보다.

`ConflictInsertPlan`은 immutable assignment/ordered target과 명시한 non-null unique tuple을 사용한다. Backend와 ordinary/relation/
coordinated session의 `ConflictInserter`는 그 tuple에만 `ON CONFLICT ... DO NOTHING`을 적용한다. Native 0/1행을 반환하고
생성 PK나 SQLite의 이전 LastInsertId를 읽지 않는다. Plan의 완전한 assignment·field type·nullability·중복·식별자를 I/O 전에 검사한다.
양 DB의 실제 두 connection pool에서 같은 pair를 동시에 삽입하면 한 번만 true이고 정확히 한 연결이 남는다. Pair 중복 후 같은
transaction의 다음 쓰기도 성공하며 retained row ID/payload를 유지한다. 다른 unique·FK·NOT NULL·CHECK와 없는 conflict target은 오류다.
두 DB에서 trigger가 INSERT를 생략한 0행 결과도 확인했다. False 자체를 membership 증명으로 세지 않으며 일반 Insert의 1행 요구는 그대로다.

네 transaction 경로 각각의 commit·늦은 unique 실패 rollback·취소·deferred FK COMMIT 오류, 만료 session과 재접속 보존을 실행했다.
Driver/metadata 오류는 성공 결과를 반환하지 않으며 callback/statement를 자동 재시도하지 않는다. Commit unknown과 rollback error는
원인을 보존한다. SQLite의 discard 확정과 종료 미확정·연결 격리를 분리해 검사했다. Compiler SQL·인자/identifier 검증과 AST 소유권도 포함한다.
PostgreSQL 실제 owner의 필수 목록에 새 product root를 연결했다. 아직 이 primitive를 통한 일반 ManyToMany 선언/manager·소비자는 구현 전이다.

Artifact는 `godj-many-to-many-reference-4sl0bvdp/`의 `latest-capture-path`와 `conflict-insert/latest-{normal,race,cgo0,supplementary}-path`가
가리킨다. 양 DB reference의 stdout/stderr·source hash·정리 영수증과 각 Go checkpoint의 전체 inventory·필수 58개 항목·source map을 저장한다.
제품·검사·workflow **17개 non-Markdown 경로(Go 12)**를 변경했다. 전체 non-Markdown source 2,046파일의 정렬 path→SHA256 map hash는
`8ff9f65ea3a81ca8069de9445db5f7c9c0c88399548bcad0322a861e7b14c41d`다. 모든 mode의 시작/종료 source 불변을 확인했다.
Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`에서
`./query ./db/internal/queryplan ./internal/conflicttest ./db/sqlite ./db/postgres ./orm`을 전체 실행했다.
normal·race·CGO=0 **각 6 package / 6,412 run=PASS / skip 0**, 필수 58개 항목과 전체 시작/종료 inventory를 확인했다.
직접 실행용 PostgreSQL revision helper만 제외하며 필수 parent의 실제 process 회귀는 유지했다.
영향 go vet와 CI Python **13 PASS**도 통과했다. 이번 변경은 generated API를 바꾸지 않아 generated drift를 새로 실행하지 않았다.
최초 normal은 새 테스트의 PostgreSQL 식별자와 SQLite 연결 discard 기대가 기존 계약과 달라 실패했다. 기존 규칙을 확인해
잘못된 기대를 고치고, rollback 실패를 discard 확정/미확정으로 나누어 위 세 mode를 완료했다. 최초 실패 원본도 보존한다.
Compile 중 fault callback의 `[]any` signature 오류도 수정했다. Reference·모든 mode의 전용 DB는 연결·table·추가 schema 0 확인 후 삭제했다.
이번 범위는 native 삽입 기반의 로컬 checkpoint다. GDJ-0099의 normalized relation·manager·컬렉션 소비자와 Hosted 통합은 남아 있다.
선행 source `93e77bd9`의 Hosted full을 이번 변경의 결과로 옮기지 않는다.

## GDJ-0098 — CASCADE·TicketLabel Hosted 전체 통합 완료

2026-09-22, source `93e77bd9c19d6e7b137de3a068c40a403970e73d`의
[Hosted full 35689549739](https://github.com/progresshans/godj/actions/runs/35689549739)은 최종 **62/62 success**, `full_platform_verified=true`다.
고정 source의 workflow에서 기대한 전체 job 좌표를 별도로 계산하고 누락·중복 없이 각 실제 checkout SHA와 최종 full scope 집계를 확인했다.
Portable Go·relation·project-check·command·conformance·exact Darwin·Python compatibility·PostgreSQL의 필수 owner가 모두 성공했다.

첫 attempt의 Intel Mac normal project-check는 Go 설치 중 `github.com`/`go.dev` DNS 오류로 제품 테스트 전에 실패했다.
성공한 60개 작업을 유지하고 실패한 작업과 dependent 집계만 같은 source로 한 번 재실행했다. 최신 API가 새 job ID를 부여한 60개 작업은
실행 시작 시각과 로그 bytes가 이전 attempt와 같음을 대조했다. 재실행된 Intel job은 실제 product step과 clean worktree 검사까지 성공했다.
최초 실패 로그·attempt metadata와 재실행 영수증을 보존하며 환경 실패를 제품 PASS로 바꾸지 않았다.

PostgreSQL 17.10 core는 normal/race/CGO=0 **각 13 package / 2,322 run=PASS / skip 0**,
operator-target은 **각 2 package / 12 run=PASS / skip 0**다. Intel Mac relation race는 **36 package / 5,502 run=PASS / skip 0**다.
Exact Darwin/arm64 Python은 **324 PASS / skip 0**이며, Python 3.12.13·3.13.15·3.14.3·3.14.7 compatibility는 각 324개 발견 중
exact-profile 전용 4개를 명시적으로 위임해 **320 실행 / 4 delegated skip**이다. 네 항목 모두 exact owner의 실제 `ok`를 확인했다.
Project-check의 PostgreSQL runserver 1개 skip도 DB URL이 있는 위 PostgreSQL core owner의 필수 no-skip 실행에서 확인했다.
Cold external CLI build는 선언된 Linux amd64 normal/full owner에서 성공했다. 비대상 좌표의 선택적 step을 추가 실행으로 세지 않는다.

Artifact `godj-cascade-reference-rusrn8sm/ticket-labels/`의 `hosted-full-closeout-audit.json`, `hosted-retry-reuse-audit.json`,
`hosted-35689549739/log-audit.json`, `hosted-35689549739/attempt-1/`에 전체 inventory·scope·checkout·위임 owner·로그와 실패 원본을 보관했다.
이 결과로 GDJ-0098의 CASCADE와 명시적 TicketLabel 소비자를 완료한다. 이후 ManyToMany reference 준비나 제품 변경의 검증으로 옮기지 않는다.

## GDJ-0098 — TicketLabel 소비자와 복수 권한 경계

2026-09-22, 기준 `86f0571b4d162c8f8523d1e1ba0ce2ce979dd419` 위 제품·생성물·검사·CI **63경로**(Go 58)를 변경했다.
Markdown을 제외한 전체 source 2,031파일의 정렬된 path→SHA256 map hash는
`49796be85c21ecab6f1d3638e57570f24e8ec86ba6edd98847ba22a98452b866`이다. 세 mode 모두 시작/종료 source 불변을 확인했다.
Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

`0019_ticket_label`은 ticket/label CASCADE FK와 named ordered pair unique를 추가한다. Category는 endpoint의 서버 배정 값으로
양쪽을 검사하며 연결 모델에 중복 저장하지 않는다. 이전 Ticket/Label 행의 forward/reverse/reapply 보존, native FK·unique와 재접속을 확인했다.
Scoped Form/Admin/API의 CRUD·두 chooser의 escaped label·양쪽 Category·교차 Category ORM 행의 비노출·전체 후보의 중복·PATCH 생략/self
검사를 연결했다. API는 25개 operation과 TicketLabel의 5개 named schema를 같은 선언에서 생성한다.
Label 삭제도 project 관계 삭제기를 사용하고 Ticket DELETE API를 추가했다. 실제 양 DB에서 반대 endpoint·다른 링크 보존,
ServiceReport PROTECT가 링크 삭제보다 먼저 실행되는 동작, 삭제 중 오류의 전체 rollback을 확인했다.

API Authentication.Require는 primary와 추가 권한을 AND로 평가한다. 권한 목록의 한도·정규형·중복·입력 slice 복사와
Session/Bearer의 한 번 인증·CSRF 경계, 누락 grant·custom authorizer 거부·오류·취소 시 handler 미실행을 검증했다.
TicketLabel POST/PUT/PATCH에는 링크 mutation 권한과 ViewTicket·ViewLabel이 모두 필요하며 파싱·DB 이전에 검사한다.
Read/delete는 링크 ID 권한이고 endpoint 이름을 게시하지 않는다. 기존 ServiceReport의 explicit-ID 입력 계약은 그대로다.
OpenAPI의 `x-godj-additional-permissions`와 실제 binding의 일치를 검사한다. 문자열 필드가 없는 Admin 모델은 SearchFields 없이
등록하고 검색 UI를 생략하며 미선언 q와 직접 registry Search를 callback 이전에 거부한다.

사전/native 중복·두 번째 endpoint 조회 실패·취소·driver/reload 실패·transaction 직전 endpoint Category 변경은 부분 쓰기를 남기지 않는다.
조회 실패 뒤 먼저 수집한 일부 validation 결과도 게시하지 않는다. 불확실한 commit은 실제 commit 후 500·한 번 실행이고,
불확실한 rollback/protection은 정상 404/validation으로 변환하지 않는다. 외부 ORM/SQL의 Category 재할당을 막는 영구 cross-table
제약이나 행 잠금은 이 소비자의 계약에 포함하지 않는다. 일반 ManyToMany manager도 아직 구현한 것으로 세지 않는다.

`go test -json -count=1 -timeout=12m -skip '^(TestPostgresRevisionFenceHelperProcess|TestPublicationCrashHelper)$'`로
`./auth ./api ./api/bearerauth ./api/sessionauth ./api/openapi ./api/openapi/consumertest ./admin ./examples/helpdesk ./examples/article/apiapp ./internal/projectgenerate ./internal/migrationautodetect`를 실행했다.
normal·`-race`·`CGO_ENABLED=0`은 **각 11 package / 652 run=PASS / skip 0**이다. 새 권한·검색 root와 양 DB의 historical_ticket_label/
ticket_labels 및 기존 Label/ServiceReport, 독립 client를 포함한 필수 18개 항목과 모든 시작/종료 inventory를 확인했다.
Publication helper는 required parent가 별도 process로 실행하며 직접 helper를 skip한 결과를 성공으로 세지 않았다.
독립 고정 ogen module은 세 profile의 생성물 drift·lock 불변을 확인한 뒤 실제 HTTP를 실행한다. Link CRUD·권한·CSRF·PROTECT·양쪽
CASCADE 뒤 유지 링크를 검증하고 부모가 최종 Ticket/Label/Category와 외부/유지 링크를 DB에서 독립 확인한다. Article 문서 두 개는 byte 불변이다.

Helpdesk CLI `generate --check`, 영향 `go vet`, Authentication interface 소비자의 conformance runner compile을 통과했다.
Conformance protocol **904 run=PASS / skip 0**, CI package/scope Python **13 PASS**도 확인했다.
최초 normal은 새 Admin 테스트가 기존 302 redirect를 303으로 잘못 기대하여 실패했다. 기대값을 기존 계약과 맞춘 뒤 위 세 mode를 통과했다.
실패 원본을 포함한 artifact는 `godj-cascade-reference-rusrn8sm/ticket-labels/`의 `latest-normal-path`, `latest-race-path`,
`latest-cgo0-path`, `latest-supplementary-path`, `latest-openapi-path`가 가리킨다. Source·events/stderr·필수 inventory·hash·receipt를 보존했다.
전용 PostgreSQL DB는 각 실행 후 다른 연결·table·추가 schema 0을 확인하고 삭제했다.
이 checkpoint는 소비자의 영향 범위 검증이다. CASCADE 기반과 이 소비자를 합친 새 source의 Hosted full 통합은 다음 milestone이다.

## GDJ-0098 — 공통 CASCADE collector와 transitive generated binding

2026-09-22, 기준 `9d95061f0066a22ddd7adeaebd98b0bc6ec799c0` 위 제품·생성물·검사·CI **97경로**(Go 86)를 변경했다.
Markdown을 제외한 전체 source 2,021파일의 정렬된 path→SHA256 map hash는
`18ea3fb06292ac953c7b603172b37324713fb87f6c919d41671e37f04895acd6`이다. 모든 mode에서 시작/종료 source 불변을 확인했다.
Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

공통 ORM은 바인딩에서 CASCADE로 도달할 model과 모든 incoming 정책을 고정한다. 호출별 반복 탐색과 model+PK 집합으로 실제 행을
한 번 수집하고, 도달한 모든 PROTECT 검사와 row/error/close 처리가 끝난 뒤 SET_NULL·각 key 삭제를 한 AtomicRelation에서 실행한다.
이미 CASCADE로 수집된 행의 PROTECT도 적용한다. 각 DELETE는 정확히 한 행이어야 하며 반환값은 root와 후손의 총수다.
기존 callback의 단일 동기 실행·오류 보존·확인된 commit 뒤 root PK 게시 계약을 유지한다.

Generated policy v2는 직접 incoming에 더해 CASCADE 후손의 policy·table·PK·FK column·cardinality·nullability를 포함한다.
Grandchild의 각 의미를 변경하면 root fingerprint도 달라지고 이전 fingerprint의 binding은 I/O 전에 거부된다.
관계없는 Node의 변경은 root fingerprint에 들어가지 않는다. 고정 두-edge graph의 새 golden은 별도로 작성한 length-framed SHA256과 대조했다.
기존 네 project를 재생성했고 새 [CASCADE fixture](../../conformance/cascadefixture/)도 실제 typed model·descriptor·project binding으로 생성했다.

양 DB에서 독립 Django의 **13개 관찰**과 실제 삭제 total·model별 감소·잔존 행을 대조했다. 재귀 CASCADE+SET_NULL, 보호된 후손과 overlap,
중복 경로, 숨긴 역관계와 OneToOne, 자기 loop·nullable/required 순환, 다른 root/observer 보존, 늦은 실패 rollback과 raw delete의 native FK 거부를 포함한다.
ManyToMany의 연결 정리 관찰은 명시적 RootLabels와 named tuple 제약으로 비교했다. 중복 연결은 실제 native unique 오류로 거부하며,
일반 ManyToMany 선언·add/remove/set manager의 구현이나 동등성으로 세지 않는다. Required 순환의 101/202 key 입력만 native fixture SQL을 쓴다.
GoDj의 generated-key 직접 할당은 이 변경의 지원 범위에 추가하지 않는다.

별도 Go-native 검사는 먼저 발견한 보호 행이 있어도 후손 query/scan/iteration/close 실패·nil/wrong-key 행·취소에서 부분 보호 진단과 쓰기를
게시하지 않음을 확인한다. 늦은 DELETE·잘못된 row count·SET_NULL 실패와 commit/rollback 불확실성은 caller와 원인을 보존하고 자동 재시도하지 않는다.
실제 양 DB에서도 commit 성공 뒤 uncertainty를 주입하면 DB의 전체 삭제는 남고 caller PK는 유지된다. 실제 rollback 뒤 uncertainty,
SET_NULL·후손 삭제 이후의 취소는 mutation prefix를 되돌리고 caller를 보존한다. 각 경우 AtomicRelation은 한 번만 실행했다.

`go test -json -count=1 -timeout=12m -skip '^(TestPostgresRevisionFenceHelperProcess|TestPublicationCrashHelper)$'`로
`./internal/relationpolicy ./orm ./codegen ./codegen/consumertest ./conformance/cascadefixture ./conformance/onetoonefixture ./conformance/relationfixture ./conformance/relationproduct ./examples/article ./examples/helpdesk ./db/sqlite ./db/postgres ./internal/projectgenerate ./internal/migrationautodetect`를 실행했다.
normal·`-race`·`CGO_ENABLED=0`은 **각 14 package / 6,971 run=PASS / skip 0**이다. 필수 항목 53개에는 양 DB의 13개 비교와
각 3개 native 실패 경계가 포함되며 모든 시작/종료 inventory를 검사했다. Process helper 자체의 직접 실행만 제외하고 실제 parent 회귀는 유지했다.
외부 module generated 소비자·공개 typed API 오용 거부·기존 PROTECT/SET_NULL·Article/Helpdesk와 publication/process 회귀도 같은 범위에 포함된다.

다섯 project의 실제 CLI `generate --check`와 영향 `go vet`를 통과했다. CI는 새 fixture의 package별 단일 owner를 유지하고,
relation 필수 목록 29항목·PostgreSQL 필수 목록 22항목을 추가했다. CI Python 검사는 **38 PASS**이며 PostgreSQL selector의 필수 root 51개가
실제 `go test -list`에 잡히는지 확인했다. 목록 검사는 Hosted 실행 결과로 세지 않는다.
첫 준비 source의 normal은 6,965 PASS였고, native 불확실성/취소 검사와 CI 연결을 보완한 위 최종 source로 세 mode를 다시 실행했다.
편집 중 새 test의 nullable pointer·Update/patch API와 outcome code 사용 오류는 compile 단계에서 수정했다.

Artifact는 `godj-cascade-reference-rusrn8sm/orm/`의 `latest-normal-path`, `latest-race-path`, `latest-cgo0-path`,
`latest-generate-path`, `latest-ci-check-path`, `latest-supplementary-path`에 source·전체 events/stderr·필수 inventory·log hash·receipt를 보관한다.
각 전용 PostgreSQL DB는 다른 연결·table·추가 schema 0을 확인한 뒤 삭제했다.
이는 CASCADE 공통 구현의 영향 범위 로컬 검증이다. TicketLabel의 Form/Admin/API/OpenAPI/client와 해당 Hosted 전체 통합은 남아 있다.
선행 native source `9d95061f`의 [PR feedback](https://github.com/progresshans/godj/actions/runs/35684055578)은 필수 Fast Go step까지 success였으며,
그 결과를 위 새 ORM source나 full-platform PASS로 옮기지 않는다.

## GDJ-0098 — CASCADE 선언·historical 정책 변경과 native FK 기반

2026-09-22, 기준 `8415fcee7f94008a97c6a440a7a5128aadb3c8a2` 위 Go **29경로**를 변경했다.
Markdown을 제외한 전체 source 1,995파일의 정렬된 path→SHA256 map hash는
`c98ff8c53c6226625b66a96467861aa94ad1b69c939c6fe54206083e07215031`이며 실행 전후 불변을 확인했다.
Go 1.26.5/Darwin arm64·modernc SQLite·격리 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

CASCADE는 required/nullable FK·OneToOne 선언과 schema hash·생성 metadata·project wire·strict Create/Add/Alter history에 남는다.
정책 변경은 FK target/column/default/nullability 등 다른 facet을 흡수하지 않는다. 잘못된 policy wire는 부분 history도 게시하지 않는다.
자동 계획의 required CASCADE Add 누락을 수정했고, 실제 cross-app cycle 계획의 모든 durable prefix에서 남은 migration의 정확한 bytes와 최종 상태가 일치한다.

양 DB는 CASCADE FK를 NO ACTION·DEFERRABLE INITIALLY DEFERRED로 생성한다. Create/Add·PROTECT→CASCADE→역방향과
nullable/required·OneToOne 결합 변경·재접속을 실제 catalog와 삭제 timing으로 대조했다. 기존 PROTECT/SET_NULL은 즉시 검사를 유지한다.
SQLite의 sealed remake는 retained FK·column/named unique·행·sequence high-water를 보존하며 다른 FK를 제거해도 CASCADE가 남는다.
DB-free SQL body를 별도 private connection lifecycle에서 실행하여 sequence의 부재·빈 테이블의 값·현재 최대 PK보다 큰 값을 각각 보존함을 확인했다.
물리 timing만 바꾼 대조는 다음 schema 변경 전에 drift로 거부되고 전체 catalog·행·history가 불변이다.
늦은 unique 실패 뒤 timing 변경도 rollback되며 명시적 데이터 수정 후 재시도/역방향이 성공한다.
Required 순환을 실제로 생성·삭제하고 역방향 migration도 실행했다. 일반/관계 transaction에서 orphan insert는 COMMIT 전 성공하지만
deferred COMMIT은 native FK 원인과 commit-outcome-unknown을 보존한다. 같은 pool에는 pending writes가 남지 않고 새 transaction이 성공한다.

`go test -json -count=1 -timeout=12m -skip '^TestPostgresRevisionFenceHelperProcess$'`로
`./schema ./schema/ir ./migrations ./migrations/backend ./migrations/definition ./internal/migrationautodetect ./internal/projectwire ./codegen ./db/sqlite ./db/postgres`를 실행했다.
normal·`-race`·`CGO_ENABLED=0`은 **각 10 package / 6,658 run=PASS / skip 0**이다. 새 필수 root 17개와 모든 시작/종료 inventory를 검사했다.
직접 실행 대상에서 뺀 process helper는 실제 cross-process parent가 실행하며, 그 parent도 각 mode에서 PASS다.
같은 source의 CLI로 네 project `generate --check`와 영향 `go vet`도 통과했다.

첫 normal 실행은 새 test의 cross-app 초기 history에 creator dependency를 빠뜨려 실패했다. 정상 초기 계획을 사용하도록 test를 수정했고
그 실패 원본은 보존했다. 최종 세 mode는 위 동일 source로 다시 실행했다.
Artifact는 `godj-cascade-reference-rusrn8sm/native/`의 `latest-normal-path`, `latest-race-path`, `latest-cgo0-path`,
`latest-supplementary-path`에 source·전체 events/stderr·필수 inventory·log hash·receipt를 저장했다.
전용 PostgreSQL DB는 각 실행 뒤 다른 연결·table·추가 schema 0을 확인하고 삭제했다.
이 범위는 CASCADE의 native/historical 기반 검증이다. 공통 ORM collector·transitive fingerprint·generated project 삭제·TicketLabel 소비자는 미완료이며,
새 source의 Hosted/full-platform 결과로 기록하지 않는다.

## GDJ-0097 — 복합 고유성·Label의 Hosted 전체 통합 완료

2026-09-22, source `231260c5116bb7cfe157cab54ceb404e05c8ba43`의
[Hosted full run 35678713385](https://github.com/progresshans/godj/actions/runs/35678713385)은 **62/62 job success**로 종료했다.
그 source의 workflow에서 matrix를 전개해 기대 이름 62개를 계산하고 실제 이름의 누락·중복·추가가 없음을 확인했다.
각 job의 전체 로그에서 실제 checkout SHA를 대조했다. 최종 job `106599463713`의 실행 출력은
`scope=full`, `full_platform_verified=true`이며 command·conformance·exact Darwin·portable Go·PostgreSQL·project check·Python compatibility·relation의
필수 owner 8개를 모두 포함한다.

PostgreSQL 17.10 core normal/race/CGO=0은 **각 13 package / 2,292 run=PASS / skip 0**,
operator-target은 **각 2 package / 12 run=PASS / skip 0**이다. Named constraint 10개와 Label historical/HTTP 하위 검사를 포함한
현재 source의 필수 선택·inventory 검사를 실행했다. Intel macOS relation race는 **31 package / 5,443 run=PASS / skip 0**이며,
exact Darwin Python은 **318 tests / skip 0**이다. 나머지 platform·mode, generated/외부 소비자·cold CLI·same-run capture와 최종 gate도 완료했다.

원본은 `godj-composite-uniqueness-9yzkn6xp/labels/hosted-35678713385/`의 job별 전체 log와 `log-audit.json`에
source·로그 hash·inventory·기대 roster·최종 scope 판정을 보관한다. 첫 run의 compile 실패와 대체 취소는 별도 실패 이력으로 유지한다.
GDJ-0097을 완료 처리한다. 이후 commit의 CASCADE 독립 reference 및 진행 중인 native/ORM 변경은 이 full 결과에 포함되지 않는다.

## GDJ-0098 — CASCADE의 독립 기준과 재귀 삭제 설계

2026-09-22, 기준 `911740f8bc68469160dd6c2ebe7ca94104bdf536`에서 직접 작성한
[독립 runner](../../conformance/runners/django/cascade_reference.py)의 SHA256은
`45cc9605304fc001d1802e49f9efdb433420f6d722c47d93eb39374fe546c2e6`이다.
GoDj나 기대 fixture를 읽지 않고 고정 Django 6.1의 public model·ORM과 실제 SQL을 실행했다.
Python 3.14.3·SQLite 3.50.4 및 별도 PostgreSQL 17.5 DB·psycopg 3.3.6에서 hash seed 0/813을 각각 실행하여
**13개 관찰**의 양 DB 결과·source fingerprint 일치를 확인했다. FK 물리 metadata는 별도 backend 값으로 보존한다.

Recursive CASCADE·SET_NULL의 observer/다른 root 보존, 보호된 후손의 쓰기 전 거부와 CASCADE/PROTECT overlap,
중복 경로·숨긴 역관계·OneToOne, 자기 loop·같은 모델/서로 다른 앱의 nullable/required 순환을 포함한다.
Native FK는 raw root delete를 거부하고, 늦은 실패를 주입하면 이미 실행한 삭제와 SET_NULL도 rollback하며 caller PK가 유지된다.
ManyToMany의 실제 intermediary pair는 중복되지 않고, endpoint 삭제는 링크를 정리하면서 다른 endpoint·링크를 보존했다.

체크인한 runner·fixture의 Python 3.12.13/3.13.15/3.14.3/3.14.7 집중 검사는 **각 6 PASS / skip 0**이다.
모든 발견 test의 시작/종료 inventory를 확인했고 버전별 실제 Python/SQLite 값만 구분했다.
숨긴 FK를 실제 CASCADE에서 SET_NULL로 바꾼 대조는 삭제 결과가 3행에서 2행으로 변함을 확인한다.
Fixture의 JSON 비교만으로 결과를 생성하지 않았음을 실제 실행으로 대조했다.

Artifact는 `godj-cascade-reference-rusrn8sm/`의 `latest-capture-path`, `latest-profile-path`에 source hash·전체 출력·receipt를 저장했다.
전용 PostgreSQL DB는 남은 연결·table·추가 schema 0을 확인한 뒤 삭제했다. 초기 준비 probe의 SQL 대소문자 비교 오류와
수정 전 실패는 보존했으며 최종 13개 관찰의 성공에 합치지 않는다.
이 단계는 독립 기준과 [삭제 설계](../adr/0074-cascade-delete-graph-and-constraint-timing.md) 채택이다.
이 reference checkpoint 당시 GoDj의 CASCADE enum·물리 FK·collector·TicketLabel 소비자는 구현 전이었다. 이후 native 기반 결과는 위 별도 항목이 소유한다.
GDJ-0097의 Hosted 결과로 CASCADE source를 검증하지 않는다.

## GDJ-0097 — Hosted 통합에서 발견한 외부 backend compile 경계 수정

2026-09-22, source `fdddfae8a58b0d6cbf6d10a40bfeb878acaf7037`의 [첫 Hosted full](https://github.com/progresshans/godj/actions/runs/35677919124)에서
Linux amd64/arm64의 relation normal·CGO=0이 실패했다. 실제 네 job의 로그는 모두 외부 migration consumer의
`externalLifecycleTransaction`에 새 `AddConstraint`·`RemoveConstraint`가 빠진 같은 compile 오류를 가리켰다.
이 fixture는 현재 backend interface를 직접 구현하는 외부 module이며, 미지원 제약 변경을 명시적 capability 오류로 반환하도록 갱신했다.
제품 인터페이스를 축소하거나 compile negative control을 삭제하지 않았다.

기준 `4c788ae70301bacf89d83d32c6dabea331ef8194` 위 변경한 fixture의 SHA256은
`606ea0955e07db2a2dd33913328fd0b91888352c1217dd3ebe0313627ab80411`이다.
Go 1.26.5/Darwin arm64에서 `go test -json -count=1 -timeout=10m ./internal/compiletest`와 같은 범위 `CGO_ENABLED=0`은
**각 1 package / 68 run=PASS / skip 0**이다. 외부 소비자·잘못된 typed API 거부·offline/platform 환경과
실패했던 migration 하위 검사의 필수 실행, 전체 시작/종료 inventory와 source 불변을 확인했다.
이 compile suite는 기존 `!race` 경계이며 race 실행으로 주장하지 않는다. 제품 Go/JSON과 CI 선택 목록은 바꾸지 않았다.
Artifact는 `godj-composite-uniqueness-9yzkn6xp/labels/compile-boundary-fix/`에 source·전체 events·stderr·receipt를 보관한다.
첫 Hosted 실패 로그도 같은 labels root의 `hosted-job-*.log`에 보존했다.

수정 source `231260c5116bb7cfe157cab54ceb404e05c8ba43`의 [PR feedback](https://github.com/progresshans/godj/actions/runs/35678691111)은
필수 Fast Go feedback까지 success다. [새 Hosted full run 35678713385](https://github.com/progresshans/godj/actions/runs/35678713385)을
같은 source로 실행했다. 첫 run은 확인된 compile 실패를 고친 source의 새 실행으로 대체되어 전체 상태가 cancelled로 끝났다.
첫 run의 실패·취소를 PASS로 합치지 않는다. 새 run의 PostgreSQL 17.10 core normal/race/CGO=0은 각각
**13 package / 2,292 run=PASS / skip 0**이며 새 native 제약·Label을 포함한 필수 inventory 검사도 통과했다.
실패했던 Linux relation normal/CGO=0도 새 source에서 통과했다. 전체 matrix와 마지막 scope gate는 아직 완료되지 않았다.
완료한 job의 checkout SHA·전체 로그와 hash·실제 inventory를 `labels/hosted-35678713385/log-audit.json`에 모으고,
해당 source workflow에서 계산한 기대 job 62개와 대조한다. 일부 job의 성공을 full_platform_verified로 기록하지 않는다.

## GDJ-0097 — Category Label의 모델부터 독립 client까지

2026-09-22, 기준 `2aaed04b30e473ea165024716f50eb5c193c778f` 위 제품·검사·생성물 **43경로**(Go 40, JSON 3)의 source map SHA256은
`ba8ba7360d990a5cbf706102f573ebcc83f56270d864faf0bf61f5d650b8930d`이다. Markdown은 제외한다.
Go 1.26.5/Darwin arm64·SQLite 3.53.3·격리 PostgreSQL 17.5(Homebrew)와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

Label의 name(Char 64)·category(FK PROTECT)·named `(category, name)`을 선언에서 생성했다. `generate`는 12개 모델/project 파일을
생성했고 `makemigrations`는 `0018_label` 한 개를 게시했다. 적용/역방향/재적용·재접속과 기존 Ticket 보존을 검사했다.
Ticket이 없는 별도 Category에서 Label만으로 PROTECT가 작동하고 Label을 삭제하면 Category 삭제가 가능한지 확인했다.
Admin과 API의 같은 저장 transaction에서 숨겨진 Category를 포함한 전체 candidate를 검사하며 name만 입력으로 받는다.

`go test -json -count=1 -timeout=10m ./examples/helpdesk ./api/openapi ./api/openapi/consumertest`와 같은 범위의
`-race`, `CGO_ENABLED=0`은 **각 3 package / 148 run=PASS / skip 0**이다. 필수 검사 11개에는 양 DB의
historical_label/category_labels 하위 실행과 API 구성·인증 실패/부분 게시 거부·schema range·독립 generated client가 포함된다.
실행마다 source 불변·package 및 시작/종료 inventory를 검사했다.

실제 HTTP는 같은/다른 Category의 이름 중복·변경/생략/self update, 64/65 Unicode 글자 경계·input allowlist·escaped 원문 보존,
검색/limit/offset/count와 공표한 최대 offset, malformed/중복 parameter의 I/O 전 거부, 외부 Category 객체의 조회/수정/삭제 거부를 확인했다.
Admin의 숨겨진 category 입력은 400이며 저장 callback을 실행하지 않는다. Anonymous/개별 권한/CSRF 거부도 제품 데이터 I/O 전에 끝난다.
저장 직전 Category 이동 대조는 앞선 Admin 조회를 신뢰하지 않고 transaction 안에서 404로 거부하며 이동을 rollback했다.
사전 조회를 의도적으로 비워 actual native 제약까지 도달한 중복은 non-field unique로 반환한다. Query/cancel/driver/reload 실패와
rollback 불확실성은 500이며 선행 Ticket 쓰기가 rollback되는지 확인했다. 실제 commit 뒤 unknown outcome을 주입한 대조는
500을 반환하고 자동 재시도 없이 transaction/write 각 1회였으며 실제 저장된 한 행을 명시적으로 확인했다.

OpenAPI `IntegerRange`로 페이지의 inclusive numeric bounds를 내보내고 inverted range를 설정 오류로 거부한다.
고정 ogen으로 세 profile을 재생성했으며 Article 두 profile·go.mod/go.sum/ogen.yml은 byte 불변이다. Helpdesk의 18개 operation 중
Label 6개 경로와 page/진단/권한/CSRF를 별도 module client가 실제 HTTP로 소비했다. 부모는 유지된 Label의 최종 이름·Category와
외부 Label 보존을 SQLite에서 독립 확인한다. 독립 client는 SQLite fixture이며 PostgreSQL HTTP는 Helpdesk 자체 검사의 별도 범위다.
네 project generated drift·추가 migration 후보 0과 영향 `go vet`도 통과했다.

첫 normal 실행은 남아 있던 12-operation test 기대와 Admin의 unknown category를 무시할 것이라는 새 test 가정 때문에 실패했다.
실제 18개 operation을 검사하고 Admin의 기존 400 거부를 negative control로 유지한 뒤 정상 name-only 요청을 별도로 검증했다.
제품의 입력 거부를 완화하지 않았고 첫 실패 source/events는 보존했다. 편집 중 unused import compile 오류도 수정했다.
Artifact root는 `godj-composite-uniqueness-9yzkn6xp/labels/`이며 `latest-normal-path`, `latest-race-path`, `latest-cgo0-path`와
`latest-client-path`에 source·전체 events·필수 inventory·receipt·재생성 후보를 보관했다. `generated-drift.json`, `migration-drift.json`, `vet.*`가 추가 검사를 기록한다.
이 결과는 명시한 영향 범위 로컬 검증이다. GDJ-0097 전체 통합·Hosted full은 아직 실행 결과를 확인해야 한다.

통합 실행 전 CI의 PostgreSQL owner가 명시한 test 이름으로 `-run`을 구성함을 확인했다. 기존 목록에는 새 named 제약이 없으므로
native/catalog 10개와 Label의 historical/HTTP 하위 검사 2개를 추가했다. 전체 SQLite/ORM package를 실행하는 relation owner에도
named 제약·전체 candidate·실행 실패의 필수 항목 17개를 추가했다. 실제 `go test -list` 선택·owner 소속을 확인했고
기존 CI Python 검사 **38 PASS**와 diff 검사를 통과했다. 제품 Go/JSON은 `474a835e5906c8f0fe3d1b1ef150241ce8dd8369`에서 바뀌지 않았다.
이 목록 확인은 native 실행 PASS가 아니며 Hosted full에서 실제 실행·종료·no-skip을 확인해야 한다.
CI 목록을 포함한 source `fdddfae8a58b0d6cbf6d10a40bfeb878acaf7037`의 [Hosted PR feedback](https://github.com/progresshans/godj/actions/runs/35677873702)은
필수 Fast Go feedback까지 success다. 같은 source에서 `suite=full`로 [Hosted full run 35677919124](https://github.com/progresshans/godj/actions/runs/35677919124)을
dispatch했고 실제 matrix job들의 실행을 확인했다. 아직 terminal 결과나 full_platform_verified를 확인하지 않았으므로 전체 통합 PASS로 기록하지 않는다.

## GDJ-0097 — 복합 고유성의 전체 candidate ORM 검증

2026-09-22, 기준 `7d769e429d8d4a9171c434877b41868a81b4264a` 위 Go 코드·검사 **10경로**의 source map SHA256은
`e30b1333a4a39858d3ba474c93145a048e370c37370b310c7d43cbf5e98a1b8e`이다. Go 1.26.5/Darwin arm64, SQLite 3.53.3,
격리된 PostgreSQL 17.5(Homebrew)와 `GODJ_REQUIRE_POSTGRES=1`로 실행했다.

Manager의 metadata snapshot에 ordered constraint member를 한 번 연결하고, column 선언 순서 뒤 canonical constraint name 순서로 검사한다.
Create의 resolved default·생성 전 Auto PK, Update의 생략 member·명시적 NULL·presence-aware 0 PK와 self exclusion을 구분한다.
모든 AST를 I/O 전에 구성하며 잘못된 이름·빈/중복/누락 member·잘못된 omitted candidate는 앞선 조회도 실행하지 않는다.
단일 member는 field/unique, 복합 member는 __all__/unique_together이며 진단에 값·행·물리 이름을 포함하지 않는다.
Named 제약도 query·iteration·close·취소 오류에서 부분 진단을 폐기하고, manager 병렬 공유가 결과를 cache하지 않는지 검사했다.

- `go test -json -count=1 -timeout=10m ./orm ./validation ./forms ./admin ./internal/uniquetest ./examples/helpdesk`:
  **6 package / 1,767 run=PASS / skip 0**, 필수 parent 15개. 기존 Helpdesk의 실제 양 DB 소비자·권한·native 충돌·취소·rollback 불확실성 검사도 포함한다.
- 양 DB의 `NamedConstraint`, `UniqueReference`, `UniqueConcurrent` 집중 검사:
  **2 package / 523 run=PASS / skip 0**, 필수 parent 20개. 독립 scalar fixture의 DB별 13 profile에 걸친 96개 입력을 같은 bucket에 저장하고,
  다른 bucket·NULL 조합·self/partial update와 사전 거부 시 선행 쓰기 rollback을 확인했다. 경쟁하는 두 writer의 사전 조회를 먼저 통과시킨 뒤 native winner가 하나인지 검사했다.
- `Validate(Named)?Unique`, 양 DB `NamedConstraint`, `PublicHelpdesk`의 `-race`와 `CGO_ENABLED=0`:
  **각 4 package / 351 run=PASS / skip 0**, 필수 parent 각 31개. 영향 package의 `go vet`도 통과했다.

첫 native 대조는 기존 SQLite의 NaN 거부 두 입력과 JSON object key 정규화 한 입력을 Django 기본 저장과 동일하게 기대하여 실패했다.
제품 저장 정책을 바꾸지 않고 기존 DEV-0015/DEV-0017 경계를 검사에 반영했다. NaN은 사전 검사와 저장 모두 invalid_value이며,
JSON은 같은 logical input인 독립 canonical profile과 대조한다. 차이의 개수도 각각 2·1로 고정하여 임의 예외를 허용하지 않는다.
이후 전체 named native 집중 검사와 위 common/race/CGO=0을 같은 최종 source로 실행했다. 첫 실패 원본은 보존한다.

Artifact root는 `godj-composite-uniqueness-9yzkn6xp/orm/`이다. `latest-common-path`, `latest-native-path`, `latest-race-path`,
`latest-cgo0-path`에 source·command·전체 JSON events·stderr·필수/package inventory·receipt를 보관했다.
생성기·schema 선언·생성 ABI는 변경하지 않았고 기존 생성 소비자는 위 compile/runtime 검사에 포함된다.
이 결과는 명시한 로컬 ORM·native·기존 소비자 회귀 범위다. Label 모델/Form/Admin/API/client 및 GDJ-0097 전체 통합·Hosted full은 미완료다.
저장한 source `2aaed04b30e473ea165024716f50eb5c193c778f`의 [Hosted PR feedback](https://github.com/progresshans/godj/actions/runs/35675133141)은
필수 Fast Go feedback step까지 success다. 이후 Label 변경의 통합 결과로 합치지 않는다.

## GDJ-0097 — 양 DB native named constraint와 전체 key ownership

2026-09-22, 기준 `47a1526bba09099f68250a27510b72956a92a656` 위 Go 코드·검사 **25경로**의 source map SHA256은
`21cb88133590911f852332ea0e024983a5551928d08dccfc6379fd69e6b599a7`이다. 환경은 Go 1.26.5/Darwin arm64, SQLite 3.53.3, PostgreSQL 17.5(Homebrew)다.
선언된 ordered logical members를 실제 storage column으로 해석하며, field Unique와 model constraint는 별도 이름 domain을 사용한다.
SQLite index_xinfo의 모든 key·rowid, PostgreSQL conkey/indkey 및 key별 collation·operator class·정렬 option을 검사한다.
PostgreSQL의 catalog 자료형에서 첫 field 전용 상태를 제거하고 bounded 전체 key 조회로 바꾸었다.

기본 회귀 명령은 `go test -json -count=1 -timeout=10m -skip '^TestPostgresRevisionFenceHelperProcess$' ./db/sqlite ./db/postgres ./migrations/... ./internal/migrationgraph ./internal/migrationautodetect ./internal/uniquetest`이다.
격리된 실제 PostgreSQL DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용하여 **9 package / 5,962 run=PASS / skip 0**, 필수 parent **21개**를 확인했다.
새 named constraint와 physical projection의 집중 `-race`, `CGO_ENABLED=0`은 **각 4 package / 89 run=PASS / skip 0**, 필수 parent **20개**다.
모든 실행의 시작·종료·package/parent inventory와 source 불변을 확인했다. 네 project generated drift·checked-in relation product와 affected vet도 통과했다.

처음의 회귀 명령은 helper를 일반 프로세스에서 호출하여 5,962 PASS와 helper skip 1을 기록했고 no-skip inventory 검사에 실패했다.
현재 명령은 독립 실행할 수 없는 helper entry만 일반 검색에서 제외한다. `TestPostgresRevisionFenceCrossProcessIntegration`을 필수 owner로 포함하고,
그 부모가 명시적 child argv·helper 환경·READY/RELEASE pipe로 실제 별도 프로세스를 실행하고 성공 종료를 확인한다. Helper 검증을 생략한 결과가 아니다.

검사에는 12 scalar 종류의 13 profile에서 같은/다른 bucket·각 SQL NULL 조합·self/update·동시 쓰기와 선행 쓰기 rollback,
PK member·named single·column Unique의 독립 소유권, 동일 이름 member 순서 교체·rename와 역방향이 포함된다.
기존 중복으로 AddConstraint가 실패하면 앞선 AddField까지 rollback되고 row·catalog·sequence·revision/recorder가 유지되며 명시적 data 수정 뒤 재시도한다.
RemoveConstraint의 역방향 충돌도 같은 경계를 검증했다. 두 번째 key의 column/순서/DESC/collation/operator class와 transitive target drift는 DB 변경 전에 거부한다.
SQLite에서는 FK 제거 전에 dependent constraint를 제거하고 remake 뒤 남은 named tuple·행·sequence high water를 복원한다.
재생성의 늦은 실패는 앞선 constraint 제거까지 rollback하며 이후 정상 재시도가 가능하다.

첫 집중 native 검사는 PostgreSQL의 column Unique와 동일 member named single이 CREATE TABLE에서 합쳐져 expected 5개 중 4개 constraint만 남는 문제를 찾았다.
독립 SQL probe로 inline 병합과 별도 ALTER의 독립 생성을 확인한 뒤, CreateModel을 table DDL과 named ALTER들의 한 operation group으로 바꿨다.
그 후 이름별 독립 제거가 통과했고, 마지막 constraint의 의도적 충돌은 먼저 생성한 table·index·sequence·bootstrap까지 rollback했다.
수정 후 집중 검사 87건이 통과했으며 transitive 두 검사를 더해 위 normal/race/CGO=0 checkpoint를 완료했다.
편집 중 compile 오류·첫 native 실패와 helper inventory 실패는 원본 로그에 보존하며 현재 PASS에 합치지 않는다.

Artifact root는 `godj-composite-uniqueness-9yzkn6xp/native/`이며 `latest-normal-path`, `latest-race-path`, `latest-cgo0-path`가 현재 실행을 가리킨다.
각 폴더에 source·command·전체 JSON events·stderr·required/package inventory·receipt를 보관하고, `verification-summary.json`·`environment.json`,
`postgres-duplicate-inline-constraints-probe.sql/.stdout/.stderr`, `generate-check.*`, `vet.*`에 범위와 원본을 보관했다.
이 검증은 named native 동작과 명시한 로컬 회귀 범위다. ORM 복합 사전 검증·Label 소비자·GDJ-0097 전체 통합 및 Hosted full은 미완료다.
Django reference의 일반 ordinary index는 현재 GoDj의 미선언 index로서 drift이며, 일반 index ownership을 구현한 결과로 주장하지 않는다.
저장한 source `7d769e429d8d4a9171c434877b41868a81b4264a`의 [Hosted PR feedback](https://github.com/progresshans/godj/actions/runs/35668606774)은
필수 Fast Go feedback step까지 success다. 이 빠른 검사를 이후 ORM 변경이나 full platform 검증에 합치지 않는다.

## GDJ-0097 — 제약 추가·제거 이력과 자동 계획

2026-09-22, 기준 `b48ec639fa4771ba18415d6c2ed797d878be69ec` 위 Go 코드·검사 **34경로**의 source map SHA256은
`86f05c3b6f749c5950487560c218749ead9013332ba05f4323f4c598dee6c914`이다. Markdown은 제외한다.
Go 1.26.5/Darwin arm64에서 AddConstraint/RemoveConstraint의 전체 member preimage·역방향·closed wire·digest·할당 전 resource 제한과
historical replay·sealed intent·SchemaEditor 인자를 연결했다. 같은 단계의 remove/add 교체도 이전 상태를 보존한다.
Backend에 전달한 모델·제약 인자를 일부러 변경해도 다음 operation·반환 state·재구성한 이력에 영향을 주지 않는 대조를 포함했다.

`go test -json -count=1 -timeout=10m ./migrations/... ./internal/migrationgraph ./internal/migrationautodetect`와
같은 범위의 `-race`, `CGO_ENABLED=0`은 **각 6 package / 719 run=PASS / skip 0**이다. 필수 parent **11개**의 실행·종료와 전체 inventory를 검사했다.
`internal/projectcheck/linked`·`conformance/migrationrelationproduct`는 **168 PASS / skip 0**,
`conformance/runners/godj`의 Migration Command/Execution/Lifecycle/TargetPlan·GenerateMigrationLifecycle 영향 검사들은 **104 PASS / skip 0**이다.
네 project `generate --check`와 checked-in relation product·영향 범위 `go vet`도 통과했다.
그 뒤 두 기존 test 파일의 backend 인자 변조 대조와 unsupported 오류 분류를 강화했고 공통 normal/race/CGO=0·해당 test vet를 다시 실행했다.
제품·생성기·선언·어댑터 source가 같음을 `source-coverage.json`으로 확인하여 앞선 어댑터·generated 검증의 범위를 보존했다.

독립 Django SQLite/PostgreSQL fixture의 add/remove/rename/member 교체/member 순서/목록 재정렬/field 동시 추가 **7종 × 2 DB 관찰**과
GoDj의 operation 의미·순서를 대조했다. 같은 앱·다른 앱 순환 FK에서는 제약의 모든 member가 먼저 생기며, 독립 inline 제약은 유지된다.
모든 게시 prefix를 역순 source 목록으로 재입력해도 남은 migration byte가 같음을 확인했다.
Django 기준의 일반 forward RemoveField까지 구현한 결과는 아니다. 해당 변경은 명시적으로 unsupported이며 RemoveConstraint만 부분 게시하지 않는다.

CreateModel/AddConstraint/RemoveConstraint의 양 built-in SQL renderer는 아직 native ownership 미완료를 capability/unsupported와 nil SQL로 거부한다.
제약 없는 control은 실제 SQL을 반환하고, 공통 renderer 계약은 제약 추가·제거에 빈 SQL을 허용하지 않는다.
Capability 부족은 transaction 전에 중단하며 가짜 backend의 제약 실행 실패는 recorder/commit 없이 rollback한다.
이 검증을 SQLite/PostgreSQL native 복합 제약 적용·catalog·실제 충돌 rollback이나 전체 플랫폼 PASS로 합치지 않는다. Native·ORM·Label은 계속 미완료다.

원본은 `godj-composite-uniqueness-9yzkn6xp/constraint-operations/`의 `final-source.json`, `final-*-receipt.json`,
`final-*.jsonl/.stderr`, `required.txt`, `*-packages.txt`, `adapters*`, `runner-adapters*`, `generate-check.*`, `vet*`, `source-coverage.json`에 있다.
직전 metadata commit `b48ec639`의 [Hosted PR feedback](https://github.com/progresshans/godj/actions/runs/35661997055)은 필수 Fast Go step을 통과했다.
그 실행은 이번 operation source의 Hosted 결과가 아니다. 전체 플랫폼 증거는 여전히 명시된 `4f92d688`의 ServiceReport milestone이 소유한다.

## GDJ-0097 — 선언·생성·이력 metadata 연결

2026-09-22, 기준 `d0f4a9a0c7ecdc4a39369bfe160b06f83f7e9c6b` 위 Go 코드·검사 **33경로**의 source map SHA256은
`bd1b1b0d4575e8d6d7e2839edfb058ce7a43df0559f5b2ca00dd6c050c8ef026`이다. Markdown은 제외한다.
Go 1.26.5/Darwin arm64에서 named constraint의 canonical identity·member 순서·입력/clone 소유권·생성 metadata와
project wire의 폐쇄된 형식·정확한 byte 한도·할당 전 resource 검사를 연결했다.
CreateModel의 현행 wire·digest·loaded state·intent도 제약을 보존하고, 필드 변경에 다른 제약 변경이 섞이거나
남은 제약의 member를 제거하는 경로를 거부한다. Model clone은 nil/empty slice 형태와 중첩 member를 함께 보존한다.

`go test -json -count=1 -timeout=5m ./schema/... ./codegen ./migrations/... ./internal/migrationgraph ./internal/irresource ./internal/projectspec ./internal/projectwire`는
**11 package / 1,106 run=PASS / skip 0**, 새 필수 parent 15개를 확인했다. 전체 시작/종료·package·required inventory와 source 불변을 검사했다.
네 project의 `generate --check`와 checked-in relation product, 영향 범위 `go vet`도 통과했다.
그 뒤 추가한 파일은 intent 복사·field delta 검사용 두 Go test뿐이며 제품·생성기·선언이 같음을 `source-coverage.json`으로 확인했다.
추가 테스트를 포함해 위 일반 checkpoint를 다시 실행했고 해당 두 package의 vet도 통과했다.
이 checkpoint는 metadata·historical CreateModel과 공개 SQL projection 경계의 검증이며 native 복합 제약의 적용·race·전체 플랫폼 검증이 아니다.

첫 SQL projection 검사는 SQLite의 CreateModel 경로가 새 제약을 생략할 수 있음을 찾았다. Common intent admission에서
source·retained target·transitive metadata를 거부하도록 고쳤다. PostgreSQL의 공개 projection 오류는 원인을 노출하지 않으므로
검사도 내부 문자열 대신 공개 capability category/code를 확인하도록 수정했다. 양 renderer의 제약 없는 control은 SQL을 만들고,
named constraint 선언은 unsupported와 nil SQL을 반환한다. 이는 구현 전 제약 누락을 막는 전환 경계이며 native 구현을 대신하지 않는다.
이 시점의 Add/Remove operation·자동 계획·실제 양 DB ownership·ORM 사전 검증과 Label 소비자는 미완료였다.

Artifact root는 아래 독립 기준과 같은 `godj-composite-uniqueness-9yzkn6xp`이며 `latest-metadata-checkpoint-path`가 현재 실행을 가리킨다.
해당 디렉터리의 `source.json`, `stdout.jsonl`, `stderr`, `packages.txt`, `required.txt`, `receipt.json`,
`generated-drift.stdout/stderr`, `vet.stdout/stderr`, `vet-added-tests.stdout/stderr`, `source-coverage.json`에 원본과 검사 범위를 보관했다.

## GDJ-0097 — 복합 고유성의 독립 기준과 선언 임시안

2026-09-22, 기준 `56c776f7a55542bdfda49e20a3b0f58ac99256bd` 위 독립 runner·raw fixture·검사를 추가했다.
Runner SHA256은 `00a104ba68656079e4e40201e64fda2ec87558d79ac7a049b8371266df4e75ca`다.
Django 6.1/asgiref 3.12.1/sqlparse 0.5.5와 PostgreSQL reference psycopg 3.3.6을 고정했다.
Python 3.14.3의 SQLite 3.50.4와 PostgreSQL 17.5에서 두 hash seed(`0`, `813`)의 fresh process 결과가 일치했다.
실제 Model/ModelForm의 단일·복합·겹치는 제약, SQL NULL 조합·self/update, 선행 쓰기 rollback,
중복 데이터의 constraint 추가 실패·명시적 수정/재시도·제거/reverse를 관찰했다.
기존 ordinary index의 보존도 확인했다. Autodetector의 이름/member/순서 변경·field 추가/제거와
같은/다른 앱의 순환 관계를 포함한 10개 변경 시나리오를 추가했다.

Form에서 서버 소유 Category를 제외하면 복합 constraint 검증도 제외된다. 전체 저장값을 검사하는 소비자가 필요한 근거다.
Constraint 목록 순서만 바꾸는 경우 migration은 없지만 member 순서를 바꾸면 remove/add를 생성한다.
순환 FK 생성에서 지연된 member가 준비되기 전에 constraint를 추가하지 않는 것도 확인했다.
선언을 `(category, name)`에서 `(category, id)`로 바꾸는 negative control이 실제 Form·native 저장 결과를 바꾼다.
Runner는 GoDj 코드나 기대 fixture를 읽지 않는다.

저장소 경로로 옮긴 검사도 Python **3.12.13·3.13.15·3.14.3·3.14.7 각각 4 PASS / skip 0**, 총 **16 PASS**다.
Unittest의 시작/종료·필수 실행·skip을 검사했으며 이는 Django 독립 기준 검증이고 GoDj 제품 parity가 아니다.
새 테스트를 포함한 Hosted 전체 검증은 실행하지 않았다.

별도 임시 Go overlay에서는 명시적 named constraint·논리 field 순서·canonical name 순서·clone/equality/hash와
생성 metadata·project wire의 폐쇄된 형식/정확한 byte·할당 전 resource 한도를 구현해 검토했다.
Go 1.26.5/Darwin arm64에서 `./schema/... ./codegen ./internal/projectspec ./internal/projectwire`는 **472 run=PASS / skip 0**,
필수 새 parent 7개를 확인했다. 12경로 patch SHA256은 `5a57048c03f02a705d40bc054f5b851a0af5e922bd48082e4c4d8d14ec3d8797`다.
이 임시안은 해당 검증 시점에 저장소 제품 코드에 적용하지 않았으며 historical operation·native constraint·ORM/Label 소비자 구현이나
race/platform PASS로 계산하지 않는다. API 채택과 제품 통합은 활성 work에서 이어간다.

Artifact root: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-composite-uniqueness-9yzkn6xp`.
`latest-capture-path`, `latest-promoted-check-path`, `promoted-reference-source.json`, `prototype/latest-checkpoint-path`가
원본 stdout/stderr·source manifest·완전한 이벤트 검사 receipt를 가리킨다. `reference-v1`은 확장 전 기준을 보존한다.
Native reference의 owned DB는 잔여 connection·table·test schema 0을 확인한 뒤 제거했으며 기존 서비스는 유지했다.

## GDJ-0096 — ServiceReport 소비자와 명시적 관계 선택

2026-09-22, 기준 `65563c988112543a9d9424bfbea937ec6f38cd1f` 위 코드·생성물·검사·CI **76경로**의 manifest SHA256은
`41645cb6dd5592c0b3c599fd4d504470da035d003fc9b8b24d0ff2bf39ef1753`이다. Markdown은 제외한다.
Darwin 25.6.0/arm64·Go 1.26.5·modernc SQLite 3.53.3(v1.56.0)·PostgreSQL 17.5 Homebrew/pgx v5.10.0,
`TZ=Pacific/Chatham`에서 실행했다. Native 시도마다 locale C/UTF8의 별도 owned DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

ServiceReport의 migration·Form/Admin·JSON CRUD/reverse·OpenAPI/client를 연결했다. 선택 범위가 표시 후 바뀌는 경우,
권한·CSRF 전 제품 I/O 금지, 실제 FK/UNIQUE 충돌, 선행 mutation rollback, self update·no-op·재할당과 삭제 후 Ticket 보존을
양 DB에서 실행했다. Ticket PROTECT는 Runtime의 coordinated relation transaction을 사용한다. Query/driver/cancellation과
rollback 불확실성은 validation·404·정상 PROTECT 응답으로 축소하지 않는다. SQLite FK-off와 borrowed session 만료,
일반/관계 쓰기의 동일 gate·DB fence, PostgreSQL acquire/rollback/commit 실패의 소유권도 검사했다.

고정 Django 6.1/asgiref 3.12.1/sqlparse 0.5.5의 독립 ModelChoice runner는 실제 scoped QuerySet에서
required/optional × initial 4개 × raw 27개, **216개 관찰**을 양 DB에서 얻는다. Runner SHA256은
`f3f776f57723562563b0c0a0719d8ddfc06601f08f6ff2f968f3e48a7efd9819`다. Django source hash도 raw fixture에 남긴다.
Reference SQLite는 Python 3.14.3의 3.50.4, PostgreSQL은 17.5/psycopg 3.3.6이다. GoDj는 양 fixture의 cleaned key/null,
오류 code·raw-input 변경 감지를 대조했다. QuerySet/model instance 대신 순수 snapshot/int64 key를 사용하는 API 차이는 ADR/DEV-0018에 기록한다.
Python 3.12.13·3.13.15·3.14.3·3.14.7에서 각각 **1 PASS / skip 0**, 총 **4 PASS**이고 각 테스트가 두 hash seed의 fresh process를 실행한다.

| 검증 | 결과 | 범위 |
| --- | --- | --- |
| 일반 영향 범위 | 4,145 PASS / skip 0 | Forms/model·Admin·systemstate·SQLite 전체와 실제 Helpdesk SQLite/PostgreSQL; 필수 parent 438개 |
| PostgreSQL coordinated adapter | 20 PASS / skip 0 | 실제 driver 경계 fault/lifetime 검사; 필수 parent 8개 |
| Runtime/입력/소비자 race | 1,514 PASS / skip 0 | Forms/model·Admin·systemstate·Helpdesk 양 DB; 필수 parent 153개 |
| Backend coordination race | 47 PASS / skip 0 | SQLite/PostgreSQL coordinated 경로; 필수 parent 18개 |
| CGO=0 영향 범위 | 82 PASS / skip 0 | ModelChoice·Admin source·Runtime/DB coordination·Helpdesk 양 DB; 필수 parent 30개 |
| 독립 OpenAPI client | 일반 10, race 10 PASS / skip 0 | locked ogen 재생성 byte 일치·독립 module build·실제 HTTP/auth/CSRF·완전한 receipt·DB 결과 |
| Generated consumer/외부 compile | 184 PASS / skip 0 | 생성 package·cross-app·facade ABI와 compile negative controls; 필수 parent 69개 |
| Generated drift·affected vet | PASS | 네 project의 generate --check와 checked-in relation product; 변경 영역 vet |
| CI 도구·format·문서·diff | 38 PASS / 검사 통과 | 새 required inventory 포함, 문서 139개의 링크 검사 |

전체 Go 이벤트의 시작/종료·필수 root·skip·failure를 검사했고 source 불변을 확인했다. 통합 실행 후 코드 차이는
CI required inventory 두 경로뿐이며 위 CI 도구 검사를 적용했다. Child process helper는 기존 parent 검사가 실행/종료/결과를 소유한다.
Ogen v1.24.0의 Helpdesk 생성 파일 12개와 schema만 바뀌고 Article 두 profile 및 go.mod/go.sum/ogen.yml은 byte 그대로다.
안전한 GET 오류 응답의 CSRF header도 client에 반영하며 generated DELETE는 실제 cookie/header를 보낸다.
Client 진단은 checked-in 고정 실패 문자열만 정확히 일치할 때 공개하고 실행 입력·URL·token이나 알 수 없는 stderr는 숨긴다.

첫 실행 실패도 보존했다. 초기 fixture adapter의 필수 field nil initial 전달, migration target 이름, 테스트용 별도 auth runtime의
CSRF 초기화와 commit-unknown code 기대를 고쳤다. 이후 대조는 valid ModelChoice의 변경 감지가 cleaned 숫자 equality를 따르는
제품 결함을 찾았다. `-0`, `+7` 같은 입력도 raw 기준으로 비교하도록 수정해 216개 관찰을 통과했다.
외부 client는 안전한 404 응답의 갱신된 CSRF header를 반영하지 않아 생성이 거부된 점을 수정한 뒤 정상/race를 통과했다.
실패 시도는 최종 PASS로 재분류하지 않았다. 각 owned DB는 실행 후 잔여 connection·table·test schema 0을 확인하고 삭제했으며 기존 서비스는 유지했다.

로컬 artifact root: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-service-reports-v29csxf7`.
`final-code-source.json`, `source-coverage.json`, `normal-1790019582178291000`, `integration-1790019882598512000`,
`consumers-1790019800088352000`, `drift-vet-1790019904749795000`, `reference-1790018552366731000`, `model-choice-python`에
원본 stdout/stderr·Go events·required inventory·manifest·cleanup receipt를 남겼다.
구현 source `63b0562a28146ca22e5b31ee7c6ce2bdb95386a6`의 첫 [Hosted full](https://github.com/progresshans/godj/actions/runs/35647579556)은
**실패**로 남긴다. 60개 job은 success였지만 macOS Intel relation race의 생성 소비자 package가 aggregate 20분 제한으로 종료되어
최종 CI owner 판정도 실패했다. `codegen/consumertest`의 59개 parent 중 30번째를 시작한 지 4초였으며 앞선 29개는 PASS였다.
Data-race/검증식 실패 대신 package 전체 시간이 소진됐고 나머지 parent가 실행되지 않았으므로 현재 full PASS로 사용할 수 없다.
해당 좌표의 package budget을 35분, job budget을 45분으로 조정한다. 기존 test/package/required inventory·race instrumentation과
실행/종료·skip·실패 검사는 유지한다. 다른 좌표의 시간 한도는 바꾸지 않는다. 첫 실패 job의 원본 로그는
`hosted/job-106491750057-failed.log`에 보존했다. 이 CI 조정은 제품 구현과 별도의 source 변경이며 필요한 full을 다시 실행한다.
CI budget을 조정한 source `4f92d68869d5491c4b56e83da40b79a4c7866bb7`의 [Hosted full](https://github.com/progresshans/godj/actions/runs/35652494345), attempt 1이 완료됐다.
**62개 job 전부 success**, 최종 판정의 `scope=full`, `full_platform_verified=true`, 필수 owner 8개를 실제 로그에서 확인했다.
PostgreSQL 17.10 core는 normal/race/CGO=0 각각 **2,134 run=PASS / skip 0**이며 보고서 현재/역방향 migration 소비자와
coordinated relation adapter를 필수 inventory로 검사한다. Exact Darwin Python은 **314 PASS / skip 0**이다.
네 compatibility 환경은 각각 314개 발견/310 PASS/지정된 skip 4개이며 그 네 검사의 실행은 exact owner가 소유한다.
Relation·portable·project check·command product의 각 플랫폼/mode와 같은 run의 DB/process capture 검증을 함께 통과했다.
앞서 시간 제한으로 실패했던 macOS Intel relation race도 필수 CI inventory validator에서 **31 package / 5,261 run=PASS / skip 0**을 확인했다.
제품 source의 [Hosted fast](https://github.com/progresshans/godj/actions/runs/35647531350)도 mandatory Fast Go feedback을 실행해 success다.
Hosted 검증은 앞의 로컬 결과와 구분하며 로컬 전체 gate를 중복 실행하지 않았다.
`hosted-retry/final-audit.json`과 source/attempt가 일치하는 job metadata·원본 로그·hash/inventory receipt를 artifact root에 보관한다.
GDJ-0096의 작업 보고서 소비자와 process/platform 통합 milestone을 완료했다.
이 범위의 통과를 전체 프레임워크 완성이나 미지원 relation 기능의 완료로 합치지 않는다.

## GDJ-0096 — OneToOne assignment와 명시적 저장

2026-09-22, 기준 `557095bea09529ef584fdd0a07e69767748c376c` 위 코드·생성물·검사·CI **74경로**의 manifest SHA256은
`af1232c26624afc5f5a762b48cab74e925b499cc96f90450fedf74d1369580b7`다. 현행 문서는 이 집합에서 제외한다. Darwin 25.6.0/arm64·Go 1.26.5,
modernc SQLite 3.53.3(v1.56.0)·PostgreSQL 17.5 Homebrew/pgx v5.10.0·`TZ=Pacific/Chatham`에서 실행했다.
Native 시도마다 별도 locale C/UTF8 DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

Generated reverse Set은 지정한 두 wrapper의 candidate를 준비한 뒤 함께 게시한다. Required OneToOne도 Clear할 수 있으나
getter/Unwrap/Save는 I/O 전 required 오류다. Owner Save가 새 PK를 얻으면 low-level handle을 재바인딩하면서 명시적 child cache를 유지한다.
상세 의미와 지원 경계는 [ADR-0073](../adr/0073-one-to-one-cardinality-and-reverse-objects.md), Django 차이는 [DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)을 따른다.

독립 Django runner에 별도 required/nullable assignment 모델과 **14개 관찰**을 추가했다. 기존 37개 동작·2개 migration·41개 lookup·
12개 eager·outgoing-delete 관찰은 그대로 유지했다. Runner SHA256은 `174cecc7a60a4ae1eddad4c9d749a393c84201cb0841509bd8125f1221706fbd`다.
양 reference backend 결과가 같으며 실제 generated facade에서 필수/선택 reverse 할당·해제·교체·재할당, unsaved forward/reverse와
부모→자식 저장, cold clear 뒤 실제 SELECT, 실패 뒤 명시적 복구를 대조했다. 두 required clear의 오류/I/O 경계와 두 unsaved reverse
실패의 중간 reciprocal cache 차이는 별도로 검증하며 전체 관찰 parity로 계산하지 않는다.
네 Python(3.12.13·3.13.15·3.14.3·3.14.7) 각각 **7 PASS / skip 0**, 총 **28 PASS**다. Django 6.1/asgiref 3.12.1/sqlparse 0.5.5,
PostgreSQL reference psycopg 3.3.6을 고정하고 두 hash seed의 fresh process 및 unittest start/stop inventory를 검사했다.

양 DB에서 native uniqueness cause·선행 mutation rollback·실패 시 child PK 미게시·입력 메모리 보존과 retry를 확인했다.
Forward derivation의 원본 보존, 같은 key의 warm identity, raw FK의 pending override, nil/copy/origin/PK mutation 거부,
실패한 쓰기의 원인 보존·자동 재시도 없음도 검증했다. 수동 PK 0의 presence·required absence는 구분된다. SQLite는 실제 PK 0 행을 저장·재조회했고,
PostgreSQL은 기존 manual identity INSERT 제한에 따른 명시적 unsupported·무게시를 검사했다. 없는 key-present target의 FK와 nullable 0 FK는
실제 native 제약이 거부하며 unsaved-target 오류로 바꾸지 않는다. PostgreSQL 수동 identity INSERT 지원을 추가한 결과는 아니다.

외부 생성 module은 다른 중간 model의 selection을 compiler가 거부하며, 새 runtime test가 reverse Set의 self-edge·두 wrapper identity와
손상된 child cache 때문에 실패한 setter의 owner reconciliation 미게시를 확인한다. 취소·required Clear의 no-I/O와 namespace 충돌도 검사했다.

| 동일 source의 범위 | 실행 결과 |
|---|---|
| query/ORM/codegen/queryplan/생성 fixture normal | 1,211 run=PASS, skip 0 |
| native PostgreSQL OneToOne/RelationDelete selector | 112 run=PASS, skip 0 |
| query/ORM/queryplan/fixture race | 867 run=PASS, skip 0 |
| 양 DB 영향 selector race | 2,349 run=PASS, skip 0 |
| 공통·양 DB 영향 selector CGO=0 | 2,815 run=PASS, skip 0 |
| generated consumer/외부 compile normal | 184 run=PASS, skip 0 |
| publication parent root 전체 | 169 run=PASS, skip 0 |

`go test -json -count=1 -timeout=15m`을 사용했고 consumer는 20m다. Normal 공통은
`./query ./orm ./codegen ./db/internal/queryplan ./conformance/onetoonefixture/...`, native normal은 `-run 'OneToOne|RelationDelete' ./db/postgres`다.
Runtime race는 codegen을 제외한다. DB race/CGO0 selector는 `OneToOne|Relation|Select|Eager|Projection|Ordering|Compile|Membership|MigrationCapabilities`이며
CGO0은 runtime와 양 DB를 함께 실행했다. Consumer는 `./codegen/consumertest ./internal/compiletest`, publication은 발견한 parent root 72개 전부다.
Child-only crash helper는 기존 parent가 실제 process 실행·종료·결과를 소유한다. 각 command의 필수 root·전체 start/terminal·package 종료·skip 0과
source 전후 불변을 검사했다. 이 native normal은 PostgreSQL 전체나 native process 검증이 아니다.

Facade ABI v10으로 네 프로젝트의 generated Go 56개·manifest를 재생성했다. Candidate compile·`make generate-check`, CI 도구 **38 PASS**를 확인했다.
SQLite/PostgreSQL assignment root와 외부 generated runtime root를 CI required inventory에 추가했다.

첫 normal은 공통 1,210 run/1,209 PASS/1 FAIL, native 111 PASS였다. Facade v10 변경 후 v9 golden/provenance 검사 갱신 누락을 수정했다.
다음 normal은 공통 1,211 PASS, native 112 run/110 PASS/2 FAIL이었다. 추가 manual PK 0 시나리오가 PostgreSQL의 기존 unsupported identity INSERT를
성공으로 가정했으며, 해당 capability 경계를 명시적 오류·행 미게시 검사로 수정했다. 그 사이의 compile 오류로 test discovery가 시작되지 않은 시도도
PASS로 계산하지 않았다. 이후 위 최종 source와 표의 실행에서 모든 필수 검사가 통과했다.
초기 reference 작성 중 Django의 unsaved failure reciprocal cache invalidation을 관찰했고, Go 기대값으로 원본 관찰을 덮어쓰지 않았다.

정확한 command·source·raw reference/Python/Go 로그·필수 root·실패 시도·cleanup 영수증은 `godj-one-to-one-assignment-f83hkabs` 로컬 artifact에 보존했다.
소유 DB의 잔여 connection·table·test schema가 0인 것을 확인한 뒤 그 DB만 제거하고 기존 service는 유지했다.

구현 source `a79cf54755ab9ed06ffa2a29cbb6b5f01b28b765`의 [Hosted fast run 35640277837](https://github.com/progresshans/godj/actions/runs/35640277837)이 성공했다.
Source·job/step·전체 실행 로그를 보존했다. `make quick` 범위이며 native PostgreSQL이나 full platform으로 계산하지 않는다.
현행 문서 139개 링크·format·diff 검사도 통과했다. 현재 결과는 assignment의 checkpoint이며 작업 보고서 Form/Admin/API/OpenAPI/client와
소비자 전체의 platform 통합은 남아 있다. 이전 Hosted full `42ae95d3b1a891e6a0692fb0399968e483f4d907`을 현재 source의 성공으로 쓰지 않는다.

## GDJ-0096 — facade reverse selection과 outgoing FK 삭제

2026-09-22, 기준 `b91f1e8299a0a2219f6b6a8bf42e2b85937cd813` 위 제품·생성물·검사·CI **82경로**의 manifest SHA256은
`73c77e8e9477f9af63370ed458f9f6abdd8d2ad885b43ae0f0fa89750152e1b2`다. 현행 문서는 이 집합에서 제외한다.
Darwin 25.6.0/arm64·Go 1.26.5, modernc SQLite 3.53.3(v1.56.0), PostgreSQL 17.5 Homebrew/pgx v5.10.0,
`TZ=Pacific/Chatham`에서 실행했다. 각 native 시도는 별도 locale C/UTF8 소유 DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

Generated Objects가 모든 single traversal을 바인딩하며 facade의 `.Related` selector와 문자열 mixed path를 공통 eager tree에 연결한다.
정방향 storage field와 reverse traversal 목록은 별도로 소유한다. Reverse-only 모델도 New/Save가 가능하고, unsaved owner의
reverse 접근만 I/O 전에 실패한다. 부모 저장으로 PK가 생기면 reverse handle을 재바인딩한다. Forward assignment/raw FK 변경은
이미 선택한 reverse 형제 cache와 subtree를 보존한다. 상세 설계와 남은 assignment 범위는 [ADR-0073](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)을 따른다.

12개 독립 Django eager 관찰을 실제 facade의 typed/string 두 방식으로 각각 실행했다. Plan.Equal·row/presence·초기 SELECT·JOIN을
대조하고 warm All/First/Count·descendant 접근은 Query 호출과 실제 data SELECT가 모두 0임을 확인했다.
PostgreSQL tracer는 이제 fixture의 부모와 모든 자식 테이블을 관찰하며 하나의 JOIN statement는 한 번만 센다. 지연 child 접근의
SELECT 1회 positive control도 추가했다. Physical-session connection/reset guard는 유지했다.
같은 facade의 target identity, 서로 다른 occurrence/materialization의 mutable field 독립성, Distinct/Offset/Limit/Fresh/First,
새 부모 저장 후 조회·기존 missing cache·새 facade의 재조회·copy/PK 변경 거부, 잘못된 origin/중간 Go type/문자열 경로·namespace,
query/후행 scan 오류의 partial result 거부와 재시도를 검증했다. 외부 생성 module의 올바른 tree는 build되고 잘못된 중간 model은 compiler가 거부한다.

Report의 reverse 형제 cache 검사를 위해 Certificate OneToOne을 생성 fixture에 추가했다. 이 과정에서 incoming 정책을 받으면서
outgoing FK도 가진 모델의 deleter가 scalar-only 검사로 거부됨을 발견했다. Canonical 단일 FK를 허용하되 incoming fingerprint·
descriptor 전체 metadata·PK clear의 non-PK 보존·AtomicRelation은 유지했다. FK를 읽지 못하는 descriptor는 callback 전 실패한다.
실제 양 DB에서 PROTECT 실패 후 행/메모리 보존, 참조 child 제거 뒤 target 삭제, outgoing parent 행과 non-PK 메모리 보존을 확인했다.

독립 Django runner에 별도의 Owner–Report–Certificate 그래프를 추가하여 PROTECT·삭제 후 부모/메모리를 관찰했다.
기존 **37개 관찰·2개 migration·41개 lookup·12개 eager**는 그대로 유지했다. 최종 runner SHA256은
`99c1bfee3e467878b4483ed6c6fbcaddb6cd1643ef21224b25c9919b7334c1ee`다. SQLite 3.50.4·PostgreSQL 17.5 결과가 같고,
삭제 전후 행 수를 실제 GoDj 결과와 비교했다. Python의 지워진 PK `None`은 기존 Go AutoField의 값 0/부재 상태와 구분한다.
네 Python 환경(3.12.13·3.13.15·3.14.3·3.14.7)은 고정 Django 6.1/asgiref 3.12.1/sqlparse 0.5.5로 각각 **6 PASS / skip 0**,
총 **24 PASS**다. 두 hash seed의 새 process와 test discovery/start/stop를 확인했다. PostgreSQL reference driver는 psycopg 3.3.6이다.

| 동일 82경로 source의 범위 | 실행 결과 |
|---|---|
| query/ORM/codegen/queryplan/생성 fixture normal | 1,187 run=PASS, skip 0 |
| native PostgreSQL OneToOne/RelationDelete selector | 90 run=PASS, skip 0 |
| query/ORM/queryplan/fixture race | 843 run=PASS, skip 0 |
| 양 DB 영향 selector race | 2,327 run=PASS, skip 0 |
| 공통·양 DB 영향 selector CGO=0 | 2,769 run=PASS, skip 0 |
| 기존·신규 generated consumer/외부 compile normal | 183 run=PASS, skip 0 |
| publication parent root 전체 | 169 run=PASS, skip 0 |

Normal·race·CGO0은 `go test -json -count=1 -timeout=15m`, 소비자는 `-timeout=20m`다. Normal 공통 package는
`./query ./orm ./codegen ./db/internal/queryplan ./conformance/onetoonefixture/...`, native normal은
`-run 'OneToOne|RelationDelete' ./db/postgres`이며 실제 parent root 5개가 실행됐다. 이 native normal은 PostgreSQL 전체나 process 검증이 아니다.
Runtime race는 공통에서 codegen을 제외한다. DB race와 CGO0 selector는
`OneToOne|Relation|Select|Eager|Projection|Ordering|Compile|Membership|MigrationCapabilities`이고 CGO0은 runtime와 양 DB에 적용했다.
소비자는 `./codegen/consumertest ./internal/compiletest`, publication은 발견한 parent root 72개 전부다.
`TestPublicationCrashHelper`는 기존 crash/recovery parent가 실제 child process 실행·종료·결과를 소유하며 단독 helper skip은 집계하지 않았다.
각 실행의 source 전후 hash, 모든 Go event의 start/terminal·package 종료·required root·skip 0을 검사했다.

Object v5/selection v6/facade v9에 맞춰 네 project의 generated Go 56개와 manifest를 재생성했다. Candidate compile와
`make generate-check`, CI 도구 38 PASS를 확인했다. 새 SQLite/PostgreSQL facade root와 외부 compile root를 CI required 목록에 연결했다.

첫 normal은 공통 **1,185 run / 1,184 PASS / 1 FAIL**, native **88 run / 87 PASS / 1 FAIL**이었다. 두 DB에서 같은 scalar-only
삭제 binding 제약을 발견했다. 이를 수정한 중간 normal은 공통 1,186/native 89 PASS였고, 최종 독립 삭제 관찰과 tracer positive control을
추가한 뒤 위 표의 최종 source로 다시 검증했다. 실패 source를 최종 PASS로 바꾸지 않았다.
정확한 command·source·reference/Python/Go stdout/stderr·필수 root·완전한 inventory·publication과 cleanup 영수증은
`godj-one-to-one-facade-rxl9j3bm` 로컬 artifact에 보존했다. 소유 DB의 잔여 connection·table·test schema가 0인 것을 확인한 뒤
그 DB만 삭제했고 기존 PostgreSQL service는 유지했다.

구현 source `75bb34db06154c0b58b520605c2c4382e73af7a4`의 [Hosted fast run 35636450469](https://github.com/progresshans/godj/actions/runs/35636450469)이 성공했다.
Source·job/step·실행 로그를 보존했다. `make quick` 범위이며 native PostgreSQL이나 전체 platform 검증으로 계산하지 않는다.
현행 문서 139개의 링크·format·diff 검사도 통과했다.

이번 결과는 facade와 해당 삭제 경계의 로컬 checkpoint다. OneToOne assignment 전체·작업 보고서의 실제 입력 소비자·platform 통합은 남아 있다.
이전 Hosted full source `42ae95d3b1a891e6a0692fb0399968e483f4d907`의 성공을 이번 source에 적용하지 않는다.

## GDJ-0096 — typed reverse/mixed eager tree

2026-09-22, 기준 `6d4e760da554b91392634e07b911c5f7e7293683` 위 제품·생성물·검사·CI **104경로**의 manifest SHA256은
`43fcd20494825ffbe452cfe82c3046850bf98c5ed66c3b20be0e0a2671c42725`다. 현행 문서는 이 집합에서 제외한다.
Darwin 25.6.0/arm64·Go 1.26.5, modernc SQLite 3.53.3(v1.56.0), PostgreSQL 17.5 Homebrew/pgx v5.10.0,
`TZ=Pacific/Chatham`에서 검사했다. PostgreSQL은 각 시도가 소유한 locale C/UTF8 DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

공통 selection을 RelatedSelect/RelatedSelection/RelatedSelectQuery/RelatedSelected로 갱신하고 forward와 OneToOne reverse를
같은 scanner·evaluation·cache publication 경로에 연결했다. Generated reverse selector/FromSelected와 한 project binding을
공유하는 BindObjectsIn/BindReverseObjectsIn을 사용한다. 모델 projection의 물리 expression은 query layer에서 만들며 기존
scalar DTO/order API를 암묵적으로 넓히지 않았다. 상세 의미와 남은 facade 경계는 [ADR-0073](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이 소유한다.

독립 Django runner의 기존 **37개 관찰·2개 migration·41개 lookup**을 그대로 유지하고 **12개 eager 경로**를 추가했다.
최종 runner SHA256은 `1bce641fa7a256227c525dd7064ff7444e18d4d7df6f50a346ad761c1f55c6dc`다.
SQLite 3.50.4·PostgreSQL 17.5의 row/presence·초기 SELECT·JOIN 관찰이 같고 실제 GoDj 양 DB와 대조했다.
Required/nullable reverse, 모두 NULL인 child와 missing child, 여러 child의 같은 FK 이름, filter의 INNER/LEFT 전환,
reverse→forward→reverse·반복 선언의 전체 prefix를 포함한다. Django의 세 reciprocal path는 값 접근 중 추가 SELECT가 1·1·3회이며
GoDj는 명시적으로 선택한 subtree의 warm I/O 0을 유지한다. 이 차이는 [DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)에 기록했다.

Go 검사는 cold Count·warm All/First/Count, generated bridge, mutable field clone·pointer-copy 거부, 16개 동시 All의 단일 평가,
다른 binding의 descendant 거부·취소·Limit 0의 실제 SQL 0회를 포함한다. Reverse Fresh는 부재 뒤 insert와 기존 child의 이동·교체를
owner 기준으로 다시 읽는다. 잘못된 FK·부분 child·다른 child/presence의 같은 owner·후행 잘못된 행·rows-close/scan/query 실패는
partial result 없이 실패하고 같은 query의 재시도가 성공한다. 반복 owner/동일 child와 명시적 PK 0은 허용한다.
없는 ancestor 아래의 present descendant도 거부한다. 실제 SQL은 SQLite QueryCount와 production session guard를 유지한 pgx tracer로 센다.

| 동일 104경로 source의 범위 | 실행 결과 |
|---|---|
| query/ORM/codegen/queryplan/생성 OneToOne fixture normal | 1,165 run=PASS, skip 0 |
| SQLite 전체 normal | 2,625 run=PASS, skip 0 |
| PostgreSQL parent root 전체 normal | 2,486 run=PASS, skip 0 |
| query/ORM/queryplan/fixture race | 821 run=PASS, skip 0 |
| 양 DB 영향 selector race | 2,308 run=PASS, skip 0 |
| 공통·양 DB 영향 selector CGO=0 | 2,728 run=PASS, skip 0 |
| 기존 generated consumer/외부 compile normal | 182 run=PASS, skip 0 |
| publication parent root 전체 | 169 run=PASS, skip 0 |

Normal·race·CGO0은 `go test -json -count=1 -timeout=15m`, 기존 소비자는 `-timeout=20m`다.
Normal package는 `./query ./orm ./codegen ./db/internal/queryplan ./conformance/onetoonefixture/...`, SQLite는 `./db/sqlite` 전체다.
PostgreSQL은 발견한 parent root 163개 전부를 전체 이름 일치 정규식으로 실행했다. Runtime race는 normal 공통에서 codegen을 제외한다.
DB race와 CGO0 selector는 `OneToOne|Relation|Select|Eager|Projection|Ordering|Compile|Membership|MigrationCapabilities`이며
CGO0은 runtime package와 양 DB에 적용했다. 기존 소비자는 `./codegen/consumertest ./internal/compiletest`다.
Publication은 발견한 parent root 72개 모두를 실행했다. PostgreSQL RevisionFenceHelperProcess와 PublicationCrashHelper는
각각 기존 cross-process/recovery parent가 실행·종료·결과를 소유한다. 단독 helper skip은 성공으로 세지 않는다.
모든 Go event의 start/terminal·package 종료·필수 root·skip 0과 실행 전후 source 불변을 검사했다.

생성 ABI reverse v3/object v4/selection v5/facade v8에 맞춰 네 프로젝트의 generated Go 56개와 manifest를 갱신했고
candidate compile·`make generate-check`를 통과했다. 네 Python 환경(3.12.13·3.13.15·3.14.3·3.14.7)은 고정 Django 6.1,
asgiref 3.12.1/sqlparse 0.5.5로 각각 **5 PASS / skip 0**, 총 **20 PASS**다. 두 hash seed의 fresh process를 비교하고
test discovery/start/stop의 정확한 일치를 확인했다. PostgreSQL reference driver는 pinned psycopg 3.3.6이다.
새 SQLite/PostgreSQL eager root는 각 CI required 목록에도 연결했다. CI 도구 38 PASS, 현행 문서 139개 링크·format·diff 검사도 통과했다.

초회 normal은 공통 **1,165 run / 1,149 PASS / 16 FAIL**, PostgreSQL **2,486 run / 2,471 PASS / 15 FAIL**이었다.
물리 selected column 구성이 여전히 forward-only scalar constructor를 사용했다. 이를 공통 모델 projection expression으로 연결했다.
같이 발견한 object golden의 잘못된 fixture import 경로를 수정했다. 첫 SQLite 전체 2,625건은 성공했지만 최종 source에서 다시 실행했다.
실패 결과를 최종 PASS로 바꾸지 않았으며 두 시도의 로그와 source를 보존했다.
생성 초기에는 직접 변경된 generated bytes를 publisher가 거부했다. 이번 turn의 기계적 rename과 정확히 일치하는 파일만 이전 byte로
복구한 뒤 canonical generator로 재생성했다. 이어 empty reverse project의 불필요한 binding API가 candidate compile에서 거부되어
해당 empty branch를 정리했다. 실패 후보를 기존 정상 생성물에 덮어쓰지 않았다.

정확한 명령·source manifest·reference/Python/Go stdout·stderr·필수 root·완전한 inventory·generation 시도와 cleanup 영수증은
`godj-one-to-one-eager-gw4dgolk` 로컬 artifact에 보존했다. 소유한 PostgreSQL DB의 잔여 connection·table·test schema가 0인 것을
확인한 뒤 그 DB만 삭제했고 기존 service는 유지했다.
구현 source `afa6936eaf0a31ffc1cfcc754547c51b3c8787ee`의 [Hosted fast run 35632382953](https://github.com/progresshans/godj/actions/runs/35632382953)이 성공했다.
Source·job/step·실행 로그를 보존했다. `make quick` 범위이며 native PostgreSQL이나 전체 platform 검증으로 계산하지 않는다.

현재 scope는 typed reverse/mixed eager의 로컬 checkpoint다. Facade reverse selector·문자열 mixed path·assignment·Helpdesk 입력 소비자와
그 소비자를 포함한 platform milestone은 남아 있다. 기존 Hosted full source `42ae95d3b1a891e6a0692fb0399968e483f4d907`의
성공을 이번 source로 옮기지 않는다.

## GDJ-0096 — 단일 reverse 조건과 nullable JOIN

2026-09-22, 기준 `9dc09d94a9aa2a60d1b481bc060588e4e2ac1cd7` 위 제품·생성물·검사·CI **97경로**의 manifest SHA256은
`bf9076a41aef591fa11793d4efedf65b17202364aa489fe32c26b7b0e505f01b`다. 이후 현행 문서 변경은 이 집합에서 제외한다.
Darwin 25.6.0/arm64·Go 1.26.5, modernc SQLite 3.53.3(v1.56.0), PostgreSQL 17.5 Homebrew/pgx v5.10.0에서 실행했다.
Go 검사는 `TZ=Pacific/Chatham`, native PostgreSQL은 locale C/UTF8 소유 임시 DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

단일 reverse의 관계/field isnull, nullable scalar·Boolean, 비교·IN·문자열 검색과 AND/OR/NOT를 typed/dynamic 공통 AST에 연결했다.
Required FK의 reverse도 자식이 없을 수 있으므로 physical Nullable과 traversal Optional을 구분한다.
조건의 존재 증명에 따라 INNER/LEFT OUTER를 선택하고 nullable negation을 보정한다. 같은 FK의 반대 방향이 OneToOne 여부를
다르게 선언하면 SQL 전에 거부한다. 일반 FK+Unique의 collection 문법을 확장한 것으로 처리하지 않는다.

독립 Django runner의 조회용 모델을 기존 lifecycle 모델과 분리하여 기존 **37개 관찰·2개 migration 결과를 그대로 유지**했다.
추가한 **41개 조건**은 양 DB에서 ID 순서·SELECT 수·INNER/LEFT OUTER 개수가 모두 일치했다. Runner SHA256은
`77a31fd96df45a33f738c24a15b26a04a3af353f26287a6177eb62886d690815`다. 이 runner는 GoDj나 기대 파일을 읽지 않는다.
PostgreSQL reference에는 고정 psycopg 3.3.6을 별도 uv overlay로 사용했다.

생성된 Ticket–Report/Review 소비자를 양 DB에서 실행하여 41개 조건의 typed/dynamic Plan.Equal과 실제 결과를 비교했다.
Cold Count, required/nullable FK의 reverse 부재, nullable Boolean false와 NULL, 빈 IN과 그 부정, literal `%_` 검색,
같은/다른 reverse·root scalar·collection exact 조건의 조합을 포함한다. SQLite QueryCount와 PostgreSQL driver tracer로
실제 SELECT를 관찰했다. PostgreSQL tracer의 connection/reset은 기존 physical-session 검사를 유지한다.
잘못된 dynamic 값·collection OR/NOT·정책 거부·취소는 I/O나 partial predicate 없이 실패했다.

Reverse generator ABI를 v2로 바꾸고 각 generator role version이 snapshot에 반영되는지 확인했다.
네 checked-in project의 generated Go **56개**와 manifest를 재생성했으며 `make generate-check`가 모두 clean이고
기존 relation product의 checked-in generated 검사도 성공했다. Presence method와 source field Go 이름 IsNull의 충돌을 거부한다.
기존 generated union/mixed snapshot, 부모/자식 Go mode, 외부 facade compile와 publication recovery를 함께 확인했다.

| Source/범위 | 실행 결과 |
|---|---|
| ABI 갱신 전 normal 공통 query/ORM/codegen/queryplan/fixture | 1,124 run=PASS, skip 0 |
| 같은 source의 전체 SQLite | 2,625 run=PASS, skip 0 |
| 같은 source의 native PostgreSQL parent root 전체 | 2,461 run=PASS, skip 0 |
| 최종 97경로 source의 공통 normal | 1,137 run=PASS, skip 0 |
| 최종 source의 query/ORM/queryplan/fixture race | 793 run=PASS, skip 0 |
| 최종 source의 양 DB 영향 selector race | 783 run=PASS, skip 0 |
| 최종 source의 공통·양 DB 영향 selector CGO=0 | 1,097 run=PASS, skip 0 |
| 최종 source의 기존 generated consumer/외부 compile | 182 run=PASS, skip 0 |
| 최종 source의 publication parent root 전체 | 169 run=PASS, skip 0 |

ABI 갱신 전 **50경로** source manifest는 `bf0641a0f56b021dfc0b2e7d5162e7e01191b3e27cab1006584ddfefb81bd69f`다.
이후 변경은 reverse ABI version·golden/ABI 검사와 네 프로젝트 재생성·generate-check 연결이다. 실행 전후 source hash와
각 Go event의 시작/완료·package 종료·필수 root·skip을 검사했다. 각 묶음은 다음 명령과 범위를 사용한다.

```sh
go test -json -count=1 -timeout=15m ./query ./orm ./codegen ./db/internal/queryplan ./conformance/onetoonefixture/...
go test -json -count=1 -timeout=15m ./db/sqlite
# -list로 발견한 PostgreSQL parent root 모두를 이름의 전체 일치 정규식으로 실행
go test -json -count=1 -timeout=15m -run '<all discovered parent roots>' ./db/postgres
go test -race -json -count=1 -timeout=15m ./query ./orm ./db/internal/queryplan ./conformance/onetoonefixture/...
go test -race -json -count=1 -timeout=15m -run 'OneToOne|Relation|Boolean|Membership|Compile|MigrationCapabilities' ./db/sqlite ./db/postgres
CGO_ENABLED=0 go test -json -count=1 -timeout=15m -run 'OneToOne|Relation|Boolean|Membership|Compile|MigrationCapabilities' ./query ./orm ./db/internal/queryplan ./conformance/onetoonefixture/... ./db/sqlite ./db/postgres
go test -json -count=1 -timeout=20m ./codegen/consumertest ./internal/compiletest ./internal/projectgenerate
# Publication도 발견한 parent root 전체를 실행
go test -json -count=1 -timeout=15m -run '<all discovered parent roots>' ./internal/projectgenerate
make generate-check
```

Native process helper `TestPostgresRevisionFenceHelperProcess`와 publication helper `TestPublicationCrashHelper`는 단독 top-level
환경에서 의도적으로 skip하며 parent가 별도 process로 호출한다. 초회 whole-package 실행에서 각각 그 skip이 잡혔으므로
그 집계를 skip 0 PASS로 사용하지 않았다. 최종 parent 목록은 discovery에서 해당 helper 하나만 제외하고 전부 실행했다.
발견/선택한 PostgreSQL parent root는 162개, publication parent root는 72개다.
`TestPostgresRevisionFenceCrossProcessIntegration`과 `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`가
helper process의 실행·종료·결과를 소유한다. 정규식 원문과 발견/선택 목록은 artifact에 보존했다.
Combined consumer 실행의 두 기존 소비자 package는 별도로 완전한 182 PASS inventory를 확인했고,
publication은 위 재실행의 169 PASS를 사용한다. Child helper의 skip을 feature test 성공으로 바꾸지 않았다.

네 Python 환경은 고정 Django 6.1·asgiref 3.12.1·sqlparse 0.5.5로 같은 reference test 네 개를 실행했다.
각 test discovery/start/stop가 정확히 한 번이며 hash seed 0·813의 새 process를 비교했다.
Python 3.12.13/SQLite 3.50.4, 3.13.15/3.53.1, 3.14.3/3.50.4, 3.14.7/3.53.1이 각각 **4 PASS / skip 0**, 총 **16 PASS**다.
CI 도구 38 PASS도 확인했다. 새 SQLite lookup root와 PostgreSQL lookup root를 각 CI owner의 required 목록에 연결했다.
현행 문서 139개의 local 링크·format·diff 검사도 통과했다.

초회 제품 비교는 공통 **1,124 run / 1,120 PASS / 4 FAIL**, DB **5,087 run / 5,084 PASS / 2 FAIL / helper skip 1**이었다.
새로 유효해진 `missing__isnull` 문법은 unknown-relation 진단을 반환하므로 기존 unsupported-suffix 기대를 갱신했다.
빈 IN은 실제 SQL을 생략했지만 테스트가 framework Query 호출을 SELECT로 세고 있었다. 이를 실제 SQL 관찰로 수정한 뒤
양 DB의 0 SELECT를 확인했다. 제품의 empty-result 검증/실행 경로를 완화하지 않았다.
기존 생성 소비자 182건은 이 첫 시도와 최종 ABI source에서도 각각 성공했다.

초기 reference 확장에서는 기존 Parent에 Review를 붙여 deletion collector의 SELECT 수 두 항목이 증가했다.
이 결과를 기대 fixture로 채택하지 않고 조회 모델을 분리하여 기존 관찰 불변을 확인한 뒤 새 결과만 추가했다.
Psycopg overlay 준비와 uv 설치 진단도 runner 결과와 구분했다. 실패/성공 stdout·stderr, source manifest·필수 root·완전한 inventory와
DB cleanup 영수증은 `godj-one-to-one-query-iu70zs2r` 로컬 artifact 디렉터리에 보존했다.
모든 소유 임시 PostgreSQL DB는 잔여 connection·user table, Go checkpoint의 test schema도 0임을 확인한 뒤 삭제했다. 기존 service는 유지했다.

구현 source `6d4e760da554b91392634e07b911c5f7e7293683`의 [Hosted fast run 35628726940](https://github.com/progresshans/godj/actions/runs/35628726940)이 성공했다.
Source·job/step와 실제 실행 로그를 보존했다. `make quick` 범위이며 native PostgreSQL이나 전체 platform 검증으로 계산하지 않는다.

현재 결과는 직접 단일 reverse 조건의 로컬 검증이다. Reverse eager·혼합 traversal·assignment 전체·Helpdesk 입력 소비자는
[GDJ-0096](../../work/0096-one-to-one-service-reports.md)에 남아 있다. 기존 37개 관찰 전체의 제품 parity나 현재 source의 Hosted full을 주장하지 않는다.

## GDJ-0096 — Hosted fast에서 확인한 capability 검사 누락

2026-09-22, 기반 구현 source `ee0f0c7601e579c43d70befb7ffa21ac51c76c9f`의
[빠른 CI run 35621606448](https://github.com/progresshans/godj/actions/runs/35621606448)이 실패했다.
`TestSQLiteMigrationCapabilities`의 기존 expected struct에 새 `AlterFieldRelation=true`가 빠져 있었고,
앞선 이름 기반 로컬 DB selector는 이 root를 포함하지 않았다. 실제 capability와 그 migration 실행은 앞선 체크에서 검증했으나
전체 SQLite suite의 이 회귀를 놓쳤다. 이 run을 PASS로 사용하지 않는다.

제품 동작을 바꾸지 않고 해당 expected capability를 추가했다. 수정한 `db/sqlite/migration_relation_test.go`의 SHA256은
`06d710b09518a07cbc00ef1886aa7ee541e7f687c2137e1713052abfbede731c`다.
같은 로컬 환경에서 `go test -json -count=1 -timeout=15m ./db/sqlite` 전체를 실행하여 **2,625 run=PASS / skip 0**을 확인했다.
완전한 종료 inventory와 capability/OneToOne migration 필수 root·검사 전후 source hash를 대조했다.
관련 ADR에는 기존 RelatedObject의 완전한 cardinality 위반 snapshot과 I/O 실패의 partial result 차이도 명시했다.
이는 기존 runtime 의미의 설명이며 별도 cache 정책 변경이 아니다.

수정 source **`51fff1f4447e5f83936f9864d7252c230ac0d0ce`**의
[Hosted fast run 35622125004](https://github.com/progresshans/godj/actions/runs/35622125004)이 성공했다.
Source·job/step 결과·실제 SQLite package 성공 출력을 대조했다. `make quick`의 빠른 Go 검증이며
문서-only 분기는 해당하지 않아 선택되지 않았다. Native PostgreSQL·전체 platform 검증으로 계산하지 않는다.

## GDJ-0096 — OneToOne 선언·이력·단일 reverse 기반 checkpoint

2026-09-22, 기준 `0b98ea1d7fb190ef2d6de48e68d2cc7f9ca39ff6` 위 제품·생성물·테스트·CI **108경로**의 최종 manifest SHA256은
`9c558328985c2f370638202a44cf1851b0ace696cf83544fa12ca0ec63d56eaa`다. 문서는 이 source 집합에서 제외했다.
Darwin **25.6.0/arm64**, Go **1.26.5**, modernc SQLite **3.53.3**(module **v1.56.0**),
PostgreSQL **17.5 Homebrew**/pgx **v5.10.0**, `TZ=Pacific/Chatham`에서 실행했다.
PostgreSQL은 시도별로 소유한 UTF8/locale C 임시 DB와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.

구현 범위는 [ADR-0073](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이다.
`schema.OneToOne`·Unique와 구분되는 cardinality·default/named/hidden reverse·historical wire/digest·autodetect를 연결했다.
FK→OneToOne과 FK+Unique→OneToOne, reverse 이름 변경/숨김, 역방향 복구를 검증했다.
양 DB의 실제 중복으로 UNIQUE 추가가 실패하면 행·물리 catalog·recorder/revision이 유지되며 명시적 수정 후 재시도·재접속했다.
SQLite에서는 sequence/FK 상태도 비교했고 미지원 `AlterFieldRelation` capability의 실행 전 거부를 확인했다.

새 [cross-app fixture](../../conformance/onetoonefixture/schema.go)의 실제 생성 타입과 공통 소비자는 양 DB에서 다음을 검증한다.

- Required/nullable OneToOne과 일반 Unique FK의 single/collection API 구분, 정상 부재의 warm cache·외부 insert/Fresh,
  16개 동시 Get의 단일 평가, pointer-copy 거부, unsaved owner와 명시적 PK 0 구분.
- Reverse exact의 typed/dynamic 결과, forward eager의 한 SELECT와 warm 접근, reverse prefetch의 한 batch·빈 입력 no-I/O·반복 owner 독립 cache.
- 주입한 두 자식 행의 cardinality 오류, batch 전체 실패의 partial publication 방지, rows Close 실패 후 재시도,
  취소 전 I/O 거부와 query 실패 후 재시도.
- 실제 중복 insert와 선행 update의 transaction rollback, 여러 SQL NULL, PROTECT와 SET_NULL 뒤 자식 행 보존.
  PostgreSQL에 추가한 AtomicRelation은 일반 Atomic의 callback/session/rollback/unknown-outcome 엔진을 공유한다.
  별도 driver control은 callback 횟수·종료된 session 거부·commit/rollback 원인 보존과 SET_NULL parameterization을 검사한다.

| 실행 묶음 | 테스트가 실행된 package | run=PASS | skip |
|---|---:|---:|---:|
| 공통 schema/query/ORM/codegen/migration/autodetect·새 generated fixture, normal | 10 | 1,808 | 0 |
| 양 DB OneToOne/Unique/Relation/Atomic, normal | 2 | 642 | 0 |
| Query/queryplan/ORM·새 generated fixture, race | 4 | 748 | 0 |
| 양 DB 같은 selector, race | 2 | 642 | 0 |
| 공통·양 DB 영향 selector, CGO=0 | 12 | 1,087 | 0 |
| 기존 생성 소비자·외부 compile 회귀, normal | 2 | 182 | 0 |

실제 명령은 다음과 같다. 모두 `-json -count=1`이며 공통/DB는 `-timeout=15m`, 기존 생성 소비자는 `-timeout=20m`다.

```sh
go test -json -count=1 -timeout=15m ./schema/... ./query ./orm ./codegen ./migrations/... ./internal/migrationautodetect ./conformance/onetoonefixture/...
go test -json -count=1 -timeout=15m -run 'OneToOne|Unique|Relation|Atomic' ./db/sqlite ./db/postgres
go test -race -json -count=1 -timeout=15m ./query ./db/internal/queryplan ./orm ./conformance/onetoonefixture/...
go test -race -json -count=1 -timeout=15m -run 'OneToOne|Unique|Relation|Atomic' ./db/sqlite ./db/postgres
CGO_ENABLED=0 go test -json -count=1 -timeout=15m -run 'OneToOne|FieldChange|AlterField|Relation|Atomic|Unique' ./schema/... ./query ./db/internal/queryplan ./orm ./migrations/... ./internal/migrationautodetect ./conformance/onetoonefixture/... ./db/sqlite ./db/postgres
go test -json -count=1 -timeout=20m ./codegen/consumertest ./internal/compiletest
```

첫 normal 성공 source의 **107경로** manifest는 `e439ebcc229485b11a478286206c831a8281ec1076aa016b737495e8b3d5899a`다.
그 뒤 prefetch GoDoc과 CI package 소유권 검사만 바꾼 race/CGO0/소비자 source **108경로**는
`bafbf35c68b0ba6e8de4bf0624f567210fb8830018638213ab58f641b92f5c03`다.
각 시도의 시작/종료 source hash, Go event의 package 종료·test run/pass 집합·필수 root와 skip 0을 검사했다.
새 generated drift·SQLite product, PostgreSQL product/alter/AtomicRelation, SQLite alter와 기존 generated union/mode/외부 facade
필수 root가 실제 실행됐음을 확인했다. 단순 명령 종료값이나 이름 목록만을 실행 증거로 삼지 않았다.

이후 [DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)의 reciprocal cache 차이에 명시적 소비자 검사를 추가했다.
Reverse에서 얻은 같은 모델로 만든 두 forward handle이 각각 한 번 읽고 각 handle의 반복 접근은 warm임을 확인했다.
제품 코드는 동일하며 변경한 소비자와 drift를 아래 명령으로 normal·race·CGO=0 각각 **3 run=PASS / skip 0** 재검증했다.
이 시점의 문서를 포함한 **113경로** manifest는 `7fcad69b9815ebf1a847ab3e7841205e5fb60f0b6939a6c6a4f34b071c32c962`이고,
문서를 제외한 최종 108경로가 이 절 첫 manifest와 같다.

```sh
go test -json -count=1 -timeout=5m -run 'OneToOne.*(GeneratedProduct|FixtureMatchesDeclaration)' ./conformance/onetoonefixture/... ./db/postgres
go test -race -json -count=1 -timeout=5m -run 'OneToOne.*(GeneratedProduct|FixtureMatchesDeclaration)' ./conformance/onetoonefixture/... ./db/postgres
CGO_ENABLED=0 go test -json -count=1 -timeout=5m -run 'OneToOne.*(GeneratedProduct|FixtureMatchesDeclaration)' ./conformance/onetoonefixture/... ./db/postgres
```

CI 도구 **38 tests PASS**에서 새 fixture와 하위 package가 관계 owner가 있을 때 중복되지 않고, 없을 때 portable owner에
포함되며 비슷한 이름의 이웃 package를 숨기지 않음을 확인했다. Relation required 목록에 새 fixture/SQLite root를,
PostgreSQL core required 목록에 새 native product/alter root를 추가했다. 32-bit relation package 목록에도 fixture를 넣었다.
실제 `go list`의 fixture 5개 package가 같은 owner 선택에 포함되고, 기존 PostgreSQL required root는 모두 유지되며
추가한 두 root가 해당 normal/race/CGO0 inventory에 존재함을 최종 source와 대조했다.
이는 Hosted 설정 변경이며 새 Hosted 실행 성공을 뜻하지 않는다.
16개 generated 파일과 manifest의 drift 검사를 통과했다. 앞선 저장소 전체 compile-only는 166 package 결과로 종료했으며
전체 runtime/platform 검사로 계산하지 않는다.

초기 normal 시도는 성공으로 사용하지 않았다. 첫 시도는 공통 **1,807 run / 1,804 PASS / 3 FAIL**, DB **642 run / 641 PASS / 1 FAIL**이었다.
Negative wire control의 치환이 실제 문서를 바꾸지 않은 문제, 새 capability field 개수, 새 소비자의 정렬 없는 First 사용을 수정했다.
둘째 시도는 공통 **1,807 PASS**였으나 SQLite capability 거부 검사가 `migrations.Error`에 없는 Is 동작을 기대하여
DB **642 run / 639 PASS / 3 FAIL**이었다. 실제 오류의 Category/Code를 `errors.As`로 검사하도록 고쳤다.
새 AST cardinality 검사까지 포함한 최종 결과가 위 표다. 기대 제품 의미를 완화하거나 실패 기록을 덮어쓰지 않았다.

로컬 artifact 디렉터리 `godj-one-to-one-foundation-9n3m837g`에 source manifest·환경·전체 JSON log/stderr·필수 root·종료 inventory와
시도별 cleanup 영수증을 보존했다. 모든 소유 임시 PostgreSQL DB는 다른 connection·user table·test schema 각각 **0** 확인 후 삭제했다.
기존 PostgreSQL service는 유지했다. 현행 문서 **139개**의 local 링크·format·diff 검사도 통과했다.

현재 변경은 로컬 영향 범위의 검증이며 Hosted full이나 양 DB의 모든 Django 관찰 parity가 아니다.
Reverse isnull·OR/NOT·넓은 lookup/eager, assignment 전체와 Helpdesk Form/Admin/API/OpenAPI/client는
[활성 작업](../../work/0096-one-to-one-service-reports.md)에 남아 있다. 마지막 Hosted full의 source는 아래 `42ae95d3`이며 이 변경의 PASS로 옮기지 않는다.

## GDJ-0096 — 일대일 관계의 독립 기준 관찰

2026-09-21, 기준 `42ae95d3b1a891e6a0692fb0399968e483f4d907` 위 독립 runner·검사·양 DB raw fixture **4경로**의
manifest SHA256은 `ef22e3c9d94f0af349af81bf78a7ce93d8653042dcbb9ace66768726a756902a`다.
Runner 자체의 SHA256은 `e5aa2df23278e4e6f65f72df2db1f576c23f8cc1569e2ef5dc2db07c7290873f`다.
[독립 runner](../../conformance/runners/django/one_to_one_reference.py)는 GoDj·기대 fixture를 읽지 않으며
Django **6.1**, Python **3.14.3**의 public ORM·ModelForm·historical state/autodetector/schema editor를 사용한다.

- SQLite **3.50.4**와 PostgreSQL **17.5 Homebrew**/psycopg **3.3.6**에서 cross-app 관계의 **37개 동작과 2개 migration 경로**를 각각 실행했다.
  Required/nullable·명시적/default/hidden reverse, 단일 객체/부재·warm cache/refresh·reciprocal cache, eager/prefetch,
  filter/exclude/OR, Form의 중복/자기 행/선택 오류, 실제 중복 저장·rollback·PROTECT/SET_NULL과 assignment/save를 관찰했다.
  FK+Unique의 reverse는 collection이며 OneToOne의 단일 객체 의미와 다르다.
- 일반 FK에 중복 행이 있으면 OneToOne 변경이 실패하며 행과 물리 제약이 그대로다. 명시적으로 중복을 수정한 뒤
  재시도·재접속하면 실제 UNIQUE가 생긴다. Reverse는 원래 비고유 FK를 복구한다. 기존 FK+Unique의 변경도 별도
  AlterField로 감지하며 역방향 변경 뒤 고유성은 유지한다. 양 경우 처음/실패 후/reverse의 제약과 행을 실제로 비교했다.
- 양 DB의 37개 결과·오류·SELECT 수는 같았다. DBAPI transaction 시작문의 execute-wrapper 포착 차이와 각 backend의
  물리 constraint 이름/구조는 raw에 보존했다. SQLite에서 보이는 BEGIN이 PostgreSQL capture에 없다는 이유로 rollback을 추론하지 않는다.
  실제 rollback 후 행을 다시 읽어 확인했다. 전용 PostgreSQL DB는 잔여 user table·다른 connection 각각 **0** 확인 후 삭제했고 기존 service는 유지했다.

Python reference 검사는 각 환경에서 서로 다른 hash seed **0·813**의 새 subprocess 결과를 저장 fixture와 비교한다.
CI와 같은 완전 실행 검사로 세 test가 각각 한 번 시작/종료되고 skip/failure가 없음을 확인했다.

| Python | 실제 SQLite | 결과 |
|---|---|---|
| 3.12.13 | 3.50.4 | 3 PASS, skip 0 |
| 3.13.15 | 3.53.1 | 3 PASS, skip 0 |
| 3.14.3 | 3.50.4 | 3 PASS, skip 0 |
| 3.14.7 | 3.53.1 | 3 PASS, skip 0 |

총 **12 PASS**는 새 독립 reference의 재현 검사다. 문서 **138개**의 local 링크·format·diff 검사도 통과했다.
Source `f2dbbc0411241d643e05edb79ab7787d94006333`에서 CI의 이전 native SQLite 버전도 추가 확인했다.
아래 JSON portability 검사에서 공식 source hash를 검증해 빌드한 **SQLite 3.45.1**을 Homebrew Python **3.13.3**의
subprocess에만 연결해 같은 세 검사 **3 PASS / skip 0**을 확인했다. 실제 runtime fingerprint를 먼저 검사했으며 기대값 수정은 없었다.
GoDj OneToOne 제품 구현이나 그 플랫폼 검증의 PASS가 아니다.
새 runner는 기존 Python test discovery에 포함된다. 이후 제품의 IR·migration·generated ORM·입력 소비자 연결은
[활성 작업](../../work/0096-one-to-one-service-reports.md)에 남아 있다.

## GDJ-0095 — 고정 source의 Hosted full 완료

2026-09-21, source **`42ae95d3b1a891e6a0692fb0399968e483f4d907`**의
[CI run 35607632806](https://github.com/progresshans/godj/actions/runs/35607632806), `workflow_dispatch`, `suite=full`, **attempt 1**이 완료됐다.
페이지 전체의 job 목록과 source/run identity를 대조했으며 **62개 job 모두 success**, 실패·취소·누락·skipped job은 없다.
최종 `CI result (full)`의 실제 출력은 `scope=full`, `full_platform_verified=true`다.
검증한 owner 집합은 scopes.py의 선택과 일치하는 portable Go·relation product·project check·command products·
PostgreSQL·exact Darwin·Python compatibility·conformance의 **8개**다.

- PostgreSQL **17.10** native core는 normal·race·CGO-disabled 각각 **13 package, 2,014 run=PASS, skip 0**이다.
  Exact service profile·실제 실행 inventory·clean worktree step이 모두 성공했으며 고유성의 필수 integration root 다섯 개가
  세 모드 모두의 required/no-skip 검사에 포함되어 실행됐다. 앞선 목록 누락 상태의 결과를 대신 사용하지 않았다.
- Exact darwin/arm64 profile은 Python suite **306 PASS, skip 0**이다. Python **3.12.13·3.13.15·3.14.3·3.14.7**
  compatibility는 모두 native SQLite **3.45.1**, 각각 **306 tests = 302 PASS + 4 prescribed skip**이다.
  이 네 exact-profile 전용 검사는 별도 exact job이 소유하며 compatibility의 skip을 실행 성공으로 세지 않는다.
- Conformance job은 이 run에서 성공한 두 PostgreSQL capture producer를 해석하고 해당 artifact의 source/run/attempt를 검증한 후
  실제 비교를 실행했다. Oracle checksum·생성물/reference 불변·32-bit compile/relation product step도 성공했다.
  오래된 저장 capture나 다른 source의 결과를 current attestation으로 사용하지 않았다.

초회 fixture/portable reference 실패와 중간 native root 선택 누락은 아래에 보존한다. 최종 run만 full 완료 근거다.
GDJ-0095 column uniqueness의 선언부터 양 DB·ORM·Form/Admin/API·Helpdesk/client까지의 통합을 완료 처리한다.
Composite/conditional/expression constraint·OneToOne과 전체 카탈로그의 남은 범위는 완료하지 않았다.
이후 상태 문서와 GDJ-0096 reference 추가는 별도 변경이며 이 full 결과의 source를 새 HEAD로 바꾸지 않는다.

## GDJ-0095 — Native PostgreSQL 고유성 검사의 Hosted 실행 소유권

2026-09-21, source `182543929bde8e3f7edbd954383dd643d8a02df0`의
[Hosted 재검증](https://github.com/progresshans/godj/actions/runs/35606169185)을 점검하면서 실제 선택 범위의 누락을 발견했다.
Native PostgreSQL core는 required root 목록으로 `-run`을 만들고 있었으며 새 고유성 integration root 다섯 개는 없었다.
Portable suite의 DB 없는 실행이나 기존 Helpdesk 검사를 이 다섯 native 검사의 성공으로 사용할 수 없다.

`TestPostgresUniqueReferenceWrites`, `TestPostgresUniqueConcurrentWritersAndAtomicRollback`,
`TestPostgresUniqueAlterFailurePreservesRevisionRowsAndInboundFK`, `TestPostgresUniqueNullableAddAndForeignKeyReverse`,
`TestPostgresUniquePhysicalDriftRejectsBeforeRevisionClaim`를 core required 목록에 추가했다.
같은 목록을 normal·race·CGO-disabled가 사용하며 `GODJ_REQUIRE_POSTGRES=1`과 `go_test_events.py --no-skips`가
실행 누락·skip·실패를 거부한다. 다른 기존 required root와 shard·mode 선택은 유지했다.

변경한 workflow SHA256은 `b2cb8ca1017943d790b6c61f28163783b83ee52307d1e24abe314165b60a0331`이다.
현재 integration source의 다섯 root와 실제 `go test -list` 결과·설정된 required 목록을 대조하고
추가된 선택이 정확히 이 다섯 개이며 기존 목록이 보존됨을 확인했다. 이는 선택·compile 확인이며 새 native 실행의 PASS가 아니다.
필요한 PostgreSQL 17.10 실제 DB·profile·동시 쓰기와 migration 검증은 이 workflow를 포함한 고정 source의 Hosted full이 소유한다.

## GDJ-0095 — Hosted 통합에서 발견한 소비자 fixture와 portable reference 수정

2026-09-21, source `64e6822d4a9eb524d6f0551267911dfb8ad39be1`의
[첫 Hosted full 실행](https://github.com/progresshans/godj/actions/runs/35603550365)에서 아래 통합 누락을 확인했다.
이 실행의 일부 job 성공을 full PASS로 간주하지 않는다. 후속 검증/환경 변경 **5경로**의 manifest SHA256은
`3b8c886e9e1507b1c4096b2f9a8c4acd8ce462f3d8aa2a315f7e10bbf05942b8`이며 제품 구현은 앞선 checkpoint와 같다.

- `internal/projectgenerate`의 외부 module fixture가 ORM의 새 `validation` 의존성을 연결하지 않았다.
  정상 candidate compile 전에 실패해 cleanup interruption·mandatory recovery 검사의 실제 실패 지점에 도달하지 못했다.
  의존성만 추가하고 실제 publication/복구·namespace 거부·기존 생성물 보존 검사를 유지했다.
- `internal/compiletest`의 migration/project 외부 consumer 두 fixture가 이전 `[]string` renderer ABI를 구현하고 있었다.
  현행 operation별 `[][]string` API로 갱신했으며 호환 adapter나 compile 검사의 면제는 추가하지 않았다.
- Portable Python reference는 native SQLite 버전을 무시하고 3.50.4의 quoted JSON path 결과를 모든 환경에 기대했다.
  고정 Django 6.1의 독립 runner를 공식 SQLite source로 직접 재실행해 차이가 나는 조건을 확인했다.
  3.45.1·3.46.1은 `quote_key/filter`, `has_quote_key/filter`, `has_quote_key/exclude`의 rows만 다르고 3.47.0부터는
  현재 기준과 같다. Version fingerprint 외의 SQL·매개변수·나머지 lookup/projection·containment·key-presence 결과는 동일했다.
  Portable 기대값은 이 세 조건만 버전별로 구분하고 별도 native JSON_EXTRACT/JSON_TYPE control도 검사한다.
  Runner의 raw 결과나 고정 product oracle을 덮어쓰지 않는다. CI는 연결된 SQLite runtime도 출력한다.

SQLite source는 공식 release에 게시된 sqlite3.c SHA3-256을 먼저 대조한 뒤 임시 dynamic library로 빌드했다.
Homebrew Python **3.13.3**의 subprocess에만 연결했으며 기존 SQLite/Python 설치는 변경하지 않았다.

| 독립 runtime | 공식 sqlite3.c SHA3-256 | 관찰 |
|---|---|---|
| [SQLite 3.45.1](https://sqlite.org/releaselog/3_45_1.html) | `0474604df9e1b69a5544295dd046aad954749279780d557da80f44b958100295` | 세 rows 차이 |
| [SQLite 3.46.1](https://sqlite.org/releaselog/3_46_1.html) | `186a1baa476b6d546de155160ca6d30ff7b7e6ee375f0bb6445e1a3d180a7dad` | 같은 세 rows 차이 |
| [SQLite 3.47.0](https://sqlite.org/releaselog/3_47_0.html) | `bcec3a4fbc97e973547924677332996ef64e06a6d10d22e2c2344f447536cc29` | 현재 기준과 일치 |

Go 검사는 darwin/arm64 Go 1.26.5, `TZ=Pacific/Chatham`, `-json -count=1 -timeout=15m`이다.
위 3개 Go fixture의 manifest는 `0e0d643ffd98d50ca24eae85a78b7b13aa41b32ed145a16fc5a92ce97f6a7db8`다.

| 실행 | 결과 |
|---|---|
| CGO=1 normal, `./internal/projectgenerate ./internal/compiletest` | 2 package, root 83 / 238 run, **237 PASS·helper 1 skip** |
| CGO=1 race, `./internal/projectgenerate` | root 73 / 170 run, **169 PASS·helper 1 skip** |
| CGO=0 normal, `./internal/projectgenerate ./internal/compiletest` | 2 package, root 83 / 238 run, **237 PASS·helper 1 skip** |

Skip은 직접 호출 시 빠지는 `TestPublicationCrashHelper` 하나뿐이며, 실제 subprocess crash/복구를 소유한 parent와
mandatory recovery·두 외부 consumer의 필수 실행을 따로 확인했다. 나머지 skip/fail·stderr는 0이고 run/pass/skip·terminal과 source가 일치한다.
Compile fixture는 `!race`이므로 race 검증으로 가장하지 않는다.
변경한 JSON reference 테스트는 위 native SQLite 세 환경 및 Python **3.12.13·3.13.15·3.14.3·3.14.7**에서 각각 fresh 실행해
총 **7 PASS / skip·failure 0**이다. 네 Python의 실제 SQLite는 각각 **3.50.4·3.53.1·3.50.4·3.53.1**이다.
CI 선택·수집 스크립트의 unittest **37 PASS**, affected Go vet·format·문서 링크·diff 검사도 통과했다.
새 전체 플랫폼 검증은 수정 source의 Hosted full 재실행이 소유한다.

## GDJ-0095 — Form/Admin/API 고유성 진단과 Helpdesk 수직 연결

2026-09-21, darwin/arm64 Go **1.26.5**, PostgreSQL **17.5 Homebrew**, `modernc.org/sqlite v1.56.0`,
`TZ=Pacific/Chatham`, `GODJ_REQUIRE_POSTGRES=1`. Normal/race는 **CGO_ENABLED=1**, CGO-disabled는 **0**이다.
기준 `1c2452f1e3bced1c2b300facc85425c870a29d3e` 위 제품·검증·생성물 **43경로** manifest SHA256은
`af5b3dc7cd7a16612f07fbbd560e7f624645e17c1110118de6326248e21ad16e`다.
설계와 오류 소유권은 [ADR-0072](../adr/0072-column-uniqueness-and-constraint-ownership.md)가 소유한다.

- `validation.Reject`는 안전한 표시 진단과 내부 원인을 분리하며 직접 전달된 rejection만 입력 오류로 처리한다.
  Wrapped/joined 오류·rollback 실패·unknown outcome과 취소를 숨기지 않는다. `Form.WithErrors`는 bound form을 복사하고
  거부된 필드만 cleaned data에서 제외한다. Initial/changed·나머지 값과 원래 form을 보존하며 unknown field는 거부한다.
- Admin create/update는 선택된 field/non-field 오류를 같은 form에 표시하고 제출한 UUID 별칭과 HTML 원문을 안전하게 escape한다.
  API는 HTTP 400 `validation_error`와 `unique` 진단을 반환한다. 기존 값·행 식별자·native 오류 문구는 응답에 담지 않는다.
- Helpdesk의 `schema.Unique()` 선언에서 실제 generator로 12개 Go 파일을 갱신하고 makemigrations로 **0016**을 생성했다.
  Migration SHA256은 `e31a0ffac6b39a2bdf25a417b1b9cd865c5d960fc2323fa6624d5720d5620bc1`이다.
  양 DB에서 0015의 중복을 만든 뒤 0016 실패 시 정확한 행과 migration state 보존, 명시적 데이터 수정 뒤 재시도·재접속과 실제 제약을 확인했다.
- 양 DB 실제 HTTP의 POST/PUT/PATCH와 Admin add/change에서 category를 넘어선 중복을 거부하고 다른 필드도 저장하지 않았다.
  자기 행 제외·UUID 별칭·zero UUID·생략·NULL·재접속 후 저장값을 확인했다. 다른 category의 대상 404, 권한/CSRF 403은
  고유성 조회·쓰기 전에 발생한다. 수정 전후 전체 모델 값을 비교했다.
- Advisory 조회만 빈 결과로 바꾸고 실제 native insert/update를 실행하여 저장 제약의 거부를 `__all__/unique`로 표시했다.
  이것은 **경쟁 뒤 stale read의 fault simulation**이며 이번 HTTP 검사 자체를 실제 두 writer 경쟁으로 세지 않는다.
  실제 두 pool의 경쟁 결과는 앞선 ORM/backend checkpoint에 속한다. 조회 실패와 rollback-unknown 오류를 추가한 simulation은
  API/Admin 500과 기존 데이터 보존을 확인하며 입력 오류로 축소하지 않는다.
- SQLite coordinated/relation transaction은 rollback과 connection 반환이 모두 성공하면 callback 오류를 그대로 전달한다.
  실제 파일 DB의 rollback 후 다른 backend 진입, cleanup 실패의 두 원인·unknown outcome·quarantine·취소·panic과 세션 만료 회귀를 확인했다.
- 실제 OpenAPI를 export하고 locked/offline Ogen으로 client를 재생성했다. Article 두 profile은 불변이며 Helpdesk는 설명과
  생성 client 주석만 달라졌다. Parent가 독립 module의 생성물 일치·compile·실제 HTTP·최종 DB를 확인했다.
  Parent/child의 **36개 required check**와 race 계측 receipt도 일치한다. 새 check는 duplicate create/update·자기 행·권한 범위를 소비한다.
  Generated HTTP client fixture는 **SQLite**다. PostgreSQL은 별도의 실제 Helpdesk HTTP 검사이며 둘을 같은 client 환경으로 합치지 않는다.

모든 Go 명령은 `-json -count=1 -timeout=15m`을 사용했다.
SQLite selector `S`는 `CoordinatedAtomic|AtomicRelation|RelationTransaction|RelationSession|RelationRetention|ForceDiscardRelation|Unconfirmed.*Cleanup|BackendClose`다.

| 실행 | 결과 |
|---|---|
| Normal: `./validation ./forms/... ./admin ./api/... ./examples/helpdesk ./examples/article/apiapp ./systemstate` | 12 package, root 224 / **1,751 run=PASS** |
| Normal: `-run S ./db/sqlite` | root 32 / **68 run=PASS** |
| Race: `./validation ./forms/... ./admin ./api ./examples/helpdesk ./api/openapi/consumertest` | 7 package, root 110 / **1,369 run=PASS** |
| Race: `-run S ./db/sqlite` | root 32 / **68 run=PASS** |
| CGO=0: `-run 'Rejection\|Rejected\|WithErrors\|ValidationErrorResponse\|PublicHelpdesk\|GeneratedOpenAPIClientContract' ./validation ./forms ./admin ./api ./examples/helpdesk ./api/openapi/consumertest` | 6 package, root 9 / **17 run=PASS** |
| CGO=0: `-run S ./db/sqlite` | root 32 / **68 run=PASS** |

최종 normal **1,819**, race **1,437**, CGO=0 **85 run=PASS**이며 skip/fail·stderr는 모두 **0**이다.
양 DB historical/HTTP unique subtest, external client, SQLite callback/cleanup 필수 sentinel·package terminal·run/pass roster와
각 실행 전후 동일 source manifest를 대조했다.

초기 첫 실행은 검사 클라이언트의 CSRF token 갱신 누락으로 양 DB HTTP와 child client가 실패했다. 기존 인증 흐름에 맞춰 수정했다.
두 번째 실행의 SQLite 500은 정상 rollback도 `errors.Join(primary, nil)`로 감싸 입력 오류의 소유권을 잃는 제품 결함이었다.
오류 전달을 고치고 cleanup 실패/불명확한 결과의 음성 대조를 유지했다. 최종 OpenAPI 설명의 검증 순서를 정정·재생성한 뒤
위 여섯 실행을 같은 최종 source에서 모두 수행했다. 초기 실패를 성공 기록에서 제외했다.

Affected vet, `make format-check docs-check`, `git diff --check`, Helpdesk `generate --check`·`makemigrations --check`는 PASS다.
Generated snapshot은 `9f6c990b6169cb3061a44df5bbaffb34d308c5d558c7b8ca4c85320b359cc3ee`, 12파일이며 추가 migration 후보는 0이다.
전용 PostgreSQL DB는 잔여 연결·사용자 table·test schema **각 0** 확인 후 삭제하고 기존 service는 유지했다.
이는 입력 소비자와 관련 트랜잭션의 로컬 checkpoint다. 전체 platform/cold-build·DB/process의 고정 source Hosted full 통합은 다음 milestone이다.

## GDJ-0095 — ORM 고유성 사전 검증과 실제 쓰기의 공통 입력

2026-09-21, darwin/arm64 Go **1.26.5**, native PostgreSQL **17.5 Homebrew**, SQLite driver `modernc.org/sqlite v1.56.0`,
`TZ=Pacific/Chatham`, `GODJ_REQUIRE_POSTGRES=1`. Normal/race/consumer는 **CGO_ENABLED=1**, 별도 CGO-disabled 검사는 **0**이다.
기준 `000c9ea9a55d4349e8604894e05e94657eb0f244` 위 제품·검증 **7경로** manifest SHA256은
`4f9f171a2c41b88fdff36e538292a1815379a3d40a6604bc763c08a51bb197ab`다.
설계는 [ADR-0072](../adr/0072-column-uniqueness-and-constraint-ownership.md)가 소유한다.

- `orm.Manager.ValidateUniqueCreate`/`ValidateUniqueUpdate`는 기존 생성 descriptor/input을 사용하고 실제 Create/Update와
  mutation 준비·타입/metadata·필수값·생략 필드 보존·PK 검사를 공유한다. `validation.Errors`와 실행 error를 별도로 반환하며 저장하지 않는다.
  Create/Update의 기존 무결성 조건을 유지하고 묵시적인 사전 조회를 추가하지 않았다.
- 생성 입력의 default false·빈 문자열, patch의 SQL NULL·생략, 명시적으로 존재하는 0 PK와 PK 부재를 구분했다.
  생성 Article의 metadata snapshot·필드 선언 순서·PK projection/LIMIT 1·cursor close와 값 없는 `unique` 진단을 확인했다.
  FK는 relation 조회 없이 저장 column의 key로 검사하며 nullable FK의 NULL은 조회하지 않는다.
- Nil context/backend/input·취소·required/empty patch·위조 PK·외부 field reference·nullable alias 변조를 I/O 전에 거부했다.
  후반 AST가 잘못된 경우 앞선 정상 필드도 조회하지 않았다. 공유 manager의 동시 검증과 반복 호출은 metadata를 유지하고 결과 cache를 재사용하지 않는다.
- 두 번째 조회의 실패, 오류와 함께 반환한 rows, nil/typed-nil rows, iteration/close 오류와 query/Next 중 취소를 주입했다.
  Cursor를 한 번 닫고 원인을 보존하며 첫 번째 조회에서 찾은 중복을 부분 결과로 게시하지 않았다.
- 양 DB의 독립 **13 profile / 각 96개 입력 시도**에 먼저 사전 검증을 실행한 뒤 기존 실제 insert를 그대로 실행하여 결과를 대조했다.
  SQL NULL·case·UUID 별칭·Float/Decimal·시간 값·JSON의 저장 equality와 자기 행/다른 행 수정도 확인했다.
  SQLite NaN **2개**는 검증/쓰기 모두 기존 사전 오류이고 기본 Django JSON의 object 순서 **1개** 차이는 기존 canonical profile로 구분한다.
  새 기준 데이터를 제품 결과로 덮어쓰지 않았다.
- 양 DB의 두 backend/pool에서 사전 검사가 모두 통과한 뒤 경쟁 insert를 시작해 **1 성공 / 1 unique_constraint**를 확인했다.
  사전 검사 결과를 동시성 보장으로 사용하지 않는다. 기존 Atomic/AtomicRelation·rollback·재접속·migration drift 회귀도 아래 unique scope에 포함한다.
- ORM 쓰기 준비를 공유한 영향으로 Article API CRUD·인증/CSRF/권한 거부, Helpdesk의 실제 양 DB CRUD/Admin·권한 유지,
  generated relation product/fixture와 생성물 일치 검사를 함께 실행했다. 이는 기존 소비자 회귀이며 새 고유성 UI 연결의 완료가 아니다.

| 실행 | 결과 |
|---|---|
| `go test -json -count=1 -timeout=15m ./orm ./validation` | 2 package, root 192 / 전체 **531 run=PASS** |
| `go test -json -count=1 -timeout=15m -run 'Unique' ./db/sqlite ./db/postgres` | 2 package, root 28 / 전체 **303 run=PASS** |
| `go test -json -count=1 -timeout=15m ./conformance/relationproduct ./conformance/relationfixture ./examples/article/apiapp ./examples/helpdesk` | 4 package, root 21 / 전체 **38 run=PASS** |
| `go test -race -json -count=1 -timeout=15m -run 'ValidateUnique\|UniqueReferenceWrites\|UniqueConcurrent' ./orm ./db/sqlite ./db/postgres` | 3 package, root 11 / 전체 **250 run=PASS** |
| `CGO_ENABLED=0 go test -json -count=1 -timeout=15m -run 'ValidateUnique\|UniqueReferenceWrites' ./orm ./db/sqlite ./db/postgres` | 3 package, root 9 / 전체 **244 run=PASS** |

Normal 합계는 **8 package / root 241 / 872 run=PASS**다. 모든 최종 실행의 skip/fail·stderr는 **0**이다.
공통 ORM 검증 roster **24개**, 양 DB reference roster **220개**와 backend별 insert subtest **96개**를 normal/race/CGO0 사이에서 대조했다.
모든 package의 terminal·run/pass 목록과 source hash가 일치했다. 초기 성공 묶음 뒤 두 pool의 사전 검사 통과를 경쟁 테스트에 추가하고
위 다섯 실행을 최종 source에서 다시 수행했다.

Affected `go vet ./orm ./validation ./internal/uniquetest ./db/sqlite ./db/postgres`, `make format-check docs-check`, `git diff --check`도 PASS다.
Markdown 137개 링크를 확인했다. Generator/ABI 변경은 없으며 기존 relation generated drift는 위 소비자 검사에서 실행했다.
전용 PostgreSQL DB는 잔여 연결·사용자 table·test schema **각 0** 확인 후 삭제했다. 기존 PostgreSQL service는 유지했다.

공통 API와 실제 양 DB 조회의 로컬 checkpoint다. Form/Admin의 오류 재표시·API 응답·Helpdesk 외부 참조의 unique migration과
generated client 소비자 연결, 그 연결에 필요한 통합 milestone은 남아 있다. 이 실행은 전체 DB/process/platform 또는 Hosted 검증이 아니다.

## GDJ-0095 — SQLite 고유성·정확한 index 검증·실패 복구

2026-09-21, darwin/arm64 Go **1.26.5**, `modernc.org/sqlite v1.56.0`의 실제 SQLite runtime **3.53.3**,
`TZ=Pacific/Chatham`. Normal/race/SQL consumer는 **CGO_ENABLED=1**, 별도 CGO-disabled 검사는 **0**이다.
기준 `a11f07f0fb4673e5660a9f3108c5b5f794905b3b` 위 제품·검증 **13경로** manifest SHA256은
`0088d21093a5fa5a5dbd82431bf4355dc12db6c70b45332eea2801143aa8b32a`다.
검사 전후 source hash와 최종 run/pass 목록을 대조했다. 설계는 [ADR-0072](../adr/0072-column-uniqueness-and-constraint-ownership.md)가 소유한다.

- `UniqueConstraints`를 제공하고 Create/Add의 table/column DDL 뒤 선언된 unique index를 같은 operation에서 생성한다.
  Unique Alter는 CREATE/DROP INDEX이며 scalar reverse는 index와 column을 순서대로 제거한다.
  순수 renderer/root의 실제 다중 SQL 순서·Choices의 빈 group·unique 제거 body를 확인했다.
  검증 없는 legacy direct editor의 네 unique mutation은 명시적으로 거부하고 schema/recorder를 쓰지 않는다.
- 독립 Django SQLite raw **13 profile / 96개 입력 시도**를 GoDj typed write에 대조했다.
  **NaN 2개는 기존 GoDj 정책으로 I/O 전에 거부**한다. Django 기본 JSON의 object key 순서에 따른 **1개 결과 차이**는
  기존 canonical 저장 profile에 따라 구분한다. 96개가 모두 실제 DB insert이거나 Django 기본 결과와 일치했다고 합치지 않는다.
  SQL NULL·빈 문자열·case·UUID 별칭·numeric 동등성·JSON null, 자기 행/중복 update·취소·재접속 후 행/NULL/catalog를 확인했다.
- File DB의 별도 두 backend/pool에서 `busy_timeout(5000)` readback 뒤 경쟁 insert는 **1 성공 / 1 unique_constraint / 저장 행 1**이다.
  Atomic과 AtomicRelation의 insert/update 충돌은 앞선 변경까지 rollback하고 후속 쓰기가 정상 동작한다.
  구조화된 extended code 2067·native cause·값을 숨긴 진단·context 취소와 기존 PK 1555를 구분하며 문구-only 분류를 거부한다.
- 기존 중복으로 unique 추가/역방향 적용이 실패하면 정확한 catalog·revision/recorder·행·inbound FK가 유지된다.
  실패 전 revision으로 재진입하고 명시적 데이터 수정 후 재시도·reopen을 확인했다.
  Nullable UUID/FK unique 추가·reverse, unique FK 변경, 모델 그래프 전체 reverse/재생성도 실행했다.
- FK Remove remake에서 scalar와 FK의 유지되는 unique index를 재생성한다. Row/NULL·FK 제약과 삭제된 행의 sequence high-water를
  보존해 다음 ID가 **91/71**이었다. 같은 step에서 uniqueness 변경 뒤 remake가 실행되는 두 방향도 operation의 After 상태를 따른다.
  Index 재생성 실패를 table 교체·sequence 복원 뒤에 주입해 원래 schema/index/행/sequence/FK 설정/이력으로 정확히 rollback됨을 확인했다.
- Missing/nonunique/wrong column/DESC/NOCASE/compound/expression/partial/extra index/이름 spelling/wrong owner의 **11개 native drift**는
  revision claim 전에 거부됐다. Future index 이름의 main/TEMP 충돌과 transitive target의 빠진 unique index도 변경 없이 거부한다.
  같은 이름의 무관한 trigger는 별도 namespace로 보존했다. Untouched target의 미선언 nonunique index는 명시적으로 거부한다.
- Table 생성 뒤 index 생성 실패, unique scalar 제거 중 DROP COLUMN의 busy, 제거 완료 뒤 catalog의 busy/physical drift를 주입했다.
  실제 중간 DDL까지 관찰하고 오류의 operation 소유권·cause를 확인했다. Recorder/commit은 실패 상태를 유지하고 전체 rollback과 재시도가 성공했다.
  여러 SQL 중 뒤 body의 busy를 revision claim 실패로 재분류하지 않는 검사도 포함했다.

| 실행 | 결과 |
|---|---|
| `go test -json -count=1 -timeout=15m ./db/sqlite ./migrations ./migrations/backend` | 3 package, root 469 / 전체 **3,035 run=PASS** |
| `go test -race -json -count=1 -timeout=15m -run 'Unique\|SQLiteRevisionFenceTwoProcessSingleWinnerAndReopen\|SQLite(RelationRemake\|LoadedRelationRemake)' ./db/sqlite` | root 25 / 전체 **179 run=PASS** |
| `CGO_ENABLED=0 go test -json -count=1 -timeout=15m -run 'Unique\|SQLite(RelationRemake\|LoadedRelationRemake)' ./db/sqlite` | root 24 / 전체 **178 run=PASS** |
| `go test -json -count=1 -timeout=15m -run 'ChoicesSQL\|MigrationSQLRendering\|MigrationRelation' ./conformance/choicesproduct ./conformance/runners/godj` | 2 package, root 10 / 전체 **17 run=PASS** |

모든 최종 실행은 skip/fail·stderr **0**이고 package terminal과 run/pass roster가 일치한다. Normal/race/CGO0의 공통 unique roster
**148개**와 독립 입력 subtest **96개**를 각각 대조했다. 실제 two-process revision fence 부모도 race에서 PASS다.
새 unique 쓰기 경쟁은 두 pool 검사이며 별도 process 경쟁으로 표현하지 않는다.

초기 focused 실행은 quoted identifier의 하이픈을 잘못 invalid로 둔 테스트 1건이 실패했다. NUL 입력으로 고쳤으며 제품의 quoting은 제한하지 않았다.
첫 normal 실행은 untouched target의 미선언 index를 허용하던 기존 기대 1건이 실패했다. 정확한 선언 inventory에 따라 preclaim 거부와
snapshot 보존을 검사하고 명시적 DROP 뒤 성공·무관한 table/trigger 보존을 이어서 확인하도록 변경했다. 위험 검사를 삭제하지 않았다.
중단 전 소스 `237224989e1f1bc1fcdbe2c900f3985c06cafa70c2fefc99d980d559a4c7ac1a`의 terminal 성공과 현재 파일 일치를 복구 시 확인했다.
추가 검토에서 unique scalar 제거의 최종 검증 오류 분류를 보강하고 세 실패 회귀를 넣은 뒤 위 네 실행을 최종 source에서 다시 수행했다.

Affected `go vet ./db/sqlite ./migrations ./migrations/backend`, `make format-check docs-check`, `git diff --check`도 PASS다.
Markdown 137개 링크를 확인했다. Generator 출력 변경은 없어 generated drift 대상이 아니다.
이 checkpoint는 SQLite 구현과 현행 SQL 소비자의 로컬 증거다. 이전 PostgreSQL native 결과는 위 baseline의 별도 증거이며 다시 실행하지 않았다.
공통 입력 검증·Form/Admin/API/Helpdesk/client의 고유성 소비자, Hosted/full-platform과 GDJ-0095 전체 완료는 남아 있다.

## GDJ-0095 — Operation별 SQL 묶음과 실제 sqlmigrate 출력

2026-09-21, darwin/arm64 Go **1.26.5**, `TZ=Pacific/Chatham`. Normal은 **CGO_ENABLED=0**, race는 CGO=1이다.
기준 `d71aa81994092f866286c66a01c508f2219d5be6` 위 최종 제품·검증 **21경로** manifest SHA256은
`d043dba6e228a76bc0a526e437bd429bd8345784a803026995e25f81bb863f39`다.
설계는 [ADR-0055](../adr/0055-project-linked-deterministic-migration-sql-projection.md)가 소유한다.

- Backend renderer는 operation별 `[][]string`을 반환하고 root는 필수 물리 SQL과 metadata-only의 빈 group을 구분한다.
  Operation 순서와 group 내부 순서대로 복사해 public root·private protocol·CLI에는 기존 flat SQL을 전달한다.
  양 DB renderer·Article 설정·프로젝트 runner·repository-external runner를 같은 반환형으로 연결했다.
- Group 최대 2,048개와 전체 statement 최대 2,048개를 별도로 검사하고 body 총 16 MiB를 유지한다.
  한 group에 statement 한도를 모두 담는 경우, 여러 group의 합산 초과, SQL 없는 group 자체의 수 초과,
  group 내부/전체 byte 초과가 의미 오류보다 먼저 거부된다. 빈 문자열은 no-op으로 허용하지 않는다.
- Required Add/unique의 빈 group·metadata 위치 이동·불필요한 SQL·후반 malformed body를 거부한다.
  Resource scan과 첫 body 복사 뒤 취소도 부분 결과를 반환하지 않는다. 반환한 중첩 slice 변경은 게시한 결과를 바꾸지 않는다.
- 실제 repository-external project의 custom renderer가 한 operation에 table DDL과 index DDL 두 개를 반환할 때
  global CLI의 SQL 순서와 `;\n` 출력이 정확했다. 두 번째 body의 semicolon 오류에서는 앞의 정상 SQL도 stdout에 노출하지 않았다.
  Poison opener·DB 연결 0·init/render marker의 write-only 경계·application hash·workspace cleanup 검사를 유지했다.
- 기존 MIG-129..138의 독립 기대값·반복 결정성·secret redaction과 실제 child 중단/reap 경로도 검사했다.
  Statement count 초과 probe는 operation group 수를 그대로 유지하고 마지막 group에 body를 추가하여 전체 합산 검사를 검증한다.
  관측 count는 실제 반환 후보의 중첩 body 수에서 계산한다.

| 실행 | 결과 |
|---|---|
| `go test -json -count=1 -timeout=15m ./migrations ./migrations/backend ./project ./internal/projectcheck/linked ./examples/article/databaseconfig` | 5 package, root 270 / 전체 **587 run=PASS** |
| `go test -json -count=1 -timeout=15m -run 'MigrationSQLRenderer\|DecimalPrecisionSQL\|^TestPostgresUniqueSQLProjectionRequiresPhysicalChanges$' ./db/sqlite ./db/postgres` | 2 package, root 20 / 전체 **34 run=PASS** |
| `go test -json -count=1 -timeout=15m -run '^TestChoicesSQLRendersZeroStatementsAndMixedStepOnlyPhysicalSQL$' ./conformance/choicesproduct` | root 1 / 전체 **3 run=PASS** |
| `go test -json -count=1 -timeout=15m -run '^TestMigrationSQLRendering' ./conformance/runners/godj` | root 5 / 전체 **7 run=PASS** |
| `go test -json -count=1 -timeout=15m ./conformance/projectsqlmigrateproduct` | root 5 / 전체 **47 run=PASS** |
| `go test -race -json -count=1 -timeout=15m -run 'RenderMigrationSQL\|RenderedMigrationSQL\|SQLProjection\|MigrationSQLRenderer\|DecimalPrecisionSQL\|PostgresUniqueSQLProjection\|RunSQLMigrate\|MigrationSQLSelection' ./migrations ./migrations/backend ./db/sqlite ./db/postgres ./internal/projectcheck/linked ./examples/article/databaseconfig` | 6 package, root 36 / 전체 **67 run=PASS** |

Normal 합계는 **10 package / root 301 / 678 run=PASS**, race는 위 선택 범위의 **67 PASS**다. 최종 실행의 skip/fail·stderr는 모두 0이다.
JSON event의 run/pass 목록과 package terminal을 대조했다. 처음 추가한 외부 CLI 테스트는 두 case가 같은 marker 경로를 재사용해
기록 수 검사에 실패했다. Case별 경로를 분리한 뒤 위 normal과 race를 실행했다.

공통 제품·검증 **19경로**의 manifest는 `0c29341511e4f873d9807f20faf6944d10a6954494e4548231bac20fb1c7a9c5`로 검사 전후 동일하다.
마지막 conformance 관측 count·진단 문구 정리 후 해당 runner의 7개 검사를 최종 21경로 source에서 다시 실행했다.
나머지 체크는 두 conformance 파일을 dependency로 사용하지 않음을 `go list -deps -test`와 파일 hash로 확인했다.
Affected `go vet`, `make format-check docs-check`, `git diff --check`도 PASS다. Markdown 137개 링크를 확인했다.

이번 checkpoint는 SQL projection 계약과 소비자 실행의 로컬 검증이다. Native PostgreSQL DDL을 다시 실행하거나 SQLite의 실제 unique
제약·catalog·복구를 구현한 증거가 아니다. Hosted/full-platform·공통 입력 검증·Form/Admin/API/Helpdesk/client의 고유성 연결도 남아 있다.
Generator 출력 변경은 없어 generated drift를 재실행하지 않았다. SQLite `UniqueConstraints`는 여전히 false이며 GDJ-0095는 계속 진행한다.

## GDJ-0095 — PostgreSQL 고유성 제약·catalog·실패 복구

2026-09-21, darwin/arm64 Go **1.26.5**, native PostgreSQL **17.5 Homebrew**, `TZ=Pacific/Chatham`,
`GODJ_REQUIRE_POSTGRES=1`. 기준 `2674ad0c836b08d16f42d135d4189bab3a1fd53c` 위 제품·검증 **13경로**
manifest SHA256은 `666b3dd2e6d2e3acb9179149a256169c3e8564f7ef66aee3ab341f1b708e49a6`다.
검사 전후 해당 파일 hash가 같음을 확인했다. 설계는 [ADR-0072](../adr/0072-column-uniqueness-and-constraint-ownership.md)가 소유한다.

- Named native UNIQUE와 단일 B-tree의 Create/Add/Alter·reverse, 순수 SQL projection을 연결했다.
  제약·소유 index의 정확한 집합/이름/OID/column·즉시 검사·NULL 정책·collation/operator class를 검사한다.
  기존 PK index 검사도 같은 경계를 사용하고 알 수 없는 index를 허용하지 않는다.
- 독립 Django PostgreSQL raw의 **13 profile / 96 insert** 전부를 실제 GoDj typed write에 실행했다.
  두 SQL NULL, 빈 문자열, case, UUID 별칭의 같은 값, Float signed zero/NaN/infinity, Decimal 숫자 동등성,
  JSONB의 numeric/object 동등성과 JSON null을 포함해 저장 성공/23505 충돌이 전부 일치했다.
  각 profile에서 자기 행 update·다른 행의 충돌·취소·재접속 후 행 수/원래 NULL과 물리 catalog를 확인했다.
  UUID 별칭은 기존 form/API 입력 parser를 통해 typed UUID로 바꾼다. Strict `uuid.Parse`의 수용 범위를 바꾸지 않았다.
- 두 별도 backend/pool의 동시 insert는 **1 성공 / 1 unique_constraint / 저장 행 1**이다.
  Atomic callback에서 정상 insert 뒤 중복 insert가 실패하면 앞선 쓰기도 rollback되고 후속 쓰기가 성공한다.
  오류는 `integrity_error/unique_constraint`, 기존 PK 오류와 구분하며 native 원인과 context 취소를 보존한다.
- 기존 중복에서 unique 추가와 unique 제거의 reverse가 실패할 때 실제 catalog·revision/recorder·행/inbound FK가 유지됐다.
  실패 전 session token으로 다시 begin/rollback해 revision 보존을 확인했다. 명시적 값 수정 후 같은 변경의 재시도가 성공했다.
  재접속 후 이력·no-op, nullable unique/unique FK AddField·중복 update·실제 FK 제약·reverse·sequence high-water도 확인했다.
  Unique FK를 CreateModel에 바로 선언하는 경로와 모델 그래프 전체 reverse·재생성도 실행해 제약/sequence 잔여물이 없음을 확인했다.
- Native schema를 직접 바꾼 NULLS NOT DISTINCT/deferrable/standalone unique index/INCLUDE/extra index/index options
  **6개 drift**가 모두 revision claim 전에 거부됐다. 원래 행과 변경 전후 catalog·recorder가 유지됐다.
  그 외 column/부분 index/expression/operator class/collation·중복 inventory 등 catalog 부정 검사를 함께 실행했다.

| 실행 | 결과 |
|---|---|
| `go test -json -count=1 -timeout=15m ./query ./db/postgres` | 2 package PASS, root 220 run / 219 PASS, 전체 2,610 run / **2,609 PASS** / helper guard skip 1 |
| `go test -race -json -count=1 -timeout=15m -run 'Unique\|PostgresRevisionFencedMigrationIntegration\|PostgresDecimalPrecisionKeepsCachedReadersUsable' ./db/postgres` | root 11 / 전체 **157 run=PASS**, skip 0 |
| `go vet ./db/postgres ./query ./internal/uniquetest` | PASS, 출력·stderr 0 |
| `make format-check docs-check`, `git diff --check` | PASS, Markdown 137개 링크 검사 |

두 실행의 공통 unique roster **152개**가 같고 독립 insert **96개**의 run/pass를 각각 필수 대조했다.
Normal의 유일한 skip은 단독 `TestPostgresRevisionFenceHelperProcess` guard다. 실제
`TestPostgresRevisionFenceCrossProcessIntegration` 부모는 PASS다. 새 unique 경쟁은 두 pool 검사이며 별도 process 검증으로 합치지 않는다.
두 실행 모두 fail 0, stderr 0이다. 최초 실행은 UUID braces 원문을 strict model parser에 넘긴 테스트 adapter 1건이 실패했다.
기존 입력 parser로 고친 후 전체 normal과 관련 race를 실행했다. 제품 UUID 정책을 완화하지 않았다.

전용 DB는 잔여 연결·사용자 table·test schema **각 0** 확인 후 삭제했고 기존 PostgreSQL service는 유지했다.
이 checkpoint는 PostgreSQL 구현의 로컬 증거다. SQLite의 실제 고유성, 공통 입력 검증과 Form/Admin/API/Helpdesk/client,
CGO-disabled·Hosted와 전체 platform은 이 실행에 포함하지 않는다. GDJ-0095 전체 완료도 아니다.

## GDJ-0095 — 고유성 선언과 미지원 실행 경계

2026-09-21, darwin/arm64 Go **1.26.5**. 기준 `2050464148d472d4e43dd15b8802d895fb42e743` 위
제품·테스트 **35경로** manifest SHA256은 `2878a45b292da36570744ad8d94cca590fa922531d4c08735a4b3f860f3a4fbe`다.
검사 전후 해당 파일 hash가 같음을 확인했다. 아래는 선언·이력 기반의 로컬 checkpoint이며 실제 UNIQUE 제약 지원의 완료가 아니다.

- `schema.Unique()`가 12종 scalar와 현재 FK의 IR·snapshot/equality/hash·생성 descriptor/schema에 전달된다.
  PK의 고유성은 PK가 소유하고 독립 Unique flag는 정규화한다. Unique FK의 many-to-one/reverse collection은 유지한다.
- Strict project wire의 정확한 byte 한도와 bool shape, historical Create/Add/Alter의 왕복·소유권·digest를 검사했다.
  잘못된 타입·중복 key·PK의 비정규 Unique wire를 거부한다. 생략과 명시적 false는 같은 의미·canonical digest를 갖는다.
  고유성 추가/제거의 자동 계획은 정확한 before/after와 재실행 no-op을 유지하고 다른 facet과 섞인 변경을 거부한다.
- Loaded lifecycle의 Create/Add/Alter 추가·제거/retained/target/transitive **7경로 × 양 방향**이
  `UniqueConstraints` 미지원 시 transaction 전에 중단됨을 검증했다. SQLite 실제 memory DB에서도 schema/recorder
  객체가 0으로 유지됐다. 양 DB의 직접 compiler·순수 SQL renderer가 고유성 선언을 버리고 성공하지 않으며,
  내부 seal이 Unique 변조를 탐지한다. Core SQL projection은 unique 변경의 빈 SQL을 거부한다.

| 실행 | 결과 |
|---|---|
| `go test -json -count=1 -timeout=12m ./schema/... ./internal/projectwire ./internal/migrationautodetect ./migrations/... ./codegen` | 테스트 소유 8 package, root 400 / 전체 run=PASS **1,065**, test skip 0 |
| `go test -json -count=1 -timeout=8m -run 'Unique\|IntentSeal\|MigrationSQLRenderer\|MigrationCapabilities' ./db/sqlite ./db/postgres` | 2 package, root 27 / 전체 run=PASS **43**, skip 0 |
| `go test -json -count=1 -timeout=12m -run '^TestGeneratedUniqueMetadataConsumer$' ./codegen/consumertest` | host 1 PASS, 실제 별도 module의 생성 코드 compile·consumer 1 PASS |
| 해당 schema/wire/autodetect/migration/codegen/backend의 `go vet` | PASS, 출력·stderr 0 |
| `make generate-check` | Helpdesk/Article/relationfixture 각각 12/12/16개 clean, checked-in relation consumer PASS |
| `make format-check docs-check`, `git diff --check` | PASS, Markdown 136개 링크 검사 |

JSON event의 run/pass 전체 목록과 package terminal 상태를 대조했다. 세 실행의 테스트 **1,109 PASS**, test skip 0, stderr 0이다.
테스트가 없는 `migrations/internal/loadeddefinition`의 package-only `[no test files]`는 실제 테스트 PASS 수에 포함하지 않는다.
최초 core 실행은 새 autodetect 테스트의 기대값이 정규화 전 빈 column을 사용해 1건 실패했다.
정규화된 ProjectState를 기대값으로 수정한 후 전체 위 범위를 다시 실행했으며 before/after·재구성·no-op 검사는 유지했다.

위 선언 기반 source의 양 backend `UniqueConstraints`는 false였다. 실제 UNIQUE DDL/catalog, native PostgreSQL의 고유성 실행,
저장 충돌/경쟁·rollback/retry·Form/Admin/API/Helpdesk 연결, 관련 race/process/Hosted 통합은 후속 구현 checkpoint가 소유한다.
이 결과를 기존 JSON의 Hosted web/full source나 전체 프레임워크 완료와 합치지 않는다.

## GDJ-0095 — 모델 고유성의 독립 기준

- 제품 기준 source `ef9b05c0ec829d5cb38c0944507a70f0a5e1f483` 위 독립 runner·test·양 DB raw와 Python lock
  **6경로** manifest SHA256은 `b57fe03aa1ffcb28711c123732501b4759b224885732fdc0662274ab2b054ac1`다.
  이 단계에는 GoDj 제품 코드 변경이 없다. 아래 관찰은 unique 선언·migration·검증 기능이 구현됐다는 증거가 아니다.
- [Runner](../../conformance/runners/django/unique_reference.py)는 Django **6.1**·Python **3.14.3**, SQLite **3.50.4**와
  native PostgreSQL **17.5 Homebrew**/psycopg **3.3.6**의 public ORM·ModelForm·migration state/autodetector/schema editor를 사용했다.
  각 backend **13 profile / 96 insert 시도**, ModelForm **6개**, unique 변경 **3개 흐름 / 8회 forward·reverse·재시도**,
  unique AddField **2개**, unique FK **5회 insert**, savepoint/outer rollback과 별도 connection **2개 경쟁 쓰기**를 관찰했다.
  13 profile은 기존 12종 scalar와 명시적인 JSON canonical 저장 profile이다. 기본 JSON 관찰을 canonical로 덮어쓰지 않는다. Bool/int/float 관찰값에 별도 type tag를 사용해
  Python의 True=1=1.0 비교나 signed zero가 reference 차이를 숨기지 않게 했다.
- 양 DB에서 nullable unique의 SQL NULL 두 개는 저장되고 같은 UUID의 문자열 별칭·동일 숫자값·빈 문자열 중복은 충돌했다.
  기본 Django SQLite JSON은 1/1.0·다른 object key 순서를 다른 TEXT로 저장하고, PostgreSQL JSONB는 같은 값으로 충돌했다.
  SQLite JSON canonical profile은 기존 GoDj key 정규화 정책에 따라 reordered object가 충돌했다. SQLite의 NaN→SQL NULL과
  PostgreSQL NaN의 중복 충돌도 보존한다. GoDj의 기존 NaN/정확한 JSON token 정책은 이 관찰로 완화하지 않는다.
- 기존 중복 데이터에서 unique 추가는 IntegrityError로 실패하고 column·constraint·원래 행과 inbound FK가 유지됐다.
  한 중복 값을 명시적으로 수정한 후 재시도는 성공했다. Unique 제거 후 새 중복을 만들면 reverse가 실패하고,
  이를 명시적으로 고친 뒤 reverse가 성공했다. 각 시도 후 connection을 닫고 재접속해 snapshot을 다시 대조했다.
  기존 두 행에 nullable unique column을 추가하면 둘 다 NULL이며 reverse가 원래 shape를 복원한다.
  동일한 literal default를 채우는 unique AddField는 실패하며 column·데이터·constraint가 추가 전과 같았다.
- Unique FK는 중복 target을 거부하고 null은 두 번 저장했다. Field는 many-to-one, reverse는 one-to-many manager이며
  one-to-one 객체 API로 변하지 않았다. 별도 경쟁 insert 두 개는 정확히 **1 성공 / 1 IntegrityError / 저장 행 1**이다.
  Native 오류는 PostgreSQL **23505**, SQLite **SQLITE_CONSTRAINT_UNIQUE**다. Duplicate savepoint는 outer의 정상 쓰기를
  손상시키지 않았고 outer rollback 뒤에는 처음 행만 남았다. 이 결과는 GoDj nested transaction 지원의 증거가 아니다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 fresh SQLite 전체 관찰 **1 PASS**, skip/warning 0이다.
  원본 runtime/version은 확인 후 그 두 fingerprint만 제외하고 모든 행·DDL·형태·오류·재접속·경쟁 결과를 비교했다.
  Raw SHA256: SQLite `e5ace05ba1b6370159f600504cdc320351359dcbfba89e094505a90aefa943b7`,
  PostgreSQL `0b240dea9b81f35e1876998c5b88bc7c46e5c95ec6d4581ca89520d413d65733`.
  전용 DB는 잔여 연결·사용자 table 0 뒤 삭제했다. 기존 PostgreSQL service는 유지했다.
- 이 독립 기준 source에는 GoDj의 IR·definition·generator에 Unique 속성이 없었고 migration catalog는 추가 unique index/constraint를 거부했다.
  이 거부를 제거하는 것으로 구현 완료 처리하지 않는다. [GDJ-0095](../../work/0095-model-uniqueness.md)의
  정확한 선언/물리 소유권·무결성 오류·소비자 연결과 실제 제품 검증을 이어간다.

## GDJ-0094 — JSON 모델 독립 기준과 저장 기반

- UUID source `7da91ad5fbd6284622fb372e7d8051120584424e` 위에서 다음 모델 연결의 독립 기준만 준비했다.
  다음 독립 관찰은 JSONField Go 제품 구현·지원 또는 UUID Hosted 완료의 증거가 아니다. 제품 checkpoint는 뒤 소절에서 구분한다.
- [Runner](../../conformance/runners/django/json_field_reference.py)는 고정 Django **6.1**·DRF **3.18.0**의 public model **54**,
  Form **66**, serializer **168**, 실제 JSON parser **32**개 입력과 SQLite schema editor·query·rollback·reopen·reverse를 관찰한다.
  GoDj 코드·expected raw를 import하지 않는다. Python int/float/bool·문자열·object/array·SQL NULL/JSON null과 validation/render 실패를 구분한다.
  Required/non-null 요청은 null을 거부하지만 완전한 instance의 null 응답은 출력한다. Instance 없는 required partial omission은 검증에 성공해도
  `.data`에서 KeyError인 관찰을 보존했다. 입력 검증 결과를 완전한 모델 응답으로 취급하지 않는다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**의 fresh 비교 각각 **1 PASS**, skip 0, 실행 stderr warning 0이다.
  고정된 `exact None`의 `RemovedInDjango70Warning` 한 개만 별도 capture하여 raw와 비교한다. 그 밖의 실행은 `-W error`로 확인했다.
  Raw SHA256은 `16753a3626f5a07bdbe24a8fa5e6841eb5426d65e28f88312671198eb31aab3a`, runner·test·raw 세 파일 manifest는
  `10c875513c0c9a2704f1aa32502a505d9b8bd6c0ed203a5697884a099077ce85`다.
- SQLite TEXT + JSON_VALID check, 기존 행의 nullable add, NULL/JSON null query와 physical storage, integer/float·객체 순서별 whole equality를 확인했다.
  외부 duplicate key는 Python whole-document decode에서 마지막 값, SQLite key lookup에서 첫 값을 반환했다.
  NaN/Infinity write·malformed 외부 text는 거부됐으며 surrogate/NUL의 SQLite 수용과 DRF render 실패도 별도 관찰이다.
  실패/rollback 뒤 행 수·기존 값, 새 connection과 nullable field reverse를 확인했다.
- 별도의 native PostgreSQL **17.5 Homebrew UTF8** 전용 DB probe에서 jsonb의 1/1.0/1e0 equality, 객체 순서 독립 equality,
  bool/number 구분·JSON null의 non-SQL-NULL·duplicate 마지막 값·숫자 표기 정규화를 확인했다.
  1e400은 native numeric으로 보존되지만 1e1000000은 SQLSTATE **22003**, NUL은 **22P05**, lone surrogate/NaN/Infinity/malformed는 **22P02**다.
  Native MIN(jsonb)는 **42883**이다. Native raw SHA256은 `fb86d5e58d8d36995026ec093f22879ec549dd5d3fe27484838660b7960ee998`다.
  이는 Django PostgreSQL 또는 GoDj 제품 PASS가 아니다. 전용 probe DB는 잔여 연결 0 뒤 삭제했고 기존 service는 유지했다.

- 별도 OpenAPI/tooling 사전 실험은 non-null 입력의 `not: {type: null}`과 임의 JSON/null 응답을 구분했다.
  pinned ogen **v1.24.0**은 양쪽을 `jx.Raw`로 생성했다. 독립 모듈의 mock HTTP wire **2 root / 15 test·subtest PASS**, 실제 test skip 0·stderr 0 bytes다.
  2^128-1·1e400·긴 소수·중첩 object/array·JSON string/null을 float 경유 없이 유지하고 누락/잘못된 응답 envelope를 거부했다.
  생성된 library package에는 별도 test가 없으며 이를 실행한 테스트 수에 넣지 않았다. 첫 임시 audit의 package-level no-test 분류를
  실제 test skip과 구분한 후 전체 terminal inventory를 확인했다. 재실행이나 assertion 삭제로 결과를 바꾸지 않았다.
  Generator는 `not`의 입력 validator를 만들지 않아 raw null 요청을 전송할 수 있다. 표준 schema validator는 같은 요청을 거부하므로 runtime 검증이 필요하다.
  OpenAPI 3.1.1 표준 검증과 요청/응답 nullability도 PASS다. Tool/input/generated/probe source **12파일** manifest는
  `99cbace2049e405862918b1376bcc0b3728852a2dd2815f76ccc43583180783c`이며 module/config lock은 기존 consumer와 동일하다.
  이 mock/tool 관찰은 실제 GoDj JSON API/client 구현 또는 DB 통합 PASS가 아니다.

### JSON 값 codec의 로컬 checkpoint

- UUID 완료 기록 source `97688f58d2370809aa3ec457ae9342b51d5def81` 위 `jsonvalue/value.go`·`jsonvalue/value_test.go`·
  `internal/wirejson/decode.go` 세 파일의 manifest SHA256은 `730d5f49f25a93c08e28582528d303cccde24bfdf4f08c4041bd077ffb04c817`이다.
  `jsonvalue.Value`는 문자열만 소유하는 값이며 명시적 JSON null과 invalid zero를 구분한다. Decode의 object/array는 매번 caller가 소유한다.
  Canonical object key/공백·escape 정리와 정확한 numeric token을 유지하고 중복 key·잘못된 UTF-8/surrogate·크기/깊이 초과를 거부한다.
  JSON 자체에서 유효한 escaped NUL은 codec이 보존하며 backend와 Form/API의 별도 수용 정책은 아직 연결 전이다.
- Go **1.26.5 darwin/arm64**에서 `./jsonvalue ./internal/wirejson`을 fresh 일반·race로 실행했다.
  각각 **2 package / 45 test·subtest PASS**, run/terminal roster 동일·skip/fail 0·stderr 0 bytes다. 검증 전후 같은 세 파일을 확인했다.
  이 결과는 값 codec과 공유 decoder의 범위다. 뒤에 편집 중인 Schema IR·query·DB·생성기 또는 JSONField 전체의 runtime PASS가 아니다.


### JSON 모델·DB·생성 소비자 로컬 checkpoint

- 기준 source `97688f58d2370809aa3ec457ae9342b51d5def81` 위 제품·테스트·module lock·독립 raw **59개 non-Markdown 파일**의
  정렬 manifest SHA256은 `ee16383282a9f18817259fbe3a860fc8bc12376402d960dc6e32517646e6ea4f`다. Normal/race/CGO=0·vet 실행 전후 같은 바이트를 확인했다.
  Go **1.26.5 darwin/arm64**, native PostgreSQL **17.5 Homebrew**, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 검사했다.
- Normal은 `go test -count=1 -timeout=20m -json`, race는 같은 명령의 `-race`로 값/decoder·schema/IR/resource/wire·query·migration/definition/autodetect·ORM·
  공통 queryplan·SQLite/PostgreSQL·codegen/consumer·projectgenerate 각각 **17 package, 1170 root, 6785 test·subtest PASS**다.
  Normal/race의 전체 run/pass/skip roster가 동일하다.
  Stderr 0 bytes이며 두 skip은 `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`의 parent-mode guard다.
  실제 process를 실행하는 `TestPostgresRevisionFenceCrossProcessIntegration`과
  `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 PASS다. 필수 test 누락이나 기능 skip이 아니다.
- `CGO_ENABLED=0`의 새 JSON SQLite/PostgreSQL·외부 generated consumer와 독립 source namespace/recovery fixture 선택은
  **4 package, 8 root, 13 test·subtest PASS**, skip/fail 0·stderr 0 bytes다. Generated consumer의 SQLite와 native PostgreSQL 하위 실행을 필수로 확인했다.
- 외부 generated module은 SQL NULL/JSON null·literal default·required/nullable·큰 정수/decimal/exponent와 SQLite/native JSONB 저장,
  typed/dynamic exact/IN/F/isnull·projection·forward/reverse·eager snapshot/캐시 소유권을 실제 두 DB에서 확인한다.
  Existing table의 nullable 추가·재접속·실패한 Save·Save field mask·invalid/canceled write·transaction read/rollback·reverse/re-add도 포함한다.
  문자열·정수·text F를 JSON API에 전달하는 별도 compile 실패를 확인하며 user model/member의 JSON 이름도 import/default를 가리지 않는다.
- Native JSONB `1e400`, `1e4095`, `1e-4094`, `-0.0`, `1.2300e2`를 실제 다시 읽어 정밀도/scale·표기 변화를 검사했다.
  전개 후 numeric 4096 byte 초과·문서 전체 확장·U+0000은 native parameter 단계에서 거부한다. SQLite에는 native 한도를 전파하지 않는다.
  SQLite의 malformed 문서는 CHECK에서 거부되며 syntax-valid BLOB/duplicate/surrogate의 strict read는 이전 scanner 값을 남기지 않는다.
  두 backend의 JSON ordering은 일반 SELECT뿐 아니라 direct/distinct/sliced/empty COUNT의 정렬 생략 경로에서도 명시적으로 거부한다.
- 처음 normal 실행은 전용 DB URL에 host가 빠져 PostgreSQL 제품 URL 검증과 해당 consumer가 실패했다. 이를 PASS로 사용하지 않는다.
  Host를 명시한 같은 전용 DB에서 normal을 재실행해 통과했고, 이후 Count의 omitted-ordering 검사를 보강한 최종 source로 normal을 다시 확인했다.
  실패/중간/최종 로그는 별도로 보존했다. Native JSONB 수용 범위나 strict codec 규칙을 낮춰 실패를 없애지 않았다.
- Affected `go vet`, gofmt·diff/link 검사와 `make generate-check`를 확인했다. Helpdesk/Article/relationfixture의 checked-in generated 결과는 clean이다.
  Recovery fixture의 독립 runtime 복사에는 새 `jsonvalue` package도 포함한다.
  최종 검사 뒤 전용 PostgreSQL DB의 잔여 연결 0을 확인해 삭제했고 기존 service는 유지했다.
- 이 결과는 JSON 모델·저장 기반의 로컬 checkpoint다. 이 checkpoint 시점의 Form/Admin·serializer·OpenAPI·실제 Helpdesk JSON 소비자·독립 HTTP client와
  JSON key/path/contains 등 추가 연산은 후속이었다. 새 입력/소비자 checkpoint는 다음 소절에 기록한다. UUID source의 Hosted full을 이 새 JSON 코드의 platform PASS로 사용하지 않는다.

### JSON Form/Admin/API·Helpdesk와 독립 client 로컬 checkpoint

- 기준 source `878eecc9c6d2987df456bdddc326a931e11c75ab` 위 제품·테스트·생성물·reference·module/config lock
  **71 non-Markdown 파일**의 정렬 SHA256 manifest는 `87ca9e2de2578ccc8c3769a9039805755337a64f4bedd7f35018695baa35d0be`다.
  Normal/race/CGO=0 실행 전후 같은 바이트를 확인했다. Go **1.26.5 darwin/arm64**, PostgreSQL **17.5 Homebrew**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`의 별도 전용 DB를 사용했다.
- 독립 reference에 빈/nested 빈 key·protocol-like key·NUL key·숫자 표기/정밀도 입력을 추가했다. 새 raw는
  **model 62 / Form 88 / serializer 192 / 실제 parser 43**개이며 SHA256은
  `10973457faa61eb2f63dc3cdbfbaf6471c8d1cb27900f9846a019a52b2f1eaea`다. 기존 모든 행과 DB/기타 관찰이 이전 raw와 같은지 별도로 대조했다.
  Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** fresh 비교 각각 **1 PASS**, skip/warning 0이다.
  다른 Python을 exact frozen profile로 실행한 초기 시도는 `requires-python ==3.14.3`에서 거부됐다. 이를 성공으로 세지 않고
  CI와 같은 isolated compatibility 환경에서 Django 6.1·DRF 3.18.0·asgiref 3.12.1·sqlparse 0.5.5를 명시해 다시 검증했다.
- Normal `go test -count=1 -json -timeout=10m`과 동일 scope의 `-race`는 아래 **9 package, 174 root,
  3736 test·subtest PASS**, run/pass 전체 roster 동일, skip/fail 0·stderr 0 bytes다.

```text
./internal/jsoninput ./forms ./forms/model ./admin ./serializers ./api ./api/openapi
./examples/helpdesk ./api/openapi/consumertest
```

- `CGO_ENABLED=0`은 실제 SQLite/PostgreSQL Helpdesk 두 root와 `TestGeneratedOpenAPIClientContract`를 선택했다.
  **2 package, 3 root/test PASS**, skip/fail 0·stderr 0 bytes이며 필수 실행 목록을 따로 검사했다.
- Form 88개 입력과 5종 initial의 clean/error/has_changed를 독립 raw에 대조했다. Required empty, bool/number, integer/float,
  floating signed zero·큰 숫자 정밀도·object key 순서·NUL validation을 구분한다. Serializer 192개 direct/43개 parsed 입력의
  누락/default/full/PATCH·nullable/required·JSON-bearing string과 exact number를 검증한다. DEV-0017의 명시적 차이는 고정 selector로 확인한다.
  Opaque JSON null의 직접 Go 입력도 non-null 제약을 우회하지 않으며 JSON null omission default와 응답의 존재는 별도로 유지한다.
- Spec-aware JSON decoder는 JSONField 안의 임의 key만 허용한다. Root/다른 필드의 이름 규칙과 Unicode/NUL/중복 key 검증,
  여러 JSONField와 envelope의 공유 값 수·깊이/byte 한도, caller가 만든 깊은 Value tree의 fail-closed를 확인했다.
  Admin은 canonical snapshot/typed initial·raw 재표시·XSS escaping과 재검증을 유지한다.
- Helpdesk의 실제 `0015_ticket_external_payload` migration은 기존 행·nullable 추가·저장·reopen·reverse/reapply를 양 DB에서 검사한다.
  실제 Admin/API에서 create·PUT/PATCH·생략/null·빈 값·큰 정수·empty/protocol key·중첩과 JSON string을 왕복했다.
  Form의 동등한 1.0/1e0와 stored JSON null 재제출은 UPDATE 0이며, 다른 필드 수정에도 원래 JSON 표현/tag가 남는다.
  Invalid/oversized 입력·valid-CSRF 권한 거부·CSRF 거부·category isolation·mutation 뒤 주입 실패/rollback·새 connection을 확인했다.
  PostgreSQL `1e2000`의 API renderer 한도와 `1e5000`의 model preflight 실패는 create/update를 commit하지 않는다.
- 고정 ogen **v1.24.0**로 실제 세 API profile을 다시 생성했다. Article 두 profile은 변하지 않고 Helpdesk schema와 생성 파일 2개만 달라졌다.
  독립 client의 module/config lock은 불변이며 GoDj import/replace 없이 생성 drift·build·실제 HTTP와 최종 SQLite DB를 확인했다.
  Parent/child **34개 receipt**와 race mode 일치를 확인한다. Any-JSON `jx.Raw`는 2^128-1, 긴 소수, 1e400/1e-400,
  null/생략과 응답 required presence를 보존한다. 새 raw field 때문에 비교 불가능해진 generated Ticket은 모든 필드를 deep 비교한다.
- Python 3.14.3·`openapi-spec-validator 0.7.2`·`jsonschema 4.25.1`로 세 실제 OpenAPI **3.1.1** 문서와
  JSON field의 any-value 및 입력 optional/완전 응답 required 의미를 독립 검사했다. Vendor extension을 표준 validator의 실행 보장으로 해석하지 않는다.
- 첫 protocol checkpoint에서 발견한 Form top-level NUL 오류와 성공한 clean의 SQL/JSON null 변경 감지를 수정했다.
  Non-finite direct Value는 Object 생성 전에 이미 거부되므로 그 실제 경계에서 실패를 검사하도록 fixture를 고쳤다.
  첫 integration은 옛 field 개수·Textarea newline·모델 HTML escape expected에서 실패했다. 다음 실행의 permission fixture는
  Change가 거부되는 Admin GET 대신 safe API GET의 유효 CSRF를 받고 정확한 `permission_denied`를 확인하도록 고쳤다.
  위 최종 source로 전체 affected scope를 다시 실행했으며 초기 실패 로그를 별도로 보존했다.
- Affected vet·Helpdesk 12파일 generated drift·format/diff/docs 검사는 PASS다. 전용 DB는 잔여 연결 0 뒤 삭제하고 기존 service는 유지했다. JSON value→양 DB→Form/Admin/API→외부 client를
  연결한 이 milestone의 전체 platform/cold CLI 검증은 다음 Hosted full이 소유한다. 이전 UUID Hosted full을 현재 JSON의 결과로 사용하지 않는다.

### JSON 수직 연결 Hosted full

- [Run 35511311272](https://github.com/progresshans/godj/actions/runs/35511311272), attempt **1**, source
  `d1a0570b87791378bc24a2cd90ce4afa8326a653`: **62/62 job success**. 전체 job의 checkout SHA와 종료 상태를 완전한 로그/metadata로 대조했다.
  최종 gate는 `scope: full`, `full_platform_verified: true`, 선언된 owner **8개**다.
- Ubuntu amd64/arm64·macOS arm64/amd64와 normal/race/CGO=0의 선언된 matrix, 실제 Ubuntu cold external CLI milestone이 통과했다.
  Exact Darwin Python은 **298 PASS/skip 0**, 네 compatibility Python은 각각 **298 tests / 선언된 skip 4**다.
  PostgreSQL 17.10 core normal/race/CGO=0 각각 **13 package / 1880 PASS / skip 0**, operator-target 각각 **2 package / 12 PASS / skip 0**다.
- 실제 실행 목록 대조에서 `TestGeneratedJSONConsumer`가 PostgreSQL owner의 `-run` 선택 목록에 없는 것을 확인했다.
  이 run은 기존 Helpdesk parent 안의 새 JSON PostgreSQL 흐름과 SQLite/portable generated JSON은 실행했지만,
  독립 generated JSON consumer의 PostgreSQL 하위 실행 증거는 아니다. 로컬 17.5 결과로 이 누락을 대체하지 않는다.
  후속에서 PostgreSQL 선택/필수 목록과 relation 필수 목록에 해당 parent를 추가했다. PostgreSQL DSN이 있는 parent는 child의
  PostgreSQL 하위 실행도 필수로 검증한다. 아래 목록 예산 보강과 함께 `web` scope Hosted에서 확인했다.

### JSON 목록 응답 예산 후속 보강

- 수직 연결 source `d1a0570b87791378bc24a2cd90ce4afa8326a653`의 Hosted 실행 중, 실제 Helpdesk HTTP에서 각각 허용되는
  1024-item 배열 네 개를 저장한 뒤 목록을 읽으면 기본 4096-value 예산 때문에 500이 되는 회귀를 재현했다.
  `api.JSONWithLimits`로 response 전체 예산을 명시할 수 있게 하고 Helpdesk 최대 20행 목록의 value 한도를 65536으로 설정했다.
  요청과 다른 응답의 기본 한도·1 MiB·깊이 16·container hard cap은 유지한다. 큰 행의 누락이나 값 변경도 실패로 검사한다.
- 이 후속은 위 Hosted source에 포함되지 않는다. 기준 source 위 변경·module/config lock **12 non-Markdown 파일** manifest는
  `5205311de14009bcc68068bf448b7c88b168ba84654f5488cf0f6749de7f8448`이다. 변경 전후 같은 바이트를 확인했다.
  Go 1.26.5 darwin/arm64, SQLite와 PostgreSQL 17.5, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서
  `./api/... ./examples/article/apiapp ./examples/helpdesk` **7 package / 76 root / 257 test·subtest**를 normal/race 각각 실행했다.
  전체 run/pass roster 동일·skip/fail 0·stderr 0 bytes다. CGO=0의 새 response budget과 실제 양 DB Helpdesk·생성 client는
  **3 package / 4 root PASS**, skip/fail 0·stderr 0 bytes다.
- 기본 예산의 거부, 정확한 aggregate node 경계, byte/depth/container/hard-cap 초과 때 부분 응답이 나오지 않는 것을 확인했다.
  수정 후 첫 HTTP fixture는 정리 과정의 ORM Delete가 모델 ID를 비우는 의미를 놓쳐 비교 ID가 사라졌다.
  삭제할 복사본을 사용해 기대 ID를 보존한 뒤 위 전체 affected scope를 재실행했다. 최초 500 재현과 fixture 실패 로그도 보존했다.
- 세 OpenAPI profile을 같은 locked ogen으로 다시 생성했고 Helpdesk 목록 설명과 generated client 설명 한 파일만 달라졌다.
  Module/config lock은 불변이며 독립 client의 34개 receipt도 통과했다. Affected vet와 실제 문서의 표준 검증도 PASS다.
  새 전용 DB는 잔여 연결 0 뒤 삭제했다. CI 필수 실행 목록의 2파일 보강은 별도이며 기존 scope/gate 테스트 4개를 통과했다.
  실제 새 PostgreSQL 선택과 목록 보강의 Hosted 증거는 다음 소절이 소유한다. 수직 연결 source의 full 결과와 합치거나 전체 platform을 중복하지 않는다.

### 목록 예산·generated JSON 필수 실행 Hosted 검증

- [Run 35513511335](https://github.com/progresshans/godj/actions/runs/35513511335), attempt **1**, source
  `33b310f72036259f1fda0e2d0310d43c341187fb`: 실행 **32 job success**, 범위 외 owner **5개 skip**.
  실행한 32개 job 모두의 checkout SHA·종료 상태를 완전한 로그와 metadata로 대조했다.
  최종 gate는 `scope: web`, `full_platform_verified: false`, owner는 `command-product-matrix`, `portable-go-matrix`, `postgresql-product`다.
- PostgreSQL 17.10 core normal/race/CGO=0 각각 **13 package / 1884 PASS / skip 0**다.
  세 모드 모두 `codegen/consumertest|TestGeneratedJSONConsumer`의 선택·필수 PASS를 확인했다.
  `GODJ_REQUIRE_POSTGRES=1`의 parent가 generated child의 `TestJSONStorageQueryAndOwnership/postgres` 실행까지 요구하므로
  앞선 full에서 누락됐던 독립 generated JSON의 실제 PostgreSQL 흐름도 검증했다.
  Operator-target normal/race/CGO=0 각각 **2 package / 12 PASS / skip 0**다.
- 선택하지 않은 owner는 `conformance-validation`, `exact-darwin-validation`, `relation-product-matrix`,
  `product-project-check-matrix`, `python-compatibility-matrix`다. 이 결과는 후속 source의 관련 범위이며 새 full-platform 결과가 아니다.
  완료 기록은 Markdown만 변경하고 제품·생성물·workflow·lock은 위 source와 동일하게 유지한다.

### JSON key/index 경로의 로컬 checkpoint

- 기준 `a49f3dca6e70a2e7f6ca17481a3f0569e03204f6` 위 제품·테스트·독립 reference 및 Go/Python lock **24 non-Markdown 파일**의
  정렬 manifest SHA256은 `6a60d5d15f4a00ffc4a69c6aec1beb452e9f45174c656cd4017dcb8b3e079b91`이다. 실행 전후 같은 바이트를 확인했다.
  Go 1.26.5 darwin/arm64, repository-pinned modernc SQLite와 PostgreSQL **17.5 (Homebrew)**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 실행했다.
- `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest`와 같은 범위 `-race`:
  각각 **8 package / 703 root / 5549 test·subtest PASS**. 전체 run/pass 목록이 동일하며 fail 0·stderr 0 bytes다.
  단독 실행의 `TestPostgresRevisionFenceHelperProcess`만 명시적으로 skip됐고 실제 cross-process parent는 필수 PASS다.
  `db` package의 `[no test files]` event는 test skip이 아니다. 보조 감사의 이 event 분류를 고친 뒤 동일 raw를 재확인했다.
- `CGO_ENABLED=0`의 `./query ./codegen/consumertest`에서 새 path domain과 `TestGeneratedJSONConsumer`를 선택해
  **2 package / 2 root / 6 PASS**, skip/fail 0·stderr 0 bytes를 확인했다. Normal/race/CGO=0 모두 parent가 generated child의
  SQLite와 PostgreSQL `TestJSONStorageQueryAndOwnership/.../paths`를 필수로 요구한다. 경로 값의 compile-negative도 포함한다.
- 실제 generated model에서 큰 정수의 인접 값·overflow/underflow token·object/array·JSON 타입,
  key/index 구분·빈 key·점/따옴표/역슬래시/제어문자/Unicode/SQL처럼 보이는 key, nested path·missing/SQL NULL/JSON null,
  empty IN·NOT/double NOT/OR·Count·typed/dynamic 동일 AST·cache snapshot·policy/잘못된 입력을 확인했다.
  Forward optional 관계·eager materialization·부정 조건과 direct reverse exact도 실행했다.
  PostgreSQL NUL key는 empty IN의 I/O 생략 전에도 거부한다. SQLite 함수는 여러 physical connection과 reopen에서 확인했다.
- 첫 native SQLite `->` 후보는 여러 행의 빈 key 조회가 NUL key 행까지 선택해 실패했다.
  Parent의 redacted 실패 요약만으로 원인을 단정하지 않고 같은 generated module의 직접 실행에서 오조회 결과를 확인했다.
  Shared bounded codec을 호출하는 deterministic SQL 함수로 바꿔 NUL/prefix key가 같은 object에 있어도 구분하고,
  duplicate/invalid Unicode/잘못된 문서·경로를 명시적으로 거부했다. 실패 후보·직접 child 로그는 최종 PASS와 따로 보존했다.
- [독립 lookup runner](../../conformance/runners/django/json_lookup_reference.py)의 Django **6.1**, Python **3.14.3** 관찰은
  SQLite **3.50.4**와 PostgreSQL **17.5**/psycopg **3.3.6** 각각 **88 filter/exclude / 3 projection**이다.
  SQLite raw SHA256은 `3766c5c9a5fd6f5b42c406cd335095f75fad7c9c90f14d48111d9cfcd7adfbb9`,
  PostgreSQL raw는 `ddb5c51172ff079f2ff12b412ed10952e2721c63eb47a347322021dcac41bed9`다.
  기존 독립 probe와 결과·parameter·projection이 같으며 SQLite SQL 내부 set 출력 순서만 `PYTHONHASHSEED=0`으로 고정했다.
  SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- Affected `go vet`, Helpdesk generated **12파일 clean**, 문서 링크·gofmt·diff 검사를 통과했다. 전용 DB는 잔여 연결 0 뒤 삭제하고
  기존 service는 유지했다. 생성기 output grammar는 바꾸지 않았으며 fresh generated module이 새 generic field API를 소비한다.
  이 새 경로 source의 Hosted 검증은 아직 실행하지 않았다. Contains 등 다음 lookup을 연결한 JSON 조회 통합 milestone에서
  관련 Hosted 범위를 정한다. 앞선 JSON 수직 연결 full과 목록 후속 web 결과를 새 경로의 platform PASS로 사용하지 않는다.

### JSON containment의 로컬 통합 checkpoint

- 기준 `f02f09573282e78a0e5fdff41fe67492e5cc2d13` 위 변경과 Go/Python lock의 **23 non-Markdown 경로** manifest SHA256은
  `28bd1a7a9ab4f6df571b02445f1e9a1c8fb9ae58095532f6030b3ce111bb73c4`다. PostgreSQL에만 쓰는 path encoder의 이동으로
  삭제 1개와 새 경로 1개도 명시한다. 실행 전후 같은 바이트·삭제 상태를 확인했다.
  Go **1.26.5 darwin/arm64**, repository-pinned modernc SQLite와 PostgreSQL **17.5**, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`이다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest`:
  **8 package / 704 root / 5551 test·subtest PASS**, fail 0·stderr 0 bytes.
  단독 PostgreSQL process helper만 skip 1이며 실제 PostgreSQL/SQLite cross-process parent는 필수 PASS다. No-test package event와 구분했다.
- 새 containment·path domain과 generated JSON consumer를 `./query ./codegen/consumertest`에서 선택한 race와 CGO=0은
  각각 **2 package / 3 root / 8 PASS**, 전체 run/pass roster 동일·skip/fail 0·stderr 0 bytes다.
  앞선 source의 8-package 전체 race와 이번 선택 race를 합쳐 같은 소스의 전체 race로 표시하지 않는다.
  Generated parent는 양 DB의 path 및 containment child를 각각 필수로 요구하며 잘못된 typed contains 입력의 compile-negative도 실행한다.
- 고정 Django **6.1** 독립 runner에 별도 table의 **33개 문서 / 168개 root·key path / contains·contained_by / filter·exclude 조건**을 추가했다.
  앞선 88개 query·3개 projection과 metadata는 양 DB에서 그대로 유지됨을 확인했다.
  SQLite raw SHA256은 `949b8c5b5716e2260f79b7f51aa971f282e9c17792978f0ab999a7fde1ef4bca`,
  PostgreSQL raw는 `a4563abffc440d099ae73e4e39fc15d5c2858cffbc5d243b9b5c931d99dbda94`다.
  Python **3.14.3**에서 실제 PostgreSQL **17.5**/psycopg **3.3.6**의 전 조건이 결과를 반환했고,
  SQLite **3.50.4**는 전 조건에서 `NotSupportedError`였다. SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- 실제 generated 모델의 nullable·non-null JSON, 중첩 object/array·배열 중복/순서·scalar·JSON null·큰 인접 정수·1/1.0을 독립 PostgreSQL raw와 비교했다.
  Typed/dynamic 동일 AST·Count·allowlist·잘못된 타입과 native parameter preflight, optional forward의 nullable/non-null target·OR/NOT·eager/Count를 확인했다.
  SQLite는 All/Count·LIMIT 0·empty IN에서도 `backend_error/unsupported_feature`와 I/O 0을 확인했다. Reverse non-exact는 기존 오류 경계를 유지한다.
- Affected vet·Helpdesk **12파일 clean**·gofmt·docs/diff를 통과했다. 전용 DB는 연결 0 확인 뒤 삭제하고 service는 유지했다.
  새 JSON 경로와 containment를 묶은 Hosted `orm` scope 통합은 고정 source를 게시한 뒤 실행하며 아래에 별도로 기록한다.
  현재 로컬 결과는 새 source의 전체 platform 또는 Hosted 완료를 뜻하지 않는다.

### JSON 경로·containment Hosted ORM 통합

- [Run 35517527972](https://github.com/progresshans/godj/actions/runs/35517527972), attempt **1**, source
  `e7bc29d0288acfa633aa1078df718c1f96610e3a`의 `orm` scope가 **실행 44 job 성공 / 선언한 범위 밖 4 owner skip**으로 완료됐다.
  완전한 job logs의 실제 checkout SHA를 모두 확인했다. Gate는 `full_platform_verified:false`이며
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix` 네 owner를 검증했다.
- PostgreSQL **17.10** core normal/race/CGO=0 각각 **13 package / 1886 run·PASS / skip 0**, operator-target 각각
  **2 package / 12 run·PASS / skip 0**이다. 모든 core 실행의 필수 목록에 `TestGeneratedJSONConsumer`가 존재하며,
  해당 source의 parent가 SQLite/PostgreSQL path·containment child를 각각 필수로 검사한다.
- Linux amd64/arm64와 macOS arm64/amd64 relation·command matrix 및 portable matrix가 terminal 성공이다.
  Relation의 normal/CGO=0과 race는 workflow가 선택한 서로 다른 inventory이며 합쳐 같은 실행 수로 표현하지 않는다.
  Python compatibility, exact darwin/arm64 profile·SQLite lifecycle, project check, reference/current capture는 이 scope의 비대상이다.
  전체 플랫폼·Python oracle·현재 개발 중인 key-presence의 Hosted PASS가 아니다.

### JSON key presence의 로컬 통합 checkpoint

- 기준 `e7bc29d0288acfa633aa1078df718c1f96610e3a` 위 제품·테스트·독립 reference·Go/Python lock의
  **28 non-Markdown 경로** manifest SHA256은 `6e4d3dc92147a068e5c744821f78784bfd67e99dbb70916e6c4d1ec319b59d71`이다.
  Go **1.26.5 darwin/arm64**, repository-pinned modernc SQLite·PostgreSQL **17.5**, `GODJ_REQUIRE_POSTGRES=1`,
  `TZ=Pacific/Chatham`에서 실행 전후 같은 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest`는
  **8 package / 706 root / 5553 test·subtest PASS**, fail 0·stderr 0 bytes다. 단독 PostgreSQL process helper만 skip 1이며
  PostgreSQL/SQLite cross-process parent는 필수 PASS다. No-test package event를 test skip과 구분했다.
- 새 key AST·SQLite SQL 함수/연결과 generated consumer를 선택한 race·CGO=0은 각각
  **3 package / 4 root / 9 PASS**, run/pass roster 동일·skip/fail 0·stderr 0 bytes다. 선택 package는
  `./query ./db/sqlite ./codegen/consumertest`다. 모든 모드에서 generated parent가 양 DB의 path·containment·keys child를 필수 검사한다.
- 고정 Django **6.1** public ORM의 `key_presence`는 SQLite **19개**, PostgreSQL **17개** 문서에서 각각 **96조건**이다.
  기존 88 lookup·3 projection·containment 168조건과 metadata를 그대로 보존했다. 확장 raw SHA256은
  SQLite `bc5068339b5e786a48028a3506de2141f8f1a9d6e94601c11742d39b31fbcb82`,
  PostgreSQL `f213e1480f54bf1fb732306c73b3749d5f9febaee1e106ec7393fc2534097007`이다.
  Python **3.14.3**/SQLite **3.50.4** 및 PostgreSQL **17.5**/psycopg **3.3.6**에서 직접 관찰했고,
  SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- Generated 소비자는 nullable/non-null 모델·root/path·typed/dynamic·Count·optional forward/eager·OR/NOT를 실제 DB에서 확인한다.
  PostgreSQL NUL 12조건은 raw의 DataError와 GoDj의 preflight 오류를 구분하며 LIMIT 0·empty IN·Count도 거부한다.
  SQLite empty-list 8조건과 root empty/NUL key 12조건만 DEV-0017의 명시적인 차이로 검사한다. 나머지는 해당 DB의 raw 결과를 직접 비교한다.
  키 목록의 source/getter 복사·순서/중복·UTF-8/개수/byte 한도·policy·잘못된 typed/dynamic 입력·literal parameter·numeric key/index와
  미지원 reverse 오류를 확인했다. SQLite의 여러 physical connection·reopen에서도 두 JSON SQL 함수를 실제 호출했다.
- 초기 checkpoint는 생성 query facade에 없는 Plan 메서드를 테스트가 호출해 compile 실패했다. 공통 ORM에서 AST를 비교하도록 수정했다.
  다음 checkpoint는 reverse JSON path key 조회가 `unsupported_lookup` 대신 `invalid_plan`을 반환하여 실패했다.
  기존 relation lookup guard를 path key 생성에도 적용한 최종 source로 normal/race/CGO=0을 통과했다. 초기 실패를 PASS에 합치지 않는다.
- Affected vet·Helpdesk generated **12파일 clean**·gofmt·docs/diff를 확인했다. 생성 grammar는 변경하지 않았고 새 generic API는 fresh 외부 모듈에서 검증했다.
  전용 DB는 잔여 연결 0 뒤 삭제하고 기존 service를 유지했다. 이 key-presence source의 Hosted 통합은 아직 실행하지 않았다.
  앞선 `e7bc29d` ORM Hosted는 path·containment 증거이며 이번 key-presence의 platform PASS로 사용하지 않는다.

### JSON 경로 projection의 로컬 통합 checkpoint

- 기준 `bbf78c2331324277c7564f56d0d11be23b7ff9ca` 위 제품·테스트·reference·Go/Python lock **44 non-Markdown 경로**의
  manifest SHA256은 `ef5f07718bf49e0339d0280b42fb18369e0acdf8152d62b531d869c96acb0c81`이다. Go **1.26.5 darwin/arm64**,
  repository-pinned modernc SQLite·PostgreSQL **17.5 Homebrew**, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서
  normal/race/CGO=0 실행 전후 같은 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest ./examples/article ./examples/helpdesk`는
  **10 package / 724 root / 5574 test·subtest PASS**다. 공통 query/ORM/DB/생성 소비자의 race는
  `./query ./orm ./db/... ./codegen/consumertest` **8 package / 707 root / 5555 PASS**이며 같은 범위의 normal run/pass/skip roster와 동일하다.
  두 실행 모두 fail 0·stderr 0 bytes다. 단독 PostgreSQL process helper만 skip 1, 실제 양 DB process parent는 필수 PASS다.
- CGO=0의 새 projection AST·generated JSON consumer와 Article의 projection/report/search HTTP 회귀는
  **3 package / 4 root / 10 PASS**, skip/fail 0·stderr 0 bytes다. 모든 모드에서 generated parent가 양 DB의
  path·containment·keys·projection child를 각각 필수 검사한다. Required JSON field의 path에도 nullable DTO 인자가 필요하다는 compile-negative를 확인했다.
- [독립 projection runner](../../conformance/runners/django/json_projection_reference.py)는 고정 Django **6.1**의 public ORM에서
  값·missing·root SQL NULL을 따로 관찰한다. SQLite **32개 문서 / 8개 경로 / 256개 행 관찰**, PostgreSQL **30개 문서 / 8개 경로** 중
  NUL 경로 1개는 DataError이고 나머지 **210개 행 관찰**이다. SQLite의 12개 셀·PostgreSQL의 10개 셀 차이는 DEV-0017의 정확한 selector로 검사한다.
  SQLite raw SHA256 `78283942c850b59cbbcd137232f6172f50f43526277b9e33ce87bb373593fed6`,
  PostgreSQL raw `a8476c9a59f4c8fb4c2a732fbdedad852077deea77fb107cebe65685a8cda27f`다.
  Python **3.14.3**/SQLite **3.50.4**, PostgreSQL **17.5**/psycopg **3.3.6**의 독립 probe와 formal runner가 같은 raw를 반환했다.
  SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- 실제 생성 모델에서 whole field와 여러 path를 함께 선택하고 required/nullable source·JSON null·missing·문자열 타입·큰 정수·numeric key/index를 확인했다.
  SELECT path→WHERE path/value→LIMIT/OFFSET의 매개변수 순서, DB별 DISTINCT의 SQL/JSON null·1/1.0 의미,
  source model cache가 있어도 갱신된 DB projection을 읽고 cache를 교체하지 않는 동작, 행/셀별 pointer 소유권을 검사했다.
  잘못된/중복/빈/canceled projection의 I/O 생략과 PostgreSQL NUL preflight, 미지원 relation projection의 오류를 확인했다.
  SQLite 외부 duplicate-key write로 projection을 실패시켜 부분 결과를 버리고 수정 후 재시도하는 경로도 PASS다.
- 저수준 projection constructor를 공통 ResultExpression 목록으로 옮긴 기존 scalar 호출부·회귀를 유지했다. Source field nullability를 위조하지 않으며
  selected path 구조 비교·caller/getter 복사·2048-expression 경계·같은 source의 서로 다른 경로를 검사했다.
  Affected vet·gofmt·docs/diff와 `make generate-check`를 통과했다. Helpdesk **12**, Article **12**, relationfixture **16파일 clean** 및
  checked-in relation 생성물 회귀가 PASS다. 전용 DB는 연결 0 뒤 삭제하고 service는 유지했다.
- Key-presence와 projection, 공통 scalar 선택 표현 변경을 묶은 JSON 조회 통합 milestone에서 고정 source의 Hosted `orm` scope를 실행한다.
  이 로컬 checkpoint나 앞선 `e7bc29d` Hosted 결과를 새 source의 platform PASS로 합치지 않는다.

### JSON key presence·projection Hosted ORM 통합

- [Run 35522171383](https://github.com/progresshans/godj/actions/runs/35522171383), attempt **1**, source
  `06c83020db3082ff730dbc692df9b8a3e8932d28`의 `orm` scope가 **실행 44 job 성공 / 범위 밖 4 owner skip**으로 완료됐다.
  완전한 job logs 44개의 실제 checkout SHA와 terminal 상태를 모두 확인했다. Gate는 `full_platform_verified:false`이고
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix` 네 owner가 성공했다.
- PostgreSQL **17.10** core normal/race/CGO=0 각각 **13 package / 1887 run·PASS / skip 0**, operator-target 각각
  **2 package / 12 run·PASS / skip 0**이다. 모든 core 실행은 generated JSON parent를 필수로 선택한다.
  해당 source의 parent가 SQLite/PostgreSQL path·containment·keys·projection child를 필수 검사한다.
- Linux amd64/arm64와 macOS arm64/amd64의 relation·command 및 portable matrix가 성공했다.
  Relation normal/CGO=0은 Linux amd64 **26 package / 4959 PASS**, 나머지 세 환경 **27 package / 5007 PASS**이며,
  별도 inventory를 선택하는 race는 각각 **4894 / 4942 PASS**, 모두 test skip 0이다.
  Scope 밖인 Python compatibility·exact darwin/arm64 profile·SQLite lifecycle·project check·reference/current capture는 실행 증거에 포함하지 않는다.
- 이 결과는 key-presence·root JSON 경로 projection과 공통 scalar 선택 표현을 묶은 조회 통합 milestone의 증거다.
  이후 개발하는 관계 filter source의 DTO 선택이나 전체 프레임워크 완료를 뜻하지 않는다. 동일 source의 전체 local/platform matrix를 반복하지 않았다.

### 관계 filter source의 root DTO projection 로컬 통합 checkpoint

- 기준 `06c83020db3082ff730dbc692df9b8a3e8932d28` 위 제품·테스트·독립 reference·Go/Python lock의
  **18 non-Markdown 경로** manifest SHA256은 `e1b82c01f45e528db9a38239b2e9b8e2f72eb5619e07f69159b9f318cbe91bb7`이다.
  Go **1.26.5 darwin/arm64**, repository-pinned modernc SQLite·native PostgreSQL **17.5 Homebrew**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 실행 전후 같은 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./orm ./db/... ./codegen/consumertest ./examples/article ./examples/helpdesk`는
  **9 package / 667 root / 5382 test·subtest PASS**다. 예제를 제외한 같은 명령의 `-race`는
  **7 package / 650 root / 5363 PASS**이며 동일 package의 normal run/pass/skip roster와 일치한다.
  두 실행 모두 fail 0·stderr 0 bytes다. 단독 PostgreSQL process helper만 skip 1이고 실제 양 DB cross-process parent는 필수 PASS다.
  No-test package event를 test skip과 구분했다.
- CGO=0의 `TestGeneratedJSONConsumer|TestGeneratedNestedForwardConsumer` 선택은 **1 package / 2 root / 8 PASS**,
  skip/fail 0·stderr 0 bytes다. 모든 모드의 generated JSON parent가 새 `sqlite/related_projection`과
  `postgres/related_projection`을 필수 검사하고 기존 path·containment·keys·projection child도 유지한다.
- [독립 public runner](../../conformance/runners/django/related_projection_reference.py)는 Django **6.1**에서
  record **5개**·link **8개**, direct forward/reverse **8조건**의 root scalar/JSON 경로 선택·DISTINCT·slice **48관찰씩**을 보존한다.
  SQLite raw SHA256 `235170b1d0f561094ad7c2492cd40425ee1d0499765ba3914b188725ace448e0`,
  PostgreSQL raw `76a50468ff0d56cbf6fe2b8cbb3ce3f50c18f47d864de0340b4b9a2169326e68`다.
  Python **3.14.3**/SQLite **3.50.4**, PostgreSQL **17.5**/psycopg **3.3.6**에서 관찰했고 formal 결과가 별도 탐색 결과와 일치한다.
  SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- Generated 소비자가 양 DB의 typed/dynamic 동일 AST와 raw 행을 대조한다. Optional target의 NOT/OR·NULL과 key presence,
  reverse exact의 중복, 선택값에 따른 DISTINCT·정렬·slice·SELECT/WHERE 매개변수 순서를 확인했다.
  Raw의 Python None 표현과 별도로 missing 경로의 nil 및 존재하는 JSON null을 검사한다. 모델 cache가 DTO rowset으로 바뀌지 않는다.
  기존 nested SQLite generated fixture의 **146관찰 중 root 선택 73개**에도 DTO를 적용해 다중 hop·nullable 경로·순환 선언·reverse 조건을 대조했다.
- DISTINCT에서 선택하지 않은 ordering의 명시적 거부, LIMIT 0·empty IN·취소의 SQLite I/O 0,
  native NUL 경로의 empty source 포함 preflight와 기존 non-count 관계 aggregate 경계를 확인했다.
  Related target-column DTO·related-object hydration과 DTO 결합·일반 관계 MIN/MAX는 지원한다고 주장하지 않는다.
- 독립 탐색의 첫 slice 비교는 이미 평가한 QuerySet의 slice가 list가 되어 `list.count()`를 호출하는 harness 오류였다.
  Fresh QuerySet에서 count/materialize 순서를 분리해 수정하고 raw를 다시 관찰했다. 이 탐색 오류를 Django의 조회 결과나 제품 실패 의미로 채택하지 않았다.
- Affected vet·gofmt·docs/diff와 `make generate-check`를 통과했다. Helpdesk **12**, Article **12**, relationfixture **16파일 clean** 및
  checked-in 관계 생성물 회귀가 PASS다. 검증 뒤 전용 PostgreSQL DB의 잔여 연결 0을 확인해 삭제했고 기존 service는 유지했다.
  이 후속 source의 Hosted/platform 검증은 아직 실행하지 않았다. 앞선 `06c8302` Hosted 결과는 새 관계 DTO 구현의 검증이 아니다.
- 최종 format-check에서 새 consumer의 anonymous struct 반환 부분에 gofmt의 두 번째 줄바꿈 정리가 필요했다.
  해당 테스트 파일만 서식을 정리하고 go/parser로 주석을 포함한 AST가 검증본과 같음을 확인했다. 제품 코드 바이트는 동일하며 기능 테스트를 중복 실행하지 않았다.
  최종 18경로 manifest는 `34fa0335d39d45d6fec90d5363ee713177cdadfc325f8517057bc023dc1cc298`이고 format/docs/diff를 다시 통과했다.

### Forward 대상 JSON 경로 선택의 로컬 통합 checkpoint

- 기준 `9b802e904056bfa26351ee7bf68aa4677e687a6d` 위 제품·테스트·독립 reference·Go/Python lock의
  **24 non-Markdown 경로** manifest SHA256은 `c811742039a173a82e0bffc041c232bdda5f6da9a13226b3b9dab84f17bfbace`다.
  Go **1.26.5 darwin/arm64**, repository-pinned modernc SQLite·native PostgreSQL **17.5 Homebrew**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 실행 전후 같은 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest ./examples/article ./examples/helpdesk`는
  **10 package / 727 root / 5576 test·subtest PASS**다. 예제를 제외한 같은 범위의 `-race`는
  **8 package / 710 root / 5557 PASS**이며 같은 package의 normal run/pass/skip roster와 일치한다.
  둘 다 fail 0·stderr 0 bytes다. 단독 PostgreSQL process helper만 skip 1, 실제 양 DB process parent는 필수 PASS다.
- CGO=0의 새 query AST·공통 JOIN 소유권과 JSON/nested 생성 소비자 선택은 **3 package / 4 root / 10 PASS**,
  skip/fail 0·stderr 0 bytes다. 모든 모드의 generated parent가 기존 root/path·containment·keys·projection·related_projection과
  새 양 DB `forward_projection` child를 필수 검사한다. No-test package event를 기능 skip과 구분한다.
- [독립 public runner](../../conformance/runners/django/forward_json_projection_reference.py)는 Django **6.1**의
  document **7개**·shelf **5개**·entry **8개**에서 required/nullable parent·child의 네 경로를 관찰했다.
  조건 없음·AND/OR/NOT·선택값 DISTINCT·slice **144관찰씩**과 대상 부재·원본 SQL NULL·missing의 별도 inventory를 보존한다.
  SQLite raw SHA256 `0bc0c48a3ee94cdca5954d070b836f3c10b9dd5a4e0ae640a0edff0088ca7591`,
  PostgreSQL raw `7287660c6952d85ca18fa3269474e776cde232dd1aa3a6106854d7f50c772034`다.
  Python **3.14.3**/SQLite **3.50.4**, PostgreSQL **17.5**/psycopg **3.3.6**에서 직접 관찰했다.
  양 DB의 144개 관찰과 absence 목록은 일치하며, SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- Generated direct selector와 `ChainForward`를 실제 양 DB에서 사용했다. Typed/dynamic predicate의 같은 AST,
  filter 없이 selection으로 생기는 JOIN·여러 alias에서 같은 target field 선택·JSON null과 nil·정렬/slice·선택값 DISTINCT를 대조했다.
  Query의 route 구조 비교·caller/getter 복사·root FK nullability와 table 검증, filter와 selection의 root/table 선언 충돌을 검사한다.
  공통 JOIN 준비의 동시 실행·작업 map/배열 소유권과 SQLite의 selection-only 64-hop JOIN 한도 거부도 포함한다.
- 원본 모델에 JSON 필드가 없는 실제 entry/shelf에서 JSONB를 선택하여 native adapter를 확인했다.
  트랜잭션 안의 target 수정·projection read·rollback 원복·만료된 session 거부, target 변경 뒤 최신 projection과 원본 모델 cache 분리,
  row pointer 소유권·empty/invalid/canceled SQLite I/O 0·native NUL의 empty source 포함 preflight가 PASS다.
  SQLite 외부 duplicate-key write의 부분 결과 폐기와 수정 후 재시도도 확인했다. Reverse path 선택은 기존 unsupported 경계를 유지한다.
- 첫 checkpoint는 테스트가 root label을 관계 전용 dynamic parser에 전달하여 실패했다. 일반 field parser로 수정했다.
  다음 checkpoint는 PostgreSQL의 native row adapter가 root/eager 필드만 보고 target JSONB 선택의 변환을 생략해 실패했다.
  선택 결과 표현도 adapter 필요 여부에 포함했다. SQL NULL/JSON null과 SQLite BLOB 거부 규칙을 약화하지 않았고,
  세 번째 고정 source에서 normal/race/CGO=0을 완료했다. 실패 로그와 외부 생성 모듈의 직접 재현은 성공에 합치지 않는다.
- Affected vet·gofmt·docs/diff와 생성 결과 검사를 확인했다. Helpdesk **12**, Article **12**, relationfixture **16파일 clean** 및
  checked-in 관계 생성물 회귀가 PASS다. Generator/예제 선언 바이트가 같은 범위의 drift 검사이며 fresh JSON 소비자는 최종 source로 매번 생성했다.
  전용 PostgreSQL DB는 잔여 연결 0 뒤 삭제했고 service는 유지했다.
- 앞선 root 관계 DTO와 이번 forward target 경로·native row adapter를 묶은 조회 통합 milestone에서 고정 source의 Hosted `orm`을 실행한다.
  이 로컬 결과와 앞선 `06c8302` Hosted를 현재 source의 platform PASS로 합치지 않는다.

### Root 관계 DTO·forward JSON 경로 Hosted ORM 통합

- [Run 35526500195](https://github.com/progresshans/godj/actions/runs/35526500195), attempt **1**, source
  `fb9651919c0169bc8f0cb3f094b6b43a8f9065fd`의 `orm` scope는 **실행 44 job success / 범위 밖 4 owner skip**으로 완료됐다.
  완전한 job logs 44개의 checkout SHA와 종료 상태를 대조했다. Gate는 `full_platform_verified:false`이며
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix` 네 owner가 성공했다.
- PostgreSQL **17.10** core normal/race/CGO=0 각각 **13 package / 1887 run·PASS / skip 0**, operator-target 각각
  **2 package / 12 run·PASS / skip 0**이다. 모든 core 실행은 generated JSON parent를 필수로 선택한다.
  해당 source의 parent는 SQLite/PostgreSQL path·containment·keys·projection·related_projection·forward_projection child를 필수 검사한다.
- Linux amd64/arm64와 macOS arm64/amd64의 relation·command 및 portable matrix가 성공했다.
  Relation normal/CGO=0은 Linux amd64 **26 package / 4960 PASS**, 나머지 세 환경 **27 package / 5008 PASS**이고,
  race는 각각 **4895 / 4943 PASS**이며 모두 test skip 0이다. Scope 밖인 Python compatibility·exact Darwin profile·
  SQLite lifecycle·project check·reference/current capture는 이 실행에 포함하지 않는다.
- 이 결과는 root 관계 DTO와 forward JSON 경로·native adapter의 통합 증거다. 후속 whole JSON/일반 scalar 선택의
  소스나 프레임워크 전체 완료로 합치지 않는다. 동일 source의 local/platform 전체 matrix를 중복 실행하지 않았다.

### Forward scalar·whole JSON 선택의 로컬 통합 checkpoint

- 기준 `fb9651919c0169bc8f0cb3f094b6b43a8f9065fd` 위 제품·generated 소비자·독립 raw·CI 필수 목록·Go/Python lock의
  **26 non-Markdown 경로** manifest SHA256은 `c83f7226b4c75b9f6a821df71549ba7ef2d3b1cbee4d5161083581b7c5ad8c2c`다.
  Go **1.26.5 darwin/arm64**, repository-pinned SQLite·native PostgreSQL **17.5 Homebrew**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 실행 전후 같은 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest ./examples/article ./examples/helpdesk`는
  **10 package / 729 root 실행 / 5580 test·subtest PASS**다. 예제를 제외한 같은 명령의 `-race`는
  **8 package / 712 root 실행 / 5561 PASS**이며 동일 package의 normal run/pass/skip 전체 목록과 일치한다.
  두 실행 모두 fail 0·stderr 0 bytes이며 단독 PostgreSQL helper guard만 skip 1이다. 실제 양 DB cross-process parent는 필수 PASS다.
- CGO=0은 새 scalar AST·기존 JSON route 및 JOIN 소유권·scalar/JSON/nested 생성 소비자를 선택해
  **3 package / 6 root / 14 PASS**, skip/fail 0·stderr 0 bytes다. 새 generated parent는 실제 SQLite/PostgreSQL child를
  모두 필수 검사하며 기존 JSON child의 path·containment·keys·projection·related_projection·forward_projection도 유지한다.
- [독립 public runner](../../conformance/runners/django/forward_scalar_projection_reference.py)는 Django **6.1**, Python **3.14.3**,
  SQLite **3.50.4**·PostgreSQL **17.5**/psycopg **3.3.6**에서 11종 필수·nullable 필드, datum 3개·holder 4개·entry 6개를 사용한다.
  네 required/optional 2-hop route의 **96개 조회·88개 DISTINCT 결과·8개 JSON SQL NULL/대상 부재 목록씩**을 보존했다.
  SQLite raw SHA256 `0e79df53cbf535bed9ee3a591f449c3c94eb0570f7c64bf76b0b7a459e474fc0`,
  PostgreSQL raw `a33d78d346cc29fb582903aa6512a2489ed9eb503a379b7c1848133c34633ce5`다.
  관찰 목록은 양 DB에서 일치한다. SQLite fresh 비교는 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **1 PASS**, skip/warning 0이다.
- Generated direct 및 chained selection은 typed/dynamic 동일 source AST, filter 없음·NOT/OR·정렬·DISTINCT·slice에서
  22개 필드의 실제 결과를 raw와 비교한다. 큰 정수·binary64 bits·Decimal scale·UTC/microsecond·UUID와 whole JSON을 확인한다.
  Nullable target·optional ancestry는 모두 pointer 결과로 표현하고 원본 field metadata 및 Decimal precision은 유지한다.
  SQL NULL/대상 부재는 nil, 존재하는 JSON null은 non-nil로 별도 검사한다.
- Root에 native scalar가 없는 entry에서 Decimal·Duration·UUID·JSON target을 혼합 선택한다. Root/target 열의 qualification,
  서로 다른 행의 pointer 소유권, model cache 분리와 transaction read·rollback·만료 session 거부를 확인했다.
  GoDj의 별도 exact Decimal 저장 정책은 `9007199254740993.125000`의 실제 target read/rollback으로 검증하며
  Django SQLite NUMERIC의 정밀도 관찰과 혼동하지 않는다.
- 같은 이름의 target ID가 DISTINCT의 root ordering 요구를 만족하지 않게 한다. LIMIT 0·empty IN에도 명시적인
  capability 오류를 유지하며 duplicate/unbound/configuration 오류·취소는 SQLite I/O 0이다. Reverse 선택은 unsupported다.
  Non-null callback 오용과 다른 모델 필드 혼합은 실제 generated module의 compile 실패로 검사했다.
- Affected vet·gofmt/docs/diff·CI 도구 **37개**, Actionlint **v1.7.12**를 통과했다. Actionlint의 ShellCheck/Pyflakes는 비활성이다.
  `make generate-check`에서 Helpdesk **12**, Article **12**, relationfixture **16파일 clean** 및 checked-in 관계 소비자 PASS다.
  새 parent를 PostgreSQL/관계 CI 필수 목록에 추가했지만 새 source의 Hosted 실행 증거로 기록하지 않는다.
  전용 PostgreSQL DB의 잔여 연결·사용자 table 0을 확인해 삭제했고 기존 service는 유지했다.
  앞선 Hosted ORM 결과는 이전 source의 증거이며, 새 query 확장의 platform 검증은 다음 통합 milestone이 소유한다.

### JSON literal 대소 비교의 로컬 통합 checkpoint

- 기준 `d0957f214b58de3da2f15ccb12cade3f24a92d04` 위 제품·generated 소비자·독립 raw 및 Go/Python lock
  **27 non-Markdown 경로** manifest SHA256은 `624788d1503ad8509338767839c85018f80d99432319b2dc4c834aa7e48d36b9`다.
  Go **1.26.5 darwin/arm64**, repository-pinned SQLite·native PostgreSQL **17.5 Homebrew**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 실행 전후 동일 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest ./examples/article ./examples/helpdesk`는
  **10 package / 730 root 실행 / 5582 test·subtest PASS**다. 예제를 제외한 같은 명령의 `-race`는
  **8 package / 713 root 실행 / 5563 PASS**이며 공통 package의 run/pass/skip 전체 목록이 normal과 같다.
  두 실행 모두 fail 0·stderr 0 bytes다. 단독 PostgreSQL helper guard만 skip 1이며 실제 양 DB cross-process parent는 필수 PASS다.
- CGO=0의 JSON AST·정밀 비교 함수·physical connection/reopen·JSON/scalar/nested 생성 소비자는
  **3 package / 7 root / 16 PASS**, skip/fail 0·stderr 0 bytes다. Generated JSON parent는 양 DB의 새 comparison child와
  기존 path·containment·keys·projection·related_projection·forward_projection을 필수 검사한다.
- [Public runner](../../conformance/runners/django/json_comparison_reference.py)는 Django **6.1**, Python **3.14.3**,
  SQLite **3.50.4** 및 PostgreSQL **17.5**/psycopg **3.3.6**에서 37개 문서의 root/path·forward
  **448개 대소 비교·4개 Boolean 조합**을 각각 관찰했다. 별도 canonical SQLite profile은 기존 GoDj 저장 표현을
  public JSONField encoder로 관찰하며 기본 Django parity로 표시하지 않는다. Raw SHA256은 다음과 같다.

  - 기본 SQLite: `79424207674a57184edc72d360726f44114bbeaebb2a13cfd18786483f86fa8f`
  - 기본 PostgreSQL: `74574bd379674e151fbd8ca80e45f0949e95763d1c99ac4f1c0e7cbd2eebcd89`
  - Canonical SQLite: `67baa67324109b2a74c45d8208d045f14874a8585866460fb5b465276931a219`
- 두 SQLite profile의 차이는 DEV-0017의 정확한 32조건에서 Unicode `d15` 또는 `l_d15` 한 행의 포함 여부다.
  나머지 대소 비교 및 Boolean 조합의 동일함도 검사했다. Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각
  **1 test / 두 profile PASS**, skip/warning 0이다. Raw의 root/path 정렬 네 개는 독립 관찰이며 GoDj 정렬의 제품 PASS가 아니다.
- Typed/dynamic 동일 AST의 실제 cold Count·All·DTO 행을 각각 해당 reference에 대조했다.
  Nullable forward OR/NOT·원본 SQL NULL·missing/JSON null, root TEXT와 path 비교값의 DB별 차이를 유지한다.
  SQLite path의 array/object RHS에 대한 48개 ProgrammingError 관찰은 GoDj의 pre-I/O unsupported로 검사한다.
  Empty IN·LIMIT 0·Count·DTO에서도 같은 오류이며 SQLite query count 0이다. Root와 PostgreSQL path compound RHS는 실제로 비교한다.
- SQLite path의 큰 정수·긴 소수·signed zero·1e400/1e-400·4,000자리 지수를 반올림/지수 확장 없이 비교한다.
  별도 **289개 numeric 쌍**은 독립 rational 계산과 대조했다. 두 physical connection과 reopen에서도 함수를 호출하며
  invalid/duplicate/Unicode/크기 초과 문서는 오류다. 양 DB의 실제 precision 조회, empty/NUL key 구분,
  native NUL/number expansion의 empty-source preflight, invalid literal·lookup policy·reverse 경계도 확인했다.
- Consumer 작성 중 FK field-set에 없는 `RecordID` accessor를 사용한 compile 오류를 확인하고 실제 generated relation의
  `Record.IsNull` API로 수정했다. Runtime checkpoint는 위 최종 source에서 normal/race/CGO=0 모두 통과했다.
  Affected vet·format/docs/diff 및 `make generate-check`도 PASS다. Helpdesk **12**, Article **12**, relationfixture **16파일 clean**과
  checked-in 관계 소비자를 확인했다. 전용 PostgreSQL DB는 잔여 연결·사용자 table 0 뒤 삭제했고 기존 service는 유지했다.
- 앞선 forward scalar·whole JSON 선택과 이번 대소 비교를 묶은 고정 source의 Hosted `orm` 통합 milestone을 진행한다.
  이 로컬 checkpoint나 이전 `fb96519` Hosted 결과를 새 source의 Hosted/full-platform PASS로 합치지 않는다.

### Forward scalar 선택·JSON 대소 비교 Hosted ORM 통합

- [Run 35531599504](https://github.com/progresshans/godj/actions/runs/35531599504), attempt **1**, source
  `8fe1281460d6c58cec7d09d937b69561b5831d0d`: **44 job success / 4 scope skip**.
  API의 전체 48 job·완전한 44개 성공 로그의 checkout SHA·terminal/step 상태·실행 inventory와 최종 gate를 대조했다.
  `scope: orm`, `full_platform_verified: false`, owner는 command-product-matrix·portable-go-matrix·postgresql-product·relation-product-matrix다.
- PostgreSQL **17.10** core normal/race/CGO=0 각각 **13 package / 1891 run=PASS / skip 0**, operator-target 각각
  **2 package / 12 PASS / skip 0**다. `TestGeneratedJSONConsumer`와 `TestGeneratedForwardScalarConsumer`가
  core 필수 목록에 포함되며 generated parent는 실제 native 하위 실행을 검사한다.
- 관계 product는 Linux amd64 normal/CGO=0 각각 **26 package / 4966 PASS**, 나머지 세 OS/arch 각각
  **27 package / 5014 PASS**다. Race는 각각 **4901 / 4949 PASS**이며 모두 skip 0이다.
  Command product 12개 조합은 각각 **1 package / 33 PASS / skip 0**다. Portable matrix도 같은 source에서 성공했다.
- 이 결과는 일반 forward scalar 선택과 JSON literal 비교의 통합 증거다. 후속 정렬 변경이나 전체 platform/cold milestone의
  성공으로 확대하지 않는다. 이전 JSON full과 이 ORM scope는 서로 다른 source·검증 범위다.


### JSON 경로·forward scalar 정렬의 로컬 통합 checkpoint

- 기준 `8fe1281460d6c58cec7d09d937b69561b5831d0d` 위 제품·generated 소비자·독립 raw 및 Go/Python lock
  **41 non-Markdown 경로** manifest SHA256은 `deabe0b0ee1f77cf372e26d8b4eb2d4f2b416e5a440aef81017324ad8b003fa4`다.
  Go **1.26.5 darwin/arm64**, repository-pinned SQLite·native PostgreSQL **17.5 Homebrew**,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 실행 전후 동일 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=10m ./query ./orm ./db/... ./codegen/consumertest ./systemstate ./examples/article ./examples/helpdesk`는
  **11 package / 795 root 실행 / 5766 test·subtest PASS**다. 예제를 제외한 같은 명령의 `-race`는
  **9 package / 778 root 실행 / 5747 PASS**다. 공통 package의 전체 run/pass/skip roster가 동일하다.
  Fail 0·stderr 0 bytes이며 PostgreSQL 단독 process helper guard만 skip 1이다. 실제 양 DB process parent는 필수 PASS다.
- `CGO_ENABLED=0`은 ordering AST·SQLite numeric sort/compiler·기존 JSON AST/comparison·physical connection/reopen과
  JSON/forward scalar/nested forward generated 소비자 **3 package / 10 root / 19 PASS**, skip/fail 0·stderr 0 bytes다.
  JSON parent는 양 DB의 `comparison/ordering` child를 새 필수 목록으로 검사하며 기존 하위 실행도 유지한다.
- 고정 Django **6.1**·Python **3.14.3**, SQLite **3.50.4**·PostgreSQL **17.5**/psycopg **3.3.6**에서 JSON reference의
  root/path·forward × ASC/DESC × DISTINCT × slice **32개**, forward scalar 22 field × 네 경로 × 같은 조합 **704개**를
  각각 관찰했다. Model/DTO의 순서와 cold Count를 함께 기록하며 기존 비교·선택·NULL·distinct bag 관찰은 모두 불변이다.
  Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** fresh reference 각각 **2 test / 3 SQLite profile PASS**다.
  Raw SHA256은 다음과 같으며 JSON의 canonical SQLite는 GoDj 저장 정책의 별도 관찰이다.

  - JSON 기본 SQLite: `18eebf9f311e6fceba11ceb91fe87ad1c73c3bd1b306eb650d73468e2baf543e`
  - JSON 기본 PostgreSQL: `b7b7b969ccbc107d9926b73e608cc769a4ab527abb25a8b03fedf858bcf309d7`
  - JSON canonical SQLite: `3841edd65f918af5f5f52cace9b95a2e664c5ee859ca94de6754c3056c13adef`
  - Forward scalar SQLite: `119ed439e85bd6c041c62bed1c8ea4fcb1314d2a031f1a0aab2b6f718bbb7649`
  - Forward scalar PostgreSQL: `d085c274581c84ad0b4548cb1f5d93e47315ab794853bda7568e3c0614fa6ca4`
- JSON path sort key의 **24×24=576개** numeric 쌍을 독립 `big.Rat` 연산과 비교했다. 4000자리 지수·큰 이웃 정수·긴 소수·
  signed zero·negative coefficient prefix·NULL/TEXT·NUL suffix와 malformed 입력을 검증한다. 두 physical connection과 reopen에서
  새 함수가 실제 SQL 정렬 비교를 수행했다. Generated 제품은 ±1e400·±1e-400·-1.201/-1.2 및 큰 정수를 실제 저장/정렬했다.
- Root/target/path identity·소유권·source metadata·방향, WHERE/SELECT/ORDER/LIMIT parameter 순서, optional ordering-only JOIN,
  full-model DISTINCT의 숨은 셀과 원래 row shape, selected-path DTO DISTINCT의 PostgreSQL ordinal, eager presence·native scalar
  readback, cold Count·slice·root MAX derived column 이름과 native NUL의 Count/빈 조회 사전 거부를 확인했다.
  테스트의 Optional 결과 접근은 compile-only 단계에서 `Get()`으로 바로잡았으며 최종 runtime checkpoint는 첫 후보에서 모두 통과했다.
- Affected vet·gofmt·diff/docs와 `make generate-check` PASS다. Helpdesk **12**, Article **12**, relationfixture **16파일 clean**과
  checked-in relation 소비자 PASS다. 전용 DB의 잔여 연결·사용자 table **0**을 확인해 삭제하고 기존 service는 유지했다.
  이 로컬 checkpoint는 후속 정렬 source의 platform 검증이 아니다. 공통 ordering/result compiler·JOIN 변경의 Hosted ORM 통합을
  다음 고정 source milestone으로 실행하며 위 `8fe1281`의 Hosted 성공을 새 변경의 검증으로 대체하지 않는다.



### JSON 경로·forward scalar 정렬 Hosted ORM 통합

- [Run 35533945489](https://github.com/progresshans/godj/actions/runs/35533945489), attempt **1**, source
  `d504cf99ec27a0ed34435e14afab8697fb9b1b2d`: **44 job success / 4 scope skip**.
  전체 48 job metadata와 완전한 44개 성공 로그의 checkout SHA·terminal/step 상태·실행 inventory를 대조했다.
  Gate는 `scope: orm`, `full_platform_verified: false`, command-product-matrix·portable-go-matrix·postgresql-product·relation-product-matrix의 네 owner다.
- PostgreSQL **17.10** core normal/race/CGO=0 각각 **13 package / 1891 run=PASS / skip 0**, operator-target 각각
  **2 package / 12 PASS / skip 0**다. Generated JSON·forward scalar parent의 필수 실행과 native child 검사를 유지한다.
- 관계 product의 Linux amd64 normal/CGO=0 각각 **26 package / 4969 PASS**, 나머지 세 OS/arch 각각
  **27 package / 5017 PASS**다. Race는 각각 **4904 / 4952 PASS**이며 모두 skip 0이다.
  Portable·command product matrix도 같은 source에서 성공했다. 누락되거나 잘린 로그를 PASS로 사용하지 않았다.
- 이 결과는 JSON/forward 정렬 source의 통합 검증이다. 후속 JSON 문자열 검색이나 전체 platform 검증을 뜻하지 않는다.



### JSON 문자열 검색·Helpdesk 소비자 로컬 통합 checkpoint

- 기준 `d504cf99ec27a0ed34435e14afab8697fb9b1b2d` 위 제품·테스트·독립 raw·생성 client와 Go/Python/config lock
  **51 non-Markdown 경로**의 정렬 manifest SHA256은 `2f786c98b0c9e5c43cd4ef95311803bac3a1bd65b9ca1e32b6e859824760fac3`다.
  Go **1.26.5 darwin/arm64**, repository-pinned SQLite·native PostgreSQL **17.5 Homebrew**, `TZ=Pacific/Chatham`,
  `GODJ_REQUIRE_POSTGRES=1`에서 normal/race/CGO=0 전후 동일 바이트를 확인했다.
- Normal `go test -json -count=1 -timeout=12m ./query ./orm ./db/... ./admin ./codegen/consumertest ./examples/helpdesk ./api/openapi ./api/openapi/consumertest ./examples/article`은
  **13 package / 814 root 실행 / 5810 test·subtest PASS**다. Article만 제외한 같은 범위의 `-race`는
  **12 package / 801 root 실행 / 5795 PASS**이며 공통 package의 run/pass/skip roster가 동일하다.
  Fail 0·stderr 0 bytes, PostgreSQL 단독 process helper guard만 skip 1이다. 실제 양 DB process parent는 필수 PASS다.
- `CGO_ENABLED=0`은 query·SQLite·generated JSON/nested forward·양 DB Helpdesk·독립 client의 필수 root를 선택했다.
  **5 package / 8 root / 16 PASS**, skip/fail 0·stderr 0 bytes다. Generated JSON parent는 양 DB의 새 `text` child와
  기존 storage/path/containment/key/projection/comparison/ordering child를 모두 필수로 확인한다.
- 고정 Django **6.1**·Python **3.14.3**, SQLite **3.50.4**·PostgreSQL **17.5**/psycopg **3.3.6**의 별도 관찰은
  **53개 입력 / 184개 root/path·root/forward·filter/exclude 조건 / 3개 Boolean 조합**이다. Native NUL write 3개와
  NUL 검색 조건 16개의 실패도 검사하며 query 오류는 normal/Count/DTO·LIMIT 0·empty IN 모두 I/O 전 거부한다.
  SQLite 기본·canonical 저장은 별도 raw이며 정확한 숫자 token/NUL 검색의 **28조건** 차이는 제품 함수를 사용하지 않는
  독립 SQLite instr/lower probe가 관찰한다. 각 차이의 원래 Django 행 목록·selector와 미사용 차이 0도 확인한다.
  Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 fresh **1 test / 2 SQLite profile + 정책 probe PASS**, skip/warning 0이다.
  Raw SHA256은 다음과 같다.

  - 기본 SQLite: `8c8097439dfa2a81ad3a9f1db6dc38da69d425d09f8c8c02a3da5a8eaad9c990`
  - 기본 PostgreSQL: `21dc6cfc21547c0022540559953a03e3ede5b87c47a89476cb802c6c131542f9`
  - Canonical SQLite: `c9a6d67281788221bab29bf50b0108c863bd269a58b5f328992d94d7e6066731`
  - 정확한 숫자/NUL 정책 차이: `abe23425bca7ceb0c96dc82e2ef985815978cf531db6421cd5dcb865dedd4327`
- 실제 generated 모델은 typed/dynamic AST 일치·lookup policy·문자열 operand 경계·Boolean/optional JOIN·cold Count·All·DTO를
  양 DB에서 확인한다. SQLite physical connection 두 개와 reopen에서 NUL을 포함한 검색어와 suffix가 보존되는지 검사한다.
  큰 정수·4000자리 지수·literal %, _, 역슬래시·ASCII case·잘못된 UTF-8/한도와 SQL NULL/missing/JSON null도 구분한다.
- Helpdesk의 실제 양 DB HTTP는 subject/whole external JSON OR 검색, source path·AND·빈 결과·Admin 검색과 category 제한을
  검사한다. Unknown/duplicate·escape/UTF-8/NUL·64-byte 값/2048-byte query 한도를 400으로 거부하며 인증·권한이 먼저다.
  잘못된 query와 권한 거부는 제품 DB 조회 0이다. 기존 저장·rollback·재접속·권한 유지 검증도 같은 parent에서 실행했다.
- 실제 API 선언으로 고정 ogen **v1.24.0** client를 재생성했다. Article 두 profile은 불변이며 Helpdesk schema와 generated
  **4파일**만 변경했다. Module/config lock은 불변이다. Parent/child **35 receipt**·race mode, 생성 drift·offline build·실제
  HTTP·최종 DB를 검사한다. JSON 검색 fixture는 public PATCH로 준비/복원하며 각 GET의 새 CSRF 상태를 유지한다.
  Python 3.14.3·openapi-spec-validator **0.7.2**·jsonschema **4.25.1**로 세 OpenAPI **3.1.1** 문서와 검색 매개변수를 독립 검증했다.
- 첫 실행은 디스크 여유 135 MiB와 기존 query 무시 기대값 때문에 실패했다. 재생성 가능한 Go build cache 약 95 GiB를 정리하고,
  기존 bare list/Accept 검증은 유지하면서 unknown query의 400·pre-I/O 회귀로 옮겼다. 옛 JSON icontains 미지원 기대값도
  JSON value operand 거부로 바꾸고 문자열 성공은 독립 raw 전체로 검증했다. 실패/중간 로그는 최종 PASS와 구분해 보존했다.
  실제 SQLite 함수 호출에서 driver의 TEXT 인자가 NUL에 잘려 빈 검색어가 되는 결함을 확인해 needle을 BLOB으로 전달했다.
  Client는 검색 GET 뒤 CSRF 상태를 갱신하지 않아 복원 PATCH가 403이던 fixture를 수정했다. 독립 SQL probe의 연결도 명시적으로 닫아
  Python 3.13+ ResourceWarning을 해결했다. 정책·권한·필수 실행을 완화하지 않고 최종 source의 전체 affected scope를 다시 검증했다.
- Affected vet·gofmt·diff/docs·generated drift PASS다. Helpdesk **12**, Article **12**, relationfixture **16파일 clean**과
  checked-in relation 소비자 PASS다. 전용 DB는 잔여 연결·사용자 table **0** 확인 후 삭제했고 기존 service는 유지했다.
  이 변경의 Hosted `web` 통합은 다음 고정 source milestone이 소유하며 앞선 `d504cf9`의 ORM 성공과 합치지 않는다.



### JSON 문자열 검색·Helpdesk Hosted web 통합

- [Run 35537035733](https://github.com/progresshans/godj/actions/runs/35537035733), attempt **1**, source
  `ef9b05c0ec829d5cb38c0944507a70f0a5e1f483`: **32 job success / 5 scope skip**.
  전체 37 job metadata와 완전한 32개 성공 로그의 checkout SHA·terminal/step 상태·gate를 대조했다.
  Gate는 `scope: web`, `full_platform_verified: false`, command-product-matrix·portable-go-matrix·postgresql-product의 세 owner다.
- PostgreSQL **17.10** core normal/race/CGO=0 각각 **13 package / 1892 run=PASS / skip 0**,
  operator-target 각각 **2 package / 12 PASS / skip 0**다. Generated JSON parent의 양 DB `text` child와
  기존 필수 child, 실제 PostgreSQL Helpdesk의 검색·권한·migration·rollback은 해당 source에서 실행됐다.
- Portable integration normal/race/CGO=0 각각의 완전한 package 출력에서 `api/openapi/consumertest`,
  `codegen/consumertest`, `examples/helpdesk`의 실제 성공을 확인했다. 독립 client parent는 source에 고정된
  **35 receipt**와 race mode·실제 schema/재생성/HTTP/최종 DB 검사를 요구한다. Command product의 선택한 operator matrix도 성공했다.
- 이 결과는 JSON 검색 source의 web 통합이다. 이후 고유성 독립 기준이나 전체 platform/미구현 기능의 PASS로 사용하지 않는다.


## GDJ-0093 — UUID 모델과 외부 연동 참조

- [GDJ-0093](../../work/0093-uuid-models.md)는 Decimal 완료 제품 위에 Helpdesk 외부 UUID 참조를 연결하는 다음 작업이다.
  아래 결과는 독립 기준 준비이며 UUID Go 제품의 runtime 또는 Form/Admin/API 완료를 뜻하지 않는다.
- [Runner](../../conformance/runners/django/uuid_reference.py)는 고정 Django 6.1·DRF 3.18.0 public model/Form/serializer와 SQLite schema editor·ORM을 실행한다.
  GoDj 구현/expected fixture를 import하지 않는다. [Raw](../../internal/uuidtest/testdata/django61.json)는 model **62**, Form **86**, serializer **252**,
  실제 JSON token **13**, representation 네 종류, 기존 행의 nullable add·zero literal default·root/forward/F query·Min/Max·rollback·reopen·reverse를 포함한다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 fresh 비교 각각 **1 PASS**, skip/warning 0이다. 실제 Python/SQLite version/source_id를 별도로 확인했다.
  최초 raw는 Python 3.14.3 / SQLite 3.50.4다. Python `UUID(int=bool)`의 public `.int`가 bool을 유지하는 관찰과 기본 wire의 0/1 정규화를 그대로 보존했다.
- Runner·fresh test·raw 세 파일의 정렬 manifest SHA256은 `219e48ca8ea8407bb2805a2fd41d1bc01e8b184dead48c49dfa793b75ffb3c65`,
  raw SHA256은 `2007304fa9f7828ffd8198fa87c4ba4d5f88372033cd3c90001ed327867ca02b`다.
- 별도 native PostgreSQL **17.5 (Homebrew)** 전용 DB probe에서 UUID canonical 출력·unsigned 순서를 확인했다. Native MIN/MAX(uuid)는 SQLSTATE 42883이며
  `MIN/MAX(value::text COLLATE "C")::uuid`는 같은 경계값 순서를 보존했다. Native raw SHA256은 `971248f41cc1da5c3491f6438819db2e1e9603ed9f4f82b23151c42871dffb78`다.
  이 사전 실험은 Django PostgreSQL 또는 GoDj 제품 PASS가 아니다. 해당 probe 전용 DB는 잔여 연결 0 확인 뒤 삭제했다.

### UUID 모델·DB·생성 소비자 로컬 checkpoint

- 기준 source `153bf08531d7ff59a795386a8f2e643d250efe4c` 위 reference 세 파일과 제품/테스트 55개, non-Markdown **58개 파일**의
  정렬 manifest SHA256은 `a6e04f228b5b6e546d7be843dc05381d89ec9cf194f57f1409f28ae7064334ee`다. 일반/race/CGO=0 종료 뒤 같은 바이트를 확인했다.
  Reference 세 파일은 `0290baca8de347cd51593f9249249c041d64c505`에도 별도로 게시했다. 뒤의 CI 선택 보강·문서와 아직 연결 중인 입력 코드는 이 manifest와 구분한다.
- UUID `[16]byte` 값·정규 IR/default·strict wire/resource/digest·typed/dynamic AST/ORM·생성 model/write/root/eager scan과
  SQLite CHAR(32)·PostgreSQL native UUID parameter/catalog/row adapter를 연결했다. 고정 128-bit 전체 범위와 zero/NULL을 구분한다.
- Generated 실제 외부 소비자는 양 DB에서 기존 행의 nullable AddField·literal default·reopen·typed/dynamic root/forward 조회·IN/F·
  projection/cache ownership·unsigned 정렬·direct/DISTINCT/sliced Min/Max·empty/all-NULL 집계를 확인한다. PostgreSQL의 NULL 정렬 위치는 native 의미로 별도 확인한다.
  Reverse exact UUID terminal·eager snapshot 격리·transaction rollback·실패한 Save/선택 update mask·취소-before-I/O·reverse/reapply와
  실제 생성 코드의 string/integer/다른 field type compile 거부도 포함한다. Parent 수에 generated child test 수를 더하지 않는다.
- SQLite 외부 noncanonical TEXT·BLOB·integer/float 입력의 scan 오류와 이전 scanner 값 제거, PostgreSQL UUID native parameter·driver text·catalog drift 거부를 확인했다.
  PostgreSQL backend의 `search_path=pg_catalog` 계약 위에서 UUID 집계의 C collation과 native UUID 반환 type을 유지한다.
- Go **1.26.5 darwin/arm64**, modernc SQLite **v1.56.0**, pgx **v5.10.0**, PostgreSQL **17.5 (Homebrew)** 전용 DB,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 affected **16 package**를 fresh 일반·race로 실행했다.
  각각 **6,558 test·subtest PASS (root 1,088)**, run/terminal roster 동일·실패 0·stderr 0 bytes다.
  유일한 helper-only skip `TestPostgresRevisionFenceHelperProcess`의 실제 process 부모는 양 mode 모두 PASS다.

```text
./uuid ./schema ./schema/ir ./query ./orm ./db/internal/queryplan ./db/sqlite ./db/postgres
./codegen ./codegen/consumertest ./internal/irresource ./internal/projectspec ./internal/projectwire
./migrations ./migrations/definition ./internal/migrationautodetect
```

- CGO=0 UUID 선택은 **4 package / 9 test·subtest PASS (root 6)**, skip 0·stderr 0 bytes다. 실제 generated SQLite/PG 소비자와 세 compile 거부를 포함한다.
- Affected vet 출력 0 bytes, gofmt/diff, `make generate-check`의 Helpdesk/Article/relationfixture와 checked-in generated test를 확인했다.
  PostgreSQL Hosted 필수 목록에 UUID native adapter와 generated consumer를 추가했으며 CI tooling **37 PASS**다. 새 UUID source의 Hosted 완료는 아직 없다.
- 이 모델 checkpoint 뒤의 Form/Admin·serializer·OpenAPI·실제 Helpdesk/client 입력 연결은 아래 별도 checkpoint가 소유한다.

### UUID 입력·Helpdesk·독립 API client 로컬 통합

- 제품 기준은 UUID 모델 기반 commit `dd321af2622b878fdfa3b80910150017bdae3a8d`다. 변경된 non-Markdown **62개 파일**의
  정렬 manifest SHA256은 `4ea151998ed4336e8d1c618c217cea882d84391f543b000f74806f6bfcb6d4ee`다. 일반/race/CGO=0/vet 전후 같은 source를 확인했다.
  아래 로컬 결과를 아직 실행하지 않은 Hosted source의 PASS로 옮기지 않는다.
- Form은 독립 86개 입력의 cleaned value·error·세 initial의 변경 감지를 대조한다. Canonical 초기값과 원문 오류·escaping·NULL·zero·default ownership을 확인했다.
  Serializer 252개 중 **240개 직접 비교**, Python bytes 객체 **8개 명시적 미지원 selector**, 공통 JSON 문서 NUL 거부 **4개**를 구분했다.
  정확한 JSON token 13개와 typed float 거부를 확인했으며 ModelEncoder가 NULL을 필수 응답 필드로 출력한다.
- 초기 7 package 실행은 serializer 테스트가 DRF의 `.data`와 `validated_data` 생략을 같은 것으로 취급한 두 fixture assertion에서 실패했다.
  Bind의 생략 presence와 ModelEncoder의 완전한 응답을 나누어 검증하도록 수정했으며, serializer 회복 실행과 아래 최종 전체 영향 검증은 PASS다.
  첫 9 package 통합은 Helpdesk의 이전 Form field count 12에서 실패했다. 13개 선택으로 갱신한 뒤 양 DB의 전체 부모 흐름을 다시 실행했다.
- 입력 검토에서 Go Unicode **15.0**과 pinned Python 3.14 Unicode **16.0**의 숫자 범위 차이를 확인했다.
  독립 runner에 model/Form/DRF 각각의 **76범위·760문자** 관찰을 추가하고 입력 표를 Unicode 16.0으로 고정했다.
  이전 raw의 다른 key 값은 모두 같음을 확인했다. 갱신 raw SHA256은 `56ba7762830600aef9c629966c4060178717b771ee06bb1a798c0b1cc3ccbfc9`,
  runner·fresh test·raw 세 파일 manifest는 `2f5c87955a40364a6f873e03ac7c49fac64007cf05620d123d0dcfd786594c67`이다.
  Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** fresh 비교 각각 **1 PASS**, skip/warning 0이며,
  앞 두 runtime의 Unicode 15에는 새 범위 8개가 없다는 차이를 명시적으로 확인했다. Go 입력 검증은 pinned 760문자를 모두 대조한다.
- 실제 Helpdesk 선언에 nullable `external_reference`를 추가하고 생성기와 `makemigrations`로 **0014_ticket_external_reference**를 만들었다.
  기존 행 NULL 추가·128-bit 값 저장·reopen·reverse/reapply, 실제 Admin 별칭/no-op/escaping/blank clear와 API create·PUT/PATCH·omission/null/zero를 확인한다.
  Invalid/range/float/중복 key 입력의 transaction 진입 전 거부, CSRF·category 격리·injected rollback·fresh reader 보존도 포함한다.
  과거 Decimal precision 역방향 실패 시 뒤의 0014 reverse가 먼저 확정될 수 있어, 기존 비용 회귀는 그 historical prefix에 실제 존재하는 비용 열만 projection한다.
  큰 비용 보존·명시적 복구·재적용 검증을 제거하지 않았다.
- 실제 API 선언에서 Article Bearer/Session과 Helpdesk Session OpenAPI를 내보내 pinned **ogen v1.24.0**으로 세 profile을 새 디렉터리에 생성했다.
  Helpdesk schema/생성물을 게시했으며 Article 생성물·`go.mod`·`go.sum`·`ogen.yml`은 동일하다. 독립 모듈은 GoDj import/replacement 없이 build한다.
  실제 HTTP와 최종 DB까지 **32개 필수 완료 receipt**, canonical UUID 전송·null/생략·zero/2^64/high-bit/max, required/malformed/type 응답 거부를 검사한다.
  기존 wire 오류 fixture에도 새 nullable 응답 필드를 넣어 UUID 누락이 다른 오류의 검증을 대신하지 않도록 했다.
- Go **1.26.5 darwin/arm64**, modernc SQLite **v1.56.0**, pgx **v5.10.0**, PostgreSQL **17.5 Homebrew** 전용 DB,
  `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`에서 아래 **9 package**를 fresh 일반·race로 실행했다.
  각각 **3,398 test/subtest PASS (root 168)**, 실행·terminal roster 동일, skip/fail 0, stderr 0 bytes다.

```text
./internal/uuidinput ./forms ./forms/model ./admin ./serializers ./api ./api/openapi
./examples/helpdesk ./api/openapi/consumertest
```

- CGO=0은 Helpdesk의 SQLite/PG 실제 부모와 독립 API client **2 package / 3 root PASS**, skip/fail 0, stderr 0 bytes다.
  Generated child의 검증 수를 부모 테스트 수에 더하지 않는다. Affected vet 출력 0 bytes다.
- `make generate-check`의 Helpdesk/Article/relationfixture와 checked-in generated 회귀는 PASS다.
  Helpdesk generated snapshot은 `42928d3e7a9d5f616bdf7b1f2be94a84a1c745bf324c328699e310de9b979cc5`다.
  별도 pinned openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**으로 실제 3.1.1 문서 세 개와
  Ticket/create/update/patch의 canonical UUID string/null schema를 검증했다. 처음 사용한 deprecated `validate_spec` shortcut의 경고로 중단된
  validator harness는 현행 `validate` API로 교체했으며 `-W error` 검증은 warning 없이 PASS다.
- Unicode runtime 차이를 포함한 최종 UUID 통합은 Hosted **full** checkpoint를 명시적으로 선택한다. 로컬 전체는 중복 실행하지 않는다.
  Hosted의 exact/compatibility reference와 플랫폼별 필수 실행이 끝나기 전까지 이 work는 active다.

### Hosted 통합에서 발견한 외부 복구 fixture 의존성 보완

- UUID 제품 source `4e5d1b910b73f59991fb6f002c517c40d76525e2`의 [첫 Hosted full](https://github.com/progresshans/godj/actions/runs/35502966024), attempt 1에서
  Portable integration normal/race/CGO=0 job `106057905862` / `106057905785` / `106057905780`이 같은 원인으로 실패했다.
  `TestRelationDeleteProductNamespaceCollisionAndRecoveryPreserveGeneratedTargets/mandatory_recovery_precedes_publication_rejection`이
  repository 밖 fixture에 현재 제품의 `uuid` source를 복사하지 않아 `-mod=readonly` candidate compile에서 실패했다.
  따라서 해당 실행을 전체 통합 성공으로 기록하지 않는다.
- 실제 복사 목록에 `uuid`를 포함했다. 제품 코드·잠금·publication/recovery assertion을 바꾸거나 skip하지 않았다.
  수정 fixture SHA256은 `7b4c0c4d5bc63dd8d6f2c87c356fbce5347930cb3c622e705d9f413fd6b45871`이다.
  같은 parent의 일반/race/CGO=0 로컬 재실행은 각각 **1 package / 3 test·subtest PASS (root 1)**, skip 0·stderr 0 bytes이며
  injected cleanup interruption 뒤 mandatory recovery가 namespace rejection보다 먼저 실행되고 기존 generated target이 유지됨을 확인했다.
- UUID 로컬 전용 DB는 잔여 연결 0을 확인한 뒤 삭제했으며 기존 PostgreSQL service는 유지했다.
  수정 source의 Hosted full을 별도 실행한다. 현재 UUID 전체 플랫폼 완료는 아직 아니다.

### UUID Hosted full 완료

- 최종 [Hosted full run 35503256679](https://github.com/progresshans/godj/actions/runs/35503256679), attempt **1**, source
  **`7da91ad5fbd6284622fb372e7d8051120584424e`**가 terminal success로 완료됐다. **62개 고유 job 모두 success**이며 job skip/failure/cancel 0이다.
  각 job의 전체 로그와 SHA256·고유 name/ID·실제 첫 checkout SHA를 대조했다. 로그의 git 경로를 OS별로 고정하지 않았다.
- Gate job **106063201144**의 실제 출력은 `scope: full`, `full_platform_verified: true`이며
  command-product·conformance·exact-Darwin·portable·PostgreSQL·project-check·Python-compatibility·relation **8개 owner**를 확인했다.
  Portable **12**, relation **12**, project-check **12**, command-product **12**의 선언된 좌표가 모두 있다.
  `Product project check (ubuntu-24.04, normal)`의 **Cold external CLI build milestone**도 실제 success다.
  다른 11개 좌표의 같은 cold step·비소유 capture publication·portable owner로 대체되는 exact job의 focused Go step은 조건에 따른 중복 실행 제외다.
- Exact Darwin/arm64 Python은 **297 tests, skips 0**이다. Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**은 각각
  **297 tests, 선언된 exact 전용 skips 4**와 semantic digest를 완료했다. UUID Unicode 16/구버전 Unicode 차이도 이 source에 포함된다.
- PostgreSQL **17.10** core normal/race/CGO=0은 각각 **13 package / 1,880 PASS**, run=pass·skip 0이다.
  operator-target normal/race/CGO=0은 각각 **2 package / 12 PASS**, skip 0이다. 필수 UUID native adapter·generated consumer와
  실제 Helpdesk 부모, 기존 Decimal cached-reader/NaN/lock 회귀가 required inventory를 통과했다.
- 실제 portable integration의 외부 복구 fixture normal/race/CGO=0도 모두 success다.
  첫 source의 run **35502966024**는 세 integration 실패 뒤 후속 실행으로 대체되어 terminal **cancelled**다.
  그 실행의 최종 job 집계는 success 30 / failure 4(세 integration과 결과 gate) / cancelled 28이며 완료 증거로 사용하지 않는다.
- 후속 게시 source `9814af2ab443e240d59f631807965b1c685c78e5`는 Markdown과 JSON 독립 runner/test/raw만 추가했다.
  UUID Go 제품·생성물·고정 client·CI 설정은 검증 source와 동일하다. 새 JSON reference의 실행과 앞으로의 제품 변경은 위 GDJ-0094 절에서 따로 관리한다.
  이 결과로 GDJ-0093의 현재 연결을 완료하며 전체 프레임워크·남은 UUID PK/FK/uniqueness/generation까지 완료 처리하지 않는다.

## GDJ-0092 — Decimal 정밀도 변경의 독립 기준 준비

- Baseline은 GDJ-0091 제품 source `d106e73d5338cff107623351c48ac4f5778fff8c`다. 현재 GoDj의 precision AlterField 구현 완료를 뜻하지 않는다.
- [독립 runner](../../conformance/runners/django/decimal_precision_reference.py)는 고정 Django 6.1의 public ProjectState·CreateModel·AlterField·
  MigrationAutodetector·schema editor를 실행한다. GoDj 코드나 expected fixture를 import하지 않는다.
- [Raw 관찰](../../internal/decimaltest/testdata/precision-changes-django61.json)은 **12개 profile**의 정밀도 확장·정확한 축소·반올림·whole overflow·
  scale/whole 교환·zero whole/scale·empty·identity를 기록한다. Forward/reverse의 SQL·물리 값·typed read와 재접속을 구분한다.
  Identity만 autodetect 0이며 나머지 11개는 AlterField다. SQLite의 물리 값은 보존되지만 조회값은 반올림될 수 있고 세 profile의 6행은 InvalidOperation이다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 fresh test 각각 **1 PASS**, skip/warning 0이다.
  Python 버전과 실제 SQLite version/source_id를 확인한 뒤 나머지 전체 관찰을 동일한 raw와 비교했다.
  초기 raw 환경은 Python 3.14.3 / SQLite 3.50.4다.
- Runner·fresh test·raw 세 파일의 정렬된 SHA256 manifest는 **`731a118d291c63ce151937e56bdf3eaadbf9394d1626a92f77e2c1dcfd042a25`**,
  raw SHA256은 **`3ad2d3a4b5bdfcb627ae28db54fbf056364825928857ec3ce1b79d04fe402221`**이다.
- 별도의 native PostgreSQL **17.5 (Homebrew)** 전용 DB 사전 실험은 같은 12개 profile을 실행했다. Django PostgreSQL 실행과 구분한다.
  세 overflow profile은 SQLSTATE 22003으로 실패하고 값·typmod를 보존했다. Scale 축소는 1.225→1.23, 2.5→3으로 실제 값을 바꾸며
  reverse는 사라진 자릿수를 복구하지 못한다. Native raw SHA256은 `03b5e001049a05df891719de5b7780da300f953b87cf423cebb2c9be8e3f1d6b`다.
  이 native scratch 관찰은 설계 판단 자료이며 GoDj 제품/Hosted PASS나 Django 관찰의 대용이 아니다. 전용 DB는 연결 0 확인 뒤 삭제했다.

### 정밀도 변경 제품·로컬 통합 checkpoint

- 제품·생성물·테스트 후보는 baseline `9ed86fe4a91d646da375d175fa4c6c2758111d72` 위 변경된 non-Markdown **59개 현재 파일**이다.
  정렬된 SHA256 manifest는 `c4def02ecfd86a341fbaa0a809ef193c4db388c8b1a491dbc089634fcdbab615`다.
  Choices 전용 이름의 backend helper 세 파일은 일반 field-change 구현으로 교체했다. 새 테스트·제품 바이트는 최종 실행 전후 같은 manifest로 확인한다.
- IR delta 분류·historical definition/default·autodetect·독립 Decimal capability와 실제 양 DB precision AlterField를 연결했다.
  기존 transaction/잠금에서 before/after precision을 검사한다. SQLite는 BLOB·schema_version을 보존하고 PostgreSQL은 검증 뒤 NUMERIC typmod를 바꾼다.
  반올림/whole overflow, 잘못된 외부 storage 및 NaN을 거부하고 실패 시 값·physical schema·history·private revision을 유지한다.
- Generated 실제 소비자는 독립 12 profile을 양 DB에서 실행하며, 다섯 명시적 lossless 거부 profile과 일곱 보존 profile을 구분한다.
  기존/새 generated model·새 큰 값 뒤 reverse 거부·명시적 값 수정 뒤 복원·fresh reopen, capability 거부·schema edit 직후 실패,
  incoming FK와 기존 eager query cache 보존을 포함한다. DB row-query/iteration/close/cancellation driver fault는 별도 simulated I/O 경계다.
- 초기 실행은 새 reference query가 ID를 정렬하면서 selected metadata에 빠뜨린 fixture 오류로 실패했다. ID를 포함해 올바른 Query AST로 고쳤다.
  이어 실제 PostgreSQL에서 NUMERIC precision을 바꾼 뒤 cached result descriptor가 SQLSTATE 0A000으로 실패했다.
  반환 Decimal 열의 선언 precision을 SQL cache identity에 포함해 해결했으며, cast/값 변환이나 전역 cache 비활성화·자동 retry를 추가하지 않았다.
  별도 reader pool의 **같은 physical PID**를 유지한 root·DISTINCT projection·transaction 조회의 expand/reverse/reapply가 회귀 owner다.
- Helpdesk 선언을 Decimal(12,2)→(14,2)로 변경하여 실제 generate와 makemigrations로 `0013_alter_ticket_expected_cost`를 만들었다.
  기존 비용·관계·이력이 있는 DB의 확장·큰 값 뒤 reverse 실패/복구와 전체 Form/Admin/API 흐름을 실행했다. 실제 세 OpenAPI 문서와
  고정 ogen v1.24.0 client를 재생성했으며 Article 문서/생성물과 module/tool locks는 동일하다. 독립 client의 30개 필수 receipt를 유지한다.
- Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5(Homebrew) 전용 DB, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`이다.
  최종 일반 실행 `go test -count=1 -json -p=4 -timeout=20m`은 **21 package / 6,433 test·subtest PASS (root 1,090)**, 실패 0이다.
  같은 범위 `-race -p=4 -timeout=25m`도 **21 package / 6,433 test·subtest PASS (root 1,090)**다.
  두 mode의 정확한 run/terminal roster와 skip 집합이 같고 필수 process/recovery·DB/cache·generated/API 부모, package 완료, stderr 0 bytes를 확인했다.

```text
./schema/ir ./migrations ./migrations/backend ./migrations/definition ./internal/migrationautodetect
./db/sqlite ./db/postgres ./codegen/consumertest ./examples/helpdesk ./api/openapi ./api/openapi/consumertest
./conformance/choicesproduct ./internal/projectcheck ./internal/projectcheck/linked ./internal/projectcheck/sqlmigrateprotocol
./cmd/godj ./conformance/projectsqlmigrateproduct ./conformance/migrationrelationproduct
./conformance/projectmigratetargetproduct ./conformance/definitionload ./conformance/postgresproduct
```

- 일반 실행의 skip 7개는 subprocess helper 4개와 명시적 Linux-only deleted-cwd 회귀 3개다. Helper의 실제 process/recovery 부모가 실행되었고,
  Linux-only 세 회귀는 macOS의 PASS로 세지 않는다. 해당 플랫폼 실행은 Hosted full 통합 milestone이 소유한다.
- CGO=0 Decimal/precision·실제 Helpdesk·독립 client 선택 범위는 **5 package / 28 test·subtest PASS (root 15)**, skip 0·stderr 0 bytes다.
  Parent 결과에 generated child test 수를 더해 부풀리지 않는다.
- Affected vet, gofmt/diff, Helpdesk/Article/relationfixture generated drift, Helpdesk makemigrations candidate 0을 확인했다.
  실제 OpenAPI 세 문서는 고정 openapi-spec-validator 0.9.0 / jsonschema 4.26.0 / referencing 0.37.0의 표준 검사도 통과했다.
- 전용 precision PostgreSQL DB는 잔여 연결 0 확인 뒤 삭제했고 기존 service는 유지했다.
- 제품 source는 `06f601ed4d939d4f60b2313b4c70b33eaf4a5939`다. 같은 source의 [Hosted full](https://github.com/progresshans/godj/actions/runs/35496796910),
  attempt 1은 **62개 고유 job 모두 success**, 실패·취소·job skip 0이다. 모든 job의 source/name/ID와 checkout SHA를 대조했다.
  Gate의 `scope:full`, `full_platform_verified:true`, 필수 owner 8개를 확인했다. Relation 12개, command 12개, project-check 12개 좌표와
  PostgreSQL 17.10 normal/race/CGO=0 × core/operator-target 6개의 완전한 inventory·필수 선택을 재확인했다.
- 추가 감사에서 PostgreSQL의 명시적 required-test 선택에 직접적인 foreign NUMERIC adapter·precision cached reader·NaN/lock 회귀 세 root가 없음을 확인했다.
  Generated Decimal 소비자의 12 profile/실패/관계 경로는 같은 Hosted source에서 실행됐지만 이 세 직접 DB root의 실행과는 구분한다.
  필수 목록을 보강한 CI-only source `153bf08531d7ff59a795386a8f2e643d250efe4c`의 [후속 reference scope](https://github.com/progresshans/godj/actions/runs/35499184070),
  attempt 1은 **14개 필수 job success**다. 범위 밖 matrix owner 네 개는 계획대로 skip이며 full PASS에 합치지 않는다.
  모든 성공 job의 checkout SHA/name/ID와 완전한 로그를 대조했다. PostgreSQL core normal/race/CGO=0 각각 **13 package / 1,875 test·subtest PASS**,
  operator-target 각각 **2 package / 12 PASS**, test skip 0이다. Strict event auditor는 보강한 세 root의 실제 terminal PASS를 필수로 확인한다.
  Python 네 버전·exact darwin/arm64·same-run capture conformance도 완료했다. Gate는 `scope:reference`, `full_platform_verified:false`, 필수 owner 4개다.
  위 59개 제품 manifest가 06f601e와 153bf08에서 동일함을 확인했다. 제품 전체 검증 62개 job과 CI 선택 보강의 추가 실행을 구분한다.

## GDJ-0091 — Decimal의 독립 정밀도·저장 기준 준비

- 초기 기준 준비 source `885590259b464a448df2ff3b825ad93a244ed283`: [GDJ-0091](../../work/0091-decimal-cost-models.md), branch `feature/decimal-models`. 아래 원본 hash는 이 초기 source의 관찰이다. 현재 제품 진행은 별도 checkpoint에 기록한다.
- Runner·reference test·raw JSON 세 파일의 정렬된 SHA256 manifest는 `3a16077823474753f8b106a4c103d710f8da262bf5226eb83cbcd648e0e42561`다.
  [Raw 관찰](../../internal/decimaltest/testdata/django61.json)의 SHA256은 `947dde60d6d0f634a96802567a7a069ff0833ff2e130e685d546cbd21f3bdb77`다.
- 고정 Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5의 public API로 Model **89**·Form **136**·serializer **360**·
  실제 JSON number **19**·precision 조합 **90**, SQLite add/reopen/update/rollback/reverse·root/relation query 각 **9**를 기록했다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**의 fresh subprocess reference를 각각 **1 test PASS**, skip/warning 0으로 확인했다.
  Python·SQLite fingerprint는 실제 runtime과 대조하고 나머지 관찰을 전부 비교했다. 첫 assertion의 sNaN changed 범위 가정을 고쳐
  초기 null의 true와 세 non-null 초기값의 InvalidOperation을 정확히 구분한 뒤 다시 실행했다. 실제 public API 예외는 raw에 그대로 남겼다.
- SQLite 저장 probe 9개 중 `123456789012345678.123456789012`는 `123456789012345680.000000000000`로 조회됐고,
  `999999999999999999.999999999999`는 정수 `1000000000000000000`으로 저장된 뒤 조회에서 InvalidOperation을 냈다.
  추가 probe는 각 transaction을 rollback했고 reverse 뒤 기존 8개 행만 남았다.
- 별도 **Go 1.26.5 / pgx v5.10.0 / PostgreSQL 17.5(Homebrew)**의 native NUMERIC binary probe 14개를 실행했다.
  NUMERIC(30,12)는 위 두 30자리 값을 정확히 보존했다. NUMERIC(12,2)의 ±1.225는 ±1.23이었고 SQLite/Django 읽기는 ±1.22였다.
  자릿수 overflow는 SQLSTATE 22003, signed zero는 양수 zero였다. Native NaN 허용을 Django Decimal 모델 허용으로 채택하지 않는다.
  임시 table의 transaction rollback과 잔존 table 없음을 확인했으며 기존 service는 유지했다.
- 별도 storage prototype의 두 Go test는 **2,005개** 유한한 Decimal을 coefficient/exponent로 보존했다. 부호·adjusted exponent·정규화 digit의
  field 독립 binary key 순서와 big.Rat 숫자 순서를 대조하고, 실제 SQLite BLOB의 정렬·비교 150조합·column equality·Min/Max를 확인했다.
  형식의 strict decode와 canonical round-trip도 PASS다. 제품 package·migration 또는 다른 OS/driver의 검증은 아니다.
  Prototype 두 source의 정렬 SHA256 manifest는 `63d3dfba9776b26d44dfaa014e2a9c2ef4f1f6edc3f314ef20f0bc1eca8cccb8`다.
- 이 초기 기준·prototype 자체는 GoDj Decimal의 runtime·Hosted 지원 증거가 아니다. Float Hosted source에도 포함되지 않는다.

### Exact JSON profile 추가

- 원래 관찰을 보존한 채 public `json.loads(parse_float=Decimal)` profile 19개와 NUMERIC(30,12) JSON precision 비교 4개를 추가했다.
  최종 세 reference 파일 manifest는 `9725788c4019496373c291f4ac267e35c158d2b4d3a3e726df446e7518fdb277`,
  raw SHA256은 `411d51330e98f2d8784ab48bc44c65df050198a06c131cabd2172ef2ff929d03`이다.
- Python 3.12.13 / 3.13.15 / 3.14.3 / 3.14.7의 fresh reference test 각각 1 PASS, skip/warning 0이다.
  초기 source와 비교해 두 새 JSON key를 제외한 전체 관찰이 동일함을 확인했다.
- 기본 float profile과 lexical Decimal profile의 12/2 오류 차이는 `1.200`, ±`1e309`, ±`1e-9999`의 5개 selector다.
  30/12에서는 큰 decimal token의 float 반올림·경계 초과와 bare integer의 정확한 보존을 분리했다.
  [DEV-0016](../DEVIATIONS.md#dev-0016--decimal의-정확한-입력저장과-초과-scale-거부)은 이 차이와 양 DB의 exact 저장 선택을 명시한다.

### 모델·DB 통합 checkpoint 완료

- [ADR-0069](../adr/0069-exact-decimal-values-and-storage.md)의 값·IR·query·generator·ORM·physical migration 묶음을 구현했다.
  Form/Admin·serializer·Helpdesk/OpenAPI/client는 아직 연결 전이며 Decimal 전체 수직 단면 완료가 아니다.
- 초기 normal checkpoint에서 기존 두 테스트가 새 지원 타입 `decimal`을 미지원 타입으로 사용해 실패했다.
  해당 음성 fixture를 `unknown`으로 바꾸고 미지원 kind와 Decimal precision 오류를 구분했다. 기존 테스트의 거부 위험은 유지한다.
- 추가 1000자리 생성 소비자 테스트는 `First`에 명시적 ordering을 누락해 실패했다. 조회에 ID 정렬을 추가했으며 제품의 unordered-query 거부를 유지했다.
  수정 후 생성 소비자와 compile-time float/string/integer 혼용 거부 3개 subtest가 통과했다.
- 원시 migration IR budget의 FloatBits 문자열 누락도 Decimal 문자열과 함께 보강했다. Default·choice의 active/inactive arm과 aggregate/per-string cap을 검사한다.
- 최종 모델·DB source `49c3ebef02b43e92e50abe70b6779814f72d43b9`는 baseline `47772f8d2920dba8fdd99dabef5c14c4bd4a391e` 위 제품·reference·테스트 변경 **69개 파일**로 묶었다.
  정렬된 SHA256 manifest는 `c6cd6a19e1418b64aad9df308d2109815b8c47e74f2966e4b3f99ea96ebc0559`다. 모든 checkpoint 종료 뒤 같은 파일 바이트를 다시 대조했다.
- Local macOS arm64, Go 1.26.5, PostgreSQL 17.5, TZ=Pacific/Chatham에서 다음 affected 17개 package를 normal·race 각각 fresh 실행했다:
  `decimal`, `internal/decimalstorage`, `schema`, `schema/ir`, `query`, `orm`, `db/internal/queryplan`, `db/sqlite`, `db/postgres`, `codegen`,
  `codegen/consumertest`, `internal/irresource`, `internal/projectspec`, `internal/projectwire`, `migrations`, `migrations/definition`, `internal/migrationautodetect`.
  각각 **17 package / 6,517 test·subtest PASS**(root 1,069), 실패 0이다. 유일한 helper-only skip `TestPostgresRevisionFenceHelperProcess`의 실제
  부모 `TestPostgresRevisionFenceCrossProcessIntegration`는 양 mode 모두 PASS다. 필수 실행을 skip으로 대체하지 않았다.
- CGO=0은 SQLite·PostgreSQL·generated consumer의 Decimal 선택 범위 **3 package / root 5개·subtest 포함 8개 PASS**, skip 0이다.
  Generated child의 실제 SQLite/PG lifecycle receipt와 세 compile-time 오타입 거부가 포함된다. Parent summary의 개수에 child test 개수를 더해 부풀리지 않는다.
- Generated model의 공통 12/2 lifecycle·root/forward query 각 9개를 고정 Django SQLite reference와 대조했다. 30자리 값과 1000자리 한도,
  cross-scale F·literal comparison, nullable/default, cache ownership, projection/Min/Max, write-before-I/O 거부, 실패·취소·rollback·재접속·reverse migration을 실제 양 DB에서 확인했다.
  SQLite 외부 TEXT/integer/float/잘못된 BLOB·선언 범위 밖 key와 PostgreSQL NaN/Infinity·범위 밖 NUMERIC을 scanner가 거부한다.
- Affected `go vet`, `make generate-check`(Helpdesk/Article/relationfixture clean 및 checked-in generated test), gofmt/diff 검사 PASS다.
  Linux 386 / CGO=0의 값·IR·query·ORM·양 backend와 생성 model/project를 cross-compile했다. 해당 환경의 runtime PASS를 뜻하지 않는다.
- 전용 Decimal PostgreSQL DB의 잔여 연결 0과 삭제 완료를 확인했고 기존 service는 유지했다.
- Decimal Form/Admin·serializer·Helpdesk/OpenAPI/client와 Hosted 검증은 다음 단계다. 이 checkpoint를 Decimal 수직 단면 전체 또는 전체 platform PASS로 표시하지 않는다.

### Form/Admin·Helpdesk·독립 client 통합 checkpoint 완료

제품 source `d106e73d5338cff107623351c48ac4f5778fff8c`는 baseline `58141a9f3c06b4508c1044aea2585df06c4bcde8` 위 제품·생성물·소비자·CI 변경 **62개 파일**이다.
정렬된 `<sha256>  <relative-path>\n` manifest는 `021911e0fd94374845d4c19dc55983e8409f37c2f849b0ebcd2640206174d620`이다.
각 실행 뒤 같은 바이트를 다시 확인했다. Markdown 기록은 이 manifest와 구분한다.

환경은 Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL **17.5 (Homebrew)**의 전용 DB와 독립 schema다.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`, `go test -count=1 -json -p=4 -timeout=20m`을 사용했다.

```text
./admin ./api ./api/openapi ./api/openapi/consumertest ./codegen ./codegen/consumertest
./conformance/projectoperatorproduct/attestation ./conformance/systemstate/attestation
./db/internal/queryplan ./db/postgres ./db/sqlite ./decimal ./examples/article/apiapp ./examples/helpdesk
./forms ./forms/model ./internal/compiletest ./internal/decimalinput ./internal/decimalstorage ./internal/floatvalue ./internal/irresource
./internal/migrationautodetect ./internal/projectgenerate ./internal/projectspec ./internal/projectwire
./migrations ./migrations/definition ./orm ./query ./schema ./schema/ir ./serializers
```

- 일반 **32 package / 10,039 test·subtest PASS**(root 1,358), 같은 범위 race **32 package / 9,974 PASS**(root 1,351)다.
  Run/terminal의 정확한 roster, 모든 package 완료, 필수 부모와 stderr 0 bytes를 확인했다.
- 차이 65개는 `internal/compiletest`의 `!race` 7 root와 하위 사례다. 두 mode의 유일한 helper-only skip은
  `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`이며 실제 process/recovery 부모는 모두 PASS다.
- Form reference 136개는 cleaned/errors와 null/±0/1.5 초기값의 changed를 비교한다. sNaN의 6개 Python changed 예외를 원문 그대로
  식별하고 Go의 invalid/changed 동작은 DEV-0016으로 분리한다. Precision profile은 bounded 72개를 대조하며 unbounded 18개는 지원 밖으로 세었다.
- Serializer 360개는 직접 field 입력 **324**, Python Decimal의 명시적 text projection **20**, non-finite Go Float constructor 거부 **12**,
  JSON parser의 NUL 거부 **4**로 구분했다. Bounded precision 72개와 lexical JSON 19개·큰 숫자 4개를 고정 public API 결과와 대조했다.
  기본 float JSON profile을 바꾸지 않으며 typed Go Decimal이 Python 객체의 원문 scale을 보존한다고 주장하지 않는다.
- Admin은 원문 자릿수 검증·fixed-scale 초기값과 snapshot 비교·재검증, 잘못된 typed 값·precision·문자열 우회 거부를 검증했다.
  실제 소비자 점검에서 빠진 Decimal 초기값/snapshot 비교를 보완했고 양 DB의 non-null 비용 편집을 다시 확인했다.
- Helpdesk `0012_ticket_expected_cost`의 nullable Decimal(12,2)를 실제 migration·로그인·Admin·API create/PUT/PATCH에 연결했다.
  `0.1`의 `"0.10"` 응답, ±최대값·소수·±0 no-op·생략/null·빈 form, escape·CSRF·category 격리·invalid-before-transaction,
  실패 rollback·fresh reopen을 실제 SQLite/PG에서 확인했다. 기존 필드와 영속 권한 회귀도 같은 부모 테스트에서 실행했다.
- 독립 ogen v1.24.0 client는 실제 앱 문서 세 개와 생성물을 재대조하고 **30개 필수 receipt**, HTTP 왕복과 최종 DB를 검증했다.
  Decimal은 문자열이며 required nullable response·잘못된 타입/scale·누락 거부와 명시적 request.Validate를 구분한다.
  Article 문서/생성물 및 module/tool lock은 변경하지 않았다. 표준 문서 검사는 openapi-spec-validator 0.9.0,
  jsonschema 4.26.0, referencing 0.37.0에서 세 profile 모두 PASS다.
- 격리된 생성물 복구 fixture에서 새 Decimal runtime package link 누락을 발견해 추가했다. Namespace 충돌·mandatory recovery·정상 생성물
  보존 검증을 유지한 채 같은 32개 package를 다시 실행했다. 초기 실패 실행은 위 최종 PASS 수에 포함하지 않는다.
- CGO=0의 generated Decimal·독립 client·Helpdesk SQLite/PG 선택 검증은 **3 package / root 4개·subtest 포함 7개 PASS**, skip 0이다.
  Generated child의 실제 DB 완료와 잘못된 Go 타입 거부를 부모가 요구한다. 자식 숫자를 부모 합계에 더하지 않는다.
- Affected vet, generated drift 세 project·checked-in generated, Helpdesk `makemigrations --check`의 candidate 0, CI tooling 37 tests,
  docs/format/diff 검사 PASS다. 새 Form/serializer/Admin/OpenAPI 및 Helpdesk 생성 model/project의 Linux/386/CGO0 cross-build도 PASS다.
  Cross-build를 386 runtime 증거로 표시하지 않는다. 전용 PostgreSQL DB는 잔여 연결 0 확인 뒤 삭제했고 기존 service를 유지했다.

### Hosted ORM 완료

- [실행 35490634932](https://github.com/progresshans/godj/actions/runs/35490634932), workflow_dispatch, attempt **1**,
  source **`d106e73d5338cff107623351c48ac4f5778fff8c`**가 terminal success다.
- Attempt 전용 jobs API의 전체 pagination을 검사했다. **고유 job 48개: success 44 / scope 제외 skipped 4 / 실패·취소 0**이다.
  모든 job의 head SHA와 terminal 상태, ID·name 중복 없음을 확인했다. Product project check, exact Darwin reference,
  Python compatibility, reference/capture 검증은 ORM scope 밖의 네 owner이며 필수 실행의 skip이 아니다.
- 최종 `CI result (orm)` 로그는 `scope:orm`, `full_platform_verified:false`와
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix` 네 owner의 완료를 확인했다.
- Linux amd64/arm64와 macOS arm64/Intel의 relation normal·race·CGO0 **12개 job** 로그에서 complete inventory와 skip 0을 재확인했다.
  PostgreSQL **17.10** actual core normal·race·CGO0 **3개 job**의 inventory와 Decimal 생성 소비자 필수 roster도 확인했다.
  해당 15개 실행 및 최종 gate 로그의 checkout SHA는 위 source다.
- 같은 제품 event head의 [Fast feedback](https://github.com/progresshans/godj/actions/runs/35490627372)도 terminal success다.
  실제 PR merge checkout `dd2e26039fcaa849068dd80378ae68847e0e34e1`의 tree `5aa5b2e81c1911fb7ad56512fe7b51e3d2a92995`가 event head와 동일함을 확인했다.
  이 quick 결과는 Hosted full 검증과 구분한다.
- 이 checkpoint는 Decimal 모델·입력·실제 소비자의 관련 환경 검증이다. 최근 Hosted full은 아래 Duration source이며
  이 결과를 전체 플랫폼 검증으로 표시하지 않는다.

## GDJ-0090 — Float 모델과 finite 소비자 연결

### 독립 기준

- 활성 구현의 독립 기준: [GDJ-0090](../../work/0090-floating-point-models.md), branch `feature/floating-point-models`.
- [ADR-0068](../adr/0068-binary64-field-and-finite-json-boundaries.md)는 binary64 모델과 finite Form/JSON의 경계를 구분한다.
  설계 채택·독립 reference·아래 제품 runtime 및 Hosted 검증은 각각의 source와 환경만 증명한다.
- Runner·reference test·raw JSON 세 파일의 정렬된 SHA256 manifest는 `e31576a2e07038176618272c40a6c0eba57b7474e0441f6c3f178f64ca0217ba`다.
  [Raw 관찰](../../internal/floattest/testdata/django61.json)의 SHA256은 `43b1abf4b53b8d81fb89f55d80b5325998895150a0762c770b287e56844ccfd4`다.
- 고정 Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5의 public API를 실행했다. Model 89·Form 136·serializer 360·
  실제 JSON number 16, SQLite add/reopen/update/rollback/reverse·root/relation query 각 9를 보존했다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 fresh reference test를 각각 **1 test PASS**, skip/warning/exception 0으로 확인했다.
  Python과 SQLite source fingerprint는 실제 runtime과 대조하고 나머지 관찰을 모두 비교했다.
- SQLite는 NaN을 NULL로, -0을 +0으로 저장했다. DRF의 non-finite 입력은 field validation을 통과한 뒤 JSONRenderer에서 ValueError가 났다.
  해당 관찰을 정상적인 GoDj 입력·저장 성공으로 채택하지 않는다.
- 별도 **Go 1.26.5 / pgx v5.10.0 / PostgreSQL 17.5(Homebrew)** probe는 transaction의 임시 table에 finite 극값·최소 subnormal·
  ±0·NaN·±Infinity를 binary parameter로 넣었다. 반환된 float64 bits와 native float8send bits가 같았으며 NaN=NaN·NaN>Infinity·-0=0을 확인했다.
  Probe transaction은 rollback했고 임시 table은 남기지 않았다. 고정 Hosted PG나 GoDj FloatField의 실행 증거로 표시하지 않는다.


### 초기 checkpoint와 보완

첫 31-package 통합 실행은 28개 package만 성공했고 historical Float default의 materialization과 생성 소비자, Helpdesk 흐름이 실패했다.
Historical loader의 Float arm과 Helpdesk non-null create/update 분기를 보완했다. 생성 소비자를 별도로 compile해 통과한 뒤
historical 복원 수정으로 실제 SQLite/PG child 실행도 통과했다. Parent helper의 일반적인 “Go build failed” 진단을 독립 compiler 실패로 확정하지 않았다.
추가한 create 회귀의 403은 새 HTTP runtime에 이전 runtime의 CSRF signing token을 사용한 테스트 설정 문제였다.
새 서버에서 form token을 얻은 뒤 정상 인증·CSRF 경로를 실행하도록 수정했다. required 실행·입력 거부·transaction 경계는 유지했다.

### 로컬 통합 checkpoint

실행 source는 Markdown을 제외한 제품·생성물·소비자 **117개** 변경 파일이다. 정렬된 `<sha256>  <relative-path>\n` manifest의 SHA256은
`231929f81df3256c172b7b92a0bfb841dc31ff7dd523fde6826d821fd87e42e4`다. 일반·race 실행 전후 이 바이트가 동일함을 확인했다. 제품 commit은 `784dbf644f71c2d3507371c2afcc117d0f746ffb`다.
별도로 CI workflow와 relation required roster 두 파일에 Float·Duration 생성 소비자 sentinel을 추가했다. 제품 동작의 수정이 아니며 CI tooling으로 검증했다.

환경은 **Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5(Homebrew)**, 작업 전용 DB와 각 테스트의 독립 schema다.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`, `go test -count=1 -json -timeout=20m`을 적용했다.

```text
./admin ./api ./api/openapi ./api/openapi/consumertest ./clock ./codegen ./codegen/consumertest
./conformance/projectoperatorproduct/attestation ./conformance/systemstate/attestation
./db/internal/queryplan ./db/postgres ./db/sqlite ./duration ./examples/article/apiapp ./examples/helpdesk
./forms ./forms/model ./internal/compiletest ./internal/floatvalue ./internal/irresource
./internal/migrationautodetect ./internal/projectgenerate ./internal/projectspec ./internal/projectwire
./migrations ./migrations/definition ./orm ./query ./schema ./schema/ir ./serializers
```

- 일반 **31 packages / 9,352 test·subtest PASS**, 같은 범위 `-race` **31 packages / 9,287 test·subtest PASS**다. Package terminal은 별도 집계했다.
  전체 event의 run/terminal·필수 부모·package completion, stderr 0 bytes를 확인했다.
- 차이 **65 events**는 `internal/compiletest`의 `!race` 파일에 속한 7 root와 subcase다. Float-vs-float32 predicate·integer write·integer F
  거부 3개는 일반 실행에서 완료했다. Race 실행으로 합산하지 않았다.
- 각 실행의 helper-only skip 2개는 `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`다.
  해당 cross-process/recovery 부모 테스트와 generated Float/Duration·Helpdesk 양 DB·독립 client 부모가 모두 PASS다.
- 독립 model raw 89개 중 문자열 변환 **67개**를 공통 parser와 직접 대조한다. Form **136개**는 cleaned/errors와 초기 null/±0/1.5의 changed를 비교한다.
  Serializer **360개**는 직접 field 332·finite Decimal token projection 4·typed nonfinite constructor 거부 12·non-JSON Decimal 거부 8·global NUL 거부 4로 구분했다.
  Direct field 중 nonfinite 52개와 별도 JSON number 16개 중 exponent overflow 2개는 [DEV-0015](../DEVIATIONS.md#dev-0015--float-non-finite-입력을-저장json-rendering-전에-거부)의 명시적 선제 거부다.
- Generated model의 binary64 default·±0/canonical NaN·nullable·add/reopen/reverse, typed/dynamic root/relation 각 9개, F/IN·projection·Min/Max,
  관계 cache/Unwrap 복사와 실패 Save·선택 필드·취소를 검증했다. 별도 module의 SQLite/PG child terminal을 요구한다.
  SQLite NaN query/write 거부·기존 행 보존과 ±Infinity, PG NaN equality/order/aggregate와 rollback을 실제 DB에서 확인했다.
- Helpdesk `0011_ticket_effort`를 실제 migration·로그인·Admin·JSON create/PUT/PATCH에 연결했다. Non-null create, 숫자 rounding·subnormal·극값,
  ±0 no-op·생략/null·빈 form·HTML escaping·invalid-before-transaction·rollback·fresh reopen을 확인했다.
  ORM으로 저장한 ±Infinity의 API list/detail·Admin 읽기는 500이며 NULL로 출력하거나 원본을 바꾸지 않는다.
- 실제 OpenAPI 문서 세 개를 다시 export하고 고정 **ogen v1.24.0**으로 생성했다. Article 생성물과 module/tool lock은 그대로이며 Helpdesk만 갱신했다.
  별도 module의 **28개 필수 receipt**, 실제 HTTP의 non-null create·Float update, 최종 DB 상태와 독립 wire의 정밀도·signed zero·required nullable·overflow를 검증했다.
  Request.Validate 명시 호출과 response decoder를 구분하며 자동 request validation을 주장하지 않는다.
- Affected vet·generated drift, Helpdesk migration `candidate_count:0`, CI tooling **37 tests**, docs/format·diff 검사를 통과했다.
  고정 openapi-spec-validator **0.9.0** / jsonschema **4.26.0** / referencing **0.37.0**의 문서 세 개 검증과 generated model/project의
  **Linux/386/CGO0 cross-compile**도 PASS다. 386 runtime 증거는 아니다. 앞선 전체 **149-package compile-only**는 runtime PASS와 구분한다.
- `CGO_ENABLED=0`의 generated Float·독립 client·Helpdesk SQLite/PG focused checkpoint는 **3 packages / 4 root tests PASS, skip 0**다.
  각 root의 terminal과 stderr 0 bytes, 같은 제품 117개 파일의 바이트를 다시 확인했다.
- 모든 로컬 실행 종료 뒤 작업 전용 DB의 연결 0개를 확인하고 삭제했다. 기존 PostgreSQL service는 유지했다.

### Hosted 검증 소유권

Float의 관련 통합은 Hosted **orm** scope로 실행한다. Portable Go·relation·targeted command·PostgreSQL owner가 선택된 OS/architecture/mode를 담당한다.
source `784dbf644f71c2d3507371c2afcc117d0f746ffb`, attempt 1의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35484302381)은 terminal success다.
Run API와 attempt 1 jobs API의 전체 **48개** 고유 id/name, source·attempt·terminal을 대조했다. **44개 success**, scope에서 제외한 **4개 skipped**이며 실패·취소는 없다.
제외 항목은 Python compatibility·exact Darwin·product project-check·conformance reference/capture owner다. 테스트 실행 성공으로 합산하지 않는다.
최종 result log는 `scope:orm`, `full_platform_verified:false`와 `command-product-matrix`, `portable-go-matrix`, `postgresql-product`,
`relation-product-matrix` 네 owner 완료를 확인했다. 선택한 Linux/macOS amd64·arm64·normal/race/CGO0와 고정 PostgreSQL 17.10 제품의 관련 회귀다.
후속 `0930779`는 실행 링크를 기록한 Markdown이며, `8855902`는 별도로 검증한 Decimal reference 준비다. 어느 것도 위 Hosted source에 포함된 것으로 표시하지 않는다.
최근 full source `79637ef3f5943c9490027723527fb5074b01411f`는 Duration까지이며 Float를 포함하지 않는다.

## GDJ-0089 — Duration의 모델·DB 범위와 소비자 연결

- 완료 작업: [GDJ-0089](../../work/0089-duration-models.md), branch `feature/duration-models`, baseline `1e04a854f1d9439e87d62e274ece93901684d888`.
- Duration 값·IR/default·typed/dynamic AST·generator·SQLite BIGINT/PG INTERVAL·Form/Admin·Helpdesk elapsed·OpenAPI/client를 연결했다.
  [ADR-0067](../adr/0067-duration-model-range-and-number-input.md)은 모델 범위·저장 한도·exact JSON number와 pinned numeric coercion을 구분한다.
- 로컬 runtime 검증 source는 Markdown 제외 **127개** 변경 파일이다. 정렬된 `<sha256>  <relative-path>\n` manifest의 SHA256은
  `a9a368834092ca313abbcf35063588c7774806f86787f2bbe1b3d58e6ccaad75`다. 아래 실행 전후 같은 파일 바이트를 확인했다.
  로컬 runtime 검증의 제품 commit은 `f06bc7a01060b014a129631f60f5d677978adcef`다.
  이후 schema/query/ORM의 GoDoc 주석 세 곳만 바로잡았고 Go scanner의 non-comment token 열이 동일함을 대조했다.
  이 주석 수정은 로컬 runtime 실행 source와 구분하며 Hosted는 실제 후속 commit에서 실행한다.

### 독립 기준과 초기 보완

Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3 UTC/en-us의 실제 public API와 file SQLite를 사용했다.
[Raw 관찰](../../internal/durationtest/testdata/django61.json)의 SHA256은
`e5cda610c3b48c31c2c9e788db77acaa5954d31cce61149b7c9913017596580e`다.
Model **67**·Form **106**·serializer **272**·실제 JSON number **15**, DB add/reopen/update/reverse·root/relation query 각 **9**·Min/Max를 보존했다.
SQLite signed int64 microsecond 양 끝과 한계를 1 넘는 write의 OverflowError·기존 행 보존도 실제 관찰했다.
숫자 전용 관찰 추가 전 raw는 `5c9eca13f8632e3f2ea79a8d0a017c5905a432da8b75073730ff4fb6cb969dba`이며 현재 reference로 표시하지 않는다.

위 최종 reference를 **Python 3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**의 fresh subprocess에서 각각 재생했다.
각 **1 test PASS**, skip/warning/exception 0이며 Python/SQLite fingerprint와 전체 관찰을 대조했다.
Go Form은 **106**개의 cleaned/errors/changed(초기 null/zero/소수초)를, serializer는 **268**개의 field 결과와 JSON number **15**개를 직접 대조한다.
NUL **4**개는 기존 global JSON `invalid_document` 경계를 별도로 assert하며 DRF field parity나 skip으로 합치지 않는다.

초기 focused 실행에서 공통 DB value-kind·migration loader의 Duration 등록, 생성 relation의 configuration error 전달 누락을 확인했다.
또한 별도 consumer fixture의 잘못된 함수 이름, Helpdesk의 embedded migration 목록과 Admin 입력 rendering 누락을 보완했다.
Required 실행이나 비교 기준을 제거하지 않았으며 아래 최종 source의 통합 실행을 새로 수행했다.

### 로컬 통합 checkpoint 완료

환경은 **Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5(Homebrew)**다. 작업 전용 DB·독립 schema를 사용하고
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다. 실행 종료 후 전용 DB의 연결 0개를 확인하고 삭제했으며 기존 service는 유지했다.

- Affected 일반 **30 packages / 8,866 pass events**, 동일 범위 race **30 packages / 8,804 pass events**가 terminal PASS다.
  Time 작업의 27개 범위에 Duration, API parser와 Article API를 더해 JSON number 변경의 기존 소비자도 검사했다.
- 차이 **62 events**는 `internal/compiletest`의 명시적 `!race` source에 속한 **7 root**와 subcase다. Test event 집합과 각 source build tag를 대조했다.
  Duration-vs-표준 time.Duration/clock.Time/calendar.Date의 predicate/write/F 컴파일 거부 3개를 일반 실행에서 확인했다.
- 양 모드의 helper-only skip 2개는 `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`다.
  실제 parent `TestPostgresRevisionFenceCrossProcessIntegration`, `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 모두 PASS다.
- CGO=0 focused 검증은 **4 packages / 5 root tests PASS, skip 0**이다. Generated Duration consumer·독립 OpenAPI client·Helpdesk SQLite/PG·
  PostgreSQL 전체 모델 범위와 외부 interval 거부를 정확한 selector로 실행했다.
- Generated model은 zero/NULL/default, 음수·microsecond·int64 양 끝, 기존 행 추가·reopen·reverse, typed/dynamic/F/IN·projection·Min/Max,
  forward/eager 관계와 cache/Unwrap 복사, 실패 Save·선택 필드·취소를 확인했다. 별도 module의 SQLite/PG child terminal receipts를 요구한다.
  SQLite 한계를 넘는 유효 모델 값은 거부하고 PostgreSQL은 저장·조회하며 probe transaction rollback 뒤 기존 행을 보존한다.
- PostgreSQL은 전체 모델의 ±999999999일 경계 저장·정렬·fresh reopen·transaction query를 확인했다. Native month/infinity/모델 범위 밖 값은 오류이고,
  nullable scanner의 이전 Valid도 해제한다. 오염된 pooled IntervalStyle은 물리 session 검증에서 교체된다.
- Helpdesk는 실제 로그인/Admin·HTML escaping·CSRF/permission·validation-before-transaction, numeric 입력, canonical no-op PATCH·PUT 생략/null,
  rollback·fresh reopen을 확인했다. `0010_ticket_elapsed` 이전 migration 파일은 변경하지 않았다.
- Actual OpenAPI 문서 3개와 고정 **ogen v1.24.0** 생성물을 갱신하고 별도 module의 **26개 필수 receipt**·HTTP·최종 DB를 검사했다.
  Module/tool lock은 유지했고 response domain validation과 명시적 request.Validate를 구분한다.
- Affected vet, generated drift, Helpdesk migration `candidate_count:0`, **Linux 386 / CGO=0** generated model/project build PASS.
  전체 **147 packages compile-only**도 완료했으며 이 결과를 전체 runtime PASS로 표시하지 않는다. CI Python tooling **37 tests PASS**.
- Pinned openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**으로 실제 문서 세 개를 검증했다. 모두 PASS다.

### Hosted 통합 milestone

Date·Time·Duration과 JSON number 기반의 통합 milestone으로 Hosted **full**을 선택했다. 기존 Draft PR #1에 통합한
source `7e338bf28d27d12516d6732e7ae5f38f7b19bda5`, attempt 1에서 [Hosted full](https://github.com/progresshans/godj/actions/runs/35478903468)을 실행했다.
로컬 runtime source `f06bc7a01060b014a129631f60f5d677978adcef`와의 차이는 위 GoDoc 주석·Markdown이다.
첫 실행의 Python compatibility 네 job은 각각 `PYTHON_SUITE_VERIFIED tests=293 skips=4`를 출력했으나 전체 scenario digest의 byte 수 검사에서 실패했다.
기대 `1,081,058` bytes와 실제 `1,081,069` bytes의 차이는 `b7269d9`의 순환 migration 구현 때 이미 갱신한
`godj.migration.writer.unsupported_delta_fail_closed` 하나다. 해당 관찰의 `self_or_cyclic_relation / relation_cycle`이
`required_field_without_backfill / unsupported_delta`로 바뀌었으나 workflow의 고정 기준은 갱신하지 않았다.
311개 관찰을 생성하고 이 시나리오만 이전 source의 함수로 치환했을 때 기존 byte 수와
SHA256 `b8d53e874169009fcd4650c79f2a007e18307d2fddd07a07d970f28bce2ed3f5`가 정확히 재현됐다.
현재 시나리오의 byte 수는 3,273, 이전은 3,262이며 구조화된 결과의 차이는 위 case/code 두 값뿐이다.

Workflow의 예상 byte 수와 digest 두 값만 수정했다. 수정된 workflow의 inline Python을 그대로 추출해
고정 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 각각 **311 scenarios / 1,081,069 bytes /
SHA256 `92bd2eb410e09ca3d046b0ca048c723376c3bfa55a9eb82f25276adc88be3450` PASS**를 확인했다.
각 runtime와 Django/DRF/asgiref/sqlparse version을 실제 실행에서 assert했고 CI tooling **37 tests PASS**, diff 검사도 완료했다.
첫 Hosted 실행은 terminal `cancelled`이며 62개 job 중 success 46·failure 5(Python 네 개와 scope 집계)·cancelled 11이다.
실패 원인을 보존하고 수정 source의 full로 대체한다. 다른 job의 중간 성공이나 이전 Time ORM·Text/DateTime full을
현재 source의 Hosted PASS로 표시하지 않는다.

대체 실행은 source `79637ef3f5943c9490027723527fb5074b01411f`, attempt 1의
[Hosted full](https://github.com/progresshans/godj/actions/runs/35479740366)이다. 첫 실행 source와의 차이는 위 CI 기준 두 값과 Markdown이다.
대체 실행은 **terminal success**, **62개 unique job 전부 success**, cancelled/skipped job 0이다.
Run API의 source/attempt/status와 attempt 1의 전체 jobs API를 대조했고 모든 job의 head SHA와 run attempt가 일치했다.
최종 `CI result (full)`은 `full_platform_verified:true`, `scope:full`과 다음 8개 owner의 완료를 출력했다.
`command-product-matrix`, `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
`product-project-check-matrix`, `python-compatibility-matrix`, `relation-product-matrix`다.

이 결과는 Date·Time·Duration과 exact JSON number를 포함하는 **79637ef** 통합 source에 적용한다.
후속 Float 독립 reference 준비 `e5104dc`와 별도 worktree의 Float 제품 구현은 이 Hosted source에 포함되지 않는다.
Duration 작업을 완료하고 Float 모델·소비자 구현과 해당 source의 검증을 이어간다.

## GDJ-0088 — Clock Time의 모델·소비자 연결

- 완료 작업: [GDJ-0088](../../work/0088-clock-time-models.md), branch `feature/clock-time-models`.
- Time 값·IR/AST/default·generator·양 DB TIME·Form/Admin·Helpdesk `0009_ticket_service_at`·OpenAPI/client를 연결했다.
  의미와 명시적 Python/Form 경계는 [ADR-0066](../adr/0066-clock-time-field-and-precision-boundaries.md)에 있다.
- 제품·검증 source: `9f0ffa8aeea143dd0da48789761235004dd4fa59`. 기존 Draft PR #1에 통합하고 push했다.
- 로컬 affected checkpoint를 완료했다. 제품·테스트·생성물·reference를 포함한 Markdown 제외 126개 파일의 정렬된
  `<sha256>  <relative-path>\n` manifest SHA256은 `2585525d6bf20dbb23a15d0b35f1868341f15eeb69847c44e3e8b55b85e20845`다.
  아래 최종 실행 중 이 source를 보존했다.

### 독립 기준과 초기 통합

고정 Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3 UTC/en-us의 실제 public API와 file SQLite를 사용했다.
Raw 관찰은 model 85개·기본 Form 152개·microsecond Form 152개·serializer 344개, 실제 DB add/reopen/update/reverse·root/relation query 각 9개·Min/Max다.
[Raw 파일](../../internal/clocktimetest/testdata/django61.json)의 SHA256은
`0d57d17153ef6060a5088b807ca706ac1c5eb4ba5e2792acc6ced933eab106c0`다.
Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 fresh subprocess의 pinned reference 재생을 각각 실행해 **1 test PASS**, skip/warning/exception 0을 확인했다.
Python/SQLite fingerprint는 실행 runtime과 대조하며 구버전의 model 11개·serializer 44개 차이는 explicit profile로 검사한다.

첫 27-package 통합 실행은 3 package가 실패했다. 기본 Django TimeInput이 초기 소수초를 제거해 Go의 precision 보존과 changed 결과
10개가 달랐다. 기본 관찰을 보존한 채 supports_microseconds=true 위젯의 별도 실제 관찰을 추가하고 Go를 후자와 비교했다.
이 precision 보존 결정은 [DEV-0014](../DEVIATIONS.md#dev-0014--timeinput의-초기-microsecond와-변경-감지를-보존)의 정확한 selector로 제한한다.
또한 Helpdesk registry fixture의 필드 수를 새 allowlist에 맞추고, 독립 client test에서 request encoder가 Validate를 자동 호출한다는
잘못된 가정을 제거해 명시적 Validate와 서버 검증을 구분했다. 제품의 소수초·JSON 보안·권한·transaction 경계를 약화하지 않았다.
수정 후 Form·Helpdesk SQLite/PG·독립 OpenAPI client·생성 Clock model의 focused **4 packages / 160 pass events**, skip 0을 확인했다.
그 뒤 추가한 import-name collision fixture와 Time config 오류 코드 정리를 포함해 다음 checkpoint를 실행했다.

### 로컬 통합 checkpoint 완료

환경은 **Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5(Homebrew)**다. 작업 전용 DB와 독립 schema를 사용하고
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다. 전용 DB는 모든 실행이 끝난 후 연결 0개를 확인하고 삭제했으며 기존 DB service는 유지했다.

- Affected 일반 실행 **27 packages / 8,390 pass events**, 같은 범위 race **27 packages / 8,331 pass events**가 terminal PASS다.
  Clock, schema/IR, query/ORM, codegen/consumer, migrations/definition, project wire/spec/generation/resource/autodetect,
  Form/model, serializer, Admin/OpenAPI/client, 양 DB/queryplan, compile boundary, Helpdesk와 두 source attestation package를 포함한다.
- 차이 **59 events**는 `internal/compiletest`의 명시적 `!race` source에 있는 7 root test와 subcase다. 일반 실행의 terminal event와
  각 source build tag를 대조했다. 새 Time-vs-Date/DateTime predicate/write/F 컴파일 거부 6개가 일반 실행에 포함된다.
- 양쪽 helper-only skip 2개는 `TestPostgresRevisionFenceHelperProcess`, `TestPublicationCrashHelper`다. 실제 process parent인
  `TestPostgresRevisionFenceCrossProcessIntegration`, `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 normal/race 모두 PASS다.
- CGO=0 focused 실행은 **3 packages / 4 root tests PASS, skip 0**이다. Generated Clock consumer, Helpdesk SQLite/PG,
  독립 OpenAPI client를 정확한 root selector로 실행했다.
- Generated consumer는 자정 default·NULL·microsecond·invalid mutation/동적 입력·원본/cache/projection 복사·F·empty aggregate·
  취소·실패 Save와 update mask·기존 행의 추가/재연결/역방향·root/forward relation 관찰을 양 DB에서 확인했다.
  SQLite/PG child의 terminal PASS를 필수 receipt로 검사했다. `Time` model과 `Time`/`TimeValue` 필드 이름도 import와 충돌하지 않는다.
- Go Form은 microsecond를 보존하는 독립 관찰 **152개**의 cleaned/errors/changed를 대조한다. Serializer **328개**는 직접 field 결과와 대조하고,
  NUL **12개**는 GoDj JSON parser의 invalid_document 경계를 별도로 assert한다. Python typed aware time **4개**는 raw 관찰을 확인하고
  대응하는 zone-free Go 값이 없다는 경계로 구분했다. 344개 전체를 동일한 field parity로 표시하지 않는다.
- Helpdesk는 실제 로그인/Admin·CSRF/permission·validation-before-transaction·canonical no-op PATCH·PUT omission/null·rollback·fresh reopen을 확인한다.
  Ogen v1.24.0은 실제 builtin Session/CSRF 문서에서 재생성했고 독립 module의 compile/HTTP/DB·24개 required receipts,
  canonical clock wire·required response/domain validation과 명시적 request.Validate를 확인했다. Module/tool lock은 바꾸지 않았다.
- Affected vet와 `make generate-check` PASS, Helpdesk `makemigrations`는 **candidate_count:0**이다.
  `go test -exec /usr/bin/true ./...`는 **144 packages compile-only**이며 runtime PASS가 아니다.
  Generated Helpdesk model/project의 **GOOS=linux GOARCH=386 CGO_ENABLED=0 go build**도 PASS다.
- CI Python tooling **37 tests PASS**. 별도 pinned openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**으로
  실제 Article Bearer/Session·Helpdesk Session 문서 세 개를 검증했고 모두 PASS다. 문서 링크·diff·format도 검사했다.

현재 로컬 결과는 위 source·환경·범위에 적용한다. Hosted ORM과 전체 platform/cold-build 증거를 대신하지 않는다.

### Hosted ORM 완료

Source `9f0ffa8aeea143dd0da48789761235004dd4fa59`, attempt 1의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35475136652)이 terminal **success**다.
Run의 head SHA·attempt와 전체 **48개 고유 job / success 44 / scope skip 4**를 확인했다. 필수 job의 실패·누락은 없다.
최종 `CI result (orm)`의 실제 출력은 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

Portable Go, Linux/macOS의 command·relation 제품 normal/race/CGO0, PostgreSQL 17.10 실제 제품을 포함한다.
Python compatibility, exact darwin/arm64 profile, product project check, references/current captures의 네 job은 ORM 범위 밖으로 skip했다.
고정 Python reference의 별도 로컬 결과는 위와 같다. 이 ORM success를 새 전체 platform/cold-build 완료로 확장하지 않는다.
후속 Markdown만 바뀐 commit은 제품 source를 바꾸지 않으며, Duration 작업 사본의 준비 코드는 이 실행에 포함하지 않는다.

## GDJ-0087 — Calendar Date의 모델·소비자 연결

- 작업: [GDJ-0087](../../work/0087-calendar-date-models.md), branch `feature/calendar-date-models`, integration baseline
  `13937986914317d365f580f2adead277d73e2ce1`.
- 제품·로컬 검증 source: `b2b01f80f9a8b04c1893f8dc5d24e9b19ba4b087`. 기존 Draft PR #1에 fast-forward로 통합하고 push했다.
- Markdown 제외 109개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `1a410b39897b27aa75016a1e22b5ac3e5bff6dafd6f698131e7b4f1696498289`다. 아래 실행 중 제품·생성물·reference·test bytes를 유지했다.
- `calendar.Date`·IR/default/strict wire·typed/dynamic AST·generator·양 DB DATE·Form/Admin·Helpdesk 방문 예정일
  `0008_ticket_service_on`·PUT/PATCH·OpenAPI/독립 client를 연결했다. 설계는 [ADR-0065](../adr/0065-calendar-date-field-and-input-boundaries.md)다.

### 독립 기준

Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3, UTC/en-us의
[독립 runner](../../conformance/runners/django/calendar_date_reference.py)를 실제 public API와 file SQLite로 실행했다.
[Raw 관찰](../../internal/calendardatetest/testdata/django61.json)의 SHA256은
`4b3560af943bec1bacc4799ace4ce8c2d6b4053ebc09fe9ba84c878e883071fd`다.
Model 67개·Form 120개·serializer 272개, 날짜 추가 전 기존 행·재접속·갱신·역방향, root query 9개·forward relation 9개·Min/Max를 보존했다.
모델의 datetime coercion은 Python 참조 관찰이며 Go 모델의 지원 기능으로 세지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with djangorestframework==3.18.0
--with asgiref==3.12.1 --with sqlparse==0.5.5 python -W error -m unittest
conformance.runners.django.tests.test_calendar_date_reference`를 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 각각 실행했다.
각 **1 test PASS, skip·warning·exception 0**이다. Fresh process의 모든 관찰을 대조하고 Python/SQLite fingerprint는 실행 runtime과 비교한다.

Go Form은 120개 cleaned/error/changed 관찰, serializer는 268개 field 결과를 대조한다. NUL 4개는 기존 GoDj JSON parser의
`invalid_document` 거부를 별도로 assert한다. DRF field 오류 코드와 같다고 세거나 필수 실행에서 skip하지 않는다.

### 로컬 통합 checkpoint

환경은 Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)다. 이 작업의 전용 DB와 각 테스트의 독립 schema를
사용했고 `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 적용했다. 임의로 UTC process 환경에 의존하지 않는지도 확인한다.

- `go test -count=1 -json`의 affected **21 packages / 7,095 pass events**. Calendar, schema/ir, query/orm, codegen/consumer,
  migration definition, project wire, autodetect, Form/model, serializer, Admin/OpenAPI/client, SQLite/PG/queryplan, compile boundary,
  Helpdesk가 포함된다. Helper-only skip 1개는 `TestPostgresRevisionFenceHelperProcess`이며 이를 호출하는 실제
  `TestPostgresRevisionFenceCrossProcessIntegration`은 PASS다.
- 같은 범위의 `-race`: **21 packages / 7,042 pass events**, 같은 helper-only skip 1개. 일반 실행과의 53 event 차이는
  `internal/compiletest`의 명시적인 `!race` source에 있는 7 root test와 subcase다. 양쪽의 terminal event 집합과 build tag를 대조했다.
- `CGO_ENABLED=0` focused 실행: **3 packages / 4 root tests PASS, skip 0**. 생성 Date consumer, 독립 OpenAPI client,
  Helpdesk SQLite·PostgreSQL을 포함한다. 처음 selection에 없던 Helpdesk SQLite는 정확한 root test 이름으로 별도 실행했다.
- 새 generated Date consumer는 root/nullable default·invalid Create/Patch·기존 행 추가·fresh reopen·root/relation query와 independent
  DB 관찰·same-model F·projection/Min/Max·cache/Unwrap 복사·Save 선택 필드·실패 보존·취소·역방향을 양 DB에서 실행한다.
  Parent는 SQLite child와 PG URL이 주어진 경우 PostgreSQL child의 terminal PASS를 필수로 요구한다.
- Helpdesk는 실제 인증·Admin·CSRF/permission 경로, 날짜 validation-before-transaction, canonical no-op PATCH·PUT omission/null,
  실제 변경 뒤 강제 rollback과 새 연결의 저장 값을 검사한다. 기존 Boolean/DateTime/choices 동작도 함께 실행했다.
- Ogen v1.24.0 client를 실제 builtin Session/CSRF의 OpenAPI 문서에서 다시 생성했다. 별도 module의 lock은 보존했고,
  실제 HTTP·DB와 독립 transport의 required date response 오류·canonical wire·null/생략·연도 경계를 검사했다.
- `go test -exec /usr/bin/true ./...`: **141 packages compile-only**. 이 명령은 테스트 본문을 실행한 PASS가 아니다.
  Generated Date model/project의 `GOOS=linux GOARCH=386 CGO_ENABLED=0 go build`도 PASS다.
- affected `go vet`: PASS. `make generate-check`의 모든 checked-in generated source가 clean이고,
  Helpdesk `makemigrations` 재실행은 `candidate_count:0`이다. CI Python tooling **37 tests PASS**.

첫 checkpoint의 Date relation 생성 consumer는 `WithConfigurationError` 누락으로 compile에 실패했다. 메서드를 연결한 뒤 다시 실행했다.
같은 checkpoint의 serializer NUL 4개는 테스트가 global JSON 거부 전에 field binding을 기대해서 실패했다. 보안 규칙을 바꾸지 않고
reference 대조와 document-boundary assertion을 구분했다. 수정 후 focused 실행과 위 일반 checkpoint가 통과했다.

### Hosted에서 발견한 dependency closure와 guard 보완

첫 [Hosted ORM 실행](https://github.com/progresshans/godj/actions/runs/35471559786)은 source `b2b01f80f9a8b04c1893f8dc5d24e9b19ba4b087`다.
Portable normal/CGO0 integration의 namespace/publication-recovery test가 격리 module에 새 `calendar` dependency를 연결하지 않아,
의도한 recovery 단계 전에 readonly compile이 실패했다. 격리 fixture의 dependency 목록에 calendar를 추가했다. Candidate compile나
namespace/recovery 조건은 완화하지 않았다. 같은 run의 macOS Intel race 작업 하나는 `raw.githubusercontent.com`와 `go.dev`의
DNS ENOTFOUND로 Go 도구 준비 전에 실패했다. 이 환경 실패를 제품 PASS나 제품 원인으로 세지 않는다. 첫 run은 **cancelled**로 종료했다.

새 scalar가 통과하는 다른 경계도 확인해 `internal/irresource`, project spec, loaded migration의 Date default/choice payload를
문자열·aggregate 바이트 한도에 포함했다. System-state/operator source binding은 `calendar`, date input과 기존 temporal/Boolean
input helper를 포함하도록 보완했다. 해당 파일이 바뀌면 실행 증거의 source binding이 달라지는 negative control을 추가했다.

Guard/fixture 보완 source는 `8aa3c477e9ef5cfa733d0a2dea1d33c6d402d3b0`이며 별도 **11 files**다. 정렬된 파일 manifest SHA256은
`bce9fbb16b7df441b607c9661536bb649b02685fe5aa5c62d7f52c4f83399d06`이다. 이 추가 source에서:

- Namespace/publication-recovery의 실제 실패 test는 normal/race/CGO0 각각 **1 package / 3 pass events**, skip 0으로 확인했다.
- Project generation·project spec·IR resource·migration·두 attestation package의 전체 affected normal/race는 각각
  **6 packages / 775 pass events**다. Helper-only skip 1개는 `TestPublicationCrashHelper`이며 실제 crash parent
  `TestPublishRecoversAfterProcessCrashAtPrecommitAndPostcommitBoundaries`는 양 모드에서 PASS다.
- Date generated consumer·Helpdesk SQLite/PG·독립 client를 재실행했다. Normal/race/CGO0 각각 **3 packages / 4 root tests PASS**, skip 0이다.
  PG required 환경을 적용했고 이 재검증에 만든 두 번째 전용 DB도 실행 후 삭제했다.
- 추가 6 package의 vet와 문서·diff 검사도 통과했다. 앞의 21 package 결과는 원래 b2b01f8 source의 결과이며 이 보완을 포함한
  모든 패키지를 다시 실행했다고 표시하지 않는다.

Loaded migration의 oversized Date 테스트는 최초에 개별 payload path를 기대해 실패했다. 기존 오류 우선순위에서는 enclosing
`definition_bytes`가 먼저 반환된다. 그 우선순위를 유지하고 실제 resource 거부를 assert하도록 테스트를 수정한 뒤 위 checkpoint가 통과했다.

### Hosted ORM 완료

[Hosted ORM](https://github.com/progresshans/godj/actions/runs/35472148411)은 보완 source
`8aa3c477e9ef5cfa733d0a2dea1d33c6d402d3b0`, attempt 1에서 terminal **success**로 종료했다.
48개 unique job의 run ID·attempt·head SHA·terminal 상태를 대조했고 **44 success / 4 scope skip**을 확인했다.
Command product·portable Go·PostgreSQL product·relation product matrix가 검증 범위다. 최초 실패했던 portable normal/CGO0
integration과 macOS relation race도 이번 source에서 성공했다.

Skip 4개는 exact darwin/arm64 profile·product project check matrix·Python compatibility matrix·reference/product capture gate다.
필수 ORM 작업을 생략한 결과가 아니다. 최종 summary는 다음 scope를 명시한다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

검증 source 이후의 Date 통합 변경은 상태 문서뿐이다. 위 로컬 Python reference 재생과 Hosted ORM은 각자의 실행 source와
범위에 적용하며, 전체 platform/cold-build 검증이나 전체 프레임워크 완성을 뜻하지 않는다. GDJ-0087의 구현·검증은 완료했고
다음 기능은 [GDJ-0088 clock Time](../../work/0088-clock-time-models.md)이다.

## GDJ-0086 — Nullable Boolean의 모델·Form/Admin/API 연결

- 작업: [GDJ-0086](../../work/0086-nullable-boolean-models.md), baseline `6d3d42bd4023b9bc8bbe4588fd5645a949b4df9f`,
  구현 branch `feature/nullable-boolean-models`.
- Markdown을 제외한 69개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `ff7c94220331f4a80f09bdcd12c5a2e5b7fd0ef803c78ed08fbf501819650f52`다.
- 제품·생성물은 일반/race/CGO0에서 같다. 넓은 일반·race 실행 뒤 추가한 관계 NOT/IN reference와 test helper,
  필수 child receipt 및 CI 목록은 최종 focused 실행과 정적 CI 도구 검사로 확인했다. 이를 기존 실행에 소급해 합산하지 않는다.

### 로컬 실행

Go 1.26.5 darwin/arm64, 전용 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.
일반/race 범위는 `./schema/... ./codegen ./codegen/consumertest ./orm ./forms/... ./admin ./examples/helpdesk
./serializers ./api/openapi/... ./db/sqlite ./db/postgres ./migrations/definition ./internal/migrationautodetect ./internal/compiletest`다.

| 범위 | 결과 |
|---|---|
| affected normal | **17 packages, 6,477 PASS events**, 직접 helper skip 1 |
| affected race | **17 packages, 6,427 PASS events**, 직접 helper skip 1 |
| focused CGO_ENABLED=0 | generated nullable Boolean·Helpdesk SQLite/PG·외부 OpenAPI client **3 packages, 4 tests PASS**, skip 0 |
| 최종 nullable Boolean Form/reference/생성 소비자 | normal/race/CGO0 각각 **2 packages, 33 PASS events**, skip 0; child의 SQLite·PG 완료를 부모가 필수 확인 |
| 전체 compile-only | **138 packages**, `go test -json -exec /usr/bin/true ./...`; 전체 runtime PASS는 아님 |
| affected vet | PASS |
| 생성 재현성 | Article·Helpdesk·relation fixture drift 없음, checked-in relation product PASS, Helpdesk makemigrations `candidate_count:0` |
| Python CI 도구 | **37 tests PASS**, skip 0 |
| 문서·format·diff | 로컬 링크 118개 문서, gofmt, `git diff --check` PASS |

모든 test 시작/종료·package terminal과 빈 stderr를 대조했다. 일반과 race의 50 event 차이는 `internal/compiletest`의
`!race`로 선언된 파일의 7개 root test와 그 하위 case다. 해당 compile 검증은 일반 실행이 소유한다.
직접 skip 하나는 기존 `TestPostgresRevisionFenceHelperProcess`이며 parent의 cross-process 검사가 실제 child를 실행했다.
Helper skip이나 nested Go event 수를 별도 제품 기능 수로 세지 않는다.

생성 소비자는 실제 serialized migration의 기존 행 추가·역방향·새 연결, default nil/false/true·명시적 null/false,
typed/dynamic root 및 nullable forward 관계의 exact/isnull/IN/NOT, scalar projection·query cache와 eager Unwrap 복사를 비교한다.
Related facade 객체의 pointer identity는 보존하고 caller에게 복사한 raw snapshot을 검증한다. Save mask·취소·실패 입력도 포함한다.
Helpdesk는 기존 0001 행을 0007까지 성장시키고 Form/Admin의 초기값·재검증·저장, PUT/PATCH 생략·default·명시적 null/false,
권한·CSRF·category 범위·실패 transaction rollback과 재접속을 실제 SQLite/PG에서 실행한다.
외부 ogen client는 실제 API 문서 byte 일치·offline 재생성·독립 compile·HTTP·최종 DB와 **20개 필수 receipt**를 확인한다.

초기 실행의 Admin null 거부/재검증 coercion을 수정했다. 초기 PostgreSQL URL의 host 누락은 테스트 환경 설정을 바로잡았다.
생성 소비자가 facade identity를 raw snapshot 복사로 오해한 assertion도 수정했다. 이 초기 실패들은 최종 PASS가 아니다.

### 독립 기준

Django 6.1 / DRF 3.18.0 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.3의
[독립 runner](../../conformance/runners/django/nullable_boolean_reference.py)가 public model·Form/widget·serializer·schema editor/ORM을 실행한다.
[Raw 관찰](../../internal/nullablebooleantest/testdata/django61.json)의 SHA256은
`ce9b6c827c8de2d449c4cba0f245592fa8969667f895d77879798d853d01ff8e`다.
Required/optional widget 30개 입력 관찰, direct field와 JSON serializer의 별도 coercion·생략/default/partial,
기존 table 추가·재접속·update·remove 및 root/nullable 관계 각각 6개 query를 보존한다. Python의 넓은 coercion을 Go JSON에 채택하지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with djangorestframework==3.18.0 --with asgiref==3.12.1
--with sqlparse==0.5.5 python -W error::ResourceWarning -m unittest conformance.runners.django.tests.test_nullable_boolean_reference`는
Python **3.12.13·3.13.15·3.14.3·3.14.7 각각 1 test PASS**, skip·warning·exception 0이다. SQLite fingerprint는 각 runtime에서
직접 읽어 비교하고 의미 관찰은 고정 raw와 대조한다. DB connection은 `closing`으로 종료해 GC 경고를 성공으로 숨기지 않는다.

### Hosted 상태

제품 source `2ea0735c7d811dd4e07862506de7643abc6073f9`의
[Hosted ORM run 35467983458](https://github.com/progresshans/godj/actions/runs/35467983458)은 attempt 1에서 완료했다.
API의 source·attempt·모든 terminal job을 대조했고 **48개 unique job 중 44 success·요청 범위 밖 skip 4개**를 확인했다.
Job 목록과 skip owner는 기존 ORM 계획과 일치하며, 이번 workflow 변경은 새 consumer를 기존 실행과 필수 receipt에 추가한 것이다.
최종 `CI result (orm)` job `105967047083`의 실제 로그는 다음과 같다.

```json
{"full_platform_verified": false, "scope": "orm", "verified_jobs": ["command-product-matrix", "portable-go-matrix", "postgresql-product", "relation-product-matrix"]}
```

새 generated consumer는 relation matrix의 SQLite 실행과 PostgreSQL product의 각 normal/race/CGO0 실행에서 필수다.
이는 해당 source의 ORM scope 결과이며 현재 source의 full이나 Date 준비 작업의 PASS가 아니다.
후속 통합 상태·완료 기록 commit은 Markdown만 바꾸며 제품 source는 같다. 기존 full의 source는 아래 기록과 CURRENT에서 구분한다.

## GDJ-0085 — Self/cyclic 자동 migration과 재개 가능한 게시

- 작업: [GDJ-0085](../../work/0085-relation-autodetection.md), 의미: [ADR-0052](../adr/0052-project-linked-deterministic-makemigrations.md),
  [ADR-0064](../adr/0064-historical-relation-graphs-and-sqlite-remakes.md).
- Baseline `7d9106bce77dfca422b38e5f3347108f57485740`의 `feature/relation-autodetection`에서 구현했다.
  Markdown을 제외한 51개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `6c0fb835c357137b957a5559e8f3d13c70e9c6623840a53ad2a75e251b6c5398`이다.
- 일반 실행과 race/CGO0의 제품·test·자동 graph fixture bytes는 같다. 일반 실행 뒤 갱신한 MIG-107 policy/artifact/provenance는
  별도 현재 writer conformance로 검증했다. 두 JSON의 기존 formatting 복원은 parsed payload가 같은지 대조했다.
  CI의 필수 receipt 추가는 CI 도구 검사에 포함했다.

### 로컬 실행

Go 1.26.5 darwin/arm64, SQLite 3.53.3과 전용 PostgreSQL 17.5(Homebrew), `GODJ_REQUIRE_POSTGRES=1`을 사용했다.
`go test -count=1 -json ./migrations/... ./internal/migrationgraph ./internal/migrationautodetect
./internal/projectmigration/... ./internal/projectcheck ./db/sqlite ./db/postgres ./conformance/migrationwriterproduct`를 실행했다.

| 범위 | 결과 |
|---|---|
| affected normal / race / CGO_ENABLED=0 | 각 **11 test packages, 5,861 PASS events**, 1 no-test package; 세 test roster 동일 |
| 현재 writer conformance | runner/checker/protocol의 `MigrationWriter\|GDJ0050` 검사 **3 packages, 20 PASS, skip 0**; 실제 MIG-099..110 12개 비교 포함 |
| 전체 compile-only | **136 packages**, `go test -json -exec /usr/bin/true ./...`; 전체 runtime PASS는 아님 |
| affected vet | PASS |
| generated drift | Article·Helpdesk·relation fixture clean, checked-in relation product 검사 PASS |
| Python CI 도구 | **37 tests PASS, skip 0** |
| 문서·format·diff | 117개 문서의 로컬 링크, gofmt와 `git diff --check` PASS |

Go JSON의 모든 test 시작/종료·package terminal, 새 graph/연결 초기화/중단 복구 필수 receipt와 빈 stderr를 대조했다.
직접 skip 둘은 기존 `TestPostgresRevisionFenceHelperProcess`, `TestMakemigrationsCrashHelper`이며 실제 child는 parent
process 검사가 실행한다. Helper skip과 Go event 수를 제품 기능 PASS 수로 세지 않는다.

별도 module의 public CLI는 세 cyclic 후보의 preview/write/hash 일치·DB-free 게시·repeat clean을 실행한다. 실제 migrate,
생성 모델의 저장·조회·명시적 순서의 First, 재접속과 잘못된 FK 저장 거부, populated reverse와 재적용·sequence를 검증한다.
동시 writer는 한 게시자와 한 clean 결과로 끝나며, 후보 세 개 각각의 partial-temp write/directory fsync 뒤 SIGKILL을 주입한
여섯 case는 이미 게시한 inode/bytes를 보존하고 동일한 나머지 후보를 재개한다.

실제 양 DB에서 same-app/cross-app 자동 정의를 load/apply/reopen/zero/reapply한다. 명시적 PK가 중간에 있는 선언과
여러 위치의 FK·integer·text를 보존하고 SQL projection, inbound/self 관계·기존 행과 sequence 상한을 검사한다.
Required FK를 미룬 중간 table에 행이 있으면 정확한 empty-table guard에서 거부하며 그 step의 column/recorder는 게시하지 않는다.
별도 column drift와 rollback·revision fence·quarantine 회귀도 유지한다.

### 독립 기준과 발견한 결함

고정 Django 6.1 / asgiref 3.12.1 / sqlparse 0.5.5를 사용한 실제 autodetector/loader/executor/schema editor/recorder 관찰을
[raw fixture](../../internal/migrationgraphtest/testdata/django-autodetect61.json)에 보존했다. SHA256은
`87487f710612204635c0b90d72b6939bb1013a23d07e8bb367acf3e482cb88fb`이며 기록 환경은 Python 3.14.7 / SQLite 3.53.1이다.
File discovery만 fixture seam이다. 두 DB의 이름별 field/constraint graph와 실제 관계 값을 대조하며 GoDj field 순서는
독립 선언 순서와 대조한다. Django historical field 순서·파일 이름·remake 후 ID 차이는 raw 그대로 보존하고
[DEV-0010/DEV-0013](../DEVIATIONS.md)에 설명한다. Exact file/field-order/ID parity를 주장하지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
python -m unittest conformance.runners.django.tests.test_migration_autodetect_reference
conformance.runners.django.tests.test_migration_writer_decisions`를 Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 실행했다.
각 **6 tests PASS, skip 0**을 확인했으며, 아래 연결 종료 수정 뒤 네 환경에서 경고·exception 없는 완료를 다시 확인했다.
Runtime fingerprint는 실행 환경과, 관찰 본문은 raw와 대조한다.
MIG-107의 Go-owned 거부 case는 독립 Python decision의 현재 실행으로만 갱신했다. 같은 writer oracle의 나머지 11개 관찰과
Django profile은 canonical bytes가 동일하며, 현재 Go-owned 다섯 decision의 재생 일치를 검사한다.

외부 재접속 소비자가 드러낸 SQLite 기본 FK OFF 결함은 per-physical-connection connector에서 ON/readback을 수행하도록 수정했다.
Pool growth·replacement·reopen, DSN의 OFF 옵션, 초기화/rows/close 오류와 취소를 검사했다. 이전 slice alias, fixture의 무정렬
First·SQLite double-quoted string fallback·Django in-memory 연결 재사용 실패는 PASS로 세지 않았다.

첫 통합 source `b7269d98c6d838fca2fda532d6d8a699dfe0a963`의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35463368487)에서
일반 artifact byte-lock 검사가 갱신되지 않은 MIG-107의 manifest/deviation/oracle size·hash와 SHA256SUMS를 검출했다.
로컬의 이름 기반 writer 검사는 이 공통 검사를 포함하지 않았으므로 Hosted 성공으로 처리하지 않는다.
독립 decision 재생과 나머지 11개 관찰 보존을 다시 확인하고 checksum catalog의 정확한 변경분만 갱신했다.
제품 코드는 바뀌지 않았다. 수정한 protocol **전체 package 904 PASS, skip 0**을 실제 실행했다.
수정 후 Markdown을 제외한 54개 파일의 manifest SHA256은
`7bd95d43b576066e32622c8880f475634cd728411c49dc3cc425f99341dad020`다. 관련 catalog 수정 외의 로컬 runtime 검증 bytes는 그대로다.

후속 로그 검토에서 Python 3.13/3.14의 종료 시 `ResourceWarning`을 발견했다. `sqlite3.Connection`의 context manager는
transaction을 종료하지만 연결을 닫지 않으므로, fingerprint 보조 연결을 `contextlib.closing`으로 명시적으로 닫았다.
종료 코드 0과 unittest OK만으로 경고 없는 완료를 판단했던 최초 Python 결과는 최종 증거로 사용하지 않는다.
같은 네 버전에서 6 tests씩 다시 실행해 stderr의 warning/exception 부재까지 확인했다. 수정한 Python test SHA256은
`839739b5a0c7ab1626502b9c5008fbc08b34071117e988066647a74530151e87`이며, Hosted ORM 대상 Go·fixture·workflow source는 변경하지 않았다.

전체 로그·source manifest·event audit는 `/tmp/godj-0085-position-path`가 가리키는 로컬 scratch에 있다.
통합 source `4320eba32a0dcb3a1e21b6244c87e32a89dad5b6`의
[Hosted ORM run 35463646580](https://github.com/progresshans/godj/actions/runs/35463646580)은 **attempt 2에서 완료**했다.
Attempt 1의 macOS-26 race command가 `go mod tidy`의 sumdb TLS handshake timeout으로 실제 제품 실행 전에 실패했고,
source 변경 없이 실패 job만 재실행했다. 최종 API에서 같은 run ID/head SHA의 48개 unique job을 대조하여
**44 success, 4 expected scope skip**과 모든 selected coordinate의 완료를 확인했다. 최종 `CI result (orm)` job은
`105956145302`이며 실제 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10의 normal/race/CGO0·core/operator-target 여섯 조합과 선택한 Linux/macOS의 관계·명령·portable 검증을 완료했다.
예를 들어 PG normal core는 12 packages·1,863 runs/pass·skip 0, Linux arm64 CGO0 relation은
27 packages·4,910 runs/pass·skip 0을 실제 job log에서 대조했다. 이후 Python fingerprint 보조 연결 종료 수정은 위 네 버전의
로컬 Python 재생으로 검증한 별도 source다. 이를 Hosted 실행 파일로 표시하지 않는다.
Full-platform·배포·전체 프레임워크 완성의 증거는 아니다.

## GDJ-0085 — 자동 relation 계획의 field insertion 기반 checkpoint

- [활성 work](../../work/0085-relation-autodetection.md)의 `feature/relation-autodetection`, baseline `7d9106bce77dfca422b38e5f3347108f57485740`에서 실행했다.
  아래는 자동 cycle 분할을 연결하기 전의 **field insertion 기반** 검사이며 GDJ-0085 전체 완료 결과가 아니다.
- Markdown을 제외한 25개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `261aad3632e9b0d83c1d5c0c3f639b7223f51ddec806a24cae7024ecb7d363a2`다. 최초 self Create/nullable self Add 탐지 변경을 포함한다.
- Go 1.26.5 darwin/arm64, 전용 PostgreSQL 17.5(Homebrew)와 SQLite를 사용했다.
  `go test -count=1 -json ./migrations/... ./internal/migrationgraph ./internal/migrationautodetect
  ./internal/projectmigration/... ./db/sqlite ./db/postgres`를 normal, `-race`, `CGO_ENABLED=0`에서 실행했다.
  각 lane **9 test packages, 5,377 PASS events**, 1 no-test package이며 세 실행의 test roster가 정확히 일치한다.
  유일한 직접 test skip은 `TestPostgresRevisionFenceHelperProcess`; 실제 helper는 기존 parent process integration이 실행한다.
  모든 test 시작/종료·package terminal과 빈 stderr를 대조했다. Go event 수는 제품 기능 수가 아니다.
- 새 실 DB 검사는 명시적 PK가 중간에 있는 self/inbound graph에 여러 FK·integer·text를 삽입한다. Serialized definition →
  SQL projection → forward/reopen/no-op → 여러 populated reverse/remake → zero/reapply를 실행하며 정확한 logical field order,
  기존 행/NULL/참조와 삭제된 ID의 sequence 상한을 보존한다. Retained column 이름 drift는 다음 schema/recorder 게시 전에 거부한다.
- 위치의 codec roundtrip/canonical digest, 잘못된 anchor/type/과대 문자열, retained field 변경·순서 변경, physical column 중복/
  ordinal/타입/constraint source 변조와 기존 rollback·contention·quarantine/process 검사를 포함한다.
  첫 실행의 slice 삽입/삭제가 borrowed Before 배열을 바꾼 실패는 PASS로 세지 않았다. 복사 경계 수정 후의 위 source만 채택했다.
- 원본 JSON·stderr·source manifest와 audit는 `/tmp/godj-0085-position-path`가 가리키는 로컬 scratch에 보관했다.
  이후 자동 candidate 분할·부분 게시 재개, Go-owned MIG-107 갱신과 CLI/생성 소비자 검증이 남았다. 새 Hosted/full-platform/배포
  결과나 기존 Draft PR에 통합된 source를 뜻하지 않는다.

## GDJ-0084 — Historical relation graph와 순환 migration

- 작업: [GDJ-0084](../../work/0084-relation-migration-graphs.md), 의미: [ADR-0064](../adr/0064-historical-relation-graphs-and-sqlite-remakes.md).
- Baseline `af49707c1530b454a06b4aefa57534a3774517f7`의 별도 `feature/relation-migration-graphs`에서 구현했다.
  Markdown을 제외한 35개 변경 파일의 정렬된 `<sha256>  <relative-path>\n` manifest SHA256은
  `c568dfdb26ab7be34978fa123f288c2c061acb5e0ac454c3407c35b968c25cb8`이다.
  Go·fixture·CI 33개는 아래 세 Go lane과 동일 bytes다. Python runner/test 두 파일의 portable runtime fingerprint 처리는
  별도 최종 4-version 재생으로 검증했다.

### 독립 관찰과 의도적 차이

고정 Django 6.1 / asgiref 3.12.1 / sqlparse 0.5.5, Python 3.14.7의 fresh process에서
[runner](../../conformance/runners/django/migration_graph_reference.py)의 실제 SQLite migration executor/loader/schema editor로
10단계의 전체 historical field/choices, 실제 행·FK와 recorder를 수집했다. File discovery만 fixture seam으로 교체했다.
[Raw fixture](../../internal/migrationgraphtest/testdata/django61.json) SHA256은
`6a6d21dead891f1e3f9c2e9967eb87bdfcc449124c54c627ef974736d767983f`다.

Self Create → 관계 두 개 Add → self Add → choices → close/reopen → self/choices 역방향 → 두 관계 역방향 → zero →
reapply → zero를 비교한다. SQLite remake 뒤 Django의 다음 ID는 3, GoDj의 다음 ID는 101이다. 기존 sequence 상한 보존을
[DEV-0013](../DEVIATIONS.md#dev-0013--sqlite-migration-remake에서-삭제된-id의-sequence-상한을-보존) 하나로 명시하며,
나머지 모든 관찰은 동일하게 대조한다. Raw oracle의 값을 바꾸거나 전체 exact parity로 표현하지 않는다.

`uv run --no-project --isolated --python <version> --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
python -m unittest conformance.runners.django.tests.test_migration_graph_reference`를 Python
**3.12.13 / 3.13.15 / 3.14.3 / 3.14.7 각각 1 test PASS, skip 0**으로 재생했다. Python fingerprint는 실행 버전과 대조하고
관찰 본문은 raw artifact와 일치해야 한다. Django PostgreSQL의 독립 reference 실행을 뜻하지 않는다.

### 로컬 실행과 source

Go 1.26.5 darwin/arm64, SQLite 3.53.3과 전용 PostgreSQL 17.5(Homebrew)를 사용했다.
실행 목록은 `./migrations/... ./internal/migrationgraph ./db/sqlite ./db/postgres ./internal/migrationautodetect ./internal/migrationgraphtest`다.

| 범위 | 결과 |
|---|---|
| affected normal / race / CGO_ENABLED=0 | 각 **7 test packages, 5,283 PASS events**, 2 no-test packages; 세 test roster 동일 |
| generated nested forward/eager 소비자 | normal/race/CGO0 각각 **2 top-level PASS**, 실제 생성 module과 strict child harness |
| 전체 compile-only | **136 packages PASS**, `go test -exec /usr/bin/true ./...`; 전체 runtime PASS는 아님 |
| affected vet | PASS |
| Python CI 도구 | **37 tests PASS, skip 0** |
| 문서·format·diff | 116개 문서의 로컬 링크, 변경 Go gofmt와 `git diff --check` PASS |

모든 Go JSON의 test 시작/종료, package terminal, 새 필수 receipt와 stderr를 대조했다. 유일한 직접 test skip은 기존
`TestPostgresRevisionFenceHelperProcess`이며 실제 child는 해당 parent process integration이 실행한다. Helper skip을
기능 PASS로 세지 않는다. Test event 수는 제품 기능 수가 아니다. Generator/생성 ABI의 변경은 없으며 기존 generated 파일에
drift가 없다. 두 소비자는 이제 수작업 DDL 대신 정확한 Schema IR을 serialized definition으로 load하여 실제 migrate한다.

### 보존·거부·실패 경계

- Self와 3-model cycle, 같은 source의 여러 Add/Remove, cross-app 같은 model 이름의 back edge를 실제 양 DB에서
  apply/unapply/reopen한다. 기존 inbound/self 참조 값, NULL, 전체 행과 sequence 상한을 보존한다.
- Direct target의 PK뿐 아니라 transitive target의 전체 physical schema를 확인한다. Target에 미기록 column을 주입하면
  다음 step의 schema/recorder successor를 남기지 않고 거부하며, 외부 drift를 제거한 fresh 호출은 성공한다.
- Transitive metadata의 누락·reserved table·caller alias와 seal 이후 변조, graph의 불연속·미래 target·reverse collision,
  깊이 2,048 cycle과 큰 field/app 이름·aggregate resource 한도를 검사한다. DB 밖의 planner/reconstructor import 경계를 유지한다.
- SQLite의 FK suspension 전후/readback, BEGIN, 첫째/둘째 copy, drop/rename, 마지막 FK 검사, recorder, COMMIT 전후,
  복원 write/read와 취소를 주입한다. 물리 discard 실패 3개를 포함한 **19개 case**에서 rollback/committed/unknown과
  row/FK·pool readback·terminal quarantine·명시적 Close 뒤 file reopen의 실제 durable 결과를 대조한다.
- 기존 contention/stale/fork/rollback/process와 recorder 회귀도 위 DB package 실행에 포함한다. 폐기한 flat-target 전용 제한
  검사는 closed graph의 양방향 실행과 whole-plan capability gate로 대체했다. Reverse ownership/resource 보장은 공통 graph와
  실제 큰 boundary fixture가 계속 검증한다.

초기 checkpoint의 기존 flat-target 기대값 실패, shared app의 중복 byte 계산, 순수 core의 backend import, Django sequence
차이와 cross-app target 선택 fixture 오류는 PASS로 세지 않았다. 최종 위 source의 완료 결과만 채택했다.
통합 source는 `d6db513aba479ebec6a5f256bebac9348a0a32ce`이며 35개 파일의 실제 bytes를 통합 사본에서도 대조했다.
[Hosted ORM run 35456370913](https://github.com/progresshans/godj/actions/runs/35456370913)은 attempt 1에서 완료했다.
48개 unique job의 run ID·head SHA·terminal 상태를 실제 API 목록과 대조했으며 **44 success, 4 expected scope skip**이다.
최종 `CI result (orm)` job `105935247654`의 실제 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10의 normal/race/CGO0·core/operator-target 여섯 조합, 선택한 Linux/macOS의 관계·명령·portable 검증을 완료했다.
제외 항목은 Python compatibility, exact Darwin profile, product project check, reference/current capture다.
전용 `godj_0084_normal` DB는 로컬 검증 후 제거했다. 이후 문서 기록 commit은 실행 source로 표시하지 않는다.
새 full/platform·Windows runtime·배포와 전체 프레임워크 완성은
이 로컬 기록의 범위가 아니다. 새 self/cyclic 선언의 자동 `makemigrations` 계획은 후속 요구다.

## GDJ-0083 — Nested eager graph와 하위 cache

- 작업: [GDJ-0083](../../work/0083-nested-forward-eager-graphs.md), 의미: [ADR-0029](../adr/0029-one-hop-forward-select-related.md#상태와-범위).
- Baseline은 GDJ-0082 통합 source `3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0`다.
  별도 `feature/nested-forward-eager`의 구현 commit은 `401d3e9fe5b1c07030d3633178b63f2f1041b305`이며,
  기존 Draft PR의 제품 통합 source는 `a49b592be1896d02d73a9657fe360623ffa296d8`다. 통합 뒤 89개 Go 검증 파일과
  CI receipt 두 파일의 hash가 검증 사본과 일치함을 확인했다. 충돌은 CURRENT·active work의 이전 진행 설명 두 곳뿐이었다.
- Python 3.14.7, Django 6.1, asgiref 3.12.1, sqlparse 0.5.5의 fresh process로
  [runner](../../conformance/runners/django/nested_eager_reference.py)의 **440개 관찰**을 수집했다.
  [fixture](../../orm/testdata/nested-eager-django61.json) SHA256:
  `eb638880bfaeffd4c51d1e2a57bb0270e12d5d57e2068efa648f26745b96a782`.
- `uv run --no-project --isolated --python 3.14.7 --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
  python -m unittest conformance.runners.django.tests.test_nested_eager_reference`: 최종 재생 **1 test PASS, skip 0**.
  이름 440개·selection 집합 8개, 모든 selected prefix의 전체 scalar 값, cold/warm Count/First/All·warm SQL 0을 대조한다.
  독립 reference는 Django SQLite이며 Django PostgreSQL 실행 증거로 확대하지 않는다.

### 로컬 실행과 source

Go 1.26.5 darwin/arm64, 실제 SQLite와 전용 PostgreSQL 17.5(Homebrew)에서 같은 43 package 목록을 실행했다.
패키지 이름 목록, 전체 JSON의 시작·종료·실패·skip과 필수 case를 대조했다. Test completion event 수는 제품 기능 수가 아니다.

| 범위 | 결과 |
|---|---|
| affected normal | **28 test packages, 6,032 PASS events**, 15 no-test packages |
| affected CGO_ENABLED=0 | **28 test packages, 6,032 PASS events**, normal과 같은 test roster |
| affected race | **28 test packages, 5,982 PASS events**, 나머지 roster는 normal과 동일 |
| 전체 compile-only | **134 packages PASS** (`go test -exec /usr/bin/true ./...`); 전체 runtime 실행을 뜻하지 않음 |
| affected vet / generated drift | PASS / Helpdesk·Article·relationfixture 세 프로젝트 PASS |
| Python CI 도구 | **37 tests PASS, skip 0**; 새 필수 receipt 7개를 실제 normal 완료 event와 대조 |
| 문서·format·diff | 로컬 링크 114개 문서 검사, 변경 Go gofmt drift 없음, `git diff --check` PASS |

세 Go lane은 **89개 변경 제품·검증 파일의 동일 SHA256 manifest**로 묶었다. Manifest SHA256:
`0d1406ef181fe092a655b821369eb783c83b3df06a3e3b9ecddd8ff01a0c700a`.
이후 CI 필수 receipt 등록 두 파일은 별도로 검증했다. 현재 non-document 변경은 이 89개와 CI 두 파일뿐이며 각각 현재 bytes를 대조했다.
Race에서 제외된 50 events는 기존 `internal/compiletest`의 `!race` 7개 top-level test와 하위 case로, normal·CGO0가 실행한다.
세 lane의 유일한 직접 skip은 `TestPostgresRevisionFenceHelperProcess`다. 실제 자식 process는 해당 parent integration tests가 실행하며
이 helper skip 자체를 기능 PASS로 세지 않는다. 생성 소비자의 child race 모드·전체 종료·필수 흐름·출력 잘림도 기존 strict harness가 검사한다.
전용 `godj_0083_normal`·`godj_0083_cgo0` DB는 검증 후 제거했다.

### 기능과 실패 검증

- 양 DB에서 440개 관찰의 전체 source·target 값, Count/First/All, 실제 조회 수와 LEFT JOIN 수를 비교했다.
  별도 generated SQLite module은 같은 440개를 facade typed/dynamic와 object-builder typed/dynamic 네 경로로 실행하고
  전체 하위 graph 접근의 추가 SQL 0·warm 반복·취소를 검사한다. Filter 입력의 별도 typed parity는 기존 GDJ-0082 회귀가 소유한다.
- 공통 prefix의 병합·부모 우선 정렬·불변 복사·유한 self-cycle occurrence를 확인했다. Raw AST source/filter metadata 충돌
  **12개씩**을 양 DB에서 일반·LIMIT 0 입력으로 pre-I/O 거부한다. SQLite의 63 selected JOIN은 실제 scan하고 64 JOIN은
  일반·빈 조회에서 SQL 전에 거부한다. Typed tree의 깊이 64/65와 입력 node 1024/1025 경계도 검사한다.
- 실제 generated scanner와 DB Rows에 scan/iteration/close 오류·취소·child PK 불일치·필수 child absence·partial child·
  없는 ancestor 아래 present/partial child를 주입한다. All/First가 부분 결과를 반환하지 않고 rowset을 한 번 닫으며,
  같은 query의 재시도와 이후 warm graph 접근이 성공함을 확인했다.
- 서로 다른 행·반환·동시 caller의 전체 graph 복제, 12개 동시 All의 SQL 1회, snapshot/Fresh/파생 query,
  선택하지 않은 하위 관계의 lazy cache·backend affinity, NULL/FK assignment와 바뀌지 않은 형제 cache를 확인했다.
  FK 직접 변경과 `With...ID` 모두에서 아직 접근하지 않은 형제 선택도 보존한다.
- `SelectedGraph`의 no-I/O·context·Fresh·absent 의미, `FromSelected`의 복사 객체·foreign binding 거부,
  다른 facade origin의 root/child selector 거부, 잘못된 중간 Go type의 compile 실패와 최초 configuration cause 보존을 검사했다.
  기존 stale generated handle·forged selector 음성 검증도 새 공통 factory selector 경로에 맞춰 유지했다.

초기 checkpoint는 통과로 세지 않았다. Selection-only 필수 child가 proven-present parent 아래에서 불필요하게 LEFT JOIN을 유지하던
경로를 수정했고, filter OR 경로의 기존 optional ancestry는 보존했다. 생성물·golden·폐쇄 selector 음성 fixture와 저장용 모델의
PK-presence를 잃던 테스트도 정리했다. 추가 검토에서 찾은 미접근 형제 cache 유실은 수정 전 생성 코드에서 두 assignment 방식의
실패를 재현한 뒤, 반환 전 하위 facade cache 준비로 해결했다. 정상 결과만으로 완료 처리하지 않고 최종 source의 세 lane을 대조했다.

제품 통합 source `a49b592be1896d02d73a9657fe360623ffa296d8`의
[Hosted ORM run 35425015186](https://github.com/progresshans/godj/actions/runs/35425015186)은 attempt 1에서 완료했다.
48개 unique job의 run ID·head SHA·terminal 상태와 예정된 제외 항목을 대조했으며 **44 success, 4 expected scope skip**이다.
최종 `CI result (orm)` job `105851855780`의 실제 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10의 normal/race/CGO0와 두 shard, 선택한 Linux/macOS의 portable·relation·command product를 검증했다.
제외된 범위는 exact Darwin profile, Python compatibility, product project check, reference/current capture다.
이후 문서 기록 commit을 실행 source로 표시하지 않는다.
새 full/platform·Windows runtime·배포 검증과 전체 프레임워크 완성은 이 기록에 포함하지 않는다.
Reverse/ManyToMany eager·무인자 자동 선택·일반 self/cyclic migration과 임의 cycle identity 공유는 후속 요구다.
Query 소비자는 명시적 FK-enforced DDL 뒤 generated Create/Save를 사용하며 cyclic migration 지원 증거로 확대하지 않는다.

## GDJ-0082 — Nested forward 경로의 독립 관찰

- 작업: [GDJ-0082](../../work/0082-nested-forward-relation-paths.md), 의미: [ADR-0023](../adr/0023-symbolic-relation-binding-and-shared-relation-ast.md#상태와-범위).
- 제품 baseline `7e5a933db69435154842162287ef86ef7172bc17`의 별도 작업 사본이다. Markdown을 제외한 변경·새 파일 91개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은
  `6c6b09512a07bb5506c199d39ff7633ef61f297816b6a3502de5bd7db1e57f25`다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)의 전용 DB·개별 schema를 사용했다. 완료 뒤 이번 작업이 만든 두 전용 DB를 제거했다.

### 독립 reference

- Python 3.14.7, Django 6.1, asgiref 3.12.1, sqlparse 0.5.5의 fresh process에서
  [runner](../../conformance/runners/django/nested_forward_reference.py)를 실행해 **146개 관찰**을 수집했다.
  [fixture](../../orm/testdata/nested-forward-django61.json)의 SHA256은 `2a7164ce3079f51227fbd60b134d2ba034ef42a9fb591b698ad45ce68741b53a`다.
- `uv run --no-project --isolated --python 3.14.7 --with Django==6.1 --with asgiref==3.12.1 --with sqlparse==0.5.5
  python -m unittest conformance.runners.django.tests.test_nested_forward_reference`는 **1 test PASS, skip 0**다.
  전체 JSON 일치, unique case 이름, count/ids·cold/warm First·warm cache의 추가 SQL 없음과 실행 수를 확인했다.
- 동일 Django 설치의 `django.db.models.sql.query`에서 `Query.setup_joins`(1890–2005), `trim_joins`(2007–2037),
  `build_filter`(1487–1657), `JoinPromoter.update_join_types`(2842–2897) source를 확보했다(BSD-3-Clause).

이 결과는 Django SQLite 관찰이다. 아래 PostgreSQL 증거는 GoDj를 실제 DB에서 실행한 결과이며 Django PostgreSQL 관찰로 세지 않는다.

### 구현과 실제 검증

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=10m`과 같은 패키지의 `CGO_ENABLED=0` 실행을 완료했다.
범위는 GDJ-0081과 같은 43 packages다. `query`, `orm`, `db/...`, `codegen`·외부 생성 소비자, compile-only typed 소비자,
관련 conformance/relationfixture·query·object·select·reverse·prefetch·delete·product와 examples를 포함한다.

- Normal·CGO0 각각 **28 test packages, 5,118 test 완료 event PASS**, no-test package 15개다. 시작/종료와 package
  terminal을 대조했다. 유일한 testcase skip은 부모가 별도 프로세스로 실행하는 `TestPostgresRevisionFenceHelperProcess`다.
- Race도 **28 test packages, 5,068 test 완료 event PASS**다. Normal/CGO0가 실행한 `internal/compiletest`의
  `!race` 최상위 7개·하위 포함 50개 event를 제외한 test roster가 일치한다. 세 lane의 제품 source manifest가 같다.
  이 source의 Hosted ORM 완료는 아래 통합 checkpoint에 별도로 기록한다. 전체 platform PASS로 확대하지 않는다.
- 양 DB에서 **146개 독립 관찰**의 All·First·Count·selected target field 값·SELECT 수·LEFT JOIN 수를 대조했다.
  Nullable ancestor 아래 required tail, self-cycle·공유 prefix·서로 다른 route, DateTime/정수/문자열/Boolean·IN·AND/OR/NOT,
  reverse 중복·Distinct·slice·direct eager 조합을 포함한다.
- 세 app의 외부 생성 모듈에서 generated Create/Save·typed/dynamic query·object builder·facade와 실제 SQLite를 실행했다.
  Dynamic/facade는 146개, typed는 명시적 NULL list member를 표현하지 않는 8개를 제외한 **138개**다.
  Selected 73개에서는 object builder도 대조하며, typed가 가능한 69개는 같은 AST를 비교한다.
  Cold/warm terminals·관계 접근의 추가 SQL 없음, 취소, cache 복제·Fresh·파생 query의 새 평가를 검사했다.
- Generated consumer의 physical fixture에는 실제 FK 제약이 있다. 기존 migration lifecycle은 self-reference CreateModel을
  거부하므로 이 fixture는 명시적 DDL로 준비했다. 이번 결과를 self/cyclic migration 지원·검증으로 확대하지 않는다.
- 두 DB에서 route 사이 FK column/nullability/target PK/table/root identity 충돌과 각 LIMIT 0 변형 **10개 plan**을 I/O 전에 거부했다.
  SQLite는 63 JOIN과 64-hop source-key trim을 실제 실행하고, 64 JOIN 초과와 LIMIT 0 초과도 I/O 전에 거부했다.
- Foreign snapshot composition, 잘못된 intermediate Go type, zero/과도한 깊이, policy 복제·오류 우선순위와 부분 batch 거부,
  초기/지연 generated group binding 실패의 원래 오류, private metadata 위조, concurrent compiler의 SQL 인자 분리를 검사했다.
- 최종 source의 전체 134 package compile-only, affected vet, 세 프로젝트 generated drift, format·113개 문서 local links·diff 검사 PASS.

### 첫 실행에서 수정한 사항

OR의 공통 nullable parent가 INNER로 바뀔 때 아래 다른 분기의 required JOIN까지 잘못 INNER로 바꾸던 계획을 수정했다.
선언상 optional ancestry와 최종 JOIN 종류를 별도로 보존하며 독립 SQL 관찰을 다시 통과했다.
Raw 관찰 helper의 빈 projection 처리, PostgreSQL fixture 관리 connection의 UTC timezone, 생성 소비자의 명시적 정렬과
structured error 기대값을 바로잡았다. 없어진 per-edge query 타입과 충돌하던 옛 schema는 이제 허용하는 양성 검증으로
전환했고, 실제 generic member/type namespace 충돌과 원자적 실패 검증은 유지한다.

### 통합 checkpoint

Feature `3cfecb9`를 기존 Draft PR #1에 통합한 source는 `3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0`다.
제품 91개 파일이 로컬 검증 manifest와 일치함을 확인했다. 두 문서 충돌은 이미 반영한 GDJ-0081 Hosted 완료와
새 구현 상태를 유지하여 해결했다. [Hosted ORM run 35421304637](https://github.com/progresshans/godj/actions/runs/35421304637)의
48개 unique job의 `head_sha`·run ID·attempt 1·terminal 상태를 대조했다. **44 success, 4 expected scope skip**으로 완료했으며
최종 `CI result (orm)` job `105841846273`의 보고서는 다음과 같다.

```json
{"full_platform_verified":false,"scope":"orm","verified_jobs":["command-product-matrix","portable-go-matrix","postgresql-product","relation-product-matrix"]}
```

PostgreSQL 17.10 여섯 mode/shard와 선택된 Linux/macOS를 검증했다. 제외 항목은 product project check,
exact Darwin profile, Python compatibility, reference/current capture다. 최신 full은 별도 source `8fd8936d`의
run `35384697050`이며, 이 source의 full·Windows runtime·배포 결과로 사용하지 않는다.

## GDJ-0081 — 여러 direct forward target 동시 선택

- 작업: [GDJ-0081](../../work/0081-multiple-forward-eager-selections.md), 의미: [ADR-0029](../adr/0029-one-hop-forward-select-related.md#상태와-범위).
- Source: `1c71610ec705c29fae85417be4800068473003bd` 기반 feature 작업 사본. Markdown을 제외한 변경·새 파일·삭제 97개의
  `<sha256 또는 deleted>  <relative-path>\n` 정렬 manifest SHA256은
  `d7384cf7b3b3a660d93aa52ca6ed656ac432a8ec3439d9d2839ee3c1b561034f`다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)의 전용 DB·개별 schema에서 실행했다.
  최종 lane 종료 후 이번 작업이 만든 두 전용 DB를 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=10m`의 영향 범위는 다음과 같다.
같은 패키지 목록을 `-race`, `CGO_ENABLED=0`에도 사용한다.

```text
./query ./orm ./db/... ./codegen ./codegen/consumertest ./internal/compiletest
./conformance/nullableforwardproduct ./conformance/relationfixture/...
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationqueryproduct ./conformance/relationreverseproduct
./conformance/relationprefetchproduct ./conformance/relationdeleteproduct
./conformance/relationproduct ./examples/...
```

Normal·CGO0 각각 **28 packages, 4,792 test 완료 event PASS**, race는 **28 packages, 4,742 event PASS**다.
각 lane의 no-test package는 15개다. `internal/compiletest`의 `!race` build tag로 제외하는 7개 compile-only 최상위 검사와
그 하위 event 50개는 normal·CGO0에서 완료했다. Race에서 이 50개를 실행한 것으로 세지 않았으며 나머지 test roster가 일치함을 확인했다.
시작/종료 event와 package 완료·필수 sentinel을 대조했다. 직접 진입 skip 한 개는 부모가 자식 프로세스로 실행하는
`TestPostgresRevisionFenceHelperProcess`이며 기능 PASS로 세지 않는다. 최초 시도는 테스트 PostgreSQL URL의 hostname 누락으로
실패했다. 이어 기존 dynamic 전용 zero 상태의 오류 기대값과 단일 selector private field를 주입하던 fixture를 현재 공통
runtime/복수 selection 구조에 맞게 고쳤다. Resolver를 바꿔 잘못된 생성물을 만드는 음성 검증도 현재 생성 위치로 갱신했고,
원래의 오류 cause·pre-I/O 거부를 계속 검사한다. 위 완료 결과는 이 fixture 보완을 포함한다.

### 확인한 의미

- FK 이름으로 정렬한 immutable projection 집합과 하나의 `ForwardSelectQuery[S]` runtime을 사용한다. Source와 모든 target을
  한 번의 Rows.Scan으로 읽으며 구체 target Go type은 닫힌 adapter에 남는다. 입력 순서·같은 선택의 반복이 결과를 바꾸지 않는다.
- 고정 Django 6.1의 **220개 독립 관찰**을 실제 SQLite·PostgreSQL의 rows·First·Count·SELECT 수·LEFT JOIN 수와 비교했다.
  같은 Person을 가리키는 두 FK와 별도 Team FK, nullable target, reverse 중복·Distinct·Offset·Limit·빈 결과를 포함한다.
- 별도 생성 모듈의 migration·SQLite·typed/dynamic object builder·model별 variadic facade를 실행했다.
  Dynamic/facade는 220개, typed는 explicit NULL-only IN 입력을 제외한 215개다. 선택 후 Filter·OrderBy·Distinct·Offset·Limit과
  Fresh가 전체 선택을 보존하며 cold/warm terminal과 각 relation accessor의 추가 I/O 없음도 검사했다.
- 같은 PK의 서로 다른 FK, reverse JOIN으로 중복된 source와 query cache는 각각 독립 소유한다. 단일→복수 builder 파생과 caller의
  selector slice 변경이 이전 query를 바꾸지 않는다. Concurrent All의 단일 평가와 모든 target cache의 독립 복제를 검사한다.
- 두 번째 target의 잘못된 key·부분 NULL·부재, scan/rows/close/취소 실패는 All·First에서 부분 결과/cache를 게시하지 않으며
  다음 All이 재시도한다. Nil scanner·잘못된 destination 수·typed nil destination, 다른 binding·충돌한 중복 selection도 거부한다.
- 양 DB에서 selected/filter/source-key provenance 충돌과 존재하지 않는 두 번째 selected source key **30개 plan**을 SQL 전에 거부한다.
  LIMIT 0도 이 검증을 생략하지 않는다. 기존 GDJ-0080의 정상 self-reference와 실패 경로도 같은 실행에서 유지한다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 fresh Django runner를 다시 실행해 각각 **1 test PASS, skip 0**을 확인했다.
  별도 invalid-selector 관찰 8개에서 Django Count는 selected 이름을 무시하지만 GoDj는 Count에도 기존 binding/configuration 검증을
  적용한다. 이를 동등성 PASS로 세지 않는다. Django oracle은 SQLite profile이고 PostgreSQL은 위 GoDj 실제 DB 결과다.
- 전체 134 package compile, affected vet, 세 프로젝트 generated drift, CI script unittest 37개, format·docs·diff 검사 PASS.

### 통합 checkpoint

Feature `876595d7dd0a0425cf9a062e7f332f38e63f3e3e`를 통합한 source `7e5a933db69435154842162287ef86ef7172bc17`의
[Hosted ORM run 35417711000](https://github.com/progresshans/godj/actions/runs/35417711000) attempt 1이 **44 success·4 expected scope skip**으로 완료했다.
48개의 고유 job ID·run ID·head SHA·종료 상태를 대조했으며 최종 job `105831664789`의 보고서는
`scope=orm`, `full_platform_verified=false`, command/portable/postgresql/relation owner 검증 완료다.
PostgreSQL 17.10 여섯 mode/shard와 선택된 Linux/macOS 환경을 포함한다. Product project-check, Python compatibility,
exact Darwin profile, reference/current-capture owner는 이 scope에서 제외했다. 통합한 97개 source manifest는 로컬 검증과 같았다.
이전 GDJ-0080 source `7397a73b933eef4d30c5a8fa12c84a79fc7945e9`의 완료 결과를 이 변경의 PASS로 재사용하지 않는다.
전체 플랫폼·Windows runtime·배포 검증은 이 로컬 결과에 포함하지 않는다.

## GDJ-0080 — Eager materialization과 filter JOIN 조합

- 작업: [GDJ-0080](../../work/0080-eager-filter-join-composition.md), 의미: [ADR-0029](../adr/0029-one-hop-forward-select-related.md#상태와-범위), [빈 조회 경계](../adr/0062-scalar-membership-and-empty-query-execution.md).
- Source: `408c4179d52c5ea8925f503a000ef69d9f892289` 기반 작업 사본. Markdown을 제외한 변경·새 파일 21개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `2e21f11bd6e759910e172ae4bf1c1471e0f51f8766a431913ec5a1b21c4ea93b`다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5(Homebrew)의 전용 DB·개별 schema에서 실행했다.
  최종 lane 종료 뒤 이번 작업의 전용 PostgreSQL DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`과 같은 범위의 `-race`, `CGO_ENABLED=0` 실행을 완료했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./conformance/nullableforwardproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationqueryproduct ./conformance/relationreverseproduct ./examples/...
```

Normal·race·CGO0 각각 **22 packages, 3,828 test 완료 event PASS**, no-test package 11개다.
모든 test의 시작/종료·package 완료와 필수 sentinel을 대조했다. 각 lane의 직접 진입 skip은 부모가 자식 프로세스로
실행하는 `TestPostgresRevisionFenceHelperProcess` 한 개이며 이를 기능 PASS로 세지 않았다.
초기 실행은 LIMIT 0의 불필요한 SQL과 새 무결성 테스트의 오류 code 기대값을 보정하기 전 실패했다.
별도 생성 모듈의 컴파일·실행으로 초기 소비자 실패 원인도 확인했다. 최종 검토에서는 같은 root FK의 forward/reverse view가
서로 다른 target을 선언해도 compile되던 self-reference 경계를 재현하고 보완했다. 위 결과는 이 보완까지 포함한 전체 영향 범위를
normal·race·CGO0로 다시 실행한 결과다.

### 확인한 의미

- 하나의 required/nullable selected edge와 다른 forward/reverse filter JOIN을 함께 compile·materialize한다.
  선택한 alias의 target columns만 root columns 뒤에 붙이며 reverse filter의 중복 행을 유지한다.
- 고정 Django 6.1의 **88개 독립 관찰**에서 양 DB의 All·First·Count, Distinct·Offset·Limit, nullable target와
  실제 SELECT 수·LEFT JOIN 수를 비교했다. PostgreSQL tracer는 production physical-session 검증을 유지했다.
  알려진 빈 결과는 PostgreSQL 전체 connection query 수와 SQLite query counter가 늘지 않음을 확인했다.
- Generated model·migration·typed/dynamic selector·facade를 별도 module의 실제 SQLite에서 실행했다.
  Dynamic/facade는 88개, typed는 explicit NULL-only member의 두 경우를 제외한 86개를 검사했다.
  Cold/warm All·First·Count, selected relation 접근의 추가 I/O 없음, 취소 우선순위와 typed/dynamic AST 일치를 대조했다.
- 중복 source object·FK pointer·selected target cache의 수정이 다른 반환 객체나 query cache에 전파되지 않음을 검사했다.
  여러 JOIN의 scan/Rows.Err/Rows.Close/취소/행 무결성 실패 후 부분 cache를 게시하지 않고 retry가 성공함을 확인했다.
- 다른 root identity, 같은 FK의 conflicting target/table/PK, nonselected FK·source-key proof 충돌을 포함한 **28개 잘못된 plan**을
  양 DB에서 pre-I/O 거부했다. LIMIT 0으로 감싸도 검증을 생략하지 않는다. 정상 self-reference는 동일 FK의 forward/reverse view로
처리하며 실제 양 DB에서 NULL parent와 child/grandchild가 만든 중복 `[1, 2, 2]`를 보존했다. 공유 plan의 concurrent compile과 detached arguments도 확인했다.
- `LIMIT 0`을 공통 empty-source 분석에 포함했다. Backend/session은 전체 compile·context/lifetime 검증을 먼저 실행하고
  model은 빈 결과, COUNT는 0을 반환한다. 조기 반환으로 metadata/capability 검사를 건너뛰지 않는다.
- 기존 generated Count 22개·nullable-forward 73개·scalar-lookup 748개 관찰에서도 서로 다른 eager/filter edge의 All을
  미지원 기대값 대신 실제 rows와 warmed relation/cache 결과로 검증했다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 독립 eager/filter runner를 다시 실행해 각각 **1 test PASS, skip 0**을 확인했다.
  Django reference는 SQLite profile이며 PostgreSQL 결과는 위 GoDj actual DB 검증이 소유한다.
- 전체 compile, affected vet, 최종 generated drift, CI script unittest 37개, format·docs·diff 검사 PASS.

### 통합 checkpoint

초기 통합 source `629f0a0deaf9d8c2beed157ef9b51fcd464fcb6d`의 [Hosted ORM](https://github.com/progresshans/godj/actions/runs/35413019895)을
attempt 1은 48개 job의 source·run ID·완료 상태를 대조해 **44 success, 4 scope skip**으로 완료했다.
최종 `CI result (orm)` report는 `scope=orm`, `full_platform_verified=false`이며 command/portable/postgresql/relation owner를 검증했다.
PostgreSQL 17.10의 core/operator-target × normal/race/CGO0 여섯 조합과 선택된 Linux/macOS를 포함한다.
그 뒤 위 self-reference 보완이 추가됐으므로 이 실행을 보완 후 source의 결과로 재사용하지 않는다.
최종 보완 source `7397a73b933eef4d30c5a8fa12c84a79fc7945e9`의 [Hosted ORM run 35414363995](https://github.com/progresshans/godj/actions/runs/35414363995),
attempt 1도 **44 success, 4 scope skip**으로 완료했다. 48개 job의 유일한 ID·run ID·head SHA·terminal 상태를 대조했다.
`CI result (orm)` job `105822544681`의 report는 `scope=orm`, `full_platform_verified=false`와
command/portable/postgresql/relation owner 완료를 기록한다. 실제 PostgreSQL 17.10의 여섯 mode/shard 조합과
선택된 Linux amd64/arm64·macOS arm64/amd64의 관계·command 검증을 포함한다.
네 skip은 project-check matrix·Python compatibility·exact Darwin profile·reference/current-capture job이며 해당 범위를 PASS로 계산하지 않는다.
여러 selected projection·nested traversal·reverse OR/NOT·새 full/platform·배포·전체 ORM 완료는 이 결과에 포함하지 않는다.

## GDJ-0079 — Direct forward scalar lookup

- 작업: [GDJ-0079](../../work/0079-forward-scalar-lookups.md), 의미: [ADR-0040 추가 결정](../adr/0040-composable-typed-boolean-predicates-and-article-search.md#직접-forward-대상의-scalar-lookup), [IN 의미](../adr/0062-scalar-membership-and-empty-query-execution.md).
- Source: `c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b` 기반 작업 사본. Markdown을 제외한 변경·새 파일 33개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `308bd105930855cca56e99da76444c2c542588111f8ce53206365e6899e667a0`이다.
  독립 reference·generated consumer 입력·CI 필수 완료 sentinel·최종 테스트 보정을 포함한다.
- Go 1.26.5 darwin/arm64, modernc SQLite와 PostgreSQL 17.5 (Homebrew)의 전용 DB/개별 schema에서 실행했다.
  최종 lane 종료 뒤 이 작업에서 만든 전용 PostgreSQL DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`으로 다음 영향 범위를 실행했다.

```text
./query ./orm ./db/... ./codegen/... ./internal/compiletest
./conformance/nullableforwardproduct ./conformance/relationqueryproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationreverseproduct ./conformance/relationprefetchproduct
./conformance/relationdeleteproduct ./examples/... ./internal/projectgenerate/...
```

최종 normal은 **29 packages, 4,199 test 완료 event PASS**, no-test package 12개다.
처음 실행 뒤 남은 두 package `codegen/consumertest`, `internal/compiletest` 전체를 보정 후 다시 실행했다.
최초 normal source와 최종 source의 차이는 이 두 package의 `_test.go` 세 파일뿐임을 hash로 대조했다.
그 외 제품·테스트·fixture bytes는 그대로다. Package별 마지막 완전한 실행을 사용하며 실패 event를 PASS에 합치지 않는다.

다음 범위는 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.
CGO0는 public generic API의 외부 compile 검사를 위해 `./internal/compiletest`도 포함했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./examples/...
./conformance/relationselectproduct ./conformance/relationqueryproduct
./conformance/relationobjectproduct ./conformance/relationreverseproduct
./conformance/relationprefetchproduct ./conformance/relationdeleteproduct
```

Race는 **24 packages, 3,614 test 완료 event PASS**, CGO0는 **25 packages, 3,668 test 완료 event PASS**다.
각 no-test package는 10개다. 모든 test의 시작/종료와 필수 consumer, 양 DB의 748개 하위 case를 대조했다.
Normal의 직접 진입 skip은 PostgreSQL revision-fence·generated publication crash helper 두 개, race/CGO0는 PostgreSQL helper 한 개다.
부모의 process 실행과 구분하며 이 skip을 기능 PASS로 계산하지 않는다.

### 확인한 의미와 수정

- Forward target의 Integer·Char/Text·DateTime nullable/non-null과 Boolean을 typed/generation에 연결했다.
  Scalar kind별 exact·비교·icontains·isnull·IN과 명시적 dynamic suffix, policy-before-value와 원자적 실패를 검사했다.
- 같은 immutable AST에서 declared field nullability와 optional forward path를 함께 계산한다. Target isnull의 JOIN promotion과
  explicit NULL member의 홀수 부정, 빈 목록의 SQL 생략을 확인했다. 원래 field metadata·입력 목록·accessor 반환 목록은 공유하지 않는다.
- 고정 Django 6.1의 **748개 독립 관찰**에서 행 ID·Count·실제 SELECT 수와 named-field JOIN 종류를 양 DB에서 대조했다.
  PostgreSQL은 production connection profile/physical guards를 유지한 tracer로 조회 수를 측정했다. Known-empty case는
  전체 connection query 수도 늘지 않음을 확인했다. SQLite도 실제 backend query counter를 비교했다.
- 별도 module의 generated model·migration·relation adapter·facade가 748개 dynamic 결과와 **typed로 표현하는 628개의 AST 일치**를 검사했다.
  나머지 120개는 explicit NULL member가 있는 dynamic 입력이며 typed parity로 세지 않는다. Cold/warm Count·eager cache·nullable target scan과
  invalid/canceled empty query의 pre-I/O 거부를 확인했다. 다른 eager/filter edge의 All은 기존 미지원 오류를 계속 검사한다.
- 실제 Helpdesk 양 DB 재연결 뒤 category 이름의 IContains+IN, dynamic suffix와 eager Count/All을 연결했다.
  서술형 검색 예시는 README에 추가했다. 일반 HTTP 검색 규칙이나 인가 정책을 바꾸지 않았다.
- 생성 후보 compile에서 공유 terminal 필터가 reverse의 nullable/Boolean까지 넓어지는 문제를 발견했다. Reverse 생성 범위를 명시하고
  세 프로젝트의 후보 compile·생성을 다시 완료했다. Reverse non-exact/OR/NOT은 계속 미지원이다.
- 새로운 sealed ReferenceField signature에도 과거 진단 문자열을 요구하던 negative compile 기대값과 nullable target 미지원 기대값을 갱신했다.
  실제 다른 model field의 사용은 계속 compile에 실패하며 타입 구분을 검사한다.
- 748개 child test의 JSON이 compiler 진단용 128 KiB 캡처를 초과했다. Test 결과에는 별도의 bounded 4 MiB 캡처를 사용하고
  초과분 drain·총량 대조·truncation/진단/skip/missing completion 거부를 유지했다. 잘린 첫 결과를 PASS로 세지 않고 package 전체를 다시 실행했다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 forward-lookup·nullable-forward·eager-count reference 각각을 다시 관찰했다.
  각 버전 **3 tests PASS, skip 0**다. 이 reference profile은 SQLite이며 PostgreSQL 비교는 위 실제 GoDj 실행이 소유한다.
- 전체 compile·vet, 최종 보정 package의 vet, generated drift, CI script unittest, docs·format·diff 검사 PASS.

### 통합 검증 소유자

이 작업의 구현과 필수 로컬 영향 범위를 완료했다. OS/process 구현이나 새 backend를 추가하지 않았다.
통합 Hosted ORM은 GDJ-0080과 함께 source `7397a73b933eef4d30c5a8fa12c84a79fc7945e9`에서 완료했다.
위 GDJ-0080 checkpoint가 source·scope와 terminal 근거를 소유한다. 위 baseline의 GDJ-0078 Hosted는
이 새 lookup source의 실행 결과가 아니다. 새 full·Windows runtime·배포·전체 ORM 완료를 주장하지 않는다.

## GDJ-0078 — Nullable forward 대상 필터와 Boolean JOIN

- 작업: [GDJ-0078](../../work/0078-nullable-forward-relation-predicates.md), 의미: [ADR-0040 추가 결정](../adr/0040-composable-typed-boolean-predicates-and-article-search.md).
- Source: `6eb40412501b8945dad06a75f95e3b39510e2fe0` 기반 작업 사본. Markdown을 제외한 변경·새 파일 36개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `289ba29a040db517ae8675c28163911dd6edb600902887219a7596473df6befc`다.
  독립 Django runner/JSON, 실제 생성 소비자 입력, 양 DB 실행 fixture와 CI 필수 완료 sentinel을 포함한다.
  검증 후 `ForwardRelation`·`BindForward`의 GoDoc 두 곳만 현재 nullable 지원에 맞춰 고쳤다. 실행 코드는 그대로이며
  통합할 36-file manifest는 `14ad182cc754178cbbbf8151775394c653c9481bfee6da50b5411d985e710342`다.
- Go 1.26.5 darwin/arm64, modernc SQLite, 실제 PostgreSQL 17.5 (Homebrew)의 전용 DB와 테스트별 schema를 사용했다.
  최종 lane이 모두 끝난 뒤 이 작업에서 만든 전용 DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`으로 다음 영향 범위를 실행했다.

```text
./query ./orm ./db/... ./codegen/... ./internal/compiletest
./conformance/nullableforwardproduct ./conformance/relationqueryproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct
./conformance/relationreverseproduct ./conformance/relationprefetchproduct
./conformance/relationdeleteproduct ./examples/... ./internal/projectgenerate/...
```

최종 normal은 **29 packages, 2,699 test 완료 event PASS**, no-test package 12개다.
다음 범위를 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./examples/...
./conformance/relationselectproduct ./conformance/relationqueryproduct
./conformance/relationobjectproduct ./conformance/relationreverseproduct
./conformance/relationprefetchproduct ./conformance/relationdeleteproduct
```

Race·CGO0는 각각 **24 packages, 2,114 test 완료 event PASS**, no-test package 10개다.
모든 lane의 JSON 전체에서 test 시작/종료를 대조하고 양 DB의 73개 하위 case, generated nullable/eager Count 소비자와
불일치하는 존재 증명의 사전 거부를 필수 완료로 확인했다. Normal의 직접 진입 skip은 PostgreSQL revision-fence·generated publication
crash helper 두 개, race·CGO0는 PostgreSQL helper 한 개다. 부모의 process 실행과 구분하며 이 skip을 기능 PASS로 계산하지 않는다.

### 확인한 경계와 수정

- Required/nullable source FK의 직접 대상 exact를 typed/dynamic 같은 AST와 generated relation adapter에 연결했다.
  기존 non-null Integer·Char/Text·DateTime과 대상 PK, root scalar·source-key isnull의 AND/OR/NOT·중첩 부정을 검증했다.
- 필터가 반드시 요구하는 대상 존재를 공통 planner가 계산한다. Nullable edge의 INNER/LEFT JOIN과 홀수 부정의 joined 대상 column
  IS NOT NULL 보정으로 null source 행을 보존한다. Source-key 존재 증명은 실제 predicate edge와 정확한 hop metadata 일치를 요구한다.
- 고정 Django 6.1/SQLite를 별도 process로 실행해 **73개 독립 관찰**의 행 ID·Count를 실제 SQLite와 PostgreSQL에서 대조했다.
  Named target-field JOIN 종류도 비교했다. Django target-PK JOIN 생략 최적화는 구현·parity 주장 대상이 아니다.
  SQLite의 실제 query 수와 별도 생성 module의 cold/warm Count·eager cache·typed/dynamic AST를 확인했다.
- 이전 GDJ-0077의 Count 관찰 22개 중 미지원이던 nullable target-field case도 이제 실제 generated consumer에서 지원한다.
  현재 source에서는 **22개 전체의 Count를 검증**한다. 서로 다른 eager/filter edge의 All 6개는 계속 명시적 미지원이다.
- 하위 Query AST의 기존 Boolean·nullable target scalar 허용과 SQLite quoted identifier 정책을 보존했다.
  이것이 typed/dynamic nullable target scalar API 확장을 뜻하지 않는다. Reverse OR/NOT·다른 relation lookup·다중/중첩 eager는 미지원이다.
- 초기 runtime 검사에서 남아 있던 nullable 거부 기대값과 sparse generated binding 기대값을 갱신했고, 테스트의 SQLite DateTime 저장 형식을
  production의 고정 UTC microsecond text에 맞췄다. 두 Boolean case에서 source-key isnull의 존재 증명도 JOIN 계획에 반영했다.
  검토 후 compiler validation의 불필요한 AST 축소와 혼합 metadata를 수정하고 위 normal 전체를 최종 source로 다시 실행했다.
  중간 재실행의 PostgreSQL URL 환경 변수 오기는 실패로 남기고 올바른 필수 환경으로 다시 실행했다. 이전 실패를 PASS에 합치지 않았다.
- Python 3.12.13·3.13.15·3.14.3·3.14.7에서 nullable-forward와 eager-count 독립 reference를 각각 **2 tests PASS, skip 0**으로 확인했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...` 및 최종 공통 compiler 수정 후 `go vet ./db/...`, generated drift,
  CI script unittest, docs·format·diff 검사 PASS. Generated/Python/CI 입력은 해당 검사 뒤 변하지 않았다.

### 통합 검증 소유자

로컬 영향 범위와 GDJ-0077 Count를 포함한 Hosted ORM 통합 검증을 완료했다.

- 통합 source `c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b`, [run 35407175164](https://github.com/progresshans/godj/actions/runs/35407175164), attempt 1:
  **completed/success, 고유 job 48개 중 44 success·4 의도한 scope skip**. 모든 job의 source·run identity·terminal 상태를 대조했다.
- 최종 job `105802085743`의 report는 `scope=orm`, `full_platform_verified=false`이며 소유자는
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix`다.
  실제 PostgreSQL 17.10 core/operator-target의 normal·race·CGO0와 선택된 Linux amd64/arm64·macOS arm64/amd64 제품/command를 포함한다.
- 비대상 네 owner는 product project-check matrix, Python compatibility matrix, exact darwin/arm64 reference profile,
  reference/current-capture 통합이다. 새 full·Windows runtime·배포 결과로 표시하지 않는다.
- 이후 GDJ-0079의 새 scalar lookup 구현은 이 source에 포함되지 않는다. 위 PASS를 다음 구현의 결과로 재사용하지 않는다.

GDJ-0076의 과거 Hosted와 Text+DateTime의 과거 full은 각각의 source를 증명한다. 전체 프레임워크의 완료나 출시 결정이 아니다.

## GDJ-0077 — 관계 조회 Count와 캐시 의미

- 작업: [GDJ-0077](../../work/0077-eager-count-and-query-cache-semantics.md), 의미: [ADR-0029 추가 결정](../adr/0029-one-hop-forward-select-related.md).
- Source: `d0f079481d660682c7a18884ce2c305234f6ad38` 기반 작업 사본. Markdown을 제외한 변경·새 파일 24개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `f88b777ce1d44f94620828e5f198d7c82884269e1f73e8da73e985d53f1f80e0`이다.
  별도 generated module의 test 입력, Python runner와 독립 JSON fixture를 포함한다.
- Go 1.26.5 darwin/arm64, modernc SQLite, 실제 PostgreSQL 17.5 (Homebrew)의 전용 DB와 테스트별 schema를 사용했다.
  모든 로컬 lane 뒤 이 작업이 만든 전용 DB만 제거했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`으로 다음 영향 범위를 실행했다.

```text
./query ./orm ./codegen/... ./internal/compiletest ./db/... ./examples/helpdesk
./conformance/relationselectproduct ./conformance/relationproduct ./conformance/relationqueryproduct
./internal/projectgenerate/...
```

최종 normal은 **16 packages, 2,371 test 완료 event PASS**, no-test package 2개다.
최초 실행에서 새 독립 consumer가 기존 generated relation API의 범위를 잘못 사용해 compile에 실패했다.
Reverse adapter를 올바르게 사용하도록 고치고 nullable target-field lookup은 기존 미지원 오류를 검사하도록 수정했다.
해당 `codegen/consumertest` package 전체를 다시 실행했다. 다른 Go 제품·테스트·fixture bytes는 그대로이며,
추가 변경한 Python runner의 version guard는 아래 독립 reference lane에서 검증했다. 실패한 event는 PASS로 계산하지 않았다.

다음 범위를 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.

```text
./query ./orm ./db/... ./codegen/consumertest ./examples/helpdesk ./conformance/relationselectproduct
```

Race·CGO0는 각각 **9 packages, 1,775 test 완료 event PASS**, no-test package 1개다.
모든 test의 start/terminal event를 대조했고 필수 generated consumer·실제 Helpdesk 양 DB·Count 실패/동시성 검사가 완료됐다.
Normal의 직접 진입 skip은 PostgreSQL revision-fence·generated publication crash helper 두 개, race/CGO0는 PostgreSQL helper 한 개다.
부모 process 검증과 구분하며 이 skip을 기능 PASS로 세지 않는다.

### 확인한 경계

- Eager projection만 제거하고 filter·order·Distinct·Offset·Limit을 보존한다. Cold Count는 SQL 집계를 사용하며
  두 번 호출하면 각각 평가한다. Eager All 완료 뒤에는 그 cache의 길이를 재사용하고 원래 QuerySet의 cache는 빌려 쓰지 않는다.
- Context/typed-nil/zero query/binding 오류의 사전 거부, scan/rows/close/backend 실패의 원인 보존·정확한 Close·재시도,
  실행 중인 All과 독립적인 cold Count를 검사했다. Count는 related object를 materialize하거나 그 row 무결성을 검사하지 않는다.
- 별도 module의 실제 generated project에서 typed·dynamic·facade Count, 필수·nullable FK, 빈 IN·slice·Distinct와
  reverse filter의 중복 행을 검증했다. 고정 Django 6.1의 22개 관찰 중 **21개 count·cold SQL 수·JOIN 수를 실제 SQLite에서 대조**했다.
  Nullable target-field filter 1개는 기존 미지원 오류를 확인했고 parity로 세지 않는다. 서로 다른 eager/filter JOIN의 All 6개도 미지원 상태다.
- 실제 SQLite/PostgreSQL Helpdesk에서 기존 DB 재연결 후 eager Count→페이지 All→cache Count와 관계 접근을 확인했다.
  기존 외부 `.go.txt` compile 소비자에도 typed/dynamic/facade Count를 연결했다.
- 독립 reference는 Python 3.12.13·3.13.15·3.14.3·3.14.7에서 각각 **1 test PASS, skip 0**로 다시 관찰했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.

### 검증 소유자

이 변경의 필수 로컬 영향 범위를 완료했다. DB별 SQL 또는 platform/process 구현은 변경하지 않았다.
새 Hosted ORM은 위 GDJ-0078과 묶은 source `c8bb50df3f540f56f37f5691fff36a6e0f7fcc8b`에서 완료했다.
GDJ-0076의 Hosted는 그 기준 source만 증명하며, 이번 통합 ORM을 전체 플랫폼·새 full·배포 결과로 표시하지 않는다.

## GDJ-0076 — 모델 선택값과 metadata-only migration

- 작업: [GDJ-0076](../../work/0076-model-choices-and-metadata-migrations.md), 의미: [ADR-0063](../adr/0063-model-choices-and-metadata-only-migrations.md).
- Source: `adb3ea62f7c8f9a57c623634e2c11b60f04374bc` 기반 작업 사본. Markdown을 제외한 변경·새 파일 144개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `601ac03fd9d9a7742e69194444b628670c0bdf9374308ffd3bd4934ebc237037`이다.
  독립 Django runner/fixture, generated model 입력과 별도 OpenAPI client module을 포함한다.
- 환경: Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5 (Homebrew). 새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했다.
- 로컬 lane 종료 후 이 작업에서 생성한 전용 DB만 제거했다.
- 로컬 제품 bytes를 통합한 source는 `e5688068ea64ac55493ccbbe0d622ccdd084847b`이다. 이후 외부 migration 어댑터의 텍스트 compile fixture 한 파일을 수정한
  `d0f079481d660682c7a18884ce2c305234f6ad38`에서 아래 Hosted ORM 검증을 완료했다.

### 로컬 실행

`GODJ_REQUIRE_POSTGRES=1`, `go test -json -count=1 -timeout=15m`의 영향 범위는 다음과 같다.

```text
./schema/... ./forms/... ./serializers ./api/openapi/... ./admin
./migrations/... ./db/... ./codegen/...
./internal/projectwire ./internal/projectspec ./internal/irresource ./internal/migrationautodetect
./internal/projectgenerate/... ./internal/projectmigration/... ./internal/projectcheck/...
./examples/... ./conformance/choicesproduct ./conformance/migrationrelationproduct ./conformance/runners/godj
```

최종 normal은 **46 packages, 4,355 test 완료 event PASS**, no-test package 14개다.
첫 broad normal 뒤 남은 실패는 새 capability의 기대값과 바뀐 Helpdesk 입력에 대한 parent fixture 기대값이었다.
이를 반영하고 capability 없는 choices 변경의 사전 거부 검사를 추가한 뒤 `migrations`, `migrations/backend`, `db/sqlite`,
`api/openapi/consumertest` 전체를 다시 실행했다. 최초 broad normal과 최종 source의 차이는 해당 네 package의 `_test.go` 네 파일뿐이며,
나머지 package의 제품·테스트·fixture bytes는 그대로임을 hash로 대조했다. 실패한 event를 PASS로 계산하지 않고 package별 최종 실행을 사용했다.

다음 범위를 `go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`으로 실행했다.

```text
./schema/... ./forms/... ./serializers ./admin ./api/openapi/...
./migrations/... ./db/... ./codegen/consumertest ./conformance/choicesproduct ./examples/helpdesk
./internal/projectwire ./internal/projectspec ./internal/migrationautodetect
```

Race·CGO0는 각각 **21 packages, 2,334 test 완료 event PASS**, no-test package 2개다.
JSON 전체를 읽어 test 시작/종료·실패·필수 소비자 완료를 확인했다. Normal의 직접 진입 skip은 부모가 process로 실행하는
PostgreSQL revision fence·makemigrations crash·generated publication crash helper 세 개이며, race/CGO0에는 PostgreSQL helper 한 개다.
이 skip을 기능 PASS로 세지 않는다. 필수 choices·actual DB·generated model·OpenAPI client는 skip 없이 완료했다.

### 수정과 검증한 의미

- 초기 실행에서 AlterField가 이전 field slice를 공유해 변경 전 상태까지 바꾸는 문제를 발견했다. 요소를 교체하기 전에 slice를 분리해
  정확한 Before/After와 역방향 복원을 보존했다. PostgreSQL은 같은 step의 relation target 선택값 변경을 허용하면서, 순서별 metadata의
  정확한 일치는 별도로 검사하고 초기 물리 catalog 비교에서만 choices를 제외했다. 위조된 target label은 양 renderer에서 거부한다.
- Project wire scan·크기 계산·resource scan, definition encode/decode·digest·loaded intent, SQLite seal에 choices와 scalar 전체 payload를
  연결했다. 검토 중 확인한 DateTime default의 scan/size/seal 누락도 함께 보완했다. 기존 migration definition golden bytes는 유지했다.
- 고정 Django 6.1/DRF 3.18.0의 독립 19개 입력을 Form/serializer에서 비교했다. [DEV-0012](../DEVIATIONS.md)의 JSON type 차이는
  명시적 assertion으로 검사하며 parity로 세지 않는다. Python model clean 관찰은 GoDj 모델 validation 구현 증거가 아니다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7에서 각각 **1 test PASS, skip 0**로 reference 전체를 다시 관찰했다.
- 별도 module의 실제 generated model은 string·Text·int64 choices의 metadata 소유권, Form Select·공백·null/0, serializer,
  ordinary ORM Create/Update/Save·typed/dynamic query와 목록 밖 저장 값을 확인했다. child test 두 개의 완전한 종료를 요구한다.
- Helpdesk의 이전 0001..0004 파일을 보존하고 실제 makemigrations로 0005 선택값 추가와 0006 label/order 변경을 작성했다.
  양 DB에서 기존 priority 99를 전진/역방향/재적용 동안 보존하고, Admin/API에서 허용값을 검증하며 기존 int64 극값을 조회·표시했다.
- SQLite는 metadata 왕복 뒤 schema_version 불변과 revision의 정확한 증가를, PostgreSQL은 table OID/heap 불변을 검사했다.
  혼합 AlterField/AddField, FK 무결성, 물리 drift 거부와 recorder 보존을 실제 DB에서 확인했다. 순수 SQL projection은 choices에 0개,
  물리 변경과 혼합된 step에는 물리 SQL만 반환한다.
- 고정 ogen을 통해 request의 nullable integer enum을 실제 생성했다. 처음의 anyOf 바깥 enum은 생성기가 보존하지 않아 non-null branch로
  옮겼다. 별도 client의 실제 HTTP는 선택값·null·생략과 잘못된 enum cast의 서버 거부를, 독립 wire 응답은 목록 밖 값과 int64 극값을 검증한다.
  Parent는 DB의 최종 행·관계·정수·Text·시각을 별도로 검사한다. Client의 encoder는 Validate를 자동 호출하지 않는다.
- Admin의 option/value/label escaping, 목록 밖 초기값 보존, 거부 입력의 무변경, 저장·audit의 raw 값 보존을 검사했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.

### Hosted ORM 통합 검증

- 최초 [run 35401098373](https://github.com/progresshans/godj/actions/runs/35401098373), source `e5688068ea64ac55493ccbbe0d622ccdd084847b`는 실패를 발견한 뒤
  수정 source의 실행으로 대체되어 최종 cancelled다. 외부 migration adapter의 `.go.txt` fixture에 새 `AlterField` 메서드가 빠져
  Linux/macOS normal·CGO0 관계 작업 8개와 최종 scope 검사 1개가 실패했다. 기존 전체 compile은 테스트가 동적으로 만드는 이 소비자를 실행하지 않았다.
- 이 fixture에 명시적 미지원 오류를 반환하는 메서드를 추가했다. `go test -json -count=1 -timeout=10m ./internal/compiletest`와
  동일한 CGO0 명령에서 각각 **54 test 완료 PASS, skip 0**을 확인했다. 수정 commit은 이 fixture 한 파일의 3줄 추가뿐이다.
- 수정 source `d0f079481d660682c7a18884ce2c305234f6ad38`, [run 35401719591](https://github.com/progresshans/godj/actions/runs/35401719591), attempt 1:
  **completed/success, 고유 job 48개 중 44 success·4 의도한 skip**. 모든 job의 terminal 상태와 정확한 source를 확인했다.
- 최종 job `105787183637`의 report는 `scope=orm`, `full_platform_verified=false`이며 소유자는
  `command-product-matrix`, `portable-go-matrix`, `postgresql-product`, `relation-product-matrix`다.
  실제 PostgreSQL 17.10, Linux amd64/arm64 및 macOS arm64/amd64의 선택된 normal·race·CGO0 제품/command 검증을 포함한다.
- 비대상 네 owner는 product project-check matrix, Python compatibility matrix, exact darwin/arm64 reference profile,
  reference/current-capture 통합이다. Windows runtime 또는 새 full 검증으로 표시하지 않는다.

### 통합 검증 소유자

이 작업의 로컬 및 수정 source의 Hosted ORM 검증을 완료했다.
전체 플랫폼·reference·cold-build의 새 full 검증이나 배포 증거는 아니다. Callable/grouped choices, 다른 scalar choice,
Python enum 내부 ABI, general physical AlterField와 전체 모델 validation은 이 작업으로 완료되지 않는다.

## GDJ-0075 — Scalar IN과 빈 조회의 실행

- 작업: [GDJ-0075](../../work/0075-scalar-membership-and-empty-query-semantics.md), 의미: [ADR-0062](../adr/0062-scalar-membership-and-empty-query-execution.md).
- Source: `8fd8936d634b5038a534936c15a2b1cfac4b853b` 기반 작업 사본. Markdown을 제외한 변경·새 파일 29개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `e3394ceec710e1e56f6271519673e2028a2121db48941ac9e22351b832450863`이다.
  실제 generated consumer 입력·Python runner·독립 JSON fixture를 포함하고 모든 checkpoint 동안 이 bytes를 유지했다.
- 환경: Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5 (Homebrew). 새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했다.
  로컬 lane 종료 뒤 이 전용 DB만 제거했다. 기존 개발 DB와 checked-in generated Go·migration은 변경하지 않았다.

### 로컬 실행과 실패 수정

`GODJ_REQUIRE_POSTGRES=1`로 실제 PostgreSQL을 필수화하고 `go test -json -count=1 -timeout=15m`을 실행했다.

```text
./query ./orm ./db/... ./codegen/...
./conformance/relationproduct ./conformance/relationqueryproduct ./conformance/relationprefetchproduct
./conformance/relationselectproduct ./conformance/relationobjectproduct ./conformance/relationreverseproduct
./conformance/relationdeleteproduct ./conformance/postgresproduct ./examples/...
```

첫 checkpoint는 SQLite 일반 Atomic 경로에서 빈 조회가 실제 SELECT를 실행하는 누락을 발견했다. 해당 실행 경계에도 전체 compile 뒤
empty rows 처리를 연결했다. 빈 조건과 미지원 relation의 결합은 compiler보다 앞선 AST 구성에서 이미 오류이므로 새 회귀의 기대 시점을
그 계약에 맞췄다. 추가 검토에서 synthetic cursor를 원래 transaction lifetime에 묶어 detached Query context가 취소를 우회하거나
transaction 종료 뒤 결과를 읽는 일을 막았다. 실패한 중간 실행을 PASS로 합치지 않고 완성된 묶음의 위 normal 범위를 다시 실행했다.

최종 normal은 **27 packages, 2,273 test 완료 event PASS**, no-test package 11개다. 같은 source의 다음 범위를 race와 CGO0로 실행했다.

```text
./query ./orm ./db/... ./codegen/...
./conformance/relationprefetchproduct ./conformance/relationqueryproduct ./examples/helpdesk
```

`go test -race -json -count=1 -timeout=15m`과 `CGO_ENABLED=0 go test -json -count=1 -timeout=15m`은 각각
**11 packages, 2,089 test 완료 event PASS**, no-test package 2개다. 모든 JSON event의 시작/종료·필수 test·package를 대조했고 fail·잘린 로그는 없다.
세 mode의 유일한 test skip은 부모가 별도 process로 실행하는 `TestPostgresRevisionFenceHelperProcess`의 직접 진입이다.
필수 membership·generated consumer는 skip 없이 완료됐고 helper skip은 기능 PASS로 세지 않는다.

### 검증한 의미와 한계

- 독립 Django 6.1/UTC/SQLite의 여섯 field × 다섯 목록 × 네 Boolean 구성, **120개** 결과·SELECT 수를 보존했다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7의 locked Django/DRF/asgiref/sqlparse 환경에서 각 **1 test PASS, skip 0**로 fixture 전체를 다시 관찰했다.
- 외부 module에 실제 생성한 모델의 dynamic 120개·typed 84개를 실제 SQLite 결과와 QueryCount로 대조했다. Typed concrete slice에 없는
  explicit NULL member는 dynamic으로 검사하며 이를 typed 실행으로 세지 않는다. 필수 child test 네 개가 각각 정확히 한 번 완료돼야 통과한다.
- 실제 PostgreSQL의 120개 결과·model SELECT 수가 같은 reference와 일치했다. Trace는 SQL/credential을 보관하지 않고 호출 수만 센다.
  Empty source는 checkout/profile SQL까지 0회였으며 COUNT 0·MIN/MAX NULL도 driver 호출 없이 반환했다. 일반 nonempty query의 profile 검증 SQL은
  model SELECT 수와 구분했다. 고정 Django의 PostgreSQL 관찰 전체를 재현했다는 주장은 아니다.
- Caller slice·accessor·cached nullable pointer 소유권, derived query의 독립 cache, int64·UTC 연도 경계, NULL/empty와 nullable NOT을 확인했다.
  All/Count/Exists/ordered First/At/Iterate/projection/aggregate, invalid list·policy 우선순위·field/order/relation 오류·취소·closed/quarantine을 검증했다.
- SQLite Atomic/CoordinatedAtomic/AtomicRelation과 PostgreSQL Atomic/CoordinatedAtomic의 empty query·expired session·detached context 취소·
  종료 후 synthetic cursor 차단을 실제 transaction에서 확인했다. BEGIN/COMMIT/coordination lock 자체의 무 I/O를 주장하지 않는다.
- 0/NULL aggregate scanner는 실제 `database/sql` SQLite와 numeric alias·pointer·Scanner·string/bytes·지원하지 않는 destination의 오류를 대조했다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.

### 통합 검증 소유자

통합 source는 `adb3ea62f7c8f9a57c623634e2c11b60f04374bc`다. Merge 뒤 로컬 checkpoint의 29개 파일 hash가 모두 같음을 확인했다.
[Hosted ORM 실행](https://github.com/progresshans/godj/actions/runs/35392098111)의 attempt 1은 **completed/success**다.
서로 다른 job 48개 중 **44개 success**, scope 밖 4개는 계획된 skip이다. 실패·취소·미완료 job은 없다.
`CI result (orm)` 실제 로그의 `scope=orm`, `full_platform_verified=false`와 네 owner
`portable-go-matrix`·`relation-product-matrix`·`command-product-matrix`·`postgresql-product`의 성공을 확인했다.

실제 범위는 Linux/macOS amd64·arm64의 선택된 relation/command 제품과 Linux portable normal/race/CGO0, PostgreSQL 17.10의 실제 제품이다.
비대상 skip은 project-check matrix·Python compatibility matrix·exact Darwin reference·current capture reference 통합이다.
이 네 범위를 이번 실행의 PASS로 표현하지 않는다. Windows runtime과 전체 reference/platform/cold-build를 요청한 `full`은 아니며,
기존 Text+DateTime full은 아래 GDJ-0074의 source에만 적용된다. 후속 GDJ-0076 작업 사본의 choices 구현도 이번 PASS에 포함하지 않는다.

## GDJ-0074 — DateTimeField와 UTC 시각 값

- 작업: [GDJ-0074](../../work/0074-datetime-field-and-model-time-values.md), 의미: [ADR-0061](../adr/0061-datetime-field-and-canonical-instant-values.md).
- Source: `d3cb0a9cf1294efbddc7aee34cdef8cafecc9837` 기반의 2026-09-19 작업 사본. Markdown을 제외한 변경·새 파일 98개의
  `<sha256>  <relative-path>\n` 정렬 manifest SHA256은 `d26a9de41607bb9e0fadfdce3b169c82d2e92ad39b790bdb91c4aaefa0f39b38`이다.
  HTML template, Python 관찰기/fixture, migration JSON과 실제 생성 Go/client도 포함한다.
- 환경: Go 1.26.5 darwin/arm64, modernc SQLite, PostgreSQL 17.5 (Homebrew), locked Django 6.1/Python 3.14.3.
  새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했고 모든 로컬 lane 종료 뒤 전용 DB만 제거했다.
  기존 개발 DB와 Helpdesk 0001·0002·0003 migration은 변경하지 않았으며 Git 원본 bytes와 대조했다.

### 로컬 실행과 실패 수정

`GODJ_REQUIRE_POSTGRES=1`과 전용 DB URL로 다음 관련 범위의 `go test -count=1 -json -timeout=15m`을 실행했다.

```text
./schema/... ./internal/temporal ./query ./orm ./codegen/... ./migrations/... ./db/...
./forms/... ./serializers ./admin ./api/... ./examples/helpdesk/... ./examples/article/...
./internal/migrationautodetect ./internal/projectgenerate/... ./internal/projectmigration/...
./conformance/definitionload ./conformance/relationproduct ./conformance/runners/godj
```

첫 checkpoint는 Form 공백/24시 처리 불일치, nullable OpenAPI branch를 잘못 읽은 새 테스트, 외부 client의 소수초 손실을 발견했다.
빈 문자열만 NULL로 처리하고 Form의 유효한 24시를 다음 날 자정으로 정규화했다. OpenAPI 테스트는 실제 null/string branch를 확인하도록
수정했다. 고정 ogen v1.24.0의 `json.EncodeDateTime`은 RFC3339 layout으로 소수초를 생략하므로, 표준 date-time과 지원되는
`x-ogen-time-format` RFC3339Nano를 실제 문서에 게시하고 재생성했다. Consumer 기대값을 초 단위로 완화하지 않았다.

영향받은 temporal/forms/query/OpenAPI/Helpdesk/client 패키지 전체를 재실행했으며 최종 패키지별 normal 결과는
**42 packages, 3,984 test 완료 event PASS, fail 0**, no-test package 13개다. 뒤이어 ordered scalar 오류 설명의 잘못된 Integer/String
표현만 정리했고 query 패키지는 normal/race/CGO0 모두 다시 통과했다.

다음 관련 범위를 `go test -race -count=1 -json` 및 `CGO_ENABLED=0 go test -count=1 -json`으로 실행했다.

```text
./schema/... ./internal/temporal ./query ./orm ./codegen/... ./migrations/definition
./db/sqlite ./db/postgres ./forms/... ./serializers ./admin ./api/openapi
./api/openapi/consumertest ./examples/helpdesk ./internal/migrationautodetect
```

각 mode는 **18 packages, 2,690 test 완료 event PASS, fail 0**, no-test package 1개다. Event 수는 하위 test를 포함한다.
Normal의 `TestPublicationCrashHelper`·`TestPostgresRevisionFenceHelperProcess`, race/CGO0의 PostgreSQL helper는 직접 실행하지
않는 부모 진입에서 skip한다. 실제 부모가 별도 helper process를 실행해 통과했고 이 skip은 기능 PASS로 세지 않았다.

### 검증한 의미와 한계

- IR/default/historical codec: offset·monotonic 정보와 미세 자릿수를 제거한 default identity, UTC 연도 1·9999, 잘못된 scalar arm/비정규
  문자열 거부. SQLite DATETIME와 PostgreSQL timestamptz DDL/catalog, application default와 영속 SQL DEFAULT의 분리를 확인했다.
- 실제 생성 외부 model consumer: required/default/nullable/time.Time zero, epoch 이전 시각·양 끝 연도·microsecond ordering, typed/dynamic/F
  query, nullable projection/scan·Min/Max/empty aggregate, cache pointer 분리, Create/Patch 정규화·Save mask·취소·I/O 전 invalid 거부를
  실제 SQLite에서 확인했다. `Time`·`TimeValue` 필드가 Go import 이름과 충돌하지 않고 필수 child test 2개가 각각 완료되어야 한다.
  Linux/386/CGO0 generated model cross-compile도 통과했으며 386 runtime을 실행한 것은 아니다.
- 생성 forward/reverse 관계의 nonnullable DateTime exact binding을 실제 consumer에서 확인했다. 새 arbitrary relation lookup이나
  nullable terminal 지원을 주장하지 않는다.
- Helpdesk 양 DB: 0001의 기존 행 → 0003 → 새 0004 → 0001 reverse → 0004 재적용, 기존 값·NULL backfill·reopen 보존.
  API offset/nanosecond 입력과 canonical 응답, Admin의 연도 1 표시·연도 9999 변경·blank NULL·invalid calendar 뒤 DB 보존,
  실제 nullable MIN/MAX와 기존 권한·CSRF·4096 byte 제한을 확인했다. 실제 브라우저 전체 E2E는 별도다.
- 독립 ogen module의 **15개 필수 check**가 실제 HTTP·DB에서 offset·precision·연도 1/9999·생략/null을 포함해 완료했다.
  실제 문서와 locked generator의 생성물 drift를 확인했으며 Article 문서·client lock·ogen 설정은 보존했다.
- 고정 Django live fixture 비교 **1 test PASS**. Go Form의 **46개** 입력은 값·오류 code·widget을 비교했고, NUL suffix **2개**는
  실제 reference 결과를 보존한 [DEV-0011](../DEVIATIONS.md#dev-0011--datetime-입력의-nul을-거부하고-문자열-전체를-해석) invalid 회귀로 구분했다.
  이 2개는 Django parity PASS가 아니다. Locale/DST 전체·date transform·자동 시각 default는 여전히 미완료다.
- 전체 compile (`go test -run '^$' ./...`), `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.
  Helpdesk `makemigrations`는 `status=clean`, candidate 0이었다.

### 누적 통합 검증

GDJ-0073 Text와 GDJ-0074 DateTime의 Hosted full milestone을 선택했다. 이 실행이 전체 플랫폼·고정 PostgreSQL 17.10·
process/reference·cold-build를 소유한다. 첫 구현 source `cea93c5dd00b50e9d0256ba764faa3554ff58b13`의
[Hosted 실행](https://github.com/progresshans/godj/actions/runs/35383582028)은 Python 3.13.15 lane에서 DateTime reference 비교가 실패했다.
고정 3.14.3에서 관찰한 24시 수용을 호환성 runtime에도 그대로 요구한 테스트의 profile 오류다.

동일한 고정 Django/DRF/asgiref/sqlparse 의존성으로 Python 3.12.13·3.13.15·3.14.3·3.14.7을 각각 직접 실행했다.
3.12/3.13은 required/optional `24:00:00` 2개를 invalid로 거부하고, 나머지 46개는 고정 fixture와 같았다. 3.14.3/3.14.7은 48개 모두 같았다.
Compatibility test는 알려진 두 runtime의 2개 expected 결과만 명시적으로 선택하고 전체 roster를 계속 비교한다.
수정 뒤 네 버전 모두 해당 unittest **1 test PASS, skip 0**이다. Go 제품·고정 reference fixture는 바꾸지 않았으며 기존 Go 실행 bytes도 유지한다.
실패를 확인한 이전 run의 남은 작업은 취소하고 수정 source로 full을 다시 실행한다. 이전 run이나 b43552a의 결과를 새 source의 PASS로 사용하지 않는다.
수정 source는 `8fd8936d634b5038a534936c15a2b1cfac4b853b`이며 [재실행 full](https://github.com/progresshans/godj/actions/runs/35384697050)의
attempt 1은 **completed/success**다. 서로 다른 **62개 job**이 모두 completed/success이고 실패·취소·skip job은 없다.
`CI result (full)`의 실제 report에서 `full_platform_verified=true`와 전체 선택 owner의 성공을 확인했다.
같은 run의 `systemstate-postgres-1`·`operator-postgres-1` capture 두 개가 게시됐고, reference job이 현재 source와 producer provenance를 검증해 소비했다.

이 full은 누적 Text/DateTime의 Linux/macOS·normal/race/CGO0·32-bit compile·고정 PostgreSQL 17.10·exact Darwin·Python compatibility·
process·cold-build와 reference 통합 범위를 완료했다. Python compatibility 네 버전도 모두 terminal success다.
실제 CI runner는 Linux/macOS이며 Windows runtime 검증은 포함하지 않는다. 이전 완료 기록의 Windows 포함 표현을 실행 roster에 맞게 정정했다.
완료 기록 이후의 Markdown 변경이나 별도 GDJ-0075 작업 사본의 IN 구현을 이 full source의 검증으로 합치지 않는다.

## GDJ-0073 — TextField와 여러 줄 Form/Admin 입력

- 작업: [GDJ-0073](../../work/0073-text-field-and-multiline-model-forms.md), 의미: [ADR-0060](../adr/0060-text-field-and-form-widget-semantics.md).
- Source: `b43552a1f88259babe97ec9fe83951f8cd205261` 기반의 2026-09-19 작업 사본. 중간 `fa74d0e`는 문서만 바꾼 commit이다.
  최종 변경·새 파일 중 Markdown을 제외한 73개 파일의 `<sha256>  <relative-path>\n` 정렬 manifest SHA256은
  `589be36af37d844dc66a5b5b2314da1a735011ddd4ccb334623712820285fb7c`다. HTML template, Python 관찰기·fixture와 생성 JSON·Go도 포함한다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite, PostgreSQL 17.5. 새 전용 PostgreSQL DB와 테스트별 schema·임시 SQLite를 사용했고
  검증 뒤 작업 전용 DB만 제거했다. 기존 개발 DB와 Helpdesk 0001·0002 migration은 바꾸지 않았다.

### 관련 실행과 실패 수정

`GODJ_REQUIRE_POSTGRES=1`, 전용 `GODJ_TEST_POSTGRES_URL`로 normal은 다음 범위를 실행했다.

```text
./schema/... ./orm ./query ./codegen/... ./migrations/... ./db/sqlite ./db/postgres
./forms/... ./admin ./serializers ./api/... ./examples/helpdesk/...
./examples/article/adminapp ./examples/article/apiapp ./internal/migrationautodetect
./internal/projectgenerate/... ./internal/projectmigration/...
```

첫 실행은 DB 주소에 host가 빠져 backend URL 검증에 거절됐고, 새 테스트가 기존 관계 terminal에 없는 IContains와
serializer default의 공백 보존을 가정해 실패했다. DB 주소를 명시적 localhost URL로 수정하고 기존 implicit-exact 및
serializer normalization 의미에 맞게 테스트를 고쳤다. 제품 정책을 테스트에 맞춰 완화하지 않았다.
실패한 `db/postgres`, Helpdesk, serializers, codegen/consumertest 패키지를 각각 전부 재실행했다.
추가로 raw textarea의 선행 newline·HTML escaping·invalid Form 뒤 DB 보존과 실제 본문 수정을 보강해 Helpdesk를 세 모드로 재실행했다.
각 패키지의 최종 결과로 normal은 **29 packages, 3,427 test 완료 event PASS, fail 0**, no-test package 6개다.

다음 범위를 `go test -race -count=1 -json`과 `CGO_ENABLED=0 go test -count=1 -json`으로 실행했다.

```text
./schema/... ./orm ./codegen ./codegen/consumertest ./migrations/definition
./db/sqlite ./db/postgres ./forms/... ./admin ./serializers
./api/openapi/consumertest ./examples/helpdesk ./internal/migrationautodetect
```

두 모드 모두 최종 **15 packages, 2,357 test 완료 event PASS, fail 0**이다. 위 event 수는 하위 test를 포함한다.
Normal의 `TestPostgresRevisionFenceHelperProcess`·`TestPublicationCrashHelper`, race/CGO0의 PostgreSQL helper는 직접 호출하지
않는 parent 진입에서 skip했다. 실제 부모 테스트는 별도 helper process를 실행해 통과했고 이 skip을 기능 PASS로 세지 않았다.

### 검증한 의미

- Schema/codec의 Text kind·nullable·긴 문자열/빈 default 보존, 잘못된 length/default/PK 거부. 양 DB TEXT DDL에는 영속 DEFAULT가 없고
  PostgreSQL catalog는 varchar·length·nullability·identity·persistent default drift를 거절했다.
- 실제 생성된 별도 module의 Text consumer는 required/default/empty/null, 긴 Unicode 본문 저장, typed/dynamic/F query,
  nullable projection·Max aggregate, cache 포인터 분리와 Create/Patch/Save mask를 확인했다. 필수 child test 두 개가 각각
  정확히 한 번 완료되어야 하며 skip·실패·잘린 출력·stderr는 거절한다. Forward/reverse 생성 관계의 nonnullable Text exact binding도 검증했다.
- Helpdesk 양 DB: 0001에 기존 행 입력 → 0002 → 0003 → 0001 reverse → 0003 재적용, 기존 값과 NULL backfill 보존.
  권한·CSRF·재시작, 잘못된 Text 타입/NUL·4096 bytes 초과 거부, 긴 본문의 API→DB→textarea→Admin 수정,
  빈 Form Text의 빈 문자열과 integer Null 구분을 확인했다. 실제 브라우저 전체 E2E를 실행했다는 주장은 아니다.
- 외부 ogen v1.24.0 module: 실제 HTTP와 DB를 확인하는 **14개 필수 check**, 문서·생성물 drift, multiline/HTML 본문과
  생략/null/empty string을 검증했다. Article 문서와 client 의존성 lock은 변경하지 않았다.
- 고정 Django 6.1 Char/Text × nullable × required의 **104개 관찰값**과 Go cleaning·widget·오류 code가 일치했다.
  Python live fixture 비교 1 test PASS. 같은 locked 환경의 Python suite는 **276 tests 중 269 PASS, 7 skip**이다.
  4개 capture/profile 요구와 설치되지 않은 DRF를 요구하는 3개 test는 이 실행의 검증 범위가 아니다. 전체 reference 통합 PASS로 쓰지 않는다.
- `go test -run '^$' ./...` 전체 compile, `go vet ./...`, `make generate-check`, `make docs-check format-check`, `git diff --check` PASS.
  Helpdesk `makemigrations` 재실행은 `status=clean`, candidate 0이었다.

최종 구현 commit은 `fca8cbfa38c072c9f8825f000a90f206ce291ddf`이며 로컬·원격 source가 일치했다.
[해당 source의 Fast feedback](https://github.com/progresshans/godj/actions/runs/35377057526)은 completed/success다.
문서-only 선행 commit 위로 통합하면서 제품·테스트 파일의 위 manifest가 그대로 유지됨을 확인했다.
이번 작업의 새 Hosted full은 실행하지 않았다. 이전 `b43552a` full 결과는 GDJ-0072까지의 근거이며 위 Text 변경의 platform PASS가 아니다.
후속 구현과 누적 변경의 영향에 맞춰 별도 통합 milestone을 선택한다.

## GDJ-0072 — 일반 정수와 기존 Helpdesk 모델의 성장

- 작업: [GDJ-0072](../../work/0072-integer-field-model-growth.md), 의미: [ADR-0059](../adr/0059-signed-integer-field-and-model-growth.md).
- 로컬 source: `8ade467afe918474e9c42fd066edbfe7972ee600`에 이번 변경을 적용한 2026-09-19 작업 사본.
  변경·새 파일 중 Markdown을 제외한 100개 파일의 `<sha256>  <relative-path>\n` 정렬 manifest SHA256은
  `e06a8538d34c60b175ea14e7f4376ade3fe479575b21b2d602ea3a9ce55f7c8a`다. HTML template과 생성 JSON·Go도 포함한다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite, 로컬 PostgreSQL 17.5. 새 전용 PostgreSQL DB와 테스트별 schema,
  임시 SQLite를 사용했다. 기존 개발 DB와 Helpdesk `migrations/0001_initial.godj.json`은 변경하지 않았다.
  검증을 마친 작업 전용 PostgreSQL DB는 제거했다.

### 로컬 관련 검증

다음 범위를 `GODJ_REQUIRE_POSTGRES=1`, 전용 `GODJ_TEST_POSTGRES_URL`로 `go test -count=1 -json -timeout=15m` 실행했다.

```text
./schema/... ./orm ./forms/... ./serializers ./admin ./codegen/...
./migrations/... ./db/... ./query ./internal/migrationautodetect ./internal/compiletest
./api/... ./examples/article/... ./examples/helpdesk/... ./conformance/relationproduct
```

첫 실행에서 standalone 관계 product의 생성물 갱신 누락과 새 관계 consumer 테스트의 지원 밖 비교 연산 사용을 발견했다.
실제 generator로 누락 파일을 재생성하고 기존 implicit-exact 의미에 맞게 테스트를 수정했다. 역방향 정수 terminal compile도 보강한 뒤
`./codegen/consumertest ./conformance/relationproduct` 전체를 재실행했다. 나머지 패키지 소스에는 이후 동작 변경이 없다.
최종 패키지별 결과는 **35 packages, 3,174 test 완료 event PASS(하위 test 포함), fail 0**이다. 13 packages는 `[no test files]`다.
유일한 test skip은 직접 실행용이 아닌 `TestPostgresRevisionFenceHelperProcess`의 parent 진입이다. 실제 교차 process 부모 테스트는
helper 전용 환경·pipe로 child를 실행하고 통과했다. 이 skip을 기능 검증 PASS로 세지 않았다.

- 신규 generated integer consumer: 별도 module에서 required/default/min/max/null/zero, typed·dynamic·F query, nullable projection·Min/Max,
  cache 포인터 분리, Create/Patch/Save mask·취소·쓰기 전 실패를 실제 SQLite와 검증했다. 필수 child test 두 개가 각각 정확히 한 번
  완료되어야 하며 skip·실패·잘린 출력·stderr는 거부한다. linux/386/CGO0 generated model **cross-compile**도 통과했다. 386 runtime 주장은 아니다.
- Helpdesk: SQLite·PostgreSQL에서 실제 0001 schema에 행을 만든 뒤 0002 적용·reverse·재적용, reopen·권한 교체와 Admin/API 흐름을
  검증했다. 기존 row의 값과 새 nullable priority, int64 양 끝·0·null 입력, 잘못된 JSON 숫자·타입과 권한 거부 뒤 DB 보존을 확인했다.
  Admin의 정확한 정수 렌더링과 null/zero 변경도 포함한다.
- 외부 ogen v1.24.0 consumer: 실제 문서·offline 재생성·독립 module build·HTTP와 DB 결과를 함께 확인했다. Helpdesk 생성 요청의
  생략/null/0/최소/최대 정수와 관계 범위·권한을 추가해 **13개 필수 check**를 확인한다. Article 문서와 client 의존성 lock은 변하지 않았다.
- 고정 Django 6.1의 `BigIntegerField.formfield()`에서 required/optional **90개 관찰값**을 생성했다. Go Form이 같은 입력·정수·null·오류
  코드를 통과했고 Python의 live 관찰/저장 fixture 비교 **1 test PASS**를 확인했다. 기존 전체 conformance corpus에 새 정수 contract가
  등록됐다는 주장은 아니다.
- `make generate-check`: Helpdesk·Article·관계 fixture 및 standalone 관계 product drift PASS.
  Helpdesk `makemigrations` 재실행은 `status=clean`, candidate 0이었다.
- 관련 `go vet`, `make docs-check format-check`, `git diff --check` PASS. 현재 Markdown 99개 local link를 검사했다.

### 통합 검증 소유권

누적 GDJ-0070/0071/0072를 묶은 Hosted full이 OS·race·CGO0·고정 PostgreSQL·process/reference 통합을 소유한다.
로컬 전체 matrix는 반복하지 않는다. 첫 [Hosted 실행](https://github.com/progresshans/godj/actions/runs/35369545141)은
source `48165d8fc7b7d3d8412fe75784ac10e7c4ed1873`에서 이전 GDJ-0071 API 변경의 네 consumer 갱신 누락을 발견했다.
`apiapp.Middleware()`가 제거됐는데 reference fixture·distinct-process worker·외부 operator runner가 남은 호출을 사용해 compile에 실패했다.
같은 원인 확인 후 나머지 실행을 취소했으며 이 run은 통합 PASS가 아니다.

네 consumer 모두 실제 API 인스턴스의 `Middleware()`로 연결하고 불필요한 wrapper를 제거했다. 호환 shim을 추가하지 않았다.
수정 후 `go test -run '^$' ./...` 전체 compile과 `go vet ./...`을 통과했다. 관련 GDJ-0044/0047 API/auth reference,
process worker·외부 SQLite operator의 기존 실제 흐름을 재실행해 **3 packages, 63 test PASS, skip/fail 0**을 확인했다.
수정 source `b43552a1f88259babe97ec9fe83951f8cd205261`의
[Hosted full 35370184198](https://github.com/progresshans/godj/actions/runs/35370184198)은 최종 attempt 2에서 **completed/success**다.
62개 고유 job 모두 같은 source·run에 묶인 completed/success이며, 최종 aggregate는 `scope=full`,
`full_platform_verified=true`와 8개 필수 owner를 확인했다. 로컬·원격 브랜치 source도 일치했다.

Attempt 1에서는 macOS Intel normal command job의 `go mod tidy`가 `proxy.golang.org`의 checksum 서버 연결 timeout으로 실패했다.
소스를 바꾸거나 checksum 검사를 끄지 않고 `gh run rerun --failed`로 실패 항목 재실행을 요청했다. 재실행한 command job은
operator·targeted migrate 양쪽의 필수 실행을 확인했고 최종 aggregate도 성공했다. GitHub의 최종 attempt job 목록에는 이전 성공 결과가
포함되므로 모든 성공 검사를 새로 중복 실행했다고 표현하지 않는다.

- Portable normal/race/CGO0, 관계·project-check·명령의 Linux/macOS·amd64/arm64 matrix와 PostgreSQL 17.10 여섯 조합이 완료됐다.
- API/client의 normal/race/CGO0, 정수 generated consumer와 32-bit compile, 실제 DB·process와 필수 sentinel은 해당 실행 owner가 검증했다.
- 고정 Darwin Python **275 tests·skip 0**, normal reference **275 tests·profile 소유 skip 4**, 네 Python 버전의 compatibility 검증이 통과했다.
- 현재 run/source의 system-state와 operator capture를 reference job이 검증·소비했다. 두 producer는 성공한 attempt 1이며,
  최종 attempt에서 그 provenance를 보존한다. 다른 source의 capture나 과거 `b74a79e` 결과로 대체하지 않았다.
- 후속 TextField 작업은 별도 worktree의 미완성 변경이며 이 full PASS의 대상이 아니다.

## GDJ-0071 — schema 정체성·JSON 정책과 실제 생성 client

- 작업: [GDJ-0071](../../work/0071-api-schema-identity-and-generated-client.md), 설계: [ADR-0058](../adr/0058-model-derived-openapi-and-operation-ownership.md).
- 기준 commit: `f7db3ed1ab0e7fcdedd9f4af0c1f760893bad823`에 이번 변경을 적용한 2026-09-12 작업 사본이다.
  해당 기준 commit 자체나 이전 Hosted 결과를 이 변경의 PASS로 표시하지 않는다.
- 최종 변경 source 입력 80개의 SHA256 manifest digest: `b452bb6f6eb748d206434bace1a81c83cba40b12d98540ce1e7a30e1e012f69e`.
  기준 commit 대비 변경·새 Go/Python/JSON/YAML 파일과 Makefile·go.mod·go.sum 경로를 정렬하고,
  각 `<file-sha256>  <relative-path>\n` 행을 이어 SHA256으로 계산했다. 문서·license는 이 digest에서 제외했다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite, 로컬 PostgreSQL 17.5. 새 전용 PostgreSQL DB와 테스트별 schema,
  임시 SQLite 파일을 사용했다. 기존 개발 DB를 변경하지 않았고 작업 전용 PostgreSQL DB는 검증 후 제거했다.

### 구현과 위험의 소유권

- `JSONPolicy`의 middleware와 문서가 실제 subtree 적용을 공유한다. Zero policy와 Helpdesk에는 자동 406이 없고,
  dynamic route 일부에만 적용되는 prefix는 명시적으로 실패한다. Article의 기존 협상·routing error 동작을 유지한다.
- 명시적 named schema와 local reference를 그대로 출력한다. 중복·미해결·순환·지원 외 참조, depth/node/byte budget,
  property 이름과 참조의 구분, 불변 snapshot과 결정성을 검증했다. 공통 오류 component의 재선언은 거부한다.
- 네 schema 연결 위치의 참조 검사, 공유 auth/406 실패 status의 필수 application header 거부,
  root error alias와 공통 오류의 일치, 빈 문서/부분 게시 방지를 확인했다.
- Helpdesk는 단일 API 구성에서 route와 문서를 만든다. 실제 encoder/input Spec, bare list·nested category,
  선택 category 밖 ticket의 404, 생성 default·null·빈 문자열, 권한 선행과 기존 category/ticket 보존을 검증했다.

### 관련 Go와 실제 외부 client 실행

통합 범위는 `./api/... ./web/... ./examples/article/... ./examples/helpdesk`다. Test가 있는 **18 packages**에서
각 모드 **555개의 test 완료 event PASS(하위 test 포함), fail/실제 test skip 0**을 확인했다.
별도의 5개 package는 Go 소스만 있어 `[no test files]`이며 실행 누락된 test를 의미하지 않는다.

```sh
make api-client-dependencies
GODJ_REQUIRE_POSTGRES=1 go test -json -count=1 ./api/... ./web/... ./examples/article/... ./examples/helpdesk
GODJ_REQUIRE_POSTGRES=1 go test -race -json -count=1 ./api/... ./web/... ./examples/article/... ./examples/helpdesk
GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -json -count=1 ./api/... ./web/... ./examples/article/... ./examples/helpdesk
```

`GODJ_TEST_POSTGRES_URL`은 전용 DB로 설정했다. Normal 첫 실행에서는 generator의 일반 stderr 경고를 consumer runtime과
똑같이 실패로 처리한 harness 때문에 외부 consumer가 실패했다. 경고는 `WWW-Authenticate`를 Go의 canonical casing으로
다루는 도구 진단이었다. Tool의 일반 진단과 실행 consumer의 엄격한 stderr 계약을 구분하고 generation drift를 그대로 유지했다.
최종 document 연결 회귀도 포함해 `./api/openapi ./api/openapi/consumertest`를 다시 실행하여 **2 packages, 125 PASS**를 확인했다.
그 외 변경되지 않은 **16 packages, 430 PASS**는 첫 normal 실행 결과다. Race/CGO0는 최종 제품/test 소스로 전체 관련 범위를 실행했다.

외부 consumer는 GoDj를 import/replace하지 않는 별도 module에서 **ogen v1.24.0**으로 생성한 세 client를 사용한다.
실제 API 문서 세 개의 byte 일치, 고정된 schema/config/tool/lock의 offline 재생성, 정확한 generated 파일 집합·내용,
별도 executable build와 실제 HTTP를 같은 테스트에서 확인했다. 부모 race 실행에서는 consumer executable도 race로 빌드했다.

- 실제 HTTP: Article Bearer CRUD·PATCH omitted/null/empty/false·PUT default/보존·인증/인가 오류, Session cookie·CSRF CRUD와
  잘못된 CSRF, Helpdesk selected relation·생성 default·읽기 전용 거부, 사전 취소 요청과 최종 DB effects.
- 별도 wire fixture: int64 최대 path/response와 overflow 거부, 필수 nullable/read-only 필드 누락과 추가 응답 필드 거부,
  PATCH의 실제 serialized omitted/null/empty/false. 각 mock 응답은 정확히 한 HTTP 교환을 요구한다.
- 완료 보고: 12개 필수 check의 정확한 집합, 중복 JSON member·잘린/후행 보고·race 불일치 거부. 빈 파일도 경로 이름을
  검사하며 subprocess 실패·취소·출력 초과·성공 종료의 runtime stderr를 성공으로 취급하지 않는 부정 대조군을 포함한다.
- Session은 실제 adapter와 CSRF 교환을 쓰지만 parent가 메모리 session을 준비한다. 생성 SDK의 로그인 기능 검증은 아니다.
  생성기의 template 출처와 Apache-2.0 license는 client fixture에 보존했다. Root framework의 go.mod/go.sum은 바꾸지 않았다.

### 독립 규격·CI 연결·문서 검증

- 격리 `uv --no-project` 환경의 openapi-spec-validator **0.9.0**, jsonschema **4.26.0**, referencing **0.37.0**:
  OpenAPI 3.1.1 문서 **3개**, named schema **17개** 유효성, native local ref를 resolve한 수용/거부 **158사례**
  (수용 69/거부 89; Article profile별 59, Helpdesk 40), default annotation **7검사** PASS.
  `default`는 값을 삽입하지 않고, `x-godj-normalization`·parser lexical/byte·권한/DB 계약은 JSON Schema가 검사하지 않음을 확인했다.
- 검사한 문서 SHA256: Article Bearer `13733fb869ffdd8d2395bd0e457854b80226154401ced9d1d7a143e067d80847`,
  Article Session `a5a951276698455afd214e207a2e6c52681a11468c2f80a05f0be1f92bdaa333`,
  Helpdesk Session `0b09e0a2589fee4e93ba061026114f39a5556820a54138e2cb5980ea1d7ae6fb`.
- CI package/scopes의 Python 회귀 **12개 PASS**. 정확한 모듈 경로를 integration으로 분류하고 normal/race/CGO0 Make target이
  의존성 준비를 소유한다. 실제 `go list`에서도 consumer는 integration, nested client는 root package 목록 밖임을 확인했다.
  의존성 준비는 임시 복사본에서 수행해 lock 변화를 거부한다. Portable Go cache key에 client go.sum을 포함했다.
- 유지보수 export 명령의 별도 compile과 실제 실행 PASS. Article Bearer 24,491 bytes/10 operations,
  Article Session 21,331 bytes/10 operations, Helpdesk Session 7,105 bytes/3 operations가 저장된 입력과 byte-identical이었다.
  비어 있지 않은 출력으로 재실행하면 exit 1이고 기존 세 파일의 SHA256가 유지됐다. 갱신 절차는
  [consumer README](../../api/openapi/consumertest/README.md)에 있다. 이 명령은 기본 Go test package 집합 밖에서 별도로 검증했다.
- 관련 `go vet`, `make format-check docs-check`, `git diff --check`: PASS. 현행 Markdown **97개**의 local link를 검사했다.
- 현행 API와 문서, subprocess/receipt·CI/CLI의 독립 읽기 리뷰를 완료했다. 발견한 empty-file membership와 상속 `GORACE`에
  의한 child false PASS 가능성을 수정하고 해당 부정 대조군까지 실행했다.
- Hosted full matrix·Linux/다른 arch와 PostgreSQL 버전, 새 Django differential contract, 배포형 SDK·다른 언어 generator는
  이번 범위에서 실행하지 않았다. 외부 validator는 repository Python lock을 변경하지 않았다.

## GDJ-0070 — 모델과 실제 API 선언에서 OpenAPI 제공

- 작업: [GDJ-0070](../../work/0070-model-derived-openapi.md), 설계: [ADR-0058](../adr/0058-model-derived-openapi-and-operation-ownership.md).
- 기준 HEAD: `6d30973ae3034e16dc56b9d2b9e0a6faffe1fb49`에 이 작업의 미커밋 변경을 적용한 2026-09-12 작업 사본이다.
  위 HEAD 자체의 PASS나 Hosted 검증을 뜻하지 않는다.
- 최종 변경 Go 파일 20개의 SHA256 manifest digest: `ef9271c9bf993514f82939e087a90d13942b2cdb7bf8ed8f5d7af9dc77014a5a`.
  HEAD 대비 변경·새 Go 파일의 경로를 정렬하고 각 `<file-sha256>  <relative-path>\n` 행을 이어 SHA256으로 계산했다.
- 환경: darwin/arm64, Go 1.26.5, modernc SQLite와 로컬 PostgreSQL 17.5. 이 작업 전용 임시 PostgreSQL DB와
  테스트별 schema·임시 SQLite fixture를 사용했다. 기존 개발 DB는 변경하지 않았다.

### 구현과 위험 검증

- 같은 serializer Spec에서 full/partial 입력과 ModelEncoder 응답을 투영한다. Read-only·required/default·null·empty·
  Unicode 문자 길이, input trim 이후 제약과 untrimmed output 길이의 차이, field allowlist와 immutable snapshot을 검증했다.
- 실제 operation에서 route·permission·body·response를 연결한다. Web의 이름·경로 문법·교차 route language 충돌,
  OAS template 고유성, 잘못된 media type, profile 소유 header 충돌과 실패 시 부분 게시 없음의 회귀를 포함한다.
- 실제 Session/Bearer adapter의 공개 metadata와 custom cookie/header 정규화, description 중 인증·인가·entropy 작업 없음,
  unsafe Session의 세 조건 AND, Bearer challenge·JSON 오류와 HEAD/204/plain 500을 검증했다.
- 실제 `http.Client`와 임시 HTTP server로 문서의 입력·출력과 Article 생성·PATCH·HEAD 200/403/404/406·빈 query 응답을 대조했다.
  기존 site fixture에서 문서의 익명 403·로그인 후 200·secret 부재와 public-only 404를 확인했다.
  기존 CRUD·missing target 우선순위·CSRF·취소·audit·two-runtime 흐름도 아래 관련 package 범위에서 실행했다.

### 실행

첫 통합은 `./api/... ./serializers ./web/... ./examples/article/...`에서 normal/race/CGO-disabled를 실행했다.
Normal 최초 실행의 PostgreSQL 4개는 환경 미설정으로 skip이었다. 이후 전용 DB를 만들었으나 host 없는 URL은 GoDj의
configuration 검증에서 거부되어 PostgreSQL normal/race 4개가 각각 실패했다. Host를 포함한 URL로 수정하고 해당 네 흐름을
다시 실행해 모두 PASS를 확인했다. 이 설정 실패를 제품 회귀나 미실행 성공으로 세지 않는다.

최종 리뷰에서 발견한 응답 `maxLength` 누락을 고친 뒤 변경이 영향을 주는 OpenAPI와 Article 전체를 다시 실행했다.
아래 세 명령은 각각 **11 packages, 185 tests PASS, fail/skip 0**이다.

```sh
GODJ_REQUIRE_POSTGRES=1 go test -json -count=1 -timeout=10m ./api/openapi ./examples/article/...
GODJ_REQUIRE_POSTGRES=1 go test -race -json -count=1 -timeout=15m ./api/openapi ./examples/article/...
GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -json -count=1 -timeout=15m ./api/openapi ./examples/article/...
```

`GODJ_TEST_POSTGRES_URL`은 실행 전에 전용 DB로 설정했다. 최종 수정에서 바뀌지 않은 `api`, `api/sessionauth`, `api/bearerauth`,
`serializers`, `web`, `web/sessionauth`는 첫 통합에서 각 모드 **6 packages, 312 tests PASS, fail/skip 0**이다.
두 범위를 합쳐 각 모드 497개 test의 관련 위험을 검증했다. Article의 실제 생성물 drift·declaration bootstrap 회귀도 포함한다.

- `openapi-spec-validator==0.9.0` 격리 실행: 실제 Article Session/Bearer 구성에서 생성한 3.1.1 문서 두 개 모두 PASS.
  최종 문서는 각각 35,137/43,433 bytes, 10 operations다. Registry나 DB 내용을 문서에 넣지 않는다.
- 같은 격리 환경의 `jsonschema==4.26.0` Draft 2020-12 validator: 186개 schema 위치의 유효성과 50개 수용/거부 사례 PASS.
  Full/partial, readonly·누락·추가 필드, null·empty, 응답 Unicode 최대 길이, int64 범위, page와 오류 envelope를 확인했다.
  Fixture 사례는 실제 HTTP 검증과 구분하며 parser lexical/byte·trim 정책 전체의 동치 검증을 주장하지 않는다.
- 관련 `go vet`, `make docs-check format-check`, `git diff --check`: PASS. 문서 94개의 local link destination을 확인했다.
- 새 Go dependency·Django oracle lock·generated ABI 변경은 없다. 외부 validator는 프로젝트 Python lock에 추가하지 않았다.
- Hosted full matrix, 다른 OS/arch·PostgreSQL 버전, 새 Django differential contract, 별도 모듈 설치·생성 SDK와 외부 client generator는
  이번 범위에서 실행하지 않았다. 아래 과거 Hosted 전체 성공은 GDJ-0070 소스의 PASS가 아니다.

## 2026-09-12 — 통합 개발 경험 조사와 개발 기준

- 범위: [공식 문서·source 비교 보고서](../research/2026-09-12-framework-developer-experience.md)와
  [개발 판단 기준](../DEVELOPMENT_CRITERIA.md), 관련 현행 문서의 연결·상태 정리.
- 코드 읽기 기준: `6d30973ae3034e16dc56b9d2b9e0a6faffe1fb49`. 제품 코드·생성물·dependency·CI·conformance profile은 변경하지 않았다.
- `PYTHONDONTWRITEBYTECODE=1 python3 scripts/check_docs.py`: PASS, 92개 문서의 local link destination 확인.
- 보고서의 각주 56개: 참조·정의의 누락·중복·미사용 없음. 새 문서의 EOF·trailing whitespace·fence 정합 확인.
- `git diff --check`: PASS. Django/DRF, FastAPI/Template/Litestar, Ninja/Modern REST 비교의 독립 source 리뷰에서
  남은 실질적 오류를 발견하지 않았다. 진단 정보의 공개 대상과 기록 절차의 중복 가능성 두 문구는 보정했다.
- 비교용 앱 구현·실행, 개발 시간·성능 측정, Go/DB/race/platform·Hosted 검증은 이번 문서 작업에서 수행하지 않았다.
  공식 테스트 source의 기대값을 읽은 사실을 로컬 실행 PASS로 표시하지 않는다. 아래 GDJ-0069는 이전 제품 소스의 검증 기록이다.

## GDJ-0069 — 바인딩·쿼리 준비와 감사 후속 개선

- 작업: [GDJ-0069](../../work/0069-boundary-preparation-and-audit-followup.md).
- 기준: `71ba61f0ecf26d397b10ac207212146f27441de7`.
- 상태: F1~F8 구현·전후 측정·관련 로컬 통합과 동일 제품 소스의 Hosted full scope 완료.
- 검증 소유권: 묶음별 affected local checkpoint, 관련 DB·race·CGO-disabled 통합, 최종 제품 소스 Hosted full scope.
- 아래 GDJ-0068은 직전 완료 근거이며 GDJ-0069 변경 소스의 PASS가 아니다.

### 변경과 검증 대상

| 항목 | 최종 변경 | 보존하는 검증 |
|---|---|---|
| F1 | ReverseObject의 정적 검사를 Bind가 소유하며 prefetch는 canonical private field를 공유 | 공개 metadata 변조·descriptor snapshot·zero/nil·PK·callback field 변경·cold cache·취소·16개 동시 소비 |
| F2 | CheckHistory가 private immutable applied map을 읽음 | Plan의 별도 mutable 복사, unknown/history 오류 순서, 64 goroutine의 반복 CheckHistory와 forward/backward Plan |
| F3 | PostgreSQL IN 값을 compile-local leaf에 한 번 준비 | mixed IN/NOT/ISNULL·relation alias·model/projection/aggregate·재컴파일 인자 독립성·기존 오류 우선순위 |
| F4 | Article의 최대 6개 optional predicate를 한 번에 Filter | 기존 typed/dynamic AST와 유효 조건의 batch/chain 의미, 검색·페이지·정렬·SQLite/PostgreSQL HTTP 흐름 |
| F5/F6 | 경로 Count 전환, 미사용 regexp 제거 | 기존 root/malformed/segment bound와 wirejson 숫자 거부 |
| F7 | 두 Article PostgreSQL 준비와 두 CLI assertion helper 공유, 표준 slices.Equal | 독립 schema·flow·fixture hash/LoadReport·required DB·redaction·별도 oracle/actual·exact SQLite snapshot |
| F8 | canonical digest byte 검증으로 임시 decode 할당 제거 | 64개 위치 각각 모든 byte와 stdlib canonical hex 대조, 기존 손상·중복·전체 검증 뒤 만료 1행 삭제·cross-runtime fence |

검증 비용을 줄이기 위해 assertion·필수 DB·오류 검사를 삭제하지 않았다. F1의 storage.Field는 외부 상태를 볼 수 있는
callback이므로 매 Load에서 계속 검사한다. F4는 이미 검증된 최대 6개 predicate를 수집하며 한도 밖의 임의 chain/batch가
같은 오류 우선순위를 갖는다고 일반화하지 않는다. F7은 fixture와 DB 수명만 공유하며 oracle을 actual 생성에 전달하지 않는다.

### F1~F4 전후 측정

2026-09-10 KST, Go 1.26.5 darwin/arm64, Apple M3 Pro. 각 제품 변경 전에 같은 workload를 추가해 기준을 측정했다.
`-benchtime=200ms -count=3`의 중앙값이며 마지막 비교는 `-p=1`로 package를 순차 실행했다. DB·HTTP 전체 성능의 증거는 아니다.

```sh
go test -p=1 -run '^$' -bench 'Benchmark(PlannerCheckHistory|ReverseObjectFrom|ReversePrefetch|PostgresConditionCompilation|ConditionBatching)$' -benchtime=200ms -count=3 ./migrations ./orm ./db/postgres ./query
```

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| ReverseObject.From | 2,093 → 282.4 | 1,408 → 736 | 15 → 7 |
| ReversePrefetch / 20 owners·1,000 rows | 166,038 → 140,910 | 465,117 → 463,482 | 7,390 → 7,372 |
| CheckHistory / 32 applied | 2,391 → 1,383 | 2,728 → 0 | 3 → 0 |
| CheckHistory / 256 applied | 19,231 → 11,700 | 21,800 → 0 | 3 → 0 |
| CheckHistory / 1,024 applied | 90,187 → 51,232 | 98,384 → 0 | 5 → 0 |
| PostgreSQL full compile / exact | 488.9 → 490.9 | 504 → 520 | 13 → 13 |
| PostgreSQL full compile / IN 8 | 827.9 → 755.9 | 1,640 → 1,272 | 17 → 16 |
| PostgreSQL full compile / IN 256 | 10,948 → 9,121 | 43,112 → 29,560 | 180 → 179 |
| PostgreSQL full compile / IN 999 | 48,908 → 42,537 | 168,665 → 119,528 | 1,670 → 1,669 |

PostgreSQL 일반 exact 조건은 leaf 준비 공간이 16 bytes 늘었고 시간은 이 측정에서 비슷했다. IN 999의 49 KB는
전체 할당량이 아니라 제거된 두 번째 public Values 복사량에 해당한다. 공개 getter의 방어적 복사는 유지한다.
각 IN leaf의 snapshot은 컴파일이 끝날 때까지 보유하므로 여러 IN이 있으면 동시에 보유하는 복사 공간은 목록 길이의 합에
비례한다. 표는 한 IN의 총 할당량 측정이며 다중 IN의 peak memory 감소를 증명하지 않는다.

| 유효 조건 수 | chain → batch ns/op | chain → batch B/op | chain → batch allocs/op |
|---|---:|---:|---:|
| 8 | 951.8 → 502.1 | 2,456 → 1,488 | 22 → 12 |
| 64 | 11,110 → 3,278 | 35,416 → 10,896 | 190 → 68 |
| 256 | 78,062 → 12,848 | 353,563 → 43,920 | 766 → 260 |
| 1,023 | 932,708 → 52,908 | 4,764,445 → 172,033 | 3,067 → 1,027 |

이 비교는 같은 유효 Plan의 수집 방식 차이다. Immutable AST의 체인 구성 자체를 변경하지 않았고 새 전역 cache를 추가하지 않았다.

### F8 실제 DB 측정과 정책 결정

SQLite는 modernc v1.56.0의 실제 임시 파일과 `_busy_timeout=5000`, PostgreSQL은 기존 로컬 17.5 서비스에 새로 만든
전용 DB·개별 schema를 사용했다. 모두 framework migration으로 준비하고 실제 coordinated transaction을 실행했다.
PostgreSQL 17.10의 Hosted 환경과 이 로컬 버전을 구분한다. 양쪽 전후 각각 34 workload × 3회, 총 102 sample이 완료됐다.

```sh
GODJ_REQUIRE_POSTGRES=1 go test -run '^$' -bench 'BenchmarkSession(Capacity|Operations)Database$' -benchtime=200ms -count=3 -timeout=15m ./systemstate
```

`GODJ_TEST_POSTGRES_URL`은 실행 전 전용 DB를 가리키도록 설정했다. Capacity는 64/1024/4096 상한 각각 25%·full-live·full-expired를
측정한다. Full-expired의 fixture 복원은 timer 밖이며 측정에는 전체 검증·실제 1행 삭제·commit이 포함된다.

| 4096 상한 / occupancy | 기준 → 변경 ms/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| SQLite / 25% | 1.294 → 1.210 | 181,681 → 148,904 | 6,986 → 5,962 |
| SQLite / full-live | 18.580 → 17.028 | 15,306,836 → 15,044,732 | 241,287 → 233,095 |
| SQLite / full-expired | 18.152 → 18.037 | 15,308,345 → 15,046,251 | 241,303 → 233,111 |
| PostgreSQL / 25% | 0.747 → 0.702 | 183,647 → 150,881 | 7,025 → 6,001 |
| PostgreSQL / full-live | 14.734 → 14.393 | 15,308,844 → 15,046,713 | 241,316 → 233,124 |
| PostgreSQL / full-expired | 14.735 → 14.296 | 15,309,532 → 15,047,401 | 241,333 → 233,142 |

포화 시 두 번의 digest scan에서 4096×2개의 decode 할당을 제거했고 payload 복원 검증은 유지했다.
용량이 남아도 inventory는 O(n)이며 포화 시 추가 O(n) payload 검증이 남는다.

Operations는 1024/4092행에서 독립 backend/Runtime 1개·4개를 사용한다. Create는 직후 Delete까지 한 cycle이며 Rotate는 두 ID를
번갈아 사용한다. 아래 값은 4092행의 cycle당 평균 wall time이며 개별 요청의 대기 시간을 뜻하지 않는다.

| DB / operation / Runtime 수 | 기준 → 변경 ms/cycle | 기준 → 변경 B/cycle |
|---|---:|---:|
| SQLite / create-delete / 1 | 7.730 → 7.824 | 738,567 → 607,590 |
| SQLite / create-delete / 4 | 10.768 → 10.284 | 739,382 → 608,245 |
| SQLite / rotate / 1 | 0.986 → 0.979 | 16,105 → 16,073 |
| SQLite / rotate / 4 | 1.266 → 1.236 | 16,209 → 16,171 |
| PostgreSQL / create-delete / 1 | 3.775 → 3.973 | 742,434 → 611,472 |
| PostgreSQL / create-delete / 4 | 3.773 → 3.499 | 742,973 → 611,930 |
| PostgreSQL / rotate / 1 | 0.769 → 0.767 | 19,558 → 19,525 |
| PostgreSQL / rotate / 4 | 0.681 → 0.626 | 19,604 → 19,571 |

할당 감소는 반복해서 관측되지만 일부 create-delete 시간은 늘었으므로 모든 DB 작업의 속도 개선을 주장하지 않는다.
Rotate는 row 수를 유지하며 ensureCapacity를 호출하지 않는다. Create의 inventory와 두 operation의 digest lookup·DB fence 비용을
구분한다. 모든 workload 종료 후 실제 inventory의 행 수·digest 유효성·중복 없음도 확인했다.

첫 SQLite 동시 benchmark는 busy timeout을 지정하지 않아 SQLITE_BUSY로 실패했다. 지원 계약대로 acquisition 실패를 전파한 것이며
제품에 retry를 추가하지 않았다. 성공 경합 비용 측정을 위해 fixture를 기존 waiting-fence profile로 고친 뒤 전후 전체를 다시 실행했다.

채택한 수정은 canonical lowercase ASCII hex의 동등 byte 검사뿐이다. 64개 위치 × 모든 256개 byte와 길이·대문자·Unicode를
stdlib decode/re-encode oracle로 대조한다. COUNT, 첫 만료 행을 찾자마자 삭제, payload decode 생략은 도입하지 않는다.
전체 스캔 제거에는 DB constraint·만료 metadata·손상 검사 책임을 함께 설계해야 한다. 현행 정책 유지 결정은
[ADR-0048](../adr/0048-database-coordinated-system-state-and-shared-csrf-key-ring.md)에 반영했다.

### 로컬 checkpoint

- F2/F5/F6: `go test -json -count=1 ./migrations ./web ./internal/projectcheck/protocol` — 3 packages·521 test pass, skip 0.
- F1: `go test -json -count=1 ./orm` — 457 test pass, skip 0. 외부 소비자는 아래 통합 checkpoint에서 실행했다.
- F3 초기 normal은 265 pass·PostgreSQL 환경 관련 10 skip, F4 초기 normal은 166 pass·4 PostgreSQL skip이었다.
  실제 DB 설정 뒤 아래 실행으로 해당 integration과 Article 흐름을 확인했다.
- `GODJ_REQUIRE_POSTGRES=1 go test -json -count=1 ./db/postgres ./examples/article ./examples/article/webapp` —
  3 packages·310 pass. 단독 실행용 `TestPostgresRevisionFenceHelperProcess` 1개만 전용 helper marker가 없는 부모 열거에서 skip하고,
  실제 cross-process integration의 자식 경로는 실행됐다. 서비스 필요 테스트의 미실행을 PASS로 세지 않았다.
- F7의 두 CLI 성공 helper 소비자 — 2 pass, skip 0. Separate actual output과 locked oracle을 실제 비교했다.
- `go test -json -count=1 -timeout=15m ./systemstate ./sessions ./db/sqlite ./codegen/consumertest` —
  4 packages·888 pass·skip 0. 실제 외부 Go module의 generated reverse/prefetch 소비자를 포함한다.
- `go test -json -count=1 -timeout=10m -run '^TestMigrationCommand' ./conformance/runners/godj` —
  47 pass·skip 0. Exact SQLite snapshot의 실제 상태·false-green 거부·catalog 손상·read-only·byte identity를 유지했다.
- 아래 관련 10 package는 race와 CGO-disabled 각각 1695 pass다. 두 모드 모두 PostgreSQL 필수 환경을 설정하고 실제 DB 테스트를
  실행했다. 위와 같은 subprocess 전용 helper 한 항목만 부모 열거에서 skip하며 테스트 실패·DB 누락은 없다.

```sh
GODJ_REQUIRE_POSTGRES=1 go test -race -json -count=1 -timeout=15m ./migrations ./orm ./query ./db/postgres ./systemstate ./codegen/consumertest ./web ./internal/projectcheck/protocol ./examples/article ./examples/article/webapp
GODJ_REQUIRE_POSTGRES=1 CGO_ENABLED=0 go test -json -count=1 -timeout=15m ./migrations ./orm ./query ./db/postgres ./systemstate ./codegen/consumertest ./web ./internal/projectcheck/protocol ./examples/article ./examples/article/webapp
```

- `make docs-check format-check generate-check` — PASS. 문서 90개 링크, Helpdesk 12·Article 12·relationfixture 16개와
  별도 relationproduct의 checked-in generated fixture가 모두 현재 소스와 일치한다.
- 변경 영향 package의 `go vet`와 `git diff --check` — PASS.
- 로컬 검증과 benchmark 종료 후 이 작업에서 만든 전용 PostgreSQL DB만 삭제했고 기존 17.5 서비스가 계속 실행됨을 확인했다.
- 자체 diff 검토에서 F1의 callback field 복사/매회 검사, F3의 analysis/emission DFS와 null-negation, F4의 predicate 순서,
  F7의 setup context/cleanup 수명·독립 oracle, F8의 canonical ASCII grammar·전체 검사 후 DML을 대조했다.

### 동일 제품 소스의 Hosted full scope

- 제품·검증 소스: `b74a79eb4948ef6b57ef5271103cf05eb514d23e`.
- [CI 34466299719](https://github.com/progresshans/godj/actions/runs/34466299719), `workflow_dispatch`, `suite=full`, attempt 1:
  2026-09-10 20:04:40 KST `completed/success`. 62개 고유 job 모두 재실행 없이 `completed/success`다.
- 최종 aggregate `102845405925`의 실제 출력은 `scope: full`, `full_platform_verified: true`이며 8개 필수 owner가 모두 있다.
  현재 workflow의 62개 예상 job 이름·OS/arch/mode와 실제 목록을 대조해 누락·중복이 없고 source/run/attempt도 모두 같음을 확인했다.
- Linux/macOS amd64·arm64 × normal/race/CGO-disabled의 relation·project·command 조합과 Portable Go 12개 조합을 모두 통과했다.
- 같은 소스의 [PR feedback 34466280714](https://github.com/progresshans/godj/actions/runs/34466280714)는 `completed/success`다.
- Command 12개 job의 실제 로그를 전부 대조해 각각 operator 15 run/pass·targeted migrate 33 run/pass·skip 0과
  `verified_command_products: [operator, targeted]`를 확인했다.
- PostgreSQL 17.10 core/operator-target × normal/race/CGO-disabled 모두 완료됐다. 각 core는 12 packages·54 run/pass,
  operator-target은 2 packages·12 run/pass이며 여섯 로그 모두 skip 0이다.
- 고정 Darwin reference `102835624565`는 `PYTHON_SUITE_VERIFIED tests=274 skips=0`과 locked oracle 검사를 통과했다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7의 각 실행은 274개와 선언된 exact 전용 4개 skip 및 semantic digest를 통과했다.
- Reference job `102837127710`은 같은 run의 두 capture를 소비해 전체 conformance 대조와 Linux 32-bit compile·관계 실행을 완료했다.
- Project-check normal Linux job `102835625169`의 `GODJ_COLD_BUILD=1` 필수 선택은 1 package·2 run/pass·skip 0으로 완료됐다.
  같은 job의 일반 Runserver 선택에서 PostgreSQL service 미설정으로 skip한 한 항목은 PostgreSQL 전담 owner가 실제 실행했다.

| 실제 PostgreSQL owner | normal job | race job | CGO-disabled job |
|---|---|---|---|
| core | `102835624811` | `102835624837` | `102835624755` |
| operator-target | `102835624756` | `102835624740` | `102835624792` |

### 현재 capture의 독립 확인

공식 resolver로 같은 run의 성공한 producer를 선택하고 GitHub archive digest, repository/run/attempt/checkout,
payload SHA-256·`SHA256SUMS`를 확인했다. 두 공식 Go `Load`도 현재 소스의 profile·canonical JSON·behavioral source binding을 검증했다.

| capture | artifact / producer job / attempt | payload SHA-256 |
|---|---|---|
| SYS-020 | `10147775230` / `102835624811` / `1` | `3c6e2537b8aa456b1e37fef858362609db1096718fcc5bea0cd812d015e3b332` |
| SYS-029 | `10147738561` / `102835624756` / `1` | `1ae68529847f7e9697711bac83b786e73d225ad3f92b7537536f5c0318f8e5f8` |

SYS-020 source binding은 327 files / 3,749,392 bytes /
`ab344c55038c4d40570fa69c8e5c3e0b0288322ea94c6554af9089dac31134ee`,
SYS-029는 389 files / 3,488,968 bytes /
`13404d6eefb717c2067c409510355d695f9f66889323bcae3be76b4bb5bd43af`다.
GitHub archive SHA-256도 각각 `3fe181b8510173668b2dfe6d70eaa07165cdd5c78d9c9556724b40d3585b1921`,
`e874ffcb5170f299c2cb02209d390040aa84eb4baa1b531fc3c35c9c856f7ac5`와 일치했다.

SYS-020은 writer 두 process의 barrier·restart 보존과 divergence/loss/drift/secret 0을 확인했다.
SYS-029는 PostgreSQL·SQLite 각각 세 독립 process, Admin/API 인증·restart 유지와 state loss/schema drift/raw secret 0을 확인했다.

완료 기록은 CURRENT·TEST_EVIDENCE·GDJ-0069 작업 문서의 Markdown만 변경한다. 제품·검증 소스와 두 behavioral source binding은
Hosted 소스와 동일하며 문서 링크·상태·diff와 공식 capture Load를 별도로 확인한다. 기존 PR #1은 Draft로 유지한다.

## GDJ-0068 — 불변 값 전달과 검증 비용 정리

- 작업: [GDJ-0068](../../work/0068-immutable-value-transfer-and-verification-cost.md).
- 기준: `6e826a1acc41720b1df99acb7e488c71f24c2841`.
- 상태: 구현·전후 측정·관련 로컬 통합 검증과 보정 소스의 Hosted full scope 완료.
- 검증 소유권: affected local checkpoint 후 기존 Draft PR의 같은 제품 소스 Hosted full scope.

### 전후 측정

2026-09-10 KST, Go 1.26.5 darwin/arm64, Apple M3 Pro. 각 묶음은 해당 제품 변경 전에 benchmark를 추가해 기준을
측정했다. Generated construction의 기준은 ORM 변경 후 생성기를 바꾸기 직전의 checkpoint다.
마지막에는 다른 로컬 검증이 끝난 뒤 같은 호스트에서 package를 순차 실행했다. 아래는 각각 세 번의 중앙값이며
DB latency·전체 서비스 처리량·CI 시간을 측정한 결과가 아니다.

```sh
go test -p=1 -run '^$' -bench 'Benchmark(PrincipalResolve|ActiveSessionLoad|FormBindErrors|FormResultAccess|SerializerUnknownErrors|JSONDecoding|JSONEncoding|JSONRejectedString|TemplateLoop|TemplateRejectedEscape|ProjectStateEquality|ProjectStateAppChange|WideModelWrite|GeneratedConstruction)$' -benchtime=200ms -count=3 ./auth ./sessions ./forms ./serializers ./templates ./migrations ./orm ./codegen/consumertest
```

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| Principal resolve / 8 permissions | 240.9 → 32.93 | 416 → 0 | 4 → 0 |
| Principal resolve / 128 permissions | 2,371 → 31.04 | 8,952 → 0 | 6 → 0 |
| Active memory session load / 3 values | 1,039 → 361.4 | 1,648 → 0 | 21 → 0 |
| Form bind errors / 16 fields | 9,194 → 2,325 | 37,024 → 6,568 | 300 → 59 |
| Form bind errors / 256 fields | 940,227 → 34,087 | 4,362,293 → 108,072 | 35,220 → 783 |
| Form immutable result access / 16 fields | 866.2 → 16.29 | 4,176 → 0 | 8 → 0 |
| Serializer unknown errors / 16 fields | 2,603 → 937.1 | 10,640 → 3,304 | 22 → 27 |
| Serializer unknown errors / 256 fields | 326,611 → 11,779 | 1,984,533 → 57,832 | 266 → 275 |
| Serializer unknown errors / 1,024 fields | 5,359,130 → 47,784 | 31,876,024 → 242,024 | 1,037 → 1,048 |
| JSON decode / 16 array-valued members | 19,223 → 17,465 | 35,912 → 27,248 | 516 → 497 |
| JSON decode / 256 array-valued members | 297,454 → 271,844 | 573,090 → 436,810 | 7,969 → 7,710 |
| Template loop / 1,000 items | 278,860 → 178,383 | 1,042,235 → 50,704 | 5,757 → 2,759 |
| Template rejected escape / 1 MiB input, 64-byte cap | 1,974,799 → 24,619 | 10,485,856 → 96 | 4 → 2 |
| ProjectState equality / 16 apps | 13,680 → 1,055 | 26,808 → 0 | 135 → 0 |
| ProjectState app replacement / 16 apps | 2,568 → 592.5 | 10,408 → 2,760 | 41 → 6 |
| JSON rejected string / 64-byte document cap | 247,322 → 3,094 | 487,065 → 64 | 6 → 1 |
| JSON encode / 1 row | 1,718 → 883.7 | 1,352 → 744 | 29 → 6 |
| JSON encode / 100 rows | 159,792 → 81,108 | 185,383 → 153,000 | 2,022 → 19 |

Serializer unknown 오류는 작은 임시 collection의 할당 수가 조금 늘었지만 누적 prefix 복사와 총 할당량이 크게 줄었다.
JSON decode의 이득은 이 측정에서 약 9~10%이며 encoder·출력 거부 경로의 큰 변화와 구분한다.
Form/Session/Principal의 불변 반환만 복사를 줄였고 mutable getter, 외부 입력과 callback 소유권은 계속 검증했다.
Template과 JSON의 출력 거부는 입력 검증 자체를 생략하지 않으며 중간 escape 문자열을 만들지 않는다.

`BenchmarkWideModelWrite`는 O(1) field reader와 fake Mutator를 가진 4/32/256개 writable scalar field 모델이다.
쓰기 재사용과 constructor 비용을 따로 측정했다. 공개 metadata 입력은 복사하고 callback에 전달할 field도 매번 분리한다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| fields 4/Create | 812 → 661.5 | 1,872 → 1,392 | 6 → 5 |
| fields 4/Update | 858.1 → 708.6 | 1,968 → 1,488 | 7 → 6 |
| fields 4/Save | 1,065 → 561.2 | 2,944 → 2,464 | 8 → 7 |
| fields 4/SaveMask | 1,199 → 495 | 3,008 → 1,312 | 9 → 5 |
| fields 4/NewManager | 307.5 → 674.8 | 1,360 → 2,368 | 5 → 9 |
| fields 32/Create | 9,534 → 4,302 | 15,544 → 12,344 | 9 → 8 |
| fields 32/Update | 9,583 → 4,445 | 16,536 → 13,336 | 10 → 9 |
| fields 32/Save | 7,813 → 3,012 | 20,768 → 17,568 | 8 → 7 |
| fields 32/SaveMask | 10,108 → 3,444 | 23,112 → 12,488 | 12 → 8 |
| fields 32/NewManager | 1,279 → 3,481 | 7,600 → 14,560 | 5 → 13 |
| fields 256/Create | 361,115 → 33,600 | 122,808 → 95,544 | 9 → 8 |
| fields 256/Update | 334,514 → 34,655 | 132,504 → 105,240 | 10 → 9 |
| fields 256/Save | 257,598 → 22,677 | 168,480 → 141,216 | 8 → 7 |
| fields 256/SaveMask | 344,249 → 26,076 | 186,952 → 100,296 | 12 → 8 |
| fields 256/NewManager | 8,931 → 26,498 | 60,336 → 115,168 | 5 → 13 |

준비된 lookup을 추가해 `NewManager`의 시간과 메모리는 늘었다. 이 변경은 Manager를 반복 사용하는 경로를 위한 것이며,
일회성 생성·쓰기까지 무조건 빨라졌다는 뜻은 아니다. Save의 fallback insert는 전체 필드를 순서대로 한 번 읽고,
forced/masked update는 사용하지 않는 insert plan을 만들지 않는다.

다음은 실제 checked-in Article 생성 코드의 BuildCreate/BuildPatch와 관계 fixture의 storage.Field 호출이다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| Create | 169.8 → 83.08 | 752 → 320 | 3 → 1 |
| Patch | 119.2 → 52.1 | 544 → 112 | 3 → 1 |
| RelationStorage | 490.9 → 277.2 | 1,696 → 384 | 12 → 4 |

Compile fixture는 15개 positive와 29개 negative를 각각 독립 package로 두고 두 외부 module build로 검사한다.
각 package의 start·terminal, negative 자신의 build failure·diagnostic을 확인한다. Dependency failure, 누락·잘린 JSON,
중복 결과나 runtime skip을 fixture 성공으로 인정하지 않는다. No-test-files terminal은 compile-only package에서만 별도 증명한다.
동일한 두 top-level 검사의 단일 관측은 package 7.128→2.736초, negative 2.31→0.12초였다.
이는 반복 성능 실험이나 Hosted 단축 시간의 증명이 아니며, 실제 facade/architecture/부모·자식 race 검사는 별도로 유지했다.

주요 구현 소스 `aaf104b`의 제품·CLI·생성기 Go는 같은 `scripts/sourceinventory` 분류에서 80,619→80,448줄이다. Generated Go는 6,723→6,740줄이고,
회귀·측정 Go는 164,446→165,320줄이다. 순수 줄 수를 줄이기 위한 테스트 삭제는 하지 않았다.

### 로컬 변경 묶음

- `go test ./validation ./auth ./sessions ./forms/... ./serializers ./api/... ./admin ./web/...`: PASS.
  외부 snapshot, 세션 원본·파생값 분리, Form 초기값 동시 overlay·callback 보관 값, invalid permission·반복 Page 응답.
- `go test ./templates ./serializers ./migrations`: PASS. Nested/include loop scope·forloop object 의미,
  escape의 정확한 cap·오류 시 nil, JSON empty-name/duplicate/child/budget 우선순위, ProjectState zero/empty·상태별 소유권.
- `go test ./orm`: PASS. Full field reference, 순서가 다른 PK의 forced/fallback insert,
  callback metadata의 동시 변경 격리와 기존 write/save 회귀.
- JSON 직접 bounded 출력 후 `go test ./serializers`: PASS. `encoding/json`의 `SetEscapeHTML(false)`를 독립 비교값으로
  사용해 ASCII control, quote/backslash, Unicode/U+2028/U+2029와 정확한 document cap을 대조했다.
- `go test ./codegen ./codegen/consumertest ./orm ./internal/compiletest`: PASS. Standalone/bundle의 실제 선행 AST 충돌,
  정상·오용 ABI와 generated module의 실제 소비자 실행을 포함한다.
- SQLite·PostgreSQL assignment compiler의 quoting/duplicate/value 오류 순서와 SQLite ASCII column key 검사: PASS.
  이 checkpoint는 실제 PostgreSQL 서비스 실행 증거가 아니다.
- Testenv·두 attestation profile과 compile/generated environment 검사: PASS. 마지막 env 값·Windows case-fold 의미,
  offline flags·실제 부모 race·취소·helper source 변경에 의한 binding 무효화를 확인했다.
- Django planning/restart의 공유 관측 helper 검사 23개 PASS. Go actual과 Django expected는 합치지 않았다.
- CI 도구 전체 `PYTHONPATH=scripts/ci PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts/ci -p 'test_*.py'`:
  36개 PASS. Command owner의 선택·미선택·실패·누락·skip과 CLI output을 확인했다.
- Actionlint v1.7.12로 `ci.yml`·`feedback.yml` PASS. `-shellcheck= -pyflakes=`로 실행했으므로 두 별도 도구 검사는 포함하지 않는다.

### 통합 checkpoint

영향 범위 25개 package를 아래 selector로 normal checkpoint 이후 race·CGO-disabled에서 실행했다.

```sh
go test -race -json -count=1 -timeout=15m ./validation ./auth ./sessions ./forms/... ./serializers ./templates ./migrations ./orm ./api/... ./admin ./web/... ./systemstate ./db/internal/queryplan ./db/sqlite ./db/postgres ./internal/testenv ./internal/compiletest ./codegen ./codegen/consumertest ./conformance/systemstate/attestation ./conformance/projectoperatorproduct/attestation
```

CGO-disabled는 같은 명령에서 `-race`를 빼고 `CGO_ENABLED=0`을 적용했다. 각 exit status와 전체 JSON terminal을 검사했다.
Race는 25 packages·2,955 pass/2,965 run, CGO-disabled는 25 packages·3,006 pass/3,016 run이다.
각각 PostgreSQL integration·helper 10개가 로컬 서비스 환경 부재로 skip됐다. 실제 PostgreSQL 검증은 Hosted 전담 owner가 맡는다.

실제 command 검증은 `go test -p=1 -json -count=1 -timeout=25m`에서 아래 20개 top-level test만 `-run`으로 선택했다.
모든 하위 test를 포함하며 필수 목록을 `go_test_events.py --required ... --no-skips`로 확인했다.
결과는 7 packages·120 run/pass·skip 0이다.

| package (`conformance/` 아래) | 필수 top-level test |
|---|---|
| projectmigrateproduct | `TestGlobalMigrateArticleSQLiteProduct`, `TestGlobalMigrateSQLiteMiddleFailureAndFreshResume`, `TestGlobalMigrateArticleSQLiteFullMIG096Concurrency`, `TestGlobalMigrateAuthenticatedArticleRestartDurability` |
| runserverproduct | `TestRunserverPublicOnlyEnvironmentsDiscardAmbientArticleCredentials`, `TestGlobalRunserverPublishesAuthenticatedArticleAdminAndAPI`, `TestGlobalRunserverArticleSQLiteDevelopmentLoop`, `TestGlobalRunserverRejectsStaleCopiedArticleBeforeRuntime`, `TestRunserverHarnessForcedCleanupIncludesSeparateDescendantGroup` |
| migrationwriterproduct | `TestMigrationWriterExternalProjectSQLitePublicSurface` |
| projectshowmigrationsproduct | `TestGlobalShowMigrationsExternalProjectSQLiteProduct` |
| projectsqlmigrateproduct | `TestGlobalSQLMigrateExternalSQLiteProduct`, `TestSQLProductRunnerPipelineExecutionControls`, `TestSQLProductRunnerSourceBoundaries` |
| projectoperatorproduct | `TestOperatorSanitizeEnvironmentDropsHostOnlyControls`, `TestGlobalCreatesuperuserExternalSQLiteProduct`, `TestOperatorCanonicalSchemaRowsSortsAndFramesWithoutAmbiguity`, `TestOperatorSQLiteSchemaSnapshotDetectsCatalogMutation`, `TestOperatorCountRawSecretOccurrencesDetectsAuditMarker` |
| projectmigratetargetproduct | `TestProjectLinkedTargetedMigrateSQLite` |

- 고정 Python/Django/DRF exact: `PYTHONDONTWRITEBYTECODE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uv tool run --from uv==0.10.12 uv run --project conformance/reference/drf --frozen python -m scripts.ci.python_tests --profile exact`:
  PASS, `PYTHON_SUITE_VERIFIED tests=274 skips=0`. Lock을 변경하지 않았다.
- `make docs-check format-check generate-check`: 최종 PASS. Helpdesk 12·Article 12·relationfixture 16개와 별도 metadata fixture를 확인했다.
- 최종 영향 package의 `go vet`와 `git diff --check`: PASS.

초기 compile에서 삭제한 namespace validator 호출과 이동한 environment import가 남아 실패했으며 함께 정리한 뒤 통과했다.
초기 `make generate-check`는 별도 `relationproduct`의 main 생성물 두 파일이 오래되어 실패했다. 두 파일을 재생성하고 전체 drift와
해당 소비자의 normal·race·CGO-disabled를 다시 실행해 모두 PASS를 확인했다. 첫 runtime benchmark의 package는 모두 PASS였지만
출력 wrapper가 zsh readonly `status` 대입으로 실패했다. 원래 완료 로그와 측정값을 확인했고 최종 측정 wrapper도 정상 종료했다.

CI는 같은 OS/arch/mode의 operator·targeted command를 한 job에서 차례로 실행해 checkout·Go·의존성 준비를 공유한다.
각 test selector·필수 sentinel·no-skip·timeout·normal vet를 유지하며 첫 제품 실패 뒤에도 다음 제품과 최종 outcome gate를 실행한다.
선택된 제품의 누락·skip은 성공이 될 수 없다. 전체 platform·PostgreSQL·capture·cold CLI·32-bit의 최종 증거는 아래 Hosted 결과다.

### 첫 Hosted 구성 검사 실패와 보정

첫 제출 소스 `aaf104b6ada9444c1683f8d9a13f62cc218618a7`의
[full scope 34447460391](https://github.com/progresshans/godj/actions/runs/34447460391), attempt 1에서
`TestWorkflowRetainsDeclaredCoordinatesAndModes`와 `TestWorkflowRequiredProductSentinelsRemainInventoried`가 실패했다.
두 검사는 통합 전 `project-operator-product-matrix` 이름을 참조하고 있었다. Linux normal job `102775334624`,
arm64 CGO-disabled `102775334610`과 macOS race `102775334557`의 실제 실패 로그로 확인했다.

검사를 새 command owner로 연결하고 각 product step을 분리해 mode·timeout·vet·실행 조건·필수 sentinel·no-skip을
확인하도록 보강했다. 두 step의 outcome을 최종 gate에 전달하는 검사도 유지했다.
보정 뒤 `go test -count=1 -timeout=10m ./conformance/internal/protocol`의 normal·race·CGO-disabled 모두 PASS다.
제품 구현은 바꾸지 않았으나 검증 source binding이 바뀌므로 보정 소스에서 전체 Hosted와 capture를 새로 실행한다.
첫 실행의 일부 성공한 job이나 capture를 최종 전체 검증으로 재사용하지 않는다.
보정 실행을 요청한 뒤 첫 실행의 남은 작업은 취소됐고 최종 conclusion은 `cancelled`다.

### 보정 소스의 Hosted full scope

- 제품·검증 소스: `ffe384492bee0b98aec918d594f6c17531797280`.
- [CI 34447908999](https://github.com/progresshans/godj/actions/runs/34447908999), `workflow_dispatch`, `suite=full`, attempt 1:
  2026-09-10 16:31 KST `completed/success`. 이 새 실행의 62개 고유 job 모두 재실행 없이 `completed/success`다.
- 최종 aggregate `102784486761`의 실제 출력은 `scope: full`, `full_platform_verified: true`다.
  Command·reference·exact Darwin·portable Go·PostgreSQL·project check·Python compatibility·relation의 8개 필수 owner를 확인했다.
- 같은 소스의 [PR feedback 34447900215](https://github.com/progresshans/godj/actions/runs/34447900215)도 `completed/success`다.
- Linux/macOS amd64·arm64 × normal/race/CGO-disabled의 relation·project·command 조합과 Portable Go 12개 조합을 모두 통과했다.
  Command 12개 job의 실제 로그를 전부 대조해 각각 operator 15 run/pass, targeted migrate 33 run/pass, skip 0과
  `verified_command_products: [operator, targeted]`를 확인했다.
- PostgreSQL 17.10 core/operator-target × 세 mode 모두 PASS. 각 core는 12 packages·54 run/pass,
  operator-target은 2 packages·12 run/pass이며 여섯 로그 모두 skip 0이다.
- 고정 Darwin reference는 `PYTHON_SUITE_VERIFIED tests=274 skips=0`과 locked oracle 검사를 통과했다.
  Python 3.12.13·3.13.15·3.14.3·3.14.7은 각각 274개·선언된 exact 전용 4개 skip과 semantic digest를 통과했다.
- Reference job `102778261410`은 같은 run의 두 capture를 소비해 전체 conformance 대조와 Linux 32-bit compile·관계 실행을 완료했다.
  Cold CLI job `102776873840`의 `GODJ_COLD_BUILD=1` 필수 선택은 1 package·2 run/pass·skip 0이다.
  같은 job의 일반 Runserver 선택에서 PostgreSQL service 미설정으로 skip한 한 항목은 PostgreSQL 전담 owner가 실제 실행했다.

| 실제 PostgreSQL owner | normal job | race job | CGO-disabled job |
|---|---|---|---|
| core | `102776873451` | `102776873485` | `102776873467` |
| operator-target | `102776873391` | `102776873456` | `102776873526` |

Full job 수는 직전 74개에서 62개로 줄었다. Workflow 생성부터 마지막 job 완료까지는 직전
[34432064345](https://github.com/progresshans/godj/actions/runs/34432064345)의 36분 21초에서 이번 29분 59초로 줄었다
(07:01:24→07:31:23 UTC). 이는 runner 대기를 포함한 각 한 번의 관측이다. 소스·cache·배정 조건이 다른 실행이므로
6분 22초 전체를 job 통합만의 효과로 해석하지 않는다. 제거한 12개 checkout·toolchain·dependency 준비와 실제 각 검증의 완료는 확인했다.

### 현재 capture의 독립 확인

성공한 normal producer의 artifact를 공식 resolver로 선택하고 GitHub archive digest, repository/run/attempt/checkout,
payload SHA-256과 `SHA256SUMS`를 검증했다. 두 공식 Go `Load`도 현재 소스의 profile·canonical JSON·behavioral source binding을 확인했다.

| capture | artifact / producer job / attempt | payload SHA-256 |
|---|---|---|
| SYS-020 | `10140459061` / `102776873451` / `1` | `2299e5f562775927b6cd3f9e6015844b32875e48c1cba9f4823373bcf9d9267f` |
| SYS-029 | `10140444503` / `102776873391` / `1` | `6a9151f6e8a1bc1e0b58ef4b5aac07380e6e421270e2fe88a9e6443e64786f48` |

SYS-020의 source binding은 327 files / 3,750,356 bytes /
`bd244ee50dea57f95fd5a25dc911ecb3ab941592b2157d69f3c401f4ee08579c`,
SYS-029는 389 files / 3,489,542 bytes /
`a00eb7b61fd71685b7d06e3d15239459d8be7316dca550fd56770ac4559653bf`다.
각 GitHub archive digest는 `95f3c120f51c6007f3335c7cd902cc29e53a7468bc89644d9cdd8c3f51ebb026`과
`6a6683d56946b1d7cbebde754d2351b38e8fe570574430f998be981699ea9b8c`이며 다운로드한 archive와 일치했다.

SYS-020은 writer 두 process의 barrier·restart 보존과 divergence/loss/drift/secret 0을 확인했다.
SYS-029는 PostgreSQL·SQLite 각각 세 독립 process, Admin/API 인증·restart 유지와 state loss/schema drift/raw secret 0을 확인했다.

완료 기록은 CURRENT·TEST_EVIDENCE·GDJ-0068 작업 문서의 Markdown만 변경한다. 제품·검증 소스와 두 behavioral source binding은
Hosted 소스와 동일하며, 문서 링크·상태·diff와 공식 capture Load를 별도로 확인한다. 기존 PR #1은 Draft로 유지한다.

## GDJ-0067 — 불변 준비와 실행 비용 정리

- 작업: [GDJ-0067](../../work/0067-immutable-preparation-and-execution-cost.md).
- 기준 제품: `9c21568dcbd7bb33c817d6e830a44026d2554e32`.
- 상태: 구현·로컬 통합 검증·동일 제품 소스 Hosted full scope 완료. 각 baseline은 해당 제품 변경 전 실행했다.
- 환경: 2026-09-10 KST, Apple M3 Pro, Go 1.26.5 darwin/arm64.

`go test -run '^$' -bench 'Benchmark(JSONEncoding|JSONNestedConstruction|TemplateComposition|TemplateLoop)$' -benchtime=200ms -count=3 ./serializers ./templates`

각 값은 세 실행의 중앙값이다. Encoding은 10개 문자열 필드를 가진 1/100행, construction은 중첩 object 생성이다.
기존 template composition은 1,000개 불변 child 공유, loop는 1,000개 항목 렌더링을 측정한다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| JSON encode / 1 row | 3,258 → 1,740 | 4,169 → 1,352 | 87 → 29 |
| JSON encode / 100 rows | 307,603 → 158,443 | 498,217 → 185,383 | 8,020 → 2,022 |
| JSON nested construction / depth 8 | 1,394 → 829.0 | 3,072 → 3,072 | 24 → 24 |
| JSON nested construction / depth 64 | 51,506 → 6,471 | 24,576 → 24,576 | 192 → 192 |
| Template loop / 1,000 items | 280,251 → 289,060 | 1,046,343 → 1,042,220 | 5,759 → 5,757 |
| Template inheritance / depth 1 | 222.5 → 131.5 | 96 → 56 | 5 → 3 |
| Template inheritance / depth 8 | 1,024 → 169.7 | 320 → 56 | 8 → 3 |
| Template inheritance / depth 32 | 4,116 → 204.4 | 1,088 → 56 | 10 → 3 |
| ORM reused Manager.Using / 4 fields | 2,022 → 23.17 | 3,457 → 48 | 37 → 1 |
| ORM NewManager / 4 fields | 1.773 → 2,070 | 0 → 4,210 | 0 → 40 |
| Reverse prefetch / 20 owners, 1,000 rows | 162,051 → 151,262 | 476,520 → 465,114 | 8,426 → 7,390 |

Inheritance은 같은 `-benchtime=200ms -count=3` 조건의 `BenchmarkTemplateInheritance`를 template 제품 변경 전후에 실행했다.
Template loop는 복사·할당은 줄었지만 이 측정의 시간은 약 3% 증가했다. 전체 render가 빨라졌다고 일반화하지 않는다.
ORM은 `Benchmark(ManagerPreparation|ReversePrefetch)$`를 제품 변경 전후에 같은 조건으로 실행했다.
준비 비용을 `NewManager`로 옮겼으므로 재사용하는 Manager의 `Using`이 측정 대상이며, 일회성 constructor가 공짜라고 주장하지 않는다.
Prefetch는 pointer field가 있는 모델과 fake row source로 복사·그룹·cache 비용을 측정하며 DB latency를 포함하지 않는다.

### 로컬 변경 묶음

- `go test ./auth ./web/sessionauth`: PASS. 난수 panic의 원래 값 전파·후속 병렬 해싱,
  clock/entropy panic을 HTTP 500으로 복구한 뒤 다음 요청의 CSRF token/cookie 발급을 확인했다.
- `go test ./serializers`: PASS. 문자열 escape·독립 동시 출력·공유 subtree의 출력 budget·정수 경계·기존 오류 우선순위.
  초기 compile에서 삭제한 `validObject`의 Spec.Bind 호출이 남아 실패했고, 같은 불변 object 검증 경계로 전환한 뒤 통과했다.
- `go test ./templates ./apps ./forms/... ./web`: PASS. 상속·nested block·include scope·깊이 실패 위치·동시 render와 기존 입력/라우팅 회귀.
- 추가 후보: JSON 정수 출력의 임시 slice 할당과 salt read 뒤 취소된 PBKDF2 계산을 제거했다. 해싱 work profile과 오류 종류는 유지했다.
- `go test ./orm`: PASS. metadata 입력/getter와 쓰기 callback mutation 격리, using별 독립 평가, 기존 relation/prefetch/eager 회귀.
- `go test ./db/sqlite ./db/postgres`: PASS. 각 dialect의 JOIN/nullable/alias/오류·SQL 결과 회귀. PostgreSQL 서비스 의존 테스트는
  로컬 환경에서 skip하며 실제 PostgreSQL 검증은 아직 Hosted owner에 남아 있다.
- `go test -count=1 -timeout=25m ./internal/compiletest ./codegen/consumertest`: PASS. 전체 정상·오용 ABI와 생성 소비자.
  최종 `-count=1`·checksum 복사·완전 offline 설정 뒤 생성 소비자 전체를 normal·race·CGO-disabled로 다시 실행해 통과했다.
- `make python-test ci-tools-test`: normal `PYTHON_SUITE_VERIFIED tests=274 skips=4`, CI 도구 34개 PASS.
  Skip은 선언된 exact 전용 네 검사다. DRF 직접 관측은 모두 실행했다.
- Attestation 두 profile normal과 migration/SQLMigrate/ShowMigrations outer flow normal: PASS.
  공유 source validation에서도 profile별 자원 한도·타입·digest 범위를 유지하고, 명령별 runner code·취소·비정상 process 분류를 확인했다.

### 통합 checkpoint

| 실제 실행 | 결과와 범위 |
|---|---|
| `go test -run '^$' ./...`, `go vet ./...` | PASS. 전체 패키지 compile·정적 분석. 전체 테스트 실행을 의미하지 않는다. |
| API·Admin·SystemState·Sessions·Article·Helpdesk normal | PASS. 새 Manager metadata 준비를 사용하는 실제 읽기·쓰기·HTTP·인증 흐름. |
| Forward object·prefetch·select·delete 제품 normal | PASS. 프로젝트에 연결된 생성 모델과 실제 SQLite 관계·cache·NULL·rollback 관측. |
| Migrate SQLite 외부 제품·중간 실패/재개·인증 restart | PASS. 세 필수 sentinel과 하위 항목을 `go_test_events.py`로 검증: 1 package, 10 run/pass, skip 0. |
| Auth·SessionAuth·Serializer·Template·ORM·SQLite/PG compiler·Attestation 두 profile·생성 소비자 race | 각 패키지 PASS. 초기 합동 실행은 `internal/compiletest`의 공용 `repositoryRoot`가 `!race` 파일에 남아 compile 실패했다. 공통 파일로 옮긴 뒤 해당 패키지의 race·normal·CGO-disabled 재검증 PASS. |
| 같은 영향 범위 CGO-disabled | PASS. 외부 ABI 오용과 생성 소비자 전체 포함. PostgreSQL 서비스 I/O는 로컬에서 검증하지 않았다. |
| Migration command 공통 분류와 세 outer 흐름 race | PASS. runner code·취소·cleanup·잘못된 process failure 거부 유지. |
| 고정 Python/DRF exact | uv 0.10.12, Python 3.14.3, Django 6.1, DRF 3.18.0. `PYTHON_SUITE_VERIFIED tests=274 skips=0`; 고정 byte/hashseed 관측 포함. |
| `make docs-check format-check generate-check`, actionlint v1.7.7 | PASS. Helpdesk 12·Article 12·relation 16개 생성 파일 clean, checked-in 생성물 검사와 Actions 문법 검증. |

Exact Python은 `uv tool run --from uv==0.10.12 uv run --project conformance/reference/drf --frozen python -m scripts.ci.python_tests --profile exact`로
실행했고 `PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC`를 적용했다. Host uv가 다른 버전이어도 lock을 변경하지 않는다.
새 JOIN helper와 CI 필수 실행 목록 변경이 두 behavioral source binding을 무효화하는 mutation test도 통과했다.
PostgreSQL projection 충돌은 기존 category/code를 유지하고 상세 메시지에 해당 edge 이름을 추가했다.

실제 제품·생성기·CLI Go 코드는 기준 80,658줄에서 80,619줄로 줄었다(`scripts/sourceinventory`의 같은 분류).
회귀·성능 측정 코드는 추가했으며 생성된 45파일/6,723줄은 바꾸지 않았다. 대규모 줄 수 절감이나 전체 서비스 처리량의 개선을 주장하지 않는다.

### Hosted 통합 검증

- 제품 소스: `56303abeefd1c911ad5954cd062a2e4ed67ce41e`.
- 실행: [34432064345](https://github.com/progresshans/godj/actions/runs/34432064345), `workflow_dispatch`, `suite=full`, attempt 1.
- 최종 결과: 2026-09-10 12:43 KST `completed/success`. 74개 고유 job이 모두 completed/success이며 attempt 1에서 재실행 없이 통과했다.
  최종 aggregate job `102736316913`의 실제 출력은 `scope: full`, `full_platform_verified: true`이며 아홉 필수 owner를 확인했다.
- Reference·현재 capture 소비, 전체 OS/arch/mode와 Linux 32-bit compile·ORM/관계·runner 실행을 확인했다.
  macOS Intel relation normal은 26 packages·2,906 run/pass·skip 0, race는 26 packages·2,871 run/pass·skip 0이다.
  마지막 targeted migration normal은 1 package·33 run/pass·skip 0으로 끝났다.
- PostgreSQL 17.10 core/operator-target의 normal·race·CGO-disabled 여섯 작업은 completed/success이며 실제 job 로그의
  완료 gate도 각각 core 12 packages·54 run/pass, operator 2 packages·12 run/pass, 모두 skip 0이다.
- Python compatibility 3.12.13·3.13.15·3.14.3·3.14.7은 각각 `PYTHON_SUITE_VERIFIED tests=274 skips=4`와 semantic digest를
  통과했다. Hosted exact Darwin은 274개·skip 0과 locked oracle 검사를 통과했다. 네 skip은 해당 exact owner가 실제 실행했다.
- Cold CLI 전담 선택은 `GODJ_COLD_BUILD=1`에서 1 package·2 run/pass·skip 0으로 통과했다. 같은 job의 Runserver 일반
  선택에서 PostgreSQL 환경 부재로 skip한 한 항목은 위 PostgreSQL 전담 여섯 작업 중 core가 실제 실행했다.
- 정상 producer가 게시한 두 capture를 현재 run의 artifact ID·producer attempt/job으로 선택했다. Python의 checkout/run/
  provenance/payload checksum 검사와 공식 Go `Load`의 profile·behavioral source 검사가 모두 통과했다.

| capture | artifact / producer job / attempt | payload SHA-256 |
|---|---|---|
| SYS-020 | `10134915349` / `102729493640` / `1` | `6f86d690ce1fcf66a5b06cb997cb1fefa5d08c7cd13fd3b31f80f8bdae1df9fe` |
| SYS-029 | `10134892644` / `102729493575` / `1` | `290c7ae34481a0635f393f4b8c0bfb10369a5dbe3af49f17cd7238cf06ac28b8` |

SYS-020의 source binding은 325 files / 3,746,834 bytes /
`8d22988e05578d6da4ca54284c41543e7b2ae538848dbdaaf43ac2cda44fc083`,
SYS-029는 386 files / 3,496,180 bytes /
`ed8afb62d161a0366fc4901cd59764bc14f162bfddbb2d99b7f28cb5d17b94bc`다.
둘 다 고정한 제품 소스의 로컬 계산과 일치한다. SYS-020은 barrier/restart 유지와 divergence/loss/drift/secret 0,
SYS-029는 PostgreSQL·SQLite의 세 독립 process, Admin/API 인증·restart 유지와 state loss/schema drift/raw secret 0을 확인했다.

제품 검증 뒤 마감 변경은 CURRENT·TEST_EVIDENCE·GDJ-0067 작업 문서 세 Markdown 파일이다. 제품 source diff는 없고
두 behavioral source binding·capture Load 결과가 검증 당시와 같다. 문서 링크·상태·diff 검사를 별도로 적용한다.


## GDJ-0066 — 실행 비용과 중복 책임의 측정 기반 정리

- 작업: [GDJ-0066](../../work/0066-measured-runtime-and-validation-optimization.md).
- 기준 제품: `d6443fa048ffdf4e22e0d817f4e8d0aaeb83e8da`, 기존 Draft PR #1의 동일 작업 브랜치.
- 상태: 구현·비교 측정·관련 로컬 검증과 최종 Hosted full scope 완료. Baseline은 제품 변경 전 비교 benchmark와 문서만 추가한 작업 사본이다.
- 환경: 2026-09-10 KST, Apple M3 Pro, Go 1.26.5 darwin/arm64.

### 변경 전 측정

`go test -run '^$' -bench 'Benchmark(PlanDerivation|LoadedAncestorIndex|AuditPruneFullSQLite|ActiveSessionLoad)$' -benchtime=200ms -count=3 ./query ./migrations ./systemstate ./sessions`

각 행은 세 번 실행한 ns/op의 중앙값이다. DB/prune 준비와 migration graph 구성은 benchmark loop 밖이다.
Audit는 실제 file-backed SQLite에 보존 한도만큼 ID를 채운 뒤 prune query를 반복한다. HTTP 처리량 또는 전체 CI 시간의 측정은 아니다.

| workload | 기준 → 변경 ns/op | 기준 → 변경 B/op | 기준 → 변경 allocs/op |
|---|---:|---:|---:|
| Plan order / 64 fields | 776.1 → 25.53 | 9,120 → 80 | 4 → 1 |
| Plan filter / 64 fields | 782.1 → 25.93 | 9,040 → 0 | 3 → 0 |
| Plan order / 256 fields | 2,453 → 25.62 | 35,616 → 80 | 4 → 1 |
| Plan filter / 256 fields | 2,446 → 25.77 | 35,536 → 0 | 3 → 0 |
| Ancestor index / 128 chain | 113,634 → 13,195 | 42,920 → 18,264 | 140 → 8 |
| Ancestor index / 2,048 chain | 28,482,495 → 394,881 | 1,229,330 → 803,025 | 2,078 → 14 |
| Full audit prune / 10,000 rows | 1,246,068 → 595,376 | 319,593 → 2,752 | 29,782 → 53 |
| Full audit prune / 100,000 rows | 12,535,509 → 6,096,642 | 3,199,626 → 2,768 | 299,782 → 53 |
| Active memory session Load | 1,275 → 1,039 | 2,112 → 1,648 | 26 → 21 |
| Eager ready-related cache / one row | 2,177 → 200.6 | 3,921 → 584 | 45 → 5 |

변경 후에는 위 selector에 `ForwardSelectedReadyCache`와 `./orm`을 추가해 같은 조건으로 실행했다.
Eager의 기준은 Query storage 변경 후·eager 최적화 전 작업 사본에서 별도로 측정한 값이다.
시간·할당 모두 세 실행의 중앙값이며 개선 비율은 이 microbenchmark 범위다. 감사 prune은 DB 내부 bounded scan을 계속 수행한다.
관계 Count는 실제 JOIN multiplicity·Distinct·slice·NULL fixture에서 한 aggregate 행을 소비함을 확인했고,
영속 Manager.Load는 transaction 1회·행 조회 1회, timestamp 변화 시 write 1회·동일 값일 때 write 0회를 확인했다.

### 로컬 checkpoint

| 실제 실행 | 결과와 보존한 검증 |
|---|---|
| 전체 `go test -run '^$' ./...` | PASS. 새 Query/Store API와 checked-in 소비자 전체 compile. 테스트 실행 PASS를 의미하지 않는다. |
| Query/ORM/SQLite/PostgreSQL compiler/systemstate normal | PASS. 생성 시점 오류·불변 소유권·관계 Count와 MIN, rows/cardinality/close/cancel과 audit rollback. PostgreSQL 서비스 필요 10개는 로컬 skip이며 Hosted가 소유한다. |
| `./sessions ./systemstate ./admin ./web/sessionauth` normal | PASS. atomic access interleaving, 저장 전 정책 검증, missing-before-clock, 시계·entropy panic cleanup, 한 transaction/한 read와 실제 인증 흐름. |
| eager 변경 뒤 `./orm` normal | PASS. 독립 ready cache·Fresh·nullable·projection·취소·모델 복사 회귀. |
| `./query ./orm ./migrations ./sessions ./systemstate ./db/internal/queryplan ./db/sqlite ./internal/gobuild` race | PASS. 실제 테스트가 없는 공통 queryplan 패키지는 compile만 수행하며 호출 backend의 검사가 해당 경로를 검증한다. |
| Query/ORM/migrations/sessions/systemstate/SQLite/PostgreSQL CGO-disabled | PASS. PostgreSQL 서비스 의존 10개 skip은 Hosted 검증 전까지 미실행이다. |
| 다섯 외부 SQLite 제품 sentinel normal | writer·targeted migrate·operator·showmigrations·sqlmigrate 모두 실제 build/child/DB 실행 PASS. `go_test_events.py`가 필수 sentinel·package 완료·no-skip을 확인했다. |
| `make generate-check` | PASS. Helpdesk 12·Article 12·relation fixture 16개 생성 파일 clean, checked-in generated test PASS. |
| PostgreSQL raw catalog negative control | PostgreSQL 17.5 별도 임시 cluster에서 PASS. 컬럼 null/default, expression/partial index, sequence, internal FK trigger, policy/view와 cancellation을 확인하고 cluster를 종료·정리했다. 요구 profile 17.10은 Hosted에서 검증한다. |
| 고정 Python/DRF reference 환경 | uv 0.10.12, Python 3.14.3, Django 6.1, DRF 3.18.0으로 `GODJ_EXACT_PROFILE=1` 및 complete discovery gate 실행: `PYTHON_SUITE_VERIFIED tests=274 skips=0`. 고정 reference byte/hashseed 대조 포함. |
| CI 도구·Actions | `scripts/ci` unittest 33개 PASS; actionlint v1.7.7 PASS. 새 PostgreSQL catalog sentinel을 core required/no-skip inventory에 포함했다. |

초기 Query API 전환에서 기존 unchecked 입력 테스트가 실패해 생성자 오류 경계로 수정했다. 초기 Python 실행은 host uv 0.12.3
profile 불일치와 DRF 없는 root 환경으로 실패·skip했으며, 고정 uv와 별도 DRF 환경의 완료 gate로 위 결과를 얻었다.
예전 reference/oracle/lock·생성물은 바꾸지 않았다. 초기 실패와 아래 확정된 실행 결과를 구분한다.

### 제품 소스의 Hosted 검증

[CI 34385261400](https://github.com/progresshans/godj/actions/runs/34385261400)는 제품 소스
`b028b77fa27f680c82f4cb188d5af1088dbb61c2`를 workflow dispatch의 `full` scope로 검증했다.
이벤트 source와 실제 checkout은 같은 commit이다. 2026-09-10 03:26 KST의 최종 conclusion은 success이며,
74개 고유 job의 최신 결과가 모두 completed/success다. 최종 aggregate job `102592262213`은 `scope: full`,
`full_platform_verified: true`와 아홉 필수 owner의 성공을 확인했다. Linux/macOS amd64·arm64의 normal/race/CGO-disabled,
PostgreSQL 17.10·cold build·고정 oracle·Python 호환성 및 선택한 32-bit Linux compile/관계 실행을 포함한다.

Attempt 1의 macOS Intel relation normal job `102579851988`은 `TestExternalConsumerCompiles`의
`project_external_consumer.go.txt`에서 module checksum 확인 중 `proxy.golang.org` DNS `i/o timeout`으로 실패했다.
변경하지 않은 compile harness의 의존성 조회 실패이며, 이 실행을 제품 PASS로 계산하지 않는다. 최초 aggregate도 그
owner 실패를 정확히 거부했다. 실행 종료 후 제품 변경 없이 실패 job과 aggregate만 attempt 2로 재실행했다.
재시도 job `102590752409`는 26 packages·2,903 run/pass·skip 0으로 성공했다. 기존 72개 성공 결과는 유지했으며,
API의 시작/종료 시각 대조로 두 job만 실제 재실행된 것을 확인했다.

PostgreSQL 17.10 core/operator-target의 normal·race·CGO-disabled 여섯 작업은 모두 success다.
각 core는 12 packages·54 run/pass, operator-target은 2 packages·12 run/pass이며 모두 skip 0이다.
공통 catalog 관측기의 물리 변경 대조와 관계 Count/MIN의 실제 DB 실행을 포함한다.
Linux amd64 normal의 cold external CLI milestone도 `GODJ_COLD_BUILD=1`, 1 package·2 run/pass·skip 0으로 완료했다.

Python 3.12.13/3.13.15/3.14.3/3.14.7 호환성 작업은 각각 `PYTHON_SUITE_VERIFIED tests=274 skips=4`로 완료했다.
그 4개는 고정 profile 전용 검사이며 exact darwin/arm64 작업이 별도로 실행했다. Exact 작업의 root Python suite는
274 tests·DRF 전용 3 skips, locked oracle 대조는 성공했다. DRF 검사는 별도 의존성을 갖춘 호환성 작업과
위 로컬 고정 DRF 환경의 274 tests·skip 0 결과가 검증한다. 비대상 skip을 실제 실행으로 계산하지 않는다.
Linux reference 작업의 root Python suite는 같은 고정 profile 4개와 DRF 3개를 제외한 274 tests·7 skips였다.
그 작업의 reference catalog·GoDj product expectation 대조, generated drift 및 32-bit Linux 검사는 모두 성공했다.

두 capture는 같은 run의 성공한 attempt 1 normal producer가 생성했다. `capture_artifact.py resolve`로 실제 producer job과
artifact ID를 확인하고, `verify`로 repository/run/attempt/checkout·payload checksum을 검증했다.
두 Go consumer의 `Load`도 현재 제품 작업 사본에서 canonical format·profile·checksum·behavioral source binding을 검증했다.
Hosted conformance consumer도 같은 artifact ID와 producer attempt 1을 확인해 제품 동작 대조를 완료했다.
최종 run attempt 2에서도 resolver와 provenance 검사는 성공한 producer attempt 1을 올바르게 선택·검증했다.
SYS-020의 loss/divergence/drift/secret은 모두 0이며 restart와 barrier 조건을 만족한다. SYS-029의 실제 PostgreSQL·SQLite
두 backend 모두 Admin/API 인증과 서로 다른 세 process의 provision/runtime/restart를 확인했다.

| Capture | Artifact / producer job | Payload SHA-256 | Source binding |
|---|---|---|---|
| SYS-020 two-process | `10117570038` / `102579851272` | `58c41d0f78e08b21f6cdc642516ba88cf3376f889c1822bb3bcb072b4a536305` | 322 files / 3,742,404 bytes / `2482656983e81f20966666af7de5a7a1728b1c4460524b7e02811a63e4e8b04e` |
| SYS-029 external operator | `10117537379` / `102579851285` | `1ac5c01249a676fd5750cafd97be876256fc78d01d6ca1dc4b6d94862591cd43` | 383 files / 3,492,733 bytes / `b20cb7120a6a9ca958bdf66424d0132f475dfb7fc4dbdcece7100ea9731defd8` |

최종 제품 검증 이후에는 ADR-0039·0044, CURRENT, 이 실행 증거와 work의 Markdown 다섯 파일만 정리했다.
제품 소스 diff가 없고 두 behavioral source binding이 위 값과 같음을 확인했다. 후속 문서에는 링크·상태·diff 검사를 적용한다.

## GDJ-0065 — 코드와 검증 체계의 중복 정리

- 작업과 감사 항목별 처리: [GDJ-0065](../../work/0065-codebase-refactoring.md).
- 기준: `341659d290ed0d344e1db51de086c5d4baac5f4e`, 기존 Draft PR #1의 동일 작업 브랜치.
- 로컬: 2026-09-08 KST, Go 1.26.5 darwin/arm64, 기준 위 변경 작업 사본.
- 상태: 영역별 구현·affected 검증·process smoke·독립 리뷰와 수정 소스의 Hosted full scope를 완료했다.

### 로컬 checkpoint

| 실제 실행 | 결과와 범위 |
|---|---|
| `./templates ./serializers ./admin ./examples/article/articleapp` normal | PASS. 외부 값·metadata 변경 격리, forloop first/last와 중첩 scope, 준비된 encoder/projector, Article no-op·update/patch·rollback을 확인했다. 후속 loop scope와 Admin index 정리 뒤 해당 package를 다시 실행해 PASS했다. |
| `./schema/... ./query/... ./orm ./codegen ./codegen/consumertest` normal | PASS. Generated namespace와 receiver 분리, dynamic relation, At limit/offset/cache, 전체 consumer 조립. 소비자 fixture의 facade 인자 누락 수정 뒤 해당 두 테스트를 다시 실행했다. |
| `./migrations/... ./db/... ./systemstate ./internal/migrationautodetect ./internal/projectmigration/... ./internal/irresource` normal | PASS. Built-in 경계, loaded graph·base-state 격리, budget·hash, rows/prune, operation seal과 변조 검증을 포함한다. PostgreSQL 서비스가 필요한 실제 검사는 이 로컬 실행의 성공으로 주장하지 않는다. |
| projectcheck의 다섯 protocol normal | PASS, 281 test pass event, skip/fail 0. 공통 code 집합과 명령별 exit 차이·strict wire·오류 우선순위. |
| Article/Helpdesk 전체와 `./internal/projectcheck ./internal/projectcheck/linked ./internal/projectgenerate/... ./cmd/godj ./project` normal | 관련 패키지를 실행하고 발견된 세 원인을 수정했다. 실패했던 adminapp/projectgenerate/linked 전체를 다시 실행해 PASS했다. PostgreSQL 서비스·helper entry point·macOS deleted-cwd 조건의 skip은 미실행으로 남긴다. |
| `make generate-check` | PASS. Helpdesk·Article·relationfixture의 현재 generated bytes/manifest와 checked-in relation product 일치. |
| `go test -run '^$' -p 2 ./...` | 저장소 전체 127 package compile PASS. 테스트 본문 실행이나 다른 platform의 compile 성공을 뜻하지 않는다. |
| 검증 helper·attestation·protocol·CI tools | 관련 Go packages PASS, Python CI tools 33 tests PASS. Source binding·complete inventory·suite 선택·owner/sentinel 이전을 확인했다. |
| Django·DRF·reference | Django 274 tests PASS, 7 skips(DRF 전용 3개·exact-profile 전용 4개). Locked DRF 환경에서 API-auth 5/5 PASS로 해당 3개를 실행했다. Reference catalog는 54회 독립 contractcheck PASS. Exact-profile oracle 실행은 Hosted가 소유한다. |
| 공유 helper의 실제 SQLite process 소비자 | targeted-migrate, showmigrations, migrate, operator, runserver의 관련 normal smoke PASS. Runserver 강제 descendant cleanup도 실행했다. |
| 문서·형식·workflow | `make docs-check format-check`, `git diff --check`, actionlint v1.7.12의 두 workflow 검사 PASS. |

초기 통합 검사에서 세 원인을 발견했다. Article Service의 과거 Admin sentinel 기대값을 현재 core repository 오류로 고쳤고,
실제 Admin callback의 sentinel 변환은 유지했다. Writer의 최초 replay를 Detect로 옮기면서 바뀐 오류 분류는 catalog 실패로
복원했다. External generated-product fixture는 새 공통 internal package 의존성을 포함하도록 수정했다. 제품의 publication
recovery assertion을 약화하지 않았다. 최초 실패 기록을 최종 PASS로 덮어 해석하지 않는다.
Python 공통 normalization을 적용하며 생긴 helper/지역 변수 이름 충돌도 수정한 뒤 전체 affected suite를 재실행했다.

독립 리뷰는 root 제품/CLI 변경, DB/migration 변경, 검증 catalog/CI/process/source binding 변경을 교차 검토했다.
Admin projector에서 남아 있던 행별 field index 재구성을 추가로 제거했다. 리뷰 자체를 DB·race 실행으로 세지 않는다.

### 한정된 성능 관찰

Apple M3 Pro의 짧은 microbenchmark이며 전체 DB/HTTP 처리량이나 장기 메모리 사용량의 보장은 아니다.

| 작업 | 관측 |
|---|---|
| Sparse Audit prune | 보관 한도 10,000과 1,000,000 모두 240 B/op. 이전 코드의 capacity+1 ID 배열은 각각 약 80KB·8MB였으며 현재는 그 선할당이 없다. |
| PostgreSQL 1,024-operation intent 검증 | 현재 operation seal 약 1.37µs·864 B/op, 비교용 전체 seal 약 1.008ms·731,631 B/op. 전체 seal은 preflight와 완료 시 계속 실행한다. |
| Serializer | 준비된 encoder 1,328 B·5 allocs/op, 같은 새 API를 매 row 준비하면 2,384 B·10 allocs/op. 과거 API 전체와의 역사적 benchmark 비교가 아니다. |
| Template 1,000회 loop | 동일 작업 사본에서 map-copy scope 1,686,874 B·6,760 allocs/op, linked scope 1,046,461 B·5,759 allocs/op. 100회 짧은 실행이며 전체 baseline 대비 속도 향상률로 해석하지 않는다. |

### 동일 분류의 소스 집계

`scripts/sourceinventory`로 기준 commit과 변경 작업 사본을 비교했다. 새 helper·테스트, 공백·주석을 포함한다.
초기 감사의 단순 generated marker 분류 대신 같은 AST 기반 분류를 양쪽에 적용했다.

| Go 역할 | 기준 줄 수 | 구현 후 | 변화 |
|---|---:|---:|---:|
| Framework·CLI·generator·support | 82,273 | 80,431 | -1,842 |
| 테스트 | 165,402 | 163,688 | -1,714 |
| Conformance 지원 | 53,478 | 53,541 | +63 |
| 직접 작성한 예제 | 4,157 | 3,968 | -189 |
| 생성물 | 6,805 | 6,723 | -82 |
| **Go 합계** | **312,115** | **308,351** | **-3,764** |

Python은 36,523→36,442줄(-81)이며 **Go+Python 합계 3,845줄 순감소**다. Go는 870→892파일,
Python은 134→139파일이다. 작게 분리한 공통 소유자·회귀 테스트가 늘었으므로 파일 수 감소를 목표로 삼지 않았다.
Makefile·JSON catalog·문서는 이 소스 분류 합계에 포함하지 않는다.
기준/구현 후 source digest는 `039e4f21d3772bf2ba69cbdf9265d72c0ba2385c42086bf1e5d708b3d548988d` /
`3c1c2ba9a6523f16101a0a29b8a060473f22ad74d91ee48065940e0b6f72786c`다.

실행 catalog를 정적으로 대조하면 full scope의 Linux amd64에서 모드당 26개 package 선택 중복을 정리했다.
25개 relation package와 runserver 1개로, normal/race/CGO-disabled 합계의 해당 선택은 156→78이다.
이 중 18개 package에 Linux test 파일이 있고 8개는 fixture 지원 package이므로 이를 테스트 suite 78회 절감으로 표현하지 않는다.
별도로 root test 6개를 선택하던 godjcheck relation subset 명령 3회도 생략한다. Go runner subset은 이전 full scope에서도
생략했으므로 새 절감에 포함하지 않는다. 기존 Portable Ubuntu 24.04와 relation/project-check 22.04를 모두 24.04로 맞춘
환경 정책 변경과 소유권 이전의 결과이며, 같은 과거 환경의 실행 시간·비용을 실측한 절감률이 아니다.

### 초기 Hosted 검사와 수정

최초 구현 `105c09fe76a8f6f8e62608f29d675b6ec87a8f8c`의
[CI 34175399787](https://github.com/progresshans/godj/actions/runs/34175399787)에서 PostgreSQL core의 normal/race/CGO-disabled가
같은 `TestPostgresRevisionFenceCrossProcessIntegration`의 초기 빈 이력 비교에서 실패했다. 공통 Clone은 빈 목록을 일관되게
반환하지만 기존 integration helper가 `reflect.DeepEqual`로 nil과 empty를 구분했다. 이력의 개수·순서·App/Name을 비교하는
`slices.Equal`로 고쳤으며 history/fingerprint unit과 PostgreSQL test package compile이 통과했다. 실행 본문이나 required/no-skip
검사는 제거하지 않았다. 실제 교차 프로세스 검사는 아래 수정 소스의 Hosted에서 다시 실행해 통과했다.

초기 PR feedback은 성공했지만 전체 CI 성공이 아니며, 수정 소스로 전체 실행을 시작하면서 이전 미완료 job은 취소됐다.
로컬 전체 `make ci`와 Hosted 전체는 중복 실행하지 않았다.

### 수정 소스의 Hosted 검증

[CI 34175865564](https://github.com/progresshans/godj/actions/runs/34175865564)는 제품 소스
`ce86851372a0fc29292aca26381482d5f24429eb`의 full scope를 검증했다. PR checkout은
`d7ed3cfaa7341a994e5774f4c52f0e0e4940e2c2`이며 두 commit의 전체 tree diff가 0인 것을 확인했다.
2026-09-08 10:46 KST 최종 conclusion은 success이며, 74개 고유 job이 모두 completed/success다.
Aggregate는 `scope: full`, `full_platform_verified: true`와 아홉 필수 owner의 성공을 확인했다.
Linux/macOS amd64·arm64의 normal/race/CGO-disabled, PostgreSQL 17.10, 32-bit Linux, cold external build,
고정 Django/DRF oracle과 Python 3.12.13/3.13.15/3.14.3/3.14.7을 포함한다. 수정 소스의 전체 실행은 attempt 1에서 완료됐다.

PostgreSQL 17.10의 core/operator-target은 normal·race·CGO-disabled 모두 success다. 각 core는
11 packages·44 run/pass, operator-target은 2 packages·12 run/pass이며 모두 skip 0이다.
초기 실패했던 교차 프로세스 revision-fence 검사도 이 required/no-skip 실행을 통과했다.

두 capture는 성공한 같은 run의 attempt 1 normal producer가 생성했다. `capture_artifact.py resolve`가 실제 producer job과
artifact ID를 확인했고, `verify`가 해당 PR checkout의 별도 작업 사본에서 repository/run/attempt/checkout·checksum을 검증했다.
두 Go consumer의 `Load`도 현재 제품 작업 사본의 canonical format·profile·checksum·behavioral source binding을 검증했다.
Hosted conformance consumer도 같은 두 artifact와 producer attempt 1을 확인해 모든 product suite 대조를 통과했다.

| Capture | Artifact / producer job | Payload SHA-256 | Source binding |
|---|---|---|---|
| SYS-020 two-process | `10037287698` / `101905103605` | `f74395f956d3003fba30068fa3422a40c156fd9d63faac2ce0eac442e07eda02` | 321 files / 3,742,523 bytes / `50e8e106b6019937a04b916aa4b6b2fe85a6e824a85cd04b7e5ad7b377539cc6` |
| SYS-029 external operator | `10037284632` / `101905103693` | `ceb1226265816c78d9ef9ac3daa78865e881d1a6e0e288bcc21376d492479f64` | 382 files / 3,493,366 bytes / `77dcadb47945ef28eb60c46379372f3e398203e981df362f3e5f21ff9150e13f` |

## GDJ-0064 — 추가 결함 수정과 불변 값의 복사 정리

- 작업: [GDJ-0064](../../work/0064-review-fixes-and-immutable-values.md).
- 검증 소스: `863724a06cd6fe75d1b8f6fe3a23b9cf10c8ec1d` 위 GDJ-0064 변경 작업 사본에서 실행했다.
- 환경: 2026-09-08 KST, Go 1.26.5 darwin/arm64.
- 범위: 사용자 요청에 따라 구현 및 간단한 로컬 확인만 완료했다. 전체 Hosted/DB/race/platform 검증은 실행하지 않았다.

| 실제 실행 | 결과와 한계 |
|---|---|
| `go test ./orm ./query` | PASS. panic 중 rows Close·flight 해제·waiter 재시도·partial cache 방지와 plan 파생 격리를 확인했다. |
| `go test ./codegen ./schema/ir -count=1` | PASS. 검증 후 취소 시 기존/첫 파일 publication 방지, canonical schema/hash와 기존 생성물 golden 비교. 별도 consumer matrix는 실행하지 않았다. |
| `go test ./web/... ./api/sessionauth ./api/bearerauth ./serializers ./admin` | 6 packages PASS. sibling/cross-site signed pair 거부, 정상 origin/logout, nil-header cookie, immutable response/serializer와 reverse round trip·escape 후 cap을 확인했다. |
| `go test -run '^$' ./conformance/runners/godj ./examples/article/apiapp` | 두 소비자 compile PASS. 해당 conformance 시나리오를 실행한 결과는 아니다. |
| 미사용 함수 제거 영향 compile | projectmigratetargetproduct, db/postgres, internal/projectgenerate, migrations/definition PASS. 실제 DB·외부 프로세스 검증은 아니다. |
| capture artifact Python unit | 6 tests PASS. producer attempt 1 → consumer attempt 2, repository/run/checkout/payload/producer mismatch, 실패·취소·누락·중복·미래 attempt 거부를 검사했다. GitHub API 응답은 fixture다. |
| Workflow·format·문서 | YAML 및 resolver → artifact ID download → producer-attempt 검증 연결 확인. 변경 Go 파일 gofmt와 `git diff --check`, 로컬 Markdown link 검사 PASS. Hosted 실행·actionlint는 하지 않았다. |

별도 공개 API 재현에서도 SQLite in-memory의 callback panic 후 다음 조회와 동일 QuerySet 재평가가 성공했다.
Nil-header cookie 적용은 panic 없이 204이며, 취소된 WriteFile은 context canceled를 반환하고 기존 파일을 보존했다.
Percent literal의 static/parameter Reverse는 모두 실제 HTTP request로 왕복해 200을 반환했다. 정상 로그인 뒤
sibling Origin과 유효 서명 쌍을 전송한 요청은 403과 mutation 0이었다. 이번 수정 후 Chrome 재실행은 하지 않았다.

동일한 1/100/1000개 flat 값·field 예제를 각 30회 관찰했다. Object.Value/Value.AsObject는 현재 0 bytes·0 allocations,
Plan.WithLimit은 8 bytes·1 allocation이다. 1000개 기준 변경 전은 각각 약 186KB와 57,352 bytes였다.
이 비교는 개별 호출의 할당 관찰이며 workload 처리량·race·최악 입력 성능을 검증한 것이 아니다.
App generation의 구조는 app당 Normalize 11→1회와 schema hash 6→1회로 정리됐고 생성 golden은 변경되지 않았다.

중간 compile에서 병행 ORM 편집의 `finishRowsLifecycle` 잔여 호출을 발견해 수정했으며 최종 영향 패키지 검사는 통과했다.
외부 재현 스크립트의 첫 실행은 정상 로그인에도 `Sec-Fetch-Site: same-site`를 설정하던 fixture가 새 정책에 의해
403으로 차단됐다. 정상 로그인은 same-origin, 공격 요청은 same-site로 구분한 뒤 재실행해 위 결과를 확인했다.

미사용 함수 17개는 선언 범위 679줄이며 주변 공백/import 포함 698줄을 제거했다. 기존 distinct-process registry와
그 실행 테스트는 보존했다. 새 오류·불변성 회귀 테스트가 추가됐으므로 이 수치를 전체 diff 순감소량으로 해석하지 않는다.

미실행: `make ci`, 전체 `make generate-check`, codegen consumer/process matrix, pinned Django/DRF conformance,
PostgreSQL·race·CGO-disabled·다른 OS/architecture·Hosted CI. 아래 GDJ-0063의 성공은 과거 고정 소스의 결과다.

## GDJ-0063 — 제품·검증 코드의 책임 정리와 결함 수정

- 작업: [GDJ-0063](../../work/0063-runtime-and-validation-ownership.md)
- 기준: `df19040fc0e9c5a0e966ceada4b2d4484bdc5a57`, 기존 Draft PR #1의 `codex/revision-fenced-migration-lifecycle`.
- 로컬: 2026-09-08 KST, Go 1.26.5 darwin/arm64. 기준 위의 변경 작업 사본에서 아래 집중·통합 검증을 실행했다.
- 구현 소스: `0badd6b369fa599ee5891665602990aaf44df3fe`.
- 상태: 구현·로컬 checkpoint·해당 고정 소스의 Hosted full scope 완료. 2026-09-08 KST에 최종 job 결과와 capture를 확인했다.

### 변경과 보존한 위험

| 항목 | 구현과 검증 소유권 |
|---|---|
| F1 | Store.Touch가 현재 record의 만료·갱신·삭제를 원자적으로 소유한다. real MemoryStore/SQLite Store에서 같은 ID의 detached-read 이후 갱신, 정확한 idle/absolute 만료, rotation, 취소를 barrier로 재현한다. Absolute 사례는 9/18/27초에 idle을 연장한 뒤 30초에 만료시킨다. |
| F2/S1 | `wirejson`의 strict lexical·구조/정수·bounded read와 `projectwire`의 Schema IR 표현/크기/사전 검사를 공유한다. showmigrations가 짝 없는 surrogate를 거부하며 유효 U+FFFD/pair는 보존한다. 각 envelope·resource budget·오류 우선순위·drain 정책, definition loader의 결정적 오류 선택은 별도다. |
| M1/S3 | Loader가 복사한 definition/source와 immutable planner를 게시하고 내부 lifecycle은 빌려 읽는다. 외부 Definitions/Sources는 필요한 view만 복사한다. Raw reconstructor 입력은 검증·복사하며 history check·plan·revision fence는 매 실행 새 snapshot에 적용한다. |
| S2 | 공용 CLI owner가 retained project/workspace/build/child/cleanup을 처리한다. 명령별 완성 응답·durable publication·TTY credential·foreground signal의 terminal policy는 active work 표와 실제 fault/process 회귀가 소유한다. |
| S4/S5 | DB 공통 AST projection/order/key/relation/scalar 검사를 `queryplan`으로 모으고 물리 quoting/schema/parameter/DDL/transaction은 backend에 남겼다. 지원 operation의 non-nil 포인터를 경계에서 값으로 정규화하며 typed nil·unknown/embedded operation 오류와 nested IR 복사를 유지한다. |
| S6 | Private AST/dataflow 해석 대신 import·I/O·공개 진입점 감사를 적용한다. 기존 우회 12개를 실제 global CLI로 빌드·실행하고 init marker로 build 실패와 구별한다. Private rename/import alias의 정상 대조, 두 renderer의 IR 변화, 정상 기대 SQL을 반환하는 stub이 변경 입력 대조에서 거부되는 실행도 확인했다. |
| S7 | Select/object/delete handler가 해당 case만 fresh DB에서 관측한다. Select 세 contract는 sibling을 합쳐 반복하던 18회 query 대신 실제 필요한 5/1/0회만 수행한다. Delete는 네 fixture 실행을 두 개로 줄였다. 전체 상태/trace/cache/rollback과 sibling 오염 대조, 12개 relation contract의 locked-oracle 비교를 유지했다. 전역 cache는 없다. |
| S8 | Pure protocol 7개를 Portable core로 분류하고 실제 CLI platform 선택에서 제외했다. argv 조합은 parser unit에 두고 outer/global/external은 대표 arity·identity·option의 pre-I/O 관측을 유지한다. SQL pipeline 대조는 Portable, PostgreSQL Phase D는 환경·schema·무접속·중단 경계를 소유한다. OS 이미지·arch/race/CGO/32-bit 경계와 필수 sentinel·no-skip·completion 검사는 유지했다. |
| M2 | `scripts/sourceinventory`가 `ast.IsGenerated`와 Git commit/현재 파일 바이트로 겹치지 않는 분류를 생성한다. 생성 머리말을 출력하는 수작업 코드·실제 header·test 우선순위·미완성 Go·untracked/ignored/deleted 파일·고정 commit 대조로 기존 오분류를 재현·차단했다. |

두 attestation의 새 wire/project helper를 source binding에 포함했다. 기존 `db/`·`migrations/` 범위가 새 queryplan/loadeddefinition도
포함하는지 mutation 대조를 추가했고 operator의 새 CLI owner, SYS-020 consumer의 relationstate도 확인했다.
`sessiontest`는 일반 Store 단위 회귀의 지원 코드이며 live capture producer는 아니다. 고정 codec fixture·oracle/expected·profile·lock을
현재 구현 소스에 맞추기 위해 덮어쓰지 않았다.

### 실제 집계와 측정

동일 도구로 기준 commit과 변경 작업 사본을 집계했다. 빈 줄·주석을 포함하고 새 helper·테스트·집계 도구도 포함한다.

| 분류 | 기준 | 구현 후 | 변화 |
|---|---:|---:|---:|
| Go 전체 | 848개 / 313,585줄 | 866개 / 312,019줄 | -1,566줄 |
| test Go | 164,801줄 | 164,724줄 | -77줄 |
| conformance 지원 Go | 54,100줄 | 54,099줄 | -1줄 |
| framework·CLI·generator·support Go | 83,722줄 | 82,234줄 | -1,488줄 |
| generated Go | 45개 / 6,805줄 | 45개 / 6,805줄 | 0 |
| 비-generated examples Go | 4,157줄 | 4,157줄 | 0 |
| Python 전체 | 134개 / 36,330줄 | 134개 / 36,350줄 | +20줄 |

합계는 **1,546줄 순감소**다. 목표 줄 수에 맞춘 테스트 삭제나 줄바꿈 압축은 하지 않았다.
기준/구현 후 Go+Python source digest는 각각 `128890711b9e7c1e6b1bbdb6531475edcda01dfb3512fc434bdbb6902062b8c9` /
`44670e2f2d2f27bed3cc0b581d6c05b064617cfedcd0c0d73018c38eee1ba359`다. 아래 GDJ-0062의 generated 분류도 정정했다.

Operation 0개인 definition 1/100/1,000개로 같은 Digest probe를 실행한 결과 호출당 allocation은 모두 0회/0 B였다.
이전의 2회/160 B, 2회/16,384 B, 2회/약 164 KB와 같은 입력 조건이다. 전체 migration 시간·처리량의 개선율을 뜻하지 않는다.

### 로컬 checkpoint

편집 중 compile 이후 각 묶음의 normal을 실행했다. 세션 3 package와 관련 실제 observation, protocol 10 package,
migration/definition, CLI owner 전체, SQLite/compiler와 relation observation의 정상·부정 대조가 통과했다.
Source 또는 통합 지원 코드가 바뀐 이후 필요한 다음 checkpoint도 실행했다.

A: `./sessions ./systemstate ./web/sessionauth ./migrations/... ./db/sqlite ./db/internal/...`,
`./internal/wirejson ./internal/projectwire ./internal/projectcheck/...`, 두 projectgenerate/projectmigration protocol,
`./conformance/internal/relationstate`와 select/object/delete product.
B: `./project ./internal/projectgenerate/... ./internal/projectmigration/...`, `./conformance/runners/godj`,
`./conformance/cmd/godjcheck`, 두 attestation, `./conformance/relationfixture/...`.
C: `./conformance/{projectmigrateproduct,projectmigratetargetproduct,projectshowmigrationsproduct,projectoperatorproduct,migrationwriterproduct,runserverproduct}`.

| 실제 실행 | 결과 |
|---|---|
| A `go test -json -race -count=1 -p=2 -timeout=20m` | 24 packages, run/pass/skip 2,399/2,398/1, fail 0 |
| A `CGO_ENABLED=0 go test -json -count=1 -p=2 -timeout=20m` | 24 packages, run/pass/skip 2,399/2,398/1, fail 0 |
| B `go test -json -count=1 -p=1 -timeout=25m` | 15 packages, run/pass/skip 1,029/1,028/1, fail 0 |
| C `go test -json -count=1 -p=1 -timeout=25m` | 6 packages, run/pass/skip 88/81/7, fail 0. DSN 미제공 PostgreSQL은 Hosted owner에 남음 |
| PostgreSQL `-run '^Test(Compile\|Compiler)'` normal/race/CGO-disabled | 각 30 top-level tests / 64 run/pass, skip/fail 0. 실제 PostgreSQL I/O 검증은 아님 |
| SQLMigrate 외부 전체 normal | 4 top-level / 43 run/pass, skip/fail 0. 이후 추가한 fixed-output 대조와 공용 성공 비교를 포함한 최종 controls normal은 31 run/pass, skip/fail 0 |
| 변경 argv parser/outer/dispatch normal | 4 top-level / 7 run/pass, skip/fail 0 |
| 고정 Python exact | 274 tests, error/failure 0, DRF 미설치 skip 3. Hosted DRF 환경이 해당 identity의 실행을 소유 |
| 고정 Django/DRF oracle check | 27개 실제 재생성 결과와 고정 바이트 일치 |
| generated drift | Helpdesk·Article·공통 relation bundle 및 별도 generated relation 회귀 PASS |
| affected vet·CI 도구 | PASS. CI Python 도구 22 tests, 실제 platform 선택은 project/projectcheck/linked만 포함 |
| 문서·format·diff·Actions 문법 | PASS. actionlint 1.7.12, ShellCheck/Pyflakes는 실행하지 않음 |

A의 skip은 `TestMakemigrationsCrashHelper`, B는 `TestPublicationCrashHelper`의 직접 진입 guard다. 실제 부모 crash/publish 회귀는
각각 성공했다. C의 skip은 migrate PostgreSQL 2개, targeted migrate 1개, showmigrations 1개, operator 2개, runserver 1개다.
고정 Python의 skip은 `APIAuthenticationScenarioTests`의 DRF missing/invalid token, permission/CSRF/profile,
full-reference deterministic/raw-bearer-free 세 검사다. 이 미실행 환경을 로컬 PASS로 세지 않는다.
Go 통합·외부 명령·최종 SQL controls의 stderr는 0 bytes이며 JSON 시작/종료 inventory도 대조했다.

고정 Python은 `GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uvx --from uv==0.10.12 uv run --frozen python -m unittest discover -s conformance/runners/django/tests -v`,
oracle은 같은 고정 uv 환경에서 `make oracle-check`를 실행했다. DRF 하위 프로젝트는 자신의 `.venv`를 사용했다.

중간 실패도 보존했다. CLI 공통화 첫 normal은 build 직전 취소 barrier가 한 번 줄어든 두 사례에서 실패했고 실제 build 직전에
barrier를 복원한 전체 normal·A race/CGO-disabled가 통과했다. 첫 SQL 우회 대조는 test fixture가 marker 경로를 공유해 실패했고
case별 경로로 고친 뒤 실제 우회·rename·입력 변화 대조가 통과했다. 최초 actionlint는 offline Go module metadata 조회가 막혀
실행되지 않았으며 고정 버전의 정상 module 조회로 실행한 두 workflow 문법 검사가 통과했다.

### 초기 Hosted 실패와 수정

최초 구현 `293d556fc591beb5dc4e1eb8e90d11092dede145`의 [full CI 34155216966](https://github.com/progresshans/godj/actions/runs/34155216966)와
[PR feedback 34155192867](https://github.com/progresshans/godj/actions/runs/34155192867)에서 `admin/site_test.go`의 Store wrapper 한 곳이
이전 `Touch(...)(Record,bool,error)`를 유지해 컴파일 실패했다. 로컬 집중 패키지 선택에서 누락한 테스트 소비자이며 환경 오류가 아니다.
Wrapper를 `TouchStatus`로 수정하고 전체 저장소의 Store 구현·호출을 다시 검색했다. `go test -run '^$' ./...`의 121 packages와
`go vet ./...`가 통과했다. Admin 전체 normal/race/CGO-disabled는 각각 86 run/pass, skip/fail 0이다.
집계의 줄 수는 같고 source digest는 위의 수정 후 값으로 갱신했다. 이 수정 소스로 Hosted full scope를 새로 실행해 아래 결과를 확인했다.
최초 `293d556` 실행은 수정 소스의 실행을 시작하면서 취소됐으며 완료 근거로 사용하지 않는다.

### 고정 소스 Hosted 완료

[CI 34155714775](https://github.com/progresshans/godj/actions/runs/34155714775)의 대상은
`0badd6b369fa599ee5891665602990aaf44df3fe`이며 최종 conclusion은 success다. 74개 고유 job의 최종 결과가 모두 success이고,
aggregate가 `scope: full`, `full_platform_verified: true` 및 아홉 필수 owner의 완료를 확인했다.
Linux/macOS amd64·arm64의 normal/race/CGO-disabled, PostgreSQL 17.10, 32-bit Linux, cold external build, 고정 reference와
Python 3.12.13/3.13.15/3.14.3/3.14.7을 포함한다. 로컬 전체 `make ci`는 중복하지 않았다.

Attempt 1은 72개 job이 성공했지만 macOS 26 race의 targeted-migrate 준비에서 pgx 모듈의 checksum 서버
`proxy.golang.org/sumdb/sum.golang.org/supported` 접속이 timeout돼 해당 job과 aggregate가 실패했다.
코드·checksum 정책을 바꾸지 않고 `gh run rerun 34155714775 --failed`로 실패 job과 aggregate만 재실행했다.
Attempt 2의 해당 product는 33 run/pass, skip 0이며 최종 aggregate도 통과했다. 이전에 성공한 72개 검증은 attempt 1에서 실행한
같은 소스의 결과다. 전체를 attempt 2에서 다시 실행했다고 주장하지 않는다.

PostgreSQL의 core/operator-target 두 그룹은 세 mode 모두 required/no-skip 검사를 통과했다. Core는 각 11 packages·44 run/pass,
operator-target은 각 2 packages·12 run/pass, 모두 skip 0이다. 로컬의 PostgreSQL skip 7개는 이 필수 roster에 포함돼 실행됐다.
Python 3.13.15의 실제 log에서 로컬 DRF skip 3개가 각각 `ok`인 것을 대조했다. 네 compatibility job도 discovery/completion guard를
통과했다. Compatibility의 exact-profile skip 4개는 별도 exact Darwin owner가 담당한다.

두 capture의 producer와 consumer는 모두 **attempt 1**이다. 성공한 normal PostgreSQL job이 만든
`systemstate-postgres-1`·`operator-postgres-1`을 같은 attempt의 conformance job이 받아 사용했다.
로컬에서도 `capture_artifact.py verify`로 repository/run/attempt/checkout·payload checksum을 확인하고,
두 Go consumer의 `Load`로 canonical format·profile·checksum·현재 behavioral source binding을 확인했다.

| Capture | Payload SHA-256 | Source binding |
|---|---|---|
| SYS-020 two-process | `f74d40b4ba59b91f3239bfc0a8c7e940aa4c1bf64f80ee51dbb5a84d03d827f1` | 307 files / 3,779,434 bytes / `822288d6cff1f0733ffebee3ed8317e543c718359a26a67bca3b056d18a525fa` |
| SYS-029 external operator | `a893c981d11b3b740b7bd941424e5ccb34d9bdf2442a4a07ef109644f4a64e76` | 365 files / 3,578,563 bytes / `6a27b8f4f199276ec0406e12674b9adcca54e1d3c776276edc6390302dc09f41` |

SYS-020은 두 writer의 동일 schema·barrier·restart 보존과 divergence/loss/drift/secret 0을 관측했다.
SYS-029는 PostgreSQL/SQLite 각각 provisioning 1 process, runtime 2 processes와 credential 1 row,
실제 Admin/API 인증·독립 restart 및 secret/state loss/schema drift 0을 관측했다.
[수정 구현 PR feedback](https://github.com/progresshans/godj/actions/runs/34155695860)도 통과했다.
완료 기록은 위 구현 소스 이후 Markdown에만 추가하며 source binding과 코드 집계를 바꾸지 않는다.

## GDJ-0062 — 테스트·검증 지원 코드 중복 점검

- 작업: [GDJ-0062](../../work/0062-validation-duplication-audit.md)
- 기준: `6ecf0b628465014ee1b1260454a08ce67713bd1a`, `codex/revision-fenced-migration-lifecycle`.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 기준에 아래 구현을 적용한 checkout에서 실행했다.
- 구현 소스: `c5b91c812af87c1450f5fdb881cb751491eb76be`.
- 상태: 구현·관련 로컬 checkpoint·고정 소스의 Hosted full scope 검증 완료. 2026-09-08 KST에 최종 결과와 capture를 확인했다.

### 검색 범위와 남긴 경계

Git 관리 Go 841개 파일(테스트 397개, 실제 generated 45개)과 Python 129개 파일 전체를 검색했다. Generated Go는 중복 삭제 대상으로
보지 않았다. Go 함수 10,029개를 파싱하고, 12줄 이상 본문의 exact-token/identifier·literal-normalized 후보 32/115개 그룹을
검토했다. Python은 같은 길이 기준 885개 함수의 AST 후보 10/29개 그룹을 검토했다. 파일 해시 목록·입력 로더·작은 포인터 복사·
DB seed·경로/환경 준비는 별도 검색으로 확인했다. 구조가 같다는 사실만으로 의미가 같은 것으로 판단하지 않았다.

| 검토 영역 | 처리와 남은 검증 소유자 |
|---|---|
| 고정 artifact 바이트·반복 입력 로딩 | `protocol/artifact_catalog_test.go`에 기존 기대 hash/size를 모았다. 각 계약 테스트는 phase·payload·provenance·mutation 비교를 유지하고 로더는 매번 새 값을 읽는다. |
| 관계 DB 준비·actual 스냅샷 | `internal/relationstate`가 여섯 관찰기의 동일 record/read를 소유한다. 다섯 seed와 네 표준 provisioning 경로를 공유하며 nullable key를 복사한다. 각 product의 DB·cache·정렬·취소·typed/dynamic 회귀는 유지했다. |
| 외부 프로젝트 준비 | `internal/testfixture`가 symlink 해석·별도 root·hash·민감값·환경 구성·동일 source import 감사를 공유한다. 환경 제거의 순서/중복 정책, timeout·출력 한도·command result·kill/reap 흐름은 각 owner에 남겼다. |
| attestation 읽기 | `internal/attestationio`가 duplicate-key/trailing JSON 거부와 bounded regular/source file 읽기를 공유한다. SYS-020/SYS-029의 schema·inventory·각각 4,096/8,192 files, 128/256 MiB 제한은 독립적으로 유지했다. |
| 일반 테스트 준비 | generator와 ORM의 fresh Schema fixture를 `internal/testschema`로 이동했다. CLI 결정성·oracle 입력은 table로 묶고 동일 negative-control/cookie/SQLSTATE-redaction helper만 공유했다. |
| Python 준비·표현 | 원자적 파일 교체 7곳, 같은 관찰 payload/row 변환, test-only decoder를 통합했다. 일반 strict decoder·PK decoder·permissive semantic decoder의 서로 다른 허용 범위는 유지했다. |
| 합치지 않은 구조 후보 | generated 프로그램 문자열 속 서로 다른 타입/실패 검증, 공개/내부 package 경계의 typed fake, DB 종류·실패 단계·savepoint SQL 분류·서로 다른 Article 모델 adapter, 실제 runtime과 테스트의 별도 관찰 구현을 유지했다. |

후속 검색은 새 파일을 포함했다. Go exact/shape 후보는 12/84개 그룹, Python은 1/14개 그룹이다. 남은 Python exact 본문은
호출하는 SQL statement classifier가 달라 결과 의미도 다르다. Go의 동일 본문도 owner별 resource bound, typed fake의 다른 package
계약, 실제 구현과 검증 사이의 독립성 등을 확인해 유지했다. 이 검색은 임의의 모든 부분 중복이 0이라는 주장이 아니다.

### 제거한 반복과 보존한 위험

- 180개 반복 reference file 대조를 **97개 고유 파일**의 기대 hash/size로 통합했다. 기대값은 기존 검사에서 복사했고 현 파일을
  해시해 새 기대값으로 덮어쓰지 않았다. SHA256SUMS 목록/형식 검증과 메모리에서 복원한 역사적 manifest의 checksum은 별도 의미라 유지했다.
- retired migration-relation의 구현 파일 8개를 특정 과거 SHA에 묶던 잠금을 제거했다. 관련 고정 manifest·NI·oracle 3개의 기존
  바이트 잠금과 실제 Django/Go 동작 검증은 유지했다. 구현 파일을 변경할 때 기대 SHA를 다시 적는 방식으로 처리하지 않았다.
- Python의 oracle 선택 20개·regeneration 대상 16개·동일 프로세스 결정성 14개·두 hash seed 결정성 5개 입력은 그대로 table에 남았다.
  마지막 5개는 서로 다른 hash seed `17`/`982451653`으로 **독립 자식 프로세스 10개**를 실제 실행하고 서로 및 고정 oracle과 대조한다.
- Go CLI 결정성 4개 입력도 각각 독립 actual을 두 번 생성한다. oracle 성공 3개의 count/첫 ID/마지막 ID assertion을 모두 유지했다.
  bundle accessor caller-owned-view 중복은 기존 별도 ownership test가 소유한다.
- 관계 fixture의 oracle-blind source 검사에 공통 actual helper를 추가했다. fresh seed의 slice/nullable pointer 오염 대조를 추가했다.
  REL-004의 orphan FK 거부와 delete의 physical FK/rollback fixture는 특수 조건을 보존했다.
- 두 attestation 모두 공통 I/O helper를 source binding에 넣었다. SYS-020은 restart가 사용하는 공통 test fixture도 포함한다.
  새 helper의 변경이 binding을 바꾸는 부정 대조를 기존 mutation test에 추가했다. 다른 source의 과거 capture를 현재 PASS로 사용하지 않는다.

빈 줄·주석 포함, 기준/현재에 같은 방식으로 집계했다. Go 분류는 `_test.go` → generated → conformance 지원 → examples →
framework/CLI 순으로 서로 겹치지 않는다. 프레임워크 runtime/API·생성 Go·고정 reference/profile/lock에는 변경이 없다.

| 분류 | 기준 | 구현 후 | 변화 |
|---|---:|---:|---:|
| Go 전체 | 315,925줄 | 313,585줄 | -2,340줄 |
| `_test.go` | 166,681줄 | 164,801줄 | -1,880줄 |
| conformance 지원 Go | 54,560줄 | 54,100줄 | -460줄 |
| Python 전체 | 37,863줄 | 36,330줄 | -1,533줄 |
| Python `test_*.py` | 13,771줄 | 12,289줄 | -1,482줄 |
| generated / framework·CLI·generator·support Go | 6,805 / 83,722줄 | 6,805 / 83,722줄 | 0 |

GDJ-0063에서 `scripts/sourceinventory`의 `ast.IsGenerated` 판정으로 기준과 구현 후를 재집계해 위 분류를 정정했다.
이전 generated 57개/12,614줄에는 생성 머리말을 출력하는 수작업 생성기 12개/5,809줄이 잘못 포함됐다.
실제 generated는 45개/6,805줄이며 나머지 5,809줄은 framework·CLI·generator·support로 옮겼다.
이 정정은 아래 전체 줄 수·순감소량을 바꾸지 않는다. 당시 함수 중복 검색 결과는 당시 검색의 관측으로 남긴다.

Go/Python 코드 합계는 **3,873줄 감소**했다. Go의 테스트+conformance 지원 비중은 70.030% → 69.806%다.
최상위 Go `Test*`는 2,213 → 2,192개(TestMain 제외), Python reference suite의 unittest method는 325 → 274개다. 합친 입력과 부정 대조는 위와 같이
유지했으며 이 개수를 새로운 영구 잠금으로 만들지 않는다. 코드 절대량과 반복 준비를 줄인 결과이며 실행 시간 개선율을 주장하지 않는다.

### 로컬 checkpoint

아래 A/B와 exact Python/oracle 검증의 구현 소스는 `8c47cbd2ebf0510cf5fb4ba1f42920e8598dc5c6`다. 이후 변경은 아래 CI 완료 검사와
그 source inventory에 한정하며 해당 변경의 별도 검증을 이어서 기록한다.

A: `./codegen/... ./internal/testschema ./orm`, `./conformance/internal/{protocol,relationstate,testprocess}`,
`./conformance/relationfixture/...`, 여섯 relation product, `./conformance/runners/godj ./conformance/cmd/godjcheck`,
`./conformance/systemstate/attestation ./conformance/projectoperatorproduct/attestation`.
A의 race/CGO-disabled는 `./codegen/...` 대신 `./codegen`을 사용했다. 외부 generator consumer의 해당 mode는 Hosted가 소유한다.
B: `./conformance/{migrationwriterproduct,projectmigrateproduct,projectmigratetargetproduct,projectshowmigrationsproduct,projectsqlmigrateproduct,runserverproduct,systemstate/restart}`.

| 실제 실행 | 결과 |
|---|---|
| A, `go test -json -count=1 -timeout=15m` | PASS, run/pass 2,850, skip/fail 0 |
| B, `go test -json -count=1 -timeout=30m` | PASS, run 104 / pass 97 / skip 7 / fail 0 |
| A, `go test -json -race -count=1 -timeout=15m` | PASS, run/pass 2,778, skip/fail 0 |
| A, `CGO_ENABLED=0 go test -json -count=1 -timeout=15m` | PASS, run/pass 2,778, skip/fail 0 |
| 최초 `make python-test-exact` | 환경 FAIL: installed uv 0.12.3과 고정 0.10.12 불일치. 274 tests, error 21, skip 3 |
| 고정 uv 0.10.12로 exact Python unittest 재실행 | PASS, 274 tests, skip 3. 기본 Django 환경에 없는 DRF 의존 검사이며 Hosted DRF 환경이 실행을 소유한다. |
| 고정 uv 0.10.12의 `make oracle-check` | PASS, Django/DRF 27개 세트의 실제 재생성 결과와 고정 바이트 일치 |

위 Go 실행의 stderr는 모두 0 bytes다. B의 skip 7개는 DSN 미제공 PostgreSQL 검사이며 Hosted PostgreSQL 세 mode에서 확인한다.
편집 중 compile에서 발견한 남은 helper 호출/미사용 import는 실제 회귀 묶음 실행 전에 수정했다.
Python 고정 실행은 `GODJ_EXACT_PROFILE=1 PYTHONWARNINGS=error::ResourceWarning LC_ALL=C TZ=UTC uvx --from uv==0.10.12 uv run --frozen python -m unittest discover -s conformance/runners/django/tests -v`다.
Oracle 대조는 같은 uv 환경에서 `make oracle-check`를 실행했다. DRF 하위 프로젝트는 자기 `.venv`를 사용했으며 root VIRTUAL_ENV를 무시한다는
도구 경고가 있었지만 고정 DRF profile 검증과 전체 checksum 대조는 통과했다.

`make generate-check`는 Helpdesk·Article·공통 relation 프로젝트 및 별도 metadata relation fixture에서 PASS다. A/B의 `go vet`,
`make ci-tools-test`(17 tests), `make docs-check format-check`(83 documents), `git diff --check`도 PASS다.
전체 OS/arch·cold build·PostgreSQL·외부 process의 모든 mode는 마지막 Hosted 실행이 소유한다. 로컬 전체 `make ci`를 중복하지 않았다.


### 초기 Hosted 실패와 CI 완료 검사 수정

[CI 34100953054](https://github.com/progresshans/godj/actions/runs/34100953054), source `8c47cbd2ebf0510cf5fb4ba1f42920e8598dc5c6`의
Python 3.12.13/3.13.15/3.14.3/3.14.7 job은 모두 `Ran 274 tests`, `OK (skipped=4)`까지 통과했지만 후속 shell이 이전
325 tests/21 skips 수량을 요구해 실패했다. 관련 job ID는 `101675085715` / `101675085702` / `101675085617` / `101675085712`다.
이 실행의 남은 job을 취소했다. 최종 49 success / 5 failure(네 Python 완료 검사와 aggregate) / 20 cancelled이며 full PASS가 아니다.

고정 수량 grep을 `scripts/ci/python_tests.py`로 교체했다. 현재 발견한 각 testcase가 정확히 한 번 시작·종료했는지 확인하고,
실패·expected failure·예상 밖 skip과 subtest skip을 거부한다. 별도 exact profile job이 실행하는 네 검사만 portable skip을 허용한다.
성공 marker는 전체 확인 후에만 출력하며 workflow의 pipefail과 terminal marker 검사로 중단된 실행도 거부한다.
새 일반 회귀를 추가할 때 전역 test-count를 갱신하지 않는다. 새 CI 실행 코드는 두 attestation의 source inventory에도 포함했다.

수정 후 검증:

- `make ci-tools-test`: PASS, 21 tests. empty/duplicate/missing discovery, dropped execution, missing stop, failure/xfail 및 임의 skip 거부 포함.
- 두 attestation package의 `go test -count=1`, `go test -race -count=1`, `CGO_ENABLED=0 go test -count=1`: 모두 PASS.
- CI와 같은 isolated Python 3.13.15 + Django 6.1/DRF 3.18.0 환경에서 새 runner: PASS, 274 tests / 4 exact-profile skips,
  `PYTHON_SUITE_VERIFIED tests=274 skips=4`. 기존 reference 코드나 고정 입력을 변경하지 않았다.
- 같은 환경에서 workflow의 semantic digest 코드 실행: PASS, 311 scenarios / 1,081,058 bytes /
  `b8d53e874169009fcd4650c79f2a007e18307d2fddd07a07d970f28bce2ed3f5`.
- actionlint v1.7.12: PASS. ShellCheck/Pyflakes는 미포함.

### 고정 소스의 Hosted 통합 검증

- [CI 34102953736, attempt 1](https://github.com/progresshans/godj/actions/runs/34102953736): PASS, source
  `c5b91c812af87c1450f5fdb881cb751491eb76be`, **74개 job 모두 success**, 실패·취소 job 0.
- 최종 집계 job `101690984587`은 `scope: full`, `full_platform_verified: true`와 선택한 9개 owner의 성공을 확인했다.
  Linux/macOS amd64·arm64의 normal/race/CGO-disabled, PostgreSQL 17.10, Python compatibility와 고정 reference를 포함한다.
- 대표 relation job `101681409513`(Linux arm64 normal), `101681409311`(Linux amd64 CGO-disabled),
  `101681409602`(macOS amd64 race), `101681409606`(macOS arm64 CGO-disabled)는 각각 26 packages, run/pass 3,213, skip 0과 필수 package/sentinel 검사를 통과했다.
- Linux amd64 project-check의 normal/race/CGO-disabled job `101681409232` / `101681409159` / `101681409181`은
  Go runner 전체 run/pass 387, skip 0과 필수 relation sentinel을 확인했다. 별도 runserver의 skip 1개는 PostgreSQL DSN
  미제공 검사이며 아래 PostgreSQL owner에서 실행했다. normal의 별도 cold CLI build milestone도 PASS다.

PostgreSQL 17.10의 필수 selector·no-skip 검사는 여섯 조합 모두 PASS다. 로컬 B에서 DSN 미제공으로 건너뛴 일곱 검사도 이 owner들이 실행했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 mode의 집계 |
|---|---|---|
| core | `101681409085` / `101681408990` / `101681409168` | 11 packages, run/pass 57, skip 0 |
| operator-target | `101681409063` / `101681408976` / `101681409116` | 2 packages, run/pass 12, skip 0 |

동일 실행의 `systemstate-postgres-1`(artifact `10011349612`)과 `operator-postgres-1`(`10011333769`)을 다운로드했다.
두 provenance의 repository/run/attempt는 `progresshans/godj` / `34102953736` / `1`, checkout은
`add43c2a27594ec51889070e0f8831da81916885`다. GitHub commit API의 tree `2793b9bef9a843b28fe5a3299010a8f266b5e129`는
로컬 구현 소스 `c5b91c8`의 tree와 일치한다. payload SHA-256을 다시 계산해 provenance·SHA256SUMS에 일치함을 확인했다.

| Capture | payload SHA-256 | 현재 source binding |
|---|---|---|
| SYS-020 `postgresql-17.10-two-process-v1.json` | `8a6aaf5c46e4091962f657c6ab57f60c7543cc6464d85cc2f521c88a0dc3dca0` | 295 files / 3,754,305 bytes / `768cd230ad2a3fdd98170f44b00c916f6bec3f0df4950dbe098e99cd707b764d` |
| SYS-029 `postgresql-17.10-sqlite-external-operator-v1.json` | `54872237de7953453fb595a7e3694510b64074d0dd0de0315a281990ea8134ec` | 354 files / 3,624,447 bytes / `c79a582482a101cc563e25e43a0c19cc2c8e48584cc8927b98836830fa7263b5` |

두 source binding은 현재 checkout에서 계산한 scope·file count·payload bytes·digest와 일치한다. Reference consumer
job `101683454026`도 같은 실행의 provenance·현재 source binding·actual 비교를 통과했고, 32비트 Linux compile/관계 product
실행과 고정 Django/DRF checksum·reference 미변경 검사를 통과했다.

Exact Darwin job `101681408911`은 고정 profile·oracle 대조와 Python 274 tests를 통과했다(DRF 의존 3개 skip).
Python 3.12.13/3.13.15/3.14.3/3.14.7 job `101681408958` / `101681408952` / `101681408942` / `101681408955`는 모두
`PYTHON_SUITE_VERIFIED tests=274 skips=4`와 311 scenarios의 고정 semantic digest 검사를 통과했다. Portable의 exact-profile
skip 4개는 exact에서, exact의 DRF skip 3개는 네 portable 환경에서 모두 `ok`임을 testcase identity로 대조했다.
Full scope에서 exact job의 중복 Go lifecycle step은 비대상이며 relation/project-check matrix가 해당 검증을 소유한다.

구현 이후 완료 기록은 Markdown 세 파일에 한정한다. 문서 변경에는 문서·링크·상태·diff 검사를 적용하며 전체 제품 검증을 반복하지 않는다.


## GDJ-0061 — 검증 fixture와 CI 실행 소유권 정리

- 작업: [GDJ-0061](../../work/0061-validation-fixtures-and-ci-ownership.md)
- 기준: `ddb8c5135533f9fb7fc280d0446f2688b7b6b649`, `codex/revision-fenced-migration-lifecycle`.
- 구현 소스: `21ceeb56021e65c7c718eef93a898150812b6c32`.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 기준에 이번 구현을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 소스의 Hosted full scope 검증 완료.

### 변경과 검증 소유권

관계 query/object/reverse/prefetch/select/delete product는 `conformance/relationfixture`의 동일 Author/Post 프로젝트를 소비한다.
중복 생성 Go 파일 42개를 없앴으며 whole-project drift·declaration bootstrap·앱 의존성·observer 경계 검사를 공통화했다.
기본 metadata-only relation fixture는 다른 생성 계약을 검증하므로 남겼다. 각 product의 실제 DB·cache·취소·rollback·typed/dynamic
회귀는 유지했다. Go AST 대조에서 이들 runtime test 본문은 변경되지 않았다.

Go runner의 oracle 일치 9개와 결정성 6개 테스트는 9개 subtest로 통합했다. 결정성을 검증하던 6개 입력은 독립 actual을 두 번
생성하며 첫 actual로 고정 oracle도 대조한다. 별도 준비로 세 번 만들던 중복을 제거했고 나머지 3개 입력은 기존처럼 한 번 생성한다.
이전 미배포 facade v2의 1,060줄 복제본과 전용 검사를 제거했다. 현재 full union의 **모든 generated file**을 다른 snapshot과 섞어
컴파일이 실패하는 검사, 기능별 prerequisite 실패, bundle 복사·순열 결정성, publication 실패 시 기존 결과 보존은 계속 실행한다.

Go 파일을 기준/현재에서 동일하게 집계했다. 빈 줄·주석 포함, `_test.go` → generated marker → conformance 지원 → examples →
framework/CLI 순으로 중복 없이 분류했다. 비교하는 양쪽 소스에서 직접 집계했다.

| 분류 | 기준 | 현재 | 변화 |
|---|---:|---:|---:|
| Go 전체 | 321,999줄 / 882개 파일 | 315,925줄 / 841개 파일 | -6,074줄 / -41개 파일 |
| `_test.go` | 168,081줄 | 166,681줄 | -1,400줄 |
| generated Go | 17,288줄 | 12,614줄 | -4,674줄 |
| conformance 검증 지원 Go | 54,560줄 | 54,560줄 | 0 |
| framework/CLI Go | 77,913줄 | 77,913줄 | 0 |

테스트와 conformance 지원 Go의 합은 222,641줄에서 221,241줄로 줄었다. 비중은 생성 코드라는 분모도 줄어 69.14%에서 70.03%가
됐다. 비중 하락을 성과로 주장하지 않는다. 실제 AST의 최상위 `Test*` 함수는 2,237개에서 2,213개로 줄었다(30개 삭제·6개 추가).
문자열 내부의 예제 `func Test...`와 TestMain은 세지 않았으며 이름·개수 자체를 새로운 영구 잠금으로 만들지 않았다.

### 로컬 checkpoint

공통 affected 집합 A:
`./conformance/relationfixture/...`, `./conformance/relationqueryproduct`, `./conformance/relationobjectproduct`,
`./conformance/relationreverseproduct`, `./conformance/relationprefetchproduct`, `./conformance/relationselectproduct`,
`./conformance/relationdeleteproduct`, `./conformance/runners/godj`, `./internal/compiletest`, `./internal/projectgenerate`,
`./conformance/internal/protocol`. 명령은 아래 flag와 해당 package를 `go test`에 직접 전달했다.

| 실제 실행 범위 | 결과 |
|---|---|
| 초기 affected `go test -run '^$'` | compile-only PASS, 테스트 본문 미실행 |
| A 및 `./codegen/... ./conformance/postgresproduct`, `-json -count=1 -timeout=15m` | 첫 checkpoint FAIL: run 2,243 / pass 2,233 / skip 2 / fail 8. 공통 source 검사기의 주석 오탐과 옛 CI include/SQLite job 가정 두 원인 |
| 수정한 `./conformance/relationfixture/... ./conformance/internal/protocol`, `-json -count=1 -timeout=15m` | PASS, run/pass 1,190, skip/fail 0. 나머지 package는 첫 checkpoint에서 PASS |
| A, `-json -race -count=1 -timeout=15m` | PASS, run 1,859 / pass 1,858 / skip 1 / fail 0 |
| A, `CGO_ENABLED=0`, `-json -count=1 -timeout=15m` | PASS, run 1,859 / pass 1,858 / skip 1 / fail 0 |
| `make generate-check`, A의 `go vet` | PASS. Helpdesk·Article·공통 relation 프로젝트와 별도 metadata fixture drift 없음 |
| `make ci-tools-test` | PASS, 17 tests. package 분류·scope 누락/skip/실패·malformed output 거부 포함 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/feedback.yml` | PASS. ShellCheck/Pyflakes 미포함 |
| `make docs-check format-check`, `git diff --check` | PASS, 문서 82개 |

수정한 observer 검사는 comment를 제외한 식별자·decoded string/import를 확인하며 escaped oracle 경로, file reader, NI shortcut과
문법 오류의 부정 대조를 통과했다. 설명 주석의 문구를 보존하기 위해 runtime 동작이나 oracle 경계를 완화하지 않았다.
normal의 PostgreSQL E2E skip은 로컬 DSN 미제공이며 최종 Hosted PostgreSQL mode들이 실행을 소유한다. publication crash helper의
직접 진입 skip은 부모가 별도 자식 프로세스로 실행하며 부모 회귀는 PASS다. 테스트 없는 generated/support package는 소비자 검증으로
확인하며 test pass 수에 넣지 않는다. 위 Go 실행의 stderr는 모두 0 bytes다.

### CI 실행 경계 확인

SQLite 전용 네 job은 관계 matrix와 같은 Linux/macOS amd64·arm64에서 같은 migrations/SQLite package의 normal·race·CGO-disabled를
반복했다. 이 job 정의를 삭제하고 관계 matrix가 전체 package와 normal vet를 소유한다. 네 주요 matrix의 include 반복을 platform 객체와
mode 축으로 정리했다. 변경 전후 YAML을 별도로 파싱·전개해 48개 좌표의 OS/CPU/mode와 timeout 값이 같음을 확인했다.
순수 schema/codegen 검사는 Portable Go의 각 mode가 소유하고 외부 consumer·DB 동작은 relation platform matrix에 남는다.

Full scope의 Go runner 전체 실행은 project-check matrix가 소유하고 동일 좌표의 relation subset은 생략한다. ORM scope에서는 relation
matrix가 subset을 직접 실행한다. 필수 sentinel과 no-skip 검사를 실제 runner owner에 적용했다. 같은 Darwin CGO-disabled lifecycle은
Full scope에서 두 matrix가 소유하고 reference-only scope에서는 exact job이 직접 실행한다.

실제 workflow의 relation/project-check shell을 추출해 synthetic Go JSON을 공급했다. 세 mode의 relation 두 분기와 project-check
9개 실행이 성공했으며 runner 누락·shared drift sentinel 누락·runner sentinel skip은 모두 거부했다. 원래 Go 실패 exit 42도 보존했다.
이는 shell 분기와 로그/필수 실행 검사의 검증이며 제품 테스트 실행을 대체하지 않는다. scope unit tests도 선택 owner의 실패·취소·skip·
누락을 거부했다. 최종 Hosted full scope가 실제 환경의 통합 실행과 PostgreSQL source-bound capture를 소유한다.

### 고정 소스의 Hosted 통합 검증

- source: `21ceeb56021e65c7c718eef93a898150812b6c32`, 2026-09-07 KST.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34088858221): PASS.
- [CI 34088869887, attempt 1](https://github.com/progresshans/godj/actions/runs/34088869887): PASS, 재시도 없이 74개 job 모두 성공.
- 최종 집계 job `101644441697`은 `scope: full`, `full_platform_verified: true`와 선택한 9개 owner의 성공을 확인했다:
  `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
  `product-project-check-matrix`, `project-operator-product-matrix`, `python-compatibility-matrix`,
  `relation-product-matrix`, `targeted-migrate-product-matrix`.

대표 relation job `101638169902`(Linux arm64 normal), `101638169908`(Linux amd64 CGO-disabled),
`101638169919`(Linux arm64 CGO-disabled), `101638169934`(macOS amd64 race), `101638169994`(macOS arm64 CGO-disabled)는 각각 26 packages,
run/pass 3,132, skip 0과 `--required`·`--packages`·`--no-skips` 검사를 통과했다. 공통 fixture의 drift/bootstrap sentinel을
검사하고 `RUNNER_COVERED=true`일 때 runner를 중복 실행하지 않는 경로를 실제로 확인했다.

Linux amd64 project-check의 normal/race/CGO-disabled job `101638169779` / `101638169880` / `101638169772`는
각각 Go runner 전체 run/pass 387, skip 0과 relation sentinel no-skip 검사를 통과했다. 별도 runserver package의 skip 하나는
PostgreSQL DSN 미제공 경로이며 해당 E2E는 아래 PostgreSQL owner에서 실행했다. normal의 별도 cold CLI build milestone도 PASS다.

PostgreSQL 17.10 검증 여섯 조합 모두 필수 selector·no-skip 검사를 통과했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 모드의 집계 |
|---|---|---|
| core | `101638169744` / `101638169758` / `101638169808` | 11 packages, run/pass 57, skip 0 |
| operator-target | `101638169850` / `101638169803` / `101638170626` | 2 packages, run/pass 12, skip 0 |

같은 실행의 `systemstate-postgres-1`(artifact `10006236076`)과 `operator-postgres-1`(`10006219408`)을 다운로드했다.
두 provenance의 repository/run/attempt는 `progresshans/godj` / `34088869887` / `1`이며 실제 checkout은
`6d06397c8007a0040a250d1fe9120e0ea0a7fbcf`이다. GitHub commit API의 tree `6557451b642de6750d63b009a5d29e6b05ccc774`는
로컬 구현 소스 `21ceeb5`의 tree와 같다. payload SHA-256을 다시 계산해 provenance와 SHA256SUMS에 일치함을 확인했다.

- `postgresql-17.10-two-process-v1.json`: `25c4467b34f65cc59635ad78792d798b1a6579f52832bb68b9f73961dc2cd13f`
- `postgresql-17.10-sqlite-external-operator-v1.json`: `7e178c6639ca6cdb92d8a03e6f6e1ef8998537bf5f545063b0ee88b5a331c82f`

Exact Darwin job `101638169661`은 고정 Python profile·oracle 대조를 통과했다. Full scope의 중복 Go lifecycle step은
실행하지 않았으며, 해당 Go 검증의 실제 결과는 관계/project-check matrix가 소유한다. Python은 325개 중 DRF 의존 3개가 skip됐다.
Python 3.12.13/3.13.15/3.14.3/3.14.7 job `101638169698` / `101638169708` / `101638169723` / `101638169728`은 모두 PASS다.
각 portable Python 실행의 skip 21개와 exact의 skip 3개를 테스트 identity로 대조해 반대 환경에서는 모두 PASS임을 확인했다.
각 환경의 skip을 그 환경에서 실행한 PASS로 세지 않는다.

Reference consumer job `101639545181`은 같은 실행의 두 provenance 검증, conformance actual 비교, 32비트 Linux compile과
관계 product 실행을 모두 통과했다. 공통 fixture는 32비트에서 실제 테스트를 실행했고 generated drift·Django/DRF oracle checksum·
reference artifact 미변경 검사도 통과했다.

### 실행 비용 관측과 완료 기록

| Hosted 실행 | 성공 job | 생성 시점부터 최종 job 완료 | 개별 job 실행 시간의 합 |
|---|---:|---:|---:|
| 이전 소스 `d1115c7`, run `34080294179` | 78 | 35분 26초 | 362분 18초 |
| 이번 소스 `21ceeb5`, run `34088869887` | 74 | 30분 27초 | 345분 27초 |

두 값은 서로 다른 실행의 관측이다. 전체 경과 시간에는 대기열이 포함되고, job 시간 합에는 병렬 실행이 중복 합산된다.
삭제한 SQLite 네 job의 이전 실행 시간 합은 5분 20초였다. 동일 cache·부하를 고정한 비교 실험이 아니므로 전체 속도 개선율로
일반화하지 않는다. 확정된 변화는 중복 네 job·runner subset·fixture compile/준비와 테스트 코드의 제거다.

전체 platform 검증은 위 Hosted 실행이 소유하며 로컬 전체 `make ci`를 반복하지 않았다.
구현 소스 이후 완료 기록은 Markdown만 변경했다. `make docs-check format-check`(82개 문서), `git diff --check`,
CURRENT·work 상태·검증 소스의 일치와 Markdown-only 변경 경계 검사를 통과했다.

## GDJ-0060 — 생성기 검증의 실행 경계 정리

- 작업: [GDJ-0060](../../work/0060-codegen-validation-boundaries.md)
- 기준: `0ffce7029b80988d6bc28391dca2f5d8967d65c7`, `codex/revision-fenced-migration-lifecycle`.
- 구현 소스: `d1115c7d8371cd52627eb4e1a6c7888b64b981fa`.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 아래는 기준에 이번 구현을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 소스의 Hosted full scope 검증 완료.

| 실제 명령·범위 | 결과 |
|---|---|
| `go test -run '^$' ./codegen/...` | compile-only PASS, 이 단계에서 테스트 본문은 실행하지 않음 |
| `go test -json -count=1 -timeout=15m ./codegen/... ./internal/compiletest ./internal/projectgenerate ./conformance/internal/protocol` | PASS. test/subtest run 1,767 / pass 1,766 / skip 1 / fail 0, stderr 0 bytes |
| `go test -json -race -count=1 -timeout=15m ./codegen/...` | PASS. run/pass 383, skip/fail 0, stderr 0 bytes |
| `CGO_ENABLED=0 go test -json -count=1 -timeout=15m ./codegen/...` | PASS. run/pass 383, skip/fail 0, stderr 0 bytes |
| `make generate-check` | PASS, checked-in 생성물 drift 없음 |
| `go vet ./codegen/...` | PASS |
| `python3 -m unittest discover -s scripts/ci -p 'test_*.py'` | PASS, 17 tests. 새 package 분류, 포맷 오류·공백 경로·tracked deletion·partial Git listing의 실패 보존 포함 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/feedback.yml` | PASS. ShellCheck/Pyflakes는 이 명령에 미포함 |
| `make quick`, `python3 scripts/check_docs.py`, `git diff --check` | PASS, 문서 81개. quick 실행 출력에 외부 consumer package가 포함되지 않음을 확인 |

normal의 skip 하나는 부모가 자식 프로세스로 실행하는 `TestPublicationCrashHelper`의 직접 진입이다. 부모 crash 회귀는 통과했다.
테스트가 없는 `codegen/internal/testfixture`는 호출하는 검사로 검증하며 별도 테스트 실행으로 세지 않는다.
제품 runtime·생성 ABI·고정 reference artifact에는 변경이 없다. PostgreSQL·전체 OS/arch/cold matrix는 최종 Hosted가 소유하며
이번 로컬 실행에서 전체 `make ci`를 반복하지 않았다.

Go AST로 기준과 변경 파일을 대조했다. 기존 최상위 test 함수 107개는 삭제·중복 없이 남았고, 생성 Go fixture literal
40종의 값과 등장 개수가 같았다. 순수 `codegen`의 직접 외부 Go 실행은 27곳에서 0곳으로 분리했다. 현재 외부 명령 생성은
consumer helper 한 곳이 소유하며 원래 각 테스트의 command argument와 결과·실패 검증은 유지한다.
이 개수는 이번 이관의 점검 결과이며 CI의 영구 roster나 제품 계약으로 잠그지 않는다.

### 실행 비용 관측

- 위 normal 실행에서 순수 `codegen`은 0.423초, 분리한 `codegen/consumertest`는 50.947초였다. package별 값이며 병렬 실행의 총 시간을 합산하지 않는다.
- 외부 검증 분리 후 최초 `make quick`은 14.40초, 포맷 배치 적용 후 실행은 2.91초였다. 캐시 상태도 다를 수 있어 전체 개선 비율로 일반화하지 않는다.
- 포맷 처리만 같은 현재 파일 집합·동일 머신에서 비교했다. 기준 commit의 Makefile로 `format-check`를 실행한 결과 2.879초,
  현재 `make format-check`는 0.179초였으며 둘 다 PASS였다. 이전 checkout의 제품 테스트를 실행한 결과가 아니다.
- Hosted의 `Fast Go feedback` 단계는 [이전 소스 `623ce53`](https://github.com/progresshans/godj/actions/runs/34046122604)의
  52초에서 [이번 소스 `d1115c7`](https://github.com/progresshans/godj/actions/runs/34080359289)의 12초로 관측됐다.
  두 실행 모두 PASS, Ubuntu 24.04·Go 1.26.5다. 서로 다른 실행·cache의 관측이며 전체 CI 속도의 비교 실험은 아니다.
- Go 라인은 새 package의 import·보조 코드와 추가 회귀를 포함해 기준보다 67줄 늘었다. 이번 개선은 빠른 경로에서 외부 빌드를 분리하고
  동일한 파일 집합의 포맷 프로세스를 줄인 것이며, 전체 검증을 제거하거나 전체 CI 시간 감소를 입증한 결과가 아니다.

### 고정 소스의 Hosted 통합 검증

- 날짜: 2026-09-07 KST, source `d1115c7d8371cd52627eb4e1a6c7888b64b981fa`.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34080359289): PASS. 빠른 실행에서 외부 consumer가 제외됐고,
  CI 도구 17개 테스트는 한 번 실행됐다.
- [CI 34080294179, attempt 1](https://github.com/progresshans/godj/actions/runs/34080294179): PASS, 재시도 없이 78개 job 모두 성공.
- 최종 집계 job `101619694472`은 `scope: full`, `full_platform_verified: true`와 선택된 10개 소유자의 성공을 확인했다:
  `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
  `product-project-check-matrix`, `project-operator-product-matrix`, `python-compatibility-matrix`,
  `relation-product-matrix`, `sqlite-matrix`, `targeted-migrate-product-matrix`.

관계·project-check·operator·targeted migration의 Linux/macOS amd64·arm64 normal/race/CGO-disabled matrix,
portable Go·SQLite 및 Python compatibility를 통과했다. 세부 package·환경·필수 실행 조건은 위 CI의 고정 workflow와 job 로그가 소유한다.

`codegen/consumertest`는 portable integration의 normal/race/CGO-disabled job
`101614191299` / `101614191225` / `101614191231`에서 실제 package 실행을 통과했다.
relation matrix는 `./codegen/...`를 실행하고, 새 package 위치의 mixed-snapshot 거부 테스트를 필수 sentinel로 검사한다.
Ubuntu amd64 세 모드 `101614191128` / `101614191176` / `101614191131`에서 각각 47 packages,
run/pass 3,495, skip 0과 `--packages`·`--required`·`--no-skips` 검사를 확인했다.

exact darwin/arm64 job `101614190974`은 SQLite lifecycle·고정 Python profile·oracle 재생성 대조를 통과했다.
Python suite는 325개 중 DRF 의존 테스트 3개가 skip됐으며, 이 세 개는 DRF를 설치한 Python
3.12.13/3.13.15/3.14.3/3.14.7 compatibility 작업 네 개에서 모두 PASS임을 실제 로그로 확인했다.
반대로 portable Python suite의 skip 21개는 exact darwin 로그에서 전부 PASS였다. skip 이름과 실행 결과를 대조했으며,
각 환경의 skip을 그 환경에서 통과한 테스트로 세지 않는다. DRF oracle 세 종류도 별도 고정 환경에서 대조를 통과했다.

PostgreSQL 17.10 제품 검증은 여섯 조합 모두 PASS다. 각 로그의 필수 selector와 `--no-skips` 검사를 확인했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 모드의 실행 집계 |
|---|---|---|
| core | `101614191080` / `101614191095` / `101614191096` | 11 packages, run/pass 57, skip 0 |
| operator-target | `101614191057` / `101614191047` / `101614191069` | 2 packages, run/pass 12, skip 0 |

같은 실행의 `systemstate-postgres-1`(artifact `10003548153`)과 `operator-postgres-1`(`10003519540`)을 다운로드해
provenance의 repository/run/attempt가 `progresshans/godj` / `34080294179` / `1`임을 확인했다.
실제 checkout은 PR merge commit `9f1ce653a2cfe1f39e7b7a4f96f26f67c543d66c`이며, GitHub commit API와 로컬 Git에서
확인한 tree `2f63a9a9279ff403a1c5dfddcf40a58bbf74f300`가 위 PR source의 tree와 같다.
payload의 SHA-256을 다시 계산해 provenance와 `SHA256SUMS`에 일치함을 확인했다:

- `postgresql-17.10-two-process-v1.json`: `0a988dfbb2fa6f58a787ef82246066bee672115a27cf913ce67054aee9fb443c`
- `postgresql-17.10-sqlite-external-operator-v1.json`: `f8020379725ebfe46601916b0c03270e710798c43b8c546e69663224f6b03f6a`

reference consumer job `101615278509`은 같은 실행의 두 캡처 provenance를 검증하고 conformance,
32비트 Linux compile·관계 product, 고정 oracle checksum과 reference artifact 미변경 검사를 통과했다.

최종 Hosted full scope가 전체 플랫폼 검증을 소유한다. 제품 API·구현 상태의 변경이 없어 구현 현황과 ADR은 수정하지 않았다.
위 구현 소스 이후 완료 기록은 Markdown만 변경한다.

완료 상태를 반영한 세 Markdown은 2026-09-07 KST에 `python3 scripts/check_docs.py`(81개 문서),
`git diff --check`, frontmatter·CURRENT 상태 일치와 검증 소스 이후 Markdown-only 변경 검사를 통과했다.

## GDJ-0059 — 테스트와 검증 코드 공통화

- 작업: [GDJ-0059](../../work/0059-test-validation-compaction.md)
- 기준: `257e593309721bb0da888a3cbdef9b93c2b52083`, 원래 작업 디렉터리와 `codex/revision-fenced-migration-lifecycle` 브랜치.
- 로컬: 2026-09-07 KST, Go 1.26.5 darwin/arm64. 아래는 기준에 이번 구현을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 source의 Hosted full scope 검증 완료.

관련 19개 package는 다음과 같다. 제품 runtime·공개 API·생성 ABI·고정 oracle/profile에는 변경이 없다.

```sh
gdj_compact_packages=(
  ./conformance/internal/generationtest ./conformance/internal/relationschema
  ./conformance/internal/testprocess ./conformance/internal/protocol
  ./conformance/relationproduct ./conformance/relationobjectproduct ./conformance/relationqueryproduct
  ./conformance/relationreverseproduct ./conformance/relationprefetchproduct
  ./conformance/relationselectproduct ./conformance/relationdeleteproduct
  ./conformance/projectmigrateproduct ./conformance/projectmigratetargetproduct
  ./conformance/projectshowmigrationsproduct ./conformance/projectsqlmigrateproduct
  ./conformance/runners/godj ./internal/compiletest ./internal/projectgenerate ./codegen
)
```

| 실제 명령·범위 | 결과 |
|---|---|
| 위 package의 `go test -count=1`을 process/fixture/protocol/runner/consumer 묶음으로 실행 (`-timeout=5m/10m/12m`) | PASS, 실제 SQLite·외부 process·consumer compile·생성/출판·oracle 비교·부정 회귀 |
| `go test -json -race -count=1 -p=2 -timeout=20m "${gdj_compact_packages[@]}"` | PASS, 19개 package. test/subtest run 2,353, pass 2,348, skip 5, fail 0; stderr 0 bytes |
| `CGO_ENABLED=0 go test -json -count=1 -p=2 -timeout=20m "${gdj_compact_packages[@]}"` | PASS, 19개 package. test/subtest run 2,353, pass 2,348, skip 5, fail 0; stderr 0 bytes |
| `make generate-check` | PASS, Helpdesk·Article·relationdelete 및 별도 관계 fixture 6개의 byte drift 없음 |
| `go vet` — 위 목록의 conformance package 16개 | PASS |
| `python3 scripts/check_docs.py`, `git diff --check` | PASS |

race/CGO-disabled의 skip은 PostgreSQL 접속 설정이 없는 전용 제품 테스트 네 개와, 부모 테스트가 자식 프로세스로만
실행하는 `TestPublicationCrashHelper`의 직접 진입 한 개다. 부모 publication crash 회귀는 통과했다.
로컬 PostgreSQL 실행을 주장하지 않으며 실제 DB 검증은 최종 Hosted scope가 소유한다.
JSONL의 test/subtest 건수는 실행 기록이며 제품 계약이나 고정 roster로 추가하지 않는다.

이관 중 남은 미사용 import로 compile-only 및 일부 normal package가 실패했다. import를 제거한 후 해당 package를
다시 실행하고 위 전체 관련 race/CGO-disabled를 통과했다. 최초 compile 실패를 성공 기록으로 재사용하지 않았다.

정적 변경 비교에서 기존 test 함수 삭제는 없다. 동일 helper 통합과 공통 입력의 상태 격리·generated inventory·process
안전성·출력 상한 회귀를 포함해 전체 Go 라인은 323,137에서 321,932로 1,205줄 감소했다. 실행시간 개선을 측정한 결과는 아니다.

### 고정 소스의 Hosted 통합 검증

- 날짜: 2026-09-07 KST(2026-09-06 UTC), source `623ce53e52187c7d2ab656775e356ba5e0ce5117`.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34046122604): PASS.
- [CI 34046136824, attempt 2](https://github.com/progresshans/godj/actions/runs/34046136824): PASS, 최종 78개 job 성공.
- attempt 1의 Python 3.14.7 job `101521418575`는 `Set up uv`에서 manifest 다운로드가 `fetch failed`로 실패했다.
  해당 Python 테스트와 semantic digest는 실행되지 않았다. 나머지 76개 job은 성공했고, 이 실패를 반영한 집계 job
  `101526253688`도 실패했다. 소스·lock을 변경하지 않고 `gh run rerun 34046136824 --failed`로 두 실패 작업을 재시도했다.
- attempt 2의 Python job `101526358026`은 도구 설치·portable suite·전체 scenario semantic digest를 통과했다.
  portable suite는 325개 tests, skip 21개로, 모두 별도 exact darwin/arm64 profile에서 실행하는 검증이다.
  최초 실행의 실패를 성공으로 바꾸어 기록하지 않으며 통과한 76개 작업은 같은 소스의 결과로 유지했다.
- 최종 집계 job `101527678459`은 `scope: full`, `full_platform_verified: true`와 선택된 10개 소유자의 성공을 확인했다:
  `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`, `postgresql-product`,
  `product-project-check-matrix`, `project-operator-product-matrix`, `python-compatibility-matrix`,
  `relation-product-matrix`, `sqlite-matrix`, `targeted-migrate-product-matrix`.

관계·project-check·operator·targeted migration의 Linux/macOS amd64·arm64 normal/race/CGO-disabled matrix,
portable Go·SQLite 및 Python 3.12.13/3.13.15/3.14.3/3.14.7 compatibility를 통과했다.
각 실행의 세부 package·환경·필수 실행 조건은 위 CI의 고정 workflow와 job 로그가 소유한다.

PostgreSQL 17.10 제품 검증은 아래 여섯 조합 모두 PASS다. 각 로그의 필수 selector와 `--no-skips` 검사를 확인했다.

| 제품 그룹 | normal / race / CGO-disabled job | 각 모드의 실행 집계 |
|---|---|---|
| core | `101521418310` / `101521418292` / `101521418302` | 11 packages, run 57 / pass 57 / skip 0 |
| operator-target | `101521418308` / `101521418305` / `101521418336` | 2 packages, run 12 / pass 12 / skip 0 |

같은 attempt 1의 system-state 캡처 `systemstate-postgres-1`(artifact `9993227200`)과 operator 캡처
`operator-postgres-1`(`9993208641`)을 생성했다. reference consumer job `101522152376`은 두 캡처의 provenance를
검증하고 conformance·32비트 Linux compile/관계 product·고정 oracle checksum 검사를 통과했다.
exact darwin/arm64 job `101521418181`도 고정 Python profile·SQLite lifecycle·reference 검증을 통과했다.

다운로드한 두 provenance의 repository/run/attempt는 `progresshans/godj` / `34046136824` / `1`이다.
실제 CI checkout은 PR merge commit `915a718477c4642ae156d095e758743d0633c63d`이며, GitHub commit API와 로컬 Git에서
확인한 tree `fa9ad08184eeea92828474ce74922b160dceca2a`가 위 PR source의 tree와 같다. commit ID를 혼동하지 않는다.
payload를 다시 SHA-256으로 계산해 provenance와 일치함을 확인했다:

- `postgresql-17.10-two-process-v1.json`: `3935e5aeeba3d78e6636bdc82185bf24abfcb1ffe004f0cef01118605f5afa69`
- `postgresql-17.10-sqlite-external-operator-v1.json`: `0608758c67f8cbbaa2569e04f1fb76a05f275b27a62fff4ef49118b9f512bef3`

전체 `make ci`와 같은 전체 플랫폼 matrix를 로컬에서 추가 실행하지 않았다. 최종 Hosted full scope가 해당 범위를 소유한다.
제품 API·구현 상태의 변경이 없어 구현 현황과 ADR은 수정하지 않았다. 이후 완료 기록은 Markdown만 변경한다.

완료 상태를 반영한 세 Markdown은 2026-09-07 KST에 `python3 scripts/check_docs.py`(80개 문서),
`git diff --check`, frontmatter·CURRENT 상태 일치와 검증 source 이후 Markdown-only 변경 검사를 통과했다.

## GDJ-0058 — 관계 조회 정리와 eager First

- 작업: [GDJ-0058](../../work/0058-eager-first-ticket-detail.md)
- 기준: `0b9955ec0ef3b013e59fd185038d38e57582d008`, 원래 작업 디렉터리와 `codex/revision-fenced-migration-lifecycle` 브랜치.
- 로컬 환경: 2026-09-06, Go 1.26.5 darwin/arm64. 아래는 해당 기준에 GDJ-0058 변경을 적용한 checkout에서 실행했다.
- 상태: 구현·로컬 checkpoint·고정 source의 관련 Hosted 검증 완료. 아래 각 기록이 해당 source와 범위를 소유한다.

| 실제 명령·범위 | 결과 |
|---|---|
| `go test ./orm ./codegen -run 'SelectRelated\|ForwardSelect\|ProjectRelationFacade' -count=1` | PASS, First 추가 전 동작 보존 정리의 기존 회귀 |
| `go test -count=1 -timeout=8m ./orm ./codegen ./examples/helpdesk ./internal/compiletest ./internal/projectgenerate ./internal/projectcheck` | PASS, First 구현·생성·출판·외부 Go module compile |
| `go test -count=1 ./orm ./examples/helpdesk` | PASS, 마지막 공통 rows 획득 이관 후 전체 ORM 및 Helpdesk 재검증 |
| `go test -race -count=1 -timeout=8m ./orm ./codegen ./examples/helpdesk ./internal/compiletest` | PASS |
| `CGO_ENABLED=0 go test -count=1 -timeout=8m ./orm ./codegen ./examples/helpdesk ./internal/compiletest` | PASS |
| `make generate-check` | PASS, Helpdesk·Article·relationdeleteproduct 전체 산출물 drift 없음 |
| `go vet ./orm ./codegen ./examples/helpdesk` | PASS |
| `python3 scripts/check_docs.py`, `git diff --check` | PASS |

검증 내용: First의 최대 1회 scan·기존 Offset/Limit/Distinct/JOIN 유지, cold/warm/empty cache, required와 nullable
관계·객체 독립 소유권, binding/context/backend/scan/rows/close 오류와 재시도, 외부 typed/dynamic First 호출을 확인했다.
Helpdesk의 실제 SQLite HTTP 상세 요청은 티켓과 Category를 1회 JOIN으로 읽고, 다른 Category와 없는 티켓에 404를 반환했다.
인증·ViewTicket 거부 시 application data Query는 0회였다. Category id/name 출력 정책과 ViewCategory의 별도 Admin 정책도 확인했다.

편집 중 삭제한 private discriminator·context probe를 요구하던 테스트와 상세 응답 필드 수 기대값을 수정했다.
공통 rows 함수로 옮길 때 남은 두 projection 호출부의 compile 오류도 수정하고 위 검증을 통과했다.
로컬 Docker daemon이 실행 중이지 않아 PostgreSQL sentinel은 로컬에서 skip됐다. 최종 PostgreSQL 검증은 아래 Hosted 실행에서 수행했다.
이번 단계에서 전체 `make ci`, 전체 플랫폼·32비트·Django differential oracle을 로컬에서 다시 실행하지 않았다.

### Hosted 실패 후 generated fixture 보정

첫 고정 소스 `d594c9547fa0a57f6da28e704a9613ba6e2336f2`의
[PR feedback](https://github.com/progresshans/godj/actions/runs/34039980956)은 PASS였다.
[ORM scope CI](https://github.com/progresshans/godj/actions/runs/34040015705)는
`conformance/relationselectproduct/project/zz_godj_relation_select_related.go`가 새 생성기 결과와 달라 실패했다.
대표 job `101504961156`, macOS race `101504961226`, portable conformance normal `101504961243`과 CGO0 `101504961253`에서
같은 `TestCheckedInGeneratedSelectRelatedProjectMatchesElevenDeterministicCandidates` 실패를 확인했다. 이 실행은 완료 증거가 아니다.
원인을 보정하고 다른 완료 결과를 확인한 뒤 남은 작업을 취소했으며, 첫 실행의 최종 conclusion은 `cancelled`다.

누락된 companion을 재생성하고 `make generate-check`에 manifest가 없는 관계 fixture 여섯 개의 기존 drift 검사를 추가했다.
보정 checkout에서 `go test -count=1 ./conformance/relationselectproduct`, 같은 범위 `-race`, `CGO_ENABLED=0` 모두 PASS.
확장된 `make generate-check`, `python3 -m unittest discover -s scripts/ci -p 'test_*.py'`(17개), 문서 링크·diff 검사도 PASS였다.
보정 후 새 고정 source와 Hosted 결과를 별도로 확인하며 첫 실행의 성공한 일부 job을 재사용하지 않는다.

### 보정 소스의 최종 관련 검증

- 날짜: 2026-09-07 KST(2026-09-06 UTC), source `aca9115223b3d4703c36553e580c3ee60f7d2c42`.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34040428257): PASS.
- [CI 34040585667, attempt 1](https://github.com/progresshans/godj/actions/runs/34040585667): PASS, 48개 job 성공·5개 범위 외 그룹 skip.
- 최종 집계 job `101508656253`은 `scope: orm`, `full_platform_verified: false`와 다음 소유자의 성공을 확인했다:
  `portable-go-matrix`, `postgresql-product`, `relation-product-matrix`, `sqlite-matrix`, `targeted-migrate-product-matrix`.
- 관계 product의 Linux/macOS amd64·arm64 normal/race/CGO-disabled와 portable Go core/integration/conformance/product,
  SQLite 및 targeted migration의 선택된 조합을 통과했다. 각 세부 조합은 위 실행의 job 및 workflow가 소유한다.
- PostgreSQL 17.10 실제 product는 core/operator-target × normal/race/CGO-disabled 6개 조합이 모두 성공했다.
  Helpdesk의 `TestPublicHelpdeskPostgresConsumerAndPermissionMaintenance`는 core 세 모드에서 필수 selector와
  `--no-skips` 실행 검사를 통과했다. 해당 job은 normal `101506541054`, race `101506541056`, CGO0 `101506541046`이다.
- 같은 attempt의 `systemstate-postgres-1`(artifact `9991617191`)과 `operator-postgres-1`(`9991598099`)이 생성됐다.
  이번 scope는 reference consumer를 선택하지 않았으며 그 실행·소비를 주장하지 않는다.

선택하지 않은 그룹은 `conformance-validation`, `exact-darwin-validation`, `product-project-check-matrix`,
`project-operator-product-matrix`, `python-compatibility-matrix`다. 이 결과는 GDJ-0058의 관련 검증이며 전체 프로젝트
platform/reference 검증으로 확대하지 않는다. 완료 기록은 Markdown만 변경하며 동일 제품 소스의 전체 matrix를 반복하지 않는다.

완료 문서는 2026-09-07 KST에 `python3 scripts/check_docs.py`(79개 문서), `git diff --check`와
`aca9115` 이후 변경이 모두 Markdown인지 확인하는 검사를 통과했다. 제품·도구·생성물은 최종 CI 소스와 동일하다.

## 이전 증거

- [GDJ-0055 마지막 제품 통합 증거](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/status/TEST_EVIDENCE.md#evid-20260905-179--gdj-0055-explicit-operator-provisioning-terminal-acceptance):
  제품 source `0b5b6fc6ec60e1704e5cebfaebd771b682d001ee`의 local/backend/platform 결과다. 현재 변경의 검증이 아니다.
- [GDJ-0056 checkpoint를 포함한 정리 전 전체 기록](https://github.com/progresshans/godj/blob/da1bfc524c4f205075fc7fac7f00b437473a5e1f/docs/status/TEST_EVIDENCE.md):
  EVID-001..182의 명령·source·환경·실패·산출물을 보존한다. EVID-182는 corrected attestation checkpoint이며
  GDJ-0056 전체 Hosted 완료를 뜻하지 않는다.

고정 commit은 현재 브랜치의 조상이다. 네트워크 없이 원문을 보려면 저장소에서 다음을 실행한다.

```sh
git show da1bfc524c4f205075fc7fac7f00b437473a5e1f:docs/status/TEST_EVIDENCE.md
```

과거 본문의 복제 archive는 만들지 않는다. 검증을 인용할 때는 실행한 source, 환경, 검증 범위와 해당 항목을 함께 가리킨다.

## GDJ-0057 — 개발 구조 정리

- 시작 기준: `da1bfc524c4f205075fc7fac7f00b437473a5e1f`
- 작업: [GDJ-0057](../../work/0057-development-simplification.md)
- 상태: 구현·통합 검증 완료. 아래에 실제 실행한 명령과 결과만 기록한다.

### 실행 기록

2026-09-06, darwin/arm64 Go 1.26.5, 기준 `da1bfc5`의 `feature/development-simplification` 작업 사본에서 실행한 구현 checkpoint다.
아래는 최종 commit의 전체 플랫폼 검증을 뜻하지 않는다. 별도 기록이 없는 PostgreSQL service 경로는 이 로컬 검사에서 실행하지 않았다.

| 명령·범위 | 결과 |
|---|---|
| `make quick` (문서 링크·gofmt·CI 도구·core package) | PASS |
| `python3 -m unittest discover -s scripts/ci -p 'test_*.py'` | PASS, 빌드 오류/timeout/잘린 로그/필수 skip와 CI scope·capture provenance 부정 회귀 포함 |
| `go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 -shellcheck= -pyflakes= .github/workflows/ci.yml .github/workflows/feedback.yml` | PASS, YAML/Actions 표현식 검사. ShellCheck/Pyflakes는 이 명령에 미포함 |
| `go test ./codegen -count=1 -timeout=8m` | PASS |
| `go test ./orm -count=1 -timeout=5m`, 같은 범위 `-race` | PASS |
| `go test ./internal/compiletest -run TestCheckedInRelationFacadeV2CannotHybridizeCurrentBundle -count=1` | PASS, 과거/현행 생성물 혼합 거부 |
| Article와 relationdeleteproduct `godj generate --check` | PASS |
| `go test ./conformance/internal/protocol -count=1 -timeout=5m` | PASS, 문서·workflow 모양 잠금 정리 후 contract/oracle/실행 누락 guard |
| `go test -count=1 ./conformance/systemstate/attestation ./conformance/projectoperatorproduct/attestation` | PASS |
| godjcheck `TestLoadRunnerInputs*`, `TestAttestationRepositoryRoot*`, `TestProjectOperatorAttestationAccepts*`, `TestRequireExactResolvedPath*`, `TestRunRejectsCrossArtifactSYS029*`, `TestRunRequiresPublishedSYS029*` | PASS |
| Form/Admin/serializer 선택·초기값·출력과 operator 권한 CAS의 affected normal 회귀 | PASS; 권한 경쟁 1승, session revoke, old runtime 거부, rollback·unknown outcome 보존 |
| Helpdesk 공개 API를 사용하는 외부 Go test package의 SQLite/Admin/API 흐름 | PASS. 별도 Go module 설치 증거는 아님 |

추가 구현 checkpoint:

- Form/Admin/serializer/systemstate/Article adapter/Helpdesk의 normal/race/CGO-disabled PASS. PostgreSQL sentinel은 로컬에서 명시 skip.
- `internal/gobuild`, `internal/projectcheck`, `internal/projectgenerate` 전체 normal PASS; gobuild/projectcheck race와 cmd/godj 전체 CGO-disabled PASS.
- `GODJ_COLD_BUILD=1 go test ./cmd/godj -run '^TestActualGodjMigrationCheckProcess$/^implicit_success$' -count=1 -timeout=5m` PASS.
- SYS-023 세 PTY 사례의 기존 oracle 비교 PASS. SYS-020의 live SQLite/injected PostgreSQL facts 및 SQL-rendering 회귀 PASS.
- 빌드 원인 누락을 보강한 실제 SQLite 두 프로세스·Article restart·operator known-created response-write-failure 회귀 PASS.
  PostgreSQL 전용 두 helper는 compile만 확인했다.
- Query typed-nil Error/Is/Unwrap panic 재현 후 query/schema 회귀 PASS. SQLite Q-019·migration outcome/durable-prefix 및 내부 migration writer 검증 PASS.
- `TestSQLiteExecutorCompetingCommitStopsTailAndReturnsOwnDurablePrefix`, `TestSQLiteExecutorRejectsRecorderCorruptionAfterValidTransition` normal/race PASS.
- 선택 부모 identity 교체+invalid/oversized descriptor 3건을 실패로 재현한 뒤 수정. 실제 selection/linked/protocol 전체 normal/race PASS.
- 실제 workspace parent 교체, launch failure/reap 0, 동시 cancel/interrupt와 Wait 직후 interrupt 재확인 normal/race PASS.
- `go test -count=1 -run '^$' ./...`, `go vet ./...` PASS (이후 옮긴 실제 테스트는 해당 패키지 normal/race로 추가 검증).
- Helpdesk 선언 runner를 연결한 뒤 `make generate-check` 세 프로젝트 모두 PASS.
- CI 도구 회귀는 17개 PASS. 실패/정상 discovery를 실제 Make에 모두 주입해, macOS GNU Make 3.81에서도 일부 목록 실패와 앞선 gofmt parse 오류가 뒤 성공에 가려지지 않음을 확인했다.

최종 제출 source의 로컬/Hosted 기록은 아래에 이어 적는다. 위 checkpoint의 elapsed 값이나 결과를 전체 플랫폼의 성능·성공으로 일반화하지 않는다.

### 첫 통합과 잔여 검사 정리

2026-09-06, source `1393624ed56782951b0b114c946b9bfab5ceebe9`에서 실행했다.

- darwin/arm64: `make quick generate-check go-vet` PASS (65.10초), `make go-test-conformance` PASS (136.43초),
  `go test -count=1 -timeout=20m ./conformance/runners/godj` PASS (133.52초). PostgreSQL service 미설정 경로는 이 로컬 성공에 포함하지 않는다.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34027839749) PASS.
- [첫 전체 CI](https://github.com/progresshans/godj/actions/runs/34027880576)에서 `internal/compiletest`의 남은
  생성 파일 byte/hash, facade 전체 함수 목록, tool 전체 직접 import 목록 검사 실패를 확인했다. 이 실행은 전체 PASS가 아니다.
  로컬의 과거/현재 생성물 혼용 거부 focused 검사만으로 전체 compiletest 성공을 대신할 수 없음을 확인했다.
- 잔여 모양 잠금을 제거하고 실제 외부 compile/type misuse·생성물 drift·혼용 거부·금지 의존 방향을 유지했다.
  Sealed selector 위조 compile-negative와 JSON value/pointer 누출·unmarshal 무변경 실행 검사를 보강했다.
- CI 라벨과 무관한 PR 라벨이 현재 검증을 취소하지 않도록 concurrency group을 분리했다.
  변경 후 actionlint PASS, CI 도구 회귀 17개 PASS. 로컬 capture 다운로드·provenance 검증·전체 gate 사용법도 보완했다.
- 후속 변경을 동결한 작업 사본: `go test ./internal/compiletest -count=1 -timeout=5m` normal/race/CGO-disabled 모두 PASS
  (각 10.916/11.709/10.629초), `make go-test-integration` PASS (52.64초), `make format-check docs-check`와 `git diff --check` PASS.

첫 실행은 수정 소스의 전체 CI가 시작된 뒤 남은 작업을 취소했다. 실패를 해결한 이전 실행의 부분 성공을 새 source의 전체 증거로 재사용하지 않는다.

### 최종 통합 소스

- 제품·검증 도구 source: `0b8235ce010f971470d344281bc51fee84fb73fa`.
- [전체 CI](https://github.com/progresshans/godj/actions/runs/34028776113), attempt 1: PASS, 78개 작업 모두 success.
  PR checkout `42dc5ea032fc687bbd6ad4ec5488f4088a5efe30`의 tree가 제출 source와 같음을 Git 객체로 확인했다.
- 최종 `CI result (ci:full)`은 `scope: full`, `full_platform_verified: true`로 10개 실행 그룹 모두의 성공을 확인했다.
  4 OS/arch × normal/race/CGO-disabled, cold CLI 경로, 고정 darwin/arm64 기준 비교와 Python 4개 버전 검증을 포함한다.
- [PR feedback](https://github.com/progresshans/godj/actions/runs/34028766526) PASS.
- 실제 PostgreSQL producer 6개(normal/race/CGO-disabled × 두 shard), 같은 attempt의 capture 소비·conformance·32비트 후속 검사 PASS.
  두 archive를 별도로 읽어 GitHub archive digest, repository/run/attempt/checkout, payload SHA256과 `SHA256SUMS` 일치를 확인했다.

| 같은 실행에서 생성·소비한 artifact | 불변 artifact ID |
|---|---|
| `systemstate-postgres-1` | `9987975595` |
| `operator-postgres-1` | `9987957182` |

위 artifact의 실제 사용법과 보관 기한은 [TESTING](../TESTING.md#실제-source의-증거)에 있다.
로컬 전체 `make ci`는 Hosted 전체와 중복 실행하지 않았다. 새로운 Helpdesk의 외부 test package 검증은 별도 Go module 설치 증거가 아니며,
Hosted의 기존 외부 archive·생성물 consumer 검증과 구분한다.

완료 상태를 기록한 후속 변경은 Markdown만 포함한다. 제품·workflow·lock을 바꾸지 않는 이 기록 때문에 전체 matrix를 반복하지 않는다.
2026-09-06 완료 문서 6개에 `make docs-check`, `git diff --check`와 검증 source 이후 Markdown-only diff 검사를 실행해 PASS를 확인했다.
