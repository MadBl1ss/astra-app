<script lang="ts">
  import { ru } from '../i18n/ru';
  import type { RailLook, Tone } from '../view';

  // The picture the app is named after: one stream of traffic splits into
  // the path through the VPN and the direct one. Thickness, brightness and
  // the speed of the moving dashes follow the traffic on each path.
  let {
    vpn,
    direct,
    gate,
    tone,
    server,
    vpnStat,
    directStat,
    vpnRate,
    directRate,
  }: {
    vpn: RailLook;
    direct: RailLook;
    /** Both paths are closed at the start by the lock. */
    gate: boolean;
    tone: Tone;
    server: string;
    vpnStat: string;
    directStat: string;
    /** Bytes per second; null when the split by path is not known yet. */
    vpnRate: number | null;
    directRate: number | null;
  } = $props();

  const t = ru.tracks;
  const VPN_PATH = 'M214 88C300 88 300 44 392 44H592';
  const DIRECT_PATH = 'M214 88C300 88 300 132 392 132H592';

  // 0 for silence, 1 for ten megabytes a second and more; logarithmic,
  // because a video call and a download differ by orders of magnitude.
  function load(rate: number | null): number {
    if (rate === null) return 0.35;
    return Math.min(Math.log10(1 + rate / 20_000) / 2.7, 1);
  }

  const width = (rate: number | null) => (2.5 + 3.5 * load(rate)).toFixed(2);
  const glow = (rate: number | null) => (0.6 + 0.4 * load(rate)).toFixed(2);
  // A full step of the dash pattern takes from 1.8 s down to 0.5 s.
  const period = (rate: number | null) => `${(1.8 - 1.3 * load(rate)).toFixed(2)}s`;
  const moving = (look: RailLook, rate: number | null) => look === 'flow' && (rate === null || rate > 512);

  // "Server not answering" is a warning; "nothing works" is an error.
  const vpnTone = $derived(tone === 'bad' ? 'bad' : 'warn');

  function short(name: string): string {
    return name.length > 25 ? `${name.slice(0, 24).trimEnd()}…` : name;
  }
</script>

<svg viewBox="0 0 820 176" role="img" aria-label={t.aria}>
  <path class="rail trunk" class:closed={gate} d="M62 88H214" />

  <path
    class="rail vpn {vpn} {vpnTone}"
    class:degraded={vpn === 'flow' && tone === 'warn'}
    d={VPN_PATH}
    style:stroke-width={vpn === 'flow' ? width(vpnRate) : null}
    style:opacity={vpn === 'flow' ? glow(vpnRate) : null}
  />
  {#if moving(vpn, vpnRate)}
    <path class="motion" d={VPN_PATH} style:stroke-width={width(vpnRate)} style:animation-duration={period(vpnRate)} />
  {/if}

  <path
    class="rail direct {direct}"
    d={DIRECT_PATH}
    style:stroke-width={direct === 'flow' ? width(directRate) : null}
    style:opacity={direct === 'flow' ? glow(directRate) : null}
  />
  {#if moving(direct, directRate)}
    <path class="motion" d={DIRECT_PATH} style:stroke-width={width(directRate)} style:animation-duration={period(directRate)} />
  {/if}

  <circle class="node" cx="54" cy="88" r="8" />
  <text class="cap" x="54" y="114" text-anchor="middle">{t.pc[0]}</text>
  <text class="cap" x="54" y="129" text-anchor="middle">{t.pc[1]}</text>
  <circle class="split" cx="214" cy="88" r="4.5" />

  {#if gate}
    <path class="mark bad" d="M176 74l24 28M200 74l-24 28" />
  {:else if vpn === 'down'}
    <path class="mark {vpnTone}" d="M556 34l16 20M572 34l-16 20" />
  {/if}

  <text class="lbl" x="392" y="26">{t.vpn}</text>
  <text class="cap" x="392" y="68">{vpnStat}</text>
  <rect class="node" x="600" y="28" width="206" height="32" rx="6" />
  <text x="703" y="49" text-anchor="middle">{short(server)}</text>

  <text class="lbl" x="392" y="114">{t.direct}</text>
  <text class="cap" x="392" y="156">{directStat}</text>
  <rect class="node" x="600" y="116" width="206" height="32" rx="6" />
  <text x="703" y="137" text-anchor="middle">{t.internet}</text>
</svg>

<style>
  svg {
    display: block;
    width: 100%;
    height: auto;
  }

  text {
    fill: var(--text);
    font-size: 13px;
  }

  .cap {
    fill: var(--muted);
    font-size: 12px;
  }

  .lbl {
    font-weight: 600;
  }

  .rail {
    fill: none;
    stroke-width: 3;
    stroke-linecap: round;
    transition:
      stroke var(--slow),
      stroke-width var(--slow),
      opacity var(--slow);
  }

  .trunk {
    stroke: var(--muted);
  }

  .trunk.closed {
    stroke: var(--line);
    stroke-dasharray: 5 8;
  }

  .vpn.flow {
    stroke: var(--vpn);
  }

  /* Unstable but working: the path keeps its shape and loses the green. */
  .vpn.flow.degraded {
    stroke: var(--warn);
  }

  .direct.flow {
    stroke: var(--direct);
  }

  /* Not carrying traffic: grey and dashed, so it reads without colour too. */
  .idle,
  .closed,
  .direct.waiting {
    stroke: var(--line);
    stroke-dasharray: 5 8;
  }

  .vpn.waiting,
  .vpn.down {
    stroke: var(--warn);
    stroke-dasharray: 5 8;
  }

  .vpn.down.bad {
    stroke: var(--bad);
  }

  /* Coming up: the dashes creep towards the server. */
  .vpn.waiting {
    animation: creep 1.2s linear infinite;
  }

  @keyframes creep {
    to {
      stroke-dashoffset: -13;
    }
  }

  /* Gaps of the background colour travel along a solid line. */
  .motion {
    fill: none;
    stroke: var(--surface);
    stroke-width: 3;
    stroke-linecap: round;
    stroke-dasharray: 2 16;
    opacity: 0.8;
    animation: flow 1s linear infinite;
  }

  @keyframes flow {
    to {
      stroke-dashoffset: -18;
    }
  }

  .node {
    fill: var(--surface-2);
    stroke: var(--line);
  }

  .split {
    fill: var(--text);
  }

  .mark {
    fill: none;
    stroke-width: 2.5;
    stroke-linecap: round;
  }

  .mark.warn {
    stroke: var(--warn);
  }

  .mark.bad {
    stroke: var(--bad);
  }

  @media (prefers-reduced-motion: reduce) {
    .motion,
    .vpn.waiting {
      animation: none;
    }

    /* Without motion a flowing path would look dotted for no reason. */
    .motion {
      display: none;
    }
  }
</style>
