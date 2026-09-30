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

Schema IR의 `FieldFile`은 기본 100 Unicode 문자의 저장 이름을 선언한다. 생성 모델은 `string` 또는 `*string`, query는
기존 DB 독립 문자열 AST, SQLite/PostgreSQL은 같은 길이의 varchar를 사용한다. Char/Email/File 간 kind만 바꾸면 역사와
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
FileField 투영은 명시적인 read-only 저장 이름에 한정한다. JSON 문자열을 upload로 받거나 참조를 URL/다운로드 권한으로 바꾸지 않는다.

Application별 storage Registry는 이미 초기화한 backend를 빌리고 설정/alias 목록만 snapshot으로 소유한다. Alias는 backend를
조회하는 설정 key이며 I/O 권한이 아니다. 전역 default·lazy factory·registry의 자원 Close는 없다. URL capability는 별도로 등록하며
URL 생성과 파일의 존재·접근 인가·server route를 분리한다. Portable 이름을 URI path로 한 번 escape하며 private alias에는 URL이 없다.

인가된 FileResponse는 매 요청 현재 권한과 모델을 조회한 뒤 얻은 저장 이름을 사용한다. 전체 middleware가 성공한 이후에만
독립 reader를 열고, 동일 handle의 metadata를 사용한다. Stat→Open 경쟁·자동 MIME sniff·디렉터리 공개를 추가하지 않는다.
기본 attachment/octet-stream/no-store/nosniff를 사용하고 inline은 명시한다. Buffered 응답 한도를 늘려 파일 전체를 메모리에
올리는 대신 유한 streaming 수명과 별도 byte 한도를 연결한다. Reader는 Application이 한 번 닫으며 HTTP header 뒤 실패는
전송을 abort해 정상 완료로 위장하지 않는다. Request/upload/transaction의 빌린 수명은 streaming이 연장하지 않는다.

[storage 계약](../../storage/README.md), [파일 응답](../../web/streaming.md)과 [모델 폼](../../forms/model/README.md#모델-파일의-준비와-저장)을 따른다.
외부 object-storage backend·서명 URL provider·ImageField·파일 choices와 자동 orphan 회수는 미완료다. 이 결정의 Accepted를 전체 파일 기능
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
