import { LineChart, Line, XAxis, YAxis, CartesianGrid, Tooltip, Legend, ResponsiveContainer } from 'recharts'
import type { ChartSpec, ChartSeries } from '@/hooks/usePresenceSession'
import { ResultStage } from './ResultStage'

const CHART_COLORS = ['#f59e0b', '#93c5fd', '#a78bfa', '#86efac', '#fca5a5', '#fcd34d']

function yAxisDomain(spec: ChartSpec): [number, number] | undefined {
  const values: number[] = []
  for (const s of spec.series) for (const p of s.points) values.push(p.y)
  if (!values.length) return undefined
  const dataMin = Math.min(...values), dataMax = Math.max(...values)
  const pad = Math.max((dataMax - dataMin) * 0.05, Math.abs(dataMax) * 0.001, 0.5)
  if (spec.y_zero_based) return [0, spec.y_max ?? dataMax + pad]
  return [spec.y_min ?? dataMin - pad, spec.y_max ?? dataMax + pad]
}

function mergeSeries(series: ChartSeries[]): Array<Record<string, string | number>> {
  const rows = new Map<string, Record<string, string | number>>()
  for (const s of series) for (const p of s.points) {
    const row = rows.get(p.x) ?? { x: p.x }
    row[s.name] = p.y
    rows.set(p.x, row)
  }
  return Array.from(rows.values())
}

interface Props {
  spec: ChartSpec | null
  onDismiss: () => void
  stackOffset?: number
}

// FloatingChart presents a live recharts LineChart inside a centered
// result stage. The spec is the assistant-pushed payload from the
// render_chart tool; dismissing clears the presence-session store.
export default function FloatingChart({ spec, onDismiss, stackOffset }: Props) {
  const isOpen = !!spec
  const data = spec ? mergeSeries(spec.series) : []

  return (
    <ResultStage
      title={spec ? `chart · ${spec.title.toLowerCase()}` : 'chart'}
      isOpen={isOpen}
      onClose={onDismiss}
      width={480}
      maxHeight="60vh"
      stackOffset={stackOffset}
    >
      {spec && (
        <div className="p-3" style={{ height: 320 }}>
          <ResponsiveContainer width="100%" height="100%">
            <LineChart data={data} margin={{ top: 8, right: 16, bottom: 8, left: 8 }}>
              <CartesianGrid stroke="rgba(255,255,255,0.05)" strokeDasharray="3 3" />
              <XAxis dataKey="x" stroke="#5a5d66" fontSize={10} tickLine={false}
                     axisLine={{ stroke: 'rgba(255,255,255,0.1)' }}
                     label={spec.x_label ? { value: spec.x_label, position: 'insideBottom', fill: '#5a5d66', fontSize: 10 } : undefined} />
              <YAxis stroke="#5a5d66" fontSize={10} tickLine={false}
                     axisLine={{ stroke: 'rgba(255,255,255,0.1)' }}
                     domain={yAxisDomain(spec) as [number, number] | undefined}
                     label={spec.y_label ? { value: spec.y_label, angle: -90, position: 'insideLeft', fill: '#5a5d66', fontSize: 10 } : undefined} />
              <Tooltip contentStyle={{ background: '#1a1b1e', border: '1px solid rgba(255,255,255,0.08)', borderRadius: 8, fontSize: 12 }}
                       labelStyle={{ color: '#c8cad0' }} />
              {spec.series.length > 1 && <Legend wrapperStyle={{ fontSize: 11, color: '#c8cad0' }} />}
              {spec.series.map((s, idx) => (
                <Line key={s.name} type="monotone" dataKey={s.name}
                      stroke={CHART_COLORS[idx % CHART_COLORS.length]}
                      strokeWidth={2} dot={false} isAnimationActive={false} />
              ))}
            </LineChart>
          </ResponsiveContainer>
        </div>
      )}
    </ResultStage>
  )
}
