---
id: GDJ-0103
status: active
updated: 2026-09-30
baseline_commit: "cb76b165aa3379c8c40aabfa7d12354460c57bf7"
integration_owner: "root"
---

# Formset과 범위가 정해진 여러 행 편집

같은 Form/ModelForm의 여러 행을 한 요청으로 다루는 기능을 구현한다. Helpdesk의 여러 항목을 함께 입력·편집하는
실제 흐름을 소비자로 연결한다. 기존 행의 서버 소유 identity·인가·transaction과 폼의 순서/삭제 요청을 구분한다.
GDJ-0102의 initial source `cb76b165`는 Hosted full 62 jobs·8 owners와 새 capture 결합까지 확인했다. 이후 Formset 변경은 별도로 검증한다.

## 구현과 검증

- [x] 고정 Django에서 management·개수/상한·빈 추가 행·삭제/정렬·cross-form 오류를 독립 관찰
- [x] 불변 SetSpec/Set·prefix/management·bounded 생성·행 오류/선택과 pure validator 구현
- [x] native 33개·malformed/forged counts·중복 입력·소유권/비공개·실제 실행 횟수·관련 race/부정 대조 검증
- [x] 공통 model candidate와 typed 준비를 재사용하는 여러 행 준비/검증 연결
- [x] 실제 Helpdesk 입력/편집과 권한·서버 범위·오류 재표시·원자 저장/실패 경로 연결
- [x] 구조가 다른 모델 소비자·관련 실제 DB/race·필수 실패 대조와 영향 checkpoint
- [x] 현행 사용법·지원 범위·환경별 증거 정리
- [x] 여러 행 unique/복합 제약·compound cleaned 제외와 실제 HTTP 쓰기 전 거부
- [x] canonical 부모 FK·InlineSet과 pending key 준비·실제 HTTP/양 DB parent-child 저장 및 rollback
- [x] core/typed/Helpdesk 제품 묶음의 Hosted full 통합: source `6d8afda5`, 이후 Inline는 별도 검증
- [x] 조회 전용 기존 행과 추가 행의 분리·typed 저장 준비 차단·형제 고유성/부모/PK 보존
- [x] Admin inline의 권한별 표시/입력·부모/자식 합성 저장과 실제 HTML 성공/실패 경로
- [x] 동적 미저장 행 추가/제거·권한별 prototype·실제 브라우저 입력/오류 재표시/SQLite 저장
- [x] Admin 합성 저장 source `6d3fe97f`의 Hosted full·새 capture 결합
- [x] 파일 multipart/임시 자원 수명·FileField와 Formset·typed 파일 명령·실제 HTTP 입력과 실패 정리
- [x] Admin 부모/inline/명령 파일 widget·multipart·권한/CSRF·오류 재표시와 실제 브라우저 파일 전달
- [x] 로컬 storage의 no-overwrite 게시·독립 파일 수명·명시적 HTTP 업로드 소비와 불확실/정리 실패 분리
- [x] 모델 FileField의 IR/생성/ORM/Form·Admin 연결, 실제 저장 이름과 파일/DB 결과 구분
- [x] 일반 모델 여러 행의 변경/추가/삭제·지연 collection·행별 결과와 파일 게시 연결
- [x] Storage alias·URL/인가된 유한 serving·독립 reader 수명과 양 DB 실제 다운로드
- [x] 격리된 Memory storage의 원자 게시·내용/파일/동시 저장 한도와 양 DB 파일 소비자
- [x] 같은 열린 파일의 조건부 조회·단일/여러 Range·HEAD와 양 DB/양 backend의 인가된 소비자
- [x] 동적 UI·업로드/storage·FileField·여러 행 저장·alias/streaming의 후속 Hosted full 통합: source `365ad9d4`
- [x] context 기반 이미지 내용 검증·Form/Formset·Admin 명령과 명시적 파일 게시의 공통 기반
- [x] 모델 ImageField의 IR·폭/높이 소유권·생성/migration·Form/Admin·양 DB와 관련 race
- [x] 저장된 이미지의 명시적 검사/typed 크기 갱신·양 DB 저장/rollback과 관련 race
- [x] BMP/DIB·classic TIFF 전체 페이지/예산·Form/Admin·양 DB 저장/재검사와 관련 race
- [x] APNG의 별도 기본 이미지·모든 frame/예산·Form/Admin·양 DB 저장/재검사와 관련 race
- [x] WebP의 모든 frame·실제 bitstream 크기/alpha·Form/Admin·양 DB 저장/재검사와 관련 race
- [x] S3의 조건부 게시·checksum·불확실한 결과·독립 version reader·명시적 서명 URL과 Form/Admin/양 DB 소비자
- [x] 저장 이미지 검사와 BMP/DIB/TIFF·APNG/WebP의 누적 Hosted full 통합: source `bcc7b76a`
- [ ] S3와 새 dependency/service profile의 Hosted web 통합
- [ ] 나머지 codec 특성/storage backend와 남은 파일 의미
- [x] Memory·Range/conditional·이미지 입력/모델의 후속 Hosted full 통합: source `4793382d`

요청의 INITIAL_FORMS를 신뢰해 저장된 행을 추가 행으로 바꾸거나 생략할 수 없게 한다. 서버 initial 수와 요청의
management 일치를 검사하며, 행 수의 hard cap은 callback/폼 생성 전에 적용한다. 이 count 검사는 실제 모델 identity와
현재 인가·revision 검사를 대체하지 않는다. Order/Delete는 입력 의도이며 자동 저장·삭제·commit 권한이 아니다.

[ADR-0081](../docs/adr/0081-formset-counts-and-row-ownership.md)의 의미와 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)의
실행 범위를 따른다. Pure formset만으로 ModelFormSet·inline·file upload 전체 완료를 선언하지 않는다.

공통 실행의 사용법은 [Formset](../forms/formset.md)과 [typed 모델 준비](../forms/model/README.md)를 따른다.
서버 current 집합의 PK로 행을 연결하고 누락/중복/외부/추가 행 identity는 삭제 여부와 무관하게 거부한다. Field/model 검증은
한 번 실행하며 core와 같은 binding의 모델 후처리만 허용한다. Article·Ticket의 scalar/collection·서버 값·동시 준비와
고정 native 20개 관찰, 영향 normal/관련 race·기존 양 DB 저장·세 부정 대조를 확인했다.

실제 [Helpdesk 편집기](../examples/helpdesk/README.md#여러-티켓을-함께-편집하기)는 현재 cohort/인가·권한별 추가/삭제·원래 입력
재표시·페이지 범위와 원자 scalar/collection·PROTECT/CASCADE·audit를 연결했다. 양 DB의 실제 HTTP 성공/실패·롤백·재개와
관련 race, Add 거부/audit 실패/unknown outcome의 부정 대조를 확인했다. 새로운 전체 플랫폼 증거는 게시한 소스의 Hosted
통합에서 얻으며 선행 소스의 성공을 전이하지 않는다. 모델 파일/storage·전체 ModelFormSet 자동화는 별도 미완료 범위다.

ModelFormSet의 고유값 검증은 고정 Django 23개 사례와 실제 양 DB HTTP로 연결했다. 겹친 제약은 검사 시작 시점의 완전한
튜플을 IR 순서로 비교하며, 후속 사용자 validator가 모델 진단을 없애지 않는다. `568b75b4`의 Hosted full은 새 하위 사례의
부모 테스트를 실행 정규식에서 선택하지 못해 필수 job이 실패했다. 실행할 부모와 확인할 전체 하위 이름을 구분하도록 고쳤고,
기존 선택자 0회/수정 선택자의 필수 32개 실행을 실제 Go에서 재현했다. 수정 소스의 전체 통합은 아래 실행에서 완료했다.

`6d8afda5`의 Hosted full `36516565253`은 62 jobs·8 owners·최종 집계와 새 capture의 Git source 결합까지 완료했다. 이후 InlineSpec은 canonical
project FK와 양 manager를 결합하고 서버 부모를 hidden 입력·모델 후보·형제 unique 검사에 연결했다. 다른 부모/중복 scalar를
삭제/빈 행에서도 전체 거부한다. Pending 부모는 저장으로 key를 받은 뒤 순수 준비하며 nullable FK도 orphan 준비를 허용하지 않는다.
고정 native 22개 입력/4개 저장 관찰, 양 DB의 부모/자식 저장·지연 쓰기·늦은 실패 rollback, 실제 HTTP 위조 거부와 관련 race/
세 부정 대조를 확인했다. 처음 지원하지 않는 FK default로 만든 테스트 fixture는 실패했고 canonical 정책 거부로 고친 모델 범위를
다시 검증했다. 실행별 source/범위는 TEST_EVIDENCE에 분리했다. 새 부모 HTML 화면과 Admin inline UI는 아래 후속 구현에서 연결했다.

고정 Admin에서 조회 전용 기존 행은 POST의 일반 값이 없어도 initial을 유지하고 callback을 건너뛴다는 동작을 확인했다.
공통 SetConfig의 ReadOnlyInitial로 일반 입력과 명시적 ORDER/DELETE를 분리했다. 모델 후보는 서버 값을 유지하며 독립/전체
준비에서 읽기 전용 행을 쓰기로 바꾸지 않는다. 새 행의 고유성 비교에서는 기존 행을 제외하지 않는다. Native 10개 관찰,
양 DB의 기존 행 무변경/새 행 저장과 같은 source의 normal·관련 race·세 실패 대조를 확인했다. 삭제 행의 callback 생략과
native의 삭제 전 DB 고유성 거부는 pure 준비와 구분한다. 권한별 inline 표시와 단일 합성 저장은 후속 Admin 계층에 연결했다.

Admin은 canonical typed inline을 부모 registration의 합성 callback에 연결했다. 서버에서 선언한 추가 행과 빈 prototype을 표시하며
권한별 current 조회·readonly·extra/DELETE, 원래 입력/오류 재표시와 binding owner를 유지한다. Helpdesk AdminRegistry는
transactional audit를 명시적으로 받아 새 티켓과 보고서·양쪽 감사 기록을 같은 transaction에 저장한다. 현재 부모/자식 scope,
삭제 전 DB unique·child-only audit·늦은 실패와 unknown outcome을 실제 양 DB HTML 소비자로 검증한다. 독립 Registry와
Application 상태는 바꾸지 않는다. 현재/실행 범위는 CURRENT와 TEST_EVIDENCE, API는 [Admin inline](../admin/inlines.md)에 있다.

동적 UI는 미저장 행만 추가/제거하며 기존 identity·INITIAL_FORMS·다른 inline과 원래 입력값을 유지한다. 고정 Django의
inline script를 동작 참고로 읽었지만 전체 브라우저 동등성을 주장하지 않는다. 실제 GoDj 브라우저 소비자는 두 HasMany inline,
min/max·오류 행 재번호·readonly/no-add·새 부모/자식의 SQLite 저장을 확인한다. Files·전체 ModelFormSet 저장 자동화는 남아 있다.

Admin 합성 저장 `6d3fe97f`는 Hosted full 62 jobs·8 owners·새 capture 결합까지 완료했다. 이후 파일 입력은 별도 source다.
`uploads`는 bounded multipart와 메모리/임시 파일·request/reader 수명을 소유하고 Form/FileField는 기존 참조 유지·새 업로드·
clear 의도를 순수하게 구분한다. 여러 행 prefix·readonly·삭제와 typed 준비의 파일 명령을 연결했다. Native 22개 관찰·
실제 HTTP의 성공/입력 오류/handler 오류/panic/한도 초과·중단 정리와 관련 race/부정 대조를 확인했다. 실행 상세는
TEST_EVIDENCE에 둔다. [사용법](../uploads/README.md)을 따르며 Admin 파일 widget/전송을 후속으로 연결했다. 모델 FileField/storage·DB 연계는 남아 있다.


모델 FileField의 저장 이름을 IR·생성 typed 모델·DB 독립 문자열 query와 SQLite/PostgreSQL migration에 연결했다. Form은
candidate 이름과 pending upload를 구분하며 typed 준비는 명시적 `SaveFiles` 이후에 모델/DB 쓰기를 허용한다. 파일 게시 후의
부분 실패·DB rollback·clear·모델 삭제를 분리하고 기존 파일을 자동 제거하지 않는다. Native 비교와 실제 Admin HTTP·양 DB
생성 소비자의 실행 범위는 TEST_EVIDENCE, 사용법은 [모델 파일](../forms/model/README.md#모델-파일의-준비와-저장)에 둔다.
Storage alias·다른 backend·URL/serving은 남아 있으며 일반 여러 행 저장은 아래에서 연결했다.

일반 모델 여러 행 저장은 `SavePlan`으로 changed 기존/새 행과 삭제 의도를 선택한다. Mutable 모델의 key를 보존하고 기존
변경/삭제의 제출 순서·새 행 저장·지연 collection·첫 실패까지의 결과를 연결했다. 모든 selected row의 파일 binding/name을
게시 전에 검사하며 삭제/readonly 행은 게시하지 않는다. Callback의 원래 오류와 outer transaction 결과는 구분한다.
Helpdesk Admin 보고서의 인가·고유값·선택 열 저장/audit를 계획에 연결했고, native 13개 관찰과 양 DB의 실제 저장/rollback,
multipart 파일 게시 뒤 PROTECT rollback·파일 보존, 관련 race·부정 대조를 확인했다. [증거](../docs/status/TEST_EVIDENCE.md)의
소스별 범위를 따른다. 기존 Ticket editor의 제품 전용 삭제 우선 저장은 그대로이며 일반 계획의 native 순서를 대신하지 않는다.

Storage alias를 application 설정의 불변 capability 등록으로 연결하고 URL 생성과 파일 인가를 구분했다. 유한 Web stream은
전체 middleware 성공 뒤 독립 reader를 열며 Request/upload 수명을 연장하지 않는다. 모델 ID와 현재 principal을 조회한
FileResponse 소비자에서 cookie login/CSRF·multipart 게시·DB 저장·권한별 다운로드를 연결했다. Header 후 오류의 전송 중단·
같은 열린 handle의 metadata·HEAD·정리/종료 수명은 [파일 응답](../web/streaming.md)을 따르고 실제 검증은 TEST_EVIDENCE에 둔다.
Memory backend·Range/conditional과 전체 플랫폼 통합은 아래에서 연결하며 다른 provider는 계속 별도 범위다.

`6d3fe97f` 이후 동적 inline UI·업로드/storage·모델 파일·일반 여러 행 저장·alias/streaming과 entropy 잠금 수정을 source
`365ad9d4`의 Hosted full에서 통합했다. 로컬 전체를 중복하지 않았고 필수 owner·최종 aggregate·새 capture 결합을 확인했다.
이후 Memory backend와 Range/conditional은 별도 source와 영향 검증이 소유한다.

추가 메모리 backend는 프로세스 내 격리·완성 후 게시·독립 reader를 기존 Backend에 연결한다. 저장/게시/삭제 후 열린 파일을
모두 quota에 포함하고 source/entropy 실패의 예약 정리·동시 저장을 제한한다. 같은 생성 모델과 로그인/CSRF·업로드/다운로드
소비자를 SQLite/PostgreSQL의 filesystem/memory 양쪽에 연결한다. Native 공유 cursor/부분 파일/재귀 directory 삭제와는
명시적으로 구분한다. `365ad9d4` Hosted source 이후 별도 영향 normal/race·양 DB 생성 소비자와 8개 부정 대조를 검증했다.
실행 환경과 source 차이는 TEST_EVIDENCE에 둔다. 다른 provider와 남은 파일 의미의 완료를 뜻하지 않는다.

열린 reader의 context 기반 seek와 ContentMetadata를 연결했다. Memory의 content version과 filesystem의 약한 수정 시각을
구분하고, 기존 FileResponse가 인가 뒤 조건 우선순위·HEAD·단일/여러 Range·본문 없는 상태를 처리한다. 파일 재사용과 동시
응답은 새 reader/metadata로 판단하며 bounded framing·중간 실패 abort·정리를 보존한다. 고정 Django·HTTP/Go 공통 결과,
양 DB/양 backend의 실제 로그인/업로드/소유권/다운로드와 관련 실패 검사를 확인했다. 기존 Hosted source에 포함되지 않은
별도 영향 checkpoint이며, 다음 파일 의미는 ImageField의 내용 검증·모델/폼·저장 연결이다.


공통 이미지 입력은 파일의 독립 reader로 실제 내용과 자원 한도를 검증한다. `Bind(ctx, ...)`가 Form/ModelForm/Formset/Admin과
기존 인증 소비자까지 같은 context를 전달하며 사용자 validator/model clean은 pure다. 검증 metadata와 클라이언트 MIME을
분리하고 기존 파일 이름은 읽지 않는다. Native 비교·Admin 실제 multipart→검증→filesystem/memory 저장·실패/정리와 기존
양 DB 소비자의 관련 회귀를 확인했다. Source 차이와 실행 범위는 TEST_EVIDENCE에 둔다. 이 변경은 모델 ImageField·크기 field
자동 반영·추가 codec/전체 플랫폼 완료를 뜻하지 않으며, 다음 단계는 이 기반을 canonical 모델 의미에 연결하는 것이다.


모델 ImageField는 canonical IR의 이름·크기 참조와 생성 descriptor를 공유하고 Form/ImageField의 내용 검증을 재사용한다.
검증한 크기는 model clean 전에 후보/typed 입력에 반영하며 크기 필드는 Form/JSON/PostClean의 독립 변경 대상에서 제외한다.
Admin의 현재 snapshot·CSRF/인가와 파일/DB 저장 경계를 유지한다. IR/이력의 참조 소유권·변경 순서·digest/자원 한도와 SQLite의
입력 정책 sealing을 확인했고, 고정 Django·양 DB/양 backend·생성 소비자/CLI·관련 race로 검증했다. 자세한 실행/source 차이는
TEST_EVIDENCE, 장기 의미와 차이는 ADR-0082, 사용법은 모델 Form 문서에 둔다.

기존 저장 파일의 명시적 검사/갱신은 `storage.InspectImage`와 `storage/model`에 연결했다. 독립 handle의 내용·길이·Close를
검사한 뒤 canonical 크기만 갱신한 모델을 반환하고 실제 DB 저장은 caller가 소유한다. 고정 native의 cache/손상/header 차이,
양 DB/양 backend 저장·rollback·재개방과 관련 race·부정 대조를 확인했다. 추가 codec/provider와 전체 기능 카탈로그는 계속
미완료이며, `4793382d`의 완료한 Hosted full에도 이 후속 코드는 포함되지 않는다.


`4793382d`의 Hosted full은 Memory·Range/conditional·공통/모델 ImageField와 분리한 reference 환경까지 통합했다.
필수 owner·최종 집계·새 capture의 실제 소비와 Git source 결합을 확인했다. 이후 저장 이미지 검사와 BMP/DIB·classic TIFF는
별도 영향 checkpoint다. TIFF는 모든 주 페이지·tile padding·metadata/블록 범위와 반복 블록 참조의 byte 합을 검사하고
전체 내용을 디코딩한다. 같은 업로드를
Form/Admin·생성 모델/양 DB 저장과 명시적 재검사에 연결했으며 실행 상세는 TEST_EVIDENCE에 둔다. 나머지 codec 특성·provider와
전체 기능 카탈로그는 계속 미완료다.

APNG를 같은 이미지 검사/저장 경계에 연결했다. 기본 이미지의 animation 참여 여부와 모든 frame의 순서·CRC·영역·개수 및
합산 예산을 확인한 뒤 각 raster를 디코딩한다. 기존 PNG의 metadata와 원문을 보존하며 `.apng` 입력을 Formset/Admin·생성
모델 저장과 명시적 재검사에 연결했다. 고정 native는 폼 결과·별도 frame player 결과를 구분한다. 영향 normal/race·양 DB/양
backend·fuzz와 원본 선택 실행이 통과하는 부정 대조를 확인했다. Hosted full `4793382d` 이후의 별도 영향 범위이며 남은
애니메이션 WebP·codec/provider와 전체 카탈로그의 완료를 뜻하지 않는다.

WebP의 정적/애니메이션 파일을 같은 이미지 경계로 연결했다. RIFF·ANIM/ANMF·실제 VP8/VP8L 치수·프레임별 alpha와
합산 예산을 먼저 검사하고 모든 frame의 pixels를 디코딩한다. 고정 native의 폼 결과·별도 재생 실패·공개 형식의 reserved
field 규칙을 구분하며 Formset/Admin·양 DB/양 backend의 저장/재검사와 실패 경로를 검증했다. 저장 이미지 검사부터 이번
WebP까지를 다음 Hosted full milestone의 통합 범위로 정했다. 로컬 전체를 중복하지 않고 게시한 source의 필수 owner·
aggregate·새 capture 결합을 확인한다. 나머지 codec 특성/provider와 전체 카탈로그는 계속 미완료다.

S3는 명시적 credential·bucket/prefix와 공식 SDK를 사용하는 backend로 연결했다. Bounded 입력 완성·conditional PUT·
전체 checksum과 게시 결과를 보존하며, immutable version을 가진 reader만 같은 버전으로 seek한다. 서명 URL은 fresh
모델 소유권 조회 뒤에 발급하고 이미 발급한 bearer capability의 유효기간을 구분한다. 같은 생성 모델/Form/Admin을
실제 MinIO와 양 DB에 연결했다. 기존 이미지 source의 Hosted full과 새 S3 source의 Hosted web은 각각 검증하며,
제품별 실행·실패·정리 증거는 TEST_EVIDENCE가 소유한다.
