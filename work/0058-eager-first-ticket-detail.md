---
id: GDJ-0058
status: active
updated: 2026-09-06
baseline_commit: "0b9955ec0ef3b013e59fd185038d38e57582d008"
---

# 관계를 포함한 단일 객체 조회

관계 조회 경로의 반복 분기·오류 처리를 먼저 정리하고, 기존 동작을 검증한 뒤 다음 기능을 연결한다.
Helpdesk의 티켓 상세 조회에서 티켓과 배정된 Category를 한 번의 joined query로 읽는 흐름을 사용한다.
라인 수 자체보다 같은 변경을 여러 곳에 적용해야 하는 비용을 줄이는 데 우선순위를 둔다.

## 범위와 의미

- `ForwardSelectQuery`와 생성된 typed/dynamic/eager facade에 `First(ctx)`를 연결한다.
- 기존 `QuerySet.First`처럼 명시적 정렬을 요구한다. Offset/Distinct/기존 Limit과 관계 JOIN을 유지한다.
- Cold First는 최대 한 row를 읽고 원래 All cache를 채우지 않는다. Warm All cache는 첫 객체를 복제해 재사용한다.
- required/nullable 관계 검증, context/error/Rows.Close 의미와 결과 객체의 독립 소유권을 유지한다.
- All/First의 projection scan·객체 변환을 공유한다. 관련 없는 migration/CLI 파일은 이번 작업에 포함하지 않는다.
- Helpdesk 상세 API는 기존 인증과 배정 Category 범위를 지키고 명시한 필드만 출력한다.
- eager Count, 다중 관계·임의 깊이 탐색과 새로운 migration operation은 별도 기능이다.

수정 허용 경로는 `orm/`, 관계 생성기와 해당 `codegen/` 테스트·golden, 영향받은 프로젝트의
generated source/manifest, `examples/helpdesk/`, `Makefile`의 generated drift gate, 이 작업 및 관련 현행 상태·ADR 문서다.

## 확인 목록

- [x] 현재 terminal·JOIN·cache 계약과 Django 기준의 관련 의미 확인
- [x] 동작 보존 정리: typed/dynamic/facade의 중복 dispatch와 context 오류 probe 제거
- [x] 공통 runtime과 생성된 typed/dynamic/eager First 구현
- [x] Helpdesk 실제 상세 조회와 권한·Category 범위·출력 연결
- [x] cold/warm/empty/ordering/slicing/nullability/error/resource 부정 회귀
- [x] generated drift와 실제 외부 consumer compile 검증
- [ ] 관련 normal/race/CGO-disabled와 PostgreSQL/Hosted 검증
- [ ] 상태·검증 증거·기존 Draft PR 정리

## 검증 실행

편집 중에는 affected ORM/codegen/Helpdesk 및 generated drift를 실행한다. 통합에서 관련 race/CGO-disabled와
외부 compile을 확인하고, 기존 Draft PR의 ORM scope를 통해 관계 platform matrix·실제 PostgreSQL·제품 integration을 검증한다.
전체 platform 검증으로 확대해 보고하지 않으며 이전 GDJ-0057의 결과를 새 소스의 결과로 쓰지 않는다.
실제 명령·결과는 TEST_EVIDENCE에 기록한다.

## 구현 결정과 변경 경로

- `orm/evaluation.go`, `manager.go`, `result.go`, `select_related.go`: cache 조회와 backend rows 획득·실패 정리를 공유한다.
  eager All/First는 동일 projection scanner와 결과 복제를 사용한다. First의 row 상한만 다르다.
- `codegen/project_relation_select_related.go`, `project_relation_facade.go`: dynamic/facade의 관계별 저장·재분기를
  하나의 private typed terminal interface로 바꾸고, All/First의 객체 변환을 공유한다. binding 오류는 runtime으로 전달한다.
- 두 생성기 버전을 올리고 Helpdesk·Article·relationdeleteproduct의 source/manifest를 다시 생성했다.
  나머지 generated 파일의 변경은 같은 bundle의 provenance hash 갱신이다.
- 첫 Hosted 실행에서 별도 관계 fixture의 미갱신 산출물을 발견했다. `relationselectproduct` companion을 재생성하고
  `make generate-check`에 manifest가 없는 관계 fixture 여섯 개의 기존 drift 검사를 포함했다.
- `examples/helpdesk/app.go`: 기존 project binding을 보관해 재사용하고 `GET /api/tickets/<int64:id>/`를 추가한다.
  ViewTicket에는 배정 Category의 id/name 조회가 포함되며 별도 Category Admin의 ViewCategory 정책은 유지한다.
- ORM 직접 테스트, 생성된 외부 Go module과 nullable facade 테스트, Helpdesk SQLite/PostgreSQL 공통 HTTP consumer가 검증을 소유한다.
  오래된 private discriminator 검사 대신 현재 shared helper의 namespace collision과 실제 오류·query 동작을 검사한다.

Django 참조는 profile과 같은 `6.1` tag commit `fe0a859f537d4238cf49fca39073513206f83122`의
`django/db/models/query.py:1182`를 읽어 확인했다. 한 row slicing 의미를 참고하며 GoDj의 기존 명시적 정렬 계약을 유지한다.
이번 First에 대한 Django differential oracle 실행·새 conformance contract 추가를 주장하지 않는다.
