import { describe, expect, it } from "vitest";
import { sortLimitsForDisplay } from "./limit-order";

type TestLimit = {
  id: string;
  limitName: string | null;
  windowDurationMinutes: number;
};

describe("sortLimitsForDisplay", () => {
  it("按 5 小时、普通 7 天、gpt-reserve 7 天和其他窗口稳定排序", () => {
    const limits: TestLimit[] = [
      { id: "other-1", limitName: null, windowDurationMinutes: 0 },
      {
        id: "reserve-1",
        limitName: " GPT-Reserve ",
        windowDurationMinutes: 10_080,
      },
      { id: "week-1", limitName: "Codex", windowDurationMinutes: 10_080 },
      { id: "five-1", limitName: "Codex", windowDurationMinutes: 300 },
      { id: "other-2", limitName: "Review", windowDurationMinutes: 43_200 },
      { id: "week-2", limitName: null, windowDurationMinutes: 10_080 },
      {
        id: "reserve-2",
        limitName: "gpt-reserve",
        windowDurationMinutes: 10_080,
      },
      { id: "five-2", limitName: "Review", windowDurationMinutes: 300 },
    ];

    expect(sortLimitsForDisplay(limits).map(({ id }) => id)).toEqual([
      "five-1",
      "five-2",
      "week-1",
      "week-2",
      "reserve-1",
      "reserve-2",
      "other-1",
      "other-2",
    ]);
    expect(limits.map(({ id }) => id)).toEqual([
      "other-1",
      "reserve-1",
      "week-1",
      "five-1",
      "other-2",
      "week-2",
      "reserve-2",
      "five-2",
    ]);
  });

  it("缺失目标窗口时仍保留其他窗口的原始顺序", () => {
    const limits: TestLimit[] = [
      { id: "monthly", limitName: null, windowDurationMinutes: 43_200 },
      { id: "unknown", limitName: null, windowDurationMinutes: 0 },
    ];

    expect(sortLimitsForDisplay(limits).map(({ id }) => id)).toEqual([
      "monthly",
      "unknown",
    ]);
  });
});
