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

async function openSettings(page: Page, version = "0.3.0-beta.1") {
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    await new Promise((resolve) => setTimeout(resolve, 40));
    if (key === "system/status") {
      await route.fulfill({
        json: { initialized: true, appServer: true, version },
      });
      return;
    }
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
  const mobile = (page.viewportSize()?.width ?? 0) <= 800;
  const brand = page.locator(mobile ? ".mobile-topbar .logo" : "aside .logo");
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
      iconWidth: iconRect.width,
      iconHeight: iconRect.height,
      iconFlexShrink: getComputedStyle(iconElement).flexShrink,
    };
  });
  expect(Math.abs(layout.badgeCenter - layout.nameCenter)).toBeLessThan(1);
  expect(Math.abs(layout.iconCenter - layout.nameCenter)).toBeLessThan(1);
  expect(layout.iconWidth).toBe(mobile ? 22 : 30);
  expect(layout.iconHeight).toBe(mobile ? 22 : 30);
  expect(layout.iconFlexShrink).toBe("0");
});

test("keeps the authenticated brand on one line with a release version", async ({
  page,
}) => {
  await openSettings(page, "0.2.0");
  const brand = page.locator(
    (page.viewportSize()?.width ?? 0) <= 800
      ? ".mobile-topbar .logo"
      : "aside .logo",
  );
  await expect(brand).toContainText("Codex Helper");
  await expect(brand.locator(".version-badge")).toHaveText("v0.2.0");
  const centers = await brand
    .locator("svg, .brand-name, .version-badge")
    .evaluateAll((elements) =>
      elements.map((element) => {
        const rect = element.getBoundingClientRect();
        return rect.top + rect.height / 2;
      }),
    );
  expect(Math.max(...centers) - Math.min(...centers)).toBeLessThan(1);
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
    "https://github.com/GleanAI/codex-helper",
  );
  await expect(github).toHaveAttribute("target", "_blank");
});

test("shows the GitHub link after login", async ({ page }) => {
  await openSettings(page);
  if ((page.viewportSize()?.width ?? 0) <= 800)
    await page.getByRole("button", { name: "打开更多操作" }).click();
  const github = page.getByRole("link", {
    name: "在 GitHub 上查看 Codex Helper",
  });
  await expect(github).toBeVisible();
  await expect(github).toHaveAttribute(
    "href",
    "https://github.com/GleanAI/codex-helper",
  );
});

test("mobile shell exposes accessible navigation without covering content", async ({
  page,
}) => {
  test.skip((page.viewportSize()?.width ?? 0) > 800, "mobile only");
  await openSettings(page);
  const settings = page.getByRole("button", { name: "设置", exact: true });
  await expect(settings).toHaveAttribute("aria-current", "page");
  const nav = page.locator(".mobile-bottom-nav");
  await page.evaluate(() => window.scrollTo(0, document.body.scrollHeight));
  const geometry = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: document.documentElement.clientWidth,
    navTop: document
      .querySelector(".mobile-bottom-nav")!
      .getBoundingClientRect().top,
    contentBottom: document
      .querySelector("#settings-panel-general .panel")!
      .getBoundingClientRect().bottom,
  }));
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewportWidth);
  expect(geometry.contentBottom).toBeLessThanOrEqual(geometry.navTop);
  await expect(nav).toBeVisible();
  await page.getByRole("button", { name: "总览", exact: true }).click();
  await expect(page).toHaveURL(/\/$/);
});

test("overview merges personal and Team connections for the same email", async ({
  page,
}) => {
  const accounts = [
    responses.accounts[0],
    {
      ...responses.accounts[0],
      id: 2,
      displayName: "Team workspace",
      planType: "business",
      expectedKind: "team",
      actualKind: "team",
    },
    {
      ...responses.accounts[0],
      id: 3,
      displayName: "尚未登录",
      email: null,
      planType: null,
      actualKind: "unknown",
      validationStatus: "pending",
      connected: false,
    },
  ];
  await page.route("**/api/v1/**", async (route) => {
    const requestUrl = new URL(route.request().url());
    const key = requestUrl.pathname.replace("/api/v1/", "");
    if (key === "accounts") return route.fulfill({ json: accounts });
    if (key === "dashboard") {
      const accountId = Number(requestUrl.searchParams.get("accountId"));
      const limitCount = accountId === 1 ? 2 : accountId === 2 ? 1 : 0;
      return route.fulfill({
        json: {
          accountId,
          displayName: accounts[accountId - 1].displayName,
          account: {
            email: accounts[accountId - 1].email,
            planType: accounts[accountId - 1].planType,
            connected: accounts[accountId - 1].connected,
          },
          limits: Array.from({ length: limitCount }, (_, index) => ({
            limitId: `limit-${index}`,
            limitName: index ? "Review" : "Codex",
            windowType: index ? "secondary" : "primary",
            usedPercent: accountId === 1 ? 25 + index * 35 : 40,
            windowDurationMinutes: index ? 43_200 : 10_080,
            resetsAt: Math.floor(Date.now() / 1000) + 604_800,
          })),
          monthlyCreditLimit:
            accountId === 2
              ? {
                  remainingPercent: 100,
                  resetsAt: Math.floor(Date.now() / 1000) + 1_036_800,
                  used: "0",
                  limit: "25000",
                }
              : null,
          summary: {},
          usage: [],
          fetchedAt: Math.floor(Date.now() / 1000),
          stale: false,
        },
      });
    }
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/");
  const cards = page.locator(".overview-card");
  await expect(cards).toHaveCount(2);
  const merged = cards.filter({ hasText: "t**t@e*****e.com" });
  await expect(merged.locator(".overview-connection")).toHaveCount(2);
  await expect(merged.getByText("个人订阅", { exact: true })).toBeVisible();
  await expect(
    merged.getByText("Team / Business 工作区", { exact: true }),
  ).toBeVisible();
  await expect(merged.locator(".overview-window")).toHaveCount(4);
  await expect(merged.getByText("75% 剩余", { exact: true })).toBeVisible();
  const teamConnection = merged.locator(".overview-connection", {
    hasText: "Team workspace",
  });
  await expect(
    teamConnection.getByText("月度额度", { exact: true }),
  ).toBeVisible();
  await expect(
    teamConnection.getByText("100% 剩余", { exact: true }),
  ).toBeVisible();
  await expect(
    teamConnection.locator('.bar[aria-label="月度额度：已使用 0%，剩余 100%"]'),
  ).toBeVisible();
  await expect(merged.getByText("下次重置").first()).toBeVisible();
  await expect(page.getByText("邮箱待识别", { exact: true })).toBeVisible();
  await expect(page.getByRole("button", { name: "立即刷新" })).toHaveCount(0);
  await expect(page.locator("body")).not.toContainText("test@example.com");

  const geometry = await cards.first().evaluate((element) => ({
    cardWidth: element.getBoundingClientRect().width,
    progressHeight: element
      .querySelector(".overview-window .bar")
      ?.getBoundingClientRect().height,
    viewportWidth: document.documentElement.clientWidth,
    documentWidth: document.documentElement.scrollWidth,
  }));
  expect(geometry.cardWidth).toBeLessThanOrEqual(geometry.viewportWidth);
  expect(geometry.progressHeight).toBe(6);
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewportWidth);

  await merged
    .locator(".overview-connection", { hasText: "Team workspace" })
    .getByRole("button", { name: "查看详情" })
    .click();
  await expect(page).toHaveURL(/\/details$/);
  await expect(page.locator(".account-select")).toHaveValue("2");
});

test("overview uses four compact columns on a wide desktop", async ({
  page,
}, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "desktop layout only");
  await page.setViewportSize({ width: 1920, height: 1080 });
  const accounts = Array.from({ length: 4 }, (_, index) => ({
    ...responses.accounts[0],
    id: index + 1,
    displayName: `账号 ${index + 1}`,
    email: `account${index + 1}@example.com`,
  }));
  await page.route("**/api/v1/**", async (route) => {
    const requestUrl = new URL(route.request().url());
    const key = requestUrl.pathname.replace("/api/v1/", "");
    if (key === "accounts") return route.fulfill({ json: accounts });
    if (key === "dashboard") {
      const accountId = Number(requestUrl.searchParams.get("accountId"));
      return route.fulfill({
        json: {
          accountId,
          displayName: `账号 ${accountId}`,
          account: {
            email: `account${accountId}@example.com`,
            planType: "plus",
            connected: true,
          },
          limits: [
            {
              limitId: "codex",
              limitName: "Codex",
              windowType: "primary",
              usedPercent: 25,
              windowDurationMinutes: 300,
              resetsAt: Math.floor(Date.now() / 1000) + 18_000,
            },
          ],
          monthlyCreditLimit: null,
          summary: {},
          usage: [],
          fetchedAt: Math.floor(Date.now() / 1000),
          stale: false,
        },
      });
    }
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/");
  const cards = page.locator(".overview-card");
  await expect(cards).toHaveCount(4);
  await expect(
    cards.first().locator(".overview-status-dot.healthy"),
  ).toBeVisible();
  const geometry = await cards.evaluateAll((elements) =>
    elements.map((element) => {
      const rect = element.getBoundingClientRect();
      return { top: rect.top, width: rect.width };
    }),
  );
  expect(new Set(geometry.map(({ top }) => Math.round(top))).size).toBe(1);
  expect(
    Math.min(...geometry.map(({ width }) => width)),
  ).toBeGreaterThanOrEqual(320);
});

test("overview does not report a connection as healthy before its dashboard loads", async ({
  page,
}) => {
  let releaseDashboard = () => undefined;
  const dashboardGate = new Promise<void>((resolve) => {
    releaseDashboard = resolve;
  });
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "dashboard") {
      await dashboardGate;
      return route.fulfill({
        json: {
          accountId: 1,
          displayName: "默认账号",
          account: {
            email: "test@example.com",
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
  const connection = page.locator(".overview-connection");
  await expect(connection.getByText("正在读取", { exact: true })).toBeVisible();
  await expect(
    connection.locator(".overview-status-dot.loading"),
  ).toBeVisible();
  await expect(connection.locator(".overview-status-dot.healthy")).toHaveCount(
    0,
  );
  releaseDashboard();
  await expect(connection.getByText("已连接", { exact: true })).toBeVisible();
  await expect(
    connection.locator(".overview-status-dot.healthy"),
  ).toBeVisible();
});

test("overview keeps healthy connections visible when one dashboard fails", async ({
  page,
}) => {
  const accounts = [
    responses.accounts[0],
    {
      ...responses.accounts[0],
      id: 2,
      displayName: "失效连接",
      email: "broken@example.com",
    },
  ];
  await page.route("**/api/v1/**", async (route) => {
    const requestUrl = new URL(route.request().url());
    const key = requestUrl.pathname.replace("/api/v1/", "");
    if (key === "accounts") return route.fulfill({ json: accounts });
    if (key === "dashboard") {
      const accountId = Number(requestUrl.searchParams.get("accountId"));
      if (accountId === 2)
        return route.fulfill({ status: 404, json: { error: "账号不存在" } });
      return route.fulfill({
        json: {
          accountId,
          displayName: "默认账号",
          account: {
            email: "test@example.com",
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
  await expect(
    page
      .locator(".overview-card", { hasText: "t**t@e*****e.com" })
      .getByText("暂无限额数据", { exact: true }),
  ).toBeVisible();
  await expect(page.getByText("账号不存在", { exact: true })).toBeVisible();
  const healthy = page.locator(".overview-connection", { hasText: "默认账号" });
  await expect(healthy.getByText("已连接", { exact: true })).toBeVisible();
  await expect(healthy.locator(".overview-status-dot.healthy")).toBeVisible();
  const failed = page.locator(".overview-connection", { hasText: "失效连接" });
  await expect(failed.getByText("读取失败", { exact: true })).toBeVisible();
  await expect(failed.locator(".overview-status-dot.failed")).toBeVisible();
  await expect(failed.locator(".overview-status-dot.healthy")).toHaveCount(0);
  await expect(page.locator(".overview-card")).toHaveCount(2);
});

test("overview refreshes immediately after returning online", async ({
  page,
}) => {
  let accountRequests = 0;
  let dashboardRequests = 0;
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "accounts") {
      accountRequests += 1;
      return route.fulfill({ json: responses.accounts });
    }
    if (key === "dashboard") {
      dashboardRequests += 1;
      return route.fulfill({
        json: {
          accountId: 1,
          displayName: "默认账号",
          account: {
            email: "test@example.com",
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
  await expect(page.locator(".overview-connection")).toHaveCount(1);
  await expect.poll(() => dashboardRequests).toBe(1);

  await page.evaluate(() => {
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: false,
    });
    window.dispatchEvent(new Event("offline"));
  });
  await page.evaluate(() => {
    Object.defineProperty(navigator, "onLine", {
      configurable: true,
      value: true,
    });
    window.dispatchEvent(new Event("online"));
  });

  await expect.poll(() => accountRequests, { timeout: 2_000 }).toBe(2);
  await expect.poll(() => dashboardRequests, { timeout: 2_000 }).toBe(2);
});

test("keeps the account selector and icon-only refresh action compact", async ({
  page,
}) => {
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "accounts/1/sync") {
      await new Promise((resolve) => setTimeout(resolve, 500));
      return route.fulfill({ json: { ok: true } });
    }
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
          limits: [],
          summary: {},
          usage: [],
          fetchedAt: 0,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/details");
  const actions = page.locator(".header-actions");
  const selector = page.locator(".account-select");
  const refresh = page.getByRole("button", { name: "立即刷新" });
  await expect(selector).toBeVisible();
  await expect(refresh).toBeVisible();
  await expect(refresh).toHaveText("");
  await expect(refresh.locator("svg")).toHaveCount(1);

  const geometry = await actions.evaluate((element) => {
    const selectRect = element.querySelector("select")!.getBoundingClientRect();
    const buttonRect = element.querySelector("button")!.getBoundingClientRect();
    return {
      selectCenter: selectRect.top + selectRect.height / 2,
      buttonCenter: buttonRect.top + buttonRect.height / 2,
      buttonWidth: buttonRect.width,
      buttonHeight: buttonRect.height,
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
    };
  });
  expect(geometry.buttonWidth).toBe(44);
  expect(geometry.buttonHeight).toBe(44);
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewportWidth);
  if ((page.viewportSize()?.width ?? 0) <= 800)
    expect(
      Math.abs(geometry.selectCenter - geometry.buttonCenter),
    ).toBeLessThan(1);

  await refresh.click();
  const loading = page.getByRole("button", { name: "刷新中" });
  await expect(loading).toBeDisabled();
  await expect(loading.locator(".lucide-refresh-cw")).toHaveCount(1);
  const done = page.getByRole("button", { name: "刷新完成" });
  await expect(done).toBeEnabled();
  await expect(done.locator(".lucide-check")).toHaveCount(1);
});

test("shows history days matching the daily buckets without a pointer focus outline on the chart", async ({
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
          limits: [],
          summary: { peakDailyTokens: 999_900, currentStreakDays: 4 },
          usage: [
            { date: "2026-08-09", totalTokens: 200_000 },
            { date: "2026-08-10", totalTokens: 0 },
            { date: "2026-08-11", totalTokens: 300_000 },
            { date: "2026-08-12", totalTokens: 999_900 },
            { date: "2026-08-13", totalTokens: 500_000 },
          ],
          fetchedAt: 0,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/details");
  await expect(page.locator(".stat small")).toContainText([
    "累计 Tokens",
    "单日峰值 Tokens",
    "历史天数",
    "最长任务时长",
  ]);
  await expect(page.locator(".stat", { hasText: "历史天数" })).toContainText(
    "5 天",
  );

  const surface = page.locator(".chart .recharts-surface");
  await expect(surface).toHaveAttribute("tabindex", "0");
  await surface.click({ position: { x: 20, y: 20 } });
  await expect(surface).toBeFocused();
  expect(
    await surface.evaluate((element) => getComputedStyle(element).outlineStyle),
  ).toBe("none");
});

test("shows zero history days when daily usage is empty", async ({ page }) => {
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "dashboard")
      return route.fulfill({
        json: {
          accountId: 1,
          displayName: "默认账号",
          account: { email: null, planType: null, connected: true },
          limits: [],
          summary: {},
          usage: [],
          fetchedAt: 0,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/details");
  await expect(page.locator(".stat", { hasText: "历史天数" })).toContainText(
    "0 天",
  );
  await expect(page.locator(".balance .badge")).toHaveText("未识别套餐");
  await expect(page.locator(".balance-updated")).toHaveText("更新于 尚未同步");
  await expect(page.locator(".balance-empty")).toHaveText(
    "该连接暂无限额数据，请确认登录后刷新。",
  );
});

test("keeps daily Token axis labels visible on narrow screens", async ({
  page,
}) => {
  test.skip((page.viewportSize()?.width ?? 0) > 800, "mobile only");
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
          limits: [],
          summary: {},
          usage: [
            { date: "2026-08-11", totalTokens: 0 },
            { date: "2026-08-12", totalTokens: 999_900 },
            { date: "2026-08-13", totalTokens: 500_000 },
          ],
          fetchedAt: 0,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/details");
  const chart = page.locator(".chart");
  await expect(
    chart.getByRole("heading", { name: "每日 Token 趋势" }),
  ).toBeVisible();
  const labels = chart.locator(
    ".recharts-yAxis .recharts-cartesian-axis-tick-value",
  );
  await expect(labels.first()).toBeVisible();
  const geometry = await chart.evaluate((element) => {
    const chartRect = element.getBoundingClientRect();
    const ticks = Array.from(
      element.querySelectorAll(
        ".recharts-yAxis .recharts-cartesian-axis-tick-value",
      ),
    ).map((tick) => tick.getBoundingClientRect());
    return {
      chartLeft: chartRect.left,
      tickLefts: ticks.map((tick) => tick.left),
      documentWidth: document.documentElement.scrollWidth,
      viewportWidth: document.documentElement.clientWidth,
    };
  });
  expect(geometry.tickLefts.length).toBeGreaterThan(0);
  expect(Math.min(...geometry.tickLefts)).toBeGreaterThanOrEqual(
    geometry.chartLeft,
  );
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewportWidth);
});

test("mobile auxiliary menu closes with Escape and restores focus", async ({
  page,
}) => {
  test.skip((page.viewportSize()?.width ?? 0) > 800, "mobile only");
  await openSettings(page);
  const trigger = page.getByRole("button", { name: "打开更多操作" });
  await trigger.click();
  await expect(trigger).toHaveAttribute("aria-expanded", "true");
  await expect(page.getByRole("button", { name: "切换主题" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(trigger).toHaveAttribute("aria-expanded", "false");
  await expect(trigger).toBeFocused();
});

test("automatically enables Telegram features and removes the configuration", async ({
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
  await expect(page.getByLabel("启用提醒")).toHaveCount(0);
  await expect(page.getByLabel("启用查询菜单")).toHaveCount(0);
  await expect(
    page.getByText("Bot 绑定后会自动启用额度提醒和查询菜单。"),
  ).toBeVisible();
  await page.getByRole("button", { name: "验证并保存" }).click();
  await expect.poll(() => savedBody?.enabled).toBe(true);
  expect(savedBody?.menuEnabled).toBe(true);

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
  for (const name of ["安全", "Codex", "Telegram", "SMTP 邮件", "通用"]) {
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
  await expect(page.getByRole("tab", { name: "安全" })).toBeFocused();
  await expect(page.getByRole("tab", { name: "安全" })).toHaveAttribute(
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

test("updates administrator credentials and clears password fields", async ({
  page,
}) => {
  let savedBody: Record<string, unknown> | undefined;
  await page.route("**/api/v1/**", async (route) => {
    const request = route.request();
    const key = new URL(request.url()).pathname.replace("/api/v1/", "");
    if (key === "auth/credentials" && request.method() === "PUT") {
      savedBody = request.postDataJSON() as Record<string, unknown>;
      return route.fulfill({ json: { username: savedBody.username } });
    }
    return route.fulfill({ json: responses[key] ?? {} });
  });
  await page.goto("/settings");
  await page.getByRole("tab", { name: "安全" }).click();
  const panel = page.locator("#settings-panel-security");
  await expect(panel.getByRole("heading", { name: "登录安全" })).toBeVisible();
  await panel.getByLabel("用户名").fill("renamed-admin");
  await panel.getByLabel("当前密码").fill("current-password");
  await panel
    .getByLabel("新密码", { exact: true })
    .fill("replacement-password");
  await panel.getByLabel("确认新密码").fill("different-password");
  await panel.getByRole("button", { name: "更新登录凭据" }).click();
  await expect(panel.getByText("两次输入的新密码不一致")).toBeVisible();
  expect(savedBody).toBeUndefined();

  await panel.getByLabel("确认新密码").fill("replacement-password");
  await panel.getByRole("button", { name: "更新登录凭据" }).click();
  await expect.poll(() => savedBody?.username).toBe("renamed-admin");
  expect(savedBody).toEqual({
    username: "renamed-admin",
    currentPassword: "current-password",
    newPassword: "replacement-password",
  });
  await expect(
    panel.getByText("登录凭据已更新，其他设备需要重新登录"),
  ).toBeVisible();
  await expect(panel.getByLabel("当前密码")).toHaveValue("");
  await expect(panel.getByLabel("新密码", { exact: true })).toHaveValue("");
  await expect(panel.getByLabel("确认新密码")).toHaveValue("");
  await expect(
    panel.getByRole("button", { name: "更新登录凭据" }),
  ).toBeDisabled();
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

test("shows regular and monthly limits in one balance panel", async ({
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
            planType: "business",
            connected: true,
          },
          limits: [
            {
              limitId: "codex",
              limitName: "Codex",
              windowType: "primary",
              usedPercent: 25,
              windowDurationMinutes: 300,
              resetsAt: 0,
            },
            {
              limitId: "codex",
              limitName: "Codex",
              windowType: "secondary",
              usedPercent: 60,
              windowDurationMinutes: 10_080,
              resetsAt: 0,
            },
            {
              limitId: "other",
              limitName: null,
              windowType: "secondary",
              usedPercent: 10,
              windowDurationMinutes: 0,
              resetsAt: 0,
            },
          ],
          monthlyCreditLimit: {
            remainingPercent: 68,
            resetsAt: 1_788_235_200,
            used: "8000",
            limit: "25000",
          },
          summary: {},
          usage: [],
          fetchedAt: 1_786_665_609,
          stale: false,
        },
      });
    return route.fulfill({ json: responses[key] ?? {} });
  });

  await page.goto("/details");
  const balance = page.locator(".balance");
  await expect(page.getByText("Codex Account", { exact: true })).toHaveCount(0);
  await expect(balance.locator(".badge")).toHaveText("Business");
  await expect(balance.locator(".badge")).toHaveCount(1);
  await expect(balance.locator(".balance-updated")).toContainText("更新于");
  await expect(balance.locator(".balance-updated")).not.toContainText(
    "尚未同步",
  );
  await expect(balance.locator(".limit-window")).toHaveCount(4);
  await expect(balance.getByText("Codex · 5 小时窗口")).toBeVisible();
  await expect(balance.getByText("Codex · 7 天窗口")).toBeVisible();
  await expect(balance.getByText("限额窗口", { exact: true })).toBeVisible();
  await expect(balance.getByText("月度额度", { exact: true })).toBeVisible();
  await expect(
    balance.getByText("已用 8,000 / 总额 25,000 credits", { exact: true }),
  ).toBeVisible();
  const bars = balance.locator(".bar");
  await expect(bars.nth(0)).toHaveAttribute(
    "aria-label",
    "Codex · 5 小时窗口：已使用 25%，剩余 75%",
  );
  await expect(bars.nth(1)).toHaveAttribute(
    "aria-label",
    "Codex · 7 天窗口：已使用 60%，剩余 40%",
  );
  await expect(bars.nth(2)).toHaveAttribute(
    "aria-label",
    "限额窗口：已使用 10%，剩余 90%",
  );
  await expect(bars.nth(3)).toHaveAttribute(
    "aria-label",
    "月度额度：已使用 32%，剩余 68%",
  );
  await expect(balance.locator('[aria-label*="primary"]')).toHaveCount(0);
  await expect(balance.locator('[aria-label*="secondary"]')).toHaveCount(0);

  const geometry = await balance.evaluate((element) => ({
    panelWidth: element.getBoundingClientRect().width,
    windowWidths: Array.from(element.querySelectorAll(".limit-window")).map(
      (window) => window.getBoundingClientRect().width,
    ),
    headerPaddingTop: getComputedStyle(
      element.querySelector(".balance-header")!,
    ).paddingTop,
    windowPaddingTop: getComputedStyle(element.querySelector(".limit-window")!)
      .paddingTop,
    barHeight: element.querySelector(".bar")!.getBoundingClientRect().height,
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: document.documentElement.clientWidth,
  }));
  expect(geometry.headerPaddingTop).toBe("14px");
  expect(geometry.windowPaddingTop).toBe("16px");
  expect(geometry.barHeight).toBe(10);
  expect(Math.max(...geometry.windowWidths)).toBeLessThanOrEqual(
    geometry.panelWidth,
  );
  expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewportWidth);
});

test("silently retries a transient dashboard read failure", async ({
  page,
}) => {
  let dashboardRequests = 0;
  await page.route("**/api/v1/**", async (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "dashboard") {
      dashboardRequests += 1;
      if (dashboardRequests === 1) return route.abort("internetdisconnected");
      return route.fulfill({
        json: {
          accountId: 1,
          displayName: "默认账号",
          account: { email: null, planType: "plus", connected: true },
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

  await page.goto("/details");
  await expect.poll(() => dashboardRequests).toBe(1);
  await expect(page.locator(".banner")).toHaveCount(0);
  await expect(page.locator(".account-select")).toBeVisible();
  expect(dashboardRequests).toBe(2);
  await expect(page.locator(".banner")).toHaveCount(0);
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

  await page.goto("/details");
  await expect(page.locator(".account-select")).toContainText(
    "t**t@e*****e.com",
  );
  await expect(page.getByText("已使用 25%", { exact: true })).toHaveCount(0);
  await expect(page.getByText("剩余 75%", { exact: true })).toHaveCount(0);
  await expect(page.locator(".bar")).toHaveAttribute(
    "aria-label",
    "5 小时窗口：已使用 25%，剩余 75%",
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
  await page.goto("/details");
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
            planType: accountId === 1 ? "plus" : "pro",
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
  await page.goto("/details");
  const selector = page.locator(".account-select");
  await selector.selectOption("2");
  await expect(page.locator(".balance .badge")).toHaveText("Pro");
  await page.waitForTimeout(350);
  await expect(page.locator(".balance .badge")).toHaveText("Pro");
  await expect(page.locator(".balance .badge")).not.toHaveText("Plus");
});
