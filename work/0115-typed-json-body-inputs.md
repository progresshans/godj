---
id: GDJ-0115
status: complete
updated: 2026-10-07
baseline_commit: "32ad43bd934789dfb9ba3174bf43ac89a50b51e8"
integration_owner: "root"
---

# Typed JSON body와 모델 입력 연결

## 문제와 결과

모델 Serializer의 검증과 OpenAPI는 같은 Spec을 사용하지만 handler는 정리된 값을 이름으로 꺼낸 뒤
타입 assertion과 presence 분기를 직접 반복한다. 기존 DRF 입력 의미를 재구현하지 않고 같은 Spec의
full/partial 검증 결과를 typed DTO·부재/null·OpenAPI와 연결한다. 모델 의미는 Schema IR에서 가져오고,
노출·권한·관계/고유성 확인·transaction·출력/audit는 기존 업무 owner를 유지한다.

우선 실제 Label과 ServiceReport의 생성·PUT/PATCH 및 ensure/save 명령을 연결한다. 두 모델의 서로 다른 입력과
기본값·관계 검증 순서를 확인한다. 기반은 현재 Serializer가 지원하는 scalar와 integer-list의 닫힌 typed 변환을
제공하며 nullable, legacy omission default, choices, temporal/Decimal/Binary/JSON·collection 소유권은 별도로 검증한다.
Ticket의 기존 validator·bulk 배열 binding을 이번 연결에 무리하게 다시 쓰지 않는다.

## 선행 I/O 경계

기존 `api.Parser`의 작은 실제 Web request 재현에서, context를 읽기 전에 취소하거나 첫 Read에서 취소해도
Body를 두 번 읽고 정상 object를 반환하는 것을 확인했다. 정상 입력과 두 취소 사례를 독립 재현 코드로 기록했다.
Typed 연결 전에 공통 object/model/list parser가 request context를 읽기 전·Read 사이·검증/반환 경계에서 확인하도록
보완한다. 취소와 원 reader 오류를 보존하고 부분 결과를 반환하지 않는다. Borrowed body를 임의로 닫거나 별도
goroutine에서 읽지 않는다. Blocked Read를 해제하는 수명은 body/transport의 소유자에게 남는다.

## 설계 경계

- Spec의 정규화·오류 순서·read-only·choices·full default·partial omission을 재사용한다. Query용 canonical/digits
  codec으로 JSON 입력 의미를 대체하지 않는다. 검증하지 않는 omission default를 임의로 재검증/정규화하지 않는다.
- DTO setter와 정리된 scalar의 Go type은 compile로 연결한다. Kind/nullability·누락/중복 이름·nil/zero·잘못된
  설정은 준비 때 거부한다. Struct reflection이나 임의 schema/decoder 쌍을 허용하지 않는다.
- 생략과 effective 값의 존재를 분리하고 nullable 값은 Go type에도 드러낸다. Full에서 적용한 default는 정리된
  present value이며 partial에서는 default를 추가하지 않는다. JSON literal null과 모델 null의 기존 의미를 보존한다.
- 전체 검증/변환이 성공하기 전에 setter를 호출하지 않는다. Validation 실패에 부분 DTO가 없고 setter도 실행하지
  않는다. 늦은 취소에도 부분 결과를 노출하지 않는다. Setter는 순수하며 DTO pointer를 외부에 보관하지 않는다.
- 선언/metadata와 요청 결과의 소유권, integer-list 및 nullable pointer의 복사, whole-body byte/depth/value 한도,
  실제 인가/CSRF/대상 lookup 순서와 unknown outcome을 유지한다.

`input.New(spec, parserConfig, Field...)`가 불변 `Body[T]`를 준비한다. `Presence[V].Get()`은 effective 값의 존재를,
`Nullable(codec)`의 pointer는 모델 null을 표현한다. `Parse`/`Bind`/`Schema`가 같은 Spec을 소비한다.
Header/path·endpoint DSL·viewset·배포 SDK 전체의
완료를 주장하지 않는다. 장기 의미는 기존 [ADR-0058](../docs/adr/0058-model-derived-openapi-and-operation-ownership.md),
실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 구현과 검증

- [x] 기존 Spec/모델/handler의 입력 순서 확인과 body context 취소 문제 재현
- [x] 공통 parser의 취소·읽기 오류·borrowed body/부분 결과 경계 보완
- [x] Spec/IR 기반 typed body와 presence·현재 scalar/collection 변환·같은 schema
- [x] Label/ServiceReport의 실제 full/partial·업무 명령 연결
- [x] 외부 compile·진단/소유권·actual HTTP/client·필요한 영향 세 mode/양 DB/drift·정리
- [x] 그룹/typed 출력/query/body를 포함하는 후속 source의 전체 platform/Hosted 통합

공통 runtime·양 DB 업무/독립 client·외부 compile과 static/drift·정리를 영향 세 mode에서 확인했다.
Source `def77d5e1c949a87d538181d05a72a3c75e97201`의 [Hosted full 37569379536](https://github.com/progresshans/godj/actions/runs/37569379536)
attempt 2에서 필수 owner·capture/source 결합·최종 집계까지 완료했다. GDJ-0112~0115의 통합 증거이며
후속 기능의 검증으로 전이하지 않는다. 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 둔다.
