# ADR-0004: 전역 `godj` CLI와 프로젝트 바이너리의 역할을 분리

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0004-cli-and-project-binary.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 질문: Q-001, Q-010

## 맥락

Django의 `manage.py`는 project settings, installed apps, models, migration, custom commands를 로드하는 프로젝트 전용 진입점입니다. Go에서 Python 파일 모양을 복제할 이유는 없지만 그 역할은 필요합니다.

## 결정

- 사용자 명령 namespace는 `godj`로 통일합니다.
- 전역 CLI는 `version`, `startproject`, `startapp`, 프로젝트 탐색과 build/orchestration을 담당합니다.
- settings/app/model/custom command가 필요한 동작은 project code가 링크된 프로젝트 바이너리가 실행합니다.
- production에서는 같은 project binary가 `serve`, `migrate`, `createsuperuser` 등을 subcommand로 제공합니다.
- `go generate`는 보조 진입점이고 `godj generate`가 공식 명령입니다.

## 결과

Django의 project-aware command 경험을 Go build/deployment 모델에 맞게 보존합니다. 전역 CLI와 project library/generator version mismatch, build cache, codegen bootstrap을 명시적으로 처리해야 합니다.

## 의도적으로 결정하지 않은 것

project entrypoint API, `godj.toml` schema, temporary runner 구현, binary command protocol은 정하지 않았습니다.
