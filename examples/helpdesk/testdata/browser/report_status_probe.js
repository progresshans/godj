// Run after real login in the isolated read-only summary/status fixture.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    await page.goto(origin + "/service-reports/status/");
    check(await page.getByRole("heading", {name:"Service report statuses",exact:true}).count() === 1, "report status navigation");
    check(await page.locator("tbody tr").count() === 20, "twenty report rows on first page");
    const first = (await page.locator("tbody tr").first().innerText()).replace(/\s+/g," ").trim();
    check(first === "1 1 Report <script>& #1 Complete Open Different", "independent completion and closure flags");
    check(await page.locator("script").count() === 0 && first.includes("<script>&"), "report summary is escaped");
    await page.getByRole("link", {name:"Next page",exact:true}).click();
    check(await page.locator("tbody tr").count() === 10, "remaining ten report rows");
    await page.getByLabel("Show", {exact:true}).selectOption("different");
    await page.getByRole("button", {name:"Apply filter",exact:true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("comparison") === "different", "report filter resets page");
    check((await page.getByRole("main").innerText()).includes("25 reports. Page 1.") && (await page.locator("tbody tr td:last-child").allTextContents()).every(value => value === "Different"), "database comparison filters report rows");
    await page.getByRole("link", {name:"Next page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("comparison") === "different" && await page.locator("tbody tr").count() === 5, "report page link preserves comparison");
    check(await page.getByRole("link", {name:"Next page",exact:true}).count() === 0, "report filter has no false next page");
    await page.getByLabel("Show", {exact:true}).selectOption("same");
    await page.getByRole("button", {name:"Apply filter",exact:true}).click();
    check((await page.getByRole("main").innerText()).includes("5 reports. Page 1.") && await page.locator("tbody tr").count() === 5 && (await page.locator("tbody tr td:last-child").allTextContents()).every(value => value === "Same"), "same-status filter retains five reports");
    await page.goto(origin + "/service-reports/status/?comparison=same&p=2");
    check((await page.getByRole("main").innerText()).includes("5 reports. Page 2.") && (await page.locator("tbody").innerText()).includes("No reports on this page."), "past-end report page retains filtered total");
    await page.getByRole("link", {name:"Previous page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("comparison") === "same" && await page.locator("tbody tr").count() === 5, "previous report page preserves filter");
    const response = await page.request.get(origin + "/api/service-reports/status/");
    const value = await response.json();
    check(response.status() === 200 && value.total_reports === 30 && value.page === 1 && value.page_size === 20 && value.comparison === "all" && value.results.length === 20 && value.results.every(row => typeof row.completed === "boolean" && typeof row.ticket_closed === "boolean" && typeof row.matched === "boolean" && row.matched === (row.completed === row.ticket_closed)) && value.results[0].completed && !value.results[0].ticket_closed && !value.results[0].matched, "API report flags and total match");
    const sameResponse = await page.request.get(origin + "/api/service-reports/status/?comparison=same");
    const same = await sameResponse.json();
    check(sameResponse.status() === 200 && same.total_reports === 5 && same.results.length === 5 && same.results.every(row => row.matched), "API same-status filter matches");
    for (const path of ["/service-reports/status/?p=bad", "/api/service-reports/status/?comparison=unknown", "/api/service-reports/status/?comparison=same&comparison=different"]) {
        const failed = await page.request.get(origin + path);
        check(failed.status() === 400, "malformed report query rejected: " + path);
    }
    check(response.headers()["cache-control"] === "no-store", "report status response is not cached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "report status page contains no write form");
    return {passed:cases.length,cases};
}
