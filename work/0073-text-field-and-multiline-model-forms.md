---
id: GDJ-0073
status: active
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

- [ ] IR·DSL·historical definition·양 DB의 TEXT 물리 schema
- [ ] shared string query/생성 코드·nullable/default와 실제 외부 소비자
- [ ] 명시적 widget과 Form/Admin·serializer/OpenAPI
- [ ] Helpdesk migration·기존 데이터·긴 본문·생성 client
- [ ] 영향 범위의 normal/race/CGO0·Django 비교·generated drift·문서

편집 중에는 compile만 확인한다. 구현 묶음이 준비되면 영향 범위의 검증을 모으며 전체 Hosted 통합은 후속 milestone으로 묶는다.

## 현재

고정 Django TextField와 현행 Char/Integer의 IR→query→Form/API 경계를 확인했다. TEXT 저장 의미, Form widget·빈 입력 정책과
Helpdesk resolution의 0003 migration·생성물을 구현 중이다. 현재 compile만 확인했으며 회귀 테스트·생성 client·실제 DB 검증은 진행 전이다.
