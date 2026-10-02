import type { FindingFilters } from './api';

const severities = ['MEDIUM', 'HIGH', 'CRITICAL'];
const rules = ['secret_file_read', 'pipe_to_shell', 'unknown_domain', 'exfiltration'];

interface Props {
  filters: FindingFilters;
  customers: string[];
  onChange: (filters: FindingFilters) => void;
}

export function Filters({ filters, customers, onChange }: Props) {
  // A selected customer stays selectable when it drops out of the API's list
  // (its findings were removed); otherwise the control would read "all" while
  // the filter is still applied.
  const customerOptions =
    filters.customer && !customers.includes(filters.customer) ? [...customers, filters.customer] : customers;

  return (
    <div className="flex flex-wrap gap-x-5 gap-y-2 text-[11px] leading-4">
      <Select
        label="severity"
        value={filters.severity}
        options={severities}
        onChange={(severity) => onChange({ ...filters, severity })}
      />
      <Select
        label="customer"
        value={filters.customer}
        options={customerOptions}
        onChange={(customer) => onChange({ ...filters, customer })}
      />
      <Select label="rule" value={filters.rule} options={rules} onChange={(rule) => onChange({ ...filters, rule })} />
    </div>
  );
}

interface SelectProps {
  label: string;
  value: string;
  options: string[];
  onChange: (value: string) => void;
}

function Select({ label, value, options, onChange }: SelectProps) {
  return (
    <label className="flex items-center gap-2 font-medium uppercase tracking-[0.1em] text-muted">
      {label}
      <select
        value={value}
        onChange={(event) => onChange(event.target.value)}
        className="rounded-none border border-muted/50 bg-panel px-2 py-[3px] text-[12px] leading-5 font-normal normal-case tracking-normal text-ink focus-visible:outline focus-visible:outline-1 focus-visible:outline-offset-2 focus-visible:outline-ink"
      >
        <option value="">all</option>
        {options.map((option) => (
          <option key={option} value={option}>
            {option}
          </option>
        ))}
      </select>
    </label>
  );
}
