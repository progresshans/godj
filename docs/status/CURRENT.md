# 현재 상태

- 갱신: 2026-10-19
- 현재: [GDJ-0133 명시적 시간대·지역별 기한 달력](../../work/0133-named-timezones-and-local-deadline-calendar.md)의 구현·검증
- 최근 전체 검증 제품·생성기·소비자 source: `c12b5c0fb95afa439b37f660ffad081003ebae64`
- 최근 전체 통합: [CI 39091754185](https://github.com/progresshans/godj/actions/runs/39091754185), attempt 1의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0131의 정확한 Duration 배율·나눗셈·비율과 라벨별 조정 시간,
GDJ-0132의 Date/Time 산술과 서비스 일정 미리보기를 공통 AST·양 backend·생성 소비자·HTML/API/client에 연결했다.
같은 source의 양 DB·독립 Django 비교·생성 소비자·HTML/API/client의 세 mode 실행과
실제 Chromium 브라우저·전체 플랫폼의 필수 실행 및 원본을 대조했다. [Draft PR #2](https://github.com/progresshans/godj/pull/2)의
이 구현 범위는 완료했으며 프레임워크 전체 목표와 구분한다. 현재 외부 입력이 필요한 blocker는 없다.

GDJ-0133은 UTC에 고정된 기한 달력에 명시적인 업무 시간대를 연결한다. 고정 IANA 데이터·불변 값·공통 AST와
양 backend의 지역별 추출/절삭과 생성 소비자·Helpdesk HTML/API/client·브라우저 검증 경로를 작성했다.
독립 관측과 현재 source의 실제 실행 대조는 아직 미완료다.

## 다음 행동

고정 전환표를 사용하는 양 backend의 DST·domain·실패/rollback을 독립 관측과 대조하고 생성 소비자·Helpdesk를 원격에서 검증한다.
현재 작업의 입출력·client·브라우저 검증을 완료하며, locale·named annotation·나머지 function/subquery/window를 포함한
[헌장](../CHARTER.md)의 전체 목표는 계속 완성 대상이다.
