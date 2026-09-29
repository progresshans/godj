# ADR-0082: 파일 저장 이름과 게시 결과

- 상태: Accepted
- 날짜: 2026-09-29
- 관련 작업: [GDJ-0103](../../work/0103-formsets-and-scoped-batch-editing.md)

업로드 capability는 요청이 끝나면 만료된다. 이를 모델의 영구 참조로 보관하거나 파일 저장과 DB commit을 하나의 성공으로
합치면 수명 종료·부분 실패에서 잘못된 이름과 파일이 남는다. 모델의 영구 값은 저장소 상대 이름이며 실제 I/O는 명시적인
storage backend의 context/error 경계가 소유한다. Template/Form의 순수 검증은 I/O 권한을 얻지 않는다.

저장소는 실제 저장한 이름/크기를 반환하고 이름이나 metadata의 구성 자체는 접근 권한이 아니다. 기본 로컬 backend는
application 소유 root handle과 예약 staging namespace를 사용한다. 불완전한 파일을 공개 이름에 직접 쓰거나 충돌한
파일을 truncate하는 방식 대신 완성/Sync/Close한 내용을 no-replace hard link로 게시한다. Collision rename은 source를
다시 읽지 않으며 이름/내용/시도 한도를 적용한다. 예약 디렉터리와 사용자 파일을 구분하고 자동으로 외부 파일을 삭제하지 않는다.

게시 전 실패·게시 확인 후 cleanup 실패·결과 불확실을 구분한다. Link의 오류는 해당 filesystem의 결과를 단정하지 않고 후보
이름과 Uncertain을 보존한다. DB transaction은 별도이며 확인되지 않은 결과를 자동 재시도하거나 보상 삭제하지 않는다.
파일 Sync와 원자적 이름 게시는 directory entry의 power-loss durability 또는 DB/파일 분산 transaction의 증명이 아니다.

현재 [storage 계약](../../storage/README.md)은 로컬 저장·요청 업로드 소비까지 구현한다. Schema IR의 FileField, 생성 모델과
폼 저장의 reference 연결 및 DB 결과 조정은 후속 구현이다. 이 결정의 Accepted를 전체 파일 기능 완료로 사용하지 않는다.
Django 6.1의 기본 이름/내용/no-overwrite 의미를 참조하며 portable 이름 제한·bounded 실행·게시 전 완성은 명시적 차이다.
