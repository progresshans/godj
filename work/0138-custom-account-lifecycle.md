# GDJ-0138: 사용자 정의 계정과 계정 수명

- 상태: I1 구현 중 — C1 수용 확인 후 주 작업 전환
- 통합: [PR #8](https://github.com/progresshans/godj/pull/8)은 merge `86c4ce01`로
  [PR #1](https://github.com/progresshans/godj/pull/1) 작업 브랜치에 합쳤다. 관리 JSON `11349e45`도 함께 통합한다.
  후속 변경은 완성된 코드 묶음 단위로 #1에 계속 반영하며, 최신 통합 source의 원격 CI와 미완료 수용을 추적한다.
- 시작: 2026-10-24
- 출발 source: `b19aeeddcfdf6ac2aef5bbf0102d5f13c8a0e1e0`
- 선행: A1·F1·C1의 완료 근거는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
  C1 source의 완료를 새 I1 코드의 원격 실행 PASS로 사용하지 않는다.
- 근거: [전체 실행 계획 I1](../docs/EXECUTION_PLAN.md),
  [credential과 session](../docs/adr/0076-credential-snapshots-and-session-binding.md),
  [재사용 앱과 host 관계 소유권](../docs/adr/0077-reusable-app-models-and-host-relation-ownership.md),
  [호환성 기준](../docs/COMPATIBILITY.md)

기본 User와 별도의 사용자 정의 모델이 같은 credential·session·permission·관리·login/reset 경계를 사용하게 한다.
모델과 필드 이름을 바꾸고 추가 프로필을 선언해도 숨은 기본 User 행이나 수동 SQL adapter를 요구하지 않는다.
기존 데이터가 있는 모델 전환은 source 설정 변경과 구분한 명시적 작업이다.

## 요구와 실제 소비자

| ID | 필요한 결과 | 완료에 필요한 확인 |
|---|---|---|
| I1-01 | 프로젝트가 하나의 계정 모델과 필드 역할·login 정책을 선택한다. | 별도 app/model·다른 unique login 필드와 잘못된 모델/타입/중복/관계 참조를 생성 및 I/O 전 검사한다. 생성·수정·인증의 Unicode와 대소문자 정책을 명시한다. |
| I1-02 | 사용자 정의 필수 프로필을 포함해 첫 계정을 생성한다. | 공개 프로젝트 명령과 생성 Form/Admin/API가 같은 타입·기본값·validation 의미를 사용한다. 잘못된 입력이나 실패가 부분 계정을 남기지 않는다. |
| I1-03 | 역할·직접 권한·그룹 권한을 읽고 변경한다. | active/staff/superuser·현재 권한·snapshot·revision·권한 한도와 관계 소유권을 유지한다. 다른 모델이나 오래된 권한으로 쓰지 못한다. |
| I1-04 | login, password change/reset, session 폐기를 연결한다. | 사용자 정의 email 참조, inactive/unusable 제외, 해싱 한도·재검사·동시 reset·비밀값 비노출과 restart를 실제 HTTP/DB에서 확인한다. |
| I1-05 | 추가 프로필을 typed Form/Admin/API에서 조회·편집한다. | hash·principal identity·revision·서버 timestamp와 권한 필드를 임의 입력으로 덮거나 노출하지 못한다. 거부·stale revision 뒤 durable 상태를 대조한다. |
| I1-06 | host 관계와 계정 삭제를 함께 처리한다. | 외부 모델의 CASCADE/PROTECT/SET_NULL, 권한 관계, session 폐기와 audit의 원자성·rollback·unknown outcome을 확인한다. |
| I1-07 | 채운 기본 계정 DB를 새 모델로 명시적으로 전환한다. | 저장된 모델/domain과 설정의 불일치를 거부한다. row ID·principal ID·hash가 우연히 같은 다른 모델이 session/reset/권한/audit를 승계하지 못한다. mapping·history·receipt·복구·restart를 확인한다. |
| I1-08 | 기본 및 사용자 정의 계정을 실제 새 앱에서 사용한다. | scaffold→generate/migration→첫 계정→인증된 프로필/권한/password/reset/비활성화/삭제→restart를 SQLite·PostgreSQL과 독립 HTTP/browser에서 실행한다. 고정 Django 공유 의미와 Go의 소유권·경합 의미를 별도로 대조한다. |

## 구현 경계

- 계정 역할은 `schema/ir`의 명시적 model/field/relation 참조다. 프로젝트의 선택을 정규화한 뒤 생성 snapshot과
  typed adapter에 연결한다. `username`, `email` 등의 문자열 이름에서 역할을 추론하지 않는다.
- 계정 선택과 보안 정책은 프로젝트 binding이다. DB table/column의 역사와 혼동해 이미 적용한 migration 정의를
  바꾸지 않는다. 선택 변경의 durable 의미는 별도 domain/전환 기록으로 검증한다.
- 구체적인 모델 접근은 생성 descriptor·typed ORM·동일 Query AST를 사용한다. 공통 runtime이 snapshot,
  해싱, 최종 권한/credential 재검사, revision, session/reset/audit와 실패 결과를 소유한다.
- Group/Permission을 포함한 설치 앱은 완전한 source·ABI binding을 갖는다. 기존 세 모델 External 앱을
  두 모델 선언에 연결하지 않는다. catalog/default-account의 배치와 기존 migration graph 전환은 아직 설계 중이다.
- 프로필 입력은 A1/F1의 준비·validation·명시적 노출 정책을 재사용한다. 생성된 Go 타입과 추가 필드 의미를
  공통 `map[string]any` 입력이나 기본 User 사본으로 대체하지 않는다.
- 기존 operator adoption의 의도된 identity 보존과 임의 모델 교체를 구분한다. 새 모델/domain의 session stamp,
  reset 수명, 현재 권한과 startup 검사까지 함께 완성하기 전에는 새 계정 runtime을 제품 완료로 처리하지 않는다.
- S1의 추가 key 타입, I2의 token/OAuth/OIDC, X1의 일반 Django 데이터 이행 요구는 각 담당 묶음에 남긴다.
  현재 I1에 필요한 명시적 모델 전환을 이 후속 작업으로 미루지 않는다.

## 진행과 검증

계정 역할 IR·프로젝트 wire·source digest·생성 snapshot과 typed `BindAccount`를 구현했다. 선택적인 서버 생성 시각과
login/인증/email의 명시적 정규화 정책을 같은 역할 fingerprint에 포함한다. 기본 User와 별도 Member·catalog·host FK
모델이 전체 host 관계 그래프에 묶이며, 기본 모델의 숨은 행이나 수동 SQL adapter를 요구하지 않는다.

독립 `godj_account_domain` 앱은 추가 전용 소유권 이력·직전 receipt·새 세대/operation ID와 작업 조회를 제공한다.
같은 DB scope의 현재 역할과 actor domain을 검사한다. 초기화·전환 receipt는 commit 전 잠정 값이며 이 API만으로
채운 DB의 mapping/retirement·session/reset 정책을 수행하거나 전환을 승인하지 않는다.

생성 팩토리와 공통 `CredentialStore`가 선택 계정·domain·직접/그룹 권한을 한 native snapshot에서 읽는다.
공개 reverse accessor가 없는 custom catalog도 forward M2M과 공통 Query AST를 사용한다. 인증은 선택한 입력 정책,
해싱 한도와 검증 뒤 현재 credential/domain/active/권한 재검사를 유지한다. `AuthorizeIn`은 오래된 domain을 먼저
거부하고 같은 borrowed scope의 현재 actor와 deny overlay를 확인한다.

공통 `ProfileEditor[M]`와 생성 팩토리는 명시한 profile 필드의 Form/JSON 입력을 받아 현재 권한·target revision·
typed unique 검증·강제 update·audit를 같은 native coordinated transaction에서 처리한다. 실제 변경만 revision과
감사를 추가하고 no-op도 현재 권한·대상 무결성을 확인한다. Credential·서버 시각·role·collection 입력은 거부한다.
FK/OneToOne과 파일 편집은 아래의 명시적 정책 확장을 따른다.
기본/사용자 정의 외부 소비자는 ModelForm과 API body binding으로 실제 typed 입력을 준비한다.

독립 `godj_account_audit` 앱과 초기 migration은 기본 User 없이 domain별 감사 기록을 저장한다. 현재 actor를 사용한
append와 정확한 domain별 bounded history, 수정/audit 실패의 원자 rollback, stale/회수/domain 변경·대상 손상·
validation·unknown 결과·revision 경합 검사를 양 DB 필수 목록에 연결했다. 명시적으로 존재하는 0/음수 계정 key도 유지한다.

최소 compile과 생성 manifest, 확인된 실패의 한정 재현·수정을 수행했다. 기본/사용자 정의 외부 생성 소비자의
실제 SQLite·HTTP profile/catalog/session 흐름도 한정 확인했다. 현재 source의 전체 필수 단위·DB·race/platform 수용은
원격 확인 대상이다. 상세와 이전 source의 실패/수정 결과는 TEST_EVIDENCE에만 둔다.
작성·compile이나 한정 실행을 I1-01~08 전체의 제품 수용으로 계산하지 않는다.

생성 `ProfileReader[M]`는 현재 인가·domain·선택 프로필 조회/목록/이력을 같은 native snapshot에서 처리한다.
`ModelView[M]`는 전체 모델 행 없이 선택한 값과 모델 source를 보관하며 공통 Form/API 출력이 같은 source와
로드 여부를 검사한다. 종료 실패·권한 회수·domain 변경·대상 손상·일관된 count/page·0/음수 key를 양 DB 필수
검사에 연결했다. 기본/사용자 정의·정규화 변형의 외부 소비자는 실제 HTTP 로그인·CSRF·Form/JSON 수정·조회와
stale/예약 필드 거부·기존 세션의 권한 회수/복구를 실행하도록 작성했다. 초기 SQLite 소비자는 아래에서 PostgreSQL에도 연결하며,
새 양 DB 본문의 원격 실행과 수용은 별도로 확인한다.

Catalog 조립의 기반으로 선택 ModelView의 M2M·Form/API 표현과 전체 host source에 대한 collection/삭제 기능 검사를
추가했다. ModelInput/ModelView 진단 형식의 선택 값 노출과 소속 그룹 256개 한도 누락을 재현해 보완했고, actor와 target의 현재 상태를
확인하는 양 DB 필수 검사에 연결했다. 이 기반만으로 catalog CRUD·역할 변경의 구현 완료를 의미하지 않는다.

이어서 공통 `Catalog[G, P]`와 생성 `NewAccountCatalog`를 작성했다. 실제 typed Group/Permission의 현재 인가·조회·
목록·생성·수정·삭제·현재 domain 이력과 명시적 노출 정책을 연결한다. 추가 scalar 필드는 같은 ModelForm/JSON·IR
준비를 사용하고, 권한 확장은 모든 해당 회원의 유효 합집합을 검사한다. 직접 owner revision·전체 host 관계 삭제·
audit의 원자성, 0/음수 key·뒤 batch 실패·동시 확장·권한 회수와 unknown 결과 검사를 양 DB 필수 목록에 추가했다.
별도 모델·NoReverse·추가 정수/텍스트 필드를 가진 외부 생성 소비자도 같은 팩토리를 사용하도록 확장했다.
작성·생성·compile과 이 새 본문의 환경별 실행은 구분하며 결과는 TEST_EVIDENCE에 둔다.

추가 계정/catalog 행을 CASCADE/SET_NULL로 바꾸는 행 삭제는 아래의 전체 수명 owner로 조정한다.
Collection 교체의 여러 삭제를 같은 owner로 합치는 작업은 I1-06에 남아 있으며, 해당 through 조합은 시작 시 명시적으로 거부한다.

독립 `godj_sessions` migration·typed DB Store와 선택 계정의 `SessionStore[M]`/생성 팩토리를 작성했다.
현재 durable domain과 세션은 같은 snapshot/fence에서 처리하고 역사 namespace를 승계하지 않는다. Typed LoginRecorder와
정확한 manager의 LoginPersistence는 현재 credential·권한·admission 재검사, 선택적인 LastLogin과 세션 교체를 원자적으로
묶는다. 현재 domain의 특정 principal 폐기는 같은 borrowed scope에서 제공한다. 기존 systemstate migration/digest와
canonical payload는 유지하며 이 저장 기능이 기존 데이터 이행·인가·계정 관리 owner를 대신하지 않는다.

Namespace/전체 용량·batch rollback·scope/cleanup/unknown·동시 생성·domain snapshot·login 재검사와 재접속을 양 DB
필수 검사에 추가했다. 외부 기본/사용자 정의 HTTP 소비자는 memory Store를 이 DB Store와 LoginPersistence로 교체하여
실제 cookie의 저장 행·선택 LastLogin을 대조하도록 확장했다. 새 본문 실행과 이전 catalog CI 실패의 한정 수정 재현은
서로 구분하며 TEST_EVIDENCE에 기록한다.

같은 SessionStore를 받는 생성 `NewAccountEditor`와 typed `AccountEditor[M]`는 명시한 scalar profile·active/staff/superuser와
직접 그룹/권한을 수정하며, 현재 인가·revision·현재/제안 소속과 유효 합집합의 한도·감사·활성→비활성 세션 폐기를
하나의 native relation transaction에 묶는다. 상세 조회는 선택한 소속을 제공하고 목록은 scalar만 읽는다.
역할 source 혼합·상수 역할·부정 입력과 양 DB의 생명주기·선택/한도·실패/rollback·경합 검사를 추가했다.
기본/renamed/NoReverse 소비자의 실제 HTTP Form·JSON 역할 수정과 대상 브라우저 세션 폐기 검사도 작성했다.
작성·compile과 이 새 본문의 실제 환경별 실행은 TEST_EVIDENCE에서 구분한다.

생성 `NewAccountCreator`와 공통 `Creator[M]`를 작성했다. 필수 scalar profile·IR default·정규화·서버 identity/revision/시각,
현재 add+change/추가 권한·소속/합집합·unique·typed/password policy의 사전 검사와 DB 밖 한 번의 hash를 연결한다.
하나의 creator·actor·domain에 묶인 준비 결과는 한 번만 저장 시도하며, 최종 재검사·행/소속/audit를 같은 native scope에서
확정한다. 해시 전에는 해당 필드를 포함한 unique만 미루고, 실제 hash를 넣은 최종 후보는 전체 제약을 검사한다.
명시적 unusable·raw 한도·권한/정책/선택 변경·0/음수 선택 key·실패/cleanup/unknown·중복 생성 경합을 양 DB 필수 목록에
추가했다. 기본/별도 Member의 실제 TCP Form·JSON 생성, 필수 region·예약 입력 거부·생성된 credential 로그인 소비자도
작성했다. 최소 compile과 실제 실행 범위는 TEST_EVIDENCE에서 구분한다. 첫 계정 명령이나 공통 설치 완료는 아니다.

생성 `NewFirstAccount`와 `FirstAccount[M]`를 별도 operator owner로 작성했다. 공통 `CreationPolicy`와 실제 typed
필수 profile/default·validation·DB 밖 hash를 재사용하며 active/staff/superuser를 서버에서 고정하고 상수 false는 거부한다.
현재 domain history와 선택 계정 행의 정확한 부재를 사전/최종 scope에서 확인한 뒤 domain·행·소속·첫 audit를 한 번에
확정한다. Unknown 결과는 게시하지 않고 상위 process가 지정한 operation ID를 보관해 명시적 receipt 대조에 쓸 수 있다.
Fresh-only·0/음수 기존 key·재사용/경합·실제 저장 뒤 rollback/unknown 검사를 양 DB 필수 목록에 연결했다.
필수 custom profile의 생성·HTTP login 외부 SQLite 소비자도 추가했다. 본문 실행·최소 compile·원격 확인은
TEST_EVIDENCE에서 구분한다. 공개 명령 연결은 아래 통합 조립을 따른다.

DB 연결 전 검사를 위해 생성 `NewFirstAccountDefinition`과 typed 선언/실제 backend binding을 분리했다.
전체 host source·역할·정적인 정책과 명시적 callback evidence를 식별하며 가짜 backend를 만들지 않는다.
`project/firstaccount.Command`는 같은 ModelForm의 실제 scalar 필드·선택값·필수/default 의미로 입력을 준비한다.
생략/명시적 빈 값·한도·source 혼합·취소·한 번의 시도와 acquired resource의 소유권을 검사하고, 실제 최초 owner에 연결한다.
외부 기본/custom 소비자는 이 경로를 통해 생성·receipt/audit·HTTP/DB-session을 검사하도록 바꿨다.
선택 필드의 기본값·빈 값·문자열 정리와 custom 정확한 선택값도 포함한다. 생성·최소 compile과 본문 실행은
TEST_EVIDENCE에서 구분한다. 터미널/프로세스의 공개 명령 연결과 scaffold runtime 구현은 아래 통합 조립을 따른다.

같은 `SessionStore`의 인증기와 manager에 묶인 선택 계정 자기 비밀번호 변경 owner를 작성했다. 생성과 공유하는 실제
typed password 정책·DB 밖 이전 검증/한 번의 hash와 최종 현재 계정/세션 재검사를 사용한다. 선택 Password/Revision,
현재 세션 회전·같은 principal의 다른 세션 폐기·domain audit를 하나의 native transaction에서 확정한다. LastLogin과
현재 profile/권한·독립 CSRF는 유지한다. 읽기/해싱/부분 쓰기·뒤 batch·cleanup/unknown·만료·경합과 entropy 한도를
양 DB 필수 목록에 추가했다. 기본/custom/정규화/LastLogin 없는 외부 소비자는 기존 계정 Application의 실제 Form/JSON
비밀번호 경로에 이 owner를 연결한다. 새 본문의 실행 여부는 TEST_EVIDENCE에서 구분한다.

공통 계정 로그인 Form이 선택 인증기의 불변 metadata를 사용하도록 전환했다. 실제 login field의 label·문자 한도와
저장/인증 조회와 별개인 명시적 Form 정규화 정책을 IR·private wire·생성 binding에 연결한다. Metadata가 없는 구성은
시작 시 거부한다. 기본/custom/인증 NFKC/Form NFKC/짧은 login/Text login/LastLogin 없는 외부 생성 소비자는
공통 로그인에서 실제 CSRF·정규화·길이·redirect·durable session을 확인하고 이어 같은 Form/JSON 비밀번호 경로를 사용한다.
앞선 password CI 실패에서 잘못된 bound-session 호출과 credential 읽기 범위 검증 누락을 재현해 수정했다.
한정 재현과 새 로그인 본문의 실행 상태는 TEST_EVIDENCE에서 구분한다. 후속 Admin·관리/reset 조립은 아래 진행과 같다.

선택 계정의 생성 `NewAccountPasswordEditor`와 공통 `PasswordEditor[M]`를 작성했다. 같은 세션/인증기와 전체 host source,
현재 관리 권한·추가 권한·target revision·기존 credential·typed password 정책을 검사한다. DB 밖 한 번의 hash 뒤 같은
native transaction에서 Password/Revision·모든 대상 세션 폐기·현재 actor의 domain audit를 확정한다. Inactive/unusable
대상 복구와 반복 unusable 표식 회전도 지원한다. 자기 대상의 관리자 변경도 기존 브라우저를 보존하거나 자동 로그인하지 않는다.

공통 Admin 명령에 추가 권한 목록을 연결하고 `identityadmin.PasswordCommands`로 private confirmation Form·byte 한도·
오류 분류를 조립했다. 기본/custom/LastLogin 없는 최초 계정 소비자는 실제 Admin Site·TCP·CSRF·stale/추가 권한 거부·
변경/disable·기존 브라우저 폐기와 선택 필드 audit를 확인하도록 확장했다. 양 DB는 현재 상태 재검사·부분 쓰기/뒤 batch·
cleanup/unknown·관리자/자기 변경/login 경합·0/음수 key 검사를 포함한다. 작성·생성·최소 compile과 본문 실행은
TEST_EVIDENCE에서 구분한다. 이 명령 조립은 자동 account/catalog Admin 등록을 완료한 것은 아니다.

선택 계정 reset을 위한 공통 key ring·상태 서명·email cleaner를 `auth`에 두고 기존 reset owner와 소비자를 전환했다.
메일러는 모델을 직접 조회하지 않고 저장 owner가 같은 snapshot에서 만든 수신자/token 전체를 받는다. 서명은 credential
domain과 실제 key·email·LastLogin을 사용하며, 메일 source와 완료 persistence의 정확한 owner도 startup에서 검사한다.
기존 native 기준과 DB/HTTP 검사는 유지하고 토큰/입력 단위는 해당 공통 package로 옮겼다.

생성 `NewAccountPasswordResetter`와 공통 typed reset owner를 작성했다. 같은 SessionStore·인증기·manager와 전체 host
source에 묶이며, 명시적 Email/Active·실제 signed key·LastLogin·domain을 사용한다. 메일 후보는 하나의 native snapshot에서
완전한 bounded 집합으로 준비하고, 실제 DB의 case-insensitive prefilter 뒤 Unicode 동등성을 확인한다. 재설정은 DB 밖 한 번의
hash 뒤 현재 proof·인증 binding·계정·typed 정책과 별도의 session/token clock을 다시 확인한다. Password/현재 Revision,
모든 대상 세션 폐기·proof 회전/자기 로그인 제거와 실제 필드의 domain audit는 같은 native transaction에서 확정한다.

읽기/cleanup·부분 DML·뒤 batch·unknown/cancel·현재 상태/만료·entropy·reset/login/관리/자기 변경/비활성화 경합과 0/음수
key를 양 DB 필수 목록에 추가했다. 같은 고정 Django 후보 자료를 기본/선택 owner가 공유한다. 별도 생성 소비자는 기본 User,
renamed Member·기본 키/별도 Text email·Email/LastLogin 부재·상수 active를 사용하며 실제 TCP Form/JSON·메일 링크·CSRF·
재접속/owner 재구성·키 회전과 모델/세대 사이의 proof 거부를 SQLite/PostgreSQL에서 실행하도록 작성했다. 같은 key/principal/hash를
가진 다른 실제 모델의 부정 검사는 채운 DB 전환 제품의 완료가 아니다. Compile과 새 본문의 환경별 실행은 TEST_EVIDENCE에서
구분한다. 이 소비자의 재접속은 별도 process restart나 공통 scaffold/runtime 전체 조립을 대신하지 않는다.

선택 계정의 private wire와 `project/firstaccount/runner`를 실제 명령 Registry에 연결했다. 선언 조회는 자원을 열지 않으며,
정확한 source·생략/빈 값·typed ModelForm 검사를 DB 연결 전에 수행한다. 한 번의 입력 전달/생성/close와 operation에 묶인
확정·unknown·cleanup 결과를 분리하며 panic/Goexit·늦은 취소·출력 실패를 포함한다. 기본/custom/LastLogin 없는 생성
소비자는 실제 별도 DB 연결과 이 Registry 경로를 통해 첫 계정·receipt·HTTP 로그인을 확인하도록 전환했다.
새 본문 실행은 원격 대상이며 최소 compile과 확인된 CI 시간 제한의 실행 소유권 수정은 TEST_EVIDENCE에서 구분한다.
공개 터미널·상위 process·scaffold/runtime 전환을 완료한 것으로 계산하지 않는다.

Admin Site와 일반 계정 Application의 로그인을 같은 `forms/auth` 정의에 연결했다. 선택 인증기의 불변 label·문자 한도·
Form 정규화와 username/password byte 한도를 사용하고, metadata 누락은 Site 시작 시 거부한다. 엄격한 파싱·CSRF 뒤의
Form 검사와 active staff/추가 권한의 선행·최종 admission, 동일 세션 owner의 원자적 저장을 유지한다.
기본/custom/인증 NFKC/Form NFKC/짧은 login/Text login/LastLogin 없는 첫 계정 소비자를 실제 Admin에 연결해
입력·권한 거부의 durable 불변과 성공 시 선택 session/LastLogin·안전한 redirect를 확인하도록 확장했다.
로컬 최소 compile과 새 단위·HTTP 본문의 원격 실행 상태는 TEST_EVIDENCE에서 구분한다.

통합 Admin 수정의 기반으로 기존 역할 편집 owner를 AccountEditor로 확장했다. 선택 프로필의 login/email 정규화와
모든 역할/소속 입력을 같은 현재 후보로 검증하고 한 번의 revision·전체 감사·비활성화 세션 폐기로 확정한다.
생성 factory와 실제 소비자를 새 API로 전환했다. 양 DB의 복합 정책/unique 거부·부분 쓰기/cleanup/unknown·동시 수정과
기본/custom 실제 Form·JSON의 프로필/소속 수정 및 비활성화를 검사하도록 확장했다. 생성/최소 compile과 새 본문 실행은
TEST_EVIDENCE에서 구분한다. 이 owner와 소비자는 공통 account/catalog Admin 자동 등록의 완료가 아니다.

공통 Admin의 CRUD와 객체 명령은 `MutationResult`로 확인된 ID/revision만 전달하도록 바꿨다.
조회용 M/Snapshot/Initial을 저장 성공의 반환 조건으로 요구하지 않는다. Registry는 대상·revision·변경 필드의
계약을 검사하며 잘못된 성공 결과는 reconciliation 오류, owner 실패는 원래 오류로 유지한다. 기존 기본 계정·
Article/Helpdesk·typed inline/collection 소비자를 같은 계약으로 연결했다. 선택 계정 HTTP 소비자는 native
AccountEditor를 통해 자기 profile+비활성화의 한 commit, 모든 자기 세션 폐기, 성공 응답 뒤 조회 권한 거부를
검사하도록 작성했다. 기존 조회 검증을 없애거나 공통 계정 자동 등록을 완료한 것은 아니다.

계정/host 삭제 조정의 기반으로 공통 삭제기가 모든 CASCADE·SET_NULL 행을 쓰기 전에 수집하도록 확장했다.
`DeleteWithEffectsInSession`은 PROTECT와 전체 조회/정리가 끝난 뒤 같은 borrowed transaction에 불변 영향 목록을 제공한다.
NULL 변경도 수집한 정확한 key/FK에 한정하고 전체 집합을 batch로 실행하며, 변경 수 불일치는 실패시킨다.
생성 정책을 사용하는 양 DB 소비자에 동반 쓰기·뒤 실패/rollback·unknown·새 참조/재지정·batch·0/음수 key 검사를
작성했다. 최소 compile과 실제 본문 실행은 TEST_EVIDENCE에서 구분한다.

생성 `NewAccountDeleter`와 typed 계정 삭제 owner를 작성하고 catalog 행 삭제를 같은 수명 조정에 연결했다.
정확한 SessionStore·전체 host source에서 모든 직접 계정/catalog 효과와 실제 소속 집합 변화를 합친다. 각 선택 모델의
현재 delete/change·추가 권한, 전체 typed 상태/한도·revision을 먼저 확인하고 생존 owner revision을 한 번만 증가시킨다.
삭제 계정의 현재 domain session과 직접 효과 audit를 원자적으로 처리하며, 중복 through 일부 제거·삭제/NULL 중복·자기 삭제를 구분한다.
생성 ABI v15와 기존 소비자를 전환했다. 별도 custom 모델·명시적 through·교차 host graph를 생성하고 양 DB의 전체 durable
비교·거부·뒤 쓰기 실패·unknown·독립 connection 경합·0/음수 key·257개 owner 검사를 작성했다. 일곱 외부 생성 변형에는
실제 HTTP login·CSRF·stale/추가 권한/host 보호 거부·삭제·두 대상 브라우저 폐기 소비자를 추가했다.
최소 compile·작성과 실제 본문 실행은 TEST_EVIDENCE에서 구분한다. 공통 Admin/HTTP 등록은 남아 있다.

여러 collection을 한 번에 조정하기 위해 typed `SetKeysChange`와 같은 session의 `PrepareCollectionChangesInSession`을 작성했다.
공통 source·전체 root 합집합·공유 cascade/NULL과 최종 생존 링크를 먼저 확인하고, typed 삽입과 모든 최종 collection 조건을
한 번 실행한다. 복사본의 재실행·scope 만료·뒤 실패는 성공으로 게시하지 않는다.
새 native 소비자는 여러 타입의 생성 factory와 독립 DB connection에서 공유 자손·교차 cascade·중복 링크·0/음수 key·실제 뒤
쓰기 실패와 전체 snapshot을 대조하도록 작성했다. CI가 드러낸 explicit-through migration 순환 분리 오류도 수정했다.
다른 계정 domain으로 전환하는 소비자 검사는 선택 모델의 HTTP 삭제 검증 뒤에 실행하며, PostgreSQL의 기존 signed key
삭제 검사는 별도 native fixture로 준비한다. 한정 재현·최소 compile·새 본문 실행은 TEST_EVIDENCE에 구분한다.

계정 편집과 catalog 생성/수정/삭제를 같은 전체 수명 owner로 연결했다. 전체 collection 의도·연쇄 삭제/NULL·typed 삽입을
합쳐 현재 및 최종 typed 상태·실제 distinct 소속·직접/간접 유효 권한 한도를 확인한다. 직접 효과별 권한과 root 전체 change
정책, 한 번의 owner revision·direct audit·삭제/비활성 계정 session 폐기, 최종 내부 재조회가 하나의 fence에 속한다.
편집 root가 삭제되면 conflict이고, 생성의 provisional row나 새 링크 상태가 추가 권한을 만들어 주지 않는다. 일반 through의
incoming critical 효과를 시작 시 일괄 거부하던 경계는 이 조정으로 대체했다. 선택 계정/catalog 자체가 through인 저장에는
별도의 identity 생성 owner가 필요하므로 해당 생성 조립은 여전히 남아 있다.

새 native 소비자는 실제 Member/Team/Grant와 explicit/자동 through에서 root·추가 계정/catalog의 겹친 cascade/NULL,
보존 링크 재생성·중복 링크·0/음수 key·자기 비활성화·최종 256/257개 경계, 전체 snapshot rollback과 뒤 batch/마지막 root
쓰기/최종 읽기·unknown을 대조하도록 작성했다. Catalog 생성 후 링크를 만드는 native-session 주입도 같은 transaction의
권한 고정·validator 거부와 실제 row/session/audit를 확인한다. Backend trigger DDL 자체를 시험했다는 의미는 아니다.
이전 CI의 fake package discovery, 저장 key fixture와 잘못된 owner callback의 outcome 기대도 보정했다.
새 본문 실행·전체 원격 검증을 제품 코드와 최소 compile 완료로 대신하지 않는다.

선택 `SessionStore.InspectStartup`과 독립 domain/audit/session의 borrowed inspection을 작성했다. 명시적으로 적용한
framework-owned migration 이력을 확인한 뒤 하나의 native 데이터 snapshot에서 전체 소유권 chain·고유 operation/generation,
선택 account/catalog/소속 column, 모든 감사와 session namespace를 읽는다. Fresh/Ready·domain 불일치와 orphan 계정/audit를
구분하고 초기화·정리·repair나 캐시 권한으로 사용하지 않는다. Migration 조회는 데이터 snapshot과 별개이며 프로젝트 graph와
schema index/constraint 전체 검증은 호스트가 소유한다.

양 DB의 빈 저장소·0/음수 기존 key·필수 migration/column 접근·과거/뒤 page 손상·중복 generation/operation·조회 수명 실패와
동시 domain/audit/session 변경 검사를 필수 목록에 연결했다. 기본/custom 최초 계정과 재접속하는 reset HTTP 소비자에도
실제 기동 관측을 연결했다. CI가 드러낸 collection 감사의 반환 순서 불일치를 고치고, 바뀐 쓰기 순서의 실패 주입은 세션/감사·
소속·최종 scalar 뒤 각각의 rollback 확인을 유지한다. 새 본문 작성·최소 compile·확인된 실패의 한정 실행은 TEST_EVIDENCE에서
구분하며 공개 CLI/scaffold의 선택 계정 전환을 완료한 것으로 계산하지 않는다.

선택된 실제 Group/Permission 타입을 받는 `identityadmin.RegisterCatalog`를 작성했다. Owner의 source·역할·명시적 읽기/쓰기와
전체 operation/추가 권한을 공통 Admin 목록·Form·CRUD·durable history에 연결한다. 그룹 권한 선택은 현재 인가된 native
snapshot에서 bounded 검색/Resolve하고, partial-invalid Form의 독립 unique·복합 unique·선택 key 검사도 같은 읽기 경계를
사용한다. 최종 쓰기는 기존 수명 owner에 위임하며 성공 뒤 다시 읽지 않는다.

공통 Admin의 모든 모델 작업에 필수 추가 권한을 적용하고 직접 호출·현재 overlay·descriptor 소유권 검사를 추가했다.
양 DB의 Form partial validation·revision/부재·권한 회수/inactive/old domain·cleanup/취소·signed key 검사를 필수 목록에
연결했다. 기본/custom 일곱 생성 변형에는 실제 TCP Admin 등록·범위가 있는 선택지·Form 오류/CSRF/stale/추가 권한 거부·
생성/수정/삭제·수집된 소속/revision·감사를 작성했다. Custom은 revision default 없이 서버 값과 추가 scalar/복합 unique도 다룬다.
작성·최소 compile과 실제 원격 본문 실행은 TEST_EVIDENCE에서 구분한다. 기존 수명 감사의 독립 기대값은 실제 모델 선언
순서에 맞췄고 CI가 관측한 cold CLI 준비/누적 시간은 검사 삭제 없이 harness 예산으로 보정했다.

선택 계정 자체의 `NewAccountManagement`와 `identityadmin.RegisterAccount`를 작성했다. 같은 SessionStore·인증기·전체 host
source에 생성/조회/수정/삭제/비밀번호 owner를 묶고 명시적 read/create/edit 노출과 전체 operation/추가 권한을 공통 Admin에
연결한다. 두 종류의 현재 소속 선택지, partial-invalid Form의 정규화/unique/선택 key 검사, 명시적 unusable 생성과 private
비밀번호 입력을 제공한다. 실제 profile 이름과 command 이름의 충돌을 피하며 저장 성공 뒤 재조회하지 않는다.

양 DB에는 읽기 전용 Form의 부분 오류·현재 행 제외·signed key·권한 회수/inactive/old domain·실패 scope/cleanup을 추가했다.
기본/custom 일곱 생성 변형에는 자동 등록 자체의 실제 TCP 생성·bounded 선택·보호/권한/비밀번호 오류·수정/history·비밀번호
명령·자기 비활성화/두 세션 폐기·삭제/audit 소비자를 작성했다. 새 본문 작성·최소 compile과 원격 실행은 TEST_EVIDENCE에서
구분한다. Account-project ABI v16으로 관리 중인 12개 project bundle을 갱신했다.

관리 JSON의 공통 권한을 위한 `api.PermissionPolicy`·Session/Bearer `RequirePolicy`와 `endpoint.Permissions`를 작성했다.
필수 추가 권한과 view/change 대안을 한 번의 인증·CSRF 뒤 확인하며 같은 선언으로 OpenAPI를 구성한다.
확정 거부·인가 오류·취소·권한 없는 principal을 overlay가 허용하는 경우와 body 이전 admission을 원격 필수 검사에 추가했다.
선행 CI의 Admin 필드 순서·선택지 query·이메일형/동일 역할 로그인 fixture 오류를 재현·보정했고, 기본/custom/Text 로그인
세 생성 소비자의 한정 재실행을 확인했다. 새 API 정책의 최소 compile과 실제 본문 검증은 TEST_EVIDENCE에서 구분한다.

모델 field와 credential 명령을 별도 객체로 결합하는 `api/input.Object`·`ObjectField`와 source를 유지한 공통 decoder를
작성했다. 중첩 전체에 한 parser 예산을 적용하고 각 child의 기존 Spec으로 full/partial·nullable·choices를 검증한다.
생략한 default는 제출값으로 다시 검사하지 않는다.
전체 검증·타입 변환 전에는 어느 setter도 실행하지 않는다. 같은 Body의 배열·typed endpoint/OpenAPI도 연결했다.
기본/custom 생성 소비자의 수동 password 분리를 이 typed 입력으로 교체하고 add+change/CSRF를 본문보다 먼저 확인한다.
새 선언/소비자의 최소 compile과 실제 feature body 실행은 TEST_EVIDENCE에서 구분한다.

공통 serializer의 고정 Unicode 16 정규화와 원문 byte 한도, static choice를 지원하는 Form normalizer를 작성했다.
선택 `Binding.FormDefinition`·`InputSpec`이 login/email 역할의 쓰기 정책을 길이·문법·선택값 검사 전에 적용하며,
원문 한도는 변환 전에 검사한다. 전체 IR source·보호 field·최종 저장 owner와 raw/initial·default·출력의 경계를 유지한다.
관리 Form·최초 계정 command·기본/custom 생성 소비자에 연결하고 OpenAPI에는 변환 전/후 제약을 구분해 기술했다.
실제 generated 기본/custom 생성·최초 계정 module의 최소 compile을 확인했으며 새 본문과 환경별 수용은 원격 확인 대상이다.
선행 CI의 PostgreSQL race·두 macOS 소비자 조합에서 관측한 누적 timeout은 기존 기한/필수 실행을 유지한 분할로 보완했다.
구체적인 원본·소스·한정 검증은 TEST_EVIDENCE에 기록하며, 공개 CLI 조립은 남아 있다.

`identityapi.NewManagement`는 실제 AccountManagement/Catalog의 명시적 노출·전체 권한·수명 owner를 22개 JSON 경로와
OpenAPI에 연결한다. 모델 `fields`와 `credential` 명령을 구분하고 역할 정규화·원문 한도·PUT default/collection 생략,
signed key·If-Revision·현재 domain 이력을 같은 선언으로 표현한다. 읽기는 선택 ModelView만 제공하고 password 사용 가능
여부도 같은 snapshot에서 관측한다. 쓰기는 확정 ID/revision만 반환하여 자기 비활성화·세션 폐기 뒤 재조회로 성공을 잃지 않는다.
실제 모델 프로필이 `password`인 경우도 credential-origin 진단과 구분하며 rollback/cleanup/unknown의 실패 경계를 유지한다.

기본/custom 일곱 생성 변형에 실제 TCP·cookie/CSRF·SQLite 소비자를 작성했다. 생성과 로그인·명시적 조회·권한/CSRF/예약 입력·
revision·PUT 생략/default·no-op·password와 세션 폐기·삭제/audit, native rollback·확정 commit 뒤 unknown·snapshot cleanup 실패·
자기 비활성화 receipt를 확인하도록 필수 실행에 연결했다. 후속 PostgreSQL 실행 연결과 통합 기동은 아래 진행을 따르며,
생성·최소 compile은 새 본문 실행과 다르다. 실제 결과와 소스 경계는 TEST_EVIDENCE에 기록한다.

선택 계정의 상위 `RunFirstAccount`는 실제 명령 runtime의 typed Description을 보관한 ProjectSpec의 전체 source·role과
대조한 뒤 TTY를 읽는다. 생략/default와 빈 입력·static choice·원문 password를 보존하고, 새 operation ID와 한 번의
bounded private pipe 전달·취소/기한·writer join·버퍼 해제·terminal 복구를 연결한다. 같은 operation의 응답이나
canonical private exit로 확정 생성·unknown·정리/출력 실패를 구분하며, project/process 정리 뒤 결과를 한 번 게시한다.

실제 생성 기본/custom 프로젝트에서 선언용 runner와 typed runtime의 두 ABI·전체 generated bundle·TTY·native SQLite를
연결하는 소비자를 작성했다. 다른 source의 입력 전 거부·DB 전 profile validation, 첫 생성·중복 거부·재접속한 계정/감사/
operation receipt, 실제 commit 뒤 unknown·close/응답 실패를 검사하도록 필수 목록에 넣었다. Typed opener 앞뒤의 fault
probe는 실제 네트워크 단절 실험과 구분한다. 작성·최소 compile과 본문/환경 실행의 결과는 TEST_EVIDENCE에 기록한다.

생성 `NewAccountRuntime`으로 같은 source·backend·세션/인증과 계정/catalog owner, login·자기 password 변경·reset을 묶었다.
연쇄 효과의 수정/삭제·추가 grant와 직접 관리 정책의 일치, session manager/storage 한도 일치를 검사한다. Reset은 동일 typed
password 정책을 공유한다. 고정 비밀번호 알고리즘/사전을 공통화하고 `Binding.DefaultPasswordValidator`가 실제 선택 모델의
문자열 속성을 읽도록 작성했다. 기존 고정 Profile/Django 비교는 유지하고 secret 역할·선언 사본·원문·취소/동시성 검사를 추가했다.
기본/custom 일곱 HTTP 생성 변형은 새 runtime을 사용하고 실제 startup source·공유 owner·typed 기본 정책·reset admission과
기존 mutation/rollback/unknown/세션 폐기를 검증하도록 연결했다. 작성/생성·최소 compile과 실제 환경별 결과는 TEST_EVIDENCE를 따른다.

공개 `createsuperuser`·Article·새 scaffold를 선택 계정 runtime에 연결했다. 선언 runner는 Schema IR과 독립 migration만 읽고,
별도 commands runtime이 동일 typed 정책의 첫 계정을 생성한다. 실제 opener의 startup 검사 뒤 획득한 backend는 process owner가
한 번 닫는다. Article/scaffold의 자동 Account/Catalog Admin·관리 JSON·login/password/reset은 같은 credential/session owner를 쓴다.
기존 `godj_system` 이력은 데이터 읽기 전에 명시적 전환 필요로 거부하며 자동 Fresh/adoption을 하지 않는다.

Article 업무 Admin/API도 전체 Principal로 같은 native snapshot/write fence에서 현재 domain·active/권한을 재확인한다.
Admin mutation과 값 없는 domain audit를 함께 저장하고 읽기 정리/commit이 확인된 뒤 결과를 게시한다. 실제 SQLite/PostgreSQL의
권한 회수·이전 domain·read cleanup·audit rollback·확정 commit 뒤 unknown 소비자를 작성했다. 공개 CLI·scaffold·외부 operator·
Article 양 DB migration/restart 소비자를 새 저장소와 operation receipt에 연결했다. 생성·최소 compile과 새 본문/전체 환경 검증은
TEST_EVIDENCE에서 구분한다. 이 구현을 I1 전체 제품 수용으로 계산하지 않는다.

기본/custom·정규화·LastLogin 없는 모델 등 일곱 변형의 독립 생성 계정 HTTP 소비자를 SQLite와 PostgreSQL 선택에 연결했다.
한 번 생성한 같은 module을 각 native backend에서 실행하며 실제 연결 종류·격리 schema와 종료 순서를 확인한다.
일반 관리와 최초 계정의 두 root는 내부 HTTP/CSRF·profile/role/catalog·password·삭제/session/audit의 필수 child를 유지한다.
새 PostgreSQL 본문은 원격 CI 확인 대상이며, 실제 module 생성·최소 compile을 실행 완료로 계산하지 않는다.

선행 CI에서 확인한 Article의 최종 인가 `403` 선언 누락과 DB wrapper의 native relation 기능 누락을 수정했다.
현재 startup 오류·강도 정책·소스 바인딩·독립 domain/session/audit에 맞게 fixture를 고치고 세 프로세스 재시작 검사를 유지했다.
Legacy adoption은 기존 credential/session 보존과 새 domain의 명시적 전환 거부를 검사한다. 한정 실패 수정 재현과 새 source의
전체 양 DB·race/platform 검증은 TEST_EVIDENCE에서 구분한다.

별도 ogen module의 Session/Bearer 관리 소비자와 자기 계정 login/password/reset fixture를 선택 runtime으로 전환했다.
전체 host 그래프·명시적 typed 정책·최초 계정/domain·공유 session owner를 사용하며 새 fields/credential·확정 mutation
receipt와 22개 경로의 실제 호출을 작성했다. 기존 권한 회수·세션 폐기·host 관계·Unicode/email·부정 header/wire를 유지하고
이력 및 receipt 필수 metadata 검사를 추가했다. 원본 문서와 고정 생성물을 함께 갱신했으며 최소 compile과 실제 HTTP/DB·
전체 환경 수용은 TEST_EVIDENCE에서 구분한다.

실제 SDK 실패를 재현하며 선택 password 역할의 감사 필드·borrowed 감사 조회·생성 decoder의 검증 오류 분류를 바로잡았다.
전체 host PROTECT가 정확한 callback 오류로 롤백된 경우에만 입력 거부로 전달하며 cleanup·unknown 실패 경계를 유지한다.
소스가 같은 임시 SDK module의 compile cache 재사용 검사와 현재 원격 실행 한도에 맞춘 필수 root 분할을 연결했다.
한정 재현, 새 검사 및 전체 원격 수용을 TEST_EVIDENCE에서 구분한다.

Helpdesk의 현재 계정 조회를 같은 native snapshot 안에서 실행하기 위해 생성 model에 `UsingReadScope`와
다대다 관계의 `InReadScope`를 추가했다. 쓰기 기능 없이 typed query·select-related·중첩 prefetch와 관계를 읽으며
model·warm/empty cache는 빌린 범위가 끝나면 만료된다. Root/쓰기 scope의 혼용·쓰기 시도·취소·cleanup 실패를
SQLite/PostgreSQL 독립 생성 소비자의 필수 child로 연결했다. 생성물 갱신과 최소 compile을 실제 환경 실행과
구분하며 원격 실행 상세는 TEST_EVIDENCE를 따른다.

Helpdesk에 명시적 계정 역할·typed 정책과 독립 migration/domain/audit/session 조립을 추가했다.
`NewSelected`의 API·Admin·여덟 HTML 화면은 현재 인가를 업무 조회 snapshot/쓰기 fence에서 재검사한다.
기존 View/Change·staff·선택 후보/inline/collection/editor 정책을 유지하며 감사 작업은 같은 domain/transaction을 쓴다.
이력에 표시명·필드 값을 넣지 않고, cleanup 실패·rollback·unknown 결과를 확정 거부/성공과 구분한다.

Browser는 명시적인 18개 권한의 일반 staff, 독립 SDK는 업무 16개/조회 4개 권한의 일반 계정으로 실제 HTTP에
로그인한다. 선택 domain·LastLogin·DB session과 업무 저장값/감사를 부모가 독립 대조한다. 실제 여섯 문서를
export해 Helpdesk만 갱신하고 고정 ogen으로 재생성했다. Native 양 DB의 오래된 admission, 권한/역할/domain 회수,
폼/inline/collection, 범위 소유권, 감사 실패/unknown·최종 fence 재검사와 일관된 동시 읽기를 필수 목록에 추가했다.
새 본문과 실제 browser/SDK의 실행 및 전체 환경 수용은 자동 원격 CI 확인 대상이다.

`ProfileRelation[M]`으로 명시적 FK/OneToOne 정책을 완전한 선택 source에 묶고 Creator/FirstAccount·프로필/통합 편집,
공통 Admin 선택·부분 Form·관리 JSON에 연결했다. 현재 actor·권한·revision과 같은 native 범위에서 선택을 재검사하고,
생략/no-op·NULL clear·unique·한 번의 감사와 기존 rollback/unknown 경계를 유지한다. 첫 계정 명령의 관계 key 파싱은
DB 획득 전이며 최종 선택 허가는 typed owner에 있다. 두 독립 생성 앱의 필수 FK/optional OneToOne과 실제 명령·
Admin/JSON·재조립 소비자, 양 DB native 실패·signed key 검사를 추가했다. 작성/최소 compile과 본문 실행은
TEST_EVIDENCE에서 구분하며 새 source의 원격 수용은 확인 대상이다.

`ProfileFile[M]`과 `UpdateForm`으로 선택 계정 파일 편집을 연결했다. 저장 전 현재 인가/대상/revision을 읽고 scope를 닫은 뒤
명시적 storage로 게시한다. 실제 이름·검증한 이미지 크기·scalar/role/collection은 최종 native owner의 재검사와 함께
저장한다. 부분/불확실 게시나 DB 실패 뒤 파일별 결과를 보존하고 자동 삭제·재시도를 하지 않는다. Admin은 결과 보관 callback을
요구하며 JSON은 파일 참조·크기의 read-only 투영을 유지한다. 기본/custom의 독립 생성 소비자에 실제 multipart·이미지 선택,
세션 폐기·signed key·경합·권한 회수·unique/audit/cleanup/unknown과 DB 재접속 검사를 작성했다.
작성·최소 compile과 새 본문/환경별 실행은 TEST_EVIDENCE에서 구분한다.

파일을 포함한 `Creator.CreateForm`/`CreateUnusableForm`, 명시적 operation ID를 받는 `FirstAccount.CreateForm`과 Admin 생성
보고를 연결했다. 현재 인가/첫 계정 부재를 확인하고 scope를 닫은 뒤 게시하며, 실제 저장 이름으로 전체 정책을 재검사한 뒤
해싱·최종 native transaction을 수행한다. 모든 파일 시도는 뒤 validation/hash/commit/cleanup 실패에도 보존한다.
기본/custom의 별도 생성 소비자는 필수 파일·이미지·choices, 실제 multipart와 요청 수명, 첫 생성 경합·unknown receipt,
소속/감사·권한 회수·실패/동시 생성·DB 재접속을 검사하도록 작성했다. 본문 실행과 최소 compile은 TEST_EVIDENCE에서 구분한다.

공개 초기 계정 CLI에 필수/선택 File/Image와 choices를 연결했다. 명시적 byte 한도와 보고 owner를 선언에 묶고,
TTY에서 지정한 regular file을 한 번 읽어 v3 private pipe로 전송한다. 실제 typed Form의 이미지 검사·원문/default 의미를
유지하며 준비된 upload와 reader는 포기/한 번의 시도 뒤 만료된다. 파일 게시 결과와 DB 결과·보고 실패를 구분하고,
보고 callback의 오류/panic/Goexit나 응답 유실이 확정 commit을 지우지 않게 한다. 실제 두 생성 모델·양 DB command와
TTY/별도 process/재접속 소비자, 입력 한도·부분 게시·rollback·unknown·보고 실패 검사를 필수 목록에 연결했다.
새 본문의 환경별 실행과 최소 compile, 선행 CI에서 확인한 선택 목록 누락의 한정 수정은 TEST_EVIDENCE에서 구분한다.

계정 생성 owner를 같은 SessionStore와 collection/lifecycle 처리에 연결했다. 실제 INSERT의 key를 잠정 값으로 두고
두 소속의 전체 삭제·NULL·삽입과 관련 계정/catalog의 권한·revision·session·audit를 함께 검증한다. 생성된 계정 행 자체가
권한 관계의 through일 때 논리적 이전 소속에서 제외해 최종 영향을 관측하며, INSERT로 얻은 권한을 효과 인가에 쓰지 않는다.
최종 그래프에서 validation·unique와 독립 readback을 확인하며, 해당 endpoint의 일반 scalar 편집은 거부한다.
기본/custom의 생성 소비자와 양 DB native 수명 소비자를 작성했고 첫 operator도 같은 경로를 사용한다.
작성·생성/최소 compile과 새 본문 실행은 TEST_EVIDENCE에서 구분한다.

추가 M2M의 환경별 검증과 남은 고정 Directory/Manager 소비자 교체, 일반 collection의 선택 identity through 자동 생성,
채운 DB의 mapping/retirement/receipt·복구·restart와 새 독립 PostgreSQL HTTP를 포함한 전체 환경 수용은 남아 있다.

### 추가 M2M 프로필

`ProfileCollection[M]`은 선택 계정 source·역할·실제 typed M2M/target capability와 현재 선택 Filter·입력 한도를 묶는다.
Create/FirstAccount의 typed/Form·AccountEditor·ProfileReader·관리 Form/Admin/JSON에 연결했다. 모든 선택 collection을
한 batch에서 준비하고, 원 Form 제출의 반복 수·필수/빈 집합·생략/no-op·최종 집합과 현재 선택을 다시 검사한다.

기존 lifecycle projection을 추가 account M2M으로 확장해 제출하지 않은 관계의 연쇄 삭제, 대칭 관계의 다른 계정과 중복
physical pair의 최종 소속을 반영한다. 관련 revision·현재 권한·session·직접 감사 및 최종 through/target readback을 같은
native scope에 두고, `AccountMemberships` validator에 실제 최종 프로필 집합을 제공한다. 추가 저장 소속의 필드별 전체 한도는
1,024개이며 초과를 숨기지 않는다. 읽기도 명시적 Fields·전체 source·bounded native snapshot에 묶는다.

기본/custom 독립 생성 소비자에 실제 Form/Admin/API·현재 선택·대칭/연쇄 효과·late readback/rollback/unknown·경합·
재접속과 source/구성 거부 검사를 작성했다. 코드 작성·compile과 양 DB/race/HTTP 본문 실행은 TEST_EVIDENCE에서 구분한다.
공개 `createsuperuser`의 collection 입력/전송은 아래 구현을 따른다. Selected identity를 일반 through setter로 생성하는
조립은 아직 명시적으로 거부하며 이번 코드로 I1 전체 수용을 완료 처리하지 않는다.

### 공개 첫 계정의 collection 입력

실제 `FirstAccountDefinition.CollectionLimits()`와 Command Description에 필드별 collection kind·개수/원문 byte 한도를
연결했다. TTY는 canonical signed int64 목록을 받고, private v4가 key 순서·반복·0/음수와 생략/빈 목록을 보존한다.
원 입력 반복 수와 scalar/key 합계 예산, 정확한 선언/source·필드 순서·종류를 DB 획득 전에 확인한다.

같은 ModelForm이 문법상 후보를 준비하고 실제 FirstAccount가 target 존재·현재 선택을 해싱 전과 최종 native scope에서
재검사한다. 입력/준비의 사본·한 번의 시도·취소·resource 종료와 기존 파일 게시 보고·확정/unknown receipt 경계를 유지한다.
기본/custom의 양 DB command 소비자와 실제 TTY·별도 runtime·파일 결합·재접속 검사를 연결했다. 새 본문 작성·최소 compile과
실제 환경별 실행은 TEST_EVIDENCE에서 구분하며 전체 원격 수용은 확인 대상이다.


### 준비된 선택 모델의 전환

서로 다른 typed 계정 모델의 전체 행을 명시적 1:1 mapping으로 대조하는 `Transition[S,T]`를 작성했다.
같은 backend·전체 host source·catalog를 요구하고 현재 전환 인가·scalar/추가 collection·principal/hash/role/권한과
기존 domain 이력을 준비/최종 fence에서 검사한다. Source unusable/role 퇴역·revision·기존 domain session 전체 폐기·감사와
새 generation/receipt를 한 transaction에 묶고 최종 행/관계를 다시 읽는다. 보관 manifest와 실제 predecessor를 사용하는
restart/unknown reconciliation도 작성했다. 작성·외부 생성/compile과 새 양 DB/race 본문 실행은 TEST_EVIDENCE에서 구분한다.

전환 대응표를 새 migration과 생성 model로 DB에 저장하도록 연결했다. Receipt의 count/digest와 canonical payload를
같은 native transaction에 기록하고 저장 직후·재시작·전체 이력 검사에서 다시 대조한다. Signed key와 예전 mapping-free
receipt를 보존하며 손상·누락·중복·실패/rollback을 거부한다. Key page와 개별 payload의 별도 한도를 적용한다.
채운 기존 이력의 migration·양 DB 손상/rollback·페이지 경계와 재접속 검사를 작성했다. 최소 compile과 실제 본문·환경별
수용은 TEST_EVIDENCE에서 구분한다.

위 경로는 대상 행과 프로필을 명시적으로 미리 준비한다. 빈 대상 모델에 행을 생성하는 경로는 다음과 같다.

### 전환 대상 계정의 생성

`TransitionTarget[T]`와 생성 팩토리·`PrepareCreation`을 작성했다. 현재 source 전체의 key/revision과 typed 로그인·추가
프로필/FK/M2M·기존 파일 참조를 명시적으로 받아, source의 principal·password encoding·boolean role·catalog 소속을
보존한 target 후보를 준비한다. 저장 key는 예상하지 않으며 실제 INSERT 뒤 한 collection batch와 전체 readback을 확인한다.
대상 생성·source 퇴역·session 폐기·양쪽 감사·새 receipt와 실제 대응표는 같은 native transaction에 속한다.

Manifest v3는 사전 준비 mapping 또는 생성 source 목록을 보관하고, restart/unknown 대조는 실제 저장 key까지 확인한다.
빈 대상 table의 실제 column과 incoming reference, 현재 선택 정책·전체 source/target 상태·unique·예산을 준비/최종 fence에서
검사한다. 대상 계정 자체가 다른 M2M의 through인 조립은 관련 owner의 revision/인가/감사 처리가 아직 없어 명시적으로 거부한다.
기본→custom과 custom→생성된 기본 팩토리 소비자에 양 DB의 실패/rollback·unknown·경합·재접속 검사를 연결했다.
작성·생성/최소 compile과 새 본문 실행은 TEST_EVIDENCE에서 구분한다.

### 일반 host 관계와 schema 단계의 전환

`TransitionReference[S,T]`는 같은 전체 source에서 typed host의 기존/대상 FK·OneToOne 쌍을 명시한다.
전체 행의 bounded 관측·현재 계정 대응표와 새 실제 key를 사용하고, 기존 참조를 보존하면서 target 참조를 채운다.
모델별 필수 권한과 optional revision, 사본을 받는 최종 정책·원문 없는 감사를 계정 생성/퇴역·session 폐기·receipt와
같은 native transaction에 묶었다. 행 추가/삭제·revision 없는 값 변경과 late write를 전체 readback으로 대조한다.
일반 업무 M2M through는 지원하며 계정/catalog 소속의 endpoint 변경은 해당 수명 owner 없이는 거부한다.
Manifest v4는 원문 없는 host inventory digest와 선언을 보관하고 unknown/restart에서 원래 source의 receipt와 대조한다.

독립 생성 소비자에 기존/확장/정리된 세 프로젝트를 함께 생성했다. 채운 legacy DB에 실제 확장 migration을 적용하고,
staged/created 계정 전환 뒤 기존 FK·M2M을 제거한 다음 contracted model로 재조립하여 계정·관계·이력·재시작을 확인하도록
작성했다. 이 소비자가 발견한 forward field removal 미지원은 I1의 실제 선행 의존성이므로 non-PK `RemoveField`를
definition·historical state·autodetector·SQL projection·native lifecycle에 연결했다. Constraint/M2M/image 의존 순서와
전체 field preimage/anchor를 보존하며, 역방향의 데이터 소실·상수 scalar default 재적용과 default 없는 required 복원 거부를 명시한다.
이는 M1 전체 완료가 아니다. 양 DB의 실제 column/값·FK/OneToOne/unique·sequence·이력, 실패/unknown·재접속 검사를
필수 목록에 연결했으며 작성·생성/최소 compile과 새 본문 실행은 TEST_EVIDENCE에서 구분한다.

### 준비된 권한 catalog의 전환

서로 다른 Group/Permission의 전체 key·revision 대응표를 `Prepare`와 `PrepareCreation`에 연결했다. 미사용 행을 포함한
전체 typed scalar·canonical 권한 코드·group 권한과 계정의 직접/group/유효 권한을 대조한다. 부분 공유 모델은 실제 같은
row ID를 보존하며 새 계정 생성은 재매핑한 catalog key로 소속을 저장한다. 전체 관측과 raw through endpoint를 같은 예산에
포함하고, source 퇴역·host 변경·감사·receipt 전후 readback이 달라지면 전체 전환을 rollback한다.

Manifest v5·payload v2에 계정과 catalog 대응표를 함께 기록했다. 기존 이력 형식을 읽으며 배열/byte 한도·canonical 형식·
전체 checksum·저장 직후 비교와 restart/unknown 대조를 유지한다. 기본→별도 Team/Grant의 새 생성 소비자에 staged/created,
부분 공유·signed key·페이지 경계·미사용 행/관계 변경·현재 인가·실제 쓰기 뒤 실패·원자성·경합·재접속 검사를 작성했다.
코드 작성·생성/최소 compile과 새 본문·환경별 실행은 TEST_EVIDENCE에서 구분한다.

### 일반 host의 catalog 참조

`TransitionReferenceFields.Kind`로 계정·그룹·권한의 대응표를 구분했다. 같은 숫자 key를 가진 서로 다른 모델과
전체/부분 공유 catalog를 실제 role·전체 inventory에 묶고, 새 계정의 할당 key가 확정되기 전에는 추측하지 않는다.
혼합 FK·OneToOne·업무 through 행도 revision과 감사는 한 번씩 적용하며 준비 이후 값/행/권한 변경과 최종 readback,
rollback·unknown·receipt 경계를 유지한다. Manifest v6에 종류를 기록하고 이전 account-only v4/v5 evidence를 보존한다.

기존 catalog 소비자와 같은 외부 생성 앱에 staged/created 각각 37개 child 및 root/strategy의 77개 필수 이벤트를 추가했다.
서로 겹치는 source key와 다른 catalog target key, 공유 catalog, nullable/no-op·signed key·한도·현재 권한·사본·여러 행 쓰기 뒤
실패·경합·재접속과 실제 host schema 확장/전환/정리 후 생성 runtime을 검사하도록 작성했다.
코드 작성·생성/최소 compile과 양 DB·race 등의 새 본문 실행은 TEST_EVIDENCE에서 구분한다.

### 전환 대상 catalog 생성

`TransitionCatalogTarget[G,P]`와 생성 팩토리, source key/revision별 typed 입력을 기존 staged/created 계정 전환에 연결했다.
변경되는 catalog 모델의 부재와 전체 source를 검토하고 실제 native INSERT key로 그룹 권한·계정 소속·host FK/OneToOne과
업무 through를 채운다. 준비 중에는 pending namespace를 정책에 명시하며 최종 정책에는 실제 key만 전달한다.
부분 공유 Group은 새 권한 관계가 바뀐 행의 revision·감사만 추가하고 Permission identity는 그대로 둔다.
Catalog 입력 권한·typed 정책·계정의 현재 인가·source 퇴역·session 폐기·domain/대응표·감사는 같은 native transaction이다.

Manifest v7의 전체 source/최종 revision과 payload v2의 실제 대응표를 연결했다. 마지막 정책 이후의 계정/catalog/host/receipt
readback과 최종 합계 예산을 유지한다. 별도 생성 소비자는 양 방향·부분 공유·signed key·페이지 경계·실제 쓰기 뒤 실패,
unknown/경합·같은 DB 재접속과 schema 정리를 검사하도록 작성했다. 새 본문과 환경별 실행은 TEST_EVIDENCE에서 구분한다.

공통 collection은 실제 native INSERT key와 full typed row readback을 소유하는 `AppliedCollectionChanges`를 반환한다.
UNIQUE 충돌/no-op과 key 0을 구분하고 NULL·중복 key·scan/close·뒤 행 변경·scope 실패에는 부분 결과를 게시하지 않는다.
일반/UNIQUE through의 실제 signed key·전체 행·rollback·읽기 오류 검사를 양 DB 필수 실행에 연결했다.
이 결과를 공유 물리 through의 이전/새 catalog·계정 소속·프로필 projection에 연결했다. 실제 key와 nullable endpoint만
예상 inventory에 추가하며 전체 grant/행 대조 뒤 catalog digest를 고정한다. 원래 scope에 묶인 `Verify`로 마지막 정책·
retirement·receipt 이후 전체 삽입 payload를 재검사하고 그 값과 역할별 행도 최종 합계 예산에 포함한다.
일반/복합 UNIQUE storage, staged/created 계정, 부분 공유 catalog와 account-only 생성, 실제 signed key·정확한 native
대응표/소속/감사/세션·rollback/unknown·재접속 및 마지막 정책의 payload/endpoint/nullable 행 변조 소비자를 추가했다.
일반 계정·catalog 생성/편집도 최종 scalar/unique/선택 정책 이후 삽입된 전체 행을 재검사하며, 기존 대상 계정의 mapping
전환도 물리 소속·프로필 identity를 고정한다. 양 DB의 늦은 PK/payload 변경과 receipt 뒤 기존 링크 key 교체 거부를
필수 소비자에 연결했다. PostgreSQL signed key fixture는 격리된 실제 identity sequence로 값을 생성하도록 수정했다.
로컬 자원 과사용으로 중단된 compile은 완료로 계산하지 않으며 새 본문·환경별 검증은 TEST_EVIDENCE에 구분한다.

선택 identity의 해싱을 쓰기 transaction 밖에 둘 수 있도록 공통 collection의 읽기 관측을 추가했다.
`ObserveCollectionChangesInReadScope`는 기존 수집·typed 입력과 같은 엔진으로 전체 삭제/NULL·누락 pair를 준비하고,
reader/입력 callback을 보관하지 않는 관측을 반환한다. 최종 `PrepareInSession`은 물리 링크 key와 모든 incoming FK
경로까지 다시 대조한다. 삭제 합계가 같은 하위 행의 재지정과 nullable 추가 endpoint 변경도 쓰기 전에 거부한다.
비관계 scalar를 snapshot 종료 후 준비할 수 있지만 source/선택/관계 topology는 고정하며 기존 단일 실행·전체 삽입 행
검사를 유지한다. 양 DB의 독립 connection 변경·cleanup/뒤 쓰기 rollback과 외부 생성 소비자의 ordinary/UNIQUE scalar
준비·retained pair·unique 거부 검사를 작성했다. 로컬 compile은 재시작하지 않았으며 새 본문은 원격 검증 대상이다.
선택 계정이 추가 프로필 collection의 through인 편집을 이 기반에 연결했다. `NewAccountThroughCreation`은 generated
binding/collection과 scalar 생성 정책만 가진 선언이며, 편집기 구성 때 실제 SessionStore에 결합한다. 읽기 snapshot에서
현재 actor·revision·전체 삭제/삽입과 생성/편집 각각의 관련 권한을 확인하고, 종료 뒤 누락 pair마다 해싱한다. 최종 fence는
같은 topology와 현재 권한·정책을 다시 검사한다. 새 계정의 revision 1·빈 자체 소속·생성 감사, 공유 owner의 단일 revision·
직접 감사와 삭제 identity의 session 폐기, 최종 실제 key·전체 payload 대조를 한 transaction에서 처리한다.
기존 scalar 생성과 해싱을 재사용하며 nested collection·파일·서버 endpoint 덮어쓰기와 암묵적 unusable은 거부한다.
삭제 예정 행을 제외하는 advisory uniqueness와 native 제약을 함께 유지한다. 기본/custom 생성 소비자에 각각 25개 시나리오와
두 ORM 단위 root를 추가했으며 실제 생성·양 DB·platform은 원격 검증 대상이다.

같은 typed 생성 경로를 계정의 그룹·직접 권한 collection에도 연결했다. 생성 선언의 저장 검사를 실제 선택한 필드에
적용해 생략한 역할을 빈 집합으로 갖는 scalar 계정/첫 계정 생성을 허용한다. 생성 Fields에 역할 collection을 명시하면서
nested identity owner를 제공하지 않는 선언은 계속 거부한다. 기본/custom × 그룹/직접 권한 네 생성 변형을 추가했고,
공통 38개 내부 필수 실행을 각각 양 DB에서 요구한다. 한 행이 그룹 소속과 그룹 권한을 동시에 만드는 조합의 여러 owner
revision·직접/간접 감사, 전체 행 rollback과 endpoint 정책 구성 거부를 함께 검사한다. 원격 본문·타입 검사는 미완료다.

공유 물리 through 전환과 새 계정 through 편집의 실제 검증, 선택 catalog·나머지 through/전환 수명과 역사 `systemstate` 가져오기,
남은 고정 소비자 교체 및 같은 source의 전체 환경 수용은 미완료이며 I1-07/08을 수용 완료로 처리하지 않는다.

1. 기본 계정과 renamed field·추가 프로필을 가진 소비자를 기준으로 정규화·생성 binding을 구현한다.
2. 공통 credential/관리 runtime과 typed 프로필, catalog 소유권, durable domain/전환을 함께 연결한다.
3. 공개 명령·Form/Admin/API·password/reset·host 삭제·restart 소비자와 실패 검사를 완성한다.
4. 포맷·필요한 최소 compile 뒤 변경 묶음을 push하고 영향 테스트·generated drift·양 DB·race/process·
   독립 기준·브라우저 및 전체 원격 CI를 실행한다. 결과 상세는 TEST_EVIDENCE에 기록한다.

- [ ] I1-01~08 제품 코드·생성기·소비자를 완성한다.
- [ ] 같은 source의 필수 원격 실행과 원본·독립 기준을 대조한다.
- [ ] 장기 계약·Matrix·Evidence·CURRENT에 구현과 환경별 확인 및 남은 범위를 반영한다.

C1의 수용 완료와 I1의 새 source 검증을 구분한다. I1은 위 요구 전체의 실제 소비자·실패 경로와 환경별 근거를
확인한 뒤 완료를 판단한다.
