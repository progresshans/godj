// Run with the fixture's real view-only session; all requests are reads.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    const input = "2024-02-29T09:00:00.000001999+09:00";
    const canonical = "2024-02-29T00:00:00.000001Z";
    const shift = "00:00:00.000001";
    const route = (values = {}, api = false) => origin + (api ? "/api" : "") + "/tickets/service-preview/?" + new URLSearchParams(values);
    const rowText = async (id) => (await page.locator(`tr[data-ticket-id="${id}"]`).innerText()).replace(/\s+/g, " ").trim();
    await page.goto(origin + "/tickets/summary/");
    await page.getByRole("link", {name: "Service schedule preview", exact: true}).click();
    check(await page.getByRole("heading", {name: "Service schedule preview", exact: true}).count() === 1, "service preview navigation");
    check(await page.locator("tbody tr").count() === 20 && (await page.getByRole("main").innerText()).includes("30 tickets. Page 1.") && await page.getByLabel("Shift", {exact: true}).inputValue() === "00:00:00", "service preview default zero shift and first scoped page");
    check(await rowText(2) === "Unset summary 2024-02-28 23:59:59.999999 00:00:00.000001 2024-02-28T23:59:59.999999Z 2024-02-28T23:59:59.999999Z 2024-02-28T23:59:59.999999Z 2024-02-29T00:00:00.000000Z -1 23:59:59.999999", "service preview exact start finish and negative slack across leap midnight");
    check(await rowText(3) === "Unset summary 2024-02-29 00:00:00 00:00:00.000002 2024-02-29T00:00:00.000001Z 2024-02-29T00:00:00.000000Z 2024-02-29T00:00:00.000000Z 2024-02-29T00:00:00.000002Z -1 23:59:59.999999", "service preview midnight remains a real clock");
    check(await rowText(6) === "Summary priority 2024-02-29 12:00:00 Not set Not set 2024-02-29T12:00:00.000000Z 2024-02-29T12:00:00.000000Z Not set Not set", "service preview missing elapsed retains the computed start");
    await page.getByLabel("Shift", {exact: true}).fill(shift);
    await page.getByRole("button", {name: "Preview schedule", exact: true}).click();
    check(new URL(page.url()).searchParams.get("shift") === shift && (await rowText(2)).endsWith("2024-02-29T00:00:00.000000Z 2024-02-29T00:00:00.000001Z -1 23:59:59.999998"), "service preview shift updates the schedule and exact slack");
    await page.getByRole("link", {name: "Next page", exact: true}).click();
    check(await page.locator("tbody tr").count() === 10 && new URL(page.url()).searchParams.get("shift") === shift && (await page.getByRole("main").innerText()).includes("30 tickets. Page 2."), "service preview second page preserves the shift");
    check(await page.getByRole("link", {name: "Next page", exact: true}).count() === 0, "service preview has no false next page");
    check(await rowText(1) === "Existing browser ticket Not set Not set Not set Not set Not set Not set Not set Not set" && await rowText(4) === "Summary priority 2024-02-29 Not set Not set Not set Not set Not set Not set Not set" && await rowText(5) === "Summary priority Not set 00:00:00 Not set Not set Not set Not set Not set Not set", "service preview independently missing date and clock propagate NULL");
    check(await rowText(30) === "Legacy summary 2024-03-24 12:34:56 00:00:00 Not set 2024-03-24T12:34:56.000000Z 2024-03-24T12:34:56.000001Z 2024-03-24T12:34:56.000001Z Not set", "service preview zero elapsed and missing deadline remain distinct");
    await page.getByRole("button", {name: "Preview schedule", exact: true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && await page.locator("tbody tr").count() === 20, "service preview adjustment resets the page");
    await page.getByLabel("Earliest adjusted start", {exact: true}).fill(input);
    await page.getByRole("button", {name: "Filter current preview", exact: true}).click();
    check(new URL(page.url()).searchParams.get("min_start") === input && await page.getByLabel("Earliest adjusted start", {exact: true}).inputValue() === canonical && new URL(page.url()).searchParams.get("shift") === shift, "service preview minimum normalizes offset and submicroseconds");
    check((await page.getByRole("main").innerText()).includes("26 tickets. Page 1.") && await page.locator("tbody tr").count() === 20 && await page.locator("tbody tr").first().getAttribute("data-ticket-id") === "3", "service preview minimum filters adjusted starts before paging");
    await page.getByRole("link", {name: "Next page", exact: true}).click();
    check(await page.locator("tbody tr").count() === 6 && new URL(page.url()).searchParams.get("min_start") === canonical && new URL(page.url()).searchParams.get("shift") === shift, "service preview filtered page preserves all inputs");
    await page.goto(route({shift, min_start: canonical, p: "3"}));
    check((await page.getByRole("main").innerText()).includes("26 tickets. Page 3.") && (await page.locator("tbody").innerText()).includes("No tickets on this page."), "service preview past-end retains the filtered total");
    await page.getByRole("link", {name: "Previous page", exact: true}).click();
    check(new URL(page.url()).searchParams.get("p") === "2" && new URL(page.url()).searchParams.get("min_start") === canonical && new URL(page.url()).searchParams.get("shift") === shift && await page.locator("tbody tr").count() === 6, "service preview previous link retains every input");
    await page.getByRole("button", {name: "Preview schedule", exact: true}).click();
    check(new URL(page.url()).searchParams.get("p") === null && new URL(page.url()).searchParams.get("min_start") === canonical && (await page.getByRole("main").innerText()).includes("26 tickets. Page 1."), "service preview adjustment retains the minimum");
    await page.getByRole("link", {name: "Clear earliest start", exact: true}).click();
    check(new URL(page.url()).searchParams.get("min_start") === null && new URL(page.url()).searchParams.get("shift") === shift && (await page.getByRole("main").innerText()).includes("30 tickets. Page 1."), "service preview clear minimum preserves shift");
    const response = await page.request.get(route({shift}, true));
    const value = await response.json();
    check(response.status() === 200 && value.category.name === "Browser category" && value.page === 1 && value.page_size === 20 && value.shift === shift && value.min_start === null && value.total_tickets === 30 && value.results.length === 20 && value.results[0].id === 2 && value.results[1].id === 3, "service preview API exact envelope and ordering");
    const first = value.results[0];
    check(Object.keys(first).length === 10 && first.service_on === "2024-02-28" && first.service_at === "23:59:59.999999" && first.elapsed === "00:00:00.000001" && first.due_at === "2024-02-28T23:59:59.999999Z" && first.service_start === "2024-02-28T23:59:59.999999Z" && first.adjusted_start === "2024-02-29T00:00:00.000000Z" && first.finish_at === "2024-02-29T00:00:00.000001Z" && first.deadline_slack === "-1 23:59:59.999998", "service preview API exact original and computed temporal values");
    const secondResponse = await page.request.get(route({shift, p: "2"}, true));
    const second = await secondResponse.json();
    const unset = second.results.find(row => row.id === 1);
    const zero = second.results.find(row => row.id === 30);
    check(secondResponse.status() === 200 && second.results.length === 10 && Object.keys(unset).length === 10 && ["service_on", "service_at", "elapsed", "due_at", "service_start", "adjusted_start", "finish_at", "deadline_slack"].every(key => unset[key] === null) && zero.elapsed === "00:00:00" && zero.finish_at === zero.adjusted_start && zero.deadline_slack === null, "service preview API required NULL and zero duration");
    const filteredResponse = await page.request.get(route({shift, min_start: input}, true));
    const filtered = await filteredResponse.json();
    check(filteredResponse.status() === 200 && filtered.min_start === canonical && filtered.total_tickets === 26 && filtered.results[0].id === 3 && filtered.results.every(row => row.adjusted_start !== null), "service preview API normalized inclusive minimum");
    const negativeResponse = await page.request.get(route({shift: "-1 23:59:59.999999"}, true));
    const negative = await negativeResponse.json();
    check(negativeResponse.status() === 200 && negative.results[1].adjusted_start === "2024-02-28T23:59:59.999999Z" && negative.results[1].finish_at === "2024-02-29T00:00:00.000001Z" && negative.results[1].deadline_slack === "00:00:00", "service preview negative shift produces exact zero slack");
    const dayResponse = await page.request.get(route({shift: "1 00:00:00"}, true));
    const day = await dayResponse.json();
    check(dayResponse.status() === 200 && day.results[1].adjusted_start === "2024-03-01T00:00:00.000000Z" && day.results[1].finish_at === "2024-03-01T00:00:00.000002Z" && day.results[1].deadline_slack === "-2 23:59:59.999999", "service preview full-day shift crosses leap February");
    const lastResponse = await page.request.get(route({shift, min_start: input, p: "50001"}, true));
    const last = await lastResponse.json();
    check(lastResponse.status() === 200 && last.page === 50001 && last.total_tickets === 26 && last.results.length === 0 && last.min_start === canonical, "service preview last allowed page preserves total and minimum");
    for (const [path, name] of [
        [route({shift: ""}, true), "service preview rejects empty shift"],
        [route({shift: "0 00:00:00"}), "service preview rejects noncanonical shift"],
        [route({min_start: ""}, true), "service preview rejects empty minimum"],
        [route({min_start: "2023-02-29T00:00:00Z"}), "service preview rejects invalid calendar date"],
        [route({min_start: "2024-02-29T00:00:00"}, true), "service preview rejects missing offset"],
        [route({shift}, true) + "&shift=" + encodeURIComponent(shift), "service preview rejects duplicate input"],
        [route({category: "2"}, true), "service preview rejects supplied category"],
        [route({p: "50002"}), "service preview rejects page outside its bound"],
    ]) {
        const rejected = await page.request.get(path);
        check(rejected.status() === 400 && !(await rejected.text()).includes('"results"'), name);
    }
    for (const [api, name] of [[true, "service preview overflow fails the entire API"], [false, "service preview overflow fails the entire HTML"]]) {
        const failed = await page.request.get(route({shift: "999999999 23:59:59.999999"}, api));
        check(failed.status() === 500 && (await failed.text()) === "Internal Server Error\n", name);
    }
    check(response.headers()["cache-control"] === "no-store" && response.headers()["referrer-policy"] === "same-origin", "service preview response is private and uncached");
    check(await page.locator('form[method="post"], input[name="csrfmiddlewaretoken"]').count() === 0, "service preview contains no write form");
    await page.getByRole("link", {name: "Reset preview", exact: true}).click();
    check(new URL(page.url()).search === "" && await page.getByLabel("Shift", {exact: true}).inputValue() === "00:00:00" && await page.getByLabel("Earliest adjusted start", {exact: true}).inputValue() === "" && await page.locator("tbody tr").count() === 20, "service preview reset restores explicit zero shift");
    const inspect = async () => await page.locator('tr[data-ticket-id="2"]').evaluate(row => {
        const cells = Array.from(row.children);
        const expected = [
            [1, "2024-02-28"], [2, "23:59:59.999999"], [3, "00:00:00.000001"],
            [4, "23:59:59.999999Z"], [5, "23:59:59.999999Z"],
            [6, "23:59:59.999999Z"], [7, "00:00:00.000000Z"], [8, "23:59:59.999999"],
        ];
        const parts = expected.map(([index, token]) => {
            const cell = cells[index];
            const start = cell.textContent.indexOf(token);
            if (start < 0) return {column: index, intact: false, missing: true};
            const walker = document.createTreeWalker(cell, NodeFilter.SHOW_TEXT);
            const range = document.createRange();
            let offset = 0, started = false, ended = false;
            for (let node = walker.nextNode(); node; node = walker.nextNode()) {
                const next = offset + node.textContent.length;
                if (!started && start < next) { range.setStart(node, start - offset); started = true; }
                if (started && start + token.length <= next) { range.setEnd(node, start + token.length - offset); ended = true; break; }
                offset = next;
            }
            if (!started || !ended) return {column: index, intact: false, missing: true};
            const rects = Array.from(range.getClientRects()).filter(rect => rect.width > 0 && rect.height > 0);
            const bounds = cell.getBoundingClientRect();
            const lines = new Set(rects.map(rect => Math.round(rect.top))).size;
            const contained = rects.length > 0 && rects.every(rect => rect.left >= bounds.left && rect.right <= bounds.right);
            return {column: index, intact: lines === 1 && contained, lines, contained};
        });
        return {parts, page_contained: document.documentElement.scrollWidth <= window.innerWidth};
    });
    const viewport = page.viewportSize();
    if (!viewport || viewport.width !== 1440 || viewport.height !== 1100) throw new Error("expected desktop fixture viewport");
    const desktop = await inspect();
    let narrow;
    try {
        await page.setViewportSize({width: 390, height: 844});
        narrow = await inspect();
    } finally { await page.setViewportSize(viewport); }
    const readable = [desktop, narrow].every(size => size.page_contained && size.parts.every(part => part.intact));
    check(readable && await page.getByRole("region", {name: "Service schedule results", exact: true}).count() === 1 && await page.getByRole("columnheader").count() === 9 && await page.locator('tr[data-ticket-id="2"]').count() === 1, "service preview complete schedule visible for visual review");
    return {passed: cases.length, cases};
}
