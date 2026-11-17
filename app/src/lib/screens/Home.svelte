<script lang="ts">
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import Fork from '../components/Fork.svelte';
  import Globe from '../components/Globe.svelte';
  import Hint from '../components/Hint.svelte';
  import Icon from '../components/Icon.svelte';
  import ListRow from '../components/ListRow.svelte';
  import Segments from '../components/Segments.svelte';
  import Select, { type SelectItem } from '../components/Select.svelte';
  import Switch from '../components/Switch.svelte';
  import { clock, speed } from '../format';
  import { countryPlace, homePlace, type Place } from '../globe/places';
  import { ru } from '../i18n/ru';
  import type { Mode, Server, State } from '../proto';
  import { win } from '../shell/window.svelte';
  import { daemon, type PathRate } from '../store.svelte';
  import {
    banner,
    countryOf,
    describe,
    displayName,
    flagOf,
    guardLook,
    pingTone,
    selectedServer,
    sortedServers,
    type BannerAction,
    type RailLook,
  } from '../view';

  let { state: s, onjournal }: { state: State; onjournal: () => void } = $props();

  const t = ru;
  const picture = $derived(describe(s, daemon.now));
  const strip = $derived(banner(s));
  const server = $derived(selectedServer(s));
  const guard = $derived(guardLook(s));
  const recent = $derived(daemon.journal.slice(0, 3));

  // The two ends of the beam on the globe. The near one is what the daemon
  // found by IP or the user chose; until then, the city of the system's
  // time zone.
  const guessed = homePlace(Intl.DateTimeFormat().resolvedOptions().timeZone, new Date().getTimezoneOffset());
  // Through two numbers, so that a new State with the same place does not
  // hand the globe a new array.
  const hereLat = $derived(s.place?.lat ?? guessed[0]);
  const hereLon = $derived(s.place?.lon ?? guessed[1]);
  const here = $derived<Place>([hereLat, hereLon]);
  const there = $derived(server ? countryPlace(countryOf(server)) : null);

  // Speeds by path come from the list of connections, which is only worth
  // asking for while the window is actually looked at. The globe turns
  // only then, too.
  let visible = $state(document.visibilityState === 'visible');
  $effect(() => {
    const update = () => (visible = document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', update);
    return () => document.removeEventListener('visibilitychange', update);
  });
  const watched = $derived(visible && win.shown);
  $effect(() => {
    if (watched) return daemon.watchConnections();
  });

  // Until the first two lists arrive only the counts from State are known.
  const vpnPath = $derived<PathRate | null>(daemon.rates?.vpn ?? null);
  const directPath = $derived<PathRate | null>(daemon.rates?.direct ?? null);

  function figures(conns: number, rate: PathRate | null): string {
    if (conns === 0 && (rate === null || rate.down + rate.up < 512)) return t.tracks.idle;
    const parts = [t.tracks.conns(conns)];
    if (rate) parts.push(t.tracks.down(speed(rate.down)), t.tracks.up(speed(rate.up)));
    // The column by the globe is narrow: the line may break between the
    // figures, never inside one ("↓" on one line, "4,7 МБ/с" on the next).
    return parts.map((part) => part.replaceAll(' ', ' ')).join(' · ');
  }

  const noLink = $derived(s.health.verdict === 'no_internet' || s.health.verdict === 'no_network');

  const vpnStat = $derived.by(() => {
    switch (picture.vpnRail) {
      case 'closed':
        return t.tracks.blockedVpn;
      case 'idle':
        // No network at all is not 'connecting': nothing is on its way.
        return noLink ? t.tracks.noInternet : t.tracks.vpnOff;
      case 'waiting':
        return t.tracks.vpnStarting;
      case 'down':
        return t.tracks.vpnWaiting;
      case 'flow':
        return figures(vpnPath?.conns ?? s.conns.vpn, vpnPath);
    }
  });

  const directStat = $derived.by(() => {
    switch (picture.directRail as RailLook) {
      case 'closed':
        return s.guard.allowLan ? t.tracks.blockedDirectLan : t.tracks.blockedDirect;
      case 'waiting':
        return t.tracks.noInternet;
      default: {
        // Until the engine is up everything goes directly, and nothing
        // counts that traffic: there are no figures to show.
        if (s.vpn.phase !== 'on') return t.tracks.directOff;
        const conns = directPath?.conns ?? Math.max(0, s.conns.total - s.conns.vpn);
        if (conns === 0 && s.vpn.mode === 'global') return t.tracks.directGlobal;
        return figures(conns, directPath);
      }
    }
  });

  function pingText(x: Server): { meta: string; metaTone: SelectItem['metaTone'] } {
    const chosen = x.id === s.server.selected;
    // The selected server is probed all the time; its number in "server" is
    // fresher than the one from the last test of all servers.
    const ping = chosen && s.vpn.phase === 'on' ? s.server.ping : x.ping;
    if (chosen && (s.health.verdict === 'server_down' || s.health.verdict === 'all_servers_down')) {
      return { meta: t.quick.noAnswer, metaTone: 'warn' };
    }
    if (ping === null) return { meta: t.quick.noPing, metaTone: 'none' };
    // Colour says "works through the VPN right now": with the VPN off a ping
    // is only a number from the past.
    return { meta: t.units.ms(ping), metaTone: s.vpn.phase === 'on' ? pingTone(ping) : 'none' };
  }

  const serverItems = $derived<SelectItem[]>(
    sortedServers(s).map((x) => ({ value: x.id, label: displayName(x), flag: flagOf(x), ...pingText(x) })),
  );

  const modes: readonly { value: Mode; label: string }[] = [
    { value: 'rule', label: t.quick.modeRule },
    { value: 'global', label: t.quick.modeGlobal },
  ];

  const guardText = $derived(
    {
      on: t.quick.guardOn,
      off: t.quick.guardOff,
      idle: t.quick.guardIdle,
      blocking: t.quick.guardBlocking,
      not_ready: t.quick.guardNotReady,
    }[guard],
  );

  function run(action: BannerAction): void {
    if (action.action === 'journal') onjournal();
    else void daemon.act(strip?.id ?? null, action.action);
  }
</script>

<div class="home" class:strip={strip !== null}>
  <header class="status">
    <span class="dot {picture.tone}" aria-hidden="true"></span>
    <div class="grow">
      <h1>{picture.word}</h1>
      <p>{picture.sub}</p>
    </div>
  </header>

  {#if strip}
    <Banner tone={strip.tone} title={strip.title} body={strip.body}>
      {#snippet actions()}
        {#each strip.actions as action, i (action.action)}
          <Button variant={i === 0 ? 'default' : 'ghost'} onclick={() => run(action)}>{action.label}</Button>
        {/each}
      {/snippet}
    </Banner>
  {/if}

  <section class="hero">
    <div class="side">
      <Fork vpn={picture.vpnRail} direct={picture.directRail} gate={picture.gate} tone={picture.tone} {vpnStat} {directStat} />
    </div>

    <div class="stage">
      <div class="disc">
        <Globe
          tone={picture.tone}
          beam={picture.vpnRail}
          from={here}
          to={there}
          rate={vpnPath ? vpnPath.down + vpnPath.up : null}
          active={watched}
        />
        <div class="power">
          <Hint text={picture.wantOn ? t.state.powerOff : t.state.powerOn}>
            {#snippet children(describedby)}
              <button
                type="button"
                role="switch"
                class="knob {picture.tone}"
                aria-checked={picture.wantOn}
                aria-label={t.state.switchLabel}
                aria-describedby={describedby}
                onclick={() => daemon.toggleVpn()}
              >
                <Icon name="power" size={26} />
              </button>
            {/snippet}
          </Hint>
        </div>
      </div>
    </div>

    <div class="side quick">
      <div class="group">
        <h3>{t.quick.server}</h3>
        <Select
          items={serverItems}
          value={s.server.selected}
          label={t.quick.server}
          placeholder={s.servers.length === 0 ? t.quick.serverEmpty : t.quick.serverPick}
          disabled={s.servers.length === 0}
          onchange={(id) => daemon.selectServer(id)}
        />
      </div>

      <div class="group">
        <h3>{t.quick.mode}</h3>
        <Segments options={modes} value={s.vpn.mode} label={t.quick.mode} onchange={(mode) => daemon.setMode(mode)} />
      </div>

      <div class="group">
        <h3>{t.quick.guard}</h3>
        <div class="lock {guard}">
          <Icon name={s.guard.enabled ? 'lock' : 'unlock'} size={16} />
          <span class="grow">{guardText}</span>
          <Hint text={guard === 'not_ready' ? t.quick.guardNotReadyHint : t.quick.guardHint}>
            {#snippet children(describedby)}
              <span aria-describedby={describedby}>
                <Switch
                  size="sm"
                  checked={s.guard.enabled}
                  label={t.quick.guardSwitch}
                  onchange={(on) => daemon.setGuard(on)}
                />
              </span>
            {/snippet}
          </Hint>
        </div>
      </div>
    </div>
  </section>

  <section class="log">
    <h2>
      {t.log.title}
      <button type="button" class="link" onclick={onjournal}>{t.log.all}</button>
    </h2>
    {#if recent.length === 0}
      <p class="empty">{t.log.empty}</p>
    {:else}
      <ul>
        {#each recent as entry (entry.id)}
          <ListRow lead={clock(entry.t)} tone={entry.level === 'critical' ? 'bad' : entry.level === 'problem' ? 'warn' : 'none'}>
            <span title={entry.text}>{entry.text}</span>
          </ListRow>
        {/each}
      </ul>
    {/if}
  </section>
</div>

<style>
  .home {
    display: flex;
    flex-direction: column;
    gap: var(--s4);
    min-height: 100%;
  }

  .status {
    display: flex;
    align-items: center;
    gap: var(--s4);
  }

  .grow {
    flex: 1;
    min-width: 0;
  }

  h1 {
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  .status p {
    margin-top: 3px;
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .dot {
    flex: none;
    width: 10px;
    height: 10px;
    margin-top: 2px;
    border-radius: 50%;
    background: var(--muted);
    transition: background var(--slow);
  }

  .dot.ok {
    background: var(--vpn);
  }

  .dot.busy,
  .dot.warn {
    background: var(--warn);
  }

  .dot.bad {
    background: var(--bad);
  }

  .dot.busy {
    animation: pulse 1.2s ease-in-out infinite;
  }

  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }

  /* The globe takes what the window has left: its side follows the height
     of the window, and gives way when the window is narrow. */
  .hero {
    --globe: clamp(200px, min(100vh - 300px, 100vw - 580px), 380px);
    flex: 1;
    display: grid;
    grid-template-columns: minmax(150px, 1fr) var(--globe) minmax(220px, 1fr);
    align-items: center;
    gap: var(--s4);
    min-height: 224px;
    padding: var(--s3) var(--s4);
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
  }

  .strip .hero {
    --globe: clamp(200px, min(100vh - 362px, 100vw - 580px), 380px);
  }

  .side {
    min-width: 0;
  }

  .stage {
    position: relative;
    align-self: stretch;
    container-type: size;
  }

  /* A square in the middle of the stage, whatever shape the stage is. */
  .disc {
    position: absolute;
    top: 50%;
    left: 50%;
    width: min(100cqw, 100cqh);
    height: min(100cqw, 100cqh);
    transform: translate(-50%, -50%);
  }

  /* The button sits on the lower edge of the globe: the canvas draws the
     ball at 80% of the square, so its edge is at 90% of the height. */
  .power {
    position: absolute;
    top: 90%;
    left: 50%;
    transform: translate(-50%, -50%);
  }

  .knob {
    --ring: var(--muted);
    display: grid;
    place-items: center;
    width: 60px;
    height: 60px;
    padding: 0;
    border: 1.5px solid color-mix(in srgb, var(--ring) 70%, var(--line));
    border-radius: 50%;
    background: var(--surface);
    color: var(--muted);
    box-shadow:
      0 0 0 5px color-mix(in srgb, var(--ring) 14%, transparent),
      0 6px 20px rgb(0 0 0 / 0.22);
    transition:
      border-color var(--slow),
      box-shadow var(--slow),
      color var(--slow),
      background var(--fast);
  }

  .knob:hover {
    background: var(--surface-2);
  }

  /* The ring is the colour of the state, as the dot by the heading is; the
     button itself stays neutral and says only "asked to be on". */
  .knob[aria-checked='true'] {
    color: var(--text);
  }

  .knob.ok {
    --ring: var(--vpn);
  }

  .knob.busy,
  .knob.warn {
    --ring: var(--warn);
  }

  .knob.bad {
    --ring: var(--bad);
  }

  .knob.busy {
    animation: pulse 1.2s ease-in-out infinite;
  }

  .quick {
    display: flex;
    flex-direction: column;
    gap: var(--s3);
  }

  .group h3 {
    margin-bottom: 6px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }

  /* The smallest window (820x560) with an incident strip: everything still
     fits without scrolling if the journal gives up its third line. */
  @media (max-height: 620px) {
    .home {
      gap: var(--s3);
    }

    .strip .log :global(li:nth-child(n + 3)) {
      display: none;
    }
  }

  .lock {
    display: flex;
    align-items: center;
    gap: var(--s2);
    min-height: 31px;
    line-height: 1.25;
  }

  .lock.off,
  .lock.idle {
    color: var(--muted);
  }

  .lock.blocking :global(svg) {
    color: var(--bad);
  }

  .lock.not_ready :global(svg) {
    color: var(--warn);
  }

  .log {
    margin-top: auto;
  }

  h2 {
    display: flex;
    align-items: baseline;
    margin-bottom: 4px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }

  .link {
    margin-left: auto;
    padding: 0;
    border: 0;
    border-radius: 3px;
    background: none;
    color: var(--muted);
    text-decoration: underline;
    text-underline-offset: 2px;
  }

  .link:hover {
    color: var(--text);
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .empty {
    padding: 5px 0;
    border-top: 1px solid var(--line);
    color: var(--muted);
  }
</style>
