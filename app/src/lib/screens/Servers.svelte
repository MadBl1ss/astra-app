<script lang="ts" module>
  // Which groups are folded survives leaving the screen, not the app.
  const folded = $state<Record<string, boolean>>({});
</script>

<script lang="ts">
  import { tick } from 'svelte';
  import Button from '../components/Button.svelte';
  import Dialog from '../components/Dialog.svelte';
  import Empty from '../components/Empty.svelte';
  import Field from '../components/Field.svelte';
  import Hint from '../components/Hint.svelte';
  import type { MenuItem } from '../components/Menu.svelte';
  import { ru } from '../i18n/ru';
  import type { Server, State, Sub } from '../proto';
  import { favoritesOf, groupServers, moved, pingOf, subFacts, type Group } from '../servers';
  import { daemon } from '../store.svelte';
  import { displayName } from '../view';
  import AddDialog from './servers/AddDialog.svelte';
  import GroupHeader from './servers/GroupHeader.svelte';
  import ServerRow from './servers/ServerRow.svelte';

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

  const busy = $derived(s.busy === 'testing' || s.busy === 'updating_subs' ? s.busy : null);
  const canTest = $derived(s.vpn.phase === 'on' && s.servers.length > 0);

  function testAll(): void {
    void daemon.rpc('server.test', {});
  }

  function updateAll(): void {
    void daemon.rpc('subs.update', {});
  }

  // ---- dialogs ----

  type DialogState =
    | { kind: 'add' }
    | { kind: 'rename'; sub: Sub; name: string; error: string | null }
    | { kind: 'removeSub'; sub: Sub }
    | { kind: 'removeServer'; server: Server }
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
      <Field label={t.search} hideLabel icon="search" placeholder={t.searchPlaceholder} bind:value={query} width="210px" />
      <Hint text={canTest ? t.testingNote(s.servers.length) : t.testNeedsVpn} side="bottom">
        {#snippet children(describedby)}
          <span aria-describedby={describedby}>
            <Button icon="pulse" busy={busy === 'testing'} disabled={!canTest || busy !== null} onclick={testAll}>
              {busy === 'testing' ? t.testing : t.testAll}
            </Button>
          </span>
        {/snippet}
      </Hint>
      <Button icon="refresh" busy={busy === 'updating_subs'} disabled={s.subs.length === 0 || busy !== null} onclick={updateAll}>
        {busy === 'updating_subs' ? t.updating : t.updateSubs}
      </Button>
      <Button icon="plus" onclick={() => (dialog = { kind: 'add' })}>{t.add}</Button>
    </div>
  </header>
  <div class="progress" class:on={busy !== null} aria-hidden="true"></div>

  <div class="list" onpointermove={moveDrag} onpointerup={endDrag} onpointercancel={endDrag} role="presentation">
    {#if empty}
      <Empty icon="servers" title={t.empty} text={t.emptyText}>
        <Button icon="plus" onclick={() => (dialog = { kind: 'add' })}>{t.add}</Button>
      </Empty>
    {:else if groups.length === 0 || groups.every((g) => g.servers.length === 0 && g.kind !== 'sub')}
      <Empty icon="search" title={t.nothingFound} text={t.nothingFoundText} />
    {:else}
      {#each groups as group (group.key)}
        {@const isFolded = folded[group.key] ?? false}
        {@const listId = `list-${group.key}`}
        <section class="group" id="group-{group.key}">
          <GroupHeader
            title={titleOf(group)}
            facts={group.sub ? subFacts(group.sub, group.starred, minute) : null}
            error={group.sub?.error ?? null}
            note={group.kind === 'favorites' && !isFolded && favorites.length > 1 ? t.favoritesNote : null}
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
                  testing={busy === 'testing'}
                  grip={group.kind === 'favorites' && !query && favorites.length > 1}
                  shift={group.kind === 'favorites' ? shiftOf(i, server.id) : 0}
                  dragging={drag?.id === server.id}
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

  /* A thin moving line while a test or an update runs: their results come
     all at once at the end, so there is no honest per-cent to show. */
  .progress {
    position: relative;
    flex: none;
    height: 2px;
    overflow: hidden;
    border-radius: 1px;
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
