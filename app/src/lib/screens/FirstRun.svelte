<script lang="ts">
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Icon from '../components/Icon.svelte';
  import { ru } from '../i18n/ru';
  import type { LegacyFolder, LegacyImportResult } from '../proto';
  import { oldTrayAutostart, oldTrayAutostartOff } from '../shell/oldtray';
  import { daemon } from '../store.svelte';
  import { linkKind } from '../sublink';

  // A profile with nothing in it: one field and one button, and an offer to
  // bring an old Strelka's subscriptions over if one is on the machine.
  // Done means the VPN is asked to start; Home shows how that goes.
  let { ondone }: { ondone: () => void } = $props();

  const t = ru.firstRun;

  let link = $state('');
  let error = $state<string | null>(null);
  let step = $state<'idle' | 'adding' | 'starting'>('idle');

  // Every old Strelka the daemon found; the user picks the one to bring
  // over (two on one machine happen: a copy in Downloads, another in use).
  let found = $state<LegacyFolder[]>([]);
  let picked = $state<string | null>(null);
  // The old tray's own entry in the user's Run key: the daemon cannot see it.
  let trayAtLogin = $state<string | null>(null);
  let legacyStep = $state<'idle' | 'importing' | 'retiring'>('idle');
  let legacyError = $state<string | null>(null);
  let imported = $state<LegacyImportResult | null>(null);
  let retired = $state(false);

  $effect(() => {
    void daemon
      .call('legacy.detect')
      .then((r) => {
        found = r.found;
        picked = r.found[0]?.dir ?? null;
      })
      .catch(() => {
        // Nothing found is the same as nothing there: the field is enough.
      });
    void oldTrayAutostart().then((v) => (trayAtLogin = v));
  });

  // The old tray kills every sing-box and xray by name when it starts, the
  // new one's included. While it runs or starts at logon, the new VPN is not
  // started without first turning the old one off - with the user's word.
  const running = $derived(found.some((f) => f.running));
  const needsRetire = $derived(
    !retired && found.length > 0 && (running || found.some((f) => f.autostart) || trayAtLogin !== null),
  );
  const mayStart = $derived(retired || !running);

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
    // A link made for an app (happ://add/…) is a subscription too; the
    // daemon unwraps it or says why it cannot.
    const kind = linkKind(value);
    if (kind === null) {
      error = t.notLink;
      return;
    }
    step = 'adding';
    try {
      if (kind === 'sub') await daemon.call('subs.add', { url: value });
      else await daemon.call('subs.addLink', { link: value });
    } catch (err) {
      error = (err as Error).message;
      step = 'idle';
      return;
    }
    if (mayStart) await start();
    else ondone();
  }

  /** The daemon stops the old programs and its task, the window its tray entry. */
  async function retire(): Promise<boolean> {
    legacyStep = 'retiring';
    try {
      await daemon.call('legacy.retire');
    } catch (err) {
      legacyError = (err as Error).message;
      return false;
    } finally {
      legacyStep = 'idle';
    }
    try {
      await oldTrayAutostartOff();
      trayAtLogin = null;
    } catch (err) {
      // The daemon's part is done, and it is the part that matters today;
      // the entry would only bring the old tray back at the next logon.
      legacyError = t.legacyTrayFailed(String(err));
    }
    retired = true;
    found = found.map((f) => ({ ...f, running: false, autostart: false }));
    return true;
  }

  /**
   * Brings the picked Strelka over; with andConnect, also turns the old one
   * off and starts the new. Each part is done once: after a failure the same
   * button carries on from where it stopped.
   */
  async function bringOver(andConnect: boolean): Promise<void> {
    if (!picked) return;
    legacyError = null;
    let result = imported;
    if (!result) {
      legacyStep = 'importing';
      try {
        result = imported = await daemon.call('legacy.import', { dir: picked });
      } catch (err) {
        legacyError = (err as Error).message;
        return;
      } finally {
        legacyStep = 'idle';
      }
    }
    if (!andConnect) return;
    if (needsRetire && !(await retire())) return;
    if (result.servers > 0) await start();
  }

  const busy = $derived(step !== 'idle' || legacyStep !== 'idle');
</script>

{#snippet subsLine(f: LegacyFolder)}
  <span class="meta">
    {t.legacySubs(f.subs)}
    {#if f.running}<span class="tag run">{t.legacyRunningNow}</span>{/if}
    {#if f.autostart}<span class="tag">{t.legacyAtLogin}</span>{/if}
  </span>
{/snippet}

<div class="first">
  <div class="col">
    <span class="mark" aria-hidden="true"><Icon name="shield" size={28} /></span>
    <h1>{t.title}</h1>

    {#if found.length > 0 && !imported}
      <section class="legacy" aria-labelledby="legacy-title">
        <h2 id="legacy-title">{t.legacyTitle(found.length)}</h2>
        {#if found.length > 1}
          <fieldset class="pick">
            <legend>{t.legacyPick}</legend>
            {#each found as f (f.dir)}
              <label class="option">
                <input type="radio" name="legacy" value={f.dir} bind:group={picked} disabled={busy} />
                <span class="what">
                  <span class="dir">{f.dir}</span>
                  {@render subsLine(f)}
                </span>
              </label>
            {/each}
          </fieldset>
        {:else}
          {#each found as f (f.dir)}
            <p class="dir">{f.dir}</p>
            {@render subsLine(f)}
          {/each}
        {/if}
        <p>{t.legacyText}</p>
        {#if needsRetire}<p class="note">{t.legacyRetireNote(found.length > 1)}</p>{/if}
        {#if legacyError}<p class="error" role="alert">{legacyError}</p>{/if}
        <div class="acts">
          <Button icon="swap" busy={legacyStep !== 'idle'} disabled={step !== 'idle'} onclick={() => bringOver(true)}>
            {legacyStep === 'importing' ? t.legacyBusy : legacyStep === 'retiring' ? t.legacyRetiring : t.legacyImport}
          </Button>
          {#if needsRetire}
            <Button variant="ghost" disabled={busy} onclick={() => bringOver(false)}>{t.legacyImportOnly}</Button>
          {/if}
        </div>
      </section>
      <p class="or"><span>{t.or}</span></p>
    {:else if imported}
      <section class="legacy done" role="status">
        <p>{t.legacyDone(imported.subs, imported.servers, imported.rules)}</p>
        {#if imported.failed.length > 0}<p class="warn">{t.legacyFailed(imported.failed.join(', '))}</p>{/if}
        {#if legacyError}<p class="error" role="alert">{legacyError}</p>{/if}
        {#if needsRetire}
          <p class="note">{t.legacyThenConnect}</p>
          <div class="acts">
            <Button icon="power" {busy} onclick={() => bringOver(true)}>
              {legacyStep === 'retiring' ? t.legacyRetiring : step === 'starting' ? t.starting : t.legacyRetireAndConnect}
            </Button>
          </div>
        {:else if imported.servers > 0}
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
        <Button type="submit" icon="power" busy={step !== 'idle'} disabled={legacyStep !== 'idle'}>
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
    width: min(560px, 100%);
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

  .legacy.done p,
  .legacy .note {
    color: var(--text);
  }

  .dir {
    color: var(--text);
    font-family: var(--font-mono);
    font-size: var(--fs-cap);
    overflow-wrap: anywhere;
  }

  /* After .legacy.done p, with the same weight: a warning stays a warning. */
  .legacy p.warn {
    color: var(--warn);
  }

  .legacy p.error {
    color: var(--bad);
  }

  .meta {
    display: flex;
    flex-wrap: wrap;
    align-items: baseline;
    gap: 4px 8px;
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .tag {
    padding: 0 6px;
    border: 1px solid var(--line);
    border-radius: 999px;
    white-space: nowrap;
  }

  /* Running is a VPN of its own right now: the word says it, the colour
     only underlines it. */
  .tag.run {
    border-color: color-mix(in srgb, var(--warn) 50%, var(--line));
    color: var(--warn);
  }

  .pick {
    display: grid;
    gap: 4px;
    min-width: 0;
    margin: 2px 0 4px;
    padding: 0;
    border: 0;
  }

  .pick legend {
    margin-bottom: 4px;
    padding: 0;
    color: var(--muted);
  }

  .option {
    display: grid;
    grid-template-columns: auto 1fr;
    align-items: start;
    gap: 10px;
    padding: 8px 10px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    cursor: pointer;
  }

  .option:hover {
    background: var(--surface-2);
  }

  .option:has(input:checked) {
    border-color: var(--muted);
    background: var(--surface-2);
  }

  .option input {
    margin: 2px 0 0;
    accent-color: var(--text);
  }

  .what {
    display: grid;
    gap: 2px;
    min-width: 0;
  }

  .acts {
    display: flex;
    flex-wrap: wrap;
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
