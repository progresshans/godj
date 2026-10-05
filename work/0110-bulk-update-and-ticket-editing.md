---
id: GDJ-0110
status: active
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

GDJ-0109는 고정 source의 Hosted 선언 범위를 통과했으며 사후 발견한 PostgreSQL 회귀의 실행 담당을 보완한다.
이 작업의 새 source와 선행 결과를 섞지 않으며 다음 full milestone이 두 작업의 통합을 소유한다.
고정 Django의 40개 결과와 PostgreSQL 잠금 대기 7개를 독립 관찰했다. AST/compiler·native write scope 기반은
실제 SQLite/PostgreSQL의 영향 세 mode를 통과했다. Generic ORM·생성 root/eager/prefetch facade와 실제 소비자도
영향 세 mode를 통과했다. 정식 40개 기준과 일곱 잠금 대기 대조도 세 mode를 통과했다. 업무 표면과 독립 client도
양 DB 영향 세 mode·실제 브라우저를 통과했으며 새 source의 Hosted 통합이 남았다.

Source `99491ff36c0eceeed6d1cec6eb74e7c5b2f49afc`를 게시하고
[Hosted full 37329368105](https://github.com/progresshans/godj/actions/runs/37329368105)을 시작했다.
이 실행이 GDJ-0109의 추가 회귀와 새 제품 source의 전체 통합을 소유한다. 완료 전까지 마지막 항목은 열린 상태다.
Article의 발행 알림까지 공통 문구로 바꾼 회귀와 Helpdesk 누적 race 실행의 시간 한도 부족을 발견했다.
앱이 액션별 성공 문구를 선언하고 model/action/count를 서명하도록 수정했고 영향 세 mode·브라우저를 통과했다.
Linux 생성 소비자의 누적 race timeout도 확인해 모든 race 좌표의 소비자를 분할하고 실행 소유권을 검증했다.
고정 Django expected/observer는 바꾸지 않았다. 수정 source `19dd8178ae6f4a3d853ab6879543efa333cffb8a`의
[Hosted full 37335450948](https://github.com/progresshans/godj/actions/runs/37335450948)을 요청했다.
이 실행도 PostgreSQL race/core의 누적 job 제한으로 취소가 발생했다. 개별 검사를 유지하는 race 분할과 원 로그 보관을
적용했으며 해당 CI source로 전체 검증을 다시 수행해야 한다.
이전 실패 실행의 부분 성공을 재사용하지 않고 새 source의 최종 통합을 확인한다.

## 구현과 검증

- [x] 고정 Django의 selected fields·필터·중복/없는 key·batch·실패/부모 의미를 양 DB에서 독립 관찰
- [x] 불변 bulk update AST와 전체 field/row/parameter 예산·실제 native SQLite/PostgreSQL statement
- [x] shared ORM의 전체 입력 검증·scope/실패/결과 소유권과 typed 생성 root facade
- [x] 생성 소비자의 여러 모델/scalar·동적 field 선택·관계/필터·잘못된 타입·실제 DB/취소/경쟁과 기준 대조
- [x] Helpdesk 여러 티켓 편집·Admin 선택 작업·API/독립 client의 같은 transaction과 보안/무결성/실패 경계
- [ ] 완성된 변경 묶음의 영향 검사·생성 drift·필요한 통합 범위와 현행 문서 기록

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
