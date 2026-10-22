# GDJ-0135: 모델 중심 Form/Admin/API 조립

- 상태: A1 구현·검증 중; 공통 입력·CRUD, Article·Helpdesk와 외부 생성 소비자 연결, 실행 검증 미완료
- 시작: 2026-10-22
- 기준: `82142bbf407798dd5649d433288e6491be3eb277` — Q1의 전체 통합 검증과 G-ORM H1~H6 충족
- 근거: [전체 실행 계획의 A1](../docs/EXECUTION_PLAN.md), [개발 기준](../docs/DEVELOPMENT_CRITERIA.md),
  [모델 JSON 입력](../docs/adr/0046-json-serializer-and-session-authenticated-article-api.md),
  [모델 OpenAPI](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md),
  [모델 입력과 post-clean](../docs/adr/0080-model-blank-policy-and-form-post-clean.md),
  [Admin 권한과 감사](../docs/adr/0044-session-auth-csrf-and-bounded-article-admin.md)

Schema IR과 명시적 표현·업무 정책으로 일반 CRUD와 업무 명령을 조립한다. 현재 모델의 타입·NULL·default와
노출·권한을 다시 선언하거나 반복 DTO·setter·Form 값 변환으로 연결하는 부분을 줄인다.
같은 의미는 재사용하되 HTML과 JSON, 생성과 수정, 요청과 응답의 실제 정책 차이는 명시한다.
DTO helper 하나의 완성을 A1 전체 완료로 계산하지 않는다.

## 현재 출발점

- Article는 모델 외에 애플리케이션 snapshot·Input·Patch, API presence DTO·setter·full/patch 변환,
  Admin의 Form 값 변환과 초기값·출력 연결을 별도로 유지한다.
- Helpdesk는 typed Form 준비와 명시적 collection 저장을 사용하지만 current row·선택 열·변경 목록·기본 CRUD 연결을
  각 경로에서 조립한다. Category와 객체/관계 선택의 현재 권한, revision·unique·audit는 실제 업무 정책이다.
- 공통 `Manager.Metadata/ModelValues/ApplyValues`, 선택 열 Save, Form의 `PrepareInstance/SaveCollections`,
  model serializer·typed input/output/endpoint, 명명 query와 DB Page가 이미 있다.
  이 작업을 위해 function/subquery/window 전체를 먼저 구현하지 않는다.

변경 전 source 경계는 `a1-assembly-baseline-82142bbf.json`의 23개 파일·역할·hash에 보존했다.
파일·줄 수는 기준 위치를 찾는 자료이며 사용성이나 완료 수치가 아니다. 실제 비용 비교 방법은 아래에 둔다.

## 하위 요구와 수용 시나리오

| ID | 구현할 결과 | 수용 시나리오 |
|---|---|---|
| A1-01 | IR과 명시적 allowlist에서 typed 모델 입력·현재 값 준비를 구성한다. 임의 DTO/명령 입력도 계속 사용할 수 있다. | 지원 scalar의 타입·NULL·default·choices·길이, full/partial 생략·null·zero, 입력 실패 후 부분 값 비게시를 확인한다. 새 필드를 추가할 때 같은 scalar 의미를 DTO setter와 Form 변환에 다시 적지 않는다. |
| A1-02 | 같은 모델 준비와 기본 저장을 Form/Admin/API가 사용한다. 선택한 collection과 명령 입력은 scalar와 분리한다. | 생성은 모델 기본값과 서버 값을 적용하고, 수정은 fresh current row의 제외 열을 보존한다. 선택 열 update-only·no-op·현재 uniqueness를 유지한다. 관계 저장과 실패는 동일 transaction에 포함되며 파일의 별도 publication outcome도 보존한다. |
| A1-03 | 일반 생성·목록·단건·수정·삭제의 공통 조립 경로를 제공한다. | Article 단일 모델과 Helpdesk Category–Ticket–Label의 관계 모델이 같은 공개 경계를 사용한다. 기본 CRUD를 쓰기 위해 예제의 private SQL·descriptor 구현을 복제하지 않는다. 목록 scope·검색/정렬/페이지·객체 및 삭제 권한을 실제 HTTP와 Admin에서 확인한다. |
| A1-04 | 권한과 read-only·응답 정책을 재사용하면서 current object/choice 검사를 유지한다. | 필드·권한·응답 변경이 선택한 HTML/API·OpenAPI/client에 함께 반영된다. 새 저장 필드는 자동 노출하지 않으며 named/computed 결과는 원 source의 read-only 권한을 유지한다. API·업로드의 선행 거부에서는 body·객체·관계 I/O를 하지 않는다. HTML 본문의 CSRF 토큰은 기존처럼 파싱한 뒤 검증하며, 거부 시 객체/관계 I/O·mutation은 0이다. 객체 정책은 필요한 현재 조회 뒤 적용한다. |
| A1-05 | 업무 명령은 명시적 입력·현재 인가·transaction·결과 준비를 공통 조립에 연결한다. | Article publish와 Helpdesk label ensure를 일반 CRUD와 함께 사용한다. 실제 명령의 권한·현재 행·중복/unique·audit와 확정된 commit 결과를 유지한다. 결과 준비 실패·취소·rollback·불확실한 commit을 정상 성공이나 자동 재시도로 바꾸지 않는다. |
| A1-06 | 독립 외부 소비자와 실제 변경 실험으로 범용성과 조립 비용을 확인한다. | 구조가 다른 생성 앱과 필요한 cross-app 관계에서 같은 공개 API를 사용한다. 모델 필드 추가, 권한 변경, 응답 변경을 같은 조건으로 전후 대조하고 수동 선언·변환·검증 책임을 기록한다. 정상 compile/runtime와 잘못된 모델/source·read-only 사용의 부정 대조를 포함한다. |

## 유지할 의미와 소유권

- Schema IR이 타입·nullable·default·choices·관계의 원본이다. 노출·입출력 차이·업무 권한·HTTP 상태는
  애플리케이션의 명시적 정책이다. 새 모델 필드를 자동으로 모두 공개하지 않는다.
- 입력 준비는 각 요청이 소유한다. 공유 설정·metadata·nullable pointee·JSON/Binary·collection을 caller가 변경해도
  다른 요청이나 query에 영향을 주지 않는다. 잘못된 설정은 시작 시, 잘못된 입력은 I/O 전 가능한 경계에서 진단한다.
- 새 모델의 기본값, 서버가 정하는 값, 기존 모델 값과 입력 생략은 서로 다르다. Go zero 값만으로 입력 의도를 추측하지 않는다.
  `ApplyValues`의 순수 할당을 모델 검증·권한이나 commit 증거로 취급하지 않는다.
- Article 수정은 기존처럼 대상 조회 후 body를 읽는다. Helpdesk Label 수정은 body 검증 뒤 write scope에서 현재 대상을
  확인한다. 공통화 때문에 이 순서와 기존 client 오류 계약을 바꾸지 않는다.
- 현재 principal·Category·객체·관계 선택·revision과 DB uniqueness를 실제 write scope에서 다시 확인한다.
  읽기 단계 검증이나 이미 만든 Form candidate가 이후 쓰기 권한을 대신하지 않는다.
- 수정은 지정한 열만 쓰고 사라진 행을 INSERT로 되살리지 않는다. Scalar·collection·inline·audit의 기존 원자성과
  파일 게시의 구분을 유지한다. 결과/응답 준비 실패는 commit 전에 처리하고 확정 commit 뒤의 취소를 재시도 신호로 바꾸지 않는다.
- Borrowed context/session은 callback 안에서만 사용하고 작업을 합류시킨다. Callback 생략·중복·오류 삼킴·늦은 실행,
  commit/rollback의 unknown outcome에서는 결과를 정상적으로 게시하지 않는다.
- Named/computed 셀은 원 모델 값과 별도 source 권한을 가진다. 공통 입력이나 저장 API를 통해 writable 열로 승격하지 않는다.

## 구현 순서와 비용 비교

1. Article의 반복 입력 변환을 첫 prototype으로 사용해 기존 typed Body·OpenAPI 선언과 공통 모델 준비를 연결한다.
   구조가 다른 Helpdesk 입력에도 적용한 뒤 공개 API를 확정한다.
2. 현재 Form 준비·기본 저장과 Admin 초기값/출력, 공통 CRUD를 함께 연결한다. 앱 고유 policy와 명령은 명시적으로 남긴다.
3. 외부 소비자에서 같은 모델 필드·권한·응답 변경을 수행한다. 전후 수동 변경 위치와 선언의 의미를 비교하며,
   생성물은 수동 작업과 구분하고 실행 시간·개발 시간을 실제 측정하지 않았으면 수치로 주장하지 않는다.
4. 제품·생성기·실제 소비자·실패 경로를 한 변경 묶음으로 정리해 push한다. 영향·DB·race·process·browser·전체 platform은
   원격 CI에서 실행하고 필수 실행 목록과 원본을 대조한다. 로컬은 포맷·최소 compile·확인된 실패 재현에 사용한다.

F1은 Form/Admin의 더 넓은 widget·관계 UI·formset/inline 확장, A2는 일반 viewset/router·문서 UI·client 도구 확대를
계속 소유한다. 이 구분은 현재 지원된 권한·관계·파일·inline 동작을 A1에서 제거하거나 검증에서 빼는 근거가 아니다.

## 같은 변경의 비교 범위

[외부 생성 소비자](../codegen/consumertest/model_resource_test.go)는 Directory.Workspace와 Inventory.Asset를
독립 모듈로 생성한다. 기본 버전과 변경 버전에 같은 native DB·Form·실제 HTTP/Admin 검증을 적용한다.
각 실제 문서에서 고정 `ogen`으로 독립 SDK도 생성한다. SDK의 typed 입력·출력·권한 거부·기본값·취소를 HTTP로
실행하고, 생산자 쪽에서 저장 값과 기존 행 보존을 별도로 대조한다. 문서·생성 source·잠금·완료 receipt를 함께 남긴다.
변경은 `priority` 정수(default 5, choices 1/5/9) 추가, 쓰기 권한 교체, 응답에서 `note`를 제외하고 `priority`를
포함하는 것으로 고정한다. 기존 선택 목록을 유지한 대조에서는 새 필드가 입력에 자동 노출되지 않아야 한다.

| 변경 책임 | 기존 수동 조립 패턴 | 공통 모델 조립 |
|---|---|---|
| 저장 필드 | Schema 선언과 생성물 갱신 | 동일. 타입·default·choices는 IR이 소유 |
| 입력 노출 | Model Spec/Form 선택과 별도의 입력 DTO | 명시적 Resource 목록에서 Form·JSON을 구성; JSON별 required/empty는 별도 정책 |
| 새 필드 전달 | DTO 필드·codec/setter·create builder·partial patch·Form typed 값 변환을 함께 변경 | `NewResource`·`PrepareInput`·공통 CRUD 구현을 변경하지 않음 |
| 권한 교체 | 명시적 권한을 admission/Admin에 연결 | 동일한 책임을 유지; 권한 선언 자체의 비용 감소는 주장하지 않음 |
| 응답 교체 | ModelEncoder의 명시적 투영 | 같은 투영을 사용; 실제 JSON·OpenAPI·독립 SDK·Admin 목록이 함께 변경되는지 확인 |
| 현재 객체와 결과 | scope·선택·unique·transaction·출력/감사 소유 | 공통 조회·준비·선택 열 저장 위에 앱 정책과 확정 결과 경계를 연결 |

비교용 [기본 수동 연결](../codegen/consumertest/testdata/modelresource/manual_baseline.go.txt)과
[변경 후 수동 연결](../codegen/consumertest/testdata/modelresource/manual_changed.go.txt)은 `82142bbf` Article의
DTO/setter·Form-to-builder 패턴을 현재 공개 API로 재현한 테스트 입력이다. 과거 binary를 실행한 근거가 아니다.
[대조 본문](../codegen/consumertest/testdata/modelresource/comparison_test.go)은 같은 JSON/Form·PATCH의 저장 값,
응답과 schema·권한을 비교하고 관측 write를 rollback한다. 제품에 과거 DTO 호환 계층을 남기지 않는다.
필드별 수동 변환의 제거를 평가하며 개발 시간·실행 성능이나 모든 앱의 비용 감소를 수치로 주장하지 않는다.
SDK 호출과 결과 검사는 전후 타입에 맞춘 별도 테스트 소비자다. 클라이언트 호출 코드의 변경 비용까지 없어진다고 주장하지 않는다.

공통 모델 준비는 HTML과 JSON의 표현 차이를 없애지 않는다. 빈 Text Form 값은 빈 문자열이고 nullable JSON
입력의 생략·명시적 NULL과 다르다. 필수 HTML 선택 필드는 initial/default가 있어도 제출해야 한다.
같은 값의 비교에는 이에 맞는 입력을 사용하며, 원래 표현 차이와 누락 오류도 따로 확인한다.
공백 제거 뒤의 문자열 길이 제한은 실제 HTTP 입력과 OpenAPI의 정규화 메타데이터를 함께 대조한다.

## 완료 조건

- [ ] A1-01~02의 공통 모델 입력·Form 준비·저장을 타입·presence·기본값·실패 의미까지 연결한다.
- [ ] A1-03~05를 Article와 Helpdesk의 실제 CRUD·업무 명령·HTML/API/client에서 사용한다.
- [ ] A1-06의 외부 소비자·동일 변경 실험과 보안·데이터 무결성 부정 대조를 확인한다.
- [ ] 같은 source의 필수 환경·생성 drift·전체 CI 원본을 대조하고 현행 문서와 다음 F1의 경계를 갱신한다.

현재 `ModelInput`·공통 모델 준비·`crud.Resource`·확정 transaction 결과 경계를 구현하고 Article·Helpdesk에
연결했다. [ADR-0092](../docs/adr/0092-model-input-and-crud-assembly.md)에 장기 의미를 기록한다.
로컬은 포맷·최소 compile과 원격 실패를 좁힌 재현에 사용했다. 선행 표현 정책 검증의 실패 재현은 수정 후 통과했다.
추가한 독립 SDK의 인증 예상값 오류도 좁힌 SQLite 호출에서 수정 후 통과했다.
수정한 현재 source의 전체 시나리오·PostgreSQL·race·전체 CI 검증은 남아 있다.
구현·실행 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.
