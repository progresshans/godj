# 현재 상태

- 갱신: 2026-09-19
- 활성 작업: [GDJ-0072 일반 정수 필드와 기존 모델의 성장](../../work/0072-integer-field-model-growth.md)
- 최근 완료: [GDJ-0071 API schema 정체성과 실제 생성 클라이언트 기반](../../work/0071-api-schema-identity-and-generated-client.md)
- 최근 문서 정리: [헌장](../CHARTER.md)·[개발 방향](../ROADMAP.md)·[개발 판단 기준](../DEVELOPMENT_CRITERIA.md)에 출시 일정 없이 목표 기능과 기반을 완성하는 원칙 명시
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

정수 field를 Schema IR부터 serializer·DB·생성 코드까지 연결했고 Helpdesk priority를 새 migration으로 추가했다.
관련 로컬 normal 검증을 마쳤으며 누적 GDJ-0070/0071/0072의 Hosted full 통합 결과를 확인한다.
장기 목표는 헌장·기능 카탈로그의 완성이며, 현재 작업 이후에도 남은 기반과 기능을 의존성 순서로 계속 구현한다.
위 전체 검증 링크는 이전 제품 소스의 근거이며 GDJ-0070/0071의 전체 PASS가 아니다.
기존 Draft PR #1을 유지하고 제출·통합 milestone의 검증 범위는 실제 변경에 맞춰 선택한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
