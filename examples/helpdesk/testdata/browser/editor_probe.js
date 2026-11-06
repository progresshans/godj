// Continue on the same fixture after bulk browser probe passes.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const passed = [];
    const check = (condition, name) => { if (!condition) throw new Error(name); passed.push(name); };
    const input = (name) => page.locator(`[name="${name}"]`);
    const stored = async () => {
        const response = await page.request.get(origin + "/api/tickets/");
        if (response.status() !== 200) throw new Error("committed ticket list failed");
        return response.json();
    };
    await page.goto(origin + "/tickets/edit/");
    check(await input("tickets-INITIAL_FORMS").inputValue() === "3" && await input("tickets-TOTAL_FORMS").inputValue() === "5", "editor reads the committed cohort and two new rows");
    await input("tickets-0-subject").fill("Updated browser ticket");
    await input("tickets-0-closed").check();
    await input("tickets-3-subject").fill("Editor bulk first");
    await input("tickets-3-external_reference").fill("00000000-0000-0000-0000-000000000001");
    await input("tickets-3-labels").selectOption({label: "Desk & <one>"});
    await input("tickets-4-subject").fill("Editor bulk second");
    await input("tickets-4-external_reference").fill("00000000000000000000000000000001");
    await input("tickets-4-labels").selectOption({label: "Spare __prefix__"});
    await page.getByRole("button", {name: "Save all changes", exact: true}).click();
    check(await page.locator('[data-error-code="unique"]').count() > 0, "equivalent UUIDs in new rows reject the entire editor submission");
    check(await input("tickets-0-subject").inputValue() === "Updated browser ticket" && await input("tickets-4-external_reference").inputValue() === "00000000000000000000000000000001", "editor redisplays existing changes and invalid later input");
    const rejected = await stored();
    check(rejected.length === 3 && rejected[0].subject === "Existing browser ticket" && rejected[0].closed === false, "invalid new rows leave the existing row and all new rows unchanged");
    await input("tickets-4-external_reference").fill("00000000-0000-0000-0000-000000000002");
    await page.getByRole("button", {name: "Save all changes", exact: true}).click();
    check(await input("tickets-INITIAL_FORMS").inputValue() === "5", "successful editor submission reloads the complete committed cohort");
    const rows = await stored();
    check(rows.length === 5 && rows[0].subject === "Updated browser ticket" && rows[0].closed === true, "existing-row update commits with both new rows");
    check(rows[3].subject === "Editor bulk first" && rows[4].subject === "Editor bulk second" && rows[3].external_reference.endsWith("0001") && rows[4].external_reference.endsWith("0002"), "new editor rows have stored identities and distinct canonical UUIDs");
    check(rows[3].labels.join(",") === "1" && rows[4].labels.join(",") === "2", "new editor rows commit both scoped label links");
    check(rows[1].expected_cost === "12.30" && rows[1].priority === 0 && rows[1].reviewed === false && rows[1].external_payload_digest.length > 0, "unchanged scalar fields and digest survive the narrow editor");
    return {passed: passed.length, cases: passed};
}
