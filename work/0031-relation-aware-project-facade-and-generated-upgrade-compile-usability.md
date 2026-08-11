---
id: GDJ-0031
status: completed
updated: 2026-08-12
baseline_branch: "codex/revision-fenced-migration-lifecycle"
baseline_commit: "ceff9e534e541edb0bd19cd6a1a61682b5435454"
depends_on: ["GDJ-0030"]
contracts: ["Q-013", "Q-017"]
allowed_paths:
  - "internal/compiletest/compile_test.go"
  - "internal/compiletest/testdata/relation_facade/**"
  - "docs/DEVELOPER_EXPERIENCE.md"
  - "docs/OPEN_QUESTIONS.md"
  - "docs/ROADMAP.md"
  - "docs/TESTING.md"
  - "docs/adr/0031-relation-aware-project-facade-and-generated-upgrade-boundary.md"
  - "docs/adr/README.md"
  - "docs/status/CURRENT.md"
  - "docs/status/IMPLEMENTATION_MATRIX.md"
  - "docs/status/TEST_EVIDENCE.md"
  - "work/0030-project-bound-protect-and-set-null-delete.md"
  - "work/0031-relation-aware-project-facade-and-generated-upgrade-compile-usability.md"
  - "work/README.md"
integration_owner: "one primary agent"
---

# Relation-aware Project Facade and Generated Upgrade Compile Usability

## 사용자에게 보이는 결과

GDJ-0031은 아직 제품 API를 생성하지 않습니다. 기존 relation-delete fixture를 바꾸지 않은 채 Go build overlay로
테스트 전용 project facade 후보 하나를 주입하고, 외부 package에서 다음 **read-only forward compile shape**가
성립하는지만 검증합니다.

```go
models, err := project.Using(backend)
if err != nil {
    return err
}

post, found, err := models.BlogPosts.
    OrderBy(blog.PostFields.ID.Asc()).
    First(ctx)
if err != nil {
    return err
}
if !found {
    return nil
}

raw, err := post.Model()
if err != nil {
    return err
}
_ = raw.ID
_ = raw.AuthorID

author, err := post.Author(ctx)
```

Eager 후보도 같은 source pointer와 accessor를 사용해야 합니다.

```go
posts, err := models.BlogPosts.
    SelectRelated(models.BlogPosts.Related.Author).
    OrderBy(blog.PostFields.ID.Asc()).
    All(ctx)
if err != nil {
    return err
}

author, err = posts[0].Author(ctx)
```

이 문법은 compile-usability 후보입니다. `Using`, `models`, `BlogPosts`, `Related`, selector, `First` tuple과
`Model` unwrap 이름이 공개 API로 채택됐다는 뜻이 아닙니다.

## 목표

- 현재 exact sixteen-file physical fixture를 byte-for-byte 보존합니다.
- project package에 존재하지 않는 Go file 하나만 build overlay로 가상 주입해 logical exact seventeen-file
  compile view를 만듭니다.
- 명시적 backend binding, exact `OrderBy(...).First(ctx)` 다중 대입, lazy/eager 동일 source pointer와
  `Author(ctx)`, scalar `Model()` unwrap 후보가 외부 package에서 컴파일되는지 검증합니다.
- `Filter`/`OrderBy`와 error-returning `Limit` 뒤에도 facade query wrapper를 잃지 않는 read-only 후보를
  compile gate로 비교합니다.
- `db.RelationSession`이 현재 interface embedding상 `db.Queryer`를 만족하므로 `AtomicRelation` callback 안에서
  query-only `project.Using(session)` 후보를 전달할 수 있는지만 컴파일로 확인합니다.
- 기존 저수준 `Bind`, `BindObjects`, factory `From`, select-related와 relation-delete surface를 최종 사용자 API로
  소급 동결하지 않고, future generated companion의 namespace/upgrade 질문을 분리합니다.

## 비목표

- production generator, generated Go source, ORM runtime 또는 backend를 구현하거나 변경하지 않습니다.
- reverse aggregate가 없는 exact sixteen fixture에서 `author.Posts`, reverse chaining 또는 prefetch를 만들지 않습니다.
- REL-002 FK assignment/cache invalidation, setter, write/delete facade, wrapper cache, copy/clone과 callback 이후
  session lifetime을 구현하거나 검증하지 않습니다.
- wrapper field는 private이므로 direct scalar promotion, wrapper JSON marshal 또는 wrapper custom model method를
  주장하지 않습니다. 이번 후보는 explicit `Model()` unwrap만 사용합니다.
- query count, lazy cache hit, eager SQL 0회, transaction pinning, cancellation, runtime error 또는 concurrency
  동작을 compile success에서 추론하지 않습니다.
- relation manifest, contract status, runner scenario, product inventory 또는 Django parity를 바꾸지 않습니다.
- public name, semver, deprecation window, generated upgrade command나 final facade capability interface를
  Accepted로 확정하지 않습니다.

## 선행 조건과 기준 상태

- Exact baseline은 `ceff9e534e541edb0bd19cd6a1a61682b5435454`입니다.
- [EVID-063](../docs/status/TEST_EVIDENCE.md#evid-20260812-063--gdj-0030-terminal-exact-head-ci-and-gdj-0031-activation-baseline)의
  hosted run `31516174741`은 이 baseline의 exact 26/26 jobs·326/326 recorded steps를 통과했습니다.
- Product는 exact `121 passing + 5 deviation + 1 oracle_locked`, relation 11/12이며 REL-002만 locked입니다.
- Relation manifest는 10,776 bytes/SHA-256
  `3dd02b5a0ba3512dac1697a5ba84261fe589ee49ee69ee77243fd5f1c64e8f46`입니다.
- `conformance/relationdeleteproduct/**` physical fixture는 다음 exact 16 files입니다.

```text
authors/zz_godj_generated.go
authors/zz_godj_relation.go
authors/zz_godj_relation_object.go
authors/zz_godj_relation_projection.go
blog/zz_godj_generated.go
blog/zz_godj_relation.go
blog/zz_godj_relation_object.go
blog/zz_godj_relation_projection.go
blog/zz_godj_relation_query.go
fixture/schema.go
observer.go
product_test.go
project/zz_godj_bindings.go
project/zz_godj_relation_delete.go
project/zz_godj_relation_object.go
project/zz_godj_relation_select_related.go
```

Sorted fixture-relative `path + NUL + decimal byte size + NUL + full content`은 exact 62,538 content bytes/
SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`입니다. 그중 `zz_godj_*.go`
13 files는 26,140 bytes/SHA-256
`a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`입니다.

## Django reference와 Go 경계

Django의 relation descriptor는 attribute 접근, cache와 app registry를 런타임에 결합합니다. GoDj는 그 내부 구조나
`post.author` 문법을 복제하지 않습니다. 잠재적 I/O는 `Author(ctx)`처럼 `context.Context`와 `error`가 보이는
method로 남기고 backend/session은 명시적으로 전달합니다.

이번 packet은 그 목표가 현재 Go type system과 generated package boundary에서 컴파일 가능한지 보는 test-only
spike입니다. Django oracle을 읽거나 새 relation behavior를 관찰하지 않습니다.

## 설계와 가설

### Physical exact 16과 logical exact 17

Physical fixture 16 files는 수정하거나 복사하지 않습니다. `internal/compiletest`가 temporary overlay JSON을 만들고,
`internal/compiletest/testdata/relation_facade/**`의 project-package source 하나를 실제로 존재하지 않는 project
`.go` path에 매핑합니다. Go command가 보는 logical view만 17 files가 됩니다.

이 17번째 file은 checked-in generated output도 generator golden도 아닙니다. Test 종료 뒤 physical target이
생기지 않아야 합니다. Existing legacy
`internal/compiletest/testdata/relation_delete/generated_external_consumer.go.txt`는 overlay 없이 계속 컴파일되어야
하고, 새 `internal/compiletest/testdata/relation_facade/**` candidate consumer만 overlay가 있을 때 컴파일되어야
합니다. 그 same candidate는 overlay가 없으면 undefined `project.Using`으로 실패해야 합니다.

### Forward read-only 후보만 검증

Overlay는 exact 16이 이미 가진 source object와 one-hop select-related kernel만 조립합니다. Positive external
consumer는 exact `post, found, err := ...OrderBy(...).First(ctx)`와 eager `All(ctx)`을 사용하고, 두 결과가 같은
source pointer type인지 정적으로 잠급니다. Private wrapper field를 우회하지 않고 `post.Model()`로 raw model을
명시적으로 얻습니다.

Exact 16에는 reverse object/prefetch aggregate가 없습니다. Metadata의 reverse name만으로 reverse manager를
만들거나 다른 conformance fixture를 import하는 것은 false green입니다.

### Session은 assignability만 검증

Candidate `Using`이 query-only `db.Queryer`를 받으면 `db.RelationSession`은 `Session → Queryer` embedding으로
전달 가능합니다. 이 compile gate는 callback 안에서 origin object를 명시적으로 건넬 수 있다는 뜻뿐입니다.
Session을 저장하거나 callback 뒤 사용하는 것이 안전하다는 뜻이 아니며, read/write/delete를 모두 포함할 future
facade가 어떤 capability를 받아야 하는지도 결정하지 않습니다.

### Source/AST whitelist

Overlay와 external consumer는 Go AST로 검사합니다. 허용된 project/authors/blog/core public imports와 이번
read-only candidate declaration/call만 사용할 수 있습니다. 다음은 거부합니다.

- 다른 `conformance/relation*product` fixture import
- oracle, static, not-implemented fixture, runner 또는 protocol source import/read
- `unsafe`, reflection, source parsing, process/file/network I/O로 compile surface를 위조하는 코드
- reverse/write/delete/cache/setter/JSON/custom-method symbol
- production package에 존재한다고 오해하게 하는 generated-version constant 또는 generator entrypoint

## 구현 단계

1. 기존 `TestExternalConsumerCompiles` 흐름에 physical exact 16 inventory/digest, legacy generated consumer의
   no-overlay success와 overlay-backed positive candidate compile을 추가합니다.
2. 같은 `TestExternalConsumerCompiles` body에서 overlay 없는 candidate, wrong model predicate/ordering/selector
   같은 negative compile attempt/helper를 subtest 없이 순차 실행합니다. Existing typed-misuse `t.Run` table에는
   row를 추가하지 않습니다.
3. 새 top-level `Test*`나 `t.Run`을 만들지 않고 기존 test inventory를 유지합니다.
4. AST/source whitelist, exactly-one overlay mapping, virtual target absence와 post-command physical cleanup을
   검증합니다.
5. normal, race, CGO-disabled, vet, root CI와 clean/diff checks를 수행하되 결과를 실행한 checkout에만 기록합니다.

## 완료 조건

- [x] Physical exact 16 count/size/digest와 exact 13 generated subset digest가 고정됨
- [x] Exactly one virtual project source만 더한 logical exact 17 compile view가 고정됨
- [x] Overlay가 있을 때 exact forward read-only external candidate가 컴파일됨
- [x] 같은 candidate가 overlay 없이는 `project.Using` 부재로 컴파일되지 않아 production false claim을 막음
- [x] Lazy/eager source result의 pointer type과 exact `OrderBy(...).First(ctx)` assignment가 컴파일됨
- [x] `Model()` unwrap 뒤 raw `ID`/`AuthorID` 접근이 컴파일됨
- [x] Wrong model/predicate/ordering/selector가 컴파일되지 않음
- [x] `project.Using(session)`은 callback 내부 assignability만 컴파일되고 lifetime claim은 없음
- [x] Reverse/REL-002/write/cache/JSON/custom method symbol과 forbidden import/source read가 AST gate에서 거부됨
- [x] Existing top-level Test/t.Run inventory, product manifest/counts와 physical fixture bytes가 unchanged
- [x] Activation, compile implementation과 completion documentation은 별도 exact head로 증명하고 재사용하지 않음
- [ ] Later exact seven-file terminal evidence/status head가 자체 hosted CI를 통과함

## 진행 기록

- [x] GDJ-0030 terminal exact-head CI를 EVID-063에서 clean baseline으로 확인
- [x] GDJ-0031 active work와 ADR-0031 Proposed boundary 작성
- [x] Internal compiletest overlay와 positive/negative/AST gates 구현
- [x] Focused normal/race/CGO0/vet와 root CI 실행
- [x] Independent scope/false-green audit
- [x] 결과에 따라 후보를 좁히되 public API acceptance는 별도 승인으로 분리
- [x] Exact 11-path completion-documentation head를 implementation과 별도 hosted CI로 검증

## 결정된 사항

- 2026-08-12: 기존 generated/fixture bytes를 바꾸지 않는 test-only Go overlay를 사용합니다.
- 2026-08-12: Exact 16에 없는 reverse kernel과 REL-002 mutation/cache를 이번 spike에서 만들지 않습니다.
- 2026-08-12: Private source wrapper의 scalar 접근은 direct field가 아니라 explicit `Model()` unwrap 후보만
  컴파일합니다.
- 2026-08-12: Raw low-level bridge의 존재는 canonical application API acceptance가 아닙니다.

## 미결정과 blocker

외부 blocker는 없습니다. 다음은 의도적으로 미결정입니다.

- `project.Using`, returned facade와 manager/selector의 exact exported names
- `First`/`Get` not-found contract와 `Limit` error ergonomics
- target도 relation-aware wrapper여야 하는 시점과 reverse manager/chaining shape
- wrapper scalar promotion, JSON, user method와 copy/clone policy
- FK assignment/cache invalidation과 loaded relation ownership
- query-only `db.Queryer` 이후 write/delete/session capability composition
- generated companion version, collision, deprecation과 upgrade command policy

## 테스트 증거

- Baseline: EVID-063 / hosted run `31516174741`, exact `ceff9e5...` only
- Activation documentation: EVID-064 / hosted run `31520396606`, exact `624347e...`; EVID-063을 재사용하지 않음
- Compile implementation: EVID-065 / hosted run `31528039746`, exact `0653902...`; activation run을 재사용하지 않음
- Completion documentation: EVID-066 / hosted run `31531470440`, exact `e9b2c0e...`; implementation run을
  재사용하지 않음
- Frozen physical exact 16은 62,538 bytes/SHA-256 `992589f0500a7f31808dac2bb2a669daecadab7b978f93f5227bee3ee1ca6cbb`,
  generated exact 13은 26,140 bytes/SHA-256
  `a284a36ce915c7d86ac28a8b7bc8866e634e7b9fa7aa2a18bbc98dc8576ef628`, logical exact 17은 65,970
  bytes/SHA-256 `29d37c4cc1446ce320bcd5476afafb77989cd980a1dd3f96cb0732803835737f`입니다.
- Local focused normal/race/CGO-disabled/vet, full `internal/compiletest`, unchanged 687/687/0 product inventory와
  independent audit P0/P1/P2/P3=`0/0/0/0`을 통과했습니다.
- EVID-066과 상태를 추가하는 later exact seven-file terminal tree 자체 CI는 `not run/pending`이며 completion
  run을 그 proof로 재사용하지 않습니다.

## 위험과 rollback

가장 큰 위험은 virtual overlay가 컴파일된 사실을 production facade 지원으로 오해하거나, exact 16에 없는 reverse/
cache/session-lifetime 동작을 test-only code로 위조하는 것입니다. 모든 production path는 frozen이고, spike rollback은
두 internal compiletest path와 이 work의 활성화 문서를 제거하는 documentation/test-only rollback입니다.

## 다음 정확한 작업

통합 담당자는 EVID-066과 상태를 추가하는 exact seven-file terminal patch의 scope/prefix/link를 감사하고 별도
commit/push 뒤 exact-head hosted CI를 얻습니다. 그 terminal head가 clean baseline이 된 뒤에만 Q-017의 production
facade/API freeze와 generated companion upgrade/collision/deprecation 정책을 새 work/ADR에서 활성화합니다. 그
전에는 이 test-only overlay의 `project.Using`, `Models`, `BlogPosts`, `Related`, `First` tuple, `Model` unwrap 또는
selector 이름을 production generator에 추가하지 않습니다. 현재 active/ready work는 없습니다.

## 결과와 인수인계

현재 work는 completed이고 ADR-0031은 test-only compile feasibility 방법에 한해서 Accepted입니다. Exact 세
`internal/compiletest` path만 구현됐고 제품 facade/generator/generated output은 없습니다. Product/manifest/counts는
unchanged exact `121 passing + 5 deviation + 1 oracle_locked`, relation 11/12이며 REL-002는 locked입니다. Q-013은
`Partial`, Q-017은 P1/open이고 모든 candidate 이름은 noncanonical입니다. Completion-documentation head
`e9b2c0e...`는 EVID-066/run `31531470440`의 별도 exact 26/26·326/326을 통과했습니다. 이 later exact seven-file
terminal record 자체 CI는 `not run/pending`이고 completion run을 재사용하지 않으며 Draft PR merge는 수행하지
않았습니다.
