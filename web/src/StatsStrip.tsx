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
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-4">
        <Tile label="queue depth" value={stats?.ready.toString()} dimmed={dimmed} />
        <Tile label="detectors" value={stats?.consumers.toString()} dimmed={dimmed} />
        <Tile label="events/s in" value={stats?.publish_rate.toFixed(1)} dimmed={dimmed} />
        <Tile label="events/s out" value={stats?.ack_rate.toFixed(1)} dimmed={dimmed} />
      </div>
      {/* Always one line, so the page does not jump when the state changes. */}
      {dimmed ? (
        <p
          role="status"
          className="mt-2 flex items-center gap-2 text-[11px] leading-4 font-medium uppercase tracking-[0.1em] text-warn before:size-1.5 before:flex-none before:bg-current before:content-['']"
        >
          statistics unavailable ({failure}){stats && '; showing the last values'}
        </p>
      ) : (
        <p
          role="status"
          className={`mt-2 flex items-center gap-2 text-[11px] leading-4 font-medium uppercase tracking-[0.1em] ${stats ? 'text-ok' : 'text-muted'} before:size-1.5 before:flex-none before:bg-current before:content-['']`}
        >
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
      className="border-t border-muted/50 bg-panel px-4 pt-3 pb-3.5 transition-opacity"
    >
      <div id={labelId} className="text-[11px] leading-4 font-medium uppercase tracking-[0.1em] text-muted">
        {label}
      </div>
      <div className="mt-2 text-[40px] leading-[44px] font-medium tracking-[-0.02em] text-ink">{value ?? '-'}</div>
    </div>
  );
}
