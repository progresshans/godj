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

- [x] 고정 Django의 단건 조회·cache·기본값·savepoint·실제 경쟁을 양 DB에서 독립 관찰하고 기준 fixture로 관리
- [x] 정확한 단건 판정과 fresh 평가·원 query cache 보존·typed/dynamic 및 관계 소비자
- [x] SQLite/PostgreSQL session의 savepoint·중첩 scope·cursor 정리·실패와 부모 commit 차단
- [x] 생성 입력의 지연 평가·원 Manager snapshot·unique 충돌 후 한 번의 조회와 명시적 미지원 capability
- [x] Helpdesk의 현재 권한·category 범위·Label 확보·원자 audit와 Form/Admin/API·독립 client 흐름
- [ ] 완성된 변경 묶음의 영향 normal/race/CGO=0·양 DB·실제 경쟁·실패 대조와 필요한 생성 drift
- [ ] transaction 기반 변경의 source를 고정한 Hosted 통합과 현행 기록

기본 동작의 설계는 [ADR-0086](../docs/adr/0086-single-object-creation-and-savepoint-ownership.md)을 따른다.
실행 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록하며, local 전체와 Hosted 전체를 중복하지 않는다.
편집 중에는 필요한 compile만 확인하고 완성된 제품·생성·소비자·테스트 묶음에서 영향 검증을 수행한다.

## 현재 상태와 다음 행동

고정 Django 6.1/Python 3.14.3의 단건 조회 11개, 기본 생성·중첩 transaction 9개와
두 연결의 unique 경쟁을 최종 observer/fixture로 관리한다. SQLite/PostgreSQL 각각 두 독립 프로세스의
byte 일치와 observer source 결합을 확인했고, 별도 생성 Go module의 세 mode 대조를 완료했다.
`db.WithSavepoint`와 양 backend의 일반·조정·관계 session, root batch의 pinned transaction에 중첩 scope를 연결했다.
이 기반의 영향 검증과 실패 수정은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0107--단건-조회와-savepoint-기반-조회-후-생성)에 기록했다.
`Get`·`GetOrCreate`와 일반/eager/prefetch 생성 소비자를 연결했다. 원 Manager와 BoundModel의 준비된 metadata를
파생 query가 보존하며 입력은 실제 부재를 확인한 뒤 transaction/savepoint 안에서 평가한다.
양 DB의 실제 unique 경쟁·부모 rollback·생성 객체의 관계 수명과 callback/unknown outcome 실패 경계를 검증했다.
Helpdesk의 현재 권한·Label 확보·원자 audit와 Form/Admin/API·독립 client를 연결했다.
공통 Admin 목록 command, Helpdesk 실제 양 DB, OpenAPI와 독립 client의 세 mode 영향 검증 및 실제 브라우저 확인을 완료했다.
범위별 source와 실행은 검증 기록에서 구분한다. 제품 source `98aa179fc4077bdefdec9deaedbb9a7865c6e3b8`를 push하고
[첫 Hosted full](https://github.com/progresshans/godj/actions/runs/36826788120)을 시작했으나 새 PostgreSQL 실행 목록 누락을 발견해 취소했다.
Relation/PostgreSQL 필수 목록을 보완했고 CI 도구 45개 검사와 기존 세 mode 로그의 실제 이름 대조를 통과했다.
보완 source `d9f2324e`의 PR feedback 성공과 checkout/tree 결합을 확인했다. 같은 source의
[Hosted full 36828841116](https://github.com/progresshans/godj/actions/runs/36828841116)에서 기존 membership 소비자가 이전의
SQLite 수명 오류를 요구하는 것을 발견해 취소했다. 현행 공통 scope 오류 계약으로 대조하고 `Next`/`Scan`/session 거부와
세 owner child의 실행을 명시했으며, 해당 독립 생성 소비자의 normal/race/CGO=0을 확인했다. 제품 코드는 그대로다.
다시 고정한 source `39fb5ed8`의 [Hosted full 36830611179](https://github.com/progresshans/godj/actions/runs/36830611179)은
macOS Intel/race 생성 소비자의 누적 package 시간 제한으로 실패했다. 자동 소비자 분할·별도 runtime owner와
실행 누락·중복·owner 밖 package 거부를 보완했다. 로컬 실제 race 실행과 최종 분배/CI 도구의 검증 범위는 검증 기록에서 구분한다.
새 source의 전체 owner·집계·새 capture/source 결합을 확인하고 검증과 전달을 함께 닫는다.
