type DisplayLimit = {
  limitName: string | null;
  windowDurationMinutes: number;
};

export function sortLimitsForDisplay<T extends DisplayLimit>(
  limits: readonly T[],
): T[] {
  return limits
    .map((limit, index) => ({ limit, index }))
    .sort(
      (left, right) =>
        displayOrder(left.limit) - displayOrder(right.limit) ||
        left.index - right.index,
    )
    .map(({ limit }) => limit);
}

function displayOrder(limit: DisplayLimit) {
  if (limit.windowDurationMinutes === 300) return 0;
  if (limit.windowDurationMinutes !== 10_080) return 3;
  return limit.limitName?.trim().toLowerCase() === "gpt-reserve" ? 2 : 1;
}
