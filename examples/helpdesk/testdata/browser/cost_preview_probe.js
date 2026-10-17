// Run in the existing isolated read-only fixture after real session login.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    await page.goto(origin + "/tickets/summary/");
    await page.getByRole("link", {name:"Preview expected cost adjustments",exact:true}).click();
    check(await page.getByRole("heading", {name:"Expected cost preview",exact:true}).count() === 1, "cost preview navigation");
    check(await page.locator("tbody tr").count() === 20 && (await page.getByRole("main").innerText()).includes("30 tickets. Page 1."), "cost preview first page and total");
    const first = (await page.locator("tbody tr").first().innerText()).replace(/\s+/g," ").trim();
    check(first === "Unset summary 0.2 0.2", "cost preview orders exact non-null costs first");
    check((await page.locator('tr[data-ticket-id="1"]').innerText()).includes("Not set"), "cost preview distinguishes missing costs");
    await page.getByRole("link", {name:"Next page",exact:true}).click();
    check(await page.locator("tbody tr").count() === 10 && new URL(page.url()).searchParams.get("factor") === "1", "cost preview second page preserves operands");
    check(await page.getByRole("link", {name:"Next page",exact:true}).count() === 0, "cost preview has no false next page");
    await page.getByLabel("Multiplier", {exact:true}).fill("1.25");
    await page.getByLabel("Adjustment", {exact:true}).fill("0.1");
    await page.getByRole("button", {name:"Update preview",exact:true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("factor") === "1.25" && new URL(page.url()).searchParams.get("delta") === "0.1", "cost preview operands reset page");
    check((await page.locator('tr[data-ticket-id="2"]').innerText()).replace(/\s+/g," ").trim() === "Unset summary 0.1 0.225", "cost preview retains more than two fractional digits");
    await page.getByLabel("Minimum adjusted cost", {exact:true}).fill("0.3");
    await page.getByRole("button", {name:"Filter current preview",exact:true}).click();
    check((await page.getByRole("main").innerText()).includes("1 tickets. Page 1.") && await page.locator('tr[data-ticket-id="3"]').count() === 1 && await page.locator("tbody tr").count() === 1, "cost preview minimum filters computed values");
    check(new URL(page.url()).searchParams.get("factor") === "1.25" && new URL(page.url()).searchParams.get("delta") === "0.1", "cost preview minimum preserves operands");
    await page.goto(origin + "/tickets/cost-preview/?factor=1.25&delta=0.1&min_cost=0.3&p=2");
    check((await page.getByRole("main").innerText()).includes("1 tickets. Page 2.") && (await page.locator("tbody").innerText()).includes("No tickets on this page."), "cost preview past-end page retains filtered total");
    await page.getByRole("link", {name:"Previous page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("min_cost") === "0.3" && await page.locator('tr[data-ticket-id="3"]').count() === 1, "cost preview previous link preserves minimum");
    await page.getByRole("link", {name:"Clear minimum",exact:true}).click();
    check(new URL(page.url()).searchParams.get("min_cost") === null && new URL(page.url()).searchParams.get("factor") === "1.25" && (await page.getByRole("main").innerText()).includes("30 tickets. Page 1."), "cost preview clearing minimum preserves adjustment");
    const response = await page.request.get(origin + "/api/tickets/cost-preview/?factor=1.25&delta=0.1");
    const value = await response.json();
    check(response.status() === 200 && value.total_tickets === 30 && value.factor === "1.25" && value.delta === "0.1" && value.min_cost === null && value.results[0].id === 3 && value.results[0].expected_cost === "0.2" && value.results[0].adjusted_cost === "0.35" && value.results[1].adjusted_cost === "0.225", "cost preview API exact strings and ordering");
    check(value.results[2].id === 1 && value.results[2].expected_cost === null && value.results[2].adjusted_cost === null, "cost preview API required null values");
    const zeroResponse = await page.request.get(origin + "/api/tickets/cost-preview/?factor=0&min_cost=0");
    const zero = await zeroResponse.json();
    check(zeroResponse.status() === 200 && zero.total_tickets === 2 && zero.results[0].id === 2 && zero.results[1].id === 3 && zero.results.every(row => row.adjusted_cost === "0"), "cost preview zero factor retains null exclusion and stable ties");
    for (const path of ["/tickets/cost-preview/?factor=1.0", "/api/tickets/cost-preview/?factor=100", "/api/tickets/cost-preview/?delta=0.001", "/api/tickets/cost-preview/?min_cost="]) {
        const failed = await page.request.get(origin + path);
        check(failed.status() === 400, "malformed cost query rejected: " + path);
    }
    check(response.headers()["cache-control"] === "no-store", "cost preview response is not cached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "cost preview contains no write form");
    return {passed:cases.length,cases};
}
