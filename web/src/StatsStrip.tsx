import { useId } from 'react';
import type { Stats } from './api';

interface Props {
  stats: Stats | undefined; // the last successful response, kept through failures
  failure: string | undefined; // why the latest answered request failed; undefined when it succeeded
}

export function StatsStrip({ stats, failure }: Props) {
  const dimmed = failure !== undefined;
  return (
    <section aria-label="pipeline statistics" className="mt-4">
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <Tile label="queue depth" value={stats?.ready.toString()} dimmed={dimmed} />
        <Tile label="detectors" value={stats?.consumers.toString()} dimmed={dimmed} />
        <Tile label="events/s in" value={stats?.publish_rate.toFixed(1)} dimmed={dimmed} />
        <Tile label="events/s out" value={stats?.ack_rate.toFixed(1)} dimmed={dimmed} />
      </div>
      {/* Always one line, so the page does not jump when the state changes. */}
      {dimmed ? (
        <p role="status" className="mt-2 text-sm text-warn">
          statistics unavailable ({failure}){stats && '; showing the last values'}
        </p>
      ) : (
        <p role="status" className={`mt-2 text-sm ${stats ? 'text-ok' : 'text-muted'}`}>
          {stats ? 'statistics live' : 'waiting for statistics'}
        </p>
      )}
    </section>
  );
}

function Tile({ label, value, dimmed }: { label: string; value: string | undefined; dimmed: boolean }) {
  const labelId = useId();
  return (
    // The dimming is an inline style so that it holds whatever the stylesheet does.
    <div
      role="group"
      aria-labelledby={labelId}
      style={{ opacity: dimmed ? 0.4 : 1 }}
      className="border border-line bg-panel px-4 py-3 transition-opacity"
    >
      <div id={labelId} className="text-sm text-muted">
        {label}
      </div>
      <div className="mt-1 text-3xl text-ok">{value ?? '-'}</div>
    </div>
  );
}
