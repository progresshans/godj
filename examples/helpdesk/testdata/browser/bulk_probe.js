// Run with playwright-cli run-code --filename after opening the fresh fixture.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const passed = [];
    const check = (condition, name) => { if (!condition) throw new Error(name); passed.push(name); };
    const input = (name) => page.locator(`[name="${name}"]`);
    const group = page.locator('[data-inline-prefix="tickets"]');
    const add = () => group.locator(':scope > [data-inline-add]');
    const remove = (index) => group.locator(`[data-inline-row="${index}"] > [data-inline-remove]`);
    const stored = async () => {
        const response = await page.request.get(origin + "/api/tickets/");
        check(response.status() === 200, "authenticated committed ticket list");
        return response.json();
    };
    if (await page.getByRole("textbox", {name: "Username", exact: true}).count()) {
        await page.getByRole("textbox", {name: "Username", exact: true}).fill("operator");
        await page.getByRole("textbox", {name: "Password", exact: true}).fill("demo-password");
        await page.getByRole("button", {name: "Sign in", exact: true}).click();
    }
    await page.goto(origin + "/admin/tickets/");
    await page.getByRole("link", {name: "Create multiple tickets", exact: true}).click();
    check(await input("tickets-TOTAL_FORMS").inputValue() === "2" && await input("tickets-INITIAL_FORMS").inputValue() === "0", "create-only formset has required and extra rows with no existing cohort");
    check(await page.locator('[name$="-id"], [name$="-category"], [name$="-external_payload_digest"]').count() === 0, "server-owned fields are absent from creation controls");
    check(await group.locator("template input").count() === 0, "empty row prototype is inert");
    await input("tickets-0-subject").fill("Bulk browser <one> __prefix__");
    await input("tickets-0-closed").check();
    await input("tickets-0-priority").selectOption({label: "Normal"});
    await input("tickets-0-reviewed").selectOption({label: "No"});
    await input("tickets-0-expected_cost").fill("12.30");
    await input("tickets-0-external_payload").fill('{"source":"browser","value":9007199254740993}');
    await input("tickets-0-labels").selectOption({label: "Desk & <one>"});
    await input("tickets-1-subject").fill("discard this row");
    await add().click();
    check(await input("tickets-2-subject").evaluate(element => document.activeElement === element), "adding a row moves focus to its first input");
    check(!(await input("tickets-2-closed").isChecked()) && await input("tickets-2-priority").inputValue() === "", "new rows keep declared defaults");
    const second = "Bulk browser tickets-2-subject __prefix__";
    await input("tickets-2-subject").fill(second);
    await input("tickets-2-labels").selectOption({label: "Spare __prefix__"});
    await remove(1).click();
    check(await input("tickets-1-subject").inputValue() === second && await input("tickets-1-labels").inputValue() === "2", "middle removal renumbers names without changing text or selected labels");
    for (let count = 2; count < 40; count++) await add().click();
    check(await input("tickets-TOTAL_FORMS").inputValue() === "40" && await add().isHidden(), "forty rows hide the add control");
    await add().evaluate(element => element.click());
    check(await input("tickets-TOTAL_FORMS").inputValue() === "40", "programmatic add cannot exceed the maximum");
    for (let index = 39; index >= 2; index--) await remove(index).click();
    check(await input("tickets-TOTAL_FORMS").inputValue() === "2" && await input("tickets-0-closed").isChecked(), "removing extra rows preserves the original candidate state");
    await input("tickets-1-external_reference").fill("not-a-uuid");
    await page.getByRole("button", {name: "Create multiple tickets", exact: true}).click();
    check(new URL(page.url()).pathname === "/admin/tickets/collection-set/create/", "invalid submission remains on the collection form");
    check(await page.locator('[data-error-field="tickets-1-external_reference"]').count() > 0, "invalid later row has an indexed field diagnostic");
    check(await input("tickets-0-subject").inputValue() === "Bulk browser <one> __prefix__" && await input("tickets-1-external_reference").inputValue() === "not-a-uuid", "escaped values and invalid input survive redisplay");
    check((await stored()).length === 1, "invalid later row leaves every new ticket uncommitted");
    await input("tickets-1-external_reference").fill("");
    await page.getByRole("button", {name: "Create multiple tickets", exact: true}).click();
    check(new URL(page.url()).pathname === "/admin/tickets/", "corrected form redirects after commit");
    const rows = await stored();
    check(rows.length === 3 && rows[1].subject === "Bulk browser <one> __prefix__" && rows[2].subject === second, "both bulk rows persist in input order");
    check(rows[1].closed === true && rows[1].priority === 0 && rows[1].reviewed === false && rows[1].expected_cost === "12.30", "stored scalar presence and zero/false values survive the form");
    check(rows[1].labels.join(",") === "1" && rows[2].labels.join(",") === "2", "each new row retains its selected scoped label");
    check(typeof rows[1].external_payload_digest === "string" && rows[1].external_payload_digest.length > 0 && rows[2].external_payload === null, "stored JSON has a server digest and an omitted payload remains null");
    await page.goto(origin + "/admin/tickets/collection-set/create/");
    await remove(1).click();
    check(await input("tickets-TOTAL_FORMS").inputValue() === "1" && await remove(0).isHidden(), "the minimum retains one creation row");
    await remove(0).evaluate(element => element.click());
    check(await input("tickets-TOTAL_FORMS").inputValue() === "1", "programmatic removal cannot violate the minimum");
    return {passed: passed.length, cases: passed};
}
