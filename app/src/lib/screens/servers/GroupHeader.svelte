<script lang="ts">
  import Icon from '../../components/Icon.svelte';
  import Menu, { type MenuItem } from '../../components/Menu.svelte';
  import { ru } from '../../i18n/ru';
  import type { SubFacts } from '../../servers';

  // The head of a group: a button that folds it, how fresh the subscription
  // is, its menu; under them its facts as plates (size, traffic left with a
  // gauge, term) and the update error if there was one.
  let {
    title,
    facts = null,
    error = null,
    note = null,
    folded,
    list,
    menu = [],
    onfold,
  }: {
    title: string;
    facts?: SubFacts | null;
    error?: string | null;
    note?: string | null;
    folded: boolean;
    /** id of the list this header folds. */
    list: string;
    menu?: MenuItem[];
    onfold: () => void;
  } = $props();

  const t = ru.servers;
</script>

<div class="head">
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
</style>
