# 현재 상태

- 갱신: 2026-09-12
- 활성 작업: 없음
- 최근 완료: [GDJ-0070 모델과 실제 API 선언에서 OpenAPI 제공](../../work/0070-model-derived-openapi.md)
- 최근 문서 정리: [통합 개발 경험·API 비교 조사](../research/2026-09-12-framework-developer-experience.md), [개발 판단 기준](../DEVELOPMENT_CRITERIA.md)
- 최신 전체 검증 소스: `b74a79eb4948ef6b57ef5271103cf05eb514d23e`
- 최신 검증: [GDJ-0069 Hosted full scope 완료](https://github.com/progresshans/godj/actions/runs/34466299719)
- 작업 브랜치: `codex/revision-fenced-migration-lifecycle`

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article 기반 Web/Form/Admin/API·영속 인증의 제한된 수직 단면이 있다.
현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 OpenAPI 3.1 문서를 만들고 Article의 인증된 `/api/openapi.json`에 게시한다.
Full/partial·응답 투영, 실제 Session/Bearer metadata와 HTTP 소비자 흐름의 관련 로컬 검증을 완료했다.
구현 범위는 [완료 작업](../../work/0070-model-derived-openapi.md), source·환경별 실제 실행은 [증거](TEST_EVIDENCE.md)에 둔다.

## 다음 행동

[개발 기준](../DEVELOPMENT_CRITERIA.md)에 따라 구조가 다른 API consumer나 typed client 연결의 구체적인 요구를 다음 범위로 선택한다.
GDJ-0070은 작업 사본에 반영되어 있으며 위 전체 검증 링크는 이전 제품 소스의 근거다.
기존 Draft PR #1을 유지하고 다음 제출·통합 milestone의 검증 범위는 실제 변경에 맞춰 선택한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
