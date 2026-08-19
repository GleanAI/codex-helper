import { describe, expect, it } from "vitest";
import { ApiError, toErrorMessage } from "./api";
import {
  decodeAction,
  decodeAuthProfile,
  decodeDashboard,
  decodeDeviceLogin,
  decodeGeneral,
  decodePublicOverview,
  decodeTelegram,
} from "./types";

describe("API decoders", () => {
  it("保留 Dashboard 的 null 与 optional 摘要", () => {
    const value = decodeDashboard({
      accountId: 2,
      displayName: "Team",
      account: { email: null, planType: null, connected: false },
      limits: [],
      monthlyCreditLimit: {
        remainingPercent: 68,
        resetsAt: 1_788_235_200,
        used: "8000",
        limit: "25000",
      },
      summary: { lifetimeTokens: null },
      usage: [],
      fetchedAt: 0,
      stale: true,
    });
    expect(value.summary.lifetimeTokens).toBeNull();
    expect(value.account.email).toBeNull();
    expect(value.monthlyCreditLimit).toEqual({
      remainingPercent: 68,
      resetsAt: 1_788_235_200,
      used: "8000",
      limit: "25000",
    });
  });

  it("兼容缺失的月度额度并拒绝非法字段", () => {
    const dashboard = {
      accountId: 1,
      displayName: "Team",
      account: { email: null, planType: "business", connected: true },
      limits: [],
      summary: {},
      usage: [],
      fetchedAt: 0,
      stale: false,
    };
    expect(decodeDashboard(dashboard).monthlyCreditLimit).toBeNull();
    expect(() =>
      decodeDashboard({
        ...dashboard,
        monthlyCreditLimit: {
          remainingPercent: "68",
          resetsAt: 1_788_235_200,
          used: "8000",
          limit: "25000",
        },
      }),
    ).toThrow("remainingPercent格式无效");
  });

  it("拒绝未知主题", () => {
    expect(() =>
      decodeGeneral({
        timezone: "UTC",
        theme: "blue",
        syncMinutes: 5,
        retentionDays: 90,
        beforeMinutes: 30,
        notifyBefore: true,
        notifyAfter: true,
      }),
    ).toThrow("theme格式无效");
  });

  it("解码脱敏的公开总览并拒绝未知状态", () => {
    const response = {
      cards: [
        {
          title: "t**t@e***e.com",
          emailIdentified: true,
          connections: [
            {
              displayName: "默认账号",
              planType: "plus",
              kind: "personal",
              status: "healthy",
              fetchedAt: 100,
              limits: [
                {
                  limitName: null,
                  windowDurationMinutes: 300,
                  usedPercent: 25,
                  resetsAt: 200,
                },
              ],
              monthlyCreditLimit: null,
            },
          ],
        },
      ],
    };
    expect(decodePublicOverview(response).cards[0].connections[0].status).toBe(
      "healthy",
    );
    expect(() =>
      decodePublicOverview({
        cards: [
          {
            ...response.cards[0],
            connections: [
              { ...response.cards[0].connections[0], status: "secret" },
            ],
          },
        ],
      }),
    ).toThrow("status格式无效");
  });

  it("只接受 HTTPS 设备授权地址", () => {
    expect(() =>
      decodeDeviceLogin({
        verificationUrl: "http://example.com",
        userCode: "ABC",
      }),
    ).toThrow("HTTPS");
    expect(
      decodeDeviceLogin({
        verificationUrl: "https://example.com",
        userCode: "ABC",
      }).userCode,
    ).toBe("ABC");
  });

  it("保留 Telegram 操作警告", () => {
    expect(
      decodeTelegram({
        chatId: 1,
        enabled: false,
        menuEnabled: false,
        configured: true,
        warning: "菜单同步失败",
      }).warning,
    ).toBe("菜单同步失败");
    expect(decodeAction({ ok: true, warning: "键盘清理失败" }).warning).toBe(
      "键盘清理失败",
    );
  });

  it("解码管理员资料", () => {
    expect(decodeAuthProfile({ username: "admin" })).toEqual({
      username: "admin",
    });
    expect(() => decodeAuthProfile({ username: null })).toThrow(
      "username格式无效",
    );
  });
});

describe("errors", () => {
  it("保留结构化 API 错误", () => {
    const error = new ApiError("未登录", "http", 401);
    expect(error.status).toBe(401);
    expect(toErrorMessage(error)).toBe("未登录");
    expect(toErrorMessage("bad")).toBe("发生未知错误");
  });
});
