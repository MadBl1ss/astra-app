<script lang="ts">
  import type { Snippet } from 'svelte';

  // One line of a list: a narrow leading cell (a time, a flag), the text,
  // and an optional trailing cell. Lists are rows between hairlines.
  let {
    lead,
    tone = 'none',
    trail,
    children,
  }: {
    lead?: string;
    tone?: 'none' | 'warn' | 'bad';
    trail?: Snippet;
    children: Snippet;
  } = $props();
</script>

<li class="row {tone}">
  {#if lead !== undefined}<span class="lead">{lead}</span>{/if}
  <span class="main">{@render children()}</span>
  {#if trail}<span class="trail">{@render trail()}</span>{/if}
</li>

<style>
  .row {
    display: flex;
    align-items: baseline;
    gap: var(--s2);
    min-height: 31px;
    padding: 5px 0;
    border-top: 1px solid var(--line);
  }

  .lead {
    flex: none;
    width: 46px;
    color: var(--muted);
  }

  .main {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* A problem line is marked with a bar as well as a colour. */
  .warn .main,
  .bad .main {
    padding-left: var(--s2);
    border-left: 2px solid var(--warn);
  }

  .bad .main {
    border-left-color: var(--bad);
  }

  .trail {
    flex: none;
    color: var(--muted);
  }
</style>
