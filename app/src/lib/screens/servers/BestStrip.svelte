<script lang="ts">
  import { flip } from 'svelte/animate';
  import Button from '../../components/Button.svelte';
  import Icon from '../../components/Icon.svelte';
  import ScoreRing from '../../components/ScoreRing.svelte';
  import { ru } from '../../i18n/ru';
  import type { Server } from '../../proto';
  import { isReliable, mbpsText, tierOf } from '../../rating';
  import { displayName, flagOf } from '../../view';

  // The few servers that have shown best over the last days, as cards on
  // top of the list: the answer to "which one should I take" without
  // reading two hundred rows. A card picks its server, as a row does.
  let {
    servers,
    selected,
    folded,
    measureDisabled = false,
    onselect,
    onmeasure,
    onfold,
  }: {
    servers: Server[];
    selected: string | null;
    folded: boolean;
    measureDisabled?: boolean;
    onselect: (id: string) => void;
    onmeasure: () => void;
    onfold: () => void;
  } = $props();

  const t = ru.rating;
  const s = ru.servers;
</script>

<section class="best" aria-labelledby="best-title">
  <div class="head">
    <button type="button" class="fold" aria-expanded={!folded} aria-controls="best-cards" onclick={onfold}>
      <span class="chev" class:folded><Icon name="chevron" size={14} /></span>
      <span class="title" id="best-title">{t.bestNow}</span>
      <span class="note">{t.bestNote}</span>
    </button>
    {#if !folded}
      <Button variant="ghost" icon="gauge" disabled={measureDisabled} onclick={onmeasure}>{t.bestMeasure}</Button>
    {/if}
  </div>
  {#if !folded}
    <ol class="cards" id="best-cards">
      {#each servers as server, i (server.id)}
        {@const st = server.stats!}
        {@const mine = server.id === selected}
        <li animate:flip={{ duration: 400 }} style:--i={i}>
          <button type="button" class="card" class:mine aria-current={mine ? 'true' : undefined} onclick={() => onselect(server.id)}>
            <span class="rank" class:first={i === 0} aria-label={t.rank(i + 1)}>
              {#if i === 0}<Icon name="crown" size={13} />{:else}{i + 1}{/if}
            </span>
            <ScoreRing stats={st} size={42} label={t.label(st.score, t.tiers[tierOf(st)])} />
            <span class="text">
              <span class="name">
                <span class="flag">{flagOf(server) ?? ''}</span>
                <span class="nm">{displayName(server)}</span>
                {#if mine}<Icon name="check" size={13} /><span class="sr-only">{s.selected}</span>{/if}
              </span>
              <span class="facts">
                <span>{ru.units.ms(st.avg)}</span>
                {#if st.speed !== null}<span class="dot">·</span><span>{mbpsText(st.speed)} {ru.speed.unit}</span>{/if}
                {#if isReliable(st)}<span class="steady" title={t.stableTitle}><Icon name="steady" size={12} />{t.stable}</span>{/if}
              </span>
            </span>
          </button>
        </li>
      {/each}
    </ol>
  {/if}
</section>

<style>
  .best {
    margin-bottom: var(--s3);
  }

  .head {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }

  .fold {
    display: flex;
    flex: 1;
    align-items: baseline;
    gap: 6px;
    min-width: 0;
    min-height: 30px;
    padding: 0 6px 0 2px;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    text-align: left;
  }

  .fold:hover .title {
    text-decoration: underline;
    text-decoration-color: var(--line);
    text-underline-offset: 3px;
  }

  .chev {
    display: flex;
    align-self: center;
    color: var(--muted);
    transition: transform var(--fast);
  }

  .chev.folded {
    transform: rotate(-90deg);
  }

  .title {
    flex: none;
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .note {
    min-width: 0;
    overflow: hidden;
    color: var(--muted);
    font-size: var(--fs-cap);
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .cards {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: var(--s2);
    margin: 4px 0 0;
    padding: 0;
    list-style: none;
  }

  .cards li {
    min-width: 0;
    animation: rise 0.45s cubic-bezier(0.2, 0.8, 0.2, 1) both;
    animation-delay: calc(var(--i, 0) * 70ms);
  }

  @keyframes rise {
    from {
      opacity: 0;
      transform: translateY(8px) scale(0.98);
    }
  }

  .card {
    position: relative;
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    padding: 10px 12px 10px 10px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
    text-align: left;
    transition:
      transform var(--fast) ease-out,
      box-shadow var(--fast) ease-out,
      border-color var(--fast);
  }

  .card:hover {
    transform: translateY(-1px);
    box-shadow: var(--shadow-pop);
  }

  .card.mine {
    border-color: color-mix(in srgb, var(--text) 45%, var(--line));
  }

  .rank {
    position: absolute;
    top: -7px;
    left: 10px;
    display: inline-grid;
    place-items: center;
    min-width: 16px;
    height: 16px;
    padding: 0 4px;
    border: 1px solid var(--line);
    border-radius: 8px;
    background: var(--surface-2);
    color: var(--muted);
    font-size: 10px;
    font-weight: 600;
    line-height: 1;
  }

  .rank.first {
    border-color: color-mix(in srgb, var(--vpn) 60%, var(--line));
    background: var(--surface);
    color: var(--vpn);
  }

  .text {
    display: grid;
    gap: 3px;
    min-width: 0;
  }

  .name {
    display: flex;
    align-items: center;
    gap: 5px;
    min-width: 0;
    font-weight: 600;
  }

  .flag {
    flex: none;
    font-size: 14px;
    line-height: 1;
  }

  .nm {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .facts {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 2px 5px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-variant-numeric: tabular-nums;
  }

  .steady {
    display: inline-flex;
    align-items: center;
    gap: 2px;
    color: var(--vpn);
  }
</style>
