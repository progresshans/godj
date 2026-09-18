---
id: GDJ-0075
status: active
updated: 2026-09-19
baseline_commit: "8fd8936d634b5038a534936c15a2b1cfac4b853b"
integration_owner: "root"
---

# Scalar IN과 빈 조회의 의미

## 목표와 현재 작업

기존 내부 IN AST를 공개 typed FieldSet과 dynamic lookup에 연결해 여러 ID·문자열·정수·Boolean·시각을 한 query로 조회한다.
`feature/scalar-in-lookups`의 별도 작업 사본에서 이어간다. 기준 source까지의 Text/DateTime full은 완료했으며 이 새 변경의 PASS로 쓰지 않는다.

고정 Django 6.1의 독립 SQLite 관찰 120개를 준비했다. 빈 IN은 조회하지 않지만 nullable field의 NULL 포함 목록은
부정 문맥에서 결과가 달라진다. NULL-only 목록의 부정은 NULL 행을 제외하므로 단순히 NULL을 버리는 정규화로 이 의미를 잃지 않는다.

Typed/dynamic 연결과 empty/NULL list AST·양 SQL compiler·empty source 결과의 초기 구현을 작성했다.
아직 기능 검증 완료가 아니며 현재 compile 확인 단계다. 실제 소비자와 회귀를 완성한 뒤 관련 검증을 묶어 실행한다.

## 이어갈 범위

- 입력 slice의 snapshot과 정확한 타입·policy/error precedence, empty list의 invalid metadata 은폐 방지.
- AND/OR/NOT 및 nullable NULL membership의 같은 AST·compiler 의미.
- 전체 backend plan 검증 뒤 empty query의 SQL 실행 생략, COUNT 0·MIN/MAX NULL, cache·iterator·취소·session 경계.
- Generated consumer와 SQLite/PostgreSQL의 실제 결과·필수 실행·실제 query 수, 고정 reference 대조와 관련 race/CGO0.

Subquery·tuple/composite-key·관계 경로 IN은 별도 남은 범위다. 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md),
전체 완성 목표는 [ROADMAP](../docs/ROADMAP.md)을 따른다.
