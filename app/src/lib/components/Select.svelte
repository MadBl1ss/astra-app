<script lang="ts" module>
  export interface SelectItem {
    value: string;
    label: string;
    /** A flag emoji before the label. */
    flag?: string | null;
    /** A right-aligned note, e.g. a ping. */
    meta?: string;
    metaTone?: 'ok' | 'warn' | 'bad' | 'none';
  }

  let seq = 0;
</script>

<script lang="ts">
  import Icon from './Icon.svelte';

  let {
    items,
    value,
    label,
    placeholder,
    disabled = false,
    onchange,
  }: {
    items: readonly SelectItem[];
    value: string | null;
    label: string;
    placeholder: string;
    disabled?: boolean;
    onchange: (value: string) => void;
  } = $props();

  const id = `select-${++seq}`;
  let open = $state(false);
  let active = $state(0);
  let root: HTMLDivElement | undefined = $state();
  let list: HTMLUListElement | undefined = $state();

  const current = $derived(items.find((item) => item.value === value));

  function show(): void {
    if (disabled || items.length === 0) return;
    active = Math.max(0, items.findIndex((item) => item.value === value));
    open = true;
  }

  function pick(item: SelectItem): void {
    open = false;
    if (item.value !== value) onchange(item.value);
  }

  function onkeydown(event: KeyboardEvent): void {
    if (!open) {
      if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
        event.preventDefault();
        show();
      }
      return;
    }
    switch (event.key) {
      case 'ArrowDown':
        active = (active + 1) % items.length;
        break;
      case 'ArrowUp':
        active = (active - 1 + items.length) % items.length;
        break;
      case 'Home':
        active = 0;
        break;
      case 'End':
        active = items.length - 1;
        break;
      case 'Enter':
      case ' ': {
        const item = items[active];
        if (item) pick(item);
        break;
      }
      case 'Escape':
      case 'Tab':
        open = false;
        // Tab moves on as usual; Escape stays on the button.
        if (event.key === 'Tab') return;
        break;
      default:
        return;
    }
    event.preventDefault();
  }

  // Keep the highlighted row in view while arrowing through a long list.
  $effect(() => {
    if (open) list?.children[active]?.scrollIntoView({ block: 'nearest' });
  });

  // A click anywhere else closes the list.
  $effect(() => {
    if (!open) return;
    const away = (event: PointerEvent) => {
      if (root && !root.contains(event.target as Node)) open = false;
    };
    document.addEventListener('pointerdown', away, true);
    return () => document.removeEventListener('pointerdown', away, true);
  });
</script>

<div class="select" bind:this={root}>
  <button
    type="button"
    class="pick"
    role="combobox"
    aria-label={label}
    aria-haspopup="listbox"
    aria-expanded={open}
    aria-controls="{id}-list"
    aria-activedescendant={open ? `${id}-${active}` : undefined}
    {disabled}
    onclick={() => (open ? (open = false) : show())}
    {onkeydown}
  >
    {#if current}
      {#if current.flag}<span class="flag">{current.flag}</span>{/if}
      <span class="grow">{current.label}</span>
      {#if current.meta}<span class="meta {current.metaTone ?? 'none'}">{current.meta}</span>{/if}
    {:else}
      <span class="grow placeholder">{placeholder}</span>
    {/if}
    <span class="chev" class:open><Icon name="chevron" size={14} /></span>
  </button>

  {#if open}
    <ul class="list" id="{id}-list" role="listbox" aria-label={label} bind:this={list}>
      {#each items as item, i (item.value)}
        <!-- The keyboard is handled on the button, which keeps the focus. -->
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <li
          id="{id}-{i}"
          role="option"
          aria-selected={item.value === value}
          class:active={i === active}
          onclick={() => pick(item)}
          onpointermove={() => (active = i)}
        >
          <span class="flag">{item.flag ?? ''}</span>
          <span class="grow">{item.label}</span>
          {#if item.meta}<span class="meta {item.metaTone ?? 'none'}">{item.meta}</span>{/if}
          <span class="tick">{#if item.value === value}<Icon name="check" size={14} />{/if}</span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  .select {
    position: relative;
    min-width: 0;
  }

  .pick {
    display: flex;
    align-items: center;
    gap: var(--s2);
    width: 100%;
    min-height: 31px;
    padding: 5px 9px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
    text-align: left;
    transition: border-color var(--fast);
  }

  .pick:hover:not(:disabled),
  .pick[aria-expanded='true'] {
    border-color: var(--muted);
  }

  .pick:disabled {
    color: var(--muted);
  }

  .grow {
    flex: 1;
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .placeholder {
    color: var(--muted);
  }

  /* A fixed cell, so that names line up whether or not a flag is known. */
  .flag {
    flex: none;
    width: 20px;
    font-size: 15px;
    line-height: 1;
    text-align: center;
  }

  .meta {
    flex: none;
    color: var(--muted);
  }

  .meta.ok {
    color: var(--vpn);
  }

  .meta.warn {
    color: var(--warn);
  }

  .meta.bad {
    color: var(--bad);
  }

  .chev {
    display: flex;
    color: var(--muted);
    transition: transform var(--fast);
  }

  .chev.open {
    transform: rotate(180deg);
  }

  .list {
    position: absolute;
    z-index: 20;
    top: calc(100% + 4px);
    left: 0;
    right: 0;
    max-height: 228px;
    margin: 0;
    padding: 4px;
    overflow-y: auto;
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

  li {
    display: flex;
    align-items: center;
    gap: var(--s2);
    min-height: 30px;
    padding: 0 6px;
    border-radius: 4px;
  }

  li.active {
    background: var(--surface-2);
  }

  .tick {
    display: flex;
    flex: none;
    width: 14px;
  }
</style>
