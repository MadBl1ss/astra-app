<script lang="ts" module>
  // Which groups are folded survives leaving the screen, not the app.
  const folded = $state<Record<string, boolean>>({});
</script>

<script lang="ts">
  import { tick, untrack } from 'svelte';
  import Button from '../components/Button.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Empty from '../components/Empty.svelte';
  import Field from '../components/Field.svelte';
  import Hint from '../components/Hint.svelte';
  import type { MenuItem } from '../components/Menu.svelte';
  import { ru } from '../i18n/ru';
  import type { Server, State, Sub } from '../proto';
  import { bestOf, fastIds } from '../rating';
  import { freshIn, progressOf, type Round } from '../rounds';
  import { favoritesOf, groupServers, moved, pingOf, subFacts, type Group } from '../servers';
  import { daemon } from '../store.svelte';
  import { displayName } from '../view';
  import AddDialog from './servers/AddDialog.svelte';
  import BestStrip from './servers/BestStrip.svelte';
  import GroupHeader from './servers/GroupHeader.svelte';
  import ServerRow from './servers/ServerRow.svelte';
  import SpeedDialog, { type Preset } from './servers/SpeedDialog.svelte';

  let { state: s }: { state: State } = $props();

  const t = ru.servers;
  const ROW = 36;

  let query = $state('');
  // Pings say "checked 5 min ago": a clock that moves once a minute is
  // enough, and 200 rows do not redraw every second.
  const minute = $derived(Math.floor(daemon.now / 60_000) * 60_000);

  // ---- favourites and their order ----

  // A new order shows at once and is held until the daemon's State agrees.
  let order = $state<string[] | null>(null);
  const favorites = $derived.by(() => {
    const fromState = favoritesOf(s);
    if (!order) return fromState;
    const byId = new Map(fromState.map((x) => [x.id, x]));
    const local = order.map((id) => byId.get(id)).filter((x): x is Server => x !== undefined);
    return local.length === fromState.length ? local : fromState;
  });
  $effect(() => {
    if (order && favoritesOf(s).map((x) => x.id).join() === order.join()) order = null;
  });

  async function reorder(ids: string[], focusId?: string): Promise<void> {
    order = ids;
    if (focusId) {
      await tick();
      document.querySelector<HTMLButtonElement>(`.pick[data-id="${CSS.escape(focusId)}"]`)?.focus();
      const at = ids.indexOf(focusId);
      const server = s.servers.find((x) => x.id === focusId);
      if (server) announce = t.moved(displayName(server), at + 1);
    }
    const done = await daemon.rpc('server.reorder', { ids });
    if (done === undefined) order = null;
  }

  let announce = $state('');

  function moveKey(event: KeyboardEvent, server: Server): void {
    if (!event.altKey || (event.key !== 'ArrowUp' && event.key !== 'ArrowDown')) return;
    event.preventDefault();
    const ids = favorites.map((x) => x.id);
    const from = ids.indexOf(server.id);
    const to = from + (event.key === 'ArrowUp' ? -1 : 1);
    if (from < 0 || to < 0 || to >= ids.length) return;
    void reorder(moved(ids, from, to), server.id);
  }

  // Dragging by the grip: the row follows the pointer, the others make room.
  let drag = $state<{ id: string; from: number; to: number; y0: number; dy: number } | null>(null);

  function startDrag(event: PointerEvent, id: string, index: number): void {
    if (event.button !== 0) return;
    event.preventDefault();
    (event.currentTarget as Element).setPointerCapture(event.pointerId);
    drag = { id, from: index, to: index, y0: event.clientY, dy: 0 };
  }

  function moveDrag(event: PointerEvent): void {
    if (!drag) return;
    const count = favorites.length;
    const dy = Math.max(-drag.from * ROW, Math.min((count - 1 - drag.from) * ROW, event.clientY - drag.y0));
    drag = { ...drag, dy, to: Math.max(0, Math.min(count - 1, drag.from + Math.round(dy / ROW))) };
  }

  function endDrag(): void {
    if (!drag) return;
    const { from, to } = drag;
    drag = null;
    if (from !== to) void reorder(moved(favorites.map((x) => x.id), from, to));
  }

  function shiftOf(index: number, id: string): number {
    if (!drag) return 0;
    if (id === drag.id) return drag.dy;
    if (drag.from < drag.to && index > drag.from && index <= drag.to) return -ROW;
    if (drag.from > drag.to && index < drag.from && index >= drag.to) return ROW;
    return 0;
  }

  // ---- groups ----

  const groups = $derived(groupServers(s, favorites, query));
  const empty = $derived(s.servers.length === 0 && s.subs.length === 0);

  function titleOf(group: Group): string {
    if (group.kind === 'favorites') return t.favorites;
    if (group.kind === 'links') return t.links;
    return group.sub!.name;
  }

  function menuOf(sub: Sub): MenuItem[] {
    return [
      { label: t.subUpdate, icon: 'refresh', disabled: busy !== null, onselect: () => daemon.rpc('subs.update', { id: sub.id }) },
      { label: t.subRename, icon: 'edit', onselect: () => (dialog = { kind: 'rename', sub, name: sub.name, error: null }) },
      { label: t.subRemove, icon: 'trash', danger: true, onselect: () => (dialog = { kind: 'removeSub', sub }) },
    ];
  }

  // ---- what is going on ----

  const busy = $derived(s.busy === 'testing' || s.busy === 'updating_subs' || s.busy === 'measuring' ? s.busy : null);
  // With the VPN on the test goes through it; off or failed, the daemon
  // brings an engine of its own. Only a VPN on its way up or down waits.
  const phaseLets = $derived(s.vpn.phase === 'on' || s.vpn.phase === 'off' || s.vpn.phase === 'failed');
  const canTest = $derived(phaseLets && s.servers.length > 0 && busy === null);
  const testNote = $derived(
    s.test ? t.testStopNote : !phaseLets ? t.testWait : s.vpn.phase === 'on' ? t.testViaVpn(s.servers.length) : t.testWithoutVpn(s.servers.length),
  );

  function testAll(): void {
    // Top to bottom as the list stands, search or not: the groups on top
    // answer first.
    const ids = groupServers(s, favorites, '').flatMap((g) => g.servers.map((x) => x.id));
    void daemon.rpc('server.test', ids.length > 0 ? { ids } : {});
  }

  function stopTest(): void {
    void daemon.rpc('server.testStop', undefined);
  }

  // ---- the test as it goes ----

  // The round outlives State.test by LINGER_MS: its outcome stays on the
  // groups and under the header long enough to be read, then fades.
  const LINGER_MS = 4000;
  let round = $state<Round | null>(null);
  $effect(() => {
    const test = s.test;
    const ids = test ? s.servers.filter((x) => x.check !== null).map((x) => x.id) : [];
    untrack(() => {
      if (test) {
        if (!round || round.started !== test.started || round.ended !== null) {
          round = { started: test.started, own: test.own, ids: new Set(ids), ended: null };
        }
      } else if (round && round.ended === null) {
        round = { ...round, ended: Date.now() };
      }
    });
  });
  $effect(() => {
    if (round?.ended == null) return;
    const timer = setTimeout(() => (round = null), LINGER_MS);
    return () => clearTimeout(timer);
  });

  /** The line under the header: the daemon's numbers live, the outcome after. */
  const overall = $derived.by(() => {
    if (s.test) {
      const { stage, own, done, total, alive } = s.test;
      return { preparing: stage === 'preparing', own, done, total, alive, ended: false };
    }
    const p = progressOf(s.servers, round);
    if (!round || !p) return null;
    return { preparing: false, own: round.own, done: p.done, total: p.total, alive: p.alive, ended: true };
  });

  function updateAll(): void {
    void daemon.rpc('subs.update', {});
  }

  // ---- the rating and the speed test ----

  // The cards on top: from two servers with a score worth trusting.
  const best = $derived(bestOf(s.servers, 3));
  const crowned = $derived(best[0]?.id ?? null);
  const fast = $derived(fastIds(s.servers));
  const canMeasure = $derived(phaseLets && busy === null);

  function measure(preset: Preset, picked: string[] = [], autostart = false): void {
    dialog = { kind: 'speed', preset, picked, autostart };
  }

  /** Opens the dialog of the test under way, or a new one on the server in use. */
  function openSpeed(): void {
    measure(s.speed ? 'picked' : 'current', s.speed?.ids ?? []);
  }

  // ---- dialogs ----

  type DialogState =
    | { kind: 'add' }
    | { kind: 'rename'; sub: Sub; name: string; error: string | null }
    | { kind: 'removeSub'; sub: Sub }
    | { kind: 'removeServer'; server: Server }
    | { kind: 'speed'; preset: Preset; picked: string[]; autostart: boolean }
    | null;
  let dialog = $state<DialogState>(null);
  let working = $state(false);

  async function rename(): Promise<void> {
    if (dialog?.kind !== 'rename') return;
    const name = dialog.name.trim();
    if (!name) {
      dialog.error = ru.renameDialog.empty;
      return;
    }
    working = true;
    try {
      await daemon.call('subs.rename', { id: dialog.sub.id, name });
      dialog = null;
    } catch (err) {
      if (dialog?.kind === 'rename') dialog.error = (err as Error).message;
    } finally {
      working = false;
    }
  }

  async function remove(): Promise<void> {
    if (dialog?.kind !== 'removeSub' && dialog?.kind !== 'removeServer') return;
    const call =
      dialog.kind === 'removeSub'
        ? daemon.rpc('subs.remove', { id: dialog.sub.id })
        : daemon.rpc('server.remove', { id: dialog.server.id });
    working = true;
    await call;
    working = false;
    dialog = null;
  }

  async function added(id: string): Promise<void> {
    // A new subscription folds nothing and is scrolled to; a new server
    // gets the focus on its row.
    query = '';
    await tick();
    const head = document.getElementById(`group-${id}`);
    const row = document.querySelector<HTMLButtonElement>(`.pick[data-id="${CSS.escape(id)}"]`);
    (head ?? row)?.scrollIntoView({ block: 'nearest' });
    row?.focus();
  }
</script>

<div class="screen">
  <header class="top">
    <h1>{ru.nav.servers}</h1>
    <div class="tools">
      <Field label={t.search} hideLabel icon="search" placeholder={t.searchPlaceholder} bind:value={query} width="180px" />
      <Hint text={testNote} side="bottom">
        {#snippet children(describedby)}
          <span aria-describedby={describedby}>
            {#if s.test}
              <Button icon="halt" onclick={stopTest}>
                {t.testStop}
                <span class="count">{s.test.done}/{s.test.total}</span>
              </Button>
            {:else}
              <Button icon="pulse" disabled={!canTest} onclick={testAll}>{t.testAll}</Button>
            {/if}
          </span>
        {/snippet}
      </Hint>
      <Hint text={s.speed ? ru.speed.openRunning(s.speed.results.length, s.speed.ids.length) : ru.speed.openNote} side="bottom">
        {#snippet children(describedby)}
          <span aria-describedby={describedby} class="speedbtn" class:running={s.speed !== null}>
            <!-- Under way, the count takes the word's place: the header has
                 no room for both, and the breathing border says the rest. -->
            <Button icon="gauge" disabled={!s.speed && (!canMeasure || s.servers.length === 0)} onclick={openSpeed}>
              {#if s.speed}<span class="num">{s.speed.results.length}/{s.speed.ids.length}</span>{:else}{ru.speed.open}{/if}
            </Button>
          </span>
        {/snippet}
      </Hint>
      <Hint text={t.updateSubs} side="bottom">
        {#snippet children(describedby)}
          <span aria-describedby={describedby}>
            <Button icon="refresh" busy={busy === 'updating_subs'} disabled={s.subs.length === 0 || busy !== null} onclick={updateAll}>
              {busy === 'updating_subs' ? t.updating : t.update}
            </Button>
          </span>
        {/snippet}
      </Hint>
      <Button icon="plus" onclick={() => (dialog = { kind: 'add' })}>{t.add}</Button>
    </div>
  </header>
  <div
    class="progress"
    class:on={busy === 'updating_subs' || overall?.preparing}
    class:ended={overall?.ended}
    aria-hidden="true"
  >
    {#if overall && !overall.preparing}
      <span class="fill" class:full={overall.ended && overall.done === overall.total} style:width="{overall.total ? (overall.done / overall.total) * 100 : 0}%"></span>
    {/if}
  </div>
  {#if overall}
    <p class="testline" class:ended={overall.ended}>
      {#if overall.preparing}
        <span>{t.testPreparing}</span>
      {:else if overall.ended}
        <span role="status">{overall.done === overall.total ? t.testFinished(overall.alive, overall.total) : t.testHalted(overall.done, overall.total)}</span>
      {:else}
        <span class="num">{t.testProgress(overall.done, overall.total)}</span>
        <span class="sep" aria-hidden="true">·</span>
        <span class="num">{t.testAlive(overall.alive)}</span>
      {/if}
      <span class="via">{overall.own ? t.testOwn : t.testThroughVpn}</span>
    </p>
  {/if}

  <div class="list" onpointermove={moveDrag} onpointerup={endDrag} onpointercancel={endDrag} role="presentation">
    {#if empty}
      <Empty icon="servers" title={t.empty} text={t.emptyText}>
        <Button icon="plus" onclick={() => (dialog = { kind: 'add' })}>{t.add}</Button>
      </Empty>
    {:else if groups.length === 0 || groups.every((g) => g.servers.length === 0 && g.kind !== 'sub')}
      <Empty icon="search" title={t.nothingFound} text={t.nothingFoundText} />
    {:else}
      {#if !query && best.length >= 2}
        <BestStrip
          servers={best}
          selected={s.server.selected}
          folded={folded['best'] ?? false}
          measureDisabled={!canMeasure}
          onselect={(id) => daemon.selectServer(id)}
          onmeasure={() => measure('best')}
          onfold={() => (folded['best'] = !(folded['best'] ?? false))}
        />
      {/if}
      {#each groups as group (group.key)}
        {@const isFolded = folded[group.key] ?? false}
        {@const listId = `list-${group.key}`}
        <section class="group" id="group-{group.key}">
          <GroupHeader
            title={titleOf(group)}
            facts={group.sub ? subFacts(group.sub, group.starred, minute) : null}
            error={group.sub?.error ?? null}
            note={group.kind === 'favorites' && !isFolded && favorites.length > 1 ? t.favoritesNote : null}
            progress={progressOf(group.servers, round)}
            folded={isFolded}
            list={listId}
            menu={group.sub ? menuOf(group.sub) : []}
            onfold={() => (folded[group.key] = !isFolded)}
          />
          {#if !isFolded && group.sub && group.sub.servers === 0}
            {@const sub = group.sub}
            <!-- Nothing to list: the two things worth doing are right here,
                 not only in the menu. -->
            <div id={listId} class="vacant">
              <p>{t.emptySub}</p>
              <Button variant="ghost" icon="refresh" disabled={busy !== null} onclick={() => daemon.rpc('subs.update', { id: sub.id })}>
                {t.subUpdate}
              </Button>
              <Button variant="danger" icon="trash" onclick={() => (dialog = { kind: 'removeSub', sub })}>{t.subRemove}</Button>
            </div>
          {:else if !isFolded && group.servers.length > 0}
            <ul id={listId} class="rows">
              {#each group.servers as server, i (server.id)}
                <ServerRow
                  {server}
                  selected={server.id === s.server.selected}
                  ping={pingOf(server, s, minute)}
                  check={server.check}
                  fresh={freshIn(server, round)}
                  grip={group.kind === 'favorites' && !query && favorites.length > 1}
                  shift={group.kind === 'favorites' ? shiftOf(i, server.id) : 0}
                  dragging={drag?.id === server.id}
                  best={server.id === crowned}
                  fast={fast.has(server.id)}
                  measuring={s.speed?.current === server.id ? s.speed.phase : null}
                  now={minute}
                  onmeasure={canMeasure ? () => measure('picked', [server.id], true) : undefined}
                  onselect={() => daemon.selectServer(server.id)}
                  onstar={() => daemon.rpc('server.favorite', { id: server.id, favorite: !server.favorite })}
                  onremove={server.sub === '' ? () => (dialog = { kind: 'removeServer', server }) : undefined}
                  ongrip={(event) => startDrag(event, server.id, i)}
                  onkeydown={group.kind === 'favorites' ? (event) => moveKey(event, server) : undefined}
                />
              {/each}
            </ul>
          {/if}
        </section>
      {/each}
    {/if}
  </div>
  <p class="sr-only" aria-live="polite">{announce}</p>
</div>

{#if dialog?.kind === 'add'}
  <AddDialog onclose={() => (dialog = null)} onadded={added} />
{:else if dialog?.kind === 'speed'}
  <SpeedDialog state={s} preset={dialog.preset} picked={dialog.picked} autostart={dialog.autostart} onclose={() => (dialog = null)} />
{:else if dialog?.kind === 'rename'}
  <Dialog title={ru.renameDialog.title} onclose={() => (dialog = null)}>
    <Field label={ru.renameDialog.name} bind:value={dialog.name} error={dialog.error} disabled={working} onenter={rename} />
    {#snippet actions()}
      <Button variant="ghost" onclick={() => (dialog = null)}>{ru.addDialog.cancel}</Button>
      <Button busy={working} onclick={rename}>{ru.renameDialog.save}</Button>
    {/snippet}
  </Dialog>
{:else if dialog?.kind === 'removeSub' || dialog?.kind === 'removeServer'}
  {@const d = dialog}
  <Dialog
    title={d.kind === 'removeSub' ? ru.removeDialog.sub(d.sub.name) : ru.removeDialog.server(displayName(d.server))}
    onclose={() => (dialog = null)}
  >
    <p class="body">{d.kind === 'removeSub' ? ru.removeDialog.subBody(d.sub.servers) : ru.removeDialog.serverBody}</p>
    {#snippet actions()}
      <Button variant="ghost" onclick={() => (dialog = null)}>{ru.addDialog.cancel}</Button>
      <Button variant="danger" icon="trash" busy={working} onclick={remove}>{ru.removeDialog.confirm}</Button>
    {/snippet}
  </Dialog>
{/if}

<style>
  .screen {
    display: flex;
    flex-direction: column;
    height: 100%;
    min-height: 0;
  }

  .top {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--s2) var(--s4);
    padding-bottom: var(--s3);
  }

  h1 {
    flex: 1;
    font: 600 var(--fs-state) / 1.15 var(--font-display);
    letter-spacing: -0.01em;
  }

  .tools {
    display: flex;
    align-items: center;
    gap: var(--s2);
  }

  /* A thin moving line while subscriptions update or the test's own engine
     comes up: there is no honest per-cent for those. A test fills the line
     as servers answer, with a light running along what is filled, and the
     line fades with the outcome. */
  .progress {
    position: relative;
    flex: none;
    height: 2px;
    overflow: hidden;
    border-radius: 1px;
  }

  .fill {
    position: absolute;
    inset: 0 auto 0 0;
    border-radius: 1px;
    background: color-mix(in srgb, var(--text) 55%, transparent);
    transition: width 0.3s ease-out;
  }

  .fill::after {
    content: '';
    position: absolute;
    inset: 0;
    background: linear-gradient(90deg, transparent, var(--text), transparent) no-repeat;
    background-size: 40% 100%;
    animation: shine 1.6s ease-in-out infinite;
  }

  .fill.full,
  .ended .fill::after {
    animation: none;
    background: none;
  }

  .fill.full {
    background: color-mix(in srgb, var(--text) 55%, transparent);
  }

  @keyframes shine {
    from {
      background-position: -60% 0;
    }
    to {
      background-position: 160% 0;
    }
  }

  .progress.ended,
  .testline.ended {
    animation: linger 4s ease-in forwards;
  }

  @keyframes linger {
    0%,
    75% {
      opacity: 1;
    }
    100% {
      opacity: 0;
    }
  }

  .testline {
    display: flex;
    align-items: center;
    gap: 6px;
    min-height: 22px;
    padding-top: 4px;
    color: var(--muted);
    font-size: var(--fs-cap);
    animation: drop var(--slow) ease-out;
  }

  .testline.ended {
    color: var(--text);
  }

  .num,
  .count {
    font-variant-numeric: tabular-nums;
  }

  .count {
    color: var(--muted);
  }

  /* A speed test under way with its window closed: the button breathes. */
  .speedbtn.running :global(.btn) {
    border-color: color-mix(in srgb, var(--vpn) 55%, var(--line));
    animation: breathe 1.8s ease-in-out infinite;
  }

  @keyframes breathe {
    50% {
      box-shadow: 0 0 0 3px color-mix(in srgb, var(--vpn) 18%, transparent);
    }
  }

  .via {
    margin-left: auto;
    padding: 0 7px;
    border: 1px solid var(--line);
    border-radius: 9px;
    line-height: 16px;
  }

  @keyframes drop {
    from {
      opacity: 0;
      transform: translateY(-4px);
    }
  }

  .progress.on::after {
    content: '';
    position: absolute;
    inset: 0 auto 0 0;
    width: 30%;
    border-radius: 1px;
    background: var(--muted);
    animation: slide 1.1s ease-in-out infinite;
  }

  @keyframes slide {
    from {
      transform: translateX(-100%);
    }
    to {
      transform: translateX(340%);
    }
  }

  .list {
    flex: 1;
    min-height: 0;
    margin-right: -14px;
    padding: var(--s2) 14px var(--s4) 0;
    overflow-y: auto;
    overscroll-behavior: contain;
  }

  .group {
    margin-bottom: var(--s3);
    /* Off-screen groups are not laid out: 200 rows scroll as lightly as 20. */
    content-visibility: auto;
    contain-intrinsic-size: auto 400px;
  }

  .rows {
    margin: 0;
    padding: 2px 0;
    list-style: none;
    border: 1px solid var(--line);
    border-radius: var(--r-card);
    background: var(--surface);
  }

  .vacant {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: var(--s2);
    padding: 10px 12px 10px 16px;
    border: 1px dashed var(--line);
    border-radius: var(--r-card);
  }

  .vacant p {
    flex: 1;
    min-width: 220px;
    color: var(--muted);
  }

  .body {
    color: var(--muted);
  }
</style>
