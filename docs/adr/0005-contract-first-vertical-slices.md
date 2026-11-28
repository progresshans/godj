# ADR-0005: Contract-first 수직 단면으로 구현한다

> 결정 이유를 보존한 기록이다. 현재 API·지원 범위는 [현행 아키텍처](../ARCHITECTURE.md)와
> [구현 현황](../status/IMPLEMENTATION_MATRIX.md)를 따른다. 옛 내부 파일 구성·단계별 검증 절차는 현재 호환 요구가 아니다.
> 당시의 전체 기록과 실행 증거는 [고정 원문](https://github.com/progresshans/godj/blob/003afee4524a0294ada8f02c140781f3e1751a5c/docs/adr/0005-contract-first-vertical-slices.md)에 있다.

- 상태: Accepted
- 날짜: 2026-08-07
- 관련 문서: [Compatibility](../COMPATIBILITY.md), [Testing](../TESTING.md), [Roadmap](../ROADMAP.md)

## 맥락

GoDj의 장기 범위는 ORM, migration, Form, Admin, API, Realtime, GIS, 다중 DB까지 매우 큽니다. 계층별 골격을 모두 먼저 만들면 사용자 동작으로 검증하기 전에 public API와 package boundary가 굳을 수 있습니다. 반대로 Django 테스트 전체를 구현 전에 번역하면 conformance harness 자체가 또 하나의 대형 프로젝트가 됩니다.

## 결정

구현 순서는 다음 feedback loop를 반복합니다.

```text
외부 동작 contract 정의
→ 정확한 Django oracle 고정
→ 최소 end-to-end GoDj 단면 구현
→ differential/invariant/Go-native test 통과
→ API와 ADR 보정
→ 다음 contract group 확장
```

M0는 8~12개 계약과 oracle/comparator를 검증하고, M1은 한 모델의 DSL부터 SQLite 결과까지 연결합니다. milestone 완료는 package 수가 아니라 contract gate로 판단합니다.

## 결과

설계 가설이 실제 Django 의미와 Go compile/runtime 사용성으로 일찍 검증됩니다. 초기에는 일부 계층이 의도적으로 좁고 임시 test provisioner를 사용할 수 있습니다. 범위를 넓히기 전에 수직 단면 품질을 유지해야 합니다.

## 의도적으로 결정하지 않은 것

초기 manifest encoding과 exact scenario adapter layout은 GDJ-0001에서 결정합니다.
