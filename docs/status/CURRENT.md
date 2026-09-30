# 현재 상태

- 갱신: 2026-10-01
- 현재 작업: [GDJ-0103 Formset과 범위가 정해진 여러 행 편집](../../work/0103-formsets-and-scoped-batch-editing.md)
- 최근 완료한 전체 검증: [Hosted full 36711704536](https://github.com/progresshans/godj/actions/runs/36711704536), source `bcc7b76a17aacc6a90a3f360bb8f25fe080a840d`; 필수 owner·최종 집계·새 capture/Git source 결합 완료
- Source·환경·실행/수정 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

Formset·모델 여러 행 unique·canonical InlineSpec과 조회 전용 기존 행을 [Admin inline](../../admin/inlines.md)에 연결했다.
[Helpdesk Admin](../../examples/helpdesk/README.md#admin에서-티켓과-보고서를-함께-편집하기)은 현재 권한·부모/cohort·revision을 확인하고
scalar/collection·삭제·audit를 같은 transaction에 저장한다. 동적 미저장 행과 서버 prototype도 입력값·오류·identity를 보존한다.
[Formset 결정](../adr/0081-formset-counts-and-row-ownership.md)을 따른다.

[파일 입력](../../uploads/README.md)은 bounded multipart·메모리/임시 파일의 요청 수명을 소유한다. 모델 File/ImageField는
Schema IR·생성 모델·typed/dynamic ORM·양 DB migration·Form/Admin으로 연결하며 저장 이름과 업로드를 구분한다.
[명시적 SaveFiles와 여러 행 저장](../../forms/model/README.md)은 부분 게시 결과와 DB rollback을 보존한다.

[Filesystem/Memory/S3 storage](../../storage/README.md)는 독립 reader·완성 후 게시·자원 한도를 제공한다. Alias/URL과
[인가된 파일 응답](../../web/streaming.md)은 같은 열린 handle에서 conditional/Range/HEAD·유한 전송·정리/실패를 처리한다.
[이미지 내용 검증](../../uploads/README.md#이미지-내용-검증)은 PNG/APNG/JPEG/GIF/WebP/BMP/DIB/TIFF·context·자원 한도를 적용한다.
[모델 이미지](../../forms/model/README.md#모델-이미지와-크기-필드)의 크기는 검사한 업로드에서 파생하며 독립 Form/JSON 입력으로 받지 않는다.

[저장된 이미지 검사/크기 갱신](../../storage/model/README.md)은 명시적으로 선택한 backend의 독립 handle을 읽고 닫은 뒤
canonical 크기만 바꾼 분리된 모델을 반환한다. 원본 모델/파일을 보존하며 DB 저장은 별도다. Native 비교·실제 양 DB/양 backend의
저장/rollback/재개방·관련 race와 부정 대조를 확인했다. BMP/DIB·여러 페이지 TIFF와 APNG/WebP도 같은 검사기와
Form/Admin·typed 저장/재검사에 연결했다. APNG는 별도 기본 이미지, WebP는 프레임별 실제 크기/alpha를 포함해
모든 frame의 내용·container·합산 예산을 검사한다.
이 이미지 변경들은 위 Hosted source에서 전체 통합했다.
장기 의미는 [파일 결정](../adr/0082-file-storage-publication-and-reference.md), 환경별 증거는 TEST_EVIDENCE를 따른다.

S3의 conditional PUT·전체 checksum·불확실한 결과 보존과 version 고정 읽기를 연결했다. 명시적 서명 URL은
현재 모델 소유권을 확인한 뒤 발급한다. 실제 MinIO·Form/Admin·양 DB의 생성 소비자와 관련 race를 검증했다.
새 dependency와 service profile은 source `ac4c40d5`의
[Hosted web 36738621534](https://github.com/progresshans/godj/actions/runs/36738621534)에서 필수 owner·최종 집계와
세 모드 service 실행·종료/source 결합까지 확인했다. 전체 플랫폼 성공과는 구분한다.

저장 이름을 선택하는 [File/ImageField choices](../../forms/model/README.md#저장-이름-선택)를 IR·Form·Admin과 명시적 이미지
검사에 연결했다. Native 비교·양 DB/세 backend의 생성 소비자·관련 race와 실패 대조를 확인했다.
Source `e824fdd7`의 [Hosted web](https://github.com/progresshans/godj/actions/runs/36744929499)에서 필수 owner·집계와
세 모드 service receipt를 source에 결합했다. 전체 플랫폼 검증과는 구분한다.

BigTIFF의 양 byte order·64-bit IFD/값 배열을 공통 검사기에 연결했다. 모든 주 페이지와 tile/strip의 내용·합산 예산을 검사하며
원문을 보존한다. 고정 LibTIFF 비교·Form/Admin·양 DB/세 backend의 생성 소비자·관련 race/fuzz와 실패 대조를 확인했다.
이 변경은 위 Hosted source 이후의 별도 영향 검증이며 새로운 Hosted 결과는 아직 포함하지 않는다.

## 다음 행동

BigTIFF 변경 source의 Hosted web 범위를 확인한다.
이어서 남은 codec 특성·storage provider를 의존 순서에 따라 구현한다.
새 파일 게시와 DB commit은 별도 결과이며,
불확실한 결과를 자동 재시도하거나 참조 문자열만으로 보상 삭제하지 않는다.
Credential/session의 별도 저장 의미를 유지하며 custom user model·인증/mail provider와 기능 카탈로그의 남은 범위도 구현한다.

기본 공유 Go cache·병렬 실행과 생성 소비자의 `-trimpath`를 유지한다. 편집 완료 후 영향 검사를 모으고 DB/race는 통합
checkpoint에서 실행한다. 전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다. [검증 문서](../TESTING.md)를 따른다.

장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이다. 출시 일정 없이 필요한 기반과
기능을 이어가며 한 작업의 완료를 전체 프레임워크 완료로 합치지 않는다.
