# 현재 상태

- 갱신: 2026-10-01
- 현재 구현: [GDJ-0106 Binary 모델 필드와 입력 정책](../../work/0106-binary-fields-and-model-input-policy.md)
- 최근 완료: [GDJ-0105 Slug 모델 필드와 Article 주소](../../work/0105-slug-fields-and-indexed-article-addresses.md)
- 최근 전체 검증: [Slug Hosted full 36785492754](https://github.com/progresshans/godj/actions/runs/36785492754), source `99ac532a2cfc5e694bfcad4f0d64a6d42c09857a`; 필수 owner·집계·새 capture/Git source 결합 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

BinaryField와 공통 편집 가능 여부의 입력 정책을 DB/history·생성·입력/출력 및 Helpdesk payload 지문에 연결했다.
[Binary 결정](../adr/0085-binary-fields-and-model-input-policy.md)에 따라 독립 native 기준과 영향 normal·CGO=0 검증을
확인했다. Race·브라우저·실패 대조와 생성 drift까지 마쳤으며 새 source의 Hosted 통합은 아직 수행하지 않았다.

SlugField와 일반 DBIndex를 선언·history·양 DB 물리 소유권·생성·Form/JSON/OpenAPI에 연결했다.
Article의 Admin/API 편집과 게시 글의 Unicode 주소·ID fallback까지 영향 검증과 Hosted full 통합을 마쳤다.
입력·일반 ORM/기존 값 보존과 index 소유권은 [Slug/인덱스 결정](../adr/0084-slug-fields-and-column-index-ownership.md)을 따른다.

## 다음 행동

Binary 구현과 영향 검증 근거를 제출하고 새 source의 Hosted full을 통합한다.
별도 작업 사본의 구현은 제출 전이며 위 Slug source의 성공에 포함하지 않는다.
환경별 검증과 남은 통합 범위는 [검증 문서](../TESTING.md)와 TEST_EVIDENCE를 따른다.
남은 codec 특성/storage provider·custom user model·인증/mail provider와 다른 카탈로그 기능은 의존 순서에 따라 이어간다.
현재 외부 입력이 필요한 blocker는 없다.

공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다. 전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다.
장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
