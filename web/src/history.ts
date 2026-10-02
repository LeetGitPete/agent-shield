import { useState } from 'react';
import { POLL_MS } from './api';
import type { Stats } from './api';

export interface Sample {
  at: number; // when the response arrived, in milliseconds since the epoch
  ready: number;
  detectors: number;
}

export const WINDOW_MS = 5 * 60 * 1000;
const MAX_SAMPLES = WINDOW_MS / POLL_MS; // one sample per poll across the window

// The chart's history: the samples of the last five minutes, held only in
// this page. A sample is added for every successful response, recognised by
// its arrival time, because two responses with equal numbers are the same
// object to the query library and would otherwise count once.
//
// The five minutes end at now, the time of the latest request whether it
// succeeded or not, so samples also age out while none arrive. What is kept
// in memory is pruned only when a sample is added, which is enough to bound it.
export function useHistory(stats: Stats | undefined, updatedAt: number, now: number): Sample[] {
  const [samples, setSamples] = useState<Sample[]>([]);
  if (stats && updatedAt !== samples.at(-1)?.at) {
    const next = [...samples, { at: updatedAt, ready: stats.ready, detectors: stats.consumers }];
    setSamples(next.filter((sample) => sample.at > updatedAt - WINDOW_MS).slice(-MAX_SAMPLES));
  }
  return samples.filter((sample) => sample.at > now - WINDOW_MS);
}
