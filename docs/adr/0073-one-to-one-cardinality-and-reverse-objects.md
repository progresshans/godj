# ADR-0073: OneToOne cardinality and reverse object ownership

- 상태: Accepted — 선언·이력·양 DB·단일 reverse object/prefetch 기반 구현. 전체 소비자 연결은 GDJ-0096에서 진행 중.
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

Reverse 생성 ABI는 v3, object v5, selection v6, facade v9다. 공통 selection 타입과 caller-owned binding·reverse selector가 snapshot에 반영되므로 같은 schema에서 만든
이전 생성물과도 섞을 수 없다. 각 generator role의 version이 snapshot을 바꾸는지 검사하고 모든 checked-in project를 함께 재생성한다.
Source field의 Go 이름 IsNull이 새 method와 충돌하면 생성 단계에서 명시적으로 거부한다.

Generated reverse factory는 OneToOne에 `RelatedObject[T]`, 일반 FK+Unique에 `RelatedSet[T]`를 반환한다.
단일 역방향 조회는 최대 두 행으로 cardinality를 검사한다. `Get(ctx)`의 `(value, present, error)`에서 부재는
`zero, false, nil`이며 성공한 빈 결과도 cache한다. 두 행 이상은 integrity 오류다. Forward의 존재해야 하는 target 누락은
기존 missing 오류를 유지한다. Unsaved owner는 I/O 전에 missing-primary-key 오류이며 명시적 PK 0과 구분한다.

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

Reverse-only 모델도 facade의 New/Save를 지원한다. Unsaved owner는 객체를 만들 수 있지만 reverse 접근은 I/O 전에 missing-primary-key 오류다.
부모 Save가 PK를 게시하면 reverse handle을 그 key에 다시 바인딩한다. 정상 missing cache를 외부 child 저장이 자동 갱신하지 않는다.
같은 facade 객체의 반복 관계 접근은 같은 target pointer를 보존하고, 다른 materialization·경로 occurrence는 독립 소유한다.
Forward assignment/raw FK 변경은 이미 선택한 reverse 형제 cache와 그 subtree를 유지한다. 다른 facade origin, 잘못된 중간 Go type,
collection/blank/unknown/과도한 문자열 경로와 promoted field·selector 이름 충돌을 거부한다.

Django의 12개 eager 관찰은 양 DB에서 row/presence·초기 SELECT 1회·JOIN 형태가 같다. 세 reciprocal path에서는 후속 descriptor 접근이
각각 1·1·3회 추가 SELECT를 수행했다. GoDj의 명시적 selected subtree는 12개 모두 warm I/O 0을 유지한다.
이는 cache 소유권 차이인 [DEV-0018](../DEVIATIONS.md#dev-0018--일대일-역방향-부재와-go-객체-소유권)에 기록하며 조회 횟수 전체의 parity로 계산하지 않는다.

PostgreSQL은 `AtomicRelation`과 bulk SET_NULL을 일반 Atomic과 같은 transaction/session 수명으로 실행한다.
PROTECT와 SET_NULL/delete는 한 transaction에 속한다. Callback 오류·확정 rollback·commit/rollback outcome unknown과
native cause를 구분하며 자동 재시도하지 않는다. SQLite의 기존 relation transaction/quarantine 의미도 유지한다.
Incoming policy를 갖는 모델에 canonical outgoing FK가 있어도 deleter를 바인딩할 수 있다. 삭제는 그 target의 AutoField PK만 지우며
참조하는 부모 행을 수정·삭제하지 않는다. Incoming policy fingerprint·descriptor 전체 metadata·PK clear의 non-PK 보존 검사를 유지한다.
FK를 읽지 못하는 descriptor는 transaction callback 전에 실패한다. 관계를 PK로 쓰는 모델이나 일반 cascade collector를 허용한 것이 아니다.

## 구현과 남은 범위

Cross-app 생성 소비자가 required/nullable 관계, reverse exact 조회·단일 lazy/prefetch, forward eager와 양 DB 삭제를 사용한다.
독립 [Django runner](../../conformance/runners/django/one_to_one_reference.py)의 관찰을 기준으로 하되 전체 37개 관찰의 parity를 주장하지 않는다.
직접 reverse lookup은 별도 41개 Django 관찰로 결과·SELECT 수·JOIN 형태를 비교했다.
Typed reverse/mixed eager tree와 facade의 reverse selector·문자열 mixed path를 구현했다. 여러 단계 reverse 조건 조회,
assignment의 전체 연결과 Helpdesk Form/Admin/API/OpenAPI/client는 남아 있다.
Relation-as-PK·arbitrary target·상속·ManyToMany도 별도 미완료 범위다. 실행 source·환경은 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
