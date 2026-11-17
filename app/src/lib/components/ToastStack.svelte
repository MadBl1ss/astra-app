<script lang="ts">
  import { fly } from 'svelte/transition';
  import { ru } from '../i18n/ru';
  import type { ToastView } from '../toasts';
  import ToastCard from './ToastCard.svelte';

  let {
    view,
    onaction,
    ondismiss,
    onmore,
    onhover,
  }: {
    view: ToastView;
    onaction: (id: string, action: string) => void;
    ondismiss: (id: string) => void;
    onmore: () => void;
    onhover: (on: boolean) => void;
  } = $props();

  // Cards slide in from the edge of the screen, the way they would come
  // from the tray; with reduced motion they just appear.
  const still = typeof matchMedia === 'function' && matchMedia('(prefers-reduced-motion: reduce)').matches;
  const motion = { x: still ? 0 : 40, duration: still ? 0 : 180, opacity: 0 };
</script>

<section
  class="stack"
  aria-label={ru.toasts.region}
  onpointerenter={() => onhover(true)}
  onpointerleave={() => onhover(false)}
>
  {#if view.more > 0}
    <button type="button" class="more" title={ru.toasts.moreHint} onclick={onmore}>{ru.toasts.more(view.more)}</button>
  {/if}
  {#each view.cards as card (card.id)}
    <div class="slot" transition:fly={motion}>
      <ToastCard {card} onaction={(action) => onaction(card.id, action)} ondismiss={() => ondismiss(card.id)} />
    </div>
  {/each}
</section>

<style>
  .stack {
    display: flex;
    flex-direction: column;
    justify-content: flex-end;
    align-items: stretch;
    gap: var(--s2);
    width: 336px;
  }

  .more {
    align-self: flex-end;
    height: 24px;
    padding: 0 10px;
    border: 1px solid var(--line);
    border-radius: 12px;
    background: var(--surface);
    color: var(--muted);
    font: inherit;
    font-size: var(--fs-cap);
    cursor: pointer;
  }

  .more:hover {
    color: var(--text);
    background: var(--surface-2);
  }
</style>
