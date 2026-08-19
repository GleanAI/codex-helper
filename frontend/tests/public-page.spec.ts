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

async function mockPublicPage(page: Page) {
  await page.route("**/api/v1/**", (route) => {
    const key = new URL(route.request().url()).pathname.replace("/api/v1/", "");
    if (key === "system/status")
      return route.fulfill({
        json: { initialized: true, appServer: true, version: "0.4.0" },
      });
    if (key === "auth/me")
      return route.fulfill({ status: 401, json: { error: "未登录" } });
    if (key === "public/overview")
      return route.fulfill({ json: publicOverview });
    return route.fulfill({ status: 404, json: { error: "接口不存在" } });
  });
}

test("匿名访问公开页并展示全部脱敏用量卡片", async ({ page }) => {
  await mockPublicPage(page);
  await page.goto("/public");

  await expect(page).toHaveURL(/\/public$/);
  await expect(
    page.getByRole("heading", { name: "Codex 用量状态" }),
  ).toBeVisible();
  await expect(page.locator(".public-usage-card")).toHaveCount(1);
  await expect(page.locator(".public-connection")).toHaveCount(2);
  await expect(page.locator(".public-limit")).toHaveCount(3);
  await expect(page.getByText("t**t@e*****e.com")).toBeVisible();
  await expect(page.getByText("运行正常", { exact: true })).toBeVisible();
  await expect(page.getByText("数据可能已过期", { exact: true })).toBeVisible();
  await expect(page.getByText("test@example.com")).toHaveCount(0);
  await expect(page.getByText("查看详情")).toHaveCount(0);

  const login = page.getByRole("link", { name: "登录" });
  await expect(login).toHaveAttribute("href", "/");
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
  await page.goto("/public");
  await expect(
    page.getByRole("heading", { name: "暂时无法读取公开用量" }),
  ).toBeVisible();
  await page.getByRole("button", { name: "重新加载" }).click();
  await expect(page.locator(".public-usage-card")).toBeVisible();
  expect(requests).toBe(2);
});
