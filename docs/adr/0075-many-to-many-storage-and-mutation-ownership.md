# ADR-0075: ManyToMany storage와 변경 소유권

상태: Accepted boundary — 제품 구현과 환경별 검증은 [GDJ-0099](../../work/0099-many-to-many-and-ticket-label-collections.md)가 구분한다.

## 요구

Ticket에 labels를 선언하고 기존 TicketLabel을 통해 컬렉션을 편집한다. Columnless 관계의 의미를 Schema IR에 한 번 정규화하고,
타입별 generated facade·공통 runtime·DB AST/compiler로 연결한다. 기존 명시적 연결 행·endpoint·Category와 권한 경계를 보존한다.
자동 intermediary와 부가 필드가 있는 명시적 intermediary, cross-app·자기 관계에서도 같은 의미를 사용한다.

고정 Django의 [독립 관찰](../../conformance/runners/django/many_to_many_reference.py)은 선언·자동 through·동시 중복 add,
set의 retained identity/payload·늦은 실패 rollback·self symmetry·query multiplicity·cache snapshot·실제 migration·signal을 기록한다.
이는 독립 작성한 public 입력(`derived=false`, Django 6.1, BSD-3-Clause)이며 제품 구현이나 전체 호환성을 뜻하지 않는다.

## 선택

ManyToMany는 owner table의 scalar/FK column이 아니다. 정규화된 relation identity·target·reverse namespace와 자동/명시적
through ownership·양 endpoint field 선택을 소유한다. 자동 storage는 deterministic하게 유도하고 명시적 through는 기존 모델을 참조한다.
Generated code와 historical state가 각자 별도의 관계 원본을 만들지 않으며 잘못되거나 오래된 binding은 I/O 전에 거부한다.
자동 through의 Add/Remove/Rename과 역방향은 endpoint 행을 보존한다. Rename은 retained link identity를 보존하고,
기존 TicketLabel에 선언만 연결하는 작업은 행을 재작성하지 않는다.

Columnless 선언은 `Model.ManyToMany`의 `ManyToManyField`에 둔다. `Fields`에는 실제 저장 컬럼만 남긴다.
`ThroughModel`은 모델 identity와 두 logical FK field를 명시해 같은 모델에 다른 FK가 추가돼도 선택이 바뀌지 않게 한다.
명시적 through는 nullable FK나 pair uniqueness의 부재를 선언 단계에서 다른 storage로 바꾸지 않는다. 실제 모델의 제약을 유지한다.
자동 storage는 별도 원본으로 저장하지 않고 `StorageSchema`로 유도한다. 이름은 `<owner>_<field>`, Go 타입은
`<Owner><Field>Link`, table은 `<owner table>_<field>`이며 source/target 두 CASCADE FK와 `relation_pair` ordered unique를 갖는다.
이 물리 필드 명명은 GoDj의 선택이며 Django의 Python intermediary 객체 이름과 같다는 주장이 아니다.
생성기와 ORM binding은 이 같은 projection을 사용한다. 생성된 schema companion은 logical 선언만 반환하고,
automatic model descriptor·공통 삭제 graph·프로젝트 모델 binding은 파생 storage를 포함한다.
선언과 storage projection을 혼합한 입력의 충돌을 거부하며 snapshot hash·clone·model equality가 columnless 의미도 소유한다.

기본 자기 관계는 symmetrical이며 별도 reverse를 만들지 않는다. Directed 자기 관계는 별도 reverse를 갖는다.
현재 Go 선언은 symmetrical과 named reverse의 동시 지정을 모순으로 거부한다. 이는 Django의 ignored-option warning과 다른
입력 정책이며 이 선언 subset의 명시적 한계다. Through field 선택의 자동 추론과 broader declaration options도 후속 범위다.
선언/metadata와 migration의 지원을 일반 collection manager의 완료로 합치지 않는다.

자동 through의 pair 추가에는 사전 존재 조회 이후 일반 INSERT만으로는 충분하지 않다. PostgreSQL의 동시 unique 오류는
이미 transaction을 실패 상태로 만들 수 있으므로 error를 나중에 정상 중복으로 바꿔서 계속 쓰지 않는다.
DB 독립 `ConflictInsertPlan`은 명시한 non-null unique tuple만 conflict target으로 삼고 모든 target field의 동일한 assignment를 요구한다.
관계 runtime은 자동 through 또는 실제 pair unique가 있는 명시적 through에서 해당 owned constraint를 target으로 만든다.
Pair unique가 없는 명시적 through의 변경 전략은 그 모델의 중복 허용 의미와 transaction 소유권을 따로 연결한다.
Backend는 `ON CONFLICT (columns) DO NOTHING`을 사용하고
다른 unique·FK·NOT NULL·CHECK·driver 오류를 보존한다. 무조건 모든 충돌을 무시하는 SQL이나 숨긴 재시도를 쓰지 않는다.

`db.ConflictInserter`는 일반 generated-key Insert와 다른 capability다. Root backend와 빌린 transaction session에서 지원하고,
없는 backend는 명시적으로 거부한다. 결과는 native affected row가 정확히 1이면 `inserted=true`, 0이면 false이며 오류면 false다.
삽입하지 않은 경우의 생성 PK를 합성하지 않고 SQLite의 이전 `LastInsertId`도 읽지 않는다. 0행은 존재 증명은 아니므로
trigger가 삽입을 생략했거나 다른 데이터가 개입할 수 있는 caller는 최종 membership을 별도로 확인한다. 일반 Insert의 1행 요구는 유지한다.
이 primitive의 true도 session 안에서는 provisional이다. 소유 transaction의 확인된 commit 이후에만 caller/cache에 성공을 게시한다.
Commit/rollback 불확실성과 SQLite 연결 격리·session 만료·취소의 기존 소유권을 그대로 사용한다.

관계 manager의 add/remove/clear/set는 같은 transaction에서 전체 후보를 검사하고 retained link identity와 payload를 보존한다.
Clear는 해당 owner의 연결 행을 삭제 root로 선택한다. 연결에서 endpoint로 나가는 FK를 따라 endpoint를 삭제하지 않는다.
연결 행을 참조하는 별도 incoming FK의 CASCADE·PROTECT·SET_NULL은 기존 collector가 전체 root 집합에 적용한다.
명시적 through의 추가 필드 제약은 정상 오류로 남는다. 대칭 자기 관계는 반대 방향 연결도 같은
변경에 포함하며, 조회 multiplicity는 명시적 Distinct 없이 축약하지 않는다.

## Collection runtime과 generated manager

`BindCollections()`는 project의 forward/reverse `orm.ManyToMany[Owner, Target, Through]` factory를 생성한다.
`From(backend, owner)`는 PK presence를 확인하고 독립 `ManyCollection`을 만든다. PK 0도 저장된 값으로 구분한다.
Columnless 선언, 선택한 두 FK, 같은 project snapshot의 양 endpoint/through descriptor와 incoming delete graph fingerprint를
함께 검증한다. 타입별 생성 코드는 키·payload/defaults의 직접 접근을 소유하고 transaction·집합 변경·cache는 공통 runtime이 소유한다.

`Add(ctx, targets, throughDefaults...)`와 `AddKeys`는 필요한 연결만 추가한다. Generated through Create input은
선택한 endpoint를 manager가 채우고 나머지 필드의 기존 default/nullable/validation을 적용한다. 신규 link를 만드는 defaults에 endpoint를
따로 지정하면 거부한다. 이미 있는 연결의 payload나 ID를 바꾸지 않으며 no-op에는 새 create의 required 값을 요구하지 않는다.
정확한 pair unique가 있으면 nullable column에도 실제 non-NULL assignment와 같은 metadata를 유지한 native conflict insert를 사용한다.
Pair unique가 없는 explicit through는 기존 duplicate를 보존하고 사전 조회에서 없는 pair만 일반 insert한다.
DB가 허용하는 동시 duplicate를 숨은 constraint나 retry로 바꾸지 않는다.

`Remove`, `RemoveKeys`, `Clear`, `Set`, `SetKeys`는 하나의 `AtomicRelation`을 소유한다. `Set`은 기본적으로 retained link를
보존하고 `ManyToManySetOptions{Clear:true}`는 기존 link를 지운 뒤 다시 만든다. 모든 새 create input을 검사한 뒤 삭제·삽입하고,
여러 삭제 root와 공유 CASCADE row를 model+PK로 합쳐 모든 PROTECT 검사를 먼저 수행한다. Nullable through의 NULL endpoint는
컬렉션 구성원이 아니므로 delta set과 특정 대상 remove에서 유지한다. Clear는 owner와 일치하는 NULL 연결도 삭제한다.
대칭 자기 관계의 mirror와 self link는 같은 변경에서 중복 없이 처리한다. 최종 membership을 다시 확인해 trigger의 0행 결과를
성공으로 오해하지 않는다. Concurrent set 전체를 serializable이라고 주장하지 않으며 더 강한 consumer fence는 별도 composition이 소유한다.

빌린 transaction session 안에서 성공한 insert는 provisional이다. 현재 root manager는 callback 횟수·동기 완료·원인 오류 보존을
확인하고 확인된 outer commit 이후에만 성공을 반환한다. Commit/rollback uncertainty를 그대로 전달하며 자동 재시도하지 않는다.
확인된 commit 뒤 늦은 context 취소로 결과를 실패로 바꾸지 않는다. 외부 transaction과의 명시적 composition은 후속 구현 범위다.

`Query()`는 동일 Query AST의 physical reverse join으로 target을 읽는다. 선택한 nullable FK의 metadata도 유지하며,
명시적 through의 duplicate는 `Distinct()` 요청 전까지 보존한다. 일반 ManyToMany traversal predicate와 prefetch는 후속 범위다.
Collection handle의 query cache는 mutex 아래 교체한다. Mutation 시작과 종료 모두 무효화하므로 실패·취소·unknown outcome이나
이전 in-flight 조회가 현재 handle에 오래된 cache를 남기지 않는다. 이미 반환한 QuerySet, 다른 owner materialization과 Fresh는
독립 snapshot을 소유하며 pointer handle의 zero/value-copy는 오류다.

독립 Django 관찰은 nullable/nonunique through의 set/remove/clear/reverse clear와 연결 행의 incoming 정책까지 확장했다.
제품의 통합 facade·외부 transaction composition·prefetch·Ticket 소비자·signal 완료를 이 root manager 구현과 합치지 않는다.

## Historical 선언 변경

Columnless 변경은 `AddManyToMany`, `RemoveManyToMany`, `RenameManyToMany`라는 별도 typed operation으로 표현한다.
저장 컬럼의 AddField를 가장하지 않는다. Add/Remove는 선언 목록의 삽입 anchor와 완전한 field를 보관하고, Rename은 이름·Go 이름 외
binding 변경을 허용하지 않는다. 기존 scalar/FK/constraint delta도 retained ManyToMany 의미를 변경할 수 없다.
Definition의 closed nested shape·사전 resource admission·semantic digest와 일반/최적화 historical state가 같은 의미를 보존한다.

명시적 through는 기존 모델을 소유하지 않는다. 양 DB의 `ExplicitManyToMany` capability와 schema editor는 원본 owner·target·through와
transitive FK graph의 실제 catalog를 검증하고, DDL이나 행 재작성 없이 같은 revision-fenced transaction에 이력을 반영한다.
Columnless source에 직접 FK가 없어도 through와 두 endpoint의 authority는 생략하지 않는다. Retained binding을 검증하는 scalar 변경도
이 capability를 요구한다. 현재 SQL projection은 해당 operation의 빈 statement group만 허용한다.

자동 계획은 explicit-through 선언을 모델·선택한 FK 생성 뒤에 추가한다. Cross-app의 모델이 이미 있어도 필요한 FK가 아직 없는
중간 prefix라면 선언을 지연한다. Dependency ancestry와 모든 재개 prefix의 남은 bytes를 보존하며, 모호한 rename이나 retained 선언의
순서 변경·binding retarget을 추측하지 않는다. 자동 intermediary도 같은 계획에 포함하고, 아직 없는 derived target을 참조하는 FK는
다음 prefix로 지연한다. Raw CreateModel의 columnless 선언은 필요한 endpoint/through가 준비된 경우에 지원한다.

자동 intermediary의 물리 변경은 하나의 logical operation에서 유도한다. 외부 입력으로 별도의 storage 원본을 받거나 합성 CreateModel을
공개 이력에 추가하지 않는다. Sealed graph는 정확한 파생 모델과 target·명시적 alias의 authority를 요구하며, 사전 resource budget은
파생 이름과 전체 fan-out도 제한한다. 생성 전 부재·제거 뒤 부재·이름 변경의 두 경계를 포함해 step 중간에만 존재하는 table도 검증한다.
같은 CreateModel에 포함된 automatic model끼리의 FK는 의존성 순서로 생성하고 역순으로 제거한다. Owner의 저장 FK가 자신의
intermediary를 다시 참조하는 생성 순환은 모델 생성 후 AddField로 작성한다. 자동 계획은 이 FK를 준비된 prefix까지 지연한다.

양 DB의 `AutomaticManyToMany` capability는 retained binding에도 필요하다. DDL 전체·최종 catalog·revision/history를 같은 transaction이
소유하고 cursor는 logical operation당 한 번 진행한다. Rename은 drop/create나 row copy를 사용하지 않는다. SQLite native table rename은
PK·rootpage·sqlite_sequence 부재/값을 유지하고 pair index는 새 managed 이름으로 교체한다. PostgreSQL은 table·identity sequence와
PK/FK/unique constraint 이름을 바꾸며 기존 table/sequence OID·last_value/is_called를 유지한다. Go 이름만 바뀌면 DDL은 없다.
관리 밖의 inbound FK나 view를 rename에 따라 조용히 retarget하지 않는다. Logical alias/FK가 이전 derived identity를 참조하면 변경 전에
해제해야 한다. 명시적 through 선언은 자동 table을 참조하더라도 해당 table을 소유하지 않는다.

Remove는 소유 link table만 삭제하고 두 endpoint의 행을 보존한다. Reverse Remove나 재적용은 빈 intermediary를 생성하며,
이미 삭제한 링크를 복구했다고 게시하지 않는다. 실제 migration과 forward SQL projection은 같은 완전한 DDL group을 사용한다.
늦은 제약 오류·취소·이름 충돌과 catalog drift에서 부분 이력이나 변경 상태를 게시하지 않고 기존 rollback/unknown outcome 계약을 유지한다.

## 후속 구현 경계

Root collection manager와 통합 facade·transaction composition·전체 query/consumer 지원은 다른 단계다.
실패 시 collection cache 무효화와 성공 publication은 위의 소유권을 따른다.
독립 materialization·평가한 QuerySet·Fresh의 소유권을 공유 cache로 합치지 않는다. Signal callback은 이후 rollback되는 변경도
관찰할 수 있으므로 durable commit 영수증으로 취급하지 않는다. 미구현 signal 범위는 카탈로그에 남긴다.
Ticket의 컬렉션 입력은 권한·CSRF를 body/DB 전에 검사하고 저장 transaction에서 양쪽 Category와 전체 원하는 집합을 다시 확인한다.
실행 source와 환경, 아직 구현하지 않은 선언·생성·migration·query·소비자는 [CURRENT](../status/CURRENT.md)와
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 구분한다.
