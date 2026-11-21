const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");

const [tools, origin, variant, phase, mailbox] = process.argv.slice(2);
const modules = path.join(tools, "node_modules");
const { chromium } = require(path.join(modules, "playwright"));
const browsers = JSON.parse(fs.readFileSync(path.join(modules, "playwright-core", "browsers.json"), "utf8"));
const expectedVersion = browsers.browsers.find(item => item.name === "chromium").browserVersion;
const operator = "operator@example.test";
const reader = "reader@example.test";
const readerPassword = "Sunlit-Cliff-812!private";
const changedPassword = "Willow-Grove-829!private";
const resetPassword = "Linen-Forest-437!private";
const custom = variant === "custom";
const loginField = custom ? "contact" : "username";
const profile = custom ? "region" : "first_name";
const active = custom ? "enabled" : "active";
const groupName = custom ? "title" : "name";
const groupGrants = custom ? "grants" : "permissions";
const grantCode = custom ? "key" : "code";
const grantName = custom ? "title" : "name";
const schemaGrant = custom ? "people.read_schema" : "godj_identity.view_user";
const noteGrant = "people.read_note";
const base = "/api/identity/";
let stage = "arguments";
let unexpectedStatus = "";
let launchDiagnostic = "";

async function api(page, method, route, expected, data, revision) {
    const headers = {Accept: "application/json"};
    if (method !== "GET") {
        const csrf = await page.request.get(origin + "/api/account/csrf/");
        assert.equal(csrf.status(), 204);
        const token = csrf.headers()["x-godj-csrftoken"];
        assert.ok(token);
        headers["X-GoDj-CSRFToken"] = token;
    }
    if (revision !== undefined) headers["If-Revision"] = String(revision);
    const response = await page.request.fetch(origin + route, {method, headers, data, maxRedirects: 0});
    if (response.status() !== expected) {
        unexpectedStatus = method + " " + route + " expected " + expected + " received " + response.status();
        throw new Error("unexpected HTTP status");
    }
    return response;
}

async function login(browser, name, password, accepted = true) {
    const page = await browser.newPage();
    await page.goto(origin + "/admin/login/");
    await page.getByLabel(custom ? "Contact" : "Username", {exact: true}).fill(name);
    await page.getByLabel("Password", {exact: true}).fill(password);
    await page.getByRole("button", {name: "Sign in"}).click();
    if (accepted) {
        await page.waitForURL(origin + "/admin/");
    } else {
        await page.locator('[data-login-error="invalid_credentials"]').waitFor();
    }
    return page;
}

async function checkColumns(page, slug, fields) {
    const response = await page.goto(origin + "/admin/" + slug + "/");
    assert.equal(response.status(), 200);
    const actual = await page.locator("th[data-list-field]").evaluateAll(nodes => nodes.map(node => node.dataset.listField));
    assert.deepEqual(actual, fields);
}

async function submitProfile(page, form, expectedStatus) {
    const action = new URL(await form.getAttribute("action"), page.url()).href;
    const [response] = await Promise.all([
        page.waitForResponse(result => result.url() === action && result.request().method() === "POST"),
        form.locator(':scope > button[type="submit"]').click(),
    ]);
    if (response.status() !== expectedStatus) {
        unexpectedStatus = "profile form expected " + expectedStatus + " received " + response.status();
        throw new Error("unexpected form status");
    }
}

function mailLink() {
    const message = JSON.parse(fs.readFileSync(path.join(mailbox, "message.json"), "utf8"));
    assert.equal(message.from, "sender@example.test");
    assert.equal(message.to, reader);
    const links = message.text.split("\n").filter(line => line.startsWith("http://"));
    assert.equal(links.length, 1);
    const link = new URL(links[0]);
    assert.equal(link.origin, origin);
    assert.match(link.pathname, /^\/account\/reset\/[A-Za-z0-9_-]+\/[A-Za-z0-9._-]+\/$/);
    return link;
}

function confirmURL(link) {
    return origin + link.pathname.replace(/\/[^/]+\/$/, "/set-password/");
}

async function requestReset(browser, email) {
    const page = await browser.newPage();
    await page.goto(origin + "/account/reset/");
    await page.getByLabel("Email address").fill(email);
    await page.getByRole("button", {name: "Send reset link"}).click();
    await page.waitForURL(origin + "/account/reset/sent/");
    const text = await page.locator("main").innerText();
    await page.close();
    return text;
}

async function prepareReset(browser, member) {
    const unknown = await requestReset(browser, "absent@example.test");
    assert.equal(fs.existsSync(path.join(mailbox, "message.json")), false);
    assert.equal(await requestReset(browser, reader), unknown);
    const link = mailLink();
    const page = await browser.newPage();
    const confirm = await page.goto(link.href);
    assert.equal(page.url(), confirmURL(link));
    assert.equal(confirm.headers()["cache-control"], "no-store");
    assert.equal(confirm.headers()["referrer-policy"], "no-referrer");
    await page.locator('body[data-account-view="reset-confirm"]').waitFor();
    await page.context().storageState({path: path.join(mailbox, "proof.json")});
    await member.context().storageState({path: path.join(mailbox, "member.json")});
    await page.close();
}

async function completeReset(browser) {
    const link = mailLink();
    const proof = await browser.newContext({storageState: path.join(mailbox, "proof.json")});
    const existing = await browser.newContext({storageState: path.join(mailbox, "member.json")});
    const session = await existing.newPage();
    await api(session, "GET", "/api/account/csrf/", 204);
    const page = await proof.newPage();
    await page.goto(confirmURL(link));
    await page.locator('body[data-account-view="reset-confirm"]').waitFor();
    await page.getByLabel("New password", {exact: true}).fill(resetPassword);
    await page.getByLabel("New password confirmation", {exact: true}).fill(resetPassword);
    await page.getByRole("button", {name: "Reset password"}).click();
    await page.waitForURL(origin + "/account/reset/complete/");
    await api(session, "GET", "/api/account/csrf/", 403);
    await page.goto(link.href);
    await page.locator('body[data-account-view="reset-invalid"]').waitFor();
    await Promise.all([proof.close(), existing.close()]);
    const old = await login(browser, reader, changedPassword, false);
    await old.close();
    return login(browser, reader, resetPassword);
}

async function exercise(browser) {
    stage = "anonymous schema";
    const anonymous = await browser.newPage();
    await api(anonymous, "GET", base + "openapi.json", 403);
    await anonymous.close();
    stage = "operator login and selected Admin columns";
    const admin = await login(browser, operator, "Slate-Meadow-946!private");
    await checkColumns(admin, "users", custom ? ["id", "contact", "enabled", "root"] : ["id", "username", "email", "active", "staff", "superuser"]);
    assert.ok(await admin.locator("tbody").getByText(operator, {exact: true}).count());
    await checkColumns(admin, "groups", ["id", groupName]);
    await checkColumns(admin, "permissions", ["id", grantCode, grantName]);
    const accountPath = base + "accounts/";
    if (phase === "restart") {
        stage = "persisted reset proof after restart";
        const member = await completeReset(browser);
        await api(member, "GET", base + "openapi.json", 200);
        stage = "deactivation after reset";
        const listing = await (await api(admin, "GET", accountPath, 200)).json();
        const target = listing.items.find(row => row.fields[loginField] === reader);
        assert.equal(target.fields[profile], "Updated");
        assert.equal(target.fields[active], true);
        const detail = accountPath + target.id + "/";
        const disabled = await (await api(admin, "PATCH", detail, 200, {fields: {[active]: false}}, target.revision)).json();
        await api(member, "GET", "/api/account/csrf/", 403);
        await member.close();
        const denied = await login(browser, reader, resetPassword, false);
        await denied.close();
        stage = "delete with current revision";
        await api(admin, "DELETE", detail, 412, undefined, target.revision);
        await api(admin, "DELETE", detail, 200, undefined, disabled.revision);
        await api(admin, "GET", detail, 404);
        await admin.close();
        return;
    }
    stage = "catalog and typed account creation";
    const grant = await (await api(admin, "POST", base + "permissions/", 201, {fields: {[grantCode]: schemaGrant, [grantName]: "Read schema"}})).json();
    const group = await (await api(admin, "POST", base + "groups/", 201, {fields: {[groupName]: "Schema readers", [groupGrants]: [grant.id]}})).json();
    const fields = {[loginField]: reader, [custom ? "teams" : "groups"]: [group.id]};
    if (!custom) { fields.staff = true; fields.email = reader; }
    if (custom) {
        await api(admin, "POST", accountPath, 400, {fields, credential: {password: readerPassword}});
        const before = await (await api(admin, "GET", accountPath, 200)).json();
        assert.equal(before.count, 1);
    }
    fields[profile] = "Initial";
    const target = await (await api(admin, "POST", accountPath, 201, {fields, credential: {password: readerPassword}})).json();
    const detail = accountPath + target.id + "/";
    stage = "schema grant and model permissions";
    const member = await login(browser, reader, readerPassword);
    const schema = await (await api(member, "GET", base + "openapi.json", 200)).json();
    assert.ok(schema.paths[accountPath]);
    await api(member, "GET", accountPath, custom ? 403 : 200);
    const groupPath = base + "groups/" + group.id + "/";
    const revoked = await (await api(admin, "PATCH", groupPath, 200, {fields: {[groupGrants]: []}}, group.revision)).json();
    await api(member, "GET", base + "openapi.json", 403);
    await api(admin, "PATCH", groupPath, 200, {fields: {[groupGrants]: [grant.id]}}, revoked.revision);
    await api(member, "GET", base + "openapi.json", 200);

    stage = "password change and session revocation";
    const other = await login(browser, reader, readerPassword);
    await member.goto(origin + "/account/password/");
    await member.locator('[name="old_password"]').fill(readerPassword);
    await member.locator('[name="new_password1"]').fill(changedPassword);
    await member.locator('[name="new_password2"]').fill(changedPassword);
    await member.locator('form:has([name="old_password"]) button[type="submit"]').click();
    await member.waitForURL(origin + "/account/password/done/");
    await api(other, "GET", "/api/account/csrf/", 403);
    await other.close();
    const old = await login(browser, reader, readerPassword, false);
    await old.close();
    const current = await login(browser, reader, changedPassword);
    await api(current, "GET", base + "openapi.json", 200);
    stage = "profile revision";
    const before = await (await api(admin, "GET", detail, 200)).json();
    let directGrant;
    if (custom) {
        // A distinct grant keeps schema access dependent on the group.
        directGrant = await (await api(admin, "POST", base + "permissions/", 201, {
            fields: {[grantCode]: noteGrant, [grantName]: "Read note"},
        })).json();
    }
    await admin.goto(origin + "/admin/users/");
    await admin.locator('tr[data-object-id="' + target.id + '"]').getByRole("link", {name: "Change"}).click();
    const modelForm = admin.locator('form:has([name="expected_revision"])');
    await modelForm.locator('[name="' + profile + '"]').fill("Updated");
    if (custom) {
        stage = "profile required direct grant";
        await submitProfile(admin, modelForm, 200);
        await modelForm.locator('[data-error-field="grants"][data-error-code="required"]').waitFor();
        assert.equal(await modelForm.locator("[data-error-code]").count(), 1);
        const unchanged = await (await api(admin, "GET", detail, 200)).json();
        assert.equal(unchanged.revision, before.revision);
        assert.equal(unchanged.fields[profile], "Initial");
        assert.deepEqual(unchanged.fields.teams, [group.id]);
        assert.deepEqual(unchanged.fields.grants, []);
        stage = "search direct grants on the rejected form";
        const chooser = modelForm.locator('[data-field-name="grants"][data-choice-url]');
        const choices = new URL(await chooser.getAttribute("data-choice-url"), origin);
        assert.equal(choices.origin, origin);
        const [page] = await Promise.all([
            admin.waitForResponse(response => {
                const url = new URL(response.url());
                return response.request().method() === "GET" && url.origin === origin && url.pathname === choices.pathname &&
                    url.searchParams.get("id") === String(target.id) && url.searchParams.get("q") === noteGrant;
            }),
            chooser.getByRole("searchbox").fill(noteGrant),
        ]);
        if (page.status() !== 200) {
            unexpectedStatus = "grant search received " + page.status();
            throw new Error("unexpected choice status");
        }
        const results = await page.json();
        assert.deepEqual(results.items.map(item => item.value), [String(directGrant.id)]);
        await chooser.locator('select[name="grants"]').selectOption(String(directGrant.id));
    }
    stage = "save profile and reject stale revision";
    await submitProfile(admin, modelForm, 302);
    await admin.locator('body[data-admin-view="list"]').waitFor();
    await api(admin, "PATCH", detail, 412, {fields: {[profile]: "Stale"}}, before.revision);
    const after = await (await api(admin, "GET", detail, 200)).json();
    assert.equal(after.fields[profile], "Updated");
    assert.ok(after.revision > before.revision);
    if (custom) {
        assert.deepEqual(after.fields.teams, [group.id]);
        assert.deepEqual(after.fields.grants, [directGrant.id]);
    }
    stage = "recipient mail and exchange for a reset proof";
    await prepareReset(browser, current);
    await Promise.all([member.close(), current.close(), admin.close()]);
}

async function main() {
    assert.ok(["custom", "default"].includes(variant) && ["initial", "restart"].includes(phase));
    assert.match(origin, /^http:\/\/127\.0\.0\.1:[1-9][0-9]*$/);
    stage = "Chromium executable";
    const executablePath = chromium.executablePath();
    fs.accessSync(executablePath, fs.constants.X_OK);
    stage = "Chromium launch";
    const browser = await chromium.launch({executablePath, headless: true});
    const version = browser.version();
    try {
        stage = "Chromium version";
        launchDiagnostic = "expected " + expectedVersion + " observed " + version;
        assert.equal(version, expectedVersion);
        launchDiagnostic = "";
        await exercise(browser);
    } finally {
        await browser.close();
    }
    process.stdout.write(JSON.stringify({variant, phase, browser_version: version}) + "\n");
}

main().catch(error => {
    // Keep credentials, cookies, form values and response bodies out of CI logs.
    const line = /browser\.cjs:(\d+):(\d+)/.exec(error.stack || "");
    const location = line ? " browser.cjs:" + line[1] + ":" + line[2] : "";
    if (stage === "Chromium launch") {
        const cause = /(ENAMETOOLONG|SingletonSocket|ProcessSingleton|EACCES|ENOENT|error while loading shared libraries|No usable sandbox)/i.exec(error.message || "");
        launchDiagnostic = cause ? cause[1] : "launcher rejected before the browser connected";
    }
    process.stderr.write(["Starter browser failed at " + stage + location, launchDiagnostic, unexpectedStatus].filter(Boolean).join(" | ") + "\n");
    process.exitCode = 1;
});
