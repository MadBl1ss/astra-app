<script lang="ts">
  import type { Snippet } from 'svelte';

  let {
    variant = 'default',
    disabled = false,
    onclick,
    children,
  }: {
    variant?: 'default' | 'ghost';
    disabled?: boolean;
    onclick?: () => void;
    children: Snippet;
  } = $props();
</script>

<button type="button" class="btn" class:ghost={variant === 'ghost'} {disabled} {onclick}>
  {@render children()}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    gap: var(--s2);
    min-height: 28px;
    padding: 4px 11px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
    white-space: nowrap;
    transition: border-color var(--fast);
  }

  .btn:hover:not(:disabled) {
    border-color: var(--muted);
  }

  .btn:disabled {
    color: var(--muted);
  }

  .ghost {
    background: transparent;
  }
</style>
