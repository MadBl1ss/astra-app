<script lang="ts">
  // The page of the toast window. It knows nothing of the daemon: the main
  // page sends it what to draw and gets the clicks back. It only measures
  // itself, so that the window is exactly as tall as its cards - the
  // transparent part of a window still catches the mouse.
  import { invoke } from '@tauri-apps/api/core';
  import { emit, listen } from '@tauri-apps/api/event';
  import { onMount, tick } from 'svelte';
  import ToastStack from '../components/ToastStack.svelte';
  import { EMPTY_VIEW, TOASTS_EVENTS, type ShowPayload } from './toastwire';

  let view = $state(EMPTY_VIEW);
  let box: HTMLDivElement;
  let last = -1;

  /** Tells the shell how tall the window must be; 0 hides it. */
  function place(): void {
    const height = view.cards.length === 0 && view.more === 0 ? 0 : Math.ceil(box.getBoundingClientRect().height);
    if (height === last) return;
    last = height;
    void invoke('toasts_place', { height });
  }

  // A hidden window may draw no frames, and then no resize is observed:
  // the first card is measured right after it is in the page.
  $effect(() => {
    void view;
    void tick().then(place);
  });

  onMount(() => {
    const off = listen<ShowPayload>(TOASTS_EVENTS.show, (event) => {
      document.documentElement.dataset.theme = event.payload.theme;
      view = event.payload.view;
    });
    // Whatever is on screen already, in case this page loaded after the
    // main one had sent it.
    void emit(TOASTS_EVENTS.ready);

    // Cards that leave shrink the stack only when their animation is over.
    const measure = new ResizeObserver(place);
    measure.observe(box);
    return () => {
      measure.disconnect();
      void off.then((stop) => stop());
    };
  });
</script>

<div class="box" bind:this={box}>
  <ToastStack
    {view}
    onaction={(id, action) => void emit(TOASTS_EVENTS.action, { id, action })}
    ondismiss={(id) => void emit(TOASTS_EVENTS.action, { id, action: 'dismiss' })}
    onmore={() => void emit(TOASTS_EVENTS.action, { id: '', action: 'more' })}
    onhover={(on) => void emit(TOASTS_EVENTS.hover, on)}
  />
</div>

<style>
  /* The window is transparent; only the cards are drawn. The padding is
     room for their shadow. */
  :global(html),
  :global(body) {
    background: transparent !important;
    overflow: hidden;
  }

  .box {
    position: fixed;
    right: 0;
    bottom: 0;
    padding: 12px;
  }
</style>
