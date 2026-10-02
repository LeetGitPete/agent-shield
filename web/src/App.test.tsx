import { act, fireEvent, render, screen, within } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { expect, it, vi } from 'vitest';
import { App } from './App';
import type { Finding, Stats } from './api';

// The seam is the rendered page against a stubbed API: fetch is replaced,
// everything from the request to the DOM is the real application.

interface ApiStub {
  stats?: () => Response | Promise<Response>;
  // Rows are answered as JSON; a Response stands for a failure or for a
  // request that is still under way.
  findings?: (search: string) => Finding[] | Response | Promise<Response>;
  customers?: () => string[];
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

const steadyStats: Stats = { ready: 42, unacked: 3, publish_rate: 118.5, ack_rate: 97.2, consumers: 3 };

// Returns the query strings of the findings requests, in the order they arrived.
function stubApi(stub: ApiStub = {}): string[] {
  const findingsSearches: string[] = [];
  vi.stubGlobal('fetch', async (input: RequestInfo | URL) => {
    const url = new URL(String(input), 'http://console.test');
    switch (url.pathname) {
      case '/api/stats':
        return stub.stats ? stub.stats() : json(steadyStats);
      case '/api/findings': {
        findingsSearches.push(url.search);
        const answer = stub.findings ? stub.findings(url.search) : [];
        return Array.isArray(answer) ? json(answer) : answer;
      }
      case '/api/customers':
        return json(stub.customers ? stub.customers() : []);
      default:
        return new Response('not found', { status: 404 });
    }
  });
  return findingsSearches;
}

function finding(overrides: Partial<Finding> & Pick<Finding, 'id' | 'agent_id'>): Finding {
  return {
    event_id: `evt-${overrides.id}`,
    customer_id: 'customer_1',
    rule: 'secret_file_read',
    severity: 'HIGH',
    ts: '2026-10-02T14:03:07Z',
    detail: 'agent read secret file /home/dev/.ssh/id_rsa',
    llm_verdict: null,
    verdict_source: null,
    triaged_at: null,
    detector_id: 'detector-6d5f7-k2x8q',
    evidence: null,
    ...overrides,
  };
}

async function rowOf(agent: string): Promise<HTMLElement> {
  const row = (await screen.findByText(agent)).closest('tr');
  if (!row) throw new Error(`no table row for ${agent}`);
  return row;
}

// The value shown next to a label in an expanded row.
function field(label: string): Element | null {
  return screen.getByText(label, { selector: 'dt' }).nextElementSibling;
}

it('shows the eight columns of each finding, in the order the API returned the rows', async () => {
  stubApi({
    findings: () => [
      finding({ id: 7, agent_id: 'agent_2_4', verdict_source: 'mock', triaged_at: '2026-10-02T14:03:08Z' }),
      finding({
        id: 6,
        agent_id: 'agent_3_1',
        customer_id: 'customer_3',
        rule: 'pipe_to_shell',
        severity: 'CRITICAL',
        ts: '2026-10-02T14:02:59Z',
        detail: 'agent piped a download into a shell',
        detector_id: 'detector-6d5f7-p9m3z',
      }),
      // An id out of sequence: the page keeps the API's order and does not sort.
      finding({ id: 9, agent_id: 'agent_1_8' }),
    ],
  });

  render(<App />);
  await rowOf('agent_2_4');

  const [header, ...rows] = screen.getAllByRole('row');
  if (!header) throw new Error('the table has no header row');
  expect(
    within(header)
      .getAllByRole('columnheader')
      .map((cell) => cell.textContent),
  ).toEqual(['time (UTC)', 'severity', 'customer', 'agent', 'rule', 'detail', 'detector', 'triage']);
  // The first cell of a row holds the control that expands it.
  const cells = rows.map((row) =>
    within(row)
      .getAllByRole('cell')
      .slice(1)
      .map((cell) => cell.textContent),
  );
  expect(cells).toEqual([
    [
      '2026-10-02 14:03:07',
      'HIGH',
      'customer_1',
      'agent_2_4',
      'secret_file_read',
      'agent read secret file /home/dev/.ssh/id_rsa',
      'detector-6d5f7-k2x8q',
      'mock',
    ],
    [
      '2026-10-02 14:02:59',
      'CRITICAL',
      'customer_3',
      'agent_3_1',
      'pipe_to_shell',
      'agent piped a download into a shell',
      'detector-6d5f7-p9m3z',
      'pending',
    ],
    [
      '2026-10-02 14:03:07',
      'HIGH',
      'customer_1',
      'agent_1_8',
      'secret_file_read',
      'agent read secret file /home/dev/.ssh/id_rsa',
      'detector-6d5f7-k2x8q',
      'pending',
    ],
  ]);
});

it('shows each of the four triage states', async () => {
  stubApi({
    findings: () => [
      finding({
        id: 5,
        agent_id: 'agent_1_1',
        llm_verdict: 'malicious: the agent read a private key it had no task for',
        verdict_source: 'gemini',
        triaged_at: '2026-10-02T14:03:09Z',
      }),
      finding({
        id: 4,
        agent_id: 'agent_1_2',
        llm_verdict: 'benign: the path is a test fixture',
        // Not a row the pipeline produces: it pins that a verdict is looked
        // at before its source.
        verdict_source: 'mock',
        triaged_at: '2026-10-02T14:03:09Z',
      }),
      finding({ id: 3, agent_id: 'agent_1_3', verdict_source: 'mock', triaged_at: '2026-10-02T14:03:08Z' }),
      finding({ id: 2, agent_id: 'agent_1_4', verdict_source: 'gemini', triaged_at: '2026-10-02T14:03:20Z' }),
      finding({ id: 1, agent_id: 'agent_1_5' }),
    ],
  });

  render(<App />);

  expect(within(await rowOf('agent_1_1')).getByText('malicious')).toBeInTheDocument();
  expect(within(await rowOf('agent_1_2')).getByText('benign')).toBeInTheDocument();
  expect(within(await rowOf('agent_1_3')).getByText('mock')).toBeInTheDocument();
  expect(within(await rowOf('agent_1_4')).getByText('no verdict')).toBeInTheDocument();
  expect(within(await rowOf('agent_1_5')).getByText('pending')).toBeInTheDocument();
});

it('expands a row with a verdict to show the full verdict text', async () => {
  stubApi({
    findings: () => [
      finding({
        id: 5,
        agent_id: 'agent_1_1',
        llm_verdict: 'malicious: the agent read a private key it had no task for',
        verdict_source: 'gemini',
        triaged_at: '2026-10-02T14:03:09Z',
      }),
      finding({ id: 1, agent_id: 'agent_1_5' }),
    ],
  });
  const user = userEvent.setup();

  render(<App />);
  const row = await rowOf('agent_1_1');
  expect(screen.queryByText('malicious: the agent read a private key it had no task for')).not.toBeInTheDocument();

  await user.click(within(row).getByRole('button', { name: 'show details' }));

  expect(screen.getByText('malicious: the agent read a private key it had no task for')).toBeInTheDocument();
  expect(screen.getByText('gemini')).toBeInTheDocument();
  expect(screen.getByText('2026-10-02 14:03:09')).toBeInTheDocument();

  await user.click(within(row).getByRole('button', { name: 'hide details' }));
  expect(screen.queryByText('malicious: the agent read a private key it had no task for')).not.toBeInTheDocument();

  // A row with neither a verdict nor evidence has nothing to expand.
  expect(within(await rowOf('agent_1_5')).queryByRole('button')).not.toBeInTheDocument();
});

it('keeps a verdict that starts with neither word out of the triage cell', async () => {
  stubApi({
    findings: () => [
      finding({
        id: 3,
        agent_id: 'agent_2_2',
        llm_verdict: 'the read may have been part of the task the agent was given',
        verdict_source: 'gemini',
        triaged_at: '2026-10-02T14:03:09Z',
      }),
    ],
  });
  const user = userEvent.setup();
  render(<App />);

  const row = await rowOf('agent_2_2');
  expect(within(row).getAllByRole('cell').at(-1)).toHaveTextContent(/^verdict$/);

  await user.click(within(row).getByRole('button', { name: 'show details' }));
  expect(field('verdict')).toHaveTextContent('the read may have been part of the task the agent was given');
});

it('expands an exfiltration row to show its evidence, with or without a verdict', async () => {
  stubApi({
    findings: () => [
      finding({
        id: 9,
        agent_id: 'attack_k3f_1',
        rule: 'exfiltration',
        severity: 'CRITICAL',
        detail: 'agent made a web request to https://paste.example/upload 2s after reading secret file /home/dev/.aws/credentials',
        detector_id: 'detector-6d5f7-p9m3z',
        evidence: {
          read_event_id: 'evt-read-91',
          read_ts: '2026-10-02T14:02:58Z',
          read_path: '/home/dev/.aws/credentials',
          read_detector_id: 'detector-6d5f7-k2x8q',
          request_detector_id: 'detector-6d5f7-p9m3z',
          raised_by: 'request',
        },
      }),
      finding({
        id: 8,
        agent_id: 'attack_k3f_2',
        rule: 'exfiltration',
        severity: 'CRITICAL',
        detail: 'agent made a web request to https://paste.example/upload 2s after reading secret file /srv/app/.env',
        llm_verdict: 'malicious: a secret was read and then sent to an unknown host',
        verdict_source: 'gemini',
        triaged_at: '2026-10-02T14:03:11Z',
        evidence: {
          read_event_id: 'evt-read-77',
          read_ts: '2026-10-02T14:02:41Z',
          read_path: '/srv/app/.env',
          read_detector_id: 'detector-6d5f7-k2x8q',
          request_detector_id: 'detector-6d5f7-k2x8q',
          raised_by: 'read',
        },
      }),
    ],
  });
  const user = userEvent.setup();
  render(<App />);

  const pending = await rowOf('attack_k3f_1');
  await user.click(within(pending).getByRole('button', { name: 'show details' }));

  expect(field('secret read event')).toHaveTextContent('evt-read-91');
  expect(field('secret read time')).toHaveTextContent('2026-10-02 14:02:58');
  expect(field('secret read path')).toHaveTextContent('/home/dev/.aws/credentials');
  expect(field('read handled by')).toHaveTextContent('detector-6d5f7-k2x8q');
  expect(field('request handled by')).toHaveTextContent('detector-6d5f7-p9m3z');
  expect(field('raised by')).toHaveTextContent(/^request$/);
  expect(field('verdict')).toHaveTextContent(/^-$/);

  await user.click(within(pending).getByRole('button', { name: 'hide details' }));
  await user.click(within(await rowOf('attack_k3f_2')).getByRole('button', { name: 'show details' }));

  expect(field('verdict')).toHaveTextContent('malicious: a secret was read and then sent to an unknown host');
  expect(field('secret read event')).toHaveTextContent('evt-read-77');
  expect(field('raised by')).toHaveTextContent(/^read$/);
});

it('shows a dash for the fields that are null on rows stored before an upgrade', async () => {
  stubApi({
    findings: () => [
      finding({
        id: 2,
        agent_id: 'agent_4_2',
        rule: 'exfiltration',
        llm_verdict: 'benign: the request went to an internal mirror',
        detector_id: null,
      }),
    ],
  });
  const user = userEvent.setup();
  render(<App />);

  const row = await rowOf('agent_4_2');
  expect(within(row).getByText('-')).toBeInTheDocument(); // the detector column

  await user.click(within(row).getByRole('button', { name: 'show details' }));
  expect(field('verdict source')).toHaveTextContent(/^-$/);
  expect(field('triaged at')).toHaveTextContent(/^-$/);
  expect(field('evidence')).toHaveTextContent(/^-$/);
});

it('marks the rows whose read and request were handled by different detectors', async () => {
  const evidence = {
    read_event_id: 'evt-read-91',
    read_ts: '2026-10-02T14:02:58Z',
    read_path: '/home/dev/.aws/credentials',
    read_detector_id: 'detector-6d5f7-k2x8q',
    raised_by: 'request',
  };
  stubApi({
    findings: () => [
      finding({
        id: 9,
        agent_id: 'attack_k3f_1',
        rule: 'exfiltration',
        evidence: { ...evidence, request_detector_id: 'detector-6d5f7-p9m3z' },
      }),
      finding({
        id: 8,
        agent_id: 'attack_k3f_2',
        rule: 'exfiltration',
        evidence: { ...evidence, request_detector_id: 'detector-6d5f7-k2x8q' },
      }),
      finding({ id: 7, agent_id: 'agent_1_3' }),
    ],
  });

  render(<App />);

  expect(within(await rowOf('attack_k3f_1')).getByText('cross-replica')).toBeInTheDocument();
  expect(within(await rowOf('attack_k3f_2')).queryByText('cross-replica')).not.toBeInTheDocument();
  expect(within(await rowOf('agent_1_3')).queryByText('cross-replica')).not.toBeInTheDocument();
});

it.each([
  { filter: 'severity', choice: 'CRITICAL', query: '?severity=CRITICAL' },
  { filter: 'customer', choice: 'customer_2', query: '?customer=customer_2' },
  { filter: 'rule', choice: 'exfiltration', query: '?rule=exfiltration' },
])('sends the $filter filter to the API and shows the rows it returned', async ({ filter, choice, query }) => {
  const searches = stubApi({
    customers: () => ['customer_1', 'customer_2'],
    findings: (search) => {
      if (search === '') {
        return [finding({ id: 4, agent_id: 'agent_unfiltered_a' }), finding({ id: 3, agent_id: 'agent_unfiltered_b' })];
      }
      if (search === query) {
        // The second row would pass none of the three filters: the table shows
        // what the API returned and never filters again in the browser.
        return [
          finding({ id: 2, agent_id: 'agent_returned', severity: 'CRITICAL', customer_id: 'customer_2', rule: 'exfiltration' }),
          finding({ id: 1, agent_id: 'agent_also_returned', severity: 'MEDIUM', customer_id: 'customer_9', rule: 'unknown_domain' }),
        ];
      }
      return [];
    },
  });
  const user = userEvent.setup();
  render(<App />);
  await screen.findByText('agent_unfiltered_a');
  await screen.findByRole('option', { name: choice });

  await user.selectOptions(screen.getByRole('combobox', { name: filter }), choice);

  expect(await screen.findByText('agent_returned')).toBeInTheDocument();
  expect(screen.getByText('agent_also_returned')).toBeInTheDocument();
  expect(screen.queryByText('agent_unfiltered_a')).not.toBeInTheDocument();
  expect(screen.queryByText('agent_unfiltered_b')).not.toBeInTheDocument();
  expect(searches.at(-1)).toBe(query);
});

it('offers the customers returned by the API as the customer filter options', async () => {
  stubApi({ customers: () => ['customer_1', 'customer_7'] });

  render(<App />);
  await screen.findByRole('option', { name: 'customer_7' });

  const options = within(screen.getByRole('combobox', { name: 'customer' })).getAllByRole('option');
  expect(options.map((option) => option.textContent)).toEqual(['all', 'customer_1', 'customer_7']);
});

it('offers the three severities and the four rules as filter options', async () => {
  stubApi();

  render(<App />);
  await screen.findByText('no findings');

  const optionsOf = (filter: string) =>
    within(screen.getByRole('combobox', { name: filter }))
      .getAllByRole('option')
      .map((option) => option.textContent);
  expect(optionsOf('severity')).toEqual(['all', 'MEDIUM', 'HIGH', 'CRITICAL']);
  expect(optionsOf('rule')).toEqual(['all', 'secret_file_read', 'pipe_to_shell', 'unknown_domain', 'exfiltration']);
});

// Under fake timers: moves the clock, then one millisecond more, because the
// query library hands a response to the page on a zero-delay timer and the
// response to a request made on the last tick would otherwise still be waiting.
async function advance(ms: number): Promise<void> {
  await act(async () => {
    await vi.advanceTimersByTimeAsync(ms);
    await vi.advanceTimersByTimeAsync(1);
  });
}

function tile(label: string): HTMLElement {
  return screen.getByRole('group', { name: label });
}

const tileLabels = ['queue depth', 'detectors', 'events/s in', 'events/s out'];

it('keeps the last values and dims the tiles while statistics are unavailable', async () => {
  vi.useFakeTimers();
  let respond = () => json(steadyStats);
  stubApi({ stats: () => respond() });

  render(<App />);
  await advance(0);

  expect(within(tile('queue depth')).getByText('42')).toBeInTheDocument();
  expect(within(tile('detectors')).getByText('3')).toBeInTheDocument();
  expect(within(tile('events/s in')).getByText('118.5')).toBeInTheDocument();
  expect(within(tile('events/s out')).getByText('97.2')).toBeInTheDocument();
  expect(tile('queue depth')).toHaveStyle({ opacity: '1' });
  expect(screen.queryByText(/statistics unavailable/)).not.toBeInTheDocument();

  respond = () => json({ error: 'broker management API is unreachable' }, 503);
  await advance(2000);

  expect(within(tile('queue depth')).getByText('42')).toBeInTheDocument();
  expect(within(tile('detectors')).getByText('3')).toBeInTheDocument();
  expect(within(tile('events/s in')).getByText('118.5')).toBeInTheDocument();
  expect(within(tile('events/s out')).getByText('97.2')).toBeInTheDocument();
  for (const label of tileLabels) {
    expect(tile(label)).toHaveStyle({ opacity: '0.4' });
  }
  expect(
    screen.getByText('statistics unavailable (broker management API is unreachable); showing the last values'),
  ).toBeInTheDocument();

  respond = () => json({ ready: 7, unacked: 0, publish_rate: 12, ack_rate: 30.4, consumers: 4 });
  await advance(2000);

  expect(within(tile('queue depth')).getByText('7')).toBeInTheDocument();
  expect(within(tile('detectors')).getByText('4')).toBeInTheDocument();
  expect(within(tile('events/s in')).getByText('12.0')).toBeInTheDocument();
  expect(within(tile('events/s out')).getByText('30.4')).toBeInTheDocument();
  expect(tile('queue depth')).toHaveStyle({ opacity: '1' });
  expect(screen.queryByText(/statistics unavailable/)).not.toBeInTheDocument();
});

it('stays dimmed, with no values to keep, while statistics have never been available', async () => {
  vi.useFakeTimers();
  let release: (response: Response) => void = () => {};
  const answers: (Response | Promise<Response>)[] = [
    json({ error: 'broker management API answered 404' }, 503),
    new Promise<Response>((resolve) => {
      release = resolve;
    }),
  ];
  stubApi({ stats: () => answers.shift() ?? json(steadyStats) });

  render(<App />);
  await advance(0);

  expect(screen.getByText('statistics unavailable (broker management API answered 404)')).toBeInTheDocument();
  expect(tile('queue depth')).toHaveStyle({ opacity: '0.4' });
  expect(within(tile('queue depth')).getByText('-')).toBeInTheDocument();

  // The next request is under way and not yet answered: nothing has changed
  // for the reader.
  await advance(2000);

  expect(screen.getByText('statistics unavailable (broker management API answered 404)')).toBeInTheDocument();
  expect(screen.queryByText('waiting for statistics')).not.toBeInTheDocument();
  for (const label of tileLabels) {
    expect(tile(label)).toHaveStyle({ opacity: '0.4' });
  }

  release(json(steadyStats));
  await advance(0);

  expect(within(tile('queue depth')).getByText('42')).toBeInTheDocument();
  expect(tile('queue depth')).toHaveStyle({ opacity: '1' });
  expect(screen.getByText('statistics live')).toBeInTheDocument();
});

it('shows a finding stored after the page was opened', async () => {
  vi.useFakeTimers();
  const stored = [finding({ id: 1, agent_id: 'agent_1_1' })];
  stubApi({ findings: () => stored });

  render(<App />);
  await advance(0);
  expect(screen.getByText('agent_1_1')).toBeInTheDocument();
  expect(screen.queryByText('agent_2_6')).not.toBeInTheDocument();

  stored.unshift(finding({ id: 2, agent_id: 'agent_2_6' }));
  await advance(2000);

  expect(screen.getByText('agent_2_6')).toBeInTheDocument();
  expect(screen.getByText('agent_1_1')).toBeInTheDocument();
});

it('shows the failure, not the previous rows, while a changed filter cannot be answered', async () => {
  vi.useFakeTimers();
  const filtered: (Response | Promise<Response>)[] = [new Response('database is down', { status: 500 })];
  stubApi({
    findings: (search) =>
      search === ''
        ? [finding({ id: 4, agent_id: 'agent_unfiltered_a' })]
        : (filtered.shift() ?? new Promise<Response>(() => {})),
  });

  render(<App />);
  await advance(0);
  expect(screen.getByText('agent_unfiltered_a')).toBeInTheDocument();

  fireEvent.change(screen.getByRole('combobox', { name: 'severity' }), { target: { value: 'CRITICAL' } });
  await advance(0);

  expect(screen.getByText('findings unavailable (HTTP 500)')).toBeInTheDocument();
  expect(screen.queryByText('agent_unfiltered_a')).not.toBeInTheDocument();

  // The next request for the same filter is under way and not yet answered.
  await advance(2000);

  expect(screen.getByText('findings unavailable (HTTP 500)')).toBeInTheDocument();
  expect(screen.queryByText('agent_unfiltered_a')).not.toBeInTheDocument();
});

it('keeps a selected customer selectable after it has left the list from the API', async () => {
  vi.useFakeTimers();
  let customers = ['customer_1', 'customer_2'];
  stubApi({ customers: () => customers });

  render(<App />);
  await advance(0);
  fireEvent.change(screen.getByRole('combobox', { name: 'customer' }), { target: { value: 'customer_2' } });

  customers = ['customer_1'];
  await advance(2000);

  expect(screen.getByRole('combobox', { name: 'customer' })).toHaveValue('customer_2');
});

it('appends one history sample per statistics response, 150 over five minutes of polling', async () => {
  vi.useFakeTimers();
  stubApi(); // every response carries the same numbers: a sample is added all the same

  render(<App />);
  await advance(0);
  expect(screen.getByText('samples held: 1 (last 5 minutes)')).toBeInTheDocument();

  await advance(20_000);
  expect(screen.getByText('samples held: 11 (last 5 minutes)')).toBeInTheDocument();

  await advance(400_000);
  expect(screen.getByText('samples held: 150 (last 5 minutes)')).toBeInTheDocument();
});

it('holds at most 150 samples when statistics arrive more often than the polling asks', async () => {
  vi.useFakeTimers();
  stubApi();

  render(<App />);
  await advance(0);

  // Coming back to the page asks for statistics at once, without waiting for
  // the next poll. Once a second for 160 seconds, that alone is 161 responses
  // with the first, all of them inside the five minutes.
  for (let second = 0; second < 160; second++) {
    await advance(1000);
    act(() => {
      window.dispatchEvent(new Event('visibilitychange'));
    });
  }
  await advance(0);

  expect(screen.getByText('samples held: 150 (last 5 minutes)')).toBeInTheDocument();
});

it('drops history samples older than five minutes, also while no sample arrives', async () => {
  vi.useFakeTimers();
  let available = true;
  stubApi({
    stats: () => (available ? json(steadyStats) : json({ error: 'broker management API is unreachable' }, 503)),
  });

  render(<App />);
  await advance(0);
  await advance(10_000);
  expect(screen.getByText('samples held: 6 (last 5 minutes)')).toBeInTheDocument();

  // An outage shorter than the window leaves the six samples inside it.
  available = false;
  await advance(200_000);
  expect(screen.getByText('samples held: 6 (last 5 minutes)')).toBeInTheDocument();

  // 400 seconds in, the samples of the first ten seconds are all older than
  // five minutes.
  await advance(190_000);
  expect(screen.getByText('samples held: 0 (last 5 minutes)')).toBeInTheDocument();

  available = true;
  await advance(2000);
  expect(screen.getByText('samples held: 1 (last 5 minutes)')).toBeInTheDocument();

  await advance(2000);
  expect(screen.getByText('samples held: 2 (last 5 minutes)')).toBeInTheDocument();
});

it('keeps polling on a hidden tab', async () => {
  vi.useFakeTimers();
  stubApi();

  render(<App />);
  await advance(0);
  expect(screen.getByText('samples held: 1 (last 5 minutes)')).toBeInTheDocument();

  vi.spyOn(document, 'visibilityState', 'get').mockReturnValue('hidden');
  await advance(4000);

  expect(screen.getByText('samples held: 3 (last 5 minutes)')).toBeInTheDocument();
});

it('keeps polling while the browser reports that it is offline', async () => {
  vi.useFakeTimers();
  stubApi();

  render(<App />);
  await advance(0);
  expect(screen.getByText('samples held: 1 (last 5 minutes)')).toBeInTheDocument();

  // The API is on this host or behind the server of the page itself, so a
  // lost network link says nothing about whether it answers.
  act(() => {
    window.dispatchEvent(new Event('offline'));
  });
  await advance(4000);

  expect(screen.getByText('samples held: 3 (last 5 minutes)')).toBeInTheDocument();
});
