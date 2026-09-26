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

이 저장 모델은 아직 다중 사용자 인증·credential 관리 제품이 아니다. EncodedPassword를 가진 raw 저장 모델을 응답·로그에
직접 내보내지 않는 표현 경계, 현재 권한 합집합, inactive/staff/superuser admission, 기존 operator adoption과 관리 UI/API가
GDJ-0100의 남은 구현이다. Source·환경별 실행 결과는 [TEST_EVIDENCE](../status/TEST_EVIDENCE.md)가 소유한다.
