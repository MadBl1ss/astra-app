<script lang="ts" module>
  let seq = 0;
</script>

<script lang="ts">
  import type { Snippet } from 'svelte';

  // A modal window over the window. The browser's own <dialog> keeps focus
  // inside and answers Escape; the parent mounts this only while it is open
  // and is told to close it, so its state stays the one truth.
  let {
    title,
    width = 440,
    onclose,
    children,
    actions,
  }: {
    title: string;
    width?: number;
    onclose: () => void;
    children: Snippet;
    actions?: Snippet;
  } = $props();

  const id = `dialog-${++seq}`;
  let el: HTMLDialogElement | undefined = $state();
  // Closing from here also fires "close"; the parent has already been told.
  let leaving = false;

  $effect(() => {
    const dialog = el!;
    const back = document.activeElement as HTMLElement | null;
    dialog.showModal();
    return () => {
      leaving = true;
      dialog.close();
      // Back where the user was, e.g. on the button that opened it.
      back?.focus?.();
    };
  });

  function cancel(event: Event): void {
    event.preventDefault();
    onclose();
  }
</script>

<!-- A click on the dimmed backdrop lands on the dialog element itself. -->
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog
  bind:this={el}
  aria-labelledby={id}
  style:width="{width}px"
  oncancel={cancel}
  onclose={() => leaving || onclose()}
  onclick={(event) => event.target === el && onclose()}
>
  <div class="body">
    <h2 {id}>{title}</h2>
    {@render children()}
    {#if actions}<div class="acts">{@render actions()}</div>{/if}
  </div>
</dialog>

<style>
  dialog {
    max-width: calc(100vw - 48px);
    padding: 0;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
    box-shadow: var(--shadow-pop);
    color: var(--text);
    animation: appear var(--slow) ease-out;
  }

  dialog::backdrop {
    background: rgb(0 0 0 / 0.45);
    animation: fade var(--slow) ease-out;
  }

  @keyframes appear {
    from {
      opacity: 0;
      transform: translateY(6px);
    }
  }

  @keyframes fade {
    from {
      opacity: 0;
    }
  }

  .body {
    display: grid;
    gap: var(--s3);
    padding: 16px 18px;
  }

  h2 {
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .acts {
    display: flex;
    justify-content: flex-end;
    gap: var(--s2);
    margin-top: var(--s1);
  }
</style>
