# GDJ-0069 — 바인딩·쿼리 준비와 감사 후속 개선

- 상태: F1~F8 구현·전후 측정·로컬 통합 검증 완료. Hosted full scope는 아직 실행 전.
- 기준 소스: `71ba61f0ecf26d397b10ac207212146f27441de7`.
- 요청: 외부 감사 F1~F8을 빠짐없이 계획하고 동작·오류·무결성을 보존하며 엄격하게 수정한다.
- 구현과 통합 기록 소유자: 현재 작업 담당자 한 명. 기존 Draft PR #1과 작업 브랜치를 유지한다.

## 변경 범위와 보존할 위험

| 항목 | 구현·조사 범위 | 보존 조건 | 상태 |
|---|---|---|---|
| F1 | ReverseObject의 반복 정적 검사를 바인딩 경계로 모으고 prefetch의 중복 private field를 제거했다. | 공개 metadata·descriptor snapshot 격리, zero/nil·backend·PK/callback 검사, 오류 순서, 독립 cache·취소·동시 사용 | 구현·개별 검증 완료 |
| F2 | CheckHistory 내부 읽기 전용 map 전달에서 복사를 제거했다. | Plan의 mutable working 복사, caller input 복사, canonical history 오류 순서, 반복·동시 사용 | 구현·개별 검증 완료 |
| F3 | PostgreSQL 컴파일의 IN 값을 한 번 준비해 기존 leaf 순서로 검증/출력에서 재사용한다. | 공개 Values 복사, scalar/relation/aggregate와 SQL·인자 순서, 오류 우선순위, bounded·compile-local 소유권 | 구현·실제 PostgreSQL 검증 완료 |
| F4 | 8/64/256/1023개 조건의 chain/batch 의미·비용을 측정하고 Article 검색의 최대 6개 조건을 모았다. | AST 불변성·표현식 한도·오류 순서·QuerySet 평가 소유권; 실측 없는 AST 전면 변경 금지 | 구현·개별 검증 완료 |
| F5 | 경로 개수 계산의 임시 Split을 Count로 바꿨다. | 루트와 malformed 입력의 결과, URL 검증 순서 | 구현·개별 검증 완료 |
| F6 | 미사용 canonicalUnsigned와 regexp import를 제거했다. | 실제 wirejson 정수 검증과 외부 공개 API 유지 | 구현·개별 검증 완료 |
| F7 | Article PostgreSQL setup, 두 conformance 성공 검증의 준비, 동등 slice 비교를 같은 패키지 안에서 정리했다. | 독립 schema·flow·oracle/actual, fixture hash·로드 보고서, required-DB 정책, redaction, context/cleanup 수명 | 구현·실제 PostgreSQL/CLI 검증 완료 |
| F8 | 실제 SQLite/PostgreSQL의 34 workload를 전후 각 3회 측정하고 digest의 임시 decode 할당을 제거했다. | bounded streaming, digest 중복·손상 감지, 전체 검사 후 최소 ID 만료 행 1개 삭제, 원자 gate와 cross-runtime fence | 실측·동등 수정·로컬 통합 완료 |

F8은 보고서에서도 병목이 확정되지 않은 항목이다. 측정 없이 COUNT·조기 삭제·검증 생략을 도입하지 않는다.
동일 계약의 개선이 확인되면 적용하고, 스키마·무결성 정책 변경이 필요한 경우 그 근거와 측정치를 남겨 현재 정책 유지와
별도 설계 범위를 명확히 결정한다. 조사 미실행을 완료로 표시하지 않는다.

F8 측정은 64/1024/4096 용량의 25%·포화·만료 행 포함 포화와 1024/4092행의 create-delete·rotate,
독립 backend/Runtime 1개·4개를 포함한다. 포화 payload 검증과 행 수에 따른 inventory 비용이 실제로 남는다.
Canonical digest를 동일한 소문자 ASCII hex로 검증하되 decode 결과를 만들지 않는 수정만 채택했다.
전체 스캔 제거는 constraint·만료 metadata·손상 검사 책임의 변경이 필요하므로 현행 정책을 유지한다.
세부 측정과 실패한 초기 benchmark 설정은 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 기록한다.

## 진행 순서

1. F2/F5/F6의 작은 제품 변경과 기존 검증 범위를 정리한다.
2. F1의 바인딩·실행 책임과 prefetch 영향 범위를 정리하고 snapshot·negative·동시 사용 회귀를 확인한다.
3. F3의 compile-local 준비와 F4의 호출부 batch를 구현하고 실제 SQL 컴파일·조건 수별 전후 측정을 남긴다.
4. F7의 준비를 공유하고 독립 실행·기대값·환경 실패 정책의 유지 여부를 확인한다.
5. F8의 실제 DB 측정과 동시성·무결성 대조 후 변경/유지 결정을 기록한다.
6. 관련 normal checkpoint, race/CGO-disabled·실제 DB·외부 생성 소비자·generated drift를 통합 검증한다.
7. 자체 diff 검토와 누락 대조를 마친 제품 소스를 기존 Draft PR에 반영한다. Hosted full scope의 개별 job와 필수 증거가
   같은 소스에서 모두 terminal 성공해야 전체 완료로 기록한다. 실패 시 원인을 수정하고 영향받은 검증을 다시 수행한다.

## 검증 소유권

- 편집 중에는 필요한 compile 확인만 하고, 의미 묶음이 완성되면 gofmt·affected test를 실행한다.
- 전후 benchmark는 지원 Go 1.26.5와 같은 workload에서 반복한다. 각 효과와 constructor 비용을 구분하고 DB/HTTP 전체
  개선으로 확대하지 않는다. 외부 감사의 Go 1.23.2 수치를 제품 검증으로 사용하지 않는다.
- 로컬은 변경된 위험의 통합 checkpoint를 맡고 전체 platform/cold build는 Hosted milestone 한 곳이 맡는다.
- 실제 PostgreSQL 테스트는 필수 환경을 명시하며 skip을 PASS로 간주하지 않는다. 로컬 또는 Hosted 중 실행한 환경을 밝힌다.
- 테스트 삭제·통합 시 유지되는 위험과 assertion 소유자를 대조한다. DB fixture와 oracle을 공유해 독립성을 잃지 않는다.
- 실행 상세는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md), 현재/다음 상태는
  [CURRENT](../docs/status/CURRENT.md), 장기 소유권 변경은 관련 현행 ADR와 동시성 문서에 반영한다.

## 완료 조건

- F1~F8 각각 구현 또는 측정에 근거한 명시적 판단과 잔여 범위가 있다.
- 동작·오류 순서·입력/반환/callback 소유권·보안/무결성 조건을 약화시키지 않는다.
- 관련 생성물과 외부 소비자가 실제 변경 소스로 검증되고 모든 실행 누락·skip·실패가 해소되거나 비대상으로 명시된다.
- 제품 소스와 Hosted 전체 결과·증거의 연결을 확인하며 현재 변경과 다른 소스의 PASS를 혼용하지 않는다.
