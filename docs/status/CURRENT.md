# 현재 상태

- 갱신: 2026-10-19
- 현재: [GDJ-0133 명시적 시간대·지역별 기한 달력](../../work/0133-named-timezones-and-local-deadline-calendar.md)의 전체 통합 검증 완료; 다음 기반 선택
- 최근 전체 검증 제품·생성기·소비자 source: `55928651beaa9e81f0d58805ee3c9e18a22c3080`
- 최근 전체 통합: [CI 39166711424](https://github.com/progresshans/godj/actions/runs/39166711424), attempt 1의 필수 실행·원본 증거 대조 완료
- Source·환경·실행 상세: [TEST_EVIDENCE](TEST_EVIDENCE.md)

## 현재

GDJ-0131의 정확한 Duration 배율·나눗셈·비율과 라벨별 조정 시간,
GDJ-0132의 Date/Time 산술과 서비스 일정 미리보기를 공통 AST·양 backend·생성 소비자·HTML/API/client에 연결했다.
같은 source의 양 DB·독립 Django 비교·생성 소비자·HTML/API/client의 세 mode 실행과
실제 Chromium 브라우저·전체 플랫폼의 필수 실행 및 원본을 대조했다. [Draft PR #2](https://github.com/progresshans/godj/pull/2)의
이 구현 범위는 완료했으며 프레임워크 전체 목표와 구분한다. 현재 외부 입력이 필요한 blocker는 없다.

GDJ-0133은 고정 IANA 데이터·불변 시간대 값·공통 AST를 통해 양 backend의 지역별 추출/절삭과
Helpdesk 기한 달력의 HTML/API/client를 연결했다. S3 조건부 PUT의 실제 본문 대기 오류도 수정했다.
같은 source의 공식 IANA 독립 빌드·Django/native 비교·생성 File/달력 소비자·Helpdesk 세 mode와
실제 브라우저·capture 후속 소비·전체 플랫폼의 필수 실행 및 원본 대조를 완료했다.
[Draft PR #3](https://github.com/progresshans/godj/pull/3)의 이 구현 범위는 완료했으며, 프레임워크 전체 목표는 계속 진행 중이다.

## 다음 행동

현재 QuerySet·결과 scope와 실제 소비자의 요구를 기준으로 named annotation의 의존성과 다음 수직 단면을 정한다.
Locale·나머지 function/subquery/window·추가 backend·Realtime·GIS를 포함한
[헌장](../CHARTER.md)과 [기능 카탈로그](../CAPABILITY_CATALOG.md)의 전체 목표는 계속 완성 대상이다.
