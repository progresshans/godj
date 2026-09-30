# ADR-0082: 파일 저장 이름과 게시 결과

- 상태: Accepted
- 날짜: 2026-09-29
- 관련 작업: [GDJ-0103](../../work/0103-formsets-and-scoped-batch-editing.md)

업로드 capability는 요청이 끝나면 만료된다. 이를 모델의 영구 참조로 보관하거나 파일 저장과 DB commit을 하나의 성공으로
합치면 수명 종료·부분 실패에서 잘못된 이름과 파일이 남는다. 모델의 영구 값은 저장소 상대 이름이며 실제 I/O는 명시적인
storage backend와 업로드 검증의 context/error 경계가 소유한다. Template과 Form의 사용자 검증 callback은 I/O 권한을 얻지 않는다.

저장소는 실제 저장한 이름/크기를 반환하고 이름이나 metadata의 구성 자체는 접근 권한이 아니다. 기본 로컬 backend는
application 소유 root handle과 예약 staging namespace를 사용한다. 불완전한 파일을 공개 이름에 직접 쓰거나 충돌한
파일을 truncate하는 방식 대신 완성/Sync/Close한 내용을 no-replace hard link로 게시한다. Collision rename은 source를
다시 읽지 않으며 이름/내용/시도 한도를 적용한다. 예약 디렉터리와 사용자 파일을 구분하고 자동으로 외부 파일을 삭제하지 않는다.

게시 전 실패·게시 확인 후 cleanup 실패·결과 불확실을 구분한다. Link의 오류는 해당 filesystem의 결과를 단정하지 않고 후보
이름과 Uncertain을 보존한다. DB transaction은 별도이며 확인되지 않은 결과를 자동 재시도하거나 보상 삭제하지 않는다.
파일 Sync와 원자적 이름 게시는 directory entry의 power-loss durability 또는 DB/파일 분산 transaction의 증명이 아니다.

Schema IR의 `FieldFile`과 `FieldImage`는 기본 100 Unicode 문자의 저장 이름을 선언한다. 생성 모델은 `string` 또는 `*string`, query는
기존 DB 독립 문자열 AST, SQLite/PostgreSQL은 같은 길이의 varchar를 사용한다. Char/Email/File/Image 간 kind만 바꾸면 역사와
입력 의미는 달라지지만 같은 물리 열을 보존한다. 다른 저장 조건의 변경을 이 전환에 숨기지 않는다. ORM의 명시적인 이름
대입은 파일을 열거나 쓰지 않으며 해당 이름의 존재/권한도 증명하지 않는다.

Model Form의 `Candidate`에는 검증할 이름을, `Input`에는 유지·교체·clear와 요청 업로드 capability를 구분해 보존한다.
`ExistingFile("")`는 빈 저장 이름이며 SQL NULL과 다르다. 새 입력 생략은 기존/default를 유지하고 clear는 nullable 필드도
빈 문자열로 만든다. 기존 빈 이름은 required upload를 충족하지 않는다. Scalar PostClean은 파일 필드를 변경할 수 없으며
이름 생성은 아래 명시적인 파일 단계가 소유한다. Filename-only 후보의 사전 unique 검사가 실제 저장 후의 DB 제약을 대체하지 않는다.

Typed `Prepare`는 새 업로드를 별도로 보유한다. 미해결 업로드가 있으면 `Model`, `Save`, `SaveCollections`는 오류를 반환해
파일 입력을 버린 채 DB에 쓰는 일을 막는다. `SaveFiles`가 필드별 명시적 backend와 pure 이름 callback을 먼저 모두 확인하고,
IR 순서로 저장한 뒤 **실제로 반환된 이름**을 가진 새 준비와 필드별 `FilePublication`을 돌려준다. Caller가 그 결과로 DB scope를
실행하며 파일 게시와 DB commit의 결과를 각각 보존한다. 일부 파일만 게시되거나 게시 응답이 불확실하면 준비 결과는 무효지만
이미 시도한 모든 파일의 Info/error/outcome은 남는다. 호출은 재시도에 대해 멱등하지 않고 업로드 수명을 늘리지 않는다.

이름 callback은 다른 준비 값과 기존/default 파일 참조를 담은 분리된 모델을 받는다. Backend alias나 I/O 객체를 Schema IR에
직렬화하지 않는다. 등록되지 않은 implicit storage, 전역 설정 조회, template에서의 숨은 파일 I/O는 없다. JSON serializer의
File/ImageField와 이미지 소유 크기의 투영은 명시적인 read-only 값에 한정한다. JSON 문자열을 upload로 받거나 참조를 URL/다운로드 권한으로 바꾸지 않는다.

Application별 storage Registry는 이미 초기화한 backend를 빌리고 설정/alias 목록만 snapshot으로 소유한다. Alias는 backend를
조회하는 설정 key이며 I/O 권한이 아니다. 전역 default·lazy factory·registry의 자원 Close는 없다. URL capability는 별도로 등록하며
URL 생성과 파일의 존재·접근 인가·server route를 분리한다. Portable 이름을 URI path로 한 번 escape하며 private alias에는 URL이 없다.

인가된 FileResponse는 매 요청 현재 권한과 모델을 조회한 뒤 얻은 저장 이름을 사용한다. 전체 middleware가 성공한 이후에만
독립 reader를 열고, 동일 handle의 metadata를 사용한다. Stat→Open 경쟁·자동 MIME sniff·디렉터리 공개를 추가하지 않는다.
기본 attachment/octet-stream/no-store/nosniff를 사용하고 inline은 명시한다. Buffered 응답 한도를 늘려 파일 전체를 메모리에
올리는 대신 유한 streaming 수명과 별도 byte 한도를 연결한다. Reader는 Application이 한 번 닫으며 HTTP header 뒤 실패는
전송을 abort해 정상 완료로 위장하지 않는다. Request/upload/transaction의 빌린 수명은 streaming이 연장하지 않는다.

[storage 계약](../../storage/README.md), [파일 응답](../../web/streaming.md)과 [모델 폼](../../forms/model/README.md#모델-파일의-준비와-저장)을 따른다.
외부 object-storage backend·서명 URL provider·추가 image codec·파일 choices와 자동 orphan 회수는 미완료다. 이 결정의 Accepted를 전체 파일 기능
완료로 사용하지 않는다. Django 6.1의 기본 이름/내용/no-overwrite와 모델 파일의 생략/clear/DB rollback 의미를 참조한다.
Portable 이름 제한·bounded 실행·게시 전 완성·명시적 파일 단계는 GoDj의 차이다. Native 출처는 BSD-3-Clause
`django/db/models/fields/files.py`, 독립 관찰은 [model file observer](../../conformance/runners/django/model_file_reference.py)다.
Alias/URL/FileResponse의 [serving observer](../../conformance/runners/django/storage_serving_reference.py)는 같은 pinned source를 사용한다.
GoDj의 pure lazy response descriptor·안전한 기본 attachment/MIME·명시적 admission은 Django의 eager file 소유·자동 MIME/inline 기본과 다르다.

메모리 backend도 독립 reader와 완성 후 원자 게시를 유지한다. 프로세스 내의 비영구 저장임을 명시하고 전체 content bytes·
파일 객체·동시 Save를 제한한다. 삭제된 파일의 열린 reader도 quota에 남으며 마지막 reader close가 해제한다. Source/entropy
panic에서도 예약과 잠금을 반환한다. 이름은 파일의 가상 prefix이며 빈 directory나 재귀 삭제를 제공하지 않는다. Native
InMemoryStorage의 공유 cursor·실패 뒤 부분 파일·재귀 directory 삭제는 따라 하지 않는다. 파일 읽기 권한과 DB commit 의미는 같다.

조건부 조회·부분 전송도 인가 후 같은 열린 handle을 기준으로 한다. ContentMetadata의 Version은 정확한 bytes의 안정적 식별자이고
수정 시각의 초 단위 strong 보장은 별도다. Memory는 내용 해시를 소유하며 filesystem의 시각/크기를 strong identity로 승격하지
않는다. Seek capability가 없는 backend의 Range는 전체 전송으로 처리한다. 조건 우선순위·HEAD·본문 없는 상태·bounded multipart·
정수/요청 한도는 HTTP 의미를 따른다. 파일 전체의 메모리 복사·별도 lookup·goroutine으로 기존 수명과 실패 경계를 우회하지 않는다.
동일 이름 재사용과 동시 요청은 새 reader/metadata를 사용하며 원래 응답 설명을 바꾸지 않는다. 쓰기 precondition과 접근 정책은
실제 쓰기/인가 경계가 소유하며 응답의 conditional 판단으로 대체하지 않는다.


ImageField의 공통 입력 기반은 `Bind(ctx, ...)`가 소유한다. 업로드 capability를 독립 reader로 읽고 실제 pixel 디코딩 전에
인코딩 bytes·폭/높이·pixel 수·GIF frame 수/합계를 제한한다. 미배포 Bind API 자체에 context를 전달하며 숨은 Background
context나 별도 호환 Bind를 만들지 않는다. Form/ModelForm/Formset/Admin/인증 소비자는 같은 context 경계를 따른다.
Codec registry는 전역 mutable image 등록 상태에 의존하지 않는다. PNG/JPEG/GIF/정적 WebP의 검증 metadata는 immutable
FileValue에만 연결하며 client MIME을 덮어쓰거나 참조 이름을 자동으로 열지 않는다. 요청 파일의 수명도 늘리지 않는다.

현재 GIF는 모든 frame을 디코딩하며 APNG·애니메이션 WebP·BMP·TIFF 등 추가 codec은 명시적으로 미지원이다. Header만 읽어
검증 성공으로 처리하지 않는다. 잘못된 이미지와 실제 I/O/취소/정리 실패를 분리한다. Limit는 검사별 예산이며 전체 process
heap/동시 요청 제한을 대신하지 않는다. 검증 원문은 변환/정화하지 않고 그대로 저장한다. [이미지 계약](../../uploads/README.md#이미지-내용-검증)을 따른다.


모델 ImageField의 width/height 참조는 canonical IR이 소유한다. 같은 모델의 일반 정수 필드를 각각 한 이미지가 소유하고,
shared/잘못된 참조는 정규화에서 거부한다. 개별 migration field는 가짜 모델을 만들지 않고 로컬 의미를 정규화한 뒤 실제 모델의
historical state에서 참조를 검사한다. 추가 크기 열·이전 참조 해제는 이미지 연산보다 먼저 수행하고 참조 중인 열의 제거를 거부한다.
Wire·hash·생성 metadata·revision 의도에 참조 이름을 포함한다. 같은 저장 조건의 이미지 kind/참조 변경은 이력 의미만 바꾼다.

Form은 새 업로드의 검증 결과를 model clean 전에 candidate와 저장 Input에 반영한다. Clear는 소유 크기를 NULL로 만들며
nonnullable typed 준비에서는 I/O 전에 실패한다. 크기 필드는 폼/JSON/모델 clean의 독립 쓰기 대상이 아니고 Admin revision으로도
쓸 수 없다. Typed 파일 준비·이름 callback·게시 결과와 DB 저장은 이미 정한 파일 경계를 유지한다. 일반 ORM 이름 대입에는
이미지 검사나 크기 갱신을 숨기지 않는다. Mutable 지연 모델은 신뢰하는 application writer가 최종 값과 저장 권한을 소유한다.

Django 6.1은 폼이 기존 ImageField를 재대입하면 storage를 열어 stale 크기를 갱신한다. GoDj의 기존/제외 입력은 현재 서버
snapshot을 그대로 사용하며 별도 backend 없는 binding에 저장소 접근을 넣지 않는다. 이 차이는 [모델 이미지 관찰](../../conformance/runners/django/model_image_reference.py)에
남긴다.

저장된 참조는 명시적 `storage.InspectImage`가 한 번의 독립 Open·같은 handle metadata·bounded 전체 디코딩·Close로 검사한다.
빌린 일반 reader는 공통 `uploads.InspectImageReader`가 현재 cursor부터 읽고 수명을 변경하지 않는다. 메타데이터가 없는
storage reader는 실제 byte 수를 측정하며 별도 Stat이나 추측한 version을 사용하지 않는다. Open이 reader와 오류를 함께
돌려주거나 이후 읽기/Close/취소가 실패해도 자원을 닫고 유효한 결과를 반환하지 않는다.

`storage/model`의 typed 갱신은 canonical IR의 ImageField·크기 소유권과 현재 scalar snapshot을 확인하고 해당 크기만 바꾼
분리된 모델을 반환한다. PK·다른 필드와 원본 모델/파일을 보존하고 DB 쓰기는 실행하지 않는다. 빈/NULL 참조는 소유 크기를
NULL로 바꾸며 nonnullable 대상은 오류다. 크기 참조가 없으면 I/O 없이 복사만 한다. Caller가 fresh admission·revision·
모델 검증과 명시적 DB 저장을 수행하며 검사 결과 자체에 장래의 파일 freshness나 DB/파일 원자성을 부여하지 않는다.

Django의 같은 ImageFieldFile cache 재사용·손상 내용의 NULL 크기·header만의 크기 성공은 따르지 않는다. GoDj는 매번 새
reader를 열고 전체 내용 검증 실패에서 현재 모델을 보존한다. [저장 이미지 관찰](../../conformance/runners/django/stored_image_reference.py)에
공통 결과·명시적 차이와 크기 갱신/DB 저장·rollback의 분리를 기록한다.
