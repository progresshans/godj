---
id: GDJ-0108
status: complete
updated: 2026-10-02
baseline_commit: "8f8831ac8231a9c32049b6649e49f8638edd5117"
integration_owner: "root"
---

# 행 잠금과 조회 후 생성 또는 갱신

## 결과와 범위

모델 query의 행 잠금을 공통 AST와 native transaction에 연결하고, 존재 여부에 따라 생성 또는 갱신 입력을
지연 평가하는 update-or-create를 제공한다. Helpdesk의 티켓별 ServiceReport 저장 흐름에서 현재 권한·관계 범위·
감사 기록과 함께 소비한다. SQL 문구를 붙인 뒤 성공을 가정하거나 application mutex로 DB 잠금을 대신하지 않는다.

기반은 GDJ-0107의 fresh 단건 조회·metadata snapshot·savepoint와 확인된 unique rollback 뒤 한 번의 재조회다.
해당 기반의 고정 source `8f8831ac`는 [Hosted 전체 통합](https://github.com/progresshans/godj/actions/runs/36866445270)의
65개 job·필수 owner·집계와 새 capture 결합까지 완료했다. 완료 기록 `f04bb77d`를 이 작업 사본에도 합쳤다.
이 작업의 새 제품 변경을 위 검증 source와 구분한다. 전체 목표는 헌장과 기능
카탈로그의 완성이며 bulk와 다른 미완료 기능도 계속 남아 있다.

## 구현과 검증

- [x] 고정 Django의 입력 분기·잠금 대상·경쟁·terminal 동작을 독립 observer와 양 DB fixture로 정리
- [x] DB 독립 잠금 AST·copy/cache 소유권·typed/dynamic 및 일반/eager/prefetch query의 연결
- [x] PostgreSQL의 실제 transaction·대상·강도·대기 정책과 SQLite의 명시적 capability 경계
- [x] 원자 update-or-create·분기별 지연 입력·실제 생성 경쟁·빈 갱신·scope 종료와 실패 경계
- [x] 생성 facade와 별도 Go module에서 양 DB의 성공·거부·경쟁·수명 동작을 소비
- [x] Helpdesk ServiceReport의 현재 권한·Category/Ticket 범위·원자 audit와 Form/Admin/API·독립 client
- [x] 완성한 변경 묶음의 영향 normal/race/CGO=0·양 DB·실제 경쟁·실패 대조와 생성 drift
- [x] 고정 source의 Hosted 통합 및 현재 구현·검증·남은 범위의 전달

동작 의미는 [ADR-0087](../docs/adr/0087-row-locking-and-update-or-create.md)을 따른다.
구체적인 Go API 서명은 생성된 소비자와 업무 흐름의 수직 구현에서 검증했다.
실행 증거는 [TEST_EVIDENCE](../docs/status/TEST_EVIDENCE.md)에만 기록한다.

## 현재 상태와 다음 행동

고정 Django에서 기본 생성/갱신 분기, 양 DB 경쟁, 잠금 query의 terminal과 선택 대상·FK 경쟁을 독립 관찰하고
Go 생성 소비자와 직접 대조했다. SQLite의 잠금 없음과 갱신 시 busy, PostgreSQL의 행 잠금·
unique 생성 경쟁·잠금 대상 및 FK 참조 경쟁을 구분한다. projection에서 원 모델의 열을 제외할 때 Django의
`of=self`가 더 넓은 잠금으로 바뀌는 관찰 결과도 명시적인 Go 대상 보존 계약과 구분한다.

AST·compiler·native session/cursor, typed/dynamic ORM·일반/eager/prefetch 생성 facade와 중첩 graph의 잠금 갱신을
구현하고 local normal/race/CGO=0 영향 checkpoint를 완료했다. UpdateOrCreate와 생성 facade도 같은 세 mode에서
검증했고 정식 기준 fixture와 생성 Go module의 직접 대조를 완료했다. 실제 외부 FK 갱신·하위 조회 실패/재시도·
잠금 범위 비확대와 기존 snapshot 보존을 생성 소비자로 확인했다. Helpdesk 저장 흐름의 실제 양 DB·동시 요청·
Admin/API·독립 client를 세 mode에서 확인했고, 브라우저·훼손 대조·생성 drift와 영향 vet도 완료했다.
새 고정 source `f6e95bb8`의 [Hosted 전체 통합](https://github.com/progresshans/godj/actions/runs/36900514942)을 완료했다.
65개 job·모든 필수 owner/단계·최종 집계와 새 capture의 Git source 결합·실제 소비를 확인했다.
다음 bulk 작업은 별도 source에서 이어가며 이 Hosted 성공으로 그 새 source를 인증하지 않는다.
