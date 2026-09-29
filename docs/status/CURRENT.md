# 현재 상태

- 갱신: 2026-09-29
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36526909898](https://github.com/progresshans/godj/actions/runs/36526909898), source `6d3fe97f3e6cbb2103c72122c2dd25cceed97d9f`; Admin 합성 저장까지 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합 완료
- Source·환경·scope·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset·모델 여러 행 unique·canonical InlineSpec·조회 전용 기존 행을 기반으로 [Admin inline](../../admin/inlines.md)의
권한별 HTML 표시와 부모·자식 합성 저장 callback을 연결했다. 기존 행의 일반 입력과 PK/DELETE·추가 행을 구분하고,
서버 부모/cohort·binding owner·optional child revision과 오류 재표시를 유지한다.

[Helpdesk Admin](../../examples/helpdesk/README.md#admin에서-티켓과-보고서를-함께-편집하기)은 explicit transactional audit를 받아
새 티켓/보고서 생성과 수정·삭제·감사 기록을 하나의 transaction에서 수행한다. 실제 HTML 성공 제어의 재제출, child-only/무변경,
readonly/Add-only/deny overlay, 위조·DB unique·늦은 scope/쓰기/audit 실패·rollback/unknown outcome을 검증한다.
구현/실행 상세와 실패 후 수정은 [TEST_EVIDENCE](TEST_EVIDENCE.md), 설계는 [ADR-0081](../adr/0081-formset-counts-and-row-ownership.md)에 둔다.

서버가 만든 빈 행과 외부 script로 미저장 inline 행의 동적 추가/제거를 연결했다. 권한별 prototype·min/max·입력값과
오류 위치·기존 identity를 보존하며 실제 브라우저의 두 inline·새 부모/자식 SQLite 저장과 관련 양 DB/race를 확인했다.

[파일 입력](../../uploads/README.md)의 bounded multipart·공유 메모리 예산/임시 파일·요청 수명과 Form/FileField·Formset·typed
파일 명령을 연결했다. 기존 파일 유지·교체·clear 충돌, 실제 HTTP 입력과 종료/오류/panic·취소 정리를 검증했다. 모델 FileField와
영구 저장 결과를 의미하지 않는다. Admin의 부모·inline·명령 폼도 파일 widget/multipart·파일 전달·오류 재표시를 연결했다.
본문 전 읽기 전용 admission과 CSRF/최종 권한 검사를 유지하고 임시 자원 정리를 확인했다. 위 Hosted full은 이후 동적 UI·파일
입력/Admin 전송을 포함하지 않으며 영향 검증을 전체 성공으로 확대하지 않는다.

## 다음 행동

Schema IR의 모델 FileField·영구 storage, DB/파일 저장 결과의 연계를 구현한다. 일반 ModelFormSet
자동화와 후속 통합도 남아 있다. 파일 입력 capability의 수명을 영구 저장이나 권한으로 해석하지 않는다.
Credential/session의 별도 저장 의미와 일반 typed 준비의 책임을 구분하며 custom user model·인증/mail provider와
기능 카탈로그의 남은 범위도 계속 구현한다.

생성 소비자의 `-trimpath`와 기본 공유 cache·병렬 실행을 유지한다. 성공한 영향 검사와 무관한 전체 compile을 덧붙이지 않고,
로컬 전체와 Hosted 전체를 관성적으로 중복하지 않는다. 실행 규칙은 [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다.
출시 일정 없이 필요한 기반과 기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
