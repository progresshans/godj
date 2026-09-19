# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0086 nullable Boolean의 모델·Form/Admin/API 연결](../../work/0086-nullable-boolean-models.md)
- 최근 완료: [GDJ-0085 self/cyclic 자동 migration 계획](../../work/0085-relation-autodetection.md)
- 최근 Hosted 기능 검증: source `4320eba32a0dcb3a1e21b6244c87e32a89dad5b6`, [Hosted ORM 완료](https://github.com/progresshans/godj/actions/runs/35463646580)
- 최근 전체 검증 source: `8fd8936d634b5038a534936c15a2b1cfac4b853b`
- 최신 전체 검증: [Text+DateTime Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35384697050)
- 로컬·Hosted의 source와 scope: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. Scalar typed/dynamic IN과 검증 뒤 빈 조회 SQL 생략도 구현했다.
Nullable Boolean의 세 상태를 generated pointer·양 DB·Form/Admin·Helpdesk PUT/PATCH·OpenAPI/client까지 연결했다.
환경별 검증 상태는 아래 현재 작업과 TEST_EVIDENCE를 따른다.
String/int64 choices를 모델·Form/Admin/API에 연결하고 물리 DDL 없는 historical AlterField로 변경 이력을 보존한다.
Loaded self/cyclic 관계 graph의 Create·다중 Add/Remove와 transitive target을 실제 양 DB migration에 연결하고,
SQLite remake의 inbound/self 참조 값·행·sequence와 실패 뒤 FK 복원/폐기·quarantine을 검증했다.
Self·same-app later/mutual·cross-app cycle의 자동 migration 계획과 선언 순서, 부분 게시 뒤 결정적 재개를 구현했다.
SQLite의 모든 새 물리 연결은 외래키 검사를 활성화·확인한다.
관계를 함께 읽는 query에도 Count를 연결해 필터·Distinct·슬라이스와 eager cache 의미를 보존한다.
Nullable/required forward FK의 유한한 여러 단계 경로에 scalar 비교·문자열·isnull·IN과 AND/OR/NOT을 연결하고 JOIN·부정의 null 의미를 보존한다.
여러 direct·nested forward selected 관계와 다른 forward/reverse filter JOIN의 All·First·Count, 중복 행·Distinct·슬라이스·하위 nullable cache를 연결했다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

GDJ-0085의 자동 계획·CLI 게시·실제 SQLite/PG migration과 생성 ORM 소비자를 연결하고 로컬·Hosted ORM 검증을 완료했다.
별도 `feature/nullable-boolean-models`에서 GDJ-0086을 구현했다. 독립 Django/DRF 기준과 일반 affected 실행을 통과했다.
로컬 영향 검증과 CI 필수 실행 목록을 마무리했으며, 기존 Draft PR 통합과 Hosted ORM 검증을 이어간다.
세 상태와 생략 규칙을 모델·실제 양 DB migration/ORM·Form/Admin·PUT/PATCH·OpenAPI 및 외부 생성 client에서 연결했다.
GDJ-0086 source `2ea0735c7d811dd4e07862506de7643abc6073f9`를 기존 Draft PR에 통합했다.
[Hosted ORM](https://github.com/progresshans/godj/actions/runs/35467983458)의 최종 결과를 확인 중이며 아직 PASS가 아니다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정 없이 필요한 기반과 기능을 계속 구현한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
