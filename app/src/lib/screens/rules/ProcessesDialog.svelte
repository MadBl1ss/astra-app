<script lang="ts">
  import Button from '../../components/Button.svelte';
  import Dialog from '../../components/Dialog.svelte';
  import Empty from '../../components/Empty.svelte';
  import Field from '../../components/Field.svelte';
  import Icon from '../../components/Icon.svelte';
  import Loading from '../../components/Loading.svelte';
  import { ru } from '../../i18n/ru';
  import type { ProcessInfo, RuleListName } from '../../proto';
  import { daemon } from '../../store.svelte';

  // Programs with windows, from the daemon. A click adds the program to the
  // list the screen is on; the dialog stays open, so several can be added.
  let {
    list,
    listTitle,
    have,
    onadd,
    onclose,
  }: {
    list: RuleListName;
    listTitle: string;
    /** Programs already in this list, lowercased. */
    have: Set<string>;
    onadd: (name: string) => Promise<boolean>;
    onclose: () => void;
  } = $props();

  const t = ru.processes;
  let all = $state<ProcessInfo[] | null>(null);
  let failed = $state<string | null>(null);
  let query = $state('');

  $effect(() => {
    daemon
      .call('rules.processes')
      .then((r) => (all = r.processes))
      .catch((err: Error) => {
        all = [];
        failed = err.message;
      });
  });

  const shown = $derived(
    (all ?? []).filter((p) => !query.trim() || `${p.name} ${p.path}`.toLowerCase().includes(query.trim().toLowerCase())),
  );
</script>

<Dialog title={t.title} width={520} {onclose}>
  <Field label={t.search} hideLabel icon="search" placeholder={t.search} bind:value={query} />
  <div class="box" data-list={list}>
    {#if all === null}
      <Loading label={t.loading} rows={5} />
    {:else if failed}
      <Empty compact icon="alert" title={failed} />
    {:else if all.length === 0}
      <Empty compact icon="app" title={t.empty} />
    {:else if shown.length === 0}
      <Empty compact icon="search" title={t.nothing} />
    {:else}
      <ul>
        {#each shown as p (p.path || p.name)}
          {@const inList = have.has(p.name.toLowerCase())}
          <li>
            <button
              type="button"
              disabled={inList}
              aria-label={inList ? `${p.name}: ${t.inList}` : t.addTo(p.name, listTitle)}
              onclick={() => onadd(p.name)}
            >
              <span class="ico"><Icon name="app" size={16} /></span>
              <span class="text">
                <span class="name">{p.name}</span>
                {#if p.path}<span class="path" title={p.path}>{p.path}</span>{/if}
              </span>
              {#if inList}<span class="mark"><Icon name="check" size={14} />{t.inList}</span>{:else}<Icon name="plus" size={15} />{/if}
            </button>
          </li>
        {/each}
      </ul>
    {/if}
  </div>
  {#snippet actions()}
    <Button variant="ghost" onclick={onclose}>{t.close}</Button>
  {/snippet}
</Dialog>

<style>
  .box {
    height: min(340px, 52vh);
    margin: 0 -6px;
    overflow-y: auto;
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  button {
    display: flex;
    align-items: center;
    gap: 10px;
    width: 100%;
    min-height: 42px;
    padding: 4px 8px;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    text-align: left;
  }

  button:hover:not(:disabled) {
    background: var(--surface-2);
  }

  button:focus-visible {
    outline-offset: -2px;
  }

  button:disabled {
    cursor: default;
  }

  .ico {
    display: flex;
    color: var(--muted);
  }

  .text {
    display: grid;
    flex: 1;
    min-width: 0;
  }

  .name,
  .path {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .path {
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  button :global(svg) {
    color: var(--muted);
  }

  .mark {
    display: flex;
    align-items: center;
    gap: 4px;
    color: var(--muted);
    font-size: var(--fs-cap);
  }
</style>
