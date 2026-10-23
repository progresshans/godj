# GDJ-0138: 사용자 정의 계정과 계정 수명

- 상태: I1 구현 시작 — C1 원격 검증과 분리한 작업트리에서 준비
- 시작: 2026-10-24
- 출발 source: `b19aeeddcfdf6ac2aef5bbf0102d5f13c8a0e1e0`
- 선행: A1·F1의 완료 근거는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)를 따른다.
  C1의 같은 source 전체 원격 검증은 아직 미완료이며 이 문서로 완료 처리하지 않는다.
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

계정 역할 IR과 기본 선언, 프로젝트 wire·사본·digest·생성 snapshot, 전체 host 그래프에 결합한 typed `BindAccount`를
작성했다. 기본 User와 별도 Member·catalog·host FK 소비자의 실제 SQLite 읽기/쓰기 및 다른 정책의 생성 파일 혼합 거부
검사를 원격 실행 대상으로 추가했다. 기존 identity 인증/관리 runtime의 custom model 연결과 durable domain/전환은
아직 남아 있다. 이 생성 기반을 I1-01~08의 완료로 계산하지 않는다. 실행 상세는 TEST_EVIDENCE를 따른다.

계정 역할 fingerprint·소유권 세대에 묶인 credential 준비와 Principal의 도메인 보존을 추가했다.
기본/별도 모델의 SQLite 행에서 공통 StoredAuthenticator를 사용하는 소비자와, 비밀번호 검증 중 도메인 변경·
기존 HTTP 세션 거부 검사를 작성했다. 독립 `godj_account_domain` 앱의 generated model·migration과 추가 전용 이력,
현재 역할/이전 receipt 비교, 새로운 세대·operation ID, 작업 ID 재조회도 작성했다. 이 앱은 기본 User를 설치하지 않는다.
Binding은 같은 DB scope에서 저장 도메인과 actor 도메인을 검사한다. 기본/별도 모델 소비자는 typed 계정 생성과
도메인 초기화의 원자 rollback, 실제 저장 세대 변경 뒤 credential/actor 거부를 검사하도록 연결했다.
양 DB의 경합·이력·snapshot·재접속·unknown 결과 조회 검사를 원격 필수 목록에 추가했다. 작성과 최소 compile은 실제 실행 PASS가 아니다.

이력 API는 서버 내부 transaction 구성 경계다. 채운 계정의 mapping 실행·인가·session/reset 폐기·audit를 대신하지 않으며,
빌린 write scope에서 받은 receipt는 commit 확인 전까지 잠정 값이다. 기존 Directory/Manager에 도메인 검증을 연결하고
catalog·typed profile·첫 계정 명령·명시적 전환을 함께 완성하는 작업은 계속 남아 있다.

생성 팩토리가 선택한 계정·Group·Permission descriptor를 공통 저장소에 묶도록 작성했다. 계정·domain·직접/그룹
권한을 한 snapshot에서 읽고 permission 한도·revision·종료 실패를 확인한다. 역방향 accessor를 숨긴 custom catalog도
선언된 forward M2M을 기준으로 같은 ORM AST를 사용한다. 소비자의 임시 credential adapter는 제거했으며 실제 생성
저장소와 선택된 Exact/NFKC 인증 정책을 사용한다. 독립 domain 생성 root는 기본 계정 root와 겹치지 않는
`account/domain`으로 옮겼고 migration의 DB 의미는 유지한다. 작성한 양 DB·외부 소비자 검사의 원격 실행은 별도 확인 대상이다.

공통 저장소의 `AuthorizeIn`도 작성했다. 기존 인증 claim의 domain을 현재 저장 값과 먼저 대조하고, 같은 borrowed scope의
현재 active/role/grant와 deny overlay를 확인한다. 실제 기본·사용자 정의 모델 소비자에 허용된 typed profile 변경을 연결하고,
양 DB에는 회수된 grant·이전 domain·inactive actor 거부, 허용 후 rollback, 만료 scope와 overlay 오류 검사를 추가했다.
이 인가 경계를 사용하는 typed profile owner를 아래와 같이 연결했으며 기존 Manager 전체의 교체는 남아 있다.

공통 `ProfileEditor[M]`와 생성 팩토리가 명시한 scalar profile 필드의 Form/JSON 입력을 받아 현재 권한·대상 revision·
typed unique 검증·수정·audit를 같은 native coordinated transaction에서 처리하도록 작성했다. 실제 변경만 revision과
audit를 추가하고 no-op도 현재 domain·권한·대상 무결성을 확인한다. 서버 소유 필드·권한·관계·파일은 이 입력에서
거부한다. 기본/사용자 정의 외부 소비자는 ModelForm과 API body binding으로 실제 typed 입력을 준비한다.

서버 생성 시각의 선택 역할과 독립적인 email 저장 정규화 정책을 IR·wire·fingerprint·생성기에 연결했다. 기본 계정은
Unicode 16 email domain 정규화를 사용하고 custom 모델은 Exact를 선택할 수 있다. Login과 email이 같은 필드는
두 명시 정책을 순서대로 적용한다. 정책을 바꾸면서 이전 domain을 그대로 사용하는 것은 거부한다.

독립 `godj_account_audit` 앱과 초기 migration은 기본 User 없이 domain별 감사 기록을 저장한다. 현재 actor를 사용한
append와 정확한 domain별 bounded history, 수정/audit 실패의 원자 rollback, stale/회수/도메인 전환·대상 손상·
validation·unknown 결과·동시 revision 검사를 양 DB 필수 목록에 추가했다. 0/음수 계정 key의 event/history도 검사한다.
최종 최소 compile과 생성 manifest 확인은 마쳤으며 새 profile/audit 테스트 본문·DB/race의 실행은 원격 확인 대상이다.

Scalar profile 조회 서비스, 관계·파일 편집, role/catalog 관리와 기존 Directory/Manager 교체, 공통 HTTP/Admin 경로,
첫 계정 명령·password/reset/session·host 삭제·audit 설치/startup·채운 DB mapping/retirement/receipt·restart는 남아 있다.
Form/API 입력 소비자의 작성이나 이 scalar 변경을 I1 전체 수용 완료로 계산하지 않는다.

1. 기본 계정과 renamed field·추가 프로필을 가진 소비자를 기준으로 정규화·생성 binding을 구현한다.
2. 공통 credential/관리 runtime과 typed 프로필, catalog 소유권, durable domain/전환을 함께 연결한다.
3. 공개 명령·Form/Admin/API·password/reset·host 삭제·restart 소비자와 실패 검사를 완성한다.
4. 포맷·필요한 최소 compile 뒤 변경 묶음을 push하고 영향 테스트·generated drift·양 DB·race/process·
   독립 기준·브라우저 및 전체 원격 CI를 실행한다. 결과 상세는 TEST_EVIDENCE에 기록한다.

- [ ] I1-01~08 제품 코드·생성기·소비자를 완성한다.
- [ ] 같은 source의 필수 원격 실행과 원본·독립 기준을 대조한다.
- [ ] 장기 계약·Matrix·Evidence·CURRENT에 구현과 환경별 확인 및 남은 범위를 반영한다.

이 작업트리의 시작·설계·compile은 custom account의 실제 동작 증거가 아니다. C1의 주 작업 완료와 전환은
C1의 같은 source 전체 결과를 통합할 때 판단한다.
