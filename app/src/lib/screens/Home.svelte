<script lang="ts">
  import Banner from '../components/Banner.svelte';
  import Button from '../components/Button.svelte';
  import Card from '../components/Card.svelte';
  import Hint from '../components/Hint.svelte';
  import Icon from '../components/Icon.svelte';
  import ListRow from '../components/ListRow.svelte';
  import Segments from '../components/Segments.svelte';
  import Select, { type SelectItem } from '../components/Select.svelte';
  import Switch from '../components/Switch.svelte';
  import Tracks from '../components/Tracks.svelte';
  import { clock, speed } from '../format';
  import { ru } from '../i18n/ru';
  import type { Mode, Server, State } from '../proto';
  import { daemon, type PathRate } from '../store.svelte';
  import {
    banner,
    countryOf,
    describe,
    displayName,
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

  // Speeds by path come from the list of connections, which is only worth
  // asking for while the window is actually looked at.
  let visible = $state(document.visibilityState === 'visible');
  $effect(() => {
    const update = () => (visible = document.visibilityState === 'visible');
    document.addEventListener('visibilitychange', update);
    return () => document.removeEventListener('visibilitychange', update);
  });
  $effect(() => {
    if (visible) return daemon.watchConnections();
  });

  // Until the first two lists arrive only the counts from State are known.
  const vpnPath = $derived<PathRate | null>(daemon.rates?.vpn ?? null);
  const directPath = $derived<PathRate | null>(daemon.rates?.direct ?? null);

  function figures(conns: number, rate: PathRate | null): string {
    if (conns === 0 && (rate === null || rate.down + rate.up < 512)) return t.tracks.idle;
    const parts = [t.tracks.conns(conns)];
    if (rate) parts.push(t.tracks.down(speed(rate.down)), t.tracks.up(speed(rate.up)));
    return parts.join(' · ');
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
    sortedServers(s).map((x) => ({ value: x.id, label: displayName(x), badge: countryOf(x), ...pingText(x) })),
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
    <Switch checked={picture.wantOn} label={t.state.switchLabel} onchange={() => daemon.toggleVpn()} />
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

  <Card flush>
    <Tracks
      vpn={picture.vpnRail}
      direct={picture.directRail}
      gate={picture.gate}
      tone={picture.tone}
      server={server ? displayName(server) : t.tracks.noServer}
      {vpnStat}
      {directStat}
      vpnRate={vpnPath ? vpnPath.down + vpnPath.up : null}
      directRate={directPath ? directPath.down + directPath.up : null}
    />
  </Card>

  <div class="quick">
    <Card caption={t.quick.server}>
      <Select
        items={serverItems}
        value={s.server.selected}
        label={t.quick.server}
        placeholder={s.servers.length === 0 ? t.quick.serverEmpty : t.quick.serverPick}
        disabled={s.servers.length === 0}
        onchange={(id) => daemon.selectServer(id)}
      />
    </Card>

    <Card caption={t.quick.mode}>
      <Segments options={modes} value={s.vpn.mode} label={t.quick.mode} onchange={(mode) => daemon.setMode(mode)} />
    </Card>

    <Card caption={t.quick.guard}>
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
    </Card>
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

  .quick {
    display: grid;
    grid-template-columns: 1.25fr 1.3fr 1fr;
    gap: 10px;
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
