<script lang="ts">
  import { ago } from '../format';
  import { ru } from '../i18n/ru';
  import type { ServerStats } from '../proto';
  import { mbpsText, tierOf, TRUST_FLOOR } from '../rating';
  import Icon from './Icon.svelte';
  import ScoreRing from './ScoreRing.svelte';

  // What stands behind a score: each part of it on its own line, with a bar
  // for the share of answers and for the days, so that "82" reads as "fast,
  // steady, there every day" rather than as a bare number.
  let { stats, now, best = false }: { stats: ServerStats; now: number; best?: boolean } = $props();

  const t = ru.rating;
  const tier = $derived(tierOf(stats));
  const silent = $derived(stats.avg === 0);
  const answers = $derived(Math.round((1 - stats.loss) * 100));
</script>

<div class="head">
  <ScoreRing {stats} size={44} label="" />
  <div class="title">
    <span class="score">
      {#if tier === 'new'}{t.tiers.new}{:else}{stats.score} <span class="of">{t.of}</span>{/if}
    </span>
    <span class="tier">
      {#if tier !== 'new'}{t.tiers[tier]}{/if}
      {#if best}<span class="badge"><Icon name="crown" size={12} />{t.best}</span>{/if}
      {#if stats.stable}<span class="badge"><Icon name="steady" size={12} />{t.stable}</span>{/if}
    </span>
  </div>
</div>

<dl>
  {#if !silent}
    <dt>{t.ping}</dt>
    <dd>{t.pingValue(stats.avg, stats.jitter)}</dd>
  {/if}
  <dt>{t.answers}</dt>
  <dd>
    <span class="bar"><i style:width="{answers}%" class:warn={answers < 95} class:bad={answers < 80}></i></span>
    {t.answersValue(answers)}
  </dd>
  <dt>{t.speed}</dt>
  <dd>{stats.speed !== null ? t.speedValue(mbpsText(stats.speed), ago(stats.speedAt, now)) : t.speedNever}</dd>
  {#if !silent}
    <dt>{t.days}</dt>
    <dd>
      <span class="days" aria-hidden="true">
        {#each { length: 14 } as _, i (i)}<i class:on={i < stats.days} style:--i={i}></i>{/each}
      </span>
      {t.daysValue(stats.days)}
    </dd>
  {/if}
  <dt>{t.samples}</dt>
  <dd>{stats.samples}</dd>
</dl>

<p class="note">
  {#if silent}{t.silentNote}{:else if stats.trust < TRUST_FLOOR}{t.newNote}{:else}{t.how}{/if}
</p>

<style>
  .head {
    display: flex;
    align-items: center;
    gap: 12px;
    margin-bottom: 10px;
  }

  .title {
    display: grid;
    gap: 3px;
    min-width: 0;
  }

  .score {
    font: 600 var(--fs-head) / 1.1 var(--font-display);
  }

  .of {
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }

  .tier {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 6px;
    color: var(--muted);
  }

  .badge {
    display: inline-flex;
    align-items: center;
    gap: 3px;
    padding: 0 6px 0 4px;
    border: 1px solid color-mix(in srgb, var(--vpn) 45%, transparent);
    border-radius: 8px;
    color: var(--vpn);
    line-height: 16px;
  }

  dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 5px 12px;
    margin: 0;
    padding: 10px 0;
    border-top: 1px solid var(--line);
    border-bottom: 1px solid var(--line);
  }

  dt {
    color: var(--muted);
  }

  dd {
    display: flex;
    align-items: center;
    justify-content: flex-end;
    gap: 8px;
    margin: 0;
    text-align: right;
    white-space: nowrap;
    font-variant-numeric: tabular-nums;
  }

  .bar {
    position: relative;
    width: 44px;
    height: 4px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--line);
  }

  .bar i {
    position: absolute;
    inset: 0 auto 0 0;
    border-radius: 2px;
    background: var(--vpn);
    animation: fill 0.7s cubic-bezier(0.22, 1, 0.36, 1) both;
    transform-origin: left;
  }

  .bar i.warn {
    background: var(--warn);
  }

  .bar i.bad {
    background: var(--bad);
  }

  @keyframes fill {
    from {
      transform: scaleX(0);
    }
  }

  .days {
    display: inline-flex;
    gap: 2px;
  }

  .days i {
    width: 3px;
    height: 9px;
    border-radius: 1px;
    background: var(--line);
  }

  .days i.on {
    background: var(--vpn);
    animation: rise 0.4s ease-out both;
    animation-delay: calc(var(--i, 0) * 30ms);
  }

  @keyframes rise {
    from {
      transform: scaleY(0.2);
      opacity: 0.3;
    }
  }

  .note {
    padding-top: 8px;
    color: var(--muted);
    line-height: 1.4;
  }
</style>
