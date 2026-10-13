// Run after real login in the isolated read-only summary/status/usage fixture.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    await page.goto(origin + "/tickets/summary/");
    await page.getByRole("link", {name:"Label usage",exact:true}).click();
    check(await page.getByRole("heading", {name:"Label usage",exact:true}).count() === 1, "label usage navigation");
    check(await page.locator("tbody tr").count() === 20 && (await page.getByRole("main").innerText()).includes("25 labels. Page 1."), "twenty label rows and scoped total");
    const first = (await page.locator('tr[data-label-id="1"]').innerText()).replace(/\s+/g," ").trim();
    check(first === "Desk & <one> 3 2 1 0.3 2 00:00:00.000003", "label collection metrics exclude missing values");
    check(await page.locator("script, one").count() === 0 && first.includes("<one>"), "label name is escaped");
    const unused = (await page.locator('tr[data-label-id="2"]').innerText()).replace(/\s+/g," ").trim();
    check(unused === "Spare __prefix__ 0 0 0 Not set Not set Not set", "unused label preserves zero counts and missing metrics");
    check(!(await page.getByRole("main").innerText()).includes("Private outside"), "outside category label remains private");
    await page.getByRole("link", {name:"Next page",exact:true}).click();
    check(await page.locator("tbody tr").count() === 5 && new URL(page.url()).searchParams.get("usage") === "all", "remaining five label rows preserve filter");
    await page.getByLabel("Show", {exact:true}).selectOption("used");
    await page.getByRole("button", {name:"Apply filter",exact:true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("usage") === "used", "usage filter resets page");
    check((await page.getByRole("main").innerText()).includes("24 labels. Page 1.") && await page.locator('tr[data-label-id="2"]').count() === 0, "used filter removes empty relations");
    await page.getByRole("link", {name:"Next page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("usage") === "used" && await page.locator("tbody tr").count() === 4, "used page link preserves usage filter");
    check(await page.getByRole("link", {name:"Next page",exact:true}).count() === 0, "used filter has no false next page");
    await page.getByLabel("Show", {exact:true}).selectOption("unused");
    await page.getByRole("button", {name:"Apply filter",exact:true}).click();
    check((await page.getByRole("main").innerText()).includes("1 labels. Page 1.") && await page.locator("tbody tr").count() === 1 && await page.locator('tr[data-label-id="2"]').count() === 1, "unused filter retains the empty label");
    await page.goto(origin + "/labels/usage/?usage=unused&p=2");
    check((await page.getByRole("main").innerText()).includes("1 labels. Page 2.") && (await page.locator("tbody").innerText()).includes("No labels on this page."), "past-end label page retains filtered total");
    await page.getByRole("link", {name:"Previous page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("usage") === "unused" && await page.locator('tr[data-label-id="2"]').count() === 1, "previous label page preserves filter");
    const response = await page.request.get(origin + "/api/labels/usage/");
    const value = await response.json();
    const row = value.results[0];
    check(response.status() === 200 && value.total_labels === 25 && value.page === 1 && value.page_size === 20 && value.usage === "all" && value.results.length === 20 && row.id === 1 && row.total === 3 && row.open === 2 && row.closed === 1 && row.expected_cost_total === "0.3" && row.effort_average === 2 && row.elapsed_total === "00:00:00.000003", "API label metrics and group total match");
    const emptyResponse = await page.request.get(origin + "/api/labels/usage/?usage=unused");
    const empty = await emptyResponse.json();
    check(emptyResponse.status() === 200 && empty.total_labels === 1 && empty.results.length === 1 && empty.results[0].id === 2 && empty.results[0].total === 0 && empty.results[0].expected_cost_total === null && empty.results[0].effort_average === null && empty.results[0].elapsed_total === null, "API unused label keeps required null metrics");
    for (const path of ["/labels/usage/?p=bad", "/api/labels/usage/?usage=unknown", "/api/labels/usage/?usage=used&usage=unused"]) {
        const failed = await page.request.get(origin + path);
        check(failed.status() === 400, "malformed label usage query rejected: " + path);
    }
    check(response.headers()["cache-control"] === "no-store", "label usage response is not cached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "label usage page contains no write form");
    return {passed:cases.length,cases};
}
