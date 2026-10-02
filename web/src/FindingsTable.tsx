import { useState } from 'react';
import type { ReactNode } from 'react';
import type { Finding } from './api';
import { formatTime } from './format';

type Tone = 'ok' | 'bad' | 'warn' | 'dim';

const toneClass: Record<Tone, string> = {
  ok: 'border-ok/55 text-ok',
  bad: 'border-bad bg-bad font-bold text-surface',
  warn: 'border-warn/55 text-warn',
  dim: 'border-dim/60 text-dim',
};

const chipClass = 'block border px-1.5 text-[11px] leading-[18px] uppercase tracking-[0.06em] whitespace-nowrap';

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
    <table className="w-full border-collapse border-b border-muted/50 text-left text-[12px] leading-5">
      <thead>
        {/* No rule of its own under the head: the first row's top rule is
            that rule, and without rows the table's closing rule shows. */}
        <tr className="border-t border-muted/50 bg-panel text-[11px] leading-4 uppercase tracking-[0.1em] text-muted">
          <td />
          {columns.map((column) => (
            <th key={column} scope="col" className="px-2.5 py-2 font-medium whitespace-nowrap">
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
        <td className="py-1.5 pl-2.5">
          {hasDetails && (
            <button
              type="button"
              aria-expanded={expanded}
              aria-label={expanded ? 'hide details' : 'show details'}
              onClick={() => setExpanded(!expanded)}
              className="cursor-pointer font-medium text-muted hover:text-ink aria-expanded:text-ink focus-visible:outline focus-visible:outline-1 focus-visible:outline-offset-2 focus-visible:outline-ink"
            >
              {expanded ? '[-]' : '[+]'}
            </button>
          )}
        </td>
        <td className="px-2.5 py-1.5 whitespace-nowrap">{formatTime(finding.ts)}</td>
        <td className="w-[1%] px-2.5 py-1.5">
          <span className={`${chipClass} ${toneClass[severityTone[finding.severity] ?? 'dim']}`}>
            {finding.severity}
          </span>
        </td>
        <td className="px-2.5 py-1.5 whitespace-nowrap">{finding.customer_id}</td>
        <td className="px-2.5 py-1.5 whitespace-nowrap">{finding.agent_id}</td>
        <td className="px-2.5 py-1.5 font-medium whitespace-nowrap">{finding.rule}</td>
        <td className="min-w-72 px-2.5 py-1.5 wrap-anywhere">{finding.detail}</td>
        <td className="px-2.5 py-1.5 whitespace-nowrap text-muted">
          {finding.detector_id ?? '-'}
          {crossReplica && (
            <div className="mt-0.5">
              <span
                title="the secret read and the request were handled by different detectors"
                className="inline-block border border-series px-[5px] text-[11px] leading-4 uppercase tracking-[0.06em] text-ink"
              >
                cross-replica
              </span>
            </div>
          )}
        </td>
        <td className="w-[1%] px-2.5 py-1.5">
          <span className={`${chipClass} ${toneClass[triage.tone]}`}>{triage.label}</span>
        </td>
      </tr>
      {expanded && hasDetails && (
        <tr>
          <td />
          <td colSpan={columns.length} className="px-2.5 pt-0.5 pb-3.5">
            <dl className="grid grid-cols-[max-content_1fr] items-baseline gap-x-6 gap-y-0 border-l border-muted/50 pl-3.5">
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
      <dt className="text-[11px] uppercase tracking-[0.08em] text-muted">{label}</dt>
      <dd className="wrap-anywhere">{children}</dd>
    </>
  );
}
