import { describe, expect, it } from "vitest";
import { ApiError, toErrorMessage } from "./api";
import { decodeDashboard, decodeDeviceLogin, decodeGeneral } from "./types";

describe("API decoders", () => {
  it("保留 Dashboard 的 null 与 optional 摘要", () => {
    const value = decodeDashboard({
      accountId: 2,
      displayName: "Team",
      account: { email: null, planType: null, connected: false },
      limits: [],
      summary: { lifetimeTokens: null },
      usage: [],
      fetchedAt: 0,
      stale: true,
    });
    expect(value.summary.lifetimeTokens).toBeNull();
    expect(value.account.email).toBeNull();
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
});

describe("errors", () => {
  it("保留结构化 API 错误", () => {
    const error = new ApiError("未登录", "http", 401);
    expect(error.status).toBe(401);
    expect(toErrorMessage(error)).toBe("未登录");
    expect(toErrorMessage("bad")).toBe("发生未知错误");
  });
});
