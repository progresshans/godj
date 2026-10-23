// Fixture credentials belong only to the disposable loopback application.
async (page) => {
    const origin = new URL(page.url()).origin;
    if (!origin.startsWith("http://127.0.0.1:")) throw new Error("expected isolated loopback fixture");
    await page.getByLabel("Username", {exact: true}).fill("operator");
    await page.getByLabel("Password", {exact: true}).fill("demo-password");
    await page.getByRole("button", {name: "Sign in", exact: true}).click();
    const response = await page.request.get(origin + "/api/tickets/");
    if (response.status() !== 200) throw new Error("fixture login did not establish authentication");
    return {authenticated: true, browser_version: page.context().browser().version()};
}
