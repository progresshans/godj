# 현재 상태

- 갱신: 2026-11-15
- 현재: [전체 범위 실행 계획](../EXECUTION_PLAN.md)의
  [I1 / GDJ-0138 사용자 정의 계정](../../work/0138-custom-account-lifecycle.md)을 구현·검증 중이다.
  [PR #8](https://github.com/progresshans/godj/pull/8)과 후속 변경을
  [통합 PR #1](https://github.com/progresshans/godj/pull/1)에 모은다. 구현 통합과 제품 수용은 구분한다.
- 최근 제품 완료 범위: [C1 / GDJ-0137 Project/Core](../../work/0137-project-core-startup.md).
  C1과 선행 F1/A1/Q1/G-ORM의 같은 source 수용 근거는 [TEST_EVIDENCE](TEST_EVIDENCE.md)를 따른다.

## 다음

1. 고정 Directory/Manager 제거·역사 runtime 내부화와 선택 조회/인증·관리 검증 이관 뒤 I1의 현재 source에서 전체 원격 CI와 필수 원본을 대조한다.
   선택 계정·프로필·catalog·collection/through 관리,
   공개 첫 계정 명령, 명시적 모델 전환과 native 필수 FK graph를 포함한다. 기본/custom 변형·독립 SDK·실제 browser·
   별도 process·migration/restart, 양 DB·race·CGO-disabled·전체 platform/cold-build의 요구를 유지한다.
2. 역사 `systemstate`의 최초 선택 domain 이행·원문 보존·퇴역 runtime 차단·receipt/복구를 양 DB에서 확인한다.
   Article의 명시적 이행 소비자와 새 독립 생성 앱의 별도 operator process→custom 모델 전환→host schema 정리→
   재접속/현재 인가·역사 조회를 확인한다. 소비자 작성과 실제 환경별 실행은 TEST_EVIDENCE에서 구분한다.
3. 선택 모델 owner의 수용과 역사 runtime의 별도 이행 책임을 확인한다.
   고정 JSON/Admin 등록과 기본 User 전용 Form 제거, 선택 Admin의 host 삭제·비밀번호 상태·LastLogin·복합 진단·권한별 화면·정책,
   비밀번호 비활성화/복구·명시적 login 입력 정책·Unicode 생성 서비스/저장 한도 편집과 독립 기준 소비자 이관을 원격에서 확인하고,
   catalog의 대량 직접 소유자·권한 합집합·현재 그룹 권한/가입 경합과 독립 lifecycle/admission 소비자의 선택 owner 이관을
   확인한다. 두 catalog 자원의 조회 실패·여섯 mutation의 scope/실제 write/취소/결과 불명과 전체 native/host 상태 검증을
   원격에서 확인한다. 사용자 생성·편집·삭제 수명, email 정규화·권한별 admission과 HTTP/세션/host 상태의 선택 소비자를
   원격에서 확인한다. 선택 생성·편집·읽기 실패의 전체 native 상태, host 삭제의 rollback/결과 불명·scope 수명과 실제
   hash 중 권한 회수·생성/편집 경합·권한 합집합 경계를 확인한다. Username 중복의 독립 관측·선택적 정책·정확한 로그인·
   hash 중 중복 유입·대소문자 경합과 조회/cleanup/audit/결과 불명 검사를 선택 owner에서 확인한다. 기본 비밀번호 정책의
   직접 서비스·HTTP·해싱 중 현재 프로필 재검사와 실패 분류를 기본/renamed 생성 앱에서 확인한다.
   Unusable 생성·반복 회전·복구의 독립 기준, 전체 native 상태·실제 HTTP·세션 폐기와 읽기/쓰기 실패·취소·현재 인가/경합을
   선택 owner에서 확인한다. 선택 로그인의 독립 TCP 수명·세 경로의 전체 native 실패/경합과 실제 collision만 재시도되는
   현재 source를 원격에서 확인한다. 고정 validator factory를 제거한 공통 독립 기준·선택 typed 정책의 실행을 확인하고,
   자기 비밀번호 변경의 전체 native/host 상태·쓰기 prefix와 오류 분류 보강을 원격에서 확인하고,
   선택 Account HTTP의 Form/JSON·입구 거부·전체 native/host 실패 검사를 원격에서 확인하고,
   자기 비밀번호의 설정 소유·별도 연결의 현재 상태/동시 변경·공통 Web runtime 이관과 domain 오류 안정성을
   원격에서 확인하고, reset 메일의 선택 후보·전달→proof→재설정·읽기/전송 실패·snapshot 결합을 확인한다.
   Reset 진입·회전·완료·현재 browser 제거의 전체 상태와 저장 오류/직접 거부/실제 충돌의 출처 구분을 원격에서 확인한다.
   Reset 직접 서비스의 독립 토큰 기준·설정 소유·현재 상태·단일 승자·대량 폐기 검사의 선택 owner 이관을
   원격에서 확인한다. Reset 세션·공통 Web의 독립 세션 기준·현재 browser·단일 proof 소비·gate·충돌 검사의
   선택 owner 이관을 확인한다. Reset Account Form/JSON의 성공·거부·동일한 공개 접수 응답·저장 실패와
   비공개 오류 보고의 선택 소비자 이관과 고정 자기 비밀번호/reset 제품 API 제거를 확인한다.
   관리자 비밀번호 변경의 전체 native/host 상태·대량 폐기·현재 그룹 권한·실패 검사 이관을 확인한다.
   고정 Manager 제품·입력 DTO·설정 제거, 선택 입력 거부와 저장 프로필의 공통 IR 문자열 검사를 확인한다.
   Directory의 독립 조회/인증 기준·실제 저장 손상·검증 중 변경과 HTTP 세션 검사의 선택 owner 이관을 확인한다.
   역사 provisioning·Directory·login 기록의 내부화와 원문/receipt·handoff 퇴역 경계를 원격에서 확인한다.
4. [I1-01~08 수용 요구](../../work/0138-custom-account-lifecycle.md#요구와-실제-소비자)의 제품·생성기·실제 소비자와
   같은 source의 검증 근거를 모두 연결한 뒤 완료를 판단한다. 세부 미완료 범위는 해당 work에 유지한다.

## 검증 경계

C1 source `b19aeedd`의 필수 원격 실행과 C1-01~08 수용 대조를 완료했다. 이 결과를 새 I1 코드의 PASS로 사용하지 않는다.
I1의 현재 실패·수정·부분 통과·원본 대조와 환경별 미확인은 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.
현재 외부 입력이 필요한 blocker는 없다. 나머지 ORM 및 전체 카탈로그의 요구는 실행 계획의 담당 묶음에 유지한다.
