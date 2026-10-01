---
id: GDJ-0107
status: done
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
- [x] 완성된 변경 묶음의 영향 normal/race/CGO=0·양 DB·실제 경쟁·실패 대조와 필요한 생성 drift
- [x] transaction 기반 변경의 source를 고정한 Hosted 통합과 현행 기록

기본 동작의 설계는 [ADR-0086](../docs/adr/0086-single-object-creation-and-savepoint-ownership.md)을 따른다.
실행 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록하며, local 전체와 Hosted 전체를 중복하지 않는다.
편집 중에는 필요한 compile만 확인하고 완성된 제품·생성·소비자·테스트 묶음에서 영향 검증을 수행한다.

## 완료 상태

Get/GetOrCreate의 fresh 판정·원 metadata snapshot·지연 입력과 실제 unique rollback 복구를 일반/eager/prefetch
query와 독립 생성 module에 연결했다. 양 DB savepoint의 중첩 session·cursor·scope 종료·실패/unknown outcome을
검증하고 고정 Django의 별도 프로세스 관찰 및 생성 소비자 대조를 완료했다. Helpdesk Label 확보를 현재 권한·
Category 범위·부모 transaction audit와 Form/Admin/API·독립 client에 연결하고 영향 세 mode·실제 브라우저를 확인했다.

고정 source `8f8831ac8231a9c32049b6649e49f8638edd5117`의
[Hosted full 36866445270](https://github.com/progresshans/godj/actions/runs/36866445270)은
65개 job / 8개 필수 owner·최종 집계와 두 새 capture의 Git source 결합·실제 소비까지 완료했다.
PostgreSQL core 세 mode는 각각 4,630 run/pass·0 skip과 실제 S3 서비스 수명을 확인했다.
macOS Intel/race는 같은 소비자 전체를 세 shard로 나누고 일반 runtime을 분리해 누적 timeout을 해소했다.
필수 목록 누락·기존 수명 대조·timeout 실패 및 source별 보정/검증은
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md#gdj-0107--단건-조회와-savepoint-기반-조회-후-생성)에 보존했다.

후속 행 잠금·update-or-create와 카탈로그의 다른 기능은 별도 구현·검증을 이어간다.
헌장과 전체 기능 카탈로그의 완료를 이 작업의 완료로 대신하지 않는다.
