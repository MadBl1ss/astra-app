// Run with `pnpm test` (node --test).

import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Server } from './proto';
import { progressOf, type Round } from './rounds.ts';

const T0 = '2026-11-21T10:00:00Z';
const before = '2026-11-21T09:30:00Z';
const after = '2026-11-21T10:00:04Z';

function server(id: string, over: Partial<Server> = {}): Server {
  return {
    id,
    name: id,
    country: null,
    sub: 's1',
    protocol: 'vless',
    via: 'sing-box',
    host: `${id}.example.com`,
    favorite: false,
    order: 0,
    ping: 80,
    pingAt: before,
    alive: true,
    stats: null,
    check: null,
    ...over,
  };
}

const round = (ids: string[], ended: number | null = null): Round => ({
  started: T0,
  ids: new Set(ids),
  own: false,
  ended,
});

test('counts a group by where its servers are', () => {
  const list = [
    server('a', { check: 'done', pingAt: after }),
    server('b', { check: 'done', pingAt: after, alive: false, ping: null }),
    server('c', { check: 'running' }),
    server('d', { check: 'queued' }),
    server('e'), // not in the round
  ];
  assert.deepEqual(progressOf(list, round(['a', 'b', 'c', 'd'])), {
    total: 4,
    done: 2,
    alive: 1,
    running: 1,
    phase: 'active',
    ended: false,
  });
});

test('a group still in the queue waits, one that is through is finished', () => {
  assert.equal(progressOf([server('a', { check: 'queued' })], round(['a']))?.phase, 'waiting');
  assert.equal(progressOf([server('a', { check: 'done', pingAt: after })], round(['a']))?.phase, 'finished');
  assert.equal(progressOf([server('x')], round(['a'])), null);
  assert.equal(progressOf([server('a')], null), null);
});

test('a group a stop left out says nothing, one it cut short says how far it got', () => {
  const list = [server('a', { pingAt: after }), server('b'), server('c')];
  assert.equal(progressOf([server('b'), server('c')], round(['b', 'c'], Date.now())), null);
  const p = progressOf(list, round(['a', 'b', 'c'], Date.now()));
  assert.equal(p?.done, 1);
  assert.equal(p?.total, 3);
  assert.equal(p?.phase, 'finished');
});

test('after the round, a fresh result tells a tested server from a skipped one', () => {
  // The daemon's last document may come without the last checks: the
  // round ended in the same tenth of a second.
  const list = [server('a', { pingAt: after }), server('b', { pingAt: after, alive: false }), server('c')];
  assert.deepEqual(progressOf(list, round(['a', 'b', 'c'], Date.now())), {
    total: 3,
    done: 2,
    alive: 1,
    running: 0,
    phase: 'finished',
    ended: true,
  });
});
