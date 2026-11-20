// Run with `pnpm test`. Every address here is made up.

import assert from 'node:assert/strict';
import { test } from 'node:test';
import { linkKind } from './sublink.ts';
import { unwrapSubLink } from './transport/mocklinks.ts';

const SUB = 'https://panel.example.com/sub/abc123';
const b64 = (s: string): string => Buffer.from(s).toString('base64');
const enc = encodeURIComponent;

test('a server link goes to subs.addLink, any other link to subs.add', () => {
  assert.equal(linkKind('vless://id@vpn.example.com:443#DE'), 'server');
  assert.equal(linkKind('  HY2://pw@vpn.example.com:443 '), 'server');
  assert.equal(linkKind(SUB), 'sub');
  assert.equal(linkKind('happ://add/' + SUB), 'sub');
  assert.equal(linkKind('https://panel.example.com/redirect?link=happ://add/' + SUB), 'sub');
  assert.equal(linkKind('sub://' + b64(SUB)), 'sub');
  assert.equal(linkKind(''), null);
  assert.equal(linkKind('привет'), null);
  assert.equal(linkKind('panel.example.com/sub/abc123'), null);
});

test('the mock unwraps what the daemon unwraps', () => {
  const cases: [string, string, string][] = [
    [SUB, SUB, ''],
    [SUB + '#Мой', SUB + '#Мой', ''],
    ['https://panel.example.com/redirect?link=happ://add/' + SUB, SUB, ''],
    ['https://panel.example.com/redirect?link=' + enc('happ://add/' + SUB), SUB, ''],
    ['https://panel.example.com/redirect?link=' + b64('happ://add/' + SUB), SUB, ''],
    ['happ://add/' + SUB, SUB, ''],
    ['happ://add/https://panel.example.com/sub?token=a&b=c', 'https://panel.example.com/sub?token=a&b=c', ''],
    ['v2raytun://import/' + SUB, SUB, ''],
    ['hiddify://import/' + SUB + '#%D0%9C%D0%BE%D0%B9%20VPN', SUB, 'Мой VPN'],
    ['streisand://import/' + SUB + '#Мой', SUB, 'Мой'],
    ['incy://add/' + SUB, SUB, ''],
    ['incy://import/' + b64(SUB), SUB, ''],
    ['sing-box://import-remote-profile?url=' + enc(SUB) + '#Мой', SUB, 'Мой'],
    ['clash://install-config?url=' + enc(SUB) + '&name=Provider', SUB, 'Provider'],
    ['v2rayng://install-sub/?url=' + enc(SUB + '#Мой'), SUB, 'Мой'],
    ['sub://' + b64(SUB) + '#Мой', SUB, 'Мой'],
    ['https://panel.example.com/r?link=' + enc('hiddify://import/happ://add/sub://' + b64(SUB)), SUB, ''],
  ];
  for (const [input, url, name] of cases) {
    assert.deepEqual(unwrapSubLink(input), { ok: true, url, name }, input);
  }
});

test('the mock refuses what the daemon refuses', () => {
  assert.deepEqual(unwrapSubLink('happ://crypt5/QUJDREVG'), { ok: false, why: 'encrypted', app: 'Happ' });
  assert.deepEqual(unwrapSubLink('incy://crypt1/QUJDREVG'), { ok: false, why: 'encrypted', app: 'INCY' });
  assert.deepEqual(unwrapSubLink('https://panel.example.com/redirect?link=happ://crypt4/QUJD'), {
    ok: false,
    why: 'encrypted',
    app: 'Happ',
  });
  assert.deepEqual(unwrapSubLink('happ://routing/onadd/' + b64('{}')), { ok: false, why: 'routing', app: 'Happ' });
  assert.deepEqual(unwrapSubLink('happ://add/vless://id@vpn.example.com:443'), { ok: false, why: 'server' });
  assert.deepEqual(unwrapSubLink('happ://add/hello'), { ok: false, why: 'noSub' });
  assert.deepEqual(unwrapSubLink('incy://connect'), { ok: false, why: 'noSub' });
  assert.deepEqual(unwrapSubLink('привет'), { ok: false, why: 'notLink' });
  assert.deepEqual(unwrapSubLink('https://'), { ok: false, why: 'notLink' });
  const fiveDeep =
    'https://a.example.com/r?link=' + enc('clash://install-config?url=' + enc('hiddify://import/happ://add/sub://' + b64(SUB)));
  assert.deepEqual(unwrapSubLink(fiveDeep), { ok: false, why: 'noSub' });
});
