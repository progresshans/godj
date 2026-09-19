# 현재 상태

- 갱신: 2026-09-20
- 활성 구현: [GDJ-0087 시간대 없는 날짜의 모델·소비자 연결](../../work/0087-calendar-date-models.md)
- 검증 중: source `b2b01f80f9a8b04c1893f8dc5d24e9b19ba4b087`, [Date Hosted ORM](https://github.com/progresshans/godj/actions/runs/35471559786)
- 최근 완료: [GDJ-0086 nullable Boolean의 모델·Form/Admin/API 연결](../../work/0086-nullable-boolean-models.md)
- 최근 Hosted 기능 검증: source `2ea0735c7d811dd4e07862506de7643abc6073f9`, [Hosted ORM 완료](https://github.com/progresshans/godj/actions/runs/35467983458)
- 최근 전체 검증 source: `8fd8936d634b5038a534936c15a2b1cfac4b853b`
- 최신 전체 검증: [Text+DateTime Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35384697050)
- 로컬·Hosted의 source와 scope: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·Date·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. Scalar typed/dynamic IN과 검증 뒤 빈 조회 SQL 생략도 구현했다.
Nullable Boolean의 세 상태를 generated pointer·양 DB·Form/Admin·Helpdesk PUT/PATCH·OpenAPI/client까지 연결했다.
Calendar Date를 별도 Go 값·양 DB DATE·Form/Admin·Helpdesk service_on·PUT/PATCH·OpenAPI/client까지 연결했다.
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

GDJ-0086의 모델·migration·생성 ORM·Form/Admin·Helpdesk PUT/PATCH·OpenAPI/client 연결과 로컬·Hosted ORM 검증을 완료했다.
별도 `feature/calendar-date-models`에서 GDJ-0087의 날짜 값·IR·generator·양 DB·소비자 연결을 구현했다.
로컬 affected 일반/race/CGO0, 생성물 drift·vet과 독립 Python reference 재생을 통과했다.
해당 제품 source를 기존 Draft PR에 통합했다. 남은 행동은 위 Hosted ORM의 source·job·최종 scope 확인과 결과 기록이다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정 없이 필요한 기반과 기능을 이어간다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
