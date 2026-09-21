# 현재 상태

- 갱신: 2026-09-22
- 활성 구현: [GDJ-0096 일대일 관계와 티켓 작업 보고서](../../work/0096-one-to-one-service-reports.md)
- 최근 완료: [GDJ-0095 모델 고유성과 외부 참조 중복 방지](../../work/0095-model-uniqueness.md)
- 최근 전체 검증: [고유성 수직 연결 Hosted full](https://github.com/progresshans/godj/actions/runs/35607632806), source `42ae95d3b1a891e6a0692fb0399968e483f4d907`
- Source·환경·scope와 실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Column uniqueness를 Schema IR·생성 모델·migration·SQLite/PostgreSQL·ORM 사전 검증에서 Form/Admin/API와
실제 Helpdesk/client까지 연결하고 통합 검증했다. 지원 범위와 제약은
[구현 현황](IMPLEMENTATION_MATRIX.md), [Backend 범위](../BACKEND_MATRIX.md), [고유성 소유권](../adr/0072-column-uniqueness-and-constraint-ownership.md)이 소유한다.

명시적 OneToOne을 IR·생성 metadata·historical migration과 양 DB의 FK+UNIQUE에 연결했다.
Cross-app 생성 소비자가 단일 reverse 조회/prefetch·forward eager·중복 저장 rollback·PROTECT/SET_NULL을 사용한다.
단일 reverse의 관계/필드 isnull·nullable/Boolean·비교/IN/검색과 AND/OR/NOT를 typed/dynamic 공통 AST에 연결했다.
양 DB에서 독립 Django의 결과·실제 SELECT 수·JOIN 형태를 비교하고 일반·race·CGO 비활성 checkpoint를 통과했다.
Typed reverse/mixed eager tree도 기존 scanner·evaluation·cache에 연결했다. 생성 selector/FromSelected bridge,
부재와 자식 교체의 owner 기준 Fresh, 잘못된 FK·중복·부분 행·실패 재시도를 양 DB에서 확인했다.
Facade의 reverse selector·문자열 mixed path와 지연 접근·새 부모 저장·선택한 형제 cache 보존도 연결했다.
Outgoing FK가 있는 모델의 incoming PROTECT/SET_NULL 삭제 바인딩을 정리했다. 관련 생성 ABI와 네 프로젝트의 생성물을 갱신했다. 설계는
[일대일 관계 ADR](../adr/0073-one-to-one-cardinality-and-reverse-objects.md)이 소유한다. 현재 변경의 Hosted 전체 검증은 아직 실행하지 않았다.

## 다음 행동

OneToOne forward/reverse assignment의 객체·cache·저장 의미와 required/nullable·unsaved 실패 경로를 완성한다.
그 위에 작업 보고서의 Form/Admin/API/OpenAPI/client·권한·실패 복구를 연결한다.
구체적인 완료 조건은 활성 work가 소유하며 일대일 관계 전체를 완료 처리하지 않았다.
현재 확인된 외부 blocker는 없다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과 기능을 이어간다.
설계 채택, 제품 구현, 환경별 검증은 구분하며 한 기능의 결과를 전체 프레임워크 완료로 합치지 않는다.
