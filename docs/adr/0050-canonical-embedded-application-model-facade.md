# ADR-0050: Canonical Embedded Application Model Facade

- 상태: Accepted
- 날짜: 2026-08-27
- 관련 work/contract: [GDJ-0048](../../work/0048-canonical-application-model-facade-and-current-generated-abi.md), GEN-M1-001, REL-002, Q-013, Q-017
- 선행 결정: [ADR-0033](0033-forward-foreign-key-assignment-save-and-cache-ownership.md), [ADR-0035](0035-pre-release-current-only-format-and-generated-publication.md), [ADR-0036](0036-project-schema-generated-bundle-and-recoverable-publication.md), [ADR-0038](0038-minimal-web-core-request-lifetime-and-representation.md)
- 대체하는 ADR: 없음

## 맥락

Current project facade는 app raw model을 private named field로 보관합니다. Query/relation/write lifecycle은 안전하지만 application
code가 ordinary scalar를 읽거나 바꾸고 app-owned model method를 호출하려면 매번 detached `Unwrap` clone을 거쳐야 합니다.
Django의 model 중심 경험을 그대로 포팅할 필요는 없지만 scalar, user method, relation과 explicit Go I/O가 하나의 logical model
surface에서 만나는 current API를 정해야 broader facade/API 작업이 같은 우회를 반복하지 않습니다.

Q-017에서 요구한 external compile comparison은 세 선택을 좁혔습니다. Explicit unwrap-only는 state safety가 강하지만 scalar/user
method와 write wrapper를 분리합니다. Global pointer-map sidecar는 raw type을 유지하지만 pointer lifetime, copy, cleanup과 concurrency
owner를 새로 만듭니다. Private alias embedding은 Go promotion으로 scalar/user method를 제공하면서 existing project-owned relation
state를 유지할 수 있지만 namespace shadowing, direct FK mutation, copy와 serialization을 명시적으로 닫아야 합니다.

## 결정 기준

- 하나의 application-facing type에서 scalar/user method/query/relation/write가 함께 보일 것
- DB I/O는 계속 explicit `context.Context`/`error` 경계에만 있을 것
- Existing REL-002 assignment/save/presence/cache behavior를 회귀시키지 않을 것
- Generated/user namespace collision이 조용한 method shadowing이 되지 않을 것
- Wrapper copy와 implicit JSON/template representation이 hidden state를 우회하지 않을 것
- Current whole-project bundle과 recoverable publication을 재사용할 것
- Schema IR/Migration/Backend 범위로 전파하지 않을 것

## 고려한 선택지

### Named raw field + explicit Unwrap only

현재 safety를 그대로 보존하지만 ordinary application method가 detached raw clone과 stateful wrapper 사이를 오가야 하고 raw clone
수정은 원래 wrapper Save에 반영되지 않습니다. Canonical model UX로 채택하지 않습니다. `Unwrap` deep clone escape hatch는 유지합니다.

### Private alias embedding + project-owned relation state

App raw type의 exported field와 method를 promotion하고 relation origin/object/cache/presence/self state는 outer wrapper가 계속 소유합니다.
Go type system과 existing package dependency를 보존하지만 namespace audit과 lazy reconciliation이 필요합니다. 채택합니다.

### Raw pointer + external sidecar/manager

Raw type identity와 its JSON method set을 그대로 유지하지만 state lookup, pointer identity, copy, reap, locking과 manager-style Save/relation
API를 새로 만들며 state lifetime을 GC와 분리합니다. 채택하지 않습니다.

## 결정

1. Project wrapper는 app raw model의 unexported type alias를 anonymous value field로 embed합니다. Public canonical type 이름은 existing
   app alias + model 이름을 유지합니다.
2. Exported scalar field와 app-owned ordinary method는 Go promotion으로 접근합니다. Relation state, origin, object/cache, presence와
   copy-detection sentinel은 outer wrapper의 private field로 남습니다.
3. Existing `Save`, `With*`, `Clear*`, forward relation accessor와 `Unwrap` exact signatures는 유지합니다. `Unwrap`은 current raw model의
   detached deep clone이며 pending relation/required presence와 receiver-copy validation을 우회하지 않습니다.
4. Framework-owned method names와 Schema-derived promoted fields는 reserved namespace입니다. App raw model에 선언된 exported production
   method가 같은 이름을 가지면 outer method가 compile-success로 조용히 가리더라도 generation은 fail-closed합니다.
5. Pure `GenerateProject` validation은 schema에서 유도 가능한 promoted field/relation/generated selector를 검사합니다. Sealed-root
   source audit은 declared app directory의 regular non-test `.go` source를 path lexical order로 bounded AST parse해 exact raw-model
   receiver method set과 framework reserved method를 비교합니다. Exact current/prior manifest-owned GoDj source는 제외하고 unowned
   `zz_godj_*.go`, symlink/non-regular/path escape는 existing project check와 함께 거부합니다. 모든 production build-tag variant를
   보수적으로 검사하되 Go toolchain이 무조건 제외하는 `.`/`_` prefix basename은 제외합니다. 첫 구현 cap은 directory entry
   65,536개/path bytes 16 MiB, source 4,096개, file당 1 MiB, aggregate 64 MiB입니다. 감사 대상 raw-model 이름과 reserved surface는
   sealed candidate facade/schema plan에서 exact하게 유도하며 manifest만 보거나 app package의 모든 type declaration을 generated
   model로 추정하지 않습니다.
6. `generate --check`와 write path 모두 manifest/byte clean 판정 전에 같은 sealed-root source audit을 실행해 handwritten method만
   바뀐 경우도 false-clean 없이 target mutation 전에 거부합니다. Write path는 candidate compile 뒤 첫 generated target mutation
   직전에 같은 source fingerprint를 다시 검증합니다. 이전 interrupted publication의 mandatory recovery는 audit보다 먼저 prior 또는
   committed-next exact state를 복원할 수 있습니다. 그 recovery가 끝난 뒤 cancellation/resource/parser/source-change failure는 새
   candidate target mutation 0이며, recovery와 새 candidate publication은 서로 다른 소유 경계입니다.
7. Project wrapper에 직접 선언한 duplicate receiver method와 package-level symbol collision은 whole-candidate compile이 소유합니다.
   Raw embedded receiver의 promotion shadowing은 Go에서 compile error가 아니므로 source audit이 소유합니다. 자동 rename/mangling,
   build result에 따른 플랫폼별 다른 surface와 compatibility shim은 만들지 않습니다.
8. Wrapper는 relation source scalar snapshot을 보존합니다. Relation accessor, `With*`/`Clear*`, `Unwrap`과 `Save` 진입 전
   current embedded FK tuple과 snapshot을 canonical order로 수집하고 전체 validation/rebuild를 staging합니다. 전부 성공한 뒤에만
   changed edge의 object/cache/pending state를 게시하며 실패하면 wrapper state unchanged/DB I/O 0입니다. Unchanged edge cache는 보존합니다.
9. Pending target 뒤 caller direct scalar mutation은 scalar가 이기고 pending cache를 폐기합니다. Nullable key는 pointer identity가 아닌
   `(present, value)`로 비교합니다. Required new raw zero는 unset이고 `With*ID(0)`와 loaded zero만 explicit presence라는 기존
   contract를 유지합니다.
10. Wrapper는 construction과 successful Save 뒤 primary-key `(present, value)` snapshot을 보존합니다. Promoted PK를 직접 바꾼
   뒤 stateful operation을 호출하면 `model_state_error/primary_key_update_field`로 I/O 전에 거부합니다. Manual PK는 app raw
   `New<Model>WithID`를 wrapper `New` 전에 사용합니다. Ordinary non-PK scalar만 direct mutable surface입니다.
11. Nil/zero/dereference-copy wrapper의 stateful operation은 pre-I/O error입니다. Promoted ordinary field/method access는 Go value semantics를
   따르며 wrapper 동시 mutation에 framework-level synchronization을 추가하지 않습니다.
12. Wrapper는 direct JSON marshal/unmarshal을 명시적으로 거부합니다. Non-nil wrapper value/pointer marshal과 pointer unmarshal은
   deterministic fixed error이며 failed unmarshal receiver는 unchanged입니다. `encoding/json`이 nil pointer를 method 호출 없이
   `null`로 처리하는 standard-library special case는 no-I/O harmless exception입니다. Embedded raw fields 또는 raw custom
   `MarshalJSON`이 hidden relation/presence state를 우회해 자동 게시되지 않습니다. `Unwrap`은 DTO 구축용 raw typed/deep-clone
   escape이고 ADR-0038대로 app-owned DTO만 supported Web JSON/template representation authority입니다. Go `html/template`가
   exported promoted fields를 볼 수 있다는 사실은 숨기지 않으며 wrapper를 직접 template data로 전달하는 사용은
   unsupported/noncontractual이지 type-level rejection 보장이 아닙니다.
13. Facade renderer version은 current v3로 승격하되 app 4/project 8/bundle-seal 1의 13-role roster와 manifest format 1을 유지합니다.
    Snapshot preimage 변화로 모든 generated output marker와 manifest를 한 current bundle로 재생성하고 ADR-0036의 read-old/write-current,
    candidate compile, journal recovery와 manifest-last publication을 재사용합니다.

## 결과

- Application code는 wrapper에서 scalar, user method, relation과 Save를 함께 사용합니다.
- Relation cache safety를 raw model 내부나 global registry로 옮기지 않습니다.
- Handwritten method를 추가하면 generation이 새 namespace 충돌을 발견할 수 있으므로 generator가 project source를 읽는 bounded 책임이
  늘어납니다.
- Direct field mutation은 compile-time dirty tracking이 아니므로 operational boundary의 deterministic reconciliation 비용이 생깁니다.
- `Unwrap`은 compatibility shim이 아니라 raw typed API/DTO를 위한 explicit deep-clone escape hatch로 남습니다.
- Existing generated bytes는 first-alpha 전 current ABI v3로 한 번 재기준화되며 older bytes를 쓰는 별도 facade reader는 만들지 않습니다.

## 의도적으로 결정하지 않은 것

- Reverse/general relation manager, identity map, application-global cache invalidation과 thread-safe mutable model
- Supported wrapper direct JSON/template format, reflection serializer와 implicit lazy loading
- Raw user method가 framework reserved name을 의도적으로 override하는 extension mechanism
- Build-tag별 다른 public method surface와 plugin-provided receiver methods
- 새 Django/DRF conformance manifest/adapter와 current global aggregate 변경
- New Field/Relation/Schema IR, migrations, backend capability와 database router
- Renderer rename/deprecation, installed generator negotiation와 first-alpha 이후 upgrader/semver policy
- Final source-fingerprint 확인 뒤 non-generated app source를 동시에 바꾸는 비협조적 editor/writer; caller가 serialize해야 함

## 검증

- External compile: direct scalar read/write, value/pointer app method, existing Save/With*/Author/Unwrap 조합
- Namespace negative: field/relation/handwritten Save/Unwrap/Author/With* 충돌, build-tag source와 resource/symlink/source-change
  failure의 pre-publication target write 0
- Runtime: direct scalar Save와 reload, warm required/nullable cache 뒤 direct FK change, pending-target override, explicit zero,
  promoted PK mutation rejection과 manual-PK-before-New
- Copy/JSON: nil/zero/shallow-copy stateful failure, non-nil value/pointer marshal 및 pointer unmarshal rejection, failed unmarshal
  no-mutation, nil-pointer `null` special case, Unwrap raw/DTO no-I/O representation
- Bundle: facade v3 deterministic exact 12/16 fixtures, current manifest, mixed generation compile-success 0, failure/recovery old-or-new exact
- SQLite/PostgreSQL Article/strong-relation vertical flow와 affected normal/race/CGO0/vet
- Frozen milestone에서 full local/386/repository-external and exact-head hosted matrix 한 번
