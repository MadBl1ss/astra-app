<script lang="ts">
  import Icon from '../components/Icon.svelte';
  import Mark from '../components/Mark.svelte';
  import { ru } from '../i18n/ru';
  import { inWindow } from '../transport';

  // The window has no system frame; this strip is its caption. Dragging is
  // done by Tauri for elements marked data-tauri-drag-region. In a browser
  // tab the buttons have nothing to act on and do nothing.
  async function act(what: 'minimize' | 'close'): Promise<void> {
    if (!inWindow) return;
    const { getCurrentWindow } = await import('@tauri-apps/api/window');
    await getCurrentWindow()[what]();
  }
</script>

<header class="titlebar" data-tauri-drag-region>
  <span class="brand" data-tauri-drag-region>
    <Mark size={16} />
    <span class="name" data-tauri-drag-region>{ru.app.name}</span>
  </span>
  <span class="grow" data-tauri-drag-region></span>
  <button type="button" class="wb" aria-label={ru.app.minimize} title={ru.app.minimize} onclick={() => act('minimize')}>
    <Icon name="minimize" size={16} />
  </button>
  <!-- Asking to close is answered by the shell with hiding to the tray, the
       same as Alt+F4; quitting is in the tray menu. -->
  <button type="button" class="wb" aria-label={ru.app.close} title={ru.app.close} onclick={() => act('close')}>
    <Icon name="close" size={16} />
  </button>
</header>

<style>
  .titlebar {
    display: flex;
    align-items: center;
    gap: var(--s2);
    height: 36px;
    padding: 0 4px 0 12px;
    border-bottom: 1px solid var(--line);
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .brand {
    display: flex;
    align-items: center;
    gap: var(--s2);
    color: var(--text);
    /* The icon must not swallow the drag. */
    pointer-events: none;
  }

  .name {
    color: var(--text);
    font-weight: 600;
    letter-spacing: 0.01em;
  }

  .grow {
    flex: 1;
    align-self: stretch;
  }

  .wb {
    display: grid;
    place-items: center;
    width: 40px;
    height: 28px;
    padding: 0;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    color: var(--muted);
    transition:
      background var(--fast),
      color var(--fast);
  }

  .wb:hover {
    background: var(--surface-2);
    color: var(--text);
  }

  .wb:focus-visible {
    outline-offset: -2px;
  }
</style>
