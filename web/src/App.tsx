import { keepPreviousData, QueryClient, QueryClientProvider, useQuery } from '@tanstack/react-query';
import { useState } from 'react';
import { fetchCustomers, fetchFindings, fetchStats, POLL_MS } from './api';
import type { FindingFilters } from './api';
import { Filters } from './Filters';
import { FindingsTable } from './FindingsTable';
import { useHistory } from './history';
import { HistoryChart } from './HistoryChart';
import { StatsStrip } from './StatsStrip';

export function App() {
  // Every query is polled. Retries are off because the next poll is the
  // retry. Polling continues on a hidden tab because the chart history exists
  // only in this page, and while the browser reports itself offline because
  // the API is on this host or behind the page's own server: only a request
  // can tell whether it answers.
  const [queryClient] = useState(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            refetchInterval: POLL_MS,
            refetchIntervalInBackground: true,
            retry: false,
            networkMode: 'always',
          },
        },
      }),
  );
  return (
    <QueryClientProvider client={queryClient}>
      <Console />
    </QueryClientProvider>
  );
}

interface Polled {
  error: Error | null;
  errorUpdatedAt: number;
  dataUpdatedAt: number;
}

// Why the latest answered request of a polled query failed, or undefined when
// it succeeded. The library's own error is not used as it stands: it is
// cleared whenever a query that has no data yet starts its next request, so an
// API that is down from the start would read as failed and as merely waiting
// in turns, on every poll. The two update times keep their values through
// that, and the message is held here.
function useFailure(query: Polled): string | undefined {
  const [held, setHeld] = useState('');
  const message = query.error?.message;
  if (message !== undefined && message !== held) setHeld(message);
  return query.errorUpdatedAt > query.dataUpdatedAt ? (message ?? held) : undefined;
}

function Console() {
  const stats = useQuery({ queryKey: ['stats'], queryFn: fetchStats });
  const statsFailure = useFailure(stats);
  // When statistics were last asked for and answered, successfully or not.
  const statsAskedAt = Math.max(stats.dataUpdatedAt, stats.errorUpdatedAt);
  const history = useHistory(stats.data, stats.dataUpdatedAt, statsAskedAt);

  const [filters, setFilters] = useState<FindingFilters>({ severity: '', customer: '', rule: '' });
  // The previous rows stay up while a changed filter is on its way, so the
  // table does not flash empty.
  const findings = useQuery({
    queryKey: ['findings', filters],
    queryFn: () => fetchFindings(filters),
    placeholderData: keepPreviousData,
  });
  const findingsFailure = useFailure(findings);
  // Once the changed filter has failed, the previous rows are no longer a
  // stand-in for an answer on its way: they would sit under a selection they
  // do not match, so the table is left empty under the failure line.
  const rows = findingsFailure !== undefined && findings.isPlaceholderData ? [] : (findings.data ?? []);
  const customers = useQuery({ queryKey: ['customers'], queryFn: fetchCustomers });

  return (
    <main className="mx-auto max-w-[100rem] px-4 py-6">
      <header className="flex flex-wrap items-baseline justify-between gap-2">
        <h1 className="text-lg text-ok">AgentShield console</h1>
        <p className="text-xs text-muted">refreshed every {POLL_MS / 1000} s, times in UTC</p>
      </header>

      <StatsStrip stats={stats.data} failure={statsFailure} />
      <HistoryChart samples={history} now={statsAskedAt} />

      <section aria-label="findings" className="mt-6">
        <div className="flex flex-wrap items-baseline justify-between gap-3">
          <h2 className="text-muted">findings, newest first</h2>
          <Filters filters={filters} customers={customers.data ?? []} onChange={setFilters} />
        </div>
        {findingsFailure !== undefined && (
          <p role="status" className="mt-2 text-sm text-warn">
            findings unavailable ({findingsFailure})
          </p>
        )}
        <div className={`mt-2 overflow-x-auto ${findings.isPlaceholderData ? 'opacity-60' : ''}`}>
          <FindingsTable findings={rows} />
        </div>
        {findings.data?.length === 0 && <p className="px-3 py-4 text-sm text-dim">no findings</p>}
      </section>
    </main>
  );
}
