# ADR-0073: OneToOne cardinality and reverse object ownership

- 상태: Accepted — 선언·이력·양 DB·단일 reverse와 Helpdesk 전체 소비자 구현. GDJ-0096의 process/platform 통합 검증 완료.
- 날짜: 2026-09-22
- 관련 작업: [GDJ-0096](../../work/0096-one-to-one-service-reports.md)

## 선언과 저장 의미

티켓에는 작업 보고서가 없거나 하나 있다. 이 관계를 일반 FK의 고유성 옵션과 구분하여 선언한다.
`schema.OneToOne`은 ForeignKey 저장 필드와 별도의 `ir.RelationOneToOne` cardinality를 갖는다.
Schema IR 정규화는 `Unique=true`를 보장하며 비어 있는 reverse 선언을 source model 이름으로 해석한다.
명시적 이름과 `NoReverse`도 지원한다. 기존 `ForeignKey(..., Unique())`의 reverse는 collection으로 유지한다.
Target은 현재 AutoField이며 nullability와 PROTECT/SET_NULL은 기존 FK 규칙을 따른다.

고유성은 [ADR-0072](0072-column-uniqueness-and-constraint-ownership.md)의 양 DB 물리 제약이 소유한다.
사전 조회나 단일 객체 반환 API만으로 중복 저장을 막았다고 하지 않는다. Nullable 관계의 여러 SQL NULL은 허용한다.
역방향에서 자식이 없는 것은 required forward FK의 무결성 위반과 다르다. Required는 존재하는 자식의 FK 값에 대한 조건이다.

## 이력과 변경

Historical wire·digest·autodetect·생성 metadata는 cardinality와 reverse namespace를 보존한다.
Wire에 비고유 OneToOne이나 미해석 default reverse를 넣으면 거부한다. Standalone Add/Alter field도 declaring model에서
이미 정규화한 reverse 이름 또는 명시적 disabled 값이 필요하다. 검사용 synthetic model 이름으로 default를 만들지 않는다.

같은 target·delete policy·nullability·field identity에서 cardinality, reverse namespace와 그에 필요한 Unique를 함께
바꾸는 AlterField를 지원한다. 다른 field 속성 변경을 이 transition에 숨기지 않는다. Reverse 이름 변경은 target field와
다른 incoming 관계의 namespace 충돌을 검증한 뒤 한 번에 게시한다. Incoming target/count는 바뀌지 않는다.

`AlterFieldRelation` capability는 이 transition의 별도 실행 조건이다. Unique 값도 바뀌면 `UniqueConstraints`가 함께 필요하다.
고유성을 유지하는 FK+Unique → OneToOne과 reverse 이름 변경은 DDL 없는 metadata 변경이다. 비고유 FK → OneToOne은
실제 UNIQUE를 추가하며 반대 방향은 정확히 이전 Unique 상태를 복구한다. Metadata-only도 물리 catalog와 revision 검증을 생략하지 않는다.
기존 중복으로 실패하면 전체 migration의 행·catalog·recorder/revision을 보존한다. 자동 정리 없이 명시적 데이터 수정 뒤 재시도한다.

## Query와 객체 소유권

Typed/dynamic query는 cardinality를 보존하는 같은 AST를 사용한다. Forward/reverse path와 projection 생성자는
cardinality를 명시적으로 받는다. OneToOne을 many-to-one으로 바꾸어 저장하거나 기존 내부 ABI를 위한 별도 경로를 두지 않는다.

단일 reverse는 직접 scalar field의 현재 lookup·IN·nullable/Boolean과 AND/OR/NOT를 지원한다. Generated reverse group의
`IsNull(bool)`과 dynamic `report__isnull`은 자식 PK를 검사한다. Field isnull은 자식 부재와 그 field의 SQL NULL을 모두 포함한다.
실제 scalar 이름이 isnull이면 dynamic parser는 그 field를 우선하며 PK의 명시적 isnull 경로도 사용할 수 있다.
Presence lookup도 복제한 실제 자식 PK metadata로 LookupPolicy를 거친다. 잘못된 값·미지원 collection 문법·정책 거부는 partial predicate를 반환하지 않는다.

`RelationHop.Nullable`은 physical FK 속성이다. `Optional`은 traversal의 부재 가능성이며 reverse는 required FK여도 optional이다.
공통 JOIN 분석은 양 DB에 같은 존재 증명을 제공한다. AND의 합집합·OR의 교집합과 NOT의 polarity로 필요한 단일 edge를 구하고,
존재가 증명되면 INNER, 아니면 LEFT OUTER를 사용한다. Nullable negation은 없는 자식도 보존하도록 joined field의 NULL을 보정한다.
같은 FK가 반대 방향의 경로에서 OneToOne 여부를 다르게 선언하면 SQL 전에 거부한다. 일반 FK+Unique의 collection은
직접 non-null exact와 최상위 conjunction을 유지하며 단일 관계의 확장에 따라 OR/NOT를 묵시적으로 허용하지 않는다.

Reverse 생성 ABI는 v3, object v5, selection v6, facade v10다. 공통 selection 타입과 caller-owned binding·reverse selector가 snapshot에 반영되므로 같은 schema에서 만든
이전 생성물과도 섞을 수 없다. 각 generator role의 version이 snapshot을 바꾸는지 검사하고 모든 checked-in project를 함께 재생성한다.
Source field의 Go 이름 IsNull이 새 method와 충돌하면 생성 단계에서 명시적으로 거부한다.

Generated reverse factory는 OneToOne에 `RelatedObject[T]`, 일반 FK+Unique에 `RelatedSet[T]`를 반환한다.
단일 역방향 조회는 최대 두 행으로 cardinality를 검사한다. `Get(ctx)`의 `(value, present, error)`에서 부재는
`zero, false, nil`이며 성공한 빈 결과도 cache한다. 두 행 이상은 integrity 오류다. Forward의 존재해야 하는 target 누락은
기존 missing 오류를 유지한다. Cache 없는 unsaved owner 조회는 I/O 전에 missing-primary-key 오류이며 명시적 PK 0과 구분한다.

각 From/Fresh와 materialization은 cache를 독립 소유한다. 같은 handle의 동시 조회는 기존 QuerySet 평가 owner를 공유한다.
취소·query/scan/rows-close 실패는 성공 결과로 게시하지 않는다. Pointer handle의 zero/nil/dereference-copy를 거부한다.
외부 insert가 warm missing cache를 자동 갱신하지 않으며 Fresh로 다시 읽는다.
기존 RelatedObject처럼 온전히 읽은 cardinality 위반 snapshot은 반복 접근에서도 같은 진단을 유지하고 Fresh로 다시 평가한다.
이는 I/O 실패의 partial result를 성공 cache로 게시하는 것과 구분한다.

Reverse prefetch는 owner key를 중복 제거한 한 batch를 읽고 전체 membership/cardinality 검증 뒤 게시한다.
반복된 owner도 각각 독립 cache를 받는다. 빈 owner 목록은 검증 뒤 I/O 없이 빈 결과를 반환하며 기존 999 distinct key 한도를 유지한다.
Django의 descriptor exception·상호 객체 cache와 Go handle의 표현 차이는 [DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)에 기록한다.

## 단일 관계의 eager tree

`RelatedSelect[S,T]`와 닫힌 `RelatedSelection[S]`는 forward와 OneToOne reverse를 같은 row scanner·evaluation·cache 경로에 연결한다.
`ResolveRelatedSelectPath`는 직접 single accessor를 해석하고 `WithChildren`은 구체 중간 Go type을 유지한다. 최대 64 hop·중복 포함 1024 node의 기존 한계를 유지한다.
Projection occurrence는 root부터 traversal accessor 경로로 구분한다. 다른 child 모델이 같은 FK 이름을 쓰거나 같은 물리 선언을 다시 지나도 합치지 않는다.
물리 Source/Target과 traversal From/To를 구분하고 모든 parent prefix·선언·target column·child FK를 검증한다.
모델 projection의 물리 expression 생성은 공통 query layer가 소유하며 scalar DTO/order API를 암묵적으로 확장하지 않는다.

Forward는 source FK와 target PK, reverse는 owner PK와 child FK가 일치해야 한다. Child PK가 NULL인 온전한 빈 projection만 정상 부재다.
Nullable scalar가 모두 NULL인 실제 child는 존재한다. 없는 ancestor 아래의 present/partial descendant를 거부한다.
같은 occurrence의 owner에 서로 다른 child key·presence가 나오면 cardinality 오류다. 다른 collection filter 때문에 반복된 동일 owner/child는 허용한다.
각 행·Get·All·SelectedGraph 반환은 mutable field와 subtree를 독립 복제하며 Close·취소·전체 행 검증이 끝나기 전에는 결과를 게시하지 않는다.

Reverse의 ready cache는 존재·부재 모두 owner FK 조건의 재조회 plan을 보존한다. `Fresh`는 원래 child PK 대신 owner 기준으로 다시 읽으므로
외부 insert·재할당·교체를 관찰한다. Forward NULL FK의 영구적인 정상 부재와 혼동하지 않는다. Cold Count는 구조·binding 검증 뒤 projection을 제거한다.

Generated `BindObjectsIn(binding)`/`BindReverseObjectsIn(binding)`은 하나의 명시적 project binding 안에서 typed factory를 조합한다.
각 `BindObjects()`/`BindReverseObjects()`는 독립 binding을 만들며 서로 섞은 tree는 I/O 전에 거부한다. Reverse factory의 `SelectReport(children...)` 같은
selector와 `FromSelected` bridge로 forward/reverse tree를 읽는다. 표준 `Objects`는 이제 모든 단일 traversal을 한 binding에서 제공하며
facade는 이 factory를 사용한다. `.Related.Report.WithChildren(...)`와 `SelectRelatedPaths("report__ticket__review")`는 같은 immutable plan으로 수렴한다.
정방향 storage field 목록과 reverse를 포함한 traversal 목록을 구분하여 reverse를 부모의 저장 column으로 다루지 않는다.

Reverse-only 모델도 facade의 New/Save를 지원한다. Unsaved owner는 객체를 만들 수 있지만 cache 없는 reverse 접근은 I/O 전에 missing-primary-key 오류다.
부모 Save가 PK를 게시하면 reverse handle을 그 key에 다시 바인딩하고 명시적으로 할당한 child cache를 유지한다. 정상 missing cache를 외부 child 저장이 자동 갱신하지 않는다.
같은 facade 객체의 반복 관계 접근은 같은 target pointer를 보존하고, 다른 materialization·경로 occurrence는 독립 소유한다.
Forward assignment/raw FK 변경은 이미 선택한 reverse 형제 cache와 그 subtree를 유지한다. 다른 facade origin, 잘못된 중간 Go type,
collection/blank/unknown/과도한 문자열 경로와 promoted field·selector 이름 충돌을 거부한다.

Django의 12개 eager 관찰은 양 DB에서 row/presence·초기 SELECT 1회·JOIN 형태가 같다. 세 reciprocal path에서는 후속 descriptor 접근이
각각 1·1·3회 추가 SELECT를 수행했다. GoDj의 명시적 selected subtree는 12개 모두 warm I/O 0을 유지한다.
이는 cache 소유권 차이인 [DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)에 기록하며 조회 횟수 전체의 parity로 계산하지 않는다.

## 할당과 저장

Forward `WithTicket`/`WithTicketID`와 `ClearTicket`는 원본을 보존한 새 wrapper를 반환한다. 명시적 OneToOne은 required여도
Clear를 제공한다. Required FK의 raw int64 0과 별도 scalar-presence/assigned-absent 상태를 함께 두며, getter·Unwrap·Save는
이 부재를 required 오류로 I/O 전에 거부한다. `WithTicketID(0)` 또는 key-present target의 PK 0은 별도의 정상 값이다.
Nullable Clear는 SQL NULL로 저장할 수 있다. 일반 required ForeignKey에 새로운 Clear를 추가하지 않는다.

Reverse `SetReport(child) error`는 DB I/O 없이 지정한 owner와 child를 제자리에서 변경한다. Owner의 reverse cache는 정확한
child pointer를, child의 forward cache는 정확한 owner pointer를 보존한다. 두 wrapper를 복제한 candidate에 raw FK reconciliation,
cache tuple·self·origin·PK 검증과 모든 fallible rebuild를 마친 뒤 두 결과를 게시한다. 검증 실패는 두 원본의 snapshot/cache를
부분 갱신하지 않는다. 자기 자신을 지정한 경우도 forward/reverse 양 edge를 한 candidate에 반영한다. Thread-safe mutable graph는
아니므로 caller가 관련 wrapper의 접근과 변경을 함께 serialize한다.

`SetReport(nil)`은 이미 cache한 자식이 있으면 그 자식의 forward FK/cache와 owner의 reverse cache를 해제한다.
Cold 또는 known-absent cache면 I/O 없이 no-op이며 아직 읽지 않은 DB 자식을 찾아서 바꾸지 않는다. Cold clear 뒤 첫 getter는
DB를 읽어 기존 자식을 반환할 수 있다. 다른 자식으로 교체해도 이전 자식의 FK를 자동으로 지우거나 저장하지 않는다.
양 DB의 UNIQUE가 충돌을 거부하며 caller가 실제 자식 저장 순서와 transaction을 정한다.

Unsaved owner도 child를 명시적으로 할당할 수 있고 warm getter는 두 exact pointer를 반환한다. Child Save는 owner의 PK가 없으면
I/O 전에 unsaved-related 오류다. Owner를 먼저 Save하면 새 PK를 게시하면서 reverse assignment cache를 보존한다.
이후 child Save가 pending forward FK를 owner의 PK로 reconcile한다. Owner Save는 reverse child를 자동 저장하지 않는다.
직접 FK 변경은 기존 규칙대로 pending assignment를 대체하며, 같은 명시적 scalar tuple은 warm target identity를 유지한다.

저장 실패는 사용자가 지정한 메모리 값을 자동 복구하거나 다른 wrapper의 cache를 바꾸지 않는다. 명시적 retry/reassignment를
통해 복구한다. Transaction은 DB의 선행 쓰기까지 rollback하지만 heap은 되감지 않는다. 다른 owner의 stale cache는 자동 invalidation하지 않는다.
필수 Clear의 사전 거부와 실패한 unsaved child Save 뒤 reciprocal cache 보존은 Django와 다른 경계이므로
[DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)에 기록한다.

명시적 target PK는 DB row 존재를 보장하지 않으며 native FK가 이를 검사한다. SQLite의 수동 PK INSERT와 달리 PostgreSQL의
수동 identity INSERT/sequence 조정은 현재 unsupported다. 관계의 PK presence를 값 0이나 해당 backend 지원 여부와 혼동하지 않는다.

## 삭제

PostgreSQL은 `AtomicRelation`과 bulk SET_NULL을 일반 Atomic과 같은 transaction/session 수명으로 실행한다.
PROTECT와 SET_NULL/delete는 한 transaction에 속한다. Callback 오류·확정 rollback·commit/rollback outcome unknown과
native cause를 구분하며 자동 재시도하지 않는다. SQLite의 기존 relation transaction/quarantine 의미도 유지한다.
Incoming policy를 갖는 모델에 canonical outgoing FK가 있어도 deleter를 바인딩할 수 있다. 삭제는 그 target의 AutoField PK만 지우며
참조하는 부모 행을 수정·삭제하지 않는다. Incoming policy fingerprint·descriptor 전체 metadata·PK clear의 non-PK 보존 검사를 유지한다.
FK를 읽지 못하는 descriptor는 transaction callback 전에 실패한다. 관계를 PK로 쓰는 모델이나 일반 cascade collector를 허용한 것이 아니다.

## 작업 보고서와 명시적 관계 선택

Helpdesk ServiceReport는 필수 OneToOne `ticket`, 필수 Text `summary`, Boolean `completed`를 가진다.
Ticket의 수명·기존 필드는 그대로 유지하며 보고서의 reverse 부재는 정상적인 `null` 응답이다.
`helpdesk_0017_service_report`는 기존 Category/Ticket 행을 보존하고 보고서 table을 별도로 만든다.
보고서를 삭제하면 티켓은 남고 보고서가 참조하는 티켓의 삭제는 PROTECT로 거부된다.

`forms.ModelChoiceField`는 int64 key와 표시명의 명시적 snapshot이다. Bind는 I/O를 하지 않고 목록이 없으면
모든 nonempty 입력을 거부한다. `Spec.WithModelChoices`는 구조·validator를 유지하는 독립 snapshot을 반환한다.
`forms/model`은 AutoField 대상의 정규화된 ForeignKey/OneToOne을 이 필드로 투영한다. 이는 Django의 QuerySet/model-instance
소유권을 모방한 것이 아니다. 같은 pinned Django ModelChoiceField의 required/empty·integer 별칭·NUL·membership·raw-input
변경 감지를 독립 [runner](../../conformance/runners/django/model_choice_reference.py)로 비교한다. 임의 target/to_field는 미지원이다.

Admin은 선택한 relation마다 `RelatedChoices` source를 요구한다. 등록은 I/O가 없으며 모든 target 읽기 권한을 확인한 뒤
요청별 source를 호출한다. 실제 Authorizer와 Principal snapshot을 둘 다 검사한다. 잘못된 내부 Form은 source 조회 전에 거부한다.
Bind용 선택지를 요청 간 공유하지 않고 write callback 직전에도 목록을 다시 확인한다. 이것만으로 race를 닫았다고 하지 않는다.
Application은 같은 transaction에서 현재 보고서·선택 티켓의 Category 범위와 고유성을 검사하고, 실제 native 제약이 최종 무결성을 소유한다.
변경된 선택 범위는 `ticket/invalid_choice`, 사전 고유성은 `ticket/unique`, native 중복은 `__all__/unique`로 반환한다.
취소·조회·driver·rollback/commit 불확실성은 실행 오류다. 오류가 합쳐진 not-found/PROTECT를 정상 404/삭제 차단으로 축소하지 않는다.

Report CRUD는 각각 명시적인 view/add/change/delete 권한을 요구한다. Admin 선택지는 티켓 subject를 열거하므로
추가로 ViewTicket이 필요하다. API의 명시적 key 입력과 reverse 부재 조회는 report 권한·Category 범위 안에서 수행하며
티켓 subject를 노출하지 않는다. GET collection/detail/reverse, POST, PUT, PATCH, DELETE를 같은 operation 선언에서
실제 handler와 OpenAPI에 연결한다. 삭제 응답은 body 없는 204다. 고정 ogen client가 실제 HTTP·cookie/CSRF로 이 계약을 소비한다.

`systemstate.Runtime.AtomicRelation`은 backend의 명시적인 `CoordinatedAtomicRelation`을 요구한다.
SQLite의 BEGIN IMMEDIATE/admission·FK readback·quarantine과 PostgreSQL의 schema advisory lock을 일반 cooperative write와 공유한다.
같은 borrowed session이 ordinary mutation과 SET_NULL을 소유하며 callback은 한 번만 실행한다. 일반 AtomicRelation로 우회하거나
중첩 transaction을 시작하지 않는다. 미지원 backend는 I/O 전에 명시적 오류다.

## 구현과 남은 범위

Cross-app 생성 소비자가 required/nullable 관계, reverse exact 조회·단일 lazy/prefetch, forward eager와 양 DB 삭제를 사용한다.
독립 [Django runner](../../conformance/runners/django/one_to_one_reference.py)의 관찰을 기준으로 하되 전체 37개 관찰의 parity를 주장하지 않는다.
직접 reverse lookup은 별도 41개 Django 관찰로 결과·SELECT 수·JOIN 형태를 비교했다.
Typed reverse/mixed eager tree와 facade의 reverse selector·문자열 mixed path, forward/reverse assignment를 구현했다.
14개 독립 Django assignment 관찰과 실제 양 DB의 저장/rollback을 비교하고 표현·cache 차이는 별도로 기록했다.
Helpdesk Form/Admin/API/OpenAPI/client는 구현했다. 여러 단계 reverse 조건 조회는 남아 있다.
Relation-as-PK·arbitrary target·상속·ManyToMany도 별도 미완료 범위다. 실행 source·환경은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
