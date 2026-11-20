// The fake daemon's reading of the links providers make for their apps, the
// same rules as core/internal/subs/unwrap.go: a redirect page with the link
// in a parameter, happ://add/<address>, sing-box://import-remote-profile?url=…,
// sub://<base64>, nested up to four deep, the inner link plain,
// percent-encoded or base64. The mock must refuse and accept what the real
// daemon does, or a screenshot of the dialog would show a different app.

export type Unwrapped =
  | { ok: true; url: string; name: string }
  | { ok: false; why: 'notLink' | 'noSub' | 'server' }
  | { ok: false; why: 'encrypted' | 'routing'; app: string };

type Refusal = Extract<Unwrapped, { ok: false }>;
type Step = { link: string; name: string };

const MAX_WRAPS = 4;
const SERVER_SCHEMES = new Set([
  'vless', 'vmess', 'trojan', 'ss', 'ssr', 'hysteria', 'hysteria2', 'hy2',
  'tuic', 'wireguard', 'wg', 'socks', 'socks5', 'anytls',
]);
const LINK_PARAMS = ['url', 'link', 'sub', 'config', 'redirect', 'redirect_to'];
const APP_NAMES: Record<string, string> = {
  happ: 'Happ', incy: 'INCY', v2raytun: 'v2RayTun', hiddify: 'Hiddify',
  streisand: 'Streisand', v2rayng: 'v2rayNG', karing: 'Karing',
};
const appName = (scheme: string): string => APP_NAMES[scheme] ?? scheme;

export function unwrapSubLink(raw: string): Unwrapped {
  let link = raw.trim();
  let name = '';
  for (let wraps = 0; ; wraps++) {
    const s = splitScheme(link);
    if (!s) return { ok: false, why: 'notLink' };
    let step: Step;
    if (s.scheme === 'http' || s.scheme === 'https') {
      const inner = fromPage(link);
      if (!inner) return finish(link, wraps, name);
      step = inner;
    } else if (SERVER_SCHEMES.has(s.scheme)) {
      return { ok: false, why: 'server' };
    } else {
      const inner = fromApp(s.scheme, s.rest);
      if ('ok' in inner) return inner;
      step = inner;
    }
    // The outermost name wins, as in the daemon.
    if (!name) name = step.name;
    if (wraps === MAX_WRAPS) return { ok: false, why: 'noSub' };
    link = step.link;
  }
}

function finish(link: string, wraps: number, name: string): Unwrapped {
  if (wraps > 0) {
    const at = link.indexOf('#');
    if (at >= 0) {
      if (!name) name = fragName(link.slice(at + 1));
      link = link.slice(0, at);
    }
  }
  try {
    if (new URL(link).hostname !== '') return { ok: true, url: link, name };
  } catch {
    // Falls through to the refusal.
  }
  return { ok: false, why: wraps > 0 ? 'noSub' : 'notLink' };
}

function fromPage(link: string): Step | null {
  let u: URL;
  try {
    u = new URL(link);
  } catch {
    return null;
  }
  if (u.search === '') return null;
  for (const k of LINK_PARAMS) {
    const inner = decodeLink(u.searchParams.get(k) ?? '');
    if (inner) return { link: inner.link, name: inner.name || fragName(u.hash.slice(1)) };
  }
  return null;
}

function fromApp(scheme: string, rest: string): Step | Refusal {
  const cut = rest.search(/[/?#]/);
  const action = cut >= 0 ? rest.slice(0, cut) : rest;
  const after = cut >= 0 ? rest.slice(cut) : '';
  const act = action.toLowerCase();
  if (act.startsWith('crypt')) return { ok: false, why: 'encrypted', app: appName(scheme) };
  if (act === 'routing' || act === 'autorouting') return { ok: false, why: 'routing', app: appName(scheme) };

  if (after.startsWith('/') && after.length > 1 && after[1] !== '?') {
    const inner = decodeLink(after.slice(1));
    if (inner) return inner;
  }
  const q = after.indexOf('?');
  if (q >= 0) {
    const tail = after.slice(q + 1);
    const hash = tail.indexOf('#');
    const params = new URLSearchParams(hash >= 0 ? tail.slice(0, hash) : tail);
    const frag = hash >= 0 ? tail.slice(hash + 1) : '';
    for (const k of LINK_PARAMS) {
      const inner = decodeLink(params.get(k) ?? '');
      if (inner) {
        const named = (params.get('name') ?? '').trim() || fragName(frag) || inner.name;
        return { link: inner.link, name: named };
      }
    }
  }
  return decodeLink(rest) ?? { ok: false, why: 'noSub' };
}

function decodeLink(raw: string): Step | null {
  const v = raw.trim();
  if (v === '') return null;
  if (looksLikeLink(v)) return { link: v, name: '' };
  const hash = v.indexOf('#');
  const body = hash >= 0 ? v.slice(0, hash) : v;
  const name = hash >= 0 ? fragName(v.slice(hash + 1)) : '';
  if (body.includes('%')) {
    try {
      const s = decodeURIComponent(body);
      if (looksLikeLink(s)) return { link: s, name };
    } catch {
      // Not percent-encoding after all.
    }
  }
  // A query turns base64's "+" into a blank, a trailing one included.
  const rawHash = raw.indexOf('#');
  for (const b of [rawHash >= 0 ? raw.slice(0, rawHash) : raw, body]) {
    const s = decodeB64(b.replaceAll(' ', '+'))?.trim();
    if (s && looksLikeLink(s)) return { link: s, name };
  }
  return null;
}

function decodeB64(s: string): string | null {
  try {
    const bin = atob(s.trim().replaceAll('-', '+').replaceAll('_', '/'));
    return new TextDecoder('utf-8', { fatal: true }).decode(Uint8Array.from(bin, (c) => c.charCodeAt(0)));
  } catch {
    return null;
  }
}

function looksLikeLink(s: string): boolean {
  const parts = splitScheme(s);
  const hash = s.indexOf('#');
  const base = hash >= 0 ? s.slice(0, hash) : s;
  return parts !== null && parts.rest !== '' && !/\s/.test(base);
}

function splitScheme(s: string): { scheme: string; rest: string } | null {
  const i = s.indexOf('://');
  if (i <= 0) return null;
  const scheme = s.slice(0, i).toLowerCase();
  if (!/^[a-z][a-z0-9+.-]*$/.test(scheme)) return null;
  return { scheme, rest: s.slice(i + 3) };
}

function fragName(frag: string): string {
  let f = frag;
  try {
    f = decodeURIComponent(f);
  } catch {
    // A broken escape keeps the text as it came.
  }
  return f.split('?')[0]!.trim();
}
