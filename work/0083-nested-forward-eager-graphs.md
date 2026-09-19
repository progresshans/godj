---
id: GDJ-0083
status: active
updated: 2026-09-19
baseline_commit: "3ab0a7dd97d6a29c56b7f75f07b7533a44e9bfc0"
integration_owner: "root"
---

# 중첩 forward eager 조회와 cache

## 목표

게시물의 작성자·팀·조직처럼 여러 단계의 관계를 한 SQL로 읽고, 반환 객체에서 하위 관계를 따라갈 때 이미 읽은 값을
재사용한다. GDJ-0082의 immutable route/alias 기반을 projection과 typed scan/cache까지 연결한다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 범위를 정하는 작업이 아니다.

## 구현 경계

- 명시적으로 선택한 forward path의 prefix를 정규화하고 공통 prefix projection은 한 번만 만든다. 서로 다른 route의 동일
  model/FK/PK와 유한한 self-cycle occurrence는 구분한다. Filter와 selection은 같은 compiler JOIN inventory를 사용한다.
- 각 target은 concrete Go type을 유지하는 닫힌 adapter로 scan·clone한다. 한 Rows.Scan 뒤 source와 모든 descendant의
  PK/FK·presence·NULL을 parent 순서대로 검증한다. 없는 nullable ancestor 아래 present/partial child는 실패다.
- Context·Rows.Err/Close·모든 node 검증을 마친 전체 결과만 cache한다. 실패 후 재시도, query/Fresh/파생 state와
  서로 다른 row·반환 호출의 독립 clone을 보존한다. 공유 prefix의 하위 관계 cache도 일반 사용자 facade에서 재사용해야 한다.
- Generated typed/dynamic selection은 같은 유한한 route를 사용한다. 경로 조합마다 타입을 생성하거나 app import cycle을
  만들지 않는다. 구체 API는 기존 object/facade/scan 경계에서 정하며 현재 내부 ABI는 필요하면 정리한다.
- Count는 전체 selection/configuration 검증을 유지한 채 projection을 제외한다. Direct eager/filter·Boolean·reverse 중복·
  Distinct·Offset/Limit·empty·취소를 회귀 검증한다. Resource 한계는 SQL과 I/O 전에 검사한다.
- Reverse/ManyToMany eager, 임의 object cycle identity 공유와 일반 self/cyclic migration은 이 완료 주장에 포함하지 않는다.
  후속 요구로 유지하며 이번 query fixture를 migration 지원 증거로 사용하지 않는다.

## 현재와 다음

GDJ-0082의 normal/race/CGO0 양 DB·생성 소비자 검증을 완료했고 baseline의 Hosted ORM은 별도 진행 중이다.
Django 6.1 fresh process에서 명시적 path 집합 8개의 독립 관찰 440개를 준비했다. 현재는 scratch 연구이며 제품/회귀
fixture로 게시하지 않았다. 각 selected prefix 접근은 원래 First/All SQL 안에 끝나고 warm 접근의 SQL은 0이었다.
이 입력을 재현 가능한 reference로 정리한 뒤 projection·runtime·generator·consumer를 한 변경 묶음으로 구현한다.
실제 검증 전에는 구현 완료나 환경별 PASS를 주장하지 않는다.
