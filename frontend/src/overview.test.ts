import { describe, expect, it } from "vitest";
import { groupAccounts } from "./overview";
import type { Account } from "./types";

const account = (overrides: Partial<Account>): Account => ({
  id: 1,
  displayName: "连接",
  email: "user@example.com",
  planType: "plus",
  expectedKind: "any",
  actualKind: "personal",
  validationStatus: "matched",
  possibleDuplicate: false,
  connected: true,
  ...overrides,
});

describe("总览账号分组", () => {
  it("忽略邮箱首尾空白和大小写，并按个人、团队、未知连接排序", () => {
    const groups = groupAccounts([
      account({ id: 3, email: " USER@example.com ", actualKind: "unknown" }),
      account({ id: 2, email: "user@EXAMPLE.com", actualKind: "team" }),
      account({ id: 1, email: "user@example.com", actualKind: "personal" }),
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].email).toBe("USER@example.com");
    expect(groups[0].accounts.map((item) => item.id)).toEqual([1, 2, 3]);
  });

  it("保留同邮箱的多个同类连接", () => {
    const groups = groupAccounts([
      account({ id: 1, actualKind: "team" }),
      account({ id: 2, actualKind: "team" }),
    ]);

    expect(groups).toHaveLength(1);
    expect(groups[0].accounts.map((item) => item.id)).toEqual([1, 2]);
  });

  it("没有邮箱的连接各自成组，并按最小账号 ID 排序", () => {
    const groups = groupAccounts([
      account({ id: 4, email: null }),
      account({ id: 2, email: null }),
    ]);

    expect(groups.map((group) => group.key)).toEqual([
      "account:2",
      "account:4",
    ]);
  });
});
