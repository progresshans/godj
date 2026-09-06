# 활성 작업

[CURRENT](../docs/status/CURRENT.md)가 활성 작업을 가리킨다. 현재 통합 작업은
[GDJ-0057 개발 구조 정리](0057-development-simplification.md)다.

work는 지금 수행할 결과, 변경 범위, 필요한 검증과 다음 행동을 기록한다. 작은 수정마다 새 문서를 만들지 않는다.
독립 작업은 코드·공개 의미·DB/port/temp 자원 소유자를 구분하고 전역 상태는 통합 담당 한 명이 갱신한다.

## 이전 작업

완료된 GDJ-0000..0055와 superseded GDJ-0035의 문서는
[고정 Git 이력](https://github.com/progresshans/godj/tree/003afee4524a0294ada8f02c140781f3e1751a5c/work)에 보존한다.
이전 작업의 full matrix·inventory·문서 절차는 새 작업의 선행 조건이 아니다.
[GDJ-0056](https://github.com/progresshans/godj/blob/da1bfc524c4f205075fc7fac7f00b437473a5e1f/work/0056-sqlite-retained-connection-terminal-quarantine.md)의 quarantine 코드는 이번 기준 source에 포함되어 있으며, 미완료 검증은 CURRENT와 이번 작업에서 추적한다.

장기 이유는 [ADR](../docs/adr/README.md), 실제 실행 결과는 [Evidence](../docs/status/TEST_EVIDENCE.md)를 따른다.
