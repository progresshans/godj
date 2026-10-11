// Run in the isolated GODJ_BROWSER_SUMMARY=1 fixture after its real login.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    await page.goto(origin + "/tickets/edit/");
    await page.getByRole("link", {name:"Ticket summary", exact:true}).click();
    check(await page.getByRole("heading", {name:"Ticket summary",exact:true}).count() === 1, "summary navigation");
    check(await page.locator("tbody tr").count() === 20, "twenty groups on first page");
    check((await page.locator("tbody tr").first().innerText()).replace(/\s+/g," ").trim() === "Not set Normal 3 2 1 66.66% 0.3 2 00:00:00.000003", "null priority retains exact metrics and excludes missing values");
    for (const name of ["After raise", "Closed tickets", "Open rate", "Total expected cost", "Average effort", "Total elapsed"]) {
        check(await page.getByRole("columnheader", {name, exact:true}).count() === 1, "numeric summary column: " + name);
    }
    check((await page.locator("tbody tr").nth(1).innerText()).includes("9223372036854775807"), "legacy maximum int64 is displayed exactly");
    check((await page.locator("tbody tr").nth(1).locator("td").first().innerText()) === "Other (9223372036854775807) (unchanged)", "legacy maximum is preserved by CASE preview");
    await page.getByRole("link", {name:"Next page",exact:true}).click();
    check(await page.locator("tbody tr").count() === 7, "remaining seven groups");
    check((await page.locator("tbody tr").nth(3).innerText()).includes("Urgent"), "current choice label");
    check((await page.locator("tbody tr").nth(5).innerText()).includes("-9223372036854775808"), "legacy minimum int64 is displayed exactly");
    check(await page.getByRole("link", {name:"Next page",exact:true}).count() === 0, "no false next page");
    await page.getByLabel("Minimum open tickets",{exact:true}).fill("2");
    await page.getByRole("button",{name:"Apply filter",exact:true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("min_open") === "2", "filter resets page");
    check(await page.locator("tbody tr").count() === 1 && (await page.locator("tbody tr").innerText()).includes("Not set"), "HAVING filters groups");
    await page.goto(origin + "/tickets/summary/?p=2&min_open=2");
    check((await page.getByRole("main").innerText()).includes("1 priority groups. Page 2."), "past-end page preserves filtered total");
    check((await page.locator("tbody").innerText()).includes("No priority groups on this page."), "past-end empty state");
    await page.getByRole("link", {name:"Previous page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("min_open") === "2" && await page.locator("tbody tr").count() === 1, "page link preserves filter");
    await page.getByRole("link", {name:"Clear filter",exact:true}).click();
    check(new URL(page.url()).search === "" && await page.locator("tbody tr").count() === 20, "clear returns first unfiltered page");
    await page.getByLabel("Minimum open rate (basis points)",{exact:true}).fill("6667");
    await page.getByRole("button",{name:"Apply filter",exact:true}).click();
    check(new URL(page.url()).searchParams.get("min_open_basis_points")==="6667" && (await page.getByRole("main").innerText()).includes("24 priority groups. Page 1.") && !(await page.locator("tbody tr").first().innerText()).includes("Not set"), "derived open-rate filter uses basis points");
    await page.getByRole("link",{name:"Next page",exact:true}).click();
    check(new URL(page.url()).searchParams.get("min_open_basis_points")==="6667" && await page.locator("tbody tr").count()===4, "page link preserves derived rate filter");
    const ratioResponse=await page.request.get(origin+"/api/tickets/summary/?min_open_basis_points=6666");
    const ratioPage=await ratioResponse.json();
    check(ratioResponse.status()===200 && ratioPage.total_groups===25 && ratioPage.min_open_basis_points===6666 && ratioPage.results[0].priority===null && ratioPage.results[0].closed===1 && ratioPage.results[0].open_basis_points===6666, "API derived ratio retains integer truncation boundary");
    for (const path of ["/tickets/summary/?p=bad","/api/tickets/summary/?p=1&p=2","/tickets/summary/?min_open_basis_points=10001","/api/tickets/summary/?min_open_basis_points=10001"]) {
        const response=await page.request.get(origin+path);
        check(response.status()===400,"malformed query rejected: "+path);
    }
    const response=await page.request.get(origin+"/api/tickets/summary/?p=2");
    const wire=await response.text();
    check(response.status()===200 && wire.includes('"priority":-9223372036854775808') && wire.includes('"total_groups":27'),"API keeps exact integer wire and group total");
    check(response.headers()["cache-control"]==="no-store","summary response is not cached");
    return {passed:cases.length,cases};
}
