# GDJ-0138: 사용자 정의 계정과 계정 수명

- 상태: I1 구현 중 — C1 수용 확인 후 주 작업 전환
- 구현: [PR #8](https://github.com/progresshans/godj/pull/8)의 `codex/custom-account-lifecycle` 작업트리
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

공통 `ProfileEditor[M]`와 생성 팩토리는 명시한 scalar profile 필드의 Form/JSON 입력을 받아 현재 권한·target revision·
typed unique 검증·강제 update·audit를 같은 native coordinated transaction에서 처리한다. 실제 변경만 revision과
감사를 추가하고 no-op도 현재 권한·대상 무결성을 확인한다. Credential·서버 시각·role·관계·파일 입력은 거부한다.
기본/사용자 정의 외부 소비자는 ModelForm과 API body binding으로 실제 typed 입력을 준비한다.

독립 `godj_account_audit` 앱과 초기 migration은 기본 User 없이 domain별 감사 기록을 저장한다. 현재 actor를 사용한
append와 정확한 domain별 bounded history, 수정/audit 실패의 원자 rollback, stale/회수/domain 변경·대상 손상·
validation·unknown 결과·revision 경합 검사를 양 DB 필수 목록에 연결했다. 명시적으로 존재하는 0/음수 계정 key도 유지한다.

최종 source의 최소 compile과 생성 manifest를 확인했고, 공개 생성에서 발견한 protocol 상한 실패를 한정 재현·수정했다.
새 profile/audit·단위·DB·외부 소비자 본문과 race/전체 platform 실행은 원격 확인 대상이다. 상세와 이전 source의
실패/수정 결과는 TEST_EVIDENCE에만 둔다. 작성과 compile을 I1-01~08의 제품 수용으로 계산하지 않는다.

생성 `ProfileReader[M]`는 현재 인가·domain·선택 프로필 조회/목록/이력을 같은 native snapshot에서 처리한다.
`ModelView[M]`는 전체 모델 행 없이 선택한 값과 모델 source를 보관하며 공통 Form/API 출력이 같은 source와
로드 여부를 검사한다. 종료 실패·권한 회수·domain 변경·대상 손상·일관된 count/page·0/음수 key를 양 DB 필수
검사에 연결했다. 기본/사용자 정의·정규화 변형의 외부 소비자는 실제 HTTP 로그인·CSRF·Form/JSON 수정·조회와
stale/예약 필드 거부·기존 세션의 권한 회수/복구를 실행하도록 작성했다. 이 소비자의 DB는 SQLite이며 PostgreSQL의 독립 HTTP 소비자는 남아 있다.

관계/파일·role/catalog 관리와 기존 Directory/Manager 교체, 공통 HTTP/Admin 제품 조립,
첫 계정 명령·password/reset/session·host 삭제·audit 설치/startup·채운 DB mapping/retirement/receipt·restart는 남아 있다.

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
