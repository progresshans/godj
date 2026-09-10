# 개발 방향

[현재 작업](status/CURRENT.md)을 끝까지 연결하면서 모델 중심 사용 경험을 넓힌다.
문서·테스트 개수나 compatibility contract 총합을 제품 진행률로 삼지 않는다.

## 다음 작업의 선택

Article와 Category–Ticket Helpdesk에서 사용한 공개 API를 출발점으로, 실제 앱의 다음 요구를 한 가지 고른다.
Schema→Migration→ORM→Form/Admin/API 중 어디까지 연결해야 하는지와 실패 의미를 정한 뒤 필요한 기능만 확장한다.

- ORM/관계: eager Count와 다중 관계 탐색은 joined row·cardinality·cache 의미를 먼저 정한다.
- 앱 성장: 새로운 모델·필드·관계가 요구하는 migration과 기존 데이터의 처리를 함께 검증한다.
- 모델 연결: 현재 allowlist·typed reader로 표현할 수 없는 실제 Form/Admin/API 흐름이 있는지 확인한다.
- Identity: 명시적 operator 권한 갱신 다음의 다중 사용자·credential/session 요구를 구체화한다.

이 목록은 다음 기능의 후보이며 동시에 진행할 작업 목록이 아니다. 활성 작업과 환경별 검증 상태는 CURRENT 한 곳에서 선택한다.

## 수직 단면의 선택

한 작업은 사용자에게 보이는 흐름을 구현한다. 관련 schema, generated code, DB 동작, validation·권한과 실패 경로를 같이 본다.
모든 계층의 빈 구조를 미리 만들거나 Django의 모든 테스트를 먼저 번역하지 않는다.

작은 회귀 수정은 관련 테스트와 integration에서 닫고 여러 변경을 한 통합 milestone으로 묶을 수 있다.
모든 work item마다 전체 로컬·Hosted·artifact publication을 별도로 수행하는 것은 완료의 정의가 아니다.
검증 환경은 위험과 변경 범위로 선택하며 미실행 환경은 명시한다.

Form/Admin/API/Relation을 범용 기능이라고 부르려면 구조가 다른 모델에서도 같은 공개 API를 사용해 본다.
Relation·ownership 의미가 있으면 cross-app 흐름을 포함한다. 작은 bounded 작업을 수행하기 전에 모든 범용 기능을 완성할 필요는 없다.

## 다음 확장 방향

| 방향 | 다음 요구를 선택하는 기준 |
|---|---|
| ORM/Schema | consumer가 요구하는 field·relation·query를 양 backend와 실패 의미까지 연결 |
| Migration | 실제 앱 성장에 필요한 difference, destructive plan의 안전한 preview·실행·복구 |
| Model metadata | Form/Admin/API의 중복 설정과 변환을 줄이되 필드 공개·권한 정책은 명시 |
| Identity | operator 권한 변경, 다중 사용자·credential/session lifecycle의 실제 요구 |
| Web/API | routing·HTTP 표현·배포 관측·API 문서화의 concrete flow |
| Backend/Realtime/GIS | 사용 시나리오와 자원·운영 경계가 정해진 시점에 별도 수직 단면 |

전체 기능 범위는 [CAPABILITY_CATALOG](CAPABILITY_CATALOG.md), 아직 닫지 않은 설계 질문은 [OPEN_QUESTIONS](OPEN_QUESTIONS.md)에 있다.
공개 Alpha나 Product 1.0의 전체 범위를 먼저 고정해야 다음 코드를 작성할 수 있는 것은 아니다.
첫 외부 지원 릴리스를 결정할 때 버전 지원·업그레이드·라이선스 정책을 함께 정한다.

## 통합 기준

현재 구현은 compile/runtime에서 사용할 수 있고 미지원 기능은 명시적으로 거부한다.
Django 동작을 목표로 한 부분은 해당 differential contract 또는 검토한 deviation을 갖는다.
취소·권한 거부·rollback·unknown outcome·publication recovery 같은 중요한 실패 경로를 보존한다.
설계 선택, 코드 구현, 플랫폼 검증 결과는 서로 다른 상태로 기록한다. 실행 전략은 [TESTING](TESTING.md)를 따른다.
