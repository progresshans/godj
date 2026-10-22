// Fresh collections-mode fixture; all writes and final file/DB reads are native.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    page.context().setDefaultTimeout(10000);
    page.context().setDefaultNavigationTimeout(20000);
    const edit = origin + "/admin/assets/collection-set/edit/";
    const cases = [];
    const check = async (ok, name) => {
        if (!ok) throw new Error(name);
        cases.push(name);
        await page.evaluate(name => console.info("COLLECTION_PROBE: " + name), name);
    };
    const input = (name, target = page) => target.locator(`[name="${name}"]`);
    const field = (name, target = page) => target.locator(`[data-field-name="${name}"]`);
    const group = () => page.locator('[data-inline-prefix="assets"]');
    const add = () => group().locator(':scope > [data-inline-add]');
    const selected = (name, target = page) => input(name, target).evaluate(element => Array.from(element.selectedOptions, option => option.value).sort());
    const same = (left, right) => JSON.stringify(left) === JSON.stringify(right);
    const ready = (name) => page.waitForFunction(name => {
        const field = document.querySelector(`[data-field-name="${name}"]`);
        return field && !field.hasAttribute("aria-busy") && field.querySelector('[data-choice-status]')?.textContent.startsWith("Page ");
    }, name, {timeout: 10000});
    const search = async (name, term, key) => {
        const container = field(name);
        const received = page.waitForResponse(response => response.request().method() === "GET" && response.url().includes("/choices/labels/") && new URL(response.url()).searchParams.get("q") === term, {timeout: 10000});
        await container.locator('[data-choice-query]').fill(term);
        await container.locator('[data-choice-search]').click();
        const response = await received;
        if (response.status() !== 200) throw new Error("collection choice search: " + response.status());
        await ready(name);
        await input(name).locator(`option[value="${key}"][data-choice-page]`).waitFor({state: "attached"});
    };
    const login = async role => {
        await page.goto(origin + "/admin/");
        const logout = page.getByRole("button", {name: "Log out", exact: true});
        if (await logout.count()) await logout.click();
        await page.goto(origin + "/admin/login/");
        await page.getByRole("textbox", {name: "Username", exact: true}).fill(role);
        await page.getByRole("textbox", {name: "Password", exact: true}).fill("demo-password");
        await page.getByRole("button", {name: "Sign in", exact: true}).click();
        await page.goto(edit);
    };
    const submit = async (target = page) => {
        const received = target.waitForResponse(response => response.request().method() === "POST" && response.url() === edit, {timeout: 10000});
        await target.getByRole("button", {name: "Edit inventory", exact: true}).click();
        return (await received).status();
    };
    const upload = async (name, filename, content) => input(name).setInputFiles({name: filename, mimeType: "text/plain", buffer: Buffer.from(content)});
    const state = async (target) => {
        const total = Number(await input("assets-INITIAL_FORMS", target).inputValue());
        const rows = [];
        for (let index = 0; index < total; index++) {
            const prefix = "assets-" + index + "-";
            rows.push({id: await input(prefix + "id", target).inputValue(), revision: await input(prefix + "expected_revision", target).inputValue(), code: await input(prefix + "code", target).inputValue(), enabled: await input(prefix + "enabled", target).isChecked(), labels: await selected(prefix + "labels", target), file: await field(prefix + "attachment", target).textContent()});
        }
        return rows;
    };
    const storedState = async () => {
        const fresh = await page.context().newPage();
        try {
            const response = await fresh.goto(edit);
            if (!response || response.status() !== 200) throw new Error("collection stored read denied");
            return await state(fresh);
        } finally { await fresh.close(); }
    };
    await login("editor");
    await ready("assets-0-labels");
    await check(await page.locator('form[action="/admin/assets/collection-set/edit/"]').getAttribute("enctype") === "multipart/form-data" && await input("assets-INITIAL_FORMS").inputValue() === "2" && await input("assets-TOTAL_FORMS").inputValue() === "2", "existing collection renders a multipart cohort without a parent identity");
    await check(await input("assets-0-id").inputValue() === "1" && await input("assets-1-id").inputValue() === "2" && await input("assets-0-expected_revision").inputValue() === "1" && await page.locator('input[name$="-category"]').count() === 0, "stored identities revisions and server scope are separate from editable fields");
    await check(await page.locator('[data-asset-code]').count() === 2 && await page.locator('[data-asset-file]').count() === 2 && await field("assets-0-attachment").textContent().then(value => value.includes("old/1.txt")), "registered text and file controls keep current stored file references");
    await check(same(await selected("assets-0-labels"), ["25"]) && await input("assets-0-labels").locator('option[data-choice-page]').count() === 20 && await input("assets-0-labels").locator('option').count() === 21, "collection choices retain an off-page selection within a bounded page");
    const foreign = await page.context().request.get(edit + "choices/labels/?index=0&row_id=3");
    await check(foreign.status() === 404, "collection search rejects a stored row outside the current cohort");
    const before = await state(page);
    await add().click();
    await check(await input("assets-2-code").evaluate(element => element === document.activeElement) && await input("assets-2-id").inputValue() === "" && await input("assets-2-expected_revision").inputValue() === "", "keyboard focus enters a new custom widget without a fabricated key or revision");
    await input("assets-2-code").fill("discard");
    await add().click();
    const invalid = '  reject assets-3-code __prefix__ <img src=x>  ';
    await input("assets-3-code").fill(invalid);
    await upload("assets-3-attachment", "new.txt", "new attachment");
    await input("assets-0-code").fill(" first revised ");
    await upload("assets-0-attachment", "first-v2.txt", "first replacement");
    await search("assets-0-labels", "03", "3");
    await input("assets-0-labels").selectOption(["25", "3"]);
    await search("assets-3-labels", "01", "1");
    await input("assets-3-labels").selectOption(["1"]);
    await input("assets-1-DELETE").check();
    await input("assets-0-ORDER").fill("2");
    await input("assets-3-ORDER").fill("1");
    await check(await input("assets-1-id").inputValue() === "2" && await input("assets-1-DELETE").isChecked() && await input("assets-INITIAL_FORMS").inputValue() === "2", "deleting a saved row retains its identity and the initial management cohort");
    await check(await submit() === 200 && page.url() === edit, "invalid custom input stays on the bound collection form");
    await check(await page.locator('[data-error-field="assets-3-code"][data-error-code="code_review"]').count() === 1 && await page.locator('[data-inline-error="assets-3"][data-error-code="model_review"]').count() === 1 && await page.locator('[data-error-code="collection_review"]').count() === 1, "field model and collection validation diagnostics are composed");
    await check(await input("assets-3-code").inputValue() === invalid && await field("assets-3-code").locator('img').count() === 0, "custom redisplay retains original text while escaping markup");
    await check(await input("assets-3-attachment").evaluate(element => element.files.length) === 0 && await page.locator('[data-file-reselect="assets-3-attachment"]').count() === 1 && await page.locator('[data-file-reselect="assets-0-attachment"]').count() === 1, "invalid multipart collection requires reselection of both uploaded files");
    await check(same(await storedState(), before), "field rejection leaves all current database values and memberships unchanged");
    // Reselect first, then remove a different unsaved row. Browser File objects
    // must survive renumbering; the earlier diagnostic must follow the new name.
    await upload("assets-3-attachment", "new.txt", "new attachment");
    await group().getByRole("button", {name: "Remove item 3", exact: true}).click();
    await ready("assets-2-labels");
    await check(await input("assets-TOTAL_FORMS").inputValue() === "3" && await input("assets-INITIAL_FORMS").inputValue() === "2" && await input("assets-2-code").inputValue() === invalid && await input("assets-0-id").inputValue() === "1", "removing an extra row renumbers structure without rewriting user text or saved keys");
    await check(await input("assets-2-attachment").evaluate(async element => element.files.length === 1 && element.files[0].name === "new.txt" && await element.files[0].text() === "new attachment"), "custom file selection and bytes survive collection row renumbering");
    await check(await input("assets-2-code").getAttribute("aria-describedby") === "assets-2-code-errors" && await page.locator('#assets-2-code-errors [data-error-field="assets-2-code"]').count() === 1 && await page.locator('[data-file-reselect="assets-2-attachment"]').count() === 1 && await page.locator('[data-file-reselect="assets-3-attachment"]').count() === 0, "renumbering moves custom error links and file reselection anchors together");
    await input("assets-2-code").fill("new assets-3-code __prefix__");
    await upload("assets-0-attachment", "first-v2.txt", "first replacement");
    await check(same(await selected("assets-2-labels"), ["1"]) && await input("assets-2-ORDER").inputValue() === "1", "renumbered row retains searched membership and explicit order input");
    await check(await submit() === 302 && new URL(page.url()).pathname === "/admin/assets/", "corrected collection commits changed new deleted and related rows");
    await page.goto(edit);
    const mixed = await state(page);
    await check(mixed.length === 2 && mixed[0].id === "1" && mixed[1].id === "4" && mixed[0].code === "FIRST REVISED" && mixed[1].code === "NEW ASSETS-3-CODE __PREFIX__" && mixed[0].revision === "2" && mixed[1].revision === "1", "stored reload confirms normalized values generated keys and revision fences");
    await check(same(mixed[0].labels, ["25", "3"]) && same(mixed[1].labels, ["1"]) && mixed[0].file.includes("attempt-1/0/first-v2.txt") && mixed[1].file.includes("attempt-1/2/new.txt"), "stored reload confirms both file references and cross-app memberships");
    await check(await submit() === 302 && same(await storedState(), mixed), "unchanged collection submission preserves revisions scalar values and file references");
    await page.goto(edit);
    await input("assets-0-attachment-clear").check();
    await check(await submit() === 302, "clear file intent passes through the existing collection writer");
    await page.goto(edit);
    const cleared = await state(page);
    await check(cleared[0].revision === "3" && !cleared[0].file.includes("Currently:") && cleared[1].file === mixed[1].file && await input("assets-0-attachment-clear").count() === 0, "clear removes only the model reference and preserves the other row file");
    await ready("assets-0-labels");
    await field("assets-0-labels").locator('[data-choice-clear]').click();
    await search("assets-0-labels", "04", "4");
    await input("assets-0-labels").selectOption(["4"]);
    await check(await submit() === 302, "membership-only collection changes use the same typed save path");
    const membership = await storedState();
    await check(membership[0].revision === "4" && same(membership[0].labels, ["4"]) && membership[0].code === mixed[0].code && membership[0].file === cleared[0].file, "membership-only reload preserves scalar and cleared file state");
    await login("readonly");
    await check(await input("assets-0-code").isDisabled() && await input("assets-0-attachment").isDisabled() && await input("assets-0-labels").isDisabled() && await input("assets-0-ORDER").count() === 0 && await input("assets-0-DELETE").count() === 0, "read-only existing rows disable custom fields and omit mutation controls");
    await check(await page.evaluate(() => {
        const data = new FormData(document.querySelector('form[action="/admin/assets/collection-set/edit/"]'));
        return data.get("assets-0-id") === "1" && data.get("assets-0-expected_revision") === "4" && !data.has("assets-0-code") && !data.has("assets-0-attachment") && !data.has("assets-0-labels");
    }), "read-only successful controls retain identity and revision without editable payloads");
    await add().click();
    await input("assets-2-code").fill("reject readonly");
    await check(await submit() === 200 && await page.locator('[data-error-field="assets-2-code"][data-error-code="code_review"]').count() === 1, "an invalid added row redisplays while existing collection rows stay read-only");
    await add().click();
    await check(await input("assets-3-code").isEnabled() && await input("assets-3-ORDER").count() === 0 && await input("assets-3-DELETE").count() === 0 && await input("assets-0-code").isDisabled(), "a prototype added after redisplay retains denied order and delete permissions");
    await group().getByRole("button", {name: "Remove item 4", exact: true}).click();
    await input("assets-2-code").fill("readonlyadd");
    await upload("assets-2-attachment", "readonly.txt", "readonly attachment");
    await search("assets-2-labels", "05", "5");
    await input("assets-2-labels").selectOption(["5"]);
    await check(await submit() === 302, "add permission creates a file row while all existing rows stay read-only");
    await login("editor");
    const added = await state(page);
    await check(added.length === 3 && same(added.slice(0, 2), membership) && added[2].id === "5" && added[2].code === "READONLYADD" && added[2].file.includes("attempt-5/2/readonly.txt") && same(added[2].labels, ["5"]), "stored reload confirms read-only preservation and the new row ownership");
    await input("assets-0-code").fill("rolled back first");
    await upload("assets-0-attachment", "rollback-first.txt", "rolled back first");
    await add().click();
    await input("assets-3-code").fill("audit_fail");
    await upload("assets-3-attachment", "rollback-new.txt", "rolled back new");
    await search("assets-3-labels", "06", "6");
    await input("assets-3-labels").selectOption(["6"]);
    await check(await submit() === 200 && await page.locator('[data-error-code="writer_review"]').count() === 1, "late audit rejection redisplays only after the native transaction rolls back");
    await check(await input("assets-0-code").inputValue() === "rolled back first" && await input("assets-3-code").inputValue() === "audit_fail" && await page.locator('[data-file-reselect="assets-0-attachment"]').count() === 1 && await page.locator('[data-file-reselect="assets-3-attachment"]').count() === 1, "late rejection retains raw text and requests both files again");
    await check(same(await storedState(), added), "fresh read confirms rollback of earlier scalar membership revision and file-reference writes");
    await page.goto(edit);
    const stale = await state(page);
    const mutator = await page.context().newPage();
    try {
        await mutator.goto(edit);
        await input("assets-1-code", mutator).fill("concurrent");
        if (await submit(mutator) !== 302) throw new Error("concurrent collection write failed");
    } finally { await mutator.close(); }
    await input("assets-0-code").fill("stale overwrite");
    await check(await submit() === 409, "an older collection revision is rejected before its write callback");
    const current = await storedState();
    await check(current[0].code === stale[0].code && current[0].revision === stale[0].revision && current[1].code === "CONCURRENT" && current[1].revision === "2" && current[2].code === stale[2].code, "stale rejection preserves the newer committed cohort");
    await login("noadd");
    await check(await add().count() === 0 && await group().locator('template').count() === 0 && !await input("assets-0-code").isDisabled(), "denied add permission omits collection prototypes while keeping authorized edits");
    await login("viewer");
    const forbidden = await page.goto(edit);
    await check(forbidden !== null && forbidden.status() === 403 && await page.locator('input[name="assets-0-code"]').count() === 0, "view-only permission cannot enter the collection mutation form");
    await login("editor");
    await check(same(await state(page), current), "permission-only navigation leaves the complete stored cohort unchanged");
    return {passed: cases.length, cases, browser_version: page.context().browser().version()};
}
