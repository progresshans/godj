# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0085 self/cyclic 자동 migration 계획](../../work/0085-relation-autodetection.md)
- 최근 완료: [GDJ-0084 historical relation graph와 순환 migration](../../work/0084-relation-migration-graphs.md)
- 최근 Hosted 기능 검증: source `d6db513aba479ebec6a5f256bebac9348a0a32ce`, [Hosted ORM 완료](https://github.com/progresshans/godj/actions/runs/35456370913)
- 최근 전체 검증 source: `8fd8936d634b5038a534936c15a2b1cfac4b853b`
- 최신 전체 검증: [Text+DateTime Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35384697050)
- 로컬·Hosted의 source와 scope: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. Scalar typed/dynamic IN과 검증 뒤 빈 조회 SQL 생략도 구현했다.
String/int64 choices를 모델·Form/Admin/API에 연결하고 물리 DDL 없는 historical AlterField로 변경 이력을 보존한다.
Loaded self/cyclic 관계 graph의 Create·다중 Add/Remove와 transitive target을 실제 양 DB migration에 연결하고,
SQLite remake의 inbound/self 참조 값·행·sequence와 실패 뒤 FK 복원/폐기·quarantine을 검증했다.
관계를 함께 읽는 query에도 Count를 연결해 필터·Distinct·슬라이스와 eager cache 의미를 보존한다.
Nullable/required forward FK의 유한한 여러 단계 경로에 scalar 비교·문자열·isnull·IN과 AND/OR/NOT을 연결하고 JOIN·부정의 null 의미를 보존한다.
여러 direct·nested forward selected 관계와 다른 forward/reverse filter JOIN의 All·First·Count, 중복 행·Distinct·슬라이스·하위 nullable cache를 연결했다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

GDJ-0084는 로컬 normal/race/CGO0·생성 소비자와 통합 source의 Hosted ORM 검증을 완료했다.
별도 `feature/relation-autodetection`에서 GDJ-0085를 시작했다. Self Create/nullable self Add 자동 후보의 구현·compile 확인을
마쳤으며 same-app later target과 cross-app cycle의 operation/candidate 분할, 선언 순서·durable publication prefix와
실제 CLI/DB 검증을 이어간다. GDJ-0085 제품 변경은 아직 기존 Draft PR에 통합하지 않았고 runtime PASS도 없다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정 없이 필요한 기반과 기능을 계속 구현한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
