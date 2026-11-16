<script lang="ts" module>
  import type { IconName } from './Icon.svelte';

  export interface MenuItem {
    label: string;
    icon?: IconName;
    danger?: boolean;
    disabled?: boolean;
    onselect: () => void;
  }

  let seq = 0;
</script>

<script lang="ts">
  import Icon from './Icon.svelte';
  import IconButton from './IconButton.svelte';

  // The "⋯" button and its short list of actions.
  let { label, items }: { label: string; items: readonly MenuItem[] } = $props();

  const id = `menu-${++seq}`;
  let open = $state(false);
  let root: HTMLDivElement | undefined = $state();
  let trigger: HTMLButtonElement | undefined = $state();
  let list: HTMLUListElement | undefined = $state();

  function buttons(): HTMLButtonElement[] {
    return [...(list?.querySelectorAll<HTMLButtonElement>('button:not(:disabled)') ?? [])];
  }

  function show(): void {
    open = true;
    // The list exists after this tick; the first action gets the focus.
    queueMicrotask(() => requestAnimationFrame(() => buttons()[0]?.focus()));
  }

  function hide(refocus: boolean): void {
    open = false;
    if (refocus) trigger?.focus();
  }

  function pick(item: MenuItem): void {
    hide(true);
    item.onselect();
  }

  function onkeydown(event: KeyboardEvent): void {
    const all = buttons();
    const at = all.indexOf(document.activeElement as HTMLButtonElement);
    switch (event.key) {
      case 'ArrowDown':
        all[(at + 1) % all.length]?.focus();
        break;
      case 'ArrowUp':
        all[(at - 1 + all.length) % all.length]?.focus();
        break;
      case 'Home':
        all[0]?.focus();
        break;
      case 'End':
        all[all.length - 1]?.focus();
        break;
      case 'Escape':
        hide(true);
        break;
      case 'Tab':
        hide(false);
        return;
      default:
        return;
    }
    event.preventDefault();
    event.stopPropagation();
  }

  $effect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => {
      if (root && !root.contains(event.target as Node)) hide(false);
    };
    document.addEventListener('pointerdown', away, true);
    return () => document.removeEventListener('pointerdown', away, true);
  });
</script>

<div class="menu" bind:this={root}>
  <IconButton
    bind:element={trigger}
    icon="more"
    {label}
    onclick={() => (open ? hide(false) : show())}
    onkeydown={(event) => {
      if (event.key === 'ArrowDown' && !open) {
        event.preventDefault();
        show();
      }
    }}
  />
  {#if open}
    <!-- svelte-ignore a11y_interactive_supports_focus -->
    <ul {id} role="menu" aria-label={label} bind:this={list} {onkeydown}>
      {#each items as item (item.label)}
        <li role="none">
          <button type="button" role="menuitem" class:danger={item.danger} disabled={item.disabled} onclick={() => pick(item)}>
            {#if item.icon}<Icon name={item.icon} size={15} />{/if}
            {item.label}
          </button>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .menu {
    position: relative;
  }

  ul {
    position: absolute;
    z-index: 25;
    top: calc(100% + 4px);
    right: 0;
    min-width: 190px;
    margin: 0;
    padding: 4px;
    list-style: none;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface);
    box-shadow: var(--shadow-pop);
    animation: pop var(--fast) ease-out;
  }

  @keyframes pop {
    from {
      opacity: 0;
      transform: translateY(-3px);
    }
  }

  button {
    display: flex;
    align-items: center;
    gap: var(--s2);
    width: 100%;
    min-height: 30px;
    padding: 0 8px;
    border: 0;
    border-radius: 4px;
    background: transparent;
    text-align: left;
    white-space: nowrap;
  }

  button:hover:not(:disabled),
  button:focus-visible {
    background: var(--surface-2);
    outline: none;
  }

  button :global(svg) {
    color: var(--muted);
  }

  .danger,
  .danger :global(svg) {
    color: var(--bad);
  }

  button:disabled {
    color: var(--muted);
  }
</style>
