import { expect, test, type Page } from "@playwright/test";

const responses: Record<string, unknown> = {
  "system/status": { initialized: true, appServer: true, version: "test" },
  "auth/me": { username: "admin" },
  "settings/general": {
    timezone: "America/New_York",
    theme: "system",
    syncMinutes: 5,
    retentionDays: 90,
    beforeMinutes: 30,
    notifyBefore: true,
    notifyAfter: true,
  },
  accounts: [{
    id: 1,
    displayName: "默认账号",
    email: "test@example.com",
    planType: "plus",
    expectedKind: "personal",
    actualKind: "personal",
    validationStatus: "matched",
    possibleDuplicate: false,
    connected: true,
  }],
  "settings/telegram": { token: "", configured: true, enabled: true, menuEnabled: true },
  "settings/smtp": {
    host: "smtp.example.com",
    port: 587,
    username: "mailer",
    password: "",
    configured: true,
    security: "starttls",
    fromName: "Codex Helper",
    from: "sender@example.com",
    to: "recipient@example.com",
    enabled: true,
  },
};

async function openSettings(page: Page) {
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    await new Promise((resolve) => setTimeout(resolve, 40));
    await route.fulfill({ json: responses[key] ?? {} });
  });
  await page.goto("/settings");
  await expect(page.locator("#settings-panel-smtp h2", { hasText: "SMTP 邮件" })).toBeAttached();
  await expect(page.locator(".settings-loading")).toHaveCount(0);
}

async function layout(page: Page) {
  return page.evaluate(() => {
    const box = (selector: string) => {
      const rect = document.querySelector(selector)!.getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    };
    return { content: box(".settings-content"), tabs: box(".tabs"), heading: box("header h1"), scrollY: window.scrollY };
  });
}

test("switching every settings panel keeps the layout stable", async ({ page }) => {
  await openSettings(page);
  const initial = await layout(page);
  for (const name of ["Codex", "Telegram", "SMTP 邮件", "通用"]) {
    await page.getByRole("tab", { name }).click();
    await expect.poll(() => layout(page)).toEqual(initial);
  }
});

test("switching panels preserves form state and isolates hidden controls", async ({ page }) => {
  await openSettings(page);
  const timezone = page.getByLabel("时区");
  await timezone.fill("Asia/Shanghai");
  await page.getByRole("tab", { name: "SMTP 邮件" }).click();
  await expect(timezone).toHaveValue("Asia/Shanghai");
  await expect(timezone).not.toBeVisible();
  await timezone.evaluate((element) => element.focus());
  await expect(timezone).not.toBeFocused();
  await page.getByRole("tab", { name: "通用" }).click();
  await expect(timezone).toBeVisible();
  await expect(timezone).toHaveValue("Asia/Shanghai");
});

test("arrow, Home, and End keys move focus and activate tabs", async ({ page }) => {
  await openSettings(page);
  const general = page.getByRole("tab", { name: "通用" });
  await general.focus();

  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Codex" })).toBeFocused();
  await expect(page.getByRole("tab", { name: "Codex" })).toHaveAttribute("aria-selected", "true");

  await page.keyboard.press("End");
  await expect(page.getByRole("tab", { name: "SMTP 邮件" })).toBeFocused();
  await expect(page.getByRole("heading", { name: "SMTP 邮件" })).toBeVisible();

  await page.keyboard.press("ArrowRight");
  await expect(general).toBeFocused();
  await page.keyboard.press("End");
  await page.keyboard.press("Home");
  await expect(general).toBeFocused();
  await expect(general).toHaveAttribute("aria-selected", "true");
});

test("programmatic tab changes do not clamp an existing scroll position", async ({ page }) => {
  await openSettings(page);
  await page.evaluate(() => window.scrollTo(0, 240));
  const before = await layout(page);
  await page.evaluate(() => document.querySelector<HTMLButtonElement>("#settings-tab-telegram")!.click());
  await expect.poll(async () => (await layout(page)).scrollY).toBe(before.scrollY);
  await expect.poll(async () => (await layout(page)).content).toEqual(before.content);
});

test("deleting a newly added account clears its device authorization", async ({ page }) => {
  let accounts: Array<(typeof responses.accounts)[number]> = [];
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const key = new URL(request.url()).pathname.replace("/api/v1/", "");
    if (key === "accounts" && request.method() === "GET")
      return route.fulfill({ json: accounts });
    if (key === "accounts" && request.method() === "POST") {
      const account = {
        ...responses.accounts[0],
        id: 2,
        displayName: "账号 1",
        email: "",
        connected: false,
        validationStatus: "pending" as const,
      };
      accounts = [account];
      return route.fulfill({ json: account });
    }
    if (key === "accounts/2/login/device")
      return route.fulfill({
        json: {
          verificationUrl: "https://auth.openai.com/codex/device",
          userCode: "PLTJ-7M6I6",
        },
      });
    if (key === "accounts/2" && request.method() === "DELETE") {
      accounts = [];
      return route.fulfill({ json: { ok: true } });
    }
    return route.fulfill({ json: responses[key] ?? {} });
  });
  page.on("dialog", (dialog) => dialog.accept());
  await page.goto("/settings");
  await page.getByRole("tab", { name: "Codex" }).click();
  await page.getByRole("button", { name: "添加账号" }).click();
  await expect(page.getByText("PLTJ-7M6I6")).toBeVisible();
  await expect(page.getByRole("link", { name: "https://auth.openai.com/codex/device" })).toBeVisible();

  await page.getByRole("button", { name: "删除“账号 1”" }).click();
  await expect(page.getByText("PLTJ-7M6I6")).toHaveCount(0);
  await expect(page.getByRole("link", { name: "https://auth.openai.com/codex/device" })).toHaveCount(0);
  await expect(page.getByText("尚未添加 Codex 账号", { exact: false })).toBeVisible();
});

test("Codex account cards fit within the viewport", async ({ page }) => {
  await openSettings(page);
  await page.getByRole("tab", { name: "Codex" }).click();
  const overflow = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: document.documentElement.clientWidth,
    cardWidth: document.querySelector(".account-card")!.getBoundingClientRect().width,
    contentWidth: document.querySelector(".settings-content")!.getBoundingClientRect().width,
  }));
  expect(overflow.documentWidth).toBeLessThanOrEqual(overflow.viewportWidth);
  expect(overflow.cardWidth).toBeLessThanOrEqual(overflow.contentWidth);
});
