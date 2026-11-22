<script lang="ts" module>
  let seq = 0;
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';

  // A card that opens next to what it explains, on hover and on keyboard
  // focus, like Hint, but with a picture of its own. It lives in <body>, not
  // in its row: a list that scrolls, or a group that is laid out only when
  // on screen (content-visibility), would cut it off at its edge.
  let {
    width = 300,
    summary,
    trigger,
    card,
  }: {
    width?: number;
    /** The card's content in words, for readers; it is what aria-describedby points at. */
    summary: string;
    trigger: Snippet<[describedby: string]>;
    card: Snippet;
  } = $props();

  const id = `popcard-${++seq}`;
  let anchor: HTMLSpanElement | undefined = $state();
  let shown = $state(false);
  let x = $state(0);
  let y = $state(0);
  let below = $state(false);
  let timer: ReturnType<typeof setTimeout> | undefined;

  function open(delay: number): void {
    clearTimeout(timer);
    timer = setTimeout(() => {
      const r = anchor!.getBoundingClientRect();
      x = Math.min(Math.max(8, r.right - width), window.innerWidth - width - 8);
      shown = true;
    }, delay);
  }

  /** Above the anchor if the card fits there, else below it. */
  function settle(node: HTMLElement): void {
    const r = anchor!.getBoundingClientRect();
    const h = node.offsetHeight;
    below = r.top - 8 - h < 8 && r.bottom + 8 + h <= window.innerHeight - 8;
    y = below ? r.bottom + 8 : Math.max(8, r.top - 8 - h);
  }

  function close(): void {
    clearTimeout(timer);
    shown = false;
  }

  $effect(() => () => clearTimeout(timer));

  /** Moves the card to <body>, where Svelte removes it from as from anywhere, and places it. */
  function portal(node: HTMLElement): void {
    document.body.appendChild(node);
    settle(node);
  }
</script>

<span
  bind:this={anchor}
  class="anchor"
  role="presentation"
  onpointerenter={() => open(320)}
  onpointerleave={close}
  onfocusin={() => open(0)}
  onfocusout={close}
  onkeydown={(event) => event.key === 'Escape' && close()}
>
  {@render trigger(id)}
  <span {id} class="sr-only">{summary}</span>
</span>
{#if shown}
  <span
    use:portal
    class="card"
    class:below
    style:width="{width}px"
    style:left="{x}px"
    style:top="{y}px"
    aria-hidden="true"
  >
    {@render card()}
  </span>
{/if}

<style>
  .anchor {
    display: inline-flex;
    flex: none;
  }

  .card {
    display: block;
    position: fixed;
    z-index: 40;
    padding: 12px 14px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
    box-shadow: var(--shadow-pop);
    color: var(--text);
    font-size: var(--fs-cap);
    pointer-events: none;
    transform-origin: bottom right;
    animation: pop 0.18s cubic-bezier(0.2, 0.8, 0.2, 1);
  }

  .card.below {
    transform-origin: top right;
  }

  @keyframes pop {
    from {
      opacity: 0;
      transform: translateY(4px) scale(0.97);
    }
  }

  .below {
    animation-name: drop;
  }

  @keyframes drop {
    from {
      opacity: 0;
      transform: translateY(-4px) scale(0.97);
    }
  }
</style>
