<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon, { type IconName } from './Icon.svelte';

  // What an empty list says instead of nothing: why it is empty and, where
  // there is one, what to do about it.
  let {
    icon,
    title,
    text,
    compact = false,
    children,
  }: {
    icon: IconName;
    title: string;
    text?: string;
    compact?: boolean;
    children?: Snippet;
  } = $props();
</script>

<div class="empty" class:compact>
  <span class="ico"><Icon name={icon} size={compact ? 18 : 24} /></span>
  <b>{title}</b>
  {#if text}<p>{text}</p>{/if}
  {#if children}<div class="acts">{@render children()}</div>{/if}
</div>

<style>
  .empty {
    display: grid;
    justify-items: center;
    gap: 6px;
    padding: 36px 16px;
    color: var(--muted);
    text-align: center;
  }

  .compact {
    gap: 4px;
    padding: 18px 12px;
  }

  .ico {
    display: flex;
    margin-bottom: 2px;
  }

  b {
    color: var(--text);
    font-weight: 600;
  }

  p {
    max-width: 360px;
  }

  .acts {
    display: flex;
    gap: var(--s2);
    margin-top: 6px;
  }
</style>
