// The shared fixture retains two dated tickets and 28 unset deadlines. Native
// HTTP tests own full multi-page groups; this browser checks actual form/link
// behavior, the empty-page boundary and exact visible date/clock semantics.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    const input = "2024-02-29T09:00:00.000001999+09:00";
    const canonical = "2024-02-29T00:00:00.000001Z";
    const route = (values, api = false) => origin + (api ? "/api" : "") + "/tickets/deadline-calendar/?" + new URLSearchParams(values).toString();
    const rowText = async (date) => (await page.locator(`tr[data-deadline-date="${date}"]`).innerText()).replace(/\s+/g, " ").trim();
    await page.goto(origin + "/tickets/summary/");
    await page.getByRole("link", {name: "Deadline calendar", exact: true}).click();
    check(await page.getByRole("heading", {name: "Deadline calendar", exact: true}).count() === 1, "deadline calendar navigation");
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === "" && await page.locator("table").count() === 0 && (await page.getByRole("main").innerText()).includes("Enter a reference time"), "deadline calendar entry requires an explicit reference");
    await page.getByLabel("Reference time", {exact: true}).fill(input);
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    check(await page.locator("tbody tr").count() === 3 && (await page.getByRole("main").innerText()).includes("3 deadline groups. Page 1."), "deadline calendar first page and scoped group total");
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === canonical, "deadline calendar normalizes the explicit reference to UTC microseconds");
    check(await rowText("2024-02-28") === "2024-02-28 1 1 1 23:59:59.999999 23:59:59.999999", "deadline calendar overdue open count and last microsecond clock");
    check(await rowText("2024-02-29") === "2024-02-29 1 0 0 00:00:00.000001 00:00:00.000001", "deadline calendar closed tickets are never overdue");
    check(await rowText("unset") === "Not set 28 26 0 Not set Not set", "deadline calendar unset group retains NULL clocks and zero overdue");
    check(await page.getByRole("link", {name: "Next page", exact: true}).count() === 0, "deadline calendar has no false next page");
    await page.getByLabel("From date", {exact: true}).fill("2024-02-28");
    await page.getByLabel("Until date", {exact: true}).fill("2024-02-29");
    await page.getByRole("button", {name: "Filter dates", exact: true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("as_of") === canonical && await page.locator("tbody tr").count() === 2 && await page.locator('tr[data-deadline-date="unset"]').count() === 0, "deadline calendar inclusive range resets page and excludes NULLs");
    await page.goto(route({as_of: canonical, from_date: "2024-02-28", until_date: "2024-02-29", p: "2"}));
    check((await page.getByRole("main").innerText()).includes("2 deadline groups. Page 2.") && (await page.locator("tbody").innerText()).includes("No deadline groups on this page."), "deadline calendar past-end page retains the filtered total");
    await page.getByRole("link", {name: "Previous page", exact: true}).click();
    let values = new URL(page.url()).searchParams;
    check(values.get("p") === "1" && values.get("as_of") === canonical && values.get("from_date") === "2024-02-28" && values.get("until_date") === "2024-02-29" && await page.locator("tbody tr").count() === 2, "deadline calendar previous page preserves reference and both bounds");
    await page.getByRole("button", {name: "Show deadline calendar", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("p") === null && values.get("from_date") === "2024-02-28" && values.get("until_date") === "2024-02-29", "deadline calendar reference form retains the date range");
    await page.getByRole("link", {name: "Clear date range", exact: true}).click();
    values = new URL(page.url()).searchParams;
    check(values.get("as_of") === canonical && values.get("from_date") === null && values.get("until_date") === null && await page.locator("tbody tr").count() === 3, "deadline calendar clear range retains the reference");
    const response = await page.request.get(route({as_of: input}, true));
    const result = await response.json();
    check(response.status() === 200 && result.category.name === "Browser category" && result.as_of === canonical && result.page === 1 && result.page_size === 20 && result.total_groups === 3 && result.from_date === null && result.until_date === null && result.results.map(row => row.date).join(",") === "2024-02-28,2024-02-29,", "deadline calendar API envelope and date ordering match the page");
    check(JSON.stringify(result.results) === JSON.stringify([
        {date: "2024-02-28", total: 1, open: 1, overdue: 1, first_due_time: "23:59:59.999999", last_due_time: "23:59:59.999999"},
        {date: "2024-02-29", total: 1, open: 0, overdue: 0, first_due_time: "00:00:00.000001", last_due_time: "00:00:00.000001"},
        {date: null, total: 28, open: 26, overdue: 0, first_due_time: null, last_due_time: null},
    ]), "deadline calendar API exact counts and required nullable clocks");
    const equalResponse = await page.request.get(route({as_of: "2024-02-28T23:59:59.999999Z"}, true));
    const equal = await equalResponse.json();
    check(equalResponse.status() === 200 && equal.results[0].open === 1 && equal.results[0].overdue === 0, "deadline calendar equal reference is not overdue");
    const onlyResponse = await page.request.get(route({as_of: input, from_date: "2024-02-29", until_date: "2024-02-29"}, true));
    const only = await onlyResponse.json();
    check(onlyResponse.status() === 200 && only.total_groups === 1 && only.results[0].date === "2024-02-29" && only.from_date === "2024-02-29" && only.until_date === "2024-02-29", "deadline calendar inclusive equal date bounds retain one day");
    const lastResponse = await page.request.get(route({as_of: input, until_date: "2024-02-28", p: "50001"}, true));
    const last = await lastResponse.json();
    check(lastResponse.status() === 200 && last.total_groups === 1 && last.results.length === 0 && last.page === 50001 && last.from_date === null && last.until_date === "2024-02-28", "deadline calendar last allowed page preserves a single date bound");
    for (const [path, name] of [
        [origin + "/api/tickets/deadline-calendar/", "deadline calendar rejects missing API reference"],
        [route({p: "1"}), "deadline calendar rejects missing HTML reference"],
        [route({as_of: input, from_date: ""}, true), "deadline calendar rejects an empty date bound"],
        [route({as_of: input, from_date: "2023-02-29"}, true), "deadline calendar rejects an invalid leap date"],
        [route({as_of: input, from_date: "2024-2-29"}, true), "deadline calendar rejects a noncanonical date"],
        [route({as_of: input, from_date: "2024-03-01", until_date: "2024-02-29"}), "deadline calendar rejects reversed HTML date range"],
        [route({as_of: input, from_date: "2024-03-01", until_date: "2024-02-29"}, true), "deadline calendar rejects reversed API date range"],
        [route({as_of: input}, true) + "&as_of=" + encodeURIComponent(input), "deadline calendar rejects duplicate reference"],
        [route({as_of: input, category: "2"}, true), "deadline calendar rejects a supplied category"],
    ]) {
        const rejected = await page.request.get(path);
        check(rejected.status() === 400 && !(await rejected.text()).includes('"results"'), name);
    }
    check(response.headers()["cache-control"] === "no-store" && response.headers()["referrer-policy"] === "same-origin", "deadline calendar response is private and uncached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "deadline calendar contains no write form");
    await page.getByRole("link", {name: "Choose another reference time", exact: true}).click();
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === "" && await page.locator("table").count() === 0, "deadline calendar reset does not choose the current time");
    await page.goto(route({as_of: input}));
    check(await rowText("2024-02-28") === "2024-02-28 1 1 1 23:59:59.999999 23:59:59.999999" && await page.locator('tr[data-deadline-date="unset"]').count() === 1, "deadline calendar exact values visible for visual review");
    return {passed: cases.length, cases};
}
