// Which method a pasted link goes to. Only a single server's share link is
// recognised here; everything else with a scheme is the daemon's to read,
// because providers wrap subscriptions in links for their apps
// (happ://add/https://…, a redirect page around one) and the daemon is the
// one that unwraps them and explains a link it cannot use.

const SERVER = /^(vless|trojan|ss|hysteria2|hy2):\/\/\S+/i;
const ANY_LINK = /^[a-z][a-z0-9+.-]*:\/\/\S/i;

export type LinkKind = 'server' | 'sub';

/** 'server' for subs.addLink, 'sub' for subs.add, null when it is no link at all. */
export function linkKind(value: string): LinkKind | null {
  const v = value.trim();
  if (SERVER.test(v)) return 'server';
  if (ANY_LINK.test(v)) return 'sub';
  return null;
}
