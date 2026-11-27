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
import { useI18n } from "@/features/i18n/i18n";

export type ChartDatum = { name: string; value: number };
type Props = { data: ChartDatum[]; label?: string };

/** OpenDashboards dashboard chart series palette (--od-series-1..8). */
export const OD_SERIES = [
  "var(--od-series-1, #5a52c4)",
  "var(--od-series-2, #2e7d57)",
  "var(--od-series-3, #c7932a)",
  "var(--od-series-4, #b84b6a)",
  "var(--od-series-5, #2f5f9e)",
  "var(--od-series-6, #c76d2b)",
  "var(--od-series-7, #8c5236)",
  "var(--od-series-8, #248a94)",
];

const tooltipContentStyle = {
  backgroundColor: "var(--od-surface, #f7f5f0)",
  border: "1px solid var(--od-border-strong, #cfcbc0)",
  borderRadius: "var(--od-radius-sm, 8px)",
  boxShadow: "var(--od-shadow-pop)",
  fontSize: "12px",
  color: "var(--od-ink, #2a2d33)",
  fontVariantNumeric: "tabular-nums",
};

const axisTickProps = {
  fontSize: 11,
  fill: "var(--od-ink-muted, #585d66)",
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

export function LineChartView({ data, label }: Props) {
  const { t } = useI18n();
  return (
    <Frame label={label ?? t.common.lineChart}>
      <LineChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--od-grid, #dedad0)" />
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
  label,
  onDayClick,
}: {
  data: { name: string; date?: string; income: number; expenses: number }[];
  label?: string;
  onDayClick?: (date: string) => void;
}) {
  const { t } = useI18n();
  return (
    <Frame label={label ?? t.common.incomeVersusExpensesByDay}>
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
        <CartesianGrid vertical={false} stroke="var(--od-grid, #dedad0)" />
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
          name={t.common.income}
          stroke="var(--od-good-mark, #16b76f)"
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4 }}
        />
        <Line
          type="monotone"
          dataKey="expenses"
          name={t.common.expenses}
          stroke="var(--od-bad-mark, #d03348)"
          strokeWidth={2}
          dot={false}
          activeDot={{ r: 4 }}
        />
      </LineChart>
    </Frame>
  );
}

export function BarChartView({ data, label }: Props) {
  const { t } = useI18n();
  return (
    <Frame label={label ?? t.common.barChart}>
      <BarChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--od-grid, #dedad0)" />
        <XAxis dataKey="name" tick={axisTickProps} axisLine={false} tickLine={false} />
        <YAxis tick={axisTickProps} axisLine={false} tickLine={false} width={54} />
        <Tooltip contentStyle={tooltipContentStyle} cursor={{ fill: "var(--od-surface-subtle)" }} />
        <Bar dataKey="value" fill={OD_SERIES[0]} radius={[6, 6, 0, 0]} maxBarSize={44} />
      </BarChart>
    </Frame>
  );
}

export function AreaChartView({ data, label }: Props) {
  const { t } = useI18n();
  return (
    <Frame label={label ?? t.common.areaChart}>
      <AreaChart data={data} margin={{ top: 8, right: 12, left: 0, bottom: 0 }}>
        <CartesianGrid vertical={false} stroke="var(--od-grid, #dedad0)" />
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

export function PieChartView({ data, label }: Props) {
  const { t } = useI18n();
  return (
    <Frame label={label ?? t.common.pieChart}>
      <PieChart>
        <Pie
          data={data}
          dataKey="value"
          nameKey="name"
          innerRadius="48%"
          outerRadius="80%"
          paddingAngle={2}
          stroke="var(--od-surface, #f7f5f0)"
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
