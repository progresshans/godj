# ADR-0006: Codegen 입력은 generated target package와 분리한다

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 질문: Q-001
- 검증: `conformance/codegenbootstrap`

## 맥락

Schema rename이나 field 삭제 후에는 오래된 generated type과 새 사용자 메서드가
서로 맞지 않아 target package가 compile되지 않을 수 있습니다. Generator가 이
package를 import해야 schema를 읽을 수 있다면 새 코드를 만들기 위해 먼저 새 코드가
필요한 순환 bootstrap이 생깁니다.

## 결정

- Schema 선언 package와 generated model target package를 import graph에서
  분리합니다.
- Generator와 프로젝트 schema runner는 선언 package와 Schema IR만 import하며
  generated target package를 import하지 않습니다.
- Codegen의 유일한 의미 입력은 정규화된 Schema IR입니다.
- M1까지 generated output은 package당 한 파일로 제한하고 candidate를 별도 위치에
  만든 뒤 format·parse·target compile 검증이 성공한 경우에만 교체합니다.
- Candidate 검증 실패 시 마지막 정상 generated output을 byte 단위로 보존하고,
  사용자 source 위치가 포함된 compile 오류를 반환합니다.

Project runner를 임시로 생성할지 checked-in bootstrap binary로 둘지는 이 결정에
포함하지 않으며 Q-010에서 다룹니다.

## 검토한 대안

### Generated target을 import하는 임시 runner

Target이 깨진 상태에서 runner 자체가 compile되지 않아 기각합니다.

### 같은 package에서 제한적 Go AST 추출

깨진 package를 type-check하지 않고 읽을 수 있지만 helper, constant resolution,
build tag를 제한하는 별도 소언어가 됩니다. 초기 기본 경로로 채택하지 않습니다.
나중에 declaration ergonomics가 실제 문제로 측정되면 보조 입력기로 다시 검토할 수
있습니다.

### 별도 JSON/YAML schema 원본

Bootstrap은 단순하지만 채택된 Go 기반 Schema DSL 방향과 Schema IR 단일 원본
규칙을 흐릴 수 있어 대조군으로만 남깁니다.

### Compile 가능한 bootstrap package

선언/generated package 분리 위에 project-aware runner를 고정하는 변형입니다. 전역
CLI와 project library version protocol이 정해진 뒤 별도로 결정합니다.

## 결과

Rename으로 target package가 깨져도 generator build graph는 정상이고 새 output을
만들 수 있습니다. Field 삭제 뒤 사용자 메서드가 stale field에 의존하면 candidate
compile이 실패하므로 기존 output이 보존됩니다. 사용자는 선언 package와 생성 모델
package를 구분해야 합니다.

## 검증

`conformance/codegenbootstrap/bootstrap_test.go`는 다음을 실행합니다.

- broken target에서 model/field rename 복구
- field delete 후 stale 사용자 메서드 오류와 last-good output hash 보존
- 사용자 메서드 수정 후 성공과 byte-identical 재생성
- malformed schema/output 실패 시 기존 파일 보존
- `go list -deps`에 generated target package가 없음을 확인

Fixture DSL과 generator는 production API가 아닙니다. 최초 생성, 다중 파일
transaction, Windows 교체 의미, build tags, cross-app relation은 후속 수직 단면에서
별도로 검증합니다.
