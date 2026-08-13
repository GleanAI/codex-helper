import { expect, test, type Page } from "@playwright/test";

const responses: Record<string, unknown> = {
  "system/status": {
    initialized: true,
    appServer: true,
    version: "0.3.0-beta.1",
  },
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
  accounts: [
    {
      id: 1,
      displayName: "默认账号",
      email: "test@example.com",
      planType: "plus",
      expectedKind: "personal",
      actualKind: "personal",
      validationStatus: "matched",
      possibleDuplicate: false,
      connected: true,
    },
  ],
  "settings/telegram": {
    token: "",
    configured: true,
    enabled: true,
    menuEnabled: true,
  },
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
  await expect(
    page.locator("#settings-panel-smtp h2", { hasText: "SMTP 邮件" }),
  ).toBeAttached();
  await expect(page.locator(".settings-loading")).toHaveCount(0);
}

test("shows the build version in the authenticated brand", async ({ page }) => {
  await openSettings(page);
  const brand = page.locator("aside .logo");
  const badge = brand.locator(".version-badge");
  await expect(badge).toBeVisible();
  await expect(badge).toHaveText("v0.3.0-beta.1");
  await expect(brand).toHaveCSS("white-space", "nowrap");
  const layout = await brand.evaluate((element) => {
    const badgeElement = element.querySelector(".version-badge");
    const nameElement = element.querySelector(".brand-name");
    const iconElement = element.querySelector("svg");
    if (!badgeElement || !nameElement || !iconElement)
      throw new Error("brand is incomplete");
    const badgeRect = badgeElement.getBoundingClientRect();
    const nameRect = nameElement.getBoundingClientRect();
    const iconRect = iconElement.getBoundingClientRect();
    return {
      badgeCenter: badgeRect.top + badgeRect.height / 2,
      nameCenter: nameRect.top + nameRect.height / 2,
      iconCenter: iconRect.top + iconRect.height / 2,
    };
  });
  expect(Math.abs(layout.badgeCenter - layout.nameCenter)).toBeLessThan(1);
  expect(Math.abs(layout.iconCenter - layout.nameCenter)).toBeLessThan(1);
});

test("shows the build version before setup", async ({ page }) => {
  await page.route("**/api/v1/system/status", (route) =>
    route.fulfill({
      json: { initialized: false, appServer: false, version: "test" },
    }),
  );
  await page.goto("/");
  const badge = page.locator(
    (page.viewportSize()?.width ?? 0) <= 800
      ? ".setup-mobile-brand .version-badge"
      : ".hero .version-badge",
  );
  await expect(badge).toBeVisible();
  await expect(badge).toHaveText("vtest");
});

test("shows the build version on login", async ({ page }) => {
  await page.route("**/api/v1/system/status", (route) =>
    route.fulfill({
      json: { initialized: true, appServer: false, version: "test" },
    }),
  );
  await page.route("**/api/v1/auth/me", (route) =>
    route.fulfill({ status: 401, json: { error: "unauthorized" } }),
  );
  await page.goto("/");
  const badge = page.locator(".login .version-badge");
  await expect(badge).toBeVisible();
  await expect(badge).toHaveText("vtest");
  const github = page.getByRole("link", {
    name: "在 GitHub 上查看 Codex Helper",
  });
  await expect(github).toHaveAttribute(
    "href",
    "https://github.com/zhoujun0601/codex-helper",
  );
  await expect(github).toHaveAttribute("target", "_blank");
});

test("shows the GitHub link after login", async ({ page }) => {
  await openSettings(page);
  const github = page.getByRole("link", {
    name: "在 GitHub 上查看 Codex Helper",
  });
  await expect(github).toBeVisible();
  await expect(github).toHaveAttribute(
    "href",
    "https://github.com/zhoujun0601/codex-helper",
  );
});

test("saves disabled Telegram switches and removes the configuration", async ({
  page,
}) => {
  let savedBody: Record<string, unknown> | undefined;
  let deleted = false;
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const key = new URL(request.url()).pathname.replace("/api/v1/", "");
    if (key === "settings/telegram" && request.method() === "PUT") {
      savedBody = request.postDataJSON() as Record<string, unknown>;
      return route.fulfill({
        json: { ...responses["settings/telegram"], ...savedBody },
      });
    }
    if (key === "settings/telegram" && request.method() === "DELETE") {
      deleted = true;
      return route.fulfill({ json: { ok: true } });
    }
    return route.fulfill({ json: responses[key] ?? {} });
  });
  page.on("dialog", (dialog) => dialog.accept());
  await page.goto("/settings");
  await page.getByRole("tab", { name: "Telegram" }).click();
  await page.getByLabel("启用提醒").uncheck();
  await page.getByLabel("启用查询菜单").uncheck();
  await page.getByRole("button", { name: "验证并保存" }).click();
  await expect.poll(() => savedBody?.enabled).toBe(false);
  expect(savedBody?.menuEnabled).toBe(false);

  await page.getByRole("button", { name: "解除绑定" }).click();
  await expect.poll(() => deleted).toBe(true);
  await expect(page.getByRole("button", { name: "解除绑定" })).toBeDisabled();
  await expect(page.getByText("Telegram Bot 配置已删除")).toBeVisible();
});

async function layout(page: Page) {
  return page.evaluate(() => {
    const box = (selector: string) => {
      const rect = document.querySelector(selector)!.getBoundingClientRect();
      return { x: rect.x, y: rect.y, width: rect.width, height: rect.height };
    };
    return {
      content: box(".settings-content"),
      tabs: box(".tabs"),
      heading: box("header h1"),
      scrollY: window.scrollY,
    };
  });
}

test("switching every settings panel keeps the layout stable", async ({
  page,
}) => {
  await openSettings(page);
  const initial = await layout(page);
  for (const name of ["Codex", "Telegram", "SMTP 邮件", "通用"]) {
    await page.getByRole("tab", { name }).click();
    await expect.poll(() => layout(page)).toEqual(initial);
  }
});

test("switching panels preserves form state and isolates hidden controls", async ({
  page,
}) => {
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

test("arrow, Home, and End keys move focus and activate tabs", async ({
  page,
}) => {
  await openSettings(page);
  const general = page.getByRole("tab", { name: "通用" });
  await general.focus();

  await page.keyboard.press("ArrowRight");
  await expect(page.getByRole("tab", { name: "Codex" })).toBeFocused();
  await expect(page.getByRole("tab", { name: "Codex" })).toHaveAttribute(
    "aria-selected",
    "true",
  );

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

test("programmatic tab changes do not clamp an existing scroll position", async ({
  page,
}) => {
  await openSettings(page);
  await page.evaluate(() => window.scrollTo(0, 240));
  const before = await layout(page);
  await page.evaluate(() =>
    document
      .querySelector<HTMLButtonElement>("#settings-tab-telegram")!
      .click(),
  );
  await expect
    .poll(async () => (await layout(page)).scrollY)
    .toBe(before.scrollY);
  await expect
    .poll(async () => (await layout(page)).content)
    .toEqual(before.content);
});

test("deleting a newly added account clears its device authorization", async ({
  page,
}) => {
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
  await expect(
    page.getByRole("link", { name: "https://auth.openai.com/codex/device" }),
  ).toBeVisible();

  await page.getByRole("button", { name: "删除“账号 1”" }).click();
  await expect(page.getByText("PLTJ-7M6I6")).toHaveCount(0);
  await expect(
    page.getByRole("link", { name: "https://auth.openai.com/codex/device" }),
  ).toHaveCount(0);
  await expect(
    page.getByText("尚未添加 Codex 账号", { exact: false }),
  ).toBeVisible();
});

test("Codex account cards fit within the viewport", async ({ page }) => {
  await openSettings(page);
  await page.getByRole("tab", { name: "Codex" }).click();
  const overflow = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: document.documentElement.clientWidth,
    cardWidth: document.querySelector(".account-card")!.getBoundingClientRect()
      .width,
    contentWidth: document
      .querySelector(".settings-content")!
      .getBoundingClientRect().width,
  }));
  expect(overflow.documentWidth).toBeLessThanOrEqual(overflow.viewportWidth);
  expect(overflow.cardWidth).toBeLessThanOrEqual(overflow.contentWidth);
});

test("Codex account emails are masked everywhere they are displayed", async ({
  page,
}) => {
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "dashboard")
      return route.fulfill({
        json: {
          accountId: 1,
          displayName: "默认账号",
          account: {
            email: "test@example.com",
            planType: "plus",
            connected: true,
          },
          limits: [
            {
              limitId: "primary",
              limitName: null,
              windowType: "5h",
              usedPercent: 25,
              windowDurationMinutes: 300,
              resetsAt: 0,
            },
          ],
          summary: {},
          usage: [],
          fetchedAt: 0,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/");
  await expect(page.locator(".account-select")).toContainText(
    "t**t@e*****e.com",
  );
  await expect(page.locator(".account b")).toContainText("t**t@e*****e.com");
  await expect(page.getByText("已使用 25%", { exact: true })).toHaveCount(0);
  await expect(page.getByText("剩余 75%", { exact: true })).toHaveCount(0);
  await expect(page.locator(".bar")).toHaveAttribute(
    "aria-label",
    "5h：已使用 25%，剩余 75%",
  );
  await expect(page.locator("body")).not.toContainText("test@example.com");

  await page.goto("/settings");
  await page.getByRole("tab", { name: "Codex" }).click();
  await expect(page.locator(".account-meta").first()).toContainText(
    "t**t@e*****e.com",
  );
  await expect(page.locator("body")).not.toContainText("test@example.com");
});

test("returns to login when an authenticated request receives 401", async ({
  page,
}) => {
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "accounts/1/sync")
      return route.fulfill({ status: 401, json: { error: "未登录" } });
    if (key === "dashboard")
      return route.fulfill({
        json: {
          accountId: 1,
          displayName: "默认账号",
          account: { email: null, planType: null, connected: false },
          limits: [],
          summary: {},
          usage: [],
          fetchedAt: 0,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });
  await page.goto("/");
  await expect(page.locator(".account-select")).toBeVisible();
  await page.getByRole("button", { name: "立即刷新" }).click();
  await expect(page.getByRole("heading", { name: "欢迎回来" })).toBeVisible();
});

test("a slow previous account response cannot replace the selected account", async ({
  page,
}) => {
  const accounts = [
    responses.accounts[0],
    {
      ...responses.accounts[0],
      id: 2,
      displayName: "第二账号",
      email: "second@example.com",
    },
  ];
  await page.route("**/api/v1/**", async (route) => {
    const requestUrl = new URL(route.request().url());
    const key = requestUrl.pathname.replace("/api/v1/", "");
    if (key === "accounts") return route.fulfill({ json: accounts });
    if (key === "dashboard") {
      const accountId = Number(requestUrl.searchParams.get("accountId"));
      if (accountId === 1)
        await new Promise((resolve) => setTimeout(resolve, 250));
      return route.fulfill({
        json: {
          accountId,
          displayName: accountId === 1 ? "默认账号" : "第二账号",
          account: {
            email: accountId === 1 ? "test@example.com" : "second@example.com",
            planType: "plus",
            connected: true,
          },
          limits: [],
          summary: {},
          usage: [],
          fetchedAt: 0,
          stale: false,
        },
      });
    }
    return route.fulfill({ json: responses[key] ?? {} });
  });
  await page.goto("/");
  const selector = page.locator(".account-select");
  await selector.selectOption("2");
  await expect(page.locator(".account b")).toContainText("第二账号");
  await page.waitForTimeout(350);
  await expect(page.locator(".account b")).toContainText("第二账号");
  await expect(page.locator(".account b")).not.toContainText("默认账号");
});
