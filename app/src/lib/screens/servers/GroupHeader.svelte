<script lang="ts">
  import Icon from '../../components/Icon.svelte';
  import Menu, { type MenuItem } from '../../components/Menu.svelte';
  import { ru } from '../../i18n/ru';
  import type { Part } from '../../servers';

  // The head of a group: a button that folds it, the line about it, the
  // update error if there was one, and the subscription's menu.
  let {
    title,
    parts = [],
    error = null,
    note = null,
    folded,
    list,
    menu = [],
    onfold,
  }: {
    title: string;
    parts?: Part[];
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
  {#if menu.length > 0}<Menu label={t.subMenu(title)} items={menu} />{/if}
</div>
{#if parts.length > 0 || error || note}
  <div class="meta">
    {#if parts.length > 0}
      <p>
        {#each parts as part, i (i)}
          {#if i > 0}<span class="sep" aria-hidden="true">{' · '}</span>{/if}<span class={part.tone}>{part.text}</span>
        {/each}
      </p>
    {/if}
    {#if note}<p>{note}</p>{/if}
    {#if error}<p class="error"><Icon name="alert" size={13} />{t.updateFailed(error)}</p>{/if}
  </div>
{/if}

<style>
  .head {
    display: flex;
    align-items: center;
    gap: var(--s1);
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

  .meta {
    display: grid;
    gap: 2px;
    padding: 0 6px 6px 22px;
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .warn {
    color: var(--warn);
  }

  .bad {
    color: var(--bad);
  }

  .error {
    display: flex;
    align-items: flex-start;
    gap: 5px;
    color: var(--warn);
  }

  .error :global(svg) {
    flex: none;
    margin-top: 1px;
  }
</style>
