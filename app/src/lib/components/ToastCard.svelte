<script lang="ts">
  import { untrack } from 'svelte';
  import { ru } from '../i18n/ru';
  import type { Card } from '../toasts';
  import Icon from './Icon.svelte';

  let {
    card,
    onaction,
    ondismiss,
  }: {
    card: Card;
    onaction: (action: string) => void;
    ondismiss: () => void;
  } = $props();

  const icon = $derived(card.severity === 'info' ? 'info' : 'alert');
  // A replaced card flashes once, so a new text in the same place is seen.
  let flash = $state(false);
  let seen = untrack(() => card.rev);
  $effect(() => {
    if (card.rev === seen) return;
    seen = card.rev;
    flash = true;
    const timer = setTimeout(() => (flash = false), 400);
    return () => clearTimeout(timer);
  });
</script>

<!-- Status, not alert: a toast must not interrupt a screen reader any more
     than it interrupts the user; critical ones are the exception. -->
<article
  class="toast {card.severity}"
  class:flash
  role={card.severity === 'critical' ? 'alert' : 'status'}
  aria-label={ru.toasts.severity[card.severity]}
>
  <span class="ico" aria-hidden="true"><Icon name={icon} size={18} /></span>
  <div class="text">
    <h3>{card.title}</h3>
    {#if card.body}<p>{card.body}</p>{/if}
    {#if card.actions.length > 0}
      <div class="actions">
        {#each card.actions as action, i (action.id)}
          <button type="button" class:primary={i === 0} onclick={() => onaction(action.id)}>{action.label}</button>
        {/each}
      </div>
    {/if}
  </div>
  <button type="button" class="close" aria-label={ru.toasts.close} title={ru.toasts.close} onclick={ondismiss}>
    <Icon name="close" size={14} />
  </button>
</article>

<style>
  .toast {
    --tone: var(--direct);
    position: relative;
    display: grid;
    grid-template-columns: 22px 1fr 24px;
    gap: 10px;
    padding: 12px 10px 12px 14px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
    box-shadow: 0 6px 20px rgb(0 0 0 / 0.28);
    color: var(--text);
    overflow: hidden;
  }

  :global([data-theme='light']) .toast {
    box-shadow: 0 6px 20px rgb(20 23 28 / 0.14);
  }

  /* The colour of the severity is a thin bar and the icon, never the text:
     the words say what happened. */
  .toast::before {
    content: '';
    position: absolute;
    inset: 0 auto 0 0;
    width: 3px;
    background: var(--tone);
  }

  .critical {
    --tone: var(--bad);
    border-color: color-mix(in srgb, var(--bad) 45%, var(--line));
  }

  .problem {
    --tone: var(--warn);
  }

  .flash {
    animation: flash 400ms ease-out;
  }

  @keyframes flash {
    from {
      background: color-mix(in srgb, var(--tone) 14%, var(--surface));
    }
  }

  .ico {
    display: flex;
    padding-top: 1px;
    color: var(--tone);
  }

  .text {
    min-width: 0;
  }

  h3 {
    font-size: var(--fs-body);
    font-weight: 600;
    line-height: 1.35;
    overflow-wrap: anywhere;
  }

  p {
    margin-top: 2px;
    color: var(--muted);
    font-size: var(--fs-body);
    line-height: 1.4;
    /* One or two lines: the journal has the rest. */
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
    overflow-wrap: anywhere;
  }

  .actions {
    display: flex;
    flex-wrap: wrap;
    gap: var(--s2);
    margin-top: 10px;
  }

  .actions button {
    height: 28px;
    padding: 0 12px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: transparent;
    color: var(--text);
    font: inherit;
    font-size: var(--fs-body);
    white-space: nowrap;
    cursor: pointer;
  }

  .actions button.primary {
    border-color: transparent;
    background: var(--surface-2);
    font-weight: 600;
  }

  .actions button:hover {
    background: var(--surface-2);
    border-color: color-mix(in srgb, var(--text) 25%, var(--line));
  }

  .close {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    color: var(--muted);
    cursor: pointer;
  }

  .close:hover {
    background: var(--surface-2);
    color: var(--text);
  }
</style>
