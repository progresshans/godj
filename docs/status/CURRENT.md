# 현재 상태

- 갱신: 2026-10-01
- 현재 구현: [GDJ-0107 단건 조회와 savepoint 기반 조회 후 생성](../../work/0107-single-object-creation-and-savepoints.md)
- 최근 완료: [GDJ-0106 Binary 모델 필드와 입력 정책](../../work/0106-binary-fields-and-model-input-policy.md)
- 최근 전체 검증: [Binary Hosted full 36805466011](https://github.com/progresshans/godj/actions/runs/36805466011), source `8e5c2b3e817e5b1090dce9d095ca85055e4af0b4`; 필수 owner·집계·새 capture/Git source 결합 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

BinaryField와 공통 편집 가능 여부의 입력 정책을 DB/history·생성·입력/출력 및 Helpdesk payload 지문에 연결했다.
[Binary 결정](../adr/0085-binary-fields-and-model-input-policy.md)의 영향 검증과 저장 File/Image 초기값 보완을 포함한
source의 Hosted full 통합까지 완료했다. SlugField·일반 DBIndex·Article 주소는
[GDJ-0105](../../work/0105-slug-fields-and-indexed-article-addresses.md)에서 완료했다.

단건 조회·조회 후 생성과 Helpdesk Label 확보 흐름을 별도 작업 사본에 구현했다.
Savepoint의 양 DB 구현과 중첩 session·cursor·실패 경계의 영향 검증을 완료했다.
`Get`·`GetOrCreate`의 metadata snapshot·지연 입력·unique 복구와 일반/eager/prefetch 생성 소비자를 연결했다.
고정 Django 기준 fixture와 별도 생성 Go module의 실제 양 DB 대조도 완료했다.
Helpdesk의 Form/Admin/API·독립 client와 원자 audit를 연결하고 영향 세 mode·양 DB 및 실제 브라우저를 확인했다.
[채택한 의미](../adr/0086-single-object-creation-and-savepoint-ownership.md)를 따른다.
위 Binary 검증 source에는 이 새 제품 변경을 포함하지 않는다.

## 다음 행동

CI 필수 목록과 생성 membership 소비자의 수명 오류 계약 대조를 보완한 source로 Hosted 전체 통합을 다시 수행한다.
기존 실행의 실패·취소와 수정 후 세 mode 확인은 검증 기록에 남겼다. 필수 owner·집계·새 capture/Git source 결합 뒤 전달을 완성한다.
남은 codec 특성/storage provider·custom user model·인증/mail provider와 다른 카탈로그 기능도 의존 순서에 따라 이어간다.
현재 외부 입력이 필요한 blocker는 없다. 새 변경의 환경별 검증은 [검증 문서](../TESTING.md)를 따른다.

공유 Go cache·병렬 실행·생성 소비자의 `-trimpath`를 유지한다. 전체/cold/Hosted 검증은 명시한 통합 milestone이 소유한다.
장기 목표는 [헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 완성이며 한 기능 완료와 구분한다.
