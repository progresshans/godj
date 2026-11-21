---
id: GDJ-0073
status: complete
updated: 2026-09-19
baseline_commit: "b43552a1f88259babe97ec9fe83951f8cd205261"
integration_owner: "root"
---

# TextField와 모델의 여러 줄 입력

## 범위

헌장·기능 카탈로그의 완성 목표를 이어 일반 TextField를 추가한다. GDJ-0072의 full 검증을 마쳤으며,
이 작업은 별도 worktree의 `feature/text-field-model-growth` 브랜치에서 진행한다. GDJ-0072 검증 결과를 이 변경의 PASS로 사용하지 않는다.

- `schema.TextField`는 Go string/nullable `*string`이며 양 DB에서 TEXT다. 문자열 default와 null·빈 문자열을 구분한다.
- 현재 TextField에는 저장 길이 제약을 두지 않는다. CharField의 기존 길이 의미와 HTTP/parser의 자원 제한은 유지한다.
- String typed selector/AST·생성 CRUD·projection·aggregate·serializer/OpenAPI를 재사용하며 metadata의 저장 kind는 보존한다.
- Form 값의 타입과 표시 widget을 구분한다. TextInput/Textarea/Checkbox의 호환 가능한 조합만 허용하며 model Text는 Textarea를 기본으로 한다.
- 고정 Django에서 nullable Char의 빈 Form 입력은 null, nullable Text는 빈 문자열임을 확인했다. Widget과 별도로 빈 문자열/null cleaning 정책을 명시하며 기존 Char 동작을 보존한다.
- Helpdesk의 선택한 editable/API 필드에 nullable `resolution` 처리 내용을 추가한다. 새 migration으로 기존 행은 NULL로 남긴다.
  긴 본문·줄바꿈·HTML escaping과 기존 권한·CSRF·transaction을 실제 소비자로 검증한다.
- 고정 Django 6.1 TextField의 저장형과 CharField/textarea Form을 참조한다. 임의 collation·모든 Text 옵션·Char→Text AlterField는 별도 미완료 범위다.

## 구현과 검증

- [x] IR·DSL·historical definition·양 DB의 TEXT 물리 schema
- [x] shared string query/생성 코드·nullable/default와 실제 외부 소비자
- [x] 명시적 widget과 Form/Admin·serializer/OpenAPI
- [x] Helpdesk migration·기존 데이터·긴 본문·생성 client
- [x] 영향 범위의 normal/race/CGO0·Django 비교·generated drift·문서

편집 중에는 compile만 확인한다. 구현 묶음이 준비되면 영향 범위의 검증을 모으며 전체 Hosted 통합은 후속 milestone으로 묶는다.

## 결과와 다음

IR·DSL·historical definition·양 DB TEXT·shared string runtime·generated consumer와 Form widget/empty-value 의미를 연결했다.
Helpdesk resolution을 실제 generator의 0003 migration으로 확장하고 OpenAPI·독립 ogen client도 재생성했다.
Local normal/race/CGO0, 기존 DB 보존·실제 Admin/API·Django 입력 비교와 drift 검증을 마쳤다.
소스·환경·실패 수정과 실행 범위는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md), 장기 의미는 [ADR-0060](../docs/adr/0060-text-field-and-form-widget-semantics.md)에 있다.

이 작업의 제한된 Text 범위를 완료했다. 전체 기능 카탈로그나 현재 Text 변경의 Hosted platform 검증을 완료한 것은 아니다.
다음은 모델 시각 필드가 필요로 하는 Query scalar·양 DB 정밀도/시간대와 Form/API 표현을 함께 확장한다.
