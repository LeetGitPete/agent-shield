import { useState } from 'react';
import type { ReactNode } from 'react';
import type { Finding } from './api';
import { formatTime } from './format';

type Tone = 'ok' | 'bad' | 'warn' | 'dim';

const toneClass: Record<Tone, string> = {
  ok: 'text-ok',
  bad: 'text-bad',
  warn: 'text-warn',
  dim: 'text-dim',
};

const severityTone: Record<string, Tone> = { CRITICAL: 'bad', HIGH: 'bad', MEDIUM: 'warn' };

// The order matters: a verdict wins over its source, and a source without a
// verdict tells the mock provider from a Gemini call that gave up.
function triageState(finding: Finding): { label: string; tone: Tone } {
  if (finding.llm_verdict) {
    const word = finding.llm_verdict.split(':', 1)[0]?.trim().toLowerCase();
    if (word === 'malicious') return { label: word, tone: 'bad' };
    if (word === 'benign') return { label: word, tone: 'ok' };
    // Triage writes nothing else. A text in another form is not squeezed into
    // the cell; the expanded row shows it in full.
    return { label: 'verdict', tone: 'dim' };
  }
  if (finding.verdict_source === 'mock') return { label: 'mock', tone: 'dim' };
  if (finding.verdict_source === 'gemini') return { label: 'no verdict', tone: 'dim' };
  return { label: 'pending', tone: 'warn' };
}

const columns = ['time (UTC)', 'severity', 'customer', 'agent', 'rule', 'detail', 'detector', 'triage'];

export function FindingsTable({ findings }: { findings: Finding[] }) {
  return (
    <table className="w-full border-collapse text-left text-sm">
      <thead>
        <tr className="border-b border-line text-muted">
          <td />
          {columns.map((column) => (
            <th key={column} scope="col" className="px-3 py-2 font-normal">
              {column}
            </th>
          ))}
        </tr>
      </thead>
      <tbody>
        {findings.map((finding) => (
          <FindingRow key={finding.id} finding={finding} />
        ))}
      </tbody>
    </table>
  );
}

function FindingRow({ finding }: { finding: Finding }) {
  const [expanded, setExpanded] = useState(false);
  const triage = triageState(finding);
  const evidence = finding.rule === 'exfiltration' ? finding.evidence : null;
  const hasDetails = Boolean(finding.llm_verdict) || evidence !== null;
  const crossReplica = evidence !== null && evidence.read_detector_id !== evidence.request_detector_id;

  return (
    <>
      <tr className="border-t border-line align-top">
        <td className="py-2 pl-3">
          {hasDetails && (
            <button
              type="button"
              aria-expanded={expanded}
              aria-label={expanded ? 'hide details' : 'show details'}
              onClick={() => setExpanded(!expanded)}
              className="cursor-pointer text-muted hover:text-ink"
            >
              {expanded ? '[-]' : '[+]'}
            </button>
          )}
        </td>
        <td className="px-3 py-2 whitespace-nowrap">{formatTime(finding.ts)}</td>
        <td className={`px-3 py-2 ${toneClass[severityTone[finding.severity] ?? 'dim']}`}>{finding.severity}</td>
        <td className="px-3 py-2 whitespace-nowrap">{finding.customer_id}</td>
        <td className="px-3 py-2 whitespace-nowrap">{finding.agent_id}</td>
        <td className="px-3 py-2 whitespace-nowrap">{finding.rule}</td>
        <td className="min-w-72 px-3 py-2 wrap-anywhere">{finding.detail}</td>
        <td className="px-3 py-2 whitespace-nowrap">
          {finding.detector_id ?? '-'}
          {crossReplica && (
            <div>
              <span
                title="the secret read and the request were handled by different detectors"
                className="border border-series px-1 text-xs text-ink"
              >
                cross-replica
              </span>
            </div>
          )}
        </td>
        <td className={`px-3 py-2 whitespace-nowrap ${toneClass[triage.tone]}`}>{triage.label}</td>
      </tr>
      {expanded && hasDetails && (
        <tr>
          <td />
          <td colSpan={columns.length} className="px-3 pb-3">
            <dl className="grid grid-cols-[max-content_1fr] gap-x-4 gap-y-1 border-l border-line pl-3">
              <Field label="verdict">{finding.llm_verdict ?? '-'}</Field>
              <Field label="verdict source">{finding.verdict_source ?? '-'}</Field>
              <Field label="triaged at">{finding.triaged_at ? formatTime(finding.triaged_at) : '-'}</Field>
              {finding.rule === 'exfiltration' &&
                (evidence ? (
                  <>
                    <Field label="secret read event">{evidence.read_event_id}</Field>
                    <Field label="secret read time">{formatTime(evidence.read_ts)}</Field>
                    <Field label="secret read path">{evidence.read_path}</Field>
                    <Field label="read handled by">{evidence.read_detector_id}</Field>
                    <Field label="request handled by">{evidence.request_detector_id}</Field>
                    <Field label="raised by">{evidence.raised_by}</Field>
                  </>
                ) : (
                  <Field label="evidence">-</Field>
                ))}
            </dl>
          </td>
        </tr>
      )}
    </>
  );
}

function Field({ label, children }: { label: string; children: ReactNode }) {
  return (
    <>
      <dt className="text-muted">{label}</dt>
      <dd className="wrap-anywhere">{children}</dd>
    </>
  );
}
