// Run on a fresh choices-mode fixture via playwright-cli run-code --filename.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (ok, name) => { if (!ok) throw new Error(name); cases.push(name); };
    const large = "9007199254740993";
    const foreign = "9007199254740994";
    const edit = origin + "/admin/tickets/change/?id=1";
    const category = origin + "/admin/categories/change/?id=1";
    const input = (name, target = page) => target.locator(`[name="${name}"]`);
    const field = (name, target = page) => target.locator(`[data-field-name="${name}"][data-choice-url]`);
    const selected = (name, target = page) => input(name, target).evaluate(element => Array.from(element.selectedOptions, option => option.value).sort());
    const same = (left, right) => JSON.stringify(left) === JSON.stringify([...right].sort());
    const ready = (name) => page.waitForFunction(name => {
        const element = document.querySelector(`[data-field-name="${name}"][data-choice-url]`);
        return element && !element.hasAttribute("aria-busy") && element.querySelector("[data-choice-status]").textContent.startsWith("Page ");
    }, name);
    const search = async (name, term) => {
        const container = field(name);
        const path = new URL(await container.getAttribute("data-choice-url"), origin).pathname;
        const received = page.waitForResponse(response => new URL(response.url()).pathname === path && new URL(response.url()).searchParams.get("q") === term && response.request().method() === "GET");
        await container.locator("[data-choice-query]").fill(term);
        await container.locator("[data-choice-search]").click();
        const response = await received;
        if (response.status() !== 200) throw new Error("choice search failed: " + response.status());
        const payload = await response.json();
        await ready(name);
        return payload;
    };
    const login = async (role) => {
        await page.goto(origin + "/admin/");
        const logout = page.getByRole("button", {name: "Log out", exact: true});
        if (await logout.count()) await logout.click();
        await page.goto(origin + "/admin/login/");
        await page.getByRole("textbox", {name: "Username", exact: true}).fill(role);
        await page.getByRole("textbox", {name: "Password", exact: true}).fill("demo-password");
        await page.getByRole("button", {name: "Sign in", exact: true}).click();
    };
    const savedState = async () => {
        const fresh = await page.context().newPage();
        try {
            const response = await fresh.goto(edit);
            if (!response || response.status() !== 200) throw new Error("authorized stored read failed");
            return {subject: await input("subject", fresh).inputValue(), labels: await selected("labels", fresh)};
        } finally { await fresh.close(); }
    };
    await login("editor");
    await page.goto(edit);
    await ready("labels");
    check(same(await selected("labels"), [large]), "selected int64 key outside the first page remains selected");
    check(await input("labels").locator("option[data-choice-page]").count() === 25 && await input("labels").locator("option").count() === 26 && await field("labels").locator("[data-choice-next]").isEnabled(), "initial choices are bounded to one page plus selection");
    const plainContext = await page.context().browser().newContext({javaScriptEnabled: false});
    try {
        await plainContext.addCookies(await page.context().cookies());
        const plain = await plainContext.newPage();
        const response = await plain.goto(edit);
        check(response !== null && response.status() === 200 && same(await selected("labels", plain), [large]) && await field("labels", plain).locator("[data-choice-controls]").isHidden() && await input("labels", plain).locator("option[data-choice-page]").count() === 25, "no JavaScript retains selection and the initial native choices");
    } finally { await plainContext.close(); }
    await field("labels").locator("[data-choice-next]").click();
    await page.waitForFunction(() => document.querySelector('[data-field-name="labels"] [data-choice-status]').textContent.startsWith("Page 2:"));
    check(await input("labels").locator("option[data-choice-page]").count() === 10 && same(await selected("labels"), [large]) && await field("labels").locator("[data-choice-next]").isDisabled(), "next page replaces prior results while retaining selected keys");
    await field("labels").locator("[data-choice-previous]").click();
    await page.waitForFunction(() => document.querySelector('[data-field-name="labels"] [data-choice-status]').textContent.startsWith("Page 1:"));
    check(await input("labels").locator("option[data-choice-page]").count() === 25 && await field("labels").locator("[data-choice-previous]").isDisabled(), "previous page restores a bounded first page");
    const unsafe = await search("labels", "<img");
    check(unsafe.items.length === 1 && unsafe.items[0].value === "32" && await input("labels").locator('option[value="32"]').textContent() === 'Choice <img src=x onerror="window.choiceInjected=true">' && await field("labels").locator("img").count() === 0 && await page.evaluate(() => window.choiceInjected !== true), "search labels remain escaped text in JSON and native options");
    const zero = await search("labels", "zero");
    await input("labels").selectOption([large, "0"]);
    const negative = await search("labels", "negative");
    await input("labels").selectOption([large, "0", "-7"]);
    check(zero.items[0].value === "0" && negative.items[0].value === "-7" && same(await selected("labels"), [large, "0", "-7"]), "zero negative and large keys remain exact strings across searches");
    const latest = await search("labels", "Choice 30");
    check(latest.items.length === 1 && latest.items[0].value === "30" && same(await selected("labels"), [large, "0", "-7"]) && await input("labels").locator("option").count() === 4, "multiple selections survive replacement of the search page");

    let releaseSlow;
    let slowPayload;
    let slowStarted;
    const started = new Promise(resolve => { slowStarted = resolve; });
    const held = new Promise(resolve => { releaseSlow = resolve; });
    const racePattern = "**/tickets/change/choices/labels/**";
    const delayed = async route => {
        const url = new URL(route.request().url());
        if (url.searchParams.get("q") !== "Choice 0") return route.continue();
        const response = await route.fetch();
        slowPayload = await response.json();
        slowStarted();
        await held;
        try { await route.fulfill({response}); } catch (error) { /* Browser abort can close this delayed route. */ }
    };
    await page.route(racePattern, delayed);
    try {
        await field("labels").locator("[data-choice-query]").fill("Choice 0");
        await field("labels").locator("[data-choice-search]").click();
        await started;
        await search("labels", "Choice 30");
        releaseSlow();
        await page.waitForTimeout(350);
        check(slowPayload.items.length === 9 && await input("labels").locator("option[data-choice-page]").count() === 1 && await input("labels").locator('option[value="30"][data-choice-page]').count() === 1 && same(await selected("labels"), [large, "0", "-7"]), "a delayed superseded response cannot overwrite newer results");
    } finally { releaseSlow(); await page.unroute(racePattern, delayed); }
    const failing = route => route.fulfill({status: 503, contentType: "application/json", body: "{}"});
    await page.route(racePattern, failing);
    try {
        await field("labels").locator("[data-choice-search]").click();
        await page.waitForFunction(() => document.querySelector('[data-field-name="labels"] [data-choice-status]').textContent.startsWith("Could not load choices."));
        check(same(await selected("labels"), [large, "0", "-7"]) && await field("labels").locator("[data-choice-search]").isEnabled(), "search failure reports an error without losing the selection");
    } finally { await page.unroute(racePattern, failing); }
    await search("labels", "Choice 30");
    check(await field("labels").getAttribute("aria-busy") === null, "a successful retry clears the loading and error state");
    const entered = page.waitForResponse(response => new URL(response.url()).pathname === "/admin/tickets/change/choices/labels/" && new URL(response.url()).searchParams.get("q") === "zero");
    await field("labels").locator("[data-choice-query]").fill("zero");
    await field("labels").locator("[data-choice-query]").press("Enter");
    const enteredResponse = await entered;
    await ready("labels");
    check(enteredResponse.status() === 200 && page.url() === edit && await input("labels").locator('option[value="0"][data-choice-page]').count() === 1, "keyboard Enter searches without submitting the parent form");

    // Exercise single selection on the existing through record before the M2M
    // replacement flow removes and recreates that record.
    const link = origin + "/admin/ticket-labels/change/?id=1";
    await page.goto(link);
    await ready("label");
    check(await input("label").inputValue() === large && await input("label").locator('option[value="' + large + '"][selected]').count() === 1, "single choice retains an exact selected key outside its first page");
    await search("label", "zero");
    await input("label").selectOption("0");
    await page.getByRole("button", {name: "Save", exact: true}).click();
    await page.goto(link);
    check(await input("label").inputValue() === "0", "single zero key survives native form submission and stored reload");
    await search("label", "selected outside");
    await input("label").selectOption(large);
    await page.getByRole("button", {name: "Save", exact: true}).click();
    await page.goto(link);
    check(await input("label").inputValue() === large, "single large key survives native form submission and stored reload");

    await page.goto(edit);
    await ready("labels");
    await field("labels").locator("[data-choice-clear]").click();
    check(same(await selected("labels"), []), "clear selection removes all multiple selected values");
    await page.getByRole("button", {name: "Save", exact: true}).click();
    check(new URL(page.url()).pathname === "/admin/tickets/" && same((await savedState()).labels, []), "cleared membership is persisted and read back from SQLite");
    await page.goto(edit);
    await search("labels", "selected outside");
    await input("labels").selectOption(large);
    await search("labels", "negative");
    await input("labels").selectOption([large, "-7"]);
    await page.getByRole("button", {name: "Save", exact: true}).click();
    check(same((await savedState()).labels, [large, "-7"]), "searched multiple selection persists exact negative and large keys");
    await page.goto(edit);
    await input("subject").fill("Rejected foreign overwrite");
    await input("labels").evaluate((element, value) => {
        const option = document.createElement("option"); option.value = value; option.textContent = "Forged target"; option.selected = true; element.append(option);
    }, foreign);
    await page.getByRole("button", {name: "Save", exact: true}).click();
    check(await page.locator('[data-error-field="labels"][data-error-code="invalid_choice"]').count() === 1 && !await field("labels").textContent().then(text => text.includes("Choice foreign")), "a forged target outside the source scope is rejected without its label");
    const afterForeign = await savedState();
    check(afterForeign.subject === "Choice owner" && same(afterForeign.labels, [large, "-7"]), "invalid membership leaves current scalar and relation rows unchanged");

    await page.goto(edit);
    await search("labels", "stale");
    await input("labels").selectOption([large, "-7", "31"]);
    await input("subject").fill("Rejected stale overwrite");
    const mutator = await page.context().newPage();
    try {
        await mutator.goto(origin + "/admin/labels/change/?id=31");
        await input("name", mutator).fill("Revoked target");
        await mutator.getByRole("button", {name: "Save", exact: true}).click();
        if (new URL(mutator.url()).pathname !== "/admin/labels/") throw new Error("target catalog update failed");
    } finally { await mutator.close(); }
    await page.getByRole("button", {name: "Save", exact: true}).click();
    check(await page.locator('[data-error-field="labels"][data-error-code="invalid_choice"]').count() === 1 && !await field("labels").textContent().then(text => text.includes("Choice stale")), "a target revoked after search is rejected without retaining its old label");
    const afterStale = await savedState();
    check(afterStale.subject === "Choice owner" && same(afterStale.labels, [large, "-7"]), "stale target rejection preserves the current owner and membership");

    const fixed = origin + "/admin/tickets/change/choices/labels/?id=1";
    const badBound = await page.context().request.get(fixed + "&limit=101");
    const selector = await page.context().request.get(fixed + "&field=category");
    const longTerm = await page.context().request.get(fixed + "&q=" + "a".repeat(257));
    check(badBound.status() === 400 && selector.status() === 400 && longTerm.status() === 400, "server rejects oversized pages terms and caller-selected fields");
    await page.goto(category);
    await ready("tickets-0-labels");
    const originalID = await input("tickets-0-id").inputValue();
    const crossRow = await page.context().request.get(origin + "/admin/categories/change/inline/tickets/choices/labels/?id=1&index=0&row_id=2");
    check(originalID === "1" && crossRow.status() === 404, "inline search rejects a stored child outside the current parent cohort");
    const requests = [];
    const observed = request => {
        const url = new URL(request.url());
        if (url.pathname === "/admin/categories/change/inline/tickets/choices/labels/") requests.push(url.searchParams);
    };
    page.on("request", observed);
    try {
        const add = page.locator('[data-inline-prefix="tickets"] > [data-inline-add]');
        await add.click();
        await input("tickets-1-subject").fill("Discard this row");
        await ready("tickets-1-labels");
        await add.click();
        await input("tickets-2-subject").fill("Browser inline");
        await ready("tickets-2-labels");
        await search("tickets-2-labels", "zero");
        await input("tickets-2-labels").selectOption("0");
        check(await input("tickets-2-id").inputValue() === "" && requests.some(params => params.get("index") === "2" && !params.has("row_id")), "new inline search uses the live row index without a forged stored identity");
        await page.locator('[data-inline-prefix="tickets"]').getByRole("button", {name: "Remove item 2", exact: true}).click();
        await ready("tickets-1-labels");
        check(await input("tickets-1-subject").inputValue() === "Browser inline" && same(await selected("tickets-1-labels"), ["0"]) && await input("tickets-0-id").inputValue() === originalID && requests.some(params => params.get("index") === "1" && params.get("q") === "zero" && !params.has("row_id")), "renumbered inline search keeps selection and refreshes its current row scope");
        check(await field("tickets-1-labels").locator("[data-choice-query]").getAttribute("aria-controls") === "tickets-1-labels-choices" && await input("tickets-1-labels").getAttribute("aria-describedby") === "tickets-1-labels-choice-status", "inline renumbering preserves accessible control and status connections");
        await page.getByRole("button", {name: "Save", exact: true}).click();
        check(new URL(page.url()).pathname === "/admin/categories/", "typed inline writer accepts the searched relation in its current transaction");
        await page.goto(category);
        check(await input("tickets-INITIAL_FORMS").inputValue() === "2" && await input("tickets-1-id").inputValue() === "3" && await input("tickets-1-subject").inputValue() === "Browser inline" && same(await selected("tickets-1-labels"), ["0"]) && same(await selected("tickets-0-labels"), [large, "-7"]), "fresh inline read confirms new child keys and both persisted memberships");
    } finally { page.off("request", observed); }

    await login("readonly");
    await page.goto(category);
    check(await input("tickets-0-labels").isDisabled() && await field("tickets-0-labels").locator("[data-choice-controls]").isHidden() && await input("tickets-0-id").inputValue() === originalID, "read-only inline selection has no active search and retains its server identity");
    const readOnlyResponse = await page.context().request.get(origin + "/admin/categories/change/inline/tickets/choices/labels/?id=1&index=0&row_id=1");
    check(readOnlyResponse.status() === 403 && await page.evaluate(() => !new FormData(document.querySelector('form[action^="/admin/categories/"]')).has("tickets-0-labels")), "read-only inline search is denied and disabled selections are not submitted");
    await login("denied");
    await page.goto(category);
    check(await input("tickets-0-labels").isDisabled() && await input("tickets-0-labels").evaluate(element => Array.from(element.selectedOptions).every(option => option.textContent === option.value)) && await field("tickets-0-labels").getAttribute("data-choice-url") === "", "a read-only row without target permission displays only stored identifiers");
    const deniedSearch = await page.context().request.get(fixed);
    const token = await page.locator('input[name="csrfmiddlewaretoken"]').first().inputValue();
    const deniedWrite = await page.context().request.post(edit, {form: {csrfmiddlewaretoken: token, subject: "Denied overwrite", labels: large}});
    check(deniedSearch.status() === 403 && deniedWrite.status() === 403, "missing target permission denies both search and form mutation");
    await login("editor");
    const afterDenied = await savedState();
    check(afterDenied.subject === "Choice owner" && same(afterDenied.labels, [large, "-7"]), "permission denial leaves current stored scalar and membership unchanged");
    await page.goto(category);
    return {passed: cases.length, cases, browser_version: page.context().browser().version()};
}
