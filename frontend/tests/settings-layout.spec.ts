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
