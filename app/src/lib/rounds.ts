// A server test as the Servers screen follows it: which servers are in the
// round and how far it got, per group and overall. Pure functions with no
// imports but types, so `pnpm test` (node --test) checks them as they are.

import type { Server } from './proto';

/**
 * A test round as the screen remembers it: the servers in it and when it
 * began. It outlives the daemon's State.test by a few seconds, so that the
 * outcome can be read before it fades.
 */
export interface Round {
  started: string;
  ids: Set<string>;
  own: boolean;
  /** When State.test went away (ms); null while the test runs. */
  ended: number | null;
}

/** How far a test got through some servers: a group, or all of them. */
export interface Progress {
  total: number;
  done: number;
  alive: number;
  running: number;
  /** waiting: none of them started; active; finished: all of them done. */
  phase: 'waiting' | 'active' | 'finished';
  /** The round is over and this is its outcome, about to fade. */
  ended: boolean;
}

/** The server got its result in this round. */
export function freshIn(server: Server, round: Round | null): boolean {
  return (
    round !== null &&
    round.ids.has(server.id) &&
    server.pingAt !== null &&
    Date.parse(server.pingAt) >= Date.parse(round.started)
  );
}

/**
 * Progress of a round over some servers. While the test runs each server
 * says where it is (check); once it is over, a result newer than the round's
 * start is what tells a tested server from one a stop left out.
 */
export function progressOf(servers: Server[], round: Round | null): Progress | null {
  if (!round) return null;
  let total = 0;
  let done = 0;
  let alive = 0;
  let running = 0;
  for (const x of servers) {
    if (!round.ids.has(x.id)) continue;
    total++;
    if (x.check === 'done' || (x.check === null && freshIn(x, round))) {
      done++;
      if (x.alive) alive++;
    } else if (x.check === 'running') {
      running++;
    }
  }
  const ended = round.ended !== null;
  // A stop left this group out altogether: there is nothing to say about it.
  if (total === 0 || (ended && done === 0)) return null;
  const phase = done === total || ended ? 'finished' : running > 0 || done > 0 ? 'active' : 'waiting';
  return { total, done, alive, running, phase, ended };
}
