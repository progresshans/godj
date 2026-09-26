---
id: GDJ-0080
status: complete
updated: 2026-09-19
baseline_commit: "408c4179d52c5ea8925f503a000ef69d9f892289"
integration_owner: "root"
---

# Eager materialization과 여러 filter JOIN의 조합

## 목표

한 관계를 select-related로 읽으면서 다른 forward/reverse 관계의 조건으로 조회하는 흐름을 지원한다.
작업 시작 시 Count는 projection을 제외해 실행할 수 있지만 같은 조건의 All은 서로 다른 eager/filter edge를 거부했다.
이를 source row·multiplicity·projection alias·nullable cache의 의미를 보존하면서 실제 materialization까지 연결한다.
헌장·기능 카탈로그의 전체 구현을 이어가는 관계 기반 작업이며 전체 관계 탐색 완료를 뜻하지 않는다.

## 설계와 구현 경계

Django 고정 버전의 실제 All/First/Count와 SQL·query 수를 먼저 관찰한다. Required/nullable selected edge,
다른 forward edge의 AND/OR/NOT, reverse filter의 중복 행, Distinct·slice와 empty query를 함께 확인한다.
하나의 selected projection과 filter JOIN의 구성을 다룬다. 여러 target projection·nested traversal·reverse OR/NOT은 별도 범위다.

공통 join inventory의 기존 제한을 없애는 것만으로 완료하지 않는다. Projection이 참조하는 alias와 scanner column 순서,
source FK/target identity·nullability·row integrity, 반환 객체의 cache 분리, 실패·취소·Rows.Close를 함께 검증한다.
현재 wrong-root/conflicting-metadata와 unsupported query의 pre-I/O 거부를 보존한다.

## 검증 소유자

제품·생성기·실제 generated consumer와 양 DB의 실패 경로를 먼저 한 묶음으로 구현한다. 편집 중에는 compile만 확인한다.
완성 뒤 affected normal·race·CGO0, Django reference와 generated drift를 확인한다.
GDJ-0079 scalar lookup과 이번 materialization 조합을 통합한 정확한 source에서 Hosted ORM checkpoint를 실행한다.
이전 GDJ-0078 Hosted 또는 다른 source의 full을 이번 변경의 PASS로 재사용하지 않는다. 실행 상세는 TEST_EVIDENCE 한 곳에 기록한다.

## 진행

공통 JOIN inventory의 조합·provenance 검증, LIMIT 0의 empty-source 처리, 실제 양 DB·독립 generated consumer를 구현했다.
필수 로컬 normal·race·CGO0·Django reference·drift 검증을 완료했다. 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 둔다.
Self-reference의 forward/reverse view도 같은 root FK 선언으로 대조하도록 보완했다. 정상 self-reference와 추가 충돌 입력을 포함한
최종 source의 로컬 세 mode를 다시 통과했다. 초기 통합 Hosted와 구분해 보완 source의 [Hosted ORM checkpoint](https://github.com/progresshans/godj/actions/runs/35414363995)도 완료했다.
