# 현재 상태

- 갱신: 2026-09-19
- 활성 구현: [GDJ-0083 중첩 forward eager 조회와 cache](../../work/0083-nested-forward-eager-graphs.md)
- 진행 중인 Hosted ORM: source `a49b592be1896d02d73a9657fe360623ffa296d8`, [실행](https://github.com/progresshans/godj/actions/runs/35425015186)
- 최근 완료: [GDJ-0082 여러 단계의 forward 관계 조회](../../work/0082-nested-forward-relation-paths.md)
- 최근 Hosted 기능 검증: source `3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0`, [Hosted ORM 완료](https://github.com/progresshans/godj/actions/runs/35421304637)
- 최근 전체 검증 source: `8fd8936d634b5038a534936c15a2b1cfac4b853b`
- 최신 전체 검증: [Text+DateTime Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35384697050)
- 로컬·Hosted의 source와 scope: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. Scalar typed/dynamic IN과 검증 뒤 빈 조회 SQL 생략도 구현했다.
String/int64 choices를 모델·Form/Admin/API에 연결하고 물리 DDL 없는 historical AlterField로 변경 이력을 보존한다.
관계를 함께 읽는 query에도 Count를 연결해 필터·Distinct·슬라이스와 eager cache 의미를 보존한다.
Nullable/required forward FK의 유한한 여러 단계 경로에 scalar 비교·문자열·isnull·IN과 AND/OR/NOT을 연결하고 JOIN·부정의 null 의미를 보존한다.
여러 direct·nested forward selected 관계와 다른 forward/reverse filter JOIN의 All·First·Count, 중복 행·Distinct·슬라이스·하위 nullable cache를 연결했다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

GDJ-0082의 다단계 forward 조회를 구현했고 normal/race/CGO0와 기존 Draft PR의 Hosted ORM 검증을 완료했다.
GDJ-0083의 typed/dynamic 중첩 선택과 생성 소비자·하위 cache를 구현하고 로컬 normal/race/CGO0를 검증했다.
기존 Draft PR에 제품 source `a49b592`를 통합했고 위 Hosted ORM 결과를 확인한 뒤 다음 기능으로 이어간다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정 없이 필요한 기반과 기능을 계속 구현한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
