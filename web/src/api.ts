// The console's only view of the pipeline: the read-only API, reached under
// the /api prefix that nginx (or the dev server) strips.

// How often statistics, findings and customers are asked for again.
export const POLL_MS = 2000;

export interface Stats {
  ready: number;
  unacked: number;
  publish_rate: number;
  ack_rate: number;
  consumers: number;
}

// What an exfiltration finding was correlated from.
export interface Evidence {
  read_event_id: string;
  read_ts: string;
  read_path: string;
  read_detector_id: string;
  request_detector_id: string;
  raised_by: string; // "read" or "request": the event that was processed second
}

export interface Finding {
  id: number;
  event_id: string;
  customer_id: string;
  agent_id: string;
  rule: string;
  severity: string;
  ts: string;
  detail: string;
  llm_verdict: string | null; // "malicious: <reason>" or "benign: <reason>"
  verdict_source: string | null; // gemini or mock; null while triage is pending
  triaged_at: string | null;
  // Both are null on rows stored before the columns existed.
  detector_id: string | null;
  evidence: Evidence | null;
}

async function get<T>(path: string): Promise<T> {
  const response = await fetch(path);
  if (!response.ok) {
    // GET /stats describes its failure in a JSON error field; the other
    // routes answer in plain text, which says nothing the status does not.
    const body: unknown = await response.json().catch(() => null);
    const described = typeof body === 'object' && body !== null && 'error' in body && typeof body.error === 'string';
    throw new Error(described ? (body.error as string) : `HTTP ${response.status}`);
  }
  return (await response.json()) as T;
}

// An empty value leaves that filter unset.
export interface FindingFilters {
  severity: string;
  customer: string;
  rule: string;
}

// The API applies the filters; the console shows the rows it returns.
export function fetchFindings(filters: FindingFilters): Promise<Finding[]> {
  const params = new URLSearchParams();
  for (const [name, value] of Object.entries(filters)) {
    if (value) params.set(name, value);
  }
  const query = params.toString();
  return get<Finding[]>(query ? `/api/findings?${query}` : '/api/findings');
}

// The customer ids that have findings, sorted ascending.
export function fetchCustomers(): Promise<string[]> {
  return get<string[]>('/api/customers');
}

export function fetchStats(): Promise<Stats> {
  return get<Stats>('/api/stats');
}
