<script lang="ts">
  import { untrack } from 'svelte';
  import { version as APP_VERSION } from '../../../package.json';
  import Button from '../components/Button.svelte';
  import Field from '../components/Field.svelte';
  import Segments from '../components/Segments.svelte';
  import Select, { type SelectItem } from '../components/Select.svelte';
  import SettingRow from '../components/SettingRow.svelte';
  import Switch from '../components/Switch.svelte';
  import { clock } from '../format';
  import { CITIES, cityOf } from '../globe/cities';
  import { ru } from '../i18n/ru';
  import type { EngineVersion, Settings, State } from '../proto';
  import { fileName, report } from '../report.svelte';
  import { daemon } from '../store.svelte';
  import { theme, type ThemeChoice } from '../theme.svelte';
  import { toasts } from '../toasts.svelte';
  import { inWindow } from '../transport';
  import { updates as appUpdates } from '../update.svelte';
  import { flagOfCode, guardLook } from '../view';
  import DiagDialog from './DiagDialog.svelte';

  let { state: s }: { state: State } = $props();

  const t = ru.settings;

  // A change shows at once; the daemon's State confirms it a moment later.
  // Until then the new value is held here, so a switch does not jump back
  // and forth. A refusal drops it and puts the reason under the row.
  let pending = $state<Partial<Settings>>({});
  let errors = $state<Partial<Record<keyof Settings, string>>>({});
  const shown = $derived<Settings>({ ...s.settings, ...pending });

  $effect(() => {
    const confirmed = Object.keys(pending).filter(
      (k) => s.settings[k as keyof Settings] === pending[k as keyof Settings],
    );
    if (confirmed.length === 0) return;
    const next = { ...pending };
    for (const k of confirmed) delete next[k as keyof Settings];
    pending = next;
  });

  async function set<K extends keyof Settings>(key: K, value: Settings[K]): Promise<void> {
    pending = { ...pending, [key]: value };
    errors = { ...errors, [key]: undefined };
    try {
      await daemon.call('settings.set', { patch: { [key]: value } });
      // Accepted but unchanged by the daemon's own cleaning: let go later.
      setTimeout(() => forget(key, value), 2000);
    } catch (err) {
      forget(key, value);
      errors = { ...errors, [key]: (err as Error).message };
    }
  }

  function forget<K extends keyof Settings>(key: K, value: Settings[K]): void {
    if (pending[key] !== value) return;
    const next = { ...pending };
    delete next[key];
    pending = next;
  }

  // ---- the local proxy: a switch and a port ----

  const DEFAULT_PORT = 10808;
  let lastPort = $state(DEFAULT_PORT);
  let portText = $state('');
  let portError = $state<string | null>(null);
  // A number, so that the field is refilled only when the port itself
  // changes, not with every State that brings new traffic figures.
  const port = $derived(shown.localProxyPort);
  $effect(() => {
    if (port > 0) lastPort = port;
  });
  $effect(() => {
    portText = String(port > 0 ? port : untrack(() => lastPort));
  });

  function applyPort(): void {
    const port = Number(portText.trim());
    if (!Number.isInteger(port) || port < 1024 || port > 65535) {
      portError = t.portBad;
      return;
    }
    portError = null;
    if (port !== shown.localProxyPort) void set('localProxyPort', port);
  }

  // ---- the window's own settings ----

  const themes: readonly { value: ThemeChoice; label: string }[] = [
    { value: 'system', label: t.themeSystem },
    { value: 'dark', label: t.themeDark },
    { value: 'light', label: t.themeLight },
  ];

  let trayAtLogin = $state<boolean | null>(null);
  let trayError = $state<string | null>(null);
  $effect(() => {
    if (!inWindow) return;
    void import('@tauri-apps/plugin-autostart').then(async ({ isEnabled }) => {
      trayAtLogin = await isEnabled();
    });
  });

  async function setTrayAtLogin(on: boolean): Promise<void> {
    trayError = null;
    const before = trayAtLogin;
    trayAtLogin = on;
    try {
      const { enable, disable } = await import('@tauri-apps/plugin-autostart');
      await (on ? enable() : disable());
    } catch (err) {
      trayAtLogin = before;
      trayError = String(err);
    }
  }

  // ---- about ----

  // The window looks for updates by itself; the button looks now, and what
  // it found stays under it.
  const updateNote = $derived.by(() => {
    if (!appUpdates.attached) return t.updatesOff;
    if (appUpdates.available) return t.updatesFound(appUpdates.available.version);
    if (appUpdates.lastCheck && !appUpdates.lastCheck.error) return t.updatesLatest(clock(new Date(appUpdates.lastCheck.at).toISOString()));
    return t.updatesNote;
  });
  const updateError = $derived(appUpdates.available ? null : (appUpdates.lastCheck?.error ?? null));

  let engines = $state<EngineVersion[] | null>(null);
  $effect(() => {
    void daemon
      .call('engine.versions')
      .then((r) => (engines = r.engines))
      .catch(() => (engines = []));
  });

  const notify = [
    { value: 'all', label: t.notifyAll },
    { value: 'important', label: t.notifyImportant },
    { value: 'silent', label: t.notifySilent },
  ] as const;

  const ipv6 = [
    { value: 'tunnel', label: t.ipv6Tunnel },
    { value: 'block', label: t.ipv6Block },
  ] as const;

  const updates = [
    { value: 'off', label: t.autoUpdateOff },
    { value: '6h', label: t.autoUpdate6 },
    { value: '12h', label: t.autoUpdate12 },
    { value: '24h', label: t.autoUpdate24 },
  ] as const;

  // ---- the near end of the globe's beam ----

  const cityItems: SelectItem[] = CITIES.map((c) => ({ value: c.id, label: c.name, flag: flagOfCode(c.country) }));
  const chosenCity = $derived(s.place?.source === 'manual' ? (cityOf(s.place)?.id ?? null) : null);
  const placeNote = $derived(
    !s.place ? t.placeNone : s.place.source === 'manual' ? t.placeManual(s.place.city) : t.placeIp(s.place.city),
  );
  let placeBusy = $state(false);
  let placeError = $state<string | null>(null);

  async function pickCity(id: string): Promise<void> {
    const c = CITIES.find((x) => x.id === id);
    if (!c) return;
    placeError = null;
    try {
      await daemon.call('place.set', { lat: c.lat, lon: c.lon, city: c.name, country: c.country });
    } catch (err) {
      placeError = (err as Error).message;
    }
  }

  async function detectPlace(): Promise<void> {
    placeBusy = true;
    placeError = null;
    try {
      await daemon.call('place.detect');
    } catch (err) {
      placeError = (err as Error).message;
    } finally {
      placeBusy = false;
    }
  }

  // ---- toasts: Strelka's own or the system's ----

  const systemNote = $derived(
    toasts.ownWindow === null ? t.systemToastsBrowser : toasts.ownWindow ? t.systemToastsNote : t.systemToastsForced,
  );

  let checking = $state(false);

  const guard = $derived(guardLook(s));
  const restartable = $derived(s.vpn.phase === 'on' || s.vpn.phase === 'recovering' || s.vpn.phase === 'failed');
</script>

<div class="settings">
  <h1>{ru.nav.settings}</h1>

  <section>
    <h2>{t.start}</h2>
    <div class="card">
      <SettingRow label={t.startOnBoot} note={t.startOnBootNote} error={errors.startOnBoot}>
        {#snippet children()}
          <Switch size="sm" label={t.startOnBoot} checked={shown.startOnBoot} onchange={(on) => set('startOnBoot', on)} />
        {/snippet}
      </SettingRow>
      <SettingRow
        label={t.trayAtLogin}
        note={inWindow ? t.trayAtLoginNote : t.trayAtLoginBrowser}
        error={trayError}
      >
        {#snippet children()}
          <Switch
            size="sm"
            label={t.trayAtLogin}
            checked={trayAtLogin ?? false}
            disabled={!inWindow || trayAtLogin === null}
            onchange={(on) => setTrayAtLogin(on)}
          />
        {/snippet}
      </SettingRow>
    </div>
  </section>

  <section>
    <h2>{t.notify}</h2>
    <div class="card">
      <SettingRow label={t.notifyLevel} note={t.notifyNote[shown.notify]} error={errors.notify}>
        {#snippet children()}
          <div class="seg3">
            <Segments options={notify} value={shown.notify} label={t.notifyLevel} onchange={(v) => set('notify', v)} />
          </div>
        {/snippet}
      </SettingRow>
      <SettingRow label={t.subWarnings} note={t.subWarningsNote} error={errors.subWarnings}>
        {#snippet children()}
          <Switch size="sm" label={t.subWarnings} checked={shown.subWarnings} onchange={(on) => set('subWarnings', on)} />
        {/snippet}
      </SettingRow>
      <SettingRow
        label={t.serverWarnings}
        note={shown.subWarnings && shown.serverWarnings ? t.serverWarningsNote : `${t.serverWarningsNote} ${t.warningsOff}`}
        error={errors.serverWarnings}
      >
        {#snippet children()}
          <Switch size="sm" label={t.serverWarnings} checked={shown.serverWarnings} onchange={(on) => set('serverWarnings', on)} />
        {/snippet}
      </SettingRow>
      <SettingRow label={t.quietFullscreen} note={t.quietFullscreenNote} error={errors.quietFullscreen}>
        {#snippet children()}
          <Switch
            size="sm"
            label={t.quietFullscreen}
            checked={shown.quietFullscreen}
            onchange={(on) => set('quietFullscreen', on)}
          />
        {/snippet}
      </SettingRow>
      <SettingRow label={t.systemToasts} note={systemNote}>
        {#snippet children()}
          <Switch
            size="sm"
            label={t.systemToasts}
            checked={toasts.ownWindow === false || toasts.systemPref}
            disabled={!toasts.ownWindow}
            onchange={(on) => toasts.setSystemPref(on)}
          />
        {/snippet}
      </SettingRow>
    </div>
  </section>

  <section>
    <h2>{t.protect}</h2>
    <div class="card">
      <SettingRow
        label={t.guard}
        note={t.guardNote}
        warn={guard === 'not_ready' ? ru.quick.guardNotReadyHint : null}
        error={errors.guard}
      >
        {#snippet children()}
          <Switch size="sm" label={t.guard} checked={shown.guard} onchange={(on) => set('guard', on)} />
        {/snippet}
      </SettingRow>
      <SettingRow label={t.allowLan} note={t.allowLanNote} error={errors.allowLan}>
        {#snippet children()}
          <Switch size="sm" label={t.allowLan} checked={shown.allowLan} onchange={(on) => set('allowLan', on)} />
        {/snippet}
      </SettingRow>
      <SettingRow label={t.ipv6} note={t.ipv6Note} error={errors.ipv6}>
        {#snippet children()}
          <div class="seg2">
            <Segments options={ipv6} value={shown.ipv6} label={t.ipv6} onchange={(v) => set('ipv6', v)} />
          </div>
        {/snippet}
      </SettingRow>
      <SettingRow label={t.failover} note={t.failoverNote} error={errors.failover}>
        {#snippet children()}
          <Switch size="sm" label={t.failover} checked={shown.failover} onchange={(on) => set('failover', on)} />
        {/snippet}
      </SettingRow>
      <SettingRow label={t.rating} note={t.ratingNote} error={errors.rating}>
        {#snippet children()}
          <Switch size="sm" label={t.rating} checked={shown.rating} onchange={(on) => set('rating', on)} />
        {/snippet}
      </SettingRow>
    </div>
  </section>

  <section>
    <h2>{t.network}</h2>
    <div class="card">
      <SettingRow
        label={t.proxy}
        note={shown.localProxyPort > 0 ? t.proxyNote(shown.localProxyPort) : t.proxyOffNote}
        error={portError ?? errors.localProxyPort}
      >
        {#snippet children()}
          {#if shown.localProxyPort > 0}
            <Field
              label={t.port}
              hideLabel
              bind:value={portText}
              invalid={portError !== null}
              inputmode="numeric"
              mono
              width="92px"
              onenter={applyPort}
              onblur={applyPort}
            />
          {/if}
          <Switch
            size="sm"
            label={t.proxy}
            checked={shown.localProxyPort > 0}
            onchange={(on) => {
              portError = null;
              void set('localProxyPort', on ? lastPort : 0);
            }}
          />
        {/snippet}
      </SettingRow>
    </div>
  </section>

  <section>
    <h2>{t.subs}</h2>
    <div class="card">
      <SettingRow label={t.autoUpdate} note={t.autoUpdateNote} error={errors.autoUpdateSubs}>
        {#snippet children()}
          <div class="seg4">
            <Segments options={updates} value={shown.autoUpdateSubs} label={t.autoUpdate} onchange={(v) => set('autoUpdateSubs', v)} />
          </div>
        {/snippet}
      </SettingRow>
    </div>
  </section>

  <section>
    <h2>{t.look}</h2>
    <div class="card">
      <SettingRow label={t.theme}>
        {#snippet children()}
          <div class="seg3">
            <Segments options={themes} value={theme.choice} label={t.theme} onchange={(choice) => theme.set(choice)} />
          </div>
        {/snippet}
      </SettingRow>
      <SettingRow label={t.place} note={placeNote} error={placeError}>
        {#snippet children()}
          <div class="place">
            <Select items={cityItems} value={chosenCity} label={t.place} placeholder={t.placePick} onchange={pickCity} />
          </div>
          <Button icon="globe" busy={placeBusy} title={t.placeDetectHint} onclick={detectPlace}>{t.placeDetect}</Button>
        {/snippet}
      </SettingRow>
    </div>
  </section>

  <section>
    <h2>{t.about}</h2>
    <div class="card">
      <div class="versions">
        <dl>
          <dt>{t.appVersion}</dt>
          <dd>{APP_VERSION}</dd>
          <dt>{t.daemonVersion}</dt>
          <dd>{s.version}</dd>
          {#each engines ?? [{ name: 'sing-box', version: null }, { name: 'xray', version: null }] as e (e.name)}
            <dt>{e.name === 'xray' ? 'Xray' : 'sing-box'}</dt>
            <dd class:muted={!e.version}>{engines === null ? '…' : (e.version ?? t.engineUnknown)}</dd>
          {/each}
        </dl>
      </div>
      <SettingRow label={t.updates} note={updateNote} error={updateError}>
        {#snippet children()}
          {#if appUpdates.available}
            <Button icon="download" onclick={() => appUpdates.show()}>{ru.update.outdated(appUpdates.available.version)}</Button>
          {:else}
            <Button icon="refresh" busy={appUpdates.checking} disabled={!appUpdates.attached} onclick={() => appUpdates.check(true)}>
              {appUpdates.checking ? t.updatesChecking : t.updatesCheck}
            </Button>
          {/if}
        {/snippet}
      </SettingRow>
      <SettingRow label={t.check} note={t.checkNote}>
        {#snippet children()}
          <Button icon="pulse" onclick={() => (checking = true)}>{t.checkButton}</Button>
        {/snippet}
      </SettingRow>
      <SettingRow label={t.restart} note={restartable ? t.restartNote : t.restartOff}>
        {#snippet children()}
          <Button icon="refresh" disabled={!restartable} onclick={() => daemon.rpc('vpn.restart')}>{t.restartButton}</Button>
        {/snippet}
      </SettingRow>
      <SettingRow label={t.report} note={report.path ? report.path : t.reportNote}>
        {#snippet children()}
          {#if report.path && inWindow}
            <Button variant="ghost" icon="folder" onclick={() => report.reveal()}>{ru.journal.reportShow}</Button>
          {/if}
          <Button icon="report" busy={report.busy} title={report.path ? fileName(report.path) : undefined} onclick={() => report.make()}>
            {report.busy ? ru.journal.reporting : ru.journal.report}
          </Button>
        {/snippet}
      </SettingRow>
      <p class="credits">{t.credits}</p>
    </div>
  </section>
</div>

{#if checking}
  <DiagDialog onclose={() => (checking = false)} />
{/if}

<style>
  .settings {
    display: grid;
    gap: var(--s5);
    max-width: 760px;
    padding-bottom: var(--s4);
  }

  h1 {
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  h2 {
    margin-bottom: var(--s2);
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .card {
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
  }

  .seg2 {
    width: 220px;
  }

  .seg3 {
    width: 330px;
  }

  .seg4 {
    width: 360px;
  }

  .place {
    width: 220px;
  }

  @media (max-width: 900px) {
    .seg3 {
      width: 290px;
    }

    .seg4 {
      width: 310px;
    }
  }

  .versions {
    padding: 11px 14px;
    border-bottom: 1px solid var(--line);
  }

  dl {
    display: grid;
    grid-template-columns: max-content 1fr;
    gap: 3px var(--s4);
    margin: 0;
  }

  dt {
    color: var(--muted);
  }

  dd {
    margin: 0;
  }

  dd.muted {
    color: var(--muted);
  }

  .credits {
    padding: 9px 14px 11px;
    border-top: 1px solid var(--line);
    color: var(--muted);
    font-size: var(--fs-cap);
  }
</style>
