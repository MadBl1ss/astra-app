<script lang="ts">
  import Icon from '../../components/Icon.svelte';
  import Menu, { type MenuItem } from '../../components/Menu.svelte';
  import { ru } from '../../i18n/ru';
  import type { Progress } from '../../rounds';
  import type { SubFacts } from '../../servers';

  // The head of a group: a button that folds it, how fresh the subscription
  // is, its menu; under them its facts as plates (size, traffic left with a
  // gauge, term) and the update error if there was one.
  let {
    title,
    facts = null,
    error = null,
    note = null,
    progress = null,
    folded,
    list,
    menu = [],
    onfold,
  }: {
    title: string;
    facts?: SubFacts | null;
    error?: string | null;
    note?: string | null;
    /** How far the server test got through this group; null outside a test. */
    progress?: Progress | null;
    folded: boolean;
    /** id of the list this header folds. */
    list: string;
    menu?: MenuItem[];
    onfold: () => void;
  } = $props();

  const t = ru.servers;
</script>

<div class="head" class:active={progress?.phase === 'active'}>
  <button
    type="button"
    class="fold"
    aria-expanded={!folded}
    aria-controls={list}
    aria-label={folded ? t.unfold(title) : t.fold(title)}
    onclick={onfold}
  >
    <span class="chev" class:folded><Icon name="chevron" size={14} /></span>
    <span class="title">{title}</span>
  </button>
  {#if progress}
    {@const p = progress}
    {@const share = p.total ? p.done / p.total : 0}
    <span class="test {p.phase}" class:ended={p.ended} class:none={p.phase === 'finished' && p.alive === 0}>
      {#if p.phase === 'finished'}
        <Icon name={p.alive === 0 ? 'alert' : 'check'} size={13} />
        <span>{p.done < p.total ? t.groupHalted(p.alive, p.done) : t.groupDone(p.alive, p.total)}</span>
      {:else}
        <svg class="ring" viewBox="0 0 16 16" aria-hidden="true">
          <circle class="track" cx="8" cy="8" r="6" />
          <circle class="arc" cx="8" cy="8" r="6" pathLength="100" stroke-dasharray="100" stroke-dashoffset={100 - share * 100} />
          {#if p.phase === 'active'}<circle class="comet" cx="8" cy="8" r="6" pathLength="100" stroke-dasharray="14 86" />{/if}
        </svg>
        <span>{p.phase === 'waiting' ? t.groupQueued : t.groupRunning(p.done, p.total)}</span>
      {/if}
    </span>
  {/if}
  {#if facts}<span class="fresh">{facts.updated}</span>{/if}
  {#if menu.length > 0}<Menu label={t.subMenu(title)} items={menu} />{/if}
</div>
{#if facts || error || note}
  <div class="meta">
    {#if facts}
      <ul class="facts">
        <li class="fact">
          <Icon name="servers" size={13} />
          <span class="value">{facts.count}</span>
          {#if facts.starred}<span class="note">· {facts.starred}</span>{/if}
        </li>
        {#if facts.traffic}
          {@const traffic = facts.traffic}
          <li class="fact {traffic.tone}" title={traffic.title}>
            <span class="gauge" aria-hidden="true"><span style:width="{Math.round(traffic.share * 100)}%"></span></span>
            <span class="sr-only">{traffic.title}</span>
            <span class="value" aria-hidden="true">{traffic.value}</span>
            {#if traffic.of}<span class="note" aria-hidden="true">{traffic.of}</span>{/if}
          </li>
        {/if}
        {#if facts.term}
          {@const term = facts.term}
          <li class="fact {term.tone}">
            <Icon name="calendar" size={13} />
            <span class="value">{term.value}</span>
            {#if term.rest}<span class="note">· {term.rest}</span>{/if}
          </li>
        {/if}
      </ul>
    {/if}
    {#if note}<p>{note}</p>{/if}
    {#if error}<p class="error"><Icon name="alert" size={14} /><span>{t.updateFailed(error)}</span></p>{/if}
  </div>
{/if}

<style>
  .head {
    position: relative;
    display: flex;
    align-items: center;
    gap: var(--s2);
  }

  .fold {
    display: flex;
    flex: 1;
    align-items: center;
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
    color: var(--muted);
    transition: transform var(--fast);
  }

  .chev.folded {
    transform: rotate(-90deg);
  }

  .title {
    min-width: 0;
    overflow: hidden;
    font-size: var(--fs-head);
    font-weight: 600;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .fresh {
    flex: none;
    color: var(--muted);
    font-size: var(--fs-cap);
    white-space: nowrap;
  }

  .meta {
    display: grid;
    gap: 6px;
    padding: 2px 0 8px 22px;
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .facts {
    display: flex;
    flex-wrap: wrap;
    gap: 6px;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  /* A plate per fact: the value in the text colour, its note quieter. The
     tone colours the value, the icon and the edge; the words say the same. */
  .fact {
    display: inline-flex;
    align-items: center;
    gap: 6px;
    min-height: 24px;
    padding: 0 10px 0 8px;
    border: 1px solid var(--line);
    border-radius: 999px;
    background: var(--surface);
    white-space: nowrap;
  }

  .fact :global(svg) {
    color: var(--muted);
  }

  .value {
    color: var(--text);
    font-weight: 500;
    font-variant-numeric: tabular-nums;
  }

  .note {
    margin-left: -2px;
  }

  .fact.warn {
    border-color: color-mix(in srgb, var(--warn) 45%, var(--line));
  }

  .fact.bad {
    border-color: color-mix(in srgb, var(--bad) 45%, var(--line));
  }

  .fact.warn .value,
  .fact.warn :global(svg) {
    color: var(--warn);
  }

  .fact.bad .value,
  .fact.bad :global(svg) {
    color: var(--bad);
  }

  /* Traffic left, like a fuel gauge: full when the plan is fresh. */
  .gauge {
    position: relative;
    width: 44px;
    height: 6px;
    overflow: hidden;
    border-radius: 3px;
    background: var(--line);
  }

  .gauge > span {
    position: absolute;
    inset: 0 auto 0 0;
    min-width: 2px;
    border-radius: 3px;
    background: var(--text);
    opacity: 0.75;
  }

  .warn .gauge > span {
    background: var(--warn);
    opacity: 1;
  }

  .bad .gauge > span {
    display: none;
  }

  .error {
    display: flex;
    align-items: flex-start;
    gap: 7px;
    width: fit-content;
    max-width: 100%;
    padding: 6px 12px 6px 9px;
    border: 1px solid color-mix(in srgb, var(--warn) 40%, var(--line));
    border-radius: var(--r-ctl);
    background: var(--surface);
    color: var(--warn);
    line-height: 1.4;
  }

  .error :global(svg) {
    flex: none;
    margin-top: 1px;
  }

  /* The server test, group by group: a hollow ring while the group waits
     its turn, a ring that fills as its servers answer, with a short arc
     running round it, and a line under the header while it is being
     tested. Then the outcome, which fades with the round. */
  .head.active::after {
    content: '';
    position: absolute;
    right: 0;
    bottom: -2px;
    left: 0;
    height: 1px;
    background: linear-gradient(90deg, transparent, var(--muted), transparent) no-repeat;
    background-size: 30% 100%;
    animation: run 1.4s ease-in-out infinite;
    pointer-events: none;
  }

  @keyframes run {
    from {
      background-position: -45% 0;
    }
    to {
      background-position: 145% 0;
    }
  }

  .test {
    display: inline-flex;
    flex: none;
    align-items: center;
    gap: 5px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-variant-numeric: tabular-nums;
    white-space: nowrap;
    animation: appear var(--slow) ease-out;
  }

  .test.active {
    color: var(--text);
  }

  .test.none {
    color: var(--bad);
  }

  .test.ended {
    animation: linger 4s ease-in forwards;
  }

  @keyframes appear {
    from {
      opacity: 0;
      transform: translateX(4px);
    }
  }

  @keyframes linger {
    0%,
    75% {
      opacity: 1;
    }
    100% {
      opacity: 0;
    }
  }

  .ring {
    width: 14px;
    height: 14px;
    transform: rotate(-90deg);
  }

  .ring circle {
    fill: none;
    stroke-width: 2;
  }

  .track {
    stroke: var(--line);
  }

  .arc {
    stroke: var(--text);
    stroke-linecap: round;
    transition: stroke-dashoffset 0.3s ease-out;
  }

  .waiting .arc {
    opacity: 0;
  }

  .comet {
    stroke: var(--muted);
    stroke-linecap: round;
    transform-origin: 8px 8px;
    animation: orbit 1s linear infinite;
  }

  @keyframes orbit {
    to {
      transform: rotate(360deg);
    }
  }
</style>
