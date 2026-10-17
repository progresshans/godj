// Run through playwright-cli run-code --filename after opening this fixture.
// No @playwright/test runner or production account is involved.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected the isolated loopback fixture");
    const passed = [];
    const check = (condition, name) => {
        if (!condition) throw new Error(name);
        passed.push(name);
    };
    const input = (name) => page.locator(`[name="${name}"]`);
    const group = (prefix) => page.locator(`[data-inline-prefix="${prefix}"]`);
    const add = (prefix) => group(prefix).locator(":scope > [data-inline-add]");
    const remove = (prefix, index) => group(prefix).getByRole("button", {name: `Remove item ${index + 1}`, exact: true});
    const edit = origin + "/admin/categories/change/?id=1";
    const login = async (role) => {
        if (await page.getByRole("button", {name: "Log out"}).count()) await page.getByRole("button", {name: "Log out"}).click();
        await page.goto(origin + "/admin/login/");
        await page.getByRole("textbox", {name: "Username", exact: true}).fill(role);
        await page.getByRole("textbox", {name: "Password", exact: true}).fill("demo-password");
        await page.getByRole("button", {name: "Sign in", exact: true}).click();
        await page.goto(edit);
    };
    await login("editor");
    check(await input("labels-TOTAL_FORMS").inputValue() === "1", "zero configured extra rows still allow addition");
    const original = await input("labels-0-id").inputValue();
    const ticket = await input("tickets-0-id").inputValue();
    check(original !== "" && ticket !== "", "existing identities came from the server");
    check(await group("labels").locator("template input").count() === 0, "prototype controls are inert document-fragment content");
    await page.evaluate(() => {
        window.inlineObservedEvents = [];
        for (const name of ["formset:added", "formset:removed"]) document.addEventListener(name, (event) => window.inlineObservedEvents.push([name, event.detail.formsetName]));
    });
    await add("labels").click();
    check(await input("labels-1-name").evaluate((element) => document.activeElement === element), "add moves keyboard focus into the new row");
    check(await input("labels-1-id").inputValue() === "" && await input("labels-1-category").inputValue() === "1", "prototype has no child key and retains the server parent");
    await input("labels-1-name").fill("discard me");
    await add("labels").click();
    const literal = "keep labels-2-name __prefix__";
    await input("labels-2-name").fill(literal);
    await add("labels").click();
    check(await add("labels").isHidden(), "maximum hides the add button");
    await add("labels").evaluate((element) => element.click());
    check(await input("labels-TOTAL_FORMS").inputValue() === "4", "programmatic click cannot exceed the UI maximum");
    await remove("labels", 1).click();
    check(await input("labels-1-name").inputValue() === literal, "renumbering never changes user values containing structural markers");
    await remove("labels", 2).click();
    check(await input("labels-TOTAL_FORMS").inputValue() === "2" && await input("labels-INITIAL_FORMS").inputValue() === "1", "removal changes total but keeps the initial cohort");
    check(await input("labels-0-id").inputValue() === original && await input("tickets-0-id").inputValue() === ticket && await input("tickets-TOTAL_FORMS").inputValue() === "2", "another inline and existing keys remain unchanged");
    check(await group("labels").locator('[data-inline-row="0"] [data-inline-remove]').count() === 0, "saved rows have deletion intent instead of DOM removal");
    check(JSON.stringify(await page.evaluate(() => window.inlineObservedEvents)) === JSON.stringify([["formset:added", "labels"], ["formset:added", "labels"], ["formset:added", "labels"], ["formset:removed", "labels"], ["formset:removed", "labels"]]), "add and remove events identify their own formset");

    check(await remove("tickets", 1).isHidden(), "minimum hides removal of the last required extra row");
    await input("tickets-1-subject").fill("discarded ticket");
    await input("tickets-1-closed").check();
    await add("tickets").click();
    check(!(await input("tickets-2-closed").isChecked()), "a new checkbox uses the prototype default instead of the previous row state");
    await input("tickets-2-subject").fill("Saved ticket __prefix__");
    await input("tickets-2-closed").check();
    check(await remove("tickets", 1).isVisible(), "an extra row becomes removable above the minimum");
    await remove("tickets", 1).click();
    check(await input("tickets-1-closed").isChecked(), "checkbox state survives renumbering");
    check(await remove("tickets", 1).isHidden(), "removal returns to the declared minimum");

    // The server redisplays a later invalid row. Remove the earlier extra row
    // and check that both names and diagnostic anchors follow the new index.
    await add("labels").click();
    const invalid = "x".repeat(300);
    await input("labels-2-name").fill(invalid);
    await page.getByRole("button", {name: "Save", exact: true}).click();
    check(new URL(page.url()).pathname.endsWith("/change/"), "invalid submission stays on the bound form");
    check(await page.locator('[data-error-field="labels-2-name"][data-error-code="max_length"]').count() > 0, "invalid added row displays a field diagnostic");
    check(await input("labels-2-name").inputValue() === invalid && await input("tickets-1-closed").isChecked(), "redisplay retains original text and checkbox input");
    await remove("labels", 1).click();
    check(await page.locator('[data-error-field="labels-1-name"][data-error-code="max_length"]').count() > 0 && await page.locator('[data-error-field="labels-2-name"]').count() === 0, "diagnostic anchors are renumbered without losing the error");
    await input("labels-1-name").fill(literal);
    await page.getByRole("button", {name: "Save", exact: true}).click();
    check(new URL(page.url()).pathname === "/admin/categories/", "corrected browser submission commits successfully");
    await page.goto(edit);
    check(await input("labels-INITIAL_FORMS").inputValue() === "2" && await input("labels-1-name").inputValue() === literal && await input("labels-1-id").inputValue() !== "", "new label is read back with a stored identity");
    check(await input("tickets-INITIAL_FORMS").inputValue() === "2" && await input("tickets-1-subject").inputValue() === "Saved ticket __prefix__" && await input("tickets-1-closed").isChecked(), "second inline persists independently in SQLite");

    await login("noadd");
    check(await page.locator("template[data-inline-empty], [data-inline-add], [data-inline-remove]").count() === 0, "no-add admission publishes neither prototypes nor extra-row controls");
    check(!(await input("labels-0-name").isDisabled()), "change permission still allows existing fields");
    await login("readonly");
    check(await input("labels-0-name").isDisabled() && await input("tickets-0-subject").isDisabled(), "view-only child fields are disabled");
    check(await input("labels-0-id").inputValue() === original && !(await input("labels-0-id").isDisabled()), "read-only identity remains a successful hidden control");
    check(await page.evaluate(() => !Array.from(new FormData(document.querySelector('form[action^="/admin/categories/"]')).keys()).includes("labels-0-name")), "browser successful controls omit disabled fields");
    await login("viewer");
    check(await page.locator('body[data-admin-view="detail"]').count() === 1 && await page.getByRole("button", {name: "Save", exact: true}).count() === 0, "view-only parent has no mutation form");

    await login("editor");
    await page.goto(origin + "/admin/categories/add/");
    check(await input("labels-TOTAL_FORMS").inputValue() === "0", "pending parent starts with zero label rows");
    await input("name").fill("New via browser");
    await add("labels").click();
    check(await input("labels-0-category").inputValue() === "", "pending prototype carries no invented parent key");
    await input("labels-0-name").fill("New child __prefix__");
    await input("tickets-0-subject").fill("First required ticket");
    await input("tickets-1-subject").fill("Second required ticket");
    await page.getByRole("button", {name: "Add", exact: true}).click();
    await page.getByRole("row").filter({has:page.getByRole("cell", {name:"New via browser", exact:true})}).getByRole("link", {name:"Change",exact:true}).click();
    check(await input("labels-INITIAL_FORMS").inputValue() === "1" && await input("labels-0-name").inputValue() === "New child __prefix__" && await input("labels-0-category").inputValue() !== "", "new parent and browser-added child receive stored keys together");
    return {passed: passed.length, cases: passed};
}
