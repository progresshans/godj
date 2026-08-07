# GoDj 작업 계속하기 — Codex 프롬프트

아래 내용을 새 Codex 대화의 첫 요청으로 사용할 수 있습니다.

```markdown
이 저장소의 GoDj 작업을 현재 상태에서 이어서 진행해줘.

작업을 시작할 때 다음 순서를 지켜줘.

1. 저장소 root와 Git branch/commit/status/remote를 직접 확인한다.
2. root `AGENTS.md`를 읽는다.
3. `docs/status/CURRENT.md`를 읽고 현재 활성 work item을 찾는다.
4. 활성 work item 전체와 그 문서가 직접 링크한 Compatibility/Testing/ADR/Open Question만 읽는다.
5. 문서의 baseline과 실제 checkout이 다르면 구현 전에 차이를 CURRENT와 work item에 기록한다.
6. work item의 목표, 비목표, allowed paths, contract IDs, 완료 조건을 짧게 요약한 뒤 작업한다.

중요한 원칙:

- Proposed, Accepted, Implemented, Verified를 구분한다.
- 긴 설계안의 API 예시를 확정 public API로 가정하지 않는다.
- Schema DSL → Schema IR → Codegen → Generic Core → Runtime Metadata → Query AST → Backend Compiler의 책임을 섞지 않는다.
- Go generic method에 receiver에 없는 새 type parameter를 선언하지 않는다.
- typed query와 dynamic lookup은 같은 Query AST 의미로 수렴한다.
- 미구현 또는 미지원 기능을 skip이나 silent fallback으로 통과시키지 않는다.
- Django oracle은 exact profile과 provenance가 고정되기 전에는 정답으로 저장하지 않는다.
- 사용자 변경과 work item 범위 밖 파일을 보존한다.
- 동일한 공개 API나 CURRENT를 여러 subagent가 동시에 수정하지 않게 한다.

작업 중 새 장기 결정이 필요하면 관련 ADR을 Proposed로 작성하고, prototype/test 증거 없이 임의 확정하지 않는다. 권한이나 사용자 선택이 반드시 필요한 경우에만 멈춰 질문한다.

작업 종료 시 반드시 다음을 갱신해줘.

- 활성 work item의 체크리스트, 변경 파일, 결정, 테스트 증거, 미결정 사항, 다음 정확한 작업
- `docs/status/CURRENT.md`
- 상태가 바뀐 항목만 `docs/status/IMPLEMENTATION_MATRIX.md`
- 실제 실행한 검증만 `docs/status/TEST_EVIDENCE.md`
- 새 의도적 차이나 장기 결정이 있으면 deviation/ADR

마지막 보고에는 변경 파일, 구현된 관찰 가능 동작, 설계 결정, 실행한 테스트와 결과, 실행하지 못한 테스트, 알려진 제한, 다음 작업을 포함해줘.
```
