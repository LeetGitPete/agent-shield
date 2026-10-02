// Times are shown in UTC, the zone the services log in, so a row can be
// matched against a log line without converting.
export function formatTime(value: string | number): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return String(value);
  return date.toISOString().slice(0, 19).replace('T', ' ');
}

// The hour and minute of the layout above, for the chart's time axis.
export function formatHourMinute(value: string | number): string {
  return formatTime(value).slice(11, 16);
}
