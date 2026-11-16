<script lang="ts" generics="T extends string">
  let {
    options,
    value,
    label,
    disabled = false,
    onchange,
  }: {
    options: readonly { value: T; label: string }[];
    value: T;
    label: string;
    disabled?: boolean;
    onchange: (value: T) => void;
  } = $props();

  let root: HTMLDivElement | undefined = $state();

  // One tab stop for the group, arrows move inside it, like native radios.
  function onkeydown(event: KeyboardEvent): void {
    const step = event.key === 'ArrowRight' || event.key === 'ArrowDown' ? 1 : event.key === 'ArrowLeft' || event.key === 'ArrowUp' ? -1 : 0;
    if (step === 0) return;
    event.preventDefault();
    const at = options.findIndex((o) => o.value === value);
    const next = options[(at + step + options.length) % options.length];
    if (!next) return;
    onchange(next.value);
    root?.querySelector<HTMLButtonElement>(`[data-value="${next.value}"]`)?.focus();
  }
</script>

<div
  bind:this={root}
  class="seg"
  role="radiogroup"
  tabindex="-1"
  aria-label={label}
  style:grid-template-columns="repeat({options.length}, 1fr)"
  {onkeydown}
>
  {#each options as option (option.value)}
    <button
      type="button"
      role="radio"
      data-value={option.value}
      aria-checked={option.value === value}
      tabindex={option.value === value ? 0 : -1}
      {disabled}
      onclick={() => option.value !== value && onchange(option.value)}
    >
      {option.label}
    </button>
  {/each}
</div>

<style>
  .seg {
    display: grid;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    /* Clip the fills to the rounded corners without hiding the focus ring. */
    background: var(--surface-2);
  }

  button {
    min-height: 29px;
    padding: 4px 4px;
    border: 0;
    border-radius: calc(var(--r-ctl) - 1px);
    background: transparent;
    color: var(--muted);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    transition:
      background var(--fast),
      color var(--fast);
  }

  button:hover:not([aria-checked='true']):not(:disabled) {
    color: var(--text);
  }

  button[aria-checked='true'] {
    background: var(--text);
    color: var(--bg);
  }

  button:focus-visible {
    outline-offset: 1px;
    position: relative;
  }
</style>
