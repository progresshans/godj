# Migration lifecycle revision-fence feasibility spike

이 디렉터리는 GDJ-0017의 SQLite 동시 실행 가설을 검증하는 **test-only conformance
package**입니다. 하위 external-fake compile gate를 포함한 모든 Go source는 `_test.go`이고,
`migrations/**`, `db/**` 제품 source나
공개 API를 구현하지 않습니다. 여기의 table 이름, column encoding, token 구조와 helper
이름도 제품 schema/API로 고정된 것이 아닙니다.

## 검증하는 가설

한 시점의 sorted recorder identities, persistent epoch, monotonic revision, identities의
SHA-256 fingerprint를 같은 read transaction에서 읽습니다. 이후 각 migration step은 별도
SQLite transaction에서 다음 순서를 지킵니다.

1. pinned connection에서 `BEGIN IMMEDIATE`로 writer reservation을 얻습니다.
2. 현재 recorder fingerprint와 persistent metadata를 expected snapshot과 대조합니다.
3. conditional metadata update로 successor revision과 예상 successor fingerprint를 먼저
   claim합니다.
4. 그 뒤에만 해당 step의 domain DDL과 recorder mutation을 수행합니다.
5. transaction 안에서 최종 recorder fingerprint를 다시 확인하고 metadata, DDL, recorder를
   함께 commit합니다.

따라서 모든 migration writer가 이 경계를 사용하면 stale snapshot은 첫 domain
DDL/recorder write 전에 거부되고, 성공한 한 step과 successor token은 원자적으로 결속됩니다.
Plan 전체를 감싸는 outer transaction은 없으므로 이전 step의 durable commit과 last-durable
state 의미는 유지됩니다. Conflict는 자동 retry하지 않습니다.

## token 선택 근거

- Persistent epoch + monotonic revision이 cooperating writer 사이 freshness의 주 fence입니다.
  Apply 뒤 unapply하면 recorder set이 원래 값으로 돌아오는 ABA가 있으므로 history
  fingerprint만으로는 충분하지 않습니다.
- Missing recorder table과 empty recorder table은 현재 history 의미상 같은 empty set으로
  canonicalize되며 fingerprint도 같습니다.
- SHA-256 history fingerprint는 revision을 대체하지 않습니다. Snapshot identities와 token의
  결속을 확인하고, metadata revision을 갱신하지 않은 direct recorder write의 non-ABA drift를
  fail-closed하는 보조 integrity gate입니다.
- Cutover 전에 revision protocol에 참여하지 않은 writer가 apply→unapply ABA를 끝내면 이를
  알아낼 durable generation이 없으므로 감지할 수 없습니다. Legacy adoption은 exclusive
  cutover가 필요하며 이 spike는 그 범위를 보장하지 않습니다.

## SQLite 경계와 결과 분류

`BEGIN IMMEDIATE`와 conditional update는 같은 transaction/pinned connection에서 실행됩니다.
Writer reservation 뒤에는 validation과 첫 domain write 사이에 다른 SQLite writer가 commit할
수 없습니다. 같은 token의 contender는 최대 하나만 claim하고 commit합니다.

- Token mismatch는 `stale`입니다. 현재 step의 domain DDL/recorder mutation은 0입니다.
- `SQLITE_BUSY`/`SQLITE_LOCKED`는 lock contention입니다. Begin, metadata/history read,
  domain DDL, recorder, final verification와 commit 모두 하나의 normalizer를 사용해 stale 또는
  integrity error로 새지 않습니다. BUSY/LOCKED를 Stale로 위장하지 않고 별도 결과로
  유지합니다. SQLite의 bounded busy timeout은 기다릴 수 있지만
  lifecycle의 semantic re-read/replan/step retry는 없습니다.
- Metadata/history fingerprint 불일치는 non-cooperating drift 또는 손상으로 분류하고
  fail-closed합니다.
- Fence capability가 없는 optional port는 unsupported로 거부하며 legacy begin/write로
  fallback하지 않습니다. Existing product interface를 이 spike가 변경하지 않습니다.

Uninitialized/legacy database는 immediate transaction 안에서 현재 canonical history를 stale
snapshot과 먼저 비교합니다. 일치한 경우에만 candidate metadata를 만들고 첫 fenced step과
함께 commit합니다. 불일치하면 metadata 생성까지 rollback되어 stale attempt의 domain DDL과
recorder mutation은 0입니다. 이 bootstrap encoding은 feasibility 후보일 뿐 제품 migration
format이 아닙니다.

## Source dependency

`current_gap_test.go`는 현재 `LoadAppliedState → Reconstruct → Plan → ExecutePlan` 경계를 실제
제품 package로 조립해 first-step 전과 step 사이의 stale acceptance gap을 증명합니다.
`coordinator_test.go`는 actual immutable `ProjectState`를 test-only candidate coordinator의
last-durable 반환값으로 사용합니다. Fence 알고리즘과 fault/concurrency harness 자체는 제품
lifecycle interface에 의존하지 않고 `database/sql`과 SQLite driver만 사용합니다.
`externalfake/`는 기존 public reader/backend만 구현하는 별도 package fake가 그대로 compile됨을
검증합니다. Candidate optional port가 없으면 coordinator는 이 legacy port의 read/begin을 한 번도
호출하지 않고 unsupported로 끝납니다.

## 실행

Repository root에서 다음을 실행합니다.

```sh
go test -count=1 -v ./conformance/lifecyclefence/...
go test -race -count=1 ./conformance/lifecyclefence/...
go test -count=20 ./conformance/lifecyclefence/...
```

검증 범위는 two connections, two OS processes, stale-before-first-write, competing commit
between steps, tail-stop과 actual last-durable `ProjectState`, same-token single winner,
busy/stale 분리와 exact one step attempt, metadata claim/DDL/recorder/final-fingerprint fault
rollback과 later success, uninitialized adoption/contender, absent/empty/ABA, unsupported
fail-closed입니다. Live schema를
recorder revision 밖에서 직접 바꾸는 drift, non-cooperating ABA, fairness, lease, crash repair,
distributed lock은 보장하지 않습니다.

상세 state machine과 제품 승격 시 남는 위험은 [DESIGN.md](DESIGN.md)에 기록합니다.
