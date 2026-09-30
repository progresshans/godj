---
id: GDJ-0104
status: active
updated: 2026-10-01
baseline_commit: "8d89ec28b10cbba4787a439142354a6c6d8bb73d"
integration_owner: "root"
---

# URL 모델 필드와 Helpdesk 외부 참조

카탈로그의 URLField를 별도 IR 의미로 선언하고 Helpdesk의 외부 참조 주소에 사용한다.
모델 선언 한 곳에서 migration·생성 모델·query·Form/Admin·JSON/OpenAPI로 이어지는 경계를 완성한다.
GDJ-0103의 파일 기능 중 남은 codec/provider는 해당 작업에 그대로 남겨 두며, 모델 타입 기반의 다음 기능을 진행한다.

## 구현과 검증

- [x] 고정 Django/DRF의 문법·Form 스킴 보완·serializer·ModelForm 후처리·choices와 양 DB 저장을 독립 관찰
- [x] URLField의 선언/IR·생성·typed/dynamic/관계 문자열 query·양 DB 저장과 metadata 변경/역방향
- [x] Form/ModelForm·URLInput·JSON 입력/출력·빈 값·생략·default의 서로 다른 책임
- [x] Helpdesk의 existing-row migration·Admin/API 저장·인가/CSRF/범위·오류/rollback·재접속
- [x] OpenAPI와 독립 생성 client의 실제 HTTP·wire/domain·최종 DB 검증
- [x] 완성된 변경 묶음의 영향 normal/race/CGO=0·생성 drift·필수 실패 대조
- [x] 현행 문서와 source별 검증 범위 정리
- [ ] 새 IR/migration/생성·공통 OpenAPI/Admin 소비자의 Hosted full 통합

[ADR-0083](../docs/adr/0083-url-fields-and-input-normalization.md)이 장기 의미를 소유하고,
[TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)가 실제 실행과 실패를 기록한다.
제품·생성물·실제 양 DB/독립 client와 Admin 브라우저 흐름의 영향 검증을 완료했다.
다섯 실패 대조·fuzz·생성 drift를 확인했으며 새 source의 플랫폼 통합은 Hosted full이 소유한다.
첫 Hosted full의 dependency closure 검사가 URL 공통 코드의 소유 목록 누락을 검출했다.
두 attestation 목록과 변조/symlink 회귀를 보완하고 관련 세 mode를 통과했으며 수정 source의 Hosted 통합은 남아 있다.

URL 문법 검증은 fetch/redirect/HTML href의 허가가 아니다. 일반 ORM은 저장 문자열을 자동 정규화하거나
검증하지 않는다. Form은 생략 스킴을 보완하고 JSON은 완전한 URL을 요구한다.
전체 framework·custom user model·나머지 필드/validator/provider는 별도 미완료 범위다.
