# 현재 상태

- 갱신: 2026-09-19
- 활성 작업: [GDJ-0075 Scalar IN과 빈 조회의 의미](../../work/0075-scalar-membership-and-empty-query-semantics.md)
- 최근 완료: [GDJ-0074 DateTimeField와 모델 시각 값](../../work/0074-datetime-field-and-model-time-values.md)
- 최근 완료·전체 검증 source: `8fd8936d634b5038a534936c15a2b1cfac4b853b`
- 최신 전체 검증: [Text+DateTime Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35384697050)
- 로컬·Hosted의 source와 scope: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

Scalar IN을 공개 typed/dynamic query에 연결하고 빈 목록·NULL·부정 조건·실제 query 실행을 함께 검증한다.
별도 `feature/scalar-in-lookups` 작업 사본에 고정 Django 관찰과 초기 구현이 있으며 아직 기능 검증 PASS가 아니다.
Text+DateTime 전체 검증은 기준 source까지 완료했다. 새 IN 작업의 실행과 결과는 구분해 기록한다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정 없이 필요한 기반과 기능을 계속 구현한다. 기존 Draft PR #1을 이어간다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
