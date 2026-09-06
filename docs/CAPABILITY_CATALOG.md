# 장기 기능 범위

GoDj가 향하는 기능 범위다. 아래 목록은 구현 완료·지원·API 안정성의 주장이 아니다.
현재 기능과 backend 제한은 [구현 현황](status/IMPLEMENTATION_MATRIX.md)과 [Backend Matrix](BACKEND_MATRIX.md)가 소유한다.
이 목록을 각 기능의 빈 package나 사전 테스트 전체를 만드는 계획으로 사용하지 않는다.

| 영역 | 장기 기능 |
|---|---|
| Project/Core | settings/environment, deterministic app registry와 lifecycle, system check, management/custom command, logging, signal/event |
| Web | routing/namespace/reverse, request/response, middleware, error handling, static/media/upload, development server/reload |
| Schema/Model | 선언·IR·metadata, fields/options/default/validator, generated type/FieldSet/codec, model method·상속에 대응하는 Go 확장, introspection |
| Fields | Auto/Integer/Float/Decimal/Boolean, Char/Text/Slug/Email/URL/UUID, Date/Time/Duration, Binary/JSON, File/Image, choices/enum, backend-specific array/range/generated/spatial |
| Relation | ForeignKey/OneToOne/ManyToMany, target/depth/cycle, forward/reverse manager, prefetch/select-related, assignment와 delete semantics |
| QuerySet | lazy/cache/iterator, typed/dynamic lookup, Q/F/expression, order/limit/distinct, projection/values, annotation/group/having, aggregate/subquery/window/function |
| ORM writes | create/save/update/delete, get-or-create/update-or-create, bulk, row locking, transaction/savepoint와 failure/retry의 명시적 의미 |
| Migration | writer/autodetector, graph/history/state, forward/backward, schema operations·custom/data operation, restart/repair/adoption, fake/squash/optimizer, multi-DB |
| Database | SQLite/PostgreSQL/MySQL/MariaDB/Oracle, capabilities, compiler/editor, introspection, connection/transaction lifetime, schema constraints/indexes |
| Validation/Form | 공통 validation, Form/ModelForm, cleaned data와 errors, widgets, relation choice, formset, files, cross-field validation |
| Template | escaping/trusted value, inheritance/include, filters/tags, localization, safe rendering과 resource limits |
| Admin | metadata registry, list/search/filter/order/page, relation UI, CRUD/actions/history, permission, safe extension points |
| Identity/security | user/group/permission, credential lifecycle, server-side session, CSRF, login/logout/reset/revocation, security headers, audit |
| API | serializer/model serializer, JSON/viewset/router, filtering/pagination, Session/Bearer/OAuth/OIDC, OpenAPI, browsable API, throttling/versioning |
| Realtime | WebSocket/SSE, consumer, groups/channel layer, presence, backpressure, cancellation, multi-process delivery |
| GIS | spatial fields/lookups/geometry, backend feature negotiation, projection/distance, GeoJSON와 Admin/map integration |
| i18n/지역화 | gettext/catalog, locale/timezone, localized formats, translated validation/template/Admin |
| Contrib | content types, generic relation, sites/redirects, messages, cache, storage, mail, pagination, syndication/sitemaps, serialization/fixtures |
| Django 데이터 이행 | table/column/relation naming, password hash, auth/content types, inspectdb와 schema/data migration 도구 |
| 품질/운영 | differential contracts, invariant/property/fuzz/race, DB/process/security/failure, profiling/benchmark, observability, reproducible packaging |

Django profile의 새 기능은 해당 버전의 공식 source와 실행 결과를 확인해 필요한 contract로 선택한다.
Upstream 기능 이름을 복사했다는 이유로 같은 내부 구현이나 Python source 호환을 요구하지 않는다.
모델 의미의 공통 원본과 실제 consumer 흐름을 유지하면서 [로드맵](ROADMAP.md)의 우선순위로 확장한다.
