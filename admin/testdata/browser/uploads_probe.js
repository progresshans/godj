// Run on a fresh fixture via playwright-cli run-code --filename.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    const cases = [];
    const check = (ok, name) => { if (!ok) throw new Error(name); cases.push(name); };
    const input = (name) => page.locator(`[name="${name}"]`);
    const edit = origin + "/admin/categories/change/?id=1";
    await page.goto(origin + "/admin/login/");
    await page.getByRole("textbox", {name:"Username",exact:true}).fill("editor");
    await page.getByRole("textbox", {name:"Password",exact:true}).fill("demo-password");
    await page.getByRole("button", {name:"Sign in",exact:true}).click();
    await page.goto(edit);
    check(await page.locator('form[action^="/admin/categories/"]').getAttribute("enctype") === "multipart/form-data", "inline-only file sets parent multipart encoding");
    check(await input("labels-TOTAL_FORMS").inputValue() === "1", "zero extra rows retain file prototype");
    const originalParent = await input("name").inputValue();
    const original = await input("labels-0-name").inputValue();
    const originalLabelID = await input("labels-0-id").inputValue();
    const originalTicket = {
        id: await input("tickets-0-id").inputValue(),
        subject: await input("tickets-0-subject").inputValue(),
        closed: await input("tickets-0-closed").isChecked(),
    };
    const add = page.locator('[data-inline-prefix="labels"] > [data-inline-add]');
    await add.click();
    await input("labels-1-name").fill("Remove this row");
    await add.click();
    await input("labels-2-name").fill("Verified upload");
    await input("labels-2-document").setInputFiles({name:"verification.txt", mimeType:"text/plain", buffer:Buffer.from("wrong content")});
    await page.locator('[data-inline-prefix="labels"]').getByRole("button",{name:"Remove item 2",exact:true}).click();
    check(await input("labels-1-name").inputValue() === "Verified upload", "file row renumbering preserves text");
    check(await input("labels-1-document").evaluate(el => el.files.length === 1 && el.files[0].name === "verification.txt"), "file selection survives another row removal");
    check(await input("labels-1-document").evaluate(async el => await el.files[0].text()) === "wrong content", "file payload survives renumbering");
    check(await input("labels-0-name").inputValue() === original, "saved sibling remains unchanged");
    await input("tickets-1-subject").fill("Second ticket for upload");
    // The writer saves this changed parent before it validates the label file.
    const rejectedParentInput = "  Pending parent rename  ";
    await input("name").fill(rejectedParentInput);
    await page.getByRole("button",{name:"Save",exact:true}).click();
    check(await page.locator('[data-error-field="labels-1-document"][data-error-code="file_mismatch"]').count() === 1, "transactional consumer reads uploaded bytes and rejects mismatch");
    check(await page.locator('[data-file-reselect="labels-1-document"]').count() === 1, "rejected upload requests reselection");
    check(await input("labels-1-document").evaluate(el => el.files.length) === 0, "redisplay does not pretend browser retained file");
    check(await input("labels-1-name").inputValue() === "Verified upload" && await input("labels-INITIAL_FORMS").inputValue() === "1", "rollback redisplay preserves text and stored identity count");
    check(await input("name").inputValue() === rejectedParentInput, "rejected upload retains the original raw parent input");
    const stored = await page.context().newPage();
    try {
        const response = await stored.goto(edit);
        const storedInput = (name) => stored.locator(`[name="${name}"]`);
        check(response !== null && response.status() === 200 && stored.url() === edit && await storedInput("name").inputValue() === originalParent, "fresh authorized read confirms parent write rollback");
        check(await storedInput("labels-INITIAL_FORMS").inputValue() === "1" && await storedInput("tickets-INITIAL_FORMS").inputValue() === "1"
            && await storedInput("labels-0-id").inputValue() === originalLabelID && await storedInput("labels-0-name").inputValue() === original
            && await storedInput("tickets-0-id").inputValue() === originalTicket.id && await storedInput("tickets-0-subject").inputValue() === originalTicket.subject
            && await storedInput("tickets-0-closed").isChecked() === originalTicket.closed, "rejected upload leaves SQLite cohort unchanged");
    } finally {
        await stored.close();
    }
    await input("name").fill(originalParent);
    await input("labels-1-document").setInputFiles({name:"verification.txt",mimeType:"text/plain",buffer:Buffer.from("Verified upload")});
    await page.getByRole("button",{name:"Save",exact:true}).click();
    check(new URL(page.url()).pathname === "/admin/categories/", "reselected valid file completes composite save");
    await page.goto(edit);
    check(await input("name").inputValue() === originalParent, "successful upload preserves the original parent value");
    check(await input("labels-INITIAL_FORMS").inputValue() === "2" && await input("labels-1-name").inputValue() === "Verified upload", "SQLite retains approved label after file validation");
    check(await input("labels-0-name").inputValue() === original, "file validation preserves saved sibling");
    await page.getByRole("button",{name:"Log out",exact:true}).click();
    await page.getByRole("textbox",{name:"Username",exact:true}).fill("readonly");
    await page.getByRole("textbox",{name:"Password",exact:true}).fill("demo-password");
    await page.getByRole("button",{name:"Sign in",exact:true}).click();
    await page.goto(edit);
    check(await input("labels-0-document").isDisabled(), "read-only child file widget is disabled");
    check(await page.locator('form[action^="/admin/categories/"]').getAttribute("enctype") === null, "read-only child uploads do not change parent transport");
    check(await page.evaluate(() => !Array.from(new FormData(document.querySelector('form[action^="/admin/categories/"]')).keys()).includes("labels-0-document")), "read-only upload is not a successful control");
    return {passed:cases.length,cases,browser_version:page.context().browser().version()};
}
