<script lang="ts">
  import { ru } from '../i18n/ru';
  import type { RailLook, Tone } from '../view';

  // What the app is named after, in two lines: one stream of traffic splits
  // into the path through the VPN and the direct one. The globe next to it
  // shows where the VPN leads; this shows what goes where.
  let {
    vpn,
    direct,
    gate,
    tone,
    vpnStat,
    directStat,
  }: {
    vpn: RailLook;
    direct: RailLook;
    /** Both paths are closed at the start by the lock. */
    gate: boolean;
    tone: Tone;
    vpnStat: string;
    directStat: string;
  } = $props();

  const t = ru.tracks;

  // "Server not answering" is a warning; "nothing works" is an error.
  const vpnTone = $derived(tone === 'bad' ? 'bad' : 'warn');
</script>

<div class="fork">
  <svg viewBox="0 0 40 112" aria-hidden="true">
    <path class="rail trunk" class:closed={gate} d="M3 56H13" />
    <path class="rail vpn {vpn} {vpnTone}" class:degraded={vpn === 'flow' && tone === 'warn'} d="M13 56C27 56 21 28 37 28" />
    <path class="rail direct {direct}" d="M13 56C27 56 21 84 37 84" />
    {#if gate}
      <path class="mark bad" d="M4 51l9 10M13 51l-9 10" />
    {:else if vpn === 'down'}
      <path class="mark {vpnTone}" d="M28 23l8 10M36 23l-8 10" />
    {/if}
  </svg>

  <div class="row">
    <b>{t.vpn}</b>
    <span>{vpnStat}</span>
  </div>
  <div class="row">
    <b>{t.direct}</b>
    <span>{directStat}</span>
  </div>
</div>

<style>
  .fork {
    display: grid;
    grid-template-columns: 40px minmax(0, 1fr);
    grid-template-rows: 56px 56px;
    column-gap: var(--s2);
  }

  svg {
    grid-row: 1 / 3;
    width: 40px;
    height: 112px;
  }

  .row {
    display: flex;
    flex-direction: column;
    justify-content: center;
    min-width: 0;
    line-height: 1.3;
  }

  b {
    font-weight: 600;
  }

  span {
    color: var(--muted);
    font-size: var(--fs-cap);
    /* Figures that do not fit one line break into even halves. */
    text-wrap: balance;
  }

  .rail {
    fill: none;
    stroke-width: 2.5;
    stroke-linecap: round;
    transition: stroke var(--slow);
  }

  .trunk {
    stroke: var(--muted);
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
    stroke-dasharray: 3 6;
  }

  .vpn.waiting,
  .vpn.down {
    stroke: var(--warn);
    stroke-dasharray: 3 6;
  }

  .vpn.down.bad {
    stroke: var(--bad);
  }

  .mark {
    fill: none;
    stroke-width: 2;
    stroke-linecap: round;
  }

  .mark.warn {
    stroke: var(--warn);
  }

  .mark.bad {
    stroke: var(--bad);
  }
</style>
