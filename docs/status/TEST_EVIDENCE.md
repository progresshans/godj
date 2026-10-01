# 테스트 증거

현재 변경의 실행 결과는 이 파일에 한 번만 기록한다. 설계 채택, 코드 존재, 특정 환경에서의 검증은 서로 다른 상태다.
미실행·비대상·환경 실패를 PASS로 표현하지 않으며 다른 source의 성공을 현재 실행 결과로 옮기지 않는다.

## GDJ-0109 — native bulk 생성과 여러 티켓 생성

### Hosted 전체 통합의 macOS race 분할 수정과 재검증

첫 [Hosted full 36923002501](https://github.com/progresshans/godj/actions/runs/36923002501), attempt 1의 source는
`d2d8518275eae9e7f46a836d0797f1f9802561c9`다. `Relation product (macos-26, race)` job `110573459531`가
생성 소비자 package의 35분 timeout으로 종료했다. 종료 시 `TestGeneratedRowLocking`은 29초째 실행 중이었고,
로그에는 개별 assertion 실패 없이 package failure·panic이 기록됐다. 남은 필수 실행을 입증하지 못했으므로 전체 PASS가 아니다.
원 실패 로그 833 lines / SHA-256 `a3f4ef0f1e0caa1617e9ac66f25053bd4ad886f3d485021c5b79d53c6046c003`를 보존했다.
수정 실행으로 전환하며 첫 run의 최종 상태는 `cancelled`였다. 65 jobs 중 success 57·failure 2(ARM/race와 최종 집계)·cancelled 6이다.
취소된 필수 실행이나 첫 run의 부분 성공을 수정 source의 PASS로 전이하지 않는다.

CI source `1819908303e90bf400e2e993286625685a33665d`에서 macOS ARM/race에도 기존 Intel/race와 같은
세 consumer shard와 별도 runtime owner를 적용했다. 각 shard의 기존 90분 job/70분 package 한도, 실제 binary root
발견·필수 child·정확히 한 번 실행·no-skip 검사와 모든 platform/mode를 유지한다. 관계 matrix는 18 jobs/12 좌표다.
CI tools 52 tests(3.224s), 184개 문서의 local link와 diff 검사를 통과했다. 첫 실패 job의 실제 발견 root 82개,
discovery `749b92d76fc9a5c02deea915bd035f42b40c70554d1117dd6a85324612fb0cd3`를 새 matrix에 대입해 모든 좌표의
배정 합집합과 중복 부재를 확인했다. 이는 새 source의 Hosted 실행 완료를 뜻하지 않는다.

수정 source로 [Hosted full 36932376723](https://github.com/progresshans/godj/actions/runs/36932376723), attempt 1을
`workflow_dispatch/suite=full`로 요청했다. 실제 remote head·event·branch를 확인했다. 모든 필수 owner·실제 step/test,
새 capture와 Git source의 결합/소비·최종 `full_platform_verified`가 확인될 때까지 전체 통합은 미완료다.
제품 Go source는 첫 full과 같고 이후 변경은 CI 분할 및 Markdown 기록뿐이다. 아래 로컬 영향 검증과 구분한다.

### 고정 Django의 최초 기준 조사

2026-10-02 KST, Go source/output과 expected fixture를 읽지 않는 authored observer로 고정 Django 6.1의 bulk
사례 32개를 SQLite/PostgreSQL에서 각각 두 새 프로세스로 실행했다. Python 3.14.3·psycopg 3.3.6,
PostgreSQL 17.10 Debian/UTF8/libc/C/C다. Observer는 QuerySet·Atomic·SQLInsertCompiler·SQLUpdateCompiler의
source hash와 자신의 SHA-256을 보고한다. 아직 임시 observer의 탐색 결과이며 정식 fixture·Go 비교가 아니다.

Observer `/tmp/godj-bulk-reference-exploration.py`, SHA-256 `c700fd2ac9a3704fc79e537cdab5286ce75450cbe55c76ef58636727e9be9cbe`.
SQLite의 두 출력은 11071 bytes / `c3c77b46e8c8a001e3894f52a4ceaec3b662f47f52814b47266abdad03b62969`,
PostgreSQL의 두 출력은 10936 bytes / `6f80e16742a65ca63bcf2b0b36c33228b87eb350d15cc0616cafc602049c8349`로 일치했다.
Receipt `godj-bulk-reference-bzs_tv6l/receipt.json`, SHA-256 `c179fb2fc4572e108668ae9618a9a33955f0886bc16fb132ae228af6104b0829`.
Observer byte 유지·table/session `0|0`·소유 DB/container 제거를 확인했다.

기본 생성은 입력 순서에 대응하는 생성 key를 반환하고, 여러 statement의 뒤쪽 실패는 앞선 batch도 rollback했다.
SQLite ignore는 unique 외 CHECK/NOT NULL 행도 생략했지만 PostgreSQL은 23514/23502로 실패했다.
같은 statement의 동일 key upsert는 SQLite가 두 입력에 같은 key를 반환하고 PostgreSQL이 21000으로 거부했다.
Bulk update의 중복 key는 한 batch에서는 첫 입력과 count 1, 두 batch에서는 마지막 입력과 count 2였다.
부모 rollback은 DB 행을 없애지만 Django의 caller object PK/state를 되돌리지 않았다.
이 차이는 Go의 지원 정책을 정할 근거이며 구현·환경별 PASS나 기존 기반의 Hosted 완료를 뜻하지 않는다.

### 다중 행 AST와 native backend 기반 checkpoint

2026-10-02 KST, parent `f6e95bb81f80605491bbf04feab49017f79f49e4`, 비Markdown 2997 files /
inventory SHA-256 `187ba82c1dae85279fdaa2365c258750155a71e415368102bc613af73694e26e`. Go 1.26.5/darwin/arm64와 공유 cache·trimpath,
실제 SQLite와 PostgreSQL 17.10 Debian/UTF8/libc/C/C에서 native 기반의 세 mode를 검증했다.
이 checkpoint는 generic ORM·생성 facade·Helpdesk/API 소비자를 포함하지 않는다. 후속 ORM checkpoint와 분리한다.

| Mode / 범위 | packages | run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---:|---|
| normal / ast | 2 | 32 | 0 | 2.292s | `c51259f7d2601d27f3cf44bf82268f29cacd8edfb79be3cac507349d75cfb292` |
| normal / backends | 2 | 82 | 0 | 28.747s | `2d6228ca15c9a14a48fdc06cedfac0794f94a31933f8f84175b6881f90660ca1` |
| race / ast | 2 | 32 | 0 | 4.182s | `bafd093380382b1314dd76417cc975d85d6626223dfeed4979102e002f946f43` |
| race / backends | 2 | 82 | 0 | 43.352s | `c3a5cbc459e6b8d24ae8bae6fa1a851bbd9876fd5dc9cbef5a5191cd227c737c` |
| cgo0 / ast | 2 | 32 | 0 | 1.311s | `0e2f415ff1eaad037e254a18af1e8a4eaeb761a72f16883c75479f9842c5933b` |
| cgo0 / backends | 2 | 82 | 0 | 3.691s | `e3da2304ac8b54e5ab2d5f54dc266a1cf5a100ed0fd4343736b8a1666668936a` |

AST/transport의 필수 4 roots와 backend의 필수 4 roots를 각 mode에서 실행했다. 모든 시작/종료·package·no-skip·
잘림을 대조했다. 다중 입력과 반환 slice 소유권·행/값/field provenance 한도·잘못된 scalar·늦은 행 실패,
정확한 큰/0/음수 key·auto-only·실제 4400 parameter INSERT·ignore/update conflict·nullable/FK/unique/Check,
trigger가 반환 행을 생략한 경우의 전체 rollback·ordinary/coordinated/relation 및 savepoint·root cursor를 포함한다.
Native SQL row transport의 scan/next/close/취소/extra/short/nil·오류와 함께 반환한 rows 정리·ignore의 actual count도 검사했다.

실행 전후 source 동일, schema/session `0|0`, 별도 DB 및 소유 container 제거를 확인했다.
Receipt `godj-bulk-foundation-jbskuawq/receipt.json`, SHA-256 `47d298732799dbc96524d2507e47812de247005416e486067282eedfefb323e0`.
당시 남은 ORM metadata·batch/transaction·생성 drift는 아래 후속 checkpoint에 기록한다. 정식 Django Go 대조·
업무 소비와 통합은 계속 미완료다. 현재 진행 중인 GDJ-0108 Hosted 검증과 이 새 source의 결과를 합치지 않는다.

### Generic ORM와 생성 facade checkpoint

2026-10-02 KST, parent `f6e95bb81f80605491bbf04feab49017f79f49e4`, 비Markdown 3004 files /
inventory SHA-256 `9cc0c8832caee27ef9b344d6017531792385b7a519338d0b32906b629725f183`에서 실행했다.
Go 1.26.5/darwin/arm64, 실제 SQLite와 PostgreSQL 17.10 Debian/UTF8/libc/C/C, 공유 Go cache와
별도 생성 module의 `-trimpath` 및 race 전파를 사용했다. Generic ORM·생성기 두 package 전체의 357 roots,
별도 module의 양 DB 소비자와 잘못된 입력/field의 compile 거부 4건을 각 mode에서 실행했다.

| Mode / 범위 | packages | run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---:|---|
| normal / orm-generator | 2 | 1176 | 0 | 2.435s | `6c198462e840effe1c7b8e28808470bc336ee48f31de92f43ece9d31ec936741` |
| normal / native-consumer | 1 | 5 | 0 | 3.978s | `40095f3a615b1e75c3cd8bd1b8362a825404145953364ce3684e559b4e8f03da` |
| race / orm-generator | 2 | 1176 | 0 | 38.228s | `f43644e603057ba1568830b286d4eab0ad7a0ef52fa2188ed38415eb1ea5f627` |
| race / native-consumer | 1 | 5 | 0 | 34.265s | `79aa0fe4b8147f519706c8bb6da236bae5e3e3b17e957860fbad5a841c9106f8` |
| cgo0 / orm-generator | 2 | 1176 | 0 | 2.36s | `0030d1476d3423a34a94277f348a278df3f9da118e587196cef4cae4699a9894` |
| cgo0 / native-consumer | 1 | 5 | 0 | 3.778s | `c4eaa819dfbcc0c781f2b40147cfd88f17ceec107bc171447dbb3738642d8dc7` |

생성 module은 각 mode에서 57개 test/subtest를 시작·성공 각 한 번씩 확인했고 필수 47 paths와 skip 0을 대조했다.
외부 root의 5개는 생성 소비자 owner와 다른 모델 입력·다른 모델 conflict target·primary key update·관계 경로를
root conflict target으로 쓰는 네 컴파일 거부 사례다. ORM 입력 준비·nullable 소유권·metadata/cache snapshot,
명시 ID/자동 ID 혼합·원 입력 순서·한 transaction의 여러 batch·parameter 한도·빈 입력의 context/capability,
부정한 callback owner·뒤쪽 배치 오류·commit/rollback/cleanup 불확실성·취소 후 결과 차단을 포함한다.

실제 DB에서는 auto-only, 큰/0/음수 key, default/SQL NULL, integer/boolean/float/decimal/duration/time/date/datetime/
UUID/binary/JSON의 batch 왕복 저장, conflict-ignore의 실제 count와 FK 오류, 단일/복합 고유 target·update mask,
동일 key의 native batch별 차이를 검증했다. 두 연결의 normal/ignore/update 경쟁과 loser의 앞선 batch rollback,
ordinary/coordinated/relation/coordinated-relation 부모의 commit/rollback·실패 child 뒤 부모 재사용,
root cursor connection affinity·read-only 거부·부모 종료 뒤 query와 캐시된 관계 사용 거부도 포함한다.

Receipt `godj-bulk-orm-y0l9zf4p/receipt.json`, SHA-256 `7f409ab4fc80fa80955cb47e5cd1e6668f337f3be97bfb5f4513f59694589d0c`.
전후 source 동일·schema/session `0|0`·소유 DB/container 제거를 확인했다. 별도 module log SHA-256은 normal
`43f31582e173758ba46e91b4583790c91c3a8581658079349c3e7a9145654748`, race
`08422bafd3522344f9ed409f440f3a5f757732269802ccfd2faa975f9c006e12`, CGO0
`9cc2ec29c4eff83007fc56101cbb4468015090c844fb84a033278e903fc3dbd6`이다.

같은 source에서 format·docs 검사, 7개 project와 Unicode의 generated drift, ORM/생성기/새 AST·backend의 affected vet를
확인했다. Generated drift 17.238s, vet 28.318s; static receipt `godj-bulk-orm-static-5e9y1pa8/receipt.json`, SHA-256
`895cead661fd0807009075fd9ccf09ac60c1b959b5fdb17b4105ec188ec6bb63`. 두 실행의 source inventory는 같다.
이는 Helpdesk bulk 업무 흐름, 정식 Django fixture의 직접 비교나 Hosted 전체 검증은 아니다.

앞선 네 실행은 실패로 유지한다. 첫 실행은 생성기 golden 미갱신과 outer `-trimpath`의 runtime.Caller 경로 문제,
둘째는 nullable Owner의 세 반환값을 두 값으로 받는 소비자 작성 오류, 셋째는 borrowed session에서 root용
`Using`을 호출한 소비자 작성 오류, 넷째는 의도대로 거부된 primary-key update의 Go inference 오류 문구를
잘못 판정한 검사였다. Golden/생성기 version을 갱신하고 outer 명령만 바로잡았으며 생성 module의 `-trimpath`는
유지했다. Nullable presence와 `UsingSession`을 명시하고 두 종류의 정확한 타입 거부 진단을 허용했다.
실패를 성공으로 바꾸거나 이전 결과를 최종 실행에 합치지 않았으며 각 소유 container의 제거를 확인했다.

| 실패 receipt | 해당 source inventory SHA-256 | Receipt SHA-256 |
|---|---|---|
| `godj-bulk-orm-7nx2q60u` | `2428618400707042039af664634297eff5b57a21a0fb6df5f95e3bcf4d983416` | `9da6d70875e02f6d86716c8673a8ce8fbc09a9b715c881c0065fd3a4d11f41a6` |
| `godj-bulk-orm-ku6p7gzg` | `0354ba379d0a2ccba2402e9342f2aa42f241a0b58a959e7f90743de38d00e44d` | `13bf0adc689df81e66e0c1f30ee47ee2d0a45ce7f5257be3ffec1a98b9da6af0` |
| `godj-bulk-orm-92nn2a11` | `3b70788549390e735bb8c76a984a643f2439d7eda49765ccacc69006e744fbd3` | `a4040558af0ab4b9f1d80ca3525f229fdebb9031f6f344dac0b4ff81f26c9a8f` |
| `godj-bulk-orm-zmdfvj9q` | `a7fd6a381da791213518c4c3b84fc180977d8b4accf44757577a157fcc486ee1` | `9b305d4830d5667e95c99245e6ff44d6b73cbeff12047a0874db9fc262dcaff0` |

### 정식 Django bulk-create fixture와 Go 직접 비교

Go source/output/expected fixture를 읽지 않는 `conformance/runners/django/bulk_create_reference.py`의 authored 23개
사례를 각 DB에서 두 새 프로세스로 실행했다. Python 3.14.3·Django 6.1·psycopg 3.3.6,
SQLite 3.50.4와 PostgreSQL 17.10 Debian/UTF8/libc/C/C다. 사례마다 table/sequence를 다시 만들었고
Django QuerySet·Atomic·SQLInsertCompiler의 전체 module hash와 observer hash를 fixture에 기록했다.
두 실행씩의 byte 일치를 확인한 원 출력만 `codegen/consumertest/testdata/bulkcreate/`에 복사했다.
Observer SHA-256 `f31b5c6caa8341f14105791d8854e790045c8c609df6685ba2b8b32067e0b7f8`.
SQLite 출력 13155 bytes / `e1c9b4e5177f36364c292f9be0d121d3707e8d32f4defe2fcbdeac01e2b4c793`,
PostgreSQL 출력 12433 bytes / `1c4224ef97c40c28fbe6a094c56b965d67065f1b4ff1e9c973af81974497d44f`.
Native receipt `godj-bulk-create-reference-f806thfs/receipt.json`, SHA-256 `e615491f67023d7a2ea83e85141cf00e50815a1708bfa86036da88c3ce3290cd`.
Observer 유지·최종 table/session `0|0`·소유 DB/container 제거를 확인했다. 이 native 실행 자체는 Go PASS가 아니다.

이후 parent `64bdce4e41da5545419aca5a941aa5dbff18931b`, 비Markdown 3009 files /
inventory SHA-256 `4b0f57258aec2bcf3b3673918986d90a9347eb2ada9cce7cdb122b195e198544`에서 생성 Go module로 직접 비교했다.
Observer/upstream hash·23개 case 이름/순서·모든 필수 경로를 확인하고, int64 pointer로 native key를 읽어
2^53 초과 값을 float64로 손실시키지 않았다. 반환 객체 순서/값·실제 batch 수·원 입력 보존·native 원인 및 SQLSTATE·
최종 Item/Group 행과 원자성·명시한 Go scope 차이를 대조했다. 기존 native bulk 소비자 57 paths와 네 compile
거부도 같은 source에서 함께 실행했다. 정식 대조는 양 DB를 포함한 필수 47 paths를 매 mode에서 확인했다.

| Mode / 생성 소비자 및 기준 대조 | packages | outer run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---:|---|
| normal | 1 | 6 | 0 | 8.63s | `d84b97fefbf217f01225ec63f0cd3a310a0ecc3d88899bda58250d42bad927b1` |
| race | 1 | 6 | 0 | 40.067s | `705fac94f2721c1c89be31da490e623069d68b725e71f2e3aa9cc637e090ce2a` |
| cgo0 | 1 | 6 | 0 | 6.188s | `7552805d6423adc77351c1b74e6e77317db959399d8ae302c20d864bc73b2e17` |

비교한 generated module log SHA-256은 normal `33ce7521fe2f99dd4f9a2c2585820b442c11e9c506bc862e4c381fc44c38e051`,
race `2b42df4c969dff1a35dc6d74bb4b522879b803cb5d7455cee3832dbfd86ba57e`, CGO0
`bbbde21d031a59cea7c6cd5a10fac6954513e1fb462237c15eee7d6b0c91603e`다. Source 동일·schema/session `0|0`·
DB/container 제거와 실행 누락/skip/잘림 없음을 확인했다. Receipt `godj-bulk-create-comparison-7as90xr3/receipt.json`,
SHA-256 `ba8aeb9d194d0712931e4fc8745ce149214c678ec1e0d822db4945998040d31f`.

[ADR-0088](../adr/0088-bulk-creation-and-native-batch-ownership.md)의 Go 차이는 native fixture를 고치지 않고 별도 조건으로
검증했다. SQLite ignore의 CHECK 생략, typed non-null에 대한 NULL 거부, 빈 입력의 invalid policy 거부,
항상 소유하는 write scope와 borrowed savepoint·부모의 재사용·호출자 모델 보존을 포함한다.
Literal COMMIT의 지연 FK 오류는 기존 `commit_outcome_unknown`을 유지하며 native SQLite 787/PostgreSQL 23503 원인과
최종 빈 DB를 별도로 확인한다. CHECK는 authored DDL로 검증했으며 Schema IR CHECK 선언 기능의 완료 주장이 아니다.

첫 비교 실행 `godj-bulk-create-comparison-_l1cwu5q`는 CHECK native 오류를 아직 분류하지 않았고 literal COMMIT을 일반
IntegrityError로 가정한 comparator 때문에 실패했다. 제품의 보수적 outcome 정책을 변경하지 않고 native CHECK
원인과 `commit_outcome_unknown`을 구별하는 비교를 추가했다. 해당 receipt SHA-256
`b7becfb72617d6977aeaeb5a483720028a7a2fbbf315fcc3d0cb25d940296885`는 실패로 보존하며 소유 container 제거를 확인했다.
이 단계는 Helpdesk의 bulk Form/Admin/API/client와 새 source의 Hosted 전체 검증을 포함하지 않는다.

### Helpdesk 여러 티켓과 공통 입력 표면의 영향 통합

2026-10-02 KST, parent `0afc838b98b20160272bd9d30802ebc30ce520c5`, 비Markdown 3025 files /
inventory SHA-256 `01b5330f57e62e501051f656a2f8a252e093a51910f3e51882bd3026e77ce80a`에서 실행했다.
Go 1.26.5/darwin/arm64와 공유 cache, 실제 SQLite·PostgreSQL 17.10 Debian/UTF8/libc/C/C를 사용했다.
`orm`, `admin`, `api`, `api/openapi`, `serializers`, `examples/helpdesk`, `api/openapi/consumertest` 전체와
`codegen/consumertest`의 `TestGeneratedBulkCreate`·`TestGeneratedBulkCreateReference`를 각 mode에서 실행했다.
생성 자식의 race·trimpath·offline profile을 유지했고 실제 DB가 필수인 경로의 skip은 허용하지 않았다.

| Mode / 범위 | packages | run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---:|---|
| normal / affected | 7 | 5896 | 0 | 93.212s | `fffca74adc28e0a68a8fd8f0ce60e4c93fef3fb19fdd5b78202c422d7d9492d3` |
| normal / native-consumer | 1 | 6 | 0 | 26.144s | `88821003b7b11109caea423a0fd742a3bb072005e85af58a921d0c81ae9f23bd` |
| race / affected | 7 | 5896 | 0 | 388.44s | `6af6ae4c30acf99525465067599d82b29dc465b91a276d583c7c516f5978733a` |
| race / native-consumer | 1 | 6 | 0 | 67.662s | `9725b67920a4a747737ce4eee078dc579c06f1b0a8395075db326ab98f3f08d1` |
| cgo0 / affected | 7 | 5896 | 0 | 60.277s | `f86d923aed7b91691f46df921311a255f0dc2150343727266f0400b67d7acb7e` |
| cgo0 / native-consumer | 1 | 6 | 0 | 6.004s | `848329758355a75c3408d7cb9f1835420e26f85a66e18ce883b2de1c0d3ad31b` |

영향 묶음의 필수 692 paths와 모든 실제 run/pass·package 종료를 정확히 대조했다. 새 Helpdesk 경로는 양 DB에서
API 44개·Admin 43개 사례와 큰 choice HTML·두 연결의 leader commit/rollback을 포함한다. 전체 40행을 실제 두
native batch로 저장하며 scalar/default·라벨·JSON/digest·행마다의 audit, 입력 index와 escaped 원문을 확인했다.
현재 권한·CSRF·Category·라벨 범위, 위조된 서버 필드·management·중복 UUID, body/item/전체 출력 예산, 많은 진단과
진단 예산 초과의 전체 거부를 검사했다. 두 번째 batch·반환 key·링크·저장 후 scope·reload·audit·취소 실패는 전체
rollback이다. 잘못된 callback owner·반복/동시/보관/오류 삼킴과 rollback/commit unknown·commit 후 취소를 구분했다.
두 연결 검사는 별도 read snapshot에서 미commit Ticket/audit가 보이지 않음을 확인한 뒤 follower 결과를 대조한다.

독립 ogen client는 실제 문서와 재생성 byte drift, 1..40 cardinality·indexed 오류·권한/CSRF·scalar/JSON/digest·
두 새 티켓 저장을 확인했다. 부모가 최종 DB와 audit를 별도로 읽는다. Wire 검사는 큰 int64 key/label·필수 shape·
누락/null/타입 오류와 명시적 Validate·500의 한 번 전송을 확인한다. ORM의 직접 제약 오류와 실제 cleanup/cancel
오류 소유권을 추가로 검사했고 정식 Django 양 DB 대조와 기존 native bulk 소비자·네 compile 거부도 새 source로 재실행했다.

Receipt `godj-bulk-helpdesk-dnkocv4z/receipt.json`, SHA-256 `9249d205aa0de6253da4bf4c466c9c1f3bf31fdd971c816044efcae9620892a7`.
6개 구간 모두 PASS이며 실행 전후 source 동일·schema/session `0|0`·소유 DB와 container 제거를 확인했다.
다른 source의 앞선 부분 성공을 이 실행에 합치지 않았다.

첫 실행 `godj-bulk-helpdesk-aa8nb1bu`는 6 packages, 5064 run / 5023 pass / skip 0으로 실패했다. Admin이 서버
Category를 typed 준비 뒤에 설정하던 순서를 `PostClean`으로 바로잡았다. ORM은 취소가 없을 때에도 `errors.Join`으로
직접 제약 오류를 감싸던 문제를 수정하고 실제 cleanup/cancel 오류는 계속 보존한다. 동시성 검사의 observer는
coordinated write를 다시 열던 audit 조회를 같은 read snapshot의 조회로 수정했다. 실패 receipt SHA-256
`805c96d7b2ad748394cde75657c9e11fb079b49ddbc48baa6512f0fe0a94db53`, source inventory
`5b995a8f021487cc914d1876283a7dac4e890ba5e117699f9e2332b77177c183`; 소유 container는 제거했다.
그 이전 preflight는 각 package에 임의 개수 이상의 roots가 있어야 한다는 harness 가정으로 DB/test 시작 전에 실패했다.
실제 필수 root를 확인하도록 바꾸었으며 이 실패를 제품 실행이나 PASS로 계산하지 않았다.

### 실제 브라우저와 최종 정적 범위

[Helpdesk 브라우저 fixture](../../examples/helpdesk/testdata/browser/README.md)는 새 임시 SQLite의 실제 identity·session·
audit Runtime에 Admin·API·기존 편집기를 연결한다. Playwright CLI 0.1.22의 새 실제 브라우저에서 Admin 22개,
편집기 9개 검사를 통과했다. 최소/40행 최대와 programmatic click 경계, 행 재번호·원문/선택 보존, 오류 재표시와
수정 후 bulk 생성, 기존 행 변경과 새 행 두 개의 전체 거부/commit을 확인했다. Screenshot을 직접 확인했다.
종료 뒤 별도 DB 조회에서 티켓 5개·정확한 링크 4개, `browser-operator`의 change 1개·add 4개를 대조했다.
브라우저/서버 종료와 fixture DB 디렉터리 0개를 확인했다. 최종 화면의 console error/warning은 0이며 첫 로그인의
favicon 404는 제품 경로 오류와 구분했다. 이 브라우저 증거는 SQLite이며 실제 양 DB 실패/동시성은 위 checkpoint가 소유한다.

브라우저 receipt `output/playwright/bulk-create/receipt.json`, SHA-256
`07f0b8ea9ae38a240ab37152ca68f8944ecaaf5a2b86b0e38c5379ee49d8dc59`.
Admin log `fd37f4d10ea6f160da53c5f3cd54e6c79c6d696beb586efbf881534d84670ee4`, editor log
`5e41178cfc7f1521365d32433d05cf11a5ebc4e0eca80099d282aacd481b94fb`, 종료 후 DB/audit log
`0d9f289301e5c36cd3677b98685a720c055b2471ac88bd3744d25b653a51b12c`.
첫 browser probe는 숨긴 최소행 remove 버튼을 role locator로 찾다가 실패했다. 제품 코드를 바꾸지 않고 실제
data selector로 hidden 제어를 확인하도록 probe를 수정했으며 새 서버/DB·새 브라우저에서 전체를 다시 실행했다.

영향 checkpoint 뒤 비Markdown 변경은 재현용 browser 파일 3개 추가와 CI 필수 목록 2개뿐임을 직접 비교했다.
실제로 브라우저에 사용한 fixture/probe와 체크인 파일의 hash가 같다. PostgreSQL CI는 명시 root regex를 사용하므로
새 native bulk·생성 소비자·Helpdesk bulk 경로 98개를 추가했다. Relation 필수 경로도 15개 추가했다. 추가한 경로는
기존 native 기반/위 영향 checkpoint의 각 mode에서 run/pass 한 번씩 존재함을 대조했으며 native 기반의 query/db
source가 변하지 않았음을 확인했다. 이 대조는 Hosted 실행을 대신하지 않는다.

최종 비Markdown 3028 files / inventory SHA-256 `de3d4b6595318c3566e323e88159cb076bd7461bbb01f2c7865f33563a804eb9`에서
`make format-check docs-check`, `make generate-check`(7개 project·Unicode), 관련 9 packages의 `go vet`,
CI 도구 52개 검사와 `git diff --check`를 통과했다. Generated drift 24.162s, vet 0.793s.
Static receipt `godj-bulk-final-static-4itr1abj/receipt.json`, SHA-256 `0bca6f29b35034a5f4935386681c1a036b30b02bd9f3efb8d88d6145dc123ce0`.
실행 전후 source 동일이며 전체 platform/cold/process·새 capture 검증은 이 작업의 Hosted full milestone이 소유한다.
GDJ-0108 source의 Hosted 결과를 새 bulk source의 완료 증거로 사용하지 않는다.

## GDJ-0108 — 행 잠금과 조회 후 생성 또는 갱신

### 고정 source의 Hosted 전체 통합 완료

2026-10-02 KST, 제품·생성기·기준·소비자·업무 흐름을 commit `f6e95bb81f80605491bbf04feab49017f79f49e4`로
고정하고 작업 branch와 기존 draft PR head에 push했다. [PR feedback 36899659510](https://github.com/progresshans/godj/actions/runs/36899659510)은
필수 whitespace·Fast Go feedback·scope 보고까지 PASS다. 실제 checkout `08a6af2c8825cbf0e844ccc4e3ae2c5f92ba1007`은
대상 source를 parent로 가지며 tree `061c6b1a3e80346c2532f1059300815f31c1757d`가 일치한다. Job `110495357815`,
log SHA-256 `e6878cbf47deccb654eafde29b7a3d48a9cceb13dbc323a42bdfa902e61d486a`, receipt `godj-upsert-feedback-2528x0u5/receipt.json`이다.

같은 source의 [Hosted full 36900514942](https://github.com/progresshans/godj/actions/runs/36900514942), attempt 1을
수동 full scope로 실행했다. 2026-10-01 17:35:34–19:16:53 UTC, 65개 job 전부 success로 끝났다.
고정 commit의 workflow와 package/matrix 선언에서 도출한 65개 job 목록·8개 필수 owner와 모든 실제 checkout을
대조했다. 각 named step과 조건별 제외 단계가 선언에 맞는지도 확인했다. 로컬 전체 `make ci`는 중복 실행하지 않았다.
Job 실행 audit SHA-256 `e7e807f1a2b497a37fd725b920587838237a4b9a81f65196f907c4b670883a9b`.

Linux amd64/arm64와 macOS amd64/arm64의 relation product·project check·command product를 normal/race/CGO=0으로
확인했다. Relation product의 12개 platform/mode 조합은 실제 발견한 80개 consumer roots를 누락·중복 없이 실행했다.
Intel macOS race의 3개 consumer shard와 별도 runtime owner도 합쳐 대조했다. 공통 discovery SHA-256
`ee44a12f5833f584e8eb860eb0ee13b19f082ca8188589a4a2ca47f1e9e42c02`. Portable Go·정확한 darwin/arm64·
conformance와 Python 3.12.13/3.13.15/3.14.3/3.14.7 compatibility owner도 완료했다.

PostgreSQL core는 각 mode에서 15 packages / 4823 run/pass / skip 0, 필수 2323 paths를 확인했다.
각 mode의 S3 23개 필수 경로와 실제 reference service 준비·child/server 정상 종료·reap/정리도 확인했다.
S3 archive SHA-256은 normal `7f2804935b2aab5bd174aac29fc66e367fccacd056aa9d1f5e5e3d5b7493b0ee`,
race `cf52973966e9a2f250e8f9fcc0839dbe1e264bc7e8affa1aa0c435c331357902`, CGO0
`305bb3b0f6f262001da1c30e78e280e6d08333688a070a83451c5f65b572008c`다.
별도 operator-target PostgreSQL 세 mode도 필수 owner에 포함된다.

| 새 capture | producer job / artifact | archive SHA-256 | payload SHA-256 |
|---|---|---|---|
| systemstate-postgres-1 | 110498289549 / 11182986094 | `1843ae1fe0f514b5957e91865f42e552dc505670a14509c9b5d1c9836145f7fc` | `2add41e5cb3ac54525d83a579a6cc9a34f63b2ebac7fad5eaa88a5cd4f75d0a3` |
| operator-postgres-1 | 110498289728 / 11181696861 | `2c9e037e3dec3ac5de0ccac465897ca5592280c640b0e94897b757835ed8cab4` | `fdab8ad8d3c627d556f432b4a9dc1b39ae6078c1a752b76e8f9d896f78dda4c2` |

두 producer 모두 같은 run/attempt의 성공과 고정 source checkout을 확인했다. Artifact digest·안전한 ZIP의 정확한 파일
구성·payload provenance를 검증하고 source binding을 working tree 대신 `f6e95bb8`의 Git 객체에서 다시 계산했다.
System-state는 713 files / 7335446 bytes / `06527a77b2cadee1d086c9d423189320be471ec5e221b475323420dfceb0ff11`,
operator는 791 files / 7200003 bytes / `0154ae91b5dbfadb9ecf9ab4584903c585183783cf55a26c3bf7f438526e995f`로 일치했다.
행 잠금·read/modify/write·update-or-create의 새 source도 선언된 결합 범위에 들어감을 확인했다.

Consumer job `110518754119`의 10개 필수 단계를 모두 확인했다. 같은 run의 새 capture 해석/다운로드/검증과 실제
conformance 소비, 32-bit Linux migration/project-check/runserver compile·relation product 실행, 저장된 oracle와 DRF
checksum·artifact 비변경 검사를 포함한다. Log SHA-256 `987116f68f329bc0877aa492a90fdc1327a74caad5f164c33a171047f5636e4e`.
최종 집계 job `110540306723`은 `full_platform_verified=true`와 8개 owner 전부를 보고했다. Log SHA-256
`1fdc51437ec497cefda9dfb591fe31b264d35f5ca209d180c5294d34fe389641`.

최종 receipt `godj-upsert-hosted-full-7x5let25/receipt.json`, SHA-256 `4c95d0d0965b6b91a35bb22231de2545d0b24976c99da33246789f4a8619f3ff`.
Dispatch 당시 queued는 `dispatch_status`, 현재 상태는 completed/success로 구분했다.
이 source 뒤의 완료 기록 변경은 Markdown뿐이다. 별도 bulk 작업의 새 제품 source는 이 Hosted 검증에 포함하지 않는다.

### Helpdesk 저장·Admin 선택 입력·독립 client의 영향 통합

2026-10-02 KST, parent `f04bb77d806c6e5337aa3f08fcc37b71b2c1a4f8`, 비Markdown 2986 files /
inventory SHA-256 `c781fcaf70005d92cea1ec5a567b6b2992a5a5b06d9e94bdf44ec3190626ad27`에서 업무 흐름을 검증했다. Go 1.26.5/darwin/arm64,
실제 SQLite와 PostgreSQL 17.10 Debian/UTF8/libc/C/C, 공유 cache·독립 client의 실제 race 전파를 사용했다.

| Mode / 범위 | packages | run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---:|---|
| normal / admin-api | 2 | 521 | 0 | 1.975s | `9087d2ea1587fe4eae974cb86b01656050c71a59e1388a82d0a7fa8054dd3a17` |
| normal / helpdesk | 1 | 650 | 0 | 45.668s | `8ddcec10d900fe097781a1af8e3ff4a72d3392b4141606159f0c62251e0d5b08` |
| normal / independent-client | 1 | 10 | 0 | 6.185s | `1c8cb0d95b5ca9f3cc69595ebb0350e3e94d0678017d6d22fa48ba5bd8cd5831` |
| race / admin-api | 2 | 521 | 0 | 20.638s | `585db05fbbbc09b420e0aee5ca749d33f24e282af7c4f76a7230a9fedf623c9e` |
| race / helpdesk | 1 | 650 | 0 | 264.261s | `855d6350044eac9c7b2bae64458bde23440ba2b172897b03676767111a8dc3ab` |
| race / independent-client | 1 | 10 | 0 | 53.941s | `108976145724dfb7555c72528ef5063091ee57a314bd8604a2462ca34832970e` |
| cgo0 / admin-api | 2 | 521 | 0 | 2.367s | `2d9213b1c95e5e5d2b8c93878fbd92c198dca6dff76f944b25be8542c9db43de` |
| cgo0 / helpdesk | 1 | 650 | 0 | 45.483s | `1f81717f7b3a8e7cc5eb0db7dd3d9a67fa62984e76959bc00e3cc259aef00e2b` |
| cgo0 / independent-client | 1 | 10 | 0 | 8.073s | `2c83b019df02df8d8191a4dba28e8ad228e4da02ee7f927fde057bee82f0f912` |

Admin/API는 2개 package 전체와 새 선택 입력·현재 목록 재검증·권한·CSRF·변경 결과의 필수 24 paths를 확인했다.
Helpdesk는 전체 650 paths와 필수 474 paths를 확인했다. 양 DB의 API/Admin 분기·권한·Category/Ticket 범위·
행의 늦은 이동·native unique·입출력/감사 오류·취소·불확실한 결과·잘못된 callback owner와 정책을 포함한다.
실제 두 Runtime의 동시 HTTP 요청에서 한 행의 생성/갱신·commit 전 비가시성·add/change 두 audit를 확인했다.
PostgreSQL은 보유한 Ticket 잠금을 별도 연결의 NOWAIT/55P03으로 확인했다. 기존 Label 확보 회귀도 전부 포함한다.
독립 client는 실제 HTTP 201/200·변경/무변경·생략 false·foreign 404·blank 400·권한/CSRF 403과 ordinary POST
중복 거부, 최종 DB/audit를 확인했다. 생성 wire의 두 성공 union·필수 nested 응답·정확한 큰 정수·500 무재시도도 포함한다.

통합 receipt `godj-report-save-impact-rsngcckm/receipt.json`, SHA-256 `a05f5fa0f56b934ce692b0a628ab6814f0572efa53716bad127ff982d833d0bd`.
첫 실행 `godj-report-save-impact-qxrvm5oz`은 normal/race의 6 sections가 모두 통과한 뒤, 새 Playwright 로그 두 개가 inventory에
추가되어 source guard가 중단했다. 원 2986개 source의 변경·삭제는 없었고 두 산출물만 `output/playwright`로 옮겼다.
그 실행의 receipt는 계속 실패로 보존한다(SHA-256 `a1eec01a3596b25ccfef31a6097e9e2d3399a636ad3a8594fefb88b10420654a`). 동일 source·원 log hash를 대조하여 완료한
6 sections를 위 receipt에 출처와 함께 보존하고, 미실행 CGO0의 3 sections만 새 소유 DB에서 실행했다.
각 필수 start/terminal·정확한 package·no-skip·잘림 검사를 유지했다. 두 container 제거와 최종 source 동일·
schema/session `0|0`·별도 DB 제거를 확인했다. 다른 source의 결과를 이 통합에 가져오지 않았다.

외부 client는 실제 exporter의 Helpdesk OpenAPI(SHA-256 `e083ecb2064acaad9743c2501b4426e24e696d091a02922beede74ddf609d91c`)를
고정 ogen으로 재생성했다. 다른 다섯 문서와 dependency lock은 byte 동일하다. 생성 receipt
`godj-report-save-client-_feg0zwn/receipt.json`, SHA-256 `1887eb378d4cf029662c27d8aa8939b9d245d51880fa1e7c1afd750e19a24ca4`.
기존 UUID validator 경고 4건을 새 기능의 실패로 해석하지 않았고 실제 별도 module build와 HTTP/wire 실행으로 확인했다.

### 실제 브라우저·부정 대조·마지막 정적 검사

별도 synthetic SQLite DB와 공개 Helpdesk AdminRegistry/SystemState Runtime으로 실제 서버를 실행했다.
Headed Playwright에서 로그인·빈 목록의 command·현재 Category 티켓만 표시되는 선택 입력·trim/HTML escaping·
생성/변경/무변경 안내와 같은 ID 유지·공백 summary 오류를 확인하고 저장 목록/오류 Form screenshot을 직접 검사했다.
서버 종료 직전 DB는 Report 1행, 원 Ticket 1, 수정 summary와 completed=true였으며 audit는 add/change 두 건뿐이었다.
Browser와 서버를 종료하고 임시 DB 제거·원 source 동일을 확인했다. Receipt `godj-report-save-browser-m_o5z1ql/receipt.json`,
SHA-256 `96d80d7ac460693e5f87bbf2e85da91dbc97f27c9a480400669fd0f863118723`. 초기 favicon 404 외 기능 요청 오류는 없었고 최종 화면 console error/warning은 0이었다.

별도 전체 source 사본에서 다음 다섯 훼손이 의도한 검사 실패를 내는지 확인했다. 실제 원 source는 변경하지 않았다.

| 훼손 | 검출 근거 |
|---|---|
| PostgreSQL의 명시적 OF target 제거 | root/관계/projection compiler의 `lock compile` 실패 |
| 불확실한 rollback/cleanup의 생성 복구 허용 | ORM의 `unrecoverable result` 실패 |
| 독립 Django fixture의 created 결과 반전 | 양 DB 생성 소비자의 `native result differs` 실패 |
| 기준 case 한 개 삭제 | 필수 observer/inventory 결합 거부 |
| observer SHA-256 손상 | 원 observer 결합 거부 |

전후 정상 compiler/ORM 검사와 복원한 양 DB 전체 UpdateOrCreate 생성 소비자는 PASS, skip 0이었다.
원 source 동일·사본 완전 복원·schema/session `0|0`·소유 DB/container 제거를 확인했다. Receipt
`godj-upsert-negative-qdm1w7rm/receipt.json`, SHA-256 `b9af668259383d757dbc656660608281405327473c0d4e9ff415b495b1350a11`. 개별 실행 log hash는 receipt에 보존했다.

`go vet ./query/... ./db/... ./orm/... ./codegen/... ./admin/... ./api/openapi/... ./examples/helpdesk/...` PASS.
`make generate-check`는 Unicode 2 tests·7개 실제 project의 check·checked-in relation product까지 PASS.
CI package/필수 sentinel ownership 검사는 unittest discovery로 13 tests PASS였다. 처음 module import 경로 오류와
직접 script 호출의 0 tests는 검증 성공에서 제외하고 discovery로 고쳤다. Receipt `godj-upsert-final-checks-hrj50skd/receipt.json`,
SHA-256 `f4ed4a070af5610be5ce1db23b7caebc5d3f60535a40bfa160d440142d14e9e2`. 이 기록은 영향 검증이며 새 source의 Hosted/full-platform 성공이 아니다.

### 독립 client의 초기 실패 보정

이전 후보 `godj-report-save-impact-g356ivvd`(inventory `e6e39f1c0b481c5bd132ac9b421b2fe5032fbbc944252309b34fb4c61f9719e8`)는
normal Admin/API 521·Helpdesk 650 paths 통과 뒤, 독립 client가 빈 문자열의 오류를 `required`로 잘못 기대하여
10 run / 9 pass로 실패했다. 실제 serializer의 `blank` 계약으로 기대를 보정했다. Race/CGO0은 미실행,
container 제거 확인, receipt SHA-256 `717efe2ce701879813ba59e3855993144efc3e92060130e6045c8d8b53322065`다.
수정 뒤 독립 client의 targeted normal은 10 run/pass·skip 0·6.983s였고 log SHA-256
`9308eff7006b88cfb8d6110b22f88ba2a72c01d229754e304027bf9d17438f1e`다. 위 통합은 이 보정 후 별도로 수행했다.

### Helpdesk 저장 흐름의 초기 영향 실행과 보정

아래 실행은 실패이며 부분 성공을 새 기능 완료로 표시하지 않는다. 모두 normal에서 중단되어 independent client와
race/CGO=0은 미실행이다. 네 후보 모두 Go 1.26.5/darwin/arm64·같은 PostgreSQL 17.10 profile을 사용했고
소유한 container의 종료·제거를 확인했다. 이후 source의 검증은 별도 완료 기록을 따른다.

- `godj-report-save-impact-6yt3b9dn`: 새 Admin 테스트가 정수 0을 잘못된 선택 값으로 가정하고 메시지 본문을 잘못 기대했다. 자료형이 틀린 선택 결과와 실제 invalid_choice field/code를 검사하도록 보정.
  Source 2986 files / inventory `b6beb208aee6d4176e35c4ed04006a98711ba5ef258f4b780812e2dabeed9084`, receipt SHA-256 `7aae0a648b29d86c43b5d65bff05abd3a47460c5e745cc2f6c4710e31f21f104`.
  admin-api: 521 run / 517 pass / 0 skip, 2.611s, log `ebe8b2c5402f4ff10d36dca87b23e88ef9bebaba6be43a6241f23474f9ae8b01`.
- `godj-report-save-impact-2sh08noc`: 새 27번째 API operation에 대해 기존 26개 표면과 인증 구성 수를 유지하던 검사 실패. 새 요청/응답 schema와 27개 admission을 명시.
  Source 2986 files / inventory `cc2b849fc00897c1075adad98be2bd308aa4f1a1c6ffb412f8ab58d5e844ff28`, receipt SHA-256 `c04e6f4c80b7383e657e4c6050f0f47a16b4015e6bf924fa9157e7a3b83739f2`.
  admin-api: 521 run / 521 pass / 0 skip, 3.178s, log `e7f00f6e9d685119b49b2f4acbe18637e4d52691f8a71632f6de296f58310a7a`.
  helpdesk: 78 run / 74 pass / 0 skip, 9.522s, log `1071c2e248baf3d4297d38780f30e319ac5fc19a8d4664f873a068c155f4eec1`.
- `godj-report-save-impact-98z9rtnh`: 기존 route별 permission 순서 목록에 새 경로가 누락된 검사 실패. 명시적인 route 이름별 primary/additional permission 계약으로 정리.
  Source 2986 files / inventory `79ab2243c69c7d668485ed6862375e9927749796e3e2db660b27db232999bc0b`, receipt SHA-256 `da01c48e2373cd9dc099e75e09dcc104800aa4b092dd95b69a36baa353a62368`.
  admin-api: 521 run / 521 pass / 0 skip, 2.218s, log `11b378150cab2f43b17b3589e7e8593515db394583a31cdb8cf8ccdeb52844c6`.
  helpdesk: 78 run / 75 pass / 0 skip, 13.515s, log `ebbf1541f739bb0ad21dddaac5c5856d46d233a03846ba55e07dfbd90874b418`.
- `godj-report-save-impact-lmw7_upo`: 업무 개별 case는 실행됐으나 새 동시 요청 검사가 AuditHistory의 오름차순을 반대로 해석했고, snapshot 검사 한 곳에 26개 인증 수가 남았다. 실제 audit reader/test의 계약과 일치하도록 보정.
  Source 2986 files / inventory `ebfba173b218faeb647f04806886ab12c72e3ee589a4624c6a432f7e7757def6`, receipt SHA-256 `0a01765d9e2bf9d853045f6107bf24b8564b9b9b632787fffbf9355147c36818`.
  admin-api: 521 run / 521 pass / 0 skip, 2.262s, log `9f6e707d1b157dbf803c31f6c0168b406d8761b3498cd047c710f80b5a41955b`.
  helpdesk: 650 run / 643 pass / 0 skip, 48.123s, log `a3f06ac5cbd661ca0e6ca39c3b684bfdabb7255aa55e2b6d046a227311b42a89`.

### 정식 기준의 Go 생성 소비자 직접 대조

2026-10-02 KST, parent `f04bb77d806c6e5337aa3f08fcc37b71b2c1a4f8`, 비Markdown 2980 files / inventory SHA-256
`1fa3f5a99b9f36026eb7872372fa8356b84fa6490375494daf007a7f07fe28e9`에서 위 정식 fixture를 생성 Go module의
실제 SQLite/PostgreSQL 동작과 대조했다. Go 1.26.5/darwin/arm64, 공유 cache·trimpath와 같은 pinned DB profile이다.
기존 UpdateOrCreate·선택 graph·borrowed scope·수명 소비자와 함께 실행했다.

| Mode | 필수 parent run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---|
| normal | 1 | 0 | 9.034s | `a169a5a4b43aba88aa0d88dbc68dcc7052c3c8d6eaf822fe3e5761ca8c813ef6` |
| race | 1 | 0 | 59.652s | `bc7b9e4a3f8933a5f731f207a61ea20c159e1880c124e061c1ac01f26ad7f97d` |
| cgo0 | 1 | 0 | 4.867s | `9fcb4cac7679954f2874f3993c8ecd7645a88351b94a70378aa4aa842411f953` |

Parent `TestGeneratedUpdateOrCreate`는 child의 모든 event를 잘림·skip·실패 없이 검사하고, 새 upsert reference의
backend별 14 cases+최종 행, 행 잠금의 backend별 10 terminals+8 targets+최종 행과 PostgreSQL contention
8 cases를 각각 필수로 검사한다. 기존 행·동시 생성 경쟁은 같은 숫자 counter와 입력 횟수·조회한 기존 값·
created 결과·잠금 조회 횟수·scope 횟수·native busy·commit 전 가시성·최종 행을 원 관찰과 대조한다.
PostgreSQL의 실제 차단과 NOWAIT/SKIP LOCKED, 별도 연결의 root/관계/중첩 대상 및 FK INSERT를 포함한다.
명시적인 Go 차이는 native fixture의 원 값을 보존하고 Go 대안만 별도로 assert한다.

실행 전후 source 동일, schema/다른 session `0|0`, 별도 DB 및 container 삭제를 확인했다.
Receipt `godj-upsert-reference-consumer-9tezrxdw/receipt.json`, SHA-256 `2d361c48b42168f48fb0b593845247f6dcec540fe3bb49d743edb7d5cd8792e7`.
`uv run --frozen --project conformance/reference/drf python -m unittest conformance.runners.django.tests.test_update_or_create_reference -v`
또한 2 tests PASS/skip 0이었다. 두 observer의 SQLite 새 프로세스를 hashseed 0/813에서 비교하고 원 fixture와
observer/upstream source 결합을 확인했다. PostgreSQL의 Python 재실행은 위 별도 native capture가 소유한다.
이 checkpoint는 변경한 기준 소비자 범위이며 기존 Helpdesk 전체와 Hosted 통합을 재실행한 결과가 아니다.

첫 후보의 normal parent는 새 fixture의 `unknown_relation` 오류 분류, PostgreSQL 빈 SELECT의 기존 계약에 대한
잘못된 기대, 이미 취소된 test context를 사용한 SQL 설정 cleanup으로 실패했다. 제품 코드 대신 fixture를 보정했다.
동일 source의 두 번째 실행은 수정 스크립트의 assertion 때문에 편집이 적용되지 않은 재실행이었다. 두 실행 모두
race/CGO=0은 미실행, container 제거를 확인했으며 성공에 포함하지 않는다.

- 실패 `godj-upsert-reference-consumer-jp6t6wfk`, source inventory `b9636a2f977af6929ee5e8c304eef0c19d7447eada27010c24bd6fb9ea8da08e`, receipt SHA-256 `f98b8e3a322795dfc0bde8f337f2504609ffac865b7bb410f3a52ba844ec5b72`, log `ea584f62cc15d8f86b84e964212cb9456ca95adf002c58fd659536425ccebfb9`.

- 실패 `godj-upsert-reference-consumer-cb91b_7g`, source inventory `b9636a2f977af6929ee5e8c304eef0c19d7447eada27010c24bd6fb9ea8da08e`, receipt SHA-256 `fcd5bc7d821542248285d014fe42899ed22132cab8adad7e283c3689de458062`, log `c52333bbe5572a875c1db317a1a55a6e8b6df7acf77dbfb4bebeb93a53b79f50`.

위 생성 소비자 대조를 마친 source에서 `make generate-check`도 통과했다. 7개 모델 project의 실제 generator check와
Unicode 검사 2개, checked-in relation product drift를 확인했다. 이후 시작한 Helpdesk ServiceReport 업무 변경의
실행 성공을 뜻하지 않는다. 새 Admin/Helpdesk/API parent 및 독립 client compile은 통과했고 runtime 영향 검증은 진행 중이다.

### 정식 Django observer와 독립 양 DB fixture

2026-10-02 KST, 작성한 synthetic 입력만 읽는 두 observer를 repository에 보존했다. Go source/output이나
expected fixture를 기준 입력으로 사용하지 않는다. 각각 SQLite·PostgreSQL의 두 새 프로세스에서 실행했고
backend별 결과가 byte 일치했다. Python 3.14.3·Django 6.1·psycopg 3.3.6, PostgreSQL 17.10
Debian/UTF8/libc/C/C의 고정 환경이다. Python 버전은 관찰 metadata이며 이후 compatibility replay에서
같은 Django의 동작을 대조할 수 있도록 실행 진입을 특정 Python patch 버전에 제한하지 않는다.

Upsert는 14개 분기/오류/명시적 옵션과 기존 행·동시 생성의 실제 경쟁 2개를 관찰했다. 행 잠금은 10개 terminal과
8개 target, PostgreSQL의 별도 연결 target/FK 경쟁 8개를 관찰했다. 두 observer 모두 QuerySet·Atomic·SQLCompiler의
source hash와 자신의 SHA-256을 출력한다. 저장 fixture는 각 독립 출력의 원 byte다.

| Observer | Observer SHA-256 | SQLite output SHA-256 | PostgreSQL output SHA-256 |
|---|---|---|---|
| `update_or_create_reference.py` | `f5814eb8daa2f00468c373218095a20b9a81104fcb378a290f680e08ec8e836f` | `2a2df343930bc9716a846945ecf3387f74e2d5c3333d6fadf5fdcff50cfab66f` | `457d238e25eb288a547d3d4cbf4e9dcbc19a005dbe60ed3c623d6e8b07f272f2` |
| `row_lock_reference.py` | `629748323ce836c0b872480db827b720d6a938b29fe7d00c1e3ec6c0fd3a2707` | `1b2b8c4c54250802761eaebd6de1d6e9e8358212c7912279e44c949a3a44fdf4` | `8decddd36ee6f4e51ccdf00062995d4e0d41837d83c16b0ef39902bd1c2fadfb` |

- Receipt `godj-upsert-reference-l94eovi2/receipt.json`, SHA-256 `45afb0e11a29c5a062fbb781104f9852a800c1aca8def0e9ebf498b2417ef4f9`.
- Receipt `godj-row-lock-reference-ux0i2om4/receipt.json`, SHA-256 `a0bf0ed32fdc8345ce247e1bb921256f06daca1dc1c74b54c209665c25e4d90d`.

두 실행 모두 observer byte 보존·schema/session `0|0`·DB 및 소유 container 삭제를 확인했다.
이 기록은 native 기준 검증이며 이후 Go 소비자 대조의 PASS를 뜻하지 않는다. 단건 기준과 마찬가지로 명시한
Go API 차이를 원 관찰과 구분한다. SQLite의 명시적 잠금 오류, filter/value JOIN의 명시적 target 지원, projection의 OF 대상 보존, upsert의 설정한 잠금 옵션 유지가 해당한다.

### UpdateOrCreate API와 기존 소비자의 영향 checkpoint

2026-10-02 KST, parent `f04bb77d`, 비Markdown 2971 files / inventory SHA-256
`5becfce56c855fbee8dc3fe77a8555988aca243ced022a0e55f259133e39821d`에서 원자 upsert·지연 생성/갱신 입력·빈 patch 검증과
반환 graph의 caller 수명을 확인했다. Go 1.26.5/darwin/arm64, PostgreSQL 17.10 Debian/UTF8/libc/C/C,
실제 file-backed SQLite와 공유 cache·독립 module의 trimpath/race 전파를 사용했다.

| Mode / 범위 | packages | run/pass | skip | 시간 | Log SHA-256 |
|---|---:|---:|---:|---:|---|
| normal / runtime | 2 | 1142 | 0 | 2.118s | `aff050c3e2a86a642e6ccac6f9e72b4b1336d5be272530c67050600e1aa597a5` |
| normal / consumers | 1 | 6 | 0 | 13.536s | `6d1d517dcfabff2cadd95fc57a95c1b22af2a966a46f5c50a0ecb735f8b3a6df` |
| normal / native-policy | 2 | 6 | 0 | 5.130s | `4c430e70a09b536881d6f7cf3b1f054bf02b4ca186a6e3a93f8cc8695c03e4aa` |
| normal / helpdesk | 1 | 488 | 0 | 35.409s | `f6c9dd89a81cd25ad171186a44d7a6b7fa26e675930e0180effd3c381ec2d748` |
| race / runtime | 2 | 1142 | 0 | 38.671s | `0df00b465a075328d61a91b5a62dab8d8861f981f5591ac5c6dac26924b9b95c` |
| race / consumers | 1 | 6 | 0 | 141.850s | `fc918d29c0a8ad585f3cef3b50e8713b140c5ed2494b80856484fee03aefe14a` |
| race / native-policy | 2 | 6 | 0 | 46.599s | `992ba63cfc8c99513dfbf7e8af631ba1ec0d292f3fde6c7967d89c8228326bc5` |
| race / helpdesk | 1 | 488 | 0 | 255.569s | `e2e1d3df858dd4ed7254922cb6d0de958ed418787cb0a418a75f92db8b2d26df` |
| cgo0 / runtime | 2 | 1142 | 0 | 3.191s | `abaa93b6d53917d495a34045abb07904f385be7987549b99d3c1b7c828a64c0d` |
| cgo0 / consumers | 1 | 6 | 0 | 14.322s | `1719be6079c2464ff0804a9d80322c42c95a3c234164859b47788550f1acc8fb` |
| cgo0 / native-policy | 2 | 6 | 0 | 6.185s | `980afff79a3fecb16c088139ceda290c378db74c0fab31bbe0a2705617ebab23` |
| cgo0 / helpdesk | 1 | 488 | 0 | 43.703s | `bcad4a63e43dc3a47091b5c0e85e0da381a4fb89206a3e86627e48be643eef3c` |

Runtime은 ORM/codegen 전체와 새 필수 59 paths다. 생성 소비자는 custom single/filtered prefetch, 행 잠금,
GetOrCreate, 고정 단건 기준 및 새 UpdateOrCreate의 6 parents다. 새 소비자의 양 DB 9 cases와 4종 borrowed
owner의 commit/rollback, 실제 기존 행·동시 생성 경쟁 2 cases를 필수로 확인했다. PostgreSQL에서는
`pg_blocking_pids`의 실제 대기와 unique rollback 후 새 잠금 조회를, SQLite에서는 native busy와 무재시도를
확인했다. Native policy의 6 paths는 writable owner·savepoint·만료와 SQLite의 명시적 잠금 거부를 포함한다.
기존 Helpdesk 전체 488 paths와 필수 312 paths를 함께 실행했다. 새 ServiceReport upsert 업무 흐름과
정식 기준 fixture의 Go 대조는 이 checkpoint에 포함하지 않는다.

실행 전후 source 동일, schema/다른 session `0|0`, 별도 DB와 소유 container 삭제를 확인했다.
Receipt `godj-update-or-create-api-ik1vzjvv/receipt.json`, SHA-256 `affc5070e18d79179b5fa292b2535eac28351abc9d35b1d6cd7eb665181a59f0`.
이 영향 결과는 Hosted/full-platform 성공이 아니다.

첫 후보 `b3d83ab1c3066e6fe9d99e36fc7e0f6ab6a2c74348d37519832b86a84c13cbb3`는 runtime 1142 paths 통과 뒤
새 graph 실패 주입이 실제 역방향 SELECT의 table을 대상으로 하지 않아 생성 소비자가 실패했다(6 run / 5 pass).
실제 RankedLink table로 주입을 옮기고 cleanup에서 hook을 반드시 해제하도록 고쳤다. 실패 receipt
`godj-update-or-create-api-4qlc5s53/receipt.json` SHA-256
`b31b1ff86dd160a0399e06e8ff35e6664ce15a76ee3e0b36e62b136c05aa9bc8`, 소비자 log
`1fcd5f56b7c19e12f18dc984e5f8d0a6afaeee887a838d67dd6df5a008aca914`다. 나머지 section은 미실행이다.

둘째 후보 `e507a5fe357a4553ed78db2b8afbb3bf862df523236ebd164539e38132904cd0`는 normal runtime 1142,
소비자 6, native policy 6 paths 통과 후 빈 patch 처리 변경에 따른 Helpdesk 3개 파일의 사용하지 않는 import로
build 실패했다(0 tests). Import를 제거하고 compile을 확인한 뒤 위 최종 source로 세 mode를 모두 실행했다.
실패 receipt `godj-update-or-create-api-th8s4pqa/receipt.json` SHA-256
`72eab32b9e94a2f18f9f6039578dd7c47492b08282e851b82134e4ba821c9446`, Helpdesk log
`656cd162b74db12a9ac207fa4305c4d7d4ec586d2e2b66eac4482876a4b633d4`다. Race/CGO=0은 미실행이며
두 실패 실행의 container 제거도 확인했다.

### 고정 Django의 잠금 대상과 실제 FK 경쟁 관찰

2026-10-01, Go 구현이나 기존 expected output을 읽지 않는 별도 observer로 선택 대상 8개 경우를 SQLite/PostgreSQL에서
각각 두 독립 프로세스로 관찰했다. Python 3.14.3/Django 6.1, PostgreSQL 17.10 Debian/UTF8/libc/C/C와 psycopg 3.3.6을 사용했다.
PostgreSQL에서는 서로 다른 서버 연결의 NOWAIT와 실제 FK INSERT/commit으로 추가 8개 경쟁 경우도 관찰했다.
이는 native 기준 조사이며 Go 제품의 구현·PASS를 뜻하지 않는다.

`OF`의 root/선택된 관계/두 단계 관계는 실제 해당 모델만 잠갔다. 조회 조건의 JOIN만 존재하는 관계와 알 수 없는 대상,
관련 값만 projection한 OF 관계는 PostgreSQL에서 SELECT 전에 FieldError였다. SQLite는 잠금 구문 없이 보통 조회를 수행했다.
특히 root 열을 제외한 values의 `of=self`는 PostgreSQL에서 OF 없는 FOR UPDATE가 되었고 실제 root와 관계 행을 함께 잠갔다.
root 열을 포함하면 OF root만 남고 관계 행은 다른 연결에서 잠글 수 있었다. 이 관찰 결과와 Go의 명시적 대상 보존 의미는
[ADR-0087](../adr/0087-row-locking-and-update-or-create.md)에서 구분한다.

FOR UPDATE로 잠근 부모를 참조하는 별도 연결의 FK INSERT는 commit에서 lock timeout/SQLSTATE 55P03으로 실패하고
행이 남지 않았다. FOR NO KEY UPDATE에서는 같은 참조 INSERT가 commit됐고 실제 행을 확인했다. 관찰 뒤 추가 행을 지웠다.
최종 seed 행은 두 개뿐이며 schema/session 0/0, 별도 DB 삭제와 소유한 container 종료·제거, observer byte 보존을 확인했다.

Observer SHA-256은 `11649693d58ef36a1177fbf8a4b2def804ed8f58db58fc5afad2c666ab46789a`다.
SQLite의 두 출력은 각각 1,769 bytes / SHA-256 `561af9e919fccd986318fded719202155e6099d5d7b5a4c86eddac7d4a4d5da1`,
PostgreSQL의 두 출력은 각각 4,411 bytes / SHA-256 `2c401dbffc1aa8a6f5142faae25b350a2a84aa5f7213637247579099c24c1c1f`로 byte 일치했다.
기준 자료는 `godj-lock-target-exploration-sql7_g7h/receipt.json`과 backend별 raw observation에 보존했다.
정식 기준 fixture·Go 구현·환경별 검증과 업무 흐름은 아직 남아 있다.

### 잠금 갱신 뒤 중첩 eager graph 보존 checkpoint

2026-10-02 KST, parent `f04bb77d`, 비Markdown 2962 files / inventory SHA-256
`86adf6448f591b8592b696b22ca8c0b4093932baedabe7f40c28c244ca7f36d6`에서 중첩 graph 보존을 추가 검증했다.
아래 ORM/생성 checkpoint와 같은 Go·양 DB profile, 공유 cache와 독립 module의 trimpath/race 전파를 사용했다.

| Mode | ORM/codegen run/pass | 생성 소비자 parent run/pass | skip | runtime / consumer 시간 |
|---|---:|---:|---:|---|
| normal | 1090 | 4 | 0 | 2.299s / 11.783s |
| race | 1090 | 4 | 0 | 34.790s / 90.691s |
| cgo0 | 1090 | 4 | 0 | 2.317s / 9.406s |

행 잠금 소비자의 backend별 필수 case는 7개다. 기존 6개에 `locked_refresh_preserves_current_descendants`를
추가했다. 실제 다른 PostgreSQL 연결에서 최초 eager SELECT와 잠금 SELECT 사이에 FK를 갱신했고, 반환된
관계와 그 하위 Badge는 새 FK를 따랐다. 앞서 반환한 graph는 기존 값을 유지하며 접근 때 추가 SQL이 없었다.
parent 잠금 SELECT에는 원래 없던 하위 JOIN을 추가하지 않았고, 별도 관계 SELECT에는 잠금이 없었다.
다른 연결의 하위 Badge NOWAIT도 성공했다. 하위 조회의 주입 실패 뒤 전체 조회를 재시도하고 성공한 cache만
게시하는 것도 확인했다. 기존 custom single/filtered/GetOrCreate 소비자와 ORM 전체를 함께 실행했다.

실행 전후 source 동일, schema/다른 session `0|0`, DB 및 container 삭제를 확인했다.
Receipt `godj-row-lock-graph-7emlom09/receipt.json`, SHA-256
`d826da41e8138b51081473b08ea0600cbc7f95c4cb7fa7546312d35e799586d4`.

| Mode / 범위 | Log SHA-256 |
|---|---|
| normal / runtime | `dc55342f450c2a780703168bb8c2252426f4558b5f517226d5a4f9a07afe0267` |
| normal / consumers | `9dcf4a131d3d9629bf25d402cda1642ae9262f64c2d4cc41f2bdf394052d6549` |
| race / runtime | `810e2169f2d502dd237f3511355c05da63c76e83003a0a2e8eb0309dafb6ab6d` |
| race / consumers | `b45dc7c3071a5a08a2fe0d03a695018df606bc7bebe3bc5acdaf82d44842d42f` |
| cgo0 / runtime | `5a01abae40318ce9344c3b819a554a96f2f52fec3c80918d16ced8bdd91be174` |
| cgo0 / consumers | `632ed974e44376e452a55f266317e53c1c1630a87e3ec10cc927a855ed33b0f9` |

새 query/ORM/SQLite 실행 sentinel 16개와 PostgreSQL/생성 소비자 sentinel 30개를 각 CI owner 명세에 추가했다.
`scripts/ci/test_packages.py`의 13 tests가 통과했다. 이 source의 Hosted 실행은 아직 없으며 앞선 backend 기반
checkpoint의 native 실행을 이 source의 재실행으로 표시하지 않는다.

앞선 graph checkpoint의 source `5e7364c983df1877a752444466a7f025da5059a53ad9bcffc14bf0ea6ab96f74`에서는
normal runtime 1090개가 성공했으나 새 소비자가 멈췄다. 첫 주입 실패가 발생한 부모 transaction을 계속 사용한
상태에서 다른 연결의 UPDATE를 기다렸기 때문이다. `pg_stat_activity`와 `pg_blocking_pids`에서 기존 transaction의
행 잠금에 UPDATE가 대기함을 확인하고 소유한 child process를 종료했다. 해당 소비자는 실패, 다른 3개 parent는
성공했으며 race/CGO=0은 미실행이다. 실패 receipt `godj-row-lock-graph-w0c0iogi/receipt.json`의 `pass=false`,
SHA-256 `e2d567d24c4aa8bb57031826be9b2db1bb7183ffffff64ed2a7276aacaad7c58`, consumer log
`28e4b67ea9368caa156e82c8582f21905146d4c0b31b4234092aa883dcf27e89`와 container 정리를 보존했다.
정상 잠금 수명을 변경하지 않고 fixture에서 외부 FK 갱신을 먼저 관찰한 뒤 조회 실패/재시도를 확인하도록 고쳤다.

### 행 잠금 ORM와 독립 생성 소비자의 영향 checkpoint

비Markdown 2962 files / inventory SHA-256 `c644a74aca0ba099be279019c505f4a7ed69a5577fb04a17e1bf647118955440`에서
typed/dynamic lock target, 일반/eager/prefetch 파생과 명시적 target 잠금을 연결하고 normal/race/CGO=0을 확인했다.
Parent는 `8f8831ac`이며 이후 `f04bb77d`의 Markdown 완료 기록을 합친 뒤에도 비Markdown source가 같았다.

Go 1.26.5/darwin/arm64·공유 cache·offline/readonly·`-count=1`, 독립 생성 module의 `-trimpath` 및 부모와 같은
race/CGO mode를 사용했다. 새 전용 PostgreSQL 17.10/UTF8/libc C/C와 실제 SQLite에서 생성 소비자를 실행했다.

| Mode | ORM/codegen run/pass | 생성 소비자 parent run/pass | skip | runtime / consumer 시간 |
|---|---:|---:|---:|---|
| normal | 1090 | 4 | 0 | 2.726s / 13.435s |
| race | 1090 | 4 | 0 | 43.763s / 100.989s |
| cgo0 | 1090 | 4 | 0 | 2.668s / 9.553s |

새 행 잠금 소비자는 양 DB 각각 cache/Count/거부·typed/dynamic 대상·terminal/scope 종료·기본 prefetch와
custom many/reverse 잠금·eager cache를 통과한 실제 잠금 query·생성 facade의 실제 대상별 NOWAIT 경쟁을 필수로 요구했다.
다른 모델의 잠금 대상 전달은 독립 module의 실제 compile 실패로 확인했다. 기존 custom single prefetch·filtered
composition·GetOrCreate 소비자도 함께 실행했다. 자식 내부 실행 수는 위 parent 수와 합산하지 않는다.
ORM은 일반 query cache의 비변경·잠금 query의 새 평가·옵션 오류/취소 우선순위·typed/dynamic AST 동일성·
eager 값/부재 cache의 재평가와 null FK의 capability 검증을 포함했다. 이 checkpoint는 UpdateOrCreate나
Helpdesk ServiceReport 흐름·GDJ-0108 Hosted 전체 완료의 증거가 아니다.

실행 전후 source가 같았고 남은 schema/다른 session `0|0`, 전용 DB/container 삭제를 확인했다.
Receipt `godj-row-lock-api-l8g6i54e/receipt.json`, SHA-256 `7682c5d895db75a5d03a9bc5bcbb4d39a7c814b98a3702b1651f86dc3583f6bd`.

| Mode / 범위 | Log SHA-256 |
|---|---|
| normal / runtime | `72b3b26b82eef5b9eaf45db769c7e88518a526771979c80c940ada649926fc08` |
| normal / consumers | `ab8e4cbcfe844e70daa0a75844169bc5b33b897324301d6855ec7cad13c0a644` |
| race / runtime | `7c6f34cb26b36dee13d3d1a418c27ffeb61c5d0117603243f77dd2a3fe6e1a7f` |
| race / consumers | `3fff380d369cb542960c1a616cb8f47728982955b9550d36ae2ec5f7160d5cb0` |
| cgo0 / runtime | `3b23c567ecc9f39268cb1fe1185d16421a4b874a4efff0cc7dd3487ab2305a7d` |
| cgo0 / consumers | `8d8d15e03007b8e9c5c4192806e0325faf6822bb2aade93855e28ec1743bff46` |

같은 비Markdown source의 `make generate-check`는 Unicode 생성/2 Python tests·7개 project·checked-in relation
fixture를 통과했다. Query/queryplan·양 DB·ORM·codegen/소비자의 `go vet`, 181개 Markdown 문서 링크와 diff도 확인했다.
생성 facade 7개 project와 query/select-related/facade golden 3개는 실제 생성기로 갱신했다.

### 행 잠금 ORM/생성 표면의 첫 checkpoint와 보정

첫 source의 비Markdown 2962 files / inventory SHA-256 `3f06c4f98254fe71f612d0b86594349b09a941dc65ecf630291a384f0d5c0177`에서
normal runtime은 1,090 run / 1,086 pass / 0 skip, 19.336s로 실패했다. ORM은 전부 성공했지만 codegen의
query·select-related·facade golden 3개를 갱신하지 않았고, 부모 실행까지 `-trimpath`를 적용하여 기존 Article
검사의 `runtime.Caller` 기반 workspace 경로를 바꿨다. 생성 소비자와 나머지 mode는 실행하지 않았다.
Golden은 실제 생성기로 갱신하고 부모 Go test는 repository의 기존 경로 조건을 사용한다. 독립 생성 module의
`-trimpath`·공유 cache는 유지한다. 실패 log SHA-256은 `96878cc8ec4de7a6d12a3048711d6bd81920cbe21fc2beb3571f54d249e48dd6`이며,
receipt `godj-row-lock-api-px7_tdus/receipt.json`의 `pass=false`와 전용 container 정리를 보존한다.

보정 source의 normal runtime은 1,090 run/pass / 0 skip으로 성공했다. 이어진 생성 소비자 checkpoint는 기존
custom single·filtered composition·단건 생성 세 parent가 성공하고 새 행 잠금 parent가 실패했다. 새 fixture에서
borrowed session을 root 생성자 `project.Using`에 전달했기 때문이다. 현행 `UsingSession` 경계를 그대로 사용하도록
fixture를 수정하며 이 실패를 제품의 capability 완화로 처리하지 않는다. source inventory SHA-256은
`24c850ecf27921d3b6da086ec4b677fd66d447453d28bbf65d4e89a894d04cc0`, 실패 log는 `a3be3d9e4eeefb58ff18346ef66599bf2d0d8a8dded84134eff4ff25a80d875b`이다.
Receipt `godj-row-lock-api-ak3xkp4m/receipt.json`은 `pass=false`; race/CGO=0은 미실행이며 전용 container는 정리했다.

### 행 잠금 AST와 native backend 기반 checkpoint

2026-10-01, parent `8f8831ac8231a9c32049b6649e49f8638edd5117`, 비Markdown 2958 files, inventory SHA-256
`272c2b1258ac30f90ccc2cddaef4a21a640b534d8e81f68a8eb7367d9b289716`에서 Query AST·compiler·native session/cursor 경계를 검증했다.
이 checkpoint는 typed/dynamic ORM·생성 facade·update-or-create 구현 완료를 뜻하지 않는다.

Go 1.26.5/darwin/arm64, 공유 cache·offline/readonly·`-trimpath`·`-count=1`을 사용했다.
PostgreSQL 17.10 Debian의 UTF8/libc C/C profile은 새 전용 local container에서 확인했다.

| Mode | AST/queryplan run/pass | backend run/pass | skip | AST / backend 시간 |
|---|---:|---:|---:|---|
| normal | 248 | 312 | 0 | 2.258s / 26.207s |
| race | 248 | 312 | 0 | 4.32s / 39.577s |
| cgo0 | 248 | 312 | 0 | 1.391s / 4.744s |

각 mode의 네 package는 누락·중복·실패·skip·잘린 출력을 거부했고 새 및 scope 회귀 25개 필수 root를 확인했다.
명시 대상/원본 소유권·projection 보존·Count 잠금 제거·잘못된 route·nullable/distinct/window 제한, root/snapshot의
SQL 이전 거부를 검증했다. 실제 두 PostgreSQL 연결에서 OF 대상별 NOWAIT, SKIP LOCKED, `pg_blocking_pids`로
확인한 대기와 commit 뒤 진행, 취소 후 연결 재사용, 네 transaction owner의 savepoint/cursor 수명, rollback 뒤 잠금
해제, pinned writable scope와 read-only 경계, NO KEY UPDATE에서의 deferred FK 참조 commit을 대조했다.
SQLite는 root·transaction·savepoint·snapshot과 빈 source의 모든 명시 옵션을 unsupported로 거부했다.
쿼리·일반 savepoint·cursor·실패 소유권 회귀도 같은 실행에 포함했다.

실행 전후 source가 같았으며 남은 schema/다른 DB session `0|0`, 전용 DB와 container 삭제를 확인했다.
Receipt `godj-row-lock-foundation-7afeqktp/receipt.json`, SHA-256 `c64c843960df713f0c12cf177d6fd0ab6fa679bdc0740fde3b2fa6f09a7660d1`.
각 원본 JSON log의 SHA-256은 다음과 같다.

| Mode / 범위 | Log SHA-256 |
|---|---|
| normal / ast | `3fe67102475e8a6dd5849294f5e32ad0000dcba089a98098cd72328d5edfd46c` |
| normal / backends | `5977a9153f5b7dd0f029d4a5fbc199fd6529db70e4f5c1f4a539c06a08c3e4b7` |
| race / ast | `ddd14ebca8c540f45435d5d2af8661dbf5cb4afb3e5d7675197abac606981690` |
| race / backends | `10be15a0d9d563a7bee4c8681854eeb3ca4ba7993d20a283f24640cf5ac66e72` |
| cgo0 / ast | `dbfcc272d26f00e42122429ad2a1833ba0c30e6257d0aac8560c9a43bca0529a` |
| cgo0 / backends | `2746fe0b245f80d12d14afc9d258fc8a75cf54f9dadb710d591a99737ce4b388` |

## GDJ-0107 — 단건 조회와 savepoint 기반 조회 후 생성

### 최종 Hosted 전체 통합 완료

Source `8f8831ac8231a9c32049b6649e49f8638edd5117`의 [Hosted full 36866445270](https://github.com/progresshans/godj/actions/runs/36866445270), attempt 1이
2026-10-01 14:44:27 UTC에 completed/success로 끝났다. 65개 job / 8개 필수 owner와 최종 집계가 모두 성공했다.
모든 job의 실제 checkout·고정 workflow의 필수 단계를 대조했으며 집계 job `110424616466`의 출력은
`full_platform_verified=true`와 정확한 8개 owner를 보고했다. Job audit SHA-256은
`0e3a8b60425e1a1c405f3a4a9d6d7f242243c18240820f0a6653ed00a48c558f`이다. 명시적으로 다른 owner가 담당하는 조건부 단계 외의 필수 skip은 없다.

관계 검증의 12개 platform/mode 모두 실제 발견한 소비자 root 78개의 누락·중복이 없었다. Discovery SHA-256은
`0776a7c44e930818e8c3a32a8713b8a0614c0d349ab94d337c24b6a6ca4699e6`이며 각 job의 실행 owner와 root 성공을 대조했다.
macOS Intel/race의 세 소비자 shard는 26개씩 담당하고 일반 관계 package는 별도 runtime job이 완료했다.
이전 누적 timeout 실행의 실패는 보존하며 해당 실행의 부분 성공을 이번 전체 성공으로 대체하지 않았다.

같은 run의 새 system-state/operator capture 두 개를 producer/artifact/provenance 및 Git 원본의 source 지문에 결합했다.
Capture consumer job `110402001858`에서 두 artifact와 producer ID, source checkout, conformance·32-bit Linux
compile/관계 실행·고정 oracle checksum·reference 비변경을 포함한 필수 10개 단계가 모두 성공했다.
소비 log SHA-256은 `532209a680f94fb4dffed07d046a3ae89df1896f65cb95f909a7224512f37351`이다. 두 capture 및 PostgreSQL core 세 mode의 실제 S3 수명 증거는 아래와 같다.

최종 receipt `godj-single-object-hosted-full-shards-9su1dxnz/receipt.json`, SHA-256
`ec134cdbaa618c43397105341acddc649c05bee1e72f896f661fbba14f8b33dd`를 보존했다. 검증 중 비Markdown source는
2,952 files / inventory SHA-256 `5356135720528ea177fe05a345446fbe03209b95c955924cf5315681f5be8a77`로 같았다.
이 source 뒤의 완료 기록 변경은 Markdown뿐이다. 별도 GDJ-0108 작업 사본의 행 잠금 변경은 포함하지 않으며
이 Hosted 성공을 그 새 source의 검증으로 사용하지 않는다. 로컬 전체는 중복 실행하지 않았다.

### Hosted 전체 통합 — 실행 목록과 소비자 계약 보완

제품 source `98aa179fc4077bdefdec9deaedbb9a7865c6e3b8`를 `feature/get-or-create`와 기존 draft PR #1의
`codex/revision-fenced-migration-lifecycle`에 동일하게 push했다. [PR feedback 36826668420](https://github.com/progresshans/godj/actions/runs/36826668420)은 성공했다.
실제 checkout `5c4b2bbe6baedb60153b263f123a59e4961741ba`의 parent에 이 source가 포함되며 tree도 같다.
Job `110253764000`의 필수 단계와 log SHA-256 `349d47d96eb83e42839d0d55ce9384b36a351e692833ab6a7884c24682219960`을 확인했다.

2026-10-01 06:48:47 UTC의 [Hosted full 36826788120](https://github.com/progresshans/godj/actions/runs/36826788120), attempt 1은
`workflow_dispatch`·`suite=full`로 시작했으나 필수 실행 목록 누락을 발견해 취소했다. PostgreSQL core는 목록에서 root test
선택식을 만들므로 새 savepoint·생성 소비자가 빠져 있으면 실제 PostgreSQL 경로가 실행되지 않는다. 이 실행은 completed/cancelled이며
전체 성공으로 사용하지 않는다. 취소된 owner 때문에 최종 집계도 성공하지 않았다.

Relation 목록에 107개, PostgreSQL core 목록에 168개의 새 필수 parent/child를 추가했다. 기존 목록은 그대로 보존했다.
새 native savepoint·Get/GetOrCreate·생성 소비자와 양 DB Helpdesk 확보 경로를 기존 영향 로그의 실제 이름과 대조했다.
45개 CI 도구 검사에서 목록의 중복/공백·실행 owner·shell 전달·child에서 parent 선택을 확인했다.
모든 추가 항목을 기존 세 mode 로그의 실제 run/pass와 대조했다. Roster audit는
`godj-single-object-roster-audit.json`에 보존했다. 보완 commit은 `d9f2324e7c53670b46b870c4d41e145051952e95`이며,
비Markdown 2,950 files의 inventory SHA-256은 `fe679c6aa06f6cf635543019728189dba3545eb7a638d0b20ce7fd05ca52d725`다.
최종 Helpdesk checkpoint와 다른 비Markdown 파일은 두 필수 목록뿐이며 제품/테스트 내용은 같다.

[보완 source의 PR feedback 36828265833](https://github.com/progresshans/godj/actions/runs/36828265833)은 성공했다.
Job `110258751170`의 checkout `12b10f8ae4f3f37926dab4d14f419e12a4fbf530`은 위 source를 parent로 가지며,
tree `19167c6de4cd9bb796dbe16b24d1f7909286782a`도 일치한다. 필수 단계와 log SHA-256
`425787d0f8b72515ef864d465488af4cfb338cf804907bbdd8dfdffabdd2765c`을 확인했다.

2026-10-01 07:10:47 UTC에 같은 source의 [Hosted full 36828841116](https://github.com/progresshans/godj/actions/runs/36828841116),
attempt 1을 `workflow_dispatch`·`suite=full`로 시작했다. 아래 생성 소비자 대조 실패 후 취소했으며, 전체 성공으로 사용하지 않는다.

중간 확인에서는 완료된 21개 job의 실제 checkout과 해당 owner의 필수 단계가 성공했음을 대조했다.
이번 실행의 operator capture는 producer job `110260590717`, artifact `11146014567`이며, archive SHA-256
`939e6f4cb5a2f29a934917e262f7e622bea09297c7d88a279df171f2952b3497`, payload SHA-256
`6241a8381fde94cdbb91a4480f5fe1525158677cfe3d45ecfc9d7958f5d9bfe4`를 확인했다.
고정 source의 Git objects로 재계산한 `godj.project-operator.combined-external-global-source/v1` 지문은
782 files / 7,079,619 bytes / SHA-256 `1a7a67f7b0cecbf3598ee7902efb2f935a40eb9623db6c57865ef549b4536fbe`로
capture와 같았다. 새 savepoint·ORM·Admin 파일이 이 source 범위에 포함된다.
이 operator 증거는 해당 실행의 부분 검증이며 이후 source의 전체 성공으로 전이하지 않는다.

Linux amd64/arm64의 Relation normal job `110260591202`/`110260591153`에서
`TestGeneratedScalarMembershipConsumer`의 synthetic aggregate 수명 대조가 실패했다. 두 job의 실제 checkout이
`d9f2324e`임을 확인했다. Log SHA-256은 각각 `be03dd594776abbaa85b063b8274250242f1abfd49a0849d737edf08577b6859`와
`c0a830b77557b1ac2f5faf92736816cbf60f07770448685241ee60c81d00dbe5`다. 종료 후 cursor는 닫혀 있고
현행 [ADR-0086](../adr/0086-single-object-creation-and-savepoint-ownership.md)의 `backend_error/invalid_plan`을 반환하지만,
기존 소비자는 이전의 SQLite `sql.ErrTxDone`을 요구했다. 실패한 실행을 취소했고 최종 상태는 completed/cancelled다.
29개 job success, 30개 cancelled, 두 Relation과 최종 집계 3개 failure이며 이를 전체 PASS로 기록하지 않는다.

제품 코드는 바꾸지 않고 기존 소비자의 오류 대조를 공통 scope 계약에 맞췄다. 종료 후 `Next=false`와 `Err`의 정확한
category/code를 확인하고, `Scan`과 session의 빈 query도 같은 오류로 거부되는지 검사한다. Atomic/coordinated/relation
세 child를 부모의 필수 성공 목록에 추가했다. 기존 취소 전파·empty aggregate·SQL 없음 검사는 유지한다.

수정한 source는 parent `d9f2324e`, 비Markdown 2,950 files, inventory SHA-256
`958d2c6358cee632ee2b9c81fce0614327cf62342006b79664cfb50013074a4f`다. 변경된 비Markdown 파일은
`codegen/consumertest/membership_test.go`와 그 `testdata/membership/consumer_test.go` 두 테스트뿐이다.
Go 1.26.5/darwin/arm64, 공유 cache·offline/readonly·독립 생성 module의 `-trimpath`·`-count=1`로 해당 부모를
normal 3.494s / race 7.155s / CGO=0 1.642s에 실행했다. 각 mode 부모 1 run/pass와 package 성공,
독립 module의 기존 4 root 및 세 수명 child의 정확히 한 번 성공을 요구했고 failure/skip/truncated output은 없었다.
실행 전후 source도 같다. Receipt는 `godj-membership-lifetime-checkpoint-nj141ma7/receipt.json`, SHA-256
`572c9c8162ca678040ee25b0af4e3bfbc09a3bf4e3774eedf3d59d4f34393ad6`이다.
보완 commit `39fb5ed831eacd75219fa909aad868e7b40ce3d0`을 두 원격 작업 branch와 draft PR #1에 반영했다.
2026-10-01 07:29:23 UTC에 같은 source의 [Hosted full 36830611179](https://github.com/progresshans/godj/actions/runs/36830611179),
attempt 1을 `workflow_dispatch`·`suite=full`로 시작했다. 같은 source의
[PR feedback 36830514555](https://github.com/progresshans/godj/actions/runs/36830514555)은 성공했다.
Job `110265806245`의 필수 단계와 실제 checkout `7755e69104660ff4c96dde858b193aaf635c4ef7`을 확인했다.
Checkout의 parent에 source가 포함되며 tree `33d306ee3a695d3adc7cc1d2024589b9a32f9af7`도 같다.
Log SHA-256은 `710b1970dacdbaf778772946d33755516f00abcf6b6066c6093549af27edd1f4`다.
진행 중간에 완료된 47개 job의 실제 checkout과 필수 단계를 확인했다. 앞선 실행에서 실패한 Linux amd64/arm64 Relation normal도
같은 source에서 성공했다. 이번 run의 operator producer `110266156098`가 새 artifact `11147450007`을 게시했으며,
archive SHA-256 `5e8f61b22699294ae0ed69d3e15936c0214d298eff7a7f4dcbf593ed6bcfbbf2`와 같은 run/source provenance를 검증했다.
Payload SHA-256은 `6241a8381fde94cdbb91a4480f5fe1525158677cfe3d45ecfc9d7958f5d9bfe4`다. 고정 source `39fb5ed8`의
Git objects로 다시 계산한 operator source 범위는 782 files / 7,079,619 bytes,
SHA-256 `1a7a67f7b0cecbf3598ee7902efb2f935a40eb9623db6c57865ef549b4536fbe`로 capture와 일치했다.
앞선 payload와 byte가 같아도 이번 run의 새 producer/artifact/provenance를 별도로 요구했다.
System-state producer `110266156172`의 새 artifact `11148361111`도 같은 run/source provenance와 함께 검증했다.
Archive SHA-256은 `42a2e5d9c782cbb99fe6df14edb213099fcb152f437333e885a36553abe40f45`, payload SHA-256은
`9a8ab65377a024030b4ba7b637f4b2c0fec5c3e826a4d4f28cd98c21c9fcc592`다. Git 원본으로 재계산한
`godj.system-state.postgresql-two-process-source/v1`은 704 files / 7,220,628 bytes,
SHA-256 `53a5368ab1e4bfb091dd1f1467684ec3c651d5c8a341ba11df6989d210881ed0`로 일치했다. 두 source 범위에
새 savepoint·ORM·Admin 파일이 포함되며 두 capture의 source 결합을 모두 확인했다.

PostgreSQL core normal/race/CGO=0은 각각 15 packages / 4,630 run/pass / 0 skip, 필수 parent/child 2,130개와
S3 경로 23개를 확인했다. Normal S3 artifact `11148391016`의 archive SHA-256은
`4eef506bfa90dbbf891c0c5c421146f49daa95cdc6a36c7a3a5d3ed4b49da90d`, CGO=0 artifact `11147317532`는
`e0b4ff831fccfff738a96f3301d6543607c189277fd1c74840f9bafb12407feb`다. Race producer `110266156106`의 artifact
`11147489065`도 확인했으며 archive SHA-256은 `cb5a96d3cef82e1ee12b8cecb6b5ce953a5677a22c90fbc81dbbbf22a5772f09`다.
세 service build는 고정 MinIO module/commit, 의존성·buildinfo와 binary SHA-256 `c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`를 검증했다.
실제 ready·test child exit 0·child 이후 service 생존·service exit 0·양 process 회수·정상 정리도 확인했다.
Capture consumer job `110274982625`는 같은 source에서 두 새 producer/artifact를 받아 검증했고 conformance·32-bit 경계·
reference 비변경을 포함한 필수 10개 단계를 완료했다. Log SHA-256은
`d05f4f8f8c321c78c1fac9837dce9b5bfeaadf10c658a2f8e3343a8e145a0dfe`다.

이 실행은 2026-10-01 09:15:55 UTC에 completed/failure로 끝났다. 62 jobs 중 60 success, 2 failure다.
실제 실패 owner는 `Relation product (macos-15-intel, race)` job `110266156645`이며 다른 실패는 최종 집계다.
`codegen/consumertest`가 누적 70분(4200.116s)을 넘었다. Timeout 당시 상위 테스트는 시작 후 5초였고,
그 전 상위 PASS의 합산 시간은 4194.73s였다. 개별 assertion·build·race 실패는 기록되지 않았으나
단건 조회/생성을 포함한 뒤쪽 테스트가 실행되지 않아 해당 좌표와 전체 통합은 미완료다.
실패 log SHA-256은 `b90f378f01f740f06a8b9f94a843e06e2f4c3bdf71eeeb3b8023cbd5221d868b`다.
실제 Go discovery의 상위 테스트 78개를 보존하고 이 좌표의 소비자를 세 job으로 분할한다.
일반 관계 검증은 별도 runtime job에서 실행하여 두 비용이 한 job에 누적되지 않도록 보완한다.
새 source의 전체 통합과 기본 작업 사본 전달은 남아 있다.

### CI 실행 분할과 전체 실행 보존

2026-10-01, macOS Intel/race의 누적 package timeout을 해결하기 위해 실제 Go binary의 discovery로 생성 소비자를
자동 분배하도록 CI를 보완했다. 상위 Test/Example/Fuzz와 모든 필수 child를 보존하며 Benchmark는 일반 `go test`의
실행 대상이 아니다. 필수 root의 discovery 누락, 잘못된 shard, 상위 테스트의 미실행·중복·실패·skip, 미완료 package와
선언한 실행 owner 밖의 package를 거부한다. 두 capture의 source 지문 범위에도 새 CI helper를 포함했다.

초기 분할의 실제 로컬 실행은 parent `39fb5ed8`, 비Markdown 2,952 files, inventory SHA-256
`12ec16f7327d9a39c100bf6f8c3b51ab5137157b534f9d212d72a0cfbbfa6d01`에서 수행했다.
Go 1.26.5/darwin/arm64, CI 도구 Python 3.13.3, 공유 cache·offline/readonly module·`-count=1`·race와 자식 `-trimpath`를 사용했다.
Workflow의 실제 Run step을 세 독립 임시 디렉터리에서 실행했고 당시 첫 분할은 일반 관계 package도 이어서 실행했다.
2026-10-01 12:29:53–12:54:21 UTC에 세 step이 모두 exit 0으로 끝났으며 실행 전후 소스가 같았다.

| 초기 실행 | package 수 | run/pass | test skip | step 시간 |
|---|---:|---:|---:|---:|
| 소비자 1 + 일반 관계 | 50 | 8,028 | 0 | 1467.099s |
| 소비자 2 | 1 | 55 | 0 | 388.820s |
| 소비자 3 | 1 | 40 | 0 | 613.535s |

각 분할은 상위 소비자 26개를 정확히 한 번 실행·성공했고 전체 78개의 누락·중복이 없다. 소비자 package의 세 별도
실행을 합친 고유 package는 50개, 전체 test event는 8,123 run/pass다. Receipt는
`godj-relation-shard-checkpoint-ur1hi2l3/receipt.json`, SHA-256
`b641ba02544aa0da97869e208351cc568a123ac34f2b3ebcc5e9f2ee18b9cadb`다.

일반 관계 검증의 시간도 따로 확인되어 최종 CI는 macOS Intel/race의 일반 관계를 runtime job에 배정하고 소비자 세 job과
독립적으로 실행한다. 다른 11개 좌표는 전체 package를 한 번 실행한다. 관계 matrix는 15 jobs, 전체는 65 jobs다.
기존 시간 제한을 유지하며 전체 필수 실행 목록은 파일에 남기고 로그에는 개수·상위 테스트·지문을 출력한다.

최종 비Markdown inventory SHA-256은 `5356135720528ea177fe05a345446fbe03209b95c955924cf5315681f5be8a77`이다.
위 실제 실행 이후 바뀐 비Markdown 파일은 workflow와 CI helper·그 테스트 세 개뿐이며 Go 소스는 같다.
완료된 raw Go event를 최종 owner로 나누어 현재 CLI로 재검사했다. Runtime은 49 packages / 7,979 run/pass /
필수 2,202개이며, 세 소비자는 각각 26개 root / 49·55·40 run/pass다. 모든 test skip은 0이고 각 package 집합도 정확히 일치했다.
이는 완료 로그의 배정 검증이며 새 Go 실행으로 세지 않는다. 재검사 receipt SHA-256은
`1226b9674b264583d7dc4dad8277e3c3603af4b81b384d255d963987a8f953b0`이다.

최종 source에서 CI 도구 52개 검사와 workflow·양 capture source 범위의 Go 3 packages / 1,180 run/pass / 0 skip을
다시 실행했다. Go event SHA-256은 `7cc21d7f15e1adb5fba5886fc3d3fca945376434cc58aa86126d7eef81355cf0`이다.
세 mode와 portable/runner owner 조합의 24개 계획에서 모든 필수 항목·상위 소비자·일반 package의 단일 배정을 확인했다.
문서 링크·format·diff·workflow YAML 구문도 확인했다. Commit `8f8831ac8231a9c32049b6649e49f8638edd5117`을 두 작업 branch와 draft PR #1에 반영했다.
2026-10-01 13:07:54 UTC에 `feature/get-or-create`에서 `workflow_dispatch`·`suite=full`로
[Hosted full 36866445270](https://github.com/progresshans/godj/actions/runs/36866445270), attempt 1을 시작했다. 이 고정 source의 65개 job·필수 owner·집계와
새 두 capture의 Git source 결합·실제 소비까지 위 최종 감사에서 확인하고 통합을 완료했다.

### 새 source의 Hosted capture와 S3 증거

Source `8f8831ac`의 [PR feedback 36866280277](https://github.com/progresshans/godj/actions/runs/36866280277)은 성공했다.
Job `110382468851`의 checkout `434e78e4b0baf9986cd84319c7fa6f4ba05a3261`은 이 source를 parent로 가지며,
tree `e001c5843548dc6866ad83791e7be72adee1db4a`도 일치한다. 필수 단계와 52개 CI 도구 검사를 확인했고
log SHA-256은 `f38a0984373216260a335dce16e6381a0e3f2be9d9938206d1d2049b1435be37`이다.

Full attempt 1의 65개 job은 위 최종 감사에서 실제 checkout과 해당 owner의 모든 필수 단계를 대조했다.
아래 두 capture는 이번 run의 새 producer·artifact·provenance를 별도로 확인했으며, 저장 payload와 고정 Git 원본으로
source 지문을 재계산했다. 앞선 run의 capture를 이번 실행 증거로 재사용하지 않았다.

| Capture | Producer job | Artifact | source files / bytes | Git source SHA-256 |
|---|---:|---:|---:|---|
| System-state | 110383043636 | 11166031199 | 705 / 7,230,063 | `8bf77fdbae1fd074f2e233da67861f7ddf366e62045c8289f7b5fc2ebfcb9e23` |
| Operator | 110383043703 | 11163578195 | 783 / 7,089,044 | `ea1c55220635a0df42c62960e04cb4dce89cbfd4949726f44a82699434843e7d` |

System-state archive / payload SHA-256은 각각 `1ec3102334baeab9d9fcb3c3f949e73820d3b7e2d64eb76fadb37e2cc304219d` /
`6c9340a50b95ccf010f2621f948990afa77e10451da338761614777b60136190`, operator는
`6653456e7501ce05bf32a53b6d6dc1ddc850736d1c2b2bd7f4ec46f4f604eb65` /
`df3d95cf964da8fa2ab0506ccd3872b69ea6b3ce81a20b0e7ec7ff1233917c73`이다.
두 source 지문의 scope는 각각 `godj.system-state.postgresql-two-process-source/v1`과
`godj.project-operator.combined-external-global-source/v1`이다.

PostgreSQL core normal/race/CGO=0 모두 15 packages / 4,630 run/pass / 0 skip, 필수 parent/child 2,130개와
S3 경로 23개를 확인했다. 세 service의 고정 MinIO source·의존성·buildinfo·binary SHA-256
`c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`, 실제 ready·child exit 0·child 이후
service 생존·service exit 0·양 process 회수·정상 정리를 검증했다.

| Mode | Producer | S3 artifact | Archive SHA-256 |
|---|---:|---:|---|
| normal | 110383043636 | 11165027331 | `f944caf8aa775cfa9edc2b71ac19944bf19e18cd97edcc19525417dc3d9ec403` |
| race | 110383043681 | 11167825443 | `6a89a932177dfc3afaa472d4134571faae7a3823f36f4e5f67003a38fdd3d027` |
| CGO=0 | 110383043589 | 11166150886 | `ef9591bec6929221f07df3186e4ba2b93796af4eead9db0dd6463aea9cdc7a85` |

위 최종 감사에서 macOS의 남은 실행·capture 소비·최종 집계까지 확인했다. 이 run의 최종 receipt는
`pass=true`, `complete=true`이며 앞선 source의 실패 receipt는 변경하지 않았다.

### Helpdesk Label 확보와 입력 표면의 영향 검증

2026-10-01, Label의 명시적 확보를 Admin 목록 Form과 API에 연결했다. 새 행과 add event를 같은 부모 transaction에
저장하고, 기존 행 재사용에는 event를 추가하지 않는다. 실제 unique 실패는 child savepoint의 rollback 뒤 한 번의
fresh 조회로 복구한다. 일반 create의 중복 거부는 유지한다. API와 감사 의존 Admin registry는 필수 audit callback을
명시적으로 받는다. [입력/출력 계약](../../examples/helpdesk/README.md)과 [ADR-0086](../adr/0086-single-object-creation-and-savepoint-ownership.md)을 따른다.

검증 source는 parent `521e43ecf3845a761a2e5e04b16b8550938a29da` 기준 비Markdown 2,950 files,
inventory SHA-256 `d9fd88504a02e2e9423236e146e4e5da2a68d6d3dfd5f1b472ec6aa8e348e341`이다.
Go 1.26.5/darwin/arm64, 실제 SQLite와 임시 PostgreSQL 17.10 Debian/UTF8/libc/C/C에서 실행했다.
공유 build cache·offline/readonly module·`-count=1`을 유지했으며 아래 네 package 전체를 각 mode로 실행했다.

| mode | Admin | Helpdesk 양 DB | OpenAPI | 독립 client 부모 | skip | checkpoint 시간 |
|---|---:|---:|---:|---:|---:|---:|
| normal | 361 | 488 | 138 | 10 | 0 | 40.116s |
| race | 361 | 488 | 138 | 10 | 0 | 244.173s |
| CGO=0 | 361 | 488 | 138 | 10 | 0 | 46.737s |

숫자는 중첩 test를 포함한 run/pass 일대일 대응이다. 각 mode는 총 997개이며, 자식 process 내부 수를 부모 수에 합산하지 않는다.
새 업무 흐름은 두 DB 각각 API/Admin의 29개 대조와 독립 Runtime의 동시 요청을 필수 경로로 요구했다.
생성·재사용·외부 Category의 같은 이름, 실제 native unique 오류/rollback/재조회, 현재 Category 제거와 Label scope 변경,
query/INSERT/reload/출력/audit 실패·취소, commit/rollback unknown, 부모 callback 누락/반복/동시 진입/늦은 호출/오류 은폐,
개별 권한·CSRF·중복/위조/길이/빈 입력과 일반 create의 중복 거부를 확인했다. Unknown outcome의 앱 대조는 fault adapter가
native owner의 실제 성공/rollback 뒤 오류를 반환하는 검사다. Native 제어문 실패 자체의 증거는 아래 savepoint checkpoint가 소유한다.

동시 HTTP 요청은 서로 다른 Runtime과 DB 연결을 사용했다. 선행 요청을 실제 audit INSERT 후 commit 전에 유지하고,
후행 요청의 DB coordination 진입을 관찰했다. 별도 연결에서 uncommitted Label이 보이지 않는지 확인한 뒤 해제했다.
두 결과의 ID, created=true/false, 최종 한 행과 한 audit가 같았다. 이 검사는 업무/인가 coordination 경계이며,
두 INSERT가 직접 경쟁하는 native lock 관찰은 아래 별도 ORM 기준 소비자의 증거와 구분한다.

고정 ogen으로 현재 Helpdesk 문서와 generated client를 갱신했다. 다른 다섯 문서와 dependency lock은 그대로다.
`HelpdeskLabelEnsureCreated`/`HelpdeskLabelEnsureOK`, 필수 label/created, 큰 int64, 누락/null/잘못된 bool과 고정 500의
단일 전송을 독립 module에서 검사했다. 실제 HTTP는 생성·반복·일반 중복·권한·CSRF를 호출하고, 부모가 DB의 Label과 최초
add event 하나를 별도로 조회한다. Generated drift와 필수 receipt·출력 잘림 거부도 세 mode에서 실행했다.
Helpdesk schema SHA-256은 `d7672cc8768584db545a74e2f0fad0d07982b2ab11d7bdb14230118f38ee556a`다.

Receipt는 `godj-label-ensure-checkpoint-0afy2v6i/receipt.json`, SHA-256
`e33517eb92713519740f6f955e4bb33fecfdaa8589ec90a64cf0d85d02665c60`이다. 같은 디렉터리의 `final-audit.json`
(`a34c93620d7d7a5e6d0945e262ac2c01a0435266c2cb8cf54f01c15d53a367f4`)이 세 log hash와 모든 시작/종료,
실행 전후 source 일치, 영향 vet·gofmt·diff를 확인한다. 임시 PostgreSQL container 제거와 부재도 확인했다.
첫 `z8h8ck7b` 실행은 다른 인증 인스턴스의 CSRF token을 재사용한 새 테스트와 특정 경로에 고정된 Form/notice 도우미가
실패했다. 각 Site의 실제 safe 응답에서 token을 얻고 기대 action/list 경로를 명시하도록 고쳤다. 제품의 CSRF/권한 검사는 완화하지 않았다.

같은 제품 source를 import하는 별도 임시 public consumer를 실제 Codex in-app browser에서 확인했다.
로그인·빈 목록의 command 링크·name/CSRF만 있는 Form·공백 정리 생성·같은 ID 재사용·HTML 문자 표시·일반 create 중복 오류·
빈 입력 재표시를 관찰했다. 종료 후 실제 SQLite에서 Label 하나와 최초 add event 하나를 읽고 서버/DB/탭을 정리했다.
Browser receipt는 `godj-label-ensure-browser-n5j406x9/receipt.json`, SHA-256
`e2f5bbb788e46e1bb1532e2f72f4b8f5f0c3452c871446452bb210103dc0c440`이다. 이는 한 브라우저의 기능/렌더링 확인이며
브라우저/platform 전체 검증이 아니다. 이 checkpoint는 위 네 package의 현재 영향 범위이며 최종 source의 Hosted 전체 통합은 남아 있다.

### 최종 Django 기준과 Go의 양 DB 대조

2026-10-01, [독립 observer](../../conformance/runners/django/single_object_reference.py)를 고정 Python 3.14.3/
Django 6.1, PostgreSQL에서는 psycopg 3.3.6으로 실행했다. SQLite와 PostgreSQL 17.10 Debian/UTF8/libc/C/C
각각 새 프로세스 두 번의 결과가 byte-identical이었다. Observer는 Go 결과나 예상 fixture를 읽지 않으며
queryset/transaction 모듈과 자신의 source hash를 출력에 포함한다.

- Observer SHA-256: `ff7785c3a378e27fb4df460ac75ee1b8396cffa1afadf161b3b0fe51915e6112`.
- SQLite fixture: `8fcb29699552f809761ee8e31cbe8a5ee2a2d22db47788afbd87f0cd30e3ec02`, 6,308 bytes.
- PostgreSQL fixture: `d3a0887b4dcbc15fd38b611bf65ab91322be3fd1164a74bc499fa8998cdd524f`, 6,312 bytes.
- Native receipt: `godj-single-object-reference-pzr1saou/receipt.json`, SHA-256
  `b769fe8dd147452125e14e829405dd4ee963a4934b7478c9008468d9bd54da31`.
  DB 잔여 table/다른 session `0|0`, DB 제거와 container 부재를 확인했다.

관찰은 Get 11개·생성/중첩 transaction 9개와 실제 unique 경쟁 하나다.
fixture를 읽는 별도 생성 Go module은 각각의 결과·cache·factory·오류 범주·transaction 효과와 최종 저장 행을 비교한다.
Python 문구/SQL 자체의 호환으로 확대하지 않으며, native 진단과 Go의 DB port 기록의 차이는 [SOURCES](../SOURCES.md)에 명시했다.
관계/eager/cache 소비자의 경쟁 검사를 이 기준 소비자로 옮겨 중복 실행을 줄였다. 두 연결의 실제 초기 부재,
factory/INSERT 각 한 번, precommit 비가시성, PostgreSQL에서 두 connection의 실제 lock 대기,
native unique 실패·확인된 rollback·한 번의 조회·동일 PK·최종 한 행의 대조는 그대로 유지했다.
반환 순서를 성공 bool로 재정렬하지 않고 실제 leader/follower 역할별 결과를 비교한다.

Go checkpoint source는 parent `521e43ecf3845a761a2e5e04b16b8550938a29da` 기준 비Markdown 2,944 files,
inventory `bcb2d777d2b462c4bbc29ac411099ab36fd6e60fd0d2d51df1e36081c9c7e543`이다.
이번 변경은 observer/fixture/생성 소비자이며 제품 ORM/backend/renderer의 재실행을 같은 source의 전체 PASS로 표시하지 않는다.
`TestGeneratedSingleObjectReference`와 수정한 `TestGeneratedSingleObjectCreation`을 normal/race/CGO=0 각각 실행했다.
각 mode의 parent 2 run/pass, skip 0이며 실제 양 DB 자식의 필수 44개/35개 경로와 출력 완결성을 확인했다.
Normal 7.374s, race 22.400s, CGO=0 3.882s다. 부모 Go는 source 경로를 보존하고 자식 module만 `-trimpath`와
부모 race/CGO 설정, offline/shared cache·`-count=1`을 사용했다.

Receipt는 `godj-single-object-reference-checkpoint-h667o8i6/receipt.json`, SHA-256
`8622d543bdc695b39f3aa06fdbe735d7d9ab3aee3abf6f5f1f5a5cabe8644b14`다.
`final-audit.json`은 시작/종료의 일대일 대응·log hash·source 동일성과 gofmt·영향 vet를 확인한다.
임시 PostgreSQL container의 제거·부재를 확인했다. 최초 `i9pyfpu9`는 새 fixture가 `CategoryID` 대신 `Category`를
Go FK 이름으로 선언하여 생성 단계에서 거부된 실패다. 선언을 고쳤으며 생성기의 검사를 약화하지 않았다.

### 단건 조회·생성과 생성 소비자의 영향 검증

2026-10-01, `Get`·`GetOrCreate`를 generic QuerySet, eager/prefetch query와 생성된 model facade에 연결했다.
원 Manager/BoundModel의 metadata snapshot, 독립 fresh 평가, 지연 입력과 transaction/savepoint 소유권을 함께 구현했다.
검증 source는 parent `521e43ecf3845a761a2e5e04b16b8550938a29da` 기준 작업 사본의 비Markdown 2,939 files이며,
inventory SHA-256은 `1a38ab7cc0c8cf9b14cb46a7567901ec126d191d58d333fe82c1e47a6f21d90a`다.
실행 전후 동일한 파일 집합과 bytes, 종료 뒤 log hash와 모든 시작/종료의 일대일 대응을 확인했다.

Go 1.26.5/darwin/arm64, 실제 SQLite와 임시 PostgreSQL 17.10 Debian/UTF8/libc/C/C, `LC_ALL=C`·`TZ=UTC`를 사용했다.
공유 Go cache·offline module·readonly와 부모 source 경로를 유지하고, 별도 generated module의 자식 Go에는
`-trimpath`와 부모 race/CGO mode를 적용했다. `go test -json -count=1 -timeout=12m`으로 실행했다.

| mode | orm/query/codegen 전체 unit | 선택한 생성 소비자 parent | skip |
|---|---|---|---|
| normal | 1,291 run/pass | 3 run/pass | 0 |
| race | 1,291 run/pass | 3 run/pass | 0 |
| CGO=0 | 1,291 run/pass | 3 run/pass | 0 |

생성 소비자는 `TestGeneratedSingleObjectCreation`, `TestGeneratedCustomSinglePrefetch`,
`TestGeneratedMaterializedStream`을 실행했다. Parent의 세 PASS를 자식 test 수로 부풀리지 않는다.
자식 출력의 실패·skip·잘림을 거부하고, 새 단건 생성 소비자의 38개 필수 경로와 기존 두 소비자의 필수 경로를
각각 한 번씩 성공했는지 검사한다. 모두 실제 양 DB를 사용하며 PostgreSQL 생략을 허용하지 않았다.

새 소비자는 typed/dynamic Get의 같은 AST, 0/1/여러 행·21행 한도·명시한 정렬/슬라이스·빈 조건,
warm cache 보존·nullable 값과 graph 복사, 일반/eager/prefetch·조합 조회의 기존 객체와 새 객체를 검사했다.
Borrowed session은 일반/조정/관계/조정 관계의 네 owner 각각에서 commit과 rollback을 실행했다.
관련 없는 unique 오류 뒤 부모의 전후 쓰기가 보존되는지, child RELEASE 뒤 parent rollback이 생성도 취소하는지,
생성 객체의 조회/저장/관계 접근이 부모에 연결되고 종료 뒤 warm cache도 만료되는지 확인했다.
Root batch의 callback context가 같은 pinned connection의 생성으로 이어지는 경계도 검사했다.

실제 두 연결이 모두 첫 조회에서 부재를 보고 factory를 각각 한 번 호출하도록 동기화했다.
선행 INSERT를 commit 전에 유지하고 독립 reader의 비가시성을 확인한 뒤 경쟁 INSERT를 진행했다.
PostgreSQL은 `pg_blocking_pids`로 해당 두 connection의 실제 lock 대기까지 확인했다.
SQLite는 생성 시도의 겹침과 실제 native unique 오류를 확인했으며 lock 대기를 직접 관찰했다는 주장은 아니다.
한 호출은 created=true, 다른 호출은 확인된 rollback 뒤 한 번의 fresh Get으로 created=false를 반환했고,
두 결과의 PK와 최종 단일 행이 같았다. 추가 INSERT·factory 재평가는 없었다.

단위 실패 대조는 하위 조회의 가짜 부재 오류·Close 실패·다중 결과, 입력이 만든 unique 오류,
비unique 오류·취소·rollback-required·unknown commit/rollback·quarantine·추가 cleanup 오류의 복구 거부를 포함한다.
0회/복수/늦은 callback과 삼킨 오류를 거부하며, join하지 않은 늦은 builder 뒤 Insert가 없는지 race로 검사했다.
확인된 root commit 직후 호출 context가 취소되는 raw 단위 대조와 양 DB의 세 생성 facade에서,
생성 성공을 취소 오류로 바꾸지 않았다.

최종 receipt는 `godj-single-object-checkpoint-xgtva_by/receipt.json`, SHA-256은
`c5839eaa40857de6a018590728ff38230b239ba8d96c79370653fa4dc64ba758`이다.
같은 디렉터리의 `final-audit.json`에 여섯 log의 hash·package 완료·source inventory를 대조했다.
임시 container의 제거와 부재를 확인했다. 생성 source·golden과 일곱 project bundle을 갱신했으며,
최종 gofmt·generated drift·영향 vet 결과는 `godj-single-object-static-dtevw3bd/receipt.json`에 기록한다.

중간 실패도 보존했다. `19r3bldj`는 부모 test에 잘못 적용한 `-trimpath`로 source 경로를 찾지 못했고,
`m166s_do`는 새 fixture의 AddKeys 인자를 slice로 고쳤으며, `bksre4i2`는 borrowed facade의 생성자를
`UsingSession`으로 고쳤다. 생성자의 수명 검사를 약화하지 않았다. `k2_ng0ew`의 세 mode 성공 뒤 최종 gofmt를 적용하고
위 최종 source에서 전체 선택 범위를 다시 실행했다. 그 재실행 준비 중 `1w44b6qw`는 PostgreSQL 임시 초기화 server를
ready로 잘못 판단해 test에 진입하지 못했다. 최종 TCP listener를 확인하도록 준비 절차를 고쳐 최종 실행을 완료했다.
이 환경/fixture 실패와 이전 source의 결과를 최종 PASS에 합산하지 않았다.

### Savepoint 기반의 구현과 영향 검증

2026-10-01, [활성 작업](../../work/0107-single-object-creation-and-savepoints.md)의 savepoint 기반을 구현했다.
`db.WithSavepoint`·공통 borrowed scope와 양 native backend의 일반/조정/관계 transaction, root batch의 pinned
transaction 및 read snapshot의 수명 경계를 연결했다. [ADR-0086](../adr/0086-single-object-creation-and-savepoint-ownership.md)의
savepoint 기반을 검증한 중간 source이며, 그 결과를 후속 ORM/생성 source의 검증으로 전이하지 않는다.

검증 source는 parent `521e43ecf3845a761a2e5e04b16b8550938a29da` 기준의 별도 작업 사본이다.
비Markdown 2,933 files의 inventory SHA-256은
`a02554d5ed7e16aeaf2071cd8a2af4d8e63d71562fa8d65f2a2a394db4c2ac88`이며 실행 전후 byte가 같음을 확인했다.
Go 1.26.5/darwin/arm64, 공유 Go cache·offline module·`-mod=readonly -trimpath`, `LC_ALL=C`·`TZ=UTC`를 사용했다.
실제 SQLite와 임시 PostgreSQL 17.10 Debian/C/C를 실행했다. PostgreSQL image는 기존 CI의 고정 digest를 사용했고
초기화 후 server version과 database collation을 직접 확인했다.

| mode | 공통 helper·scope·readscope·batch/stream unit | 양 DB 영향 검사 | skip |
|---|---|---|---|
| normal | 40 run/pass | 1,089 run/pass | 0 |
| race | 40 run/pass | 1,089 run/pass | 0 |
| CGO=0 | 40 run/pass | 1,089 run/pass | 0 |

각 mode는 `go test -json -count=1 -timeout=12m`으로 unit 5 packages를 실행한 뒤 `db/sqlite`·`db/postgres`의
`^Test.*(Savepoint|Atomic|Session|Batch|Snapshot|Conflict|Cascade|Transaction|Empty|Commit)` 범위를 실행했다.
모든 시작/종료·package 완료·필수 root·skip·잘린 출력을 대조하고, 새 native savepoint의 60개 필수 하위 경로도
세 mode에서 각각 PASS임을 확인했다. 선택 범위에는 기존 인증/session·cascade·conflict·빈 결과와 batch 회귀가 포함된다.
DB suite 전체나 전체 platform을 실행했다는 주장은 아니다.

실제 unique 오류 뒤 부모 쓰기를 계속하고 commit하는 경우, child RELEASE 뒤 parent rollback, 중첩 child만의 rollback,
child 취소·panic·Goexit의 저장 결과, parent handle 사용 거부·retained session/rowset의 만료를 검사했다.
열린 parent cursor와 batch의 savepoint 진입 거부, child batch 안의 추가 조회, 관계 SET_NULL rollback과
root stream의 같은 pinned connection에서 수행하는 savepoint도 실제 양 DB에서 검증했다.
제어문 생성/rollback/release 실패를 무시해도 root COMMIT이 실행되지 않는 fault 대조와 root rollback 실패의
unknown 분류·SQLite quarantine을 검증했다. 읽기 전용 snapshot은 write/savepoint capability를 노출하지 않는다.

Receipt는 `godj-savepoint-foundation-fnibu5lc/receipt.json`, 세부 하위 경로와 log hash audit는 같은 디렉터리의
`final-audit.json`이다. Receipt SHA-256은 `eba8a069e3f702d873e01447f0dcd72353c5ff519dc0ff6483a9982e8a6da97a`다.
실행 container 제거와 부재를 확인했고, 영향 `go vet ./db/... ./internal/savepointtest` 및 해당 변경의 gofmt·diff 검사도 통과했다.

### Savepoint 기반 검증에서 수정한 경계

첫 실행 `godj-savepoint-foundation-jphpca1o`는 PostgreSQL의 잘못된 초기 collation으로 DB 검증에 진입하지 못했다.
또한 개별 query 취소를 scope 정리 실패로 기록해 기존 SQLite batch의 회복 후 parent commit을 막는 회귀를 발견했다.
이 실행은 실패로 보존했다. `Rows.Err`의 조회 오류·취소와 실제 cursor `Close` 실패를 분리하고,
처리한 query 취소가 parent를 실패시키지 않는 직접 회귀 검사를 추가했다. 실제 Close 실패는 root 종료 오류로 계속 보존한다.

두 번째 `godj-savepoint-foundation-3j97c1sz`는 각 mode에서 unit 40 pass, DB 1,073 run 중 1,070 pass였다.
새 native savepoint는 모두 통과했지만 기존 빈 cursor 수명 검사 두 경로와 그 parent가 실패했다.
기존 검사의 `sql.ErrTxDone` 기대를 새 공통 session의 `backend_error/invalid_plan`으로 맞추고, 종료 뒤 Scan 거부도
추가했다. 빈 결과의 SQL 미실행·취소·수명 조건은 유지했다. 위 최종 source에서 전체 선택 범위를 다시 실행했다.

### 검증 범위와 다음 작업

위 checkpoint들은 각각 명시한 source와 영향 범위의 결과다. Helpdesk Label 확보·현재 권한/CSRF/audit와
Form/Admin/API/독립 client의 영향 검증 및 GDJ-0107 최종 source `8f8831ac`의 Hosted 통합을 완료했다.
행 잠금·update-or-create·bulk와 카탈로그의 나머지 기능은 이 작업의 완료 범위에 포함하지 않는다.

## GDJ-0106 — Binary 모델 필드와 입력 정책

### 현재 검증과 독립 기준

2026-10-01, 구현·아래 영향 검증과 최종 source `8e5c2b3e`의 Hosted full 통합을 완료했다.
기준 parent는 `9d1804b2e14eaa9d6f46b5d4a6ed343f02aa3454`이며 아래 결과는 Binary 변경을 포함한 작업 사본의 결과다.
Slug source의 Hosted full 성공을 이 변경의 성공으로 옮기지 않는다.

고정 Python 3.14.3/Django 6.1/DRF 3.18.0의 `binary_field_reference.py`를 SQLite와
PostgreSQL 17.10 Debian/UTF8/libc/C/C에서 각각 독립 두 프로세스로 재실행했다. 각 결과는 byte-identical이며
체크인한 fixture와도 일치했다. 7 profiles·39 합성 입력, 실제 저장 7행·10 조회·6 lifecycle 관찰을 포함한다.
ModelForm의 문자열 domain 224개와 JSON 273개를 Go 입력에 대조한다. Python 객체/TypeError,
read-only 제출 거부와 기존 JSON NUL 경계는 [DEV-0020](../DEVIATIONS.md#dev-0020--binary의-닫힌-입력과-명시적-집계)의 명시적 차이다.
PostgreSQL의 native bytea Min/Max 오류와 GoDj의 명시적 집계 확장을 구분한다.

- SQLite fixture: `28f0bb41ba3f8031ebcc04dd64a7b214e035183be48eab0414b59fa2d67dcdd2`, 222,510 bytes.
- PostgreSQL fixture: `76cf9722a9aac71d33a764a4c4e32411af220184f090ed779cb609bc69b71ae0`, 222,404 bytes.
- Corpus: `d89cb51b632f3d9d63ecc883b61b9eb70d1e35e3e28c73cc117621daf8f44f2e`.
- Observer: `15d09226c1470224de55d793feb14e8c83674ffde9375c740a0ace2678e0ea0d`.
- Native receipt: `godj-binary-native-replay-mzfadoi9/receipt.json`; 잔여 `0|0`, DB 제거·container 종료 확인.

### 제품과 소비자의 영향 검증

Go 1.26.5/darwin/arm64, `TZ=Pacific/Chatham`, 공유 Go cache와 offline module·readonly 설정을 사용했다.
각 DB checkpoint는 별도 PostgreSQL 17.10 Debian/UTF8/libc/C/C container/DB를 소유하며 SQLite도 실제 실행했다.
실행 전 compile된 root 목록과 현재 필수 하위 경로를 고정하고 `go test -json -count=1 -timeout=15m`의
모든 시작/종료·필수 실행·skip·build failure·출력 완결성을 검사했다. 자식 소비자는 부모의 race/CGO mode와 `-trimpath`를 사용한다.

| group | 범위 | race / CGO=0 | wall seconds |
|---|---|---|---|
| fields | 18 packages / 1,177 roots: 값·IR·query·폼·serializer·Admin·history·wire·생성기 | 각 9,625 pass, 0 skip | 16.639 / 6.569 |
| db | 양 DB Binary·column index/unique·default backfill·capability·seal의 48 roots | 각 445 pass, 0 skip | 12.717 / 12.803 |
| consumers | Binary/Slug/JSON/ModelFormSave/ModelClean의 5 생성 소비자와 실제 양 DB 자식 | 각 16 pass, 0 skip | 86.424 / 21.939 |
| helpdesk | 9 roots와 필수 203 경로: 실제 양 DB API/Admin·migration·저장·인가·rollback | 각 364 pass, 0 skip | 196.723 / 34.509 |
| openapi_client | 고정 ogen 재생성/drift·별도 module/HTTP·wire·부모 DB 검증 | 각 1 pass, 0 skip | 19.162 / 5.241 |
| source_and_candidate | 3 packages / 28 roots: source 결합·whole-candidate·외부 앱/namespace·실패 보존 | 각 190 pass, 0 skip | 9.854 / 6.510 |

Race와 CGO=0은 각각 10,641 run/pass다. Receipt는 `godj-binary-checkpoint-race-dftsnu_e`와
`godj-binary-checkpoint-cgo0-oq3cbl1u`에 있다. 전체 local platform/cold suite를 실행했다는 의미는 아니다.
두 mode와 마지막 normal 보완 실행의 비Markdown source는 2,922 files /
`4cc79c557690a65981c273b5d2cb4409865d6e09475bff36968bf23448a1bb2a`다.
모든 DB checkpoint는 잔여 `0|0|0`·DB 제거·container 종료를 확인했다.

Normal 첫 실행은 history의 binary default 판별 누락, 생성 소비자 호출 오류, Admin 저장 초기값의 입력 정책 혼동,
독립 client의 저장 JSON/HTTP escaping 구분과 HTTP fixture CSRF 문제를 발견해 실패했다.
수정 후 `godj-binary-checkpoint-normal-flbxqgwa`에서 DB 445·생성 소비자 16·독립 client 1·source/candidate 190 pass를 확인했다.
Fields는 9,624 pass와 Admin HTML 검사 1 fail, Helpdesk는 read-only 표시를 입력으로 오인한 검사에서 실패했다.
이 실행 전체를 PASS로 계산하지 않는다. 이때 source는 `8aa73237b525c8757fb3772bdb25f63a74c148875c38b31ecfe20bf918019a73`다.

이후 달라진 소스는 Admin render/template/test, Helpdesk Binary test, ModelForm initial helper/test의 여섯 파일뿐이다.
`godj-binary-checkpoint-normal-hsg121a7`에서 Admin 98 roots/302 pass, ModelForm 83 roots/2,098 pass,
Helpdesk 9 roots/364 pass와 독립 client 1 pass를 다시 확인했다. 모두 0 skip·누락이며 다른 파일의 바이트는 보존됐다.
수정 후 race/CGO=0은 위 여섯 group을 동일 source로 모두 실행했다.

Race runtime은 모든 group이 성공했지만 실행 중 생성한 Playwright 로그/스냅샷 여섯 개가 비Markdown inventory에 추가돼
원 receipt의 `source_unchanged`와 `pass`는 false다. 원 기록을 보존하고 `artifact-reconciliation.json`에서
기존 2,922개 제품/테스트 파일의 바이트가 그대로이며 추가분이 그 브라우저 산출물뿐임을 확인했다.
검증 산출물을 ignored output으로 이동한 뒤 현재 inventory가 원 실행 전 inventory와 정확히 같음을 확인했다.
이 보완 audit과 runtime 성공을 함께 사용하며 원 receipt가 처음부터 성공했다고 표시하지 않는다.

로컬에서 S3 환경이 없는 `TestAdminImageUploadVerifiedContentAndStorage` 전체 root는 실행 계획에서 제외했다.
첫 실행의 S3 하위 경로 누락은 성공으로 인정하지 않았다. 이미지 codec/S3 matrix의 23 필수 경로는
기존 Hosted PostgreSQL core owner가 소유하며 CI의 roster·no-skip 규칙은 유지했다.

생성 Binary 소비자는 기본값/빈 bytes/NULL/비 UTF-8·가변 버퍼/nullable cache 소유권, typed/dynamic/관계 query·F/IN·
DTO/정렬/집계, 상수 backfill·입력 정책/index/unique 정·역방향, 중복 DDL 실패의 history/행 보존·수정 후 재시도,
stale revision 거부·SQL projection의 DDL 없음과 typed deferred Form save·rollback·취소를 실제 양 DB에서 검사했다.
잘못된 string·mutable slice·F 타입의 별도 외부 compile도 의도한 정적 경계에서 실패했다.

Helpdesk는 실제 저장 JSON의 지문 계산과 SQL NULL/JSON null/빈 bytes·기존 길이 초과 출력, read-only POST/PUT/PATCH,
Admin·CSRF·현재 권한/Category scope, 지문 쓰기 전/후 오류·취소·응답 검증 실패의 원자 rollback을 검사했다.
PostgreSQL 동시 쓰기는 `pg_blocking_pids`로 실제 행 잠금 대기를 관찰한 뒤 writer를 재개해 최종 저장 JSON과 지문 일치를 확인했다.
읽기·no-op·관계만의 변경이 숨은 지문 쓰기를 만들지 않는 것도 검사했다.

### 생성 drift·실패 대조·브라우저

`make generate-check`는 Unicode 생성/두 Python 검사, 실제 일곱 프로젝트와 체크인된 relation 생성물 검사를 완료했다.
Helpdesk의 12개 생성 파일 snapshot은 `9e0e9d865bd4e5b0bac2917b130a6e17c36e91f77d90ccbe29479337c65acda2`다.
Receipt: `godj-binary-generated-drift-q0oyap32`. 영향 패키지 vet는 `godj-binary-vet-6cnqs8oh`, 전체 현재 Go 파일의 gofmt와
`git diff --check`도 통과했다. 고정 ogen을 실제 실행해 Helpdesk schema/client를 재생성했으며 나머지 다섯 schema와 module lock은 그대로다.

원본 파일을 바꾸지 않은 overlay 대조 다섯 개는 각 baseline PASS 뒤 의도한 runtime assertion으로 실패했다.
가변 입력 버퍼 공유·SQLite 외부 TEXT 수용·수동 ModelForm의 비편집 필드 binding 허용·JSON의 자동 read-only 제거·
history digest의 NonEditable 누락을 각각 잡았다. Build 실패나 skip을 대조 성공으로 계산하지 않았다.
Receipt: `godj-binary-controls-dbmgjs34`. `FuzzBinaryRoundTrip`은 30초/4 workers, 8,843,742 executions로 통과했다.
Receipt: `godj-binary-fuzz-2yh50h0m`; 값·base64 경계의 fuzz이며 전체 DB/platform 검증이 아니다.

실제 Chromium Admin에서 합성 계정으로 로그인해 legacy NULL 표시→JSON 저장→정확한 base64 SHA-256 표시를 확인했다.
HTML escaping과 `1e0` token을 포함한 저장 bytes의 기대 지문을 Python SHA-256으로 별도 계산했다.
잘못된 JSON은 field 오류와 원문을 재표시하고 저장 지문을 보존했다. DOM에는 지문 입력이 없으며 위조 hidden input 제출은
HTTP 400으로 거부됐다. 새 GET과 서버 종료 전 독립 DB read에서 원래 payload/지문을 확인했다.
브라우저/fixture를 종료하고 소유한 임시 DB를 제거했다. Receipt: `godj-binary-browser-f3rnavgn`; 화면·snapshot은
ignored `output/playwright/binary-field`에 있다. 실제 운영 인증·배포·다른 browser/platform의 증거는 아니다.

### Hosted 진행과 필수 목록 형식 보완

구현 source `94312b9d5884c6fd49eeea21c54fd288a59b66c2`의 [PR feedback 36803368763](https://github.com/progresshans/godj/actions/runs/36803368763)은
필수 목록에 추가된 빈 줄 두 개 때문에 CI 도구 검사 세 곳에서 실패했다. 실패 job `110182383682`의 원 로그 SHA256은
`3ffa56e83fe75b249273d7dd508aa5acb6a22de8f46fb16443fc534aaea0eca0`다.
같은 소스의 [full 36803443485](https://github.com/progresshans/godj/actions/runs/36803443485)은 형식 보완 후 재실행하기 위해
취소하여 `cancelled` 종료를 확인했으며 완료 증거로 사용하지 않는다.

두 필수 목록에서 빈 줄만 제거했다. PostgreSQL 1,962개·relation 2,127개의 모든 항목과 순서를 그대로 유지했고,
기존 race/CGO=0 plan의 실제 필수 하위 경로 집합도 새 목록과 동일함을 재검사했다.
`python3 -m unittest discover -s scripts/ci -p 'test_*.py'`의 45개 검사가 수정 뒤 통과했다.
Receipt는 `godj-binary-roster-repair-2e5vjs3e`이며 비Markdown 차이는 두 목록 파일뿐이다.
제품·생성기·생성물·Go 테스트·native fixture는 위 영향 검증과 byte-identical이다. 이 형식 보완과 최종 source의 Hosted 결과를 구분한다.

### 저장된 File/Image 초기값 보완

비편집 ImageField에 choices가 있을 때 `ValidateInitialValues`가 입력용 이미지 검사 기능을 요구하는 결함을
추가 회귀로 재현했다. 저장 초기값은 pending upload/clear 명령을 거부한 뒤 저장 이름의 타입·UTF-8/NUL·nullability만
검사한다. 현재 choices·입력 길이·업로드/이미지 I/O를 요구하지 않는다. 검사하는 map을 복사해 호출자 초기값을 보존한다.
기존 선택 목록 밖 이미지 이름·nullable File NULL·ExistingFile·잘못된 타입/NUL/비nullable NULL과 입력 정책 불변을 검사했다.
실제 Admin HTTP에도 비편집 이미지 선택 필드를 연결해 표시·수정 때 이름 보존과 위조 제출 거부를 확인했다.

이 보완의 기준 parent는 `c39656b370a1774762681a6a9d13d315f0bc3a8d`다. 앞선 제품 코드에 대한 변경은
`forms/model/values.go`, 그 새 회귀 파일, `admin/site_binary_test.go`의 세 파일이다.
현재 비Markdown source는 2,923 files / `76f3933c5ca524591657bef4b95becd2d3f7666e95643ebbb537f9131091c4dd`다.

| 보완 group | 실제 실행 | normal / race / CGO=0 pass | wall seconds |
|---|---|---|---|
| admin | Admin 98 roots (앞서 명시한 S3 image root 제외) | 각각 302 | 1.395 / 3.779 / 0.984 |
| forms_initial | ModelForm 84 roots | 각각 2,099 | 1.255 / 2.864 / 0.909 |
| helpdesk | SQLite/PG public 소비자 2 roots·필수 151 경로 | 각각 300 | 24.776 / 159.320 / 33.674 |
| openapi_client | 고정 client 전체 계약 1 root | 각각 1 | 5.786 / 21.615 / 6.061 |

각 mode 2,702 run/pass·0 skip/누락·실행 전후 동일 source, 독립 DB 잔여 `0|0|0`와 DB/container 제거를 확인했다.
Receipt는 `godj-binary-initial-checkpoint-normal-gyp6gkuh`, `godj-binary-initial-checkpoint-race-jmlc_pl3`,
`godj-binary-initial-checkpoint-cgo0-l_hjsh0d`다. 영향 vet와 현재 Go formatting/diff 검사도 통과했다.
앞선 10,641개 실행이나 브라우저 결과를 이 새 source에서 다시 실행한 것으로 표기하지 않는다.
새 source의 전체 통합과 현재 capture 결합은 Hosted full이 소유한다.

### 현재 source의 PR feedback과 Hosted full

`8e5c2b3e817e5b1090dce9d095ca85055e4af0b4`의 [PR feedback 36804890610](https://github.com/progresshans/godj/actions/runs/36804890610)이
성공했다. Job `110186915761`의 Whitespace·Fast Go feedback·scope 보고가 성공했고, 실제 merge checkout
`5c391c7ecc1b46fcaddaad89f3b2390a55c54833`의 parent에 해당 source가 포함되며 tree가
`1e0307c7b0f7d4b03ab33fa356201362491d67b4`로 동일함을 Git 객체와 대조했다.
원 로그 SHA256은 `571be8fdb3031a1b33e3a26765da748735251f4f3313d43e1f3e38eb9ed8728f`이며
receipt는 `godj-binary-feedback-zcihg31m`이다.

같은 source의 [Hosted full 36805466011](https://github.com/progresshans/godj/actions/runs/36805466011), attempt 1이 성공했다.
고정 source의 workflow에서 계산한 62개 job 이름과 실제 62개 고유 job ID가 일치하고 모두 성공했다.
집계 job `110205358003`에서 필수 owner 8개와 `full_platform_verified: true`를 확인했다.
모든 job의 source/run 결합·실제 checkout commit과 고정 workflow의 필수 명명 단계를 대조했다.
다른 owner가 맡는 Darwin 중복 실행, 지정 좌표의 cold build와 mode/shard별 S3·capture 단계의
조건부 생략도 해당 workflow 조건과 일치했다. 필수 실행 단계의 생략을 성공으로 계산하지 않았다.
Job 실행 audit SHA256: `322d3fe18457c56166f0eafbe87847bceb9cbb982acdae8f2f8b7a1c90a50b48` (`job-execution-audit.json`).
이전 `94312b9d`의 취소된 실행과 구분한다. Receipt: `godj-binary-final-hosted-full-adp71x2p/receipt.json`.

| PostgreSQL core | producer job | 실제 run/pass | skip | S3 receipt artifact |
|---|---|---|---|---|
| normal | `110188743849` | 4,462 | 0 | `11137807921` |
| race | `110188743700` | 4,462 | 0 | `11138635883` |
| cgo0 | `110188743780` | 4,462 | 0 | `11138151628` |

각 mode는 필수 1,962개 경로와 S3 관련 23개 경로를 누락 없이 실행했다. 고정 MinIO module/version/commit·
module checksum·Go 1.26.5/linux/amd64·CGO=0 build information·바이너리 digest를 검증하고, 실제 service ready·
제품 child exit 0·child 뒤 service 생존·graceful 종료·child/server reaped를 세 receipt에서 확인했다.
GitHub artifact의 archive SHA256, 원 producer의 artifact ID·attempt, buildinfo checksum을 함께 대조했다.

| capture | producer job / artifact | payload SHA256 |
|---|---|---|
| systemstate-postgres-1 | `110188743849` / `11137797995` | `fe5c52deb562b50e35ea0bba19a092a42b13b975898ed2467a6cac85fe9a47b3` |
| operator-postgres-1 | `110188743765` / `11137247179` | `9cd8fec02ef832e50624d6929d824bcf29d0cdd59da4277364d1d03bc3d59813` |

두 capture의 provenance·checksum·producer checkout을 확인한 뒤, 현재 작업 파일이 아닌 위 commit의 Git blob에서
각 source closure를 다시 계산했다. `binaryvalue/value.go`와 `internal/urlinput/url.go`가 두 closure에 포함된다.

| source closure | files | payload bytes | SHA256 |
|---|---|---|---|
| systemstate | 696 | 7,115,356 | `ebf4ece0dab58ae381956cd34632149da550abed29a1b94255e649cfdb947533` |
| operator | 774 | 6,970,658 | `79124ecfe286929a2cf471f66234b1d296622abc65b1f36e20cb459c361ff56d` |

Consumer job `110198569116`가 같은 실행의 두 producer/artifact를 받아 conformance를 실행했다.
provenance 검사·32-bit Linux migration/project-check·runserver·relation·Django/DRF oracle checksum·
reference 보존을 포함한 필수 10개 step의 성공도 확인했다.
Consumer log SHA256: `7a048574988494d662c2abfc4071efe5c79755f3a960ed7a05ea3bc1ef378aa4`.
최종 상태 기록은 Markdown만 바꾸며, 검증된 제품 source의 결과를 이후 제품 코드 변경으로 전이하지 않는다.

## GDJ-0105 — Slug 모델 필드와 Article 주소

### 독립 native 기준과 현재 범위

2026-10-01, 고정 Django 6.1/DRF 3.18.0/Python 3.14.3의 `slug_field_reference.py`를
SQLite 및 PostgreSQL 17.10 Debian/UTF8/libc/C/C에서 각각 독립 두 프로세스로 실행해 byte-identical 결과를 확인했다.
합성 73개 입력에 Form 9 profile 657개·serializer 8 profile 584개·validator 2 profile 146개,
Changed 6개·전체 ModelForm ASCII/Unicode 각각 5개·choices 3개·default 4개와 실제 저장/인덱스 변경을 관찰했다.
Char→ASCII Slug/index→Unicode→index 제거→재추가→unique→nonunique와 Char reverse에서 기존 문자열을 보존했다.
중복 unique 실패·commit=False 후보의 I/O 없음·명시적 save·transaction rollback도 관찰했다.
SQLite 여섯 변경의 SQL count는 5/5/4/5/4/5, PostgreSQL은 2/0/4/2/6/4다.
Go의 default btree 구현과 Django PostgreSQL의 pattern-opclass 최적화/SQL count를 동일하다고 하지 않는다.

- SQLite fixture SHA256: `d191049fcf08617786bf7f5bbd94f01d5b76b789b60f4239eb9feab202325dde`
- PostgreSQL fixture SHA256: `3bd978bd2c11ebe8cd44d61027c62020dba3952a3b0bd5a65d0528ca61433f6b`
- Corpus SHA256: `dc4e8ef4f3b07d9fa705b7b28e64c807a97d1a35c5a1836c64cc1614ac472886`
- Observer SHA256: `63433f68e3c362734840c8a502a7cf6a7efe17ec885743fe2c4389e5a8f7a15f`
- 전용 PG container/DB의 잔여 `0|0`, DB 제거와 container 종료 확인. Receipt: `godj-slug-native-_7zqmrag/receipt.json`.
- DRF untrimmed ASCII의 마지막 LF 수용 한 사례와 JSON NUL 16개는 명시한 차이로 검사하며 parity로 세지 않는다.
  Validator의 Python None 두 사례는 Go string 입력 domain 밖이다. [DEV-0015](../DEVIATIONS.md#dev-0015--slug-json의-절대-문자열-끝)를 따른다.

Article fixture에 현재 모델 slug를 추가한 뒤 정확한 고정 profile의 native suite 27개를 새 임시 출력에 실행했다.
변경된 read/Admin/API 세 묶음은 별도 프로세스의 재실행도 byte-identical이었고 나머지 24개는 기존 바이트와 같았다.
기본 uv 0.12.3은 profile guard가 거부했고, 기존 cache의 실제 uv 0.10.12 실행기를 PATH로 지정해 재실행했다.
Go actual을 기대값 생성에 사용하지 않았다. Receipt: `godj-slug-reference-refresh-3xtkbc0k/receipt.json`.

### 제품·생성·양 DB 소비자의 영향 검증

Slug/DBIndex를 history wire·정규화·digest·capability와 seal에 보존하고 일반 단일 column index를 양 DB에 연결했다.
Create/Add/Remove/Delete·reverse·SQLite remake·default backfill과 일반 index↔unique 전환을 실제 DB에서 검사했다.
중복·중간 DDL 실패·취소·revision/retry·새 연결과 namespace/catalog 위조를 포함하며 기존 행·recorder·index를 보존한다.
Scalar 저장 profile 13개(SQLite)/12개(PostgreSQL)에서는 중복과 NULL을 허용하는 일반 index를 확인했고,
PrimaryKey/Unique의 중복 일반 index 생성은 금지했다. 별도 생성 module은 일곱 단계 history·typed/dynamic 문자열 query·
ModelForm 지연 후보/명시적 typed Save·rollback/재개방을 native 저장 결과와 비교한다.

Article `0003_article_slug`는 기존 행에 NULL을 더하고 Unicode/nullable/blank/unique 선언을 Admin·Session/Bearer API에
연결한다. 공개 상세는 게시 행만 정확한 slug로 조회하며 미지정·잘못된 기존 값은 ID 주소로 연결한다.
인가·CSRF·입력 오류·중복·rollback과 실제 unique 충돌 뒤 오류 분류를 양 DB에서 실행했다. Rollback이 확인된 직접 거부만
입력 진단으로 전달하고 추가 소유자 실패·취소를 입력 오류로 숨기지 않는다.
독립 ogen client는 정리 전 긴 입력·Unicode·생략/null/blank·기존 값 출력·응답 누락/타입/길이 위조를 검사한다.
Parent는 최종 retained Article의 정확한 slug를 DB에서 따로 확인한다. 고정 생성기를 실행했으며 생성 코드를 수동 수정하지 않았다.

Go 1.26.5/darwin/arm64, `TZ=Pacific/Chatham`, 공유 Go cache와 GOPROXY/GOSUMDB off·`-mod=readonly`를 사용했다.
각 mode마다 PostgreSQL 17.10 Debian/UTF8/libc/C/C의 독립 container/DB에서 실제 PostgreSQL과 SQLite를 실행했다.
모든 최종 checkpoint는 실행 전후 source identity·잔여 `0|0|0`·DB 제거·container 종료를 확인했다.

| 실행 group | 실제 범위 | normal/race/CGO=0 결과 | 각 mode wall seconds |
|---|---|---|---|
| fields | schema/IR·validation·Form/ModelForm·serializer·ORM·history/자동 계획·project wire·codegen/Admin의 133 roots, 13 실행 packages | 각각 3,042 pass, 0 skip | 3.159 / 7.082 / 3.902 |
| db | 양 DB index/unique·named constraint·remake·field 순서·string/OneToOne 변경의 73 roots | 각각 850 pass, 0 skip | 7.700 / 17.551 / 18.432 |
| api_schema | OpenAPI 전체 44 roots | 각각 136 pass, 0 skip | 0.595 / 1.702 / 0.586 |
| consumers | Slug/URL/Email 생성 소비자 세 roots와 필수 자식의 실제 양 DB 실행 | 각각 부모 3 pass, 필수 자식 누락/skip 없음 | 5.514 / 24.049 / 13.825 |
| article | Article 전체 88 roots, 11 실행 packages와 현재 필수 32 경로 | 각각 150 pass, 0 skip | 6.265 / 50.613 / 8.947 |
| openapi_client | 독립 module 생성/드리프트·실제 HTTP/wire·필수 receipt·부모 DB 검증 | 각각 1 pass | 4.692 / 18.438 / 6.709 |
| conformance | Article Admin/API·현재 runner/schema의 28 roots | 각각 82 pass, 0 skip | 1.947 / 5.435 / 3.122 |

각 mode 합계는 4,264 run/pass다. 실행 전에 compile된 root 목록과 필수 하위 경로를 고정하고,
`go test -json -count=1 -timeout=15m`의 전체 시작/종료를 검사했다. Skip·누락·잘린/비JSON 출력은 없다.
자식 생성 소비자는 실제 mode와 `-trimpath`를 이어받는다.
Normal 최종 receipt는 `godj-slug-db-normal-qjyt42yh`, race는 `godj-slug-db-race-t06oy6fb`,
CGO=0은 `godj-slug-db-cgo0-egihnjdt`다.

위 일곱 group의 normal 비Markdown source inventory는 2,891 files /
`524345b33dd625d817c76fbe8414892492b11f01694bf8ffdf74e38f45262c51`다.
Race/CGO=0은 `79894a8f41af37d5d7abe004fefd26171c20cb0d067ccf50ed1f0faa0c8174ed`로 실행했으며,
이후 바뀐 것은 아래 process fixture의 기대값 세 파일과 `postgres-core-required.txt`뿐이다.
제품·생성기·생성물·위 일곱 group의 테스트는 byte-identical이다. 추가된 Article 필수 26 경로도 저장한 두 mode의
실제 JSON에서 모두 pass했음을 현재 32 경로 roster로 재검사했다. 서로 다른 inventory를 같은 source로 표기하지 않는다.

현재 모델을 실제 독립 executable에 연결하는 project migrate/createsuperuser 네 roots는 별도 normal checkpoint에서
24 run/pass, 0 skip·누락으로 완료했다(175.994초). SQLite/PG의 새 column·unique catalog·이전 prefix·9개 history와
operator의 실제 목록 decoding을 확인했다. `godj-slug-db-normal-xgw599sn`은 위 normal과 같은 inventory이며
별도 DB 잔여 `0|0|0`·DB/container 제거를 확인했다. 이 네 roots의 race/cold 전체 플랫폼 결과는 Hosted 소유다.

공유 Article 실행기를 마지막으로 확인하면서 격리된 system-state/runserver용 합성 초기 스키마도 현재 slug column에
맞췄다. 실제 Article migration 이력은 변경하지 않았으며 기존 행의 변경은 계속 별도 `0003_article_slug`가 소유한다.
Authenticated restart의 엄격한 JSON decoding과 실제 양 DB row snapshot에 slug를 추가해 NULL 보존을 검사했다.
그 결과 final inventory는 같은 2,891 files /
`d1a8b967bb914555d9f3defb6ff7c85f2aecbfcbe4ebc51fe0b7398bc547135f`다.
위 normal과 달라진 파일은 두 authenticated restart 테스트와 합성 `testdata/postgres/0001_initial.godj.json`뿐이다.
새 범위의 양 DB authenticated restart·runserver development loop/stale generated code 거부·system-state product/
distinct-process restart·고정 native 결과 비교는 10 roots, **15 run/pass, 0 skip·누락**으로 통과했다(48.451초).
`godj-slug-db-normal-5vieq8vh`에서 source 전후 일치·잔여 `0|0|0`·DB/container 제거를 확인했다.

이 확장의 첫 로컬 묶음 `godj-slug-db-normal-dcy34e5o`는 Linux/amd64 전용 capture producer를 잘못 포함해 실패했다.
별도 진단 overlay에서 `command.Start`의 `exec format error`를 확인했으며 해당 build 환경은 고정
`attestation.ProducerOS/ProducerArch`를 사용한다. 제품·검사·CI 필수 roster를 변경하거나 성공으로 세지 않았다.
위 로컬 범위에서 해당 전용 producer를 제외하고 다시 실행했으며, 실제 Linux producer와 새 capture/source 결합은
이 source의 Hosted full에서 확인한다. 진단 원문과 실패 receipt도 별도 보존했다.

### 브라우저·실패 대조·보조 검사와 초기 실패

실제 Chrome과 격리된 loopback Article/Admin·SQLite에서 합성 editor의 로그인/CSRF를 사용했다.
Padding을 포함한 Unicode 입력은 `Browser_읽기-주소`로 저장됐고 목록의 escaped slug link와 공개 상세를 확인했다.
`bad/slug`의 문법 거부와 `reserved_slug`의 실제 중복 거부는 제출 원문·서버 진단을 표시하고 DB의 두 행을 보존했다.
빈 값은 NULL로 저장되며 ID 상세 주소로 이동했다. 읽기 전용 SQLite snapshot을 각 실패 뒤 따로 확인했다.
전용 tab을 닫고 서버 정상 종료·임시 DB directory 제거를 확인했다. Receipt와 screenshot은
`godj-slug-browser-u_arzpb3`에 보존하며 배포 또는 다른 브라우저의 증거가 아니다.

여섯 overlay 실패 대조는 각각 원본 positive의 성공과 mutant의 실제 assertion 실패를 확인했다:
ASCII slash 허용, JSON 마지막 LF 허용, ModelForm 후처리 생략, digest의 DBIndex 누락,
ColumnIndexes capability 누락, SQLite catalog uniqueness 무시. Build 실패/skip을 대조 성공으로 세지 않았다.
Receipt는 `godj-slug-controls-uohzrxwu`다. `FuzzSlugValidator` 30초/4 workers는 72 baseline,
**3,872,083 executions**, 47 new interesting, 31.393초로 PASS했다. 존재하지 않는 이전 fuzz 이름의
`no fuzz tests to fuzz` 실행은 증거에 포함하지 않는다.

영향 package vet와 `make generate-check`(Unicode tables·일곱 프로젝트·checked-in relation 생성물),
CI 도구 45개(3.867초), 두 native dependency closure와 workflow 다섯 검사를 통과했다.
로컬 Git의 `setup_standard_excludes → access()` 정지 때문에 Go build의 VCS stamp만 `-buildvcs=false`로 제외했다.
Source inventory는 실제 ignore 규칙과 동등한 명시적 파일 목록으로 확인했고 저장소/전역 설정을 변경하지 않았다.
전체 Go 파일 2,204개의 gofmt, 현재 문서 175개의 local link와 diff whitespace 검사도 통과했다.
Receipt는 `godj-slug-final-checks-_9hdnku6`이며, 공유 실행기 fixture를 반영한 final source에서도
`godj-slug-final-checks-r9k4y80w`로 같은 문서/format/diff 검사를 통과했다.

초기 normal checkpoint는 history digest의 두 flag 누락, SQLite의 중간 DDL 오류 marker가 숨긴 native unique cause,
생성 소비자의 잘못된 URL 설정명, Project5 테스트의 중복 column/이전 네 column helper,
session client의 갱신 전 CSRF token과 옛 Article field/history 기대값으로 실패했다.
Digest와 제한된 constraint cause 전달을 수정하고 나머지 fixture를 실제 계약에 맞췄다. BUSY/capability 오류의
late-DDL 재분류 방지, duplicate projection 거부, CSRF 검증과 과거 migration prefix 계약은 유지했다.
최종 묶음을 위 일곱 group으로 다시 실행했으며 실패한 초기 receipt도 보존했다.

새 IR kind·일반 index·migration·생성과 Article의 누적 플랫폼 통합은 이 source의 **Hosted full**이 소유한다.
URL source의 결과는 이 변경의 Hosted 성공으로 전이하지 않는다. 로컬 전체/cold는 중복하지 않았다.

### Hosted feedback의 공유 fixture 회귀와 보정

Slug 구현 source `e7dfddfec7a78e3ee7c19376895507cc10b02b2d`의
[PR feedback 36779684516](https://github.com/progresshans/godj/actions/runs/36779684516)은 실패했다.
실제 job `110106390097`의 checkout `3ac8c2e65f5805dd75dab3681db43596f1ed2131`는 해당 source를 parent로 가지며,
두 tree `ee5d1b4f5e14c70467a42820c77648fda5aee0f4`의 일치를 확인했다. Log SHA256은 `f7c64a17c67d2e804c3844b4621df8590847d9f79156bccc7545f79c5e420333`다.
Codegen의 기존 Article 기준 hash, SQLite의 네 column 임시 table과 capability 기대값,
backend capability field 목록, ORM의 네 field metadata/세 writable field 기대값이 현재 선언을 반영하지 못했다.

공유 SQLite fixture에 실제 nullable/unique slug column을 추가하고 SQL NULL decode·Unicode 저장/명시적 NULL을 검사했다.
이전에는 SQLite의 quoted identifier fallback 때문에 일부 조회만 통과할 수 있었으므로 실제 NULL도 assert한다.
현재 generator hash와 13개 capability의 엄격한 목록을 갱신했고, concurrent descriptor의 다섯 field와
Slug flag 복사, Save snapshot의 독립 Slug pointer·NULL/전체 writable field 순서를 확인했다.
제품 코드·생성기·생성물은 바꾸지 않았으며 기존 실패/취소/rollback/trigger-ignored 행 검사는 유지한다.

Codegen/ORM/migration backend 전체 331 roots는 normal/race/CGO=0 각각 **1,033 run/pass**,
공유 fixture를 직접 쓰는 SQLite 열 roots와 capability 한 root는 각각 **11 run/pass**로 통과했다.
필수 root·package의 시작/종료·0 skip·0 누락과 변경한 일곱 테스트 파일의 source 전후 일치를 확인했다.
각 mode의 unit/SQLite wall seconds는 1.015/1.274, 6.046/2.715, 0.984/1.255다.
초기 fragment 검사의 gofmt 정렬 공백 불일치도 runtime metadata 검사와 구분해 보정했다.
실패 receipt는 `godj-slug-feedback-36779684516-cnkzvc8x`, 보정의 scoped 세 mode는 `godj-slug-feedback-regressions-ogc6zzle`에 보존한다.
비Markdown inventory는 2,891 files / `a5927169218fcfd50408d1dae5d1603dfd93a812182e26e219b242fb793aa18b`다.
`godj-slug-repair-checks-rutif3cj`에서 일곱 테스트 외 제품/생성 source의 byte identity와
Go 2,204 files format·문서 175개 local link·diff whitespace도 확인했다.
보정 source `e124dda8de6934b039f56cf80b80839f2db2333a`의
[PR feedback 36781940910](https://github.com/progresshans/godj/actions/runs/36781940910)은 성공했다.
실제 job `110114072843`의 merge checkout `286170938b1ef46ca5e7d471d6ee0a91ec812ccf`는 source를 parent로 갖고,
source와 tree `6ca2c545914aba53177a148c0fd95d1dd4efd171`가 같다. Log SHA256은
`c1a55e94d797ab70b7a2d049bd91c429ee9106f9b07b1a4f729cdc63d7b3c863`이며 receipt는 `godj-slug-repaired-feedback-36781940910-0qvvpv__`다.
이전 source의 실패를 성공으로 덮지 않는다.

같은 source의 [Hosted full 36782116636](https://github.com/progresshans/godj/actions/runs/36782116636), attempt 1을 새로 시작했다.
Plan job `110114666889`의 실제 checkout과 full 선택을 확인했으며 plan log SHA256은
`f4cf6b1da7f0c7915d2ff8f5b356b1f9bc9ed554f940e7c3469fae8aab9d33b2`다. 아래 회귀가 확인되어 이 source는 전체 검증을 통과하지 못했다.
실행 receipt는 `godj-slug-repaired-hosted-full-36782116636-4dun59i1`에 보존하며 아직 전체 PASS가 아니다.
이 실행을 기록하는 문서 갱신은 별도이며 실제 검증 source는 위 commit으로 고정한다.

### Hosted full의 Form 시나리오와 native 체크섬 회귀 보정

`36782116636`의 project-check·portable conformance·relation 작업에서 두 원인이 확인됐다.
Go Form 비교 시나리오는 전체 Article 필드를 투영했지만 고정 Django `_ArticleForm`과 `_ArticleModelForm`은
명시적으로 title/published/summary 세 필드만 선언한다. Go의 공개 `NewSpecForFields`로 같은 입력 범위를 지정했다.
FRM-002의 실제 cleaned 값/순서와 FRM-004의 실패 뒤 cleaned 값, FRM-005의 실제 쓰기/행 검사를 유지한다.
Slug 입력과 전체 모델 투영은 위 전용 필드·생성 소비자·Article/Admin/API 범위에서 계속 검사한다.

독립 native 실행으로 갱신했던 read/Admin/API 세 파일은 현재 고정 profile 재실행과 byte-identical이었지만,
두 `SHA256SUMS`와 세 protocol 테스트의 고정 바이트 목록이 이전 값을 유지한 것을 확인했다.
네 묶음(read/template-form/article-admin/article-api)을 실제 uv 0.10.12·고정 Python/Django/DRF로
새 임시 출력에 다시 실행해 현재 관찰과 대조한 뒤 세 관찰을 가리키는 checksum 기록과 고정 바이트 목록을 갱신했다.
Template-form 관찰, 의존성 lock/profile, 나머지 native 관찰과 비교 선택자는 바꾸지 않았다.
Go actual을 native 기대값 생성에 사용하지 않았다. 재관찰 receipt는 `godj-slug-lock-verification-_7g7uqnn`이다.

| Native 관찰 | 검증한 SHA256 |
|---|---|
| read | `b0e1c178c3d8f999fa606d67eaadda178e910f62f3eb5fbf6b2cd881cafd5cd0` |
| article-admin | `112c5da2e2a2c1393c9f52ac058b87dfb9f76f20b4111edb735e5fa17ca641cf` |
| article-api | `91abed33c2c39b96aa31c57690bda9d62d4bc51b401385741a510f945f260467` |

Protocol 전체 124 roots는 normal/race/CGO=0 각각 **909 run/pass**, 관련 runner·godjcheck 일곱 roots는
각각 **31 run/pass**로 통과했다. 각 mode의 protocol/Form wall seconds는 2.621/2.078, 14.530/5.079,
2.554/1.974다. 필수 root/package·시작/종료·0 skip·0 누락과 source 전후 일치를 확인했다.
이 보정은 여섯 conformance 파일에 한정되며 제품·생성기·생성물·CI 선택 범위는 그대로다.
Receipt는 `godj-slug-full-regressions-zfv0fgx4`, 실패 로그는 `godj-slug-full-failures-36782116636`에 보존한다.
비Markdown source는 2,891 files / `7d10e5830419965c4d8978a59894eabb4d948692e44399efda4d12a34d7320af`다.
새 source의 Hosted full은 별도로 실행하며 이전 실패·미완료 job을 성공으로 옮기지 않는다.

### 추가 owner의 iexact fixture와 Python 호환 digest 보정

위 보정 source `d5aa14b28498f1aa70cdf2b48f9cbb97ca5756ec`의
[Fast feedback 36784045323](https://github.com/progresshans/godj/actions/runs/36784045323)은 성공했다.
실제 job `110121054372`의 merge checkout `7561afa10d8a2a69f28832d19710de1d2197a69a`는 source를 parent로 갖고
tree `6ef400061b9b5e1ce3924c5ac4fa4933b5a3eac8`가 같다. Log SHA256은
`f75ef8b50edf32db70dd65c4206ba647837d5cf959adb934f24334396632c50e`다.
새 [full 36784073634](https://github.com/progresshans/godj/actions/runs/36784073634)의 실제 plan checkout도 확인했지만,
이전 full의 종료 로그에서 추가 실패가 확인되어 이 실행은 취소했다. 완료된 전체 플랫폼 증거가 아니다.
이전 `36782116636`은 20 success / 21 failure / 21 cancelled로 종료했고 모든 failure job의 실제 checkout과 로그를
`godj-slug-full-failures-36782116636/terminal-receipt.json`에 보존했다. 새 full의 취소 상태는
`godj-slug-integrated-hosted-full-36784073634-g1829i3n`에 보존한다.

PostgreSQL core의 실제 실패는 공유 `iexacttest.Tables`에 slug column이 없어 현재 Article projection이
SQLSTATE 42703을 반환한 것이다. 양 DB가 쓰는 임시 table에 nullable/unique slug를 더하고
seed 뒤 전체 Article 조회에서 모든 slug가 SQL NULL인지 확인했다. 같은 형태의 다른 수동 table을 확인해
Admin delete/audit rollback fixture 한 곳도 보정하고 거부 뒤 행과 NULL 보존을 명시했다.
이전 네 column DDL만 주입하는 SQLite overlay는 새 NULL assertion으로 실제 실패했다.
Receipt `godj-slug-iexact-control-_j1otcrk`는 build 오류나 skip을 실패 대조로 세지 않는다.

Python 3.12.13 호환 job의 테스트 375개는 정해진 reference-profile 전용 skip 네 개를 포함해 검증됐지만,
뒤의 311-scenario digest 검사는 Article native 결과 변화로 실패했다. 고정 Django 6.1/DRF 3.18.0/
Python 3.14.3에서 동일한 311 시나리오를 독립 두 프로세스로 다시 실행해 전체 바이트의 일치를 확인했다.
새 payload는 **1,082,209 bytes**, SHA256 `c07d1ed3914ba692e7b35bb38da73e9d1f3565de9ed39a7e9391590d1f0062e5`다.
`godj-slug-compat-native-58lzhqad`에 시나리오별 원문 크기·hash와 전체 payload를 보존했다.
CI의 길이·hash 상수 두 곳만 갱신했으며 시나리오 수·실행·환경·owner·비교는 변경하지 않았다.
다른 Python/플랫폼의 새 source 검증은 Hosted에 남아 있다.

실제 PostgreSQL 17.10/SQLite iexact 네 roots와 필수 하위 경로는 normal/race/CGO=0 각각
**470 run/pass**, 0 skip·누락·inventory 오류로 통과했다(2.557/4.484/2.398초).
각 mode의 독립 DB/container에서 source 전후 일치·잔여 `0|0|0`·DB/container 제거를 확인했다.
Receipts는 `godj-slug-iexact-normal-onqjm8i8`, `godj-slug-iexact-race-nacxxseo`,
`godj-slug-iexact-cgo0-uq1uu7bg`다. Workflow 구조 여섯 roots와 Admin audit rollback 한 root도
각 mode **7 run/pass**로 통과했다(0.844/2.088/0.797초, `godj-slug-tail-regressions-_lxi097m`).
최종 비Markdown source는 2,891 files / `04fc0816f6046ad2818e79f363a9a88559c7df05e8d590958da362bc240f15e1`다.
iexact normal의 이전 inventory는 `9b378fc712ab75480781fb4a8d169795e21563be9441e399e1f32c44be9530c8`이며,
그 후 바뀐 파일은 위 별도 세 mode로 실행한 Admin audit fixture 한 곳뿐이다. 다른 source를 동일하다고 기록하지 않는다.

### 최종 Hosted full과 source 결합

2026-10-01, source `99ac532a2cfc5e694bfcad4f0d64a6d42c09857a`의
[Hosted full 36785492754](https://github.com/progresshans/godj/actions/runs/36785492754), attempt 1이 완료됐다.
고정 workflow에서 독립적으로 도출한 62개 job 이름·좌표와 실제 목록이 일치하며 **62/62 success**, 취소·실패·미선택은 없다.
Plan job `110125789091`의 실제 checkout/full 선택과 aggregate job `110148500083`의
`scope: full`, `full_platform_verified: true` 및 아래 여덟 필수 owner를 확인했다:
`command-product-matrix`, `conformance-validation`, `exact-darwin-validation`, `portable-go-matrix`,
`postgresql-product`, `product-project-check-matrix`, `python-compatibility-matrix`, `relation-product-matrix`.
Linux/macOS amd64·arm64의 관계/명령/project-check 세 mode, 고정 darwin profile, Python 네 버전과
기존 32-bit/외부 archive 경계를 포함한다. 앞선 실패/취소 실행의 부분 결과는 재사용하지 않았다.

같은 source의 [Fast feedback 36785469445](https://github.com/progresshans/godj/actions/runs/36785469445)도 성공했다.
Job `110125708900`의 실제 merge checkout `1ce50ea47f98d29ad7bacd5bb3c08ba15fb7871b`는 source를 parent로 갖고,
tree `f30cdcf1edf9520274453ce002035396fec8e2f3`가 source와 같다. Log SHA256은
`7463f2be3f2ec68daf5a70909497f762142d5a3f4a6b6a711f0cd3a83451bf4b`다.

새 capture 두 개는 archive SHA256·payload·provenance·동일 run/attempt/producer·Git 객체의 source binding을 확인했다.
Consumer job `110137935093`에서 같은 artifact와 producer ID, checkout, 실제 conformance·32-bit 세 단계·
두 oracle checksum·reference 무변경을 포함한 필수 열 단계를 확인했다.
Consumer log SHA256은 `3cb99d193649497b20ead596a77df8036f055e9048c05f75628d355d066ea398`다.

| Capture | Artifact / producer job | Archive SHA256 | Payload SHA256 |
|---|---|---|---|
| `systemstate-postgres-1` | `11129813046` / `110125834250` | `546e90c3893528ef9e99f25feeee2d2aea4b590a8ddea7785b877e8d16a073df` | `7875689ec4429058d0571fcc7f74740a5ad93210277448cbd5ad2d91116557c9` |
| `operator-postgres-1` | `11129633993` / `110125834257` | `cbbdc0960046709b8718842bdf7e479e6e025eec85fad6a351ebd071a3cac87e` | `cff5331e8f4affff25ec56bc4de492afdd94a1e5dd38a4643f0163a345c340d4` |

Git에 저장된 해당 source로 다시 계산한 binding은 다음과 같다. BinaryField 작업 사본의 미검증 파일을 읽지 않았다.

- systemstate: 690 files / 7079516 bytes / `02e19e675448901e350bf894c88c231de64c25981f79982a52248e2e6f3ec947`.
- operator: 768 files / 6933461 bytes / `6bec55ee0cf90be074d17a31f42d1afbc38c5eb81af42eef1253a831dc531213`.

PostgreSQL core normal/race/CGO=0 각각 15 packages, **4,445 run/pass**, 0 skip를 확인했고
필수 1,954 경로와 실제 S3 필수 23개를 포함한다. 고정 MinIO module·commit·lock·Go build info·binary hash와
ready, child/server exit 0, 양쪽 reap, graceful cleanup을 세 mode의 archive에서 따로 검사했다.
Binary SHA256은 세 mode 모두 `c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`다.

| S3 mode | Artifact | Archive SHA256 |
|---|---|---|
| normal | `11130047803` | `266fcd806d29371b8877045c8f99c44ba92b071c2f0b906a5b4d8e0e3ecbf94a` |
| race | `11131400911` | `d0d6e72f00bf02f956179059904fcadcf438fbc40c8d7724e10ad4211090538a` |
| cgo0 | `11130520271` | `618598a0abea9bf509dbd39f03264379d1da3946ecfd3143be4e20d9eb52fa72` |

최종 receipt는 `godj-slug-tail-hosted-full-36785492754-5g4s85rl/receipt.json`이다.
위 결과는 GDJ-0105 source의 누적 전체 검증이며 BinaryField의 진행 중인 변경 또는 기능 카탈로그 전체 완료를 뜻하지 않는다.
완료 기록은 Markdown 세 파일만 변경하며 제품·생성물·workflow·lock은 위 검증 source와 같다.
로컬 전체/cold와 같은 source의 Hosted full을 다시 실행하지 않는다.

## GDJ-0104 — URL 모델 필드와 Helpdesk 외부 참조

### 독립 native 기준

2026-10-01, 기반 source `8d89ec28b10cbba4787a439142354a6c6d8bb73d` 이후 URLField를 연결한다.
`url_field_reference.py`는 고정 Django 6.1/DRF 3.18.0/Python 3.14.3과 synthetic 82개 입력만 사용한다.
SQLite와 PostgreSQL 각각 독립된 두 프로세스의 출력이 byte-identical이었다. DB별 Form 7 profile × 82 = 574,
serializer 4 × 82 = 328, validator 82, Changed 6, 전체 ModelForm 5와 choices 3, default 4,
Char→URL→Char의 저장/조회 결과를 기록했다. SQLite의 forward/reverse SQL count는 4/4, PostgreSQL은 0/0이다.
Go의 metadata-only 구현과 SQL text가 같다는 의미가 아니다. JSON `null_path`의 4 profile은 전역 transport 거부로
비교하며 native field 진단 순서의 parity로 세지 않는다.

- SQLite fixture SHA256: `5627780b808bb43fa75d1371797de7d339aa7b9fed81451d9ec42e6b6e2fb366`
- PostgreSQL fixture SHA256: `fecc35eac769c773e7a076f9fbbf8e70562d3d6bde4dbe3e5f5cfef10fbd450c`
- PostgreSQL 17.10 Debian/UTF8/libc/C/C의 전용 container·DB를 사용하고 `0|0` 잔여 상태·DB 제거·container 종료를 확인했다.
- 실행은 `uv run --project conformance/reference/drf --frozen --offline --with 'psycopg[binary]==3.3.6' python conformance/runners/django/url_field_reference.py`와 명시적 PG 환경을 사용했다. 기존 lock은 변경하지 않았다.
- 초기 direct venv 실행은 PostgreSQL driver 부재로 실패했다. SQLite 결과만으로 양 DB 성공을 선언하지 않았고 고정 overlay로 재실행했다.
- 로컬 receipt: `godj-url-native-22j2_ilp/receipt.json`; 초기 실패: `godj-url-native-bv21pvel`.

### URLField 제품과 소비자의 영향 검증

Schema IR `url`·기본 200자·생성 string/nullable descriptor·typed/dynamic/관계 lookup·양 DB migration을 연결했다.
Char↔URL의 history wire·동일 storage metadata 변경·reverse·query·unvalidated 저장·재개방과 실제 Form→typed Save의
rollback/commit을 별도 생성 module에서 실행했다. 자동 계획은 Char→URL→Email→URL→Char와 no-op을 확인한다.
Helpdesk의 `0022_ticket_external_url`은 기존 데이터에 NULL을 추가하며, Admin/API 생성/수정·빈 문자열/null/생략·기존 값 출력과
현재 인가/CSRF/Category 범위·실패/rollback·재접속을 유지한다. 오래된 migration 단계는 그 시점에 존재하는 scalar column의
직접 storage snapshot으로 비교하며, 현재 descriptor로 아직 없는 URL column을 조회하는 테스트 오류를 제거했다.
기존 unique 실패·수정·재시도, report/label/link의 역방향 데이터 보존 검증은 계속 실행한다.

최종 비Markdown source/config inventory는 **2,856 files** /
`96de3934bd84dbeb6737c9416df6daa8a3e8af09810c409abf89b6e97df84091`이다.
세 mode의 실행 전후와 브라우저 종료 뒤 같은 inventory를 확인했다. Go 1.26.5/darwin/arm64,
normal/race는 CGO=1, CGO=0은 명시적 환경을 사용했다. `TZ=Pacific/Chatham`, 공유 기본 Go cache,
GOPROXY/GOSUMDB off와 `-mod=readonly`를 유지했다. mode마다 PostgreSQL 17.10 Debian/UTF8/libc/C/C의 별도
container/DB를 사용하고 모든 실행에서 잔여 `0|0|0`, DB 제거·container 종료를 확인했다.

| 실행 group | 실제 범위 | normal/race/CGO=0 결과 | 각 mode wall seconds |
|---|---|---|---|
| fields | validation/schema/IR/Form/ModelForm/serializer/ORM/migration·자동 계획 등의 `URL/Email/Char/String/Blank/ModelBind` 관련 42 roots·10 실행 packages | 각각 1,343 pass, 0 skip | 7.324 / 10.280 / 7.306 |
| api_schema | `api/openapi` 전체 42 roots | 각각 134 pass, 0 skip | 1.273 / 1.869 / 1.059 |
| consumers | `TestGeneratedURLConsumer`, `TestGeneratedEmailConsumer`; 자식의 실제 SQLite/PostgreSQL history/storage/typed 저장 | 각각 부모 2 pass; 필수 자식 누락/skip 없음 | 4.377 / 9.957 / 3.529 |
| helpdesk | SQLite/PostgreSQL public consumer 두 roots, 필수 145 경로 | 각각 288 pass, 0 skip | 20.333 / 148.204 / 20.434 |
| openapi_client | `TestGeneratedOpenAPIClientContract`; 고정 ogen의 별도 module 생성/드리프트·build·HTTP·wire·부모의 최종 SQLite 조회 | 각각 1 pass, 필수 receipt 전체 확인 | 9.518 / 18.343 / 8.544 |

각 group은 compile된 test 목록과 선택된 필수 roster를 먼저 정하고 실제 `-json -count=1`의 시작/종료를 검사했다.
Go test timeout은 15m이며 정상 종료·필수 누락/skip·잘린/비JSON 출력 부재를 확인했다. generated child는 실제 race/CGO mode와
`-trimpath`를 이어받는다. JSON NUL 4개는 transport 거부를 별도로 assert했다. Direct validator의 비교 가능한 문자열은 81개이며
native `None`은 Go string 함수의 입력 domain 밖이다; Form/serializer의 생략/null은 별도 사례에서 실제 실행한다.

OpenAPI의 정리 전 URL 입력은 raw 길이/URI format으로 차단하지 않는다. `x-godj-url`은 네 스킴·추론 없음·2048자 grammar 한도를,
정규화 metadata는 모델의 200자 한도를 설명한다. nullable response의 `maxLength`를 문자열 anyOf branch에 두어 고정 ogen이
문자열 길이 검증을 생성하도록 고쳤다. Helpdesk 외에 Article Session/Bearer validator가 갱신됐으며 Identity 두 문서의 동등한
nullable 표현도 갱신됐다. 생성 코드를 수동 수정하지 않았다. Client는 200자를 넘는 padding의 서버 정리·문법 거부/기존 값 보존·
생략/blank/null, 잘못된 기존 URL의 출력과 응답 누락/타입/길이 초과를 검사한다. Parent는 최종 URL 문자열을 DB에서 따로 확인한다.

최종 실행 위치는 `godj-url-db-normal-0j4996ta`, `godj-url-db-race-82f6m6b1`, `godj-url-db-cgo0-3r32yfc9`다.
UI 수정 전 source inventory `195f21c9114d1ce0772b8e060f90fd43fa0cb14329a1c25d7b876f77f61b35b6`에서도 세 mode를
완료했으나 아래 브라우저 문제가 남아 최종 두 파일 변경 후 다시 통합했다. 최종 source와 달라진 파일은
`admin/site_templates/form.html`, `examples/helpdesk/url_test.go` 두 개뿐이다.

### 브라우저 제출 경계

실제 Chrome 154·격리된 loopback HTTP host에서 제품 Helpdesk/Admin과 SQLite를 실행했다. 합성 editor의 정상 로그인/CSRF를 사용했다.
수정 전 `Example.com/Browser`는 `typeMismatch=true`, `form.noValidate=false`였고 Save를 눌러도 POST하지 않아 DB는 NULL이었다.
고정 Django 6.1의 `admin/change_form.html`과 같이 Admin 모델 입력 form에 `novalidate`를 적용했다.
수정 후 같은 입력은 브라우저 문법 상태와 무관하게 제출되고, 실제 redirect/목록·새 GET과 직접 SQLite 조회에서
`https://Example.com/Browser`를 확인했다. 빈 subject+`javascript://example.com` 제출은 서버의 required/invalid 두 오류를
원래 입력과 함께 표시하고 DB 값을 보존했다. 유효한 subject와 빈 URL 재제출은 NULL로 저장했다.
브라우저 검증 비활성화가 서버 필수/문법 검증을 제거하지 않는 것을 실제 제출로 확인했다.

Before/after 서버를 각각 정상 종료하고 전용 DB directory 제거, 전용 브라우저 종료를 확인했다.
호스트는 실제 공개 API를 조립한 임시 main이며 제품 배포 또는 다른 브라우저의 증거가 아니다.
Receipt/host source는 `godj-url-browser-8q0honyn`, snapshot·오류 screenshot은 ignored `output/playwright/url-field`에 보존했다.

### 실패 대조·fuzz·생성물과 초기 실패

고정 source inventory `195f21...`에서 다섯 Go overlay를 원본 positive 실행과 쌍으로 실행했다.
허용 스킴에 javascript 추가, Form의 스킴 보완 제거, JSON에 스킴 추론 허용, model URL 후처리 제거,
authority NFKC delimiter 검사 제거를 각각 실제 assertion 실패로 검출했다. Compile 실패나 skip을 성공으로 세지 않았다.
`godj-url-controls-b6d7pg_p`의 receipt와 source hash를 보존했다. 이후 UI 두 파일 외에는 byte identity를 확인했다.

같은 algorithm source에서 `go test -json -run '^$' -fuzz '^FuzzURLValidator$' -fuzztime=30s -parallel=4 ./validation`은
81/81 baseline, **2,530,954 executions**, 92 new interesting, 31.01s로 PASS했다. 영향 package vet,
`make generate-check`와 CI 도구 unit suite도 PASS했다. 실행 위치는 `godj-url-final-checks-6h2lwsef`다.
이후 UI template/test만 변경했고 생성기·선언·고정 client 생성물·algorithm/controls source는 같음을 비교했다.

초기 통합 `godj-url-db-normal-v0zncslh`는 None initial의 잘못된 typed 표현, 생성 테스트의 존재하지 않는 Get API,
과거 단계의 current descriptor 조회/고정 노출 목록, nullable 길이를 놓친 generated client assertion으로 실패했다.
`godj-url-db-normal-xkfev_34`는 typed instance PK initial 누락과 상세 응답의 옛 필드 수 때문에 실패했다.
이 실패들을 보존하고 실제 BindInstance/First·역사 storage 비교·현재 노출 목록과 공통 schema 위치를 수정한 뒤 통합했다.
불일치를 삭제/skip하거나 문법 검증을 완화하지 않았다.

로컬 전체/cold 검증은 반복하지 않았다. 새 IR kind·migration·생성·공통 OpenAPI·Admin/소비자 연결의 누적 플랫폼 통합은
**Hosted full**이 소유한다. 이전 BigTIFF web 성공은 이 구현의 Hosted 증거가 아니다.

### Hosted 통합에서 발견한 source binding 누락

URL 구현 source `a1fa31d4a790d1de5ad89cafaec09ca2b32d978c`의
[PR feedback 36760015763](https://github.com/progresshans/godj/actions/runs/36760015763)은 PASS다.
실제 job `110039915585`의 merge checkout `5f593fd5f03f9d87c8ed9c66b942816cf2e4b395`는 해당 source를 parent로 가지며,
tree `c6593602ada001bf1b7a20c1156f02e50ee26dca`가 source tree와 같다. 로그 SHA256은
`f8f3470ba915df6668506321e2558b423e7ea760b05af87a8ffe5057f2c083c3`이다.
Receipt는 `godj-url-feedback-36760015763-a_3akhm7`에 보존했다.

같은 source의 [Hosted full 36760086296](https://github.com/progresshans/godj/actions/runs/36760086296), attempt 1은
전체 성공이 아니다. 실제 plan job `110040138151`의 checkout과 full 선택은 확인했지만 Portable Go conformance의
normal `110040197877`·race `110040197846`·CGO=0 `110040197998`가 모두 실패했다. 두 attestation package의
`TestSourceBindingOwnsNativeProductDependencies`가 새 `internal/urlinput/url.go`의 source binding 누락과
`internal/urlinput` directory symlink의 은폐 가능성을 검출했다. 세 실제 job log와 run/plan 증거는
`godj-url-hosted-full-36760086296-bus0sk8f`에 보존했다. Fast feedback 성공으로 이 실패를 덮지 않는다.

두 package의 소유 prefix에 `internal/urlinput/`을 추가하고 실제 파일 변경 시 binding 변경·directory symlink 거부를
회귀 사례로 추가했다. 독립 native dependency closure 검사는 그대로 유지한다. 수정한 두 attestation package 전체를
`go test -json -count=1 -timeout=3m`으로 normal/race/CGO=0 각각 실행해 **267 run/pass, 0 skip·실패·필수 누락**을
확인했다. Go 1.26.5/darwin/arm64, `TZ=Pacific/Chatham`, 공유 Go cache와 offline/readonly 설정을 사용했으며
wall time은 각각 2.154/3.494/1.606초다. 필수 여섯 경로는 양 package의 dependency closure·URL 파일 변조·URL directory
symlink 거부다. Receipt와 전체 JSON은 `godj-url-attestation-fix-x3l_1as2`에 보존했다.
이 후속 변경은 attestation 네 파일뿐이며 URL 제품/생성기/소비자의 위 로컬 검증 source는 `a1fa31d4`로 유지한다.
수정 source를 게시한 뒤 새 Hosted full과 새 capture/source 결합을 실행하며, 결과 전까지 플랫폼 통합은 미완료다.


### 후속 source의 PostgreSQL race 작업 예산

수정 source `90b9b59fc6753092b653d87635c5bb08ad7165df`의
[PR feedback 36761888911](https://github.com/progresshans/godj/actions/runs/36761888911)은 PASS다. 실제 job `110046259094`의
merge checkout `3491a99bb6c5f587a00a32d6a97cb123e178fe48`는 source를 parent로 가지며,
tree `4d4b8c5acfe91049404d5ec04ba8a94f2a7e2813`가 source tree와 같다. Log SHA256은
`dedae709b28360bf4b6681618a000932fdd2182214df3f7e4edc5a37785001d4`이며
`godj-url-repaired-feedback-36761888911-t32e7rl5`에 보존했다.

[Hosted full 36761947384](https://github.com/progresshans/godj/actions/runs/36761947384), attempt 1의 세 Portable Go conformance는
통과했지만 PostgreSQL race/core job `110046536528`은 **40분 작업 제한**에 의해 취소됐다. GitHub annotation의
`The job has exceeded the maximum execution time of 40m0s`와 실제 종료 로그를 확인했다. 전체 작업 2,418초 중
고정 S3 service build 125초·제품 실행 2,249초였고, 종료 당시 restart test의 Go compile 자식이 남아 있었다.
이 로그로 테스트 전체 통과나 정상 service 수명 종료를 주장하지 않는다. 같은 source의 normal/core는 1,183초에 성공했고,
이전 두 race/core 성공 실행도 각각 1,893초(BigTIFF)·2,258초(File/Image choices)가 걸렸다.

Normal/CGO=0 core의 각 15 packages·4,385 run/pass·0 skip과 필수 1,920개 경로, 두 mode의 S3 build/lifecycle receipt는
확인했다. 두 새 PostgreSQL capture의 producer·archive/payload·실제 checkout·Git blob source binding도 확인했으나,
race·후속 conformance/집계까지 포함한 전체 통합 성공으로 세지 않는다. API 응답·annotation·로그·부분 audit는
`godj-url-repaired-hosted-full-36761947384-p7v_8zsp`에 보존했다.

PostgreSQL race/core의 작업 예산만 40분에서 60분으로 조정했다. 18분의 각 Go package 제한, 전체 필수 roster,
no-skip 검사·source/capture 결합·service cleanup 조건은 유지한다. 실제 작업 제한에 도달한 실패를 근거로 한 수정이며
관찰 도구 timeout 때문에 같은 실행을 다시 시작하는 조치가 아니다. 새 source의 Hosted full 결과는 별도로 통합한다.

### Workflow 주석과 선언된 matrix 검사

예산 보정 source `8184da6ae7bebea3b5f26cf503e0e62cd03cca0a`의
[PR feedback 36767491339](https://github.com/progresshans/godj/actions/runs/36767491339)은 PASS다.
실제 job `110065220249`의 merge checkout `12a2b12a19a99e1685a3a0059c23b6287a8ffda8`가 source를 parent로 가지며,
tree `d1b43f7867775eab850a880c744b56ec2c38909a`가 source tree와 같다. Log SHA256은
`202a11d995e6ee06b2a2425e90c33f47f196020fd4a4a65e7df571a1cf2d2729`다.

[Hosted full 36767526813](https://github.com/progresshans/godj/actions/runs/36767526813), attempt 1에서
`TestWorkflowRetainsDeclaredCoordinatesAndModes`가 matrix include 행 내부의 두 설명 주석을
`unsupported matrix include property`로 거부했다. 실제 relation jobs `110066000090`과 `110066000212`의
checkout/log를 보존했다. 전체 성공이 아니며 실패한 검사를 삭제하거나 완화하지 않았다.
두 주석을 job 선언 바로 위로 옮겨 좁은 workflow reader가 기존 matrix를 그대로 검사하도록 했다.
수정된 작업 사본에서 `go test ./conformance/internal/protocol -run '^TestWorkflow' -count=1`의 다섯 검사 PASS
(0.678초), CI Python 도구 45개 PASS (4.053초)를 확인했다. 이 후속 수정은 실행 예산과 matrix 값에 영향을 주지 않는다.
Full 실행과 artifact의 별도 검증 receipt는 `godj-url-budget-hosted-full-36767526813-ansg035r`,
Fast feedback receipt는 `godj-url-budget-feedback-36767491339` prefix directory에 보존한다.


### URL source의 Hosted full 완료

2026-10-01, source `e79d7795f4735ba7dbf02bb0d6b399a3475451e2`, attempt 1의
[Hosted full 36772676839](https://github.com/progresshans/godj/actions/runs/36772676839)을 완료했다.
Plan `110083395228`의 실제 checkout과 full 선택을 확인했고 **62/62 jobs success, skip 0**이다.
필수 여덟 owner(command product, conformance, exact Darwin, portable Go, PostgreSQL,
project check, Python compatibility, relation product)가 모두 성공했다.
실제 aggregate `110110865352`의 `full_platform_verified=true`와 정확한 owner 목록을 검증했다.

새 capture 두 개는 같은 run/attempt의 실제 producer checkout·성공 step·artifact ID·archive SHA256·provenance·
payload SHA256을 결합했다. 현재 dirty 파일을 사용하지 않고 위 source의 Git objects에서 다시 계산한 binding도 같다.
System-state는 682 files/7,042,587 bytes, SHA256 `65c9f630422d9ba40dd0c7e9cc9e38185b5fbf0136b4cfc626a7bb2cec008b99`,
operator는 759 files/6,895,897 bytes, SHA256 `86940f1d1f175177fd08f60159360d07ea3035fb2f9ea09000e4fce2455d248b`다.

| Capture | Producer job | Artifact | Archive SHA256 | Payload SHA256 |
|---|---|---|---|---|
| systemstate-postgres-1 | `110083452938` | `11125651795` | `41b838fe068171c1b7ae6a0dfe3134da0db9a6ecd5364bc342371022472768bb` | `5d152d0753a281e124767213d8869eab6af3ec4abd242c420de6919ae0bc8f3e` |
| operator-postgres-1 | `110083452973` | `11123899162` | `f1ec5f6ed3f30895b3fa905629d444fc0b195dc37cbc4780258a66c2bafb7bb2` | `21027b3617a6362c4e660140930232f760f07cfadd0c5621fd2b6821a1f9f117` |

Consumer `110099258242`의 실제 checkout과 두 artifact/producer ID를 확인했다.
Same-run provenance·conformance 실행, 32-bit migration/project-check/runserver compile·relation runtime,
두 oracle checksum·reference artifact 비변경 step이 모두 성공했다.
Consumer log SHA256은 `9b04e9e05976c8992edc07e92f0e869809bb05c754497f936b11da6a5779dfaf`다.

PostgreSQL core의 normal/race/CGO=0은 각각 15 packages, **4,385 run/pass, 0 skip**이며 필수 1,920 경로와
S3 23 경로를 포함한다. 세 S3 artifact에서 pinned build/module/buildinfo와 동일한 Linux/amd64 binary
`c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`, child/server exit 0·양 process reaped·
graceful cleanup을 확인했다. 이전 run의 서비스 종료 결과를 전이하지 않았다.

| Mode | Producer job | S3 artifact | Archive SHA256 |
|---|---|---|---|
| normal | `110083452938` | `11124739290` | `d12254ef41de03ad022f71848511975c5279bb32e31fcdf0a59a2877878914be` |
| race | `110083453116` | `11126027387` | `312c00ec16cb1b25b8c5fd308a7323228c789f2896d824c4a65dced23e39cd64` |
| cgo0 | `110083453052` | `11125111458` | `c149f4439e12dac8f05ea993491a997133c64f621f93aae9dc5b7aa1a47a4310` |

같은 source의 [PR feedback 36770722357](https://github.com/progresshans/godj/actions/runs/36770722357)도 성공했다.
실제 job `110076115531`의 merge checkout `9a8d92c56f090a2c07aee833e263320a698f1c5e`는 source를 parent로 갖고,
source와 tree `73e85eb03259dfd7bed5f40e8110f2fc377cc214`가 같다. Log SHA256은
`09ffc59a7c2c27ef97466be9bbdc3320f346914217ce873d98ce82d12cf69f14`다.
Full receipt는 `godj-url-comment-hosted-full-36772676839-xb84w8xi`, Fast receipt는 `godj-url-comment-feedback-36770722357-uq9x16gc`다.

별도 검증 도구의 첫 terminal audit는 consumer에 존재하지 않는 `Require a clean worktree` step 이름을 요구해 중단했다.
고정 source의 실제 선언인 `Ensure reference artifacts were not rewritten`와 32-bit/oracle 필수 step 전체로
검사 대상을 바로잡고 모두 확인했다. Workflow나 필수 실행을 바꾸지 않았으며 실패한 audit를 전체 PASS로 세지 않았다.
이 결과로 GDJ-0104의 누적 통합을 완료한다. 이후 Slug source의 Hosted 증거는 별도로 확인한다.

## GDJ-0103 — Formset과 범위가 정해진 여러 행 편집

### BigTIFF source의 Hosted web 완료

2026-10-01, source `8d89ec28b10cbba4787a439142354a6c6d8bb73d`, attempt 1의
[Hosted web 36751035636](https://github.com/progresshans/godj/actions/runs/36751035636)을 완료했다.
실제 checkout을 plan job `110009410720`의 로그와 결합했다. 전체 37 jobs 중 선택된 32 success,
비대상 5 skipped이며 `command-product-matrix`, `portable-go-matrix`, `postgresql-product` 세 필수 owner와
최종 집계 job `110022476232`가 성공했다. Summary는 `scope=web`, `full_platform_verified=false`다.

| PostgreSQL core mode | 실제 producer job | artifact | archive SHA256 |
|---|---:|---:|---|
| normal | 110009575620 | 11115416633 | `b64c6f0dc9bfe2d8317db9e900eb9bc918f28f43b69b43a48fe9908b4806513f` |
| race | 110009575561 | 11115918302 | `4d98b97d32b2bdb5ba058bc151c4dcb7800aae4bc4e12dede5cbbbfd7d5f92a4` |
| CGO=0 | 110009575408 | 11114864047 | `9acab070031f3d76bab04c7e42e27c45aecad8e8fc444101f17c7222c2ea48ae` |

세 producer 모두 15 packages·4,380 run/pass·0 skip, source roster의 필수 1,915와 S3 전용 23 test를 확인했다.
새 BigTIFF Admin create/command의 12개 경로가 포함된다. Artifact producer/attempt·capture/source·module/version/build
정보와 lifecycle receipt를 제출 당시 Git blob에 대조했다. 현재 URL 작업본이나 그 추가 roster에서 요구를 재구성하지 않았다.
MinIO commit `9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a`, Linux binary SHA256
`c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`이며 각 mode에서 ready,
child exit 0·종료 전 server 생존·server exit 0·두 process reap·graceful cleanup을 모두 확인했다.
로컬 source audit는 `godj-bigtiff-hosted-web-36751035636-xn9jdm2x`에 보존했다.

[PR fast feedback 36751018567](https://github.com/progresshans/godj/actions/runs/36751018567)도 성공했다.
실제 job `110009347301`의 merge checkout `9d62b16fff7715e18c50e1e47bbdc1f3fef88814`는 해당 source를 parent로 가지며,
Git tree `fe29405426297379439f643b950c40541fa04776`이 source tree와 같다. 로그 SHA256은
`00e5f584d58c734622ac7b5ff422352824d0b69bb461a8f2b18c46a747250da7`이다.
Receipt는 `godj-bigtiff-feedback-36751018567-ew7gw3wz`에 보존했다.
이 두 결과는 이후 URLField 작업본의 검증이 아니며 최근 전체 플랫폼 source는 여전히 `bcc7b76a`다.

### BigTIFF의 전체 페이지 검사와 파일 소비자

2026-10-01, 기반 `e824fdd74b72cde17452bf502e5a7a4b6c4eec09` 이후 BigTIFF의 little/big endian·64-bit IFD와 값/offset을
기존 bounded 이미지 검사에 연결했다. 비Markdown code/config inventory는 2,833 files /
`4c51d4cd6d4c7d1caa1a2eb8349ae967e3d3cb5ad87a4e66e515d40adcedcea7`이며 normal/race와 후속 검사의 source가 같다.
이 변경은 아래 File/Image choices의 Hosted source 이후다. 새 소스의 Hosted web 결과는 바로 위 source별 완료 기록을 따른다.

| 실행 | 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1; uploads·forms·forms/model·storage·storage/model·admin의 Image/TIFF 선택, 6 packages / 49 roots / 433 PASS / 2.152초 |
| 생성 소비자 normal | FileConsumer parent 1 PASS / 8.747초; SQLite/PostgreSQL × filesystem/memory/S3, BigTIFF 4개 사례와 기존 파일·이미지·choices·서빙 회귀 |
| 관련 race | 같은 Image/TIFF 범위, 6 packages / 49 roots / 433 PASS / 3.911초 |
| 생성 소비자 race | 부모·생성 child 모두 race, 같은 양 DB/세 backend / parent 1 PASS / 16.238초 |
| Native reference | 고정 Django 6.1 `fe0a859f537d4238cf49fca39073513206f83122` / Python 3.14.3 / Pillow 12.3.0 / LibTIFF 4.7.1, 18개 사례를 독립 2회 실행해 fixture byte 일치 |
| Fuzz | `go test -json -run '^$' -fuzz '^FuzzInspectImage$' -fuzztime=30s -parallel=4 ./uploads`, seed/cache baseline 430개·3,519,173 executions·신규 interesting 46개, PASS |
| 실패 대조 | 후속 페이지 생략·64-bit offset 상위 bit 제거·압축 블록 참조 byte 예산 제거·tile padding 예산 제거, 원본 선택자 PASS 뒤 네 runtime 실패 검출 |
| 생성 drift/정적 검사 | `make generate-check`의 Unicode·일곱 저장 프로젝트·relation consumer, 영향 7 packages vet, `make docs-check format-check`의 171문서 링크/포맷과 `git diff --check` PASS |

[독립 observer](../../conformance/runners/django/bigtiff_reference.py)는 `tiffcp -8`로 16개 정상 파일과 후속 픽셀/디렉터리가
손상된 2개 파일을 만들었다. LE/BE·raw/LZW/Deflate/PackBits·gray16/RGBA/palette·Group 3/4·다중 strip/tile/page를 포함하고
JPEG/CMYK의 현재 명시적 미지원도 유지한다. Fixture SHA256은
`d7a43569d3770d8be19ea076e102498e0e64a1c529c3eb76901b3a90dff562e0`다. 두 도구의 버전·binary SHA와 Django/Pillow source
SHA를 fixture에 남겼다. 모든 정상 페이지를 LibTIFF로 읽고 classic 변환 후 실제 픽셀을 검사하며 폼의 첫 이미지 승인과 구분한다.
후속 directory 손상에서 `tiffcp`가 첫 페이지만 내보내고 exit 0을 반환했지만 `tiffinfo -D`가 실패하므로 정상으로 취급하지 않는다.

Big-endian BigTIFF를 고정 Pillow 폼이 잘못 식별해 거부하는 차이를 기록한다. 압축 저장에서 `big_tiff=True`가 classic TIFF를
만드는 것도 직접 확인해 생성기를 `tiffcp -8`로 정했다. 최초 픽셀 대조는 palette를 8-bit로 낮춘 뒤 다시 16-bit로 늘리는 관찰
오류가 있어 원래 TIFF의 16-bit ColorMap으로 수정했다. Group 3/4 BlackIsZero의 두 사례에서는 고정 x/image가 LibTIFF와
반대의 fax-run 색을 반환했다. 독립 LibTIFF가 만든 classic CCITT에서도 같은 디코더 결과를 확인하고 BigTIFF 뷰와 픽셀 일치를
검사한다. 이 알려진 색 해석 차이는 테스트에 명시하며, GoDj 검사는 픽셀을 노출하거나 재인코딩하지 않고 메타데이터와 원문만
사용한다. 따라서 저장 내용이나 반환 치수가 변경되는 문제로 표현하지 않으며 렌더러의 색 동등성도 주장하지 않는다.

공통 preflight는 unsigned 64-bit 개수·offset·width/height의 overflow/절삭, 양 byte order의 순환·겹침·header 참조·잘린 값,
SubIFD/분리 plane·후속 압축 오류를 검사한다. LONG8 배열의 부분 ReadAt·EOF/취소·동시 독립 읽기와 원문 보존을 확인했다.
반복 LZW strip은 파일 크기보다 큰 참조 작업량을 만들며 `MaxBytes` 경계에서 거부/승인을 대조한다. Tile padding과 뒤 페이지의
개별/합산 pixel/frame 예산도 포함한다. 기존 classic TIFF·다른 이미지 형식의 선택 회귀를 같은 checkpoint에서 실행했다.

생성 Photograph의 BigTIFF 4개 사례 × 양 DB × 세 backend = 필수 24개 조합에서 업로드→typed 준비→게시→DB 저장→재조회·
DB 재개방→명시적 저장 이미지 검사와 저장 이름 choices를 확인했다. 같은 부모가 기존 rollback/실패 소비자도 실행한다.
후속 pixel 손상은 valid model/게시 준비로 진행하지 않는다. Admin의 생성/명령 × 세 backend × 새 2개 variant는 실제 로그인·
CSRF·multipart·내용 오류/재선택·요청 임시 정리와 원문 게시를 확인한다. 새 decoder dependency나 생성 ABI 변경은 없다.

전체 부모 JSON·필수 하위 경로·0 skip/0 missing을 확인했다. Normal/race는 각각 새 PostgreSQL 17.10 Debian container·
UTF8/libc/C/C·독립 DB/port와 고정 MinIO process를 사용했다. DB 최종 상태 `0|0|0`·DB/container 제거, S3 child/server exit 0·
양 process reap·graceful cleanup을 확인했다. Source/binary 결합은 기존 service wrapper가 검사한다. 로컬 전체/cold 검증은
중복하지 않았다. 첫 시도의 테스트 컴파일 오류, 최초 독립 대조의 위 네 assertion 실패는 최종 PASS에 합치지 않는다.

상세는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래 `godj-bigtiff-db-normal-bz3el1gp`,
`godj-bigtiff-db-race-yak5trmk`, `godj-bigtiff-service-normal-ctbm0c2b`, `godj-bigtiff-service-race-y_ftf5qp`,
`godj-bigtiff-controls-dhix5evo`, `godj-bigtiff-final-checks-b6h8wgnn`에 보존한다. TIFF의 나머지 codec 특성·추가 provider와
전체 기능 카탈로그는 계속 미완료다.

### File/Image choices의 저장 이름 선택과 명시적 검사

2026-10-01, 기반 `ac4c40d5061dd427543a33fd08d0dc71b0b1159a` 이후 File/Image choices를 canonical IR·Form/Admin·
typed 준비에 연결했다. Runtime checkpoint의 비Markdown code/config inventory는 2,829 files /
`9ffe85c86ed6a7a1635df6e5cec3c6fa8be9634a5c328889cb1a7979f84434d5`이며 normal/race의 실행 전후가 같다.
선행 S3 Hosted web은 이 choices 변경의 검증이 아니다. 후속 source의 Hosted web 결과는 아래에 별도로 기록한다.

| 실행 | 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1; forms·forms/model·admin·storage·storage/model·schema/ir·schema·serializers·projectwire·migrationautodetect·migration definition, 11 packages / 535 roots / 5,319 PASS / 5.064초 |
| 생성 소비자 normal | FileConsumer parent 1 PASS / 8.774초; SQLite/PostgreSQL × filesystem/memory/S3의 저장 이름 선택·원문 검사·typed 저장/rollback/재개방과 기존 파일/이미지/서빙 회귀 |
| 관련 race | storage·storage/model·forms·forms/model·admin의 Choice/File/Image/Set/Inline, 5 packages / 114 roots / 618 PASS / 3.666초 |
| 생성 소비자 race | 부모·생성 child 모두 race, 같은 양 DB/세 backend / parent 1 PASS / 23.882초 |
| Native reference | 고정 Django 6.1 `fe0a859f537d4238cf49fca39073513206f83122` / Python 3.14.3 / Pillow 12.3.0; 20개 입력과 이미지 선택 후 DB rollback을 독립 2회 실행, fixture byte 일치 |
| 실패 대조 | 선언 목록·명시적 검사·canonical 목록 확대 금지·생략 default·치수 파생·default 준비의 I/O 분리·Admin 업로드 part 차단·서로 다른 검사 결과의 동일 이름 unique, 여덟 runtime 실패 검출 |
| 생성 drift | `make generate-check`: Unicode 검사와 일곱 저장 프로젝트·checked-in relation consumer PASS |
| 정적 검사 | 영향 forms/model·Admin·storage·schema·생성 소비자 vet, `make docs-check format-check`, `git diff --check` PASS |

Native fixture SHA256은 `3afcd797bc6d7ccc0f062dc74fba007f04abe7bba8abbcfefa01555872b01689`다. 관찰 프로그램은 GoDj를
사용하지 않고 Django 소스 hash/commit·라이선스를 남긴다. Select/비multipart·정확한 이름·empty/null/default·upload part 무시·
이미지 새 선택/같은 선택/없는 파일과 실제 rollback을 비교한다. Native가 빈 nullable 선택에서 현재 이미지를 다시 읽는 부분은
GoDj의 현재 이름/치수 보존·I/O 생략과 명시적으로 구분한다. 전체 내용 검사는 기존 저장 이미지 정책을 사용한다.
이후 observer의 라이선스 위치 주석만 repository root로 바로잡고 두 번 다시 실행해 같은 fixture를 확인했다. Runtime 제품·
생성기·Go test·fixture bytes는 바꾸지 않았다.

생성 소비자는 두 독립 모델과 복합 unique를 추가하고 output을 두 번 생성해 byte 일치를 확인한다. File/Image choices의
정의 wire·metadata-only migration·reverse·DB 재개방, 저장 이름의 준비와 새 게시 0회, 이미지 치수의 model clean 이전 반영,
DB rollback 중 원래/선택 파일 보존과 DB에서 다시 얻은 선택의 실제 HTTP 응답을 확인한다. 이 새 응답 사례 자체는 별도 로그인
통합을 주장하지 않는다. 기존 생성 serving suite가 로그인·CSRF·소유권/Range/서명을 소유하고, 새 Admin HTTP 검사가 실제
cookie login·create/change Select·초기 선택/escaping·위조 치수·중복/미선언 선택·CSRF·조회 전용 principal·multipart part 거부·
검사 실패의 500과 미쓰기를 검증한다. 순수 Form은 context/limits·동시 재검사·검증 순서·readonly/빈 입력의 I/O 생략을 확인한다.

필수 실행·전체 부모 Go JSON·no-skip을 확인했다. 생성 부모는 child JSON 전체와 양 DB/세 backend의 reference_choices 및
formset_unique 필수 하위 경로를 검사한다. 각 normal/race는 PostgreSQL 17.10 Debian·UTF8/libc/C/C의 새 container/database와
새 고정 MinIO process를 사용했다. DB 종료 상태 `0|0|0`·DB/container 제거와 MinIO child/server exit 0·reap·graceful cleanup을
확인했다. 공유 Go cache와 child `-trimpath`를 유지하고 로컬 전체/cold 검증은 반복하지 않았다.

첫 실행에서 image default 준비가 폼 inspector를 요구하는 결합을 발견해 순수 참조 기본값 준비를 분리했다. Admin 중복 입력의
기존 `multiple` 재표시를 HTTP 400으로 기대했던 테스트도 현재 오류 정책에 맞게 수정했다. 첫 core 실패를 최종 PASS로 합치지
않는다. 모든 실패 대조는 원본이 같은 선택자로 먼저 통과하고 목표 runtime assertion으로 실패했다. 생성 child가 host overlay를
거부하므로 unique 대조는 소유한 source 복사본에서 수행했으며 실제 작업 사본은 바꾸지 않았다.

로컬 상세는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래 `godj-file-choices-db-normal-0520q418`,
`godj-file-choices-db-race-1gwiygm6`, `godj-file-choices-service-normal-ltvohu8r`, `godj-file-choices-service-race-35uu9ve6`,
`godj-file-choices-controls-no0hr63b`에 보존한다. 사용법/장기 의미는 모델 Form 문서와 ADR-0082에 둔다. 추가 codec/provider·
전체 파일 및 기능 카탈로그 범위는 완료로 표현하지 않는다.

게시 source `e824fdd74b72cde17452bf502e5a7a4b6c4eec09`의
[PR feedback 36744894940](https://github.com/progresshans/godj/actions/runs/36744894940)은 PASS다. 실제 merge checkout
`ebf663fe115c286ad0cf6bd82a24aff755263803`의 tree `2b6b5743bc5fd8c377ea869bb59710e1e7e91b7b`가 source와 일치한다.
[Hosted web 36744929499](https://github.com/progresshans/godj/actions/runs/36744929499), attempt 1도 PASS다. 총 37 jobs 중
필수 32 success·비대상 5 skip, plan `109988588777`과 최종 집계 `110004033413`을 확인했다. 실제 checkout과 `web` 선택을
대조하고 `command-product-matrix`, `portable-go-matrix`, `postgresql-product` 세 owner를 검증했다.
`full_platform_verified=false`이며 선행 full이나 이후 BigTIFF source의 검증으로 바꾸지 않는다.

세 PostgreSQL core는 각각 15 packages / 4,368 runs·passes / 0 skip과 source의 필수 1,915개 경로를 충족했다. FileConsumer
부모는 choices의 양 DB/세 backend·formset_unique 하위 실행을 강제한다. S3 필수 23개와 세 build/lifecycle artifact도 source에
결합했다. Linux binary SHA256 `c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`, 고정 module/commit·
checksum·Go 1.26.5와 각 ready·child/server exit 0·child 뒤 server alive·양 process reap·graceful cleanup을 확인했다.

| S3 mode | 실제 producer job | Artifact | Archive SHA256 |
| --- | --- | --- | --- |
| normal | `109988661897` | `11112788099` | `26748cf7ab505fbe778c906c80adaff81f955221774a015e3d0567a4d1ddaf60` |
| race | `109988661800` | `11113309332` | `b269a172b73c9217ec9c30d192b6da4cf9229e4c7735ac32dd9669e0c073ebbb` |
| CGO=0 | `109988661757` | `11112951711` | `f12df77f8e830627dd898fa49d7c9aee93c86a496cc49b3181157732fa8208f7` |

API 응답·실제 job log·artifact ID/archive digest·receipt와 최종 scope 결과는 위 임시 경로 아래
`godj-file-choices-hosted-web-36744929499-43l8ppqy`, `godj-file-choices-feedback-36744894940-dywltnce`에 보존한다.

### S3의 게시 결과·독립 reader·서명 다운로드

2026-09-30, 기반 `bcc7b76a17aacc6a90a3f360bb8f25fe080a840d` 이후 명시적 S3 backend와 서명 다운로드를 기존 파일
경계에 연결했다. 제품 구현 checkpoint의 비Markdown code/config inventory는
`e330cc705e260d9cdf1154787fc514cb5715cfe83281f2de2b390c2b977fb78f` (2,823 files)다. 최종 normal/race의 전후 source가
일치한다. 공식 SDK dependency를 추가했으며 IR·생성기·모델 선언은 바꾸지 않았다. 생성 소비자가 새 module에서 같은
프로젝트를 두 번 생성해 byte 일치와 실제 사용을 확인했다. 추가 generated drift·로컬 전체/cold 검증은 중복하지 않았다.

| 실행 | 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1; uploads·storage·storage/model·forms·forms/model·admin·web 7 packages / 390 roots / 3,135 PASS / 4.741초 |
| 생성 소비자 normal | FileConsumer parent 1 PASS / 8.510초; SQLite/PostgreSQL × filesystem/memory/S3의 파일·이미지·Formset·인가된 HTTP 소비자 |
| 관련 race | storage·storage/model·forms·forms/model·admin·web 6 packages / 93 roots / 504 PASS / 7.821초 |
| 생성 소비자 race | 부모와 생성 child 모두 race, 같은 양 DB/세 backend / parent 1 PASS / 13.715초 |
| 실제 동시 저장 반복 | 2개 독립 S3 backend·16 inputs × 50회, race; 778개 confirmed·22개 Uncertain과 실제 778개 객체를 대조, 숨은 중복/덮어쓰기/보상 삭제 없음 |
| 부정 대조 | 조건부 PUT·전체 checksum·Uncertain·EOF 전 게시 금지·version Range·정확한 길이·service identity·UTF-8 byte 한도·서명 attachment·SDK 무재시도·시도별 cursor·Close 이후 연결 회수의 12개 overlay를 지정 runtime assertion으로 검출 |
| 검증 도구 | CI Python 45 tests, storage/s3fixture vet, 포맷·문서 링크·diff 검사 PASS; required S3 환경 누락은 실제 Go test 실패, child exit 23도 그대로 반환하면서 실제 server 종료·data 회수 확인 |

필수 실행·전체 Go JSON과 no-skip을 확인했다. Normal/race는 각각 PostgreSQL 17.10 Debian의 새 container·UTF8/libc/C/C·
독립 port/database와 새 MinIO process를 사용했다. DB 종료 뒤 schema/public table/다른 session `0|0|0`, DB/container 제거를
확인했다. S3 fixture는 자신의 bucket/version/delete marker만 제거하며 wrapper는 child exit 0·server alive/exit 0·reap과
소유 data directory 제거를 기록했다. 공유 Go cache를 유지하고 test-result cache를 끄며 cloud account를 사용하지 않았다.

S3 wire 검사는 source/entropy/cancellation·잘못된 Read count·context·quota·Close 대기, 조건부 충돌과 409/403/500·응답 유실·
잘못된 2xx checksum·redirect 금지, 단일 GET metadata·누락/손상/길이·version/range 재결합 거부·막힌 Read 취소를 포함한다.
실제 서버는 빈 파일·독립 인스턴스의 동시 게시·SHA256 거부·교체/삭제/Close 뒤 원래 version의 읽기, unversioned의 seek 미제공,
서명 변조·PUT 전환·실제 만료·인증 거부와 게시 이후 응답 유실의 원본 보존을 확인했다. Backend Close 뒤 reader가 새 GET을
열어도 reader Close가 그 owned idle connection을 회수하며, 각 PUT 후보는 독립 cursor/context 수명을 가진다.

실제 생성 Photograph는 양 DB/세 backend × 13 codecs의 필수 78개 조합에서 준비·게시·typed 저장·원문/크기 재검사·rollback과
DB 재개방을 확인한다. Cookie login·CSRF·모델 소유권·Range/conditional과 S3 서명 발급/실제 GET도 같은 소비자다. 소유권을
바꾸면 새 URL 발급은 거부하지만 기존 bearer URL은 유효기간까지 사용할 수 있음을 별도로 확인했다. Admin은 생성/명령 ×
세 backend × 8 variants의 48개 조합에서 실제 multipart·인가·검증·원문 게시·실패 재표시와 임시 자원 정리를 실행했다.

독립 reference binary는 MinIO `9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a` / RELEASE.2025-10-15T17-29-55Z다.
별도 Go module의 pinned checksum·go.mod/go.sum hash와 `go mod verify`를 확인하고 Go 1.26.5·CGO=0으로 빌드했다.
Darwin/arm64 binary SHA256은 `bfba176b4281e6f7f6198a1c11e19cfbd5af37bef473cd226f4471067ae9cca6`다.
GoDj를 import하지 않는 공식 SDK observer도 conditional 412·full checksum·version/range·실제 서명 GET을 관찰했다.

첫 묶음은 SDK가 owner query를 소문자로 서명하는 점과 고정 MinIO가 checksum 거부에 `XAmzContentChecksumMismatch`를
쓰는 점을 테스트가 잘못 가정해 실패했다. 공개 코드와 실제 거부/미게시를 확인해 고쳤다. 후속 동시 검사에서는 idle connection
종료가 발생했다. GoDj 없는 공식 SDK 480회도 472개 성공·EOF/closed-idle 8개를 보였으므로 자동 retry로 숨기지 않았다.
실제 conformance는 성공한 모든 내용/receipt와 불확실한 후보를 native inventory·각 객체의 단일 version에 대조한다. Protocol
거부 자체의 독립 probe만 fresh connection을 사용하고 제품의 동시 요청은 기본 pool을 사용한다. 별도 응답 유실 사례는 서버가
게시한 뒤에도 결과가 Uncertain이고 데이터가 보존되는지 검사한다. 서명 만료 검사는 timestamp의 초 정밀도를 고려해 2초 TTL과
3.1초 대기를 사용한다. 처음 종료-only overlay는 목표 cursor 공유 결함을 만들지 못했으며, 실제 cursor 공유·연결 회수 제거
overlay로 해당 경계를 검증했다. 이 초기 실패/대조를 최종 PASS에 포함하지 않는다.

CI는 기존 PostgreSQL product core owner의 normal/race/cgo0에 새 MinIO process·필수 live 하위 사례·Admin·양 DB 생성 소비자를
연결했다. Build/lifecycle receipts도 별도 artifact로 남긴다. 게시한 새 source의 Hosted web 통합이 후속 검증 범위이며,
선행 이미지 source의 Hosted full을 이 변경에 전이하지 않는다. AWS 운영 account·다른 S3 구현·추가 provider·multipart 저장·
자동 version/orphan 회수와 전체 기능 카탈로그는 미완료다.

로컬 상세는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래 `godj-s3-db-normal-_nxymzlo`,
`godj-s3-db-race-7r2i9pd2`, `godj-s3-checkpoint-0s7n2bqt`, `godj-s3-negative-8bpwdvq9`,
`godj-s3-lifecycle-controls-ad6risuz`에 남긴다. 부정 대조의 제품/선택 테스트 source는 최종 소스와 같으며, 이후 변경은
live 서명 만료 사례의 위 timing 보정뿐이다.

게시 source `e3cb1a0ffa833092290ea7f00ee2875632637b58`의 [PR feedback 36731197891](https://github.com/progresshans/godj/actions/runs/36731197891)은
통과했고 실제 merge checkout `7959ee03095c718d74df7a918493db007c3b26d5`의 tree
`4f6c278307aa2211c5ed7d9a17b8126ddd759c6f`가 source와 일치한다. 첫 [Hosted web 36731449591](https://github.com/progresshans/godj/actions/runs/36731449591)의
새 S3 service build는 통과했지만 여러 owner가 clean-worktree gate에서 실패했다. 확인한 command/portable/operator 제품
실행은 통과했고, `go mod download all`이 tidy에서 제거된 기존 checksum 7줄을 복구한 것이 공통 원인이었다. 이 run을
Hosted 통합 PASS로 기록하지 않는다. 수정 source의 새 실행이 시작되면서 남은 작업은 취소됐고 최종 conclusion은
`cancelled`다. 세 S3 core 작업의 실제 제품 실행도 취소됐으므로 service build의 성공을 제품 PASS로 사용하지 않는다.

`go.sum`만 전체 graph 준비 결과로 복구했다. Blob은 `cf0374450c58065e5b15cb4e5c5dd2446b885519`, SHA256은
`7f8b2a7638acafa9b1d5a641c7091732e218d44109cc762ff3a2fdb62aab41fc`이며 실제 CI의 추가 diff와 일치한다.
`go mod download all`을 두 번 더 실행해 byte 무변경을 확인하고 `go mod verify`도 통과했다. 제품/생성기/선택한 dependency
version은 바꾸지 않았으며 해당 부분의 로컬 제품 검사를 반복하지 않는다. 이 checksum 수정 뒤의 Hosted web을 새 source로
검증한다. 수정 실행의 receipt 위치는 `/tmp/godj-s3-module-graph-repair-path`가 가리키는 소유 임시 디렉터리다.

2026-10-01, 수정 source `3965ad02cb75bd2f128aba86920b7a09dc21610f`의
[PR feedback 36733079080](https://github.com/progresshans/godj/actions/runs/36733079080)은 통과했다. 실제 merge checkout
`576df90b5102fbe662f5c54023208f27a0dae854`의 tree `dc36ba8f58cb19c9dedf30709929d86ad7f74b7b`가 source와 일치한다.
같은 source의 [Hosted web 36733177546](https://github.com/progresshans/godj/actions/runs/36733177546)은 30 success / 2 failure /
5 비대상 skip으로 끝났다. Plan의 실제 checkout·`web` 범위와 필수 command/portable/PostgreSQL 세 owner를 확인했다.
S3를 실행하는 PostgreSQL core는 normal/race/CGO=0 모두 성공했지만 portable integration race와 최종 집계가 실패했으므로
전체 web 통합 PASS가 아니다.

세 S3 core 작업은 각각 15 packages / 4,368 runs·passes / 0 skip이며 공통 필수 목록 1,915개를 충족했다. 목록에는 실제 S3
부모/하위 7개, Admin S3 이미지의 16개 조합과 생성 FileConsumer 부모가 포함된다. 실행 source의 필수 목록과 검사기를 확인하고
실제 job checkout·제품 실행·clean worktree·서비스/DB 종료 step의 성공을 확인했다.

| S3 mode | 실제 producer job | Artifact | Archive SHA256 |
| --- | --- | --- | --- |
| normal | `109949061246` | `11107396536` | `9a41dbb45ef39395138b8ffaa3234cf8577a0b49f31c147c88d19677d7b612c1` |
| race | `109949061106` | `11108055274` | `f45ed5ed5b4a4bedf9b894dbb9359a4c7986971624910b11fd13234c89ae197b` |
| CGO=0 | `109949061419` | `11107735451` | `0c17e700d9c65c74a50f32ecc0226c160e6ae8fba0698cc3b4d88ff43ccf287e` |

Artifact의 run/head·실제 producer log의 artifact ID와 archive digest를 대조했다. `build.json`의 module/commit·dependency
checksum·Go 1.26.5, `buildinfo.txt` hash와 Linux/amd64/CGO=0을 확인했다. 세 독립 빌드의 binary SHA256은 모두
`c47d14d5b232424962e46715ab6c1656217e298f64058141199b39e7b565fe59`다. 각 `run.json`은 같은 binary·ready·child exit 0·
child 종료 후 server alive·server exit 0·양 process reap·graceful cleanup을 기록한다. 제품 테스트와 source가 다른 서버
receipt를 섞지 않았다. 이 상세는 임시 디렉터리 `godj-s3-hosted-web-36733177546-ce7pljo5`에 보존한다.

실패 job `109949062019`에서는 Helpdesk SQLite 소비자 전체가 공유한 2분 context가 만료됐다. 부모 테스트는 120.31초에
실패했고 `admin_inlines/readonly_delete`의 조회부터 후속 초기화와 ticket editor가 같은 `context deadline exceeded`를
받았다. Race detector 경고나 다른 제품 assertion 실패는 없었지만 후반 사례는 완료되지 않았다. Web scope의 portable
integration은 생성 소비자를 포함한 여러 package를 병렬 실행하며, 이 job의 생성 소비자 package도 1,383.739초 실행했다.
공유 CPU 부하의 기여는 추정이며 확인된 직접 원인은 소비자 전체의 만료된 context다.

SQLite 소비자의 누적 예산을 test 수명에 결합한 5분으로 조정했다. 제품 code·scenario·assertion·package 병렬 실행과 개별
취소/경합 검사의 별도 deadline은 바꾸지 않았다. 수정 후 Go 1.26.5 / darwin-arm64 / CGO=1에서 해당 부모 전체를 normal과
race로 실행해 각각 142개 run/pass·0 skip과 필수 68개를 확인했다. Package 실행은 각각 6.619초/72.709초, compile을 포함한
명령 전체는 42.113초/127.643초다. Admin의 `readonly_delete`·unknown rollback/commit과 ticket editor/collection의 실제
취소·후반 저장 검사가 모두 완료됐다. 비Markdown inventory `4fa05601be33b1996eb8b6ae7f4e19cadc72b2db2d51a12942da9c32804a5f68`는
두 실행의 전후에 같았다. 로컬 상세는 `godj-helpdesk-budget-normal-gyrks930`, `godj-helpdesk-budget-race-2qc23c3g`다.
새 source의 Hosted web으로 최종 통합을 다시 확인한다. 앞선 세 S3 component의 성공을 새 source 전체의 PASS로 사용하지 않는다.

2026-10-01, 수정 source `ac4c40d5061dd427543a33fd08d0dc71b0b1159a`의
[Hosted web 36738621534](https://github.com/progresshans/godj/actions/runs/36738621534), attempt 1이 완료됐다.
37 jobs 중 32 success·5 비대상 skip이며 필수 command-product-matrix·portable-go-matrix·postgresql-product와
최종 집계 job `109981630637`이 통과했다. Plan과 집계의 실제 checkout/선택 범위를 확인했고 결과는 `scope=web`,
`full_platform_verified=false`다. 새 전체 플랫폼 성공으로 표현하지 않는다. 앞선 Helpdesk portable integration race도
이 source에서 완료됐다. [PR feedback 36738560435](https://github.com/progresshans/godj/actions/runs/36738560435)도 통과했으며,
실제 merge `bfe2baa6d31e6f9512cf1d6073e26c617b7f3b99`의 tree `18275ffd25d87e4f6eed14e242be0022eba36d49`가 source와 일치한다.

| 새 source의 S3 mode | 실제 producer job | Artifact | Archive SHA256 |
| --- | --- | --- | --- |
| normal | `109966888903` | `11109444328` | `2a28fa9b6b11f32ecae0337004490a5eba99aef771cefec412c792338e9aff51` |
| race | `109966888869` | `11111291860` | `46724507b5bbc626f76744b1bb1ca2b27fc185e4e1bfd9b82ffd17dcc1b042d0` |
| CGO=0 | `109966888798` | `11109249377` | `b3ddb62a15fcdff055c4408378bbfa71bdba4b2d665df9690e1b2c0a752c8e68` |

각 producer에서 15 packages·4,368 runs/passes·0 skip과 필수 1,915개를 새로 확인했다. Git source의 실행 목록·
job checkout·실제 artifact 발행 ID/metadata/digest·고정 server build와 독립 process 수명을 다시 대조했다. 세 server binary는
위 Linux SHA256과 같으며 각 receipt의 ready·child/server exit 0·reap·graceful cleanup이 성립한다. 상세는
`godj-s3-budget-hosted-web-36738621534-rwic3c9y`에 보존한다. 이후 편집한 파일 choices는 이 Hosted 실행에 포함되지 않는다.

### 저장 이미지·추가 codec의 Hosted 통합

2026-09-30, [Hosted full 36711704536](https://github.com/progresshans/godj/actions/runs/36711704536), source
`bcc7b76a17aacc6a90a3f360bb8f25fe080a840d`의 62개 job과 8개 필수 owner·최종 `full_platform_verified=true`를 확인했다.
저장 이미지 검사·BMP/DIB/TIFF·APNG/WebP까지 포함하며 이후 S3 변경은 포함하지 않는다. 로컬 전체는 중복하지 않았다.

Attempt 1은 Intel macOS relation race job이 runner 획득을 5회 실패해 테스트 시작 전 실패했고 aggregate도 실패했다.
GitHub annotation과 빈 steps를 확인하고 실패 job만 같은 source로 재실행했다. Attempt 2에서 해당 실제 race와 최종 집계가
통과했다. 성공한 선행 job을 새 실행으로 기록하지 않는다. Aggregate job은 `109929167362`다.

두 실제 PostgreSQL producer의 attempt 1 artifact를 resolver가 선택했다. Producer job/run/attempt·checkout과 archive digest,
`provenance.json`·`SHA256SUMS`·payload envelope를 검증했으며 실행 source의 Git blobs에서 각 source binding을 재계산했다.
현재 S3 작업 사본의 파일이나 다른 run의 capture로 대체하지 않았다. Hosted conformance owner의 실제 capture 소비도 성공했다.

| Capture | 실제 producer / artifact | Payload SHA256 | Git source binding |
| --- | --- | --- | --- |
| systemstate | `109874860795` / `11095442083` | `0fd2dda17e50d24ec900d54eb499f993aad9d01e2bfd0cfd6d87ee49e9c74b8d` | 674 files / 6,981,278 bytes / `33c974c47ef9147fefc4e4476ff388eee1c7e471fda9a0135030ea64e7532216` |
| operator | `109874860804` / `11094239313` | `1788e9b7140b9695aa4979be6f6e2d166ad2ee09591074ce93cedd7d215e9ea7` | 751 files / 6,834,385 bytes / `6b0b9e6184f85f5f077ce06bcf1553ccb5e247e0268f77249c5b7cdbae4bee0c` |

로컬 보존 기록은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-webp-integration-full-36711704536-hr7kzquc`다.

### WebP의 실제 크기와 전체 frame 검사

2026-09-30, 기반 `15b33afe9174ded7e45f749edac0b6816f85f96e` 이후 WebP의 RIFF·VP8X·ANIM/ANMF·프레임별 alpha와
실제 bitstream 크기를 같은 업로드/저장 검사에 연결했다. 최종 비Markdown code/config inventory는
`e828dda4b9e325b31754dc6810c8991c039f55d734074b34745207765add444b` (2,814 files)다. 아래 normal/race/fuzz의 전후
source가 일치하며 이후에는 문서만 정리했다. 이전 source의 전체 플랫폼 성공을 전이하지 않는다.

| 실행 | 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1; uploads·storage·storage/model·forms·forms/model·admin·web 7 packages / 372 roots / 3,027 PASS / 2.860초 |
| 실제 생성 소비자 normal | FileConsumer parent 1 PASS / 8.633초; SQLite/PostgreSQL × filesystem/memory × 13 codec 사례의 필수 52개, WebP 4개 형식의 새 16개 조합 포함 |
| 관련 race | uploads·storage·storage/model·forms·admin 5 packages / 43 roots / 457 PASS / 3.595초 |
| 실제 생성 소비자 race | 부모와 생성 child 모두 race, 같은 양 DB/양 backend의 필수 흐름 / parent 1 PASS / 20.212초 |
| Fuzz | `FuzzInspectImage`, 기존 corpus와 새 WebP seed 392개 baseline, 4 workers·20초 요청 / 806,295회 / Go test 21.887초·명령 전체 24.049초 / PASS |
| 부정 대조 | 전체 frame·개수/합산 pixel·영역/좌표 단위·실제 bitstream header·선언 크기 일치·raw alpha 길이·RIFF 경계/zero padding·reader 취소·프레임별 alpha flag의 12개 overlay를 지정 runtime assertion으로 검출; 각 원본을 같은 선택자로 먼저 PASS 확인 |

필수 실행의 완료와 전체 Go JSON을 확인했으며 누락·skip·잘린 출력은 없다. Normal/race는 별도 PostgreSQL 17.10 Debian
container, UTF8/libc/C/C·독립 port/database를 사용했다. 종료 뒤 schema·public table·다른 session `0|0|0`, DB/container
제거를 확인했다. 공유 build cache를 유지하고 test-result cache는 껐다.

기존 생성 Photograph의 WebP lossy/lossless·compressed alpha·mixed 입력을 모델 Form 준비/게시·typed DB 저장·재개방과
저장 이미지 크기 재검사에 연결했다. 3×2 canvas 크기·전체 frame 수·저장 이름·원문을 보존하고 손상된 후속 frame은 typed
준비에서 거부한다. 기존 파일/이미지·Formset·인가된 Range/conditional·rollback 회귀도 같은 소비자로 실행했다.
Admin의 생성/명령 × 양 backend × 8 codec 변형의 32개 조합에서 로그인/CSRF·multipart·오류/재선택·후속 pixel/alpha 거부·
쓰기 callback·임시 파일 정리·원문 게시를 확인했다. Formset은 확장자 대소문자/내용 불일치·내용 우선 오류·validator 횟수와
frame/합산 pixel 한도를 검증했다. 새 화면/재생 API는 없으며 새 브라우저 시각 검증은 실행하지 않았다.

Native는 고정 Django 6.1 / CPython 3.14.3 / Pillow 12.3.0 / libwebp 1.6.0에서
[WebP observer](../../conformance/runners/django/webp_animation_reference.py)를 두 번 실행하고 저장 fixture와 byte 일치를
확인했다. 61개 중 폼 결과가 같은 44개(유효 26개·거부 18개), GoDj가 더 엄격히 거부하는 14개, 공개 container가 무시하도록
정한 VP8X reserved/future field를 GoDj가 허용하지만 native가 거부하는 3개를 구분한다. 공통 유효 중 ALPH reserved bits는
native 폼이 허용하지만 별도 player가 첫 frame 뒤 `OSError`로 실패한다. GoDj는 모든 frame을 검사한다. 정적 lossy alpha flag와
실제 ALPH가 모순되는 두 입력은 native가 허용하지만 고정 Go decoder는 거부하며 이 차이를 유지한다. 전체 Pillow 재생 동등성을
주장하지 않는다. Fixture는 61,200 bytes, SHA-256 `106ff169c0ae67de44ef1adad820fd1c8add5376ee7950227e10fd6fed2d6e6c`다.
기존 32개 Form fixture의 animated WebP도 이제 공통 결과이며 두 GIF 손상 입력만 명시적 차이로 남긴다.

작은 ANMF 안의 16384×16384 VP8L header는 pixel 디코딩 전에 `image_pixels`로 거부한다. VP8X canvas만 보는 경로로 바꾼
부정 대조는 지정 assertion에서 실패한다. 모든 frame의 영역·실제 크기·전체 예산을 먼저 확인하고 고정 30-byte header와
원본 alpha/pixel chunk view로 각 frame을 디코딩한다. Raw/compressed alpha·네 alpha filter·부분 영역·2배 좌표 단위·
zero/최대 duration·metadata·unknown tail·reserved fields·중복/잘린 control·후속 pixels와 context/동시 reader를 검증했다.

IR·생성기·모델 선언·Go/Python dependency와 기존 reference profile/oracle는 바꾸지 않았다. 추가 generated drift·로컬 전체/
cold·다른 OS/CGO=0은 이번 영향 범위가 아니다. 저장 이미지 검사부터 BMP/DIB/TIFF·APNG/WebP까지의 누적 변경을 다음
Hosted full 통합 milestone으로 정했으며, 게시 source의 필수 owner·최종 aggregate·새 capture/Git source 결합이 완료 기준이다.
남은 codec 특성/provider와 전체 기능 카탈로그는 미완료다.

로컬 상세는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래 `godj-webp-animation-db-normal-18dun817`,
`godj-webp-animation-db-race-n31_i3hu`, `godj-webp-animation-fuzz-3dxgkhsm`, `godj-webp-animation-controls-c3vr_85s`,
`godj-webp-animation-native-s3oc0yoy`에 남긴다.

### APNG의 기본 이미지와 전체 frame 검사

2026-09-30, 기반 `a3a68cd6bd6f2acade0dc09d8a1b16d0d365ff14` 이후 PNG 3/APNG의 chunk CRC·sequence·frame 개수/영역과
default image를 같은 업로드/저장 이미지 검사에 연결했다. 최종 비Markdown code/config inventory는
`0b9dddea298723842f2e0e3c1a2742cd7dd72583d000f99fa02ce29b08ac7997` (2,810 files)다. 아래 normal/race/fuzz의 전후
source가 일치하며 이후에는 문서만 정리했다. 기존 완료한 Hosted full의 source와 구분한다.

| 실행 | 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1; uploads·storage·storage/model·forms·forms/model·admin·web 7 packages / 367 roots / 2,923 PASS / 3.407초 |
| 실제 생성 소비자 normal | FileConsumer parent 1 PASS / 9.125초; SQLite/PostgreSQL × filesystem/memory × 9 codec 사례의 필수 36개, APNG 4개 형식의 새 16개 조합 포함 |
| 관련 race | uploads·storage·storage/model·forms·admin 5 packages / 38 roots / 353 PASS / 4.274초 |
| 실제 생성 소비자 race | 부모와 생성 child 모두 race, 같은 양 DB/양 backend의 필수 흐름 / parent 1 PASS / 13.256초 |
| Fuzz | `FuzzInspectImage`, 기존 corpus와 새 APNG seed 319개 baseline, 4 workers·20초 요청 / 652,132회 / Go test 21.680초·명령 전체 24.467초 / PASS |
| 부정 대조 | 전체 frame decode·control/data sequence·원문 CRC·합산/default pixel·frame 영역·선언 개수·reader/CRC 취소·fdAT view의 11개 overlay를 지정 runtime assertion으로 검출; 각 원본을 같은 선택자로 먼저 PASS 확인 |

필수 실행의 완료와 전체 Go JSON을 확인했으며 누락·skip·잘린 출력은 없다. 각 normal/race는 별도 PostgreSQL 17.10 Debian
container, UTF8/libc/C/C와 독립 port/database를 사용했다. 종료 뒤 schema·public table·다른 session `0|0|0`, DB/container
제거를 확인했다. 기본 Go build cache를 유지하고 test-result cache는 끈다.

기존 실제 생성 Photograph의 APNG RGBA·별도 기본 이미지·palette·Adam7 입력을 준비/게시·typed 저장·DB 재개방과 저장 이미지
크기 재검사에 연결했다. Canvas 크기 3×2·기본 이미지 포함 frame 수·저장 이름·원문을 보존하고, 손상된 후속 frame은 typed
준비에서 거부한다. 기존 파일/이미지·Formset·현재 권한을 확인한 Range/conditional·DB rollback 회귀도 같은 소비자로 실행했다.
Admin은 생성/명령 × 양 backend × 기존 4형식과 APNG/별도 기본 이미지의 24개 조합에서 로그인/CSRF·multipart·입력 오류/
재선택·후속 pixels/sequence 거부·쓰기 callback·임시 파일 정리·원문 게시를 확인했다. Formset의 확장자 대소문자와 `.apng`,
실제 format과 허용 확장자의 불일치·내용 우선 오류·validator 횟수·default frame/합산 pixel 한도를 검증했다. 새 화면이나
재생 기능을 추가하지 않았으며 새 브라우저 시각 검증은 실행하지 않았다.

Native는 고정 Django 6.1 / CPython 3.14.3 / Pillow 12.3.0에서 [APNG observer](../../conformance/runners/django/apng_reference.py)를
두 번 실행하고 저장 fixture와 byte 일치를 확인했다. 49개 중 폼 결과가 같은 26개(유효 22개·거부 4개), GoDj가 더 엄격히
거부하는 23개를 구분한다. 유효 22개 중 ancillary chunk가 frame data 사이에 있는 경우와 후속 Adam7의 native frame player는
각각 `OSError`/`TypeError`로 실패하지만 폼 metadata는 성공한다. 독립 static Adam7과 PNG 3의 허용 구조를 함께 관찰했고,
GoDj는 이 두 입력의 pixels도 검사한다. 이 결과를 전체 Pillow 재생 동등성으로 표현하지 않는다. Fixture는 56,655 bytes,
SHA-256 `4e3d4e4fa04abf68c37f65556ea2f1e8d5ad9142ceb6b974e981ca6313033ad7`다. 기존 32개 Form fixture의 APNG는
이제 공통 결과이며 애니메이션 WebP와 두 GIF 손상 입력만 명시적 차이로 남긴다.

초기 normal/race/fuzz 뒤 개별 native 사례의 선택 실행에서 child는 통과하지만 부모의 전체 사례 수 검사가 실패하는 문제를
재현했다. Fixture 목록/중복 검사를 `t.Run`의 선택 실행과 분리하고 11개 원본 선택 실행의 PASS와 변형 후 지정 실패를
각각 확인했다. 위 최종 source의 영향 검증을 다시 완료했으며 앞선 source의 실행을 최종 결과로 재사용하지 않았다.
IR·생성기·모델 선언·Go/Python dependency와 기존 reference profile/oracle는 바꾸지 않았다. 추가 generated drift·전체/cold·
다른 OS/CGO=0 검증은 이번 로컬 범위가 아니며 남은 animation/codec/provider와 카탈로그는 계속 미완료다.

로컬 상세는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래 `godj-apng-db-normal-4dpzbkc8`,
`godj-apng-db-race-8faz0ebn`, `godj-apng-fuzz-to2i82ez`, `godj-apng-controls-2g0nywwg`, `godj-apng-native-cyjawtij`다.
선택 실행의 초기 실패는 `godj-apng-selected-baseline-before-r1s09ry4`에 남긴다.

위 APNG 구현 source `15b33afe`의 [PR feedback 36707396432](https://github.com/progresshans/godj/actions/runs/36707396432)은 success다.
실제 merge checkout `c6632a8fc272f673c3da40a6d60cb453b2e4d95b`의 tree `8042525647b99d28c97c722493bd463d6edfe6ec`가
해당 head와 일치한다. 이 fast 결과는 이후 WebP 변경이나 전체 플랫폼 증거가 아니다.

### BMP/DIB와 여러 페이지 TIFF

2026-09-30, 기반 `deb24431cc3bfb79b35e11bfb3c663989de7d0d6` 이후 공통 업로드/저장 이미지 검사에 BMP/DIB와 classic TIFF를
연결했다. 최종 비Markdown code/config inventory는 `2dc2f81de55e402f8659e95eca7be49597b54ff2095818d1739e45561f0cd333`
(2,806 files)다. 아래 최종 normal/race의 전후 source는 같으며 이후에는 문서만 정리했다. 바로 아래 완료한 Hosted source는
이 코드와 선행 저장 이미지 검사보다 이전이며 결과를 전이하지 않는다.

| 실행 | 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1; uploads·storage·storage/model·forms·forms/model·admin·web 7 packages / 362 roots / 2,821 PASS / 2.080초 |
| 실제 생성 소비자 normal | FileConsumer parent 1 PASS / 6.617초; SQLite/PostgreSQL × filesystem/memory × 5 codec 사례의 필수 20개 실행, 기존 이미지/파일/Formset/인가된 serving 회귀 |
| 관련 race | uploads·storage·storage/model·forms·admin 5 packages / 33 roots / 251 PASS / 2.729초 |
| 실제 생성 소비자 race | 부모와 생성 child 모두 race, 양 DB/양 backend의 같은 필수 흐름 / parent 1 PASS / 13.311초 |
| Fuzz | `FuzzInspectImage`, 기존 corpus·codec/공유 블록 seed 224개 baseline, 4 workers·20초 요청 / 2,947,081회 / Go test 21.447초·명령 전체 22.821초 / PASS |
| 부정 대조 | 모든 TIFF 페이지·합산 pixel·tile padding·page header view·ReadAt context·DIB MIME·IFD 순환·블록 byte 합·header pixel 참조의 9개 overlay를 지정 runtime assertion으로 검출 |

최종 Go JSON에서 필수 실행·완료와 no-skip/no-truncation을 확인했다. Normal/race는 별도 PostgreSQL 17.10 Debian container,
UTF8/libc/C/C·독립 port/database를 사용했다. 종료 뒤 schema·public table·다른 session `0|0|0`, DB/container 제거를 확인했다.
공유 build cache는 유지하고 test-result cache는 끈다. 새 codec이 추가되는 동안에도 파일/DB rollback·원본 보존 회귀를 유지했다.

실제 생성 Photograph는 palette BMP, header 없는 DIB, 여러 페이지 TIFF, big-endian 16-bit TIFF와
padded tile TIFF를 모델 Form으로 준비·게시·DB 저장하고 재개방했다. 저장 이름·첫 페이지의 3×2 크기와 전체 원문이 보존되며,
명시적 재검사도 같은 format/MIME/페이지 수를 반환한다. 잘못된 후속 TIFF 페이지는 typed 준비 단계에서 거부한다. 실제 Admin의
생성/명령 × 양 backend × PNG/BMP/DIB/TIFF 16개 조합에서 로그인/CSRF·multipart·오류 재표시/재선택·임시 수명과 게시한 bytes를
확인했다. TIFF의 늦은 손상도 쓰기 callback 없이 거부했다. Formset은 확장자 대소문자/내용 불일치·잘못된 확장자와 내용 오류의
우선순위·검증 callback 횟수·frame 한도를 확인했다. 이 범위는 새 브라우저 시각 검증을 주장하지 않는다.

Native는 고정 Django 6.1 / CPython 3.14.3 / Pillow 12.3.0에서 독립 observer를 두 번 실행했다. Windows 40/108/124 header,
palette/alpha/top-down·little/big endian·여러 compression·page/tile의 36개 결과와 fixture bytes가 일치했다. 공통 29개와
미지원 BMP16/TIFF CMYK/JPEG 3개, 더 엄격한 잘린 BMP/DIB/TIFF·손상된 후속 TIFF 4개를 구분한다. 기존 32개 폼 관찰의
BMP/TIFF도 이제 실제 native metadata와 비교하며 차이 목록에서 제외한다. Fixture는 34,596 bytes, SHA-256
`e19a2c748923839cbb6d3c39d9b116df32d99f132f1cf87a32edf9bdd5e48a7d`다.

IFD 순환/겹침·범위 밖/overflow·정렬/중복 tag·다른 plane/SubIFD·늦은 compression/pixel 오류와 큰 후속 페이지를 거부한다.
TIFF 3×2 표시 영역의 16×16 tile은 실제 256픽셀로 예산을 계산하며 overflow 곱셈 전에 제한한다. Page view는 공유 원문을
변경하지 않고 독립 context의 ReadAt을 사용한다. 동시 검사와 원본/빌린 reader 수명을 검증했다.

초기 영향 검증 뒤 코드 검토에서 작은 압축 블록의 반복 참조가 파일 크기/pixel 한도만으로 작업량을 제한하지 못하고,
pixel offset이 page view의 수정된 header를 읽을 수 있음을 확인했다. 모든 strip/tile의 개수·span·header 참조를 검사하고
전체 페이지의 참조 byte 합을 `MaxBytes`로 제한했다. 1/2-page PackBits의 동일 블록 반복 참조에서 실제 파일 크기와
합계 직전 한도는 거부하고 정확한 합계에서는 허용한다. 두 방어를 각각 제거한 overlay가 지정 assertion에서 실패했으며,
위 normal/race/fuzz 전체는 이 수정 이후 같은 inventory의 결과다. 앞선 통과 결과를 최종 source의 증거로 재사용하지 않았다.

첫 생성 consumer 검증 두 번은 `InstanceForm`/`BoundForm`에 없는 `Valid` 호출로 compile 실패했다. 현행 공개 API
`BoundForm().Form().Valid()`로 검사한 뒤 위 최종 실행을 완료했다. 첫 core 실행의 성공과 이 실패를 전체 PASS로 합치지 않는다.
Native observer도 폼 검증은 성공하지만 이후 `n_frames`가 실패하는 입력을 만나 처음에는 중단됐다. 이를 별도 `frame_error`와
warning으로 관찰하도록 고친 뒤 최종 재현값을 저장했으며 해당 Go 입력은 계속 거부한다. 기존 Django/DRF profile·oracle와
Python lock은 바꾸지 않았다. 생성기·IR·모델 선언 변경은 없어 추가 generated drift/full/cold/다른 OS·CGO=0은 실행하지 않았다.

로컬 상세는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래 `godj-image-codec-db-normal-eu1360jh`,
`godj-image-codec-db-race-vycyeirn`, `godj-image-codec-fuzz-yu24_lmr`, `godj-image-codec-controls-0lpzg_ho`,
`godj-image-codec-native-9_i3bkma`다. 초기 실패는 `godj-image-codec-db-normal-7il7o6v5`와 `godj-image-codec-db-normal-zodb1x61`에 남긴다.

위 구현 source `a3a68cd6`의 [PR feedback 36703311471](https://github.com/progresshans/godj/actions/runs/36703311471)은 success다.
실제 merge checkout `5e08b7d18f26475bf795860409435bac02d9426a`의 tree `b574a92c94f2a4af40b3ca41bd1feed96333a798`가
해당 head와 일치한다. 이 fast 결과는 뒤에 추가한 APNG의 실행이나 전체 플랫폼 증거가 아니다.

### 누적 파일/이미지 Hosted full 완료

Source `4793382d445a12ee9715162094674133cd147529`의
[Hosted full 36693816049](https://github.com/progresshans/godj/actions/runs/36693816049), attempt 1이 완료됐다.
62개 job이 모두 success이며 최종 aggregate `109838925452`에서 해당 source의 필수 8 owners와 `full_platform_verified: true`를
확인했다. Memory storage·conditional/Range·공통/모델 ImageField·Go dependency 준비와 분리한 이미지 reference 환경까지
포함한다. 저장 이미지 검사 `deb24431`과 위 BMP/DIB/TIFF 변경은 이 source 이후이며 별도의 영향 검증이다. 로컬 전체를 중복 실행하지 않았다.

새 PostgreSQL capture는 archive digest·payload digest·repository/run/attempt·producer job과 Git checkout에 결합했다.
Consumer job `109829007364`의 실제 checkout도 정확히 `4793382d`다. 아래 두 artifact ID를 resolve하고 각각 다운로드한 로그,
producer attempt 1을 넣은 provenance 검사와 실제 conformance step success를 확인했다.

| Capture | producer job / artifact | 실제 Git blob에서 재계산한 source binding |
| --- | --- | --- |
| systemstate | `109817518160` / `11088116589` | 668 files / 6,942,411 bytes / `682fc8a03d425007a2b4e76c60f68fa42076208bbf330bccdf8ead479166ec55` |
| operator | `109817518398` / `11087921260` | 745 files / 6,795,518 bytes / `d1129b148ea27674309b2e055a5e78e3ad64824aed6758fef954975cb810766f` |

Systemstate archive SHA-256은 `cfa76351eed6d60d607e69e29aa15a1ccd5bb2b07502371b809ed2fa514addd8`, payload는
`88f46ac2dcb26e0c14828ca7b8ab280e49531f1ed9fbc68fd5ecbe8309285af4`다. Operator archive는
`16dbcb81edbfd905e46fe1645c702d313767086ea6ecfa62796ccc0ef335da47`, payload는
`178ab97f159396b39e2d026758dfbe7143f5875966ad8e881ccb158bca25135c`다. Source binding은 현재 작업 폴더가 아니라
`4793382d`의 Git 객체로 계산했다. 앞서 취소된 두 실행의 부분 성공은 완료 증거에서 제외한다.

같은 source의 [PR feedback 36693723767](https://github.com/progresshans/godj/actions/runs/36693723767)도 success다.
실제 merge checkout `a18e2587c55d33615997c2ffc2807b7518dd8b62`의 tree `8df2532c737b65aef9d0dda57d3312c07a7ee188`가
해당 head와 일치한다. 이후 저장 이미지 source `deb24431`의 [feedback 36696675795](https://github.com/progresshans/godj/actions/runs/36696675795)도
success이며 merge `fff9432328d5936e67945f4c8d4197e9e5f56514` / tree `1cc91a38fb98db9ccc25b0fa8c39189a8e54bd09`의 source 일치를 확인했다.
로컬 raw evidence는 위 T directory의 `godj-image-reference-integration-full-36693816049-0kfivcl4`에 둔다.


### 저장된 이미지 검사와 typed 크기 갱신

2026-09-30, 기반 `4793382d445a12ee9715162094674133cd147529` 이후 빌린 reader의 공통 내용 검사·storage의 독립 Open/Close·
canonical 모델 크기 갱신을 연결했다. 최종 비Markdown code/config inventory는
`48ed9701ee8896eb679c077da8933ee83935d6389ca3e229da40006377590684` (2,799 files)다. 아래 최종 normal/race는
같은 source이며 전후 inventory가 일치한다. 이후 문서만 정리했고 이전 source의 Hosted 결과를 이 코드에 전이하지 않는다.

| 실행 | 확인한 범위와 결과 |
| --- | --- |
| 영향 normal | Go 1.26.5 / darwin-arm64 / CGO=1, uploads·storage·storage/model·forms·forms/model·admin·web 7 packages / 353 roots / 2,717 PASS / 1.909초 |
| 실제 생성 소비자 normal | FileConsumer parent 1 PASS / 6.642초; SQLite/PostgreSQL × filesystem/memory의 `stored_inspection` 4개 필수 하위 실행과 기존 파일/Formset/인가된 serving 회귀 |
| 관련 race | borrowed/owned reader·이미지 내용·typed snapshot/IR 소유권 3 packages / 20 roots / 101 PASS / 2.064초 |
| 실제 생성 소비자 race | 부모와 생성 child 모두 race / parent 1 PASS / 20.025초; 같은 양 DB/양 backend 필수 실행 |
| 고정 native | Django 6.1 / CPython 3.14.3 / Pillow 12.3.0, 9개 검사·cache 재사용/새 instance·SQLite 저장/rollback; 두 실행과 fixture bytes 일치 |
| 부정 대조 | reader Close·metadata 이름 결합·전체 내용 검증·폭/높이 대입·canonical 크기 소유권·빌린 reader 수명 6개 overlay 모두 지정 runtime assertion에서 실패 |

최종 Go 실행은 필수 root·하위 사례의 완료와 전체 JSON을 확인했다. 누락·skip·잘린 JSON은 없었다. PostgreSQL은 각
normal/race의 별도 `postgres:17.10-bookworm` container (digest `sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`),
UTF8/libc/C/C·다른 port/database로 실행했다. 종료 뒤 관리 schema·public table·다른 session은 `0|0|0`이고 DB/container를 제거했다.
기본 GOCACHE와 cache된 dependency를 사용하고 Go 결과 cache는 끈다.

실제 생성 Photograph는 저장된 7×5 PNG와 stale 19×23 DB 크기에서 시작한다. 검사 후 반환 모델만 7×5가 되고 원본/DB는
19×23을 유지한다. 명시적 크기 저장의 바깥 transaction rollback은 DB를 19×23으로 보존하고 후속 저장/DB 재개방은 7×5다.
늦은 Close 오류는 zero 모델/검사와 원래 원인으로 실패하며 DB나 원본 bytes를 바꾸지 않는다. 생성된 nonnullable 크기의
빈 이미지 참조는 I/O 전에 실패한다. 크기 참조 없는 모델은 I/O 없이 복사만 한다. 이 API는 새 HTTP endpoint가 아니다.

Native 9개 중 일반/한쪽 크기·참조 없음·빈/NULL·없는 파일의 7개 결과는 공통이다. 손상된 파일의 NULL 크기 성공과 PNG
header만의 3×2 성공은 GoDj가 내용 오류로 거부하고 원본을 보존한다. 같은 native ImageFieldFile의 cache는 이름 재사용 후에도
3×2를 유지하지만 새 instance와 GoDj의 명시적 재검사는 7×5를 읽는다. Fixture SHA-256은
`8dfbbce94e08df4972b5306f448cc85dadafb085b1b0d0459d165b3f031718c9`다. Source와 라이선스는 SOURCES와 ADR-0082에 둔다.

최초 normal은 공통 패키지가 통과했으나 생성 consumer의 nonnullable 오류 단언에서 실패했다. 두 크기 모두 NULL을 거부할 때
ORM이 이름순으로 `height`를 먼저 진단하므로 `width` 기대값을 바로잡았다. 잘못된 Read 길이와 함께 반환된 원래 오류도 보존하도록
보강한 뒤 위 최종 normal/race를 실행했다. 부정 대조는 compile 실패가 아니며 원본 제품 파일은 바꾸지 않았다.

로컬 상세: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래
`godj-stored-image-db-normal-lqkwphw4`, `godj-stored-image-db-race-1itquo8p`, `godj-stored-image-controls-_qg0_m6n`,
`godj-stored-image-native-m8a_qvla`다. 첫 실패는 `godj-stored-image-db-normal-bfsnpdul`에 별도로 남긴다.
`make generate-check`의 Unicode·7개 프로젝트·checked-in 생성물 검사, 변경 Go 포맷·171개 문서의 로컬 링크·diff도 통과했다.
전체/cold·다른 OS/arch·CGO=0·후속 Hosted full은 이 checkpoint에서 실행하지 않았다.


### 누적 파일/이미지 Hosted 통합의 의존성 준비 수정

수정 source `958f7facec853f2fe99cbd12dae9c626c3a26fee`의
[후속 Hosted full 36689657934](https://github.com/progresshans/godj/actions/runs/36689657934)에서는 앞선 Go 의존성 검사가
진행됐지만 exact Darwin reference가 기존 profile의 root lock 해시 불일치로 실패했다. Byte catalog만 새 Pillow lock에
맞춘 것으로는 profile/oracle provenance가 보존되지 않았다. Pillow 관찰을 `conformance/reference/images`의 별도 고정
프로젝트로 옮기고 root pyproject/lock을 마지막 전체 검증 source의 bytes로 복원했다. 기존 Django/DRF profile·oracle는
재작성하지 않는다. 새 이미지 lock은 기존 Django/asgiref/sqlparse/tzdata 기록과 Pillow 12.3.0을 그대로 사용한다.
아래 첫 수정의 검사와 이 추가 수정의 검증을 구분하며, 두 실패 실행의 부분 성공을 전체 통합 PASS로 합치지 않는다.

환경 분리 뒤 CPython 3.14.3 / Django 6.1 / uv 0.10.12 / darwin-arm64의 exact reference 경계 4 tests가 4.137초에
skip 없이 통과했다. 기본 suite 결정성·manifest 순서·14개 reference suite의 기존 oracle bytes·migration의 별도 hashseed
process 결과를 확인했다. 새 이미지 환경의 기존 입력/모델 observer 출력도 저장된 두 fixture bytes와 각각 일치했다.
새 portable profile→lock 연결 검사와 artifact byte 검사는 2 roots / 103 PASS이며, 수정 전 Pillow root lock을 넣은 별도
fixture에서는 Django profile 연결만 지정 assertion으로 실패했다. DRF 연결은 통과했고 컴파일 실패는 없었다.
Root/image lock offline 검사·포맷·170개 문서 링크와 diff 검사도 통과했다. 원래 profile·oracle·DRF 환경의 Git diff는 없다.
전체 Python suite와 OS/DB 통합은 새 Hosted 실행이 소유하며 위 관련 검사를 전체 통합 완료로 표현하지 않는다.

2026-09-30, source `4cfa1b40c4417b02aea3f8221a4bddeee13e06be`의
[Hosted full 36688158690](https://github.com/progresshans/godj/actions/runs/36688158690)에서 세 가지 불일치를 확인했다.
CLI/private workspace 테스트가 이전 `x/sys v0.47.0` 캐시를 요구했고, 현재 전체 module graph의 checksum 7줄이 빠져
`go mod download all` 뒤 clean-worktree 검사가 실패했다. Pillow 추가 뒤 `pyproject.toml`/`uv.lock`의 reference byte lock도
갱신되지 않았다. 해당 실행의 부분 성공과 같은 source의 PR feedback 성공을 전체 통합 PASS로 사용하지 않는다.

외부 CLI fixture는 framework의 실제 의존성을 준비하고 불필요한 `x/sys` 로컬 replacement를 제거했다. Private download
검사는 readonly/offline `go list -m`으로 현재 선택 버전을 사용한다. 실제 module downloader로 checksum을 보완했으며,
Python lock은 이전 전체 검증 source와 비교해 Pillow 12.3.0만 추가되고 기존 패키지 기록이 같음을 확인한 뒤 두 byte lock을
명시적으로 갱신했다. 기존 oracle/기대 관찰의 내용과 checksum 검사는 유지한다.

로컬 Go 1.26.5 / darwin-arm64에서 현재 graph만 담은 새 GOMODCACHE를 만들었다. 이전 x/sys의 내용과 download metadata가
모두 없으며 공유 GOCACHE는 유지했다. 실제 CLI check/generate·TTY 중단·migration 게시/재시작·dry-run·별도 migrate/show
runner와 private download/cleanup의 8 roots / 26 PASS를 62.470초에 확인했다. Darwin에서 제외된 Linux deleted-cwd 하위
사례 2개는 별도 미실행이다. 최초 검증 driver의 허용 skip 이름 오타를 수정해 이미 완료한 JSON의 시작/종료·필수 실행을
재검사했으며 제품 테스트를 반복하지 않았다. 같은 캐시에서 수정 전 두 helper를 각각 overlay하면 지정 assertion에서 실패했고
컴파일 실패가 아님을 확인했다. 준비/실행 뒤 루트 lock bytes도 같았다. Reference artifact 1 root / 98 PASS와
`uv lock --check --offline`, 포맷·문서 링크·diff 검사를 확인한다. 전체 OS/arch·race·CGO·cold/reference와 실제 양 DB 통합은
수정 소스의 새 Hosted full이 소유하며 이 로컬 검사로 대체하지 않는다.

### 모델 ImageField·크기 소유권과 생성/저장 연결

2026-09-30, 기반 `61540ea29904a7e6a83c5ba97b0f1965521f40f3` 이후 모델 ImageField의 canonical IR·가로/세로 필드
참조·생성 descriptor·typed/dynamic/관계 query·migration·Form/Admin과 typed 저장을 연결했다. 원본 디코딩의 검증 크기를
model clean 전에 candidate/Input에 넣으며 일반 저장 이름 대입에는 I/O를 넣지 않는다. 크기 필드는 폼/JSON/PostClean의
독립 입력에서 제외하고 clear의 NULL·파일 게시·DB commit을 구분한다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 넓은 영향 normal | schema/IR·ORM·codegen·serializer·모델 Form·Admin·IR resource·project spec/wire·autodetect·migration/definition/backend·SQLite/PostgreSQL 16 packages / 1,573 roots / 필수 하위 3,216개 / 14,089 PASS / 253.309초. 아래 중간 source이며 같은 실행의 소비자 그룹은 컴파일 오류로 FAIL |
| 최종 영향 normal | SQLite·모델 Form 2 packages / 448 roots / 필수 하위 1,490개 / 5,052 PASS / 46.113초 |
| 최종 생성 소비자 양 DB/normal | FileConsumer parent PASS / 7.061초; 이미지의 SQLite/PostgreSQL × filesystem/memory와 각 Formset 필수 이름을 한 번씩 PASS로 검사 |
| 최종 관련 race | 이미지 IR·후보·폼/JSON 소유권·Admin HTTP·wire/자원 경계·이력/자동 계획·SQLite 실행 의도 10 packages / 14 roots / 49 PASS / 4.967초 |
| 최종 생성 소비자 양 DB/race | FileConsumer parent PASS / 19.281초; 생성 자식도 `-trimpath -race`와 필수 실행/skip/잘린 결과 검사를 사용 |
| 독립 native | Django 6.1·CPython 3.14.3·Pillow 12.3.0, 15개 폼 사례와 실제 저장/충돌/rollback/clear/삭제; 두 번 생성한 fixture bytes 일치 |
| 실제 CLI | 별도 Go module의 public project runner→`godj generate`→`--check`→생성 model/form 소비자 PASS; 잘못된 크기 참조의 재생성 실패 뒤 기존 생성 파일/manifest bytes 보존 |
| 부정 대조 | 크기 타입·검증 결과 파생·저장 Input·이미지 폼 투영·SQLite 참조 seal·Admin private snapshot·정확한 wire 크기·복사 전 예산 8개 overlay 검출. 최종 seal에서 참조·scalar Blank·collection Blank의 추가 3개 overlay 검출 |
| 생성 drift | 최종 `make generate-check`: Unicode16 생성/2 Python tests·7 projects·checked-in relation product PASS; 기존 생성 파일 bytes 유지 |

생성 소비자는 실제 PNG의 크기를 typed 준비와 이름 callback에 전달하고 충돌 뒤 반환된 저장 이름을 같은 크기와 저장한다.
Nullable 이름/크기·JSON read-only·typed/dynamic AST·forward 관계 조회를 확인했다. 게시 뒤 DB uniqueness 오류와 transaction
rollback은 이미지 파일을 제거하지 않고, rollback 뒤 재조회한 모델 이름/크기는 이전 값이다. Clear와 모델 삭제도 기존 파일을
유지한다. 여러 행의 pending upload 차단·행별 게시·같은 DB scope 저장과 재개방 후 크기/빈 이름을 확인했다.
ImageField 직접 DDL과 File→Image·크기 참조 변경/역방향의 실제 migration은 같은 물리 열과 저장 값을 보존한다.

Native 15개 중 stale 크기 유지 한 사례는 의도적인 결과 차이다. Django는 포함된 기존 ImageField를 다시 대입할 때 storage를
열어 실제 크기를 갱신한다. GoDj는 저장소를 열지 않고 서버 크기를 유지한다. 다른 유지 사례도 같은 값이지만 I/O 횟수의 동등성을
주장하지 않는다. 새 업로드·clear·충돌·늦은 폼 오류의 candidate/model clean·업로드 제외/위조 크기·저장 결과는 비교했다.
실제 Admin은 로그인/CSRF와 디스크 multipart 뒤 생성/교체/유지/오류/clear를 처리했다. 크기 input은 렌더링하지 않고
위조 POST는 기존 이름 allowlist에서 400으로 거부한다. 누락/불일치/잘못된 서버 크기는 500으로 거부하며 현재 파일 수명과
게시한 원문을 보존한다. 이 HTTP 검증을 별도 브라우저 시각 검증으로 표현하지 않는다.

마이그레이션의 field 단독 정규화를 실제 모델 참조 검사와 분리해 가짜 PK/model을 만들던 경로를 제거했다. 새 크기 열·기존
소유자 해제의 순서와 선언 위치, dangling/공유/비정수 참조 거부, wire 재직렬화·semantic digest·source/실행 의도 결합과
자원 한도를 확인했다. SQLite seal의 기존 scalar/collection Blank 누락도 찾아 보완했다. 이 hash는 실행 중의 의도 검사이며
저장된 migration definition wire/digest를 재작성하지 않는다.

최종 비Markdown code/config inventory는 `d90802c9e6ac9a55e7f8691e20944c73772fb2ab8a5b6f6164b72560c93bc609`
(2,788 files)다. 최종 normal/race와 추가 seal 대조는 같은 source다. 넓은 영향 normal·8개 대조의 source는
`e7d9a78bedb3e89025122ebaa6565c61652cfbde6453382ca0ae76378ea409df`다. 이후 차이는 SQLite hash/2개 테스트,
생성 image 소비자의 잘못된 메서드명 수정, 모델 Form 3개 파일의 API 설명 주석이다. 해당 3개 파일은 주석을 역치환한 bytes가
이전 inventory와 정확히 일치함을 검사했다. 변경한 SQLite·모델 Form과 생성 FileConsumer는 최종 소스로 다시 검증했다.
나머지 제품/테스트는 같은 bytes이며 넓은 실행을 최종 source 전체 실행으로 표현하지 않는다.

첫 checkpoint는 새 참조의 semantic digest 누락과 Admin 테스트의 잘못된 허용 입력 가정 때문에 실패했다. 해시·resource/wire
경로를 보완하고 Admin의 기존 거부 정책을 유지했다. 두 소비자 컴파일 실패는 새 테스트가 존재하지 않는 StartsWith/Contains를
호출한 문제였으며 실제 API `IContains`로 수정한 뒤 양 DB의 normal/race를 실행했다. 실패 실행의 부분 성공을 소비자 PASS로
사용하지 않는다. Native observer의 clean 호출 목록은 저장 후 관찰이 앞선 폼 기록을 바꾸지 않도록 분리했다.

환경은 Go 1.26.5 darwin/arm64·CGO=1·offline/readonly modules·공유 cache/기본 병렬 실행이다. 각 DB checkpoint는 독립
PostgreSQL 17.10 Debian/C/UTF8 container/DB를 사용했고 schema/table/다른 connection `0|0|0`, DB/container 제거를 확인했다.
Compiled root·필수 하위 이름·완료 JSON·누락/skip·실행 전후 source 불변을 검사했다. Native fixture
`forms/model/testdata/model-image-django61.json`의 SHA256은
`61f6fabe5fa2d73c06d1f14af60446bf472215a96959e1cc74943e4f39479a90`이다. 출처·라이선스는 SOURCES·ADR-0082를 따른다.

결합 receipt는 `/tmp/godj-model-image-validation/receipt.json`, 원문은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/`의
`godj-model-image-db-normal-5e26nxp1`(첫 실패), `godj-model-image-db-final-normal-9bd444v4`(넓은 normal/소비자 실패),
`godj-model-image-db-confirmed-normal-gp6u4f3c`, `godj-model-image-db-confirmed-race-eu61izrd`,
`godj-model-image-controls-ksa0q2l_`, `godj-model-image-seal-controls-w_ovq7_1`, `godj-model-image-cli-et4efzlp`에 있다.

이 checkpoint에서 전체/cold·다른 OS/arch·CGO=0·Hosted full은 실행하지 않았다. Memory·Range/conditional·이미지 기반/모델과
누적 의존성의 새 Hosted full 통합을 다음 milestone으로 실행한다. 기존 `365ad9d4`의 결과를 전이하지 않으며 저장된 이미지의
명시적 검사/크기 갱신·추가 codec/provider·기능 카탈로그 전체는 미완료다.

### 공통 ImageField 입력과 context 기반 바인딩

2026-09-30, 기반 `1ec857477db4d641d23d566bed78eee1487ca9f0` 이후 이미지의 실제 내용 검증을 Form/Formset·모델 폼의
비저장 입력과 Admin 생성/명령에 연결했다. Bind 계열이 context를 명시적으로 받으며 업로드를 독립 reader로 읽는다.
클라이언트 MIME·기존 저장 참조와 검증 metadata를 구분하고, 사용자 field/cross/model validator의 pure 계약은 유지한다.
이 checkpoint는 **공통 입력 기반**이며 모델 ImageField의 IR·폭/높이 field 참조·생성 모델/migration은 아직 구현하지 않았다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 최종 영향 normal | uploads/Form/Admin 3 packages / 171 roots / 1,576 PASS / 1.222초 |
| 최종 이미지 관련 race | 독립 reader·내용/한도·Form/Formset·실제 Admin 생성/명령·I/O 실패 10 roots / 62 PASS / 2.334초 |
| 최종 기존 생성 파일 소비자 양 DB/race | FileConsumer parent PASS / 20.066초; 생성 module의 필수 16개 이름을 각각 한 번의 PASS로 확인, SQLite/PostgreSQL × filesystem/memory 및 Range/conditional 포함 |
| 최종 Helpdesk 양 DB/race | 기존 공개 HTML/Admin/API 2 roots / 필수 하위 141개 / 총 284 PASS / 145.843초 |
| 기존 호출경로 영향 normal | 아래 첫 source에서 최종 PASS한 11 packages / 366 roots / 2,064 PASS. 같은 실행 전체는 이미지 2개 package 실패로 FAIL이며 아래 수정/재검과 구분 |
| 생성 소비자 normal | 첫 source에서 File/Choices/Email/ForwardScalar/ManyToManyCollections/ModelClean/ModelFormDatabase/ModelFormSave 8 roots / 11 parent·subtest PASS / 41.384초; 내부 생성 module의 필수 실행 검사와 양 DB 활성화 |
| 독립 native | Django 6.1·CPython 3.14.3·Pillow 12.3.0의 32개 관찰, 공통 26개·명시적 차이 6개; 두 번 실행한 bytes와 fixture 일치 |
| Fuzz | `FuzzInspectImage`, 12개 seed·4 workers·20초 요청, 실제 21.477초 / 1,436,107회 실행 / PASS |
| 부정 대조 | context 전달·전체 GIF 디코딩·단일 픽셀/프레임/합산 한도·reader close·WebP framing·운영 오류 구분·확장자/user validator 순서·검증 metadata·APNG 거부의 11개 overlay가 지정 runtime assertion에서 실패 |
| 정적 확인 | 전체 205 packages warm compile-only PASS(167.566초, 테스트 실행 없음), 영향 go vet, `make generate-check`의 Unicode16 생성/2 Python tests·7 projects·checked-in relation product drift PASS; gofmt/diff·170 Markdown 문서의 local 링크 PASS |

새 이미지 입력은 static PNG(16-bit 포함)·JPEG(CMYK/progressive 포함)·GIF 전체 frame·static WebP(lossless/alpha 포함)를
확인했다. 인코딩 bytes·폭/높이·단일 pixel·GIF frame 수/합계를 디코딩 전에 제한한다. BMP/TIFF/APNG/animated WebP는 현재
명시적 미지원이며, Django/Pillow가 허용하는 잘린 GIF tail와 구조가 유지된 두 번째 frame의 잘못된 LZW code도 GoDj는 거부한다.
이 여섯 사례를 native 동등성으로 합산하지 않았다. 허용 확장자와 실제 format의 불일치는 허용하되 실제 MIME은 검증 metadata에서
얻는다. 검증은 원문 bytes를 바꾸지 않고, 보유 reader cursor·공유 Spec·request staging 수명을 보존한다.

실제 Admin HTTP는 PBKDF2 로그인·CSRF 뒤 disk-spilled multipart를 생성/명령 폼에서 검증하고 filesystem/memory에 게시한다.
잘못된 이미지·확장자 오류는 저장 callback 0회와 200 오류 재표시·재선택 안내를 유지한다. 잘못된 CSRF도 게시를 막고, 정상
게시 뒤 upload는 닫히지만 backend의 원문은 재개방된다. 파싱 후 staging 파일이 사라진 입력은 500이며 `invalid_image`나
저장 성공으로 바뀌지 않는다. 별도 fault에서는 reader close 실패가 성공 metadata를 무효화하고 내용/정리 양쪽 cause를 보존한다.

최종 비Markdown code/config inventory는 `c2f8854e31081cf2884f372e5e2d8e9e216c869c5eec9501e69c453f77d858ae`
(2,774 files)다. 최종 normal/race/DB·11개 대조는 같은 source다. 첫 영향 실행 source는
`be8d86dafcdf07bb0cd23f18832bbe33a0a55e73b2c8a8cd300e6847b1d2a573`이었다. 이 실행 전체를 PASS로 보지 않는다.
고정 관찰에서 확장자가 잘못돼도 Django가 사용자 validator를 계속 호출함을 확인해 `forms/file.go`의 순서를 수정했다.
또한 Admin의 기존 HTML 오류 재표시는 200이므로 새 테스트의 잘못된 400 기대값을 고쳤다. 최종 차이는 이 두 파일과
`uploads/image.go`의 설명 주석, `uploads/image_test.go`의 close-failure 검사뿐이다. 영향 3개 package를 최종 source로 다시
검증하고 기존 File/Helpdesk의 실제 양 DB/race를 확인했다. 나머지 호출경로·생성 source/fixture·Go 의존성은 inventory로 같은
bytes임을 확인해 이미 통과한 broad normal을 반복하지 않았다. 선행 source의 성공을 최종 source 전체 실행으로 표현하지 않는다.

Go 1.26.5 Darwin arm64·offline/readonly module·공유 cache와 기본 병렬 실행이다. 생성 자식도 `-trimpath -race`를 전달한다.
각 DB checkpoint는 별도 PostgreSQL 17.10 Debian/C/UTF8 container와 DB를 사용했고 종료 후 schema/table/다른 connection
`0|0|0`, DB/container 제거를 확인했다. Compiled root·필수 하위 이름·완료 JSON·누락/skip·실행 전후 source 불변을 검사했다.
WebP는 고정 `golang.org/x/image v0.46.0`을 사용하며 이 의존성이 요구하는 x/sys v0.48.0·x/text v0.42.0·x/sync v0.23.0을
반영했다. 생성 drift와 기존 Unicode16/인증 정규화 경로도 영향 검사에 포함했다.

Native observer는 `conformance/runners/django/image_field_reference.py`, fixture는 `forms/testdata/image-django61.json`이며
SHA256은 `a9572a8b4915c15e18fe36768390574f8c5e2a4cdb5bf4de8a1d361e6e193e87`이다. 같은 Django commit
`fe0a859f537d4238cf49fca39073513206f83122`/BSD-3-Clause의 `django.forms.fields` SHA256은
`2b756230a12f2329e2796314dd99fc57a8d0b016658863be2e899a2988d33f66`, Pillow 12.3.0/MIT-CMU의 `PIL.Image`는
`04af3f2db7ffdd61e829fa7aa8f5481d394a0a4129a8fd8aa5b13908928fcd6d`다. Source/라이선스와 지원 계약은 SOURCES·ADR-0082를 따른다.

원문 receipts는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/`의 `godj-image-final-normal-jyrgwwc0`,
`godj-image-binding-db-race-z75cx3sa`, `godj-image-binding-db-normal-e7snja87`(첫 실패 포함), `godj-image-native-ox2_81pg`,
`godj-image-controls-zb7806rl`, `godj-image-fuzz-ehoadg5f`, `godj-image-vet-vq9qit_k`, `godj-image-drift-gskpzufx`,
`godj-image-compile-ro8g6ic0`에 있다. Source와 결과를 결합한 receipt는 `/tmp/godj-image-validation/receipt.json`이다.

새 HTML은 file input의 accept와 오류 안내뿐이며 실제 HTTP HTML/전송을 확인했다. 별도 브라우저 전체 시각 검증은 실행하지 않았다.
이번 source의 전체/cold·CGO=0·Hosted full도 실행하지 않았다. 앞선 `365ad9d4` Hosted full 성공을 전이하지 않으며,
모델 ImageField·크기 반영·추가 codec/storage provider와 나머지 기능 카탈로그는 계속 미완료다.

### 같은 열린 파일의 conditional 조회와 byte Range

2026-09-30, 기반 `798e138d64e5fadffb0f2b01edf1b22e3b7c73ff` 이후 Reader의 context 기반 seek와 immutable ContentMetadata를
연결했다. Memory는 content SHA256 version을, filesystem은 같은 handle의 수정 시각을 제공한다. FileResponse는 전체 인가 뒤
현재 열린 내용으로 ETag/date의 우선순위·304/412·HEAD와 단일/여러 byte Range를 판단한다. Multipart framing까지 전송 한도에
포함하고 파일 전체 복사·새 전송 goroutine 없이 read/seek/close를 소유한다. 요청/목록/정수 한도·없는 파일·header 후 중단을 보존한다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 최종 영향 normal | storage/settings/uploads/Web/sessionauth/API/OpenAPI/Admin/모델 Form 9 packages / 358 roots / 1,673 PASS / 4.289초 |
| 최종 관련 race | storage/Web/sessionauth의 reader·파일/stream·cookie·drain 44 roots / 229 PASS / 4.830초 |
| 최종 생성 소비자 양 DB/race | FileConsumer parent PASS / 12.344초; 별도 생성 module의 필수 16개 이름을 각각 한 번의 PASS로 확인, SQLite/PostgreSQL 각각 filesystem/memory/ranges_and_conditionals 포함 |
| Helpdesk 양 DB/race | 기존 공개 HTTP 2 roots / 필수 하위 141개 / 총 284 PASS / 138.340초; 아래 source 차이를 구분 |
| 독립 native/표준 | 고정 Django 6.1/CPython 3.14.3의 22개 관찰을 두 번 같은 원문으로 확인; 공통 19개와 명시적 차이 3개. Go 1.26.5 ServeContent의 공통 range 10개 결과를 별도 실행 |
| 최종 부정 대조 | ASCII range unit·파일 version 결합·strong 비교·HEAD·선택 offset·reader close·304 framing·범위 수·조건 우선순위를 각각 훼손한 9개 overlay가 지정 runtime assertion에서 실패 |
| 정적 확인 | storage/Web/생성 소비자 compile-only, 영향 10 packages go vet·gofmt·diff·local Markdown 링크 PASS |

같은 열기에서 얻은 metadata를 사용하며 별도 Stat와 이름 재조회는 없다. 이름을 같은 길이의 다른 bytes로 재사용해도 이전 ETag가
304나 잘못된 resume를 만들지 않고 동시 응답의 cursor/header/status를 공유하지 않는다. HEAD는 Range를 무시하며 304/412/416은
read/seek 0과 한 번의 close를 유지한다. 여러 범위의 순서·MIME parts·전체 Content-Length·고정 read buffer, bytes/부분 응답 한도,
약한 날짜의 If-Range fallback·미상 metadata·미래 시각 제한·정수 overflow·잘못된 문법/과도한 요청을 확인했다. Seek 실패/잘못된
position/panic·짧은 reader·joined EOF·늦은 두 번째 seek와 metadata 실패는 같은 자원/전송 경계를 따른다. 실제 TCP client가
304의 빈 본문/길이 없음과 206의 선언된 99,999 bytes 중 71,680 bytes 뒤 `unexpected EOF`를 관찰하며 reader 정리가 완료된다.

생성 소비자는 실제 cookie login/CSRF·1 MiB 초과 multipart 게시·DB 저장 뒤 두 storage에서 같은 GET/HEAD·단일/여러 범위·조건부
요청을 보낸다. 인증/권한 거부·다른 owner와 소유권 변경 후 요청에서 wildcard/Range도 storage open을 우회하지 못한다. 304/412/416의
read/seek 0·reader close·기존 cache/cookie 정책, 인가된 missing 404와 upload staging 정리를 확인했다. 기존 Helpdesk의 공개 HTML/
Admin/API·revision·audit·실패/rollback 흐름도 양 DB에서 검증했다.

최종 비Markdown code/config inventory는 `a86149468b69b812e6a508a9a3c5681952a96b84326c0477e86ee427095d8211`이다.
위 최종 normal/race·FileConsumer·9개 대조는 모두 이 소스다. Helpdesk는
`9a44c2146d46a6b4ce2aeaf24b0de85d730f933ae3644b6269c44a1589c7abdf`에서 실행했다. 이후 달라진 것은 File 전용
`web/file_ranges.go`의 ASCII unit 검사와 `web/file_http_test.go`, 별도 생성 fixture `codegen/consumertest/testdata/files/serving_test.go`
두 테스트뿐이다. Helpdesk/API/Admin/Form은 FileResponse/streaming 응답을 만들지 않아 이 parser에 진입하지 않으며, 나머지 실행
제품/Helpdesk 테스트는 전체 inventory로 일치를 확인했다. 변경한 File 소비자는 최종 source에서 다시 검증했고 Helpdesk를 반복하지
않았다. Source 전후 불변·필수 root/이름·완료 JSON·skip/잘린 출력 검사를 결합한 receipt는 `/tmp/godj-file-http-validation/receipt.json`이다.

Go 1.26.5 Darwin arm64·offline/readonly module·기본 공유 cache와 병렬 실행이다. 생성 자식도 `-trimpath -race`를 전달했다.
DB 실행은 각각 별도 PostgreSQL 17.10 Debian/C/UTF8 container/DB를 썼고 종료 후 schema/table/다른 connection `0|0|0`, DB와
해당 container 제거를 확인했다. Native observer는 `conformance/runners/django/file_conditional_reference.py`, golden은
`web/testdata/file-conditional-django61.json`이며 SHA256은 `bfaf44056e3ec5ac70662b065bc067666e549342d5e9d30847e6f24921720ae3`다.
같은 pinned commit `fe0a859f537d4238cf49fca39073513206f83122`/BSD-3-Clause의 `django/utils/cache.py` SHA256은
`cef63ee1b300683e517241a7232d111dd627a61ab86abc3b6c273cb8ee81da4e`다. Native의 Range 미지원·잘못된 ETag 목록 일부 수용·
ETag 없는 wildcard와의 차이를 명시했다. HTTP 의미는 RFC 9110을 기준으로 하며 source와 계약은 SOURCES·ADR-0082에 둔다.

최종 receipts는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/`의 `godj-file-http-normal-i8mahush`,
`godj-file-http-race-8jj1mpji`, `godj-file-http-db-race-0rxcdpwu`, `godj-file-http-controls-_iqyc8td`다.
Helpdesk 원문은 `godj-file-http-db-race-r190qzbe`, native 원문은 `godj-file-http-native-hf2iiqf4`에 있다.
검토 중 ETag 목록 끝의 공백을 거부한다고 추정해 분기를 추가했으나, `godj-file-http-controls-cp7t6zy4`의 대조에서 원래 코드도
공백 포함 입력을 통과했다. 함수 시작의 정규화가 이미 처리하고 있음을 확인해 추정을 기각하고 불필요한 제품 분기를 제거했다.
이 대조는 실패 검출 성공에 합산하지 않았다. 강화한 테스트 입력은 유지했다. 별도로 Unicode case fold가 비ASCII `byteſ`를
bytes unit으로 받아들이는 것을 막는 ASCII token 검사를 추가하고 최종 부정 대조로 확인했다.

Schema/generator·모델 선언·checked-in generated Go는 바꾸지 않아 generated drift를 반복하지 않았다. HTML/JS 변경이 없어
브라우저를 반복하지 않았다. 이번 source의 전체/cold·CGO=0·Hosted full은 실행하지 않았고 앞선 `365ad9d4` 전체 성공을 전이하지
않는다. 외부 storage provider·ImageField·파일 choices·자동 회수와 나머지 기능 카탈로그는 미완료다.

### 격리된 Memory storage와 실제 파일 소비자

2026-09-30, 기반 `365ad9d4bb94f049f692ef7722b2684a6f48f379` 이후 프로세스별 Memory backend를 같은 Storage 계약에 연결했다.
Source는 한 번 읽고 완성한 내용만 no-overwrite 게시한다. 내용 byte·파일 수·동시 Save를 제한하며 pending·게시된 파일·삭제 뒤
열린 reader의 내용을 모두 quota에 포함한다. 독립 cursor·변하지 않는 열린 파일 metadata·name 재사용·Close 수명을 보존하고
실패/취소·source/entropy panic의 예약과 잠금을 정리한다. Source는 빌린 객체로서 Storage가 닫지 않는다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 영향 normal | storage/settings/Web 3 packages / 81 roots / 372 PASS / 1.892초 |
| 관련 race | storage/Web 19 roots / 73 PASS / 2.060초 |
| 최종 storage normal/race | 각각 26 roots / 79 PASS / 1.109·3.018초; 실패 예약 회수 검사를 강화하고 빌린 입력의 close 소유권 검사를 추가한 뒤 실행 |
| 생성 소비자 양 DB/race | FileConsumer parent PASS / 10.207초; 별도 생성 module의 필수 12개 이름을 각각 한 번의 PASS로 확인, SQLite/PostgreSQL 각각 filesystem/memory serving 포함 |
| 독립 native | 고정 Django 6.1/CPython 3.14.3의 bytes·Unicode·empty 3개와 인스턴스 분리, 두 번 같은 원문; 공유 cursor·실패 후 부분 파일·재귀 directory 삭제의 명시적 차이 확인 |
| 부정 대조 | 열린 reader의 quota·실패 예약 회수·원자 게시·독립 cursor·취소·빈 파일 개수·동시 Save·입력 close 소유권을 각각 훼손한 8개 overlay가 지정 runtime assertion에서 실패 |
| 정적 확인 | storage/생성 소비자 go vet·gofmt·diff와 local Markdown 링크 PASS |

생성 소비자는 backend별 별도 owner/DB title/파일 prefix를 사용한다. 실제 PBKDF2 cookie login과 갱신 CSRF 뒤 1 MiB보다 큰
multipart 파일을 게시하고 충돌 후 실제 저장 이름을 DB에 기록한다. 위조 owner·미인증·다른 owner·인가 실패·파일 문자열 위조의
Storage I/O 0, GET/HEAD·query의 alias/name 위조 무시·소유권 변경 후 재인가·missing file과 upload 정리를 양 DB/양 backend에서
확인했다. Storage reader는 한 번만 닫히고 32 KiB 이하 전송과 별도 Stat 0을 유지한다. 생성 모델/DB 재개방과 기존 formset
파일 게시/rollback도 같은 소비자 안에서 실행한다.

Go 1.26.5 Darwin arm64, offline/readonly module·기본 공유 cache·병렬 실행이다. 생성 자식도 `-trimpath -race`를 전달했다.
Compiled roots·필수 하위 이름·완료 JSON·skip/잘린 출력·실행 전후 source 불변을 확인했다. 별도 PostgreSQL 17.10
Debian/C/UTF8 container/DB에서 종료 후 schema/table/다른 connection `0|0|0`, DB와 해당 container 제거를 확인했다.

최종 비Markdown code/config inventory는 `a28ffae4e5baab84cad0e8fa3949ff0cd04689379872d1c4e2dab47549737b38`이다.
영향 normal·관련 race·DB 소비자는 `b7057dc428f22298b289ebaf06679101f8fafa6b51dae07faa0d0d3dcbfbefe6`에서 실행했고 이후
바뀐 유일한 파일은 `storage/memory_test.go`다. 최종 storage normal/race와 부정 대조는 최종 source에서 실행했다.
전체 inventory 대조로 **DB 실행의 제품 코드와 생성 소비자 테스트가 최종 소스와 동일**함을 확인했다. 결합 receipt는
`/tmp/godj-memory-storage-validation/receipt.json`이다. 제품 수정 없는 DB/Helpdesk 전체 회귀는 반복하지 않았다.

Local receipts는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/`의
`godj-memory-storage-normal-esbdm2sb`, `godj-memory-storage-race-6w0qpe3h`, `godj-memory-storage-db-race-wm4fb9d4`,
`godj-memory-storage-normal-dvd91ceq`, `godj-memory-storage-race-9q64gl5w`, `godj-memory-storage-controls-3jtkmwx1`이다.
Native observer는 `conformance/runners/django/memory_storage_reference.py`, golden은 `storage/testdata/memory-django61.json`이며
SHA256은 `4a89dbd22dc653da0688c31c9024ae27c5762867cb99d26a68be009c77a4c271`이다. Django memory.py SHA256은
`7940d107b075b9355bd5aef4cde6ac225577dd3de47c5705f5ccafcfde12e24f`, upstream은
`fe0a859f537d4238cf49fca39073513206f83122`/BSD-3-Clause다. Native의 공유 reader/부분 게시/재귀 삭제는 기존 GoDj Backend의
더 강한 수명·게시·명시적 삭제 계약을 대체하지 않는다. 판단과 사용법은 ADR-0082·Storage README·SOURCES를 따른다.

Memory의 MaxBytes는 보관/예약한 내용 길이의 한도이며 process RSS 한도가 아니다. Slice capacity/GC·고정 transfer buffer·
metadata·reader handle은 별도 메모리를 쓴다. 임의 source의 막힌 Read를 강제 중단하지 않으며 context 협조가 필요하다.
Schema/generator·모델 선언·checked-in generated Go는 바꾸지 않아 generated drift를 반복하지 않았다. 이번 Memory 변경은
`365ad9d4`의 Hosted full에 포함되지 않으며 로컬 전체/cold·CGO=0·Hosted 전체 성공으로 확대하지 않는다. 추가 provider·서명 URL·
Range/conditional·ImageField와 나머지 기능 카탈로그는 미완료다.

반영 source `ce4cdde1dedcc01eaa15343f5778ab7d2137ca06`의 [PR feedback 36671339317](https://github.com/progresshans/godj/actions/runs/36671339317)은
completed/success다. 이는 아래 `365ad9d4`의 Hosted full과 별도 실행이며 Memory의 전체 플랫폼 검증을 뜻하지 않는다.


### Storage alias와 인가된 파일 streaming

2026-09-30, 기반 `249bc9bacb2f021d2f1f6975feb78939b5705d8d` 이후 application별 immutable storage registry와 별도 URL
capability를 Settings에 연결했다. 유한 Web stream은 전체 middleware 성공 뒤에만 독립 reader를 열고 borrowed Request/upload를
연장하지 않는다. 같은 opened handle의 metadata·알려진 길이/EOF·상한·HEAD·한 번의 close와 header 이후 transport abort를
구현했다. FileResponse의 기본 attachment/octet-stream/no-store/nosniff/no-referrer, 현재 모델과 principal을 조회하는 파일
다운로드 소비자를 연결했다. `Body()`는 buffered 복사본/error를 반환하고 stream을 암묵적으로 소비하지 않는다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 영향 normal | storage/settings/uploads/Web/sessionauth/API/OpenAPI/Admin/모델 Form 9 packages / 334 roots / 1,538 PASS / 6.401초 |
| 관련 race | storage/settings/Web/sessionauth의 alias·파일/stream·cookie·server drain 20 roots / 60 PASS / 9.738초 |
| 생성 소비자 양 DB/race | FileConsumer parent PASS / 8.256초; 별도 생성 module의 projection·저장·SQLite/PostgreSQL·각 formset/serving 필수 8개 이름을 각 한 번의 PASS로 검사 |
| Helpdesk 양 DB/race | 실제 공개 HTTP 소비자 2 roots / 필수 하위 141개 / 총 284 PASS / 134.091초 |
| 독립 native | 고정 Django 6.1/CPython 3.14.3 alias 관찰, URL 12개·FileResponse 6개와 명시적 이름 차이 4개; 두 번 같은 원문 |
| 부정 대조 | 응답 구성의 I/O 지연·reader close·정확한 EOF·전송 abort·framing header 소유·불필요한 Stat를 훼손한 6개 overlay가 지정 runtime assertion에서 실패 |
| ABI/생성/정적 확인 | 전체 205 packages compile-only, 영향 10 packages go vet, `make generate-check`의 7개 프로젝트/checked-in 관계 생성물, gofmt·diff·170개 문서 링크 PASS |

생성 소비자는 실제 cookie login과 CSRF 갱신 뒤 1 MiB보다 큰 multipart 파일을 게시하고 DB에 충돌 후 실제 저장 이름과 서버
소유자를 기록한다. 기존 파일은 보존한다. 미인증/무권한/다른 소유자·CSRF 실패·파일 이름 문자열 위조에서 storage I/O가 없고,
GET/HEAD·query의 alias/name 위조·소유권 변경 후 이전 사용자 거부·새 소유자 허용·인가된 missing file 404를 확인했다.
성공 reader는 한 번만 닫고 read buffer는 32 KiB 이하이며 별도 Stat와 남은 upload staging이 없다. Helpdesk의 기존 로그인·
Form/Admin/API·revision·권한·audit·실패/rollback 경로도 실제 SQLite/PostgreSQL 소비자로 실행했다.

Web 검사는 handler/middleware 거부·응답 교체·panic·repeated next에서 open 0, 요청 해제 후 stream의 살아 있는 transport
context, 재사용/동시 요청의 새 reader, reader/writer 오류·panic·짧거나 긴 길이·무진행·joined EOF 실패·close 실패 진단을 포함한다.
실제 TCP client가 잘못된 길이에서 불완전 body 오류를 받고 취소 시 reader가 닫히며 graceful server drain이 전송을 기다리는 것을
확인했다. 임의 context 비협조 reader의 막힌 Read를 강제 종료하는 API나 SSE/WebSocket 검증은 아니다.

Go 1.26.5 Darwin arm64·영향/DB 실행의 offline/readonly module·기본 공유 cache와 병렬 실행이다. 생성 자식에도 `-trimpath -race`를
전달했다. Compiled roots·required 이름·완료 JSON·skip/잘린 출력과 source 전후 불변을 확인했다. 별도 PostgreSQL 17.10
Debian/C/UTF8 container/DB에서 종료 후 schema/table/다른 connection `0|0|0`, DB 제거·해당 container 제거를 확인했다.

최종 비Markdown code/config inventory는 `0d7f60679b23dbfbf7817ddaf71f4c86bd8f0df358d0d528a222272f727681a2`다.
양 DB/race와 부정 대조는 이 소스다. Normal/관련 race는 `768108cff81494d55540b785fc20022b95d5d3f8162b0e4034512b4368526018`이며
이후 바뀐 유일한 파일은 별도 생성 fixture `codegen/consumertest/testdata/files/serving_test.go`의 HTTP helper nil-header 수정이다.
해당 fixture는 최종 양 DB/race에서 다시 실행했고 **제품 코드와 core 테스트는 동일**함을 전체 inventory로 확인했다. 결합 receipt는
`/tmp/godj-streaming-validation/receipt.json`이다. 변경하지 않은 core normal/race를 반복하지 않았다.

초기 compile의 미사용 import와 새 fixture의 literal 괄호를 수정했다. 최초 normal은 short reader가 첫 Read에서 EOF를 주지 않아
이미 header를 보낸 뒤 abort했으나 테스트가 500을 기대해 실패했다. 첫 Read의 EOF를 명시해 header 전 실패와 기존 late-EOF
검사를 분리했으며 제품의 framing 의미를 완화하지 않았다. 최초 양 DB 소비자 실행은 compile 실패가 아니라 테스트 HTTP helper가
nil Header를 넣어 cookie jar의 AddCookie에서 panic한 것이다. 요약 진단만 보고 compiler 오류로 추정했다가 compile-only와
실제 원문으로 바로잡았다. Helper는 NewRequest의 header map을 보존하며 최종 양 DB 실행이 통과했다. 처음 metadata 부정 대조는
Stat 오류로 더 이른 500/MIME 진단에서 실패해 계획한 assertion을 만족하지 못했다. 같은 handle의 Info를 유지하며 불필요한 Stat
호출만 주입하는 대조로 명확히 분리한 뒤 6개 최종 대조를 완료했다. 이 초기 실행들을 PASS에 합치지 않았다.

Local receipts는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/`의
`godj-streaming-normal-cdhezhm9`, `godj-streaming-race-wzp7zi59`, `godj-streaming-db-race-ohbi2hr3`,
`godj-streaming-controls-2_ul5sf3`다. 최초 실패는 `godj-streaming-normal-gih0ptph`, `godj-streaming-db-race-tz2a5mky`,
`godj-streaming-controls-febvewrl`에 남긴다. Native observer는 `conformance/runners/django/storage_serving_reference.py`,
golden은 `storage/testdata/serving-django61.json`이고 SHA256은 `37cfb4f1313d2c51c0988e823e732a07ca6861b3274ce9eb6adb7662bacd3ad7`이다.
Django `handler.py`, `filesystem.py`, `response.py` SHA256은 golden에 보관한다. Upstream commit
`fe0a859f537d4238cf49fca39073513206f83122`/BSD-3-Clause를 참조한다. Go의 eager backend 등록·pure lazy response·portable 이름·
안전한 attachment/MIME 기본은 native의 lazy factory/eager file ownership·MIME 추측/inline 기본과 구분한다.

이번 로컬 checkpoint는 전체/cold·CGO=0·Hosted full 성공이 아니다. `6d3fe97f` 이후 동적 inline UI·파일 입력/storage·FileField·
일반 ModelFormSet 저장과 이번 alias/streaming을 함께 묶은 **Hosted full 통합 milestone**을 다음 실행 범위로 정했다. 로컬 전체
`make ci`를 중복하지 않고, 해당 실행의 source·각 owner·최종 aggregate·같은 run의 새 capture 결합을 확인한 뒤 별도로 기록한다.
HTML/JS 변경이 없어 이번 slice의 브라우저를 반복하지 않았다. 추가 storage backend·서명 URL provider·Range/conditional,
ImageField와 나머지 기능 카탈로그는 계속 미완료다.



#### 수정 소스의 Hosted full 통합 완료

수정 source `365ad9d4bb94f049f692ef7722b2684a6f48f379`의 [PR feedback 36666761713](https://github.com/progresshans/godj/actions/runs/36666761713)은
completed/success다. 같은 source의 [Hosted full 36666773815](https://github.com/progresshans/godj/actions/runs/36666773815),
attempt 1은 completed/success다. 62개 job이 모두 completed/success이며 최종 집계 job `109749121574`의 실제 로그는
`full_platform_verified: true`와 필수 8개 owner를 기록했다. 해당 source의 `scripts/ci/scopes.py`에서 독립 계산한 기대 집계와
정확히 일치한다. 아래 두 normal PostgreSQL producer는 같은 run/attempt에서 성공했다.

| capture | artifact / producer job | payload SHA256 |
| --- | --- | --- |
| systemstate-postgres-1 | 11077101996 / 109733045740 | `76f6179868a40ffdad829b9bfed157beb4a7846aa99681876d33776af76a2457` |
| operator-postgres-1 | 11077175437 / 109733045779 | `99268433c48a4eab992c566c57fa669189eb99ddc2a7e5fbfbdc4c5ee96f34a3` |

새 archive의 GitHub digest·정확한 파일 집합·SHA256SUMS·producer/provenance를 확인했다. Working tree 대신 위 Git commit의
blob에서 source binding을 재계산해 systemstate 663 files / 6,889,574 bytes /
`9d3e62de4ab4a9fe5db24643fe1e61cff9d000ef8be780addc3ac23b4510bea7`, operator 740 files / 6,739,207 bytes /
`ef41858ccfaee0d8e4cddfaeddf8d2c30d09759ddf031d519976ab4acb3ee955` 일치를 확인했다.
원문과 결합 receipt는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-streaming-integration-full-36666773815-v459a4f9`에 있다.
Conformance 소비 job `109739093382`의 실제 로그에서 위 두 artifact ID의 resolve/download와 같은 producer attempt의 provenance
검증·실행을 확인했다. 최종 run/jobs·aggregate 로그·소비 로그를 보존했고 결합 receipt의 전체 `pass`는 true다.
Command/relation·portable Go·PostgreSQL·project check·Python compatibility·exact Darwin·conformance 8 owners를 통합했다.
이 결과는 동적 inline UI·업로드/storage·FileField·일반 여러 행 저장·alias/streaming 및 entropy 잠금 수정까지의 위 source에
한정한다. 이후 Memory backend나 작성 중인 Range/conditional 변경으로 전체 성공을 전이하지 않는다. 로컬 전체/cold는 중복하지 않았다.

#### 난수 callback panic 후 후속 저장의 잠금 해제

Hosted 통합을 기다리는 동안 `27ba040e`의 Random 호출이 panic하면 entropy 직렬화 잠금을 해제하지 않는 경로를 확인했다.
Staging 이름과 충돌 이름에서 각각 재현했고, 상위 HTTP 경계가 panic을 복구한 뒤 같은 backend의 다음 Save가 멈출 수 있었다.
두 entropy 경계에 defer unlock을 적용하여 원래 panic을 전파하면서 잠금·staging 정리와 기존 파일 보존을 유지했다.

- 이전 `27ba040ee8313a3351341e0d3072b5a15affeb8f`의 Filesystem만 overlay한 부정 대조는 stage/collision 두 runtime assertion에서
  `entropy panic retained serialization lock`으로 실패했다. 테스트가 영구 대기하지 않도록 잠금을 먼저 확인하며, 수정 소스에서는
  같은 backend의 실제 후속 Save/Open과 내용·이전 파일 보존·staging 0을 계속 검사한다.
- 수정 소스의 storage 전체 normal/race는 각각 16 roots / 52 PASS, 0.935/3.020초다. Skip·잘린 JSON·미실행 root가 없고
  실행 전후 비Markdown inventory `ba5b86ba762ec61392ce65f1581b5a335298fddee7c961127af8a48954ac99ce`가 같았다.
- go vet·gofmt·diff 확인을 포함한다. 공개 API·schema/generator·DB 의미가 바뀌지 않아 앞선 generated drift나 로컬 DB suite를
  반복하지 않았다. 수정한 Filesystem의 platform/소비자 조합은 다음 Hosted full이 소유한다.

Receipts는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/`의 `godj-storage-entropy-normal-7tzl6509`,
`godj-storage-entropy-race-_dazi5xe`, `godj-storage-entropy-regression-agm8q27r`다.
진행 중이던 [full 36665945911](https://github.com/progresshans/godj/actions/runs/36665945911)은 이 재현 가능한 소스 수정 때문에
취소했고 전체 완료 증거로 사용하지 않는다. 완료 상태는 cancelled이며 필수 owner 취소로 최종 집계도 failure다.
관찰 timeout으로 중단을 추정하거나 같은 소스의 실행을 중복 요청한 것이 아니다.
수정 소스의 새 full에서 필수 owner·최종 aggregate·같은 run의 새 capture와 Git source 결합을 다시 확인한다.


### 일반 모델 여러 행의 저장 계획과 파일 게시

2026-09-30, 기반 `e613660f8033a4e461a6fc419e01cfa4c1a77f19` 이후 `PreparedSet.SavePlan`의 changed/new/deleted 선택과
mutable typed 모델·제출 순서 저장·지연 collection·실패 연산까지의 결과를 구현했다. 기본 collection binding과 필수 delete
callback을 첫 쓰기 전에 검사하고 callback error를 그대로 보존한다. Set SaveFiles는 모든 행의 pure binding/name을 검사한 뒤
게시하고 실제 이름과 행별 partial/uncertain outcome을 반환한다. Helpdesk Admin report의 최종 인가·고유값·선택 열 저장·audit를
같은 계획에 연결했다. 삭제 정책·현재 권한·outer transaction terminal 결과는 여전히 애플리케이션이 소유한다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 영향 normal | Form/model·storage/uploads·Web·Admin 6 packages / 285 roots / 2,354 PASS / 1.853초 |
| 관련 race | 모델 여러 행 저장·파일/단일 저장 관련 21 roots / 97 PASS / 1.877초 |
| 생성 소비자 양 DB/race | ModelForm 저장과 FileField 2 parent roots PASS / 12.229초; 생성본 ModelForm 필수 73개와 FileField 필수 6개 이름을 각각 한 번의 PASS로 확인 |
| Helpdesk Admin 양 DB/race | SQLite/PostgreSQL 2 roots / 필수 inline 하위 62개 / 총 66 PASS / 61.411초 |
| 독립 native | 고정 Django 6.1/CPython 3.14.3의 모델 여러 행 저장 13개 관찰; 두 번 같은 원문 |
| 부정 대조 | unchanged 선택·제출 순서·전체 deferred key 사전 검사·전체 file 사전 검사·행별 partial receipt·원래 callback error 보존을 각각 훼손한 6개 overlay가 지정 runtime assertion에서 실패 |
| 실행 소유자 | relation/PostgreSQL의 FileConsumer 필수 sentinel 추가, 현행 ExecutionOwnerTests 3개 PASS; 수정한 필수 이름으로 완료한 소비자 JSON을 재평가하여 runs=passes=2 / skips=0 확인 |

기존 native 단일 Form 15개 비교도 보존했다. 새 13개는 mixed/reordered·unchanged·ORDER만 변경·collection만 변경·excluded
model clean만 변경·deferred/명시적 삭제·동일 new 모델 반복 저장·늦은 writer/collection 실패와 원자/비원자 결과다.
SQLite/PostgreSQL의 실제 행·관계 상태와 발급된 key를 비교했다. Native의 callback 역할 이름과 Go의 SetWrite.Kind는 원래
폼의 create/update/delete 의도이며 실제 SQL 종류나 commit 증명이 아니다. Go의 더 엄격한 identity/인가 규칙을 완화하지 않았다.

파일 소비자는 실제 multipart 네 행 중 변경/신규 두 행만 게시한다. 기존 삭제/삭제된 extra의 업로드는 게시하지 않으며,
게시 후 PROTECT 실패에서 앞선 DB update를 롤백하고 새 파일·원래 파일과 게시 receipt를 보존한다. 요청 종료 뒤 upload가 닫히고
임시 파일이 제거됨을 확인했다. 별도의 명시적 재검토 후 같은 저장 계획의 이미 게시된 이름으로 저장하며 자동 파일 재게시/재시도는
하지 않는다. 기존 생성 소비자의 migration·query·FileField/DB 재개방 검증도 실행했다. Helpdesk의 인증/CSRF·권한·cohort/revision·
unique·child-only audit·뒤쪽 실패·rollback/commit unknown 검사는 기존 실제 HTTP 경로를 통해 실행했다.

Go 1.26.5 Darwin arm64·offline/readonly module·기본 공유 cache/병렬 실행이다. 생성 자식도 `-trimpath -race`를 전달한다.
Compiled roots/필수 하위 이름·완료한 JSON·skip/잘린 출력·source 전후 불변을 확인했다. 각 DB 실행은 별도 PostgreSQL 17.10
Debian/C/UTF8 container/DB를 사용했고 종료 후 schema/table/다른 connection `0|0|0`, DB 제거와 해당 container 제거를 확인했다.

소스 범위를 합쳐 쓰지 않는다. 최종 비Markdown code/config inventory는
`13c5a2d1ac76222d180f823a5380e81f18ccc442d1f5d329d77c7ab34da21386`이다.
Normal/race/부정 대조는 `a9178d026c37f322df0ac4b965f96a5253b5736cb43bb9ab3f2703df8d25e852`에서 실행했고 이후 두 CI roster만
바뀌었다. 소비자 최종 실행은 `1beb3ccc58791ae3c870f30468326800dca26ac08063d119bb702b295d2384e1`이며 이후 core의 테스트
`forms/model/set_files_test.go`와 두 roster만 바뀌었다. Helpdesk 성공은 `5110e713d9f6c144fd22b6b240dfc359434424c352de8b8de1b7403c331ab83a`
실행의 해당 group 결과다. 이후 위 core 테스트·별도 File 생성 소비자 테스트와 roster만 수정됐다. 전체 파일 inventory 대조로
**모든 DB 검사의 runtime 제품 코드와 Helpdesk 테스트는 최종 소스와 동일**함을 확인했다. 수정한 core 테스트는 final normal/race로,
File 소비자는 수정한 양 DB 실행으로 검증했다. 서로 다른 전체 source를 같은 실행으로 표기하지 않고, 변경 없는 DB/Helpdesk 검사를
반복하지 않았다. 최종 결합 receipt는 `/tmp/godj-formset-save-validation/receipt.json`에 있다.

초기 normal/race는 새 테스트가 ValidateMin과 unchanged extra 선택을 잘못 기대하여 실패했다. 고정 Django에서 ValidateMin=true는
`too_few_forms`, false는 valid·changed=false·deferred 저장 0행임을 따로 관찰하고, 이미 core ActiveForms가 빈 extra를 제외한다는
현행 경계에 맞게 테스트를 수정했다. 최초 DB 실행은 File 소비자가 없는 `uploads.DefaultPolicy`를 참조하여 compile 실패했고
`DefaultConfig`로 수정했다. 같은 실행의 ModelForm 소비자와 Helpdesk group 성공을 전체 PASS로 쓰지 않았다. CI 검사 첫 호출은
경로/class 지정 오류로 실행되지 않았고 현행 ExecutionOwnerTests 3개를 지정하여 완료했다. 제품 저장 의미를 통과용으로 변경하지 않았다.

Local receipts는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/` 아래
`godj-formset-save-normal-siicyg6a`, `godj-formset-save-race-nqlmhwg9`, `godj-formset-save-controls-0pl396fe`,
`godj-formset-save-db-race-xtkm7emt`와 최초 DB/Helpdesk `godj-formset-save-db-race-qh8d2s38`에 있다.
고정 observer는 `conformance/runners/django/model_formset_save_reference.py`, golden은
`codegen/consumertest/testdata/modelsave/formset-reference.json`이다. Native forms/models.py SHA256은
`858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910`, golden SHA256은
`d8058ff7690397fd01c70e556d35913f7263daa2b23d6cd19b02428d07dead66`이며 commit/라이선스는 observer와 SOURCES를 따른다.

영향 8 packages의 go vet·gofmt/diff와 169개 문서의 local Markdown 링크 검사 PASS. 생성기·IR·모델 선언과 checked-in generated Go는 바꾸지 않아 generate-check를 반복하지 않았고,
별도 생성 module에서 현재 public API를 compile/실행했다. HTML/JS 변경이 없어 브라우저를 반복하지 않았다. 이번 source의 전체/cold·
CGO=0·Hosted full은 실행하지 않았으며 선행 `6d3fe97f`의 전체 결과를 전이하지 않는다. Storage alias·URL/인가된 serving·다른 backend와
나머지 기능 카탈로그는 계속 미완료다.



### 모델 FileField와 명시적인 파일/DB 참조 저장

2026-09-30, 기반 `6efec7283c59737488422edf8b3c1d94c623102f` 이후 IR `FieldFile`과 100자 기본 이름, 생성 string/*string,
공통 typed/dynamic 문자열 AST·관계 query, SQLite/PostgreSQL varchar·historical kind와 Char/Email 전환을 연결했다.
Model Form은 이름 후보와 요청 upload를 구분하며 typed 준비의 pending file은 Model/Save/SaveCollections를 막는다.
`SaveFiles`는 모든 saver/이름을 먼저 확인하고 실제 저장 이름과 필드별 게시 결과를 반환한다. Partial failure는 앞선 결과와
실패한 연산의 Info/error/outcome을 보존하며 DB transaction·재시도·기존/새 파일 삭제를 숨기지 않는다. Empty reference와
SQL NULL, nullable clear→빈 문자열을 구분하고 JSON은 명시적인 read-only 이름 투영만 허용한다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 영향 normal | schema/IR·codegen·ORM·Form/model·storage/uploads/Web·Admin·serializer/OpenAPI·migration/definition/autodetect 15 packages / 1,043 roots / 6,729 PASS / 7.612초 |
| 관련 race | 파일 Form/model·Admin·storage 4 packages / 22 roots / 109 PASS / 4.580초 |
| 양 DB 생성 소비자/race | 새 FileField의 외부 Go module과 JSON 실패 진단 2 parent roots PASS; 생성 소비자의 projection·storage root·SQLite·PostgreSQL 필수 4개 실행 PASS / 7.011초 |
| 생성물 drift | `make generate-check` PASS / 40.152초; 기존 7개 프로젝트와 checked-in 관계 생성물 검사 |
| 독립 native | 고정 Django 6.1/CPython 3.14.3의 모델 파일 폼 11개 관찰, 기본 100자·충돌 이름·DB rollback/clear/delete 이후 파일 보존; 두 번 같은 원문 |
| 부정 대조 | pending guard 제거·실제 이름 대신 제안 이름 반영·부분 receipt 제거·nullable clear를 NULL로 변경·Input의 업로드 capability 제거, 5개 overlay 모두 지정 runtime assertion에서 실패 |

Go 1.26.5 Darwin arm64·offline/readonly module·공유 cache/기본 병렬 실행이다. Normal/race와 양 DB checkpoint 및 부정 대조의
비Markdown code/config inventory는 모두 `223ccebdc765441fd1dd9e98ac560dc8b99cb7002585d48562848e58c9c54186`이며 실행 전후 같았다.
Compiled roots와 실제 run/pass를 대조했고 누락·skip·잘린 event·stderr는 없다. 생성 소비자는 기존 `-trimpath`/공유 cache를
사용하며 child 성공/필수 실행/잘리지 않은 JSON을 부모가 확인한다. PostgreSQL은 고정 17.10/C/UTF8의 별도 container/DB다.
종료 후 남은 schema/table/다른 connection `0|0|0`, DB 제거와 해당 container 제거를 확인했다.

실제 소비자는 FileField로 직접 만든 열과 Char→File→Char 역사 전환, nullable/빈 참조·unique·typed/dynamic/관계 query를
확인한다. Multipart→typed 준비→storage→DB 저장 뒤 request 임시 자원은 닫히며 DB와 파일 backend를 다시 열어 이름과
바이너리 내용을 확인했다. File publication 후 DB rollback은 이전 DB 이름과 새 파일을 각각 남기고 clear·모델 삭제도 파일을
자동 삭제하지 않는다. 이 격리 HTTP route가 production 인증을 구현했다고 주장하지 않는다. 별도 Admin 실제 로그인/CSRF
route는 모델 파일의 생성·교체·텍스트 위조 무시·오류 재선택·clear·빈 이름 유지와 요청 종료를 검증한다.

Native `django/db/models/fields/files.py` SHA256은 `fed8e0f0f32feb483bcc96fb16ce417f77493079981c252182a4fee17b4298c8`,
관찰 원문의 SHA256은 `72c54016035e3ca7f19d30aadd9f0125d9a6f46d073656d68c11f270541cda68`다. 출처는 BSD-3-Clause다.
Pure naming callback·명시적 SaveFiles·파일 PostClean/choices 제한·read-only JSON 참조는 GoDj의 현재 경계로 문서화했다.

첫 normal 실행은 Admin 검증 실패 화면을 400으로 잘못 기대한 테스트에서 실패했다. 실제 기존 규약은 오류 표시와 HTTP 200이고
저장 callback은 호출되지 않았다. 테스트를 그 규약에 맞췄다. 첫 생성 소비자는 존재하지 않는 QuerySet.Get/Delete를 사용해
compile 실패했다. 현행 All의 단일 결과 확인과 project relation deleter로 고쳤다. Go JSON envelope 전체를 redaction한 기존
실패 요약이 실제 compiler 오류를 숨겨 Output만 추출해 같은 sanitizer에 전달하도록 보완하고 JSON/plain 진단 회귀를 추가했다.
실패를 전체 PASS로 처리하지 않았으며 위 최종 checkpoint는 이 수정 뒤 source다. 첫 양 DB 실행에서 기존 Email 생성 소비자만
PASS였던 결과는 별도 선행 회귀이며 새 File 소비자의 최종 실행을 대체하지 않는다.

- normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-files-normal-znxz1nyw`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-files-race-e_gbgc8i`
- DB/race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-files-db-race-6r_fdjgi`
- controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-files-controls-kazcf717`
- native: `forms/model/testdata/model-file-django61.json`, `conformance/runners/django/model_file_reference.py`
- 선행 storage source `6efec728`의 [Fast 36544917169](https://github.com/progresshans/godj/actions/runs/36544917169)는 success로 확인했다.

영향 go vet, gofmt, 169개 Markdown 링크와 diff 검사를 통과했다.

전체/cold/Hosted platform 검증을 반복하지 않았다. 마지막 닫힌 full은 위 파일 변경 이전 source `6d3fe97f`의
[36526909898](https://github.com/progresshans/godj/actions/runs/36526909898)이며 새 FileField에 그 결과를 전이하지 않는다.
다른 OS·storage alias/추가 backend·URL/인가된 serving·ImageField·자동 orphan 회수·일반 ModelFormSet 전체 자동화는 남아 있다.


### 로컬 파일 storage와 요청 이후 수명

기반 `ec18a213528094f3994ce5e70def9ff47b282cef` 이후 `storage.Backend`와 os.Root 기반 Filesystem을 구현했다. 전체 상대
이름·파일 크기·collision 시도 예산, private staging·File.Sync/Close 뒤 no-replace hard link, 독립 reader/Close를 제공한다.
Save는 borrowed reader를 닫거나 재탐색하지 않으며 SaveUpload는 자신이 연 업로드 reader만 닫는다. Info는 실제 저장 이름과
크기를 담은 metadata이며 권한이나 DB commit 증명이 아니다. 게시 전 실패·게시 후 정리 실패·결과 불확실을 구분한다.
모델 FileField의 IR/생성/ORM/Form 저장과 storage alias·다른 backend·URL/serving, DB/파일 결과 조정은 이번 범위가 아니다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| normal | storage·uploads·forms·web·두 attestation 6 packages / 158 roots / 1,778 PASS / 5.442초 |
| 관련 race | Filesystem·업로드 수명·source binding 6 packages / 33 roots / 227 PASS / 3.723초 |
| native | 고정 Django 6.1/CPython 3.14.3 FileSystemStorage 7개 이름/내용/오류 관찰, 기존 내용 보존·없는 파일 삭제 관찰을 두 번 실행해 같은 원문 |
| 부정 대조 | 기존 destination 삭제 후 게시·파일 예산 제거·staging 정리 제거·불확실을 미게시로 축소·entropy 뒤 취소 검사 제거의 5개 overlay가 지정 runtime assertion에서 실패 |

Go 1.26.5 Darwin arm64·offline/readonly module·Pacific/Chatham·기본 공유 cache/병렬 실행이다. 각 실행 전후 source inventory는
`f4ca8cbc64b8dfcbd74e32cc2c6477ef6ddcd5bf3ac3fcd4d55b5888b5d97426`로 같고 compiled roots와 실제 run/pass를 대조했다.
누락·skip·잘린 event·stderr는 0이다. 프로세스 검사는 새 Go build를 반복하지 않고 현재 test binary의 helper를 두 번 실행해
기존 파일과 각 process의 내용을 각각 보존함을 확인한다. 같은 root를 연 8개 독립 인스턴스/goroutine도 서로 덮어쓰지 않는다.

Actual Application.ServeHTTP 소비자는 multipart/Form을 통과한 바이너리를 저장한 뒤 request upload의 Open 거부/임시 정리,
backend Close/재개방 후 동일 내용을 확인한다. source가 아직 읽히는 동안 최종 이름은 없으며 읽기 오류·파일 예산 초과·취소·
reader panic은 부분 파일을 게시하지 않는다. traversal·절대 경로/backslash·제어문자/Windows 예약 이름·예약 staging 이름과
root 밖 symlink를 통한 Save/Open/Delete를 거부하고 외부 파일을 보존한다. 디렉터리 삭제 거부, 없는 파일 분류/삭제 멱등,
이름/Unicode 길이/compound suffix·빈 파일·entropy 실패·bounded collision·일반 formatting 비공개·POSIX mode도 확인했다.
Windows ACL과 다른 OS 실행은 이 로컬 결과에 포함하지 않는다.

비공개 rootOperations fault port는 실제 filesystem 연산을 유지하면서 성공한 Link의 응답 유실과 staging Remove 실패를 주입한다.
오류가 있어도 새 파일이 실제 존재하는 경우에 후보 Info와 Uncertain/Published가 보존되고 원래 파일도 남는지 확인했다.
이는 실제 정전/디스크 손상 실험이나 directory entry의 power-loss durability 검증이 아니다. 강제 종료 후 orphan staging 회수와
DB 저장 결과/보상 정책을 구현했다고 주장하지 않는다.

Native base/filesystem source SHA256은 각각 `774578b9e5c43daf46fb4fb5f0d020b96c2c4c1df795b4100d8643a0296203c9`,
`58590ed08e59875f970c889188deeb1d914a56d61da7e750652fd797a631ffeb`이다. Random spelling을 고정해 실제 collision/truncation을
대조했다. Portable 이름 제한·private staging·bounded 실행과 불확실한 결과 분리는 명시적 GoDj 정책이다.

처음 덮어쓰기 부정 대조는 두 process가 destination을 보기 전에 함께 진행하는 스케줄에서 통과했다. 기존 파일을 먼저 둔 뒤
그 내용과 두 새 파일이 모두 남도록 process 회귀를 보강했고 같은 mutation이 지정 assertion에서 실패함을 확인했다. 취소
부정 대조도 별도 미사용 suffix를 갖는 target을 사용해, 다음 collision의 취소 검사에 우연히 의존하지 않도록 했다. 최종
normal/race와 5개 부정 대조는 이 보강 뒤 source다. 실제 source는 overlay로 수정하지 않았다. Go vet·gofmt·169개 문서 링크·diff
검사를 통과했다. IR/generator/DB writer를 바꾸지 않아 generated drift·DB matrix·cold/local full을 중복하지 않았다.

- normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-storage-normal-z5w_592f`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-storage-race-dtaaixul`
- controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-storage-controls-371gcmzc`
- native: `storage/testdata/filesystem-django61.json`, observer `conformance/runners/django/storage_reference.py`
- 선행 Admin 파일 source `ec18a213`의 [Fast 36541475262](https://github.com/progresshans/godj/actions/runs/36541475262)는 success로 확인했다.



### Admin 파일 widget·multipart·요청 수명 연결

기반 `29c86df1f8c4014013613e539d5523a38d2218bb` 이후 부모 생성·수정·inline·명령의 Form 입력에 파일 capability와 clear를
전달했다. 편집 가능한 inline의 zero-extra prototype도 부모 multipart를 활성화한다. 위젯은 기존 이름/clear·required를
구분하고 파일 value나 임의 URL을 출력하지 않는다. 오류 화면은 파일 재선택을 안내한다. 파일-only hidden inline·비파일
필드/PK/management/CSRF의 파일 part를 거부하고 readonly 기존 행은 파일을 채택하지 않는다. 기존 명령/저장 재검증도
FileValue를 허용하되 파일 수명을 늘리거나 영구 저장을 수행하지 않는다.

Multipart 본문 전 `InspectPrincipal`로 session 무변경 staff/site/route admission을 확인하고 파싱 후 CSRF·현재 인가를 다시
검사한다. `SiteConfig.Uploads`는 immutable snapshot이며 Admin의 값당 4 KiB·문자열 합계 64 KiB·입력 1,024개 상한도 유지한다.
실제 body I/O·임시 저장·취소/정리 오류는 입력 오류로 낮추지 않는다. Wire 예산이 정확히 소진된 뒤의 reader 오류도
`read_failed`와 원래 cause를 보존하도록 고쳤다. 파일/clear 생략을 빈 supplied key로 바꾸지 않는다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| normal | admin·uploads·forms·forms/model·web·두 attestation 7 packages / 296 roots / 2,503 PASS / 6.437초 |
| 관련 race | Admin 파일/inline·업로드/파일 Form·source binding 7 packages / 47 roots / 297 PASS / 5.096초 |
| 기존 양 DB HTTP/race | Helpdesk Admin 부모/자식 합성 저장 SQLite·PostgreSQL 2 roots / 필수 62 cases / 66 PASS / 57.278초 |
| 실제 브라우저 | Playwright CLI 0.1.22·Chrome UA 154.0.0.0, 독립 loopback SQLite의 기존 33개 + 파일 17개 조건 PASS |
| 부정 대조 | 생략 보존·본문 전 admission·파일 전달·file-field allowlist·요청 정리·정확한 wire 상한 뒤 I/O 분류를 제거한 6개 overlay가 지정 runtime assertion에서 실패 |

Go 1.26.5 Darwin arm64·offline/readonly module·Pacific/Chatham이며 공유 cache·기본 병렬 실행을 사용했다. 각 checkpoint의
compiled root와 실제 run/pass를 대조했고 누락·skip·잘린 event·stderr는 0이다. PostgreSQL은 고정 17.10/C/UTF8의 별도
container와 DB를 사용했다. 실행 후 schema/table/다른 connection `0|0|0`, DB 제거와 해당 container 종료/제거를 확인했다.
브라우저 두 probe는 각각 새 SQLite DB에서 실행했다. 파일을 고른 행의 앞 행을 제거해도 File 객체/내용이 보존되고, 실제
transaction에서 내용 불일치로 거부하면 자식 cohort가 늘지 않으며 원문/진단·재선택 안내가 남는다. 재선택 후 저장과 재조회,
readonly file disabled·successful control 제외를 확인했다. Fixture는 파일 내용을 검증하는 비저장 명령이며 파일 storage
구현 증거가 아니다. Console 오류는 fixture의 `/favicon.ico` 404 한 건이고 JavaScript 실행 오류는 없었다.

고정 Django 6.1/CPython 3.14.3에서 required/optional·기존 파일·일반/clearable widget의 8개 HTML 관찰과 zero-extra Formset의
multipart/prototype 관찰 1개가 통과했다. 기존 파일의 required 생략·file input value 없음·optional clear 조건과 일치한다.
Django Stored.url의 링크 기능과 달리 GoDj의 opaque ExistingFile 이름은 URL 권한이 아니므로 텍스트로만 표시한다. 기존 22개
FileField 차등 관찰은 위 normal에도 포함된다. Native widgets/formsets SHA256은 각각
`e743c7612a74ca1106ef1ee9aca760e7bcbff1901be7d186b1bb1f63ee830b47`,
`de64ae2a70f2e85b2b479b93cea8fb315f39ab9beaf9840d2306c842f9e71380`이다.

최종 normal/race/DB 실행의 원래 비문서 inventory는 모두 `f165604de408354e4bddc0d1eb4ebe082def0e20416f7ea7b155f9961e215bbe`다.
이 inventory에 함께 들어간 자동 생성 `.playwright-cli` console/snapshot 2개를 제외한 2,722개 code/config 파일의 digest는
`953bc562db816e60ffd7f070f0c57d5bbc0311f1ad9ad50f84fb6538acca4fe3`이다. 각 실행의 before/after 목록과 실제 최종 파일을
내용·크기별로 대조해 같음을 확인했으며 원래 receipt는 수정하지 않았다. 브라우저 산출물은 무시되는 output/playwright로 옮긴다.

초기 test fixture는 examples/helpdesk root를 admin 내부 테스트에서 import해 순환 의존성이 생겼다. 생성 모델/metadata와
독립 앱 등록을 사용하도록 수정했다. 비staff를 Admin 로그인 성공으로 준비한 기대도 기존 admission 정책에 어긋나 제거했다.
비staff 로그인 거부는 기존 Site 검사가 계속 소유하며 새 body-before-admission 검사는 익명·모델 권한 없음·view-only·deny
추가 정책을 확인한다. 첫 집중 검사 후 누락 키 보존 문제를 검토에서 찾아 실제 최종 callback 회귀를 추가했고 영향·race·양 DB와
브라우저를 최종 source로 다시 확인했다. 이전 source의 결과를 위 최종 수치에 합산하지 않았다. 부정 대조는 build-fail/skip이
아닌 runtime assertion 실패이고 실제 source를 바꾸지 않았다. Go vet·gofmt·브라우저 probe 문법·167개 문서 링크·diff 검사 통과.
IR/generator/DB writer를 바꾸지 않아 generated drift·cold/local full을 추가하지 않았다. 모델 FileField/storage와 DB/파일 결과
연계, 다른 OS 및 새 Hosted 전체는 후속 통합 범위다. 선행 source `29c86df1`의 Fast `36537367025`는 success로 확인했다.

- normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-upload-normal-0l68w7sf`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-upload-race-bssk_b9w`
- DB/race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-dynamic-inline-race-b2lnsdnw`
- controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-upload-controls-qvib7ak0`
- source 대조: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-upload-source-a0wbrnf3`
- browser 원문: `/tmp/godj-admin-upload-browser-baseline.log`, `/tmp/godj-admin-upload-browser-files.log`
- native observer/원문: `/tmp/godj-admin-upload-widget-reference.py`, `/tmp/godj-admin-upload-widget-reference.json`


### Multipart 파일 입력과 Form/Formset 수명

기반 `db1e85e2a716dad9a4c0eb05768580d2217aea22` 뒤 `uploads`의 bounded multipart와 메모리/임시 파일·독립 reader 수명을
구현했다. Request는 결과와 실패를 한 번만 파싱하고 정상 반환/오류/panic에서 임시 자원을 닫는다. Parser는 한도·취소·
malformed body와 reader panic에서도 부분 업로드를 게시하지 않고 정리한다. FileField는 metadata로 기존 참조/새 파일/clear를
구분하고 Formset prefix·빈 extra·readonly·삭제 및 typed ExtraFields 명령에 연결한다. Admin 파일 UI·모델 FileField·영구
storage/DB 쓰기와 인증된 파일 serving은 이 구현과 검증에 포함하지 않는다.

고정 Django 6.1/CPython 3.14.3에서 `file_form_reference.py`를 두 번 실행해 같은 22개 관찰을 얻었다. 필수/optional·기존 파일
유지·새 파일/빈 파일·Unicode filename 길이·clear와 충돌·required/plain widget·POST 문자열 위조·callback 횟수를 비교했다.
20개는 관찰한 결과/진단/변경/validator 실행과 일치한다. 반복 파일에서 마지막 값을 선택하는 동작과 arbitrary 문자열 clear는
GoDj가 각각 `multiple`/`invalid`로 거부하는 명시적 차이다. Field/widget source SHA256은 각각
`2b756230a12f2329e2796314dd99fc57a8d0b016658863be2e899a2988d33f66`,
`e743c7612a74ca1106ef1ee9aca760e7bcbff1901be7d186b1bb1f63ee830b47`이다.

| 실행 | 범위와 실제 결과 |
| --- | --- |
| normal | uploads·forms·forms/model·web·두 attestation·admin 7 packages / 287 roots / 2,452 PASS / 2.345초 |
| 관련 race | 업로드/파일 Form/typed 명령/실제 HTTP와 source binding 6 packages / 27 roots / 207 PASS / 3.783초 |
| 부정 대조 | 값 formatting 비공개·종료 경계 확인·공유 메모리 예산·upload/clear 충돌·request 정리·reader panic 정리를 각각 제거한 6개 overlay가 지정 runtime assertion에서 실패 |

Go 1.26.5 Darwin arm64, offline/readonly module·Pacific/Chatham·공유 cache·기본 병렬 실행이다. 실제 HTTP server/client는
바이너리 payload의 SHA256, 파일만 받은 extra 행, invalid input/handler error/panic/413, 재파싱 금지와 종료 후 capability
거부를 확인한다. 메모리-only에서는 디스크 무쓰기, 전역 예산 초과의 private spill·독립 cursor, partial 실패/취소/reader panic의
임시 디렉터리 정리·다른 파일 보존·동시 Open/Read/Close와 일반 formatting 비공개를 확인했다. 이 테스트는 영구 DB/파일 저장
성공이나 업로드 endpoint의 인증 정책 검증이 아니다. IR/generator/DB writer를 바꾸지 않아 DB matrix·generated drift·cold/local
full을 반복하지 않았다. 새 package는 기존 core 분류에 포함되고 두 attestation의 product prefix가 소스를 소유한다.

최종 normal/race 전후 비문서 inventory는 `0cd0318911a1125efb897c169791a84bdb172323116cb347b49af2bd2167f02d`로 같고
compiled root 목록과 실제 run/pass를 대조했다. 실행 누락·skip·잘린 event·stderr는 0이다. 부정 대조는 실제 runtime 실패이며
build-fail/skip이 아니고 실제 소스는 바꾸지 않았다. 별도 OS/browser/전체 platform 검증으로 확대하지 않는다.

첫 normal은 잘못된 multipart 입력의 EOF를 성공으로 받아 실패했다. Wrapped EOF를 거부한 뒤에도 MIME parser가 part header
누락에서 plain EOF를 반환하는 사례를 발견해 실제 종료 delimiter 관찰을 더했다. 빈 form의 LF-only 종료는 표준 Go parser가
허용하지 않는 fixture 기대여서 CRLF/끝 newline 생략/transport padding/epilogue와 분할 입력을 확인하도록 고쳤다. 마지막
검토에서는 reader panic 중 아직 열린 spool writer도 닫고 Parse의 자원을 정리하도록 보강했다. 위 성공은 이 수정 뒤 source의
결과이며 이전 실패를 PASS에 합치지 않았다.
공개 Reader를 private cursor state의 handle로 바꿔 값 복사가 lock을 복제하지 않게 하고 Form/Reader/Error의 값 formatting도
비공개로 유지했다. 공유 cursor·복사본 Close와 실제 formatting 부정 대조를 확인했다. Windows의 os.FileMode 0666을 POSIX
권한으로 오해하지 않게 해당 mode 검사는 POSIX에 적용한다. Windows ACL 검증은 이 로컬 실행에 포함하지 않는다. 최종
영향 go vet·gofmt·167개 문서 링크·diff 검사도 통과했다.

- normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-file-input-normal-ieoh9qqc`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-file-input-race-lz26_g9z`
- controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-file-input-controls-43jvl_p1`
- 초기 실패: `/tmp/godj-file-input-normal.jsonl`, `/tmp/godj-file-input-normal-final.jsonl`, `/tmp/godj-file-envelope-normal.jsonl`
- native 원문: `forms/testdata/file-django61.json`

### Admin 동적 행 UI와 실제 브라우저 소비자

기반 `6d3fe97f3e6cbb2103c72122c2dd25cceed97d9f` 이후 서버의 EmptyForm·권한별 inert prototype과 외부 JavaScript를
연결했다. 미저장 행만 min/max 안에서 추가/제거하며 기존 PK·INITIAL_FORMS·다른 inline과 입력값/checkbox 상태를 보존한다.
오류 재표시의 구조적 이름/anchor도 재번호하고 사용자 텍스트에 있는 `__prefix__`/이전 행 이름은 수정하지 않는다.
내용 SHA256 asset의 public cache·session 무변경·login next 제외를 검사했다. 실제 embed한 JS를 두 attestation의 정확한
source 경로에도 포함하고 변경 감지 회귀를 추가했다. 기존 Helpdesk HTML 제출 helper는 inert template 안의 입력을 제외한다.

| 실행 | 범위와 실제 결과 |
| --- | --- |
| normal | admin/forms-model/두 attestation의 Inline·source-binding 24 roots / 193 PASS / 1.585초; 양 DB Admin HTTP 2 roots / 필수 62 cases / 66 PASS / 10.004초 |
| race | 같은 pure 24 roots / 193 PASS / 3.294초; 같은 양 DB HTTP 2 roots / 필수 62 cases / 66 PASS / 56.663초 |
| 실제 브라우저 | Playwright CLI 0.1.22, Chrome UA 154.0.0.0, 별도 loopback SQLite 소비자 33개 조건 PASS |
| 브라우저 부정 대조 | asset 응답의 최대 행 수 guard만 제거한 1회 주입이 `programmatic click cannot exceed the UI maximum`에서 실패 |

Go 1.26.5 Darwin arm64·PostgreSQL 17.10 UTF8/libc/C·SQLite, offline/readonly module·Pacific/Chatham·기본 공유 cache와
병렬 실행이다. 성공한 normal/race의 전후 비문서 inventory는
`2134a9be74ed21e668a1054231a5b77a3744e5ef747d241abbfd4af48af130d1`로 동일하다. Compiled root 목록과 required 하위
run/pass를 대조했으며 누락·skip·잘린 event는 0, DB 잔여 `0|0|0`과 임시 DB/container 삭제도 확인했다. IR/generator를
바꾸지 않아 generated drift·cold build·전체 compile을 추가하지 않았다. 새 UI의 다른 OS·browser·CGO mode 전체는 미검증이다.

[브라우저 fixture와 재현법](../../admin/testdata/browser/README.md)은 공개 API로 Category의 두 HasMany inline을 구성한다.
ExtraForms 0·min/max·독립 count/identity·default/checkbox·focus·추가/제거 이벤트·오류 행 재번호·no-add/readonly/view-only,
수정 후 readback과 새 부모/자식의 실제 저장을 확인했다. 동일한 prototype을 사용하는 Helpdesk OneToOne의 transaction/audit는
위 양 DB HTTP 회귀로 확인한다. 브라우저 fixture의 synthetic 인증은 운영 인증 검증으로 확대하지 않는다.

첫 DB checkpoint는 disabled fieldset에 data 속성을 더한 뒤 남아 있던 literal HTML 비교에서 실패했다. DOM을 파싱해 실제
일반 필드의 disabled·독립 PK·부모 mutation form 부재를 검사하도록 고치고 위 normal/race를 실행했다. 브라우저 fixture의
부모 First 조회에는 명시적 정렬이 필요했고, probe의 숨겨진 버튼/목록 링크 선택도 수정했다. 부정 대조 후 같은 세션의 정상
재실행은 변조 영향이 남아 실패했으므로 새 세션에서 원래 asset으로 33개 전체를 다시 확인했다. 첫 결과 추출 스크립트는
성공 JSON 뒤 CLI 로그까지 JSON으로 파싱해 실패했다. 원래 로그에서 하나의 JSON 결과를 읽어 33개를 확인했으며 브라우저를
반복 실행하지 않았다. 초기 favicon 404는 관찰했고 마지막 페이지의 console error는 0이다. 이 실패들을 성공으로 합치지 않는다.

- normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-dynamic-inline-normal-_izuh1wf`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-dynamic-inline-race-an4ijtlf`
- 최초 DB 실패: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-dynamic-inline-normal-rikndhmb`
- 브라우저 원문: worker의 `output/playwright/replay-clean.log`, `negative.log`, `browser-receipt.json` (ignored local artifacts)

고정 Django inline script는 동작 참고로 읽었지만 새 native 브라우저 differential을 실행한 것은 아니다. Files와 전체
ModelFormSet 저장 자동화는 남아 있으며 선행 `6d3fe97f`의 Hosted full에 이 UI 변경을 포함시키지 않는다.
최종 비문서 source가 위 normal/race와 같음을 재확인했고 `make format-check docs-check`(166개 문서)와 diff 검사를 통과했다.

### Admin 합성 저장 source의 Hosted 통합 완료

Source `6d3fe97f3e6cbb2103c72122c2dd25cceed97d9f`의 [PR feedback 36526887945](https://github.com/progresshans/godj/actions/runs/36526887945)은
terminal success다. [Hosted full 36526909898](https://github.com/progresshans/godj/actions/runs/36526909898), attempt 1은
최종 completed/success이며 62개 job이 모두 success다. 집계 job `109290129336`의 `scope: full`과
`full_platform_verified: true`, 같은 Git source의 scopes.py가 요구하는 8개 owner 일치를 확인했다.
기다리는 동안 실행을 교체하거나 로컬 전체를 중복하지 않았다.

같은 run/attempt의 새 capture를 내려받아 archive digest·파일 집합·SHA256SUMS·producer/provenance를 검증했다. 현재 dirty
파일이 아닌 해당 Git commit에서 source binding을 재계산해 systemstate 644 files / SHA
`5cbade384a5a86dbc4f7bb3ccc23495b1ed02b476772dea39273e2f286611c3f`, operator 721 files / SHA
`e9c97e13938c3e277b29561c75b1ddbb35e65d9a6f0e8e3adbc00aada9dafd0e` 일치를 확인했다.

| capture | artifact / producer job | payload SHA256 |
| --- | --- | --- |
| systemstate-postgres-1 | 11015278425 / 109271888854 | `22908a10d7d6bc1303946e6d3b01f0cf3016fbc95aa8bbc453737d3a99124e3a` |
| operator-postgres-1 | 11014897454 / 109271888891 | `491eb375b991f3c4d5cf304cca1067c01863b13611d865bec246c3fc56ce1e34` |

원문·source inventory·최종 `pass: true` receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-inline-integration-full-36526909898-9gg5e6lj`에 있다.
이 통합 source는 위 동적 행 UI와 새 JS source inventory 정책·파일 입력을 포함하지 않는다. 동적 UI source `db1e85e2`의
[PR feedback 36532302315](https://github.com/progresshans/godj/actions/runs/36532302315)도 terminal success를 확인했다.

### Admin inline HTML과 부모·자식 원자 저장

기반 `00e07e1f4552f895dae3397b4687a89892566555` 뒤 Admin Inline을 canonical 부모 registration에 연결했다.
권한별 current 조회/readonly/추가/DELETE, 합산 allocation·input 예산, parent/PK/optional revision, immutable 제출과 오류
재표시를 구현했다. Create/Update callback은 필수 InlineSubmission을 받고 실제 소비자 전부를 새 계약으로 compile/검증했다.
Helpdesk의 opt-in AdminRegistry는 같은 transaction에서 티켓·OneToOne 보고서·양쪽 audit를 저장한다. 원래 Application과
독립 Registry는 변경하지 않는다. Callback 자체가 transaction을 소유하며 공통 Site가 개별 행별 commit을 실행하지 않는다.

실제 HTML의 성공 제어(ancestor disabled, textarea, select, hidden PK/management)를 읽어 다시 POST한 31개 사례를
SQLite/PostgreSQL 각각 실행했다. 새 부모/자식·child-only·무변경·긴 본문·invalid 부모/자식·삭제·readonly/Add-only/deny
overlay·부모/자식 위조·개수/unknown 입력·삭제 전 DB unique·늦은 parent scope/cohort/자식 쓰기/audit 실패·rollback/commit
unknown을 확인했다. Unknown outcome은 500으로 남고 자동 재시도하지 않는다. 성공한 child-only 변경은 부모 audit에
`reports`를 남기며 무변경은 audit를 추가하지 않는다. 실제 DB의 후속 상태와 감사 기록도 대조했다.

| 실행 | 정확한 범위와 결과 |
| --- | --- |
| normal | forms/model·admin·Helpdesk·identity/admin·Article Admin 5 packages / 159 roots / 필수 532 cases / 1,416 PASS / 24.289초 |
| pure race | Admin inline 4 roots / 10 PASS / 1.831초; origin/인가·readonly·bounds·revision/DELETE·동시 입력 소유권 |
| 실제 HTTP race | 양 DB 2 roots / 필수 62 cases / 66 PASS / 58.362초 |
| 부정 대조 | readonly admission 제거, 부모 audit 오류 무시, invisible inline 입력 거부 제거의 3개 overlay가 지정 runtime assertion에서 실패 |

Go 1.26.5 Darwin arm64, SQLite/PostgreSQL 17.10 UTF8/libc/C, offline/readonly module·Pacific/Chatham·기본 공유 cache와
기본 병렬 실행이다. 성공한 normal/race 전후 비문서 source inventory는 모두
`3fb2f4c6965d6f85cf4049b6a1e6d06e7065c02465ded9a2c6066a48395a1003`이다. Compiled root 목록과 required 하위 사례의
실제 run/pass를 대조했고 누락·skip·잘린 event는 0이다. DB 잔여 `0|0|0` 및 임시 DB/container 삭제를 확인했다.
부정 대조는 build-fail/skip이 아니며 overlay 밖 실제 source가 같았다. 신규 HTTP 62개를 Hosted required roster에도 넣었다.
IR·generator를 변경하지 않아 generated drift/cold build를 추가하지 않았고, 공개 callback의 모든 저장소 소비자와 external test
package를 위 영향 compile/실행에 포함했다. 별도 설치 archive/다른 OS·CGO mode는 다음 Hosted 통합 소유다.

첫 checkpoint에서는 fixture의 PK presence 누락·Admin의 기존 302와 잘못된 303 기대·제약 없는 Text의 잘못된 max_length 기대와
실제 삭제 후 audit ID 소실을 검출했다. 삭제 전 ID를 보존하고 긴 본문을 bounded audit label로 복사하지 않게 고쳤다. 이후
두 checkpoint에서 무변경 fixture의 nullable/blank 초기값 차이로 audit가 추가되는 것을 검출했고, details는 NULL·resolution은
빈 문자열이라는 실제 HTML 변환에 맞춰 fixture를 고쳤다. 마지막 진단은 SQLite의 해당 사례만 실행했다. 이 실패들을 성공으로
합치지 않았으며 최종 소스의 위 normal/race와 DB 상태를 다시 확인했다. gofmt·165개 문서 링크·diff 검사도 통과했다.

- normal/controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-inline-normal-hrvr16ui`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-admin-inline-race-osa3pihq`
- 초기 실패: `godj-admin-inline-normal-yk2o_r2n`, `godj-admin-inline-normal-sixvnd8i`, `godj-admin-inline-normal-jk0kcvkr` (같은 임시 부모 디렉터리)

고정 Django의 기존 inline/readonly 관찰과 삭제 전 고유성 계약을 연결했다. 새로운 Django 전체 Admin HTTP 동등성이나
동적 행 JavaScript/files 완료를 주장하지 않는다. `00e07e1f`의 [PR feedback 36522197482](https://github.com/progresshans/godj/actions/runs/36522197482)은
terminal success를 확인했다. 아래 선행 Hosted full은 이후 Inline/readonly/Admin source의 전체 증거가 아니다.

### 조회 전용 기존 행과 추가 행의 분리

기반 `ed1dc79f932e8cc552326f1b23c4da78d656b63c` 이후 `SetConfig.ReadOnlyInitial`과 `Form.ReadOnly()`를 연결했다.
기존 행의 일반 field/cross/model clean을 실행하지 않고 서버 initial을 cleaned 값/모델 후보에 보존한다. 추가 행은 정상
검증하고 구성에서 켠 ORDER/DELETE는 독립 제어로 처리한다. 형제 고유성에는 조회 전용 행을 포함하지만 저장용 Input과
독립 Instance.Prepare를 거부하고 PreparedSet.Rows에서는 제외한다. PK/cohort·parent·count/전체 진단은 계속 적용한다.
Admin 값 출력은 readonly에서 제출 대신 Initial을 사용하며 nullable Boolean·선택 값·PasswordInput 비공개 정책을 유지한다.

고정 Django 6.1/CPython 3.14.3에서 독립 `admin_inline_readonly_reference.py`가 실제 Admin `_create_formsets`와 SQLite를
사용해 10개 관찰을 생성했다. `django.contrib.admin.options` SHA는
`7526be63d08ff1338acc3e78e56171683bcdc697d32ce4af1de17aff8f328a48`이다. Unbound·누락/위조/반복 일반값·좁아진 입력 길이·
새 행·기존 행과 중복·invalid extra·삭제·삭제 후 같은 값 추가를 관찰하고 DB 무변경/정리를 확인했다. Go의 서버 후보·쓰기/삭제
선택을 대조한다. 삭제할 readonly 행의 일반 callback/오류 생략은 명시적 차이다. Native는 같은 unique 값의 추가를 삭제 전 DB
검사에서 거부하지만 pure Go 준비에는 DB 읽기가 없어 해당 삭제/추가 의도를 준비한다. 이를 native Admin의 전체 동등성이나
DB 검증 완료로 합치지 않는다. Admin 권한별 전체 HTML/합성 저장은 아직 연결하지 않았다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| normal | forms/model/admin/Helpdesk 4 packages / 197 roots / 130 required cases / 2,175 PASS / 23.219초 |
| pure race | 3 packages / 5 roots / 15 PASS / 1.940초; readonly initial·독립 준비 거부·동시 준비·안전한 표시 |
| 실제 저장 race | 양 DB `inline_readonly`, 1 root / 2 required cases / 5 PASS / 4.067초 |
| 부정 대조 | initial 고정 제거→위조/반복 입력 검증, readonly Input guard 제거→독립 쓰기 준비, readonly unique 참여 제거→기존 행과 새 행 중복 허용을 각각 검출 |

양 DB의 public InlineSpec 소비자는 위조된 기존 이름을 무시하고 새 행 한 개만 준비/저장해 기존 행이 유지됨을 확인했다.
Go 1.26.5 Darwin arm64, PostgreSQL 17.10 UTF8/libc/C와 SQLite, readonly/offline module·Pacific/Chatham·기본 공유 cache/
병렬 실행이다. 위 normal/race 전후의 비문서 inventory는
`5617dc14b71826a60ca53751079ca43c5d8131f314d764d700256acadd1eedd3`이다. Compiled root 목록과 필수 하위 run/pass를
대조했고 성공 scope의 누락·skip·잘린 event 0, DB 잔여 `0|0|0`과 임시 DB/container 삭제를 확인했다. 세 overlay는 빌드 오류나
skip이 아닌 지정 runtime assertion에서 실패했다. IR/generator를 바꾸지 않아 generated drift·CGO0·전체 compile을 추가하지 않았다.
마지막 검토에서 일반 편집 행에 사용하지 않을 후보 복사본을 만들지 않게 `forms/model/set.go`의 분기를 정리했다.
최종 inventory `067c494bd75179be14b39334f5429a83a804d451b9e0b270ee2f17bf5747f1b2`와의 유일한 비문서 차이는 그 파일이다.
최종 모델의 Inline/Readonly 6 roots를 normal/race 각각 38 PASS로 확인했으며 앞선 전체 affected/양 DB 실행을 반복하지 않았다.
변경 Go의 gofmt와 local Markdown 164개 링크·diff 검사도 통과했다.

- normal/controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-inline-readonly-normal-y5atbh5d`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-inline-readonly-race-98i4ydly`
- native 원문: `forms/model/testdata/admin-inline-readonly-django61.json`
- 최종 모델 분기 검사/source: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-readonly-final-model-nkr8bom3`

### Formset/Helpdesk 통합 source의 Hosted full 완료

[Hosted full 36516565253](https://github.com/progresshans/godj/actions/runs/36516565253), attempt 1, source
`6d8afda5086ba3fc058376a60dc567bdcd5a05d7`은 최종 success다. 62개 job이 모두 completed/success이고 최종 집계 job
`109254088312`의 `scope: full` / `full_platform_verified: true`를 확인했다. 같은 Git source의 `scopes.py`와 대조해
portable Go·PostgreSQL·관계·command·project check·고정 darwin·Python·conformance의 필수 owner 8개가 모두 성공함을 확인했다.
이는 core/typed Formset·Helpdesk HTTP·여러 행 고유값·CI 선택자 수정까지의 통합이며 이후 Inline/readonly 소스에는 전이하지 않는다.

같은 run/attempt의 새 PostgreSQL capture 두 개를 내려받아 archive digest, 정확한 파일 집합, SHA256SUMS와 provenance를
검사했다. Producer job의 실제 success/attempt와 repository/run/checkout을 대조하고 source binding은 현재 dirty 파일이 아닌
해당 commit의 Git 객체에서 재계산했다. Systemstate 636 files / SHA
`0af369b39f90c62ba9cda5682e8fcf11b7be35d6b07812a971e8e7b55face094`, operator 713 files / SHA
`9e3105b552eb1ffaaa49d18c31bb94dbf57162dcd92a6d0c5ec36c39dad4b2fe`가 capture와 일치한다.

| capture | artifact / producer job | payload SHA256 |
| --- | --- | --- |
| systemstate-postgres-1 | 11011053746 / 109240390377 | `a6c86bb1fbd5de02db6d12b5cd9af35033efcf8f0a93e2a4c358178df1a2d723` |
| operator-postgres-1 | 11011637601 / 109240390006 | `0428148513319ffe77ff4ef1fe0a5c62d414d07c3a36b7864ccb6b01b227a993` |

원문·archive·inventory·최종 receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-formset-integration-full-36516565253-_fa5jbg9`에 있다.
실행 지연 때문에 교체하거나 로컬 전체를 중복하지 않았다. 이후 Inline source `ed1dc79f`의
[PR feedback 36519619103](https://github.com/progresshans/godj/actions/runs/36519619103)은 실제 Fast Go feedback step과
job `109249407379`이 success다. Documentation-only step은 비대상이며 이 결과를 full-platform 성공으로 표현하지 않는다.

### Inline 부모 결합과 새 부모 저장 후 자식 준비

기반 `6d8afda5086ba3fc058376a60dc567bdcd5a05d7` 이후 InlineSpec/InlineSet을 구현했다. Canonical project의 FK와 양 typed
manager를 확인하고 서버 부모·current 자식의 소속을 검증한다. Hidden parent는 Changed에 들어가지 않고 raw 위조/중복을
삭제 행·빈 extra에서도 전체 거부한다. Model clean은 부모 FK를 소유할 수 없다. Pending parent를 먼저 검증하고, 저장에서
받은 key로 별도 자식 후보를 준비한다. nullable/cross-app FK·명시적 PK 0·OneToOne과 같은 부모의 unique tuple을 유지한다.
독립 행 Prepare로 pending 부모 검사를 우회할 수 없다. Helpdesk의 기존 Category 편집기를 공통 InlineSet으로 이관했다.

독립 `conformance/runners/django/inline_formset_reference.py`를 고정 Django 6.1/CPython 3.14.3에서 실제 실행했다.
`django.forms.models` SHA는 `858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910`이며 22개 입력/표시와
4개 SQLite 저장 관찰을 fixture에 기록했다. Ordinary validity·행/전체 진단·clean 횟수·서버 부모 후보와 hidden/optional 의미를
대조한다. 부모 관련 7개 입력은 GoDj의 전체 `invalid_parent`를 검사하며 native가 허용한 삭제/빈 extra 위조·반복 부모값·Python
`None` 표기 네 경우는 강화 차이다. 나머지 세 거부도 native와 오류 위치가 동일하다고 합치지 않는다.

실제 양 DB의 public Form/InlineSpec/ORM 소비자는 부모 생성→자식 두 개 저장, 지연 준비의 child write 0, 늦은 두 번째 자식
고유성 실패의 부모/첫 자식 rollback, unsaved parent의 준비 거부를 네 native 저장 결과와 대조했다. Rollback 뒤 발급된 key가
Go 값에 남아도 저장됐다고 판단하지 않는다. Helpdesk HTTP는 다른 부모·삭제된 위조 행·빈 extra 위조·반복값의 쓰기/audit 0과
서버 부모를 사용한 안전한 오류 재표시를 확인했다. 신규 부모 HTML 화면이나 Admin inline 편집 UI는 이 실행에 포함하지 않는다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| 최초 affected normal의 성공 package | forms/admin/Helpdesk 3 packages / 141 roots / 128 required cases / 1,680 PASS |
| 모델 fixture 수정 후 normal | forms/model 1 package / 52 roots / 479 PASS / 0.518초 |
| pure race | 3 packages / 6 roots / 28 PASS / 2.213초; 부모 소유권·동시 key 완성·native/hidden 출력 |
| HTTP race | 양 DB mixed/4가지 부모 위조, 2 roots / 10 required cases / 14 PASS / 46.820초 |
| 저장 race | 양 DB의 inline parent/child subtree, 1 root / 10 required cases / 13 PASS / 3.585초 |
| 부정 대조 | 부모 전체 admission 제거→삭제 위조 HTTP 저장, pending guard 제거→nullable orphan 준비, parent tuple 상수 제거→새 부모 형제 중복 허용을 각각 검출 |
| CI 도구/형식 | 42 Python tests PASS / 6.638초; 변경 Go 16개 gofmt clean |

최초 4-package normal은 21.952초 / 2,158 PASS와 **1개 테스트 실패**로 전체 실패했다. 지원하지 않는 FK default를 IR에 넣은
`TestInlineUnsavedParentOverridesForeignKeyDefault` fixture가 원인이다. IR 기능을 임의로 넓히지 않고 canonical project와 다른
manager 정책을 거부하는 `TestInlineRejectsNoncanonicalForeignKeyPolicy`로 교정했다. 모델 package 전체를 다시 실행해 위 479 PASS를
얻었다. 처음 통과한 다른 세 package는 반복하지 않았다. 두 실행의 합은 193 roots / 2,159 PASS이지만 최종 소스의 한 번의 전체
실행으로 표현하지 않는다. 초기 실패 receipt와 raw event는 보존한다.

Go 1.26.5 Darwin arm64, PostgreSQL 17.10 UTF8/libc/C와 SQLite, 기본 공유 cache/병렬 실행·readonly/offline module·
Pacific/Chatham에서 실행했다. 성공한 각 범위의 compiled root/required child와 실제 run/pass를 대조했고 누락·skip·잘린 event는
0이다. 첫 normal inventory `32edc66b688d6b36798e6be66ec81c5d83bf36e800660289657b01498be7ec2c`에서 최종 inventory
`206eaaeb8502936c62cc1b102ed852350f0b79d3d691b3371f19ad4c3e0f0ecc`로의 유일한 비문서 차이는 `forms/model/inline_test.go`다.
제품/다른 테스트/fixture는 같고 수정 모델 normal과 모든 race는 최종 inventory에서 실행했다. 각 실행 전후 source 일치,
DB 잔여 `0|0|0`과 임시 DB/container 제거를 확인했다. 모델-only repair helper도 불필요하게 private DB를 시작했다가 제거했으며
순수 모델 재검사에는 필요하지 않다. IR/generator 변경이 없어 generated drift·CGO0·cold/full compile을 추가하지 않았다.

Inline checkpoint 당시 `6d8afda5`의 [Hosted full 36516565253](https://github.com/progresshans/godj/actions/runs/36516565253)은
61개 job 중 성공 51·실행 중 5·대기 5였다. 이후 위 별도 항목에서 62 jobs·최종 success/capture 결합을 완료했다.
이 run은 Inline 변경 이전 소스이며 이후 Inline의 full-platform 증거로 전이하지 않는다.

원문·source·compiled 목록·receipt/controls:

- 최초 normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-inline-normal-_evmy71h`
- 모델 repair/부정 대조: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-inline-model-normal-viwc5d7p`
- 관련 race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-inline-race-9zwgztzw`
- source와 성공 범위 재대조: `/tmp/godj-inline-source-reconciliation.json`
- Hosted run/job 관찰 원문: `/tmp/godj-inline-hosted-observation`

이후 문서 정리는 local Markdown 164개 링크와 diff 검사로 확인했다. 비문서 source는 위 최종 inventory와 같다.

### 여러 행 고유값 검증과 Hosted 테스트 선택 수정

기반 `568b75b40ac5da5b3d03b2406808d33c6c23f911` 이후 ModelFormSet의 unique field/복합 UniqueConstraint 검증을 연결했다.
행별 검증→개수 검사→모델 여러 행 검사→사용자 SetValidator 순서다. Valid 행의 cleaned 입력과 완전한 tuple을 비교하며,
뒤쪽 중복 행의 non-field/전체 `unique` 진단과 cleaned member 제거를 typed/core에 같이 반영한다. Candidate·raw·initial은
보존한다. 겹친 제약은 동일 시작 snapshot과 IR 순서로 검사하며 부분 tuple 비교로 인한 오진을 방지한다. 최종 DB/candidate
고유성 검사는 유지하고 Helpdesk의 UUID 표기만 다른 중복을 실제 쓰기/audit 전에 거부한다.

고정 Django 6.1/CPython 3.14.3의 `model_formset_unique_reference.py`를 실제 SQLite에서 실행했다. 기존 고정 source와 같은
`django.forms.models` SHA `858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910`이며 저장 무변경/정리를 확인했다.
23개 사례에서 valid·행/전체 오류·cleaned field 제거·clean 횟수를 대조했다. NULL·삭제와 다른 invalid 행의 조합·UUID 별칭·복합
FK/문자열·제외 field·개수 우선·model clean 후보와 cleaned 입력의 분리를 포함한다. Native의 코드 없는 중복 메시지는 안정된
`unique`로, editable Form 밖의 PK는 별도 identity로 비교한다. 겹친 제약의 결정적 snapshot 정책은 Go-native 검사로 구분한다.

| 실행 | 실제 범위와 결과 |
| --- | --- |
| normal | Form/model/Helpdesk 3 packages / 110 roots / 110 required cases / 1,898 PASS / 19.612초 |
| pure race | compound 거부·Set binding/순서·native unique/겹친 제약/동시 바인딩, 2 packages / 5 roots / 31 PASS / 1.721초 |
| product race | 양 DB `duplicate_tuple`/`late_unique`, 1 package / 2 roots / 4 required cases / 8 PASS / 46.040초 |
| 부정 대조 | 여러 행 검사를 제거하면 HTTP가 쓰기에 도달, cleaned member 제거를 생략하면 native 대조 실패, Set token 검사를 생략하면 다른 binding 허용을 검출 |
| CI 도구 | 42 Python tests PASS; 처음의 macOS Bash empty-array/nounset 실패는 `${required_tests[*]-}`로 수정 후 재검증 |

Go 1.26.5 Darwin arm64, 기본 공유 cache/병렬 실행, readonly/offline module·Pacific/Chatham과 PostgreSQL 17.10 UTF8/libc/C를
사용했다. 필수 누락·skip·잘린 event 0, 실제 compiled root 목록과 run/pass를 대조했다. DB 잔여 0/0/0과 임시 DB/container 삭제를
확인했다. Normal inventory는 `3d182b41d484f2ba428581d434e489b952c3bf8bd7abb70ab067b288c9640ed9`, race는
`ab976cbe3f720bcfb693256e98caac8eb50b07a64c4cecb5e154b5bf039427d8`다. 최종 비문서 inventory
`14c4a385594800d066997e5974cc25e8763476a2d77dde6d9b1078c45bd7bcd6`와의 차이는 CI workflow/Python 회귀뿐이며 제품/Go 테스트는 같다.
이후 CI 선택자 실제 실행과 42 Python 검사는 최종 파일에 수행했다. IR/generator 변경이나 local 전체 검증은 없다.

선행 소스의 [Hosted full 36514445961](https://github.com/progresshans/godj/actions/runs/36514445961), attempt 1은 PostgreSQL core
normal job `109233540180`, CGO=0 job `109233540143`의 필수 목록 검증에서 실패했다. 실제 Go 명령은 0으로 끝났지만 새 SQLite
Helpdesk 하위 사례 31개가 실행되지 않았다. 이 실패를 PASS/skip 면제로 바꾸지 않았다. 전체 경로를 괄호 안에 합친 기존 `-run`
선택자는 부모의 별도 sentinel이 없는 경우 부모를 선택하지 않았다. 실행 regex는 중복 없는 부모 이름에서 만들고, 결과 검증은
완전한 원래 하위 이름을 유지하도록 고쳤다. 현 소스의 SQLite editor 필수 32개로 원래 workflow 코드를 실행하면 **run 0**과
inventory 거부가 재현되며, 수정한 workflow 코드는 **106 run / 필수 32개 전부 PASS / skip 0**이다. 다른 실행 owner의 누락을
허용하거나 테스트를 삭제한 수정이 아니다. 이 run은 최종 `cancelled`이며 62 jobs 중 success 45·cancelled 14·failure 3이다.
위 두 core job과 최종 집계의 실패를 보존한다. 이 실패 run에는 전체 platform 성공이 없고 수정 소스의 성공은 위 별도 run에 기록했다.

원문·source/receipt·controls:

- normal/부정 대조: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-set-unique-normal-7jki55aq`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-set-unique-race-2d1y1_7s`
- 실행 선택자 대조: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-set-unique-selector-ud204vn5`
- Hosted 실패 logs/run/jobs: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-hosted-failure-9hdohriz`
- 파일별 source 재대조: `/tmp/godj-set-unique-source-reconciliation.json`

### Helpdesk 여러 행 HTTP 편집과 원자 저장

기반 `812d0136962ca46251deb22ad80e84077402ea80` 이후 `/tickets/edit/`의 GET/POST 편집기를 연결했다.
서버 Category의 페이지별 20개 current 모델과 선택지를 snapshot/최종 transaction에서 각각 읽는다. Subject·Closed·External
reference·Labels를 한 요청에서 편집·생성·삭제하고 제외 scalar를 보존한다. Typed 준비와 단일 Ticket writer의 session 내부
helper를 재사용한다. Project relation deleter의 borrowed PROTECT/CASCADE와 모든 감사 기록도 같은 transaction에 둔다.
권한/CSRF·cohort/identity·bounded parser·출력 escaping·오류 재표시·commit 이후 303을 제공하며 실행 실패/unknown outcome은
입력 오류나 성공으로 바꾸지 않는다. 임의 SQL/새로운 optimistic revision/전체 ModelFormSet 자동 저장을 이 증거로 주장하지 않는다.

실제 SQLite/PostgreSQL에서 HTML을 DOM으로 읽고 successful controls를 제출했다. Mixed 수정/삭제/생성·선택 관계·제외값과
감사 기록의 동시 commit·다른 Runtime 재개를 확인했다. Invalid 뒤쪽 행·중복/외부/추가 PK·외부 라벨·삭제 보호·늦은 고유성
충돌·두 번째 audit 실패·관계 쓰기 실패·취소에서 행/관계/audit의 원자성을 검사했다. Commit-unknown fixture는 실제 commit 뒤
오류를 반환하여 결과가 저장됐어도 성공 redirect/자동 재시도가 없음을 확인했다. Rollback-unknown의 추가 오류는 렌더 가능한
입력 거부로 축소하지 않았다. 현재 scope 이동·forged/duplicate management·권한 overlay·CSRF/파서·pagination도 포함한다.

제품 코드의 비문서 inventory `c39ee9c8ac6f6b17cab2a5168d7563f3c644714bb1057a712ef2a54c611e82e0`, Darwin arm64
Go 1.26.5·offline modules·기본 공유 cache/병렬 실행·TZ Pacific/Chatham에서 확인했다.

| 범위 | 결과 |
| --- | --- |
| Helpdesk 전체 normal | 1 package / 9 roots / 필수 108 cases / 261 PASS / 23.221초 |
| 최초 editor의 모든 관련 race | 초기 inventory `0b955c4f…`, 2 roots / 필수 60 cases / 66 PASS / 62.176초 |
| startup/응답 한도 보강 후 관련 race | inventory `c39ee9c8…`, 구성 거부·양 DB mixed HTTP/원자 저장·pagination·최대 출력 3 roots / 11 PASS / 50.272초 |
| 부정 대조 | Add 권한 overlay 제거·audit 오류 무시·unknown 오류를 입력 재표시로 축소한 3 overlay가 지정 runtime assertion에서 실패 |
| 공통 검사 | CI 도구 41 tests, gofmt·문서 링크 164개·diff 검사 성공 |

각 DB 실행은 PostgreSQL 17.10 UTF8/libc/C와 실제 SQLite를 사용하고 compiled 목록·필수 run/pass·complete JSON event를 대조했다.
누락/skip/잘린 event 0, source 전후 동일, 남은 schema/table/다른 연결 `0|0|0`과 DB/container 제거를 확인했다.
최초 normal/race는 성공했다. 검토에서 startup cookie/복귀 경로 검사와 큰 선택지 페이지의 template/Web 응답 한도 정합성을
보강했다. 256개 최대 길이 label의 escaping 확대와 40행 오류 재표시를 실제 HTTP로 확인해 기본 Web 1 MiB 때문에 정상
페이지가 거부되지 않게 했다. Template과 Web 출력 예산은 명시적 8 MiB이며 선택지/요청/반복 한도는 유지한다.

마지막에는 출력 한도 테스트가 pagination subtest 없이도 독립 실행되도록 그 테스트의 데이터 준비만 보완했다.
최종 inventory `f4f658ab8554ee4f5c93c4691b14ddab8a84242cbc40ae55a9cdb35ae28b05dc`에서 해당 양 DB 사례를
별도로 실행해 2 roots / 6 PASS / 7.893초를 확인했다. 두 inventory의 유일한 차이는 이 test fixture이며 제품 코드는 동일하다.
기존 2 MiB template 한도를 복원한 추가 overlay는 40행 invalid POST 재표시가 500이 되는 runtime assertion에서 실패했다.
첫 audit는 최초 GET의 실패 문구를 기대해 false였으나 실제 실패는 더 큰 POST 재표시였다. 원래 raw/실패 receipt를 보존하고,
동일 정상 사례의 PASS·정확한 overlay 차이·전체 event와 실제 실패 문구를 재대조했다. Go build를 반복하지 않았다.
이전 race의 모든 사례를 최종 source에서 재실행했다고 합치지 않는다. IR/generator 변경이 없어 generated drift·별도 module
전체·CGO=0·로컬 전체 compile을 추가하지 않았다. Formset core/typed/실제 제품을 묶은 Hosted full milestone이 나머지 환경을 소유한다.

원문·실제 목록·receipt/source와 controls:

- 제품 normal/controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-normal-szfxlbm0`
- 제품 관련 race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-race-lninn10f`
- 독립 출력 한도 사례: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-edge-0bhzkjtd`
- 최초 normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-normal-j8bi8497`
- 최초 전체 관련 race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-race-c3qqe3kq`
- startup 보강 후 normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-normal-_eshc3_y`
- startup 보강 후 관련 race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-ticket-editor-race-thgczlmt`

### 서버 identity와 모델 후보의 typed 여러 행 준비

기반 `7d56b3c9ff284eb68ef743e5646c6dc0ab18da3c` 이후 `InstanceSet`과 typed 준비를 연결했다. 모델 PK로 서버 current 집합의
행을 찾아 순서 변경을 허용하며 누락/중복/외부/추가 행 PK는 전체 요청을 거부한다. DELETE나 빈 추가 행이 이 경계를
우회하지 못한다. 일반 Form binding 뒤의 pure processor와 단일/여러 행 공통 model candidate 경로를 사용한다.
다른 binding/행으로 교체할 수 없고, raw input을 재바인딩하거나 field/model clean을 반복하지 않는다. 모델 오류 뒤에
count/set 검증을 수행한다. 늦은 DB 데이터 진단과 요청 전체 admission을 구분한다.

고정 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122` / CPython 3.14.3에서 독립
`model_formset_reference.py`가 실제 SQLite/queryset과 `save(commit=False)`를 관찰했다. 20개 중 정상/모델 오류/삭제/정렬
11개는 후보·field 오류·validity·변경·model clean 호출 순서·deferred 값/삭제 ID/정렬을 대조한다. Identity 9개는 GoDj가
전체 거부하며, 그중 native가 허용하는 6개는 의도적 강화 차이다. 나머지 3개도 오류 위치/후보가 같다고 주장하지 않는다.
원문 SHA256 `40d061e30e1dd253cdceede3b9abe7fd18f6a6bd5c4256392b12f4aa1246a159`, native models.py SHA256
`858772e26a8e1d023782b410d3de67b9fc73dfb98120683fdf14584c0d5b0910`이다. Native DB의 무변경을 확인했다.
Go binding은 이미 읽은 모델/관계 snapshot을 받으며 query 수를 native와 동등하다고 세지 않는다. Go Prepare는 변경하지 않은
기존 행도 보존하므로 deferred 비교는 변경/신규 행을 선택한 값으로 수행했다. 삭제 비교는 원래 서버 snapshot의 ID다.

최종 비문서 inventory `9e272267cc017f99d7a2a9e6b5db0e25e7dae6e35303c450b12d4d7245ad01d8`, Darwin arm64
Go 1.26.5·offline modules·기본 공유 cache/병렬 실행·TZ Pacific/Chatham에서 확인했다.

| 범위 | 결과 |
| --- | --- |
| Normal Form/model·Admin·Helpdesk | 4 packages / 181 roots / 2,013 PASS / 22.336초 |
| 관련 Formset/InstanceSet·Helpdesk typed 저장 race | 3 packages / 11 roots / 96 PASS / 5.053초 |
| 기존 실제 SQLite/PostgreSQL consumer | 필수 backend/case 실행, PostgreSQL 17.10 UTF8/libc/C, 잔여 schema/table/다른 연결 `0|0|0`, DB/container 제거 |
| 부정 대조 | binding 소유권 제거·identity 전체 거부 제거·model clean 반복의 3 overlay가 지정 runtime assertion에서 실패 |

Article의 nullable/제외 값·PK 재정렬·input 비노출·후처리와 Ticket의 선택 ManyToMany·서버 category·24개 동시 typed 준비를
검사했다. 일반 field/model clean의 정확한 실행 횟수·raw 공백과 cleaned 값 분리·DELETE 데이터 오류와 admission·복사본
소유권/formatting도 확인했다. 실제 compiled test 목록과 JSON run/pass를 대조했고 누락/skip/잘린 event 0이다.
첫 normal과 관련 race가 성공했으며 source 전후 동일이다. 이 증거는 아직 새로운 여러 행 HTTP/DB 저장 consumer가 아니다.
IR/generator 변경이 없어 generated drift·독립 module 전체·CGO=0·전체 compile을 반복하지 않았다. 후속 제품 연결 milestone이
추가 범위를 소유한다. 선행 initial source의 Hosted full을 새로 시작하거나 현재 source로 전이하지 않았다.

원문·실제 목록·receipt/source·부정 대조:

- Normal/controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-formset-normal-fmk4fqfq`
- Race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-formset-race-isk8_6ve`

### Pure Formset core checkpoint

기반 `cb76b165aa3379c8c40aabfa7d12354460c57bf7` 이후 database-independent `SetSpec/Set`을 구현했다.
Prefix·management·서버 initial 개수·생성 상한, 빈 추가 행·정렬/삭제·행별/전체 진단·불변 선택과 pure 여러 행 validator를
제공한다. Required choice의 빈 값이 초기 빈 값과 같을 때 Changed를 false로 처리해 optional 추가 행이 불필요하게
검증되지 않게 했다. 이 core checkpoint 시점에는 실제 model identity·typed 여러 행 준비를 포함하지 않았다.
후속 typed 준비와 Helpdesk 여러 행 HTTP/저장은 위 항목에 별도로 기록한다. Inline/file upload는 아직 미구현이다.
이 core checkpoint를 ModelFormSet 또는 제품 수직 단면의 완료로 표현하지 않는다.

고정 Django 6.1 / CPython 3.14.3에서 독립 `formset_reference.py`를 실행했다. 총 33개 사례 중 30개에서
management·row 수/field 순서·bound/valid·empty_permitted·Changed·cleaned/errors·삭제/정렬 index·row clean 실행 수를
대조했다. 음수 TOTAL, INITIAL>TOTAL, 서버 initial과 다른 INITIAL의 세 사례는 native가 허용하지만 GoDj는 명시적으로
거부한다. Int64/단일 값 중복 입력·기존 Boolean/Choice parser 경계는 전체 Python coercion 동등성으로 세지 않는다.
원문 SHA256: `428e21e1652b88eb1549d320cace0e4a6c02564f0267618a4a5ca5a0e35f4472`.

최종 비문서 inventory `b66cf31a17ae0015ff420e8536a509206be5b88046d02a1e7b4a85ba8bb738ee`,
Darwin arm64 Go 1.26.5·offline modules·기본 공유 cache/병렬 실행·TZ Pacific/Chatham에서 다음을 확인했다.

| 범위 | 결과 |
| --- | --- |
| `go test -json -count=1 ./forms/...` | 2 packages / 89 roots / 1,573 PASS / 0.749초 |
| 관련 Formset race | 3 roots / 42 PASS / 1.511초, 같은 불변 spec의 24개 동시 binding 포함 |
| Admin choice의 실제 HTTP/검증 경계 | 9 roots / 21 PASS / 0.571초 |
| 부정 대조 | 서버 initial count 일치 제거·생성 상한 +1·빈 추가 행 검증 skip 제거의 3 overlay가 지정 runtime assertion에서 실패 |

각 실행은 실제 Go test 목록과 complete JSON event의 필수 실행/종료를 대조했다. Missing/skip/잘린 event 0,
source 전후 동일을 확인했다. Count overflow·중복·잘못된 prefix/config·reserved ORDER/DELETE 충돌·typed nil·
입력/initial/result 소유권·formatting 비공개·선택 시 callback 재실행 없음도 포함한다.
처음 32-case checkpoint는 성공했다. 검토 중 management 오류와 cross-form 오류가 함께 생기는 우선순위를 추가 관찰해
non-form 진단 소유권을 수정하고, 33-case 최종 source에서 영향 검사를 다시 수행했다. 이전 성공을 최종 source로 옮기지 않았다.

최종 원문/목록/receipt/source/세 controls·Admin 확인:
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-formset-core-9ym8729b`.
앞선 32-case 원문은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-formset-core-k1lb4u4d`에 보존한다.
이번 core에는 I/O·Schema IR·generator·migration 변경이 없어 DB/container·전체 compile·CGO=0·Hosted 전체를 새로 반복하지
않았다. 후속 모델/제품 묶음에서 필요한 소비자와 환경 범위를 정한다. 선행 `cb76b165`의 CI는 이 core를 포함하지 않는다.

게시된 core source `7d56b3c9ff284eb68ef743e5646c6dc0ab18da3c`의
[Fast 36507584740](https://github.com/progresshans/godj/actions/runs/36507584740)은 terminal success와 실제 Go step 성공을
확인했다. 이 source에는 위의 후속 typed 여러 행 준비가 없다.
후속 typed 준비 source `812d0136962ca46251deb22ad80e84077402ea80`의
[Fast 36510853114](https://github.com/progresshans/godj/actions/runs/36510853114)도 실제 Go step·terminal success를 확인했다.
이 source에는 후속 Helpdesk 편집기가 없다.

## GDJ-0102 — 모델 Blank와 Form 후처리 구현 중

기반 `d95b969b3297bcad13fd6760706e733684cb8add` 이후 Blank metadata·생성 metadata·historical wire/digest,
scalar ChangeBlank와 columnless AlterManyToMany를 구현했다. Form required를 Blank에서 투영하고 입력 cleaned data와
모델 candidate를 분리했다. 원래 제출의 생략/반복값, 기본값/기존 값, checkbox/multiple-select 생략을 보존하며
모델 field·pure model validator와 단계별 exclusion, scalar unique/constraint의 별도 read-only ORM API를 작성했다.
User/Helpdesk/Article의 명시적 Blank 선언·새 migration·생성물과 Admin/Identity의 pure 모델 후처리는 연결했다.
현재 권한을 갖는 같은 DB read scope의 단계별 unique/constraint를 User 수정·Ticket·TicketLabel에 연결하고 native DB와
대조했다. 후속 checkpoint에서 Group/Permission·Label/ServiceReport의 읽기 검증도 연결했다.
후속 model clean의 명시적 변환과 저장 입력·Admin 연결 및 영향 checkpoint를 완료했다.
명시적 scalar/collection 저장 조정의 후속 구현·영향 검증도 완료했다. 전체 ModelForm 자동화·제품 adapter 확대와 후속 source의 Hosted 전체는 **아직 미완료**다.
기반 검증을 전체 제품 또는 전체 ModelForm 완료로 기록하지 않는다. 이 작업본은 게시된 EmailField CI에 포함되지 않는다.

### 저장/캐시/초기값 source의 Hosted 통합 완료

`cb76b165aa3379c8c40aabfa7d12354460c57bf7`를 양 branch에 게시했고
[Fast 36504615628](https://github.com/progresshans/godj/actions/runs/36504615628)의 terminal success와 실제 Fast Go step 성공을 확인했다.
같은 source의 [full 36504649962](https://github.com/progresshans/godj/actions/runs/36504649962), attempt 1은 terminal success다.
62개 실제 job이 모두 성공했고 최종 aggregate job `109223107908`의 `full_platform_verified=true`와 필수 8개 owner를 source의
검증 규칙에 대조했다. 새 capture의 checksum/provenance·producer attempt와 Git 객체 기반 source 결합도 함께 확인했다.

| capture | artifact / producer job | Git source binding |
| --- | --- | --- |
| systemstate | `11007292394` / `109203252031` | 633 files / 6,694,950 bytes / `132d29a16f87adfd4067d4bf2d339275a95037251b18e8a5ba6510129bb8f6a9` |
| operator | `11006837748` / `109203252101` | 710 files / 6,544,386 bytes / `2067975d74c57aebb0bf8a69fc70029e05ea80e140b33d38cd89340b7429756c` |

Archive/payload SHA·run/jobs·producer attempt·Git 객체 inventory와 최종 성공 receipt:
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-initial-full-36504649962-r71rryo8`.
이 source에는 후속 Formset 구현이 없으며 이후 소스의 전체 검증으로 전이하지 않는다.

### 기존 initial과 제출 입력 제약의 분리

기반 `eca6ec108a43864fb07a0c75f7ce28ca665511d6` 이후 Form의 initial을 표시·변경 비교 값으로 처리한다.
새 입력의 길이·Decimal 정밀도가 기존 저장값보다 좁아도 폼을 열고 수정할 수 있다. 제공한 initial의 타입·유효한 표현과
UTF-8/NUL, 선언 default의 제약은 유지한다. Admin은 Spec.Unbound 뒤의 중복 초기값 제약 검사를 제거하고,
인가된 모델 snapshot과 initial의 동일성을 계속 확인한다. Decimal 표시가 입력 scale에 맞지 않으면 수치를 유지한
문자열을 사용한다. 제출·typed 준비·최종 저장의 검증과 현재 권한/revision 경계는 바꾸지 않는다.

고정 Django 6.1 / CPython 3.14.3에서 독립 observer `form_initial_reference.py`를 실행하고 저장된 원문과 byte 일치를
확인했다. Pure Form 12개와 ModelForm 2개를 initial·유효성·오류 코드·cleaned/candidate·Changed·commit=False 결과로
대조했으며 DB query는 0이다. 원문 SHA256은 `c359e9cd1467599030279735aaf2687ab7c57e368cb502eadc582572cd097d27`이다.
Unbound 전체 HTML 및 Python 객체 identity의 동일성을 주장하지 않는다. Admin 실제 HTTP는 기존 값 escaping·잘못된
동일 제출의 무변경·유효한 수정 저장을 검증하고, Helpdesk는 실제 SQLite/PostgreSQL에서 scalar/선택 관계를 함께 확인한다.

최종 비문서 source inventory `6aa1e47a4444ac1c211a0e47355e5ec189629811192fe18f30ba56c6a1f7bccc`,
Darwin arm64 Go 1.26.5·기본 공유 cache·기본 병렬 실행·offline modules·TZ Pacific/Chatham에서 다음을 확인했다.

| 범위 | 최종 성공 증거 |
| --- | --- |
| Normal Form/model·Admin·Helpdesk | 4 packages / 171 roots / 1,940 PASS |
| 관련 race: initial/typed 준비·Admin snapshot/표시·Helpdesk 저장 | 4 packages / 10 roots / 54 PASS |
| Native PostgreSQL | 17.10 UTF8/libc/C, 필수 양 backend/case 실행·잔여 schema/table/다른 연결 0|0|0·DB/container 제거 |
| 회귀 대조 | 이전 Form 초기값 제약, Admin 중복 제약, Decimal 빈 표시를 각각 복원한 3 overlay가 지정 runtime assertion에서 실패 |
| 검증 도구 | CI 도구 41 tests PASS; 실제 Go test의 기본 vet 포함 |

첫 normal에서 Form/model·Helpdesk는 성공했지만 Admin의 중복 max_length 검사로 GET 500이 발생했다. 이를 고친 두 번째
normal과 첫 race는 GET 200을 반환했으나, 새 테스트가 현행 template에 없는 maxlength HTML 속성을 요구해 실패했다.
전체 HTML 동등성은 이 변경의 계약이 아니므로 이 잘못된 기대를 제거하고, 기존 값 표시와 서버의 입력 제약·무변경·저장
결과를 검사했다. Final Admin 전체 normal 77 roots/215 PASS(0.646초), 관련 race 5 roots/13 PASS(1.625초)를 확인했다.
실패한 aggregate receipt는 그대로 보존하며 성공으로 바꾸지 않는다.

성공한 Form/model normal·Helpdesk normal/race·관련 pure race는 재실행하지 않았다. 각 원문에서 실제 package 종료,
필수 run/pass·누락/skip/잘린 event 없음과 source 전후 동일을 확인하고, 최종 source와의 변경 파일이 해당 package의
실제 `go list -deps -test` 입력 집합 밖에 있음을 독립 검증했다. 이 성공 결과와 최종 Admin 검사를 합친 수치가 위 표다.
소스가 다른 결과를 무조건 전이한 것이 아니며 monolithic 전체 실행으로 표현하지 않는다. Schema IR·generator·ABI는
바뀌지 않아 generated drift/별도 module 전체를 반복하지 않았고, CGO=0·다른 platform은 후속 Hosted 통합이 소유한다.

원문/receipt/source inventory:

- 최초 normal·Admin GET 500: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-initial-normal-r_hz0knv`
- Admin 수정 후 normal·native 원문: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-initial-normal-70j7hv_x`
- 관련 race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-initial-race-lrd_qad6`
- 최종 Admin·세 부정 대조·package별 입력 집합/결합 receipt: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-initial-admin-final-k9svwtfy`

### 생성 소비자의 임시 경로와 build cache 재사용

기반 `c5f5e56a8603ff22bc4b8324f1e261bf147935bf`의 기본 collection saver는 양 branch에 게시했고
[Fast 36402154359](https://github.com/progresshans/godj/actions/runs/36402154359)의 실제 Fast Go step·terminal success를 확인했다.
이후 `codegen/consumertest` 자식 Go 명령에 `-trimpath`를 적용했다. 격리된 writable module·같은 shared GOCACHE와
원래 CGO/OS/arch·실제 race·`-count=1`은 유지한다. 부모와 CLI/process의 컴파일 설정은 바꾸지 않는다.

새 실제 Go 검사는 다른 임시 디렉터리의 동일 source/embedded input에서 같은 BuildID와 Export cache 파일을 확인한다.
한 module의 source를 바꾸면 다른 identity를 사용하고, 원래 module의 identity/값은 보존한다. 같은 명령 반복을 포함한 다섯 번의
test body 실행을 module 밖 marker로 확인하여 build cache 재사용과 test-result 재사용을 구분한다. 기본 cache를 지우거나
private GOCACHE를 새로 만들지 않는다. 첫 `-trimpath` profile 준비와 source 변경에는 정상적인 새 build/cache가 필요할 수 있다.
SSD 전체 write 양·일반 workload의 감소율은 실측하지 않았으며 이 검사를 성능 benchmark로 표현하지 않는다.

비문서 inventory `2c2140904f55a582d72e69eb91ac5d3aad8895d918ef2c9f49a8205f1692e3c5`, Darwin arm64 Go 1.26.5·offline modules·공유 기본 cache·TZ Pacific/Chatham,
전용 PostgreSQL 17.10 UTF8/libc/C에서 다음 영향 검증을 완료했다.

| 범위 | 결과 |
| --- | --- |
| Normal: 영향을 받는 생성 소비자 package 전체 | 실제 71 roots / 필수 26 cases / 134 PASS / 228.636초 |
| Race: helper·cache 재사용·취소·실제 자식 race·ManyToMany/ModelFormSave·오용 compile 경계 | 7 roots / 12 PASS / 62.331초 |
| CGO=0: 같은 관련 범위 | 7 roots / 12 PASS / 10.865초 |

선택한 두 DB 소비자는 실제 SQLite/PostgreSQL의 collection child 336개와 native save 필수 child 45개를 포함하며,
각 부모가 complete event·필수 실행을 검증한다. 필수 누락·test skip·잘린 event 0, source 전후 동일과 각 DB의 잔여
schema/table/다른 연결 **0|0|0**, DB/container 제거를 확인했다. 영향 vet·gofmt·161개 문서 링크·diff 검사를 통과했다.
정규화 제거와 `-count=1` 제거의 두 overlay 부정 대조는 각각 지정된 실제 runtime assertion에서 실패했다.

최초 normal의 실제 Go 실행은 exit 0이었다. 임시 집계 script가 raw fixture 문자열 속 child Test 선언 39개를 부모 root로
잘못 세어 missing으로 보고했다. `go test -json -list '^Test'`가 제공하는 실제 71개 부모 목록과 기존 전체 JSON의 run/pass
각 1회를 대조하고 source 동일성을 확인했다. 최초 집계 receipt와 정정 근거를 모두 보존했으며 runtime 검사를 재실행하지 않았다.
후속 mode의 목록 수집도 실제 Go 목록을 사용한다. 저장소의 필수 child 검사나 CI no-skip 조건을 줄이지 않았다.

원문/receipt/source inventory:

- Normal 전체·실제 test 목록/집계 정정·두 controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-generated-cache-normal-16evu_k7`
- Race 관련 범위: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-generated-cache-race-x5uzawwo`
- CGO=0 관련 범위: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-generated-cache-cgo0-axiyl_4c`

현재 변경에는 새로운 제품 runtime·generator·Schema IR·migration 변경이 없다. 로컬 전체 platform/cold build는 반복하지 않았다.
Helper commit `eca6ec108a43864fb07a0c75f7ce28ca665511d6`의 [Fast 36404332456](https://github.com/progresshans/godj/actions/runs/36404332456)는 실제 Fast Go step·terminal success를 확인했다. Hosted 전체는 후속 통합에 남으며, 선행 full `36397837881`의 성공을 이 변경으로 전이하지 않는다.

### Typed 관계에서 유도한 기본 Form collection saver

`SaveManyToMany`가 기존 forward typed binding에서 field 이름과 저장 callback을 만든다. `ManyToMany.Declaration`은
분리된 모델/field IR을 반환하고 reverse/zero binding을 거부한다. Form 저장 시작 시 전체 모델 정책·field 이름을 확인해
잘못 연결한 모델/관계가 scalar 쓰기에 도달하지 않게 한다. Options를 복사하고 typed nil through 입력을 거부하며,
실제 쓰기는 기존 From/InSession과 SetKeys로 위임한다. 외부 transaction·현재 인가·선택 검증·불투명 custom through
callback의 동시 실행 안전성을 대신하지 않는다. Helpdesk의 실제 관계 저장과 native 15-case 생성 소비자를 연결했다.

교차 앱 생성 소비자에는 자동 through의 retained ID와 Clear/options 소유권, nullable endpoint를 가진 explicit through의
required unique payload·실패/owner 변경 거부, 대칭/방향 자기 참조, 빌린 transaction의 rollback·중첩 0·만료·취소를 추가했다.
기존 15개 native 저장 관찰의 대조와 원래 COMMIT outcome-unknown 차이는 유지한다. 새로운 Python API 동등성을 주장하지 않는다.

비문서 source inventory `52ce41f39a087b49f093c034e61bf7ea9a41da9f77ca16bcd793a363d920498d`에서 Darwin arm64 Go 1.26.5·offline modules·TZ Pacific/Chatham,
전용 PostgreSQL 17.10 UTF8/libc/C와 실제 SQLite를 사용했다.

| 검사 | normal | race | CGO=0 |
| --- | --- | --- | --- |
| Form/ORM/Helpdesk 4 packages / 324 roots / 필수 73 cases | 2,380 PASS / 22.608초 | 2,380 PASS / 325.73초 | 2,380 PASS / 50.391초 |
| 두 독립 generated parents / collection 336·native save 필수 45 child events | PASS / 19.539초 | PASS / 53.412초 | PASS / 20.127초 |

필수 누락·test skip·잘린 JSON event는 없다. Race core는 중단 전에 전체 package/필수 case가 이미 성공했으므로 반복하지 않고
미완료 generated 그룹만 다시 실행했다. 그 원문과 정상 종료한 generated source 전후 inventory·현재 전체 비문서 inventory가
동일함을 별도 combined receipt로 확인했다. 정상 종료 checkpoint의 DB 잔여 schema/table/다른 연결은 **0|0|0**이고
DB/container를 제거했다. 중단됐던 race도 소유 container 제거를 확인했지만 그 checkpoint의 DB 잔여 카운트는 미측정이다.

모델 연결 검사 생략, typed nil through 허용, 모델/through metadata 복사 제거의 네 overlay 부정 대조는 모두 지정 runtime
assertion에서 실패했다. Compile 실패나 skip으로 대체하지 않았으며 source 불변을 확인했다. 영향 vet는 3.39초에
통과했다. Generated ABI·정규화 model·migration bytes는 바뀌지 않으며 독립 모듈은 실제 현재 생성기로 만들었다.

실패/환경 복구와 검증 범위:

- 최초 normal의 새 generated fixture 두 조회가 실제 API(Exact/All)와 달라 compile 실패했다. 올바른 API로 고치고 위 source를 검증했다.
- 영향 검증과 함께 추가 실행한 전체 compile에서 실제 `no space left on device`를 확인했다. 이 부가 전체 compile은 미완료이며
  현재 source의 PASS로 집계하지 않는다. 영향 범위를 넘어선 전체 compile 재실행은 이번 checkpoint에서 제외했다.
- 사용자가 빌드를 종료하고 공용 Go cache를 지웠음을 확인했다. 삭제와 겹친 재시도는 표준 패키지의 cache 파일 부재로 실패했다.
  임시 전용 cache를 쓴 시도도 중단됐으며 소유 container·cache를 정리하고 기본 GOCACHE로 복귀했다. 테스트/보호 조건은 완화하지 않았다.
- 동시 실행 제한을 지속하지 않는다는 사용자 지시를 반영했다. 성공한 범위의 관성적인 재실행과 불필요한 전체 compile을 피한다.
  `-count=1`은 test result cache만 끄며 build cache는 공유한다. 실제 Go 1.26.5 compiler action key와 generated helper를 확인한 결과,
  새 t.TempDir 경로가 generated package의 cache key에 포함된다. 경로로 인한 중복 build/cache 개선은 별도 후속 작업이며 아직 구현하지 않았다.
  이 분석은 SSD 전체 write 양을 실측한 결과가 아니다.

원문/receipt/source inventory:

- Normal·4 controls·vet·최종 combined receipt: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-collection-saver-normal-m8a2uqz9`
- Race core(성공 후 suite 중단): `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-collection-saver-race-pql3rj1e`
- Race generated(별도 정상 종료): `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-collection-saver-race-jz1dl7kk`
- CGO=0 최종 정상 종료: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-collection-saver-cgo0-yr48xok2`
- 최초 normal, 공간 부족/공용 cache 삭제와 겹친 시도 및 임시 cache 정리는 `/tmp/godj-collection-saver-*-path`가 가리키는 별도 원문에 보존한다.

이 기본 saver 후속 source의 Hosted 전체는 아직 남아 있다. 완료한 full `36397837881`은 선행 `f5b0020f`를 검사했다.
해당 run의 두 PostgreSQL capture는 archive checksum·producer attempt/provenance와 Git blob source 결합까지 확인했지만
최종 aggregate는 아직 미완료다. Capture 원문은 `/tmp/godj-product-save-full-closeout-path`가 가리킨다.

### BoundForm 전달과 Helpdesk typed 저장 연결

기반 `ce52685c6060b9999a95f7218b59d49d849a31d5` 이후 Admin의 Create/Update callback을 BoundForm으로 전환했다.
Registry의 최종 원본 제출 재바인딩·현재 선택/row/revision 확인과 Input의 nonnullable 표현 거부를 유지한다. 기존 consumer는
bound.Input을 읽고, Helpdesk Ticket은 새 `PrepareInstance`로 모델 metadata/PK 일치·기존 제외 값·후처리 변경을 typed 모델에
직접 연결한다. 과거 callback 형태를 위한 호환 분기는 없다. Command의 Values 계약과 credential/session 저장 의미는 유지한다.

Helpdesk Form의 수동 필드 복사·전체 patch 조립을 제거했다. 같은 기존 relation transaction에서 admitted principal·현재 row의
category·선택 label 소속·complete write uniqueness를 확인한다. 신규는 전체 Save, 수정은 변경 field mask를 가진 ORM Save
후 SaveCollections를 호출한다. 변경하지 않은 scalar나 제외된 category를 UPDATE하지 않으며 category 변경 clean을 거부한다.
JSON Form 동등성의 원문/SQL NULL 보존과 JSON API의 exact-token 계약, changed field/audit·저장된 응답의 사전 변환도 유지한다.
실제 systemstate Runtime은 기존 coordinated relation domain을 사용한다. Template/CSRF·권한·현재 선택 재검사·관계 실패/
rollback·outcome-unknown·복구 후 읽기 등 기존 HTTP/DB 회귀는 삭제하거나 완화하지 않았다.

새 단위 검사는 이미 바인딩한 candidate·원래 제출/cleaned 값·현재 제외 값의 분리, nullable 복사, 모델 정책과 이름·PK
값/존재·적용된 오류·zero BoundForm 거부와 clean 재실행 0을 확인한다. 새 실제 Helpdesk 소비자는 양 DB에서 신규/수정의
제외 clean 값, 관계만 변경, collection 제외/clear, 신규/수정 category 변경 거부, 바인딩 이후 category 이동의 8개 사례를
검증한다. 실제 native UpdatePlan도 추적해 category가 갱신되지 않고 collection-only에서는 scalar UPDATE가 0임을 확인했다.

Go/test/fixture를 포함한 비문서 source inventory `e9bc1141586582c453ddcbd4490759306b2b96165ecc71860c1a7e4cf7f284f0`에서
Darwin arm64 Go 1.26.5·offline/read-only module·TZ Pacific/Chatham·private PostgreSQL 17.10 UTF8/libc/C checkpoint를 실행했다.

| 검사 | normal | race | CGO=0 |
| --- | --- | --- | --- |
| Form/Admin/Identity/Article Admin/Helpdesk/systemstate 10 packages / 필수 266 roots·566 cases | 2,705 PASS / 26.556초 | 2,705 PASS / 157.667초 | 2,705 PASS / 25.544초 |
| 실제 SQLite/PostgreSQL Identity 관리 4 roots·370 cases | 370 PASS / 44.884초 | 370 PASS / 147.366초 | 370 PASS / 45.334초 |
| Generated model DB/clean/save 소비자 3 parents / 필수 child 113개 | PASS / 7.832초 | PASS / 19.016초 | PASS / 7.782초 |

세 mode 모두 필수 누락·skip·잘린 event 0, 실제 generated child race 상속, source 전후 동일을 확인했다. 각 PostgreSQL의
잔여 schema/table/다른 연결은 **0|0|0**이며 DB/container를 제거했다. 전체 **203 packages compile-only / 실제 test run 0**도
39.061초에 통과했다. Model/generator/migration bytes는 변경하지 않았고 로컬 full-platform을 반복하지 않았다.

앞선 normal inventory `de117a1e72995e98aaf45e242db3eea88535da789605064b23f71e9b27bbc1f6`도 core 2,705 / Identity
370 / generated 3 parents가 통과했다. 그러나 코드 검토에서 기존 객체의 전체 Save가 제외 category column을 다시 쓸 수 있음을
확인해 field mask와 native write 추적 검사를 추가했다. 앞선 성공으로 이 위험을 덮지 않고 위 수정 source를 세 mode로 검증했다.
편집 중 compile 확인에서 발견한 테스트 forwarding callback 7개의 이전 Values 인자도 BoundForm으로 전환했다. 이 compile 실패를
runtime 실패나 PASS로 세지 않는다.

네 negative control은 모델 정책 검사·현재 PK 검사 생략, 제외 clean 변경 누락, UPDATE의 category column 추가를 Go overlay로
주입했다. **4/4이 지정 runtime assertion에서 실패**했으며 마지막 두 개는 실제 SQLite 제품 저장을 실행한다. Compile 실패/skip은
대조 성공으로 세지 않았고 baseline/source 전후 동일을 확인했다.

검증 후 CI의 실행 owner 등록을 정리했다. Helpdesk는 portable integration/actual PostgreSQL owner이므로 relation-required에
추가했던 10개 항목을 제거하고 PostgreSQL 필수 parent/backend/case 19개는 유지했다. 최종 비문서 inventory는
`48b0e483ee30a7416c958abdff187b7cfcdc07c9dcf3faeae69b96883b378f74`이다. 두 inventory의 차이가 이 파일 하나뿐이고
**모든 Go/test/generated fixture bytes와 세 mode의 실제 로컬 필수 실행 집합이 동일**함을 독립 비교했다. 변경한 owner 분류의
CI 도구 **41 tests**(4.262초)와 영향 vet(0.618초), 문서 검사(161 documents)를 최종 source에서 통과했다. 이를 최종 source의 새 full-platform 실행으로 기록하지 않는다.

원문/receipt/source inventory:

- Before-mask normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-product-form-normal-bfrt0pnb`
- Final Go normal/negative controls/compile/owner correction: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-product-form-normal-wfjh6fmx`
- Race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-product-form-race-f8z3gp8k`
- CGO=0: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-product-form-cgo0-mozfvijt`

제품 연결 commit `996ff5eccf0393781080d834ddd9636e981e53bd`를 양 branch에 원자적으로 push했다.
[Fast 36397043744](https://github.com/progresshans/godj/actions/runs/36397043744)는 해당 source의 실제 Fast Go step과
terminal success를 확인했다. 원문은 위 normal evidence의 `hosted/fast-run.json`, `hosted/fast-jobs.json`에 보존한다.
선행 full의 terminal success를 확인한 뒤 제품/검증 입력이 같은 문서 후속 commit
`f5b0020fa6c3f2c150eed720464b8e6765521113`의 [Hosted full 36397837881](https://github.com/progresshans/godj/actions/runs/36397837881)을
`suite=full`로 dispatch했다. 정확한 head/event/ref와 live queued 상태를 확인했고 `hosted/full-dispatch.json`에 보존했다.
해당 full은 attempt 1에서 **62 jobs / 8 owners 모두 success**로 완료했다. 최종 aggregate job `108874801710`의 실제
JSON에서 `scope=full`, `full_platform_verified=true`와 같은 Git source의 CI owner 집합 일치를 확인했다.
다음 새 capture의 archive checksum·producer attempt/provenance·payload SHA와 Git 객체 source binding도 확인했다.

| capture | artifact / producer job | Git source binding |
| --- | --- | --- |
| systemstate | `10958519547` / `108848428582` | 632 files / 6,693,101 bytes / `466aa524b5431b18e4f85544f3cfe52ad91e44439a9868f848e6b8c49979e36f` |
| operator | `10958528374` / `108848428562` | 709 files / 6,542,537 bytes / `4864a2dfd6899ac5170373f6c4694ff2eceebafba919e9ca9da878c7901bf267` |

최종 run/jobs·aggregate·capture receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-product-save-full-36397837881-iaom3qp_`에 보존한다.
이 full에는 이후 기본 saver·cache·initial 변경이 없으므로 그 결과를 후속 source의 검증으로 전이하지 않는다.

선행 저장 조정 source `ce52685c`의 [Fast 36393894699](https://github.com/progresshans/godj/actions/runs/36393894699)는
terminal success와 실제 Fast Go step 성공을 확인했다. 이 후속 callback/Helpdesk source의 검증으로 전이하지 않는다.
선행 typed 준비 full `36391162296` / `1b2fc492`는 **62 jobs / 8 owners 모두 success**로 완료했다. 이 후속 저장 코드는 포함하지 않는다.

같은 full의 새 capture 두 개는 archive checksum·정확한 파일 집합·producer attempt/provenance와 Git blob source binding을
확인했다. 최종 aggregate job `108847611870`의 실제 JSON도 `scope=full`, `full_platform_verified=true`와 8개 owner를
반환했다. 같은 Git SHA의 `scripts/ci/scopes.py`가 계산한 full owner 목록과 대조했으며 skip/cancel/실패는 0이다.

| capture | artifact / producer job | Git source binding |
| --- | --- | --- |
| systemstate | `10957076172` / `108827131757` | 631 files / 6,683,912 bytes / `51227f1c8a4cae15a6260f0cb750bffce995df2bcb4e379c10b0a02e6cbea888` |
| operator | `10956591365` / `108827131715` | 708 files / 6,533,348 bytes / `0a2e084d44864c1e1a4825a1f02aa8caa15b9c0d3550b71b5e51254709bc3e0c` |

Archive/payload SHA·검증 receipt·정확한 Git source inventory는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-typed-form-full-36391162296-z1evc7q1`에 있다.


### 후속 ModelForm scalar·collection 저장 조정

기반 `da4831303a7d733a0c1339511c8928c57ae12972` 이후 `PreparedInstance.Save/SaveCollections`와 typed
`CollectionSaver`를 구현했다. Caller 소유의 모델 pointer를 먼저 일반 ORM Save하고 선택 collection을 IR 선언 순서로
반영한다. 누락·중복·nil·제외 field/부족한 backend capability는 첫 scalar 쓰기 전에 거부한다. 모델/nullable pointee와
key 목록은 callback마다 분리하고 빈 선택은 clear, 제외는 무호출로 유지한다. 발급 PK/present zero·반복 저장 identity,
취소·만료 session·오류 그대로 전달과 deferred 관계 단계도 검증한다. Runtime은 암묵적 transaction/재시도나 권한 승인을 하지 않는다.

고정 Django 6.1 `fe0a859f537d4238cf49fca39073513206f83122` / CPython 3.14.3의 **15 cases × 양 DB**를
독립 관찰했다. SQLite 3.50.4와 private PostgreSQL 17.10 UTF8/libc/C의 관찰 차이는 0이다. 설치한 임시 app의 두
automatic ManyToMany를 사용해 신규/반복/deferred/미저장 relation·기존 교체/clear/제외·무효 scalar/choice·두 번째 relation
실패·명시적 atomic rollback·validation 뒤 선택 대상 삭제를 실행했다. 모든 commit=False의 I/O는 0이며 schema와
DB/container 정리를 확인했다. `uv run --frozen --offline --with 'psycopg[binary]==3.3.6'` overlay만 사용했고 lock은 불변이다.
Observer는 독립 작성했고 출처/라이선스 주석만 실행 파일 앞에 추가했다. 실행 본문과 tracked observer의 일치를 확인했다.
첫 wrapper 시도는 이전 observer의 `rows_restored` 키 검사를 남겨 결과 수집에서 실패했다. 새 observer의 준비 I/O·schema
정리 조건으로 수정했고 양 DB를 다시 실행했다. 이를 native 동작 실패나 Go PASS로 세지 않는다.

Generated `example.com/godj-model-save` 소비자는 양 DB에서 15개 native 사례의 유효성·오류·준비·저장 성공 여부·PK 존재와
실제 scalar/collection 결과를 대조한다. Python 객체 identity와 M2M signal event API는 Go 동등성 대상으로 세지 않는다.
특히 지연 FK로 literal COMMIT이 실패하는 **2개 사례**에서 Django는 IntegrityError지만 Go는 기존
`backend_error/commit_outcome_unknown`을 **반드시 유지**한다. 이후 새 조회로 테스트 DB 결과를 대조하며 자동 재시도하지 않는다.
현재 actor 거부·저장 직전 row 변경·선택 대상 범위 변경·collection 실제 쓰기 뒤 callback 실패·session 만료·context 취소
**6개 추가 사례**는 실제 DB의 coordinated relation transaction과 무변경/rollback을 확인한다. 최종 권한/revision 검사는
소비자 소유이며 이 API가 모든 제품의 adapter를 대체했다는 뜻은 아니다.

최종 비문서 source inventory `0ad55a985d8b896c9e2a1c14e16df47e272c4ba9dc23d1ac66da9ada60ee453c`에서
Darwin arm64 Go 1.26.5·offline/read-only module·TZ Pacific/Chatham의 normal/race/CGO=0 checkpoint를 실행했다.

| 검사 | normal | race | CGO=0 |
| --- | --- | --- | --- |
| Form 2 packages / 필수 79 roots | 1,497 PASS / 1.557초 | 1,497 PASS / 3.272초 | 1,497 PASS / 1.486초 |
| Generated save consumer 1 parent / 필수 child 45개 | PASS / 5.847초 | PASS / 12.511초 | PASS / 5.793초 |

각 mode에 private PostgreSQL 17.10 UTF8/libc/C를 사용했으며 SQLite도 실제 실행했다. 필수 누락·skip·잘린 JSON event는 0이고
parent는 모든 child의 정확히 한 번 PASS를 확인한다. Race child도 실제 race mode를 상속한다. 세 mode 모두 source 전후 동일,
DB 잔여 schema/table/다른 연결 **0|0|0**, DB/container 제거를 확인했다. CI의 relation/PostgreSQL 필수 parent roster에도 등록했다.
CI 도구 **41 tests**, 영향 `go vet ./forms/... ./codegen/consumertest`도 통과했다. 기존 generator/생성물/migration bytes는
변경하지 않아 전체 generation/full-platform을 로컬에서 관성적으로 반복하지 않았다.

최초 normal source `25a77fdff89f5c1f9a520c4b20fb29c8f99669a4e81f7e801d425fe15dfa54e2`에서는 Form
**1,460 PASS / 4 roots 실패**와 generated parent 실패를 보존했다. Unit fixture의 lowercase GoName을 바로잡았고,
redacted child summary만으로 추측하지 않고 진단 overlay에서 실제 SQLite의 위 **2개 COMMIT 오류 기대 실패**를 확인했다.
기존 backend 의미를 바꾸지 않고 정확한 outcome-unknown 기대를 추가했다. Final source에서는 양 DB/세 mode를 모두 다시 검증했다.

여섯 negative control은 실제 source를 바꾸지 않는 Go overlay로 필수 saver 생략·빈 선택 누락·collection 역순·callback pointee
공유·만료 callback 성공·미저장 collection 허용을 각각 주입했다. **6/6이 지정 runtime assertion에서 실패**했고 compile 실패/skip을
대조 성공으로 세지 않았다. Baseline·전후 inventory도 동일하다.

원문/receipt/source inventory:

- Native: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-save-reference-gsfe3nca`
- First normal/diagnostic: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-save-normal-dzrnth8j`
- Final normal/checks/negative controls: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-save-normal-ttgq080b`
- Race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-save-race-jwgtopzs`
- CGO=0: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-form-save-cgo0-erb39vgg`

이 후속 저장 source는 완료된 clean/typed 준비 full `36391162296` / `1b2fc492`에 포함되지 않는다.

### 독립 입력 기준과 Go 대조

고정 Django 6.1 / DRF 3.18.0 / CPython 3.14.3에서 Char/Email/Integer/Boolean의 null·blank·default
**32 profiles / 160 inputs**를 관찰했다. DB 조회는 0이다. Native source hash와 원문은 GDJ-0101 보완 evidence의
`next-input-policy`에 있다. 실제 임시 SQLite와 private PostgreSQL 17.10 UTF8/libc/C의 ModelForm **16 cases**는
오류·cleaned data·단계별 제외와 query 수가 일치했다. 각각 DB 불변과 table 정리, PostgreSQL DB/container 제거를 확인했다.
최초 PostgreSQL 시도는 psycopg 누락으로 실패했고 별도 `psycopg[binary]==3.3.6` overlay로 재실행했다. Lock은 그대로다.
이 관찰 단계의 **DB 16개 사례는 아래 후속 generated consumer에서 Go와 대조**했으며 원문은 같은 보완 evidence의 `next-model-postclean`과
`next-model-postclean-postgres-with-driver`에 있다.

아래 core source의 별도 Go probe로 160개 입력의 Form required/widget, 유효성·오류 code·cleaned data·model candidate를
대조했다. **156개 입력의 의미가 일치**했고 기존 nonnullable checkbox의 임의 문자열 입력 거부 **4개 차이**를 분리했다.
Django는 해당 문자열을 true로 읽지만 GoDj는 invalid로 거부한다. Integer의 기존 TextInput/inputmode와 Django NumberInput도
**8 profiles의 widget 차이**로 기록한다. 나머지 예상하지 않은 차이는 0이다. 전체 native 동등성으로 합산하지 않는다.
DRF serializer 정책·초기 렌더링·DB 검사 순서는 이 Go probe의 범위가 아니다.
첫 비교에서 `blank=True` 숫자 None을 모델 단계에서 거부한 잘못된 해석을 발견했다. Native model.clean_fields는
blank empty를 먼저 건너뛰므로 이를 수정했다. 읽기 검사의 NULL 생략도 저장의 nullable 검사와 분리했고,
기존 complete write 검증이 여전히 nonnullable NULL을 거부하는 검사를 추가했다. 최초 차이 원문도 보존한다.

### 기반 checkpoint

Core 비문서 source inventory `9278407916c01486032c4502b0ada4150d3206893f11c9d2e99f9d3c04b64f17`에서
schema/IR·migration/definition/backend·autodetect/graph·codegen·forms/model·ORM의 **11 packages / 750 required roots**를
normal로 실행했다. **3,333 PASS / skip 0**, 필수 누락 0, source 전후 동일이다. 초기 실행의 automatic relation owner
fixture 불일치·두 기존 Form fixture의 nullable/optional 기대를 바로잡았다. JSON optional fixture에는 명시적 Blank를
추가하고 선택 목록 검사에는 nonempty 입력을 사용했다. 빈 값 의미는 별도 null/blank/override matrix가 소유한다.
Typed-nil callback·잘못된 initial/spec 입력의 callback 이전 거부, secret formatting/입력 소유권,
기본값/명시적 empty·후속 validator에서 실패 값 제외·unique/constraint의 부분 candidate·present zero self exclusion,
read/row/close/cancel 실패의 partial diagnostic 폐기와 자원 정리를 포함한다.

다음 source `33926a5faa220b1fab70ca5b978fa33a6b5fd0e7181c2b1cec1a179a5382ce3f`에서는
별도로 생성한 실제 Go 모듈의 Blank consumer를 **SQLite·PostgreSQL 17.10 UTF8/libc/C**에서 실행했다.
필수 parent 1개와 child root/sqlite/postgres 3개를 strict event inventory로 확인했고 skip 0이다.
Blank scalar·automatic ManyToMany의 metadata 출력·wire·양 renderer DDL 0·forward/reverse·연결 재개 후
기존 malformed email/빈 문자열/default·관계 link·자동 ID 보존을 확인했다. DB **0|0|0**, DB/container 제거와
source 불변을 확인했다. 최초 consumer는 First의 필수 명시적 ordering 누락으로 실패했다. 제한된 기본 진단만으로
원인을 추정하지 않고 별도 진단 overlay의 child 원문에서 런타임 실패를 확인해 수정했다.
Core source와의 차이는 이 consumer 한 줄과 CI 필수 목록 두 파일뿐이며 core 전체 재실행으로 합산하지 않는다.

이어 자동 계획의 scalar/ManyToMany 양방향 변경·정확한 logical operation·결정성·재구성과 mixed storage/binding 거부
**7 PASS / skip 0**를 추가했다. 초기 test의 정규화 전 field index 가정 오류를 고친 재실행이다.
최종 inventory `43907ed2525141978fed72d79b4b5562dd2497e9f8d27c0c55aca2b6dae60443`는 직전 source와
이 자동 계획 test 파일만 다르다. 영향 `go vet`와 CI 도구 **26 tests**도 통과했다.
160개 문서의 코드 구문을 제외한 로컬 링크 target 1,245개와 diff 검사를 통과했다. Heading fragment는 검사 범위가 아니다.
Darwin arm64 / Go 1.26.5 / offline readonly이며 core는 `TZ=Pacific/Chatham`이다.
실행·roster·JSON 전체 로그·실패/재실행·native Go probe·source inventory/차이·cleanup receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-form-core-6xu_ecd0`에 있다.
최초 core/Go 대조 실패는 같은 parent의 `godj-blank-form-core-s_w3dh7t`에 보존한다.

선행 기반 compile-only source `bab62e7aa2823df1714de716da0e2341d2b800a48d875b0b489cc3252cc3e412`의
11 packages와 후속 forms/model/ORM 세 package compile 확인은 동작 PASS 수에 포함하지 않는다.
원문은 `godj-model-blank-foundation-gitc3a4s`와 세션 출력이다. 아래 제품 전환과 검증은 후속 source의 별도 checkpoint다.
전체 DB ModelForm 대조와 부정 대조·이후 소스의 Hosted 전체는 별도로 남아 있다.


### 제품 선언·원래 제출과 현재 row의 모델 후보

비문서 source inventory `c0af85b28dff1e00bdaa0725020ab4455a6541e13982ea056c96c9705306a74a`에서
User의 optional scalar·Group/User 컬렉션, Helpdesk의 optional scalar·labels, Article summary에 Blank를 명시했다.
기존 migration을 수정하지 않고 Identity `0004_auto_1da4dbd173ec` 7개, Helpdesk `0021_auto_72857bab23b6` 13개,
Article `0002_alter_article_summary` 1개 연산을 추가했다. 모든 연산이 before의 Blank=false에서 true만 바꾸며
나머지 scalar/relation 속성은 같고 기존 세 프로젝트의 tracked migration bytes는 그대로임을 확인했다.

Admin과 재사용 User creation Form을 model Bind로 전환했다. 저장 직전 재검증은 원래 입력을 사용하므로 생략/default를
유지하고 non-idempotent string normalizer를 정리 값에 다시 적용하지 않는다. 수정은 인가된 현재 row를 다시 읽어
revision을 확인하고 그 값에서 candidate를 만든다. Pure model validator가 있으면 Initial은 제외된 stored scalar도
포함해야 한다. Registry가 PK를 소유하고 추가 값은 rendered initial과 typed mutation input에 포함하지 않는다.
선택 입력 실패 뒤의 모델 clean, nil validator의 callback 이전 거부, 불완전/위조된 현재 snapshot과 최종 fence를 검증한다.

실제 CLI generate는 internal/projectwire의 Blank 전달·wire 크기 계산 누락을 드러냈고 scalar/collection의 optional bool과
정확한 크기 경계·중복/잘못된 타입 거부를 함께 수정했다. 실제 Helpdesk makemigrations는 private protocol 정규화와
snapshot 복사가 External을 잃는 문제를 드러냈다. 전체 AppSpec을 복사하고 imported declaration을 host managed app에서
제외했다. 공급 history와 정확히 같아야 하며 누락·불일치·host filesystem의 소유권 주장은 후보를 공개하지 않는다.
소유 app의 외부 FK 의존·역사 재구성·두 번째 no-op도 검증했다. Directory를 꾸며 우회하지 않는다.

| 동일 source 영향 검증 | Required roots | PASS | Test skip | 시간 |
|---|---:|---:|---:|---:|
| normal | 304 | 2,662 | 0 | 24.773초 |
| race | 304 | 2,662 | 0 | 139.277초 |
| CGO=0 | 304 | 2,662 | 0 | 32.046초 |

23개 test package의 source 파일에서 active-platform root를 독립 수집했으며 필수 누락·실패·잘린/비정형 JSON은 0이다.
테스트 파일이 없는 14개 package의 Go `skip` event는 no-test roster로 따로 기록했고 test skip으로 합치지 않았다.
환경은 Darwin arm64 / Go 1.26.5 / offline readonly / `TZ=Pacific/Chatham`, private PostgreSQL **17.10 UTF8/libc/C**다.
각 mode에서 실제 Article/Helpdesk PostgreSQL 소비자를 포함하고 DB **0|0|0**, DB/container 제거와 source 불변을 확인했다.
Identity의 DB backend 전용 제품 검사와 별도 child process는 아래 후속 checkpoint의 소유다.

최초 source `cacb52ffca9796e6a7e88b7563edb5b5ef156280c6344536d9e30761a0472517`의 실행은 세 root에서 실패했다.
새 빈 title fixture의 표시 label도 빈 값이던 오류, 누락 initial의 개선된 오류 code 기대, 새 generated metadata와 이전
Article migration만 읽던 PostgreSQL fixture를 수정했다. Article의 현재 schema를 쓰는 PostgreSQL와 stable-root/reopen
검사는 새 migration도 읽고 정확한 history를 확인한다. 과거 migration 파일 자체는 바꾸지 않았다.
첫 실행 로그·receipt를 보존하고 재실행 결과와 합산하지 않는다.

같은 source에서 Identity·Identity fixture·Helpdesk·Article의 `generate --check` 네 번과 Identity·Helpdesk·Article의
`makemigrations --check` 세 번이 성공했고 source 전후 동일이다. 영향 `go vet`도 통과했다.
각 source inventory·root roster·모든 event·실패 원문·DB cleanup과 drift 명령 결과는 아래 임시 evidence에 보존한다.

- normal: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-product-normal-7rthvmly`
- race: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-product-race-c8o1pzo2`
- CGO=0: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-product-cgo0-_w2s8raw`
- initial failure: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-product-normal-3illnvpz`

동일 `c0af85b2` source의 후속 actual DB/process checkpoint는 **5 packages / 14 roots / 394 required root/subtest 항목**으로
normal **410 PASS / skip 0 / 74.776초**, 필수 누락 0이다. SQLite·PostgreSQL 각각 Identity email migration, 실제 관리 Form,
사용자 생성 Form/복합 오류·현재 권한 **196 PASS**, 실제 global migrate **8 PASS**, SQLite/PostgreSQL runserver와 stale source
거부 **8 PASS**, 양 DB A/B/C 독립 process 재시작 **2 PASS**다. 이 checkpoint의 race/CGO=0은 실행하지 않았다.
DB **0|0|0**과 database 제거를 확인했다. 최초 orchestration은 roster loop가 container-name 변수를 덮어써 container cleanup을
완료하지 못했으므로 첫 receipt는 false로 보존했다. 별도로 기록해 둔 해당 container ID와 name을 대조한 뒤 그 container만
종료하고 제거를 확인해 final receipt를 갱신했다. 테스트 재실행으로 세지 않는다. 원문은
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-integration-normal-tvgp80sr`에 있다.

그 뒤 revision 검사에서 실제 typed update callback도 호출되지 않아야 한다는 assertion을 Admin test 한 파일에 보강했다.
Source `b4fde05f9c4786b905e213bee78f9df94048a2526d7babc2535427a9386b5661`에서 Admin **71 roots / 193 PASS / skip 0**를
normal/race/CGO=0 각각 확인했다. Go overlay 세 개가 정확한 runtime assertion을 실패시킴을 확인했다:
client Form.Initial을 현재 row 대신 사용, pure model validator 생략, observed revision 검사 생략이다. Compile 실패나 단순
exit nonzero를 부정 대조 성공으로 쓰지 않는다. 양 backend의 정확한 capability 목록 **2 PASS**도 세 mode에서 확인했다.
이 범위의 source 전후 동일과 source 차이, 세 overlay·전체 로그는 normal evidence의 `admin-controls`에 있다.

현재 제품 generated metadata가 Article summary의 Blank를 포함하므로 최종 foundation도 통합 검증했다. 처음에는
`TestGenerateIsDeterministicAndContainsProvenance`의 이전 Article schema hash 기대가 세 mode에서 실패했다.
Article의 현재 canonical IR에서 summary Blank만 false로 되돌리면 이전 고정 hash `3e6ec104…`가 재현됨을 독립 확인하고,
새 hash `922c4f2b…`를 고정했다. 생성 결과만 그대로 복사해 의미 변화의 검사를 없애지 않았다.

최종 비문서 source `7aa5b67934029ebd4e3b28666f2f9014d501e5fa773a7a414d8b969c24508df7`에서 foundation
**11 packages / 751 roots / 3,340 PASS / skip 0**를 normal **5.839초**, race **13.852초**, CGO=0 **4.174초**에 각각 완료했다.
모든 필수 root·source 불변을 확인했다. 앞선 product `c0af85b2`와의 차이는 Admin assertion과 generator hash 기대의
**두 test 파일만**이며 앞선 제품/DB/process 실행을 최종 source에서 다시 했다고 합산하지 않는다.
실패 source/로그도 보존한다. 세 최종 core evidence는 각각
`godj-blank-final-core-normal-yhmjmww7`, `godj-blank-final-core-race-3fss7vwn`, `godj-blank-final-core-cgo0-jibfwpiu`이며
모두 위 evidence들과 같은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T` 아래에 있다.
CI 도구 **41 tests**, 160개 문서의 local target **1,245개**와 `git diff --check`도 통과했다. Heading fragment는 검사하지 않았다.
전체 ModelForm DB 후처리나 이 새 소스의 Hosted 전체 성공이 아니다.


### Blank Hosted Fast의 이력 기대 보완

구현 commit `037f8b6f9a90e325b9e7aec6e30fed6932bff0de`의
[Fast 36375502838](https://github.com/progresshans/godj/actions/runs/36375502838)은 systemstate의 두 테스트에서 실패했다.
`TestIdentityTransitionDefinitionMatchesNormalizedSchemaAndIsDetached`는 내장 migration 문서 수를 이전 5개로,
`TestIdentityHistoryRequiresEveryOwnedMigrationExactlyOnce`는 이전 다섯 key로 기대했다. 제품의 canonical 이력 검사는
새 `godj_identity.0004_auto_1da4dbd173ec`를 요구하고 있었다. 문서 수를 6개로, 독립 key roster에도 새 migration을
명시했다. 누락/중복/미지 key 거부와 원본 document의 소유권 검사는 유지하며, 새 key의 누락·중복도 같은 검사를 받는다.

Clean primary의 `037f8b6f` 위에서 이 두 test 파일만 변경해 `./systemstate` **68 required roots / 210 PASS / skip 0**를
normal **9.664초**, race **22.881초**, CGO=0 **3.501초**에 각각 확인했다. 필수 누락 0, source 전후 동일이며 제품 코드는
변경하지 않았다. 최초 Hosted 실패 로그와 exact changed-file hash·roster·세 JSON event stream은 기존 final core normal
 evidence의 `hosted-fast-failure/history-repair`에 있다. 진행 중인 별도 DB 후처리 작업과 결과를 합치지 않는다.

보완 commit `65a0e6ba750a8cd2d7ce0d09ed107a340cc2d16f`의
[Fast 36378041214](https://github.com/progresshans/godj/actions/runs/36378041214)은 실제 `Fast Go feedback` 단계까지 성공했다.
이후 DB 후처리 작업본의 Hosted 결과는 아니다.

### 인가된 읽기 scope의 모델 DB 후처리

기반 commit `65a0e6ba`의 비문서 source inventory
`d5805bef28bd0fa34b93472eed5575a975a2fea16efc70e178091dba02960fdd`에서 `BoundForm.CheckDatabase`를 구현했다.
Unique field 진단을 내부 Form 복사본에 적용한 뒤 constraint의 제외와 candidate를 다시 계산한다. 이미 실패한 field가
있어도 다른 유효 값은 검증하며 새 진단만 반환한다. 실행·취소 실패는 이전 단계의 진단까지 버리고 원인은 보존하되
일반 formatting으로 raw DB 오류를 노출하지 않는다. Callback map 변경도 다음 단계나 caller의 Form을 바꾸지 않는다.

Admin의 ValidateCreate/ValidateChange는 BoundForm을 받는다. CSRF·입장/선택 권한과 change의 observed revision 뒤에
실행하고, registry가 현재 Snapshot의 PK와 revision을 후보에 공급한다. 명시한 initial 값이 다르면 거부한다.
기본 User 수정은 같은 management snapshot에서 현재 저장된 actor 권한·대상 row/revision·DB 제약을 확인한다.
Ticket과 TicketLabel은 같은 read snapshot에서 category·현재 row를 확인하고, TicketLabel은 선택 목록 이후 바뀐
양 endpoint의 소속을 재검사한다. 범위 밖 endpoint는 constraint 검사에서도 제외하여 foreign tuple 존재를 노출하지 않는다.
입력 진단은 read scope cleanup 성공 뒤에만 공개하며 최종 write transaction·revision·DB 제약 검사는 그대로다.
기존 late DB conflict 테스트는 새 advisory 조회와 마지막 write 조회 모두를 명시적으로 건너뛰게 하여 실제 저장 실패를 계속 검증한다.

| 동일 source checkpoint | Required roots / required cases | normal PASS / 초 | race PASS / 초 | CGO=0 PASS / 초 |
|---|---:|---:|---:|---:|
| forms/Admin/Identity/Helpdesk/systemstate, 9 test packages | 235 / 536 | 2,572 / 32.519 | 2,572 / 125.274 | 2,572 / 24.497 |
| SQLite·PostgreSQL Identity ManagementAdmin, 2 packages | 2 / 130 | 130 / 24.972 | 130 / 61.873 | 130 / 18.831 |
| 독립 생성 ModelForm DB 소비자 parent | 1 / 1 | 1 / 2.609 | 1 / 6.186 | 1 / 2.292 |

모든 필수 항목과 package 완료를 확인했고 test skip·누락·실패·잘린/비정형 JSON은 0이다. 첫 그룹의 테스트 없는
5개 identity package는 no-test roster로 따로 기록한다. 새 Identity DB 사례는 복합 오류·same row·현재 권한 회수·revision 변경,
읽기/정리 실패·foreign model/key/revision·unbound·취소와 실제 HTTP 복합 오류를 포함한다. Borrowed reader의 수명과
무해싱·profile/credential/session/audit 불변도 검증한다. Helpdesk의 관계 재검사는 stale choice에서 tuple 조회 0을 확인한다.

독립 공개 import module은 양 DB 각각 native 16개 사례를 실행하며 root/backend/case **35개 child 필수 항목**을
strict JSON event 검사로 확인한다. Parent race/CGO 모드는 기존 generated command 환경으로 child에 전달한다.
각 DB의 **15개 사례**에서 유효성·오류 code·cleaned data·unique/constraint 단계별 제외·조회 수와 pure model clean 실행이
native 관찰과 일치하며 저장 row는 불변이다. **1개 excluded_extra**는 Django가 허용하는 제외 field 이름의 추가 입력 재사용을
GoDj가 construction 단계의 shadows_model로 거부하는 기존 차이로 명시한다. 16개 전체 동등성으로 세지 않는다.
Checked-in reference에는 native 원문 SHA와 Django source hash·고정 버전/라이선스·양 DB 정리 근거를 보존했다.

환경은 Darwin arm64 / Go 1.26.5 / offline readonly / TZ=Pacific/Chatham, private PostgreSQL **17.10 UTF8/libc/C**다.
세 mode의 source 전후 동일, DB **0|0|0**, DB/container 제거를 확인했다. Evidence는 각각
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-db-normal-srsfhzf2`,
`godj-model-db-race-aqe9ffnv`, `godj-model-db-cgo0-vsuj7i0e`이며 뒤 두 경로도 같은 임시 parent 아래다.
Normal evidence의 `negative-controls`에는 unique 오류 적용 생략·실패 시 부분 진단 공개·현재 actor 권한 확인 생략·
관계 소속 재검사 생략의 Go overlay 네 개가 각각 지정한 runtime assertion에서 실패한 원문과 source 불변 receipt가 있다.
Compile 실패나 일반 nonzero만을 부정 대조 성공으로 세지 않았다.

영향 go vet는 성공했다. 최초 CI 도구 검사는 forms/model의 네 root를 relation owner 필수 목록에 잘못 넣은 것을
검출했다. 해당 package는 원래 portable core의 전체 package 실행 소유이므로 잘못된 relation 목록 네 줄만 제거했다.
Generated 소비자·Identity DB의 relation 필수 목록과 PostgreSQL 필수 항목은 유지한다. 실제 Go 검사를 다시 했다고
합산하지 않으며 수정 전/후 source delta와 CI 도구 재실행은 normal evidence의 `static-checks`/`static-checks-retry`에 있다.
재실행은 **41 tests 성공**이며 최종 inventory `74750eb98b07a2d611ed665b6289556dce0b2ef91f3bb16145b3035e374f85da`와
앞선 checkpoint의 차이는 `scripts/ci/relation-required.txt` 한 파일이다. 제품·Go test bytes는 동일하다.
모델 metadata·generator는 이 DB 후처리 묶음에서 변경하지 않았고 새 independent consumer는 매 실행 실제 생성한다.
이 scoped checkpoint는 최신 source의 전체 platform/process milestone 또는 전체 ModelForm 완료가 아니다.

### Group·Permission·Label·ServiceReport의 읽기 후처리

기반 `b8cc51b56a5c03a1815181ced5406e1154cdc2b9` 위의 비문서 source inventory
`d9b1a3111ba51a63472d6292f53f9689085f6098320258a4d3c73de6c480c004`에서 나머지 기본 관리 폼을 연결했다.
Group/Permission create/change는 올바른 모델과 unsaved/current ID·revision 후보를 I/O 전에 확인하고, 같은 management
snapshot에서 현재 actor 권한·현재 row/revision·unique/constraint를 확인한다. Group은 선택 권한의 삭제도 다시 확인한다.
유효한 중복 field는 다른 입력/선택 오류가 있어도 검사하며 오류를 합친다. 마지막 저장의 권한·revision·관계·사용자 grant 한도는
기존 transaction에서 계속 검증한다. Group 관계의 읽기 진단과 I/O 실패는 별도 반환값으로 구분한다.

Label은 서버가 고정한 category만 constraint 후보에 명시적으로 공급해 이름과의 tuple을 검사한다. Excluded initial의
category를 신뢰하거나 editable input에 포함하지 않는다. 이는 제품의 고정 parent 범위 검사이며 일반 ModelForm이
제외된 값을 자동으로 포함하는 규칙이 아니다. ServiceReport는 같은 snapshot에서 현재 row와 선택 Ticket 소속을 확인한 뒤
OneToOne unique를 검사한다. 범위 밖 Ticket은 unique 조회에서 제외하므로 private report 존재를 공개하지 않는다.
Helpdesk read scope의 typed-nil reader도 일반 오류로 거부한다. 기존 Label/Report fault adapter는 새로운 사전 읽기와
최종 write 읽기에 같은 실패 주입을 적용하며 late native constraint·rollback/unknown 검증을 유지한다.

| 동일 source checkpoint | Required roots / cases | normal PASS / 초 | race PASS / 초 | CGO=0 PASS / 초 |
|---|---:|---:|---:|---:|
| Identity/Helpdesk, 5 test packages | 30 / 547 | 746 / 21.547 | 746 / 167.282 | 746 / 39.293 |
| 양 DB Identity ManagementAdmin/CatalogManagement, 2 packages | 4 / 370 | 370 / 48.470 | 370 / 159.789 | 370 / 53.564 |

모든 필수 항목·package 완료와 source 불변을 확인했고 test skip·누락·실패·비정형 JSON은 0이다.
Core의 테스트 없는 identity 5 packages는 no-test roster로 따로 기록한다. 신규 catalog matrix는 네 create/change 경로의
**50개 사례**와 실제 HTTP 복합 오류를 포함한다. 같은 row·현재 권한 회수·revision 변경·삭제·읽기/정리 실패,
잘못된 모델/키/revision·취소·unbound·삭제된 선택과 순서/중복 선택, 빌린 reader 수명과 무해싱·전체 durable 상태 불변을 확인한다.
Helpdesk는 Label/ServiceReport의 서버 범위·복합 오류·같은 row·외부 row/관계·정리 실패를 검사하고,
양 DB 실제 Admin 소비자에서도 입력 오류/중복 거부가 write transaction을 시작하지 않음을 확인한다.

네 부정 대조는 Label의 서버 category를 excluded initial로 바꾸기, Report의 Ticket 소속 검사 생략,
Group의 현재 권한 검사 생략, Permission의 현재 revision 검사 생략을 각각 지정 runtime assertion으로 검출했다.
Group 첫 overlay는 미사용 permission 변수 때문에 compile 실패했으므로 검출 성공으로 세지 않았다. 앞의 두 성공을 재실행하지
않고 수정한 Group과 남은 Permission만 실행했다. 원래 실패·두 receipt·source 불변과 네 결과의 독립 집계를 보존했다.
영향 vet와 CI 도구 **41 tests**도 성공했다. 모델 선언·generator는 바뀌지 않아 generated drift를 반복하지 않았다.

Darwin arm64 / Go 1.26.5 / offline readonly / TZ=Pacific/Chatham에서 각 mode 전용 PostgreSQL **17.10 UTF8/libc/C**를 사용했다.
DB **0|0|0**, DB/container 제거를 확인했다. 전체 JSON·roster·계획·source/cleanup receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-catalog-normal-7v5xa_c4`,
`godj-model-catalog-race-32k4re6w`, `godj-model-catalog-cgo0-6om1qtqk`에 있다. 뒤 두 경로도 같은 임시 parent 아래다.
Normal evidence의 `static-checks`, `negative-controls`, `negative-controls-retry`, `negative-controls-combined.json`에 추가 검증이 있다.
Blank 선언·내부 API와 새 DB 후처리를 포함한 source의 Hosted 전체는 다음 통합 milestone이며 선행 ea2867f4의 성공을 전이하지 않는다.

구현 commit `d2c9d6fef328af5a87dd61cc507b61fe8295912a`를 양 작업 branch에 원자적으로 push하고
[Hosted full 36381300069](https://github.com/progresshans/godj/actions/runs/36381300069)을 `suite=full`로 시작했다.
기존 ea2867f4 run이 모두 종료한 뒤 새 source를 확인했으며 현재 결과는 진행 중이다. 로컬 전체를 중복 실행하지 않는다.
현재 필수 Go source·생성물·DB/process·platform·capture 검증은 이 통합 milestone이 소유한다.

### Hosted 통합의 선택 입력·동시 migration fixture 보완

위 d2c9d6fe full에서 선택 입력의 두 소비자와 양 DB 실제 동시 migration 검사가 실패했다.
`conformance/choicesproduct`의 priority 모델은 독립 Django `choices_reference.py`의 `null=True, blank=True` 중
Blank가 누락돼 있었다. 독립 generated choices consumer도 optional reason/priority의 Blank 선언이 없었다.
Generated child의 원래 remote 오류는 진단을 제한했으므로 내용만 추정하지 않았다. 임시 Go overlay로 해당 SQLite-only
child의 전체 test event를 받아 `optional choices rejected empty input`이라는 실제 runtime 실패를 확인했다.
두 Go fixture에 Blank를 명시하며 독립 Django 원문·오류/입력 기대와 제품의 Nullable/Blank 분리는 유지한다.

동시 migration용 독립 프로젝트는 Article `0001_initial`만 복사하고 있었다. 현재 기대 catalog는 새 Blank 이력을 포함한
8개 definition이므로 실제 7개 begin과 정확한 8개 기대가 달랐다. Fixture가 현재 Article의 모든 `.godj.json`을 복사하게 했다.
8개 history/18 operations, 정확한 private response·source digest, 하나의 승자/하나의 revision contention·무재시도와
재조정 no-op의 기존 검사는 그대로다. 세 test 파일만 변경했고 제품 코드·기존 migration·oracle은 변경하지 않았다.

실패 jobs 원문·단계·source JSON과 generated child 진단은 catalog normal evidence의
`hosted-repair-1790573414603119000`에 있다. 보완 source inventory는
`633062b437e0e642987aef5ba8c0fe67bfafb757f9e6347e15c542ea1499df9a`이며 아래 동일 source checkpoint로 검증한다.
동일 source에서 아래 세 mode를 완료했다. 모두 skip·필수 누락·실패·잘린/비정형 JSON 0이며 실제 양 DB/독립 CLI·두 process·재시작을 포함한다.

| 범위 | normal PASS / 초 | race PASS / 초 | CGO=0 PASS / 초 |
|---|---:|---:|---:|
| choices, 3 roots | 24 / 1.647 | 24 / 3.312 | 24 / 1.825 |
| generated choices, parent 1·필수 child 2 | 1 / 2.110 | 1 / 5.503 | 1 / 2.251 |
| 전체 global migrate, 6 roots | 16 / 160.502 | 16 / 179.546 | 16 / 183.816 |

Race는 generated child에 실제 적용되며 global CLI/process child는 기존 일반 build 정책, harness는 해당 mode를 따른다.
Darwin arm64 / Go 1.26.5 / offline readonly / TZ=Pacific/Chatham, 각 mode 전용 PostgreSQL 17.10 UTF8/libc/C다.
영향 vet, source 전후 동일, DB 0|0|0과 DB/container 제거를 확인했다. Source delta는 위 세 test 파일뿐이다.
환경·전체 JSON·roster·source/cleanup receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-blank-hosted-normal-prremmjq`에 있다.
Race/CGO=0은 같은 임시 parent의 `godj-blank-hosted-race-oqc4bzb6`, `godj-blank-hosted-cgo0-7smskwu5`다.
원격 실행은 보완 source에서 새로 검증하며 실패한 d2c9d6fe 실행을 성공으로 합치지 않는다.

보완 commit `211499d05f6763e3dc5ecf501fd4539393c264cb`를 양 branch에 원자적으로 push했고
[Fast 36383495153](https://github.com/progresshans/godj/actions/runs/36383495153)의 실제 Go 검사도 성공했다.
[보완 full 36383539735](https://github.com/progresshans/godj/actions/runs/36383539735)을 같은 source의 `suite=full`로 시작했다.
실패가 확인된 기존 d2c9d6fe run은 workflow의 같은 ref concurrency 정책으로 대체 취소되어
**33 성공 / 17 실패 / 11 취소 / 1 skip**, run conclusion `cancelled`로 종료했다. 부분 성공이나 capture를 전체 성공으로 재사용하지 않는다.
Final 상태·실패 로그와 replacement source/Fast/run JSON은 위 Hosted repair evidence에 보존한다.
211499d0의 full은 후속 감사에서 62 jobs·8 owners와 새 capture 결합을 확인해 완료했다. 아래 최종 집계 기록을 따른다.

### 다음 model clean 값 변환의 독립 관찰

Go 구현에 앞서 고정 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122`, CPython 3.14.3으로
실제 SQLite 3.50.4와 private PostgreSQL 17.10 UTF8/libc/C에서 각각 **13개 사례**를 관찰했다.
Model/form source 두 파일의 SHA는 앞선 고정 native 관찰과 일치하고 uv.lock은 변경하지 않았다.
캐시된 psycopg 3.3.6의 임시 uv overlay를 사용했다. 두 DB의 유효성·오류·cleaned data·candidate·단계별 trace와
조회 수, commit=False와 rollback으로 감싼 실제 save 결과는 모두 같았다. 모델 row는 매 사례 뒤 복원하고 table·DB/container를 정리했다.

Model clean은 cleaned data를 그대로 둔 채 instance를 바꾸며 DB unique/constraint는 바뀐 candidate를 본다.
이미 발생한 field 오류는 값을 고쳐도 사라지지 않는다. Model clean 이후 field 문법을 재검사하지 않으므로 이메일을
잘못된 문법으로 바꾸어도 이 단계만으로 form 오류가 생기지는 않는다. 제외된 field의 변경도 native save에 반영될 수 있지만
해당 unique는 제외돼 실제 저장에서 IntegrityError가 날 수 있다. Commit=False는 I/O 없이 같은 instance를 반환하고,
invalid form의 prepare/save는 ValueError로 실패한다. Clean의 반환 mapping 자체는 native instance를 바꾸지 않는다.

이 관찰 단계만으로 Go의 값 변환/제외 field 저장 API를 구현하거나 차이를 채택했다고 기록하지 않았다.
이후 Go의 소유권 채택과 소비자 검증은 다음 checkpoint로 구분한다.
원문·observer SHA·명령·비교·정리 receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-model-clean-reference-wo050u9o`에 있다.

### Model clean의 명시적 변경과 저장 입력

`PostClean.Fields/Clean/Validators`로 pure 모델 변환과 읽기 validator를 함께 구성한다. Field cleaning 뒤의 후보만
바꾸고 Form.Cleaned·원래 제출·기존 오류를 유지한다. 후보는 후속 DB 검사에 사용하며 selection/error exclusion은
다시 계산한다. Input은 선택 입력 외에 명시적으로 반환한 제외 scalar 변경만 포함한다. PK·컬렉션·command input·미선언
변경과 잘못된 값 kind는 거부한다. Admin은 현재 전체 row·PK/revision 소유권, 생성/수정 typed 저장·audit 검사를 연결한다.

독립 생성 Contact 모듈은 앞선 고정 Django **13 cases × SQLite/PostgreSQL**의 유효성·오류·cleaned data·후보,
clean 전후·unique/constraint trace와 조회 수, typed 준비·실제 ORM Save·row 수와 rollback 복원을 대조했다.
변환된 이메일의 field validation을 재실행하지 않고, 제외 hidden의 중복은 실제 저장의 unique 제약으로 거부한다.
Invalid form은 typed 준비와 저장에 진입하지 않는다. Python clean의 무시되는 반환 mapping은 Go의 빈 변경 집합에
대응시키며 반환 규약과 Python instance identity를 Go API 일치로 주장하지 않는다. Go 준비는 detached typed 값이다.
기존 16-case DB consumer도 함께 실행해 단계별 진단·제외의 회귀를 확인했다.

| 범위 | normal PASS / 초 | race PASS / 초 | CGO=0 PASS / 초 |
|---|---:|---:|---:|
| Form/Admin/Identity/Helpdesk/Systemstate, 9 test packages / 243 roots | 2,620 / 24.332 | 2,620 / 155.192 | 2,620 / 41.166 |
| Identity 실제 양 DB, 4 roots / 370 required cases | 370 / 53.697 | 370 / 150.285 | 370 / 57.670 |
| 독립 생성 모듈, 2 parents / 필수 child 35+29 | 2 / 3.992 | 2 / 10.681 | 2 / 4.190 |

모든 성공 범위에서 test skip·필수 누락·비정형/잘린 JSON은 0이다. Race는 generated child에도 적용한다.
Darwin arm64 / Go 1.26.5 / offline readonly / TZ=Pacific/Chatham, 각 mode 전용 PostgreSQL 17.10 UTF8/libc/C다.
각 source 전후 동일, DB **0|0|0**, DB/container 제거를 확인했다. 일반 실행의 첫 core source는
`6ffb3d8fac3dbb7f9f9e05bdc490dd89510b9d4dd2dec9efc55b8c6779fd70c2`이며 동일 실행의 DB/새 consumer 실패는 성공으로 세지 않는다.
Internal Identity fixture의 Bind 인자 전환 누락과 consumer의 First 명시적 ordering 누락을 수정했다. Child의 원인은 임시
진단 overlay로 실제 runtime event를 확보해 확인했다. 이 두 test-support 파일만 바뀐 source
`25cfe8bac2c58e6f7aed12ec9aa718132b0e9f04acd4e283efc11298e58bc485`에서 DB/두 생성 parent를 재검증했다.

최종 inventory는 `24ce7de04bc6c8e025480db85b07b368959620672e9f3dabd10031963f409dc4`다. 직전과의 차이는
relation-required의 잘못 배치한 Admin 10 entries 제거뿐이다. Admin은 portable core가 소유하며 위 core 검사에서
해당 두 root와 HTTP 8개 사례를 실제 실행했다. 최초 CI 도구의 owner 오류 10개와 수정 뒤 **41 tests PASS**를 구분한다.
Race/CGO=0은 최종 inventory에서 전체 위 scoped checkpoint를 실행했다. 범위 밖 로컬 전체 테스트를 반복하지 않았다.
영향 vet와 **203 packages compile-only / 실행 test 0**도 성공했다. Compile-only를 제품 동작 PASS 수에 합치지 않는다.

최종 source에서 overlay로 제외 변경의 저장 입력 전달, clean 선언 소유권, DB 검사의 변환 후보 사용,
Admin revision 보호, Admin의 현재 전체 snapshot 요구를 각각 제거한 **5개 부정 대조**가 지정 runtime assertion에서
실패했다. Compile 실패로 대신한 사례·skip·source 변경은 0이다. HTTP는 생성/수정·필드 오류·숨긴 입력 위조·CSRF·권한
거부·이미 stale인 revision과 최종 transaction 경쟁을 포함한다. Form 정의 복사·동시 bind 후보 격리도 검사한다.

전체 JSON·필수 roster·source inventory/delta·최초 실패·진단 원문·cleanup receipt는 같은 임시 parent의
`godj-model-clean-normal-d7j8dfzk`(첫 core/실패), `godj-model-clean-normal-zpoc8y08`(DB/생성 재검증),
`godj-model-clean-race-19sg4rxt`, `godj-model-clean-cgo0-ec2clpgb`에 있다. Parent는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T`다. 마지막 cgo0 directory의 `negative-controls`에 5개 overlay와 receipt,
normal 재검증 directory의 `final-checks`에 vet/CI 도구/compile-only 원문을 보존한다.

선행 full `36383539735`, source `211499d0`, attempt 1의 두 새 capture는 archive checksum·정확한 3-file 구성·같은
run/producer attempt·payload checksum/provenance를 검증했다. 현재 작업 파일 대신 해당 SHA의 Git blob에서 source binding을
독립 재구성했다. Systemstate는 artifact `10953619007` / producer `108804427006`, **628 files / 6,662,330 bytes** /
`bdcb69932aa1bd4063913cc17bdede3755704b96b5da5288422cda75aa96d1c5`, operator는 artifact `10952634816` /
producer `108804426971`, **705 files / 6,510,618 bytes** /
`dc5d3c44416ddce338d46a7ef3703ad46e8d8c691f52a456ca130ba0f783a580`로 일치했다. Archive·inventory·receipt는
기존 Email reference directory의 `hosted-full-36383539735-1790576439131454000`에 있다. 해당 source `211499d0`의 full은
2026-09-28 최종 감사에서 **62 jobs 모두 success**를 확인했다. Aggregate job `108822511109`의 실제 JSON은
`scope: full`, `full_platform_verified: true`, 필수 8 owners이며 같은 Git SHA의 scopes.py로 재계산한 결과와 일치했다.
Artifact ID/같은 attempt/checksum도 완료 시점에 다시 확인했다. Final run/jobs·aggregate 원문·최종 PASS receipt를 같은
directory에 보존한다. 이 성공은 model clean `36632e24`와 이후 typed 준비를 포함하지 않는다.
Model clean `36632e24bdd79b1002ef5ce98944d60cb8691995`의 [Fast 36386575601](https://github.com/progresshans/godj/actions/runs/36386575601)는
terminal success와 실제 Fast Go feedback step success를 별도로 확인했다. 빠른 검사를 Hosted 전체와 합치지 않는다.

### 생성 모델의 typed 준비와 nonnullable NULL 경계

기반 commit `36632e24`에서 `ValueAssignmentDescriptor.SetFieldValue`를 생성하고 ORM Manager의 `Metadata/ModelValues/ApplyValues`,
Form의 `BindInstance/PreparedInstance`를 구현했다. 생성 코드는 scalar의 직접 Go 대입을 소유하고 runtime은 선언 소유권·
타입/nullable·결과 값·미변경 field·PK 값/존재를 검사한다. Instance, metadata, nullable pointee와 반환값은 분리한다.
새 모델은 IR default/unsaved 후보에서 준비하고 기존 모델은 typed snapshot을 사용한다. Collection 선택/명시적 clear와
command 입력은 별도 값으로 보존하며 scalar 준비만으로 관계 저장/commit을 완료했다고 기록하지 않는다.

Model clean의 nonnullable NULL이 기존 Admin typed callback에서 zero value로 바뀔 수 있는 경계도 막았다. BoundForm.Input은
해당 값을 준비 오류로 거부하며 후보와 Form의 유효성·cleaned data는 유지한다. Nullable NULL은 그대로 허용한다.
고정 Django observer를 양 DB **15 cases**로 확장했고 기존 13개 원문 결과가 그대로임을 확인했다. 추가 두 사례는 selected
counter와 excluded hidden을 clean에서 None으로 바꾸는 경우다. Native는 is_valid/commit=False에 성공한 후 저장에서
IntegrityError가 된다. Go는 int/string 표현을 위해 준비 단계에서 거부한다. **13개 의미 대조 + 2개 명시적 준비 시점 차이**이며
Python 객체 identity/반환 규약의 차이도 유지한다. Native source pin·해시·uv.lock 보존, 양 DB 동일 관찰과 table/DB/container
정리를 확인했다. 원문·observer·환경/명령·비교 receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-typed-form-reference-0wl7uxg6`에 있다.

독립 Contact consumer의 수동 필드 복사를 새 typed 준비로 바꾸고 실제 양 DB의 단계별 DB 검사·준비·ORM Save·rollback을
검증했다. 넓은 forward-scalar 생성 소비자도 11 kinds의 required/nullable snapshot·대입·clear·Form 초기값/제외 값 보존과
required/nullable FK를 확인한다. Char/Email은 Contact, Boolean·pointer/현재 snapshot·PK present-zero는 별도 core 검사와
함께 검증한다. 다른 모델 타입을 준비하려는 Go 코드는 독립 module의 compile-negative로 거부했다.

최종 비문서 source inventory는 `f038e3b25b5bceef6cff3208b559b8a5d21228d35382251b31a8a3781a7eba51`다.
동일 source에서 아래 checkpoint를 실행했고 필수 누락·test skip·비정형/잘린 JSON은 0이다.

| 범위 | normal PASS / 초 | race PASS / 초 | CGO=0 PASS / 초 |
|---|---:|---:|---:|
| Form/Admin/Identity/Helpdesk/Systemstate/ORM/codegen, 11 test packages / 569 roots | 3,653 / 33.101 | 3,653 / 178.945 | 3,653 / 28.018 |
| Identity 실제 양 DB, 4 roots / 370 required cases | 370 / 53.942 | 370 / 174.252 | 370 / 51.626 |
| 생성 소비자, 3 parents + 3 compile-negative subcases / 필수 child 35+33+5 | 6 / 11.771 | 6 / 41.710 | 6 / 12.795 |

Race는 실제 generated child에 적용한다. 환경은 Darwin arm64 / Go 1.26.5 / offline readonly / TZ=Pacific/Chatham,
각 mode 전용 PostgreSQL 17.10 UTF8/libc/C다. Source 전후 동일, DB **0|0|0**, DB/container 제거를 확인했다.
최초 source `b252e69f…`는 새 collection test fixture의 target/symmetry 누락과 generator의 공개 API 기대에
SetFieldValue가 빠져 core 두 root가 실패했다. 관계 fixture는 IR normalization을 거치게 하고 새 public capability를
정확한 기대에 추가했다. 이 실행의 DB/생성 소비자 성공을 전체 성공으로 세지 않는다. 보완 `401f82ac…`에서 core·생성을
통과한 뒤 NULL 경계를 추가한 최종 source에서 위 세 mode 전체를 별도로 실행했다. 앞선 결과와 합산하지 않는다.

실제 CLI로 Identity→identityfixture→Helpdesk→Article→relationfixture→onetoonefixture→cascadefixture의 생성물을 갱신했다.
Standalone relationproduct의 두 main companion도 현행 생성기로 갱신했다. 모든 기존 migration bytes는 그대로다.
`make generate-check`의 전체 7개 CLI project·standalone fixture·Unicode 생성 검사는 PASS다. CI 도구 **41 tests**, 영향 vet,
전체 **203 packages compile-only / 실행 test 0**도 PASS이며 compile-only를 동작 PASS 수에 포함하지 않는다.

최종 source의 **7개 부정 대조**는 입력 표현의 대입 전 검사, nullable caller 복사, 결과 assignment 일치,
PK presence 보존, Admin의 clean NULL 저장 입력 거부, collection 저장 의도 보존, present-zero snapshot을 각각 제거하고
지정 runtime assertion의 실패를 확인했다. Compile 실패·skip·source 변경을 탐지 성공으로 세지 않았다.
전체 JSON/roster/source/cleanup은 같은 임시 parent의 `godj-typed-form-normal-p26cbhkz`, `godj-typed-form-race-ib63jkmn`,
`godj-typed-form-cgo0-i8w1d52z`에 있다. Parent는 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T`다.
Normal의 `negative-controls`와 `integration-checks`에 overlay·drift/vet/CI 도구/compile 원문을 보존한다.
최초/보완 실행은 `godj-typed-form-normal-1yw77b23`, `godj-typed-form-normal-z2w2wfsp`에 보존한다.
후속 clean/typed 준비 commit `1b2fc49267181f321c0a079844e945cd6cf81584`를 양 branch에 원자적으로 push한 뒤
[Hosted full 36391162296](https://github.com/progresshans/godj/actions/runs/36391162296)을 해당 SHA의 `suite=full`로 시작했다.
[Fast 36391165591](https://github.com/progresshans/godj/actions/runs/36391165591)은 같은 source에서 terminal success와 실제 Fast Go feedback step success를 확인했다.
해당 full은 62 jobs·8 owners와 최종 aggregate·새 capture의 Git source 결합까지 완료했다. 상세 closeout은 이 절 상단의
BoundForm/제품 연결 evidence에 있다. 선행 `211499d0`이나 이후 저장 조정/제품 source의 결과로 혼동하지 않는다.

## GDJ-0101 — 모델 이메일 필드와 Identity 입력

기반 commit `76f7b8e44650c874cb61f43e323f52c15707bb15`에서 EmailField를 별도 IR kind로 추가했다.
Go/SQL 값은 기존 bounded string이며 typed/dynamic·관계 query가 같은 AST를 사용한다. Form 기본 320자와 모델 기본 254자를
구분하고 모델 projection·EmailInput·Admin/API/OpenAPI·독립 Session/Bearer client에 연결했다. 기본 User는 원본 0001/0002를
보존한 0003 migration으로 바꾼다. 기존 주소를 정리하거나 ORM 저장에 이메일 문법 검증을 암묵적으로 추가하지 않는다.
영향·actual process checkpoint는 아래 source별로 완료했다. 이 변경의 Hosted 전체와 전체 프레임워크 완성은 별도다.

- 고정 Django 6.1 commit `fe0a859f537d4238cf49fca39073513206f83122` / DRF 3.18.0의 실제 양 DB에서 각각
  **Form 144개 + serializer 96개 입력 + 생략 default 4개**를 관찰했다. Input/source hash와 raw JSON을 남겼다.
  잘못된 주소·빈 문자열·NULL·서로 다른 case의 저장, exact/iexact/icontains/isnull, Char→Email→Char의 데이터 보존을 확인했다.
  PostgreSQL은 DDL 0개, SQLite는 remake 4개다. Go의 동일 저장 구조 변경은 별도 capability를 갖는 metadata 변경이며
  native SQLite와 SQL text가 같다고 주장하지 않는다.
- Go Form의 144개 입력과 JSON의 **84개 일치 + 12개 전역 NUL transport 거부**를 구분한다. DRF의 NUL validator 순서와
  같다고 합산하지 않는다. JSON 생략 default 4개는 원문을 보존하고 partial input에는 적용하지 않는다.
  OpenAPI는 정리 전 입력/기존 출력에 `format: email`을 강제하지 않으며 정리·검증·생략 기본값을 extension으로 설명한다.
- User email migration/reverse의 credential·session·audit·host reference 보존, 구버전 schema의 runtime admission 거부,
  Form/API의 invalid input 무저장과 기존 malformed response 읽기를 검증한다. 이력 검사는 내장 canonical graph를 사용하며
  모든 필수 항목의 누락·중복과 같은 app의 알 수 없는 이력을 거부한다.

초기 foundation에서는 NUL 전역 경계를 field 오류로 기대한 검사와 capability roster 10개 기대가 실패했다.
새 string-semantics capability까지 11개를 명시하고 NUL 차이를 분리한 뒤 실패한 두 package, 생성 양 DB 소비자,
native observer 검사를 재검증했다. 이 앞선 source의 결과를 아래 통합 source의 전체 성공으로 합치지 않는다.
첫 통합에서는 system-state의 두 단계 고정 이력 검사가 새 migration을 거부하는 실제 연결 누락을 발견해 수정했다.
또 새 helper와 migration의 source binding 누락을 독립 native dependency 감사로 검출해 포함했다.
이후 source `0b9aee0a9cefae737ad738be7f115818a18ef75be9cd1e03854907edfff5144f`에서
normal core **20 packages / 6,855 PASS**, 양 DB Identity **2 packages / 2,120 PASS**, 생성 Email 소비자 **1 root PASS**를 완료했다.
모두 skip 0이고 private DB는 **0|0|0**, container 제거와 source 전후 동일성을 확인했다.
동일 실행의 SDK는 새 GET 뒤 CSRF 응답을 반영하지 않은 consumer 검사에서 실패했다. 제품 CSRF 검사는 유지하고 client를 수정했다.
다음 source `6c2d790ee34325feca04477f064161ca0c7d85bcf5db76f414ad1b84651f176c`와의 차이는
독립 client의 `identity_email.go`·`identity_session.go` 두 파일뿐이다. 이 source에서는 SDK normal **10 PASS**와
source ownership **152 PASS**, 전체 영향 race·CGO=0 각각 **9,138 PASS / skip 0**를 완료했다.
범위는 **26 packages / 1,096 roots / 3,631 필수 항목**이다. Native observer **2 tests**도 통과했다.
최종 source 전후 동일, private PostgreSQL **17.10 UTF8/libc/C**, DB **0|0|0**과 container 제거를 확인했다.
Normal의 앞선 세 그룹은 실제 제품/검사 byte가 그대로인 범위이며, 두 client 수정 후 재실행한 것으로 합산하지 않는다.

추가로 실제 migrate/runserver/restart 소비자 네 파일의 명시적 graph 기대를 6 definitions / 10 operations와
새 Email migration을 포함한 정확한 key 집합으로 갱신했다. 제품 코드는 그대로다. 이 source inventory는
`8cf6e015e146a0144a4ff2f0e8b013f4ca359f8a7226e5ff42edba545f87ebfe`이며 아래 actual process checkpoint를 완료했다.

| 최종 source의 추가 범위 | normal | race | CGO=0 |
|---|---|---|---|
| 실제 global migrate·기존 prefix·fresh-process no-op | 8 PASS | 8 PASS | 8 PASS |
| 실제 SQLite/PostgreSQL runserver·기존 이력 | 9 PASS | 9 PASS | 9 PASS |
| 양 DB의 서로 다른 A/B/C process 재시작 | 2 PASS | 2 PASS | 2 PASS |
| Source ownership·mutation/symlink | 152 PASS | 152 PASS | 152 PASS |

모두 skip 0이며 **5 packages / 21 required roots**, 합계 **513 PASS**다. Process 검사의 race는 harness에 적용되고
migrate/runserver/site child는 기존 일반 build 정책을 따른다. 앞선 generated model/ogen child는 부모 race 모드를 실제로 상속한다.
최종 source 전후 동일, private DB **0|0|0**과 container 제거를 확인했다. 직전 source와의 차이는 위 네 process test 파일뿐이며
앞선 영향 검사를 마지막 source에서 다시 실행했다고 기록하지 않는다.

- 같은 최종 source에서 Identity·Identity fixture·Helpdesk·Article 생성물 drift와 `makemigrations --check`의 변경 없음이 통과했다.
  실제 schema 여섯 개와 고정 ogen 생성물의 byte/파일 집합·module lock 일치는 앞선 SDK의 각 모드에서 확인했다.
- Go overlay **9개**가 지정 runtime assertion을 검출했다: Form/serializer 문법 검사 제거, default 강제 정리,
  다른 저장 속성을 섞은 Char/Email 변환 허용, capability 우회, 출력의 강제 이메일 문법,
  미지의 migration 이력 수용, source owner 제거, 생성 metadata의 Char kind 치환.
  마지막 생성 소비자는 일반 child 진단이 원인을 숨겼으므로 첫 시도를 성공으로 세지 않았다.
  별도 probe가 child JSON의 정확한 test 실패와 고정 assertion만 확인한 뒤 재검증했다. Compile 실패는 부정 대조가 아니다.
- Native Form/DRF validator를 각각 제거하는 두 부정 대조도 관측 차이를 검출했다.
  영향 vet·독립 client vet와 CI 도구 **37 tests**가 통과했다. CI 도구의 최초 호출은 module 검색 경로 누락으로 실패했으며
  해당 경로를 명시한 재실행을 별도로 보존했다. Django-only 환경의 최초 DRF import 실패도 보존한다.

이 검증은 Darwin arm64 / Go 1.26.5 / offline readonly / `TZ=Pacific/Chatham`, PostgreSQL 17.10 UTF8/libc/C와
실제 SQLite에서 수행했다. Python native 관측은 CPython 3.14.3에서 수행했으며 새 observer의 다른 Python 버전은 Hosted 소유다.

Native 원문/receipt와 foundation·integration의 전체 로그는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-email-field-reference-kz1sj84g`에 보존한다.
실행마다 source inventory·필수 root/subtest roster·JSON event stream·실패와 cleanup receipt를 구분한다.
최종 영향 실행은 `integration-repair-1790550447642973000`, process 실행은 `process-1790551790811322000`에 있다.

### EmailField Hosted 실패와 계약 검사 보완

구현 source `2b6129683c68c185188ab477be5c000ee1a4ac5e`의
[Fast 36359488366](https://github.com/progresshans/godj/actions/runs/36359488366)는
`TestSQLiteMigrationCapabilities`의 새 `AlterFieldStringSemantics` 기대 누락으로 실패했다.
[Full 36359485025](https://github.com/progresshans/godj/actions/runs/36359485025)에서도 같은 원인이 네 relation job에,
새 migration 이전의 개수·key 기대가 세 portable integration job에 나타났다. 실패한 여덟 job의 전체 로그를 확인했다.
나머지 하나는 필수 owner 실패·취소를 거부한 최종 집계다. 확인된 실패를 수정한 소스로 대체하기 위해 실행을 취소했다.
최종 **62 jobs = 28 성공 / 26 취소 / 8 실패**, run conclusion `cancelled`이며 전체 성공으로 기록하지 않는다.
원문과 failure summary는 위 process evidence의 `hosted`에 있다.

제품 코드는 유지하고 SQLite의 명시적 capability 기대에 새 항목을 포함했다. PostgreSQL도 일부 boolean 확인을
현재 지원하는 11개 capability의 정확한 계약으로 강화했다. Article projectrunner는 definitions 6개와
두 번째 실행/no-op의 전체 ordered history, site는 Identity/system의 정확한 다섯 key를 기대하도록 보완했다.
첫 보완 실행에서는 backend/guard가 통과했으나 두 번째 실행 이력의 이전 5개 기대가 남아 실패했다.
이 실패와 DB 정리는 `hosted-repair-1790553045708567000`에 보존하고 수정 후 별도로 재검증했다.

최종 비문서 source inventory는 `079ff433c5b21bc0d49f4f26bfc900d52fc3ff2c0422ad46a9aa3e96a17a810a`다.
직전 process source와의 차이는 `db/sqlite/migration_relation_test.go`, `db/postgres/migration_lifecycle_test.go`,
Article의 `cmd/projectrunner/main_test.go`·`cmd/site/main_test.go` 네 테스트 파일뿐이다.
**7 packages / 46 required roots**를 strict JSON event inventory와 대조했다.

| 보완 검증 범위 | normal | race | CGO=0 |
|---|---|---|---|
| 양 DB capability·SQL renderer·physical limit/integrity 계약 | 35 PASS | 35 PASS | 35 PASS |
| Email capability 요구·capability 구조 guard | 2 PASS | 2 PASS | 2 PASS |
| Article cmd 전체 | 32 PASS | 32 PASS | 32 PASS |

각 모드 **69 PASS / skip 0**, 합계 **207 PASS**다. Darwin arm64 / Go 1.26.5 / offline readonly /
`TZ=Pacific/Chatham`, private PostgreSQL **17.10 UTF8/libc/C**, 실제 SQLite에서 수행했다.
소스 전후 동일, DB **0|0|0**과 container 제거를 확인했다. Receipt·roster·전체 로그는
`hosted-repair-1790553210826583000`에 있다. 이전 제품 검사를 이 수정 source에서 다시 실행했다고 합산하지 않는다.
영향 vet와 158개 문서의 로컬 링크·format/diff 검사를 통과하고 commit `b6bff156224a205bd4247b42e72be46254c49828`을 게시했다.
[Fast 36364530446](https://github.com/progresshans/godj/actions/runs/36364530446)와
[Full 36364531786](https://github.com/progresshans/godj/actions/runs/36364531786)은 이 source의 새 실행이다.
Fast는 실제 `Fast Go feedback` 단계까지 성공했다. Full은 **62 jobs = 60 성공 / 2 실패**, conclusion `failure`로 종료했다.
실패한 실행 owner는 `Relation product (macos-15-intel, race)` 한 개이며 다른 실패는 필수 owner를 확인하는 최종 집계다.
원격 JSON과 Fast 전체 로그도 같은 보완 evidence directory에 보존한다.

Full의 새 두 capture는 같은 run/producer/attempt, artifact archive digest와 payload checksum/provenance를 확인했다.
해당 commit의 Git blob에서 직접 재계산한 source binding도 일치한다:
systemstate **620 files / 6,602,368 bytes / `a48f1df484273b364d5e2ab9d615c2a1d336f8b515752d4cbf754c1957ca1bc5`**,
operator **696 files / 6,447,359 bytes / `ce498f1cd0621ce0f0e50e591cbea447b08cc2ee02ef59acd520190528a35d6c`**.
Archive·원문·source inventory와 receipt는 같은 evidence root의 `hosted-full-36364531786-1790558077664132000`에 보존한다.
Capture 일치만으로 실패한 필수 owner의 성공을 대신하지 않는다.

실패 job `108748227586`의 전체 로그에서 generated consumer package가 **35분 합산 시간 제한**을 초과했다.
이때 `TestGeneratedNestedEagerConsumer`는 2분 11초 실행 중이었다. 앞선 25개 parent root의 완료가 보이지만,
이는 전체 필수 inventory의 완료가 아니다. 로그에 assertion 실패나 race 진단은 없었고 진행 중인 child의 성공도
확정하지 않는다. Exact macOS는 **16분 12초**, native **375 tests / 769.878초**와 전체 locked-oracle replay를 완료했다.
Full에서 별도 relation/project matrix가 소유하는 focused Go 중복 단계는 exact job에서 선택되지 않았다.

Source `bb9eae3c7e50df29cb19e15a8407d9e703026d0c`는 b6 이후 비문서 변경이 CI 예산 수정 하나뿐이다.
Intel race만 package **70분**, job **90분**으로 조정하고 다른 runner/mode는 유지했다.
필수 package/root 목록·race 전파·no-skip·capture 대조·집계 조건은 그대로다.
YAML 구조를 이전 commit과 비교해 변경 위치를 확인했고 세 mode의 shell syntax, CI 도구 **10 tests**와 diff 검사가 통과했다.
실패 원문·최종 run JSON·검증 receipt는 보완 evidence의 `hosted-b6-failure`에 있다.
[새 full 36369062484](https://github.com/progresshans/godj/actions/runs/36369062484)과
[Fast 36369057930](https://github.com/progresshans/godj/actions/runs/36369057930)은 이 source의 별도 실행이다. Fast는 실제 Go 단계까지 성공했고 full은 진행 중이다.
새 Form/Blank 구현은 작업본에 보존했고 이 실행에는 포함하지 않았다.

이 새 실행의 두 capture도 같은 run/producer/attempt·archive digest·payload checksum/provenance를 확인했다.
수정 commit의 실제 Git blob 목록에서 재계산한 source binding과 일치한다:
systemstate **620 files / 6,602,601 bytes / `84e242a4872dd9ab96a1ecb9f668070bde9da7d6b9d644b05dc00c7aba41e752`**,
operator **696 files / 6,447,592 bytes / `590017b6b975363cd88ebb4b86be7299c3630461e4f263e319c4afefdd1fbac3`**.
두 source 목록에 CI workflow도 포함되므로 예산 수정이 binding에도 반영됐다. 원문과 receipt는
`hosted-full-36369062484-1790563600816040000`에 있다. 같은 source의 Fast 전체 로그와 실제 Go step 성공도 확인했다.
최근 snapshot은 **49 성공 / 6 실행 / 6 대기**, exact macOS 성공, final 집계 미생성이다.
Intel race와 나머지 owner·최종 집계가 끝나기 전에는 전체 PASS로 기록하지 않는다.


### Intel race 인증 확인 보완과 새 Hosted 실행

Source `bb9eae3c7e50df29cb19e15a8407d9e703026d0c`의
[full 36369062484](https://github.com/progresshans/godj/actions/runs/36369062484)은 **62 jobs 중 60 성공 / 2 실패**로 종료했다.
이전 시간 초과 대상 Intel relation race `108761357035`는 **02:28:51~03:21:12 UTC**에 성공했고 모든 relation owner가 통과했다.
실패는 Intel race command products `108761356952`와 최종 aggregate다. 해당 job의 targeted migrate는 통과했으나
SQLite operator lost-response의 known-created backend-close/response failure, malformed response, over-limit response
세 사례가 새 runtime의 credential 확인에서 실패했다. 원래 진단에는 실패 이유가 없어 실제 deadline 발생을 단정하지 않는다.

인증 확인 helper의 10초 context는 runtime open과 기본 PBKDF2 계산을 함께 포함했다. 검증 예산을 `t.Context()`에
결합한 1분으로 분리하고 failed/deadline/canceled/invalid-credentials 분류·경과 시간과 principal/active/staff/superuser
일치 여부만 출력한다. Password/hash/username/raw error는 노출하지 않는다. 제품의 기본 hash 정책·반복 횟수,
principal/flag 검사와 응답 유실 뒤 자동 재시도 금지 조건은 그대로다.

Clean primary의 `4d451c39` 위에서 이 helper 한 파일만 바꾼 source로 SQLite operator 제품 전체를 실행했다.
Normal **11 PASS / skip 0 / 117.717초**, race **11 PASS / skip 0 / 141.210초**이며 source는 그대로다.
최초 집계는 잘못 고른 required roster가 비어 있었으므로 완료 증거로 사용하지 않았다. 이후 source의 실제 11개 root/subtest를
독립 명시해 기존 전체 JSON에 누락·추가·skip·실패가 없음을 다시 감사했으며 실행을 다시 했다고 세지 않는다.
수정은 `ea2867f4323fb34e713fc1d10d8a62c05c785d37`로 양 작업 branch에 원자적으로 push했고
[Fast 36372684175](https://github.com/progresshans/godj/actions/runs/36372684175)의 실제 Go 검사도 성공했다.
이 수정 source의 [새 full 36374533286](https://github.com/progresshans/godj/actions/runs/36374533286)은 아래 감사까지 완료했다.
이후 Blank/DB 후처리 source의 검증으로 전이하지 않는다.

bb9 capture의 동일 run/attempt/producer, archive/payload checksum과 실제 Git blob source 결합은 별도로 확인했다.
System-state는 620 files / 6,602,601 bytes / `84e242a4872dd9ab96a1ecb9f668070bde9da7d6b9d644b05dc00c7aba41e752`,
operator는 696 files / 6,447,592 bytes / `590017b6b975363cd88ebb4b86be7299c3630461e4f263e319c4afefdd1fbac3`다.
Capture 성공은 실패한 전체 run의 성공이 아니다. Exact macOS job도 성공했지만 앞선 source의 native test count를 전이하지 않는다.
종료 run JSON·capture audit·실패 원문과 repair receipt는 GDJ-0101 evidence 아래
`hosted-repair-1790553210826583000/hosted-full-36369062484-1790563600816040000`에 보존한다.

### EmailField·인증 lifecycle의 Hosted 전체 완료

Source `ea2867f4323fb34e713fc1d10d8a62c05c785d37`의
[full 36374533286](https://github.com/progresshans/godj/actions/runs/36374533286), attempt **1**이
**62 jobs 모두 completed/success**로 종료했다. 이전 실패 owner인 Intel race command products와 Intel relation의
normal/race도 성공했다. 기본 password 해싱 정책·필수 검사 조건은 그대로다. 원래 실패 로그에 없던 원인을 사후 단정하지 않는다.

최종 aggregate `108791644519`의 실제 로그에서 `scope: full`, `full_platform_verified: true`와
**8개 실행 owner**의 정확한 목록을 확인했다: command-product-matrix, conformance-validation,
exact-darwin-validation, portable-go-matrix, postgresql-product, product-project-check-matrix,
python-compatibility-matrix, relation-product-matrix. 각 job의 성공, aggregate 단계의 실행과 source에 고정된 scope 정의를 대조했다.

같은 run/attempt의 새 system-state·operator capture를 성공 producer에 결합해 받았고 artifact archive SHA,
payload checksum·provenance·정확한 3개 파일 집합을 검사했다. 해당 source의 Git blob에서 behavioral inventory를
독립 재구성해 capture의 source binding과 일치함을 확인했다.

| Capture | Producer job / artifact | 파일 / bytes | Source SHA-256 |
|---|---|---:|---|
| system-state | 108777568350 / 10950812106 | 620 / 6,602,601 | `84e242a4872dd9ab96a1ecb9f668070bde9da7d6b9d644b05dc00c7aba41e752` |
| operator | 108777568304 / 10949823725 | 696 / 6,448,276 | `9ee194d758c208b2c002419da72055dc3311bec16f85b336f6cb1966b3b164da` |

Run/jobs/artifacts JSON·새 archive·payload/provenance·source inventory·최종 aggregate 원문과 receipt는
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-email-field-reference-kz1sj84g/hosted-full-36374533286-1790571899785049000`에 있다.
이 결과는 GDJ-0100의 선택한 lifecycle 통합과 GDJ-0101을 포함한 해당 source의 전체 검증이다.
뒤의 Blank 및 모델 DB 검증 구현, custom user model·다른 인증 provider·운영 mail provider 또는 전체 기능 카탈로그 완료가 아니다.

후속 DB 후처리 첫 묶음 `b8cc51b56a5c03a1815181ced5406e1154cdc2b9`의
[Fast 36379234202](https://github.com/progresshans/godj/actions/runs/36379234202)는 실제 Go 검사까지 성공했다.
이후 catalog Form 작업본과 Hosted full 성공으로 합치지 않는다.

### 선행 재사용 Form Hosted 전체의 종료

Source `563aac29d611f08e0b943cc24bfb334881e72ba1`의 [Hosted full 36353329053](https://github.com/progresshans/godj/actions/runs/36353329053)은
**62 jobs 중 60 성공 / exact darwin 1 취소 / 최종 집계 1 실패**, run conclusion `cancelled`로 종료했다.
8개 실행 owner 중 7개는 성공했고 exact darwin은 15분 job 제한을 초과했다. Native Python **373 tests / 776.172초** 뒤
locked-oracle replay 도중 취소됐으며 gate는 전체 성공을 거부했다. 같은 필수 명령과 runner를 유지하고 다음 source의 예산을 30분으로 늘렸다.
이 설정 변경이 종료된 실행을 성공으로 바꾸지는 않는다.

두 capture의 같은 run/producer/attempt·artifact digest·payload checksum/provenance를 검증하고, 해당 Git object에서 재계산한
source binding과 일치함을 확인했다: systemstate **617 files / 6,593,902 bytes / `f12fd181c20eb19f78e4f1828fcdaa903764033969397b727db873211fc9dd50`**,
operator **693 files / 6,438,825 bytes / `8afc1232e0b0c438369f308e6edeec27315f2ba4e6143ed222b0b4c13c2b6a5a`**.
원문은 위 evidence root의 `hosted-full-36353329053-1790550237890603000`에 있다. Capture 일치는 필수 owner의 취소를 대체하지 않는다.

## GDJ-0100 — 재사용 사용자 생성 Form과 한 번의 준비/저장

기반 commit `a80fe2f7a9f64c0f3995638ee78fa7f6b8b70850` 이후 기본 User IR의 일반/Admin 생성 Form을
공통 `forms/model.Definition`으로 연결했다. `Bind`는 읽기 검증만 수행하고 `Prepare`는 읽기 종료 뒤 credential을 준비한다.
`Commit`은 원래 Manager·actor와 현재 인가·중복·관계·policy를 다시 확인하여 user/관계/audit를 원자 저장한다.
복사된 준비 객체도 한 번의 저장 시도만 공유하며 실패·unknown 뒤 재사용과 삭제된 사용자의 과거 credential 재생성을 거부한다.
기존 즉시 생성과 Admin도 같은 경로를 사용한다. Custom user model·모든 ModelForm 저장·익명 signup의 완료 선언은 아니다.

최종 비문서 source inventory SHA-256은 `9489a1278ab274a37cc0447fb7e0f849015813b0a579eacd80f0ad339650ba24`이며 시작/종료·기록 시점이 같다.
범위는 **8 packages / 130 roots / 840 필수 항목**이다. Admin·Identity·Identity Admin·model Form·build 진단·Article 전체와
SQLite/PostgreSQL의 생성 Form/validation·관리 HTTP/API·사용자 관리·password policy·unusable·이름 중복 root를 선택했다.
두 DB의 새 재사용 Form 필수 roster는 각각 **90항목**이며 기존 생성 검증 roster도 그대로 실행했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,595 PASS / skip 0 | 49.204초 |
| race | 1,595 PASS / skip 0 | 314.899초 |
| CGO=0 | 1,595 PASS / skip 0 | 56.397초 |

합계 **4,785 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5 / offline readonly / `TZ=Pacific/Chatham`,
private PostgreSQL **17.10 UTF8/libc/C**와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다. 마지막 DB **0|0|0**과 container 제거를 확인했다.

- 고정 Django 6.1 / CPython 3.14.3 / Unicode 16의 양 DB에서 각각 **60개 입력 + 12개 저장 lifecycle 관측**이 일치했다.
  일반·Admin enabled·Admin disabled 각각의 즉시 저장, 지연 준비/저장, 준비 후 포기, invalid 저장 거부를 실제 hasher 호출과 DB로 관찰했다.
  Input corpus는 유지하고 auth forms·model forms·password validation·hashers·base user·model base의 upstream hash를 기록했다.
  Go는 저장 전 Python의 mutable instance 속성을 비교하지 않으며 opaque 후보와 최종 저장 결과·hash/write 수명을 대조한다.
- 일반 Form을 현재 add/change 권한이 있는 nonstaff actor로 실행했다. Bind에는 hash/session/audit 변경이 없고 Prepare도 DB에 쓰지 않는다.
  Usable 준비는 hash 1회, unusable은 0회, Commit은 추가 hash 0회다. 준비 포기·invalid 입력에는 사용자 저장이 없으며 최종 기본값도 대조했다.
- 임의 typed 값의 미선택 이름/타입/confirmation/문자 문법 우회, nil Form의 private 필드 노출, 정의 slice alias를 거부했다.
  원래 Manager/actor 결합, 현재 권한 회수·중복 삽입·관계 삭제, audit rollback·unknown 결과 미게시를 양 DB에서 검증했다.
  다른 actor는 현재 superuser로 만들어 권한 부족이 아닌 owner 결합 자체의 거부를 확인했다.
  후보 복사본 4개의 경쟁은 성공 1개/소비 충돌 3개이며, 삭제 후 재사용·확정 실패/unknown 뒤 재호출은 새 write 없이 거부된다.
  다른 owner의 호출과 저장 진입 전 context 취소는 정당한 owner의 저장 기회를 소모하지 않는다.
- **7개 Go source overlay**가 각각 지정 runtime assertion으로 실패했다: 시도 소비 제거, nil 정의를 전체 필드로 변경,
  Manager/actor 결합 제거, 준비 중 조기 저장, 최종 fence 생략, dependency 원인 진단 제거. Compile 실패는 부정 대조로 세지 않았다.
  Native observer의 validation 4개·저장/hash 2개 변형도 예상 차이를 검출했다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **3 tests / skip 0**이 통과했다.
  앞선 세 compatibility 실행과 최종 실행 사이 Python observer/test/fixture byte는 동일하다.
  영향 vet, 두 attestation의 native dependency 소유권 검사와 CI event/roster/scope **26 tests**도 통과했다.
  Model IR·생성 ABI·OpenAPI를 바꾸지 않아 generated drift 전체는 반복하지 않았다.

최종 준비 객체 소비 제약을 추가하기 전 source `524c4a32fb22ae361830e25919bbeaf9555e0992e9047a3c37f61b2311a7102a`에서는
**9 packages / 131 roots / 837 필수 항목**, 세 모드 각각 **1,600 PASS / skip 0**을 완료했다. 이 범위에는 아래 수정한
실제 외부 CLI SQLite lifecycle/응답 유실 소유권의 각 모드 **11 PASS**가 포함된다. 현재 source와의 차이는
`identity/user_creation.go`, 공통 identity Form test 및 양 DB 필수 roster의 **4파일**뿐이다.
CLI fixture·build 진단·Python 파일은 동일하지만 이 앞선 전체 inventory를 최종 source에서 재실행한 결과로 합산하지 않는다.

로컬 원문은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-user-creation-reference-rxk1a9mx` 아래
`reusable-form-consumption-checkpoint-1790545045310777000`의 receipt/source 전후·필수 roster·JSON stream·controls/quality,
그리고 `reusable-form-final-checkpoint-1790544116497452000`의 CLI/Python/CI 도구 로그에 있다.
최초 재사용 Form checkpoint와 dependency guard가 실패한 `reusable-form-final-checkpoint-1790543879822175000`도 보존한다.

### 선행 Hosted 실패와 외부 CLI 준비 수정

선행 `a80fe2f7`의 [Hosted Fast 36348779762](https://github.com/progresshans/godj/actions/runs/36348779762)는 실제 Fast Go feedback까지 성공했다.
Reset 수정 source `1ae07db3644290df4cc2c319d6f7dc44f8f7fd57`의
[Hosted full 36346992368](https://github.com/progresshans/godj/actions/runs/36346992368)은 **62 jobs 중 60 성공/2 실패**로 종료했다.
`Command products (macos-26, normal)`의 실제 durable_lifecycle은 외부 모듈 준비 중 exit 1이고 최종 full 집계도 실패했다.
진단에는 `github.com/jackc/pgx/v5@v5.10.0 requires`까지만 남았다. 들여쓴 dependency 원인이 기존 Summary에서 제외되어
당시 원격 오류를 확정할 수 없으므로 네트워크 장애나 특정 timeout으로 단정하지 않는다.

Fresh private module/cache와 실패하는 loopback checksum service에서 실제 GoDj 의존성을 사용해 재현했다.
Root go.sum이 없으면 checksum 요청 **4회 후 실패**, 복사하면 **요청 0회로 성공**, 결과 **76개 record**는 모두 root checksum에 포함됐다.
외부 fixture는 root의 검증된 go.sum을 복사하고 준비도 offline으로 수행하며, 결과가 root checksum 밖을 선택하면 거부한다.
Third-party module을 로컬 경로로 치환하던 중복 경로는 제거했다. 최초 엄격 검사에서 이 치환이 추가 `kr/text v0.2.0/go.mod`를
선택하는 것을 확인했으며 native module/cache resolution으로 수정했다. Gate와 go.mod/go.sum·dependency version은 바꾸지 않았다.
Summary는 인식한 module chain의 bounded continuation만 보존하며 URL·secret·임의 child output은 계속 숨긴다.

재현 원문은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-operator-checksum-repro-s58bwx1c`에 있다.
Hosted final jobs·실패 로그와 capture 원문은 `godj-many-to-many-reference-4sl0bvdp/hosted-full-36346992368-1790541124878313000`에 있다.
같은 attempt 1의 systemstate `10941347662`/operator `10940732028`은 archive/provenance/producer와
source Git blob inventory **612/688개**를 확인했다. 두 capture 검증은 실패한 전체 run의 성공 근거가 아니다.
구현을 `563aac29d611f08e0b943cc24bfb334881e72ba1`로 게시했고 같은 source의
[Hosted Fast 36353272560](https://github.com/progresshans/godj/actions/runs/36353272560)와
새 [Hosted full 36353329053](https://github.com/progresshans/godj/actions/runs/36353329053)을 시작했다.
Fast는 실제 Fast Go feedback까지 성공했다. 전체는 진행 중이며 필수 owner·최종 집계·새 capture의 검증을 기다린다. 이전 source의 결과를 전이하지 않는다.
후속 상태 문서 수정은 비문서 inventory를 바꾸지 않으며 이 실행의 source는 위 구현 commit으로 고정한다.

## GDJ-0100 — 생성 Form의 복합 오류와 현재 권한 검증

기반 commit `1ae07db3644290df4cc2c319d6f7dc44f8f7fd57` 이후 Admin의 read-only `ValidateCreate`와
`Manager.CheckUserCreation`을 연결했다. 고정 Django의 username 중복 → model grammar → password policy 결과를
사용 가능한 field마다 검사한다. 일부 입력 오류가 있어도 다른 진단을 수집하며 최종 write의 현재 권한·중복·policy fence는 유지한다.
일반 재사용 UserCreationForm/준비·저장 API와 custom user model의 완료 선언은 아니다.

로컬 checkpoint의 비문서 source inventory SHA-256은 `e31c7f98df67250f9b1eca4d5f848e2e1964e835c4cb4e3b02b0774cf356e6fd`이며 시작/종료가 동일하다.
검증 범위는 **6 packages / 98 roots / 304 필수 항목**이다. Admin·Identity·Identity Admin·Article composition 전체와
SQLite/PostgreSQL의 기존 관리 Form 및 새 생성 검증 root를 선택했다. 두 DB의 새 필수 실행 roster는 각각 **53항목**이다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 949 PASS / skip 0 | 19.292초 |
| race | 949 PASS / skip 0 | 156.969초 |
| CGO=0 | 949 PASS / skip 0 | 23.713초 |

합계 **2,847 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly·`TZ=Pacific/Chatham`,
private PostgreSQL **17.10 UTF8/libc/C**와 `GODJ_REQUIRE_POSTGRES=1`에서 수행했다. DB 정리 **0|0|0**과 container 제거를 확인했다.

- 독립 Django 6.1 / CPython 3.14.3 / Unicode 16 observer가 양 DB에서 각각 **60개 입력 관측**을 만들었으며 결과가 일치했다.
  Standard·Admin enabled·Admin disabled Form을 각각 20개 입력으로 검사했고 input/upstream source hash를 고정했다.
  Go의 실제 Admin HTTP는 enabled/disabled의 오류 코드·policy candidate username·성공 저장을 대조한다.
  Native instance의 None/빈 username은 Go Profile의 빈 문자열에 대응하며 DB query 횟수/내부 객체 구조는 동일성 대상이 아니다.
- 중복+약한 password, 문법 오류+similarity, password1 누락/NUL+password2 policy, mismatch·password2 NUL의 policy 생략,
  사용 불가 선택·정규화·공백 password를 확인했다. Invalid Form은 ID 발급/hash/user/session/audit 변경이 없고 password를 재표시하지 않는다.
  정상 usable 생성은 hash 1회, disabled 생성은 0회다. 정상 Form 이후 Manager preflight와 마지막 write에서도 정책을 재검사한다.
- Native snapshot의 누락·nil·중복 callback·swallowed read 오류·종료 실패·취소는 입력 진단으로 낮추지 않았다.
  과거 actor 권한은 현재 저장 인가를 통과하지 못하며 policy도 호출하지 않는다. 별도 DB 연결이 hash 중 권한을 회수하거나
  대소문자 중복을 삽입하면 마지막 write가 거부되고 새 후보는 저장되지 않는다.
- 공통 Admin callback은 CSRF와 모든 add 권한 뒤에 실행한다. 기존 field 오류를 보존하고 알 수 없는 진단 field·wrapped storage·취소를
  실행 오류로 구분한다. 영향 Article 소비자와 기존 Identity CRUD/실패/unknown 경로도 함께 통과했다.
- Source overlay **3개**(invalid Form 검사 생략, 중복 후보를 policy에 전달, 최종 case-insensitive 재검사 제거)를 지정 assertion으로 검출했다.
  Compile 오류는 성공한 부정 대조로 세지 않았다. Native observer의 **4개 단계 제거 대조**도 결과 차이를 검출했다.
- Python **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7**에서 각 **2 tests / skip 0**을 수행했다.
  영향 vet·gofmt, 두 attestation의 native dependency 소유권 검사, CI event/roster/scope 도구 **26 tests**가 통과했다.
  IR·생성 ABI·OpenAPI가 변경되지 않아 generated drift 전체를 반복하지 않았다.

첫 checkpoint는 새 테스트가 로그인된 브라우저로 다른 계정의 login GET을 호출하여 정상 302를 실패로 판단했다.
독립 cookie jar로 수정했으며 이전 실패 로그를 보존했다. CI 도구의 최초 직접 unittest 명령은 import 경로가 빠져 실행되지 않았고,
해당 도구 디렉터리에서 다시 실행해 26 tests를 완료했다. 두 오류를 제품 성공이나 부정 대조로 세지 않았다.
원문은 `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-user-creation-reference-rxk1a9mx`의 `checkpoint`, `checkpoint-2`, `controls`, `python-compat`, `quality`에 있다.

이 생성 Form source의 Hosted 전체는 아직 실행하지 않았다. Reset 소비자·실행 기반 수정 source `1ae07db3`의
[Hosted Fast 36346945448](https://github.com/progresshans/godj/actions/runs/36346945448)는 실제 Fast Go feedback까지 성공했다.
[Hosted full 36346992368](https://github.com/progresshans/godj/actions/runs/36346992368)은 외부 CLI 준비에서 실패했다. 최종 결과와 후속 수정은 위 기록을 따른다.
동일 attempt 1의 systemstate capture `10941347662`와 operator capture `10940732028`은 archive digest·provenance·producer 성공을
확인했고 해당 commit의 Git blob source inventory 612/688개와 일치했다. Capture 검증만으로 전체 완료를 선언하지 않는다.

## GDJ-0100 — reset 소비자 Hosted 통합에서 발견한 실행 기반 수정

`e3ec9a2a75f6983d8e71ea8842aa8f924609575a`의 [Hosted Fast 36345090890](https://github.com/progresshans/godj/actions/runs/36345090890)는
실제 Fast Go feedback까지 성공했다. 같은 source의 [Hosted full 36345110007](https://github.com/progresshans/godj/actions/runs/36345110007)에서는
아래 네 결함을 확인했고 전체 성공으로 기록하지 않는다. 실패 로그를 보존하고 남아 있던 실행을 중단했으며 최종 상태는 `cancelled`다.

1. `go mod download all`이 체크섬 7개를 추가해 여러 owner의 clean-worktree 검사가 실패했다.
   별도 manifest 복사본에서 같은 7개를 재현하고 공식 Go module proxy/checksum DB로 검증해 `go.sum`에 반영했다.
   `go.mod`와 선택한 version은 바꾸지 않았다. 반복 offline download가 두 lock 파일을 바꾸지 않고 `go mod verify`도 통과했다.
2. 실제 CLI용 외부 fixture가 x/sys만 준비하던 가정 때문에 새 `mail`의 IDNA 의존성을 readonly build에서 찾지 못했다.
   모든 공통 fixture의 실행 전 현재 프로젝트를 `go mod tidy`로 준비하고 makemigrations도 수동 x/sys-only checksum 덮어쓰기를 없앴다.
   준비와 제품 실행을 구분하며 제품의 private cache·offline·readonly·무변경 검사는 유지한다.
3. SYS-021의 source API probe는 기본 go/types importer가 GOPATH만 조회해 x/net/idna를 찾지 못했다.
   native `go list -deps -export`의 bounded inventory로 dependency export를 로드하고 GoDj 자체는 여전히 현재 source를 type-check한다.
   Context/timeout·취소·진단/잘림 거부를 추가했으며 callable alias·wrapper·nested secret 검사는 유지한다.
4. Reset HTTP와 문자열 routing의 새 native observer가 CPython 3.14.3만 허용해 다른 세 compatibility runtime에서 실행 전에 실패했다.
   Django 6.1과 source hash·행동 비교를 유지하면서 실제 interpreter metadata를 기록한다. 고정 fixture의 Python은 여전히 3.14.3이며
   compatibility 결과를 고정 reference로 다시 쓰지 않는다. 두 observer와 해당 test만 수정했고 fixture는 그대로다.

Go 보완 checkpoint source map `c9fd11e166c2a83a71124318524a41931a63ff60dd2c183104ba41a4c85dad76`의
시작/종료 동일성을 확인했다. **3 packages / 12 roots / 31 필수 항목**이며 일반 CLI의 25개, PostgreSQL 실제 generated migrate/restart 1개,
source probe 4개, 실제 godjcheck 30-contract 소비자의 1개 검사를 각 모드에서 실행했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 31 PASS / skip 0 | 104.991초 |
| race | 31 PASS / skip 0 | 128.463초 |
| CGO=0 | 31 PASS / skip 0 | 99.868초 |

합계 **93 PASS / skip 0**이며 Darwin arm64 / Go 1.26.5 / offline readonly / `TZ=Pacific/Chatham`을 사용했다.
PostgreSQL 17.10 UTF8/libc/C의 필수 실제 DB 실행과 마지막 **0|0|0** 상태를 확인했다.
Linux deleted-CWD의 3개 경로는 이 Darwin scope에서 제외하고 Hosted Linux가 소유한다. Restart helper 2개는
독립 root 성공을 요구하지 않고 실제 SQLite/PostgreSQL 부모의 별도 process 실행·종료·durable 상태 확인으로 검증했다.
초기 scope가 이 helper/OS 전용 root와 미설정 PostgreSQL을 섞어 **6 skip**을 낸 inventory 실패는 `checkpoint/`에 그대로 보존했다.
수정된 명시적 scope는 `checkpoint-with-postgres/`의 전체 required run/pass·package 완료·skip 0을 확인했다.

마지막 DB 상태 검사는 통과했으나 임시 harness의 container 이름 변수를 실행 label로 덮어쓴 탓에 최초 종료 명령이 실패했다.
원래 false receipt를 보존하고, 해당 private container의 정확한 ID로 종료한 뒤 비동기 제거 완료를 재조회했다.
`cleanup-recovery.json`은 원래 모든 test/inventory 성공·source 불변과 실제 container 부재를 결합한 최종 정리 근거다.
제품/test code의 실패를 이 정리 복구로 바꾸지 않았다.

기본 importer로 되돌리기와 CLI dependency 준비 생략의 **2개 Go overlay**는 각각 지정된 실제 source-probe/CLI assertion으로 실패했다.
새 nil/canceled context 검사와 영향 vet, 두 attestation의 native dependency 대조도 통과했다.

Python 변경은 두 observer/test와 기존 세 fixture를 별도 경로에 복사해 검증했다. Package initializer는 원본도 docstring뿐이며,
네 수정 파일의 검증 SHA를 그대로 현재 작업 사본에 반영했다. Django **6.1**, DRF **3.18.0**, asgiref **3.12.1**, sqlparse **0.5.5**를
격리 환경에 고정하고 설치된 CPython **3.12.13 / 3.13.15 / 3.14.3 / 3.14.7** 각각 **4 tests / skip 0**을 통과했다.
원래 3.13.15의 version assertion 실패도 별도로 재현·보존했다. 각 실행은 hash-seed 재생, 원본 source hash와 동작 비교 및
CSRF·proof cleanup·auto-login·string converter의 native mutation 검출을 유지했다. 이 영향 16 tests를 전체 Python suite로 표현하지 않는다.

최종 source map `6151fbbecc0b5f0d9dc2452f3f6155b2ea837a83be41c24dffd558900fb0af69` (**2,568 파일**)의
Go checkpoint 이후 delta는 위 네 Python 파일뿐이다. Reference fixture와 Go source는 해당 검증 byte를 그대로 유지했다.
원본/receipt·최종 inventory·control·Python source/run은 `godj-many-to-many-reference-4sl0bvdp/reset-full-repair-1790538236761337000`에 있다.
선행 Hosted run/jobs/실패 로그는 reset-consumer checkpoint의 `hosted/`에 있다. 수정 source의 새 Hosted 전체와 새 capture는 다음 검증이다.

## GDJ-0100 — 공개 password reset Form·JSON과 독립 client

2026-09-28, `95e3c0f0` 이후 실제 email request·hidden-token entry·confirmation Form과 JSON/OpenAPI를 연결했다.
명시적 anonymous CSRF adapter와 application proof의 session-cookie 계약을 구분하며 Article이 같은 resetter/policy로
persistence·mailer·Form/API를 구성한다. 새 login은 만들지 않고 마지막 proof/password/session/audit를 원자적으로 정리한다.
Model IR·migration·생성 model ABI·module/tool lock은 바꾸지 않았다. Account OpenAPI와 해당 ogen 생성물만 갱신했다.

통합 checkpoint source map `6cbce1b957666014843b2908f4d0f2508797ce4478c0fc906ee2c1345d3fc6d2`
(non-Markdown **2,568 파일**)의 시작/종료 동일성을 확인했다. **12 packages / 213 roots / 842 필수 항목**이며
Web·API/두 auth adapter·OpenAPI·Account/Identity·Article composition **936 PASS**, 양 DB mail/proof/account/새 reset consumer
**488 PASS**, 여섯 profile의 기존 generated drift·HTTP와 확장된 account client **10 PASS**를 각 모드에서 실행했다.
필수 run/pass·package 완료와 모든 child의 skip 부재를 event inventory로 확인했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,434 PASS / skip 0 | 26.604초 |
| race | 1,434 PASS / skip 0 | 173.217초 |
| CGO=0 | 1,434 PASS / skip 0 | 30.652초 |

합계 **4,302 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly와 `TZ=Pacific/Chatham`,
private PostgreSQL **17.10 UTF8/libc/C**, `GODJ_REQUIRE_POSTGRES=1`을 사용했다. DB 정리 **0|0|0**과 container 제거를 확인했다.
두 DB 실행 roster에 각각 **120개 필수 항목**을 추가했으며 실제 두 DB의 해당 run/pass를 확인했다.

- Form/JSON 각각 session 없음·anonymous·본인·다른 계정에서 실제 Memory sender 메일의 URL을 사용했다.
  다른 브라우저의 hidden link 거부, entry ID 회전·token-free redirect, 별도 DB 연결/runtime 재접속 뒤 proof 재개,
  policy 거부의 무변경, 정확한 password/revision/last_login/session/payload/lifetime/audit·proof 정리와 replay 거부를 검사했다.
  별도 서버 process restart 증거와는 구분한다. 기존 고정 Django HTTP fixture의 required/mismatch/policy 순서도 대조했다.
- 유효 email의 known/unknown/inactive/unusable, 전달 거절/unknown/panic/cancel, snapshot 오류와 reporter panic에서
  동일 공개 status/body/location을 확인했다. 내부 오류 보고·전송 회수와 password/session/audit 무변경을 별도로 검사했다.
  처리 시간 동일성이나 운영 mail provider의 실제 도착은 검증하지 않았다.
- CSRF/origin·malformed/padded/wrong-target/missing proof·query/빈 query·duplicate/null/unknown member·body/input 한도·
  media·method·없는 경로·Accept 거부를 실제 HTTP로 검사했다. Raw-token POST는 CSRF 뒤 proof만 수립한다.
  실패/404/405/406에도 no-store/no-referrer를 확인했고 Web 최종 response budget의 500 privacy도 검사했다.
- Password write·proof/session 회전·이전 ID 삭제·audit·revocation 오류, 취소와 unknown rollback/commit을 양 DB에 주입했다.
  확정 실패는 전체 rollback, unknown commit fixture만 실제 저장을 유지하며 모두 오류 보고·무쿠키·무재시도·hash 1회를 확인했다.
- Anonymous CSRF adapter는 login store/authenticator/authorizer를 접근하지 않는 guard를 사용한다. Origin/CSRF가
  handler보다 앞서며 schema는 익명 bootstrap과 proof cookie를 구분한다. Article은 실제 migrated identity와 global password
  policy를 사용해 요청·메일·entry·JSON 완료·최종 저장을 검증했다.
- 독립 Go module의 account generated client는 새 익명 jar, 실제 메일 링크, 제품 entry/Form, proof/policy/completion·replay와
  자동 login 없음까지 실행했다. Parent fixture의 mailbox 읽기만 별도 capability 경로를 사용하며 제품 API/schema에는 없다.
  부모는 별도 native runtime에서 password/revision 4·last_login 유지·대상 session 폐기/외부 session 보존·audit 3건을 확인했다.
  Synthetic typed 503/no-retry는 실제 DB unknown 주입과 별도 증거다. 다른 다섯 schema/client와 tool/dependency lock은 불변이다.

Source overlay **6개**는 CSRF 생략, 메일 오류의 공개 응답 차이, raw-token redirect, proof-cookie security 누락,
unknown을 성공 status로 게시, 마지막 500의 Referrer-Policy 누락을 지정 assertion으로 검출했다. Compile 실패는 control로 세지 않았다.
영향 vet·gofmt, CI event/roster/scope 도구 **26 tests**, 두 attestation의 실제 native Go dependency 대조도 통과했다.

첫 checkpoint의 core **936 PASS**와 양 DB **488 PASS** 뒤 independent client는 실패했다. 추가 진단에서
중간 safe generated GET이 CSRF cookie를 보내지 않아 새 pair를 발급받았는데 완료 검사가 이전 bootstrap 값을 비교한 것을 확인했다.
완료 직전의 proof 응답을 기준으로 수정했고 실제 completion 전후의 cookie 보존 검사는 유지했다. Set-Cookie/HttpOnly 검사를 추가했다.
실패 실행은 `reset-consumer-checkpoint-1790536911174940000`에 보존했으며 race/CGO=0 미실행을 성공으로 세지 않았다.

최종 raw/receipt·source inventory·6개 control·quality는 `godj-many-to-many-reference-4sl0bvdp/reset-consumer-checkpoint-1790537339279142000`에 있다.
이 consumer source의 Hosted 전체는 후속 credential lifecycle 통합 milestone에서 실행한다. 로컬 전체 platform/cold build는 중복하지 않았다.

## GDJ-0100 — bounded 문자열 경로와 reset URL 기반

2026-09-28, `cffb6e10` 이후 실제 reset URL의 선행 조건인 `<str:name>`를 추가했다. Router·typed reverse/accessor와
prefix/OpenAPI가 같은 kind를 소비한다. UTF-8 512-byte 한도·dot/control/slash 거부, 정수와의 language 충돌과
static method 우선순위를 유지한다. 기본 Web 오류 로그는 원본 path 대신 route name을 기록한다.
공개 reset Form/API·익명 CSRF admission·독립 제품 client는 이 검증의 완료 범위가 아니다.

통합 checkpoint source map `4a67e4b134290bb58fa6a5d0d0c69c6de22311133085bbc55be5e42029671173`
(non-Markdown **2,559 파일**)의 시작/종료 동일성을 확인했다. **12 packages / 241 필수 roots**이며
Web·API/OpenAPI·양 auth adapter·Admin·Account·Article composition **864 PASS**, 기존 생성 client/Article Web **13 PASS**,
기존 integer routing contract·외부 Go compiler admission **32 PASS**를 각 모드에서 실행했다.
Root run/pass·package 완료와 모든 child의 skip 부재를 event inventory로 확인했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 909 PASS / skip 0 | 14.158초 |
| race | 909 PASS / skip 0 | 134.573초 |
| CGO=0 | 909 PASS / skip 0 | 16.302초 |

합계 **2,727 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly와 `TZ=Pacific/Chatham`을 사용했다.
라우팅/표현 변경이며 별도 PostgreSQL milestone이나 전체 platform을 반복하지 않았다.
기존 여섯 ogen client는 실제 문서·생성 drift와 HTTP/SQLite 최종 상태를 기존 integration owner에서 검사했다.

- String/정수 혼합의 typed 조회·reverse·borrowed lifetime, type 강제 변환 거부, percent/Unicode/공백/query punctuation의
  한 번 escaping과 원문 보존을 확인했다. Encoded slash/backslash/control·dot, UTF-8 byte와 전체 path cap을 검사했다.
  입력별 404/405·sorted Allow, 같은 method의 교차 language, static 우선순위와 부분 JSON prefix 정책을 검사했다.
- Native Django 6.1 / CPython 3.14.3의 resolver/str converter/reverse **24개 관찰**과 upstream **3개 hash**를 기록했다.
  Go의 더 엄격한 dot/control/backslash/byte 제한은 명시적 차이다. Python **2 tests / 0.470초**가 fixture·hash-seed 재생과
  converter runtime mutation을 검출했다. Native URL 검사는 DB를 사용하지 않는다.
- 실제 loopback HTTP 서버에서 동시 문자열 round-trip을 검사했다. Handler/panic/response budget과 middleware 전후·미매칭
  오류 로그에 원본 token/path가 없고, 알려진 route name과 method가 남는 것을 확인했다. ReverseArgument의 fmt 출력도 비공개다.
- 별도 module의 고정 ogen v1.24.0으로 실제 새 string OpenAPI에서 probe client를 생성했다. GoDj import/replace 없이
  세 모드 각각 **21 wire checks / 21 requests**, **16 published-pattern checks**를 통과했다. Unicode·percent·점 포함 token이
  보존되고 invalid segment는 실패하며 자동 retry가 없었다. ECMAScript 끝 개행 예외도 실제 generator 정규식 엔진에서 거부했다.
  Server는 일반 빌드이며 client를 normal/race/CGO=0로 빌드했다. Root의 실제 HTTP handler는 위 race suite에도 포함했다.
  이는 routing probe이며 실제 reset Form/API의 generated consumer 검증으로 표시하지 않는다. Tool/dependency lock은 불변이다.

통합 뒤 OAS의 raw/reverse URL 한도 **설명 한 문장**을 명확히 하고, 전체 경로 4096/4097 경계의 공통 language test를 추가했다.
최종 source map `a747501df827ae615eac5f54363cad159f3f046ea1fe349c491446dd7dad3a07` (**2,559 파일**)의 delta는
`api/openapi/document.go`와 `web/string_router_test.go` 두 파일이며 routing/schema constraint는 동일하다.
후속 Web/OpenAPI **2 packages / 76 roots**를 normal **373 PASS / 0.907초**, race **373 PASS / 2.955초**,
CGO=0 **373 PASS / 1.182초**, 모두 skip 0으로 확인했다. 독립 client probe는 최종 OpenAPI 설명으로 재생성했다.

Source overlay **4개**는 string byte 한도 생략, mixed-kind overlap 오인, 원본 path 로그와 ECMA terminal guard 누락을
지정 assertion으로 검출했다. 영향 vet·gofmt와 두 attestation의 native source dependency 대조를 통과했다.
준비 중 compile import/이름 충돌과 probe의 generator option 이름을 바로잡았으며 compile 실패를 control 성공으로 세지 않았다.
원본/receipt·control·probe source/생성물은 `godj-many-to-many-reference-4sl0bvdp/string-routing-checkpoint-1790533946874403000`
아래 보존했다. Model IR·생성 ABI·기존 제품 client 생성물은 변경하지 않았다. 이 source의 Hosted 전체는 아직 실행하지 않았다.

구현 commit `95e3c0f0bf516652cc33b966bba3c24c4d1d30ee`의
[Hosted Fast 36341705950](https://github.com/progresshans/godj/actions/runs/36341705950)는 실제 Fast Go feedback step까지 성공했다.
이후 공개 reset consumer나 Hosted 전체의 검증으로 전이하지 않는다. Run/jobs/steps 원본은 reset-consumer checkpoint의 `hosted/`에 있다.

## GDJ-0100 — reset proof의 durable session과 Web runtime

2026-09-28, `507eb473` 이후 `auth.PasswordResetPersistence`와 동일 저장 영역의 SystemState 구현,
Web runtime의 entry/check/complete·확정 cookie 게시를 연결했다. Entry는 ID를 회전하거나 anonymous session을 만들고
token을 서버 값에 저장한다. 완료는 최신 proof·인증 binding·실제 fence 시점의 session 만료를 검사하며,
현재 proof 정리와 password/revision·대상 session 폐기·audit를 함께 저장한다.
Anonymous/다른 사용자 payload와 absolute lifetime을 보존하고, 본인 인증 session은 폐기한다.
제품 token route·Form/JSON·OpenAPI·독립 client와 email 요청의 동일 공개 응답은 아직 연결하지 않았다.

통합 checkpoint source map `bc15fca409bf798ed0e30e37263ad7f6876873b127fa514b36b50a5ebdc5d354`
(non-Markdown **2,553 파일**)의 시작/종료 동일성을 확인했다. **7 packages / 203 roots / 927 필수 항목**이다.
Auth/session/Identity/SystemState/Web의 **726 PASS**와 양 DB의 reset·password change·login **604 PASS**를 각 모드에서 실행했다.
필수 run/pass·package 완료와 skip 부재를 event inventory로 확인했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,330 PASS / skip 0 | 43.877초 |
| race | 1,330 PASS / skip 0 | 169.367초 |
| CGO=0 | 1,330 PASS / skip 0 | 46.082초 |

합계 **3,990 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly와 `TZ=Pacific/Chatham`,
private PostgreSQL **17.10 UTF8/libc/C**, `GODJ_REQUIRE_POSTGRES=1`을 사용했다. DB 정리 **0|0|0**과 container 제거를 확인했다.

- 실제 저장소의 익명/본인/다른 계정 proof 생성·ID 회전, 조회/약한 password 거부의 무변경, 다른 연결의 runtime 재개와
  완료·replay 거부를 검사했다. 이전에 확보한 native HTTP 양 DB fixture의 인증/자동 로그인 없음·token 제거·last_login·공백 의미를
  Go의 실제 저장 상태와 비교했다. Native의 다음 접근 시 session 삭제와 session save 실패 뒤 부분 password 변경은 차이로 유지했다.
- 잘못된 target·교체된 proof·다른 저장소 manager·좁은 manager 한도는 변경 없이 거부했다.
  한도 초과를 commit 전에 검사하는 `Manager.CheckRecord`는 시간/entropy/I/O를 사용하지 않는다.
- 익명과 본인 완료의 password/session/audit 각 쓰기 전후, callback zero/nil/twice/swallow, 일반/비교 불가능 오류,
  validation/cleanup, 취소·unknown rollback/commit·늦은 취소를 주입했다. Entry의 create/rotate 실패도 따로 검증했다.
  실패/unknown에는 결과 cookie가 없고, 확정된 늦은 취소만 기존 성공을 유지했다.
- 두 연결이 같은 proof를 동시에 제출하면 하나만 성공했다. Hash 중 logout·proof 교체·계정 변경과 ID collision의
  제한 재시도/unknown 무재시도, gate 대기 뒤 만료 재검사를 확인했다. DB scope 안에서 password hash를 하지 않았다.
- 실제 HTTP 서버·cookie jar와 양 DB의 runtime을 연결했다. CSRF 누락·다른 브라우저 거부, 불투명 session cookie 회전/삭제,
  독립 CSRF cookie 보존과 unknown commit의 무쿠키/무성공 응답을 확인했다. 이 probe handler의 입력은 테스트 전용이며
  제품 reset route·Form/API 검증으로 표시하지 않는다. Web 단위 검사는 잘못된 persistence 결과와 오류 cause 분류도 포함한다.

통합 뒤 같은 ID의 최신 payload/proof/인증 binding 변경에 대한 **test와 필수 목록만** 추가했다.
최종 source map `09a67665952a03caa481dabbe9b1b2def37b5236282d147b7fa26684a6359cd5` (**2,554 파일**)의
후속 delta는 test helper·양 DB adapter·양 필수 목록 5개뿐이며 제품은 동일하다. 추가 **2 roots / 8 필수 항목**은
normal **8 PASS / 2.420초**, race **8 PASS / 17.583초**, CGO=0 **8 PASS / 4.544초**, 모두 skip 0이다.
최신 일반 값은 보존하고 교체된 proof나 인증 binding은 거부하는 것을 실제 두 연결에서 확인했다.

Go source overlay **6개**는 최종 expiry 검사 생략, manager 한도 검사 생략, unknown cookie 게시,
이전 payload 덮어쓰기, 최신 proof·인증 binding 검사 생략을 지정한 assertion으로 검출했다.
초기 checkpoint에서 비교 불가능한 DB 오류의 직접 equality가 panic을 내는 결함을 찾아 알려진 비교 가능 오류만 비교하도록 고쳤다.
Fixture의 startup dummy hash를 작업의 hash 수로 세던 측정도 바로잡았다. 최초 실패와 정리 기록은 보존했으며 성공으로 세지 않았다.

최종 영향 vet·gofmt, CI 도구 **41 tests**, 두 attestation의 실제 Go source 의존성 대조를 통과했다.
Model IR·생성 ABI·생성물·native fixture는 바꾸지 않았고 로컬 전체 platform을 중복 실행하지 않았다.
원본/receipt는 `godj-many-to-many-reference-4sl0bvdp` 아래 `reset-session-checkpoint-1790532114441067000`,
`reset-session-latest-checkpoint-1790532447330421000`과 각각의 `controls/`에 있다.
최초 실패는 `reset-session-checkpoint-1790531949484302000`에 보존했다. 이 source의 Hosted 전체는 아직 실행하지 않았다.
구현 commit `cffb6e10a84ea030d5eb41b93594df1707adbba3`의
[Hosted Fast 36340235834](https://github.com/progresshans/godj/actions/runs/36340235834)는 실제 Fast Go feedback step까지 성공했다.
이후 문자열 경로 변경이나 Hosted 전체의 검증으로 전이하지 않는다.

## GDJ-0100 — HTTP reset 기준과 빌린 transaction의 원자 결합

2026-09-28, `fde61349` 이후 실제 proof session을 연결하기 전에 native HTTP 의미와 원자 구성 경계를 구현했다.
`PasswordResetter.Prepare`는 hash를 DB scope 밖에서 한 번 준비하고, owner에 결합한 불변 값의 `ApplyIn`은
현재 token·account·expiry·policy를 재검사한 뒤 password/현재 revision·session 폐기·audit를 호출자의 transaction에 쓴다.
`CheckTokenIn`은 빌린 scope에서만 읽으며 기존 `ResetPassword`도 같은 Prepare/ApplyIn 경로를 사용한다.
실제 proof persistence·Web/Form/API·독립 client가 연결됐다는 뜻은 아니다.

통합 checkpoint source map `ffe3c39ddac7726807b0067cc2f5ce68f62c81e35a404fb7e900c7f70d794779`
(non-Markdown **2,547 파일**)의 시작/종료 동일성을 확인했다. **3 packages / 31 roots / 371 필수 항목**이며
Identity **213 PASS**, SQLite/PostgreSQL의 reset service·mail·composition **198 PASS**를 각 모드에서 실행했다.
실제 run/pass·package 완료와 skip 부재를 event inventory로 확인했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 411 PASS / skip 0 | 13.635초 |
| race | 411 PASS / skip 0 | 64.831초 |
| CGO=0 | 411 PASS / skip 0 | 16.798초 |

합계 **1,233 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly와 `TZ=Pacific/Chatham`,
private PostgreSQL **17.10 UTF8/libc/C**, `GODJ_REQUIRE_POSTGRES=1`을 사용했다. DB 정리 **0|0|0**과 container 제거를 확인했다.

- 독립 Django 6.1 / CPython 3.14.3 HTTP runner는 실제 CSRF/auth/session middleware와 두 DB에서
  **15개 관찰군 / 8개 upstream hash**를 확보했다. 두 backend의 관찰이 정확히 같다. 알려진/없는/비활성/사용 불가 email,
  send 실패의 공개 redirect, missing/bad/foreign CSRF, token 숨김·다른 브라우저·교체/삭제된 proof,
  confirmation·policy 오류 순서와 password 미반사, 익명/본인/다른 사용자 session·replay를 포함한다.
  Native session save 실패는 HTTP 500이어도 새 password가 남고 DB의 reset token이 남는 것을 관찰했다.
  Go의 active 재검사·즉시 session 폐기·후속 proof 저장과 원자 결합은 [ADR-0076](../adr/0076-credential-snapshots-and-session-binding.md)에 구분했다.
  Python **2 tests / 13.040초**가 hash-seed 재생·양 fixture 전체 비교와 CSRF/cleanup/자동 로그인 **3개 runtime mutation**을 확인했다.
- Go에서 ApplyIn 이후 실제 DB로 password 변경을 확인한 다음 후속 오류를 주입했다. Password/session/audit 전부 rollback되고
  다른 연결에서도 원상태이며 token은 유효했다. 확인된 rollback 뒤 준비 값을 명시적으로 재사용해도 hash를 반복하지 않고,
  성공 뒤 재사용은 거부했다. 다른 owner·zero/nil/cancel 입력은 아무 효과가 없었다.
- 준비 후 다른 연결의 profile/revision 변경은 보존하고 email/password/active·만료 변경은 거부했다.
  빌린 token 검사는 nested snapshot·hash·write를 만들지 않고 현재 transaction의 계정 상태를 사용했다.
  기존 rollback/unknown·cleanup/취소·두 연결 경쟁과 메일에서 실제 reset까지의 검증도 모두 실행했다.
- Go overlay **4개**가 prepared owner 누락, 빌린 token admission 생략, 현재 revision 무시와 최종 policy 누락을
  각각 지정한 assertion으로 탐지했다. 실제 테스트 실행 없이 생긴 compile 오류는 성공으로 세지 않았다.

최초 새 test는 트랜잭션 안의 확인용 `First`에 명시적 ordering을 빠뜨려 양 DB에서 실패했다. 이를 고쳤다.
CI 도구는 필수 목록의 빈 줄과 PostgreSQL 항목을 relation owner에도 넣은 오류를 검출했다.
필수 항목은 PostgreSQL owner에 유지하고 중복된 다른 owner의 9개 항목만 제거했다. 통합 checkpoint의 필수 실행 집합이
그대로임을 독립 대조했으며 **제품·test·fixture는 바꾸지 않았다**. 최종 source map
`e6922c0a1851dee436fe796e29c74dfc1fb87c2dd4092abe46621d13a8f6f049`에서 유일한 후속 delta는
`scripts/ci/relation-required.txt`다. 최종 CI 도구 **41 tests / 3.425초**가 실제 shell inventory 전달과 실행 owner를 확인했다.
이 metadata 수정 때문에 DB/platform 검증을 중복하지 않았다. Model IR·생성 ABI·생성물은 바꾸지 않았다.
영향 vet·gofmt, Markdown 155개 링크와 diff 검사를 통과했다.

원본은 `godj-many-to-many-reference-4sl0bvdp` 아래 `reset-http-reference-1790529404967304000`,
`reset-composition-checkpoint-1790529973472606000`에 있다. 최초 실패와 네 control은
`reset-composition-checkpoint-1790529801829814000`에 보존했으며 control의 제품 SHA가 최종 코드와 같음을 확인했다.
이 source의 Hosted 전체는 실행하지 않았다. 앞선 `fde61349` Fast나 `fb817d6b` full의 성공을 현재 source에 전이하지 않는다.
구현 commit `507eb473340ab1b08092dae567f3cb25fcca1770`의
[Hosted Fast 36337317864](https://github.com/progresshans/godj/actions/runs/36337317864)은 실제 Fast Go feedback step까지 성공했다.
Run/jobs/steps 원본은 reset-composition checkpoint의 `hosted/`에 보존했다.

## GDJ-0100 — snapshot에 결합한 reset 메일 요청

2026-09-28, `f66b393c` 이후 `identity.PasswordResetMailer`와 default/custom content를 연결했다.
한 read snapshot의 active/DB-iexact 후보에 NFKC/full casefold·usable 검사, 같은 Account의 수신자/token,
완전한 후보/메일 준비 뒤 전달을 구현했다. 내부 I/O 실패·취소·unknown을 보존하며 자동 재시도하지 않는다.
제품 공개 Form/API·독립 client는 아직 연결하지 않았다. 동일 공개 응답과 별도 오류 보고는 다음 소비자의 검증 조건이다.

통합 checkpoint source map `41ff3ced536f2bed5ee3cb4514692383df8740ec90e4fd67a2f57be3a194a3c7`
(non-Markdown **2,542 파일**)의 시작/종료 동일성을 확인했다. **9 packages / 90 roots / 752 필수 항목**을 실행했다.
공통 Identity/Admin/validation/mail/Unicode와 두 source-binding owner는 **882 PASS**, 양 DB reset 전체는 **180 PASS**다.
각 mode의 필수 run/pass·package 완료와 skip 부재를 감사했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,062 PASS / skip 0 | 16.008초 |
| race | 1,062 PASS / skip 0 | 107.190초 |
| CGO=0 | 1,062 PASS / skip 0 | 19.692초 |

합계 **3,186 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly Go와 `TZ=Pacific/Chatham`,
private PostgreSQL **17.10 UTF8/libc/C**, `GODJ_REQUIRE_POSTGRES=1`을 사용했다. DB 정리 **0|0|0**과 container 제거를 확인했다.

- 고정 Django reset observer에 **12개 수신자 선택**과 **9개 입력** 관찰을 추가했다. 전체 **13개 관찰군**과
  **8개 upstream hash**를 실제 SQLite/PostgreSQL에서 확보했고 이 구성에서는 두 backend의 결과가 같았다.
  Native DB prefilter와 NFKC/full casefold를 구분하며 duplicate·inactive/unusable·unknown·quoted/IDN/Unicode local,
  strip·length·NUL/error 순서를 비교했다. 이전 관찰과 source hash를 보존했고 최종 Python **2 tests / 23.281초**가
  hash-seed 재생·양 backend fixture 전체의 정확한 일치와 기존 runtime controls를 확인했다.
- `CaseFolding.txt`의 고정 Unicode 16 원문 **86,092 byte**, SHA256
  `6f1f9c588eb4a5c718d9e8f93b782685e5c7fec872cf05e8e6878053599e09bb`를 manifest와 deterministic gzip에 추가했다.
  Python 결과로 table을 만들지 않고 공식 C/F mapping에서 생성했다. 독립 CPython 3.14.3의
  **1,112,064 scalar** full-casefold digest `5f578437c5eaa594f59de533d294d0a0cfa0070ea0b41308000f745e96cd9136`와 비교했다.
  기존 NFKC/lower/property/context digest는 동일하다. 공식 normalization corpus·sequence·잘못된 UTF-8 검사도 유지했다.
- 모든 메일의 token이 실제 대상 계정에서 유효하고, 그 token으로 password/현재 revision·session 폐기·audit가
  원자 반영되며 다른 연결에서도 보이는지 검사했다. 요청 자체는 hash·User/session/audit를 변경하지 않는다.
  Snapshot 직후 다른 연결의 email/credential/active/last_login 변경은 이전 token을 무효화하고 profile 편집은 보존한다.
- 후보 256개 경계와 초과 무전송, read callback/cleanup 오류, 두 번째 render 오류·header injection·크기 초과·취소의
  무전송, 전송 실패 후 다음 대상 처리·unknown·취소 후 중단·마지막 확정 접수 뒤 취소를 검사했다.
  Renderer/sender가 DB read scope 밖에서 호출되고 origin/path/config 및 fmt/JSON이 값을 노출하지 않는지 확인했다.
- 별도 source overlay **10개**가 DB prefilter 누락, 새 token과 예전 수신자 혼합, 후보 초과 허용, cleanup 무시,
  render 전 조기 전달, 실패 뒤 대상 중단, unknown 오분류, full casefold의 lowercase 대체,
  두 attestation의 mail 파일 누락을 각각 지정한 assertion으로 탐지했다. Compile 실패나 미실행을 성공으로 세지 않았다.

`validation.ValidEmail`로 문법을 옮긴 뒤 기존 Admin의 독립 304개 EmailValidator 관찰도 통과했다.
메일 runtime이 이제 Identity의 실제 의존성이므로 두 attestation 목록에 `mail/`을 추가하고 native Go 의존성 대조를 수행했다.
Unicode 생성 drift·실패 보존 **2 tests**, CI 도구 **41 tests**, 영향 vet·gofmt와 문서 링크·diff를 확인했다.
Model IR/ABI와 model 생성물은 바꾸지 않았고 전체 platform 검증을 로컬에서 중복 실행하지 않았다.

통합 checkpoint 뒤 native reference를 양 DB fixture의 모든 관찰을 비교하도록 강화하고,
Admin 제품/reference test의 기존 import 그룹을 정리했다. 제품 동작·생성 table·fixture와 나머지 test는 동일하다.
최종 source map `5f4e92b078b1504d7e53d3d01a37255ba4d7023a27328c371b1892f046133c34` (**2,542 파일**)과
세 파일의 delta를 별도 기록했다. 최종 Admin test는 normal **340 PASS / 2.860초**, race **340 PASS / 5.715초**,
CGO=0 **340 PASS / 0.779초**, 모두 필수 **340개 실행·성공 / skip 0**이며 위 최종 Python 검증도 통과했다.
이 assertion·import 정리 때문에 통합 DB/platform 검증을 중복하지 않았다.

원본/receipt는 `godj-many-to-many-reference-4sl0bvdp` 아래 `reset-request-reference-1790526396735756000`,
`reset-request-checkpoint-1790527303758873000`과 그 `controls/`에 보존했다. 독립 Unicode output/observer도 reference 디렉터리에 있다.
완료된 `fb817d6b` Hosted full과 `f66b393c` Fast는 이 이후 request/Unicode 변경의 Hosted 검증으로 전이하지 않는다.
구현 commit `fde6134901fafcd5e9a3c9455f0e34727aa15e47`의
[Hosted Fast 36335750053](https://github.com/progresshans/godj/actions/runs/36335750053)은 실제 Fast Go feedback step까지 성공했다.
Run/jobs/steps 원본은 reset-request checkpoint의 `hosted/`에 보존했다.

## GDJ-0100 — 공통 메일과 명시적 SMTP 접수 결과

2026-09-28, `18870d01` 이후 불변 mail Message·Address·MIME, bounded Memory와 context/timeout을 따르는
SMTP backend를 구현했다. Header injection·Bcc 비공개·입력 소유 복사·진단 redaction, STARTTLS/implicit TLS·인증서/이름,
AUTH PLAIN·SMTPUTF8, 모든 RCPT 확인 뒤 DATA, 거절/접수 불명/최종 250 뒤 정리 실패의 결과를 구분한다.
설계는 [ADR-0078](../adr/0078-mail-message-ownership-and-delivery.md), 독립 출처는 [mail/NOTICE](../../mail/NOTICE.md)에 둔다.
Reset 수신자 선택·메일 내용/발급 값·공개 응답과 제품 Form/API·독립 client의 완료는 아니다.

최종 source map `66036f6b17bc3997ca434a309eea6ce979ff67c2cfc578ceea97142649877526`
(non-Markdown **2,537 파일**)의 시작/종료 동일성을 확인했다. Message/Memory/SMTP와 영향을 받는 Form/ModelForm은
**3 packages / 63 roots**, 별도 PostgreSQL 의존성 회귀는 **1 package / 7 roots / 28 필수 항목**으로 실행했다.

| 모드 | Mail·Form 완료 | PostgreSQL 의존성 완료 | 실행 시간 합계 |
|---|---|---|---|
| normal | 1,299 PASS / skip 0 | 28 PASS / skip 0 | 4.873초 |
| race | 1,299 PASS / skip 0 | 28 PASS / skip 0 | 17.442초 |
| CGO=0 | 1,299 PASS / skip 0 | 28 PASS / skip 0 | 6.793초 |

합계 **3,981 PASS / skip 0**이다. 각 package의 종료·필수 run/pass와 실패/skip 부재를 독립 event 검사기로 확인했다.
Darwin arm64 / Go 1.26.5, offline readonly module, `TZ=Pacific/Chatham`을 사용했다.
현재 model/IR/generator·생성 ABI는 바꾸지 않았으므로 generated drift나 전체 platform을 중복 실행하지 않았다.

- 실제 loopback TCP SMTP가 envelope·MIME·dot-stuffing을 수신했다. Plaintext, STARTTLS, implicit TLS,
  신뢰되지 않는 인증서와 잘못된 이름·CA pool 소유 복사·capability 부재·인증 거절을 검사했다.
  보안 협상 실패 뒤 MAIL/DATA를 보내거나 인증 정보를 평문으로 보내지 않았다.
- 일부 RCPT 뒤 거절은 DATA 없음, DATA command/최종 4xx/5xx는 명시적 거절, 마지막 응답 유실은 unknown이었다.
  250 확인 뒤 QUIT 단절/취소는 nil 접수를 유지했다. 본문 쓰기 중 단절, greeting/최종 응답 중 취소,
  전체 timeout과 응답 상한을 검사했다. 전송 재시도는 없다.
- 본문 마지막 byte·줄바꿈·공백·긴 Unicode/ASCII 제목, multipart alternative·binary attachment·quoted/IDN 주소,
  UTC 변환 뒤 날짜 범위와 EHLO IP literal을 검사했다. Inputs/returned byte·recipient slice의 소유권을 확인했다.
  Memory의 concurrent Send·상한 초과·Snapshot/Drain과 redacted fmt/JSON을 검사했다.
- 고정 Django 6.1 / CPython 3.14.3 native mail에서 **5개 관찰군**, Django **4개**와 Python email **2개** source hash를 기록했다.
  별도 Python **2 tests**가 다른 hash seed의 같은 결과와 recipient/delivery **2개 runtime mutation**을 검사했다.
  Go는 독립 fixture의 decoded MIME·Bcc·주소를 대조하며 SMTPUTF8·bounded/immutable API의 차이를 명시했다.
- Go source overlay **6개**는 Bcc envelope 누락, 첨부 소유 복사 누락, unknown 결과 오분류,
  접수 후 정리 실패 오분류, TLS 검증 생략, Memory 한도 누락을 지정된 의미 assertion으로 탐지했다.
  실제 테스트가 실행되지 않은 compile 실패를 검증 성공으로 세지 않았다.

IDNA를 위해 `x/net v0.57.0`을 명시했고 module graph가 `x/text v0.40.0`과 `x/sync v0.22.0`을 선택한다.
기존 독립 client module도 같은 버전을 사용한다. Form의 해당 정규화 소비자와 PostgreSQL 연결 설정·pool 종료·
native read snapshot·acquire 실패·저장 Identity/HTTP를 검증했다. Private PostgreSQL **17.10 UTF8/libc/C**에서
비ASCII 비밀번호의 실제 SCRAM 연결을 사용했으며 table/schema/다른 connection **0|0|0**과 container 제거를 확인했다.

최초 PostgreSQL 보조 checkpoint는 검증용 URL의 userinfo를 percent encoding하지 않아 native pgx config parsing에서
실패했다. Raw userinfo 거부와 escaped userinfo의 password 보존을 별도 probe로 확인했고 검증 도구를 고쳐 위 결과를 확보했다.
제품 DB/인증 코드는 수정하지 않았다. 실패/정리 기록 `mail-dependency-checkpoint-1790524898500010000`을 보존한다.
최초 mail pass 후 코드 검토로 UTC 변환 경계와 EHLO literal을 보완해 최종 source에서 다시 실행했다.

원본/receipt는 `godj-many-to-many-reference-4sl0bvdp` 아래 `mail-reference-1790524131136124000`,
`mail-checkpoint-1790525182765815000`과 그 `controls/`, `mail-dependency-checkpoint-1790525047688345000`에 있다.
영향 vet, module checksum verification, 두 attestation의 native dependency closure, gofmt·Markdown 155개 링크·diff, 최종 source 동일성을 확인했다.
운영 SMTP provider에 실제 사용자 메일을 보내지 않았다. 이 source의 Hosted 전체는 아직 실행하지 않았으며,
완료된 `fb817d6b`의 결과에는 새 mail package와 dependency 변경이 포함되지 않는다.
구현 commit `f66b393cb365b8894efc163312ff1808b853c820`의 [Hosted Fast 36332336997](https://github.com/progresshans/godj/actions/runs/36332336997)은
실제 Fast Go feedback step까지 성공했다. Run/jobs/steps 원본은 mail checkpoint의 `hosted/`에 보존했다.

## GDJ-0100 — 계정 소비자·reset service·source 목록 보완 Hosted 완료

2026-09-28, source `fb817d6b58b53f147067d027624786e004df8dda`의
[Hosted full 36328590201](https://github.com/progresshans/godj/actions/runs/36328590201), attempt **1**이
**62 jobs 모두 success**로 종료됐다. 최종 `CI result (full)`의 실제 로그에서 `scope=full`,
`full_platform_verified=true`와 정확히 **8개 실행 owner**의 성공을 확인했다.
Command products·conformance·exact Darwin·portable Go·PostgreSQL·project check·Python compatibility·relation product를 포함한다.
선행 Python timeout 이후 검증 항목을 줄이지 않고 전체 실행을 마쳤다.

같은 run의 새 PostgreSQL capture 두 개를 artifact archive digest, payload checksum, provenance,
producer job/attempt와 대조했다. Worker의 현재 파일을 대입하지 않고 해당 source의 Git blob을 독립적으로 읽어
선언된 목록의 경로·mode·size·SHA256을 재계산했다. Native dependency closure 검사도 이 source의 CI가 실행한다.

| Capture | 파일 | payload byte | source binding SHA256 |
|---|---|---|---|
| systemstate | 599 | 6,393,514 | `2e5cacb666b98a04ffe50b3d83bc417887b90b269d4f19312533ceff140d3ad3` |
| operator | 675 | 6,238,021 | `7a90c03684fffb3e134cd44cf0e804470a3ef038359d1e5952caa03875c0e7b1` |

Archive·capture/provenance·Git blob inventories·run/jobs·최종 gate log와 `pass=true` receipt는
`godj-many-to-many-reference-4sl0bvdp/hosted-full-36328590201-1790524734332431000`에 보존했다.
이 완료는 이후 `f66b393c`의 mail/dependency 변경이나 그 뒤 reset recipient/Unicode/form 변경을 포함하지 않는다.
선행 `b995c8c6`의 cancelled/failure 기록은 그대로 유지한다.

## GDJ-0100 — reset token과 현재 상태를 재검사하는 원자 service

2026-09-28, `44069bc1` 이후 `identity.PasswordResetter`를 추가했다. 명시적 immutable key ring·timeout·clock·validator와
기존 native identity backend를 사용한다. 수신자에게만 전달할 token 발급과 읽기 전용 검사, hash 전 read 종료,
최종 token/현재 profile 재검사·password/현재 revision patch·모든 대상 session 폐기·값 없는 audit의 원자 저장을 구현했다.
IR/model/migration·생성 ABI·dependency lock 변경은 없다. 메일·제품 reset Form/API·독립 client는 아직 연결하지 않았다.

최종 source map `da2eaf32c451b5403e135ba67bd6d37295c3e11e66686f37d9eeb41b3d2ccb04`
(non-Markdown **2,525 파일**)의 시작/종료 동일성을 확인했다. Auth/Identity/Systemstate 전체와 양 DB의 새 reset roots를
실행했고 **5 packages / 115 roots / 371 필수 항목**의 run/pass·package 완료·no-skip을 감사했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 592 PASS / skip 0 | 12.532초 |
| race | 592 PASS / skip 0 | 119.549초 |
| CGO=0 | 592 PASS / skip 0 | 18.583초 |

합계 **1,776 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly Go, `TZ=Pacific/Chatham`,
private PostgreSQL 17.10 UTF8/libc/C와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다. DB 종료 `0|0|0`·container 제거를 확인했다.

- Credential/email/last_login·identity·서명·만료/future·active/usable 결합, canonical bounded parsing,
  key copy·fallback 제거·마지막 verification key·active-key 발급과 diagnostic 비공개를 검사했다.
- 독립 Django 양 DB fixture의 11개 binding을 직접 비교하며 inactive·microsecond 변경은 원본 true를 확인한 뒤
  Go의 더 엄격한 false를 별도로 검사한다. Native 미래 시각 허용·stale form overwrite와의 차이도 ADR/DEV에 명시했다.
- 260개 session의 batch 경계에서 target만 폐기하고 다른 사용자·익명 session을 보존했다. 발급/검사/약한 password는
  hash·User/session/audit 무변경이다. 재접속·원문 공백·같은 password의 새 hash·token 재사용 거부·last_login 보존을 확인했다.
- 준비 중 다른 연결의 username/profile/revision 편집을 보존하며 현재 policy를 다시 적용한다. Credential/email/last_login,
  비활성/사용 불가/삭제·시각 변화는 최종 fence에서 거부한다. 두 연결의 같은 token 제출은 정확히 하나만 성공했다.
- Read callback zero/nil/twice/swallow/cleanup과 write callback zero/nil/twice/swallow, 비교 불가능한 host error,
  password·audit 전후·revocation 실패·취소·unknown rollback/commit·commit 뒤 취소를 실제 native scope에서 검사했다.
  Token/validation 거부에 cleanup 오류가 붙으면 일반 거부로 낮추지 않는다. Hasher 실패·같은 encoded 반환·취소도 검사했다.
- 별도 source overlay **9개**가 credential/email/last_login 결합, 현재 token 재검사, session 폐기, audit,
  현재 profile 보존·현재 policy·unknown 성공 오인을 각각 지정된 assertion으로 탐지했다. Compile 실패를 성공으로 세지 않았다.

최초 checkpoint에서는 caller의 validator slice를 nil로 바꾼 뒤 그 caller config로 두 번째 service까지 만들던 테스트가
실패했다. 첫 service의 소유 복사는 정상이고 새 constructor의 nil 거부가 올바른 동작이므로 재접속용 config를 새로 구성했다.
최초 실패 `password-reset-checkpoint-1790521172804713000`과 최종
`password-reset-checkpoint-1790521256503677000`의 raw/receipt·controls를 `godj-many-to-many-reference-4sl0bvdp` 아래 보존했다.
영향 vet·gofmt, CI 도구 41개, 현재 두 attestation의 native dependency closure, Markdown 152개 링크·diff를 확인했다.
최종 product/control source hash도 동일하다. 이 로컬 scope의 성공을 Hosted 전체 또는 메일/HTTP consumer의 완료로 옮기지 않는다.

구현 commit `fb817d6b58b53f147067d027624786e004df8dda`를 양 branch에 게시하고,
같은 commit의 [Hosted full 36328590201](https://github.com/progresshans/godj/actions/runs/36328590201)을 `suite=full`로 요청했다.
선행 run의 마지막 작업을 취소하지 않도록 현재 worker branch `feature/json-models`를 사용했으며,
양 branch가 같은 commit임을 dispatch 전에 확인했다. 최종 종료와 새 source-bound captures는 위 Hosted 완료 항목에서 확인했다.
같은 code source의 [Hosted Fast 36328406907](https://github.com/progresshans/godj/actions/runs/36328406907)은 실제 Fast Go feedback까지 성공했다.
Full 시작과 Fast 완료의 run/jobs/steps 원본은 reset checkpoint의 `hosted/`에 보존한다.

다음 소비자 설계를 위한 별도 native SQLite HTTP probe에서는 실제 PasswordResetView/ConfirmView와 CSRF/session/auth
middleware를 실행했다. 알려진/없는 email 모두 같은 302 목적지, CSRF 거부 403, valid token을 session에 넣고
`set-password` URL로 302 이동, form HTML의 raw token 부재·no-store, 다른 브라우저의 거부와 약한 password 오류를 관찰했다.
성공 후 session의 reset token 제거·자동 로그인 없음·last_login 불변, 기존 사용자 session의 다음 접근 시 폐기를 확인했다.
이는 양 DB reference·mutation tests 또는 Go HTTP consumer의 완료가 아니다. 원본 runner/SQLite JSON/7개 upstream hash와
cleanup receipt는 `password-reset-http-probe-1790522019669116000`에 보존했다.


## GDJ-0100 — 제품 source binding 보완과 reset의 독립 기준

2026-09-27, 실제 제품 entrypoint의 `go list -deps`와 비교해 두 attestation source 목록의 누락을 확인했다.
Account template, common-password 사전, Unicode·Decimal·JSON·UUID와 migration 내부 패키지를 추가했고,
system-state 목록에는 현재 Permission revision migration도 추가했다. 두 owner의 독립 검사는 현재 OS·CGO·race
선택의 local Go/native source·embedded asset과 symlink 방어 범위를 대조한다. 기존 목록을 expected로 재사용하지 않는다.
Wire format과 dependency lock, 제품 runtime 동작은 바꾸지 않았다. 구현 commit `44069bc137480154b683cd7d95eb936b16d8ec99`를 양 branch에 게시했고
[Hosted Fast 36327113730](https://github.com/progresshans/godj/actions/runs/36327113730)은 실제 Fast Go feedback까지 성공했다.
Run/jobs/steps 원본은 최종 source-closure checkpoint에 보존했다.

최종 source map `5ff364d4c681e9e0fe113d020dc0d712d678129e4584fcaabd4a9a130ae9d308` (non-Markdown **2,519 파일**)의 시작/종료 동일성을 확인했다.
`-trimpath`를 적용한 두 owner와 shared I/O **3 packages / 39 roots**의 normal **254 PASS (4.700초)**,
race **254 PASS (7.230초)**, CGO=0 **254 PASS (2.143초)**, 합계 **762 PASS / skip 0**이다.
Shared I/O는 독립 root가 없는 helper이며 두 owner가 실행하는 실제 파일 검증으로 포함한다.
이전 source 목록 overlay 두 개는 각각 **19개/18개 실제 의존 파일 누락**으로 지정한 assertion에서 실패했다.
CI 도구 41개, workflow 계약 package, 영향 vet·gofmt와 Markdown 152개 링크·diff 검사를 통과했다.
최초 checkpoint runner의 helper-package root 필수 가정은 실행 전 discovery에서 실패했으며 별도 기록을 보존했다.
이 로컬 scope는 새 PostgreSQL process capture나 전체 platform 검증을 대신하지 않는다.

`b995c8c6`의 [Hosted full 36324864466](https://github.com/progresshans/godj/actions/runs/36324864466)은
Python 3.14.3 owner가 20분 제한에 도달했다. 실제 portable Python **362개**는 **1,098.110초**에 끝났고,
exact Darwin owner가 맡는 네 항목만 허용된 skip이었다. 후속 all-scenario semantic digest는 취소되고
clean-worktree step은 실행되지 않았다. Python 3.12.13/3.13.15/3.14.7 owner는 성공했으나 전체 성공을 뜻하지 않는다.
362개 실행과 all-scenario digest·clean-worktree를 그대로 유지하고 Python compatibility 제한을 **30분**으로 조정했다.
나머지 live jobs의 terminal 결과도 보존한다. 이 run의 capture를 확인해도 당시 선언 목록의 결합만 입증하며,
위 누락을 보완한 source의 새 capture와 통합 milestone이 필요하다. 실제 두 PostgreSQL capture의 archive SHA-256·동일 run/attempt/producer provenance와
해당 commit Git blob에서 재계산한 당시 선언 목록의 결합은 확인했다. System-state는 578 files/5,978,200 bytes, operator는
655 files/5,823,526 bytes이며 `hosted-full-36324864466-1790520313642329000`에 원본을 보존한다.
이 부분 확인을 전체 PASS로 올리지 않았고 과거 raw evidence도 다시 작성하지 않는다.
선행 run은 **62 jobs 중 60 success, Python 3.14.3 cancelled, CI result (full) failure**로 종료됐고
전체 conclusion은 `cancelled`다. 마지막 macOS race 작업도 성공했지만 전체 gate는 올바르게 미통과를 유지했다.
Terminal run/jobs와 부분 확인 receipt(`pass=false`)를 같은 Hosted evidence 디렉터리에 보존했다.

별도로 고정 Django 6.1/CPython 3.14.3의 native PasswordResetTokenGenerator·PasswordResetForm·SetPasswordForm과
in-process mail을 실제 SQLite·PostgreSQL 17.10에서 실행했다. **11개 관찰군**, 양 DB의 observations와
**8개 upstream module hash**가 일치했다. Private PG table 0과 container 제거를 확인했다.
Password/email/초 단위 last_login 변경의 token 무효화, 만료 경계·미래 시각·secret fallback,
대소문자가 다른 중복 email의 active/usable 수신자 선택과 없는 계정의 같은 반환,
전송 실패를 삼키고 다음 수신자로 진행하는 native 동작·template 실패 전파,
password 확인/정책/원문 공백과 stale SetPasswordForm의 profile/active 덮어쓰기를 관찰했다.
Native active 변경만으로는 token이 무효화되지 않는다는 관찰도 그대로 보존한다.
네트워크 mail은 전송하지 않았고 raw 링크/token/password는 관찰 JSON에 남기지 않았다.
Python tests **2개**와 token binding·inactive selection·mail delivery mutation **3개**, 두 hashseed 결정성과
기록된 fixture/upstream hash 비교를 **23.481초**에 확인했다. 초기 deprecated mail 설정의 경고 실패는
Django 6.1 `MAILERS` 설정으로 해결했으며 warning을 숨기지 않았다.
이는 독립 기준 확보이며 **Go reset service·메일 전달·Form/API 구현 또는 native HTTP reset view 검증의 완료가 아니다**.

Raw/receipt는 `godj-many-to-many-reference-4sl0bvdp` 아래에 보존했다.

- `source-closure-final-1790520197272757000`: 최종 세 모드 event/inventory·source map·두 누락 controls·CI tools/workflow/vet·Hosted Python logs.
- `source-closure-checkpoint-1790519442546599000`: 선행 inventory 검사·controls와 trimpath 확인.
- `password-reset-reference-1790519441441160000`: 양 DB native 관찰·runner/hash·cleanup과 Python tests/controls.

## GDJ-0100 — 일반 계정 Form·Session API와 독립 client

2026-09-27, `8986dcda` 이후 일반 계정의 제품 login/logout·password Form/완료 화면과 Session JSON/OpenAPI를
Article에 연결했다. 명시적 authenticated-only API admission, session touch/cleanup 없는 preflight와
필드별 password 검사를 추가했다. 빈 permission은 여전히 구성 오류다. Admin은 shared redirect의 추가 앱 경로를
명시적으로 선언하며 staff admission을 유지한다. IR/model/migration/생성 ABI 변경은 없다.

주요 구현 source map `ac829cc815d5c030a6c0a23a2b68386f49d676e033e700717c09340826196313`
(non-Markdown **2,509 파일**)의 시작/종료 동일성을 확인했다.
Core 15 packages의 전체 tests, 독립 OpenAPI consumer 1 package의 전체 tests, 양 DB Identity 전체를 실행했다.
**18 packages / 402 roots / 1,980 필수 항목**의 실행·성공·package 종료와 no-skip을 감사했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 3,708 PASS / skip 0 | 108.110초 |
| race | 3,708 PASS / skip 0 | 358.431초 |
| CGO=0 | 3,708 PASS / skip 0 | 97.851초 |

총 **11,124 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly Go,
`TZ=Pacific/Chatham`, private PostgreSQL 17.10 UTF8/libc/C와 `GODJ_REQUIRE_POSTGRES=1`을 사용했다.
DB 종료 `0|0|0`, owned container 제거와 source 동일성을 확인했다.

- 고정 Django 6.1/CPython 3.14.3의 실제 PasswordChangeForm/View를 SQLite·PostgreSQL에서 실행해 복합 오류를 포함한
  **10개 거부**와 성공·같은 password·실패 부작용·검증 뒤 변경을 관찰했다. 양 DB observations/9개 upstream module hash 일치,
  private PG table 0·container 제거, Python tests **2개**와 runtime mutation **3개**를 확인했다.
- 양 DB의 제품 Form은 old-password/required/confirmation/policy 오류 조합과 password 비공개 재표시를 이 독립 fixture와 비교한다.
  잘못된 입력은 hash·User/session/audit 변화가 없으며 시간을 진행시킨 뒤에도 session을 touch/cleanup하지 않는다.
- 실제 일반 계정 login의 Unicode 정규화·안전한 next, Form/JSON 변경, 같은 password의 두 번째 변경,
  current payload/lifetime·last_login 보존, session cookie만 교체·기존 CSRF 유지·other-session 폐기,
  새 runtime의 credential/session 관찰과 제품 logout을 확인했다. Article에서 ordinary 계정은 staff Admin으로 진입하지 못한다.
- CSRF/origin/query/empty-query/unknown/duplicate/null/type/string/body/media, 중복·stale·만료·익명 cookie를 검사했다.
  Password/audit/revocation 실패·취소·validation+cleanup·unknown rollback/commit은 실제 native transaction에 주입했다.
  실패는 cookie나 private 원문을 게시하지 않고, unknown은 503/재시도 없음이며 durable commit 여부를 별도로 비교한다.
- 실제 schema에서 여섯 번째 `accountsession` ogen client를 생성했다. 기존 다섯 profile schema는 byte-identical이다.
  모든 profile의 offline generated drift·별도 module build와 실제 HTTP를 세 모드에서 검사했으며 child도 부모 race를 따른다.
  Account child는 제품 Form으로 두 일반 session을 만들고 generated CSRF/password/typed errors/required replacement cookie,
  other-session 거부와 제품 logout을 실행한다. 부모는 reopen한 SQLite의 새 password·revision 3·last_login·unused/foreign session과
  정확한 두 password audit를 별도로 확인한다. Synthetic 503은 decoder/no-retry 증거이고 실제 unknown 증거와 구분한다.
- source overlay **10개**가 current credential fence·other-session revocation·last_login·current revision·rollback·unknown·
  wrapped verifier/caused validation·read-only API admission·Django 복합 Form 오류 누락을 지정된 assertion으로 탐지했다.
  Compile 실패를 탐지 성공으로 세지 않았으며 해당 제품 파일 hash가 최종 source에서도 같음을 확인했다.

후속 source `6329a7e1d58880662966394688b501ef36992359acf80da53dfdda0f547944c6`
(non-Markdown **2,510 파일**)은 로그인/logout 거부 테스트 helper·양 DB root 선언·두 CI 목록의 **5개 파일만** 다르다.
제품 코드·reference·generated client 입력/출력이 동일함을 source map으로 확인했다. 새 **2 roots / 36 필수 항목**을
normal **36 PASS (1.889초)**, race **36 PASS (14.986초)**, CGO=0 **36 PASS (3.349초)**로 각각 검사했다.
잘못된 비밀번호·없는/비활성 사용자·필수/중복 username·escaping·CSRF/origin/query/next/media와 logout GET/CSRF/중복/unknown
거부에서 cookie·last_login·User/session/audit 무변경을 확인했다. DB 정리 `0|0|0`과 source 동일성도 확인했다.
별도 실행 108 PASS를 앞 checkpoint의 같은 source/단일 실행으로 합치지 않는다.
현재 필수 core 목록 851항목으로 기존 세 raw event stream을 다시 감사해 새 account constructor 필수 subcase도 모두 확인했다.
CI 도구 **41개**, 영향 vet, gofmt와 Markdown **152개** 링크 검사를 통과했다.

최초 실패를 보존했다. Admin의 정확한 redirect allowlist에 추가 앱 경로 선언이 필요했고,
Form GET과 JSON command의 route 이름 중복을 분리하고 account construction에서도 전체 route를 검사하도록 수정했다.
기존 Admin의 non-staff 처리는 login redirect이며 테스트의 403 가정을 고쳤다. validation+cleanup 주입은 두 번째 policy fence의
실제 거부도 함께 주입해야 rollback을 관찰할 수 있어 fixture를 수정했다. CI 실행 목록은 주석을 허용하지 않아 형식을 정리했다.

구현 commit `b995c8c6c8256bf3b1ea4995f383472d7c27629c`를 양 branch에 게시하고
[Hosted full 36324864466](https://github.com/progresshans/godj/actions/runs/36324864466)을 명시적으로 요청했다.
같은 code source의 [Hosted Fast 36324857281](https://github.com/progresshans/godj/actions/runs/36324857281)은
실제 `Fast Go feedback` step까지 성공했다. Run/jobs/steps 원본을 broad checkpoint의 `hosted/`에 보존했다.
이 기록 시점에는 전체 platform/process 완료가 아니다. 실행·artifact/source binding 확인이 남아 있으며 이전 `63b07213` 결과를 전이하지 않는다. Reset·전체 UserCreationForm과 다른 인증 provider도 별도 미완료 범위다.

로컬 raw/receipt는 `godj-many-to-many-reference-4sl0bvdp` 아래 다음에 보존했다.

- `account-reference-1790515981434462000`: 실제 SQLite/PG native 관찰·runner·upstream hash·cleanup receipt.
- `account-consumer-checkpoint-1790517209523226000`: 세 모드 전체 event stream·필수 목록·source map·10개 controls·vet·delta 감사.
- `account-entry-checkpoint-1790517850665452000`: 후속 거부 테스트 세 모드·필수 inventory·source 동일성·private PG cleanup.
- 최초 실패: `account-consumer-checkpoint-1790516804347770000`, `1790516909169572000`, `1790517061459893000`.

## GDJ-0100 — 자기 비밀번호 변경의 원자 저장 기반

2026-09-27, `fe033b3c` 이후 non-Markdown **2,481 파일**의 source map
`452298239456d9ac4b19b8a26b287ed4f036b5ca27b6e3012a4e0e72ace616c3`를 고정하고 시작/종료 동일성을 확인했다.
고정 principal ID의 old password 확인, profile policy·hash의 scope 분리와 현재 credential/session fence,
password·현재 revision·현재 session 회전·다른 session 폐기·본인 audit의 원자 저장을 구현했다.
현재 profile/권한/last_login과 session payload·absolute lifetime을 보존한다. 새 표·IR·migration/생성 ABI 변경은 없다.
구현 commit `26f590e4593423dedeeee686eb89b4979912a424`를 두 branch에 게시했고, 같은 source의
[Hosted Fast 36320774913](https://github.com/progresshans/godj/actions/runs/36320774913)은 실제 Fast Go feedback까지 성공했다.
이 결과는 전체 platform/process 검증이 아니며, 아래 최종 checkpoint 디렉터리에 run/jobs/steps 원본도 보존했다.

- 일반 사용자에게 관리 권한을 요구하지 않는다. 260개 session의 batch 경계에서 target만 폐기하며 다른 사용자/익명 행의
  bytes를 보존한다. 실제 양 DB 재접속, 같은 password의 새 hash/stamp/ID, raw 공백, read-only old confirmation을 검사했다.
- callback zero/nil/twice/swallow, 비교 불가능한 host error, session insert/delete 전후, password/audit 전후·revocation 실패,
  취소·unknown rollback/commit·commit 뒤 취소와 final validator/cleanup 실패를 실제 native transaction에서 검사했다.
- hash 중 다른 연결의 profile/username/last_login 편집은 보존하고 현재 policy를 다시 적용한다. Password/active/삭제,
  현재 session logout/rotation과 idle/absolute 만료는 거부하며 만료 cleanup도 rollback한다. 두 연결의 동시 변경은 하나만 성공한다.
- old-ID entropy·실제 다른 session ID 충돌은 확정 rollback 뒤 새 entropy로 처리하고 hash는 한 번만 실행한다.
  충돌 한도·entropy 실패는 상태를 보존하며, unknown collision은 두 번째 ID를 소비하거나 transaction을 재시도하지 않는다.
- 준비 값의 validator slice 소유권·다른 owner 거부·다른 principal/stamp/session 거부와 fmt/JSON 비공개를 검사했다.
  Web capability는 명시적으로 정확한 manager에 결합하며 typed-nil/누락을 구분한다. 실제 HTTP/cookie jar의 성공·CSRF·잘못된 입력·
  anonymous/중복 cookie·rollback/unknown, 새 cookie만 게시·기존 CSRF 유지·old/other session 거부를 검사했다.
- Auth의 wrapped hasher 실행 오류를 invalid credentials로 낮추지 않는다. 외부 password provider가 원인이 있는 validation을
  반환해도 Web은 실행 오류 wrapper를 유지한다. 취소·unknown 원인은 errors.Is로 보존하고 private 원문과 cookie를 게시하지 않는다.

| 모드 | 최종 영향 scope | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|---|
| normal | 10 packages / 180 roots / 794 필수 항목 | 1,115 PASS / skip 0 | 16.223초 |
| race | 10 packages / 180 roots / 794 필수 항목 | 1,115 PASS / skip 0 | 79.404초 |
| CGO=0 | 10 packages / 180 roots / 794 필수 항목 | 1,115 PASS / skip 0 | 15.653초 |

총 **3,345 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly Go,
`TZ=Pacific/Chatham`, `GODJ_REQUIRE_POSTGRES=1`, private PostgreSQL 17.10 UTF8/libc/C에서 실행했다.
Core는 auth/identity/sessions/systemstate/Web sessionauth/API sessionauth/Identity API·Admin의 전체 tests,
DB는 양쪽 자기 password change의 service·failure·경쟁·HTTP runtime roots다. 필수 항목 run/pass와 package 종료를 감사했고
private DB 종료 `0|0|0`과 container 제거를 확인했다. 제품 Form·JSON/OpenAPI·독립 generated client는 아직 연결 전이다.
이 테스트용 HTTP adapter를 완성된 제품 화면/API 검증으로 집계하지 않는다.

선행 source `0e439fce00b96b2fee09cbbb0973d05c156e031d9d31ac5788f19808dbee4ae0`에서는 같은 core와
**양 DB Identity 전체**를 실행했다. 10 packages / 216 roots / 1,662 필수 항목에서 각 mode **1,991 PASS / skip 0**,
총 5,973 PASS였다. 이후 delta는 새 password 서비스/Web의 caused-validation 분류와 추가 보안 test/필수 inventory의
8개 non-Markdown 파일이다. 그 영향 경로를 위 final checkpoint에서 다시 실행했으며 두 source를 같은 실행으로 합치지 않는다.

제품에 포함한 독립 고정 Django 6.1/CPython 3.14.3 observer를 private PostgreSQL에서 다시 실행했다.
SQLite·PostgreSQL fixture와 9개 upstream module hash가 일치하고 Python tests **2개**·runtime mutation **3개**가 성공했다.
이 Python 입력 4개 파일은 선행/최종 source에서 byte-identical함을 supplemental receipt로 확인했다.
Django의 lazy other-session 폐기, session 저장 실패의 부분 상태, stale form.save()의 password/active 덮어쓰기를 그대로 남기고
Go의 의도적인 원자 저장·현재 상태 재검사를 ADR-0076에 구분했다.

최종 source에 Go overlay **8개**를 적용해 credential fence·다른 session 폐기·last_login 불변·현재 revision·rollback·unknown 분류·
wrapped verifier 오류·caused validation wrapper 누락을 각각 지정된 assertion으로 탐지했다. Compile 실패를 탐지 성공으로 세지 않았다.
영향 vet·CI 도구 **41개**·gofmt·문서 151개 링크·diff도 통과했다. 전체 platform/process는 기존 `63b07213`의 결과를 전이하지 않고,
제품 소비자까지 연결한 다음 credential lifecycle 통합 milestone에서 소유한다.

최초 실패도 보존했다. HTTP fixture가 대소문자가 다른 CSRF header map 키를 중복/유지해 세 사례가 실패했고 Header.Set으로 정리했다.
추가 검토에서 caused validation의 취소가 공통 sessionFailure에서 원형으로 반환되어 입력 거부로 재노출되는 것을 재현했다.
새 password 경로는 명시적인 실행 wrapper를 유지하도록 수정했다. 첫 guard만 적용한 중간 실패도 PASS에서 제외했다.

- 선행 Identity 전체 checkpoint: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-core-checkpoint-1790512745062889000/receipt.json`
- 최종 영향/소스: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-final-checkpoint-1790513528104779000/receipt.json`
- 입력 동일성·vet/참조: 같은 디렉터리의 `supplemental.json`, `controls/receipt.json`
- 승격한 observer의 PostgreSQL 재실행/cleanup: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-promoted-reference-1790512841241787000/receipt.json`
- 최초 HTTP fixture 실패: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-core-checkpoint-1790512602525515000/receipt.json`
- Caused-validation baseline: 선행 Identity 전체 checkpoint의 `caused-validation/before.jsonl`
- 중간 caused-cancel 실패: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-final-checkpoint-1790513382825934000/receipt.json`


## GDJ-0100 — 사용자 정의 로그인 오류의 비교 경계

저장 로그인의 마지막 검토에서 slice 기반 host admission 오류를 반환하면 `error == error` 비교가 panic하는 것을 재현했다.
알려진 인증 거부 sentinel 또는 세션 오류 pointer만 비교하도록 바꿨다. 임의 host error는 실행 오류로 정규화하고
정상 거부/재시도로 낮추지 않으며 User/session/audit의 정확한 rollback을 유지한다.

2026-09-27, non-Markdown 2,466 파일의 source map
`968541a6bed7a589d32ed5f71937b3113cab554ab976034ae4201740fe2a13b2`에서 시작/종료 동일성을 확인했다.
Auth/Session/Systemstate/Web sessionauth 네 package와 SQLite/PostgreSQL Login lifecycle/boundary 네 root를 실행했다.
**6 packages / 145 roots / 245 필수 항목**, normal·race·CGO=0 각각 **489 PASS / skip 0**, 총 **1,467 PASS**다.
각 모드 시간 합계는 11.859 / 48.637 / 14.771초다. 앞 절과 같은 Darwin arm64/Go 1.26.5, offline readonly,
Pacific/Chatham·private PostgreSQL 17.10 환경이며 종료 `0|0|0`과 container 제거를 확인했다.
변경 전의 동일 테스트는 `comparing uncomparable type identitytest.loginOpaqueError` panic으로 실패했다.
이 재현과 변경 후 실행을 분리해 보존한다. 수정 commit `94d0229b813c0d7a6c3b31ebf51636e55f77e1ad`의
[Hosted Fast 36316637831](https://github.com/progresshans/godj/actions/runs/36316637831)은 실제 Go feedback까지 성공했다.
이전 `63b07213`의 Hosted 전체 실행은 이 추가 오류 수정의 전체 검증으로 전이하지 않는다.

- 재현: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/login-error-comparison-1790509140115594000/before.json`
- 수정 검증: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/login-error-comparison-checkpoint-1790509213803444000/receipt.json`


### 후속 자기 비밀번호 변경의 독립 관찰 준비

제품 구현 전의 참조 초안을 private SQLite/PostgreSQL 17.10에서 실행했다. 고정 Django 6.1의 실제
PasswordChangeView/PasswordChangeForm과 auth/session middleware를 사용하며 오류 렌더링만 JSON으로 관찰한다.
두 DB와 hashseed 0/817의 결과가 같고, 아홉 upstream source hash를 기록했다. 원래 비밀번호·공백·confirmation·strength,
현재 세션 회전/데이터 보존·다른 세션의 lazy 무효화·last_login 불변, 같은 비밀번호 재설정, 저장 실패/검증 뒤 변경을 관찰했다.
세션 회전 누락·상수 session hash·변경 시각의 잘못된 갱신을 넣은 세 runtime mutation도 지정된 관찰 변화를 탐지했다.
실패 hook은 실제로 한 번 실행됐는지 확인한다. Django form의 전체 User 저장이 동시 비활성/비밀번호 변경을 덮어쓰는 결과와
세션 저장 실패의 부분 상태를 그대로 기록했다. Go의 원자 저장·현재 상태 재검사와 구분할 참조이며 Go 제품 구현/검증 완료가 아니다.

- 초안·양 DB 관찰·동일성/cleanup receipt: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-reference-1790508448796614000/receipt.json`
- Runtime control: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-reference-1790508448796614000/controls.json`
- 보존한 runner: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/self-password-reference-1790508448796614000/runner.py`
- 다음 구현 메모: `/tmp/godj-self-service-next.md`


## GDJ-0100 — 저장 로그인 관찰과 원자 세션 수립

2026-09-27, `2f44a1ab` 이후 non-Markdown **2,466 파일**의 source map
`00993309981c73bf13f4e046fbadd2dd2399a1d16bfc9d1cb625bb1ce3df1b7f`를 고정했다.
저장 Identity의 last_login·세션 수립과 현재 credential/admission을 같은 native coordinated transaction에 연결했다.
관찰은 관리 revision/audit를 늘리지 않는다. 계정 교체는 이전 payload와 lifetime을 버리는 원자 Replace를 사용한다.

- 양 DB에서 실제 HTTP 인증만 수행한 경우, 잘못된 비밀번호/nonstaff 거부, 익명 payload 보존, 재로그인·계정 교체·접근·logout,
  UTC microsecond와 revision/audit 보존, reopen, 실제 Admin 읽기 전용 표시·API와 입력 위조를 검사했다.
- Create/Rotate/Replace의 callback zero/nil/twice/swallow, insert 전후·delete 후·last_login 쓰기 전후 실패,
  취소·unknown rollback/commit·commit 뒤 취소를 실제 native transaction에서 검증했다. Unknown에는 결과를 게시하거나 재시도하지 않는다.
  읽기 뒤 만료된 세션의 cleanup도 로그인 실패와 함께 rollback한다.
- 인증 뒤 password/active/staff/grant/삭제 변경을 마지막 fence에서 거부한다. 다른 runtime의 profile 편집과 동시 로그인은
  시각과 관리 revision을 모두 보존한다. 비밀번호 변경과 동시 로그인은 stale 신규 세션을 남기지 않는다.
  웹 binding은 정확한 manager, typed-nil 거부, 불완전/다른 ID/stamp 결과의 cookie 비게시와 현재 Principal 게시를 검사했다.
- Store.Replace의 lifetime/소유 데이터·충돌·누락·만료·취소와 Manager의 source 공유·Peek 무변경을 검증했다.
  wrapper에 포함된 collision/capacity 원인이 unknown 오류를 지우거나 재시도를 유도하지 않는지 확인했다.
- 두 독립 Session/Bearer 생성 client는 실제 HTTP 로그인 뒤 User/UserSummary의 nullable last_login을 소비한다.
  부모는 reopen한 DB의 정확한 시각·revision·credential/session/audit를 확인한다. 기존 다섯 OpenAPI profile의 generated drift,
  독립 module build·실제 HTTP 소비가 세 mode에서 모두 성공했다. Schema IR/모델 migration 변경은 없다.

| 모드 | 필수 scope | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|---|
| normal | 15 packages / 315 roots / 1,657 필수 항목 | 2,259 PASS / skip 0 | 83.645초 |
| race | 15 packages / 315 roots / 1,657 필수 항목 | 2,259 PASS / skip 0 | 342.605초 |
| CGO=0 | 15 packages / 315 roots / 1,657 필수 항목 | 2,259 PASS / skip 0 | 92.277초 |

총 **6,777 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly Go,
`TZ=Pacific/Chatham`, `GODJ_REQUIRE_POSTGRES=1`에서 scope를 `go test -json -count=1 -p=2 -timeout=20m`과 각 mode로 실행했다.
필수 run/pass와 package 종료를 감사했고 private PostgreSQL 17.10 UTF8/libc/C의 종료 `0|0|0`·container 제거를 확인했다.
Normal DB와 소비자는 앞서 통과한 결과를 재사용했다. 그 뒤의 차이가 각각 소비자 테스트 파일/systemstate의 테스트 전용 DSN,
그리고 테스트 전용 DSN뿐임을 source map으로 확인했다. 해당 package의 실행 입력은 동일하며 재사용 receipt를 구분한다.
Final checkpoint의 시작/종료 source map도 동일하다. Race와 CGO=0은 이 final source의 모든 선택 scope를 실행했다.

독립 고정 Django 6.1/CPython 3.14.3 observer를 SQLite와 private PostgreSQL에서 실행해 9개 upstream module hash와
일반 lifecycle·실패·인증 뒤 변경을 기록했다. 참조 테스트 **2개**, runtime mutation **3개**가 통과했다.
Signal 누락, Authenticate의 잘못된 시각 갱신, 계정 교체 뒤 payload 잔존을 지정한 관찰 변화로 탐지한다.
Django의 재로그인 ID 유지·시각만 남는 저장 실패·다음 요청의 stale credential 거부는 fixture에 그대로 남기고
GoDj의 의도적인 차이를 ADR-0076/DEV-0013에 명시했다. 부작용까지 같은 동작이라고 집계하지 않는다.

Go compiler overlay **6개**는 틀린 시각, 계정 간 payload 유지, credential fence 누락, rollback 누락,
unknown 분류 누락, 관리 revision 증가를 각각 해당 assertion으로 탐지했다. Compile 실패는 통과로 인정하지 않는다.
영향 vet, CI 도구 **41개**, gofmt·문서 링크·diff 검사도 통과했다. Python/control의 실행 입력은 final source와 동일함을
별도 receipt로 확인했다. Process worker와 양 DB의 global Article 재시작 검사는 로그인 HTTP 시간 구간과
이후 시각 불변성을 검증하도록 보강했으며 로컬 compile만 확인했다. 실제 process/platform 실행은 아래 통합 milestone이 소유한다.

최초 실행의 실패는 보존했다. 삭제 fixture가 relation 정책 없이 raw delete를 호출한 점은 실제 Manager.DeleteUser 경로로 고쳤다.
기존 복구 테스트의 nil last_login 기대는 실제 로그인 시각을 정확히 확인한 후 다른 모든 field를 비교하도록 보강했다.
독립 client의 잘못된 generated detail 응답 타입/변수 충돌도 수정했다. 이어 race 실행에서 중복 subtest의 `#`가
SQLite URI fragment로 해석돼 TempDir 밖 DB가 재사용된 것을 확인했다. Fixture DSN을 URL 인코딩하고 해당 32 KiB DB를
실패 checkpoint에 보존했으며 공통 scope를 normal/race/CGO=0에서 다시 통과했다. 실패 실행은 PASS 합계에 포함하지 않았다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/login-lifecycle-checkpoint-1790507318155416000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/login-lifecycle-checkpoint-1790507318155416000/supplemental.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/login-lifecycle-reference-1790505710776084000/receipt.json`

이번 저장 로그인·세션 수립 통합 milestone은 새 source의 Hosted 전체 platform/cold-build/process 검증을 소유한다.
구현 commit `63b07213ecaaea37a2270b33d8c806a982817269`를 두 branch에 게시했다.
같은 source의 [Hosted full 36315320971](https://github.com/progresshans/godj/actions/runs/36315320971)을 명시적으로 dispatch했다.
2026-09-27, attempt 1의 **62 jobs 전부 성공**과 최종 `full_platform_verified=true`, **8개 실행 owner**를 확인했다.
최종 결과는 portable Go, exact Darwin, Python compatibility, command product, project check,
PostgreSQL product, relation product, conformance validation을 포함한다. 실제 재시작/process 실행도 이 source에서 완료됐다.
Systemstate PostgreSQL two-process와 operator global external capture의 archive/payload 해시·producer job/attempt,
source `63b07213`의 Git blob에서 재계산한 binding을 독립 검증했다. 현재 작업 디렉터리의 bytes로 대체하지 않았다.

- 통합 receipt·최종 로그·run/jobs·capture: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/hosted-full-36315320971-1790509633532881000/receipt.json`
- Systemstate binding: 568 files / 5,896,951 bytes, `8e96dce53562f89536fa7fbbeb841eb58a8543f15acd3a8be815e12d2b50def4`
- Operator binding: 645 files / 5,742,277 bytes, `4140a30311c17d65d126897166820df46dc41078567cd78dac4f5fccd07f1420`

이후 `94d0229b`의 오류 수정과 자기 비밀번호 변경에는 이 결과를 전이하지 않는다.
Code Fast 36315317548은 후속 문서 push로 자동 취소됐다.
문서 source `1ed565b2`의 [feedback 36315395506](https://github.com/progresshans/godj/actions/runs/36315395506)은 Go step을 skip한 성공이며 구현 Fast 성공으로 합산하지 않는다. 로컬 전체 검증을 추가로 중복 실행하지 않았다.
이전 `f3264aef`의 Hosted full을 이 변경의 전체 성공으로 전이하지 않는다.


## GDJ-0100 — 현재 password 상태의 Admin/API 표현

2026-09-27, `b162f001` 이후 non-Markdown **2,452 파일**의 source map
`290a151194999e32afe59ca0da74132903002170d8a73a7b73c17f48137703f4`에서 시작/종료 동일성을 확인했다.
Profile의 password 사용 상태를 동일한 User 행에서 계산하고, Admin 편집/view-only 상세와
관리 API의 User/UserSummary·두 독립 생성 client에 연결했다. 저장 필드나 추가 DB 조회·hash 작업은 넣지 않았다.

- 활성 상태와 비밀번호 사용 상태를 분리한다. 고정 Django의 usable → disabled → inactive/disabled → inactive/restored → active 전이를
  SQLite/PostgreSQL에서 독립 관찰하고 GoDj의 Directory·Manager·API 상세/목록·Admin 및 검증 오류 재표시와 비교했다.
  입력 위조 거부, 현재 권한과 view-only, raw/encoded password 비노출·revision 보존을 실제 HTTP/DB에서 확인했다.
- 공통 serializer의 computed field는 명시적 read-only 값과 출력 Spec을 소유한다. 모델/관계 이름 대체와 중복 거부,
  원본/형제 projection의 독립성·동시 reuse, 필수 값·type/null·Unicode 길이·decimal precision/scale와 부분 출력 금지를 검사했다.
  Admin의 순수 snapshot display는 설정/descriptor 소유권·input 충돌·HTML escaping·값 한도·실패/취소·생성 화면 제외를 검사했다.
- 실제 exporter에서 Identity Session/Bearer의 두 OpenAPI 문서와 네 generated Go 파일을 다시 생성했다.
  나머지 세 문서와 module/generator lock은 동일하다. 각 모드에서 다섯 profile 전체의 generated drift·독립 module build·실제 HTTP와
  부모의 SQLite credential/session/audit 재조회가 성공했다. 두 응답형의 boolean true/false 및 누락/null/숫자/문자열 거부도 확인했다.

| 모드 | 필수 scope | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|---|
| normal | 13 packages / 239 roots / 1,481 필수 항목 | 4,102 PASS / skip 0 | 75.243초 |
| race | 13 packages / 239 roots / 1,481 필수 항목 | 4,102 PASS / skip 0 | 324.869초 |
| CGO=0 | 13 packages / 239 roots / 1,481 필수 항목 | 4,102 PASS / skip 0 | 81.287초 |

총 **12,306 PASS / skip 0**이다. Darwin arm64 / Go 1.26.5, offline readonly Go,
`TZ=Pacific/Chatham`, `GODJ_REQUIRE_POSTGRES=1`에서 선택한 roots를 `go test -json -count=1 -p=2 -timeout=20m`과
각 mode로 실행했다. 공통 Auth/Serializer/Admin·Identity/API/OpenAPI, 양 DB의 Identity 전체, 독립 client·Article·Helpdesk 소비자가 scope다.
필수 run/pass와 실제 종료를 감사했다. PostgreSQL 17.10은 앞 단계와 같은 pinned UTF8/libc/C image이며
종료 `0|0|0`과 private container 제거를 확인했다. 전체 platform/cold/process 검증을 뜻하지 않는다.

고정 Django 6.1/CPython 3.14.3의 참조 테스트 **2개**, runtime mutation **7개**와 Go compiler overlay **7개**가 통과했다.
Go control은 active와 password 상태 혼동, API 상수 상태, 공유 computed registry, 숨긴 model field 대체,
Admin 검증 오류의 표시 누락·read-only 입력 허용·HTML 신뢰를 각각 지정된 assertion으로 탐지했다. Compile 실패는 성공이 아니다.
영향 vet와 CI 도구 41개, gofmt·문서·diff 검사도 통과했다.

첫 checkpoint는 새 테스트의 `name=` 선택자가 `data-field-name=`까지 입력으로 오인해 실패했다.
두 번째는 기존 Article 검사가 모든 `password` 부분 문자열을 금지해 새 public boolean도 거부했다.
정확한 입력 속성과 private JSON key를 검사하도록 수정했고 원문/저장 인코딩 비노출 검사는 추가로 유지했다.
두 실패 receipt는 보존하고 PASS에 합산하지 않았다. CI 도구의 최초 잘못된 discovery 경로는 미실행이며 올바른 경로의 41개 실행만 인정했다.
최종 source에서 세 mode의 전체 선택 scope를 실행했다. 앞서 통과한 독립 Python/control 입력은 source map으로 동일성을 확인했다.
그 후의 차이는 해당 control 범위 밖의 Go 테스트 선택자/Article 응답 검사뿐이다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/password-status-checkpoint-1790502220586349000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/password-status-checkpoint-1790502220586349000/supplemental.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/password-status-checkpoint-1790502050533610000/controls/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/password-status-checkpoint-1790502050533610000/boundary-controls/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/unusable-password-reference-1790501836267029000/receipt.json`

구현 `8316b27a4acbeb56a6935fe77114cc434a9f7a51`의 [Hosted Fast 36310871626](https://github.com/progresshans/godj/actions/runs/36310871626)는
실제 Fast Go feedback까지 성공했다. 종료 기록의 Markdown-only 변경은 위 non-Markdown source를 유지한다.
기반 `b162f001`의 [Hosted Fast 36308476130](https://github.com/progresshans/godj/actions/runs/36308476130)도 별도로 성공했다.
이번 변경의 새 Hosted 전체 실행은 후속 lifecycle 통합 milestone이 소유한다.
`last_login`, self-service password/reset·전체 UserCreationForm과 다른 인증 provider는 미완료다.

## GDJ-0100 — Admin 생성의 사용 불가 password 선택

2026-09-27, 구현 `f1801d2f` 이후 non-Markdown **2,446 파일** source map
`bc5436872b7999f9cd0f9e035c52878b80d422ca2f34496796fa852cb406a430`의 시작/종료 동일성을 확인했다.
Admin 생성의 checkbox는 기본 password 생성, 명시적 선택만 사용 불가 생성이다.
두 private password field의 required/confirmation을 선택에 맞게 적용하며, 사용 불가 선택은 raw input과 host password 정책을 사용하지 않는다.
Username·현재 add/change 권한·감사 transaction·CSRF·실패/unknown과 field 자원 제한은 유지한다.

고정 Django AdminUserCreationForm의 8개 관찰을 SQLite/PostgreSQL에서 다시 얻었고 upstream `forms.py`를 포함한 5개 source hash를 기록했다.
기본/명시적 enabled의 빈 입력, mismatch/equal, disabled의 빈/mismatch/equal, case-insensitive duplicate를 실제 GoDj Admin HTTP·DB 결과와 비교했다.
Host 정책 분리, 현재 권한 회수, 누락 CSRF, unknown commit/감사 rollback과 무재시도도 확인했다.
Python 테스트 2개와 6개 runtime mutation, Go compiler overlay의 4개 negative control이 통과했다.
Go control은 항상 required인 field, disabled 상태의 confirmation 강제, disabled 요청의 raw password 사용,
기본 생성의 required 진단 누락을 지정된 HTTP/reference assertion으로 탐지했다. Compile 실패는 성공이 아니다.

| 모드 | 필수 scope | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|---|
| normal | 5 packages / 76 roots / 602 필수 항목 | 687 PASS / skip 0 | 15.414초 |
| race | 동일 | 687 PASS / skip 0 | 93.464초 |
| CGO=0 | 동일 | 687 PASS / skip 0 | 18.231초 |

Darwin arm64 / Go 1.26.5, SQLite·PostgreSQL 17.10, offline readonly Go와 `TZ=Pacific/Chatham`에서
`go test -json -count=1 -p=2 -timeout=20m -run <선택한 roots>`와 각 mode를 실행했다. 총 **2,061 PASS / skip 0**이다.
Identity Admin·공통 Admin, 양 DB의 관리 Admin/password 정책/사용 불가 HTTP, Article composition이 해당 scope다.
필수 run/pass/종료를 감사했으며 PostgreSQL은 앞 단계와 같은 pinned UTF8/libc/C image를 사용했다.
종료 상태 `0|0|0`과 container 제거를 확인했다. 영향 vet·CI 도구 41개·gofmt·문서·diff 검사도 통과했다.
실제 exporter의 OpenAPI 5개 문서가 기존 bytes와 같아 generated client/model drift의 새 실행은 비대상이다.

첫 checkpoint의 기존 form 참조 검사가 optional 생성 field에 이전 confirmation callback만 붙여 required 세 사례를 놓쳤다.
테스트를 실제 생성의 conditional validator에 연결하고 고정 Django 기대는 유지했다.
이 최초 실패 source/receipt는 보존했으며 PASS로 세지 않았다. 갱신한 source에서 전체 선택 scope를 다시 실행했다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/admin-password-creation-checkpoint-1790499513285745000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/admin-password-creation-checkpoint-1790499513285745000/controls/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/admin-password-creation-checkpoint-1790499513285745000/supplemental/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/unusable-password-reference-1790499360238206000/receipt.json`

현재 password 상태의 Admin/API 표시·last_login·self-service/reset과 전체 UserCreationForm은 남아 있다.

## GDJ-0100 — 관리 소비자·입력 경계 Hosted 전체 통합 완료

2026-09-27, source **`f3264aeffce3c6a07ea07bb3a41097edf26ce15a`**, attempt 1의
[Hosted full 36305013585](https://github.com/progresshans/godj/actions/runs/36305013585)이 완료됐다.
**62 jobs 전부 success**, 최종 `CI result (full)`은 `scope: full`, `full_platform_verified: true`이며
command·conformance·exact Darwin·portable Go·PostgreSQL·외부 project·Python compatibility·relation의 **8개 owner**를 모두 확인했다.
선택된 platform/normal/race/CGO=0·cold/process·고정 참조 검사를 포함하며 로컬 전체를 중복 실행하지 않았다.

| 같은 실행의 불변 capture | Artifact ID | Producer job ID |
|---|---|---|
| systemstate-postgres-1 | 10927027844 | 108580016909 |
| operator-postgres-1 | 10926129760 | 108580016889 |

두 archive의 GitHub SHA256, repository/run/attempt/head와 producer 성공, 실제 payload SHA256·SHA256SUMS·provenance를 확인했다.
현재 변경된 checkout을 검증 source로 가장하지 않고, 신뢰된 GitHub 실행 identity를 verifier에 전달했다.
그 verifier 코드가 `f3264aef`와 같음도 확인했다. 내부 source binding은 해당 commit의 Git blob/mode로 별도 재계산했다.
System-state는 561 files / 5,849,985 bytes / `10a9d9016d02d90ed869f1581442d98aa36b22f8d9c073f1e7e82b9151545c6c`,
operator는 638 files / 5,695,466 bytes / `b28679696f65e738fb390d6374ad4ff45fcffbb3a1370e461725084616602984`로 capture와 정확히 일치했다.

종료 감사 receipt:
`/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/hosted-full-36305013585-1790499733433562000/receipt.json`

이 전체 성공은 이후의 unusable password/Admin 생성 변경으로 전이하지 않는다.
후속 구현 `f1801d2fa5322f2a4661c5d727ffcd032e5dcce5`의
[Hosted Fast 36307184357](https://github.com/progresshans/godj/actions/runs/36307184357)는 실제 Fast Go feedback까지 성공했다.
그 구현의 새 범위는 아래 로컬 checkpoint와 구분한다.


## GDJ-0100 — 사용 불가 password의 credential와 관리 command

2026-09-27, `750632b9` 이후의 변경을 고정한 non-Markdown **2,445 파일** source map
`0e3ccc1863171e29a8a7aefd8344113185bc9eeb4f4a92574f0ea59030cee5ee`에서 실행했다.
실행 시작/종료 source가 동일하다. 이 범위는 새 credential/관리 command의 영향 검증이며 전체 framework/플랫폼 검증이 아니다.

- `MakeUnusablePassword`의 새 256-bit marker, opaque Credential/Account의 사용 가능 상태,
  Memory/StoredAuthenticator의 한 번의 dummy 검증과 무조건 거부, 현재 active identity 조회를 검증했다.
  Marker를 raw password로 제출하거나 dummy password가 일치해도 로그인하지 않는다.
  일반 encoded envelope·손상 hash의 실행 오류·취소·비공개 entropy 실패와 hash 도중 변경도 검사했다.
- Manager의 사용 불가 생성·설정·반복 설정과 기존 SetPassword 복구는 현재 인가·revision·credential fence,
  대상 session만 폐기·값 없는 audit·rollback/unknown을 같이 검증했다. 사용 불가 요청은 raw password 정책과 hasher를 호출하지 않는다.
  양 DB 두 연결의 disable/replace 경쟁, grant 회수·inactive·revision 및 revision 없는 hash 교체,
  callback contract 위반과 부분 실패·확정 commit 뒤 취소를 포함한다.
- 실제 Session API/Admin에서 required explicit null과 확인 checkbox, 누락/빈 값/잘못된 타입,
  CSRF·stale/missing revision, 정책 분리·반복 설정·자기 세션 폐기와 복구를 실행했다.
  현재 stamp로 이미 수립된 다른 인증 수단의 세션은 동작하고, 다시 설정하면 폐기되는지도 HTTP로 관찰했다.
- 실제 OpenAPI에서 Session/Bearer 두 client를 고정 ogen으로 다시 생성했다. 세 모드 모두 별도 module의 drift·build·HTTP 실행과
  부모의 SQLite 재조회·현재 credential·정확한 revision/session/audit를 통과했다.
  Client는 null 생성·반복 disable·복구와 Bearer disable을 호출한다. Wire 검사는 null/빈 문자열/공백 보존을 구분하며 unknown을 재시도하지 않는다.
  Nullable string에 대한 현재 생성물에는 별도 PasswordReplacement.Validate 메서드가 없다. 서버의 required/length/type 검사를 유지하며
  생성 client가 모든 입력을 미리 검증한다고 주장하지 않는다. 모델 IR/생성 model ABI는 변경하지 않았다.

| 모드 | 필수 scope | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|---|
| normal | 11 packages / 194 roots / 1,458 필수 항목 | 1,687 PASS / skip 0 | 71.393초 |
| race | 동일 | 1,687 PASS / skip 0 | 290.175초 |
| CGO=0 | 동일 | 1,687 PASS / skip 0 | 71.571초 |

Darwin arm64 / Go 1.26.5, `go test -json -count=1 -p=2 -timeout=20m -run <선택한 roots>`와
각 mode의 `-race`/`CGO_ENABLED=0`, `GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`, offline readonly Go 환경이다.
선택된 package와 필수 root/subtest의 실제 run/pass, 종료와 no-skip을 감사했다. 전체 합은 **5,061 PASS / skip 0**이다.
PostgreSQL 17.10은 UTF8/libc/C의 고정 image
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`를 사용했다.
종료 시 public table/잔여 godj schema/추가 연결은 `0|0|0`이며 private container 제거를 확인했다.

고정 Django 6.1/CPython 3.14.3의 독립 observer를 SQLite 3.50.4와 PostgreSQL 17.10에서 실행해 관찰과 네 upstream source hash의 일치를 확인했다.
Python 테스트 2개는 hash seed 반복과 양 DB fixture 비교, 상수 marker·상수 stamp·dummy work 제거·password-only resolution의
4개 runtime mutation 탐지를 통과했다. PostgreSQL 최초 시도는 선택한 환경에 psycopg가 없어 중단됐으며 성공에 포함하지 않는다.
고정 `psycopg[binary]==3.3.6` 환경을 준비한 뒤 다시 실행했고 양 DB의 임시 table과 PostgreSQL container를 정리했다.

별도 compiler overlay의 Go negative control **7개**도 모두 지정된 test assertion으로 실패했다.
Dummy 일치 admission, marker 직접 검증, 고정 marker, password-only Resolve, session 폐기 제거,
최종 credential fence 제거, 감사 제거를 탐지했다. Compile 실패를 탐지 성공으로 세지 않았고 원본 source는 바꾸지 않았다.
영향 vet, CI 도구 41개, gofmt·문서·diff 검사도 통과했다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/unusable-password-checkpoint-1790498018374018000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/unusable-password-checkpoint-1790498018374018000/controls/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/unusable-password-checkpoint-1790498018374018000/supplemental/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/unusable-password-reference-1790497734454983000/receipt.json`

`f3264aef`의 기존 Hosted full 실행은 이 변경 이전 source의 검증이다. 새 password command에 그 결과를 전이하지 않는다.
새 범위의 전체 platform/cold-build는 GDJ-0100의 후속 lifecycle 소비자를 연결한 통합 milestone이 소유한다.
Admin 생성의 사용 불가 선택·현재 password 상태 표시, last_login·self-service/reset과 전체 UserCreationForm은 남아 있다.


## GDJ-0100 — Linux PTY 에코 관찰과 Python 호환성 fingerprint 후속 수정

2026-09-27, `c7a0c5e0`의 [Hosted full](https://github.com/progresshans/godj/actions/runs/36303289415)에서
Linux PTY overflow 검사가 완전한 에코가 도착하기 전의 조각만 수집했고, 네 Python 호환성 job의 최종 합산 fingerprint가 이전 GoDj 결정을 사용했다.
이 실행은 전체 PASS가 아니며, 수정된 source의 새 전체 실행이 시작되면서 미완료 job은 취소됐다.

- PTY의 복원 후 canonical 입력 확인과 master 출력 전달은 별개다. 네 복원 probe가 기존의 5초 제한 marker reader로
  완전한 에코를 기다리고 그 앞의 모든 bytes도 보존하도록 바꿨다. 입력 queue의 정확한 sentinel과 secret 비노출 검사는 유지했다.
  실제 terminal restore/flush 제품 코드는 변경하지 않았다.
- Darwin arm64와 Linux arm64 / Go 1.26.5에서 10개 terminal roots와 3개 하위 사례를 각 50회 실행했다.
  normal/race/CGO=0의 각 환경에서 **650 PASS / skip 0**, 총 **3,900회 PASS**다.
  필수 root와 각 하위 사례의 run/pass 수가 모두 50인지 확인했다.
  Linux arm64는 실제 Docker PTY이며 Ubuntu amd64의 Hosted 결과로 표현하지 않는다.
  Go image는 `sha256:53eeac89074db483fdf0ab3be1df32bf6e47562263d2d0d6baa7f26acb4957dd`다.
- 별도 source copy의 Linux negative control 두 개가 지정된 assertion으로 실패했다.
  ECHO 복원 제거는 완전한 marker의 bounded timeout, input flush 제거는 queued secret이 섞인 입력 거부로 탐지했다.
  Build 실패를 탐지 성공으로 세지 않았으며 컨테이너는 모두 제거됐다.
- Django 6.1/DRF 3.18.0/CPython 3.14.3의 고정 DRF 환경에서 **311개 전체 시나리오**를 다시 관찰했다.
  SYS-023 전송 byte 한도와 SYS-028 명시적 identity 전환의 두 GoDj 결정만 이전 값으로 대체하면
  기존 1,081,069 bytes / `92bd2eb410e09ca3d046b0ca048c723376c3bfa55a9eb82f25276adc88be3450`가 정확히 재현된다.
  다른 관찰의 변경을 새 checksum으로 덮지 않았다. 현재 payload는 **1,081,175 bytes**,
  SHA256 `e162bcfe0695bc2a56f37387d2c346cdbc472b706e4f8b21abc7c6ed629e6a3f`다. 시나리오 수 311은 유지한다.
- Workflow의 고정 length/hash 두 값만 갱신하고, 그 step의 Python body를 그대로 추출해 고정 DRF 환경에서 실행했다.
  갱신된 검사와 CI 도구 41개, 영향 vet·gofmt·diff 검사를 통과했다.
  첫 관찰 시도는 Django 전용 환경에 DRF가 없어 중단됐으며 성공으로 세지 않았다.

터미널 실행/controls의 non-Markdown source는 `0e69b2a14d54034cbc4ca546e28f59b916457a5b097d047d365da6debfe7ace9`다.
최종 source는 `2251864890e634b0e982f78eb918a3fd018ed975fe28e16e06129a992d001d9b`이며 이후 차이는 `.github/workflows/ci.yml`뿐이다.
Go 제품·테스트 입력이 동일함을 source map으로 확인해 완료한 terminal 실행을 중복하지 않았다.
Workflow reference 검사는 갱신된 파일에서 별도로 실행했다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/terminal-echo-checkpoint-1790494963212670000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/terminal-echo-checkpoint-1790494963212670000/controls-receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/terminal-echo-checkpoint-1790494963212670000/scope-delta.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/python-compatibility-digest-1790495534360303000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/python-compatibility-digest-1790495534360303000/workflow-receipt.json`

후속 수정 `f3264aeffce3c6a07ea07bb3a41097edf26ce15a`을 게시했다.
같은 head의 [Hosted Fast 36304842192](https://github.com/progresshans/godj/actions/runs/36304842192)는 실제 Go feedback까지 성공했다.
확인된 실패를 모두 수정한 source의 [Hosted full 36305013585](https://github.com/progresshans/godj/actions/runs/36305013585)은 이후 전체 성공했다. 종료 감사는 위 기록을 따른다.
이 교체 실행으로 이전 `c7a0c5e0`의 실행은 cancelled로 종료됐다. 실패·취소·미완료 job은 성공으로 세지 않는다.
이후 Markdown 기록 commit과 실행 head를 구분한다. 새 password 구현은 그 전체 source 이후의 별도 검증 범위다.


## GDJ-0100 — 관리 기능의 Hosted 통합에서 발견한 소비자와 참조 증거 수정

2026-09-27, 구현 `5c2045f4a0ba5235e73bf0a0e7fac0e3aed45565`의
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36300139638)는 실제 Go feedback까지 성공했다.
같은 source의 [명시한 Hosted full](https://github.com/progresshans/godj/actions/runs/36300136646)에서는
이전 migration/config를 사용하는 통합 소비자와 참조 잠금 누락이 드러났다. 이 실행은 전체 PASS가 아니다.

- Article CLI·동시성 runner·SQLite/PostgreSQL 개발 서버와 별도 프로세스 재시작을 현재 다섯 migration에 연결했다.
  독립 기대는 정확한 key/순서·source digest와 현재 table/column/constraint/index/sequence를 확인한다.
  Credential만 읽던 로그인 재시작 검사는 User의 profile/role/revision·직접 grant·전환 receipt와 이전 credential의 비활성 tombstone도 읽는다.
  실제 HTTP 로그인, 동일 session의 재시작 후 CRUD/audit, secret 비노출, 데이터·이력 보존과 프로세스/DB 정리를 유지했다.
- 재사용 Identity 모델을 찾는 정확한 read-only `go list -mod=readonly -find -json` 호출을 Go 명령 감사에 반영했다.
  허용되지 않은 명령/인자와 누락·추가·순서가 다른 binary build는 계속 거부한다.
- SQLmigrate의 scalar backfill을 frozen SQL에 반영했다. PostgreSQL은 임시 default를 제거하며, SQLite는 행을 복사하고 sequence를 보존한다.
  실제 외부 CLI가 출력한 SQL을 별도의 SQLite에 적용해 기존 행·NULL 보존, false backfill, 영구 default 없음,
  삭제된 ID 19 뒤의 다음 ID 20을 확인한다. Renderer의 database-free·redaction·취소/자원 소유권 검사는 유지한다.
- MIG-138의 위치 인자 선언에 따른 source 영향 관찰을 설정 필드 수 비교에서 실제 외부 compiler의 arity 진단으로 바꿨다.
  완전한 positional 선언과 유효한 keyed 선언은 컴파일되고, 마지막 인자가 빠진 선언만 해당 진단으로 분류한다.
  무관한 컴파일 실패·취소는 긍정 증거가 아니며, 출력은 bounded capture와 redaction, 프로세스 그룹 정리를 사용한다.
  독립 외부 project consumer는 현재 InitialSuperuser/PasswordHasher API를 사용한다.
- SYS-023의 ADR-0056/0076, SYS-028의 ADR-0076 출처를 Python/Go 검사에 연결하고, 검토된 두 byte metrics의 잠금을 갱신했다.
  변경된 oracle 값은 frame 2,060 / username 1,024뿐이다. Django 소유의 관찰은 변경하지 않았다.
  Manifest·oracle·SHA256SUMS와 각 별도 byte-lock을 함께 확인했다. 출처 누락/변조/범위 이탈 거부도 검사한다.

최종 non-Markdown **2,433 파일** source map은
`79ffb94b3873cd12b97c522b167b138499a1b55449525873a076b29c69dfe784`다. 두 실행 chunk와 보조 검사의 시작/종료 source 동일성을 확인했다.

| 모드 | 필수 scope | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|---|
| normal | 9 packages / 164 roots | 1,032 PASS / skip 0 | 219.973초 |
| race | 8 packages / 163 roots | 1,016 PASS / skip 0 | 275.561초 |
| CGO=0 | 9 packages / 164 roots | 1,032 PASS / skip 0 | 213.427초 |

`go test -json -count=1 -p=2 -timeout=20m -run <선택한 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. 시작/종료·필수 root·package·no-skip을 검사했다.
외부 ABI 컴파일 검사는 저장소의 `!race` owner이므로 normal/CGO=0에 배치한다.
Race의 runtime·프로세스 검사는 실제 `-race` test binary로 수행하며 CLI가 빌드하는 일반 child binary와 구분한다.
전체 platform/cold-build 실행의 소유자는 Hosted full이다. 모델·생성 ABI·OpenAPI schema 변경이 없어 생성 drift 재실행은 비대상이다.

고정 Django/CPython의 system-state 테스트와 uv 0.10.12 formal reference check, 영향 vet·gofmt·문서·diff 검사를 통과했다.
Private PostgreSQL 17.10 UTF8/libc/C, image `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`를 사용했다.
각 실행 chunk의 종료 시 public table/잔여 godj schema/추가 연결은 `0|0|0`이고 container 제거를 확인했다.

수정 중에는 SQLite Integer 기대값, 옛 동시성/개발 서버 graph, SQL 실행용 테스트 driver,
임베디드 runner의 중복 import·compiler의 qualified type 진단, 별도 checksum lock을 단계적으로 바로잡았다.
최종 source의 normal과 race protocol은 완료 후 재사용했다. 뒤따른 race external 선택에서는 `!race` 때문에 테스트가 하나도 실행되지 않아
inventory가 거부했다. 이를 PASS로 세지 않고 owner를 명시한 뒤 동일 source에서 나머지를 이어 실행했다.
초기 실패와 scope 선택 오류의 receipt/log도 보존한다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-integration-repair-checkpoint-1790493198906028000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-integration-repair-checkpoint-1790493198906028000/supplemental/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-integration-repair-checkpoint-1790493198906028000/initial-selection-receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-integration-repair-checkpoint-1790493198906028000/godj-full-latest.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-integration-repair-checkpoint-1790493198906028000/godj-full-failure-audit.json`

구현 `c7a0c5e0763d7a531f52c3ea9a4bc8af276ffabb`을 게시했고,
[Hosted Fast 36303277121](https://github.com/progresshans/godj/actions/runs/36303277121)의 실제 Go feedback도 같은 head에서 성공했다. 같은 head의
[Hosted full 36303289415](https://github.com/progresshans/godj/actions/runs/36303289415)을 다시 시작했다.
이후 PTY 관찰·합산 fingerprint 실패가 발견돼 후속 수정과 새 전체 실행으로 이어졌다. 전체 PASS가 아니다.
Markdown 기록만 바꾸는 commit은 이 실행의 head와 구분한다.
마지막 성공한 Hosted 전체는 `01b67211a083c507d5e69c6be26702439aada559`의
[36253381368](https://github.com/progresshans/godj/actions/runs/36253381368)이며, 이후 기능의 전체 PASS로 전이하지 않는다.

사용 불가능한 password·last_login, self-service/reset과 프레임워크 전체 완성은 여전히 미완료다.


## GDJ-0100 — 내장 password validator와 관리 정책의 공통 적용

2026-09-27, `3a7d10a2` 뒤의 구현이다. 고정 Django의 similarity·minimum-length·common·numeric validator와
명시적인 `DefaultPasswordValidators` 구성을 추가했다. API의 누락된 정책 설정과 Article의 공통 Admin/API 설정을 연결했다.
Policy 생략은 기존 빈 strength 검사이며, 선택한 정책도 secret 원문이나 인증 의미를 자동 변경하지 않는다.

최종 non-Markdown **2,431 파일** source map은
`316c93f7a786514ce42db9c2140d9d3683306f05ee63eff6ff8a97035400877e`다. Checkpoint·negative control·보조 검사의 시작/종료 source는 같다.
Darwin arm64 / Go 1.26.5의 **9 packages / 1,000 required entries**(60 roots·940 subcases)를 검사했다.
Identity/Admin/API·Article composition/adapter/application, 양 DB의 password/user 관리·Admin/API·Unicode 입력 및 새 정책,
독립 Session/Bearer generated client를 포함한다. 이전 source의 전체 platform 결과를 옮겨 쓴 것이 아니다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,007 PASS / skip 0 | 44.637초 |
| race | 1,007 PASS / skip 0 | 193.016초 |
| CGO=0 | 1,007 PASS / skip 0 | 48.706초 |

`go test -json -count=1 -p=3 -timeout=20m -run <선택한 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. 모든 시작/종료·필수 root/subcase·package·no-skip을 검사했다.

- 독립 Django 6.1 / CPython 3.14.3 / Unicode 16에서 **156개 입력**의 네 개별 validator와 실제 `validate_password`가
  반환하는 code/params/순서를 비교했다. **16,245개 similarity 조합**, 사전 **19,640개 항목 × 3변형 = 58,920 비교**도 일치한다.
  Byte/글자 길이·superscript/new digit/fraction·anagram/multiset·inclusive threshold·빈 split 부분·Unicode full lowercase·
  NFKC 미적용·C0/Unicode strip·custom/empty 사전·속성 선택 순서와 빈 선택을 포함한다.
- Observer 두 독립 실행과 checked-in JSON bytes가 같고 input·Django source·사전 hash를 보존한다.
  Reference SHA256은 `7afdd8dca011248e83e4757027c539de27c600cd2983a823493e6de6915da8e7`이다.
  Default dictionary의 압축 원본 hash `3c1baed62596de36860824eb3f436d5932d37ca8b06e59df78f5a44ec175afe4`,
  크기 80,228 / decoded 162,384바이트와 count를 확인했다. 누락·잘림·변조 입력은 factory가 결과를 게시하지 않는다.
- 양 DB에서 새 정책 **41 subcases + root** 각각을 필수 목록에 등록했다. Service/Admin/API × 생성/password command의
  짧음·common·Unicode 숫자·similarity·복합 오류 순서를 실제 요청으로 검증했다. 거부는 hash 0·User/revision 불변·
  대상 session 보존·audit 없음이다. 성공은 hash 1·확정 credential/session/audit와 별도 연결/runtime의 실제 HTTP 인증을 확인했다.
- 현재 권한·stale revision 거부는 약한 password 진단보다 먼저 적용한다. 생략한 policy는 자동으로 강도를 검사하지 않는다.
  Policy slice 변경은 이미 등록된 Manager/Admin/API를 바꾸지 않고, secret은 HTML/JSON에 재표시하지 않는다.
- Hash 중 별도 연결이 revision 없이 profile을 바꾸는 고의적 비협력 writer에서도 마지막 validator는 현재 profile을 읽는다.
  확정 거부·cleanup failure·swallowed failure·unknown을 구분하고 자신의 credential/session/audit 효과는 남기지 않는다.
  이는 비협력 SQL writer 전체의 원자성/동시성 지원을 선언하는 것이 아니다.
- 실제 Article composition에서 같은 설정이 Admin과 JSON API 모두에 적용됨을 확인했다. 독립 generated client도
  생성과 password 교체의 복합/minimum/common/numeric 오류를 실제 HTTP로 받으며 이후 count/revision·부모 DB 검사가 불변을 확인한다.
- **11개 변형 / 11개 지정 assertion 탐지**: byte length, ASCII-only digit, multiset 중복 집계, exclusive threshold, 빈 split 유실,
  이전 Unicode lowercase, common strip 유실·사전 항목 누락, API 미연결, 마지막 profile 검사 누락, Article API 정책 누락.
  Build 오류를 탐지 성공으로 세지 않는다. 옵션/사전 소유권·동시 조회·진단 redaction·잘못된 구성/UTF-8도 검사했다.

영향 vet·identity/Article 생성 drift·gofmt·docs·diff와 CI 도구 41개를 통과했다. 생성 ABI/OpenAPI schema는 바꾸지 않았다.
Private PostgreSQL 17.10 UTF8/libc/C를 사용했다. 종료 시 public table/잔여 godj schema/추가 연결은 `0|0|0`, container 제거도 확인했다.
Image는 `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
처음 통합 검사는 새 Article 테스트의 잘못된 CSRF header 이름 때문에 403으로 실패했다. 공식 상수로 수정한 최종 source에서
모든 모드/controls/보조 검사를 실행했다. 편집 중 발견한 native wrapper 함수명 compile 오류도 수정했고 성공으로 세지 않았다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-password-checkpoint-1790489775507271000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-password-controls-1790489919668221000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-password-reference-1790489965217578000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-password-supplemental-1790489966395992000/receipt.json`

관리 소비자와 Unicode/password 입력 경계를 닫았으므로 이번 source의 명시된 Hosted full milestone을 실행한다.
이 로컬 checkpoint는 그 전체 검증이 아니다. 사용 불가능한 password·last_login, self-service/reset은 여전히 미완료다.

## GDJ-0100 — 고정 Unicode 16과 사용자명 전송·저장 경계

2026-09-27, `1e42620d` 뒤의 구현이다. NFKC·full lowercase·문자 판정을 Unicode 16에 고정하고,
credential/CLI 1,024바이트 envelope와 Schema IR의 User 256자 한도를 구분했다.
관리 생성은 150자, 편집은 IR 256자를 사용한다. 새 bootstrap은 NFKC, 기존 operator adoption은 정확한 이전 바이트를 보존한다.

최종 non-Markdown **2,421 파일** source map은
`7096ff5262ec21b79fb133cb29127639f1b41194b5d6a660fc3c8a9727eb2c82`다. Checkpoint·negative control·보조 검사 모두 시작/종료 source 동일성을 확인했다.
구현 `82baf3719bdbe7094e09ba41be17aaf53c29a27c`을 Draft PR #1에 게시했고,
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36298363689)의 실제 Go feedback도 같은 source에서 성공했다.
Darwin arm64 / Go 1.26.5의 **19 packages / 1,297 required entries**(333 roots·964 subcases)를 검사했다.
Unicode/auth·기존 Form/Admin·identity/Admin/API·systemstate·Article/Helpdesk와 양 native DB의 identity 전체 영향 범위,
createsuperuser terminal/process helper·private protocol, 독립 OpenAPI client 및 실제 SYS-023 producer를 포함한다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 2,927 PASS / skip 0 | 82.581초 |
| race | 2,927 PASS / skip 0 | 334.469초 |
| CGO=0 | 2,927 PASS / skip 0 | 80.273초 |

`go test -json -count=1 -p=3 -timeout=20m -run <선택한 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. 시작/종료·필수 root/subcase·package·no-skip을 검증했다.
SYS-023은 명시한 단일 실제 producer이며, 나머지 SYS 계약 또는 전체 platform/cold-process 실행을 뜻하지 않는다.

- 공식 Unicode 16 normalization **19,965행 × 5열 = 99,825 비교**와 **1,112,064 Unicode scalar** 전체의 NFKC·full lowercase·
  isalnum/isdigit/isspace, scalar마다 네 Final_Sigma 문맥을 독립 CPython 결과와 비교했다. Surrogate는 UTF-8 입력 영역 밖이다.
  긴 nonstarter의 CGJ 무삽입, canonical ordering·Hangul·확장/새 casing과 malformed UTF-8 비수정도 검사했다.
- 양 DB에서 각 **27 subcase + root**로 새 문자·한글 150자·보충 평면 256자·확장/정규화 입력의 service 생성,
  저장 후 별도 연결/runtime의 실제 HTTP 로그인, Admin 생성·긴 기존 이름 편집을 검사했다. 과대 입력은 hash/저장 전에 거부한다.
  초기 계정의 NFKC와 legacy adoption의 이전 이름/credential 보존도 별도 연결에서 인증했다.
- 독립 Session/Bearer generated consumer는 fullwidth Latin과 Todhri를 포함한 150자 사용자명을 실제 API로 생성/수정하고,
  부모 프로세스가 저장된 정규화 이름·credential을 다시 읽어 인증한다. 기존 revision/session/권한/audit/host-delete 회귀도 유지했다.
- 실제 PTY에서 한글 150자·보충 평면 256자를 입력하고 비밀번호 공백·비에코·terminal 복원을 확인했다.
  Private frame 왕복은 같은 UTF-8 bytes와 분리된 비밀번호를 보존하며 1,024바이트 초과를 거부한다.
  SYS-023 Go-native 결정의 frame 2,060/username 1,024바이트를 독립 decision oracle과 실제 producer로 확인했다.
- **10개 변형 / 10개 지정 assertion 탐지**: Unicode 16 NFKC/문자 누락, CGJ 삽입, Final_Sigma 누락, 이전 256-byte cap,
  IR 길이 검사 누락, 편집 한도 축소, adoption의 암묵적 rename, bootstrap 정규화 누락, private frame cap 회귀.
  Build failure를 탐지 성공으로 세지 않는다.

고정 Django 6.1 / CPython 3.14.3 / Unicode 16 observer 세 개를 각각 두 번 실행해 checked-in bytes와 일치시켰다.
Form은 **username 26·EmailValidator 304·confirmation 8**개다. 이전의 세 Unicode version probe를 정상 corpus에 포함했다.
별도 manager/form observer는 12개 입력의 username/email 및 생성 Form 결과를 기록한다. Generator는 이 Python 결과를 읽지 않는다.
Reference SHA256은 각각 all-scalar `2df3fd214c1012bb04e11aec81cffb69ea75337524977854523b5ec8d32d093f`,
native 입력 `f4251c1f36415ca86baab9ff236b772bae03868602b8cb35bf164ea66282f48c`,
Form `d7046ba7537d820d7addb7e906018baad76ae2251920560cca9146ac1b973dec`다.
고정 uv 0.10.12의 formal system-state oracle check도 통과했다. SYS-023의 두 byte metrics만 변경됐다.

영향 vet·identity/Article/Helpdesk generated drift·gofmt·docs·diff, CI 도구 **41개**, Unicode generator **2개**를 통과했다.
공식 UCD의 압축 원본과 hash/size manifest로 오프라인 생성 drift를 검사하고, 누락/손상 입력은 이전 생성 결과를 보존했다.
Private PostgreSQL 17.10 UTF8/libc/C를 사용했으며 종료 시 public table/잔여 godj schema/추가 연결은 `0|0|0`, container 제거도 확인했다.
Image는 `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.

초기 checkpoint 두 번은 새 테스트의 실패 플래그 해석과 email 관찰 입력의 Admin 재사용 오류로 실패했다.
첫 실패는 DecodeRequest의 마지막 bool을 성공으로 오해한 assertion, 두 번째는 의도적 non-email 문자열을 그대로 편집 제출한 fixture였다.
이를 바로잡고 최종 source에서 모든 모드와 negative control·보조 검사를 다시 실행했다. 실패 기록을 PASS로 합치지 않았다.

로컬 receipt:
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-unicode-checkpoint-1790487637995969000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-unicode-controls-1790487680668627000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-unicode-reference-1790487416892231000/receipt.json`
- `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp/identity-unicode-supplemental-1790487680668642000/receipt.json`

이 결과는 GDJ-0100 전체 완료나 전체 UserCreationForm 호환이 아니다. 내장 password strength, self-service/reset,
unusable password/last_login과 새 source의 전체 platform/process milestone이 남아 있다. 마지막 Hosted full의 source는
`01b67211a083c507d5e69c6be26702439aada559`이며 이번 변경의 전체 검증으로 전이하지 않는다.

## GDJ-0100 — 실제 Identity Form/Admin과 현재 권한의 선택·감사

2026-09-27, `3f59e1fd` 뒤의 구현이다. `identity/admin`의 User/Group/Permission CRUD·password command를
Article의 실제 Admin composition에 연결했다. Current action 인가의 선택 목록, 인가와 같은 snapshot의 감사,
revision 조건·호스트 관계 삭제, username/email 입력·password confirmation·host policy와 view-only 상세를 포함한다.

실행 시 non-Markdown **2,398 파일** source map은
`fbb23e7e8c882f312b6ccd0041e7a72fbdf9a376e1a7020dd430ba7f73255b3b`다.
최종 source map은 `1ec477347722ddc3f6ecd877c8c9d10011ec97230433871cbd9e0273c8ad3ef2`다.
구현 commit `9fe12534a25cea1bd56e54b5d3c9ff82f37542c3`을 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36295406909)가 이 source의 실제 Fast Go feedback까지 성공했다.
최종 non-Markdown bytes는 위 최종 map과 같으며 Hosted full 성공을 뜻하지 않는다.

두 map 사이 차이는 CI 필수 목록 두 파일의 빈 줄/주석 제거뿐이다. 제품·테스트·fixture bytes와 모든 필수 실행 이름의
동일성을 별도 receipt로 확인하고 여섯 JSON 실행 기록을 최종 roster와 다시 대조했다. Go 테스트를 재실행한 것으로 쓰지 않는다.

Darwin arm64 / Go 1.26.5에서 **13 packages / 1,082 required entries**(239 roots·843 subcases)를 실행했다.
Forms·model form·Admin·identity·identity Admin/API·systemstate·Article composition/adapter·Article/Helpdesk 전체와,
양 native DB의 기존 password/user/catalog/API 관리·iexact 생성 정책·실제 저장 인증 HTTP 및 새 Admin을 대상으로 했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 2,610 PASS / skip 0 | 58.616초 |
| race | 2,610 PASS / skip 0 | 287.578초 |
| CGO=0 | 2,610 PASS / skip 0 | 64.089초 |

`go test -json -count=1 -p=3 -timeout=20m -run <선택한 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. 모든 시작/종료, 필수 root/subcase와 no-skip을 검사했다.
새 native Admin은 DB별 46개 subcase와 root를 필수 목록에 등록했다. 완전한 CI/platform/cold-process 범위가 아니다.

- 실제 HTTP의 staff 로그인·cookie·CSRF를 통해 사용자·그룹·권한 생성/편집/삭제, escaped 선택 label·중복 선택,
  비밀번호 공백·confirmation, no-op·stale revision, 현재 action 권한, private-field 위조를 검사했다.
- Password command는 대상의 로그인/미사용 session을 폐기하고 다른 session을 유지한다. 자기 password 교체도 확정 성공을
  반환한 뒤 재로그인을 요구한다. 같은 durable DB의 별도 연결/runtime에서 새 비밀번호를 확인하고 이전 비밀번호를 거부했다.
- 선행 읽기 뒤 다른 연결의 revision 변경·권한 회수를 마지막 fence에서 거부한다. 네 관리 작업의 audit 실패는
  user/session/audit rollback, 실제 commit 뒤 injected unknown은 503·no redirect/no retry를 검증했다.
  HTTP 검사의 session clock만 고정해 일반 sliding expiry와 관리 부작용을 혼동하지 않고 bytes를 비교한다.
- 호스트 PROTECT의 확정 거부와 CASCADE를 실제 삭제 Form에서 검사했다. 기존 Helpdesk의 view-only 상세를 허용하되
  change POST는 데이터 접근 전에 거부하고 편집용 target permission 검사는 유지했다.
- 선택과 감사는 권한 확인 중 다른 연결이 변경한 데이터를 섞지 않는다. 다음 요청의 현재 인가, reader 종료 후 사용 거부,
  query·read cleanup·취소·잘못된 callback, permission 모양 실행 오류, catalog 4,096개 경계/초과의 무절단 거부를 검사했다.
- Host password policy의 hash 전/최종 fence 거부와 취소, rollback cleanup·swallowed rejection·unknown을 구분한다.
  Validator의 profile·옵션 소유권, 잘못된 field 오류, 기본 빈 strength 정책과 비공개 config 진단도 검사했다.
- 별도 Article composition 테스트가 새 관리 목록을 실제 등록하고, CSRF user 생성·NFKC 저장·hash 확인·안전한 기본 role을
  검사한다. 테스트 callback만의 공통 Admin 기반 검증과 구분된다.

독립 Django 6.1 / Python 3.14.3 observer가 새 temporary SQLite에서 **username 23·EmailValidator 304·confirmation 8**개를
관찰했다. 공통 synthetic 입력만 읽으며 GoDj 출력/기대값을 import하지 않는다. 두 독립 실행 JSON과 checked-in fixture가
일치하고, input hash와 auth forms/validators·core validators 소스 hash를 보존한다.
Reference SHA256은 `23c00ae7588ace59684e41ea6708ee268c05b6aeb74dfc32d78bab0a96fb1741`,
input SHA256은 `a9f9e8dcb0e127863de178508239ed14d82643bdf7e35bcfe521327ce2cdaf2c`다.
Django grammar의 adapted 범위와 BSD-3-Clause 출처는 [NOTICE](../../identity/admin/NOTICE.md)에 명시했다.

위 **335개 입력 subset**의 일치와 별개로 **Unicode version probe 3개는 불일치**다. CPython Unicode 16이 허용/정규화하는
outlined Latin U+1CCD6, Todhri U+105C0, 새 숫자 U+10D40을 현재 Go/x/text Unicode 15 경로는 거부한다.
실제 관찰을 fixture·실행 로그에 남겼으며 이 세 입력을 parity PASS나 skip으로 계산하지 않는다.
Form의 150자와 기존 credential의 256-byte 제한도 남아 있다. 내장 password strength validator, self-service/reset,
unusable password·last_login 및 GDJ-0100 전체 platform/process milestone을 완료했다고 주장하지 않는다.

Go overlay **8개 변형 / 8개 assertion 탐지**: unrelated target 권한 요구, history 현재 인가 생략, 마지막 password policy 거부
무시, username 정규화 생략, password trim, unknown을 conflict로 변경, oversized 선택 truncate, password widget 공개.
실제 해당 테스트의 실패를 확인했고 build 실패는 탐지로 세지 않았다. Controls는 실행 source 전후가 같다.

초기 checkpoint에서 view-only GET의 옛 403 가정, 새 descriptor 권한 목록/명시적 ordering 기대, HTTP session sliding expiry를
관리 rollback과 혼동한 fixture, query hook의 인가용 Permission SELECT 포함, 실패 reader의 test wrapper panic을 발견했다.
기존 쓰기 거부 검증을 유지하며 fixture를 고친 후 위 선언 범위를 다시 실행했다. 실패와 정리 기록을 보존했다.
추가로 CI roster에 넣은 빈 줄/주석이 기존 CI parser에서 거부됐다. 필수 테스트 이름은 유지하고 형식만 바로잡았으며,
최종 source에서 CI 도구 **41개 PASS**와 여섯 event inventory 재검사를 완료했다.

영향 vet·identity/Article/Helpdesk 생성물 drift·gofmt·문서 링크·diff 검사는 통과했다. Supplemental 최초 receipt의 CI 도구
실패는 위 roster repair receipt가 해결한다. Source 차이를 숨겨 supplemental 전체를 최초부터 PASS로 표현하지 않는다.
Private PostgreSQL은 17.10 UTF8/libc/C,
image `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
완료 시 public table·owned schema·다른 connection은 `0|0|0`이고 container를 제거했다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.

- `identity-admin-checkpoint-1790483731939424000/receipt.json`: 세 모드·필수 roster·raw events·실행 source/cleanup.
- `identity-admin-controls-1790483888131023000/receipt.json`: 8개 변형 탐지와 source 보존.
- `identity-admin-reference-1790483793276407000/receipt.json`: 두 독립 실행·hash·335개 subset과 세 version probe.
- `identity-admin-roster-1790484231194097000/receipt.json`: 최종 source map·정확한 두 파일 차이·실행명 동일성·CI 도구와 inventory 재검사.
- `identity-admin-supplemental-1790483888131010000/receipt.json`: vet·생성 drift·gofmt·docs·diff와 최초 CI roster 실패.
- `identity-admin-checkpoint-1790483495316706000/receipt.json`, `identity-admin-checkpoint-1790483615526882000/receipt.json`: 초기 fixture 실패·정리.

## GDJ-0100 — literal iexact와 사용자 생성의 중복 정책

2026-09-27, `143eb3bf` 뒤의 변경이다. Char/Text의 root·nullable·관계 `IExact`와 dynamic `__iexact`를
공통 Query AST에 연결했다. SQLite는 완전한 LIKE literal escape, PostgreSQL은 column::text/RHS 양쪽 UPPER를 사용한다.
`UserCreate.WithCaseInsensitiveUsernameCheck()`는 NFKC 후보를 현재 인가 뒤 hash 전과 write fence 안에서 재검사한다.
기본 Manager/API 생성과 로그인 의미, 실제 Identity Form/Admin 전체 완료와 구분한다.
최종 non-Markdown **2,373 파일** source map은
`ab78baabd6e369bc98c40ce451e8e82d2d71ea1a8bce15ff73155642df781544`다.

구현 commit `c7c9ff73fc98ac7251df20156511b1ea9a129ac6`을 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36291920509)가 이 source의 실제 Fast Go feedback까지 성공했다.
게시된 non-Markdown bytes는 아래 로컬 검증 source와 같으며 Hosted full 성공을 뜻하지 않는다.

독립 Django 6.1 observer는 synthetic 입력만 읽는다. Python 3.14.3, SQLite **3.50.4**, psycopg **3.3.6**과
PostgreSQL **17.10 UTF8/libc/C**에서 각각 **조회 233개·생성 11개** 결과를 얻었다.
각 DB를 새로 준비해 두 번 실행했고 결과 JSON bytes가 일치했다. 결과에 input hash·Django lookup/auth forms/backend
operations 파일 hash·DB profile을 포함했다. 관찰한 SQL/parameter도 보존하며 GoDj 결과에서 기대값을 만들지 않았다.
SQLite 결과 SHA256은 `77227756016ac443ede5c860133cb7f0ff037afa12f8bd9dd9477e1af9e4148f`,
PostgreSQL은 `28996a042a82f2a1fff03dc391f4de67b8ab58b3d54eadd50088998ad10dd89f`다.
독립 reference database와 private container 정리도 완료했다.

Darwin arm64 / Go 1.26.5에서 **8 packages / 898 required entries**(340 roots·558 subcases)를 실행했다.
Query·ORM·공통 query planner·identity·identity API 전체, 양 DB의 새 조회/생성 정책과 기존 사용자 관리·forward scalar·
nullable Boolean/compiler 회귀, 7개 외부 generated consumer parent가 scope다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 3,151 PASS / skip 0 | 27.159초 |
| race | 3,151 PASS / skip 0 | 134.543초 |
| CGO=0 | 3,151 PASS / skip 0 | 29.658초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. JSON event의 필수 root/subcase·전체 시작/종료와 no-skip을
검사했다. Native query 233개는 typed/dynamic AST와 실제 Rows/Count/Exists를 대조하며 Count/Exists를 All보다 먼저 실행해
cache가 DB 실행을 가리지 못하게 했다. 입력/기준 roster hash를 확인하고 모든 이름을 CI 필수 목록에 연결했다.
External Text consumer는 실제 재생성한 별도 module에서 root/nullable Text·완전 일치/부분 문자열 구분·부정의 NULL을 검사한다.
그 밖의 6개 parent는 forward scalar와 relation query 생성·타입 경계를 검증한다. Child는 부모와 같은 race 모드를 사용한다.
Parent inventory를 child PASS 개수와 합산하지 않는다.

32개 문자열은 빈 값·ASCII 대소문자·공백·줄바꿈·quote·percent/underscore/backslash·accent·Greek sigma·Turkish I·
분해 Unicode·전각 문자를 포함한다. Optional FK의 부재, OR/NOT/double-NOT, reverse/mixed 관계의 중복 행과 NOT EXISTS를 검사했다.
생성은 독립 Django 11개 결과, 기본 정책의 case-sensitive 생성/로그인 유지, hash 도중 다른 연결이 저장한 중복,
서로 다른 principal의 대소문자 경쟁에서 1승·1거부, hash 전 현재 인가, 읽기 종료 실패와 양 단계 lookup 실행 오류를 검사했다.
감사 실패는 rollback하고 unknown commit은 결과를 게시하거나 자동 재시도하지 않는다. 기존 사용자·session·감사 bytes도 대조했다.

Go overlay **8개 변형 / 8개 assertion 탐지**를 확인했다. SQLite escape 제거·부분 검색으로 변경,
PostgreSQL RHS UPPER 제거, 양 DB의 부정 NULL guard 제거, 생성 정책 생략·마지막 fence의 거부 무시·lookup 오류 삼키기를
각각 관련 테스트가 거부했다. Build 실패는 탐지로 세지 않았다. Checkpoint와 controls의 source 전후는 같다.
Private PostgreSQL image는 `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`이며,
두 실행의 잔여 public table·owned schema·다른 connection은 모두 `0|0|0`이고 container를 제거했다.

초기 `a99644c5…`는 fixture의 root scalar를 relation-only parser에 전달하고 PostgreSQL에 미지원인 명시적 AutoField key
삽입을 시도해 실패했다. Root/관계 parser를 구분하고 DB 생성 key를 기대 roster와 확인하도록 고친 뒤 전체 선언 scope를
다시 실행했다. 실패·cleanup 로그도 보존했다. 제품에서 이 제한을 우회하는 호환 분기는 추가하지 않았다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.

- `iexact-reference-1790479061769897000/receipt.json`: DB별 두 독립 실행·결과 hash·환경·정리.
- `iexact-checkpoint-1790479785893058000/receipt.json`: 최종 세 모드·필수 roster·raw events·source/cleanup.
- `iexact-controls-1790479904704031000/receipt.json`: 최종 8개 실행 탐지·source 일치·cleanup.
- `iexact-checkpoint-1790479712743698000/receipt.json`: 초기 fixture 실패와 cleanup.
- `iexact-supplemental-1790480070875358000/receipt.json`: 영향 vet·CI 도구·identity/Article/relation 생성 drift·gofmt·docs·diff.

보조 검사도 위 source에서 PASS다. 최종 증거 문서 추가 뒤에는 링크·diff 검사를 다시 적용했다.

실제 Identity 관리 화면·username/password 전체 validator·confirmation·현재 권한의 선택 목록·감사 조회는 미완료다.
GDJ-0100 전체 platform/process milestone은 관리 소비자 통합 뒤 실행한다. 이전 Hosted full을 새 source의 성공으로 전이하지 않는다.

## GDJ-0100 — 생성·비밀번호 command·revision의 공통 Admin 기반

2026-09-27, `3d7b9c52` 뒤의 변경이다. 생성 전용 model form·비저장 입력과 object command,
조회 actor 전달·view 또는 change admission·revision 제출 조건을 공통 Admin에 추가했다.
비밀번호의 공백 보존·재표시 금지와 진단 비공개를 연결하고 기존 Article·Helpdesk callback도 함께 변경했다.
최종 non-Markdown **2,362 파일** source map은
`73f776444e5b7d5b22013571671d99dc0ad9521f0d94c3da123f34d897eaf93d`다.

구현 commit `d72ac36bf5ad027283e36c71a11b17f7e9aee4d8`을 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36289923385)가 실제 Fast Go feedback까지 성공했다.
이는 아래 영향 검증과 별개의 Fast 범위이며 새로운 Hosted full 성공을 뜻하지 않는다.

Darwin arm64 / Go 1.26.5에서 **6 packages / 154 required entries**(137 roots·17 subcases)를 실행했다.
Scope는 `forms`, `forms/model`, `admin`, Article의 Admin adapter·HTTP 소비자, Helpdesk 전체 package다.
새 생성 폼·command HTTP는 test callback을 사용하며, 실제 Identity Manager/Admin DB 연결의 증거로 세지 않는다.
기존 소비자의 실제 SQLite/PostgreSQL CRUD·권한·관계·감사·실패/재시작과 Article 생성물 drift는 같은 checkpoint에서 실행했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,547 PASS / skip 0 | 14.172초 |
| race | 1,547 PASS / skip 0 | 99.160초 |
| CGO=0 | 1,547 PASS / skip 0 | 14.043초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. JSON event에서 모든 시작·종료·필수 root와
revision/command subcase를 대조하고 skip을 거부했다. Private PostgreSQL **17.10** image는
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
종료 후 잔여 public table·owned schema·다른 connection은 `0|0|0`이며 container를 제거했다.

새 검사는 생성/편집 field 분리·password 확인 실패·공백/Unicode 보존·HTML 재표시 금지·formatter fallback 비공개,
add와 추가 change의 conjunction, 생성/편집 선택 목록의 독립 권한·쓰기 전 재검사를 포함한다.
Revision의 누락·잘못된 값·stale·2^53 밖 int64·validation 재표시·no-op·삭제, callback 직전 경쟁 주입을 검사했다.
Command의 별도 Form·권한·CSRF·위조 입력·실패/unknown·한 번만 호출·성공 후 잘못된 결과 거부와
논리적 password audit 이름의 명시적 허용도 포함한다. View 인가 실행 오류는 change 허용으로 우회하지 않아야 한다.
Helpdesk는 view가 거부되고 change가 허용되면 목록을 읽을 수 있지만, 둘 다 거부되면 data I/O 전에 차단해야 한다.
생성 폼의 비저장 추가 입력과 논리적 audit 이름은 저장 field·PK를 가리지 못한다.

Go overlay **7개 변형 / 7개 assertion 탐지**를 확인했다. Password trim·값 진단 노출,
stale command 선행 검사 우회·추가 생성 권한 누락·unknown의 정상 HTTP 반환·인가 오류 fallback·
생성에서 편집 선택 loader를 사용하는 변형을 각각 관련 테스트가 거부했다. Build 실패를 탐지로 세지 않았다.
초기 stale 변형은 미사용 변수로 compile에서 실패해 제외했고, 변수를 유지한 조건 우회로 다시 실행했다.
최종 control 7개는 위 최종 source에서 모두 실행했으며 source 전후가 같다.

초기 `99222533…`에서는 command fixture의 기존 세션 재로그인, reserved-field 오류의 중복 wrapping,
목록에 편집 전용 field까지 요구하던 옛 테스트 조건을 확인했다. Session을 정상 종료하고 정확한 오류 경계를 유지하며,
목록의 최소 projection과 편집의 전체 projection을 각각 검증하도록 수정했다.
`1914e347…`의 Helpdesk 검사는 view-only deny를 전체 read deny로 기대해 실패했다. 두 권한의 실제 계약을
분리해 양 DB에서 재검증했다. 뒤의 논리적 audit 이름/PK 제한까지 포함한 최종 source에서 전체 선언 scope를 다시 실행했다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.

- `admin-foundation-checkpoint-1790477420850632000/receipt.json`: 최종 세 모드·필수 roster·raw events·source/cleanup.
- `admin-foundation-controls-1790477440522157000/receipt.json`: 최종 7개 실행 탐지와 source 일치.
- `admin-foundation-supplemental-1790477670731156000/receipt.json`: 영향 vet·docs·gofmt·diff와 source 일치.
- `admin-foundation-checkpoint-1790476751886184000/receipt.json`, `admin-foundation-checkpoint-1790476839819230000/receipt.json`: 앞선 실패와 cleanup.

영향 `go vet`·147개 문서의 링크·gofmt·diff도 통과했다. 새로운 Identity Form/Admin 등록·현재 저장 인가의 선택 목록,
UserCreationForm validator·password 정책·self-service/reset과 GDJ-0100 전체 platform/process milestone은 미완료다.
이번 공통 기반 또는 기존 Hosted full을 그 완료 근거로 전이하지 않는다.

## GDJ-0100 — 독립 Identity Session/Bearer 생성 클라이언트

2026-09-27, `3a7b1fcd` 뒤의 변경이다. 실제 관리 API 선언에서 고정 **ogen v1.24.0**으로
Session/Bearer client와 OpenAPI snapshot을 생성했다. 별도 module은 GoDj를 import/replace하지 않는다.
`If-Revision`/`Revision` header의 OpenAPI를 required int64와 정확한 양수 범위로 기술했다.
HTTP 의미·Schema IR·model/migration은 바꾸지 않았다.
최종 non-Markdown **2,352 파일** source map은
`9bd4e1269622d00f5218f404e4181a72ec0aaa3e1375f6f60b2d92c0dc5248cd`다.

구현 commit `b00395a08d6391eda5c0b9f1145340990a7b7f92`를 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36286775333)는 이 source의 실제 **Fast Go feedback**까지 성공했다.
게시한 non-Markdown bytes는 아래 로컬 검증 source와 같으며 Hosted full이나 새 전체 platform/process 검증을 뜻하지 않는다.

Darwin arm64 / Go 1.26.5에서 **6 packages / 153 required entries**(45 roots·108 subcases)를 실행했다.
Scope는 identity API·OpenAPI, 독립 client 전체, Article의 실제 identity API/login root,
SQLite/PostgreSQL `IdentityManagementAPI` root다. JSON event의 전체 completion·no-skip을 감사했다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 247 PASS / skip 0 | 18.714초 |
| race | 247 PASS / skip 0 | 100.648초 |
| CGO=0 | 247 PASS / skip 0 | 21.528초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다. Private PostgreSQL은 **17.10**이며 image는
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
모든 모드에서 기존 Article Bearer/Session·Helpdesk Session과 새 Identity Session/Bearer의 5개 문서가
실제 adapter와 같고 offline 재생성에 drift가 없었다. Child는 부모와 같은 race 모드로 빌드했다.
부모가 독립적으로 요구하는 **52개 completion check**가 모두 있어야 최종 DB 검사를 실행한다.

새 Session client는 관리 API 19개 operation과 scalar 페이지, Unicode username·unique 오류,
PUT scalar default, collection 생략/명시적 빈 배열/중복 제거/no-op을 실제 HTTP로 확인했다.
생성된 int64 수정 조건의 400/412와 header를 제거한 negative transport의 428, read-only·CSRF 거부,
Permission/Group 삭제에 따른 직접 소유자 revision 증가와 이전 수정 거부를 포함한다.
Safe response의 새 CSRF cookie와 typed masked header를 함께 갱신하며 후속 변경에 사용한다.

두 Identity profile은 같은 durable SQLite·실제 저장 계정/권한을 사용한다. Bearer CRUD와 401/403 challenge를 확인했다.
Session client가 actor를 비활성화한 뒤에도 원래 superuser snapshot을 반환하는 Bearer verifier를 사용해
현재 DB 권한이 read/write를 거부하는지 검사했다. 세션은 부모가 실제 credential stamp와 함께 seed한다.
이 fixture가 로그인 endpoint나 token issuer를 구현했다는 뜻은 아니다.

부모는 새 runtime에서 최종 profile·서버 principal ID·password 공백 보존/이전 password 거부,
정확한 through 행과 host PROTECT/CASCADE/SET_NULL 결과, audit를 직접 검사했다.
Password 교체와 비활성/재활성화 후 사용한 세션뿐 아니라 **HTTP에 한 번도 쓰지 않은 보조 세션**도 폐기되어야 한다.
다른 anonymous session의 저장 snapshot은 그대로 유지되어야 한다. 삭제 audit는 삭제한 resource의 이벤트이며,
직접 owner의 revision 증가를 별도 owner 편집 audit와 혼동하지 않는다.

별도 wire probe는 2^53 밖 ID/revision/collection의 exact bytes와 int64 경계, required collection의 누락/null/잘못된 원소,
required Revision header의 누락/잘못된 값, typed 412/428/503와 한 번만 전송하는 동작을 검사했다.
Synthetic 503은 서버의 unknown commit/rollback 증거가 아니다. 실제 실패/unknown은 같은 checkpoint의 양 DB API root가 소유한다.
독립 생성 client 자체의 DB는 SQLite이며 PostgreSQL client나 server process 재시작까지 검증한 것으로 표현하지 않는다.

Go overlay **7개 변형 / 7개 assertion 탐지**를 확인했다. Password trim 선언은 schema drift에서 거부했고,
선언을 바꾸지 않는 hash 전 trim은 부모의 실제 저장 password 검사에서 탐지했다.
수정 조건 누락·no-store 제거·현재 actor 인가 우회·session 폐기 생략·identity-only 호스트 삭제 정책도 탐지했다.
6개는 실제 HTTP/DB 검사, 1개는 schema drift 검사다. Build 실패·timeout·race를 탐지로 세지 않았다.
Control의 앞선 5개 통과 결과는 **동일 source 전후 map**을 확인한 뒤 그대로 보존하고, password control만 보강 실행했다.

영향 parent/child `go vet`, docs·format·diff와 guard를 둔 실제 exporter의 5개 schema 일치를 확인했다.
Checkpoint·control·보조 검사 모두 non-Markdown source 전후가 같다. PostgreSQL 잔여 table·owned schema·다른 connection은
`0|0|0`이고 private container를 제거했다. 기존 dependency lock과 기존 3개 schema/generated package는 변경하지 않았다.

초기 source `80c8996e…`는 safe response 이후 CSRF token 갱신 누락으로 client가 실패했다.
`e0c988dc…`는 폐기된 Session의 403 `not_authenticated`를 `permission_denied`로 잘못 기대해 실패했고,
`b706e891…`는 catalog 삭제에 따른 owner revision 증가를 owner audit 2개로 중복 기대해 부모 검사가 실패했다.
각 fixture를 실제 계약에 맞게 고치고 최종 source에서 전체 선언 범위를 다시 실행했다.
최초 password-trim control도 schema drift에서 조기 거부되어 HTTP 증거로 세지 않았고, 별도의 hash 경계 변형을 추가했다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.

- `identity-client-checkpoint-1790473432279296000/receipt.json`: 최종 세 모드·roster·raw events·source/cleanup.
- `identity-client-controls-1790473649489350000/receipt.json`: 최종 control 7개와 같은 source의 앞선 5개 결과 참조.
- `identity-client-controls-1790473498523239000/receipt.json`: 앞선 5개 실제 탐지와 schema-drift에서 끝난 최초 password probe.
- `identity-client-supplemental-1790473567548706000/receipt.json`: parent/child vet·format/docs·실제 exporter 비교.
- `identity-client-checkpoint-1790473120970830000/receipt.json`, `identity-client-checkpoint-1790473230759633000/receipt.json`, `identity-client-checkpoint-1790473299191703000/receipt.json`: 초기 fixture assertion 실패와 cleanup.

전용 관리 Form/Admin·action별 관련 선택 목록, self-service/reset·unusable password/last_login 및
GDJ-0100 전체 platform/process milestone은 미완료다. 이전 Hosted full을 이 source의 검증으로 전이하지 않는다.

## GDJ-0100 — 실제 관리 JSON API와 권한·수정 조건

2026-09-27, `e36afb74` 뒤의 변경이다. User·Group·Permission의 목록/상세·생성·PUT/PATCH·삭제와 관리자 password 교체
19개 operation을 `identity/api`·OpenAPI·Article의 실제 인증 composition에 연결했다.
최종 non-Markdown **2,321 파일** source map은
`2cfbaa3ce570cb7213921a1fb31cd44fb076d9ee076a65bb8d2467d8455cc98f`다.
Darwin arm64 / Go 1.26.5, 실제 SQLite와 private PostgreSQL **17.10**, **16 packages / 962 required entries**
(558 roots·404 subcases)를 실행했고 JSON event의 completion·no-skip을 감사했다.
Identity API의 native 필수 entry는 backend당 55개다. schema·migration·생성 model ABI 변경은 없다.

구현 commit `c81dee613d518acb6dd4ba2dd5c9d5dd12145d64`를 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36284619903)는 이 commit의 실제 **Fast Go feedback**까지 성공했다.
검증한 non-Markdown source와 게시 제품 bytes가 같으며 Hosted full의 완료를 뜻하지 않는다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 4,044 PASS / skip 0 | 52.709초 |
| race | 4,044 PASS / skip 0 | 213.027초 |
| CGO=0 | 4,044 PASS / skip 0 | 48.235초 |

Auth·identity·ORM·systemstate·Admin·Session/Bearer API 인증·OpenAPI·serializer·새 identity API의 core,
Article/siteapp 소비자와 양 native backend의 snapshot·identity·adoption 범위다.
`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`, mode별 `-race` 또는 `CGO_ENABLED=0`,
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`을 사용했다.
PostgreSQL image는 `postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
Native fixture는 양 DB의 실제 두 연결을 각각 max-open=1로 제한한다.

실제 Session HTTP에서 User/Group/Permission CRUD·NFKC username·이름 trim·password 공백 보존,
bounded scalar page와 상세 collection·no-op·생략 보존·stale revision 412를 확인했다.
`If-Revision` 누락/잘못된 값·CSRF·unknown/duplicate JSON·server-owned field·잘못된 관계·body 한도는
저장 상태를 보존한다. 정상 인증의 idle expiry와 DB mutation을 혼동하지 않도록 HTTP fixture clock을 고정했다.
입력 거부 뒤에도 전체 모델·관계·감사·저장 session bytes를 비교했다.

각 모델의 none/view/change/add/add+change/delete를 실제 저장 권한으로 대조했다.
Change-only 조회, Group/Permission의 add-only 생성, User의 add+change 조건, delete-only 응답과
기존 cookie를 유지한 권한 폐기를 포함한다. Password 교체와 비활성화 뒤 기존 session 거부,
재활성화가 폐기된 session을 부활시키지 않는 점, 새 password 로그인과 runtime 재접속을 확인했다.
Permission code 변경은 현재 grant를 바꾸고, 관계 삭제는 직접 Group/User revision을 증가시켜 이전 HTTP 수정 요청을 거부했다.
호스트 PROTECT/CASCADE와 cleanup 실패의 500 경계를 확인했다. 비밀번호 profile의 직접 입력 거부는 400,
wrapped/일반 hash 장애는 500이며 한 번의 호출과 저장 상태 보존을 검사했다.

세 모델의 create/update/delete와 password 명령에 audit 실패·unknown rollback/commit을 실제 native transaction에 주입했다.
500/503 응답에 성공 DTO·Revision·Retry-After가 없고 호출을 재시도하지 않는지 확인했다.
Unknown commit의 durable 변경과 unknown rollback의 보존을 별도로 검사했다.
새 관리 API의 실제 HTTP fixture는 Session이다. Bearer의 대안 권한·오류 중단은 인증 adapter의 실제 HTTP 테스트로 검증했으며,
새 management Bearer client 통합이나 독립 process 재시작을 이 범위로 주장하지 않는다.

Article의 실제 Admin HTML 로그인으로 관리 API·보호된 OpenAPI를 사용했다.
API가 생성한 staff가 공백을 포함한 password로 실제 Admin 로그인에 성공하고, staff만으로 관리 API 권한을 얻지 않는지 확인했다.
이미 열린 두 Article runtime이 같은 저장 변경을 관찰한다. 전용 사용자 관리 Admin 화면의 검증은 아니다.

Go overlay **13개 변형 / 24개 실제 assertion 탐지**를 통과했다:
password trim, 수정 조건 생략, stale/unknown HTTP 성공 처리, view/change를 AND로 변경, private field 응답 노출,
Group 선택 누락, PROTECT cleanup 실패를 입력으로 낮춤, no-store 제거, identity-only 삭제 정책 선택,
Session/Bearer 인가 실패 fallback, wrapped hash 실패의 입력 오인이다.
Build 실패·timeout·race는 탐지로 세지 않았다. 최종 checkpoint·control 모두 source 전후 일치와
PostgreSQL 잔여 table·owned schema·다른 connection `0|0|0`, private container 제거를 확인했다.

같은 source에서 기존 Article Bearer/Session·Helpdesk Session의 독립 generated client를 normal/race/CGO=0으로 실행했다.
각 모드의 `TestGeneratedOpenAPIClientContract` 1 PASS / skip 0, 고정 ogen 재생성 drift·실제 HTTP·child 필수 receipt·최종 DB 검사를 통과했다.
시간은 25.677/41.280/4.884초다. 이는 기존 OpenAPI 소비자 호환성의 증거이며 새 관리 API의 독립 generated client는 아직 없다.
영향 `go vet`, CI Python **41/41**, format·diff·147개 문서 링크 검사도 통과했다.

초기 source `9c8288bf73016a0d9838a92c1a25c38925695ef65d116f0c1a8eac27126cda04`의 native 실행은
테스트 로그인 header에서 password 공백이 제거되고 정상 session idle expiry 갱신을 rollback 실패로 비교해 실패했다.
HTTP fixture의 password 전송을 명시적으로 인코딩하고 clock을 고정했다. 후속 source `f8be8560…`는
거부된 field 이름을 응답 데이터 노출로 오인한 assertion을 수정했다. 실제 저장 field key·값 노출 검사는 유지한다.

Source `e20c4715…`에서 normal 전체와 race core는 통과했으나 disk 여유가 약 300 MiB로 떨어졌다.
Linker의 `no space left on device`와 OrbStack 중지로 나머지 실행·cleanup 확인이 중단됐다.
재생성 가능한 Go build cache 95 GiB를 `go clean -cache`로 정리하고 OrbStack을 재시작했다.
이전 private container가 남지 않았음을 확인했으며 코드·원본 증거는 보존했다.
중단 실행이나 build 실패를 PASS/negative-control 탐지로 옮기지 않았다.
Hasher 입력/실행 오류 구분을 추가한 위 최종 source에서 세 모드와 control을 모두 새로 실행했다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.
- `management-api-checkpoint-1790470742413654000/receipt.json`: 최종 16 packages·세 모드·roster·원본 stream·source/cleanup.
- `management-api-controls-1790470742413654000/receipt.json`: 최종 13개 overlay·24개 실제 assertion·source/cleanup.
- `management-api-existing-client-1790470913714552000/receipt.json`: 같은 source의 기존 generated client 세 모드·drift/HTTP.
- `management-api-supplemental-1790470912499964000/receipt.json`: 같은 source의 vet·CI 도구·format/docs.
- `management-api-checkpoint-1790469728870599000/receipt.json`: 초기 HTTP fixture assertion 실패와 cleanup.
- `management-api-checkpoint-1790470024720922000/receipt.json`: 후속 field-name assertion 실패와 cleanup.
- `management-api-checkpoint-1790470171037416000/interruption.json`, `management-api-controls-1790470270477468000/interruption.json`: disk/OrbStack 중단과 복구 기록.

전용 관리 Form/Admin·관련 선택 목록·새 API의 독립 generated client, self-service/reset·unusable password/last_login과
GDJ-0100 전체 platform/process milestone은 미완료다. 이번 영향 실행을 Hosted full이나 전체 프레임워크 완료로 표현하지 않는다.

## GDJ-0100 — Group·Permission 관리와 직접 소유자의 revision

2026-09-27, `e28eccbe` 뒤의 변경이다. 최종 non-Markdown **2,312 파일** source map은
`a63cb4eaaab3de49bac94993bd1284fc7ebf405446b6ab71459e50065c3e9f1f`다.
Darwin arm64 / Go 1.26.5, 실제 SQLite·private PostgreSQL **17.10**, **10 packages / 743 required entries**
(447 roots·296 subcases)의 completion·no-skip을 검사했다. Auth·identity·ORM·systemstate·Admin·Web/API session·Bearer 전체와
native snapshot/identity/adoption roots가 scope다. Catalog의 필수 entry는 backend당 64개다.
Schema/생성 ABI 변경은 없다. 전용 관리 Form/Admin/API·독립 client, 별도 process restart와 Hosted full은 이 checkpoint의 대상이 아니다.

구현 commit `a9907ab1bc603f963984832d48f36d7ca12095c2`를 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36281969809)는 같은 commit의 실제 **Fast Go feedback**까지 성공했다.
아래 로컬 검증 source와 게시한 non-Markdown bytes가 같다. 이 결과는 Hosted 전체 검증을 대체하지 않는다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,501 PASS / skip 0 | 28.084초 |
| race | 1,501 PASS / skip 0 | 124.219초 |
| CGO=0 | 1,501 PASS / skip 0 | 30.250초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`와 mode별 `-race`/`CGO_ENABLED=0`을 사용했다.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`, PostgreSQL image는
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
양 DB의 실제 두 연결을 각각 max-open=1로 제한했다. 단순 동시 결과뿐 아니라 두 creator·editor의 한 번 성공,
Group 권한 확장과 User의 그룹 가입이 함께 한도를 넘기지 못하는 공유 fence를 검증했다.

Group/Permission 생성·단건/페이지·편집·삭제와 현재 add/view/change/delete 권한, view/change 대체, deny overlay,
self-revocation 뒤 과거 actor의 거부를 확인했다. Group의 생략/명시적 빈 선택·중복 정규화·입출력 slice 소유권,
이름의 문자 수 한도·잘못된 UTF-8/NUL·unique/missing/stale 입력·bounded scalar page·no-op revision/audit를 포함한다.
Permission code 변경은 직접/그룹 할당 ID를 유지하고 현재 grant를 바꾼다. 생성·관리에는 password hash 작업이 없다.
호스트의 AccessNote CASCADE/SET_NULL·AccessGuard PROTECT와 membership 제거, 값 없는 감사 기록을 실제 행으로 검사했다.
Runtime 재접속 뒤 password·session stamp와 기존 session bytes 보존, 현재 권한 반영을 확인했다. 새 관리 HTTP route의 증거는 아니다.

257명째 사용자의 직접 권한과 다른 그룹 권한을 포함해 유효 합집합 256/257 경계를 검사한다.
Overlap은 한 번만 세며, 첫 batch를 통과해도 뒤 사용자가 초과하면 이름·관계·revision·audit를 모두 보존한다.
Permission 삭제는 직접 할당 User **258명**과 Group **257개**의 revision을 한 번씩 증가시켰다.
권한만 상속하는 User는 변경하지 않았고, 마지막 Group revision의 overflow는 먼저 수행한 User/Group 갱신까지 rollback했다.
Group 삭제도 직접 소속 User의 revision을 갱신한다. 이전 revision으로 재제출한 User/Group 편집은 conflict로 거부했다.
이 version 전파는 credential/session 변경과 구분하며 정상 삭제가 세션을 폐기하지 않는다.

각 create/update/delete의 audit 실패·callback 취소·unknown rollback/commit·확정 commit 뒤 늦은 취소,
해당 경로의 update/membership 실패를 실행했다. 지정 fault가 실제 도달했는지 검사하고 확정 rollback의 모든 행과 audit를 비교했다.
Unknown에는 성공 DTO를 게시하거나 재시도하지 않았다. 누락/nil/중복/삼킨 callback·읽기 종료 오류도 결과를 게시하지 않는다.
입력 거부에 cleanup 실패가 결합되면 실행 오류다. 인가 callback 오류의 cause가 permission-denied여도 change 권한으로
우회하지 않는 회귀를 User/Group/Permission의 단건과 목록에 모두 적용했다.

Go overlay **14개 변형 / 28개 실제 assertion 탐지**: view/change 대체 제거, Group 생성에 불필요한 change 강제,
Group/Permission revision 검사 제거, group membership 생략, 유효 합집합 검사 생략, 첫 user batch만 처리,
직접 User/Group owner revision 미증가, audit 오류 무시, unknown 성공 게시, 호스트 대신 identity-only 삭제 정책 선택,
owner revision overflow 허용, 인가 실패를 권한 거부로 오인한 fallback.
Compile 실패·timeout·race는 탐지로 세지 않았다. 모든 checkpoint/control의 source 전후 동일,
PostgreSQL 잔여 table·owned schema·다른 connection `0|0|0`, private container 제거를 확인했다.
영향 `go vet`, CI Python **41/41**, format·diff·문서 링크 검사를 통과했다.

고정 Django 6.1 / Python **3.14.3**, SQLite **3.50.4**·PostgreSQL **17.10**에서 독립
`identity_catalog_reference.py`를 backend별 `PYTHONHASHSEED=0/813`으로 재실행했다.
Runner SHA-256은 `fceb04dec06cd7442a4d4e008d787deed82a54780b3a5c7e83a1637d9799e38c`다.
두 seed의 capture가 backend별 byte 동일했고, 양 DB의 관찰·upstream source hash와 기존 checked-in capture도 일치했다.
실제 Group/Permission 모델 변경·transaction·GroupAdmin 및 명시적으로 등록한 Permission ModelAdmin admission을 비교한다.
Django의 기본 Permission admin 등록이나 Go의 revision 정책을 Django 동작으로 주장하지 않는다.
Python **2/2**는 view/change 대체·Group add admission·저장 codename 변경의 3개 semantic mutation을 구분했다.
독립 reference의 table 0·DB 삭제·container 제거도 확인했다.

직전 source `029de452d9f08c5368942aba53462508647c20f29dc3965ec7cd8f8c01621fea`의 Article/siteapp/Helpdesk 포함
**13 packages / 763 entries**는 세 모드 각각 **1,665 PASS / skip 0**이었다.
이후 공통 view fallback이 인가 실패를 숨기지 않도록 보완하고 위 직접 영향 core/native를 새 source로 재실행했다.
이전 소비자 실행을 최종 source의 새 실행으로 표현하지 않는다.
초기 source `8834312718d7a97d57e0de5e6019060d68df2e98e3be6aa2320ad9fe0af92a29`는 감사 이력의 순서를
테스트가 역순으로 예상해 양 DB lifecycle assertion이 실패했다. 기존 AuditHistory의 ascending 반환 계약과 대조해 수정했다.
추가 설계 점검에서 관계 삭제에 따른 직접 owner revision 전파를 구현했으며 초기 실행을 완료 근거로 합치지 않는다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.
- `catalog-management-checkpoint-1790467698047895000/receipt.json`: 최종 세 모드·roster·원본 stream·source map.
- `catalog-management-controls-1790467748170461000/receipt.json`: 최종 14개 overlay·28개 assertion·cleanup.
- `catalog-management-reference-1790466765831718000/receipt.json`: 양 DB·두 seed·기존 capture와 byte 대조.
- `catalog-management-supplemental-1790467914175821000/receipt.json`: 같은 source의 vet·CI 도구/format/docs.
- `catalog-management-checkpoint-1790467183892292000/receipt.json`: 직전 source의 소비자 포함 세 모드.
- `catalog-management-checkpoint-1790466765830740000/receipt.json`: 초기 감사 순서 assertion 실패와 cleanup.

User/password·Group/Permission service까지 구현했다. 실제 관리 Form/Admin/API·독립 client, self-service/reset·unusable password/
last_login 등 남은 lifecycle과 GDJ-0100 전체 통합 milestone은 미완료다.

## GDJ-0100 — 기존 행의 기본값 backfill과 Permission revision migration

2026-09-27, `b6edcb8f` 뒤의 변경이다. Permission revision의 historical migration을 위해 scalar-default AddField를
양 DB의 revision-fenced lifecycle·자동 계획·SQL projection에 연결했다. `0001_initial` bytes는 유지하고 별도
`godj_identity.0002_permission_revision`이 기존 Permission에 revision 1을 채운다. Group/Permission 관리 service와
전용 Form/Admin/API/client의 완료를 뜻하지 않는다. 전체 platform/cold-build/Hosted milestone은 관리 소비자 통합 뒤에 둔다.

구현 commit `292745ea42b8df68c2b848ebef4cae418bceb432`를 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36279174264)는 이 commit의 실제 **Fast Go feedback**까지 성공했다.
게시 source map과 검증한 제품 bytes의 연결은 아래에 명시한다. 이 결과는 Hosted 전체 검증을 대체하지 않는다.

최종 Go checkpoint의 non-Markdown **2,303 파일** source map은
`8de9e3c1d47f2ee4bd7ac22d7b96e963482305c45ce045d47fe0004ee04731f7`이다.
Darwin arm64 / Go 1.26.5, SQLite·private PostgreSQL **17.10**, **13 packages / 985 required entries**
(780 roots·205 subcases)의 completion·no-skip을 검사했다. Migration·autodetect·project check·identity·systemstate·protocol,
양 native backend의 migration/identity 회귀와 실제 SYS-028 handler가 scope다. PostgreSQL의 직접 helper는 부모 process test가 실행한다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 2,662 PASS / skip 0 | 30.776초 |
| race | 2,662 PASS / skip 0 | 104.567초 |
| CGO=0 | 2,662 PASS / skip 0 | 27.736초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`와 mode별 `-race`/`CGO_ENABLED=0`을 사용했다.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`, private image는
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
SQLite/PostgreSQL의 실제 별도 process migration fence도 포함한다. 전체 identity management의 process/Hosted 증거로 확대하지 않는다.

16개 scalar 사례에서 ordinary typed INSERT로 만든 별도 expected column과 실제 backfill을 비교했다.
Add·reverse·reapply, incoming CASCADE FK·삭제된 ID 상한·최종 DB default 부재·누락 INSERT 동작을 확인했다.
false·빈 text·int64 최솟값·nullable 값·Float/Decimal/UUID/JSON/Date/Time/Duration/DateTime을 포함한다.
JSON null과 세미콜론·탭·제어 문자·따옴표·역슬래시 문자열은 별도 Go invariant다. SQL body 제한을 완화하지 않고 값을 보존한다.
Unique constant 충돌은 행·column·관계·history·revision을 rollback했다. Permission upgrade는 기존 PK/code/name,
User/hash/revision·직접/그룹 grant·호스트 CASCADE/SET_NULL/PROTECT 참조·session/audit bytes·로그인을 보존했다.
누락 migration의 startup 거부와 손상된 Permission revision의 부분 계정 미게시도 확인했다.

직전 source `daea1ec96297718fb494f4cc2aebba67894bc42ad1400fa812c172675197ced9`에서는 생성된 identity fixture와
Article/projectrunner/siteapp/Helpdesk를 더한 **18 packages / 941 entries**, 세 모드 각각 **2,834 PASS / skip 0**을 확인했다.
이후 SQL 문자열 escape·corrupt Permission revision 회귀·필수 roster를 보완하고 위 직접 영향 범위를 재실행했다.
소비자 실행을 최종 source에서 다시 수행한 것으로 표현하지 않는다.

Go overlay **9개 변형 / 12개 실제 assertion 탐지**: typed default를 0으로 교체(양 DB), PostgreSQL의 DROP DEFAULT 제거,
SQLite sequence 훼손·복사 행 누락·ROLLBACK 대신 COMMIT, identity migration admission 제거(양 DB),
양 dialect의 SQL literal escape 제거, Permission revision 검증 제거(양 DB).
DB final catalog·row/sequence guard가 잡은 실패와 테스트의 직접 rollback/인가 assertion을 구분해 원본 stream에 보존했다.
Compile 실패·timeout·race는 탐지로 세지 않았다. 마지막 control의 첫 실행은 필요한 선행 subcase를 제외해 잘못된 단계에서 실패했다.
이를 탐지로 인정하지 않고 같은 source에서 해당 전체 root를 실행해 정확한 invalid_permission_revision assertion 2개를 확인했다.
모든 checkpoint/control의 source 전후 동일, PostgreSQL table·owned schema·다른 connection `0|0|0`, container 제거를 확인했다.

독립 `migration_default_reference.py`는 고정 Django 6.1 / Python **3.14.3**의 실제 migration state·schema editor로
14개 default 사례를 SQLite **3.50.4**와 PostgreSQL **17.10**에서 각각 `PYTHONHASHSEED=0/813`으로 실행했다.
Backend별 capture는 두 seed에서 byte 동일하다. SQLite의 next_id 3과 PostgreSQL의 4 차이를 raw에 보존하며,
GoDj의 상한 보존은 [DEV-0013](../DEVIATIONS.md#dev-0013--sqlite-migration-remake에서-삭제된-id의-sequence-상한을-보존)의 정확한 14개 셀에만 적용한다.
최종 runner SHA-256은 `34e39331e236183883cee02d1ab60117191e4370067575c8a8651d7fb91aad8d`다.
Frozen DRF reference 환경의 Python **2/2**도 통과했고 effective_default를 0으로 바꾸면 실제 matched rows가 2→0으로 변했다.
독립 PG table 0·DB 삭제·container 제거를 확인했다. Group/Permission catalog의 별도 독립 capture도 준비했지만 Go 관리 구현의 PASS는 아니다.

Checkpoint 뒤 SQLite-only replay의 PostgreSQL schema import가 선택적 psycopg에 의존하는 문제를 수정했다.
Pinned package의 같은 source 파일을 import 없이 hash하도록 바꾼 게시 source map은
`1843ffc89a3d9add9e86c5acc830a3e456c471ae28a4d4e773606002a95663de`다.
전후 map에서 이 Python runner 한 파일만 달라졌고 Go·생성물·test/reference bytes는 동일함을 확인했다.
수정 runner로 양 DB·두 seed를 다시 실행해 기존 capture와 byte 동일함을 확인했다. Go checkpoint를 재실행했다고 주장하지 않는다.
이 source에서 네 프로젝트(identity·identityfixture·Helpdesk·Article)의 `generate --check`, 영향 `go vet`,
CI Python **41/41**·format·문서 링크 검사를 통과했다.

초기 checkpoint `cfafc5a6b05d18327bcd6cf13f6d3c24c43ef50c8b03b8a3786245d80d706983`는 defaulted AddField의 옛 기대값,
이전 adoption commit의 SYS-028 기준 파일 잠금 누락, expanded package와 roster 연결 오류 때문에 실패했다.
SYS-028만 ADR-0076 결정으로 변경됐고 독립 결정 함수와 일치함을 확인한 뒤 checksum·정확한 provenance/결과 기대를 갱신했다.
Django 소유 관찰은 변경하지 않았고 실제 Article startup handler를 세 모드에서 실행했다. 초기 실패를 최종 PASS에 합치지 않는다.
초기 생성의 별도 실패는 compiler overlay로 디스크 부족을 확인했다. GoDj 소유임을 재확인한 오래된 재생성 가능 cache 12.0 GiB를
정리한 뒤 실제 생성을 완료했다. source·사용자 문서·증거는 삭제하지 않았다. Cold download 진행이 실제 compiler 진단을 가리던
bounded summary도 수정하고 비밀 값 redaction을 포함한 회귀를 검증했다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.
- `default-backfill-checkpoint-1790464343329461000/receipt.json`: 최종 Go 세 모드·roster·원본 stream·source map.
- `default-backfill-checkpoint-1790463879276562000/receipt.json`: 직전 source의 생성 소비자 포함 세 모드.
- `default-backfill-controls-1790464360317291000/receipt.json`: 첫 8개 유효 변형과 마지막 잘못 선택한 control의 실패.
- `default-backfill-controls-1790464452909273000/receipt.json`: 같은 source의 마지막 control, 정확한 assertion 2개.
- `migration-default-reference-1790464560924426000/receipt.json`: 수정 runner의 양 DB·두 seed·원본 capture·cleanup.
- `default-backfill-supplemental-1790464600945647000/receipt.json`: Python-only source 변경 확인·네 generate-check·vet·CI 도구/format/docs.
- `default-backfill-checkpoint-1790463278498631000/receipt.json`: 초기 실패·정리 근거.
- `catalog-management-reference-1790461161494175000/receipt.json`: 다음 Group/Permission 관리에 사용할 독립 capture만의 근거.

## GDJ-0100 — 사용자 관리와 호스트 관계 삭제

2026-09-27, `69b74b232e4debfb531fb2b3f10d95f0e7d84b81` 뒤의 변경이다. 최종 non-Markdown **2,288 파일** source map SHA-256은
`5568b8b9459dfabe3ec077e652de29f5c3f92df0cf22914e4fead9b4518228e6`이다.
Darwin arm64 / Go 1.26.5, 실제 SQLite와 private PostgreSQL **17.10**, **10 packages / 537 required entries**
(427 roots와 명시한 password/user-management subcase 110개)의 completion·no-skip을 검사했다.
Auth·identity·ORM·systemstate·Admin·Web/API session·Bearer 전체와 native snapshot/identity/adoption roots가 최종 scope다.
Generated schema/ABI 변경은 없다. 새 관리 Form/Admin/API/client, 별도 process restart와 Hosted full은 이 checkpoint의 대상이 아니다.

구현 commit `48aefdb148e2f9422e5c37380e65524017bee71f`를 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36275314844)는 같은 commit의 실제 **Fast Go feedback**까지 성공했다.
게시 뒤 non-Markdown bytes가 위 checkpoint source와 같음을 확인했다. 이 결과는 Hosted 전체 검증을 대체하지 않는다.

| 모드 | 완료 inventory | 그룹 실행 시간 합계 |
|---|---|---|
| normal | 1,369 PASS / skip 0 | 15.065초 |
| race | 1,369 PASS / skip 0 | 95.875초 |
| CGO=0 | 1,369 PASS / skip 0 | 24.507초 |

`go test -json -count=1 -p=3 -timeout=20m -run <고정 roots>`에 mode별 `-race` 또는 `CGO_ENABLED=0`.
`GODJ_REQUIRE_POSTGRES=1`, `TZ=Pacific/Chatham`; PostgreSQL image는
`postgres:17.10-bookworm@sha256:9b18b78397054fce88a9552e9d5a3ad5bb7fd258c5b3cc1c5028e46373d6ea8f`다.
양 native 연결은 각각 max-open=1이며, 실제 서로 다른 연결의 create 경쟁은 한 번만 생성되고 나머지는 renderable unique rejection이었다.
Update 경쟁은 한 번만 revision을 증가시켰다. Hash 안에서 같은 backend의 인가 변경이 완료되어 preflight 읽기 종료를 확인했다.

생성·profile/role·그룹/직접 권한 편집, 생략/명시적 빈 집합, no-op·반환 slice 소유권, bounded page를 검사했다.
편집의 credential/stamp 보존과 다음 Resolve의 현재 grant, 비활성화의 session 폐기, runtime 재접속과 실제 HTTP cookie/login을 확인했다.
호스트 generated policy의 외부 Note CASCADE·Guard PROTECT, 관계 membership 정리와 Group/Permission 보존을 실제 행으로 검사했다.
삭제 응답은 ID·revision·건수 3개 field만 공개하며 borrowed delete는 caller model key를 바꾸지 않는다.
현재 actor 권한·deny overlay, 중복/없는 관계의 hash·write 생략, 유효 grant 256/257 경계,
update/membership/audit 실패·취소·unknown commit·늦은 취소와 읽기 종료/누락/nil/중복/삼킨 오류를 포함한다.
실패 시 profile·관계·외부 모델·session·audit의 rollback을 검사하고 지정한 fault 단계의 실제 실행을 확인했다.
Unknown outcome은 성공 DTO를 게시하거나 재시도하지 않는다. 별도 command receipt/idempotency API의 증거는 아니다.

고정 Django 6.1 / Python 3.14.3에서 독립 `identity_management_reference.py`를 실행했다.
실제 UserManager·UserAdmin add view·transaction으로 생성/편집/rollback/삭제/인가/Unicode email 정규화 6개 observation 그룹을 수집했다.
SQLite **3.50.4**와 PostgreSQL **17.10**, `PYTHONHASHSEED=0/813`의 backend별 capture가 각각 byte 동일했고 관찰과 upstream source hash도 일치했다.
Runner SHA-256은 `4a6895b2bf635b3659657ccefdd0207b5354e7fc47205e9a05066266eec8cec8`이다.
Python reference **2/2**는 username/email 정규화·view/change fallback 제거 3개 semantic mutation도 구분한다.
이 관찰은 전체 UserCreationForm의 validation·대소문자 무시 중복까지 검증했다는 뜻은 아니다.

Go overlay **9개 변형 / 17개 실제 assertion 탐지**: create의 change 권한 누락, revision 검사 제거,
원문 password 저장, group membership 누락, 비활성화 session 폐기 누락, audit 오류 무시, unknown 성공 게시,
full Unicode lowercase를 단순 lowercase로 교체(각 양 DB), borrowed delete의 session lifetime admission 제거(ORM).
Compile 실패·timeout·race는 탐지로 세지 않았다. Source 전후 동일, PostgreSQL table·owned schema·다른 connection `0|0|0`,
private container 제거를 확인했다. 독립 reference도 table 0·DB 삭제·container 제거를 확인했다.
영향 `go vet`, gofmt/diff·문서 링크, CI Python **41/41**을 통과했다.

직전 source `a8ac6f04d2ae22e121466fb9f0abaef14f49aa078263bd682117071145c04c71`에서는 Article·siteapp·Helpdesk를 더한
**13 packages, 세 모드 각각 1,533 PASS / skip 0**을 확인했다. 이후 Unicode 정규화·삭제 응답 제한과 해당 test/reference/roster를 보완했다.
최종 source에서는 직접 영향받는 core/native를 재실행했다. 위 소비자 결과를 최종 source의 새 실행으로 표현하지 않는다.

초기 source `9247ade76d9d6b43cab4bc9a07f927853c4f4bbb6f5155ce1234baae77ed3c2f`에서는 native read 종료가 callback error를
`errors.Join`으로 감싼 뒤 예상 입력 거부가 실행 오류로 분류되어 양 DB의 duplicate/missing-target·grant-limit 검사가 실패했다.
예상 거부를 읽기 데이터로 보관하고 정상 종료 뒤에만 공개하도록 고쳤으며 cleanup 실패가 겹치는 별도 회귀를 추가했다.
Python unittest 초기 URLconf import 실패는 `runpy.run_module(..., alter_sys=True)`로 actual module 등록을 바로잡았다.
각 실패 stream은 보존하고 수정 source의 PASS에 합치지 않는다.

Evidence 상위 경로: `/var/folders/4v/9w5s7mln3jbfcv13w9q38rzc0000gn/T/godj-many-to-many-reference-4sl0bvdp`.
- `user-management-checkpoint-1790459768792806000/receipt.json`: 최종 세 모드의 roster·원본 stream·전후 source map.
- `user-management-controls-1790459824690637000/receipt.json`: 9개 overlay와 17개 assertion 탐지.
- `user-management-reference-1790459613858405000/receipt.json`: 양 DB·두 seed 독립 capture와 cleanup.
- `user-management-checkpoint-1790459202555099000/receipt.json`: 직전 source의 13-package 소비자 포함 실행.
- `user-management-checkpoint-1790458963865035000/receipt.json`: 초기 native preflight 분류 실패.

사용자/password service까지 구현했다. Group/Permission 자체의 관리, 전용 Form/Admin/API·독립 client,
self-service/reset·나머지 credential lifecycle와 GDJ-0100의 전체 통합 milestone은 아직 미완료다.

## GDJ-0100 — 관리자 비밀번호 교체와 대상별 세션 폐기

2026-09-27, `9cb5085c` 뒤의 변경이다. Non-Markdown **2,279 파일** source map SHA-256
`a271708620a21c3f4667151ab414b6981c489fcbb4dadc9167a951f382e607c5`를 고정했다.
Darwin arm64 / Go 1.26.5, 실제 SQLite와 private PostgreSQL **17.10**에서 **12 packages / 283 required entries**
(227 roots와 명시한 password-management subcase 56개)의 completion·no-skip을 검사했다.
Auth·identity·systemstate·Admin·Web/API session·Bearer 전체, native snapshot/identity/adoption roots,
Article·siteapp·Helpdesk 소비자 전체를 선택했다. Generated schema/ABI 변경은 없으며 이 묶음의 별도 process/Hosted full은 아직 실행하지 않았다.

구현 commit `03b6be938fefcec985966458455461c277ccaa01`을 Draft PR #1에 게시했다.
[Hosted Fast](https://github.com/progresshans/godj/actions/runs/36271619201)는 해당 commit의 실제 **Fast Go feedback**까지 성공했다.
게시 뒤 작업 트리의 non-Markdown bytes가 위 checkpoint source와 같음을 다시 확인했다. 이 결과는 Hosted full을 대체하지 않는다.

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
