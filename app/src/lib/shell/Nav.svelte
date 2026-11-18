<script lang="ts" module>
  import type { IconName } from '../components/Icon.svelte';

  export type Section = 'home' | 'servers' | 'rules' | 'journal' | 'settings';

  /** In the order of the column: Ctrl+1 is the first. */
  export const SECTIONS: readonly Section[] = ['home', 'servers', 'rules', 'journal', 'settings'];

  const top: readonly { id: Section; icon: IconName }[] = [
    { id: 'home', icon: 'home' },
    { id: 'servers', icon: 'servers' },
    { id: 'rules', icon: 'rules' },
    { id: 'journal', icon: 'journal' },
  ];
</script>

<script lang="ts">
  import Icon from '../components/Icon.svelte';
  import { ru } from '../i18n/ru';
  import { updates } from '../update.svelte';

  let { section, onselect }: { section: Section; onselect: (section: Section) => void } = $props();
</script>

{#snippet item(id: Section, icon: IconName)}
  <button
    type="button"
    aria-current={section === id ? 'page' : undefined}
    aria-keyshortcuts="Control+{SECTIONS.indexOf(id) + 1}"
    title="{ru.nav[id]} (Ctrl+{SECTIONS.indexOf(id) + 1})"
    onclick={() => onselect(id)}
  >
    <Icon name={icon} />
    {ru.nav[id]}
  </button>
{/snippet}

<nav aria-label={ru.nav.label}>
  {#each top as entry (entry.id)}
    {@render item(entry.id, entry.icon)}
  {/each}
  <span class="grow"></span>
  {#if updates.available}
    <!-- Not a section: it opens the dialog of the new version. -->
    <button type="button" class="update" title={ru.update.navHint(updates.available.version)} onclick={() => updates.show()}>
      <span class="dot-wrap"><Icon name="download" /><span class="dot" aria-hidden="true"></span></span>
      {ru.update.nav}
    </button>
  {/if}
  {@render item('settings', 'settings')}
</nav>

<style>
  nav {
    display: flex;
    flex-direction: column;
    gap: 2px;
    padding: 10px 8px;
    border-right: 1px solid var(--line);
  }

  button {
    display: grid;
    justify-items: center;
    gap: var(--s1);
    padding: 9px 0 7px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: var(--muted);
    font-size: 11px;
    transition:
      background var(--fast),
      color var(--fast);
  }

  button:hover {
    background: var(--surface);
  }

  /* The accent is neutral: a lighter plate and full-strength text. */
  button[aria-current='page'] {
    background: var(--surface-2);
    color: var(--text);
  }

  button:focus-visible {
    outline-offset: -2px;
  }

  .grow {
    flex: 1;
  }

  /* Seen, not shouting: full-strength text and a dot, no blinking. */
  .update {
    color: var(--text);
  }

  .dot-wrap {
    position: relative;
    display: flex;
  }

  .dot {
    position: absolute;
    top: -2px;
    right: -3px;
    width: 7px;
    height: 7px;
    border: 1.5px solid var(--bg);
    border-radius: 50%;
    background: var(--text);
  }
</style>
