---
id: GDJ-0110
status: complete
updated: 2026-10-06
baseline_commit: "d2d8518275eae9e7f46a836d0797f1f9802561c9"
integration_owner: "root"
---

# Native bulk update와 여러 티켓 수정

## 결과와 범위

여러 기존 모델의 명시적으로 선택한 저장 필드를 native batch UPDATE로 수정한다. QuerySet의 현재 필터와
모델/field 소유권, primary-key 존재 상태, 실제 affected count와 하나의 transaction/savepoint를 유지한다.
단건 Save를 반복하는 구현이나 실제 수정 대상을 읽기 조건보다 넓히는 변환을 bulk update로 제공하지 않는다.
Helpdesk의 여러 티켓 편집, Admin 선택 작업과 API의 일괄 수정에서 현재 권한·Category·라벨·고유성·audit를 연결한다.

고정 Django의 40개 결과와 PostgreSQL 잠금 대기 7개를 독립 관찰하고 AST/compiler·native write scope,
generic ORM·생성 facade와 실제 소비자를 양 DB 영향 세 mode에서 대조했다. 업무 표면과 독립 client,
실제 브라우저를 확인했다. Hosted에서 발견한 Article 알림 회귀와 누적 race 실행/fixture 준비 한도를 보완했고
기존 기준과 필수 실행을 유지했다.

Source `720c9be6211f00a146a39d00957a81ce69ed294f`의
[Hosted full 37365281161](https://github.com/progresshans/godj/actions/runs/37365281161)에서 GDJ-0109의 추가
PostgreSQL 회귀와 GDJ-0111을 포함해 전체 통합을 완료했다. 원 로그·필수 owner와 경로·capture/source 결합·
실제 소비·최종 집계를 확인했다. 앞선 실패와 상세 실행은 TEST_EVIDENCE에 남기며 후속 source로 전이하지 않는다.

## 구현과 검증

- [x] 고정 Django의 selected fields·필터·중복/없는 key·batch·실패/부모 의미를 양 DB에서 독립 관찰
- [x] 불변 bulk update AST와 전체 field/row/parameter 예산·실제 native SQLite/PostgreSQL statement
- [x] shared ORM의 전체 입력 검증·scope/실패/결과 소유권과 typed 생성 root facade
- [x] 생성 소비자의 여러 모델/scalar·동적 field 선택·관계/필터·잘못된 타입·실제 DB/취소/경쟁과 기준 대조
- [x] Helpdesk 여러 티켓 편집·Admin 선택 작업·API/독립 client의 같은 transaction과 보안/무결성/실패 경계
- [x] 완성된 변경 묶음의 영향 검사·생성 drift·필요한 통합 범위와 현행 문서 기록

## 판단할 의미

고정 Django는 query filter를 유지하고, 같은 batch 안의 중복 key는 첫 값을 적용하며 다른 batch의 같은 key는
다시 수정될 수 있다. Missing key와 중복은 실제 affected count를 줄일 수 있다. 이 동작을 실제 native 관찰로
확정하며 application이 요구하는 전부 존재/유일한 입력 정책과 구별한다. 원래 모델·읽기 cache를 자동으로 수정하거나
없는 행을 생성하지 않는다. Primary key·columnless relation을 update field로 허용하지 않는다.

값·필터는 같은 DB 독립 AST로 전달하고 SQL 식별자/field codec/parameter 수·statement syntax는 compiler가 소유한다.
Read ordering·slice·lock·eager 설정이 쓰기에 미치는 의미를 조사해 명시하며 조건을 조용히 누락하지 않는다.
전체 입력 준비, 뒤쪽 batch 실패·borrowed parent·cleanup/commit unknown·취소의 정책은 native bulk 생성의
기존 보수적 소유권과 정합성을 유지한다. ORM bulk는 model save/clean 또는 audit를 자동 호출하지 않는다.

이 작업의 출발점은 Go typed 모델 값과 selected-field mask다. Django의 객체 필드에 임의 expression을 넣는
Python 표현 방식을 Go 모델에 복제하지 않는다. 일반 writable F/연산 expression·QuerySet update는 공통 AST의
후속 기반으로 계속 남으며 카탈로그에서 제외하거나 이 작업의 성공으로 완료 처리하지 않는다.

장기 의미는 [ADR-0089](../docs/adr/0089-bulk-update-and-selected-field-ownership.md)에 기록한다. 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가
소유한다. 현재 외부 입력이 필요한 blocker는 없다.
