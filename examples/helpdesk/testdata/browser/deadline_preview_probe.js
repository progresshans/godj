// Use the existing isolated fixture and its genuinely read-only session.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    const input = "2024-02-29T09:00:00.000001999+09:00";
    const canonical = "2024-02-29T00:00:00.000001Z";
    const shift = "00:00:00.000001";
    const query = (values) => new URLSearchParams(values).toString();
    const route = (values, api = false) => origin + (api ? "/api" : "") + "/tickets/deadline-preview/?" + query(values);
    const rowText = async (id) => (await page.locator(`tr[data-ticket-id="${id}"]`).innerText()).replace(/\s+/g, " ").trim();
    await page.goto(origin + "/tickets/summary/");
    await page.getByRole("link", {name: "Preview deadline shifts", exact: true}).click();
    check(await page.getByRole("heading", {name: "Deadline preview", exact: true}).count() === 1, "deadline preview navigation");
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === "" && await page.getByLabel("Shift", {exact: true}).inputValue() === "00:00:00" && await page.locator("table").count() === 0 && (await page.getByRole("main").innerText()).includes("Enter a reference time to load the preview."), "deadline preview entry requires an explicit reference");
    await page.getByLabel("Reference time", {exact: true}).fill(input);
    await page.getByLabel("Shift", {exact: true}).fill(shift);
    await page.getByRole("button", {name: "Preview deadlines", exact: true}).click();
    check(await page.locator("tbody tr").count() === 20 && (await page.getByRole("main").innerText()).includes("30 tickets. Page 1."), "deadline preview first page and scoped total");
    check(new URL(page.url()).searchParams.get("as_of") === input && await page.getByLabel("Reference time", {exact: true}).inputValue() === canonical, "deadline preview offset normalizes to explicit UTC microseconds");
    check(await rowText(2) === "Unset summary 2024-02-28T23:59:59.999999Z 2024-02-29T00:00:00.000000Z -1 23:59:59.999999", "deadline preview one microsecond crosses the leap-day boundary");
    check(await rowText(3) === "Unset summary 2024-02-29T00:00:00.000001Z 2024-02-29T00:00:00.000002Z 00:00:00.000001", "deadline preview positive remaining microsecond is exact");
    check(await rowText(1) === "Existing browser ticket Not set Not set Not set", "deadline preview distinguishes all nullable outputs");
    await page.getByRole("link", {name: "Next page", exact: true}).click();
    check(await page.locator("tbody tr").count() === 10 && new URL(page.url()).searchParams.get("as_of") === canonical && new URL(page.url()).searchParams.get("shift") === shift, "deadline preview second page retains reference and shift");
    check(await page.getByRole("link", {name: "Next page", exact: true}).count() === 0, "deadline preview has no false next page");
    await page.getByRole("button", {name: "Preview deadlines", exact: true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("as_of") === canonical && new URL(page.url()).searchParams.get("shift") === shift && await page.locator("tbody tr").count() === 20, "deadline preview submitted operands reset the page");
    await page.getByLabel("Minimum remaining time", {exact: true}).fill("00:00:00");
    await page.getByRole("button", {name: "Filter current preview", exact: true}).click();
    check((await page.getByRole("main").innerText()).includes("1 tickets. Page 1.") && await page.locator("tbody tr").count() === 1 && await page.locator('tr[data-ticket-id="3"]').count() === 1, "deadline preview minimum filters computed values and NULLs");
    check(new URL(page.url()).searchParams.get("as_of") === canonical && new URL(page.url()).searchParams.get("shift") === shift, "deadline preview minimum retains reference and shift");
    await page.goto(route({as_of: canonical, shift, min_remaining: "00:00:00", p: "2"}));
    check((await page.getByRole("main").innerText()).includes("1 tickets. Page 2.") && (await page.locator("tbody").innerText()).includes("No tickets on this page."), "deadline preview past-end retains filtered total");
    await page.getByRole("link", {name: "Previous page", exact: true}).click();
    check(new URL(page.url()).searchParams.get("as_of") === canonical && new URL(page.url()).searchParams.get("shift") === shift && new URL(page.url()).searchParams.get("min_remaining") === "00:00:00" && await page.locator('tr[data-ticket-id="3"]').count() === 1, "deadline preview previous link retains every operand");
    await page.getByRole("link", {name: "Clear minimum", exact: true}).click();
    check(new URL(page.url()).searchParams.get("min_remaining") === null && new URL(page.url()).searchParams.get("as_of") === canonical && new URL(page.url()).searchParams.get("shift") === shift && (await page.getByRole("main").innerText()).includes("30 tickets. Page 1."), "deadline preview clearing minimum preserves the reference");
    const response = await page.request.get(route({as_of: input, shift}, true));
    const value = await response.json();
    check(response.status() === 200 && value.category.name === "Browser category" && value.as_of === canonical && value.shift === shift && value.total_tickets === 30 && value.results[0].id === 2 && value.results[0].due_at === "2024-02-28T23:59:59.999999Z" && value.results[0].adjusted_due_at === "2024-02-29T00:00:00.000000Z" && value.results[0].remaining === "-1 23:59:59.999999" && value.results[1].id === 3 && value.results[1].adjusted_due_at === "2024-02-29T00:00:00.000002Z" && value.results[1].remaining === "00:00:00.000001", "deadline preview API preserves exact temporal strings and order");
    check(value.min_remaining === null && value.results[2].id === 1 && value.results[2].due_at === null && value.results[2].adjusted_due_at === null && value.results[2].remaining === null, "deadline preview API retains required nullable values");
    const zeroResponse = await page.request.get(route({as_of: input, min_remaining: "00:00:00"}, true));
    const zero = await zeroResponse.json();
    check(zeroResponse.status() === 200 && zero.shift === "00:00:00" && zero.total_tickets === 1 && zero.results[0].id === 3 && zero.results[0].remaining === "00:00:00" && zero.results[0].due_at === zero.results[0].adjusted_due_at, "deadline preview default zero shift preserves actual zero and excludes NULL");
    const negativeResponse = await page.request.get(route({as_of: input, shift: "-1 23:59:59.999999"}, true));
    const negative = await negativeResponse.json();
    check(negativeResponse.status() === 200 && negative.total_tickets === 30 && negative.results[0].adjusted_due_at === "2024-02-28T23:59:59.999998Z" && negative.results[0].remaining === "-1 23:59:59.999997" && negative.results[1].adjusted_due_at === "2024-02-29T00:00:00.000000Z" && negative.results[1].remaining === "-1 23:59:59.999999", "deadline preview negative shift retains floor-normalized microseconds");
    const dayResponse = await page.request.get(route({as_of: input, shift: "1 00:00:00"}, true));
    const day = await dayResponse.json();
    check(dayResponse.status() === 200 && day.results[1].adjusted_due_at === "2024-03-01T00:00:00.000001Z" && day.results[1].remaining === "1 00:00:00", "deadline preview full-day shift crosses leap February");
    const lastResponse = await page.request.get(route({as_of: input, shift, p: "50001"}, true));
    const last = await lastResponse.json();
    check(lastResponse.status() === 200 && last.page === 50001 && last.total_tickets === 30 && last.results.length === 0 && last.as_of === canonical, "deadline preview last allowed page retains total and reference");
    for (const [path, name] of [
        [origin + "/api/tickets/deadline-preview/", "deadline preview rejects missing API reference"],
        [route({shift}), "deadline preview rejects missing HTML reference"],
        [route({as_of: "2023-02-29T00:00:00Z"}, true), "deadline preview rejects invalid calendar dates"],
        [route({as_of: input, shift: ""}, true), "deadline preview rejects empty duration"],
        [route({as_of: input}, true) + "&as_of=" + encodeURIComponent(input), "deadline preview rejects duplicate reference"],
        [route({as_of: input, unexpected: "1"}, true), "deadline preview rejects unknown input"],
        [route({as_of: input, min_remaining: "0 00:00:00"}, true), "deadline preview rejects noncanonical duration"],
    ]) {
        const rejected = await page.request.get(path);
        check(rejected.status() === 400 && !(await rejected.text()).includes('"results"'), name);
    }
    for (const [api, name] of [[true, "deadline preview valid range overflow fails the entire API"], [false, "deadline preview valid range overflow fails the entire HTML"]]) {
        const failed = await page.request.get(route({as_of: input, shift: "999999999 23:59:59.999999"}, api));
        check(failed.status() === 500 && (await failed.text()) === "Internal Server Error\n", name);
    }
    check(response.headers()["cache-control"] === "no-store" && response.headers()["referrer-policy"] === "same-origin", "deadline preview response is private and uncached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "deadline preview contains no write form");
    await page.getByRole("link", {name: "Choose another reference time", exact: true}).click();
    check(await page.getByLabel("Reference time", {exact: true}).inputValue() === "" && await page.locator("table").count() === 0, "deadline preview reset does not substitute the current time");
    await page.goto(route({as_of: input, shift}));
    check(await page.locator('tr[data-ticket-id="2"]').count() === 1 && await page.getByLabel("Reference time", {exact: true}).inputValue() === canonical && await page.getByLabel("Shift", {exact: true}).inputValue() === shift, "deadline preview exact values visible for visual review");
    return {passed: cases.length, cases};
}
