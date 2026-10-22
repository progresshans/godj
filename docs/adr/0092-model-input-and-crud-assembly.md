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

## 애플리케이션의 차이

Article는 대상 조회 후 body를 읽고, Helpdesk Label/Ticket는 body 검증 뒤 write scope에서 현재 대상을 확인한다.
공통화는 이 순서를 바꾸지 않는다. Article publish와 Label ensure는 업무 명령으로 남으며 공통 transaction 결과
경계와 현재 조회·출력 준비를 사용한다.

Helpdesk Category는 기존처럼 읽기 전용이다. Ticket의 numeric no-op, 폼 JSON 동등성, JSON API exact-token 정책은
명시적 candidate 준비 정책이다. bulk는 전 후보 검증과 native batch·일관된 scope·동일 transaction을 계속 소유한다.
파일 upload를 일반 scalar 입력으로 바꾸거나 관계·inline·audit 실패를 부분 성공으로 처리하지 않는다.

## 검증 경계

설계 채택과 제품 실행 PASS는 구분한다. 현재 로컬에서는 포맷·최소 compile만 확인하며 새 동작의 실행 검증은
아직 완료하지 않았다. 외부 생성 소비자, 동일 필드·권한·출력 변경 실험, 실제 HTTP/Admin 및 native DB·race·전체
platform 검증의 source와 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에 기록한다.
