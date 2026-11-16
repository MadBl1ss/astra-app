<script lang="ts" module>
  let seq = 0;
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';

  // A tooltip. The element it explains goes inside; the text appears on
  // hover and on keyboard focus and is announced through aria-describedby.
  let {
    text,
    side = 'top',
    children,
  }: {
    text: string;
    side?: 'top' | 'bottom';
    children: Snippet<[describedby: string]>;
  } = $props();

  const id = `hint-${++seq}`;
  let shown = $state(false);
</script>

<span
  class="hint"
  role="presentation"
  onpointerenter={() => (shown = true)}
  onpointerleave={() => (shown = false)}
  onfocusin={() => (shown = true)}
  onfocusout={() => (shown = false)}
  onkeydown={(event) => event.key === 'Escape' && (shown = false)}
>
  {@render children(id)}
  <span {id} class="tip {side}" class:shown role="tooltip">{text}</span>
</span>

<style>
  .hint {
    position: relative;
    display: inline-flex;
    min-width: 0;
  }

  .tip {
    position: absolute;
    z-index: 30;
    right: 0;
    width: max-content;
    max-width: 260px;
    padding: 6px 9px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
    box-shadow: var(--shadow-pop);
    color: var(--text);
    font-size: var(--fs-cap);
    line-height: 1.4;
    white-space: normal;
    pointer-events: none;
    opacity: 0;
    visibility: hidden;
    transition:
      opacity var(--fast),
      visibility var(--fast);
  }

  .top {
    bottom: calc(100% + 6px);
  }

  .bottom {
    top: calc(100% + 6px);
  }

  .shown {
    opacity: 1;
    visibility: visible;
    /* Do not flash while the pointer merely crosses the element. */
    transition-delay: 350ms;
  }
</style>
