# 현재 상태

- 갱신: 2026-09-30
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36666773815](https://github.com/progresshans/godj/actions/runs/36666773815), source `365ad9d4bb94f049f692ef7722b2684a6f48f379`; alias/streaming까지 62 jobs·필수 8 owners·최종 집계와 새 capture/Git source 결합 완료
- Source·환경·scope·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset·모델 여러 행 unique·canonical InlineSpec·조회 전용 기존 행을 기반으로 [Admin inline](../../admin/inlines.md)의
권한별 HTML과 부모·자식 합성 저장을 연결했다. [Helpdesk Admin](../../examples/helpdesk/README.md#admin에서-티켓과-보고서를-함께-편집하기)은
부모/cohort·권한·revision을 다시 확인하고 scalar/collection·삭제·audit를 같은 transaction에서 저장한다. 서버가 만든
prototype과 동적 미저장 행 추가/제거도 입력값·오류 위치·기존 identity를 보존한다. [결정](../adr/0081-formset-counts-and-row-ownership.md)을 따른다.

[파일 입력](../../uploads/README.md)은 bounded multipart·메모리/임시 파일과 요청 수명을 소유한다. Form/Formset과 Admin
부모·inline·명령은 기존 이름 유지·교체·clear 충돌, CSRF/현재 권한과 실패 재표시·파일 재선택을 구분한다.
[로컬 storage](../../storage/README.md)는 완성한 파일을 기존 이름을 대체하지 않고 게시하며 독립 reader와 게시/정리/불확실한 결과를 제공한다.

[모델 FileField](../../forms/model/README.md#모델-파일의-준비와-저장)를 Schema IR·생성 모델·typed/dynamic ORM·양 DB migration과
Form/Admin에 연결했다. 모델 값은 저장 이름이고 새 업로드는 별도다. 미저장 업로드가 남으면 typed 모델/DB 저장을 거부하며
명시적 `SaveFiles`가 실제 저장 이름을 반영한다. 부분 게시 결과를 보존하고 DB rollback·clear·모델 삭제가 파일을 자동 삭제하지 않는다.
고정 Django 관찰, 실제 Admin multipart, 생성 소비자의 SQLite/PostgreSQL 저장·재개방과 관련 race를 확인했다.
[파일 결정](../adr/0082-file-storage-publication-and-reference.md)과 실행 증거를 따른다.

일반 [모델 여러 행 저장](../../forms/model/README.md#여러-행의-저장과-지연-저장)은 변경·추가·삭제의 실행 순서와 mutable 저장 계획,
지연 컬렉션 저장·행별 실패 결과를 연결했다. 여러 행 파일의 사전 검사/부분 게시 결과를 보존하며 Helpdesk Admin 보고서는
기존 인가·고유값·감사 기록을 같은 계획 안에 연결한다. 고정 Django 관찰과 실제 양 DB·관련 race를 확인했다.

[Storage alias/URL](../../storage/README.md)과 [인가된 파일 응답](../../web/streaming.md)을 연결했다. 전체 middleware 성공 뒤
독립 reader를 열고 Request/upload 수명을 분리한다. 같은 열린 handle의 metadata·유한 전송/EOF·HEAD·정리·실패 시 중단과
실제 로그인/CSRF·모델 소유권·양 DB 다운로드 및 기존 Helpdesk HTTP의 관련 race를 확인했다.

동적 UI·파일 입력·storage·모델 FileField·일반 모델 여러 행 저장·alias/streaming과 storage 난수 callback의 panic 후 잠금
수정을 위 Hosted full 통합 milestone에서 확인했다. 수정 전 취소한 실행은 완료 증거로 쓰지 않는다.

메모리 backend의 독립 파일/reader와 완성 후 게시·용량·동시 저장 제한을 구현했다. 삭제 후 열린 reader가 보유한 내용도
한도에 포함하며 실패/취소/panic의 예약 정리를 확인했다. 같은 양 DB 인증·파일 소비자의 filesystem/memory 조합과 관련
normal/race·부정 대조를 검증했다. 이 변경은 위 Hosted full source에 포함되지 않으며 영향 검증의 범위는 TEST_EVIDENCE를 따른다.

파일 Range·conditional 응답을 같은 열린 handle의 metadata와 seek capability에 연결했다. 파일 버전/수정 시각·조건 우선순위·
단일/여러 범위·HEAD·본문 없는 응답과 중간 실패를 구현하고 실제 양 DB/양 backend의 인증된 소비자·영향 normal/race로 검증했다.
이 변경도 위 Hosted full에는 포함되지 않는다. [파일 응답 계약](../../web/streaming.md)과 별도 영향 증거를 따른다.

## 다음 행동

ImageField의 내용 검증·모델/폼·저장 의미를 연결한다. 추가 storage provider와 나머지 파일 의미도 의존 순서에 따라 구현한다.
새 파일 게시와 DB commit은 별도 결과이며, 불확실한 결과를 자동 재시도하거나 참조 문자열만으로 보상 삭제하지 않는다.
Credential/session의 별도 저장 의미를 유지하며 custom user model·인증/mail provider와 기능 카탈로그의 남은 범위도 구현한다.

기본 공유 Go cache·병렬 실행과 생성 소비자의 `-trimpath`를 유지한다. 편집 완료 후 영향 검사를 모으고 DB/race는 통합
checkpoint에서 실행한다. 전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과
기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
