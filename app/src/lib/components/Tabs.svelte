<script lang="ts" generics="T extends string">
  // Tabs over one panel. The panel is the caller's; it is labelled by the
  // selected tab through `panel` and `tabId`. Arrows move between tabs.
  let {
    tabs,
    value,
    label,
    panel,
    onchange,
  }: {
    tabs: readonly { value: T; label: string; count?: number }[];
    value: T;
    label: string;
    /** id of the element the tabs control. */
    panel: string;
    onchange: (value: T) => void;
  } = $props();

  let root: HTMLDivElement | undefined = $state();

  function onkeydown(event: KeyboardEvent): void {
    const step = event.key === 'ArrowRight' ? 1 : event.key === 'ArrowLeft' ? -1 : 0;
    if (step === 0) return;
    event.preventDefault();
    const at = tabs.findIndex((t) => t.value === value);
    const next = tabs[(at + step + tabs.length) % tabs.length];
    if (!next) return;
    onchange(next.value);
    root?.querySelector<HTMLButtonElement>(`[data-value="${next.value}"]`)?.focus();
  }
</script>

<div class="tabs" role="tablist" aria-label={label} tabindex="-1" bind:this={root} {onkeydown}>
  {#each tabs as tab (tab.value)}
    <button
      type="button"
      role="tab"
      id="{panel}-tab-{tab.value}"
      data-value={tab.value}
      aria-selected={tab.value === value}
      aria-controls={panel}
      tabindex={tab.value === value ? 0 : -1}
      onclick={() => tab.value !== value && onchange(tab.value)}
    >
      {tab.label}
      {#if tab.count !== undefined}<span class="n">{tab.count}</span>{/if}
    </button>
  {/each}
</div>

<style>
  .tabs {
    display: flex;
    gap: 2px;
    border-bottom: 1px solid var(--line);
  }

  button {
    display: flex;
    align-items: baseline;
    gap: 6px;
    margin-bottom: -1px;
    padding: 6px 10px 7px;
    border: 0;
    border-bottom: 2px solid transparent;
    background: transparent;
    color: var(--muted);
    transition:
      color var(--fast),
      border-color var(--fast);
  }

  button:hover {
    color: var(--text);
  }

  /* The accent is neutral, as everywhere: full text and a light underline. */
  button[aria-selected='true'] {
    border-bottom-color: var(--text);
    color: var(--text);
    font-weight: 600;
  }

  button:focus-visible {
    outline-offset: -2px;
  }

  .n {
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }
</style>
