// Run after bulk_probe.js, editor_probe.js and bulk_update_probe.js.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (value, name) => { if (!value) throw new Error(name); cases.push(name); };
    const stored = async () => {
        const response = await page.request.get(origin + "/api/tickets/");
        if (response.status() !== 200) throw new Error("cannot read committed priorities");
        return response.json();
    };
    const select = async (ids) => {
        const boxes = page.locator('input[name="selected"]');
        for (let i = 0; i < await boxes.count(); i++) await boxes.nth(i).uncheck();
        for (const id of ids) await page.locator(`input[name="selected"][value="${id}"]`).check();
    };
    const initial = await stored();
    check(initial.length === 5, "five existing tickets retained");
    await page.goto(origin + "/admin/tickets/");
    check(await page.getByRole("button", {name: "Raise selected ticket priorities", exact: true}).count() === 1, "admitted priority action is discoverable");
    const ids = [initial[1].id, initial[2].id];
    let previous = initial;
    for (let turn = 0; turn < 3; turn++) {
        await select(ids);
        await page.getByRole("button", {name: "Raise selected ticket priorities", exact: true}).click();
        const rows = await stored();
        let changed = 0;
        for (let i = 0; i < rows.length; i++) {
            let expected = previous[i].priority;
            if (ids.includes(rows[i].id) && (expected === null || expected === -1 || expected === 0)) {
                expected = expected === null ? 0 : expected + 1;
                changed++;
            }
            check(rows[i].priority === expected, `turn ${turn} row ${i} current priority policy`);
            check(JSON.stringify({...rows[i], priority: null}) === JSON.stringify({...previous[i], priority: null}), `turn ${turn} row ${i} other fields and links preserved`);
        }
        check(await page.locator(`[data-admin-message="priority-raised"][data-affected="${changed}"]`).textContent() === `Priority raised for ${changed} ticket(s).`, `turn ${turn} exact actual-change notice`);
        previous = rows;
    }
    check(previous.filter(row => ids.includes(row.id)).every(row => row.priority === 1), "selected priorities reach Urgent and stay there");
    return {passed: cases.length, cases, final: previous.map(row => ({id: row.id, priority: row.priority}))};
}
