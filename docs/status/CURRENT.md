# 현재 상태

- 갱신: 2026-09-19
- 최근 완료: [GDJ-0073 TextField와 모델의 여러 줄 입력](../../work/0073-text-field-and-multiline-model-forms.md)
- 최신 관련 검증: TextField의 local normal/race/CGO0·SQLite/PostgreSQL·Django 비교·생성 client — [증거](TEST_EVIDENCE.md)
- 최신 전체 검증 소스: `b43552a1f88259babe97ec9fe83951f8cd205261` (GDJ-0072까지)
- 최신 전체 검증: [Hosted full 완료](https://github.com/progresshans/godj/actions/runs/35370184198)

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article·Helpdesk의 Web/Form/Admin/API·영속 인증 흐름이 있다.
일반 signed integer와 Text를 모델부터 실제 소비자까지 연결했다. Text는 저장 길이 제약 없는 문자열이며 Form의 textarea와
빈 입력 정책을 분리한다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

모델 serializer와 같은 operation에서 Article·Helpdesk OpenAPI 3.1 문서를 만든다. Named schema와 JSON 정책을
공유하고 별도 module의 고정 ogen Go client로 Bearer·Session/CSRF·CRUD·관계·정수·여러 줄 본문을 검증한다.

## 다음 행동

모델의 시각 필드와 공통 Query scalar, SQLite/PostgreSQL의 정밀도·시간대, Form/API 표현을 함께 확장한다.
현재 Schema IR·Query에는 시간 scalar가 없으므로 고정 Django 관찰과 저장 표현을 먼저 확인한다.
장기 목표는 헌장·기능 카탈로그의 완성이며 출시 일정을 두지 않고 남은 기반과 기능을 의존성 순서로 계속 구현한다.
TextField의 새 Hosted full은 아직 실행하지 않았으며 이전 full 성공을 이번 source의 platform PASS로 사용하지 않는다.
기존 Draft PR #1을 유지하고 누적 통합 milestone의 검증 범위는 실제 변경에 맞춰 선택한다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
