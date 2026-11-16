<script lang="ts">
  import type { Snippet } from 'svelte';
  import Icon, { type IconName } from './Icon.svelte';

  let {
    variant = 'default',
    disabled = false,
    busy = false,
    icon,
    type = 'button',
    title,
    onclick,
    children,
  }: {
    /** danger: an action that deletes something; red outline, never filled. */
    variant?: 'default' | 'ghost' | 'danger';
    disabled?: boolean;
    /** The action is under way: a spinner instead of the icon, no second click. */
    busy?: boolean;
    icon?: IconName;
    type?: 'button' | 'submit';
    title?: string;
    onclick?: () => void;
    children: Snippet;
  } = $props();
</script>

<button
  {type}
  class="btn {variant}"
  disabled={disabled || busy}
  aria-busy={busy || undefined}
  {title}
  {onclick}
>
  {#if busy}
    <span class="spin" aria-hidden="true"></span>
  {:else if icon}
    <Icon name={icon} size={15} />
  {/if}
  {@render children()}
</button>

<style>
  .btn {
    display: inline-flex;
    align-items: center;
    gap: 6px;
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

  .danger {
    border-color: color-mix(in srgb, var(--bad) 55%, var(--line));
    background: transparent;
    color: var(--bad);
  }

  .danger:hover:not(:disabled) {
    border-color: var(--bad);
  }

  .spin {
    width: 13px;
    height: 13px;
    border: 2px solid var(--line);
    border-top-color: var(--text);
    border-radius: 50%;
    animation: turn 0.8s linear infinite;
  }

  @keyframes turn {
    to {
      transform: rotate(1turn);
    }
  }
</style>
