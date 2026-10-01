# GDJ-0067 불변 준비와 실행 비용 정리

- 상태: 완료 — 제품 `56303abeefd1c911ad5954cd062a2e4ed67ce41e`의 [Hosted full scope](https://github.com/progresshans/godj/actions/runs/34432064345) 검증 포함
- 기준: `9c21568dcbd7bb33c817d6e830a44026d2554e32`
- 요청: 전체 감사의 수정·추가 후보를 엄격하게 구현하고 추가 탐색까지 완료한다.

## 목표와 완료 조건

실행 경로의 반복 순회·복사·준비를 줄이고 같은 검증의 중복 실행 책임을 정리한다.
성능 수치나 줄 수 자체를 목표로 안전장치를 제거하지 않는다. 제품·생성기·회귀 테스트를 같은 변경 묶음으로 완성한다.
측정 가능한 경로는 같은 benchmark의 변경 전후를 비교한다. 수치가 없는 개선은 구조 변경으로만 기록한다.

| 범위 | 구현 목표 | 보존할 검증 |
|---|---|---|
| 인증·CSRF·쿠키 source | panic 시 잠금 해제, 해싱 계산은 잠금 밖에서 실행 | 같은 panic 전파, 후속 호출 완료, 병렬 사용·기존 보안 흐름 |
| JSON immutable value | 생성 시 검증된 컨테이너의 반복 재귀 검증 제거 | zero·UTF-8·NUL·순서·오류 우선순위·정확한 budget |
| JSON 문자열·출력 | 호출 내 encoder/scratch 재사용, 독점 버퍼의 소유권 이전 | 표준 JSON escape 의미, 출력 상한, 반환 값 간 독립성 |
| 템플릿 | 고정 상속/block 준비, 독점 출력 버퍼 반환 | include·중첩 block·오류 위치·깊이·취소·동시 render |
| ORM prefetch | 함수 내부 그룹의 중간 복사 제거 | callback 입력 격리·중복 owner 캐시 독립성·실패 시 미게시 |
| ORM manager | 명시적인 metadata snapshot으로 기본 plan 재사용 | mutable descriptor 입력과 getter 격리·파생별 평가 cache |
| 관계 compiler | DB 독립 join 계획 공통화 | 각 DB의 capability·SQL·NULL·alias·오류·실제 query 결과 |
| 라우팅 | 일치하는 method/path에서 조기 반환 | 정적 우선순위·파라미터·404/405와 Allow |
| 외부 compile | ABI 검증과 부모 race 검증의 실행 책임·의존성 준비 정리 | 모든 정상/오용 fixture·OS/arch/CGO·offline compile·완료 판정 |
| Python | normal/exact/compatibility 완료 판정의 공통 실행 경로 | 발견/시작/종료·프로필별 허용 skip·실패/expected failure 거부 |
| 작은 정리 | 폼 선택 필드 복사, Registry 임시 index, history fingerprint 재사용 | 입력 독립성·중복 이름 거부·독립 actual 관측 |
| 추가 탐색 | 인접 호출과 전역 유사 패턴 재검토, 타당한 후보 구현 | 다른 의미의 정책·독립 oracle·DB/fence/publication 경계 보존 |

## 검증 소유권

1. 묶음 편집 중 compile 확인, 완료 후 gofmt·affected normal·필요한 generated drift.
2. 변경한 runtime·compiler·검증기와 관련 실제 SQLite/HTTP/관계 회귀를 로컬 통합 checkpoint에서 실행.
3. 관련 race·CGO-disabled를 모아서 실행. 테스트 통합 시 계속 검증하는 위험과 실행 owner를 명시.
4. 기존 Draft PR #1의 동일 제품 소스 Hosted full scope가 PostgreSQL·전체 OS/arch·cold build·process 경계를 소유.
5. 오류·skip·누락·진행 중을 PASS로 쓰지 않는다. 제품 소스 최종 검증 뒤 문서-only 증거 정리는 별도 검사.

장기 의미는 현행 ARCHITECTURE·CONCURRENCY·TESTING과 해당 ADR에 반영한다.
실제 명령·측정·환경·source·결과는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에 한 번 기록한다.

## 진행

- [x] baseline 측정과 수정 묶음
- [x] 전체 감사 항목 구현·추가 탐색
- [x] 영향 범위 normal·race·CGO-disabled·generated·Python/reference 검증
- [x] 동일 제품 소스 Hosted full scope 완료
- [x] 현행 계약·최종 실행 증거·CURRENT 정리

## 추가 탐색 처리

- Hash의 entropy 뒤 취소 재확인, JSON 정수 임시 할당, project-bound 관계 기본 plan의 재사용을 함께 적용했다.
- Migration/ShowMigrations/SQLMigrate의 outer child 실패 분류를 공유하되 각 runner code·response·durable outcome 판정은 유지했다.
- Attestation source framing 검증을 기존 공용 package로 옮겼다. SYS-020/SYS-029의 타입·범위·한도는 각 owner가 유지한다.
- 생성 소비자 자식의 실제 race 계측과 결과 cache 비사용, 외부 ABI의 offline checksum 준비 및 필수 completion roster를 명시했다.
- 공통화하지 않은 경계: check/generate/makemigrations/operator의 서로 다른 process stage·실패 grammar와
  durable publication, callback 입력·public 반환·중복 owner 캐시의 방어 복사, 독립 DB 관측/oracle.
  Runserver의 writer는 worker goroutine에서 실행되어 인증 요청의 outer recover 뒤 잠금 잔류 문제와 같은 수명 경계가 아니다.
