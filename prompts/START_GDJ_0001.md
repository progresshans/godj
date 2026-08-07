# GDJ-0001 Compatibility Lab 시작 프롬프트

```markdown
GoDj의 첫 구현 작업인 GDJ-0001 Compatibility Lab을 수행해줘.

먼저 다음을 직접 확인하고 읽어.

- Git branch/commit/status/remote
- `AGENTS.md`
- `docs/status/CURRENT.md`
- `work/0001-compatibility-lab.md` 전체
- 그 work item의 `필수 읽기`에 링크된 문서와 ADR

`work/0001-compatibility-lab.md`를 이번 작업의 범위와 완료 조건 정본으로 사용해. 문서의 baseline과 실제 checkout이 다르면 먼저 기록을 바로잡아.

이 작업의 핵심 결과는 ORM 구현이 아니라 다음 다섯 가지야.

1. Django 6.1 reference 환경의 exact/reproducible lock
2. provenance가 있는 8~12개 작은 compatibility contract
3. byte-deterministic Django oracle과 검증된 normalizer
4. 의도적인 결과/순서/오류/DB-state 차이를 반드시 잡는 comparator
5. stale generated code 때문에 package가 compile되지 않는 codegen bootstrap 실패의 재현과 후보 비교

범용 scenario DSL이나 production ORM을 만들지 마. 초기 adapter는 명시적으로 작성하고 반복이 확인된 데이터만 공통 protocol로 추출해. GoDj 미구현 상태를 skip/pass로 처리하지 마.

module path를 만들기 전 remote를 다시 확인하고, 새로운 production dependency나 public API가 필요하면 이유와 대안을 work item/ADR에 기록해. `/Users/hanhyeonjin/Documents/django`의 현재 checkout은 6.2 alpha reference이므로 수정하거나 6.1 oracle로 그대로 사용하지 마. disposable environment 또는 정확히 고정된 6.1 tag/package를 사용해.

작업 중에는 work item 체크리스트를 실제 결과에 맞게 갱신하고, 종료 시 CURRENT/IMPLEMENTATION_MATRIX/TEST_EVIDENCE와 일치시켜. 테스트를 실행하지 않았다면 통과했다고 표현하지 마.
```
