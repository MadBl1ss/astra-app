<script lang="ts" module>
  // Which programs are unfolded survives leaving the screen.
  const open = $state<Record<string, boolean>>({});
</script>

<script lang="ts">
  import Empty from '../../components/Empty.svelte';
  import Field from '../../components/Field.svelte';
  import Icon from '../../components/Icon.svelte';
  import { bytes, speed } from '../../format';
  import { ru } from '../../i18n/ru';
  import type { Connection, RuleListName } from '../../proto';
  import { appLines, pinned, type RuleRef, type Route } from '../../rules';

  // Open connections, gathered by program. A row offers the other path in
  // one click: a program that goes directly gets "Через VPN" and back. An
  // unfolded program lists its addresses with the same button for the site.
  let {
    connections,
    vpnOn,
    onrule,
  }: {
    /** null: the first list has not come yet. */
    connections: Connection[] | null;
    vpnOn: boolean;
    onrule: (list: RuleListName, rule: RuleRef, what: string) => void;
  } = $props();

  const t = ru.rules;
  let query = $state('');

  // Bytes per second per connection, from the difference of two lists.
  let previous = new Map<string, number>();
  let previousAt = 0;
  let rates = $state.raw<Map<string, number> | null>(null);
  $effect(() => {
    const list = connections;
    if (!list) return;
    const at = performance.now();
    const seconds = previousAt ? Math.min(Math.max((at - previousAt) / 1000, 0.5), 10) : 0;
    const next = new Map<string, number>();
    const now = new Map<string, number>();
    for (const c of list) {
      const total = c.up + c.down;
      now.set(c.id, total);
      if (seconds) next.set(c.id, Math.max(0, total - (previous.get(c.id) ?? 0)) / seconds);
    }
    previous = now;
    previousAt = at;
    if (seconds) rates = next;
  });

  const lines = $derived(connections ? appLines(connections, rates, query) : []);
  const searching = $derived(query.trim() !== '');

  function routeWord(route: Route): string {
    return route === 'vpn' ? t.routeVpn : route === 'direct' ? t.routeDirect : t.routeMixed;
  }

  /** The list a row's button adds to: the path it does not take now. */
  function target(route: Route): RuleListName {
    return route === 'vpn' ? 'direct' : 'vpn';
  }
</script>

<section class="live" aria-labelledby="live-title">
  <header>
    <h2 id="live-title">{t.live}</h2>
    {#if connections && connections.length > 0}<span class="n">{t.liveCount(connections.length)}</span>{/if}
  </header>
  <Field label={t.liveFilter} hideLabel icon="search" placeholder={t.liveFilter} bind:value={query} />

  <div class="list">
    {#if !vpnOn}
      <Empty compact icon="route" title={t.liveOff} />
    {:else if connections === null}
      <Empty compact icon="route" title={t.liveWaiting} />
    {:else if connections.length === 0}
      <Empty compact icon="route" title={t.liveEmpty} />
    {:else if lines.length === 0}
      <Empty compact icon="search" title={t.liveNothing} />
    {:else}
      <ul>
        {#each lines as app (app.key)}
          {@const name = app.name || t.noProcess}
          {@const unfolded = open[app.key] || searching}
          {@const to = target(app.route)}
          <li class="app">
            <div class="line">
              <button
                type="button"
                class="fold"
                aria-expanded={unfolded}
                aria-label={unfolded ? t.fold(name) : t.unfold(name)}
                onclick={() => (open[app.key] = !open[app.key])}
              >
                <span class="chev" class:unfolded><Icon name="chevron" size={13} /></span>
                <span class="text">
                  <span class="name" class:muted={!app.name}>{name}</span>
                  <span class="meta">
                    {t.liveCount(app.conns)}{#if app.rate !== null}{' · '}{t.rate(speed(app.rate))}{/if}
                  </span>
                </span>
              </button>
              <span class="route {app.route}">{routeWord(app.route)}</span>
              {#if app.name && !pinned(app.name)}
                <button
                  type="button"
                  class="act"
                  title={to === 'vpn' ? t.toVpnFor(app.name) : t.toDirectFor(app.name)}
                  aria-label={to === 'vpn' ? t.toVpnFor(app.name) : t.toDirectFor(app.name)}
                  onclick={() => onrule(to, { kind: 'process', value: app.name }, app.name)}
                >
                  {to === 'vpn' ? t.toVpn : t.toDirect}
                </button>
              {:else}
                <span class="act-gap" title={app.name ? t.pinned : undefined}></span>
              {/if}
            </div>
            {#if unfolded}
              <ul class="hosts">
                {#each app.hosts as host (host.key)}
                  {@const hostTo = target(host.route)}
                  <li class="host">
                    <span class="label mono" title={host.label}>{host.label}</span>
                    <span class="hmeta">{host.conns > 1 ? `×${host.conns} · ` : ''}{bytes(host.bytes)}</span>
                    <span class="route {host.route}">{routeWord(host.route)}</span>
                    {#if host.rule && !pinned(app.name)}
                      <button
                        type="button"
                        class="act"
                        title={hostTo === 'vpn' ? t.toVpnFor(host.rule.value) : t.toDirectFor(host.rule.value)}
                        aria-label={hostTo === 'vpn' ? t.toVpnFor(host.rule.value) : t.toDirectFor(host.rule.value)}
                        onclick={() => onrule(hostTo, host.rule!, host.rule!.value)}
                      >
                        {hostTo === 'vpn' ? t.toVpn : t.toDirect}
                      </button>
                    {:else}
                      <span class="act-gap"></span>
                    {/if}
                  </li>
                {/each}
              </ul>
            {/if}
          </li>
        {/each}
      </ul>
    {/if}
  </div>
</section>

<style>
  .live {
    display: flex;
    flex-direction: column;
    gap: var(--s2);
    min-height: 0;
  }

  header {
    display: flex;
    align-items: baseline;
    gap: var(--s2);
  }

  h2 {
    font-size: var(--fs-head);
    font-weight: 600;
  }

  .n {
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .list {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
    overscroll-behavior: contain;
  }

  ul {
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .app + .app {
    border-top: 1px solid var(--line);
  }

  .line {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 44px;
    padding: 0 8px 0 2px;
  }

  .fold {
    display: flex;
    flex: 1;
    align-items: center;
    gap: 4px;
    min-width: 0;
    padding: 4px 4px;
    border: 0;
    border-radius: var(--r-ctl);
    background: transparent;
    text-align: left;
  }

  .fold:focus-visible {
    outline-offset: -2px;
  }

  .chev {
    display: flex;
    flex: none;
    color: var(--muted);
    transform: rotate(-90deg);
    transition: transform var(--fast);
  }

  .chev.unfolded {
    transform: none;
  }

  .text {
    display: grid;
    min-width: 0;
  }

  .name,
  .meta,
  .label {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  .muted {
    color: var(--muted);
  }

  .meta,
  .hmeta {
    color: var(--muted);
    font-size: var(--fs-cap);
  }

  .route {
    flex: none;
    width: 58px;
    font-size: var(--fs-cap);
    text-align: right;
  }

  /* The word says the path; the colour repeats the two paths of Home. */
  .route.vpn {
    color: var(--vpn);
  }

  .route.direct {
    color: var(--direct);
  }

  .route.mixed {
    color: var(--warn);
  }

  .act {
    flex: none;
    min-width: 74px;
    min-height: 24px;
    padding: 1px 6px;
    border: 1px solid var(--line);
    border-radius: var(--r-ctl);
    background: var(--surface-2);
    font-size: var(--fs-cap);
    white-space: nowrap;
  }

  .act:hover {
    border-color: var(--muted);
  }

  .act-gap {
    flex: none;
    width: 74px;
  }

  .hosts {
    padding: 0 8px 6px 24px;
  }

  .host {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 30px;
    border-top: 1px dashed var(--line);
  }

  .label {
    flex: 1;
    min-width: 0;
    font-size: var(--fs-cap);
  }

  .hmeta {
    flex: none;
  }
</style>
