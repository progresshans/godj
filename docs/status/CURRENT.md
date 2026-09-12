# 현재 상태

- 갱신: 2026-09-12
- 활성 작업: 없음 — 다음 기반 작업 선택 전
- 최근 완료: [GDJ-0071 API schema 정체성과 실제 생성 클라이언트 기반](../../work/0071-api-schema-identity-and-generated-client.md)
- 최근 문서 정리: [통합 개발 경험·API 비교 조사](../research/2026-09-12-framework-developer-experience.md), [개발 판단 기준](../DEVELOPMENT_CRITERIA.md)
- 최신 전체 검증 소스: `b74a79eb4948ef6b57ef5271103cf05eb514d23e`
- 최신 검증: [GDJ-0069 Hosted full scope 완료](https://github.com/progresshans/godj/actions/runs/34466299719)
- 작업 브랜치: `codex/revision-fenced-migration-lifecycle`

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article 기반 Web/Form/Admin/API·영속 인증의 제한된 수직 단면이 있다.
현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계 흐름을 검증한다.
구현 범위는 [완료 작업](../../work/0071-api-schema-identity-and-generated-client.md), source·환경별 실제 실행은 [증거](TEST_EVIDENCE.md)에 둔다.

## 다음 행동

다음 후보는 정수 field 의미를 Schema IR부터 serializer·DB·생성 코드까지 연결하는 작업이다. 구체적인 consumer와
실패 계약을 먼저 정하고 필요한 기반의 완성도를 MVP 일정에 앞세운다. 아직 다음 작업을 시작하지 않았다.
위 전체 검증 링크는 이전 제품 소스의 근거이며 GDJ-0070/0071의 전체 PASS가 아니다.
기존 Draft PR #1을 유지하고 제출·통합 milestone의 검증 범위는 실제 변경에 맞춰 선택한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
