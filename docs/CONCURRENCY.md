# 동시성, 취소와 실패 소유권

성능은 실제 workload로 측정한다. Go나 goroutine을 사용한다는 사실만으로 처리량·안전성을 주장하지 않는다.
모든 I/O는 context와 error를 전달하고, pool·transaction·cache·process의 소유자를 구분한다.

## QuerySet 평가

Query plan은 불변이고 평가 cache는 별도 state다. Direct QuerySet value copy는 같은 평가 state를 공유할 수 있지만
Filter/OrderBy/Limit/Fresh에서 만들어진 query는 독립 평가 state를 갖는다. 같은 state의 concurrent All은 한 owner가
평가하고 waiter는 자신의 context로 기다린다. Waiter cancellation이 owner를 취소하지 않는다.

성공한 전체 결과만 cache한다. Empty 결과도 성공이며 caller에게는 descriptor clone으로 분리한 model을 돌려준다.
Backend/scan/rows/close/context 실패는 성공 cache로 남기지 않는다. Cold Count/Exists/At/First는 full cache를 채우지 않으며,
warm terminal은 cache를 사용할 수 있다. Iterate는 full result cache를 사용하거나 채우지 않는다.
Nil/already-canceled context는 warm cache와 nullable no-I/O 경로에서도 확인한다.
Descriptor·callback panic도 열린 rows를 정리하고 평가 flight를 해제해 waiter가 다시 시도할 수 있게 한다.
Panic은 그대로 전파하며 partial result를 cache하지 않는다. 정상 반환의 rows·close·context 오류 결합은 유지한다.
Limit/Offset/Distinct 파생은 불변 plan metadata를 공유하고, 외부로 반환하는 mutable slice는 분리한다.

이 경계의 이유는 [ADR-0012](adr/0012-queryset-evaluation-cache-ownership.md)에 있다. QuerySet 자체의 안전성을 backend
session·사용자 callback·반환된 mutable model의 임의 공유 안전성으로 확대하지 않는다.

## 관계 객체

- 같은 relation owner의 cache와 새 materialization/Fresh의 cache를 구분한다. 전역 identity map을 가정하지 않는다.
- Lazy required 관계의 missing/cardinality와 nullable absent는 서로 다른 결과다.
- Prefetch/eager All은 전체 scan·row close·cancel·cardinality 검증이 끝난 뒤 한 번에 결과를 게시한다. 실패 시 partial cache를 남기지 않는다.
- Eager First는 cold query에서 최대 한 row를 읽고 All cache를 채우지 않는다. Warm All cache의 첫 결과도 독립 복제하며,
  관계 검증과 row close가 끝나기 전에는 결과를 반환하지 않는다. 기존 QuerySet.First처럼 명시적 정렬을 요구한다.
- Relation assignment는 FK 값과 cache를 함께 reconcile한다. Application memory를 DB rollback으로 자동 되돌리지 않는다.
- 다른 query materialization에서 얻은 동일 PK의 객체가 같은 pointer일 필요는 없다.
- Transaction에서 만든 query의 사용 가능 범위는 transaction/session 계약을 따른다. Warm cache가 session 이후에도 값을 제공할 수 있다는 사실을
  session I/O가 계속 유효하다는 뜻으로 해석하지 않는다.

상세 이유: [forward cache](adr/0026-forward-foreign-key-object-cache-and-nullability.md),
[prefetch](adr/0028-reverse-foreign-key-prefetch.md), [eager](adr/0029-one-hop-forward-select-related.md),
[assignment](adr/0033-forward-foreign-key-assignment-save-and-cache-ownership.md).

## Migration의 durable 상태

Recorder identities와 opaque revision은 같은 DB snapshot에서 읽는다. Each-step transaction은 첫 DDL/recorder mutation 전에
expected revision을 검증한다. 성공한 schema·recorder·successor revision을 원자적으로 commit하고, 다음 step은 그 committed
successor만 사용한다. 다른 writer가 끼어들면 현재 step은 stale로 실패하고 앞선 commit은 durable하게 남는다.

Fingerprint는 history binding을 검증하지만 apply→unapply로 집합이 같아지는 ABA를 구별하지 못한다. Persistent generation과
revision이 협력 writer의 freshness를 소유한다. `BUSY`/`LOCKED`는 stale revision이 아니라 contention이다.
Automatic re-read/replan/retry는 caller의 의도를 바꿀 수 있으므로 caller가 명시적으로 새 lifecycle을 시작한다.

Legacy recorder의 최초 adoption은 old writer를 중지한 cutover가 필요하다. 비협력 writer의 완료된 ABA, recorder 밖에서 직접
수정한 schema, DB copy/restore의 운영 정책은 fence만으로 해결되지 않는다. SQLite PRAGMA의 data/schema/user version을
이 durable revision으로 대신 사용하지 않는다.

성공, 확정 rollback, outcome unknown을 구분한다. Commit error만으로 rollback을 주장하거나 자동 retry하지 않는다.
Caller에게 마지막 confirmed state를 돌려주더라도 DB가 그 state라고 확정한 것은 아닐 수 있다.
[ADR-0017](adr/0017-revision-fenced-migration-lifecycle.md)과 [ADR-0018](adr/0018-revision-fenced-migration-lifecycle-product-shape.md)에
이 선택의 이유가 있다.

## SQLite raw transaction과 quarantine

`database/sql`이 직접 추적하지 않는 raw BEGIN은 pinned connection에서 완료 여부를 확인해야 한다.
Cleanup은 취소된 caller context와 분리된 bounded context로 시도한다. Confirmed rollback은 release할 수 있고,
confirmed physical discard는 pool 재사용을 막는다. `Conn.Raw`가 nil을 반환했다는 사실만으로 discard를 확인했다고 하지 않는다.

Rollback과 discard를 모두 확인할 수 없으면 connection을 pool로 돌려주지 않는다. 같은 Backend의 retention-producing
`AtomicRelation`/`CoordinatedAtomic`은 하나의 context-aware admission을 공유하며, 첫 retain은 terminal quarantine을 게시한다.
Retained physical connection은 최대 하나이며 이후 새 DB I/O는 connection acquire/callback/SQL 전에
`backend_error/backend_recovery_required`로 실패한다. 이미 admission된 작업을 소급 취소하지 않는다.

Explicit Backend.Close는 새 admission을 닫고 pool을 먼저 seal한 뒤 보관 handle을 drain한다. 늦게 retain한 이미-admitted
operation은 sealed pool의 handle을 스스로 닫는다. Error path에서 context 없는 implicit pool Close를 호출하지 않는다.
Concurrent Close loser는 winner의 전체 drain 완료를 기다리는 barrier가 아니다.

Callback panic은 cleanup 뒤 같은 panic value로 다시 발생시킨다. Unknown outcome cause를 보존하되 이것이 자동 retry나
현재 DB state를 추측할 권한이 되지는 않는다. 같은 SQLite 파일의 다른 Backend/process가 자동 quarantine되는 것도 아니다.
설계 이유는 [ADR-0057](adr/0057-sqlite-retained-connection-terminal-quarantine.md), 검증된 환경은 [Evidence](status/TEST_EVIDENCE.md)에서
각각 확인한다.

## 프로세스와 서비스

CLI child의 input/output pipe, process group, signal과 reap은 실행자가 소유한다. Timeout이나 caller cancellation 뒤에도
child가 실행 중이거나 pipe writer가 남아 있는데 성공으로 반환하지 않는다. 큰 출력은 bounded protocol과 제한된 진단 buffer로
처리하고 원래 exit status와 실패 종류를 보존한다. Test fixture가 외부 process를 쓸 때도 같은 수명 규칙을 적용한다.

Web request의 context를 background job의 영구 상태로 보관하지 않는다. Server shutdown은 listener를 닫고 정해진 범위에서
in-flight request를 drain한다. Streaming이나 장시간 작업을 추가할 때는 backpressure와 취소 소유권을 별도로 정한다.

System-state coordination은 credential/session/audit와 application mutation의 원자성이 필요한 경계를 묶는다.
같은 schema를 사용하는 협력 runtime은 같은 credential·CSRF policy를 사용해야 한다. Session rotation/logout/revocation 뒤의
거부와 application permission은 실제 DB·HTTP 흐름에서 검증한다. Process-local lock만으로 여러 runtime을 안전하다고 하지 않는다.

Operator 권한 변경은 `UpdateOperatorPermissions`에 expected current policy를 명시하여 요청한다.
Cooperative transaction에서 policy 비교·권한 변경·session 폐기를 함께 수행한다. Authenticate/Resolve는 현재 policy를 확인하므로
변경 전 runtime은 새 인증을 거부하고 새 policy로 다시 열어야 한다. 이미 허용된 in-flight 작업까지 소급 취소하지 않는다.
