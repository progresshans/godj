---
id: GDJ-0107
status: active
updated: 2026-10-01
baseline_commit: "c808c682d54c98bf67021a98da648998cd8bc0a3"
integration_owner: "root"
---

# 단건 조회와 savepoint 기반 조회 후 생성

## 결과와 범위

모델 query로 정확히 한 객체를 조회하고, 없을 때만 생성 입력을 평가한다. 실제 unique 충돌은
해당 생성 범위를 rollback한 뒤 한 번 다시 조회한다. Helpdesk의 category/name Label을 확보하는
명시적 업무 흐름에서 부모 transaction·현재 권한·감사 기록과 이 동작을 함께 사용한다.
일반 create의 중복 오류 의미는 유지한다.

기반은 공통 QuerySet의 `Get`, borrowed session의 savepoint, `GetOrCreate`다.
공통 AST·Manager의 metadata snapshot·query cache 소유권과 실제 DB 제약을 유지한다.
취소·cursor 수명·nested scope·rollback 실패·unknown outcome을 정상 생성이나 자동 재시도로 바꾸지 않는다.
내부 savepoint를 해제한 성공은 부모 transaction의 commit을 뜻하지 않는다.

이 작업은 별도 작업 사본에서 구현한다. GDJ-0106은 고정 source `8e5c2b3e`의
[Hosted full](https://github.com/progresshans/godj/actions/runs/36805466011)까지 완료했다.
해당 source에 이 작업의 새 제품 변경은 포함되지 않으며 성공을 새 변경의 검증으로 전이하지 않는다.

## 구현과 검증

- [ ] 고정 Django의 단건 조회·cache·기본값·savepoint·실제 경쟁을 양 DB에서 독립 관찰하고 기준 fixture로 관리
- [ ] 정확한 단건 판정과 fresh 평가·원 query cache 보존·typed/dynamic 및 관계 소비자
- [ ] SQLite/PostgreSQL session의 savepoint·중첩 scope·cursor 정리·실패와 부모 commit 차단
- [ ] 생성 입력의 지연 평가·원 Manager snapshot·unique 충돌 후 한 번의 조회와 명시적 미지원 capability
- [ ] Helpdesk의 현재 권한·category 범위·Label 확보·원자 audit와 Form/Admin/API·독립 client 흐름
- [ ] 완성된 변경 묶음의 영향 normal/race/CGO=0·양 DB·실제 경쟁·실패 대조와 필요한 생성 drift
- [ ] transaction 기반 변경의 source를 고정한 Hosted 통합과 현행 기록

기본 동작의 설계는 [ADR-0086](../docs/adr/0086-single-object-creation-and-savepoint-ownership.md)에서 검토한다.
실행 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록하며, local 전체와 Hosted 전체를 중복하지 않는다.
편집 중에는 필요한 compile만 확인하고 완성된 제품·생성·소비자·테스트 묶음에서 영향 검증을 수행한다.

## 현재 상태와 다음 행동

독립 탐색에서 Django 6.1/Python 3.14.3의 단건 조회 11개, 기본 생성·중첩 transaction 9개,
두 연결의 unique 경쟁을 SQLite/PostgreSQL에서 각각 두 프로세스로 재현했다. 이는 설계 근거이며
Go 구현 성공이나 최종 기준 fixture의 완료 주장이 아니다.
`orm/manager.go`·`orm/materialize.go`의 query 평가와 양 backend의 transaction session을 확장한다.
