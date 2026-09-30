# 현재 상태

- 갱신: 2026-10-01
- 현재 작업: [GDJ-0104 URL 모델 필드와 Helpdesk 외부 참조](../../work/0104-url-fields-and-helpdesk-links.md)
- 최근 전체 검증: [Hosted full 36711704536](https://github.com/progresshans/godj/actions/runs/36711704536), source `bcc7b76a17aacc6a90a3f360bb8f25fe080a840d`; 필수 owner·집계·새 capture/Git source 결합 완료
- 최근 영향 통합: [BigTIFF Hosted web 36751035636](https://github.com/progresshans/godj/actions/runs/36751035636), source `8d89ec28b10cbba4787a439142354a6c6d8bb73d`; 전체 플랫폼 성공과 구분
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

URLField를 Schema IR·생성 모델·문자열 ORM·migration·Form/Admin·JSON/OpenAPI와 Helpdesk의 `external_url`에
연결했다. 독립 native와 실제 양 DB·생성 소비자·인가/CSRF/rollback·관련 세 mode, 실패 대조/fuzz·생성 drift를 확인했다.
실제 Admin 브라우저 입력의 스킴 보완·저장과 서버 오류 재표시/DB 보존도 확인했다.
[URL 결정](../adr/0083-url-fields-and-input-normalization.md)에 따라 Form과 JSON의 입력 정책, 일반 ORM/출력의
기존 값 보존을 구분한다. Nullable OpenAPI 문자열의 길이는 실제 문자열 branch에서 생성 client까지 검증한다.

[GDJ-0103](../../work/0103-formsets-and-scoped-batch-editing.md)의 Formset/Admin inline·파일 업로드/저장·이미지 검사는
구현된 범위와 검증 source를 유지한다. S3·File/Image choices·BigTIFF의 후속 web 범위까지 source 결합을 확인했다.
[파일/저장 결정](../adr/0082-file-storage-publication-and-reference.md)과 [모델 Form](../../forms/model/README.md)을 따른다.

## 다음 행동

게시한 URL source의 Hosted full에서 드러난 공통 URL 코드의 attestation 소유 목록 누락을 보완했다.
수정 source를 게시하고 새 **Hosted full**을 통합한다. 새 IR kind·migration·생성·공통 OpenAPI/Admin의
누적 플랫폼 검증은 이 milestone이 소유하며 로컬 전체/cold를 중복하지 않는다. 이전 source의 성공은 전이하지 않는다.
남은 codec 특성/storage provider·custom user model·인증/mail provider와 다른 카탈로그 기능은 의존 순서에 따라 이어간다.
현재 외부 입력이 필요한 blocker는 없다.

[검증 문서](../TESTING.md)에 따라 공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지하고,
편집 완료 후 영향 검사를 모은다. 전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다.
장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
