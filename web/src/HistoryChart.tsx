import { Area, AreaChart, CartesianGrid, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts';
import { POLL_MS } from './api';
import { formatHourMinute, formatTime } from './format';
import { WINDOW_MS } from './history';
import type { Sample } from './history';

// Two samples further apart than this have a sampling gap between them (a
// failed request, or a hidden tab whose timers the browser slowed down). One
// missed poll puts them two intervals apart; a timer that is merely late
// stays well under that, so the threshold sits between the two.
const GAP_MS = 1.75 * POLL_MS;
const MINUTE_MS = 60 * 1000;

interface Point {
  at: number;
  ready: number | null;
  detectors: number | null;
  lone: boolean; // no neighbour on either side, so no line segment would show it
}

// A point without values between two samples makes the line break there
// instead of joining them across the time nothing was sampled.
function withGaps(samples: Sample[]): Point[] {
  const points: Point[] = [];
  samples.forEach((sample, index) => {
    const previous = samples[index - 1];
    const next = samples[index + 1];
    const gapBefore = !previous || sample.at - previous.at > GAP_MS;
    const gapAfter = !next || next.at - sample.at > GAP_MS;
    if (previous && gapBefore) {
      points.push({ at: (previous.at + sample.at) / 2, ready: null, detectors: null, lone: false });
    }
    points.push({ ...sample, lone: gapBefore && gapAfter });
  });
  return points;
}

// The whole minutes inside the window, as tick positions of the time axis.
function minuteTicks(start: number, end: number): number[] {
  const ticks: number[] = [];
  for (let at = Math.ceil(start / MINUTE_MS) * MINUTE_MS; at <= end; at += MINUTE_MS) {
    ticks.push(at);
  }
  return ticks;
}

interface Props {
  samples: Sample[];
  // The time of the latest statistics request, 0 before the first: the right
  // edge of the time axis. It is not the newest sample, so that an outage
  // shows as a gap growing at the right instead of a chart that looks current.
  now: number;
}

export function HistoryChart({ samples, now }: Props) {
  const points = withGaps(samples);
  const end = now > 0 ? now : undefined;

  return (
    <figure aria-label="pipeline history" className="mt-4 border-t border-muted/50 bg-panel px-4 pt-3 pb-2.5">
      {/* Two panels, not two scales on one plot: depth runs to thousands and
          the detector count to a handful, and a shared plot would suggest a
          relation between the two scales that is only an accident of drawing. */}
      <Panel title="queue depth" dataKey="ready" curve="linear" points={points} end={end} />
      <Panel title="detectors" dataKey="detectors" curve="stepAfter" points={points} end={end} timeAxis />
      <figcaption className="mt-1 text-[11px] leading-4 uppercase tracking-[0.08em] text-muted">
        samples held: {samples.length} (last {WINDOW_MS / MINUTE_MS} minutes)
      </figcaption>
    </figure>
  );
}

interface PanelProps {
  title: string;
  dataKey: 'ready' | 'detectors';
  curve: 'linear' | 'stepAfter';
  points: Point[];
  end: number | undefined;
  timeAxis?: boolean; // only the lower panel labels the time axis the two share
}

function Panel({ title, dataKey, curve, points, end, timeAxis = false }: PanelProps) {
  return (
    <div className="not-first:mt-2.5 not-first:border-t not-first:border-line not-first:pt-2.5">
      <div className="text-[11px] leading-4 font-medium uppercase tracking-[0.1em] text-muted">{title}</div>
      <ResponsiveContainer width="100%" height={timeAxis ? 132 : 110}>
        <AreaChart data={points} syncId="pipeline" margin={{ top: 6, right: 20, bottom: 0, left: 0 }}>
          {/* The grid leaves out its line at the foot of the plot: the axis
              line is drawn there, and the dashes would show through it. */}
          <CartesianGrid
            vertical={false}
            horizontal={({ x1, y1, x2, y2, offset }) =>
              y1 === offset.top + offset.height ? (
                <g />
              ) : (
                <line
                  x1={x1}
                  y1={y1}
                  x2={x2}
                  y2={y2}
                  stroke="var(--color-muted)"
                  strokeOpacity={0.4}
                  strokeDasharray="2 4"
                />
              )
            }
          />
          <XAxis
            dataKey="at"
            type="number"
            domain={end === undefined ? ['auto', 'auto'] : [end - WINDOW_MS, end]}
            ticks={end === undefined ? [] : minuteTicks(end - WINDOW_MS, end)}
            tickFormatter={formatHourMinute}
            tick={timeAxis ? { fill: 'var(--color-muted)', fontSize: 11 } : false}
            tickLine={false}
            tickMargin={4}
            axisLine={{ stroke: 'var(--color-muted)', strokeOpacity: 0.5 }}
            height={timeAxis ? 24 : 8}
          />
          <YAxis
            width={52}
            allowDecimals={false}
            tickCount={3}
            interval={0}
            domain={[0, 'auto']}
            tick={{ fill: 'var(--color-muted)', fontSize: 11 }}
            tickLine={false}
            axisLine={false}
          />
          <Tooltip
            isAnimationActive={false}
            cursor={{ stroke: 'var(--color-muted)' }}
            contentStyle={{ background: 'var(--color-surface)', border: '1px solid var(--color-line)', borderRadius: 0 }}
            labelStyle={{ color: 'var(--color-muted)' }}
            itemStyle={{ color: 'var(--color-ink)' }}
            labelFormatter={(at) => `${formatTime(Number(at))} UTC`}
          />
          <Area
            name={title}
            dataKey={dataKey}
            type={curve}
            stroke="var(--color-series)"
            strokeWidth={1.5}
            strokeLinecap="round"
            strokeLinejoin="round"
            fill="var(--color-series)"
            fillOpacity={0.14}
            connectNulls={false}
            isAnimationActive={false}
            activeDot={{ r: 4, stroke: 'var(--color-panel)', strokeWidth: 2 }}
            dot={({ cx, cy, payload, index }: { cx?: number; cy?: number; payload?: Point; index?: number }) =>
              payload?.lone && cx !== undefined && cy !== undefined ? (
                <circle key={index} cx={cx} cy={cy} r={2} fill="var(--color-series)" />
              ) : (
                <g key={index} />
              )
            }
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
