# ADR-0077 — 재사용 앱의 모델과 호스트 관계 소유권

- 상태: accepted design
- 날짜: 2026-09-27
- 관련 작업: [GDJ-0100](../../work/0100-multi-user-credential-and-session-lifecycle.md)

## 결정과 이유

User·Group·Permission을 라이브러리 앱으로 공급하려면 여러 호스트가 같은 Go 모델 타입을 사용할 수 있어야 한다.
기존 전체 프로젝트 snapshot만으로 app의 ABI를 표시하면 호스트별 관계 추가가 라이브러리 모델의 재생성을 요구한다.
`codegen.AppSpec.External`로 모델 package의 파일 소유권을 선언한다. 외부 앱은 ImportPath·PackageName·Schema를 제공하며
Directory는 비워 둔다. Schema IR은 호스트의 전체 관계 해석에 계속 참여한다.

라이브러리는 자기 앱의 모델·metadata·relation object·projection companion을 생성하고 소유한다.
네 companion 각각에 정규화 schema hash와 네 app generator ABI를 결합한 marker를 출력한다.
호스트 binding은 실제 import한 네 marker를 compile 시 요구한다. 호스트가 소유하는 일반 app의 전체 프로젝트 seal은 유지한다.
호스트는 모든 app을 포함한 query·facade·prefetch·delete binding을 생성한다. 호스트에서 User를 향해 추가한 FK의
CASCADE·PROTECT까지 호스트 graph가 소유하므로 라이브러리의 별도 project binding을 전체 호스트 삭제기로 대신 사용하지 않는다.

외부 app에는 호스트가 생성·삭제하는 파일 roster가 없다. Manifest, runner wire, bounded scan/size와 snapshot hash에
External을 보존한다. 검사기는 격리한 Go module 환경의 `go list -find`로 dependency를 찾아 읽는다.
현재 네 companion과 marker, handwritten raw-model의 예약 메서드 충돌을 검사하고 생성 파일도 변경 전후 fingerprint에 포함한다.
Marker는 schema/ABI 선언이며 제3자 코드의 진위 서명은 아니다. 임의 dependency를 신뢰하는 결정은 Go module 선택 경계에 남는다.

외부 앱은 선택한 프로젝트 폴더 안에도 있을 수 있다. 이 경우 확인한 companion을 read-only namespace로 인식하되
publication journal의 쓰기·삭제 소유권에 넣지 않는다. 현재/이전 manifest의 소유 파일과 겹치면 candidate 검증 전에 거부한다.
Manifest와 같은 대소문자 비교 규칙을 적용해 경로 표기만 바꾼 겹침이나 control directory 우회를 거부한다.
외부 app과 publication control directory의 겹침도 거부한다. 의존성 변경이 검증 도중 생기면 기존 정상 호스트를 보존한다.
Candidate 검증은 compile만 하며 dependency init이나 test body를 실행하지 않는다.

## 적용 범위

`identity/modeldef`는 User·Group·Permission과 세 ManyToMany 관계를 선언한다. PrincipalID는 수정 가능한 username이나
관계 PK와 구분되는 opaque 인증·감사 identity다. 초기 historical migration은 라이브러리가 제공하며 적용·adoption은 호스트의
명시적 작업이다. `conformance/identityfixture`는 같은 라이브러리 User 타입에 호스트 FK·보호 정책을 추가하는 실제 소비자다.

이 저장 모델은 아직 다중 사용자 인증·credential 관리 제품이 아니다. Inactive/staff/superuser admission,
기존 operator adoption과 관리 UI/API는 GDJ-0100의 남은 구현이다.

## 현재 계정의 조회와 표현

`identity.Directory`는 username 또는 opaque principal ID로 저장된 사용자와 직접·그룹 권한의 합집합을 읽는다.
동일 permission의 여러 연결은 DISTINCT로 합치고 canonical code 순서로 반환한다. 기존 auth 한도인 256개보다
하나 더 읽어 초과를 거부하며, 잘린 권한 목록을 유효한 계정으로 내보내지 않는다. 잘못된 저장 revision·identity·
credential envelope·permission도 오류이며 부분 결과를 반환하지 않는다. 비밀번호 알고리즘 검증은 실제 인증기의 책임이다.

사용자 행과 권한 SQL 사이에 일반 ORM writer가 commit해도 한 계정에 두 시점이 섞이면 안 된다.
`db.SnapshotReader`의 읽기 전용 callback을 사용한다. PostgreSQL은 REPEATABLE READ READ ONLY,
SQLite는 pinned connection의 첫 읽기 snapshot을 사용한다. 일반 READ COMMITTED Atomic으로 이 보장을 대신하지 않는다.
고정 연결·rowset·만료·취소·panic/Goexit·실패한 종료의 discard/retention은 backend가 소유한다.
종료 실패는 write commit-unknown 성공/실패 판정이 아니며, Directory는 정상 종료된 조회만 게시한다.
여러 SELECT의 일관성은 GoDj가 선택한 계약이며 Django 기본 transaction이 같은 snapshot을 보장한다고 주장하지 않는다.

반환 `Account`는 private credential과 profile을 가진 불변 데이터 관찰이다. Profile의 nullable timestamp와 권한 slice는
복사본을 반환하고 기본 진단은 redacted, JSON은 명시한 Profile만 출력한다. Raw `models.User`의 EncodedPassword는
저장용 필드로 남으며 raw model을 응답·로그에 직접 사용하면 안 된다. 관리 serializer가 허용할 필드는 후속 소비자가 소유한다.
Directory는 active/staff/superuser 값을 보존하지만 로그인이나 권한 허용을 결정하지 않는다. 비활성 계정의 저장된 grant도
조회할 수 있다. Per-user/group cache는 없고 다음 조회는 새 snapshot을 읽으며 이미 반환한 Account는 바꾸지 않는다.

[독립 Django runner](../../conformance/runners/django/identity_reference.py)는 고정 6.1의 User/Group/Permission,
ModelBackend와 AdminSite를 직접 실행한다. 권한 합집합·보유 객체와 새 객체·그룹 삭제 결과를 GoDj 조회와 비교한다.
8개 active/staff/superuser 조합과 미등록·비정규 permission의 관찰도 보관하되 아직 GoDj admission 구현 증거로 세지 않는다.
출처와 라이선스는 [SOURCES](../SOURCES.md)와 [Django license](../../LICENSE.django)를 따른다.
Source·환경별 실행 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
