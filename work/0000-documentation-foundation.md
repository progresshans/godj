---
id: GDJ-0000
status: completed
updated: 2026-08-07
baseline_branch: "main"
baseline_commit: "fe7bf44"
depends_on: []
contracts: []
allowed_paths: ["README.md", "AGENTS.md", "docs/**", "work/**", "prompts/**"]
integration_owner: "primary agent"
---

# 문서·결정·인수인계 기반 수립

## 사용자에게 보이는 결과

새 대화, 새 사람, 새 에이전트가 현재 방향과 실제 진행 상태를 혼동하지 않고 다음 작업을 재개할 수 있습니다.

## 입력

- 참조 ChatGPT 대화 `Django 성능과 비동기 처리`
- 첨부된 2,623줄 GoDj 종합 설계안
- 초기 `README.md`만 있는 GoDj Git 저장소와 Django 로컬 reference checkout
- 2026-08-07 공식 Django, Go, OpenAI 문서 확인

## 목표

- 긴 설계안을 제품 헌장, 아키텍처, 호환성, 테스트, 로드맵, ADR, 상태, work item으로 분리
- Accepted 원칙과 prototype 전 가설을 분리
- contract-first vertical slice 실행 순서 정의
- 자동으로 읽히는 짧은 `AGENTS.md`와 재개 프롬프트 제공
- 첫 두 구현 work item의 범위와 gate 정의

## 비목표

- Go module 또는 package 생성
- Schema/ORM/CLI/conformance harness 구현
- Django checkout 수정
- commit/push/PR

## 핵심 판단

- 초안의 전체 기능 범위는 장기 제품 비전으로 보존합니다.
- 정확한 API 예시는 compile/runtime prototype 전에는 확정하지 않습니다.
- 기존 Milestone 1처럼 모든 Schema/Codegen 골격을 먼저 완성하지 않고, M0 Compatibility Lab 후 한 모델의 전체 query 수직 단면을 구현합니다.
- codegen bootstrap, dynamic lookup chaining, relation typing, QuerySet cache는 명시적 open question입니다.
- prompt는 정본이 아니며 `AGENTS → CURRENT → active work → linked ADR/spec`을 읽게 합니다.

## 완료 조건

- [x] 저장소와 reference 상태 확인
- [x] 공식 Django/Go/Codex 기준 확인
- [x] root/document/status/ADR/work/prompt 구조 작성
- [x] 다음 M0/M1 범위와 gate 문서화
- [x] Markdown link와 문서 일관성 검증
- [x] Git 신규 파일과 whitespace 검토
- [x] CURRENT, TEST_EVIDENCE, 이 파일을 최종 결과로 갱신

## 테스트 증거

[EVID-20260807-001](../docs/status/TEST_EVIDENCE.md#evid-20260807-001--documentation-foundation-validation): 33개 Markdown parse, local link, heading, trailing whitespace, Git whitespace 검사를 통과했습니다. Go 코드 테스트는 실행 대상이 없었습니다.

## 다음 정확한 작업

사용자가 구현 시작을 요청하면 GDJ-0001의 baseline을 현재 checkout으로 갱신하고 상태를 `active`로 바꿉니다.

## 결과와 인수인계

- 32개 Markdown 파일을 새로 작성하고 기존 `README.md`와 연결했습니다.
- 실제 Go 코드, test harness, CI는 만들지 않았습니다.
- 파일은 모두 untracked이며 commit/stage/push하지 않았습니다.
- 다음 작업의 범위와 gate는 [GDJ-0001](0001-compatibility-lab.md)에 준비되어 있습니다.
