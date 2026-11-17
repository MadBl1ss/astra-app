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

  // A narrow column breaks the figures between their parts, never inside
  // one: "↓ 3,9 МБ/с" split at the slash reads as two numbers.
  const SEP = ' · ';
  const parts = (stat: string) => stat.split(SEP);
</script>

{#snippet figures(stat: string)}
  <!-- A sentence ("Программы из списка VPN ждут сервер") wraps as usual. -->
  <span class="stat">
    {#each parts(stat) as part, i (i)}{#if i > 0}<span class="sep">{SEP}</span>{/if}<span class:part={parts(stat).length > 1}>{part}</span>{/each}
  </span>
{/snippet}

<div class="fork">
  <!-- Stretched with its rows: the ends of the paths stay at their middles. -->
  <svg viewBox="0 0 40 112" preserveAspectRatio="none" aria-hidden="true">
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
    {@render figures(vpnStat)}
  </div>
  <div class="row">
    <b>{t.direct}</b>
    {@render figures(directStat)}
  </div>
</div>

<style>
  .fork {
    display: grid;
    grid-template-columns: 40px minmax(0, 1fr);
    /* Equal rows, 56 px or as tall as the taller one needs: in a narrow
       window the figures take three lines. */
    grid-template-rows: repeat(2, minmax(56px, 1fr));
    column-gap: var(--s2);
  }

  svg {
    grid-row: 1 / 3;
    width: 40px;
    height: 100%;
    min-height: 112px;
  }

  svg path {
    vector-effect: non-scaling-stroke;
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

  .stat {
    color: var(--muted);
    font-size: var(--fs-cap);
    /* Figures that do not fit one line break into even halves. */
    text-wrap: balance;
  }

  .part {
    white-space: nowrap;
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
