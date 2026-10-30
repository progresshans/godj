// The shared fixture retains two dated tickets and 28 unset deadlines. Native
// HTTP tests own full multi-page groups; this browser checks actual form/link
// behavior, the empty-page boundary and exact visible period/instant semantics.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    const input = "2024-02-29T09:00:00.000001999+09:00";
    const canonical = "2024-02-29T00:00:00.000001Z";
    const route = (values, api = false) => origin + (api ? "/api" : "") + "/tickets/deadline-calendar/?" + new URLSearchParams(values).toString();
    const rowText = async (date) => (await page.locator(`tr[data-deadline-period="${date}"]`).innerText()).replace(/\s+/g, " ").trim();
    await page.goto(origin + "/tickets/summary/");
    await page.getByRole("link", {name: "Deadline calendar", exact: true}).click();
    check(await page.getByRole("heading", {name: "Deadline calendar", exact: true}).count() === 1, "deadline calendar navigation");
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === "" && await page.locator("table").count() === 0 && (await page.getByRole("main").innerText()).includes("Enter a reference time"), "deadline calendar entry requires an explicit reference");
    await page.getByLabel("Reference time", {exact: true}).fill(input);
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    check(await page.locator("tbody tr").count() === 3 && (await page.getByRole("main").innerText()).includes("3 deadline groups. Page 1."), "deadline calendar first page and scoped group total");
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === canonical, "deadline calendar normalizes the explicit reference to UTC microseconds");
    check(await rowText("2024-02-28") === "2024-02-28 1 1 1 2024-02-28T23:59:59.999999Z 2024-02-28T23:59:59.999999Z", "deadline calendar overdue open count and last microsecond instant");
    check(await rowText("2024-02-29") === "2024-02-29 1 0 0 2024-02-29T00:00:00.000001Z 2024-02-29T00:00:00.000001Z", "deadline calendar closed tickets are never overdue");
    check(await rowText("unset") === "Not set 28 26 0 Not set Not set", "deadline calendar unset group retains NULL instants and zero overdue");
    check(await page.getByRole("link", {name: "Next page", exact: true}).count() === 0, "deadline calendar has no false next page");
    await page.getByLabel("From period start", {exact: true}).fill("2024-02-28");
    await page.getByLabel("Until period start", {exact: true}).fill("2024-02-29");
    await page.getByRole("button", {name: "Filter periods", exact: true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("as_of") === canonical && await page.locator("tbody tr").count() === 2 && await page.locator('tr[data-deadline-period="unset"]').count() === 0, "deadline calendar inclusive range resets page and excludes NULLs");
    await page.goto(route({as_of: canonical, from_date: "2024-02-28", until_date: "2024-02-29", p: "2"}));
    check((await page.getByRole("main").innerText()).includes("2 deadline groups. Page 2.") && (await page.locator("tbody").innerText()).includes("No deadline groups on this page."), "deadline calendar past-end page retains the filtered total");
    await page.getByRole("link", {name: "Previous page", exact: true}).click();
    let values = new URL(page.url()).searchParams;
    check(values.get("p") === "1" && values.get("period") === "day" && values.get("as_of") === canonical && values.get("from_date") === "2024-02-28" && values.get("until_date") === "2024-02-29" && await page.locator("tbody tr").count() === 2, "deadline calendar previous page preserves reference and both bounds");
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("p") === null && values.get("period") === "day" && values.get("from_date") === "2024-02-28" && values.get("until_date") === "2024-02-29", "deadline calendar reference form retains the date range");
    await page.getByRole("link", {name: "Clear date range", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("as_of") === canonical && values.get("period") === "day" && values.get("from_date") === null && values.get("until_date") === null && await page.locator("tbody tr").count() === 3, "deadline calendar clear range retains the reference");
    const response = await page.request.get(route({as_of: input}, true));
    const result = await response.json();
    check(response.status() === 200 && result.category.name === "Browser category" && result.as_of === canonical && result.period === "day" && result.time_zone === "UTC" && result.page === 1 && result.page_size === 20 && result.total_groups === 3 && result.from_date === null && result.until_date === null && result.results.map(row => row.period_start).join(",") === "2024-02-28,2024-02-29,", "deadline calendar API envelope and date ordering match the page");
    check(JSON.stringify(result.results) === JSON.stringify([
        {period_start: "2024-02-28", total: 1, open: 1, overdue: 1, first_due_at: "2024-02-28T23:59:59.999999Z", last_due_at: "2024-02-28T23:59:59.999999Z"},
        {period_start: "2024-02-29", total: 1, open: 0, overdue: 0, first_due_at: "2024-02-29T00:00:00.000001Z", last_due_at: "2024-02-29T00:00:00.000001Z"},
        {period_start: null, total: 28, open: 26, overdue: 0, first_due_at: null, last_due_at: null},
    ]), "deadline calendar API exact counts and required nullable instants");
    const equalResponse = await page.request.get(route({as_of: "2024-02-28T23:59:59.999999Z"}, true));
    const equal = await equalResponse.json();
    check(equalResponse.status() === 200 && equal.results[0].open === 1 && equal.results[0].overdue === 0, "deadline calendar equal reference is not overdue");
    const onlyResponse = await page.request.get(route({as_of: input, from_date: "2024-02-29", until_date: "2024-02-29"}, true));
    const only = await onlyResponse.json();
    check(onlyResponse.status() === 200 && only.total_groups === 1 && only.results[0].period_start === "2024-02-29" && only.from_date === "2024-02-29" && only.until_date === "2024-02-29", "deadline calendar inclusive equal date bounds retain one day");
    const lastResponse = await page.request.get(route({as_of: input, until_date: "2024-02-28", p: "50001"}, true));
    const last = await lastResponse.json();
    check(lastResponse.status() === 200 && last.total_groups === 1 && last.results.length === 0 && last.page === 50001 && last.from_date === null && last.until_date === "2024-02-28", "deadline calendar last allowed page preserves a single date bound");
    await page.goto(route({as_of: input, from_date: "2024-02-28", until_date: "2024-02-29", p: "2"}));
    await page.getByLabel("Group by", {exact: true}).selectOption("month");
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("period") === "month" && values.get("p") === null && values.get("from_date") === "2024-02-28" && values.get("until_date") === "2024-02-29" && (await page.getByRole("main").innerText()).includes("0 deadline groups. Page 1."), "deadline calendar period selection retains bounds and filters period starts");
    await page.getByLabel("From period start", {exact: true}).fill("2024-02-01");
    await page.getByLabel("Until period start", {exact: true}).fill("2024-02-01");
    await page.getByRole("button", {name: "Filter periods", exact: true}).click();
    values = new URL(page.url()).searchParams;
    const monthRow = "2024-02-01 2 1 1 2024-02-28T23:59:59.999999Z 2024-02-29T00:00:00.000001Z";
    check(values.get("period") === "month" && values.get("as_of") === canonical && values.get("p") === null && await page.locator("tbody tr").count() === 1 && await rowText("2024-02-01") === monthRow, "deadline calendar range form retains period and complete cross-day deadlines");
    await page.goto(route({as_of: canonical, period: "month", from_date: "2024-02-01", until_date: "2024-02-01", p: "2"}));
    check((await page.getByRole("main").innerText()).includes("1 deadline groups. Page 2.") && await page.getByRole("link", {name: "Next page", exact: true}).count() === 0, "deadline calendar monthly empty page retains filtered total");
    await page.getByRole("link", {name: "Previous page", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("p") === "1" && values.get("period") === "month" && values.get("as_of") === canonical && values.get("from_date") === "2024-02-01" && values.get("until_date") === "2024-02-01" && await rowText("2024-02-01") === monthRow, "deadline calendar monthly previous link preserves all query context");
    await page.getByRole("link", {name: "Clear date range", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("period") === "month" && values.get("as_of") === canonical && values.get("from_date") === null && values.get("until_date") === null && await page.getByLabel("Group by", {exact: true}).inputValue() === "month" && await page.locator("tbody tr").count() === 2, "deadline calendar clear range retains the selected period");
    for (const [period, start] of [["week", "2024-02-26"], ["month", "2024-02-01"], ["quarter", "2024-01-01"], ["year", "2024-01-01"]]) {
        await page.getByLabel("Group by", {exact: true}).selectOption(period);
        await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
        const periodResponse = await page.request.get(route({as_of: input, period}, true));
        const periodResult = await periodResponse.json();
        const expected = [
            {period_start: start, total: 2, open: 1, overdue: 1, first_due_at: "2024-02-28T23:59:59.999999Z", last_due_at: "2024-02-29T00:00:00.000001Z"},
            {period_start: null, total: 28, open: 26, overdue: 0, first_due_at: null, last_due_at: null},
        ];
        check(new URL(page.url()).searchParams.get("period") === period && await page.getByLabel("Group by", {exact: true}).inputValue() === period && await page.locator("tbody tr").count() === 2 && await rowText(start) === start + " 2 1 1 2024-02-28T23:59:59.999999Z 2024-02-29T00:00:00.000001Z" && await rowText("unset") === "Not set 28 26 0 Not set Not set" && periodResponse.status() === 200 && periodResult.period === period && periodResult.total_groups === 2 && periodResult.as_of === canonical && JSON.stringify(periodResult.results) === JSON.stringify(expected), "deadline calendar " + period + " HTML and API exact periods and complete deadlines");
    }
    check(await page.getByLabel("Calendar time zone", {exact: true}).inputValue() === "UTC", "deadline calendar defaults to explicit UTC");
    await page.getByLabel("Group by", {exact: true}).selectOption("day");
    await page.getByLabel("Calendar time zone", {exact: true}).fill("Asia/Seoul");
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    const localRow = "2024-02-29 2 1 1 2024-02-28T23:59:59.999999Z 2024-02-29T00:00:00.000001Z";
    values = new URL(page.url()).searchParams;
    check(values.get("time_zone") === "Asia/Seoul" && values.get("period") === "day" && values.get("p") === null && await rowText("2024-02-29") === localRow && await page.locator("tbody tr").count() === 2 && (await page.locator("caption").innerText()).startsWith("Asia/Seoul"), "deadline calendar local zone combines dates while preserving UTC instants");
    await page.getByLabel("From period start", {exact: true}).fill("2024-02-29");
    await page.getByLabel("Until period start", {exact: true}).fill("2024-02-29");
    await page.getByRole("button", {name: "Filter periods", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("time_zone") === "Asia/Seoul" && values.get("p") === null && await rowText("2024-02-29") === localRow && await page.locator("tbody tr").count() === 1, "deadline calendar range form retains zone and local date membership");
    await page.goto(route({as_of: canonical, time_zone: "Asia/Seoul", period: "day", from_date: "2024-02-29", until_date: "2024-02-29", p: "2"}));
    await page.getByRole("link", {name: "Previous page", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("time_zone") === "Asia/Seoul" && values.get("p") === "1" && values.get("as_of") === canonical && values.get("period") === "day" && values.get("from_date") === "2024-02-29" && values.get("until_date") === "2024-02-29" && await rowText("2024-02-29") === localRow, "deadline calendar page links preserve the entire zoned query");
    await page.getByLabel("Group by", {exact: true}).selectOption("month");
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("time_zone") === "Asia/Seoul" && values.get("period") === "month" && values.get("from_date") === "2024-02-29" && (await page.getByRole("main").innerText()).includes("0 deadline groups. Page 1."), "deadline calendar period form preserves zone and start bounds");
    await page.getByRole("link", {name: "Clear date range", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("time_zone") === "Asia/Seoul" && values.get("period") === "month" && values.get("as_of") === canonical && values.get("from_date") === null && values.get("until_date") === null && await rowText("2024-02-01") === monthRow, "deadline calendar clear range preserves zone period and reference");
    await page.getByLabel("Group by", {exact: true}).selectOption("day");
    await page.getByLabel("Calendar time zone", {exact: true}).fill("America/New_York");
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    check(await rowText("2024-02-28") === localRow.replace("2024-02-29 2", "2024-02-28 2") && await page.locator("tbody tr").count() === 2, "deadline calendar switching zones moves the business day");
    const aliasResponse = await page.request.get(route({as_of: input, time_zone: "US/Eastern"}, true));
    const aliasResult = await aliasResponse.json();
    check(aliasResponse.status() === 200 && aliasResult.time_zone === "US/Eastern" && aliasResult.as_of === canonical && aliasResult.total_groups === 2 && aliasResult.results[0].period_start === "2024-02-28" && aliasResult.results[0].total === 2 && aliasResult.results[0].first_due_at === "2024-02-28T23:59:59.999999Z" && aliasResult.results[0].last_due_at === "2024-02-29T00:00:00.000001Z" && aliasResult.results[1].period_start === null, "deadline calendar API preserves aliases and exact local groups");
    for (const [path, name] of [
        [origin + "/api/tickets/deadline-calendar/", "deadline calendar rejects missing API reference"],
        [route({p: "1"}), "deadline calendar rejects missing HTML reference"],
        [route({as_of: input, from_date: ""}, true), "deadline calendar rejects an empty date bound"],
        [route({as_of: input, from_date: "2023-02-29"}, true), "deadline calendar rejects an invalid leap date"],
        [route({as_of: input, from_date: "2024-2-29"}, true), "deadline calendar rejects a noncanonical date"],
        [route({as_of: input, from_date: "2024-03-01", until_date: "2024-02-29"}), "deadline calendar rejects reversed HTML date range"],
        [route({as_of: input, from_date: "2024-03-01", until_date: "2024-02-29"}, true), "deadline calendar rejects reversed API date range"],
        [route({as_of: input}, true) + "&as_of=" + encodeURIComponent(input), "deadline calendar rejects duplicate reference"],
        [route({as_of: input, period: "hour"}, true), "deadline calendar rejects an unsupported API period"],
        [route({as_of: input, period: "Month"}), "deadline calendar rejects a noncanonical HTML period"],
        [route({as_of: input, period: "day"}, true) + "&period=month", "deadline calendar rejects duplicate periods"],
        [route({as_of: input, time_zone: ""}, true), "deadline calendar rejects an empty zone"],
        [route({as_of: input, time_zone: "Local"}), "deadline calendar rejects an ambient zone"],
        [route({as_of: input, time_zone: "Asia/seoul"}, true), "deadline calendar rejects an unknown zone spelling"],
        [route({as_of: input, time_zone: "UTC"}, true) + "&time_zone=UTC", "deadline calendar rejects duplicate zones"],
        [route({as_of: input, category: "2"}, true), "deadline calendar rejects a supplied category"],
    ]) {
        const rejected = await page.request.get(path);
        check(rejected.status() === 400 && !(await rejected.text()).includes('"results"'), name);
    }
    check(response.headers()["cache-control"] === "no-store" && response.headers()["referrer-policy"] === "same-origin", "deadline calendar response is private and uncached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "deadline calendar contains no write form");
    await page.getByRole("link", {name: "Choose another reference time", exact: true}).click();
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === "" && await page.getByLabel("Group by", {exact: true}).inputValue() === "day" && await page.getByLabel("Calendar time zone", {exact: true}).inputValue() === "UTC" && await page.locator("table").count() === 0, "deadline calendar reset does not choose the current time");
    await page.goto(route({as_of: input, period: "month"}));
    check(await rowText("2024-02-01") === monthRow && await page.locator('tr[data-deadline-period="unset"]').count() === 1, "deadline calendar exact values visible for visual review");
    return {passed: cases.length, cases};
}
