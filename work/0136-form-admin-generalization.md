# GDJ-0136: Form/Admin의 관계 선택과 typed 편집 일반화

- 상태: F1 구현 중 — A1 완료 후 주 작업 전환; typed inline·bounded 선택·독립 비교의 원격 검증 진행
- 시작: 2026-10-22
- 구현 기준: `ffc8b5ad0bcb17aaa548f8aab71835ad3db68879` — A1 구현과 같은 source의 전체 CI 원본 대조 완료
- 근거: [전체 실행 계획의 F1](../docs/EXECUTION_PLAN.md), [개발 기준](../docs/DEVELOPMENT_CRITERIA.md),
  [모델 Form](../forms/model/README.md), [Admin inline](../admin/inlines.md),
  [공통 모델 입력과 CRUD](../docs/adr/0092-model-input-and-crud-assembly.md)

A1의 공통 모델 준비·현재 행 기반 저장을 Form/Admin의 관계 선택, 여러 행·inline 편집과 파일에 연결한다.
A1 검증 중에는 별도 작업트리에서 독립적인 요구·설계와 다음 구현을 준비했다.
A1의 같은 source 전체 검증·원본 대조를 확인해 주 작업을 전환했다. A1의 검증된 작업트리는 보존한다.

## 현재 출발점

- `Definition`, `BindInstance`, `InstanceSet`, `InlineSet`, `PreparedSet.SavePlan`과 명시적 파일 게시가 있다.
  scalar·collection·파일의 준비 및 저장 전체를 다시 구현하지 않는다.
- `admin.NewInline`은 typed 설정을 받아 읽기용 등록으로 지운다. Helpdesk의 최종 writer는 같은 자식 Form,
  SetConfig와 canonical 관계를 다시 구성해 현재 권한·행으로 바인딩한다. 이 반복과 정책 불일치 위험을 줄인다.
- `RelatedChoices.Load`는 전체 `[]forms.Choice`를 읽는다. 선택한 key 확인과 검색 결과의 페이지를 구분하는
  공통 경계가 없으며, 목록이 커질 때 모델 Form마다 전체 선택지를 반복해서 렌더링한다.
- F1 시작 시 Admin의 `CollectionFormSet`은 새 객체 생성만 지원했다. 기존 행 편집·삭제·정렬·파일과 관련된
  공통 준비는 하위 FormSet에 있지만 Admin의 반복 가능한 화면·현재 저장 연결은 더 넓게 검증해야 한다.
- 필드·cross-field·모델 clean·집합 validator와 고정 widget을 지원한다. 등록한 widget의 표현과
  검증/저장 의미를 분리하고 원래 입력·복합 오류·read-only·secret 값을 안전하게 전달하는 확장 경계를 정리한다.
- 현재 자동 브라우저 owner는 Helpdesk의 조회 화면을 검사한다. 기존 inline/업로드 브라우저 fixture와
  새 관계 선택·여러 행 저장의 실제 화면을 원격 CI에 연결해야 한다.

## 하위 요구와 수용 시나리오

| ID | 구현할 결과 | 완료에 필요한 실제 동작 |
|---|---|---|
| F1-01 | 같은 typed inline 선언을 초기 화면과 현재 행 기반 재바인딩에 사용한다. | 자식 Form/Set/관계의 재선언 없이 현재 parent·cohort·권한·choice·revision을 검사한다. 새 부모 key 전달, FK·OneToOne, 조회 전용 기존 행과 허용된 새 행을 확인한다. 다른 source·등록·prefix·부모·PK의 입력을 거부한다. |
| F1-02 | FK/OneToOne/M2M 선택에 bounded 검색·페이지·선택 key 해석을 제공한다. | 전체 target 행을 렌더링하지 않고 현재 선택과 검색 페이지를 구분한다. 현재 target 권한·scope·안정된 정렬, page 경계·중복/위조 key·오래된 선택과 취소를 실제 DB/HTTP에서 검사한다. 선택 목록에 보였다는 사실을 저장 권한으로 사용하지 않는다. |
| F1-03 | 공통 typed FormSet 준비를 Admin의 여러 행 편집과 inline 저장에 연결한다. | 신규/변경/미변경/삭제/정렬·collection-only·read-only가 같은 정책을 사용한다. 서버 parent와 PK·management/cohort를 재검사하고 뒤쪽 행/collection/audit 실패는 전체 DB 작업을 rollback한다. 동일 입력을 여러 모델 구조와 cross-app 관계에서 사용한다. |
| F1-04 | 지원된 scalar·collection·모델 파일의 명시적 준비/저장 단계를 함께 사용한다. | 파일 교체·clear·보존·read-only·여러 행 업로드와 늦은 실패를 연결한다. 파일의 부분 게시/unknown outcome과 DB rollback을 구분하고 필요한 게시 결과를 보존한다. DB 실패를 파일 게시 취소나 자동 재시도 성공으로 오인하지 않는다. |
| F1-05 | widget·validation·read-only·action/command 확장 경계를 명시한다. | 기존 입력 타입과 IR의 제약을 유지하면서 표시를 바꾸고 field/model/set 오류를 합성한다. 잘못된 등록은 시작 시 거부하며 입력·오류·파일 재선택·키보드 동작과 escaping을 확인한다. Password와 숨겨진 필드, source가 없는 computed 값을 노출하거나 writable로 승격하지 않는다. |
| F1-06 | 구조가 다른 두 앱과 외부 cross-app 소비자에서 같은 공개 경로를 사용한다. | Helpdesk의 Ticket–Label–ServiceReport와 독립 생성 Inventory 관계 모델에 실제 HTTP/Admin·브라우저·DB 저장을 연결한다. 필드/choice 정책 변경의 수동 책임을 전후 비교하고 정상/오용 compile, 위조·경합·rollback과 원본 증거를 남긴다. |

F1은 현재 지원된 모델 필드와 관계를 일반화한다. S1/S2/G1의 새 타입·key·spatial 기능, I1의 custom user,
L1의 번역·지역화는 해당 묶음에 남기되 같은 Form/Admin 경계에 연결할 책임을 제거하지 않는다.
T1의 template 언어 확장이나 A2의 일반 API/router/client 도구를 F1 완료 조건에 새로 합치지 않는다.

## 유지할 의미와 설계 경계

- Schema IR과 canonical project binding이 모델·관계·default·nullable·choices·파일 의미를 소유한다.
  필드 노출과 UI·업무 권한은 명시적 정책이며 새 필드를 자동으로 공개하지 않는다.
- 재사용 선언은 불변이다. 요청별 원래 입력, 현재 행·관계 snapshot, 파일 capability와 준비 결과를 공유하거나 cache하지 않는다.
  callback은 context/error와 수명을 유지하고 순수 바인딩 뒤에 몰래 DB I/O를 숨기지 않는다.
- 초기 admission과 Site의 deny overlay로 허용한 동작을 최종 writer가 넓힐 수 없다. 현재 principal·객체·관계
  권한은 쓰기 scope에서 다시 확인한다. 서버에서 얻은 parent·PK·cohort·revision을 사용하고 제출 값을 믿지 않는다.
- 선택 검색과 제출 key 검증은 별도 연산이다. 페이지 밖의 현재 선택을 누락하지 않고, 승인되지 않은 객체의 label을
  오래된 입력·오류 재표시로 노출하지 않는다. 결과 overflow·잘못된 페이지·잘린 loader 결과는 명시적 오류다.
- 관리 화면의 준비 결과는 commit 증거가 아니다. 부모·자식 scalar·선택 collection·삭제 정책·audit를 같은
  borrowed transaction에 연결하고 확정 commit 후 결과를 게시한다. source가 다른 입력과 expired session은 거부한다.
- 파일 게시를 DB transaction의 일부처럼 숨기지 않는다. 기존 `SaveFiles`와 게시 receipt를 사용하고 후속
  DB 실패/불확실한 결과에서도 이미 시도한 파일 작업을 보존한다. 임의 삭제 보상이나 자동 재시도는 하지 않는다.
- 새 공통 경계는 기존 내부 함수 모양을 보존하기 위한 wrapper가 아니다. 실제 소비자의 중복 선언을 제거하며,
  generic runtime·생성된 typed 모델·등록 시 metadata·애플리케이션의 현재 정책 책임을 나눈다.

## 구현 순서와 검증

1. typed inline 선언을 유지하고 같은 정책으로 현재 snapshot을 다시 바인딩하는 경계를 먼저 구현한다.
   Helpdesk의 실제 writer를 연결하며 기존 권한·cohort·OneToOne unique·revision·audit 실패 의미를 보존한다.
2. 선택 결과의 검색/페이지와 선택 key 해석을 분리하고 일반 Form·inline·여러 행 화면에 연결한다.
   렌더링 widget과 서버 검증은 같은 선언을 사용하며 browser 동작이 서버 검사를 대체하지 않는다.
3. 기존 행·collection·파일이 있는 여러 행 편집과 명시적 확장점을 연결한다. 독립 생성 소비자에서도
   같은 공개 API를 사용하고 실제 변경 비용을 기록한다. 파일 수나 개발 시간의 추정치를 성과로 쓰지 않는다.
4. 필요한 제품·생성기·소비자·실패 검사를 함께 정리한 뒤 포맷·최소 compile을 확인하고 push한다.
   실제 DB·race·process·브라우저·generated drift·전체 platform은 기존 원격 CI owner가 실행한다.

기존 고정 Django 6.1 FormSet 저장·inline read-only 관측을 재사용한다. 관계 검색과 widget에 추가한 외부 의미는
[Django Admin](https://docs.djangoproject.com/en/6.1/ref/contrib/admin/#django.contrib.admin.ModelAdmin.autocomplete_fields)과
[ModelFormSet 저장](https://docs.djangoproject.com/en/6.1/topics/forms/modelforms/#saving-objects-in-the-formset)의
고정 버전 실제 관측으로 대조한다. Python 구현 구조나 Select2 자체를 Go 내부 설계로 복제하지 않는다.
Go 고유 source·수명·게시 결과는 독립 native 실패/경합 검사로 검증한다.

## 완료 조건

- [x] A1의 현재 source 전체 검증을 확인하고 F1으로의 주 작업 전환을 기록한다.
- [ ] F1-01~02의 재사용 선언·현재 바인딩·관계 선택을 실제 앱에서 사용한다.
- [ ] F1-03~05의 여러 행·파일·read-only·확장과 실패 경계를 연결한다.
- [ ] F1-06의 두 앱·외부 cross-app·동일 변경·실제 브라우저와 native DB 증거를 확인한다.
- [ ] 같은 source의 전체 CI 원본과 필수 실행을 대조하고 현행 문서 및 다음 C1 경계를 갱신한다.

현재 `ModelInline`의 공통 재바인딩과 Helpdesk·Admin browser fixture 연결, 관련 권한/source·현재 값·revision
검사를 작성했다. 별도 생성 Directory–Inventory의 baseline·필드/권한 변경 버전은 같은 typed 선언으로 현재 값,
새 부모·삭제·read-only·scope 거부·cohort 변동·늦은 실패 rollback을 검사하도록 연결했다.
기존 inline·업로드 probe는 원격 browser owner에 연결했고 native 저장/정리 증거를 추가했다.
`ModelChoices`는 canonical FK/OneToOne/M2M 및 명시적 target-only 선택의 bounded Search와 선택 key Resolve를 구분한다. 읽기 전용 target
key capability, 안정된 root PK 보조 정렬, 실제 DB scope/취소/수명·오류 검사를 추가했다. `SetPreflight`와
`InlinePreflight`는 management·PK/cohort·parent를 먼저 검사하고, Admin writer는 행별 선택 snapshot으로 검증을
이어간다. 지원되는 정수 ModelChoice의 기본값/현재 값을 입력 누락으로 보존하는 경우에도 선택 범위를 확인하며 기존 PostClean 순서는 유지한다. canonical FK default는 현재 IR에서 지원하지 않는다.
로컬의 최소 compile과 아직 실행하지 않은 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.
Admin의 일반 Form·command·inline·여러 건 생성에 선택 key Resolve와 별도의 검색 페이지를 연결했다.
Helpdesk와 identity는 전체 target loader 대신 canonical 선언과 현재 범위를 사용한다. 검색 widget은 native select와
문자열 ID를 유지한다. source 검토에서 기존 inline control initial의 혼입, 저장 직전 거절된 label의 재사용,
readonly 취소 소거를 수정하고 회귀 검사를 추가했다. 새 실제 browser 흐름과 같은 source의 전체 실행 검증은 진행 중이다.
고정 Django Admin의 autocomplete·Form 관측기와 Go의 native 비교를 작성했다. 두 DB의 94개 입력 가운데
49개 공통 동작과 45개 정책 차이를 분리하고, 실제 HTTP commit·현재 scope·source 원본·정리 결과를 보존한다.
이 비교의 로컬 최소 compile만 확인했으며 독립 관측과 native 본문은 원격 CI에서 실행해야 한다.
`ModelCollectionFormSet`에 canonical source·현재 전체 cohort와 CRUD deny overlay를 선언하고, sealed 제출을
같은 선언의 `BindCurrent`로 writer에서 재검사하도록 연결했다. Helpdesk의 생성은 기존 native bulk writer를 유지하며
새 편집은 `SavePlan`과 같은 borrowed transaction에서 scalar/collection·생성·삭제·audit를 처리한다.
행 순서·PK·revision·관리 입력·선택 범위·읽기 전용과 정확한 결과 대조, 실제 HTTP 혼합 저장과 늦은 실패,
multipart 모델 파일의 현재 바인딩·부분/unknown 게시·request 수명 검사를 작성했다.
이 검사의 본문과 실제 DB 저장은 원격 CI에서 확인해야 한다. 최소 compile은 실행 성공의 근거가 아니다.
별도 생성 Directory–Inventory 컬렉션 소비자에 FileField·revision fencing·보호된 삭제·transactional audit를 연결했다.
기본/필드·권한 변경 모델에서 같은 공개 선언과 writer를 사용하고, 양 DB의 19개 사례와 잘못된 typed loader/file saver
compile 거부를 필수 실행으로 작성했다. 파일 게시 뒤 DB rollback/unknown 결과, 읽기 전용/삭제의 stale revision,
현재 scope/cohort와 오래된 권한을 대조한다. 두 생성 모듈의 최소 compile만 확인했으며 실제 native 실행은 미검증이다.
실제 브라우저 컬렉션 편집과 widget 확장, 전체 원격 원본 대조는 계속 남아 있다.
이 구현 묶음이나 이 파일의 요구 정의를 F1 전체 PASS로 계산하지 않는다.
