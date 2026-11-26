import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  Legend,
  Line,
  LineChart,
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";

export type ChartDatum = { name: string; value: number };
type Props = { data: ChartDatum[]; label?: string };

/** OpenDashboards dashboard chart series palette (--od-series-1..8). */
export const OD_SERIES = [
  "var(--od-series-1, #6155e8)",
  "var(--od-series-2, #16b76f)",
  "var(--od-series-3, #ffc700)",
  "var(--od-series-4, #df507b)",
  "var(--od-series-5, #3894ff)",
  "var(--od-series-6, #ff8000)",
  "var(--od-series-7, #a0522d)",
  "var(--od-series-8, #00cbd9)",
];

const tooltipContentStyle = {
  backgroundColor: "var(--od-surface, #ffffff)",
  border: "1px solid var(--od-border-strong, rgba(0, 0, 0, 0.1))",
  borderRadius: "var(--od-radius-sm, 8px)",
  boxShadow: "var(--od-shadow-pop)",
  fontSize: "12px",
  color: "var(--od-ink, #0d0d0d)",
  fontVariantNumeric: "tabular-nums",
};

const axisTickProps = {
  fontSize: 11,
  fill: "var(--od-ink-muted, rgba(13, 13, 13, 0.52))",
};

function Frame({ children, label }: { children: React.ReactElement; label: string }) {
  return (
    <div role="img" aria-label={label} className="h-64 w-full">
      <ResponsiveContainer width="100%" height="100%">
        {children}
      </ResponsiveContainer>
    </div>
  );
}

export function LineChartView({ data, label = "Line chart" }: Props) {
  return (
    <Frame label={label}>
      <LineChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--od-grid, #f1f1f1)" />
        <XAxis dataKey="name" tick={axisTickProps} axisLine={false} tickLine={false} />
        <YAxis tick={axisTickProps} axisLine={false} tickLine={false} width={48} />
        <Tooltip contentStyle={tooltipContentStyle} />
        <Line
          type="monotone"
          dataKey="value"
          stroke={OD_SERIES[0]}
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4 }}
        />
      </LineChart>
    </Frame>
  );
}

export function IncomeExpenseChart({
  data,
  label = "Income versus expenses by day",
  onDayClick,
}: {
  data: { name: string; date?: string; income: number; expenses: number }[];
  label?: string;
  onDayClick?: (date: string) => void;
}) {
  return (
    <Frame label={label}>
      <LineChart
        data={data}
        margin={{ top: 8, right: 12, left: 0, bottom: 0 }}
        onClick={(state) => {
          const active = state?.activeLabel;
          if (!onDayClick || !active) return;
          const match = data.find((d) => d.name === String(active));
          if (match?.date) onDayClick(match.date);
        }}
      >
        <CartesianGrid vertical={false} stroke="var(--od-grid, #f1f1f1)" />
        <XAxis dataKey="name" tick={axisTickProps} axisLine={false} tickLine={false} />
        <YAxis tick={axisTickProps} axisLine={false} tickLine={false} width={54} />
        <Tooltip contentStyle={tooltipContentStyle} />
        <Legend
          wrapperStyle={{
            fontSize: "12px",
            color: "var(--od-ink-secondary)",
            paddingTop: "8px",
          }}
        />
        <Line
          type="monotone"
          dataKey="income"
          name="Income"
          stroke="var(--od-good-mark, #16b76f)"
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4 }}
        />
        <Line
          type="monotone"
          dataKey="expenses"
          name="Expenses"
          stroke="var(--od-bad-mark, #d03348)"
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4 }}
        />
      </LineChart>
    </Frame>
  );
}

export function BarChartView({ data, label = "Bar chart" }: Props) {
  return (
    <Frame label={label}>
      <BarChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--od-grid, #f1f1f1)" />
        <XAxis dataKey="name" tick={axisTickProps} axisLine={false} tickLine={false} />
        <YAxis tick={axisTickProps} axisLine={false} tickLine={false} width={54} />
        <Tooltip contentStyle={tooltipContentStyle} cursor={{ fill: "var(--od-surface-subtle)" }} />
        <Bar dataKey="value" fill={OD_SERIES[0]} radius={[6, 6, 0, 0]} maxBarSize={44} />
      </BarChart>
    </Frame>
  );
}

export function AreaChartView({ data, label = "Area chart" }: Props) {
  return (
    <Frame label={label}>
      <AreaChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--od-grid, #f1f1f1)" />
        <XAxis dataKey="name" tick={axisTickProps} axisLine={false} tickLine={false} />
        <YAxis tick={axisTickProps} axisLine={false} tickLine={false} width={54} />
        <Tooltip contentStyle={tooltipContentStyle} />
        <Area
          type="monotone"
          dataKey="value"
          stroke={OD_SERIES[0]}
          strokeWidth={2}
          fill={OD_SERIES[0]}
          fillOpacity={0.12}
        />
      </AreaChart>
    </Frame>
  );
}

export function PieChartView({ data, label = "Pie chart" }: Props) {
  return (
    <Frame label={label}>
      <PieChart>
        <Pie
          data={data}
          dataKey="value"
          nameKey="name"
          innerRadius="48%"
          outerRadius="80%"
          paddingAngle={2}
          stroke="var(--od-surface, #ffffff)"
          strokeWidth={2}
        >
          {data.map((item, index) => (
            <Cell key={item.name} fill={OD_SERIES[index % OD_SERIES.length]} />
          ))}
        </Pie>
        <Tooltip contentStyle={tooltipContentStyle} />
        <Legend
          wrapperStyle={{
            fontSize: "12px",
            color: "var(--od-ink-secondary)",
          }}
        />
      </PieChart>
    </Frame>
  );
}
