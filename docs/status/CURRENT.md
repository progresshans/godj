# 현재 상태

- 갱신: 2026-09-08
- 활성 작업: [GDJ-0063 제품·검증 코드의 책임 정리와 결함 수정](../../work/0063-runtime-and-validation-ownership.md)
- 최근 완료: [GDJ-0062 테스트·검증 지원 코드의 중복 점검](../../work/0062-validation-duplication-audit.md)
- 최근 검증 소스(full scope): `c5b91c812af87c1450f5fdb881cb751491eb76be`
- 작업 브랜치: `codex/revision-fenced-migration-lifecycle`

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article 기반 Web/Form/Admin/API·영속 인증의 제한된 수직 단면이 있다.
범용 기능 전체를 지원한다는 뜻은 아니다. 현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.
Helpdesk는 티켓과 배정 Category를 한 번에 읽는 상세 API를 제공한다.

## 현재 작업

세션 만료 경쟁과 showmigrations Unicode identity 검증을 수정하고 protocol·CLI·migration·DB AST·검증 실행의 책임을 정리한다.
설계 채택·구현·환경별 검증을 구분한다. 위의 최근 full scope 결과는 GDJ-0062 소스이며 진행 중인 변경의 PASS가 아니다.

## 다음 행동

[활성 work](../../work/0063-runtime-and-validation-ownership.md)의 구현과 집중 검증을 마쳤다. 통합 checkpoint를 닫고
고정 구현 소스의 Hosted full scope를 실행한다. 현재 blocker는 없다.
eager Count·다중 관계 탐색은 별도 범위다. 기존 PR #1은 Draft로 유지한다.

## 검증과 제한

[테스트 증거](TEST_EVIDENCE.md)에 실제 명령·source·환경을 기록한다.
현재 지원은 제한된 기능 집합이며 eager Count·다중 관계 탐색 등 미지원 경계는 현행 명세를 따른다.
과거의 상세 진행 기록은 기준 commit의 Git 이력에 있으며 현재 상태의 정본으로 사용하지 않는다.
