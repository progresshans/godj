---
id: GDJ-0112
status: active
updated: 2026-10-06
baseline_commit: "60c6395ff95896591bc8be1d6b898e1e8ad198c6"
integration_owner: "root"
---

# 그룹 집계와 티켓 업무 요약

## 결과와 범위

현재 QuerySet의 행을 명시한 키로 묶고, 그룹별 집계·HAVING·정렬·페이지를 DB에서 실행한다.
Typed와 dynamic 입력은 같은 불변 Query AST와 metadata 검사를 사용한다. 그룹 결과를 모델 행으로
위장하거나 전체 원본 행을 애플리케이션으로 가져와 집계하지 않는다.

실제 소비자는 현재 Category의 티켓을 우선순위별로 보는 업무 요약이다. 전체·열린 티켓 수와 최소 열린
건수 조건, 결정적인 정렬·페이지를 HTML/API/독립 생성 client에 연결한다. 미설정 우선순위와 기존 선택지
밖의 값도 실제 그룹으로 유지하며, 현재 ViewTicket 권한과 Category 범위를 입력 해석·조회에 적용한다.
조회는 저장·audit를 발생시키지 않는다. 요약 값과 페이지 건수의 관찰 범위·일관성을 명시한다.

GDJ-0109/0110/0111의 통합 source는 별도 [Hosted full 37359348832](https://github.com/progresshans/godj/actions/runs/37359348832)에서
검증 중이다. 이 작업은 그 source에 없는 후속 구현이며 선행 실행의 성공을 이 기능의 증거로 사용하지 않는다.

## 구현과 검증

- [ ] 고정 Django의 NULL·복합/관계 키·조건부/중복 제거 집계·HAVING·정렬·slice/cache를 독립 관찰
- [ ] 그룹 결과·집계 참조·HAVING·정렬의 공통 AST, metadata/type 소유권·자원 한도·명시적 미지원 오류
- [ ] SQLite/PostgreSQL native 실행, 실제 그룹 수·NULL·관계 cardinality와 context/session 수명
- [ ] Generic ORM·typed 생성 facade·dynamic 입력과 독립 생성 소비자, 잘못된 연결의 사전 거부
- [ ] Helpdesk의 현재 권한·범위를 보존하는 HTML/API/독립 client 업무 요약
- [ ] 완성된 묶음의 기준 대조·영향 검사·generated drift·필요한 통합과 현행 의미/증거 기록

## 설계와 검증 경계

그룹을 만들기 전의 WHERE와 그룹을 만든 뒤의 HAVING을 구분한다. 기존 source ordering·slice·distinct·
eager/row-lock 상태를 묵시적으로 다른 뜻으로 재사용하지 않는다. NULL 키·nullable 결과·빈 원본/빈 그룹,
count와 distinct count, 조건부 집계의 실패와 조합을 실제 기준에서 확인한 뒤 API를 확정한다.
관계 join이 원래 행을 늘리는 경우와 단일 forward 값의 선택을 구별하고, 지원하지 않는 경로는 실행 전에 거부한다.

현재 field/codec의 동등·정렬·scan 의미를 그대로 사용한다. Schema IR·generic runtime·codegen·backend의
기존 소유권을 유지하고, 임의 SQL 문자열·가짜 model field·사용자가 조작 가능한 SQL alias로 경계를 우회하지 않는다.
Query 복사, 입력과 출력의 가변 값, cache 공유·취소·rows/transaction 종료도 명시한다.

한 묶음의 제품·생성기·테스트를 먼저 작성하고 compile 확인 뒤 영향 검증을 모아 실행한다. Core와 실제
소비자를 양 DB에서 normal/race/CGO=0으로 확인하며, generated drift와 필수 실행 누락 검사를 포함한다.
전체/cold/Hosted 검증은 통합 milestone에서 소유하고 기존 전체 실행과 관성적으로 중복하지 않는다.
일반 annotation·계산식·SUM/AVG·subquery/window/function 등 아직 완성하지 않은 범위는 카탈로그에 남긴다.

[개발 판단 기준](../docs/DEVELOPMENT_CRITERIA.md), [기능 카탈로그](../docs/CAPABILITY_CATALOG.md),
[검증 전략](../docs/TESTING.md)을 따른다. 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에만 기록한다.
현재 외부 입력이 필요한 blocker는 없다.
