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
  import Starfield from '../components/Starfield.svelte';
  import Switch from '../components/Switch.svelte';
  import { clock, speed } from '../format';
  import { countryPlace, homePlace, type Place } from '../globe/places';
  import { ru } from '../i18n/ru';
  import type { Mode, Server, State } from '../proto';
  import { win } from '../shell/window.svelte';
  import { daemon, type PathRate } from '../store.svelte';
  import ModeDialog from './ModeDialog.svelte';
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

  let { state: s, onjournal, onrules }: { state: State; onjournal: () => void; onrules: () => void } = $props();

  const t = ru;
  const picture = $derived(describe(s, daemon.now));
  const strip = $derived(banner(s));
  const server = $derived(selectedServer(s));
  const guard = $derived(guardLook(s));
  const recent = $derived(daemon.journal.slice(0, 3));
  let aboutMode = $state(false);

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

  function hide(id: string | null): void {
    if (id) void daemon.act(id, 'hide');
  }

  function run(action: BannerAction): void {
    if (action.action === 'journal') onjournal();
    else void daemon.act(strip?.id ?? null, action.action);
  }
</script>

<div class="home" class:strip={strip !== null}>
  <!-- The sky fills the whole pane, edge to edge; only the journal below
       sits apart, on the ground of the window. -->
  <div class="sky">
    <Starfield active={watched} />

    {#if strip}
      <Banner
        tone={strip.tone}
        title={strip.title}
        body={strip.body}
        closeLabel={t.banner.hide}
        onclose={strip.closable && strip.id ? () => hide(strip.id) : undefined}
      >
        {#snippet actions()}
          {#each strip.actions as action, i (action.action)}
            <Button variant={i === 0 ? 'default' : 'ghost'} onclick={() => run(action)}>{action.label}</Button>
          {/each}
        {/snippet}
      </Banner>
    {/if}

    <section class="hero {picture.tone}">
      <!-- The state is told above the globe, in the globe's own colours: the
           word, the ball, its glow and the ring of the button read as one. -->
      <header class="status">
        <h1>{picture.word}</h1>
        {#if picture.facts}
          <p class="facts">
            {#each picture.facts as fact, i (i)}<span>{fact}</span>{/each}
          </p>
        {:else}
          <p title={picture.sub}>{picture.sub}</p>
        {/if}
      </header>

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
          <h3>
            {t.quick.mode}
            <button type="button" class="about" aria-label={t.quick.modeAbout} title={t.quick.modeAbout} onclick={() => (aboutMode = true)}>
              <Icon name="info" size={14} />
            </button>
          </h3>
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
  </div>

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

{#if aboutMode}
  <ModeDialog
    mode={s.vpn.mode}
    onmode={(mode) => daemon.setMode(mode)}
    onrules={() => {
      aboutMode = false;
      onrules();
    }}
    onclose={() => (aboutMode = false)}
  />
{/if}

<style>
  .home {
    display: flex;
    flex-direction: column;
    min-height: 100%;
  }

  /* The night is the whole pane, edge to edge, with nothing framing it.
     Its padding is the one the other screens get from <main>. */
  .sky {
    position: relative;
    isolation: isolate;
    overflow: hidden;
    flex: 1;
    display: flex;
    flex-direction: column;
    gap: var(--s4);
    padding: 22px 26px var(--s4);
    background: var(--sky);
  }

  .sky > :global(canvas) {
    z-index: -1;
  }

  .grow {
    flex: 1;
    min-width: 0;
  }

  @keyframes pulse {
    50% {
      opacity: 0.35;
    }
  }

  /* The two colours of the state, the same pair the globe is washed with.
     Registered, so that the word and the glow drift to a new state as the
     globe does, instead of jumping. */
  @property --a {
    syntax: '<color>';
    inherits: true;
    initial-value: #808080;
  }

  @property --b {
    syntax: '<color>';
    inherits: true;
    initial-value: #808080;
  }

  /* The globe takes what the window has left: its side follows the height
     of the window, and gives way when the window is narrow. */
  .hero {
    --globe: clamp(200px, min(100vh - 330px, 100vw - 580px), 380px);
    --a: var(--globe-off-a);
    --b: var(--globe-off-b);
    flex: 1;
    display: grid;
    grid-template-columns: minmax(150px, 1fr) var(--globe) minmax(220px, 1fr);
    grid-template-rows: auto 1fr;
    align-items: center;
    gap: 0 var(--s4);
    min-height: 280px;
    transition:
      --a 350ms ease,
      --b 350ms ease;
  }

  .hero.ok {
    --a: var(--globe-ok-a);
    --b: var(--globe-ok-b);
  }

  .hero.busy,
  .hero.warn {
    --a: var(--globe-warn-a);
    --b: var(--globe-warn-b);
  }

  .hero.bad {
    --a: var(--globe-bad-a);
    --b: var(--globe-bad-b);
  }

  .strip .hero {
    --globe: clamp(200px, min(100vh - 392px, 100vw - 580px), 380px);
  }

  .status {
    grid-column: 1 / -1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 2px;
    min-width: 0;
    text-align: center;
  }

  /* The word is painted with the state's gradient: it is large enough that
     3:1 is the bar, and every pair of --globe-* clears it on --sky. */
  h1 {
    max-width: 100%;
    padding: 0 2px;
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
    background: linear-gradient(100deg, var(--a), var(--b)) 0 0 / 100% 100%;
    background-clip: text;
    color: transparent;
  }

  /* While something is on its way the light runs along the word, at the
     pace the globe's continents breathe. */
  .busy h1 {
    background-image: linear-gradient(100deg, var(--a) 20%, var(--b) 50%, var(--a) 80%);
    background-size: 200% 100%;
    animation: shimmer 2.4s linear infinite;
  }

  @keyframes shimmer {
    to {
      background-position: -200% 0;
    }
  }

  .status p {
    max-width: min(100%, 560px);
    color: var(--muted);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .facts {
    display: flex;
    flex-wrap: wrap;
    justify-content: center;
  }

  .facts span + span::before {
    content: '·';
    margin: 0 7px;
    opacity: 0.6;
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

  /* The air round the ball glows with the state; the ball itself covers
     the middle of the glow, so only a rim of light is left round it. */
  .disc::before {
    content: '';
    position: absolute;
    inset: -12%;
    z-index: -1;
    border-radius: 50%;
    background: radial-gradient(
      closest-side,
      color-mix(in srgb, var(--a) 30%, transparent) 62%,
      color-mix(in srgb, var(--b) 10%, transparent) 78%,
      transparent
    );
  }

  :global([data-theme='light']) .disc::before {
    opacity: 0.6;
  }

  .busy .disc::before {
    animation: breathe 2.4s ease-in-out infinite;
  }

  @keyframes breathe {
    50% {
      opacity: 0.45;
    }
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

  /* The ring is the colour of the state, as the word above the globe is; the
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
    display: flex;
    align-items: center;
    gap: 4px;
    min-height: 18px;
    margin-bottom: 6px;
    color: var(--muted);
    font-size: var(--fs-cap);
    font-weight: 400;
  }

  .about {
    display: grid;
    place-items: center;
    width: 20px;
    height: 20px;
    margin: -1px 0;
    padding: 0;
    border: 0;
    border-radius: 4px;
    background: transparent;
    color: var(--muted);
  }

  .about:hover {
    background: var(--surface-2);
    color: var(--text);
  }

  /* The smallest window (820x560) with an incident strip: everything still
     fits without scrolling if the journal gives up its third line. */
  @media (max-height: 620px) {
    .sky {
      gap: var(--s3);
      padding-top: 14px;
      padding-bottom: var(--s3);
    }

    .log {
      padding-bottom: 10px;
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

  /* The journal is the ground under the sky: the window's own colour,
     a hairline between them. */
  .log {
    padding: var(--s3) 26px 14px;
    border-top: 1px solid var(--line);
    background: var(--bg);
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
