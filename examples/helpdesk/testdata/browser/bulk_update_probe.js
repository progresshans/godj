// Continue on a fresh fixture after bulk_probe.js and editor_probe.js pass.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const passed = [];
    const check = (condition, name) => { if (!condition) throw new Error(name); passed.push(name); };
    const stored = async () => {
        const response = await page.request.get(origin + "/api/tickets/");
        if (response.status() !== 200) throw new Error("committed ticket list failed");
        return response.json();
    };
    const select = async (keys) => {
        const boxes = page.locator('input[name="selected"]');
        for (let index = 0; index < await boxes.count(); index++) await boxes.nth(index).uncheck();
        for (const key of keys) await page.locator(`input[name="selected"][value="${key}"]`).check();
    };
    const initial = await stored();
    check(initial.length === 5 && initial[0].closed && initial[1].closed && initial.slice(2).every(row => !row.closed), "expected initial committed ticket states");
    await page.goto(origin + "/admin/tickets/");
    check(await page.getByRole("button", {name: "Close selected tickets", exact: true}).count() === 1 && await page.getByRole("button", {name: "Reopen selected tickets", exact: true}).count() === 1, "both admitted actions are discoverable");
    await select([initial[1].id, initial[2].id]);
    await page.getByRole("button", {name: "Close selected tickets", exact: true}).click();
    check(await page.locator('[data-admin-message="closed"][data-affected="1"]').textContent() === "1 ticket(s) closed.", "close action counts only the one selected ticket that changed");
    let rows = await stored();
    check(rows[1].closed && rows[2].closed && !rows[3].closed && !rows[4].closed, "only selected tickets close");
    await select([initial[1].id, initial[2].id]);
    await page.getByRole("button", {name: "Close selected tickets", exact: true}).click();
    check(await page.locator('[data-admin-message="closed"][data-affected="0"]').textContent() === "0 ticket(s) closed.", "repeated close is a no-op");
    await select([initial[0].id, initial[1].id]);
    await page.getByRole("button", {name: "Reopen selected tickets", exact: true}).click();
    rows = await stored();
    check(await page.locator('[data-admin-message="reopened"][data-affected="2"]').textContent() === "2 ticket(s) reopened." && !rows[0].closed && !rows[1].closed && rows[2].closed, "reopen changes both selected tickets and keeps the unselected closed ticket");
    check(rows[1].expected_cost === initial[1].expected_cost && rows[1].external_payload_digest === initial[1].external_payload_digest && rows.every((row, index) => row.labels.join(",") === initial[index].labels.join(",")), "selected actions preserve unrelated fields and memberships");
    await page.goto(origin + "/tickets/edit/");
    await page.locator('[name="tickets-3-subject"]').fill("Edited batch fourth");
    await page.locator('[name="tickets-4-subject"]').fill("Edited batch fifth");
    await page.getByRole("button", {name: "Save all changes", exact: true}).click();
    rows = await stored();
    check(rows.length === 5 && rows[3].subject === "Edited batch fourth" && rows[4].subject === "Edited batch fifth", "two existing editor rows commit together");
    check(rows[3].external_reference === initial[3].external_reference && rows[4].external_reference === initial[4].external_reference && rows[0].subject === initial[0].subject, "editor batch preserves omitted fields and untouched rows");
    return {passed: passed.length, cases: passed};
}
