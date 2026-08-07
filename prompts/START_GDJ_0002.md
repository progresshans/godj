# GDJ-0002 Model-to-Query Walking Skeleton 시작 프롬프트

```markdown
GoDj의 GDJ-0002 첫 Model-to-Query 수직 단면을 수행해줘.

먼저 Git branch/commit/status/remote를 직접 확인하고 다음을 순서대로 읽어.

1. `AGENTS.md`
2. `docs/status/CURRENT.md`
3. `work/0002-model-to-query-walking-skeleton.md` 전체
4. 해당 work가 링크한 Architecture, Compatibility, Testing, Open Questions
5. `docs/adr/0001`, `0002`, `0003`, `0005`, `0006`
6. `conformance/README.md`와 M0 manifest/profile/protocol의 공개 형식

작업을 시작할 때 GDJ-0002를 `active`로 바꾸고 현재 commit을 baseline으로 기록해.
M0 fixture DSL이나 generator를 production API로 복사하지 마.

첫 구현 전에 descriptor, M1 nullable 표현, dynamic lookup 오류/chaining, package
dependency, SQLite driver를 작은 compile/runtime spike로 비교해. 최신 dependency
버전·Go 지원·license는 공식 출처와 실제 module metadata로 확인해. 결과가 장기
package/API 결정을 만들면 ADR과 work 문서에 함께 기록해.

그다음 다음 하나의 좁은 흐름만 끝까지 연결해.

Schema DSL → normalized/versioned IR → one-file deterministic codegen
→ generated Article/FieldSet/descriptor → generic Manager/QuerySet
→ typed/dynamic predicate가 같은 immutable Query AST로 수렴
→ SQLite compiler/executor → normalized GoDj observation
→ M0 Django oracle comparator

범위는 Article 한 모델, AutoField/CharField/BooleanField와 필요한 최소 nullable,
exact/ASCII icontains/isnull, AND/order/limit/All로 제한해. Migration engine, relation,
write lifecycle, PostgreSQL, Admin/Form/API를 만들지 마.

미구현 contract를 skip/pass하지 말고 `not_implemented` 또는 red로 유지해. 종료할 때
work/CURRENT/IMPLEMENTATION_MATRIX/TEST_EVIDENCE와 ADR을 실제 결과에 맞춰 갱신하고,
external consumer compile, codegen stale/idempotency, context cancellation/resource
close, relevant race test의 실행 여부를 정확히 보고해.
```
