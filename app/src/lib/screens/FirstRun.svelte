<script lang="ts">
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import { ru } from '../i18n/ru';
  import type { LegacyImportResult } from '../proto';
  import { daemon } from '../store.svelte';

  // A profile with nothing in it: one field and one button, and an offer to
  // bring the old Strelka's subscriptions over if one is on the machine.
  // Done means the VPN is asked to start; Home shows how that goes.
  let { ondone }: { ondone: () => void } = $props();

  const t = ru.firstRun;

  let link = $state('');
  let error = $state<string | null>(null);
  let step = $state<'idle' | 'adding' | 'starting'>('idle');

  let legacy = $state<{ dir: string; running: boolean } | null>(null);
  let legacyBusy = $state(false);
  let legacyError = $state<string | null>(null);
  let imported = $state<LegacyImportResult | null>(null);

  $effect(() => {
    void daemon
      .call('legacy.detect')
      .then((r) => {
        if (r.dir) legacy = { dir: r.dir, running: r.running };
      })
      .catch(() => {
        // Nothing found is the same as nothing there: the field is enough.
      });
  });

  // Two VPNs at once fight over the routes. While the old one runs, the new
  // one is set up but not started.
  const mayStart = $derived(!legacy?.running);

  async function start(): Promise<void> {
    step = 'starting';
    try {
      await daemon.call('vpn.start');
      ondone();
    } catch (err) {
      error = (err as Error).message;
      step = 'idle';
    }
  }

  async function connect(): Promise<void> {
    const value = link.trim();
    error = null;
    if (value === '') {
      error = t.empty;
      return;
    }
    const isSub = /^https?:\/\/\S+$/i.test(value);
    const isServer = /^(vless|trojan|ss|hysteria2|hy2):\/\/\S+$/i.test(value);
    if (!isSub && !isServer) {
      error = t.notLink;
      return;
    }
    step = 'adding';
    try {
      if (isSub) await daemon.call('subs.add', { url: value });
      else await daemon.call('subs.addLink', { link: value });
    } catch (err) {
      error = (err as Error).message;
      step = 'idle';
      return;
    }
    if (mayStart) await start();
    else ondone();
  }

  async function bringOver(): Promise<void> {
    if (!legacy) return;
    legacyBusy = true;
    legacyError = null;
    try {
      imported = await daemon.call('legacy.import', { dir: legacy.dir });
    } catch (err) {
      legacyError = (err as Error).message;
      return;
    } finally {
      legacyBusy = false;
    }
    if (mayStart && imported.servers > 0) await start();
  }

  const busy = $derived(step !== 'idle' || legacyBusy);
</script>

<div class="first">
  <div class="col">
    <span class="mark" aria-hidden="true"><Icon name="shield" size={28} /></span>
    <h1>{t.title}</h1>

    {#if legacy && !imported}
      <section class="legacy" aria-labelledby="legacy-title">
        <h2 id="legacy-title">{t.legacyTitle}</h2>
        <p class="dir">{legacy.dir}</p>
        <p>{t.legacyText}</p>
        {#if legacy.running}<p class="warn">{t.legacyRunning}</p>{/if}
        {#if legacyError}<p class="error" role="alert">{legacyError}</p>{/if}
        <div class="acts">
          <Button icon="swap" busy={legacyBusy} disabled={step !== 'idle'} onclick={bringOver}>
            {legacyBusy ? t.legacyBusy : legacy.running ? t.legacyImportOnly : t.legacyImport}
          </Button>
        </div>
      </section>
      <p class="or"><span>{t.or}</span></p>
    {:else if imported}
      <section class="legacy done" role="status">
        <p>{t.legacyDone(imported.subs, imported.servers, imported.rules)}</p>
        {#if imported.failed.length > 0}<p class="warn">{t.legacyFailed(imported.failed.join(', '))}</p>{/if}
        {#if !mayStart}
          <p>{t.legacyThenConnect}</p>
          <div class="acts">
            <Button icon="power" busy={step === 'starting'} onclick={start}>{t.connectImported}</Button>
          </div>
        {/if}
      </section>
    {/if}

    {#if !imported}
      <p class="lead">{t.lead}</p>
      <form
        onsubmit={(event) => {
          event.preventDefault();
          void connect();
        }}
      >
        <Field
          label={t.field}
          hideLabel
          bind:value={link}
          placeholder={t.placeholder}
          inputmode="url"
          mono
          icon="link"
          {error}
          disabled={busy}
        />
        <Button type="submit" icon="power" busy={step !== 'idle'} disabled={legacyBusy}>
          {step === 'adding' ? t.adding : step === 'starting' ? t.starting : t.connect}
        </Button>
      </form>
    {/if}

    <button type="button" class="skip" disabled={busy} onclick={ondone}>{t.skip}</button>
  </div>
</div>

<style>
  .first {
    display: grid;
    place-items: center;
    min-height: 0;
    padding: 24px;
    overflow-y: auto;
  }

  .col {
    display: grid;
    justify-items: start;
    gap: var(--s3);
    width: min(520px, 100%);
  }

  .mark {
    display: grid;
    place-items: center;
    width: 48px;
    height: 48px;
    border: 1px solid var(--line);
    border-radius: 14px;
    background: var(--surface);
    color: var(--text);
  }

  h1 {
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  .lead {
    color: var(--muted);
  }

  form {
    display: grid;
    grid-template-columns: 1fr auto;
    align-items: start;
    gap: var(--s2);
    width: 100%;
  }

  .legacy {
    display: grid;
    gap: 6px;
    width: 100%;
    padding: 14px 16px;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
  }

  .legacy h2 {
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .legacy p {
    color: var(--muted);
  }

  .legacy.done p {
    color: var(--text);
  }

  .legacy .dir {
    color: var(--text);
    font-family: var(--font-mono);
    font-size: var(--fs-cap);
    overflow-wrap: anywhere;
  }

  .legacy .warn {
    color: var(--warn);
  }

  .legacy .error {
    color: var(--bad);
  }

  .acts {
    display: flex;
    gap: var(--s2);
    margin-top: 6px;
  }

  .or {
    display: grid;
    grid-template-columns: 1fr auto 1fr;
    align-items: center;
    gap: var(--s3);
    width: 100%;
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .or::before,
  .or::after {
    content: '';
    height: 1px;
    background: var(--line);
  }

  .or span {
    grid-column: 2;
  }

  .skip {
    margin-top: var(--s2);
    padding: 2px 0;
    border: 0;
    background: none;
    color: var(--muted);
    font: inherit;
    text-decoration: underline;
    text-underline-offset: 3px;
    cursor: pointer;
  }

  .skip:hover:not(:disabled) {
    color: var(--text);
  }
</style>
