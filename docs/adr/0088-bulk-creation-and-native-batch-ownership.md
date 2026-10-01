# ADR-0088: bulk 생성과 native batch 소유권

- 상태: Accepted — 다중 행 AST/backend·ORM/생성 API 의미 채택, 환경별 검증·기준 대조·업무 소비 진행 중
- 날짜: 2026-10-02
- 구현: [GDJ-0109](../../work/0109-bulk-creation-and-ticket-import.md)

## 책임

다중 생성은 Schema IR에서 준비한 모델 입력을 같은 필드 순서의 불변 값 행렬로 변환한다. Backend가 native 다중 행
INSERT·식별자·scalar 인코딩·parameter 한도와 반환 rowset을 소유한다. Generic ORM은 전체 입력·모델 metadata와
고유 target의 정합성, batch 분할·같은 transaction/savepoint·결과 게시를 소유한다. 생성 표면은 모델 타입을 유지한다.

한 `BulkInsertPlan`은 한 native statement다. 행과 값은 복사하며 같은 필드 순서를 갖는다. 명시적인 primary-key 열은
선언한 key와 정확히 일치하고 자동 key를 받을 행은 해당 열을 생략한다. 전체 입력에 두 형태가 있으면 전체 scope의
owner가 각각의 native statement로 나누고 최종 입력 순서를 복원한다. 기존 caller 모델을 중간 결과로 수정하지 않는다.
명시적인 key의 native sequence 영향은 backend 의미를 따른다. PostgreSQL sequence를 임의로 재설정하지 않는다.

Backend는 양수 row/parameter 한도를 제공한다. ORM은 요청 batch 크기와 실제 한도 중 작은 값으로 나누며, 총 작업의
transaction을 batch마다 별도로 commit하지 않는다. AST의 한 statement는 65,535행·65,535 scalar 값까지다.
PostgreSQL protocol은 65,535 parameters, 고정 modernc SQLite profile은 32,766 parameters를 사용한다.
Auto-key-only 모델은 여러 default key의 native INSERT로 표현한다. 입력 크기·배치 구성·policy가 유효하지 않으면 명시적으로 실패한다.

## Go 입력과 생성 API

`Manager.BulkCreate`는 이미 구성한 모델 값 slice를 받아 `Save`와 같이 default나 model clean을 다시 적용하지 않는다.
명시 key의 존재 상태를 사용하므로 0·음수·큰 int64 key도 자동 key와 구분한다. 명시 key 입력을 먼저 저장하되
반환 객체의 순서는 원 입력 순서다. `BulkCreateInputs`는 생성 builder의 필수 값·default·nullable 결정을 한 번씩
평가한다. 모든 입력의 준비를 첫 INSERT 전에 끝내며, builder가 반환한 모델을 clone하고 자동 key만 요청한다.
`CreateInputs`는 구체적인 생성 builder slice를 모델 타입을 유지한 채 runtime 입력 slice로 변환한다.

Manager와 root QuerySet, 생성 project의 root query가 같은 runtime을 사용한다. 생성 facade는 구체적인 raw 모델 또는
그 모델의 create builder slice를 받고 원 backend에 결합된 독립 객체를 반환한다. 읽기 filter/order/slice/lock은
명시한 생성 입력에 복사하지 않는다. 생성 객체는 필요한 관계를 나중에 조회하며 기존 query cache를 채우거나 무효화하지 않는다.
입력이 비어도 context·설정·scope·정책·capability를 검증하고 실제 transaction이나 INSERT는 열지 않는다.

`BulkBatchSize`는 양수 상한이며 중복 지정할 수 없다. `BulkIgnoreConflicts`와 `BulkUpdateConflicts` 또는 동적
`BulkUpdateConflictNames`는 서로 배타적이다. 생성 필드의 모델 타입을 유지하고 primary key는 conflict target으로만
허용한다. 동적 입력도 같은 frozen Schema IR에 결합하며 고유 target과 갱신할 열을 동일하게 검증한다.

## 충돌과 반환 값

기본 정책은 native 제약 오류를 전파한다. Ignore는 uniqueness 충돌만 생략하고 CHECK·NOT NULL·FK·운영 오류를
숨기지 않는다. SQLite의 `INSERT OR IGNORE`는 사용하지 않는다. 고정 Django SQLite의 넓은 ignore 관찰과 구분되는
Go 정책이며 기존 relation conflict insert의 무결성 경계와 일치한다.

Update conflict는 명시적인 unique target과 갱신할 non-primary 필드를 요구한다. ORM은 target이 실제 모델의
고유성 선언인지 확인한다. Caller가 지정하지 않은 필드나 primary key는 갱신하지 않는다. Native duplicate-key 의미를
유지하고 자동 중복 제거·재시도를 하지 않는다. 같은 statement의 중복 upsert key는 PostgreSQL에서 오류이고 SQLite에서는
순서대로 적용될 수 있다. Batch 경계가 이 결과를 바꿀 수 있으므로 명시적인 API 정책과 테스트에 남긴다.

기본/update INSERT는 현재 지원하는 native profile의 RETURNING 순서로 입력별 key를 받는다. Rowset을 끝까지 읽고
close와 context를 확인한 뒤 반환한다. 누락·초과·잘못된 key·scan/rows/close 오류이면 key 일부를 게시하지 않는다.
Ignore는 생략한 행과 원 입력의 위치를 결합할 수 없으므로 key를 추측하지 않고 실제 affected count만 제공한다.
Affected count는 create와 update를 구분하는 신호가 아니다. Trigger가 생략한 행도 저장됐다고 가정하지 않는다.

`BulkCreateResult`의 `Objects`는 독립적인 입력 값과 확인된 반환 key를 가진다. Update conflict에서 update mask 밖의
입력 값은 현재 DB의 값으로 자동 새로고침하지 않는다. `ReturnedKeys=false`인 ignore 결과에서는 자동 key를 붙이지
않고 원래의 명시 key도 저장 성공의 증거로 해석하지 않는다. 전체 입력의 성공/실패 위치를 추측하지 않는다.

Native DB의 한 statement 결과와 전체 operation의 확정을 구분한다. 모든 batch와 반환 값 준비가 성공한 뒤에만
owned transaction의 결과를 게시한다. Borrowed savepoint의 결과는 부모 commit 전까지 provisional이며, 확인되지 않은
rollback·commit·정리 실패를 retry나 성공으로 바꾸지 않는다. 기본 query의 warm cache는 변경하지 않는다.

## 수명과 업무 소비

실제 root·ordinary/coordinated/relation session과 pinned root cursor에 capability를 연결한다. Read-only snapshot은
bulk 쓰기를 제공하지 않는다. 종료된 scope·child 실행 중 parent·잘못된 context는 limits 조회에도 거부한다.
Native RETURNING rowset은 scope가 끝나기 전에 항상 닫으며, root cursor에서는 같은 대여 연결을 사용한다.

Bulk는 일반 model save/clean callback과 감사 이벤트를 암묵적으로 반복 실행하는 API가 아니다. Application은
현재 권한·Category/관계 범위와 audit·publishable output의 정책을 명시한다. Helpdesk 여러 티켓 생성에서 그 정책과
같은 transaction을 공유하는 소비자를 완성한다. Public Go API는 typed 생성 소비자와 이 흐름에서 확정한다.

## 기준과 검증

Django 6.1의 독립 새 프로세스 관찰을 기준으로 입력 순서·default/관계·반환 key·batch/충돌·부모/실패 의미를 비교한다.
고정 upstream 출처와 명시적 차이를 보존한다. Native 기준 조사는 Go 구현이나 환경별 성공의 증거가 아니다.
제품 AST/backend·실제 양 DB·generic ORM/생성 module·업무 소비와 실패 경계를 함께 검증하고 실행 범위는
[TEST_EVIDENCE](../status/TEST_EVIDENCE.md)에만 기록한다. Bulk update와 writable expression은 후속 기반이며 이 작업의 완료로 간주하지 않는다.
