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
const colors = ["#1c9c70", "#5e81e6", "#e7aa42", "#bc70d6", "#e47c70"];
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
      <LineChart data={data}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="name" />
        <YAxis />
        <Tooltip />
        <Line dataKey="value" stroke={colors[0]} />
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
        onClick={(state) => {
          const active = state?.activeLabel;
          if (!onDayClick || !active) return;
          const match = data.find((d) => d.name === String(active));
          if (match?.date) onDayClick(match.date);
        }}
      >
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="name" fontSize={12} />
        <YAxis fontSize={12} />
        <Tooltip />
        <Legend />
        <Line
          type="monotone"
          dataKey="income"
          name="Income"
          stroke="#1c9c70"
          strokeWidth={2}
          dot={false}
        />
        <Line
          type="monotone"
          dataKey="expenses"
          name="Expenses"
          stroke="#e45c50"
          strokeWidth={2}
          dot={false}
        />
      </LineChart>
    </Frame>
  );
}
export function BarChartView({ data, label = "Bar chart" }: Props) {
  return (
    <Frame label={label}>
      <BarChart data={data}>
        <CartesianGrid strokeDasharray="3 3" />
        <XAxis dataKey="name" fontSize={12} />
        <YAxis fontSize={12} />
        <Tooltip />
        <Bar dataKey="value" fill={colors[0]} radius={[4, 4, 0, 0]} />
      </BarChart>
    </Frame>
  );
}
export function AreaChartView({ data, label = "Area chart" }: Props) {
  return (
    <Frame label={label}>
      <AreaChart data={data}>
        <XAxis dataKey="name" />
        <YAxis />
        <Tooltip />
        <Area dataKey="value" stroke={colors[0]} fill={colors[0]} fillOpacity={0.2} />
      </AreaChart>
    </Frame>
  );
}
export function PieChartView({ data, label = "Pie chart" }: Props) {
  return (
    <Frame label={label}>
      <PieChart>
        <Pie data={data} dataKey="value" nameKey="name" outerRadius="80%">
          {data.map((item, index) => (
            <Cell key={item.name} fill={colors[index % colors.length]} />
          ))}
        </Pie>
        <Tooltip />
      </PieChart>
    </Frame>
  );
}
