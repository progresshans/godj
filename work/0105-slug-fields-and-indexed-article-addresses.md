---
id: GDJ-0105
status: active
updated: 2026-10-01
baseline_commit: "90b9b59fc6753092b653d87635c5bb08ad7165df"
integration_owner: "root"
---

# Slug 모델 필드와 인덱스가 있는 Article 주소

Article에 사용자가 정하는 읽기 쉬운 주소를 연결한다. SlugField의 입력 의미와 기본 검색 인덱스가
Schema IR·migration·생성 모델·ORM·Form/Admin·JSON/OpenAPI에서 하나의 모델 선언을 따라야 한다.
일반 필드 인덱스의 선언·생성/삭제·물리 소유권 검사는 이 요구에 필요한 기반이다.
GDJ-0104의 게시 source에 대한 Hosted full 검증은 별도로 계속 추적한다.

## 구현과 검증

- [x] 고정 Django/DRF의 ASCII/Unicode·공백/길이·choices/default와 실제 인덱스 변경/역방향을 독립 관찰
- [x] 정규화된 SlugField·기본 50자/인덱스·Unicode 입력 정책과 일반 DBIndex 선언
- [x] 엄격한 history/자동 계획·양 DB 인덱스 DDL·catalog/revision/rollback·unique와의 상호 작용
- [x] 생성 string/nullable metadata·공통 문자열 Query AST·양 DB 생성 소비자
- [x] Form/ModelForm·서버 후처리·JSON 입력/출력·OpenAPI와 독립 client
- [x] Article의 기존 데이터 migration·Admin/API 편집·안전한 주소 조회와 오류/인가/rollback
- [x] 완성된 변경 묶음의 영향 검증·필수 실패 대조·생성 drift와 현행 문서
- [ ] 새 IR·일반 index·migration·생성/Article 소비자의 Hosted full 통합

자동 slug 생성·소문자화·Unicode 정규화를 입력 검증과 섞지 않는다. 일반 ORM은 기존 문자열을 보존한다.
양 DB·영향 세 mode·독립 생성 client·프로젝트 명령과 실제 Admin/공개 상세 브라우저를 검증했다.
[ADR-0084](../docs/adr/0084-slug-fields-and-column-index-ownership.md)가 장기 의미를 소유한다.
검증 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록하며, 구현과 환경별 실행을 구분한다.
