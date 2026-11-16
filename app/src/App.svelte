<script lang="ts">
  import Icon from './lib/components/Icon.svelte';
  import { ru } from './lib/i18n/ru';
  import Home from './lib/screens/Home.svelte';
  import Journal from './lib/screens/Journal.svelte';
  import NoDaemon from './lib/screens/NoDaemon.svelte';
  import Rules from './lib/screens/Rules.svelte';
  import Servers from './lib/screens/Servers.svelte';
  import Settings from './lib/screens/Settings.svelte';
  import Nav, { SECTIONS, type Section } from './lib/shell/Nav.svelte';
  import Titlebar from './lib/shell/Titlebar.svelte';
  import { daemon } from './lib/store.svelte';

  let section = $state<Section>('home');

  // Ctrl+1…5 open the sections in the order of the column. A dialog that is
  // open keeps the keys to itself: its page must not change under it.
  function onkeydown(event: KeyboardEvent): void {
    if (!event.ctrlKey || event.altKey || event.shiftKey || event.metaKey) return;
    const next = SECTIONS[Number(event.key) - 1];
    if (!next || !daemon.ready || document.querySelector('dialog[open]')) return;
    event.preventDefault();
    section = next;
  }

  // A daemon that restarts is gone for a moment. Showing "the service does
  // not answer" for that moment would be an alarm about nothing, so the last
  // picture stays until the silence has lasted.
  const GRACE_MS = 1500;
  let lost = $state(false);
  $effect(() => {
    if (daemon.link === 'connected') {
      lost = false;
      return;
    }
    const timer = setTimeout(() => (lost = true), GRACE_MS);
    return () => clearTimeout(timer);
  });
</script>

<svelte:window {onkeydown} />

<div class="window">
  <Titlebar />

  {#if daemon.outdated || lost}
    <NoDaemon />
  {:else if daemon.state}
    <div class="body">
      <Nav {section} onselect={(next) => (section = next)} />
      <main>
        {#if section === 'home'}
          <Home state={daemon.state} onjournal={() => (section = 'journal')} />
        {:else if section === 'servers'}
          <Servers state={daemon.state} />
        {:else if section === 'rules'}
          <Rules state={daemon.state} />
        {:else if section === 'journal'}
          <Journal state={daemon.state} />
        {:else}
          <Settings state={daemon.state} />
        {/if}
      </main>
    </div>
  {:else}
    <p class="loading">{ru.noDaemon.connecting}</p>
  {/if}

  {#if daemon.notice}
    <div class="notice" role="alert">
      <span class="ico"><Icon name="alert" size={16} /></span>
      <span class="grow">{daemon.notice}</span>
      <button type="button" aria-label={ru.errors.dismiss} onclick={() => daemon.dismissNotice()}>
        <Icon name="close" size={14} />
      </button>
    </div>
  {/if}
</div>

<style>
  .window {
    position: relative;
    display: grid;
    grid-template-rows: 36px 1fr;
    height: 100%;
    background: var(--bg);
  }

  .body {
    display: grid;
    grid-template-columns: 84px 1fr;
    min-height: 0;
  }

  main {
    min-width: 0;
    padding: 22px 26px;
    overflow-y: auto;
  }

  @media (max-height: 620px) {
    main {
      padding-block: 14px;
    }
  }

  .loading {
    place-self: center;
    color: var(--muted);
  }

  .notice {
    position: absolute;
    z-index: 40;
    left: 50%;
    bottom: 16px;
    display: flex;
    align-items: center;
    gap: var(--s2);
    width: max-content;
    max-width: min(560px, calc(100% - 48px));
    padding: 8px 8px 8px 12px;
    border: 1px solid color-mix(in srgb, var(--warn) 45%, var(--line));
    border-radius: var(--r-card);
    background: var(--surface);
    box-shadow: var(--shadow-pop);
    transform: translateX(-50%);
    animation: rise var(--slow) ease-out;
  }

  @keyframes rise {
    from {
      opacity: 0;
      transform: translate(-50%, 6px);
    }
  }

  .ico {
    display: flex;
    color: var(--warn);
  }

  .grow {
    flex: 1;
    min-width: 0;
  }

  .notice button {
    display: grid;
    place-items: center;
    width: 24px;
    height: 24px;
    padding: 0;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    color: var(--muted);
  }

  .notice button:hover {
    background: var(--surface-2);
    color: var(--text);
  }
</style>
