import {
  Area,
  AreaChart,
  CartesianGrid,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import type { Point } from "./types";

const compactAxis = (value: number) =>
  new Intl.NumberFormat("en", {
    notation: "compact",
    maximumFractionDigits: 1,
  }).format(value);

export default function UsageChart({ data }: { data: Point[] }) {
  return (
    <div className="panel chart">
      <div>
        <h3>每日 Token 趋势</h3>
      </div>
      <ResponsiveContainer width="100%" height={280}>
        <AreaChart data={data}>
          <defs>
            <linearGradient id="a" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" stopColor="#5ee7f7" stopOpacity={0.35} />
              <stop offset="1" stopColor="#5ee7f7" stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke="var(--grid)" vertical={false} />
          <XAxis dataKey="date" stroke="var(--muted)" />
          <YAxis
            width={55}
            tickCount={5}
            tickFormatter={compactAxis}
            stroke="var(--muted)"
          />
          <Tooltip
            formatter={(value) => [
              new Intl.NumberFormat("zh-CN").format(Number(value)),
              "Tokens",
            ]}
            contentStyle={{
              background: "var(--panel)",
              border: "1px solid var(--border)",
              borderRadius: 12,
            }}
          />
          <Area
            type="monotone"
            dataKey="totalTokens"
            stroke="#4dd8ed"
            fill="url(#a)"
            strokeWidth={2}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
