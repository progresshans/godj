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
    check((await page.locator("tbody tr").first().innerText()).replace(/\s+/g," ").trim() === "Not set 3 2", "null priority remains a counted group");
    check((await page.locator("tbody tr").nth(1).innerText()).includes("9223372036854775807"), "legacy maximum int64 is displayed exactly");
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
    for (const path of ["/tickets/summary/?p=bad","/api/tickets/summary/?p=1&p=2"]) {
        const response=await page.request.get(origin+path);
        check(response.status()===400,"malformed query rejected: "+path);
    }
    const response=await page.request.get(origin+"/api/tickets/summary/?p=2");
    const wire=await response.text();
    check(response.status()===200 && wire.includes('"priority":-9223372036854775808') && wire.includes('"total_groups":27'),"API keeps exact integer wire and group total");
    check(response.headers()["cache-control"]==="no-store","summary response is not cached");
    return {passed:cases.length,cases};
}
