import { expect, test, type Page } from "@playwright/test";

const publicOverview = {
  cards: [
    {
      title: "t**t@e*****e.com",
      emailIdentified: true,
      connections: [
        {
          displayName: "个人订阅",
          planType: "plus",
          kind: "personal",
          status: "healthy",
          fetchedAt: 1_787_090_400,
          limits: [
            {
              limitName: "Codex",
              windowDurationMinutes: 300,
              usedPercent: 25,
              resetsAt: 1_887_090_400,
            },
            {
              limitName: null,
              windowDurationMinutes: 10_080,
              usedPercent: 60,
              resetsAt: 1_887_694_800,
            },
          ],
          monthlyCreditLimit: {
            remainingPercent: 68,
            resetsAt: 1_888_213_200,
          },
        },
        {
          displayName: "Team workspace",
          planType: "business",
          kind: "team",
          status: "stale",
          fetchedAt: 1_787_090_300,
          limits: [],
          monthlyCreditLimit: null,
        },
      ],
    },
  ],
};

async function mockPublicPage(
  page: Page,
  authenticated = false,
  overview = publicOverview,
) {
  await page.route("**/api/v1/**", (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "system/status")
      return route.fulfill({
        json: { initialized: true, appServer: true, version: "0.4.0" },
      });
    if (key === "auth/me")
      return authenticated
        ? route.fulfill({ json: { username: "admin" } })
        : route.fulfill({ status: 401, json: { error: "未登录" } });
    if (key === "public/overview") return route.fulfill({ json: overview });
    return route.fulfill({ status: 404, json: { error: "接口不存在" } });
  });
}

test("匿名访问公开页并展示全部脱敏用量卡片", async ({ page }) => {
  await mockPublicPage(page);
  await page.goto("/");

  await expect(page).toHaveURL(/\/$/);
  await expect(page.getByText("PUBLIC USAGE STATUS")).toHaveCount(0);
  await expect(page.getByText("Codex 用量状态", { exact: true })).toHaveCount(
    0,
  );
  await expect(
    page.getByText("无需登录，快速查看各个 Codex 连接的额度与重置时间。"),
  ).toHaveCount(0);
  await expect(page.getByText("最近更新", { exact: true })).toHaveCount(0);
  await expect(page.locator(".public-usage-card")).toHaveCount(1);
  await expect(page.locator(".public-connection")).toHaveCount(2);
  await expect(page.locator(".public-limit")).toHaveCount(3);
  await expect(page.getByText("t**t@e*****e.com")).toBeVisible();
  await expect(page.getByText("运行正常", { exact: true })).toBeVisible();
  await expect(page.getByText("数据可能已过期", { exact: true })).toBeVisible();
  await expect(page.getByText("test@example.com")).toHaveCount(0);
  await expect(page.getByText("查看详情")).toHaveCount(0);

  const cardSpacing = await page
    .locator(".public-usage-card")
    .evaluate((card) => {
      const header = card.querySelector(".public-card-header");
      const connections = card.querySelector(".public-connections");
      const heading = card.querySelector(".public-connection-heading");
      if (!header || !connections || !heading)
        throw new Error("卡片结构不完整");
      const headerRect = header.getBoundingClientRect();
      return {
        headerMarginBottom: getComputedStyle(header).marginBottom,
        connectionGap:
          connections.getBoundingClientRect().top - headerRect.bottom,
        headingGap: heading.getBoundingClientRect().top - headerRect.bottom,
      };
    });
  expect(cardSpacing.headerMarginBottom).toBe("0px");
  expect(Math.abs(cardSpacing.connectionGap)).toBeLessThanOrEqual(1);
  expect(cardSpacing.headingGap).toBeLessThanOrEqual(14);

  const login = page.getByRole("link", { name: "登录" });
  await expect(login).toHaveAttribute("href", "/login");
  const github = page.locator(".public-icon-button");
  await expect(github).toHaveAttribute(
    "href",
    "https://github.com/GleanAI/codex-helper",
  );
  await expect(github).toHaveAttribute("target", "_blank");
  const footer = page.locator(".public-footer");
  await expect(footer).toContainText("本站点由 Codex Helper 驱动");
  await expect(
    footer.getByRole("link", { name: "Codex Helper" }),
  ).toHaveAttribute("href", "https://github.com/GleanAI/codex-helper");

  const overflow = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
  }));
  expect(overflow.documentWidth).toBeLessThanOrEqual(overflow.viewportWidth);
});

test("公开页读取失败后允许手动重试", async ({ page }) => {
  let requests = 0;
  await page.route("**/api/v1/**", (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "system/status")
      return route.fulfill({
        json: { initialized: true, appServer: true, version: "test" },
      });
    if (key === "auth/me")
      return route.fulfill({ status: 401, json: { error: "未登录" } });
    if (key === "public/overview") {
      requests += 1;
      if (requests === 1)
        return route.fulfill({ status: 403, json: { error: "暂不可用" } });
      return route.fulfill({ json: publicOverview });
    }
    return route.fulfill({ status: 404, json: { error: "接口不存在" } });
  });
  await page.goto("/");
  await expect(
    page.getByRole("heading", { name: "暂时无法读取公开用量" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "重新加载" }).click();
  await expect(page.locator(".public-usage-card")).toBeVisible();
  expect(requests).toBe(2);
});

test("旧公开页路径跳转到根路径", async ({ page }) => {
  await mockPublicPage(page);
  await page.goto("/public");

  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator(".public-usage-card")).toBeVisible();
});

test("已登录用户访问根路径仍然看到公开页", async ({ page }) => {
  await mockPublicPage(page, true);
  await page.goto("/");

  await expect(page).toHaveURL(/\/$/);
  await expect(page.locator(".public-usage-card")).toBeVisible();
});

test("公开页在宽屏中紧凑显示四张完整卡片", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "desktop layout only");
  await page.setViewportSize({ width: 1920, height: 929 });
  const overview = {
    cards: Array.from({ length: 4 }, (_, index) => ({
      ...publicOverview.cards[0],
      title: `a***${index + 1}@e*****e.com`,
    })),
  };
  await mockPublicPage(page, false, overview);
  await page.goto("/");

  const cards = page.locator(".public-usage-card");
  await expect(cards).toHaveCount(4);
  await expect(cards.last().locator(".public-limit")).toHaveCount(3);
  const geometry = await cards.evaluateAll((elements) =>
    elements.map((element) => {
      const rect = element.getBoundingClientRect();
      return { top: rect.top, width: rect.width };
    }),
  );
  expect(new Set(geometry.map(({ top }) => Math.round(top))).size).toBe(1);
  expect(
    Math.min(...geometry.map(({ width }) => width)),
  ).toBeGreaterThanOrEqual(340);
  expect(Math.max(...geometry.map(({ width }) => width))).toBeLessThan(400);
  const viewport = await page.evaluate(() => ({
    documentHeight: document.documentElement.scrollHeight,
    documentWidth: document.documentElement.scrollWidth,
    viewportHeight: window.innerHeight,
    viewportWidth: window.innerWidth,
  }));
  expect(viewport.documentHeight).toBeLessThanOrEqual(viewport.viewportHeight);
  expect(viewport.documentWidth).toBeLessThanOrEqual(viewport.viewportWidth);
});

test("公开页适配不同桌面宽度的卡片列数", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "desktop layout only");
  const overview = {
    cards: Array.from({ length: 4 }, (_, index) => ({
      ...publicOverview.cards[0],
      title: `a***${index + 1}@e*****e.com`,
    })),
  };
  await mockPublicPage(page, false, overview);
  await page.goto("/");
  const usageCards = page.locator(".public-usage-card");
  await expect(usageCards).toHaveCount(4);

  for (const { width, columns } of [
    { width: 1920, columns: 4 },
    { width: 1536, columns: 4 },
    { width: 1366, columns: 3 },
    { width: 1024, columns: 2 },
    { width: 768, columns: 2 },
  ]) {
    await page.setViewportSize({ width, height: 650 });
    const cards = await usageCards.evaluateAll((cards) =>
      cards.map((card) => {
        const rect = card.getBoundingClientRect();
        return { top: Math.round(rect.top), width: rect.width };
      }),
    );
    expect(cards.filter(({ top }) => top === cards[0].top)).toHaveLength(
      columns,
    );
    expect(Math.max(...cards.map((card) => card.width))).toBeLessThanOrEqual(
      360,
    );
    const pageWidth = await page.evaluate(() => ({
      document: document.documentElement.scrollWidth,
      viewport: window.innerWidth,
    }));
    expect(pageWidth.document).toBeLessThanOrEqual(pageWidth.viewport);
  }
});

test("公开页常规卡片和页脚适配不同桌面可用高度", async ({ page }, testInfo) => {
  test.skip(testInfo.project.name !== "desktop", "desktop layout only");
  await mockPublicPage(page);
  await page.goto("/");
  await expect(page.locator(".public-usage-card")).toBeVisible();
  await expect(page.locator(".public-footer")).toBeVisible();

  for (const viewport of [
    { width: 1920, height: 929 },
    { width: 1536, height: 743 },
    { width: 1366, height: 650 },
    { width: 1024, height: 650 },
    { width: 768, height: 650 },
  ]) {
    await page.setViewportSize(viewport);
    const geometry = await page.evaluate(() => {
      const card = document.querySelector(".public-usage-card");
      const footer = document.querySelector(".public-footer");
      if (!card || !footer) throw new Error("公开页布局不完整");
      const cardRect = card.getBoundingClientRect();
      const footerRect = footer.getBoundingClientRect();
      return {
        cardLeft: cardRect.left,
        cardBottom: cardRect.bottom,
        footerTop: footerRect.top,
        footerBottom: footerRect.bottom,
        documentHeight: document.documentElement.scrollHeight,
        documentWidth: document.documentElement.scrollWidth,
        viewportHeight: window.innerHeight,
        viewportWidth: window.innerWidth,
      };
    });
    expect(geometry.cardLeft).toBeGreaterThanOrEqual(31);
    expect(geometry.cardBottom).toBeLessThanOrEqual(geometry.footerTop);
    expect(geometry.footerBottom).toBeLessThanOrEqual(geometry.viewportHeight);
    expect(geometry.documentHeight).toBeLessThanOrEqual(
      geometry.viewportHeight,
    );
    expect(geometry.documentWidth).toBeLessThanOrEqual(geometry.viewportWidth);
  }
});

test("公开页在移动浏览器使用无横向溢出的完整单列卡片", async ({
  page,
}, testInfo) => {
  test.skip(testInfo.project.name === "desktop", "mobile layout only");
  const overview = {
    cards: [
      publicOverview.cards[0],
      { ...publicOverview.cards[0], title: "s**d@e*****e.com" },
    ],
  };
  await mockPublicPage(page, false, overview);
  await page.goto("/");

  const cards = page.locator(".public-usage-card");
  await expect(cards).toHaveCount(2);
  const geometry = await cards.evaluateAll((elements) =>
    elements.map((element) => {
      const rect = element.getBoundingClientRect();
      return { left: rect.left, top: rect.top, width: rect.width };
    }),
  );
  expect(Math.round(geometry[0].left)).toBe(Math.round(geometry[1].left));
  expect(geometry[1].top).toBeGreaterThan(geometry[0].top);
  const pageGeometry = await page.evaluate(() => ({
    documentWidth: document.documentElement.scrollWidth,
    viewportWidth: window.innerWidth,
  }));
  expect(pageGeometry.documentWidth).toBeLessThanOrEqual(
    pageGeometry.viewportWidth,
  );
  for (const control of [
    page.locator(".public-icon-button"),
    page.locator(".public-login-button"),
  ]) {
    const box = await control.boundingBox();
    expect(box?.width).toBeGreaterThanOrEqual(44);
    expect(box?.height).toBeGreaterThanOrEqual(44);
  }
});

test("后台路径使用独立登录入口并在登录后进入总览", async ({ page }) => {
  await page.route("**/api/v1/**", (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "system/status")
      return route.fulfill({
        json: { initialized: true, appServer: true, version: "test" },
      });
    if (key === "auth/me")
      return route.fulfill({ status: 401, json: { error: "未登录" } });
    if (key === "auth/login") return route.fulfill({ json: { ok: true } });
    if (key === "accounts") return route.fulfill({ json: [] });
    if (key === "settings/general")
      return route.fulfill({
        json: {
          timezone: "UTC",
          theme: "system",
          syncMinutes: 5,
          retentionDays: 90,
          beforeMinutes: 30,
          notifyBefore: true,
          notifyAfter: true,
        },
      });
    return route.fulfill({ status: 404, json: { error: "接口不存在" } });
  });

  await page.goto("/overview");
  await expect(page).toHaveURL(/\/login$/);
  await page.getByLabel("用户名").fill("admin");
  await page.getByLabel("密码").fill("test-password");
  await page.getByRole("button", { name: "登录" }).click();

  await expect(page).toHaveURL(/\/overview$/);
  await expect(page.getByRole("heading", { name: "用量总览" })).toBeVisible();
});
