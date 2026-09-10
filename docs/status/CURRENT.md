# 현재 상태

- 갱신: 2026-09-10
- 활성 작업: [GDJ-0067 불변 준비와 실행 비용 정리](../../work/0067-immutable-preparation-and-execution-cost.md)
- 최근 완료: [GDJ-0066 실행 비용과 중복 책임의 측정 기반 정리](../../work/0066-measured-runtime-and-validation-optimization.md)
- 이전 전체 검증 소스: `b028b77fa27f680c82f4cb188d5af1088dbb61c2`
- 이전 검증: [GDJ-0066 Hosted full scope 완료](https://github.com/progresshans/godj/actions/runs/34385261400)
- 작업 브랜치: `codex/revision-fenced-migration-lifecycle`

## 현재 구현

Schema/Codegen/ORM/Migration, SQLite·PostgreSQL과 Article 기반 Web/Form/Admin/API·영속 인증의 제한된 수직 단면이 있다.
현재 지원 범위는 [구현 현황](IMPLEMENTATION_MATRIX.md)을 따른다.

GDJ-0066에서 쿼리 파생 복사, 세션의 중복 transaction/read, 감사 prune의 행 전송, 관계 Count와 eager materialization,
migration ancestor index 및 외부 빌드·관측 helper를 정리했다. 관련 로컬 compile·normal·race·CGO-disabled·생성물·
Python/reference·외부 다섯 명령 검증과 같은 제품 소스의 Hosted 전체 검증을 마쳤다.
항목별 처리와 실행 범위는 [작업](../../work/0066-measured-runtime-and-validation-optimization.md)·[테스트 증거](TEST_EVIDENCE.md)에 기록했다.

## 다음 행동

GDJ-0067의 인증 callback, JSON·템플릿·ORM 준비/복사, 관계 compiler와 검증 실행 책임을 구현하고
변경 전후 측정·영향 범위 normal/race/CGO-disabled·생성물·고정 Python/DRF 검증을 마쳤다.
기존 Draft PR #1에서 동일 제품 소스의 Hosted 전체 검증을 수행한다. PostgreSQL·전체 OS/arch·cold build는 아직 미검증이며
위 전체 검증 결과는 GDJ-0066 제품 소스에 해당한다. [실행 증거](TEST_EVIDENCE.md)를 따른다.
임의 깊이/다중 관계 탐색의 기능 확장은 GDJ-0066에 포함하지 않았다.

## 근거

장기 의미는 관련 ADR·[아키텍처](../ARCHITECTURE.md)·[동시성](../CONCURRENCY.md), 실제 실행은
[테스트 증거](TEST_EVIDENCE.md)를 따른다. 과거 상세 기록은 기준 commit의 Git 이력에 있다.
