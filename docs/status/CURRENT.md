# 현재 상태

- 갱신: 2026-09-19
- 활성 작업: [GDJ-0078 Nullable ForeignKey의 대상 필터](../../work/0078-nullable-forward-relation-predicates.md)
- 최근 완료: [GDJ-0077 관계 조회의 Count와 캐시 의미](../../work/0077-eager-count-and-query-cache-semantics.md)
- 최근 Hosted 기능 검증: source `d0f079481d660682c7a18884ce2c305234f6ad38`, [Hosted ORM 완료](https://github.com/progresshans/godj/actions/runs/35401719591)
- 최근 전체 검증 source: `8fd8936d634b5038a534936c15a2b1cfac4b853b`
- 최신 전체 검증: [Text+DateTime Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35384697050)
- 로컬·Hosted의 source와 scope: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. Scalar typed/dynamic IN과 검증 뒤 빈 조회 SQL 생략도 구현했다.
String/int64 choices를 모델·Form/Admin/API에 연결하고 물리 DDL 없는 historical AlterField로 변경 이력을 보존한다.
관계를 함께 읽는 query에도 Count를 연결해 필터·Distinct·슬라이스와 eager cache 의미를 보존한다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

GDJ-0078에서 nullable ForeignKey 대상 필터의 Django 결과·Boolean 조합·null 행의 의미를 확인하고 양 backend에 연결한다.
GDJ-0077 eager Count는 관련 로컬·양 DB·race·generated consumer 검증을 마쳤다. 다음 관계 확장과 묶어 Hosted ORM을 실행한다.
GDJ-0076의 Hosted는 위 기준 source를 소유하며, 정확한 변경별 source와 scope는 TEST_EVIDENCE를 따른다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정 없이 필요한 기반과 기능을 계속 구현한다. 기존 Draft PR #1을 이어간다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
