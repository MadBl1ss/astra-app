import { test } from 'node:test';
import assert from 'node:assert/strict';
import type { Server, ServerStats } from './proto.ts';
import { bestOf, dialAt, fastIds, follow, isReliable, mbpsText, TICKS, tierOf, toneOf, winner } from './rating.ts';

function stats(over: Partial<ServerStats> = {}): ServerStats {
  return { avg: 70, jitter: 6, loss: 0, samples: 50, score: 80, trust: 0.9, days: 5, stable: true, speed: null, speedAt: null, ...over };
}

function server(id: string, s: ServerStats | null): Server {
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
    ping: null,
    pingAt: null,
    alive: true,
    stats: s,
    check: null,
  };
}

test('tiers follow the score once there is evidence', () => {
  assert.equal(tierOf(null), 'none');
  assert.equal(tierOf(stats({ trust: 0.2, score: 95 })), 'new');
  assert.equal(tierOf(stats({ score: 85 })), 'top');
  assert.equal(tierOf(stats({ score: 70 })), 'good');
  assert.equal(tierOf(stats({ score: 50 })), 'fair');
  assert.equal(tierOf(stats({ score: 30 })), 'poor');
  // A server that never answered is poor whatever its trust.
  assert.equal(tierOf(stats({ avg: 0, loss: 1, trust: 0.1, score: 45 })), 'poor');
  assert.deepEqual(
    (['top', 'good', 'fair', 'poor', 'new', 'none'] as const).map(toneOf),
    ['ok', 'ok', 'warn', 'bad', 'none', 'none'],
  );
});

test('reliable is stable and good', () => {
  assert.equal(isReliable(stats()), true);
  assert.equal(isReliable(stats({ stable: false })), false);
  assert.equal(isReliable(stats({ score: 50 })), false);
  assert.equal(isReliable(null), false);
});

test('the best are scored, answering and in a steady order', () => {
  const list = [
    server('a', stats({ score: 70 })),
    server('b', stats({ score: 90, trust: 0.2 })), // lucky, no evidence
    server('c', stats({ score: 85 })),
    server('d', stats({ score: 85, speed: 50 })), // same score, faster
    server('e', null),
    server('f', stats({ score: 88, avg: 0, loss: 1 })), // silent
  ];
  assert.deepEqual(
    bestOf(list, 3).map((x) => x.id),
    ['d', 'c', 'a'],
  );
  assert.deepEqual(bestOf([], 3), []);
});

test('fast is close to the fastest measured', () => {
  const list = [
    server('a', stats({ speed: 100 })),
    server('b', stats({ speed: 90 })),
    server('c', stats({ speed: 40 })),
    server('d', stats()),
  ];
  assert.deepEqual([...fastIds(list)].sort(), ['a', 'b']);
  // One measured server is fast only if it is fast in itself.
  assert.deepEqual([...fastIds([server('a', stats({ speed: 12 }))])], []);
  assert.deepEqual([...fastIds([server('a', stats({ speed: 64 }))])], ['a']);
  // Everybody slow: nobody is "fast".
  assert.deepEqual([...fastIds([server('a', stats({ speed: 4 })), server('b', stats({ speed: 3 }))])], []);
});

test('the winner is the fastest download', () => {
  const r = (id: string, mbps: number | null) => ({ id, ping: 50, jitter: 2, mbps, bytes: 0, error: null });
  assert.equal(winner([r('a', 10), r('b', null), r('c', 42.5)])?.id, 'c');
  assert.equal(winner([r('a', null)]), null);
});

test('the dial is even between its marks and bounded', () => {
  assert.equal(dialAt(0), 0);
  assert.equal(dialAt(-3), 0);
  assert.equal(dialAt(1000), 1);
  assert.equal(dialAt(5000), 1);
  const step = 1 / (TICKS.length - 1);
  assert.ok(Math.abs(dialAt(10) - 2 * step) < 1e-9);
  assert.ok(Math.abs(dialAt(75) - 4.5 * step) < 1e-9);
  for (let v = 0; v < 1200; v += 7) assert.ok(dialAt(v + 7) >= dialAt(v));
});

test('speeds read the Russian way', () => {
  assert.equal(mbpsText(84.34), '84,3');
  assert.equal(mbpsText(9.6), '9,6');
  assert.equal(mbpsText(118.4), '118');
  assert.equal(mbpsText(-1), '0,0');
});

test('the needle closes half the gap per half-life whatever the frame rate', () => {
  assert.equal(follow(0, 100, 0), 0);
  assert.ok(Math.abs(follow(0, 100, 120) - 50) < 1e-9);
  let a = 0;
  for (let i = 0; i < 4; i++) a = follow(a, 100, 30);
  assert.ok(Math.abs(a - follow(0, 100, 120)) < 1e-9);
  assert.equal(follow(99.9999, 100, 16), 100);
});
