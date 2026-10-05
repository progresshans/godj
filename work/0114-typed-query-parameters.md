---
id: GDJ-0114
status: active
updated: 2026-10-06
baseline_commit: "8ecc3152af2cdc3efa5250c5acb1fd2705f71bef"
integration_owner: "root"
---

# Typed query 입력과 parameter 선언

## 문제와 결과

Helpdesk의 우선순위 요약과 Label/티켓–Label 목록은 같은 query 이름·정수 범위·기본값·문자열 한도를
parser와 OpenAPI에 각각 선언한다. [개발 기준](../docs/DEVELOPMENT_CRITERIA.md)의 typed 입력/출력 연결을
확장해, 하나의 불변 query 입력 선언에서 Go DTO·실행 검증·parameter schema를 함께 만든다.
실제 인가·Category/관계 범위·집계/목록 조회·error presentation은 각 handler가 계속 소유한다.

최초 소비자는 요약의 `p`/`min_open`과 Label/티켓–Label 목록의 `limit`/`offset`/선택 `search`다.
요약은 정규 decimal 정수, 두 페이지 목록은 부호 없는 digits의 앞쪽 0도 받아들이는 기존 의미를 갖는다.
두 의미를 명시한 닫힌 변환 선언으로 구분하고 정수 범위·기본값·UTF-8 byte/empty 정책을 함께 검증한다.
Ticket 본문 검색의 별도 parser는 raw segment와 오류 순서가 다르므로 이번 연결에 포함하지 않는다.
임시 호환 계층이나 모든 endpoint의 동시 재작성 없이 실제로 필요한 공통 기반을 채택한다.

## 설계 경계

Getter/DTO 출력과 마찬가지로 잘못된 scalar/DTO 할당은 Go compile 단계에서 거부한다. Unknown/duplicate 이름,
잘못된 URL encoding·정수 표기·overflow·UTF-8/NUL·query/value 한도를 명시하고, 선언의 zero/nil/중복/잘못된
range/default/name은 준비 시 오류다. 이름과 parse·schema를 별도 임의 callback 쌍으로 바꾸지 않는다.
선언 slice와 request 결과는 호출자별로 소유하고, 입력 실패 시 부분 DTO나 setter 실행을 노출하지 않는다.
Omitted·required·default·empty를 구분하며 query의 부재를 JSON null 의미로 바꾸지 않는다.

파싱은 명시한 크기의 문자열에 대한 순수 작업이다. 실제 request/body I/O·인가·DB 조회와 그 context/error는
현재 owner를 유지한다. Schema가 표현하는 값 범위와 URL lexical/UTF-8 byte 정책을 구분하고, 공개할 metadata가
runtime과 어긋나지 않도록 실제 OpenAPI와 고정 생성 client로 대조한다. 새 schema 표현 때문에 생성물이 바뀌면
실제 문서에서 재생성하며 Go module/tool lock은 소유권에 따라 유지한다.

`api/parameters`의 닫힌 codec·presence·DTO setter와 prepared Query를 실제 소비자에 연결했다. 기존 Go query 계약을 연결하는 작업이며 새로운
Django parsing 의미를 채택하거나 JSON body/model binder·header/path 입력·endpoint DSL·viewset 전체를 완료한 것으로
계산하지 않는다. 장기 선택은 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md)에 반영한다.

## 구현과 검증

- [x] 실제 query parser·parameter 선언·인가/오류 순서와 서로 다른 lexical 정책 확인
- [x] 불변 typed query 선언·scalar/default/absence/한도·실패와 같은 parameter schema
- [x] 요약 HTML/API와 두 페이지 API의 실제 typed DTO 연결
- [x] 독립 compile 거부·값/오류/소유권·실제 HTTP/생성 client 대조
- [x] 필요한 영향 세 mode·양 DB·drift와 문서/실행 증거 기록
- [ ] 선행 그룹/typed 출력과 함께 후속 source의 통합 milestone 전체 platform/Hosted 확인

선행 Hosted [37365281161](https://github.com/progresshans/godj/actions/runs/37365281161)은
`720c9be6211f00a146a39d00957a81ce69ed294f`만 검증한다. 현재 기능의 전체 local/Hosted를 중복하지 않고
관련 영향 검사와 필요한 통합 milestone을 분리한다. 상세 실행은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md),
활성 상태는 [CURRENT](../docs/status/CURRENT.md)에 둔다. 현재 외부 입력이 필요한 blocker는 없다.
