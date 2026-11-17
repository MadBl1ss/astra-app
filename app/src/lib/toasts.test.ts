// Run with `pnpm test` (node --test): Node strips the types itself, so the
// logic is checked without a browser, a window or a build.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import type { Toast } from './proto';
import { EMPTY, MAX_CARDS, SHOW_MS, dismiss, expire, hover, nextExpiry, receive, release, view } from './toasts.ts';

function toast(id: string, over: Partial<Toast> = {}): Toast {
  return {
    id,
    severity: 'problem',
    title: `Заголовок ${id}`,
    body: 'Текст.',
    actions: [],
    sticky: false,
    close: false,
    ...over,
  };
}

test('the same id replaces the card in its place', () => {
  let s = receive(EMPTY, toast('a'), 0, false);
  s = receive(s, toast('b'), 100, false);
  s = receive(s, toast('a', { title: 'Появилась подсказка' }), 200, false);
  assert.deepEqual(
    s.shown.map((c) => [c.id, c.title, c.rev]),
    [
      ['a', 'Появилась подсказка', 1],
      ['b', 'Заголовок b', 0],
    ],
  );
  // The replacement starts its time anew.
  assert.equal(s.shown[0]!.at, 200);
});

test('close takes the card away, and nothing else', () => {
  let s = receive(EMPTY, toast('a'), 0, false);
  s = receive(s, toast('b'), 0, false);
  s = receive(s, toast('a', { close: true, title: '', body: '' }), 10, false);
  assert.deepEqual(
    s.shown.map((c) => c.id),
    ['b'],
  );
  assert.equal(receive(s, toast('zzz', { close: true }), 20, false), s);
});

test('ordinary cards go after 6 s, critical ones stay', () => {
  let s = receive(EMPTY, toast('a'), 0, false);
  s = receive(s, toast('crit', { severity: 'critical', sticky: true }), 0, false);
  s = receive(s, toast('info', { severity: 'info' }), 1000, false);
  assert.equal(nextExpiry(s, 0), SHOW_MS);
  assert.equal(expire(s, SHOW_MS - 1), s);
  s = expire(s, SHOW_MS);
  assert.deepEqual(
    s.shown.map((c) => c.id),
    ['crit', 'info'],
  );
  s = expire(s, SHOW_MS + 1000);
  assert.deepEqual(
    s.shown.map((c) => c.id),
    ['crit'],
  );
  assert.equal(nextExpiry(s, 99_999), null);
});

test('a critical card is sticky even if the daemon forgot to say so', () => {
  const s = receive(EMPTY, toast('crit', { severity: 'critical' }), 0, false);
  assert.equal(expire(s, 60_000).shown.length, 1);
});

test('time under the pointer does not count', () => {
  let s = receive(EMPTY, toast('a'), 0, false);
  s = hover(s, true, 5000);
  assert.equal(expire(s, 20_000), s);
  assert.equal(nextExpiry(s, 20_000), null);
  s = hover(s, false, 20_000);
  // 5 s had passed before the pointer came: 1 s is left.
  assert.equal(nextExpiry(s, 20_000), 1000);
  assert.equal(expire(s, 20_999).shown.length, 1);
  assert.equal(expire(s, 21_000).shown.length, 0);
});

test('four at once: three cards and "one more"', () => {
  let s = EMPTY;
  for (const id of ['1', '2', '3', '4']) s = receive(s, toast(id), Number(id), false);
  const v = view(s);
  assert.equal(v.cards.length, MAX_CARDS);
  assert.deepEqual(
    v.cards.map((c) => c.id),
    ['2', '3', '4'],
  );
  assert.equal(v.more, 1);
  // Once one is dismissed, the hidden one comes back.
  assert.deepEqual(
    view(dismiss(s, '4')).cards.map((c) => c.id),
    ['1', '2', '3'],
  );
});

test('a critical card is never the one folded away', () => {
  let s = receive(EMPTY, toast('crit', { severity: 'critical' }), 0, false);
  for (const id of ['a', 'b', 'c']) s = receive(s, toast(id), 1, false);
  const v = view(s);
  assert.deepEqual(
    v.cards.map((c) => c.id),
    ['crit', 'b', 'c'],
  );
  assert.equal(v.more, 1);
});

test('two buttons at most', () => {
  const actions = [
    { id: 'switch:nl', label: 'Переключить' },
    { id: 'open', label: 'Открыть' },
    { id: 'retry', label: 'Ещё раз' },
  ];
  const s = receive(EMPTY, toast('a', { actions }), 0, false);
  assert.deepEqual(
    s.shown[0]!.actions.map((a) => a.id),
    ['switch:nl', 'open'],
  );
});

test('over a game: problems wait, critical ones do not, only the last is shown after', () => {
  let s = receive(EMPTY, toast('a'), 0, true);
  s = receive(s, toast('b', { severity: 'info' }), 100, true);
  s = receive(s, toast('crit', { severity: 'critical' }), 200, true);
  assert.deepEqual(
    s.shown.map((c) => c.id),
    ['crit'],
  );
  assert.deepEqual(
    s.waiting.map((c) => c.id),
    ['a', 'b'],
  );
  s = release(s, 10_000);
  assert.deepEqual(
    s.shown.map((c) => [c.id, c.at]),
    [
      ['crit', 200],
      ['b', 10_000],
    ],
  );
  assert.equal(s.waiting.length, 0);
});

test('a problem that ended during the game is not shown after it', () => {
  let s = receive(EMPTY, toast('a'), 0, true);
  s = receive(s, toast('a', { close: true }), 100, true);
  assert.equal(s.waiting.length, 0);
  assert.equal(release(s, 200), s);
  // Replaced while waiting: the newer text is what comes out.
  s = receive(EMPTY, toast('a'), 0, true);
  s = receive(s, toast('a', { severity: 'info', title: 'Снова работает' }), 100, true);
  assert.deepEqual(
    release(s, 300).shown.map((c) => c.title),
    ['Снова работает'],
  );
});

test('a card already on screen is replaced even over a game', () => {
  let s = receive(EMPTY, toast('a'), 0, false);
  s = receive(s, toast('a', { severity: 'info', title: 'Снова работает' }), 100, true);
  assert.deepEqual(
    s.shown.map((c) => c.title),
    ['Снова работает'],
  );
  assert.equal(s.waiting.length, 0);
});
