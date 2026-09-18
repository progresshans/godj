# 현재 상태

- 갱신: 2026-09-19
- 활성 작업: [GDJ-0073 TextField와 모델의 여러 줄 입력](../../work/0073-text-field-and-multiline-model-forms.md)
- 최근 완료: [GDJ-0072 일반 정수 필드와 기존 모델의 성장](../../work/0072-integer-field-model-growth.md)
- 최근 문서 정리: [헌장](../CHARTER.md)·[개발 방향](../ROADMAP.md)·[개발 판단 기준](../DEVELOPMENT_CRITERIA.md)에 출시 일정 없이 목표 기능과 기반을 완성하는 원칙 명시
- 최신 전체 검증 소스: `b43552a1f88259babe97ec9fe83951f8cd205261`
- 최신 검증: [GDJ-0070/0071/0072 Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35370184198), 최종 attempt 2
- 작업 브랜치: `codex/revision-fenced-migration-lifecycle`
- 후속 구현: 별도 worktree의 `feature/text-field-model-growth` 브랜치, 아직 현재 브랜치에 통합하지 않음

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article 기반 Web/Form/Admin/API·영속 인증의 제한된 수직 단면이 있다.
현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계 흐름을 검증한다.
구현 범위는 [완료 작업](../../work/0071-api-schema-identity-and-generated-client.md), source·환경별 실제 실행은 [증거](TEST_EVIDENCE.md)에 둔다.

일반 signed int64 필드의 nullable/default·typed query·Form/Admin/API를 연결했다. Helpdesk priority는 새 migration으로
기존 데이터를 보존하며 [정수 의미](../adr/0059-signed-integer-field-and-model-growth.md)에 따라 동작한다.

## 다음 행동

별도 worktree에서 TextField와 textarea·빈 입력 정책을 구현하고 Helpdesk resolution의 새 migration·실제 소비자 검증을 연결한다.
장기 목표는 헌장·기능 카탈로그의 완성이며, 현재 작업 이후에도 남은 기반과 기능을 의존성 순서로 계속 구현한다.
위 전체 검증은 GDJ-0072까지의 source 근거이며 후속 TextField 변경의 PASS가 아니다.
기존 Draft PR #1을 유지하고 제출·통합 milestone의 검증 범위는 실제 변경에 맞춰 선택한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
