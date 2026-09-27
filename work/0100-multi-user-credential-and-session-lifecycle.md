---
id: GDJ-0100
status: active
updated: 2026-09-27
baseline_commit: "1036bcd079e96260dc5230dab1172e0228f34ce5"
integration_owner: "root"
---

# 다중 사용자와 credential/session lifecycle

ManyToMany 기반을 User·Group·Permission에 사용하고, 단일 operator에서 실제 다중 사용자 관리로 확장한다.
모델·migration·현재 권한 평가와 세션 폐기, 관리 UI/API·실패 경로를 함께 연결한다.
새로운 저장 모델을 만들기 전에 로그인한 credential과 세션의 결합부터 명시한다.
GDJ-0099와 credential/session 기반의 Hosted 통합은 완료했다. 이후 다중 사용자 구현은 새 source에서 검증한다.

## 구현 조건

- [ ] 고정 Django의 사용자·권한·credential/session 외부 동작을 독립 관찰하고 차이와 소유권을 채택
- [x] 불변 credential snapshot과 세션 결합을 기존 인증·실제 HTTP 소비자·실패 경로에 연결
- [x] Schema IR의 User/Group/Permission과 관계·생성 model·historical migration을 연결하고 기존 operator 데이터를 보존
- [x] 현재 사용자·그룹 권한의 합집합과 active/staff/superuser 의미를 durable 조회·실제 Admin/API admission에 연결
- [ ] 사용자·credential 관리의 권한·변경 transaction·세션 폐기·감사·동시성·unknown outcome을 연결
- [ ] 실제 Form/Admin/API·독립 client에서 생성·편집·비밀번호 변경·비활성·재시작을 검증
- [ ] 영향 normal/race/CGO0·양 DB/process와 선택한 통합 milestone의 source/범위를 기록

## 현재와 다음

[Credential snapshot·관리 결정](../docs/adr/0076-credential-snapshots-and-session-binding.md)과
[외부 app·호스트 관계 소유권](../docs/adr/0077-reusable-app-models-and-host-relation-ownership.md)을 구현했다.
현재 credential·role·grant snapshot과 session stamp, User/Group/Permission 모델·migration,
Directory·저장 인증·staff admission·명시적 operator 전환을 Article/Helpdesk·CLI·durable 소비자에 연결했다.
관리자 비밀번호 교체는 현재 인가·revision·credential을 재확인하고 User·대상 session 폐기·감사를 원자적으로 쓴다.
이 기반은 게시와 영향 검증을 완료했다. 이전 Hosted 전체 결과는 이후 관리 변경의 전체 검증으로 전이하지 않는다.

사용자 생성·조회·편집·삭제 service를 구현하고 영향 checkpoint를 통과했다.
생성은 현재 add/change 권한을 모두 확인하고 hash 전 읽기를 종료한 뒤 transaction에서 재검사한다.
편집은 expected revision, scalar와 그룹/직접 grant의 전체 집합을 처리하며 no-op은 revision/audit를 늘리지 않는다.
비활성/삭제의 대상 session 폐기, 호스트 관계 정책과 감사 rollback, unknown 결과 미게시를 양 DB에서 검증했다.
독립 Django manager/UserAdmin 관찰과 Unicode 정규화, 실제 HTTP/session·runtime 재접속, 두 연결의 경쟁과 실패 경계를 포함한다.
전체 UserCreationForm validator의 구현을 뜻하지 않는다.

Permission의 expected revision을 위해 기존 행에 revision 1을 채우는 명시적 migration을 추가했다.
이를 수행하는 scalar-default AddField를 양 DB의 fenced lifecycle·자동 계획·SQL projection에 연결했다.
초기 identity migration은 유지하며 새 runtime은 current migration 누락을 startup에서 거부한다.
관계·credential·session·audit 보존과 backend별 상수 표현을 영향 normal/race/CGO=0·양 DB와 독립 기준으로 검증했다.

Group/Permission 생성·조회·편집·삭제 service를 추가했다. 현재 모델 권한과 revision·unique 선택을 확인하고,
그룹 권한 편집은 모든 관련 사용자의 직접/다른 그룹 합집합을 bounded batch로 검사한다.
삭제는 호스트 관계 정책과 직접 소유자 revision 증가·감사를 한 transaction에 반영한다.
영향 normal/race/CGO=0·양 DB·독립 Django 비교와 negative control을 통과했다.
실제 관리 JSON API·OpenAPI를 Article composition에 연결하고 영향 normal/race/CGO=0·양 DB·negative control을 통과했다.
수정 조건·current 권한·password/session 의미를 HTTP에 연결했다.
새 API의 독립 Session/Bearer client도 실제 HTTP·영속 SQLite와 부모의 DB/session/audit 검사에 연결하고 영향 검증했다.
생성 전용 폼·명시적 object command·revision 조건과 password 비공개 입력을 공통 Admin에 구현했다.
조회 callback에 actor를 전달하고 view 또는 change 권한을 허용하며, 생성·편집의 선택 목록 권한을 구분한다.
기존 Article·Helpdesk 어댑터와 영향 normal/race/CGO=0·양 DB 소비자 검증을 완료했다.
이 공통 기반을 실제 Identity Form/Admin 등록·action별 현재 DB 인가의 선택 목록·감사 조회와 Manager에 연결했다.
Article의 실제 composition, username/email 입력·password confirmation·host policy, view-only 상세를 구현하고 영향 검증했다.
Username 중복 정책의 선행 조건인 literal string `iexact`를 공통 AST·typed/dynamic·관계 경로에 구현했다.
SQLite LIKE escape와 PostgreSQL UPPER 비교는 각 compiler가 소유한다. 명시적 생성 옵션은 NFKC 후보를
hash 전과 write fence 안에서 재검사하며 기본 Manager/API 생성·로그인 의미는 유지한다.
실제 관리 화면은 이 중복 정책을 사용한다. 환경별 실행 상태는 TEST_EVIDENCE를 따른다.
선택 목록은 target view 대신 User change/Group add·change의 현재 권한과 같은 snapshot에서 완전한 목록을 읽는다.
감사 조회도 actor 인가와 같은 read snapshot에 연결하며 Runtime의 별도 transaction을 중첩하지 않는다.
정규화·confirmation·EmailValidator의 독립 Django subset과 실제 양 DB HTTP, revision·권한 회수·감사 실패·unknown,
password/session·PROTECT/CASCADE를 검증했다. 전체 UserCreationForm validator의 완료는 아니다.
고정 Unicode 16의 NFKC·full lowercase·문자 분류와 공식/독립 Python 기준을 연결하고 영향 checkpoint를 통과했다.
이전 세 version probe는 정상 corpus에 포함했다. Credential/CLI 1,024바이트와 IR 256자, 관리 생성 150자/편집 256자를 구분했다.
양 DB의 실제 생성·로그인·수정·bootstrap/adoption, PTY·독립 generated client와 실패/negative control을 검증했다.
내장 네 password validator와 명시적 API 정책·Article 공통 Admin/API 설정을 구현하고 영향 checkpoint를 통과했다.
독립 Django 오류/params·similarity matrix·전체 common 사전, 양 DB의 hash·session/audit·현재 profile fence와 독립 client를 검증했다.
관리 소비자/입력 경계가 닫힌 `5c2045f4`의 Hosted 전체 platform/process 실행에서 이전 graph/config 소비자와 참조 잠금 누락을 확인했다.
현재 graph의 독립 기대, 양 DB 재시작의 User/권한/전환 기록과 이전 credential 비활성화,
scalar backfill SQL 실행, 실제 외부 compiler의 위치 인자 누락 판정과 출처/byte lock을 함께 수정하고 영향 통합 검증을 통과했다.
수정 source의 Hosted 전체를 다시 검증한다. `c7a0c5e0`의 실행에서 추가로 드러난 PTY 에코 관찰과
Python 합산 fingerprint도 수정·검증했고, 남은 job 결과를 확인한 뒤 최종 source의 전체 검증을 이어간다.
전체 UserCreationForm 호환은 별도 조건이다.
Self-service password/reset·사용 불가능한 password lifecycle·last_login 갱신도 별도 미완료 범위다.
이후 기능을 이전 Hosted 검증의 성공으로 표시하지 않는다.
기존 operator에 staff를 자동 추론하는 호환 분기나 in-memory role 부여는 사용하지 않는다.
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md) 한 곳에 기록한다.
