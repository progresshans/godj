---
id: GDJ-0060
status: active
updated: 2026-09-07
baseline_commit: "0ffce7029b80988d6bc28391dca2f5d8967d65c7"
---

# 생성기 검증의 실행 경계 정리

빠른 생성기 검사에 섞인 외부 Go 프로젝트 실행을 integration 범위로 옮기고, 동일한 입력과 임시 프로젝트 준비를 공유한다.
생성물의 의미·외부 consumer compile·잘못된 조합 거부·실패 시 기존 산출물 보존 검증을 유지한다.

## 범위와 진행

- 수정 범위: `codegen/`의 테스트·테스트 보조 코드, Make/CI 실행·package 선택·필수 실행 연결, 검증 주기와 상태 문서.
- 제품 생성기·공개 API·생성 ABI·고정 reference artifact는 이번 변경 대상이 아니다.
- 순수 byte/schema 검증은 빠른 package에 남기고 외부 Go 실행을 별도 consumer package로 이관한다.
- 단순 이관의 테스트 본문·생성 fixture 내용은 대조한다. 공통화는 같은 의미일 때 적용하고 mutable 입력은 호출마다 새로 만든다.
- 사용자가 승인한 주기에 따라 한 변경 묶음을 먼저 완성한다. 편집 중에는 필요한 compile 확인만 하고 관련 테스트를 모아 실행한다.

## 완료 조건

- [x] 생성기 단위 검사와 외부 consumer 실행 분리, 공통 준비 정리
- [x] Make/Hosted의 integration·platform·필수 sentinel에 새 위치 연결
- [x] 관련 normal/race/CGO-disabled·generated drift와 CI 도구 회귀 확인
- [ ] 고정 소스의 통합 검증, 실제 실행 비용·미실행 범위·완료 상태 기록

관련 범위는 로컬에서 한 번 확인하고 전체 platform/cold/external 검증은 최종 Hosted 통합 시점이 소유한다.
문서 완료 기록에 전체 제품 검증을 반복하지 않는다. 검증 결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 다음 행동

로컬 검증을 통과했다. 소스를 고정하고 기존 Draft PR의 전체 Hosted 통합 검증을 확인한다.

## 구현 결과

- `codegen/consumertest`가 외부 Go 실행을 포함하는 기존 최상위 테스트 29개를 소유한다. 순수 생성기 테스트 78개는 기존 package에 남겼다.
- 공유 schema는 호출마다 새 값을 만든다. codegen 내부 테스트의 import cycle을 피하도록 순수 IR 입력과 생성기 타입을 사용하는 준비를 나눴다.
- 동일한 임시 module·파일 쓰기·명령 환경·Go 실행 준비를 공유했다. 기존 bundle의 별도 checksum 설정을 유지하고 명령에 context를 연결했다.
- 이관 전 최상위 테스트 107개는 모두 남아 있고, 생성된 Go fixture literal 40종의 바이트와 개수도 보존했다.
  nested schema 격리·실행 환경 보존·취소된 명령의 미실행 회귀 3개를 추가했다.
- Make의 포맷 검사는 NUL로 파일 이름을 전달하며 실제 존재하는 파일을 배치로 처리한다. parse 오류, 미포맷 파일,
  공백이 있는 이름, tracked deletion과 실패한 Git listing의 부정 회귀를 확인했다.
- `make quick`의 외부 consumer 제외, integration 분류와 relation platform 필수 sentinel을 연결했다. PR feedback의 문서·CI 도구 중복 실행도 제거했다.

이번 변경은 실행 경계와 반복 준비를 정리한다. 분리한 package의 import·보조 코드와 새 회귀를 포함한 Go 라인은 기준보다 67줄 늘었다.
무거운 consumer 검증을 integration에서 계속 실행하며 전체 테스트 비용이나 지원 범위가 줄었다고 주장하지 않는다.
