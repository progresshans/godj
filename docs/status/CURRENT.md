# 현재 상태

- 갱신: 2026-09-19
- 활성 작업: [GDJ-0074 DateTimeField와 모델 시각 값](../../work/0074-datetime-field-and-model-time-values.md)
- 최근 완료: [GDJ-0073 TextField와 모델의 여러 줄 입력](../../work/0073-text-field-and-multiline-model-forms.md)
- 최근 완료 소스: `fca8cbfa38c072c9f8825f000a90f206ce291ddf`
- 최신 관련 검증: DateTime 작업 사본의 local normal/race/CGO0·SQLite/PostgreSQL·Django 비교·생성 client — [증거](TEST_EVIDENCE.md)
- 최신 전체 검증 소스: `b43552a1f88259babe97ec9fe83951f8cd205261` (GDJ-0072까지)
- 최신 전체 검증: [Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35370184198)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer·Text·DateTime을 모델부터 실제 소비자까지 연결했다. DateTime은 UTC microsecond와 명시적 null을 사용하며
Form/Admin·RFC3339 JSON/OpenAPI·독립 client까지 구현했다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

DateTime local normal/race/CGO0·실제 양 DB·Django 비교·client·drift 검증을 마쳤다. TextField와 DateTimeField의 누적 변경을
같은 통합 source의 Hosted full로 검증한다. 기존 b43552a full은 두 작업의 platform PASS가 아니다.
전체 플랫폼·고정 PostgreSQL·process/reference 결과를 확인한 뒤 다음 모델 기능을 이어간다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정을 두지 않고 남은 기반과 기능을 의존성 순서로 계속 구현한다.
기존 Draft PR #1을 유지하며 정확한 source와 실행 상태는 [TEST_EVIDENCE](TEST_EVIDENCE.md)에 기록한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
