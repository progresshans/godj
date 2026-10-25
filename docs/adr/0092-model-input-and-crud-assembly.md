# ADR-0092: 모델 입력과 Form/Admin/API CRUD 조립

- 상태: 채택, A1 구현·검증 진행 중
- 날짜: 2026-10-22
- 작업: [GDJ-0135](../../work/0135-model-centered-form-admin-api.md)

Schema IR의 타입·NULL·default를 DTO·setter·폼 변환·생성 builder마다 다시 적으면 필드 변경이 여러 계층으로
흩어진다. 이 반복을 줄이면서 입력 생략, 현재 객체·관계 권한, 선택 열 저장과 transaction outcome을 보존한다.

## 결정

`serializers.FromModel`의 Spec은 완전한 모델 source를 소유한다. generic `NewSpec`의 필드 이름만 같은 선언은
이 source를 대체할 수 없다. `api/input.NewModel`은 같은 parser·Body·schema 경로에서
`orm.ModelInput[M]`을 생성하며, `NewResource`는 CRUD 쓰기 목록과 JSON 노출의 일치도 시작 시 확인한다.
computed 출력은 원 모델 source를 유지하고 계속 read-only다.

`ModelInput`은 검증된 scalar의 presence와 별도의 collection 선택을 소유한다. SQL NULL, JSON null, 생략,
explicit zero를 구분한다. `forms/model.PrepareInput`은 모델 clean을 다시 실행하지 않고 같은 입력으로 전달한다.
폼의 초기 primary-key binding도 유지한다. 새 객체의 NULL key와 present zero key를 혼동하지 않으며 다른 객체로
binding을 교체할 수 없다. 원래 값의 사본은 최신 권한이나 revision 증거가 아니다.
입력 값도 불투명한 내부 pointer 뒤에 두어 진단 형식의 fallback에서 선택 값을 숨긴다. Target·collection·서버 값의
파생 입력은 내부 상태와 변경하는 map을 복사하므로 원래 입력을 수정하지 않는다.

`orm.ModelView[M]`은 명시적으로 선택한 scalar 값·완전한 모델 source·별도의 key 존재 여부를 소유한다.
원 모델 행이나 선택하지 않은 값을 보관하지 않으며, 입력 정책으로 저장된 값을 정규화하거나 현재 default로
대체하지 않는다. `Manager.View` 자체는 I/O나 인가를 수행하지 않는다. 읽기 owner가 현재 권한과 scope를 확인한다.
`WithCollections`는 같은 scope에서 명시적으로 읽은 완전한 M2M key 집합을 새 view에 담는다. 생략과 로드한 빈 집합을
구분하고 key 순서·중복·0/음수를 보존하며 모든 map/slice 접근은 사본이다. Relation 조회·권한·한도 검사는 owner가
소유하고 이 데이터 값이 대신하지 않는다. 내부 상태는 불투명한 pointer 뒤에 두어 잘못된 진단 형식의 reflection도
선택 값에 도달하지 못하게 한다.
`forms/model.InitialView`와 `BindView`는 같은 모델 source와 선택 값의 실제 존재를 검사한다. Form candidate에는
명시적으로 읽은 값과 key만 남기므로 model clean/validator는 제외된 필드의 존재 여부를 확인해야 한다. 최종 쓰기
검증은 권한을 다시 확인한 최신 전체 모델에서 수행한다. `api/output.ModelView`도 serializer의 명시적 allowlist와
완전한 source를 대조한다. 선택 값이 없으면 출력 실패이며 숨은 값이나 zero/default를 출력에 채우지 않는다.
Form/API의 M2M도 명시적으로 로드된 선택만 사용한다. Form에서 제외된 로드 값은 candidate 관측에는 남아도
쓰기 intent에는 포함하지 않는다. 이 일반 표현 기능은 ProfileReader의 별도 scalar 전용 노출 정책을 넓히지 않는다.

`orm.PreparedModel`은 새 객체의 전체 IR default와 현재 객체의 제외 열 보존을 공통으로 담당한다. 현재 모델·입력·반환
모델은 각자 소유하고, 선택 열 UPDATE와 no-op, ForceInsert, 생성 key 보존을 기존 ORM 저장 경로에 연결한다.
폼의 파일 게시·collection saver는 이 scalar 준비 위의 별도 단계로 남긴다. Migration default도 동일한 순수
IR scalar 변환을 사용하되 각 backend의 backfill 및 schema editor 책임은 바꾸지 않는다.

`crud.Resource`는 고정 모델 source·명시적 쓰기 allowlist·좁아지는 query 범위로 기본 CRUD를 조립한다. 현재 객체
조회 뒤의 Before callback, 서버 소유 값, 고유성 정책, collection saver와 host 삭제 정책은 명시한다.
값 할당·변경 열·unique 기본 경로를 공통화하고, 앱의 인가·revision·감사·관계 선택 정책은 숨기지 않는다.
생성된 facade 목록은 `ListQuery`에 그대로 연결해 eager/prefetch와 source 소유권을 유지한다.

`db.AtomicResult`와 `RelationAtomicResult`는 단 한 번 합류한 callback과 확정 commit 뒤에만 결과를 반환한다.
출력 준비·감사를 같은 callback에 넣는다. callback 생략·중복·늦은 실행·오류 삼킴·미완료와 불확실한 commit은
정상 성공이나 재시도로 바꾸지 않는다. 확정 commit 이후의 취소는 이미 성공한 write를 실패로 뒤집지 않는다.

`admin.ModelConfig[M]`의 `M`은 목록·상세 조회와 `Snapshot`/`Initial`의 타입이다. CRUD와 객체 명령은
공통 `MutationResult{ID, Revision}`만 반환하고, Update는 별도로 실제 변경 필드를 반환한다. Callback은 저장
owner가 확정한 값을 바로 전달한다. 저장 뒤 표시 모델을 만들기 위해 현재 권한으로 다시 조회하지 않는다.
자기 비활성화·권한 회수·삭제·세션 폐기가 성공했다면 그 결과로 조회할 수 없게 되어도 저장 성공을 유지한다.
이 결과는 조회 권한이나 저장 값의 사본이 아니며 화면의 다음 요청은 다시 현재 권한을 검사한다.

Registry는 현재 Admin URL의 양수 객체 key 경계, 같은 update/delete 대상, versioned create의 양수 revision,
변경 update의 증가·no-op과 delete의 관측 revision 유지, unversioned 결과의 revision 0을 확인한다.
객체 명령은 같은 ID와 관측값 이상의 revision을 반환한다. 변경 필드는 editable/inline allowlist에 속하고
중복이 없어야 하며 결과 slice는 복사한다. Callback 오류가 있으면 결과 전체를 버리고 원래 오류를 보존한다.
Callback 성공 뒤 잘못된 metadata가 발견되면 `ErrReconciliationRequired`로 구분하며 입력 거부나 자동 재시도로
바꾸지 않는다. 조회 `Snapshot`/`Initial`의 IR·필드/값·revision 검증과 저장 owner의 최종 인가·제약·감사·transaction
검증은 계속 필요하다. 저장 결과에서 표시 snapshot을 제거하는 것은 이 검증을 대신하거나 생략하는 허가가 아니다.

`db.ReadSnapshotResult`는 live validator가 있는 빌린 읽기 전용 scope에서 단 한 번 합류한 callback과 성공한
snapshot 종료를 확인한 뒤 결과를 반환한다. root backend·쓰기/transaction capability와 callback 생략·중복·
오류 삼킴·미완료는 결과를 게시하지 않는다. 읽기에서는 종료 중 취소도 결과를 버린다. 쓰기 commit unknown으로
해석하거나 자동 재시도하지 않는다.

## 애플리케이션의 차이

Article는 대상 조회 후 body를 읽고, Helpdesk Label/Ticket는 body 검증 뒤 write scope에서 현재 대상을 확인한다.
공통화는 이 순서를 바꾸지 않는다. Article publish와 Label ensure는 업무 명령으로 남으며 공통 transaction 결과
경계와 현재 조회·출력 준비를 사용한다.

Helpdesk Category는 기존처럼 읽기 전용이다. Ticket의 numeric no-op, 폼 JSON 동등성, JSON API exact-token 정책은
명시적 candidate 준비 정책이다. bulk는 전 후보 검증과 native batch·일관된 scope·동일 transaction을 계속 소유한다.
파일 upload를 일반 scalar 입력으로 바꾸거나 관계·inline·audit 실패를 부분 성공으로 처리하지 않는다.

선택 계정의 `CatalogResource[M]`도 실제 생성 타입의 `Manager`·`ModelInput`·`ModelView`를 같은 Form/JSON 경계에
연결한다. 이름을 바꾼 custom Group/Permission의 추가 scalar 필드는 별도 DTO에 복사하지 않는다. 순수 metadata와
선택 view를 전달해도 현재 인가·relation source·expected revision·audit는 catalog owner의 native scope에서 재검사한다.
이 typed 소비자의 작성·compile과 공통 Admin/HTTP 제품 조립 및 실제 환경의 실행 완료는 구분한다.

## 검증 경계

설계 채택과 제품 실행 PASS는 구분한다. 현재 로컬에서는 포맷·최소 compile만 확인하며 새 동작의 실행 검증은
아직 완료하지 않았다. 외부 생성 소비자, 동일 필드·권한·출력 변경 실험, 실제 HTTP/Admin 및 native DB·race·전체
platform 검증의 source와 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.
